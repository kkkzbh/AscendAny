package modelrelease

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/kkkzbh/AscendAny/backend/internal/inferencemodel"
	"github.com/kkkzbh/AscendAny/backend/internal/modelartifact"
)

func TestRegisterUsesReadCommittedTransaction(t *testing.T) {
	t.Parallel()
	beginErr := errors.New("begin stopped after recording options")
	beginner := &recordingBeginner{err: beginErr}
	repository, err := NewRepository(beginner)
	if err != nil {
		t.Fatal(err)
	}
	_, err = repository.Register(context.Background(), testLoadedModel(t))
	if !errors.Is(err, beginErr) {
		t.Fatalf("Register() error = %v", err)
	}
	if beginner.calls != 1 || beginner.options.IsoLevel != pgx.ReadCommitted {
		t.Fatalf("BeginTx() calls = %d, options = %#v", beginner.calls, beginner.options)
	}
}

func TestBindUsesReadCommittedTransaction(t *testing.T) {
	t.Parallel()
	beginErr := errors.New("begin stopped after recording options")
	beginner := &recordingBeginner{err: beginErr}
	repository, err := NewRepository(beginner)
	if err != nil {
		t.Fatal(err)
	}
	_, err = repository.Bind(context.Background(), testLoadedModel(t), ApplicationIdentity{
		Version: "0.2.0", Commit: strings.Repeat("a", 40), BuildTime: "2026-07-13T12:00:00Z",
	})
	if !errors.Is(err, beginErr) {
		t.Fatalf("Bind() error = %v", err)
	}
	if beginner.calls != 1 || beginner.options.IsoLevel != pgx.ReadCommitted {
		t.Fatalf("BeginTx() calls = %d, options = %#v", beginner.calls, beginner.options)
	}
}

func TestRequireCurrentUsesReadOnlyReadCommittedTransaction(t *testing.T) {
	t.Parallel()
	beginErr := errors.New("begin stopped after recording options")
	beginner := &recordingBeginner{err: beginErr}
	repository, err := NewRepository(beginner)
	if err != nil {
		t.Fatal(err)
	}
	_, err = repository.RequireCurrent(context.Background(), testLoadedModel(t), ApplicationIdentity{
		Version: "0.2.0", Commit: strings.Repeat("a", 40), BuildTime: "2026-07-13T12:00:00Z",
	})
	if !errors.Is(err, beginErr) {
		t.Fatalf("RequireCurrent() error = %v", err)
	}
	if beginner.calls != 1 || beginner.options.IsoLevel != pgx.ReadCommitted || beginner.options.AccessMode != pgx.ReadOnly {
		t.Fatalf("BeginTx() calls = %d, options = %#v", beginner.calls, beginner.options)
	}
}

func TestValidateApplicationIdentity(t *testing.T) {
	t.Parallel()
	valid := ApplicationIdentity{Version: "0.2.0", Commit: strings.Repeat("a", 40), BuildTime: "2026-07-13T12:00:00Z"}
	if err := validateApplicationIdentity(valid); err != nil {
		t.Fatalf("validateApplicationIdentity() error = %v", err)
	}
	for name, value := range map[string]ApplicationIdentity{
		"empty version":   {Commit: valid.Commit, BuildTime: valid.BuildTime},
		"spaced commit":   {Version: valid.Version, Commit: " " + valid.Commit, BuildTime: valid.BuildTime},
		"long build time": {Version: valid.Version, Commit: valid.Commit, BuildTime: strings.Repeat("x", 129)},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateApplicationIdentity(value); !errors.Is(err, ErrInvalidConfiguration) {
				t.Fatalf("validateApplicationIdentity() error = %v", err)
			}
		})
	}
}

func TestEnsureHeadCreatesReplaysAndAdvances(t *testing.T) {
	t.Parallel()
	application := ApplicationIdentity{Version: "0.2.0", Commit: "commit", BuildTime: "build"}
	release := preparedRelease{
		artifactSHA256:         strings.Repeat("a", 64),
		knowledgeCatalogSHA256: strings.Repeat("c", 64),
	}

	t.Run("bootstrap", func(t *testing.T) {
		tx := &headTx{head: headRow{err: pgx.ErrNoRows}}
		revision, activated, err := ensureHead(context.Background(), tx, 7, release, application)
		if err != nil || revision != 1 || !activated || len(tx.execs) != 2 ||
			!strings.Contains(tx.execs[0], "recommendation_model_activation_events") ||
			!strings.Contains(tx.execs[1], "recommendation_model_head") || tx.execArgs[0][6] != nil ||
			len(tx.queries) != 0 {
			t.Fatalf("revision=%d activated=%t execs=%v args=%v queries=%v error=%v", revision, activated, tx.execs, tx.execArgs, tx.queries, err)
		}
	})
	t.Run("replay", func(t *testing.T) {
		tx := &headTx{
			head:       headRow{releaseID: 7, revision: 4, artifactSHA256: release.artifactSHA256},
			activation: activationRow{artifactSHA256: release.artifactSHA256, application: application},
		}
		revision, activated, err := ensureHead(context.Background(), tx, 7, release, application)
		if err != nil || revision != 4 || activated || len(tx.execs) != 0 {
			t.Fatalf("revision=%d activated=%t execs=%v error=%v", revision, activated, tx.execs, err)
		}
	})
	t.Run("same model new application without publication", func(t *testing.T) {
		tx := &headTx{
			head: headRow{releaseID: 7, revision: 4, artifactSHA256: release.artifactSHA256},
			activation: activationRow{
				artifactSHA256: release.artifactSHA256,
				application:    ApplicationIdentity{Version: "0.1.0", Commit: "old", BuildTime: "old-build"},
			},
		}
		if _, _, err := ensureHead(context.Background(), tx, 7, release, application); !errors.Is(err, ErrActivationUnauthorized) || len(tx.execs) != 0 {
			t.Fatalf("execs=%v error=%v", tx.execs, err)
		}
	})
	t.Run("same model consumes publication", func(t *testing.T) {
		tx := &headTx{
			head:         headRow{releaseID: 7, revision: 4, artifactSHA256: release.artifactSHA256, pendingPublicationID: int64Value(31)},
			activation:   activationRow{artifactSHA256: release.artifactSHA256, application: application},
			publications: []int64{31},
			updateRows:   1,
		}
		revision, activated, err := ensureHead(context.Background(), tx, 7, release, application)
		if err != nil || revision != 5 || !activated || len(tx.execs) != 2 || tx.execArgs[0][6] != int64(31) ||
			!strings.Contains(tx.execs[0], "knowledge_catalog_publication_id") ||
			!strings.Contains(tx.execs[1], "UPDATE ascendany.recommendation_model_head") {
			t.Fatalf("revision=%d activated=%t execs=%v args=%v error=%v", revision, activated, tx.execs, tx.execArgs, err)
		}
		assertPublicationQuery(t, tx, 7, release, 4, release.artifactSHA256, application)
	})
	t.Run("same model new application consumes publication", func(t *testing.T) {
		tx := &headTx{
			head: headRow{releaseID: 7, revision: 4, artifactSHA256: release.artifactSHA256, pendingPublicationID: int64Value(33)},
			activation: activationRow{
				artifactSHA256: release.artifactSHA256,
				application:    ApplicationIdentity{Version: "0.1.0", Commit: "old", BuildTime: "old-build"},
			},
			publications:           []int64{33},
			publicationApplication: &application,
			updateRows:             1,
		}
		revision, activated, err := ensureHead(context.Background(), tx, 7, release, application)
		if err != nil || revision != 5 || !activated || len(tx.execs) != 2 || tx.execArgs[0][6] != int64(33) {
			t.Fatalf("revision=%d activated=%t execs=%v args=%v error=%v", revision, activated, tx.execs, tx.execArgs, err)
		}
	})
	t.Run("same model different application cannot consume publication", func(t *testing.T) {
		publishedApplication := ApplicationIdentity{Version: "0.1.0", Commit: "old", BuildTime: "old-build"}
		tx := &headTx{
			head:                   headRow{releaseID: 7, revision: 4, artifactSHA256: release.artifactSHA256, pendingPublicationID: int64Value(34)},
			activation:             activationRow{artifactSHA256: release.artifactSHA256, application: publishedApplication},
			publications:           []int64{34},
			publicationApplication: &publishedApplication,
		}
		if _, _, err := ensureHead(context.Background(), tx, 7, release, application); !errors.Is(err, ErrActivationUnauthorized) || len(tx.execs) != 0 {
			t.Fatalf("execs=%v error=%v", tx.execs, err)
		}
	})
	t.Run("same model corrupt activation", func(t *testing.T) {
		tx := &headTx{
			head:       headRow{releaseID: 7, revision: 4, artifactSHA256: release.artifactSHA256},
			activation: activationRow{artifactSHA256: strings.Repeat("b", 64), application: application},
		}
		if _, _, err := ensureHead(context.Background(), tx, 7, release, application); !errors.Is(err, ErrStoredDataInvalid) {
			t.Fatalf("ensureHead() error = %v", err)
		}
	})
	t.Run("advance through publication", func(t *testing.T) {
		tx := &headTx{
			head: headRow{releaseID: 6, revision: 4, artifactSHA256: strings.Repeat("b", 64), pendingPublicationID: int64Value(32)},
			activation: activationRow{
				artifactSHA256: strings.Repeat("b", 64),
				application:    ApplicationIdentity{Version: "0.1.0", Commit: "old", BuildTime: "old-build"},
			},
			publications: []int64{32},
			updateRows:   1,
		}
		revision, activated, err := ensureHead(context.Background(), tx, 7, release, application)
		if err != nil || revision != 5 || !activated || len(tx.execs) != 2 || tx.execArgs[0][6] != int64(32) ||
			!strings.Contains(tx.execs[1], "UPDATE ascendany.recommendation_model_head") {
			t.Fatalf("revision=%d activated=%t execs=%v args=%v error=%v", revision, activated, tx.execs, tx.execArgs, err)
		}
	})
	t.Run("advance without publication", func(t *testing.T) {
		tx := &headTx{
			head:       headRow{releaseID: 6, revision: 4, artifactSHA256: strings.Repeat("b", 64)},
			activation: activationRow{artifactSHA256: strings.Repeat("b", 64), application: application},
		}
		if _, _, err := ensureHead(context.Background(), tx, 7, release, application); !errors.Is(err, ErrActivationUnauthorized) {
			t.Fatalf("ensureHead() error = %v", err)
		}
	})
	t.Run("A to B to A", func(t *testing.T) {
		releaseA := release
		releaseB := preparedRelease{
			artifactSHA256:         strings.Repeat("b", 64),
			knowledgeCatalogSHA256: strings.Repeat("d", 64),
		}
		toB := &headTx{
			head:         headRow{releaseID: 7, revision: 1, artifactSHA256: releaseA.artifactSHA256, pendingPublicationID: int64Value(41)},
			activation:   activationRow{artifactSHA256: releaseA.artifactSHA256, application: application},
			publications: []int64{41},
			updateRows:   1,
		}
		if revision, activated, err := ensureHead(context.Background(), toB, 8, releaseB, application); err != nil || revision != 2 || !activated {
			t.Fatalf("A to B revision=%d activated=%t error=%v", revision, activated, err)
		}
		backToA := &headTx{
			head:         headRow{releaseID: 8, revision: 2, artifactSHA256: releaseB.artifactSHA256, pendingPublicationID: int64Value(42)},
			activation:   activationRow{artifactSHA256: releaseB.artifactSHA256, application: application},
			publications: []int64{42},
			updateRows:   1,
		}
		if revision, activated, err := ensureHead(context.Background(), backToA, 7, releaseA, application); err != nil || revision != 3 || !activated {
			t.Fatalf("B to A revision=%d activated=%t error=%v", revision, activated, err)
		}
		if toB.execArgs[0][6] != int64(41) || backToA.execArgs[0][6] != int64(42) {
			t.Fatalf("publication consumption A->B=%v B->A=%v", toB.execArgs[0][6], backToA.execArgs[0][6])
		}
	})
}

func assertPublicationQuery(
	t *testing.T,
	tx *headTx,
	targetReleaseID int64,
	targetRelease preparedRelease,
	currentRevision int64,
	currentArtifactSHA256 string,
	targetApplication ApplicationIdentity,
) {
	t.Helper()
	if len(tx.queries) != 1 || len(tx.queryArgs) != 1 || len(tx.queryArgs[0]) != 9 || tx.head.pendingPublicationID == nil {
		t.Fatalf("publication queries=%v args=%v", tx.queries, tx.queryArgs)
	}
	query := tx.queries[0]
	if !strings.Contains(query, "publication.knowledge_catalog_publication_id = $1") ||
		!strings.Contains(query, "publication.target_model_release_id = $2") ||
		!strings.Contains(query, "publication.target_model_artifact_sha256 = $3") ||
		!strings.Contains(query, "publication.catalog_sha256 = $4") ||
		!strings.Contains(query, "publication.current_model_head_revision = $5") ||
		!strings.Contains(query, "publication.current_model_artifact_sha256 = $6") ||
		!strings.Contains(query, "publication.target_application_version = $7") ||
		!strings.Contains(query, "publication.target_application_commit = $8") ||
		!strings.Contains(query, "publication.target_application_build_time = $9") ||
		strings.Contains(query, "JOIN ascendany.configuration_items") ||
		strings.Contains(query, "JOIN ascendany.analytics_head") ||
		!strings.Contains(query, "consumed.knowledge_catalog_publication_id") ||
		strings.Contains(query, "FOR UPDATE") {
		t.Fatalf("publication query does not enforce the complete activation contract:\n%s", query)
	}
	want := []any{
		*tx.head.pendingPublicationID,
		targetReleaseID,
		targetRelease.artifactSHA256,
		targetRelease.knowledgeCatalogSHA256,
		currentRevision,
		currentArtifactSHA256,
		targetApplication.Version,
		targetApplication.Commit,
		targetApplication.BuildTime,
	}
	if len(tx.rowQueries) != 4 || !strings.Contains(tx.rowQueries[0], "recommendation_model_head") ||
		!strings.Contains(tx.rowQueries[1], "recommendation_model_activation_events") ||
		!strings.Contains(tx.rowQueries[2], "analytics_head") || !strings.Contains(tx.rowQueries[2], "FOR UPDATE OF head") ||
		!strings.Contains(tx.rowQueries[3], "configuration_items") || !strings.Contains(tx.rowQueries[3], "FOR UPDATE") {
		t.Fatalf("mutable lock order=%v", tx.rowQueries)
	}
	for index := range want {
		if tx.queryArgs[0][index] != want[index] {
			t.Fatalf("publication arg[%d]=%v want=%v", index, tx.queryArgs[0][index], want[index])
		}
	}
}

type recordingBeginner struct {
	options pgx.TxOptions
	calls   int
	err     error
}

func (beginner *recordingBeginner) BeginTx(_ context.Context, options pgx.TxOptions) (pgx.Tx, error) {
	beginner.calls++
	beginner.options = options
	return nil, beginner.err
}

func testLoadedModel(t *testing.T) modelartifact.Loaded {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(
		"..", "..", "..", "contracts", "recommendation", "fixtures",
		"synthetic-test-only.inference-model.v1.json",
	))
	if err != nil {
		t.Fatal(err)
	}
	model, err := inferencemodel.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return modelartifact.Loaded{
		Model: model, SHA256: model.SHA256(), Size: int64(len(raw)), Mode: modelartifact.RequiredMode,
	}
}

type activationRow struct {
	artifactSHA256 string
	application    ApplicationIdentity
	err            error
}

func int64Value(value int64) *int64 {
	return &value
}

func (row activationRow) Scan(destinations ...any) error {
	if row.err != nil {
		return row.err
	}
	*destinations[0].(*string) = row.artifactSHA256
	*destinations[1].(*string) = row.application.Version
	*destinations[2].(*string) = row.application.Commit
	*destinations[3].(*string) = row.application.BuildTime
	return nil
}

type headRow struct {
	releaseID            int64
	revision             int64
	artifactSHA256       string
	pendingPublicationID *int64
	err                  error
}

type activationAnalyticsRow struct{}

func (activationAnalyticsRow) Scan(destinations ...any) error {
	*(destinations[0].(*int64)) = 17
	*(destinations[1].(*int64)) = 9
	*(destinations[2].(*string)) = strings.Repeat("a", 64)
	*(destinations[3].(*string)) = "succeeded"
	return nil
}

type activationConfigurationRow struct{}

func (activationConfigurationRow) Scan(destinations ...any) error {
	*(destinations[0].(*int64)) = 102
	*(destinations[1].(*int64)) = 1
	*(destinations[2].(*string)) = "recommendation.catalog.active"
	*(destinations[3].(*string)) = "knowledge_catalog"
	return nil
}

func (row headRow) Scan(destinations ...any) error {
	if row.err != nil {
		return row.err
	}
	*destinations[0].(*int64) = row.releaseID
	*destinations[1].(*int64) = row.revision
	*destinations[2].(*string) = row.artifactSHA256
	if len(destinations) == 4 {
		pending := destinations[3].(*pgtype.Int8)
		if row.pendingPublicationID != nil {
			*pending = pgtype.Int8{Int64: *row.pendingPublicationID, Valid: true}
		} else {
			*pending = pgtype.Int8{}
		}
	}
	return nil
}

type headTx struct {
	head                   headRow
	activation             activationRow
	publications           []int64
	publicationApplication *ApplicationIdentity
	queryErr               error
	queries                []string
	queryArgs              [][]any
	rowQueries             []string
	execs                  []string
	execArgs               [][]any
	updateRows             int64
}

func (tx *headTx) QueryRow(_ context.Context, sql string, arguments ...any) pgx.Row {
	if strings.Contains(sql, "FROM ascendany.knowledge_catalog_publications AS publication") {
		tx.queries = append(tx.queries, sql)
		tx.queryArgs = append(tx.queryArgs, append([]any(nil), arguments...))
		if tx.queryErr != nil {
			return publicationRow{err: tx.queryErr}
		}
		if len(tx.publications) == 0 || arguments[0] != tx.publications[0] ||
			tx.publicationApplication != nil &&
				(arguments[6] != tx.publicationApplication.Version ||
					arguments[7] != tx.publicationApplication.Commit ||
					arguments[8] != tx.publicationApplication.BuildTime) {
			return publicationRow{err: pgx.ErrNoRows}
		}
		return publicationRow{id: tx.publications[0]}
	}
	tx.rowQueries = append(tx.rowQueries, sql)
	if strings.Contains(sql, "recommendation_model_activation_events") {
		return tx.activation
	}
	if strings.Contains(sql, "analytics_head") {
		return activationAnalyticsRow{}
	}
	if strings.Contains(sql, "configuration_items") {
		return activationConfigurationRow{}
	}
	return tx.head
}

type publicationRow struct {
	id  int64
	err error
}

func (row publicationRow) Scan(destinations ...any) error {
	if row.err != nil {
		return row.err
	}
	if len(destinations) != 7 || row.id < 1 {
		return errors.New("invalid publication row")
	}
	*destinations[0].(*int64) = row.id
	*destinations[1].(*int64) = 101
	*destinations[2].(*int64) = 102
	*destinations[3].(*int64) = 1
	*destinations[4].(*int64) = 17
	*destinations[5].(*int64) = 9
	*destinations[6].(*string) = strings.Repeat("a", 64)
	return nil
}

func (tx *headTx) Exec(_ context.Context, sql string, arguments ...any) (pgconn.CommandTag, error) {
	tx.execs = append(tx.execs, sql)
	tx.execArgs = append(tx.execArgs, append([]any(nil), arguments...))
	rows := int64(1)
	if strings.Contains(sql, "UPDATE ascendany.recommendation_model_head") {
		rows = tx.updateRows
	}
	return pgconn.NewCommandTag(fmt.Sprintf("UPDATE %d", rows)), nil
}

func (*headTx) Begin(context.Context) (pgx.Tx, error) { panic("unexpected Begin") }
func (*headTx) Commit(context.Context) error          { panic("unexpected Commit") }
func (*headTx) Rollback(context.Context) error        { panic("unexpected Rollback") }
func (*headTx) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	panic("unexpected CopyFrom")
}
func (*headTx) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults { panic("unexpected SendBatch") }
func (*headTx) LargeObjects() pgx.LargeObjects                         { panic("unexpected LargeObjects") }
func (*headTx) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	panic("unexpected Prepare")
}
func (*headTx) Query(context.Context, string, ...any) (pgx.Rows, error) {
	panic("unexpected Query")
}
func (*headTx) Conn() *pgx.Conn { panic("unexpected Conn") }
