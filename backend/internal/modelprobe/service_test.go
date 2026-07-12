package modelprobe

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/chatagent"
	"github.com/kkkzbh/AscendAny/backend/internal/configuration"
)

type configurationReaderStub struct {
	item  configuration.Item
	found bool
	err   error
	token string
	key   string
}

func (stub *configurationReaderStub) Get(_ context.Context, token, key string) (configuration.Item, bool, error) {
	stub.token = token
	stub.key = key
	return stub.item, stub.found, stub.err
}

type providerStub struct {
	result   chatagent.ModelConnectionProbeResult
	err      error
	snapshot chatagent.ConfigurationSnapshot
}

func (stub *providerStub) ProbeModelConnection(_ context.Context, snapshot chatagent.ConfigurationSnapshot) (chatagent.ModelConnectionProbeResult, error) {
	stub.snapshot = snapshot
	return stub.result, stub.err
}

func TestServiceTestsExactActiveModelVersionAndReturnsBoundedMetadata(t *testing.T) {
	t.Parallel()
	credentialRef := "models.primary"
	checkedAt := time.Date(2026, 7, 11, 9, 8, 7, 0, time.FixedZone("offset", 8*60*60))
	reader := &configurationReaderStub{found: true, item: configuration.Item{
		Key: "agent.model.default", Kind: configuration.KindModelConnection, HeadRevision: 3,
		ActiveVersion: &configuration.Version{
			Number: 3, SchemaID: chatagent.OpenAICompatibleModelSchema,
			Document:       json.RawMessage(`{"endpoint":"https://models.example/v1/chat/completions","model":"reasoner","timeoutMilliseconds":1000,"maxCompletionTokens":128}`),
			DocumentSHA256: strings.Repeat("a", 64), CredentialRef: &credentialRef,
		},
	}}
	provider := &providerStub{result: chatagent.ModelConnectionProbeResult{
		Authority: "models.example:443", Model: "reasoner", LatencyMilliseconds: 17,
	}}
	service, err := newService(reader, provider, func() time.Time { return checkedAt })
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Test(context.Background(), "admin-access", "agent.model.default")
	if err != nil {
		t.Fatal(err)
	}
	if reader.token != "admin-access" || reader.key != "agent.model.default" ||
		provider.snapshot.Key != "agent.model.default" || provider.snapshot.CredentialRef == nil ||
		*provider.snapshot.CredentialRef != credentialRef || result.ConfigurationHeadRevision != 3 ||
		result.ConfigurationVersion != 3 || result.ConfigurationSHA256 != strings.Repeat("a", 64) ||
		result.Authority != "models.example:443" || result.Model != "reasoner" ||
		result.LatencyMilliseconds != 17 || !result.CheckedAt.Equal(checkedAt.UTC()) || result.CheckedAt.Location() != time.UTC {
		t.Fatalf("result=%#v snapshot=%#v reader=%#v", result, provider.snapshot, reader)
	}
}

func TestServicePreservesAuthorizationFailureAndRejectsWrongConfiguration(t *testing.T) {
	t.Parallel()
	authFailure := errors.New("authorization rejected")
	service, err := NewService(&configurationReaderStub{err: authFailure}, &providerStub{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Test(context.Background(), "token", "agent.model.default"); !errors.Is(err, authFailure) {
		t.Fatalf("authorization error=%v", err)
	}

	service, err = NewService(&configurationReaderStub{found: false}, &providerStub{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Test(context.Background(), "token", "agent.model.default"); CodeOf(err) != ErrorConfigurationMissing {
		t.Fatalf("missing code=%q error=%v", CodeOf(err), err)
	}

	service, err = NewService(&configurationReaderStub{found: true, item: configuration.Item{
		Key: "agent.model.default", Kind: configuration.KindPrompt, HeadRevision: 1,
		ActiveVersion: &configuration.Version{Number: 1, DocumentSHA256: strings.Repeat("b", 64)},
	}}, &providerStub{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Test(context.Background(), "token", "agent.model.default"); CodeOf(err) != ErrorConfigurationKind {
		t.Fatalf("kind code=%q error=%v", CodeOf(err), err)
	}
}

func TestServiceSanitizesProviderFailureAtThePublicBoundary(t *testing.T) {
	t.Parallel()
	secret := "provider-secret"
	credentialRef := "models.primary"
	reader := &configurationReaderStub{found: true, item: configuration.Item{
		Key: "agent.model.default", Kind: configuration.KindModelConnection, HeadRevision: 1,
		ActiveVersion: &configuration.Version{
			Number: 1, SchemaID: chatagent.OpenAICompatibleModelSchema,
			Document: json.RawMessage(`{}`), DocumentSHA256: strings.Repeat("c", 64), CredentialRef: &credentialRef,
		},
	}}
	providerFailure := &chatagent.ProviderFailure{Code: "provider_auth_rejected", Detail: "model endpoint rejected authorization", Cause: errors.New("status 401")}
	service, err := NewService(reader, &providerStub{err: providerFailure})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Test(context.Background(), "token", "agent.model.default")
	if CodeOf(err) != ErrorProviderRejected || strings.Contains(err.Error(), secret) {
		t.Fatalf("provider error=%v", err)
	}
}

func TestServiceRejectsInvalidConstructionAndInput(t *testing.T) {
	t.Parallel()
	if _, err := NewService(nil, &providerStub{}); CodeOf(err) != ErrorInvalidConfiguration {
		t.Fatalf("construction error=%v", err)
	}
	service, err := NewService(&configurationReaderStub{}, &providerStub{})
	if err != nil {
		t.Fatal(err)
	}
	for _, input := range []struct {
		ctx   context.Context
		token string
		key   string
	}{
		{ctx: nil, token: "token", key: "agent.model.default"},
		{ctx: context.Background(), token: "", key: "agent.model.default"},
		{ctx: context.Background(), token: "token", key: "Bad Key"},
	} {
		if _, err := service.Test(input.ctx, input.token, input.key); CodeOf(err) != ErrorInvalidInput {
			t.Fatalf("input=%#v error=%v", input, err)
		}
	}
}
