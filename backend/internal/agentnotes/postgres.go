package agentnotes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
	"github.com/kkkzbh/AscendAny/backend/internal/principalguard"
)

type PgxBeginner interface {
	BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
}

type postgresTx interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
	Commit(context.Context) error
	Rollback(context.Context) error
}

type beginTransaction func(context.Context, pgx.TxOptions) (postgresTx, error)

type PostgresRepository struct {
	begin beginTransaction
}

func NewPostgresRepository(pool PgxBeginner) (*PostgresRepository, error) {
	if pool == nil {
		return nil, notesError(ErrorInvalidConfiguration, "construct agent notes PostgreSQL repository", errors.New("database pool is required"))
	}
	return &PostgresRepository{begin: func(ctx context.Context, options pgx.TxOptions) (postgresTx, error) {
		return pool.BeginTx(ctx, options)
	}}, nil
}

func newPostgresRepository(begin beginTransaction) (*PostgresRepository, error) {
	if begin == nil {
		return nil, notesError(ErrorInvalidConfiguration, "construct agent notes PostgreSQL repository", errors.New("transaction beginner is required"))
	}
	return &PostgresRepository{begin: begin}, nil
}

func (repository *PostgresRepository) LoadPage(ctx context.Context, query ListQuery) (Page, error) {
	var page Page
	err := repository.transaction(ctx, "load agent note page", true, func(tx postgresTx) error {
		owner, err := resolveStudent(ctx, tx, query.Principal, false)
		if err != nil {
			return err
		}
		var cursorUpdatedAt *time.Time
		var cursorDatabaseID *int64
		if query.Cursor != nil {
			cursor, err := decodeCursor(*query.Cursor)
			if err != nil {
				return notesError(ErrorCursorInvalid, "resolve agent note cursor", err)
			}
			var databaseID int64
			if err := tx.QueryRow(ctx, `
SELECT agent_note_id
FROM ascendany.agent_notes
WHERE public_id = $1::uuid
  AND owner_account_id = $2`, cursor.NoteID, owner.AccountDatabaseID).Scan(&databaseID); errors.Is(err, pgx.ErrNoRows) {
				return notesError(ErrorCursorInvalid, "resolve agent note cursor", errors.New("cursor does not identify an owned note"))
			} else if err != nil {
				return databaseFailure("resolve agent note cursor", err)
			}
			cursorUpdatedAt = &cursor.UpdatedAt
			cursorDatabaseID = &databaseID
		}

		rows, err := tx.Query(ctx, summarySelect+`
WHERE note.owner_account_id = $1
  AND ($2::timestamptz IS NULL OR (note.updated_at, note.agent_note_id) < ($2, $3::bigint))
ORDER BY note.updated_at DESC, note.agent_note_id DESC
LIMIT $4`, owner.AccountDatabaseID, cursorUpdatedAt, cursorDatabaseID, query.Limit+1)
		if err != nil {
			return databaseFailure("query agent note page", err)
		}
		defer rows.Close()
		items := make([]Summary, 0, query.Limit+1)
		for rows.Next() {
			summary, _, err := scanSummary(rows)
			if err != nil {
				return databaseFailure("scan agent note page", err)
			}
			items = append(items, summary)
		}
		if err := rows.Err(); err != nil {
			return databaseFailure("iterate agent note page", err)
		}
		if len(items) > query.Limit {
			items = items[:query.Limit]
			cursor, err := encodeCursor(items[len(items)-1])
			if err != nil {
				return notesError(ErrorStoredDataInvalid, "encode agent note cursor", err)
			}
			page.NextCursor = &cursor
		}
		page.Items = items
		return nil
	})
	if err != nil {
		return Page{}, err
	}
	return page, nil
}

func (repository *PostgresRepository) LoadDetail(ctx context.Context, query DetailQuery) (Note, bool, error) {
	var note Note
	found := false
	err := repository.transaction(ctx, "load agent note detail", true, func(tx postgresTx) error {
		owner, err := resolveStudent(ctx, tx, query.Principal, false)
		if err != nil {
			return err
		}
		note, _, err = loadOwnedNote(ctx, tx, owner.AccountDatabaseID, query.NoteID, false)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return databaseFailure("query agent note detail", err)
		}
		found = true
		return nil
	})
	if err != nil {
		return Note{}, false, err
	}
	return note, found, nil
}

func (repository *PostgresRepository) ApplyUserMutation(ctx context.Context, command UserMutationCommand) (MutationResult, error) {
	var result MutationResult
	err := repository.transaction(ctx, "apply user agent note mutation", false, func(tx postgresTx) error {
		owner, err := resolveStudent(ctx, tx, command.Principal, true)
		if err != nil {
			return err
		}
		existing, found, err := loadMutation(ctx, tx, owner.AccountDatabaseID, command.MutationID)
		if err != nil {
			return err
		}
		if found {
			if err := validateReplay(existing, command); err != nil {
				return err
			}
			current, _, err := loadOwnedNote(ctx, tx, owner.AccountDatabaseID, existing.NoteID, false)
			if err != nil {
				return notesError(ErrorStoredDataInvalid, "load idempotent agent note result", err)
			}
			result = MutationResult{Note: current, Idempotent: true}
			return nil
		}

		var current Note
		var noteDatabaseID int64
		var createdAt time.Time
		if command.Operation == OperationCreate {
			if command.ExpectedHeadRevision != 0 {
				return notesError(ErrorHeadConflict, "create agent note", errors.New("new note head is zero"))
			}
			if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&createdAt); err != nil {
				return databaseFailure("read agent note mutation time", err)
			}
			if err := tx.QueryRow(ctx, `
INSERT INTO ascendany.agent_notes (
    public_id,
    owner_account_id,
    owner_role,
    created_at,
    updated_at
)
VALUES ($1::uuid, $2, 'student', $3, $3)
RETURNING agent_note_id`, command.NoteID, owner.AccountDatabaseID, createdAt).Scan(&noteDatabaseID); err != nil {
				return databaseFailure("insert agent note", err)
			}
			current = Note{Summary: Summary{
				ID: command.NoteID, HeadRevision: 0, State: StateActive,
				CreatedAt: createdAt.UTC(), UpdatedAt: createdAt.UTC(),
			}}
		} else {
			current, noteDatabaseID, err = loadOwnedNote(ctx, tx, owner.AccountDatabaseID, command.NoteID, true)
			if errors.Is(err, pgx.ErrNoRows) {
				return notesError(ErrorNotFound, "lock agent note mutation target", errors.New("owned note does not exist"))
			}
			if err != nil {
				return databaseFailure("lock agent note mutation target", err)
			}
			createdAt = current.UpdatedAt
		}
		if current.HeadRevision != command.ExpectedHeadRevision {
			return notesError(ErrorHeadConflict, "compare agent note head", fmt.Errorf("expected head revision %d, found %d", command.ExpectedHeadRevision, current.HeadRevision))
		}

		state, title, content, digest, err := nextDocument(current, command)
		if err != nil {
			return err
		}
		var mutationTime time.Time
		if command.Operation == OperationCreate {
			mutationTime = createdAt
		} else if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&mutationTime); err != nil {
			return databaseFailure("read agent note mutation time", err)
		}
		mutationTime = mutationTime.UTC()
		nextRevision := command.ExpectedHeadRevision + 1
		var revisionDatabaseID int64
		if err := tx.QueryRow(ctx, `
INSERT INTO ascendany.agent_note_revisions (
    agent_note_id,
    owner_account_id,
    revision_number,
    mutation_id,
    source_kind,
    operation,
    note_state,
    title,
    content,
    content_sha256,
    actor_session_id,
    created_at
)
VALUES ($1, $2, $3, $4::uuid, 'user', $5, $6, $7, $8, $9, $10, $11)
RETURNING agent_note_revision_id`, noteDatabaseID, owner.AccountDatabaseID, nextRevision, command.MutationID,
			string(command.Operation), string(state), title, content, digest, owner.SessionDatabaseID, mutationTime).Scan(&revisionDatabaseID); err != nil {
			return databaseFailure("insert immutable agent note revision", err)
		}
		tag, err := tx.Exec(ctx, `
UPDATE ascendany.agent_notes
SET current_revision_id = $2,
    head_revision = head_revision + 1,
    updated_at = $3
WHERE agent_note_id = $1
  AND owner_account_id = $4
  AND head_revision = $5`, noteDatabaseID, revisionDatabaseID, mutationTime, owner.AccountDatabaseID, command.ExpectedHeadRevision)
		if err != nil {
			return databaseFailure("advance agent note head", err)
		}
		if tag.RowsAffected() != 1 {
			return notesError(ErrorHeadConflict, "advance agent note head", errors.New("agent note head changed concurrently"))
		}
		if err := appendMutationAudit(ctx, tx, owner, command, state, digest, nextRevision, mutationTime); err != nil {
			return err
		}
		stored, _, err := loadOwnedNote(ctx, tx, owner.AccountDatabaseID, command.NoteID, false)
		if err != nil {
			return notesError(ErrorStoredDataInvalid, "load committed agent note mutation", err)
		}
		result = MutationResult{Note: stored}
		return nil
	})
	if err != nil {
		return MutationResult{}, err
	}
	return result, nil
}

func nextDocument(current Note, command UserMutationCommand) (State, string, string, string, error) {
	switch command.Operation {
	case OperationCreate:
		return StateActive, command.Title, command.Content, command.ContentSHA256, nil
	case OperationReplace:
		if current.State != StateActive {
			return "", "", "", "", notesError(ErrorStateConflict, "replace agent note", errors.New("archived note must be restored before replacement"))
		}
		return StateActive, command.Title, command.Content, command.ContentSHA256, nil
	case OperationArchive:
		if current.State != StateActive {
			return "", "", "", "", notesError(ErrorStateConflict, "archive agent note", errors.New("note is already archived"))
		}
		return StateArchived, current.Title, current.Content, current.ContentSHA256, nil
	case OperationRestore:
		if current.State != StateArchived {
			return "", "", "", "", notesError(ErrorStateConflict, "restore agent note", errors.New("note is already active"))
		}
		return StateActive, current.Title, current.Content, current.ContentSHA256, nil
	default:
		return "", "", "", "", notesError(ErrorInvalidQuery, "apply agent note mutation", errors.New("unsupported user operation"))
	}
}

type storedMutation struct {
	NoteID        string
	Revision      int64
	MutationID    string
	Source        SourceKind
	Operation     Operation
	State         State
	Title         string
	Content       string
	ContentSHA256 string
}

func loadMutation(ctx context.Context, tx postgresTx, ownerDatabaseID int64, mutationID string) (storedMutation, bool, error) {
	var stored storedMutation
	err := tx.QueryRow(ctx, `
SELECT note.public_id::text,
       revision.revision_number,
       revision.mutation_id::text,
       revision.source_kind,
       revision.operation,
       revision.note_state,
       revision.title,
       revision.content,
       revision.content_sha256
FROM ascendany.agent_note_revisions AS revision
JOIN ascendany.agent_notes AS note
  ON note.agent_note_id = revision.agent_note_id
 AND note.owner_account_id = revision.owner_account_id
WHERE revision.owner_account_id = $1
  AND revision.mutation_id = $2::uuid`, ownerDatabaseID, mutationID).Scan(
		&stored.NoteID, &stored.Revision, &stored.MutationID, &stored.Source, &stored.Operation,
		&stored.State, &stored.Title, &stored.Content, &stored.ContentSHA256,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return storedMutation{}, false, nil
	}
	if err != nil {
		return storedMutation{}, false, databaseFailure("load agent note mutation", err)
	}
	return stored, true, nil
}

func validateReplay(stored storedMutation, command UserMutationCommand) error {
	expectedState := StateActive
	if command.Operation == OperationArchive {
		expectedState = StateArchived
	}
	matching := stored.MutationID == command.MutationID && stored.Source == SourceUser &&
		stored.Operation == command.Operation && stored.State == expectedState &&
		stored.Revision == command.ExpectedHeadRevision+1
	if command.Operation != OperationCreate {
		matching = matching && stored.NoteID == command.NoteID
	}
	if command.Operation == OperationCreate || command.Operation == OperationReplace {
		matching = matching && stored.Title == command.Title && stored.Content == command.Content &&
			stored.ContentSHA256 == command.ContentSHA256
	}
	if !matching {
		return notesError(ErrorIdempotencyConflict, "validate agent note mutation replay", errors.New("mutation ID was already committed with different immutable input"))
	}
	if digestContent(stored.Content) != stored.ContentSHA256 {
		return notesError(ErrorStoredDataInvalid, "validate agent note mutation replay", errors.New("stored immutable revision digest is invalid"))
	}
	return nil
}

const summarySelect = `
SELECT note.agent_note_id,
       note.public_id::text,
       note.head_revision,
       revision.note_state,
       revision.title,
       revision.content_sha256,
       revision.mutation_id::text,
       revision.operation,
       revision.created_at,
       note.created_at,
       note.updated_at
FROM ascendany.agent_notes AS note
JOIN ascendany.agent_note_revisions AS revision
  ON revision.agent_note_revision_id = note.current_revision_id
 AND revision.agent_note_id = note.agent_note_id
 AND revision.revision_number = note.head_revision
 AND revision.owner_account_id = note.owner_account_id
`

func scanSummary(scanner interface{ Scan(...any) error }) (Summary, int64, error) {
	var summary Summary
	var databaseID int64
	if err := scanner.Scan(
		&databaseID,
		&summary.ID,
		&summary.HeadRevision,
		&summary.State,
		&summary.Title,
		&summary.ContentSHA256,
		&summary.CurrentMutationID,
		&summary.CurrentOperation,
		&summary.CurrentRevisionCreatedAt,
		&summary.CreatedAt,
		&summary.UpdatedAt,
	); err != nil {
		return Summary{}, 0, err
	}
	summary.CurrentRevisionCreatedAt = summary.CurrentRevisionCreatedAt.UTC()
	summary.CreatedAt = summary.CreatedAt.UTC()
	summary.UpdatedAt = summary.UpdatedAt.UTC()
	return summary, databaseID, nil
}

const detailSelect = `
SELECT note.agent_note_id,
       note.public_id::text,
       note.head_revision,
       revision.note_state,
       revision.title,
       revision.content_sha256,
       revision.mutation_id::text,
       revision.operation,
       revision.created_at,
       note.created_at,
       note.updated_at,
       revision.content
FROM ascendany.agent_notes AS note
JOIN ascendany.agent_note_revisions AS revision
  ON revision.agent_note_revision_id = note.current_revision_id
 AND revision.agent_note_id = note.agent_note_id
 AND revision.revision_number = note.head_revision
 AND revision.owner_account_id = note.owner_account_id
`

func loadOwnedNote(ctx context.Context, tx postgresTx, ownerDatabaseID int64, noteID string, lock bool) (Note, int64, error) {
	query := detailSelect + `
WHERE note.owner_account_id = $1
  AND note.public_id = $2::uuid`
	if lock {
		query += ` FOR UPDATE OF note`
	}
	var note Note
	var databaseID int64
	if err := tx.QueryRow(ctx, query, ownerDatabaseID, noteID).Scan(
		&databaseID,
		&note.ID,
		&note.HeadRevision,
		&note.State,
		&note.Title,
		&note.ContentSHA256,
		&note.CurrentMutationID,
		&note.CurrentOperation,
		&note.CurrentRevisionCreatedAt,
		&note.CreatedAt,
		&note.UpdatedAt,
		&note.Content,
	); err != nil {
		return Note{}, 0, err
	}
	note.CurrentRevisionCreatedAt = note.CurrentRevisionCreatedAt.UTC()
	note.CreatedAt = note.CreatedAt.UTC()
	note.UpdatedAt = note.UpdatedAt.UTC()
	return note, databaseID, nil
}

func appendMutationAudit(
	ctx context.Context,
	tx postgresTx,
	owner principalguard.Resolved,
	command UserMutationCommand,
	state State,
	digest string,
	revision int64,
	occurredAt time.Time,
) error {
	payload, err := json.Marshal(map[string]any{
		"noteId":         command.NoteID,
		"mutationId":     command.MutationID,
		"operation":      command.Operation,
		"state":          state,
		"revisionNumber": revision,
		"contentSha256":  digest,
	})
	if err != nil {
		return notesError(ErrorStoredDataInvalid, "encode agent note audit event", err)
	}
	eventType := "student.agent_note." + string(command.Operation)
	if _, err := tx.Exec(ctx, `
INSERT INTO ascendany.audit_events (account_id, session_id, event_type, occurred_at, payload)
VALUES ($1, $2, $3, $4, $5::jsonb)`, owner.AccountDatabaseID, owner.SessionDatabaseID, eventType, occurredAt, string(payload)); err != nil {
		return databaseFailure("append agent note audit event", err)
	}
	return nil
}

func resolveStudent(ctx context.Context, tx postgresTx, principal auth.AccessPrincipal, lock bool) (principalguard.Resolved, error) {
	var resolved principalguard.Resolved
	var err error
	if lock {
		resolved, err = principalguard.ResolveForUpdate(ctx, tx, principal, principalguard.Roles(auth.RoleStudent))
	} else {
		resolved, err = principalguard.Resolve(ctx, tx, principal, principalguard.Roles(auth.RoleStudent))
	}
	if err == nil {
		return resolved, nil
	}
	switch principalguard.CodeOf(err) {
	case principalguard.ErrorInvalidPrincipal:
		return principalguard.Resolved{}, notesError(ErrorInvalidQuery, "revalidate agent notes principal", err)
	case principalguard.ErrorRejected:
		return principalguard.Resolved{}, notesError(ErrorPrincipalRejected, "revalidate agent notes principal", err)
	case principalguard.ErrorStoredData:
		return principalguard.Resolved{}, notesError(ErrorStoredDataInvalid, "revalidate agent notes principal", err)
	case principalguard.ErrorCanceled:
		return principalguard.Resolved{}, notesError(ErrorCanceled, "revalidate agent notes principal", err)
	case principalguard.ErrorDatabase:
		return principalguard.Resolved{}, notesError(ErrorDatabase, "revalidate agent notes principal", err)
	default:
		return principalguard.Resolved{}, notesError(ErrorDatabase, "revalidate agent notes principal", err)
	}
}

func (repository *PostgresRepository) transaction(
	ctx context.Context,
	operation string,
	readOnly bool,
	run func(postgresTx) error,
) (resultErr error) {
	if ctx == nil {
		return notesError(ErrorInvalidQuery, operation, errors.New("context is required"))
	}
	options := pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}
	if !readOnly {
		options = pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadWrite}
	}
	tx, err := repository.begin(ctx, options)
	if err != nil {
		return databaseFailure("begin "+operation, err)
	}
	finished := false
	defer func() {
		if finished {
			return
		}
		rollbackContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if rollbackErr := tx.Rollback(rollbackContext); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			wrapped := databaseFailure("rollback "+operation, rollbackErr)
			if resultErr == nil {
				resultErr = wrapped
			} else {
				resultErr = errors.Join(resultErr, wrapped)
			}
		}
	}()
	if err := run(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return databaseFailure("commit "+operation, err)
	}
	finished = true
	return nil
}
