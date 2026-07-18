package auth

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/kkkzbh/AscendAny/backend/internal/pintia"
)

func (r *PostgresRepository) RegisterStudent(
	ctx context.Context,
	command RegisterStudentCommand,
) (RegisterStudentResult, error) {
	if err := validateRegisterStudentCommand(command); err != nil {
		return RegisterStudentResult{}, err
	}
	result := RegisterStudentResult{Status: RegistrationIdentityUnavailable}
	err := r.transaction(ctx, "register student account", func(tx postgresTx) error {
		if err := lockStudentAccountProvisioning(ctx, tx); err != nil {
			return err
		}
		if err := lockCurrentParticipantIdentity(ctx, tx); err != nil {
			return err
		}
		transactionNow, err := enrollmentTransactionTime(ctx, tx)
		if err != nil {
			return err
		}

		actorID, identityMatches, err := resolveCurrentRegistrationIdentity(
			ctx,
			tx,
			*command.Account.StudentNumber,
			*command.Account.PTANickname,
		)
		if err != nil {
			return err
		}
		if !identityMatches {
			return nil
		}

		usernameUnavailable, err := registrationUsernameUnavailableInTransaction(
			ctx,
			tx,
			command.Account.Username,
			transactionNow,
		)
		if err != nil {
			return err
		}
		if usernameUnavailable {
			result.Status = RegistrationUsernameUnavailable
			return nil
		}
		identityUnavailable, err := registrationActorUnavailableInTransaction(
			ctx,
			tx,
			actorID,
			*command.Account.StudentNumber,
			transactionNow,
		)
		if err != nil {
			return err
		}
		if identityUnavailable {
			return nil
		}

		var accountDatabaseID int64
		err = tx.QueryRow(ctx, `
INSERT INTO ascendany.auth_accounts (
    public_id,
    actor_id,
    username,
    password_phc,
    display_name,
    student_number,
    pta_nickname,
    role,
    auth_revision,
    created_at,
    updated_at
)
VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, 'student', 1, $8, $8)
ON CONFLICT DO NOTHING
RETURNING account_id`,
			command.Account.ID,
			actorID,
			command.Account.Username,
			command.Account.PasswordPHC,
			command.Account.DisplayName,
			*command.Account.StudentNumber,
			*command.Account.PTANickname,
			transactionNow,
		).Scan(&accountDatabaseID)
		if errors.Is(err, pgx.ErrNoRows) {
			usernameUnavailable, classifyErr := registrationUsernameUnavailableInTransaction(
				ctx,
				tx,
				command.Account.Username,
				transactionNow,
			)
			if classifyErr != nil {
				return classifyErr
			}
			if usernameUnavailable {
				result.Status = RegistrationUsernameUnavailable
				return nil
			}
			identityUnavailable, classifyErr := registrationActorUnavailableInTransaction(
				ctx,
				tx,
				actorID,
				*command.Account.StudentNumber,
				transactionNow,
			)
			if classifyErr != nil {
				return classifyErr
			}
			if identityUnavailable {
				return nil
			}
			return databaseFailure("classify student account conflict", errors.New("account insert conflicted outside username and student identity"))
		}
		if err != nil {
			return databaseFailure("insert registered student account", err)
		}

		refreshTTL := command.SessionExpiry.Sub(command.Now)
		refresh := command.RefreshToken
		refresh.CreatedAt = transactionNow
		refresh.ExpiresAt = transactionNow.Add(refreshTTL)
		sessionDatabaseID, err := insertSessionAndRefresh(
			ctx,
			tx,
			accountDatabaseID,
			command.Account.AuthRevision,
			command.SessionID,
			transactionNow,
			refresh.ExpiresAt,
			refresh,
		)
		if err != nil {
			return err
		}
		if err := appendAuditEvent(ctx, tx, accountDatabaseID, sessionDatabaseID, "auth.registration", transactionNow); err != nil {
			return err
		}
		account := command.Account
		result = RegisterStudentResult{
			Status:          StudentRegistered,
			Account:         account,
			AuthenticatedAt: transactionNow,
		}
		return nil
	})
	return result, err
}

// A registration nickname is the exact PTA user.nickname captured as
// display_name on the actor's most recently updated current logical-exam
// participant row. Historical snapshots and superseded logical-exam heads
// cannot authorize a registration.
func resolveCurrentRegistrationIdentity(
	ctx context.Context,
	tx postgresTx,
	studentNumber string,
	ptaNickname string,
) (int64, bool, error) {
	var actorID int64
	var currentStudentNumber *string
	var currentDisplayName *string
	var currentExporterName *string
	var currentExporterVersion *string
	err := tx.QueryRow(ctx, `
SELECT identifier.actor_id,
       current_participant.student_number,
       current_participant.display_name,
       current_participant.exporter_name,
       current_participant.exporter_version
FROM ascendany.pintia_actor_identifiers AS identifier
LEFT JOIN LATERAL (
    SELECT participant.student_number,
           participant.display_name,
           snapshot.exporter_name,
           snapshot.exporter_version
    FROM ascendany.logical_exams AS exam
    JOIN ascendany.exam_snapshots AS snapshot
      ON snapshot.snapshot_id = exam.active_snapshot_id
    JOIN ascendany.pintia_snapshot_participants AS participant
      ON participant.snapshot_id = exam.active_snapshot_id
     AND participant.actor_id = identifier.actor_id
    WHERE exam.active_snapshot_id IS NOT NULL
    ORDER BY exam.updated_at DESC, exam.exam_id DESC
    LIMIT 1
) AS current_participant ON true
WHERE identifier.identifier_kind = 'student_number'
  AND identifier.identifier_value = $1`, studentNumber).Scan(
		&actorID,
		&currentStudentNumber,
		&currentDisplayName,
		&currentExporterName,
		&currentExporterVersion,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, databaseFailure("resolve current registration identity", err)
	}
	matches := actorID > 0 && currentStudentNumber != nil && currentDisplayName != nil &&
		currentExporterName != nil && currentExporterVersion != nil &&
		*currentStudentNumber == studentNumber && *currentDisplayName == ptaNickname &&
		pintia.SupportsRegistrationNicknameIdentity(*currentExporterName, *currentExporterVersion)
	return actorID, matches, nil
}

func lockCurrentParticipantIdentity(ctx context.Context, tx postgresTx) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, pintia.ParticipantIdentityAdvisoryLockID); err != nil {
		return databaseFailure("lock current participant identity", err)
	}
	return nil
}

func registrationUsernameUnavailableInTransaction(
	ctx context.Context,
	tx postgresTx,
	username string,
	now time.Time,
) (bool, error) {
	var unavailable bool
	err := tx.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1
    FROM ascendany.auth_accounts
    WHERE username = $1
)
OR EXISTS (
    SELECT 1
    FROM ascendany.auth_enrollment_grants AS enrollment
    WHERE enrollment.username = $1
      AND enrollment.expires_at > $2
      AND NOT EXISTS (
          SELECT 1
          FROM ascendany.auth_enrollment_events AS terminal
          WHERE terminal.enrollment_grant_id = enrollment.enrollment_grant_id
            AND terminal.event_slot = 1
      )
)`, username, now).Scan(&unavailable)
	if err != nil {
		return false, databaseFailure("check registration username", err)
	}
	return unavailable, nil
}

func registrationActorUnavailableInTransaction(
	ctx context.Context,
	tx postgresTx,
	actorID int64,
	studentNumber string,
	now time.Time,
) (bool, error) {
	var unavailable bool
	err := tx.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1
    FROM ascendany.auth_accounts
    WHERE actor_id = $1
       OR student_number = $2
)
OR EXISTS (
    SELECT 1
    FROM ascendany.auth_enrollment_grants AS enrollment
    WHERE (enrollment.actor_id = $1 OR enrollment.student_number = $2)
      AND enrollment.expires_at > $3
      AND NOT EXISTS (
          SELECT 1
          FROM ascendany.auth_enrollment_events AS terminal
          WHERE terminal.enrollment_grant_id = enrollment.enrollment_grant_id
            AND terminal.event_slot = 1
      )
)`, actorID, studentNumber, now).Scan(&unavailable)
	if err != nil {
		return false, databaseFailure("check registration student identity", err)
	}
	return unavailable, nil
}

func validateRegisterStudentCommand(command RegisterStudentCommand) error {
	if err := validateAccountRecord(command.Account); err != nil {
		return err
	}
	if command.Account.Role != RoleStudent || command.Account.StudentNumber == nil ||
		command.Account.PTANickname == nil || command.Account.DisplayName != command.Account.Username ||
		command.Account.AuthRevision != 1 || command.Account.DisabledAt != nil {
		return authError(ErrorInternal, "Student registration account is invalid.", nil)
	}
	if _, err := parseUUIDv4(command.SessionID); err != nil {
		return authError(ErrorInternal, "Registration session ID is invalid.", err)
	}
	if command.Now.IsZero() || !command.SessionExpiry.After(command.Now) {
		return authError(ErrorInternal, "Registration transaction boundary is invalid.", nil)
	}
	if err := validateNewRefreshToken(command.RefreshToken, command.Now, command.SessionExpiry); err != nil {
		return err
	}
	return nil
}
