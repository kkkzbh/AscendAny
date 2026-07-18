package migrate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

const preservedUpgradePassword = "local-rehearsal-password"

type preservedUpgradeFixture struct {
	studentAccountID int64
	studentSessionID int64
	adminAccountID   int64
	adminSessionID   int64
	promptVersionID  int64
	modelVersionID   int64
	feedbackID       int64
	feedbackArtifact int64
}

func TestPostgresSchemaSevenUpgradePreservesDataAndContracts(t *testing.T) {
	databaseURL := os.Getenv("ASCENDANY_MIGRATE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ASCENDANY_MIGRATE_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	definitions, err := Embedded()
	if err != nil {
		t.Fatal(err)
	}
	if len(definitions) != 10 || len(embeddedManifest) != 10 {
		t.Fatalf("migration manifest length = %d/%d, want 10/10", len(definitions), len(embeddedManifest))
	}
	for index := range definitions {
		if definitions[index].Version != int64(index+1) ||
			definitions[index].Version != embeddedManifest[index].Version ||
			definitions[index].Name != embeddedManifest[index].Name ||
			definitions[index].SHA256 != embeddedManifest[index].SHA256 {
			t.Fatalf("migration manifest entry %d differs: definition=%#v history=%#v", index, definitions[index], embeddedManifest[index])
		}
	}

	configuration := Config{
		DatabaseURL: databaseURL, Password: preservedUpgradePassword,
		LockTimeout: 5 * time.Second, ConnectTimeout: 5 * time.Second,
	}
	connection := openPreservedUpgradeConnection(t, ctx, databaseURL)
	if err := apply(ctx, connection, configuration.LockTimeout, definitions[:7]); err != nil {
		t.Fatalf("apply schema 1..7: %v", err)
	}
	assertPreservedUpgradeHistory(t, ctx, connection, embeddedManifest[:7])
	fixture := seedPreservedUpgradeFixture(t, ctx, connection)
	assertPreservedUpgradeConstraints(t, ctx, connection)
	before := loadPreservedUpgradeFingerprint(t, ctx, connection)
	assertSchemaSevenRejectsDuplicateFeedbackArtifact(t, ctx, connection, fixture)
	assertMigrationEightPreflight(t, ctx, connection, definitions[7], fixture)
	if err := connection.Close(ctx); err != nil {
		t.Fatal(err)
	}

	if err := Up(ctx, configuration); err != nil {
		t.Fatalf("upgrade schema 7 to 10: %v", err)
	}
	connection = openPreservedUpgradeConnection(t, ctx, databaseURL)
	assertPreservedUpgradeHistory(t, ctx, connection, embeddedManifest)
	assertPreservedUpgradeConstraints(t, ctx, connection)
	after := loadPreservedUpgradeFingerprint(t, ctx, connection)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("preserved schema-7 data changed during upgrade\nbefore=%#v\nafter=%#v", before, after)
	}
	assertMigrationEightBehavior(t, ctx, connection, fixture)
	assertMigrationNineBehavior(t, ctx, connection)
	assertMigrationTenBehavior(t, ctx, connection, fixture)
	postBehavior := loadPreservedUpgradeFingerprint(t, ctx, connection)
	if err := connection.Close(ctx); err != nil {
		t.Fatal(err)
	}

	if err := Up(ctx, configuration); err != nil {
		t.Fatalf("idempotent current-schema Up: %v", err)
	}
	connection = openPreservedUpgradeConnection(t, ctx, databaseURL)
	defer connection.Close(context.Background())
	assertPreservedUpgradeHistory(t, ctx, connection, embeddedManifest)
	if replayed := loadPreservedUpgradeFingerprint(t, ctx, connection); !reflect.DeepEqual(replayed, postBehavior) {
		t.Fatalf("idempotent Up changed data\nbefore=%#v\nafter=%#v", postBehavior, replayed)
	}
}

func openPreservedUpgradeConnection(t *testing.T, ctx context.Context, databaseURL string) *pgx.Conn {
	t.Helper()
	configuration, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	configuration.Password = preservedUpgradePassword
	configuration.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	connection, err := pgx.ConnectConfig(ctx, configuration)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyAndAssumeOwner(ctx, connection); err != nil {
		connection.Close(context.Background())
		t.Fatal(err)
	}
	return connection
}

func seedPreservedUpgradeFixture(t *testing.T, ctx context.Context, connection *pgx.Conn) preservedUpgradeFixture {
	t.Helper()
	tx, err := connection.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())

	fixture := preservedUpgradeFixture{}
	var actorID int64
	mustQueryPreservedUpgradeID(t, ctx, tx, &actorID, `
INSERT INTO ascendany.pintia_actors (user_id)
VALUES ('schema-seven-student')
RETURNING actor_id`)
	mustExecPreservedUpgrade(t, ctx, tx, `
INSERT INTO ascendany.pintia_actor_identifiers (identifier_kind, identifier_value, actor_id)
VALUES ('student_number', '20260001', $1)`, actorID)

	mustQueryPreservedUpgradeID(t, ctx, tx, &fixture.studentAccountID, `
INSERT INTO ascendany.auth_accounts (
    public_id, username, password_phc, display_name, student_number, actor_id,
    role, auth_revision, created_at, updated_at
)
VALUES (
    '71000000-0000-4000-8000-000000000001'::uuid,
    'upgrade_student', 'fixture-phc', 'Upgrade Student', '20260001', $1,
    'student', 3, '2026-07-01T00:00:00Z', '2026-07-01T00:00:00Z'
)
RETURNING account_id`, actorID)
	mustQueryPreservedUpgradeID(t, ctx, tx, &fixture.adminAccountID, `
INSERT INTO ascendany.auth_accounts (
    public_id, username, password_phc, display_name, role, auth_revision, created_at, updated_at
)
VALUES (
    '71000000-0000-4000-8000-000000000002'::uuid,
    'upgrade_admin', 'fixture-phc', 'Upgrade Admin', 'admin', 2,
    '2026-07-01T00:00:00Z', '2026-07-01T00:00:00Z'
)
RETURNING account_id`)
	mustQueryPreservedUpgradeID(t, ctx, tx, &fixture.studentSessionID, `
INSERT INTO ascendany.auth_sessions (
    public_id, account_id, auth_revision, created_at, expires_at, last_seen_at
)
VALUES (
    '71000000-0000-4000-8000-000000000003'::uuid, $1, 3,
    '2026-07-01T00:00:00Z', '2027-07-01T00:00:00Z', '2026-07-01T00:05:00Z'
)
RETURNING session_id`, fixture.studentAccountID)
	mustQueryPreservedUpgradeID(t, ctx, tx, &fixture.adminSessionID, `
INSERT INTO ascendany.auth_sessions (
    public_id, account_id, auth_revision, created_at, expires_at, last_seen_at
)
VALUES (
    '71000000-0000-4000-8000-000000000004'::uuid, $1, 2,
    '2026-07-01T00:00:00Z', '2027-07-01T00:00:00Z', '2026-07-01T00:05:00Z'
)
RETURNING session_id`, fixture.adminAccountID)

	fixture.promptVersionID = seedPreservedConfiguration(t, ctx, tx,
		"71000000-0000-4000-8000-000000000010", "agent.prompt.upgrade", "prompt",
		"ascendany.prompt.integration.v1", `{"system":"preserved prompt"}`, nil, fixture)
	credentialRef := "agent.model.upgrade"
	fixture.modelVersionID = seedPreservedConfiguration(t, ctx, tx,
		"71000000-0000-4000-8000-000000000011", "agent.model.upgrade", "model_connection",
		"ascendany.model-connection.integration.v1", `{"model":"preserved-model"}`, &credentialRef, fixture)
	catalogDocument := `{"knowledgePoints":[{"description":"Preserved","id":"arrays","label":"Arrays","prerequisiteIds":[]}],"problemAssignments":[],"taxonomyId":"schema-seven-upgrade"}`
	catalogDigest := preservedUpgradeSHA256(catalogDocument)
	seedPreservedConfiguration(t, ctx, tx,
		"71000000-0000-4000-8000-000000000012", "recommendation.catalog.active", "knowledge_catalog",
		"ascendany.knowledge_catalog.recommendation.v1", catalogDocument, nil, fixture)

	mustExecPreservedUpgrade(t, ctx, tx, `
INSERT INTO ascendany.logical_exams (public_id, platform, source_exam_id)
VALUES ('71000000-0000-4000-8000-000000000020'::uuid, 'pintia', 'schema-seven-exam')`)

	var threadID int64
	mustQueryPreservedUpgradeID(t, ctx, tx, &threadID, `
INSERT INTO ascendany.chat_threads (public_id, owner_account_id, thread_kind)
VALUES ('71000000-0000-4000-8000-000000000030'::uuid, $1, 'conversation')
RETURNING chat_thread_id`, fixture.studentAccountID)
	var messageID int64
	mustQueryPreservedUpgradeID(t, ctx, tx, &messageID, `
INSERT INTO ascendany.chat_messages (
    public_id, chat_thread_id, owner_account_id, message_sequence,
    message_kind, content, author_session_id
)
VALUES (
    '71000000-0000-4000-8000-000000000031'::uuid, $1, $2, 1,
    'user', 'Preserve this reply request.', $3
)
RETURNING chat_message_id`, threadID, fixture.studentAccountID, fixture.studentSessionID)
	mustExecPreservedUpgrade(t, ctx, tx, `
INSERT INTO ascendany.agent_runs (
    public_id, chat_thread_id, owner_account_id, request_session_id, client_request_id,
    run_kind, input_message_id, input_message_kind,
    prompt_configuration_version_id, model_configuration_version_id, status
)
VALUES (
    '71000000-0000-4000-8000-000000000032'::uuid, $1, $2, $3,
    '71000000-0000-4000-8000-000000000033'::uuid,
    'reply', $4, 'user', $5, $6, 'queued'
)`, threadID, fixture.studentAccountID, fixture.studentSessionID, messageID,
		fixture.promptVersionID, fixture.modelVersionID)

	modelArtifactSHA := strings.Repeat("c", 64)
	var modelReleaseID int64
	mustQueryPreservedUpgradeID(t, ctx, tx, &modelReleaseID, `
INSERT INTO ascendany.recommendation_model_releases (
    model_id, artifact_sha256, artifact_size_bytes, artifact_mode,
    model_schema, model_purpose, algorithm, inference_contract, trained_at,
    training_provenance_sha256, feature_schema_sha256, knowledge_catalog_sha256,
    parameter_sha256, golden_vectors_sha256, manifest, manifest_sha256
)
VALUES (
    '71000000-0000-4000-8000-000000000040'::uuid, $1, 128, 420,
    'ascendany.recommendation.inference-model.v1', 'acceptance_test',
    'knowledge_mirt_feature_v1', 'ascendany.recommendation.inference.v1',
    '2026-06-30T00:00:00Z', $2, $3, $4, $5, $6,
    '{"fixture":"schema-seven"}'::jsonb, $7
)
RETURNING recommendation_model_release_id`, modelArtifactSHA, strings.Repeat("d", 64),
		strings.Repeat("e", 64), catalogDigest, strings.Repeat("f", 64), strings.Repeat("1", 64), strings.Repeat("2", 64))
	mustExecPreservedUpgrade(t, ctx, tx, `
INSERT INTO ascendany.recommendation_model_activation_events (
    head_revision, recommendation_model_release_id, artifact_sha256,
    application_version, application_commit, application_build_time, activated_at
)
VALUES (1, $1, $2, '0.2.14', 'preserved-commit', '2026-07-01T00:00:00Z', '2026-07-01T00:00:00Z')`,
		modelReleaseID, modelArtifactSHA)
	mustExecPreservedUpgrade(t, ctx, tx, `
INSERT INTO ascendany.recommendation_model_head (singleton, current_release_id, head_revision, updated_at)
VALUES (true, $1, 1, '2026-07-01T00:00:00Z')`, modelReleaseID)

	feedbackDigest := strings.Repeat("b", 64)
	mustQueryPreservedUpgradeID(t, ctx, tx, &fixture.feedbackArtifact, `
INSERT INTO ascendany.artifacts (sha256, size_bytes, media_type, storage_key)
VALUES ($1, 64, 'image/png', 'sha256/' || substr($1, 1, 2) || '/' || $1)
RETURNING artifact_id`, feedbackDigest)
	mustQueryPreservedUpgradeID(t, ctx, tx, &fixture.feedbackID, `
INSERT INTO ascendany.feedback_submissions (
    public_id, submission_mode, account_id, session_id, rate_limit_subject_digest,
    client_request_id, title, content, platform, app_version, user_agent
)
VALUES (
    '71000000-0000-4000-8000-000000000050'::uuid, 'authenticated', $1, $2,
    decode($3, 'hex'), '71000000-0000-4000-8000-000000000051'::uuid,
    'Preserved feedback', 'Feedback body preserved through the upgrade.',
    'desktop', '0.2.14', 'schema-seven-integration'
)
RETURNING feedback_id`, fixture.studentAccountID, fixture.studentSessionID, strings.Repeat("a", 64))
	mustExecPreservedUpgrade(t, ctx, tx, `
INSERT INTO ascendany.feedback_attachments (feedback_id, attachment_sequence, artifact_id, filename)
VALUES ($1, 1, $2, 'preserved.png')`, fixture.feedbackID, fixture.feedbackArtifact)

	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func seedPreservedConfiguration(
	t *testing.T,
	ctx context.Context,
	tx pgx.Tx,
	publicID, key, kind, schemaID, document string,
	credentialRef *string,
	fixture preservedUpgradeFixture,
) int64 {
	t.Helper()
	var itemID int64
	mustQueryPreservedUpgradeID(t, ctx, tx, &itemID, `
INSERT INTO ascendany.configuration_items (public_id, configuration_key, configuration_kind)
VALUES ($1::uuid, $2, $3)
RETURNING configuration_item_id`, publicID, key, kind)
	var versionID int64
	mustQueryPreservedUpgradeID(t, ctx, tx, &versionID, `
INSERT INTO ascendany.configuration_versions (
    configuration_item_id, configuration_kind, version_number, schema_id,
    document, document_sha256, credential_ref, created_by_account_id, created_by_session_id
)
VALUES ($1, $2, 1, $3, $4::jsonb, $5, $6, $7, $8)
RETURNING configuration_version_id`, itemID, kind, schemaID, document, preservedUpgradeSHA256(document),
		credentialRef, fixture.adminAccountID, fixture.adminSessionID)
	mustExecPreservedUpgrade(t, ctx, tx, `
UPDATE ascendany.configuration_items
SET active_version_id = $2, head_revision = 1, updated_at = clock_timestamp()
WHERE configuration_item_id = $1`, itemID, versionID)
	return versionID
}

func assertMigrationEightPreflight(
	t *testing.T,
	ctx context.Context,
	connection *pgx.Conn,
	migration Definition,
	fixture preservedUpgradeFixture,
) {
	t.Helper()
	tx, err := connection.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	var threadID int64
	mustQueryPreservedUpgradeID(t, ctx, tx, &threadID, `
INSERT INTO ascendany.chat_threads (public_id, owner_account_id, thread_kind)
VALUES ('71000000-0000-4000-8000-000000000060'::uuid, $1, 'auto_analysis')
RETURNING chat_thread_id`, fixture.studentAccountID)
	mustExecPreservedUpgrade(t, ctx, tx, `
INSERT INTO ascendany.chat_messages (
    public_id, chat_thread_id, owner_account_id, message_sequence,
    message_kind, content, author_session_id
)
VALUES (
    '71000000-0000-4000-8000-000000000061'::uuid, $1, $2, 1,
    'auto_analysis_request',
    'Analyze the student''s current published analytics snapshot and provide a concise, actionable progress review.',
    $3
)`, threadID, fixture.studentAccountID, fixture.studentSessionID)
	_, err = tx.Exec(ctx, migration.SQL)
	assertPostgresCode(t, err, "P0001")
	if !strings.Contains(err.Error(), "requires zero existing automatic analysis requests") {
		t.Fatalf("migration 8 preflight error = %v", err)
	}
}

func assertMigrationEightBehavior(t *testing.T, ctx context.Context, connection *pgx.Conn, fixture preservedUpgradeFixture) {
	t.Helper()
	var replyRows int64
	if err := connection.QueryRow(ctx, `
SELECT count(*)
FROM ascendany.agent_runs
WHERE public_id = '71000000-0000-4000-8000-000000000032'::uuid
  AND run_kind = 'reply'
  AND auto_analysis_exam_id IS NULL
  AND auto_analysis_role_id IS NULL`).Scan(&replyRows); err != nil || replyRows != 1 {
		t.Fatalf("preserved reply identity rows=%d error=%v", replyRows, err)
	}
	assertPreservedIndex(t, ctx, connection, "agent_runs_owner_exam_role_auto_analysis_unique", true)
	assertPreservedIndex(t, ctx, connection, "agent_runs_owner_analytics_auto_analysis_unique", false)

	var threadID int64
	if err := connection.QueryRow(ctx, `
INSERT INTO ascendany.chat_threads (public_id, owner_account_id, thread_kind)
VALUES ('71000000-0000-4000-8000-000000000062'::uuid, $1, 'auto_analysis')
RETURNING chat_thread_id`, fixture.studentAccountID).Scan(&threadID); err != nil {
		t.Fatal(err)
	}
	content := preservedFrontendContext(t)
	if _, err := connection.Exec(ctx, `
INSERT INTO ascendany.chat_messages (
    public_id, chat_thread_id, owner_account_id, message_sequence,
    message_kind, content, author_session_id
)
VALUES ('71000000-0000-4000-8000-000000000063'::uuid, $1, $2, 1,
        'auto_analysis_request', $3, $4)`, threadID, fixture.studentAccountID, content, fixture.studentSessionID); err != nil {
		t.Fatalf("insert valid frontend context: %v", err)
	}
	_, err := connection.Exec(ctx, `
INSERT INTO ascendany.chat_messages (
    public_id, chat_thread_id, owner_account_id, message_sequence,
    message_kind, content, author_session_id
)
VALUES ('71000000-0000-4000-8000-000000000064'::uuid, $1, $2, 2,
        'auto_analysis_request',
        'Analyze the student''s current published analytics snapshot and provide a concise, actionable progress review.',
        $3)`, threadID, fixture.studentAccountID, fixture.studentSessionID)
	assertPostgresCode(t, err, "23514")
}

func assertMigrationNineBehavior(t *testing.T, ctx context.Context, connection *pgx.Conn) {
	t.Helper()
	var nickname *string
	if err := connection.QueryRow(ctx, `
SELECT pta_nickname FROM ascendany.auth_accounts WHERE username = 'upgrade_student'`).Scan(&nickname); err != nil {
		t.Fatal(err)
	}
	if nickname != nil {
		t.Fatalf("backfilled PTA nickname = %q, want NULL", *nickname)
	}
	if _, err := connection.Exec(ctx, `
UPDATE ascendany.auth_accounts SET pta_nickname = 'UpgradeNick' WHERE username = 'upgrade_student'`); err != nil {
		t.Fatal(err)
	}
	_, err := connection.Exec(ctx, `
UPDATE ascendany.auth_accounts SET pta_nickname = 'AdminNick' WHERE username = 'upgrade_admin'`)
	assertPostgresCode(t, err, "23514")
	_, err = connection.Exec(ctx, `
UPDATE ascendany.auth_accounts SET pta_nickname = ' padded ' WHERE username = 'upgrade_student'`)
	assertPostgresCode(t, err, "23514")
}

func assertMigrationTenBehavior(
	t *testing.T,
	ctx context.Context,
	connection *pgx.Conn,
	fixture preservedUpgradeFixture,
) {
	t.Helper()
	if _, err := connection.Exec(ctx, `
INSERT INTO ascendany.feedback_attachments (feedback_id, attachment_sequence, artifact_id, filename)
VALUES ($1, 2, $2, 'preserved-copy.png')`, fixture.feedbackID, fixture.feedbackArtifact); err != nil {
		t.Fatalf("insert repeated feedback artifact after migration 10: %v", err)
	}
	var count int64
	if err := connection.QueryRow(ctx, `
SELECT count(*) FROM ascendany.feedback_attachments WHERE feedback_id = $1`, fixture.feedbackID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("feedback attachment count=%d error=%v", count, err)
	}
	_, err := connection.Exec(ctx, `
INSERT INTO ascendany.feedback_attachments (feedback_id, attachment_sequence, artifact_id, filename)
VALUES ($1, 1, $2, 'duplicate-sequence.png')`, fixture.feedbackID, fixture.feedbackArtifact)
	assertPostgresCode(t, err, "23505")
	_, err = connection.Exec(ctx, `
INSERT INTO ascendany.feedback_attachments (feedback_id, attachment_sequence, artifact_id, filename)
VALUES ($1, 3, 9223372036854775807, 'missing-artifact.png')`, fixture.feedbackID)
	assertPostgresCode(t, err, "23503")
	_, err = connection.Exec(ctx, `
INSERT INTO ascendany.feedback_attachments (feedback_id, attachment_sequence, artifact_id, filename)
VALUES ($1, 9, $2, 'out-of-range.png')`, fixture.feedbackID, fixture.feedbackArtifact)
	assertPostgresCode(t, err, "23514")
}

func assertSchemaSevenRejectsDuplicateFeedbackArtifact(
	t *testing.T,
	ctx context.Context,
	connection *pgx.Conn,
	fixture preservedUpgradeFixture,
) {
	t.Helper()
	_, err := connection.Exec(ctx, `
INSERT INTO ascendany.feedback_attachments (feedback_id, attachment_sequence, artifact_id, filename)
VALUES ($1, 2, $2, 'schema-seven-copy.png')`, fixture.feedbackID, fixture.feedbackArtifact)
	assertPostgresCode(t, err, "23505")
}

func assertPreservedUpgradeHistory(t *testing.T, ctx context.Context, connection *pgx.Conn, want []HistoryEntry) {
	t.Helper()
	history, err := readHistory(ctx, connection)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(history, want) {
		t.Fatalf("migration history=%#v, want %#v", history, want)
	}
}

func assertPreservedUpgradeConstraints(t *testing.T, ctx context.Context, connection *pgx.Conn) {
	t.Helper()
	expected := []struct {
		name string
		kind string
	}{
		{"auth_sessions_account_fk", "f"},
		{"logical_exams_source_unique", "u"},
		{"configuration_items_active_version_fk", "f"},
		{"chat_messages_thread_owner_fk", "f"},
		{"chat_messages_thread_sequence_unique", "u"},
		{"recommendation_model_head_release_fk", "f"},
		{"feedback_attachments_feedback_fk", "f"},
		{"feedback_attachments_artifact_fk", "f"},
		{"feedback_attachments_pkey", "p"},
	}
	for _, item := range expected {
		var kind string
		var validated bool
		err := connection.QueryRow(ctx, `
SELECT constraint_type::text, convalidated
FROM (
    SELECT CASE contype WHEN 'f' THEN 'f' WHEN 'u' THEN 'u' WHEN 'p' THEN 'p' ELSE contype::text END AS constraint_type,
           convalidated
    FROM pg_constraint
    WHERE connamespace = 'ascendany'::regnamespace AND conname = $1
) AS constraint_state`, item.name).Scan(&kind, &validated)
		if err != nil || kind != item.kind || !validated {
			t.Fatalf("constraint %s kind=%q validated=%v error=%v", item.name, kind, validated, err)
		}
	}
}

func assertPreservedIndex(t *testing.T, ctx context.Context, connection *pgx.Conn, name string, want bool) {
	t.Helper()
	var present bool
	if err := connection.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1
    FROM pg_index AS index_state
    JOIN pg_class AS index_relation ON index_relation.oid = index_state.indexrelid
    JOIN pg_namespace AS namespace ON namespace.oid = index_relation.relnamespace
    WHERE namespace.nspname = 'ascendany'
      AND index_relation.relname = $1
      AND index_state.indisunique
      AND index_state.indisvalid
)`, name).Scan(&present); err != nil {
		t.Fatal(err)
	}
	if present != want {
		t.Fatalf("index %s present=%v, want %v", name, present, want)
	}
}

func loadPreservedUpgradeFingerprint(t *testing.T, ctx context.Context, connection *pgx.Conn) map[string]string {
	t.Helper()
	tables := []string{
		"artifacts", "pintia_actors", "pintia_actor_identifiers", "auth_accounts", "auth_sessions",
		"logical_exams", "analytics_head", "configuration_items", "configuration_versions",
		"recommendation_model_releases", "recommendation_model_activation_events", "recommendation_model_head",
		"chat_threads", "chat_messages", "agent_runs", "feedback_submissions", "feedback_attachments",
	}
	result := make(map[string]string, len(tables))
	for _, table := range tables {
		expression := "to_jsonb(row_value)"
		switch table {
		case "auth_accounts":
			expression += " - 'pta_nickname'"
		case "agent_runs":
			expression += " - ARRAY['auto_analysis_exam_id', 'auto_analysis_role_id']::text[]"
		}
		query := fmt.Sprintf(`
SELECT COALESCE(
    jsonb_agg((%s) ORDER BY (%s)::text),
    '[]'::jsonb
)::text
FROM ascendany.%s AS row_value`, expression, expression, table)
		var fingerprint string
		if err := connection.QueryRow(ctx, query).Scan(&fingerprint); err != nil {
			t.Fatalf("fingerprint %s: %v", table, err)
		}
		result[table] = fingerprint
	}
	return result
}

func preservedFrontendContext(t *testing.T) string {
	t.Helper()
	value := map[string]any{
		"schema":      "ascendany.agent.auto-analysis.frontend-context.v1",
		"instruction": "Analyze the student's current published analytics snapshot and provide a concise, actionable progress review.",
		"context": map[string]any{
			"studentId": "20260001", "ptaNickname": "UpgradeNick",
			"roleId": "coach", "roleName": "Coach", "roleSystemPrompt": "Give concise guidance.",
			"latestExamId": "71000000-0000-4000-8000-000000000020",
			"notes":        "Preserved notes", "notesTitle": "Upgrade", "notesLocked": true,
		},
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func mustExecPreservedUpgrade(t *testing.T, ctx context.Context, tx pgx.Tx, query string, arguments ...any) {
	t.Helper()
	if _, err := tx.Exec(ctx, query, arguments...); err != nil {
		t.Fatal(err)
	}
}

func mustQueryPreservedUpgradeID(
	t *testing.T,
	ctx context.Context,
	tx pgx.Tx,
	destination *int64,
	query string,
	arguments ...any,
) {
	t.Helper()
	if err := tx.QueryRow(ctx, query, arguments...).Scan(destination); err != nil {
		t.Fatal(err)
	}
}

func preservedUpgradeSHA256(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}
