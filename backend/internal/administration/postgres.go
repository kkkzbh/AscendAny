package administration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
	"github.com/kkkzbh/AscendAny/backend/internal/principalguard"
)

type PgxBeginner interface {
	BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
}

type postgresTx interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
	Commit(context.Context) error
	Rollback(context.Context) error
}

type beginTransaction func(context.Context, pgx.TxOptions) (postgresTx, error)

type PostgresRepository struct {
	begin beginTransaction
}

func NewPostgresRepository(pool PgxBeginner) (*PostgresRepository, error) {
	if pool == nil {
		return nil, adminError(ErrorInvalidConfiguration, "construct administration PostgreSQL repository", errors.New("database pool is required"))
	}
	return &PostgresRepository{begin: func(ctx context.Context, options pgx.TxOptions) (postgresTx, error) {
		return pool.BeginTx(ctx, options)
	}}, nil
}

func newPostgresRepository(begin beginTransaction) (*PostgresRepository, error) {
	if begin == nil {
		return nil, adminError(ErrorInvalidConfiguration, "construct administration PostgreSQL repository", errors.New("transaction beginner is required"))
	}
	return &PostgresRepository{begin: begin}, nil
}

func (repository *PostgresRepository) LoadAccounts(ctx context.Context, query AccountQuery) (AccountPage, error) {
	var page AccountPage
	err := repository.transaction(ctx, "load managed accounts", true, func(tx postgresTx) error {
		if err := resolveAdminPrincipal(ctx, tx, query.Principal, false); err != nil {
			return err
		}
		var cursorCreatedAt *time.Time
		var cursorDatabaseID *int64
		if query.Cursor != nil {
			var createdAt time.Time
			var databaseID int64
			err := tx.QueryRow(ctx, `
SELECT created_at, account_id
FROM ascendany.auth_accounts
WHERE public_id = $1::uuid`, *query.Cursor).Scan(&createdAt, &databaseID)
			if errors.Is(err, pgx.ErrNoRows) {
				return adminError(ErrorCursorInvalid, "resolve managed account cursor", errors.New("cursor does not identify an account"))
			}
			if err != nil {
				return databaseFailure("resolve managed account cursor", err)
			}
			cursorCreatedAt = &createdAt
			cursorDatabaseID = &databaseID
		}
		rows, err := tx.Query(ctx, managedAccountSelect+`
WHERE ($1::timestamptz IS NULL OR (account.created_at, account.account_id) < ($1, $2))
ORDER BY account.created_at DESC, account.account_id DESC
LIMIT $3`, cursorCreatedAt, cursorDatabaseID, query.Limit+1)
		if err != nil {
			return databaseFailure("query managed accounts", err)
		}
		defer rows.Close()
		items := make([]ManagedAccount, 0, query.Limit+1)
		for rows.Next() {
			account, err := scanManagedAccount(rows)
			if err != nil {
				return databaseFailure("scan managed account", err)
			}
			items = append(items, account)
		}
		if err := rows.Err(); err != nil {
			return databaseFailure("iterate managed accounts", err)
		}
		if len(items) > query.Limit {
			items = items[:query.Limit]
			cursor := items[len(items)-1].ID
			page.NextCursor = &cursor
		}
		page.Items = items
		return nil
	})
	if err != nil {
		return AccountPage{}, err
	}
	return page, nil
}

func (repository *PostgresRepository) LoadStudents(ctx context.Context, query StudentQuery) (StudentPage, error) {
	var page StudentPage
	err := repository.transaction(ctx, "load managed students", true, func(tx postgresTx) error {
		if err := resolveAdminPrincipal(ctx, tx, query.Principal, false); err != nil {
			return err
		}
		var cursorStudentNumber *string
		if query.Cursor != nil {
			studentNumber, err := DecodeStudentCursor(*query.Cursor)
			if err != nil {
				return adminError(ErrorInvalidQuery, "decode managed student cursor", err)
			}
			var exists bool
			if err := tx.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1
    FROM ascendany.pintia_actor_identifiers
    WHERE identifier_kind = 'student_number'
      AND identifier_value = $1
)`, studentNumber).Scan(&exists); err != nil {
				return databaseFailure("resolve managed student cursor", err)
			}
			if !exists {
				return adminError(ErrorCursorInvalid, "resolve managed student cursor", errors.New("cursor does not identify an imported student"))
			}
			cursorStudentNumber = &studentNumber
		}
		rows, err := tx.Query(ctx, `
SELECT identifier.identifier_value,
       actor.user_id,
       source.display_name,
       account.public_id::text,
       account.username,
       account.display_name,
       account.disabled_at,
       analytics.rating::text
FROM ascendany.pintia_actor_identifiers AS identifier
JOIN ascendany.pintia_actors AS actor
  ON actor.actor_id = identifier.actor_id
LEFT JOIN ascendany.auth_accounts AS account
  ON account.actor_id = actor.actor_id
 AND account.role = 'student'
LEFT JOIN ascendany.analytics_head AS head
  ON head.singleton
LEFT JOIN ascendany.student_analytics AS analytics
  ON analytics.analytics_generation_id = head.current_generation_id
 AND analytics.actor_id = actor.actor_id
LEFT JOIN LATERAL (
    SELECT participant.display_name
    FROM ascendany.logical_exams AS exam
    JOIN ascendany.pintia_snapshot_participants AS participant
      ON participant.snapshot_id = exam.active_snapshot_id
     AND participant.actor_id = actor.actor_id
    WHERE participant.display_name IS NOT NULL
    ORDER BY exam.updated_at DESC, exam.exam_id DESC
    LIMIT 1
) AS source ON true
WHERE identifier.identifier_kind = 'student_number'
  AND ($1::text IS NULL OR identifier.identifier_value COLLATE "C" > $1 COLLATE "C")
ORDER BY identifier.identifier_value COLLATE "C" ASC
LIMIT $2`, cursorStudentNumber, query.Limit+1)
		if err != nil {
			return databaseFailure("query managed students", err)
		}
		defer rows.Close()
		items := make([]ManagedStudent, 0, query.Limit+1)
		for rows.Next() {
			student, err := scanManagedStudent(rows)
			if err != nil {
				return err
			}
			items = append(items, student)
		}
		if err := rows.Err(); err != nil {
			return databaseFailure("iterate managed students", err)
		}
		if len(items) > query.Limit {
			items = items[:query.Limit]
			cursor, err := EncodeStudentCursor(items[len(items)-1].StudentNumber)
			if err != nil {
				return adminError(ErrorStoredDataInvalid, "encode managed student cursor", err)
			}
			page.NextCursor = &cursor
		}
		page.Items = items
		return nil
	})
	if err != nil {
		return StudentPage{}, err
	}
	return page, nil
}

func (repository *PostgresRepository) LoadAudit(ctx context.Context, query AuditQuery) (AuditPage, error) {
	var page AuditPage
	err := repository.transaction(ctx, "load audit events", true, func(tx postgresTx) error {
		if err := resolveAdminPrincipal(ctx, tx, query.Principal, false); err != nil {
			return err
		}
		var cursorID *int64
		if query.Cursor != nil {
			identifier, err := parseAuditID(*query.Cursor)
			if err != nil {
				return adminError(ErrorInvalidQuery, "decode audit cursor", err)
			}
			var exists bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM ascendany.audit_events WHERE audit_event_id = $1)`, identifier).Scan(&exists); err != nil {
				return databaseFailure("resolve audit cursor", err)
			}
			if !exists {
				return adminError(ErrorCursorInvalid, "resolve audit cursor", errors.New("cursor does not identify an audit event"))
			}
			cursorID = &identifier
		}
		rows, err := tx.Query(ctx, `
SELECT event.audit_event_id,
       account.public_id::text,
       session.public_id::text,
       event.event_type,
       event.occurred_at,
       event.payload::text
FROM ascendany.audit_events AS event
LEFT JOIN ascendany.auth_accounts AS account
  ON account.account_id = event.account_id
LEFT JOIN ascendany.auth_sessions AS session
  ON session.session_id = event.session_id
WHERE ($1::bigint IS NULL OR event.audit_event_id < $1)
ORDER BY event.audit_event_id DESC
LIMIT $2`, cursorID, query.Limit+1)
		if err != nil {
			return databaseFailure("query audit events", err)
		}
		defer rows.Close()
		items := make([]AuditEvent, 0, query.Limit+1)
		for rows.Next() {
			var databaseID int64
			var payload string
			var event AuditEvent
			if err := rows.Scan(&databaseID, &event.ActorAccountID, &event.ActorSessionID, &event.Type, &event.OccurredAt, &payload); err != nil {
				return databaseFailure("scan audit event", err)
			}
			event.ID = strconv.FormatInt(databaseID, 10)
			event.OccurredAt = event.OccurredAt.UTC()
			event.Payload = json.RawMessage(payload)
			items = append(items, event)
		}
		if err := rows.Err(); err != nil {
			return databaseFailure("iterate audit events", err)
		}
		if len(items) > query.Limit {
			items = items[:query.Limit]
			cursor := items[len(items)-1].ID
			page.NextCursor = &cursor
		}
		page.Items = items
		return nil
	})
	if err != nil {
		return AuditPage{}, err
	}
	return page, nil
}

func (repository *PostgresRepository) SetAccountDisabled(
	ctx context.Context,
	command AccountStateCommand,
) (ManagedAccount, error) {
	if err := validateAccountStateCommand(command); err != nil {
		return ManagedAccount{}, err
	}
	var result ManagedAccount
	err := repository.transaction(ctx, "set managed account state", false, func(tx postgresTx) error {
		resolved, err := lockAdministrationMutation(ctx, tx, command.Principal, command.TargetID)
		if err != nil {
			return err
		}
		if command.Disabled && command.TargetID == resolved.AccountID {
			return adminError(ErrorSelfDisable, "set managed account state", errors.New("administrator cannot disable the active account"))
		}
		current, err := scanManagedAccount(tx.QueryRow(ctx, managedAccountSelect+`
WHERE account.public_id = $1::uuid
FOR UPDATE OF account`, command.TargetID))
		if errors.Is(err, pgx.ErrNoRows) {
			return adminError(ErrorTargetNotFound, "lock managed account target", errors.New("account does not exist"))
		}
		if err != nil {
			return databaseFailure("lock managed account target", err)
		}
		if (current.DisabledAt != nil) == command.Disabled {
			result = current
			return nil
		}
		var mutationTime time.Time
		if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&mutationTime); err != nil {
			return databaseFailure("read managed account mutation time", err)
		}
		mutationTime = mutationTime.UTC()
		var disabledAt *time.Time
		if command.Disabled {
			value := mutationTime
			disabledAt = &value
		}
		tag, err := tx.Exec(ctx, `
UPDATE ascendany.auth_accounts
SET disabled_at = $2,
    auth_revision = auth_revision + 1,
    updated_at = $3
WHERE public_id = $1::uuid`, command.TargetID, disabledAt, mutationTime)
		if err != nil {
			return databaseFailure("update managed account state", err)
		}
		if tag.RowsAffected() != 1 {
			return databaseFailure("update managed account state", errors.New("locked target was not updated"))
		}
		if command.Disabled {
			if _, err := tx.Exec(ctx, `
UPDATE ascendany.auth_sessions
SET revoked_at = COALESCE(revoked_at, $2),
    revocation_reason = COALESCE(revocation_reason, 'admin_disabled')
WHERE account_id = (SELECT account_id FROM ascendany.auth_accounts WHERE public_id = $1::uuid)`, command.TargetID, mutationTime); err != nil {
				return databaseFailure("revoke disabled account sessions", err)
			}
			if _, err := tx.Exec(ctx, `
UPDATE ascendany.auth_refresh_tokens
SET revoked_at = COALESCE(revoked_at, $2)
WHERE session_id IN (
    SELECT session.session_id
    FROM ascendany.auth_sessions AS session
    JOIN ascendany.auth_accounts AS account
      ON account.account_id = session.account_id
    WHERE account.public_id = $1::uuid
)`, command.TargetID, mutationTime); err != nil {
				return databaseFailure("revoke disabled account refresh credentials", err)
			}
		}
		if err := appendAccountStateAudit(ctx, tx, resolved, command, mutationTime); err != nil {
			return err
		}
		result, err = scanManagedAccount(tx.QueryRow(ctx, managedAccountSelect+`WHERE account.public_id = $1::uuid`, command.TargetID))
		if err != nil {
			return databaseFailure("reload managed account state", err)
		}
		return nil
	})
	if err != nil {
		return ManagedAccount{}, err
	}
	return result, nil
}

func lockAdministrationMutation(
	ctx context.Context,
	tx postgresTx,
	principal auth.AccessPrincipal,
	targetPublicID string,
) (principalguard.Resolved, error) {
	resolved, err := principalguard.Resolve(ctx, tx, principal, principalguard.Roles(auth.RoleAdmin))
	if err != nil {
		return principalguard.Resolved{}, mapPrincipalError("resolve administration mutation principal", err)
	}
	var targetDatabaseID int64
	err = tx.QueryRow(ctx, `
SELECT account_id
FROM ascendany.auth_accounts
WHERE public_id = $1::uuid`, targetPublicID).Scan(&targetDatabaseID)
	if errors.Is(err, pgx.ErrNoRows) {
		return principalguard.Resolved{}, adminError(ErrorTargetNotFound, "resolve managed account target", errors.New("account does not exist"))
	}
	if err != nil {
		return principalguard.Resolved{}, databaseFailure("resolve managed account target", err)
	}

	rows, err := tx.Query(ctx, `
SELECT account_id
FROM ascendany.auth_accounts
WHERE account_id = $1 OR account_id = $2
ORDER BY account_id
FOR UPDATE`, resolved.AccountDatabaseID, targetDatabaseID)
	if err != nil {
		return principalguard.Resolved{}, databaseFailure("lock administration mutation accounts", err)
	}
	lockedAccounts := 0
	for rows.Next() {
		lockedAccounts++
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return principalguard.Resolved{}, databaseFailure("iterate administration mutation account locks", err)
	}
	expectedAccounts := 2
	if resolved.AccountDatabaseID == targetDatabaseID {
		expectedAccounts = 1
	}
	if lockedAccounts != expectedAccounts {
		return principalguard.Resolved{}, adminError(ErrorStoredDataInvalid, "lock administration mutation accounts", errors.New("account lock set changed during transaction"))
	}

	var lockedSessionID int64
	err = tx.QueryRow(ctx, `
SELECT session_id
FROM ascendany.auth_sessions
WHERE session_id = $1
FOR UPDATE`, resolved.SessionDatabaseID).Scan(&lockedSessionID)
	if err != nil {
		return principalguard.Resolved{}, databaseFailure("lock administration mutation session", err)
	}
	if lockedSessionID != resolved.SessionDatabaseID {
		return principalguard.Resolved{}, adminError(ErrorStoredDataInvalid, "lock administration mutation session", errors.New("session lock identity changed"))
	}

	lockedPrincipal, err := principalguard.Resolve(ctx, tx, principal, principalguard.Roles(auth.RoleAdmin))
	if err != nil {
		return principalguard.Resolved{}, mapPrincipalError("revalidate locked administration mutation principal", err)
	}
	if lockedPrincipal.AccountDatabaseID != resolved.AccountDatabaseID ||
		lockedPrincipal.SessionDatabaseID != resolved.SessionDatabaseID {
		return principalguard.Resolved{}, adminError(ErrorStoredDataInvalid, "revalidate locked administration mutation principal", errors.New("principal database identity changed"))
	}
	return lockedPrincipal, nil
}

const managedAccountSelect = `
SELECT account.public_id::text,
       account.username,
       account.display_name,
       account.student_number,
       account.role,
       account.auth_revision,
       account.disabled_at,
       account.created_at,
       account.updated_at,
       (
           SELECT count(*)
           FROM ascendany.auth_sessions AS session
           WHERE session.account_id = account.account_id
             AND session.revoked_at IS NULL
             AND session.expires_at > transaction_timestamp()
       )
FROM ascendany.auth_accounts AS account
`

type rowScanner interface {
	Scan(...any) error
}

func scanManagedAccount(scanner rowScanner) (ManagedAccount, error) {
	var account ManagedAccount
	var role string
	if err := scanner.Scan(
		&account.ID,
		&account.Username,
		&account.DisplayName,
		&account.StudentNumber,
		&role,
		&account.AuthRevision,
		&account.DisabledAt,
		&account.CreatedAt,
		&account.UpdatedAt,
		&account.ActiveSessionCount,
	); err != nil {
		return ManagedAccount{}, err
	}
	account.Role = auth.Role(role)
	account.CreatedAt = account.CreatedAt.UTC()
	account.UpdatedAt = account.UpdatedAt.UTC()
	if account.DisabledAt != nil {
		value := account.DisabledAt.UTC()
		account.DisabledAt = &value
	}
	return account, nil
}

func scanManagedStudent(scanner rowScanner) (ManagedStudent, error) {
	var student ManagedStudent
	var accountID *string
	var username *string
	var accountDisplayName *string
	var disabledAt *time.Time
	var ratingText *string
	if err := scanner.Scan(
		&student.StudentNumber,
		&student.PintiaUserID,
		&student.SourceDisplayName,
		&accountID,
		&username,
		&accountDisplayName,
		&disabledAt,
		&ratingText,
	); err != nil {
		return ManagedStudent{}, databaseFailure("scan managed student", err)
	}
	if accountID == nil {
		if username != nil || accountDisplayName != nil || disabledAt != nil {
			return ManagedStudent{}, adminError(ErrorStoredDataInvalid, "scan managed student", errors.New("partial account binding"))
		}
	} else {
		if username == nil || accountDisplayName == nil {
			return ManagedStudent{}, adminError(ErrorStoredDataInvalid, "scan managed student", errors.New("incomplete account binding"))
		}
		binding := &StudentAccountBinding{ID: *accountID, Username: *username, DisplayName: *accountDisplayName}
		if disabledAt != nil {
			value := disabledAt.UTC()
			binding.DisabledAt = &value
		}
		student.Account = binding
	}
	if ratingText != nil {
		rating, err := strconv.ParseInt(*ratingText, 10, 64)
		if err != nil || rating < 0 || strconv.FormatInt(rating, 10) != *ratingText {
			return ManagedStudent{}, adminError(ErrorStoredDataInvalid, "scan managed student", errors.New("rating is not a canonical non-negative int64"))
		}
		student.Rating = &rating
	}
	return student, nil
}

func appendAccountStateAudit(
	ctx context.Context,
	tx postgresTx,
	actor principalguard.Resolved,
	command AccountStateCommand,
	mutationTime time.Time,
) error {
	eventType := "admin.account_enabled"
	if command.Disabled {
		eventType = "admin.account_disabled"
	}
	payload, err := json.Marshal(map[string]any{
		"targetAccountId": command.TargetID,
		"disabled":        command.Disabled,
	})
	if err != nil {
		return adminError(ErrorStoredDataInvalid, "encode managed account audit", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO ascendany.audit_events (
    account_id,
    session_id,
    event_type,
    occurred_at,
    payload
)
VALUES ($1, $2, $3, $4, $5::jsonb)`, actor.AccountDatabaseID, actor.SessionDatabaseID, eventType, mutationTime, string(payload)); err != nil {
		return databaseFailure("append managed account audit", err)
	}
	return nil
}

func resolveAdminPrincipal(ctx context.Context, tx postgresTx, principal auth.AccessPrincipal, lock bool) error {
	var err error
	if lock {
		_, err = principalguard.ResolveForUpdate(ctx, tx, principal, principalguard.Roles(auth.RoleAdmin))
	} else {
		_, err = principalguard.Resolve(ctx, tx, principal, principalguard.Roles(auth.RoleAdmin))
	}
	if err == nil {
		return nil
	}
	return mapPrincipalError("revalidate administration principal", err)
}

func mapPrincipalError(operation string, err error) error {
	switch principalguard.CodeOf(err) {
	case principalguard.ErrorRejected:
		return adminError(ErrorPrincipalRejected, operation, err)
	case principalguard.ErrorCanceled:
		return adminError(ErrorCanceled, operation, err)
	case principalguard.ErrorDatabase:
		return adminError(ErrorDatabase, operation, err)
	default:
		return adminError(ErrorStoredDataInvalid, operation, err)
	}
}

func (repository *PostgresRepository) transaction(
	ctx context.Context,
	operation string,
	readOnly bool,
	run func(postgresTx) error,
) error {
	const maxMutationAttempts = 3
	for attempt := 1; attempt <= maxMutationAttempts; attempt++ {
		err := repository.transactionOnce(ctx, operation, readOnly, run)
		if err == nil || readOnly || !retryableTransactionConflict(err) {
			return err
		}
		if attempt == maxMutationAttempts {
			return adminError(ErrorConcurrentMutation, operation, err)
		}
	}
	return adminError(ErrorStoredDataInvalid, operation, errors.New("transaction retry loop terminated unexpectedly"))
}

func (repository *PostgresRepository) transactionOnce(
	ctx context.Context,
	operation string,
	readOnly bool,
	run func(postgresTx) error,
) (resultErr error) {
	options := pgx.TxOptions{IsoLevel: pgx.RepeatableRead}
	if readOnly {
		options.AccessMode = pgx.ReadOnly
	} else {
		// The ordered account locks serialize state changes. READ COMMITTED gives
		// statements after a lock wait a fresh snapshot, so a login session that
		// committed while holding the target account lock is also revoked.
		options.IsoLevel = pgx.ReadCommitted
		options.AccessMode = pgx.ReadWrite
	}
	tx, err := repository.begin(ctx, options)
	if err != nil {
		return databaseFailure("begin "+operation, err)
	}
	finished := false
	defer func() {
		if finished {
			return
		}
		rollbackContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if rollbackErr := tx.Rollback(rollbackContext); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			wrapped := databaseFailure("rollback "+operation, rollbackErr)
			if resultErr == nil {
				resultErr = wrapped
			} else {
				resultErr = errors.Join(resultErr, wrapped)
			}
		}
	}()
	if err := run(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return databaseFailure("commit "+operation, err)
	}
	finished = true
	return nil
}

func retryableTransactionConflict(err error) bool {
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) {
		return false
	}
	return postgresError.Code == "40001" || postgresError.Code == "40P01"
}

func validateAccountStateCommand(command AccountStateCommand) error {
	if !canonicalUUIDv4.MatchString(command.TargetID) {
		return adminError(ErrorInvalidQuery, "validate account state command", fmt.Errorf("target is invalid"))
	}
	return validateAdminPrincipal(command.Principal)
}
