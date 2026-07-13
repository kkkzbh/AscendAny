package configuration

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
	"github.com/kkkzbh/AscendAny/backend/internal/principalguard"
)

type PgxBeginner interface {
	BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
}

type postgresTx interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
	Commit(context.Context) error
	Rollback(context.Context) error
}

type beginTransaction func(context.Context, pgx.TxOptions) (postgresTx, error)

type PostgresRepository struct {
	begin             beginTransaction
	writePrecondition VersionWritePrecondition
}

func NewPostgresRepository(pool PgxBeginner, writePrecondition VersionWritePrecondition) (*PostgresRepository, error) {
	if pool == nil || writePrecondition == nil {
		return nil, configurationError(ErrorInvalidConfiguration, "construct configuration PostgreSQL repository", errors.New("database pool and version write precondition are required"))
	}
	return &PostgresRepository{begin: func(ctx context.Context, options pgx.TxOptions) (postgresTx, error) {
		return pool.BeginTx(ctx, options)
	}, writePrecondition: writePrecondition}, nil
}

func newPostgresRepository(begin beginTransaction, writePrecondition VersionWritePrecondition) (*PostgresRepository, error) {
	if begin == nil || writePrecondition == nil {
		return nil, configurationError(ErrorInvalidConfiguration, "construct configuration PostgreSQL repository", errors.New("transaction beginner and version write precondition are required"))
	}
	return &PostgresRepository{begin: begin, writePrecondition: writePrecondition}, nil
}

type storedItem struct {
	Item
	databaseID      int64
	activeVersionID *int64
}

func (repository *PostgresRepository) CreateCatalogPublicationAuthorization(
	ctx context.Context,
	command CreateCatalogPublicationAuthorizationCommand,
	catalogSHA256 string,
) (CatalogPublicationAuthorizationRecord, error) {
	var result CatalogPublicationAuthorizationRecord
	err := repository.transaction(ctx, "authorize knowledge catalog publication", false, func(tx postgresTx) error {
		actor, err := resolveAdmin(ctx, tx, command.Principal, true)
		if err != nil {
			return err
		}
		existing, found, err := loadCatalogPublicationAuthorization(ctx, tx, command, catalogSHA256, actor)
		if err != nil {
			return err
		}
		if found {
			result = existing
			return nil
		}
		var authorizedAt time.Time
		if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&authorizedAt); err != nil {
			return databaseFailure("read catalog publication authorization time", err)
		}
		authorizedAt = authorizedAt.UTC()
		if !command.Principal.ExpiresAt.After(authorizedAt) {
			return configurationError(
				ErrorPrincipalRejected,
				"authorize knowledge catalog publication",
				errors.New("administrator access token expired before authorization"),
			)
		}
		var targetModelReleaseID int64
		var targetModelID string
		if err := tx.QueryRow(ctx, `
SELECT recommendation_model_release_id,
       model_id::text
FROM ascendany.recommendation_model_releases
WHERE artifact_sha256 = $1
  AND knowledge_catalog_sha256 = $2`,
			command.PublicationIntent.TargetModelArtifactSHA256,
			catalogSHA256,
		).Scan(&targetModelReleaseID, &targetModelID); errors.Is(err, pgx.ErrNoRows) {
			return configurationError(
				ErrorStoredDataInvalid,
				"resolve catalog publication authorization target",
				errors.New("target model release is not registered"),
			)
		} else if err != nil {
			return databaseFailure("resolve catalog publication authorization target", err)
		}
		intent := command.PublicationIntent
		generationID := intent.ExpectedAnalyticsGenerationID
		analyticsHeadRevision := intent.ExpectedAnalyticsHeadRevision
		inputManifestSHA256 := intent.ExpectedInputManifestSHA256
		currentModelHeadRevision := intent.ExpectedCurrentModelHeadRevision
		currentModelArtifactSHA256 := intent.ExpectedCurrentModelArtifactSHA256
		validationCommand := CreateVersionCommand{
			Principal: command.Principal, Key: KnowledgeCatalogKey, Kind: KindKnowledgeCatalog,
			ExpectedHeadRevision:          intent.ExpectedConfigurationHeadRevision,
			ExpectedAnalyticsGenerationID: &generationID, ExpectedAnalyticsHeadRevision: &analyticsHeadRevision,
			ExpectedInputManifestSHA256:        &inputManifestSHA256,
			ExpectedCurrentModelHeadRevision:   &currentModelHeadRevision,
			ExpectedCurrentModelArtifactSHA256: &currentModelArtifactSHA256,
			TargetCatalogSHA256:                catalogSHA256, TargetModelID: targetModelID,
			TargetModelArtifactSHA256:  intent.TargetModelArtifactSHA256,
			TargetApplicationVersion:   intent.TargetApplicationVersion,
			TargetApplicationCommit:    intent.TargetApplicationCommit,
			TargetApplicationBuildTime: intent.TargetApplicationBuildTime,
			SchemaID:                   KnowledgeCatalogSchemaID, Document: command.Document,
		}
		if err := repository.writePrecondition.ValidateVersionWrite(ctx, tx, validationCommand); err != nil {
			if CodeOf(err) == "" {
				return configurationError(ErrorStoredDataInvalid, "validate catalog publication authorization precondition", err)
			}
			return err
		}
		var configurationID string
		var currentConfigurationKind string
		var currentConfigurationHeadRevision int64
		err = tx.QueryRow(ctx, `
SELECT public_id::text,
	   configuration_kind,
	   head_revision
FROM ascendany.configuration_items
WHERE configuration_key = $1
FOR SHARE`, KnowledgeCatalogKey).Scan(
			&configurationID,
			&currentConfigurationKind,
			&currentConfigurationHeadRevision,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			if intent.ExpectedConfigurationHeadRevision != 0 {
				return configurationError(ErrorHeadConflict, "authorize knowledge catalog publication", errors.New("catalog configuration head is absent"))
			}
			configurationID, err = randomUUIDv4()
			if err != nil {
				return configurationError(ErrorStoredDataInvalid, "generate catalog publication configuration ID", err)
			}
		} else if err != nil {
			return databaseFailure("read catalog configuration authorization head", err)
		} else if currentConfigurationKind != string(KindKnowledgeCatalog) ||
			currentConfigurationHeadRevision != intent.ExpectedConfigurationHeadRevision {
			return configurationError(ErrorHeadConflict, "authorize knowledge catalog publication", errors.New("catalog configuration head changed"))
		}
		authorizationID, err := randomUUIDv4()
		if err != nil {
			return configurationError(ErrorStoredDataInvalid, "generate catalog publication authorization ID", err)
		}
		publicationRequest := AuthorizedCatalogPublicationRequest{
			AuthorizationID:          authorizationID,
			CatalogPublicationIntent: intent,
		}
		canonicalRequest, err := CanonicalCatalogPublicationRequest(publicationRequest)
		if err != nil {
			return configurationError(ErrorStoredDataInvalid, "encode catalog publication authorization request", err)
		}
		_, err = tx.Exec(ctx, `
INSERT INTO ascendany.knowledge_catalog_publication_authorizations (
    public_id,
    access_jwt_id,
    access_token_sha256,
    request_canonical_json,
    configuration_public_id,
    expected_configuration_head_revision,
    expected_analytics_generation_id,
    expected_analytics_head_revision,
    expected_input_manifest_sha256,
    expected_current_model_head_revision,
    expected_current_model_artifact_sha256,
    catalog_schema_id,
    catalog_document,
    catalog_sha256,
    target_model_release_id,
    target_model_id,
    target_model_artifact_sha256,
    target_application_version,
    target_application_commit,
    target_application_build_time,
    authorized_account_id,
    authorized_session_id,
    authorized_auth_revision,
    access_token_expires_at,
    authorized_at
) VALUES (
    $1::uuid, $2::uuid, $3, $4, $5::uuid,
    $6, $7::bigint, $8, $9, $10, $11,
    $12, $13::jsonb, $14, $15, $16::uuid, $17,
    $18, $19, $20, $21, $22, $23, $24, $25
)`,
			authorizationID,
			command.Principal.JWTID,
			command.AccessTokenSHA256,
			string(canonicalRequest),
			configurationID,
			intent.ExpectedConfigurationHeadRevision,
			intent.ExpectedAnalyticsGenerationID,
			intent.ExpectedAnalyticsHeadRevision,
			intent.ExpectedInputManifestSHA256,
			intent.ExpectedCurrentModelHeadRevision,
			intent.ExpectedCurrentModelArtifactSHA256,
			KnowledgeCatalogSchemaID,
			string(command.Document),
			catalogSHA256,
			targetModelReleaseID,
			targetModelID,
			intent.TargetModelArtifactSHA256,
			intent.TargetApplicationVersion,
			intent.TargetApplicationCommit,
			intent.TargetApplicationBuildTime,
			actor.AccountDatabaseID,
			actor.SessionDatabaseID,
			actor.AuthRevision,
			command.Principal.ExpiresAt.UTC(),
			authorizedAt,
		)
		if err != nil {
			return databaseFailure("store catalog publication authorization", err)
		}
		result = CatalogPublicationAuthorizationRecord{
			AuthorizationID:    authorizationID,
			ExpiresAt:          command.Principal.ExpiresAt.UTC(),
			PublicationRequest: publicationRequest,
		}
		return nil
	})
	if err != nil {
		return CatalogPublicationAuthorizationRecord{}, err
	}
	return result, nil
}

func loadCatalogPublicationAuthorization(
	ctx context.Context,
	tx postgresTx,
	command CreateCatalogPublicationAuthorizationCommand,
	catalogSHA256 string,
	actor principalguard.Resolved,
) (CatalogPublicationAuthorizationRecord, bool, error) {
	var authorizationID string
	var accessTokenSHA256 string
	var requestText string
	var expiresAt time.Time
	var storedCatalogSHA256 string
	var catalogDocumentMatches bool
	var accountID string
	var sessionID string
	var authRevision int64
	err := tx.QueryRow(ctx, `
SELECT capability.public_id::text,
       capability.access_token_sha256,
       capability.request_canonical_json,
       capability.access_token_expires_at,
       capability.catalog_sha256,
       capability.catalog_document = $2::jsonb,
       account.public_id::text,
       session.public_id::text,
       capability.authorized_auth_revision
FROM ascendany.knowledge_catalog_publication_authorizations AS capability
JOIN ascendany.auth_accounts AS account
  ON account.account_id = capability.authorized_account_id
JOIN ascendany.auth_sessions AS session
  ON session.session_id = capability.authorized_session_id
 AND session.account_id = capability.authorized_account_id
WHERE capability.access_jwt_id = $1::uuid`, command.Principal.JWTID, string(command.Document)).Scan(
		&authorizationID,
		&accessTokenSHA256,
		&requestText,
		&expiresAt,
		&storedCatalogSHA256,
		&catalogDocumentMatches,
		&accountID,
		&sessionID,
		&authRevision,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return CatalogPublicationAuthorizationRecord{}, false, nil
	}
	if err != nil {
		return CatalogPublicationAuthorizationRecord{}, false, databaseFailure("load catalog publication authorization", err)
	}
	request, parseErr := ParseCatalogPublicationRequest(json.RawMessage(requestText))
	if parseErr != nil || accessTokenSHA256 != command.AccessTokenSHA256 || storedCatalogSHA256 != catalogSHA256 ||
		!catalogDocumentMatches || accountID != actor.AccountID || sessionID != actor.SessionID ||
		authRevision != actor.AuthRevision || !expiresAt.Equal(command.Principal.ExpiresAt) ||
		request.AuthorizationID != authorizationID || request.CatalogPublicationIntent != command.PublicationIntent {
		return CatalogPublicationAuthorizationRecord{}, false, configurationError(
			ErrorDocumentConflict,
			"replay catalog publication authorization",
			errors.New("access token already owns a different immutable publication authorization"),
		)
	}
	return CatalogPublicationAuthorizationRecord{
		AuthorizationID:    authorizationID,
		ExpiresAt:          expiresAt.UTC(),
		PublicationRequest: request,
	}, true, nil
}

func (repository *PostgresRepository) LoadItems(ctx context.Context, query ListQuery) (ItemPage, error) {
	var page ItemPage
	err := repository.transaction(ctx, "load configuration items", true, func(tx postgresTx) error {
		if _, err := resolveAdmin(ctx, tx, query.Principal, false); err != nil {
			return err
		}
		var kind *string
		if query.Kind != nil {
			value := string(*query.Kind)
			kind = &value
		}
		rows, err := tx.Query(ctx, `
SELECT configuration_item_id,
       public_id::text,
       configuration_key,
       configuration_kind,
       active_version_id,
       head_revision,
       created_at,
       updated_at
FROM ascendany.configuration_items
WHERE ($1::text IS NULL OR configuration_kind = $1)
  AND ($2::text IS NULL OR configuration_key COLLATE "C" > $2 COLLATE "C")
ORDER BY configuration_key COLLATE "C" ASC
LIMIT $3`, kind, query.AfterKey, query.Limit+1)
		if err != nil {
			return databaseFailure("query configuration items", err)
		}
		items := make([]storedItem, 0, query.Limit+1)
		for rows.Next() {
			item, err := scanStoredItem(rows)
			if err != nil {
				rows.Close()
				return databaseFailure("scan configuration item", err)
			}
			items = append(items, item)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return databaseFailure("iterate configuration items", err)
		}
		if len(items) > query.Limit {
			items = items[:query.Limit]
			cursor := items[len(items)-1].Key
			page.NextCursor = &cursor
		}
		page.Items = make([]Item, 0, len(items))
		for _, stored := range items {
			item, err := loadActiveVersion(ctx, tx, stored)
			if err != nil {
				return err
			}
			page.Items = append(page.Items, item)
		}
		return nil
	})
	if err != nil {
		return ItemPage{}, err
	}
	return page, nil
}

func (repository *PostgresRepository) LoadItem(ctx context.Context, query ItemQuery) (Item, bool, error) {
	var item Item
	found := false
	err := repository.transaction(ctx, "load configuration item", true, func(tx postgresTx) error {
		if _, err := resolveAdmin(ctx, tx, query.Principal, false); err != nil {
			return err
		}
		stored, err := scanStoredItem(tx.QueryRow(ctx, itemSelect+`WHERE configuration_key = $1`, query.Key))
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return databaseFailure("query configuration item", err)
		}
		item, err = loadActiveVersion(ctx, tx, stored)
		if err != nil {
			return err
		}
		found = true
		return nil
	})
	if err != nil {
		return Item{}, false, err
	}
	return item, found, nil
}

func (repository *PostgresRepository) LoadVersions(ctx context.Context, query VersionsQuery) (VersionPage, bool, error) {
	var result VersionPage
	found := false
	err := repository.transaction(ctx, "load configuration versions", true, func(tx postgresTx) error {
		if _, err := resolveAdmin(ctx, tx, query.Principal, false); err != nil {
			return err
		}
		stored, err := scanStoredItem(tx.QueryRow(ctx, itemSelect+`WHERE configuration_key = $1`, query.Key))
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return databaseFailure("resolve configuration version item", err)
		}
		rows, err := tx.Query(ctx, versionSelect+`
WHERE version.configuration_item_id = $1
  AND ($2::bigint IS NULL OR version.version_number < $2)
ORDER BY version.version_number DESC
LIMIT $3`, stored.databaseID, query.BeforeNumber, query.Limit+1)
		if err != nil {
			return databaseFailure("query configuration versions", err)
		}
		versions := make([]Version, 0, query.Limit+1)
		for rows.Next() {
			version, _, err := scanVersion(rows, stored.Kind)
			if err != nil {
				rows.Close()
				return configurationError(ErrorStoredDataInvalid, "scan configuration version", err)
			}
			versions = append(versions, version)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return databaseFailure("iterate configuration versions", err)
		}
		page := VersionPage{Key: stored.Key, Kind: stored.Kind, HeadRevision: stored.HeadRevision}
		if len(versions) > query.Limit {
			versions = versions[:query.Limit]
			cursor := versions[len(versions)-1].Number
			page.NextBeforeNumber = &cursor
		}
		page.Items = versions
		result = page
		found = true
		return nil
	})
	if err != nil {
		return VersionPage{}, false, err
	}
	return result, found, nil
}

func (repository *PostgresRepository) StoreVersion(ctx context.Context, request CreateVersionCommand, documentSHA256 string) (CreateVersionResult, error) {
	if request.Kind == KindKnowledgeCatalog || request.Key == KnowledgeCatalogKey {
		if request.Kind != KindKnowledgeCatalog || request.Key != KnowledgeCatalogKey {
			return CreateVersionResult{}, configurationError(ErrorInvalidQuery, "store knowledge catalog version", errors.New("knowledge catalog identity is inconsistent"))
		}
		return repository.storeKnowledgeCatalogVersion(ctx, request, documentSHA256)
	}
	var result CreateVersionResult
	err := repository.transaction(ctx, "store configuration version", false, func(tx postgresTx) error {
		actor, err := resolveAdmin(ctx, tx, request.Principal, true)
		if err != nil {
			return err
		}
		if err := repository.writePrecondition.ValidateVersionWrite(ctx, tx, request); err != nil {
			if CodeOf(err) == "" {
				return configurationError(ErrorStoredDataInvalid, "validate configuration version write precondition", err)
			}
			return err
		}
		stored, err := lockOrCreateItem(ctx, tx, request.Key, request.Kind)
		if err != nil {
			return err
		}
		if stored.Kind != request.Kind {
			return configurationError(ErrorDocumentConflict, "store configuration version", errors.New("configuration key is owned by another kind"))
		}
		existing, existingID, found, err := findVersionByHash(ctx, tx, stored, documentSHA256)
		if err != nil {
			return err
		}
		if found {
			if existing.SchemaID != request.SchemaID || !sameOptionalString(existing.CredentialRef, request.CredentialRef) {
				return configurationError(ErrorDocumentConflict, "store configuration version", errors.New("document hash already exists with different immutable metadata"))
			}
			if stored.activeVersionID == nil || *stored.activeVersionID != existingID {
				return configurationError(ErrorDocumentConflict, "store configuration version", errors.New("document already exists as an inactive immutable version"))
			}
			if request.ExpectedHeadRevision != stored.HeadRevision &&
				(stored.HeadRevision == 0 || request.ExpectedHeadRevision != stored.HeadRevision-1) {
				return configurationError(ErrorHeadConflict, "store configuration version", fmt.Errorf("expected head revision %d, found %d", request.ExpectedHeadRevision, stored.HeadRevision))
			}
			item, err := loadActiveVersion(ctx, tx, stored)
			if err != nil {
				return err
			}
			auditEventID, err := findVersionAuditEventID(ctx, tx, item, existing, request)
			if err != nil {
				return err
			}
			result = CreateVersionResult{Item: item, Idempotent: true, AuditEventID: auditEventID}
			return nil
		}
		if stored.HeadRevision != request.ExpectedHeadRevision {
			return configurationError(ErrorHeadConflict, "store configuration version", fmt.Errorf("expected head revision %d, found %d", request.ExpectedHeadRevision, stored.HeadRevision))
		}
		var nextNumber int64
		if err := tx.QueryRow(ctx, `
SELECT COALESCE(max(version_number), 0) + 1
FROM ascendany.configuration_versions
WHERE configuration_item_id = $1`, stored.databaseID).Scan(&nextNumber); err != nil {
			return databaseFailure("allocate configuration version number", err)
		}
		var mutationTime time.Time
		if err := tx.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&mutationTime); err != nil {
			return databaseFailure("read configuration mutation time", err)
		}
		mutationTime = mutationTime.UTC()
		var versionID int64
		if err := tx.QueryRow(ctx, `
INSERT INTO ascendany.configuration_versions (
    configuration_item_id,
    configuration_kind,
    version_number,
    schema_id,
    document,
    document_sha256,
    credential_ref,
    created_by_account_id,
    created_by_role,
    created_by_session_id,
    created_at
)
VALUES ($1, $2, $3, $4, $5::jsonb, $6, $7, $8, 'admin', $9, $10)
RETURNING configuration_version_id`, stored.databaseID, string(request.Kind), nextNumber, request.SchemaID,
			string(request.Document), documentSHA256, request.CredentialRef,
			actor.AccountDatabaseID, actor.SessionDatabaseID, mutationTime).Scan(&versionID); err != nil {
			return databaseFailure("insert configuration version", err)
		}
		tag, err := tx.Exec(ctx, `
UPDATE ascendany.configuration_items
SET active_version_id = $2,
    head_revision = head_revision + 1,
    updated_at = $3
WHERE configuration_item_id = $1
  AND head_revision = $4`, stored.databaseID, versionID, mutationTime, request.ExpectedHeadRevision)
		if err != nil {
			return databaseFailure("advance configuration head", err)
		}
		if tag.RowsAffected() != 1 {
			return configurationError(ErrorHeadConflict, "advance configuration head", errors.New("configuration head changed concurrently"))
		}
		auditEventID, err := appendVersionAudit(
			ctx, tx, actor, request, documentSHA256, stored.ID, nextNumber, mutationTime,
		)
		if err != nil {
			return err
		}
		stored.activeVersionID = &versionID
		stored.HeadRevision++
		stored.UpdatedAt = mutationTime
		item, err := loadActiveVersion(ctx, tx, stored)
		if err != nil {
			return err
		}
		result = CreateVersionResult{Item: item, AuditEventID: auditEventID}
		return nil
	})
	if err != nil {
		return CreateVersionResult{}, err
	}
	return result, nil
}

func (repository *PostgresRepository) storeKnowledgeCatalogVersion(
	ctx context.Context,
	request CreateVersionCommand,
	documentSHA256 string,
) (CreateVersionResult, error) {
	var result CreateVersionResult
	err := repository.transaction(ctx, "publish knowledge catalog", false, func(tx postgresTx) error {
		var encoded []byte
		err := tx.QueryRow(ctx, `
SELECT ascendany.publish_authorized_knowledge_catalog($1::uuid, $2, $3)`,
			request.PublicationAuthorizationID,
			request.PublicationAccessTokenSHA256,
			string(request.PublicationAuthorizationRequest),
		).Scan(&encoded)
		if err != nil {
			return mapCatalogPublicationDatabaseError(err)
		}
		decoded, err := decodeCatalogPublicationResult(encoded)
		if err != nil {
			return configurationError(ErrorStoredDataInvalid, "decode authorized catalog publication result", err)
		}
		if err := validateCatalogPublicationResult(decoded, request, documentSHA256); err != nil {
			return configurationError(ErrorStoredDataInvalid, "validate authorized catalog publication result", err)
		}
		result = decoded
		return nil
	})
	if err != nil {
		return CreateVersionResult{}, err
	}
	return result, nil
}

func mapCatalogPublicationDatabaseError(err error) error {
	var databaseError *pgconn.PgError
	if errors.As(err, &databaseError) {
		switch databaseError.Code {
		case "28000":
			return configurationError(ErrorPrincipalRejected, "authorize catalog publication capability", err)
		case "40001":
			return configurationError(ErrorHeadConflict, "compare catalog publication release heads", err)
		case "23514":
			return configurationError(ErrorStoredDataInvalid, "enforce catalog publication provenance", err)
		case "23505":
			return configurationError(ErrorDocumentConflict, "reserve catalog publication intent", err)
		}
	}
	return databaseFailure("publish authorized knowledge catalog", err)
}

func decodeCatalogPublicationResult(encoded []byte) (CreateVersionResult, error) {
	var wire struct {
		Item                        Item                        `json:"item"`
		Idempotent                  bool                        `json:"idempotent"`
		AuditEventID                int64                       `json:"auditEventId"`
		KnowledgeCatalogPublication KnowledgeCatalogPublication `json:"knowledgeCatalogPublication"`
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return CreateVersionResult{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return CreateVersionResult{}, errors.New("catalog publication result contains a trailing value")
	}
	if wire.Item.ActiveVersion != nil {
		canonical, digest, err := canonicalDocument(wire.Item.ActiveVersion.Document)
		if err != nil || digest != wire.Item.ActiveVersion.DocumentSHA256 {
			return CreateVersionResult{}, errors.New("catalog publication result contains an invalid configuration document")
		}
		wire.Item.ActiveVersion.Document = canonical
	}
	return CreateVersionResult{
		Item:                        wire.Item,
		Idempotent:                  wire.Idempotent,
		AuditEventID:                wire.AuditEventID,
		KnowledgeCatalogPublication: &wire.KnowledgeCatalogPublication,
	}, nil
}

func validateCatalogPublicationResult(
	result CreateVersionResult,
	request CreateVersionCommand,
	documentSHA256 string,
) error {
	publication := result.KnowledgeCatalogPublication
	active := result.Item.ActiveVersion
	if publication == nil || active == nil || result.AuditEventID < 1 ||
		publication.AuthorizationID != request.PublicationAuthorizationID ||
		!validPositiveInt64String(publication.KnowledgeCatalogPublicationID) ||
		!validPositiveInt64String(publication.TargetModelReleaseID) ||
		publication.CatalogSHA256 != documentSHA256 || publication.CatalogSHA256 != request.TargetCatalogSHA256 ||
		publication.TargetModelArtifactSHA256 != request.TargetModelArtifactSHA256 ||
		publication.TargetModelID != request.TargetModelID ||
		publication.TargetApplicationVersion != request.TargetApplicationVersion ||
		publication.TargetApplicationCommit != request.TargetApplicationCommit ||
		publication.TargetApplicationBuildTime != request.TargetApplicationBuildTime ||
		result.Item.ID != publication.ConfigurationID || result.Item.Key != request.Key ||
		result.Item.Kind != request.Kind || result.Item.HeadRevision != publication.ConfigurationHeadRevision ||
		publication.ExpectedConfigurationHeadRevision != request.ExpectedHeadRevision ||
		publication.ConfigurationMutated && publication.ConfigurationHeadRevision != request.ExpectedHeadRevision+1 ||
		!publication.ConfigurationMutated && publication.ConfigurationHeadRevision != request.ExpectedHeadRevision ||
		publication.ConfigurationVersionID != active.ID || publication.ConfigurationVersionNumber != active.Number ||
		active.SchemaID != request.SchemaID || active.CredentialRef != nil || active.DocumentSHA256 != documentSHA256 ||
		!bytes.Equal(active.Document, request.Document) ||
		publication.AnalyticsGenerationID != *request.ExpectedAnalyticsGenerationID ||
		publication.AnalyticsHeadRevision != *request.ExpectedAnalyticsHeadRevision ||
		publication.InputManifestSHA256 != *request.ExpectedInputManifestSHA256 ||
		publication.CurrentModelHeadRevision != *request.ExpectedCurrentModelHeadRevision ||
		publication.CurrentModelArtifactSHA256 != *request.ExpectedCurrentModelArtifactSHA256 ||
		publication.PublishedByAccountID != request.Principal.AccountID ||
		publication.PublishedBySessionID != request.Principal.SessionID ||
		!validUTCTime(publication.PublishedAt) || publication.AuditEventID != result.AuditEventID ||
		publication.ConfigurationMutated && active.CreatedByAccountID != publication.PublishedByAccountID ||
		publication.ConfigurationMutated && active.CreatedBySessionID != publication.PublishedBySessionID ||
		publication.ConfigurationMutated && !active.CreatedAt.Equal(publication.PublishedAt) {
		return errors.New("database result differs from the authorized catalog publication")
	}
	if err := validateItem(result.Item); err != nil {
		return err
	}
	return nil
}

const itemSelect = `
SELECT configuration_item_id,
       public_id::text,
       configuration_key,
       configuration_kind,
       active_version_id,
       head_revision,
       created_at,
       updated_at
FROM ascendany.configuration_items
`

func scanStoredItem(scanner interface{ Scan(...any) error }) (storedItem, error) {
	var stored storedItem
	var kind string
	if err := scanner.Scan(&stored.databaseID, &stored.ID, &stored.Key, &kind, &stored.activeVersionID,
		&stored.HeadRevision, &stored.CreatedAt, &stored.UpdatedAt); err != nil {
		return storedItem{}, err
	}
	stored.Kind = Kind(kind)
	stored.CreatedAt = stored.CreatedAt.UTC()
	stored.UpdatedAt = stored.UpdatedAt.UTC()
	return stored, nil
}

const versionSelect = `
SELECT version.configuration_version_id,
       version.version_number,
       version.schema_id,
       version.document::text,
       version.document_sha256,
       version.credential_ref,
       account.public_id::text,
       session.public_id::text,
       version.created_at
FROM ascendany.configuration_versions AS version
JOIN ascendany.auth_accounts AS account
  ON account.account_id = version.created_by_account_id
LEFT JOIN ascendany.auth_sessions AS session
  ON session.session_id = version.created_by_session_id
 AND session.account_id = version.created_by_account_id
`

func scanVersion(scanner interface{ Scan(...any) error }, kind Kind) (Version, int64, error) {
	var version Version
	var versionID int64
	var document string
	var sessionID *string
	if err := scanner.Scan(&versionID, &version.Number, &version.SchemaID, &document, &version.DocumentSHA256,
		&version.CredentialRef, &version.CreatedByAccountID, &sessionID, &version.CreatedAt); err != nil {
		return Version{}, 0, err
	}
	if sessionID == nil {
		return Version{}, 0, errors.New("configuration version lacks an administrator session")
	}
	version.CreatedBySessionID = *sessionID
	version.ID = fmt.Sprintf("%d", versionID)
	version.CreatedAt = version.CreatedAt.UTC()
	canonical, digest, err := canonicalDocument(json.RawMessage(document))
	if err != nil || digest != version.DocumentSHA256 {
		return Version{}, 0, errors.New("stored configuration document hash is invalid")
	}
	if err := rejectCredentialFields(kind, canonical); err != nil {
		return Version{}, 0, err
	}
	version.Document = canonical
	return version, versionID, nil
}

func loadActiveVersion(ctx context.Context, tx postgresTx, stored storedItem) (Item, error) {
	item := stored.Item
	if stored.activeVersionID == nil {
		return item, nil
	}
	version, versionID, err := scanVersion(tx.QueryRow(ctx, versionSelect+`
WHERE version.configuration_item_id = $1
  AND version.configuration_version_id = $2`, stored.databaseID, *stored.activeVersionID), stored.Kind)
	if err != nil {
		return Item{}, configurationError(ErrorStoredDataInvalid, "load active configuration version", err)
	}
	if versionID != *stored.activeVersionID {
		return Item{}, configurationError(ErrorStoredDataInvalid, "load active configuration version", errors.New("active version identity changed"))
	}
	item.ActiveVersion = &version
	return item, nil
}

func lockOrCreateItem(ctx context.Context, tx postgresTx, key string, kind Kind) (storedItem, error) {
	stored, err := scanStoredItem(tx.QueryRow(ctx, itemSelect+`WHERE configuration_key = $1 FOR UPDATE`, key))
	if err == nil {
		return stored, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return storedItem{}, databaseFailure("lock configuration item", err)
	}
	publicID, err := randomUUIDv4()
	if err != nil {
		return storedItem{}, configurationError(ErrorStoredDataInvalid, "generate configuration item ID", err)
	}
	stored, err = scanStoredItem(tx.QueryRow(ctx, `
INSERT INTO ascendany.configuration_items (public_id, configuration_key, configuration_kind)
VALUES ($1::uuid, $2, $3)
ON CONFLICT (configuration_key) DO NOTHING
RETURNING configuration_item_id,
          public_id::text,
          configuration_key,
          configuration_kind,
          active_version_id,
          head_revision,
          created_at,
          updated_at`, publicID, key, string(kind)))
	if errors.Is(err, pgx.ErrNoRows) {
		stored, err = scanStoredItem(tx.QueryRow(ctx, itemSelect+`WHERE configuration_key = $1 FOR UPDATE`, key))
	}
	if err != nil {
		return storedItem{}, databaseFailure("create configuration item", err)
	}
	return stored, nil
}

func findVersionByHash(ctx context.Context, tx postgresTx, stored storedItem, digest string) (Version, int64, bool, error) {
	version, versionID, err := scanVersion(tx.QueryRow(ctx, versionSelect+`
WHERE version.configuration_item_id = $1
  AND version.document_sha256 = $2`, stored.databaseID, digest), stored.Kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return Version{}, 0, false, nil
	}
	if err != nil {
		return Version{}, 0, false, configurationError(ErrorStoredDataInvalid, "find configuration version by hash", err)
	}
	return version, versionID, true, nil
}

func appendVersionAudit(
	ctx context.Context,
	tx postgresTx,
	actor principalguard.Resolved,
	request CreateVersionCommand,
	documentSHA256 string,
	itemID string,
	number int64,
	occurredAt time.Time,
) (int64, error) {
	payloadValue := map[string]any{
		"configurationId": itemID,
		"key":             request.Key,
		"kind":            request.Kind,
		"versionNumber":   number,
		"schemaId":        request.SchemaID,
		"documentSha256":  documentSHA256,
		"headRevision":    request.ExpectedHeadRevision + 1,
		"credentialRef":   request.CredentialRef,
	}
	payload, err := json.Marshal(payloadValue)
	if err != nil {
		return 0, configurationError(ErrorStoredDataInvalid, "encode configuration audit event", err)
	}
	var auditEventID int64
	if err := tx.QueryRow(ctx, `
INSERT INTO ascendany.audit_events (account_id, session_id, event_type, occurred_at, payload)
VALUES ($1, $2, 'admin.configuration_version_created', $3, $4::jsonb)
RETURNING audit_event_id`, actor.AccountDatabaseID, actor.SessionDatabaseID, occurredAt, string(payload)).Scan(&auditEventID); err != nil {
		return 0, databaseFailure("append configuration audit event", err)
	}
	if auditEventID < 1 {
		return 0, configurationError(ErrorStoredDataInvalid, "append configuration audit event", errors.New("database returned an invalid audit event ID"))
	}
	return auditEventID, nil
}

func findVersionAuditEventID(ctx context.Context, tx postgresTx, item Item, version Version, request CreateVersionCommand) (int64, error) {
	var auditEventID int64
	var count int64
	if err := tx.QueryRow(ctx, `
SELECT COALESCE(min(audit.audit_event_id), 0), count(*)
FROM ascendany.audit_events AS audit
JOIN ascendany.auth_accounts AS account
  ON account.account_id = audit.account_id
JOIN ascendany.auth_sessions AS session
  ON session.session_id = audit.session_id
 AND session.account_id = audit.account_id
WHERE audit.event_type = 'admin.configuration_version_created'
  AND account.public_id = $1::uuid
  AND session.public_id = $2::uuid
  AND audit.occurred_at = $3
  AND audit.payload ->> 'configurationId' = $4
  AND audit.payload ->> 'key' = $5
  AND audit.payload ->> 'kind' = $6
  AND audit.payload ->> 'versionNumber' = $7
  AND audit.payload ->> 'schemaId' = $8
  AND audit.payload ->> 'documentSha256' = $9
	AND audit.payload ->> 'headRevision' = $10`,
		version.CreatedByAccountID,
		version.CreatedBySessionID,
		version.CreatedAt,
		item.ID,
		item.Key,
		string(item.Kind),
		strconv.FormatInt(version.Number, 10),
		version.SchemaID,
		version.DocumentSHA256,
		strconv.FormatInt(item.HeadRevision, 10),
	).Scan(&auditEventID, &count); err != nil {
		return 0, databaseFailure("resolve configuration version audit event", err)
	}
	if count != 1 || auditEventID < 1 {
		return 0, configurationError(ErrorStoredDataInvalid, "resolve configuration version audit event", errors.New("configuration version audit ownership is ambiguous"))
	}
	return auditEventID, nil
}

func resolveAdmin(ctx context.Context, tx postgresTx, principal auth.AccessPrincipal, lock bool) (principalguard.Resolved, error) {
	var resolved principalguard.Resolved
	var err error
	if lock {
		resolved, err = principalguard.ResolveForUpdate(ctx, tx, principal, principalguard.Roles(auth.RoleAdmin))
	} else {
		resolved, err = principalguard.Resolve(ctx, tx, principal, principalguard.Roles(auth.RoleAdmin))
	}
	if err == nil {
		return resolved, nil
	}
	switch principalguard.CodeOf(err) {
	case principalguard.ErrorRejected:
		return principalguard.Resolved{}, configurationError(ErrorPrincipalRejected, "revalidate configuration principal", err)
	case principalguard.ErrorCanceled:
		return principalguard.Resolved{}, configurationError(ErrorCanceled, "revalidate configuration principal", err)
	case principalguard.ErrorDatabase:
		return principalguard.Resolved{}, configurationError(ErrorDatabase, "revalidate configuration principal", err)
	default:
		return principalguard.Resolved{}, configurationError(ErrorStoredDataInvalid, "revalidate configuration principal", err)
	}
}

func (repository *PostgresRepository) transaction(ctx context.Context, operation string, readOnly bool, run func(postgresTx) error) (resultErr error) {
	options := pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}
	if !readOnly {
		options = pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadWrite}
	}
	tx, err := repository.begin(ctx, options)
	if err != nil {
		return databaseFailure("begin "+operation, err)
	}
	finished := false
	defer func() {
		if finished {
			return
		}
		rollbackContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if rollbackErr := tx.Rollback(rollbackContext); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			wrapped := databaseFailure("rollback "+operation, rollbackErr)
			if resultErr == nil {
				resultErr = wrapped
			} else {
				resultErr = errors.Join(resultErr, wrapped)
			}
		}
	}()
	if err := run(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return databaseFailure("commit "+operation, err)
	}
	finished = true
	return nil
}

func randomUUIDv4() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	raw[6] = raw[6]&0x0f | 0x40
	raw[8] = raw[8]&0x3f | 0x80
	encoded := hex.EncodeToString(raw[:])
	return encoded[:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:], nil
}
