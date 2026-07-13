package recommendation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/kkkzbh/AscendAny/backend/internal/inferencemodel"
	"github.com/kkkzbh/AscendAny/backend/internal/modelrelease"
)

type PgxBeginner interface {
	BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
}

type recommendationTx interface {
	recommendationQuery
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Commit(context.Context) error
	Rollback(context.Context) error
}

type recommendationQuery interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

type beginTransaction func(context.Context, pgx.TxOptions) (recommendationTx, error)

type PostgresRepository struct {
	begin   beginTransaction
	model   *inferencemodel.Model
	binding modelrelease.Binding
}

func NewPostgresRepository(pool PgxBeginner, model *inferencemodel.Model, binding modelrelease.Binding) (*PostgresRepository, error) {
	if pool == nil {
		return nil, domainError(ErrorInvalidConfiguration, true, "construct recommendation repository", errors.New("database pool is required"))
	}
	return newPostgresRepository(func(ctx context.Context, options pgx.TxOptions) (recommendationTx, error) {
		return pool.BeginTx(ctx, options)
	}, model, binding)
}

func newPostgresRepository(begin beginTransaction, model *inferencemodel.Model, binding modelrelease.Binding) (*PostgresRepository, error) {
	if begin == nil {
		return nil, domainError(ErrorInvalidConfiguration, true, "construct recommendation repository", errors.New("transaction beginner and inference model are required"))
	}
	if err := ValidateInferenceModel(model, binding.ModelPurpose); err != nil {
		return nil, domainError(ErrorInvalidConfiguration, true, "construct recommendation repository", err)
	}
	if err := validateRuntimeBinding(model, binding); err != nil {
		return nil, domainError(ErrorInvalidConfiguration, true, "construct recommendation repository", err)
	}
	binding.ManifestJSON = append(json.RawMessage(nil), binding.ManifestJSON...)
	return &PostgresRepository{begin: begin, model: model, binding: binding}, nil
}

// ValidateInferenceModel applies the online runtime's fixed feature contract to
// an already parsed artifact. Release verification, installation, and startup
// must all call this gate.
func ValidateInferenceModel(model *inferencemodel.Model, expectedPurpose inferencemodel.Purpose) error {
	if model == nil {
		return errors.New("inference model is required")
	}
	if _, err := inferencemodel.ParsePurpose(string(expectedPurpose)); err != nil {
		return fmt.Errorf("expected model purpose: %w", err)
	}
	manifest := model.Manifest()
	if manifest.Purpose != expectedPurpose {
		return fmt.Errorf("model purpose %q differs from expected purpose %q", manifest.Purpose, expectedPurpose)
	}
	if manifest.FeatureSchemaSHA256 != FeatureSchemaSHA256() ||
		!slices.Equal(manifest.ActorFeatureIDs, actorFeatureIDs) ||
		!slices.Equal(manifest.ProblemFeatureIDs, problemFeatureIDs) {
		return errors.New("model feature contract differs from the online feature schema")
	}
	if err := inferencemodel.ValidateKnowledgePointIDs(manifest.KnowledgePointIDs); err != nil {
		return fmt.Errorf("model knowledge point identities differ from the knowledge catalog contract: %w", err)
	}
	if err := model.ValidateNumericEnvelope(actorFeatureRanges, problemFeatureRanges); err != nil {
		return fmt.Errorf("model numeric envelope exceeds the online feature domain: %w", err)
	}
	return nil
}

func (repository *PostgresRepository) transaction(ctx context.Context, operation string, run func(recommendationTx) error) (resultErr error) {
	return readTransaction(ctx, repository.begin, operation, run)
}

func readTransaction(ctx context.Context, begin beginTransaction, operation string, run func(recommendationTx) error) (resultErr error) {
	if ctx == nil {
		return domainError(ErrorInvalidInput, true, operation, errors.New("context is required"))
	}
	tx, err := begin(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return databaseError("begin "+operation, err)
	}
	finished := false
	defer func() {
		if finished {
			return
		}
		rollbackContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := tx.Rollback(rollbackContext); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			rollbackErr := databaseError("rollback "+operation, err)
			if resultErr == nil {
				resultErr = rollbackErr
			} else {
				resultErr = errors.Join(resultErr, rollbackErr)
			}
		}
	}()
	if err := run(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return databaseError("commit "+operation, err)
	}
	finished = true
	return nil
}

type releaseManifestWire struct {
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

func validateRuntimeBinding(model *inferencemodel.Model, binding modelrelease.Binding) error {
	manifest := model.Manifest()
	if binding.ReleaseID <= 0 || binding.HeadRevision <= 0 || !lowercaseSHA256Pattern.MatchString(binding.ManifestSHA256) ||
		!canonicalUUIDv4Pattern.MatchString(manifest.ModelID) || model.SHA256() == "" || !lowercaseSHA256Pattern.MatchString(model.SHA256()) {
		return errors.New("model or binding identity is invalid")
	}
	canonical, digest, err := canonicalObject(binding.ManifestJSON, binding.ManifestSHA256, maximumManifestBytes, "bound model manifest")
	if err != nil || digest != binding.ManifestSHA256 || !bytes.Equal(canonical, binding.ManifestJSON) {
		return errors.New("bound model manifest is invalid")
	}
	var wire releaseManifestWire
	if err := decodeClosed(canonical, &wire); err != nil {
		return fmt.Errorf("decode bound model manifest: %w", err)
	}
	if wire.Schema != inferencemodel.Schema || wire.ModelID != manifest.ModelID || wire.Purpose != string(manifest.Purpose) ||
		wire.TrainedAt != manifest.TrainedAt || binding.ModelPurpose != manifest.Purpose ||
		wire.Algorithm != manifest.Algorithm || wire.InferenceContract != manifest.InferenceContract ||
		wire.TrainingProvenanceSHA256 != manifest.TrainingProvenanceSHA256 ||
		wire.FeatureSchemaSHA256 != manifest.FeatureSchemaSHA256 || wire.KnowledgeCatalogSHA256 != manifest.KnowledgeCatalogSHA256 ||
		wire.ParameterSHA256 != manifest.ParameterSHA256 || wire.GoldenVectorsSHA256 != manifest.GoldenVectorsSHA256 ||
		!slices.Equal(wire.ActorFeatureIDs, manifest.ActorFeatureIDs) || !slices.Equal(wire.ProblemFeatureIDs, manifest.ProblemFeatureIDs) ||
		!slices.Equal(wire.KnowledgePointIDs, manifest.KnowledgePointIDs) {
		return errors.New("bound manifest differs from the parsed model")
	}
	return nil
}
