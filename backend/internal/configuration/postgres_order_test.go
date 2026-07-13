package configuration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

type configurationRowFunc func(...any) error

func (row configurationRowFunc) Scan(destinations ...any) error {
	return row(destinations...)
}

type catalogPublisherTx struct {
	query         string
	arguments     []any
	encoded       []byte
	scanErr       error
	queryRowCount int
	queryCount    int
	execCount     int
	commitCount   int
	rollbackCount int
}

func (tx *catalogPublisherTx) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	tx.execCount++
	return pgconn.CommandTag{}, errors.New("catalog publisher must not execute direct DML")
}

func (tx *catalogPublisherTx) QueryRow(_ context.Context, query string, arguments ...any) pgx.Row {
	tx.queryRowCount++
	tx.query = query
	tx.arguments = append([]any(nil), arguments...)
	return configurationRowFunc(func(destinations ...any) error {
		if tx.scanErr != nil {
			return tx.scanErr
		}
		if len(destinations) != 1 {
			return errors.New("catalog publisher result must have exactly one column")
		}
		encoded, ok := destinations[0].(*[]byte)
		if !ok {
			return errors.New("catalog publisher result must be scanned as JSON bytes")
		}
		*encoded = append((*encoded)[:0], tx.encoded...)
		return nil
	})
}

func (tx *catalogPublisherTx) Query(context.Context, string, ...any) (pgx.Rows, error) {
	tx.queryCount++
	return nil, errors.New("catalog publisher must not issue a rows query")
}

func (tx *catalogPublisherTx) Commit(context.Context) error {
	tx.commitCount++
	return nil
}

func (tx *catalogPublisherTx) Rollback(context.Context) error {
	tx.rollbackCount++
	return nil
}

type publicationOrderTx struct {
	queryRowCount int
	committed     bool
	rolledBack    bool
}

func (tx *publicationOrderTx) QueryRow(_ context.Context, _ string, _ ...any) pgx.Row {
	tx.queryRowCount++
	if tx.queryRowCount != 1 {
		return configurationRowFunc(func(...any) error {
			return errors.New("configuration storage was reached before the publication precondition succeeded")
		})
	}
	return configurationRowFunc(func(destinations ...any) error {
		*(destinations[0].(*int64)) = 11
		*(destinations[1].(*string)) = testConfigurationAccountID
		*(destinations[2].(*string)) = string(auth.RoleAdmin)
		*(destinations[3].(*int64)) = 3
		*(destinations[4].(**int64)) = nil
		*(destinations[5].(**string)) = nil
		*(destinations[6].(*int64)) = 33
		*(destinations[7].(*string)) = testConfigurationSessionID
		*(destinations[8].(*int64)) = 3
		*(destinations[9].(**int64)) = nil
		*(destinations[10].(**string)) = nil
		return nil
	})
}

func (*publicationOrderTx) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, errors.New("unexpected query")
}

func (*publicationOrderTx) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, errors.New("unexpected mutation")
}

func (tx *publicationOrderTx) Commit(context.Context) error {
	tx.committed = true
	return nil
}

func (tx *publicationOrderTx) Rollback(context.Context) error {
	tx.rolledBack = true
	return nil
}

type rejectingPublicationPrecondition struct {
	called bool
}

func (precondition *rejectingPublicationPrecondition) ValidateVersionWrite(context.Context, VersionWriteTransaction, CreateVersionCommand) error {
	precondition.called = true
	return configurationError(ErrorDocumentConflict, "validate publication order", errors.New("publication provenance changed"))
}

const (
	testConfigurationAccountID = "123e4567-e89b-42d3-a456-426614174000"
	testConfigurationSessionID = "123e4567-e89b-42d3-a456-426614174001"
	testConfigurationJWTID     = "123e4567-e89b-42d3-a456-426614174002"
)

func TestPostgresValidatesPublicationBeforeAllocatingConfigurationIdentity(t *testing.T) {
	t.Parallel()
	tx := &publicationOrderTx{}
	precondition := &rejectingPublicationPrecondition{}
	repository, err := newPostgresRepository(func(context.Context, pgx.TxOptions) (postgresTx, error) {
		return tx, nil
	}, precondition)
	if err != nil {
		t.Fatal(err)
	}

	_, err = repository.StoreVersion(context.Background(), CreateVersionCommand{
		Principal: auth.AccessPrincipal{
			AccountID:    testConfigurationAccountID,
			SessionID:    testConfigurationSessionID,
			JWTID:        testConfigurationJWTID,
			Role:         auth.RoleAdmin,
			AuthRevision: 3,
		},
		Key:      "test.prompt.publication-order",
		Kind:     KindPrompt,
		SchemaID: "ascendany.prompt.v1",
		Document: []byte(`{"value":1}`),
	}, "08f271887ce94707da822d5263bae19d5519cb3614fb3bd89123798bbf7d7a2e")
	if CodeOf(err) != ErrorDocumentConflict {
		t.Fatalf("StoreVersion() error=%v code=%q", err, CodeOf(err))
	}
	if !precondition.called || tx.queryRowCount != 1 || tx.committed || !tx.rolledBack {
		t.Fatalf("preconditionCalled=%t queryRows=%d committed=%t rolledBack=%t", precondition.called, tx.queryRowCount, tx.committed, tx.rolledBack)
	}
}

func TestPostgresCatalogPublicationUsesOneDatabasePublisherCall(t *testing.T) {
	t.Parallel()
	command := catalogPublisherCommand(t)
	want := catalogPublisherResult(command)
	tx := &catalogPublisherTx{encoded: encodeCatalogPublisherResult(t, want)}
	precondition := &rejectingPublicationPrecondition{}
	var options pgx.TxOptions
	beginCount := 0
	repository, err := newPostgresRepository(func(_ context.Context, transactionOptions pgx.TxOptions) (postgresTx, error) {
		beginCount++
		options = transactionOptions
		return tx, nil
	}, precondition)
	if err != nil {
		t.Fatal(err)
	}

	got, err := repository.StoreVersion(context.Background(), command, command.TargetCatalogSHA256)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("StoreVersion() result=%#v want=%#v", got, want)
	}
	query := strings.Join(strings.Fields(tx.query), " ")
	const wantQuery = "SELECT ascendany.publish_authorized_knowledge_catalog($1::uuid, $2, $3)"
	if query != wantQuery {
		t.Fatalf("publisher query=%q want=%q", query, wantQuery)
	}
	wantArguments := []any{
		command.PublicationAuthorizationID,
		command.PublicationAccessTokenSHA256,
		string(command.PublicationAuthorizationRequest),
	}
	if !reflect.DeepEqual(tx.arguments, wantArguments) {
		t.Fatalf("publisher arguments=%#v want=%#v", tx.arguments, wantArguments)
	}
	if beginCount != 1 || options != (pgx.TxOptions{IsoLevel: pgx.ReadCommitted, AccessMode: pgx.ReadWrite}) ||
		tx.queryRowCount != 1 || tx.queryCount != 0 || tx.execCount != 0 || tx.commitCount != 1 ||
		tx.rollbackCount != 0 || precondition.called {
		t.Fatalf("begin=%d options=%#v queryRows=%d queries=%d execs=%d commits=%d rollbacks=%d preconditionCalled=%t",
			beginCount, options, tx.queryRowCount, tx.queryCount, tx.execCount, tx.commitCount, tx.rollbackCount, precondition.called)
	}
}

func TestPostgresCatalogPublicationCanonicalizesPostgresJSONBDocument(t *testing.T) {
	t.Parallel()
	command := catalogPublisherCommand(t)
	want := catalogPublisherResult(command)
	encoded := encodeCatalogPublisherResult(t, want)
	encoded = bytes.Replace(encoded, []byte(`"document":{}`), []byte(`"document":{ }`), 1)
	if bytes.Contains(encoded, []byte(`"document":{}`)) {
		t.Fatal("fixture still contains the canonical catalog document")
	}
	tx := &catalogPublisherTx{encoded: encoded}
	repository := newCatalogPublisherRepository(t, tx, &rejectingPublicationPrecondition{})

	got, err := repository.StoreVersion(context.Background(), command, command.TargetCatalogSHA256)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("StoreVersion() result=%#v want=%#v", got, want)
	}
}

func TestPostgresCatalogPublicationStrictlyDecodesPublisherResult(t *testing.T) {
	t.Parallel()
	command := catalogPublisherCommand(t)
	valid := encodeCatalogPublisherResult(t, catalogPublisherResult(command))
	unknownTopLevel := append(append([]byte(nil), valid[:len(valid)-1]...), []byte(`,"unexpected":true}`)...)
	unknownPublication := addCatalogPublisherPublicationField(t, valid, "unexpected", true)

	for name, encoded := range map[string][]byte{
		"malformed JSON":            []byte(`{"item":`),
		"unknown top-level field":   unknownTopLevel,
		"unknown publication field": unknownPublication,
		"trailing JSON value":       append(append([]byte(nil), valid...), []byte(` {}`)...),
	} {
		name, encoded := name, encoded
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			tx := &catalogPublisherTx{encoded: encoded}
			repository := newCatalogPublisherRepository(t, tx, &rejectingPublicationPrecondition{})

			_, err := repository.StoreVersion(context.Background(), command, command.TargetCatalogSHA256)
			if CodeOf(err) != ErrorStoredDataInvalid {
				t.Fatalf("StoreVersion() error=%v code=%q", err, CodeOf(err))
			}
			assertCatalogPublisherRolledBack(t, tx)
		})
	}
}

func TestPostgresCatalogPublicationValidatesExactPublisherResult(t *testing.T) {
	t.Parallel()
	command := catalogPublisherCommand(t)
	credentialRef := "forbidden.catalog.credential"
	nonUTC := time.FixedZone("non-utc", 3600)

	tests := map[string]func(*CreateVersionResult){
		"authorization identity": func(result *CreateVersionResult) {
			result.KnowledgeCatalogPublication.AuthorizationID = "99999999-9999-4999-8999-999999999999"
		},
		"publication identity": func(result *CreateVersionResult) {
			result.KnowledgeCatalogPublication.KnowledgeCatalogPublicationID = "0"
		},
		"model release identity": func(result *CreateVersionResult) {
			result.KnowledgeCatalogPublication.TargetModelReleaseID = "01"
		},
		"catalog digest": func(result *CreateVersionResult) {
			result.KnowledgeCatalogPublication.CatalogSHA256 = strings.Repeat("f", 64)
		},
		"target model artifact": func(result *CreateVersionResult) {
			result.KnowledgeCatalogPublication.TargetModelArtifactSHA256 = strings.Repeat("f", 64)
		},
		"target model identity": func(result *CreateVersionResult) {
			result.KnowledgeCatalogPublication.TargetModelID = "99999999-9999-4999-8999-999999999999"
		},
		"target application version": func(result *CreateVersionResult) {
			result.KnowledgeCatalogPublication.TargetApplicationVersion = "0.2.1"
		},
		"target application commit": func(result *CreateVersionResult) {
			result.KnowledgeCatalogPublication.TargetApplicationCommit = strings.Repeat("f", 40)
		},
		"target application build time": func(result *CreateVersionResult) {
			result.KnowledgeCatalogPublication.TargetApplicationBuildTime = "2026-07-13T04:00:01Z"
		},
		"configuration identity": func(result *CreateVersionResult) {
			result.KnowledgeCatalogPublication.ConfigurationID = "99999999-9999-4999-8999-999999999999"
		},
		"configuration key": func(result *CreateVersionResult) {
			result.Item.Key = "recommendation.catalog.other"
		},
		"configuration kind": func(result *CreateVersionResult) {
			result.Item.Kind = KindPrompt
		},
		"configuration expected head": func(result *CreateVersionResult) {
			result.KnowledgeCatalogPublication.ExpectedConfigurationHeadRevision++
		},
		"configuration published head": func(result *CreateVersionResult) {
			result.KnowledgeCatalogPublication.ConfigurationHeadRevision++
		},
		"configuration version identity": func(result *CreateVersionResult) {
			result.KnowledgeCatalogPublication.ConfigurationVersionID = "42"
		},
		"configuration version number": func(result *CreateVersionResult) {
			result.KnowledgeCatalogPublication.ConfigurationVersionNumber++
		},
		"catalog schema": func(result *CreateVersionResult) {
			result.Item.ActiveVersion.SchemaID = "ascendany.knowledge_catalog.other.v1"
		},
		"catalog credential": func(result *CreateVersionResult) {
			result.Item.ActiveVersion.CredentialRef = &credentialRef
		},
		"catalog document digest": func(result *CreateVersionResult) {
			result.Item.ActiveVersion.DocumentSHA256 = strings.Repeat("f", 64)
		},
		"catalog document": func(result *CreateVersionResult) {
			result.Item.ActiveVersion.Document = json.RawMessage(`{"different":true}`)
		},
		"analytics generation": func(result *CreateVersionResult) {
			result.KnowledgeCatalogPublication.AnalyticsGenerationID = "18"
		},
		"analytics head": func(result *CreateVersionResult) {
			result.KnowledgeCatalogPublication.AnalyticsHeadRevision++
		},
		"analytics manifest": func(result *CreateVersionResult) {
			result.KnowledgeCatalogPublication.InputManifestSHA256 = strings.Repeat("f", 64)
		},
		"current model head": func(result *CreateVersionResult) {
			result.KnowledgeCatalogPublication.CurrentModelHeadRevision++
		},
		"current model artifact": func(result *CreateVersionResult) {
			result.KnowledgeCatalogPublication.CurrentModelArtifactSHA256 = strings.Repeat("f", 64)
		},
		"publisher account": func(result *CreateVersionResult) {
			result.KnowledgeCatalogPublication.PublishedByAccountID = "99999999-9999-4999-8999-999999999999"
		},
		"publisher session": func(result *CreateVersionResult) {
			result.KnowledgeCatalogPublication.PublishedBySessionID = "99999999-9999-4999-8999-999999999999"
		},
		"publication time zone": func(result *CreateVersionResult) {
			result.KnowledgeCatalogPublication.PublishedAt = result.KnowledgeCatalogPublication.PublishedAt.In(nonUTC)
		},
		"audit identity": func(result *CreateVersionResult) {
			result.KnowledgeCatalogPublication.AuditEventID++
		},
		"version author account": func(result *CreateVersionResult) {
			result.Item.ActiveVersion.CreatedByAccountID = "99999999-9999-4999-8999-999999999999"
		},
		"version author session": func(result *CreateVersionResult) {
			result.Item.ActiveVersion.CreatedBySessionID = "99999999-9999-4999-8999-999999999999"
		},
		"version publication time": func(result *CreateVersionResult) {
			result.Item.ActiveVersion.CreatedAt = result.Item.ActiveVersion.CreatedAt.Add(time.Second)
		},
	}

	for name, mutate := range tests {
		name, mutate := name, mutate
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			result := catalogPublisherResult(command)
			mutate(&result)
			tx := &catalogPublisherTx{encoded: encodeCatalogPublisherResult(t, result)}
			repository := newCatalogPublisherRepository(t, tx, &rejectingPublicationPrecondition{})

			_, err := repository.StoreVersion(context.Background(), command, command.TargetCatalogSHA256)
			if CodeOf(err) != ErrorStoredDataInvalid {
				t.Fatalf("StoreVersion() error=%v code=%q", err, CodeOf(err))
			}
			assertCatalogPublisherRolledBack(t, tx)
		})
	}
}

func TestPostgresCatalogPublicationMapsSQLStates(t *testing.T) {
	t.Parallel()
	command := catalogPublisherCommand(t)
	tests := map[string]ErrorCode{
		"28000": ErrorPrincipalRejected,
		"40001": ErrorHeadConflict,
		"23514": ErrorStoredDataInvalid,
		"23505": ErrorDocumentConflict,
		"55000": ErrorDatabase,
	}

	for sqlState, wantCode := range tests {
		sqlState, wantCode := sqlState, wantCode
		t.Run(sqlState, func(t *testing.T) {
			t.Parallel()
			tx := &catalogPublisherTx{scanErr: &pgconn.PgError{Code: sqlState, Message: "publication rejected"}}
			repository := newCatalogPublisherRepository(t, tx, &rejectingPublicationPrecondition{})

			_, err := repository.StoreVersion(context.Background(), command, command.TargetCatalogSHA256)
			if CodeOf(err) != wantCode {
				t.Fatalf("StoreVersion() error=%v code=%q want=%q", err, CodeOf(err), wantCode)
			}
			assertCatalogPublisherRolledBack(t, tx)
		})
	}
}

func newCatalogPublisherRepository(
	t *testing.T,
	tx postgresTx,
	precondition VersionWritePrecondition,
) *PostgresRepository {
	t.Helper()
	repository, err := newPostgresRepository(func(context.Context, pgx.TxOptions) (postgresTx, error) {
		return tx, nil
	}, precondition)
	if err != nil {
		t.Fatal(err)
	}
	return repository
}

func catalogPublisherCommand(t *testing.T) CreateVersionCommand {
	t.Helper()
	generationID := "17"
	analyticsHeadRevision := int64(9)
	currentModelHeadRevision := int64(2)
	manifestSHA256 := strings.Repeat("a", 64)
	currentModelArtifactSHA256 := strings.Repeat("b", 64)
	const catalogSHA256 = "44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a"
	command := CreateVersionCommand{
		Principal: auth.AccessPrincipal{
			AccountID: testConfigurationAccountID, SessionID: testConfigurationSessionID,
			JWTID: testConfigurationJWTID, Role: auth.RoleAdmin, AuthRevision: 3,
		},
		Key: KnowledgeCatalogKey, Kind: KindKnowledgeCatalog, ExpectedHeadRevision: 3,
		ExpectedAnalyticsGenerationID: &generationID, ExpectedAnalyticsHeadRevision: &analyticsHeadRevision,
		ExpectedInputManifestSHA256: &manifestSHA256, ExpectedCurrentModelHeadRevision: &currentModelHeadRevision,
		ExpectedCurrentModelArtifactSHA256: &currentModelArtifactSHA256,
		TargetCatalogSHA256:                catalogSHA256,
		TargetModelID:                      "11111111-1111-4111-8111-111111111111",
		TargetModelArtifactSHA256:          strings.Repeat("c", 64),
		TargetApplicationVersion:           "0.2.0",
		TargetApplicationCommit:            strings.Repeat("d", 40),
		TargetApplicationBuildTime:         "2026-07-13T04:00:00Z",
		SchemaID:                           KnowledgeCatalogSchemaID,
		Document:                           json.RawMessage(`{}`),
	}
	authorization := AuthorizedCatalogPublicationRequest{
		AuthorizationID: "88888888-8888-4888-8888-888888888888",
		CatalogPublicationIntent: CatalogPublicationIntent{
			Schema:                             CatalogPublicationRequestSchema,
			ExpectedConfigurationHeadRevision:  command.ExpectedHeadRevision,
			ExpectedAnalyticsGenerationID:      generationID,
			ExpectedAnalyticsHeadRevision:      analyticsHeadRevision,
			ExpectedInputManifestSHA256:        manifestSHA256,
			ExpectedCurrentModelHeadRevision:   currentModelHeadRevision,
			ExpectedCurrentModelArtifactSHA256: currentModelArtifactSHA256,
			TargetCatalogSHA256:                catalogSHA256,
			TargetModelArtifactSHA256:          command.TargetModelArtifactSHA256,
			TargetApplicationVersion:           command.TargetApplicationVersion,
			TargetApplicationCommit:            command.TargetApplicationCommit,
			TargetApplicationBuildTime:         command.TargetApplicationBuildTime,
		},
	}
	canonical, err := CanonicalCatalogPublicationRequest(authorization)
	if err != nil {
		t.Fatal(err)
	}
	command.PublicationAuthorizationID = authorization.AuthorizationID
	command.PublicationAccessTokenSHA256 = strings.Repeat("e", 64)
	command.PublicationAuthorizationRequest = canonical
	return command
}

func catalogPublisherResult(command CreateVersionCommand) CreateVersionResult {
	createdAt := time.Date(2026, 7, 12, 4, 0, 0, 0, time.UTC)
	publishedAt := time.Date(2026, 7, 13, 4, 0, 0, 0, time.UTC)
	active := &Version{
		ID: "41", Number: command.ExpectedHeadRevision + 1, SchemaID: command.SchemaID,
		Document: command.Document, DocumentSHA256: command.TargetCatalogSHA256,
		CreatedByAccountID: command.Principal.AccountID, CreatedBySessionID: command.Principal.SessionID,
		CreatedAt: publishedAt,
	}
	publication := &KnowledgeCatalogPublication{
		AuthorizationID: command.PublicationAuthorizationID, KnowledgeCatalogPublicationID: "51",
		TargetModelReleaseID: "61", CatalogSHA256: command.TargetCatalogSHA256,
		TargetModelArtifactSHA256: command.TargetModelArtifactSHA256, TargetModelID: command.TargetModelID,
		TargetApplicationVersion: command.TargetApplicationVersion,
		TargetApplicationCommit:  command.TargetApplicationCommit, TargetApplicationBuildTime: command.TargetApplicationBuildTime,
		ConfigurationID:                   "33333333-3333-4333-8333-333333333333",
		ExpectedConfigurationHeadRevision: command.ExpectedHeadRevision,
		ConfigurationHeadRevision:         command.ExpectedHeadRevision + 1, ConfigurationMutated: true,
		ConfigurationVersionID: active.ID, ConfigurationVersionNumber: active.Number,
		AnalyticsGenerationID:      *command.ExpectedAnalyticsGenerationID,
		AnalyticsHeadRevision:      *command.ExpectedAnalyticsHeadRevision,
		InputManifestSHA256:        *command.ExpectedInputManifestSHA256,
		CurrentModelHeadRevision:   *command.ExpectedCurrentModelHeadRevision,
		CurrentModelArtifactSHA256: *command.ExpectedCurrentModelArtifactSHA256,
		PublishedByAccountID:       command.Principal.AccountID, PublishedBySessionID: command.Principal.SessionID,
		PublishedAt: publishedAt, AuditEventID: 71,
	}
	return CreateVersionResult{
		Item: Item{
			ID: publication.ConfigurationID, Key: command.Key, Kind: command.Kind,
			HeadRevision: publication.ConfigurationHeadRevision, ActiveVersion: active,
			CreatedAt: createdAt, UpdatedAt: publishedAt,
		},
		AuditEventID: 71, KnowledgeCatalogPublication: publication,
	}
}

func encodeCatalogPublisherResult(t *testing.T, result CreateVersionResult) []byte {
	t.Helper()
	encoded, err := json.Marshal(struct {
		Item                        Item                         `json:"item"`
		Idempotent                  bool                         `json:"idempotent"`
		AuditEventID                int64                        `json:"auditEventId"`
		KnowledgeCatalogPublication *KnowledgeCatalogPublication `json:"knowledgeCatalogPublication"`
	}{
		Item:                        result.Item,
		Idempotent:                  result.Idempotent,
		AuditEventID:                result.AuditEventID,
		KnowledgeCatalogPublication: result.KnowledgeCatalogPublication,
	})
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func addCatalogPublisherPublicationField(t *testing.T, encoded []byte, key string, value any) []byte {
	t.Helper()
	var wire map[string]any
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatal(err)
	}
	publication, ok := wire["knowledgeCatalogPublication"].(map[string]any)
	if !ok {
		t.Fatal("encoded publication result lacks a publication object")
	}
	publication[key] = value
	result, err := json.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func assertCatalogPublisherRolledBack(t *testing.T, tx *catalogPublisherTx) {
	t.Helper()
	if tx.queryRowCount != 1 || tx.queryCount != 0 || tx.execCount != 0 || tx.commitCount != 0 || tx.rollbackCount != 1 {
		t.Fatalf("queryRows=%d queries=%d execs=%d commits=%d rollbacks=%d",
			tx.queryRowCount, tx.queryCount, tx.execCount, tx.commitCount, tx.rollbackCount)
	}
}
