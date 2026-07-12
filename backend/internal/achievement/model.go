package achievement

import (
	"github.com/kkkzbh/AscendAny/backend/internal/analytics"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

type State string

const (
	StateNotGenerated   State = "not_generated"
	StateNoObservations State = "no_observations"
	StateReady          State = "ready"
)

type ProgressKey string

const (
	ProgressExamCount          ProgressKey = "exam_count"
	ProgressPositiveDeltaCount ProgressKey = "positive_delta_count"
	ProgressBestPositiveStreak ProgressKey = "best_positive_streak"
	ProgressKnowledgeMax       ProgressKey = "knowledge_max"
	ProgressAccuracyMax        ProgressKey = "accuracy_max"
	ProgressQualityMax         ProgressKey = "quality_max"
	ProgressFlexibilityMax     ProgressKey = "flexibility_max"
	ProgressProficiencyMax     ProgressKey = "proficiency_max"
	ProgressMaxRating          ProgressKey = "max_rating"
	ProgressMaxRatingDelta     ProgressKey = "max_rating_delta"
	ProgressTop10Count         ProgressKey = "top10_count"
	ProgressTop3Count          ProgressKey = "top3_count"
	ProgressRank1Count         ProgressKey = "rank1_count"
	ProgressMaxExamMinMetric   ProgressKey = "max_of_exam_min_metric"
	ProgressCurrentMinMetric   ProgressKey = "current_min_metric"
	ProgressAIDialogueCount    ProgressKey = "ai_dialogue_count"
)

type SelfQuery struct {
	Principal auth.AccessPrincipal
}

type Rule struct {
	Code         string
	Title        string
	Description  string
	ProgressKey  ProgressKey
	BronzeTarget float64
	SilverTarget float64
	GoldTarget   float64
	SortOrder    int64
}

type RepositorySnapshot struct {
	RuleSetVersion        int64
	RuleHeadRevision      int64
	AnalyticsHeadRevision int64
	Rules                 []Rule
	Metrics               *analytics.StudentMetrics
	AIDialogueCount       int64
}

type Result struct {
	State                 State   `json:"state"`
	AnalyticsHeadRevision int64   `json:"analyticsHeadRevision"`
	RuleSetVersion        int64   `json:"ruleSetVersion"`
	RuleHeadRevision      int64   `json:"ruleHeadRevision"`
	Summary               Summary `json:"summary"`
	Items                 []Item  `json:"items"`
}

type Summary struct {
	Total  int `json:"total"`
	Locked int `json:"locked"`
	Bronze int `json:"bronze"`
	Silver int `json:"silver"`
	Gold   int `json:"gold"`
}

type Item struct {
	Code         string      `json:"code"`
	Title        string      `json:"title"`
	Description  string      `json:"description"`
	ProgressKey  ProgressKey `json:"progressKey"`
	Tier         int         `json:"tier"`
	Progress     float64     `json:"progress"`
	BronzeTarget float64     `json:"bronzeTarget"`
	SilverTarget float64     `json:"silverTarget"`
	GoldTarget   float64     `json:"goldTarget"`
	SortOrder    int64       `json:"sortOrder"`
}
