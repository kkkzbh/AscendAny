package administration

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

type accountStateOutcome struct {
	account ManagedAccount
	err     error
}

type signalingAdministrationTx struct {
	postgresTx
	accountLockReached chan struct{}
	once               *sync.Once
}

func (tx *signalingAdministrationTx) Query(
	ctx context.Context,
	query string,
	arguments ...any,
) (pgx.Rows, error) {
	if strings.Contains(query, "ORDER BY account_id") && strings.Contains(query, "FOR UPDATE") {
		tx.once.Do(func() { close(tx.accountLockReached) })
	}
	return tx.postgresTx.Query(ctx, query, arguments...)
}

type barrierAdministrationTx struct {
	postgresTx
	accountLockReached chan<- struct{}
	accountLockRelease <-chan struct{}
	once               *sync.Once
}

func (tx *barrierAdministrationTx) Query(
	ctx context.Context,
	query string,
	arguments ...any,
) (pgx.Rows, error) {
	if strings.Contains(query, "ORDER BY account_id") && strings.Contains(query, "FOR UPDATE") {
		tx.once.Do(func() { tx.accountLockReached <- struct{}{} })
		select {
		case <-tx.accountLockRelease:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return tx.postgresTx.Query(ctx, query, arguments...)
}

func TestPostgresAdministrationReadModelsUseOneActiveAdminPrincipal(t *testing.T) {
	databaseURL := os.Getenv("ASCENDANY_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ASCENDANY_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	principal, found := loadActiveAdminPrincipal(t, ctx, pool)
	if !found {
		t.Skip("integration database has no active admin session")
	}
	repository, err := NewPostgresRepository(pool)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewProductionService(repository)
	if err != nil {
		t.Fatal(err)
	}
	accounts, err := service.ListAccounts(ctx, AccountQuery{Principal: principal, Limit: 2})
	if err != nil || len(accounts.Items) == 0 {
		t.Fatalf("ListAccounts() page=%#v error=%v", accounts, err)
	}
	students, err := service.ListStudents(ctx, StudentQuery{Principal: principal, Limit: 2})
	if err != nil {
		t.Fatalf("ListStudents() error=%v", err)
	}
	if len(students.Items) > 0 && students.Items[0].StudentNumber == "" {
		t.Fatalf("ListStudents() page=%#v", students)
	}
	if _, err := service.ListAudit(ctx, AuditQuery{Principal: principal, Limit: 2}); err != nil {
		t.Fatalf("ListAudit() error=%v", err)
	}
}

func TestPostgresAccountDisableOrdersAfterConcurrentLoginSession(t *testing.T) {
	databaseURL := os.Getenv("ASCENDANY_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ASCENDANY_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		t.Fatal(err)
	}

	actor := insertAdministrationAdmin(t, ctx, pool, true)
	target := insertAdministrationAdmin(t, ctx, pool, false)
	loginTx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	loginFinished := false
	defer func() {
		if !loginFinished {
			_ = loginTx.Rollback(context.Background())
		}
	}()
	var targetDatabaseID int64
	var targetRevision int64
	var disabledAt *time.Time
	if err := loginTx.QueryRow(ctx, `
SELECT account_id, auth_revision, disabled_at
FROM ascendany.auth_accounts
WHERE public_id = $1::uuid
FOR UPDATE`, target.Principal.AccountID).Scan(&targetDatabaseID, &targetRevision, &disabledAt); err != nil {
		t.Fatal(err)
	}
	if targetDatabaseID != target.DatabaseID || targetRevision != 1 || disabledAt != nil {
		t.Fatalf("locked login target id=%d revision=%d disabledAt=%v", targetDatabaseID, targetRevision, disabledAt)
	}

	accountLockReached := make(chan struct{})
	var accountLockSignal sync.Once
	repository, err := newPostgresRepository(func(ctx context.Context, options pgx.TxOptions) (postgresTx, error) {
		tx, beginErr := pool.BeginTx(ctx, options)
		if beginErr != nil {
			return nil, beginErr
		}
		return &signalingAdministrationTx{
			postgresTx:         tx,
			accountLockReached: accountLockReached,
			once:               &accountLockSignal,
		}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	outcome := make(chan accountStateOutcome, 1)
	go func() {
		account, mutationErr := repository.SetAccountDisabled(ctx, AccountStateCommand{
			Principal: actor.Principal,
			TargetID:  target.Principal.AccountID,
			Disabled:  true,
		})
		outcome <- accountStateOutcome{account: account, err: mutationErr}
	}()
	select {
	case <-accountLockReached:
	case <-ctx.Done():
		t.Fatalf("administration mutation did not reach the ordered account lock: %v", ctx.Err())
	}

	concurrentSessionID := randomUUIDv4(t)
	var sessionDatabaseID int64
	var sessionCreatedAt time.Time
	if err := loginTx.QueryRow(ctx, `
WITH instant AS (
    SELECT clock_timestamp() AS value
)
INSERT INTO ascendany.auth_sessions (
    public_id,
    account_id,
    auth_revision,
    created_at,
    expires_at,
    last_seen_at
)
SELECT $1::uuid,
       $2,
       $3,
       instant.value,
       instant.value + interval '1 hour',
       instant.value
FROM instant
RETURNING session_id, created_at`, concurrentSessionID, targetDatabaseID, targetRevision).Scan(&sessionDatabaseID, &sessionCreatedAt); err != nil {
		t.Fatal(err)
	}
	if err := loginTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	loginFinished = true

	var mutation accountStateOutcome
	select {
	case mutation = <-outcome:
	case <-ctx.Done():
		t.Fatalf("administration mutation did not finish after login commit: %v", ctx.Err())
	}
	if mutation.err != nil {
		t.Fatalf("SetAccountDisabled() error = %v", mutation.err)
	}
	if mutation.account.DisabledAt == nil || mutation.account.ActiveSessionCount != 0 || mutation.account.AuthRevision != 2 {
		t.Fatalf("disabled account=%#v", mutation.account)
	}

	var storedDisabledAt time.Time
	var revokedAt *time.Time
	var revocationReason *string
	var auditOccurredAt time.Time
	if err := pool.QueryRow(ctx, `
SELECT account.disabled_at,
       session.revoked_at,
       session.revocation_reason,
       audit.occurred_at
FROM ascendany.auth_accounts AS account
JOIN ascendany.auth_sessions AS session
  ON session.session_id = $2
JOIN ascendany.audit_events AS audit
  ON audit.account_id = $3
 AND audit.session_id = $4
 AND audit.event_type = 'admin.account_disabled'
WHERE account.account_id = $1
ORDER BY audit.audit_event_id DESC
LIMIT 1`, targetDatabaseID, sessionDatabaseID, actor.DatabaseID, actor.SessionDatabaseID).Scan(
		&storedDisabledAt,
		&revokedAt,
		&revocationReason,
		&auditOccurredAt,
	); err != nil {
		t.Fatal(err)
	}
	if revokedAt == nil || revocationReason == nil || *revocationReason != "admin_disabled" {
		t.Fatalf("concurrent login session remained active: revokedAt=%v reason=%v", revokedAt, revocationReason)
	}
	if revokedAt.Before(sessionCreatedAt) || !storedDisabledAt.Equal(*revokedAt) ||
		!auditOccurredAt.Equal(*revokedAt) || !mutation.account.DisabledAt.Equal(*revokedAt) {
		t.Fatalf(
			"timestamps session=%s account=%s revoked=%s audit=%s result=%s",
			sessionCreatedAt,
			storedDisabledAt,
			revokedAt,
			auditOccurredAt,
			mutation.account.DisabledAt,
		)
	}
}

func TestPostgresMutualAdminDisableUsesOneAccountLockOrder(t *testing.T) {
	databaseURL := os.Getenv("ASCENDANY_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ASCENDANY_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		t.Fatal(err)
	}

	first := insertAdministrationAdmin(t, ctx, pool, true)
	second := insertAdministrationAdmin(t, ctx, pool, true)
	accountLockReached := make(chan struct{}, 2)
	accountLockRelease := make(chan struct{})
	newBarrierRepository := func() *PostgresRepository {
		t.Helper()
		var accountLockSignal sync.Once
		repository, repositoryErr := newPostgresRepository(func(ctx context.Context, options pgx.TxOptions) (postgresTx, error) {
			tx, beginErr := pool.BeginTx(ctx, options)
			if beginErr != nil {
				return nil, beginErr
			}
			return &barrierAdministrationTx{
				postgresTx:         tx,
				accountLockReached: accountLockReached,
				accountLockRelease: accountLockRelease,
				once:               &accountLockSignal,
			}, nil
		})
		if repositoryErr != nil {
			t.Fatal(repositoryErr)
		}
		return repository
	}
	firstRepository := newBarrierRepository()
	secondRepository := newBarrierRepository()
	firstOutcome := make(chan accountStateOutcome, 1)
	secondOutcome := make(chan accountStateOutcome, 1)
	go func() {
		account, mutationErr := firstRepository.SetAccountDisabled(ctx, AccountStateCommand{
			Principal: first.Principal,
			TargetID:  second.Principal.AccountID,
			Disabled:  true,
		})
		firstOutcome <- accountStateOutcome{account: account, err: mutationErr}
	}()
	go func() {
		account, mutationErr := secondRepository.SetAccountDisabled(ctx, AccountStateCommand{
			Principal: second.Principal,
			TargetID:  first.Principal.AccountID,
			Disabled:  true,
		})
		secondOutcome <- accountStateOutcome{account: account, err: mutationErr}
	}()
	for range 2 {
		select {
		case <-accountLockReached:
		case <-ctx.Done():
			t.Fatalf("both mutations did not reach the shared ordered lock: %v", ctx.Err())
		}
	}
	close(accountLockRelease)

	var outcomes [2]accountStateOutcome
	for index, channel := range []<-chan accountStateOutcome{firstOutcome, secondOutcome} {
		select {
		case outcomes[index] = <-channel:
		case <-ctx.Done():
			t.Fatalf("mutual administration mutations did not finish: %v", ctx.Err())
		}
	}
	var successes int
	var rejected int
	for _, outcome := range outcomes {
		switch CodeOf(outcome.err) {
		case "":
			successes++
			if outcome.account.DisabledAt == nil || outcome.account.ActiveSessionCount != 0 {
				t.Fatalf("successful outcome=%#v", outcome.account)
			}
		case ErrorPrincipalRejected:
			rejected++
		default:
			t.Fatalf("mutual disable returned error=%v code=%q", outcome.err, CodeOf(outcome.err))
		}
	}
	if successes != 1 || rejected != 1 {
		t.Fatalf("mutual disable outcomes=%#v", outcomes)
	}
	var disabledCount int
	if err := pool.QueryRow(ctx, `
SELECT count(*)
FROM ascendany.auth_accounts
WHERE public_id = ANY($1::uuid[])
  AND disabled_at IS NOT NULL`, []string{first.Principal.AccountID, second.Principal.AccountID}).Scan(&disabledCount); err != nil {
		t.Fatal(err)
	}
	if disabledCount != 1 {
		t.Fatalf("disabled admins=%d, want 1", disabledCount)
	}
}

type administrationAdminFixture struct {
	Principal         auth.AccessPrincipal
	DatabaseID        int64
	SessionDatabaseID int64
}

func insertAdministrationAdmin(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	withSession bool,
) administrationAdminFixture {
	t.Helper()
	accountID := randomUUIDv4(t)
	username := "adm_" + strings.ReplaceAll(accountID[:18], "-", "")
	var fixture administrationAdminFixture
	fixture.Principal = auth.AccessPrincipal{
		AccountID:    accountID,
		Role:         auth.RoleAdmin,
		AuthRevision: 1,
		JWTID:        randomUUIDv4(t),
	}
	if err := pool.QueryRow(ctx, `
WITH instant AS (
    SELECT clock_timestamp() AS value
)
INSERT INTO ascendany.auth_accounts (
    public_id,
    username,
    password_phc,
    display_name,
    role,
    auth_revision,
    created_at,
    updated_at
)
SELECT $1::uuid,
       $2,
       'integration-test-password-phc',
       $3,
       'admin',
       1,
       instant.value,
       instant.value
FROM instant
RETURNING account_id`, accountID, username, "Integration "+username).Scan(&fixture.DatabaseID); err != nil {
		t.Fatal(err)
	}
	if !withSession {
		return fixture
	}
	fixture.Principal.SessionID = randomUUIDv4(t)
	if err := pool.QueryRow(ctx, `
WITH instant AS (
    SELECT clock_timestamp() AS value
)
INSERT INTO ascendany.auth_sessions (
    public_id,
    account_id,
    auth_revision,
    created_at,
    expires_at,
    last_seen_at
)
SELECT $1::uuid,
       $2,
       1,
       instant.value,
       instant.value + interval '1 hour',
       instant.value
FROM instant
RETURNING session_id`, fixture.Principal.SessionID, fixture.DatabaseID).Scan(&fixture.SessionDatabaseID); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func randomUUIDv4(t *testing.T) string {
	t.Helper()
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		t.Fatal(err)
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	return fmt.Sprintf(
		"%08x-%04x-%04x-%04x-%012x",
		value[0:4],
		value[4:6],
		value[6:8],
		value[8:10],
		value[10:16],
	)
}

func loadActiveAdminPrincipal(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
) (auth.AccessPrincipal, bool) {
	t.Helper()
	principal := auth.AccessPrincipal{Role: auth.RoleAdmin, JWTID: "99999999-9999-4999-8999-999999999999"}
	err := pool.QueryRow(ctx, `
SELECT account.public_id::text,
       session.public_id::text,
       account.auth_revision
FROM ascendany.auth_accounts AS account
JOIN ascendany.auth_sessions AS session
  ON session.account_id = account.account_id
 AND session.auth_revision = account.auth_revision
WHERE account.role = 'admin'
  AND account.disabled_at IS NULL
  AND session.revoked_at IS NULL
  AND session.expires_at > clock_timestamp()
ORDER BY session.session_id DESC
LIMIT 1`).Scan(&principal.AccountID, &principal.SessionID, &principal.AuthRevision)
	if err == pgx.ErrNoRows {
		return auth.AccessPrincipal{}, false
	}
	if err != nil {
		t.Fatal(err)
	}
	return principal, true
}
