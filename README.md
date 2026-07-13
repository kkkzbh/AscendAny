# AscendAny

English | [简体中文](README.zh-CN.md)

<p align="center">
  <img src="image/LOGO_SHRIND.png" alt="AscendAny Logo" width="96" />
</p>

<p align="center">
  <strong>Student Ability Analytics Platform</strong>
</p>

AscendAny turns complete Pintia programming problem-set snapshots into traceable ability profiles, ratings, achievements, leaderboards, exam analyses, and personalized learning recommendations. Web, Desktop, and Mobile share the core student capabilities for accounts, profiles, exams, AI chat, and recommendations, while each client provides platform-specific interactions. Administrators use a separate Import Console for imports, accounts, configuration, audits, and recommendation knowledge-catalog authoring.

## v2 architecture

| Scope | Sole owner |
| --- | --- |
| Public HTTP, authentication, business transactions, durable jobs, SSE/WebSocket | Go `ascendanyd` |
| PostgreSQL migrations | Go `ascendany-migrate` |
| Backup, verification, and restore rehearsal | Go `ascendany-backup` |
| Isolated OJ and C++ LSP | Go `ascendany-judge`, `ascendany-lsp` |
| Web, Desktop, Mobile, Import Console, product site | TypeScript |
| Pintia data capture | TypeScript Manifest V3 browser extension |
| Recommendation artifact verification and online inference | Go `ascendany-model`, `ascendanyd` |

Production receives one externally trained, immutable `ascendany.recommendation.inference-model.v1` artifact at release-build time. The release builder verifies its exact SHA-256, closed contract, feature schema, parameter digests, and golden vectors. `ascendanyd` binds that artifact to an immutable database model release and performs inference in Go. Training is a separate future module and is outside this repository, production release, systemd units, credentials, HTTP API, and database roles.

## Data boundary

- v2 starts from an empty PostgreSQL database and does not migrate legacy accounts or business data.
- `ascendany.pintia.snapshot.v2` is the only accepted exam format.
- The browser extension reads official Pintia page APIs from the user's current authenticated problem-set tab and exports one complete snapshot JSON document.
- Import Console streams the snapshot to Go; the backend performs SHA-256 verification, strict schema and semantic validation, idempotent persistence, analytics generation, and ordered SSE delivery.
- Every snapshot, analytics generation, recommendation model, and configuration version retains immutable provenance.

## Product capabilities

- Enrollment claim, login, refresh, logout, profile, session revocation, and role authorization.
- Five ability dimensions, rating history, achievements, leaderboard, exam catalog, and exam analysis.
- Durable AI chat, automatic analysis, audited note tools, and resumable event streams.
- Fresh and unavailable recommendation states with learning paths, evidence, immutable model provenance, and knowledge detail.
- OJ run/submit and clangd LSP, with execution workers holding no database credential.
- Pintia v2 import, job history, failure diagnostics, and reconnect-safe ordered events.
- Account, student, audit, prompt/model configuration, model-connection probe, and recommendation knowledge-catalog administration.

## Repository entry points

| Path | Contents |
| --- | --- |
| `backend/` | Go services, workers, CLIs, migrations, and domain tests. |
| `apps/web/` | Student Web application. |
| `apps/desktop/` | Electron student application. |
| `apps/mobile/` | Capacitor mobile application. |
| `apps/import-console/` | Administrator console. |
| `apps/site/` | Product website. |
| `packages/sdk/` | The only TypeScript SDK, generated from the final OpenAPI contract. |
| `tools/pintia-exporter-extension/` | Pintia snapshot v2 Chrome extension. |
| `contracts/` | OpenAPI, Pintia snapshot v2, and external recommendation-model contracts and fixtures. |
| `deploy/v2/` | systemd, privilege, configuration, and production acceptance contracts. |
| `doc/重写v2架构与验收.md` | v2 ownership, data flow, cleanup scope, and acceptance gates. |

## Local verification

The repository expects Go 1.26, Node.js 22, pnpm 9.15.4, and PostgreSQL 17 for integration tests.

```bash
cd backend
go test ./...
go vet ./...

cd ..
pnpm install --frozen-lockfile
pnpm --filter @ascendany/sdk check
pnpm --filter @ascendany/pintia-exporter check
pnpm --filter @ascendany/web check
pnpm --filter @ascendany/mobile check
pnpm --filter @ascendany/import-console check
pnpm --filter @ascendany/desktop test
pnpm --filter @ascendany/desktop build
```

### Rootless PostgreSQL rehearsal

The disposable integration environment uses the digest-pinned PostgreSQL 17
image and the release-locked native Fedora 44 x86_64 PgBouncer 1.25.2 RPM.
PgBouncer derives its temporary configuration and HBA rules from the production
release, receives only SCRAM verifiers in the private runtime tree, and binds to
loopback. Pull the PostgreSQL image explicitly if it is absent; the rehearsal
never pulls or starts a PgBouncer image.
After each disposable role-password reset, the integration runner publishes the
exact v2 capability-role SCRAM verifier set with same-directory fsync and atomic
rename, then issues explicit PgBouncer `RELOAD` and database `RECONNECT` commands.

```bash
tools/run-v2-postgres-podman-rehearsal.sh \
  --confirm-reset drop-disposable-ascendany-v2
```

The default input is the sanitized complete Pintia fixture. Exercise a real
export with an absolute path and select different free loopback ports when the
defaults (`55432` and `56432`) are occupied:

```bash
tools/run-v2-postgres-podman-rehearsal.sh \
  --confirm-reset drop-disposable-ascendany-v2 \
  --snapshot /absolute/path/to/ascendany-pintia-snapshot.json \
  --direct-port 55433 \
  --pgbouncer-port 56433
```

The confirmation authorizes resets only inside the newly created disposable
cluster. The exit trap terminates the native PgBouncer child, removes exactly
the labeled rehearsal pod and its temporary credential directory, then verifies
that pre-existing Podman container and pod identities are unchanged.

The independent backup/restore rehearsal defaults to the same committed
sanitized snapshot, validates it through the production Go Pintia validator,
and exercises the real create, verify, restore-verify, ownership, ACL, and
cleanup paths:

```bash
tools/run-v2-backup-restore-podman-rehearsal.sh \
  --confirm-reset drop-disposable-ascendany-v2-backup-restore \
  --recommendation-model /absolute/canonical/recommendation-model.json \
  --recommendation-model-sha256 64_lowercase_hex
```

Pass `--snapshot /absolute/canonical/snapshot.json` to rehearse a protected
real export without changing the script or repository. The model must be an
externally trained, reviewed artifact; the rehearsal verifies and binds it for
Go inference and does not execute training.

The release-to-restore acceptance path, its guarded local invocation, and the
separate fail-closed real Judge/LSP sandbox gate are documented in
[AscendAny v2 full E2E](doc/v2-full-e2e.md).

See [deploy/v2/README.md](deploy/v2/README.md) for the production structure, credential boundary, and install/migrate/bootstrap/import/backup/restore sequence. The complete acceptance definition is [v2 rewrite architecture and acceptance](doc/重写v2架构与验收.md).
