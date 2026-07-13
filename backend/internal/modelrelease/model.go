// Package modelrelease owns immutable database provenance and activation for a
// release-bound inference model.
package modelrelease

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/kkkzbh/AscendAny/backend/internal/canonicaljson"
	"github.com/kkkzbh/AscendAny/backend/internal/configuration"
	"github.com/kkkzbh/AscendAny/backend/internal/inferencemodel"
	"github.com/kkkzbh/AscendAny/backend/internal/modelartifact"
	"github.com/kkkzbh/AscendAny/backend/internal/releaseidentity"
)

// ModelTransitionAdvisoryLockID serializes release registration, catalog
// publication, and model-head activation across their database transactions.
const ModelTransitionAdvisoryLockID int64 = 0x4153434d4f44454c

var (
	ErrInvalidConfiguration   = errors.New("invalid model release configuration")
	ErrStoredDataInvalid      = errors.New("invalid stored model release")
	ErrActivationUnauthorized = errors.New("model activation requires an unconsumed catalog publication")
)

type PgxBeginner interface {
	BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
}

type ApplicationIdentity struct {
	Version   string
	Commit    string
	BuildTime string
}

type Binding struct {
	ReleaseID      int64
	HeadRevision   int64
	Activated      bool
	ModelPurpose   inferencemodel.Purpose
	ArtifactSHA256 string
	ManifestJSON   json.RawMessage
	ManifestSHA256 string
}

type Repository struct {
	pool PgxBeginner
}

func NewRepository(pool PgxBeginner) (*Repository, error) {
	if pool == nil {
		return nil, fmt.Errorf("%w: database pool is required", ErrInvalidConfiguration)
	}
	return &Repository{pool: pool}, nil
}

// Register persists or verifies one immutable inference model release without
// creating or advancing the current model head. Registration is the durable
// identity boundary used by a later catalog publication intent.
func (repository *Repository) Register(
	ctx context.Context,
	loaded modelartifact.Loaded,
) (binding Binding, resultErr error) {
	release, err := prepareRelease(loaded)
	if err != nil {
		return Binding{}, err
	}
	tx, err := repository.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return Binding{}, fmt.Errorf("begin model release registration: %w", err)
	}
	finished := false
	defer func() {
		if finished {
			return
		}
		rollbackContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if rollbackErr := tx.Rollback(rollbackContext); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			if resultErr == nil {
				resultErr = fmt.Errorf("rollback model release registration: %w", rollbackErr)
			} else {
				resultErr = errors.Join(resultErr, fmt.Errorf("rollback model release registration: %w", rollbackErr))
			}
		}
	}()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, ModelTransitionAdvisoryLockID); err != nil {
		return Binding{}, fmt.Errorf("lock model release registration: %w", err)
	}
	releaseID, err := ensureRelease(ctx, tx, release)
	if err != nil {
		return Binding{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Binding{}, fmt.Errorf("commit model release registration: %w", err)
	}
	finished = true
	return releaseBinding(releaseID, 0, false, release), nil
}

func (repository *Repository) Bind(
	ctx context.Context,
	loaded modelartifact.Loaded,
	application ApplicationIdentity,
) (binding Binding, resultErr error) {
	release, err := prepareRelease(loaded)
	if err != nil {
		return Binding{}, err
	}
	if err := validateApplicationIdentity(application); err != nil {
		return Binding{}, err
	}
	// The transaction-scoped advisory lock is the serialization boundary. Under
	// READ COMMITTED, each subsequent data-reading statement takes a new snapshot
	// after the waiter acquires the lock and sees its predecessor's committed state.
	tx, err := repository.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return Binding{}, fmt.Errorf("begin model release binding: %w", err)
	}
	finished := false
	defer func() {
		if finished {
			return
		}
		rollbackContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if rollbackErr := tx.Rollback(rollbackContext); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			if resultErr == nil {
				resultErr = fmt.Errorf("rollback model release binding: %w", rollbackErr)
			} else {
				resultErr = errors.Join(resultErr, fmt.Errorf("rollback model release binding: %w", rollbackErr))
			}
		}
	}()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, ModelTransitionAdvisoryLockID); err != nil {
		return Binding{}, fmt.Errorf("lock model release binding: %w", err)
	}
	releaseID, err := requireRelease(ctx, tx, release)
	if err != nil {
		return Binding{}, err
	}
	headRevision, activated, err := ensureHead(ctx, tx, releaseID, release, application)
	if err != nil {
		return Binding{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Binding{}, fmt.Errorf("commit model release binding: %w", err)
	}
	finished = true
	return releaseBinding(releaseID, headRevision, activated, release), nil
}

// RequireCurrent resolves the already-activated release for the verified
// model and application identity without mutating durable state.
func (repository *Repository) RequireCurrent(
	ctx context.Context,
	loaded modelartifact.Loaded,
	application ApplicationIdentity,
) (binding Binding, resultErr error) {
	release, err := prepareRelease(loaded)
	if err != nil {
		return Binding{}, err
	}
	if err := validateApplicationIdentity(application); err != nil {
		return Binding{}, err
	}
	tx, err := repository.pool.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:   pgx.ReadCommitted,
		AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return Binding{}, fmt.Errorf("begin current model release verification: %w", err)
	}
	finished := false
	defer func() {
		if finished {
			return
		}
		rollbackContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if rollbackErr := tx.Rollback(rollbackContext); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			if resultErr == nil {
				resultErr = fmt.Errorf("rollback current model release verification: %w", rollbackErr)
			} else {
				resultErr = errors.Join(resultErr, fmt.Errorf("rollback current model release verification: %w", rollbackErr))
			}
		}
	}()

	// READ COMMITTED takes a fresh snapshot after this shared lock is granted.
	// Register, publication, and activation all hold the matching exclusive
	// transaction lock, so startup cannot accept their pre-commit state.
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock_shared($1)`, ModelTransitionAdvisoryLockID); err != nil {
		return Binding{}, fmt.Errorf("lock current model release verification: %w", err)
	}
	releaseID, err := requireRelease(ctx, tx, release)
	if err != nil {
		return Binding{}, err
	}
	var currentReleaseID, headRevision int64
	var artifactSHA256, version, commit, buildTime string
	if err := tx.QueryRow(ctx, `
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
		&currentReleaseID,
		&headRevision,
		&artifactSHA256,
		&version,
		&commit,
		&buildTime,
	); err != nil {
		return Binding{}, fmt.Errorf("read current model activation: %w", err)
	}
	if currentReleaseID != releaseID || artifactSHA256 != release.artifactSHA256 ||
		version != application.Version || commit != application.Commit || buildTime != application.BuildTime {
		return Binding{}, fmt.Errorf("%w: current model activation differs from the verified release", ErrStoredDataInvalid)
	}
	if err := RequireCurrentActivationCatalog(ctx, tx, CurrentActivationExpectation{
		ReleaseID: releaseID, HeadRevision: headRevision,
		ArtifactSHA256: release.artifactSHA256, KnowledgeCatalogSHA256: release.knowledgeCatalogSHA256,
		Application: application,
	}); err != nil {
		return Binding{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Binding{}, fmt.Errorf("commit current model release verification: %w", err)
	}
	finished = true
	return releaseBinding(releaseID, headRevision, false, release), nil
}

func releaseBinding(releaseID, headRevision int64, activated bool, release preparedRelease) Binding {
	return Binding{
		ReleaseID: releaseID, HeadRevision: headRevision, Activated: activated,
		ModelPurpose: release.modelPurpose, ArtifactSHA256: release.artifactSHA256,
		ManifestJSON: append(json.RawMessage(nil), release.manifestJSON...), ManifestSHA256: release.manifestSHA256,
	}
}

type preparedRelease struct {
	modelID                  string
	modelPurpose             inferencemodel.Purpose
	artifactSHA256           string
	artifactSizeBytes        int64
	artifactMode             int64
	modelSchema              string
	algorithm                string
	inferenceContract        string
	trainedAt                time.Time
	trainedAtText            string
	trainingProvenanceSHA256 string
	featureSchemaSHA256      string
	knowledgeCatalogSHA256   string
	parameterSHA256          string
	goldenVectorsSHA256      string
	manifestJSON             json.RawMessage
	manifestSHA256           string
}

type manifestWire struct {
	Schema                   string   `json:"schema"`
	ModelID                  string   `json:"modelId"`
	Purpose                  string   `json:"purpose"`
	TrainedAt                string   `json:"trainedAt"`
	Algorithm                string   `json:"algorithm"`
	InferenceContract        string   `json:"inferenceContract"`
	TrainingProvenanceSHA256 string   `json:"trainingProvenanceSha256"`
	FeatureSchemaSHA256      string   `json:"featureSchemaSha256"`
	KnowledgeCatalogSHA256   string   `json:"knowledgeCatalogSha256"`
	ParameterSHA256          string   `json:"parameterSha256"`
	GoldenVectorsSHA256      string   `json:"goldenVectorsSha256"`
	ActorFeatureIDs          []string `json:"actorFeatureIds"`
	ProblemFeatureIDs        []string `json:"problemFeatureIds"`
	KnowledgePointIDs        []string `json:"knowledgePointIds"`
}

func prepareRelease(loaded modelartifact.Loaded) (preparedRelease, error) {
	if loaded.Model == nil || loaded.SHA256 == "" || loaded.SHA256 != loaded.Model.SHA256() ||
		loaded.Size < 1 || loaded.Size > inferencemodel.MaximumArtifactBytes || loaded.Mode != modelartifact.RequiredMode {
		return preparedRelease{}, fmt.Errorf("%w: verified model artifact metadata is inconsistent", ErrInvalidConfiguration)
	}
	manifest := loaded.Model.Manifest()
	trainedAt, err := time.Parse(time.RFC3339Nano, manifest.TrainedAt)
	if err != nil {
		return preparedRelease{}, fmt.Errorf("%w: parsed model trainedAt is invalid", ErrInvalidConfiguration)
	}
	wire := manifestWire{
		Schema: inferencemodel.Schema, ModelID: manifest.ModelID, Purpose: string(manifest.Purpose), TrainedAt: manifest.TrainedAt,
		Algorithm: manifest.Algorithm, InferenceContract: manifest.InferenceContract,
		TrainingProvenanceSHA256: manifest.TrainingProvenanceSHA256,
		FeatureSchemaSHA256:      manifest.FeatureSchemaSHA256, KnowledgeCatalogSHA256: manifest.KnowledgeCatalogSHA256,
		ParameterSHA256: manifest.ParameterSHA256, GoldenVectorsSHA256: manifest.GoldenVectorsSHA256,
		ActorFeatureIDs: manifest.ActorFeatureIDs, ProblemFeatureIDs: manifest.ProblemFeatureIDs,
		KnowledgePointIDs: manifest.KnowledgePointIDs,
	}
	raw, err := json.Marshal(wire)
	if err != nil {
		return preparedRelease{}, fmt.Errorf("%w: encode model manifest: %v", ErrInvalidConfiguration, err)
	}
	canonical, digest, err := canonicaljson.Object(raw, 1<<20)
	if err != nil {
		return preparedRelease{}, fmt.Errorf("%w: canonicalize model manifest: %v", ErrInvalidConfiguration, err)
	}
	return preparedRelease{
		modelID: manifest.ModelID, modelPurpose: manifest.Purpose,
		artifactSHA256: loaded.SHA256, artifactSizeBytes: loaded.Size,
		artifactMode: int64(loaded.Mode.Perm()), modelSchema: inferencemodel.Schema,
		algorithm: manifest.Algorithm, inferenceContract: manifest.InferenceContract,
		trainedAt: trainedAt.UTC(), trainedAtText: manifest.TrainedAt,
		trainingProvenanceSHA256: manifest.TrainingProvenanceSHA256,
		featureSchemaSHA256:      manifest.FeatureSchemaSHA256, knowledgeCatalogSHA256: manifest.KnowledgeCatalogSHA256,
		parameterSHA256: manifest.ParameterSHA256, goldenVectorsSHA256: manifest.GoldenVectorsSHA256,
		manifestJSON: canonical, manifestSHA256: digest,
	}, nil
}

func validateApplicationIdentity(identity ApplicationIdentity) error {
	if err := releaseidentity.Validate(identity.Version, identity.Commit, identity.BuildTime); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidConfiguration, err)
	}
	return nil
}

func ensureRelease(ctx context.Context, tx pgx.Tx, expected preparedRelease) (int64, error) {
	return resolveRelease(ctx, tx, expected, true)
}

func requireRelease(ctx context.Context, tx pgx.Tx, expected preparedRelease) (int64, error) {
	return resolveRelease(ctx, tx, expected, false)
}

func resolveRelease(ctx context.Context, tx pgx.Tx, expected preparedRelease, create bool) (int64, error) {
	rows, err := tx.Query(ctx, `
SELECT recommendation_model_release_id,
       model_id::text,
       model_purpose,
       artifact_sha256,
       artifact_size_bytes,
       artifact_mode,
       model_schema,
       algorithm,
       inference_contract,
       trained_at,
       training_provenance_sha256,
       feature_schema_sha256,
       knowledge_catalog_sha256,
       parameter_sha256,
       golden_vectors_sha256,
       manifest::text,
       manifest_sha256
FROM ascendany.recommendation_model_releases
WHERE model_id = $1::uuid
   OR artifact_sha256 = $2
ORDER BY recommendation_model_release_id`, expected.modelID, expected.artifactSHA256)
	if err != nil {
		return 0, fmt.Errorf("query model release identity: %w", err)
	}
	defer rows.Close()
	var stored []any
	var releaseID int64
	for rows.Next() {
		if releaseID != 0 {
			return 0, fmt.Errorf("%w: model ID and artifact digest resolve to different releases", ErrStoredDataInvalid)
		}
		var modelID, modelPurpose, artifactSHA256, modelSchema, algorithm, inferenceContract string
		var trainingSHA, featureSHA, catalogSHA, parameterSHA, goldenSHA, manifestText, manifestSHA string
		var size, mode int64
		var trainedAt time.Time
		if err := rows.Scan(&releaseID, &modelID, &modelPurpose, &artifactSHA256, &size, &mode, &modelSchema, &algorithm,
			&inferenceContract, &trainedAt, &trainingSHA, &featureSHA, &catalogSHA, &parameterSHA,
			&goldenSHA, &manifestText, &manifestSHA); err != nil {
			return 0, fmt.Errorf("scan model release identity: %w", err)
		}
		stored = []any{modelID, modelPurpose, artifactSHA256, size, mode, modelSchema, algorithm, inferenceContract,
			trainedAt.UTC(), trainingSHA, featureSHA, catalogSHA, parameterSHA, goldenSHA, manifestText, manifestSHA}
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("iterate model release identity: %w", err)
	}
	if releaseID == 0 {
		if !create {
			return 0, fmt.Errorf("%w: verified model release is not persisted", ErrStoredDataInvalid)
		}
		if err := tx.QueryRow(ctx, `
INSERT INTO ascendany.recommendation_model_releases (
    model_id, model_purpose, artifact_sha256, artifact_size_bytes, artifact_mode,
    model_schema, algorithm, inference_contract, trained_at,
    training_provenance_sha256, feature_schema_sha256, knowledge_catalog_sha256,
    parameter_sha256, golden_vectors_sha256, manifest, manifest_sha256
) VALUES (
    $1::uuid, $2, $3, $4, $5, $6, $7, $8, $9,
    $10, $11, $12, $13, $14, $15::jsonb, $16
)
RETURNING recommendation_model_release_id`,
			expected.modelID, expected.modelPurpose, expected.artifactSHA256, expected.artifactSizeBytes, expected.artifactMode,
			expected.modelSchema, expected.algorithm, expected.inferenceContract, expected.trainedAt,
			expected.trainingProvenanceSHA256, expected.featureSchemaSHA256, expected.knowledgeCatalogSHA256,
			expected.parameterSHA256, expected.goldenVectorsSHA256, string(expected.manifestJSON), expected.manifestSHA256,
		).Scan(&releaseID); err != nil {
			return 0, fmt.Errorf("insert model release: %w", err)
		}
		return releaseID, nil
	}
	storedManifest, storedManifestSHA, err := canonicaljson.Object(json.RawMessage(stored[14].(string)), 1<<20)
	if err != nil || storedManifestSHA != stored[15].(string) || !jsonEqual(storedManifest, expected.manifestJSON) ||
		stored[0] != expected.modelID || stored[1] != string(expected.modelPurpose) || stored[2] != expected.artifactSHA256 ||
		stored[3] != expected.artifactSizeBytes || stored[4] != expected.artifactMode ||
		stored[5] != expected.modelSchema || stored[6] != expected.algorithm || stored[7] != expected.inferenceContract ||
		stored[8].(time.Time).UTC().Format(time.RFC3339Nano) != expected.trainedAtText ||
		stored[9] != expected.trainingProvenanceSHA256 ||
		stored[10] != expected.featureSchemaSHA256 || stored[11] != expected.knowledgeCatalogSHA256 ||
		stored[12] != expected.parameterSHA256 || stored[13] != expected.goldenVectorsSHA256 ||
		stored[15] != expected.manifestSHA256 {
		return 0, fmt.Errorf("%w: persisted release differs from the verified model", ErrStoredDataInvalid)
	}
	return releaseID, nil
}

func ensureHead(
	ctx context.Context,
	tx pgx.Tx,
	releaseID int64,
	release preparedRelease,
	application ApplicationIdentity,
) (int64, bool, error) {
	var currentReleaseID, currentRevision int64
	var currentArtifactSHA256 string
	var pendingPublicationID pgtype.Int8
	err := tx.QueryRow(ctx, `
SELECT head.current_release_id,
       head.head_revision,
       release.artifact_sha256,
       head.pending_catalog_publication_id
FROM ascendany.recommendation_model_head AS head
JOIN ascendany.recommendation_model_releases AS release
  ON release.recommendation_model_release_id = head.current_release_id
WHERE head.singleton
FOR UPDATE OF head`).Scan(&currentReleaseID, &currentRevision, &currentArtifactSHA256, &pendingPublicationID)
	if errors.Is(err, pgx.ErrNoRows) {
		if err := insertActivation(ctx, tx, 1, releaseID, release.artifactSHA256, application, nil); err != nil {
			return 0, false, err
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO ascendany.recommendation_model_head (
    singleton, current_release_id, head_revision
) VALUES (true, $1, 1)`, releaseID); err != nil {
			return 0, false, fmt.Errorf("insert model head: %w", err)
		}
		return 1, true, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("read model head: %w", err)
	}
	var currentActivationArtifactSHA256, version, commit, buildTime string
	if err := tx.QueryRow(ctx, `
SELECT artifact_sha256,
       application_version,
       application_commit,
       application_build_time
FROM ascendany.recommendation_model_activation_events
WHERE head_revision = $1
  AND recommendation_model_release_id = $2`, currentRevision, currentReleaseID).Scan(
		&currentActivationArtifactSHA256, &version, &commit, &buildTime,
	); err != nil {
		return 0, false, fmt.Errorf("%w: read current model activation event: %v", ErrStoredDataInvalid, err)
	}
	if currentActivationArtifactSHA256 != currentArtifactSHA256 {
		return 0, false, fmt.Errorf("%w: current activation artifact differs from the locked model release", ErrStoredDataInvalid)
	}

	if !pendingPublicationID.Valid {
		if currentReleaseID == releaseID &&
			currentArtifactSHA256 == release.artifactSHA256 &&
			version == application.Version && commit == application.Commit && buildTime == application.BuildTime {
			return currentRevision, false, nil
		}
		return 0, false, fmt.Errorf(
			"%w: target release or application differs from the locked current activation",
			ErrActivationUnauthorized,
		)
	}
	publicationID, publicationFound, err := findActivationPublication(
		ctx,
		tx,
		pendingPublicationID.Int64,
		releaseID,
		release,
		currentRevision,
		currentArtifactSHA256,
		application,
	)
	if err != nil {
		return 0, false, err
	}
	if !publicationFound {
		return 0, false, fmt.Errorf(
			"%w: pending publication does not authorize the target release and application",
			ErrActivationUnauthorized,
		)
	}
	nextRevision := currentRevision + 1
	if err := insertActivation(
		ctx,
		tx,
		nextRevision,
		releaseID,
		release.artifactSHA256,
		application,
		&publicationID,
	); err != nil {
		return 0, false, err
	}
	command, err := tx.Exec(ctx, `
UPDATE ascendany.recommendation_model_head
SET current_release_id = $1,
    head_revision = $2,
    pending_catalog_publication_id = NULL,
    updated_at = clock_timestamp()
WHERE singleton
  AND current_release_id = $3
  AND head_revision = $4
  AND pending_catalog_publication_id = $5`,
		releaseID, nextRevision, currentReleaseID, currentRevision, publicationID)
	if err != nil {
		return 0, false, fmt.Errorf("advance model head: %w", err)
	}
	if command.RowsAffected() != 1 {
		return 0, false, errors.New("model head compare-and-swap failed")
	}
	return nextRevision, true, nil
}

func findActivationPublication(
	ctx context.Context,
	tx pgx.Tx,
	pendingPublicationID int64,
	targetReleaseID int64,
	targetRelease preparedRelease,
	currentHeadRevision int64,
	currentArtifactSHA256 string,
	targetApplication ApplicationIdentity,
) (int64, bool, error) {
	type candidatePublication struct {
		publicationID          int64
		configurationItemID    int64
		configurationVersionID int64
		configurationRevision  int64
		analyticsGenerationID  int64
		analyticsRevision      int64
		inputManifestSHA256    string
	}
	var candidate candidatePublication
	err := tx.QueryRow(ctx, `
SELECT publication.knowledge_catalog_publication_id,
       publication.configuration_item_id,
       publication.configuration_version_id,
       publication.configuration_head_revision,
       publication.analytics_generation_id,
       publication.analytics_head_revision,
       publication.input_manifest_sha256
FROM ascendany.knowledge_catalog_publications AS publication
JOIN ascendany.recommendation_model_releases AS target_release
  ON target_release.recommendation_model_release_id = publication.target_model_release_id
 AND target_release.artifact_sha256 = publication.target_model_artifact_sha256
 AND target_release.knowledge_catalog_sha256 = publication.catalog_sha256
JOIN ascendany.configuration_versions AS configuration_version
  ON configuration_version.configuration_item_id = publication.configuration_item_id
 AND configuration_version.configuration_version_id = publication.configuration_version_id
 AND configuration_version.configuration_kind = 'knowledge_catalog'
 AND configuration_version.document_sha256 = publication.catalog_sha256
WHERE publication.knowledge_catalog_publication_id = $1
  AND publication.target_model_release_id = $2
  AND publication.target_model_artifact_sha256 = $3
  AND publication.catalog_sha256 = $4
  AND publication.current_model_head_revision = $5
  AND publication.current_model_artifact_sha256 = $6
  AND publication.target_application_version = $7
  AND publication.target_application_commit = $8
  AND publication.target_application_build_time = $9
  AND NOT EXISTS (
      SELECT 1
      FROM ascendany.recommendation_model_activation_events AS consumed
      WHERE consumed.knowledge_catalog_publication_id = publication.knowledge_catalog_publication_id
	)
`,
		pendingPublicationID,
		targetReleaseID,
		targetRelease.artifactSHA256,
		targetRelease.knowledgeCatalogSHA256,
		currentHeadRevision,
		currentArtifactSHA256,
		targetApplication.Version,
		targetApplication.Commit,
		targetApplication.BuildTime,
	).Scan(
		&candidate.publicationID,
		&candidate.configurationItemID,
		&candidate.configurationVersionID,
		&candidate.configurationRevision,
		&candidate.analyticsGenerationID,
		&candidate.analyticsRevision,
		&candidate.inputManifestSHA256,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("query model activation publication: %w", err)
	}
	if candidate.publicationID != pendingPublicationID || candidate.publicationID < 1 ||
		candidate.configurationItemID < 1 || candidate.configurationVersionID < 1 ||
		candidate.configurationRevision < 1 || candidate.analyticsGenerationID < 1 || candidate.analyticsRevision < 1 {
		return 0, false, fmt.Errorf("%w: activation publication identity is invalid", ErrStoredDataInvalid)
	}

	var analyticsGenerationID, analyticsRevision int64
	var inputManifestSHA256, analyticsStatus string
	err = tx.QueryRow(ctx, `
SELECT head.current_generation_id,
       head.head_revision,
       generation.input_manifest_sha256,
       generation.status
FROM ascendany.analytics_head AS head
JOIN ascendany.analytics_generations AS generation
  ON generation.analytics_generation_id = head.current_generation_id
WHERE head.singleton
FOR UPDATE OF head`).Scan(
		&analyticsGenerationID,
		&analyticsRevision,
		&inputManifestSHA256,
		&analyticsStatus,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("lock model activation analytics head: %w", err)
	}
	if analyticsStatus != "succeeded" || analyticsGenerationID != candidate.analyticsGenerationID ||
		analyticsRevision != candidate.analyticsRevision || inputManifestSHA256 != candidate.inputManifestSHA256 {
		return 0, false, nil
	}

	var activeVersionID, configurationRevision int64
	var configurationKey, configurationKind string
	err = tx.QueryRow(ctx, `
SELECT active_version_id,
       head_revision,
       configuration_key,
       configuration_kind
FROM ascendany.configuration_items
WHERE configuration_item_id = $1
FOR UPDATE`, candidate.configurationItemID).Scan(
		&activeVersionID,
		&configurationRevision,
		&configurationKey,
		&configurationKind,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, false, fmt.Errorf("%w: catalog configuration item is missing", ErrStoredDataInvalid)
	}
	if err != nil {
		return 0, false, fmt.Errorf("lock model activation catalog configuration head: %w", err)
	}
	if activeVersionID != candidate.configurationVersionID || configurationRevision != candidate.configurationRevision ||
		configurationKey != configuration.KnowledgeCatalogKey || configurationKind != string(configuration.KindKnowledgeCatalog) {
		return 0, false, nil
	}
	return candidate.publicationID, true, nil
}

func insertActivation(
	ctx context.Context,
	tx pgx.Tx,
	revision, releaseID int64,
	artifactSHA256 string,
	application ApplicationIdentity,
	publicationID *int64,
) error {
	var publicationValue any
	if publicationID != nil {
		publicationValue = *publicationID
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO ascendany.recommendation_model_activation_events (
    head_revision, recommendation_model_release_id, artifact_sha256,
    application_version, application_commit, application_build_time,
    knowledge_catalog_publication_id
) VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		revision, releaseID, artifactSHA256, application.Version, application.Commit, application.BuildTime, publicationValue,
	); err != nil {
		return fmt.Errorf("insert model activation event: %w", err)
	}
	return nil
}

func jsonEqual(left, right []byte) bool {
	return string(left) == string(right)
}
