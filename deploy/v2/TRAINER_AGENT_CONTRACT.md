# Recommendation trainer-agent transport contract v1

This internal contract connects the RTX workstation Go supervisor to the km6
Go service. It is not a browser API and is not exposed in the public OpenAPI
document.

## Ownership

- `ascendanyd` owns PostgreSQL, queue transitions, lease fencing, the durable
  artifact store, output validation, and atomic model activation.
- `ascendany-trainer-agent` owns one scoped bearer credential, HTTPS polling,
  lease heartbeats, local child-process supervision, and bounded transfer of
  canonical bundles.
- The Python child reads one immutable input bundle from stdin and writes one
  canonical output bundle containing model parameters and training diagnostics.
  Its bubblewrap namespace has no network, bearer credential, database socket,
  or km6 artifact path. It does not materialize or persist student results.

The workstation receives no PostgreSQL credential and never mounts
`/var/lib/ascendany/artifacts`.

## Pinned Python and CUDA runtime

The only production interpreter is selected through
`/opt/ascendany-trainer-runtime/current/python/bin/python3.14`. The `current`
entry is one root-owned relative symlink to an immutable
`torch-2.13.0-cu130-<construction-sha256>` directory.
It is relocatable CPython `3.14.6` selected by the reviewed
`runtime-python-cu130.json` upstream URL and SHA-256 contract. The root installer
uses exact `uv 0.9.26`, captures the release manifest, requirements lock,
distribution closure, Python source contract, installer, and both identity
tools into a private stage before any network work, removes the exact PBS seed
pip files, and installs every production distribution from the hash-required
lock. It compares live `importlib.metadata` with the exact 30-item closure before
publishing an immutable provenance marker and the captured construction inputs.
The runtime contains exactly `torch==2.13.0+cu130`; `torch.version.cuda` is
exactly `13.0`, and
`torch.cuda.is_available()` must be true for the `ascendany-trainer` identity.
The host validator also creates a tensor on the one configured `cuda:0` device
and synchronizes it before accepting the runtime.

The runtime parent and construction directory are canonical `root:root` mode
`0755` directories. The construction tree contains only root-owned, non-writable regular
files, directories, and safe internal portable-Python symlinks; external or
dangling links, hard-link aliases, and special nodes fail. The exact Python
executable is a mode-`0755` single-link regular file. The host validator verifies
the sealed source manifest and all six construction-input digests against the
current release manifest, recomputes the complete path/mode/content/symlink tree
identity before Python import, then compares the complete live distribution set
with the reviewed closure. It also reproduces a CUDA `/proc/self/maps` identity
inside the same `/usr`-free bubblewrap runtime/device namespace for every runtime-external
regular file and binds that set to the NVIDIA driver
version and kernel-driver version digest. Runtime replacement is a
stopped-unit, new-version-directory
operation. In-place package mutation is outside the production contract. The
installer serializes construction with a host lock, publishes new construction
directories side by side, and replaces only the relative `current` selector
atomically. A failed post-switch attestation restores the previous selector.

The portable interpreter source is the 20260623 python-build-standalone asset
`cpython-3.14.6+20260623-x86_64-unknown-linux-gnu-install_only_stripped.tar.gz`,
with exact size `36,049,152` bytes and SHA-256
`c172314f4a8ec137a8f605289010c3d19c8b56867d968f0095074cc68efa1d29`.

The bubblewrap child receives the exact sorted read-only mount list
`/lib,/lib64,/opt/ascendany-trainer-runtime/current,/sys`, the
release-owned package parent mounted at `/trainer/recommendation`, its private
output directory, and the three declared NVIDIA character devices. `/usr` is
absent from the namespace. The Go
binary uses a fixed `runpy` bootstrap that inserts only that sandbox package
parent and runs `ascendany_recommendation_trainer` as `__main__`; caller paths
and `PYTHONPATH` have no authority. The supervisor clears the inherited
environment before setting the closed child allowlist, then invokes Python with
`-B -s -P -c`. Safe-path and user-site isolation stay enabled while the
interpreter consumes the sole declared `PYTHONHASHSEED=0` value. All seven
package source files are separate release-manifest entries. The systemd unit
marks the runtime parent and package parent read-only and runs the exact
same-process runtime attestation before the Go supervisor starts.
Both the HTTPS transport and child protocol cap input and output bundles at
128 MiB (`134217728` bytes). The server artifact-object cap is also 128 MiB,
so every transport-valid output can enter the immutable artifact publication
path.

CUDA training receives the exact deterministic cuBLAS setting
`CUBLAS_WORKSPACE_CONFIG=:4096:8` together with the fixed seed and
`PYTHONHASHSEED=0`. Missing or altered deterministic compute configuration is a
configuration error. `CUDA_VISIBLE_DEVICES`, `MKL_NUM_THREADS`,
`OMP_NUM_THREADS`, and `OPENBLAS_NUM_THREADS` are also mandatory child
configuration; each thread count is a canonical integer from 1 to 256. The
reviewed production environment fixes all three thread counts to `8` and
exposes only CUDA device `0`.

## Deployment transport and cutover independence

The production deployment fixes the trainer origin to
`https://ascendany-trainer.kkkzbh.cn`. The trainer never uses the public
application origin `https://ascendany.kkkzbh.cn`, so changing that origin from
the legacy listener to v2 cannot gate model acceptance.

Before starting the production trainer, the existing remotely managed km6
Cloudflare Tunnel must publish these ordered ingress rules ahead of its global
catch-all:

1. hostname `ascendany-trainer.kkkzbh.cn`, path `^/version$`, service
   `http://127.0.0.1:18000`;
2. hostname `ascendany-trainer.kkkzbh.cn`, path
   `^/api/v2/internal/recommendation/trainer-agent/claims(/.*)?$`, service
   `http://127.0.0.1:18000`;
3. hostname `ascendany-trainer.kkkzbh.cn`, no path, service
   `http_status:404`.

The Tunnel owns public DNS, edge TLS, and the outbound connection to the km6
loopback listener. `ascendanyd` owns bearer authentication, agent binding,
rate limits, protocol validation, queue transitions, and durable publication.
The dedicated hostname exposes only release metadata and the scoped trainer
transport; the application bearer token remains the sole trainer credential.
SSH forwarding, a process proxy, direct origin access, and alternate hostnames
are outside this deployment contract. The route remains stable through public
origin cutover and normal production operation.

## HTTP envelope

The base path is `/api/v2/internal/recommendation/trainer-agent`. Every JSON
body is exactly one canonical UTF-8 JSON object with no unknown or duplicate
fields. Requests and successful responses use the exact versioned media type
for their operation. Errors use
`application/vnd.ascendany.recommendation.trainer-agent.error.v1+json`.
Content encodings, redirects, URL credentials, proxy environment variables,
and plaintext HTTP are forbidden.

All requests carry `Authorization: Bearer <token>`. The server binds each token
to one configured `agentId`, authenticates before reading a large body, and
rate-limits claim attempts. Tokens grant only this four-operation transport;
they grant no user, admin, artifact-path, or general API capability.

The exact protocol IDs, media types, and DTO field names are declared in
`backend/internal/traineragentprotocol/protocol.go`.

## Operations

| Operation | Method and path | Success |
| --- | --- | --- |
| Claim immutable input | `POST .../claims` | `200` with `ClaimResponseV1`, or bodyless `204` with no `Content-Type` |
| Renew lease | `POST .../claims/{runId}/heartbeats` | `200` with `HeartbeatResponseV1` |
| Upload output artifact | `POST .../claims/{runId}/output` | `200` with `OutputResponseV1` |
| Report child failure | `POST .../claims/{runId}/failures` | `200` with `FailureResponseV1` |

`runId` and `attemptToken` are canonical UUIDv4 values. Digests are lowercase
SHA-256 hex. Timestamps are canonical UTC RFC3339Nano strings. Lease durations
are positive whole milliseconds and the claim response echoes the requested
duration exactly.

The claim embeds the immutable canonical training bundle plus its bundle and
input-manifest digests. The output request embeds the immutable canonical
training-output bundle, its digest, and the claimed input-manifest digest. The
training-output bundle itself contains the model parameter manifest and
trainer diagnostics. `ascendanyd` reloads the immutable input artifact,
validates the complete parameter set and hashes, recomputes held-out metrics
and the publication quality gate, and materializes the exact sorted student
result set under Go ownership.

## Lease and terminal semantics

The server generates a fresh attempt token while claiming a queued or expired
run. Every heartbeat and terminal operation fences on the tuple
`(runId, agentId, attemptToken)`. A stale tuple receives a versioned structured
error and cannot extend, fail, or publish the run.

Output handling validates the complete output bundle and publishes its bytes
to the artifact store before one database transaction fences the claim and
activates or supersedes the model. A non-retryable output rejection records a
terminal failed run in the same server operation. Failure reporting either
records a terminal failure or requeues the run according to its explicit
`retryable` value and server retry policy.

Terminal operations are safe under response loss. If the server committed and
the workstation did not receive the response, later heartbeats are fenced and
the run is not executed again. If the request never committed, the lease
expires and server-owned recovery requeues it. The agent does not guess whether
an ambiguous terminal request committed.

The server bounds request bodies before decoding, verifies declared hashes
against exact canonical bytes, rejects unsupported protocols and media types,
and returns only bounded canonical error details. The agent independently
bounds response bodies and verifies every claim, digest, protocol, timestamp,
and terminal disposition before acting.

## Acceptance evidence

After the server accepts the first terminal output request for a release, the
Go agent atomically writes one canonical `ascendany.trainer.acceptance.v3`
candidate at the
explicit `ASCENDANY_TRAINER_AGENT_ACCEPTANCE_CANDIDATE_PATH`. Its immediate
directory is pre-created as trainer-owned mode `0700`; the candidate is a
single-link trainer-owned mode `0600` regular file. The candidate is write-once
for its exact release, agent, and origin; later successful training runs leave
its bytes unchanged. Restart accepts an existing candidate only after exact
canonical validation against that identity. A failed, fenced, or ambiguous
terminal request does not produce a new candidate. A new release requires a
stopped-unit operator step that archives and removes the old candidate first.

The candidate binds the release version and commit, agent/origin, run and
attempt token, canonical terminal-request SHA-256, input manifest, output
bundle, model, runtime construction, runtime provenance marker, portable tree,
host capability, complete same-process runtime attestation, disposition, and
ordered claim/heartbeat/upload timestamps. A
release version is canonical SemVer capped at 128 ASCII bytes. The offline
`ascendany-trainer-agent verify-acceptance` command reads one bounded candidate
from stdin and succeeds silently only for its exact canonical JSON bytes and
nanosecond-ordered canonical UTC RFC3339Nano timestamps. A
root operator stops the agent, copies the exact candidate bytes through a
temporary file, and atomically promotes them to root-owned mode `0600`
`/var/lib/ascendany-acceptance/trainer-latest.json`; the same promoted bytes are
installed on km6. The RTX validator requires byte identity between candidate
and promoted evidence. The km6 validator joins
`(runId, attemptToken, agentId, requestSHA256)` to the immutable terminal
receipt, run, model, and output artifact, then hashes the published artifact
bytes. Promoted evidence lives in a root-owned mode-`0700` directory.

Trainer-host validation has three closed phases. `staged` requires the unit to
be disabled, inactive, and dead, with an empty work root, an empty candidate
directory, and no promoted evidence; it validates the fixed endpoint without
contacting it. `production` requires the unit to be enabled, active, and
running with byte-identical candidate and promoted evidence. `quiesced`
requires the unit to remain enabled while inactive and dead, with an empty work
root and the same evidence. Both evidence-bearing phases require the dedicated
origin to serve the exact release at `/version` and return `404` for `/livez`,
proving the path-closed Tunnel route is active.

Release validation must also retain the full integration trace proving:

1. authenticated claim and exact bundle digest verification;
2. initial and periodic heartbeat renewal;
3. successful output upload, durable artifact verification, and atomic model
   activation;
4. stale-attempt fencing for heartbeat, output, and failure operations;
5. retryable failure requeue and non-retryable terminal failure;
6. response-loss behavior for both committed and uncommitted output requests;
7. absence of PostgreSQL configuration and artifact-store access on the RTX
   service and absence of network/credential access inside the Python child.
