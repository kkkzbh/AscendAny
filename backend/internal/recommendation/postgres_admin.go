package recommendation

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
	"github.com/kkkzbh/AscendAny/backend/internal/principalguard"
)

type ReviewContextPostgresRepository struct {
	begin beginTransaction
}

func NewReviewContextPostgresRepository(pool PgxBeginner) (*ReviewContextPostgresRepository, error) {
	if pool == nil {
		return nil, domainError(ErrorInvalidConfiguration, true, "construct recommendation review repository", errors.New("database pool is required"))
	}
	return &ReviewContextPostgresRepository{begin: func(ctx context.Context, options pgx.TxOptions) (recommendationTx, error) {
		return pool.BeginTx(ctx, options)
	}}, nil
}

func (repository *ReviewContextPostgresRepository) ReadReviewContext(ctx context.Context, principal auth.AccessPrincipal) (result ReviewContext, resultErr error) {
	resultErr = readTransaction(ctx, repository.begin, "read recommendation review context", func(tx recommendationTx) error {
		if _, err := principalguard.Resolve(ctx, tx, principal, principalguard.Roles(auth.RoleAdmin)); err != nil {
			return mapPrincipalError("authorize recommendation review context", err)
		}
		var loadErr error
		result, loadErr = loadReviewContext(ctx, tx, false)
		return loadErr
	})
	return result, resultErr
}

func loadReviewContext(ctx context.Context, tx recommendationQuery, lockHead bool) (ReviewContext, error) {
	var state analyticsState
	var err error
	if lockHead {
		state, err = lockAnalyticsState(ctx, tx)
	} else {
		state, err = loadAnalyticsState(ctx, tx)
	}
	if err != nil {
		return ReviewContext{}, err
	}
	if state.GenerationID == nil {
		return ReviewContext{}, domainError(ErrorAnalyticsUnavailable, true, "read recommendation review context", errors.New("current succeeded analytics head is unavailable"))
	}
	rows, err := queryProblemRows(ctx, tx, *state.GenerationID, false)
	if err != nil {
		return ReviewContext{}, err
	}
	problems, err := buildReviewCandidates(rows)
	if err != nil {
		return ReviewContext{}, domainError(ErrorStoredDataInvalid, true, "build recommendation review context", err)
	}
	return ReviewContext{
		AnalyticsGenerationID: *state.GenerationID, AnalyticsHeadRevision: state.HeadRevision,
		InputManifestSHA256: state.InputManifestSHA256, Problems: problems,
	}, nil
}
