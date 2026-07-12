package feedback

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

func TestPostgresAuthenticatedFeedbackIsIdempotentAndRateLimited(t *testing.T) {
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

	principal, configurationKey := seedFeedbackPrincipalAndConfiguration(t, ctx, pool)
	repository, err := NewPostgresRepository(pool)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(repository, Policy{
		Window:                   time.Hour,
		MaximumSubmissions:       2,
		DeliveryConfigurationKey: configurationKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	input := SubmitInput{
		Principal:       principal,
		ClientRequestID: integrationUUID(t),
		Title:           "Feedback integration",
		Content:         "The delivery job must be durable.",
	}
	first, err := service.SubmitAuthenticated(ctx, input)
	if err != nil || !first.Created {
		t.Fatalf("first submission=%#v error=%v", first, err)
	}
	replayed, err := service.SubmitAuthenticated(ctx, input)
	if err != nil || replayed.Created || replayed.Submission != first.Submission {
		t.Fatalf("replayed submission=%#v error=%v", replayed, err)
	}
	worker, err := NewDeliveryWorker(repository, deliveryProviderFunc(func(context.Context, DeliveryRequest) ([]byte, error) {
		return []byte("integration-receipt"), nil
	}), DeliveryWorkerConfig{
		Owner:         "feedback-integration",
		LeaseDuration: time.Minute,
		RetryDelay:    time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	deliveryOutcome, err := worker.RunOne(ctx)
	if err != nil || deliveryOutcome == nil || deliveryOutcome.JobID != first.Submission.DeliveryJobID ||
		deliveryOutcome.Disposition != DeliverySucceeded || deliveryOutcome.ReceiptSHA256 == nil {
		t.Fatalf("delivery outcome=%#v error=%v", deliveryOutcome, err)
	}
	changed := input
	changed.Content = "A different request body."
	if _, err := service.SubmitAuthenticated(ctx, changed); CodeOf(err) != ErrorIdempotencyConflict {
		t.Fatalf("changed replay error=%v code=%q", err, CodeOf(err))
	}
	second := input
	second.ClientRequestID = integrationUUID(t)
	second.Title = "Second feedback"
	secondResult, err := service.SubmitAuthenticated(ctx, second)
	if err != nil || !secondResult.Created {
		t.Fatalf("second submission=%#v error=%v", secondResult, err)
	}
	staleClaim, err := repository.ClaimDelivery(ctx, "feedback-stale", integrationUUID(t), time.Minute)
	if err != nil || staleClaim == nil || staleClaim.ID != secondResult.Submission.DeliveryJobID {
		t.Fatalf("stale claim=%#v error=%v", staleClaim, err)
	}
	if _, err := pool.Exec(ctx, `
UPDATE ascendany.feedback_delivery_jobs
SET lease_expires_at = clock_timestamp() - interval '1 second'
WHERE feedback_delivery_job_id = $1`, staleClaim.DatabaseID); err != nil {
		t.Fatal(err)
	}
	activeClaim, err := repository.ClaimDelivery(ctx, "feedback-active", integrationUUID(t), time.Minute)
	if err != nil || activeClaim == nil || activeClaim.ID != staleClaim.ID || !activeClaim.Reclaimed || activeClaim.AttemptCount != 2 {
		t.Fatalf("active claim=%#v error=%v", activeClaim, err)
	}
	if err := repository.CompleteDelivery(ctx, *staleClaim, strings.Repeat("a", 64)); CodeOf(err) != ErrorLeaseLost {
		t.Fatalf("stale completion error=%v code=%q", err, CodeOf(err))
	}
	if outcome, err := worker.Process(ctx, *activeClaim); err != nil || outcome.Disposition != DeliverySucceeded {
		t.Fatalf("reclaimed delivery outcome=%#v error=%v", outcome, err)
	}
	third := second
	third.ClientRequestID = integrationUUID(t)
	third.Title = "Third feedback"
	if _, err := service.SubmitAuthenticated(ctx, third); CodeOf(err) != ErrorRateLimited {
		t.Fatalf("third submission error=%v code=%q", err, CodeOf(err))
	}

	var jobStatus string
	var eventCount int
	if err := pool.QueryRow(ctx, `
SELECT job.status, count(event.event_sequence)
FROM ascendany.feedback_submissions AS feedback
JOIN ascendany.feedback_delivery_jobs AS job ON job.feedback_id = feedback.feedback_id
JOIN ascendany.feedback_delivery_events AS event ON event.feedback_delivery_job_id = job.feedback_delivery_job_id
WHERE feedback.public_id = $1::uuid
GROUP BY job.status`, first.Submission.ID).Scan(&jobStatus, &eventCount); err != nil {
		t.Fatal(err)
	}
	if jobStatus != "succeeded" || eventCount != 3 {
		t.Fatalf("job status=%q eventCount=%d", jobStatus, eventCount)
	}
}

type deliveryProviderFunc func(context.Context, DeliveryRequest) ([]byte, error)

func (provider deliveryProviderFunc) Deliver(ctx context.Context, request DeliveryRequest) ([]byte, error) {
	return provider(ctx, request)
}

func seedFeedbackPrincipalAndConfiguration(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
) (auth.AccessPrincipal, string) {
	t.Helper()
	accountID := integrationUUID(t)
	sessionID := integrationUUID(t)
	jwtID := integrationUUID(t)
	suffix := randomIntegrationHex(t, 6)
	username := "feedback_" + suffix
	var accountDatabaseID int64
	if err := pool.QueryRow(ctx, `
INSERT INTO ascendany.auth_accounts (
    public_id, username, password_phc, display_name, role, auth_revision, created_at, updated_at
)
VALUES ($1::uuid, $2, '$argon2id$v=19$m=65536,t=3,p=1$test$test', 'Feedback Admin', 'admin', 1,
        clock_timestamp(), clock_timestamp())
RETURNING account_id`, accountID, username).Scan(&accountDatabaseID); err != nil {
		t.Fatal(err)
	}
	var sessionDatabaseID int64
	if err := pool.QueryRow(ctx, `
INSERT INTO ascendany.auth_sessions (
    public_id, account_id, auth_revision, created_at, expires_at, last_seen_at
)
VALUES ($1::uuid, $2, 1, clock_timestamp(), clock_timestamp() + interval '1 hour', clock_timestamp())
RETURNING session_id`, sessionID, accountDatabaseID).Scan(&sessionDatabaseID); err != nil {
		t.Fatal(err)
	}

	key := "feedback.delivery." + suffix
	var itemDatabaseID int64
	if err := pool.QueryRow(ctx, `
INSERT INTO ascendany.configuration_items (public_id, configuration_key, configuration_kind)
VALUES ($1::uuid, $2, 'feedback_delivery')
RETURNING configuration_item_id`, integrationUUID(t), key).Scan(&itemDatabaseID); err != nil {
		t.Fatal(err)
	}
	document := []byte(`{"provider":"integration"}`)
	documentHash := sha256.Sum256(document)
	var versionDatabaseID int64
	if err := pool.QueryRow(ctx, `
INSERT INTO ascendany.configuration_versions (
    configuration_item_id, configuration_kind, version_number, schema_id,
    document, document_sha256, credential_ref,
    created_by_account_id, created_by_session_id
)
VALUES ($1, 'feedback_delivery', 1, 'ascendany.feedback-delivery.v1', $2::jsonb, $3, 'feedback.integration', $4, $5)
RETURNING configuration_version_id`, itemDatabaseID, string(document), hex.EncodeToString(documentHash[:]), accountDatabaseID, sessionDatabaseID).Scan(&versionDatabaseID); err != nil {
		t.Fatal(err)
	}
	if commandTag, err := pool.Exec(ctx, `
UPDATE ascendany.configuration_items
SET active_version_id = $2, head_revision = 1, updated_at = clock_timestamp()
WHERE configuration_item_id = $1 AND head_revision = 0`, itemDatabaseID, versionDatabaseID); err != nil || commandTag.RowsAffected() != 1 {
		t.Fatalf("activate feedback configuration rows=%d error=%v", commandTag.RowsAffected(), err)
	}
	return auth.AccessPrincipal{
		AccountID:    accountID,
		SessionID:    sessionID,
		JWTID:        jwtID,
		Role:         auth.RoleAdmin,
		AuthRevision: 1,
	}, key
}

func integrationUUID(t *testing.T) string {
	t.Helper()
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		t.Fatal(err)
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(value)
	return fmt.Sprintf("%s-%s-%s-%s-%s", encoded[0:8], encoded[8:12], encoded[12:16], encoded[16:20], encoded[20:32])
}

func randomIntegrationHex(t *testing.T, size int) string {
	t.Helper()
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(value)
}
