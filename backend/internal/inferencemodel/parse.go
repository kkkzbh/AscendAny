package inferencemodel

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"regexp"
	"strconv"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/canonicaljson"
)

var (
	lowercaseSHA256Pattern   = regexp.MustCompile(`^[0-9a-f]{64}$`)
	uuidV4Pattern            = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	identityPattern          = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)
	knowledgeIdentityPattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,127}$`)
	canonicalTrainedAt       = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(?:\.[0-9]{0,5}[1-9])?Z$`)
)

type artifactWire struct {
	Schema        *string         `json:"schema"`
	Manifest      json.RawMessage `json:"manifest"`
	Parameters    json.RawMessage `json:"parameters"`
	GoldenVectors json.RawMessage `json:"goldenVectors"`
}

type manifestWire struct {
	ModelID                  *string   `json:"modelId"`
	Purpose                  *string   `json:"purpose"`
	TrainedAt                *string   `json:"trainedAt"`
	Algorithm                *string   `json:"algorithm"`
	InferenceContract        *string   `json:"inferenceContract"`
	TrainingProvenanceSHA256 *string   `json:"trainingProvenanceSha256"`
	FeatureSchemaSHA256      *string   `json:"featureSchemaSha256"`
	KnowledgeCatalogSHA256   *string   `json:"knowledgeCatalogSha256"`
	ParameterSHA256          *string   `json:"parameterSha256"`
	GoldenVectorsSHA256      *string   `json:"goldenVectorsSha256"`
	ActorFeatureIDs          *[]string `json:"actorFeatureIds"`
	ProblemFeatureIDs        *[]string `json:"problemFeatureIds"`
	KnowledgePointIDs        *[]string `json:"knowledgePointIds"`
}

type parametersWire struct {
	ActorNormalization    *normalizationWire         `json:"actorNormalization"`
	ProblemNormalization  *normalizationWire         `json:"problemNormalization"`
	KnowledgeParameters   *[]knowledgeParametersWire `json:"knowledgeParameters"`
	ProblemFeatureWeights *[]json.Number             `json:"problemFeatureWeights"`
	DifficultyBias        *json.Number               `json:"difficultyBias"`
	Discrimination        *json.Number               `json:"discrimination"`
}

type normalizationWire struct {
	Means  *[]json.Number `json:"means"`
	Scales *[]json.Number `json:"scales"`
}

type knowledgeParametersWire struct {
	KnowledgePointID    *string        `json:"knowledgePointId"`
	ActorFeatureWeights *[]json.Number `json:"actorFeatureWeights"`
	Bias                *json.Number   `json:"bias"`
}

type goldenVectorWire struct {
	ID       *string             `json:"id"`
	Input    *goldenInputWire    `json:"input"`
	Expected *goldenExpectedWire `json:"expected"`
}

type goldenInputWire struct {
	FeatureSchemaSHA256    *string                `json:"featureSchemaSha256"`
	KnowledgeCatalogSHA256 *string                `json:"knowledgeCatalogSha256"`
	ActorFeatures          *[]featureValueWire    `json:"actorFeatures"`
	ProblemFeatures        *[]featureValueWire    `json:"problemFeatures"`
	KnowledgeWeights       *[]knowledgeWeightWire `json:"knowledgeWeights"`
}

type featureValueWire struct {
	FeatureID *string      `json:"featureId"`
	Value     *json.Number `json:"value"`
}

type knowledgeWeightWire struct {
	KnowledgePointID *string      `json:"knowledgePointId"`
	Weight           *json.Number `json:"weight"`
}

type goldenExpectedWire struct {
	Probability      *json.Number         `json:"probability"`
	KnowledgeMastery *[]goldenMasteryWire `json:"knowledgeMastery"`
}

type goldenMasteryWire struct {
	KnowledgePointID *string      `json:"knowledgePointId"`
	Probability      *json.Number `json:"probability"`
}

// Parse validates canonical bytes, the closed schema, all internal digests and
// shapes, and every mandatory golden vector before returning a model.
func Parse(raw []byte) (*Model, error) {
	canonical, artifactSHA256, err := canonicaljson.Object(json.RawMessage(raw), MaximumArtifactBytes)
	if err != nil {
		return nil, invalidArtifact("canonical JSON", err)
	}
	if !bytes.Equal(raw, canonical) {
		return nil, invalidArtifact("canonical JSON", errors.New("artifact bytes must already be canonical"))
	}

	var artifact artifactWire
	if err := decodeClosed(canonical, &artifact); err != nil {
		return nil, invalidArtifact("artifact", err)
	}
	if artifact.Schema == nil || *artifact.Schema != Schema {
		return nil, invalidArtifact("schema", fmt.Errorf("must equal %q", Schema))
	}
	if len(artifact.Manifest) == 0 || len(artifact.Parameters) == 0 || len(artifact.GoldenVectors) == 0 {
		return nil, invalidArtifact("artifact", errors.New("manifest, parameters, and goldenVectors are required"))
	}

	var encodedManifest manifestWire
	if err := decodeClosed(artifact.Manifest, &encodedManifest); err != nil {
		return nil, invalidArtifact("manifest", err)
	}
	manifest, err := parseManifest(encodedManifest)
	if err != nil {
		return nil, invalidArtifact("manifest", err)
	}
	if actual := digestBytes(artifact.Parameters); actual != manifest.ParameterSHA256 {
		return nil, invalidArtifact("parameters", fmt.Errorf("SHA-256 %q does not match manifest %q", actual, manifest.ParameterSHA256))
	}
	if actual := digestBytes(artifact.GoldenVectors); actual != manifest.GoldenVectorsSHA256 {
		return nil, invalidArtifact("goldenVectors", fmt.Errorf("SHA-256 %q does not match manifest %q", actual, manifest.GoldenVectorsSHA256))
	}

	model, err := parseParameters(artifact.Parameters, manifest)
	if err != nil {
		return nil, invalidArtifact("parameters", err)
	}
	model.sha256 = artifactSHA256
	if err := verifyGoldenVectors(artifact.GoldenVectors, model); err != nil {
		return nil, invalidArtifact("goldenVectors", err)
	}
	return model, nil
}

// SHA256 returns the digest of a complete valid canonical artifact. It performs
// the same validation, including golden-vector execution, as Parse.
func SHA256(raw []byte) (string, error) {
	model, err := Parse(raw)
	if err != nil {
		return "", err
	}
	return model.sha256, nil
}

func parseManifest(wire manifestWire) (Manifest, error) {
	modelID, err := requiredString("modelId", wire.ModelID)
	if err != nil || !uuidV4Pattern.MatchString(modelID) {
		return Manifest{}, errors.New("modelId must be a canonical lowercase UUIDv4")
	}
	purposeValue, err := requiredString("purpose", wire.Purpose)
	if err != nil {
		return Manifest{}, err
	}
	purpose, err := ParsePurpose(purposeValue)
	if err != nil {
		return Manifest{}, err
	}
	trainedAt, err := requiredString("trainedAt", wire.TrainedAt)
	if err != nil {
		return Manifest{}, err
	}
	parsedTime, err := time.Parse(time.RFC3339Nano, trainedAt)
	if err != nil || parsedTime.Year() < 1 || !canonicalTrainedAt.MatchString(trainedAt) || parsedTime.Nanosecond()%1_000 != 0 ||
		parsedTime.Format(time.RFC3339Nano) != trainedAt {
		return Manifest{}, errors.New("trainedAt must be a canonical UTC RFC3339 timestamp with at most microsecond precision")
	}
	algorithm, err := requiredString("algorithm", wire.Algorithm)
	if err != nil || algorithm != Algorithm {
		return Manifest{}, fmt.Errorf("algorithm must equal %q", Algorithm)
	}
	inferenceContract, err := requiredString("inferenceContract", wire.InferenceContract)
	if err != nil || inferenceContract != InferenceContract {
		return Manifest{}, fmt.Errorf("inferenceContract must equal %q", InferenceContract)
	}

	trainingProvenanceSHA256, err := requiredDigest("trainingProvenanceSha256", wire.TrainingProvenanceSHA256)
	if err != nil {
		return Manifest{}, err
	}
	featureSchemaSHA256, err := requiredDigest("featureSchemaSha256", wire.FeatureSchemaSHA256)
	if err != nil {
		return Manifest{}, err
	}
	knowledgeCatalogSHA256, err := requiredDigest("knowledgeCatalogSha256", wire.KnowledgeCatalogSHA256)
	if err != nil {
		return Manifest{}, err
	}
	parameterSHA256, err := requiredDigest("parameterSha256", wire.ParameterSHA256)
	if err != nil {
		return Manifest{}, err
	}
	goldenVectorsSHA256, err := requiredDigest("goldenVectorsSha256", wire.GoldenVectorsSHA256)
	if err != nil {
		return Manifest{}, err
	}

	actorFeatureIDs, err := parseIdentityList("actorFeatureIds", wire.ActorFeatureIDs)
	if err != nil {
		return Manifest{}, err
	}
	problemFeatureIDs, err := parseIdentityList("problemFeatureIds", wire.ProblemFeatureIDs)
	if err != nil {
		return Manifest{}, err
	}
	knowledgePointIDs, err := parseKnowledgeIdentityList(wire.KnowledgePointIDs)
	if err != nil {
		return Manifest{}, err
	}

	return Manifest{
		ModelID:                  modelID,
		Purpose:                  purpose,
		TrainedAt:                trainedAt,
		Algorithm:                algorithm,
		InferenceContract:        inferenceContract,
		TrainingProvenanceSHA256: trainingProvenanceSHA256,
		FeatureSchemaSHA256:      featureSchemaSHA256,
		KnowledgeCatalogSHA256:   knowledgeCatalogSHA256,
		ParameterSHA256:          parameterSHA256,
		GoldenVectorsSHA256:      goldenVectorsSHA256,
		ActorFeatureIDs:          actorFeatureIDs,
		ProblemFeatureIDs:        problemFeatureIDs,
		KnowledgePointIDs:        knowledgePointIDs,
	}, nil
}

func parseParameters(raw json.RawMessage, manifest Manifest) (*Model, error) {
	var wire parametersWire
	if err := decodeClosed(raw, &wire); err != nil {
		return nil, err
	}
	actorNormalization, err := parseNormalization("actorNormalization", wire.ActorNormalization, len(manifest.ActorFeatureIDs))
	if err != nil {
		return nil, err
	}
	problemNormalization, err := parseNormalization("problemNormalization", wire.ProblemNormalization, len(manifest.ProblemFeatureIDs))
	if err != nil {
		return nil, err
	}
	if wire.KnowledgeParameters == nil || len(*wire.KnowledgeParameters) != len(manifest.KnowledgePointIDs) {
		return nil, fmt.Errorf("knowledgeParameters count must equal %d", len(manifest.KnowledgePointIDs))
	}
	knowledge := make([]knowledgeParameters, len(*wire.KnowledgeParameters))
	for index, encoded := range *wire.KnowledgeParameters {
		knowledgeID, err := requiredString("knowledgePointId", encoded.KnowledgePointID)
		if err != nil || knowledgeID != manifest.KnowledgePointIDs[index] {
			return nil, fmt.Errorf("knowledgeParameters item %d identity must equal %q", index, manifest.KnowledgePointIDs[index])
		}
		weights, err := parseParameterVector("actorFeatureWeights", encoded.ActorFeatureWeights, len(manifest.ActorFeatureIDs), false)
		if err != nil {
			return nil, fmt.Errorf("knowledgeParameters %q: %w", knowledgeID, err)
		}
		bias, err := parseParameter("bias", encoded.Bias, false)
		if err != nil {
			return nil, fmt.Errorf("knowledgeParameters %q: %w", knowledgeID, err)
		}
		knowledge[index] = knowledgeParameters{actorFeatureWeights: weights, bias: bias}
	}
	problemWeights, err := parseParameterVector("problemFeatureWeights", wire.ProblemFeatureWeights, len(manifest.ProblemFeatureIDs), false)
	if err != nil {
		return nil, err
	}
	difficultyBias, err := parseParameter("difficultyBias", wire.DifficultyBias, false)
	if err != nil {
		return nil, err
	}
	discrimination, err := parseParameter("discrimination", wire.Discrimination, true)
	if err != nil {
		return nil, err
	}

	return &Model{
		manifest:              manifest,
		actorNormalization:    actorNormalization,
		problemNormalization:  problemNormalization,
		knowledge:             knowledge,
		problemFeatureWeights: problemWeights,
		difficultyBias:        difficultyBias,
		discrimination:        discrimination,
	}, nil
}

func parseNormalization(label string, wire *normalizationWire, count int) (normalization, error) {
	if wire == nil {
		return normalization{}, fmt.Errorf("%s is required", label)
	}
	means, err := parseParameterVector(label+".means", wire.Means, count, false)
	if err != nil {
		return normalization{}, err
	}
	scales, err := parseParameterVector(label+".scales", wire.Scales, count, true)
	if err != nil {
		return normalization{}, err
	}
	return normalization{means: means, scales: scales}, nil
}

func parseParameterVector(label string, wire *[]json.Number, count int, strictlyPositive bool) ([]float64, error) {
	if wire == nil || len(*wire) != count {
		return nil, fmt.Errorf("%s count must equal %d", label, count)
	}
	values := make([]float64, count)
	for index := range *wire {
		value := (*wire)[index]
		parsed, err := parseParameter(label, &value, strictlyPositive)
		if err != nil {
			return nil, fmt.Errorf("%s item %d: %w", label, index, err)
		}
		values[index] = parsed
	}
	return values, nil
}

func parseParameter(label string, wire *json.Number, strictlyPositive bool) (float64, error) {
	value, err := parseFiniteNumber(label, wire)
	if err != nil {
		return 0, err
	}
	if math.Abs(value) > MaximumParameterAbsoluteValue {
		return 0, fmt.Errorf("%s absolute value must not exceed %d", label, MaximumParameterAbsoluteValue)
	}
	if strictlyPositive && value <= 0 {
		return 0, fmt.Errorf("%s must be positive", label)
	}
	return value, nil
}

func verifyGoldenVectors(raw json.RawMessage, model *Model) error {
	var vectors []goldenVectorWire
	if err := decodeClosed(raw, &vectors); err != nil {
		return err
	}
	if len(vectors) == 0 {
		return errors.New("at least one golden vector is required")
	}
	seenIDs := make(map[string]struct{}, len(vectors))
	for index, wire := range vectors {
		id, err := requiredString("id", wire.ID)
		if err != nil || !identityPattern.MatchString(id) {
			return fmt.Errorf("golden vector %d id is invalid", index)
		}
		if _, exists := seenIDs[id]; exists {
			return fmt.Errorf("golden vector id %q is duplicated", id)
		}
		seenIDs[id] = struct{}{}
		if wire.Input == nil || wire.Expected == nil {
			return fmt.Errorf("golden vector %q input and expected are required", id)
		}
		input, err := parseGoldenInput(*wire.Input, model.manifest)
		if err != nil {
			return fmt.Errorf("golden vector %q input: %w", id, err)
		}
		expected, err := parseGoldenExpected(*wire.Expected, model.manifest)
		if err != nil {
			return fmt.Errorf("golden vector %q expected: %w", id, err)
		}
		actual, err := model.Evaluate(input)
		if err != nil {
			return fmt.Errorf("golden vector %q evaluation: %w", id, err)
		}
		if math.Abs(actual.Probability-expected.Probability) > GoldenTolerance {
			return fmt.Errorf("golden vector %q probability differs by more than %.0e", id, GoldenTolerance)
		}
		for masteryIndex := range actual.KnowledgeMastery {
			if math.Abs(actual.KnowledgeMastery[masteryIndex].Probability-expected.KnowledgeMastery[masteryIndex].Probability) > GoldenTolerance {
				return fmt.Errorf("golden vector %q mastery %q differs by more than %.0e", id, actual.KnowledgeMastery[masteryIndex].KnowledgePointID, GoldenTolerance)
			}
		}
	}
	return nil
}

func parseGoldenInput(wire goldenInputWire, manifest Manifest) (Input, error) {
	featureSchemaSHA256, err := requiredDigest("featureSchemaSha256", wire.FeatureSchemaSHA256)
	if err != nil || featureSchemaSHA256 != manifest.FeatureSchemaSHA256 {
		return Input{}, errors.New("featureSchemaSha256 must equal the manifest digest")
	}
	knowledgeCatalogSHA256, err := requiredDigest("knowledgeCatalogSha256", wire.KnowledgeCatalogSHA256)
	if err != nil || knowledgeCatalogSHA256 != manifest.KnowledgeCatalogSHA256 {
		return Input{}, errors.New("knowledgeCatalogSha256 must equal the manifest digest")
	}
	actorFeatures, err := parseGoldenFeatures("actorFeatures", wire.ActorFeatures, manifest.ActorFeatureIDs)
	if err != nil {
		return Input{}, err
	}
	problemFeatures, err := parseGoldenFeatures("problemFeatures", wire.ProblemFeatures, manifest.ProblemFeatureIDs)
	if err != nil {
		return Input{}, err
	}
	if wire.KnowledgeWeights == nil || len(*wire.KnowledgeWeights) != len(manifest.KnowledgePointIDs) {
		return Input{}, fmt.Errorf("knowledgeWeights count must equal %d", len(manifest.KnowledgePointIDs))
	}
	weights := make([]KnowledgeWeight, len(*wire.KnowledgeWeights))
	rationalWeightSum := new(big.Rat)
	for index, encoded := range *wire.KnowledgeWeights {
		knowledgeID, err := requiredString("knowledgePointId", encoded.KnowledgePointID)
		if err != nil || knowledgeID != manifest.KnowledgePointIDs[index] {
			return Input{}, fmt.Errorf("knowledgeWeights item %d identity must equal %q", index, manifest.KnowledgePointIDs[index])
		}
		weight, err := parseProbability("weight", encoded.Weight)
		if err != nil {
			return Input{}, fmt.Errorf("knowledgeWeights %q: %w", knowledgeID, err)
		}
		rationalWeight, ok := new(big.Rat).SetString(string(*encoded.Weight))
		if !ok {
			return Input{}, fmt.Errorf("knowledgeWeights %q is not a finite decimal", knowledgeID)
		}
		rationalWeightSum.Add(rationalWeightSum, rationalWeight)
		weights[index] = KnowledgeWeight{KnowledgePointID: knowledgeID, Weight: weight}
	}
	if rationalWeightSum.Cmp(big.NewRat(1, 1)) != 0 {
		return Input{}, errors.New("knowledgeWeights must sum exactly to 1")
	}
	input := Input{
		FeatureSchemaSHA256:    featureSchemaSHA256,
		KnowledgeCatalogSHA256: knowledgeCatalogSHA256,
		ActorFeatures:          actorFeatures,
		ProblemFeatures:        problemFeatures,
		KnowledgeWeights:       weights,
	}
	if err := validateKnowledgeWeights(input.KnowledgeWeights); err != nil {
		return Input{}, err
	}
	return input, nil
}

func parseGoldenFeatures(label string, wire *[]featureValueWire, identities []string) ([]FeatureValue, error) {
	if wire == nil || len(*wire) != len(identities) {
		return nil, fmt.Errorf("%s count must equal %d", label, len(identities))
	}
	features := make([]FeatureValue, len(*wire))
	for index, encoded := range *wire {
		featureID, err := requiredString("featureId", encoded.FeatureID)
		if err != nil || featureID != identities[index] {
			return nil, fmt.Errorf("%s item %d identity must equal %q", label, index, identities[index])
		}
		value, err := parseFiniteNumber("value", encoded.Value)
		if err != nil {
			return nil, fmt.Errorf("%s %q: %w", label, featureID, err)
		}
		features[index] = FeatureValue{FeatureID: featureID, Value: value}
	}
	return features, nil
}

func parseGoldenExpected(wire goldenExpectedWire, manifest Manifest) (Result, error) {
	probability, err := parseProbability("probability", wire.Probability)
	if err != nil {
		return Result{}, err
	}
	if wire.KnowledgeMastery == nil || len(*wire.KnowledgeMastery) != len(manifest.KnowledgePointIDs) {
		return Result{}, fmt.Errorf("knowledgeMastery count must equal %d", len(manifest.KnowledgePointIDs))
	}
	mastery := make([]KnowledgeMastery, len(*wire.KnowledgeMastery))
	for index, encoded := range *wire.KnowledgeMastery {
		knowledgeID, err := requiredString("knowledgePointId", encoded.KnowledgePointID)
		if err != nil || knowledgeID != manifest.KnowledgePointIDs[index] {
			return Result{}, fmt.Errorf("knowledgeMastery item %d identity must equal %q", index, manifest.KnowledgePointIDs[index])
		}
		value, err := parseProbability("probability", encoded.Probability)
		if err != nil {
			return Result{}, fmt.Errorf("knowledgeMastery %q: %w", knowledgeID, err)
		}
		mastery[index] = KnowledgeMastery{KnowledgePointID: knowledgeID, Probability: value}
	}
	return Result{Probability: probability, KnowledgeMastery: mastery}, nil
}

func validateKnowledgeWeights(weights []KnowledgeWeight) error {
	values := make([]float64, len(weights))
	for index := range weights {
		values[index] = weights[index].Weight
	}
	sum, err := finiteSum(values)
	if err != nil || math.Abs(sum-1) > knowledgeWeightSumTolerance {
		return fmt.Errorf("knowledgeWeights must sum to 1 within %.0e", knowledgeWeightSumTolerance)
	}
	return nil
}

func parseProbability(label string, wire *json.Number) (float64, error) {
	value, err := parseFiniteNumber(label, wire)
	if err != nil {
		return 0, err
	}
	if value < 0 || value > 1 {
		return 0, fmt.Errorf("%s must be within [0,1]", label)
	}
	return value, nil
}

func parseFiniteNumber(label string, wire *json.Number) (float64, error) {
	if wire == nil || string(*wire) == "" {
		return 0, fmt.Errorf("%s is required", label)
	}
	value, err := strconv.ParseFloat(string(*wire), 64)
	if err != nil || !finite(value) {
		return 0, fmt.Errorf("%s must be representable as a finite float64 without range loss", label)
	}
	return value, nil
}

func parseIdentityList(label string, wire *[]string) ([]string, error) {
	if wire == nil || len(*wire) == 0 {
		return nil, fmt.Errorf("%s must contain at least one identity", label)
	}
	identities := append([]string(nil), (*wire)...)
	seen := make(map[string]struct{}, len(identities))
	for index, identity := range identities {
		if !identityPattern.MatchString(identity) {
			return nil, fmt.Errorf("%s item %d is invalid", label, index)
		}
		if _, exists := seen[identity]; exists {
			return nil, fmt.Errorf("%s identity %q is duplicated", label, identity)
		}
		seen[identity] = struct{}{}
	}
	return identities, nil
}

func parseKnowledgeIdentityList(wire *[]string) ([]string, error) {
	if wire == nil {
		return nil, fmt.Errorf("knowledgePointIds must contain between 1 and %d identities", MaximumKnowledgePoints)
	}
	identities := append([]string(nil), (*wire)...)
	if err := ValidateKnowledgePointIDs(identities); err != nil {
		return nil, err
	}
	return identities, nil
}

// ValidateKnowledgePointIDs validates the ordered identity domain shared by
// the inference artifact and the production knowledge catalog.
func ValidateKnowledgePointIDs(identities []string) error {
	if len(identities) < 1 || len(identities) > MaximumKnowledgePoints {
		return fmt.Errorf("knowledgePointIds must contain between 1 and %d identities", MaximumKnowledgePoints)
	}
	for index, identity := range identities {
		if !knowledgeIdentityPattern.MatchString(identity) {
			return fmt.Errorf("knowledgePointIds item %d is invalid", index)
		}
		if index > 0 && identity <= identities[index-1] {
			return errors.New("knowledgePointIds must be strictly sorted")
		}
	}
	return nil
}

func requiredString(label string, value *string) (string, error) {
	if value == nil || *value == "" {
		return "", fmt.Errorf("%s is required", label)
	}
	return *value, nil
}

func requiredDigest(label string, value *string) (string, error) {
	parsed, err := requiredString(label, value)
	if err != nil || !lowercaseSHA256Pattern.MatchString(parsed) {
		return "", fmt.Errorf("%s must be a lowercase SHA-256", label)
	}
	return parsed, nil
}

func decodeClosed(raw []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("JSON has a trailing value")
		}
		return err
	}
	return nil
}

func digestBytes(raw []byte) string {
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func invalidArtifact(stage string, err error) error {
	return fmt.Errorf("%w: %s: %v", ErrInvalidArtifact, stage, err)
}
