package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kkkzbh/AscendAny/backend/internal/achievement"
	"github.com/kkkzbh/AscendAny/backend/internal/analytics"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
	"github.com/kkkzbh/AscendAny/backend/internal/feedback"
	"github.com/kkkzbh/AscendAny/backend/internal/importing"
	"github.com/kkkzbh/AscendAny/backend/internal/studentanalytics"
)

const (
	agentV1DataPollInterval           = 2 * time.Second
	agentV1DataHeartbeat              = 15 * time.Second
	maxAgentV1FeedbackJSONBytes int64 = feedback.MaxImages*feedback.MaxImageDataURLBytes + 128<<10
)

type agentV1MetricValues struct {
	Knowledge   float64 `json:"knowledge"`
	Accuracy    float64 `json:"accuracy"`
	Quality     float64 `json:"quality"`
	Flexibility float64 `json:"flexibility"`
	Proficiency float64 `json:"proficiency"`
}

type agentV1MetricMissingValues struct {
	Knowledge   bool `json:"knowledge"`
	Accuracy    bool `json:"accuracy"`
	Quality     bool `json:"quality"`
	Flexibility bool `json:"flexibility"`
	Proficiency bool `json:"proficiency"`
}

type agentV1StudentIdentity struct {
	StudentID           string  `json:"studentId"`
	PTANickname         *string `json:"ptaNickname"`
	NoSubmissionRecords bool    `json:"noSubmissionRecords"`
}

type agentV1RatingPoint struct {
	ExamID    string `json:"examId"`
	ExamName  string `json:"examName"`
	Date      string `json:"date"`
	OldRating int64  `json:"oldRating"`
	Delta     int64  `json:"delta"`
	NewRating int64  `json:"newRating"`
}

type agentV1Rating struct {
	Current   int64                `json:"current"`
	LastDelta *int64               `json:"lastDelta"`
	History   []agentV1RatingPoint `json:"history"`
}

type agentV1MetricDelta struct {
	LatestExamID   *string             `json:"latestExamId"`
	LatestExamName *string             `json:"latestExamName"`
	LatestExamDate *string             `json:"latestExamDate"`
	Baseline       string              `json:"baseline"`
	Values         agentV1MetricValues `json:"values"`
}

type agentV1ProgressExplanation struct {
	Available       bool     `json:"available"`
	LatestExamID    *string  `json:"latestExamId"`
	LatestExamName  *string  `json:"latestExamName"`
	LatestExamDate  *string  `json:"latestExamDate"`
	RatingDelta     *int64   `json:"ratingDelta"`
	KeyImprovements []string `json:"keyImprovements"`
	KeySetbacks     []string `json:"keySetbacks"`
	Summary         string   `json:"summary"`
}

type agentV1MilestoneStreak struct {
	Available             bool                   `json:"available"`
	CurrentPositiveStreak int64                  `json:"currentPositiveStreak"`
	BestPositiveStreak    int64                  `json:"bestPositiveStreak"`
	NewMilestones         []agentV1MilestoneItem `json:"newMilestones"`
	RecentMilestones      []agentV1MilestoneItem `json:"recentMilestones"`
	NextTargets           []string               `json:"nextTargets"`
}

type agentV1MilestoneItem struct {
	Code     string  `json:"code"`
	Label    string  `json:"label"`
	Detail   string  `json:"detail"`
	ExamID   *string `json:"examId"`
	ExamDate *string `json:"examDate"`
}

type agentV1PeerMetricGap struct {
	Score       *float64 `json:"score"`
	Solved      *float64 `json:"solved"`
	Knowledge   *float64 `json:"knowledge"`
	Accuracy    *float64 `json:"accuracy"`
	Quality     *float64 `json:"quality"`
	Flexibility *float64 `json:"flexibility"`
	Proficiency *float64 `json:"proficiency"`
}

type agentV1PeerComparison struct {
	Available      bool   `json:"available"`
	DefaultMode    string `json:"defaultMode"`
	PercentileBand struct {
		TotalParticipants int64                `json:"totalParticipants"`
		MyRank            *int64               `json:"myRank"`
		MyPercentile      *float64             `json:"myPercentile"`
		BandCode          *string              `json:"bandCode"`
		BandLabel         string               `json:"bandLabel"`
		GapVsBandMedian   agentV1PeerMetricGap `json:"gapVsBandMedian"`
	} `json:"percentileBand"`
	PreviousRanker struct {
		Available           bool                 `json:"available"`
		RankGap             *int64               `json:"rankGap"`
		ScoreGap            *float64             `json:"scoreGap"`
		SolvedGap           *float64             `json:"solvedGap"`
		MetricGapVsPrevious agentV1PeerMetricGap `json:"metricGapVsPrevious"`
	} `json:"previousRanker"`
}

type agentV1PostExamSupport struct {
	Available       bool     `json:"available"`
	Mode            string   `json:"mode"`
	Headline        string   `json:"headline"`
	Message         string   `json:"message"`
	ActionPlan      []string `json:"actionPlan"`
	CheckInQuestion string   `json:"checkInQuestion"`
}

type agentV1StudentDashboardResponse struct {
	Metrics             agentV1MetricValues        `json:"metrics"`
	MetricMissing       agentV1MetricMissingValues `json:"metricMissing"`
	Rating              agentV1Rating              `json:"rating"`
	MetricDelta         agentV1MetricDelta         `json:"metricDelta"`
	Identity            agentV1StudentIdentity     `json:"identity"`
	ProgressExplanation agentV1ProgressExplanation `json:"progressExplanation"`
	MilestoneStreak     agentV1MilestoneStreak     `json:"milestoneStreak"`
	PeerComparison      agentV1PeerComparison      `json:"peerComparison"`
	PostExamSupport     agentV1PostExamSupport     `json:"postExamSupport"`
}

type agentV1AchievementsResponse struct {
	Identity agentV1StudentIdentity   `json:"identity"`
	Summary  achievement.Summary      `json:"summary"`
	Items    []agentV1AchievementItem `json:"items"`
}

type agentV1AchievementItem struct {
	Code         string  `json:"code"`
	Title        string  `json:"title"`
	Description  string  `json:"description"`
	Tier         int     `json:"tier"`
	Progress     float64 `json:"progress"`
	BronzeTarget float64 `json:"bronzeTarget"`
	SilverTarget float64 `json:"silverTarget"`
	GoldTarget   float64 `json:"goldTarget"`
	SortOrder    int64   `json:"sortOrder"`
}

type agentV1LeaderboardResponse struct {
	Items []agentV1LeaderboardItem `json:"items"`
}

type agentV1LeaderboardItem struct {
	StudentID   string  `json:"studentId"`
	Grade       string  `json:"grade"`
	Username    string  `json:"username"`
	Rating      int64   `json:"rating"`
	Knowledge   float64 `json:"knowledge"`
	Accuracy    float64 `json:"accuracy"`
	Quality     float64 `json:"quality"`
	Flexibility float64 `json:"flexibility"`
	Proficiency float64 `json:"proficiency"`
}

type agentV1LatestExamResponse struct {
	LatestExamImportedAt *string `json:"latestExamImportedAt"`
}

type agentV1FeedbackImage struct {
	Name    string `json:"name"`
	DataURL string `json:"dataUrl"`
}

type agentV1FeedbackRequest struct {
	Title      string                  `json:"title"`
	Content    string                  `json:"content"`
	Images     *[]agentV1FeedbackImage `json:"images"`
	Platform   *string                 `json:"platform,omitempty"`
	AppVersion *string                 `json:"appVersion,omitempty"`
	UserAgent  *string                 `json:"userAgent,omitempty"`
}

type agentV1FeedbackResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

func (handler *Handler) agentV1StudentDashboard(writer http.ResponseWriter, request *http.Request) {
	if !requestBodyIsEmpty(request) {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "request_body_not_allowed", "Request body must be empty.")
		return
	}
	access, account, ok := handler.agentV1SelfRequest(writer, request)
	if !ok {
		return
	}
	result, err := handler.studentAnalytics.GetSelf(request.Context(), access, studentanalytics.MaxHistoryLimit)
	if err != nil {
		handler.handleStudentAnalyticsError(writer, request, err)
		return
	}
	response, err := mapAgentV1Dashboard(account, result)
	if err != nil {
		handler.logAgentV1Failure(request, "project_student_dashboard", err)
		handler.writeAPIError(writer, request, http.StatusInternalServerError, "internal_error", "Request could not be completed.")
		return
	}
	writeJSON(writer, http.StatusOK, response)
}

func (handler *Handler) agentV1StudentAchievements(writer http.ResponseWriter, request *http.Request) {
	if !requestBodyIsEmpty(request) {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "request_body_not_allowed", "Request body must be empty.")
		return
	}
	_, authorizationPresent, authorizationValid := singleHeader(request.Header, "Authorization")
	if !authorizationValid {
		handler.writeAPIError(writer, request, http.StatusUnauthorized, "auth_authentication_rejected", "Authentication was rejected.")
		return
	}

	var result achievement.Result
	var identity agentV1StudentIdentity
	var err error
	if authorizationPresent {
		access, account, ok := handler.agentV1SelfRequest(writer, request)
		if !ok {
			return
		}
		result, err = handler.achievement.GetSelf(request.Context(), access)
		identity = agentV1Identity(account, false)
	} else {
		selectors, selectorErr := parseAgentV1SelfSelectors(request.URL.RawQuery, request.URL.ForceQuery)
		if selectorErr != nil {
			handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_student_selector", "Student selector query is invalid.")
			return
		}
		if selectors.studentID == nil {
			if selectors.ptaNickname != nil {
				handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_student_selector", "Student selector query is invalid.")
				return
			}
			handler.writeAPIError(writer, request, http.StatusUnauthorized, "auth_authentication_rejected", "Authentication was rejected.")
			return
		}
		if selectors.ptaNickname == nil {
			result, err = handler.achievement.GetByStudentNumber(request.Context(), *selectors.studentID)
		} else {
			result, err = handler.achievement.GetByStudentIdentity(request.Context(), *selectors.studentID, *selectors.ptaNickname)
		}
		identity = agentV1StudentIdentity{StudentID: *selectors.studentID, PTANickname: selectors.ptaNickname}
	}
	if err != nil {
		handler.handleAchievementError(writer, request, err)
		return
	}
	if !validAchievementResult(result) {
		handler.logAgentV1Failure(request, "project_student_achievements", errors.New("achievement service returned an invalid result"))
		handler.writeAPIError(writer, request, http.StatusInternalServerError, "internal_error", "Request could not be completed.")
		return
	}
	items := make([]agentV1AchievementItem, len(result.Items))
	for index, item := range result.Items {
		items[index] = agentV1AchievementItem{
			Code: item.Code, Title: item.Title, Description: item.Description,
			Tier: item.Tier, Progress: item.Progress, BronzeTarget: item.BronzeTarget,
			SilverTarget: item.SilverTarget, GoldTarget: item.GoldTarget, SortOrder: item.SortOrder,
		}
	}
	identity.NoSubmissionRecords = result.State != achievement.StateReady
	writeJSON(writer, http.StatusOK, agentV1AchievementsResponse{
		Identity: identity,
		Summary:  result.Summary,
		Items:    items,
	})
}

func (handler *Handler) agentV1StudentLeaderboard(writer http.ResponseWriter, request *http.Request) {
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
	result, err := handler.studentAnalytics.GetLeaderboard(request.Context(), access, studentanalytics.MaxLeaderboardLimit)
	if err != nil {
		handler.handleStudentAnalyticsError(writer, request, err)
		return
	}
	items := make([]agentV1LeaderboardItem, len(result.Items))
	for index, item := range result.Items {
		username := item.StudentNumber
		if item.DisplayName != nil {
			username = *item.DisplayName
		}
		grade := ""
		if len(item.StudentNumber) >= 4 {
			grade = item.StudentNumber[:4]
		}
		metrics, _ := agentV1Metrics(item.Metrics)
		items[index] = agentV1LeaderboardItem{
			StudentID: item.StudentNumber, Grade: grade, Username: username, Rating: item.Rating,
			Knowledge: metrics.Knowledge, Accuracy: metrics.Accuracy, Quality: metrics.Quality,
			Flexibility: metrics.Flexibility, Proficiency: metrics.Proficiency,
		}
	}
	writeJSON(writer, http.StatusOK, agentV1LeaderboardResponse{Items: items})
}

func (handler *Handler) agentV1LatestExamImportedAt(writer http.ResponseWriter, request *http.Request) {
	if !requestBodyIsEmpty(request) {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "request_body_not_allowed", "Request body must be empty.")
		return
	}
	if !handler.requireNoQuery(writer, request) {
		return
	}
	latest, err := handler.latestAgentV1ExamImportedAt(request.Context())
	if err != nil {
		handler.handleImportError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, agentV1LatestExamResponse{LatestExamImportedAt: latest})
}

func (handler *Handler) agentV1DataEvents(writer http.ResponseWriter, request *http.Request) {
	if !requestBodyIsEmpty(request) {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "request_body_not_allowed", "Request body must be empty.")
		return
	}
	if !handler.requireNoQuery(writer, request) {
		return
	}
	if !handler.acquireSSE(writer, request) {
		return
	}
	defer handler.releaseSSE()
	streamDeadline := time.Now().Add(handler.sseMaxDuration)
	streamContext, cancelStream := context.WithDeadline(request.Context(), streamDeadline)
	defer cancelStream()
	request = request.WithContext(streamContext)
	latest, err := handler.latestAgentV1ExamImportedAt(request.Context())
	if err != nil {
		handler.handleImportError(writer, request, err)
		return
	}

	controller := http.NewResponseController(writer)
	if err := clearSSEWriteDeadline(controller); err != nil {
		handler.writeAPIError(writer, request, http.StatusInternalServerError, "internal_error", "Request could not be completed.")
		return
	}
	stopWriteInterrupt := installSSEWriteInterrupt(request.Context(), controller)
	defer stopWriteInterrupt()
	if err := handler.setSSEWriteDeadline(controller, streamDeadline); err != nil {
		handler.handleSSESetupError(writer, request, err)
		return
	}
	header := writer.Header()
	header.Set("Content-Type", "text/event-stream; charset=utf-8")
	header.Set("Cache-Control", "no-store")
	header.Set("X-Accel-Buffering", "no")
	header.Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(http.StatusOK)
	if err := writeAgentV1DataEvent(writer, "snapshot", map[string]any{
		"type": "snapshot", "latestExamImportedAt": latest,
	}); err != nil {
		return
	}
	if err := controller.Flush(); err != nil {
		return
	}
	if err := clearSSEWriteDeadline(controller); err != nil {
		return
	}

	poll := time.NewTicker(agentV1DataPollInterval)
	defer poll.Stop()
	heartbeat := time.NewTicker(agentV1DataHeartbeat)
	defer heartbeat.Stop()
	for {
		select {
		case <-request.Context().Done():
			return
		case <-poll.C:
			current, readErr := handler.latestAgentV1ExamImportedAt(request.Context())
			if readErr != nil {
				_ = handler.writeAgentV1StreamEvent(controller, writer, streamDeadline, "error", map[string]any{
					"type": "error", "code": "data_freshness_read_failed", "message": "数据更新流读取失败。",
				})
				return
			}
			if equalAgentV1Timestamp(latest, current) {
				continue
			}
			latest = current
			if err := handler.writeAgentV1StreamEvent(controller, writer, streamDeadline, "data_changed", map[string]any{
				"type": "data_changed", "latestExamImportedAt": latest,
			}); err != nil {
				return
			}
		case now := <-heartbeat.C:
			if err := handler.writeAgentV1StreamEvent(controller, writer, streamDeadline, "heartbeat", map[string]any{
				"type": "heartbeat", "ts": now.UTC().Format(time.RFC3339Nano),
			}); err != nil {
				return
			}
		}
	}
}

func (handler *Handler) latestAgentV1ExamImportedAt(ctx context.Context) (*string, error) {
	var cursor *string
	var latest time.Time
	for {
		page, err := handler.importReader.ListJobs(ctx, cursor, importing.MaxJobPageSize)
		if err != nil {
			return nil, err
		}
		for _, job := range page.Items {
			if job.Status == importing.JobSucceeded && job.UpdatedAt.After(latest) {
				latest = job.UpdatedAt
			}
		}
		if page.NextCursor == nil {
			break
		}
		cursor = page.NextCursor
	}
	if latest.IsZero() {
		return nil, nil
	}
	value := latest.UTC().Format(time.RFC3339Nano)
	return &value, nil
}

func (handler *Handler) writeAgentV1StreamEvent(
	controller *http.ResponseController,
	writer http.ResponseWriter,
	streamDeadline time.Time,
	event string,
	payload any,
) error {
	if err := handler.setSSEWriteDeadline(controller, streamDeadline); err != nil {
		return err
	}
	if err := writeAgentV1DataEvent(writer, event, payload); err != nil {
		return err
	}
	if err := controller.Flush(); err != nil {
		return err
	}
	return clearSSEWriteDeadline(controller)
}

func writeAgentV1DataEvent(writer http.ResponseWriter, event string, payload any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(writer, "event: %s\ndata: %s\n\n", event, encoded)
	return err
}

func equalAgentV1Timestamp(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func (handler *Handler) agentV1SubmitFeedback(writer http.ResponseWriter, request *http.Request) {
	if !handler.requireNoQuery(writer, request) {
		return
	}
	access, ok := bearerToken(request)
	if !ok {
		handler.writeAPIError(writer, request, http.StatusUnauthorized, "auth_authentication_rejected", "Authentication was rejected.")
		return
	}
	var payload agentV1FeedbackRequest
	if err := decodeStrictJSONWithLimit(
		writer,
		request,
		&payload,
		maxAgentV1FeedbackJSONBytes,
		fmt.Sprintf("Feedback payload exceeds %d bytes.", maxAgentV1FeedbackJSONBytes),
		"Feedback request body exceeded its duration limit.",
	); err != nil {
		handler.handleRequestContractError(writer, request, err)
		return
	}
	if payload.Images == nil {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "FEEDBACK_IMAGE_INVALID", "反馈截图格式不正确。")
		return
	}
	if len(*payload.Images) > feedback.MaxImages {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "FEEDBACK_TOO_MANY_IMAGES", "最多上传 8 张反馈截图。")
		return
	}
	images := make([]feedback.ImageInput, len(*payload.Images))
	for index, image := range *payload.Images {
		images[index] = feedback.ImageInput{Name: image.Name, DataURL: image.DataURL}
	}
	_, err := handler.feedback.SubmitAuthenticated(request.Context(), access, feedback.ApplicationInput{
		ClientRequestID: handler.requestIDs.Next(),
		Title:           payload.Title, Content: payload.Content, Platform: payload.Platform,
		AppVersion: payload.AppVersion, UserAgent: payload.UserAgent,
		Images: images,
	})
	if err != nil {
		handler.handleAgentV1FeedbackError(writer, request, err)
		return
	}
	writeJSON(writer, http.StatusOK, agentV1FeedbackResponse{Success: true, Message: "反馈已发送，感谢你的反馈。"})
}

func (handler *Handler) handleAgentV1FeedbackError(writer http.ResponseWriter, request *http.Request, err error) {
	switch feedback.CodeOf(err) {
	case feedback.ErrorImageInvalid:
		handler.writeAPIError(writer, request, http.StatusBadRequest, "FEEDBACK_IMAGE_INVALID", "反馈截图格式不正确。")
	case feedback.ErrorImageTooLarge:
		handler.writeAPIError(writer, request, http.StatusBadRequest, "FEEDBACK_IMAGE_TOO_LARGE", "单张反馈截图不能超过 8MB。")
	case feedback.ErrorTooManyImages:
		handler.writeAPIError(writer, request, http.StatusBadRequest, "FEEDBACK_TOO_MANY_IMAGES", "最多上传 8 张反馈截图。")
	default:
		handler.handleFeedbackError(writer, request, err)
	}
}

func (handler *Handler) agentV1SelfRequest(
	writer http.ResponseWriter,
	request *http.Request,
) (string, auth.Account, bool) {
	selectors, err := parseAgentV1SelfSelectors(request.URL.RawQuery, request.URL.ForceQuery)
	if err != nil {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_student_selector", "Student selector query is invalid.")
		return "", auth.Account{}, false
	}
	access, ok := bearerToken(request)
	if !ok {
		handler.writeAPIError(writer, request, http.StatusUnauthorized, "auth_authentication_rejected", "Authentication was rejected.")
		return "", auth.Account{}, false
	}
	account, err := handler.auth.Me(request.Context(), access)
	if err != nil {
		handler.handleAuthError(writer, request, err)
		return "", auth.Account{}, false
	}
	if selectors.studentID != nil && (account.StudentNumber == nil || *selectors.studentID != *account.StudentNumber) {
		handler.writeAPIError(writer, request, http.StatusForbidden, "student_selector_rejected", "Student selector does not identify the authenticated account.")
		return "", auth.Account{}, false
	}
	if selectors.ptaNickname != nil &&
		(account.PTANickname == nil || *selectors.ptaNickname != *account.PTANickname) {
		handler.writeAPIError(writer, request, http.StatusForbidden, "student_selector_rejected", "Student selector does not identify the authenticated account.")
		return "", auth.Account{}, false
	}
	return access, account, true
}

type agentV1SelfSelectors struct {
	studentID   *string
	ptaNickname *string
}

func parseAgentV1SelfSelectors(rawQuery string, forceQuery bool) (agentV1SelfSelectors, error) {
	if rawQuery == "" && !forceQuery {
		return agentV1SelfSelectors{}, nil
	}
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return agentV1SelfSelectors{}, err
	}
	for key, entries := range values {
		if (key != "studentId" && key != "ptaNickname") || len(entries) != 1 ||
			entries[0] == "" || strings.TrimSpace(entries[0]) != entries[0] ||
			!utf8.ValidString(entries[0]) || strings.IndexByte(entries[0], 0) >= 0 {
			return agentV1SelfSelectors{}, errors.New("student selector has an invalid key or value")
		}
		if key == "studentId" && (len(entries[0]) < auth.MinStudentNumberBytes || len(entries[0]) > auth.MaxStudentNumberBytes) {
			return agentV1SelfSelectors{}, errors.New("student ID is outside the canonical length boundary")
		}
	}
	selectors := agentV1SelfSelectors{}
	if entries, present := values["studentId"]; present {
		selectors.studentID = &entries[0]
	}
	if entries, present := values["ptaNickname"]; present {
		selectors.ptaNickname = &entries[0]
	}
	return selectors, nil
}

func mapAgentV1Dashboard(account auth.Account, result studentanalytics.Result) (agentV1StudentDashboardResponse, error) {
	response := emptyAgentV1Dashboard(account, result.State != studentanalytics.StateReady)
	switch result.State {
	case studentanalytics.StateNotGenerated, studentanalytics.StateNoObservations:
		return response, nil
	case studentanalytics.StateReady:
		if result.Ready == nil || len(result.Ready.ExamHistory) == 0 ||
			len(result.Ready.ExamHistory) != len(result.Ready.RatingHistory) {
			return agentV1StudentDashboardResponse{}, errors.New("ready analytics result is incomplete")
		}
	default:
		return agentV1StudentDashboardResponse{}, errors.New("analytics result state is invalid")
	}
	ready := result.Ready
	response.Metrics, response.MetricMissing = agentV1Metrics(ready.Current)
	response.Rating.Current = ready.Rating
	response.Rating.History = make([]agentV1RatingPoint, len(ready.RatingHistory))
	for index := range ready.RatingHistory {
		point := ready.RatingHistory[len(ready.RatingHistory)-1-index]
		response.Rating.History[index] = agentV1RatingPoint{
			ExamID: point.ExamID, ExamName: point.Title, Date: point.EventTime.UTC().Format(time.DateOnly),
			OldRating: point.OldRating, Delta: point.Delta, NewRating: point.NewRating,
		}
	}
	latestIndex := len(ready.ExamHistory) - 1
	latestExam := ready.ExamHistory[latestIndex]
	latestRating := ready.RatingHistory[latestIndex]
	latestDate := latestExam.EventTime.UTC().Format(time.DateOnly)
	response.Rating.LastDelta = &latestRating.Delta
	response.MetricDelta.LatestExamID = &latestExam.ExamID
	response.MetricDelta.LatestExamName = &latestExam.Title
	response.MetricDelta.LatestExamDate = &latestDate
	baseline := analytics.MetricValues{}
	firstExam := true
	if latestIndex > 0 {
		response.MetricDelta.Baseline = "previous_exam"
		baseline = ready.ExamHistory[latestIndex-1].Values
		firstExam = false
	}
	response.MetricDelta.Values = subtractAgentV1Metrics(latestExam.Values, baseline)
	response.ProgressExplanation = agentV1ProgressFromLatest(latestExam, latestRating, baseline, firstExam)
	response.MilestoneStreak = agentV1Milestones(ready)
	response.PeerComparison = agentV1PeerFromLatest(ready)
	response.PostExamSupport = agentV1SupportFromLatest(response.ProgressExplanation, ready)
	response.Identity.NoSubmissionRecords = false
	return response, nil
}

type agentV1MilestoneEvent struct {
	at   time.Time
	item agentV1MilestoneItem
}

func agentV1Milestones(ready *studentanalytics.ReadyResult) agentV1MilestoneStreak {
	response := agentV1MilestoneStreak{
		Available: true, NewMilestones: []agentV1MilestoneItem{},
		RecentMilestones: []agentV1MilestoneItem{}, NextTargets: []string{},
	}
	for index := len(ready.RatingHistory) - 1; index >= 0; index-- {
		if ready.RatingHistory[index].Delta <= 0 {
			break
		}
		response.CurrentPositiveStreak++
	}
	running := int64(0)
	events := make([]agentV1MilestoneEvent, 0)
	for _, point := range ready.RatingHistory {
		if point.Delta > 0 {
			running++
			response.BestPositiveStreak = max(response.BestPositiveStreak, running)
		} else {
			running = 0
		}
		for _, threshold := range []int64{900, 1000, 1200, 1400} {
			if point.OldRating < threshold && point.NewRating >= threshold {
				events = append(events, agentV1MilestoneEvent{
					at: point.EventTime,
					item: agentV1Milestone(point, fmt.Sprintf("rating_%d", threshold),
						fmt.Sprintf("Rating 达到 %d", threshold), fmt.Sprintf("本场后 Rating 达到 %d", point.NewRating)),
				})
			}
		}
		for _, threshold := range []int64{3, 5, 8} {
			if point.Delta > 0 && running == threshold {
				events = append(events, agentV1MilestoneEvent{
					at: point.EventTime,
					item: agentV1Milestone(point, fmt.Sprintf("streak_%d", threshold),
						fmt.Sprintf("正增连胜达到 %d 场", threshold), fmt.Sprintf("已连续 %d 场 Rating 正增长", running)),
				})
			}
		}
	}
	var previous analytics.MetricValues
	for _, point := range ready.ExamHistory {
		for _, metric := range agentV1NamedMetrics(point.Values) {
			current, present := agentV1RoundedMetric(metric.value)
			if !present {
				continue
			}
			baseline, _ := agentV1RoundedMetric(agentV1MetricByKey(previous, metric.key))
			for _, threshold := range []int64{60, 70, 80, 90} {
				if baseline < threshold && current >= threshold {
					examID := point.ExamID
					examDate := point.EventTime.UTC().Format(time.DateOnly)
					events = append(events, agentV1MilestoneEvent{
						at: point.EventTime,
						item: agentV1MilestoneItem{
							Code:   fmt.Sprintf("%s_%d", metric.key, threshold),
							Label:  fmt.Sprintf("%s达到 %d", metric.label, threshold),
							Detail: fmt.Sprintf("本场 %s来到 %d", metric.label, current),
							ExamID: &examID, ExamDate: &examDate,
						},
					})
				}
			}
		}
		previous = point.Values
	}
	sort.Slice(events, func(left, right int) bool {
		if events[left].at.Equal(events[right].at) {
			return events[left].item.Code < events[right].item.Code
		}
		return events[left].at.Before(events[right].at)
	})
	latestExamID := ready.ExamHistory[len(ready.ExamHistory)-1].ExamID
	for _, event := range events {
		if event.item.ExamID != nil && *event.item.ExamID == latestExamID {
			response.NewMilestones = append(response.NewMilestones, event.item)
		}
	}
	recent := append([]agentV1MilestoneEvent(nil), events...)
	sort.SliceStable(recent, func(left, right int) bool {
		return recent[left].at.After(recent[right].at)
	})
	for index := 0; index < min(5, len(recent)); index++ {
		response.RecentMilestones = append(response.RecentMilestones, recent[index].item)
	}
	response.NextTargets = agentV1NextTargets(ready)
	return response
}

func agentV1Milestone(
	point studentanalytics.RatingHistoryPoint,
	code, label, detail string,
) agentV1MilestoneItem {
	examID := point.ExamID
	examDate := point.EventTime.UTC().Format(time.DateOnly)
	return agentV1MilestoneItem{
		Code: code, Label: label, Detail: detail, ExamID: &examID, ExamDate: &examDate,
	}
}

func agentV1NextTargets(ready *studentanalytics.ReadyResult) []string {
	result := make([]string, 0, 2)
	for _, threshold := range []int64{900, 1000, 1200, 1400} {
		if threshold > ready.Rating {
			result = append(result, fmt.Sprintf("再 +%d rating 到 %d", threshold-ready.Rating, threshold))
			break
		}
	}
	type target struct {
		label     string
		threshold int64
		gap       int64
	}
	var nearest *target
	latest := ready.ExamHistory[len(ready.ExamHistory)-1]
	for _, metric := range agentV1NamedMetrics(latest.Values) {
		value, present := agentV1RoundedMetric(metric.value)
		if !present {
			continue
		}
		for _, threshold := range []int64{60, 70, 80, 90} {
			if threshold <= value {
				continue
			}
			candidate := target{label: metric.label, threshold: threshold, gap: threshold - value}
			if nearest == nil || candidate.gap < nearest.gap {
				nearest = &candidate
			}
			break
		}
	}
	if nearest != nil {
		result = append(result, fmt.Sprintf("%s再 +%d 到 %d", nearest.label, nearest.gap, nearest.threshold))
	}
	if len(result) == 0 {
		result = append(result, "保持节奏，争取在下一场考试刷新个人最佳表现")
	}
	return result[:min(2, len(result))]
}

func agentV1PeerFromLatest(ready *studentanalytics.ReadyResult) agentV1PeerComparison {
	if ready.LatestPeer == nil {
		return emptyAgentV1PeerComparison()
	}
	peer := ready.LatestPeer
	response := emptyAgentV1PeerComparison()
	response.Available = true
	response.PercentileBand.TotalParticipants = peer.TotalParticipants
	response.PercentileBand.MyRank = &peer.Rank
	percentile := math.RoundToEven(((float64(peer.TotalParticipants-peer.Position+1)/float64(peer.TotalParticipants))*100)*10) / 10
	response.PercentileBand.MyPercentile = &percentile
	code, label := agentV1PeerBand(peer.TotalParticipants, peer.Position)
	response.PercentileBand.BandCode = &code
	response.PercentileBand.BandLabel = label
	latest := ready.ExamHistory[len(ready.ExamHistory)-1].Values
	response.PercentileBand.GapVsBandMedian = agentV1PeerGap(
		peer.Score, int64Pointer(peer.Solved), latest,
		peer.BandMedian.Score, peer.BandMedian.Solved, peer.BandMedian.Values,
	)
	if peer.Previous == nil {
		return response
	}
	previous := peer.Previous
	response.PreviousRanker.Available = true
	rankGap := peer.Rank - previous.Rank
	response.PreviousRanker.RankGap = &rankGap
	response.PreviousRanker.ScoreGap = agentV1FloatGap(previous.Score, peer.Score)
	previousSolved := float64(previous.Solved)
	selfSolved := float64(peer.Solved)
	response.PreviousRanker.SolvedGap = agentV1FloatGap(&previousSolved, &selfSolved)
	response.PreviousRanker.MetricGapVsPrevious = agentV1PeerGap(
		nil, nil, previous.Values, nil, nil, latest,
	)
	return response
}

func agentV1PeerBand(total, position int64) (string, string) {
	top10 := max(int64(1), int64(math.Ceil(float64(total)*0.1)))
	top30 := max(top10+1, int64(math.Ceil(float64(total)*0.3)))
	median := max(top30+1, int64(math.Ceil(float64(total)*0.7)))
	if position <= top10 {
		return "top_10", "Top 10%"
	}
	if position <= top30 {
		return "top_30", "Top 30%"
	}
	if position <= median {
		return "median_zone", "中位区间"
	}
	return "improve_zone", "提升区间"
}

func agentV1PeerGap(
	currentScore *float64,
	currentSolved *int64,
	current analytics.MetricValues,
	baselineScore, baselineSolved *float64,
	baseline analytics.MetricValues,
) agentV1PeerMetricGap {
	result := agentV1PeerMetricGap{
		Score: agentV1FloatGap(currentScore, baselineScore),
	}
	if currentSolved != nil && baselineSolved != nil {
		value := float64(*currentSolved - int64(math.RoundToEven(*baselineSolved)))
		result.Solved = &value
	}
	result.Knowledge = agentV1MetricGap(current.Knowledge, baseline.Knowledge)
	result.Accuracy = agentV1MetricGap(current.Accuracy, baseline.Accuracy)
	result.Quality = agentV1MetricGap(current.Quality, baseline.Quality)
	result.Flexibility = agentV1MetricGap(current.Flexibility, baseline.Flexibility)
	result.Proficiency = agentV1MetricGap(current.Proficiency, baseline.Proficiency)
	return result
}

func agentV1FloatGap(current, baseline *float64) *float64 {
	if current == nil || baseline == nil {
		return nil
	}
	value := math.RoundToEven((*current-*baseline)*10) / 10
	return &value
}

func agentV1MetricGap(current, baseline *float64) *float64 {
	if current == nil || baseline == nil {
		return nil
	}
	value := float64(int64(math.RoundToEven(*current)) - int64(math.RoundToEven(*baseline)))
	return &value
}

func agentV1SupportFromLatest(
	progress agentV1ProgressExplanation,
	ready *studentanalytics.ReadyResult,
) agentV1PostExamSupport {
	latestIndex := len(ready.ExamHistory) - 1
	latest := ready.ExamHistory[latestIndex].Values
	previous := analytics.MetricValues{}
	if latestIndex > 0 {
		previous = ready.ExamHistory[latestIndex-1].Values
	}
	improvements, setbacks := 0, 0
	for _, metric := range agentV1NamedMetrics(latest) {
		current, present := agentV1RoundedMetric(metric.value)
		if !present {
			continue
		}
		baseline, _ := agentV1RoundedMetric(agentV1MetricByKey(previous, metric.key))
		delta := current - baseline
		if delta >= 3 {
			improvements++
		} else if delta <= -3 {
			setbacks++
		}
	}
	ratingDelta := int64(0)
	if progress.RatingDelta != nil {
		ratingDelta = *progress.RatingDelta
	}
	if ratingDelta <= -10 || setbacks >= 3 {
		return agentV1PostExamSupport{
			Available: true, Mode: "recovery", Headline: "先止损，再反弹",
			Message: "这次波动不代表能力定型，先把失分来源收敛，下一场就有机会稳住回升。",
			ActionPlan: []string{
				"先挑 1 道最可惜的错题，补齐触发错误的知识点。",
				"下一次练习限制无效提交次数，先想后交。",
				"练习结束后写下 1 条“我下次会怎么做”的执行句。",
			},
			CheckInQuestion: "如果下一场只改 1 件事，你最想先改哪一件？",
		}
	}
	if ratingDelta >= 10 && improvements >= 3 {
		return agentV1PostExamSupport{
			Available: true, Mode: "reinforce", Headline: "保持优势，固化方法",
			Message: "当前上升势头很明确，关键是把这次有效策略沉淀成可复用流程。",
			ActionPlan: []string{
				"复盘本场最有效的 2 个策略，并写成固定检查清单。",
				"下一场沿用同样节奏，再增加 1 个小挑战目标。",
				"保留做对题目的模板，形成个人题型解法库。",
			},
			CheckInQuestion: "这次最值得复用的做题策略是哪一条？",
		}
	}
	return agentV1PostExamSupport{
		Available: true, Mode: "steady", Headline: "稳步推进",
		Message: "整体状态可控，先维持有效节奏，再逐步抬高短板能力。",
		ActionPlan: []string{
			"保持当前做题节奏，优先减少 1 类重复失误。",
			"为下一场设定 1 个可量化小目标（如准确 +3）。",
			"训练后复盘 10 分钟，记录可复用步骤。",
		},
		CheckInQuestion: "下一场你希望先把哪项指标提升一点点？",
	}
}

type agentV1NamedMetric struct {
	key   string
	label string
	value *float64
}

func agentV1NamedMetrics(values analytics.MetricValues) []agentV1NamedMetric {
	return []agentV1NamedMetric{
		{key: "knowledge", label: "知识", value: values.Knowledge},
		{key: "accuracy", label: "准确", value: values.Accuracy},
		{key: "quality", label: "质量", value: values.Quality},
		{key: "flexibility", label: "灵活", value: values.Flexibility},
		{key: "proficiency", label: "熟练", value: values.Proficiency},
	}
}

func agentV1MetricByKey(values analytics.MetricValues, key string) *float64 {
	switch key {
	case "knowledge":
		return values.Knowledge
	case "accuracy":
		return values.Accuracy
	case "quality":
		return values.Quality
	case "flexibility":
		return values.Flexibility
	case "proficiency":
		return values.Proficiency
	default:
		panic("unknown metric key")
	}
}

func agentV1RoundedMetric(value *float64) (int64, bool) {
	if value == nil {
		return 0, false
	}
	return int64(math.RoundToEven(*value)), true
}

func int64Pointer(value int64) *int64 { return &value }

func emptyAgentV1Dashboard(account auth.Account, noSubmissions bool) agentV1StudentDashboardResponse {
	response := agentV1StudentDashboardResponse{
		MetricMissing:       agentV1MetricMissingValues{Knowledge: true, Accuracy: true, Quality: true, Flexibility: true, Proficiency: true},
		Rating:              agentV1Rating{History: []agentV1RatingPoint{}},
		MetricDelta:         agentV1MetricDelta{Baseline: "zero"},
		Identity:            agentV1Identity(account, noSubmissions),
		ProgressExplanation: agentV1ProgressExplanation{KeyImprovements: []string{}, KeySetbacks: []string{}, Summary: "暂无可比较数据"},
		MilestoneStreak:     agentV1MilestoneStreak{NewMilestones: []agentV1MilestoneItem{}, RecentMilestones: []agentV1MilestoneItem{}, NextTargets: []string{}},
		PeerComparison:      emptyAgentV1PeerComparison(),
		PostExamSupport: agentV1PostExamSupport{
			Mode: "steady", Headline: "先稳住节奏",
			Message: "当前暂无足够数据做完整判断，先完成下一场训练，再看趋势变化。",
			ActionPlan: []string{
				"先复盘最近一次训练，标出 1 个可立即修正的问题。",
				"下一场练习只设 1 个核心目标，避免目标过多。",
				"训练后记录 3 句话：做对了什么、卡住了什么、下次怎么改。",
			},
			CheckInQuestion: "下一场你最想先稳住哪一项能力？",
		},
	}
	return response
}

func emptyAgentV1PeerComparison() agentV1PeerComparison {
	response := agentV1PeerComparison{DefaultMode: "percentile_band"}
	response.PercentileBand.BandLabel = "暂无可比较数据"
	return response
}

func agentV1Identity(account auth.Account, noSubmissions bool) agentV1StudentIdentity {
	studentID := ""
	if account.StudentNumber != nil {
		studentID = *account.StudentNumber
	}
	return agentV1StudentIdentity{StudentID: studentID, PTANickname: account.PTANickname, NoSubmissionRecords: noSubmissions}
}

func agentV1Metrics(values analytics.MetricValues) (agentV1MetricValues, agentV1MetricMissingValues) {
	read := func(value *float64) (float64, bool) {
		if value == nil {
			return 0, true
		}
		return *value, false
	}
	metrics := agentV1MetricValues{}
	missing := agentV1MetricMissingValues{}
	metrics.Knowledge, missing.Knowledge = read(values.Knowledge)
	metrics.Accuracy, missing.Accuracy = read(values.Accuracy)
	metrics.Quality, missing.Quality = read(values.Quality)
	metrics.Flexibility, missing.Flexibility = read(values.Flexibility)
	metrics.Proficiency, missing.Proficiency = read(values.Proficiency)
	return metrics, missing
}

func subtractAgentV1Metrics(current, baseline analytics.MetricValues) agentV1MetricValues {
	delta := func(currentValue, baselineValue *float64) float64 {
		currentRounded, _ := agentV1RoundedMetric(currentValue)
		baselineRounded, _ := agentV1RoundedMetric(baselineValue)
		return float64(currentRounded - baselineRounded)
	}
	return agentV1MetricValues{
		Knowledge:   delta(current.Knowledge, baseline.Knowledge),
		Accuracy:    delta(current.Accuracy, baseline.Accuracy),
		Quality:     delta(current.Quality, baseline.Quality),
		Flexibility: delta(current.Flexibility, baseline.Flexibility),
		Proficiency: delta(current.Proficiency, baseline.Proficiency),
	}
}

func agentV1ProgressFromLatest(
	exam studentanalytics.ExamHistoryPoint,
	rating studentanalytics.RatingHistoryPoint,
	baseline analytics.MetricValues,
	firstExam bool,
) agentV1ProgressExplanation {
	date := exam.EventTime.UTC().Format(time.DateOnly)
	response := agentV1ProgressExplanation{
		Available: true, LatestExamID: &exam.ExamID, LatestExamName: &exam.Title,
		LatestExamDate: &date, RatingDelta: &rating.Delta,
		KeyImprovements: []string{}, KeySetbacks: []string{},
	}
	type change struct {
		key   string
		label string
		delta int64
	}
	improvements := make([]change, 0, 5)
	setbacks := make([]change, 0, 5)
	for _, metric := range agentV1NamedMetrics(exam.Values) {
		current, present := agentV1RoundedMetric(metric.value)
		if !present {
			continue
		}
		previous, _ := agentV1RoundedMetric(agentV1MetricByKey(baseline, metric.key))
		item := change{key: metric.key, label: metric.label, delta: current - previous}
		if item.delta >= 3 {
			improvements = append(improvements, item)
		} else if item.delta <= -3 {
			setbacks = append(setbacks, item)
		}
	}
	sort.SliceStable(improvements, func(left, right int) bool { return improvements[left].delta > improvements[right].delta })
	sort.SliceStable(setbacks, func(left, right int) bool { return setbacks[left].delta < setbacks[right].delta })
	for _, item := range improvements[:min(2, len(improvements))] {
		response.KeyImprovements = append(response.KeyImprovements,
			fmt.Sprintf("%s %+d，%s", item.label, item.delta, agentV1ProgressReason(item.key, true)))
	}
	for _, item := range setbacks[:min(2, len(setbacks))] {
		response.KeySetbacks = append(response.KeySetbacks,
			fmt.Sprintf("%s %+d，%s", item.label, item.delta, agentV1ProgressReason(item.key, false)))
	}
	response.Summary = agentV1ProgressSummary(&rating.Delta, response.KeyImprovements, response.KeySetbacks, firstExam)
	return response
}

func agentV1ProgressReason(key string, improvement bool) string {
	if improvement {
		return map[string]string{
			"knowledge": "说明本次知识点命中更稳定", "accuracy": "说明提交策略更稳，低效尝试更少",
			"quality": "说明代码实现质量更稳，边界处理更扎实", "flexibility": "说明做题取舍更灵活，节奏把控更好",
			"proficiency": "说明编码速度更顺，熟练度提升",
		}[key]
	}
	return map[string]string{
		"knowledge": "说明知识点命中还不稳定", "accuracy": "说明无效提交偏多或策略波动",
		"quality": "说明实现细节质量波动，需要复盘边界与复杂度", "flexibility": "说明切题策略偏保守，建议优化做题节奏",
		"proficiency": "说明解题速度有回落，需要强化模板熟练度",
	}[key]
}

func agentV1ProgressSummary(ratingDelta *int64, improvements, setbacks []string, firstExam bool) string {
	firstLabel := func(values []string) string {
		if len(values) == 0 {
			return ""
		}
		return strings.SplitN(values[0], "，", 2)[0]
	}
	if ratingDelta != nil {
		delta := *ratingDelta
		if delta > 0 {
			if len(improvements) > 0 {
				return fmt.Sprintf("本场整体向好，Rating +%d，关键增益来自%s。", delta, firstLabel(improvements))
			}
			return fmt.Sprintf("本场 Rating +%d，整体趋势在上行。", delta)
		}
		if delta < 0 {
			if len(setbacks) > 0 {
				return fmt.Sprintf("本场出现回落，Rating %d，主要波动在%s。", delta, firstLabel(setbacks))
			}
			return fmt.Sprintf("本场 Rating %d，建议先稳住做题节奏。", delta)
		}
		return "本场 Rating 持平，能力结构有变化，建议保留有效策略并修正波动点。"
	}
	if len(improvements) > 0 && len(setbacks) == 0 {
		prefix := "本场能力表现向好"
		if firstExam {
			prefix = "首场考试表现积极"
		}
		return fmt.Sprintf("%s，亮点集中在%s。", prefix, firstLabel(improvements))
	}
	if len(setbacks) > 0 && len(improvements) == 0 {
		prefix := "本场能力有回落"
		if firstExam {
			prefix = "首场考试出现波动"
		}
		return fmt.Sprintf("%s，优先关注%s。", prefix, firstLabel(setbacks))
	}
	if len(improvements) > 0 && len(setbacks) > 0 {
		return "本场有进步也有波动，建议保留有效策略并优先修正退步项。"
	}
	if firstExam {
		return "这是首场考试，当前数据将作为后续成长对比基线。"
	}
	return "本场整体变化不大，建议保持节奏并观察下一场趋势。"
}
