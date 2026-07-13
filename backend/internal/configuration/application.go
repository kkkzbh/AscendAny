package configuration

import (
	"context"
	"errors"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

type AccessPrincipalVerifier interface {
	VerifyAccessToken(string) (auth.AccessPrincipal, error)
}

type Configuration interface {
	List(context.Context, ListQuery) (ItemPage, error)
	Get(context.Context, ItemQuery) (Item, bool, error)
	ListVersions(context.Context, VersionsQuery) (VersionPage, bool, error)
	CreateVersion(context.Context, CreateVersionCommand) (CreateVersionResult, error)
}

type ApplicationService struct {
	verifier      AccessPrincipalVerifier
	configuration Configuration
}

func NewApplicationService(verifier AccessPrincipalVerifier, configuration Configuration) (*ApplicationService, error) {
	if verifier == nil || configuration == nil {
		return nil, configurationError(ErrorInvalidConfiguration, "construct configuration application service", errors.New("principal verifier and configuration service are required"))
	}
	return &ApplicationService{verifier: verifier, configuration: configuration}, nil
}

func (service *ApplicationService) List(ctx context.Context, token string, kind *Kind, afterKey *string, limit int) (ItemPage, error) {
	principal, err := service.verifier.VerifyAccessToken(token)
	if err != nil {
		return ItemPage{}, err
	}
	return service.configuration.List(ctx, ListQuery{Principal: principal, Kind: kind, AfterKey: afterKey, Limit: limit})
}

func (service *ApplicationService) Get(ctx context.Context, token, key string) (Item, bool, error) {
	principal, err := service.verifier.VerifyAccessToken(token)
	if err != nil {
		return Item{}, false, err
	}
	return service.configuration.Get(ctx, ItemQuery{Principal: principal, Key: key})
}

func (service *ApplicationService) ListVersions(ctx context.Context, token, key string, beforeNumber *int64, limit int) (VersionPage, bool, error) {
	principal, err := service.verifier.VerifyAccessToken(token)
	if err != nil {
		return VersionPage{}, false, err
	}
	return service.configuration.ListVersions(ctx, VersionsQuery{
		Principal: principal, Key: key, BeforeNumber: beforeNumber, Limit: limit,
	})
}

func (service *ApplicationService) CreateVersion(ctx context.Context, token string, input CreateVersionInput) (CreateVersionResult, error) {
	principal, err := service.verifier.VerifyAccessToken(token)
	if err != nil {
		return CreateVersionResult{}, err
	}
	return service.configuration.CreateVersion(ctx, CreateVersionCommand{
		Principal:                     principal,
		Key:                           input.Key,
		Kind:                          input.Kind,
		ExpectedHeadRevision:          input.ExpectedHeadRevision,
		ExpectedAnalyticsGenerationID: input.ExpectedAnalyticsGenerationID,
		ExpectedAnalyticsHeadRevision: input.ExpectedAnalyticsHeadRevision,
		ExpectedInputManifestSHA256:   input.ExpectedInputManifestSHA256,
		SchemaID:                      input.SchemaID,
		Document:                      input.Document,
		CredentialRef:                 input.CredentialRef,
	})
}
