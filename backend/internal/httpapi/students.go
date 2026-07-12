package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/analytics"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
	"github.com/kkkzbh/AscendAny/backend/internal/studentanalytics"
)

const defaultStudentAnalyticsHistoryLimit = 50
const defaultStudentLeaderboardLimit = 100

type studentAnalyticsUnavailableResponse struct {
	State        studentanalytics.State `json:"state"`
	HeadRevision int64                  `json:"headRevision"`
}

type studentAnalyticsReadyResponse struct {
	State         studentanalytics.State              `json:"state"`
	HeadRevision  int64                               `json:"headRevision"`
	ReferenceTime time.Time                           `json:"referenceTime"`
	Rating        int64                               `json:"rating"`
	Current       studentMetricValuesResponse         `json:"current"`
	ExamHistory   []studentExamHistoryPointResponse   `json:"examHistory"`
	RatingHistory []studentRatingHistoryPointResponse `json:"ratingHistory"`
}

type studentMetricValuesResponse struct {
	Knowledge   *float64 `json:"knowledge"`
	Accuracy    *float64 `json:"accuracy"`
	Quality     *float64 `json:"quality"`
	Flexibility *float64 `json:"flexibility"`
	Proficiency *float64 `json:"proficiency"`
}

type studentExamHistoryPointResponse struct {
	ExamID     string                      `json:"examId"`
	SnapshotID string                      `json:"snapshotId"`
	Title      string                      `json:"title"`
	EventTime  time.Time                   `json:"eventTime"`
	Values     studentMetricValuesResponse `json:"values"`
}

type studentRatingHistoryPointResponse struct {
	ExamID      string    `json:"examId"`
	SnapshotID  string    `json:"snapshotId"`
	Title       string    `json:"title"`
	EventTime   time.Time `json:"eventTime"`
	Rank        int64     `json:"rank"`
	OldRating   int64     `json:"oldRating"`
	Delta       int64     `json:"delta"`
	NewRating   int64     `json:"newRating"`
	Seed        float64   `json:"seed"`
	Performance float64   `json:"performance"`
}

type studentLeaderboardResponse struct {
	State        studentanalytics.State           `json:"state"`
	HeadRevision int64                            `json:"headRevision"`
	Population   int64                            `json:"population"`
	Items        []studentLeaderboardItemResponse `json:"items"`
}

type studentLeaderboardItemResponse struct {
	Rank          int64                       `json:"rank"`
	StudentNumber string                      `json:"studentNumber"`
	DisplayName   *string                     `json:"displayName"`
	Rating        int64                       `json:"rating"`
	Metrics       studentMetricValuesResponse `json:"metrics"`
}

func (handler *Handler) getSelfStudentAnalytics(writer http.ResponseWriter, request *http.Request) {
	if !requestBodyIsEmpty(request) {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "request_body_not_allowed", "Request body must be empty.")
		return
	}
	historyLimit, err := parseStudentAnalyticsHistoryLimit(request.URL.RawQuery, request.URL.ForceQuery)
	if err != nil {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_history_limit", fmt.Sprintf("limit must be one canonical decimal integer from 1 through %d.", studentanalytics.MaxHistoryLimit))
		return
	}
	access, ok := bearerToken(request)
	if !ok {
		handler.writeAPIError(writer, request, http.StatusUnauthorized, "auth_authentication_rejected", "Authentication was rejected.")
		return
	}
	result, err := handler.studentAnalytics.GetSelf(request.Context(), access, historyLimit)
	if err != nil {
		handler.handleStudentAnalyticsError(writer, request, err)
		return
	}
	payload, err := mapStudentAnalyticsResponse(result)
	if err != nil {
		handler.logStudentAnalyticsFailure(request, "invalid_service_result")
		handler.writeAPIError(writer, request, http.StatusInternalServerError, "internal_error", "Request could not be completed.")
		return
	}
	writeJSON(writer, http.StatusOK, payload)
}

func (handler *Handler) getStudentLeaderboard(writer http.ResponseWriter, request *http.Request) {
	if !requestBodyIsEmpty(request) {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "request_body_not_allowed", "Request body must be empty.")
		return
	}
	limit, err := parseStudentLeaderboardLimit(request.URL.RawQuery, request.URL.ForceQuery)
	if err != nil {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_limit", fmt.Sprintf("limit must be one canonical decimal integer from 1 through %d.", studentanalytics.MaxLeaderboardLimit))
		return
	}
	access, ok := bearerToken(request)
	if !ok {
		handler.writeAPIError(writer, request, http.StatusUnauthorized, "auth_authentication_rejected", "Authentication was rejected.")
		return
	}
	result, err := handler.studentAnalytics.GetLeaderboard(request.Context(), access, limit)
	if err != nil {
		handler.handleStudentAnalyticsError(writer, request, err)
		return
	}
	items := make([]studentLeaderboardItemResponse, len(result.Items))
	for index, item := range result.Items {
		items[index] = studentLeaderboardItemResponse{
			Rank:          item.Rank,
			StudentNumber: item.StudentNumber,
			DisplayName:   item.DisplayName,
			Rating:        item.Rating,
			Metrics:       mapStudentMetricValues(item.Metrics),
		}
	}
	writeJSON(writer, http.StatusOK, studentLeaderboardResponse{
		State:        result.State,
		HeadRevision: result.HeadRevision,
		Population:   result.Population,
		Items:        items,
	})
}

func parseStudentAnalyticsHistoryLimit(rawQuery string, forceQuery bool) (int, error) {
	if rawQuery == "" && !forceQuery {
		return defaultStudentAnalyticsHistoryLimit, nil
	}
	const prefix = "limit="
	if len(rawQuery) <= len(prefix) || rawQuery[:len(prefix)] != prefix {
		return 0, errors.New("query must contain only limit")
	}
	value := rawQuery[len(prefix):]
	if value[0] == '0' || len(value) > 3 {
		return 0, errors.New("limit is not canonical decimal")
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return 0, errors.New("limit is not decimal")
		}
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 1 || parsed > studentanalytics.MaxHistoryLimit {
		return 0, errors.New("limit is outside the supported range")
	}
	return parsed, nil
}

func parseStudentLeaderboardLimit(rawQuery string, forceQuery bool) (int, error) {
	if rawQuery == "" && !forceQuery {
		return defaultStudentLeaderboardLimit, nil
	}
	return parseCanonicalLimit(rawQuery, studentanalytics.MaxLeaderboardLimit)
}

func parseCanonicalLimit(rawQuery string, maximum int) (int, error) {
	const prefix = "limit="
	if len(rawQuery) <= len(prefix) || rawQuery[:len(prefix)] != prefix {
		return 0, errors.New("query must contain only limit")
	}
	value := rawQuery[len(prefix):]
	if value[0] == '0' || len(value) > len(strconv.Itoa(maximum)) {
		return 0, errors.New("limit is not canonical decimal")
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return 0, errors.New("limit is not decimal")
		}
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 1 || parsed > maximum {
		return 0, errors.New("limit is outside the supported range")
	}
	return parsed, nil
}

func mapStudentAnalyticsResponse(result studentanalytics.Result) (any, error) {
	switch result.State {
	case studentanalytics.StateNotGenerated, studentanalytics.StateNoObservations:
		return studentAnalyticsUnavailableResponse{
			State:        result.State,
			HeadRevision: result.HeadRevision,
		}, nil
	case studentanalytics.StateReady:
		if result.Ready == nil {
			return nil, errors.New("ready analytics payload is missing")
		}
		ready := result.Ready
		examHistory := make([]studentExamHistoryPointResponse, len(ready.ExamHistory))
		for index, point := range ready.ExamHistory {
			examHistory[index] = studentExamHistoryPointResponse{
				ExamID:     point.ExamID,
				SnapshotID: point.SnapshotID,
				Title:      point.Title,
				EventTime:  point.EventTime,
				Values:     mapStudentMetricValues(point.Values),
			}
		}
		ratingHistory := make([]studentRatingHistoryPointResponse, len(ready.RatingHistory))
		for index, point := range ready.RatingHistory {
			ratingHistory[index] = studentRatingHistoryPointResponse{
				ExamID:      point.ExamID,
				SnapshotID:  point.SnapshotID,
				Title:       point.Title,
				EventTime:   point.EventTime,
				Rank:        point.Rank,
				OldRating:   point.OldRating,
				Delta:       point.Delta,
				NewRating:   point.NewRating,
				Seed:        point.Seed,
				Performance: point.Performance,
			}
		}
		return studentAnalyticsReadyResponse{
			State:         result.State,
			HeadRevision:  result.HeadRevision,
			ReferenceTime: ready.ReferenceTime,
			Rating:        ready.Rating,
			Current:       mapStudentMetricValues(ready.Current),
			ExamHistory:   examHistory,
			RatingHistory: ratingHistory,
		}, nil
	default:
		return nil, fmt.Errorf("unknown analytics state %q", result.State)
	}
}

func mapStudentMetricValues(values analytics.MetricValues) studentMetricValuesResponse {
	return studentMetricValuesResponse{
		Knowledge:   values.Knowledge,
		Accuracy:    values.Accuracy,
		Quality:     values.Quality,
		Flexibility: values.Flexibility,
		Proficiency: values.Proficiency,
	}
}

func (handler *Handler) handleStudentAnalyticsError(writer http.ResponseWriter, request *http.Request, err error) {
	if auth.ErrorCodeOf(err) != "" {
		handler.handleAuthError(writer, request, err)
		return
	}
	code := studentanalytics.CodeOf(err)
	switch code {
	case studentanalytics.ErrorForbidden:
		handler.writeAPIError(writer, request, http.StatusForbidden, "auth_forbidden", "Authorization was rejected.")
		return
	case studentanalytics.ErrorPrincipalRejected:
		handler.writeAPIError(writer, request, http.StatusUnauthorized, "auth_authentication_rejected", "Authentication was rejected.")
		return
	case studentanalytics.ErrorCanceled:
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(context.Cause(request.Context()), context.DeadlineExceeded) {
			handler.writeAPIError(writer, request, http.StatusRequestTimeout, "request_timeout", "Request exceeded its duration limit.")
			return
		}
		if errors.Is(err, context.Canceled) {
			handler.writeAPIError(writer, request, http.StatusBadRequest, "request_canceled", "Request was canceled.")
			return
		}
	}
	handler.logStudentAnalyticsFailure(request, string(code))
	handler.writeAPIError(writer, request, http.StatusInternalServerError, "internal_error", "Request could not be completed.")
}

func (handler *Handler) logStudentAnalyticsFailure(request *http.Request, code string) {
	handler.logger.ErrorContext(request.Context(), "student analytics HTTP operation failed",
		"request_id", requestID(request.Context()),
		"code", code,
	)
}
