package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/configuration"
)

func TestCatalogPublicationAuthorizationRouteReturnsExactReleaseRequest(t *testing.T) {
	t.Parallel()
	intent := catalogPublicationAuthorizationIntent()
	input := configuration.CatalogPublicationAuthorizationInput{
		PublicationIntent: intent,
		Document:          json.RawMessage(`{"taxonomyId":"recommendation.taxonomy.v1","knowledgePoints":[],"problemAssignments":[]}`),
	}
	authorizationID := "33333333-3333-4333-8333-333333333333"
	expiresAt := time.Date(2026, 7, 14, 4, 15, 0, 0, time.UTC)
	result := configuration.CatalogPublicationAuthorizationResult{
		AuthorizationID: authorizationID,
		ExpiresAt:       expiresAt,
		PublicationRequest: configuration.AuthorizedCatalogPublicationRequest{
			AuthorizationID:          authorizationID,
			CatalogPublicationIntent: intent,
		},
	}
	calls := 0
	service := configurationServiceStub{authorize: func(
		_ context.Context,
		access string,
		got configuration.CatalogPublicationAuthorizationInput,
	) (configuration.CatalogPublicationAuthorizationResult, error) {
		calls++
		if access != "admin-token" || got.PublicationIntent != input.PublicationIntent ||
			string(got.Document) != string(input.Document) {
			t.Fatalf("access=%q input=%#v", access, got)
		}
		return result, nil
	}}
	handler := newConfigurationTestHandler(t, service, true)
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v2/admin/recommendation/catalog-publication-authorizations", strings.NewReader(string(body)))
	request.RemoteAddr = "192.0.2.10:43100"
	request.Header.Set("Authorization", "Bearer admin-token")
	request.Header.Set("Content-Type", "application/json")
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || calls != 1 {
		t.Fatalf("status=%d calls=%d body=%s", response.Code, calls, response.Body)
	}
	var responseFields map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &responseFields); err != nil {
		t.Fatal(err)
	}
	var requestFields map[string]json.RawMessage
	if err := json.Unmarshal(responseFields["publicationRequest"], &requestFields); err != nil {
		t.Fatal(err)
	}
	if len(responseFields) != 3 || len(requestFields) != 13 ||
		string(responseFields["authorizationId"]) != `"`+authorizationID+`"` ||
		string(responseFields["expiresAt"]) != `"2026-07-14T04:15:00Z"` ||
		string(requestFields["authorizationId"]) != `"`+authorizationID+`"` {
		t.Fatalf("response=%s", response.Body)
	}
}

func TestCatalogPublicationAuthorizationRouteRejectsUnknownFieldsBeforeService(t *testing.T) {
	t.Parallel()
	calls := 0
	service := configurationServiceStub{authorize: func(
		context.Context,
		string,
		configuration.CatalogPublicationAuthorizationInput,
	) (configuration.CatalogPublicationAuthorizationResult, error) {
		calls++
		return configuration.CatalogPublicationAuthorizationResult{}, nil
	}}
	handler := newConfigurationTestHandler(t, service, true)
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v2/admin/recommendation/catalog-publication-authorizations",
		strings.NewReader(`{"publicationIntent":{},"document":{},"authorizationId":"forbidden"}`),
	)
	request.RemoteAddr = "192.0.2.10:43100"
	request.Header.Set("Authorization", "Bearer admin-token")
	request.Header.Set("Content-Type", "application/json")
	response := newTestResponseRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || calls != 0 {
		t.Fatalf("status=%d calls=%d body=%s", response.Code, calls, response.Body)
	}
}

func catalogPublicationAuthorizationIntent() configuration.CatalogPublicationIntent {
	return configuration.CatalogPublicationIntent{
		Schema:                             configuration.CatalogPublicationRequestSchema,
		ExpectedConfigurationHeadRevision:  3,
		ExpectedAnalyticsGenerationID:      "17",
		ExpectedAnalyticsHeadRevision:      9,
		ExpectedInputManifestSHA256:        strings.Repeat("a", 64),
		ExpectedCurrentModelHeadRevision:   2,
		ExpectedCurrentModelArtifactSHA256: strings.Repeat("b", 64),
		TargetCatalogSHA256:                strings.Repeat("c", 64),
		TargetModelArtifactSHA256:          strings.Repeat("d", 64),
		TargetApplicationVersion:           "0.2.0",
		TargetApplicationCommit:            strings.Repeat("e", 40),
		TargetApplicationBuildTime:         "2026-07-13T04:00:00Z",
	}
}
