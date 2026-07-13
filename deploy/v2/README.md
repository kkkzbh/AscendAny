# AscendAny v2 production deployment

本文件是 AscendAny v2 唯一生产部署入口和 acceptance sequence。v2 在线系统只部署 Go backend、TypeScript first-party artifacts 和一个已经训练完成的 immutable recommendation model。训练工具、Python runtime、CUDA/RTX capability、trainer agent、训练 credential、训练 route 与训练 receipt 均不进入 release 或生产主机。

v2 从独立空数据库 `ascendany_v2` 启动。旧账号、session、业务数据、导入记录和旧 recommendation 数据全部放弃。唯一考试输入为浏览器插件产生的 `ascendany.pintia.snapshot.v2`。

## 1. Production ownership

| Component | OS identity | PostgreSQL identity | Network | Durable ownership |
| --- | --- | --- | --- | --- |
| `ascendanyd` | `ascendany` | `ascendanyd_login` through PgBouncer `127.0.0.1:6432` | HTTP `127.0.0.1:18000` | Online DB writes、artifact store、model inference |
| `ascendany-model` | release/root at verification; library code in `ascendanyd` | none | none | Reads one release-owned model artifact |
| Migrator | `ascendany-migrator` | `ascendany_migrator_login`, explicit `SET ROLE ascendany_owner` | PostgreSQL `127.0.0.1:5432` | Embedded schema migrations only |
| Backup | `ascendany-backup` | `ascendany_backup_login` | PostgreSQL `127.0.0.1:5432` | Immutable backup bundles |
| Restore verifier | `ascendany-restore` | `ascendany_restore_login`, explicit `SET ROLE ascendany_owner` | PostgreSQL `127.0.0.1:5432` | Disposable restore database and evidence |
| Judge instance | `ascendany-judge` | none | `PrivateNetwork=yes` | Per-job isolated state |
| LSP instance | `ascendany-lsp` | none | authenticated Unix control socket | Ephemeral session workspace |
| Cloudflared | dynamic `ascendany-cloudflared` | none | Tunnel outbound only | none |

Database URLs never contain passwords. Secret bytes enter services only through systemd encrypted credentials and `%d` file paths. Judge and LSP receive no database credential and have no network fallback.

## 2. Inference model contract

The release contains exactly:

```text
/opt/ascendany/v2/bin/ascendany-model
/opt/ascendany/v2/models/recommendation-model.json
```

`recommendation-model.json` is supplied from an external protected path at release-build time. The builder requires its independently recorded SHA-256 and runs the newly built command below before publication:

```bash
/opt/ascendany/v2/bin/ascendany-model verify \
  --model /opt/ascendany/v2/models/recommendation-model.json \
  --sha256 64_lowercase_hex \
  --expected-purpose production
```

The accepted model is canonical JSON, mode `0644`, one regular file, one hard link, and 1..16 MiB. Its closed contract is:

- schema `ascendany.recommendation.inference-model.v1`;
- algorithm `knowledge_mirt_feature_v1`;
- inference contract `ascendany.recommendation.inference.v1`;
- deployment purpose `production` for production releases;
- canonical UUIDv4 model identity;
- immutable training, feature-schema, knowledge-catalog, parameter and golden-vector provenance;
- deterministic Go evaluation with mandatory golden-vector verification.

`ascendany-model-activate.service` verifies the release artifact and transactionally binds it to schema v6 tables `recommendation_model_releases`, `recommendation_model_activation_events`, and `recommendation_model_head` during the explicit stopped-runtime activation phase. The activation event binds model SHA-256 to release version, Git commit and build time. `ascendanyd.service` verifies the same artifact before every start and requires that exact binding without mutating it. Production validation rejects a missing table, missing head, model mismatch, activation mismatch, release mismatch, semantic verification failure or file drift.

A model change always creates a new reviewed application release. Operators must never replace the model file inside an installed release.

## 3. Closed release

The manifest-closed payload contains 59 files and eight Go binaries:

```text
bin/ascendanyd
bin/ascendany-admin-bootstrap
bin/ascendany-backup
bin/ascendany-judge
bin/ascendany-lsp
bin/ascendany-migrate
bin/ascendany-model
bin/ascendany-release-ops
models/recommendation-model.json
```

It also contains the OpenAPI and Pintia contracts, DB role contract, non-secret configuration, systemd units, polkit rules, sysusers/tmpfiles definitions, backup/restore operators, package acquisition/attestation tools, the one-time fresh PostgreSQL/PgBouncer provisioner, and production validators. The installer rejects every additional or missing file/directory, mode drift, ownership drift, hard link, symlink, special node, descendant mount, hash mismatch and size mismatch.

The PostgreSQL/PgBouncer provisioner accepts one explicit `postgres` DBA channel in a fresh PostgreSQL 17 container. It creates only `ascendany_v2` and the v2 capability roles, publishes the closed PostgreSQL HBA/ident generation, creates the single-database native PgBouncer configuration, verifies one runtime connection, and emits an immutable receipt. It has no service migration, compatibility, recovery or reverse path. A partial run fails closed and requires an operator-reviewed cleanup through the DBA boundary before another attempt.

## 4. Build and local gates

Use one clean, reviewed SHA-1 Git commit. The production builder must run from the canonical protected checkout. Its output parent must be protected and owned by the build identity; production installation requires a root-owned release tree.

```bash
set -euo pipefail

commit="$(git rev-parse --verify 'HEAD^{commit}')"
source_date_epoch="$(git show -s --format=%ct "$commit")"
go_path="$(realpath "$(command -v go)")"
go_version="$(GOTOOLCHAIN=local GOENV=off "$go_path" env GOVERSION)"
model=/root/ascendany-models/recommendation-model.json
model_sha256="$(sha256sum "$model" | awk '{print $1}')"

tools/build-v2-release.sh \
  --version 0.2.0 \
  --commit "$commit" \
  --source-date-epoch "$source_date_epoch" \
  --go-path "$go_path" \
  --go-version "$go_version" \
  --goos linux \
  --goarch amd64 \
  --goamd64 v1 \
  --release-purpose production \
  --recommendation-model "$model" \
  --recommendation-model-sha256 "$model_sha256" \
  --output /root/ascendany-release/ascendany-v2
```

The builder uses the reviewed commit object, reconstructs and verifies its complete tree in an isolated object store, builds offline with `GOTOOLCHAIN=local`, `GOPROXY=off`, `CGO_ENABLED=0`, and writes the model digest into `config/ascendanyd.env`. Dirty worktree bytes do not enter the release.

Run these exact repository gates before transferring any payload:

```bash
bash -n deploy/v2/scripts/*.sh tools/build-v2-release.sh tools/verify-v2-boundary.sh
tools/verify-v2-boundary.sh
tools/tests/build-v2-release-fixture.sh
tools/tests/install-v2-release-fixture.sh
tools/tests/systemd-installed-unit-closure-fixture.sh
tools/tests/provision-postgres-pgbouncer-fixture.sh
tools/tests/validate-cloudflared-fixture.sh
tools/tests/validate-production-unit-closure-fixture.sh
systemd-analyze verify --man=no deploy/v2/systemd/*.service deploy/v2/systemd/*.timer
```

Go, TypeScript, OpenAPI, Pintia semantic fixtures, PostgreSQL 17 integration, race-sensitive tests and the full E2E sequence remain mandatory under the repository engineering policy.

Create an independent root-only release trust anchor:

```bash
release=/root/ascendany-release/ascendany-v2
trust_dir=/root/ascendany-release-trust/0.2.0
mkdir -m 0700 -- "$trust_dir"
anchor="$trust_dir/manifest.sha256"
manifest_sha256="$(sha256sum "$release/release-manifest.json" | awk '{print $1}')"
(umask 077; set -o noclobber; printf '%s\n' "$manifest_sha256" >"$anchor")
chmod 0400 "$anchor"
sync "$anchor"
```

Transfer the release and trust anchor through separate operator-controlled channels. Never derive the installation trust anchor from the received release tree.

## 5. Initial host installation

The canonical destination `/opt/ascendany/v2` must be absent. Run the installer from the protected reviewed checkout:

```bash
/absolute/reviewed-checkout/deploy/v2/scripts/install-v2-release.sh \
  --source /absolute/root-owned/ascendany-v2 \
  --manifest-sha256 "$(</root/ascendany-release-trust/0.2.0/manifest.sha256)" \
  --expected-purpose production
```

After the installer and staged acceptance succeed, write `stat -Lc '%d:%i' /opt/ascendany/v2` to `/root/ascendany-release-trust/0.2.0/installed.identity` with root-only mode `0400` and sync it. Keep that identity beside the independently transferred `manifest.sha256` and outside `/opt/ascendany`. A later replacement accepts this retained manifest digest and `DEVICE:INODE` pair as explicit installed-release trust inputs.

Then perform the following host setup in order:

1. Complete the one-way old-runtime cutover: stop, disable and mask `ascendany-api.service`; verify `MainPID=0` and no TCP listener on port `8000`. Do not retain an old-service rollback path on the production host.
2. Install `sysusers.d/ascendany-v2.conf` byte-for-byte at `/etc/sysusers.d/ascendany-v2.conf`; run `systemd-sysusers`.
3. Allocate non-overlapping `/etc/subuid` and `/etc/subgid` ranges for `ascendany-judge`.
4. Install `tmpfiles.d/ascendany-v2.conf` byte-for-byte at `/etc/tmpfiles.d/ascendany-v2.conf`; run `systemd-tmpfiles --create`.
5. Install release systemd units at `/etc/systemd/system`, polkit rules at `/etc/polkit-1/rules.d`, and non-secret application configuration at `/etc/ascendany/v2` with the ownership/modes declared by file headers and validators.
6. Install the release-owned `40-read-only-smoke.conf` at `/etc/systemd/system/ascendanyd.service.d/40-read-only-smoke.conf` before starting v2.
7. Acquire, attest and install the exact PgBouncer and Cloudflared packages pinned by `config/fedora-runtime-packages.json`. Mask the package-owned `pgbouncer.service`.
8. Acquire and attest the locked judge OCI image with `acquire-judge-image.sh`, `preload-judge-image.sh`, and `attest-judge-image.sh`.
9. Run `systemctl daemon-reload`; `NeedDaemonReload=yes` blocks every phase gate.

### Fresh host boundary

Provisioning starts from one closed host/database state:

1. The retired `ascendany-api.service` is masked and inactive, has `MainPID=0`, and TCP port `8000` has no listener. The production validator owns this retirement gate; the fresh database provisioner contains no legacy-service path.
2. The package-owned `pgbouncer.service` is masked and inactive.
3. The release-owned `ascendany-pgbouncer.service` is installed, disabled and inactive.
4. TCP ports `6432` and `18000` have no listeners, the reserved `ascendany-pgbouncer` container name is unused, and `/opt/ascendany/infra/pgbouncer` is absent.
5. `ascendany-postgres` is a fresh PostgreSQL 17 cluster. Its only non-system role is `postgres`; its only databases are `postgres`, `template0` and `template1`; its container-local peer DBA channel is available through the `postgres` OS identity.
6. The v2 database, v2 roles, encrypted database credentials and provisioning receipt are absent.

The provisioner validates every precondition before its first mutation. It owns no service conversion, data conversion, recovery or reverse path.

### One-time fresh database and pool provisioning

Create four distinct root-owned mode-`0600` password files with no trailing newline:

```text
/run/ascendany-v2-provision/runtime_db_password
/run/ascendany-v2-provision/migrator_db_password
/run/ascendany-v2-provision/backup_db_password
/run/ascendany-v2-provision/restore_db_password
```

Run the exact provisioning command once:

```bash
/opt/ascendany/v2/scripts/provision-postgres-pgbouncer.sh \
  --postgres-container ascendany-postgres \
  --postgres-dba-role postgres \
  --confirm-fresh-database ascendany_v2
```

Require `PASS [committed]`, removal of the plaintext input directory and the root-owned mode-`0400` receipt `/var/lib/ascendany-v2-provision/receipt`. Any existing receipt, v2 role/database, credential output, pool configuration, foreign process or partial state blocks another invocation. Review and remove partial state explicitly through the DBA/operator channel; the release contains no automatic reversal.

### Credentials and migration

The final encrypted credential set is:

```text
runtime_db_password.cred
migrator_db_password.cred
backup_db_password.cred
restore_db_password.cred
pgbouncer_userlist.cred
jwt_signing_key.cred
password_pepper.cred
cloudflare_tunnel_credentials.cred
```

All files live under `/etc/ascendany/credentials`, owned by `root:root`, mode `0400`. Optional feedback credentials use unique reviewed IDs and an exact generated systemd drop-in. No training credential is permitted.

Start the pool, apply embedded migrations, and verify roles:

```bash
systemctl enable --now ascendany-pgbouncer.service
systemctl start ascendany-migrate.service

podman exec -i --user postgres ascendany-postgres \
  /usr/bin/env -i HOME=/var/lib/postgresql PATH=/usr/bin:/bin LC_ALL=C \
  /usr/bin/psql -X --no-psqlrc --no-password --set=ON_ERROR_STOP=1 \
  --username=postgres --dbname=ascendany_v2 \
  < /opt/ascendany/v2/db/roles/001_v2_roles.sql
podman exec -i --user postgres ascendany-postgres \
  /usr/bin/env -i HOME=/var/lib/postgresql PATH=/usr/bin:/bin LC_ALL=C \
  /usr/bin/psql -X --no-psqlrc --no-password --set=ON_ERROR_STOP=1 \
  --username=postgres --dbname=ascendany_v2 \
  < /opt/ascendany/v2/db/roles/verify_v2_roles.sql
```

Use the container-local `postgres` peer DBA channel for the two direct `psql` invocations. Migration readiness must report schema version 6.

## 6. Cloudflare ingress

The locally managed Tunnel ID is `e448a34c-9274-4c9d-8c69-e1a7fa369e52`. The release config owns exactly three ordered rules:

1. `ascendany.kkkzbh.cn` → `http://127.0.0.1:18000`;
2. `ascendany-v2.kkkzbh.cn` → `http://127.0.0.1:18000`;
3. global `http_status:404`.

Encrypt the Tunnel-scoped JSON credential as `cloudflare_tunnel_credentials.cred`, install the release unit, and start it:

```bash
systemctl enable --now ascendany-cloudflared.service
```

Before smoke validation, route both public and shadow hostnames to the locally managed Tunnel. The smoke drop-in keeps the public service read-only during this DNS cutover:

```bash
cloudflared tunnel route dns --overwrite-dns \
  e448a34c-9274-4c9d-8c69-e1a7fa369e52 ascendany.kkkzbh.cn
cloudflared tunnel route dns --overwrite-dns \
  e448a34c-9274-4c9d-8c69-e1a7fa369e52 ascendany-v2.kkkzbh.cn
```

`validate-cloudflared.sh` verifies package bytes, signer, release config, encrypted credential shape, effective unit, process argv, capability sets, seccomp, connector readiness and live routes. It never receives an account API token.

## 7. Exact acceptance sequence

Each gate must exit zero and its complete stdout/stderr must be retained with the release manifest digest.

### 7.1 Staged

Requirements: `ascendanyd.service` disabled/inactive, backup timer disabled/inactive, smoke drop-in installed, port 18000 unused, Cloudflared active, empty fresh v2 database at schema v6, and no production backup evidence.

Create one temporary root-owned mode-`0600` `PGPASSFILE` containing only the `ascendanyd_login` entry, then run:

```bash
ASCENDANY_DEPLOYMENT_TRANSITION=initial \
ASCENDANY_VALIDATION_PHASE=staged \
PGPASSFILE=/run/ascendany-validation/runtime.pgpass \
  /opt/ascendany/v2/scripts/validate-production.sh
```

Remove the temporary file immediately after the gate. Staged validation performs release, model, database, role, credential-source, unit, package, pool, artifact and connector checks without starting the application.

### 7.2 Model activation

Run the explicit release-bound model activation while the HTTP service remains
stopped. Keep the read-only smoke drop-in installed until the following smoke
gate completes:

Create a fresh temporary root-owned mode-`0600` runtime `PGPASSFILE` under the
same contract as the staged gate, and remove it immediately after validation.

```bash
systemctl start ascendany-model-activate.service
ASCENDANY_DEPLOYMENT_TRANSITION=initial \
ASCENDANY_VALIDATION_PHASE=activation \
PGPASSFILE=/run/ascendany-validation/runtime.pgpass \
  /opt/ascendany/v2/scripts/validate-production.sh
```

The successful `ascendany-model-activate.service` transaction is the production
commit point. The static one-shot must return to inactive/dead with a successful
result. `ascendanyd serve` requires the exact schema-v6 release/head binding and
never creates or advances it. Initial activation is allowed before the first
knowledge catalog exists; recommendation reads return the explicit
`knowledge_catalog_unavailable` domain result until isolated initialization
publishes the matching catalog.

### 7.3 Read-only smoke

Route public and shadow DNS as specified above, then start the still-disabled
service manually:

```bash
systemctl start ascendanyd.service
ASCENDANY_DEPLOYMENT_TRANSITION=initial \
ASCENDANY_VALIDATION_PHASE=smoke \
  /opt/ascendany/v2/scripts/validate-production.sh
systemctl stop ascendanyd.service
```

The gate requires exact health/version/capability responses, schema version 6,
`writesEnabled=false`, byte-identical loopback/public/shadow version responses,
the release-bound model activation, and no business or durable-job mutation.

### 7.4 Isolated initialization and functional acceptance

Stop public ingress before enabling writes. Remove the smoke drop-in and start
the write-enabled service only on loopback:

```bash
systemctl stop ascendany-cloudflared.service ascendanyd.service
rm -- /etc/systemd/system/ascendanyd.service.d/40-read-only-smoke.conf
systemctl daemon-reload
systemctl start ascendanyd.service
```

Create the first administrator through `ascendany-admin-bootstrap.service` using
its one-time encrypted `admin_password` credential. Destroy the plaintext source
and require the unit to return to inactive/static state. Complete the following
workflow through loopback generated-SDK operations before restoring ingress:

1. Login and refresh-token/CSRF rotation acceptance.
2. Export the currently loaded Pintia problem set with the MV3 exporter and import the resulting `ascendany.pintia.snapshot.v2`.
3. Repeat identical bytes, identical typed domain content, and a newer snapshot; verify the three documented idempotency outcomes for logical key `(platform, problemSetId)`.
4. Reject unknown fields, duplicate identities, dangling references, partial pagination, count/hash mismatches and bounded-input violations.
5. Read imported exam, participant, problem and submission results through generated SDK clients.
6. Execute recommendation inference and verify the response binds the active model ID, model SHA-256, feature schema and knowledge catalog.
7. Exercise analytics publish/CAS, publish the fixed active knowledge catalog, then test SSE reconnect/authorization, Judge isolation and LSP create/attach/close.
8. Verify the deployed route, process, unit, credential and database-role closures exactly match this document.

The catalog publication in step 7 must use the specialized fixed key
`recommendation.catalog.active`, an analytics-generation CAS, canonical catalog
bytes whose SHA-256 equals the activated model manifest, and the application
configuration service. A release without a reviewed catalog artifact and an
operator path that exercises this contract cannot proceed to production.

After this isolated workflow succeeds, enable the application and restore the
locally managed Tunnel:

```bash
systemctl enable ascendanyd.service
systemctl start ascendany-cloudflared.service
```

### 7.5 Backup and restore

Create the first schema-v6 backup, verify it, run the sole restore operator, then enable the timer:

```bash
systemctl start ascendany-backup.service
backup_id="$(find /var/backups/ascendany -mindepth 1 -maxdepth 1 -type d -printf '%f\n' | LC_ALL=C sort | tail -n 1)"
/opt/ascendany/v2/bin/ascendany-backup verify "$backup_id"
systemctl start "ascendany-restore-verify@${backup_id}.service"
systemctl enable --now ascendany-backup.timer
```

The restore unit must publish `/var/lib/ascendany-acceptance/restore-verify.json` only after dropping its scratch database and removing scratch credentials/artifacts. Evidence must bind the active release and newest schema-v6 backup and be at most 31 days old.

### 7.6 Final production gate

```bash
ASCENDANY_DEPLOYMENT_TRANSITION=initial \
ASCENDANY_VALIDATION_PHASE=production \
  /opt/ascendany/v2/scripts/validate-production.sh
```

Production requires the smoke drop-in absent, `ascendanyd` enabled/active, `writesEnabled=true`, public and shadow routes byte-identical to loopback v2, exact model artifact and database activation binding, a successful current backup/restore evidence pair, and a future backup timer elapse.

Remove any offline retirement evidence from the production runtime tree after this gate. Production retains only the v2 release, v2 database/artifacts, encrypted credentials, backups and acceptance evidence.

## 8. Forward recovery boundary

Provisioning is one-way and write activation follows all staged and smoke gates. A failed partial provision requires operator-reviewed cleanup through the explicit DBA boundary before a fresh attempt. After activation, every repair is delivered as a reviewed forward v2 release. Selecting an earlier trained model still requires a new release that embeds that immutable artifact and records a new activation event.

Build the replacement at a strictly greater SemVer precedence and create a new independent manifest SHA-256 trust anchor. Preserve the accepted installed release's version-qualified manifest digest and directory identity record. Close the systemd activation boundary and stop every fixed and instantiated release consumer:

```bash
systemctl disable --now ascendanyd.service ascendany-backup.timer
mapfile -t release_instances < <(
  systemctl list-units --all --plain --no-legend --no-pager --type=service \
    'ascendany-restore-verify@*.service' \
    'ascendany-judge@*.service' \
    'ascendany-lsp@*.service' | awk '{print $1}'
)
if (( ${#release_instances[@]} != 0 )); then
  systemctl stop "${release_instances[@]}"
  systemctl reset-failed "${release_instances[@]}"
fi
systemctl stop \
  ascendany-model-activate.service \
  ascendany-admin-bootstrap.service \
  ascendany-backup.service \
  ascendany-cloudflared.service \
  ascendany-migrate.service \
  ascendany-pgbouncer.service
systemctl reset-failed \
  ascendanyd.service \
  ascendany-model-activate.service \
  ascendany-admin-bootstrap.service \
  ascendany-backup.service \
  ascendany-backup.timer \
  ascendany-cloudflared.service \
  ascendany-migrate.service \
  ascendany-pgbouncer.service
```

Do not run release binaries or scripts manually during this window. Require no `.v2.installing.*` or `.v2.removing.*` entry under `/opt/ascendany`, then run the protected installer with both trust boundaries explicit:

```bash
/absolute/reviewed-checkout/deploy/v2/scripts/install-v2-release.sh \
  --source /absolute/root-owned/ascendany-v2-next \
  --manifest-sha256 "$(</root/ascendany-release-trust/0.2.1/manifest.sha256)" \
  --replace-installed-manifest-sha256 "$(</root/ascendany-release-trust/0.2.0/manifest.sha256)" \
  --replace-installed-identity "$(</root/ascendany-release-trust/0.2.0/installed.identity)" \
  --expected-purpose production
```

Replacement is an explicit operation. The installer rejects a missing target, a target identity mismatch, installed manifest trust mismatch, installed tree drift, equal manifest, non-advancing SemVer, purpose drift, concurrent installer, pre-existing private entry, mount boundary, any old/new closed-tree integrity failure, or a non-quiesced release consumer. The enforced quiescence gate requires every fixed consumer service and backup timer to be loaded and exactly inactive/dead, enumerates and verifies every restore/Judge/LSP instance with `MainPID=0`, and rejects any other process whose executable, working/root directory, open descriptor, mapping, or absolute argv references the installed release. The installer runs this gate before staging, immediately before the namespace exchange, and immediately before retired-tree removal. It verifies the installed tree before staging and immediately before commit. It verifies the new source, staged tree, and model contract before commit.

The namespace commit is one Linux `renameat2(..., RENAME_EXCHANGE)` executed by the manifest-bound Go helper. The helper binds both directory identities, fsyncs the parent before and after the exchange, and returns an explicit committed status for every post-exchange failure. It has no alternate rename path. The installer then verifies the new tree at `/opt/ascendany/v2`, verifies the trusted old tree at the private stage name, and syncs the publication. The same manifest-bound helper binds the new target and old-tree identities, moves the old tree with `RENAME_NOREPLACE` to the matching `.v2.removing.*` tombstone, recursively unlinks through anchored directory file descriptors, fsyncs each cleared directory and the parent, and verifies that both private names are absent. The installer verifies the new tree again. Success leaves no old tree, `.v2.installing.*` entry, or `.v2.removing.*` entry.

An identity or namespace race fails directly. A failure before the exchange leaves the canonical installed tree unchanged and retains the new stage for explicit operator inspection. A failure after the exchange reports `committed-unverified`, reports both possible private cleanup names and the trusted retired identity, and performs no reverse exchange. Cleanup may have removed part of the trusted retired tree before a later namespace race is detected. Resolve the reported state through an operator-reviewed forward release.

Initial activation and forward activation are separate validator state machines. Initial `staged` requires empty administrator/model state; the explicit activation gate creates the first release-bound model head, and read-only smoke requires that exact head while allowing the catalog initialization window to remain incomplete. Every forward gate requires the canonical administrator and bootstrap audit, retained business data, one active prior model head, the fixed active knowledge catalog, and an existing verified backup/restore evidence pair. A forward preactivation gate rejects an empty database and rejects a model head already activated for the replacement application.

After a successful replacement, install the new release-owned host files, install its read-only smoke drop-in, reload systemd, restore the pool and Tunnel, and apply the reviewed migrations. Keep the application and backup timer disabled and inactive:

```bash
systemctl disable --now ascendanyd.service ascendany-backup.timer
install -o root -g root -m 0644 \
  /opt/ascendany/v2/systemd/ascendanyd.service.d/40-read-only-smoke.conf \
  /etc/systemd/system/ascendanyd.service.d/40-read-only-smoke.conf
systemctl daemon-reload
systemctl start ascendany-pgbouncer.service
systemctl start ascendany-migrate.service
systemctl start ascendany-cloudflared.service
```

Run the forward staged gate with the same temporary protected runtime `PGPASSFILE` contract used by the initial staged gate. This gate verifies the replacement artifact and retained administrator, data, active model/catalog, and prior backup evidence. It emits three state anchors only after every check passes:

```bash
forward_log=/root/ascendany-release-trust/0.2.1/forward-staged.log
forward_state=/root/ascendany-release-trust/0.2.1/forward-state.env
umask 077
ASCENDANY_DEPLOYMENT_TRANSITION=forward \
ASCENDANY_VALIDATION_PHASE=staged \
PGPASSFILE=/run/ascendany-validation/runtime.pgpass \
  /opt/ascendany/v2/scripts/validate-production.sh | tee "$forward_log"
awk '/^ASCENDANY_FORWARD_(DATABASE_FINGERPRINT_SHA256|BUSINESS_FINGERPRINT_SHA256|MODEL_HEAD_REVISION)=/' \
  "$forward_log" >"$forward_state"
[[ "$(wc -l <"$forward_state")" == 3 ]]
chmod 0400 "$forward_state"
sync "$forward_log" "$forward_state"
```

`ASCENDANY_FORWARD_DATABASE_FINGERPRINT_SHA256` binds every `ascendany` base table and sequence. `ASCENDANY_FORWARD_BUSINESS_FINGERPRINT_SHA256` binds the same state with only the three model activation tables and their release-ID sequence excluded. The model-head anchor records the exact prior revision. Keep this root-only file with the new release trust record.

Start the disabled service in read-only mode, load the three anchors, and run forward smoke:

```bash
set -a
. /root/ascendany-release-trust/0.2.1/forward-state.env
set +a
systemctl start ascendanyd.service
ASCENDANY_DEPLOYMENT_TRANSITION=forward \
ASCENDANY_VALIDATION_PHASE=smoke \
ASCENDANY_FORWARD_DATABASE_FINGERPRINT_SHA256="$ASCENDANY_FORWARD_DATABASE_FINGERPRINT_SHA256" \
ASCENDANY_FORWARD_BUSINESS_FINGERPRINT_SHA256="$ASCENDANY_FORWARD_BUSINESS_FINGERPRINT_SHA256" \
ASCENDANY_FORWARD_MODEL_HEAD_REVISION="$ASCENDANY_FORWARD_MODEL_HEAD_REVISION" \
  /opt/ascendany/v2/scripts/validate-production.sh
systemctl stop ascendanyd.service
```

Forward smoke requires `writesEnabled=false`, the prior model head/revision, and byte-exact full-database fingerprint equality with the staged anchor. The deployed mutation probes use existing valid administrator and student identities; every representative business and durable-job mutation must return `503 writes_disabled`. The authoritative Go route-contract test covers every state-changing route and proves the disabled capability reaches no mutation service.

Before activation, publish the replacement catalog through a reviewed
offline/ingress-isolated CAS phase. Its canonical digest must equal the
replacement model manifest while the prior model head remains active. The
current release cannot proceed when that operator phase or its release-owned
catalog artifact is absent.

Activate the replacement model while the HTTP service remains stopped, validate
the exact head transition, then remove the smoke drop-in, start writes, and
create and restore-verify a new backup before the forward production gate:

Create a fresh temporary root-owned mode-`0600` runtime `PGPASSFILE` before
this block and remove it immediately after the activation gate.

```bash
systemctl start ascendany-model-activate.service
ASCENDANY_DEPLOYMENT_TRANSITION=forward \
ASCENDANY_VALIDATION_PHASE=activation \
ASCENDANY_FORWARD_DATABASE_FINGERPRINT_SHA256="$ASCENDANY_FORWARD_DATABASE_FINGERPRINT_SHA256" \
ASCENDANY_FORWARD_BUSINESS_FINGERPRINT_SHA256="$ASCENDANY_FORWARD_BUSINESS_FINGERPRINT_SHA256" \
ASCENDANY_FORWARD_MODEL_HEAD_REVISION="$ASCENDANY_FORWARD_MODEL_HEAD_REVISION" \
PGPASSFILE=/run/ascendany-validation/runtime.pgpass \
  /opt/ascendany/v2/scripts/validate-production.sh
rm -- /etc/systemd/system/ascendanyd.service.d/40-read-only-smoke.conf
systemctl daemon-reload
systemctl enable --now ascendanyd.service
systemctl start ascendany-backup.service
backup_id="$(find /var/backups/ascendany -mindepth 1 -maxdepth 1 -type d -printf '%f\n' | LC_ALL=C sort | tail -n 1)"
/opt/ascendany/v2/bin/ascendany-backup verify "$backup_id"
systemctl start "ascendany-restore-verify@${backup_id}.service"
systemctl enable --now ascendany-backup.timer
ASCENDANY_DEPLOYMENT_TRANSITION=forward \
ASCENDANY_VALIDATION_PHASE=production \
ASCENDANY_FORWARD_DATABASE_FINGERPRINT_SHA256="$ASCENDANY_FORWARD_DATABASE_FINGERPRINT_SHA256" \
ASCENDANY_FORWARD_BUSINESS_FINGERPRINT_SHA256="$ASCENDANY_FORWARD_BUSINESS_FINGERPRINT_SHA256" \
ASCENDANY_FORWARD_MODEL_HEAD_REVISION="$ASCENDANY_FORWARD_MODEL_HEAD_REVISION" \
  /opt/ascendany/v2/scripts/validate-production.sh
```

The stopped-runtime forward activation gate requires exactly `prior head revision + 1`, a changed full fingerprint, and an unchanged business fingerprint. The production gate requires the new immutable release/model activation, the matching fixed-key active knowledge catalog, and new backup/restore evidence bound to the replacement release. It permits normal business and durable-job state changes after `ascendanyd` starts and does not compare live fingerprints against the stopped-runtime anchor. Run release-specific functional acceptance after activation. Create and sync the next version-qualified installed manifest/identity trust record after success.

## 9. Release blockers

Any item below blocks deployment:

- uncommitted or unreviewed release input;
- missing independent manifest or model SHA-256 trust anchor;
- any model-construction source, executable, runtime, unit, credential or accelerator payload in the release;
- release closed-set, ownership, mode, path, mount, size or hash drift;
- schema version other than 6 or migration hash drift;
- model semantic, golden-vector, release-manifest or DB activation drift;
- missing release-owned knowledge-catalog artifact or missing isolated catalog-publication operator phase;
- plaintext secret, password in a database URL, undeclared systemd credential or manager environment drift;
- public/shadow/global-404 ingress drift or any additional AscendAny ingress rule;
- partial Pintia pagination or malformed snapshot acceptance;
- Judge/LSP database credential or network capability;
- missing current backup/restore verification evidence.

`OJ_JUDGE_CONTRACT.md` and `LSP_CONTROL_CONTRACT.md` define the worker protocols. `doc/重写v2架构与验收.md` defines the complete architecture and final product-level acceptance boundary.
