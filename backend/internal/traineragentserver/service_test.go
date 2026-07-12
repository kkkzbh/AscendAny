package traineragentserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/artifact"
	"github.com/kkkzbh/AscendAny/backend/internal/canonicaljson"
	"github.com/kkkzbh/AscendAny/backend/internal/recommendation"
	"github.com/kkkzbh/AscendAny/backend/internal/traineragentprotocol"
)

const (
	testRunID        = "11111111-1111-4111-8111-111111111111"
	testAttemptToken = "22222222-2222-4222-8222-222222222222"
	testModelID      = "33333333-3333-4333-8333-333333333333"
)

type trainerRepositoryStub struct {
	claim   func(context.Context, string, string, time.Duration) (*recommendation.Claim, error)
	resolve func(context.Context, recommendation.TrainerAgentAttempt) (recommendation.Claim, []int64, error)
	renew   func(context.Context, recommendation.TrainerAgentAttempt, time.Duration) (time.Time, error)
	lookup  func(context.Context, recommendation.TrainerAgentAttempt, recommendation.TrainerAgentTerminalOperation, string) (*recommendation.TrainerAgentTerminalReceipt, error)
	report  func(context.Context, recommendation.TrainerAgentFailureCommand) (recommendation.TrainerAgentTerminalReceipt, error)
	reject  func(context.Context, recommendation.TrainerAgentOutputRejectionCommand) (recommendation.TrainerAgentTerminalReceipt, error)
	publish func(context.Context, recommendation.PublishCommand) (recommendation.PublishResult, error)
	requeue func(context.Context, recommendation.Claim, time.Duration, string) error
	fail    func(context.Context, recommendation.Claim, string, string) error
}

type trainerArtifactStoreStub struct {
	publication *artifact.Publication
	verified    artifact.Artifact
}

func (stub trainerArtifactStoreStub) Publish(context.Context, io.Reader) (*artifact.Publication, error) {
	return stub.publication, nil
}

func (stub trainerArtifactStoreStub) Verify(_ context.Context, hash string, size int64) (artifact.Artifact, error) {
	if stub.verified.Hash != hash || stub.verified.Size != size {
		return artifact.Artifact{}, errors.New("verified input artifact differs")
	}
	return stub.verified, nil
}

func (stub trainerRepositoryStub) ClaimTraining(ctx context.Context, owner, token string, duration time.Duration) (*recommendation.Claim, error) {
	return stub.claim(ctx, owner, token, duration)
}

func (stub trainerRepositoryStub) ResolveTrainerAgentClaim(ctx context.Context, attempt recommendation.TrainerAgentAttempt) (recommendation.Claim, []int64, error) {
	return stub.resolve(ctx, attempt)
}

func (stub trainerRepositoryStub) RenewTrainerAgentLease(ctx context.Context, attempt recommendation.TrainerAgentAttempt, duration time.Duration) (time.Time, error) {
	return stub.renew(ctx, attempt, duration)
}

func (stub trainerRepositoryStub) LookupTrainerAgentTerminalReceipt(ctx context.Context, attempt recommendation.TrainerAgentAttempt, operation recommendation.TrainerAgentTerminalOperation, digest string) (*recommendation.TrainerAgentTerminalReceipt, error) {
	if stub.lookup == nil {
		return nil, nil
	}
	return stub.lookup(ctx, attempt, operation, digest)
}

func (stub trainerRepositoryStub) ReportTrainerAgentFailure(ctx context.Context, command recommendation.TrainerAgentFailureCommand) (recommendation.TrainerAgentTerminalReceipt, error) {
	return stub.report(ctx, command)
}

func (stub trainerRepositoryStub) RejectTrainerAgentOutput(ctx context.Context, command recommendation.TrainerAgentOutputRejectionCommand) (recommendation.TrainerAgentTerminalReceipt, error) {
	return stub.reject(ctx, command)
}

func (stub trainerRepositoryStub) PublishTrainingOutput(ctx context.Context, command recommendation.PublishCommand) (recommendation.PublishResult, error) {
	return stub.publish(ctx, command)
}

func (stub trainerRepositoryStub) RequeueTraining(ctx context.Context, claim recommendation.Claim, duration time.Duration, reason string) error {
	return stub.requeue(ctx, claim, duration, reason)
}

func (stub trainerRepositoryStub) FailTraining(ctx context.Context, claim recommendation.Claim, code, detail string) error {
	return stub.fail(ctx, claim, code, detail)
}

func TestNewServiceRejectsFractionalRetryPolicy(t *testing.T) {
	t.Parallel()
	_, err := newService(
		trainerRepositoryStub{}, trainerArtifactStoreStub{},
		ServiceConfig{
			LeaseDuration: 30 * time.Second, RetryDelay: time.Second + time.Nanosecond,
			MaximumInputBundleBytes: 1 << 20, MaximumOutputBundleBytes: 1 << 20,
		},
		func() (string, error) { return testAttemptToken, nil },
	)
	if err == nil || !strings.Contains(err.Error(), "whole-millisecond") {
		t.Fatalf("newService() error = %v", err)
	}
}

func TestServiceClaimsVerifiedImmutableInput(t *testing.T) {
	t.Parallel()
	store, claim, input, _ := trainerServiceFixture(t)
	repository := trainerRepositoryStub{
		claim: func(_ context.Context, owner, token string, duration time.Duration) (*recommendation.Claim, error) {
			if owner != "rtx-01" || token != testAttemptToken || duration != 30*time.Second {
				t.Fatalf("claim arguments = %q %q %s", owner, token, duration)
			}
			return &claim, nil
		},
		requeue: unexpectedRequeue(t), fail: unexpectedFail(t),
	}
	service := testTrainerService(t, repository, store, testAttemptToken)
	result, err := service.Claim(context.Background(), "rtx-01", traineragentprotocol.ClaimRequestV1{
		Protocol: traineragentprotocol.ClaimRequestProtocolV1, AgentID: "rtx-01", LeaseDurationMilliseconds: 30000,
	})
	if err != nil || result == nil || result.RunID != testRunID || result.AttemptToken != testAttemptToken ||
		result.InputManifestSHA256 != claim.InputManifestSHA256 || !bytes.Equal(result.InputBundle, input) {
		t.Fatalf("claim result = %#v error = %v", result, err)
	}
	_, expectedDigest, err := canonicaljson.Object(input, 1<<20)
	if err != nil || result.InputBundleSHA256 != expectedDigest {
		t.Fatalf("input digest = %q expected = %q error = %v", result.InputBundleSHA256, expectedDigest, err)
	}
}

func TestServiceRejectsUnsupportedClaimProtocolBeforeRepositoryAccess(t *testing.T) {
	t.Parallel()
	store, _, _, _ := trainerServiceFixture(t)
	service := testTrainerService(t, trainerRepositoryStub{}, store, testAttemptToken)
	result, err := service.Claim(context.Background(), "rtx-01", traineragentprotocol.ClaimRequestV1{
		Protocol: "ascendany.recommendation.trainer-agent.claim-request.v2",
		AgentID:  "rtx-01", LeaseDurationMilliseconds: 30000,
	})
	if result != nil || CodeOf(err) != ErrorUnsupportedProtocol {
		t.Fatalf("claim result = %#v error = %v code = %q", result, err, CodeOf(err))
	}
}

func TestServiceHeartbeatsPublishesAndReplaysOutput(t *testing.T) {
	t.Parallel()
	store, claim, _, parsedInput := trainerServiceFixture(t)
	output := trainingOutput(t, parsedInput)
	_, outputDigest, err := canonicaljson.Object(output, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	outputRequest := traineragentprotocol.OutputRequestV1{
		Protocol: traineragentprotocol.OutputRequestProtocolV1, AgentID: "rtx-01", AttemptToken: testAttemptToken,
		InputManifestSHA256: claim.InputManifestSHA256, OutputBundleSHA256: outputDigest, OutputBundle: output,
	}
	requestDigest := wireDigest(t, outputRequest)
	renewedAt := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	published := false
	repository := trainerRepositoryStub{
		resolve: func(_ context.Context, attempt recommendation.TrainerAgentAttempt) (recommendation.Claim, []int64, error) {
			assertAttempt(t, attempt)
			return claim, parsedInput.ActorIDs, nil
		},
		renew: func(_ context.Context, attempt recommendation.TrainerAgentAttempt, duration time.Duration) (time.Time, error) {
			assertAttempt(t, attempt)
			if duration != 30*time.Second {
				t.Fatalf("renew duration = %s", duration)
			}
			return renewedAt, nil
		},
		publish: func(_ context.Context, command recommendation.PublishCommand) (recommendation.PublishResult, error) {
			published = true
			if command.Claim.ID != claim.ID || command.Input.ManifestSHA256 != parsedInput.ManifestSHA256 ||
				command.Output.SHA256 != outputDigest || command.Receipt == nil ||
				command.Receipt.RequestSHA256 != requestDigest || command.Receipt.Attempt.RunID != testRunID {
				t.Fatalf("publish command = %#v", command)
			}
			return recommendation.PublishResult{Disposition: recommendation.PublishActivated, ModelID: command.ModelPublicID}, nil
		},
	}
	service := testTrainerService(t, repository, store, testModelID)
	heartbeat, err := service.Heartbeat(context.Background(), testRunID, "rtx-01", traineragentprotocol.HeartbeatRequestV1{
		Protocol: traineragentprotocol.HeartbeatRequestProtocolV1, AgentID: "rtx-01", AttemptToken: testAttemptToken,
	})
	if err != nil || heartbeat.LeaseExpiresAt != renewedAt.Format(time.RFC3339Nano) {
		t.Fatalf("heartbeat = %#v error = %v", heartbeat, err)
	}
	response, err := service.Publish(context.Background(), testRunID, "rtx-01", requestDigest, outputRequest)
	if err != nil || !published || response.Disposition != "activated" || response.ModelID != testModelID ||
		response.RuntimeConstructionSHA256 != strings.Repeat("a", 64) ||
		response.RuntimeProvenanceSHA256 != strings.Repeat("b", 64) ||
		response.RuntimeTreeSHA256 != strings.Repeat("c", 64) ||
		response.HostCapabilitySHA256 != strings.Repeat("d", 64) ||
		response.RuntimeAttestationSHA256 != strings.Repeat("e", 64) {
		t.Fatalf("publication = %#v published = %v error = %v", response, published, err)
	}

	replayRepository := trainerRepositoryStub{lookup: func(_ context.Context, attempt recommendation.TrainerAgentAttempt, operation recommendation.TrainerAgentTerminalOperation, digest string) (*recommendation.TrainerAgentTerminalReceipt, error) {
		assertAttempt(t, attempt)
		if operation != recommendation.TrainerAgentOutputOperation || digest != requestDigest {
			t.Fatalf("lookup operation/digest = %q %q", operation, digest)
		}
		modelID := testModelID
		runtimeConstructionSHA256 := strings.Repeat("a", 64)
		runtimeProvenanceSHA256 := strings.Repeat("b", 64)
		runtimeTreeSHA256 := strings.Repeat("c", 64)
		hostCapabilitySHA256 := strings.Repeat("d", 64)
		runtimeAttestationSHA256 := strings.Repeat("e", 64)
		return &recommendation.TrainerAgentTerminalReceipt{
			Operation: operation, RequestSHA256: digest, Result: recommendation.TrainerAgentActivated, ModelID: &modelID,
			RuntimeConstructionSHA256: &runtimeConstructionSHA256,
			RuntimeProvenanceSHA256:   &runtimeProvenanceSHA256,
			RuntimeTreeSHA256:         &runtimeTreeSHA256,
			HostCapabilitySHA256:      &hostCapabilitySHA256,
			RuntimeAttestationSHA256:  &runtimeAttestationSHA256,
		}, nil
	}}
	replayService := testTrainerService(t, replayRepository, store, "44444444-4444-4444-8444-444444444444")
	replayed, err := replayService.Publish(context.Background(), testRunID, "rtx-01", requestDigest, outputRequest)
	if err != nil || replayed.ModelID != testModelID || replayed.Disposition != "activated" {
		t.Fatalf("replayed = %#v error = %v", replayed, err)
	}
}

func TestServiceReportsPublicationLockReleaseFailureAfterCommit(t *testing.T) {
	t.Parallel()
	inputStore, claim, _, parsedInput := trainerServiceFixture(t)
	output := trainingOutput(t, parsedInput)
	_, outputDigest, err := canonicaljson.Object(output, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	request := traineragentprotocol.OutputRequestV1{
		Protocol: traineragentprotocol.OutputRequestProtocolV1, AgentID: "rtx-01", AttemptToken: testAttemptToken,
		InputManifestSHA256: claim.InputManifestSHA256, OutputBundleSHA256: outputDigest, OutputBundle: output,
	}
	repository := trainerRepositoryStub{
		resolve: func(context.Context, recommendation.TrainerAgentAttempt) (recommendation.Claim, []int64, error) {
			return claim, parsedInput.ActorIDs, nil
		},
		publish: func(_ context.Context, command recommendation.PublishCommand) (recommendation.PublishResult, error) {
			return recommendation.PublishResult{Disposition: recommendation.PublishActivated, ModelID: command.ModelPublicID}, nil
		},
	}
	verifiedInput, err := inputStore.Verify(context.Background(), claim.InputArtifact.Hash, claim.InputArtifact.Size)
	if err != nil {
		t.Fatal(err)
	}
	store := trainerArtifactStoreStub{verified: verifiedInput, publication: &artifact.Publication{Artifact: artifact.Artifact{
		Hash: outputDigest, Size: int64(len(output)), StorageKey: "sha256/" + outputDigest[:2] + "/" + outputDigest,
		Path: "/test/output",
	}}}
	service := testTrainerService(t, repository, store, testModelID)
	response, err := service.Publish(context.Background(), testRunID, "rtx-01", wireDigest(t, request), request)
	if response != (traineragentprotocol.OutputResponseV1{}) || CodeOf(err) != ErrorStorageUnavailable {
		t.Fatalf("response = %#v error = %v code = %q", response, err, CodeOf(err))
	}
}

func TestServiceRejectsInvalidOutputTerminallyAndReportsFailure(t *testing.T) {
	t.Parallel()
	store, claim, _, parsedInput := trainerServiceFixture(t)
	invalidOutputRequest := traineragentprotocol.OutputRequestV1{
		Protocol: traineragentprotocol.OutputRequestProtocolV1, AgentID: "rtx-01", AttemptToken: testAttemptToken,
		InputManifestSHA256: claim.InputManifestSHA256, OutputBundleSHA256: strings.Repeat("c", 64),
		OutputBundle: json.RawMessage(`{"invalid":true}`),
	}
	requestDigest := wireDigest(t, invalidOutputRequest)
	rejected := false
	reported := false
	repository := trainerRepositoryStub{
		resolve: func(context.Context, recommendation.TrainerAgentAttempt) (recommendation.Claim, []int64, error) {
			return claim, parsedInput.ActorIDs, nil
		},
		reject: func(_ context.Context, command recommendation.TrainerAgentOutputRejectionCommand) (recommendation.TrainerAgentTerminalReceipt, error) {
			rejected = true
			if command.RequestSHA256 != requestDigest || command.FailureCode != "invalid_training_output" {
				t.Fatalf("rejection = %#v", command)
			}
			errorCode, detail, retryable := "output_rejected", "Training output was rejected.", false
			return recommendation.TrainerAgentTerminalReceipt{
				Operation: recommendation.TrainerAgentOutputOperation, RequestSHA256: requestDigest,
				Result: recommendation.TrainerAgentOutputRejected, ErrorCode: &errorCode, ErrorDetail: &detail,
				ErrorRetryable: &retryable,
			}, nil
		},
		report: func(_ context.Context, command recommendation.TrainerAgentFailureCommand) (recommendation.TrainerAgentTerminalReceipt, error) {
			reported = true
			if !command.Retryable || command.Code != "trainer_timeout" || command.RetryDelay != time.Second {
				t.Fatalf("failure command = %#v", command)
			}
			return recommendation.TrainerAgentTerminalReceipt{
				Operation: recommendation.TrainerAgentFailureOperation, RequestSHA256: command.RequestSHA256,
				Result: recommendation.TrainerAgentRequeued,
			}, nil
		},
	}
	service := testTrainerService(t, repository, store, testModelID)
	_, err := service.Publish(context.Background(), testRunID, "rtx-01", requestDigest, invalidOutputRequest)
	if CodeOf(err) != ErrorOutputRejected || !rejected {
		t.Fatalf("publish error = %v code = %q rejected = %v", err, CodeOf(err), rejected)
	}
	failureRequest := traineragentprotocol.FailureRequestV1{
		Protocol: traineragentprotocol.FailureRequestProtocolV1, AgentID: "rtx-01", AttemptToken: testAttemptToken,
		Code: "trainer_timeout", Detail: "training timed out", Retryable: true,
	}
	failure, err := service.ReportFailure(context.Background(), testRunID, "rtx-01", wireDigest(t, failureRequest), failureRequest)
	if err != nil || !reported || failure.Disposition != "requeued" {
		t.Fatalf("failure = %#v reported = %v error = %v", failure, reported, err)
	}
}

func trainerServiceFixture(t *testing.T) (*artifact.Store, recommendation.Claim, []byte, recommendation.ParsedInputBundle) {
	t.Helper()
	manifest, manifestSHA256 := canonicalObjectWithDigest(t, map[string]any{"source": "fixture"})
	problemOneHTML := "<p>Array practice.</p>"
	problemTwoHTML := "<p>Graph practice.</p>"
	maxScore := "100"
	timeLimit := int64(1000)
	memoryLimit := int64(67_108_864)
	problemOneHash := trainerProblemFactSHA256(t, "501", "Array Practice", problemOneHTML, timeLimit, memoryLimit)
	problemTwoHash := trainerProblemFactSHA256(t, "502", "Graph Practice", problemTwoHTML, timeLimit, memoryLimit)
	catalog, catalogSHA256 := canonicalObjectWithDigest(t, map[string]any{
		"taxonomyId": "recommendation.fixture",
		"knowledgePoints": []any{
			map[string]any{"id": "arrays", "label": "Arrays", "description": "Array fundamentals", "prerequisiteIds": []any{}},
			map[string]any{"id": "graphs", "label": "Graphs", "description": "Graph traversal", "prerequisiteIds": []any{"arrays"}},
		},
		"problemAssignments": []any{
			map[string]any{"platform": "pintia", "problemId": "501", "problemFactSha256": problemOneHash, "knowledge": []any{map[string]any{"knowledgePointId": "arrays", "weight": 1}}},
			map[string]any{"platform": "pintia", "problemId": "502", "problemFactSha256": problemTwoHash, "knowledge": []any{map[string]any{"knowledgePointId": "graphs", "weight": 1}}},
		},
	})
	configuration, configurationSHA256 := canonicalObjectWithDigest(t, map[string]any{
		"algorithm": "knowledge_mirt_v1", "knowledgeCatalogVersionId": "3", "accelerator": "cuda",
		"seed": 2026, "epochs": 4, "patience": 2, "batchSize": 2, "learningRate": 0.01,
		"weightDecay": 0, "minTrainInteractions": 4, "minActorInteractions": 3, "minProblemInteractions": 2,
		"validation":     map[string]any{"minActors": 2, "minInteractions": 2, "minRelativeLogLossImprovement": 0},
		"pathPolicy":     map[string]any{"targetMastery": 0.8, "maxKnowledgeTargets": 2, "minSteps": 2, "maxSteps": 4, "problemsPerStep": 1, "targetSuccessProbability": 0.7},
		"rankingWeights": map[string]any{"knowledgeGap": 1, "successDistance": 1},
	})
	baseTime := time.Date(2026, 7, 11, 1, 0, 0, 0, time.UTC)
	dataset := recommendation.TrainingDataset{
		Analytics: recommendation.AnalyticsProvenance{
			GenerationID: 1, HeadRevision: 1, InputManifest: manifest, InputManifestSHA256: manifestSHA256,
			AlgorithmVersion: "analytics_v2", ConfigurationSHA256: strings.Repeat("d", 64),
		},
		Configuration: recommendation.TrainingConfiguration{
			VersionID: 2, Key: "recommendation.training.default", VersionNumber: 1,
			SchemaID: "ascendany.training.recommendation.v2", Document: configuration, DocumentSHA256: configurationSHA256,
		},
		KnowledgeCatalog: recommendation.KnowledgeCatalogConfiguration{
			VersionID: 3, Key: "recommendation.knowledge.default", VersionNumber: 1,
			SchemaID: "ascendany.knowledge_catalog.recommendation.v1", Document: catalog, DocumentSHA256: catalogSHA256,
		},
		Students: []recommendation.TrainingStudent{
			{ActorID: 10, Rating: "1000", Metrics: canonicalObject(t, map[string]any{"solved": 3})},
			{ActorID: 20, Rating: "1200", Metrics: canonicalObject(t, map[string]any{"solved": 5})},
		},
		Problems: []recommendation.TrainingProblem{
			{SnapshotID: 101, ProblemSetID: "1001", ProblemSetProblemID: "2001", SourceURL: "https://pintia.cn/problem-sets/1001", Platform: "pintia", ProblemID: "501", Title: "Array Practice", ContentHTML: &problemOneHTML, MaxScore: &maxScore, TimeLimitMS: &timeLimit, MemoryLimitBytes: &memoryLimit},
			{SnapshotID: 101, ProblemSetID: "1001", ProblemSetProblemID: "2002", SourceURL: "https://pintia.cn/problem-sets/1001", Platform: "pintia", ProblemID: "502", Title: "Graph Practice", ContentHTML: &problemTwoHTML, MaxScore: &maxScore, TimeLimitMS: &timeLimit, MemoryLimitBytes: &memoryLimit},
			{SnapshotID: 102, ProblemSetID: "1002", ProblemSetProblemID: "3001", SourceURL: "https://pintia.cn/problem-sets/1002", Platform: "pintia", ProblemID: "501", Title: "Array Practice", ContentHTML: &problemOneHTML, MaxScore: &maxScore, TimeLimitMS: &timeLimit, MemoryLimitBytes: &memoryLimit},
		},
		Observations: []recommendation.TrainingObservation{
			trainerObservation(101, 10, "2001", baseTime),
			trainerObservation(101, 10, "2002", baseTime.Add(time.Minute)),
			trainerObservation(102, 10, "3001", baseTime.Add(2*time.Minute)),
			trainerObservation(101, 20, "2001", baseTime),
			trainerObservation(101, 20, "2002", baseTime.Add(time.Minute)),
			trainerObservation(102, 20, "3001", baseTime.Add(2*time.Minute)),
		},
	}
	built, err := recommendation.BuildInputBundle(dataset, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	store, err := artifact.NewStore(filepath.Join(t.TempDir(), "artifacts"), 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	publication, err := store.Publish(context.Background(), bytes.NewReader(built.CanonicalJSON))
	if err != nil {
		t.Fatal(err)
	}
	inputArtifact := publication.Artifact
	if err := publication.Release(); err != nil {
		t.Fatal(err)
	}
	startedAt := time.Now().UTC()
	claim := recommendation.Claim{
		TrainingRun: recommendation.TrainingRun{
			DatabaseID: 1, ID: testRunID, SourceAnalyticsGenerationID: 1, SourceAnalyticsHeadRevision: 1,
			InputArtifact: inputArtifact, TrainingConfigurationVersionID: 2, KnowledgeCatalogVersionID: 3,
			BundleProtocol: recommendation.TrainingBundleProtocolV2, InputManifest: built.Manifest,
			InputManifestSHA256: built.ManifestSHA256, Status: recommendation.RunRunning, AttemptCount: 1,
			CreatedAt: startedAt, StartedAt: &startedAt,
		},
		AttemptToken: testAttemptToken, LeaseOwner: "rtx-01", LeaseExpiresAt: startedAt.Add(time.Minute),
	}
	parsed, err := recommendation.ParseInputBundle(built.CanonicalJSON, 1<<20, claim.TrainingRun)
	if err != nil {
		t.Fatal(err)
	}
	return store, claim, built.CanonicalJSON, parsed
}

func trainerProblemFactSHA256(t *testing.T, problemID, title, contentHTML string, timeLimit, memoryLimit int64) string {
	t.Helper()
	_, digest := canonicalObjectWithDigest(t, map[string]any{
		"platform": "pintia", "problemId": problemID, "title": title, "contentHtml": contentHTML,
		"maxScore": json.Number("100"), "timeLimitMs": timeLimit, "memoryLimitBytes": memoryLimit,
	})
	return digest
}

func trainerObservation(snapshotID, actorID int64, problemSetProblemID string, submittedAt time.Time) recommendation.TrainingObservation {
	score, maxScore, passed, submissions := "50", "100", false, int64(1)
	return recommendation.TrainingObservation{
		SnapshotID: snapshotID, ActorID: actorID, ProblemSetProblemID: problemSetProblemID,
		Score: &score, MaxScore: &maxScore, Passed: &passed, ValidSubmissionCount: &submissions,
		SubmissionCount: submissions, FirstSubmittedAt: &submittedAt, LastSubmittedAt: &submittedAt,
	}
}

func trainingOutput(t *testing.T, input recommendation.ParsedInputBundle) json.RawMessage {
	t.Helper()
	actorMeans, actorScales := trainerNormalization(input.Actors, func(actor recommendation.TrainingActorInput) []float64 { return actor.Features })
	problemMeans, problemScales := trainerNormalization(input.Problems, func(problem recommendation.TrainingProblemInput) []float64 { return problem.Features })
	studentWeights := make([]any, len(input.KnowledgePoints))
	for index := range studentWeights {
		studentWeights[index] = make([]float64, len(input.FeatureSchema.ActorFeatureIDs))
	}
	actorResiduals := make([]any, len(input.Actors))
	for index, actor := range input.Actors {
		actorResiduals[index] = map[string]any{"actorId": actor.ActorID, "values": make([]float64, len(input.KnowledgePoints))}
	}
	rawDiscrimination := math.Log(math.Expm1(1 - 1e-6))
	problemParameters := make([]any, len(input.Problems))
	for index, problem := range input.Problems {
		problemParameters[index] = map[string]any{"problemKey": problem.ProblemKey, "difficultyResidual": 0, "rawDiscrimination": rawDiscrimination}
	}
	parameters := map[string]any{
		"normalization":         map[string]any{"actorMeans": actorMeans, "actorScales": actorScales, "problemMeans": problemMeans, "problemScales": problemScales},
		"studentFeatureWeights": studentWeights, "actorResiduals": actorResiduals,
		"problemFeatureWeights": make([]float64, len(input.FeatureSchema.ProblemFeatureIDs)), "problems": problemParameters,
	}
	_, parameterSHA256 := canonicalObjectWithDigest(t, parameters)
	var manifest struct {
		TrainingConfiguration struct {
			DocumentSHA256 string `json:"documentSha256"`
		} `json:"trainingConfiguration"`
		KnowledgeCatalog struct {
			DocumentSHA256 string `json:"documentSha256"`
		} `json:"knowledgeCatalog"`
		FeatureSchemaSHA256 string `json:"featureSchemaSha256"`
		SplitSHA256         string `json:"splitSha256"`
	}
	if err := json.Unmarshal(input.Manifest, &manifest); err != nil {
		t.Fatal(err)
	}
	var trainCount, validationCount int
	for _, interaction := range input.Interactions {
		if interaction.Split == "train" {
			trainCount++
		} else {
			validationCount++
		}
	}
	logLoss := math.Log(2)
	return canonicalObject(t, map[string]any{
		"protocol": recommendation.TrainingOutputProtocolV2, "inputManifestSha256": input.ManifestSHA256,
		"model": map[string]any{
			"schema": recommendation.ModelSchemaV2,
			"manifest": map[string]any{
				"algorithm": "knowledge_mirt_v1", "parameterSchema": "ascendany.recommendation.parameters.knowledge-mirt.v1",
				"parameterSha256": parameterSHA256, "inputManifestSha256": input.ManifestSHA256,
				"trainingConfigurationSha256": manifest.TrainingConfiguration.DocumentSHA256,
				"knowledgeCatalogSha256":      manifest.KnowledgeCatalog.DocumentSHA256,
				"featureSchemaSha256":         manifest.FeatureSchemaSHA256, "splitSha256": manifest.SplitSHA256,
				"knowledgePointCount": len(input.KnowledgePoints), "actorFeatureCount": len(input.FeatureSchema.ActorFeatureIDs),
				"problemFeatureCount": len(input.FeatureSchema.ProblemFeatureIDs), "actorCount": len(input.Actors),
				"problemCount": len(input.Problems), "trainInteractionCount": trainCount, "validationInteractionCount": validationCount,
				"runtimeConstructionSha256": strings.Repeat("a", 64),
				"runtimeProvenanceSha256":   strings.Repeat("b", 64),
				"runtimeTreeSha256":         strings.Repeat("c", 64),
				"hostCapabilitySha256":      strings.Repeat("d", 64),
				"runtimeAttestationSha256":  strings.Repeat("e", 64),
				"torchVersion":              "2.13.0+cu130", "accelerator": "cuda", "seed": input.TrainingConfiguration.Seed,
				"configuredEpochs": input.TrainingConfiguration.Epochs, "bestEpoch": 1,
				"actorFeatureIds": input.FeatureSchema.ActorFeatureIDs, "problemFeatureIds": input.FeatureSchema.ProblemFeatureIDs,
			},
			"parameters": parameters,
			"diagnostics": map[string]any{
				"epochsCompleted": 1, "bestEpoch": 1, "initialTrainLogLoss": logLoss, "finalTrainLogLoss": logLoss,
				"reportedBaselineValidationLogLoss": logLoss, "reportedValidationLogLoss": logLoss, "reportedValidationBrier": 0,
			},
		},
	})
}

func trainerNormalization[T any](rows []T, features func(T) []float64) ([]float64, []float64) {
	columns := len(features(rows[0]))
	means := make([]float64, columns)
	for _, row := range rows {
		for column, value := range features(row) {
			means[column] += value
		}
	}
	for column := range means {
		means[column] /= float64(len(rows))
	}
	scales := make([]float64, columns)
	for _, row := range rows {
		for column, value := range features(row) {
			difference := value - means[column]
			scales[column] += difference * difference
		}
	}
	for column := range scales {
		scales[column] = math.Sqrt(scales[column] / float64(len(rows)))
		if scales[column] == 0 {
			scales[column] = 1
		}
	}
	return means, scales
}

func canonicalObject(t *testing.T, value any) json.RawMessage {
	t.Helper()
	canonical, _ := canonicalObjectWithDigest(t, value)
	return canonical
}

func canonicalObjectWithDigest(t *testing.T, value any) (json.RawMessage, string) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	canonical, digest, err := canonicaljson.Object(raw, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	return canonical, digest
}

func wireDigest(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	_, digest, err := canonicaljson.Object(raw, 2<<20)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func testTrainerService(t *testing.T, repository recommendation.TrainerAgentRepository, store recommendation.ArtifactStore, uuid string) *Service {
	t.Helper()
	service, err := newService(repository, store, ServiceConfig{
		LeaseDuration: 30 * time.Second, RetryDelay: time.Second,
		MaximumInputBundleBytes: 1 << 20, MaximumOutputBundleBytes: 1 << 20,
	}, func() (string, error) { return uuid, nil })
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func assertAttempt(t *testing.T, attempt recommendation.TrainerAgentAttempt) {
	t.Helper()
	if attempt.RunID != testRunID || attempt.AgentID != "rtx-01" || attempt.AttemptToken != testAttemptToken {
		t.Fatalf("attempt = %#v", attempt)
	}
}

func unexpectedRequeue(t *testing.T) func(context.Context, recommendation.Claim, time.Duration, string) error {
	t.Helper()
	return func(context.Context, recommendation.Claim, time.Duration, string) error {
		t.Fatal("unexpected requeue")
		return errors.New("unexpected requeue")
	}
}

func unexpectedFail(t *testing.T) func(context.Context, recommendation.Claim, string, string) error {
	t.Helper()
	return func(context.Context, recommendation.Claim, string, string) error {
		t.Fatal("unexpected fail")
		return errors.New("unexpected fail")
	}
}
