package configuration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

type repositoryStub struct {
	itemsResult    ItemPage
	itemResult     Item
	itemFound      bool
	versionsResult VersionPage
	versionsFound  bool
	createResult   CreateVersionResult
	createRequest  CreateVersionCommand
	createDigest   string
	err            error
}

type acceptingRecommendationDocumentValidator struct{}

func (acceptingRecommendationDocumentValidator) ValidateRecommendationDocument(Kind, string, json.RawMessage) error {
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
	_, err = service.CreateVersion(context.Background(), CreateVersionCommand{
		Principal: testAdminPrincipal(), Key: "recommendation.training.default", Kind: KindTraining,
		SchemaID: "ascendany.training.recommendation.v2", Document: json.RawMessage(`{}`),
	})
	if CodeOf(err) != ErrorDocumentInvalid || stub.createDigest != "" {
		t.Fatalf("error=%v code=%q storedDigest=%q", err, CodeOf(err), stub.createDigest)
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
	}
	for _, command := range commands {
		if _, err := service.CreateVersion(context.Background(), command); CodeOf(err) != ErrorInvalidQuery {
			t.Fatalf("invalid command=%#v error=%v", command, err)
		}
	}
}

func TestKnowledgeCatalogKindOwnsItsSchemaNamespace(t *testing.T) {
	t.Parallel()
	if !ValidKind(KindKnowledgeCatalog) ||
		!validSchemaForKind("ascendany.knowledge_catalog.recommendation.v1", KindKnowledgeCatalog) ||
		validSchemaForKind("ascendany.training.recommendation.v2", KindKnowledgeCatalog) {
		t.Fatal("knowledge_catalog kind or schema ownership is invalid")
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
	if _, err := NewPostgresRepository(nil); CodeOf(err) != ErrorInvalidConfiguration {
		t.Fatalf("NewPostgresRepository(nil) error=%v", err)
	}
	if _, err := newPostgresRepository(nil); CodeOf(err) != ErrorInvalidConfiguration {
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

func stringPointer(value string) *string { return &value }
