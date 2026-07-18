package feedback

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sort"

	artifactstore "github.com/kkkzbh/AscendAny/backend/internal/artifact"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

type AccessPrincipalVerifier interface {
	VerifyAccessToken(string) (auth.AccessPrincipal, error)
}

type SubmissionService interface {
	SubmitAuthenticated(context.Context, SubmitInput) (SubmitResult, error)
}

type PublicationStore interface {
	Publish(context.Context, io.Reader) (*artifactstore.Publication, error)
}

type ApplicationInput struct {
	ClientRequestID string
	Title           string
	Content         string
	Platform        *string
	AppVersion      *string
	UserAgent       *string
	Images          []ImageInput
}

type ApplicationService struct {
	verifier AccessPrincipalVerifier
	service  SubmissionService
	store    PublicationStore
	writable bool
}

func NewApplicationService(verifier AccessPrincipalVerifier, service SubmissionService, store PublicationStore) (*ApplicationService, error) {
	if verifier == nil || service == nil || store == nil {
		return nil, feedbackError(ErrorInvalidConfiguration, true, "construct feedback application service", errors.New("principal verifier, feedback service, and publication store are required"))
	}
	return &ApplicationService{verifier: verifier, service: service, store: store, writable: true}, nil
}

func NewReadOnlyApplicationService(verifier AccessPrincipalVerifier, service SubmissionService) (*ApplicationService, error) {
	if verifier == nil || service == nil {
		return nil, feedbackError(ErrorInvalidConfiguration, true, "construct read-only feedback application service", errors.New("principal verifier and feedback service are required"))
	}
	return &ApplicationService{verifier: verifier, service: service}, nil
}

func (application *ApplicationService) SubmitAuthenticated(
	ctx context.Context,
	accessToken string,
	input ApplicationInput,
) (_ SubmitResult, resultErr error) {
	principal, err := application.verifier.VerifyAccessToken(accessToken)
	if err != nil {
		return SubmitResult{}, err
	}
	if !application.writable {
		return SubmitResult{}, feedbackError(ErrorWritesDisabled, true, "submit authenticated feedback", errors.New("feedback writes are disabled"))
	}
	preflight := SubmitInput{
		Principal: principal, ClientRequestID: input.ClientRequestID, Title: input.Title, Content: input.Content,
		Platform: input.Platform, AppVersion: input.AppVersion, UserAgent: input.UserAgent,
	}
	if err := validateSubmitInput(ctx, preflight); err != nil {
		return SubmitResult{}, err
	}
	decoded, err := decodeImages(input.Images)
	if err != nil {
		return SubmitResult{}, err
	}

	type uniqueImage struct {
		hash      string
		mediaType string
		content   []byte
	}
	uniqueByHash := make(map[string]uniqueImage, len(decoded))
	for _, image := range decoded {
		if existing, found := uniqueByHash[image.SHA256]; found {
			if existing.mediaType != image.MediaType || !bytes.Equal(existing.content, image.Content) {
				return SubmitResult{}, imageInvalid("one image digest is bound to conflicting media content")
			}
			continue
		}
		uniqueByHash[image.SHA256] = uniqueImage{hash: image.SHA256, mediaType: image.MediaType, content: image.Content}
	}
	unique := make([]uniqueImage, 0, len(uniqueByHash))
	for _, image := range uniqueByHash {
		unique = append(unique, image)
	}
	sort.Slice(unique, func(left, right int) bool { return unique[left].hash < unique[right].hash })

	publications := make([]*artifactstore.Publication, 0, len(unique))
	defer func() {
		for index := len(publications) - 1; index >= 0; index-- {
			if releaseErr := publications[index].Release(); releaseErr != nil {
				wrapped := feedbackError(ErrorArtifactFailure, false, "release feedback attachment publication", releaseErr)
				if resultErr == nil {
					resultErr = wrapped
				} else {
					resultErr = errors.Join(resultErr, wrapped)
				}
			}
		}
	}()
	artifactByHash := make(map[string]artifactstore.Artifact, len(unique))
	for _, image := range unique {
		publication, publishErr := application.store.Publish(ctx, bytes.NewReader(image.content))
		if publishErr != nil {
			return SubmitResult{}, mapArtifactError("publish feedback attachment", publishErr)
		}
		if publication == nil {
			return SubmitResult{}, feedbackError(ErrorArtifactFailure, false, "publish feedback attachment", errors.New("artifact store returned no publication"))
		}
		publications = append(publications, publication)
		artifact := publication.Artifact
		if artifact.Hash != image.hash || artifact.Size != int64(len(image.content)) ||
			artifact.StorageKey != "sha256/"+image.hash[:2]+"/"+image.hash || artifact.Path == "" {
			return SubmitResult{}, feedbackError(ErrorArtifactFailure, true, "validate feedback attachment publication", errors.New("artifact store returned mismatched metadata"))
		}
		artifactByHash[image.hash] = artifact
	}
	attachments := make([]Attachment, len(decoded))
	for index, image := range decoded {
		artifact := artifactByHash[image.SHA256]
		attachments[index] = Attachment{
			Sequence: image.Sequence, Filename: image.Filename, SHA256: artifact.Hash,
			SizeBytes: artifact.Size, MediaType: image.MediaType, StorageKey: artifact.StorageKey,
		}
	}
	return application.service.SubmitAuthenticated(ctx, SubmitInput{
		Principal:       principal,
		ClientRequestID: input.ClientRequestID,
		Title:           input.Title,
		Content:         input.Content,
		Platform:        input.Platform,
		AppVersion:      input.AppVersion,
		UserAgent:       input.UserAgent,
		Attachments:     attachments,
	})
}

func mapArtifactError(operation string, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return feedbackError(ErrorCanceled, false, operation, err)
	}
	if code, owned := artifactstore.CodeOf(err); owned && code == artifactstore.ErrorCanceled {
		return feedbackError(ErrorCanceled, false, operation, err)
	}
	return feedbackError(ErrorArtifactFailure, false, operation, err)
}
