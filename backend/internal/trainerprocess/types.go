package trainerprocess

import (
	"github.com/kkkzbh/AscendAny/backend/internal/recommendationprotocol"
)

const (
	TrainingBundleProtocolV2 = recommendationprotocol.TrainingBundleV2
	TrainingOutputProtocolV2 = recommendationprotocol.TrainingOutputV2
)

type TrainingRequest = recommendationprotocol.TrainingRequest
type Trainer = recommendationprotocol.Trainer
type TrainerFailure = recommendationprotocol.TrainerFailure

// ConfigurationError distinguishes deterministic startup rejection from a
// child-process training failure without importing an application domain.
type ConfigurationError struct {
	Operation string
	Cause     error
}

func (failure *ConfigurationError) Error() string {
	if failure == nil {
		return "<nil>"
	}
	if failure.Cause == nil {
		return failure.Operation
	}
	return failure.Operation + ": " + failure.Cause.Error()
}

func (failure *ConfigurationError) Unwrap() error {
	if failure == nil {
		return nil
	}
	return failure.Cause
}
