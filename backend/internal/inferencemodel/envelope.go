package inferencemodel

import (
	"fmt"
	"math"
)

// ValidateNumericEnvelope proves that normalization, every dot product,
// knowledge aggregation, difficulty construction, and the final logit remain
// finite for every value in the supplied feature-output domains. This gate is
// deliberately separate from Parse because a model artifact binds a feature
// schema by digest while the owning online runtime supplies the schema's typed
// domains.
func (m *Model) ValidateNumericEnvelope(actorDomains, problemDomains []FeatureDomain) error {
	if m == nil {
		return fmt.Errorf("%w: model is nil", ErrInvalidArtifact)
	}
	actorBounds, err := normalizedAbsoluteBounds("actor", m.manifest.ActorFeatureIDs, actorDomains, m.actorNormalization)
	if err != nil {
		return invalidArtifact("numeric envelope", err)
	}
	problemBounds, err := normalizedAbsoluteBounds("problem", m.manifest.ProblemFeatureIDs, problemDomains, m.problemNormalization)
	if err != nil {
		return invalidArtifact("numeric envelope", err)
	}

	thetaBounds := make([]float64, len(m.knowledge))
	for index, parameters := range m.knowledge {
		linearBound, err := dotAbsoluteBound(parameters.actorFeatureWeights, actorBounds)
		if err != nil {
			return invalidArtifact("numeric envelope", fmt.Errorf("knowledge %q actor dot product: %w", m.manifest.KnowledgePointIDs[index], err))
		}
		thetaBounds[index], err = addPositiveBounds(math.Abs(parameters.bias), linearBound)
		if err != nil {
			return invalidArtifact("numeric envelope", fmt.Errorf("knowledge %q theta: %w", m.manifest.KnowledgePointIDs[index], err))
		}
	}

	// Runtime knowledge weights are nonnegative and their finite sum differs
	// from one by at most knowledgeWeightSumTolerance. Therefore the absolute
	// sum of weighted theta terms is bounded by max(thetaBounds)*(1+tolerance).
	// Doubling that sum proves the evaluator's fixed-order Neumaier sum and its
	// correction remain finite.
	maximumThetaBound := 0.0
	for _, bound := range thetaBounds {
		maximumThetaBound = math.Max(maximumThetaBound, bound)
	}
	weightedThetaBound := maximumThetaBound * (1 + knowledgeWeightSumTolerance)
	abilityBound := 2 * weightedThetaBound
	if !finite(weightedThetaBound) || !finite(abilityBound) {
		return invalidArtifact("numeric envelope", fmt.Errorf("knowledge ability: %w", errorsNewNonFiniteNumber))
	}
	problemLinearBound, err := dotAbsoluteBound(m.problemFeatureWeights, problemBounds)
	if err != nil {
		return invalidArtifact("numeric envelope", fmt.Errorf("problem dot product: %w", err))
	}
	difficultyBound, err := addPositiveBounds(math.Abs(m.difficultyBias), problemLinearBound)
	if err != nil {
		return invalidArtifact("numeric envelope", fmt.Errorf("difficulty: %w", err))
	}
	differenceBound, err := addPositiveBounds(abilityBound, difficultyBound)
	if err != nil {
		return invalidArtifact("numeric envelope", fmt.Errorf("ability minus difficulty: %w", err))
	}
	if product := m.discrimination * differenceBound; !finite(product) {
		return invalidArtifact("numeric envelope", errorsNewNonFiniteNumber)
	}
	return nil
}

func normalizedAbsoluteBounds(
	label string,
	identities []string,
	domains []FeatureDomain,
	parameters normalization,
) ([]float64, error) {
	if len(domains) != len(identities) {
		return nil, fmt.Errorf("%s feature domain count %d does not match manifest count %d", label, len(domains), len(identities))
	}
	bounds := make([]float64, len(domains))
	for index, domain := range domains {
		if domain.FeatureID != identities[index] {
			return nil, fmt.Errorf("%s feature domain %d identity %q does not match %q", label, index, domain.FeatureID, identities[index])
		}
		if !finite(domain.Minimum) || !finite(domain.Maximum) || domain.Minimum > domain.Maximum {
			return nil, fmt.Errorf("%s feature %q domain is not a finite ordered interval", label, domain.FeatureID)
		}
		minimumDelta := domain.Minimum - parameters.means[index]
		maximumDelta := domain.Maximum - parameters.means[index]
		if !finite(minimumDelta) || !finite(maximumDelta) {
			return nil, fmt.Errorf("%s feature %q centering can overflow", label, domain.FeatureID)
		}
		centeredBound := math.Max(math.Abs(minimumDelta), math.Abs(maximumDelta))
		bounds[index] = centeredBound / parameters.scales[index]
		if !finite(bounds[index]) {
			return nil, fmt.Errorf("%s feature %q normalization can overflow", label, domain.FeatureID)
		}
	}
	return bounds, nil
}

func dotAbsoluteBound(weights, valueBounds []float64) (float64, error) {
	if len(weights) != len(valueBounds) {
		return 0, fmt.Errorf("vector dimensions %d and %d differ", len(weights), len(valueBounds))
	}
	terms := make([]float64, len(weights))
	for index := range weights {
		terms[index] = math.Abs(weights[index]) * valueBounds[index]
		if !finite(terms[index]) {
			return 0, errorsNewNonFiniteNumber
		}
	}
	return finiteSumAbsoluteBound(terms)
}

// finiteSumAbsoluteBound is conservative for finiteSum's Neumaier correction:
// each partial sum and the correction are bounded by sum(abs(values)), so the
// returned result envelope is twice that absolute sum.
func finiteSumAbsoluteBound(values []float64) (float64, error) {
	var sum float64
	for _, value := range values {
		if !finite(value) || value < 0 {
			return 0, errorsNewNonFiniteNumber
		}
		sum += value
		if !finite(sum) {
			return 0, errorsNewNonFiniteNumber
		}
	}
	bound := 2 * sum
	if !finite(bound) {
		return 0, errorsNewNonFiniteNumber
	}
	return bound, nil
}

func addPositiveBounds(left, right float64) (float64, error) {
	if !finite(left) || !finite(right) || left < 0 || right < 0 {
		return 0, errorsNewNonFiniteNumber
	}
	result := left + right
	if !finite(result) {
		return 0, errorsNewNonFiniteNumber
	}
	return result, nil
}
