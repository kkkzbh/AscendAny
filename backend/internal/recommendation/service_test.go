package recommendation

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

func TestReaderServiceCoalescesConcurrentReadsForExactPrincipal(t *testing.T) {
	repository := newBlockingReaderRepository()
	service, err := NewReaderService(repository)
	if err != nil {
		t.Fatal(err)
	}
	principal := testReaderPrincipal("account-1", "session-1", "jwt-1")
	results := make(chan CurrentRecommendation, 2)
	errorsChannel := make(chan error, 2)
	for range 2 {
		go func() {
			result, readErr := service.ReadCurrent(context.Background(), principal)
			results <- result
			errorsChannel <- readErr
		}()
	}
	waitForReaderWaiters(t, service, principal, 2)
	select {
	case <-repository.started:
	case <-time.After(time.Second):
		t.Fatal("repository read did not start")
	}
	select {
	case <-repository.started:
		t.Fatal("exact concurrent principal started a duplicate repository read")
	case <-time.After(50 * time.Millisecond):
	}
	close(repository.release)
	coalesced := make([]CurrentRecommendation, 2)
	for index := range 2 {
		if err := <-errorsChannel; err != nil {
			t.Fatal(err)
		}
		coalesced[index] = <-results
		if coalesced[index].CurrentAnalyticsHeadRevision != 7 {
			t.Fatalf("coalesced result = %#v", coalesced[index])
		}
	}
	coalesced[0].KnowledgeActivity[0].RecentSeries[0].Correct = 0
	if coalesced[1].KnowledgeActivity[0].RecentSeries[0].Correct != 1 {
		t.Fatal("coalesced callers received shared mutable result slices")
	}
	if calls := repository.callCount(); calls != 1 {
		t.Fatalf("repository calls = %d", calls)
	}
}

func TestReaderServiceKeepsSharedReadAliveForRemainingWaiter(t *testing.T) {
	repository := newBlockingReaderRepository()
	service, err := NewReaderService(repository)
	if err != nil {
		t.Fatal(err)
	}
	principal := testReaderPrincipal("account-1", "session-1", "jwt-1")
	firstContext, cancelFirst := context.WithCancel(context.Background())
	firstResult := make(chan error, 1)
	go func() {
		_, readErr := service.ReadCurrent(firstContext, principal)
		firstResult <- readErr
	}()
	select {
	case <-repository.started:
	case <-time.After(time.Second):
		t.Fatal("repository read did not start")
	}
	secondResult := make(chan error, 1)
	go func() {
		_, readErr := service.ReadCurrent(context.Background(), principal)
		secondResult <- readErr
	}()
	waitForReaderWaiters(t, service, principal, 2)
	select {
	case <-repository.started:
		t.Fatal("second waiter started a duplicate repository read")
	case <-time.After(50 * time.Millisecond):
	}
	cancelFirst()
	if err := <-firstResult; !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled waiter error = %v", err)
	}
	select {
	case <-repository.canceled:
		t.Fatal("one canceled waiter canceled the shared repository read")
	case <-time.After(50 * time.Millisecond):
	}
	close(repository.release)
	if err := <-secondResult; err != nil {
		t.Fatalf("remaining waiter error = %v", err)
	}
	if calls := repository.callCount(); calls != 1 {
		t.Fatalf("repository calls = %d", calls)
	}
}

func TestReaderServiceDoesNotCoalesceDifferentPrincipalTokens(t *testing.T) {
	repository := newBlockingReaderRepository()
	service, err := NewReaderService(repository)
	if err != nil {
		t.Fatal(err)
	}
	errorsChannel := make(chan error, 2)
	for _, principal := range []auth.AccessPrincipal{
		testReaderPrincipal("account-1", "session-1", "jwt-1"),
		testReaderPrincipal("account-1", "session-1", "jwt-2"),
	} {
		principal := principal
		go func() {
			_, readErr := service.ReadCurrent(context.Background(), principal)
			errorsChannel <- readErr
		}()
	}
	for range 2 {
		select {
		case <-repository.started:
		case <-time.After(time.Second):
			t.Fatal("distinct principal repository read did not start")
		}
	}
	close(repository.release)
	for range 2 {
		if err := <-errorsChannel; err != nil {
			t.Fatal(err)
		}
	}
	if calls := repository.callCount(); calls != 2 {
		t.Fatalf("repository calls = %d", calls)
	}
}

type blockingReaderRepository struct {
	mutex    sync.Mutex
	calls    int
	started  chan struct{}
	release  chan struct{}
	canceled chan struct{}
	once     sync.Once
}

func newBlockingReaderRepository() *blockingReaderRepository {
	return &blockingReaderRepository{
		started: make(chan struct{}, 2), release: make(chan struct{}), canceled: make(chan struct{}),
	}
}

func (repository *blockingReaderRepository) ReadCurrent(ctx context.Context, _ auth.AccessPrincipal) (CurrentRecommendation, error) {
	repository.mutex.Lock()
	repository.calls++
	repository.mutex.Unlock()
	repository.started <- struct{}{}
	select {
	case <-repository.release:
		return CurrentRecommendation{
			CurrentAnalyticsHeadRevision: 7,
			KnowledgeActivity: []RecommendationKnowledgeActivity{{
				KnowledgePointID: "arrays",
				RecentSeries:     []RecommendationKnowledgeActivityDay{{Date: "2026-07-18", Attempted: 1, Correct: 1}},
			}},
		}, nil
	case <-ctx.Done():
		repository.once.Do(func() { close(repository.canceled) })
		return CurrentRecommendation{}, ctx.Err()
	}
}

func (repository *blockingReaderRepository) callCount() int {
	repository.mutex.Lock()
	defer repository.mutex.Unlock()
	return repository.calls
}

func testReaderPrincipal(accountID, sessionID, jwtID string) auth.AccessPrincipal {
	return auth.AccessPrincipal{
		AccountID: accountID, SessionID: sessionID, JWTID: jwtID,
		Role: auth.RoleStudent, AuthRevision: 1, ExpiresAt: time.Now().Add(time.Hour),
	}
}

func waitForReaderWaiters(t *testing.T, service *ReaderService, principal auth.AccessPrincipal, expected int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	key := currentRecommendationKey(principal)
	for time.Now().Before(deadline) {
		service.mutex.Lock()
		call := service.inFlight[key]
		waiters := 0
		if call != nil {
			waiters = call.waiters
		}
		service.mutex.Unlock()
		if waiters == expected {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("in-flight waiters did not reach %d", expected)
}
