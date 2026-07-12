package recommendation

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"slices"
	"strconv"
	"strings"

	"github.com/kkkzbh/AscendAny/backend/internal/canonicaljson"
	"github.com/kkkzbh/AscendAny/backend/internal/recommendationprotocol"
)

const (
	recommendationParameterSchemaV1 = recommendationprotocol.KnowledgeMIRTParametersV1
	productionTorchVersion          = "2.13.0+cu130"
	productionAccelerator           = "cuda"
	parameterAbsoluteLimit          = 100
	normalizationTolerance          = 1e-12
	metricTolerance                 = 1e-9
	discriminationEpsilon           = 1e-6
)

type outputBundleV2Wire struct {
	Protocol            *string            `json:"protocol"`
	InputManifestSHA256 *string            `json:"inputManifestSha256"`
	Model               *outputModelV2Wire `json:"model"`
}

type outputModelV2Wire struct {
	Schema      *string         `json:"schema"`
	Manifest    json.RawMessage `json:"manifest"`
	Parameters  json.RawMessage `json:"parameters"`
	Diagnostics json.RawMessage `json:"diagnostics"`
}

type outputModelManifestV2Wire struct {
	Algorithm                   *string   `json:"algorithm"`
	ParameterSchema             *string   `json:"parameterSchema"`
	ParameterSHA256             *string   `json:"parameterSha256"`
	InputManifestSHA256         *string   `json:"inputManifestSha256"`
	TrainingConfigurationSHA256 *string   `json:"trainingConfigurationSha256"`
	KnowledgeCatalogSHA256      *string   `json:"knowledgeCatalogSha256"`
	FeatureSchemaSHA256         *string   `json:"featureSchemaSha256"`
	SplitSHA256                 *string   `json:"splitSha256"`
	KnowledgePointCount         *int64    `json:"knowledgePointCount"`
	ActorFeatureCount           *int64    `json:"actorFeatureCount"`
	ProblemFeatureCount         *int64    `json:"problemFeatureCount"`
	ActorCount                  *int64    `json:"actorCount"`
	ProblemCount                *int64    `json:"problemCount"`
	TrainInteractionCount       *int64    `json:"trainInteractionCount"`
	ValidationInteractionCount  *int64    `json:"validationInteractionCount"`
	RuntimeConstructionSHA256   *string   `json:"runtimeConstructionSha256"`
	RuntimeProvenanceSHA256     *string   `json:"runtimeProvenanceSha256"`
	RuntimeTreeSHA256           *string   `json:"runtimeTreeSha256"`
	HostCapabilitySHA256        *string   `json:"hostCapabilitySha256"`
	RuntimeAttestationSHA256    *string   `json:"runtimeAttestationSha256"`
	TorchVersion                *string   `json:"torchVersion"`
	Accelerator                 *string   `json:"accelerator"`
	Seed                        *int64    `json:"seed"`
	ConfiguredEpochs            *int64    `json:"configuredEpochs"`
	BestEpoch                   *int64    `json:"bestEpoch"`
	ActorFeatureIDs             *[]string `json:"actorFeatureIds"`
	ProblemFeatureIDs           *[]string `json:"problemFeatureIds"`
}

type outputParametersV2Wire struct {
	Normalization         *outputNormalizationV2Wire       `json:"normalization"`
	StudentFeatureWeights *[][]json.Number                 `json:"studentFeatureWeights"`
	ActorResiduals        *[]outputActorResidualV2Wire     `json:"actorResiduals"`
	ProblemFeatureWeights *[]json.Number                   `json:"problemFeatureWeights"`
	Problems              *[]outputProblemParametersV2Wire `json:"problems"`
}

type outputNormalizationV2Wire struct {
	ActorMeans    *[]json.Number `json:"actorMeans"`
	ActorScales   *[]json.Number `json:"actorScales"`
	ProblemMeans  *[]json.Number `json:"problemMeans"`
	ProblemScales *[]json.Number `json:"problemScales"`
}

type outputActorResidualV2Wire struct {
	ActorID *string        `json:"actorId"`
	Values  *[]json.Number `json:"values"`
}

type outputProblemParametersV2Wire struct {
	ProblemKey         *string      `json:"problemKey"`
	DifficultyResidual *json.Number `json:"difficultyResidual"`
	RawDiscrimination  *json.Number `json:"rawDiscrimination"`
}

type outputDiagnosticsV2Wire struct {
	EpochsCompleted                   *int64       `json:"epochsCompleted"`
	BestEpoch                         *int64       `json:"bestEpoch"`
	InitialTrainLogLoss               *json.Number `json:"initialTrainLogLoss"`
	FinalTrainLogLoss                 *json.Number `json:"finalTrainLogLoss"`
	ReportedBaselineValidationLogLoss *json.Number `json:"reportedBaselineValidationLogLoss"`
	ReportedValidationLogLoss         *json.Number `json:"reportedValidationLogLoss"`
	ReportedValidationBrier           *json.Number `json:"reportedValidationBrier"`
}

type parsedOutputParametersV2 struct {
	actorMeans             []float64
	actorScales            []float64
	problemMeans           []float64
	problemScales          []float64
	studentFeatureWeights  [][]float64
	actorResiduals         [][]float64
	problemFeatureWeights  []float64
	problemDifficulties    []float64
	problemDiscriminations []float64
}

type modelQualityMetricsV2 struct {
	initialTrainLogLoss       float64
	finalTrainLogLoss         float64
	baselineValidationLogLoss float64
	validationLogLoss         float64
	validationBrier           float64
}

type weightedKnowledgeIndex struct {
	index  int
	weight float64
}

type outputModelEvaluatorV2 struct {
	input                     ParsedInputBundle
	parameters                parsedOutputParametersV2
	actorIndex                map[string]int
	problemIndex              map[string]int
	knowledgeIndex            map[string]int
	problemKnowledge          [][]weightedKnowledgeIndex
	problemNormalizedFeatures [][]float64
	problemDifficulties       []float64
	problemDiscriminations    []float64
	interactionsByActor       [][]int
	trainInteractionCounts    []int64
	baselineProbabilities     []float64
}

type studentResultBodyV2 struct {
	Status           RecommendationResultStatus         `json:"status"`
	SourceRating     json.Number                        `json:"sourceRating"`
	Evidence         RecommendationEvidenceV2           `json:"evidence"`
	KnowledgeMastery []RecommendationKnowledgeMasteryV2 `json:"knowledgeMastery"`
	LearningPath     []RecommendationLearningPathStepV2 `json:"learningPath,omitempty"`
	Insufficiency    *RecommendationInsufficiencyV2     `json:"insufficiency,omitempty"`
}

// ParseOutputBundle validates the complete trainer-owned numerical artifact,
// independently recomputes its quality metrics, and materializes every
// student recommendation under the Go-owned path and ranking policy.
func ParseOutputBundle(raw json.RawMessage, maximumBytes int, input ParsedInputBundle) (ParsedOutputBundle, error) {
	if maximumBytes <= 0 {
		return ParsedOutputBundle{}, invalidOutput(errors.New("positive maximum output bytes are required"))
	}
	canonical, digest, err := requireCanonicalObject(raw, maximumBytes, "recommendation training output")
	if err != nil {
		return ParsedOutputBundle{}, err
	}
	var wire outputBundleV2Wire
	if err := decodeStrict(canonical, &wire); err != nil {
		return ParsedOutputBundle{}, invalidOutput(fmt.Errorf("decode output: %w", err))
	}
	if wire.Protocol == nil || wire.InputManifestSHA256 == nil || wire.Model == nil ||
		wire.Model.Schema == nil || len(wire.Model.Manifest) == 0 || len(wire.Model.Parameters) == 0 || len(wire.Model.Diagnostics) == 0 {
		return ParsedOutputBundle{}, invalidOutput(errors.New("every training output field is required"))
	}
	if *wire.Protocol != TrainingOutputProtocolV2 || *wire.Model.Schema != ModelSchemaV2 {
		return ParsedOutputBundle{}, invalidOutput(errors.New("output protocol or model schema is unsupported"))
	}
	if !lowercaseSHA256Pattern.MatchString(*wire.InputManifestSHA256) || *wire.InputManifestSHA256 != input.ManifestSHA256 {
		return ParsedOutputBundle{}, invalidOutput(errors.New("output input manifest differs from the verified input bundle"))
	}
	if input.TrainingConfiguration.Accelerator != productionAccelerator {
		return ParsedOutputBundle{}, invalidOutput(errors.New("production recommendation training requires the CUDA accelerator"))
	}

	inputManifest, err := decodeVerifiedInputManifest(input)
	if err != nil {
		return ParsedOutputBundle{}, invalidOutput(err)
	}
	modelManifest, modelManifestJSON, modelManifestSHA256, err := parseOutputModelManifest(wire.Model.Manifest, input, inputManifest)
	if err != nil {
		return ParsedOutputBundle{}, invalidOutput(err)
	}
	parameters, err := parseOutputParameters(wire.Model.Parameters, maximumBytes, *modelManifest.ParameterSHA256, input)
	if err != nil {
		return ParsedOutputBundle{}, invalidOutput(err)
	}
	diagnostics, diagnosticsJSON, err := parseOutputDiagnostics(wire.Model.Diagnostics, modelManifest, input.TrainingConfiguration)
	if err != nil {
		return ParsedOutputBundle{}, invalidOutput(err)
	}
	evaluator, err := newOutputModelEvaluator(input, parameters)
	if err != nil {
		return ParsedOutputBundle{}, invalidOutput(err)
	}
	recomputed, err := evaluator.recomputeQualityMetrics()
	if err != nil {
		return ParsedOutputBundle{}, invalidOutput(err)
	}
	if err := verifyModelQuality(diagnostics, recomputed, input.TrainingConfiguration); err != nil {
		return ParsedOutputBundle{}, invalidOutput(err)
	}
	results, err := evaluator.materializeResults()
	if err != nil {
		return ParsedOutputBundle{}, invalidOutput(err)
	}
	return ParsedOutputBundle{
		CanonicalJSON: canonical, SHA256: digest, InputManifestSHA256: *wire.InputManifestSHA256,
		Model: ModelOutput{
			Schema: *wire.Model.Schema, Manifest: modelManifestJSON, ManifestSHA256: modelManifestSHA256, Metrics: diagnosticsJSON,
			RuntimeConstructionSHA256: *modelManifest.RuntimeConstructionSHA256,
			RuntimeProvenanceSHA256:   *modelManifest.RuntimeProvenanceSHA256,
			RuntimeTreeSHA256:         *modelManifest.RuntimeTreeSHA256,
			HostCapabilitySHA256:      *modelManifest.HostCapabilitySHA256,
			RuntimeAttestationSHA256:  *modelManifest.RuntimeAttestationSHA256,
		},
		Results: results,
	}, nil
}

func invalidOutput(cause error) error {
	return domainError(ErrorInvalidBundle, true, "validate recommendation training output", cause)
}

func decodeVerifiedInputManifest(input ParsedInputBundle) (inputManifestWire, error) {
	if len(input.Manifest) == 0 || !lowercaseSHA256Pattern.MatchString(input.ManifestSHA256) {
		return inputManifestWire{}, errors.New("verified input manifest is absent")
	}
	canonical, digest, err := canonicaljson.Object(input.Manifest, maxManifestBytes)
	if err != nil || digest != input.ManifestSHA256 || !slices.Equal(canonical, input.Manifest) {
		return inputManifestWire{}, errors.New("verified input manifest bytes or digest are inconsistent")
	}
	var manifest inputManifestWire
	if err := decodeStrict(canonical, &manifest); err != nil {
		return inputManifestWire{}, fmt.Errorf("decode verified input manifest: %w", err)
	}
	return manifest, nil
}

func parseOutputModelManifest(
	raw json.RawMessage,
	input ParsedInputBundle,
	inputManifest inputManifestWire,
) (outputModelManifestV2Wire, json.RawMessage, string, error) {
	canonical, digest, err := canonicaljson.Object(raw, maxModelManifestBytes)
	if err != nil {
		return outputModelManifestV2Wire{}, nil, "", fmt.Errorf("canonicalize model manifest: %w", err)
	}
	var value outputModelManifestV2Wire
	if err := decodeStrict(canonical, &value); err != nil {
		return outputModelManifestV2Wire{}, nil, "", fmt.Errorf("decode model manifest: %w", err)
	}
	if value.Algorithm == nil || value.ParameterSchema == nil || value.ParameterSHA256 == nil ||
		value.InputManifestSHA256 == nil || value.TrainingConfigurationSHA256 == nil ||
		value.KnowledgeCatalogSHA256 == nil || value.FeatureSchemaSHA256 == nil || value.SplitSHA256 == nil ||
		value.KnowledgePointCount == nil || value.ActorFeatureCount == nil || value.ProblemFeatureCount == nil ||
		value.ActorCount == nil || value.ProblemCount == nil || value.TrainInteractionCount == nil ||
		value.ValidationInteractionCount == nil || value.RuntimeConstructionSHA256 == nil ||
		value.RuntimeProvenanceSHA256 == nil || value.RuntimeTreeSHA256 == nil ||
		value.HostCapabilitySHA256 == nil || value.RuntimeAttestationSHA256 == nil ||
		value.TorchVersion == nil || value.Accelerator == nil ||
		value.Seed == nil || value.ConfiguredEpochs == nil || value.BestEpoch == nil ||
		value.ActorFeatureIDs == nil || value.ProblemFeatureIDs == nil {
		return outputModelManifestV2Wire{}, nil, "", errors.New("every model manifest field is required")
	}
	if *value.Algorithm != trainingAlgorithmV2 || *value.ParameterSchema != recommendationParameterSchemaV1 ||
		!lowercaseSHA256Pattern.MatchString(*value.ParameterSHA256) ||
		*value.InputManifestSHA256 != input.ManifestSHA256 ||
		*value.TrainingConfigurationSHA256 != inputManifest.TrainingConfiguration.DocumentSHA256 ||
		*value.KnowledgeCatalogSHA256 != inputManifest.KnowledgeCatalog.DocumentSHA256 ||
		*value.FeatureSchemaSHA256 != inputManifest.FeatureSchemaSHA256 || *value.SplitSHA256 != inputManifest.SplitSHA256 {
		return outputModelManifestV2Wire{}, nil, "", errors.New("model manifest provenance differs from the input bundle")
	}
	trainCount, validationCount := interactionSplitCounts(input.Interactions)
	if *value.KnowledgePointCount != int64(len(input.KnowledgePoints)) ||
		*value.ActorFeatureCount != int64(len(input.FeatureSchema.ActorFeatureIDs)) ||
		*value.ProblemFeatureCount != int64(len(input.FeatureSchema.ProblemFeatureIDs)) ||
		*value.ActorCount != int64(len(input.Actors)) || *value.ProblemCount != int64(len(input.Problems)) ||
		*value.TrainInteractionCount != trainCount || *value.ValidationInteractionCount != validationCount ||
		*value.KnowledgePointCount != int64(inputManifest.KnowledgePointCount) ||
		*value.ActorCount != int64(inputManifest.ActorCount) || *value.ProblemCount != int64(inputManifest.ProblemCount) ||
		*value.TrainInteractionCount != int64(inputManifest.TrainInteractionCount) ||
		*value.ValidationInteractionCount != int64(inputManifest.ValidationInteractionCount) {
		return outputModelManifestV2Wire{}, nil, "", errors.New("model manifest collection counts differ from the input bundle")
	}
	if *value.TorchVersion != productionTorchVersion || *value.Accelerator != productionAccelerator ||
		!lowercaseSHA256Pattern.MatchString(*value.RuntimeConstructionSHA256) ||
		!lowercaseSHA256Pattern.MatchString(*value.RuntimeProvenanceSHA256) ||
		!lowercaseSHA256Pattern.MatchString(*value.RuntimeTreeSHA256) ||
		!lowercaseSHA256Pattern.MatchString(*value.HostCapabilitySHA256) ||
		!lowercaseSHA256Pattern.MatchString(*value.RuntimeAttestationSHA256) ||
		*value.Accelerator != input.TrainingConfiguration.Accelerator ||
		*value.Seed != input.TrainingConfiguration.Seed || *value.ConfiguredEpochs != input.TrainingConfiguration.Epochs ||
		!slices.Equal(*value.ActorFeatureIDs, input.FeatureSchema.ActorFeatureIDs) ||
		!slices.Equal(*value.ProblemFeatureIDs, input.FeatureSchema.ProblemFeatureIDs) {
		return outputModelManifestV2Wire{}, nil, "", errors.New("model runtime or feature manifest differs from the production contract")
	}
	return value, canonical, digest, nil
}

func interactionSplitCounts(interactions []TrainingInteractionInput) (int64, int64) {
	var train, validation int64
	for _, interaction := range interactions {
		if interaction.Split == "train" {
			train++
		} else if interaction.Split == "validation" {
			validation++
		}
	}
	return train, validation
}

func parseOutputParameters(raw json.RawMessage, maximumBytes int, expectedSHA256 string, input ParsedInputBundle) (parsedOutputParametersV2, error) {
	canonical, digest, err := canonicaljson.Object(raw, maximumBytes)
	if err != nil {
		return parsedOutputParametersV2{}, fmt.Errorf("canonicalize model parameters: %w", err)
	}
	if digest != expectedSHA256 {
		return parsedOutputParametersV2{}, errors.New("model parameter digest differs from its manifest")
	}
	var wire outputParametersV2Wire
	if err := decodeStrict(canonical, &wire); err != nil {
		return parsedOutputParametersV2{}, fmt.Errorf("decode model parameters: %w", err)
	}
	if wire.Normalization == nil || wire.StudentFeatureWeights == nil || wire.ActorResiduals == nil ||
		wire.ProblemFeatureWeights == nil || wire.Problems == nil ||
		wire.Normalization.ActorMeans == nil || wire.Normalization.ActorScales == nil ||
		wire.Normalization.ProblemMeans == nil || wire.Normalization.ProblemScales == nil {
		return parsedOutputParametersV2{}, errors.New("every model parameter field is required")
	}
	actorFeatureCount := len(input.FeatureSchema.ActorFeatureIDs)
	problemFeatureCount := len(input.FeatureSchema.ProblemFeatureIDs)
	knowledgeCount := len(input.KnowledgePoints)
	actorCount := len(input.Actors)
	problemCount := len(input.Problems)
	actorMeans, err := parseBoundedVector(*wire.Normalization.ActorMeans, actorFeatureCount, false, "actorMeans")
	if err != nil {
		return parsedOutputParametersV2{}, err
	}
	actorScales, err := parseBoundedVector(*wire.Normalization.ActorScales, actorFeatureCount, true, "actorScales")
	if err != nil {
		return parsedOutputParametersV2{}, err
	}
	problemMeans, err := parseBoundedVector(*wire.Normalization.ProblemMeans, problemFeatureCount, false, "problemMeans")
	if err != nil {
		return parsedOutputParametersV2{}, err
	}
	problemScales, err := parseBoundedVector(*wire.Normalization.ProblemScales, problemFeatureCount, true, "problemScales")
	if err != nil {
		return parsedOutputParametersV2{}, err
	}
	expectedActorMeans, expectedActorScales, err := populationNormalization(actorFeatureRows(input.Actors), actorFeatureCount)
	if err != nil || !vectorsClose(actorMeans, expectedActorMeans, normalizationTolerance) ||
		!vectorsClose(actorScales, expectedActorScales, normalizationTolerance) {
		return parsedOutputParametersV2{}, errors.New("actor normalization differs from the verified input features")
	}
	expectedProblemMeans, expectedProblemScales, err := populationNormalization(problemFeatureRows(input.Problems), problemFeatureCount)
	if err != nil || !vectorsClose(problemMeans, expectedProblemMeans, normalizationTolerance) ||
		!vectorsClose(problemScales, expectedProblemScales, normalizationTolerance) {
		return parsedOutputParametersV2{}, errors.New("problem normalization differs from the verified input features")
	}
	studentWeights, err := parseBoundedMatrix(*wire.StudentFeatureWeights, knowledgeCount, actorFeatureCount, "studentFeatureWeights")
	if err != nil {
		return parsedOutputParametersV2{}, err
	}
	if len(*wire.ActorResiduals) != actorCount {
		return parsedOutputParametersV2{}, errors.New("actor residual count differs from the verified actor set")
	}
	actorResiduals := make([][]float64, actorCount)
	for index, residual := range *wire.ActorResiduals {
		if residual.ActorID == nil || residual.Values == nil || *residual.ActorID != input.Actors[index].ActorID {
			return parsedOutputParametersV2{}, errors.New("actor residual identities must exactly follow verified actor order")
		}
		actorResiduals[index], err = parseBoundedVector(*residual.Values, knowledgeCount, false, fmt.Sprintf("actorResiduals[%d]", index))
		if err != nil {
			return parsedOutputParametersV2{}, err
		}
	}
	problemFeatureWeights, err := parseBoundedVector(*wire.ProblemFeatureWeights, problemFeatureCount, false, "problemFeatureWeights")
	if err != nil {
		return parsedOutputParametersV2{}, err
	}
	if len(*wire.Problems) != problemCount {
		return parsedOutputParametersV2{}, errors.New("problem parameter count differs from the verified problem set")
	}
	problemDifficulties := make([]float64, problemCount)
	problemDiscriminations := make([]float64, problemCount)
	for index, problem := range *wire.Problems {
		if problem.ProblemKey == nil || problem.DifficultyResidual == nil || problem.RawDiscrimination == nil ||
			*problem.ProblemKey != input.Problems[index].ProblemKey {
			return parsedOutputParametersV2{}, errors.New("problem parameter identities must exactly follow verified problem order")
		}
		problemDifficulties[index], err = parseBoundedNumber(*problem.DifficultyResidual, fmt.Sprintf("problems[%d].difficultyResidual", index))
		if err != nil {
			return parsedOutputParametersV2{}, err
		}
		rawDiscrimination, parseErr := parseBoundedNumber(*problem.RawDiscrimination, fmt.Sprintf("problems[%d].rawDiscrimination", index))
		if parseErr != nil {
			return parsedOutputParametersV2{}, parseErr
		}
		problemDiscriminations[index] = stableSoftplus(rawDiscrimination) + discriminationEpsilon
		if !finiteFloat(problemDiscriminations[index]) || problemDiscriminations[index] <= 0 {
			return parsedOutputParametersV2{}, errors.New("problem discrimination is invalid")
		}
	}
	return parsedOutputParametersV2{
		actorMeans: actorMeans, actorScales: actorScales, problemMeans: problemMeans, problemScales: problemScales,
		studentFeatureWeights: studentWeights, actorResiduals: actorResiduals,
		problemFeatureWeights: problemFeatureWeights, problemDifficulties: problemDifficulties,
		problemDiscriminations: problemDiscriminations,
	}, nil
}

func parseBoundedMatrix(values [][]json.Number, rows, columns int, label string) ([][]float64, error) {
	if len(values) != rows {
		return nil, fmt.Errorf("%s row count differs from the model shape", label)
	}
	parsed := make([][]float64, rows)
	var err error
	for row := range values {
		parsed[row], err = parseBoundedVector(values[row], columns, false, fmt.Sprintf("%s[%d]", label, row))
		if err != nil {
			return nil, err
		}
	}
	return parsed, nil
}

func parseBoundedVector(values []json.Number, size int, positive bool, label string) ([]float64, error) {
	if len(values) != size {
		return nil, fmt.Errorf("%s length differs from the model shape", label)
	}
	parsed := make([]float64, size)
	for index, value := range values {
		var err error
		parsed[index], err = parseBoundedNumber(value, fmt.Sprintf("%s[%d]", label, index))
		if err != nil {
			return nil, err
		}
		if positive && parsed[index] <= 0 {
			return nil, fmt.Errorf("%s[%d] must be positive", label, index)
		}
	}
	return parsed, nil
}

func parseBoundedNumber(value json.Number, label string) (float64, error) {
	parsed, err := strconv.ParseFloat(string(value), 64)
	if err != nil || !finiteFloat(parsed) || math.Abs(parsed) > parameterAbsoluteLimit {
		return 0, fmt.Errorf("%s is non-finite or exceeds its numerical bound", label)
	}
	return parsed, nil
}

func actorFeatureRows(values []TrainingActorInput) [][]float64 {
	rows := make([][]float64, len(values))
	for index := range values {
		rows[index] = values[index].Features
	}
	return rows
}

func problemFeatureRows(values []TrainingProblemInput) [][]float64 {
	rows := make([][]float64, len(values))
	for index := range values {
		rows[index] = values[index].Features
	}
	return rows
}

func populationNormalization(rows [][]float64, columns int) ([]float64, []float64, error) {
	if len(rows) == 0 || columns == 0 {
		return nil, nil, errors.New("normalization corpus is empty")
	}
	means := make([]float64, columns)
	scales := make([]float64, columns)
	terms := make([]float64, len(rows))
	for column := 0; column < columns; column++ {
		for row := range rows {
			if len(rows[row]) != columns || !finiteFloat(rows[row][column]) {
				return nil, nil, errors.New("normalization feature shape or value is invalid")
			}
			terms[row] = rows[row][column]
		}
		means[column] = accuratelySum(terms) / float64(len(rows))
		for row := range rows {
			delta := rows[row][column] - means[column]
			terms[row] = delta * delta
		}
		scales[column] = math.Sqrt(accuratelySum(terms) / float64(len(rows)))
		if scales[column] == 0 {
			scales[column] = 1
		}
	}
	return means, scales, nil
}

func accuratelySum(values []float64) float64 {
	total := new(big.Float).SetPrec(256).SetMode(big.ToNearestEven)
	term := new(big.Float).SetPrec(256).SetMode(big.ToNearestEven)
	for _, value := range values {
		term.SetFloat64(value)
		total.Add(total, term)
	}
	result, _ := total.Float64()
	return result
}

func vectorsClose(actual, expected []float64, tolerance float64) bool {
	if len(actual) != len(expected) {
		return false
	}
	for index := range actual {
		if math.Abs(actual[index]-expected[index]) > tolerance {
			return false
		}
	}
	return true
}

func stableSoftplus(value float64) float64 {
	if value > 0 {
		return value + math.Log1p(math.Exp(-value))
	}
	return math.Log1p(math.Exp(value))
}

func parseOutputDiagnostics(
	raw json.RawMessage,
	manifest outputModelManifestV2Wire,
	configuration ParsedTrainingConfiguration,
) (outputDiagnosticsV2Wire, json.RawMessage, error) {
	canonical, _, err := canonicaljson.Object(raw, maxMetricsBytes)
	if err != nil {
		return outputDiagnosticsV2Wire{}, nil, fmt.Errorf("canonicalize model diagnostics: %w", err)
	}
	var value outputDiagnosticsV2Wire
	if err := decodeStrict(canonical, &value); err != nil {
		return outputDiagnosticsV2Wire{}, nil, fmt.Errorf("decode model diagnostics: %w", err)
	}
	if value.EpochsCompleted == nil || value.BestEpoch == nil || value.InitialTrainLogLoss == nil ||
		value.FinalTrainLogLoss == nil || value.ReportedBaselineValidationLogLoss == nil ||
		value.ReportedValidationLogLoss == nil || value.ReportedValidationBrier == nil {
		return outputDiagnosticsV2Wire{}, nil, errors.New("every model diagnostic field is required")
	}
	if *value.EpochsCompleted < 1 || *value.EpochsCompleted > configuration.Epochs ||
		*value.BestEpoch < 1 || *value.BestEpoch > *value.EpochsCompleted || *value.BestEpoch != *manifest.BestEpoch {
		return outputDiagnosticsV2Wire{}, nil, errors.New("model epoch diagnostics violate the configured training bounds")
	}
	for label, number := range map[string]json.Number{
		"initialTrainLogLoss":               *value.InitialTrainLogLoss,
		"finalTrainLogLoss":                 *value.FinalTrainLogLoss,
		"reportedBaselineValidationLogLoss": *value.ReportedBaselineValidationLogLoss,
		"reportedValidationLogLoss":         *value.ReportedValidationLogLoss,
		"reportedValidationBrier":           *value.ReportedValidationBrier,
	} {
		parsed, parseErr := strconv.ParseFloat(string(number), 64)
		if parseErr != nil || !finiteFloat(parsed) || parsed < 0 || (label != "reportedValidationBrier" && parsed == 0) ||
			(label == "reportedValidationBrier" && parsed > 1) {
			return outputDiagnosticsV2Wire{}, nil, fmt.Errorf("%s is outside its numerical contract", label)
		}
	}
	return value, canonical, nil
}

func newOutputModelEvaluator(input ParsedInputBundle, parameters parsedOutputParametersV2) (*outputModelEvaluatorV2, error) {
	actorIndex := make(map[string]int, len(input.Actors))
	for index, actor := range input.Actors {
		actorIndex[actor.ActorID] = index
	}
	problemIndex := make(map[string]int, len(input.Problems))
	for index, problem := range input.Problems {
		problemIndex[problem.ProblemKey] = index
	}
	knowledgeIndex := make(map[string]int, len(input.KnowledgePoints))
	for index, knowledge := range input.KnowledgePoints {
		knowledgeIndex[knowledge.ID] = index
	}
	problemKnowledge := make([][]weightedKnowledgeIndex, len(input.Problems))
	problemNormalized := make([][]float64, len(input.Problems))
	problemDifficulties := make([]float64, len(input.Problems))
	for index, problem := range input.Problems {
		problemNormalized[index] = normalizeVector(problem.Features, parameters.problemMeans, parameters.problemScales)
		problemDifficulties[index] = dot(parameters.problemFeatureWeights, problemNormalized[index]) + parameters.problemDifficulties[index]
		if !finiteFloat(problemDifficulties[index]) {
			return nil, errors.New("derived problem difficulty is non-finite")
		}
		problemKnowledge[index] = make([]weightedKnowledgeIndex, len(problem.KnowledgeWeights))
		for weightIndex, weight := range problem.KnowledgeWeights {
			knowledgePosition, exists := knowledgeIndex[weight.KnowledgePointID]
			weightValue, parseErr := strconv.ParseFloat(string(weight.Weight), 64)
			if !exists || parseErr != nil || !finiteFloat(weightValue) || weightValue <= 0 {
				return nil, errors.New("verified problem knowledge weights are invalid")
			}
			problemKnowledge[index][weightIndex] = weightedKnowledgeIndex{index: knowledgePosition, weight: weightValue}
		}
	}
	interactionsByActor := make([][]int, len(input.Actors))
	trainCounts := make([]int64, len(input.Problems))
	trainTargetsByProblem := make([][]float64, len(input.Problems))
	for index, interaction := range input.Interactions {
		actorPosition, actorExists := actorIndex[interaction.ActorID]
		problemPosition, problemExists := problemIndex[interaction.ProblemKey]
		if !actorExists || !problemExists {
			return nil, errors.New("verified interaction contains a dangling model identity")
		}
		interactionsByActor[actorPosition] = append(interactionsByActor[actorPosition], index)
		if interaction.Split == "train" {
			target, parseErr := strconv.ParseFloat(string(interaction.TargetScoreRate), 64)
			if parseErr != nil || !finiteFloat(target) || target < 0 || target > 1 {
				return nil, errors.New("verified training target is invalid")
			}
			trainCounts[problemPosition]++
			trainTargetsByProblem[problemPosition] = append(trainTargetsByProblem[problemPosition], target)
		}
	}
	baseline := make([]float64, len(input.Problems))
	for index := range input.Problems {
		if trainCounts[index] < input.TrainingConfiguration.MinProblemInteractions {
			return nil, errors.New("verified problem lacks its configured training evidence")
		}
		baseline[index] = (accuratelySum(trainTargetsByProblem[index]) + 1) / float64(trainCounts[index]+2)
		if baseline[index] <= 0 || baseline[index] >= 1 || !finiteFloat(baseline[index]) {
			return nil, errors.New("derived problem baseline is invalid")
		}
	}
	return &outputModelEvaluatorV2{
		input: input, parameters: parameters, actorIndex: actorIndex, problemIndex: problemIndex,
		knowledgeIndex: knowledgeIndex, problemKnowledge: problemKnowledge,
		problemNormalizedFeatures: problemNormalized, problemDifficulties: problemDifficulties,
		problemDiscriminations: parameters.problemDiscriminations, interactionsByActor: interactionsByActor,
		trainInteractionCounts: trainCounts, baselineProbabilities: baseline,
	}, nil
}

func normalizeVector(values, means, scales []float64) []float64 {
	normalized := make([]float64, len(values))
	for index := range values {
		normalized[index] = (values[index] - means[index]) / scales[index]
	}
	return normalized
}

func dot(left, right []float64) float64 {
	terms := make([]float64, len(left))
	for index := range left {
		terms[index] = left[index] * right[index]
	}
	return accuratelySum(terms)
}

func (e *outputModelEvaluatorV2) actorTheta(actorIndex int) ([]float64, error) {
	features := normalizeVector(e.input.Actors[actorIndex].Features, e.parameters.actorMeans, e.parameters.actorScales)
	theta := make([]float64, len(e.input.KnowledgePoints))
	for knowledgeIndex := range theta {
		theta[knowledgeIndex] = dot(e.parameters.studentFeatureWeights[knowledgeIndex], features) + e.parameters.actorResiduals[actorIndex][knowledgeIndex]
		if !finiteFloat(theta[knowledgeIndex]) {
			return nil, errors.New("derived actor mastery logit is non-finite")
		}
	}
	return theta, nil
}

func (e *outputModelEvaluatorV2) logit(theta []float64, problemIndex int) (float64, error) {
	terms := make([]float64, len(e.problemKnowledge[problemIndex]))
	for index, weight := range e.problemKnowledge[problemIndex] {
		terms[index] = weight.weight * theta[weight.index]
	}
	value := e.problemDiscriminations[problemIndex] * (accuratelySum(terms) - e.problemDifficulties[problemIndex])
	if !finiteFloat(value) {
		return 0, errors.New("derived model logit is non-finite")
	}
	return value, nil
}

func (e *outputModelEvaluatorV2) recomputeQualityMetrics() (modelQualityMetricsV2, error) {
	initialTrain := make([]float64, 0)
	finalTrain := make([]float64, 0)
	baselineValidation := make([]float64, 0)
	validation := make([]float64, 0)
	validationBrier := make([]float64, 0)
	for actorIndex, interactionIndices := range e.interactionsByActor {
		theta, err := e.actorTheta(actorIndex)
		if err != nil {
			return modelQualityMetricsV2{}, err
		}
		for _, interactionIndex := range interactionIndices {
			interaction := e.input.Interactions[interactionIndex]
			problemIndex := e.problemIndex[interaction.ProblemKey]
			target, err := strconv.ParseFloat(string(interaction.TargetScoreRate), 64)
			if err != nil || !finiteFloat(target) || target < 0 || target > 1 {
				return modelQualityMetricsV2{}, errors.New("verified interaction target is invalid")
			}
			logit, err := e.logit(theta, problemIndex)
			if err != nil {
				return modelQualityMetricsV2{}, err
			}
			if interaction.Split == "train" {
				initialTrain = append(initialTrain, binaryLogLossProbability(e.baselineProbabilities[problemIndex], target))
				finalTrain = append(finalTrain, binaryLogLossLogit(logit, target))
			} else {
				baselineValidation = append(baselineValidation, binaryLogLossProbability(e.baselineProbabilities[problemIndex], target))
				validation = append(validation, binaryLogLossLogit(logit, target))
				probability := stableSigmoid(logit)
				validationBrier = append(validationBrier, (probability-target)*(probability-target))
			}
		}
	}
	if len(initialTrain) == 0 || len(validation) == 0 {
		return modelQualityMetricsV2{}, errors.New("verified train or validation metric corpus is empty")
	}
	return modelQualityMetricsV2{
		initialTrainLogLoss: mean(initialTrain), finalTrainLogLoss: mean(finalTrain),
		baselineValidationLogLoss: mean(baselineValidation), validationLogLoss: mean(validation),
		validationBrier: mean(validationBrier),
	}, nil
}

func binaryLogLossProbability(probability, target float64) float64 {
	return -(target*math.Log(probability) + (1-target)*math.Log1p(-probability))
}

func binaryLogLossLogit(logit, target float64) float64 {
	return math.Max(logit, 0) - logit*target + math.Log1p(math.Exp(-math.Abs(logit)))
}

func mean(values []float64) float64 {
	return accuratelySum(values) / float64(len(values))
}

func stableSigmoid(value float64) float64 {
	if value >= 0 {
		exponential := math.Exp(-value)
		return 1 / (1 + exponential)
	}
	exponential := math.Exp(value)
	return exponential / (1 + exponential)
}

func verifyModelQuality(reported outputDiagnosticsV2Wire, actual modelQualityMetricsV2, configuration ParsedTrainingConfiguration) error {
	checks := []struct {
		label    string
		reported json.Number
		actual   float64
	}{
		{"initialTrainLogLoss", *reported.InitialTrainLogLoss, actual.initialTrainLogLoss},
		{"finalTrainLogLoss", *reported.FinalTrainLogLoss, actual.finalTrainLogLoss},
		{"reportedBaselineValidationLogLoss", *reported.ReportedBaselineValidationLogLoss, actual.baselineValidationLogLoss},
		{"reportedValidationLogLoss", *reported.ReportedValidationLogLoss, actual.validationLogLoss},
		{"reportedValidationBrier", *reported.ReportedValidationBrier, actual.validationBrier},
	}
	for _, check := range checks {
		value, _ := strconv.ParseFloat(string(check.reported), 64)
		scale := math.Max(1, math.Max(math.Abs(value), math.Abs(check.actual)))
		if !finiteFloat(check.actual) || math.Abs(value-check.actual) > metricTolerance*scale {
			return fmt.Errorf("%s differs from the Go recomputation", check.label)
		}
	}
	minimumImprovement, err := strconv.ParseFloat(string(configuration.Validation.MinRelativeLogLossImprovement), 64)
	if err != nil || !finiteFloat(minimumImprovement) {
		return errors.New("verified validation quality threshold is invalid")
	}
	improvement := (actual.baselineValidationLogLoss - actual.validationLogLoss) / actual.baselineValidationLogLoss
	if !finiteFloat(improvement) || improvement < minimumImprovement {
		return errors.New("model does not meet the configured relative validation log-loss improvement")
	}
	return nil
}

func (e *outputModelEvaluatorV2) materializeResults() ([]StudentOutput, error) {
	results := make([]StudentOutput, len(e.input.Actors))
	for actorIndex := range e.input.Actors {
		body, err := e.materializeActorResult(actorIndex)
		if err != nil {
			return nil, err
		}
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encode actor %s result: %w", e.input.Actors[actorIndex].ActorID, err)
		}
		canonical, digest, err := canonicaljson.Object(raw, maxStudentResultBytes)
		if err != nil {
			return nil, fmt.Errorf("canonicalize actor %s result: %w", e.input.Actors[actorIndex].ActorID, err)
		}
		actorID, err := parseCanonicalID(e.input.Actors[actorIndex].ActorID)
		if err != nil {
			return nil, errors.New("verified actor ID is invalid")
		}
		results[actorIndex] = StudentOutput{ActorID: actorID, Schema: ResultSchemaV2, Result: canonical, ResultSHA256: digest}
	}
	return results, nil
}

func (e *outputModelEvaluatorV2) materializeActorResult(actorIndex int) (studentResultBodyV2, error) {
	theta, err := e.actorTheta(actorIndex)
	if err != nil {
		return studentResultBodyV2{}, err
	}
	mastery := make([]float64, len(theta))
	for index, value := range theta {
		mastery[index] = stableSigmoid(value)
	}
	evidence, knowledgeCounts := e.actorEvidence(actorIndex)
	masteryOutput := make([]RecommendationKnowledgeMasteryV2, len(e.input.KnowledgePoints))
	for index, knowledge := range e.input.KnowledgePoints {
		masteryOutput[index] = RecommendationKnowledgeMasteryV2{
			KnowledgePointID: knowledge.ID, Label: knowledge.Label, Description: knowledge.Description,
			PrerequisiteIDs: slices.Clone(knowledge.PrerequisiteIDs), Mastery: resultNumber(mastery[index]),
			TrainInteractionCount: knowledgeCounts[index],
		}
	}
	rating := json.Number(string(e.input.Actors[actorIndex].CurrentRating))
	pathIndices, directTargets, reason := e.buildKnowledgePath(mastery)
	if reason != "" {
		return studentResultBodyV2{
			Status: RecommendationResultInsufficient, SourceRating: rating, Evidence: evidence, KnowledgeMastery: masteryOutput,
			Insufficiency: e.insufficiency(reason, pathIndices, nil, actorIndex),
		}, nil
	}
	steps, blocked, eligible, err := e.rankPathProblems(actorIndex, theta, mastery, pathIndices, directTargets)
	if err != nil {
		return studentResultBodyV2{}, err
	}
	if len(blocked) != 0 {
		return studentResultBodyV2{
			Status: RecommendationResultInsufficient, SourceRating: rating, Evidence: evidence, KnowledgeMastery: masteryOutput,
			Insufficiency: &RecommendationInsufficiencyV2{
				ReasonCode: "problem_candidates_below_minimum", MinimumPathSteps: e.input.TrainingConfiguration.PathPolicy.MinSteps,
				CandidatePathSteps: int64(len(pathIndices)), ProblemsPerStep: e.input.TrainingConfiguration.PathPolicy.ProblemsPerStep,
				EligibleProblemCount: eligible, BlockedKnowledgePointIDs: blocked,
			},
		}, nil
	}
	return studentResultBodyV2{
		Status: RecommendationResultReady, SourceRating: rating, Evidence: evidence, KnowledgeMastery: masteryOutput,
		LearningPath: steps,
	}, nil
}

func (e *outputModelEvaluatorV2) actorEvidence(actorIndex int) (RecommendationEvidenceV2, []int64) {
	var evidence RecommendationEvidenceV2
	knowledgeCounts := make([]int64, len(e.input.KnowledgePoints))
	distinctSources := make(map[string]struct{})
	passedSources := make(map[string]struct{})
	for _, interactionIndex := range e.interactionsByActor[actorIndex] {
		interaction := e.input.Interactions[interactionIndex]
		problemIndex := e.problemIndex[interaction.ProblemKey]
		source := e.input.Problems[problemIndex].SourceProblemKey
		distinctSources[source] = struct{}{}
		if interaction.Passed {
			passedSources[source] = struct{}{}
		}
		if interaction.Split == "train" {
			evidence.TrainInteractionCount++
			for _, weight := range e.problemKnowledge[problemIndex] {
				knowledgeCounts[weight.index]++
			}
		} else {
			evidence.ValidationInteractionCount++
		}
	}
	evidence.DistinctProblemCount = int64(len(distinctSources))
	evidence.PassedProblemCount = int64(len(passedSources))
	return evidence, knowledgeCounts
}

type knowledgeGapV2 struct {
	index int
	gap   float64
}

func (e *outputModelEvaluatorV2) buildKnowledgePath(mastery []float64) ([]int, map[int]struct{}, string) {
	target := mustNumber(e.input.TrainingConfiguration.PathPolicy.TargetMastery)
	gaps := make([]knowledgeGapV2, 0)
	for index := range mastery {
		if gap := target - mastery[index]; gap > 0 {
			gaps = append(gaps, knowledgeGapV2{index: index, gap: gap})
		}
	}
	if len(gaps) == 0 {
		return nil, nil, "mastery_target_satisfied"
	}
	slices.SortFunc(gaps, func(left, right knowledgeGapV2) int {
		if left.gap > right.gap {
			return -1
		}
		if left.gap < right.gap {
			return 1
		}
		return strings.Compare(e.input.KnowledgePoints[left.index].ID, e.input.KnowledgePoints[right.index].ID)
	})
	maximumTargets := int(e.input.TrainingConfiguration.PathPolicy.MaxKnowledgeTargets)
	if len(gaps) > maximumTargets {
		gaps = gaps[:maximumTargets]
	}
	direct := make(map[int]struct{}, len(gaps))
	closure := make(map[int]struct{})
	for _, gap := range gaps {
		direct[gap.index] = struct{}{}
		candidate := mapsCloneSet(closure)
		e.includePrerequisiteClosure(candidate, gap.index)
		if int64(len(candidate)) > e.input.TrainingConfiguration.PathPolicy.MaxSteps {
			return e.stableTopologicalPath(candidate), direct, "path_exceeds_maximum"
		}
		closure = candidate
	}
	path := e.stableTopologicalPath(closure)
	if int64(len(path)) < e.input.TrainingConfiguration.PathPolicy.MinSteps {
		return path, direct, "path_below_minimum"
	}
	return path, direct, ""
}

func mapsCloneSet(source map[int]struct{}) map[int]struct{} {
	clone := make(map[int]struct{}, len(source))
	for index := range source {
		clone[index] = struct{}{}
	}
	return clone
}

func (e *outputModelEvaluatorV2) includePrerequisiteClosure(closure map[int]struct{}, index int) {
	if _, exists := closure[index]; exists {
		return
	}
	closure[index] = struct{}{}
	for _, prerequisiteID := range e.input.KnowledgePoints[index].PrerequisiteIDs {
		e.includePrerequisiteClosure(closure, e.knowledgeIndex[prerequisiteID])
	}
}

func (e *outputModelEvaluatorV2) stableTopologicalPath(nodes map[int]struct{}) []int {
	indegree := make(map[int]int, len(nodes))
	dependents := make(map[int][]int, len(nodes))
	for index := range nodes {
		for _, prerequisiteID := range e.input.KnowledgePoints[index].PrerequisiteIDs {
			prerequisite := e.knowledgeIndex[prerequisiteID]
			if _, included := nodes[prerequisite]; included {
				indegree[index]++
				dependents[prerequisite] = append(dependents[prerequisite], index)
			}
		}
	}
	available := make([]int, 0)
	for index := range nodes {
		if indegree[index] == 0 {
			available = append(available, index)
		}
	}
	sortKnowledgeIndices(available, e.input.KnowledgePoints)
	path := make([]int, 0, len(nodes))
	for len(available) != 0 {
		index := available[0]
		available = available[1:]
		path = append(path, index)
		for _, dependent := range dependents[index] {
			indegree[dependent]--
			if indegree[dependent] == 0 {
				available = append(available, dependent)
			}
		}
		sortKnowledgeIndices(available, e.input.KnowledgePoints)
	}
	return path
}

func sortKnowledgeIndices(indices []int, knowledge []TrainingKnowledgePoint) {
	slices.SortFunc(indices, func(left, right int) int { return strings.Compare(knowledge[left].ID, knowledge[right].ID) })
}

func (e *outputModelEvaluatorV2) insufficiency(reason string, path []int, blocked []string, actorIndex int) *RecommendationInsufficiencyV2 {
	if blocked == nil {
		blocked = []string{}
	}
	return &RecommendationInsufficiencyV2{
		ReasonCode: reason, MinimumPathSteps: e.input.TrainingConfiguration.PathPolicy.MinSteps,
		CandidatePathSteps: int64(len(path)), ProblemsPerStep: e.input.TrainingConfiguration.PathPolicy.ProblemsPerStep,
		EligibleProblemCount: e.eligibleProblemCount(actorIndex, path), BlockedKnowledgePointIDs: blocked,
	}
}

type rankedProblemCandidateV2 struct {
	problemIndex int
	score        float64
	probability  float64
	gap          float64
	distance     float64
	stepWeight   float64
}

func (e *outputModelEvaluatorV2) rankPathProblems(
	actorIndex int,
	theta []float64,
	mastery []float64,
	path []int,
	direct map[int]struct{},
) ([]RecommendationLearningPathStepV2, []string, int64, error) {
	passed := e.passedSources(actorIndex)
	usedSources := make(map[string]struct{})
	eligible := e.eligibleProblemCountWithPassed(passed, path)
	steps := make([]RecommendationLearningPathStepV2, len(path))
	blocked := make([]string, 0)
	targetMastery := mustNumber(e.input.TrainingConfiguration.PathPolicy.TargetMastery)
	targetSuccess := mustNumber(e.input.TrainingConfiguration.PathPolicy.TargetSuccessProbability)
	knowledgeWeight := mustNumber(e.input.TrainingConfiguration.RankingWeights.KnowledgeGap)
	distanceWeight := mustNumber(e.input.TrainingConfiguration.RankingWeights.SuccessDistance)
	problemsPerStep := int(e.input.TrainingConfiguration.PathPolicy.ProblemsPerStep)
	for order, knowledgeIndex := range path {
		knowledge := e.input.KnowledgePoints[knowledgeIndex]
		candidates := make([]rankedProblemCandidateV2, 0)
		for problemIndex, problem := range e.input.Problems {
			if e.trainInteractionCounts[problemIndex] < e.input.TrainingConfiguration.MinProblemInteractions {
				continue
			}
			if _, excluded := passed[problem.SourceProblemKey]; excluded {
				continue
			}
			if _, used := usedSources[problem.SourceProblemKey]; used {
				continue
			}
			stepWeight := e.problemKnowledgeWeight(problemIndex, knowledgeIndex)
			if stepWeight <= 0 {
				continue
			}
			gap := e.problemKnowledgeGap(problemIndex, mastery, targetMastery)
			logit, err := e.logit(theta, problemIndex)
			if err != nil {
				return nil, nil, 0, err
			}
			probability := stableSigmoid(logit)
			distance := math.Abs(probability - targetSuccess)
			candidates = append(candidates, rankedProblemCandidateV2{
				problemIndex: problemIndex, probability: probability, gap: gap, distance: distance,
				stepWeight: stepWeight, score: knowledgeWeight*gap - distanceWeight*distance,
			})
		}
		slices.SortFunc(candidates, func(left, right rankedProblemCandidateV2) int {
			if left.score > right.score {
				return -1
			}
			if left.score < right.score {
				return 1
			}
			return strings.Compare(e.input.Problems[left.problemIndex].ProblemKey, e.input.Problems[right.problemIndex].ProblemKey)
		})
		selected := make([]RecommendationProblemV2, 0, problemsPerStep)
		for _, candidate := range candidates {
			problem := e.input.Problems[candidate.problemIndex]
			if _, used := usedSources[problem.SourceProblemKey]; used {
				continue
			}
			usedSources[problem.SourceProblemKey] = struct{}{}
			selected = append(selected, RecommendationProblemV2{
				ProblemKey: problem.ProblemKey, SourceProblemKey: problem.SourceProblemKey,
				Platform: problem.Platform, ProblemID: problem.ProblemID, Title: problem.Title,
				SourceProblemSets:           slices.Clone(problem.SourceProblemSets),
				PredictedSuccessProbability: resultNumber(candidate.probability), RecommendationScore: resultNumber(candidate.score),
				RankingEvidence: RecommendationRankingEvidenceV2{
					KnowledgeGap: resultNumber(candidate.gap), SuccessDistance: resultNumber(candidate.distance),
					StepKnowledgeWeight: resultNumber(candidate.stepWeight),
				},
			})
			if len(selected) == problemsPerStep {
				break
			}
		}
		if len(selected) != problemsPerStep {
			blocked = append(blocked, knowledge.ID)
		}
		_, isDirect := direct[knowledgeIndex]
		reasonCode := "prerequisite"
		if isDirect {
			reasonCode = "knowledge_gap"
		}
		steps[order] = RecommendationLearningPathStepV2{
			Order: int64(order + 1), KnowledgePointID: knowledge.ID, Label: knowledge.Label,
			Description: knowledge.Description, PrerequisiteIDs: slices.Clone(knowledge.PrerequisiteIDs),
			Mastery: resultNumber(mastery[knowledgeIndex]), TargetMastery: resultNumber(targetMastery),
			ReasonCode: reasonCode, RecommendedProblems: selected,
		}
	}
	if len(blocked) != 0 {
		return nil, blocked, eligible, nil
	}
	return steps, nil, eligible, nil
}

func (e *outputModelEvaluatorV2) problemKnowledgeGap(problemIndex int, mastery []float64, targetMastery float64) float64 {
	terms := make([]float64, len(e.problemKnowledge[problemIndex]))
	for index, weight := range e.problemKnowledge[problemIndex] {
		terms[index] = weight.weight * math.Max(targetMastery-mastery[weight.index], 0)
	}
	return accuratelySum(terms)
}

func (e *outputModelEvaluatorV2) problemKnowledgeWeight(problemIndex, knowledgeIndex int) float64 {
	for _, weight := range e.problemKnowledge[problemIndex] {
		if weight.index == knowledgeIndex {
			return weight.weight
		}
	}
	return 0
}

func (e *outputModelEvaluatorV2) passedSources(actorIndex int) map[string]struct{} {
	passed := make(map[string]struct{})
	for _, interactionIndex := range e.interactionsByActor[actorIndex] {
		interaction := e.input.Interactions[interactionIndex]
		if interaction.Passed {
			passed[e.input.Problems[e.problemIndex[interaction.ProblemKey]].SourceProblemKey] = struct{}{}
		}
	}
	return passed
}

func (e *outputModelEvaluatorV2) eligibleProblemCount(actorIndex int, path []int) int64 {
	return e.eligibleProblemCountWithPassed(e.passedSources(actorIndex), path)
}

func (e *outputModelEvaluatorV2) eligibleProblemCountWithPassed(passed map[string]struct{}, path []int) int64 {
	eligibleSources := make(map[string]struct{})
	pathSet := make(map[int]struct{}, len(path))
	for _, index := range path {
		pathSet[index] = struct{}{}
	}
	for problemIndex, problem := range e.input.Problems {
		if e.trainInteractionCounts[problemIndex] < e.input.TrainingConfiguration.MinProblemInteractions {
			continue
		}
		if _, excluded := passed[problem.SourceProblemKey]; excluded {
			continue
		}
		for _, weight := range e.problemKnowledge[problemIndex] {
			if _, included := pathSet[weight.index]; included {
				eligibleSources[problem.SourceProblemKey] = struct{}{}
				break
			}
		}
	}
	return int64(len(eligibleSources))
}

func mustNumber(value json.Number) float64 {
	parsed, _ := strconv.ParseFloat(string(value), 64)
	return parsed
}

func resultNumber(value float64) json.Number {
	if value == 0 {
		return json.Number("0")
	}
	return json.Number(strconv.FormatFloat(value, 'g', -1, 64))
}
