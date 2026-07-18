package auth

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type postgresClaimOutcome struct {
	result ClaimEnrollmentResult
	err    error
}

type postgresRevokeOutcome struct {
	status RevokeEnrollmentStatus
	err    error
}

type futurePostgresEnrollment struct {
	GrantID    string
	Serialized string
}

func TestPostgresEnrollmentIssueConcurrentClaimAndRevocation(t *testing.T) {
	databaseURL := os.Getenv("ASCENDANY_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ASCENDANY_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	poolConfig, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	poolConfig.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeExec
	poolConfig.ConnConfig.StatementCacheCapacity = 0
	poolConfig.ConnConfig.DescriptionCacheCapacity = 0
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	var existingAccounts int64
	var existingGrants int64
	if err := pool.QueryRow(ctx, `
SELECT (SELECT count(*) FROM ascendany.auth_accounts),
       (SELECT count(*) FROM ascendany.auth_enrollment_grants)`).Scan(&existingAccounts, &existingGrants); err != nil {
		t.Fatal(err)
	}
	if existingAccounts != 0 || existingGrants != 0 {
		t.Fatalf("auth enrollment integration database is not empty: %d accounts, %d grants", existingAccounts, existingGrants)
	}

	now := time.Now().UTC().Truncate(time.Microsecond)
	repository, err := NewPostgresRepository(pool)
	if err != nil {
		t.Fatal(err)
	}
	passwordWork, err := newPasswordWorkLimiter(4)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(repository, ServiceConfig{
		Issuer:               "ascendany",
		Audience:             "ascendany-v2",
		JWTSigningPrivateKey: testEd25519PrivateKey(0x31),
		PasswordPepper:       []byte("abcdef0123456789abcdef0123456789"),
		AccessTTL:            15 * time.Minute,
		RefreshTTL:           24 * time.Hour,
		Clock:                fixedClock{now: now},
		Random:               rand.Reader,
		passwordWork:         passwordWork,
	})
	if err != nil {
		t.Fatal(err)
	}
	const adminPassword = "integration-admin-password"
	adminPHC, err := service.passwords.Hash(adminPassword)
	if err != nil {
		t.Fatal(err)
	}
	seedPostgresEnrollmentIdentities(t, ctx, pool, adminPHC, now)

	adminSession, err := service.Login(ctx, LoginInput{Username: "enroll_admin", Password: adminPassword})
	if err != nil {
		t.Fatal(err)
	}
	adminPrincipal, err := service.jwt.ParseAt(adminSession.AccessToken, now)
	if err != nil {
		t.Fatal(err)
	}
	issued, err := service.IssueEnrollment(ctx, adminSession.AccessToken, EnrollmentIssueInput{
		Username:      "enroll_student_a",
		DisplayName:   "Enrollment Student A",
		StudentNumber: "integration-student-a",
		ExpiresAt:     now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	digest, err := parseEnrollmentToken(issued.Token)
	if err != nil {
		t.Fatal(err)
	}
	var storedDigest []byte
	if err := pool.QueryRow(ctx, `
SELECT secret_digest
FROM ascendany.auth_enrollment_grants
WHERE public_id = $1::uuid`, issued.Grant.ID).Scan(&storedDigest); err != nil {
		t.Fatal(err)
	}
	if !equalBytes(storedDigest, digest[:]) {
		t.Fatalf("stored digest = %x, want %x", storedDigest, digest)
	}
	var plaintextColumns int64
	if err := pool.QueryRow(ctx, `
SELECT count(*)
FROM information_schema.columns
WHERE table_schema = 'ascendany'
  AND table_name = 'auth_enrollment_grants'
  AND column_name IN ('token', 'secret', 'token_value', 'secret_value')`).Scan(&plaintextColumns); err != nil {
		t.Fatal(err)
	}
	if plaintextColumns != 0 {
		t.Fatalf("enrollment grant schema contains %d plaintext credential columns", plaintextColumns)
	}

	start := make(chan struct{})
	claimErrors := make(chan error, 2)
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, claimErr := service.ClaimEnrollment(ctx, EnrollmentClaimInput{
				Token:    issued.Token,
				Password: "integration-student-password",
			})
			claimErrors <- claimErr
		}()
	}
	close(start)
	wait.Wait()
	close(claimErrors)
	var successfulClaims int
	var rejectedClaims int
	for claimErr := range claimErrors {
		switch ErrorCodeOf(claimErr) {
		case "":
			successfulClaims++
		case ErrorEnrollmentRejected:
			rejectedClaims++
		default:
			t.Fatalf("unexpected concurrent claim error: %v", claimErr)
		}
	}
	if successfulClaims != 1 || rejectedClaims != 1 {
		t.Fatalf("concurrent claims: successful=%d rejected=%d", successfulClaims, rejectedClaims)
	}

	var studentAccounts int64
	var studentSessions int64
	var eventTypes []string
	rows, err := pool.Query(ctx, `
SELECT event.event_type
FROM ascendany.auth_enrollment_events AS event
JOIN ascendany.auth_enrollment_grants AS enrollment
  ON enrollment.enrollment_grant_id = event.enrollment_grant_id
WHERE enrollment.public_id = $1::uuid
ORDER BY event.event_slot`, issued.Grant.ID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var eventType string
		if err := rows.Scan(&eventType); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		eventTypes = append(eventTypes, eventType)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	rows.Close()
	if err := pool.QueryRow(ctx, `
SELECT count(*),
       (SELECT count(*)
        FROM ascendany.auth_sessions AS session
        JOIN ascendany.auth_accounts AS account ON account.account_id = session.account_id
        WHERE account.username = 'enroll_student_a')
FROM ascendany.auth_accounts
WHERE username = 'enroll_student_a'`).Scan(&studentAccounts, &studentSessions); err != nil {
		t.Fatal(err)
	}
	if studentAccounts != 1 || studentSessions != 1 || len(eventTypes) != 2 ||
		eventTypes[0] != "issued" || eventTypes[1] != "consumed" {
		t.Fatalf("student state: accounts=%d sessions=%d events=%v", studentAccounts, studentSessions, eventTypes)
	}

	revoked, err := service.IssueEnrollment(ctx, adminSession.AccessToken, EnrollmentIssueInput{
		Username:      "enroll_student_b",
		DisplayName:   "Enrollment Student B",
		StudentNumber: "integration-student-b",
		ExpiresAt:     now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.RevokeEnrollment(ctx, adminSession.AccessToken, revoked.Grant.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ClaimEnrollment(ctx, EnrollmentClaimInput{
		Token:    revoked.Token,
		Password: "integration-student-password",
	}); ErrorCodeOf(err) != ErrorEnrollmentRejected {
		t.Fatalf("revoked PostgreSQL enrollment was accepted: %v", err)
	}

	racing, err := service.IssueEnrollment(ctx, adminSession.AccessToken, EnrollmentIssueInput{
		Username:      "enroll_student_c",
		DisplayName:   "Enrollment Student C",
		StudentNumber: "integration-student-c",
		ExpiresAt:     now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	racingDigest, err := parseEnrollmentToken(racing.Token)
	if err != nil {
		t.Fatal(err)
	}
	racingPasswordPHC, err := service.passwords.Hash("integration-racing-student-password")
	if err != nil {
		t.Fatal(err)
	}
	racingAccountID, racingSessionID, racingCredential, racingCSRF, _, err := service.issueSessionCredentials()
	if err != nil {
		t.Fatal(err)
	}
	racingSessionExpiry := now.Add(24 * time.Hour)
	claimOutcomeChannel := make(chan postgresClaimOutcome, 1)
	revokeOutcomeChannel := make(chan postgresRevokeOutcome, 1)
	racingStart := make(chan struct{})
	go func() {
		<-racingStart
		result, claimErr := repository.ClaimEnrollment(ctx, ClaimEnrollmentCommand{
			SecretDigest: racingDigest,
			AccountID:    racingAccountID,
			PasswordPHC:  racingPasswordPHC,
			SessionID:    racingSessionID,
			RefreshToken: newRefreshToken(
				racingCredential,
				racingCSRF,
				now,
				racingSessionExpiry,
			),
			Now:           now,
			SessionExpiry: racingSessionExpiry,
		})
		claimOutcomeChannel <- postgresClaimOutcome{result: result, err: claimErr}
	}()
	go func() {
		<-racingStart
		status, revokeErr := repository.RevokeEnrollment(ctx, RevokeEnrollmentCommand{
			GrantID:                     racing.Grant.ID,
			RevokerAccountID:            adminSession.Account.ID,
			ExpectedRevokerAuthRevision: adminSession.Account.AuthRevision,
			RevokerSessionID:            adminPrincipal.SessionID,
			Now:                         now,
		})
		revokeOutcomeChannel <- postgresRevokeOutcome{status: status, err: revokeErr}
	}()
	close(racingStart)
	claimOutcome := <-claimOutcomeChannel
	revokeOutcome := <-revokeOutcomeChannel
	if claimOutcome.err != nil || revokeOutcome.err != nil {
		t.Fatalf("claim/revoke race errors: claim=%v revoke=%v", claimOutcome.err, revokeOutcome.err)
	}
	var expectedTerminal string
	switch {
	case claimOutcome.result.Status == EnrollmentClaimed && revokeOutcome.status == EnrollmentRevokeNotRevocable:
		expectedTerminal = "consumed"
	case claimOutcome.result.Status == EnrollmentClaimRejected && revokeOutcome.status == EnrollmentRevoked:
		expectedTerminal = "revoked"
	default:
		t.Fatalf(
			"claim/revoke race outcomes: claim=%d revoke=%d",
			claimOutcome.result.Status,
			revokeOutcome.status,
		)
	}
	var racingTerminal string
	if err := pool.QueryRow(ctx, `
SELECT event.event_type
FROM ascendany.auth_enrollment_events AS event
JOIN ascendany.auth_enrollment_grants AS enrollment
  ON enrollment.enrollment_grant_id = event.enrollment_grant_id
WHERE enrollment.public_id = $1::uuid
  AND event.event_slot = 1`, racing.Grant.ID).Scan(&racingTerminal); err != nil {
		t.Fatal(err)
	}
	if racingTerminal != expectedTerminal {
		t.Fatalf("claim/revoke terminal event = %q, want %q", racingTerminal, expectedTerminal)
	}

	protected, err := service.IssueEnrollment(ctx, adminSession.AccessToken, EnrollmentIssueInput{
		Username:      "enroll_student_d",
		DisplayName:   "Enrollment Student D",
		StudentNumber: "integration-student-d",
		ExpiresAt:     now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}

	realTimeService, err := NewService(repository, ServiceConfig{
		Issuer:               "ascendany",
		Audience:             "ascendany-v2",
		JWTSigningPrivateKey: testEd25519PrivateKey(0x31),
		PasswordPepper:       []byte("abcdef0123456789abcdef0123456789"),
		AccessTTL:            15 * time.Minute,
		RefreshTTL:           24 * time.Hour,
		Clock:                systemClock{},
		Random:               rand.Reader,
		passwordWork:         passwordWork,
	})
	if err != nil {
		t.Fatal(err)
	}
	expiryAt := time.Now().UTC().Add(3 * time.Second).Truncate(time.Microsecond)
	expiring, err := realTimeService.IssueEnrollment(ctx, adminSession.AccessToken, EnrollmentIssueInput{
		Username:      "enroll_student_e",
		DisplayName:   "Enrollment Student E",
		StudentNumber: "integration-student-e",
		ExpiresAt:     expiryAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	blockerTransaction, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	blockerOpen := true
	defer func() {
		if blockerOpen {
			_ = blockerTransaction.Rollback(context.Background())
		}
	}()
	if _, err := blockerTransaction.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, studentAccountProvisioningAdvisoryLock); err != nil {
		t.Fatal(err)
	}
	expiringClaim := make(chan error, 1)
	go func() {
		_, claimErr := realTimeService.ClaimEnrollment(ctx, EnrollmentClaimInput{
			Token:    expiring.Token,
			Password: "integration-expiring-student-password",
		})
		expiringClaim <- claimErr
	}()
	waitForPostgresAdvisoryWaiter(t, ctx, pool)
	waitUntil := time.Until(expiring.Grant.ExpiresAt.Add(200 * time.Millisecond))
	if waitUntil > 0 {
		timer := time.NewTimer(waitUntil)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			t.Fatal(ctx.Err())
		}
	}
	if err := blockerTransaction.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	blockerOpen = false
	if claimErr := <-expiringClaim; ErrorCodeOf(claimErr) != ErrorEnrollmentRejected {
		t.Fatalf("claim crossing PostgreSQL expiry error = %v", claimErr)
	}
	var expiringTerminalEvents int64
	if err := pool.QueryRow(ctx, `
SELECT count(*)
FROM ascendany.auth_enrollment_events AS event
JOIN ascendany.auth_enrollment_grants AS enrollment
  ON enrollment.enrollment_grant_id = event.enrollment_grant_id
WHERE enrollment.public_id = $1::uuid
  AND event.event_slot = 1`, expiring.Grant.ID).Scan(&expiringTerminalEvents); err != nil {
		t.Fatal(err)
	}
	if expiringTerminalEvents != 0 {
		t.Fatalf("expired enrollment has %d terminal events", expiringTerminalEvents)
	}

	assertPostgresEnrollmentStateConstraints(t, ctx, pool, adminPrincipal.SessionID)

	futureToken := insertFuturePostgresEnrollment(
		t,
		ctx,
		pool,
		adminPrincipal.SessionID,
		"integration-student-h",
	)
	if _, err := service.ClaimEnrollment(ctx, EnrollmentClaimInput{
		Token:    futureToken.Serialized,
		Password: "integration-future-student-password",
	}); ErrorCodeOf(err) != ErrorEnrollmentRejected {
		t.Fatalf("future-dated claim error = %v", err)
	}
	if err := service.RevokeEnrollment(ctx, adminSession.AccessToken, futureToken.GrantID); ErrorCodeOf(err) != ErrorEnrollmentNotRevocable {
		t.Fatalf("future-dated revoke error = %v", err)
	}

	if err := service.Logout(ctx, LogoutInput{
		AccessToken:  adminSession.AccessToken,
		RefreshToken: adminSession.RefreshCookieValue,
		CSRFToken:    adminSession.CSRFToken,
	}); err != nil {
		t.Fatal(err)
	}
	rejectedRevoke, err := repository.RevokeEnrollment(ctx, RevokeEnrollmentCommand{
		GrantID:                     protected.Grant.ID,
		RevokerAccountID:            adminSession.Account.ID,
		ExpectedRevokerAuthRevision: adminSession.Account.AuthRevision,
		RevokerSessionID:            adminPrincipal.SessionID,
		Now:                         now,
	})
	if err != nil || rejectedRevoke != EnrollmentRevokeIssuerRejected {
		t.Fatalf("revoked administrator repository revoke = %d, error = %v", rejectedRevoke, err)
	}
	var protectedTerminalEvents int64
	if err := pool.QueryRow(ctx, `
SELECT count(*)
FROM ascendany.auth_enrollment_events AS event
JOIN ascendany.auth_enrollment_grants AS enrollment
  ON enrollment.enrollment_grant_id = event.enrollment_grant_id
WHERE enrollment.public_id = $1::uuid
  AND event.event_slot = 1`, protected.Grant.ID).Scan(&protectedTerminalEvents); err != nil {
		t.Fatal(err)
	}
	if protectedTerminalEvents != 0 {
		t.Fatalf("revoked administrator session appended %d terminal events", protectedTerminalEvents)
	}
	rejectedGrantID := "45555555-5555-4555-8555-555555555555"
	rejectedDigest := sha256.Sum256([]byte("revoked administrator session cannot issue enrollment"))
	rejectedIssue, err := repository.IssueEnrollment(ctx, IssueEnrollmentCommand{
		Grant: EnrollmentGrant{
			ID:              rejectedGrantID,
			Username:        "enroll_student_i",
			DisplayName:     "Enrollment Student I",
			StudentNumber:   "integration-student-i",
			IssuerAccountID: adminSession.Account.ID,
			IssuedAt:        now,
			ExpiresAt:       now.Add(time.Hour),
		},
		SecretDigest:               rejectedDigest,
		ExpectedIssuerAuthRevision: adminSession.Account.AuthRevision,
		IssuerSessionID:            adminPrincipal.SessionID,
	})
	if err != nil || rejectedIssue.Status != EnrollmentIssueIssuerRejected {
		t.Fatalf("revoked administrator repository issue = %#v, error = %v", rejectedIssue, err)
	}
	var rejectedGrantCount int64
	if err := pool.QueryRow(ctx, `
SELECT count(*)
FROM ascendany.auth_enrollment_grants
WHERE public_id = $1::uuid`, rejectedGrantID).Scan(&rejectedGrantCount); err != nil {
		t.Fatal(err)
	}
	if rejectedGrantCount != 0 {
		t.Fatalf("revoked administrator session created %d grants", rejectedGrantCount)
	}

	_, err = pool.Exec(ctx, `
UPDATE ascendany.auth_enrollment_grants
SET display_name = display_name
WHERE public_id = $1::uuid`, revoked.Grant.ID)
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != "42501" {
		t.Fatalf("immutable enrollment grant update error = %v", err)
	}
}

func waitForPostgresAdvisoryWaiter(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	timeout := time.NewTimer(3 * time.Second)
	defer timeout.Stop()
	for {
		var waiting bool
		if err := pool.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1
    FROM pg_catalog.pg_locks
    WHERE locktype = 'advisory'
      AND NOT granted
)`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		select {
		case <-ticker.C:
		case <-timeout.C:
			t.Fatal("enrollment claim did not wait on the PostgreSQL advisory lock")
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
}

func assertPostgresEnrollmentStateConstraints(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	issuerSessionPublicID string,
) {
	t.Helper()
	issuerAccountID, issuerSessionID := postgresEnrollmentIssuerIDs(t, ctx, pool, issuerSessionPublicID)
	stateNow := time.Now().UTC().Truncate(time.Microsecond)

	missingEventActorID := postgresEnrollmentActorID(t, ctx, pool, "integration-student-f")
	missingEventDigest := sha256.Sum256([]byte("grant without issued event must fail at commit"))
	missingEventTransaction, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := missingEventTransaction.Exec(ctx, `
INSERT INTO ascendany.auth_enrollment_grants (
    public_id, secret_digest, username, display_name, student_number, actor_id,
    issuer_account_id, issuer_role, issuer_session_id, issued_at, expires_at
)
VALUES (
    '46666666-6666-4666-8666-666666666666'::uuid,
    $1,
    'enroll_student_f',
    'Enrollment Student F',
    'integration-student-f',
    $2,
    $3,
    'admin',
    $4,
    $5,
    $6
)`, missingEventDigest[:], missingEventActorID, issuerAccountID, issuerSessionID, stateNow, stateNow.Add(time.Hour)); err != nil {
		_ = missingEventTransaction.Rollback(context.Background())
		t.Fatal(err)
	}
	requirePostgresCommitCode(t, ctx, missingEventTransaction, "23514")

	mismatchedEventActorID := postgresEnrollmentActorID(t, ctx, pool, "integration-student-g")
	mismatchedEventDigest := sha256.Sum256([]byte("grant issued timestamp must match append-only event"))
	mismatchedEventTransaction, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var mismatchedGrantID int64
	if err := mismatchedEventTransaction.QueryRow(ctx, `
INSERT INTO ascendany.auth_enrollment_grants (
    public_id, secret_digest, username, display_name, student_number, actor_id,
    issuer_account_id, issuer_role, issuer_session_id, issued_at, expires_at
)
VALUES (
    '47777777-7777-4777-8777-777777777777'::uuid,
    $1,
    'enroll_student_g',
    'Enrollment Student G',
    'integration-student-g',
    $2,
    $3,
    'admin',
    $4,
    $5,
    $6
)
RETURNING enrollment_grant_id`,
		mismatchedEventDigest[:],
		mismatchedEventActorID,
		issuerAccountID,
		issuerSessionID,
		stateNow,
		stateNow.Add(time.Hour),
	).Scan(&mismatchedGrantID); err != nil {
		_ = mismatchedEventTransaction.Rollback(context.Background())
		t.Fatal(err)
	}
	if _, err := mismatchedEventTransaction.Exec(ctx, `
INSERT INTO ascendany.auth_enrollment_events (
    enrollment_grant_id, event_type, actor_account_id, actor_role,
    session_id, subject_actor_id, occurred_at
)
VALUES ($1, 'issued', $2, 'admin', $3, NULL, $4)`,
		mismatchedGrantID,
		issuerAccountID,
		issuerSessionID,
		stateNow.Add(time.Second),
	); err != nil {
		_ = mismatchedEventTransaction.Rollback(context.Background())
		t.Fatal(err)
	}
	requirePostgresCommitCode(t, ctx, mismatchedEventTransaction, "23514")

	var invalidGrantCount int64
	if err := pool.QueryRow(ctx, `
SELECT count(*)
FROM ascendany.auth_enrollment_grants
WHERE public_id IN (
    '46666666-6666-4666-8666-666666666666'::uuid,
    '47777777-7777-4777-8777-777777777777'::uuid
)`).Scan(&invalidGrantCount); err != nil {
		t.Fatal(err)
	}
	if invalidGrantCount != 0 {
		t.Fatalf("deferred enrollment constraints retained %d invalid grants", invalidGrantCount)
	}
}

func insertFuturePostgresEnrollment(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	issuerSessionPublicID string,
	studentNumber string,
) futurePostgresEnrollment {
	t.Helper()
	issuedToken, err := issueEnrollmentToken(bytes.NewReader(bytes.Repeat([]byte{0x6f}, secretBytes)))
	if err != nil {
		t.Fatal(err)
	}
	issuerAccountID, issuerSessionID := postgresEnrollmentIssuerIDs(t, ctx, pool, issuerSessionPublicID)
	actorID := postgresEnrollmentActorID(t, ctx, pool, studentNumber)
	issuedAt := time.Now().UTC().Add(10 * time.Minute).Truncate(time.Microsecond)
	transaction, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = transaction.Rollback(context.Background()) }()
	const grantPublicID = "48888888-8888-4888-8888-888888888888"
	var grantDatabaseID int64
	if err := transaction.QueryRow(ctx, `
INSERT INTO ascendany.auth_enrollment_grants (
    public_id, secret_digest, username, display_name, student_number, actor_id,
    issuer_account_id, issuer_role, issuer_session_id, issued_at, expires_at
)
VALUES (
    $1::uuid,
    $2,
    'enroll_student_h',
    'Enrollment Student H',
    $3,
    $4,
    $5,
    'admin',
    $6,
    $7,
    $8
)
RETURNING enrollment_grant_id`,
		grantPublicID,
		issuedToken.Digest[:],
		studentNumber,
		actorID,
		issuerAccountID,
		issuerSessionID,
		issuedAt,
		issuedAt.Add(time.Hour),
	).Scan(&grantDatabaseID); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Exec(ctx, `
INSERT INTO ascendany.auth_enrollment_events (
    enrollment_grant_id, event_type, actor_account_id, actor_role,
    session_id, subject_actor_id, occurred_at
)
VALUES ($1, 'issued', $2, 'admin', $3, NULL, $4)`,
		grantDatabaseID,
		issuerAccountID,
		issuerSessionID,
		issuedAt,
	); err != nil {
		t.Fatal(err)
	}
	if err := transaction.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return futurePostgresEnrollment{GrantID: grantPublicID, Serialized: issuedToken.Serialized}
}

func postgresEnrollmentIssuerIDs(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	issuerSessionPublicID string,
) (int64, int64) {
	t.Helper()
	var issuerAccountID int64
	var issuerSessionID int64
	if err := pool.QueryRow(ctx, `
SELECT account.account_id, session.session_id
FROM ascendany.auth_accounts AS account
JOIN ascendany.auth_sessions AS session ON session.account_id = account.account_id
WHERE account.username = 'enroll_admin'
  AND session.public_id = $1::uuid`, issuerSessionPublicID).Scan(&issuerAccountID, &issuerSessionID); err != nil {
		t.Fatal(err)
	}
	return issuerAccountID, issuerSessionID
}

func postgresEnrollmentActorID(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	studentNumber string,
) int64 {
	t.Helper()
	var actorID int64
	if err := pool.QueryRow(ctx, `
SELECT actor_id
FROM ascendany.pintia_actor_identifiers
WHERE identifier_kind = 'student_number'
  AND identifier_value = $1`, studentNumber).Scan(&actorID); err != nil {
		t.Fatal(err)
	}
	return actorID
}

func requirePostgresCommitCode(t *testing.T, ctx context.Context, transaction pgx.Tx, code string) {
	t.Helper()
	commitErr := transaction.Commit(ctx)
	var postgresError *pgconn.PgError
	if !errors.As(commitErr, &postgresError) || postgresError.Code != code {
		t.Fatalf("PostgreSQL commit error = %v, want SQLSTATE %s", commitErr, code)
	}
}

func seedPostgresEnrollmentIdentities(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	adminPHC string,
	now time.Time,
) {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(ctx, `
INSERT INTO ascendany.auth_accounts (
    public_id, actor_id, username, password_phc, display_name, student_number,
    role, auth_revision, created_at, updated_at
)
VALUES ('41111111-1111-4111-8111-111111111111'::uuid, NULL, 'enroll_admin', $1,
        'Enrollment Administrator', NULL, 'admin', 1, $2, $2)`, adminPHC, now); err != nil {
		t.Fatal(err)
	}
	for index, identity := range []struct {
		userID        string
		studentNumber string
	}{
		{userID: "integration-user-a", studentNumber: "integration-student-a"},
		{userID: "integration-user-b", studentNumber: "integration-student-b"},
		{userID: "integration-user-c", studentNumber: "integration-student-c"},
		{userID: "integration-user-d", studentNumber: "integration-student-d"},
		{userID: "integration-user-e", studentNumber: "integration-student-e"},
		{userID: "integration-user-f", studentNumber: "integration-student-f"},
		{userID: "integration-user-g", studentNumber: "integration-student-g"},
		{userID: "integration-user-h", studentNumber: "integration-student-h"},
		{userID: "integration-user-i", studentNumber: "integration-student-i"},
	} {
		var actorID int64
		if err := tx.QueryRow(ctx, `
INSERT INTO ascendany.pintia_actors (user_id)
VALUES ($1)
RETURNING actor_id`, identity.userID).Scan(&actorID); err != nil {
			t.Fatalf("insert actor %d: %v", index, err)
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO ascendany.pintia_actor_identifiers (identifier_kind, identifier_value, actor_id)
VALUES ('student_number', $1, $2)`, identity.studentNumber, actorID); err != nil {
			t.Fatalf("insert actor identifier %d: %v", index, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

func equalBytes(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	var different byte
	for index := range left {
		different |= left[index] ^ right[index]
	}
	return different == 0
}
