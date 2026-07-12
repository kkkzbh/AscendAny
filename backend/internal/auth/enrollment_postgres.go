package auth

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

const enrollmentAdvisoryLock int64 = 4706902253123607892

func (r *PostgresRepository) IssueEnrollment(
	ctx context.Context,
	command IssueEnrollmentCommand,
) (IssueEnrollmentResult, error) {
	if err := validateIssueEnrollmentCommand(command); err != nil {
		return IssueEnrollmentResult{}, err
	}
	result := IssueEnrollmentResult{Status: EnrollmentIssueIssuerRejected}
	err := r.transaction(ctx, "issue enrollment grant", func(tx postgresTx) error {
		issuerSession, err := lockAdminSession(
			ctx,
			tx,
			command.Grant.IssuerAccountID,
			command.IssuerSessionID,
		)
		if err != nil {
			return err
		}
		if err := lockEnrollmentTransactions(ctx, tx); err != nil {
			return err
		}
		transactionNow, err := enrollmentTransactionTime(ctx, tx)
		if err != nil {
			return err
		}
		if !issuerSession.activeAt(command.ExpectedIssuerAuthRevision, transactionNow) {
			return nil
		}
		if !transactionNow.Before(command.Grant.ExpiresAt) ||
			command.Grant.ExpiresAt.After(transactionNow.Add(MaxEnrollmentLifetime)) {
			result.Status = EnrollmentIssueExpired
			return nil
		}

		var actorID int64
		err = tx.QueryRow(ctx, `
SELECT actor_id
FROM ascendany.pintia_actor_identifiers
WHERE identifier_kind = 'student_number'
  AND identifier_value = $1`, command.Grant.StudentNumber).Scan(&actorID)
		if errors.Is(err, pgx.ErrNoRows) {
			result.Status = EnrollmentIssueIdentityUnavailable
			return nil
		}
		if err != nil {
			return databaseFailure("resolve enrollment actor", err)
		}

		var accountExists bool
		if err := tx.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1
    FROM ascendany.auth_accounts
    WHERE username = $1
       OR student_number = $2
       OR actor_id = $3
)`, command.Grant.Username, command.Grant.StudentNumber, actorID).Scan(&accountExists); err != nil {
			return databaseFailure("check enrollment account identity", err)
		}
		if accountExists {
			result.Status = EnrollmentIssueIdentityUnavailable
			return nil
		}

		var activeGrantExists bool
		if err := tx.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1
    FROM ascendany.auth_enrollment_grants AS enrollment
    WHERE (
        enrollment.username = $1
        OR enrollment.student_number = $2
        OR enrollment.actor_id = $3
    )
      AND enrollment.expires_at > $4
      AND NOT EXISTS (
          SELECT 1
          FROM ascendany.auth_enrollment_events AS terminal
          WHERE terminal.enrollment_grant_id = enrollment.enrollment_grant_id
            AND terminal.event_slot = 1
      )
)`, command.Grant.Username, command.Grant.StudentNumber, actorID, transactionNow).Scan(&activeGrantExists); err != nil {
			return databaseFailure("check active enrollment identity", err)
		}
		if activeGrantExists {
			result.Status = EnrollmentIssueIdentityUnavailable
			return nil
		}

		var grantDatabaseID int64
		if err := tx.QueryRow(ctx, `
INSERT INTO ascendany.auth_enrollment_grants (
    public_id,
    secret_digest,
    username,
    display_name,
    student_number,
    actor_id,
    issuer_account_id,
    issuer_role,
    issuer_session_id,
    issued_at,
    expires_at
)
VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, 'admin', $8, $9, $10)
RETURNING enrollment_grant_id`,
			command.Grant.ID,
			command.SecretDigest[:],
			command.Grant.Username,
			command.Grant.DisplayName,
			command.Grant.StudentNumber,
			actorID,
			issuerSession.accountDatabaseID,
			issuerSession.sessionDatabaseID,
			transactionNow,
			command.Grant.ExpiresAt,
		).Scan(&grantDatabaseID); err != nil {
			return databaseFailure("insert enrollment grant", err)
		}
		if err := appendEnrollmentEvent(
			ctx,
			tx,
			grantDatabaseID,
			issuerSession.accountDatabaseID,
			issuerSession.sessionDatabaseID,
			0,
			"issued",
			transactionNow,
		); err != nil {
			return err
		}
		if err := appendAuditEvent(ctx, tx, issuerSession.accountDatabaseID, issuerSession.sessionDatabaseID, "auth.enrollment_issued", transactionNow); err != nil {
			return err
		}
		issuedGrant := command.Grant
		issuedGrant.IssuedAt = transactionNow
		result = IssueEnrollmentResult{Status: EnrollmentIssued, Grant: issuedGrant}
		return nil
	})
	return result, err
}

func (r *PostgresRepository) RevokeEnrollment(
	ctx context.Context,
	command RevokeEnrollmentCommand,
) (RevokeEnrollmentStatus, error) {
	if err := validateRevokeEnrollmentCommand(command); err != nil {
		return 0, err
	}
	status := EnrollmentRevokeIssuerRejected
	err := r.transaction(ctx, "revoke enrollment grant", func(tx postgresTx) error {
		revokerSession, err := lockAdminSession(
			ctx,
			tx,
			command.RevokerAccountID,
			command.RevokerSessionID,
		)
		if err != nil {
			return err
		}
		if err := lockEnrollmentTransactions(ctx, tx); err != nil {
			return err
		}
		transactionNow, err := enrollmentTransactionTime(ctx, tx)
		if err != nil {
			return err
		}
		if !revokerSession.activeAt(command.ExpectedRevokerAuthRevision, transactionNow) {
			return nil
		}

		var grantDatabaseID int64
		var issuedAt time.Time
		var expiresAt time.Time
		var terminalEvent *string
		err = tx.QueryRow(ctx, `
SELECT enrollment.enrollment_grant_id,
       enrollment.issued_at,
       enrollment.expires_at,
       terminal.event_type
FROM ascendany.auth_enrollment_grants AS enrollment
LEFT JOIN ascendany.auth_enrollment_events AS terminal
  ON terminal.enrollment_grant_id = enrollment.enrollment_grant_id
 AND terminal.event_slot = 1
WHERE enrollment.public_id = $1::uuid
`, command.GrantID).Scan(&grantDatabaseID, &issuedAt, &expiresAt, &terminalEvent)
		if errors.Is(err, pgx.ErrNoRows) {
			status = EnrollmentRevokeNotRevocable
			return nil
		}
		if err != nil {
			return databaseFailure("lock enrollment grant for revocation", err)
		}
		if terminalEvent != nil || transactionNow.Before(issuedAt) || !transactionNow.Before(expiresAt) {
			status = EnrollmentRevokeNotRevocable
			return nil
		}
		if err := appendEnrollmentEvent(
			ctx,
			tx,
			grantDatabaseID,
			revokerSession.accountDatabaseID,
			revokerSession.sessionDatabaseID,
			0,
			"revoked",
			transactionNow,
		); err != nil {
			return err
		}
		if err := appendAuditEvent(ctx, tx, revokerSession.accountDatabaseID, revokerSession.sessionDatabaseID, "auth.enrollment_revoked", transactionNow); err != nil {
			return err
		}
		status = EnrollmentRevoked
		return nil
	})
	return status, err
}

func (r *PostgresRepository) ClaimEnrollment(
	ctx context.Context,
	command ClaimEnrollmentCommand,
) (ClaimEnrollmentResult, error) {
	if err := validateClaimEnrollmentCommand(command); err != nil {
		return ClaimEnrollmentResult{}, err
	}
	result := ClaimEnrollmentResult{Status: EnrollmentClaimRejected}
	err := r.transaction(ctx, "claim enrollment grant", func(tx postgresTx) error {
		if err := lockEnrollmentTransactions(ctx, tx); err != nil {
			return err
		}
		transactionNow, err := enrollmentTransactionTime(ctx, tx)
		if err != nil {
			return err
		}

		var grantDatabaseID int64
		var actorID int64
		var username string
		var displayName string
		var studentNumber string
		var issuedAt time.Time
		var expiresAt time.Time
		var terminalEvent *string
		err = tx.QueryRow(ctx, `
SELECT enrollment.enrollment_grant_id,
       enrollment.actor_id,
       enrollment.username,
       enrollment.display_name,
       enrollment.student_number,
       enrollment.issued_at,
       enrollment.expires_at,
       terminal.event_type
FROM ascendany.auth_enrollment_grants AS enrollment
LEFT JOIN ascendany.auth_enrollment_events AS terminal
  ON terminal.enrollment_grant_id = enrollment.enrollment_grant_id
 AND terminal.event_slot = 1
WHERE enrollment.secret_digest = $1
`, command.SecretDigest[:]).Scan(
			&grantDatabaseID,
			&actorID,
			&username,
			&displayName,
			&studentNumber,
			&issuedAt,
			&expiresAt,
			&terminalEvent,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return databaseFailure("lock enrollment grant for claim", err)
		}
		if terminalEvent != nil || transactionNow.Before(issuedAt) || !transactionNow.Before(expiresAt) {
			return nil
		}

		var identityAvailable bool
		if err := tx.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1
    FROM ascendany.pintia_actor_identifiers
    WHERE identifier_kind = 'student_number'
      AND identifier_value = $1
      AND actor_id = $2
)
AND NOT EXISTS (
    SELECT 1
    FROM ascendany.auth_accounts
    WHERE username = $3
       OR student_number = $1
       OR actor_id = $2
)`, studentNumber, actorID, username).Scan(&identityAvailable); err != nil {
			return databaseFailure("validate enrollment identity", err)
		}
		if !identityAvailable {
			return nil
		}

		var accountDatabaseID int64
		if err := tx.QueryRow(ctx, `
INSERT INTO ascendany.auth_accounts (
    public_id,
    actor_id,
    username,
    password_phc,
    display_name,
    student_number,
    role,
    auth_revision,
    created_at,
    updated_at
)
VALUES ($1::uuid, $2, $3, $4, $5, $6, 'student', 1, $7, $7)
RETURNING account_id`,
			command.AccountID,
			actorID,
			username,
			command.PasswordPHC,
			displayName,
			studentNumber,
			transactionNow,
		).Scan(&accountDatabaseID); err != nil {
			return databaseFailure("insert enrolled student account", err)
		}
		refreshTTL := command.SessionExpiry.Sub(command.Now)
		refresh := command.RefreshToken
		refresh.CreatedAt = transactionNow
		refresh.ExpiresAt = transactionNow.Add(refreshTTL)
		sessionDatabaseID, err := insertSessionAndRefresh(
			ctx,
			tx,
			accountDatabaseID,
			1,
			command.SessionID,
			transactionNow,
			refresh.ExpiresAt,
			refresh,
		)
		if err != nil {
			return err
		}
		if err := appendEnrollmentEvent(
			ctx,
			tx,
			grantDatabaseID,
			accountDatabaseID,
			sessionDatabaseID,
			actorID,
			"consumed",
			transactionNow,
		); err != nil {
			return err
		}
		if err := appendAuditEvent(ctx, tx, accountDatabaseID, sessionDatabaseID, "auth.enrollment_consumed", transactionNow); err != nil {
			return err
		}
		account := AccountRecord{
			Account: Account{
				ID:            command.AccountID,
				Username:      username,
				DisplayName:   displayName,
				StudentNumber: &studentNumber,
				Role:          RoleStudent,
				AuthRevision:  1,
			},
			PasswordPHC: command.PasswordPHC,
		}
		result = ClaimEnrollmentResult{Status: EnrollmentClaimed, Account: account, AuthenticatedAt: transactionNow}
		return nil
	})
	return result, err
}

func lockEnrollmentTransactions(ctx context.Context, tx postgresTx) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, enrollmentAdvisoryLock); err != nil {
		return databaseFailure("lock enrollment transactions", err)
	}
	return nil
}

type lockedAdminSession struct {
	accountDatabaseID   int64
	sessionDatabaseID   int64
	role                Role
	accountAuthRevision int64
	disabledAt          *time.Time
	sessionAuthRevision int64
	sessionCreatedAt    time.Time
	sessionExpiresAt    time.Time
	sessionRevokedAt    *time.Time
}

func (session lockedAdminSession) activeAt(expectedAuthRevision int64, now time.Time) bool {
	return session.accountDatabaseID > 0 && session.sessionDatabaseID > 0 &&
		session.role == RoleAdmin && session.accountAuthRevision == expectedAuthRevision &&
		session.sessionAuthRevision == session.accountAuthRevision && session.disabledAt == nil &&
		session.sessionRevokedAt == nil && !now.Before(session.sessionCreatedAt) && now.Before(session.sessionExpiresAt)
}

func lockAdminSession(
	ctx context.Context,
	tx postgresTx,
	accountPublicID string,
	sessionPublicID string,
) (lockedAdminSession, error) {
	var session lockedAdminSession
	err := tx.QueryRow(ctx, `
SELECT account.account_id,
       session.session_id,
       account.role,
       account.auth_revision,
       account.disabled_at,
       session.auth_revision,
       session.created_at,
       session.expires_at,
       session.revoked_at
FROM ascendany.auth_accounts AS account
JOIN ascendany.auth_sessions AS session
  ON session.account_id = account.account_id
WHERE account.public_id = $1::uuid
  AND session.public_id = $2::uuid
FOR UPDATE OF account, session`, accountPublicID, sessionPublicID).Scan(
		&session.accountDatabaseID,
		&session.sessionDatabaseID,
		&session.role,
		&session.accountAuthRevision,
		&session.disabledAt,
		&session.sessionAuthRevision,
		&session.sessionCreatedAt,
		&session.sessionExpiresAt,
		&session.sessionRevokedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return lockedAdminSession{}, nil
	}
	if err != nil {
		return lockedAdminSession{}, databaseFailure("lock enrollment administrator session", err)
	}
	return session, nil
}

func enrollmentTransactionTime(ctx context.Context, tx postgresTx) (time.Time, error) {
	var now time.Time
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		return time.Time{}, databaseFailure("read enrollment transaction time", err)
	}
	return canonicalAuthTime(now), nil
}

func appendEnrollmentEvent(
	ctx context.Context,
	tx postgresTx,
	grantDatabaseID int64,
	actorAccountDatabaseID int64,
	sessionDatabaseID int64,
	subjectActorID int64,
	eventType string,
	now time.Time,
) error {
	if sessionDatabaseID <= 0 {
		return authError(ErrorInternal, "Enrollment event session is invalid.", nil)
	}
	var subjectActor any
	if eventType == "consumed" && subjectActorID > 0 {
		subjectActor = subjectActorID
	} else if eventType == "consumed" || subjectActorID != 0 {
		return authError(ErrorInternal, "Enrollment event subject actor is invalid.", nil)
	}
	actorRole := RoleAdmin
	if eventType == "consumed" {
		actorRole = RoleStudent
	} else if eventType != "issued" && eventType != "revoked" {
		return authError(ErrorInternal, "Enrollment event type is invalid.", nil)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO ascendany.auth_enrollment_events (
    enrollment_grant_id,
    event_type,
    actor_account_id,
    actor_role,
    session_id,
    subject_actor_id,
    occurred_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		grantDatabaseID,
		eventType,
		actorAccountDatabaseID,
		actorRole,
		sessionDatabaseID,
		subjectActor,
		now,
	); err != nil {
		return databaseFailure("append enrollment event", err)
	}
	return nil
}

func validateIssueEnrollmentCommand(command IssueEnrollmentCommand) error {
	if _, err := parseUUIDv4(command.Grant.ID); err != nil {
		return authError(ErrorInternal, "Enrollment grant ID is invalid.", err)
	}
	if _, err := parseUUIDv4(command.Grant.IssuerAccountID); err != nil {
		return authError(ErrorInternal, "Enrollment issuer account ID is invalid.", err)
	}
	if _, err := parseUUIDv4(command.IssuerSessionID); err != nil {
		return authError(ErrorInternal, "Enrollment issuer session ID is invalid.", err)
	}
	if err := validateUsername(command.Grant.Username); err != nil {
		return authError(ErrorInternal, "Enrollment username is invalid.", err)
	}
	if !validEnrollmentStoredField(command.Grant.DisplayName, MinDisplayNameBytes, MaxDisplayNameBytes) ||
		!validEnrollmentStoredField(command.Grant.StudentNumber, MinStudentNumberBytes, MaxStudentNumberBytes) ||
		zeroDigest(command.SecretDigest) ||
		command.ExpectedIssuerAuthRevision < 1 ||
		command.Grant.IssuedAt.IsZero() ||
		!command.Grant.ExpiresAt.After(command.Grant.IssuedAt) ||
		command.Grant.ExpiresAt.After(command.Grant.IssuedAt.Add(MaxEnrollmentLifetime)) {
		return authError(ErrorInternal, "Enrollment issue command is invalid.", nil)
	}
	return nil
}

func validateRevokeEnrollmentCommand(command RevokeEnrollmentCommand) error {
	if _, err := parseUUIDv4(command.GrantID); err != nil {
		return authError(ErrorInternal, "Enrollment revoke grant ID is invalid.", err)
	}
	if _, err := parseUUIDv4(command.RevokerAccountID); err != nil {
		return authError(ErrorInternal, "Enrollment revoker account ID is invalid.", err)
	}
	if _, err := parseUUIDv4(command.RevokerSessionID); err != nil {
		return authError(ErrorInternal, "Enrollment revoker session ID is invalid.", err)
	}
	if command.ExpectedRevokerAuthRevision < 1 || command.Now.IsZero() {
		return authError(ErrorInternal, "Enrollment revoke command is invalid.", nil)
	}
	return nil
}

func validateClaimEnrollmentCommand(command ClaimEnrollmentCommand) error {
	if zeroDigest(command.SecretDigest) {
		return authError(ErrorInternal, "Enrollment claim digest is invalid.", nil)
	}
	if _, err := parseUUIDv4(command.AccountID); err != nil {
		return authError(ErrorInternal, "Enrolled account ID is invalid.", err)
	}
	if _, err := parseUUIDv4(command.SessionID); err != nil {
		return authError(ErrorInternal, "Enrollment session ID is invalid.", err)
	}
	if _, _, err := parsePHC(command.PasswordPHC); err != nil {
		return authError(ErrorInternal, "Enrollment password hash is invalid.", err)
	}
	if command.Now.IsZero() || !command.SessionExpiry.After(command.Now) {
		return authError(ErrorInternal, "Enrollment claim transaction boundary is invalid.", nil)
	}
	return validateNewRefreshToken(command.RefreshToken, command.Now, command.SessionExpiry)
}

func validEnrollmentStoredField(value string, minimum, maximum int) bool {
	return utf8.ValidString(value) && strings.IndexByte(value, 0) < 0 &&
		len(value) >= minimum && len(value) <= maximum && strings.TrimSpace(value) == value
}

func zeroDigest(value [32]byte) bool {
	var combined byte
	for _, item := range value {
		combined |= item
	}
	return combined == 0
}
