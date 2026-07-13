package recommendation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/kkkzbh/AscendAny/backend/internal/analytics"
	"github.com/kkkzbh/AscendAny/backend/internal/inferencemodel"
)

func TestValidateInferenceModelAndRuntimeBinding(t *testing.T) {
	t.Parallel()
	catalog := testCatalogDocument(t, []any{})
	_, _, digest, err := parseKnowledgeCatalog(catalog)
	if err != nil {
		t.Fatal(err)
	}
	model, binding := testModel(t, digest)
	if err := ValidateInferenceModel(model, inferencemodel.PurposeAcceptanceTest); err != nil {
		t.Fatal(err)
	}
	if err := ValidateInferenceModel(nil, inferencemodel.PurposeAcceptanceTest); err == nil {
		t.Fatal("nil inference model was accepted")
	}
	if err := ValidateInferenceModel(model, inferencemodel.PurposeProduction); err == nil {
		t.Fatal("acceptance model was accepted for production")
	}
	repository, err := newPostgresRepository(func(context.Context, pgx.TxOptions) (recommendationTx, error) {
		return nil, errors.New("unused")
	}, model, binding)
	if err != nil || repository.model != model || repository.binding.HeadRevision != binding.HeadRevision {
		t.Fatalf("repository=%#v error=%v", repository, err)
	}
	binding.ManifestSHA256 = strings.Repeat("f", 64)
	if _, err := newPostgresRepository(func(context.Context, pgx.TxOptions) (recommendationTx, error) {
		return nil, errors.New("unused")
	}, model, binding); err == nil {
		t.Fatal("drifted runtime binding was accepted")
	}
}

func TestReviewContextRepositoryRejectsNilDatabase(t *testing.T) {
	t.Parallel()
	if _, err := NewReviewContextPostgresRepository(nil); CodeOf(err) != ErrorInvalidConfiguration {
		t.Fatalf("nil review database error=%v", err)
	}
}

func TestAnalyticsGenerationStateUsesOwnedManifestCanonicalization(t *testing.T) {
	t.Parallel()
	manifest, err := analytics.CanonicalManifest(analytics.Manifest{
		Protocol: analytics.ManifestProtocolV1,
		Target: analytics.ManifestTarget{
			ExamID: 1, SnapshotID: 11, ExamHeadRevision: 2,
		},
		Snapshots: []analytics.ManifestSnapshot{{
			ExamID: 1, SnapshotID: 11, DomainHash: strings.Repeat("a", 64),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var jsonbProjection map[string]any
	if err := json.Unmarshal(manifest.Canonical, &jsonbProjection); err != nil {
		t.Fatal(err)
	}
	storedJSON, err := json.Marshal(jsonbProjection)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(storedJSON, manifest.Canonical) {
		t.Fatal("test manifest did not exercise reordered JSON object fields")
	}
	generationID := int64(42)
	head := analyticsState{GenerationID: &generationID, HeadRevision: 1}
	generation := analyticsGenerationState{
		Status:                 "succeeded",
		BaseHeadRevision:       0,
		TargetExamID:           1,
		TargetSnapshotID:       11,
		TargetExamHeadRevision: 2,
		InputManifest:          string(storedJSON),
		InputManifestSHA256:    manifest.SHA256,
	}
	if err := validateAnalyticsGenerationState(head, generation); err != nil {
		t.Fatalf("valid analytics-owned manifest was rejected: %v", err)
	}

	digestDrift := generation
	digestDrift.InputManifestSHA256 = strings.Repeat("f", 64)
	if err := validateAnalyticsGenerationState(head, digestDrift); err == nil || !strings.Contains(err.Error(), "SHA-256 differs") {
		t.Fatalf("digest drift error=%v", err)
	}
	identityDrift := generation
	identityDrift.TargetSnapshotID++
	if err := validateAnalyticsGenerationState(head, identityDrift); err == nil || !strings.Contains(err.Error(), "differs from generation scalar columns") {
		t.Fatalf("identity drift error=%v", err)
	}
	headDrift := head
	headDrift.HeadRevision++
	if err := validateAnalyticsGenerationState(headDrift, generation); err == nil || !strings.Contains(err.Error(), "scalar columns are inconsistent") {
		t.Fatalf("head drift error=%v", err)
	}
}

func TestObservationCountsHaveIndependentSourceBoundaries(t *testing.T) {
	t.Parallel()
	rankingValidCount := int64(2)
	if err := validateObservationCounts(&rankingValidCount, 1); err != nil {
		t.Fatalf("independent ranking and exported submission counts were rejected: %v", err)
	}
	negative := int64(-1)
	for _, test := range []struct {
		name          string
		rankingCount  *int64
		exportedCount int64
	}{
		{name: "negative ranking aggregate", rankingCount: &negative, exportedCount: 0},
		{name: "negative exported count", rankingCount: nil, exportedCount: -1},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := validateObservationCounts(test.rankingCount, test.exportedCount); err == nil {
				t.Fatal("negative observation count was accepted")
			}
		})
	}
}

func TestRepositoryReadTransactionIsRepeatableReadOnly(t *testing.T) {
	t.Parallel()
	catalog := testCatalogDocument(t, []any{})
	_, _, digest, err := parseKnowledgeCatalog(catalog)
	if err != nil {
		t.Fatal(err)
	}
	model, binding := testModel(t, digest)
	tx := &transactionStub{}
	var options pgx.TxOptions
	repository, err := newPostgresRepository(func(_ context.Context, value pgx.TxOptions) (recommendationTx, error) {
		options = value
		return tx, nil
	}, model, binding)
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.transaction(context.Background(), "test read", func(recommendationTx) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if options.IsoLevel != pgx.RepeatableRead || options.AccessMode != pgx.ReadOnly || tx.commits != 1 || tx.rollbacks != 0 {
		t.Fatalf("options=%#v commits=%d rollbacks=%d", options, tx.commits, tx.rollbacks)
	}
}

func TestActiveCatalogUnavailableReasonDistinguishesAbsenceAndMismatch(t *testing.T) {
	t.Parallel()
	document := testCatalogDocument(t, []any{})
	catalog, _, digest, err := parseKnowledgeCatalog(document)
	if err != nil {
		t.Fatal(err)
	}
	model, _ := testModel(t, digest)
	manifest := model.Manifest()

	absent := activeCatalogUnavailableReason(catalogState{}, manifest)
	if absent == nil || *absent != UnavailableKnowledge {
		t.Fatalf("absent reason=%v", absent)
	}
	mismatched := activeCatalogUnavailableReason(catalogState{
		Available: true, Catalog: catalog, Digest: strings.Repeat("f", 64),
	}, manifest)
	if mismatched == nil || *mismatched != UnavailableKnowledgeMatch {
		t.Fatalf("digest mismatch reason=%v", mismatched)
	}
	if exact := activeCatalogUnavailableReason(catalogState{
		Available: true, Catalog: catalog, Digest: digest,
	}, manifest); exact != nil {
		t.Fatalf("exact catalog reason=%v", exact)
	}
}

type transactionStub struct {
	commits   int
	rollbacks int
}

func (*transactionStub) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	panic("unexpected Exec")
}
func (*transactionStub) Query(context.Context, string, ...any) (pgx.Rows, error) {
	panic("unexpected Query")
}
func (*transactionStub) QueryRow(context.Context, string, ...any) pgx.Row {
	panic("unexpected QueryRow")
}
func (tx *transactionStub) Commit(context.Context) error {
	tx.commits++
	return nil
}
func (tx *transactionStub) Rollback(context.Context) error {
	tx.rollbacks++
	return nil
}
