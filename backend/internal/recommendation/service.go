package recommendation

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/artifact"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
	"github.com/kkkzbh/AscendAny/backend/internal/canonicaljson"
)

var canonicalUUIDv4Pattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

type UUIDGenerator func() (string, error)

type ServiceConfig struct {
	MaximumInputBundleBytes int
}

type ReaderService struct {
	repository ReaderRepository
}

type AdminReaderService struct {
	repository AdminReaderRepository
}

func NewAdminReaderService(repository AdminReaderRepository) (*AdminReaderService, error) {
	if repository == nil {
		return nil, domainError(ErrorInvalidConfiguration, true, "construct recommendation admin reader service", errors.New("admin reader repository is required"))
	}
	return &AdminReaderService{repository: repository}, nil
}

func (service *AdminReaderService) ReadReviewContext(ctx context.Context, principal auth.AccessPrincipal) (ReviewContext, error) {
	if ctx == nil || !validPrincipalShape(principal, auth.RoleAdmin) {
		return ReviewContext{}, domainError(ErrorInvalidInput, true, "read recommendation review context", errors.New("admin principal and context are required"))
	}
	result, err := service.repository.ReadReviewContext(ctx, principal)
	if err != nil {
		return ReviewContext{}, err
	}
	if err := validateReviewContext(result); err != nil {
		return ReviewContext{}, domainError(ErrorStoredDataInvalid, true, "validate recommendation review context", err)
	}
	return result, nil
}

func (service *AdminReaderService) ReadTrainingRun(ctx context.Context, principal auth.AccessPrincipal, runID string) (TrainingRunDetail, bool, error) {
	if ctx == nil || !validPrincipalShape(principal, auth.RoleAdmin) || !canonicalUUIDv4Pattern.MatchString(runID) {
		return TrainingRunDetail{}, false, domainError(ErrorInvalidInput, true, "read recommendation training run", errors.New("admin principal, context, and canonical run ID are required"))
	}
	result, found, err := service.repository.ReadTrainingRun(ctx, principal, runID)
	if err != nil || !found {
		return result, found, err
	}
	if err := validateTrainingRunDetail(result, runID); err != nil {
		return TrainingRunDetail{}, false, domainError(ErrorStoredDataInvalid, true, "validate recommendation training run", err)
	}
	return result, true, nil
}

func (service *AdminReaderService) ReadTrainingEvents(ctx context.Context, principal auth.AccessPrincipal, runID string, afterSequence int64, limit int) (TrainingEventPage, bool, error) {
	if ctx == nil || !validPrincipalShape(principal, auth.RoleAdmin) || !canonicalUUIDv4Pattern.MatchString(runID) || afterSequence < 0 || limit < 1 || limit > 100 {
		return TrainingEventPage{}, false, domainError(ErrorInvalidInput, true, "read recommendation training events", errors.New("admin principal, context, run ID, cursor, and bounded limit are required"))
	}
	result, found, err := service.repository.ReadTrainingEvents(ctx, principal, runID, afterSequence, limit)
	if err != nil || !found {
		return result, found, err
	}
	for index := range result.Items {
		normalized, normalizeErr := normalizePublicTrainingEventPayload(result.Items[index].Type, result.Items[index].Payload)
		if normalizeErr != nil {
			return TrainingEventPage{}, false, domainError(ErrorStoredDataInvalid, true, "normalize recommendation training event", normalizeErr)
		}
		result.Items[index].Payload = normalized
	}
	if err := validateTrainingEventPage(result, runID, afterSequence, limit); err != nil {
		return TrainingEventPage{}, false, domainError(ErrorStoredDataInvalid, true, "validate recommendation training events", err)
	}
	return result, true, nil
}

func validateReviewContext(value ReviewContext) error {
	if value.AnalyticsGenerationID <= 0 || value.AnalyticsHeadRevision <= 0 ||
		!lowercaseSHA256Pattern.MatchString(value.InputManifestSHA256) || len(value.Problems) == 0 || len(value.Problems) > maximumTrainingProblems {
		return errors.New("review context provenance is invalid")
	}
	previous := ""
	for index, problem := range value.Problems {
		if problem.Platform != "pintia" || !canonicalSourceID(problem.ProblemID) || !canonicalText(problem.Title, 4096) ||
			!lowercaseSHA256Pattern.MatchString(problem.ProblemFactSHA256) || problem.SourceProblemKey != problem.Platform+":"+problem.ProblemID ||
			problem.ProblemKey != problem.SourceProblemKey+":"+problem.ProblemFactSHA256 || index > 0 && problem.ProblemKey <= previous ||
			len(problem.SourceProblemSets) == 0 || len(problem.SourceProblemSets) > maximumTrainingProblems {
			return errors.New("review problem identity or order is invalid")
		}
		for sourceIndex, source := range problem.SourceProblemSets {
			if !canonicalDecimalID(source.ProblemSetID) || !canonicalPintiaURL(source.SourceURL) ||
				sourceIndex > 0 && compareSourceProblemSet(problem.SourceProblemSets[sourceIndex-1], source) >= 0 {
				return errors.New("review problem source provenance is invalid")
			}
		}
		previous = problem.ProblemKey
	}
	return nil
}

func ValidateReviewContext(value ReviewContext) error { return validateReviewContext(value) }

func validateTrainingRunDetail(value TrainingRunDetail, expectedID string) error {
	run := value.Run
	if run.ID != expectedID || run.DatabaseID <= 0 || run.SourceAnalyticsGenerationID <= 0 || run.SourceAnalyticsHeadRevision <= 0 ||
		run.TrainingConfigurationVersionID <= 0 || run.KnowledgeCatalogVersionID <= 0 ||
		run.BundleProtocol != TrainingBundleProtocolV2 || !lowercaseSHA256Pattern.MatchString(run.InputManifestSHA256) ||
		!lowercaseSHA256Pattern.MatchString(run.InputArtifact.Hash) || run.InputArtifact.Size <= 0 ||
		!configurationKeyPattern.MatchString(value.TrainingConfigurationKey) || run.AttemptCount < 0 || !validRecommendationUTCTime(run.CreatedAt) ||
		run.StartedAt != nil && !validRecommendationUTCTime(*run.StartedAt) || run.FinishedAt != nil && !validRecommendationUTCTime(*run.FinishedAt) {
		return errors.New("training run detail metadata is invalid")
	}
	switch run.Status {
	case RunQueued, RunRunning, RunSucceeded, RunSuperseded, RunFailed:
	default:
		return errors.New("training run status is invalid")
	}
	if run.Status == RunFailed {
		if value.Failure == nil || !failureCodePattern.MatchString(value.Failure.Code) || value.Failure.Message == "" || len(value.Failure.Message) > 256 {
			return errors.New("failed training run lacks a safe failure")
		}
	} else if value.Failure != nil {
		return errors.New("nonfailed training run has a failure")
	}
	return nil
}

func ValidateTrainingRunDetail(value TrainingRunDetail, expectedID string) error {
	return validateTrainingRunDetail(value, expectedID)
}

func validateTrainingEventPage(value TrainingEventPage, runID string, afterSequence int64, limit int) error {
	if value.RunID != runID || value.Items == nil || len(value.Items) > limit {
		return errors.New("training event page metadata is invalid")
	}
	previous := afterSequence
	for _, event := range value.Items {
		if event.Sequence <= previous || !validRecommendationUTCTime(event.CreatedAt) || validatePublicTrainingEventPayload(event.Type, event.Payload) != nil {
			return errors.New("training event identity, order, or payload is invalid")
		}
		previous = event.Sequence
	}
	if value.NextAfterSequence != nil && (len(value.Items) == 0 || *value.NextAfterSequence != value.Items[len(value.Items)-1].Sequence) {
		return errors.New("training event cursor is invalid")
	}
	return nil
}

func ValidateTrainingEventPage(value TrainingEventPage, runID string, afterSequence int64, limit int) error {
	return validateTrainingEventPage(value, runID, afterSequence, limit)
}

func validRecommendationUTCTime(value time.Time) bool {
	if value.IsZero() {
		return false
	}
	_, offset := value.Zone()
	return offset == 0
}

func validatePublicTrainingEventPayload(eventType string, payload json.RawMessage) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil || fields == nil {
		return errors.New("event payload must be an object")
	}
	contract, exists := publicTrainingEventContract(eventType, "id_string")
	if !exists || len(fields) != len(contract) {
		return errors.New("event type or payload keys are not public")
	}
	for key, kind := range contract {
		raw, present := fields[key]
		if !present || !validPublicTrainingEventValue(raw, kind) {
			return errors.New("event payload value is invalid")
		}
	}
	return nil
}

func normalizePublicTrainingEventPayload(eventType string, payload json.RawMessage) (json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(payload, &fields); err != nil || fields == nil {
		return nil, errors.New("event payload must be an object")
	}
	contract, exists := publicTrainingEventContract(eventType, "id")
	if !exists || len(fields) != len(contract) {
		return nil, errors.New("event type or payload keys are not public")
	}
	for key, kind := range contract {
		raw, present := fields[key]
		if !present {
			return nil, errors.New("event payload key is missing")
		}
		if kind == "id" {
			var value int64
			if json.Unmarshal(raw, &value) != nil || value <= 0 {
				return nil, errors.New("event payload ID is invalid")
			}
			encoded, _ := json.Marshal(strconv.FormatInt(value, 10))
			fields[key] = encoded
			continue
		}
		if !validPublicTrainingEventValue(raw, kind) {
			return nil, errors.New("event payload value is invalid")
		}
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	canonical, _, err := canonicaljson.Object(encoded, maximumTrainingEventPayloadBytes)
	return canonical, err
}

func publicTrainingEventContract(eventType, idKind string) (map[string]string, bool) {
	contracts := map[string]map[string]string{
		"queued":          {"artifactSha256": "sha256", "configurationVersionId": idKind, "knowledgeCatalogVersionId": idKind, "sourceAnalyticsGenerationId": idKind, "sourceAnalyticsHeadRevision": "positive"},
		"claimed":         {"attemptCount": "positive", "leaseOwner": "text"},
		"reclaimed":       {"attemptCount": "positive", "leaseOwner": "text"},
		"lease_renewed":   {"attemptCount": "positive", "leaseOwner": "text"},
		"retry_scheduled": {"attemptCount": "positive", "delayMilliseconds": "positive", "reason": "code"},
		"failed":          {"attemptCount": "positive", "code": "code"},
		"activated":       {"modelId": "uuid", "outputArtifactSha256": "sha256", "sourceAnalyticsGenerationId": idKind, "sourceAnalyticsHeadRevision": "positive"},
		"superseded":      {"modelId": "uuid", "outputArtifactSha256": "sha256", "sourceAnalyticsGenerationId": idKind, "sourceAnalyticsHeadRevision": "positive"},
	}
	contract, exists := contracts[eventType]
	return contract, exists
}

func validPublicTrainingEventValue(raw json.RawMessage, kind string) bool {
	switch kind {
	case "positive":
		var value int64
		return json.Unmarshal(raw, &value) == nil && value > 0
	case "id_string":
		var value string
		if json.Unmarshal(raw, &value) != nil {
			return false
		}
		parsed, err := strconv.ParseInt(value, 10, 64)
		return err == nil && parsed > 0 && strconv.FormatInt(parsed, 10) == value
	case "text":
		var value string
		return json.Unmarshal(raw, &value) == nil && value != "" && strings.TrimSpace(value) == value && len(value) <= 128
	case "code":
		var value string
		return json.Unmarshal(raw, &value) == nil && failureCodePattern.MatchString(value)
	case "uuid":
		var value string
		return json.Unmarshal(raw, &value) == nil && canonicalUUIDv4Pattern.MatchString(value)
	case "sha256":
		var value string
		return json.Unmarshal(raw, &value) == nil && lowercaseSHA256Pattern.MatchString(value)
	default:
		return false
	}
}

func NewReaderService(repository ReaderRepository) (*ReaderService, error) {
	if repository == nil {
		return nil, domainError(ErrorInvalidConfiguration, true, "construct recommendation reader service", errors.New("reader repository is required"))
	}
	return &ReaderService{repository: repository}, nil
}

func (service *ReaderService) ReadCurrent(ctx context.Context, principal auth.AccessPrincipal) (CurrentRecommendation, error) {
	if ctx == nil || !validPrincipalShape(principal, auth.RoleStudent) {
		return CurrentRecommendation{}, domainError(ErrorInvalidInput, true, "read current recommendation", errors.New("student principal and context are required"))
	}
	return service.repository.ReadCurrent(ctx, principal)
}

type QueueService struct {
	repository   QueueRepository
	artifacts    ArtifactStore
	maximumBytes int
	uuid         UUIDGenerator
}

func NewQueueService(
	repository QueueRepository,
	artifacts ArtifactStore,
	config ServiceConfig,
) (*QueueService, error) {
	return newQueueService(repository, artifacts, config, randomUUIDv4)
}

func newQueueService(
	repository QueueRepository,
	artifacts ArtifactStore,
	config ServiceConfig,
	uuid UUIDGenerator,
) (*QueueService, error) {
	if repository == nil || artifacts == nil || uuid == nil || config.MaximumInputBundleBytes <= 0 {
		return nil, domainError(ErrorInvalidConfiguration, true, "construct recommendation queue service", errors.New("queue repository, artifact store, UUID generator, and positive input limit are required"))
	}
	return &QueueService{
		repository: repository, artifacts: artifacts,
		maximumBytes: config.MaximumInputBundleBytes, uuid: uuid,
	}, nil
}

// QueueTraining owns the artifact/row publication boundary: the content hash
// lock remains held until QueueTraining commits or rolls back its database row.
func (service *QueueService) QueueTraining(ctx context.Context, input QueueInput) (_ QueueResult, resultErr error) {
	if ctx == nil || !validPrincipalShape(input.Principal, auth.RoleAdmin) || !configurationKeyPattern.MatchString(input.ConfigurationKey) ||
		input.ExpectedAnalyticsGenerationID <= 0 || input.ExpectedAnalyticsHeadRevision <= 0 {
		return QueueResult{}, domainError(ErrorInvalidInput, true, "queue recommendation training", errors.New("admin principal, context, canonical training configuration key, and reviewed analytics head are required"))
	}
	dataset, err := service.repository.PrepareTraining(ctx, input.Principal, input.ConfigurationKey)
	if err != nil {
		return QueueResult{}, err
	}
	if dataset.Analytics.GenerationID != input.ExpectedAnalyticsGenerationID || dataset.Analytics.HeadRevision != input.ExpectedAnalyticsHeadRevision {
		return QueueResult{}, domainError(ErrorStateConflict, false, "queue recommendation training", &AnalyticsHeadConflict{
			ExpectedGenerationID: input.ExpectedAnalyticsGenerationID,
			ExpectedHeadRevision: input.ExpectedAnalyticsHeadRevision,
			CurrentGenerationID:  dataset.Analytics.GenerationID,
			CurrentHeadRevision:  dataset.Analytics.HeadRevision,
		})
	}
	bundle, err := BuildInputBundle(dataset, service.maximumBytes)
	if err != nil {
		return QueueResult{}, err
	}
	runPublicID, err := service.uuid()
	if err != nil || !canonicalUUIDv4Pattern.MatchString(runPublicID) {
		return QueueResult{}, domainError(ErrorInvalidConfiguration, false, "generate recommendation training run ID", errors.New("UUID generator failed to return a canonical UUIDv4"))
	}
	publication, err := service.artifacts.Publish(ctx, bytes.NewReader(bundle.CanonicalJSON))
	if err != nil {
		return QueueResult{}, domainError(ErrorInvalidArtifact, false, "publish recommendation training input", err)
	}
	defer func() {
		if err := publication.Release(); err != nil {
			releaseErr := domainError(ErrorInvalidArtifact, false, "release recommendation input publication", err)
			if resultErr == nil {
				resultErr = releaseErr
			} else {
				resultErr = errors.Join(resultErr, releaseErr)
			}
		}
	}()
	if err := validateArtifact(publication.Artifact, bundle.SHA256, int64(len(bundle.CanonicalJSON))); err != nil {
		return QueueResult{}, err
	}
	return service.repository.QueueTraining(ctx, QueueCommand{
		Principal:                     input.Principal,
		RunPublicID:                   runPublicID,
		ExpectedAnalyticsGenerationID: input.ExpectedAnalyticsGenerationID,
		ExpectedAnalyticsHeadRevision: input.ExpectedAnalyticsHeadRevision,
		Dataset:                       dataset,
		Bundle:                        bundle,
		Artifact:                      publication.Artifact,
		MediaType:                     TrainingBundleMediaTypeV2,
		MaximumBundleBytes:            service.maximumBytes,
	})
}

func validateArtifact(value artifact.Artifact, expectedHash string, expectedSize int64) error {
	if !lowercaseSHA256Pattern.MatchString(value.Hash) || value.Hash != expectedHash || value.Size != expectedSize || expectedSize <= 0 {
		return domainError(ErrorInvalidArtifact, true, "validate recommendation artifact", errors.New("published artifact digest or size differs from canonical bytes"))
	}
	expectedStorageKey := "sha256/" + expectedHash[:2] + "/" + expectedHash
	if value.StorageKey != expectedStorageKey || strings.TrimSpace(value.Path) == "" {
		return domainError(ErrorInvalidArtifact, true, "validate recommendation artifact", fmt.Errorf("artifact must use storage key %q and expose its verified path", expectedStorageKey))
	}
	return nil
}

func validPrincipalShape(principal auth.AccessPrincipal, role auth.Role) bool {
	return canonicalUUIDv4Pattern.MatchString(principal.AccountID) &&
		canonicalUUIDv4Pattern.MatchString(principal.SessionID) &&
		canonicalUUIDv4Pattern.MatchString(principal.JWTID) &&
		principal.Role == role && principal.AuthRevision > 0
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
