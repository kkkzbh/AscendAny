package feedback

import (
	"context"
	"errors"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

type AccessPrincipalVerifier interface {
	VerifyAccessToken(string) (auth.AccessPrincipal, error)
}

type SubmissionService interface {
	SubmitAuthenticated(context.Context, SubmitInput) (SubmitResult, error)
}

type ApplicationInput struct {
	ClientRequestID string
	Title           string
	Content         string
	Platform        *string
	AppVersion      *string
	UserAgent       *string
}

type ApplicationService struct {
	verifier AccessPrincipalVerifier
	service  SubmissionService
}

func NewApplicationService(verifier AccessPrincipalVerifier, service SubmissionService) (*ApplicationService, error) {
	if verifier == nil || service == nil {
		return nil, feedbackError(ErrorInvalidConfiguration, true, "construct feedback application service", errors.New("principal verifier and feedback service are required"))
	}
	return &ApplicationService{verifier: verifier, service: service}, nil
}

func (application *ApplicationService) SubmitAuthenticated(
	ctx context.Context,
	accessToken string,
	input ApplicationInput,
) (SubmitResult, error) {
	principal, err := application.verifier.VerifyAccessToken(accessToken)
	if err != nil {
		return SubmitResult{}, err
	}
	return application.service.SubmitAuthenticated(ctx, SubmitInput{
		Principal:       principal,
		ClientRequestID: input.ClientRequestID,
		Title:           input.Title,
		Content:         input.Content,
		Platform:        input.Platform,
		AppVersion:      input.AppVersion,
		UserAgent:       input.UserAgent,
	})
}
