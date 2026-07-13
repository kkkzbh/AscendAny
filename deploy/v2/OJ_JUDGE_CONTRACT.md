# OJ judge v2 execution contract

This document is the closed production contract between `ascendanyd`, one
`ascendany-judge@<job-id>.service` instance, the C++20 compiler image, and the
empty execution image.

## Ownership and control flow

1. The Go durable OJ worker claims and leases one database job.
2. `judgeexecutor.Executor` re-verifies and opens the source, optional stdin,
   and test-bundle artifacts before starting any service.
3. `SystemdLauncher` starts exactly
   `ascendany-judge@<canonical-uuid>.service`. The polkit rule permits the
   `ascendany` OS user to start or stop only this UUID-shaped template.
4. `systemd-tmpfiles` owns the persistent, setgid mode-`2770`
   `/run/ascendany-judge` directory. The instance keeps its dedicated primary
   GID and creates `/run/ascendany-judge/<job-id>.sock` with mode `0660`; the
   socket inherits the `ascendany-runtime` group.
   Both peers verify `SO_PEERCRED` UID before transferring job data.
5. A canonical JSON request header is length-prefixed and followed by exact
   source, stdin, and test-bundle byte ranges. Every range is bound to its
   declared size and SHA-256. The response uses the same framing and binds any
   output bytes to a SHA-256 in its canonical header.
6. The instance validates and materializes the bundle under its private
   per-job directory, invokes only rootless Podman argv, returns one result,
   deletes the work directory, per-job Podman runtime and socket, and exits.

There is no TCP listener, database client, credential input, host execution,
shell command, image pull, or shared artifact mount in the judge process.

## Capability dependency boundary

`internal/judgecontract` is the single capability-free owner of execution
request/result values, verdicts, artifact descriptors, UUID validation, problem
spec parsing, and exact/token checker semantics. The durable `internal/oj`
worker and the isolated protocol/runner both depend on this package directly;
the isolated process never imports the online OJ domain.

The `ascendany-judge` package test runs `go list -deps` and rejects PostgreSQL
or `internal/database`, `artifact`, `auth`, `credential`, `principalguard`, and
`oj` dependencies. This gate makes the capability claim a build invariant.

## Problem spec

`problemSpec` must be canonical JSON with this exact shape:

```json
{"checker":"exact","schema":"ascendany.oj.problem-spec.v1"}
```

`checker` is one of `exact` or `tokens`. Unknown and duplicate fields are
rejected. `exact` compares raw stdout bytes. `tokens` compares Unicode strings
split by Go whitespace semantics.

## Test bundle

The media type is `application/vnd.ascendany.oj-test-bundle.v1+tar`. The tar is
deterministic USTAR. Every member is a regular file with mode `0600`, uid/gid
zero, epoch mtime, empty user/group/link/PAX/xattr fields, and the exact order:

1. `manifest.json`
2. `cases/<id>.in`
3. `cases/<id>.out`
4. Repeat steps 2–3 in manifest order.

The manifest is canonical JSON:

```json
{"cases":[{"id":"case-001","weight":1}],"schema":"ascendany.oj.test-bundle.v1"}
```

Case identifiers match `[a-z0-9][a-z0-9_-]{0,63}`, are sorted and unique, and
weights are positive bounded integers. Paths are derived from identifiers;
the manifest cannot provide filesystem paths. Directories, symlinks, hardlinks,
device nodes, traversal, duplicate members, undeclared members, noncanonical
JSON and trailing archive data are rejected before compilation.

## Two-image container boundary

The compiler and runtime references contain distinct immutable `sha256`
digests and are preloaded for the `ascendany-judge` rootless Podman identity.
The compiler is the reviewed Alpine 3.23.5 rootfs with exact
`g++-15.2.0-r2`; its release-bound inventory records every path, type, mode,
symlink target hash, and regular-file hash. The runtime is an exact empty
`scratch` rootfs. Python, trainer, CUDA, accelerator runtime, shell, libc, and
compiler files cannot enter execution.

Compilation always supplies `-static`. The Go runner parses the resulting ELF
and requires Linux/amd64 `ET_EXEC`, a nonzero entry point, at least one
`PT_LOAD` segment, and no `PT_INTERP` or `PT_DYNAMIC`. The resulting regular
executable is copied into a distinct execution directory and is the only file
mounted into the empty runtime image.

Every compile and run uses `--pull=never`, `--network=none`, a read-only image, no capabilities,
`no-new-privileges`, private PID/IPC/UTS/cgroup namespaces, an exact PID limit,
memory plus no-swap limit, CPU quota, bounded no-exec tmpfs, and hard combined
stdout/stderr capture. The explicit rootless `--userns=host` mapping maps
container UID/GID 0 to the dedicated Judge process identity. It preserves the
preloaded image-layer ownership and gives the container no host privilege
beyond that unprivileged account.

The outer unit leaves `ProtectHostname` disabled so crun can set the hostname
after entering the container's private UTS namespace. The dedicated service
identity has no `CAP_SYS_ADMIN`, so it cannot change the host UTS namespace.

Each job owns both a private systemd-created XDG runtime directory and a
private Podman `--runroot` with `--transient-store`, so concurrent
`PrivateNetwork` service instances never share pause, user/cgroup namespace,
or container metadata. Image loading and attestation use a separate persistent
operator XDG runtime. Image layers remain in the dedicated user's shared
rootless image store. The outer service permits only
`CAP_SETUID`/`CAP_SETGID` in its bounding
set so the file-capability `newuidmap`/`newgidmap` helpers can establish the
allocated subordinate ranges; it grants no ambient capability. The inner
container drops all capabilities and sets `no-new-privileges`. Podman uses the
pre-created delegated `containers` cgroup through exact
`--cgroups=enabled --cgroup-parent=/containers` arguments, creating bounded
payload children beside the `supervisor` subgroup. Podman clients
receive a parent-death `SIGKILL` and do not mutate process groups; container
removal remains the sole timeout cleanup path.

The outer unit exposes all procfs entries while retaining
`ProtectProc=invisible`, because Podman and crun must read namespace capability
metadata. It permits kernel tunable and proc-kmsg mount access so crun can
mount procfs and write `ping_group_range` after entering the private user and
network namespaces. The unprivileged service identity has no host capability
to mutate those host resources. Explicit no-exec tmpfs mounts isolate `/tmp`
and `/var/tmp`. `RemoveIPC` remains disabled to preserve Podman's shared,
SELinux-labelled image-store lock; container argv still fixes `--ipc=none`.

Compilation mounts only the private compile directory read-write. Execution
mounts only its copied executable directory read-only. Test input is sent over
stdin. Expected output and all other cases remain outside the container mount.

Each result binds the canonical closed manifest
`ascendany.oj.execution-manifest.v2`, containing `compilerImage`,
`runtimeImage`, `mode`, `checker`, and the ordered case evidence. Both image
identities therefore participate in the execution provenance hash.

`timeout`, `memory`, `output`, compile, runtime and wrong-answer outcomes are
explicit verdicts. Container startup/supervision failures are classified as
retryable system failures. Invalid protocol, artifact, problem-spec, bundle or
result contracts are permanent failures.

## API-side construction

The production wiring is intentionally explicit:

```go
launcher, err := judgeexecutor.NewSystemdLauncher("/usr/bin/systemctl")
executor, err := judgeexecutor.New(artifactStore, launcher, judgeexecutor.Config{
    SocketDirectory:  "/run/ascendany-judge",
    ExpectedJudgeUID: resolvedJudgeUID,
    StartupTimeout:   30 * time.Second,
    SessionTimeout:   30 * time.Minute,
    StopTimeout:      15 * time.Second,
    Policy:           ojPolicy,
})
worker, err := oj.NewWorker(repository, executor, outputPublisher, workerConfig)
```

`ExpectedJudgeUID` is resolved once from the dedicated `ascendany-judge` OS
account. Startup fails if the socket directory is absent or traverses a
symlink. The caller owns the normal durable `oj.Worker` poll loop and lease.
