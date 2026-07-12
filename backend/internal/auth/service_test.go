package auth

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

type memorySession struct {
	record    SessionRecord
	accountID string
}

type memoryRefresh struct {
	token     NewRefreshToken
	sessionID string
	usedAt    *time.Time
	revokedAt *time.Time
}

type memoryRepository struct {
	mu                     sync.Mutex
	accountsByUsername     map[string]AccountRecord
	accountsByID           map[string]AccountRecord
	sessions               map[string]*memorySession
	refresh                map[string]*memoryRefresh
	nextDatabaseID         int64
	reuseRevocationCommits int
	logoutCommits          int
	lastTransactionNow     time.Time
	enrollment             *memoryEnrollmentState
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{
		accountsByUsername: make(map[string]AccountRecord),
		accountsByID:       make(map[string]AccountRecord),
		sessions:           make(map[string]*memorySession),
		refresh:            make(map[string]*memoryRefresh),
		enrollment:         newMemoryEnrollmentState(),
	}
}

func (r *memoryRepository) FindLoginAccount(_ context.Context, username string) (AccountRecord, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	account, found := r.accountsByUsername[username]
	return account, found, nil
}

func (r *memoryRepository) CreateSession(_ context.Context, command CreateSessionCommand) (CreateSessionResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	account, found := r.accountsByID[command.AccountID]
	if !found || account.DisabledAt != nil || account.AuthRevision != command.ExpectedAuthRevision {
		return CreateSessionResult{Status: SessionRejected}, nil
	}
	r.storeSession(account.ID, account.AuthRevision, command.SessionID, command.Now, command.SessionExpiry, command.RefreshToken)
	return CreateSessionResult{Status: SessionCreated, Account: account}, nil
}

func (r *memoryRepository) TransactRefresh(
	_ context.Context,
	tokenID string,
	now time.Time,
	decide RefreshDecider,
) (RefreshDecisionKind, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lastTransactionNow = now
	snapshot := RefreshSnapshot{Found: false}
	stored, found := r.refresh[tokenID]
	if found {
		session := r.sessions[stored.sessionID]
		account := r.accountsByID[session.accountID]
		snapshot = RefreshSnapshot{
			Found:          true,
			TokenID:        stored.token.ID,
			SecretDigest:   stored.token.SecretDigest,
			CSRFDigest:     stored.token.CSRFDigest,
			TokenExpiresAt: stored.token.ExpiresAt,
			UsedAt:         stored.usedAt,
			TokenRevokedAt: stored.revokedAt,
			Session:        session.record,
			Account:        account,
		}
	}
	decision := decide(snapshot)
	switch decision.Kind {
	case RefreshRotate:
		usedAt := now
		stored.usedAt = &usedAt
		r.refresh[decision.NextToken.ID] = &memoryRefresh{token: *decision.NextToken, sessionID: stored.sessionID}
		session := r.sessions[stored.sessionID]
		session.record.LastSeenAt = now
	case RefreshRevokeReuse:
		r.revokeSession(stored.sessionID, now)
		r.reuseRevocationCommits++
	case RefreshLogout:
		r.revokeSession(stored.sessionID, now)
		r.logoutCommits++
	}
	return decision.Kind, nil
}

func (r *memoryRepository) LoadPrincipal(
	_ context.Context,
	accountID string,
	sessionID string,
	_ time.Time,
) (PrincipalSnapshot, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	account, accountFound := r.accountsByID[accountID]
	session, sessionFound := r.sessions[sessionID]
	if !accountFound || !sessionFound || session.accountID != accountID {
		return PrincipalSnapshot{Found: false}, nil
	}
	return PrincipalSnapshot{Found: true, Account: account, Session: session.record}, nil
}

func (r *memoryRepository) storeSession(
	accountID string,
	authRevision int64,
	sessionID string,
	now time.Time,
	expiresAt time.Time,
	refresh NewRefreshToken,
) {
	r.nextDatabaseID++
	r.sessions[sessionID] = &memorySession{
		accountID: accountID,
		record: SessionRecord{
			DatabaseID:   r.nextDatabaseID,
			ID:           sessionID,
			AccountID:    accountID,
			AuthRevision: authRevision,
			CreatedAt:    now,
			ExpiresAt:    expiresAt,
			LastSeenAt:   now,
		},
	}
	r.refresh[refresh.ID] = &memoryRefresh{token: refresh, sessionID: sessionID}
}

func (r *memoryRepository) revokeSession(sessionID string, now time.Time) {
	revokedAt := now
	r.sessions[sessionID].record.RevokedAt = &revokedAt
	for _, token := range r.refresh {
		if token.sessionID == sessionID {
			tokenRevokedAt := now
			token.revokedAt = &tokenRevokedAt
		}
	}
}

func newTestService(t *testing.T, repository Repository, now time.Time) *Service {
	t.Helper()
	passwordWork, err := newPasswordWorkLimiter(2)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(repository, ServiceConfig{
		Issuer:         "ascendany",
		Audience:       "ascendany-v2",
		JWTKey:         []byte("0123456789abcdef0123456789abcdef"),
		PasswordPepper: []byte("abcdef0123456789abcdef0123456789"),
		AccessTTL:      15 * time.Minute,
		RefreshTTL:     24 * time.Hour,
		Clock:          fixedClock{now: now},
		Random:         rand.Reader,
		passwordWork:   passwordWork,
	})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestPasswordWorkLimiterRejectsWorkBeyondCapacity(t *testing.T) {
	limiter, err := newPasswordWorkLimiter(2)
	if err != nil {
		t.Fatal(err)
	}
	releaseFirst, acquired := limiter.tryAcquire()
	if !acquired {
		t.Fatal("first password work slot was rejected")
	}
	releaseSecond, acquired := limiter.tryAcquire()
	if !acquired {
		t.Fatal("second password work slot was rejected")
	}
	if release, acquired := limiter.tryAcquire(); acquired || release != nil {
		t.Fatal("password work beyond capacity was accepted")
	}
	releaseFirst()
	releaseThird, acquired := limiter.tryAcquire()
	if !acquired {
		t.Fatal("released password work slot was not reusable")
	}
	releaseSecond()
	releaseThird()
}

func TestLoginRejectsWhenPasswordWorkIsSaturated(t *testing.T) {
	now := time.Date(2026, 7, 10, 4, 0, 0, 0, time.UTC)
	repository := newMemoryRepository()
	service := newTestService(t, repository, now)
	release, acquired := service.passwordWork.tryAcquire()
	if !acquired {
		t.Fatal("failed to reserve password work slot")
	}
	secondRelease, acquired := service.passwordWork.tryAcquire()
	if !acquired {
		t.Fatal("failed to reserve second password work slot")
	}
	_, err := service.Login(context.Background(), LoginInput{
		Username: "student_1",
		Password: "long-enough-password",
	})
	release()
	secondRelease()
	if errorCode(err) != ErrorPasswordWorkSaturated {
		t.Fatalf("login error = %v", err)
	}
}

func loginTestAccount(t *testing.T, repository *memoryRepository, service *Service, username string) AuthResult {
	t.Helper()
	const password = "long-enough-password"
	passwordPHC, err := service.passwords.Hash(password)
	if err != nil {
		t.Fatal(err)
	}
	accountID, err := newUUIDv4(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	studentNumber := "20260001"
	account := AccountRecord{
		Account: Account{
			ID:            accountID,
			Username:      username,
			DisplayName:   "Student Name",
			StudentNumber: &studentNumber,
			Role:          RoleStudent,
			AuthRevision:  1,
		},
		PasswordPHC: passwordPHC,
	}
	repository.mu.Lock()
	repository.accountsByUsername[username] = account
	repository.accountsByID[accountID] = account
	repository.mu.Unlock()
	result, err := service.Login(context.Background(), LoginInput{Username: username, Password: password})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestLoginDoesNotRevealUsernameExistence(t *testing.T) {
	now := time.Date(2026, 7, 10, 4, 0, 0, 0, time.UTC)
	repository := newMemoryRepository()
	service := newTestService(t, repository, now)

	_, unknownLoginErr := service.Login(context.Background(), LoginInput{
		Username: "student_1",
		Password: "long-enough-password",
	})
	if ErrorCodeOf(unknownLoginErr) != ErrorAuthentication {
		t.Fatalf("unknown login error = %v", unknownLoginErr)
	}
	loginAuthErr := unknownLoginErr.(*Error)

	loginTestAccount(t, repository, service, "student_1")
	_, badPasswordErr := service.Login(context.Background(), LoginInput{
		Username: "student_1",
		Password: "wrong-password-value",
	})
	if errorCode(badPasswordErr) != ErrorAuthentication || badPasswordErr.(*Error).Message != loginAuthErr.Message {
		t.Fatalf("bad-password response leaks username existence: %v", badPasswordErr)
	}
}

func TestLoginSurfacesStoredPasswordCorruption(t *testing.T) {
	now := time.Date(2026, 7, 10, 4, 0, 0, 0, time.UTC)
	repository := newMemoryRepository()
	service := newTestService(t, repository, now)
	account := AccountRecord{
		Account: Account{
			ID:           "123e4567-e89b-42d3-a456-426614174000",
			Username:     "student_1",
			DisplayName:  "Student Name",
			Role:         RoleStudent,
			AuthRevision: 1,
		},
		PasswordPHC: "malformed",
	}
	repository.accountsByUsername[account.Username] = account
	repository.accountsByID[account.ID] = account
	_, err := service.Login(context.Background(), LoginInput{
		Username: "student_1",
		Password: "long-enough-password",
	})
	if ErrorCodeOf(err) != ErrorInternal {
		t.Fatalf("stored password corruption error = %v", err)
	}
}

func TestRefreshRotationReuseRevokesFamilyAndCommitsBeforeError(t *testing.T) {
	now := time.Date(2026, 7, 10, 5, 0, 0, 0, time.UTC)
	repository := newMemoryRepository()
	service := newTestService(t, repository, now)
	initial := loginTestAccount(t, repository, service, "student_2")

	rotated, err := service.Refresh(context.Background(), RefreshInput{
		RefreshToken: initial.RefreshCookieValue,
		CSRFToken:    initial.CSRFToken,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, reuseErr := service.Refresh(context.Background(), RefreshInput{
		RefreshToken: initial.RefreshCookieValue,
		CSRFToken:    initial.CSRFToken,
	})
	if errorCode(reuseErr) != ErrorRefreshReuse {
		t.Fatalf("reuse did not return the stable code: %v", reuseErr)
	}
	repository.mu.Lock()
	reuseCommits := repository.reuseRevocationCommits
	sessionRevoked := repository.sessions[sessionIDFromAccess(t, service, initial.AccessToken, now)].record.RevokedAt != nil
	transactionNow := repository.lastTransactionNow
	repository.mu.Unlock()
	if reuseCommits != 1 || !sessionRevoked {
		t.Fatalf("reuse error escaped before revocation commit: commits=%d revoked=%v", reuseCommits, sessionRevoked)
	}
	if !transactionNow.Equal(now) {
		t.Fatalf("repository did not receive the service clock timestamp: %v", transactionNow)
	}
	if _, err := service.Refresh(context.Background(), RefreshInput{
		RefreshToken: rotated.RefreshCookieValue,
		CSRFToken:    rotated.CSRFToken,
	}); errorCode(err) != ErrorAuthentication {
		t.Fatalf("replacement token survived family revocation: %v", err)
	}
}

func TestConcurrentRefreshHasOneRotationThenReuseRevocation(t *testing.T) {
	now := time.Date(2026, 7, 10, 6, 0, 0, 0, time.UTC)
	repository := newMemoryRepository()
	service := newTestService(t, repository, now)
	initial := loginTestAccount(t, repository, service, "student_3")

	start := make(chan struct{})
	errorsByCall := make(chan error, 2)
	for range 2 {
		go func() {
			<-start
			_, err := service.Refresh(context.Background(), RefreshInput{
				RefreshToken: initial.RefreshCookieValue,
				CSRFToken:    initial.CSRFToken,
			})
			errorsByCall <- err
		}()
	}
	close(start)
	var successes, reuseFailures int
	for range 2 {
		err := <-errorsByCall
		switch errorCode(err) {
		case "":
			successes++
		case ErrorRefreshReuse:
			reuseFailures++
		default:
			t.Fatalf("unexpected concurrent refresh result: %v", err)
		}
	}
	if successes != 1 || reuseFailures != 1 {
		t.Fatalf("concurrent outcomes: successes=%d reuse=%d", successes, reuseFailures)
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.reuseRevocationCommits != 1 {
		t.Fatalf("reuse revocation commit count: %d", repository.reuseRevocationCommits)
	}
}

func TestLogoutAndMeEnforceSessionState(t *testing.T) {
	now := time.Date(2026, 7, 10, 7, 0, 0, 0, time.UTC)
	repository := newMemoryRepository()
	service := newTestService(t, repository, now)
	initial := loginTestAccount(t, repository, service, "student_4")
	account, err := service.Me(context.Background(), initial.AccessToken)
	if err != nil || account.ID != initial.Account.ID {
		t.Fatalf("active access token was rejected: account=%#v err=%v", account, err)
	}
	if err := service.Logout(context.Background(), LogoutInput{
		AccessToken:  initial.AccessToken,
		RefreshToken: initial.RefreshCookieValue,
		CSRFToken:    initial.CSRFToken,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Me(context.Background(), initial.AccessToken); errorCode(err) != ErrorAuthentication {
		t.Fatalf("revoked session still authorized /me: %v", err)
	}
	repository.mu.Lock()
	defer repository.mu.Unlock()
	if repository.logoutCommits != 1 {
		t.Fatalf("logout commit count: %d", repository.logoutCommits)
	}
}

func TestAuthenticateReturnsTheDatabaseVerifiedAccountAndExactPrincipal(t *testing.T) {
	now := time.Date(2026, 7, 10, 7, 15, 0, 0, time.UTC)
	repository := newMemoryRepository()
	service := newTestService(t, repository, now)
	initial := loginTestAccount(t, repository, service, "student_authenticated")
	verified, err := service.VerifyAccessToken(initial.AccessToken)
	if err != nil || verified.AccountID != initial.Account.ID || verified.SessionID == "" ||
		verified.Role != initial.Account.Role || verified.AuthRevision != initial.Account.AuthRevision || verified.JWTID == "" {
		t.Fatalf("verified principal = %#v, error=%v", verified, err)
	}

	authenticated, err := service.Authenticate(context.Background(), initial.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if authenticated.Account != initial.Account {
		t.Fatalf("authenticated account = %#v, want %#v", authenticated.Account, initial.Account)
	}
	if authenticated.Principal.AccountID != initial.Account.ID ||
		authenticated.Principal.SessionID == "" ||
		authenticated.Principal.Role != initial.Account.Role ||
		authenticated.Principal.AuthRevision != initial.Account.AuthRevision ||
		authenticated.Principal.JWTID == "" {
		t.Fatalf("authenticated principal is incomplete: %#v", authenticated.Principal)
	}

	repository.mu.Lock()
	session := repository.sessions[authenticated.Principal.SessionID]
	repository.mu.Unlock()
	if session == nil || session.record.AccountID != authenticated.Account.ID {
		t.Fatal("authenticated principal does not identify the verified database session")
	}
}

func TestAuthResultJSONNeverExposesRefreshCookieValue(t *testing.T) {
	now := time.Date(2026, 7, 10, 7, 30, 0, 0, time.UTC)
	repository := newMemoryRepository()
	service := newTestService(t, repository, now)
	result := loginTestAccount(t, repository, service, "student_json")

	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), result.RefreshCookieValue) || strings.Contains(string(encoded), "refresh") {
		t.Fatalf("auth JSON exposed the opaque refresh credential: %s", encoded)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"accessToken", "expiresAt", "csrfToken", "account"} {
		if _, exists := fields[required]; !exists {
			t.Fatalf("auth JSON is missing %q: %s", required, encoded)
		}
	}
	if len(fields) != 4 {
		t.Fatalf("auth JSON contains fields outside the OpenAPI response: %s", encoded)
	}
}

func TestRefreshRejectsDisabledAndAuthRevisionMismatch(t *testing.T) {
	now := time.Date(2026, 7, 10, 8, 0, 0, 0, time.UTC)
	for _, mutate := range []func(*AccountRecord){
		func(account *AccountRecord) { disabled := now; account.DisabledAt = &disabled },
		func(account *AccountRecord) { account.AuthRevision++ },
	} {
		repository := newMemoryRepository()
		service := newTestService(t, repository, now)
		initial := loginTestAccount(t, repository, service, "student_5")
		repository.mu.Lock()
		account := repository.accountsByID[initial.Account.ID]
		mutate(&account)
		repository.accountsByID[initial.Account.ID] = account
		repository.accountsByUsername[account.Username] = account
		repository.mu.Unlock()
		if _, err := service.Refresh(context.Background(), RefreshInput{
			RefreshToken: initial.RefreshCookieValue,
			CSRFToken:    initial.CSRFToken,
		}); errorCode(err) != ErrorAuthentication {
			t.Fatalf("invalid account state was accepted: %v", err)
		}
	}
}

func sessionIDFromAccess(t *testing.T, service *Service, access string, now time.Time) string {
	t.Helper()
	principal, err := service.jwt.ParseAt(access, now)
	if err != nil {
		t.Fatal(err)
	}
	return principal.SessionID
}
