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

func TestReleaseInventoryCoversMigrationObjectClasses(t *testing.T) {
	t.Parallel()
	inventory, err := expectedInventory()
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"relation:r:recommendation_model_releases",
		"sequence:recommendation_model_release_ids_seq",
		"type:recommendation_model_releases",
		"type:_recommendation_model_releases",
		"routine:f:validate_recommendation_model_activation()",
		"trigger:recommendation_model_head_activation_complete",
		"constraint:t:recommendation_model_head_activation_complete",
		"constraint:p:recommendation_model_releases_pkey",
		"constraint:u:recommendation_model_releases_model_id_key",
		"index:recommendation_model_releases_model_id_key",
		"index:recommendation_knowledge_catalog_digest_idx",
		"trigger:agent_note_revisions_immutable_rows",
		"trigger:agent_note_revisions_immutable_truncate",
	} {
		if !containsInventoryKey(inventory, required) {
			t.Errorf("release inventory is missing %q", required)
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
