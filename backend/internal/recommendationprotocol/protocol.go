// Package recommendationprotocol defines the capability-free value contract
// shared by the recommendation product, the remote trainer agent, and the
// isolated trainer process implementation.
package recommendationprotocol

import (
	"context"
	"encoding/json"
)

const (
	TrainingBundleV2          = "ascendany.recommendation.training-bundle.v2"
	TrainingOutputV2          = "ascendany.recommendation.training-output.v2"
	ModelV2                   = "ascendany.recommendation.model.v2"
	ResultV2                  = "ascendany.recommendation.result.v2"
	KnowledgeMIRTParametersV1 = "ascendany.recommendation.parameters.knowledge-mirt.v1"
	TrainingConfigurationV2   = "ascendany.training.recommendation.v2"
	KnowledgeCatalogV1        = "ascendany.knowledge_catalog.recommendation.v1"
	KnowledgeMIRTAlgorithmV1  = "knowledge_mirt_v1"
)

type TrainingRequest struct {
	RunID               string
	InputBundle         json.RawMessage
	InputManifestSHA256 string
}

type Trainer interface {
	Train(context.Context, TrainingRequest) ([]byte, error)
}

type TrainerFailure struct {
	Code      string
	Detail    string
	Retryable bool
	Cause     error
}

func (failure *TrainerFailure) Error() string {
	if failure == nil {
		return "<nil>"
	}
	if failure.Cause != nil {
		return failure.Code + ": " + failure.Detail + ": " + failure.Cause.Error()
	}
	return failure.Code + ": " + failure.Detail
}

func (failure *TrainerFailure) Unwrap() error {
	if failure == nil {
		return nil
	}
	return failure.Cause
}
