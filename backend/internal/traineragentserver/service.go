package traineragentserver

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/kkkzbh/AscendAny/backend/internal/artifact"
	"github.com/kkkzbh/AscendAny/backend/internal/canonicaljson"
	"github.com/kkkzbh/AscendAny/backend/internal/recommendation"
	"github.com/kkkzbh/AscendAny/backend/internal/traineragentprotocol"
	"github.com/kkkzbh/AscendAny/backend/internal/workerlease"
)

const (
	maximumLeaseDuration       = 24 * time.Hour
	maximumFailureDetailBytes  = 2048
	maximumStoredFailureBytes  = 4096
	maximumTransportBundleSize = 1 << 30
)

var (
	uuidV4Pattern     = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	sha256Pattern     = regexp.MustCompile(`^[0-9a-f]{64}$`)
	remoteCodePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,127}$`)
)

type UUIDGenerator func() (string, error)

type ServiceConfig struct {
	LeaseDuration            time.Duration
	RetryDelay               time.Duration
	MaximumInputBundleBytes  int
	MaximumOutputBundleBytes int
}

type Service struct {
	repository recommendation.TrainerAgentRepository
	artifacts  recommendation.ArtifactStore
	config     ServiceConfig
	uuid       UUIDGenerator
}

func NewService(
	repository recommendation.TrainerAgentRepository,
	artifacts recommendation.ArtifactStore,
	config ServiceConfig,
) (*Service, error) {
	return newService(repository, artifacts, config, randomUUIDv4)
}

func newService(
	repository recommendation.TrainerAgentRepository,
	artifacts recommendation.ArtifactStore,
	config ServiceConfig,
	uuid UUIDGenerator,
) (*Service, error) {
	if repository == nil || artifacts == nil || uuid == nil || config.RetryDelay < time.Second ||
		config.RetryDelay%time.Millisecond != 0 ||
		config.MaximumInputBundleBytes <= 0 || config.MaximumInputBundleBytes > maximumTransportBundleSize ||
		config.MaximumOutputBundleBytes <= 0 || config.MaximumOutputBundleBytes > maximumTransportBundleSize ||
		config.LeaseDuration > maximumLeaseDuration || config.LeaseDuration%time.Millisecond != 0 {
		return nil, errors.New("trainer-agent server requires repositories, artifact store, UUID generator, bounded bundles, and whole-millisecond lease and retry policy")
	}
	if _, err := workerlease.ValidateDuration(config.LeaseDuration); err != nil {
		return nil, fmt.Errorf("trainer-agent server lease duration: %w", err)
	}
	return &Service{repository: repository, artifacts: artifacts, config: config, uuid: uuid}, nil
}

func (service *Service) MaximumOutputRequestBytes() int64 {
	return int64(service.config.MaximumOutputBundleBytes) + (1 << 20)
}

func (service *Service) MaximumClaimResponseBytes() int64 {
	return int64(service.config.MaximumInputBundleBytes) + (1 << 20)
}

func (service *Service) Claim(
	ctx context.Context,
	agentID string,
	request traineragentprotocol.ClaimRequestV1,
) (*traineragentprotocol.ClaimResponseV1, error) {
	if ctx == nil || !agentIDPattern.MatchString(agentID) || request.AgentID != agentID ||
		request.LeaseDurationMilliseconds != service.config.LeaseDuration.Milliseconds() {
		return nil, errorValue(ErrorInvalidRequest, "Claim request is invalid.", false, errors.New("claim envelope differs from configured agent or lease"))
	}
	if request.Protocol != traineragentprotocol.ClaimRequestProtocolV1 {
		return nil, errorValue(ErrorUnsupportedProtocol, "Trainer-agent protocol is unsupported.", false, errors.New("claim protocol differs"))
	}
	attemptToken, err := service.uuid()
	if err != nil || !uuidV4Pattern.MatchString(attemptToken) {
		return nil, errorValue(ErrorServiceUnavailable, "Trainer-agent service is unavailable.", true, errors.New("UUID generation failed"))
	}
	claim, err := service.repository.ClaimTraining(ctx, agentID, attemptToken, service.config.LeaseDuration)
	if err != nil || claim == nil {
		return nil, mapRecommendationError(err)
	}
	bundle, err := service.loadInputBundle(ctx, *claim)
	if err != nil {
		if transitionErr := service.abortUndeliveredClaim(ctx, *claim, err); transitionErr != nil {
			return nil, transitionErr
		}
		return nil, err
	}
	parsed, err := recommendation.ParseInputBundle(bundle, service.config.MaximumInputBundleBytes, claim.TrainingRun)
	if err != nil || parsed.ManifestSHA256 != claim.InputManifestSHA256 {
		cause := err
		if cause == nil {
			cause = errors.New("parsed input manifest digest differs from the claimed manifest")
		}
		invalid := errorValue(ErrorStorageUnavailable, "Training input is unavailable.", false, cause)
		if transitionErr := service.abortUndeliveredClaim(ctx, *claim, invalid); transitionErr != nil {
			return nil, transitionErr
		}
		return nil, invalid
	}
	_, bundleSHA256, err := canonicaljson.Object(bundle, service.config.MaximumInputBundleBytes)
	if err != nil {
		if transitionErr := service.abortUndeliveredClaim(ctx, *claim, err); transitionErr != nil {
			return nil, transitionErr
		}
		return nil, errorValue(ErrorStorageUnavailable, "Training input is unavailable.", false, err)
	}
	return &traineragentprotocol.ClaimResponseV1{
		Protocol: traineragentprotocol.ClaimResponseProtocolV1, RunID: claim.ID, AttemptToken: claim.AttemptToken,
		LeaseDurationMilliseconds: request.LeaseDurationMilliseconds,
		LeaseExpiresAt:            claim.LeaseExpiresAt.UTC().Format(time.RFC3339Nano),
		InputManifestSHA256:       claim.InputManifestSHA256,
		InputBundleSHA256:         bundleSHA256,
		InputBundle:               json.RawMessage(bytes.Clone(bundle)),
	}, nil
}

func (service *Service) Heartbeat(
	ctx context.Context,
	runID, agentID string,
	request traineragentprotocol.HeartbeatRequestV1,
) (traineragentprotocol.HeartbeatResponseV1, error) {
	attempt, err := validateAttemptEnvelope(
		ctx, runID, agentID, request.AgentID, request.AttemptToken, request.Protocol,
		traineragentprotocol.HeartbeatRequestProtocolV1,
	)
	if err != nil {
		return traineragentprotocol.HeartbeatResponseV1{}, err
	}
	expiresAt, err := service.repository.RenewTrainerAgentLease(ctx, attempt, service.config.LeaseDuration)
	if err != nil {
		return traineragentprotocol.HeartbeatResponseV1{}, mapRecommendationError(err)
	}
	return traineragentprotocol.HeartbeatResponseV1{
		Protocol:       traineragentprotocol.HeartbeatResponseProtocolV1,
		LeaseExpiresAt: expiresAt.UTC().Format(time.RFC3339Nano),
	}, nil
}

func (service *Service) Publish(
	ctx context.Context,
	runID, agentID, requestSHA256 string,
	request traineragentprotocol.OutputRequestV1,
) (traineragentprotocol.OutputResponseV1, error) {
	attempt, err := validateAttemptEnvelope(
		ctx, runID, agentID, request.AgentID, request.AttemptToken, request.Protocol,
		traineragentprotocol.OutputRequestProtocolV1,
	)
	if err != nil {
		return traineragentprotocol.OutputResponseV1{}, err
	}
	if err := verifyCanonicalRequestDigest(request, requestSHA256); err != nil {
		return traineragentprotocol.OutputResponseV1{}, err
	}
	replayed, err := service.repository.LookupTrainerAgentTerminalReceipt(
		ctx, attempt, recommendation.TrainerAgentOutputOperation, requestSHA256,
	)
	if err != nil {
		return traineragentprotocol.OutputResponseV1{}, mapRecommendationError(err)
	}
	if replayed != nil {
		return outputResponseFromReceipt(*replayed)
	}
	claim, _, err := service.repository.ResolveTrainerAgentClaim(ctx, attempt)
	if err != nil {
		return traineragentprotocol.OutputResponseV1{}, mapRecommendationError(err)
	}
	if !sha256Pattern.MatchString(request.InputManifestSHA256) || request.InputManifestSHA256 != claim.InputManifestSHA256 ||
		!sha256Pattern.MatchString(request.OutputBundleSHA256) {
		return traineragentprotocol.OutputResponseV1{}, service.rejectOutput(ctx, claim, requestSHA256, errors.New("declared output provenance is invalid"))
	}
	canonicalOutput, digest, canonicalErr := canonicaljson.Object(request.OutputBundle, service.config.MaximumOutputBundleBytes)
	if canonicalErr != nil || !bytes.Equal(canonicalOutput, request.OutputBundle) || digest != request.OutputBundleSHA256 {
		return traineragentprotocol.OutputResponseV1{}, service.rejectOutput(ctx, claim, requestSHA256, errors.New("output bundle bytes or digest are invalid"))
	}
	inputBundle, inputErr := service.loadInputBundle(ctx, claim)
	if inputErr != nil {
		if transitionErr := service.abortUndeliveredClaim(ctx, claim, inputErr); transitionErr != nil {
			return traineragentprotocol.OutputResponseV1{}, transitionErr
		}
		return traineragentprotocol.OutputResponseV1{}, inputErr
	}
	parsedInput, inputParseErr := recommendation.ParseInputBundle(
		inputBundle, service.config.MaximumInputBundleBytes, claim.TrainingRun,
	)
	if inputParseErr != nil {
		invalid := errorValue(ErrorStorageUnavailable, "Training input is unavailable.", false, inputParseErr)
		if transitionErr := service.abortUndeliveredClaim(ctx, claim, invalid); transitionErr != nil {
			return traineragentprotocol.OutputResponseV1{}, transitionErr
		}
		return traineragentprotocol.OutputResponseV1{}, invalid
	}
	parsed, parseErr := recommendation.ParseOutputBundle(
		canonicalOutput, service.config.MaximumOutputBundleBytes, parsedInput,
	)
	if parseErr != nil {
		return traineragentprotocol.OutputResponseV1{}, service.rejectOutput(ctx, claim, requestSHA256, parseErr)
	}
	modelID, err := service.uuid()
	if err != nil || !uuidV4Pattern.MatchString(modelID) {
		return traineragentprotocol.OutputResponseV1{}, errorValue(ErrorServiceUnavailable, "Trainer-agent service is unavailable.", true, errors.New("model UUID generation failed"))
	}
	publication, err := service.artifacts.Publish(ctx, bytes.NewReader(parsed.CanonicalJSON))
	if err != nil {
		return traineragentprotocol.OutputResponseV1{}, mapArtifactError(err)
	}
	if publication == nil {
		return traineragentprotocol.OutputResponseV1{}, errorValue(ErrorStorageUnavailable, "Training output could not be published.", true, errors.New("artifact store returned no publication"))
	}
	result, err := service.publishOutputWithLock(ctx, publication, recommendation.PublishCommand{
		Claim: claim, ModelPublicID: modelID, Input: parsedInput, Output: parsed, Artifact: publication.Artifact,
		MediaType: recommendation.TrainingOutputMediaTypeV2,
		Receipt: &recommendation.TrainerAgentReceiptCommand{
			Attempt: attempt, Operation: recommendation.TrainerAgentOutputOperation, RequestSHA256: requestSHA256,
		},
	})
	if err != nil {
		return traineragentprotocol.OutputResponseV1{}, err
	}
	disposition := string(traineragentprotocol.PublicationSuperseded)
	if result.Disposition == recommendation.PublishActivated {
		disposition = string(traineragentprotocol.PublicationActivated)
	} else if result.Disposition != recommendation.PublishSuperseded {
		return traineragentprotocol.OutputResponseV1{}, errorValue(ErrorServiceUnavailable, "Trainer-agent service is unavailable.", true, errors.New("repository publication disposition is invalid"))
	}
	return traineragentprotocol.OutputResponseV1{
		Protocol: traineragentprotocol.OutputResponseProtocolV1, Disposition: disposition, ModelID: result.ModelID,
		RuntimeConstructionSHA256: parsed.Model.RuntimeConstructionSHA256,
		RuntimeProvenanceSHA256:   parsed.Model.RuntimeProvenanceSHA256,
		RuntimeTreeSHA256:         parsed.Model.RuntimeTreeSHA256,
		HostCapabilitySHA256:      parsed.Model.HostCapabilitySHA256,
		RuntimeAttestationSHA256:  parsed.Model.RuntimeAttestationSHA256,
	}, nil
}

func (service *Service) ReportFailure(
	ctx context.Context,
	runID, agentID, requestSHA256 string,
	request traineragentprotocol.FailureRequestV1,
) (traineragentprotocol.FailureResponseV1, error) {
	attempt, err := validateAttemptEnvelope(
		ctx, runID, agentID, request.AgentID, request.AttemptToken, request.Protocol,
		traineragentprotocol.FailureRequestProtocolV1,
	)
	if err != nil {
		return traineragentprotocol.FailureResponseV1{}, err
	}
	if err := verifyCanonicalRequestDigest(request, requestSHA256); err != nil {
		return traineragentprotocol.FailureResponseV1{}, err
	}
	if !remoteCodePattern.MatchString(request.Code) ||
		request.Detail == "" || strings.TrimSpace(request.Detail) != request.Detail ||
		len(request.Detail) > maximumFailureDetailBytes || !utf8.ValidString(request.Detail) || strings.ContainsRune(request.Detail, 0) {
		return traineragentprotocol.FailureResponseV1{}, errorValue(ErrorInvalidRequest, "Failure request is invalid.", false, errors.New("failure fields are invalid"))
	}
	replayed, err := service.repository.LookupTrainerAgentTerminalReceipt(
		ctx, attempt, recommendation.TrainerAgentFailureOperation, requestSHA256,
	)
	if err != nil {
		return traineragentprotocol.FailureResponseV1{}, mapRecommendationError(err)
	}
	if replayed != nil {
		return failureResponseFromReceipt(*replayed)
	}
	claim, _, err := service.repository.ResolveTrainerAgentClaim(ctx, attempt)
	if err != nil {
		return traineragentprotocol.FailureResponseV1{}, mapRecommendationError(err)
	}
	receipt, err := service.repository.ReportTrainerAgentFailure(ctx, recommendation.TrainerAgentFailureCommand{
		Claim: claim, RequestSHA256: requestSHA256, Code: request.Code, Detail: request.Detail,
		Retryable: request.Retryable, RetryDelay: service.config.RetryDelay,
	})
	if err != nil {
		return traineragentprotocol.FailureResponseV1{}, mapRecommendationError(err)
	}
	return failureResponseFromReceipt(receipt)
}

func (service *Service) rejectOutput(ctx context.Context, claim recommendation.Claim, requestSHA256 string, cause error) error {
	detail := boundedDetail(cause, maximumStoredFailureBytes, "invalid training output")
	receipt, err := service.repository.RejectTrainerAgentOutput(ctx, recommendation.TrainerAgentOutputRejectionCommand{
		Claim: claim, RequestSHA256: requestSHA256,
		FailureCode: "invalid_training_output", FailureDetail: detail,
		ErrorCode: string(ErrorOutputRejected), ErrorDetail: "Training output was rejected.",
	})
	if err != nil {
		return mapRecommendationError(err)
	}
	if receipt.Result != recommendation.TrainerAgentOutputRejected {
		return errorValue(ErrorLeaseLost, "Claim lease is no longer active.", false, errors.New("attempt already completed with a different output disposition"))
	}
	_, responseErr := outputResponseFromReceipt(receipt)
	return responseErr
}

func (service *Service) loadInputBundle(ctx context.Context, claim recommendation.Claim) ([]byte, error) {
	verified, err := service.artifacts.Verify(ctx, claim.InputArtifact.Hash, claim.InputArtifact.Size)
	if err != nil {
		return nil, mapArtifactError(err)
	}
	if verified.Hash != claim.InputArtifact.Hash || verified.Size != claim.InputArtifact.Size ||
		verified.StorageKey != claim.InputArtifact.StorageKey || verified.Path == "" {
		return nil, errorValue(ErrorStorageUnavailable, "Training input is unavailable.", false, errors.New("verified artifact metadata differs"))
	}
	return readVerifiedArtifact(ctx, verified, service.config.MaximumInputBundleBytes)
}

func (service *Service) abortUndeliveredClaim(ctx context.Context, claim recommendation.Claim, cause error) error {
	if owned, ok := cause.(*Error); ok && owned.Retryable {
		if err := service.repository.RequeueTraining(ctx, claim, service.config.RetryDelay, "input_artifact_io"); err != nil {
			return mapRecommendationError(err)
		}
		return nil
	}
	if err := service.repository.FailTraining(ctx, claim, "invalid_input_artifact", boundedDetail(cause, maximumStoredFailureBytes, "invalid training input")); err != nil {
		return mapRecommendationError(err)
	}
	return nil
}

func (service *Service) publishOutputWithLock(
	ctx context.Context,
	publication *artifact.Publication,
	command recommendation.PublishCommand,
) (_ recommendation.PublishResult, resultErr error) {
	defer func() {
		if err := publication.Release(); err != nil {
			releaseErr := mapArtifactError(err)
			if resultErr == nil {
				resultErr = releaseErr
			} else {
				resultErr = errors.Join(releaseErr, resultErr)
			}
		}
	}()
	if err := validatePublishedArtifact(publication.Artifact, command.Output.SHA256, int64(len(command.Output.CanonicalJSON))); err != nil {
		return recommendation.PublishResult{}, err
	}
	result, err := service.repository.PublishTrainingOutput(ctx, command)
	if err != nil {
		return recommendation.PublishResult{}, mapRecommendationError(err)
	}
	return result, nil
}

func validateAttemptEnvelope(
	ctx context.Context,
	runID, authenticatedAgentID, bodyAgentID, attemptToken, protocol, expectedProtocol string,
) (recommendation.TrainerAgentAttempt, error) {
	if ctx == nil || !uuidV4Pattern.MatchString(runID) || !agentIDPattern.MatchString(authenticatedAgentID) ||
		bodyAgentID != authenticatedAgentID || !uuidV4Pattern.MatchString(attemptToken) {
		return recommendation.TrainerAgentAttempt{}, errorValue(ErrorInvalidRequest, "Request attempt identity is invalid.", false, errors.New("attempt envelope is invalid"))
	}
	if protocol != expectedProtocol {
		return recommendation.TrainerAgentAttempt{}, errorValue(ErrorUnsupportedProtocol, "Trainer-agent protocol is unsupported.", false, errors.New("protocol differs"))
	}
	return recommendation.TrainerAgentAttempt{RunID: runID, AgentID: authenticatedAgentID, AttemptToken: attemptToken}, nil
}

func verifyCanonicalRequestDigest(value any, expected string) error {
	if !sha256Pattern.MatchString(expected) {
		return errorValue(ErrorInvalidRequest, "Trainer-agent request is invalid.", false, errors.New("request digest is invalid"))
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return errorValue(ErrorInvalidRequest, "Trainer-agent request is invalid.", false, err)
	}
	_, digest, err := canonicaljson.Object(raw, maximumTransportBundleSize+(1<<20))
	if err != nil || digest != expected {
		return errorValue(ErrorInvalidRequest, "Trainer-agent request is invalid.", false, errors.New("request digest differs from canonical envelope"))
	}
	return nil
}

func outputResponseFromReceipt(receipt recommendation.TrainerAgentTerminalReceipt) (traineragentprotocol.OutputResponseV1, error) {
	switch receipt.Result {
	case recommendation.TrainerAgentActivated, recommendation.TrainerAgentSuperseded:
		if receipt.ModelID == nil || receipt.RuntimeConstructionSHA256 == nil ||
			receipt.RuntimeProvenanceSHA256 == nil || receipt.RuntimeTreeSHA256 == nil ||
			receipt.HostCapabilitySHA256 == nil || receipt.RuntimeAttestationSHA256 == nil {
			break
		}
		return traineragentprotocol.OutputResponseV1{
			Protocol:    traineragentprotocol.OutputResponseProtocolV1,
			Disposition: string(receipt.Result), ModelID: *receipt.ModelID,
			RuntimeConstructionSHA256: *receipt.RuntimeConstructionSHA256,
			RuntimeProvenanceSHA256:   *receipt.RuntimeProvenanceSHA256,
			RuntimeTreeSHA256:         *receipt.RuntimeTreeSHA256,
			HostCapabilitySHA256:      *receipt.HostCapabilitySHA256,
			RuntimeAttestationSHA256:  *receipt.RuntimeAttestationSHA256,
		}, nil
	case recommendation.TrainerAgentOutputRejected:
		if receipt.ErrorCode != nil && receipt.ErrorDetail != nil && receipt.ErrorRetryable != nil {
			return traineragentprotocol.OutputResponseV1{}, errorValue(
				ErrorCode(*receipt.ErrorCode), *receipt.ErrorDetail, *receipt.ErrorRetryable, errors.New("replayed output rejection"),
			)
		}
	}
	return traineragentprotocol.OutputResponseV1{}, errorValue(ErrorServiceUnavailable, "Trainer-agent service is unavailable.", true, errors.New("terminal output receipt is invalid"))
}

func failureResponseFromReceipt(receipt recommendation.TrainerAgentTerminalReceipt) (traineragentprotocol.FailureResponseV1, error) {
	disposition := ""
	switch receipt.Result {
	case recommendation.TrainerAgentFailed:
		disposition = string(traineragentprotocol.FailureRecorded)
	case recommendation.TrainerAgentRequeued:
		disposition = string(traineragentprotocol.FailureRequeued)
	default:
		return traineragentprotocol.FailureResponseV1{}, errorValue(ErrorServiceUnavailable, "Trainer-agent service is unavailable.", true, errors.New("terminal failure receipt is invalid"))
	}
	return traineragentprotocol.FailureResponseV1{Protocol: traineragentprotocol.FailureResponseProtocolV1, Disposition: disposition}, nil
}

func readVerifiedArtifact(ctx context.Context, value artifact.Artifact, maximumBytes int) ([]byte, error) {
	if value.Size <= 0 || value.Size > int64(maximumBytes) || !sha256Pattern.MatchString(value.Hash) {
		return nil, errorValue(ErrorStorageUnavailable, "Training input is unavailable.", false, errors.New("artifact bound is invalid"))
	}
	file, err := os.Open(value.Path)
	if err != nil {
		return nil, errorValue(ErrorStorageUnavailable, "Training input is unavailable.", true, err)
	}
	defer file.Close()
	digest := sha256.New()
	buffer := bytes.NewBuffer(make([]byte, 0, value.Size))
	reader := io.TeeReader(io.LimitReader(file, value.Size+1), digest)
	if _, err := io.Copy(buffer, reader); err != nil {
		return nil, errorValue(ErrorStorageUnavailable, "Training input is unavailable.", true, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, errorValue(ErrorServiceUnavailable, "Trainer-agent service is unavailable.", true, err)
	}
	if int64(buffer.Len()) != value.Size || hex.EncodeToString(digest.Sum(nil)) != value.Hash {
		return nil, errorValue(ErrorStorageUnavailable, "Training input is unavailable.", false, errors.New("artifact bytes differ from metadata"))
	}
	return buffer.Bytes(), nil
}

func validatePublishedArtifact(value artifact.Artifact, digest string, size int64) error {
	if value.Hash != digest || value.Size != size || size <= 0 ||
		value.StorageKey != "sha256/"+digest[:2]+"/"+digest || value.Path == "" {
		return errorValue(ErrorStorageUnavailable, "Training output could not be published.", true, errors.New("artifact publication differs from output"))
	}
	return nil
}

func mapArtifactError(err error) error {
	if err == nil {
		return nil
	}
	if code, ok := artifact.CodeOf(err); ok {
		retryable := code == artifact.ErrorIO || code == artifact.ErrorCanceled
		return errorValue(ErrorStorageUnavailable, "Artifact storage is unavailable.", retryable, err)
	}
	return errorValue(ErrorStorageUnavailable, "Artifact storage is unavailable.", true, err)
}

func mapRecommendationError(err error) error {
	if err == nil {
		return nil
	}
	switch recommendation.CodeOf(err) {
	case recommendation.ErrorLeaseLost, recommendation.ErrorStateConflict:
		return errorValue(ErrorLeaseLost, "Claim lease is no longer active.", false, err)
	case recommendation.ErrorInvalidInput, recommendation.ErrorInvalidBundle:
		return errorValue(ErrorInvalidRequest, "Trainer-agent request is invalid.", false, err)
	case recommendation.ErrorCanceled, recommendation.ErrorDatabase:
		return errorValue(ErrorServiceUnavailable, "Trainer-agent service is unavailable.", true, err)
	case recommendation.ErrorInvalidArtifact:
		return errorValue(ErrorStorageUnavailable, "Artifact storage is unavailable.", true, err)
	default:
		return errorValue(ErrorServiceUnavailable, "Trainer-agent service is unavailable.", false, err)
	}
}

func boundedDetail(cause error, maximum int, fallback string) string {
	value := fallback
	if cause != nil {
		value = strings.TrimSpace(cause.Error())
		if value == "" || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
			value = fallback
		}
	}
	if len(value) > maximum {
		value = value[:maximum]
		for !utf8.ValidString(value) {
			value = value[:len(value)-1]
		}
	}
	return value
}

func randomUUIDv4() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(value)
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32], nil
}
