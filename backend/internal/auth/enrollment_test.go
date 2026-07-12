package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"sync"
	"testing"
	"time"
)

type memoryEnrollmentState struct {
	grantsByID      map[string]*memoryEnrollmentGrant
	grantIDByDigest map[[sha256.Size]byte]string
	actorNumbers    map[string]bool
}

type memoryEnrollmentGrant struct {
	grant    EnrollmentGrant
	digest   [sha256.Size]byte
	terminal string
}

func newMemoryEnrollmentState() *memoryEnrollmentState {
	return &memoryEnrollmentState{
		grantsByID:      make(map[string]*memoryEnrollmentGrant),
		grantIDByDigest: make(map[[sha256.Size]byte]string),
		actorNumbers:    make(map[string]bool),
	}
}

func (r *memoryRepository) IssueEnrollment(
	_ context.Context,
	command IssueEnrollmentCommand,
) (IssueEnrollmentResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	issuer, exists := r.accountsByID[command.Grant.IssuerAccountID]
	issuerSession, sessionExists := r.sessions[command.IssuerSessionID]
	if !exists || issuer.Role != RoleAdmin || issuer.DisabledAt != nil ||
		issuer.AuthRevision != command.ExpectedIssuerAuthRevision || !sessionExists ||
		issuerSession.accountID != issuer.ID || issuerSession.record.RevokedAt != nil ||
		!command.Grant.IssuedAt.Before(issuerSession.record.ExpiresAt) {
		return IssueEnrollmentResult{Status: EnrollmentIssueIssuerRejected}, nil
	}
	if !r.enrollment.actorNumbers[command.Grant.StudentNumber] {
		return IssueEnrollmentResult{Status: EnrollmentIssueIdentityUnavailable}, nil
	}
	for _, account := range r.accountsByID {
		if account.Username == command.Grant.Username ||
			(account.StudentNumber != nil && *account.StudentNumber == command.Grant.StudentNumber) {
			return IssueEnrollmentResult{Status: EnrollmentIssueIdentityUnavailable}, nil
		}
	}
	for _, stored := range r.enrollment.grantsByID {
		if stored.terminal == "" && stored.grant.ExpiresAt.After(command.Grant.IssuedAt) &&
			(stored.grant.Username == command.Grant.Username ||
				stored.grant.StudentNumber == command.Grant.StudentNumber) {
			return IssueEnrollmentResult{Status: EnrollmentIssueIdentityUnavailable}, nil
		}
	}
	r.enrollment.grantsByID[command.Grant.ID] = &memoryEnrollmentGrant{
		grant:  command.Grant,
		digest: command.SecretDigest,
	}
	r.enrollment.grantIDByDigest[command.SecretDigest] = command.Grant.ID
	return IssueEnrollmentResult{Status: EnrollmentIssued, Grant: command.Grant}, nil
}

func (r *memoryRepository) RevokeEnrollment(
	_ context.Context,
	command RevokeEnrollmentCommand,
) (RevokeEnrollmentStatus, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	revoker, exists := r.accountsByID[command.RevokerAccountID]
	revokerSession, sessionExists := r.sessions[command.RevokerSessionID]
	if !exists || revoker.Role != RoleAdmin || revoker.DisabledAt != nil ||
		revoker.AuthRevision != command.ExpectedRevokerAuthRevision || !sessionExists ||
		revokerSession.accountID != revoker.ID || revokerSession.record.RevokedAt != nil ||
		!command.Now.Before(revokerSession.record.ExpiresAt) {
		return EnrollmentRevokeIssuerRejected, nil
	}
	grant, exists := r.enrollment.grantsByID[command.GrantID]
	if !exists || grant.terminal != "" || !command.Now.Before(grant.grant.ExpiresAt) {
		return EnrollmentRevokeNotRevocable, nil
	}
	grant.terminal = "revoked"
	return EnrollmentRevoked, nil
}

func (r *memoryRepository) ClaimEnrollment(
	_ context.Context,
	command ClaimEnrollmentCommand,
) (ClaimEnrollmentResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	grantID, exists := r.enrollment.grantIDByDigest[command.SecretDigest]
	if !exists {
		return ClaimEnrollmentResult{Status: EnrollmentClaimRejected}, nil
	}
	stored := r.enrollment.grantsByID[grantID]
	if stored.terminal != "" || !command.Now.Before(stored.grant.ExpiresAt) {
		return ClaimEnrollmentResult{Status: EnrollmentClaimRejected}, nil
	}
	for _, existing := range r.accountsByID {
		if existing.Username == stored.grant.Username ||
			(existing.StudentNumber != nil && *existing.StudentNumber == stored.grant.StudentNumber) {
			return ClaimEnrollmentResult{Status: EnrollmentClaimRejected}, nil
		}
	}
	studentNumber := stored.grant.StudentNumber
	account := AccountRecord{
		Account: Account{
			ID:            command.AccountID,
			Username:      stored.grant.Username,
			DisplayName:   stored.grant.DisplayName,
			StudentNumber: &studentNumber,
			Role:          RoleStudent,
			AuthRevision:  1,
		},
		PasswordPHC: command.PasswordPHC,
	}
	r.accountsByID[account.ID] = account
	r.accountsByUsername[account.Username] = account
	r.storeSession(
		account.ID,
		account.AuthRevision,
		command.SessionID,
		command.Now,
		command.SessionExpiry,
		command.RefreshToken,
	)
	stored.terminal = "consumed"
	return ClaimEnrollmentResult{Status: EnrollmentClaimed, Account: account, AuthenticatedAt: command.Now}, nil
}

func loginAdminAccount(
	t *testing.T,
	repository *memoryRepository,
	service *Service,
	username string,
) AuthResult {
	t.Helper()
	const password = "long-enough-admin-password"
	passwordPHC, err := service.passwords.Hash(password)
	if err != nil {
		t.Fatal(err)
	}
	accountID, err := newUUIDv4(bytes.NewReader(bytes.Repeat([]byte{0x51}, 64)))
	if err != nil {
		t.Fatal(err)
	}
	account := AccountRecord{
		Account: Account{
			ID:            accountID,
			Username:      username,
			DisplayName:   "Administrator",
			StudentNumber: nil,
			Role:          RoleAdmin,
			AuthRevision:  1,
		},
		PasswordPHC: passwordPHC,
	}
	repository.mu.Lock()
	repository.accountsByID[account.ID] = account
	repository.accountsByUsername[account.Username] = account
	repository.mu.Unlock()
	result, err := service.Login(context.Background(), LoginInput{Username: username, Password: password})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func issueMemoryEnrollment(
	t *testing.T,
	repository *memoryRepository,
	service *Service,
	admin AuthResult,
	now time.Time,
	username string,
	studentNumber string,
) IssuedEnrollment {
	t.Helper()
	repository.mu.Lock()
	repository.enrollment.actorNumbers[studentNumber] = true
	repository.mu.Unlock()
	issued, err := service.IssueEnrollment(context.Background(), admin.AccessToken, EnrollmentIssueInput{
		Username:      username,
		DisplayName:   "  Enrolled Student  ",
		StudentNumber: "  " + studentNumber + "  ",
		ExpiresAt:     now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	return issued
}

func TestEnrollmentTokenIsCanonicalOpaque256BitSecret(t *testing.T) {
	raw := bytes.Repeat([]byte{0x7a}, secretBytes)
	issued, err := issueEnrollmentToken(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	wantSerialized := base64.RawURLEncoding.EncodeToString(raw)
	if issued.Serialized != wantSerialized || issued.Digest != sha256.Sum256(raw) {
		t.Fatalf("issued token = %#v", issued)
	}
	parsed, err := parseEnrollmentToken(issued.Serialized)
	if err != nil || parsed != issued.Digest {
		t.Fatalf("parsed digest = %x, error = %v", parsed, err)
	}
	for _, malformed := range []string{
		issued.Serialized + "=",
		issued.Serialized[:len(issued.Serialized)-1],
		"v1." + issued.Serialized,
		"!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!!",
	} {
		if _, err := parseEnrollmentToken(malformed); err == nil {
			t.Fatalf("malformed enrollment token accepted: %q", malformed)
		}
	}
}

func TestAdminIssuesBoundEnrollmentAndClaimCreatesSessionAtomically(t *testing.T) {
	now := time.Date(2026, 7, 11, 1, 0, 0, 123_456_789, time.UTC)
	repository := newMemoryRepository()
	service := newTestService(t, repository, now)
	admin := loginAdminAccount(t, repository, service, "admin_1")
	issued := issueMemoryEnrollment(t, repository, service, admin, now, "student_6", "20260006")

	if issued.Token == "" || issued.Grant.Username != "student_6" ||
		issued.Grant.DisplayName != "Enrolled Student" || issued.Grant.StudentNumber != "20260006" ||
		issued.Grant.IssuerAccountID != admin.Account.ID ||
		!issued.Grant.IssuedAt.Equal(canonicalAuthTime(now)) {
		t.Fatalf("issued enrollment = %#v", issued)
	}
	digest, err := parseEnrollmentToken(issued.Token)
	if err != nil {
		t.Fatal(err)
	}
	repository.mu.Lock()
	stored := repository.enrollment.grantsByID[issued.Grant.ID]
	repository.mu.Unlock()
	if stored == nil || stored.digest != digest {
		t.Fatalf("stored enrollment grant = %#v", stored)
	}

	claimed, err := service.ClaimEnrollment(context.Background(), EnrollmentClaimInput{
		Token:    issued.Token,
		Password: "student-password-strong",
	})
	if err != nil {
		t.Fatal(err)
	}
	if claimed.Account.Username != "student_6" || claimed.Account.DisplayName != "Enrolled Student" ||
		claimed.Account.StudentNumber == nil || *claimed.Account.StudentNumber != "20260006" ||
		claimed.Account.Role != RoleStudent || claimed.RefreshCookieValue == "" || claimed.CSRFToken == "" {
		t.Fatalf("claimed result = %#v", claimed)
	}
	if _, err := service.Me(context.Background(), claimed.AccessToken); err != nil {
		t.Fatalf("enrollment session is not active: %v", err)
	}
	if _, err := service.ClaimEnrollment(context.Background(), EnrollmentClaimInput{
		Token:    issued.Token,
		Password: "another-strong-password",
	}); ErrorCodeOf(err) != ErrorEnrollmentRejected {
		t.Fatalf("second claim error = %v", err)
	}
}

func TestConcurrentEnrollmentClaimHasExactlyOneWinner(t *testing.T) {
	now := time.Date(2026, 7, 11, 2, 0, 0, 0, time.UTC)
	repository := newMemoryRepository()
	service := newTestService(t, repository, now)
	admin := loginAdminAccount(t, repository, service, "admin_2")
	issued := issueMemoryEnrollment(t, repository, service, admin, now, "student_7", "20260007")

	start := make(chan struct{})
	errorsByClaim := make(chan error, 2)
	var wait sync.WaitGroup
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			_, err := service.ClaimEnrollment(context.Background(), EnrollmentClaimInput{
				Token:    issued.Token,
				Password: "student-password-strong",
			})
			errorsByClaim <- err
		}()
	}
	close(start)
	wait.Wait()
	close(errorsByClaim)
	var succeeded, rejected int
	for err := range errorsByClaim {
		switch ErrorCodeOf(err) {
		case "":
			succeeded++
		case ErrorEnrollmentRejected:
			rejected++
		default:
			t.Fatalf("unexpected claim error = %v", err)
		}
	}
	if succeeded != 1 || rejected != 1 {
		t.Fatalf("claim outcomes: succeeded=%d rejected=%d", succeeded, rejected)
	}
}

func TestEnrollmentRevocationAndExpiryFailClosed(t *testing.T) {
	now := time.Date(2026, 7, 11, 3, 0, 0, 0, time.UTC)
	repository := newMemoryRepository()
	service := newTestService(t, repository, now)
	admin := loginAdminAccount(t, repository, service, "admin_3")
	issued := issueMemoryEnrollment(t, repository, service, admin, now, "student_8", "20260008")
	if err := service.RevokeEnrollment(context.Background(), admin.AccessToken, issued.Grant.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ClaimEnrollment(context.Background(), EnrollmentClaimInput{
		Token:    issued.Token,
		Password: "student-password-strong",
	}); ErrorCodeOf(err) != ErrorEnrollmentRejected {
		t.Fatalf("revoked claim error = %v", err)
	}
	if err := service.RevokeEnrollment(context.Background(), admin.AccessToken, issued.Grant.ID); ErrorCodeOf(err) != ErrorEnrollmentNotRevocable {
		t.Fatalf("second revoke error = %v", err)
	}

	expired := issueMemoryEnrollment(t, repository, service, admin, now, "student_9", "20260009")
	repository.mu.Lock()
	repository.enrollment.grantsByID[expired.Grant.ID].grant.ExpiresAt = now
	repository.mu.Unlock()
	if _, err := service.ClaimEnrollment(context.Background(), EnrollmentClaimInput{
		Token:    expired.Token,
		Password: "student-password-strong",
	}); ErrorCodeOf(err) != ErrorEnrollmentRejected {
		t.Fatalf("expired claim error = %v", err)
	}
}

func TestEnrollmentRequiresAdminImportedIdentityAndBoundInput(t *testing.T) {
	now := time.Date(2026, 7, 11, 4, 0, 0, 0, time.UTC)
	repository := newMemoryRepository()
	service := newTestService(t, repository, now)
	student := loginTestAccount(t, repository, service, "student_10")
	if _, err := service.IssueEnrollment(context.Background(), student.AccessToken, EnrollmentIssueInput{
		Username: "student_11", DisplayName: "Student", StudentNumber: "20260011", ExpiresAt: now.Add(time.Hour),
	}); ErrorCodeOf(err) != ErrorForbidden {
		t.Fatalf("student issue error = %v", err)
	}
	admin := loginAdminAccount(t, repository, service, "admin_4")
	if _, err := service.IssueEnrollment(context.Background(), admin.AccessToken, EnrollmentIssueInput{
		Username: "student_11", DisplayName: "Student", StudentNumber: "20260011", ExpiresAt: now.Add(time.Hour),
	}); ErrorCodeOf(err) != ErrorEnrollmentIdentity {
		t.Fatalf("missing imported identity error = %v", err)
	}
	for _, input := range []EnrollmentIssueInput{
		{Username: "Student", DisplayName: "Student", StudentNumber: "20260011", ExpiresAt: now.Add(time.Hour)},
		{Username: "student_11", DisplayName: "Student\x00Name", StudentNumber: "20260011", ExpiresAt: now.Add(time.Hour)},
		{Username: "student_11", DisplayName: "Student", StudentNumber: "20260011", ExpiresAt: now},
		{Username: "student_11", DisplayName: "Student", StudentNumber: "20260011", ExpiresAt: now.Add(MaxEnrollmentLifetime + time.Microsecond)},
	} {
		if _, err := service.IssueEnrollment(context.Background(), admin.AccessToken, input); ErrorCodeOf(err) != ErrorInvalidInput {
			t.Fatalf("invalid issue input error = %v for %#v", err, input)
		}
	}
}

func TestEnrollmentClaimUsesPasswordWorkCapacityAndContextCodes(t *testing.T) {
	now := time.Date(2026, 7, 11, 5, 0, 0, 0, time.UTC)
	repository := newMemoryRepository()
	service := newTestService(t, repository, now)
	admin := loginAdminAccount(t, repository, service, "admin_5")
	issued := issueMemoryEnrollment(t, repository, service, admin, now, "student_12", "20260012")

	firstRelease, first := service.passwordWork.tryAcquire()
	secondRelease, second := service.passwordWork.tryAcquire()
	if !first || !second {
		t.Fatal("failed to reserve password work capacity")
	}
	_, err := service.ClaimEnrollment(context.Background(), EnrollmentClaimInput{
		Token: issued.Token, Password: "student-password-strong",
	})
	firstRelease()
	secondRelease()
	if ErrorCodeOf(err) != ErrorPasswordWorkSaturated {
		t.Fatalf("saturated claim error = %v", err)
	}

	canceledContext, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.ClaimEnrollment(canceledContext, EnrollmentClaimInput{
		Token: issued.Token, Password: "student-password-strong",
	}); ErrorCodeOf(err) != ErrorCanceled {
		t.Fatalf("canceled claim error = %v", err)
	}
	if _, err := service.ClaimEnrollment(context.Background(), EnrollmentClaimInput{
		Token: "malformed", Password: "student-password-strong",
	}); ErrorCodeOf(err) != ErrorEnrollmentRejected {
		t.Fatalf("malformed token error = %v", err)
	}
}

type revokeAdminSessionBeforeIssueRepository struct {
	*memoryRepository
}

func (repository *revokeAdminSessionBeforeIssueRepository) IssueEnrollment(
	ctx context.Context,
	command IssueEnrollmentCommand,
) (IssueEnrollmentResult, error) {
	repository.mu.Lock()
	repository.revokeSession(command.IssuerSessionID, command.Grant.IssuedAt)
	repository.mu.Unlock()
	return repository.memoryRepository.IssueEnrollment(ctx, command)
}

func TestEnrollmentIssueRevalidatesAdminSessionInsideWriteBoundary(t *testing.T) {
	now := time.Date(2026, 7, 11, 6, 0, 0, 0, time.UTC)
	memory := newMemoryRepository()
	repository := &revokeAdminSessionBeforeIssueRepository{memoryRepository: memory}
	service := newTestService(t, repository, now)
	admin := loginAdminAccount(t, memory, service, "admin_6")
	memory.mu.Lock()
	memory.enrollment.actorNumbers["20260013"] = true
	memory.mu.Unlock()
	_, err := service.IssueEnrollment(context.Background(), admin.AccessToken, EnrollmentIssueInput{
		Username:      "student_13",
		DisplayName:   "Student Thirteen",
		StudentNumber: "20260013",
		ExpiresAt:     now.Add(time.Hour),
	})
	if ErrorCodeOf(err) != ErrorAuthentication {
		t.Fatalf("revoked admin session issue error = %v", err)
	}
	memory.mu.Lock()
	grantCount := len(memory.enrollment.grantsByID)
	memory.mu.Unlock()
	if grantCount != 0 {
		t.Fatalf("revoked admin session created %d enrollment grants", grantCount)
	}
}

type databaseAuthoritativeIssueTimeRepository struct {
	*memoryRepository
	offset time.Duration
}

func (repository *databaseAuthoritativeIssueTimeRepository) IssueEnrollment(
	ctx context.Context,
	command IssueEnrollmentCommand,
) (IssueEnrollmentResult, error) {
	command.Grant.IssuedAt = command.Grant.IssuedAt.Add(repository.offset)
	return repository.memoryRepository.IssueEnrollment(ctx, command)
}

func TestEnrollmentIssueUsesRepositoryAuthoritativeTimestamp(t *testing.T) {
	now := time.Date(2026, 7, 11, 7, 0, 0, 0, time.UTC)
	memory := newMemoryRepository()
	repository := &databaseAuthoritativeIssueTimeRepository{
		memoryRepository: memory,
		offset:           -time.Millisecond,
	}
	service := newTestService(t, repository, now)
	admin := loginAdminAccount(t, memory, service, "admin_7")
	memory.mu.Lock()
	memory.enrollment.actorNumbers["20260014"] = true
	memory.mu.Unlock()
	issued, err := service.IssueEnrollment(context.Background(), admin.AccessToken, EnrollmentIssueInput{
		Username:      "student_14",
		DisplayName:   "Student Fourteen",
		StudentNumber: "20260014",
		ExpiresAt:     now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := now.Add(-time.Millisecond); !issued.Grant.IssuedAt.Equal(want) || issued.Token == "" {
		t.Fatalf("issued enrollment timestamp = %s, token empty = %t", issued.Grant.IssuedAt, issued.Token == "")
	}
}
