package backup

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/kkkzbh/AscendAny/backend/internal/canonicaljson"
	"github.com/kkkzbh/AscendAny/backend/internal/databasecontract"
	"github.com/kkkzbh/AscendAny/backend/internal/inferencemodel"
)

type liveDatabaseSnapshot struct {
	connection  *pgx.Conn
	transaction pgx.Tx
	data        databaseSnapshot
}

const dumpOmittedRowTypeACLContractQuery = `
WITH row_types AS (
    SELECT type.oid, type.typacl, type.typowner
    FROM pg_type AS type
    JOIN pg_namespace AS namespace ON namespace.oid = type.typnamespace
    JOIN pg_class AS relation ON relation.oid = type.typrelid
    WHERE namespace.nspname = 'ascendany'
      AND type.typelem = 0
      AND relation.relkind <> 'c'
), actual AS (
    SELECT type.oid AS object_oid,
           CASE WHEN acl.grantee = 0 THEN 'PUBLIC' ELSE grantee.rolname END AS grantee_name,
           acl.privilege_type,
           acl.is_grantable
    FROM row_types AS type
    CROSS JOIN LATERAL aclexplode(
        COALESCE(type.typacl, acldefault('T', type.typowner))
    ) AS acl
    LEFT JOIN pg_roles AS grantee ON grantee.oid = acl.grantee
), expected AS (
    SELECT type.oid AS object_oid,
           grantee_name,
           'USAGE'::text AS privilege_type,
           false AS is_grantable
    FROM row_types AS type
    CROSS JOIN LATERAL unnest(
        ARRAY['ascendany_owner', 'ascendany_runtime', 'ascendany_backup']::text[]
    ) AS grantees(grantee_name)
), difference AS (
    (SELECT * FROM actual EXCEPT ALL SELECT * FROM expected)
    UNION ALL
    (SELECT * FROM expected EXCEPT ALL SELECT * FROM actual)
)
SELECT count(*) FROM difference`

// pg_dump represents relation row types through their owning relations and
// deliberately omits their typacl. Reconstruct exactly that omitted class;
// standalone composite and named types retain the ACL emitted by pg_dump.
const reconstructDumpOmittedRowTypeACLsSQL = `
DO $reconstruct_dump_omitted_row_type_acls$
DECLARE
    row_type record;
    acl_entry record;
    grantee_sql text;
BEGIN
    FOR row_type IN
        SELECT type.oid, namespace.nspname, type.typname
        FROM pg_type AS type
        JOIN pg_namespace AS namespace ON namespace.oid = type.typnamespace
        JOIN pg_class AS relation ON relation.oid = type.typrelid
        WHERE namespace.nspname = 'ascendany'
          AND type.typelem = 0
          AND relation.relkind <> 'c'
    LOOP
        FOR acl_entry IN
            SELECT DISTINCT acl.grantee, grantee.rolname
            FROM pg_type AS type
            CROSS JOIN LATERAL aclexplode(type.typacl) AS acl
            LEFT JOIN pg_roles AS grantee ON grantee.oid = acl.grantee
            WHERE type.oid = row_type.oid
        LOOP
            grantee_sql := CASE
                WHEN acl_entry.grantee = 0 THEN 'PUBLIC'
                ELSE format('%I', acl_entry.rolname)
            END;
            EXECUTE format(
                'REVOKE ALL PRIVILEGES ON TYPE %I.%I FROM %s',
                row_type.nspname,
                row_type.typname,
                grantee_sql
            );
        END LOOP;

        EXECUTE format(
            'REVOKE ALL PRIVILEGES ON TYPE %I.%I FROM PUBLIC, ascendany_owner',
            row_type.nspname,
            row_type.typname
        );
        EXECUTE format(
            'GRANT USAGE ON TYPE %I.%I TO ascendany_owner, ascendany_runtime, ascendany_backup',
            row_type.nspname,
            row_type.typname
        );
    END LOOP;
END
$reconstruct_dump_omitted_row_type_acls$`

func openDatabaseSnapshot(ctx context.Context, config CreateConfig) (*liveDatabaseSnapshot, error) {
	connectionConfig, err := pgx.ParseConfig(config.DatabaseURL)
	if err != nil {
		return nil, errors.New("parse backup database URL")
	}
	connectionConfig.Password = config.DatabasePassword
	connectionConfig.ConnectTimeout = config.ConnectTimeout
	connection, err := pgx.ConnectConfig(ctx, connectionConfig)
	if err != nil {
		return nil, errors.New("connect to backup database")
	}
	transaction, err := connection.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		connection.Close(context.Background())
		return nil, errors.New("begin backup snapshot transaction")
	}
	snapshot := &liveDatabaseSnapshot{connection: connection, transaction: transaction}
	if err := transaction.QueryRow(ctx, `SELECT pg_export_snapshot()`).Scan(&snapshot.data.ID); err != nil {
		snapshot.Close(context.Background())
		return nil, errors.New("export PostgreSQL backup snapshot")
	}
	if err := databasecontract.Verify(ctx, transaction, databasecontract.SourceSnapshot); err != nil {
		snapshot.Close(context.Background())
		return nil, fmt.Errorf("source database contract rejected: %w", err)
	}
	artifacts, err := readArtifactDescriptors(ctx, transaction)
	if err != nil {
		snapshot.Close(context.Background())
		return nil, err
	}
	migrations, err := readMigrationDescriptors(ctx, transaction)
	if err != nil {
		snapshot.Close(context.Background())
		return nil, err
	}
	snapshot.data.Artifacts = artifacts
	snapshot.data.Migrations = migrations
	model, err := readRecommendationModelDescriptor(ctx, transaction)
	if err != nil {
		snapshot.Close(context.Background())
		return nil, err
	}
	snapshot.data.RecommendationModel = model
	return snapshot, nil
}

type modelManifestDescriptorWire struct {
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

var backupModelIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func readRecommendationModelDescriptor(ctx context.Context, queryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}) (RecommendationModelDescriptor, error) {
	var value RecommendationModelDescriptor
	var manifestText string
	var headCount, releaseCount, activationCount int64
	err := queryer.QueryRow(ctx, `
SELECT release.recommendation_model_release_id,
       head.head_revision,
       release.model_id::text,
       release.model_purpose,
       release.artifact_sha256,
       release.artifact_size_bytes,
       release.artifact_mode,
       release.model_schema,
       release.algorithm,
       release.inference_contract,
       release.trained_at,
       release.training_provenance_sha256,
       release.feature_schema_sha256,
       release.knowledge_catalog_sha256,
       release.parameter_sha256,
       release.golden_vectors_sha256,
       release.manifest::text,
       release.manifest_sha256,
       release.created_at,
       activation.application_version,
       activation.application_commit,
       activation.application_build_time,
       activation.activated_at,
       head.updated_at,
       (SELECT count(*) FROM ascendany.recommendation_model_head),
       (SELECT count(*) FROM ascendany.recommendation_model_releases AS candidate
        WHERE candidate.recommendation_model_release_id = head.current_release_id),
       (SELECT count(*) FROM ascendany.recommendation_model_activation_events AS candidate
        WHERE candidate.head_revision = head.head_revision
          AND candidate.recommendation_model_release_id = head.current_release_id
          AND candidate.artifact_sha256 = release.artifact_sha256)
FROM ascendany.recommendation_model_head AS head
JOIN ascendany.recommendation_model_releases AS release
  ON release.recommendation_model_release_id = head.current_release_id
JOIN ascendany.recommendation_model_activation_events AS activation
  ON activation.head_revision = head.head_revision
 AND activation.recommendation_model_release_id = head.current_release_id
 AND activation.artifact_sha256 = release.artifact_sha256
WHERE head.singleton`).Scan(
		&value.ReleaseID, &value.HeadRevision, &value.ModelID, &value.ModelPurpose, &value.ArtifactSHA256,
		&value.ArtifactSizeBytes, &value.ArtifactMode, &value.ModelSchema, &value.Algorithm,
		&value.InferenceContract, &value.TrainedAt, &value.TrainingProvenanceSHA256,
		&value.FeatureSchemaSHA256, &value.KnowledgeCatalogSHA256, &value.ParameterSHA256,
		&value.GoldenVectorsSHA256, &manifestText, &value.ManifestSHA256, &value.ReleaseCreatedAt,
		&value.ApplicationVersion, &value.ApplicationCommit, &value.ApplicationBuildTime,
		&value.ActivatedAt, &value.HeadUpdatedAt, &headCount, &releaseCount, &activationCount,
	)
	if err != nil {
		return RecommendationModelDescriptor{}, fmt.Errorf("read active recommendation model snapshot: %w", err)
	}
	if headCount != 1 || releaseCount != 1 || activationCount != 1 {
		return RecommendationModelDescriptor{}, errors.New("active recommendation model ownership is ambiguous")
	}
	normalizeRecommendationModelTimestamps(&value)
	canonical, digest, err := canonicaljson.Object(json.RawMessage(manifestText), 1<<20)
	if err != nil || digest != value.ManifestSHA256 {
		return RecommendationModelDescriptor{}, errors.New("active recommendation model manifest is invalid")
	}
	value.Manifest = canonical
	if err := validateRecommendationModelDescriptor(value); err != nil {
		return RecommendationModelDescriptor{}, fmt.Errorf("active recommendation model snapshot rejected: %w", err)
	}
	return value, nil
}

func normalizeRecommendationModelTimestamps(value *RecommendationModelDescriptor) {
	value.TrainedAt = value.TrainedAt.UTC()
	value.ReleaseCreatedAt = value.ReleaseCreatedAt.UTC()
	value.ActivatedAt = value.ActivatedAt.UTC()
	value.HeadUpdatedAt = value.HeadUpdatedAt.UTC()
}

func validateRecommendationModelDescriptor(value RecommendationModelDescriptor) error {
	if value.ReleaseID <= 0 || value.HeadRevision <= 0 || !backupModelIDPattern.MatchString(value.ModelID) ||
		!sha256Pattern.MatchString(value.ArtifactSHA256) || value.ArtifactSizeBytes < 1 || value.ArtifactSizeBytes > inferencemodel.MaximumArtifactBytes ||
		value.ArtifactMode != 0o644 || value.ModelSchema != inferencemodel.Schema || value.Algorithm != inferencemodel.Algorithm ||
		value.InferenceContract != inferencemodel.InferenceContract {
		return errors.New("model release identity is invalid")
	}
	if _, err := inferencemodel.ParsePurpose(value.ModelPurpose); err != nil {
		return errors.New("model purpose is invalid")
	}
	for _, digest := range []string{
		value.TrainingProvenanceSHA256, value.FeatureSchemaSHA256, value.KnowledgeCatalogSHA256,
		value.ParameterSHA256, value.GoldenVectorsSHA256, value.ManifestSHA256,
	} {
		if !sha256Pattern.MatchString(digest) {
			return errors.New("model provenance digest is invalid")
		}
	}
	for _, timestamp := range []time.Time{value.TrainedAt, value.ReleaseCreatedAt, value.ActivatedAt, value.HeadUpdatedAt} {
		if timestamp.IsZero() || timestamp.Location() != time.UTC {
			return errors.New("model provenance timestamp is invalid")
		}
	}
	for _, identity := range []string{value.ApplicationVersion, value.ApplicationCommit, value.ApplicationBuildTime} {
		if identity == "" || strings.TrimSpace(identity) != identity || len(identity) > 128 {
			return errors.New("model application identity is invalid")
		}
	}
	canonical, digest, err := canonicaljson.Object(value.Manifest, 1<<20)
	if err != nil || digest != value.ManifestSHA256 {
		return errors.New("model manifest digest is invalid")
	}
	decoder := json.NewDecoder(bytes.NewReader(canonical))
	decoder.DisallowUnknownFields()
	var manifest modelManifestDescriptorWire
	if err := decoder.Decode(&manifest); err != nil {
		return errors.New("model manifest shape is invalid")
	}
	if err := requireJSONEOF(decoder); err != nil {
		return errors.New("model manifest trailer is invalid")
	}
	if manifest.Schema != value.ModelSchema || manifest.ModelID != value.ModelID || manifest.Purpose != value.ModelPurpose ||
		manifest.TrainedAt != value.TrainedAt.Format(time.RFC3339Nano) || manifest.Algorithm != value.Algorithm ||
		manifest.InferenceContract != value.InferenceContract ||
		manifest.TrainingProvenanceSHA256 != value.TrainingProvenanceSHA256 ||
		manifest.FeatureSchemaSHA256 != value.FeatureSchemaSHA256 ||
		manifest.KnowledgeCatalogSHA256 != value.KnowledgeCatalogSHA256 ||
		manifest.ParameterSHA256 != value.ParameterSHA256 || manifest.GoldenVectorsSHA256 != value.GoldenVectorsSHA256 ||
		len(manifest.ActorFeatureIDs) == 0 || len(manifest.ProblemFeatureIDs) == 0 || len(manifest.KnowledgePointIDs) == 0 {
		return errors.New("model manifest differs from release columns")
	}
	return nil
}

func equalRecommendationModel(left, right RecommendationModelDescriptor) bool {
	leftManifest, leftDigest, leftErr := canonicaljson.Object(left.Manifest, 1<<20)
	rightManifest, rightDigest, rightErr := canonicaljson.Object(right.Manifest, 1<<20)
	if leftErr != nil || rightErr != nil || leftDigest != rightDigest || !bytes.Equal(leftManifest, rightManifest) {
		return false
	}
	left.Manifest = leftManifest
	right.Manifest = rightManifest
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftJSON, rightJSON)
}

func requireExactDumpOmittedRowTypeACLs(ctx context.Context, queryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}) error {
	var violationCount int64
	if err := queryer.QueryRow(ctx, dumpOmittedRowTypeACLContractQuery).Scan(&violationCount); err != nil {
		return errors.New("inspect relation row-type ACL contract")
	}
	if violationCount != 0 {
		return fmt.Errorf("relation row-type ACL contract has %d differences", violationCount)
	}
	return nil
}

func readArtifactDescriptors(ctx context.Context, queryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}) ([]ArtifactDescriptor, error) {
	rows, err := queryer.Query(ctx, `
SELECT sha256, size_bytes, storage_key
FROM ascendany.artifacts
ORDER BY sha256 COLLATE "C"`)
	if err != nil {
		return nil, errors.New("read artifact snapshot")
	}
	defer rows.Close()
	values := make([]ArtifactDescriptor, 0)
	for rows.Next() {
		var value ArtifactDescriptor
		if err := rows.Scan(&value.SHA256, &value.SizeBytes, &value.StorageKey); err != nil {
			return nil, errors.New("decode artifact snapshot")
		}
		if err := validateArtifactDescriptor(value); err != nil {
			return nil, fmt.Errorf("database artifact metadata rejected: %w", err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("read artifact snapshot")
	}
	return values, nil
}

func readMigrationDescriptors(ctx context.Context, queryer interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}) ([]MigrationDescriptor, error) {
	rows, err := queryer.Query(ctx, `
SELECT version, name, sha256
FROM ascendany.schema_migrations_v2
ORDER BY version`)
	if err != nil {
		return nil, errors.New("read migration snapshot")
	}
	defer rows.Close()
	values := make([]MigrationDescriptor, 0)
	for rows.Next() {
		var value MigrationDescriptor
		if err := rows.Scan(&value.Version, &value.Name, &value.SHA256); err != nil {
			return nil, errors.New("decode migration snapshot")
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New("read migration snapshot")
	}
	if err := validateMigrations(values); err != nil {
		return nil, fmt.Errorf("database migration history rejected: %w", err)
	}
	return values, nil
}

func (snapshot *liveDatabaseSnapshot) Data() databaseSnapshot {
	return snapshot.data
}

func (snapshot *liveDatabaseSnapshot) Close(ctx context.Context) error {
	if snapshot == nil {
		return nil
	}
	var transactionError error
	if snapshot.transaction != nil {
		transactionError = snapshot.transaction.Rollback(ctx)
		if errors.Is(transactionError, pgx.ErrTxClosed) {
			transactionError = nil
		}
		snapshot.transaction = nil
	}
	var connectionError error
	if snapshot.connection != nil {
		connectionError = snapshot.connection.Close(ctx)
		snapshot.connection = nil
	}
	return errors.Join(transactionError, connectionError)
}

func connectRestoreDatabase(ctx context.Context, config RestoreConfig) (*pgx.Conn, error) {
	connectionConfig, err := pgx.ParseConfig(config.DatabaseURL)
	if err != nil {
		return nil, errors.New("parse restore database URL")
	}
	connectionConfig.Password = config.DatabasePassword
	connectionConfig.ConnectTimeout = config.ConnectTimeout
	connection, err := pgx.ConnectConfig(ctx, connectionConfig)
	if err != nil {
		return nil, errors.New("connect to restore verification database")
	}
	return connection, nil
}

func requireFreshRestoreDatabase(ctx context.Context, connection *pgx.Conn) error {
	var databaseName string
	var databaseOwner string
	var schemaExists bool
	var allowsConnections bool
	var exactACL bool
	if err := connection.QueryRow(ctx, `
WITH database_boundary AS (
    SELECT database.datname,
           owner.rolname AS owner_name,
           database.datallowconn,
           database.datacl,
           database.datdba
    FROM pg_database AS database
    JOIN pg_roles AS owner ON owner.oid = database.datdba
    WHERE database.datname = current_database()
), actual_acl AS (
    SELECT CASE WHEN acl.grantee = 0 THEN 'PUBLIC' ELSE grantee.rolname END AS grantee_name,
           acl.privilege_type,
           acl.is_grantable
    FROM database_boundary AS database
    CROSS JOIN LATERAL aclexplode(COALESCE(database.datacl, acldefault('d', database.datdba))) AS acl
    LEFT JOIN pg_roles AS grantee ON grantee.oid = acl.grantee
), expected_acl(grantee_name, privilege_type, is_grantable) AS (
    VALUES
        ('ascendany_owner', 'CONNECT', false),
        ('ascendany_owner', 'CREATE', false),
        ('ascendany_owner', 'TEMPORARY', false),
        ('ascendany_restore_login', 'CONNECT', false)
), acl_difference AS (
    (SELECT * FROM actual_acl EXCEPT ALL SELECT * FROM expected_acl)
    UNION ALL
    (SELECT * FROM expected_acl EXCEPT ALL SELECT * FROM actual_acl)
)
SELECT database.datname,
       database.owner_name,
       to_regnamespace('ascendany') IS NOT NULL,
       database.datallowconn,
       NOT EXISTS (SELECT 1 FROM acl_difference)
FROM database_boundary AS database`).Scan(
		&databaseName,
		&databaseOwner,
		&schemaExists,
		&allowsConnections,
		&exactACL,
	); err != nil {
		return errors.New("inspect restore verification database")
	}
	if databaseName != RestoreDatabaseName {
		return fmt.Errorf("restore verification database must be %s", RestoreDatabaseName)
	}
	if databaseOwner != RestoreDatabaseRole || !allowsConnections || !exactACL {
		return errors.New("restore verification database owner or ACL differs from the isolated scratch contract")
	}
	if schemaExists {
		return errors.New("restore verification database must not contain schema ascendany")
	}
	return nil
}

type restoredDatabaseConnection interface {
	BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
}

func reconstructDumpOmittedRowTypeACLs(
	ctx context.Context,
	connection restoredDatabaseConnection,
) error {
	transaction, err := connection.BeginTx(ctx, pgx.TxOptions{
		IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadWrite,
	})
	if err != nil {
		return errors.New("begin relation row-type ACL reconstruction transaction")
	}
	defer transaction.Rollback(context.Background())
	if _, err := transaction.Exec(ctx, `SET LOCAL ROLE ascendany_owner`); err != nil {
		return errors.New("assume restore owner for relation row-type ACL reconstruction")
	}
	if _, err := transaction.Exec(ctx, reconstructDumpOmittedRowTypeACLsSQL); err != nil {
		return errors.New("reconstruct pg_dump-omitted relation row-type ACLs")
	}
	if err := requireExactDumpOmittedRowTypeACLs(ctx, transaction); err != nil {
		return fmt.Errorf("verify reconstructed relation row-type ACLs: %w", err)
	}
	if err := transaction.Commit(ctx); err != nil {
		return errors.New("commit relation row-type ACL reconstruction")
	}
	return nil
}

func beginRestoredDatabaseVerification(ctx context.Context, connection restoredDatabaseConnection) (pgx.Tx, error) {
	transaction, err := connection.BeginTx(ctx, pgx.TxOptions{
		IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return nil, errors.New("begin post-restore verification transaction")
	}
	if _, err := transaction.Exec(ctx, `SET LOCAL ROLE ascendany_owner`); err != nil {
		_ = transaction.Rollback(context.Background())
		return nil, errors.New("assume restore verification owner role")
	}
	return transaction, nil
}

func verifyRestoredDatabase(ctx context.Context, connection restoredDatabaseConnection, manifest Manifest, artifactRoot string) error {
	transaction, err := beginRestoredDatabaseVerification(ctx, connection)
	if err != nil {
		return err
	}
	defer transaction.Rollback(context.Background())
	if err := databasecontract.Verify(ctx, transaction, databasecontract.Restore); err != nil {
		return fmt.Errorf("restored database contract rejected: %w", err)
	}
	migrations, err := readMigrationDescriptors(ctx, transaction)
	if err != nil {
		return err
	}
	if !equalMigrations(migrations, manifest.Database.Migrations) {
		return errors.New("restored migration history does not match the backup manifest")
	}
	artifacts, err := readArtifactDescriptors(ctx, transaction)
	if err != nil {
		return err
	}
	if !equalArtifacts(artifacts, manifest.Artifacts.Entries) {
		return errors.New("restored artifact table does not match the backup manifest")
	}
	model, err := readRecommendationModelDescriptor(ctx, transaction)
	if err != nil {
		return err
	}
	if !equalRecommendationModel(model, manifest.Database.RecommendationModel) {
		return errors.New("restored recommendation model provenance does not match the backup manifest")
	}
	if err := transaction.Commit(ctx); err != nil {
		return errors.New("commit post-restore verification transaction")
	}
	root, err := os.OpenRoot(artifactRoot)
	if err != nil {
		return errors.New("open restored artifact root")
	}
	defer root.Close()
	for _, artifact := range artifacts {
		if err := verifyRootArtifact(root, artifact); err != nil {
			return fmt.Errorf("restored artifact %s failed verification: %w", artifact.SHA256, err)
		}
	}
	return nil
}

func equalMigrations(left, right []MigrationDescriptor) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func equalArtifacts(left, right []ArtifactDescriptor) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
