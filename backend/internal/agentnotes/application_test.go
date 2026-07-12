package agentnotes

import (
	"context"
	"errors"
	"testing"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

type applicationVerifier struct {
	principal auth.AccessPrincipal
	err       error
	token     string
}

func (verifier *applicationVerifier) VerifyAccessToken(token string) (auth.AccessPrincipal, error) {
	verifier.token = token
	return verifier.principal, verifier.err
}

type applicationNotes struct {
	createCommand  CreateCommand
	replaceCommand ReplaceCommand
	stateCommand   StateCommand
	result         MutationResult
}

func (*applicationNotes) List(context.Context, ListQuery) (Page, error) { return Page{}, nil }
func (*applicationNotes) Get(context.Context, DetailQuery) (Note, bool, error) {
	return Note{}, false, nil
}
func (notes *applicationNotes) Create(_ context.Context, command CreateCommand) (MutationResult, error) {
	notes.createCommand = command
	return notes.result, nil
}
func (notes *applicationNotes) Replace(_ context.Context, command ReplaceCommand) (MutationResult, error) {
	notes.replaceCommand = command
	return notes.result, nil
}
func (notes *applicationNotes) Archive(_ context.Context, command StateCommand) (MutationResult, error) {
	notes.stateCommand = command
	return notes.result, nil
}
func (notes *applicationNotes) Restore(_ context.Context, command StateCommand) (MutationResult, error) {
	notes.stateCommand = command
	return notes.result, nil
}

func TestApplicationVerifiesTokenAndBuildsCommands(t *testing.T) {
	t.Parallel()
	verifier := &applicationVerifier{principal: validPrincipal()}
	notes := &applicationNotes{}
	service, err := NewApplicationService(verifier, notes)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(context.Background(), "access-token", CreateInput{
		MutationID: testMutationID, ExpectedHeadRevision: 0, Title: "Title", Content: "body",
	}); err != nil {
		t.Fatal(err)
	}
	if verifier.token != "access-token" || notes.createCommand.Principal.AccountID != testAccountID ||
		notes.createCommand.MutationID != testMutationID || notes.createCommand.Title != "Title" {
		t.Fatalf("verifier=%#v command=%#v", verifier, notes.createCommand)
	}
	if _, err := service.Replace(context.Background(), "access-token", testNoteID, ReplaceInput{
		MutationID: testMutationID, ExpectedHeadRevision: 4, Title: "Replacement", Content: "new body",
	}); err != nil {
		t.Fatal(err)
	}
	if notes.replaceCommand.NoteID != testNoteID || notes.replaceCommand.ExpectedHeadRevision != 4 {
		t.Fatalf("replace command=%#v", notes.replaceCommand)
	}
	if _, err := service.Archive(context.Background(), "access-token", testNoteID, StateInput{
		MutationID: testMutationID, ExpectedHeadRevision: 5,
	}); err != nil {
		t.Fatal(err)
	}
	if notes.stateCommand.NoteID != testNoteID || notes.stateCommand.ExpectedHeadRevision != 5 {
		t.Fatalf("state command=%#v", notes.stateCommand)
	}
}

func TestApplicationStopsAtTokenVerificationFailure(t *testing.T) {
	t.Parallel()
	want := errors.New("invalid token")
	verifier := &applicationVerifier{err: want}
	notes := &applicationNotes{}
	service, err := NewApplicationService(verifier, notes)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Create(context.Background(), "bad", CreateInput{})
	if !errors.Is(err, want) || notes.createCommand.MutationID != "" {
		t.Fatalf("error=%v command=%#v", err, notes.createCommand)
	}
}
