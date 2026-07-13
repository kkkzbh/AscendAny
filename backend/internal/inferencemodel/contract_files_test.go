package inferencemodel_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/kkkzbh/AscendAny/backend/internal/canonicaljson"
	"github.com/kkkzbh/AscendAny/backend/internal/configuration"
	"github.com/kkkzbh/AscendAny/backend/internal/inferencemodel"
	"github.com/kkkzbh/AscendAny/backend/internal/recommendation"
)

const (
	featureSchemaSHA256      = "09c18717b8de4b3dba6c8bd9341fb237176c6d22ab7f99e2e937bf7b387a060f"
	knowledgeCatalogSHA256   = "a58370ec66def22b13a0bd64acf195e9fa28530e81481e7ade2545aaaa9bfe3c"
	trainingProvenanceSHA256 = "a2513967a725e6a8ba8dcff3fdb6188a437c4a6eec74a668e48440ea651f19f7"
	syntheticArtifactSHA256  = "5182ed451d74a4e10d8384f3a4d9fcb2a8d2ad7d043e3721f2247e10c029bf58"
)

type contractVectors struct {
	Schema           string `json:"schema"`
	Canonicalization struct {
		InputBytes     string `json:"inputBytes"`
		CanonicalBytes string `json:"canonicalBytes"`
		SHA256         string `json:"sha256"`
	} `json:"canonicalization"`
	Documents []struct {
		Path       string `json:"path"`
		ByteLength int    `json:"byteLength"`
		SHA256     string `json:"sha256"`
	} `json:"documents"`
	SubdocumentDigests struct {
		ParameterSHA256     string `json:"parameterSha256"`
		GoldenVectorsSHA256 string `json:"goldenVectorsSha256"`
	} `json:"subdocumentDigests"`
}

func TestExternalInferenceModelJSONSchemaIsClosedAndAcceptsFixture(t *testing.T) {
	t.Parallel()
	contractDirectory := recommendationContractDirectory(t)
	var schemaDocument any
	decodeClosed(t, mustRead(t, filepath.Join(contractDirectory, "ascendany.recommendation.inference-model.v1.schema.json")), &schemaDocument)
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	const resource = "urn:ascendany:recommendation:inference-model:v1:test"
	if err := compiler.AddResource(resource, schemaDocument); err != nil {
		t.Fatalf("load inference-model JSON Schema: %v", err)
	}
	compiled, err := compiler.Compile(resource)
	if err != nil {
		t.Fatalf("compile inference-model JSON Schema: %v", err)
	}
	artifact := decodeArtifact(t, mustRead(t, filepath.Join(contractDirectory, "fixtures", "synthetic-test-only.inference-model.v1.json")))
	if err := compiled.Validate(artifact); err != nil {
		t.Fatalf("validate synthetic artifact against JSON Schema: %v", err)
	}
	artifact["unknown"] = true
	if err := compiled.Validate(artifact); err == nil {
		t.Fatal("closed JSON Schema accepted an unknown root field")
	}
	delete(artifact, "unknown")
	artifact["parameters"].(map[string]any)["discrimination"] = json.Number("0")
	if err := compiled.Validate(artifact); err == nil {
		t.Fatal("JSON Schema accepted a non-positive discrimination")
	}
	artifact = decodeArtifact(t, mustRead(t, filepath.Join(contractDirectory, "fixtures", "synthetic-test-only.inference-model.v1.json")))
	artifact["manifest"].(map[string]any)["purpose"] = "production_test"
	if err := compiled.Validate(artifact); err == nil {
		t.Fatal("JSON Schema accepted an unknown deployment purpose")
	}
	artifact = decodeArtifact(t, mustRead(t, filepath.Join(contractDirectory, "fixtures", "synthetic-test-only.inference-model.v1.json")))
	artifact["manifest"].(map[string]any)["trainedAt"] = "2000-01-01T00:00:00.1234567Z"
	if err := compiled.Validate(artifact); err == nil {
		t.Fatal("JSON Schema accepted trainedAt beyond PostgreSQL microsecond precision")
	}
	artifact = decodeArtifact(t, mustRead(t, filepath.Join(contractDirectory, "fixtures", "synthetic-test-only.inference-model.v1.json")))
	artifact["manifest"].(map[string]any)["trainedAt"] = "0000-01-01T00:00:00Z"
	if err := compiled.Validate(artifact); err == nil {
		t.Fatal("JSON Schema accepted trainedAt outside the PostgreSQL common-era round-trip range")
	}
	artifact = decodeArtifact(t, mustRead(t, filepath.Join(contractDirectory, "fixtures", "synthetic-test-only.inference-model.v1.json")))
	artifact["manifest"].(map[string]any)["purpose"] = "test"
	if err := compiled.Validate(artifact); err == nil {
		t.Fatal("JSON Schema accepted an unknown model purpose")
	}
	artifact = decodeArtifact(t, mustRead(t, filepath.Join(contractDirectory, "fixtures", "synthetic-test-only.inference-model.v1.json")))
	expandKnowledgeIdentities(artifact, inferencemodel.MaximumKnowledgePoints+1)
	if err := compiled.Validate(artifact); err == nil {
		t.Fatal("JSON Schema accepted knowledge arrays above the catalog maximum")
	}
}

func TestExternalContractFilesBindToProductionRuntime(t *testing.T) {
	t.Parallel()
	contractDirectory := recommendationContractDirectory(t)

	featureBytes, featureDigest := readCanonicalObject(t, filepath.Join(contractDirectory, "ascendany.recommendation.feature-schema.v1.json"), 1<<20)
	if featureDigest != featureSchemaSHA256 || featureDigest != recommendation.FeatureSchemaSHA256() ||
		!bytes.Equal(featureBytes, recommendation.FeatureSchemaDocument()) {
		t.Fatalf("feature schema binding differs: digest=%s runtime=%s\nfile=%s\nruntime document=%s", featureDigest, recommendation.FeatureSchemaSHA256(), featureBytes, recommendation.FeatureSchemaDocument())
	}

	catalogBytes, catalogDigest := readCanonicalObject(t, filepath.Join(contractDirectory, "fixtures", "synthetic-test-only.knowledge-catalog.v1.json"), 256<<10)
	if catalogDigest != knowledgeCatalogSHA256 {
		t.Fatalf("knowledge catalog digest = %s", catalogDigest)
	}
	validator := recommendation.ConfigurationPublicationContract{}
	if err := validator.ValidateRecommendationDocument(configuration.KindKnowledgeCatalog, recommendation.KnowledgeCatalogSchemaV1, catalogBytes); err != nil {
		t.Fatalf("validate synthetic knowledge catalog: %v", err)
	}

	_, provenanceDigest := readCanonicalObject(t, filepath.Join(contractDirectory, "fixtures", "synthetic-test-only.training-input-provenance.v1.json"), 1<<20)
	if provenanceDigest != trainingProvenanceSHA256 {
		t.Fatalf("training provenance digest = %s", provenanceDigest)
	}

	artifactBytes, artifactDigest := readCanonicalObject(t, filepath.Join(contractDirectory, "fixtures", "synthetic-test-only.inference-model.v1.json"), inferencemodel.MaximumArtifactBytes)
	if artifactDigest != syntheticArtifactSHA256 {
		t.Fatalf("synthetic artifact digest = %s", artifactDigest)
	}
	model, err := inferencemodel.Parse(artifactBytes)
	if err != nil {
		t.Fatalf("parse synthetic artifact: %v", err)
	}
	if err := recommendation.ValidateInferenceModel(model, inferencemodel.PurposeAcceptanceTest); err != nil {
		t.Fatalf("bind synthetic artifact to production features: %v", err)
	}
	if err := recommendation.ValidateInferenceModel(model, inferencemodel.PurposeProduction); err == nil {
		t.Fatal("synthetic acceptance fixture entered the production model gate")
	}
	manifest := model.Manifest()
	if manifest.TrainingProvenanceSHA256 != provenanceDigest || manifest.FeatureSchemaSHA256 != featureDigest ||
		manifest.KnowledgeCatalogSHA256 != catalogDigest || model.SHA256() != artifactDigest {
		t.Fatalf("synthetic artifact provenance differs: manifest=%+v artifact=%s", manifest, model.SHA256())
	}
}

func TestFullE2EFixturesBindCatalogToInferenceModel(t *testing.T) {
	t.Parallel()
	contractDirectory := recommendationContractDirectory(t)
	catalogBytes, catalogDigest := readCanonicalObject(
		t,
		filepath.Join(contractDirectory, "fixtures", "e2e-test-only.knowledge-catalog.v1.json"),
		256<<10,
	)
	if catalogDigest != "9db76af3f2b8e6fa018b6a955e674b0273bb457582981e67dfc159e12c7d43bf" {
		t.Fatalf("full E2E knowledge catalog digest = %s", catalogDigest)
	}
	validator := recommendation.ConfigurationPublicationContract{}
	if err := validator.ValidateRecommendationDocument(
		configuration.KindKnowledgeCatalog,
		recommendation.KnowledgeCatalogSchemaV1,
		catalogBytes,
	); err != nil {
		t.Fatalf("validate full E2E knowledge catalog: %v", err)
	}

	artifactBytes, artifactDigest := readCanonicalObject(
		t,
		filepath.Join(contractDirectory, "fixtures", "e2e-test-only.inference-model.v1.json"),
		inferencemodel.MaximumArtifactBytes,
	)
	if artifactDigest != "26798ac81a219fd2e38aa5cf45f47eec460f75bd039aa8fc8db45dd11425e0a8" {
		t.Fatalf("full E2E inference-model artifact digest = %s", artifactDigest)
	}
	model, err := inferencemodel.Parse(artifactBytes)
	if err != nil {
		t.Fatalf("parse full E2E inference model: %v", err)
	}
	if err := recommendation.ValidateInferenceModel(model, inferencemodel.PurposeAcceptanceTest); err != nil {
		t.Fatalf("bind full E2E inference model to production features: %v", err)
	}
	if err := recommendation.ValidateInferenceModel(model, inferencemodel.PurposeProduction); err == nil {
		t.Fatal("full E2E acceptance fixture entered the production model gate")
	}
	if model.Manifest().KnowledgeCatalogSHA256 != catalogDigest || model.SHA256() != artifactDigest {
		t.Fatalf("full E2E model/catalog provenance differs: manifest=%+v artifact=%s", model.Manifest(), model.SHA256())
	}
}

func TestExternalContractDigestVectors(t *testing.T) {
	t.Parallel()
	contractDirectory := recommendationContractDirectory(t)
	vectorBytes, _ := readCanonicalObject(t, filepath.Join(contractDirectory, "vectors", "ascendany.recommendation.contract-vectors.v1.json"), 1<<20)
	var vectors contractVectors
	decodeClosed(t, vectorBytes, &vectors)
	if vectors.Schema != "ascendany.recommendation.contract-vectors.v1" {
		t.Fatalf("vector schema = %q", vectors.Schema)
	}

	canonical, digest, err := canonicaljson.Object(json.RawMessage(vectors.Canonicalization.InputBytes), 1024)
	if err != nil {
		t.Fatalf("canonicalize vector input: %v", err)
	}
	if string(canonical) != vectors.Canonicalization.CanonicalBytes || digest != vectors.Canonicalization.SHA256 {
		t.Fatalf("canonicalization vector differs: canonical=%s digest=%s", canonical, digest)
	}

	for _, document := range vectors.Documents {
		document := document
		t.Run(document.Path, func(t *testing.T) {
			t.Parallel()
			raw, actualDigest := readCanonicalObject(t, filepath.Join(contractDirectory, filepath.FromSlash(document.Path)), inferencemodel.MaximumArtifactBytes)
			if len(raw) != document.ByteLength || actualDigest != document.SHA256 {
				t.Fatalf("document bytes=%d digest=%s, want bytes=%d digest=%s", len(raw), actualDigest, document.ByteLength, document.SHA256)
			}
		})
	}

	artifact := decodeArtifact(t, mustRead(t, filepath.Join(contractDirectory, "fixtures", "synthetic-test-only.inference-model.v1.json")))
	if digestValue(t, artifact["parameters"]) != vectors.SubdocumentDigests.ParameterSHA256 ||
		digestValue(t, artifact["goldenVectors"]) != vectors.SubdocumentDigests.GoldenVectorsSHA256 {
		t.Fatalf("artifact subdocument digests differ from vectors")
	}
}

func TestExternalContractRejectsInvalidArtifactsAtTheirOwningGate(t *testing.T) {
	t.Parallel()
	valid := mustRead(t, filepath.Join(recommendationContractDirectory(t), "fixtures", "synthetic-test-only.inference-model.v1.json"))

	t.Run("noncanonical bytes", func(t *testing.T) {
		raw := append(append([]byte(nil), valid...), '\n')
		if _, err := inferencemodel.Parse(raw); !errors.Is(err, inferencemodel.ErrInvalidArtifact) {
			t.Fatalf("Parse() error = %v", err)
		}
	})

	t.Run("unknown root field", func(t *testing.T) {
		root := decodeArtifact(t, valid)
		root["unknown"] = true
		if _, err := inferencemodel.Parse(canonicalArtifact(t, root)); !errors.Is(err, inferencemodel.ErrInvalidArtifact) {
			t.Fatalf("Parse() error = %v", err)
		}
	})

	t.Run("parameter digest mismatch", func(t *testing.T) {
		root := decodeArtifact(t, valid)
		root["parameters"].(map[string]any)["difficultyBias"] = json.Number("1")
		if _, err := inferencemodel.Parse(canonicalArtifact(t, root)); !errors.Is(err, inferencemodel.ErrInvalidArtifact) {
			t.Fatalf("Parse() error = %v", err)
		}
	})

	t.Run("subnormal normalization scale with benign golden input", func(t *testing.T) {
		root := decodeArtifact(t, valid)
		mean := root["parameters"].(map[string]any)["actorNormalization"].(map[string]any)["means"].([]any)[0].(json.Number)
		goldenValue := goldenInput(root)["actorFeatures"].([]any)[0].(map[string]any)["value"].(json.Number)
		if mean != goldenValue {
			t.Fatalf("fixture does not keep the subnormal scale inactive in its golden vector: mean=%s value=%s", mean, goldenValue)
		}
		root["parameters"].(map[string]any)["actorNormalization"].(map[string]any)["scales"].([]any)[0] = json.Number("1e-320")
		rebindArtifactDigests(t, root)
		if _, err := inferencemodel.Parse(canonicalArtifact(t, root)); !errors.Is(err, inferencemodel.ErrInvalidArtifact) {
			t.Fatalf("Parse() error = %v", err)
		}
	})

	for _, test := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{
			name: "production feature digest mismatch",
			mutate: func(root map[string]any) {
				invalidDigest := strings.Repeat("f", 64)
				root["manifest"].(map[string]any)["featureSchemaSha256"] = invalidDigest
				goldenInput(root)["featureSchemaSha256"] = invalidDigest
			},
		},
		{
			name: "production feature order mismatch",
			mutate: func(root map[string]any) {
				identities := root["manifest"].(map[string]any)["actorFeatureIds"].([]any)
				identities[0], identities[1] = identities[1], identities[0]
				features := goldenInput(root)["actorFeatures"].([]any)
				features[0], features[1] = features[1], features[0]
			},
		},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			root := decodeArtifact(t, valid)
			test.mutate(root)
			rebindArtifactDigests(t, root)
			model, err := inferencemodel.Parse(canonicalArtifact(t, root))
			if err != nil {
				t.Fatalf("Parse() must leave production feature ownership to the runtime gate: %v", err)
			}
			if err := recommendation.ValidateInferenceModel(model, inferencemodel.PurposeAcceptanceTest); err == nil {
				t.Fatal("ValidateInferenceModel() error = nil")
			}
		})
	}

	for name, mutate := range map[string]func(map[string]any){
		"knowledge identity outside catalog key domain": func(root map[string]any) {
			renameKnowledgeIdentity(root, "arrays", "Bad")
		},
		"knowledge identities are not strictly sorted": func(root map[string]any) {
			renameKnowledgeIdentity(root, "arrays", "zeta")
			renameKnowledgeIdentity(root, "graphs", "alpha")
		},
	} {
		name, mutate := name, mutate
		t.Run(name, func(t *testing.T) {
			root := decodeArtifact(t, valid)
			mutate(root)
			rebindArtifactDigests(t, root)
			if _, err := inferencemodel.Parse(canonicalArtifact(t, root)); !errors.Is(err, inferencemodel.ErrInvalidArtifact) {
				t.Fatalf("Parse() error = %v", err)
			}
		})
	}

	t.Run("knowledge point count exceeds catalog maximum", func(t *testing.T) {
		root := decodeArtifact(t, valid)
		expandKnowledgeIdentities(root, 1025)
		rebindArtifactDigests(t, root)
		if _, err := inferencemodel.Parse(canonicalArtifact(t, root)); !errors.Is(err, inferencemodel.ErrInvalidArtifact) {
			t.Fatalf("Parse() error = %v", err)
		}
	})
}

func TestTrainingProvenanceIsAnOpaqueDigestBindingAtInference(t *testing.T) {
	t.Parallel()
	valid := mustRead(t, filepath.Join(recommendationContractDirectory(t), "fixtures", "synthetic-test-only.inference-model.v1.json"))
	root := decodeArtifact(t, valid)
	opaqueDigest := strings.Repeat("e", 64)
	root["manifest"].(map[string]any)["trainingProvenanceSha256"] = opaqueDigest
	model, err := inferencemodel.Parse(canonicalArtifact(t, root))
	if err != nil {
		t.Fatalf("Parse() rejected an opaque well-formed provenance binding: %v", err)
	}
	if err := recommendation.ValidateInferenceModel(model, inferencemodel.PurposeAcceptanceTest); err != nil {
		t.Fatalf("ValidateInferenceModel() rejected an opaque provenance binding: %v", err)
	}
	if model.Manifest().TrainingProvenanceSHA256 != opaqueDigest {
		t.Fatalf("training provenance binding = %q", model.Manifest().TrainingProvenanceSHA256)
	}

	root = decodeArtifact(t, valid)
	root["manifest"].(map[string]any)["trainingProvenanceSha256"] = strings.Repeat("E", 64)
	if _, err := inferencemodel.Parse(canonicalArtifact(t, root)); !errors.Is(err, inferencemodel.ErrInvalidArtifact) {
		t.Fatalf("Parse() error = %v", err)
	}
}

func recommendationContractDirectory(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate contract test source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(source), "..", "..", "..", "contracts", "recommendation"))
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return raw
}

func readCanonicalObject(t *testing.T, path string, limit int) ([]byte, string) {
	t.Helper()
	raw := mustRead(t, path)
	canonical, digest, err := canonicaljson.Object(raw, limit)
	if err != nil {
		t.Fatalf("canonicalize %s: %v", path, err)
	}
	if !bytes.Equal(raw, canonical) {
		t.Fatalf("%s is not stored as canonical bytes", path)
	}
	return raw, digest
}

func decodeClosed(t *testing.T, raw []byte, target any) {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		t.Fatalf("JSON trailer error = %v", err)
	}
}

func decodeArtifact(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var root map[string]any
	decodeClosed(t, raw, &root)
	return root
}

func canonicalArtifact(t *testing.T, root map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	canonical, _, err := canonicaljson.Object(raw, inferencemodel.MaximumArtifactBytes)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

func digestValue(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"value": value})
	if err != nil {
		t.Fatal(err)
	}
	canonical, _, err := canonicaljson.Object(raw, inferencemodel.MaximumArtifactBytes)
	if err != nil {
		t.Fatal(err)
	}
	var wrapper struct {
		Value json.RawMessage `json:"value"`
	}
	decodeClosed(t, canonical, &wrapper)
	digest := sha256.Sum256(wrapper.Value)
	return hex.EncodeToString(digest[:])
}

func goldenInput(root map[string]any) map[string]any {
	return root["goldenVectors"].([]any)[0].(map[string]any)["input"].(map[string]any)
}

func rebindArtifactDigests(t *testing.T, root map[string]any) {
	t.Helper()
	manifest := root["manifest"].(map[string]any)
	manifest["parameterSha256"] = digestValue(t, root["parameters"])
	manifest["goldenVectorsSha256"] = digestValue(t, root["goldenVectors"])
}

func renameKnowledgeIdentity(root map[string]any, previous, next string) {
	manifest := root["manifest"].(map[string]any)
	for index, identity := range manifest["knowledgePointIds"].([]any) {
		if identity == previous {
			manifest["knowledgePointIds"].([]any)[index] = next
		}
	}
	for _, parameter := range root["parameters"].(map[string]any)["knowledgeParameters"].([]any) {
		entry := parameter.(map[string]any)
		if entry["knowledgePointId"] == previous {
			entry["knowledgePointId"] = next
		}
	}
	vector := root["goldenVectors"].([]any)[0].(map[string]any)
	for _, weight := range vector["input"].(map[string]any)["knowledgeWeights"].([]any) {
		entry := weight.(map[string]any)
		if entry["knowledgePointId"] == previous {
			entry["knowledgePointId"] = next
		}
	}
	for _, mastery := range vector["expected"].(map[string]any)["knowledgeMastery"].([]any) {
		entry := mastery.(map[string]any)
		if entry["knowledgePointId"] == previous {
			entry["knowledgePointId"] = next
		}
	}
}

func expandKnowledgeIdentities(root map[string]any, count int) {
	manifest := root["manifest"].(map[string]any)
	actorCount := len(manifest["actorFeatureIds"].([]any))
	identities := make([]any, count)
	parameters := make([]any, count)
	weights := make([]any, count)
	mastery := make([]any, count)
	for index := range count {
		identity := fmt.Sprintf("k%04d", index)
		identities[index] = identity
		actorWeights := make([]any, actorCount)
		for featureIndex := range actorWeights {
			actorWeights[featureIndex] = json.Number("0")
		}
		parameters[index] = map[string]any{
			"actorFeatureWeights": actorWeights,
			"bias":                json.Number("0"),
			"knowledgePointId":    identity,
		}
		weight := "0"
		if index == 0 {
			weight = "1"
		}
		weights[index] = map[string]any{"knowledgePointId": identity, "weight": json.Number(weight)}
		mastery[index] = map[string]any{"knowledgePointId": identity, "probability": json.Number("0.5")}
	}
	manifest["knowledgePointIds"] = identities
	root["parameters"].(map[string]any)["knowledgeParameters"] = parameters
	vector := root["goldenVectors"].([]any)[0].(map[string]any)
	vector["input"].(map[string]any)["knowledgeWeights"] = weights
	vector["expected"].(map[string]any)["knowledgeMastery"] = mastery
	vector["expected"].(map[string]any)["probability"] = json.Number("0.5")
}
