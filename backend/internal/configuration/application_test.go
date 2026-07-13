package configuration

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

type verifierStub struct {
	principal auth.AccessPrincipal
	err       error
}

func (stub *verifierStub) VerifyAccessToken(string) (auth.AccessPrincipal, error) {
	return stub.principal, stub.err
}

type configurationStub struct {
	createCommand        CreateVersionCommand
	authorizationCommand CreateCatalogPublicationAuthorizationCommand
	authorizationResult  CatalogPublicationAuthorizationResult
}

func (*configurationStub) List(context.Context, ListQuery) (ItemPage, error) {
	return ItemPage{Items: []Item{}}, nil
}

func (*configurationStub) Get(context.Context, ItemQuery) (Item, bool, error) {
	return Item{}, false, nil
}

func (*configurationStub) ListVersions(context.Context, VersionsQuery) (VersionPage, bool, error) {
	return VersionPage{}, false, nil
}

func (stub *configurationStub) CreateVersion(_ context.Context, command CreateVersionCommand) (CreateVersionResult, error) {
	stub.createCommand = command
	return CreateVersionResult{}, nil
}

func (stub *configurationStub) CreateCatalogPublicationAuthorization(
	_ context.Context,
	command CreateCatalogPublicationAuthorizationCommand,
) (CatalogPublicationAuthorizationResult, error) {
	stub.authorizationCommand = command
	return stub.authorizationResult, nil
}

func TestApplicationServiceAuthorizesCatalogPublicationWithExactTokenProvenance(t *testing.T) {
	t.Parallel()
	principal := testAdminPrincipal()
	principal.ExpiresAt = time.Date(2026, 7, 13, 5, 0, 0, 0, time.UTC)
	stub := &configurationStub{}
	application, err := NewApplicationService(&verifierStub{principal: principal}, stub)
	if err != nil {
		t.Fatal(err)
	}
	intent := testCatalogPublicationIntent("44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a")
	_, err = application.AuthorizeKnowledgeCatalogPublication(context.Background(), "header.payload.signature", CatalogPublicationAuthorizationInput{
		PublicationIntent: intent,
		Document:          json.RawMessage(`{}`),
	})
	if err != nil || stub.authorizationCommand.Principal != principal ||
		stub.authorizationCommand.AccessTokenSHA256 != accessTokenSHA256("header.payload.signature") ||
		stub.authorizationCommand.PublicationIntent != intent ||
		string(stub.authorizationCommand.Document) != `{}` {
		t.Fatalf("command=%#v error=%v", stub.authorizationCommand, err)
	}
}

func TestApplicationServiceOwnsVerifiedPrincipal(t *testing.T) {
	principal := testAdminPrincipal()
	configuration := &configurationStub{}
	application, err := NewApplicationService(&verifierStub{principal: principal}, configuration)
	if err != nil {
		t.Fatal(err)
	}
	generationID := "7"
	analyticsHeadRevision := int64(3)
	manifestSHA256 := strings.Repeat("a", 64)
	modelHeadRevision := int64(2)
	modelArtifactSHA256 := strings.Repeat("b", 64)
	publicationRequest := AuthorizedCatalogPublicationRequest{
		AuthorizationID: "88888888-8888-4888-8888-888888888888",
		CatalogPublicationIntent: CatalogPublicationIntent{
			Schema: CatalogPublicationRequestSchema, ExpectedConfigurationHeadRevision: 3,
			ExpectedAnalyticsGenerationID: generationID, ExpectedAnalyticsHeadRevision: analyticsHeadRevision,
			ExpectedInputManifestSHA256: manifestSHA256, ExpectedCurrentModelHeadRevision: modelHeadRevision,
			ExpectedCurrentModelArtifactSHA256: modelArtifactSHA256, TargetCatalogSHA256: strings.Repeat("c", 64),
			TargetModelArtifactSHA256: strings.Repeat("d", 64), TargetApplicationVersion: "0.2.0",
			TargetApplicationCommit: strings.Repeat("e", 40), TargetApplicationBuildTime: "2026-07-13T04:00:00Z",
		},
	}
	canonicalRequest, err := CanonicalCatalogPublicationRequest(publicationRequest)
	if err != nil {
		t.Fatal(err)
	}
	_, err = application.PublishKnowledgeCatalogVersion(context.Background(), "access", KnowledgeCatalogPublicationInput{
		PublicationAuthorizationID:      publicationRequest.AuthorizationID,
		PublicationAuthorizationRequest: canonicalRequest,
		Key:                             KnowledgeCatalogKey, Kind: KindKnowledgeCatalog, ExpectedHeadRevision: 3,
		ExpectedAnalyticsGenerationID: generationID, ExpectedAnalyticsHeadRevision: analyticsHeadRevision,
		ExpectedInputManifestSHA256:        manifestSHA256,
		ExpectedCurrentModelHeadRevision:   modelHeadRevision,
		ExpectedCurrentModelArtifactSHA256: modelArtifactSHA256,
		TargetCatalogSHA256:                strings.Repeat("c", 64),
		TargetModelID:                      "11111111-1111-4111-8111-111111111111",
		TargetModelArtifactSHA256:          strings.Repeat("d", 64),
		TargetApplicationVersion:           "0.2.0",
		TargetApplicationCommit:            strings.Repeat("e", 40),
		TargetApplicationBuildTime:         "2026-07-13T04:00:00Z",
		SchemaID:                           "ascendany.knowledge_catalog.recommendation.v1", Document: json.RawMessage(`{}`),
	})
	if err != nil || configuration.createCommand.Principal != principal || configuration.createCommand.Key != KnowledgeCatalogKey ||
		configuration.createCommand.PublicationAuthorizationID != publicationRequest.AuthorizationID ||
		configuration.createCommand.PublicationAccessTokenSHA256 != accessTokenSHA256("access") ||
		string(configuration.createCommand.PublicationAuthorizationRequest) != string(canonicalRequest) ||
		configuration.createCommand.ExpectedAnalyticsGenerationID == nil || *configuration.createCommand.ExpectedAnalyticsGenerationID != generationID ||
		configuration.createCommand.ExpectedAnalyticsHeadRevision == nil || *configuration.createCommand.ExpectedAnalyticsHeadRevision != analyticsHeadRevision ||
		configuration.createCommand.ExpectedInputManifestSHA256 == nil || *configuration.createCommand.ExpectedInputManifestSHA256 != manifestSHA256 ||
		configuration.createCommand.ExpectedCurrentModelHeadRevision == nil || *configuration.createCommand.ExpectedCurrentModelHeadRevision != modelHeadRevision ||
		configuration.createCommand.ExpectedCurrentModelArtifactSHA256 == nil || *configuration.createCommand.ExpectedCurrentModelArtifactSHA256 != modelArtifactSHA256 ||
		configuration.createCommand.TargetCatalogSHA256 != strings.Repeat("c", 64) ||
		configuration.createCommand.TargetModelID != "11111111-1111-4111-8111-111111111111" ||
		configuration.createCommand.TargetModelArtifactSHA256 != strings.Repeat("d", 64) ||
		configuration.createCommand.TargetApplicationVersion != "0.2.0" ||
		configuration.createCommand.TargetApplicationCommit != strings.Repeat("e", 40) ||
		configuration.createCommand.TargetApplicationBuildTime != "2026-07-13T04:00:00Z" {
		t.Fatalf("command=%#v error=%v", configuration.createCommand, err)
	}
}

func TestApplicationServiceRejectsOnlineKnowledgeCatalogPublication(t *testing.T) {
	t.Parallel()
	verifier := &verifierStub{principal: testAdminPrincipal()}
	configuration := &configurationStub{}
	application, err := NewApplicationService(verifier, configuration)
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []CreateVersionInput{
		{Key: KnowledgeCatalogKey, Kind: KindPrompt},
		{Key: "catalog.other", Kind: KindKnowledgeCatalog},
	} {
		if _, err := application.CreateVersion(context.Background(), "access", input); CodeOf(err) != ErrorInvalidQuery {
			t.Fatalf("CreateVersion(%#v) error=%v", input, err)
		}
	}
	if configuration.createCommand.Kind != "" {
		t.Fatalf("online catalog request reached configuration service: %#v", configuration.createCommand)
	}
}

func TestApplicationServicePropagatesVerifierError(t *testing.T) {
	want := errors.New("verification failed")
	application, err := NewApplicationService(&verifierStub{err: want}, &configurationStub{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.List(context.Background(), "bad", nil, nil, 1); !errors.Is(err, want) {
		t.Fatalf("List() error=%v", err)
	}
}

func TestApplicationConstructorsRejectNil(t *testing.T) {
	if _, err := NewApplicationService(nil, &configurationStub{}); CodeOf(err) != ErrorInvalidConfiguration {
		t.Fatalf("nil verifier error=%v", err)
	}
	if _, err := NewApplicationService(&verifierStub{}, nil); CodeOf(err) != ErrorInvalidConfiguration {
		t.Fatalf("nil configuration error=%v", err)
	}
}
