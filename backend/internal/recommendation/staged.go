package recommendation

import (
	"context"
	"errors"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

// StagedReaderService is the student inference contract before a verified
// model crosses the activation commit point. Analytics-backed administrator
// review uses ReviewContextPostgresRepository independently of model state.
type StagedReaderService struct{}

func NewStagedReaderService() *StagedReaderService {
	return &StagedReaderService{}
}

func (service *StagedReaderService) ReadCurrent(
	ctx context.Context,
	principal auth.AccessPrincipal,
) (CurrentRecommendation, error) {
	if service == nil || ctx == nil || principal.Role != auth.RoleStudent {
		return CurrentRecommendation{}, domainError(
			ErrorInvalidInput,
			true,
			"read staged current recommendation",
			errors.New("student principal and context are required"),
		)
	}
	return CurrentRecommendation{}, modelInactiveError("read staged current recommendation")
}

func modelInactiveError(operation string) error {
	return domainError(
		ErrorModelInactive,
		true,
		operation,
		errors.New("recommendation model activation has not committed"),
	)
}
