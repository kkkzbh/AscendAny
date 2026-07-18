package databasecontract

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const verifierMarker = "-- ascendany-go-verifier-begin\n"

type failingACLExecutor struct {
	error error
}

func (executor failingACLExecutor) Exec(
	_ context.Context,
	statement string,
	_ ...any,
) (pgconn.CommandTag, error) {
	if statement == verifierSQL {
		return pgconn.CommandTag{}, executor.error
	}
	return pgconn.CommandTag{}, nil
}

func (failingACLExecutor) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("inventory query must not run after ACL rejection")
}

func TestGeneratedVerifierMatchesCanonicalContract(t *testing.T) {
	t.Parallel()
	path := filepath.Join("..", "..", "..", "db", "roles", "verify_v2_roles.sql")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	_, body, found := strings.Cut(string(contents), verifierMarker)
	if !found {
		t.Fatal("canonical verifier marker is missing")
	}
	if verifierSQL != body {
		t.Fatal("generated Go verifier drifted from db/roles/verify_v2_roles.sql; run go generate ./internal/databasecontract")
	}
	if strings.Contains(verifierSQL, `\set`) || strings.Contains(verifierSQL, `\if`) {
		t.Fatal("generated verifier contains psql-only commands")
	}
}

func TestGeneratedVerifierOwnsExactCatalogPublisherBoundary(t *testing.T) {
	t.Parallel()
	for _, fragment := range []string{
		"publisher_select_tables constant text[] := ARRAY[\n        'schema_migrations_v2'\n    ];",
		"publisher_insert_tables constant text[] := ARRAY[]::text[];",
		"publisher_sequences constant text[] := ARRAY[]::text[];",
		"publisher_update_columns constant text[] := ARRAY[]::text[];",
		"procedure.oid = to_regprocedure('ascendany.publish_authorized_knowledge_catalog(uuid,text,text)')",
		"owner.rolname = 'ascendany_owner'",
		"procedure.prokind = 'f'",
		"procedure.prosecdef",
		"procedure.prorettype = 'jsonb'::regtype",
		"procedure.provolatile = 'v'",
		"procedure.proconfig = ARRAY['search_path=pg_catalog']::text[]",
		"'ascendany_catalog_publisher'::text AS grantee_name",
		"RAISE EXCEPTION 'catalog publisher atomic routine differs from the exact security-definer contract'",
	} {
		if !strings.Contains(verifierSQL, fragment) {
			t.Errorf("generated verifier is missing catalog publisher contract %q", fragment)
		}
	}
	if strings.Contains(verifierSQL, "lock_knowledge_catalog_publication_state") {
		t.Fatal("generated verifier retains the removed catalog publication lock routine")
	}
}

func TestVerifyRejectsACLDriftBeforeInventory(t *testing.T) {
	t.Parallel()
	aclFailure := errors.New("routine ACL entries differ")
	err := Verify(context.Background(), failingACLExecutor{error: aclFailure}, Restore)
	if !errors.Is(err, aclFailure) || !strings.Contains(err.Error(), "ownership and ACL") {
		t.Fatalf("Verify() error = %v", err)
	}
}

func TestVerifyRejectsUnknownProfile(t *testing.T) {
	t.Parallel()
	err := Verify(context.Background(), failingACLExecutor{}, Profile("auto"))
	if err == nil || !strings.Contains(err.Error(), "unknown database verification profile") {
		t.Fatalf("Verify() error = %v", err)
	}
}

func TestReleaseInventoryRejectsExtraSchemaObject(t *testing.T) {
	t.Parallel()
	expected, err := expectedInventory()
	if err != nil {
		t.Fatal(err)
	}
	actual := append(append([]string(nil), expected...), "relation:r:restore_contract_extra")
	err = compareInventory(expected, actual)
	if err == nil || !strings.Contains(err.Error(), `extra="relation:r:restore_contract_extra"`) {
		t.Fatalf("compareInventory() error = %v", err)
	}
}

func TestReleaseInventoryRejectsIdentifiersPostgreSQLWouldTruncate(t *testing.T) {
	t.Parallel()
	err := addInventoryKey(map[string]struct{}{}, "constraint:c:"+strings.Repeat("a", 64))
	if err == nil || !strings.Contains(err.Error(), "exceeds 63 bytes") {
		t.Fatalf("addInventoryKey() error = %v", err)
	}
}

func TestMigrationInventoryReplacesNamedConstraint(t *testing.T) {
	t.Parallel()
	keys := map[string]struct{}{
		"constraint:c:messages_content_valid": {},
	}
	err := addMigrationInventory(keys, `ALTER TABLE ascendany.messages
DROP CONSTRAINT messages_content_valid;

ALTER TABLE ascendany.messages
ADD CONSTRAINT messages_content_valid CHECK (content <> '');`)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := keys["constraint:c:messages_content_valid"]; !exists {
		t.Fatal("replacement constraint is absent from the final inventory")
	}
}

func TestMigrationInventoryDropsConstraintOwnedIndex(t *testing.T) {
	t.Parallel()
	keys := map[string]struct{}{
		"constraint:u:accounts_email_key": {},
		"index:accounts_email_key":        {},
	}
	err := addMigrationInventory(keys, `ALTER TABLE ascendany.accounts
DROP CONSTRAINT accounts_email_key;`)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"constraint:u:accounts_email_key", "index:accounts_email_key"} {
		if _, exists := keys[key]; exists {
			t.Errorf("dropped inventory object remains: %s", key)
		}
	}
}

func TestMigrationInventoryAppliesExplicitIndexTransitionsInOrder(t *testing.T) {
	t.Parallel()
	keys := map[string]struct{}{
		"index:agent_runs_owner_analytics_auto_analysis_unique": {},
	}
	err := addMigrationInventory(keys, `DROP INDEX ascendany.agent_runs_owner_analytics_auto_analysis_unique;

CREATE UNIQUE INDEX agent_runs_owner_exam_role_auto_analysis_unique
ON ascendany.agent_runs (owner_account_id, auto_analysis_exam_id, auto_analysis_role_id)
WHERE kind = 'auto_analysis';`)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := keys["index:agent_runs_owner_analytics_auto_analysis_unique"]; exists {
		t.Fatal("dropped explicit index remains in the final inventory")
	}
	if _, exists := keys["index:agent_runs_owner_exam_role_auto_analysis_unique"]; !exists {
		t.Fatal("replacement explicit index is absent from the final inventory")
	}
}

func TestMigrationInventoryRejectsUnknownDroppedIndex(t *testing.T) {
	t.Parallel()
	err := addMigrationInventory(map[string]struct{}{}, `DROP INDEX ascendany.missing_index;`)
	if err == nil || !strings.Contains(err.Error(), "does not remove an earlier index") {
		t.Fatalf("addMigrationInventory() error = %v", err)
	}
}

func TestMigrationInventoryKeepsAddThenDropConstraintAbsent(t *testing.T) {
	t.Parallel()
	keys := map[string]struct{}{}
	err := addMigrationInventory(keys, `ALTER TABLE ascendany.messages
ADD CONSTRAINT messages_content_valid CHECK (content <> '');

ALTER TABLE ascendany.messages
DROP CONSTRAINT messages_content_valid;`)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := keys["constraint:c:messages_content_valid"]; exists {
		t.Fatal("constraint added then dropped remains in the final inventory")
	}
}

func TestMigrationInventoryRejectsUnknownDroppedConstraint(t *testing.T) {
	t.Parallel()
	err := addMigrationInventory(map[string]struct{}{}, `ALTER TABLE ascendany.messages
DROP CONSTRAINT messages_content_valid;`)
	if err == nil || !strings.Contains(err.Error(), "does not remove an earlier constraint") {
		t.Fatalf("addMigrationInventory() error = %v", err)
	}
}

func TestReleaseInventoryCoversMigrationObjectClasses(t *testing.T) {
	t.Parallel()
	inventory, err := expectedInventory()
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"relation:r:recommendation_model_releases",
		"relation:r:knowledge_catalog_publication_authorizations",
		"relation:r:knowledge_catalog_publications",
		"sequence:recommendation_model_release_ids_seq",
		"sequence:knowledge_catalog_publication_ids_seq",
		"type:recommendation_model_releases",
		"type:_recommendation_model_releases",
		"type:knowledge_catalog_publication_authorizations",
		"type:_knowledge_catalog_publication_authorizations",
		"routine:f:validate_recommendation_model_activation()",
		"routine:f:enforce_catalog_publication_authorization_transition()",
		"routine:f:catalog_publication_result(requested_publication_id bigint, idempotent_result boolean)",
		"routine:f:publish_authorized_knowledge_catalog(authorization_public_id uuid, supplied_access_token_sha256 text, supplied_request_canonical_json text)",
		"trigger:recommendation_model_head_activation_complete",
		"trigger:catalog_publication_authorizations_transition",
		"trigger:catalog_publication_authorizations_immutable_truncate",
		"trigger:knowledge_catalog_publications_immutable_rows",
		"trigger:knowledge_catalog_publications_immutable_truncate",
		"constraint:t:recommendation_model_head_activation_complete",
		"constraint:p:knowledge_catalog_publication_authorizations_pkey",
		"constraint:u:catalog_publication_auth_jwt_unique",
		"constraint:u:catalog_publication_auth_request_unique",
		"constraint:f:catalog_publication_authorizations_consumed_publication_fk",
		"constraint:u:knowledge_catalog_publications_auth_unique",
		"constraint:f:knowledge_catalog_publications_authorization_fk",
		"constraint:u:recommendation_model_activation_events_head_artifact_unique",
		"constraint:u:recommendation_model_release_catalog_identity_unique",
		"constraint:u:recommendation_model_activation_catalog_publication_unique",
		"constraint:f:recommendation_model_head_pending_publication_fk",
		"constraint:u:recommendation_model_head_pending_publication_unique",
		"constraint:c:recommendation_model_activation_catalog_publication_required",
		"constraint:f:recommendation_model_activation_events_catalog_publication_fk",
		"constraint:f:knowledge_catalog_publications_current_model_activation_fk",
		"constraint:u:knowledge_catalog_publications_activation_intent_unique",
		"constraint:u:knowledge_catalog_publications_activation_reference_unique",
		"constraint:u:knowledge_catalog_publications_intent_unique",
		"constraint:u:knowledge_catalog_publications_audit_event_unique",
		"constraint:p:recommendation_model_releases_pkey",
		"constraint:u:recommendation_model_releases_model_id_key",
		"index:recommendation_model_releases_model_id_key",
		"index:recommendation_knowledge_catalog_digest_idx",
		"index:agent_runs_owner_exam_role_auto_analysis_unique",
		"trigger:agent_note_revisions_immutable_rows",
		"trigger:agent_note_revisions_immutable_truncate",
	} {
		if !containsInventoryKey(inventory, required) {
			t.Errorf("release inventory is missing %q", required)
		}
	}
	for _, removed := range []string{
		"routine:f:lock_knowledge_catalog_publication_state(account_public_id uuid, session_public_id uuid, principal_auth_revision bigint)",
		"sequence:knowledge_catalog_publication_authorization_ids_seq",
		"trigger:knowledge_catalog_publication_authorizations_immutable_rows",
		"constraint:u:knowledge_catalog_publications_publication_authorization_id_key",
		"index:agent_runs_owner_analytics_auto_analysis_unique",
	} {
		if containsInventoryKey(inventory, removed) {
			t.Errorf("release inventory retains removed migration object %q", removed)
		}
	}
}

func TestPostgresReleaseContractIntegration(t *testing.T) {
	databaseURL := os.Getenv("ASCENDANY_DATABASE_CONTRACT_TEST_URL")
	if databaseURL == "" {
		t.Skip("ASCENDANY_DATABASE_CONTRACT_TEST_URL is unset")
	}
	ctx := context.Background()
	connection, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close(context.Background())
	transaction, err := connection.BeginTx(ctx, pgx.TxOptions{
		IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.Rollback(context.Background())
	if err := Verify(ctx, transaction, SourceSnapshot); err != nil {
		t.Fatal(err)
	}
}

func containsInventoryKey(inventory []string, expected string) bool {
	for _, key := range inventory {
		if key == expected {
			return true
		}
	}
	return false
}
