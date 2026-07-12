package recommendation

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/canonicaljson"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

const (
	trainingConfigurationSchemaV2 = "ascendany.training.recommendation.v2"
	knowledgeCatalogSchemaV1      = "ascendany.knowledge_catalog.recommendation.v1"
	trainingAlgorithmV2           = "knowledge_mirt_v1"

	maximumTrainingActors       = 20_000
	maximumTrainingProblems     = 10_000
	maximumTrainingInteractions = 200_000
	maximumKnowledgePoints      = 1_024
	maximumStatementBytes       = 1 << 20
	maximumStatementTotalBytes  = 32 << 20
	maximumContentHTMLBytes     = 4 << 20
)

var trainingFeatureSchemaV2 = TrainingFeatureSchema{
	ActorFeatureIDs: []string{
		"log1p_train_interaction_count",
		"train_pass_rate_beta11",
		"train_mean_score_rate",
		"train_mean_log1p_submission_count",
	},
	ProblemFeatureIDs: []string{
		"train_acceptance_logit_beta11",
		"log1p_train_actor_count",
		"log1p_train_submission_count",
	},
}

type inputManifestWire struct {
	Protocol                   string                     `json:"protocol"`
	Source                     inputManifestSource        `json:"source"`
	TrainingConfiguration      inputManifestConfiguration `json:"trainingConfiguration"`
	KnowledgeCatalog           inputManifestConfiguration `json:"knowledgeCatalog"`
	FeatureSchemaSHA256        string                     `json:"featureSchemaSha256"`
	KnowledgePointCount        int                        `json:"knowledgePointCount"`
	KnowledgePointSetSHA256    string                     `json:"knowledgePointSetSha256"`
	ActorCount                 int                        `json:"actorCount"`
	ActorSetSHA256             string                     `json:"actorSetSha256"`
	ProblemCount               int                        `json:"problemCount"`
	ProblemSetSHA256           string                     `json:"problemSetSha256"`
	InteractionCount           int                        `json:"interactionCount"`
	InteractionSetSHA256       string                     `json:"interactionSetSha256"`
	TrainInteractionCount      int                        `json:"trainInteractionCount"`
	ValidationInteractionCount int                        `json:"validationInteractionCount"`
	SplitSHA256                string                     `json:"splitSha256"`
}

type inputManifestSource struct {
	AnalyticsGenerationID        string `json:"analyticsGenerationId"`
	AnalyticsHeadRevision        int64  `json:"analyticsHeadRevision"`
	AnalyticsInputManifestSHA256 string `json:"analyticsInputManifestSha256"`
	AlgorithmVersion             string `json:"algorithmVersion"`
	AnalyticsConfigurationSHA256 string `json:"analyticsConfigurationSha256"`
}

type inputManifestConfiguration struct {
	VersionID      string `json:"versionId"`
	Key            string `json:"key"`
	VersionNumber  int64  `json:"versionNumber"`
	SchemaID       string `json:"schemaId"`
	DocumentSHA256 string `json:"documentSha256"`
}

type inputBundleWire struct {
	Protocol               string                     `json:"protocol"`
	Manifest               json.RawMessage            `json:"manifest"`
	AnalyticsInputManifest json.RawMessage            `json:"analyticsInputManifest"`
	TrainingConfiguration  inputBundleConfiguration   `json:"trainingConfiguration"`
	KnowledgeCatalog       inputBundleConfiguration   `json:"knowledgeCatalog"`
	FeatureSchema          TrainingFeatureSchema      `json:"featureSchema"`
	KnowledgePoints        []TrainingKnowledgePoint   `json:"knowledgePoints"`
	Actors                 []TrainingActorInput       `json:"actors"`
	Problems               []TrainingProblemInput     `json:"problems"`
	Interactions           []TrainingInteractionInput `json:"interactions"`
}

type inputBundleConfiguration struct {
	SchemaID string          `json:"schemaId"`
	Document json.RawMessage `json:"document"`
}

type actorSetWire struct {
	ActorIDs []string `json:"actorIds"`
}

type knowledgePointSetWire struct {
	KnowledgePointIDs []string `json:"knowledgePointIds"`
}

type problemSetWire struct {
	ProblemKeys []string `json:"problemKeys"`
}

type interactionSetWire struct {
	InteractionIDs []string `json:"interactionIds"`
}

type splitSetWire struct {
	Interactions []splitEntryWire `json:"interactions"`
}

type splitEntryWire struct {
	InteractionID string `json:"interactionId"`
	Split         string `json:"split"`
}

type interactionIdentityWire struct {
	ActorID    string `json:"actorId"`
	ProblemKey string `json:"problemKey"`
	SnapshotID string `json:"snapshotId"`
}

type rawTrainingConfiguration struct {
	Algorithm                 *string                         `json:"algorithm"`
	KnowledgeCatalogVersionID *string                         `json:"knowledgeCatalogVersionId"`
	Accelerator               *string                         `json:"accelerator"`
	Seed                      *int64                          `json:"seed"`
	Epochs                    *int64                          `json:"epochs"`
	Patience                  *int64                          `json:"patience"`
	BatchSize                 *int64                          `json:"batchSize"`
	LearningRate              *json.Number                    `json:"learningRate"`
	WeightDecay               *json.Number                    `json:"weightDecay"`
	MinTrainInteractions      *int64                          `json:"minTrainInteractions"`
	MinActorInteractions      *int64                          `json:"minActorInteractions"`
	MinProblemInteractions    *int64                          `json:"minProblemInteractions"`
	Validation                *rawValidationConfiguration     `json:"validation"`
	PathPolicy                *rawPathPolicyConfiguration     `json:"pathPolicy"`
	RankingWeights            *rawRankingWeightsConfiguration `json:"rankingWeights"`
}

type rawValidationConfiguration struct {
	MinActors                     *int64       `json:"minActors"`
	MinInteractions               *int64       `json:"minInteractions"`
	MinRelativeLogLossImprovement *json.Number `json:"minRelativeLogLossImprovement"`
}

type rawPathPolicyConfiguration struct {
	TargetMastery            *json.Number `json:"targetMastery"`
	MaxKnowledgeTargets      *int64       `json:"maxKnowledgeTargets"`
	MinSteps                 *int64       `json:"minSteps"`
	MaxSteps                 *int64       `json:"maxSteps"`
	ProblemsPerStep          *int64       `json:"problemsPerStep"`
	TargetSuccessProbability *json.Number `json:"targetSuccessProbability"`
}

type rawRankingWeightsConfiguration struct {
	KnowledgeGap    *json.Number `json:"knowledgeGap"`
	SuccessDistance *json.Number `json:"successDistance"`
}

type rawKnowledgeCatalog struct {
	TaxonomyID         *string                          `json:"taxonomyId"`
	KnowledgePoints    *[]rawKnowledgePoint             `json:"knowledgePoints"`
	ProblemAssignments *[]rawKnowledgeProblemAssignment `json:"problemAssignments"`
}

type rawKnowledgePoint struct {
	ID              *string   `json:"id"`
	Label           *string   `json:"label"`
	Description     *string   `json:"description"`
	PrerequisiteIDs *[]string `json:"prerequisiteIds"`
}

type rawKnowledgeProblemAssignment struct {
	Platform          *string                       `json:"platform"`
	ProblemID         *string                       `json:"problemId"`
	ProblemFactSHA256 *string                       `json:"problemFactSha256"`
	Knowledge         *[]rawTrainingKnowledgeWeight `json:"knowledge"`
}

type rawTrainingKnowledgeWeight struct {
	KnowledgePointID *string      `json:"knowledgePointId"`
	Weight           *json.Number `json:"weight"`
}

type preparedProblem struct {
	wire TrainingProblemInput
}

// BuildInputBundle is the only owner of feature engineering and the
// deterministic train/validation split sent to the isolated trainer.
func BuildInputBundle(dataset TrainingDataset, maximumBytes int) (BuiltInputBundle, error) {
	if maximumBytes <= 0 {
		return BuiltInputBundle{}, domainError(ErrorInvalidConfiguration, true, "build training input bundle", errors.New("positive maximum bundle bytes are required"))
	}
	trainingConfiguration, knowledgeCatalog, err := validateDatasetProvenance(dataset)
	if err != nil {
		return BuiltInputBundle{}, err
	}
	actors, actorIDs, actorIndex, err := prepareActors(dataset.Students)
	if err != nil {
		return BuiltInputBundle{}, err
	}
	problems, problemBySnapshot, err := prepareProblems(dataset.Problems, knowledgeCatalog)
	if err != nil {
		return BuiltInputBundle{}, err
	}
	interactions, err := prepareInteractions(dataset.Observations, actorIndex, problemBySnapshot, problems)
	if err != nil {
		return BuiltInputBundle{}, err
	}
	if err := assignTrainingSplit(interactions, trainingConfiguration); err != nil {
		return BuiltInputBundle{}, err
	}
	if err := applyTrainingFeatures(actors, problems, interactions, trainingConfiguration); err != nil {
		return BuiltInputBundle{}, err
	}

	featureSchema := TrainingFeatureSchema{
		ActorFeatureIDs:   slices.Clone(trainingFeatureSchemaV2.ActorFeatureIDs),
		ProblemFeatureIDs: slices.Clone(trainingFeatureSchemaV2.ProblemFeatureIDs),
	}
	featureSchemaSHA256, err := hashCanonicalValue(featureSchema, maxManifestBytes, "feature schema")
	if err != nil {
		return BuiltInputBundle{}, err
	}
	knowledgePointIDs := make([]string, len(knowledgeCatalog.KnowledgePoints))
	for index := range knowledgeCatalog.KnowledgePoints {
		knowledgePointIDs[index] = knowledgeCatalog.KnowledgePoints[index].ID
	}
	actorIDStrings := make([]string, len(actorIDs))
	for index, actorID := range actorIDs {
		actorIDStrings[index] = strconv.FormatInt(actorID, 10)
	}
	problemKeys := make([]string, len(problems))
	for index := range problems {
		problemKeys[index] = problems[index].wire.ProblemKey
	}
	interactionIDs := make([]string, len(interactions))
	splits := make([]splitEntryWire, len(interactions))
	trainCount := 0
	for index := range interactions {
		interactionIDs[index] = interactions[index].InteractionID
		splits[index] = splitEntryWire{InteractionID: interactions[index].InteractionID, Split: interactions[index].Split}
		if interactions[index].Split == "train" {
			trainCount++
		}
	}
	knowledgeSetSHA256, err := hashCanonicalValue(knowledgePointSetWire{KnowledgePointIDs: knowledgePointIDs}, maxManifestBytes, "knowledge point set")
	if err != nil {
		return BuiltInputBundle{}, err
	}
	actorSetSHA256, err := hashCanonicalValue(actorSetWire{ActorIDs: actorIDStrings}, maxManifestBytes, "actor set")
	if err != nil {
		return BuiltInputBundle{}, err
	}
	problemSetSHA256, err := hashCanonicalValue(problemSetWire{ProblemKeys: problemKeys}, maxManifestBytes, "problem set")
	if err != nil {
		return BuiltInputBundle{}, err
	}
	interactionSetSHA256, err := hashCanonicalValue(interactionSetWire{InteractionIDs: interactionIDs}, maxManifestBytes, "interaction set")
	if err != nil {
		return BuiltInputBundle{}, err
	}
	splitSHA256, err := hashCanonicalValue(splitSetWire{Interactions: splits}, maxManifestBytes, "interaction split")
	if err != nil {
		return BuiltInputBundle{}, err
	}

	manifestValue := inputManifestWire{
		Protocol: TrainingBundleProtocolV2,
		Source: inputManifestSource{
			AnalyticsGenerationID:        strconv.FormatInt(dataset.Analytics.GenerationID, 10),
			AnalyticsHeadRevision:        dataset.Analytics.HeadRevision,
			AnalyticsInputManifestSHA256: dataset.Analytics.InputManifestSHA256,
			AlgorithmVersion:             dataset.Analytics.AlgorithmVersion,
			AnalyticsConfigurationSHA256: dataset.Analytics.ConfigurationSHA256,
		},
		TrainingConfiguration: manifestConfiguration(dataset.Configuration),
		KnowledgeCatalog:      manifestCatalogConfiguration(dataset.KnowledgeCatalog),
		FeatureSchemaSHA256:   featureSchemaSHA256,
		KnowledgePointCount:   len(knowledgePointIDs), KnowledgePointSetSHA256: knowledgeSetSHA256,
		ActorCount: len(actorIDs), ActorSetSHA256: actorSetSHA256,
		ProblemCount: len(problemKeys), ProblemSetSHA256: problemSetSHA256,
		InteractionCount: len(interactionIDs), InteractionSetSHA256: interactionSetSHA256,
		TrainInteractionCount:      trainCount,
		ValidationInteractionCount: len(interactions) - trainCount,
		SplitSHA256:                splitSHA256,
	}
	manifestBytes, err := json.Marshal(manifestValue)
	if err != nil {
		return BuiltInputBundle{}, domainError(ErrorInvalidBundle, true, "encode training input manifest", err)
	}
	manifest, manifestSHA256, err := canonicaljson.Object(manifestBytes, maxManifestBytes)
	if err != nil {
		return BuiltInputBundle{}, domainError(ErrorInvalidBundle, true, "canonicalize training input manifest", err)
	}
	analyticsManifest, _, err := canonicaljson.Object(dataset.Analytics.InputManifest, maxManifestBytes)
	if err != nil {
		return BuiltInputBundle{}, domainError(ErrorStoredDataInvalid, true, "canonicalize analytics input manifest", err)
	}
	wireProblems := make([]TrainingProblemInput, len(problems))
	for index := range problems {
		wireProblems[index] = problems[index].wire
	}
	bundleBytes, err := json.Marshal(inputBundleWire{
		Protocol: TrainingBundleProtocolV2, Manifest: manifest, AnalyticsInputManifest: analyticsManifest,
		TrainingConfiguration: inputBundleConfiguration{SchemaID: dataset.Configuration.SchemaID, Document: dataset.Configuration.Document},
		KnowledgeCatalog:      inputBundleConfiguration{SchemaID: dataset.KnowledgeCatalog.SchemaID, Document: dataset.KnowledgeCatalog.Document},
		FeatureSchema:         featureSchema, KnowledgePoints: slices.Clone(knowledgeCatalog.KnowledgePoints),
		Actors: actors, Problems: wireProblems, Interactions: interactions,
	})
	if err != nil {
		return BuiltInputBundle{}, domainError(ErrorInvalidBundle, true, "encode training input bundle", err)
	}
	canonical, digest, err := canonicaljson.Object(bundleBytes, maximumBytes)
	if err != nil {
		return BuiltInputBundle{}, domainError(ErrorInvalidBundle, true, "canonicalize training input bundle", err)
	}
	return BuiltInputBundle{CanonicalJSON: canonical, SHA256: digest, Manifest: manifest, ManifestSHA256: manifestSHA256, ActorIDs: actorIDs}, nil
}

func manifestConfiguration(value TrainingConfiguration) inputManifestConfiguration {
	return inputManifestConfiguration{
		VersionID: strconv.FormatInt(value.VersionID, 10), Key: value.Key, VersionNumber: value.VersionNumber,
		SchemaID: value.SchemaID, DocumentSHA256: value.DocumentSHA256,
	}
}

func manifestCatalogConfiguration(value KnowledgeCatalogConfiguration) inputManifestConfiguration {
	return inputManifestConfiguration{
		VersionID: strconv.FormatInt(value.VersionID, 10), Key: value.Key, VersionNumber: value.VersionNumber,
		SchemaID: value.SchemaID, DocumentSHA256: value.DocumentSHA256,
	}
}

func validateDatasetProvenance(dataset TrainingDataset) (ParsedTrainingConfiguration, ParsedKnowledgeCatalog, error) {
	analytics := dataset.Analytics
	if analytics.GenerationID <= 0 || analytics.HeadRevision <= 0 ||
		!lowercaseSHA256Pattern.MatchString(analytics.InputManifestSHA256) ||
		!lowercaseSHA256Pattern.MatchString(analytics.ConfigurationSHA256) ||
		analytics.AlgorithmVersion == "" || strings.TrimSpace(analytics.AlgorithmVersion) != analytics.AlgorithmVersion {
		return ParsedTrainingConfiguration{}, ParsedKnowledgeCatalog{}, domainError(ErrorStoredDataInvalid, true, "validate analytics provenance", errors.New("analytics provenance columns are invalid"))
	}
	_, analyticsHash, err := canonicaljson.Object(analytics.InputManifest, maxManifestBytes)
	if err != nil || analyticsHash != analytics.InputManifestSHA256 {
		return ParsedTrainingConfiguration{}, ParsedKnowledgeCatalog{}, domainError(ErrorStoredDataInvalid, true, "validate analytics provenance", errors.New("analytics input manifest is noncanonical or its hash differs"))
	}
	if err := validateConfigurationProvenance(
		dataset.Configuration.VersionID, dataset.Configuration.Key, dataset.Configuration.VersionNumber,
		dataset.Configuration.SchemaID, dataset.Configuration.Document, dataset.Configuration.DocumentSHA256,
		trainingConfigurationSchemaV2, "training configuration",
	); err != nil {
		return ParsedTrainingConfiguration{}, ParsedKnowledgeCatalog{}, err
	}
	if err := validateConfigurationProvenance(
		dataset.KnowledgeCatalog.VersionID, dataset.KnowledgeCatalog.Key, dataset.KnowledgeCatalog.VersionNumber,
		dataset.KnowledgeCatalog.SchemaID, dataset.KnowledgeCatalog.Document, dataset.KnowledgeCatalog.DocumentSHA256,
		knowledgeCatalogSchemaV1, "knowledge catalog",
	); err != nil {
		return ParsedTrainingConfiguration{}, ParsedKnowledgeCatalog{}, err
	}
	training, err := parseTrainingConfiguration(dataset.Configuration.Document)
	if err != nil {
		return ParsedTrainingConfiguration{}, ParsedKnowledgeCatalog{}, preflightFailure("training_configuration_invalid", nil)
	}
	if training.KnowledgeCatalogVersionID != dataset.KnowledgeCatalog.VersionID {
		return ParsedTrainingConfiguration{}, ParsedKnowledgeCatalog{}, preflightFailure("knowledge_catalog_reference_changed", nil)
	}
	catalog, err := parseKnowledgeCatalog(dataset.KnowledgeCatalog.Document)
	if err != nil {
		return ParsedTrainingConfiguration{}, ParsedKnowledgeCatalog{}, preflightFailure("knowledge_catalog_invalid", nil)
	}
	return training, catalog, nil
}

func validateConfigurationProvenance(
	versionID int64,
	key string,
	versionNumber int64,
	schemaID string,
	document json.RawMessage,
	documentSHA256 string,
	expectedSchema string,
	label string,
) error {
	if versionID <= 0 || versionNumber <= 0 || !configurationKeyPattern.MatchString(key) ||
		schemaID != expectedSchema || !lowercaseSHA256Pattern.MatchString(documentSHA256) {
		return domainError(ErrorStoredDataInvalid, true, "validate "+label, errors.New(label+" provenance columns are invalid"))
	}
	_, digest, err := canonicaljson.Object(document, maxConfigurationBytes)
	if err != nil || digest != documentSHA256 {
		return domainError(ErrorStoredDataInvalid, true, "validate "+label, errors.New(label+" document is noncanonical or its hash differs"))
	}
	return nil
}

func parseTrainingConfiguration(document json.RawMessage) (ParsedTrainingConfiguration, error) {
	var raw rawTrainingConfiguration
	if err := decodeStrict(document, &raw); err != nil {
		return ParsedTrainingConfiguration{}, err
	}
	if raw.Algorithm == nil || raw.KnowledgeCatalogVersionID == nil || raw.Accelerator == nil || raw.Seed == nil ||
		raw.Epochs == nil || raw.Patience == nil || raw.BatchSize == nil || raw.LearningRate == nil ||
		raw.WeightDecay == nil || raw.MinTrainInteractions == nil || raw.MinActorInteractions == nil ||
		raw.MinProblemInteractions == nil || raw.Validation == nil || raw.PathPolicy == nil || raw.RankingWeights == nil {
		return ParsedTrainingConfiguration{}, errors.New("every training configuration field is required")
	}
	if *raw.Algorithm != trainingAlgorithmV2 {
		return ParsedTrainingConfiguration{}, fmt.Errorf("algorithm must be %q", trainingAlgorithmV2)
	}
	catalogVersionID, err := parseCanonicalID(*raw.KnowledgeCatalogVersionID)
	if err != nil {
		return ParsedTrainingConfiguration{}, fmt.Errorf("knowledgeCatalogVersionId: %w", err)
	}
	if *raw.Accelerator != "cuda" {
		return ParsedTrainingConfiguration{}, errors.New("accelerator must be cuda")
	}
	if *raw.Seed < 0 || *raw.Seed > math.MaxInt32 || *raw.Epochs < 1 || *raw.Epochs > 10_000 ||
		*raw.Patience < 1 || *raw.Patience > *raw.Epochs || *raw.MinTrainInteractions < 2 ||
		*raw.MinTrainInteractions > maximumTrainingInteractions || *raw.BatchSize < 1 ||
		*raw.BatchSize > *raw.MinTrainInteractions || *raw.MinActorInteractions < 2 ||
		*raw.MinActorInteractions > maximumTrainingInteractions || *raw.MinProblemInteractions < 1 ||
		*raw.MinProblemInteractions > maximumTrainingInteractions {
		return ParsedTrainingConfiguration{}, errors.New("integer training configuration fields are outside their contract bounds")
	}
	if err := requireNumberRange(*raw.LearningRate, false, "1", true, "learningRate"); err != nil {
		return ParsedTrainingConfiguration{}, err
	}
	if err := requireNumberRange(*raw.WeightDecay, true, "1", false, "weightDecay"); err != nil {
		return ParsedTrainingConfiguration{}, err
	}
	validation, err := parseValidationConfiguration(*raw.Validation)
	if err != nil {
		return ParsedTrainingConfiguration{}, err
	}
	pathPolicy, err := parsePathPolicyConfiguration(*raw.PathPolicy)
	if err != nil {
		return ParsedTrainingConfiguration{}, err
	}
	rankingWeights, err := parseRankingWeightsConfiguration(*raw.RankingWeights)
	if err != nil {
		return ParsedTrainingConfiguration{}, err
	}
	return ParsedTrainingConfiguration{
		Algorithm: *raw.Algorithm, KnowledgeCatalogVersionID: catalogVersionID, Accelerator: *raw.Accelerator,
		Seed: *raw.Seed, Epochs: *raw.Epochs, Patience: *raw.Patience, BatchSize: *raw.BatchSize,
		LearningRate: *raw.LearningRate, WeightDecay: *raw.WeightDecay,
		MinTrainInteractions: *raw.MinTrainInteractions, MinActorInteractions: *raw.MinActorInteractions,
		MinProblemInteractions: *raw.MinProblemInteractions, Validation: validation, PathPolicy: pathPolicy,
		RankingWeights: rankingWeights,
	}, nil
}

func parseValidationConfiguration(raw rawValidationConfiguration) (TrainingValidationConfiguration, error) {
	if raw.MinActors == nil || raw.MinInteractions == nil || raw.MinRelativeLogLossImprovement == nil {
		return TrainingValidationConfiguration{}, errors.New("every validation field is required")
	}
	if *raw.MinActors < 1 || *raw.MinActors > maximumTrainingActors || *raw.MinInteractions < 1 ||
		*raw.MinInteractions > maximumTrainingInteractions {
		return TrainingValidationConfiguration{}, errors.New("validation counts are outside their contract bounds")
	}
	if err := requireNumberRange(*raw.MinRelativeLogLossImprovement, true, "1", false, "validation.minRelativeLogLossImprovement"); err != nil {
		return TrainingValidationConfiguration{}, err
	}
	return TrainingValidationConfiguration{
		MinActors: *raw.MinActors, MinInteractions: *raw.MinInteractions,
		MinRelativeLogLossImprovement: *raw.MinRelativeLogLossImprovement,
	}, nil
}

func parsePathPolicyConfiguration(raw rawPathPolicyConfiguration) (TrainingPathPolicyConfiguration, error) {
	if raw.TargetMastery == nil || raw.MaxKnowledgeTargets == nil || raw.MinSteps == nil || raw.MaxSteps == nil ||
		raw.ProblemsPerStep == nil || raw.TargetSuccessProbability == nil {
		return TrainingPathPolicyConfiguration{}, errors.New("every pathPolicy field is required")
	}
	if err := requireNumberRange(*raw.TargetMastery, false, "1", false, "pathPolicy.targetMastery"); err != nil {
		return TrainingPathPolicyConfiguration{}, err
	}
	if err := requireNumberRange(*raw.TargetSuccessProbability, false, "1", false, "pathPolicy.targetSuccessProbability"); err != nil {
		return TrainingPathPolicyConfiguration{}, err
	}
	if *raw.MaxKnowledgeTargets < 1 || *raw.MaxKnowledgeTargets > maximumKnowledgePoints ||
		*raw.MinSteps < 2 || *raw.MinSteps > *raw.MaxSteps || *raw.MaxSteps > 8 ||
		*raw.ProblemsPerStep < 1 || *raw.ProblemsPerStep > 20 {
		return TrainingPathPolicyConfiguration{}, errors.New("pathPolicy integer fields are outside their contract bounds")
	}
	return TrainingPathPolicyConfiguration{
		TargetMastery: *raw.TargetMastery, MaxKnowledgeTargets: *raw.MaxKnowledgeTargets,
		MinSteps: *raw.MinSteps, MaxSteps: *raw.MaxSteps, ProblemsPerStep: *raw.ProblemsPerStep,
		TargetSuccessProbability: *raw.TargetSuccessProbability,
	}, nil
}

func parseRankingWeightsConfiguration(raw rawRankingWeightsConfiguration) (TrainingRankingWeightsConfiguration, error) {
	if raw.KnowledgeGap == nil || raw.SuccessDistance == nil {
		return TrainingRankingWeightsConfiguration{}, errors.New("every rankingWeights field is required")
	}
	if err := requireNumberRange(*raw.KnowledgeGap, false, "100", true, "rankingWeights.knowledgeGap"); err != nil {
		return TrainingRankingWeightsConfiguration{}, err
	}
	if err := requireNumberRange(*raw.SuccessDistance, false, "100", true, "rankingWeights.successDistance"); err != nil {
		return TrainingRankingWeightsConfiguration{}, err
	}
	return TrainingRankingWeightsConfiguration{KnowledgeGap: *raw.KnowledgeGap, SuccessDistance: *raw.SuccessDistance}, nil
}

func requireNumberRange(value json.Number, zeroInclusive bool, maximum string, maximumInclusive bool, label string) error {
	rational, ok := new(big.Rat).SetString(string(value))
	if !ok {
		return fmt.Errorf("%s must be a finite decimal", label)
	}
	floatValue, err := strconv.ParseFloat(string(value), 64)
	if err != nil || math.IsNaN(floatValue) || math.IsInf(floatValue, 0) ||
		(rational.Sign() != 0 && floatValue == 0) {
		return fmt.Errorf("%s must preserve its finite nonzero meaning in float64", label)
	}
	zeroComparison := rational.Sign()
	if zeroComparison < 0 || (!zeroInclusive && zeroComparison == 0) {
		return fmt.Errorf("%s is below its lower bound", label)
	}
	maximumValue, _ := new(big.Rat).SetString(maximum)
	comparison := rational.Cmp(maximumValue)
	if comparison > 0 || (!maximumInclusive && comparison == 0) {
		return fmt.Errorf("%s exceeds its upper bound", label)
	}
	return nil
}

func parseKnowledgeCatalog(document json.RawMessage) (ParsedKnowledgeCatalog, error) {
	var raw rawKnowledgeCatalog
	if err := decodeStrict(document, &raw); err != nil {
		return ParsedKnowledgeCatalog{}, err
	}
	if raw.TaxonomyID == nil || raw.KnowledgePoints == nil || raw.ProblemAssignments == nil {
		return ParsedKnowledgeCatalog{}, errors.New("every knowledge catalog field is required")
	}
	if !configurationKeyPattern.MatchString(*raw.TaxonomyID) {
		return ParsedKnowledgeCatalog{}, errors.New("taxonomyId must be canonical")
	}
	if len(*raw.KnowledgePoints) == 0 || len(*raw.KnowledgePoints) > maximumKnowledgePoints {
		return ParsedKnowledgeCatalog{}, errors.New("knowledge point count is outside its contract bounds")
	}
	points := make([]TrainingKnowledgePoint, len(*raw.KnowledgePoints))
	pointIDs := make(map[string]int, len(points))
	for index, point := range *raw.KnowledgePoints {
		if point.ID == nil || point.Label == nil || point.Description == nil || point.PrerequisiteIDs == nil {
			return ParsedKnowledgeCatalog{}, fmt.Errorf("knowledgePoints[%d] requires every field", index)
		}
		if !configurationKeyPattern.MatchString(*point.ID) || !canonicalText(*point.Label, 256) || !canonicalText(*point.Description, 4096) {
			return ParsedKnowledgeCatalog{}, fmt.Errorf("knowledgePoints[%d] has invalid text", index)
		}
		if index > 0 && *point.ID <= points[index-1].ID {
			return ParsedKnowledgeCatalog{}, errors.New("knowledgePoints must be sorted by unique ID")
		}
		prerequisites := slices.Clone(*point.PrerequisiteIDs)
		for prerequisiteIndex, prerequisite := range prerequisites {
			if !configurationKeyPattern.MatchString(prerequisite) || prerequisite == *point.ID ||
				(prerequisiteIndex > 0 && prerequisite <= prerequisites[prerequisiteIndex-1]) {
				return ParsedKnowledgeCatalog{}, fmt.Errorf("knowledge point %q has invalid prerequisites", *point.ID)
			}
		}
		points[index] = TrainingKnowledgePoint{ID: *point.ID, Label: *point.Label, Description: *point.Description, PrerequisiteIDs: prerequisites}
		pointIDs[*point.ID] = index
	}
	if err := validateKnowledgeDAG(points, pointIDs); err != nil {
		return ParsedKnowledgeCatalog{}, err
	}
	assignments := make([]KnowledgeProblemAssignment, len(*raw.ProblemAssignments))
	for index, assignment := range *raw.ProblemAssignments {
		if assignment.Platform == nil || assignment.ProblemID == nil || assignment.ProblemFactSHA256 == nil || assignment.Knowledge == nil {
			return ParsedKnowledgeCatalog{}, fmt.Errorf("problemAssignments[%d] requires every field", index)
		}
		if *assignment.Platform != "pintia" || !canonicalSourceID(*assignment.ProblemID) ||
			!lowercaseSHA256Pattern.MatchString(*assignment.ProblemFactSHA256) || len(*assignment.Knowledge) == 0 {
			return ParsedKnowledgeCatalog{}, fmt.Errorf("problemAssignments[%d] has invalid identity", index)
		}
		weights := make([]TrainingKnowledgeWeight, len(*assignment.Knowledge))
		total := new(big.Rat)
		for weightIndex, weight := range *assignment.Knowledge {
			if weight.KnowledgePointID == nil || weight.Weight == nil {
				return ParsedKnowledgeCatalog{}, fmt.Errorf("problemAssignments[%d].knowledgeWeights[%d] requires every field", index, weightIndex)
			}
			if _, exists := pointIDs[*weight.KnowledgePointID]; !exists ||
				(weightIndex > 0 && *weight.KnowledgePointID <= weights[weightIndex-1].KnowledgePointID) {
				return ParsedKnowledgeCatalog{}, fmt.Errorf("problemAssignments[%d] has invalid knowledge point order or reference", index)
			}
			rational, ok := new(big.Rat).SetString(string(*weight.Weight))
			if !ok || rational.Sign() <= 0 || rational.Cmp(big.NewRat(1, 1)) > 0 {
				return ParsedKnowledgeCatalog{}, fmt.Errorf("problemAssignments[%d] has an invalid knowledge weight", index)
			}
			total.Add(total, rational)
			weights[weightIndex] = TrainingKnowledgeWeight{KnowledgePointID: *weight.KnowledgePointID, Weight: *weight.Weight}
		}
		if total.Cmp(big.NewRat(1, 1)) != 0 {
			return ParsedKnowledgeCatalog{}, fmt.Errorf("problemAssignments[%d] knowledge weights must sum exactly to one", index)
		}
		assignments[index] = KnowledgeProblemAssignment{
			Platform: *assignment.Platform, ProblemID: *assignment.ProblemID,
			ProblemFactSHA256: *assignment.ProblemFactSHA256, Knowledge: weights,
		}
		if index > 0 && compareAssignment(assignments[index-1], assignments[index]) >= 0 {
			return ParsedKnowledgeCatalog{}, errors.New("problemAssignments must be sorted by unique platform, problemId, and problemFactSha256")
		}
	}
	return ParsedKnowledgeCatalog{TaxonomyID: *raw.TaxonomyID, KnowledgePoints: points, ProblemAssignments: assignments}, nil
}

func validateKnowledgeDAG(points []TrainingKnowledgePoint, indices map[string]int) error {
	state := make([]uint8, len(points))
	var visit func(int) error
	visit = func(index int) error {
		if state[index] == 1 {
			return errors.New("knowledge prerequisite graph contains a cycle")
		}
		if state[index] == 2 {
			return nil
		}
		state[index] = 1
		for _, prerequisite := range points[index].PrerequisiteIDs {
			prerequisiteIndex, exists := indices[prerequisite]
			if !exists {
				return fmt.Errorf("knowledge point %q references missing prerequisite %q", points[index].ID, prerequisite)
			}
			if err := visit(prerequisiteIndex); err != nil {
				return err
			}
		}
		state[index] = 2
		return nil
	}
	for index := range points {
		if err := visit(index); err != nil {
			return err
		}
	}
	return nil
}

func compareAssignment(left, right KnowledgeProblemAssignment) int {
	if value := strings.Compare(left.Platform, right.Platform); value != 0 {
		return value
	}
	if value := strings.Compare(left.ProblemID, right.ProblemID); value != 0 {
		return value
	}
	return strings.Compare(left.ProblemFactSHA256, right.ProblemFactSHA256)
}

func canonicalText(value string, maximumBytes int) bool {
	return value != "" && strings.TrimSpace(value) == value && len(value) <= maximumBytes && !strings.ContainsRune(value, 0)
}

func canonicalSourceID(value string) bool {
	return value != "" && len(value) <= 256 && strings.TrimSpace(value) == value && !strings.ContainsAny(value, ":\x00")
}

type snapshotProblemKey struct {
	snapshotID          int64
	problemSetProblemID string
}

type catalogAssignmentKey struct {
	platform          string
	problemID         string
	problemFactSHA256 string
}

func prepareActors(students []TrainingStudent) ([]TrainingActorInput, []int64, map[int64]int, error) {
	if len(students) == 0 {
		return nil, nil, nil, domainError(ErrorAnalyticsUnavailable, true, "build training actors", errors.New("current analytics generation has no actors"))
	}
	if len(students) > maximumTrainingActors {
		return nil, nil, nil, domainError(ErrorStoredDataInvalid, true, "build training actors", errors.New("actor count exceeds 20000"))
	}
	sorted := slices.Clone(students)
	slices.SortFunc(sorted, func(left, right TrainingStudent) int { return compareInt64(left.ActorID, right.ActorID) })
	actors := make([]TrainingActorInput, len(sorted))
	actorIDs := make([]int64, len(sorted))
	actorIndex := make(map[int64]int, len(sorted))
	for index, student := range sorted {
		if student.ActorID <= 0 || (index > 0 && sorted[index-1].ActorID == student.ActorID) {
			return nil, nil, nil, domainError(ErrorStoredDataInvalid, true, "build training actors", errors.New("analytics actor IDs must be unique positive values"))
		}
		rating, err := canonicalNumber(student.Rating)
		if err != nil {
			return nil, nil, nil, domainError(ErrorStoredDataInvalid, true, "build training actors", fmt.Errorf("actor %d rating: %w", student.ActorID, err))
		}
		if err := requireNumberRange(json.Number(rating), true, "1000000", true, "currentRating"); err != nil {
			return nil, nil, nil, domainError(ErrorStoredDataInvalid, true, "build training actors", fmt.Errorf("actor %d rating: %w", student.ActorID, err))
		}
		if _, _, err := canonicaljson.Object(student.Metrics, maxMetricsBytes); err != nil {
			return nil, nil, nil, domainError(ErrorStoredDataInvalid, true, "build training actors", fmt.Errorf("actor %d metrics: %w", student.ActorID, err))
		}
		actorID := strconv.FormatInt(student.ActorID, 10)
		actors[index] = TrainingActorInput{ActorID: actorID, CurrentRating: rating}
		actorIDs[index] = student.ActorID
		actorIndex[student.ActorID] = index
	}
	return actors, actorIDs, actorIndex, nil
}

func prepareProblems(
	rows []TrainingProblem,
	catalog ParsedKnowledgeCatalog,
) ([]preparedProblem, map[snapshotProblemKey]string, error) {
	if len(rows) == 0 {
		return nil, nil, domainError(ErrorStoredDataInvalid, true, "build training problems", errors.New("analytics generation has no Pintia problems"))
	}
	if len(rows) > maximumTrainingProblems {
		return nil, nil, domainError(ErrorStoredDataInvalid, true, "build training problems", errors.New("problem instance count exceeds 10000"))
	}
	assignments := make(map[catalogAssignmentKey][]TrainingKnowledgeWeight, len(catalog.ProblemAssignments))
	for _, assignment := range catalog.ProblemAssignments {
		key := catalogAssignmentKey{assignment.Platform, assignment.ProblemID, assignment.ProblemFactSHA256}
		assignments[key] = slices.Clone(assignment.Knowledge)
	}
	byProblemKey := make(map[string]*preparedProblem)
	problemBySnapshot := make(map[snapshotProblemKey]string, len(rows))
	sourceSets := make(map[string]map[TrainingSourceProblemSet]struct{})
	totalStatementBytes := 0
	for rowIndex, row := range rows {
		if row.SnapshotID <= 0 || !canonicalDecimalID(row.ProblemSetID) || !canonicalSourceID(row.ProblemSetProblemID) {
			return nil, nil, domainError(ErrorStoredDataInvalid, true, "build training problems", fmt.Errorf("problem instance %d has invalid identity or title", rowIndex))
		}
		if !canonicalPintiaURL(row.SourceURL) {
			return nil, nil, domainError(ErrorStoredDataInvalid, true, "build training problems", fmt.Errorf("problem instance %d source URL is invalid", rowIndex))
		}
		fact, err := buildCanonicalProblemFact(row)
		if err != nil {
			return nil, nil, domainError(ErrorStoredDataInvalid, true, "build training problems", fmt.Errorf("problem instance %d: %w", rowIndex, err))
		}
		snapshotKey := snapshotProblemKey{snapshotID: row.SnapshotID, problemSetProblemID: row.ProblemSetProblemID}
		if _, exists := problemBySnapshot[snapshotKey]; exists {
			return nil, nil, domainError(ErrorStoredDataInvalid, true, "build training problems", errors.New("snapshot problem identity is duplicated"))
		}
		problemBySnapshot[snapshotKey] = fact.ProblemKey
		if existing := byProblemKey[fact.ProblemKey]; existing == nil {
			weights, exists := assignments[catalogAssignmentKey{row.Platform, row.ProblemID, fact.ProblemFactSHA256}]
			if !exists {
				return nil, nil, preflightFailure("knowledge_catalog_assignment_missing", []string{fact.ProblemKey})
			}
			statement, err := statementText(row.ContentHTML)
			if err != nil {
				return nil, nil, domainError(ErrorStoredDataInvalid, true, "parse problem statement HTML", fmt.Errorf("%s: %w", fact.ProblemKey, err))
			}
			if len(statement) > maximumStatementBytes {
				return nil, nil, domainError(ErrorStoredDataInvalid, true, "build training problems", fmt.Errorf("%s statement exceeds 1 MiB", fact.ProblemKey))
			}
			totalStatementBytes += len(statement)
			if totalStatementBytes > maximumStatementTotalBytes {
				return nil, nil, domainError(ErrorStoredDataInvalid, true, "build training problems", errors.New("statement text total exceeds 32 MiB"))
			}
			byProblemKey[fact.ProblemKey] = &preparedProblem{wire: TrainingProblemInput{
				ProblemKey: fact.ProblemKey, SourceProblemKey: fact.SourceProblemKey, ProblemFactSHA256: fact.ProblemFactSHA256,
				Platform: row.Platform, ProblemID: row.ProblemID, Title: row.Title, StatementText: statement,
				MaxScore: fact.MaxScore, TimeLimitMS: cloneInt64(row.TimeLimitMS), MemoryLimitBytes: cloneInt64(row.MemoryLimitBytes),
				KnowledgeWeights: slices.Clone(weights),
			}}
			sourceSets[fact.ProblemKey] = make(map[TrainingSourceProblemSet]struct{})
		}
		sourceSets[fact.ProblemKey][TrainingSourceProblemSet{ProblemSetID: row.ProblemSetID, SourceURL: row.SourceURL}] = struct{}{}
	}
	if len(byProblemKey) > maximumTrainingProblems {
		return nil, nil, domainError(ErrorStoredDataInvalid, true, "build training problems", errors.New("versioned problem count exceeds 10000"))
	}
	problems := make([]preparedProblem, 0, len(byProblemKey))
	for problemKey, problem := range byProblemKey {
		sets := make([]TrainingSourceProblemSet, 0, len(sourceSets[problemKey]))
		for sourceSet := range sourceSets[problemKey] {
			sets = append(sets, sourceSet)
		}
		slices.SortFunc(sets, compareSourceProblemSet)
		problem.wire.SourceProblemSets = sets
		problems = append(problems, *problem)
	}
	slices.SortFunc(problems, func(left, right preparedProblem) int {
		return strings.Compare(left.wire.ProblemKey, right.wire.ProblemKey)
	})
	return problems, problemBySnapshot, nil
}

func canonicalPintiaURL(value string) bool {
	if value == "" || value != strings.TrimSpace(value) {
		return false
	}
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme == "https" && parsed.Host == "pintia.cn" && parsed.User == nil && parsed.Fragment == ""
}

func canonicalPositiveNumber(value, label string) (json.RawMessage, error) {
	canonical, err := canonicalNumber(value)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", label, err)
	}
	rational, ok := new(big.Rat).SetString(string(canonical))
	if !ok || rational.Sign() <= 0 {
		return nil, fmt.Errorf("%s must be positive", label)
	}
	return canonical, nil
}

func cloneInt64(value *int64) *int64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func compareSourceProblemSet(left, right TrainingSourceProblemSet) int {
	if comparison := len(left.ProblemSetID) - len(right.ProblemSetID); comparison != 0 {
		return comparison
	}
	if comparison := strings.Compare(left.ProblemSetID, right.ProblemSetID); comparison != 0 {
		return comparison
	}
	return strings.Compare(left.SourceURL, right.SourceURL)
}

func canonicalDecimalID(value string) bool {
	return len(value) <= 256 && canonicalIDPattern.MatchString(value)
}

func statementText(contentHTML *string) (string, error) {
	if contentHTML == nil || *contentHTML == "" {
		return "", nil
	}
	context := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
	nodes, err := html.ParseFragment(strings.NewReader(*contentHTML), context)
	if err != nil {
		return "", err
	}
	parts := make([]string, 0)
	var visit func(*html.Node, bool)
	visit = func(node *html.Node, suppressed bool) {
		if node.Type == html.ElementNode && (node.DataAtom == atom.Script || node.DataAtom == atom.Style || node.DataAtom == atom.Template) {
			suppressed = true
		}
		if node.Type == html.TextNode && !suppressed {
			parts = append(parts, strings.Fields(node.Data)...)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child, suppressed)
		}
	}
	for _, node := range nodes {
		visit(node, false)
	}
	return strings.Join(parts, " "), nil
}

func prepareInteractions(
	observations []TrainingObservation,
	actorIndex map[int64]int,
	problemBySnapshot map[snapshotProblemKey]string,
	problems []preparedProblem,
) ([]TrainingInteractionInput, error) {
	if len(observations) == 0 {
		return nil, domainError(ErrorStoredDataInvalid, true, "build training interactions", errors.New("analytics generation has no eligible ranking observations"))
	}
	if len(observations) > maximumTrainingInteractions {
		return nil, domainError(ErrorStoredDataInvalid, true, "build training interactions", errors.New("interaction count exceeds 200000"))
	}
	maxScoreByProblem := make(map[string]*big.Rat, len(problems))
	for _, problem := range problems {
		rational, ok := new(big.Rat).SetString(string(problem.wire.MaxScore))
		if !ok || rational.Sign() <= 0 {
			return nil, domainError(ErrorStoredDataInvalid, true, "build training interactions", fmt.Errorf("problem %s maxScore is invalid", problem.wire.ProblemKey))
		}
		maxScoreByProblem[problem.wire.ProblemKey] = rational
	}
	interactions := make([]TrainingInteractionInput, 0, len(observations))
	seen := make(map[string]struct{}, len(observations))
	for index, observation := range observations {
		if observation.SnapshotID <= 0 || observation.ActorID <= 0 || !canonicalSourceID(observation.ProblemSetProblemID) {
			return nil, domainError(ErrorStoredDataInvalid, true, "build training interactions", fmt.Errorf("observation %d has an invalid identity", index))
		}
		if _, exists := actorIndex[observation.ActorID]; !exists {
			return nil, domainError(ErrorStoredDataInvalid, true, "build training interactions", fmt.Errorf("observation %d references an actor outside current analytics", index))
		}
		problemKey, exists := problemBySnapshot[snapshotProblemKey{observation.SnapshotID, observation.ProblemSetProblemID}]
		if !exists {
			return nil, domainError(ErrorStoredDataInvalid, true, "build training interactions", fmt.Errorf("observation %d references an unknown snapshot problem", index))
		}
		if observation.Score == nil || observation.MaxScore == nil || observation.Passed == nil ||
			observation.ValidSubmissionCount == nil || observation.FirstSubmittedAt == nil || observation.LastSubmittedAt == nil {
			return nil, domainError(ErrorStoredDataInvalid, true, "build training interactions", fmt.Errorf("observation %d is missing final ranking or submission evidence", index))
		}
		if observation.SubmissionCount < 1 || *observation.ValidSubmissionCount < 0 ||
			*observation.ValidSubmissionCount > observation.SubmissionCount {
			return nil, domainError(ErrorStoredDataInvalid, true, "build training interactions", fmt.Errorf("observation %d has invalid submission counts", index))
		}
		firstSubmittedAt := observation.FirstSubmittedAt.UTC()
		lastSubmittedAt := observation.LastSubmittedAt.UTC()
		if firstSubmittedAt.IsZero() || lastSubmittedAt.IsZero() || firstSubmittedAt.After(lastSubmittedAt) {
			return nil, domainError(ErrorStoredDataInvalid, true, "build training interactions", fmt.Errorf("observation %d has invalid submission timestamps", index))
		}
		scoreRaw, err := canonicalNumber(*observation.Score)
		if err != nil {
			return nil, domainError(ErrorStoredDataInvalid, true, "build training interactions", fmt.Errorf("observation %d score: %w", index, err))
		}
		maxScoreRaw, err := canonicalPositiveNumber(*observation.MaxScore, "maxScore")
		if err != nil {
			return nil, domainError(ErrorStoredDataInvalid, true, "build training interactions", fmt.Errorf("observation %d: %w", index, err))
		}
		score, scoreOK := new(big.Rat).SetString(string(scoreRaw))
		maxScore, maxScoreOK := new(big.Rat).SetString(string(maxScoreRaw))
		if !scoreOK || !maxScoreOK || score.Sign() < 0 || score.Cmp(maxScore) > 0 ||
			maxScore.Cmp(maxScoreByProblem[problemKey]) != 0 {
			return nil, domainError(ErrorStoredDataInvalid, true, "build training interactions", fmt.Errorf("observation %d score or maxScore is inconsistent", index))
		}
		if *observation.Passed && score.Cmp(maxScore) != 0 || !*observation.Passed && score.Cmp(maxScore) >= 0 {
			return nil, domainError(ErrorStoredDataInvalid, true, "build training interactions", fmt.Errorf("observation %d final passed flag contradicts its historical score rate", index))
		}
		targetScoreRate, err := scoreRateNumber(score, maxScore, *observation.Passed)
		if err != nil {
			return nil, domainError(ErrorStoredDataInvalid, true, "build training interactions", fmt.Errorf("observation %d target score rate: %w", index, err))
		}
		actorID := strconv.FormatInt(observation.ActorID, 10)
		snapshotID := strconv.FormatInt(observation.SnapshotID, 10)
		interactionID, err := hashCanonicalValue(interactionIdentityWire{
			ActorID: actorID, ProblemKey: problemKey, SnapshotID: snapshotID,
		}, maxManifestBytes, "interaction identity")
		if err != nil {
			return nil, err
		}
		if _, duplicate := seen[interactionID]; duplicate {
			return nil, domainError(ErrorStoredDataInvalid, true, "build training interactions", errors.New("ranking observation identity is duplicated"))
		}
		seen[interactionID] = struct{}{}
		interactions = append(interactions, TrainingInteractionInput{
			InteractionID: interactionID, SnapshotID: snapshotID, ActorID: actorID, ProblemKey: problemKey,
			FirstSubmittedAt: firstSubmittedAt, LastSubmittedAt: lastSubmittedAt,
			SubmissionCount: observation.SubmissionCount, ValidSubmissionCount: *observation.ValidSubmissionCount,
			TargetScoreRate: targetScoreRate, Passed: *observation.Passed, Split: "train",
		})
	}
	slices.SortFunc(interactions, func(left, right TrainingInteractionInput) int {
		return strings.Compare(left.InteractionID, right.InteractionID)
	})
	return interactions, nil
}

func scoreRateNumber(score, maximum *big.Rat, passed bool) (json.Number, error) {
	if passed {
		return json.Number("1"), nil
	}
	ratio := new(big.Rat).Quo(score, maximum)
	value, exact := ratio.Float64()
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value >= 1 {
		return "", errors.New("score rate is outside [0,1)")
	}
	_ = exact
	return json.Number(strconv.FormatFloat(value, 'g', -1, 64)), nil
}

func assignTrainingSplit(interactions []TrainingInteractionInput, configuration ParsedTrainingConfiguration) error {
	byActor := make(map[string][]int)
	for index := range interactions {
		byActor[interactions[index].ActorID] = append(byActor[interactions[index].ActorID], index)
	}
	for actorID, indices := range byActor {
		if int64(len(indices)) < configuration.MinActorInteractions {
			return domainError(ErrorStoredDataInvalid, true, "split training interactions", fmt.Errorf("actor %s has fewer than minActorInteractions observations", actorID))
		}
		validationIndex := indices[0]
		for _, candidateIndex := range indices[1:] {
			candidate := interactions[candidateIndex]
			current := interactions[validationIndex]
			if candidate.LastSubmittedAt.After(current.LastSubmittedAt) ||
				(candidate.LastSubmittedAt.Equal(current.LastSubmittedAt) && candidate.InteractionID > current.InteractionID) {
				validationIndex = candidateIndex
			}
		}
		interactions[validationIndex].Split = "validation"
	}
	trainCount := 0
	validationActors := make(map[string]struct{})
	for _, interaction := range interactions {
		if interaction.Split == "train" {
			trainCount++
		} else {
			validationActors[interaction.ActorID] = struct{}{}
		}
	}
	validationCount := len(interactions) - trainCount
	if int64(trainCount) < configuration.MinTrainInteractions ||
		int64(validationCount) < configuration.Validation.MinInteractions ||
		int64(len(validationActors)) < configuration.Validation.MinActors {
		return domainError(ErrorStoredDataInvalid, true, "split training interactions", errors.New("train or validation split does not meet configured minimums"))
	}
	return nil
}

type featureStats struct {
	count           int64
	passed          int64
	scoreTotal      float64
	logAttemptTotal float64
	submissionTotal int64
	actors          map[string]struct{}
}

func applyTrainingFeatures(
	actors []TrainingActorInput,
	problems []preparedProblem,
	interactions []TrainingInteractionInput,
	configuration ParsedTrainingConfiguration,
) error {
	actorStats := make(map[string]*featureStats, len(actors))
	problemStats := make(map[string]*featureStats, len(problems))
	for _, interaction := range interactions {
		if interaction.Split != "train" {
			continue
		}
		score, err := strconv.ParseFloat(string(interaction.TargetScoreRate), 64)
		if err != nil || !finiteFloat(score) || score < 0 || score > 1 {
			return domainError(ErrorInvalidBundle, true, "compute training features", errors.New("target score rate is invalid"))
		}
		actor := actorStats[interaction.ActorID]
		if actor == nil {
			actor = &featureStats{}
			actorStats[interaction.ActorID] = actor
		}
		problem := problemStats[interaction.ProblemKey]
		if problem == nil {
			problem = &featureStats{actors: make(map[string]struct{})}
			problemStats[interaction.ProblemKey] = problem
		}
		for _, stats := range []*featureStats{actor, problem} {
			stats.count++
			if interaction.Passed {
				stats.passed++
			}
			stats.scoreTotal += score
			stats.logAttemptTotal += math.Log1p(float64(interaction.SubmissionCount))
			stats.submissionTotal += interaction.SubmissionCount
		}
		problem.actors[interaction.ActorID] = struct{}{}
	}
	for index := range actors {
		stats := actorStats[actors[index].ActorID]
		if stats == nil || stats.count < 1 {
			return domainError(ErrorStoredDataInvalid, true, "compute training actor features", fmt.Errorf("actor %s has no training interaction", actors[index].ActorID))
		}
		actors[index].Features = []float64{
			math.Log1p(float64(stats.count)),
			float64(stats.passed+1) / float64(stats.count+2),
			stats.scoreTotal / float64(stats.count),
			stats.logAttemptTotal / float64(stats.count),
		}
		if !finiteVector(actors[index].Features) {
			return domainError(ErrorStoredDataInvalid, true, "compute training actor features", errors.New("actor feature vector is non-finite"))
		}
	}
	for index := range problems {
		stats := problemStats[problems[index].wire.ProblemKey]
		if stats == nil || stats.count < configuration.MinProblemInteractions {
			return domainError(ErrorStoredDataInvalid, true, "compute training problem features", fmt.Errorf("problem %s has fewer than minProblemInteractions training observations", problems[index].wire.ProblemKey))
		}
		acceptanceRate := float64(stats.passed+1) / float64(stats.count+2)
		problems[index].wire.Features = []float64{
			math.Log(acceptanceRate / (1 - acceptanceRate)),
			math.Log1p(float64(len(stats.actors))),
			math.Log1p(float64(stats.submissionTotal)),
		}
		problems[index].wire.TrainActorCount = int64(len(stats.actors))
		problems[index].wire.TrainSubmissionCount = stats.submissionTotal
		if !finiteVector(problems[index].wire.Features) {
			return domainError(ErrorStoredDataInvalid, true, "compute training problem features", errors.New("problem feature vector is non-finite"))
		}
	}
	return nil
}

func finiteFloat(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func finiteVector(values []float64) bool {
	for _, value := range values {
		if !finiteFloat(value) {
			return false
		}
	}
	return true
}

func hashCanonicalValue(value any, maximumBytes int, label string) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", domainError(ErrorInvalidBundle, true, "encode "+label, err)
	}
	_, digest, err := canonicaljson.Object(raw, maximumBytes)
	if err != nil {
		return "", domainError(ErrorInvalidBundle, true, "canonicalize "+label, err)
	}
	return digest, nil
}

// ParseInputBundle revalidates the complete v2 numerical contract before a
// trainer process receives it and retains the typed values for output checks.
func ParseInputBundle(raw json.RawMessage, maximumBytes int, run TrainingRun) (ParsedInputBundle, error) {
	canonical, _, err := requireCanonicalObject(raw, maximumBytes, "training input bundle")
	if err != nil {
		return ParsedInputBundle{}, err
	}
	var value inputBundleWire
	if err := decodeStrict(canonical, &value); err != nil {
		return ParsedInputBundle{}, domainError(ErrorInvalidBundle, true, "decode training input bundle", err)
	}
	if value.Protocol != TrainingBundleProtocolV2 || run.BundleProtocol != TrainingBundleProtocolV2 {
		return ParsedInputBundle{}, domainError(ErrorInvalidBundle, true, "validate training input bundle", errors.New("training bundle protocol is unsupported"))
	}
	manifest, manifestSHA256, err := canonicaljson.Object(value.Manifest, maxManifestBytes)
	if err != nil || manifestSHA256 != run.InputManifestSHA256 || !bytes.Equal(manifest, run.InputManifest) {
		return ParsedInputBundle{}, domainError(ErrorInvalidBundle, true, "validate training input bundle", errors.New("input manifest differs from the queued run"))
	}
	var manifestValue inputManifestWire
	if err := decodeStrict(manifest, &manifestValue); err != nil {
		return ParsedInputBundle{}, domainError(ErrorInvalidBundle, true, "decode training input manifest", err)
	}
	if err := validateInputManifestAgainstRun(manifestValue, run); err != nil {
		return ParsedInputBundle{}, err
	}
	_, analyticsSHA256, err := canonicaljson.Object(value.AnalyticsInputManifest, maxManifestBytes)
	if err != nil || analyticsSHA256 != manifestValue.Source.AnalyticsInputManifestSHA256 {
		return ParsedInputBundle{}, domainError(ErrorInvalidBundle, true, "validate training analytics manifest", errors.New("analytics manifest hash differs from provenance"))
	}
	trainingDocument, trainingSHA256, err := canonicaljson.Object(value.TrainingConfiguration.Document, maxConfigurationBytes)
	if err != nil || trainingSHA256 != manifestValue.TrainingConfiguration.DocumentSHA256 ||
		value.TrainingConfiguration.SchemaID != trainingConfigurationSchemaV2 ||
		value.TrainingConfiguration.SchemaID != manifestValue.TrainingConfiguration.SchemaID {
		return ParsedInputBundle{}, domainError(ErrorInvalidBundle, true, "validate bundled training configuration", errors.New("training configuration differs from its manifest"))
	}
	trainingConfiguration, err := parseTrainingConfiguration(trainingDocument)
	if err != nil {
		return ParsedInputBundle{}, domainError(ErrorInvalidBundle, true, "parse bundled training configuration", err)
	}
	catalogDocument, catalogSHA256, err := canonicaljson.Object(value.KnowledgeCatalog.Document, maxConfigurationBytes)
	if err != nil || catalogSHA256 != manifestValue.KnowledgeCatalog.DocumentSHA256 ||
		value.KnowledgeCatalog.SchemaID != knowledgeCatalogSchemaV1 ||
		value.KnowledgeCatalog.SchemaID != manifestValue.KnowledgeCatalog.SchemaID {
		return ParsedInputBundle{}, domainError(ErrorInvalidBundle, true, "validate bundled knowledge catalog", errors.New("knowledge catalog differs from its manifest"))
	}
	knowledgeCatalog, err := parseKnowledgeCatalog(catalogDocument)
	if err != nil {
		return ParsedInputBundle{}, domainError(ErrorInvalidBundle, true, "parse bundled knowledge catalog", err)
	}
	if trainingConfiguration.KnowledgeCatalogVersionID != run.KnowledgeCatalogVersionID ||
		trainingConfiguration.KnowledgeCatalogVersionID != mustCanonicalID(manifestValue.KnowledgeCatalog.VersionID) {
		return ParsedInputBundle{}, domainError(ErrorInvalidBundle, true, "validate bundled knowledge catalog", errors.New("catalog version provenance differs"))
	}
	if !slices.Equal(value.FeatureSchema.ActorFeatureIDs, trainingFeatureSchemaV2.ActorFeatureIDs) ||
		!slices.Equal(value.FeatureSchema.ProblemFeatureIDs, trainingFeatureSchemaV2.ProblemFeatureIDs) {
		return ParsedInputBundle{}, domainError(ErrorInvalidBundle, true, "validate training feature schema", errors.New("feature schema differs from v2"))
	}
	featureSchemaSHA256, err := hashCanonicalValue(value.FeatureSchema, maxManifestBytes, "feature schema")
	if err != nil || featureSchemaSHA256 != manifestValue.FeatureSchemaSHA256 {
		return ParsedInputBundle{}, domainError(ErrorInvalidBundle, true, "validate training feature schema", errors.New("feature schema hash differs"))
	}
	if err := validateKnowledgePoints(value.KnowledgePoints, knowledgeCatalog, manifestValue); err != nil {
		return ParsedInputBundle{}, err
	}
	actorIDs, actorIndex, err := validateInputActors(value.Actors, manifestValue)
	if err != nil {
		return ParsedInputBundle{}, err
	}
	problemIndex, err := validateInputProblems(value.Problems, knowledgeCatalog, manifestValue)
	if err != nil {
		return ParsedInputBundle{}, err
	}
	if err := validateInputInteractions(value.Interactions, value.Actors, value.Problems, actorIndex, problemIndex, trainingConfiguration, manifestValue); err != nil {
		return ParsedInputBundle{}, err
	}
	return ParsedInputBundle{
		CanonicalJSON: canonical, Manifest: manifest, ManifestSHA256: manifestSHA256, ActorIDs: actorIDs,
		FeatureSchema: value.FeatureSchema, TrainingConfiguration: trainingConfiguration, KnowledgeCatalog: knowledgeCatalog,
		Actors: slices.Clone(value.Actors), KnowledgePoints: slices.Clone(value.KnowledgePoints),
		Problems: slices.Clone(value.Problems), Interactions: slices.Clone(value.Interactions),
	}, nil
}

func validateInputManifestAgainstRun(value inputManifestWire, run TrainingRun) error {
	if value.Protocol != TrainingBundleProtocolV2 ||
		value.Source.AnalyticsGenerationID != strconv.FormatInt(run.SourceAnalyticsGenerationID, 10) ||
		value.Source.AnalyticsHeadRevision != run.SourceAnalyticsHeadRevision ||
		value.TrainingConfiguration.VersionID != strconv.FormatInt(run.TrainingConfigurationVersionID, 10) ||
		value.KnowledgeCatalog.VersionID != strconv.FormatInt(run.KnowledgeCatalogVersionID, 10) ||
		value.TrainingConfiguration.SchemaID != trainingConfigurationSchemaV2 ||
		value.KnowledgeCatalog.SchemaID != knowledgeCatalogSchemaV1 ||
		value.KnowledgePointCount <= 0 || value.KnowledgePointCount > maximumKnowledgePoints ||
		value.ActorCount <= 0 || value.ActorCount > maximumTrainingActors ||
		value.ProblemCount <= 0 || value.ProblemCount > maximumTrainingProblems ||
		value.InteractionCount <= 0 || value.InteractionCount > maximumTrainingInteractions ||
		value.TrainInteractionCount <= 0 || value.ValidationInteractionCount <= 0 ||
		value.TrainInteractionCount+value.ValidationInteractionCount != value.InteractionCount ||
		!manifestHashesValid(value) || value.Source.AlgorithmVersion == "" ||
		strings.TrimSpace(value.Source.AlgorithmVersion) != value.Source.AlgorithmVersion ||
		!configurationKeyPattern.MatchString(value.TrainingConfiguration.Key) ||
		!configurationKeyPattern.MatchString(value.KnowledgeCatalog.Key) ||
		value.TrainingConfiguration.VersionNumber <= 0 || value.KnowledgeCatalog.VersionNumber <= 0 {
		return domainError(ErrorInvalidBundle, true, "validate training input manifest", errors.New("manifest provenance differs from the queued run or violates v2"))
	}
	return nil
}

func manifestHashesValid(value inputManifestWire) bool {
	hashes := []string{
		value.Source.AnalyticsInputManifestSHA256, value.Source.AnalyticsConfigurationSHA256,
		value.TrainingConfiguration.DocumentSHA256, value.KnowledgeCatalog.DocumentSHA256,
		value.FeatureSchemaSHA256, value.KnowledgePointSetSHA256, value.ActorSetSHA256,
		value.ProblemSetSHA256, value.InteractionSetSHA256, value.SplitSHA256,
	}
	for _, hash := range hashes {
		if !lowercaseSHA256Pattern.MatchString(hash) {
			return false
		}
	}
	return true
}

func mustCanonicalID(value string) int64 {
	parsed, _ := parseCanonicalID(value)
	return parsed
}

func validateKnowledgePoints(points []TrainingKnowledgePoint, catalog ParsedKnowledgeCatalog, manifest inputManifestWire) error {
	if len(points) != len(catalog.KnowledgePoints) || len(points) != manifest.KnowledgePointCount {
		return domainError(ErrorInvalidBundle, true, "validate training knowledge points", errors.New("knowledge point count differs"))
	}
	identities := make([]string, len(points))
	for index := range points {
		expected := catalog.KnowledgePoints[index]
		actual := points[index]
		if actual.ID != expected.ID || actual.Label != expected.Label || actual.Description != expected.Description ||
			!slices.Equal(actual.PrerequisiteIDs, expected.PrerequisiteIDs) {
			return domainError(ErrorInvalidBundle, true, "validate training knowledge points", errors.New("normalized knowledge points differ from the catalog"))
		}
		identities[index] = actual.ID
	}
	hash, err := hashCanonicalValue(knowledgePointSetWire{KnowledgePointIDs: identities}, maxManifestBytes, "knowledge point set")
	if err != nil || hash != manifest.KnowledgePointSetSHA256 {
		return domainError(ErrorInvalidBundle, true, "validate training knowledge points", errors.New("knowledge point set hash differs"))
	}
	return nil
}

func validateInputActors(actors []TrainingActorInput, manifest inputManifestWire) ([]int64, map[string]int, error) {
	if len(actors) != manifest.ActorCount {
		return nil, nil, domainError(ErrorInvalidBundle, true, "validate training actors", errors.New("actor count differs"))
	}
	actorIDs := make([]int64, len(actors))
	actorIDStrings := make([]string, len(actors))
	actorIndex := make(map[string]int, len(actors))
	for index, actor := range actors {
		actorID, err := parseCanonicalID(actor.ActorID)
		if err != nil || (index > 0 && actorID <= actorIDs[index-1]) || len(actor.Features) != len(trainingFeatureSchemaV2.ActorFeatureIDs) ||
			!finiteVector(actor.Features) {
			return nil, nil, domainError(ErrorInvalidBundle, true, "validate training actors", errors.New("actor order, identity, or feature vector is invalid"))
		}
		rating, err := canonicalNumber(string(actor.CurrentRating))
		if err != nil || requireNumberRange(json.Number(rating), true, "1000000", true, "currentRating") != nil {
			return nil, nil, domainError(ErrorInvalidBundle, true, "validate training actors", errors.New("currentRating is invalid"))
		}
		actorIDs[index] = actorID
		actorIDStrings[index] = actor.ActorID
		actorIndex[actor.ActorID] = index
	}
	hash, err := hashCanonicalValue(actorSetWire{ActorIDs: actorIDStrings}, maxManifestBytes, "actor set")
	if err != nil || hash != manifest.ActorSetSHA256 {
		return nil, nil, domainError(ErrorInvalidBundle, true, "validate training actors", errors.New("actor set hash differs"))
	}
	return actorIDs, actorIndex, nil
}

func validateInputProblems(
	problems []TrainingProblemInput,
	catalog ParsedKnowledgeCatalog,
	manifest inputManifestWire,
) (map[string]int, error) {
	if len(problems) != manifest.ProblemCount {
		return nil, domainError(ErrorInvalidBundle, true, "validate training problems", errors.New("problem count differs"))
	}
	assignments := make(map[catalogAssignmentKey][]TrainingKnowledgeWeight, len(catalog.ProblemAssignments))
	for _, assignment := range catalog.ProblemAssignments {
		assignments[catalogAssignmentKey{assignment.Platform, assignment.ProblemID, assignment.ProblemFactSHA256}] = assignment.Knowledge
	}
	problemKeys := make([]string, len(problems))
	problemIndex := make(map[string]int, len(problems))
	statementTotal := 0
	for index, problem := range problems {
		if problem.Platform != "pintia" || !canonicalSourceID(problem.ProblemID) ||
			problem.SourceProblemKey != "pintia:"+problem.ProblemID ||
			!lowercaseSHA256Pattern.MatchString(problem.ProblemFactSHA256) ||
			problem.ProblemKey != problem.SourceProblemKey+":"+problem.ProblemFactSHA256 ||
			!canonicalText(problem.Title, 4096) || len(problem.StatementText) > maximumStatementBytes ||
			len(problem.Features) != len(trainingFeatureSchemaV2.ProblemFeatureIDs) || !finiteVector(problem.Features) ||
			problem.TrainActorCount < 1 || problem.TrainSubmissionCount < 1 ||
			(index > 0 && problem.ProblemKey <= problems[index-1].ProblemKey) {
			return nil, domainError(ErrorInvalidBundle, true, "validate training problems", errors.New("problem identity, fields, order, or features are invalid"))
		}
		statementTotal += len(problem.StatementText)
		if statementTotal > maximumStatementTotalBytes {
			return nil, domainError(ErrorInvalidBundle, true, "validate training problems", errors.New("statement text total exceeds 32 MiB"))
		}
		if _, err := canonicalPositiveNumber(string(problem.MaxScore), "maxScore"); err != nil ||
			problem.TimeLimitMS != nil && *problem.TimeLimitMS < 0 || problem.MemoryLimitBytes != nil && *problem.MemoryLimitBytes < 0 {
			return nil, domainError(ErrorInvalidBundle, true, "validate training problems", errors.New("problem score or limits are invalid"))
		}
		if len(problem.SourceProblemSets) == 0 {
			return nil, domainError(ErrorInvalidBundle, true, "validate training problems", errors.New("problem source set is empty"))
		}
		for sourceIndex, source := range problem.SourceProblemSets {
			if !canonicalDecimalID(source.ProblemSetID) || !canonicalPintiaURL(source.SourceURL) ||
				(sourceIndex > 0 && compareSourceProblemSet(problem.SourceProblemSets[sourceIndex-1], source) >= 0) {
				return nil, domainError(ErrorInvalidBundle, true, "validate training problems", errors.New("problem source sets are invalid or unsorted"))
			}
		}
		expectedWeights, exists := assignments[catalogAssignmentKey{problem.Platform, problem.ProblemID, problem.ProblemFactSHA256}]
		if !exists || !equalKnowledgeWeights(problem.KnowledgeWeights, expectedWeights) {
			return nil, domainError(ErrorInvalidBundle, true, "validate training problems", errors.New("problem knowledge weights differ from the reviewed catalog"))
		}
		problemKeys[index] = problem.ProblemKey
		problemIndex[problem.ProblemKey] = index
	}
	hash, err := hashCanonicalValue(problemSetWire{ProblemKeys: problemKeys}, maxManifestBytes, "problem set")
	if err != nil || hash != manifest.ProblemSetSHA256 {
		return nil, domainError(ErrorInvalidBundle, true, "validate training problems", errors.New("problem set hash differs"))
	}
	return problemIndex, nil
}

func equalKnowledgeWeights(left, right []TrainingKnowledgeWeight) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].KnowledgePointID != right[index].KnowledgePointID || left[index].Weight != right[index].Weight {
			return false
		}
	}
	return true
}

func validateInputInteractions(
	interactions []TrainingInteractionInput,
	actors []TrainingActorInput,
	problems []TrainingProblemInput,
	actorIndex map[string]int,
	problemIndex map[string]int,
	configuration ParsedTrainingConfiguration,
	manifest inputManifestWire,
) error {
	if len(interactions) != manifest.InteractionCount {
		return domainError(ErrorInvalidBundle, true, "validate training interactions", errors.New("interaction count differs"))
	}
	interactionIDs := make([]string, len(interactions))
	splitEntries := make([]splitEntryWire, len(interactions))
	for index, interaction := range interactions {
		if !lowercaseSHA256Pattern.MatchString(interaction.InteractionID) ||
			(index > 0 && interaction.InteractionID <= interactions[index-1].InteractionID) {
			return domainError(ErrorInvalidBundle, true, "validate training interactions", errors.New("interaction IDs are invalid or unsorted"))
		}
		if _, exists := actorIndex[interaction.ActorID]; !exists {
			return domainError(ErrorInvalidBundle, true, "validate training interactions", errors.New("interaction actor is unknown"))
		}
		if _, exists := problemIndex[interaction.ProblemKey]; !exists {
			return domainError(ErrorInvalidBundle, true, "validate training interactions", errors.New("interaction problem is unknown"))
		}
		if _, err := parseCanonicalID(interaction.SnapshotID); err != nil {
			return domainError(ErrorInvalidBundle, true, "validate training interactions", errors.New("interaction snapshot ID is invalid"))
		}
		expectedID, err := hashCanonicalValue(interactionIdentityWire{
			ActorID: interaction.ActorID, ProblemKey: interaction.ProblemKey, SnapshotID: interaction.SnapshotID,
		}, maxManifestBytes, "interaction identity")
		if err != nil || expectedID != interaction.InteractionID {
			return domainError(ErrorInvalidBundle, true, "validate training interactions", errors.New("interaction identity hash differs"))
		}
		if interaction.FirstSubmittedAt.Location() != time.UTC || interaction.LastSubmittedAt.Location() != time.UTC ||
			interaction.FirstSubmittedAt.IsZero() || interaction.LastSubmittedAt.IsZero() ||
			interaction.FirstSubmittedAt.After(interaction.LastSubmittedAt) || interaction.SubmissionCount < 1 ||
			interaction.ValidSubmissionCount < 0 || interaction.ValidSubmissionCount > interaction.SubmissionCount ||
			(interaction.Split != "train" && interaction.Split != "validation") {
			return domainError(ErrorInvalidBundle, true, "validate training interactions", errors.New("interaction evidence or split is invalid"))
		}
		rate, err := strconv.ParseFloat(string(interaction.TargetScoreRate), 64)
		if err != nil || !finiteFloat(rate) || rate < 0 || rate > 1 || interaction.Passed && rate != 1 || !interaction.Passed && rate >= 1 {
			return domainError(ErrorInvalidBundle, true, "validate training interactions", errors.New("interaction target contradicts historical passed state"))
		}
		interactionIDs[index] = interaction.InteractionID
		splitEntries[index] = splitEntryWire{InteractionID: interaction.InteractionID, Split: interaction.Split}
	}
	interactionHash, err := hashCanonicalValue(interactionSetWire{InteractionIDs: interactionIDs}, maxManifestBytes, "interaction set")
	if err != nil || interactionHash != manifest.InteractionSetSHA256 {
		return domainError(ErrorInvalidBundle, true, "validate training interactions", errors.New("interaction set hash differs"))
	}
	splitHash, err := hashCanonicalValue(splitSetWire{Interactions: splitEntries}, maxManifestBytes, "interaction split")
	if err != nil || splitHash != manifest.SplitSHA256 {
		return domainError(ErrorInvalidBundle, true, "validate training interactions", errors.New("interaction split hash differs"))
	}
	expectedInteractions := slices.Clone(interactions)
	for index := range expectedInteractions {
		expectedInteractions[index].Split = "train"
	}
	if err := assignTrainingSplit(expectedInteractions, configuration); err != nil {
		return err
	}
	trainCount := 0
	for index := range interactions {
		if interactions[index].Split != expectedInteractions[index].Split {
			return domainError(ErrorInvalidBundle, true, "validate training interactions", errors.New("interaction split was not derived by the v2 policy"))
		}
		if interactions[index].Split == "train" {
			trainCount++
		}
	}
	if trainCount != manifest.TrainInteractionCount || len(interactions)-trainCount != manifest.ValidationInteractionCount {
		return domainError(ErrorInvalidBundle, true, "validate training interactions", errors.New("interaction split counts differ"))
	}
	expectedActors := slices.Clone(actors)
	for index := range expectedActors {
		expectedActors[index].Features = nil
	}
	expectedProblems := make([]preparedProblem, len(problems))
	for index := range problems {
		expectedProblems[index].wire = problems[index]
		expectedProblems[index].wire.Features = nil
		expectedProblems[index].wire.TrainActorCount = 0
		expectedProblems[index].wire.TrainSubmissionCount = 0
	}
	if err := applyTrainingFeatures(expectedActors, expectedProblems, interactions, configuration); err != nil {
		return err
	}
	for index := range actors {
		if !equalFloatVectors(actors[index].Features, expectedActors[index].Features) {
			return domainError(ErrorInvalidBundle, true, "validate training actor features", errors.New("actor features were not derived from train interactions"))
		}
	}
	for index := range problems {
		expected := expectedProblems[index].wire
		if !equalFloatVectors(problems[index].Features, expected.Features) ||
			problems[index].TrainActorCount != expected.TrainActorCount ||
			problems[index].TrainSubmissionCount != expected.TrainSubmissionCount {
			return domainError(ErrorInvalidBundle, true, "validate training problem features", errors.New("problem features were not derived from train interactions"))
		}
	}
	return nil
}

func equalFloatVectors(left, right []float64) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		scale := math.Max(1, math.Max(math.Abs(left[index]), math.Abs(right[index])))
		if math.Abs(left[index]-right[index]) > 1e-12*scale {
			return false
		}
	}
	return true
}
