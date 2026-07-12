package studentanalytics

import (
	"context"
	"errors"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

type AccessPrincipalVerifier interface {
	VerifyAccessToken(string) (auth.AccessPrincipal, error)
}

type SelfReader interface {
	GetSelf(context.Context, SelfQuery) (Result, error)
}

type LeaderboardReader interface {
	Get(context.Context, LeaderboardQuery) (LeaderboardResult, error)
}

// ApplicationService verifies the signed access credential, then passes its
// immutable principal to repositories that revalidate the session inside the
// same database snapshot used for product data.
type ApplicationService struct {
	verifier    AccessPrincipalVerifier
	self        SelfReader
	leaderboard LeaderboardReader
}

func NewApplicationService(
	verifier AccessPrincipalVerifier,
	self SelfReader,
	leaderboard LeaderboardReader,
) (*ApplicationService, error) {
	if verifier == nil || self == nil || leaderboard == nil {
		return nil, studentAnalyticsError(
			ErrorInvalidConfiguration,
			"construct student analytics application service",
			errors.New("principal verifier, self reader, and leaderboard reader are required"),
		)
	}
	return &ApplicationService{verifier: verifier, self: self, leaderboard: leaderboard}, nil
}

func (service *ApplicationService) GetSelf(
	ctx context.Context,
	accessToken string,
	historyLimit int,
) (Result, error) {
	principal, err := service.verifier.VerifyAccessToken(accessToken)
	if err != nil {
		return Result{}, err
	}
	return service.self.GetSelf(ctx, SelfQuery{
		AccountID:            principal.AccountID,
		SessionID:            principal.SessionID,
		ExpectedAuthRevision: principal.AuthRevision,
		ExpectedRole:         principal.Role,
		HistoryLimit:         historyLimit,
	})
}

func (service *ApplicationService) GetLeaderboard(
	ctx context.Context,
	accessToken string,
	limit int,
) (LeaderboardResult, error) {
	principal, err := service.verifier.VerifyAccessToken(accessToken)
	if err != nil {
		return LeaderboardResult{}, err
	}
	return service.leaderboard.Get(ctx, LeaderboardQuery{
		AccountID:            principal.AccountID,
		SessionID:            principal.SessionID,
		ExpectedAuthRevision: principal.AuthRevision,
		ExpectedRole:         principal.Role,
		Limit:                limit,
	})
}
