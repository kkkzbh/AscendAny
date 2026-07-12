package oj

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
	"github.com/kkkzbh/AscendAny/backend/internal/judgecontract"
)

func TestPostgresOJProblemSubmissionAndFencedJudgeLifecycle(t *testing.T) {
	databaseURL := os.Getenv("ASCENDANY_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ASCENDANY_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	principal := seedOJAdmin(t, ctx, pool)
	repository, err := NewPostgresRepository(pool)
	if err != nil {
		t.Fatal(err)
	}
	drainIntegrationJudgeQueue(t, ctx, pool, repository)
	service, err := NewService(repository, DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	suffix := strings.ReplaceAll(integrationUUID(t), "-", "")
	input := CreateProblemVersionInput{
		Principal: principal, Slug: "oj-integration-" + suffix, ExpectedHeadRevision: 0,
		Lifecycle: LifecycleActive, Title: "Integration sum", StatementMarkdown: "Return the sum.",
		SolutionMarkdown: ptrString("Use one pass."), KnowledgeTags: []string{"array", "sum"},
		TimeLimitMS: 1000, MemoryLimitBytes: 128 << 20, OutputLimitBytes: 1 << 20,
		ProblemSpec: json.RawMessage(` {"checker":"token","caseCount":2} `),
		TestBundle:  integrationArtifact("oj-tests-"+suffix, TestBundleMediaType),
	}
	first, err := service.CreateProblemVersion(ctx, input)
	if err != nil || first.Idempotent || first.Problem.HeadRevision != 1 || first.Problem.CurrentVersion == nil {
		t.Fatalf("first=%#v error=%v", first, err)
	}
	replay, err := service.CreateProblemVersion(ctx, input)
	if err != nil || !replay.Idempotent || replay.Problem.HeadRevision != 1 {
		t.Fatalf("replay=%#v error=%v", replay, err)
	}
	secondInput := input
	secondInput.ExpectedHeadRevision = 1
	secondInput.Title = "Integration sum revised"
	second, err := service.CreateProblemVersion(ctx, secondInput)
	if err != nil || second.Idempotent || second.Problem.HeadRevision != 2 || second.Problem.CurrentVersion == nil ||
		second.Problem.CurrentVersion.Number != 2 {
		t.Fatalf("second=%#v error=%v", second, err)
	}
	if _, err := service.CreateProblemVersion(ctx, input); CodeOf(err) != ErrorIdempotencyConflict {
		t.Fatalf("inactive replay error=%v code=%q", err, CodeOf(err))
	}
	got, found, err := service.GetProblem(ctx, ProblemQuery{Principal: principal, ProblemID: first.Problem.ID})
	if err != nil || !found || got.HeadRevision != 2 || got.CurrentVersion == nil || got.CurrentVersion.SolutionMarkdown == nil {
		t.Fatalf("get=%#v found=%t error=%v", got, found, err)
	}
	student := seedOJStudent(t, ctx, pool)
	studentView, found, err := service.GetProblem(ctx, ProblemQuery{Principal: student, ProblemID: first.Problem.ID})
	if err != nil || !found || studentView.CurrentVersion == nil || studentView.CurrentVersion.SolutionMarkdown != nil ||
		studentView.CurrentVersion.ProblemSpec != nil || studentView.CurrentVersion.TestBundle != nil {
		t.Fatalf("student view=%#v found=%t error=%v", studentView, found, err)
	}
	page, err := service.ListProblems(ctx, ProblemListQuery{Principal: principal, Limit: MaxPageSize, IncludeArchived: true})
	if err != nil || !containsProblem(page.Items, first.Problem.ID) {
		t.Fatalf("page=%#v error=%v", page, err)
	}

	source := integrationArtifact("oj-source-"+suffix, CPP20SourceMediaType)
	stdin := integrationArtifact("oj-stdin-"+suffix, PlainTextMediaType)
	submissionInput := CreateSubmissionInput{
		Principal: principal, ClientRequestID: integrationUUID(t), ProblemID: first.Problem.ID,
		ExpectedProblemHeadRevision: 2, Mode: SubmissionRun, LanguageID: LanguageCPP20,
		Source: source, Stdin: &stdin,
	}
	created, err := service.CreateSubmission(ctx, submissionInput)
	if err != nil || !created.Created || created.Submission.ProblemVersion != 2 {
		t.Fatalf("created submission=%#v error=%v", created, err)
	}
	replayedSubmission, err := service.CreateSubmission(ctx, submissionInput)
	if err != nil || replayedSubmission.Created || replayedSubmission.Submission != created.Submission {
		t.Fatalf("replayed submission=%#v error=%v", replayedSubmission, err)
	}
	changed := submissionInput
	changed.Source = integrationArtifact("different-source-"+suffix, CPP20SourceMediaType)
	if _, err := service.CreateSubmission(ctx, changed); CodeOf(err) != ErrorIdempotencyConflict {
		t.Fatalf("changed replay error=%v code=%q", err, CodeOf(err))
	}

	staleClaim, err := repository.ClaimJudge(ctx, "oj-stale", integrationUUID(t), time.Minute)
	if err != nil || staleClaim == nil || staleClaim.ID != created.Submission.JudgeJobID {
		t.Fatalf("stale claim=%#v error=%v", staleClaim, err)
	}
	if _, err := pool.Exec(ctx, `
UPDATE ascendany.oj_judge_jobs
SET lease_expires_at = clock_timestamp() - interval '1 second'
WHERE judge_job_id = $1`, staleClaim.DatabaseID); err != nil {
		t.Fatal(err)
	}
	activeClaim, err := repository.ClaimJudge(ctx, "oj-active", integrationUUID(t), time.Minute)
	if err != nil || activeClaim == nil || !activeClaim.Reclaimed || activeClaim.AttemptCount != 2 || activeClaim.ID != staleClaim.ID {
		t.Fatalf("active claim=%#v error=%v", activeClaim, err)
	}
	if err := repository.FailJudge(ctx, *staleClaim, "stale_attempt", "must be fenced"); CodeOf(err) != ErrorLeaseLost {
		t.Fatalf("stale completion error=%v code=%q", err, CodeOf(err))
	}
	request, err := repository.LoadExecution(ctx, *activeClaim)
	if err != nil || request.ProblemVersion != 2 || request.Stdin == nil || request.Source.SHA256 != source.SHA256 {
		t.Fatalf("execution request=%#v error=%v", request, err)
	}
	publisher := &integrationOutputPublisher{suffix: suffix}
	worker, err := NewWorker(repository, executorFunc(func(context.Context, judgecontract.ExecutionRequest) (judgecontract.ExecutorResult, error) {
		return judgecontract.ExecutorResult{
			Verdict: judgecontract.VerdictAccepted, ScoreFraction: 1, PassedCaseCount: 2, TotalCaseCount: 2,
			MaxTimeMS: 7, MaxMemoryBytes: 8192, Output: []byte("42\n"),
			ResultManifest: json.RawMessage(`{"cases":[{"verdict":"accepted"},{"verdict":"accepted"}]}`),
		}, nil
	}), publisher, WorkerConfig{Owner: "oj-worker", LeaseDuration: time.Minute, RetryDelay: time.Second, MaximumAttempts: 3})
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := worker.Process(ctx, *activeClaim)
	if err != nil || outcome.Disposition != "completed" || outcome.Result == nil || outcome.Result.Verdict != VerdictAccepted || !publisher.released {
		t.Fatalf("outcome=%#v released=%t error=%v", outcome, publisher.released, err)
	}
	detail, found, err := service.GetSubmission(ctx, SubmissionQuery{Principal: principal, SubmissionID: created.Submission.ID})
	if err != nil || !found || detail.Status != JobCompleted || detail.Result == nil ||
		detail.Result.ResultSHA256 != outcome.Result.ResultSHA256 || detail.Result.Output == nil {
		t.Fatalf("submission detail=%#v found=%t error=%v", detail, found, err)
	}
	if _, found, err := service.GetSubmission(ctx, SubmissionQuery{Principal: student, SubmissionID: created.Submission.ID}); err != nil || found {
		t.Fatalf("foreign student found=%t error=%v", found, err)
	}
	events, found, err := service.ReadJudgeEvents(ctx, JudgeEventQuery{
		Principal: principal, SubmissionID: created.Submission.ID, AfterSequence: 0, Limit: 100,
	})
	if err != nil || !found || len(events.Events) != 4 || events.Events[1].Type != "running" || events.Events[2].Type != "running" ||
		events.Events[3].Type != "completed" || strings.Contains(string(events.Events[1].Payload), "leaseOwner") {
		t.Fatalf("events=%#v found=%t error=%v", events, found, err)
	}

	secondSubmission := submissionInput
	secondSubmission.ClientRequestID = integrationUUID(t)
	secondSubmission.Mode = SubmissionSubmit
	secondSubmission.Stdin = nil
	secondCreated, err := service.CreateSubmission(ctx, secondSubmission)
	if err != nil || !secondCreated.Created {
		t.Fatalf("second submission=%#v error=%v", secondCreated, err)
	}
	retryClaim, err := repository.ClaimJudge(ctx, "oj-retry", integrationUUID(t), time.Minute)
	if err != nil || retryClaim == nil || retryClaim.ID != secondCreated.Submission.JudgeJobID {
		t.Fatalf("retry claim=%#v error=%v", retryClaim, err)
	}
	if err := repository.RequeueJudge(ctx, *retryClaim, time.Second, "runner_busy"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)
	failureClaim, err := repository.ClaimJudge(ctx, "oj-failure", integrationUUID(t), time.Minute)
	if err != nil || failureClaim == nil || failureClaim.AttemptCount != 2 {
		t.Fatalf("failure claim=%#v error=%v", failureClaim, err)
	}
	if err := repository.FailJudge(ctx, *failureClaim, "sandbox_unavailable", "sandbox service is unavailable"); err != nil {
		t.Fatal(err)
	}

	var completedStatus, failedStatus string
	var completedEvents, failedEvents int
	if err := pool.QueryRow(ctx, `
SELECT job.status, count(event.event_sequence)
FROM ascendany.oj_judge_jobs AS job
JOIN ascendany.oj_judge_job_events AS event ON event.judge_job_id = job.judge_job_id
WHERE job.public_id = $1::uuid
GROUP BY job.status`, created.Submission.JudgeJobID).Scan(&completedStatus, &completedEvents); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
SELECT job.status, count(event.event_sequence)
FROM ascendany.oj_judge_jobs AS job
JOIN ascendany.oj_judge_job_events AS event ON event.judge_job_id = job.judge_job_id
WHERE job.public_id = $1::uuid
GROUP BY job.status`, secondCreated.Submission.JudgeJobID).Scan(&failedStatus, &failedEvents); err != nil {
		t.Fatal(err)
	}
	if completedStatus != "completed" || completedEvents != 4 || failedStatus != "system_error" || failedEvents != 5 {
		t.Fatalf("completed=%s/%d failed=%s/%d", completedStatus, completedEvents, failedStatus, failedEvents)
	}
}

func TestPostgresOJConcurrentNewSlugConvergesOnOneHead(t *testing.T) {
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
	repository, err := NewPostgresRepository(pool)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(repository, DefaultPolicy())
	if err != nil {
		t.Fatal(err)
	}
	principals := []auth.AccessPrincipal{seedOJAdmin(t, ctx, pool), seedOJAdmin(t, ctx, pool)}
	suffix := strings.ReplaceAll(integrationUUID(t), "-", "")
	base := CreateProblemVersionInput{
		Slug: "oj-concurrent-" + suffix, ExpectedHeadRevision: 0, Lifecycle: LifecycleActive,
		Title: "Concurrent problem", StatementMarkdown: "Create one durable head.",
		KnowledgeTags: []string{"concurrency"}, TimeLimitMS: 1000, MemoryLimitBytes: 64 << 20,
		OutputLimitBytes: 1 << 20, ProblemSpec: json.RawMessage(`{"checker":"exact"}`),
		TestBundle: integrationArtifact("oj-concurrent-bundle-"+suffix, TestBundleMediaType),
	}
	results := make([]CreateProblemVersionResult, 2)
	errorsByCall := make([]error, 2)
	start := make(chan struct{})
	var wait sync.WaitGroup
	for index := range principals {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			input := base
			input.Principal = principals[index]
			results[index], errorsByCall[index] = service.CreateProblemVersion(ctx, input)
		}(index)
	}
	close(start)
	wait.Wait()
	for index, callErr := range errorsByCall {
		if callErr != nil {
			t.Fatalf("call %d error=%v code=%q", index, callErr, CodeOf(callErr))
		}
	}
	if results[0].Problem.ID == "" || results[0].Problem.ID != results[1].Problem.ID ||
		results[0].Problem.HeadRevision != 1 || results[1].Problem.HeadRevision != 1 ||
		results[0].Idempotent == results[1].Idempotent {
		t.Fatalf("results=%#v", results)
	}
}

type integrationOutputPublisher struct {
	suffix   string
	released bool
}

func (publisher *integrationOutputPublisher) PublishJudgeOutput(_ context.Context, output []byte) (*PublishedOutput, error) {
	artifact := integrationArtifact(string(output)+publisher.suffix, JudgeOutputMediaType)
	artifact.SizeBytes = int64(len(output))
	return &PublishedOutput{Artifact: artifact, Release: func() error { publisher.released = true; return nil }}, nil
}

func seedOJAdmin(t *testing.T, ctx context.Context, pool *pgxpool.Pool) auth.AccessPrincipal {
	t.Helper()
	accountID := integrationUUID(t)
	sessionID := integrationUUID(t)
	suffix := strings.ReplaceAll(accountID, "-", "")[:12]
	var accountDatabaseID int64
	if err := pool.QueryRow(ctx, `
INSERT INTO ascendany.auth_accounts (
    public_id, username, password_phc, display_name, role, auth_revision, created_at, updated_at
)
VALUES ($1::uuid, $2, '$argon2id$v=19$m=65536,t=3,p=1$test$test', 'OJ Integration Admin',
        'admin', 1, clock_timestamp(), clock_timestamp())
RETURNING account_id`, accountID, "oj_admin_"+suffix).Scan(&accountDatabaseID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO ascendany.auth_sessions (
    public_id, account_id, auth_revision, created_at, expires_at, last_seen_at
)
VALUES ($1::uuid, $2, 1, clock_timestamp(), clock_timestamp() + interval '1 hour', clock_timestamp())`, sessionID, accountDatabaseID); err != nil {
		t.Fatal(err)
	}
	return auth.AccessPrincipal{AccountID: accountID, SessionID: sessionID,
		JWTID: integrationUUID(t), Role: auth.RoleAdmin, AuthRevision: 1}
}

func seedOJStudent(t *testing.T, ctx context.Context, pool *pgxpool.Pool) auth.AccessPrincipal {
	t.Helper()
	accountID := integrationUUID(t)
	sessionID := integrationUUID(t)
	suffix := strings.ReplaceAll(accountID, "-", "")[:12]
	studentNumber := "oj-student-" + suffix
	var actorID int64
	if err := pool.QueryRow(ctx, `
INSERT INTO ascendany.pintia_actors (user_id)
VALUES ($1)
RETURNING actor_id`, "oj-user-"+suffix).Scan(&actorID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO ascendany.pintia_actor_identifiers (identifier_kind, identifier_value, actor_id)
VALUES ('student_number', $1, $2)`, studentNumber, actorID); err != nil {
		t.Fatal(err)
	}
	var accountDatabaseID int64
	if err := pool.QueryRow(ctx, `
INSERT INTO ascendany.auth_accounts (
    public_id, username, password_phc, display_name, student_number, actor_id,
    role, auth_revision, created_at, updated_at
)
VALUES ($1::uuid, $2, '$argon2id$v=19$m=65536,t=3,p=1$test$test', 'OJ Integration Student',
        $3, $4, 'student', 1, clock_timestamp(), clock_timestamp())
RETURNING account_id`, accountID, "oj_student_"+suffix, studentNumber, actorID).Scan(&accountDatabaseID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO ascendany.auth_sessions (
    public_id, account_id, auth_revision, created_at, expires_at, last_seen_at
)
VALUES ($1::uuid, $2, 1, clock_timestamp(), clock_timestamp() + interval '1 hour', clock_timestamp())`, sessionID, accountDatabaseID); err != nil {
		t.Fatal(err)
	}
	return auth.AccessPrincipal{AccountID: accountID, SessionID: sessionID,
		JWTID: integrationUUID(t), Role: auth.RoleStudent, AuthRevision: 1}
}

func integrationArtifact(content, mediaType string) Artifact {
	digest := sha256.Sum256([]byte(content))
	hash := hex.EncodeToString(digest[:])
	return Artifact{SHA256: hash, SizeBytes: int64(len(content)), MediaType: mediaType,
		StorageKey: "sha256/" + hash[:2] + "/" + hash}
}

func integrationUUID(t *testing.T) string {
	t.Helper()
	value, err := randomUUIDv4()
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func containsProblem(items []Problem, id string) bool {
	for _, item := range items {
		if item.ID == id {
			return true
		}
	}
	return false
}

func drainIntegrationJudgeQueue(t *testing.T, ctx context.Context, pool *pgxpool.Pool, repository *PostgresRepository) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
UPDATE ascendany.oj_judge_jobs
SET lease_expires_at = clock_timestamp() - interval '1 second', updated_at = clock_timestamp()
WHERE status = 'running'`); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		claim, err := repository.ClaimJudge(ctx, "oj-integration-cleanup", integrationUUID(t), time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if claim != nil {
			if err := repository.FailJudge(ctx, *claim, "integration_cleanup", "stale integration fixture cleanup"); err != nil {
				t.Fatal(err)
			}
			continue
		}
		var pending int
		if err := pool.QueryRow(ctx, `
SELECT count(*) FROM ascendany.oj_judge_jobs WHERE status IN ('queued', 'running')`).Scan(&pending); err != nil {
			t.Fatal(err)
		}
		if pending == 0 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("timed out draining stale OJ integration jobs")
}

func ptrString(value string) *string { return &value }
