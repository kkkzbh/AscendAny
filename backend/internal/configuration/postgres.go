package configuration

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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
	begin beginTransaction
}

func NewPostgresRepository(pool PgxBeginner) (*PostgresRepository, error) {
	if pool == nil {
		return nil, configurationError(ErrorInvalidConfiguration, "construct configuration PostgreSQL repository", errors.New("database pool is required"))
	}
	return &PostgresRepository{begin: func(ctx context.Context, options pgx.TxOptions) (postgresTx, error) {
		return pool.BeginTx(ctx, options)
	}}, nil
}

func newPostgresRepository(begin beginTransaction) (*PostgresRepository, error) {
	if begin == nil {
		return nil, configurationError(ErrorInvalidConfiguration, "construct configuration PostgreSQL repository", errors.New("transaction beginner is required"))
	}
	return &PostgresRepository{begin: begin}, nil
}

type storedItem struct {
	Item
	databaseID      int64
	activeVersionID *int64
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
	var result CreateVersionResult
	err := repository.transaction(ctx, "store configuration version", false, func(tx postgresTx) error {
		actor, err := resolveAdmin(ctx, tx, request.Principal, true)
		if err != nil {
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
			result = CreateVersionResult{Item: item, Idempotent: true}
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
		if err := appendVersionAudit(ctx, tx, actor, request, documentSHA256, stored.ID, nextNumber, mutationTime); err != nil {
			return err
		}
		stored.activeVersionID = &versionID
		stored.HeadRevision++
		stored.UpdatedAt = mutationTime
		item, err := loadActiveVersion(ctx, tx, stored)
		if err != nil {
			return err
		}
		result = CreateVersionResult{Item: item}
		return nil
	})
	if err != nil {
		return CreateVersionResult{}, err
	}
	return result, nil
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

func appendVersionAudit(ctx context.Context, tx postgresTx, actor principalguard.Resolved, request CreateVersionCommand, documentSHA256, itemID string, number int64, occurredAt time.Time) error {
	payload, err := json.Marshal(map[string]any{
		"configurationId": itemID,
		"key":             request.Key,
		"kind":            request.Kind,
		"versionNumber":   number,
		"schemaId":        request.SchemaID,
		"documentSha256":  documentSHA256,
		"headRevision":    request.ExpectedHeadRevision + 1,
		"credentialRef":   request.CredentialRef,
	})
	if err != nil {
		return configurationError(ErrorStoredDataInvalid, "encode configuration audit event", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO ascendany.audit_events (account_id, session_id, event_type, occurred_at, payload)
VALUES ($1, $2, 'admin.configuration_version_created', $3, $4::jsonb)`, actor.AccountDatabaseID, actor.SessionDatabaseID, occurredAt, string(payload)); err != nil {
		return databaseFailure("append configuration audit event", err)
	}
	return nil
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
