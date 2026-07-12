package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/artifact"
	"github.com/kkkzbh/AscendAny/backend/internal/canonicaljson"
	"github.com/kkkzbh/AscendAny/backend/internal/health"
	"github.com/kkkzbh/AscendAny/backend/internal/recommendation"
)

type recommendationReaderStub struct {
	read func(context.Context, string) (recommendation.CurrentRecommendation, error)
}

func (stub recommendationReaderStub) ReadCurrent(ctx context.Context, token string) (recommendation.CurrentRecommendation, error) {
	return stub.read(ctx, token)
}

type recommendationQueueStub struct {
	queue func(context.Context, string, string, int64, int64) (recommendation.QueueResult, error)
}

type recommendationAdminReaderStub struct {
	review func(context.Context, string) (recommendation.ReviewContext, error)
	run    func(context.Context, string, string) (recommendation.TrainingRunDetail, bool, error)
	events func(context.Context, string, string, int64, int) (recommendation.TrainingEventPage, bool, error)
}

func (stub recommendationAdminReaderStub) ReadReviewContext(ctx context.Context, token string) (recommendation.ReviewContext, error) {
	return stub.review(ctx, token)
}

func (stub recommendationAdminReaderStub) ReadTrainingRun(ctx context.Context, token, runID string) (recommendation.TrainingRunDetail, bool, error) {
	return stub.run(ctx, token, runID)
}

func (stub recommendationAdminReaderStub) ReadTrainingEvents(ctx context.Context, token, runID string, after int64, limit int) (recommendation.TrainingEventPage, bool, error) {
	return stub.events(ctx, token, runID, after, limit)
}

func (stub recommendationQueueStub) QueueTraining(ctx context.Context, token, key string, generationID, headRevision int64) (recommendation.QueueResult, error) {
	return stub.queue(ctx, token, key, generationID, headRevision)
}

func TestGetSelfRecommendationExposesTypedFreshResultAndProvenance(t *testing.T) {
	want := testFreshRecommendation()
	handler := newRecommendationTestHandler(t, false, recommendationReaderStub{read: func(_ context.Context, token string) (recommendation.CurrentRecommendation, error) {
		if token != "student-access" {
			t.Fatalf("token = %q", token)
		}
		return want, nil
	}}, nil)
	request := recommendationRequest(http.MethodGet, "/api/v2/students/me/recommendation", "")
	request.Header.Set("Authorization", "Bearer student-access")
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", response.Code, response.Body.String())
	}
	var got recommendation.CurrentRecommendation
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.State != recommendation.RecommendationFresh || got.Model == nil || got.Result == nil ||
		got.Model.TrainingConfigurationKey != "recommendation.training.default" ||
		got.Model.KnowledgeCatalogKey != "recommendation.knowledge.default" ||
		got.Result.Status != recommendation.RecommendationResultReady ||
		got.Result.KnowledgeMastery[1].KnowledgePointID != "graphs" || got.Result.LearningPath[1].KnowledgePointID != "graphs" ||
		got.Result.SourceRating.String() != "1234.5" {
		t.Fatalf("response = %#v", got)
	}
}

func TestRecommendationReadRemainsAvailableWithWritesDisabled(t *testing.T) {
	handler := newRecommendationTestHandler(t, false, recommendationReaderStub{read: func(context.Context, string) (recommendation.CurrentRecommendation, error) {
		reason := "no_active_model"
		return recommendation.CurrentRecommendation{
			State: recommendation.RecommendationUnavailable, UnavailableReason: &reason,
			CurrentAnalyticsHeadRevision: 0, RecommendationHeadRevision: 0,
		}, nil
	}}, nil)
	get := recommendationRequest(http.MethodGet, "/api/v2/students/me/recommendation", "")
	get.Header.Set("Authorization", "Bearer student-access")
	getResponse := newTestResponseRecorder()
	handler.ServeHTTP(getResponse, get)
	if getResponse.Code != http.StatusOK || !strings.Contains(getResponse.Body.String(), `"state":"unavailable"`) {
		t.Fatalf("GET status = %d body = %s", getResponse.Code, getResponse.Body.String())
	}

	post := recommendationRequest(http.MethodPost, "/api/v2/admin/recommendation/training-runs", `{"trainingConfigurationKey":"recommendation.training.default"}`)
	post.Header.Set("Authorization", "Bearer admin-access")
	post.Header.Set("Content-Type", "application/json")
	postResponse := newTestResponseRecorder()
	handler.ServeHTTP(postResponse, post)
	if postResponse.Code != http.StatusNotFound || !strings.Contains(postResponse.Body.String(), `"code":"route_not_found"`) {
		t.Fatalf("POST status = %d body = %s", postResponse.Code, postResponse.Body.String())
	}
}

func TestQueueRecommendationTrainingUsesCanonicalRequestAndStatus(t *testing.T) {
	calls := 0
	queue := recommendationQueueStub{queue: func(_ context.Context, token, key string, generationID, headRevision int64) (recommendation.QueueResult, error) {
		calls++
		if token != "admin-access" || key != "recommendation.training.default" || generationID != 73 || headRevision != 9 {
			t.Fatalf("queue input = %q/%q/%d/%d", token, key, generationID, headRevision)
		}
		return testRecommendationQueueResult(calls == 1), nil
	}}
	handler := newRecommendationTestHandler(t, true, unusedRecommendationReader{}, queue)

	for index, wantStatus := range []int{http.StatusAccepted, http.StatusOK} {
		request := recommendationRequest(http.MethodPost, "/api/v2/admin/recommendation/training-runs", `{"trainingConfigurationKey":"recommendation.training.default","expectedAnalyticsGenerationId":"73","expectedAnalyticsHeadRevision":9}`)
		request.Header.Set("Authorization", "Bearer admin-access")
		request.Header.Set("Content-Type", "application/json")
		response := newTestResponseRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != wantStatus {
			t.Fatalf("request %d status = %d body = %s", index, response.Code, response.Body.String())
		}
		if !strings.Contains(response.Body.String(), `"trainingConfigurationKey":"recommendation.training.default"`) ||
			!strings.Contains(response.Body.String(), `"sourceAnalyticsGenerationId":"73"`) ||
			!strings.Contains(response.Body.String(), `"knowledgeCatalogVersionId":"52"`) {
			t.Fatalf("response body = %s", response.Body.String())
		}
	}
}

func TestRecommendationTransportRejectsInvalidContractsAndMapsDomainErrors(t *testing.T) {
	queueCalls := 0
	handler := newRecommendationTestHandler(t, true, recommendationReaderStub{read: func(context.Context, string) (recommendation.CurrentRecommendation, error) {
		return recommendation.CurrentRecommendation{}, &recommendation.Error{
			Code: recommendation.ErrorPrincipalRejected, Permanent: true, Op: "read", Cause: errors.New("rejected"),
		}
	}}, recommendationQueueStub{queue: func(context.Context, string, string, int64, int64) (recommendation.QueueResult, error) {
		queueCalls++
		return recommendation.QueueResult{}, nil
	}})

	read := recommendationRequest(http.MethodGet, "/api/v2/students/me/recommendation", "")
	read.Header.Set("Authorization", "Bearer student-access")
	readResponse := newTestResponseRecorder()
	handler.ServeHTTP(readResponse, read)
	if readResponse.Code != http.StatusForbidden || !strings.Contains(readResponse.Body.String(), `"code":"auth_forbidden"`) {
		t.Fatalf("read status = %d body = %s", readResponse.Code, readResponse.Body.String())
	}

	for _, body := range []string{
		`{"trainingConfigurationKey":"recommendation.training.default","extra":true}`,
		`{"trainingConfigurationKey":"recommendation.training.default"}{}`,
	} {
		request := recommendationRequest(http.MethodPost, "/api/v2/admin/recommendation/training-runs", body)
		request.Header.Set("Authorization", "Bearer admin-access")
		request.Header.Set("Content-Type", "application/json")
		response := newTestResponseRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("body %q status = %d response = %s", body, response.Code, response.Body.String())
		}
	}
	if queueCalls != 0 {
		t.Fatalf("invalid payload reached queue %d times", queueCalls)
	}
}

func TestRecommendationDependenciesMatchWriteCapability(t *testing.T) {
	ready := health.Report{Status: health.StatusReady}
	missingReader := testHandlerOptions(ready)
	missingReader.RecommendationReader = nil
	if _, err := New(missingReader); err == nil {
		t.Fatal("New accepted a missing recommendation reader")
	}

	disabledWithQueue := testHandlerOptions(ready)
	disabledWithQueue.Capabilities = testCapabilities(false)
	disabledWithQueue.Artifacts = nil
	disabledWithQueue.Imports = nil
	if _, err := New(disabledWithQueue); err == nil {
		t.Fatal("New accepted a recommendation queue while writes were disabled")
	}

	enabledWithoutQueue := testHandlerOptions(ready)
	enabledWithoutQueue.RecommendationQueue = nil
	if _, err := New(enabledWithoutQueue); err == nil {
		t.Fatal("New accepted enabled writes without a recommendation queue")
	}
}

func TestRecommendationReviewRunAndEventsAdminReads(t *testing.T) {
	runID := "123e4567-e89b-42d3-a456-426614174041"
	problemHash := strings.Repeat("a", 64)
	admin := recommendationAdminReaderStub{
		review: func(_ context.Context, token string) (recommendation.ReviewContext, error) {
			if token != "admin-access" {
				t.Fatalf("review token=%q", token)
			}
			return recommendation.ReviewContext{
				AnalyticsGenerationID: 73, AnalyticsHeadRevision: 9, InputManifestSHA256: strings.Repeat("b", 64),
				Problems: []recommendation.ReviewProblemCandidate{{
					ProblemKey: "pintia:501:" + problemHash, SourceProblemKey: "pintia:501", Platform: "pintia",
					ProblemID: "501", ProblemFactSHA256: problemHash, Title: "Problem A",
					SourceProblemSets: []recommendation.TrainingSourceProblemSet{{ProblemSetID: "1001", SourceURL: "https://pintia.cn/problem-sets/1001"}},
				}},
			}, nil
		},
		run: func(_ context.Context, token, gotRunID string) (recommendation.TrainingRunDetail, bool, error) {
			if token != "admin-access" || gotRunID != runID {
				t.Fatalf("run input=%q/%q", token, gotRunID)
			}
			run := testRecommendationQueueResult(true).Run
			run.DatabaseID = 1
			return recommendation.TrainingRunDetail{Run: run, TrainingConfigurationKey: "recommendation.training.default"}, true, nil
		},
		events: func(_ context.Context, token, gotRunID string, after int64, limit int) (recommendation.TrainingEventPage, bool, error) {
			if token != "admin-access" || gotRunID != runID || after != 1 || limit != 10 {
				t.Fatalf("events input=%q/%q/%d/%d", token, gotRunID, after, limit)
			}
			return recommendation.TrainingEventPage{RunID: runID, Items: []recommendation.TrainingEvent{{
				Sequence: 2, Type: "claimed", Payload: json.RawMessage(`{"attemptCount":1,"leaseOwner":"trainer-1"}`),
				CreatedAt: time.Date(2026, 7, 11, 2, 4, 0, 0, time.UTC),
			}}}, true, nil
		},
	}
	options := testHandlerOptions(health.Report{Status: health.StatusReady})
	options.RecommendationAdminReader = admin
	handler, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"/api/v2/admin/recommendation/review-context",
		"/api/v2/admin/recommendation/training-runs/" + runID,
		"/api/v2/admin/recommendation/training-runs/" + runID + "/events?afterSequence=1&limit=10",
	} {
		request := recommendationRequest(http.MethodGet, path, "")
		request.Header.Set("Authorization", "Bearer admin-access")
		response := newTestResponseRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
}

func TestRecommendationAdminReadRejectsInvalidResultWith500(t *testing.T) {
	options := testHandlerOptions(health.Report{Status: health.StatusReady})
	options.RecommendationAdminReader = recommendationAdminReaderStub{
		review: func(context.Context, string) (recommendation.ReviewContext, error) {
			return recommendation.ReviewContext{AnalyticsGenerationID: 1, AnalyticsHeadRevision: 1}, nil
		},
		run: func(context.Context, string, string) (recommendation.TrainingRunDetail, bool, error) {
			panic("unexpected")
		},
		events: func(context.Context, string, string, int64, int) (recommendation.TrainingEventPage, bool, error) {
			panic("unexpected")
		},
	}
	handler, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	request := recommendationRequest(http.MethodGet, "/api/v2/admin/recommendation/review-context", "")
	request.Header.Set("Authorization", "Bearer admin-access")
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError || !strings.Contains(response.Body.String(), `"code":"internal_error"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestParseRecommendationEventQueryRejectsNonCanonicalForms(t *testing.T) {
	if after, limit, err := parseRecommendationEventQuery("", false); err != nil || after != 0 || limit != 50 {
		t.Fatalf("defaults=%d/%d error=%v", after, limit, err)
	}
	if after, limit, err := parseRecommendationEventQuery("afterSequence=0&limit=100", false); err != nil || after != 0 || limit != 100 {
		t.Fatalf("canonical=%d/%d error=%v", after, limit, err)
	}
	for _, input := range []struct {
		raw   string
		force bool
	}{
		{"", true}, {"afterSequence=", false}, {"afterSequence=1&afterSequence=2", false},
		{"after%53equence=1", false}, {"afterSequence=+1", false}, {"unknown=1", false},
		{"afterSequence=-1", false}, {"afterSequence=01", false}, {"limit=0", false}, {"limit=101", false},
	} {
		if _, _, err := parseRecommendationEventQuery(input.raw, input.force); err == nil {
			t.Fatalf("query raw=%q force=%t accepted", input.raw, input.force)
		}
	}
}

func TestRecommendationQueueReturnsStructuredHeadConflictAndPreflight(t *testing.T) {
	cases := []struct {
		err        error
		status     int
		code       string
		detailNeed string
	}{
		{
			err: &recommendation.Error{Code: recommendation.ErrorStateConflict, Op: "queue", Cause: &recommendation.AnalyticsHeadConflict{
				ExpectedGenerationID: 73, ExpectedHeadRevision: 9, CurrentGenerationID: 74, CurrentHeadRevision: 10,
			}},
			status: http.StatusConflict, code: "recommendation_analytics_head_conflict", detailNeed: `"currentAnalyticsGenerationId":"74"`,
		},
		{
			err: &recommendation.Error{Code: recommendation.ErrorPreflightFailed, Permanent: true, Op: "preflight", Cause: &recommendation.PreflightFailure{
				IssueCode: "knowledge_catalog_assignment_missing", ProblemKeys: []string{"pintia:501:" + strings.Repeat("a", 64)},
			}},
			status: http.StatusUnprocessableEntity, code: "recommendation_preflight_failed", detailNeed: `"issueCode":"knowledge_catalog_assignment_missing"`,
		},
	}
	for _, testCase := range cases {
		handler := newRecommendationTestHandler(t, true, unusedRecommendationReader{}, recommendationQueueStub{queue: func(context.Context, string, string, int64, int64) (recommendation.QueueResult, error) {
			return recommendation.QueueResult{}, testCase.err
		}})
		request := recommendationRequest(http.MethodPost, "/api/v2/admin/recommendation/training-runs", `{"trainingConfigurationKey":"recommendation.training.default","expectedAnalyticsGenerationId":"73","expectedAnalyticsHeadRevision":9}`)
		request.Header.Set("Authorization", "Bearer admin-access")
		request.Header.Set("Content-Type", "application/json")
		response := newTestResponseRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != testCase.status || !strings.Contains(response.Body.String(), `"code":"`+testCase.code+`"`) || !strings.Contains(response.Body.String(), testCase.detailNeed) {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
	}
}

func newRecommendationTestHandler(
	t *testing.T,
	writes bool,
	reader RecommendationReader,
	queue RecommendationQueue,
) http.Handler {
	t.Helper()
	options := testHandlerOptions(health.Report{Status: health.StatusReady})
	options.RecommendationReader = reader
	options.RecommendationQueue = queue
	options.Capabilities = testCapabilities(writes)
	if !writes {
		options.Artifacts = nil
		options.Imports = nil
		options.ModelProbe = nil
	}
	handler, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func recommendationRequest(method, path, body string) *http.Request {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.RemoteAddr = "192.0.2.1:44000"
	request.Header.Set("Origin", "https://ascendany.example")
	return request
}

func testFreshRecommendation() recommendation.CurrentRecommendation {
	currentGenerationID := "73"
	return recommendation.CurrentRecommendation{
		State:                        recommendation.RecommendationFresh,
		CurrentAnalyticsGenerationID: &currentGenerationID,
		CurrentAnalyticsHeadRevision: 9,
		RecommendationHeadRevision:   4,
		Model: &recommendation.ModelProvenance{
			ModelID:               "123e4567-e89b-42d3-a456-426614174031",
			TrainingRunID:         "123e4567-e89b-42d3-a456-426614174032",
			AnalyticsGenerationID: "73", AnalyticsHeadRevision: 9,
			InputManifestSHA256:            strings.Repeat("a", 64),
			TrainingConfigurationVersionID: "41",
			TrainingConfigurationKey:       "recommendation.training.default",
			TrainingConfigurationVersion:   3,
			TrainingConfigurationSchema:    "ascendany.training.recommendation.v2",
			TrainingConfigurationSHA256:    strings.Repeat("b", 64),
			KnowledgeCatalogVersionID:      "52",
			KnowledgeCatalogKey:            "recommendation.knowledge.default",
			KnowledgeCatalogVersion:        2,
			KnowledgeCatalogSchema:         "ascendany.knowledge_catalog.recommendation.v1",
			KnowledgeCatalogSHA256:         strings.Repeat("f", 64),
			OutputArtifactSHA256:           strings.Repeat("c", 64),
			ModelSchema:                    recommendation.ModelSchemaV2,
			ModelManifest:                  json.RawMessage(`{"algorithm":"deterministic-rating-v1"}`),
			ModelManifestSHA256:            strings.Repeat("d", 64),
			Metrics:                        json.RawMessage(`{"studentCount":2}`),
			CreatedAt:                      time.Date(2026, 7, 11, 1, 2, 3, 0, time.UTC),
		},
		Result: testRecommendationResultV2(),
	}
}

func testRecommendationQueueResult(created bool) recommendation.QueueResult {
	return recommendation.QueueResult{Created: created, Run: recommendation.TrainingRun{
		ID:                          "123e4567-e89b-42d3-a456-426614174041",
		SourceAnalyticsGenerationID: 73, SourceAnalyticsHeadRevision: 9,
		InputArtifact:                  artifact.Artifact{Hash: strings.Repeat("a", 64), Size: 1024},
		TrainingConfigurationVersionID: 41,
		KnowledgeCatalogVersionID:      52,
		BundleProtocol:                 recommendation.TrainingBundleProtocolV2,
		InputManifestSHA256:            strings.Repeat("b", 64),
		Status:                         recommendation.RunQueued, AttemptCount: 0,
		CreatedAt: time.Date(2026, 7, 11, 2, 3, 4, 0, time.UTC),
	}}
}

func testRecommendationResultV2() *recommendation.StudentRecommendationResultV2 {
	mastery := []recommendation.RecommendationKnowledgeMasteryV2{
		{KnowledgePointID: "arrays", Label: "Arrays", Description: "Array fundamentals", PrerequisiteIDs: []string{}, Mastery: "0.45", TrainInteractionCount: 3},
		{KnowledgePointID: "graphs", Label: "Graphs", Description: "Graph traversal", PrerequisiteIDs: []string{"arrays"}, Mastery: "0.3", TrainInteractionCount: 3},
	}
	path := []recommendation.RecommendationLearningPathStepV2{
		{
			Order: 1, KnowledgePointID: "arrays", Label: "Arrays", Description: "Array fundamentals", PrerequisiteIDs: []string{},
			Mastery: "0.45", TargetMastery: "0.8", ReasonCode: "prerequisite",
			RecommendedProblems: []recommendation.RecommendationProblemV2{{
				ProblemKey: "pintia:501:" + strings.Repeat("1", 64), SourceProblemKey: "pintia:501", Platform: "pintia",
				ProblemID: "501", Title: "Array Practice", SourceProblemSets: []recommendation.TrainingSourceProblemSet{{ProblemSetID: "1001", SourceURL: "https://pintia.cn/problem-sets/1001"}},
				PredictedSuccessProbability: "0.65", RecommendationScore: "0.2",
				RankingEvidence: recommendation.RecommendationRankingEvidenceV2{KnowledgeGap: "0.35", SuccessDistance: "0.05", StepKnowledgeWeight: "1"},
			}},
		},
		{
			Order: 2, KnowledgePointID: "graphs", Label: "Graphs", Description: "Graph traversal", PrerequisiteIDs: []string{"arrays"},
			Mastery: "0.3", TargetMastery: "0.8", ReasonCode: "knowledge_gap",
			RecommendedProblems: []recommendation.RecommendationProblemV2{{
				ProblemKey: "pintia:502:" + strings.Repeat("2", 64), SourceProblemKey: "pintia:502", Platform: "pintia",
				ProblemID: "502", Title: "Graph Practice", SourceProblemSets: []recommendation.TrainingSourceProblemSet{{ProblemSetID: "1001", SourceURL: "https://pintia.cn/problem-sets/1001"}},
				PredictedSuccessProbability: "0.6", RecommendationScore: "0.3",
				RankingEvidence: recommendation.RecommendationRankingEvidenceV2{KnowledgeGap: "0.5", SuccessDistance: "0.1", StepKnowledgeWeight: "1"},
			}},
		},
	}
	evidence := recommendation.RecommendationEvidenceV2{TrainInteractionCount: 6, ValidationInteractionCount: 2, DistinctProblemCount: 4, PassedProblemCount: 1}
	body := map[string]any{"status": "ready", "sourceRating": json.Number("1234.5"), "evidence": evidence, "knowledgeMastery": mastery, "learningPath": path}
	raw, err := json.Marshal(body)
	if err != nil {
		panic(err)
	}
	_, digest, err := canonicaljson.Object(raw, 1<<20)
	if err != nil {
		panic(err)
	}
	return &recommendation.StudentRecommendationResultV2{
		Schema: recommendation.ResultSchemaV2, SHA256: digest, Status: recommendation.RecommendationResultReady,
		SourceRating: "1234.5", Evidence: evidence, KnowledgeMastery: mastery, LearningPath: path,
	}
}
