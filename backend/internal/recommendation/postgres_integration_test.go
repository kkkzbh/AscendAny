package recommendation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/kkkzbh/AscendAny/backend/internal/analytics"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
	"github.com/kkkzbh/AscendAny/backend/internal/configuration"
	"github.com/kkkzbh/AscendAny/backend/internal/modelartifact"
	"github.com/kkkzbh/AscendAny/backend/internal/modelrelease"
)

func TestPostgresRecommendationCatalogPublicationFencesAnalyticsReview(t *testing.T) {
	databaseURL := os.Getenv("ASCENDANY_TEST_DATABASE_URL")
	databasePassword := os.Getenv("ASCENDANY_TEST_DATABASE_PASSWORD")
	runtimeDatabaseURL := os.Getenv("ASCENDANY_TEST_RUNTIME_DATABASE_URL")
	runtimeDatabasePassword := os.Getenv("ASCENDANY_TEST_RUNTIME_DATABASE_PASSWORD")
	modelPath := os.Getenv("ASCENDANY_TEST_RECOMMENDATION_MODEL_PATH")
	modelSHA256 := os.Getenv("ASCENDANY_TEST_RECOMMENDATION_MODEL_SHA256")
	catalogPath := os.Getenv("ASCENDANY_TEST_RECOMMENDATION_CATALOG_PATH")
	catalogSHA256 := os.Getenv("ASCENDANY_TEST_RECOMMENDATION_CATALOG_SHA256")
	if databaseURL == "" || databasePassword == "" || runtimeDatabaseURL == "" || runtimeDatabasePassword == "" ||
		modelPath == "" || modelSHA256 == "" || catalogPath == "" || catalogSHA256 == "" {
		t.Skip("catalog publisher, runtime, model, and catalog integration inputs are not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	publisherPool := openRecommendationIntegrationPool(t, ctx, databaseURL, databasePassword)
	defer publisherPool.Close()
	runtimePool := openRecommendationIntegrationPool(t, ctx, runtimeDatabaseURL, runtimeDatabasePassword)
	defer runtimePool.Close()
	assertRecommendationIntegrationIdentity(t, ctx, publisherPool, "ascendany_catalog_publisher_login")
	assertRecommendationIntegrationIdentity(t, ctx, runtimePool, "ascendanyd_login")
	assertRecommendationIntegrationPublisherACL(t, ctx, publisherPool)
	principal := loadRecommendationIntegrationAdmin(t, ctx, runtimePool)
	reviewRepository, err := NewReviewContextPostgresRepository(runtimePool)
	if err != nil {
		t.Fatal(err)
	}
	review, err := reviewRepository.ReadReviewContext(ctx, principal)
	if err != nil {
		t.Fatal(err)
	}
	complete := loadRecommendationIntegrationCatalog(t, catalogPath, catalogSHA256)
	targetApplication := modelrelease.ApplicationIdentity{
		Version:   "0.2.0-integration",
		Commit:    "0000000000000000000000000000000000000000",
		BuildTime: "1970-01-01T00:00:00Z",
	}
	loadedModel, err := modelartifact.Load(modelPath, modelSHA256)
	if err != nil {
		t.Fatal(err)
	}
	modelReleases, err := modelrelease.NewRepository(runtimePool)
	if err != nil {
		t.Fatal(err)
	}
	h1Binding, err := modelReleases.RequireCurrent(ctx, loadedModel, targetApplication)
	if err != nil || h1Binding.HeadRevision != 1 {
		t.Fatalf("initial H1 runtime binding=%#v error=%v", h1Binding, err)
	}
	h1Repository, err := NewPostgresRepository(runtimePool, loadedModel.Model, h1Binding, testAnalyticsConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	contract := ConfigurationPublicationContract{}
	runtimeRepository, err := configuration.NewPostgresRepository(runtimePool, contract)
	if err != nil {
		t.Fatal(err)
	}
	runtimeService, err := configuration.NewService(runtimeRepository, contract)
	if err != nil {
		t.Fatal(err)
	}
	publisherRepository, err := configuration.NewPostgresRepository(publisherPool, contract)
	if err != nil {
		t.Fatal(err)
	}
	publisherService, err := configuration.NewService(publisherRepository, contract)
	if err != nil {
		t.Fatal(err)
	}

	publicationCommand := authorizeRecommendationIntegrationCatalog(
		t,
		ctx,
		runtimeService,
		integrationCatalogCommand(t, ctx, runtimePool, principal, review, 0, targetApplication, complete),
	)
	assertRecommendationIntegrationInvalidCapabilityIsAtomic(
		t,
		ctx,
		runtimePool,
		publisherService,
		publicationCommand,
	)

	publication, err := publisherService.CreateVersion(ctx, publicationCommand)
	if err != nil || publication.Idempotent || publication.Item.HeadRevision != 1 {
		t.Fatalf("catalog publication=%#v error=%v", publication, err)
	}
	if publication.KnowledgeCatalogPublication == nil ||
		publication.KnowledgeCatalogPublication.ConfigurationMutated != true {
		t.Fatalf("initial catalog publication provenance=%#v", publication.KnowledgeCatalogPublication)
	}
	revokeRecommendationIntegrationAdminSession(t, ctx, runtimePool, principal)
	replayed, err := publisherService.CreateVersion(ctx, publicationCommand)
	if err != nil || !replayed.Idempotent || replayed.KnowledgeCatalogPublication == nil ||
		replayed.KnowledgeCatalogPublication.KnowledgeCatalogPublicationID != publication.KnowledgeCatalogPublication.KnowledgeCatalogPublicationID ||
		replayed.KnowledgeCatalogPublication.PublishedByAccountID != principal.AccountID ||
		replayed.KnowledgeCatalogPublication.PublishedBySessionID != principal.SessionID {
		t.Fatalf("revoked-session exact capability replay=%#v error=%v", replayed, err)
	}
	assertRecommendationIntegrationPublicationForeignKey(
		t,
		ctx,
		runtimePool,
		*publication.KnowledgeCatalogPublication,
		targetApplication,
	)
	var pendingPublicationID string
	if err := runtimePool.QueryRow(ctx, `
SELECT pending_catalog_publication_id::text
FROM ascendany.recommendation_model_head
WHERE singleton`).Scan(&pendingPublicationID); err != nil {
		t.Fatal(err)
	}
	if pendingPublicationID != publication.KnowledgeCatalogPublication.KnowledgeCatalogPublicationID {
		t.Fatalf("pending publication=%s expected=%s", pendingPublicationID, publication.KnowledgeCatalogPublication.KnowledgeCatalogPublicationID)
	}
	if _, err := modelReleases.RequireCurrent(ctx, loadedModel, targetApplication); !errors.Is(err, modelrelease.ErrStoredDataInvalid) {
		t.Fatalf("H1 startup accepted pending publication: %v", err)
	}
	if err := h1Repository.transaction(ctx, "reject pending publication", func(tx recommendationTx) error {
		_, loadErr := h1Repository.loadModelProvenance(ctx, tx)
		return loadErr
	}); CodeOf(err) != ErrorStoredDataInvalid {
		t.Fatalf("H1 recommendation read accepted pending publication: code=%s error=%v", CodeOf(err), err)
	}
	binding, err := modelReleases.Bind(ctx, loadedModel, targetApplication)
	if err != nil || !binding.Activated || binding.HeadRevision != publication.KnowledgeCatalogPublication.CurrentModelHeadRevision+1 {
		t.Fatalf("catalog-authorized model activation=%#v error=%v", binding, err)
	}
	var consumedPublicationID string
	if err := runtimePool.QueryRow(ctx, `
SELECT knowledge_catalog_publication_id::text
FROM ascendany.recommendation_model_activation_events
WHERE head_revision = $1
  AND recommendation_model_release_id = $2`, binding.HeadRevision, binding.ReleaseID).Scan(&consumedPublicationID); err != nil {
		t.Fatal(err)
	}
	if consumedPublicationID != publication.KnowledgeCatalogPublication.KnowledgeCatalogPublicationID {
		t.Fatalf("model activation consumed publication %s; expected %s", consumedPublicationID, publication.KnowledgeCatalogPublication.KnowledgeCatalogPublicationID)
	}
	var pendingCleared bool
	if err := runtimePool.QueryRow(ctx, `
SELECT pending_catalog_publication_id IS NULL
FROM ascendany.recommendation_model_head
WHERE singleton`).Scan(&pendingCleared); err != nil {
		t.Fatal(err)
	}
	if !pendingCleared {
		t.Fatal("H2 activation did not clear the pending publication")
	}
	if _, err := modelReleases.RequireCurrent(ctx, loadedModel, targetApplication); err != nil {
		t.Fatalf("H2 startup binding rejected consumed publication: %v", err)
	}
	h2Repository, err := NewPostgresRepository(runtimePool, loadedModel.Model, binding, testAnalyticsConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := h2Repository.transaction(ctx, "accept consumed publication", func(tx recommendationTx) error {
		_, loadErr := h2Repository.loadModelProvenance(ctx, tx)
		return loadErr
	}); err != nil {
		t.Fatalf("H2 recommendation read rejected consumed publication: %v", err)
	}

	advancedGenerationID, err := advanceRecommendationIntegrationAnalyticsHead(ctx, runtimePool, 0)
	if err != nil {
		t.Fatal(err)
	}
	rotatedPrincipal := createRecommendationIntegrationAdminSession(t, ctx, runtimePool, principal)
	_, err = runtimeService.CreateCatalogPublicationAuthorization(
		ctx,
		integrationCatalogAuthorizationCommand(
			integrationCatalogCommand(t, ctx, runtimePool, rotatedPrincipal, review, 1, targetApplication, complete),
		),
	)
	assertPublicationIssue(t, err, configuration.ErrorReviewConflict, publicationIssueAnalyticsChanged, false, false)

	advancedReview := loadRecommendationIntegrationReview(t, ctx, runtimePool)
	if advancedReview.AnalyticsGenerationID != advancedGenerationID || advancedReview.AnalyticsHeadRevision != review.AnalyticsHeadRevision+1 {
		t.Fatalf("advanced review=%#v generation=%d", advancedReview, advancedGenerationID)
	}
	secondCommand := authorizeRecommendationIntegrationCatalog(
		t,
		ctx,
		runtimeService,
		integrationCatalogCommand(t, ctx, runtimePool, rotatedPrincipal, advancedReview, 1, targetApplication, complete),
	)
	second := publishRecommendationIntegrationWithConfigurationFence(
		t,
		ctx,
		runtimePool,
		publisherService,
		secondCommand,
	)
	if second.Idempotent || second.Item.HeadRevision != 1 || second.KnowledgeCatalogPublication == nil ||
		second.KnowledgeCatalogPublication.ConfigurationMutated {
		t.Fatalf("advanced catalog publication=%#v", second)
	}
	var auditGenerationID string
	var auditHeadRevision int64
	var auditManifestSHA256 string
	if err := runtimePool.QueryRow(ctx, `
SELECT payload ->> 'analyticsGenerationId',
       (payload ->> 'analyticsHeadRevision')::bigint,
       payload ->> 'inputManifestSha256'
FROM ascendany.audit_events
WHERE event_type = 'admin.knowledge_catalog_release_bound'
  AND payload ->> 'configurationId' = $1
ORDER BY audit_event_id DESC
LIMIT 1`, second.Item.ID).Scan(&auditGenerationID, &auditHeadRevision, &auditManifestSHA256); err != nil {
		t.Fatal(err)
	}
	if auditGenerationID != fmt.Sprint(advancedReview.AnalyticsGenerationID) ||
		auditHeadRevision != advancedReview.AnalyticsHeadRevision || auditManifestSHA256 != advancedReview.InputManifestSHA256 {
		t.Fatalf("audit review provenance=%s/%d/%s", auditGenerationID, auditHeadRevision, auditManifestSHA256)
	}
	if _, err := modelReleases.RequireCurrent(ctx, loadedModel, targetApplication); !errors.Is(err, modelrelease.ErrStoredDataInvalid) {
		t.Fatalf("H2 startup accepted second pending publication: %v", err)
	}
	h3Binding, err := modelReleases.Bind(ctx, loadedModel, targetApplication)
	if err != nil || !h3Binding.Activated || h3Binding.HeadRevision != binding.HeadRevision+1 {
		t.Fatalf("second publication activation=%#v error=%v", h3Binding, err)
	}
	if _, err := modelReleases.RequireCurrent(ctx, loadedModel, targetApplication); err != nil {
		t.Fatalf("H3 startup binding rejected consumed publication: %v", err)
	}
}

func openRecommendationIntegrationPool(
	t *testing.T,
	ctx context.Context,
	databaseURL string,
	databasePassword string,
) *pgxpool.Pool {
	t.Helper()
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	config.ConnConfig.Password = databasePassword
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatal(err)
	}
	return pool
}

func assertRecommendationIntegrationIdentity(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	expected string,
) {
	t.Helper()
	var currentUser string
	if err := pool.QueryRow(ctx, `SELECT current_user`).Scan(&currentUser); err != nil {
		t.Fatal(err)
	}
	if currentUser != expected {
		t.Fatalf("integration database identity=%q, want %q", currentUser, expected)
	}
}

func assertRecommendationIntegrationPublisherACL(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	for name, statement := range map[string]string{
		"read accounts":                   `SELECT account_id FROM ascendany.auth_accounts LIMIT 0`,
		"read sessions":                   `SELECT session_id FROM ascendany.auth_sessions LIMIT 0`,
		"write configuration items":       `UPDATE ascendany.configuration_items SET head_revision = head_revision WHERE false`,
		"write configuration versions":    `DELETE FROM ascendany.configuration_versions WHERE false`,
		"write publication authorization": `UPDATE ascendany.knowledge_catalog_publication_authorizations SET consumed_at = consumed_at WHERE false`,
		"write publications":              `DELETE FROM ascendany.knowledge_catalog_publications WHERE false`,
	} {
		t.Run("publisher ACL "+name, func(t *testing.T) {
			_, err := pool.Exec(ctx, statement)
			var databaseError *pgconn.PgError
			if !errors.As(err, &databaseError) || databaseError.Code != "42501" {
				t.Fatalf("publisher %s error=%v", name, err)
			}
		})
	}
}

type recommendationIntegrationPublicationResult struct {
	result configuration.CreateVersionResult
	err    error
}

type recommendationIntegrationPublicationState struct {
	authorizationUnconsumed   bool
	publicationCount          int64
	configurationItemCount    int64
	configurationVersionCount int64
	auditEventCount           int64
	pendingPublicationCount   int64
}

func assertRecommendationIntegrationInvalidCapabilityIsAtomic(
	t *testing.T,
	ctx context.Context,
	runtimePool *pgxpool.Pool,
	publisherService *configuration.Service,
	command configuration.CreateVersionCommand,
) {
	t.Helper()
	before := loadRecommendationIntegrationPublicationState(t, ctx, runtimePool, command.PublicationAuthorizationID)
	invalid := command
	invalid.PublicationAccessTokenSHA256 = fmt.Sprintf("%064x", 1)
	if invalid.PublicationAccessTokenSHA256 == command.PublicationAccessTokenSHA256 {
		invalid.PublicationAccessTokenSHA256 = fmt.Sprintf("%064x", 2)
	}
	_, err := publisherService.CreateVersion(ctx, invalid)
	if configuration.CodeOf(err) != configuration.ErrorPrincipalRejected {
		t.Fatalf("invalid publication capability code=%s error=%v", configuration.CodeOf(err), err)
	}
	after := loadRecommendationIntegrationPublicationState(t, ctx, runtimePool, command.PublicationAuthorizationID)
	if after != before {
		t.Fatalf("invalid publication capability mutated state: before=%#v after=%#v", before, after)
	}
}

func loadRecommendationIntegrationPublicationState(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	authorizationID string,
) recommendationIntegrationPublicationState {
	t.Helper()
	var state recommendationIntegrationPublicationState
	if err := pool.QueryRow(ctx, `
SELECT capability.consumed_publication_id IS NULL,
       (SELECT count(*) FROM ascendany.knowledge_catalog_publications),
       (SELECT count(*) FROM ascendany.configuration_items WHERE configuration_key = 'recommendation.catalog.active'),
       (SELECT count(*) FROM ascendany.configuration_versions WHERE configuration_kind = 'knowledge_catalog'),
       (SELECT count(*) FROM ascendany.audit_events),
       (SELECT count(*) FROM ascendany.recommendation_model_head WHERE singleton AND pending_catalog_publication_id IS NOT NULL)
FROM ascendany.knowledge_catalog_publication_authorizations AS capability
WHERE capability.public_id = $1::uuid`, authorizationID).Scan(
		&state.authorizationUnconsumed,
		&state.publicationCount,
		&state.configurationItemCount,
		&state.configurationVersionCount,
		&state.auditEventCount,
		&state.pendingPublicationCount,
	); err != nil {
		t.Fatal(err)
	}
	return state
}

func authorizeRecommendationIntegrationCatalog(
	t *testing.T,
	ctx context.Context,
	service *configuration.Service,
	command configuration.CreateVersionCommand,
) configuration.CreateVersionCommand {
	t.Helper()
	authorization, err := service.CreateCatalogPublicationAuthorization(
		ctx,
		integrationCatalogAuthorizationCommand(command),
	)
	if err != nil {
		t.Fatal(err)
	}
	request, err := configuration.CanonicalCatalogPublicationRequest(authorization.PublicationRequest)
	if err != nil {
		t.Fatal(err)
	}
	command.PublicationAuthorizationID = authorization.AuthorizationID
	command.PublicationAccessTokenSHA256 = integrationCatalogAccessTokenSHA256(command.Principal)
	command.PublicationAuthorizationRequest = request
	return command
}

func integrationCatalogAuthorizationCommand(command configuration.CreateVersionCommand) configuration.CreateCatalogPublicationAuthorizationCommand {
	return configuration.CreateCatalogPublicationAuthorizationCommand{
		Principal:         command.Principal,
		AccessTokenSHA256: integrationCatalogAccessTokenSHA256(command.Principal),
		PublicationIntent: configuration.CatalogPublicationIntent{
			Schema:                             configuration.CatalogPublicationRequestSchema,
			ExpectedConfigurationHeadRevision:  command.ExpectedHeadRevision,
			ExpectedAnalyticsGenerationID:      *command.ExpectedAnalyticsGenerationID,
			ExpectedAnalyticsHeadRevision:      *command.ExpectedAnalyticsHeadRevision,
			ExpectedInputManifestSHA256:        *command.ExpectedInputManifestSHA256,
			ExpectedCurrentModelHeadRevision:   *command.ExpectedCurrentModelHeadRevision,
			ExpectedCurrentModelArtifactSHA256: *command.ExpectedCurrentModelArtifactSHA256,
			TargetCatalogSHA256:                command.TargetCatalogSHA256,
			TargetModelArtifactSHA256:          command.TargetModelArtifactSHA256,
			TargetApplicationVersion:           command.TargetApplicationVersion,
			TargetApplicationCommit:            command.TargetApplicationCommit,
			TargetApplicationBuildTime:         command.TargetApplicationBuildTime,
		},
		Document: command.Document,
	}
}

func integrationCatalogAccessTokenSHA256(principal auth.AccessPrincipal) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte("recommendation-integration."+principal.JWTID)))
}

func publishRecommendationIntegrationWithConfigurationFence(
	t *testing.T,
	ctx context.Context,
	runtimePool *pgxpool.Pool,
	publisherService *configuration.Service,
	command configuration.CreateVersionCommand,
) configuration.CreateVersionResult {
	t.Helper()
	configurationLock, err := runtimePool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	released := false
	defer func() {
		if !released {
			_ = configurationLock.Rollback(context.Background())
		}
	}()
	var configurationID string
	if err := configurationLock.QueryRow(ctx, `
SELECT public_id::text
FROM ascendany.configuration_items
WHERE configuration_key = 'recommendation.catalog.active'
FOR UPDATE`).Scan(&configurationID); err != nil {
		t.Fatal(err)
	}
	if configurationID == "" {
		t.Fatal("catalog configuration identity is empty")
	}

	published := make(chan recommendationIntegrationPublicationResult, 1)
	go func() {
		result, publishErr := publisherService.CreateVersion(ctx, command)
		published <- recommendationIntegrationPublicationResult{result: result, err: publishErr}
	}()
	waitForRecommendationIntegrationAnalyticsFence(t, ctx, runtimePool, published)
	_, blockedErr := advanceRecommendationIntegrationAnalyticsHead(ctx, runtimePool, 250*time.Millisecond)
	var lockError *pgconn.PgError
	if !errors.As(blockedErr, &lockError) || lockError.Code != "55P03" {
		t.Fatalf("analytics head advance did not reach the database publication fence: %v", blockedErr)
	}
	if err := configurationLock.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	released = true
	select {
	case publication := <-published:
		if publication.err != nil {
			t.Fatal(publication.err)
		}
		return publication.result
	case <-ctx.Done():
		t.Fatal(ctx.Err())
		return configuration.CreateVersionResult{}
	}
}

func waitForRecommendationIntegrationAnalyticsFence(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	published <-chan recommendationIntegrationPublicationResult,
) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var singleton bool
		err = tx.QueryRow(ctx, `
SELECT singleton
FROM ascendany.analytics_head
WHERE singleton
FOR UPDATE NOWAIT`).Scan(&singleton)
		rollbackErr := tx.Rollback(context.Background())
		var databaseError *pgconn.PgError
		if errors.As(err, &databaseError) && databaseError.Code == "55P03" {
			return
		}
		if err != nil {
			t.Fatalf("probe publication analytics fence: %v", err)
		}
		if rollbackErr != nil {
			t.Fatal(rollbackErr)
		}
		select {
		case result := <-published:
			t.Fatalf("catalog publication completed before configuration lock release: result=%#v error=%v", result.result, result.err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	t.Fatal("catalog publication did not acquire the analytics head before the configuration table")
}

func createRecommendationIntegrationAdminSession(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	principal auth.AccessPrincipal,
) auth.AccessPrincipal {
	t.Helper()
	rotated := principal
	if err := pool.QueryRow(ctx, `
INSERT INTO ascendany.auth_sessions (
    public_id,
    account_id,
    auth_revision,
    created_at,
    expires_at,
    last_seen_at
)
SELECT gen_random_uuid(),
       account_id,
       auth_revision,
       clock_timestamp(),
       clock_timestamp() + interval '15 minutes',
       clock_timestamp()
FROM ascendany.auth_accounts
WHERE public_id = $1::uuid
  AND role = 'admin'
  AND auth_revision = $2
  AND disabled_at IS NULL
RETURNING public_id::text, gen_random_uuid()::text, expires_at`, principal.AccountID, principal.AuthRevision).Scan(
		&rotated.SessionID,
		&rotated.JWTID,
		&rotated.ExpiresAt,
	); err != nil {
		t.Fatal(err)
	}
	rotated.ExpiresAt = rotated.ExpiresAt.UTC()
	if rotated.SessionID == principal.SessionID || rotated.JWTID == "" {
		t.Fatalf("rotated administrator principal=%#v", rotated)
	}
	return rotated
}

func revokeRecommendationIntegrationAdminSession(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	principal auth.AccessPrincipal,
) {
	t.Helper()
	command, err := pool.Exec(ctx, `
UPDATE ascendany.auth_sessions
SET revoked_at = clock_timestamp(),
    revocation_reason = 'catalog publication integration replay'
WHERE public_id = $1::uuid
  AND revoked_at IS NULL`, principal.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if err := requireRecommendationIntegrationRows(command, 1, "revoke catalog publication administrator session"); err != nil {
		t.Fatal(err)
	}
}

func assertRecommendationIntegrationPublicationForeignKey(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	publication configuration.KnowledgeCatalogPublication,
	targetApplication modelrelease.ApplicationIdentity,
) {
	t.Helper()
	_, err := pool.Exec(ctx, `
INSERT INTO ascendany.recommendation_model_activation_events (
    head_revision,
    recommendation_model_release_id,
    artifact_sha256,
    application_version,
    application_commit,
    application_build_time,
    knowledge_catalog_publication_id
)
VALUES ($1, $2::bigint, $3, $4, $5, $6, $7::bigint)`,
		publication.CurrentModelHeadRevision+1,
		publication.TargetModelReleaseID,
		publication.TargetModelArtifactSHA256,
		targetApplication.Version,
		targetApplication.Commit,
		"2026-07-13T04:00:01Z",
		publication.KnowledgeCatalogPublicationID,
	)
	var databaseError *pgconn.PgError
	if !errors.As(err, &databaseError) || databaseError.Code != "23503" ||
		databaseError.ConstraintName != "recommendation_model_activation_events_catalog_publication_fk" {
		t.Fatalf("mismatched publication activation error=%v", err)
	}
}

func loadRecommendationIntegrationAdmin(t *testing.T, ctx context.Context, pool *pgxpool.Pool) auth.AccessPrincipal {
	t.Helper()
	principal := auth.AccessPrincipal{JWTID: "99999999-9999-4999-8999-999999999999"}
	var role string
	if err := pool.QueryRow(ctx, `
SELECT account.public_id::text,
       session.public_id::text,
       account.role,
       account.auth_revision,
       session.expires_at
FROM ascendany.auth_accounts AS account
JOIN ascendany.auth_sessions AS session
  ON session.account_id = account.account_id
 AND session.auth_revision = account.auth_revision
WHERE account.role = 'admin'
  AND account.disabled_at IS NULL
  AND session.revoked_at IS NULL
  AND session.expires_at > clock_timestamp()
ORDER BY session.session_id DESC
LIMIT 1`).Scan(
		&principal.AccountID,
		&principal.SessionID,
		&role,
		&principal.AuthRevision,
		&principal.ExpiresAt,
	); err != nil {
		t.Fatal(err)
	}
	principal.Role = auth.Role(role)
	principal.ExpiresAt = principal.ExpiresAt.UTC()
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

func loadRecommendationIntegrationCatalog(t *testing.T, path, expectedSHA256 string) json.RawMessage {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	byteSHA256 := fmt.Sprintf("%x", sha256.Sum256(raw))
	_, canonical, domainSHA256, err := parseKnowledgeCatalog(raw)
	if err != nil || byteSHA256 != expectedSHA256 || domainSHA256 != expectedSHA256 || !bytes.Equal(raw, canonical) {
		t.Fatalf(
			"knowledge catalog trust anchors: byte=%s domain=%s expected=%s canonical=%t error=%v",
			byteSHA256,
			domainSHA256,
			expectedSHA256,
			bytes.Equal(raw, canonical),
			err,
		)
	}
	return append(json.RawMessage(nil), raw...)
}

func integrationCatalogCommand(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	principal auth.AccessPrincipal,
	review ReviewContext,
	expectedHeadRevision int64,
	targetApplication modelrelease.ApplicationIdentity,
	document json.RawMessage,
) configuration.CreateVersionCommand {
	t.Helper()
	generationID := strconv.FormatInt(review.AnalyticsGenerationID, 10)
	headRevision := review.AnalyticsHeadRevision
	manifestSHA256 := review.InputManifestSHA256
	var modelHeadRevision int64
	var modelID, modelArtifactSHA256 string
	if err := pool.QueryRow(ctx, `
SELECT head.head_revision,
       release.model_id::text,
       release.artifact_sha256
FROM ascendany.recommendation_model_head AS head
JOIN ascendany.recommendation_model_releases AS release
  ON release.recommendation_model_release_id = head.current_release_id
WHERE head.singleton`).Scan(
		&modelHeadRevision,
		&modelID,
		&modelArtifactSHA256,
	); err != nil {
		t.Fatal(err)
	}
	_, _, catalogSHA256, err := parseKnowledgeCatalog(document)
	if err != nil {
		t.Fatal(err)
	}
	return configuration.CreateVersionCommand{
		Principal: principal, Key: configuration.KnowledgeCatalogKey, Kind: configuration.KindKnowledgeCatalog,
		ExpectedHeadRevision: expectedHeadRevision, ExpectedAnalyticsGenerationID: &generationID,
		ExpectedAnalyticsHeadRevision: &headRevision, ExpectedInputManifestSHA256: &manifestSHA256,
		ExpectedCurrentModelHeadRevision: &modelHeadRevision, ExpectedCurrentModelArtifactSHA256: &modelArtifactSHA256,
		TargetCatalogSHA256: catalogSHA256, TargetModelID: modelID, TargetModelArtifactSHA256: modelArtifactSHA256,
		TargetApplicationVersion: targetApplication.Version, TargetApplicationCommit: targetApplication.Commit,
		TargetApplicationBuildTime: targetApplication.BuildTime,
		SchemaID:                   KnowledgeCatalogSchemaV1, Document: document,
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

func advanceRecommendationIntegrationAnalyticsHead(
	ctx context.Context,
	pool *pgxpool.Pool,
	headLockTimeout time.Duration,
) (int64, error) {
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
	var expectedStudentCount, expectedProblemCount int64
	if err := tx.QueryRow(ctx, `
SELECT (SELECT count(*) FROM ascendany.student_analytics WHERE analytics_generation_id = $1),
       (SELECT count(*) FROM ascendany.problem_analytics WHERE analytics_generation_id = $1)`,
		currentGenerationID,
	).Scan(&expectedStudentCount, &expectedProblemCount); err != nil {
		return 0, err
	}
	if expectedStudentCount == 0 || expectedProblemCount == 0 {
		return 0, errors.New("current analytics generation has incomplete metric rows")
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
	command, err := tx.Exec(ctx, `
INSERT INTO ascendany.analytics_generation_events (
    analytics_generation_id, event_sequence, event_type, payload
)
VALUES ($1, 1, 'queued', jsonb_build_object('attemptCount', 0))`, generationID)
	if err != nil {
		return 0, err
	}
	if err := requireRecommendationIntegrationRows(command, 1, "append queued analytics event"); err != nil {
		return 0, err
	}
	command, err = tx.Exec(ctx, `
UPDATE ascendany.analytics_generations
SET status = 'running', attempt_count = 1, lease_owner = 'catalog-publication-integration',
    lease_expires_at = clock_timestamp() + interval '1 hour', started_at = clock_timestamp()
WHERE analytics_generation_id = $1`, generationID)
	if err != nil {
		return 0, err
	}
	if err := requireRecommendationIntegrationRows(command, 1, "claim replacement analytics generation"); err != nil {
		return 0, err
	}
	command, err = tx.Exec(ctx, `
INSERT INTO ascendany.analytics_generation_events (
    analytics_generation_id, event_sequence, event_type, payload
)
VALUES ($1, 2, 'running', jsonb_build_object(
    'attemptCount', 1,
    'reclaimed', false
))`, generationID)
	if err != nil {
		return 0, err
	}
	if err := requireRecommendationIntegrationRows(command, 1, "append running analytics event"); err != nil {
		return 0, err
	}
	command, err = tx.Exec(ctx, `
INSERT INTO ascendany.student_analytics (analytics_generation_id, actor_id, rating, metrics)
SELECT $1, actor_id, rating, metrics
FROM ascendany.student_analytics
WHERE analytics_generation_id = $2`, generationID, currentGenerationID)
	if err != nil {
		return 0, err
	}
	if err := requireRecommendationIntegrationRows(command, expectedStudentCount, "copy replacement student analytics"); err != nil {
		return 0, err
	}
	command, err = tx.Exec(ctx, `
INSERT INTO ascendany.problem_analytics (
    analytics_generation_id, snapshot_id, problem_set_problem_id, metrics
)
SELECT $1, snapshot_id, problem_set_problem_id, metrics
FROM ascendany.problem_analytics
WHERE analytics_generation_id = $2`, generationID, currentGenerationID)
	if err != nil {
		return 0, err
	}
	if err := requireRecommendationIntegrationRows(command, expectedProblemCount, "copy replacement problem analytics"); err != nil {
		return 0, err
	}
	command, err = tx.Exec(ctx, `
UPDATE ascendany.analytics_generations
SET status = 'succeeded', lease_owner = NULL, lease_expires_at = NULL, finished_at = clock_timestamp()
WHERE analytics_generation_id = $1`, generationID)
	if err != nil {
		return 0, err
	}
	if err := requireRecommendationIntegrationRows(command, 1, "publish replacement analytics generation"); err != nil {
		return 0, err
	}
	command, err = tx.Exec(ctx, `
INSERT INTO ascendany.analytics_generation_events (
    analytics_generation_id, event_sequence, event_type, payload
)
VALUES ($1, 3, 'succeeded', jsonb_build_object(
    'studentCount', $2::bigint,
    'problemCount', $3::bigint,
    'headRevision', $4::bigint
))`, generationID, expectedStudentCount, expectedProblemCount, currentHeadRevision+1)
	if err != nil {
		return 0, err
	}
	if err := requireRecommendationIntegrationRows(command, 1, "append succeeded analytics event"); err != nil {
		return 0, err
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if headLockTimeout > 0 {
		milliseconds := headLockTimeout.Milliseconds()
		if milliseconds < 1 {
			return 0, errors.New("analytics head lock timeout must be at least one millisecond")
		}
		if _, err := tx.Exec(ctx, `SELECT set_config('lock_timeout', $1, true)`, fmt.Sprintf("%dms", milliseconds)); err != nil {
			return 0, err
		}
	}
	command, err = tx.Exec(ctx, `
UPDATE ascendany.analytics_head
SET current_generation_id = $1, head_revision = $2, updated_at = clock_timestamp()
WHERE singleton AND current_generation_id = $3 AND head_revision = $4`,
		generationID, currentHeadRevision+1, currentGenerationID, currentHeadRevision)
	if err != nil {
		return 0, err
	}
	if err := requireRecommendationIntegrationRows(command, 1, "advance replacement analytics head"); err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return generationID, nil
}

func requireRecommendationIntegrationRows(command pgconn.CommandTag, expected int64, operation string) error {
	if command.RowsAffected() != expected {
		return fmt.Errorf("%s affected %d rows; expected %d", operation, command.RowsAffected(), expected)
	}
	return nil
}

func TestPostgresModelBindingMatchesVerifiedArtifact(t *testing.T) {
	databaseURL := os.Getenv("ASCENDANY_TEST_DATABASE_URL")
	modelPath := os.Getenv("ASCENDANY_TEST_RECOMMENDATION_MODEL_PATH")
	modelSHA256 := os.Getenv("ASCENDANY_TEST_RECOMMENDATION_MODEL_SHA256")
	catalogPath := os.Getenv("ASCENDANY_TEST_RECOMMENDATION_CATALOG_PATH")
	catalogSHA256 := os.Getenv("ASCENDANY_TEST_RECOMMENDATION_CATALOG_SHA256")
	if databaseURL == "" || modelPath == "" || modelSHA256 == "" || catalogPath == "" || catalogSHA256 == "" {
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
	application := modelrelease.ApplicationIdentity{
		Version:   "0.2.0-integration",
		Commit:    "2222222222222222222222222222222222222222",
		BuildTime: "2026-07-13T04:00:00Z",
	}
	registered, err := releases.Register(ctx, loaded)
	if err != nil || registered.Activated || registered.HeadRevision != 0 {
		t.Fatalf("registered model release=%#v error=%v", registered, err)
	}
	initial, err := releases.Bind(ctx, loaded, application)
	if err != nil || !initial.Activated || initial.HeadRevision != 1 || initial.ReleaseID != registered.ReleaseID {
		t.Fatalf("initial model activation=%#v registration=%#v error=%v", initial, registered, err)
	}
	var storedReleaseID, storedHeadRevision int64
	var storedArtifactSHA256 string
	var storedApplication modelrelease.ApplicationIdentity
	if err := pool.QueryRow(ctx, `
SELECT head.current_release_id,
       head.head_revision,
       activation.artifact_sha256,
       activation.application_version,
       activation.application_commit,
       activation.application_build_time
FROM ascendany.recommendation_model_head AS head
JOIN ascendany.recommendation_model_activation_events AS activation
  ON activation.head_revision = head.head_revision
 AND activation.recommendation_model_release_id = head.current_release_id
WHERE head.singleton`).Scan(
		&storedReleaseID,
		&storedHeadRevision,
		&storedArtifactSHA256,
		&storedApplication.Version,
		&storedApplication.Commit,
		&storedApplication.BuildTime,
	); err != nil {
		t.Fatal(err)
	}
	if storedReleaseID != initial.ReleaseID || storedHeadRevision != initial.HeadRevision ||
		storedArtifactSHA256 != initial.ArtifactSHA256 || storedApplication != application {
		t.Fatalf("stored model head=%d/%d/%s/%#v initial=%#v application=%#v", storedReleaseID, storedHeadRevision, storedArtifactSHA256, storedApplication, initial, application)
	}
	binding, err := releases.Bind(ctx, loaded, application)
	if err != nil {
		t.Fatal(err)
	}
	if binding.Activated || binding.ReleaseID != storedReleaseID || binding.HeadRevision != storedHeadRevision ||
		binding.ArtifactSHA256 != storedArtifactSHA256 {
		t.Fatalf("current model replay mismatch: stored=%d/%d/%s binding=%#v", storedReleaseID, storedHeadRevision, storedArtifactSHA256, binding)
	}
	current, err := releases.RequireCurrent(ctx, loaded, application)
	if err != nil {
		t.Fatal(err)
	}
	if current.Activated || current.ReleaseID != binding.ReleaseID || current.HeadRevision != binding.HeadRevision ||
		current.ArtifactSHA256 != binding.ArtifactSHA256 {
		t.Fatalf("current model verification mismatch: replay=%#v current=%#v", binding, current)
	}
	var releaseCount, activationCount, headCount int64
	if err := pool.QueryRow(ctx, `
SELECT (SELECT count(*) FROM ascendany.recommendation_model_releases),
       (SELECT count(*) FROM ascendany.recommendation_model_activation_events),
	   (SELECT count(*) FROM ascendany.recommendation_model_head)
`).Scan(&releaseCount, &activationCount, &headCount); err != nil {
		t.Fatal(err)
	}
	if releaseCount != 1 || activationCount != 1 || headCount != 1 || storedHeadRevision != 1 {
		t.Fatalf("releaseCount=%d activationCount=%d headCount=%d headRevision=%d", releaseCount, activationCount, headCount, storedHeadRevision)
	}
	repository, err := NewPostgresRepository(pool, loaded.Model, binding, testAnalyticsConfig(t))
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
	matchingBytesSHA256 := fmt.Sprintf("%x", sha256.Sum256(matchingCatalog))
	_, _, matchingDigest, err := parseKnowledgeCatalog(matchingCatalog)
	if err != nil {
		t.Fatal(err)
	}
	if matchingBytesSHA256 != catalogSHA256 || matchingDigest != matchingBytesSHA256 ||
		matchingDigest != loaded.Model.Manifest().KnowledgeCatalogSHA256 {
		t.Fatalf(
			"catalog byte digest %s and domain digest %s do not match independent trust anchor %s and model %s",
			matchingBytesSHA256,
			matchingDigest,
			catalogSHA256,
			loaded.Model.Manifest().KnowledgeCatalogSHA256,
		)
	}
	state, err := readIntegrationCatalog(ctx, repository)
	if err != nil || state.Available {
		t.Fatalf("missing active catalog was not reported unavailable: state=%#v error=%v", state, err)
	}
	reason := activeCatalogUnavailableReason(state, loaded.Model.Manifest())
	if reason == nil || *reason != UnavailableKnowledge {
		t.Fatalf("missing active catalog reason=%v", reason)
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
