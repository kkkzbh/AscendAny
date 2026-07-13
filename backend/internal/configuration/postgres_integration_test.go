package configuration

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

func TestPostgresConfigurationVersionLifecycle(t *testing.T) {
	databaseURL := os.Getenv("ASCENDANY_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ASCENDANY_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	principal, found := loadConfigurationAdminPrincipal(t, ctx, pool)
	if !found {
		t.Skip("integration database has no active administrator session")
	}
	repository, err := NewPostgresRepository(pool, acceptingRecommendationDocumentValidator{})
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(repository, acceptingRecommendationDocumentValidator{})
	if err != nil {
		t.Fatal(err)
	}
	identifier, err := randomUUIDv4()
	if err != nil {
		t.Fatal(err)
	}
	key := "test.prompt." + strings.ReplaceAll(identifier, "-", "")
	firstCommand := CreateVersionCommand{
		Principal: principal,
		Key:       key,
		Kind:      KindPrompt,
		SchemaID:  "ascendany.prompt.v1",
		Document:  json.RawMessage(` {"z":2.0,"a":1e0} `),
	}
	first, err := service.CreateVersion(ctx, firstCommand)
	if err != nil {
		t.Fatalf("first CreateVersion() error=%v", err)
	}
	if first.Idempotent || first.Item.HeadRevision != 1 || first.Item.ActiveVersion == nil ||
		first.Item.ActiveVersion.Number != 1 || string(first.Item.ActiveVersion.Document) != `{"a":1,"z":2}` {
		t.Fatalf("first result=%#v", first)
	}

	replay, err := service.CreateVersion(ctx, firstCommand)
	if err != nil || !replay.Idempotent || replay.Item.HeadRevision != 1 || replay.Item.ActiveVersion == nil || replay.Item.ActiveVersion.Number != 1 {
		t.Fatalf("idempotent replay=%#v error=%v", replay, err)
	}

	_, err = service.CreateVersion(ctx, CreateVersionCommand{
		Principal: principal, Key: key, Kind: KindPrompt, ExpectedHeadRevision: 0,
		SchemaID: "ascendany.prompt.v1", Document: json.RawMessage(`{"a":2}`),
	})
	if CodeOf(err) != ErrorHeadConflict {
		t.Fatalf("stale head error=%v", err)
	}

	second, err := service.CreateVersion(ctx, CreateVersionCommand{
		Principal: principal, Key: key, Kind: KindPrompt, ExpectedHeadRevision: 1,
		SchemaID: "ascendany.prompt.v1", Document: json.RawMessage(`{"a":2}`),
	})
	if err != nil || second.Item.HeadRevision != 2 || second.Item.ActiveVersion == nil || second.Item.ActiveVersion.Number != 2 {
		t.Fatalf("second result=%#v error=%v", second, err)
	}

	item, found, err := service.Get(ctx, ItemQuery{Principal: principal, Key: key})
	if err != nil || !found || item.ID != first.Item.ID || item.HeadRevision != 2 {
		t.Fatalf("Get() item=%#v found=%t error=%v", item, found, err)
	}
	kind := KindPrompt
	page, err := service.List(ctx, ListQuery{Principal: principal, Kind: &kind, AfterKey: nil, Limit: MaxPageSize})
	if err != nil {
		t.Fatalf("List() error=%v", err)
	}
	listed := false
	for _, candidate := range page.Items {
		listed = listed || candidate.Key == key
	}
	if !listed {
		t.Fatalf("List() omitted %s", key)
	}
	history, found, err := service.ListVersions(ctx, VersionsQuery{Principal: principal, Key: key, Limit: 10})
	if err != nil || !found || len(history.Items) != 2 || history.Items[0].Number != 2 || history.Items[1].Number != 1 {
		t.Fatalf("ListVersions() page=%#v found=%t error=%v", history, found, err)
	}

	var auditCount int
	if err := pool.QueryRow(ctx, `
SELECT count(*)
FROM ascendany.audit_events
WHERE account_id = (SELECT account_id FROM ascendany.auth_accounts WHERE public_id = $1::uuid)
  AND session_id = (SELECT session_id FROM ascendany.auth_sessions WHERE public_id = $2::uuid)
  AND event_type = 'admin.configuration_version_created'
  AND payload ->> 'configurationId' = $3`, principal.AccountID, principal.SessionID, first.Item.ID).Scan(&auditCount); err != nil {
		t.Fatal(err)
	}
	if auditCount != 2 {
		t.Fatalf("audit count=%d", auditCount)
	}
}

func TestPostgresRejectsReservedCatalogKeyWithWrongKind(t *testing.T) {
	databaseURL := os.Getenv("ASCENDANY_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ASCENDANY_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	identifier, err := randomUUIDv4()
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(ctx, `
INSERT INTO ascendany.configuration_items (public_id, configuration_key, configuration_kind)
VALUES ($1::uuid, $2, 'prompt')`, identifier, KnowledgeCatalogKey)
	var databaseError *pgconn.PgError
	if !errors.As(err, &databaseError) || databaseError.Code != "23514" ||
		databaseError.ConstraintName != "configuration_items_recommendation_catalog_identity" {
		t.Fatalf("reserved catalog key error=%v", err)
	}
}

func loadConfigurationAdminPrincipal(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (auth.AccessPrincipal, bool) {
	t.Helper()
	var principal auth.AccessPrincipal
	var role string
	err := pool.QueryRow(ctx, `
SELECT account.public_id::text,
       session.public_id::text,
       account.role,
       account.auth_revision
FROM ascendany.auth_accounts AS account
JOIN ascendany.auth_sessions AS session
  ON session.account_id = account.account_id
 AND session.auth_revision = account.auth_revision
WHERE account.role = 'admin'
  AND account.disabled_at IS NULL
  AND session.revoked_at IS NULL
  AND session.expires_at > clock_timestamp()
ORDER BY session.session_id DESC
LIMIT 1`).Scan(&principal.AccountID, &principal.SessionID, &role, &principal.AuthRevision)
	if err == pgx.ErrNoRows {
		return auth.AccessPrincipal{}, false
	}
	if err != nil {
		t.Fatal(err)
	}
	principal.Role = auth.Role(role)
	principal.JWTID = "99999999-9999-4999-8999-999999999999"
	return principal, true
}
