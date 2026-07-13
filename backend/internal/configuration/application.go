package configuration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	CreateCatalogPublicationAuthorization(context.Context, CreateCatalogPublicationAuthorizationCommand) (CatalogPublicationAuthorizationResult, error)
}

func (service *ApplicationService) AuthorizeKnowledgeCatalogPublication(
	ctx context.Context,
	token string,
	input CatalogPublicationAuthorizationInput,
) (CatalogPublicationAuthorizationResult, error) {
	principal, err := service.verifier.VerifyAccessToken(token)
	if err != nil {
		return CatalogPublicationAuthorizationResult{}, err
	}
	return service.configuration.CreateCatalogPublicationAuthorization(ctx, CreateCatalogPublicationAuthorizationCommand{
		Principal: principal, AccessTokenSHA256: accessTokenSHA256(token),
		PublicationIntent: input.PublicationIntent, Document: input.Document,
	})
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
	if input.Kind == KindKnowledgeCatalog || input.Key == KnowledgeCatalogKey {
		return CreateVersionResult{}, configurationError(
			ErrorInvalidQuery,
			"create public configuration version",
			errors.New("knowledge catalog publication requires the stopped-runtime operator"),
		)
	}
	principal, err := service.verifier.VerifyAccessToken(token)
	if err != nil {
		return CreateVersionResult{}, err
	}
	return service.configuration.CreateVersion(ctx, CreateVersionCommand{
		Principal: principal, Key: input.Key, Kind: input.Kind,
		ExpectedHeadRevision: input.ExpectedHeadRevision, SchemaID: input.SchemaID,
		Document: input.Document, CredentialRef: input.CredentialRef,
	})
}

// PublishKnowledgeCatalogVersion is exposed only to the stopped-runtime
// publisher. The verified token principal becomes the immutable publication
// actor inside the database transaction.
func (service *ApplicationService) PublishKnowledgeCatalogVersion(
	ctx context.Context,
	token string,
	input KnowledgeCatalogPublicationInput,
) (CreateVersionResult, error) {
	principal, err := service.verifier.VerifyAccessToken(token)
	if err != nil {
		return CreateVersionResult{}, err
	}
	analyticsGenerationID := input.ExpectedAnalyticsGenerationID
	analyticsHeadRevision := input.ExpectedAnalyticsHeadRevision
	inputManifestSHA256 := input.ExpectedInputManifestSHA256
	currentModelHeadRevision := input.ExpectedCurrentModelHeadRevision
	currentModelArtifactSHA256 := input.ExpectedCurrentModelArtifactSHA256
	return service.configuration.CreateVersion(ctx, CreateVersionCommand{
		Principal:                          principal,
		PublicationAuthorizationID:         input.PublicationAuthorizationID,
		PublicationAccessTokenSHA256:       accessTokenSHA256(token),
		PublicationAuthorizationRequest:    input.PublicationAuthorizationRequest,
		Key:                                input.Key,
		Kind:                               input.Kind,
		ExpectedHeadRevision:               input.ExpectedHeadRevision,
		ExpectedAnalyticsGenerationID:      &analyticsGenerationID,
		ExpectedAnalyticsHeadRevision:      &analyticsHeadRevision,
		ExpectedInputManifestSHA256:        &inputManifestSHA256,
		ExpectedCurrentModelHeadRevision:   &currentModelHeadRevision,
		ExpectedCurrentModelArtifactSHA256: &currentModelArtifactSHA256,
		TargetCatalogSHA256:                input.TargetCatalogSHA256,
		TargetModelID:                      input.TargetModelID,
		TargetModelArtifactSHA256:          input.TargetModelArtifactSHA256,
		TargetApplicationVersion:           input.TargetApplicationVersion,
		TargetApplicationCommit:            input.TargetApplicationCommit,
		TargetApplicationBuildTime:         input.TargetApplicationBuildTime,
		SchemaID:                           input.SchemaID,
		Document:                           input.Document,
		CredentialRef:                      input.CredentialRef,
	})
}

func accessTokenSHA256(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}
