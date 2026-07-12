package agentnotes

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

const (
	testAccountID  = "123e4567-e89b-42d3-a456-426614174000"
	testSessionID  = "123e4567-e89b-42d3-a456-426614174001"
	testJWTID      = "123e4567-e89b-42d3-a456-426614174002"
	testNoteID     = "123e4567-e89b-42d3-a456-426614174003"
	testMutationID = "123e4567-e89b-42d3-a456-426614174004"
)

type serviceRepository struct {
	page     Page
	note     Note
	found    bool
	result   MutationResult
	err      error
	mutation UserMutationCommand
}

func (repository *serviceRepository) LoadPage(context.Context, ListQuery) (Page, error) {
	return repository.page, repository.err
}

func (repository *serviceRepository) LoadDetail(context.Context, DetailQuery) (Note, bool, error) {
	return repository.note, repository.found, repository.err
}

func (repository *serviceRepository) ApplyUserMutation(_ context.Context, command UserMutationCommand) (MutationResult, error) {
	repository.mutation = command
	if repository.err != nil {
		return MutationResult{}, repository.err
	}
	result := repository.result
	if result.Note.ID == "" {
		now := time.Date(2026, 7, 11, 8, 9, 10, 123000000, time.UTC)
		state := StateActive
		title := command.Title
		content := command.Content
		digest := command.ContentSHA256
		if command.Operation == OperationArchive {
			state = StateArchived
		}
		if command.Operation == OperationArchive || command.Operation == OperationRestore {
			title = "Existing"
			content = "body"
			digest = digestContent(content)
		}
		result.Note = Note{
			Summary: Summary{
				ID: command.NoteID, HeadRevision: command.ExpectedHeadRevision + 1, State: state,
				Title: title, ContentSHA256: digest,
				CurrentMutationID: command.MutationID, CurrentOperation: command.Operation,
				CurrentRevisionCreatedAt: now, CreatedAt: now, UpdatedAt: now,
			},
			Content: content,
		}
	}
	return result, nil
}

func TestCreateBuildsCanonicalUserRevision(t *testing.T) {
	t.Parallel()
	repository := &serviceRepository{}
	service, err := newService(repository, func() (string, error) { return testNoteID, nil })
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Create(context.Background(), CreateCommand{
		Principal: validPrincipal(), MutationID: testMutationID, ExpectedHeadRevision: 0,
		Title: "Training plan", Content: "First line\nSecond line",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Note.ID != testNoteID || repository.mutation.Operation != OperationCreate ||
		repository.mutation.ContentSHA256 != digestContent("First line\nSecond line") ||
		repository.mutation.Principal.AccountID != testAccountID {
		t.Fatalf("result=%#v command=%#v", result, repository.mutation)
	}
}

func TestUserMutationsUseExplicitOperationsAndCAS(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		operation Operation
		invoke    func(*Service) (MutationResult, error)
	}{
		{
			name: "replace", operation: OperationReplace,
			invoke: func(service *Service) (MutationResult, error) {
				return service.Replace(context.Background(), ReplaceCommand{
					Principal: validPrincipal(), NoteID: testNoteID, MutationID: testMutationID,
					ExpectedHeadRevision: 7, Title: "Revised", Content: "body",
				})
			},
		},
		{
			name: "archive", operation: OperationArchive,
			invoke: func(service *Service) (MutationResult, error) {
				return service.Archive(context.Background(), StateCommand{
					Principal: validPrincipal(), NoteID: testNoteID, MutationID: testMutationID, ExpectedHeadRevision: 7,
				})
			},
		},
		{
			name: "restore", operation: OperationRestore,
			invoke: func(service *Service) (MutationResult, error) {
				return service.Restore(context.Background(), StateCommand{
					Principal: validPrincipal(), NoteID: testNoteID, MutationID: testMutationID, ExpectedHeadRevision: 7,
				})
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repository := &serviceRepository{}
			service, err := newService(repository, func() (string, error) { return testNoteID, nil })
			if err != nil {
				t.Fatal(err)
			}
			if _, err := test.invoke(service); err != nil {
				t.Fatal(err)
			}
			if repository.mutation.Operation != test.operation || repository.mutation.ExpectedHeadRevision != 7 ||
				repository.mutation.NoteID != testNoteID {
				t.Fatalf("command=%#v", repository.mutation)
			}
		})
	}
}

func TestServiceRejectsNonCanonicalInputBeforeRepository(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		command CreateCommand
		code    ErrorCode
	}{
		{
			name:    "nonzero create head",
			command: CreateCommand{Principal: validPrincipal(), MutationID: testMutationID, ExpectedHeadRevision: 1, Title: "Title", Content: "body"},
			code:    ErrorInvalidQuery,
		},
		{
			name:    "title whitespace",
			command: CreateCommand{Principal: validPrincipal(), MutationID: testMutationID, Title: " Title", Content: "body"},
			code:    ErrorInvalidQuery,
		},
		{
			name:    "decomposed unicode",
			command: CreateCommand{Principal: validPrincipal(), MutationID: testMutationID, Title: "Cafe\u0301", Content: "body"},
			code:    ErrorInvalidQuery,
		},
		{
			name:    "CR line ending",
			command: CreateCommand{Principal: validPrincipal(), MutationID: testMutationID, Title: "Title", Content: "a\r\nb"},
			code:    ErrorInvalidQuery,
		},
		{
			name: "admin",
			command: func() CreateCommand {
				principal := validPrincipal()
				principal.Role = auth.RoleAdmin
				return CreateCommand{Principal: principal, MutationID: testMutationID, Title: "Title", Content: "body"}
			}(),
			code: ErrorPrincipalRejected,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repository := &serviceRepository{}
			service, err := newService(repository, func() (string, error) { return testNoteID, nil })
			if err != nil {
				t.Fatal(err)
			}
			if _, err := service.Create(context.Background(), test.command); CodeOf(err) != test.code {
				t.Fatalf("error=%v code=%q", err, CodeOf(err))
			}
			if repository.mutation.MutationID != "" {
				t.Fatal("repository received invalid input")
			}
		})
	}

	oversized := strings.Repeat("x", MaxContentBytes+1)
	service, _ := newService(&serviceRepository{}, func() (string, error) { return testNoteID, nil })
	if _, err := service.Create(context.Background(), CreateCommand{
		Principal: validPrincipal(), MutationID: testMutationID, Title: "Title", Content: oversized,
	}); CodeOf(err) != ErrorInvalidQuery {
		t.Fatalf("oversized error=%v", err)
	}
}

func TestListCursorRoundTripAndValidation(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 11, 8, 9, 10, 123456000, time.UTC)
	summary := Summary{ID: testNoteID, UpdatedAt: now}
	cursor, err := encodeCursor(summary)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeCursor(cursor)
	if err != nil || decoded.NoteID != testNoteID || !decoded.UpdatedAt.Equal(now) || !ValidCursor(cursor) {
		t.Fatalf("decoded=%#v error=%v", decoded, err)
	}
	for _, invalid := range []string{"", cursor + "=", "bm90ZS1jdXJzb3I"} {
		if ValidCursor(invalid) {
			t.Fatalf("accepted cursor %q", invalid)
		}
	}
}

func validPrincipal() auth.AccessPrincipal {
	return auth.AccessPrincipal{
		AccountID: testAccountID, SessionID: testSessionID, JWTID: testJWTID,
		Role: auth.RoleStudent, AuthRevision: 1,
	}
}
