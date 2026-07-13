# AscendAny v2 production deployment

本文件是 AscendAny v2 唯一生产部署入口和 acceptance sequence。v2 在线系统只部署 Go backend、TypeScript first-party artifacts 和一个已经训练完成的 immutable recommendation model。训练工具、Python runtime、CUDA/RTX capability、trainer agent、训练 credential、训练 route 与训练 receipt 均不进入 release 或生产主机。

v2 从独立空数据库 `ascendany_v2` 启动。旧账号、session、业务数据、导入记录和旧 recommendation 数据全部放弃。唯一考试输入为浏览器插件产生的 `ascendany.pintia.snapshot.v2`。

## 1. Production ownership

| Component | OS identity | PostgreSQL identity | Network | Durable ownership |
| --- | --- | --- | --- | --- |
| `ascendanyd` | `ascendany` | `ascendanyd_login` through PgBouncer `127.0.0.1:6432` | HTTP `127.0.0.1:18000` | Online DB writes、artifact store、model inference |
| Model verifier | release/root at verification | none | none | Reads one release-owned model/catalog pair |
| Model registrar/activator | `ascendany` | `ascendanyd_login` through PgBouncer `127.0.0.1:6432` | PostgreSQL only | Immutable registration and explicit head activation |
| Catalog publisher | `ascendany-catalog-publisher` with effective group `ascendany-catalog-readers` | `ascendany_catalog_publisher_login` through PgBouncer `127.0.0.1:6432` | loopback PostgreSQL only | Atomic authorized-publication function invocation and receipt files; no direct catalog DML |
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
/opt/ascendany/v2/models/recommendation-knowledge-catalog.json
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

`ascendany-model-register.service` verifies and transactionally registers the
release artifact without changing the active head. The initial stopped-runtime
activation creates revision `1` before the initial catalog publication. Every
forward catalog publication creates one immutable publication/receipt for the
registered target release and atomically reserves it as the current head's
single pending publication. Runtime startup and recommendation reads reject a
pending head. `ascendany-model-activate.service` consumes that exact publication,
clears the pending state, and advances the head exactly once. The schema-v7 ownership is
closed over `recommendation_model_releases`,
`knowledge_catalog_publications`, `recommendation_model_activation_events`, and
`recommendation_model_head`. Each activation binds the model SHA-256, catalog
publication, release version, Git commit and build time. `ascendanyd.service`
verifies the same artifact before every start and requires the committed binding
without mutating it. Production validation rejects a missing row, receipt, head,
pending publication, model/catalog mismatch, activation mismatch, release mismatch, semantic
verification failure or file drift.

During online `prepare`, `ascendanyd` validates the complete catalog/release
intent and persists one short-lived, single-use authorization bound to the
server-returned canonical request and the administrator access token's expiry.
The stopped publisher consumes that authorization only through the unique
`SECURITY DEFINER` function
`ascendany.publish_authorized_knowledge_catalog(uuid, text, text)`; its database
role has no direct catalog DML. Success produces the strict 26-field
`ascendany.knowledge_catalog.publication-receipt.v1`, including
`authorizationId`.

A model change always creates a new reviewed application release. Operators must never replace the model file inside an installed release.

## 3. Closed release

The manifest-closed payload contains 68 files, nine Go binaries, and one bundled
TypeScript operator artifact:

```text
bin/ascendanyd
bin/ascendany-admin-bootstrap
bin/ascendany-backup
bin/ascendany-catalog-publish
bin/ascendany-judge
bin/ascendany-lsp
bin/ascendany-migrate
bin/ascendany-model
bin/ascendany-release-ops
models/recommendation-model.json
models/recommendation-knowledge-catalog.json
operators/ascendany-production-initialize.mjs
```

The operator is a pinned-esbuild single-file bundle, mode `0555`. It runs only
during the root-controlled initialization/cutover window through the exact
`/usr/bin/node-22` package contract verified by the staged gate. Online units do
not execute Node. The release also contains the OpenAPI and Pintia contracts, DB
role contract, non-secret configuration, systemd units, polkit rules,
sysusers/tmpfiles definitions, backup/restore operators, package
acquisition/attestation tools, the one-time fresh PostgreSQL/PgBouncer
provisioner, and production validators. The installer rejects every additional
or missing file/directory, mode drift, ownership drift, hard link, symlink,
special node, descendant mount, hash mismatch and size mismatch.

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
catalog=/root/ascendany-models/recommendation-knowledge-catalog.json
catalog_sha256="$(sha256sum "$catalog" | awk '{print $1}')"

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
  --knowledge-catalog "$catalog" \
  --knowledge-catalog-sha256 "$catalog_sha256" \
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
tools/tests/validate-production-state-fixture.sh
tools/tests/validate-production-generation-fixture.sh
tools/tests/validate-production-catalog-fixture.sh
tools/tests/validate-production-schema-fingerprint-fixture.sh
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

### One-time pre-v2-generation retirement on km6

km6 currently contains a trainer-era `/opt/ascendany/v2`, the Python release
under `/opt/ascendany/Release` and `/opt/ascendany/.venv`, legacy API/trainer
units and credentials, a containerized legacy Tunnel, and a PostgreSQL cluster
whose `AscendAny` and trainer-era `ascendany_v2` databases are outside the v2
contract. This state is a different deployment generation. It must never enter
the forward-release state machine or the generation-v2 installer replacement
path.

Run the following one-way operator sequence once, from a protected root shell,
before the canonical initial install below. A failed assertion stops the
sequence. Inspect and resolve the retained state explicitly; do not skip a gate
or rerun a completed destructive step. The archive is evidence only. No release
runtime reads it and no legacy data is imported into generation v2.

First isolate every writer, prove the known old cluster identity, and create a
root-only archive outside all production roots:

```bash
set -Eeuo pipefail
umask 077

postgres_image_reference='docker.io/library/postgres@sha256:5c855ad7b85e68e48a62f34662853f38b57c1c1d80f3a927ab58034fd6d31c5e'
postgres_image_id='07f76768a0c956d6e9bddbcdb3c2be7fd9fd45ee6174a26873f8219fccbad65d'
archive_parent=/root/ascendany-generation-archive
archive_id="pre-v2-$(date -u +%Y%m%dT%H%M%SZ)"
archive="$archive_parent/$archive_id"
verify_container="ascendany-generation-verify-${archive_id#pre-v2-}"
verify_volume="${verify_container}-data"
verify_secret_root=/run/ascendany-generation-verify

[[ "$archive_id" =~ ^pre-v2-[0-9]{8}T[0-9]{6}Z$ ]]
[[ ! -e "$archive" && ! -L "$archive" ]]
for path in \
  /opt/ascendany/Release \
  /opt/ascendany/.venv \
  /opt/ascendany/v2/release-manifest.json \
  /etc/systemd/system/ascendany-api.service \
  /etc/systemd/system/ascendany-trainer-agent.service; do
  [[ -e "$path" && ! -L "$path" ]]
done
podman container exists ascendany-postgres
[[ "$(podman inspect --format '{{.State.Running}}' ascendany-postgres)" == true ]]
[[ "$(podman inspect --format '{{.Image}}' ascendany-postgres)" == "$postgres_image_id" ]]
old_volume="$(podman inspect ascendany-postgres | jq -er '
  if type == "array" and length == 1 and
     (.[0].Mounts | length) == 1 and
     .[0].Mounts[0].Type == "volume" and
     .[0].Mounts[0].Destination == "/var/lib/postgresql/data"
  then .[0].Mounts[0].Name else empty end')"
[[ "$old_volume" == ascendany-postgres-data ]]

systemctl disable --now \
  ascendany-api.service \
  ascendany-trainer-agent.service \
  ascendanyd.service \
  ascendany-backup.timer \
  ascendany-cloudflared.service \
  ascendany-pgbouncer.service
systemctl stop \
  ascendany-admin-bootstrap.service \
  ascendany-backup.service \
  ascendany-migrate.service
mapfile -t transient_consumers < <(
  systemctl list-units --all --plain --no-legend \
    'ascendany-judge@*.service' \
    'ascendany-lsp@*.service' \
    'ascendany-restore-verify@*.service' |
    awk '{print $1}' | LC_ALL=C sort -u
)
for unit in \
  ascendany-model-register.service \
  ascendany-model-activate.service \
  ascendany-catalog-publish.service; do
  if [[ "$(systemctl show "$unit" --property=LoadState --value)" == loaded ]]; then
    transient_consumers+=("$unit")
  fi
done
mapfile -t transient_consumers < <(printf '%s\n' "${transient_consumers[@]}" | sed '/^$/d' | LC_ALL=C sort -u)
if (( ${#transient_consumers[@]} > 0 )); then
  systemctl stop "${transient_consumers[@]}"
fi
for unit in \
  ascendany-api.service \
  ascendany-trainer-agent.service \
  ascendanyd.service \
  ascendany-admin-bootstrap.service \
  ascendany-backup.service \
  ascendany-migrate.service \
  "${transient_consumers[@]}"; do
  [[ "$(systemctl show "$unit" --property=ActiveState --value)" == inactive ]]
  [[ "$(systemctl show "$unit" --property=MainPID --value)" == 0 ]]
done
podman container exists ascendany-cloudflared
podman stop ascendany-cloudflared
for port in 8000 6432 18000; do
  [[ -z "$(ss -H -ltn "sport = :$port")" ]]
done

retired_markers=$'/opt/ascendany/Release\n/opt/ascendany/.venv\n/opt/ascendany/v2\n/opt/ascendany-trainer-runtime\n/var/lib/ascendany-trainer'
for process in /proc/[1-9]*; do
  references="$(
    readlink -- "$process/exe" "$process/cwd" "$process/root" 2>/dev/null || true
    tr '\0' ' ' <"$process/cmdline" 2>/dev/null || true
    sed -n '1,$p' "$process/maps" 2>/dev/null || true
    for descriptor in "$process"/fd/*; do
      readlink -- "$descriptor" 2>/dev/null || true
    done
  )"
  while IFS= read -r marker; do
    [[ "$references" != *"$marker"* ]]
  done <<<"$retired_markers"
done

old_databases="$(podman exec -i --user postgres ascendany-postgres \
  /usr/bin/env -i HOME=/var/lib/postgresql PATH=/usr/bin:/bin LC_ALL=C \
  /usr/bin/psql -X --no-psqlrc --no-password --set=ON_ERROR_STOP=1 \
  --username=postgres --dbname=postgres --tuples-only --no-align \
  --command='SELECT datname FROM pg_database ORDER BY datname')"
[[ "$old_databases" == $'AscendAny\nascendany_v2\npostgres\ntemplate0\ntemplate1' ]]
old_sessions="$(podman exec -i --user postgres ascendany-postgres \
  /usr/bin/env -i HOME=/var/lib/postgresql PATH=/usr/bin:/bin LC_ALL=C \
  /usr/bin/psql -X --no-psqlrc --no-password --set=ON_ERROR_STOP=1 \
  --username=postgres --dbname=postgres --tuples-only --no-align \
  --command="SELECT count(*) FROM pg_stat_activity WHERE pid <> pg_backend_pid() AND backend_type = 'client backend'")"
[[ "$old_sessions" == 0 ]]

install -d -o root -g root -m 0700 "$archive_parent" "$archive"
old_system_identifier="$(podman exec -i --user postgres ascendany-postgres \
  /usr/bin/env -i HOME=/var/lib/postgresql PATH=/usr/bin:/bin LC_ALL=C \
  /usr/bin/psql -X --no-psqlrc --no-password --set=ON_ERROR_STOP=1 \
  --username=postgres --dbname=postgres --tuples-only --no-align \
  --command='SELECT system_identifier FROM pg_control_system()')"
[[ "$old_system_identifier" =~ ^[0-9]{10,20}$ ]]
printf '%s\n' "$old_system_identifier" >"$archive/postgres-system-identifier"
printf '%s\n' "$old_databases" >"$archive/postgres-databases.before"
cp -- /opt/ascendany/v2/release-manifest.json "$archive/trainer-era-v2-release-manifest.json"
cp -- /etc/subuid "$archive/subuid.before"
cp -- /etc/subgid "$archive/subgid.before"
systemctl cat ascendany-api.service ascendany-trainer-agent.service ascendanyd.service \
  >"$archive/systemd-units.before"
podman inspect ascendany-postgres | jq -S '[.[] | {
  Id, Image, ImageName: .Config.Image, Created,
  Command: .Config.Cmd, RestartPolicy: .HostConfig.RestartPolicy,
  Mounts, NetworkSettings: .NetworkSettings.Networks,
  PortBindings: .HostConfig.PortBindings
}]' >"$archive/postgres-container.before.json"
for path in \
  /opt/ascendany \
  /opt/ascendany-trainer-runtime \
  /var/lib/ascendany-trainer \
  /var/backups/ascendany \
  /etc/ascendany; do
  if [[ -e "$path" || -L "$path" ]]; then
    find -H "$path" -xdev -printf '%p|%y|%m|%u|%g|%s\n'
  fi
done | LC_ALL=C sort >"$archive/runtime-filesystem-inventory.before"
```

Create logical dumps without role passwords, record the original row-count
inventory, and prove that every custom-format dump has a readable catalog:

```bash
podman exec -i --user postgres ascendany-postgres \
  /usr/bin/env -i HOME=/var/lib/postgresql PATH=/usr/bin:/bin LC_ALL=C \
  /usr/bin/pg_dumpall --username=postgres --globals-only --no-role-passwords \
  >"$archive/globals.sql"
for database in AscendAny ascendany_v2 postgres; do
  podman exec -i --user postgres ascendany-postgres \
    /usr/bin/env -i HOME=/var/lib/postgresql PATH=/usr/bin:/bin LC_ALL=C \
    /usr/bin/pg_dump --username=postgres --dbname="$database" \
    --format=custom --no-owner --no-acl \
    >"$archive/$database.dump"
  /usr/bin/pg_restore --list "$archive/$database.dump" \
    >"$archive/$database.restore-list"
  [[ -s "$archive/$database.restore-list" ]]
done

database_inventory() {
  local container="$1" database="$2" output="$3"
  podman exec -i --user postgres "$container" \
    /usr/bin/env -i HOME=/var/lib/postgresql PATH=/usr/bin:/bin LC_ALL=C \
    /usr/bin/psql -X --no-psqlrc --no-password --set=ON_ERROR_STOP=1 \
    --username=postgres --dbname="$database" --tuples-only --no-align <<'SQL' \
    | LC_ALL=C sort >"$output"
SELECT format(
  'SELECT %L || count(*)::text FROM %I.%I;',
  table_schema || '.' || table_name || '|', table_schema, table_name
)
FROM information_schema.tables
WHERE table_type = 'BASE TABLE'
  AND table_schema NOT IN ('information_schema', 'pg_catalog')
ORDER BY table_schema, table_name
\gexec
SQL
}

for database in AscendAny ascendany_v2 postgres; do
  database_inventory ascendany-postgres "$database" \
    "$archive/$database.rows.before"
done
```

Restore all three dumps into a disposable, network-isolated container using the
same pinned PostgreSQL 17 image. The original and restored table/count
inventories must be byte-identical. Failure leaves the disposable container and
volume available for explicit inspection; continue only after a successful
verification and cleanup:

```bash
[[ ! -e "$verify_secret_root" && ! -L "$verify_secret_root" ]]
! podman container exists "$verify_container"
! podman volume exists "$verify_volume"
install -d -o root -g root -m 0700 "$verify_secret_root"
openssl rand -base64 48 | tr -d '\n' >"$verify_secret_root/postgres-password"
[[ "$(stat -Lc '%u:%g:%a:%h:%s' "$verify_secret_root/postgres-password")" == 0:0:600:1:64 ]]
podman volume create "$verify_volume" >/dev/null
podman run --detach \
  --name "$verify_container" \
  --network none \
  --http-proxy=false \
  --env POSTGRES_USER=ascendany_archive_restore \
  --env POSTGRES_DB=postgres \
  --env POSTGRES_PASSWORD_FILE=/run/secrets/postgres-password \
  --volume "$verify_volume:/var/lib/postgresql/data" \
  --volume "$verify_secret_root/postgres-password:/run/secrets/postgres-password:ro,Z" \
  "$postgres_image_reference" \
  postgres -c password_encryption=scram-sha-256 >/dev/null
for attempt in {1..120}; do
  if podman exec --user postgres "$verify_container" \
      /usr/bin/pg_isready --username=ascendany_archive_restore --dbname=postgres >/dev/null 2>&1; then
    break
  fi
  [[ "$attempt" != 120 ]]
  sleep 0.5
done
podman exec -i --user postgres "$verify_container" \
  /usr/bin/psql -X --no-psqlrc --no-password --set=ON_ERROR_STOP=1 \
  --username=ascendany_archive_restore --dbname=postgres <"$archive/globals.sql"
for database in AscendAny ascendany_v2; do
  podman exec --user postgres "$verify_container" \
    /usr/bin/createdb --username=ascendany_archive_restore --template=template0 "$database"
done
for database in AscendAny ascendany_v2 postgres; do
  podman exec -i --user postgres "$verify_container" \
    /usr/bin/pg_restore --username=postgres --dbname="$database" \
    --exit-on-error --no-owner --no-acl <"$archive/$database.dump"
  database_inventory "$verify_container" "$database" \
    "$archive/$database.rows.restored"
  cmp --silent -- "$archive/$database.rows.before" "$archive/$database.rows.restored"
done
podman exec -i --user postgres "$verify_container" \
  /usr/bin/psql -X --no-psqlrc --no-password --set=ON_ERROR_STOP=1 \
  --username=postgres --dbname=postgres --tuples-only --no-align \
  --command='SELECT datname FROM pg_database ORDER BY datname' \
  >"$archive/postgres-databases.restored"
cmp --silent -- "$archive/postgres-databases.before" "$archive/postgres-databases.restored"
printf '%s\n' \
  'schema=ascendany.pre-v2-generation-archive.v1' \
  "oldPostgresSystemIdentifier=$old_system_identifier" \
  'restoreVerified=true' \
  >"$archive/restore-verification"
podman stop "$verify_container" >/dev/null
podman rm "$verify_container" >/dev/null
podman volume rm "$verify_volume" >/dev/null
rm -rf -- "$verify_secret_root"

(
  cd -- "$archive"
  find . -type f ! -name SHA256SUMS -print0 | LC_ALL=C sort -z |
    xargs -0 sha256sum >SHA256SUMS
  sha256sum --check SHA256SUMS
)
find "$archive" -type f -exec chmod 0400 -- {} +
find "$archive" -type d -exec chmod 0500 -- {} +
sync "$archive"
```

Only the sealed archive authorizes destruction. Remove the old container,
volume, releases, runtime/config/credential state and durable v2 state. Preserve
the archive under `/root/ascendany-generation-archive`; it is outside every
production root and remains unreadable to service identities:

```bash
(
  cd -- "$archive"
  sha256sum --check SHA256SUMS
)
grep -Fx 'restoreVerified=true' "$archive/restore-verification"

podman stop ascendany-postgres >/dev/null
podman rm ascendany-postgres >/dev/null
podman volume rm "$old_volume" >/dev/null
podman rm ascendany-cloudflared >/dev/null

systemctl disable --now \
  ascendany-api.service \
  ascendany-trainer-agent.service \
  ascendanyd.service \
  ascendany-backup.timer \
  ascendany-cloudflared.service \
  ascendany-pgbouncer.service
rm -f -- \
  /etc/systemd/system/ascendany-api.service \
  /etc/systemd/system/ascendany-trainer-agent.service \
  /etc/systemd/system/ascendanyd.service \
  /etc/systemd/system/ascendany-model-activate.service \
  /etc/systemd/system/ascendany-catalog-publish.service \
  /etc/systemd/system/ascendany-admin-bootstrap.service \
  /etc/systemd/system/ascendany-backup.service \
  /etc/systemd/system/ascendany-backup.timer \
  /etc/systemd/system/ascendany-cloudflared.service \
  /etc/systemd/system/ascendany-migrate.service \
  /etc/systemd/system/ascendany-pgbouncer.service \
  /etc/systemd/system/ascendany-restore-verify@.service \
  /etc/systemd/system/ascendany-judge@.service \
  /etc/systemd/system/ascendany-lsp@.service \
  /etc/sysusers.d/ascendany-v2.conf \
  /etc/tmpfiles.d/ascendany-v2.conf \
  /etc/polkit-1/rules.d/60-ascendany-judge.rules \
  /etc/polkit-1/rules.d/61-ascendany-lsp.rules
rm -rf -- \
  /etc/systemd/system/ascendany-api.service.d \
  /etc/systemd/system/ascendany-trainer-agent.service.d \
  /etc/systemd/system/ascendanyd.service.d \
  /opt/ascendany \
  /opt/ascendany-trainer-runtime \
  /etc/ascendany \
  /etc/ascendany-catalog-publisher \
  /var/lib/ascendany \
  /var/lib/ascendany-catalog-publisher \
  /var/lib/ascendany-trainer \
  /var/lib/ascendany-v2-provision \
  /var/lib/ascendany-acceptance \
  /var/lib/ascendany-restore \
  /var/lib/ascendany-migrate \
  /var/lib/ascendany-judge \
  /var/lib/ascendany-lsp-root \
  /var/backups/ascendany \
  /var/log/ascendany-trainer \
  /run/ascendany \
  /run/ascendany-v2-provision \
  /run/ascendany-admin-bootstrap-input \
  /run/ascendany-catalog-publish-input \
  /run/ascendany-restore-operator
for user in \
  ascendany \
  ascendany-backup \
  ascendany-catalog-publisher \
  ascendany-judge \
  ascendany-lsp \
  ascendany-migrator \
  ascendany-restore \
  ascendany-trainer; do
  if getent passwd "$user" >/dev/null; then
    userdel "$user"
  fi
done
for group in \
  ascendany \
  ascendany-backup \
  ascendany-backup-readers \
  ascendany-catalog-readers \
  ascendany-catalog-publisher \
  ascendany-judge \
  ascendany-lsp \
  ascendany-lsp-control \
  ascendany-migrator \
  ascendany-restore \
  ascendany-runtime \
  ascendany-trainer; do
  if getent group "$group" >/dev/null; then
    groupdel "$group"
  fi
done
install -d -o root -g root -m 0755 /etc/systemd/system
ln -s /dev/null /etc/systemd/system/ascendany-api.service
ln -s /dev/null /etc/systemd/system/ascendany-trainer-agent.service
systemctl daemon-reload

for unit in ascendany-api.service ascendany-trainer-agent.service; do
  [[ "$(systemctl is-enabled "$unit")" == masked ]]
  [[ "$(systemctl is-active "$unit")" == inactive ]]
  [[ "$(systemctl show -P MainPID "$unit")" == 0 ]]
done
for port in 8000 5432 6432 18000; do
  [[ -z "$(ss -H -ltn "sport = :$port")" ]]
done
! podman container exists ascendany-postgres
! podman container exists ascendany-cloudflared
! podman volume exists "$old_volume"
[[ ! -e /opt/ascendany && ! -L /opt/ascendany ]]
[[ ! -e /etc/ascendany && ! -L /etc/ascendany ]]
[[ ! -e /etc/ascendany-catalog-publisher && ! -L /etc/ascendany-catalog-publisher ]]
[[ ! -e /var/lib/ascendany-catalog-publisher && ! -L /var/lib/ascendany-catalog-publisher ]]
for identity in \
  ascendany ascendany-backup ascendany-catalog-publisher ascendany-judge \
  ascendany-lsp ascendany-migrator ascendany-restore ascendany-trainer; do
  ! getent passwd "$identity" >/dev/null
done
for identity in \
  ascendany ascendany-backup ascendany-backup-readers ascendany-catalog-publisher \
  ascendany-catalog-readers ascendany-judge ascendany-lsp ascendany-lsp-control ascendany-migrator \
  ascendany-restore ascendany-runtime ascendany-trainer; do
  ! getent group "$identity" >/dev/null
done
! grep -Eq '^ascendany([:-]|-[a-z0-9-]+:)' /etc/subuid
! grep -Eq '^ascendany([:-]|-[a-z0-9-]+:)' /etc/subgid
```

Create a new PostgreSQL data volume with a disposable initialization container.
The final `ascendany-postgres` container receives no password file, bootstrap
identity, proxy variable or legacy environment. Its system identifier must
differ from the sealed old cluster:

```bash
bootstrap_container=ascendany-postgres-bootstrap
bootstrap_secret_root=/run/ascendany-postgres-bootstrap
[[ ! -e "$bootstrap_secret_root" && ! -L "$bootstrap_secret_root" ]]
! podman container exists "$bootstrap_container"
! podman container exists ascendany-postgres
! podman volume exists ascendany-postgres-data
resolved_image_id="$(podman image inspect "$postgres_image_reference" --format '{{.Id}}')"
resolved_image_id="${resolved_image_id#sha256:}"
[[ "$resolved_image_id" == "$postgres_image_id" ]]

install -d -o root -g root -m 0700 "$bootstrap_secret_root"
openssl rand -base64 48 | tr -d '\n' >"$bootstrap_secret_root/postgres-password"
[[ "$(stat -Lc '%u:%g:%a:%h:%s' "$bootstrap_secret_root/postgres-password")" == 0:0:600:1:64 ]]
podman volume create ascendany-postgres-data >/dev/null
podman run --detach \
  --name "$bootstrap_container" \
  --network none \
  --http-proxy=false \
  --env POSTGRES_USER=postgres \
  --env POSTGRES_DB=postgres \
  --env POSTGRES_PASSWORD_FILE=/run/secrets/postgres-password \
  --volume ascendany-postgres-data:/var/lib/postgresql/data \
  --volume "$bootstrap_secret_root/postgres-password:/run/secrets/postgres-password:ro,Z" \
  "$postgres_image_reference" \
  postgres -c password_encryption=scram-sha-256 >/dev/null
for attempt in {1..120}; do
  if podman exec --user postgres "$bootstrap_container" \
      /usr/bin/pg_isready --username=postgres --dbname=postgres >/dev/null 2>&1; then
    break
  fi
  [[ "$attempt" != 120 ]]
  sleep 0.5
done
podman stop "$bootstrap_container" >/dev/null
podman rm "$bootstrap_container" >/dev/null
rm -rf -- "$bootstrap_secret_root"

podman run --detach \
  --name ascendany-postgres \
  --restart=always \
  --network podman \
  --ip 10.88.0.2 \
  --publish 127.0.0.1:5432:5432 \
  --http-proxy=false \
  --volume ascendany-postgres-data:/var/lib/postgresql/data \
  "$postgres_image_reference" \
  postgres -c password_encryption=scram-sha-256 >/dev/null
for attempt in {1..120}; do
  if podman exec --user postgres ascendany-postgres \
      /usr/bin/pg_isready --username=postgres --dbname=postgres >/dev/null 2>&1; then
    break
  fi
  [[ "$attempt" != 120 ]]
  sleep 0.5
done

podman inspect ascendany-postgres | jq -e \
  --arg imageId "$postgres_image_id" \
  --arg imageReference "$postgres_image_reference" '
    type == "array" and length == 1 and
    .[0].Image == $imageId and
    .[0].Config.Image == $imageReference and
    .[0].Config.Cmd == ["postgres", "-c", "password_encryption=scram-sha-256"] and
    .[0].HostConfig.RestartPolicy == {Name: "always", MaximumRetryCount: 0} and
    .[0].HostConfig.PortBindings == {"5432/tcp": [{HostIp: "127.0.0.1", HostPort: "5432"}]} and
    (.[0].Mounts | length) == 1 and
    .[0].Mounts[0].Type == "volume" and
    .[0].Mounts[0].Name == "ascendany-postgres-data" and
    .[0].Mounts[0].Destination == "/var/lib/postgresql/data" and
    .[0].Mounts[0].RW == true and
    (.[0].Mounts[0].Options | sort) == ["nodev", "nosuid", "rbind"] and
    all(.[0].Config.Env[];
      ((split("=")[0]) as $name |
        ($name | test("(^|_)(http|https|all|no)_proxy$|^POSTGRES_(PASSWORD|PASSWORD_FILE|USER|DB|HOST_AUTH_METHOD)$"; "i") | not)))'
new_system_identifier="$(podman exec -i --user postgres ascendany-postgres \
  /usr/bin/env -i HOME=/var/lib/postgresql PATH=/usr/bin:/bin LC_ALL=C \
  /usr/bin/psql -X --no-psqlrc --no-password --set=ON_ERROR_STOP=1 \
  --username=postgres --dbname=postgres --tuples-only --no-align \
  --command='SELECT system_identifier FROM pg_control_system()')"
[[ "$new_system_identifier" =~ ^[0-9]{10,20}$ ]]
[[ "$new_system_identifier" != "$(<"$archive/postgres-system-identifier")" ]]
fresh_cluster="$(podman exec -i --user postgres ascendany-postgres \
  /usr/bin/env -i HOME=/var/lib/postgresql PATH=/usr/bin:/bin LC_ALL=C \
  /usr/bin/psql -X --no-psqlrc --no-password --set=ON_ERROR_STOP=1 \
  --username=postgres --dbname=postgres --tuples-only --no-align --field-separator='|' \
  --command="SELECT
    (SELECT string_agg(rolname, ',' ORDER BY rolname) FROM pg_roles WHERE rolname !~ '^pg_'),
    (SELECT string_agg(datname, ',' ORDER BY datname) FROM pg_database)")"
[[ "$fresh_cluster" == 'postgres|postgres,template0,template1' ]]
```

This is the only pre-v2-generation path. The next operation is the canonical
initial installer. The same-generation forward installer remains unavailable
until the complete initial acceptance sequence has succeeded.

### Canonical generation-v2 installation

The canonical destination `/opt/ascendany/v2` must be absent. Run the installer from the protected reviewed checkout:

```bash
/absolute/reviewed-checkout/deploy/v2/scripts/install-v2-release.sh \
  --source /absolute/root-owned/ascendany-v2 \
  --manifest-sha256 "$(</root/ascendany-release-trust/0.2.0/manifest.sha256)" \
  --expected-purpose production
```

After the installer and staged acceptance succeed, write `stat -Lc '%d:%i' /opt/ascendany/v2` to `/root/ascendany-release-trust/0.2.0/installed.identity` with root-only mode `0400` and sync it. Keep that identity beside the independently transferred `manifest.sha256` and outside `/opt/ascendany`. A later replacement accepts this retained manifest digest and `DEVICE:INODE` pair as explicit installed-release trust inputs.

Then perform the following host setup in order:

1. Verify the sealed generation-retirement archive, the permanent `ascendany-api.service` and `ascendany-trainer-agent.service` masks, `MainPID=0` for both units, and no TCP listener on port `8000`. Do not retain an old-service rollback path on the production host.
2. Install `sysusers.d/ascendany-v2.conf` byte-for-byte at `/etc/sysusers.d/ascendany-v2.conf`; run `systemd-sysusers`.
3. Allocate non-overlapping `/etc/subuid` and `/etc/subgid` ranges for `ascendany-judge`.
4. Install `tmpfiles.d/ascendany-v2.conf` byte-for-byte at `/etc/tmpfiles.d/ascendany-v2.conf`; run `systemd-tmpfiles --create`.
5. Install release systemd units at `/etc/systemd/system`, polkit rules at `/etc/polkit-1/rules.d`, core non-secret application configuration at `/etc/ascendany/v2`, and the isolated catalog publisher config/credential namespace at `/etc/ascendany-catalog-publisher`, with the ownership and modes declared by file headers and validators.
6. Install the release-owned `40-read-only-smoke.conf` at `/etc/systemd/system/ascendanyd.service.d/40-read-only-smoke.conf` before starting v2.
7. Acquire, attest and install the exact PgBouncer and Cloudflared packages pinned by `config/fedora-runtime-packages.json`. Mask the package-owned `pgbouncer.service`.
8. Acquire and attest the locked judge OCI image with `acquire-judge-image.sh`, `preload-judge-image.sh`, and `attest-judge-image.sh`.
9. Run `systemctl daemon-reload`; `NeedDaemonReload=yes` blocks every phase gate.

### Fresh host boundary

Provisioning starts from one closed host/database state:

1. The retired `ascendany-api.service` and `ascendany-trainer-agent.service` are permanent `/dev/null` masks, inactive and `MainPID=0`; TCP port `8000` has no listener; the retired trainer OS identity, roots, processes, descriptors and container are absent. The production validator owns this retirement gate; the fresh database provisioner contains no legacy-service path.
2. The package-owned `pgbouncer.service` is masked and inactive.
3. The release-owned `ascendany-pgbouncer.service` is installed, disabled and inactive.
4. TCP ports `6432` and `18000` have no listeners, the reserved `ascendany-pgbouncer` container name is unused, and `/opt/ascendany/infra/pgbouncer` is absent.
5. `ascendany-postgres` uses the pinned PostgreSQL 17 image digest, exact command/restart/loopback-port contract, one `ascendany-postgres-data` volume, and only the validator's closed image/runtime environment set. It contains no AscendAny, trainer, bootstrap-secret or proxy environment. Its system identifier differs from the sealed retired cluster.
6. The fresh cluster's only non-system role is `postgres`; its only databases are `postgres`, `template0` and `template1`; its container-local peer DBA channel is available through the `postgres` OS identity. The v2 database, v2 roles, encrypted database credentials and provisioning receipt are absent.

The provisioner validates every precondition before its first mutation. It owns no service conversion, data conversion, recovery or reverse path.

### One-time fresh database and pool provisioning

Create five distinct root-owned mode-`0600` password files with no trailing newline:

```text
/run/ascendany-v2-provision/runtime_db_password
/run/ascendany-v2-provision/migrator_db_password
/run/ascendany-v2-provision/backup_db_password
/run/ascendany-v2-provision/restore_db_password
/run/ascendany-v2-provision/catalog_publisher_db_password
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

The final core encrypted credential set is:

```text
runtime_db_password.cred
migrator_db_password.cred
backup_db_password.cred
restore_db_password.cred
pgbouncer_userlist.cred
jwt_signing_private_key.cred
jwt_verification_public_key.cred
password_pepper.cred
cloudflare_tunnel_credentials.cred
```

These files live under `/etc/ascendany/credentials`, owned by `root:root`, mode `0400`. `jwt_signing_private_key.cred` contains one canonical PKCS#8 Ed25519 private-key PEM and is loaded only by `ascendanyd`; `jwt_verification_public_key.cred` contains its canonical PKIX public-key PEM and is the only JWT key loaded by the stopped publisher. Access tokens use EdDSA exclusively. The publisher has a separate closed namespace: `/etc/ascendany-catalog-publisher/catalog-publish.env` is `root:ascendany-catalog-publisher` mode `0640`; its `credentials/` directory is `root:root` mode `0700` and contains only `catalog_publisher_db_password.cred` as `root:root` mode `0400`. During a publication window the static publisher receives that database credential, the public verification key, and exactly two encrypted pending inputs: `catalog_publication_request.cred` and `admin_access_token.cred`. `/var/lib/ascendany-catalog-publisher/pending` is `root:root` mode `0700` and is empty outside that window. Optional feedback credentials use unique reviewed IDs and an exact generated systemd drop-in. No training credential is permitted.

Generate the JWT keypair once in a root-only transient directory and encrypt each capability separately:

```bash
install -d -o root -g root -m 0700 /run/ascendany-v2-jwt-keygen
openssl genpkey -algorithm ED25519 \
  -out /run/ascendany-v2-jwt-keygen/private.pem
openssl pkey \
  -in /run/ascendany-v2-jwt-keygen/private.pem \
  -pubout -out /run/ascendany-v2-jwt-keygen/public.pem
chmod 0400 /run/ascendany-v2-jwt-keygen/private.pem \
  /run/ascendany-v2-jwt-keygen/public.pem
systemd-creds encrypt --with-key=host --name=jwt_signing_private_key \
  /run/ascendany-v2-jwt-keygen/private.pem \
  /etc/ascendany/credentials/jwt_signing_private_key.cred
systemd-creds encrypt --with-key=host --name=jwt_verification_public_key \
  /run/ascendany-v2-jwt-keygen/public.pem \
  /etc/ascendany/credentials/jwt_verification_public_key.cred
chown root:root \
  /etc/ascendany/credentials/jwt_signing_private_key.cred \
  /etc/ascendany/credentials/jwt_verification_public_key.cred
chmod 0400 \
  /etc/ascendany/credentials/jwt_signing_private_key.cred \
  /etc/ascendany/credentials/jwt_verification_public_key.cred
rm -rf -- /run/ascendany-v2-jwt-keygen
```

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

Use the container-local `postgres` peer DBA channel for the two direct `psql` invocations. Migration readiness must report schema version 7.

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

Requirements: `ascendanyd.service` disabled/inactive, backup timer disabled/inactive, smoke drop-in installed, port 18000 unused, and Cloudflared active. The database must contain exactly the schema-v7 set of 54 base tables and 32 sequences; only the seven migration rows, zero-revision analytics singleton, and immutable migration-v4 achievement seed may be populated. Every other table is empty and every non-seed sequence is uncalled. The published artifact namespace, incoming/lock namespaces, catalog receipt namespace, backup root and acceptance/restore-evidence root are empty.

Create one temporary root-owned mode-`0600` `PGPASSFILE` containing only the `ascendanyd_login` entry, then run:

```bash
ASCENDANY_DEPLOYMENT_TRANSITION=initial \
ASCENDANY_VALIDATION_PHASE=staged \
PGPASSFILE=/run/ascendany-validation/runtime.pgpass \
  /opt/ascendany/v2/scripts/validate-production.sh
```

Remove the temporary file immediately after the gate. Staged validation performs release, model, database, role, credential-source, unit, package, pool, artifact and connector checks without starting the application. It also proves the exact `/opt/ascendany`, `/etc/ascendany`, credential and legacy-unit namespaces contain no Python/trainer generation state.

### 7.2 Read-only smoke before activation

Route public and shadow DNS as specified above, then start the still-disabled
service manually:

```bash
systemctl start ascendanyd.service
ASCENDANY_DEPLOYMENT_TRANSITION=initial \
ASCENDANY_VALIDATION_PHASE=smoke \
  /opt/ascendany/v2/scripts/validate-production.sh
systemctl stop ascendanyd.service
```

The gate requires exact health/version/capability responses, schema version 7,
`writesEnabled=false`, byte-identical loopback/public/shadow version responses,
and no business, durable-job or model-state mutation. The same
54-table/32-sequence canonical fresh-state and empty durable-namespace gate runs
again. `recommendation_model_releases`, `recommendation_model_head` and
`recommendation_model_activation_events` remain empty, and
`recommendation_model_release_ids_seq` remains uncalled. Read-only startup uses
`StagedReaderService`; recommendation reads return the explicit
`recommendation_model_inactive` domain result until the activation commit point.

### 7.3 Bootstrap model head H1

Keep the smoke drop-in installed and the HTTP service stopped. Register the
release-bound model first, bind bootstrap revision `1`, then validate the committed
binding with a fresh temporary root-owned mode-`0600` runtime `PGPASSFILE` under
the same contract as the staged gate. Remove the temporary file immediately
after validation.

```bash
systemctl stop ascendanyd.service
systemctl start ascendany-model-register.service
systemctl start ascendany-model-activate.service
ASCENDANY_DEPLOYMENT_TRANSITION=initial \
ASCENDANY_VALIDATION_PHASE=activation \
PGPASSFILE=/run/ascendany-validation/runtime.pgpass \
  /opt/ascendany/v2/scripts/validate-production.sh
```

The successful registration creates the immutable release row without changing
the head. The successful bootstrap binding creates head/event revision `1` with a NULL
catalog-publication reference. This initial-only H1 state supplies the immutable
current-model anchor used by the reviewed publication request. The static
one-shot returns to inactive/dead with a successful result.
`ascendanyd serve` requires this exact schema-v7 release/head binding whenever
writes are enabled and never creates or advances it. Recommendation reads return the explicit
`knowledge_catalog_unavailable` domain result until section 7.4 publishes the
catalog.

### 7.4 Isolated initialization, catalog publication and functional acceptance

All plaintext inputs in this section live below one root-owned mode-`0700`
acceptance directory. `snapshot_path` is the real exporter output from the
currently loaded Pintia problem set. It must already be a complete
`ascendany.pintia.snapshot.v2`. `admin_password_path` contains 12..128 unpadded
bytes without a trailing newline. `acceptance_student_number` names one unique
enrollable participant in that snapshot.

Create the one-time administrator credential, run the bootstrap unit, and retain
the protected plaintext password only until the release operator finishes its
login checks:

```bash
set -euo pipefail
umask 077

acceptance_dir=/root/ascendany-release-trust/0.2.0/initialization
snapshot_path="$acceptance_dir/pintia.snapshot.v2.json"
admin_password_path="$acceptance_dir/admin.password"
acceptance_student_number='REPLACE_WITH_EXACT_STUDENT_NUMBER'
student_username=release_acceptance
student_credentials="$acceptance_dir/student-credentials"

[[ -d "$acceptance_dir" && ! -L "$acceptance_dir" ]]
[[ "$(stat -Lc '%u:%g:%a' -- "$acceptance_dir")" == 0:0:700 ]]
[[ -f "$snapshot_path" && ! -L "$snapshot_path" ]]
[[ -f "$admin_password_path" && ! -L "$admin_password_path" ]]
[[ "$(stat -Lc '%u:%g:%a:%h' -- "$admin_password_path")" == 0:0:400:1 ]]

install -d -o root -g root -m 0700 /run/ascendany-admin-bootstrap-input
systemd-creds encrypt --with-key=host --name=admin_password \
  "$admin_password_path" \
  /run/ascendany-admin-bootstrap-input/admin_password.cred
chown root:root /run/ascendany-admin-bootstrap-input/admin_password.cred
chmod 0400 /run/ascendany-admin-bootstrap-input/admin_password.cred
systemctl start ascendany-admin-bootstrap.service
[[ ! -e /run/ascendany-admin-bootstrap-input/admin_password.cred ]]
```

Stop public ingress, remove the read-only drop-in, and start the write-enabled
application on loopback. Derive every release identity from the installed
manifest and execute only the manifest-bound operator bundle:

```bash
systemctl stop ascendany-cloudflared.service ascendanyd.service
rm -- /etc/systemd/system/ascendanyd.service.d/40-read-only-smoke.conf
systemctl daemon-reload
systemctl start ascendanyd.service

manifest=/opt/ascendany/v2/release-manifest.json
operator=/opt/ascendany/v2/operators/ascendany-production-initialize.mjs
catalog=/opt/ascendany/v2/models/recommendation-knowledge-catalog.json
target_version="$(jq -er '.version' "$manifest")"
target_commit="$(jq -er '.commit' "$manifest")"
target_epoch="$(jq -er '.sourceDateEpoch | select(type == "number" and floor == .)' "$manifest")"
target_build_time="$(date -u --date="@$target_epoch" '+%Y-%m-%dT%H:%M:%SZ')"
target_purpose="$(jq -er '.purpose' "$manifest")"
target_model_sha256="$(jq -er '.files[] | select(.path == "models/recommendation-model.json") | .sha256' "$manifest")"
target_catalog_sha256="$(jq -er '.files[] | select(.path == "models/recommendation-knowledge-catalog.json") | .sha256' "$manifest")"
catalog_credentials="$acceptance_dir/catalog-publication-credentials"
prepare_receipt="$acceptance_dir/prepare-receipt.json"

[[ ! -e "$catalog_credentials" && ! -e "$prepare_receipt" ]]
ASCENDANY_INITIALIZATION_DEPLOYMENT_KIND=initial \
ASCENDANY_INITIALIZATION_BASE_URL=http://127.0.0.1:18000/ \
ASCENDANY_INITIALIZATION_ORIGIN=https://ascendany.kkkzbh.cn \
ASCENDANY_INITIALIZATION_SNAPSHOT_PATH="$snapshot_path" \
ASCENDANY_INITIALIZATION_ADMIN_PASSWORD_FILE="$admin_password_path" \
ASCENDANY_INITIALIZATION_TARGET_APPLICATION_VERSION="$target_version" \
ASCENDANY_INITIALIZATION_TARGET_APPLICATION_COMMIT="$target_commit" \
ASCENDANY_INITIALIZATION_TARGET_APPLICATION_BUILD_TIME="$target_build_time" \
ASCENDANY_INITIALIZATION_EXPECTED_MODEL_PURPOSE="$target_purpose" \
ASCENDANY_INITIALIZATION_EXPECTED_MODEL_SHA256="$target_model_sha256" \
ASCENDANY_INITIALIZATION_EXPECTED_MODEL_HEAD_REVISION=2 \
ASCENDANY_INITIALIZATION_EXPECTED_CURRENT_MODEL_SHA256="$target_model_sha256" \
ASCENDANY_INITIALIZATION_EXPECTED_CURRENT_MODEL_HEAD_REVISION=1 \
ASCENDANY_INITIALIZATION_EXPECTED_CURRENT_CATALOG_HEAD_REVISION=0 \
ASCENDANY_INITIALIZATION_EXPECTED_CATALOG_HEAD_REVISION=1 \
ASCENDANY_INITIALIZATION_KNOWLEDGE_CATALOG_PATH="$catalog" \
ASCENDANY_INITIALIZATION_EXPECTED_CATALOG_SHA256="$target_catalog_sha256" \
ASCENDANY_INITIALIZATION_ACCEPTANCE_STUDENT_NUMBER="$acceptance_student_number" \
ASCENDANY_INITIALIZATION_CATALOG_CREDENTIAL_DIRECTORY_OUTPUT="$catalog_credentials" \
  /usr/bin/node-22 "$operator" prepare >"$prepare_receipt"
chmod 0400 "$prepare_receipt"
jq -e '.schema == "ascendany.production-initialization.prepare-receipt.v1"' \
  "$prepare_receipt" >/dev/null
```

`prepare` verifies the running release identity, authenticates the canonical
administrator, imports exactly the supplied snapshot, waits for the durable
import and analytics generation, and proves catalog coverage against the
current analytics manifest. It asks the online runtime to create one
short-lived, single-use authorization for the exact catalog/release intent,
then writes the server-returned canonical `catalog_publication_request` and the
administrator `admin_access_token` as mode-`0600` files in an exact root-only
credential directory. The request includes `authorizationId` and binds
configuration, analytics, current-model, target catalog/model and target
application identity. Enter the stopped-runtime publication window before the
access token and authorization expire.

Stop the application, restore the exact release smoke drop-in, restart the
Tunnel, encrypt both reviewed publisher inputs into their pending paths, delete
the plaintext directory, and run the static publisher once:

```bash
systemctl stop ascendanyd.service
install -o root -g root -m 0644 \
  /opt/ascendany/v2/systemd/ascendanyd.service.d/40-read-only-smoke.conf \
  /etc/systemd/system/ascendanyd.service.d/40-read-only-smoke.conf
systemctl daemon-reload
systemctl start ascendany-cloudflared.service

install -d -o root -g root -m 0700 /var/lib/ascendany-catalog-publisher/pending
systemd-creds encrypt --with-key=host --name=catalog_publication_request \
  "$catalog_credentials/catalog_publication_request" \
  /var/lib/ascendany-catalog-publisher/pending/catalog_publication_request.cred
systemd-creds encrypt --with-key=host --name=admin_access_token \
  "$catalog_credentials/admin_access_token" \
  /var/lib/ascendany-catalog-publisher/pending/admin_access_token.cred
chown root:root \
  /var/lib/ascendany-catalog-publisher/pending/catalog_publication_request.cred \
  /var/lib/ascendany-catalog-publisher/pending/admin_access_token.cred
chmod 0400 \
  /var/lib/ascendany-catalog-publisher/pending/catalog_publication_request.cred \
  /var/lib/ascendany-catalog-publisher/pending/admin_access_token.cred
rm -rf -- "$catalog_credentials"

receipts_before="$acceptance_dir/catalog-receipts.before"
receipts_after="$acceptance_dir/catalog-receipts.after"
find /var/lib/ascendany-catalog-publisher/receipts -mindepth 1 -maxdepth 1 \
  -type f -printf '%f\n' | LC_ALL=C sort >"$receipts_before"
systemctl start ascendany-catalog-publish.service
[[ ! -e /var/lib/ascendany-catalog-publisher/pending/catalog_publication_request.cred ]]
[[ ! -e /var/lib/ascendany-catalog-publisher/pending/admin_access_token.cred ]]
find /var/lib/ascendany-catalog-publisher/receipts -mindepth 1 -maxdepth 1 \
  -type f -printf '%f\n' | LC_ALL=C sort >"$receipts_after"
mapfile -t new_receipts < <(comm -13 "$receipts_before" "$receipts_after")
[[ "${#new_receipts[@]}" == 1 && "${new_receipts[0]}" =~ ^[1-9][0-9]*\.json$ ]]
catalog_publication_receipt="/var/lib/ascendany-catalog-publisher/receipts/${new_receipts[0]}"
jq -e \
  '.schema == "ascendany.knowledge_catalog.publication-receipt.v1"' \
  "$catalog_publication_receipt" >/dev/null
```

Create a fresh protected runtime `PGPASSFILE`, run the catalog commit gate, and
remove the file immediately. This gate proves the exact 26-field canonical
`ascendany.knowledge_catalog.publication-receipt.v1`, including `authorizationId`,
filesystem/DB publication-ID equality, target release/model/catalog/application
binding, analytics CAS, configuration mutation, actor/session and exact audit
payload. H1 remains current and atomically reserves the publication as its single
pending publication until the next binding transaction consumes it.

```bash
ASCENDANY_DEPLOYMENT_TRANSITION=initial \
ASCENDANY_VALIDATION_PHASE=catalog \
PGPASSFILE=/run/ascendany-validation/runtime.pgpass \
  /opt/ascendany/v2/scripts/validate-production.sh
```

Bind the exact publication into H2, remove the smoke drop-in, start the target application, and run `verify` through
loopback while the Tunnel remains active. The first run creates one persistent
root-only acceptance-student credential directory and proves single-use
enrollment, current analytics/leaderboard state, receipt/audit provenance and
online inference bound to the exact model, catalog and application identity.

```bash
systemctl start ascendany-model-activate.service
rm -- /etc/systemd/system/ascendanyd.service.d/40-read-only-smoke.conf
systemctl daemon-reload
systemctl start ascendanyd.service

verify_receipt="$acceptance_dir/verify-receipt.json"
[[ ! -e "$verify_receipt" ]]
ASCENDANY_INITIALIZATION_DEPLOYMENT_KIND=initial \
ASCENDANY_INITIALIZATION_BASE_URL=http://127.0.0.1:18000/ \
ASCENDANY_INITIALIZATION_ORIGIN=https://ascendany.kkkzbh.cn \
ASCENDANY_INITIALIZATION_SNAPSHOT_PATH="$snapshot_path" \
ASCENDANY_INITIALIZATION_ADMIN_PASSWORD_FILE="$admin_password_path" \
ASCENDANY_INITIALIZATION_TARGET_APPLICATION_VERSION="$target_version" \
ASCENDANY_INITIALIZATION_TARGET_APPLICATION_COMMIT="$target_commit" \
ASCENDANY_INITIALIZATION_TARGET_APPLICATION_BUILD_TIME="$target_build_time" \
ASCENDANY_INITIALIZATION_EXPECTED_MODEL_PURPOSE="$target_purpose" \
ASCENDANY_INITIALIZATION_EXPECTED_MODEL_SHA256="$target_model_sha256" \
ASCENDANY_INITIALIZATION_EXPECTED_MODEL_HEAD_REVISION=2 \
ASCENDANY_INITIALIZATION_EXPECTED_CURRENT_MODEL_SHA256="$target_model_sha256" \
ASCENDANY_INITIALIZATION_EXPECTED_CURRENT_MODEL_HEAD_REVISION=1 \
ASCENDANY_INITIALIZATION_EXPECTED_CURRENT_CATALOG_HEAD_REVISION=0 \
ASCENDANY_INITIALIZATION_EXPECTED_CATALOG_HEAD_REVISION=1 \
ASCENDANY_INITIALIZATION_KNOWLEDGE_CATALOG_PATH="$catalog" \
ASCENDANY_INITIALIZATION_EXPECTED_CATALOG_SHA256="$target_catalog_sha256" \
ASCENDANY_INITIALIZATION_ACCEPTANCE_STUDENT_NUMBER="$acceptance_student_number" \
ASCENDANY_INITIALIZATION_CATALOG_PUBLICATION_RECEIPT_PATH="$catalog_publication_receipt" \
ASCENDANY_INITIALIZATION_STUDENT_USERNAME="$student_username" \
ASCENDANY_INITIALIZATION_STUDENT_CREDENTIAL_DIRECTORY="$student_credentials" \
  /usr/bin/node-22 "$operator" verify >"$verify_receipt"
chmod 0400 "$verify_receipt"
jq -e '.schema == "ascendany.production-initialization.verify-receipt.v1"' \
  "$verify_receipt" >/dev/null
rm -f -- "$admin_password_path"
systemctl enable ascendanyd.service
```

The real snapshot import is the sole production import in this sequence. Schema
negative fixtures, duplicate identities, dangling references, partial
pagination, count/hash limits, all three import idempotency outcomes, SSE,
Judge and LSP lifecycle checks run against the same built release in the
mandatory disposable full-E2E gate before transfer. Production validation owns
the deployed route, process, unit, credential and database-role closures.

### 7.5 Backup and restore

Create the first schema-v7 backup, verify it, run the sole restore operator, then enable the timer:

```bash
systemctl start ascendany-backup.service
backup_id="$(find /var/backups/ascendany -mindepth 1 -maxdepth 1 -type d -printf '%f\n' | LC_ALL=C sort | tail -n 1)"
/opt/ascendany/v2/bin/ascendany-backup verify "$backup_id"
systemctl start "ascendany-restore-verify@${backup_id}.service"
systemctl enable --now ascendany-backup.timer
```

The restore unit must publish `/var/lib/ascendany-acceptance/restore-verify.json` only after dropping its scratch database and removing scratch credentials/artifacts. Evidence must bind the active release and newest schema-v7 backup and be at most 31 days old.

### 7.6 Final production gate

```bash
ASCENDANY_DEPLOYMENT_TRANSITION=initial \
ASCENDANY_VALIDATION_PHASE=production \
  /opt/ascendany/v2/scripts/validate-production.sh
```

Production requires the smoke drop-in absent, `ascendanyd` enabled/active, `writesEnabled=true`, public and shadow routes byte-identical to loopback v2, exact model artifact and database activation binding, a successful current backup/restore evidence pair, and a future backup timer elapse.

Remove any offline retirement evidence from the production runtime tree after this gate. Production retains only the v2 release, v2 database/artifacts, encrypted credentials, backups and acceptance evidence.

## 8. Forward recovery boundary

Provisioning and every release replacement are one-way. A failed initial
provision requires operator-reviewed cleanup through the explicit DBA boundary.
After initial activation, every repair is a strictly greater reviewed v2
release. Selecting an earlier trained model still requires a new release that
embeds that immutable artifact and records a new activation. No forward failure
authorizes restarting the retired release.

### 8.1 Prepare the target while the accepted release is live

Build the replacement at a strictly greater SemVer precedence and create an
independent manifest SHA-256 trust anchor. Complete every static gate in section
4 plus the disposable PostgreSQL 17/full-E2E acceptance against that exact
target before this section. The full E2E must execute the operator from its
installed test release. Preserve all gate logs under the target trust directory.

The live production release remains installed and serving while `prepare` runs.
Use the target bundle directly from the independently verified target release;
never build or execute it from source during deployment. Verify the target
manifest, operator bytes, and exact operator-only Node runtime first:

```bash
set -euo pipefail
umask 077

current_release=/opt/ascendany/v2
target_release=/absolute/root-owned/ascendany-v2-next
previous_trust=/root/ascendany-release-trust/0.2.0
target_trust=/root/ascendany-release-trust/0.2.1
cutover_dir="$target_trust/forward-initialization"
snapshot_path="$previous_trust/initialization/pintia.snapshot.v2.json"
student_credentials="$previous_trust/initialization/student-credentials"
admin_password_path="$cutover_dir/admin.password"
acceptance_student_number='REPLACE_WITH_EXACT_STUDENT_NUMBER'
student_username=release_acceptance

install -d -o root -g root -m 0700 "$cutover_dir"
[[ -f "$admin_password_path" && ! -L "$admin_password_path" ]]
[[ "$(stat -Lc '%u:%g:%a:%h' -- "$admin_password_path")" == 0:0:400:1 ]]
[[ "$(sha256sum "$target_release/release-manifest.json" | awk '{print $1}')" == \
   "$(<"$target_trust/manifest.sha256")" ]]

target_operator="$target_release/operators/ascendany-production-initialize.mjs"
operator_record="$(jq -er '.files[] | select(.path == "operators/ascendany-production-initialize.mjs") | [.sha256, .size, .mode] | @tsv' "$target_release/release-manifest.json")"
IFS=$'\t' read -r operator_sha operator_size operator_mode <<<"$operator_record"
[[ "$(stat -Lc '%u:%g:%a:%s:%h' -- "$target_operator")" == \
   "0:0:${operator_mode#0}:$operator_size:1" ]]
[[ "$(sha256sum "$target_operator" | awk '{print $1}')" == "$operator_sha" ]]
[[ "$(realpath -e /usr/bin/node-22)" == /usr/bin/node-22 ]]
[[ "$(stat -Lc '%u:%g:%a:%h' /usr/bin/node-22)" == 0:0:755:1 ]]
[[ "$(sha256sum /usr/bin/node-22 | awk '{print $1}')" == \
   7ed75caca3ed639ebde926277e43ed04c67de55bfece9d56bd752159d96368f0 ]]
[[ "$(rpm -q --qf '%{NAME}-%{VERSION}-%{RELEASE}.%{ARCH}' nodejs22)" == \
   nodejs22-22.22.2-3.fc44.x86_64 ]]
[[ "$(/usr/bin/node-22 --version)" == v22.22.2 ]]
/usr/bin/node-22 --check "$target_operator"
```

Create a temporary root-owned mode-`0600` runtime `PGPASSFILE` and capture the
accepted old model/catalog heads directly from the live database. Compare both
artifact digests with the accepted installed manifest. Remove the file after the
query. The following variables are the explicit current and target transition
contract:

```bash
current_manifest="$current_release/release-manifest.json"
target_manifest="$target_release/release-manifest.json"
state_row="$(PGPASSFILE=/run/ascendany-validation/runtime.pgpass \
  psql -X --no-psqlrc --no-password --set=ON_ERROR_STOP=1 \
    --tuples-only --no-align --field-separator='|' \
    'postgresql://ascendanyd_login@127.0.0.1:6432/ascendany_v2' <<'SQL'
SELECT head.head_revision,
       model.artifact_sha256,
       item.head_revision,
       version.document_sha256
FROM ascendany.recommendation_model_head AS head
JOIN ascendany.recommendation_model_releases AS model
  ON model.recommendation_model_release_id = head.current_release_id
JOIN ascendany.configuration_items AS item
  ON item.configuration_key = 'recommendation.catalog.active'
JOIN ascendany.configuration_versions AS version
  ON version.configuration_version_id = item.active_version_id
WHERE head.singleton;
SQL
)"
rm -f -- /run/ascendany-validation/runtime.pgpass
IFS='|' read -r current_model_head current_model_sha256 \
  current_catalog_head current_catalog_sha256 <<<"$state_row"
[[ "$current_model_head" =~ ^[1-9][0-9]*$ ]]
[[ "$current_catalog_head" =~ ^[1-9][0-9]*$ ]]
[[ "$current_model_sha256" == "$(jq -er '.files[] | select(.path == "models/recommendation-model.json") | .sha256' "$current_manifest")" ]]
[[ "$current_catalog_sha256" == "$(jq -er '.files[] | select(.path == "models/recommendation-knowledge-catalog.json") | .sha256' "$current_manifest")" ]]

current_version="$(jq -er '.version' "$current_manifest")"
current_commit="$(jq -er '.commit' "$current_manifest")"
current_epoch="$(jq -er '.sourceDateEpoch' "$current_manifest")"
current_build_time="$(date -u --date="@$current_epoch" '+%Y-%m-%dT%H:%M:%SZ')"
target_version="$(jq -er '.version' "$target_manifest")"
target_commit="$(jq -er '.commit' "$target_manifest")"
target_epoch="$(jq -er '.sourceDateEpoch' "$target_manifest")"
target_build_time="$(date -u --date="@$target_epoch" '+%Y-%m-%dT%H:%M:%SZ')"
target_purpose="$(jq -er '.purpose' "$target_manifest")"
target_model_sha256="$(jq -er '.files[] | select(.path == "models/recommendation-model.json") | .sha256' "$target_manifest")"
target_catalog_sha256="$(jq -er '.files[] | select(.path == "models/recommendation-knowledge-catalog.json") | .sha256' "$target_manifest")"
target_catalog="$target_release/models/recommendation-knowledge-catalog.json"
target_model_head="$((current_model_head + 1))"
target_catalog_head="$current_catalog_head"
if [[ "$target_catalog_sha256" != "$current_catalog_sha256" ]]; then
  target_catalog_head="$((current_catalog_head + 1))"
fi
```

Run the target operator against the live accepted application. Forward
`prepare` performs no import. It verifies the retained real snapshot/exam,
analytics manifest, prior model/catalog heads, target catalog coverage, current
application identity and target release intent, then asks the online runtime to
create one short-lived, single-use authorization bound to that intent and the
administrator access token's expiry. It writes the server-returned immutable
publication request plus that token below the root-only cutover directory:

```bash
catalog_credentials="$cutover_dir/catalog-publication-credentials"
prepare_receipt="$cutover_dir/prepare-receipt.json"
[[ ! -e "$catalog_credentials" && ! -e "$prepare_receipt" ]]
ASCENDANY_INITIALIZATION_DEPLOYMENT_KIND=forward \
ASCENDANY_INITIALIZATION_BASE_URL=http://127.0.0.1:18000/ \
ASCENDANY_INITIALIZATION_ORIGIN=https://ascendany.kkkzbh.cn \
ASCENDANY_INITIALIZATION_SNAPSHOT_PATH="$snapshot_path" \
ASCENDANY_INITIALIZATION_ADMIN_PASSWORD_FILE="$admin_password_path" \
ASCENDANY_INITIALIZATION_TARGET_APPLICATION_VERSION="$target_version" \
ASCENDANY_INITIALIZATION_TARGET_APPLICATION_COMMIT="$target_commit" \
ASCENDANY_INITIALIZATION_TARGET_APPLICATION_BUILD_TIME="$target_build_time" \
ASCENDANY_INITIALIZATION_CURRENT_APPLICATION_VERSION="$current_version" \
ASCENDANY_INITIALIZATION_CURRENT_APPLICATION_COMMIT="$current_commit" \
ASCENDANY_INITIALIZATION_CURRENT_APPLICATION_BUILD_TIME="$current_build_time" \
ASCENDANY_INITIALIZATION_EXPECTED_MODEL_PURPOSE="$target_purpose" \
ASCENDANY_INITIALIZATION_EXPECTED_MODEL_SHA256="$target_model_sha256" \
ASCENDANY_INITIALIZATION_EXPECTED_MODEL_HEAD_REVISION="$target_model_head" \
ASCENDANY_INITIALIZATION_EXPECTED_CURRENT_MODEL_SHA256="$current_model_sha256" \
ASCENDANY_INITIALIZATION_EXPECTED_CURRENT_MODEL_HEAD_REVISION="$current_model_head" \
ASCENDANY_INITIALIZATION_EXPECTED_CURRENT_CATALOG_HEAD_REVISION="$current_catalog_head" \
ASCENDANY_INITIALIZATION_EXPECTED_CATALOG_HEAD_REVISION="$target_catalog_head" \
ASCENDANY_INITIALIZATION_EXPECTED_CURRENT_CATALOG_SHA256="$current_catalog_sha256" \
ASCENDANY_INITIALIZATION_KNOWLEDGE_CATALOG_PATH="$target_catalog" \
ASCENDANY_INITIALIZATION_EXPECTED_CATALOG_SHA256="$target_catalog_sha256" \
ASCENDANY_INITIALIZATION_ACCEPTANCE_STUDENT_NUMBER="$acceptance_student_number" \
ASCENDANY_INITIALIZATION_CATALOG_CREDENTIAL_DIRECTORY_OUTPUT="$catalog_credentials" \
  /usr/bin/node-22 "$target_operator" prepare >"$prepare_receipt"
chmod 0400 "$prepare_receipt"
jq -e '.schema == "ascendany.production-initialization.prepare-receipt.v1" and .deploymentKind == "forward"' \
  "$prepare_receipt" >/dev/null
```

The request is immutable and the access token remains bound to the issuing
administrator session and expiry. Both exist only in the protected plaintext
directory and their subsequent encrypted pending credentials.

### 8.2 Unique quiesced install, migration, staged gate and smoke

Enter one downtime window. Disable the application/timer, stop every fixed and
instantiated release consumer, and reset completed/failed static units. Include
the model registrar and catalog publisher in the closed consumer set:

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
  ascendany-model-register.service \
  ascendany-model-activate.service \
  ascendany-catalog-publish.service \
  ascendany-admin-bootstrap.service \
  ascendany-backup.service \
  ascendany-cloudflared.service \
  ascendany-migrate.service \
  ascendany-pgbouncer.service
systemctl reset-failed \
  ascendanyd.service \
  ascendany-model-register.service \
  ascendany-model-activate.service \
  ascendany-catalog-publish.service \
  ascendany-admin-bootstrap.service \
  ascendany-backup.service \
  ascendany-backup.timer \
  ascendany-cloudflared.service \
  ascendany-migrate.service \
  ascendany-pgbouncer.service
```

Require no `.v2.installing.*` or `.v2.removing.*` entry, then run the protected
installer with both trust boundaries explicit:

```bash
/absolute/reviewed-checkout/deploy/v2/scripts/install-v2-release.sh \
  --source "$target_release" \
  --manifest-sha256 "$(<"$target_trust/manifest.sha256")" \
  --replace-installed-manifest-sha256 "$(<"$previous_trust/manifest.sha256")" \
  --replace-installed-identity "$(<"$previous_trust/installed.identity")" \
  --expected-purpose production
```

Replacement is explicit. The installer rejects a missing target, target identity
mismatch, installed manifest trust mismatch, installed tree drift, equal
manifest, non-advancing SemVer, purpose drift, concurrent installer, pre-existing
private entry, mount boundary, closed-tree integrity failure, or a non-quiesced
consumer. It verifies the old tree before staging/commit and the new source,
staged tree, model/catalog/operator contract before commit. Its quiescence gate
runs before staging, immediately before namespace exchange, and immediately
before retired-tree removal.

The namespace commit is one Linux `renameat2(..., RENAME_EXCHANGE)` executed by
the manifest-bound Go helper. It binds both directory identities, fsyncs the
parent around the exchange and reports every post-exchange failure as committed.
The retired tree is moved with `RENAME_NOREPLACE`, removed through anchored
directory descriptors and fsynced. Success leaves no old tree or private install
name. An identity/namespace race fails directly. A pre-exchange failure retains
the accepted installed tree. A post-exchange failure keeps downtime and is
resolved through the resulting forward state; no reverse exchange or old-release
restart is permitted.

Publish the replacement's release-owned host files exactly as in section 5,
install its smoke drop-in, restore PgBouncer/Tunnel, and apply migrations. Keep
the application and backup timer disabled/inactive:

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

Create a fresh protected runtime `PGPASSFILE`. The forward staged gate verifies
the installed target, exact Node/operator bytes, retained administrator/business
state, prior active model/catalog and prior backup/restore evidence. It emits
four anchors only after every check passes:

```bash
forward_staged_log="$target_trust/forward-staged.log"
forward_staged_state="$target_trust/forward-staged-state.env"
ASCENDANY_DEPLOYMENT_TRANSITION=forward \
ASCENDANY_VALIDATION_PHASE=staged \
PGPASSFILE=/run/ascendany-validation/runtime.pgpass \
  /opt/ascendany/v2/scripts/validate-production.sh | tee "$forward_staged_log"
awk '/^ASCENDANY_FORWARD_(DATABASE_FINGERPRINT_SHA256|BUSINESS_FINGERPRINT_SHA256|MODEL_HEAD_REVISION|MODEL_ARTIFACT_SHA256)=/' \
  "$forward_staged_log" >"$forward_staged_state"
[[ "$(wc -l <"$forward_staged_state")" == 4 ]]
chmod 0400 "$forward_staged_log" "$forward_staged_state"
sync "$forward_staged_log" "$forward_staged_state"
set -a
. "$forward_staged_state"
set +a
```

The full fingerprint binds every AscendAny base table and sequence. The business
fingerprint excludes only the three model activation tables and the model
release-ID sequence. The model anchors bind the exact retained prior head and
artifact. Start the disabled service in read-only mode and consume all four:

```bash
systemctl start ascendanyd.service
ASCENDANY_DEPLOYMENT_TRANSITION=forward \
ASCENDANY_VALIDATION_PHASE=smoke \
ASCENDANY_FORWARD_DATABASE_FINGERPRINT_SHA256="$ASCENDANY_FORWARD_DATABASE_FINGERPRINT_SHA256" \
ASCENDANY_FORWARD_BUSINESS_FINGERPRINT_SHA256="$ASCENDANY_FORWARD_BUSINESS_FINGERPRINT_SHA256" \
ASCENDANY_FORWARD_MODEL_HEAD_REVISION="$ASCENDANY_FORWARD_MODEL_HEAD_REVISION" \
ASCENDANY_FORWARD_MODEL_ARTIFACT_SHA256="$ASCENDANY_FORWARD_MODEL_ARTIFACT_SHA256" \
  /opt/ascendany/v2/scripts/validate-production.sh
systemctl stop ascendanyd.service
```

Smoke requires `writesEnabled=false`, byte-exact full/business fingerprints and
the retained model anchors. Representative mutation probes return
`503 writes_disabled`; the Go route contract proves every state-changing route
reaches no mutation service.

### 8.3 Register, publish, activate and verify the target

Register the target model while the prior head remains active. Encrypt both
reviewed publisher inputs only after smoke succeeds, delete their plaintext
directory, and publish exactly once:

```bash
systemctl start ascendany-model-register.service

install -d -o root -g root -m 0700 /var/lib/ascendany-catalog-publisher/pending
systemd-creds encrypt --with-key=host --name=catalog_publication_request \
  "$catalog_credentials/catalog_publication_request" \
  /var/lib/ascendany-catalog-publisher/pending/catalog_publication_request.cred
systemd-creds encrypt --with-key=host --name=admin_access_token \
  "$catalog_credentials/admin_access_token" \
  /var/lib/ascendany-catalog-publisher/pending/admin_access_token.cred
chown root:root \
  /var/lib/ascendany-catalog-publisher/pending/catalog_publication_request.cred \
  /var/lib/ascendany-catalog-publisher/pending/admin_access_token.cred
chmod 0400 \
  /var/lib/ascendany-catalog-publisher/pending/catalog_publication_request.cred \
  /var/lib/ascendany-catalog-publisher/pending/admin_access_token.cred
rm -rf -- "$catalog_credentials"

receipts_before="$cutover_dir/catalog-receipts.before"
receipts_after="$cutover_dir/catalog-receipts.after"
find /var/lib/ascendany-catalog-publisher/receipts -mindepth 1 -maxdepth 1 \
  -type f -printf '%f\n' | LC_ALL=C sort >"$receipts_before"
systemctl start ascendany-catalog-publish.service
[[ ! -e /var/lib/ascendany-catalog-publisher/pending/catalog_publication_request.cred ]]
[[ ! -e /var/lib/ascendany-catalog-publisher/pending/admin_access_token.cred ]]
find /var/lib/ascendany-catalog-publisher/receipts -mindepth 1 -maxdepth 1 \
  -type f -printf '%f\n' | LC_ALL=C sort >"$receipts_after"
mapfile -t new_receipts < <(comm -13 "$receipts_before" "$receipts_after")
[[ "${#new_receipts[@]}" == 1 && "${new_receipts[0]}" =~ ^[1-9][0-9]*\.json$ ]]
catalog_publication_receipt="/var/lib/ascendany-catalog-publisher/receipts/${new_receipts[0]}"
jq -e \
  '.schema == "ascendany.knowledge_catalog.publication-receipt.v1"' \
  "$catalog_publication_receipt" >/dev/null
```

Run the catalog gate against the staged anchors. It requires a new immutable
publication and receipt even when the target catalog bytes equal the current
catalog and `configurationMutated=false`. It proves the prior model head is
unchanged and emits the four post-catalog anchors. Full and business
fingerprints must both advance because publication/audit state is immutable:

```bash
forward_catalog_log="$target_trust/forward-catalog.log"
forward_catalog_state="$target_trust/forward-post-catalog-state.env"
staged_model_head="$ASCENDANY_FORWARD_MODEL_HEAD_REVISION"
staged_model_sha256="$ASCENDANY_FORWARD_MODEL_ARTIFACT_SHA256"
ASCENDANY_DEPLOYMENT_TRANSITION=forward \
ASCENDANY_VALIDATION_PHASE=catalog \
ASCENDANY_FORWARD_DATABASE_FINGERPRINT_SHA256="$ASCENDANY_FORWARD_DATABASE_FINGERPRINT_SHA256" \
ASCENDANY_FORWARD_BUSINESS_FINGERPRINT_SHA256="$ASCENDANY_FORWARD_BUSINESS_FINGERPRINT_SHA256" \
ASCENDANY_FORWARD_MODEL_HEAD_REVISION="$ASCENDANY_FORWARD_MODEL_HEAD_REVISION" \
ASCENDANY_FORWARD_MODEL_ARTIFACT_SHA256="$ASCENDANY_FORWARD_MODEL_ARTIFACT_SHA256" \
PGPASSFILE=/run/ascendany-validation/runtime.pgpass \
  /opt/ascendany/v2/scripts/validate-production.sh | tee "$forward_catalog_log"
awk '/^ASCENDANY_FORWARD_(DATABASE_FINGERPRINT_SHA256|BUSINESS_FINGERPRINT_SHA256|MODEL_HEAD_REVISION|MODEL_ARTIFACT_SHA256)=/' \
  "$forward_catalog_log" >"$forward_catalog_state"
[[ "$(wc -l <"$forward_catalog_state")" == 4 ]]
chmod 0400 "$forward_catalog_log" "$forward_catalog_state"
sync "$forward_catalog_log" "$forward_catalog_state"
set -a
. "$forward_catalog_state"
set +a
[[ "$ASCENDANY_FORWARD_MODEL_HEAD_REVISION" == "$staged_model_head" ]]
[[ "$ASCENDANY_FORWARD_MODEL_ARTIFACT_SHA256" == "$staged_model_sha256" ]]
```

Activate the target while HTTP remains stopped. Activation must consume this
exact target publication and advance the model head by one while preserving the
post-catalog business fingerprint:

```bash
systemctl start ascendany-model-activate.service
ASCENDANY_DEPLOYMENT_TRANSITION=forward \
ASCENDANY_VALIDATION_PHASE=activation \
ASCENDANY_FORWARD_DATABASE_FINGERPRINT_SHA256="$ASCENDANY_FORWARD_DATABASE_FINGERPRINT_SHA256" \
ASCENDANY_FORWARD_BUSINESS_FINGERPRINT_SHA256="$ASCENDANY_FORWARD_BUSINESS_FINGERPRINT_SHA256" \
ASCENDANY_FORWARD_MODEL_HEAD_REVISION="$ASCENDANY_FORWARD_MODEL_HEAD_REVISION" \
ASCENDANY_FORWARD_MODEL_ARTIFACT_SHA256="$ASCENDANY_FORWARD_MODEL_ARTIFACT_SHA256" \
PGPASSFILE=/run/ascendany-validation/runtime.pgpass \
  /opt/ascendany/v2/scripts/validate-production.sh
```

Remove the protected `PGPASSFILE`, enable writes, and run the installed target
operator's forward verification with the new publication receipt and the
persistent initial acceptance-student credentials:

```bash
rm -- /etc/systemd/system/ascendanyd.service.d/40-read-only-smoke.conf
systemctl daemon-reload
systemctl enable --now ascendanyd.service

installed_operator=/opt/ascendany/v2/operators/ascendany-production-initialize.mjs
verify_receipt="$cutover_dir/verify-receipt.json"
[[ ! -e "$verify_receipt" ]]
ASCENDANY_INITIALIZATION_DEPLOYMENT_KIND=forward \
ASCENDANY_INITIALIZATION_BASE_URL=http://127.0.0.1:18000/ \
ASCENDANY_INITIALIZATION_ORIGIN=https://ascendany.kkkzbh.cn \
ASCENDANY_INITIALIZATION_SNAPSHOT_PATH="$snapshot_path" \
ASCENDANY_INITIALIZATION_ADMIN_PASSWORD_FILE="$admin_password_path" \
ASCENDANY_INITIALIZATION_TARGET_APPLICATION_VERSION="$target_version" \
ASCENDANY_INITIALIZATION_TARGET_APPLICATION_COMMIT="$target_commit" \
ASCENDANY_INITIALIZATION_TARGET_APPLICATION_BUILD_TIME="$target_build_time" \
ASCENDANY_INITIALIZATION_CURRENT_APPLICATION_VERSION="$current_version" \
ASCENDANY_INITIALIZATION_CURRENT_APPLICATION_COMMIT="$current_commit" \
ASCENDANY_INITIALIZATION_CURRENT_APPLICATION_BUILD_TIME="$current_build_time" \
ASCENDANY_INITIALIZATION_EXPECTED_MODEL_PURPOSE="$target_purpose" \
ASCENDANY_INITIALIZATION_EXPECTED_MODEL_SHA256="$target_model_sha256" \
ASCENDANY_INITIALIZATION_EXPECTED_MODEL_HEAD_REVISION="$target_model_head" \
ASCENDANY_INITIALIZATION_EXPECTED_CURRENT_MODEL_SHA256="$current_model_sha256" \
ASCENDANY_INITIALIZATION_EXPECTED_CURRENT_MODEL_HEAD_REVISION="$current_model_head" \
ASCENDANY_INITIALIZATION_EXPECTED_CURRENT_CATALOG_HEAD_REVISION="$current_catalog_head" \
ASCENDANY_INITIALIZATION_EXPECTED_CATALOG_HEAD_REVISION="$target_catalog_head" \
ASCENDANY_INITIALIZATION_EXPECTED_CURRENT_CATALOG_SHA256="$current_catalog_sha256" \
ASCENDANY_INITIALIZATION_KNOWLEDGE_CATALOG_PATH=/opt/ascendany/v2/models/recommendation-knowledge-catalog.json \
ASCENDANY_INITIALIZATION_EXPECTED_CATALOG_SHA256="$target_catalog_sha256" \
ASCENDANY_INITIALIZATION_ACCEPTANCE_STUDENT_NUMBER="$acceptance_student_number" \
ASCENDANY_INITIALIZATION_CATALOG_PUBLICATION_RECEIPT_PATH="$catalog_publication_receipt" \
ASCENDANY_INITIALIZATION_STUDENT_USERNAME="$student_username" \
ASCENDANY_INITIALIZATION_STUDENT_CREDENTIAL_DIRECTORY="$student_credentials" \
  /usr/bin/node-22 "$installed_operator" verify >"$verify_receipt"
chmod 0400 "$verify_receipt"
jq -e '.schema == "ascendany.production-initialization.verify-receipt.v1" and .deploymentKind == "forward"' \
  "$verify_receipt" >/dev/null
rm -f -- "$admin_password_path"
```

Create and restore-verify a target-bound backup, enable the timer, and run the
production gate with the post-catalog prior-model anchors:

```bash
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
ASCENDANY_FORWARD_MODEL_ARTIFACT_SHA256="$ASCENDANY_FORWARD_MODEL_ARTIFACT_SHA256" \
  /opt/ascendany/v2/scripts/validate-production.sh
```

The production gate requires the new immutable release/model activation, its
consumed catalog publication, matching fixed-key catalog, new backup/restore
evidence and active future timer. Live business/durable-job state may advance
after `ascendanyd` starts, so production does not compare live fingerprints with
the stopped-runtime anchors. Sync the new installed manifest/identity trust
record and all operator/gate receipts after success.

## 9. Release blockers

Any item below blocks deployment:

- uncommitted or unreviewed release input;
- missing independent manifest or model SHA-256 trust anchor;
- any model-construction source, executable, runtime, unit, credential or accelerator payload in the release;
- release closed-set, ownership, mode, path, mount, size or hash drift;
- schema version other than 7 or migration hash drift;
- model semantic, golden-vector, release-manifest or DB activation drift;
- missing release-owned knowledge-catalog artifact or missing isolated catalog-publication operator phase;
- plaintext secret, password in a database URL, undeclared systemd credential or manager environment drift;
- public/shadow/global-404 ingress drift or any additional AscendAny ingress rule;
- partial Pintia pagination or malformed snapshot acceptance;
- Judge/LSP database credential or network capability;
- missing current backup/restore verification evidence.

`OJ_JUDGE_CONTRACT.md` and `LSP_CONTROL_CONTRACT.md` define the worker protocols. `doc/重写v2架构与验收.md` defines the complete architecture and final product-level acceptance boundary.
