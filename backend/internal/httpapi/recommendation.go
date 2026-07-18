package httpapi

import (
	"context"
	"errors"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
	"github.com/kkkzbh/AscendAny/backend/internal/inferencemodel"
	"github.com/kkkzbh/AscendAny/backend/internal/pintia"
	"github.com/kkkzbh/AscendAny/backend/internal/recommendation"
)

var (
	recommendationUUIDv4Pattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	recommendationSHA256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
	recommendationKnowledgeID   = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,127}$`)
)

type RecommendationReader interface {
	ReadCurrent(context.Context, string) (recommendation.CurrentRecommendation, error)
}

type RecommendationAdminReader interface {
	ReadReviewContext(context.Context, string) (recommendation.ReviewContext, error)
}

type recommendationReviewProblemResponse struct {
	ProblemKey        string                                   `json:"problemKey"`
	SourceProblemKey  string                                   `json:"sourceProblemKey"`
	Platform          string                                   `json:"platform"`
	ProblemID         string                                   `json:"problemId"`
	ProblemFactSHA256 string                                   `json:"problemFactSha256"`
	Title             string                                   `json:"title"`
	SourceProblemSets []recommendation.RecommendationSourceSet `json:"sourceProblemSets"`
}

type recommendationReviewContextResponse struct {
	AnalyticsGenerationID string                                `json:"analyticsGenerationId"`
	AnalyticsHeadRevision int64                                 `json:"analyticsHeadRevision"`
	InputManifestSHA256   string                                `json:"inputManifestSha256"`
	Problems              []recommendationReviewProblemResponse `json:"problems"`
}

func (handler *Handler) getRecommendationReviewContext(writer http.ResponseWriter, request *http.Request) {
	if !requestBodyIsEmpty(request) {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "request_body_not_allowed", "Request body must be empty.")
		return
	}
	if !handler.requireNoQuery(writer, request) {
		return
	}
	access, ok := bearerToken(request)
	if !ok {
		handler.writeAPIError(writer, request, http.StatusUnauthorized, "auth_authentication_rejected", "Authentication was rejected.")
		return
	}
	result, err := handler.recommendationAdminReader.ReadReviewContext(request.Context(), access)
	if err != nil {
		handler.handleRecommendationError(writer, request, err)
		return
	}
	if !validRecommendationReviewContext(result) {
		handler.logRecommendationFailure(request, "invalid_review_context_result")
		handler.writeAPIError(writer, request, http.StatusInternalServerError, "internal_error", "Request could not be completed.")
		return
	}
	problems := make([]recommendationReviewProblemResponse, len(result.Problems))
	for index, problem := range result.Problems {
		problems[index] = recommendationReviewProblemResponse{
			ProblemKey: problem.ProblemKey, SourceProblemKey: problem.SourceProblemKey,
			Platform: problem.Platform, ProblemID: problem.ProblemID,
			ProblemFactSHA256: problem.ProblemFactSHA256, Title: problem.Title,
			SourceProblemSets: problem.SourceProblemSets,
		}
	}
	writeJSON(writer, http.StatusOK, recommendationReviewContextResponse{
		AnalyticsGenerationID: strconv.FormatInt(result.AnalyticsGenerationID, 10),
		AnalyticsHeadRevision: result.AnalyticsHeadRevision,
		InputManifestSHA256:   result.InputManifestSHA256,
		Problems:              problems,
	})
}

func (handler *Handler) getSelfRecommendation(writer http.ResponseWriter, request *http.Request) {
	if !requestBodyIsEmpty(request) {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "request_body_not_allowed", "Request body must be empty.")
		return
	}
	if !handler.requireNoQuery(writer, request) {
		return
	}
	access, ok := bearerToken(request)
	if !ok {
		handler.writeAPIError(writer, request, http.StatusUnauthorized, "auth_authentication_rejected", "Authentication was rejected.")
		return
	}
	result, err := handler.recommendationReader.ReadCurrent(request.Context(), access)
	if err != nil {
		handler.handleRecommendationError(writer, request, err)
		return
	}
	if !validCurrentRecommendation(result) {
		handler.logRecommendationFailure(request, "invalid_reader_result")
		handler.writeAPIError(writer, request, http.StatusInternalServerError, "internal_error", "Request could not be completed.")
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

func validRecommendationReviewContext(value recommendation.ReviewContext) bool {
	if value.AnalyticsGenerationID <= 0 || value.AnalyticsHeadRevision <= 0 ||
		!recommendationSHA256Pattern.MatchString(value.InputManifestSHA256) ||
		len(value.Problems) < 1 || len(value.Problems) > 10_000 {
		return false
	}
	seen := make(map[string]struct{}, len(value.Problems))
	for _, problem := range value.Problems {
		if !validReviewProblem(problem) {
			return false
		}
		if _, duplicate := seen[problem.ProblemKey]; duplicate {
			return false
		}
		seen[problem.ProblemKey] = struct{}{}
	}
	return true
}

func validReviewProblem(value recommendation.ReviewProblemCandidate) bool {
	return validText(value.ProblemKey, 400, false) && len(value.ProblemKey) >= 74 &&
		validText(value.SourceProblemKey, 300, false) && len(value.SourceProblemKey) >= 8 &&
		value.Platform == "pintia" && pintia.ValidID(value.ProblemID) &&
		recommendationSHA256Pattern.MatchString(value.ProblemFactSHA256) &&
		validText(value.Title, 4096, false) && validSourceSets(value.SourceProblemSets)
}

func validCurrentRecommendation(value recommendation.CurrentRecommendation) bool {
	if value.Model == nil || value.ModelHeadRevision <= 0 ||
		value.Model.ModelHeadRevision != value.ModelHeadRevision || !validRecommendationModel(*value.Model) {
		return false
	}
	if value.CurrentAnalyticsGenerationID != nil && !canonicalPositiveInt64(*value.CurrentAnalyticsGenerationID) {
		return false
	}
	switch value.State {
	case recommendation.RecommendationFresh:
		return value.UnavailableReason == nil && value.CurrentAnalyticsGenerationID != nil &&
			value.CurrentAnalyticsHeadRevision > 0 && value.Result != nil &&
			validRecommendationResult(*value.Result) &&
			validRecommendationKnowledgeActivity(value.KnowledgeActivity, value.Result.KnowledgeMastery)
	case recommendation.RecommendationUnavailable:
		return value.UnavailableReason != nil && validUnavailableReason(*value.UnavailableReason) &&
			value.CurrentAnalyticsHeadRevision >= 0 && value.Result == nil && len(value.KnowledgeActivity) == 0
	default:
		return false
	}
}

func validRecommendationKnowledgeActivity(
	activity []recommendation.RecommendationKnowledgeActivity,
	mastery []recommendation.RecommendationKnowledgeMastery,
) bool {
	if len(activity) != len(mastery) || len(activity) == 0 {
		return false
	}
	expected := make(map[string]struct{}, len(mastery))
	for _, item := range mastery {
		expected[item.KnowledgePointID] = struct{}{}
	}
	seen := make(map[string]struct{}, len(activity))
	for _, item := range activity {
		if _, exists := expected[item.KnowledgePointID]; !exists ||
			item.Attempted < 0 || item.Correct < 0 || item.Correct > item.Attempted {
			return false
		}
		if _, duplicate := seen[item.KnowledgePointID]; duplicate {
			return false
		}
		seen[item.KnowledgePointID] = struct{}{}
		if item.LastTriedAt != nil {
			_, offset := item.LastTriedAt.Zone()
			if item.LastTriedAt.IsZero() || offset != 0 {
				return false
			}
		}
		if len(item.RecentSeries) > 8 {
			return false
		}
		previousDate := ""
		for _, day := range item.RecentSeries {
			parsed, err := time.Parse(time.DateOnly, day.Date)
			if err != nil || parsed.Format(time.DateOnly) != day.Date || day.Date <= previousDate ||
				day.Attempted <= 0 || day.Correct < 0 || day.Correct > day.Attempted {
				return false
			}
			previousDate = day.Date
		}
	}
	return len(seen) == len(expected)
}

func validUnavailableReason(value recommendation.UnavailableReason) bool {
	switch value {
	case recommendation.UnavailableAnalytics, recommendation.UnavailableActorAnalytics,
		recommendation.UnavailableKnowledge, recommendation.UnavailableKnowledgeMatch,
		recommendation.UnavailableEligibleProblem:
		return true
	default:
		return false
	}
}

func validRecommendationModel(value recommendation.ModelProvenance) bool {
	if _, err := inferencemodel.ParsePurpose(value.Purpose); err != nil {
		return false
	}
	if !recommendationUUIDv4Pattern.MatchString(value.ModelID) ||
		!recommendationSHA256Pattern.MatchString(value.ArtifactSHA256) ||
		value.ArtifactSizeBytes < 1 || value.ArtifactSizeBytes > inferencemodel.MaximumArtifactBytes ||
		value.ArtifactMode != 0o644 || value.ModelSchema != inferencemodel.Schema ||
		value.Algorithm != inferencemodel.Algorithm || value.InferenceContract != inferencemodel.InferenceContract ||
		!canonicalUTCTime(value.TrainedAt) || value.ModelHeadRevision <= 0 {
		return false
	}
	for _, digest := range []string{
		value.TrainingProvenanceSHA256, value.FeatureSchemaSHA256, value.KnowledgeCatalogSHA256,
		value.ParameterSHA256, value.GoldenVectorsSHA256,
	} {
		if !recommendationSHA256Pattern.MatchString(digest) {
			return false
		}
	}
	return validText(value.ApplicationVersion, 128, false) &&
		validText(value.ApplicationCommit, 128, false) &&
		validText(value.ApplicationBuildTime, 128, false)
}

func validRecommendationResult(value recommendation.StudentRecommendationInferenceResult) bool {
	if value.Schema != recommendation.ResultSchemaV1 || !recommendationSHA256Pattern.MatchString(value.SHA256) ||
		!finiteBetween(value.SourceRating, 0, 1_000_000) || !validRecommendationEvidence(value.Evidence) ||
		len(value.KnowledgeMastery) < 1 || len(value.KnowledgeMastery) > 1024 ||
		!validMastery(value.KnowledgeMastery) {
		return false
	}
	switch value.Status {
	case recommendation.RecommendationResultReady:
		return value.Insufficiency == nil && validLearningPath(value.LearningPath)
	case recommendation.RecommendationResultInsufficient:
		return len(value.LearningPath) == 0 && value.Insufficiency != nil &&
			validRecommendationInsufficiency(*value.Insufficiency)
	default:
		return false
	}
}

func validRecommendationEvidence(value recommendation.RecommendationInferenceEvidence) bool {
	return value.ObservationCount >= 0 && value.DistinctProblemCount >= 0 &&
		value.PassedProblemCount >= 0 && value.PassedProblemCount <= value.DistinctProblemCount &&
		value.DistinctProblemCount <= value.ObservationCount
}

func validMastery(values []recommendation.RecommendationKnowledgeMastery) bool {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !recommendationKnowledgeID.MatchString(value.KnowledgePointID) ||
			!validText(value.Label, 256, false) || !validText(value.Description, 4096, true) ||
			!finiteBetween(value.Mastery, 0, 1) || value.ObservationCount < 0 ||
			!validKnowledgeIDs(value.PrerequisiteIDs, 1024) {
			return false
		}
		if _, duplicate := seen[value.KnowledgePointID]; duplicate {
			return false
		}
		seen[value.KnowledgePointID] = struct{}{}
	}
	return true
}

func validLearningPath(values []recommendation.RecommendationLearningPathStep) bool {
	if len(values) < 2 || len(values) > 8 {
		return false
	}
	seen := make(map[string]struct{}, len(values))
	for index, value := range values {
		if value.Order != int64(index+1) || !recommendationKnowledgeID.MatchString(value.KnowledgePointID) ||
			!validText(value.Label, 256, false) || !validText(value.Description, 4096, true) ||
			!validKnowledgeIDs(value.PrerequisiteIDs, 1024) || !finiteBetween(value.Mastery, 0, 1) ||
			!finite(value.TargetMastery) || value.TargetMastery <= 0 || value.TargetMastery >= 1 ||
			(value.ReasonCode != "knowledge_gap" && value.ReasonCode != "prerequisite") ||
			!validRecommendedProblems(value.RecommendedProblems) {
			return false
		}
		if _, duplicate := seen[value.KnowledgePointID]; duplicate {
			return false
		}
		seen[value.KnowledgePointID] = struct{}{}
	}
	return true
}

func validRecommendedProblems(values []recommendation.RecommendationProblem) bool {
	if len(values) < 1 || len(values) > 20 {
		return false
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !validText(value.ProblemKey, 512, false) || !validText(value.SourceProblemKey, 512, false) ||
			value.Platform != "pintia" || !pintia.ValidID(value.ProblemID) ||
			!validText(value.Title, 1024, false) || !validSourceSets(value.SourceProblemSets) ||
			!finiteBetween(value.PredictedSuccessProbability, 0, 1) || !finite(value.RecommendationScore) ||
			!finite(value.RankingEvidence.KnowledgeGap) || value.RankingEvidence.KnowledgeGap < 0 ||
			!finiteBetween(value.RankingEvidence.SuccessDistance, 0, 1) ||
			!finite(value.RankingEvidence.StepKnowledgeWeight) || value.RankingEvidence.StepKnowledgeWeight <= 0 ||
			value.RankingEvidence.StepKnowledgeWeight > 1 {
			return false
		}
		if _, duplicate := seen[value.ProblemKey]; duplicate {
			return false
		}
		seen[value.ProblemKey] = struct{}{}
	}
	return true
}

func validRecommendationInsufficiency(value recommendation.RecommendationInsufficiency) bool {
	switch value.ReasonCode {
	case "mastery_target_satisfied", "path_below_minimum", "path_exceeds_maximum", "problem_candidates_below_minimum":
	default:
		return false
	}
	return value.MinimumPathSteps >= 2 && value.MinimumPathSteps <= 8 &&
		value.CandidatePathSteps >= 0 && value.CandidatePathSteps <= 1024 &&
		value.ProblemsPerStep >= 1 && value.ProblemsPerStep <= 20 && value.EligibleProblemCount >= 0 &&
		validKnowledgeIDs(value.BlockedKnowledgePointIDs, 8)
}

func validSourceSets(values []recommendation.RecommendationSourceSet) bool {
	if len(values) < 1 || len(values) > 10_000 {
		return false
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !pintia.ValidID(value.ProblemSetID) || !canonicalPintiaSourceURL(value.SourceURL) {
			return false
		}
		key := value.ProblemSetID + "\x00" + value.SourceURL
		if _, duplicate := seen[key]; duplicate {
			return false
		}
		seen[key] = struct{}{}
	}
	return true
}

func validKnowledgeIDs(values []string, maximum int) bool {
	if len(values) > maximum {
		return false
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !recommendationKnowledgeID.MatchString(value) {
			return false
		}
		if _, duplicate := seen[value]; duplicate {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func canonicalPositiveInt64(value string) bool {
	parsed, err := strconv.ParseInt(value, 10, 64)
	return err == nil && parsed > 0 && strconv.FormatInt(parsed, 10) == value
}

func canonicalUTCTime(value string) bool {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	return err == nil && strings.HasSuffix(value, "Z") && parsed.Format(time.RFC3339Nano) == value
}

func canonicalPintiaSourceURL(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && len(value) <= 2048 && parsed.Scheme == "https" && parsed.Host == "pintia.cn" &&
		parsed.User == nil && parsed.Fragment == "" && parsed.String() == value
}

func validText(value string, maximumBytes int, emptyAllowed bool) bool {
	return len(value) <= maximumBytes && (emptyAllowed || value != "") && strings.TrimSpace(value) == value &&
		!strings.ContainsRune(value, 0)
}

func finiteBetween(value, minimum, maximum float64) bool {
	return finite(value) && value >= minimum && value <= maximum
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func (handler *Handler) handleRecommendationError(writer http.ResponseWriter, request *http.Request, err error) {
	if auth.ErrorCodeOf(err) != "" {
		handler.handleAuthError(writer, request, err)
		return
	}
	switch recommendation.CodeOf(err) {
	case recommendation.ErrorInvalidInput:
		handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_recommendation_request", "Recommendation request is invalid.")
		return
	case recommendation.ErrorPrincipalRejected:
		handler.writeAPIError(writer, request, http.StatusForbidden, "auth_forbidden", "Authorization was rejected.")
		return
	case recommendation.ErrorModelInactive:
		handler.writeAPIError(writer, request, http.StatusServiceUnavailable, "recommendation_model_inactive", "The recommendation model has not been activated.")
		return
	case recommendation.ErrorAnalyticsUnavailable:
		handler.writeAPIError(writer, request, http.StatusConflict, "recommendation_analytics_unavailable", "A current analytics generation is required.")
		return
	case recommendation.ErrorCanceled:
		if errors.Is(context.Cause(request.Context()), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
			handler.writeAPIError(writer, request, http.StatusRequestTimeout, "request_timeout", "Request exceeded its duration limit.")
			return
		}
		if errors.Is(err, context.Canceled) {
			handler.writeAPIError(writer, request, http.StatusBadRequest, "request_canceled", "Request was canceled.")
			return
		}
	}
	handler.logRecommendationFailure(request, recommendation.CodeOf(err))
	handler.writeAPIError(writer, request, http.StatusInternalServerError, "internal_error", "Request could not be completed.")
}

func (handler *Handler) logRecommendationFailure(request *http.Request, code any) {
	handler.logger.ErrorContext(request.Context(), "recommendation HTTP operation failed",
		"request_id", requestID(request.Context()),
		"code", code,
	)
}
