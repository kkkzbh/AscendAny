package recommendation

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kkkzbh/AscendAny/backend/internal/artifact"
)

func TestPostgresTrainerAgentTerminalReceiptsFenceAndReplay(t *testing.T) {
	databaseURL := os.Getenv("ASCENDANY_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ASCENDANY_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	fixture := seedRecommendationFixture(t, ctx, pool)
	repository, err := NewPostgresRepository(pool)
	if err != nil {
		t.Fatal(err)
	}
	store, err := artifact.NewStore(filepath.Join(t.TempDir(), "trainer-agent-artifacts"), 32<<20)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewQueueService(repository, store, ServiceConfig{MaximumInputBundleBytes: 8 << 20})
	if err != nil {
		t.Fatal(err)
	}

	generation := publishFixtureAnalyticsGeneration(t, ctx, pool, fixture)
	queued, err := service.QueueTraining(ctx, QueueInput{
		Principal: fixture.AdminPrincipal, ConfigurationKey: fixture.ConfigurationKey,
		ExpectedAnalyticsGenerationID: generation.ID, ExpectedAnalyticsHeadRevision: generation.Revision,
	})
	if err != nil || !queued.Created {
		t.Fatalf("queue = %#v error = %v", queued, err)
	}
	claim, err := repository.ClaimTraining(ctx, "rtx-01", integrationUUID(t), 10*time.Second)
	if err != nil || claim == nil {
		t.Fatalf("claim = %#v error = %v", claim, err)
	}
	attempt := trainerAgentAttemptFromClaim(*claim)
	resolved, actorIDs, err := repository.ResolveTrainerAgentClaim(ctx, attempt)
	if err != nil || resolved.ID != claim.ID || len(actorIDs) != len(fixture.ActorIDs) {
		t.Fatalf("resolved = %#v actors = %#v error = %v", resolved, actorIDs, err)
	}
	if _, err := repository.RenewTrainerAgentLease(ctx, attempt, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	input := parseIntegrationClaimInput(t, ctx, store, *claim)
	output := parseTrainerAgentIntegrationOutput(t, input)
	publication, err := store.Publish(ctx, bytes.NewReader(output.CanonicalJSON))
	if err != nil {
		t.Fatal(err)
	}
	requestSHA256 := strings.Repeat("a", 64)
	modelID := integrationUUID(t)
	command := PublishCommand{
		Claim: *claim, ModelPublicID: modelID, Input: input, Output: output, Artifact: publication.Artifact,
		MediaType: TrainingOutputMediaTypeV2,
		Receipt: &TrainerAgentReceiptCommand{
			Attempt: attempt, Operation: TrainerAgentOutputOperation, RequestSHA256: requestSHA256,
		},
	}
	published, err := repository.PublishTrainingOutput(ctx, command)
	if releaseErr := publication.Release(); releaseErr != nil {
		t.Fatal(releaseErr)
	}
	if err != nil || published.Disposition != PublishActivated || published.ModelID != modelID {
		t.Fatalf("published = %#v error = %v", published, err)
	}

	replay := command
	replay.ModelPublicID = integrationUUID(t)
	replayed, err := repository.PublishTrainingOutput(ctx, replay)
	if err != nil || replayed.Disposition != PublishActivated || replayed.ModelID != modelID {
		t.Fatalf("replayed = %#v error = %v", replayed, err)
	}
	receipt, err := repository.LookupTrainerAgentTerminalReceipt(ctx, attempt, TrainerAgentOutputOperation, requestSHA256)
	if err != nil || receipt == nil || receipt.Result != TrainerAgentActivated || receipt.ModelID == nil || *receipt.ModelID != modelID ||
		receipt.RuntimeConstructionSHA256 == nil || *receipt.RuntimeConstructionSHA256 != output.Model.RuntimeConstructionSHA256 ||
		receipt.RuntimeProvenanceSHA256 == nil || *receipt.RuntimeProvenanceSHA256 != output.Model.RuntimeProvenanceSHA256 ||
		receipt.RuntimeTreeSHA256 == nil || *receipt.RuntimeTreeSHA256 != output.Model.RuntimeTreeSHA256 ||
		receipt.HostCapabilitySHA256 == nil || *receipt.HostCapabilitySHA256 != output.Model.HostCapabilitySHA256 ||
		receipt.RuntimeAttestationSHA256 == nil || *receipt.RuntimeAttestationSHA256 != output.Model.RuntimeAttestationSHA256 {
		t.Fatalf("receipt = %#v error = %v", receipt, err)
	}
	if _, err := repository.LookupTrainerAgentTerminalReceipt(ctx, attempt, TrainerAgentOutputOperation, strings.Repeat("b", 64)); CodeOf(err) != ErrorLeaseLost {
		t.Fatalf("conflicting replay error = %v code = %q", err, CodeOf(err))
	}
	if _, err := repository.LookupTrainerAgentTerminalReceipt(ctx, attempt, TrainerAgentFailureOperation, requestSHA256); CodeOf(err) != ErrorLeaseLost {
		t.Fatalf("cross-operation replay error = %v code = %q", err, CodeOf(err))
	}
	if _, err := repository.RenewTrainerAgentLease(ctx, attempt, 10*time.Second); CodeOf(err) != ErrorLeaseLost {
		t.Fatalf("terminal heartbeat error = %v code = %q", err, CodeOf(err))
	}

	generation = publishFixtureAnalyticsGeneration(t, ctx, pool, fixture)
	retryQueued, err := service.QueueTraining(ctx, QueueInput{
		Principal: fixture.AdminPrincipal, ConfigurationKey: fixture.ConfigurationKey,
		ExpectedAnalyticsGenerationID: generation.ID, ExpectedAnalyticsHeadRevision: generation.Revision,
	})
	if err != nil || !retryQueued.Created {
		t.Fatalf("retry queue = %#v error = %v", retryQueued, err)
	}
	retryClaim, err := repository.ClaimTraining(ctx, "rtx-01", integrationUUID(t), 10*time.Second)
	if err != nil || retryClaim == nil {
		t.Fatalf("retry claim = %#v error = %v", retryClaim, err)
	}
	retryReceipt, err := repository.ReportTrainerAgentFailure(ctx, TrainerAgentFailureCommand{
		Claim: *retryClaim, RequestSHA256: strings.Repeat("c", 64), Code: "trainer_busy",
		Detail: "trainer is temporarily busy", Retryable: true, RetryDelay: time.Second,
	})
	if err != nil || retryReceipt.Result != TrainerAgentRequeued {
		t.Fatalf("retry receipt = %#v error = %v", retryReceipt, err)
	}
	retryReplay, err := repository.ReportTrainerAgentFailure(ctx, TrainerAgentFailureCommand{
		Claim: *retryClaim, RequestSHA256: strings.Repeat("c", 64), Code: "trainer_busy",
		Detail: "trainer is temporarily busy", Retryable: true, RetryDelay: time.Second,
	})
	if err != nil || retryReplay.Result != TrainerAgentRequeued {
		t.Fatalf("retry replay = %#v error = %v", retryReplay, err)
	}
	if _, err := repository.RenewTrainerAgentLease(ctx, trainerAgentAttemptFromClaim(*retryClaim), 10*time.Second); CodeOf(err) != ErrorLeaseLost {
		t.Fatalf("requeued heartbeat error = %v code = %q", err, CodeOf(err))
	}
	if _, err := repository.LookupTrainerAgentTerminalReceipt(
		ctx, trainerAgentAttemptFromClaim(*retryClaim), TrainerAgentOutputOperation, strings.Repeat("c", 64),
	); CodeOf(err) != ErrorLeaseLost {
		t.Fatalf("stale output after failure error = %v code = %q", err, CodeOf(err))
	}
	if _, err := repository.ReportTrainerAgentFailure(ctx, TrainerAgentFailureCommand{
		Claim: *retryClaim, RequestSHA256: strings.Repeat("f", 64), Code: "trainer_busy",
		Detail: "trainer is temporarily busy", Retryable: true, RetryDelay: time.Second,
	}); CodeOf(err) != ErrorLeaseLost {
		t.Fatalf("conflicting failure replay error = %v code = %q", err, CodeOf(err))
	}
	time.Sleep(1100 * time.Millisecond)
	secondAttempt, err := repository.ClaimTraining(ctx, "rtx-01", integrationUUID(t), 10*time.Second)
	if err != nil || secondAttempt == nil || secondAttempt.ID != retryClaim.ID || secondAttempt.AttemptCount != retryClaim.AttemptCount+1 {
		t.Fatalf("second attempt = %#v error = %v", secondAttempt, err)
	}
	failedReceipt, err := repository.ReportTrainerAgentFailure(ctx, TrainerAgentFailureCommand{
		Claim: *secondAttempt, RequestSHA256: strings.Repeat("d", 64), Code: "trainer_rejected",
		Detail: "trainer rejected the immutable input", Retryable: false, RetryDelay: time.Second,
	})
	if err != nil || failedReceipt.Result != TrainerAgentFailed {
		t.Fatalf("failed receipt = %#v error = %v", failedReceipt, err)
	}

	generation = publishFixtureAnalyticsGeneration(t, ctx, pool, fixture)
	rejectedQueued, err := service.QueueTraining(ctx, QueueInput{
		Principal: fixture.AdminPrincipal, ConfigurationKey: fixture.ConfigurationKey,
		ExpectedAnalyticsGenerationID: generation.ID, ExpectedAnalyticsHeadRevision: generation.Revision,
	})
	if err != nil || !rejectedQueued.Created {
		t.Fatalf("rejection queue = %#v error = %v", rejectedQueued, err)
	}
	rejectedClaim, err := repository.ClaimTraining(ctx, "rtx-01", integrationUUID(t), 10*time.Second)
	if err != nil || rejectedClaim == nil {
		t.Fatalf("rejection claim = %#v error = %v", rejectedClaim, err)
	}
	rejectedReceipt, err := repository.RejectTrainerAgentOutput(ctx, TrainerAgentOutputRejectionCommand{
		Claim: *rejectedClaim, RequestSHA256: strings.Repeat("e", 64),
		FailureCode: "invalid_training_output", FailureDetail: "output actor set differs from immutable input",
		ErrorCode: "output_rejected", ErrorDetail: "Training output was rejected.",
	})
	if err != nil || rejectedReceipt.Result != TrainerAgentOutputRejected {
		t.Fatalf("rejected receipt = %#v error = %v", rejectedReceipt, err)
	}
	rejectedReplay, err := repository.RejectTrainerAgentOutput(ctx, TrainerAgentOutputRejectionCommand{
		Claim: *rejectedClaim, RequestSHA256: strings.Repeat("e", 64),
		FailureCode: "invalid_training_output", FailureDetail: "output actor set differs from immutable input",
		ErrorCode: "output_rejected", ErrorDetail: "Training output was rejected.",
	})
	if err != nil || rejectedReplay.Result != TrainerAgentOutputRejected {
		t.Fatalf("rejected replay = %#v error = %v", rejectedReplay, err)
	}

	var receiptCount, modelCount int
	if err := pool.QueryRow(ctx, `
SELECT count(*), count(model_public_id)
FROM ascendany.recommendation_trainer_attempt_receipts
WHERE training_run_id IN (
    SELECT training_run_id
    FROM ascendany.recommendation_training_runs
    WHERE public_id IN ($1::uuid, $2::uuid, $3::uuid)
)`, queued.Run.ID, retryQueued.Run.ID, rejectedQueued.Run.ID).Scan(&receiptCount, &modelCount); err != nil {
		t.Fatal(err)
	}
	if receiptCount != 4 || modelCount != 1 {
		t.Fatalf("receipt count = %d model receipt count = %d", receiptCount, modelCount)
	}
	if _, err := pool.Exec(ctx, `
UPDATE ascendany.recommendation_trainer_attempt_receipts
SET request_sha256 = request_sha256
WHERE training_run_id = $1`, claim.DatabaseID); !postgresCodeIs(err, "42501") {
		t.Fatalf("runtime receipt mutation error = %v, want SQLSTATE 42501", err)
	}

	generation = publishFixtureAnalyticsGeneration(t, ctx, pool, fixture)
	uncommittedQueued, err := service.QueueTraining(ctx, QueueInput{
		Principal: fixture.AdminPrincipal, ConfigurationKey: fixture.ConfigurationKey,
		ExpectedAnalyticsGenerationID: generation.ID, ExpectedAnalyticsHeadRevision: generation.Revision,
	})
	if err != nil || !uncommittedQueued.Created {
		t.Fatalf("uncommitted queue = %#v error = %v", uncommittedQueued, err)
	}
	uncommittedClaim, err := repository.ClaimTraining(ctx, "rtx-01", integrationUUID(t), 10*time.Second)
	if err != nil || uncommittedClaim == nil {
		t.Fatalf("uncommitted claim = %#v error = %v", uncommittedClaim, err)
	}
	_, _, err = repository.ResolveTrainerAgentClaim(ctx, trainerAgentAttemptFromClaim(*uncommittedClaim))
	if err != nil {
		t.Fatal(err)
	}
	uncommittedInput := parseIntegrationClaimInput(t, ctx, store, *uncommittedClaim)
	uncommittedOutput := parseTrainerAgentIntegrationOutput(t, uncommittedInput)
	uncommittedPublication, err := store.Publish(ctx, bytes.NewReader(uncommittedOutput.CanonicalJSON))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
UPDATE ascendany.recommendation_training_runs
SET lease_expires_at = clock_timestamp() - interval '1 second', updated_at = clock_timestamp()
WHERE training_run_id = $1`, uncommittedClaim.DatabaseID); err != nil {
		t.Fatal(err)
	}
	_, err = repository.PublishTrainingOutput(ctx, PublishCommand{
		Claim: *uncommittedClaim, ModelPublicID: integrationUUID(t), Input: uncommittedInput, Output: uncommittedOutput,
		Artifact: uncommittedPublication.Artifact, MediaType: TrainingOutputMediaTypeV2,
		Receipt: &TrainerAgentReceiptCommand{
			Attempt: trainerAgentAttemptFromClaim(*uncommittedClaim), Operation: TrainerAgentOutputOperation,
			RequestSHA256: strings.Repeat("1", 64),
		},
	})
	if releaseErr := uncommittedPublication.Release(); releaseErr != nil {
		t.Fatal(releaseErr)
	}
	if CodeOf(err) != ErrorLeaseLost {
		t.Fatalf("uncommitted output error = %v code = %q", err, CodeOf(err))
	}
	var uncommittedReceiptCount int
	if err := pool.QueryRow(ctx, `
SELECT count(*)
FROM ascendany.recommendation_trainer_attempt_receipts AS receipt
JOIN ascendany.recommendation_training_runs AS run ON run.training_run_id = receipt.training_run_id
WHERE run.public_id = $1::uuid`, uncommittedQueued.Run.ID).Scan(&uncommittedReceiptCount); err != nil {
		t.Fatal(err)
	}
	if uncommittedReceiptCount != 0 {
		t.Fatalf("uncommitted receipt count = %d", uncommittedReceiptCount)
	}
	recoveredClaim, err := repository.ClaimTraining(ctx, "rtx-01", integrationUUID(t), 10*time.Second)
	if err != nil || recoveredClaim == nil || recoveredClaim.ID != uncommittedClaim.ID ||
		recoveredClaim.AttemptCount != uncommittedClaim.AttemptCount+1 {
		t.Fatalf("recovered claim = %#v error = %v", recoveredClaim, err)
	}
	if err := repository.FailTraining(ctx, *recoveredClaim, "test_cleanup", "integration recovery cleanup"); err != nil {
		t.Fatal(err)
	}
}

func postgresCodeIs(err error, code string) bool {
	var postgresError *pgconn.PgError
	return errors.As(err, &postgresError) && postgresError.Code == code
}

func parseTrainerAgentIntegrationOutput(t *testing.T, input ParsedInputBundle) ParsedOutputBundle {
	t.Helper()
	raw := outputTestBundle(t, input)
	parsed, err := ParseOutputBundle(raw, 8<<20, input)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
