package recommendation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kkkzbh/AscendAny/backend/internal/analytics"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
	"github.com/kkkzbh/AscendAny/backend/internal/configuration"
	"github.com/kkkzbh/AscendAny/backend/internal/modelartifact"
	"github.com/kkkzbh/AscendAny/backend/internal/modelrelease"
)

func TestPostgresRecommendationCatalogPublicationFencesAnalyticsReview(t *testing.T) {
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
	principal := loadRecommendationIntegrationAdmin(t, ctx, pool)
	reviewRepository, err := NewReviewContextPostgresRepository(pool)
	if err != nil {
		t.Fatal(err)
	}
	review, err := reviewRepository.ReadReviewContext(ctx, principal)
	if err != nil {
		t.Fatal(err)
	}
	complete := integrationCatalogForReview(t, review, "integration.review.initial")
	contract := ConfigurationPublicationContract{}

	repository, err := configuration.NewPostgresRepository(pool, contract)
	if err != nil {
		t.Fatal(err)
	}
	service, err := configuration.NewService(repository, contract)
	if err != nil {
		t.Fatal(err)
	}

	missing := integrationCatalogWithoutLastAssignment(t, complete)
	_, err = service.CreateVersion(ctx, integrationCatalogCommand(principal, review, 0, missing))
	assertPublicationIssue(t, err, configuration.ErrorDocumentInvalid, publicationIssueCatalogCoverage, true, false)

	factChanged := integrationCatalogWithChangedFact(t, complete)
	_, err = service.CreateVersion(ctx, integrationCatalogCommand(principal, review, 0, factChanged))
	assertPublicationIssue(t, err, configuration.ErrorDocumentInvalid, publicationIssueCatalogCoverage, true, true)

	blocking := &blockingPublicationContract{release: make(chan struct{}), validated: make(chan struct{})}
	blockingRepository, err := configuration.NewPostgresRepository(pool, blocking)
	if err != nil {
		t.Fatal(err)
	}
	blockingService, err := configuration.NewService(blockingRepository, blocking)
	if err != nil {
		t.Fatal(err)
	}
	type publicationResult struct {
		result configuration.CreateVersionResult
		err    error
	}
	published := make(chan publicationResult, 1)
	go func() {
		result, publishErr := blockingService.CreateVersion(ctx, integrationCatalogCommand(principal, review, 0, complete))
		published <- publicationResult{result: result, err: publishErr}
	}()
	select {
	case <-blocking.validated:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	blockedContext, cancelBlocked := context.WithTimeout(ctx, 250*time.Millisecond)
	_, advanceErr := advanceRecommendationIntegrationAnalyticsHead(blockedContext, pool)
	cancelBlocked()
	if !errors.Is(advanceErr, context.DeadlineExceeded) {
		close(blocking.release)
		t.Fatalf("analytics head advance was not fenced by catalog publication: %v", advanceErr)
	}
	close(blocking.release)
	publication := <-published
	if publication.err != nil || publication.result.Idempotent || publication.result.Item.HeadRevision != 1 {
		t.Fatalf("catalog publication=%#v error=%v", publication.result, publication.err)
	}

	advancedGenerationID, err := advanceRecommendationIntegrationAnalyticsHead(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.CreateVersion(ctx, integrationCatalogCommand(principal, review, 1, complete))
	assertPublicationIssue(t, err, configuration.ErrorReviewConflict, publicationIssueAnalyticsChanged, false, false)

	advancedReview := loadRecommendationIntegrationReview(t, ctx, pool)
	if advancedReview.AnalyticsGenerationID != advancedGenerationID || advancedReview.AnalyticsHeadRevision != review.AnalyticsHeadRevision+1 {
		t.Fatalf("advanced review=%#v generation=%d", advancedReview, advancedGenerationID)
	}
	advancedCatalog := integrationCatalogForReview(t, advancedReview, "integration.review.advanced")
	second, err := service.CreateVersion(ctx, integrationCatalogCommand(principal, advancedReview, 1, advancedCatalog))
	if err != nil || second.Idempotent || second.Item.HeadRevision != 2 {
		t.Fatalf("advanced catalog publication=%#v error=%v", second, err)
	}
	var auditGenerationID string
	var auditHeadRevision int64
	var auditManifestSHA256 string
	if err := pool.QueryRow(ctx, `
SELECT payload ->> 'analyticsGenerationId',
       (payload ->> 'analyticsHeadRevision')::bigint,
       payload ->> 'inputManifestSha256'
FROM ascendany.audit_events
WHERE event_type = 'admin.configuration_version_created'
  AND payload ->> 'configurationId' = $1
ORDER BY audit_event_id DESC
LIMIT 1`, second.Item.ID).Scan(&auditGenerationID, &auditHeadRevision, &auditManifestSHA256); err != nil {
		t.Fatal(err)
	}
	if auditGenerationID != fmt.Sprint(advancedReview.AnalyticsGenerationID) ||
		auditHeadRevision != advancedReview.AnalyticsHeadRevision || auditManifestSHA256 != advancedReview.InputManifestSHA256 {
		t.Fatalf("audit review provenance=%s/%d/%s", auditGenerationID, auditHeadRevision, auditManifestSHA256)
	}
}

type blockingPublicationContract struct {
	ConfigurationPublicationContract
	once      sync.Once
	validated chan struct{}
	release   chan struct{}
}

func (contract *blockingPublicationContract) ValidateVersionWrite(ctx context.Context, tx configuration.VersionWriteTransaction, command configuration.CreateVersionCommand) error {
	if err := contract.ConfigurationPublicationContract.ValidateVersionWrite(ctx, tx, command); err != nil {
		return err
	}
	if command.Kind == configuration.KindKnowledgeCatalog {
		contract.once.Do(func() { close(contract.validated) })
		select {
		case <-contract.release:
		case <-ctx.Done():
			return &configuration.Error{Code: configuration.ErrorCanceled, Op: "wait for integration publication release", Cause: ctx.Err()}
		}
	}
	return nil
}

func loadRecommendationIntegrationAdmin(t *testing.T, ctx context.Context, pool *pgxpool.Pool) auth.AccessPrincipal {
	t.Helper()
	principal := auth.AccessPrincipal{JWTID: "99999999-9999-4999-8999-999999999999"}
	var role string
	if err := pool.QueryRow(ctx, `
SELECT account.public_id::text, session.public_id::text, account.role, account.auth_revision
FROM ascendany.auth_accounts AS account
JOIN ascendany.auth_sessions AS session
  ON session.account_id = account.account_id
 AND session.auth_revision = account.auth_revision
WHERE account.role = 'admin'
  AND account.disabled_at IS NULL
  AND session.revoked_at IS NULL
  AND session.expires_at > clock_timestamp()
ORDER BY session.session_id DESC
LIMIT 1`).Scan(&principal.AccountID, &principal.SessionID, &role, &principal.AuthRevision); err != nil {
		t.Fatal(err)
	}
	principal.Role = auth.Role(role)
	return principal
}

func loadRecommendationIntegrationReview(t *testing.T, ctx context.Context, pool *pgxpool.Pool) ReviewContext {
	t.Helper()
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	review, err := loadReviewContext(ctx, tx, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return review
}

func integrationCatalogForReview(t *testing.T, review ReviewContext, taxonomyID string) json.RawMessage {
	t.Helper()
	assignments := make([]map[string]any, len(review.Problems))
	for index, problem := range review.Problems {
		assignments[index] = map[string]any{
			"platform": problem.Platform, "problemId": problem.ProblemID, "problemFactSha256": problem.ProblemFactSHA256,
			"knowledge": []any{map[string]any{"knowledgePointId": "core", "weight": "1"}},
		}
	}
	slices.SortFunc(assignments, func(left, right map[string]any) int {
		for _, key := range []string{"platform", "problemId", "problemFactSha256"} {
			if comparison := strings.Compare(left[key].(string), right[key].(string)); comparison != 0 {
				return comparison
			}
		}
		return 0
	})
	raw, err := json.Marshal(map[string]any{
		"taxonomyId": taxonomyID,
		"knowledgePoints": []any{map[string]any{
			"id": "core", "label": "Core", "description": "Integration reviewed knowledge", "prerequisiteIds": []any{},
		}},
		"problemAssignments": assignments,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := parseKnowledgeCatalog(raw); err != nil {
		t.Fatal(err)
	}
	return raw
}

func integrationCatalogWithoutLastAssignment(t *testing.T, raw json.RawMessage) json.RawMessage {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	assignments := value["problemAssignments"].([]any)
	value["problemAssignments"] = assignments[:len(assignments)-1]
	result, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func integrationCatalogWithChangedFact(t *testing.T, raw json.RawMessage) json.RawMessage {
	t.Helper()
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	assignments := value["problemAssignments"].([]any)
	first := assignments[0].(map[string]any)
	original := first["problemFactSha256"].(string)
	replacement := "f" + original[1:]
	if replacement == original {
		replacement = "e" + original[1:]
	}
	first["problemFactSha256"] = replacement
	slices.SortFunc(assignments, func(left, right any) int {
		leftValue, rightValue := left.(map[string]any), right.(map[string]any)
		for _, key := range []string{"platform", "problemId", "problemFactSha256"} {
			if comparison := strings.Compare(leftValue[key].(string), rightValue[key].(string)); comparison != 0 {
				return comparison
			}
		}
		return 0
	})
	result, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := parseKnowledgeCatalog(result); err != nil {
		t.Fatal(err)
	}
	return result
}

func integrationCatalogCommand(principal auth.AccessPrincipal, review ReviewContext, expectedHeadRevision int64, document json.RawMessage) configuration.CreateVersionCommand {
	generationID := strconv.FormatInt(review.AnalyticsGenerationID, 10)
	headRevision := review.AnalyticsHeadRevision
	manifestSHA256 := review.InputManifestSHA256
	return configuration.CreateVersionCommand{
		Principal: principal, Key: configuration.KnowledgeCatalogKey, Kind: configuration.KindKnowledgeCatalog,
		ExpectedHeadRevision: expectedHeadRevision, ExpectedAnalyticsGenerationID: &generationID,
		ExpectedAnalyticsHeadRevision: &headRevision, ExpectedInputManifestSHA256: &manifestSHA256,
		SchemaID: KnowledgeCatalogSchemaV1, Document: document,
	}
}

func assertPublicationIssue(t *testing.T, err error, code configuration.ErrorCode, issueCode string, missing, dangling bool) {
	t.Helper()
	if configuration.CodeOf(err) != code {
		t.Fatalf("publication error=%v code=%s", err, configuration.CodeOf(err))
	}
	issue, ok := configuration.PublicationIssueOf(err)
	if !ok || issue.IssueCode != issueCode || (len(issue.MissingProblemKeys) > 0) != missing || (len(issue.DanglingProblemKeys) > 0) != dangling {
		t.Fatalf("publication issue=%#v found=%t", issue, ok)
	}
}

func advanceRecommendationIntegrationAnalyticsHead(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(context.Background())
	var currentGenerationID, currentHeadRevision, targetExamID, targetSnapshotID, targetExamHeadRevision int64
	var algorithmVersion, configSHA256 string
	if err := tx.QueryRow(ctx, `
SELECT head.current_generation_id, head.head_revision,
       generation.target_exam_id, generation.target_snapshot_id, generation.target_exam_head_revision,
       generation.algorithm_version, generation.config_sha256
FROM ascendany.analytics_head AS head
JOIN ascendany.analytics_generations AS generation
  ON generation.analytics_generation_id = head.current_generation_id
WHERE head.singleton`).Scan(
		&currentGenerationID, &currentHeadRevision, &targetExamID, &targetSnapshotID, &targetExamHeadRevision,
		&algorithmVersion, &configSHA256,
	); err != nil {
		return 0, err
	}
	rows, err := tx.Query(ctx, `
SELECT exam_id, snapshot_id, domain_hash
FROM ascendany.analytics_generation_snapshots
WHERE analytics_generation_id = $1
ORDER BY exam_id`, currentGenerationID)
	if err != nil {
		return 0, err
	}
	snapshots := make([]analytics.ManifestSnapshot, 0)
	for rows.Next() {
		var snapshot analytics.ManifestSnapshot
		if err := rows.Scan(&snapshot.ExamID, &snapshot.SnapshotID, &snapshot.DomainHash); err != nil {
			rows.Close()
			return 0, err
		}
		snapshots = append(snapshots, snapshot)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	manifest, err := analytics.CanonicalManifest(analytics.Manifest{
		Protocol: analytics.ManifestProtocolV1, BaseAnalyticsGenerationID: &currentGenerationID,
		BaseHeadRevision: currentHeadRevision,
		Target:           analytics.ManifestTarget{ExamID: targetExamID, SnapshotID: targetSnapshotID, ExamHeadRevision: targetExamHeadRevision},
		Snapshots:        snapshots,
	})
	if err != nil {
		return 0, err
	}
	var generationID int64
	if err := tx.QueryRow(ctx, `
INSERT INTO ascendany.analytics_generations (
    status, base_analytics_generation_id, base_head_revision,
    target_exam_id, target_snapshot_id, target_exam_head_revision,
    input_manifest, input_manifest_sha256, algorithm_version, config_sha256
)
VALUES ('queued', $1, $2, $3, $4, $5, $6::jsonb, $7, $8, $9)
RETURNING analytics_generation_id`, currentGenerationID, currentHeadRevision,
		targetExamID, targetSnapshotID, targetExamHeadRevision, string(manifest.Canonical), manifest.SHA256,
		algorithmVersion, configSHA256).Scan(&generationID); err != nil {
		return 0, err
	}
	for _, snapshot := range snapshots {
		if _, err := tx.Exec(ctx, `
INSERT INTO ascendany.analytics_generation_snapshots (analytics_generation_id, exam_id, snapshot_id, domain_hash)
VALUES ($1, $2, $3, $4)`, generationID, snapshot.ExamID, snapshot.SnapshotID, snapshot.DomainHash); err != nil {
			return 0, err
		}
	}
	if _, err := tx.Exec(ctx, `
UPDATE ascendany.analytics_generations
SET status = 'running', attempt_count = 1, lease_owner = 'catalog-publication-integration',
    lease_expires_at = clock_timestamp() + interval '1 hour', started_at = clock_timestamp()
WHERE analytics_generation_id = $1`, generationID); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `
UPDATE ascendany.analytics_generations
SET status = 'succeeded', lease_owner = NULL, lease_expires_at = NULL, finished_at = clock_timestamp()
WHERE analytics_generation_id = $1`, generationID); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `
UPDATE ascendany.analytics_head
SET current_generation_id = $1, head_revision = $2, updated_at = clock_timestamp()
WHERE singleton AND current_generation_id = $3 AND head_revision = $4`,
		generationID, currentHeadRevision+1, currentGenerationID, currentHeadRevision); err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return generationID, nil
}

func TestPostgresModelBindingMatchesVerifiedArtifact(t *testing.T) {
	databaseURL := os.Getenv("ASCENDANY_TEST_DATABASE_URL")
	modelPath := os.Getenv("ASCENDANY_TEST_RECOMMENDATION_MODEL_PATH")
	modelSHA256 := os.Getenv("ASCENDANY_TEST_RECOMMENDATION_MODEL_SHA256")
	catalogPath := os.Getenv("ASCENDANY_TEST_RECOMMENDATION_CATALOG_PATH")
	if databaseURL == "" || modelPath == "" || modelSHA256 == "" || catalogPath == "" {
		t.Skip("ASCENDANY_TEST_DATABASE_URL and recommendation model/catalog artifact variables are not configured")
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
	loaded, err := modelartifact.Load(modelPath, modelSHA256)
	if err != nil {
		t.Fatal(err)
	}
	releases, err := modelrelease.NewRepository(pool)
	if err != nil {
		t.Fatal(err)
	}
	firstBinding, err := releases.Bind(ctx, loaded, modelrelease.ApplicationIdentity{
		Version:   "0.0.0-integration",
		Commit:    "0000000000000000000000000000000000000000",
		BuildTime: "1970-01-01T00:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !firstBinding.Activated {
		t.Fatal("fresh database did not activate the verified model")
	}
	secondApplication := modelrelease.ApplicationIdentity{
		Version:   "0.0.1-integration",
		Commit:    "1111111111111111111111111111111111111111",
		BuildTime: "1970-01-01T00:00:01Z",
	}
	binding, err := releases.Bind(ctx, loaded, secondApplication)
	if err != nil {
		t.Fatal(err)
	}
	if !binding.Activated || binding.ReleaseID != firstBinding.ReleaseID || binding.HeadRevision != firstBinding.HeadRevision+1 {
		t.Fatalf("same model application activation mismatch: first=%#v second=%#v", firstBinding, binding)
	}
	replayed, err := releases.Bind(ctx, loaded, secondApplication)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Activated || replayed.ReleaseID != binding.ReleaseID || replayed.HeadRevision != binding.HeadRevision {
		t.Fatalf("same application replay mismatch: activated=%t revision=%d", replayed.Activated, replayed.HeadRevision)
	}
	var releaseCount, activationCount int64
	var storedHeadRevision int64
	if err := pool.QueryRow(ctx, `
SELECT (SELECT count(*) FROM ascendany.recommendation_model_releases),
       (SELECT count(*) FROM ascendany.recommendation_model_activation_events),
       (SELECT head_revision FROM ascendany.recommendation_model_head WHERE singleton)
`).Scan(&releaseCount, &activationCount, &storedHeadRevision); err != nil {
		t.Fatal(err)
	}
	if releaseCount != 1 || activationCount != 2 || storedHeadRevision != binding.HeadRevision {
		t.Fatalf("releaseCount=%d activationCount=%d headRevision=%d", releaseCount, activationCount, storedHeadRevision)
	}
	repository, err := NewPostgresRepository(pool, loaded.Model, binding)
	if err != nil {
		t.Fatal(err)
	}
	var provenance ModelProvenance
	if err := repository.transaction(ctx, "verify integration model binding", func(tx recommendationTx) error {
		var loadErr error
		provenance, loadErr = repository.loadModelProvenance(ctx, tx)
		return loadErr
	}); err != nil {
		t.Fatal(err)
	}
	manifest := loaded.Model.Manifest()
	if provenance.ArtifactSHA256 != loaded.SHA256 || provenance.ModelHeadRevision != binding.HeadRevision ||
		provenance.Purpose != string(manifest.Purpose) || provenance.TrainedAt != manifest.TrainedAt {
		t.Fatalf("provenance=%#v binding=%#v", provenance, binding)
	}

	matchingCatalog, err := os.ReadFile(catalogPath)
	if err != nil {
		t.Fatal(err)
	}
	_, _, matchingDigest, err := parseKnowledgeCatalog(matchingCatalog)
	if err != nil {
		t.Fatal(err)
	}
	if matchingDigest != loaded.Model.Manifest().KnowledgeCatalogSHA256 {
		t.Fatalf("catalog digest %s does not match model %s", matchingDigest, loaded.Model.Manifest().KnowledgeCatalogSHA256)
	}
	state, err := readIntegrationCatalog(ctx, repository)
	if err != nil || state.Available {
		t.Fatalf("missing active catalog was not reported unavailable: state=%#v error=%v", state, err)
	}
	publishIntegrationCatalog(t, ctx, pool, "recommendation.catalog.active", "44444444-4444-4444-8444-444444444444", matchingCatalog)
	state, err = readIntegrationCatalog(ctx, repository)
	if err != nil || !state.Available || state.Digest != matchingDigest {
		t.Fatalf("exact model catalog was not selected: state=%#v error=%v", state, err)
	}
	var mismatchValue map[string]any
	if err := json.Unmarshal(matchingCatalog, &mismatchValue); err != nil {
		t.Fatal(err)
	}
	mismatchValue["taxonomyId"] = "integration.mismatch"
	mismatchCatalog, err := json.Marshal(mismatchValue)
	if err != nil {
		t.Fatal(err)
	}
	publishIntegrationCatalogVersion(t, ctx, pool, mismatchCatalog)
	state, err = readIntegrationCatalog(ctx, repository)
	if err != nil || !state.Available || state.Digest == matchingDigest {
		t.Fatalf("mismatched active catalog was not loaded independently: state=%#v error=%v", state, err)
	}
	reason := activeCatalogUnavailableReason(state, loaded.Model.Manifest())
	if reason == nil || *reason != UnavailableKnowledgeMatch {
		t.Fatalf("mismatched active catalog reason=%v", reason)
	}
	publishIntegrationCatalogVersion(t, ctx, pool, matchingCatalog)
	state, err = readIntegrationCatalog(ctx, repository)
	if err != nil || !state.Available || state.Digest != matchingDigest {
		t.Fatalf("matching active catalog was not restored: state=%#v error=%v", state, err)
	}
	if _, err := pool.Exec(ctx, `
INSERT INTO ascendany.configuration_items (public_id, configuration_key, configuration_kind)
VALUES ('55555555-5555-4555-8555-555555555555'::uuid, 'recommendation.catalog.duplicate', 'knowledge_catalog')`); err == nil {
		t.Fatal("database accepted a second knowledge catalog identity")
	}
}

func readIntegrationCatalog(ctx context.Context, repository *PostgresRepository) (catalogState, error) {
	var state catalogState
	err := repository.transaction(ctx, "test catalog selection", func(tx recommendationTx) error {
		var loadErr error
		state, loadErr = loadActiveCatalog(ctx, tx)
		return loadErr
	})
	return state, err
}

func publishIntegrationCatalog(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	key string,
	publicID string,
	document json.RawMessage,
) {
	t.Helper()
	_, canonical, digest, err := parseKnowledgeCatalog(document)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	var accountID, sessionID, itemID, versionID int64
	if err := tx.QueryRow(ctx, `
SELECT account.account_id, session.session_id
FROM ascendany.auth_accounts AS account
JOIN ascendany.auth_sessions AS session ON session.account_id = account.account_id
WHERE account.public_id = '11111111-1111-4111-8111-111111111111'::uuid
  AND session.public_id = '22222222-2222-4222-8222-222222222222'::uuid`).Scan(&accountID, &sessionID); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, `
INSERT INTO ascendany.configuration_items (public_id, configuration_key, configuration_kind)
VALUES ($1::uuid, $2, 'knowledge_catalog')
RETURNING configuration_item_id`, publicID, key).Scan(&itemID); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, `
INSERT INTO ascendany.configuration_versions (
    configuration_item_id, configuration_kind, version_number, schema_id,
    document, document_sha256, created_by_account_id, created_by_role, created_by_session_id
) VALUES ($1, 'knowledge_catalog', 1, $2, $3::jsonb, $4, $5, 'admin', $6)
RETURNING configuration_version_id`, itemID, KnowledgeCatalogSchemaV1, string(canonical), digest, accountID, sessionID).Scan(&versionID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, `
UPDATE ascendany.configuration_items
SET active_version_id = $2, head_revision = 1, updated_at = clock_timestamp()
WHERE configuration_item_id = $1`, itemID, versionID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

func publishIntegrationCatalogVersion(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	document json.RawMessage,
) {
	t.Helper()
	_, canonical, digest, err := parseKnowledgeCatalog(document)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	var accountID, sessionID, itemID, headRevision, versionID int64
	if err := tx.QueryRow(ctx, `
SELECT account.account_id, session.session_id
FROM ascendany.auth_accounts AS account
JOIN ascendany.auth_sessions AS session ON session.account_id = account.account_id
WHERE account.public_id = '11111111-1111-4111-8111-111111111111'::uuid
  AND session.public_id = '22222222-2222-4222-8222-222222222222'::uuid`).Scan(&accountID, &sessionID); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, `
SELECT configuration_item_id, head_revision
FROM ascendany.configuration_items
WHERE configuration_key = $1
  AND configuration_kind = 'knowledge_catalog'
FOR UPDATE`, configuration.KnowledgeCatalogKey).Scan(&itemID, &headRevision); err != nil {
		t.Fatal(err)
	}
	if err := tx.QueryRow(ctx, `
INSERT INTO ascendany.configuration_versions (
    configuration_item_id, configuration_kind, version_number, schema_id,
    document, document_sha256, created_by_account_id, created_by_role, created_by_session_id
) VALUES ($1, 'knowledge_catalog', $2, $3, $4::jsonb, $5, $6, 'admin', $7)
RETURNING configuration_version_id`, itemID, headRevision+1, KnowledgeCatalogSchemaV1,
		string(canonical), digest, accountID, sessionID).Scan(&versionID); err != nil {
		t.Fatal(err)
	}
	if command, err := tx.Exec(ctx, `
UPDATE ascendany.configuration_items
SET active_version_id = $2, head_revision = $3, updated_at = clock_timestamp()
WHERE configuration_item_id = $1
  AND head_revision = $4`, itemID, versionID, headRevision+1, headRevision); err != nil {
		t.Fatal(err)
	} else if command.RowsAffected() != 1 {
		t.Fatal("catalog head compare-and-swap failed")
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}
