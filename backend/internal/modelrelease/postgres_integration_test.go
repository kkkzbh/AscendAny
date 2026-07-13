package modelrelease

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kkkzbh/AscendAny/backend/internal/modelartifact"
)

func TestPostgresBindingPersistsVerifiedArtifact(t *testing.T) {
	databaseURL := os.Getenv("ASCENDANY_TEST_DATABASE_URL")
	modelPath := os.Getenv("ASCENDANY_TEST_RECOMMENDATION_MODEL_PATH")
	modelSHA256 := os.Getenv("ASCENDANY_TEST_RECOMMENDATION_MODEL_SHA256")
	if databaseURL == "" || modelPath == "" || modelSHA256 == "" {
		t.Skip("ASCENDANY_TEST_DATABASE_URL and recommendation model artifact variables are not configured")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	controlPool := openIntegrationPool(t, ctx, databaseURL)
	var binderAcquireArmed atomic.Bool
	binderAcquireReady := make(chan struct{}, 2)
	binderAcquireRelease := make(chan struct{})
	binderPools := [2]*pgxpool.Pool{
		openGatedIntegrationPool(t, ctx, databaseURL, &binderAcquireArmed, binderAcquireReady, binderAcquireRelease),
		openGatedIntegrationPool(t, ctx, databaseURL, &binderAcquireArmed, binderAcquireReady, binderAcquireRelease),
	}

	var initialReleaseCount, initialActivationCount, initialHeadCount int64
	if err := controlPool.QueryRow(ctx, `
SELECT (SELECT count(*) FROM ascendany.recommendation_model_releases),
       (SELECT count(*) FROM ascendany.recommendation_model_activation_events),
       (SELECT count(*) FROM ascendany.recommendation_model_head)
`).Scan(&initialReleaseCount, &initialActivationCount, &initialHeadCount); err != nil {
		t.Fatal(err)
	}
	if initialReleaseCount != 0 || initialActivationCount != 0 || initialHeadCount != 0 {
		t.Fatalf(
			"model release tables are not fresh: releases=%d activations=%d heads=%d",
			initialReleaseCount,
			initialActivationCount,
			initialHeadCount,
		)
	}

	loaded, err := modelartifact.Load(modelPath, modelSHA256)
	if err != nil {
		t.Fatal(err)
	}
	var repositories [2]*Repository
	for index, pool := range binderPools {
		repositories[index], err = NewRepository(pool)
		if err != nil {
			t.Fatal(err)
		}
	}
	application := ApplicationIdentity{
		Version:   "0.2.0-integration",
		Commit:    "0000000000000000000000000000000000000000",
		BuildTime: "1970-01-01T00:00:00Z",
	}
	for index, repository := range repositories {
		registered, registerErr := repository.Register(ctx, loaded)
		if registerErr != nil {
			t.Fatalf("register through repository %d: %v", index, registerErr)
		}
		if registered.ReleaseID != 1 || registered.HeadRevision != 0 || registered.Activated ||
			registered.ArtifactSHA256 != loaded.SHA256 || registered.ManifestSHA256 == "" {
			t.Fatalf("registered model through repository %d = %#v", index, registered)
		}
	}
	var registeredReleaseCount, registeredActivationCount, registeredHeadCount int64
	if err := controlPool.QueryRow(ctx, `
SELECT (SELECT count(*) FROM ascendany.recommendation_model_releases),
       (SELECT count(*) FROM ascendany.recommendation_model_activation_events),
       (SELECT count(*) FROM ascendany.recommendation_model_head)
`).Scan(&registeredReleaseCount, &registeredActivationCount, &registeredHeadCount); err != nil {
		t.Fatal(err)
	}
	if registeredReleaseCount != 1 || registeredActivationCount != 0 || registeredHeadCount != 0 {
		t.Fatalf(
			"registration mutated activation state: releases=%d activations=%d heads=%d",
			registeredReleaseCount,
			registeredActivationCount,
			registeredHeadCount,
		)
	}
	gate, err := controlPool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		t.Fatal(err)
	}
	gateOpen := true
	defer func() {
		if gateOpen {
			_ = gate.Rollback(context.Background())
		}
	}()
	if _, err := gate.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, ModelTransitionAdvisoryLockID); err != nil {
		t.Fatal(err)
	}
	binderAcquireArmed.Store(true)

	start := make(chan struct{})
	bindings := make([]Binding, 2)
	errorsByIndex := make([]error, 2)
	started := make(chan struct{}, len(bindings))
	finished := make(chan int, len(bindings))
	var binders sync.WaitGroup
	for index := range bindings {
		binders.Add(1)
		go func(index int) {
			defer binders.Done()
			<-start
			started <- struct{}{}
			bindings[index], errorsByIndex[index] = repositories[index].Bind(ctx, loaded, application)
			finished <- index
		}(index)
	}
	close(start)
	for range bindings {
		<-started
	}
	for range bindings {
		select {
		case <-binderAcquireReady:
		case index := <-finished:
			close(binderAcquireRelease)
			_ = gate.Rollback(context.Background())
			gateOpen = false
			binders.Wait()
			t.Fatalf("concurrent binder %d completed before reaching the acquisition gate: %v", index, errorsByIndex[index])
		case <-ctx.Done():
			close(binderAcquireRelease)
			_ = gate.Rollback(context.Background())
			gateOpen = false
			binders.Wait()
			t.Fatal(errors.Join(ctx.Err(), errors.New("concurrent binders did not reach the acquisition gate")))
		}
	}
	close(binderAcquireRelease)
	if err := waitForAdvisoryLockWaiters(ctx, controlPool, ModelTransitionAdvisoryLockID, int64(len(bindings))); err != nil {
		_ = gate.Rollback(context.Background())
		gateOpen = false
		binders.Wait()
		t.Fatal(err)
	}
	select {
	case index := <-finished:
		_ = gate.Rollback(context.Background())
		gateOpen = false
		binders.Wait()
		t.Fatalf("concurrent binder %d completed while the model transition lock was held", index)
	default:
	}
	if err := gate.Commit(ctx); err != nil {
		_ = gate.Rollback(context.Background())
		gateOpen = false
		binders.Wait()
		t.Fatal(err)
	}
	gateOpen = false
	binders.Wait()
	for index, bindErr := range errorsByIndex {
		if bindErr != nil {
			t.Fatalf("concurrent binder %d: %v", index, bindErr)
		}
	}
	if bindings[0].ReleaseID != 1 || bindings[0].HeadRevision != 1 ||
		bindings[1].ReleaseID != bindings[0].ReleaseID || bindings[1].HeadRevision != bindings[0].HeadRevision ||
		bindings[0].ManifestSHA256 != bindings[1].ManifestSHA256 ||
		bindings[0].ArtifactSHA256 != loaded.SHA256 || bindings[1].ArtifactSHA256 != loaded.SHA256 ||
		bindings[0].Activated == bindings[1].Activated {
		t.Fatalf("concurrent fresh bindings = %#v and %#v", bindings[0], bindings[1])
	}
	binding := bindings[0]
	if !binding.Activated {
		binding = bindings[1]
	}
	for index, repository := range repositories {
		replayed, replayErr := repository.Bind(ctx, loaded, application)
		if replayErr != nil {
			t.Fatalf("replay through repository %d: %v", index, replayErr)
		}
		if replayed.Activated || replayed.ReleaseID != binding.ReleaseID || replayed.HeadRevision != binding.HeadRevision ||
			replayed.ManifestSHA256 != binding.ManifestSHA256 {
			t.Fatalf("replayed model binding through repository %d = %#v; first = %#v", index, replayed, binding)
		}
		current, currentErr := repository.RequireCurrent(ctx, loaded, application)
		if currentErr != nil {
			t.Fatalf("current model verification through repository %d: %v", index, currentErr)
		}
		if current.Activated || current.ReleaseID != binding.ReleaseID || current.HeadRevision != binding.HeadRevision ||
			current.ManifestSHA256 != binding.ManifestSHA256 {
			t.Fatalf("current model binding through repository %d = %#v; first = %#v", index, current, binding)
		}
	}

	var releaseCount, activationCount, headCount, headRevision int64
	var artifactSHA256, purpose string
	if err := controlPool.QueryRow(ctx, `
SELECT (SELECT count(*) FROM ascendany.recommendation_model_releases),
       (SELECT count(*) FROM ascendany.recommendation_model_activation_events),
       (SELECT count(*) FROM ascendany.recommendation_model_head),
       head.head_revision,
       release.artifact_sha256,
       release.model_purpose
FROM ascendany.recommendation_model_head AS head
JOIN ascendany.recommendation_model_releases AS release
  ON release.recommendation_model_release_id = head.current_release_id
WHERE head.singleton
`).Scan(&releaseCount, &activationCount, &headCount, &headRevision, &artifactSHA256, &purpose); err != nil {
		t.Fatal(err)
	}
	if releaseCount != 1 || activationCount != 1 || headCount != 1 || headRevision != binding.HeadRevision ||
		artifactSHA256 != loaded.SHA256 || purpose != string(binding.ModelPurpose) {
		t.Fatalf(
			"stored binding differs: releases=%d activations=%d heads=%d revision=%d artifact=%s purpose=%s",
			releaseCount,
			activationCount,
			headCount,
			headRevision,
			artifactSHA256,
			purpose,
		)
	}
}

func openIntegrationPool(t *testing.T, ctx context.Context, databaseURL string) *pgxpool.Pool {
	t.Helper()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	return pool
}

func openGatedIntegrationPool(
	t *testing.T,
	ctx context.Context,
	databaseURL string,
	armed *atomic.Bool,
	ready chan<- struct{},
	release <-chan struct{},
) *pgxpool.Pool {
	t.Helper()
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	var blocked atomic.Bool
	config.BeforeAcquire = func(acquireContext context.Context, _ *pgx.Conn) bool {
		if !armed.Load() || !blocked.CompareAndSwap(false, true) {
			return true
		}
		select {
		case ready <- struct{}{}:
		case <-acquireContext.Done():
			return false
		}
		select {
		case <-release:
			return true
		case <-acquireContext.Done():
			return false
		}
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	return pool
}

func waitForAdvisoryLockWaiters(
	ctx context.Context,
	pool *pgxpool.Pool,
	lockID int64,
	want int64,
) error {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	var waiters int64
	for {
		if err := pool.QueryRow(ctx, `
SELECT count(*)
FROM pg_catalog.pg_locks
WHERE locktype = 'advisory'
  AND database = (SELECT oid FROM pg_catalog.pg_database WHERE datname = current_database())
  AND classid = (($1::bigint >> 32) & 4294967295)::oid
  AND objid = ($1::bigint & 4294967295)::oid
  AND objsubid = 1
  AND NOT granted`, lockID).Scan(&waiters); err != nil {
			return err
		}
		if waiters >= want {
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.Join(ctx.Err(), errors.New("no concurrent binder reached the model release lock"))
		case <-ticker.C:
		}
	}
}
