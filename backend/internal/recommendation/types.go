package recommendation

const (
	ResultSchemaV1 = "ascendany.recommendation.inference-result.v1"

	RecommendationFresh       RecommendationState = "fresh"
	RecommendationUnavailable RecommendationState = "unavailable"

	RecommendationResultReady        RecommendationResultStatus = "ready"
	RecommendationResultInsufficient RecommendationResultStatus = "insufficient"
)

type RecommendationState string
type RecommendationResultStatus string
type UnavailableReason string

const (
	UnavailableAnalytics       UnavailableReason = "analytics_unavailable"
	UnavailableActorAnalytics  UnavailableReason = "actor_analytics_unavailable"
	UnavailableKnowledge       UnavailableReason = "knowledge_catalog_unavailable"
	UnavailableKnowledgeMatch  UnavailableReason = "knowledge_catalog_mismatch"
	UnavailableEligibleProblem UnavailableReason = "eligible_problems_unavailable"
)

type CurrentRecommendation struct {
	State                        RecommendationState                   `json:"state"`
	UnavailableReason            *UnavailableReason                    `json:"unavailableReason,omitempty"`
	CurrentAnalyticsGenerationID *string                               `json:"currentAnalyticsGenerationId,omitempty"`
	CurrentAnalyticsHeadRevision int64                                 `json:"currentAnalyticsHeadRevision"`
	ModelHeadRevision            int64                                 `json:"modelHeadRevision"`
	Model                        *ModelProvenance                      `json:"model"`
	Result                       *StudentRecommendationInferenceResult `json:"result,omitempty"`
}

type ModelProvenance struct {
	ModelID                  string `json:"modelId"`
	Purpose                  string `json:"purpose"`
	ArtifactSHA256           string `json:"artifactSha256"`
	ArtifactSizeBytes        int64  `json:"artifactSizeBytes"`
	ArtifactMode             int64  `json:"artifactMode"`
	ModelSchema              string `json:"modelSchema"`
	Algorithm                string `json:"algorithm"`
	InferenceContract        string `json:"inferenceContract"`
	TrainedAt                string `json:"trainedAt"`
	TrainingProvenanceSHA256 string `json:"trainingProvenanceSha256"`
	FeatureSchemaSHA256      string `json:"featureSchemaSha256"`
	KnowledgeCatalogSHA256   string `json:"knowledgeCatalogSha256"`
	ParameterSHA256          string `json:"parameterSha256"`
	GoldenVectorsSHA256      string `json:"goldenVectorsSha256"`
	ModelHeadRevision        int64  `json:"modelHeadRevision"`
	ApplicationVersion       string `json:"applicationVersion"`
	ApplicationCommit        string `json:"applicationCommit"`
	ApplicationBuildTime     string `json:"applicationBuildTime"`
}

type StudentRecommendationInferenceResult struct {
	Schema           string                           `json:"schema"`
	SHA256           string                           `json:"sha256"`
	Status           RecommendationResultStatus       `json:"status"`
	SourceRating     float64                          `json:"sourceRating"`
	Evidence         RecommendationInferenceEvidence  `json:"evidence"`
	KnowledgeMastery []RecommendationKnowledgeMastery `json:"knowledgeMastery"`
	LearningPath     []RecommendationLearningPathStep `json:"learningPath,omitempty"`
	Insufficiency    *RecommendationInsufficiency     `json:"insufficiency,omitempty"`
}

type RecommendationInferenceEvidence struct {
	ObservationCount     int64 `json:"observationCount"`
	DistinctProblemCount int64 `json:"distinctProblemCount"`
	PassedProblemCount   int64 `json:"passedProblemCount"`
}

type RecommendationKnowledgeMastery struct {
	KnowledgePointID string   `json:"knowledgePointId"`
	Label            string   `json:"label"`
	Description      string   `json:"description"`
	PrerequisiteIDs  []string `json:"prerequisiteIds"`
	Mastery          float64  `json:"mastery"`
	ObservationCount int64    `json:"observationCount"`
}

type RecommendationLearningPathStep struct {
	Order               int64                   `json:"order"`
	KnowledgePointID    string                  `json:"knowledgePointId"`
	Label               string                  `json:"label"`
	Description         string                  `json:"description"`
	PrerequisiteIDs     []string                `json:"prerequisiteIds"`
	Mastery             float64                 `json:"mastery"`
	TargetMastery       float64                 `json:"targetMastery"`
	ReasonCode          string                  `json:"reasonCode"`
	RecommendedProblems []RecommendationProblem `json:"recommendedProblems"`
}

type RecommendationProblem struct {
	ProblemKey                  string                        `json:"problemKey"`
	SourceProblemKey            string                        `json:"sourceProblemKey"`
	Platform                    string                        `json:"platform"`
	ProblemID                   string                        `json:"problemId"`
	Title                       string                        `json:"title"`
	SourceProblemSets           []RecommendationSourceSet     `json:"sourceProblemSets"`
	PredictedSuccessProbability float64                       `json:"predictedSuccessProbability"`
	RecommendationScore         float64                       `json:"recommendationScore"`
	RankingEvidence             RecommendationRankingEvidence `json:"rankingEvidence"`
}

type RecommendationSourceSet struct {
	ProblemSetID string `json:"problemSetId"`
	SourceURL    string `json:"sourceUrl"`
}

type RecommendationRankingEvidence struct {
	KnowledgeGap        float64 `json:"knowledgeGap"`
	SuccessDistance     float64 `json:"successDistance"`
	StepKnowledgeWeight float64 `json:"stepKnowledgeWeight"`
}

type RecommendationInsufficiency struct {
	ReasonCode               string   `json:"reasonCode"`
	MinimumPathSteps         int64    `json:"minimumPathSteps"`
	CandidatePathSteps       int64    `json:"candidatePathSteps"`
	ProblemsPerStep          int64    `json:"problemsPerStep"`
	EligibleProblemCount     int64    `json:"eligibleProblemCount"`
	BlockedKnowledgePointIDs []string `json:"blockedKnowledgePointIds"`
}

type ReviewContext struct {
	AnalyticsGenerationID int64                    `json:"analyticsGenerationId"`
	AnalyticsHeadRevision int64                    `json:"analyticsHeadRevision"`
	InputManifestSHA256   string                   `json:"inputManifestSha256"`
	Problems              []ReviewProblemCandidate `json:"problems"`
}

type ReviewProblemCandidate struct {
	ProblemKey        string                    `json:"problemKey"`
	SourceProblemKey  string                    `json:"sourceProblemKey"`
	Platform          string                    `json:"platform"`
	ProblemID         string                    `json:"problemId"`
	ProblemFactSHA256 string                    `json:"problemFactSha256"`
	Title             string                    `json:"title"`
	SourceProblemSets []RecommendationSourceSet `json:"sourceProblemSets"`
}
