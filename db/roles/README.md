# PostgreSQL v2 role boundary

`001_v2_roles.sql` owns the complete PostgreSQL 17 role and ACL contract for
`ascendany_v2`. It creates five `NOLOGIN` capability roles and four passwordless
login principals:

| Capability | Login principal | Scope |
| --- | --- | --- |
| `ascendany_database_owner` | none | Owns only the production database. It has no membership edges. |
| `ascendany_owner` | none | Owns the application schema, relations, routines, and types. It has no database ACL. |
| `ascendany_runtime` | `ascendanyd_login` | Reviewed DML and sequence access through PgBouncer. No DDL. |
| `ascendany_migrator` | `ascendany_migrator_login` | Explicit `SET ROLE ascendany_owner` bridge for embedded migrations. |
| `ascendany_backup` | `ascendany_backup_login` | Read-only direct PostgreSQL access for `pg_dump`. |
| `ascendany_owner` | `ascendany_restore_login` | Explicit `SET ROLE` during restore into a scratch database. |

`ascendany_restore_login` is the only managed role with `CREATEDB`. That flag
owns the deterministic scratch-database create/drop lifecycle required by the
restore operator. It is not a service identity, has `NOINHERIT`, receives no
direct ACL on `ascendany_v2`, and cannot connect to the production database.
Its encrypted credential is root-owned and is loaded only by the restore
operation.

Production database ownership is held by `ascendany_database_owner`. This role
has `NOLOGIN`, `NOINHERIT`, no database password, no membership edges, and no
cluster capability flags. Migrator and restore identities can still explicitly
assume `ascendany_owner` for schema/object ownership, while that role has no
ability to drop `ascendany_v2` from a maintenance database.

The fixed maintenance connection uses database `postgres`. The bootstrap
removes every direct database privilege from `ascendany_restore_login` there
and grants exactly `CONNECT`; the verifier checks that direct ACL independently
of the cluster's PUBLIC policy. This gives the restore operator a deterministic
place to create and drop its isolated scratch database while preserving zero
access to `ascendany_v2`.

The five membership edges include exact PostgreSQL 17 `ADMIN`, `INHERIT`, and
`SET` options. The bootstrap deletes every other edge touching a managed role,
resets every managed `rolconfig`, closes all direct database/schema/relation/
column/sequence/routine/type/default ACL drift, and reconstructs the minimum
grants. It preserves password hashes because credential provisioning is an
independent administration boundary.

Create an empty database named `ascendany_v2` from `template0`, then apply the
bootstrap before and after the embedded schema migrations. The first pass
creates roles and owner default privileges. The second pass closes concrete
ACLs, including PostgreSQL table row types, which do not accept default type
ACLs. An idempotent rerun is accepted only for an empty schema or the exact
embedded schema-v5 migration history. A non-empty unknown schema fails
directly.

```bash
createdb --template=template0 ascendany_v2
psql -X -v ON_ERROR_STOP=1 --dbname=ascendany_v2 -f db/roles/001_v2_roles.sql
# Provision the login credentials, then apply the embedded migrations.
systemctl start ascendany-migrate.service
psql -X -v ON_ERROR_STOP=1 --dbname=ascendany_v2 -f db/roles/001_v2_roles.sql
psql -X -v ON_ERROR_STOP=1 --dbname=ascendany_v2 -f db/roles/verify_v2_roles.sql
```

The application connection uses `ascendanyd_login` on PgBouncer port `6432`.
Migration, backup, and restore administration use direct PostgreSQL port
`5432`. `verify_v2_roles.sql` compares catalog rows and ACL entries with exact
sets, including database ownership, both schemas, every managed membership
option, owner-only routines, closed type usage, and five default-ACL rows.
