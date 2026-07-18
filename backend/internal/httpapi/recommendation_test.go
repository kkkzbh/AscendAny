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

	"github.com/kkkzbh/AscendAny/backend/internal/health"
	"github.com/kkkzbh/AscendAny/backend/internal/recommendation"
)

type recommendationReaderStub struct {
	read func(context.Context, string) (recommendation.CurrentRecommendation, error)
}

func (stub recommendationReaderStub) ReadCurrent(ctx context.Context, token string) (recommendation.CurrentRecommendation, error) {
	return stub.read(ctx, token)
}

type recommendationAdminReaderStub struct {
	read func(context.Context, string) (recommendation.ReviewContext, error)
}

func (stub recommendationAdminReaderStub) ReadReviewContext(ctx context.Context, token string) (recommendation.ReviewContext, error) {
	return stub.read(ctx, token)
}

func TestRecommendationHTTPReturnsFreshReadyAndInsufficientResults(t *testing.T) {
	t.Parallel()

	for _, status := range []recommendation.RecommendationResultStatus{
		recommendation.RecommendationResultReady,
		recommendation.RecommendationResultInsufficient,
	} {
		status := status
		t.Run(string(status), func(t *testing.T) {
			t.Parallel()
			value := testFreshRecommendation(status)
			handler := newRecommendationHandler(t, recommendationReaderStub{read: func(_ context.Context, token string) (recommendation.CurrentRecommendation, error) {
				if token != "student-access" {
					t.Fatalf("token = %q", token)
				}
				return value, nil
			}}, unusedRecommendationAdminReader{})

			response := newTestResponseRecorder()
			handler.ServeHTTP(response, recommendationRequest(http.MethodGet, "/api/v2/students/me/recommendation"))
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
			}
			var body recommendation.CurrentRecommendation
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.State != recommendation.RecommendationFresh || body.Result == nil || body.Result.Status != status ||
				body.Model == nil || body.Model.ModelHeadRevision != 4 {
				t.Fatalf("response = %#v", body)
			}
		})
	}
}

func TestRecommendationHTTPReturnsEveryClosedUnavailableReason(t *testing.T) {
	t.Parallel()

	reasons := []recommendation.UnavailableReason{
		recommendation.UnavailableAnalytics,
		recommendation.UnavailableActorAnalytics,
		recommendation.UnavailableKnowledge,
		recommendation.UnavailableKnowledgeMatch,
		recommendation.UnavailableEligibleProblem,
	}
	for _, reason := range reasons {
		reason := reason
		t.Run(string(reason), func(t *testing.T) {
			t.Parallel()
			model := testRecommendationModel()
			value := recommendation.CurrentRecommendation{
				State: recommendation.RecommendationUnavailable, UnavailableReason: &reason,
				CurrentAnalyticsHeadRevision: 0, ModelHeadRevision: 4, Model: &model,
			}
			handler := newRecommendationHandler(t, recommendationReaderStub{read: func(context.Context, string) (recommendation.CurrentRecommendation, error) {
				return value, nil
			}}, unusedRecommendationAdminReader{})
			response := newTestResponseRecorder()
			handler.ServeHTTP(response, recommendationRequest(http.MethodGet, "/api/v2/students/me/recommendation"))
			if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"unavailableReason":"`+string(reason)+`"`) {
				t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestRecommendationHTTPRejectsInvalidDomainOutputAndRemovedTrainingRoutes(t *testing.T) {
	t.Parallel()

	invalid := testFreshRecommendation(recommendation.RecommendationResultReady)
	invalid.Model.ArtifactMode = 0o600
	handler := newRecommendationHandler(t, recommendationReaderStub{read: func(context.Context, string) (recommendation.CurrentRecommendation, error) {
		return invalid, nil
	}}, unusedRecommendationAdminReader{})

	response := newTestResponseRecorder()
	handler.ServeHTTP(response, recommendationRequest(http.MethodGet, "/api/v2/students/me/recommendation"))
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("invalid output status = %d body=%s", response.Code, response.Body.String())
	}

	for _, request := range []*http.Request{
		recommendationRequest(http.MethodPost, "/api/v2/admin/recommendation/training-runs"),
		recommendationRequest(http.MethodGet, "/api/v2/admin/recommendation/training-runs/123e4567-e89b-42d3-a456-426614174041"),
		recommendationRequest(http.MethodPost, "/api/v2/internal/recommendation/trainer-agent/claims"),
	} {
		response := newTestResponseRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound || !strings.Contains(response.Body.String(), `"code":"route_not_found"`) {
			t.Fatalf("removed route %s status=%d body=%s", request.URL.Path, response.Code, response.Body.String())
		}
	}
}

func TestRecommendationHTTPRejectsInvalidKnowledgeActivityProjection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*recommendation.CurrentRecommendation)
	}{
		{name: "missing point", mutate: func(value *recommendation.CurrentRecommendation) {
			value.KnowledgeActivity = value.KnowledgeActivity[:1]
		}},
		{name: "duplicate point", mutate: func(value *recommendation.CurrentRecommendation) {
			value.KnowledgeActivity[1].KnowledgePointID = value.KnowledgeActivity[0].KnowledgePointID
		}},
		{name: "correct exceeds attempted", mutate: func(value *recommendation.CurrentRecommendation) {
			value.KnowledgeActivity[0].Correct = value.KnowledgeActivity[0].Attempted + 1
		}},
		{name: "non canonical recent date", mutate: func(value *recommendation.CurrentRecommendation) {
			value.KnowledgeActivity[0].RecentSeries[0].Date = "2026-7-18"
		}},
		{name: "non UTC last attempt", mutate: func(value *recommendation.CurrentRecommendation) {
			last := time.Date(2026, 7, 18, 16, 30, 0, 0, time.FixedZone("UTC+8", 8*60*60))
			value.KnowledgeActivity[0].LastTriedAt = &last
		}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			value := testFreshRecommendation(recommendation.RecommendationResultReady)
			test.mutate(&value)
			if validCurrentRecommendation(value) {
				t.Fatalf("invalid activity was accepted: %#v", value.KnowledgeActivity)
			}
		})
	}
}

func TestRecommendationReviewContextUsesCanonicalStringGenerationID(t *testing.T) {
	t.Parallel()

	admin := recommendationAdminReaderStub{read: func(_ context.Context, token string) (recommendation.ReviewContext, error) {
		if token != "student-access" {
			t.Fatalf("token = %q", token)
		}
		return recommendation.ReviewContext{
			AnalyticsGenerationID: 73, AnalyticsHeadRevision: 9,
			InputManifestSHA256: strings.Repeat("a", 64),
			Problems: []recommendation.ReviewProblemCandidate{{
				ProblemKey: strings.Repeat("p", 74), SourceProblemKey: "pintia:1",
				Platform: "pintia", ProblemID: "problem:1", ProblemFactSHA256: strings.Repeat("b", 64),
				Title: "Array Practice", SourceProblemSets: testRecommendationSourceSets(),
			}},
		}, nil
	}}
	handler := newRecommendationHandler(t, unusedRecommendationReader{}, admin)
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, recommendationRequest(http.MethodGet, "/api/v2/admin/recommendation/review-context"))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"analyticsGenerationId":"73"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestRecommendationHTTPMapsOwnedAndCanceledErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "principal", err: &recommendation.Error{Code: recommendation.ErrorPrincipalRejected, Permanent: true, Op: "read", Cause: errors.New("rejected")}, want: http.StatusForbidden},
		{name: "model inactive", err: &recommendation.Error{Code: recommendation.ErrorModelInactive, Permanent: true, Op: "read", Cause: errors.New("inactive")}, want: http.StatusServiceUnavailable},
		{name: "analytics", err: &recommendation.Error{Code: recommendation.ErrorAnalyticsUnavailable, Permanent: true, Op: "read", Cause: errors.New("missing")}, want: http.StatusConflict},
		{name: "canceled", err: &recommendation.Error{Code: recommendation.ErrorCanceled, Op: "read", Cause: context.Canceled}, want: http.StatusBadRequest},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			handler := newRecommendationHandler(t, recommendationReaderStub{read: func(context.Context, string) (recommendation.CurrentRecommendation, error) {
				return recommendation.CurrentRecommendation{}, test.err
			}}, unusedRecommendationAdminReader{})
			response := newTestResponseRecorder()
			handler.ServeHTTP(response, recommendationRequest(http.MethodGet, "/api/v2/students/me/recommendation"))
			if response.Code != test.want {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestRecommendationHTTPUsesAuthoritativePintiaIdentityContract(t *testing.T) {
	t.Parallel()

	problem := recommendation.ReviewProblemCandidate{
		ProblemKey: strings.Repeat("p", 74), SourceProblemKey: "pintia:9:problem:1",
		Platform: "pintia", ProblemID: "problem:1", ProblemFactSHA256: strings.Repeat("b", 64),
		Title: "Array Practice", SourceProblemSets: testRecommendationSourceSets(),
	}
	if !validReviewProblem(problem) {
		t.Fatal("authoritative Pintia identities were rejected")
	}
	for _, invalid := range []string{":problem", "problem/1", "problem 1", "题目1"} {
		candidate := problem
		candidate.ProblemID = invalid
		if validReviewProblem(candidate) {
			t.Fatalf("invalid Pintia problem ID %q was accepted", invalid)
		}
	}
}

func newRecommendationHandler(t *testing.T, reader RecommendationReader, admin RecommendationAdminReader) http.Handler {
	t.Helper()
	options := testHandlerOptions(health.Report{Status: health.StatusReady})
	options.RecommendationReader = reader
	options.RecommendationAdminReader = admin
	handler, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func recommendationRequest(method, path string) *http.Request {
	request := httptest.NewRequest(method, path, nil)
	request.Header.Set("Authorization", "Bearer student-access")
	request.RemoteAddr = "192.0.2.10:43210"
	return request
}

func testFreshRecommendation(status recommendation.RecommendationResultStatus) recommendation.CurrentRecommendation {
	generationID := "73"
	model := testRecommendationModel()
	result := testRecommendationResult(status)
	lastTriedAt := time.Date(2026, 7, 18, 8, 30, 0, 0, time.UTC)
	return recommendation.CurrentRecommendation{
		State: recommendation.RecommendationFresh, CurrentAnalyticsGenerationID: &generationID,
		CurrentAnalyticsHeadRevision: 9, ModelHeadRevision: 4, Model: &model, Result: &result,
		KnowledgeActivity: []recommendation.RecommendationKnowledgeActivity{
			{
				KnowledgePointID: "arrays", Attempted: 3, Correct: 2, LastTriedAt: &lastTriedAt,
				RecentSeries: []recommendation.RecommendationKnowledgeActivityDay{{Date: "2026-07-18", Attempted: 4, Correct: 2}},
			},
			{KnowledgePointID: "graphs", Attempted: 1, Correct: 0, RecentSeries: []recommendation.RecommendationKnowledgeActivityDay{}},
		},
	}
}

func testRecommendationModel() recommendation.ModelProvenance {
	return recommendation.ModelProvenance{
		ModelID: "123e4567-e89b-42d3-a456-426614174040", Purpose: "acceptance_test",
		ArtifactSHA256: strings.Repeat("1", 64), ArtifactSizeBytes: 4096, ArtifactMode: 0o644,
		ModelSchema: "ascendany.recommendation.inference-model.v1", Algorithm: "knowledge_mirt_feature_v1",
		InferenceContract: "ascendany.recommendation.inference.v1", TrainedAt: "2026-07-13T00:00:00Z",
		TrainingProvenanceSHA256: strings.Repeat("2", 64), FeatureSchemaSHA256: strings.Repeat("3", 64),
		KnowledgeCatalogSHA256: strings.Repeat("4", 64), ParameterSHA256: strings.Repeat("5", 64),
		GoldenVectorsSHA256: strings.Repeat("6", 64), ModelHeadRevision: 4,
		ApplicationVersion: "2.0.0", ApplicationCommit: strings.Repeat("a", 40),
		ApplicationBuildTime: "2026-07-13T00:05:00Z",
	}
}

func testRecommendationResult(status recommendation.RecommendationResultStatus) recommendation.StudentRecommendationInferenceResult {
	result := recommendation.StudentRecommendationInferenceResult{
		Schema: recommendation.ResultSchemaV1, SHA256: strings.Repeat("f", 64), Status: status,
		SourceRating: 1500, Evidence: recommendation.RecommendationInferenceEvidence{
			ObservationCount: 8, DistinctProblemCount: 4, PassedProblemCount: 2,
		},
		KnowledgeMastery: []recommendation.RecommendationKnowledgeMastery{
			{KnowledgePointID: "arrays", Label: "Arrays", Description: "", PrerequisiteIDs: []string{}, Mastery: 0.45, ObservationCount: 4},
			{KnowledgePointID: "graphs", Label: "Graphs", Description: "Graph traversal", PrerequisiteIDs: []string{"arrays"}, Mastery: 0.25, ObservationCount: 4},
		},
	}
	if status == recommendation.RecommendationResultInsufficient {
		result.Insufficiency = &recommendation.RecommendationInsufficiency{
			ReasonCode: "path_below_minimum", MinimumPathSteps: 2, CandidatePathSteps: 1,
			ProblemsPerStep: 2, EligibleProblemCount: 1, BlockedKnowledgePointIDs: []string{"graphs"},
		}
		return result
	}
	result.LearningPath = []recommendation.RecommendationLearningPathStep{
		testRecommendationStep(1, "arrays", "knowledge_gap"),
		testRecommendationStep(2, "graphs", "prerequisite"),
	}
	return result
}

func testRecommendationStep(order int64, knowledgeID, reason string) recommendation.RecommendationLearningPathStep {
	return recommendation.RecommendationLearningPathStep{
		Order: order, KnowledgePointID: knowledgeID, Label: strings.ToUpper(knowledgeID[:1]) + knowledgeID[1:],
		Description: "", PrerequisiteIDs: []string{}, Mastery: 0.25, TargetMastery: 0.75,
		ReasonCode: reason, RecommendedProblems: []recommendation.RecommendationProblem{{
			ProblemKey: "pintia:problem:" + knowledgeID, SourceProblemKey: "pintia:" + knowledgeID,
			Platform: "pintia", ProblemID: knowledgeID, Title: "Practice " + knowledgeID,
			SourceProblemSets: testRecommendationSourceSets(), PredictedSuccessProbability: 0.6,
			RecommendationScore: 0.7, RankingEvidence: recommendation.RecommendationRankingEvidence{
				KnowledgeGap: 0.5, SuccessDistance: 0.1, StepKnowledgeWeight: 1,
			},
		}},
	}
}

func testRecommendationSourceSets() []recommendation.RecommendationSourceSet {
	return []recommendation.RecommendationSourceSet{{
		ProblemSetID: "set:2039341868571590656",
		SourceURL:    "https://pintia.cn/problem-sets/set:2039341868571590656",
	}}
}
