# AscendAny v2 production foundation

本目录定义 Go-only production runtime 的 reviewable foundation。所有文件仅用于审查和 rehearsal；仓库操作不会安装 unit、创建用户、写入 credential、修改 km6 服务或执行数据库变更。

v2 使用独立空数据库 `ascendany_v2`。旧数据库不向 v2 迁移数据，也不参与 row/hash parity。切换窗口只为旧数据库生成一次 read-only offline backup。

## 1. Ownership boundary

| Component | OS identity | PostgreSQL identity | Network | Durable writes |
| --- | --- | --- | --- | --- |
| `ascendanyd` | `ascendany` | `ascendanyd_login` through PgBouncer `127.0.0.1:6432` | HTTP only on `127.0.0.1:18000`; outbound model APIs allowed | Sole online DB writer; owns `/var/lib/ascendany/artifacts` |
| Judge instance | `ascendany-judge` | none | `PrivateNetwork=yes`; Unix control socket only | Per-job state under `/var/lib/ascendany-judge` |
| LSP instance | `ascendany-lsp` + `ascendany-lsp-control` | none | `RootDirectory=` minimal root, `PrivateNetwork=yes`; authenticated dedicated Unix control socket only | Ephemeral bounded workspace inside the instance-private `/tmp`; removed on disconnect or unit teardown |
| Recommendation trainer agent | `ascendany-trainer` | none | Authenticated HTTPS to one configured `ascendanyd` origin | Ephemeral staging under `/var/lib/ascendany-trainer/work`; each invocation is deleted |
| Migrator | `ascendany-migrator` | `ascendany_migrator_login`, then explicit `SET ROLE ascendany_owner` through controlled membership | Direct PostgreSQL on `127.0.0.1:5432` | Schema and migration history only |
| First-admin bootstrap | `ascendany` | `ascendanyd_login` through PgBouncer `127.0.0.1:6432` | Loopback PostgreSQL only | One serialized administrator row and its immutable audit event |
| Backup | `ascendany-backup` | `ascendany_backup_login` | Direct PostgreSQL on `127.0.0.1:5432` | Reads published `sha256` artifacts through the `ascendany` group; atomically publishes reader-group backup bundles |
| Restore verifier | `ascendany-restore` | `ascendany_restore_login`, then explicit `SET ROLE ascendany_owner` | Direct PostgreSQL on `127.0.0.1:5432` | One disposable scratch database, one disposable artifact tree, and root-published acceptance evidence |

The workstation Go trainer agent owns the scoped HTTPS credential, queue lease heartbeats, and bounded upload transport. Its Python child has no network, DB credential, or production filesystem mount. The child produces one immutable canonical output bundle containing model parameters and training diagnostics. The Go backend reloads the immutable input, validates every parameter and digest, recomputes held-out metrics and the publication quality gate, materializes student results, and owns atomic model/result publication.

### RTX trainer runtime

The RTX host selects one immutable, construction-addressed runtime through
`/opt/ascendany-trainer-runtime/current`. Published directories are named
`torch-2.13.0-cu130-<construction-sha256>`; multiple complete constructions
may coexist for atomic promotion and rollback. Build the runtime from the same
reviewed release while the trainer unit is stopped:

```bash
/opt/ascendany/v2/scripts/install-trainer-runtime.sh
```

The installer takes an exclusive host lock, removes only stale stages whose
root-owned stage marker proves their boot ID, PID, and path identity, and
captures the release manifest plus the hashed lock, exact 30-distribution
closure, reviewed wheel manifest, portable-Python source contract, installer,
portable-tree identity tool, and host-capability identity tool. It downloads
the official `uv 0.9.26` archive from the fixed GitHub release URL, requires
archive SHA-256
`30ccbf0a66dc8727a02b0e245c583ee970bdafecf3a443c1686e1b30ec4939e8`,
extracts its exact entry set, and requires binary SHA-256
`0650696de7f403348e9dd617e1f65dc32147c106c40129138017efd8f0f01cc8`.
The captured binary is part of runtime provenance; no host `uv` or host
CPython participates in the portable runtime.

The Python source contract selects CPython `3.14.6` from the 20260623
python-build-standalone release. The
`cpython-3.14.6+20260623-x86_64-unknown-linux-gnu-install_only_stripped.tar.gz`
asset is `36,049,152` bytes with SHA-256
`c172314f4a8ec137a8f605289010c3d19c8b56867d968f0095074cc68efa1d29`.
After installing that exact source, the installer validates and removes the
PBS seed packaging files. It proves a one-to-one relationship between the
hashed lock, normalized closure, and 30-entry
`runtime-wheels-cu130.json`. Every wheel is downloaded from its exact reviewed
HTTPS URL into a private wheelhouse and checked by filename and SHA-256.
Captured `uv pip sync` then runs offline inside a fresh
`--unshare-all` bubblewrap namespace with `--no-index`,
`--find-links /wheelhouse`, no `/usr`, no network, cleared environment, and
no credential mount. The wheelhouse and cache are deleted before publication.

The final portable tree contains only regular `python3.14` in `bin/` and
must exactly match the 30-item live `importlib.metadata` closure. Its identity
binds every path, mode, file byte, and internal symlink; descendant mounts,
cross-device entries, special nodes, hard-linked files, non-root ownership,
and group/world write permission fail directly. A CUDA probe in the same
`/usr`-free namespace records every mapped host library and
`/sys/module/nvidia/version`. The construction digest binds the release
manifest, all reviewed construction inputs, official uv artifact, portable
tree, and host capability identity.

Before publication, after publication, and after selector promotion, the
installer runs the production Python attestation in the exact training
namespace. The same attestation runs as `ExecStartPre=... verify-runtime` and
at the start of every training child before stdin is read. It verifies the
selector, marker, source package bytes, construction digest, tree, lock,
closure, wheel manifest, official uv binary, mapped libraries, kernel driver,
exact mount/device set, CPython `3.14.6`, torch `2.13.0+cu130`, CUDA
`13.0`, and a real CUDA tensor operation. It produces five lowercase digests:
runtime construction, marker provenance, portable tree, host capability, and
complete runtime attestation. The model manifest, immutable terminal receipt,
HTTP publication response, and write-once acceptance candidate all carry those
five values.

The production configuration fixes
`ASCENDANY_TRAINER_AGENT_RUNTIME_ROOT=/opt/ascendany-trainer-runtime/current`
and
`ASCENDANY_TRAINER_AGENT_PYTHON=/opt/ascendany-trainer-runtime/current/python/bin/python3.14`.
The read-only mount set is exactly
`/lib,/lib64,/opt/ascendany-trainer-runtime/current,/sys`, with only
`/dev/nvidia-uvm`, `/dev/nvidia0`, and `/dev/nvidiactl` exposed.
`CUBLAS_WORKSPACE_CONFIG=:4096:8`, `CUDA_VISIBLE_DEVICES=0`, and the three
thread counts fixed at `8` are the only compute configuration. The child runs
with `-B -s -P -c`, a cleared fixed environment, no network namespace, and no
host `/usr`. Input and output bundles are each capped at 128 MiB
(`134217728` bytes).

Publication uses a same-filesystem no-replace rename for a new construction
and one atomic relative-symlink replacement for `current`. A failed
post-switch check restores the exact previous selector. Reuse is permitted only
for an already-published directory whose construction digest, marker, tree,
host capability, and full same-process attestation exactly match the current
reviewed release.

## 2. Release binary and configuration contract

The units reference binaries produced by the Go commands under `backend/cmd`.
A release is eligible for installation after all paths are staged, root-owned,
non-writable by service users, and built from the same reviewed commit. The
closed release contains eight Go binaries, the seven-file isolated trainer package,
the public contracts, all non-secret production configuration, base systemd
units, restore operator scripts, polkit rules, sysusers/tmpfiles definitions,
and both validators:

```text
/opt/ascendany/v2/bin/ascendanyd
/opt/ascendany/v2/bin/ascendany-admin-bootstrap
/opt/ascendany/v2/bin/ascendany-judge
/opt/ascendany/v2/bin/ascendany-lsp
/opt/ascendany/v2/bin/ascendany-migrate
/opt/ascendany/v2/bin/ascendany-backup
/opt/ascendany/v2/bin/ascendany-release-ops
/opt/ascendany/v2/bin/ascendany-trainer-agent
/opt/ascendany/v2/trainers/recommendation/ascendany_recommendation_trainer/
/opt/ascendany/v2/config/
/opt/ascendany/v2/config/ascendanyd-read-only-smoke.env
/opt/ascendany/v2/db/roles/
/opt/ascendany/v2/systemd/
/opt/ascendany/v2/systemd/ascendanyd.service.d/40-read-only-smoke.conf
/opt/ascendany/v2/polkit-1/rules.d/
/opt/ascendany/v2/sysusers.d/
/opt/ascendany/v2/tmpfiles.d/
/opt/ascendany/v2/scripts/
```

Create the production payload as root from one explicit reviewed commit in a
root-owned protected checkout, using a root-owned protected output parent. This
ownership is the explicit bridge to the installer, which accepts only a
root-owned staging tree. A non-root release-user build remains valid for local
rehearsal and review, but that output cannot be passed to the privileged
installer. Do not repair a rehearsal tree with recursive ownership changes;
rebuild the production payload under the production ownership boundary.

```bash
commit="$(git rev-parse --verify 'HEAD^{commit}')"
go_path="$(realpath "$(command -v go)")"
go_version="$(GOTOOLCHAIN=local GOENV=off "$go_path" env GOVERSION)"
tools/build-v2-release.sh \
  --version 0.1.0 \
  --commit "$commit" \
  --source-date-epoch "$(git show -s --format=%ct "$commit")" \
  --go-path "$go_path" \
  --go-version "$go_version" \
  --goos linux \
  --goarch amd64 \
  --goamd64 v1 \
  --output /absolute/staging/ascendany-v2
```

Before the completed production payload leaves the protected build boundary,
record its manifest SHA-256 in a separate root-only trust-anchor file. Keep that
file outside the payload and transfer it through the operator-controlled channel.
Do not derive the expected digest from the staged source during installation.

```bash
manifest_sha256="$(sha256sum /absolute/staging/ascendany-v2/release-manifest.json | awk '{print $1}')"
anchor=/root/ascendany-v2-0.1.0.manifest.sha256
(umask 077; set -o noclobber; printf '%s\n' "$manifest_sha256" >"$anchor")
chmod 0400 "$anchor"
sync "$anchor"
```

The canonical destination is one-shot. `/opt/ascendany/v2` must not exist. Run
the trusted bootstrap from the protected reviewed checkout, external to the
release payload, and pass the independently retained manifest digest:

```bash
/absolute/root-owned-reviewed-checkout/deploy/v2/scripts/install-v2-release.sh \
  --source /absolute/staging/ascendany-v2 \
  --manifest-sha256 "$(</root/ascendany-v2-0.1.0.manifest.sha256)"
```

The external bootstrap verifies its protected root-owned ancestry and binds the
captured manifest to the operator-provided digest before interpreting any
payload contract. It verifies root ownership, exact file and directory closure,
modes, sizes, hashes, single-link files, every descendant mountpoint, one
filesystem, and an unchanged source inode through the copy. It builds a private
sibling stage under `/opt/ascendany`, fsyncs every file and directory, and
revalidates through anchored descriptors. The externally anchored 77-path
manifest includes `ascendany-release-ops`; the bootstrap executes that verified
binary through its open descriptor. The native helper publishes through the
anchored parent directory descriptor with Linux `renameat2(RENAME_NOREPLACE)`
and fails if that syscall is unavailable. It does not update, merge, or replace
an existing canonical tree. Any existing or racing `/opt/ascendany/v2` fails
directly. A failed pre-publication stage remains private for explicit forensic
inspection and operator removal; failure handling never performs a pathname-
based recursive delete. A post-publication verification failure likewise leaves
the no-replace canonical inode intact and requires explicit operator inspection;
the installer never attempts an identity-check-to-delete rollback.

Failure diagnostics expose the state machine explicitly.
`pre-commit-staging-retained` reports the anchored parent device/inode and private stage basename;
the next run rejects every pre-existing `.v2.installing.*` entry until the
operator resolves that exact retained object. `committed-unverified` reports the
canonical target and its verified staging identity after rename; the target must
be inspected against `release-manifest.json` and the external digest before the
operator either accepts it or removes it during an exclusive maintenance window.
`verified` is the only state that prints `/opt/ascendany/v2` to standard output
and exits successfully.

The invoking root operator, kernel, `/usr/bin/bash`, and the dynamic-loader state
that exists before the bootstrap's clean re-exec form the deployment trust
boundary. After that re-exec, the bootstrap admits only its fixed environment
closure; the native helper independently rejects every undeclared inherited
variable before publication.

The builder must be executed through its absolute `/usr/bin/bash -p` shebang.
Before any Go build child starts, the live builder must be the canonical
repository `tools/build-v2-release.sh`, be root/release-user owned mode `0755`,
and have protected non-symlink ancestry. The repository and requested object ID
must use SHA-1 with one canonical 40-lowercase-hex identity. The builder captures
the raw commit payload, writes it into a private isolated SHA-1 object store,
and requires its recomputed object ID to equal `--commit`. It takes the root
tree only from that verified payload, enumerates the raw tree, and materializes
each `100644` or `100755` blob into a private mode-0700 source directory. Every
materialized file is rehashed with `hash-object --no-filters` in the isolated
store. A NUL-delimited temporary index reconstructs all file paths and modes;
`write-tree` must reproduce the verified root tree exactly. Only after this
closed provenance check does the builder require the live fixed-path builder to
match the reconstructed commit's unique mode-`100755` blob byte for byte.
Symlinks, gitlinks, special modes, unsafe paths, caller Git configuration,
global/info attributes, and every `export-ignore` rule have no authority over
the materialized tree. Missing promisor objects fail locally because lazy Git
fetching and interactive authentication are disabled. Live worktree changes during the build cannot enter the
release. Caller `BASH_ENV`, exported functions, and `GO*` variables are removed
before any child tool runs. `GOTOOLCHAIN=local` prevents an implicit toolchain
download; the requested `go env GOVERSION` must match the installed compiler
exactly. The builder fixes child `PATH` to `/usr/bin:/bin`; Git, jq, and core
release tools therefore come from the Fedora system path. `--go-path` must name
one canonical executable owned by root or the release user with no group/other
write permission. The builder sets `GOPROXY=off` and builds with
`-mod=readonly`; the reviewed `go.mod`/`go.sum`
 graph must already exist in the default module cache under the release user's
 trusted canonical `HOME`. The explicit Go binary and system binaries remain
 controlled release-host/toolchain inputs; the builder does not attest their
 binary hashes. `HOME`, the explicit Go tool, and the output parent must have a
 fully protected canonical ancestry: every directory is owned by root or the
 release user and has no group/other write bit; a writable ancestor is accepted
 only when it is root-owned and sticky (for example `/tmp`). The builder pins
 the output parent's device/inode before creating its workspace, keeps an
 anchored descriptor for workspace cleanup and publication, revalidates the
 named parent after child-tool execution and immediately before and after
 publication, then rechecks the published directory owner, mode, closed path
 set, hard-link count, size, and SHA-256 values. Release files and directories
 are synchronized before rename; the source and destination parent directories
 are synchronized after rename. A final digest verification runs against the
 published inode before the builder reports success. The
 manifest records the fixed 77 payload paths,
each byte digest, mode, size, canonical semantic version capped at 128 ASCII
bytes, Git commit, source
date, exact Go version, `CGO_ENABLED=0`, and the `linux/amd64` target.
It also records `GOAMD64=v1`, the toolchain's effective experiment set, and
`GOFIPS140=off`.
Publication is one same-filesystem no-replace rename; an existing
or racing target makes the build fail and never receives a nested release tree.
If the named output parent is replaced, cleanup remains anchored to the original
directory inode and removes the private workspace there. A failed post-rename
verification removes only the published directory whose inode came from the
verified staging tree.
Installation preserves the manifest's root-owned modes and bytes.
`validate-production.sh` rejects symlinks, unmanifested files,
service-writable paths, digest drift, unreviewed installed unit/policy/config
bytes, and an active `/version` response whose commit, version, build time,
toolchain, OS, or architecture differs from the manifest.

The deterministic builder fixture uses a synthetic Git repository and fake Go
compiler to exercise privileged entry, shell/environment isolation, raw commit
materialization under hostile Git attributes/configuration, CRLF content,
binary NUL bytes, preserved final newlines, executable modes, and path names
containing tabs/newlines. It also covers isolated commit/blob identity checks,
reconstructed-root equality, corrupted-object refusal, fixed-path/mode/blob
builder self-provenance, dirty live builder and
live-symlink rejection, the 77-path closed set, exact toolchain and target selection, offline
read-only dependency resolution, worktree `PATH` isolation, canonical SemVer,
unsafe path ancestry, anchored cleanup after output-parent device/inode replacement, unsafe parent
modes, and a target-creation race:

```bash
tools/tests/build-v2-release-fixture.sh
tools/tests/install-v2-release-fixture.sh
tools/tests/systemd-installed-unit-closure-fixture.sh
tools/tests/trainer-runtime-provenance-fixture.sh
tools/tests/trainer-runtime-tree-identity-fixture.sh
```

Required behavior:

- `ascendanyd serve` reads non-secret values from `/etc/ascendany/v2/ascendanyd.env`. Core secrets use `ASCENDANY_DATABASE_PASSWORD_FILE`, `ASCENDANY_JWT_SIGNING_KEY_FILE`, and `ASCENDANY_PASSWORD_PEPPER_FILE`. Each feedback webhook credential uses a generated `ASCENDANY_CREDENTIAL_FILE_REF_HEX_<REF>_AUTHORITY_HEX_<AUTHORITY>` path variable that binds the credential reference to one canonical HTTPS authority.
- The reviewed production environment fixes `ASCENDANY_HTTP_LISTEN=127.0.0.1:18000` and `ASCENDANY_WRITE_MODE=enabled`. Staged and smoke phases install the release-owned `40-read-only-smoke.conf`; it resets the unit's `EnvironmentFile=` list and loads the production file followed by the release-owned `ascendanyd-read-only-smoke.env`, whose only effective assignment is `ASCENDANY_WRITE_MODE=disabled`. systemd gives the later environment file deterministic precedence. Disabled mode does not construct the artifact/import/analytics/feedback/chat/OJ/LSP/trainer write runtimes, and every HTTP mutation fails with `503 writes_disabled`.
- `ascendanyd` reads the complete closed-shape analytics algorithm configuration from `/etc/ascendany/v2/analytics.json`; startup rejects omitted fields, unknown fields, unsupported algorithm versions, or a config hash mismatch in queued work.
- HTTP request bodies use route-specific read deadlines backed by connection read deadlines. SSE connections have a global slot cap, periodic authorization checks, a maximum lifetime, bounded writes, and immediate cancellation during service shutdown.
- `ascendany-migrate up` reads `/etc/ascendany/v2/migrate.env` and `ASCENDANY_DATABASE_PASSWORD_FILE`, then explicitly `SET ROLE ascendany_owner`. Migration SQL and expected hashes are embedded in the binary. It acquires an advisory lock, uses transactional DDL, records immutable hashes, and rejects drift. Every created schema object must be owned by `ascendany_owner`.
- `ascendany-backup create` reads `/etc/ascendany/v2/backup.env` and `ASCENDANY_DATABASE_PASSWORD_FILE`. It opens one read-only repeatable-read transaction, exports its PostgreSQL snapshot to `pg_dump --format=custom --snapshot`, reads the ordered artifact and migration manifests from that same snapshot, checks every immutable artifact byte, and atomically renames one complete bundle containing `database.dump`, `artifacts.tar.zst`, `manifest.json`, and `manifest.sha256`. The staging tree remains private mode `0700` with mode-`0600` files while bytes are incomplete; publication changes the closed four-file tree to `ascendany-backup:ascendany-backup-readers` mode `0750`/`0640` before the atomic rename. Daily/ISO-week retention runs only after publication and refuses malformed bundles.
- `ascendany-backup verify <backup-id>` needs no credential. It rejects extra bundle entries, manifest drift, payload mode/size/hash drift, an invalid custom dump, and any tar entry that differs from the exact database artifact manifest.
- `ascendany-restore-verify@<backup-id>.service` is the sole production restore-verification operator. Its dedicated OS and PostgreSQL identities create a fixed template0 scratch database, run the release-bound `ascendany-backup restore-verify`, bind its one-line JSON result to the selected backup manifest and installed release manifest, drop the database, remove all scratch artifact and credential paths, and publish evidence atomically as root. `pg_restore` uses `--single-transaction --exit-on-error --role=ascendany_owner`; the scheduled backup identity remains read-only.
- The production database is owned by the isolated `ascendany_database_owner` capability. It has no login, membership edge, database password, or cluster privilege. `ascendany_owner` owns only the application schema and its objects. The restore login can explicitly assume `ascendany_owner` for one scratch database, while neither the restore nor migrator path can assume the production database owner or drop `ascendany_v2` from a maintenance connection.
- The operator publishes the successful JSON log line as root-owned
  `/var/lib/ascendany-acceptance/restore-verify.json`. Production validation requires
  mode `0600` inside a root-owned mode-`0700` acceptance directory. Production
  validation requires it to bind the active release and an existing schema-v5
  bundle and be no older than 31 days. It also runs a fresh credential-free
  `verify` against the newest published bundle, requires a future timer elapse,
  and binds that bundle to a successful completed backup-service execution.
- The artifact root and published `sha256` tree are mode `0750`, and immutable artifact files are mode `0640`. `ascendanyd` retains `UMask=0077`; the artifact store creates new paths privately, then uses no-follow file descriptors to set and verify the exact published modes. Pre-existing mode drift fails startup or reconciliation and is never silently repaired. The backup user is a member of the `ascendany` group. `incoming` and `.locks` stay mode `0700`, so backup can read committed artifacts without seeing uploads or lock state.
- Every database URL omits the password. Empty or unreadable required credential files stop startup.
- Judge accepts one canonical UUID instance id, creates one per-job Unix socket under `/run/ascendany-judge`, and verifies the `ascendanyd` peer UID. Its strict streaming protocol and test-bundle format are defined in `OJ_JUDGE_CONTRACT.md`. LSP uses the authenticated, bounded Unix control protocol defined in `LSP_CONTROL_CONTRACT.md`. Neither worker has a DB client or network fallback.
- `ascendany-trainer-agent run` reads `/etc/ascendany/v2/trainer-agent.env` and `ASCENDANY_TRAINER_AGENT_TOKEN_FILE`. It accepts one canonical HTTPS origin, does not use process proxy variables or redirects, and carries versioned canonical claim, heartbeat, output, and failure documents. The first successful terminal output for a release atomically seals the canonical write-once candidate configured by `ASCENDANY_TRAINER_AGENT_ACCEPTANCE_CANDIDATE_PATH`; later training runs cannot replace it. The token never enters the Python child. The agent has no PostgreSQL configuration and cannot access `/var/lib/ascendany/artifacts`.
- The trainer endpoint is fixed to `https://ascendany-trainer.kkkzbh.cn`. The release-owned locally managed Tunnel routes only `^/version$` and `^/api/v2/internal/recommendation/trainer-agent/claims(/.*)?$` on that hostname to `http://127.0.0.1:18000`, followed by a same-host `http_status:404` rule. The shadow hostname reaches the same v2 origin; the public hostname stays on the old Tunnel until the final DNS overwrite.
- The internal trainer transport, fencing rules, terminal semantics, and acceptance evidence are fixed in `TRAINER_AGENT_CONTRACT.md`.

The builder maps the reviewed non-secret `config/*.example` sources into the
release as `config/*.env` and `config/analytics.json`, and copies the fixed
judge-image lock, Fedora runtime package lock, locally managed Tunnel ingress,
PgBouncer config/HBA, and PostgreSQL HBA/pg_ident files directly. Production installs
the application configuration bytes under `/etc/ascendany/v2` and the two pool
files under `/opt/ascendany/infra/pgbouncer`; a configuration change requires a
reviewed commit and a new release. There is no TOML or release-external
migration-directory contract.
The builder also publishes the read-only smoke drop-in as an immutable release
input. Operators install its exact bytes for staged/smoke validation and remove
that one installed file at the write-activation commit point.

## 3. systemd credentials

Secrets live under `/etc/ascendany/credentials/` as root-readable encrypted credential files. Units use `LoadCredentialEncrypted=` and expose decrypted values through the private `%d` credential directory. Service code receives file paths. Unit environment, env files, and release files contain no plaintext secret. Every encrypted source is a real single-link `root:root` mode `0400` file reached only through root-owned, non-writable, non-symlink directories. Environment files are real root-owned files with no group/other write bit and the same protected ancestry.

| Unit | Credential id | Encrypted source |
| --- | --- | --- |
| `ascendanyd.service` | `db_password` | `runtime_db_password.cred` |
| `ascendanyd.service` | `jwt_signing_key` | `jwt_signing_key.cred` |
| `ascendanyd.service` | `password_pepper` | `password_pepper.cred` |
| `ascendanyd.service` | `trainer_agent_rtx_01` | `trainer_agent_rtx_01.cred` |
| `ascendany-admin-bootstrap.service` | `db_password` | `runtime_db_password.cred` |
| `ascendany-admin-bootstrap.service` | `password_pepper` | `password_pepper.cred` |
| `ascendany-admin-bootstrap.service` | `admin_password` | `/run/ascendany-admin-bootstrap-input/admin_password.cred` (one-time) |
| `/etc/systemd/system/ascendanyd.service.d/50-feedback-credentials.conf` | reviewed feedback credential id | `/etc/ascendany/credentials/<id>.cred` |
| `ascendany-migrate.service` | `migrator_db_password` | `migrator_db_password.cred` |
| `ascendany-backup.service` | `backup_db_password` | `backup_db_password.cred` |
| `ascendany-restore-verify@.service` | `restore_db_password` | `restore_db_password.cred` |
| `ascendany-trainer-agent.service` | `trainer_agent_token` | `trainer_agent_token.cred` |
| `ascendany-pgbouncer.service` | `pgbouncer_userlist` | `pgbouncer_userlist.cred` |
| `ascendany-cloudflared.service` | `tunnel_credentials` | `cloudflare_tunnel_credentials.cred` |

The database and PgBouncer boundary has one release-owned provisioning entrypoint.
Before invoking it, the operator secret process writes four pairwise-distinct,
newline-free 16..128-byte passwords to this fixed volatile input tree:

```text
/run/ascendany-v2-provision/                         root:root 0700
/run/ascendany-v2-provision/runtime_db_password      root:root 0600
/run/ascendany-v2-provision/migrator_db_password     root:root 0600
/run/ascendany-v2-provision/backup_db_password       root:root 0600
/run/ascendany-v2-provision/restore_db_password      root:root 0600
```

The old API on port `8000`, PostgreSQL 17 container, and old PgBouncer container
must be running. Install and attest the release-locked Fedora 44
`pgbouncer-1.25.2-1.fc44.x86_64` RPM, mask the package-owned
`pgbouncer.service`, and install the reviewed inactive
`ascendany-pgbouncer.service` unit. The production PostgreSQL container
attachment is fixed to the rootful
`podman` bridge `10.88.0.0/16`, gateway `10.88.0.1`, and container address
`10.88.0.2`. The four direct-5432 systemd units admit both localhost and
`10.88.0.2/32`: the latter is the PostgreSQL reply source visible to systemd's
cgroup IP filter before Podman reverse NAT. Provisioning and every production
validation phase reject drift in the bridge or container attachment.
Run the fixed confirmation contract from the installed release:

```bash
/opt/ascendany/v2/scripts/provision-postgres-pgbouncer.sh \
  --confirm-fresh-database ascendany_v2 \
  --confirm-legacy-connect-closure AscendAny \
  --confirm-pgbouncer-replacement ascendany-pgbouncer
```

The provisioner renames PostgreSQL bootstrap OID 10 to the passwordless,
peer-only `ascendany_cluster_admin`, creates a new non-superuser `AscendAny`
login with the existing legacy SCRAM verifier, and retains only the two legacy
task-table ownership exceptions required by the running old API. It creates the
fresh database and nine v2 capability roles, assigns four distinct SCRAM
passwords, encrypts all database credentials plus the two-record PgBouncer
userlist, and atomically publishes the PostgreSQL HBA, pg_ident, pool config,
credential, and native DynamicUser service boundaries. A durable root-only
journal fences every phase. Failures before the role split restore the entry
state; failures after the split preserve the hardened role boundary and recover
the legacy service while the provisioner rolls forward. A committed run enables
the native pool, removes the retired container and plaintext userlist, consumes
the volatile passwords, and deletes the completed journal.

Provision the remaining service credentials directly with `systemd-creds`.
Example:

```bash
systemd-ask-password -n 'AscendAny JWT signing key' \
  | systemd-creds encrypt --name=jwt_signing_key - \
      /etc/ascendany/credentials/jwt_signing_key.cred
systemd-ask-password -n 'AscendAny password pepper' \
  | systemd-creds encrypt --name=password_pepper - \
      /etc/ascendany/credentials/password_pepper.cred
chmod 0400 /etc/ascendany/credentials/*.cred
```

The km6 `trainer_agent_rtx_01` credential and the RTX
`trainer_agent_token` credential contain the same scoped random token bytes.
Generate that value once in the operator secret process, present it directly to
`systemd-creds encrypt --name=trainer_agent_rtx_01` on km6 and to
`systemd-creds encrypt --name=trainer_agent_token` on RTX, then erase the
operator copy. Each host performs its own encryption because system credential
sealing is machine-bound. The plaintext never enters a repository, shell
argument, environment variable, or deployment log. The different credential
IDs preserve each service's ownership boundary while the shared bytes let km6
authenticate only the `rtx-01` agent.

Create the initial administrator password only after staged validation has
passed. The plaintext travels directly from the password agent to
`systemd-creds`; `-n` prevents a newline from becoming part of the password:

```bash
systemd-ask-password -n 'AscendAny initial administrator password' \
  | systemd-creds encrypt --name=admin_password - \
      /run/ascendany-admin-bootstrap-input/admin_password.cred
chmod 0400 /run/ascendany-admin-bootstrap-input/admin_password.cred
systemctl start ascendany-admin-bootstrap.service
test ! -e /run/ascendany-admin-bootstrap-input/admin_password.cred
```

The unit is static and manually started exactly once. Its root
`ExecStopPost` removes the encrypted one-time input on success and failure;
its systemd `EnvironmentFile=-` declaration exists only to keep that cleanup
path reachable if the required configuration disappears after credential
provisioning. A root `ExecStartPre` still requires the exact file, and the
validator requires the installed configuration before any bootstrap attempt.
the database transaction creates `admin` plus one `auth.admin_bootstrap`
audit event atomically. Smoke validation requires that exact active account
and audit pair. Production permits later administrators while retaining the
canonical active bootstrap account and sole bootstrap audit.

Use each credential id as the `--name` value. Preserve an offline recovery copy through the operator's secret-management process. Backup bundles exclude `/etc/ascendany/credentials`.

Feedback delivery accepts only schema `ascendany.feedback_delivery.webhook.v1` with document `{ "url": "https://host/path", "timeoutMilliseconds": 5000 }`. `credentialRef` is required. Redirects, URL credentials, query strings, fragments, plaintext HTTP, process proxy variables, and unbound destination credentials are rejected. Derive the path-variable suffix with uppercase bytewise hex; implicit HTTPS port is canonicalized to `443`. For reference `feedback.webhook.primary` and endpoint authority `feedback.example.com:443`, the exact variable is:

```text
ASCENDANY_CREDENTIAL_FILE_REF_HEX_666565646261636B2E776562686F6F6B2E7072696D617279_AUTHORITY_HEX_666565646261636B2E6578616D706C652E636F6D3A343433
```

Install the single reviewed drop-in at
`/etc/systemd/system/ascendanyd.service.d/50-feedback-credentials.conf`.
Its canonical bytes start with `[Service]`, followed by one adjacent
`LoadCredentialEncrypted=<id>:/etc/ascendany/credentials/<id>.cred` and
`Environment=<exact-variable>=%d/<id>` pair for every approved
`(credentialRef, authority)`. Sort the pairs bytewise by the complete
`<exact-variable>=<id>` binding. Comments, blank lines, reset directives,
additional directives, alternate paths, hard links, and a second feedback
credential drop-in are rejected. The base unit provisions no feedback
destination credential.

Production validation treats the effective encrypted credential IDs and
environment bindings as closed sets. Pass the exact whitespace-separated
variable-to-ID bindings used to generate the fixed drop-in:

```bash
ASCENDANY_VALIDATION_PHASE=production \
ASCENDANY_EXPECTED_RUNTIME_FEEDBACK_CREDENTIAL_BINDINGS='ASCENDANY_CREDENTIAL_FILE_REF_HEX_666565646261636B2E776562686F6F6B2E7072696D617279_AUTHORITY_HEX_666565646261636B2E6578616D706C652E636F6D3A343433=feedback_primary' \
  deploy/v2/scripts/validate-production.sh
```

Variable hex segments must use non-empty uppercase even-length bytewise hex.
Every credential ID is unique, and deployment database, JWT, password-pepper,
or trainer-token credential IDs cannot appear in a feedback binding. Staged
and smoke phases allow exactly the release-owned
`40-read-only-smoke.conf` plus the optional canonical
`50-feedback-credentials.conf`. Production forbids the smoke drop-in; with no
feedback bindings it allows no unit-specific drop-in. Fedora's package-owned
`/usr/lib/systemd/system/service.d/10-timeout-abort.conf` is the sole global
service drop-in allowed for all services. Its canonical root-owned file
may contain comments and must have exactly the effective
`[Service]`/`TimeoutStopFailureMode=abort` directive; the validator also checks
the resulting effective property. The backup timer allows no drop-in. The
validator rejects every effective plaintext `LoadCredential=`, an
undeclared encrypted ID, a duplicate ID, a linked or service-writable
credential source, and an optional `EnvironmentFile=`.

The km6 system manager global environment is a closed production contract:
`LANG=zh_CN.UTF-8` and `PATH=/usr/local/bin:/usr/bin` are the complete set.
`systemctl show-environment` must match both names and values exactly;
`DATABASE_URL`, `LD_PRELOAD`, proxy variables, language tool options, and every
other inherited manager variable block acceptance. Variables synthesized by
systemd for one service process do not belong to this manager-global block.
Active `ascendanyd` validation reads `/proc/<MainPID>/environ` separately and
requires the exact reviewed environment-file and base-unit values together
with the phase-owned write-mode override when the smoke drop-in is installed,
with exact identity values and the value-validated systemd 259 variables
`INVOCATION_ID`, `JOURNAL_STREAM`, `SYSTEMD_EXEC_PID`,
`MEMORY_PRESSURE_WATCH`, `MEMORY_PRESSURE_WRITE`, `CREDENTIALS_DIRECTORY`,
`RUNTIME_DIRECTORY`, `STATE_DIRECTORY`, and `LOGS_DIRECTORY`. An undeclared
process variable blocks acceptance even after the manager environment has
subsequently been cleaned.
The base unit pins `StandardOutput=journal`, `StandardError=journal`,
`MemoryPressureWatch=yes`, and `MemoryPressureThresholdSec=200ms`; validation
requires their effective properties to match. This binds `JOURNAL_STREAM` and
the PSI environment values to reviewed service configuration.

## 4. Installation sequence

Run only after rehearsal and explicit approval:

1. As root, run the production builder from the protected reviewed checkout
   into a root-owned protected staging parent, complete every builder and
   77-path manifest gate, record the manifest SHA-256 outside the payload, then
   execute the trusted external `deploy/v2/scripts/install-v2-release.sh` from
   the protected reviewed checkout with that explicit digest. Confirm
   `/opt/ascendany/v2` is the exact immutable manifest tree before installing
   any host configuration. The canonical destination must be absent before
   this one-shot promotion.
2. Install `/opt/ascendany/v2/sysusers.d/ascendany-v2.conf` byte-for-byte as
   `/etc/sysusers.d/ascendany-v2.conf`, then run `systemd-sysusers`.
3. Allocate non-overlapping `/etc/subuid` and `/etc/subgid` ranges for `ascendany-judge`; prove rootless Podman is active.
4. Install `/opt/ascendany/v2/tmpfiles.d/ascendany-v2.conf` byte-for-byte as
   `/etc/tmpfiles.d/ascendany-v2.conf`, then run `systemd-tmpfiles --create`.
5. Install the release copies of the base systemd units under
   `/etc/systemd/system`, the two polkit rules under `/etc/polkit-1/rules.d`,
   and the applicable release `config/` files under `/etc/ascendany/v2` without
   editing their bytes, including `ascendanyd-read-only-smoke.env`. Install the exact release-owned
   `systemd/ascendanyd.service.d/40-read-only-smoke.conf` under
   `/etc/systemd/system/ascendanyd.service.d/`. On a networked acquisition host,
   run `scripts/acquire-judge-image.sh --output /absolute/gcc.oci.tar
   --sha256-output /absolute/gcc.oci.tar.sha256`. Transfer both immutable files
   to km6, then run `scripts/preload-judge-image.sh --archive
   /absolute/gcc.oci.tar --archive-sha256 /absolute/gcc.oci.tar.sha256
   --target-user ascendany-judge` as root. This verifies the reviewed upstream
   Dockerfile, OCI index, linux/amd64 leaf and config identities before loading,
   and attests `/usr/local/bin/g++` version `15.2.0` from the offline store.
6. Acquire and offline-attest the exact PgBouncer RPM with
   `scripts/acquire-pgbouncer-rpm.sh` and `scripts/attest-pgbouncer-rpm.sh`,
   install it, mask the package-owned `pgbouncer.service`, and verify the
   installed NEVRA and `/usr/bin/pgbouncer` against
   `config/fedora-runtime-packages.json`. Install Cloudflared
   `2026.7.1-1.x86_64`, then verify its installed NEVRA, binary size, mode, and
   SHA-256 against the same lock. Keep both release-owned units inactive until
   their encrypted credentials are present.
7. Stage the four database password inputs under
   `/run/ascendany-v2-provision`, then run the installed
   `scripts/provision-postgres-pgbouncer.sh` with its three exact confirmation
   arguments. Require its `PASS [committed]` terminal result and prove the
   volatile input directory no longer exists. Do not create the v2 database,
   roles, encrypted database credentials, or pool configuration through any
   other path.
8. Encrypt the JWT signing key, password pepper, scoped trainer token, and the
   new locally managed Tunnel credential. Install the closed
   `config/cloudflared.yaml`, enable and start `ascendany-cloudflared.service`,
   and prove `ascendany-v2.kkkzbh.cn` plus
   `ascendany-trainer.kkkzbh.cn` route to Tunnel
   `e448a34c-9274-4c9d-8c69-e1a7fa369e52`. Leave
   `ascendany.kkkzbh.cn` on the old Tunnel. Run `systemctl daemon-reload`.
   Staged validation requires
   `ascendanyd.service` and the backup timer to be disabled and inactive;
   validation rejects `NeedDaemonReload=yes`.
9. Run `ascendany-migrate.service` manually against an isolated fresh rehearsal
   database, then against the empty production `ascendany_v2` database. After
   each migration, apply `/opt/ascendany/v2/db/roles/001_v2_roles.sql` a second
   time and then run `/opt/ascendany/v2/db/roles/verify_v2_roles.sql`. The second
   closure pass removes concrete ACL drift on migration-created table row types;
   any schema, owner, membership, database-owner, or grant finding blocks the
   next step.
10. Rehearse backup creation and destructive restore verification against the isolated rehearsal database. Retain rehearsal logs outside the live production evidence paths. The production backup, restore evidence, and timer gate run only after the write-activation commit point.
11. On the RTX workstation, materialize the reviewed commit as the canonical
   root-owned mode-`0755` protected checkout at `/opt/ascendany/Release`, build
   and promote the exact release through the same production builder and
   manifest-bound installer boundary, and retain that checkout while the trainer
   unit is installed. The unit asserts that exact directory before creating its
   namespace and makes it inaccessible to the service. Provision the scoped
   trainer-agent token, provision the pinned trainer runtime above, validate
   the NVIDIA device set, and run
   `ASCENDANY_TRAINER_VALIDATION_PHASE=staged`. This phase requires the unit to
   be disabled/inactive with empty work and acceptance roots and does not
   contact a remote endpoint. Exercise the agent separately against the
   isolated enabled rehearsal backend. Do not create or promote
   `trainer-latest.json` for production while km6 remains in staged or smoke
   phase; the disabled km6 runtime intentionally exposes no trainer transport.

## 5. Objective gates

Every gate retains its log and exits nonzero on failure.

### Static and runtime security

```bash
bash -n deploy/v2/scripts/validate-cloudflared.sh
bash -n deploy/v2/scripts/validate-production.sh
bash -n deploy/v2/scripts/validate-trainer-host.sh
bash -n deploy/v2/scripts/install-v2-release.sh
tools/tests/build-v2-release-fixture.sh
tools/tests/install-v2-release-fixture.sh
tools/tests/systemd-installed-unit-closure-fixture.sh
tools/tests/pgbouncer-rpm-contract-fixture.sh
tools/tests/provision-postgres-pgbouncer-fixture.sh
tools/tests/validate-cloudflared-fixture.sh
tools/tests/validate-production-unit-closure-fixture.sh
systemd-analyze verify --man=no deploy/v2/systemd/*.service deploy/v2/systemd/*.timer
systemd-analyze security --offline=yes deploy/v2/systemd/ascendanyd.service
```

Run production validation against an assembled release root containing all eight
Go binaries and the complete isolated trainer package. Unknown directive,
dependency, specifier, missing executable, ownership, or mode diagnostics block
release.

After units and encrypted credentials are installed, run
`/opt/ascendany/v2/scripts/validate-production.sh` as root with the required
`ASCENDANY_VALIDATION_PHASE` value:

- `staged` requires the smoke drop-in, an inactive and disabled
  `ascendanyd.service`, an inactive and disabled backup timer, and an explicit
  canonical root-owned mode-`0600` operator `PGPASSFILE`. It validates static
  release, unit, credential-source, role, artifact, and database boundaries;
  v2 port `18000` must be unused, while decrypted unit credentials, an HTTP
  listener, trainer evidence, and final backup evidence do not exist yet. The
  release-owned native Cloudflared unit must be enabled and active with its
  encrypted tunnel-scoped credential. The trainer catch-all returns 404 and the
  public hostname must return byte-identical legacy DB-backed metadata from
  `127.0.0.1:8000`, proving that production DNS has not moved early.
- `smoke` requires the smoke drop-in, a manually started but still disabled
  service on `127.0.0.1:18000`, and an inactive/disabled backup timer. It
  verifies exact `/livez`, schema-v5 `/readyz`, `/version`, and capability
  responses and requires `writesEnabled=false`. Trainer and final backup
  evidence remain out of scope. The shadow and trainer `/version` responses
  must equal loopback v2 bytes; the public hostname must still equal the
  legacy DB-backed loopback response throughout private smoke acceptance.
- `production` forbids the smoke drop-in, requires an enabled active service on
  `127.0.0.1:18000`, requires `writesEnabled=true`, and runs the full trainer
  receipt, backup, restore-evidence, and active timer gates. After the explicit
  DNS overwrite, its connector gate proves that public, shadow, and trainer
  `/version` bytes all reach the exact loopback v2 process and that the retired
  remotely managed connector has been removed.

Example staged invocation, using a temporary operator-owned password file under
the volatile runtime filesystem that contains only the `ascendanyd_login`
entry:

```bash
install -d -o root -g root -m 0700 /run/ascendany-validation
# The operator secret process writes one escaped
# 127.0.0.1:6432:ascendany_v2:ascendanyd_login:<password> line here.
test "$(stat -Lc '%U:%G:%a:%h' /run/ascendany-validation/runtime.pgpass)" = root:root:600:1
PGPASSFILE=/run/ascendany-validation/runtime.pgpass \
ASCENDANY_VALIDATION_PHASE=staged \
  /opt/ascendany/v2/scripts/validate-production.sh
rm -f -- /run/ascendany-validation/runtime.pgpass
rmdir -- /run/ascendany-validation
```

The staged `PGPASSFILE` must be a canonical root-owned mode-`0600` single-link
file with protected ancestry. Remove it through the operator secret process
after the staged gate; it is never copied into the release or acceptance paths.
Cloudflare validation reads the release config, package lock, effective native
unit, encrypted credential source, process sandbox, connector readiness, and
live HTTPS routes. It needs no account-wide API token.

The validator compares each production unit's exact fragment path, authorized
drop-in set, working directory, effective `ExecStart`/`ExecStartPre`, identity,
isolation values, environment, credential, environment-file and device-policy
sets. Active validation also resolves `/proc/<MainPID>/exe` and the
NUL-delimited argv for `ascendanyd.service` to the staged `ascendanyd serve`
command. It fails for unit or process drift, worker credentials or network
access, plaintext or undeclared credentials, unsafe credential/config
ancestry, a missing/short JWT key, runtime DB cluster privilege or owner
membership, deployed bytes that differ from the release, a trainer
receipt/artifact mismatch, an artifact root under the release, symbolic links
or special nodes in the published artifact tree, or an AscendAny-managed port
exposed outside loopback. Port `8000` remains managed only to constrain the old
rollback listener to loopback; v2 requires port `18000` in active phases.
Command and environment sequence validation parses the raw effective
`systemctl cat` directives, preserving `%d`, template `%i`, directive resets,
and declaration order. `systemctl show` remains authoritative for fragment,
drop-in, working-directory, identity, isolation, journal destination,
memory-pressure, and timer properties. The
active health, version, and capability probes put curl `--disable` first,
ignore process proxy configuration, and permit only plain HTTP to the fixed
loopback endpoint. The capability probe supplies the fixed
`CF-Connecting-IP: 127.0.0.1` identity required by the configured trusted
loopback proxy boundary; health and version remain transport-only probes.
`/livez` and `/readyz` must return HTTP 200 and their exact
closed JSON shapes; readiness binds PostgreSQL and the complete migration
manifest at schema version `5`.
The same gate closes the backup timer fragment and drop-in set, target service,
calendar, persistence, accuracy, and randomized delay.

Run `/opt/ascendany/v2/scripts/validate-trainer-host.sh` as root on the RTX workstation.
It verifies the trainer binary and Python entry point against the release
manifest, a dedicated OS identity with no runtime group membership, the exact
installed unit fragment with no drop-ins, exact effective startup commands and
active process executable/argv, encrypted credential, closed non-secret
environment, canonical HTTPS origin, the single closed root-owned runtime tree,
the exact portable CPython 3.14.6 and torch/CUDA build, a successful CUDA tensor operation,
one CUDA-visible GPU and matching `DeviceAllow` set, exact owner-mode state and
work/acceptance roots, at most one canonical active invocation tree,
bubblewrap/systemd isolation, and release-bound claim-heartbeat-upload evidence whose
bytes equal the agent-written candidate. Online validation allows
the active UUIDv4 staging directory and its exact `output/output.json` shape.
The three exact invocations are:

```bash
ASCENDANY_TRAINER_VALIDATION_PHASE=staged \
  /opt/ascendany/v2/scripts/validate-trainer-host.sh
ASCENDANY_TRAINER_VALIDATION_PHASE=production \
  /opt/ascendany/v2/scripts/validate-trainer-host.sh
ASCENDANY_TRAINER_VALIDATION_PHASE=quiesced \
  /opt/ascendany/v2/scripts/validate-trainer-host.sh
```

`staged` requires disabled/inactive/dead plus empty work/candidate/evidence.
`production` requires enabled/active/running plus byte-identical promoted
evidence. `quiesced` requires the unit to remain enabled while inactive/dead,
an empty work root, the same evidence, and the same remote route. Both
evidence-bearing phases require the dedicated origin's `/version` to match the
release and `/livez` to return `404`, proving that the Tunnel route exposes no
general application surface.

### Rootless judge

```bash
(
  cd /var/lib/ascendany-judge
  exec runuser -u ascendany-judge -- env -i \
    PATH=/usr/bin:/bin \
    LANG=C.UTF-8 \
    HOME=/var/lib/ascendany-judge \
    XDG_RUNTIME_DIR=/run/ascendany-judge-image-podman \
    XDG_DATA_HOME=/var/lib/ascendany-judge/.local/share \
    XDG_CONFIG_HOME=/var/lib/ascendany-judge/.config \
    XDG_CACHE_HOME=/var/lib/ascendany-judge/.cache \
    podman --cgroup-manager=cgroupfs \
      --runroot=/run/ascendany-judge-image-podman/containers \
      info --format '{{.Host.Security.Rootless}}'
)
probe_id="$(cat /proc/sys/kernel/random/uuid)"
if [[ ! "$probe_id" =~ ^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$ ]]; then
  printf 'kernel did not return a canonical UUIDv4\n' >&2
  exit 1
fi
probe_unit="ascendany-judge@${probe_id}.service"
trap 'systemctl stop "$probe_unit" 2>/dev/null || true; systemctl reset-failed "$probe_unit" 2>/dev/null || true' EXIT
systemctl start "$probe_unit"
systemctl show "$probe_unit" -p User -p PrivateNetwork -p NoNewPrivileges -p CapabilityBoundingSet -p AmbientCapabilities
systemctl stop "$probe_unit"
systemctl reset-failed "$probe_unit"
trap - EXIT
```

The Judge unit fixes Podman to `cgroupfs`, places the Go supervisor in the
`supervisor` delegation subgroup, and exposes a private writable cgroup
namespace. The reviewed tmpfiles contract creates the empty mode-`0700`
`/run/ascendany-judge-podman` parent. Each template instance owns and removes
exactly `/run/ascendany-judge-podman/<job-id>`, so a preloader or another job
cannot donate a stale Podman pause user/cgroup namespace. Image load and
attestation use the separate persistent mode-`0700`
`/run/ascendany-judge-image-podman` runtime while image layers remain in the
dedicated user's shared rootless store. The tmpfiles contract is also the sole
owner of the shared `/run/ascendany-judge` socket directory, which persists
between template instances. Its setgid mode `2770` makes each socket inherit
`ascendany-runtime` while the Judge keeps its own primary GID for one stable
rootless Podman identity.

Fresh rootless namespace construction requires full procfs visibility, a
writable `ping_group_range` inside the job's private user/network namespace,
and an unmasked proc-kmsg mount point. The outer identity remains unprivileged,
has a private network/cgroup namespace, and keeps the exact
`CAP_SETUID`/`CAP_SETGID` bounding set. The unit therefore fixes
`ProcSubset=all`, `ProtectKernelTunables=no`, and `ProtectKernelLogs=no` as
reviewed runtime capabilities. It isolates `/tmp` and `/var/tmp` with explicit
no-exec tmpfs mounts. `RemoveIPC=no` preserves Podman's SELinux-labelled shared
image-store lock; every inner container still runs with `--ipc=none`.

The corpus covers fork bombs, traversal, symlink escape, oversized output, timeout, memory and process exhaustion, compiler abuse, and network access. Each attack terminates inside its job boundary.

### Fresh database, backup, and plugin import

- `verify_v2_roles.sql` passes on `ascendany_v2`; bootstrap and migration fail on a non-fresh target or schema drift. Verification also rejects any relation, routine, or type in `ascendany` whose owner is not `ascendany_owner`, any production database owner other than isolated `ascendany_database_owner`, and every membership edge that could reach that database owner.
- Runtime integration uses `ascendanyd_login` through the release-locked native
  PgBouncer transaction pool on port `6432`. Acceptance binds the signed Fedora
  package and binary, exact release config/HBA bytes, encrypted two-record SCRAM
  userlist, DynamicUser process argv/isolation, final PostgreSQL HBA/pg_ident
  bytes, bootstrap role split, and both cross-database rejection directions.
  Migration and backup use direct PostgreSQL on port `5432`.
- A newly generated plugin v2 export imports successfully into an isolated migrated v2 database. Schema validation, idempotency behavior, rejected malformed exports, and imported result reads are covered.
- A v2 backup containing rehearsal data restores into an isolated PostgreSQL instance and scratch artifact root. Manifest hashes match and every published DB artifact reference resolves inside that root.
- Old database contents, identifiers, hashes, row counts, sequences, jobs, and auth/session rows have no v2 parity requirement.

### Auth and first-party artifacts

Retain a machine-readable local-auth acceptance matrix covering fresh admin bootstrap, imported student-number binding, exact Argon2id parameters, access-token issuer/audience/method validation, refresh-cookie and CSRF rotation, replay detection, session revocation, disabled accounts, role authorization, and logout. E2E uses fresh v2 accounts and proves wrong-password indistinguishability, rotated-token replay revocation, wrong CSRF, expiry, origin rejection, and cross-account rejection. It carries no old-row or legacy-hash requirement.

Generate TypeScript clients from the final OpenAPI document and reject endpoint drift. Build and test web, import console, Electron, Android, site, and the plugin v2 exporter from one commit. Verify signed update artifacts by actual byte size and SHA-512 before origin switch. Enumerate off-repo consumers and record a v2 readiness result for each.

## 6. Cutover and rollback

The release environment is the final enabled production configuration on
`127.0.0.1:18000`. The release-owned smoke drop-in is installed first and
deterministically overrides only `ASCENDANY_WRITE_MODE` to `disabled`.
The connector is the release-owned native `ascendany-cloudflared.service`.
`config/fedora-runtime-packages.json` pins Cloudflared
`2026.7.1-1.x86_64`, RPM SHA-256
`b9143a52ee388e330fb7300fa740de0c488415e777fb219af7ec9a070982f790`,
signing fingerprint `CC94B39C77AE7342A68B89628A682D308D4E5E73`, and the
exact `/usr/bin/cloudflared` byte identity. Production acceptance rejects the
retired Podman connector, mutable tags, package drift, and binary drift.

The release-owned `scripts/validate-cloudflared.sh` gate is read-only. It reads
the RPM lock, reviewed local ingress, installed unit, encrypted credential
source, effective systemd state, runtime `/proc` security state, connector
readiness, and live HTTPS routes. It performs no Cloudflare mutation. The exact
connector contract is:

- locally managed Tunnel `e448a34c-9274-4c9d-8c69-e1a7fa369e52` with one
  root-owned encrypted JSON credential containing exactly `AccountTag`,
  `Endpoint`, `TunnelID`, and a 32-byte `TunnelSecret`;
- `DynamicUser=ascendany-cloudflared`, no capability in any process set,
  `NoNewPrivileges=1`, seccomp filter mode, closed address families, strict
  filesystem/device/kernel isolation, and metrics on `127.0.0.1:20090`;
- exact native command using the immutable release config and the private
  systemd credential path; no token, secret, proxy variable, account API token,
  or remote-managed configuration enters argv or the environment;
- ordered ingress for public v2, shadow v2, trainer `/version`, scoped trainer
  claims, trainer 404, and global 404. All owned origins are
  `http://127.0.0.1:18000`;
- staged/smoke prove the public hostname remains byte-identical to the legacy
  DB-backed loopback endpoint on port `8000`; smoke additionally proves shadow
  and trainer version bytes equal loopback v2. Production proves public,
  shadow, and trainer version bytes equal loopback v2 after DNS cutover.

Create the locally managed Tunnel once with the protected account origin
certificate, route only the shadow and trainer hostnames to it, then encrypt
the tunnel-scoped JSON credential on km6:

```bash
cloudflared tunnel create ascendany-v2-km6
cloudflared tunnel route dns e448a34c-9274-4c9d-8c69-e1a7fa369e52 ascendany-v2.kkkzbh.cn
cloudflared tunnel route dns e448a34c-9274-4c9d-8c69-e1a7fa369e52 ascendany-trainer.kkkzbh.cn
ssh km6 'install -d -o root -g root -m 0700 /run/ascendany-cloudflare'
scp "$HOME/.cloudflared/e448a34c-9274-4c9d-8c69-e1a7fa369e52.json" \
  km6:/run/ascendany-cloudflare/tunnel.json
ssh km6 'systemd-creds encrypt --name=tunnel_credentials \
  /run/ascendany-cloudflare/tunnel.json \
  /etc/ascendany/credentials/cloudflare_tunnel_credentials.cred && \
  chmod 0400 /etc/ascendany/credentials/cloudflare_tunnel_credentials.cred && \
  rm -f /run/ascendany-cloudflare/tunnel.json && \
  rmdir /run/ascendany-cloudflare && \
  systemctl enable --now ascendany-cloudflared.service'
```

The account origin certificate remains off km6. The installed credential can
run only this Tunnel and cannot create, delete, or reroute another Tunnel. The
final public cutover uses the protected operator environment and one explicit
DNS overwrite:

```bash
cloudflared tunnel route dns --overwrite-dns \
  e448a34c-9274-4c9d-8c69-e1a7fa369e52 ascendany.kkkzbh.cn
```

The deterministic fixture covers the package lock, credential shape, local
ingress order, native unit isolation, and retired-path rejection:

```bash
tools/tests/validate-cloudflared-fixture.sh
```

1. Complete an isolated rehearsal with a fresh `ascendany_v2` database, embedded migrations, synthetic fixtures, and a plugin v2 export/import cycle.
2. Run the release-owned PostgreSQL/PgBouncer provisioner against the still-live
   old stack, apply embedded migrations, apply the role bootstrap again, and
   verify schema version `5`, the isolated database owner, the complete role/ACL
   boundary, and the fixed HBA pool contract.
3. Keep the old Python API on `127.0.0.1:8000`. Freeze its writers only for the
   final window, produce one read-only offline custom-format dump of the old
   database, and prove that it restores in isolation. No old data enters
   `ascendany_v2`.
4. Start the reviewed native connector for the new locally managed Tunnel. Route
   only the shadow and trainer hostnames to it, keep the public hostname on the
   old Tunnel, and verify connector readiness plus the trainer 404 closure.
   With the v2 service and timer disabled/inactive, run
   `ASCENDANY_VALIDATION_PHASE=staged` using the protected operator
   `PGPASSFILE`. Start `ascendanyd.service` manually; the installed smoke
   drop-in keeps writes disabled on `127.0.0.1:18000`. Run
   `ASCENDANY_VALIDATION_PHASE=smoke`, empty-state HTTP/SSE/read-only client
   smoke tests, and explicit `GET /livez` plus `GET /readyz` checks. Prove every
   mutation route is rejected. LSP create, attach, and close must each return
   `503 writes_disabled`; disabled mode creates no LSP control socket and
   resolves no worker account.
5. Stop `ascendanyd.service`, remove only
   `/etc/systemd/system/ascendanyd.service.d/40-read-only-smoke.conf`, run
   `systemctl daemon-reload`, then enable and start the service. This is the
   write-activation commit point. Keep public DNS on the old Tunnel during
   private acceptance; the shadow and trainer routes are already
   smoke-validated through the new Tunnel.
6. Immediately run a disposable v2 write transaction, one fresh plugin v2
   import, the LSP control-manager/session smoke, and the production trainer
   run. Enable and start the trainer agent; after it writes its mode-`0600`
   candidate, stop it, verify the candidate with
   `ascendany-trainer-agent verify-acceptance`, promote those exact bytes
   atomically to root-owned
   `/var/lib/ascendany-acceptance/trainer-latest.json`, and copy the identical
   evidence to km6. Run the RTX `quiesced` gate, start the still-enabled unit,
   and run the RTX `production` gate. Create and verify the first production backup, run its sole
   restore operator, and then enable the timer:

   ```bash
   systemctl start ascendany-backup.service
   backup_id="$(find /var/backups/ascendany -mindepth 1 -maxdepth 1 -type d -printf '%f\n' | LC_ALL=C sort | tail -n 1)"
   /opt/ascendany/v2/bin/ascendany-backup verify "$backup_id"
   systemctl start "ascendany-restore-verify@${backup_id}.service"
   systemctl enable --now ascendany-backup.timer
   ```

   The restore unit publishes release- and manifest-bound evidence only after
   it has dropped the scratch database and removed scratch artifacts and
   credentials. The production gate requires the backup service result to
   match the newest bundle and the timer to have a future elapse.
7. Overwrite only the public hostname's DNS route to locally managed Tunnel
   `e448a34c-9274-4c9d-8c69-e1a7fa369e52`, wait until public `/version` equals
   loopback v2, then stop and remove the old `ascendany-cloudflared` container
   and remove its active token directory. Retain any required legacy evidence
   in a separate root-only offline archive. Run
   `ASCENDANY_VALIDATION_PHASE=production` and retain its complete log. The
   production gate rejects the retired connector and every native package,
   credential, unit, process, ingress, or route drift. After the full gate passes, confirm public `/livez`, schema-v5
   `/readyz`, `/version`, login, and one authorized read.

Step 5 is the commit point. Before it, rollback stops v2 and resumes the
unchanged old origin/service; v2 has accepted no writes. After it, v2 recovery
uses roll-forward because the acceptance flow creates durable v2 records and
artifacts. Preserve the old offline dump, v2 backup, old release, old unit,
OpenAPI document, client artifacts, and cutover logs until the retention
decision after stable operation.

Legacy cleanup occurs only after the retention gate passes and every off-repo consumer has a recorded v2 result.
