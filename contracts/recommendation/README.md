# Recommendation inference-model contract

This directory is the machine-readable boundary between an external training
module and AscendAny production inference. Production accepts one immutable
`ascendany.recommendation.inference-model.v1` artifact. It never starts a
trainer, imports a training library, or reads a training dataset.

## Normative files

- `ascendany.recommendation.inference-model.v1.schema.json` is the closed
  Draft 2020-12 structural schema.
- `ascendany.recommendation.feature-schema.v1.json` is the canonical production
  feature schema. Its SHA-256 is
  `09c18717b8de4b3dba6c8bd9341fb237176c6d22ab7f99e2e937bf7b387a060f`.
- `vectors/ascendany.recommendation.contract-vectors.v1.json` contains
  canonicalization, byte-length, and digest vectors.
- `vectors/ascendany.recommendation.feature-extraction-vectors.v1.json`
  executes representative actor and problem aggregation, nullable-value,
  transform, and zero-denominator cases against the online Go implementation.
- `fixtures/synthetic-test-only.inference-model.v1.json` is a complete
  `acceptance_test` artifact used by parser and CLI tests.
- `fixtures/e2e-test-only.inference-model.v1.json` and
  `fixtures/e2e-test-only.knowledge-catalog.v1.json` are a matched acceptance
  pair bound to the sanitized Pintia exporter fixture's exact problem facts.

Both checked-in model fixtures declare `manifest.purpose = "acceptance_test"`.
Their parameters were selected by hand and do not attest to a training dataset.
Production release construction and runtime startup require the immutable
artifact purpose `production`; acceptance releases and runtimes must explicitly
require `acceptance_test`. Purpose mismatch fails before database mutation.

## Artifact encoding and hashes

The artifact is a UTF-8 JSON object between 1 byte and 16 MiB inclusive. Its
file bytes must already be in the repository canonical JSON encoding:

1. Reject invalid UTF-8, a non-object root, duplicate object keys, NUL in a key
   or string value, a trailing JSON token, and nesting deeper than 64 levels.
2. Treat every number as an exact rational decimal. Reject exponents outside
   `[-8192,8192]`, a denominator containing factors other than 2 or 5, more
   than 4096 decimal places, or a canonical number longer than 8192 bytes.
3. Render numbers without an exponent or redundant fractional zeroes and
   render negative zero as `0`.
4. Encode compact JSON with the same object-key ordering and string escaping as
   Go `encoding/json.Marshal`. No insignificant whitespace or final newline is
   present.

`backend/internal/canonicaljson.Object` is the normative implementation. The
canonicalization vector makes independent implementations testable.

Hash fields use lowercase hexadecimal SHA-256:

- the release artifact digest covers the complete canonical artifact bytes;
- `parameterSha256` covers the canonical `parameters` value bytes;
- `goldenVectorsSha256` covers the canonical `goldenVectors` value bytes;
- `featureSchemaSha256` covers the complete canonical feature-schema file;
- `knowledgeCatalogSha256` covers the complete canonical knowledge-catalog
  document bound to the model;
- `trainingProvenanceSha256` covers the canonical bytes of the external
  training module's immutable input provenance manifest.

The external training module owns the provenance manifest and must retain it
with the training run. The production binary validates and persists the
`trainingProvenanceSha256` binding as a 64-character lowercase digest string.
Production does not fetch, parse, reconstruct, or trust the referenced
provenance manifest during inference.

`manifest.purpose` is a required deployment authorization with exactly two
values: `production` and `acceptance_test`. The expected purpose is supplied
independently by release construction and runtime configuration, persisted in
model release provenance, and compared with the artifact value. A production
builder cannot accept either checked-in fixture because both are structurally
marked `acceptance_test`.

## Ordered feature and knowledge contract

`actorFeatureIds` and `problemFeatureIds` must byte-for-byte equal the ordered
`id` values in the feature schema's `actorFeatures` and `problemFeatures`
arrays. Each definition also binds source protocol and fields, aggregation
scope and operation, missing-value encoding, transform, source/output domain,
and denominator-zero behavior. The production runtime checks the complete
schema digest and both ordered model arrays. Any extraction-semantic change or
feature reorder is a contract change.

`knowledgePointIds` must equal the ordered IDs in the bound canonical knowledge
catalog. The array contains 1 through 1024 strictly ascending, unique IDs that
match `^[a-z][a-z0-9_.-]{0,127}$`, which is the knowledge-catalog key domain.
Every parameter vector, golden input, golden output, and runtime input uses
manifest order. Counts, identities, and order must all match. Golden knowledge
weights are canonical decimal strings in `(0,1]` and sum to exactly one as rational decimals; runtime
weights use the same range and a fixed `1e-12` sum tolerance.

## Inference semantics

For actor features `x`, problem features `p`, and ordered knowledge weights
`q`, evaluation is:

```text
x_norm[i] = (x[i] - actor_mean[i]) / actor_scale[i]
p_norm[j] = (p[j] - problem_mean[j]) / problem_scale[j]
theta[k]  = knowledge_bias[k] + dot(knowledge_actor_weights[k], x_norm)
mastery[k] = sigmoid(theta[k])
ability    = sum(q[k] * theta[k])
difficulty = difficulty_bias + dot(problem_feature_weights, p_norm)
probability = sigmoid(discrimination * (ability - difficulty))
```

All model parameters must be finite float64 values with absolute value at most
100. Normalization scales and `discrimination` must be positive. Evaluation
uses manifest order and Neumaier summation. Every golden vector is executed at
parse time; probabilities must agree within absolute tolerance `1e-12`.

Every feature definition contains finite `outputMinimum` and `outputMaximum`
values derived from the production extractor's closed source domain and
transform. The runtime validates the full model numeric envelope at release
verification and startup: both normalization extrema, every weighted linear
term, every knowledge theta, the admissible weighted ability interval,
difficulty, and the final discrimination logit must remain finite. A benign
golden vector cannot authorize a scale or parameter that overflows elsewhere
in the production feature domain.

## Validation gates

JSON Schema validation is the first structural gate. It rejects missing or
unknown fields, malformed identities and digests, invalid constants, and basic
numeric bounds. The Go parser remains authoritative for properties JSON Schema
cannot express, including canonical source bytes, canonical UTC timestamps,
float64 representability, cross-field array lengths and order, subdocument
digests, exact weight sums, and golden-vector execution.

`trainedAt` is canonical UTC RFC3339 using `Z`, with a common-era year from
`0001` through `9999`, zero through six
fractional-second digits and no redundant trailing fractional zero. This is the
exact precision preserved by PostgreSQL `timestamptz`; parsing and formatting
with Go `time.RFC3339Nano` must reproduce the original string byte-for-byte.

`recommendation.ValidateInferenceModel` then binds the parsed model to the
fixed production feature schema. At inference time the active knowledge
catalog digest and ordered knowledge IDs must also match the model manifest.
Any failed gate rejects the release directly.

The installed artifact must be a regular file at an absolute clean path, mode
`0644`, with exactly one hard link. Verify a candidate release with:

```sh
ascendany-model verify \
  --model /absolute/path/model.json \
  --sha256 64_lowercase_hexadecimal_characters \
  --expected-purpose production
```

The synthetic fixture digest is
`5182ed451d74a4e10d8384f3a4d9fcb2a8d2ad7d043e3721f2247e10c029bf58`.
The full-E2E-only model/catalog digests are
`26798ac81a219fd2e38aa5cf45f47eec460f75bd039aa8fc8db45dd11425e0a8` and
`9db76af3f2b8e6fa018b6a955e674b0273bb457582981e67dfc159e12c7d43bf`.
