package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type PgxBeginner interface {
	Begin(context.Context) (pgx.Tx, error)
}

type postgresTx interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	QueryRow(context.Context, string, ...any) pgx.Row
	Commit(context.Context) error
	Rollback(context.Context) error
}

type beginTransaction func(context.Context) (postgresTx, error)

type PostgresRepository struct {
	begin beginTransaction
}

func NewPostgresRepository(pool PgxBeginner) (*PostgresRepository, error) {
	if pool == nil {
		return nil, authError(ErrorInvalidConfiguration, "PostgreSQL pool is required.", nil)
	}
	return &PostgresRepository{
		begin: func(ctx context.Context) (postgresTx, error) { return pool.Begin(ctx) },
	}, nil
}

func newPostgresRepository(begin beginTransaction) (*PostgresRepository, error) {
	if begin == nil {
		return nil, authError(ErrorInvalidConfiguration, "PostgreSQL transaction beginner is required.", nil)
	}
	return &PostgresRepository{begin: begin}, nil
}

func (r *PostgresRepository) transaction(ctx context.Context, operation string, run func(postgresTx) error) (resultErr error) {
	if ctx == nil {
		return authError(ErrorDatabase, "Authentication storage failed.", errors.New("context is required"))
	}
	tx, err := r.begin(ctx)
	if err != nil {
		return databaseFailure(operation, err)
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

// BootstrapFirstAdmin creates the sole initial administrator under a
// transaction-scoped advisory lock. No HTTP path calls this method.
func (r *PostgresRepository) BootstrapFirstAdmin(
	ctx context.Context,
	command AdminBootstrapCommand,
) (AdminBootstrapResult, error) {
	if err := validateAccountRecord(command.Account); err != nil {
		return AdminBootstrapResult{}, err
	}
	if command.Account.Role != RoleAdmin || command.Account.StudentNumber != nil {
		return AdminBootstrapResult{}, authError(ErrorInternal, "Admin bootstrap account is invalid.", nil)
	}
	if command.Now.IsZero() {
		return AdminBootstrapResult{}, authError(ErrorInternal, "Admin bootstrap timestamp is invalid.", nil)
	}

	result := AdminBootstrapResult{Status: AdminBootstrapAlreadyExists}
	err := r.transaction(ctx, "bootstrap first admin", func(tx postgresTx) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(4706902253123607891)`); err != nil {
			return databaseFailure("lock admin bootstrap", err)
		}
		var adminExists bool
		if err := tx.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1
    FROM ascendany.auth_accounts
    WHERE role = 'admin'
)`).Scan(&adminExists); err != nil {
			return databaseFailure("check existing administrator", err)
		}
		if adminExists {
			return nil
		}

		var accountDatabaseID int64
		err := tx.QueryRow(ctx, `
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
VALUES ($1::uuid, NULL, $2, $3, $4, NULL, 'admin', $5, $6, $6)
ON CONFLICT (username) DO NOTHING
RETURNING account_id`,
			command.Account.ID,
			command.Account.Username,
			command.Account.PasswordPHC,
			command.Account.DisplayName,
			command.Account.AuthRevision,
			command.Now,
		).Scan(&accountDatabaseID)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return databaseFailure("insert first administrator", err)
		}
		if err := appendAuditEvent(ctx, tx, accountDatabaseID, 0, "auth.admin_bootstrap", command.Now); err != nil {
			return err
		}
		result = AdminBootstrapResult{Status: AdminBootstrapCreated, Account: command.Account}
		return nil
	})
	return result, err
}

func (r *PostgresRepository) FindLoginAccount(ctx context.Context, username string) (AccountRecord, bool, error) {
	if ctx == nil {
		return AccountRecord{}, false, databaseFailure("find login account", errors.New("context is required"))
	}
	account, err := scanAccount(r.queryRow(ctx, `
SELECT
    account.public_id::text,
    account.username,
    account.display_name,
    account.student_number,
    account.pta_nickname,
    account.role,
    account.auth_revision,
    account.password_phc,
    account.disabled_at
FROM ascendany.auth_accounts AS account
WHERE account.username = $1`, username))
	if errors.Is(err, pgx.ErrNoRows) {
		return AccountRecord{}, false, nil
	}
	if err != nil {
		return AccountRecord{}, false, databaseFailure("find login account", err)
	}
	return account, true, nil
}

func (r *PostgresRepository) CreateSession(ctx context.Context, command CreateSessionCommand) (CreateSessionResult, error) {
	if err := validateCreateSessionCommand(command); err != nil {
		return CreateSessionResult{}, err
	}
	result := CreateSessionResult{Status: SessionRejected}
	err := r.transaction(ctx, "create auth session", func(tx postgresTx) error {
		account, accountDatabaseID, err := scanAccountStateWithDatabaseID(tx.QueryRow(ctx, `
SELECT
    account.account_id,
    account.public_id::text,
    account.username,
    account.display_name,
    account.student_number,
    account.pta_nickname,
    account.role,
    account.auth_revision,
    account.disabled_at
FROM ascendany.auth_accounts AS account
WHERE account.public_id = $1::uuid
FOR UPDATE OF account`, command.AccountID))
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return databaseFailure("lock login account", err)
		}
		if account.DisabledAt != nil || account.AuthRevision != command.ExpectedAuthRevision {
			return nil
		}
		sessionDatabaseID, err := insertSessionAndRefresh(
			ctx,
			tx,
			accountDatabaseID,
			account.AuthRevision,
			command.SessionID,
			command.Now,
			command.SessionExpiry,
			command.RefreshToken,
		)
		if err != nil {
			return err
		}
		if err := appendAuditEvent(ctx, tx, accountDatabaseID, sessionDatabaseID, "auth.login", command.Now); err != nil {
			return err
		}
		result = CreateSessionResult{Status: SessionCreated, Account: account}
		return nil
	})
	return result, err
}

func (r *PostgresRepository) TransactRefresh(
	ctx context.Context,
	tokenID string,
	now time.Time,
	decide RefreshDecider,
) (RefreshDecisionKind, error) {
	if _, err := parseUUIDv4(tokenID); err != nil {
		return 0, authError(ErrorInternal, "Refresh transaction token ID is invalid.", err)
	}
	if decide == nil {
		return 0, authError(ErrorInvalidConfiguration, "Refresh transaction decider is required.", nil)
	}
	committedDecision := RefreshReject
	err := r.transaction(ctx, "transact refresh token", func(tx postgresTx) error {
		snapshot, err := loadRefreshSnapshot(ctx, tx, tokenID)
		if err != nil {
			return err
		}
		decision := decide(snapshot)
		if err := validateRefreshDecision(snapshot, decision, now); err != nil {
			return err
		}
		switch decision.Kind {
		case RefreshReject:
		case RefreshRotate:
			if err := rotateRefreshToken(ctx, tx, snapshot, *decision.NextToken, now); err != nil {
				return err
			}
		case RefreshRevokeReuse:
			if err := revokeSession(ctx, tx, snapshot, "refresh_reuse", "auth.refresh_reuse", now); err != nil {
				return err
			}
		case RefreshLogout:
			if err := revokeSession(ctx, tx, snapshot, "logout", "auth.logout", now); err != nil {
				return err
			}
		default:
			return authError(ErrorInternal, "Refresh transaction decision is invalid.", nil)
		}
		committedDecision = decision.Kind
		return nil
	})
	return committedDecision, err
}

func (r *PostgresRepository) LoadPrincipal(
	ctx context.Context,
	accountID string,
	sessionID string,
	_ time.Time,
) (PrincipalSnapshot, error) {
	if ctx == nil {
		return PrincipalSnapshot{}, databaseFailure("load access principal", errors.New("context is required"))
	}
	if _, err := parseUUIDv4(accountID); err != nil {
		return PrincipalSnapshot{}, authError(ErrorInternal, "Principal account ID is invalid.", err)
	}
	if _, err := parseUUIDv4(sessionID); err != nil {
		return PrincipalSnapshot{}, authError(ErrorInternal, "Principal session ID is invalid.", err)
	}
	row := r.queryRow(ctx, `
SELECT
    account.public_id::text,
    account.username,
    account.display_name,
    account.student_number,
    account.pta_nickname,
    account.role,
    account.auth_revision,
    account.disabled_at,
    session.session_id,
    session.public_id::text,
    session.auth_revision,
    session.created_at,
    session.expires_at,
    session.last_seen_at,
    session.revoked_at
FROM ascendany.auth_accounts AS account
JOIN ascendany.auth_sessions AS session
    ON session.account_id = account.account_id
WHERE account.public_id = $1::uuid
  AND session.public_id = $2::uuid`, accountID, sessionID)
	account, session, err := scanPrincipal(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return PrincipalSnapshot{Found: false}, nil
	}
	if err != nil {
		return PrincipalSnapshot{}, databaseFailure("load access principal", err)
	}
	return PrincipalSnapshot{Found: true, Account: account, Session: session}, nil
}

func (r *PostgresRepository) queryRow(ctx context.Context, sql string, arguments ...any) pgx.Row {
	return &beginnerRow{begin: r.begin, ctx: ctx, sql: sql, arguments: arguments}
}

// beginnerRow keeps the repository constructor transaction-only. Read queries
// execute in a read-only transaction and close it immediately after Scan.
type beginnerRow struct {
	begin     beginTransaction
	ctx       context.Context
	sql       string
	arguments []any
}

func (r *beginnerRow) Scan(destinations ...any) error {
	tx, err := r.begin(r.ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := tx.QueryRow(r.ctx, r.sql, r.arguments...).Scan(destinations...); err != nil {
		return err
	}
	return tx.Commit(r.ctx)
}

func insertSessionAndRefresh(
	ctx context.Context,
	tx postgresTx,
	accountDatabaseID int64,
	authRevision int64,
	sessionID string,
	now time.Time,
	sessionExpiry time.Time,
	refresh NewRefreshToken,
) (int64, error) {
	var sessionDatabaseID int64
	err := tx.QueryRow(ctx, `
INSERT INTO ascendany.auth_sessions (
    public_id,
    account_id,
    auth_revision,
    created_at,
    expires_at,
    last_seen_at
)
VALUES ($1::uuid, $2, $3, $4, $5, $4)
RETURNING session_id`, sessionID, accountDatabaseID, authRevision, now, sessionExpiry).Scan(&sessionDatabaseID)
	if err != nil {
		return 0, databaseFailure("insert auth session", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO ascendany.auth_refresh_tokens (
    public_id,
    session_id,
    secret_digest,
    csrf_digest,
    created_at,
    expires_at
)
VALUES ($1::uuid, $2, $3, $4, $5, $6)`,
		refresh.ID,
		sessionDatabaseID,
		refresh.SecretDigest[:],
		refresh.CSRFDigest[:],
		refresh.CreatedAt,
		refresh.ExpiresAt,
	); err != nil {
		return 0, databaseFailure("insert auth refresh token", err)
	}
	return sessionDatabaseID, nil
}

func loadRefreshSnapshot(ctx context.Context, tx postgresTx, tokenID string) (RefreshSnapshot, error) {
	var snapshot RefreshSnapshot
	var secretDigest []byte
	var csrfDigest []byte
	err := tx.QueryRow(ctx, `
SELECT
    refresh.refresh_token_id,
    refresh.public_id::text,
    refresh.secret_digest,
    refresh.csrf_digest,
    refresh.expires_at,
    refresh.used_at,
    refresh.revoked_at,
    session.session_id,
    account.account_id,
    session.public_id::text,
    account.public_id::text,
    session.auth_revision,
    session.created_at,
    session.expires_at,
    session.last_seen_at,
    session.revoked_at,
    account.username,
    account.display_name,
    account.student_number,
    account.pta_nickname,
    account.role,
    account.auth_revision,
    account.disabled_at
FROM ascendany.auth_refresh_tokens AS refresh
JOIN ascendany.auth_sessions AS session
    ON session.session_id = refresh.session_id
JOIN ascendany.auth_accounts AS account
    ON account.account_id = session.account_id
WHERE refresh.public_id = $1::uuid
FOR UPDATE OF refresh, session, account`, tokenID).Scan(
		&snapshot.TokenDatabaseID,
		&snapshot.TokenID,
		&secretDigest,
		&csrfDigest,
		&snapshot.TokenExpiresAt,
		&snapshot.UsedAt,
		&snapshot.TokenRevokedAt,
		&snapshot.Session.DatabaseID,
		&snapshot.AccountDatabaseID,
		&snapshot.Session.ID,
		&snapshot.Session.AccountID,
		&snapshot.Session.AuthRevision,
		&snapshot.Session.CreatedAt,
		&snapshot.Session.ExpiresAt,
		&snapshot.Session.LastSeenAt,
		&snapshot.Session.RevokedAt,
		&snapshot.Account.Username,
		&snapshot.Account.DisplayName,
		&snapshot.Account.StudentNumber,
		&snapshot.Account.PTANickname,
		&snapshot.Account.Role,
		&snapshot.Account.AuthRevision,
		&snapshot.Account.DisabledAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return RefreshSnapshot{Found: false}, nil
	}
	if err != nil {
		return RefreshSnapshot{}, databaseFailure("lock refresh token", err)
	}
	snapshot.Account.ID = snapshot.Session.AccountID
	if len(secretDigest) != 32 || len(csrfDigest) != 32 {
		return RefreshSnapshot{}, databaseFailure("decode refresh token digests", errors.New("digest length is not 32 bytes"))
	}
	copy(snapshot.SecretDigest[:], secretDigest)
	copy(snapshot.CSRFDigest[:], csrfDigest)
	snapshot.Found = true
	return snapshot, nil
}

func rotateRefreshToken(
	ctx context.Context,
	tx postgresTx,
	snapshot RefreshSnapshot,
	next NewRefreshToken,
	now time.Time,
) error {
	var nextDatabaseID int64
	err := tx.QueryRow(ctx, `
INSERT INTO ascendany.auth_refresh_tokens (
    public_id,
    session_id,
    secret_digest,
    csrf_digest,
    created_at,
    expires_at
)
VALUES ($1::uuid, $2, $3, $4, $5, $6)
RETURNING refresh_token_id`,
		next.ID,
		snapshot.Session.DatabaseID,
		next.SecretDigest[:],
		next.CSRFDigest[:],
		next.CreatedAt,
		next.ExpiresAt,
	).Scan(&nextDatabaseID)
	if err != nil {
		return databaseFailure("insert rotated refresh token", err)
	}
	tag, err := tx.Exec(ctx, `
UPDATE ascendany.auth_refresh_tokens
SET used_at = $2,
    replaced_by_token_id = $3
WHERE refresh_token_id = $1
  AND used_at IS NULL
  AND revoked_at IS NULL`, snapshot.TokenDatabaseID, now, nextDatabaseID)
	if err != nil {
		return databaseFailure("consume refresh token", err)
	}
	if tag.RowsAffected() != 1 {
		return databaseFailure("consume refresh token", errors.New("locked token was not consumable"))
	}
	if _, err := tx.Exec(ctx, `
UPDATE ascendany.auth_sessions
SET last_seen_at = $2
WHERE session_id = $1`, snapshot.Session.DatabaseID, now); err != nil {
		return databaseFailure("touch auth session", err)
	}
	return appendAuditEvent(ctx, tx, snapshot.AccountDatabaseID, snapshot.Session.DatabaseID, "auth.refresh", now)
}

func revokeSession(
	ctx context.Context,
	tx postgresTx,
	snapshot RefreshSnapshot,
	reason string,
	eventType string,
	now time.Time,
) error {
	if _, err := tx.Exec(ctx, `
UPDATE ascendany.auth_sessions
SET revoked_at = COALESCE(revoked_at, $2),
    revocation_reason = COALESCE(revocation_reason, $3)
WHERE session_id = $1`, snapshot.Session.DatabaseID, now, reason); err != nil {
		return databaseFailure("revoke auth session", err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE ascendany.auth_refresh_tokens
SET revoked_at = COALESCE(revoked_at, $2)
WHERE session_id = $1`, snapshot.Session.DatabaseID, now); err != nil {
		return databaseFailure("revoke refresh token family", err)
	}
	return appendAuditEvent(ctx, tx, snapshot.AccountDatabaseID, snapshot.Session.DatabaseID, eventType, now)
}

func appendAuditEvent(
	ctx context.Context,
	tx postgresTx,
	accountDatabaseID int64,
	sessionDatabaseID int64,
	eventType string,
	now time.Time,
) error {
	var account any
	if accountDatabaseID > 0 {
		account = accountDatabaseID
	}
	var session any
	if sessionDatabaseID > 0 {
		session = sessionDatabaseID
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO ascendany.audit_events (
    account_id,
    session_id,
    event_type,
    occurred_at,
    payload
)
VALUES ($1, $2, $3, $4, '{}'::jsonb)`, account, session, eventType, now); err != nil {
		return databaseFailure("append auth audit event", err)
	}
	return nil
}

func validateCreateSessionCommand(command CreateSessionCommand) error {
	if _, err := parseUUIDv4(command.AccountID); err != nil {
		return authError(ErrorInternal, "Login account ID is invalid.", err)
	}
	if _, err := parseUUIDv4(command.SessionID); err != nil {
		return authError(ErrorInternal, "Login session ID is invalid.", err)
	}
	if command.ExpectedAuthRevision < 1 || !command.SessionExpiry.After(command.Now) {
		return authError(ErrorInternal, "Login session command is invalid.", nil)
	}
	return validateNewRefreshToken(command.RefreshToken, command.Now, command.SessionExpiry)
}

func validateAccountRecord(account AccountRecord) error {
	if _, err := parseUUIDv4(account.ID); err != nil {
		return authError(ErrorInternal, "Account ID is invalid.", err)
	}
	if err := validateUsername(account.Username); err != nil {
		return authError(ErrorInternal, "Account username is invalid.", err)
	}
	studentIdentityValid := account.Role == RoleStudent && account.StudentNumber != nil &&
		len(*account.StudentNumber) >= MinStudentNumberBytes &&
		len(*account.StudentNumber) <= MaxStudentNumberBytes &&
		strings.TrimSpace(*account.StudentNumber) == *account.StudentNumber
	adminIdentityValid := account.Role == RoleAdmin && account.StudentNumber == nil
	ptaIdentityValid := (account.Role == RoleStudent && (account.PTANickname == nil ||
		validEnrollmentStoredField(*account.PTANickname, MinPTANicknameBytes, MaxPTANicknameBytes))) ||
		(account.Role == RoleAdmin && account.PTANickname == nil)
	if len(account.DisplayName) < MinDisplayNameBytes || len(account.DisplayName) > MaxDisplayNameBytes ||
		strings.TrimSpace(account.DisplayName) != account.DisplayName ||
		(!studentIdentityValid && !adminIdentityValid) || !ptaIdentityValid ||
		!validRole(account.Role) || account.AuthRevision < 1 || account.PasswordPHC == "" {
		return authError(ErrorInternal, "Account record is invalid.", nil)
	}
	return nil
}

func validateNewRefreshToken(token NewRefreshToken, now, sessionExpiry time.Time) error {
	if _, err := parseUUIDv4(token.ID); err != nil {
		return authError(ErrorInternal, "Refresh token ID is invalid.", err)
	}
	if !token.CreatedAt.Equal(now) || !token.ExpiresAt.Equal(sessionExpiry) {
		return authError(ErrorInternal, "Refresh token transaction boundary is invalid.", nil)
	}
	return nil
}

func validateRefreshDecision(snapshot RefreshSnapshot, decision RefreshDecision, now time.Time) error {
	switch decision.Kind {
	case RefreshReject:
		if decision.NextToken != nil {
			return authError(ErrorInternal, "Rejected refresh decision contains a token.", nil)
		}
		return nil
	case RefreshRotate:
		if !snapshot.Found || decision.NextToken == nil {
			return authError(ErrorInternal, "Rotate decision is missing locked state or its replacement token.", nil)
		}
		return validateNewRefreshToken(*decision.NextToken, now, snapshot.Session.ExpiresAt)
	case RefreshRevokeReuse, RefreshLogout:
		if !snapshot.Found || decision.NextToken != nil {
			return authError(ErrorInternal, "Revocation decision is invalid.", nil)
		}
		return nil
	default:
		return authError(ErrorInternal, "Refresh decision kind is invalid.", nil)
	}
}

func scanAccount(row pgx.Row) (AccountRecord, error) {
	var account AccountRecord
	err := row.Scan(
		&account.ID,
		&account.Username,
		&account.DisplayName,
		&account.StudentNumber,
		&account.PTANickname,
		&account.Role,
		&account.AuthRevision,
		&account.PasswordPHC,
		&account.DisabledAt,
	)
	return account, err
}

func scanAccountStateWithDatabaseID(row pgx.Row) (AccountRecord, int64, error) {
	var account AccountRecord
	var databaseID int64
	err := row.Scan(
		&databaseID,
		&account.ID,
		&account.Username,
		&account.DisplayName,
		&account.StudentNumber,
		&account.PTANickname,
		&account.Role,
		&account.AuthRevision,
		&account.DisabledAt,
	)
	return account, databaseID, err
}

func scanPrincipal(row pgx.Row) (AccountRecord, SessionRecord, error) {
	var account AccountRecord
	var session SessionRecord
	err := row.Scan(
		&account.ID,
		&account.Username,
		&account.DisplayName,
		&account.StudentNumber,
		&account.PTANickname,
		&account.Role,
		&account.AuthRevision,
		&account.DisabledAt,
		&session.DatabaseID,
		&session.ID,
		&session.AuthRevision,
		&session.CreatedAt,
		&session.ExpiresAt,
		&session.LastSeenAt,
		&session.RevokedAt,
	)
	session.AccountID = account.ID
	return account, session, err
}

func databaseFailure(operation string, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return canceled(err)
	}
	return authError(ErrorDatabase, "Authentication storage failed.", fmt.Errorf("%s: %w", operation, err))
}
