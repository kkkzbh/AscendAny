# AscendAny recommendation trainer

This package is the isolated Python numerical-compute boundary for AscendAny
recommendation training. Go owns PostgreSQL access, corpus construction,
feature calculation, split selection, durable scheduling, quality-gate
recomputation, recommendation ranking, learning-path materialization, artifact
publication, and model activation.

The trainer accepts one immutable canonical
`ascendany.recommendation.training-bundle.v2` document on standard input. It
strictly validates the manifest, provenance, catalog, collection identities,
references, ordering, split, counts, and train-only feature formulas before
allocating model tensors. Unknown fields and non-finite numbers fail the run.

The sole algorithm is `knowledge_mirt_v1`, implemented with PyTorch float64.
Actor and problem feature columns are z-score normalized with published
population means and scales before the forward pass:

```text
theta_s = W_s * normalize(x_s) + u_s
b_p = w_p * normalize(z_p) + delta_p
a_p = softplus(raw_a_p) + 1e-6
p(s,p) = sigmoid(a_p * (q_p dot theta_s - b_p))
```

Soft score rates are optimized with binary cross-entropy and AdamW. Every
training hyperparameter in the v2 configuration is required and validated;
the configured accelerator is authoritative, so unavailable CUDA fails
directly. The trainer emits only model parameters, their canonical SHA-256,
and reproducible diagnostics. It never emits student recommendations or
learning paths.

The output protocol is `ascendany.recommendation.training-output.v2`. On
success, canonical output bytes are written both to stdout and to an exclusive
mode-0600 `output.json` beneath `ASCENDANY_TRAINER_OUTPUT_DIR`, followed by file
and directory `fsync`. Go independently recomputes predictions, baseline and
validation metrics before any publish decision.

Production selects one immutable construction-addressed root-owned runtime
through `/opt/ascendany-trainer-runtime/current`. The runtime has no database
credential, host network, production data path, or writable filesystem beyond
the one output directory. Before reading stdin, the child validates the exact
selector, provenance marker, captured construction inputs, seven-file source
package, portable Python tree, mapped host libraries, NVIDIA driver, mount and
device set, CPython, PyTorch, CUDA, and a real CUDA tensor operation. The model
manifest carries the resulting five runtime digests.

CPU tests use the project Python 3.14 environment. The production CUDA
dependency hashes are recorded in `runtime-requirements-cu130.lock`; the exact
normalized installed distribution set is recorded in
`runtime-closure-cu130.json`; and `runtime-wheels-cu130.json` binds every
offline wheel URL, filename, and SHA-256. The root installer captures official
`uv 0.9.26` and executes `uv pip sync` in a fresh networkless `/usr`-free
bubblewrap namespace using only the reviewed private wheelhouse.

Exit codes are `0` for success, `2` for an invalid contract or environment,
`3` for an invalid or unpublishable model output, and `70` for an unavailable
runtime capability or internal failure.
