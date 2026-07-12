package agentnotes

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

var (
	canonicalUUIDv4 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	lowercaseSHA256 = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

func ValidPublicID(value string) bool { return canonicalUUIDv4.MatchString(value) }

type Repository interface {
	LoadPage(context.Context, ListQuery) (Page, error)
	LoadDetail(context.Context, DetailQuery) (Note, bool, error)
	ApplyUserMutation(context.Context, UserMutationCommand) (MutationResult, error)
}

type UUIDGenerator func() (string, error)

type Service struct {
	repository Repository
	uuid       UUIDGenerator
}

func NewService(repository Repository) (*Service, error) {
	return newService(repository, randomUUIDv4)
}

func newService(repository Repository, uuid UUIDGenerator) (*Service, error) {
	if repository == nil || uuid == nil {
		return nil, notesError(ErrorInvalidConfiguration, "construct agent notes service", errors.New("repository and UUID generator are required"))
	}
	return &Service{repository: repository, uuid: uuid}, nil
}

func (service *Service) List(ctx context.Context, query ListQuery) (Page, error) {
	if err := validateListQuery(ctx, query); err != nil {
		return Page{}, err
	}
	page, err := service.repository.LoadPage(ctx, query)
	if err != nil {
		return Page{}, err
	}
	if err := validatePage(page, query.Limit); err != nil {
		return Page{}, notesError(ErrorStoredDataInvalid, "validate agent note page", err)
	}
	return page, nil
}

func (service *Service) Get(ctx context.Context, query DetailQuery) (Note, bool, error) {
	if err := validateDetailQuery(ctx, query); err != nil {
		return Note{}, false, err
	}
	note, found, err := service.repository.LoadDetail(ctx, query)
	if err != nil || !found {
		return Note{}, found, err
	}
	if note.ID != query.NoteID {
		return Note{}, false, notesError(ErrorStoredDataInvalid, "validate agent note detail", errors.New("repository returned a different note"))
	}
	if err := validateNote(note); err != nil {
		return Note{}, false, notesError(ErrorStoredDataInvalid, "validate agent note detail", err)
	}
	return note, true, nil
}

func (service *Service) Create(ctx context.Context, command CreateCommand) (MutationResult, error) {
	if err := validateMutationMetadata(ctx, command.Principal, "", command.MutationID, command.ExpectedHeadRevision, true); err != nil {
		return MutationResult{}, err
	}
	if command.ExpectedHeadRevision != 0 {
		return MutationResult{}, notesError(ErrorInvalidQuery, "validate agent note create command", errors.New("create must compare against head revision zero"))
	}
	title, content, digest, err := canonicalizeDocument(command.Title, command.Content)
	if err != nil {
		return MutationResult{}, notesError(ErrorInvalidQuery, "canonicalize agent note create document", err)
	}
	noteID, err := service.uuid()
	if err != nil {
		return MutationResult{}, notesError(ErrorInvalidConfiguration, "generate agent note ID", err)
	}
	if !canonicalUUIDv4.MatchString(noteID) {
		return MutationResult{}, notesError(ErrorInvalidConfiguration, "generate agent note ID", errors.New("UUID generator returned a non-canonical UUIDv4"))
	}
	return service.apply(ctx, UserMutationCommand{
		Principal: command.Principal, NoteID: noteID, MutationID: command.MutationID,
		Operation: OperationCreate, ExpectedHeadRevision: 0, Title: title, Content: content, ContentSHA256: digest,
	})
}

func (service *Service) Replace(ctx context.Context, command ReplaceCommand) (MutationResult, error) {
	if err := validateMutationMetadata(ctx, command.Principal, command.NoteID, command.MutationID, command.ExpectedHeadRevision, false); err != nil {
		return MutationResult{}, err
	}
	title, content, digest, err := canonicalizeDocument(command.Title, command.Content)
	if err != nil {
		return MutationResult{}, notesError(ErrorInvalidQuery, "canonicalize agent note replacement", err)
	}
	return service.apply(ctx, UserMutationCommand{
		Principal: command.Principal, NoteID: command.NoteID, MutationID: command.MutationID,
		Operation: OperationReplace, ExpectedHeadRevision: command.ExpectedHeadRevision,
		Title: title, Content: content, ContentSHA256: digest,
	})
}

func (service *Service) Archive(ctx context.Context, command StateCommand) (MutationResult, error) {
	return service.changeState(ctx, command, OperationArchive)
}

func (service *Service) Restore(ctx context.Context, command StateCommand) (MutationResult, error) {
	return service.changeState(ctx, command, OperationRestore)
}

func (service *Service) changeState(ctx context.Context, command StateCommand, operation Operation) (MutationResult, error) {
	if err := validateMutationMetadata(ctx, command.Principal, command.NoteID, command.MutationID, command.ExpectedHeadRevision, false); err != nil {
		return MutationResult{}, err
	}
	return service.apply(ctx, UserMutationCommand{
		Principal: command.Principal, NoteID: command.NoteID, MutationID: command.MutationID,
		Operation: operation, ExpectedHeadRevision: command.ExpectedHeadRevision,
	})
}

func (service *Service) apply(ctx context.Context, command UserMutationCommand) (MutationResult, error) {
	result, err := service.repository.ApplyUserMutation(ctx, command)
	if err != nil {
		return MutationResult{}, err
	}
	if err := validateNote(result.Note); err != nil {
		return MutationResult{}, notesError(ErrorStoredDataInvalid, "validate agent note mutation result", err)
	}
	if command.Operation != OperationCreate && result.Note.ID != command.NoteID {
		return MutationResult{}, notesError(ErrorStoredDataInvalid, "validate agent note mutation result", errors.New("repository mutated a different note"))
	}
	if !result.Idempotent {
		if result.Note.HeadRevision != command.ExpectedHeadRevision+1 || result.Note.CurrentMutationID != command.MutationID ||
			result.Note.CurrentOperation != command.Operation {
			return MutationResult{}, notesError(ErrorStoredDataInvalid, "validate agent note mutation result", errors.New("repository returned a different committed revision"))
		}
		if (command.Operation == OperationCreate || command.Operation == OperationReplace) &&
			(result.Note.Title != command.Title || result.Note.Content != command.Content || result.Note.ContentSHA256 != command.ContentSHA256) {
			return MutationResult{}, notesError(ErrorStoredDataInvalid, "validate agent note mutation result", errors.New("repository returned a different committed document"))
		}
	}
	return result, nil
}

func validateListQuery(ctx context.Context, query ListQuery) error {
	if ctx == nil || query.Limit < 1 || query.Limit > MaxPageSize {
		return notesError(ErrorInvalidQuery, "validate agent note list query", errors.New("context and bounded limit are required"))
	}
	if err := validateStudentPrincipal(query.Principal); err != nil {
		return err
	}
	if query.Cursor != nil {
		if _, err := decodeCursor(*query.Cursor); err != nil {
			return notesError(ErrorCursorInvalid, "validate agent note list query", err)
		}
	}
	return nil
}

func validateDetailQuery(ctx context.Context, query DetailQuery) error {
	if ctx == nil || !canonicalUUIDv4.MatchString(query.NoteID) {
		return notesError(ErrorInvalidQuery, "validate agent note detail query", errors.New("context and canonical note ID are required"))
	}
	return validateStudentPrincipal(query.Principal)
}

func validateMutationMetadata(ctx context.Context, principal auth.AccessPrincipal, noteID, mutationID string, expected int64, create bool) error {
	if ctx == nil || !canonicalUUIDv4.MatchString(mutationID) || expected < 0 || (!create && !canonicalUUIDv4.MatchString(noteID)) {
		return notesError(ErrorInvalidQuery, "validate agent note mutation", errors.New("context, canonical identities, and nonnegative expected head are required"))
	}
	return validateStudentPrincipal(principal)
}

func validateStudentPrincipal(principal auth.AccessPrincipal) error {
	if !canonicalUUIDv4.MatchString(principal.AccountID) || !canonicalUUIDv4.MatchString(principal.SessionID) ||
		!canonicalUUIDv4.MatchString(principal.JWTID) || principal.AuthRevision < 1 {
		return notesError(ErrorInvalidQuery, "validate agent notes principal", errors.New("canonical access principal is required"))
	}
	if principal.Role != auth.RoleStudent {
		return notesError(ErrorPrincipalRejected, "authorize agent notes principal", errors.New("student role is required"))
	}
	return nil
}

func validatePage(page Page, limit int) error {
	if page.Items == nil || len(page.Items) > limit {
		return errors.New("agent note page is nil or oversized")
	}
	seen := make(map[string]struct{}, len(page.Items))
	for index, item := range page.Items {
		if err := validateSummary(item); err != nil {
			return err
		}
		if _, exists := seen[item.ID]; exists {
			return errors.New("agent note page contains a duplicate note")
		}
		seen[item.ID] = struct{}{}
		if index > 0 && (item.UpdatedAt.After(page.Items[index-1].UpdatedAt) || item.UpdatedAt.Equal(page.Items[index-1].UpdatedAt) && item.ID == page.Items[index-1].ID) {
			return errors.New("agent note page order is invalid")
		}
	}
	if page.NextCursor != nil {
		if len(page.Items) == 0 {
			return errors.New("empty agent note page has a cursor")
		}
		cursor, err := decodeCursor(*page.NextCursor)
		last := page.Items[len(page.Items)-1]
		if err != nil || cursor.NoteID != last.ID || !cursor.UpdatedAt.Equal(last.UpdatedAt) {
			return errors.New("agent note page cursor does not identify its final item")
		}
	}
	return nil
}

func validateSummary(summary Summary) error {
	if !canonicalUUIDv4.MatchString(summary.ID) || summary.HeadRevision < 1 ||
		(summary.State != StateActive && summary.State != StateArchived) ||
		!canonicalUUIDv4.MatchString(summary.CurrentMutationID) || !validOperation(summary.CurrentOperation) ||
		!lowercaseSHA256.MatchString(summary.ContentSHA256) || !validUTCTime(summary.CurrentRevisionCreatedAt) ||
		!validUTCTime(summary.CreatedAt) || !validUTCTime(summary.UpdatedAt) || summary.UpdatedAt.Before(summary.CreatedAt) ||
		!summary.CurrentRevisionCreatedAt.Equal(summary.UpdatedAt) {
		return errors.New("agent note summary violates the public contract")
	}
	if _, _, _, err := canonicalizeDocument(summary.Title, ""); err != nil {
		return errors.New("agent note summary title is invalid")
	}
	if summary.CurrentOperation == OperationArchive && summary.State != StateArchived ||
		summary.CurrentOperation != OperationArchive && summary.State != StateActive {
		return errors.New("agent note state differs from its current operation")
	}
	return nil
}

func validateNote(note Note) error {
	if err := validateSummary(note.Summary); err != nil {
		return err
	}
	_, content, digest, err := canonicalizeDocument(note.Title, note.Content)
	if err != nil || content != note.Content || digest != note.ContentSHA256 {
		return errors.New("agent note content or digest is invalid")
	}
	return nil
}

func validOperation(operation Operation) bool {
	switch operation {
	case OperationCreate, OperationReplace, OperationArchive, OperationRestore:
		return true
	default:
		return false
	}
}

func validUTCTime(value time.Time) bool {
	if value.IsZero() {
		return false
	}
	_, offset := value.Zone()
	return offset == 0
}

func randomUUIDv4() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	raw[6] = raw[6]&0x0f | 0x40
	raw[8] = raw[8]&0x3f | 0x80
	encoded := hex.EncodeToString(raw[:])
	return encoded[:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:], nil
}

func digestContent(content string) string {
	digest := sha256.Sum256([]byte(content))
	return hex.EncodeToString(digest[:])
}
