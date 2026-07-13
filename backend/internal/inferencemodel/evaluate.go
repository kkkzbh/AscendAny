package inferencemodel

import (
	"errors"
	"fmt"
	"math"
)

const knowledgeWeightSumTolerance = 1e-12

// Evaluate applies model to input after validating every identity, count,
// order, numeric value, and knowledge weight.
func Evaluate(model *Model, input Input) (Result, error) {
	if model == nil {
		return Result{}, fmt.Errorf("%w: model is nil", ErrInvalidArtifact)
	}
	return model.Evaluate(input)
}

// Evaluate applies the parsed model to input.
func (m *Model) Evaluate(input Input) (Result, error) {
	if m == nil {
		return Result{}, fmt.Errorf("%w: model is nil", ErrInvalidArtifact)
	}
	if err := m.validateInput(input); err != nil {
		return Result{}, err
	}

	actor, err := normalizeFeatures(input.ActorFeatures, m.actorNormalization)
	if err != nil {
		return Result{}, fmt.Errorf("%w: normalize actor features: %v", ErrInvalidInput, err)
	}
	problem, err := normalizeFeatures(input.ProblemFeatures, m.problemNormalization)
	if err != nil {
		return Result{}, fmt.Errorf("%w: normalize problem features: %v", ErrInvalidInput, err)
	}

	theta := make([]float64, len(m.knowledge))
	mastery := make([]KnowledgeMastery, len(m.knowledge))
	for index, parameters := range m.knowledge {
		linear, err := finiteDot(parameters.actorFeatureWeights, actor)
		if err != nil {
			return Result{}, fmt.Errorf("%w: knowledge %q: %v", ErrInvalidInput, m.manifest.KnowledgePointIDs[index], err)
		}
		theta[index] = parameters.bias + linear
		if !finite(theta[index]) {
			return Result{}, fmt.Errorf("%w: knowledge %q theta is not finite", ErrInvalidInput, m.manifest.KnowledgePointIDs[index])
		}
		mastery[index] = KnowledgeMastery{
			KnowledgePointID: m.manifest.KnowledgePointIDs[index],
			Probability:      sigmoid(theta[index]),
		}
	}

	abilityTerms := make([]float64, len(theta))
	for index := range theta {
		abilityTerms[index] = input.KnowledgeWeights[index].Weight * theta[index]
		if !finite(abilityTerms[index]) {
			return Result{}, fmt.Errorf("%w: weighted knowledge ability is not finite", ErrInvalidInput)
		}
	}
	ability, err := finiteSum(abilityTerms)
	if err != nil {
		return Result{}, fmt.Errorf("%w: ability: %v", ErrInvalidInput, err)
	}
	problemLinear, err := finiteDot(m.problemFeatureWeights, problem)
	if err != nil {
		return Result{}, fmt.Errorf("%w: difficulty: %v", ErrInvalidInput, err)
	}
	difficulty := m.difficultyBias + problemLinear
	if !finite(difficulty) {
		return Result{}, fmt.Errorf("%w: difficulty is not finite", ErrInvalidInput)
	}
	logit := m.discrimination * (ability - difficulty)
	if !finite(logit) {
		return Result{}, fmt.Errorf("%w: probability logit is not finite", ErrInvalidInput)
	}

	return Result{
		Probability:      sigmoid(logit),
		KnowledgeMastery: mastery,
	}, nil
}

func (m *Model) validateInput(input Input) error {
	if input.FeatureSchemaSHA256 != m.manifest.FeatureSchemaSHA256 {
		return fmt.Errorf("%w: feature schema SHA-256 does not match model manifest", ErrInvalidInput)
	}
	if input.KnowledgeCatalogSHA256 != m.manifest.KnowledgeCatalogSHA256 {
		return fmt.Errorf("%w: knowledge catalog SHA-256 does not match model manifest", ErrInvalidInput)
	}
	if err := validateOrderedFeatures("actor features", input.ActorFeatures, m.manifest.ActorFeatureIDs); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if err := validateOrderedFeatures("problem features", input.ProblemFeatures, m.manifest.ProblemFeatureIDs); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if len(input.KnowledgeWeights) != len(m.manifest.KnowledgePointIDs) {
		return fmt.Errorf("%w: knowledge weight count %d does not match manifest count %d", ErrInvalidInput, len(input.KnowledgeWeights), len(m.manifest.KnowledgePointIDs))
	}
	weights := make([]float64, len(input.KnowledgeWeights))
	for index, weight := range input.KnowledgeWeights {
		expectedID := m.manifest.KnowledgePointIDs[index]
		if weight.KnowledgePointID != expectedID {
			return fmt.Errorf("%w: knowledge weight %d identity %q does not match %q", ErrInvalidInput, index, weight.KnowledgePointID, expectedID)
		}
		if !finite(weight.Weight) || weight.Weight < 0 || weight.Weight > 1 {
			return fmt.Errorf("%w: knowledge weight %q must be finite and within [0,1]", ErrInvalidInput, expectedID)
		}
		weights[index] = weight.Weight
	}
	sum, err := finiteSum(weights)
	if err != nil || math.Abs(sum-1) > knowledgeWeightSumTolerance {
		return fmt.Errorf("%w: knowledge weights must sum to 1 within %.0e", ErrInvalidInput, knowledgeWeightSumTolerance)
	}
	return nil
}

func validateOrderedFeatures(label string, actual []FeatureValue, expected []string) error {
	if len(actual) != len(expected) {
		return fmt.Errorf("%s count %d does not match manifest count %d", label, len(actual), len(expected))
	}
	for index, feature := range actual {
		if feature.FeatureID != expected[index] {
			return fmt.Errorf("%s item %d identity %q does not match %q", label, index, feature.FeatureID, expected[index])
		}
		if !finite(feature.Value) {
			return fmt.Errorf("%s value %q is not finite", label, feature.FeatureID)
		}
	}
	return nil
}

func normalizeFeatures(features []FeatureValue, parameters normalization) ([]float64, error) {
	normalized := make([]float64, len(features))
	for index, feature := range features {
		delta := feature.Value - parameters.means[index]
		if !finite(delta) {
			return nil, fmt.Errorf("feature %q centered value is not finite", feature.FeatureID)
		}
		normalized[index] = delta / parameters.scales[index]
		if !finite(normalized[index]) {
			return nil, fmt.Errorf("feature %q normalized value is not finite", feature.FeatureID)
		}
	}
	return normalized, nil
}

func finiteDot(left, right []float64) (float64, error) {
	if len(left) != len(right) {
		return 0, fmt.Errorf("vector dimensions %d and %d differ", len(left), len(right))
	}
	terms := make([]float64, len(left))
	for index := range left {
		terms[index] = left[index] * right[index]
		if !finite(terms[index]) {
			return 0, errorsNewNonFiniteNumber
		}
	}
	return finiteSum(terms)
}

var errorsNewNonFiniteNumber = errors.New("numeric operation is not finite")

func finiteSum(values []float64) (float64, error) {
	// Neumaier summation keeps the fixed manifest order while reducing loss from
	// mixed-magnitude terms.
	var sum, correction float64
	for _, value := range values {
		if !finite(value) {
			return 0, errorsNewNonFiniteNumber
		}
		next := sum + value
		if !finite(next) {
			return 0, errorsNewNonFiniteNumber
		}
		if math.Abs(sum) >= math.Abs(value) {
			correction += (sum - next) + value
		} else {
			correction += (value - next) + sum
		}
		if !finite(correction) {
			return 0, errorsNewNonFiniteNumber
		}
		sum = next
	}
	result := sum + correction
	if !finite(result) {
		return 0, errorsNewNonFiniteNumber
	}
	return result, nil
}

func sigmoid(value float64) float64 {
	if value >= 0 {
		return 1 / (1 + math.Exp(-value))
	}
	exponential := math.Exp(value)
	return exponential / (1 + exponential)
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
