package examgeneration

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
	"github.com/kkkzbh/AscendAny/backend/internal/canonicaljson"
)

var (
	canonicalUUIDv4   = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	canonicalPositive = regexp.MustCompile(`^[1-9][0-9]{0,18}$`)
)

func ValidPublicID(value string) bool {
	return canonicalUUIDv4.MatchString(value)
}

func ValidGenerationID(value string) bool {
	if !canonicalPositive.MatchString(value) {
		return false
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	return err == nil && parsed > 0
}

type Repository interface {
	LoadCurrent(context.Context, CurrentQuery) (Generation, bool, error)
	LoadEvents(context.Context, EventQuery) (EventBatch, bool, error)
}

type Service struct {
	repository Repository
}

func NewService(repository Repository) (*Service, error) {
	if repository == nil {
		return nil, domainError(
			ErrorInvalidConfiguration,
			true,
			"construct exam generation service",
			errors.New("repository is required"),
		)
	}
	return &Service{repository: repository}, nil
}

func (service *Service) GetCurrent(ctx context.Context, query CurrentQuery) (Generation, bool, error) {
	if err := validateCurrentQuery(ctx, query); err != nil {
		return Generation{}, false, err
	}
	generation, found, err := service.repository.LoadCurrent(ctx, query)
	if err != nil || !found {
		return Generation{}, found, err
	}
	if err := validateGeneration(generation); err != nil {
		return Generation{}, false, domainError(ErrorStoredDataInvalid, true, "validate current exam generation", err)
	}
	return generation, true, nil
}

func (service *Service) ReadEvents(ctx context.Context, query EventQuery) (EventBatch, bool, error) {
	if err := validateEventQuery(ctx, query); err != nil {
		return EventBatch{}, false, err
	}
	batch, found, err := service.repository.LoadEvents(ctx, query)
	if err != nil || !found {
		return EventBatch{}, found, err
	}
	if query.AfterSequence > batch.EventHead {
		return EventBatch{}, false, domainError(
			ErrorEventCursorInvalid,
			true,
			"validate exam generation event cursor",
			errors.New("event cursor exceeds the durable generation event head"),
		)
	}
	if batch.GenerationID != query.GenerationID {
		return EventBatch{}, false, domainError(
			ErrorStoredDataInvalid,
			true,
			"validate pinned exam generation identity",
			errors.New("event batch generation does not match the requested generation"),
		)
	}
	if err := validateEventBatch(&batch, query); err != nil {
		return EventBatch{}, false, domainError(ErrorStoredDataInvalid, true, "validate exam generation events", err)
	}
	return batch, true, nil
}

func validateCurrentQuery(ctx context.Context, query CurrentQuery) error {
	if ctx == nil || validatePrincipal(query.Principal) != nil || !canonicalUUIDv4.MatchString(query.ExamID) {
		return domainError(
			ErrorInvalidInput,
			true,
			"validate current exam generation query",
			errors.New("canonical principal, context, and exam ID are required"),
		)
	}
	return nil
}

func validateEventQuery(ctx context.Context, query EventQuery) error {
	if ctx == nil || validatePrincipal(query.Principal) != nil || !canonicalUUIDv4.MatchString(query.ExamID) ||
		!ValidGenerationID(query.GenerationID) ||
		query.AfterSequence < 0 || query.Limit < 1 || query.Limit > MaxEventPageSize {
		return domainError(
			ErrorInvalidInput,
			true,
			"validate exam generation event query",
			errors.New("canonical principal, exam ID, generation ID, nonnegative cursor, and bounded limit are required"),
		)
	}
	return nil
}

func validatePrincipal(principal auth.AccessPrincipal) error {
	if !canonicalUUIDv4.MatchString(principal.AccountID) ||
		!canonicalUUIDv4.MatchString(principal.SessionID) ||
		!canonicalUUIDv4.MatchString(principal.JWTID) ||
		principal.AuthRevision < 1 ||
		(principal.Role != auth.RoleStudent && principal.Role != auth.RoleAdmin) {
		return errors.New("access principal is invalid")
	}
	return nil
}

func validateGeneration(generation Generation) error {
	if !ValidGenerationID(generation.GenerationID) || generation.AttemptCount < 0 || generation.EventHead < 1 ||
		!validUTCTime(generation.CreatedAt) {
		return errors.New("generation identity, counters, or creation time is invalid")
	}
	if generation.StartedAt != nil && (!validUTCTime(*generation.StartedAt) || generation.StartedAt.Before(generation.CreatedAt)) {
		return errors.New("generation start time is invalid")
	}
	if generation.FinishedAt != nil && (!validUTCTime(*generation.FinishedAt) ||
		generation.StartedAt == nil || generation.FinishedAt.Before(*generation.StartedAt)) {
		return errors.New("generation finish time is invalid")
	}
	switch generation.Status {
	case StatusQueued:
		if generation.AttemptCount != 0 || generation.StartedAt != nil || generation.FinishedAt != nil || generation.ErrorCode != nil {
			return errors.New("queued generation state is inconsistent")
		}
	case StatusRunning:
		if generation.AttemptCount < 1 || generation.StartedAt == nil || generation.FinishedAt != nil || generation.ErrorCode != nil {
			return errors.New("running generation state is inconsistent")
		}
	case StatusSucceeded, StatusSuperseded:
		if generation.AttemptCount < 1 || generation.StartedAt == nil || generation.FinishedAt == nil || generation.ErrorCode != nil {
			return errors.New("successful or superseded generation state is inconsistent")
		}
	case StatusFailed:
		if generation.AttemptCount < 1 || generation.StartedAt == nil || generation.FinishedAt == nil ||
			generation.ErrorCode == nil || !validPublicFailureCode(*generation.ErrorCode) {
			return errors.New("failed generation state is inconsistent")
		}
	default:
		return errors.New("generation status is invalid")
	}
	return nil
}

func validateEventBatch(batch *EventBatch, query EventQuery) error {
	if !ValidGenerationID(batch.GenerationID) || batch.EventHead < 1 || len(batch.Events) > query.Limit {
		return errors.New("event batch identity, head, or size is invalid")
	}
	previous := query.AfterSequence
	for index := range batch.Events {
		event := &batch.Events[index]
		if event.Sequence != previous+1 || !validEventType(event.Type) || !validUTCTime(event.CreatedAt) {
			return errors.New("event sequence, type, or creation time is invalid")
		}
		canonical, _, err := canonicaljson.Object(event.Payload, MaxEventPayloadBytes)
		if err != nil {
			return err
		}
		event.Payload = canonical
		if terminalEvent(event.Type) && event.Sequence != batch.EventHead {
			return errors.New("terminal event precedes the durable event head")
		}
		previous = event.Sequence
	}
	if previous > batch.EventHead {
		return errors.New("event page exceeds the durable event head")
	}
	if len(batch.Events) == 0 && query.AfterSequence < batch.EventHead {
		return errors.New("event history contains a gap")
	}
	if len(batch.Events) < query.Limit && previous != batch.EventHead {
		return errors.New("event page ends before the durable event head")
	}
	if batch.Terminal && previous != batch.EventHead {
		return errors.New("terminal event page has unread durable events")
	}
	if previous == batch.EventHead && len(batch.Events) > 0 &&
		batch.Terminal != terminalEvent(batch.Events[len(batch.Events)-1].Type) {
		return errors.New("terminal flag and durable head event disagree")
	}
	return nil
}

func validPublicFailureCode(value string) bool {
	switch value {
	case "invalid_configuration", "invalid_manifest", "algorithm_mismatch", "config_mismatch", "invalid_dataset":
		return true
	default:
		return false
	}
}

func validEventType(value EventType) bool {
	switch value {
	case EventQueued, EventRunning, EventSucceeded, EventSuperseded, EventFailed:
		return true
	default:
		return false
	}
}

func terminalStatus(status Status) bool {
	return status == StatusSucceeded || status == StatusSuperseded || status == StatusFailed
}

func terminalEvent(eventType EventType) bool {
	return eventType == EventSucceeded || eventType == EventSuperseded || eventType == EventFailed
}

func validUTCTime(value time.Time) bool {
	return !value.IsZero() && value.Location() == time.UTC
}
