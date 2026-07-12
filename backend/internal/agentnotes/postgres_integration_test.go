package agentnotes

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

func TestPostgresAgentNoteOwnedLifecycleAndFencing(t *testing.T) {
	databaseURL := os.Getenv("ASCENDANY_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ASCENDANY_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	principal := seedIntegrationStudent(t, ctx, pool)
	otherPrincipal := seedIntegrationStudent(t, ctx, pool)
	repository, err := NewPostgresRepository(pool)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(repository)
	if err != nil {
		t.Fatal(err)
	}

	createMutation := mustIntegrationUUID(t)
	createCommand := CreateCommand{
		Principal: principal, MutationID: createMutation, ExpectedHeadRevision: 0,
		Title: "Integration plan", Content: "Review graphs\nSolve two problems",
	}
	created, err := service.Create(ctx, createCommand)
	if err != nil {
		t.Fatal(err)
	}
	if created.Idempotent || created.Note.HeadRevision != 1 || created.Note.State != StateActive ||
		created.Note.ContentSHA256 != digestContent(createCommand.Content) {
		t.Fatalf("created=%#v", created)
	}
	replayed, err := service.Create(ctx, createCommand)
	if err != nil || !replayed.Idempotent || replayed.Note.ID != created.Note.ID || replayed.Note.HeadRevision != 1 {
		t.Fatalf("replayed=%#v error=%v", replayed, err)
	}
	changedReplay := createCommand
	changedReplay.Content = "different content"
	if _, err := service.Create(ctx, changedReplay); CodeOf(err) != ErrorIdempotencyConflict {
		t.Fatalf("changed replay error=%v", err)
	}

	replaceMutation := mustIntegrationUUID(t)
	replaced, err := service.Replace(ctx, ReplaceCommand{
		Principal: principal, NoteID: created.Note.ID, MutationID: replaceMutation,
		ExpectedHeadRevision: 1, Title: "Integration plan v2", Content: "Review trees",
	})
	if err != nil || replaced.Note.HeadRevision != 2 || replaced.Note.CurrentOperation != OperationReplace {
		t.Fatalf("replaced=%#v error=%v", replaced, err)
	}
	if _, err := service.Replace(ctx, ReplaceCommand{
		Principal: principal, NoteID: created.Note.ID, MutationID: mustIntegrationUUID(t),
		ExpectedHeadRevision: 1, Title: "Stale", Content: "stale",
	}); CodeOf(err) != ErrorHeadConflict {
		t.Fatalf("stale head error=%v", err)
	}

	archiveMutation := mustIntegrationUUID(t)
	archived, err := service.Archive(ctx, StateCommand{
		Principal: principal, NoteID: created.Note.ID, MutationID: archiveMutation, ExpectedHeadRevision: 2,
	})
	if err != nil || archived.Note.HeadRevision != 3 || archived.Note.State != StateArchived ||
		archived.Note.Content != replaced.Note.Content || archived.Note.ContentSHA256 != replaced.Note.ContentSHA256 {
		t.Fatalf("archived=%#v error=%v", archived, err)
	}
	if _, err := service.Replace(ctx, ReplaceCommand{
		Principal: principal, NoteID: created.Note.ID, MutationID: mustIntegrationUUID(t),
		ExpectedHeadRevision: 3, Title: "Archived edit", Content: "forbidden",
	}); CodeOf(err) != ErrorStateConflict {
		t.Fatalf("archived replace error=%v", err)
	}

	restoreMutation := mustIntegrationUUID(t)
	restored, err := service.Restore(ctx, StateCommand{
		Principal: principal, NoteID: created.Note.ID, MutationID: restoreMutation, ExpectedHeadRevision: 3,
	})
	if err != nil || restored.Note.HeadRevision != 4 || restored.Note.State != StateActive ||
		restored.Note.Content != replaced.Note.Content || restored.Note.ContentSHA256 != replaced.Note.ContentSHA256 {
		t.Fatalf("restored=%#v error=%v", restored, err)
	}
	lateArchiveReplay, err := service.Archive(ctx, StateCommand{
		Principal: principal, NoteID: created.Note.ID, MutationID: archiveMutation, ExpectedHeadRevision: 2,
	})
	if err != nil || !lateArchiveReplay.Idempotent || lateArchiveReplay.Note.HeadRevision != 4 || lateArchiveReplay.Note.State != StateActive {
		t.Fatalf("late archive replay=%#v error=%v", lateArchiveReplay, err)
	}

	detail, found, err := service.Get(ctx, DetailQuery{Principal: principal, NoteID: created.Note.ID})
	if err != nil || !found || detail.HeadRevision != 4 || detail.Content != "Review trees" {
		t.Fatalf("detail=%#v found=%t error=%v", detail, found, err)
	}
	if _, found, err := service.Get(ctx, DetailQuery{Principal: otherPrincipal, NoteID: created.Note.ID}); err != nil || found {
		t.Fatalf("cross-owner get found=%t error=%v", found, err)
	}
	if _, err := service.Archive(ctx, StateCommand{
		Principal: otherPrincipal, NoteID: created.Note.ID, MutationID: mustIntegrationUUID(t), ExpectedHeadRevision: 4,
	}); CodeOf(err) != ErrorNotFound {
		t.Fatalf("cross-owner mutation error=%v", err)
	}

	concurrentNote, err := service.Create(ctx, CreateCommand{
		Principal: principal, MutationID: mustIntegrationUUID(t), Title: "CAS note", Content: "head one",
	})
	if err != nil {
		t.Fatal(err)
	}
	mutations := []string{mustIntegrationUUID(t), mustIntegrationUUID(t)}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wait sync.WaitGroup
	for index := range 2 {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			_, mutationErr := service.Replace(ctx, ReplaceCommand{
				Principal: principal, NoteID: concurrentNote.Note.ID, MutationID: mutations[index],
				ExpectedHeadRevision: 1, Title: "CAS winner", Content: "candidate " + string(rune('A'+index)),
			})
			results <- mutationErr
		}(index)
	}
	close(start)
	wait.Wait()
	close(results)
	var successful, conflicted int
	for mutationErr := range results {
		switch CodeOf(mutationErr) {
		case "":
			successful++
		case ErrorHeadConflict:
			conflicted++
		default:
			t.Fatalf("unexpected concurrent mutation error=%v", mutationErr)
		}
	}
	if successful != 1 || conflicted != 1 {
		t.Fatalf("concurrent mutations successful=%d conflicted=%d", successful, conflicted)
	}

	third, err := service.Create(ctx, CreateCommand{
		Principal: principal, MutationID: mustIntegrationUUID(t), Title: "Third note", Content: "pagination",
	})
	if err != nil {
		t.Fatal(err)
	}
	firstPage, err := service.List(ctx, ListQuery{Principal: principal, Limit: 2})
	if err != nil || len(firstPage.Items) != 2 || firstPage.NextCursor == nil {
		t.Fatalf("first page=%#v error=%v", firstPage, err)
	}
	secondPage, err := service.List(ctx, ListQuery{Principal: principal, Cursor: firstPage.NextCursor, Limit: 2})
	if err != nil || len(secondPage.Items) != 1 || secondPage.NextCursor != nil {
		t.Fatalf("second page=%#v error=%v", secondPage, err)
	}
	if _, err := service.List(ctx, ListQuery{Principal: otherPrincipal, Cursor: firstPage.NextCursor, Limit: 2}); CodeOf(err) != ErrorCursorInvalid {
		t.Fatalf("cross-owner cursor error=%v", err)
	}
	seen := map[string]bool{}
	for _, item := range append(firstPage.Items, secondPage.Items...) {
		if seen[item.ID] {
			t.Fatalf("pagination duplicated note %s", item.ID)
		}
		seen[item.ID] = true
	}
	if !seen[created.Note.ID] || !seen[concurrentNote.Note.ID] || !seen[third.Note.ID] {
		t.Fatalf("pagination omitted owned notes: %#v", seen)
	}

	var revisionCount int
	var operations []string
	rows, err := pool.Query(ctx, `
SELECT revision.operation
FROM ascendany.agent_note_revisions AS revision
JOIN ascendany.agent_notes AS note ON note.agent_note_id = revision.agent_note_id
WHERE note.public_id = $1::uuid
ORDER BY revision.revision_number`, created.Note.ID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var operation string
		if err := rows.Scan(&operation); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		operations = append(operations, operation)
		revisionCount++
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	rows.Close()
	if revisionCount != 4 || strings.Join(operations, ",") != "create,replace,archive,restore" {
		t.Fatalf("revision operations=%v", operations)
	}
	rows, err = pool.Query(ctx, `
SELECT revision.content, revision.content_sha256, revision.source_kind,
       revision.agent_run_id, revision.agent_tool_call_id
FROM ascendany.agent_note_revisions AS revision
JOIN ascendany.agent_notes AS note ON note.agent_note_id = revision.agent_note_id
WHERE note.public_id = $1::uuid`, created.Note.ID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var content, digest, source string
		var runID, toolID *int64
		if err := rows.Scan(&content, &digest, &source, &runID, &toolID); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		if digestContent(content) != digest || source != "user" || runID != nil || toolID != nil {
			rows.Close()
			t.Fatalf("invalid revision source/hash source=%q digest=%q", source, digest)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatal(err)
	}
	rows.Close()

	var auditCount int
	if err := pool.QueryRow(ctx, `
SELECT count(*)
FROM ascendany.audit_events
WHERE account_id = (SELECT account_id FROM ascendany.auth_accounts WHERE public_id = $1::uuid)
  AND payload ->> 'noteId' = $2
  AND event_type LIKE 'student.agent_note.%'`, principal.AccountID, created.Note.ID).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 4 {
		t.Fatalf("audit count=%d", auditCount)
	}

	_, err = pool.Exec(ctx, `
UPDATE ascendany.agent_note_revisions
SET title = title
WHERE agent_note_id = (SELECT agent_note_id FROM ascendany.agent_notes WHERE public_id = $1::uuid)
  AND revision_number = 1`, created.Note.ID)
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != "42501" {
		t.Fatalf("immutable revision update error=%v", err)
	}
}

func seedIntegrationStudent(t *testing.T, ctx context.Context, pool *pgxpool.Pool) auth.AccessPrincipal {
	t.Helper()
	accountID := mustIntegrationUUID(t)
	sessionID := mustIntegrationUUID(t)
	suffix := strings.ReplaceAll(accountID, "-", "")[:12]
	studentNumber := "notes-student-" + suffix
	now := time.Now().UTC().Truncate(time.Microsecond)
	var actorDatabaseID int64
	if err := pool.QueryRow(ctx, `
INSERT INTO ascendany.pintia_actors (user_id)
VALUES ($1)
RETURNING actor_id`, "notes-user-"+suffix).Scan(&actorDatabaseID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO ascendany.pintia_actor_identifiers (identifier_kind, identifier_value, actor_id)
VALUES ('student_number', $1, $2)`, studentNumber, actorDatabaseID); err != nil {
		t.Fatal(err)
	}
	var accountDatabaseID int64
	if err := pool.QueryRow(ctx, `
INSERT INTO ascendany.auth_accounts (
    public_id, username, password_phc, display_name, student_number, actor_id,
    role, auth_revision, created_at, updated_at
)
VALUES ($1::uuid, $2, 'integration-unused-password', $3, $4, $5, 'student', 1, $6, $6)
RETURNING account_id`, accountID, "notes_"+suffix, "Notes "+suffix, studentNumber, actorDatabaseID, now).Scan(&accountDatabaseID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO ascendany.auth_sessions (
    public_id, account_id, auth_revision, created_at, expires_at, last_seen_at
)
VALUES ($1::uuid, $2, 1, $3, $4, $3)`, sessionID, accountDatabaseID, now, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	return auth.AccessPrincipal{
		AccountID: accountID, SessionID: sessionID, JWTID: mustIntegrationUUID(t),
		Role: auth.RoleStudent, AuthRevision: 1,
	}
}

func mustIntegrationUUID(t *testing.T) string {
	t.Helper()
	identifier, err := randomUUIDv4()
	if err != nil {
		t.Fatal(err)
	}
	return identifier
}
