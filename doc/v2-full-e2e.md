# AscendAny v2 full E2E

`tools/run-v2-full-e2e.sh` is the single disposable acceptance path for the
reviewed server release and the first-party TypeScript applications. It starts
from a clean Git commit and exercises this control flow:

```text
reviewed commit
  -> production release builder
  -> externally anchored production installer in a user namespace
  -> installed migrate/admin/server/backup binaries
  -> PostgreSQL 17 direct endpoint + native PgBouncer HBA/transaction endpoint
  -> TypeScript Pintia exporter contract/build and committed sanitized fixture
  -> generated SDK byte replay, typed-domain duplicate, new snapshot sequence
  -> analytics/enrollment/student reads
  -> embedded site/web/admin HTTP assets + mobile/desktop build smoke
  -> backup create/verify/restore-verify
  -> complete source/restored table + sequence fingerprints
  -> artifact path + mode + size + digest equality
```

The release builder and installer own the canonical payload inventory. The E2E
runner consumes that inventory without copying a fixed path count. It verifies
every release and installed entry as a regular non-symlink file with the exact
manifest SHA-256, size, and mode, and rejects unmanifested installed entries.

## Local prerequisites

- Fedora 44 x86_64 host with rootless Podman
- bubblewrap, PostgreSQL client tools, zstd, jq, curl, OpenSSL, and iproute2
- Go 1.26 at an absolute canonical non-symlink path
- Node.js 22, pnpm 9.15.4, and a frozen workspace install
- the preloaded digest-pinned PostgreSQL image
  `docker.io/library/postgres@sha256:030da09481c3876b71a7e49738a932e1c18c398201a1e4ccfdbff1e5a541215b`
- the exact native PgBouncer package and binary identity in
  `deploy/v2/config/fedora-runtime-packages.json`

The runner derives its temporary PgBouncer configuration and HBA policy from
the installed release. Its mode-0400 userlist contains PostgreSQL-generated
SCRAM verifiers only. Production supplies the same userlist contract through an
encrypted systemd credential; the disposable runner keeps the verifier file in
its mode-0700 temporary tree and removes it on exit.
The PostgreSQL integration runner republishes the exact ordered admin/legacy/runtime
verifier set after every role-password reset using a same-directory temporary
file, fsync, mode `0400`, atomic rename, and directory fsync. It then issues
explicit PgBouncer `RELOAD` and database `RECONNECT` commands before any runtime
probe. `ASCENDANY_CI_PGBOUNCER_USERLIST_PATH` is required and must name a
canonical, caller-owned, mode-`0400` file inside a canonical caller-owned
mode-`0700` directory.

The checkout must be completely clean, including untracked files. The requested
commit must equal `HEAD`. The explicit destructive confirmation only authorizes
removal of this invocation's random label, pod, volume, network, credentials,
isolated `/opt` tree, and temporary work tree.

Use the guarded heavy-run wrapper on a workstation:

```bash
commit="$(git rev-parse HEAD)"
go_path="$(realpath -e -- "$(command -v go)")"

/home/kkkzbh/.agents/skills/guarded-heavy-run/scripts/guarded-run.sh \
  --mem-high 20G \
  --mem-max 22G \
  --swap-max 0 \
  --min-available 4G \
  --min-swap-free 0 \
  -- \
  ./tools/run-v2-full-e2e.sh \
    --confirm-reset run-disposable-ascendany-v2-full-e2e \
    --version 0.0.0-e2e.1 \
    --commit "${commit}" \
    --go-path "${go_path}"
```

The import flow retains three immutable provenance artifacts: the original
exporter snapshot, a byte-distinct snapshot with equal typed domain content,
and a new domain snapshot for the same logical exam. It proves same bytes map
to one job, equal typed content is superseded onto one snapshot, and new domain
content publishes snapshot sequence 2. All three artifacts enter backup and
are verified after restore.

The runner writes only bounded summary evidence to stdout. Secrets, database
fingerprints, server logs, and backup command logs stay in its mode-0700
temporary tree and are removed by the exit trap.

## Sandbox acceptance

Judge and LSP sandbox security has a separate fail-closed gate:

```bash
./tools/run-v2-sandbox-acceptance.sh \
  --confirm run-real-ascendany-v2-sandbox-acceptance \
  --clangd-sha256 REVIEWED_USR_BIN_CLANGD_SHA256
```

It requires the production digest-pinned judge image and a root-owned,
mode-0755 `/usr/bin/clangd` matching an externally reviewed SHA-256. `bwrap` is
a hard dependency of the real clangd sandbox test. Acquire, load, and attest the
release-bound linux/amd64 Judge leaf before running the gate:

```bash
work="$(mktemp -d)"
archive="${work}/judge-image.oci.tar"
archive_sha256="${work}/judge-image.oci.tar.sha256"

/usr/bin/bash deploy/v2/scripts/acquire-judge-image.sh \
  --output "${archive}" \
  --sha256-output "${archive_sha256}"
podman load <"${archive}"
/usr/bin/bash deploy/v2/scripts/attest-judge-image.sh
```

The acquisition command verifies the locked upstream Dockerfile, OCI index,
linux/amd64 leaf manifest, config digest, and toolchain identity. The sandbox
gate recompiles and lists each package first, so every required test name must
exist exactly once before test execution begins. The exact real Podman and real
clangd test manifest must then pass. A skipped test, missing or duplicate
required test, package failure, image/lock/config drift, missing image, failed
attestation, or clangd identity drift fails the command.

`.github/workflows/v2-full-e2e.yml` runs static RPM and entrypoint fixtures on an
Ubuntu runner. The weekly and on-demand release/business/restore path runs only
on a self-hosted runner labeled `ascendany-v2-full-e2e`; that runner must be the
documented Fedora 44 x86_64 rehearsal host, also labeled `fedora-44`, with the
exact native PgBouncer RPM.
The sandbox job acquires and attests the locked Judge leaf on a Linux x64
self-hosted runner, then reads the reviewed clangd digest from the repository
variable `ASCENDANY_SANDBOX_CLANGD_SHA256`. An absent variable fails closed.
