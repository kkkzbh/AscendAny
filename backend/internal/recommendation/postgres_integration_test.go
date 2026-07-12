package recommendation

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/kkkzbh/AscendAny/backend/internal/artifact"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
	configurationdomain "github.com/kkkzbh/AscendAny/backend/internal/configuration"
)

type analyticsAdvancingQueueRepository struct {
	QueueRepository
	advance func()
}

func (repository analyticsAdvancingQueueRepository) QueueTraining(ctx context.Context, command QueueCommand) (QueueResult, error) {
	repository.advance()
	return repository.QueueRepository.QueueTraining(ctx, command)
}

func TestPostgresRecommendationOperatorBootstrapReviewQueueAndEvents(t *testing.T) {
	databaseURL := os.Getenv("ASCENDANY_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ASCENDANY_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	fixture := seedRecommendationFixture(t, ctx, pool)
	repository, err := NewPostgresRepository(pool)
	if err != nil {
		t.Fatal(err)
	}
	adminReader, err := NewAdminReaderService(repository)
	if err != nil {
		t.Fatal(err)
	}
	generation := publishFixtureAnalyticsGeneration(t, ctx, pool, fixture)
	review, err := adminReader.ReadReviewContext(ctx, fixture.AdminPrincipal)
	if err != nil || review.AnalyticsGenerationID != generation.ID || review.AnalyticsHeadRevision != generation.Revision || len(review.Problems) != 2 {
		t.Fatalf("bootstrap review=%#v error=%v", review, err)
	}

	assignments := make([]any, len(review.Problems))
	for index, problem := range review.Problems {
		assignments[index] = map[string]any{
			"platform": problem.Platform, "problemId": problem.ProblemID, "problemFactSha256": problem.ProblemFactSHA256,
			"knowledge": []any{map[string]any{"knowledgePointId": "fundamentals", "weight": 1}},
		}
	}
	catalogDocument, _ := canonicalRaw(t, map[string]any{
		"taxonomyId": "recommendation.bootstrap." + fmt.Sprint(time.Now().UnixNano()),
		"knowledgePoints": []any{map[string]any{
			"id": "fundamentals", "label": "Fundamentals", "description": "Reviewed problem fundamentals", "prerequisiteIds": []any{},
		}},
		"problemAssignments": assignments,
	})
	configurationRepository, err := configurationdomain.NewPostgresRepository(pool)
	if err != nil {
		t.Fatal(err)
	}
	configurationService, err := configurationdomain.NewService(configurationRepository, ConfigurationDocumentValidator{})
	if err != nil {
		t.Fatal(err)
	}
	suffix := fmt.Sprint(time.Now().UnixNano())
	catalogResult, err := configurationService.CreateVersion(ctx, configurationdomain.CreateVersionCommand{
		Principal: fixture.AdminPrincipal, Key: "recommendation.bootstrap.catalog." + suffix,
		Kind: configurationdomain.KindKnowledgeCatalog, SchemaID: knowledgeCatalogSchemaV1, Document: catalogDocument,
	})
	if err != nil || catalogResult.Item.ActiveVersion == nil || catalogResult.Item.ActiveVersion.ID == "" {
		t.Fatalf("bootstrap catalog=%#v error=%v", catalogResult, err)
	}
	trainingDocument, _ := canonicalRaw(t, map[string]any{
		"algorithm": trainingAlgorithmV2, "knowledgeCatalogVersionId": catalogResult.Item.ActiveVersion.ID, "accelerator": "cuda",
		"seed": 2026, "epochs": 4, "patience": 2, "batchSize": 2, "learningRate": 0.01,
		"weightDecay": 0.001, "minTrainInteractions": 2, "minActorInteractions": 2, "minProblemInteractions": 1,
		"validation":     map[string]any{"minActors": 2, "minInteractions": 2, "minRelativeLogLossImprovement": 0},
		"pathPolicy":     map[string]any{"targetMastery": 0.8, "maxKnowledgeTargets": 1, "minSteps": 2, "maxSteps": 4, "problemsPerStep": 2, "targetSuccessProbability": 0.7},
		"rankingWeights": map[string]any{"knowledgeGap": 1, "successDistance": 1},
	})
	trainingKey := "recommendation.bootstrap.training." + suffix
	trainingResult, err := configurationService.CreateVersion(ctx, configurationdomain.CreateVersionCommand{
		Principal: fixture.AdminPrincipal, Key: trainingKey, Kind: configurationdomain.KindTraining,
		SchemaID: trainingConfigurationSchemaV2, Document: trainingDocument,
	})
	if err != nil || trainingResult.Item.ActiveVersion == nil || trainingResult.Item.ActiveVersion.ID == "" {
		t.Fatalf("bootstrap training=%#v error=%v", trainingResult, err)
	}
	store, err := artifact.NewStore(filepath.Join(t.TempDir(), "artifacts"), 32<<20)
	if err != nil {
		t.Fatal(err)
	}
	staleDataset, err := repository.PrepareTraining(ctx, fixture.AdminPrincipal, trainingKey)
	if err != nil {
		t.Fatal(err)
	}
	staleBundle, err := BuildInputBundle(staleDataset, 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	racingQueue, err := NewQueueService(analyticsAdvancingQueueRepository{
		QueueRepository: repository,
		advance:         func() { publishFixtureAnalyticsGeneration(t, ctx, pool, fixture) },
	}, store, ServiceConfig{MaximumInputBundleBytes: 8 << 20})
	if err != nil {
		t.Fatal(err)
	}
	_, err = racingQueue.QueueTraining(ctx, QueueInput{
		Principal: fixture.AdminPrincipal, ConfigurationKey: trainingKey,
		ExpectedAnalyticsGenerationID: review.AnalyticsGenerationID, ExpectedAnalyticsHeadRevision: review.AnalyticsHeadRevision,
	})
	var headConflict *AnalyticsHeadConflict
	if CodeOf(err) != ErrorStateConflict || !errors.As(err, &headConflict) || headConflict.ExpectedGenerationID != review.AnalyticsGenerationID {
		t.Fatalf("bootstrap TOCTOU error=%v code=%q conflict=%#v", err, CodeOf(err), headConflict)
	}
	republished, err := store.Publish(ctx, bytes.NewReader(staleBundle.CanonicalJSON))
	if err != nil {
		t.Fatalf("stale artifact lock was not released after queue conflict: %v", err)
	}
	if err := republished.Release(); err != nil {
		t.Fatal(err)
	}
	review, err = adminReader.ReadReviewContext(ctx, fixture.AdminPrincipal)
	if err != nil || review.AnalyticsGenerationID == generation.ID {
		t.Fatalf("advanced review=%#v error=%v", review, err)
	}
	queue, err := NewQueueService(repository, store, ServiceConfig{MaximumInputBundleBytes: 8 << 20})
	if err != nil {
		t.Fatal(err)
	}
	queued, err := queue.QueueTraining(ctx, QueueInput{
		Principal: fixture.AdminPrincipal, ConfigurationKey: trainingKey,
		ExpectedAnalyticsGenerationID: review.AnalyticsGenerationID, ExpectedAnalyticsHeadRevision: review.AnalyticsHeadRevision,
	})
	if err != nil || !queued.Created {
		t.Fatalf("bootstrap queue=%#v error=%v", queued, err)
	}
	detail, found, err := adminReader.ReadTrainingRun(ctx, fixture.AdminPrincipal, queued.Run.ID)
	if err != nil || !found || detail.TrainingConfigurationKey != trainingKey {
		t.Fatalf("bootstrap run=%#v found=%t error=%v", detail, found, err)
	}
	events, found, err := adminReader.ReadTrainingEvents(ctx, fixture.AdminPrincipal, queued.Run.ID, 0, 100)
	if err != nil || !found || len(events.Items) != 1 || events.Items[0].Type != "queued" ||
		!bytes.Contains(events.Items[0].Payload, []byte(`"configurationVersionId":"`)) {
		t.Fatalf("bootstrap events=%#v found=%t error=%v", events, found, err)
	}
}

func TestPostgresRecommendationTrainingLifecycleAndStudentFreshness(t *testing.T) {
	databaseURL := os.Getenv("ASCENDANY_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ASCENDANY_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		t.Fatal(err)
	}

	fixture := seedRecommendationFixture(t, ctx, pool)
	repository, err := NewPostgresRepository(pool)
	if err != nil {
		t.Fatal(err)
	}
	store, err := artifact.NewStore(filepath.Join(t.TempDir(), "artifacts"), 32<<20)
	if err != nil {
		t.Fatal(err)
	}
	queueService, err := NewQueueService(repository, store, ServiceConfig{MaximumInputBundleBytes: 8 << 20})
	if err != nil {
		t.Fatal(err)
	}
	readerService, err := NewReaderService(repository)
	if err != nil {
		t.Fatal(err)
	}
	adminReaderService, err := NewAdminReaderService(repository)
	if err != nil {
		t.Fatal(err)
	}

	firstGeneration := publishFixtureAnalyticsGeneration(t, ctx, pool, fixture)
	review, err := adminReaderService.ReadReviewContext(ctx, fixture.AdminPrincipal)
	if err != nil || review.AnalyticsGenerationID != firstGeneration.ID || review.AnalyticsHeadRevision != firstGeneration.Revision || len(review.Problems) != 2 {
		t.Fatalf("review context=%#v error=%v", review, err)
	}
	firstQueued, err := queueService.QueueTraining(ctx, QueueInput{
		Principal: fixture.AdminPrincipal, ConfigurationKey: fixture.ConfigurationKey,
		ExpectedAnalyticsGenerationID: review.AnalyticsGenerationID, ExpectedAnalyticsHeadRevision: review.AnalyticsHeadRevision,
	})
	if err != nil || !firstQueued.Created || firstQueued.Run.SourceAnalyticsGenerationID != firstGeneration.ID {
		t.Fatalf("first queue=%#v error=%v", firstQueued, err)
	}
	detail, found, err := adminReaderService.ReadTrainingRun(ctx, fixture.AdminPrincipal, firstQueued.Run.ID)
	if err != nil || !found || detail.Run.ID != firstQueued.Run.ID || detail.TrainingConfigurationKey != fixture.ConfigurationKey || detail.Failure != nil {
		t.Fatalf("queued run detail=%#v found=%t error=%v", detail, found, err)
	}
	events, found, err := adminReaderService.ReadTrainingEvents(ctx, fixture.AdminPrincipal, firstQueued.Run.ID, 0, 100)
	if err != nil || !found || len(events.Items) != 1 || events.Items[0].Sequence != 1 || events.Items[0].Type != "queued" ||
		!bytes.Contains(events.Items[0].Payload, []byte(`"sourceAnalyticsGenerationId":"`)) {
		t.Fatalf("queued run events=%#v found=%t error=%v", events, found, err)
	}
	staleClaim, err := repository.ClaimTraining(ctx, "recommendation-stale", integrationUUID(t), 10*time.Second)
	if err != nil || staleClaim == nil || staleClaim.ID != firstQueued.Run.ID {
		t.Fatalf("stale claim=%#v error=%v", staleClaim, err)
	}
	if _, err := pool.Exec(ctx, `
UPDATE ascendany.recommendation_training_runs
SET lease_expires_at = clock_timestamp() - interval '1 second',
    updated_at = clock_timestamp()
WHERE training_run_id = $1`, staleClaim.DatabaseID); err != nil {
		t.Fatal(err)
	}
	activeClaim, err := repository.ClaimTraining(ctx, "recommendation-active", integrationUUID(t), 10*time.Second)
	if err != nil || activeClaim == nil || !activeClaim.Reclaimed || activeClaim.AttemptCount != 2 {
		t.Fatalf("active claim=%#v error=%v", activeClaim, err)
	}

	secondGeneration := publishFixtureAnalyticsGeneration(t, ctx, pool, fixture)
	if err := repository.FailTraining(ctx, *staleClaim, "stale_worker", "stale worker must be fenced"); CodeOf(err) != ErrorLeaseLost {
		t.Fatalf("stale failure error=%v code=%q", err, CodeOf(err))
	}
	firstOutcome := publishIntegrationClaim(t, ctx, repository, store, *activeClaim)
	if firstOutcome.Disposition != PublishSuperseded {
		t.Fatalf("first outcome=%#v", firstOutcome)
	}

	secondQueued, err := queueService.QueueTraining(ctx, QueueInput{
		Principal: fixture.AdminPrincipal, ConfigurationKey: fixture.ConfigurationKey,
		ExpectedAnalyticsGenerationID: secondGeneration.ID, ExpectedAnalyticsHeadRevision: secondGeneration.Revision,
	})
	if err != nil || !secondQueued.Created || secondQueued.Run.SourceAnalyticsGenerationID != secondGeneration.ID {
		t.Fatalf("second queue=%#v error=%v", secondQueued, err)
	}
	secondReplay, err := queueService.QueueTraining(ctx, QueueInput{
		Principal: fixture.AdminPrincipal, ConfigurationKey: fixture.ConfigurationKey,
		ExpectedAnalyticsGenerationID: secondGeneration.ID, ExpectedAnalyticsHeadRevision: secondGeneration.Revision,
	})
	if err != nil || secondReplay.Created || secondReplay.Run.ID != secondQueued.Run.ID {
		t.Fatalf("second replay=%#v error=%v", secondReplay, err)
	}
	secondClaim, err := repository.ClaimTraining(ctx, "recommendation-winner", integrationUUID(t), 10*time.Second)
	if err != nil || secondClaim == nil || secondClaim.ID != secondQueued.Run.ID {
		t.Fatalf("second claim=%#v error=%v", secondClaim, err)
	}
	secondOutcome := publishIntegrationClaim(t, ctx, repository, store, *secondClaim)
	if secondOutcome.Disposition != PublishActivated {
		t.Fatalf("second outcome=%#v", secondOutcome)
	}

	fresh, err := readerService.ReadCurrent(ctx, fixture.StudentPrincipal)
	if err != nil || fresh.State != RecommendationFresh || fresh.Model == nil || fresh.Result == nil ||
		fresh.Model.AnalyticsGenerationID != fmt.Sprint(secondGeneration.ID) || fresh.Model.ModelID != secondOutcome.ModelID ||
		fresh.Result.Schema != ResultSchemaV2 || len(fresh.Result.KnowledgeMastery) == 0 {
		t.Fatalf("fresh recommendation=%#v error=%v", fresh, err)
	}

	thirdGeneration := publishFixtureAnalyticsGeneration(t, ctx, pool, fixture)
	stale, err := readerService.ReadCurrent(ctx, fixture.StudentPrincipal)
	if err != nil || stale.State != RecommendationStale || stale.Model == nil ||
		stale.CurrentAnalyticsGenerationID == nil || *stale.CurrentAnalyticsGenerationID != fmt.Sprint(thirdGeneration.ID) {
		t.Fatalf("stale recommendation=%#v error=%v", stale, err)
	}

	thirdQueued, err := queueService.QueueTraining(ctx, QueueInput{
		Principal: fixture.AdminPrincipal, ConfigurationKey: fixture.ConfigurationKey,
		ExpectedAnalyticsGenerationID: thirdGeneration.ID, ExpectedAnalyticsHeadRevision: thirdGeneration.Revision,
	})
	if err != nil || !thirdQueued.Created {
		t.Fatalf("third queue=%#v error=%v", thirdQueued, err)
	}
	thirdClaim, err := repository.ClaimTraining(ctx, "recommendation-retry", integrationUUID(t), 10*time.Second)
	if err != nil || thirdClaim == nil || thirdClaim.ID != thirdQueued.Run.ID {
		t.Fatalf("third claim=%#v error=%v", thirdClaim, err)
	}
	if err := repository.RenewTrainingLease(ctx, *thirdClaim, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := repository.RequeueTraining(ctx, *thirdClaim, time.Second, "trainer_busy"); err != nil {
		t.Fatal(err)
	}
	if err := repository.FailTraining(ctx, *thirdClaim, "stale_retry", "old retry attempt"); CodeOf(err) != ErrorLeaseLost {
		t.Fatalf("requeued stale failure error=%v code=%q", err, CodeOf(err))
	}
	time.Sleep(1100 * time.Millisecond)
	thirdRetryClaim, err := repository.ClaimTraining(ctx, "recommendation-retry", integrationUUID(t), 10*time.Second)
	if err != nil || thirdRetryClaim == nil || thirdRetryClaim.ID != thirdQueued.Run.ID || thirdRetryClaim.AttemptCount != 2 {
		t.Fatalf("third retry claim=%#v error=%v", thirdRetryClaim, err)
	}
	if err := repository.FailTraining(ctx, *thirdRetryClaim, "trainer_rejected", "trainer rejected the deterministic fixture"); err != nil {
		t.Fatal(err)
	}

	assertRecommendationPersistence(t, ctx, pool, firstQueued.Run.ID, secondQueued.Run.ID, thirdQueued.Run.ID, len(fixture.ActorIDs))
}

type recommendationFixture struct {
	AdminPrincipal   auth.AccessPrincipal
	StudentPrincipal auth.AccessPrincipal
	ConfigurationKey string
	TargetExamID     int64
	TargetSnapshotID int64
	ActorIDs         []int64
	sequence         int64
}

type fixtureGeneration struct {
	ID       int64
	Revision int64
}

func seedRecommendationFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) *recommendationFixture {
	t.Helper()
	suffix := randomHex(t, 6)
	adminAccountID := integrationUUID(t)
	adminSessionID := integrationUUID(t)
	var adminDatabaseID, adminSessionDatabaseID int64
	if err := pool.QueryRow(ctx, `
INSERT INTO ascendany.auth_accounts (
    public_id, username, password_phc, display_name, role, auth_revision, created_at, updated_at
)
VALUES ($1::uuid, $2, '$argon2id$v=19$m=65536,t=3,p=1$test$test', 'Recommendation Admin', 'admin', 1,
        clock_timestamp(), clock_timestamp())
RETURNING account_id`, adminAccountID, "reca_"+suffix).Scan(&adminDatabaseID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
INSERT INTO ascendany.auth_sessions (
    public_id, account_id, auth_revision, created_at, expires_at, last_seen_at
)
VALUES ($1::uuid, $2, 1, clock_timestamp(), clock_timestamp() + interval '1 hour', clock_timestamp())
RETURNING session_id`, adminSessionID, adminDatabaseID).Scan(&adminSessionDatabaseID); err != nil {
		t.Fatal(err)
	}

	studentNumber := "26" + suffix
	var studentActorID int64
	if err := pool.QueryRow(ctx, `
INSERT INTO ascendany.pintia_actors (user_id)
VALUES ($1)
RETURNING actor_id`, "recommendation-student-"+suffix).Scan(&studentActorID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO ascendany.pintia_actor_identifiers (identifier_kind, identifier_value, actor_id)
VALUES ('student_number', $1, $2)`, studentNumber, studentActorID); err != nil {
		t.Fatal(err)
	}
	studentAccountID := integrationUUID(t)
	studentSessionID := integrationUUID(t)
	var studentDatabaseID int64
	if err := pool.QueryRow(ctx, `
INSERT INTO ascendany.auth_accounts (
    public_id, username, password_phc, display_name, student_number, actor_id,
    role, auth_revision, created_at, updated_at
)
VALUES ($1::uuid, $2, '$argon2id$v=19$m=65536,t=3,p=1$test$test', 'Recommendation Student', $3, $4,
        'student', 1, clock_timestamp(), clock_timestamp())
RETURNING account_id`, studentAccountID, "recs_"+suffix, studentNumber, studentActorID).Scan(&studentDatabaseID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO ascendany.auth_sessions (
    public_id, account_id, auth_revision, created_at, expires_at, last_seen_at
)
VALUES ($1::uuid, $2, 1, clock_timestamp(), clock_timestamp() + interval '1 hour', clock_timestamp())`, studentSessionID, studentDatabaseID); err != nil {
		t.Fatal(err)
	}
	var secondActorID int64
	if err := pool.QueryRow(ctx, `
INSERT INTO ascendany.pintia_actors (user_id)
VALUES ($1)
RETURNING actor_id`, "recommendation-peer-"+suffix).Scan(&secondActorID); err != nil {
		t.Fatal(err)
	}
	actorIDs := []int64{studentActorID, secondActorID}
	if actorIDs[0] > actorIDs[1] {
		actorIDs[0], actorIDs[1] = actorIDs[1], actorIDs[0]
	}
	problemOneHTML := "<p>Compute <strong>A</strong>.</p>"
	problemTwoHTML := "<p>Traverse a graph.</p>"
	maxScore := "100"
	timeLimit := int64(1000)
	memoryLimit := int64(67_108_864)
	problemOneHash := testProblemFactHash(t, "501", "Problem A", &problemOneHTML, maxScore, &timeLimit, &memoryLimit)
	problemTwoHash := testProblemFactHash(t, "502", "Problem B", &problemTwoHTML, maxScore, &timeLimit, &memoryLimit)
	catalogDocument, catalogHash := canonicalRaw(t, map[string]any{
		"taxonomyId": "recommendation.integration." + suffix,
		"knowledgePoints": []any{
			map[string]any{"id": "arrays", "label": "Arrays", "description": "Array fundamentals", "prerequisiteIds": []any{}},
			map[string]any{"id": "graphs", "label": "Graphs", "description": "Graph traversal", "prerequisiteIds": []any{"arrays"}},
		},
		"problemAssignments": []any{
			map[string]any{"platform": "pintia", "problemId": "501", "problemFactSha256": problemOneHash, "knowledge": []any{map[string]any{"knowledgePointId": "arrays", "weight": 1}}},
			map[string]any{"platform": "pintia", "problemId": "502", "problemFactSha256": problemTwoHash, "knowledge": []any{map[string]any{"knowledgePointId": "graphs", "weight": 1}}},
		},
	})
	var catalogItemID, catalogVersionID int64
	if err := pool.QueryRow(ctx, `
INSERT INTO ascendany.configuration_items (public_id, configuration_key, configuration_kind)
VALUES ($1::uuid, $2, 'knowledge_catalog')
RETURNING configuration_item_id`, integrationUUID(t), "recommendation.knowledge."+suffix).Scan(&catalogItemID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
INSERT INTO ascendany.configuration_versions (
    configuration_item_id, configuration_kind, version_number, schema_id,
    document, document_sha256, created_by_account_id, created_by_session_id
)
VALUES ($1, 'knowledge_catalog', 1, $2, $3::jsonb, $4, $5, $6)
RETURNING configuration_version_id`, catalogItemID, knowledgeCatalogSchemaV1, string(catalogDocument), catalogHash,
		adminDatabaseID, adminSessionDatabaseID).Scan(&catalogVersionID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
UPDATE ascendany.configuration_items
SET active_version_id = $2, head_revision = 1, updated_at = clock_timestamp()
WHERE configuration_item_id = $1 AND head_revision = 0`, catalogItemID, catalogVersionID); err != nil {
		t.Fatal(err)
	}

	configurationKey := "recommendation.training." + suffix
	document, documentHash := canonicalRaw(t, map[string]any{
		"algorithm": trainingAlgorithmV2, "knowledgeCatalogVersionId": fmt.Sprint(catalogVersionID), "accelerator": "cuda",
		"seed": 2026, "epochs": 4, "patience": 2, "batchSize": 2, "learningRate": 0.01,
		"weightDecay": 0.001, "minTrainInteractions": 2, "minActorInteractions": 2, "minProblemInteractions": 1,
		"validation":     map[string]any{"minActors": 2, "minInteractions": 2, "minRelativeLogLossImprovement": 0},
		"pathPolicy":     map[string]any{"targetMastery": 0.8, "maxKnowledgeTargets": 2, "minSteps": 2, "maxSteps": 4, "problemsPerStep": 2, "targetSuccessProbability": 0.7},
		"rankingWeights": map[string]any{"knowledgeGap": 1, "successDistance": 1},
	})
	var configurationItemID, configurationVersionID int64
	if err := pool.QueryRow(ctx, `
INSERT INTO ascendany.configuration_items (public_id, configuration_key, configuration_kind)
VALUES ($1::uuid, $2, 'training')
RETURNING configuration_item_id`, integrationUUID(t), configurationKey).Scan(&configurationItemID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
INSERT INTO ascendany.configuration_versions (
    configuration_item_id, configuration_kind, version_number, schema_id,
    document, document_sha256, created_by_account_id, created_by_session_id
)
VALUES ($1, 'training', 1, $2, $3::jsonb, $4, $5, $6)
RETURNING configuration_version_id`, configurationItemID, trainingConfigurationSchemaV2, string(document), documentHash,
		adminDatabaseID, adminSessionDatabaseID).Scan(&configurationVersionID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
UPDATE ascendany.configuration_items
SET active_version_id = $2, head_revision = 1, updated_at = clock_timestamp()
WHERE configuration_item_id = $1 AND head_revision = 0`, configurationItemID, configurationVersionID); err != nil {
		t.Fatal(err)
	}

	problemSetID := fmt.Sprintf("9%d", time.Now().UnixNano())
	targetExamID, targetSnapshotID := seedAnalyticsTarget(
		t, ctx, pool, suffix, problemSetID, actorIDs, problemOneHTML, problemTwoHTML,
	)
	return &recommendationFixture{
		AdminPrincipal: auth.AccessPrincipal{
			AccountID: adminAccountID, SessionID: adminSessionID, JWTID: integrationUUID(t), Role: auth.RoleAdmin, AuthRevision: 1,
		},
		StudentPrincipal: auth.AccessPrincipal{
			AccountID: studentAccountID, SessionID: studentSessionID, JWTID: integrationUUID(t), Role: auth.RoleStudent, AuthRevision: 1,
		},
		ConfigurationKey: configurationKey,
		TargetExamID:     targetExamID,
		TargetSnapshotID: targetSnapshotID,
		ActorIDs:         actorIDs,
	}
}

func seedAnalyticsTarget(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	suffix string,
	problemSetID string,
	actorIDs []int64,
	problemOneHTML string,
	problemTwoHTML string,
) (int64, int64) {
	t.Helper()
	artifactHash := randomHex(t, 32)
	var artifactID int64
	if err := pool.QueryRow(ctx, `
INSERT INTO ascendany.artifacts (sha256, size_bytes, media_type, storage_key)
VALUES ($1, 1, 'application/octet-stream', 'sha256/' || substr($1, 1, 2) || '/' || $1)
RETURNING artifact_id`, artifactHash).Scan(&artifactID); err != nil {
		t.Fatal(err)
	}
	var importJobID int64
	if err := pool.QueryRow(ctx, `
INSERT INTO ascendany.import_jobs (public_id, artifact_id, job_kind, status, stage)
VALUES ($1::uuid, $2, 'pintia_snapshot_v2', 'queued', 'received')
RETURNING import_job_id`, integrationUUID(t), artifactID).Scan(&importJobID); err != nil {
		t.Fatal(err)
	}
	var examID int64
	if err := pool.QueryRow(ctx, `
INSERT INTO ascendany.logical_exams (public_id, platform, source_exam_id)
VALUES ($1::uuid, 'pintia', $2)
RETURNING exam_id`, integrationUUID(t), problemSetID).Scan(&examID); err != nil {
		t.Fatal(err)
	}
	var snapshotID int64
	if err := pool.QueryRow(ctx, `
INSERT INTO ascendany.exam_snapshots (
    public_id, exam_id, snapshot_sequence, source_artifact_id, import_job_id,
    contract_schema, contract_schema_sha256, domain_hash_protocol, domain_hash,
    exporter_name, exporter_version, exported_at, title, source_url,
    problems_observed_count, problems_exported_count, problems_pagination_exhausted,
    rankings_observed_count, rankings_exported_count, rankings_pagination_exhausted,
    submissions_observed_count, submissions_exported_count, submissions_pagination_exhausted,
    participants_exported_count
)
VALUES (
    $1::uuid, $2, 1, $3, $4,
    'ascendany.pintia.snapshot.v2', $5, 'domain_hash_proto_v1', $6,
    'ascendany-pintia-exporter', '2.0.0', clock_timestamp(), 'Recommendation Fixture', $7,
	    2, 2, true,
	    2, 2, true,
	    4, 4, true,
	    2
)
RETURNING snapshot_id`, integrationUUID(t), examID, artifactID, importJobID, randomHex(t, 32), randomHex(t, 32),
		"https://pintia.cn/problem-sets/"+problemSetID).Scan(&snapshotID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO ascendany.pintia_snapshot_problems (
    snapshot_id, problem_set_problem_id, problem_id, label, title, problem_type,
    max_score, content_html, time_limit_ms, memory_limit_bytes
)
VALUES
    ($1, '2001', '501', 'A', 'Problem A', 'PROGRAMMING', 100, $2, 1000, 67108864),
    ($1, '2002', '502', 'B', 'Problem B', 'PROGRAMMING', 100, $3, 1000, 67108864)`, snapshotID, problemOneHTML, problemTwoHTML); err != nil {
		t.Fatal(err)
	}
	if len(actorIDs) != 2 {
		t.Fatal("recommendation fixture requires exactly two actors")
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO ascendany.pintia_snapshot_participants (snapshot_id, actor_id)
VALUES ($1, $2), ($1, $3)`, snapshotID, actorIDs[0], actorIDs[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO ascendany.pintia_rankings (snapshot_id, actor_id, rank, total_score, time_used_seconds)
VALUES ($1, $2, 2, 180, 600), ($1, $3, 1, 170, 500)`, snapshotID, actorIDs[0], actorIDs[1]); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO ascendany.pintia_ranking_problem_results (
    snapshot_id, actor_id, problem_set_problem_id, score, passed, valid_submission_count, accept_time_seconds
)
VALUES
    ($1, $2, '2001', 80, false, 1, 0),
    ($1, $2, '2002', 100, true, 1, 300),
    ($1, $3, '2001', 100, true, 1, 400),
    ($1, $3, '2002', 70, false, 1, 0)`, snapshotID, actorIDs[0], actorIDs[1]); err != nil {
		t.Fatal(err)
	}
	baseTime := time.Date(2026, 7, 11, 2, 0, 0, 0, time.UTC)
	type submissionFixture struct {
		actorID   int64
		problem   string
		score     int64
		verdict   string
		timestamp time.Time
	}
	submissions := []submissionFixture{
		{actorID: actorIDs[0], problem: "2001", score: 80, verdict: "WRONG_ANSWER", timestamp: baseTime},
		{actorID: actorIDs[0], problem: "2002", score: 100, verdict: "ACCEPTED", timestamp: baseTime.Add(3 * time.Minute)},
		{actorID: actorIDs[1], problem: "2002", score: 70, verdict: "WRONG_ANSWER", timestamp: baseTime.Add(2 * time.Minute)},
		{actorID: actorIDs[1], problem: "2001", score: 100, verdict: "ACCEPTED", timestamp: baseTime.Add(4 * time.Minute)},
	}
	for index, submission := range submissions {
		var identityID int64
		if err := pool.QueryRow(ctx, `
INSERT INTO ascendany.pintia_submission_identities (
    submission_id, exam_id, actor_id, problem_set_problem_id, submitted_at, code, code_sha256
)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING submission_identity_id`, fmt.Sprintf("recommendation-%s-%d", suffix, index), examID,
			submission.actorID, submission.problem, submission.timestamp, "int main(){}", randomHex(t, 32)).Scan(&identityID); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `
INSERT INTO ascendany.pintia_snapshot_submissions (
    snapshot_id, exam_id, submission_identity_id, actor_id, problem_set_problem_id, verdict, score
)
VALUES ($1, $2, $3, $4, $5, $6, $7)`, snapshotID, examID, identityID, submission.actorID,
			submission.problem, submission.verdict, submission.score); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `
UPDATE ascendany.logical_exams
SET active_snapshot_id = $2, head_revision = 1, updated_at = clock_timestamp()
WHERE exam_id = $1 AND head_revision = 0`, examID, snapshotID); err != nil {
		t.Fatal(err)
	}
	return examID, snapshotID
}

func publishFixtureAnalyticsGeneration(t *testing.T, ctx context.Context, pool *pgxpool.Pool, fixture *recommendationFixture) fixtureGeneration {
	t.Helper()
	transaction, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.Rollback(ctx)
	var baseGenerationID *int64
	var baseRevision int64
	if err := transaction.QueryRow(ctx, `
SELECT current_generation_id, head_revision
FROM ascendany.analytics_head
WHERE singleton
FOR UPDATE`).Scan(&baseGenerationID, &baseRevision); err != nil {
		t.Fatal(err)
	}
	fixture.sequence++
	manifest, manifestHash := canonicalRaw(t, map[string]any{
		"fixture":      fixture.ConfigurationKey,
		"sequence":     fixture.sequence,
		"baseRevision": baseRevision,
	})
	var generationID int64
	if err := transaction.QueryRow(ctx, `
INSERT INTO ascendany.analytics_generations (
    status, base_analytics_generation_id, base_head_revision,
    target_exam_id, target_snapshot_id, target_exam_head_revision,
    input_manifest, input_manifest_sha256, algorithm_version, config_sha256
)
VALUES ('queued', $1, $2, $3, $4, 1, $5::jsonb, $6, 'recommendation_integration_v1', $7)
RETURNING analytics_generation_id`, baseGenerationID, baseRevision, fixture.TargetExamID, fixture.TargetSnapshotID,
		string(manifest), manifestHash, randomHex(t, 32)).Scan(&generationID); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Exec(ctx, `
UPDATE ascendany.analytics_generations
SET status = 'running', attempt_count = 1, lease_owner = 'recommendation-fixture',
    lease_expires_at = clock_timestamp() + interval '1 minute', started_at = clock_timestamp()
WHERE analytics_generation_id = $1`, generationID); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Exec(ctx, `
INSERT INTO ascendany.analytics_generation_snapshots (
    analytics_generation_id, exam_id, snapshot_id, domain_hash
)
SELECT $1, exam_id, snapshot_id, domain_hash
FROM ascendany.exam_snapshots
WHERE snapshot_id = $2`, generationID, fixture.TargetSnapshotID); err != nil {
		t.Fatal(err)
	}
	for index, actorID := range fixture.ActorIDs {
		if _, err := transaction.Exec(ctx, `
INSERT INTO ascendany.student_analytics (analytics_generation_id, actor_id, rating, metrics)
VALUES ($1, $2, $3, jsonb_build_object('solved', $4::integer, 'sequence', $5::bigint))`,
			generationID, actorID, 1000+index*150+int(fixture.sequence), 3+index, fixture.sequence); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := transaction.Exec(ctx, `
UPDATE ascendany.analytics_generations
SET status = 'succeeded', lease_owner = NULL, lease_expires_at = NULL, finished_at = clock_timestamp()
WHERE analytics_generation_id = $1`, generationID); err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.Exec(ctx, `
UPDATE ascendany.analytics_head
SET current_generation_id = $1, head_revision = $2, updated_at = clock_timestamp()
WHERE singleton AND current_generation_id IS NOT DISTINCT FROM $3 AND head_revision = $4`,
		generationID, baseRevision+1, baseGenerationID, baseRevision); err != nil {
		t.Fatal(err)
	}
	if err := transaction.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return fixtureGeneration{ID: generationID, Revision: baseRevision + 1}
}

func publishIntegrationClaim(
	t *testing.T,
	ctx context.Context,
	repository *PostgresRepository,
	store ArtifactStore,
	claim Claim,
) PublishResult {
	t.Helper()
	input := parseIntegrationClaimInput(t, ctx, store, claim)
	output, err := ParseOutputBundle(outputTestBundle(t, input), 8<<20, input)
	if err != nil {
		t.Fatal(err)
	}
	publication, err := store.Publish(ctx, bytes.NewReader(output.CanonicalJSON))
	if err != nil {
		t.Fatal(err)
	}
	result, publishErr := repository.PublishTrainingOutput(ctx, PublishCommand{
		Claim: claim, ModelPublicID: integrationUUID(t), Input: input, Output: output,
		Artifact: publication.Artifact, MediaType: TrainingOutputMediaTypeV2,
	})
	if releaseErr := publication.Release(); releaseErr != nil {
		t.Fatal(releaseErr)
	}
	if publishErr != nil {
		t.Fatal(publishErr)
	}
	return result
}

func parseIntegrationClaimInput(
	t *testing.T,
	ctx context.Context,
	store ArtifactStore,
	claim Claim,
) ParsedInputBundle {
	t.Helper()
	verified, err := store.Verify(ctx, claim.InputArtifact.Hash, claim.InputArtifact.Size)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(verified.Path)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseInputBundle(raw, 8<<20, claim.TrainingRun)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func assertRecommendationPersistence(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	firstRunID, secondRunID, thirdRunID string,
	actorCount int,
) {
	t.Helper()
	var firstStatus, secondStatus, thirdStatus string
	if err := pool.QueryRow(ctx, `
SELECT first.status, second.status, third.status
FROM ascendany.recommendation_training_runs AS first
JOIN ascendany.recommendation_training_runs AS second ON second.public_id = $2::uuid
JOIN ascendany.recommendation_training_runs AS third ON third.public_id = $3::uuid
WHERE first.public_id = $1::uuid`, firstRunID, secondRunID, thirdRunID).Scan(&firstStatus, &secondStatus, &thirdStatus); err != nil {
		t.Fatal(err)
	}
	if firstStatus != string(RunSuperseded) || secondStatus != string(RunSucceeded) || thirdStatus != string(RunFailed) {
		t.Fatalf("statuses=%q,%q,%q", firstStatus, secondStatus, thirdStatus)
	}
	var modelCount, resultCount int
	if err := pool.QueryRow(ctx, `
SELECT count(DISTINCT model.recommendation_model_id), count(result.actor_id)
FROM ascendany.recommendation_training_runs AS run
JOIN ascendany.recommendation_models AS model ON model.training_run_id = run.training_run_id
JOIN ascendany.student_recommendation_results AS result ON result.recommendation_model_id = model.recommendation_model_id
WHERE run.public_id IN ($1::uuid, $2::uuid)`, firstRunID, secondRunID).Scan(&modelCount, &resultCount); err != nil {
		t.Fatal(err)
	}
	if modelCount != 2 || resultCount != actorCount*2 {
		t.Fatalf("modelCount=%d resultCount=%d", modelCount, resultCount)
	}
	var eventCount, minimumSequence, maximumSequence, distinctSequence int
	if err := pool.QueryRow(ctx, `
SELECT count(*), min(event.event_sequence), max(event.event_sequence), count(DISTINCT event.event_sequence)
FROM ascendany.recommendation_training_events AS event
JOIN ascendany.recommendation_training_runs AS run ON run.training_run_id = event.training_run_id
WHERE run.public_id = $1::uuid`, thirdRunID).Scan(&eventCount, &minimumSequence, &maximumSequence, &distinctSequence); err != nil {
		t.Fatal(err)
	}
	if eventCount != 6 || minimumSequence != 1 || maximumSequence != eventCount || distinctSequence != eventCount {
		t.Fatalf("third events count=%d min=%d max=%d distinct=%d", eventCount, minimumSequence, maximumSequence, distinctSequence)
	}
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
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32]
}

func randomHex(t *testing.T, size int) string {
	t.Helper()
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(value)
}
