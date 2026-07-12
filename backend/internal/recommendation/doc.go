// Package recommendation owns durable recommendation training and student
// result reads. Its PostgreSQL boundary is the recommendation tables from
// migration 0002; no parallel generation-input table is used.
//
// # Training input contract
//
// The administrator selects a configuration key. The service snapshots the
// current succeeded analytics head, the active immutable training
// configuration version, its referenced immutable knowledge catalog, and the
// exact Pintia facts and ranking observations pinned by that analytics
// generation. BuildInputBundle owns the reviewed Q-matrix, deterministic
// per-actor validation split, and train-only feature vectors. It emits protocol
// ascendany.recommendation.training-bundle.v2 without account identifiers.
// Queue publication keeps the
// artifact store's per-hash lock through the database commit. The queue
// transaction re-resolves the administrator and reconstructs the snapshot, so
// a changed analytics head or active configuration fails directly.
//
// # Trainer output contract
//
// The remote trainer agent publishes a compact canonical JSON object with protocol
// ascendany.recommendation.training-output.v2. It contains the exact
// inputManifestSha256 and one ascendany.recommendation.model.v2 numerical model.
// Go revalidates the complete input artifact, recomputes normalization and
// quality metrics, then materializes one ascendany.recommendation.result.v2 for
// every input actor. Duplicate or unknown fields, padded JSON, noncanonical
// numbers, provenance drift, shape drift, metric drift, and malformed learning
// paths are rejected. Python process invocation is owned by the separate
// trainer-agent executable and is absent from the online server.
//
// # Publication contract
//
// Claim, renewal, retry, failure, and terminal publication all carry the
// attempt token, attempt count, owner, and live lease. Each transition appends
// the next durable event while holding the run row lock. A successful output
// transaction inserts the artifact reference, terminal run state, immutable
// model, and the complete student result set atomically. It advances
// recommendation_head only when the analytics head still matches and the
// recommendation head CAS can advance monotonically. Other valid outputs are
// recorded with the superseded outcome.
package recommendation
