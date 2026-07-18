package httpapi

import (
	"context"
	"errors"
	"math"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/kkkzbh/AscendAny/backend/internal/achievement"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

var achievementCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

type AchievementService interface {
	GetSelf(context.Context, string) (achievement.Result, error)
	GetByStudentNumber(context.Context, string) (achievement.Result, error)
	GetByStudentIdentity(context.Context, string, string) (achievement.Result, error)
}

func (handler *Handler) getSelfAchievements(writer http.ResponseWriter, request *http.Request) {
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
	result, err := handler.achievement.GetSelf(request.Context(), access)
	if err != nil {
		handler.handleAchievementError(writer, request, err)
		return
	}
	if !validAchievementResult(result) {
		handler.logAchievementFailure(request, "invalid_service_result")
		handler.writeAPIError(writer, request, http.StatusInternalServerError, "internal_error", "Request could not be completed.")
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

func validAchievementResult(result achievement.Result) bool {
	if result.RuleSetVersion <= 0 || result.RuleHeadRevision <= 0 || result.AnalyticsHeadRevision < 0 || len(result.Items) == 0 {
		return false
	}
	switch result.State {
	case achievement.StateNotGenerated:
		if result.AnalyticsHeadRevision != 0 {
			return false
		}
	case achievement.StateNoObservations, achievement.StateReady:
		if result.AnalyticsHeadRevision <= 0 {
			return false
		}
	default:
		return false
	}

	counts := achievement.Summary{Total: len(result.Items)}
	seenCodes := make(map[string]struct{}, len(result.Items))
	seenOrders := make(map[int64]struct{}, len(result.Items))
	var previousOrder int64
	previousCode := ""
	for index, item := range result.Items {
		if !validAchievementItem(item) {
			return false
		}
		if _, exists := seenCodes[item.Code]; exists {
			return false
		}
		seenCodes[item.Code] = struct{}{}
		if _, exists := seenOrders[item.SortOrder]; exists {
			return false
		}
		seenOrders[item.SortOrder] = struct{}{}
		if index > 0 && (item.SortOrder < previousOrder || item.SortOrder == previousOrder && item.Code <= previousCode) {
			return false
		}
		previousOrder = item.SortOrder
		previousCode = item.Code
		switch item.Tier {
		case 0:
			counts.Locked++
		case 1:
			counts.Bronze++
		case 2:
			counts.Silver++
		case 3:
			counts.Gold++
		default:
			return false
		}
	}
	return counts == result.Summary
}

func validAchievementItem(item achievement.Item) bool {
	if !achievementCodePattern.MatchString(item.Code) ||
		!validAchievementText(item.Title, 256) ||
		!validAchievementText(item.Description, 2048) ||
		!validAchievementProgressKey(item.ProgressKey) || item.SortOrder <= 0 ||
		!finiteAchievementValue(item.Progress) ||
		!positiveAchievementTarget(item.BronzeTarget) ||
		!positiveAchievementTarget(item.SilverTarget) ||
		!positiveAchievementTarget(item.GoldTarget) ||
		item.BronzeTarget > item.SilverTarget || item.SilverTarget > item.GoldTarget {
		return false
	}
	wantTier := 0
	switch {
	case item.Progress >= item.GoldTarget:
		wantTier = 3
	case item.Progress >= item.SilverTarget:
		wantTier = 2
	case item.Progress >= item.BronzeTarget:
		wantTier = 1
	}
	return item.Tier == wantTier
}

func validAchievementText(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && utf8.ValidString(value) && strings.TrimSpace(value) == value
}

func validAchievementProgressKey(key achievement.ProgressKey) bool {
	switch key {
	case achievement.ProgressExamCount,
		achievement.ProgressPositiveDeltaCount,
		achievement.ProgressBestPositiveStreak,
		achievement.ProgressKnowledgeMax,
		achievement.ProgressAccuracyMax,
		achievement.ProgressQualityMax,
		achievement.ProgressFlexibilityMax,
		achievement.ProgressProficiencyMax,
		achievement.ProgressMaxRating,
		achievement.ProgressMaxRatingDelta,
		achievement.ProgressTop10Count,
		achievement.ProgressTop3Count,
		achievement.ProgressRank1Count,
		achievement.ProgressMaxExamMinMetric,
		achievement.ProgressCurrentMinMetric,
		achievement.ProgressAIDialogueCount:
		return true
	default:
		return false
	}
}

func finiteAchievementValue(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
}

func positiveAchievementTarget(value float64) bool {
	return finiteAchievementValue(value) && value > 0
}

func (handler *Handler) handleAchievementError(writer http.ResponseWriter, request *http.Request, err error) {
	if auth.ErrorCodeOf(err) != "" {
		handler.handleAuthError(writer, request, err)
		return
	}
	switch achievement.CodeOf(err) {
	case achievement.ErrorForbidden:
		handler.writeAPIError(writer, request, http.StatusForbidden, "auth_forbidden", "Authorization was rejected.")
		return
	case achievement.ErrorPrincipalRejected:
		handler.writeAPIError(writer, request, http.StatusUnauthorized, "auth_authentication_rejected", "Authentication was rejected.")
		return
	case achievement.ErrorStudentNotFound:
		handler.writeAPIError(writer, request, http.StatusNotFound, "student_not_found", "Student was not found.")
		return
	case achievement.ErrorCanceled:
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(context.Cause(request.Context()), context.DeadlineExceeded) {
			handler.writeAPIError(writer, request, http.StatusRequestTimeout, "request_timeout", "Request exceeded its duration limit.")
			return
		}
		if errors.Is(err, context.Canceled) {
			handler.writeAPIError(writer, request, http.StatusBadRequest, "request_canceled", "Request was canceled.")
			return
		}
	}
	handler.logAchievementFailure(request, string(achievement.CodeOf(err)))
	handler.writeAPIError(writer, request, http.StatusInternalServerError, "internal_error", "Request could not be completed.")
}

func (handler *Handler) logAchievementFailure(request *http.Request, code string) {
	handler.logger.ErrorContext(request.Context(), "achievement HTTP operation failed",
		"request_id", requestID(request.Context()),
		"code", code,
	)
}
