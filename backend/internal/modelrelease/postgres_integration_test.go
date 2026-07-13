package modelrelease

import (
	"context"
	"errors"
	"os"
	"sync"
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
	binderPools := [2]*pgxpool.Pool{
		openIntegrationPool(t, ctx, databaseURL),
		openIntegrationPool(t, ctx, databaseURL),
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
		Version:   "v2-backup-rehearsal",
		Commit:    "0000000000000000000000000000000000000000",
		BuildTime: "1970-01-01T00:00:00Z",
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
	if _, err := gate.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, advisoryLockID); err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	bindings := make([]Binding, 2)
	errorsByIndex := make([]error, 2)
	var binders sync.WaitGroup
	for index := range bindings {
		binders.Add(1)
		go func(index int) {
			defer binders.Done()
			<-start
			bindings[index], errorsByIndex[index] = repositories[index].Bind(ctx, loaded, application)
		}(index)
	}
	close(start)
	if err := waitForAdvisoryLockWaiters(ctx, controlPool, 2); err != nil {
		_ = gate.Rollback(context.Background())
		gateOpen = false
		binders.Wait()
		t.Fatal(err)
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

func waitForAdvisoryLockWaiters(
	ctx context.Context,
	pool *pgxpool.Pool,
	want int64,
) error {
	const maximumUint32 = int64(1<<32 - 1)
	classID := int64(uint64(advisoryLockID) >> 32)
	objectID := advisoryLockID & maximumUint32
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()

	var waiters int64
	for {
		if err := pool.QueryRow(ctx, `
SELECT count(*)
FROM pg_catalog.pg_locks
WHERE locktype = 'advisory'
  AND database = (SELECT oid FROM pg_catalog.pg_database WHERE datname = current_database())
  AND classid::bigint = $1
  AND objid::bigint = $2
  AND objsubid = 1
  AND NOT granted`, classID, objectID).Scan(&waiters); err != nil {
			return err
		}
		if waiters == want {
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.Join(ctx.Err(), errors.New("concurrent binders did not both wait for the model release lock"))
		case <-ticker.C:
		}
	}
}
