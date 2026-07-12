package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
	"github.com/kkkzbh/AscendAny/backend/internal/recommendation"
)

const maxRecommendationQueueJSONBytes int64 = 4 * 1024

var (
	recommendationUUIDv4Pattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	recommendationSHA256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

type RecommendationReader interface {
	ReadCurrent(context.Context, string) (recommendation.CurrentRecommendation, error)
}

type RecommendationQueue interface {
	QueueTraining(context.Context, string, string, int64, int64) (recommendation.QueueResult, error)
}

type RecommendationAdminReader interface {
	ReadReviewContext(context.Context, string) (recommendation.ReviewContext, error)
	ReadTrainingRun(context.Context, string, string) (recommendation.TrainingRunDetail, bool, error)
	ReadTrainingEvents(context.Context, string, string, int64, int) (recommendation.TrainingEventPage, bool, error)
}

type queueRecommendationTrainingRequest struct {
	TrainingConfigurationKey      string `json:"trainingConfigurationKey"`
	ExpectedAnalyticsGenerationID string `json:"expectedAnalyticsGenerationId"`
	ExpectedAnalyticsHeadRevision int64  `json:"expectedAnalyticsHeadRevision"`
}

type recommendationReviewProblemResponse struct {
	ProblemKey        string                                    `json:"problemKey"`
	SourceProblemKey  string                                    `json:"sourceProblemKey"`
	Platform          string                                    `json:"platform"`
	ProblemID         string                                    `json:"problemId"`
	ProblemFactSHA256 string                                    `json:"problemFactSha256"`
	Title             string                                    `json:"title"`
	SourceProblemSets []recommendation.TrainingSourceProblemSet `json:"sourceProblemSets"`
}

type recommendationReviewContextResponse struct {
	AnalyticsGenerationID string                                `json:"analyticsGenerationId"`
	AnalyticsHeadRevision int64                                 `json:"analyticsHeadRevision"`
	InputManifestSHA256   string                                `json:"inputManifestSha256"`
	Problems              []recommendationReviewProblemResponse `json:"problems"`
}

type recommendationTrainingRunResponse struct {
	ID                             string                   `json:"id"`
	SourceAnalyticsGenerationID    string                   `json:"sourceAnalyticsGenerationId"`
	SourceAnalyticsHeadRevision    int64                    `json:"sourceAnalyticsHeadRevision"`
	TrainingConfigurationVersionID string                   `json:"trainingConfigurationVersionId"`
	KnowledgeCatalogVersionID      string                   `json:"knowledgeCatalogVersionId"`
	TrainingConfigurationKey       string                   `json:"trainingConfigurationKey"`
	BundleProtocol                 string                   `json:"bundleProtocol"`
	InputManifestSHA256            string                   `json:"inputManifestSha256"`
	InputArtifactSHA256            string                   `json:"inputArtifactSha256"`
	InputArtifactSizeBytes         int64                    `json:"inputArtifactSizeBytes"`
	Status                         recommendation.RunStatus `json:"status"`
	AttemptCount                   int                      `json:"attemptCount"`
	CreatedAt                      time.Time                `json:"createdAt"`
	StartedAt                      *time.Time               `json:"startedAt"`
	FinishedAt                     *time.Time               `json:"finishedAt"`
}

type queueRecommendationTrainingResponse struct {
	Created     bool                              `json:"created"`
	TrainingRun recommendationTrainingRunResponse `json:"trainingRun"`
}

type recommendationTrainingFailureResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type recommendationTrainingRunDetailResponse struct {
	recommendationTrainingRunResponse
	Failure *recommendationTrainingFailureResponse `json:"failure"`
}

type recommendationTrainingEventResponse struct {
	Sequence  int64           `json:"sequence"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"createdAt"`
}

type recommendationTrainingEventPageResponse struct {
	RunID             string                                `json:"runId"`
	Items             []recommendationTrainingEventResponse `json:"items"`
	NextAfterSequence *int64                                `json:"nextAfterSequence"`
}

func (handler *Handler) getRecommendationReviewContext(writer http.ResponseWriter, request *http.Request) {
	if !requestBodyIsEmpty(request) || !handler.requireNoQuery(writer, request) {
		if !requestBodyIsEmpty(request) {
			handler.writeAPIError(writer, request, http.StatusBadRequest, "request_body_not_allowed", "Request body must be empty.")
		}
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
	if recommendation.ValidateReviewContext(result) != nil {
		handler.logRecommendationFailure(request, "invalid_review_context_result")
		handler.writeAPIError(writer, request, http.StatusInternalServerError, "internal_error", "Request could not be completed.")
		return
	}
	problems := make([]recommendationReviewProblemResponse, len(result.Problems))
	for index, problem := range result.Problems {
		problems[index] = recommendationReviewProblemResponse{
			ProblemKey: problem.ProblemKey, SourceProblemKey: problem.SourceProblemKey,
			Platform: problem.Platform, ProblemID: problem.ProblemID, ProblemFactSHA256: problem.ProblemFactSHA256,
			Title: problem.Title, SourceProblemSets: problem.SourceProblemSets,
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

func (handler *Handler) queueRecommendationTraining(writer http.ResponseWriter, request *http.Request) {
	if !handler.requireNoQuery(writer, request) {
		return
	}
	access, ok := bearerToken(request)
	if !ok {
		handler.writeAPIError(writer, request, http.StatusUnauthorized, "auth_authentication_rejected", "Authentication was rejected.")
		return
	}
	payload := queueRecommendationTrainingRequest{}
	if err := decodeStrictJSONWithLimit(
		writer,
		request,
		&payload,
		maxRecommendationQueueJSONBytes,
		"Recommendation training request exceeds 4096 bytes.",
		"Recommendation training request body exceeded its duration limit.",
	); err != nil {
		handler.handleRequestContractError(writer, request, err)
		return
	}
	expectedGenerationID, err := parseCanonicalPositiveInt64(payload.ExpectedAnalyticsGenerationID)
	if err != nil || payload.ExpectedAnalyticsHeadRevision <= 0 {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_recommendation_request", "Recommendation request is invalid.")
		return
	}
	result, err := handler.recommendationQueue.QueueTraining(
		request.Context(), access, payload.TrainingConfigurationKey, expectedGenerationID, payload.ExpectedAnalyticsHeadRevision,
	)
	if err != nil {
		handler.handleRecommendationError(writer, request, err)
		return
	}
	response, ok := mapRecommendationTrainingRun(result, payload.TrainingConfigurationKey)
	if !ok {
		handler.logRecommendationFailure(request, "invalid_queue_result")
		handler.writeAPIError(writer, request, http.StatusInternalServerError, "internal_error", "Request could not be completed.")
		return
	}
	status := http.StatusOK
	if result.Created {
		status = http.StatusAccepted
	}
	writeJSON(writer, status, queueRecommendationTrainingResponse{Created: result.Created, TrainingRun: response})
}

func (handler *Handler) getRecommendationTrainingRun(writer http.ResponseWriter, request *http.Request) {
	if !requestBodyIsEmpty(request) || !handler.requireNoQuery(writer, request) {
		if !requestBodyIsEmpty(request) {
			handler.writeAPIError(writer, request, http.StatusBadRequest, "request_body_not_allowed", "Request body must be empty.")
		}
		return
	}
	access, ok := bearerToken(request)
	if !ok {
		handler.writeAPIError(writer, request, http.StatusUnauthorized, "auth_authentication_rejected", "Authentication was rejected.")
		return
	}
	result, found, err := handler.recommendationAdminReader.ReadTrainingRun(request.Context(), access, request.PathValue("runId"))
	if err != nil {
		handler.handleRecommendationError(writer, request, err)
		return
	}
	if !found {
		handler.writeAPIError(writer, request, http.StatusNotFound, "recommendation_training_run_not_found", "Recommendation training run does not exist.")
		return
	}
	if recommendation.ValidateTrainingRunDetail(result, request.PathValue("runId")) != nil {
		handler.logRecommendationFailure(request, "invalid_training_run_result")
		handler.writeAPIError(writer, request, http.StatusInternalServerError, "internal_error", "Request could not be completed.")
		return
	}
	mapped, ok := mapRecommendationTrainingRun(recommendation.QueueResult{Run: result.Run}, result.TrainingConfigurationKey)
	if !ok {
		handler.logRecommendationFailure(request, "invalid_training_run_result")
		handler.writeAPIError(writer, request, http.StatusInternalServerError, "internal_error", "Request could not be completed.")
		return
	}
	response := recommendationTrainingRunDetailResponse{recommendationTrainingRunResponse: mapped}
	if result.Failure != nil {
		response.Failure = &recommendationTrainingFailureResponse{Code: result.Failure.Code, Message: result.Failure.Message}
	}
	writeJSON(writer, http.StatusOK, response)
}

func (handler *Handler) listRecommendationTrainingEvents(writer http.ResponseWriter, request *http.Request) {
	if !requestBodyIsEmpty(request) {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "request_body_not_allowed", "Request body must be empty.")
		return
	}
	access, ok := bearerToken(request)
	if !ok {
		handler.writeAPIError(writer, request, http.StatusUnauthorized, "auth_authentication_rejected", "Authentication was rejected.")
		return
	}
	after, limit, err := parseRecommendationEventQuery(request.URL.RawQuery, request.URL.ForceQuery)
	if err != nil {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_recommendation_request", "Recommendation request is invalid.")
		return
	}
	result, found, err := handler.recommendationAdminReader.ReadTrainingEvents(request.Context(), access, request.PathValue("runId"), after, limit)
	if err != nil {
		handler.handleRecommendationError(writer, request, err)
		return
	}
	if !found {
		handler.writeAPIError(writer, request, http.StatusNotFound, "recommendation_training_run_not_found", "Recommendation training run does not exist.")
		return
	}
	if recommendation.ValidateTrainingEventPage(result, request.PathValue("runId"), after, limit) != nil {
		handler.logRecommendationFailure(request, "invalid_training_event_result")
		handler.writeAPIError(writer, request, http.StatusInternalServerError, "internal_error", "Request could not be completed.")
		return
	}
	items := make([]recommendationTrainingEventResponse, len(result.Items))
	for index, event := range result.Items {
		items[index] = recommendationTrainingEventResponse{Sequence: event.Sequence, Type: event.Type, Payload: event.Payload, CreatedAt: event.CreatedAt}
	}
	writeJSON(writer, http.StatusOK, recommendationTrainingEventPageResponse{
		RunID: result.RunID, Items: items, NextAfterSequence: result.NextAfterSequence,
	})
}

func parseRecommendationEventQuery(rawQuery string, forceQuery bool) (int64, int, error) {
	after := int64(0)
	limit := 50
	if rawQuery == "" && !forceQuery {
		return after, limit, nil
	}
	fields, err := parseCanonicalQueryFields(rawQuery, forceQuery, map[string]struct{}{
		"afterSequence": {}, "limit": {},
	})
	if err != nil {
		return 0, 0, err
	}
	if value, present := fields["afterSequence"]; present {
		after, err = parseCanonicalNonNegativeDecimal64(value)
		if err != nil {
			return 0, 0, err
		}
	}
	if value, present := fields["limit"]; present {
		limit, err = parseCanonicalPositiveDecimal(value, 1, 100)
		if err != nil {
			return 0, 0, err
		}
	}
	return after, limit, nil
}

func parseCanonicalPositiveInt64(value string) (int64, error) {
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed <= 0 || strconv.FormatInt(parsed, 10) != value {
		return 0, errors.New("canonical positive int64 string is required")
	}
	return parsed, nil
}

func validCurrentRecommendation(value recommendation.CurrentRecommendation) bool {
	if value.CurrentAnalyticsHeadRevision < 0 || value.RecommendationHeadRevision < 0 {
		return false
	}
	if value.CurrentAnalyticsGenerationID != nil {
		if parsed, err := strconv.ParseInt(*value.CurrentAnalyticsGenerationID, 10, 64); err != nil || parsed <= 0 || strconv.FormatInt(parsed, 10) != *value.CurrentAnalyticsGenerationID {
			return false
		}
	}
	switch value.State {
	case recommendation.RecommendationFresh, recommendation.RecommendationStale:
		return value.UnavailableReason == nil && value.Model != nil && value.Result != nil &&
			validRecommendationResult(*value.Result) &&
			validRecommendationModel(*value.Model)
	case recommendation.RecommendationUnavailable:
		if value.UnavailableReason == nil || value.Result != nil {
			return false
		}
		switch *value.UnavailableReason {
		case "no_active_model":
			return value.Model == nil
		case "actor_not_in_active_model":
			return value.Model != nil && validRecommendationModel(*value.Model)
		default:
			return false
		}
	default:
		return false
	}
}

func validRecommendationResult(value recommendation.StudentRecommendationResultV2) bool {
	return recommendation.ValidateStudentRecommendationResultV2(value) == nil
}

func validRecommendationModel(value recommendation.ModelProvenance) bool {
	return recommendationUUIDv4Pattern.MatchString(value.ModelID) &&
		recommendationUUIDv4Pattern.MatchString(value.TrainingRunID) &&
		value.AnalyticsGenerationID != "" && value.AnalyticsHeadRevision > 0 &&
		recommendationSHA256Pattern.MatchString(value.InputManifestSHA256) &&
		value.TrainingConfigurationVersionID != "" && value.TrainingConfigurationVersion > 0 &&
		value.TrainingConfigurationKey != "" && value.TrainingConfigurationSchema != "" &&
		recommendationSHA256Pattern.MatchString(value.TrainingConfigurationSHA256) &&
		value.KnowledgeCatalogVersionID != "" && value.KnowledgeCatalogVersion > 0 &&
		value.KnowledgeCatalogKey != "" && value.KnowledgeCatalogSchema == "ascendany.knowledge_catalog.recommendation.v1" &&
		recommendationSHA256Pattern.MatchString(value.KnowledgeCatalogSHA256) &&
		recommendationSHA256Pattern.MatchString(value.OutputArtifactSHA256) &&
		value.ModelSchema == recommendation.ModelSchemaV2 && len(value.ModelManifest) > 0 &&
		recommendationSHA256Pattern.MatchString(value.ModelManifestSHA256) && len(value.Metrics) > 0 &&
		!value.CreatedAt.IsZero()
}

func mapRecommendationTrainingRun(
	result recommendation.QueueResult,
	configurationKey string,
) (recommendationTrainingRunResponse, bool) {
	run := result.Run
	if !recommendationUUIDv4Pattern.MatchString(run.ID) || run.SourceAnalyticsGenerationID <= 0 ||
		run.SourceAnalyticsHeadRevision <= 0 || run.TrainingConfigurationVersionID <= 0 ||
		run.KnowledgeCatalogVersionID <= 0 || run.BundleProtocol != recommendation.TrainingBundleProtocolV2 ||
		!recommendationSHA256Pattern.MatchString(run.InputManifestSHA256) ||
		!recommendationSHA256Pattern.MatchString(run.InputArtifact.Hash) || run.InputArtifact.Size <= 0 ||
		run.AttemptCount < 0 || run.CreatedAt.IsZero() || configurationKey == "" || !validRecommendationRunStatus(run.Status) {
		return recommendationTrainingRunResponse{}, false
	}
	return recommendationTrainingRunResponse{
		ID:                             run.ID,
		SourceAnalyticsGenerationID:    strconv.FormatInt(run.SourceAnalyticsGenerationID, 10),
		SourceAnalyticsHeadRevision:    run.SourceAnalyticsHeadRevision,
		TrainingConfigurationVersionID: strconv.FormatInt(run.TrainingConfigurationVersionID, 10),
		KnowledgeCatalogVersionID:      strconv.FormatInt(run.KnowledgeCatalogVersionID, 10),
		TrainingConfigurationKey:       configurationKey,
		BundleProtocol:                 run.BundleProtocol,
		InputManifestSHA256:            run.InputManifestSHA256,
		InputArtifactSHA256:            run.InputArtifact.Hash,
		InputArtifactSizeBytes:         run.InputArtifact.Size,
		Status:                         run.Status,
		AttemptCount:                   run.AttemptCount,
		CreatedAt:                      run.CreatedAt.UTC(),
		StartedAt:                      utcTimePointer(run.StartedAt),
		FinishedAt:                     utcTimePointer(run.FinishedAt),
	}, true
}

func validRecommendationRunStatus(status recommendation.RunStatus) bool {
	switch status {
	case recommendation.RunQueued, recommendation.RunRunning, recommendation.RunSucceeded,
		recommendation.RunSuperseded, recommendation.RunFailed:
		return true
	default:
		return false
	}
}

func utcTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	utc := value.UTC()
	return &utc
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
	case recommendation.ErrorAnalyticsUnavailable:
		handler.writeAPIError(writer, request, http.StatusConflict, "recommendation_analytics_unavailable", "A current analytics generation is required.")
		return
	case recommendation.ErrorTrainingConfigurationUnavailable:
		handler.writeAPIError(writer, request, http.StatusConflict, "recommendation_training_configuration_unavailable", "The selected training configuration is unavailable.")
		return
	case recommendation.ErrorStateConflict:
		var conflict *recommendation.AnalyticsHeadConflict
		if errors.As(err, &conflict) {
			handler.writeAPIErrorDetails(writer, request, http.StatusConflict, "recommendation_analytics_head_conflict", "Recommendation analytics head changed after review.", map[string]any{
				"expectedAnalyticsGenerationId": strconv.FormatInt(conflict.ExpectedGenerationID, 10),
				"expectedAnalyticsHeadRevision": conflict.ExpectedHeadRevision,
				"currentAnalyticsGenerationId":  strconv.FormatInt(conflict.CurrentGenerationID, 10),
				"currentAnalyticsHeadRevision":  conflict.CurrentHeadRevision,
			})
			return
		}
		handler.writeAPIError(writer, request, http.StatusConflict, "recommendation_state_conflict", "Recommendation source state changed concurrently.")
		return
	case recommendation.ErrorPreflightFailed:
		var failure *recommendation.PreflightFailure
		details := map[string]any{"issueCode": "recommendation_preflight_failed"}
		if errors.As(err, &failure) {
			details["issueCode"] = failure.IssueCode
			if len(failure.ProblemKeys) > 0 {
				details["problemKeys"] = failure.ProblemKeys
			}
		}
		handler.writeAPIErrorDetails(writer, request, http.StatusUnprocessableEntity, "recommendation_preflight_failed", "Recommendation training prerequisites require review.", details)
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
