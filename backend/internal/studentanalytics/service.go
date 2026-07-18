package studentanalytics

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/analytics"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

var canonicalUUIDv4Pattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

type Repository interface {
	LoadSelf(context.Context, SelfQuery) (Result, error)
}

type Service struct {
	repository Repository
}

func NewService(repository Repository) (*Service, error) {
	if repository == nil {
		return nil, studentAnalyticsError(ErrorInvalidConfiguration, "construct service", errors.New("repository is required"))
	}
	return &Service{repository: repository}, nil
}

func (service *Service) GetSelf(ctx context.Context, query SelfQuery) (Result, error) {
	if err := validateSelfQuery(ctx, query); err != nil {
		return Result{}, err
	}
	result, err := service.repository.LoadSelf(ctx, query)
	if err != nil {
		return Result{}, err
	}
	if err := validateResultShape(result, query.HistoryLimit); err != nil {
		return Result{}, storedDataFailure("validate repository result", err)
	}
	return result, nil
}

func validateSelfQuery(ctx context.Context, query SelfQuery) error {
	if ctx == nil {
		return studentAnalyticsError(ErrorInvalidQuery, "validate self query", errors.New("context is required"))
	}
	if !canonicalUUIDv4Pattern.MatchString(query.AccountID) {
		return studentAnalyticsError(ErrorInvalidQuery, "validate self query", errors.New("account ID must be a canonical UUIDv4"))
	}
	if !canonicalUUIDv4Pattern.MatchString(query.SessionID) {
		return studentAnalyticsError(ErrorInvalidQuery, "validate self query", errors.New("session ID must be a canonical UUIDv4"))
	}
	if query.ExpectedAuthRevision <= 0 {
		return studentAnalyticsError(ErrorInvalidQuery, "validate self query", errors.New("expected auth revision must be positive"))
	}
	if query.ExpectedRole != auth.RoleStudent {
		return studentAnalyticsError(ErrorForbidden, "validate self query", errors.New("student role is required"))
	}
	if query.HistoryLimit <= 0 || query.HistoryLimit > MaxHistoryLimit {
		return studentAnalyticsError(ErrorInvalidQuery, "validate self query", fmt.Errorf("history limit must be in [1, %d]", MaxHistoryLimit))
	}
	return nil
}

func validateResultShape(result Result, historyLimit int) error {
	switch result.State {
	case StateNotGenerated:
		if result.HeadRevision != 0 || result.Ready != nil {
			return errors.New("not_generated requires head revision zero and no ready payload")
		}
	case StateNoObservations:
		if result.HeadRevision <= 0 || result.Ready != nil {
			return errors.New("no_observations requires a positive head revision and no ready payload")
		}
	case StateReady:
		if result.HeadRevision <= 0 || result.Ready == nil {
			return errors.New("ready requires a positive head revision and payload")
		}
		ready := result.Ready
		if ready.Rating < 0 || !validUTCTime(ready.ReferenceTime) {
			return errors.New("ready payload has invalid rating or reference time")
		}
		if err := validateMetricValues(ready.Current); err != nil {
			return fmt.Errorf("ready current metrics: %w", err)
		}
		if len(ready.ExamHistory) == 0 || len(ready.ExamHistory) != len(ready.RatingHistory) || len(ready.ExamHistory) > historyLimit {
			return errors.New("ready histories are empty, misaligned, or exceed the requested limit")
		}
		seenExamIDs := make(map[string]struct{}, len(ready.ExamHistory))
		seenSnapshotIDs := make(map[string]struct{}, len(ready.ExamHistory))
		var previousEventTime time.Time
		var previousNewRating int64
		for index := range ready.ExamHistory {
			exam := ready.ExamHistory[index]
			rating := ready.RatingHistory[index]
			if !canonicalUUIDv4Pattern.MatchString(exam.ExamID) || !canonicalUUIDv4Pattern.MatchString(exam.SnapshotID) || strings.TrimSpace(exam.Title) == "" ||
				exam.ExamID != rating.ExamID || exam.SnapshotID != rating.SnapshotID || exam.Title != rating.Title || !exam.EventTime.Equal(rating.EventTime) {
				return fmt.Errorf("ready history point %d is invalid or misaligned", index)
			}
			if !validUTCTime(exam.EventTime) || !validUTCTime(rating.EventTime) || exam.EventTime.After(ready.ReferenceTime) || index > 0 && exam.EventTime.Before(previousEventTime) {
				return fmt.Errorf("ready history point %d has invalid or descending event time", index)
			}
			if _, exists := seenExamIDs[exam.ExamID]; exists {
				return fmt.Errorf("ready history point %d duplicates exam ID", index)
			}
			seenExamIDs[exam.ExamID] = struct{}{}
			if _, exists := seenSnapshotIDs[exam.SnapshotID]; exists {
				return fmt.Errorf("ready history point %d duplicates snapshot ID", index)
			}
			seenSnapshotIDs[exam.SnapshotID] = struct{}{}
			if err := validateMetricValues(exam.Values); err != nil {
				return fmt.Errorf("ready history point %d metrics: %w", index, err)
			}
			if rating.Rank <= 0 || rating.OldRating < 0 || rating.NewRating < 0 || rating.NewRating-rating.OldRating != rating.Delta || !finite(rating.Seed) || !finite(rating.Performance) {
				return fmt.Errorf("ready rating point %d is invalid", index)
			}
			if index > 0 && rating.OldRating != previousNewRating {
				return fmt.Errorf("ready rating point %d does not continue the previous rating", index)
			}
			previousEventTime = exam.EventTime
			previousNewRating = rating.NewRating
		}
		if ready.Rating != ready.RatingHistory[len(ready.RatingHistory)-1].NewRating {
			return errors.New("ready canonical rating differs from rating history")
		}
		if ready.LatestPeer != nil {
			if err := validateLatestExamPeer(*ready.LatestPeer); err != nil {
				return fmt.Errorf("ready latest peer: %w", err)
			}
			if ready.LatestPeer.Rank != ready.RatingHistory[len(ready.RatingHistory)-1].Rank {
				return errors.New("ready latest peer rank differs from rating history")
			}
		}
	default:
		return fmt.Errorf("unknown result state %q", result.State)
	}
	return nil
}

func validateMetricValues(values analytics.MetricValues) error {
	for _, metric := range []struct {
		name  string
		value *float64
	}{
		{name: "knowledge", value: values.Knowledge},
		{name: "accuracy", value: values.Accuracy},
		{name: "quality", value: values.Quality},
		{name: "flexibility", value: values.Flexibility},
		{name: "proficiency", value: values.Proficiency},
	} {
		if metric.value != nil && (!finite(*metric.value) || *metric.value < 0 || *metric.value > 100) {
			return fmt.Errorf("%s must be nil or finite in [0, 100]", metric.name)
		}
	}
	return nil
}

func validUTCTime(value time.Time) bool {
	if value.IsZero() {
		return false
	}
	_, offset := value.Zone()
	return offset == 0
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
