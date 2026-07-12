package recommendation

import (
	"context"
	"encoding/json"
	"io"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/artifact"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
	"github.com/kkkzbh/AscendAny/backend/internal/recommendationprotocol"
)

const (
	TrainingBundleProtocolV2 = recommendationprotocol.TrainingBundleV2
	TrainingOutputProtocolV2 = recommendationprotocol.TrainingOutputV2
	ModelSchemaV2            = recommendationprotocol.ModelV2
	ResultSchemaV2           = recommendationprotocol.ResultV2

	TrainingBundleMediaTypeV2 = "application/vnd.ascendany.recommendation.training-bundle.v2+json"
	TrainingOutputMediaTypeV2 = "application/vnd.ascendany.recommendation.training-output.v2+json"
)

type AnalyticsProvenance struct {
	GenerationID        int64
	HeadRevision        int64
	InputManifest       json.RawMessage
	InputManifestSHA256 string
	AlgorithmVersion    string
	ConfigurationSHA256 string
}

type TrainingConfiguration struct {
	VersionID      int64
	Key            string
	VersionNumber  int64
	SchemaID       string
	Document       json.RawMessage
	DocumentSHA256 string
}

type TrainingStudent struct {
	ActorID int64
	Rating  string
	Metrics json.RawMessage
}

type TrainingProblem struct {
	SnapshotID          int64
	ProblemSetID        string
	ProblemSetProblemID string
	SourceURL           string
	Platform            string
	ProblemID           string
	Title               string
	ContentHTML         *string
	MaxScore            *string
	TimeLimitMS         *int64
	MemoryLimitBytes    *int64
}

type TrainingObservation struct {
	SnapshotID           int64
	ActorID              int64
	ProblemSetProblemID  string
	Score                *string
	MaxScore             *string
	Passed               *bool
	ValidSubmissionCount *int64
	SubmissionCount      int64
	FirstSubmittedAt     *time.Time
	LastSubmittedAt      *time.Time
}

type KnowledgeCatalogConfiguration struct {
	VersionID      int64
	Key            string
	VersionNumber  int64
	SchemaID       string
	Document       json.RawMessage
	DocumentSHA256 string
}

type TrainingDataset struct {
	Analytics        AnalyticsProvenance
	Configuration    TrainingConfiguration
	KnowledgeCatalog KnowledgeCatalogConfiguration
	Students         []TrainingStudent
	Problems         []TrainingProblem
	Observations     []TrainingObservation
}

type BuiltInputBundle struct {
	CanonicalJSON  json.RawMessage
	SHA256         string
	Manifest       json.RawMessage
	ManifestSHA256 string
	ActorIDs       []int64
}

type QueueInput struct {
	Principal                     auth.AccessPrincipal
	ConfigurationKey              string
	ExpectedAnalyticsGenerationID int64
	ExpectedAnalyticsHeadRevision int64
}

type ReviewProblemCandidate struct {
	ProblemKey        string
	SourceProblemKey  string
	ProblemFactSHA256 string
	Platform          string
	ProblemID         string
	Title             string
	SourceProblemSets []TrainingSourceProblemSet
}

type ReviewContext struct {
	AnalyticsGenerationID int64
	AnalyticsHeadRevision int64
	InputManifestSHA256   string
	Problems              []ReviewProblemCandidate
}

type QueueCommand struct {
	Principal                     auth.AccessPrincipal
	RunPublicID                   string
	ExpectedAnalyticsGenerationID int64
	ExpectedAnalyticsHeadRevision int64
	Dataset                       TrainingDataset
	Bundle                        BuiltInputBundle
	Artifact                      artifact.Artifact
	MediaType                     string
	MaximumBundleBytes            int
}

type RunStatus string

const (
	RunQueued     RunStatus = "queued"
	RunRunning    RunStatus = "running"
	RunSucceeded  RunStatus = "succeeded"
	RunSuperseded RunStatus = "superseded"
	RunFailed     RunStatus = "failed"
)

type TrainingRun struct {
	DatabaseID                     int64
	ID                             string
	SourceAnalyticsGenerationID    int64
	SourceAnalyticsHeadRevision    int64
	InputArtifact                  artifact.Artifact
	TrainingConfigurationVersionID int64
	KnowledgeCatalogVersionID      int64
	BundleProtocol                 string
	InputManifest                  json.RawMessage
	InputManifestSHA256            string
	Status                         RunStatus
	AttemptCount                   int
	CreatedAt                      time.Time
	StartedAt                      *time.Time
	FinishedAt                     *time.Time
}

type TrainingRunFailure struct {
	Code    string
	Message string
}

type TrainingRunDetail struct {
	Run                      TrainingRun
	TrainingConfigurationKey string
	Failure                  *TrainingRunFailure
}

type TrainingEvent struct {
	Sequence  int64
	Type      string
	Payload   json.RawMessage
	CreatedAt time.Time
}

type TrainingEventPage struct {
	RunID             string
	Items             []TrainingEvent
	NextAfterSequence *int64
}

type QueueResult struct {
	Run     TrainingRun
	Created bool
}

type Claim struct {
	TrainingRun
	AttemptToken   string
	LeaseOwner     string
	LeaseExpiresAt time.Time
	Reclaimed      bool
}

type ParsedInputBundle struct {
	CanonicalJSON         json.RawMessage
	Manifest              json.RawMessage
	ManifestSHA256        string
	ActorIDs              []int64
	FeatureSchema         TrainingFeatureSchema
	TrainingConfiguration ParsedTrainingConfiguration
	KnowledgeCatalog      ParsedKnowledgeCatalog
	Actors                []TrainingActorInput
	KnowledgePoints       []TrainingKnowledgePoint
	Problems              []TrainingProblemInput
	Interactions          []TrainingInteractionInput
}

type TrainingFeatureSchema struct {
	ActorFeatureIDs   []string `json:"actorFeatureIds"`
	ProblemFeatureIDs []string `json:"problemFeatureIds"`
}

type TrainingActorInput struct {
	ActorID       string          `json:"actorId"`
	CurrentRating json.RawMessage `json:"currentRating"`
	Features      []float64       `json:"features"`
}

type TrainingKnowledgePoint struct {
	ID              string   `json:"id"`
	Label           string   `json:"label"`
	Description     string   `json:"description"`
	PrerequisiteIDs []string `json:"prerequisiteIds"`
}

type TrainingSourceProblemSet struct {
	ProblemSetID string `json:"problemSetId"`
	SourceURL    string `json:"sourceUrl"`
}

type TrainingKnowledgeWeight struct {
	KnowledgePointID string      `json:"knowledgePointId"`
	Weight           json.Number `json:"weight"`
}

type TrainingProblemInput struct {
	ProblemKey           string                     `json:"problemKey"`
	SourceProblemKey     string                     `json:"sourceProblemKey"`
	ProblemFactSHA256    string                     `json:"problemFactSha256"`
	Platform             string                     `json:"platform"`
	ProblemID            string                     `json:"problemId"`
	Title                string                     `json:"title"`
	StatementText        string                     `json:"statementText"`
	SourceProblemSets    []TrainingSourceProblemSet `json:"sourceProblemSets"`
	MaxScore             json.Number                `json:"maxScore"`
	TimeLimitMS          *int64                     `json:"timeLimitMs"`
	MemoryLimitBytes     *int64                     `json:"memoryLimitBytes"`
	KnowledgeWeights     []TrainingKnowledgeWeight  `json:"knowledgeWeights"`
	Features             []float64                  `json:"features"`
	TrainActorCount      int64                      `json:"trainActorCount"`
	TrainSubmissionCount int64                      `json:"trainSubmissionCount"`
}

type TrainingInteractionInput struct {
	InteractionID        string      `json:"interactionId"`
	SnapshotID           string      `json:"snapshotId"`
	ActorID              string      `json:"actorId"`
	ProblemKey           string      `json:"problemKey"`
	FirstSubmittedAt     time.Time   `json:"firstSubmittedAt"`
	LastSubmittedAt      time.Time   `json:"lastSubmittedAt"`
	SubmissionCount      int64       `json:"submissionCount"`
	ValidSubmissionCount int64       `json:"validSubmissionCount"`
	TargetScoreRate      json.Number `json:"targetScoreRate"`
	Passed               bool        `json:"passed"`
	Split                string      `json:"split"`
}

type ParsedTrainingConfiguration struct {
	Algorithm                 string
	KnowledgeCatalogVersionID int64
	Accelerator               string
	Seed                      int64
	Epochs                    int64
	Patience                  int64
	BatchSize                 int64
	LearningRate              json.Number
	WeightDecay               json.Number
	MinTrainInteractions      int64
	MinActorInteractions      int64
	MinProblemInteractions    int64
	Validation                TrainingValidationConfiguration
	PathPolicy                TrainingPathPolicyConfiguration
	RankingWeights            TrainingRankingWeightsConfiguration
}

type TrainingValidationConfiguration struct {
	MinActors                     int64
	MinInteractions               int64
	MinRelativeLogLossImprovement json.Number
}

type TrainingPathPolicyConfiguration struct {
	TargetMastery            json.Number
	MaxKnowledgeTargets      int64
	MinSteps                 int64
	MaxSteps                 int64
	ProblemsPerStep          int64
	TargetSuccessProbability json.Number
}

type TrainingRankingWeightsConfiguration struct {
	KnowledgeGap    json.Number
	SuccessDistance json.Number
}

type ParsedKnowledgeCatalog struct {
	TaxonomyID         string
	KnowledgePoints    []TrainingKnowledgePoint
	ProblemAssignments []KnowledgeProblemAssignment
}

type KnowledgeProblemAssignment struct {
	Platform          string
	ProblemID         string
	ProblemFactSHA256 string
	Knowledge         []TrainingKnowledgeWeight
}

type ModelOutput struct {
	Schema                    string
	Manifest                  json.RawMessage
	ManifestSHA256            string
	Metrics                   json.RawMessage
	RuntimeConstructionSHA256 string
	RuntimeProvenanceSHA256   string
	RuntimeTreeSHA256         string
	HostCapabilitySHA256      string
	RuntimeAttestationSHA256  string
}

type StudentOutput struct {
	ActorID      int64
	Schema       string
	Result       json.RawMessage
	ResultSHA256 string
}

type ParsedOutputBundle struct {
	CanonicalJSON       json.RawMessage
	SHA256              string
	InputManifestSHA256 string
	Model               ModelOutput
	Results             []StudentOutput
}

type ArtifactStore interface {
	Publish(context.Context, io.Reader) (*artifact.Publication, error)
	Verify(context.Context, string, int64) (artifact.Artifact, error)
}

type QueueRepository interface {
	PrepareTraining(context.Context, auth.AccessPrincipal, string) (TrainingDataset, error)
	QueueTraining(context.Context, QueueCommand) (QueueResult, error)
}

type AdminReaderRepository interface {
	ReadReviewContext(context.Context, auth.AccessPrincipal) (ReviewContext, error)
	ReadTrainingRun(context.Context, auth.AccessPrincipal, string) (TrainingRunDetail, bool, error)
	ReadTrainingEvents(context.Context, auth.AccessPrincipal, string, int64, int) (TrainingEventPage, bool, error)
}

type PublishCommand struct {
	Claim         Claim
	ModelPublicID string
	Input         ParsedInputBundle
	Output        ParsedOutputBundle
	Artifact      artifact.Artifact
	MediaType     string
	Receipt       *TrainerAgentReceiptCommand
}

type PublishDisposition string

const (
	PublishActivated  PublishDisposition = "activated"
	PublishSuperseded PublishDisposition = "superseded"
)

type PublishResult struct {
	Disposition                PublishDisposition
	ModelID                    string
	RecommendationHeadRevision *int64
}

type TrainerAgentAttempt struct {
	RunID        string
	AgentID      string
	AttemptToken string
}

type TrainerAgentTerminalOperation string

const (
	TrainerAgentOutputOperation  TrainerAgentTerminalOperation = "output"
	TrainerAgentFailureOperation TrainerAgentTerminalOperation = "failure"
)

type TrainerAgentTerminalResult string

const (
	TrainerAgentActivated      TrainerAgentTerminalResult = "activated"
	TrainerAgentSuperseded     TrainerAgentTerminalResult = "superseded"
	TrainerAgentFailed         TrainerAgentTerminalResult = "failed"
	TrainerAgentRequeued       TrainerAgentTerminalResult = "requeued"
	TrainerAgentOutputRejected TrainerAgentTerminalResult = "output_rejected"
)

type TrainerAgentReceiptCommand struct {
	Attempt       TrainerAgentAttempt
	Operation     TrainerAgentTerminalOperation
	RequestSHA256 string
}

type TrainerAgentTerminalReceipt struct {
	Operation                 TrainerAgentTerminalOperation
	RequestSHA256             string
	Result                    TrainerAgentTerminalResult
	ModelID                   *string
	RuntimeConstructionSHA256 *string
	RuntimeProvenanceSHA256   *string
	RuntimeTreeSHA256         *string
	HostCapabilitySHA256      *string
	RuntimeAttestationSHA256  *string
	ErrorCode                 *string
	ErrorDetail               *string
	ErrorRetryable            *bool
}

type TrainerAgentFailureCommand struct {
	Claim         Claim
	RequestSHA256 string
	Code          string
	Detail        string
	Retryable     bool
	RetryDelay    time.Duration
}

type TrainerAgentOutputRejectionCommand struct {
	Claim         Claim
	RequestSHA256 string
	FailureCode   string
	FailureDetail string
	ErrorCode     string
	ErrorDetail   string
}

type TrainerAgentRepository interface {
	ClaimTraining(context.Context, string, string, time.Duration) (*Claim, error)
	RequeueTraining(context.Context, Claim, time.Duration, string) error
	FailTraining(context.Context, Claim, string, string) error
	ResolveTrainerAgentClaim(context.Context, TrainerAgentAttempt) (Claim, []int64, error)
	RenewTrainerAgentLease(context.Context, TrainerAgentAttempt, time.Duration) (time.Time, error)
	LookupTrainerAgentTerminalReceipt(context.Context, TrainerAgentAttempt, TrainerAgentTerminalOperation, string) (*TrainerAgentTerminalReceipt, error)
	ReportTrainerAgentFailure(context.Context, TrainerAgentFailureCommand) (TrainerAgentTerminalReceipt, error)
	RejectTrainerAgentOutput(context.Context, TrainerAgentOutputRejectionCommand) (TrainerAgentTerminalReceipt, error)
	PublishTrainingOutput(context.Context, PublishCommand) (PublishResult, error)
}

type RecommendationState string

const (
	RecommendationFresh       RecommendationState = "fresh"
	RecommendationStale       RecommendationState = "stale"
	RecommendationUnavailable RecommendationState = "unavailable"
)

type ModelProvenance struct {
	ModelID                        string          `json:"modelId"`
	TrainingRunID                  string          `json:"trainingRunId"`
	AnalyticsGenerationID          string          `json:"analyticsGenerationId"`
	AnalyticsHeadRevision          int64           `json:"analyticsHeadRevision"`
	InputManifestSHA256            string          `json:"inputManifestSha256"`
	TrainingConfigurationVersionID string          `json:"trainingConfigurationVersionId"`
	TrainingConfigurationKey       string          `json:"trainingConfigurationKey"`
	TrainingConfigurationVersion   int64           `json:"trainingConfigurationVersion"`
	TrainingConfigurationSchema    string          `json:"trainingConfigurationSchema"`
	TrainingConfigurationSHA256    string          `json:"trainingConfigurationSha256"`
	KnowledgeCatalogVersionID      string          `json:"knowledgeCatalogVersionId"`
	KnowledgeCatalogKey            string          `json:"knowledgeCatalogKey"`
	KnowledgeCatalogVersion        int64           `json:"knowledgeCatalogVersion"`
	KnowledgeCatalogSchema         string          `json:"knowledgeCatalogSchema"`
	KnowledgeCatalogSHA256         string          `json:"knowledgeCatalogSha256"`
	OutputArtifactSHA256           string          `json:"outputArtifactSha256"`
	ModelSchema                    string          `json:"modelSchema"`
	ModelManifest                  json.RawMessage `json:"modelManifest"`
	ModelManifestSHA256            string          `json:"modelManifestSha256"`
	Metrics                        json.RawMessage `json:"metrics"`
	CreatedAt                      time.Time       `json:"createdAt"`
}

type RecommendationResultStatus string

const (
	RecommendationResultReady        RecommendationResultStatus = "ready"
	RecommendationResultInsufficient RecommendationResultStatus = "insufficient"
)

type RecommendationEvidenceV2 struct {
	TrainInteractionCount      int64 `json:"trainInteractionCount"`
	ValidationInteractionCount int64 `json:"validationInteractionCount"`
	DistinctProblemCount       int64 `json:"distinctProblemCount"`
	PassedProblemCount         int64 `json:"passedProblemCount"`
}

type RecommendationKnowledgeMasteryV2 struct {
	KnowledgePointID      string      `json:"knowledgePointId"`
	Label                 string      `json:"label"`
	Description           string      `json:"description"`
	PrerequisiteIDs       []string    `json:"prerequisiteIds"`
	Mastery               json.Number `json:"mastery"`
	TrainInteractionCount int64       `json:"trainInteractionCount"`
}

type RecommendationRankingEvidenceV2 struct {
	KnowledgeGap        json.Number `json:"knowledgeGap"`
	SuccessDistance     json.Number `json:"successDistance"`
	StepKnowledgeWeight json.Number `json:"stepKnowledgeWeight"`
}

type RecommendationProblemV2 struct {
	ProblemKey                  string                          `json:"problemKey"`
	SourceProblemKey            string                          `json:"sourceProblemKey"`
	Platform                    string                          `json:"platform"`
	ProblemID                   string                          `json:"problemId"`
	Title                       string                          `json:"title"`
	SourceProblemSets           []TrainingSourceProblemSet      `json:"sourceProblemSets"`
	PredictedSuccessProbability json.Number                     `json:"predictedSuccessProbability"`
	RecommendationScore         json.Number                     `json:"recommendationScore"`
	RankingEvidence             RecommendationRankingEvidenceV2 `json:"rankingEvidence"`
}

type RecommendationLearningPathStepV2 struct {
	Order               int64                     `json:"order"`
	KnowledgePointID    string                    `json:"knowledgePointId"`
	Label               string                    `json:"label"`
	Description         string                    `json:"description"`
	PrerequisiteIDs     []string                  `json:"prerequisiteIds"`
	Mastery             json.Number               `json:"mastery"`
	TargetMastery       json.Number               `json:"targetMastery"`
	ReasonCode          string                    `json:"reasonCode"`
	RecommendedProblems []RecommendationProblemV2 `json:"recommendedProblems"`
}

type RecommendationInsufficiencyV2 struct {
	ReasonCode               string   `json:"reasonCode"`
	MinimumPathSteps         int64    `json:"minimumPathSteps"`
	CandidatePathSteps       int64    `json:"candidatePathSteps"`
	ProblemsPerStep          int64    `json:"problemsPerStep"`
	EligibleProblemCount     int64    `json:"eligibleProblemCount"`
	BlockedKnowledgePointIDs []string `json:"blockedKnowledgePointIds"`
}

type StudentRecommendationResultV2 struct {
	Schema           string                             `json:"schema"`
	SHA256           string                             `json:"sha256"`
	Status           RecommendationResultStatus         `json:"status"`
	SourceRating     json.Number                        `json:"sourceRating"`
	Evidence         RecommendationEvidenceV2           `json:"evidence"`
	KnowledgeMastery []RecommendationKnowledgeMasteryV2 `json:"knowledgeMastery"`
	LearningPath     []RecommendationLearningPathStepV2 `json:"learningPath,omitempty"`
	Insufficiency    *RecommendationInsufficiencyV2     `json:"insufficiency,omitempty"`
}

type CurrentRecommendation struct {
	State                        RecommendationState            `json:"state"`
	UnavailableReason            *string                        `json:"unavailableReason,omitempty"`
	CurrentAnalyticsGenerationID *string                        `json:"currentAnalyticsGenerationId,omitempty"`
	CurrentAnalyticsHeadRevision int64                          `json:"currentAnalyticsHeadRevision"`
	RecommendationHeadRevision   int64                          `json:"recommendationHeadRevision"`
	Model                        *ModelProvenance               `json:"model,omitempty"`
	Result                       *StudentRecommendationResultV2 `json:"result,omitempty"`
}

type ReaderRepository interface {
	ReadCurrent(context.Context, auth.AccessPrincipal) (CurrentRecommendation, error)
}
