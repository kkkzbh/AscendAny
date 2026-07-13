package configuration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

type repositoryStub struct {
	itemsResult          ItemPage
	itemResult           Item
	itemFound            bool
	versionsResult       VersionPage
	versionsFound        bool
	createResult         CreateVersionResult
	createRequest        CreateVersionCommand
	createDigest         string
	authorizationRecord  CatalogPublicationAuthorizationRecord
	authorizationCommand CreateCatalogPublicationAuthorizationCommand
	authorizationDigest  string
	err                  error
}

type acceptingRecommendationDocumentValidator struct{}

func (acceptingRecommendationDocumentValidator) ValidateRecommendationDocument(Kind, string, json.RawMessage) error {
	return nil
}

func (acceptingRecommendationDocumentValidator) ValidateVersionWrite(context.Context, VersionWriteTransaction, CreateVersionCommand) error {
	return nil
}

type rejectingRecommendationDocumentValidator struct{}

func (rejectingRecommendationDocumentValidator) ValidateRecommendationDocument(Kind, string, json.RawMessage) error {
	return errors.New("semantic document rejected")
}

func TestCreateVersionRejectsInvalidRecommendationDocumentBeforeStore(t *testing.T) {
	stub := &repositoryStub{}
	service, err := NewService(stub, rejectingRecommendationDocumentValidator{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.CreateVersion(context.Background(), authorizedCatalogCommand(t, CreateVersionCommand{
		Principal: testAdminPrincipal(), Key: KnowledgeCatalogKey, Kind: KindKnowledgeCatalog,
		ExpectedAnalyticsGenerationID: stringPointer("1"), ExpectedAnalyticsHeadRevision: int64Pointer(1),
		ExpectedInputManifestSHA256:        stringPointer(strings.Repeat("a", 64)),
		ExpectedCurrentModelHeadRevision:   int64Pointer(1),
		ExpectedCurrentModelArtifactSHA256: stringPointer(strings.Repeat("b", 64)),
		TargetCatalogSHA256:                "44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a",
		TargetModelID:                      "11111111-1111-4111-8111-111111111111",
		TargetModelArtifactSHA256:          strings.Repeat("c", 64),
		TargetApplicationVersion:           "0.2.0",
		TargetApplicationCommit:            strings.Repeat("d", 40),
		TargetApplicationBuildTime:         "2026-07-13T04:00:00Z",
		SchemaID:                           "ascendany.knowledge_catalog.recommendation.v1", Document: json.RawMessage(`{}`),
	}))
	if CodeOf(err) != ErrorDocumentInvalid || stub.createDigest != "" {
		t.Fatalf("error=%v code=%q storedDigest=%q", err, CodeOf(err), stub.createDigest)
	}
}

func TestCreateVersionAcceptsKnowledgeCatalogWithCompleteProvenance(t *testing.T) {
	principal := testAdminPrincipal()
	createdAt := time.Date(2026, 7, 13, 4, 0, 0, 0, time.UTC)
	canonical := json.RawMessage(`{}`)
	const catalogSHA256 = "44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a"
	active := &Version{
		ID: "1", Number: 1, SchemaID: "ascendany.knowledge_catalog.recommendation.v1",
		Document: canonical, DocumentSHA256: catalogSHA256,
		CreatedByAccountID: principal.AccountID, CreatedBySessionID: principal.SessionID, CreatedAt: createdAt,
	}
	stub := &repositoryStub{createResult: CreateVersionResult{
		Item: Item{
			ID: "33333333-3333-4333-8333-333333333333", Key: KnowledgeCatalogKey,
			Kind: KindKnowledgeCatalog, HeadRevision: 1, ActiveVersion: active,
			CreatedAt: createdAt, UpdatedAt: createdAt,
		},
		KnowledgeCatalogPublication: &KnowledgeCatalogPublication{KnowledgeCatalogPublicationID: "1"},
	}}
	service, err := NewService(stub, acceptingRecommendationDocumentValidator{})
	if err != nil {
		t.Fatal(err)
	}
	manifestSHA256 := strings.Repeat("a", 64)
	currentModelSHA256 := strings.Repeat("b", 64)
	result, err := service.CreateVersion(context.Background(), authorizedCatalogCommand(t, CreateVersionCommand{
		Principal: principal, Key: KnowledgeCatalogKey, Kind: KindKnowledgeCatalog,
		ExpectedAnalyticsGenerationID:      stringPointer("17"),
		ExpectedAnalyticsHeadRevision:      int64Pointer(9),
		ExpectedInputManifestSHA256:        &manifestSHA256,
		ExpectedCurrentModelHeadRevision:   int64Pointer(2),
		ExpectedCurrentModelArtifactSHA256: &currentModelSHA256,
		TargetCatalogSHA256:                catalogSHA256,
		TargetModelID:                      "11111111-1111-4111-8111-111111111111",
		TargetModelArtifactSHA256:          strings.Repeat("c", 64),
		TargetApplicationVersion:           "0.2.0",
		TargetApplicationCommit:            strings.Repeat("d", 40),
		TargetApplicationBuildTime:         "2026-07-13T04:00:00Z",
		SchemaID:                           "ascendany.knowledge_catalog.recommendation.v1",
		Document:                           canonical,
	}))
	if err != nil || result.Item.ActiveVersion == nil {
		t.Fatalf("CreateVersion() result=%#v error=%v", result, err)
	}
	if stub.createDigest != catalogSHA256 || stub.createRequest.TargetCatalogSHA256 != catalogSHA256 ||
		stub.createRequest.ExpectedAnalyticsGenerationID == nil || *stub.createRequest.ExpectedAnalyticsGenerationID != "17" ||
		stub.createRequest.ExpectedCurrentModelArtifactSHA256 == nil || *stub.createRequest.ExpectedCurrentModelArtifactSHA256 != currentModelSHA256 {
		t.Fatalf("stored command=%#v digest=%q", stub.createRequest, stub.createDigest)
	}
}

func (stub *repositoryStub) LoadItems(context.Context, ListQuery) (ItemPage, error) {
	return stub.itemsResult, stub.err
}

func (stub *repositoryStub) LoadItem(context.Context, ItemQuery) (Item, bool, error) {
	return stub.itemResult, stub.itemFound, stub.err
}

func (stub *repositoryStub) LoadVersions(context.Context, VersionsQuery) (VersionPage, bool, error) {
	return stub.versionsResult, stub.versionsFound, stub.err
}

func (stub *repositoryStub) StoreVersion(_ context.Context, request CreateVersionCommand, digest string) (CreateVersionResult, error) {
	stub.createRequest = request
	stub.createDigest = digest
	return stub.createResult, stub.err
}

func (stub *repositoryStub) CreateCatalogPublicationAuthorization(
	_ context.Context,
	command CreateCatalogPublicationAuthorizationCommand,
	digest string,
) (CatalogPublicationAuthorizationRecord, error) {
	stub.authorizationCommand = command
	stub.authorizationDigest = digest
	return stub.authorizationRecord, stub.err
}

func TestCreateCatalogPublicationAuthorizationOwnsCanonicalIntentAndDocument(t *testing.T) {
	t.Parallel()
	principal := testAdminPrincipal()
	principal.ExpiresAt = time.Date(2026, 7, 13, 5, 0, 0, 0, time.UTC)
	intent := testCatalogPublicationIntent("43258cff783fe7036d8a43033f830adfc60ec037382473548ac742b888292777")
	request := AuthorizedCatalogPublicationRequest{
		AuthorizationID:          "33333333-3333-4333-8333-333333333333",
		CatalogPublicationIntent: intent,
	}
	stub := &repositoryStub{authorizationRecord: CatalogPublicationAuthorizationRecord{
		AuthorizationID:    request.AuthorizationID,
		ExpiresAt:          principal.ExpiresAt,
		PublicationRequest: request,
	}}
	service, err := NewService(stub, acceptingRecommendationDocumentValidator{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.CreateCatalogPublicationAuthorization(context.Background(), CreateCatalogPublicationAuthorizationCommand{
		Principal:         principal,
		AccessTokenSHA256: strings.Repeat("f", 64),
		PublicationIntent: intent,
		Document:          json.RawMessage(` {"b":2,"a":1} `),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.AuthorizationID != request.AuthorizationID || !result.ExpiresAt.Equal(principal.ExpiresAt) ||
		result.PublicationRequest != request || stub.authorizationDigest != intent.TargetCatalogSHA256 ||
		string(stub.authorizationCommand.Document) != `{"a":1,"b":2}` ||
		stub.authorizationCommand.AccessTokenSHA256 != strings.Repeat("f", 64) {
		t.Fatalf("result=%#v command=%#v digest=%q", result, stub.authorizationCommand, stub.authorizationDigest)
	}
}

func TestCanonicalDocumentNormalizesOrderingAndNumbers(t *testing.T) {
	canonical, digest, err := canonicalDocument(json.RawMessage(` {"z":1e2,"a":[1.00,-0,0.0100]} `))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"a":[1,0,0.01],"z":100}`
	if string(canonical) != want {
		t.Fatalf("canonical = %s", canonical)
	}
	wantDigest := sha256.Sum256([]byte(want))
	if digest != hex.EncodeToString(wantDigest[:]) {
		t.Fatalf("digest = %s", digest)
	}
}

func TestCanonicalDocumentRejectsDuplicateKeysAndNonObject(t *testing.T) {
	for _, raw := range []string{`{"a":1,"a":2}`, `[1]`, `{"a":NaN}`} {
		if _, _, err := canonicalDocument(json.RawMessage(raw)); err == nil {
			t.Fatalf("canonicalDocument(%s) succeeded", raw)
		}
	}
}

func TestCreateVersionCanonicalizesAndRejectsCredentialContent(t *testing.T) {
	principal := testAdminPrincipal()
	createdAt := time.Date(2026, 7, 11, 6, 0, 0, 0, time.UTC)
	canonical := json.RawMessage(`{"baseUrl":"https://models.example/v1","model":"m1"}`)
	_, digest, err := canonicalDocument(canonical)
	if err != nil {
		t.Fatal(err)
	}
	credentialRef := "models.primary"
	version := Version{
		ID:                 "1",
		Number:             1,
		SchemaID:           "ascendany.model_connection.v1",
		Document:           canonical,
		DocumentSHA256:     digest,
		CredentialRef:      &credentialRef,
		CreatedByAccountID: principal.AccountID,
		CreatedBySessionID: principal.SessionID,
		CreatedAt:          createdAt,
	}
	stub := &repositoryStub{createResult: CreateVersionResult{Item: Item{
		ID: "33333333-3333-4333-8333-333333333333", Key: "models.primary", Kind: KindModelConnection,
		HeadRevision: 1, ActiveVersion: &version, CreatedAt: createdAt, UpdatedAt: createdAt,
	}}}
	service, err := NewService(stub, acceptingRecommendationDocumentValidator{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.CreateVersion(context.Background(), CreateVersionCommand{
		Principal: principal, Key: "models.primary", Kind: KindModelConnection, SchemaID: "ascendany.model_connection.v1",
		Document: json.RawMessage(`{"model":"m1","baseUrl":"https://models.example/v1"}`), CredentialRef: &credentialRef,
	})
	if err != nil || result.Item.ActiveVersion == nil {
		t.Fatalf("CreateVersion() result=%#v error=%v", result, err)
	}
	if string(stub.createRequest.Document) != string(canonical) || stub.createDigest != digest {
		t.Fatalf("prepared request=%#v", stub.createRequest)
	}

	_, err = service.CreateVersion(context.Background(), CreateVersionCommand{
		Principal: principal, Key: "models.primary", Kind: KindModelConnection, SchemaID: "ascendany.model_connection.v1",
		Document: json.RawMessage(`{"model":"m1","apiKey":"secret"}`), CredentialRef: &credentialRef,
	})
	if CodeOf(err) != ErrorInvalidQuery {
		t.Fatalf("credential document error=%v", err)
	}
}

func TestServiceRejectsStudentAndInvalidMetadata(t *testing.T) {
	service, err := NewService(&repositoryStub{}, acceptingRecommendationDocumentValidator{})
	if err != nil {
		t.Fatal(err)
	}
	student := testAdminPrincipal()
	student.Role = auth.RoleStudent
	if _, err := service.List(context.Background(), ListQuery{Principal: student, Limit: 1}); CodeOf(err) != ErrorPrincipalRejected {
		t.Fatalf("student list error=%v", err)
	}
	admin := testAdminPrincipal()
	commands := []CreateVersionCommand{
		{Principal: admin, Key: "Bad", Kind: KindPrompt, SchemaID: "ascendany.prompt.v1", Document: json.RawMessage(`{}`)},
		{Principal: admin, Key: "prompt.main", Kind: KindPrompt, SchemaID: "ascendany.training.v1", Document: json.RawMessage(`{}`)},
		{Principal: admin, Key: "prompt.main", Kind: KindPrompt, SchemaID: "ascendany.prompt.v1", Document: json.RawMessage(`{}`), CredentialRef: stringPointer("secret.name")},
		{Principal: admin, Key: "recommendation.catalog.second", Kind: KindKnowledgeCatalog, SchemaID: "ascendany.knowledge_catalog.recommendation.v1", Document: json.RawMessage(`{}`)},
		{Principal: admin, Key: KnowledgeCatalogKey, Kind: KindPrompt, SchemaID: "ascendany.prompt.v1", Document: json.RawMessage(`{}`)},
	}
	for _, command := range commands {
		if _, err := service.CreateVersion(context.Background(), command); CodeOf(err) != ErrorInvalidQuery {
			t.Fatalf("invalid command=%#v error=%v", command, err)
		}
	}
}

func TestCreateVersionRequiresPublicationProvenanceOnlyForKnowledgeCatalog(t *testing.T) {
	stub := &repositoryStub{}
	service, err := NewService(stub, acceptingRecommendationDocumentValidator{})
	if err != nil {
		t.Fatal(err)
	}
	manifestSHA256 := strings.Repeat("a", 64)
	currentModelSHA256 := strings.Repeat("b", 64)
	validCatalog := authorizedCatalogCommand(t, CreateVersionCommand{
		Principal: testAdminPrincipal(), Key: KnowledgeCatalogKey, Kind: KindKnowledgeCatalog,
		ExpectedAnalyticsGenerationID:      stringPointer("9223372036854775807"),
		ExpectedAnalyticsHeadRevision:      int64Pointer(1),
		ExpectedInputManifestSHA256:        &manifestSHA256,
		ExpectedCurrentModelHeadRevision:   int64Pointer(1),
		ExpectedCurrentModelArtifactSHA256: &currentModelSHA256,
		TargetCatalogSHA256:                "44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a",
		TargetModelID:                      "11111111-1111-4111-8111-111111111111",
		TargetModelArtifactSHA256:          strings.Repeat("c", 64),
		TargetApplicationVersion:           "0.2.0",
		TargetApplicationCommit:            strings.Repeat("d", 40),
		TargetApplicationBuildTime:         "2026-07-13T04:00:00Z",
		SchemaID:                           "ascendany.knowledge_catalog.recommendation.v1",
		Document:                           json.RawMessage(`{}`),
	})
	mutations := map[string]func(*CreateVersionCommand){
		"generation absent": func(value *CreateVersionCommand) { value.ExpectedAnalyticsGenerationID = nil },
		"generation noncanonical": func(value *CreateVersionCommand) {
			value.ExpectedAnalyticsGenerationID = stringPointer("01")
		},
		"generation overflow": func(value *CreateVersionCommand) {
			value.ExpectedAnalyticsGenerationID = stringPointer("9223372036854775808")
		},
		"analytics revision absent": func(value *CreateVersionCommand) { value.ExpectedAnalyticsHeadRevision = nil },
		"analytics revision invalid": func(value *CreateVersionCommand) {
			value.ExpectedAnalyticsHeadRevision = int64Pointer(0)
		},
		"manifest absent": func(value *CreateVersionCommand) { value.ExpectedInputManifestSHA256 = nil },
		"manifest invalid": func(value *CreateVersionCommand) {
			value.ExpectedInputManifestSHA256 = stringPointer("A" + manifestSHA256[1:])
		},
		"model revision absent": func(value *CreateVersionCommand) { value.ExpectedCurrentModelHeadRevision = nil },
		"model revision invalid": func(value *CreateVersionCommand) {
			value.ExpectedCurrentModelHeadRevision = int64Pointer(0)
		},
		"current model digest absent": func(value *CreateVersionCommand) {
			value.ExpectedCurrentModelArtifactSHA256 = nil
		},
		"current model digest invalid": func(value *CreateVersionCommand) {
			value.ExpectedCurrentModelArtifactSHA256 = stringPointer("B" + currentModelSHA256[1:])
		},
		"catalog digest invalid": func(value *CreateVersionCommand) { value.TargetCatalogSHA256 = strings.Repeat("A", 64) },
		"model identity invalid": func(value *CreateVersionCommand) { value.TargetModelID = "invalid" },
		"target model digest invalid": func(value *CreateVersionCommand) {
			value.TargetModelArtifactSHA256 = strings.Repeat("C", 64)
		},
		"application identity invalid": func(value *CreateVersionCommand) { value.TargetApplicationVersion = "01.2.3" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			command := validCatalog
			mutate(&command)
			if _, err := service.CreateVersion(context.Background(), command); CodeOf(err) != ErrorInvalidQuery {
				t.Fatalf("CreateVersion() error=%v command=%#v", err, command)
			}
		})
	}

	generic := CreateVersionCommand{
		Principal: testAdminPrincipal(), Key: "prompt.main", Kind: KindPrompt,
		ExpectedAnalyticsGenerationID:      validCatalog.ExpectedAnalyticsGenerationID,
		ExpectedAnalyticsHeadRevision:      validCatalog.ExpectedAnalyticsHeadRevision,
		ExpectedInputManifestSHA256:        validCatalog.ExpectedInputManifestSHA256,
		ExpectedCurrentModelHeadRevision:   validCatalog.ExpectedCurrentModelHeadRevision,
		ExpectedCurrentModelArtifactSHA256: validCatalog.ExpectedCurrentModelArtifactSHA256,
		TargetCatalogSHA256:                validCatalog.TargetCatalogSHA256,
		TargetModelID:                      validCatalog.TargetModelID,
		TargetModelArtifactSHA256:          validCatalog.TargetModelArtifactSHA256,
		TargetApplicationVersion:           validCatalog.TargetApplicationVersion,
		TargetApplicationCommit:            validCatalog.TargetApplicationCommit,
		TargetApplicationBuildTime:         validCatalog.TargetApplicationBuildTime,
		SchemaID:                           "ascendany.prompt.v1", Document: json.RawMessage(`{}`),
	}
	if _, err := service.CreateVersion(context.Background(), generic); CodeOf(err) != ErrorInvalidQuery {
		t.Fatalf("generic publication provenance error=%v", err)
	}
	if stub.createDigest != "" {
		t.Fatalf("rejected write reached repository with digest %q", stub.createDigest)
	}
}

func TestKnowledgeCatalogKindOwnsItsSchemaNamespace(t *testing.T) {
	t.Parallel()
	if !ValidKind(KindKnowledgeCatalog) ||
		KnowledgeCatalogKey != "recommendation.catalog.active" ||
		!validKnowledgeCatalogIdentity(KnowledgeCatalogKey, KindKnowledgeCatalog) ||
		validKnowledgeCatalogIdentity(KnowledgeCatalogKey, KindPrompt) ||
		validKnowledgeCatalogIdentity("recommendation.catalog.second", KindKnowledgeCatalog) ||
		!validSchemaForKind("ascendany.knowledge_catalog.recommendation.v1", KindKnowledgeCatalog) ||
		validSchemaForKind("ascendany.training.recommendation.v2", KindKnowledgeCatalog) {
		t.Fatal("knowledge_catalog kind or schema ownership is invalid")
	}
}

func TestStoredConfigurationRequiresBidirectionalCatalogIdentity(t *testing.T) {
	t.Parallel()
	createdAt := time.Date(2026, 7, 11, 6, 0, 0, 0, time.UTC)
	for _, item := range []Item{
		{ID: "11111111-1111-4111-8111-111111111111", Key: KnowledgeCatalogKey, Kind: KindPrompt, CreatedAt: createdAt, UpdatedAt: createdAt},
		{ID: "22222222-2222-4222-8222-222222222222", Key: "recommendation.catalog.second", Kind: KindKnowledgeCatalog, CreatedAt: createdAt, UpdatedAt: createdAt},
	} {
		if err := validateItem(item); err == nil {
			t.Fatalf("stored item %#v passed catalog identity validation", item)
		}
	}
	if err := validateVersionPage(VersionPage{
		Key: KnowledgeCatalogKey, Kind: KindPrompt, HeadRevision: 1, Items: []Version{},
	}, VersionsQuery{Key: KnowledgeCatalogKey, Limit: 1}); err == nil {
		t.Fatal("stored version page passed catalog identity validation")
	}
}

func TestServiceValidatesVersionPageAndStoredHash(t *testing.T) {
	principal := testAdminPrincipal()
	createdAt := time.Date(2026, 7, 11, 6, 0, 0, 0, time.UTC)
	stub := &repositoryStub{versionsFound: true, versionsResult: VersionPage{
		Key: "prompt.main", Kind: KindPrompt, HeadRevision: 1,
		Items: []Version{{
			ID: "1", Number: 1, SchemaID: "ascendany.prompt.v1", Document: json.RawMessage(`{"text":"hello"}`),
			DocumentSHA256:     "0000000000000000000000000000000000000000000000000000000000000000",
			CreatedByAccountID: principal.AccountID, CreatedBySessionID: principal.SessionID, CreatedAt: createdAt,
		}},
	}}
	service, err := NewService(stub, acceptingRecommendationDocumentValidator{})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.ListVersions(context.Background(), VersionsQuery{Principal: principal, Key: "prompt.main", Limit: 10}); CodeOf(err) != ErrorStoredDataInvalid {
		t.Fatalf("stored hash error=%v", err)
	}
}

func TestConstructorsRejectNilDependencies(t *testing.T) {
	if _, err := NewService(nil, nil); CodeOf(err) != ErrorInvalidConfiguration {
		t.Fatalf("NewService(nil) error=%v", err)
	}
	if _, err := NewPostgresRepository(nil, nil); CodeOf(err) != ErrorInvalidConfiguration {
		t.Fatalf("NewPostgresRepository(nil) error=%v", err)
	}
	if _, err := newPostgresRepository(nil, nil); CodeOf(err) != ErrorInvalidConfiguration {
		t.Fatalf("newPostgresRepository(nil) error=%v", err)
	}
}

func testAdminPrincipal() auth.AccessPrincipal {
	return auth.AccessPrincipal{
		AccountID: "11111111-1111-4111-8111-111111111111",
		SessionID: "22222222-2222-4222-8222-222222222222",
		JWTID:     "99999999-9999-4999-8999-999999999999",
		Role:      auth.RoleAdmin, AuthRevision: 1,
	}
}

func testCatalogPublicationIntent(catalogSHA256 string) CatalogPublicationIntent {
	return CatalogPublicationIntent{
		Schema:                             CatalogPublicationRequestSchema,
		ExpectedConfigurationHeadRevision:  3,
		ExpectedAnalyticsGenerationID:      "17",
		ExpectedAnalyticsHeadRevision:      9,
		ExpectedInputManifestSHA256:        strings.Repeat("a", 64),
		ExpectedCurrentModelHeadRevision:   2,
		ExpectedCurrentModelArtifactSHA256: strings.Repeat("b", 64),
		TargetCatalogSHA256:                catalogSHA256,
		TargetModelArtifactSHA256:          strings.Repeat("c", 64),
		TargetApplicationVersion:           "0.2.0",
		TargetApplicationCommit:            strings.Repeat("d", 40),
		TargetApplicationBuildTime:         "2026-07-13T04:00:00Z",
	}
}

func authorizedCatalogCommand(t *testing.T, command CreateVersionCommand) CreateVersionCommand {
	t.Helper()
	request := AuthorizedCatalogPublicationRequest{
		AuthorizationID: "88888888-8888-4888-8888-888888888888",
		CatalogPublicationIntent: CatalogPublicationIntent{
			Schema:                             CatalogPublicationRequestSchema,
			ExpectedConfigurationHeadRevision:  command.ExpectedHeadRevision,
			ExpectedAnalyticsGenerationID:      *command.ExpectedAnalyticsGenerationID,
			ExpectedAnalyticsHeadRevision:      *command.ExpectedAnalyticsHeadRevision,
			ExpectedInputManifestSHA256:        *command.ExpectedInputManifestSHA256,
			ExpectedCurrentModelHeadRevision:   *command.ExpectedCurrentModelHeadRevision,
			ExpectedCurrentModelArtifactSHA256: *command.ExpectedCurrentModelArtifactSHA256,
			TargetCatalogSHA256:                command.TargetCatalogSHA256,
			TargetModelArtifactSHA256:          command.TargetModelArtifactSHA256,
			TargetApplicationVersion:           command.TargetApplicationVersion,
			TargetApplicationCommit:            command.TargetApplicationCommit,
			TargetApplicationBuildTime:         command.TargetApplicationBuildTime,
		},
	}
	canonical, err := CanonicalCatalogPublicationRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	command.PublicationAuthorizationID = request.AuthorizationID
	command.PublicationAccessTokenSHA256 = strings.Repeat("e", 64)
	command.PublicationAuthorizationRequest = canonical
	return command
}

func stringPointer(value string) *string { return &value }
func int64Pointer(value int64) *int64    { return &value }
