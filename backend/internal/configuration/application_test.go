package configuration

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

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
	createCommand CreateVersionCommand
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

func TestApplicationServiceOwnsVerifiedPrincipal(t *testing.T) {
	principal := testAdminPrincipal()
	configuration := &configurationStub{}
	application, err := NewApplicationService(&verifierStub{principal: principal}, configuration)
	if err != nil {
		t.Fatal(err)
	}
	_, err = application.CreateVersion(context.Background(), "access", CreateVersionInput{
		Key: "prompt.main", Kind: KindPrompt, SchemaID: "ascendany.prompt.v1", Document: json.RawMessage(`{}`),
	})
	if err != nil || configuration.createCommand.Principal != principal || configuration.createCommand.Key != "prompt.main" {
		t.Fatalf("command=%#v error=%v", configuration.createCommand, err)
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
