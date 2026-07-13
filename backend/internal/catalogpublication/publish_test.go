package catalogpublication

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/catalogartifact"
	"github.com/kkkzbh/AscendAny/backend/internal/configuration"
	"github.com/kkkzbh/AscendAny/backend/internal/modelartifact"
	"github.com/kkkzbh/AscendAny/backend/internal/modelrelease"
	"github.com/kkkzbh/AscendAny/backend/internal/recommendation"
)

type publisherStub struct {
	input  configuration.KnowledgeCatalogPublicationInput
	token  string
	result configuration.CreateVersionResult
}

func (stub *publisherStub) PublishKnowledgeCatalogVersion(_ context.Context, token string, input configuration.KnowledgeCatalogPublicationInput) (configuration.CreateVersionResult, error) {
	stub.token = token
	stub.input = input
	return stub.result, nil
}

func TestPublishUsesVerifiedPrincipalAndBuildsCanonicalReceipt(t *testing.T) {
	t.Parallel()
	catalog, model := publicationArtifacts(t)
	application := publicationApplication()
	request := publicationRequest(catalog, model, application)
	createdAt := time.Date(2026, 7, 13, 4, 34, 56, 123000000, time.UTC)
	stub := &publisherStub{result: publicationResult(catalog, model, application, createdAt)}

	const accessToken = "header.payload.signature"
	receipt, err := Publish(context.Background(), stub, accessToken, request, catalog, model, application)
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if stub.token != accessToken || stub.input.Key != configuration.KnowledgeCatalogKey ||
		stub.input.PublicationAuthorizationID != request.AuthorizationID ||
		stub.input.Kind != configuration.KindKnowledgeCatalog || stub.input.ExpectedHeadRevision != 3 ||
		stub.input.ExpectedAnalyticsGenerationID != "17" || stub.input.ExpectedAnalyticsHeadRevision != 9 ||
		stub.input.ExpectedInputManifestSHA256 != strings.Repeat("a", 64) ||
		stub.input.ExpectedCurrentModelHeadRevision != 2 ||
		stub.input.ExpectedCurrentModelArtifactSHA256 != strings.Repeat("b", 64) ||
		stub.input.TargetCatalogSHA256 != catalog.SHA256 || stub.input.TargetModelID != model.Model.Manifest().ModelID ||
		stub.input.TargetModelArtifactSHA256 != model.SHA256 || stub.input.SchemaID != recommendation.KnowledgeCatalogSchemaV1 ||
		!bytes.Equal(stub.input.Document, catalog.Artifact.Document()) {
		t.Fatalf("publication input = %#v", stub.input)
	}
	if receipt.Schema != ReceiptSchema || receipt.AuthorizationID != request.AuthorizationID ||
		receipt.KnowledgeCatalogPublicationID != "7" ||
		receipt.TargetModelReleaseID != "23" || receipt.ConfigurationHeadRevision != 4 ||
		receipt.PublishedAt != "2026-07-13T04:34:56.123Z" {
		t.Fatalf("receipt = %#v", receipt)
	}
	canonical, err := CanonicalReceipt(receipt)
	if err != nil {
		t.Fatalf("CanonicalReceipt() error = %v", err)
	}
	if bytes.Contains(canonical, []byte(accessToken)) {
		t.Fatalf("receipt contains access token: %s", canonical)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(canonical, &fields); err != nil {
		t.Fatal(err)
	}
	if len(fields) != 26 {
		t.Fatalf("receipt field count = %d, bytes = %s", len(fields), canonical)
	}
	parsed, err := ParseReceipt(canonical)
	if err != nil || parsed != receipt {
		t.Fatalf("ParseReceipt() = %#v, %v", parsed, err)
	}
}

func TestPublishRejectsRequestForAnotherRelease(t *testing.T) {
	t.Parallel()
	catalog, model := publicationArtifacts(t)
	application := publicationApplication()
	request := publicationRequest(catalog, model, application)
	request.TargetCatalogSHA256 = strings.Repeat("f", 64)
	stub := &publisherStub{result: publicationResult(catalog, model, application, time.Now().UTC())}
	if _, err := Publish(context.Background(), stub, "a.b.c", request, catalog, model, application); err == nil {
		t.Fatal("Publish() error = nil")
	}
}

func publicationResult(
	catalog catalogartifact.Loaded,
	model modelartifact.Loaded,
	application modelrelease.ApplicationIdentity,
	publishedAt time.Time,
) configuration.CreateVersionResult {
	active := &configuration.Version{
		ID: "4", Number: 4, SchemaID: recommendation.KnowledgeCatalogSchemaV1,
		Document: catalog.Artifact.Document(), DocumentSHA256: catalog.SHA256,
		CreatedByAccountID: "33333333-3333-4333-8333-333333333333",
		CreatedBySessionID: "44444444-4444-4444-8444-444444444444", CreatedAt: publishedAt,
	}
	return configuration.CreateVersionResult{
		Item: configuration.Item{
			ID: "11111111-1111-4111-8111-111111111111", Key: configuration.KnowledgeCatalogKey,
			Kind: configuration.KindKnowledgeCatalog, HeadRevision: 4, ActiveVersion: active,
		},
		AuditEventID: 29,
		KnowledgeCatalogPublication: &configuration.KnowledgeCatalogPublication{
			AuthorizationID: requestAuthorizationID, KnowledgeCatalogPublicationID: "7", TargetModelReleaseID: "23",
			CatalogSHA256: catalog.SHA256, TargetModelArtifactSHA256: model.SHA256,
			TargetModelID: model.Model.Manifest().ModelID, TargetApplicationVersion: application.Version,
			TargetApplicationCommit: application.Commit, TargetApplicationBuildTime: application.BuildTime,
			ConfigurationID:                   "11111111-1111-4111-8111-111111111111",
			ExpectedConfigurationHeadRevision: 3, ConfigurationHeadRevision: 4, ConfigurationMutated: true,
			ConfigurationVersionID: "4", ConfigurationVersionNumber: 4,
			AnalyticsGenerationID: "17", AnalyticsHeadRevision: 9, InputManifestSHA256: strings.Repeat("a", 64),
			CurrentModelHeadRevision: 2, CurrentModelArtifactSHA256: strings.Repeat("b", 64),
			PublishedByAccountID: active.CreatedByAccountID, PublishedBySessionID: active.CreatedBySessionID,
			PublishedAt: publishedAt, AuditEventID: 29,
		},
	}
}

const requestAuthorizationID = "22222222-2222-4222-8222-222222222222"

func publicationApplication() modelrelease.ApplicationIdentity {
	return modelrelease.ApplicationIdentity{
		Version: "0.2.0", Commit: strings.Repeat("c", 40), BuildTime: "2026-07-13T04:00:00Z",
	}
}

func publicationRequest(
	catalog catalogartifact.Loaded,
	model modelartifact.Loaded,
	application modelrelease.ApplicationIdentity,
) Request {
	return Request{
		AuthorizationID: requestAuthorizationID,
		CatalogPublicationIntent: configuration.CatalogPublicationIntent{
			Schema:                             RequestSchema,
			ExpectedConfigurationHeadRevision:  3,
			ExpectedAnalyticsGenerationID:      "17",
			ExpectedAnalyticsHeadRevision:      9,
			ExpectedInputManifestSHA256:        strings.Repeat("a", 64),
			ExpectedCurrentModelHeadRevision:   2,
			ExpectedCurrentModelArtifactSHA256: strings.Repeat("b", 64),
			TargetCatalogSHA256:                catalog.SHA256,
			TargetModelArtifactSHA256:          model.SHA256,
			TargetApplicationVersion:           application.Version,
			TargetApplicationCommit:            application.Commit,
			TargetApplicationBuildTime:         application.BuildTime,
		},
	}
}

func publicationArtifacts(t *testing.T) (catalogartifact.Loaded, modelartifact.Loaded) {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate publication test source")
	}
	fixtureDirectory := filepath.Clean(filepath.Join(
		filepath.Dir(source), "..", "..", "..", "contracts", "recommendation", "fixtures",
	))
	modelPath := filepath.Join(fixtureDirectory, "e2e-test-only.inference-model.v1.json")
	modelBytes, err := os.ReadFile(modelPath)
	if err != nil {
		t.Fatal(err)
	}
	modelDigest := sha256.Sum256(modelBytes)
	model, err := modelartifact.Load(modelPath, hex.EncodeToString(modelDigest[:]))
	if err != nil {
		t.Fatal(err)
	}
	catalogPath := filepath.Join(fixtureDirectory, "e2e-test-only.knowledge-catalog.v1.json")
	catalogBytes, err := os.ReadFile(catalogPath)
	if err != nil {
		t.Fatal(err)
	}
	catalogDigest := sha256.Sum256(catalogBytes)
	catalog, err := catalogartifact.Load(catalogPath, hex.EncodeToString(catalogDigest[:]), model.Model.Manifest())
	if err != nil {
		t.Fatal(err)
	}
	return catalog, model
}
