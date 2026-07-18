package recommendation

import (
	"context"
	"errors"
	"slices"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
	"github.com/kkkzbh/AscendAny/backend/internal/inferencemodel"
	"github.com/kkkzbh/AscendAny/backend/internal/principalguard"
)

func (repository *PostgresRepository) ReadCurrent(ctx context.Context, principal auth.AccessPrincipal) (result CurrentRecommendation, resultErr error) {
	resultErr = repository.transaction(ctx, "read current recommendation", func(tx recommendationTx) error {
		resolved, err := principalguard.Resolve(ctx, tx, principal, principalguard.Roles(auth.RoleStudent))
		if err != nil {
			return mapPrincipalError("authorize current recommendation", err)
		}
		if resolved.ActorID == nil || *resolved.ActorID <= 0 {
			return domainError(ErrorStoredDataInvalid, true, "authorize current recommendation", errors.New("student principal has no actor binding"))
		}

		provenance, err := repository.loadModelProvenance(ctx, tx)
		if err != nil {
			return err
		}
		result.Model = &provenance
		result.ModelHeadRevision = provenance.ModelHeadRevision

		analyticsState, err := loadAnalyticsState(ctx, tx)
		if err != nil {
			return err
		}
		result.CurrentAnalyticsHeadRevision = analyticsState.HeadRevision
		if analyticsState.GenerationID != nil {
			value := analyticsIDString(*analyticsState.GenerationID)
			result.CurrentAnalyticsGenerationID = &value
		}
		if err := repository.validateCurrentAnalyticsBinding(analyticsState); err != nil {
			return err
		}

		catalogState, err := loadActiveCatalog(ctx, tx)
		if err != nil {
			return err
		}
		var student studentState
		var problems []problemRow
		var observations []observationRow
		if analyticsState.GenerationID != nil {
			student, err = loadStudentState(ctx, tx, *analyticsState.GenerationID, *resolved.ActorID)
			if err != nil {
				return err
			}
			problems, err = queryProblemRows(ctx, tx, *analyticsState.GenerationID, true)
			if err != nil {
				return err
			}
			observations, err = queryObservations(ctx, tx, *analyticsState.GenerationID, *resolved.ActorID)
			if err != nil {
				return err
			}
		}

		if analyticsState.GenerationID == nil {
			setUnavailable(&result, UnavailableAnalytics)
			return nil
		}
		if !student.Available {
			setUnavailable(&result, UnavailableActorAnalytics)
			return nil
		}
		manifest := repository.model.Manifest()
		if reason := activeCatalogUnavailableReason(catalogState, manifest); reason != nil {
			setUnavailable(&result, *reason)
			return nil
		}
		problemFacts, err := buildProblemFactIndex(problems)
		if err != nil {
			return domainError(ErrorStoredDataInvalid, true, "build current recommendation problem facts", err)
		}
		problemActivity, err := queryProblemActivity(ctx, tx, *analyticsState.GenerationID, *resolved.ActorID, repository.acceptedVerdicts)
		if err != nil {
			return err
		}
		recentActivity, err := queryRecentActivity(ctx, tx, *analyticsState.GenerationID, *resolved.ActorID, repository.acceptedVerdicts)
		if err != nil {
			return err
		}
		knowledgeActivity, err := buildKnowledgeActivity(catalogState.Catalog, problemFacts, problemActivity, recentActivity)
		if err != nil {
			return domainError(ErrorStoredDataInvalid, true, "build current recommendation knowledge activity", err)
		}
		actorFeatures, rating, err := buildActorFeatures(student.Rating, student.MetricsJSON)
		if err != nil {
			return domainError(ErrorStoredDataInvalid, true, "build current recommendation actor features", err)
		}
		evidence, err := buildObservationEvidence(observations, catalogState.Catalog)
		if err != nil {
			return domainError(ErrorStoredDataInvalid, true, "build current recommendation evidence", err)
		}
		candidates, err := buildCandidates(problems, problemFacts, catalogState.Catalog, manifest.KnowledgePointIDs, evidence.PassedSources)
		if err != nil {
			return domainError(ErrorStoredDataInvalid, true, "build current recommendation candidates", err)
		}
		if len(candidates) == 0 {
			setUnavailable(&result, UnavailableEligibleProblem)
			return nil
		}
		inference, err := materializeInference(repository.model, catalogState.Catalog, actorFeatures, rating, candidates, evidence)
		if err != nil {
			return domainError(ErrorStoredDataInvalid, true, "materialize current recommendation inference", err)
		}
		result.State = RecommendationFresh
		result.Result = &inference
		result.KnowledgeActivity = knowledgeActivity
		return nil
	})
	return result, resultErr
}

func (repository *PostgresRepository) validateCurrentAnalyticsBinding(state analyticsState) error {
	if state.GenerationID == nil {
		return nil
	}
	if state.AlgorithmVersion != repository.analyticsAlgorithmVersion ||
		state.ConfigSHA256 != repository.analyticsConfigSHA256 {
		return domainError(ErrorStoredDataInvalid, true, "bind current recommendation analytics configuration", errors.New("analytics generation algorithm or config SHA-256 differs from the runtime configuration"))
	}
	return nil
}

func activeCatalogUnavailableReason(state catalogState, manifest inferencemodel.Manifest) *UnavailableReason {
	if !state.Available {
		reason := UnavailableKnowledge
		return &reason
	}
	catalogIDs := make([]string, len(state.Catalog.Points))
	for index, point := range state.Catalog.Points {
		catalogIDs[index] = point.ID
	}
	if state.Digest != manifest.KnowledgeCatalogSHA256 || !slices.Equal(catalogIDs, manifest.KnowledgePointIDs) {
		reason := UnavailableKnowledgeMatch
		return &reason
	}
	return nil
}

func setUnavailable(result *CurrentRecommendation, reason UnavailableReason) {
	result.State = RecommendationUnavailable
	result.UnavailableReason = &reason
	result.Result = nil
}
