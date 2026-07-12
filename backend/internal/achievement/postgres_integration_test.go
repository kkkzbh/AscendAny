package achievement

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kkkzbh/AscendAny/backend/internal/analytics"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

func TestPostgresAchievementReadMatchesCurrentDatabaseSnapshot(t *testing.T) {
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

	principal, accountDatabaseID, actorID, found := loadAchievementIntegrationPrincipal(t, ctx, pool)
	if !found {
		principal, accountDatabaseID, actorID, found = seedAchievementNoObservationPrincipal(t, ctx, pool)
	}
	if !found {
		t.Skip("integration database is non-empty and has no usable active student session")
	}
	var expectedDialogueCount int64
	if err := pool.QueryRow(ctx, `
SELECT count(*)::bigint
FROM ascendany.agent_runs
WHERE owner_account_id = $1
  AND run_kind = 'reply'
  AND status = 'succeeded'`, accountDatabaseID).Scan(&expectedDialogueCount); err != nil {
		t.Fatal(err)
	}
	var generationID *int64
	var expectedHeadRevision int64
	if err := pool.QueryRow(ctx, `
SELECT current_generation_id, head_revision
FROM ascendany.analytics_head
WHERE singleton`).Scan(&generationID, &expectedHeadRevision); err != nil {
		t.Fatal(err)
	}
	expectedState := StateNotGenerated
	if generationID != nil {
		expectedState = StateNoObservations
		var exists bool
		if err := pool.QueryRow(ctx, `
SELECT EXISTS (
    SELECT 1
    FROM ascendany.student_analytics
    WHERE analytics_generation_id = $1
      AND actor_id = $2
)`, *generationID, actorID).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists {
			expectedState = StateReady
		}
	}

	repository, err := NewPostgresRepository(pool)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(repository)
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.GetSelf(ctx, SelfQuery{Principal: principal})
	if err != nil {
		t.Fatalf("GetSelf() error = %v", err)
	}
	if result.State != expectedState || result.AnalyticsHeadRevision != expectedHeadRevision ||
		result.RuleSetVersion != 1 || result.RuleHeadRevision != 1 || len(result.Items) != 17 || result.Summary.Total != 17 {
		t.Fatalf("result metadata = %#v", result)
	}
	foundDialogue := false
	for _, item := range result.Items {
		if item.Code == "any_metric_top1_count" || item.ProgressKey == "any_metric_top1_count" {
			t.Fatal("unsupported cross-population achievement rule is active")
		}
		if item.ProgressKey == ProgressAIDialogueCount {
			foundDialogue = true
			if item.Progress != float64(expectedDialogueCount) {
				t.Fatalf("AI dialogue progress = %v, want %d", item.Progress, expectedDialogueCount)
			}
		}
	}
	if !foundDialogue {
		t.Fatal("active rule set has no AI dialogue rule")
	}
}

func seedAchievementNoObservationPrincipal(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
) (auth.AccessPrincipal, int64, int64, bool) {
	t.Helper()
	var accountCount, generationCount int64
	if err := pool.QueryRow(ctx, `
SELECT (SELECT count(*) FROM ascendany.auth_accounts),
       (SELECT count(*) FROM ascendany.analytics_generations)`).Scan(&accountCount, &generationCount); err != nil {
		t.Fatal(err)
	}
	if accountCount != 0 || generationCount != 0 {
		return auth.AccessPrincipal{}, 0, 0, false
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	var actorID int64
	if err := tx.QueryRow(ctx, `
INSERT INTO ascendany.pintia_actors (user_id)
VALUES ('achievement-integration-user')
RETURNING actor_id`).Scan(&actorID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO ascendany.pintia_actor_identifiers (
    identifier_kind,
    identifier_value,
    actor_id
)
VALUES ('student_number', 'achievement-integration-student', $1)`, actorID); err != nil {
		t.Fatal(err)
	}
	principal := auth.AccessPrincipal{
		AccountID:    "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
		SessionID:    "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
		JWTID:        "cccccccc-cccc-4ccc-8ccc-cccccccccccc",
		Role:         auth.RoleStudent,
		AuthRevision: 1,
	}
	var accountDatabaseID int64
	if err := tx.QueryRow(ctx, `
INSERT INTO ascendany.auth_accounts (
    public_id,
    username,
    password_phc,
    display_name,
    student_number,
    actor_id,
    role,
    auth_revision,
    created_at,
    updated_at
)
VALUES (
    $1::uuid,
    'achievement_integration',
    'integration-password-phc',
    'Achievement Integration',
    'achievement-integration-student',
    $2,
    'student',
    1,
    clock_timestamp(),
    clock_timestamp()
)
RETURNING account_id`, principal.AccountID, actorID).Scan(&accountDatabaseID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO ascendany.auth_sessions (
    public_id,
    account_id,
    auth_revision,
    created_at,
    expires_at,
    last_seen_at
)
VALUES (
    $1::uuid,
    $2,
    1,
    clock_timestamp() - interval '1 minute',
    clock_timestamp() + interval '1 hour',
    clock_timestamp()
)`, principal.SessionID, accountDatabaseID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return principal, accountDatabaseID, actorID, true
}

func loadAchievementIntegrationPrincipal(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
) (auth.AccessPrincipal, int64, int64, bool) {
	t.Helper()
	var principal auth.AccessPrincipal
	var accountDatabaseID, actorID int64
	var role string
	rows, err := pool.Query(ctx, `
SELECT account.account_id,
       account.actor_id,
       account.public_id::text,
       session.public_id::text,
       account.role,
       account.auth_revision,
       student.metrics::text
FROM ascendany.auth_accounts AS account
JOIN ascendany.auth_sessions AS session
  ON session.account_id = account.account_id
 AND session.auth_revision = account.auth_revision
JOIN ascendany.pintia_actor_identifiers AS identifier
  ON identifier.actor_id = account.actor_id
 AND identifier.identifier_kind = 'student_number'
 AND identifier.identifier_value = account.student_number
JOIN ascendany.analytics_head AS head
  ON head.singleton
LEFT JOIN ascendany.student_analytics AS student
  ON student.analytics_generation_id = head.current_generation_id
 AND student.actor_id = account.actor_id
WHERE account.role = 'student'
  AND account.disabled_at IS NULL
  AND session.revoked_at IS NULL
  AND session.expires_at > clock_timestamp()
ORDER BY (student.actor_id IS NULL) DESC, session.session_id DESC`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var metricsJSON *string
		if err := rows.Scan(
			&accountDatabaseID,
			&actorID,
			&principal.AccountID,
			&principal.SessionID,
			&role,
			&principal.AuthRevision,
			&metricsJSON,
		); err != nil {
			t.Fatal(err)
		}
		if metricsJSON != nil {
			if _, err := analytics.DecodeStoredStudentMetrics([]byte(*metricsJSON)); err != nil {
				continue
			}
		}
		principal.Role = auth.Role(role)
		principal.JWTID = "99999999-9999-4999-8999-999999999999"
		return principal, accountDatabaseID, actorID, true
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return auth.AccessPrincipal{}, 0, 0, false
}
