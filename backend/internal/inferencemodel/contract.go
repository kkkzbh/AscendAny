// Package inferencemodel owns the closed, immutable recommendation inference
// model contract and its deterministic Go evaluator.
package inferencemodel

import "errors"

const (
	// Schema is the only artifact schema accepted by Parse.
	Schema = "ascendany.recommendation.inference-model.v1"
	// Algorithm identifies the mathematical model implemented by Evaluate.
	Algorithm = "knowledge_mirt_feature_v1"
	// InferenceContract identifies the runtime input and output semantics.
	InferenceContract = "ascendany.recommendation.inference.v1"

	// MaximumArtifactBytes is the hard artifact boundary.
	MaximumArtifactBytes = 16 << 20
	// MaximumParameterAbsoluteValue bounds every learned parameter, including
	// normalization values and scales.
	MaximumParameterAbsoluteValue = 100
	// MaximumKnowledgePoints is shared with the online knowledge-catalog
	// contract. The evaluator rejects model manifests outside this boundary.
	MaximumKnowledgePoints = 1024
	// GoldenTolerance is the absolute tolerance used to verify artifact golden
	// vectors.
	GoldenTolerance = 1e-12

	// PurposeProduction marks an artifact authorized for a production release.
	PurposeProduction Purpose = "production"
	// PurposeAcceptanceTest marks a deterministic artifact that may only run in
	// an explicitly acceptance-scoped release and runtime.
	PurposeAcceptanceTest Purpose = "acceptance_test"
)

// Purpose is the deployment authorization carried by an immutable model.
type Purpose string

var (
	// ErrInvalidArtifact reports a malformed or internally inconsistent model.
	ErrInvalidArtifact = errors.New("invalid inference model artifact")
	// ErrInvalidInput reports inference input that violates the model manifest.
	ErrInvalidInput = errors.New("invalid inference input")
)

// Manifest is immutable model identity and provenance. Slice fields returned by
// Model.Manifest are independent copies.
type Manifest struct {
	ModelID                  string
	Purpose                  Purpose
	TrainedAt                string
	Algorithm                string
	InferenceContract        string
	TrainingProvenanceSHA256 string
	FeatureSchemaSHA256      string
	KnowledgeCatalogSHA256   string
	ParameterSHA256          string
	GoldenVectorsSHA256      string
	ActorFeatureIDs          []string
	ProblemFeatureIDs        []string
	KnowledgePointIDs        []string
}

// ParsePurpose validates a deployment-purpose configuration value.
func ParsePurpose(value string) (Purpose, error) {
	purpose := Purpose(value)
	if purpose != PurposeProduction && purpose != PurposeAcceptanceTest {
		return "", errors.New("model purpose must be production or acceptance_test")
	}
	return purpose, nil
}

// FeatureValue is one ordered actor or problem feature.
type FeatureValue struct {
	FeatureID string
	Value     float64
}

// FeatureDomain is the closed numeric output domain for one ordered runtime
// feature. ValidateNumericEnvelope uses these ranges to prove that every
// normalization, linear term, and final logit remains finite for the complete
// runtime domain.
type FeatureDomain struct {
	FeatureID string
	Minimum   float64
	Maximum   float64
}

// KnowledgeWeight is one ordered knowledge contribution to ability.
type KnowledgeWeight struct {
	KnowledgePointID string
	Weight           float64
}

// Input contains the exact ordered identities declared by the model manifest.
type Input struct {
	FeatureSchemaSHA256    string
	KnowledgeCatalogSHA256 string
	ActorFeatures          []FeatureValue
	ProblemFeatures        []FeatureValue
	KnowledgeWeights       []KnowledgeWeight
}

// KnowledgeMastery is the inferred mastery probability for one manifest
// knowledge point.
type KnowledgeMastery struct {
	KnowledgePointID string
	Probability      float64
}

// Result is the deterministic output of Evaluate.
type Result struct {
	Probability      float64
	KnowledgeMastery []KnowledgeMastery
}

type normalization struct {
	means  []float64
	scales []float64
}

type knowledgeParameters struct {
	actorFeatureWeights []float64
	bias                float64
}

// Model is a parsed and golden-verified immutable inference artifact.
type Model struct {
	manifest              Manifest
	actorNormalization    normalization
	problemNormalization  normalization
	knowledge             []knowledgeParameters
	problemFeatureWeights []float64
	difficultyBias        float64
	discrimination        float64
	sha256                string
}

// Manifest returns an independent copy of the model manifest.
func (m *Model) Manifest() Manifest {
	if m == nil {
		return Manifest{}
	}
	manifest := m.manifest
	manifest.ActorFeatureIDs = append([]string(nil), manifest.ActorFeatureIDs...)
	manifest.ProblemFeatureIDs = append([]string(nil), manifest.ProblemFeatureIDs...)
	manifest.KnowledgePointIDs = append([]string(nil), manifest.KnowledgePointIDs...)
	return manifest
}

// SHA256 returns the lowercase SHA-256 of the canonical artifact bytes from
// which the model was parsed.
func (m *Model) SHA256() string {
	if m == nil {
		return ""
	}
	return m.sha256
}
