package studentanalytics

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/kkkzbh/AscendAny/backend/internal/analytics"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

const MaxLeaderboardLimit = 200

type LeaderboardQuery struct {
	AccountID            string
	SessionID            string
	ExpectedAuthRevision int64
	ExpectedRole         auth.Role
	Limit                int
}

type LeaderboardResult struct {
	State        State
	HeadRevision int64
	Population   int64
	Items        []LeaderboardItem
}

type LeaderboardItem struct {
	Rank          int64
	StudentNumber string
	DisplayName   *string
	Rating        int64
	Metrics       analytics.MetricValues
}

type LeaderboardRepository interface {
	LoadLeaderboard(context.Context, LeaderboardQuery) (LeaderboardResult, error)
}

type LeaderboardService struct {
	repository LeaderboardRepository
}

func NewLeaderboardService(repository LeaderboardRepository) (*LeaderboardService, error) {
	if repository == nil {
		return nil, studentAnalyticsError(ErrorInvalidConfiguration, "construct leaderboard service", errors.New("repository is required"))
	}
	return &LeaderboardService{repository: repository}, nil
}

func (service *LeaderboardService) Get(
	ctx context.Context,
	query LeaderboardQuery,
) (LeaderboardResult, error) {
	if err := validateLeaderboardQuery(ctx, query); err != nil {
		return LeaderboardResult{}, err
	}
	result, err := service.repository.LoadLeaderboard(ctx, query)
	if err != nil {
		return LeaderboardResult{}, err
	}
	if err := validateLeaderboardResult(result, query.Limit); err != nil {
		return LeaderboardResult{}, storedDataFailure("validate leaderboard repository result", err)
	}
	return result, nil
}

func validateLeaderboardQuery(ctx context.Context, query LeaderboardQuery) error {
	if ctx == nil {
		return studentAnalyticsError(ErrorInvalidQuery, "validate leaderboard query", errors.New("context is required"))
	}
	if !canonicalUUIDv4Pattern.MatchString(query.AccountID) {
		return studentAnalyticsError(ErrorInvalidQuery, "validate leaderboard query", errors.New("account ID must be a canonical UUIDv4"))
	}
	if !canonicalUUIDv4Pattern.MatchString(query.SessionID) {
		return studentAnalyticsError(ErrorInvalidQuery, "validate leaderboard query", errors.New("session ID must be a canonical UUIDv4"))
	}
	if query.ExpectedAuthRevision <= 0 {
		return studentAnalyticsError(ErrorInvalidQuery, "validate leaderboard query", errors.New("expected auth revision must be positive"))
	}
	if query.ExpectedRole != auth.RoleStudent {
		return studentAnalyticsError(ErrorForbidden, "validate leaderboard query", errors.New("student role is required"))
	}
	if query.Limit < 1 || query.Limit > MaxLeaderboardLimit {
		return studentAnalyticsError(
			ErrorInvalidQuery,
			"validate leaderboard query",
			fmt.Errorf("limit must be in [1, %d]", MaxLeaderboardLimit),
		)
	}
	return nil
}

func validateLeaderboardResult(result LeaderboardResult, limit int) error {
	switch result.State {
	case StateNotGenerated:
		if result.HeadRevision != 0 || result.Population != 0 || len(result.Items) != 0 {
			return errors.New("not_generated leaderboard must be empty at head revision zero")
		}
		return nil
	case StateNoObservations:
		if result.HeadRevision <= 0 || result.Population != 0 || len(result.Items) != 0 {
			return errors.New("no_observations leaderboard must be empty at a positive head revision")
		}
		return nil
	case StateReady:
		if result.HeadRevision <= 0 || result.Population <= 0 || len(result.Items) == 0 ||
			len(result.Items) > limit || int64(len(result.Items)) > result.Population {
			return errors.New("ready leaderboard cardinality is invalid")
		}
	default:
		return fmt.Errorf("unknown leaderboard state %q", result.State)
	}

	seenNumbers := make(map[string]struct{}, len(result.Items))
	var previous *LeaderboardItem
	for index := range result.Items {
		item := &result.Items[index]
		if item.Rank <= 0 || item.Rank > result.Population || item.Rating < 0 ||
			strings.TrimSpace(item.StudentNumber) != item.StudentNumber || item.StudentNumber == "" ||
			len(item.StudentNumber) > auth.MaxStudentNumberBytes {
			return fmt.Errorf("leaderboard item %d has invalid rank, rating, or student number", index)
		}
		if _, exists := seenNumbers[item.StudentNumber]; exists {
			return fmt.Errorf("leaderboard item %d duplicates a student number", index)
		}
		seenNumbers[item.StudentNumber] = struct{}{}
		if item.DisplayName != nil && (strings.TrimSpace(*item.DisplayName) != *item.DisplayName || *item.DisplayName == "" ||
			len(*item.DisplayName) > auth.MaxDisplayNameBytes) {
			return fmt.Errorf("leaderboard item %d has a non-canonical display name", index)
		}
		if err := validateMetricValues(item.Metrics); err != nil {
			return fmt.Errorf("leaderboard item %d metrics: %w", index, err)
		}
		if previous != nil {
			if item.Rating > previous.Rating ||
				(item.Rating == previous.Rating && item.StudentNumber < previous.StudentNumber) {
				return fmt.Errorf("leaderboard item %d is not in canonical order", index)
			}
			if item.Rating == previous.Rating && item.Rank != previous.Rank {
				return fmt.Errorf("leaderboard item %d breaks a rating tie", index)
			}
			if item.Rating < previous.Rating && item.Rank <= previous.Rank {
				return fmt.Errorf("leaderboard item %d rank did not advance", index)
			}
		}
		previous = item
	}
	return nil
}
