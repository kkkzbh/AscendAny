package achievement

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"

	"github.com/kkkzbh/AscendAny/backend/internal/analytics"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

var (
	canonicalUUIDv4 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	ruleCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
)

type Repository interface {
	LoadSelf(context.Context, SelfQuery) (RepositorySnapshot, error)
}

type Service struct {
	repository Repository
}

func NewService(repository Repository) (*Service, error) {
	if repository == nil {
		return nil, achievementError(ErrorInvalidConfiguration, "construct achievement service", errors.New("repository is required"))
	}
	return &Service{repository: repository}, nil
}

func (service *Service) GetSelf(ctx context.Context, query SelfQuery) (Result, error) {
	if err := validateSelfQuery(ctx, query); err != nil {
		return Result{}, err
	}
	snapshot, err := service.repository.LoadSelf(ctx, query)
	if err != nil {
		return Result{}, err
	}
	if err := validateSnapshot(snapshot); err != nil {
		return Result{}, storedDataFailure("validate achievement repository snapshot", err)
	}
	result := buildResult(snapshot)
	if err := validateResult(result); err != nil {
		return Result{}, storedDataFailure("validate achievement result", err)
	}
	return result, nil
}

func validateSelfQuery(ctx context.Context, query SelfQuery) error {
	if ctx == nil {
		return achievementError(ErrorInvalidQuery, "validate achievement self query", errors.New("context is required"))
	}
	principal := query.Principal
	if !canonicalUUIDv4.MatchString(principal.AccountID) ||
		!canonicalUUIDv4.MatchString(principal.SessionID) ||
		!canonicalUUIDv4.MatchString(principal.JWTID) || principal.AuthRevision <= 0 {
		return achievementError(ErrorInvalidQuery, "validate achievement self query", errors.New("access principal is invalid"))
	}
	if principal.Role != auth.RoleStudent {
		return achievementError(ErrorForbidden, "validate achievement self query", errors.New("student role is required"))
	}
	return nil
}

func validateSnapshot(snapshot RepositorySnapshot) error {
	if snapshot.RuleSetVersion <= 0 || snapshot.RuleHeadRevision <= 0 {
		return errors.New("active achievement rule head is invalid")
	}
	if snapshot.AIDialogueCount < 0 {
		return errors.New("AI dialogue count is negative")
	}
	if snapshot.AnalyticsHeadRevision < 0 || snapshot.AnalyticsHeadRevision == 0 && snapshot.Metrics != nil {
		return errors.New("analytics head and metrics are inconsistent")
	}
	if snapshot.Metrics != nil {
		encoded, err := json.Marshal(snapshot.Metrics)
		if err != nil {
			return fmt.Errorf("encode student metrics for validation: %w", err)
		}
		if _, err := analytics.DecodeStoredStudentMetrics(encoded); err != nil {
			return fmt.Errorf("validate student metrics: %w", err)
		}
	}
	if snapshot.Rules == nil || len(snapshot.Rules) == 0 {
		return errors.New("active achievement rule set is empty")
	}
	seenCodes := make(map[string]struct{}, len(snapshot.Rules))
	seenOrders := make(map[int64]struct{}, len(snapshot.Rules))
	var previousOrder int64
	previousCode := ""
	for index, rule := range snapshot.Rules {
		if err := validateRule(rule); err != nil {
			return fmt.Errorf("rule %d: %w", index, err)
		}
		if _, exists := seenCodes[rule.Code]; exists {
			return fmt.Errorf("rule %d duplicates code %q", index, rule.Code)
		}
		seenCodes[rule.Code] = struct{}{}
		if _, exists := seenOrders[rule.SortOrder]; exists {
			return fmt.Errorf("rule %d duplicates sort order %d", index, rule.SortOrder)
		}
		seenOrders[rule.SortOrder] = struct{}{}
		if index > 0 && (rule.SortOrder < previousOrder || rule.SortOrder == previousOrder && rule.Code <= previousCode) {
			return errors.New("achievement rules are not strictly ordered")
		}
		previousOrder = rule.SortOrder
		previousCode = rule.Code
	}
	return nil
}

func validateRule(rule Rule) error {
	if !ruleCodePattern.MatchString(rule.Code) ||
		strings.TrimSpace(rule.Title) != rule.Title || rule.Title == "" || len(rule.Title) > 256 ||
		strings.TrimSpace(rule.Description) != rule.Description || rule.Description == "" || len(rule.Description) > 2048 ||
		rule.SortOrder <= 0 || !validProgressKey(rule.ProgressKey) {
		return errors.New("rule identity, text, progress key, or order is invalid")
	}
	if !finitePositive(rule.BronzeTarget) || !finitePositive(rule.SilverTarget) || !finitePositive(rule.GoldTarget) ||
		rule.BronzeTarget > rule.SilverTarget || rule.SilverTarget > rule.GoldTarget {
		return errors.New("rule thresholds are invalid")
	}
	return nil
}

func validProgressKey(key ProgressKey) bool {
	switch key {
	case ProgressExamCount,
		ProgressPositiveDeltaCount,
		ProgressBestPositiveStreak,
		ProgressKnowledgeMax,
		ProgressAccuracyMax,
		ProgressQualityMax,
		ProgressFlexibilityMax,
		ProgressProficiencyMax,
		ProgressMaxRating,
		ProgressMaxRatingDelta,
		ProgressTop10Count,
		ProgressTop3Count,
		ProgressRank1Count,
		ProgressMaxExamMinMetric,
		ProgressCurrentMinMetric,
		ProgressAIDialogueCount:
		return true
	default:
		return false
	}
}

func buildResult(snapshot RepositorySnapshot) Result {
	state := StateNotGenerated
	progress := map[ProgressKey]float64{ProgressAIDialogueCount: float64(snapshot.AIDialogueCount)}
	if snapshot.AnalyticsHeadRevision > 0 {
		state = StateNoObservations
	}
	if snapshot.Metrics != nil {
		state = StateReady
		for key, value := range deriveAnalyticsProgress(*snapshot.Metrics) {
			progress[key] = value
		}
	}
	items := make([]Item, 0, len(snapshot.Rules))
	summary := Summary{Total: len(snapshot.Rules)}
	for _, rule := range snapshot.Rules {
		value := progress[rule.ProgressKey]
		tier := evaluateTier(value, rule)
		switch tier {
		case 0:
			summary.Locked++
		case 1:
			summary.Bronze++
		case 2:
			summary.Silver++
		case 3:
			summary.Gold++
		}
		items = append(items, Item{
			Code: rule.Code, Title: rule.Title, Description: rule.Description,
			ProgressKey: rule.ProgressKey, Tier: tier, Progress: value,
			BronzeTarget: rule.BronzeTarget, SilverTarget: rule.SilverTarget, GoldTarget: rule.GoldTarget,
			SortOrder: rule.SortOrder,
		})
	}
	return Result{
		State: state, AnalyticsHeadRevision: snapshot.AnalyticsHeadRevision,
		RuleSetVersion: snapshot.RuleSetVersion, RuleHeadRevision: snapshot.RuleHeadRevision,
		Summary: summary, Items: items,
	}
}

func deriveAnalyticsProgress(metrics analytics.StudentMetrics) map[ProgressKey]float64 {
	result := map[ProgressKey]float64{
		ProgressExamCount: float64(len(metrics.ExamHistory)),
	}
	positiveStreak := int64(0)
	for index, point := range metrics.ExamHistory {
		for key, value := range metricValues(point.Values) {
			if value != nil && *value > result[key] {
				result[key] = *value
			}
		}
		if minimum, complete := completeMetricMinimum(point.Values); complete && minimum > result[ProgressMaxExamMinMetric] {
			result[ProgressMaxExamMinMetric] = minimum
		}
		rating := metrics.RatingHistory[index]
		if rating.Delta > 0 {
			result[ProgressPositiveDeltaCount]++
			positiveStreak++
			if float64(positiveStreak) > result[ProgressBestPositiveStreak] {
				result[ProgressBestPositiveStreak] = float64(positiveStreak)
			}
		} else {
			positiveStreak = 0
		}
		if float64(rating.NewRating) > result[ProgressMaxRating] {
			result[ProgressMaxRating] = float64(rating.NewRating)
		}
		if rating.Delta > 0 && float64(rating.Delta) > result[ProgressMaxRatingDelta] {
			result[ProgressMaxRatingDelta] = float64(rating.Delta)
		}
		if rating.Rank <= 10 {
			result[ProgressTop10Count]++
		}
		if rating.Rank <= 3 {
			result[ProgressTop3Count]++
		}
		if rating.Rank == 1 {
			result[ProgressRank1Count]++
		}
	}
	if minimum, complete := completeMetricMinimum(metrics.Current); complete {
		result[ProgressCurrentMinMetric] = minimum
	}
	return result
}

func metricValues(values analytics.MetricValues) map[ProgressKey]*float64 {
	return map[ProgressKey]*float64{
		ProgressKnowledgeMax: values.Knowledge, ProgressAccuracyMax: values.Accuracy,
		ProgressQualityMax: values.Quality, ProgressFlexibilityMax: values.Flexibility,
		ProgressProficiencyMax: values.Proficiency,
	}
}

func completeMetricMinimum(values analytics.MetricValues) (float64, bool) {
	metrics := []*float64{values.Knowledge, values.Accuracy, values.Quality, values.Flexibility, values.Proficiency}
	for _, value := range metrics {
		if value == nil {
			return 0, false
		}
	}
	minimum := *metrics[0]
	for _, value := range metrics[1:] {
		if *value < minimum {
			minimum = *value
		}
	}
	return minimum, true
}

func evaluateTier(progress float64, rule Rule) int {
	switch {
	case progress >= rule.GoldTarget:
		return 3
	case progress >= rule.SilverTarget:
		return 2
	case progress >= rule.BronzeTarget:
		return 1
	default:
		return 0
	}
}

func validateResult(result Result) error {
	if result.RuleSetVersion <= 0 || result.RuleHeadRevision <= 0 || result.AnalyticsHeadRevision < 0 || result.Items == nil || len(result.Items) == 0 {
		return errors.New("result head metadata or items are invalid")
	}
	switch result.State {
	case StateNotGenerated:
		if result.AnalyticsHeadRevision != 0 {
			return errors.New("not_generated requires analytics head revision zero")
		}
	case StateNoObservations, StateReady:
		if result.AnalyticsHeadRevision <= 0 {
			return errors.New("generated states require a positive analytics head revision")
		}
	default:
		return fmt.Errorf("unknown achievement state %q", result.State)
	}
	counts := Summary{Total: len(result.Items)}
	for index, item := range result.Items {
		rule := Rule{
			Code: item.Code, Title: item.Title, Description: item.Description, ProgressKey: item.ProgressKey,
			BronzeTarget: item.BronzeTarget, SilverTarget: item.SilverTarget, GoldTarget: item.GoldTarget, SortOrder: item.SortOrder,
		}
		if err := validateRule(rule); err != nil || !finiteNonnegative(item.Progress) || item.Tier != evaluateTier(item.Progress, rule) {
			return fmt.Errorf("result item %d is invalid", index)
		}
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
			return fmt.Errorf("result item %d has invalid tier", index)
		}
	}
	if counts != result.Summary {
		return errors.New("result summary differs from item tiers")
	}
	return nil
}

func finiteNonnegative(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0
}

func finitePositive(value float64) bool {
	return finiteNonnegative(value) && value > 0
}
