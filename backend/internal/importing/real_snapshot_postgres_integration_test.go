package importing

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/kkkzbh/AscendAny/backend/internal/analytics"
	"github.com/kkkzbh/AscendAny/backend/internal/artifact"
	"github.com/kkkzbh/AscendAny/backend/internal/database"
	"github.com/kkkzbh/AscendAny/backend/internal/pintia"
)

// TestPostgresRealPintiaSnapshotImport is an opt-in rehearsal for a locally
// captured exporter artifact. The database must be disposable and migrated;
// the runtime URL is expected to target PgBouncer transaction pooling.
func TestPostgresRealPintiaSnapshotImport(t *testing.T) {
	snapshotPath := os.Getenv("ASCENDANY_REAL_PINTIA_SNAPSHOT_PATH")
	analyticsConfigPath := os.Getenv("ASCENDANY_REAL_ANALYTICS_CONFIG_PATH")
	databaseURL := os.Getenv("ASCENDANY_TEST_DATABASE_URL")
	databasePassword := os.Getenv("ASCENDANY_TEST_DATABASE_PASSWORD")
	if snapshotPath == "" || analyticsConfigPath == "" || databaseURL == "" || databasePassword == "" {
		t.Skip("real snapshot, analytics config, and disposable database credentials are not configured")
	}
	if !filepath.IsAbs(snapshotPath) {
		t.Fatal("ASCENDANY_REAL_PINTIA_SNAPSHOT_PATH must be absolute")
	}
	if !filepath.IsAbs(analyticsConfigPath) {
		t.Fatal("ASCENDANY_REAL_ANALYTICS_CONFIG_PATH must be absolute")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	limits := pintia.DefaultLimits()
	validator, err := pintia.NewEmbeddedValidator(limits)
	if err != nil {
		t.Fatal("construct embedded Pintia validator")
	}
	snapshotFile, err := os.Open(snapshotPath)
	if err != nil {
		t.Fatal("open real Pintia snapshot")
	}
	snapshot, validationErr := validator.ValidateReader(ctx, snapshotFile)
	closeErr := snapshotFile.Close()
	if validationErr != nil {
		t.Fatal("real Pintia snapshot failed the embedded Go validator")
	}
	if closeErr != nil {
		t.Fatal("close validated real Pintia snapshot")
	}
	domainHash, err := pintia.DomainHash(ctx, snapshot)
	if err != nil {
		t.Fatal("compute real Pintia snapshot domain hash")
	}
	expected := expectedRealSnapshotRows(snapshot)
	analyticsJSON, err := os.ReadFile(analyticsConfigPath)
	if err != nil {
		t.Fatal("read real snapshot analytics configuration")
	}
	parsedAnalytics, err := analytics.ParseConfig(analyticsJSON)
	if err != nil {
		t.Fatal("parse real snapshot analytics configuration")
	}

	pool, err := database.Open(ctx, database.PoolOptions{
		URL:                   databaseURL,
		Password:              databasePassword,
		MaxConnections:        4,
		MinConnections:        0,
		ConnectTimeout:        5 * time.Second,
		MaxConnectionLifetime: 5 * time.Minute,
		MaxConnectionIdleTime: time.Minute,
	})
	if err != nil {
		t.Fatal("open disposable PgBouncer runtime pool")
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		t.Fatal("ping disposable PgBouncer runtime pool")
	}

	var existing int
	if err := pool.QueryRow(ctx, `
SELECT count(*)
FROM ascendany.logical_exams
WHERE platform = 'pintia' AND source_exam_id = $1`, snapshot.Exam.ProblemSetID).Scan(&existing); err != nil {
		t.Fatal("inspect disposable database before real import")
	}
	if existing != 0 {
		t.Fatal("disposable database already contains the real logical exam")
	}

	store, err := artifact.NewStore(filepath.Join(t.TempDir(), "artifacts"), limits.MaxTotalBytes)
	if err != nil {
		t.Fatal("construct disposable artifact store")
	}
	service, err := NewService(pool)
	if err != nil {
		t.Fatal("construct import service")
	}
	worker, err := NewWorker(pool, store, WorkerConfig{
		LeaseDuration: 5 * time.Minute,
		RetryDelay:    time.Second,
		PintiaLimits:  limits,
		Analytics: AnalyticsConfig{
			AlgorithmVersion: parsedAnalytics.Value.AlgorithmVersion,
			ConfigSHA256:     parsedAnalytics.SHA256,
		},
	})
	if err != nil {
		t.Fatal("construct import worker")
	}

	snapshotFile, err = os.Open(snapshotPath)
	if err != nil {
		t.Fatal("reopen real Pintia snapshot")
	}
	publication, publishErr := store.Publish(ctx, snapshotFile)
	closeErr = snapshotFile.Close()
	if publishErr != nil {
		t.Fatal("publish real Pintia snapshot to the disposable artifact store")
	}
	if closeErr != nil {
		_ = publication.Release()
		t.Fatal("close published real Pintia snapshot")
	}
	queued, err := service.QueuePublication(ctx, publication, PintiaSnapshotV2MediaType)
	if err != nil {
		t.Fatal("queue real Pintia snapshot")
	}
	claim, err := service.Claim(ctx, "real-snapshot-rehearsal", 5*time.Minute)
	if err != nil || claim == nil || claim.ID != queued.Job.ID {
		t.Fatal("claim real Pintia snapshot import")
	}
	outcome, err := worker.Process(ctx, *claim)
	if err != nil {
		t.Fatal("process real Pintia snapshot import")
	}
	if outcome.Disposition != ImportCreated || outcome.SnapshotID == nil || outcome.AnalyticsGenerationID == nil {
		t.Fatalf("real snapshot import outcome = %q, want %q", outcome.Disposition, ImportCreated)
	}

	// Replaying the exact exporter artifact must return the durable original
	// job and must not enqueue a second import or analytics generation.
	snapshotFile, err = os.Open(snapshotPath)
	if err != nil {
		t.Fatal("reopen real Pintia snapshot for replay")
	}
	replayPublication, replayPublishErr := store.Publish(ctx, snapshotFile)
	closeErr = snapshotFile.Close()
	if replayPublishErr != nil {
		t.Fatal("republish real Pintia snapshot for replay")
	}
	if closeErr != nil {
		_ = replayPublication.Release()
		t.Fatal("close replayed real Pintia snapshot")
	}
	replayed, err := service.QueuePublication(ctx, replayPublication, PintiaSnapshotV2MediaType)
	if err != nil {
		t.Fatal("replay real Pintia snapshot")
	}
	if replayed.Created || replayed.Job.ID != queued.Job.ID || replayed.Job.PublicID != queued.Job.PublicID {
		t.Fatal("real Pintia snapshot replay did not return the original durable job")
	}

	analyticsWorker, err := analytics.NewWorker(pool, analytics.WorkerConfig{
		Owner:         "real-snapshot-analytics-rehearsal",
		LeaseDuration: 5 * time.Minute,
		AnalyticsJSON: analyticsJSON,
	})
	if err != nil {
		t.Fatal("construct real snapshot analytics worker")
	}
	analyticsOutcome, err := analyticsWorker.RunOne(ctx)
	if err != nil {
		t.Fatal("process real snapshot analytics")
	}
	if analyticsOutcome == nil || analyticsOutcome.GenerationID != *outcome.AnalyticsGenerationID || analyticsOutcome.Disposition != analytics.RunSucceeded {
		t.Fatal("real snapshot analytics did not publish the imported generation")
	}

	assertRealSnapshotRows(t, ctx, pool, *outcome.SnapshotID, domainHash, expected)
	assertIntegrationEvents(t, ctx, pool, queued.Job.ID, []string{
		"received", "claimed", "validation_completed", "snapshot_imported", "completed",
	})
	assertIntegrationManifest(t, ctx, pool, *outcome.AnalyticsGenerationID, *outcome.SnapshotID)
	assertRealSnapshotAnalytics(t, ctx, pool, queued.Job.ID, *outcome.AnalyticsGenerationID, expected)
}

type realSnapshotRows struct {
	problems          int64
	participants      int64
	rankings          int64
	rankingResults    int64
	identifiers       int64
	submissions       int64
	caseResults       int64
	acceptTimeMinimum int64
	acceptTimeMaximum int64
}

func expectedRealSnapshotRows(snapshot *pintia.Snapshot) realSnapshotRows {
	rows := realSnapshotRows{
		problems:     int64(len(snapshot.Problems)),
		participants: int64(len(snapshot.Participants)),
		submissions:  int64(len(snapshot.Submissions)),
	}
	firstAcceptTime := true
	for _, participant := range snapshot.Participants {
		if participant.StudentUserID != nil {
			rows.identifiers++
		}
		if participant.StudentNumber != nil {
			rows.identifiers++
		}
		if participant.Ranking == nil {
			continue
		}
		rows.rankings++
		rows.rankingResults += int64(len(participant.Ranking.ProblemResults))
		for _, result := range participant.Ranking.ProblemResults {
			value, _ := result.AcceptTimeSeconds.Int64()
			if firstAcceptTime || value < rows.acceptTimeMinimum {
				rows.acceptTimeMinimum = value
			}
			if firstAcceptTime || value > rows.acceptTimeMaximum {
				rows.acceptTimeMaximum = value
			}
			firstAcceptTime = false
		}
	}
	for _, submission := range snapshot.Submissions {
		rows.caseResults += int64(len(submission.CaseResults))
	}
	return rows
}

type realSnapshotQueryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func assertRealSnapshotRows(
	t *testing.T,
	ctx context.Context,
	pool realSnapshotQueryer,
	snapshotID int64,
	domainHash string,
	want realSnapshotRows,
) {
	t.Helper()
	var got realSnapshotRows
	var storedDomainHash string
	if err := pool.QueryRow(ctx, `
SELECT snapshot.domain_hash,
       (SELECT count(*) FROM ascendany.pintia_snapshot_problems WHERE snapshot_id = snapshot.snapshot_id),
       (SELECT count(*) FROM ascendany.pintia_snapshot_participants WHERE snapshot_id = snapshot.snapshot_id),
       (SELECT count(*) FROM ascendany.pintia_rankings WHERE snapshot_id = snapshot.snapshot_id),
       (SELECT count(*) FROM ascendany.pintia_ranking_problem_results WHERE snapshot_id = snapshot.snapshot_id),
       (
           SELECT count(*)
           FROM ascendany.pintia_actor_identifiers AS identifier
           JOIN ascendany.pintia_snapshot_participants AS participant
             ON participant.actor_id = identifier.actor_id
            AND participant.snapshot_id = snapshot.snapshot_id
       ),
       (SELECT count(*) FROM ascendany.pintia_snapshot_submissions WHERE snapshot_id = snapshot.snapshot_id),
       (SELECT count(*) FROM ascendany.pintia_submission_case_results WHERE snapshot_id = snapshot.snapshot_id),
       (SELECT min(accept_time_seconds) FROM ascendany.pintia_ranking_problem_results WHERE snapshot_id = snapshot.snapshot_id),
       (SELECT max(accept_time_seconds) FROM ascendany.pintia_ranking_problem_results WHERE snapshot_id = snapshot.snapshot_id)
FROM ascendany.exam_snapshots AS snapshot
WHERE snapshot.snapshot_id = $1`, snapshotID).Scan(
		&storedDomainHash,
		&got.problems,
		&got.participants,
		&got.rankings,
		&got.rankingResults,
		&got.identifiers,
		&got.submissions,
		&got.caseResults,
		&got.acceptTimeMinimum,
		&got.acceptTimeMaximum,
	); err != nil {
		t.Fatal("read imported real snapshot row counts")
	}
	if storedDomainHash != domainHash {
		t.Fatal("stored real snapshot domain hash differs from Go canonical hash")
	}
	if got != want {
		t.Fatalf("stored real snapshot row counts = %+v, want %+v", got, want)
	}
}

func assertRealSnapshotAnalytics(
	t *testing.T,
	ctx context.Context,
	pool realSnapshotQueryer,
	jobID int64,
	generationID int64,
	want realSnapshotRows,
) {
	t.Helper()
	var generationStatus string
	var currentGenerationID int64
	var headRevision int64
	var jobStatus string
	var jobStage string
	var studentCount int64
	var canonicalStudentCount int64
	var problemCount int64
	var canonicalProblemCount int64
	if err := pool.QueryRow(ctx, `
SELECT generation.status,
       head.current_generation_id,
       head.head_revision,
       job.status,
       job.stage,
       (SELECT count(*) FROM ascendany.student_analytics WHERE analytics_generation_id = generation.analytics_generation_id),
       (
           SELECT count(*)
           FROM ascendany.student_analytics
           WHERE analytics_generation_id = generation.analytics_generation_id
             AND metrics ->> 'protocol' = 'student_analytics_v1'
             AND jsonb_array_length(metrics -> 'examHistory') = 1
             AND jsonb_array_length(metrics -> 'ratingHistory') = 1
       ),
       (SELECT count(*) FROM ascendany.problem_analytics WHERE analytics_generation_id = generation.analytics_generation_id),
       (
           SELECT count(*)
           FROM ascendany.problem_analytics
           WHERE analytics_generation_id = generation.analytics_generation_id
             AND metrics ->> 'protocol' = 'problem_analytics_v1'
       )
FROM ascendany.analytics_generations AS generation
JOIN ascendany.analytics_head AS head ON head.singleton
JOIN ascendany.import_jobs AS job ON job.import_job_id = $2
WHERE generation.analytics_generation_id = $1`, generationID, jobID).Scan(
		&generationStatus,
		&currentGenerationID,
		&headRevision,
		&jobStatus,
		&jobStage,
		&studentCount,
		&canonicalStudentCount,
		&problemCount,
		&canonicalProblemCount,
	); err != nil {
		t.Fatal("read real snapshot analytics state")
	}
	if generationStatus != "succeeded" || currentGenerationID != generationID || headRevision != 1 || jobStatus != "succeeded" || jobStage != "completed" {
		t.Fatal("real snapshot analytics generation or import job is not atomically completed")
	}
	if studentCount != want.participants || canonicalStudentCount != want.participants {
		t.Fatalf("real snapshot student analytics counts = %d/%d, want %d", studentCount, canonicalStudentCount, want.participants)
	}
	if problemCount != want.problems || canonicalProblemCount != want.problems {
		t.Fatalf("real snapshot problem analytics counts = %d/%d, want %d", problemCount, canonicalProblemCount, want.problems)
	}
}
