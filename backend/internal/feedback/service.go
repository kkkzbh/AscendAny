package feedback

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

var (
	canonicalUUIDv4  = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	configurationKey = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,127}$`)
	lowercaseSHA256  = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

type Repository interface {
	SubmitAuthenticated(context.Context, SubmitCommand) (SubmitResult, error)
}

type UUIDGenerator func() (string, error)

type Service struct {
	repository Repository
	policy     Policy
	uuid       UUIDGenerator
}

func NewService(repository Repository, policy Policy) (*Service, error) {
	return newService(repository, policy, randomUUIDv4)
}

func newService(repository Repository, policy Policy, uuid UUIDGenerator) (*Service, error) {
	if repository == nil || uuid == nil {
		return nil, feedbackError(ErrorInvalidConfiguration, true, "construct feedback service", errors.New("repository and UUID generator are required"))
	}
	if policy.Window < time.Second || policy.MaximumSubmissions < 1 || policy.MaximumSubmissions > 1000 ||
		!configurationKey.MatchString(policy.DeliveryConfigurationKey) {
		return nil, feedbackError(ErrorInvalidConfiguration, true, "construct feedback service", errors.New("bounded rate and delivery policy is required"))
	}
	return &Service{repository: repository, policy: policy, uuid: uuid}, nil
}

func (service *Service) SubmitAuthenticated(ctx context.Context, input SubmitInput) (SubmitResult, error) {
	if err := validateSubmitInput(ctx, input); err != nil {
		return SubmitResult{}, err
	}
	feedbackID, err := service.uuid()
	if err != nil {
		return SubmitResult{}, feedbackError(ErrorInvalidConfiguration, false, "generate feedback public ID", err)
	}
	deliveryID, err := service.uuid()
	if err != nil {
		return SubmitResult{}, feedbackError(ErrorInvalidConfiguration, false, "generate feedback delivery public ID", err)
	}
	result, err := service.repository.SubmitAuthenticated(ctx, SubmitCommand{
		SubmitInput:      input,
		FeedbackPublicID: feedbackID,
		DeliveryPublicID: deliveryID,
		SubjectDigest:    sha256.Sum256([]byte("authenticated:" + input.Principal.AccountID)),
		Policy:           service.policy,
	})
	if err != nil {
		return SubmitResult{}, err
	}
	if !canonicalUUIDv4.MatchString(result.Submission.ID) || !canonicalUUIDv4.MatchString(result.Submission.DeliveryJobID) ||
		result.Submission.CreatedAt.IsZero() || !result.Submission.CreatedAt.Equal(result.Submission.CreatedAt.UTC()) {
		return SubmitResult{}, feedbackError(ErrorStoredDataInvalid, true, "validate feedback submission result", errors.New("repository returned a malformed submission"))
	}
	return result, nil
}

func validateSubmitInput(ctx context.Context, input SubmitInput) error {
	if ctx == nil || !canonicalUUIDv4.MatchString(input.Principal.AccountID) || !canonicalUUIDv4.MatchString(input.Principal.SessionID) ||
		!canonicalUUIDv4.MatchString(input.Principal.JWTID) || input.Principal.AuthRevision < 1 ||
		(input.Principal.Role != auth.RoleAdmin && input.Principal.Role != auth.RoleStudent) ||
		!canonicalUUIDv4.MatchString(input.ClientRequestID) {
		return feedbackError(ErrorInvalidInput, true, "validate authenticated feedback", errors.New("canonical active principal and client request ID are required"))
	}
	if input.Title != strings.TrimSpace(input.Title) || len(input.Title) < 1 || len(input.Title) > MaxTitleBytes ||
		input.Content != strings.TrimSpace(input.Content) || len(input.Content) < 1 || len(input.Content) > MaxContentBytes {
		return feedbackError(ErrorInvalidInput, true, "validate authenticated feedback", errors.New("title and content violate byte limits or trimming rules"))
	}
	for _, optional := range []struct {
		value *string
		limit int
	}{
		{input.Platform, MaxPlatformBytes},
		{input.AppVersion, MaxAppVersionBytes},
		{input.UserAgent, MaxUserAgentBytes},
	} {
		if optional.value != nil && (len(*optional.value) > optional.limit || strings.TrimSpace(*optional.value) != *optional.value) {
			return feedbackError(ErrorInvalidInput, true, "validate authenticated feedback", errors.New("optional metadata violates byte limits or trimming rules"))
		}
	}
	if err := validateAttachmentManifest(input.Attachments); err != nil {
		return err
	}
	return nil
}

func validateAttachmentManifest(attachments []Attachment) error {
	if len(attachments) > MaxImages {
		return feedbackError(ErrorTooManyImages, true, "validate feedback attachments", errors.New("feedback attachment count exceeds eight"))
	}
	for index, attachment := range attachments {
		expectedSequence := int16(index + 1)
		if attachment.Sequence != expectedSequence || strings.TrimSpace(attachment.Filename) != attachment.Filename ||
			len(attachment.Filename) < 1 || len(attachment.Filename) > MaxAttachmentFilenameBytes ||
			!lowercaseSHA256.MatchString(attachment.SHA256) || attachment.SizeBytes < 1 || attachment.SizeBytes > MaxImageBytes ||
			attachment.MediaType != strings.ToLower(attachment.MediaType) ||
			!imageDataURLMediaType(attachment.MediaType) ||
			attachment.StorageKey != "sha256/"+attachment.SHA256[:2]+"/"+attachment.SHA256 {
			return feedbackError(ErrorImageInvalid, true, "validate feedback attachments", errors.New("feedback attachment manifest is malformed"))
		}
	}
	return nil
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
