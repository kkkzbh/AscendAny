package agentnotes

import (
	"context"
	"errors"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

type AccessPrincipalVerifier interface {
	VerifyAccessToken(string) (auth.AccessPrincipal, error)
}

type Notes interface {
	List(context.Context, ListQuery) (Page, error)
	Get(context.Context, DetailQuery) (Note, bool, error)
	Create(context.Context, CreateCommand) (MutationResult, error)
	Replace(context.Context, ReplaceCommand) (MutationResult, error)
	Archive(context.Context, StateCommand) (MutationResult, error)
	Restore(context.Context, StateCommand) (MutationResult, error)
}

type ApplicationService struct {
	verifier AccessPrincipalVerifier
	notes    Notes
}

func NewApplicationService(verifier AccessPrincipalVerifier, notes Notes) (*ApplicationService, error) {
	if verifier == nil || notes == nil {
		return nil, notesError(ErrorInvalidConfiguration, "construct agent notes application service", errors.New("principal verifier and notes service are required"))
	}
	return &ApplicationService{verifier: verifier, notes: notes}, nil
}

func (service *ApplicationService) List(ctx context.Context, token string, cursor *string, limit int) (Page, error) {
	principal, err := service.verifier.VerifyAccessToken(token)
	if err != nil {
		return Page{}, err
	}
	return service.notes.List(ctx, ListQuery{Principal: principal, Cursor: cursor, Limit: limit})
}

func (service *ApplicationService) Get(ctx context.Context, token, noteID string) (Note, bool, error) {
	principal, err := service.verifier.VerifyAccessToken(token)
	if err != nil {
		return Note{}, false, err
	}
	return service.notes.Get(ctx, DetailQuery{Principal: principal, NoteID: noteID})
}

func (service *ApplicationService) Create(ctx context.Context, token string, input CreateInput) (MutationResult, error) {
	principal, err := service.verifier.VerifyAccessToken(token)
	if err != nil {
		return MutationResult{}, err
	}
	return service.notes.Create(ctx, CreateCommand{
		Principal: principal, MutationID: input.MutationID, ExpectedHeadRevision: input.ExpectedHeadRevision,
		Title: input.Title, Content: input.Content,
	})
}

func (service *ApplicationService) Replace(ctx context.Context, token, noteID string, input ReplaceInput) (MutationResult, error) {
	principal, err := service.verifier.VerifyAccessToken(token)
	if err != nil {
		return MutationResult{}, err
	}
	return service.notes.Replace(ctx, ReplaceCommand{
		Principal: principal, NoteID: noteID, MutationID: input.MutationID,
		ExpectedHeadRevision: input.ExpectedHeadRevision, Title: input.Title, Content: input.Content,
	})
}

func (service *ApplicationService) Archive(ctx context.Context, token, noteID string, input StateInput) (MutationResult, error) {
	principal, err := service.verifier.VerifyAccessToken(token)
	if err != nil {
		return MutationResult{}, err
	}
	return service.notes.Archive(ctx, StateCommand{
		Principal: principal, NoteID: noteID, MutationID: input.MutationID, ExpectedHeadRevision: input.ExpectedHeadRevision,
	})
}

func (service *ApplicationService) Restore(ctx context.Context, token, noteID string, input StateInput) (MutationResult, error) {
	principal, err := service.verifier.VerifyAccessToken(token)
	if err != nil {
		return MutationResult{}, err
	}
	return service.notes.Restore(ctx, StateCommand{
		Principal: principal, NoteID: noteID, MutationID: input.MutationID, ExpectedHeadRevision: input.ExpectedHeadRevision,
	})
}
