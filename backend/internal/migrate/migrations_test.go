package migrate

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEmbeddedMigrationsHaveFixedContiguousManifestAndHashes(t *testing.T) {
	t.Parallel()

	definitions, err := Embedded()
	if err != nil {
		t.Fatalf("Embedded() error = %v", err)
	}
	if len(definitions) != 7 ||
		definitions[0].Version != 1 || definitions[0].Name != "fresh_schema" ||
		definitions[1].Version != 2 || definitions[1].Name != "product_domains" ||
		definitions[2].Version != 3 || definitions[2].Name != "recommendation_catalog_contract" ||
		definitions[3].Version != 4 || definitions[3].Name != "achievement_rules" ||
		definitions[4].Version != 5 || definitions[4].Name != "auto_analysis_once" ||
		definitions[5].Version != 6 || definitions[5].Name != "inference_model_runtime" ||
		definitions[6].Version != 7 || definitions[6].Name != "catalog_publication_provenance" {
		t.Fatalf("definitions = %#v", definitions)
	}
	for index := range definitions {
		if definitions[index].SHA256 != embeddedManifest[index].SHA256 {
			t.Fatalf("definition %d hash = %q, manifest = %q", index, definitions[index].SHA256, embeddedManifest[index].SHA256)
		}
	}
	if CurrentVersion() != 7 {
		t.Fatalf("CurrentVersion() = %d", CurrentVersion())
	}
}

func TestCatalogPublicationMigrationOwnsAuthorizedAtomicPublication(t *testing.T) {
	t.Parallel()
	contents, err := os.ReadFile(filepath.Join("migrations", "0007_catalog_publication_provenance.sql"))
	if err != nil {
		t.Fatal(err)
	}
	sql := string(contents)
	for _, fragment := range []string{
		"CREATE TABLE ascendany.knowledge_catalog_publication_authorizations",
		"public_id uuid PRIMARY KEY",
		"access_jwt_id uuid NOT NULL",
		"access_token_sha256 text NOT NULL",
		"request_canonical_json text NOT NULL",
		"catalog_publication_auth_jwt_unique",
		"catalog_publication_auth_request_unique",
		"catalog_publication_authorizations_consumed_publication_fk",
		"CREATE FUNCTION ascendany.enforce_catalog_publication_authorization_transition()",
		"CREATE TRIGGER catalog_publication_authorizations_transition",
		"CREATE TRIGGER catalog_publication_authorizations_immutable_truncate",
		"CREATE TABLE ascendany.knowledge_catalog_publications",
		"SEQUENCE NAME ascendany.knowledge_catalog_publication_ids_seq",
		"publication_authorization_id uuid NOT NULL",
		"knowledge_catalog_publications_auth_unique",
		"knowledge_catalog_publications_authorization_fk",
		"expected_configuration_head_revision",
		"target_model_artifact_sha256",
		"target_application_version",
		"target_application_commit",
		"target_application_build_time",
		"target_model_release_id",
		"knowledge_catalog_publication_id",
		"current_model_artifact_sha256",
		"configuration_mutated",
		"knowledge_catalog_publications_immutable_rows",
		"knowledge_catalog_publications_intent_unique",
		"knowledge_catalog_publications_activation_intent_unique",
		"knowledge_catalog_publications_activation_reference_unique",
		"knowledge_catalog_publications_current_model_activation_fk",
		"knowledge_catalog_publications_audit_event_unique",
		"recommendation_model_activation_events_head_artifact_unique",
		"publication_current_model_head_revision bigint GENERATED ALWAYS AS",
		"recommendation_model_activation_events_catalog_publication_fk",
		"pending_catalog_publication_id bigint",
		"recommendation_model_head_pending_publication_fk",
		"CREATE OR REPLACE FUNCTION ascendany.enforce_recommendation_model_head_transition",
		"model head activation must consume its pending catalog publication",
		"ALTER FUNCTION ascendany.validate_recommendation_model_activation()\nSECURITY DEFINER",
		"ALTER FUNCTION ascendany.validate_recommendation_model_activation()\nSET search_path = pg_catalog",
		"CREATE FUNCTION ascendany.catalog_publication_result(",
		"CREATE FUNCTION ascendany.publish_authorized_knowledge_catalog(",
		"GRANT EXECUTE ON FUNCTION ascendany.publish_authorized_knowledge_catalog(uuid, text, text)",
	} {
		if !strings.Contains(sql, fragment) {
			t.Errorf("catalog publication migration is missing %q", fragment)
		}
	}
	for _, forbidden := range []string{
		"ON DELETE CASCADE",
		"DROP TABLE",
		"IF NOT EXISTS",
		"CREATE FUNCTION ascendany.lock_knowledge_catalog_publication_state",
		"GRANT SELECT ON TABLE ascendany.knowledge_catalog_publications\nTO ascendany_catalog_publisher",
		"GRANT SELECT, INSERT ON TABLE ascendany.knowledge_catalog_publication_authorizations\nTO ascendany_catalog_publisher",
		"ON SEQUENCE ascendany.knowledge_catalog_publication_ids_seq\nTO ascendany_catalog_publisher",
		"RAISE EXCEPTION 'recommendation model head is unavailable'",
	} {
		if strings.Contains(sql, forbidden) {
			t.Errorf("catalog publication migration contains forbidden fragment %q", forbidden)
		}
	}
	publisherGrants := make([]string, 0, 1)
	for _, statement := range strings.Split(sql, ";") {
		if strings.Contains(statement, "GRANT ") && strings.Contains(statement, "TO ascendany_catalog_publisher") {
			publisherGrants = append(publisherGrants, strings.Join(strings.Fields(statement), " "))
		}
	}
	wantPublisherGrant := "GRANT EXECUTE ON FUNCTION ascendany.publish_authorized_knowledge_catalog(uuid, text, text) TO ascendany_catalog_publisher"
	if len(publisherGrants) != 1 || publisherGrants[0] != wantPublisherGrant {
		t.Errorf("catalog publisher migration grants = %#v, want [%q]", publisherGrants, wantPublisherGrant)
	}
	routineStart := strings.Index(sql, "CREATE FUNCTION ascendany.publish_authorized_knowledge_catalog(")
	if routineStart < 0 {
		t.Fatal("catalog publication migration has no atomic publish routine")
	}
	routineEnd := strings.Index(sql[routineStart:], "\n$function$;")
	if routineEnd < 0 {
		t.Fatal("catalog publication atomic routine has no canonical end marker")
	}
	routineSQL := sql[routineStart : routineStart+routineEnd]
	for _, fragment := range []string{
		"authorization_public_id uuid,\n    supplied_access_token_sha256 text,\n    supplied_request_canonical_json text",
		"RETURNS jsonb\nLANGUAGE plpgsql\nSECURITY DEFINER\nSET search_path = pg_catalog",
		"pg_catalog.pg_advisory_xact_lock(4707180034853717324)",
		"FROM ascendany.knowledge_catalog_publication_authorizations AS stored",
		"FOR UPDATE OF stored",
		"current_account_role text",
		"current_account_auth_revision bigint",
		"current_account_disabled_at timestamptz",
		"current_session_auth_revision bigint",
		"current_session_expires_at timestamptz",
		"current_session_revoked_at timestamptz",
		"SELECT account.role,\n           account.auth_revision,\n           account.disabled_at,\n           session.auth_revision,\n           session.expires_at,\n           session.revoked_at",
		"FOR UPDATE OF account, session",
		"current_account_role <> 'admin'",
		"current_account_auth_revision <> capability.authorized_auth_revision",
		"current_account_disabled_at IS NOT NULL",
		"current_session_auth_revision <> capability.authorized_auth_revision",
		"current_session_revoked_at IS NOT NULL",
		"current_session_expires_at <= authorized_use_at",
		"USING ERRCODE = '28000'",
		"UPDATE ascendany.knowledge_catalog_publication_authorizations",
	} {
		if !strings.Contains(routineSQL, fragment) {
			t.Errorf("catalog publication atomic routine is missing %q", fragment)
		}
	}
	advisoryLock := strings.Index(routineSQL, "pg_catalog.pg_advisory_xact_lock(4707180034853717324)")
	authorizationLock := strings.Index(routineSQL, "FOR UPDATE OF stored")
	principalLock := strings.Index(routineSQL, "FOR UPDATE OF account, session")
	modelHeadLock := strings.Index(routineSQL, "FROM ascendany.recommendation_model_head AS head")
	analyticsHeadLock := strings.Index(routineSQL, "FROM ascendany.analytics_head AS head")
	configurationLock := strings.Index(routineSQL, "FROM ascendany.configuration_items AS item")
	if advisoryLock < 0 || authorizationLock <= advisoryLock || principalLock <= authorizationLock ||
		modelHeadLock <= principalLock || analyticsHeadLock <= modelHeadLock || configurationLock <= analyticsHeadLock {
		t.Errorf(
			"catalog publication lock order is advisory=%d authorization=%d principal=%d model=%d analytics=%d configuration=%d",
			advisoryLock,
			authorizationLock,
			principalLock,
			modelHeadLock,
			analyticsHeadLock,
			configurationLock,
		)
	}
}

func TestInferenceModelRuntimeOwnsImmutableActivation(t *testing.T) {
	t.Parallel()

	definitions, err := Embedded()
	if err != nil {
		t.Fatal(err)
	}
	sql := definitions[5].SQL
	for _, fragment := range []string{
		"CREATE TABLE ascendany.recommendation_model_releases",
		"SEQUENCE NAME ascendany.recommendation_model_release_ids_seq",
		"CREATE TABLE ascendany.recommendation_model_activation_events",
		"CREATE TABLE ascendany.recommendation_model_head",
		"model_schema = 'ascendany.recommendation.inference-model.v1'",
		"model_purpose IN ('production', 'acceptance_test')",
		"inference_contract = 'ascendany.recommendation.inference.v1'",
		"recommendation_model_releases_immutable_rows",
		"recommendation_model_activation_events_immutable_rows",
		"CREATE CONSTRAINT TRIGGER recommendation_model_head_activation_complete",
		"GRANT UPDATE (\n    current_release_id,\n    head_revision,\n    updated_at",
	} {
		if !strings.Contains(sql, fragment) {
			t.Errorf("inference model runtime migration is missing %q", fragment)
		}
	}
	for _, forbidden := range []string{
		"recommendation_training",
		"recommendation_trainer",
		"configuration_kind = 'training'",
		"legacy recommendation",
		"DROP TABLE",
		"CREATE TABLE ascendany.student_recommendation_results",
		"ON DELETE CASCADE",
		"GRANT UPDATE ON TABLE",
	} {
		if strings.Contains(sql, forbidden) {
			t.Errorf("inference model runtime migration contains forbidden fragment %q", forbidden)
		}
	}
}

func TestRoleBootstrapBindsTheEmbeddedMigrationManifest(t *testing.T) {
	t.Parallel()

	path := filepath.Join("..", "..", "..", "db", "roles", "001_v2_roles.sql")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read role bootstrap %s: %v", path, err)
	}
	roleBootstrap := string(contents)
	for _, entry := range embeddedManifest {
		expected := fmt.Sprintf("'%d:%s:%s'", entry.Version, entry.Name, entry.SHA256)
		if strings.Count(roleBootstrap, expected) != 1 {
			t.Errorf("role bootstrap migration identity %q count = %d, want 1", expected, strings.Count(roleBootstrap, expected))
		}
	}
}

func TestAutomaticAnalysisMigrationOwnsDedicatedAtMostOnceBoundaries(t *testing.T) {
	t.Parallel()

	definitions, err := Embedded()
	if err != nil {
		t.Fatal(err)
	}
	sql := definitions[4].SQL
	for _, fragment := range []string{
		"ADD COLUMN thread_kind text NOT NULL DEFAULT 'conversation'",
		"thread_kind IN ('conversation', 'auto_analysis')",
		"chat_messages_auto_analysis_content_fixed",
		"CREATE FUNCTION ascendany.enforce_chat_message_thread_kind()",
		"CREATE TRIGGER chat_messages_thread_kind_consistent",
		"chat_threads_owner_auto_analysis_unique",
		"WHERE thread_kind = 'auto_analysis'",
		"agent_runs_owner_analytics_auto_analysis_unique",
		"ON ascendany.agent_runs (owner_account_id, analytics_generation_id)",
		"WHERE run_kind = 'auto_analysis'",
		"CREATE FUNCTION ascendany.enforce_agent_run_thread_kind()",
		"CREATE TRIGGER agent_runs_thread_kind_consistent",
		"CREATE TRIGGER chat_threads_kind_immutable",
	} {
		if !strings.Contains(sql, fragment) {
			t.Errorf("automatic analysis migration is missing %q", fragment)
		}
	}
	for _, forbidden := range []string{"ON DELETE CASCADE", "GRANT UPDATE ON TABLE", "legacy_"} {
		if strings.Contains(sql, forbidden) {
			t.Errorf("automatic analysis migration contains forbidden fragment %q", forbidden)
		}
	}
}

func TestAchievementRulesOwnImmutableVersionedThresholds(t *testing.T) {
	t.Parallel()

	definitions, err := Embedded()
	if err != nil {
		t.Fatal(err)
	}
	sql := definitions[3].SQL
	for _, fragment := range []string{
		"CREATE TABLE ascendany.achievement_rule_sets",
		"CREATE TABLE ascendany.achievement_rules",
		"CREATE TABLE ascendany.achievement_rule_head",
		"CREATE FUNCTION ascendany.enforce_achievement_rule_head_transition()",
		"achievement_rule_sets_immutable_rows",
		"achievement_rules_immutable_rows",
		"GRANT SELECT ON TABLE",
		"TO ascendany_runtime, ascendany_backup",
	} {
		if !strings.Contains(sql, fragment) {
			t.Errorf("achievement rules migration is missing %q", fragment)
		}
	}
	for _, code := range []string{
		"exam_count_first",
		"exam_count_veteran",
		"positive_delta_count",
		"best_positive_streak",
		"ai_dialogue_count",
		"knowledge_max",
		"accuracy_max",
		"quality_max",
		"flexibility_max",
		"proficiency_max",
		"max_rating",
		"max_rating_delta",
		"top10_count",
		"top3_count",
		"max_of_exam_min_metric",
		"rank1_count",
		"current_min_metric",
	} {
		if !strings.Contains(sql, "('"+code+"',") {
			t.Errorf("achievement rules migration is missing seed %q", code)
		}
	}
	if strings.Contains(sql, "any_metric_top1_count") {
		t.Error("achievement rules migration includes an unsupported cross-population rule")
	}
	for _, forbidden := range []string{"student_achievement_states", "student_activity_counters", "ON DELETE CASCADE", "GRANT UPDATE ON TABLE"} {
		if strings.Contains(sql, forbidden) {
			t.Errorf("achievement rules migration contains legacy or broad mutation fragment %q", forbidden)
		}
	}
}

func TestRecommendationCatalogMigrationOwnsDatabaseContract(t *testing.T) {
	t.Parallel()

	definitions, err := Embedded()
	if err != nil {
		t.Fatal(err)
	}
	sql := definitions[2].SQL
	for _, fragment := range []string{
		"configuration_versions_recommendation_catalog_contract",
		"configuration_items_recommendation_catalog_identity",
		"(configuration_kind = 'knowledge_catalog')",
		"= (configuration_key = 'recommendation.catalog.active')",
		"configuration_key = 'recommendation.catalog.active'",
		"schema_id = 'ascendany.knowledge_catalog.recommendation.v1'",
		"AND credential_ref IS NULL",
		"CREATE INDEX recommendation_knowledge_catalog_digest_idx",
		"WHERE configuration_kind = 'knowledge_catalog'",
	} {
		if !strings.Contains(sql, fragment) {
			t.Errorf("recommendation catalog migration is missing %q", fragment)
		}
	}
	for _, forbidden := range []string{"training", "trainer", "CREATE TABLE", "DROP TABLE"} {
		if strings.Contains(sql, forbidden) {
			t.Errorf("recommendation catalog migration contains forbidden runtime %q", forbidden)
		}
	}
}

func TestFreshSchemaSupportsAnalyticsClaimAndExpiredRunningReclaim(t *testing.T) {
	t.Parallel()

	definitions, err := Embedded()
	if err != nil {
		t.Fatalf("Embedded() error = %v", err)
	}
	sql := definitions[0].SQL
	for _, fragment := range []string{
		"attempt_count integer NOT NULL DEFAULT 0",
		"lease_owner text",
		"lease_expires_at timestamptz",
		"next_attempt_at timestamptz NOT NULL DEFAULT clock_timestamp()",
		"analytics_generations_queued_claim_idx",
		"analytics_generations_expired_lease_idx",
	} {
		if !strings.Contains(sql, fragment) {
			t.Errorf("fresh schema is missing analytics lease fragment %q", fragment)
		}
	}
}

func TestFreshSchemaGrantsUpdateOnlyToMutableColumns(t *testing.T) {
	t.Parallel()

	definitions, err := Embedded()
	if err != nil {
		t.Fatalf("Embedded() error = %v", err)
	}
	sql := definitions[0].SQL
	for _, fragment := range []string{
		"GRANT UPDATE (\n    active_snapshot_id,\n    head_revision,\n    updated_at\n) ON TABLE ascendany.logical_exams",
		"GRANT UPDATE (\n    password_phc,\n    display_name,\n    auth_revision,\n    disabled_at,\n    updated_at\n) ON TABLE ascendany.auth_accounts",
		"GRANT UPDATE (\n    status,\n    error_code,\n    error_detail,\n    attempt_count",
	} {
		if !strings.Contains(sql, fragment) {
			t.Errorf("fresh schema is missing column UPDATE grant %q", fragment)
		}
	}
	if strings.Contains(sql, "GRANT UPDATE ON TABLE") {
		t.Error("fresh schema contains a table-level UPDATE grant")
	}
}

func TestFreshSchemaOwnsTheV2ImportAndAnalyticsBoundaries(t *testing.T) {
	t.Parallel()

	definitions, err := Embedded()
	if err != nil {
		t.Fatalf("Embedded() error = %v", err)
	}
	sql := definitions[0].SQL
	required := []string{
		"CREATE TABLE ascendany.schema_migrations_v2",
		"CREATE TABLE ascendany.artifacts",
		"CREATE TABLE ascendany.import_jobs",
		"CREATE TABLE ascendany.import_job_events",
		"CREATE TABLE ascendany.logical_exams",
		"CREATE TABLE ascendany.exam_snapshots",
		"CREATE TABLE ascendany.pintia_actors",
		"CREATE TABLE ascendany.pintia_actor_identifiers",
		"CREATE TABLE ascendany.auth_accounts",
		"CREATE TABLE ascendany.auth_enrollment_grants",
		"CREATE TABLE ascendany.auth_enrollment_events",
		"CREATE TABLE ascendany.auth_sessions",
		"CREATE TABLE ascendany.auth_refresh_tokens",
		"CREATE TABLE ascendany.audit_events",
		"CREATE TABLE ascendany.pintia_snapshot_problems",
		"CREATE TABLE ascendany.pintia_snapshot_participants",
		"CREATE TABLE ascendany.pintia_rankings",
		"CREATE TABLE ascendany.pintia_ranking_problem_results",
		"accept_time_seconds bigint NOT NULL",
		"pintia_ranking_results_accept_time_nonnegative",
		"CREATE TABLE ascendany.pintia_submission_identities",
		"CREATE TABLE ascendany.pintia_snapshot_submissions",
		"CREATE TABLE ascendany.pintia_submission_case_results",
		"CREATE TABLE ascendany.analytics_generations",
		"CREATE TABLE ascendany.analytics_generation_snapshots",
		"CREATE TABLE ascendany.analytics_head",
		"contract_schema = 'ascendany.pintia.snapshot.v2'",
		"domain_hash_protocol = 'domain_hash_proto_v1'",
		"error_permanent boolean",
		"stage text NOT NULL",
	}
	for _, fragment := range required {
		if !strings.Contains(sql, fragment) {
			t.Errorf("fresh schema is missing %q", fragment)
		}
	}
	if got := strings.Count(sql, "public_id uuid NOT NULL UNIQUE"); got != 7 {
		t.Errorf("public UUID declarations = %d, want 7", got)
	}
	for _, forbidden := range []string{"public.schema_migrations", " dirty ", "pintia.snapshot.v1", "accepted_at"} {
		if strings.Contains(sql, forbidden) {
			t.Errorf("fresh schema contains forbidden legacy fragment %q", forbidden)
		}
	}
}

func TestProductDomainsOwnRequiredV2Boundaries(t *testing.T) {
	t.Parallel()

	definitions, err := Embedded()
	if err != nil {
		t.Fatalf("Embedded() error = %v", err)
	}
	sql := definitions[1].SQL
	requiredTables := []string{
		"configuration_items",
		"configuration_versions",
		"analytics_generation_events",
		"chat_threads",
		"chat_messages",
		"agent_runs",
		"agent_run_events",
		"agent_tool_calls",
		"agent_notes",
		"agent_note_revisions",
		"oj_problems",
		"oj_problem_versions",
		"oj_submissions",
		"oj_judge_jobs",
		"oj_judge_job_events",
		"oj_judge_results",
		"feedback_submissions",
		"feedback_attachments",
		"feedback_delivery_jobs",
		"feedback_delivery_events",
	}
	for _, table := range requiredTables {
		if !strings.Contains(sql, "CREATE TABLE ascendany."+table+" (") {
			t.Errorf("product domains migration is missing table %q", table)
		}
	}
	for _, fragment := range []string{
		"CREATE FUNCTION ascendany.enforce_fenced_job_transition()",
		"CREATE FUNCTION ascendany.enforce_initial_queued_job()",
		"CREATE FUNCTION ascendany.enforce_initial_zero_head()",
		"CREATE FUNCTION ascendany.enforce_import_job_transition()",
		"CREATE FUNCTION ascendany.enforce_analytics_generation_transition()",
		"CREATE FUNCTION ascendany.enforce_analytics_head_advance()",
		"CREATE FUNCTION ascendany.validate_agent_run_output()",
		"analytics_generation_events_created_idx",
		"analytics_generations_initial_queue",
		"import_jobs_initial_queue",
		"logical_exams_initial_head",
		"agent_runs_initial_queue",
		"feedback_delivery_jobs_initial_queue",
		"analytics_head_monotonic_advance",
		"pintia_student_number_c_order_idx",
		"student_analytics_rating_finite_v2",
		"audit_events_payload_size",
		"oj_judge_results_job_fk",
		"feedback_submissions_subject_created_idx",
		"'configuration_versions',\n        'analytics_generation_events',",
		"REVOKE ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA ascendany",
	} {
		if !strings.Contains(sql, fragment) {
			t.Errorf("product domains migration is missing %q", fragment)
		}
	}
	for _, forbidden := range []string{
		"CREATE VIEW",
		"CREATE MATERIALIZED VIEW",
		"legacy_",
		"recommendation_training",
		"recommendation_trainer",
		"configuration_kind IN (\n            'prompt',\n            'model_connection',\n            'training'",
		"ON DELETE CASCADE",
		"GRANT UPDATE ON TABLE",
	} {
		if strings.Contains(sql, forbidden) {
			t.Errorf("product domains migration contains forbidden compatibility or broad privilege fragment %q", forbidden)
		}
	}
}

func TestFreshSchemaEnrollmentCredentialsAndStateAreAppendOnly(t *testing.T) {
	t.Parallel()

	definitions, err := Embedded()
	if err != nil {
		t.Fatalf("Embedded() error = %v", err)
	}
	sql := definitions[0].SQL
	for _, fragment := range []string{
		"secret_digest bytea NOT NULL UNIQUE",
		"auth_enrollment_grants_secret_digest_sha256",
		"auth_enrollment_grants_issuer_admin",
		"auth_enrollment_grants_student_identifier_fk",
		"auth_enrollment_grants_issuer_session_fk",
		"auth_enrollment_events_one_issued_one_terminal",
		"auth_enrollment_events_actor_role_consistent",
		"auth_enrollment_events_issued_binding_fk",
		"auth_enrollment_events_consumed_grant_actor_fk",
		"CREATE CONSTRAINT TRIGGER auth_enrollment_grants_complete_state",
		"event_type IN ('issued', 'revoked', 'consumed')",
		"CREATE TRIGGER auth_enrollment_grants_immutable_rows",
		"CREATE TRIGGER auth_enrollment_events_immutable_rows",
		"ascendany.auth_enrollment_grants,\n    ascendany.auth_enrollment_events,",
	} {
		if !strings.Contains(sql, fragment) {
			t.Errorf("fresh schema is missing enrollment fragment %q", fragment)
		}
	}
	for _, forbidden := range []string{
		"enrollment_token text",
		"token_value text",
		"secret_value text",
		"GRANT UPDATE (\n    consumed_at",
		"GRANT UPDATE (\n    revoked_at",
	} {
		if strings.Contains(sql, forbidden) {
			t.Errorf("fresh schema contains mutable or plaintext enrollment fragment %q", forbidden)
		}
	}
}

func TestValidateDefinitionsRejectsGapAndHashDrift(t *testing.T) {
	t.Parallel()

	valid := testDefinition(1, "first", "SELECT 1;\n")
	tests := []struct {
		name        string
		definitions []Definition
		want        string
	}{
		{name: "empty", definitions: nil, want: "at least one"},
		{name: "gap", definitions: []Definition{{Version: 2, Name: valid.Name, SHA256: valid.SHA256, SQL: valid.SQL}}, want: "gap"},
		{name: "name", definitions: []Definition{{Version: 1, Name: "Bad-Name", SHA256: valid.SHA256, SQL: valid.SQL}}, want: "invalid name"},
		{name: "hash format", definitions: []Definition{{Version: 1, Name: valid.Name, SHA256: "xyz", SQL: valid.SQL}}, want: "invalid SHA-256"},
		{name: "hash drift", definitions: []Definition{{Version: 1, Name: valid.Name, SHA256: valid.SHA256, SQL: valid.SQL + " "}}, want: "hash drift"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateDefinitions(test.definitions)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateDefinitions() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestValidateMigrationFilenamesRejectsUnknownOrMissingFiles(t *testing.T) {
	t.Parallel()

	manifest := []HistoryEntry{
		{Version: 1, Name: "fresh_schema", SHA256: strings.Repeat("a", 64)},
		{Version: 2, Name: "product_domains", SHA256: strings.Repeat("b", 64)},
	}
	if err := validateMigrationFilenames([]string{"0001_fresh_schema.sql", "0002_product_domains.sql"}, manifest[:2]); err != nil {
		t.Fatalf("exact file set error = %v", err)
	}
	for _, filenames := range [][]string{
		nil,
		{"0001_fresh_schema.sql"},
		{"0001_fresh_schema.sql", "0002_unknown.sql"},
		{"0001_renamed.sql", "0002_product_domains.sql"},
	} {
		if err := validateMigrationFilenames(filenames, manifest); err == nil {
			t.Fatalf("validateMigrationFilenames(%v) error = nil", filenames)
		}
	}
}

func TestValidateHistoryAcceptsOnlyAnExactPrefix(t *testing.T) {
	t.Parallel()

	definitions := []Definition{
		testDefinition(1, "first", "SELECT 1;\n"),
		testDefinition(2, "second", "SELECT 2;\n"),
	}
	if err := ValidateHistory(nil, definitions); err != nil {
		t.Fatalf("empty prefix error = %v", err)
	}
	if err := ValidateHistory([]HistoryEntry{{
		Version: definitions[0].Version,
		Name:    definitions[0].Name,
		SHA256:  definitions[0].SHA256,
	}}, definitions); err != nil {
		t.Fatalf("exact prefix error = %v", err)
	}

	tests := []struct {
		name    string
		history []HistoryEntry
		want    string
	}{
		{name: "unknown version", history: []HistoryEntry{{Version: 1, Name: definitions[0].Name, SHA256: definitions[0].SHA256}, {Version: 2, Name: definitions[1].Name, SHA256: definitions[1].SHA256}, {Version: 3, Name: "third", SHA256: strings.Repeat("a", 64)}}, want: "binary knows"},
		{name: "gap", history: []HistoryEntry{{Version: 2, Name: definitions[0].Name, SHA256: definitions[0].SHA256}}, want: "contiguous prefix"},
		{name: "name drift", history: []HistoryEntry{{Version: 1, Name: "renamed", SHA256: definitions[0].SHA256}}, want: "name drift"},
		{name: "hash drift", history: []HistoryEntry{{Version: 1, Name: definitions[0].Name, SHA256: strings.Repeat("f", 64)}}, want: "hash drift"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateHistory(test.history, definitions)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ValidateHistory() error = %v, want %q", err, test.want)
			}
		})
	}
}

func testDefinition(version int64, name, sql string) Definition {
	digest := sha256.Sum256([]byte(sql))
	return Definition{
		Version: version,
		Name:    name,
		SHA256:  hex.EncodeToString(digest[:]),
		SQL:     sql,
	}
}
