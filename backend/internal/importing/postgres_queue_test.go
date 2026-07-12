package importing

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/kkkzbh/AscendAny/backend/internal/artifact"
)

func TestPostgresQueueCreatesOneJobAndFirstDurableEvent(t *testing.T) {
	job := testQueuedJob()
	tx := &scriptedTx{rows: []pgx.Row{
		rowScan(func(targets ...any) error {
			*(targets[0].(*int64)) = job.ArtifactID
			return nil
		}),
		jobScanRow(job),
		rowScan(func(targets ...any) error {
			*(targets[0].(*int64)) = 1
			return nil
		}),
	}}
	repository, err := newPostgresRepository(func(context.Context) (dbTx, error) { return tx, nil })
	if err != nil {
		t.Fatal(err)
	}
	published := testPublishedArtifact()

	result, err := repository.QueueArtifact(
		context.Background(),
		published,
		PintiaSnapshotV2MediaType,
		job.PublicID,
	)
	if err != nil {
		t.Fatalf("QueueArtifact() error = %v", err)
	}
	if !result.Created || result.Job.ID != job.ID || !tx.committed || tx.rolledBack {
		t.Fatalf("result=%#v committed=%v rolledBack=%v", result, tx.committed, tx.rolledBack)
	}
	if countQueriesContaining(tx.queries, "INSERT INTO ascendany.import_job_events") != 1 {
		t.Fatalf("queries = %v", tx.queries)
	}
}

func TestPostgresQueueDuplicateReturnsExistingJobWithoutSecondEvent(t *testing.T) {
	job := testQueuedJob()
	published := testPublishedArtifact()
	tx := &scriptedTx{rows: []pgx.Row{
		rowError{err: pgx.ErrNoRows},
		rowScan(func(targets ...any) error {
			*(targets[0].(*int64)) = job.ArtifactID
			*(targets[1].(*int64)) = published.Size
			*(targets[2].(*string)) = PintiaSnapshotV2MediaType
			*(targets[3].(*string)) = published.StorageKey
			return nil
		}),
		rowError{err: pgx.ErrNoRows},
		jobScanRow(job),
	}}
	repository, err := newPostgresRepository(func(context.Context) (dbTx, error) { return tx, nil })
	if err != nil {
		t.Fatal(err)
	}

	result, err := repository.QueueArtifact(
		context.Background(),
		published,
		PintiaSnapshotV2MediaType,
		"22222222-2222-4222-8222-222222222222",
	)
	if err != nil {
		t.Fatalf("QueueArtifact() error = %v", err)
	}
	if result.Created || result.Job.PublicID != job.PublicID {
		t.Fatalf("result = %#v", result)
	}
	if countQueriesContaining(tx.queries, "INSERT INTO ascendany.import_job_events") != 0 {
		t.Fatalf("duplicate appended an event: %v", tx.queries)
	}
}

func TestPostgresClaimAndExpiredReclaimAdvanceAttemptAndEvent(t *testing.T) {
	for _, test := range []struct {
		name           string
		previousStatus JobStatus
		stage          JobStage
		eventType      string
		reclaimed      bool
	}{
		{"queued", JobQueued, StageValidating, "claimed", false},
		{"expired running", JobRunning, StageImporting, "reclaimed", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			job := testQueuedJob()
			owner := "worker-a"
			expires := time.Now().Add(time.Minute)
			job.Status = JobRunning
			job.Stage = test.stage
			job.AttemptCount = 2
			job.LeaseOwner = &owner
			job.LeaseExpiresAt = &expires
			tx := &scriptedTx{rows: []pgx.Row{
				jobScanRowWithPrevious(job, test.previousStatus),
				rowScan(func(targets ...any) error {
					*(targets[0].(*int64)) = 2
					return nil
				}),
			}}
			repository, err := newPostgresRepository(func(context.Context) (dbTx, error) { return tx, nil })
			if err != nil {
				t.Fatal(err)
			}

			claim, err := repository.Claim(context.Background(), owner, time.Minute)
			if err != nil {
				t.Fatalf("Claim() error = %v", err)
			}
			if claim == nil || claim.Reclaimed != test.reclaimed || claim.AttemptCount != 2 {
				t.Fatalf("claim = %#v", claim)
			}
			if countQueriesContaining(tx.queries, "'"+test.eventType+"'") != 0 {
				// Event type is a bound value, so SQL must stay generic.
				t.Fatalf("event type leaked into SQL text: %v", tx.queries)
			}
			if countQueriesContaining(tx.queries, "MAX(event_sequence)") != 1 {
				t.Fatalf("event sequence is not allocated durably: %v", tx.queries)
			}
		})
	}
}

func TestTransactionRollsBackIdentityConflict(t *testing.T) {
	tx := &scriptedTx{}
	repository, err := newPostgresRepository(func(context.Context) (dbTx, error) { return tx, nil })
	if err != nil {
		t.Fatal(err)
	}
	identityErr := importError(ErrorIdentityConflict, true, "test", context.Canceled)
	err = repository.transaction(context.Background(), "identity test", func(dbTx) error { return identityErr })
	assertImportCode(t, err, ErrorIdentityConflict)
	if !tx.rolledBack || tx.committed {
		t.Fatalf("committed=%v rolledBack=%v", tx.committed, tx.rolledBack)
	}
}

func testQueuedJob() Job {
	now := time.Unix(1_700_000_000, 0).UTC()
	return Job{
		ID: 7, PublicID: "11111111-1111-4111-8111-111111111111", ArtifactID: 3,
		Status: JobQueued, Stage: StageReceived, CreatedAt: now, UpdatedAt: now,
	}
}

func testPublishedArtifact() artifact.Artifact {
	hash := strings.Repeat("a", 64)
	return artifact.Artifact{
		Hash: hash, Size: 100, StorageKey: "sha256/aa/" + hash, Path: "/tmp/artifact",
	}
}

func jobScanRow(job Job) pgx.Row {
	return rowScan(func(targets ...any) error {
		assignJobTargets(targets, job)
		return nil
	})
}

func jobScanRowWithPrevious(job Job, previous JobStatus) pgx.Row {
	return rowScan(func(targets ...any) error {
		assignJobTargets(targets, job)
		*(targets[13].(*JobStatus)) = previous
		return nil
	})
}

func assignJobTargets(targets []any, job Job) {
	*(targets[0].(*int64)) = job.ID
	*(targets[1].(*string)) = job.PublicID
	*(targets[2].(*int64)) = job.ArtifactID
	*(targets[3].(*JobStatus)) = job.Status
	*(targets[4].(*JobStage)) = job.Stage
	*(targets[5].(*int32)) = job.AttemptCount
	*(targets[6].(**string)) = job.LeaseOwner
	*(targets[7].(**time.Time)) = job.LeaseExpiresAt
	*(targets[8].(**int64)) = job.SnapshotID
	*(targets[9].(*time.Time)) = job.CreatedAt
	*(targets[10].(**time.Time)) = job.StartedAt
	*(targets[11].(**time.Time)) = job.FinishedAt
	*(targets[12].(*time.Time)) = job.UpdatedAt
}

func countQueriesContaining(queries []string, pattern string) int {
	count := 0
	for _, query := range queries {
		if strings.Contains(query, pattern) {
			count++
		}
	}
	return count
}
