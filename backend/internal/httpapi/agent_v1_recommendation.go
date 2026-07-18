package httpapi

import (
	"errors"
	"net/http"
	"sort"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/recommendation"
)

type agentV1LearningPathResponse struct {
	StudentEntityID  int64          `json:"studentEntityId"`
	StudentEntityIDs []int64        `json:"studentEntityIds"`
	ModelRunID       *int64         `json:"modelRunId"`
	GeneratedAt      *string        `json:"generatedAt"`
	Targets          []string       `json:"targets"`
	Path             []string       `json:"path"`
	Explanations     map[string]any `json:"explanations"`
}

type agentV1LearningPathStatusResponse struct {
	StudentEntityID  int64                           `json:"studentEntityId"`
	StudentEntityIDs []int64                         `json:"studentEntityIds"`
	Items            []agentV1LearningPathStatusItem `json:"items"`
}

type agentV1LearningPathStatusItem struct {
	Point       string  `json:"point"`
	Mastery     float64 `json:"mastery"`
	Attempted   int64   `json:"attempted"`
	Correct     int64   `json:"correct"`
	LastTriedAt *string `json:"lastTriedAt"`
}

type agentV1KnowledgeStats struct {
	Attempted    int64                       `json:"attempted"`
	Correct      int64                       `json:"correct"`
	Accuracy     float64                     `json:"accuracy"`
	LastTriedAt  *string                     `json:"lastTriedAt"`
	RecentSeries []agentV1KnowledgeRecentDay `json:"recentSeries"`
}

type agentV1KnowledgeRecentDay struct {
	Date      string `json:"date"`
	Attempted int64  `json:"attempted"`
	Correct   int64  `json:"correct"`
}

type agentV1KnowledgeProblem struct {
	ProblemID       string   `json:"problemId"`
	Title           *string  `json:"title"`
	Difficulty      *float64 `json:"difficulty"`
	KnowledgePoints []string `json:"knowledgePoints"`
	Score           *float64 `json:"score"`
	Reason          *string  `json:"reason"`
}

type agentV1KnowledgeNodeResponse struct {
	Point         string                    `json:"point"`
	Level         *string                   `json:"level"`
	Parents       []string                  `json:"parents"`
	Children      []string                  `json:"children"`
	Prerequisites []string                  `json:"prerequisites"`
	Successors    []string                  `json:"successors"`
	Description   *string                   `json:"description"`
	Mastery       float64                   `json:"mastery"`
	Stats         agentV1KnowledgeStats     `json:"stats"`
	Problems      []agentV1KnowledgeProblem `json:"problems"`
}

func (handler *Handler) agentV1LearningPath(writer http.ResponseWriter, request *http.Request) {
	if !requestBodyIsEmpty(request) {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "request_body_not_allowed", "Request body must be empty.")
		return
	}
	if !handler.requireNoQuery(writer, request) {
		return
	}
	current, ok := handler.agentV1CurrentRecommendation(writer, request)
	if !ok {
		return
	}
	writeJSON(writer, http.StatusOK, mapAgentV1LearningPath(current))
}

func (handler *Handler) agentV1LearningPathStatus(writer http.ResponseWriter, request *http.Request) {
	if !requestBodyIsEmpty(request) {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "request_body_not_allowed", "Request body must be empty.")
		return
	}
	if !handler.requireNoQuery(writer, request) {
		return
	}
	current, ok := handler.agentV1CurrentRecommendation(writer, request)
	if !ok {
		return
	}
	items := []agentV1LearningPathStatusItem{}
	if current.State == recommendation.RecommendationFresh && current.Result != nil &&
		current.Result.Status == recommendation.RecommendationResultReady {
		mastery := make(map[string]float64, len(current.Result.KnowledgeMastery))
		for _, item := range current.Result.KnowledgeMastery {
			mastery[item.KnowledgePointID] = item.Mastery
		}
		items = make([]agentV1LearningPathStatusItem, len(current.Result.LearningPath))
		activity := agentV1KnowledgeActivityByPoint(current.KnowledgeActivity)
		for index, step := range current.Result.LearningPath {
			stats := activity[step.KnowledgePointID]
			items[index] = agentV1LearningPathStatusItem{
				Point: step.KnowledgePointID, Mastery: mastery[step.KnowledgePointID],
				Attempted: stats.Attempted, Correct: stats.Correct,
				LastTriedAt: agentV1ActivityTime(stats.LastTriedAt),
			}
		}
	}
	writeJSON(writer, http.StatusOK, agentV1LearningPathStatusResponse{
		StudentEntityID: 0, StudentEntityIDs: []int64{}, Items: items,
	})
}

func (handler *Handler) agentV1KnowledgeNode(writer http.ResponseWriter, request *http.Request) {
	if !requestBodyIsEmpty(request) {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "request_body_not_allowed", "Request body must be empty.")
		return
	}
	topK, err := parseAgentV1KnowledgeTopK(request.URL.RawQuery, request.URL.ForceQuery)
	if err != nil {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_top_k", "topK must be one canonical decimal integer from 1 through 20.")
		return
	}
	point := request.PathValue("point")
	if !recommendationKnowledgeID.MatchString(point) {
		handler.writeAPIError(writer, request, http.StatusBadRequest, "invalid_knowledge_point", "Knowledge point identity is invalid.")
		return
	}
	current, ok := handler.agentV1CurrentRecommendation(writer, request)
	if !ok {
		return
	}
	response, found := mapAgentV1KnowledgeNode(current, point, topK)
	if !found {
		handler.writeAPIError(writer, request, http.StatusNotFound, "knowledge_point_not_found", "Knowledge point does not exist in the current recommendation.")
		return
	}
	writeJSON(writer, http.StatusOK, response)
}

func (handler *Handler) agentV1CurrentRecommendation(
	writer http.ResponseWriter,
	request *http.Request,
) (recommendation.CurrentRecommendation, bool) {
	access, ok := bearerToken(request)
	if !ok {
		handler.writeAPIError(writer, request, http.StatusUnauthorized, "auth_authentication_rejected", "Authentication was rejected.")
		return recommendation.CurrentRecommendation{}, false
	}
	current, err := handler.recommendationReader.ReadCurrent(request.Context(), access)
	if err != nil {
		handler.handleRecommendationError(writer, request, err)
		return recommendation.CurrentRecommendation{}, false
	}
	if !validCurrentRecommendation(current) {
		handler.logAgentV1Failure(request, "project_current_recommendation", errors.New("recommendation reader returned an invalid result"))
		handler.writeAPIError(writer, request, http.StatusInternalServerError, "internal_error", "Request could not be completed.")
		return recommendation.CurrentRecommendation{}, false
	}
	return current, true
}

func mapAgentV1LearningPath(current recommendation.CurrentRecommendation) agentV1LearningPathResponse {
	response := agentV1LearningPathResponse{
		StudentEntityID: 0, StudentEntityIDs: []int64{}, Targets: []string{}, Path: []string{},
		Explanations: map[string]any{},
	}
	if current.ModelHeadRevision > 0 {
		modelRunID := current.ModelHeadRevision
		response.ModelRunID = &modelRunID
	}
	if current.State != recommendation.RecommendationFresh || current.Result == nil ||
		current.Result.Status != recommendation.RecommendationResultReady {
		return response
	}
	response.Path = make([]string, len(current.Result.LearningPath))
	for index, step := range current.Result.LearningPath {
		response.Path[index] = step.KnowledgePointID
		if step.ReasonCode == "knowledge_gap" {
			response.Targets = append(response.Targets, step.KnowledgePointID)
		}
		response.Explanations[step.KnowledgePointID] = map[string]any{
			"order": step.Order, "label": step.Label, "description": step.Description,
			"reasonCode": step.ReasonCode, "mastery": step.Mastery,
			"targetMastery": step.TargetMastery, "prerequisiteIds": step.PrerequisiteIDs,
		}
	}
	return response
}

func mapAgentV1KnowledgeNode(
	current recommendation.CurrentRecommendation,
	point string,
	topK int,
) (agentV1KnowledgeNodeResponse, bool) {
	if current.State != recommendation.RecommendationFresh || current.Result == nil {
		return agentV1KnowledgeNodeResponse{}, false
	}
	var selected *recommendation.RecommendationKnowledgeMastery
	children := make([]string, 0)
	for index := range current.Result.KnowledgeMastery {
		item := &current.Result.KnowledgeMastery[index]
		if item.KnowledgePointID == point {
			selected = item
		}
		for _, prerequisite := range item.PrerequisiteIDs {
			if prerequisite == point {
				children = append(children, item.KnowledgePointID)
				break
			}
		}
	}
	if selected == nil {
		return agentV1KnowledgeNodeResponse{}, false
	}
	sort.Strings(children)
	parents := append([]string(nil), selected.PrerequisiteIDs...)
	sort.Strings(parents)
	description := (*string)(nil)
	if selected.Description != "" {
		value := selected.Description
		description = &value
	}
	problems := []agentV1KnowledgeProblem{}
	for _, step := range current.Result.LearningPath {
		if step.KnowledgePointID != point {
			continue
		}
		limit := min(topK, len(step.RecommendedProblems))
		problems = make([]agentV1KnowledgeProblem, limit)
		for index := 0; index < limit; index++ {
			problem := step.RecommendedProblems[index]
			title := problem.Title
			score := problem.RecommendationScore
			reason := step.ReasonCode
			problems[index] = agentV1KnowledgeProblem{
				ProblemID: problem.ProblemID, Title: &title, Difficulty: nil,
				KnowledgePoints: []string{point}, Score: &score, Reason: &reason,
			}
		}
		break
	}
	activity := agentV1KnowledgeActivityByPoint(current.KnowledgeActivity)[point]
	recent := make([]agentV1KnowledgeRecentDay, len(activity.RecentSeries))
	for index, day := range activity.RecentSeries {
		recent[index] = agentV1KnowledgeRecentDay{
			Date: day.Date, Attempted: day.Attempted, Correct: day.Correct,
		}
	}
	accuracy := 0.0
	if activity.Attempted > 0 {
		accuracy = float64(activity.Correct) / float64(activity.Attempted)
	}
	return agentV1KnowledgeNodeResponse{
		Point: point, Level: nil, Parents: parents, Children: children,
		Prerequisites: append([]string(nil), parents...), Successors: append([]string(nil), children...),
		Description: description, Mastery: selected.Mastery,
		Stats: agentV1KnowledgeStats{
			Attempted: activity.Attempted, Correct: activity.Correct, Accuracy: accuracy,
			LastTriedAt: agentV1ActivityTime(activity.LastTriedAt), RecentSeries: recent,
		},
		Problems: problems,
	}, true
}

func agentV1KnowledgeActivityByPoint(
	items []recommendation.RecommendationKnowledgeActivity,
) map[string]recommendation.RecommendationKnowledgeActivity {
	result := make(map[string]recommendation.RecommendationKnowledgeActivity, len(items))
	for _, item := range items {
		result[item.KnowledgePointID] = item
	}
	return result
}

func agentV1ActivityTime(value *time.Time) *string {
	if value == nil {
		return nil
	}
	text := value.UTC().Format(time.RFC3339Nano)
	return &text
}

func parseAgentV1KnowledgeTopK(rawQuery string, forceQuery bool) (int, error) {
	if rawQuery == "" && !forceQuery {
		return 5, nil
	}
	fields, err := parseCanonicalQueryFields(rawQuery, forceQuery, map[string]struct{}{"topK": {}})
	if err != nil {
		return 0, err
	}
	value, present := fields["topK"]
	if !present {
		return 5, nil
	}
	return parseCanonicalPositiveDecimal(value, 1, 20)
}
