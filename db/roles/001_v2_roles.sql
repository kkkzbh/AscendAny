\set ON_ERROR_STOP on

BEGIN;

-- The bootstrap has two valid entry states: a fresh template0 database, or
-- the exact embedded schema-v7 history. The second state makes ACL repair
-- idempotent while preserving one closed v2 schema identity.
DO $database_boundary$
DECLARE
    target_schema oid;
    actual_history text[];
    expected_history constant text[] := ARRAY[
        '1:fresh_schema:0cffdb00acefd37c049a654bad76d8fac79727ed7c54cc3fa9234d54964ce0cf',
        '2:product_domains:1762304608ed3f93d62c01ad494a2b6110b07737cc652f38a2581392985fdd36',
        '3:recommendation_catalog_contract:6fa4a81fbe3440fc4b149a5b77d6c3860031e285bafef50b5a881e8783f36267',
        '4:achievement_rules:3242ddfbdee0911d961ebe0f46237f6e2b8a6e7c5e09cf1d94f6ae98c4caaccb',
        '5:auto_analysis_once:40fed038bc7773f45e940de2880ca18427573e10555937afa202e684aecdaa17',
        '6:inference_model_runtime:330bd7bebdd6e67572a76fcb0c1e84c897df2a766f6e821312c46ecfc18e39ea',
        '7:catalog_publication_provenance:a69c081d1b0eaa31df8490773d3feed355fdb4053925f84087552df9b5fc940b'
    ];
BEGIN
    IF current_database() <> 'ascendany_v2' THEN
        RAISE EXCEPTION 'role bootstrap requires database ascendany_v2; connected to %', current_database();
    END IF;

    SELECT oid INTO target_schema
    FROM pg_namespace
    WHERE nspname = 'ascendany';

    IF target_schema IS NULL OR NOT EXISTS (
        SELECT 1
        FROM pg_depend
        WHERE refclassid = 'pg_namespace'::regclass
          AND refobjid = target_schema
          AND deptype = 'n'
    ) THEN
        RETURN;
    END IF;

    IF to_regclass('ascendany.schema_migrations_v2') IS NULL THEN
        RAISE EXCEPTION 'non-empty ascendany schema has no v2 migration history';
    END IF;

    EXECUTE $history$
        SELECT array_agg(version::text || ':' || name || ':' || sha256 ORDER BY version)
        FROM ascendany.schema_migrations_v2
    $history$
    INTO actual_history;

    IF actual_history IS DISTINCT FROM expected_history THEN
        RAISE EXCEPTION 'non-empty ascendany schema does not match the embedded schema-v7 history';
    END IF;
END
$database_boundary$;

-- Capability roles never authenticate. Five login principals have one
-- reviewed purpose each; passwords are provisioned outside this SQL file.
DO $roles$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'ascendany_database_owner') THEN
        CREATE ROLE ascendany_database_owner;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'ascendany_owner') THEN
        CREATE ROLE ascendany_owner;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'ascendany_runtime') THEN
        CREATE ROLE ascendany_runtime;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'ascendany_migrator') THEN
        CREATE ROLE ascendany_migrator;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'ascendany_backup') THEN
        CREATE ROLE ascendany_backup;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'ascendany_catalog_publisher') THEN
        CREATE ROLE ascendany_catalog_publisher;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'ascendanyd_login') THEN
        CREATE ROLE ascendanyd_login LOGIN;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'ascendany_migrator_login') THEN
        CREATE ROLE ascendany_migrator_login LOGIN;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'ascendany_backup_login') THEN
        CREATE ROLE ascendany_backup_login LOGIN;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'ascendany_restore_login') THEN
        CREATE ROLE ascendany_restore_login LOGIN;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'ascendany_catalog_publisher_login') THEN
        CREATE ROLE ascendany_catalog_publisher_login LOGIN;
    END IF;
END
$roles$;

ALTER ROLE ascendany_database_owner WITH NOLOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS CONNECTION LIMIT -1;
ALTER ROLE ascendany_owner WITH NOLOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS CONNECTION LIMIT -1;
ALTER ROLE ascendany_runtime WITH NOLOGIN INHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS CONNECTION LIMIT -1;
ALTER ROLE ascendany_migrator WITH NOLOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS CONNECTION LIMIT -1;
ALTER ROLE ascendany_backup WITH NOLOGIN INHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS CONNECTION LIMIT -1;
ALTER ROLE ascendany_catalog_publisher WITH NOLOGIN INHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS CONNECTION LIMIT -1;
ALTER ROLE ascendanyd_login WITH LOGIN INHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS CONNECTION LIMIT -1;
ALTER ROLE ascendany_migrator_login WITH LOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS CONNECTION LIMIT -1;
ALTER ROLE ascendany_backup_login WITH LOGIN INHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS CONNECTION LIMIT -1;
ALTER ROLE ascendany_restore_login WITH LOGIN NOINHERIT NOSUPERUSER CREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS CONNECTION LIMIT -1;
ALTER ROLE ascendany_catalog_publisher_login WITH LOGIN INHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS CONNECTION LIMIT -1;

ALTER ROLE ascendany_database_owner RESET ALL;
ALTER ROLE ascendany_owner RESET ALL;
ALTER ROLE ascendany_runtime RESET ALL;
ALTER ROLE ascendany_migrator RESET ALL;
ALTER ROLE ascendany_backup RESET ALL;
ALTER ROLE ascendany_catalog_publisher RESET ALL;
ALTER ROLE ascendanyd_login RESET ALL;
ALTER ROLE ascendany_migrator_login RESET ALL;
ALTER ROLE ascendany_backup_login RESET ALL;
ALTER ROLE ascendany_restore_login RESET ALL;
ALTER ROLE ascendany_catalog_publisher_login RESET ALL;

DO $closed_memberships$
DECLARE
    membership record;
    managed_roles constant text[] := ARRAY[
        'ascendany_database_owner',
        'ascendany_owner',
        'ascendany_runtime',
        'ascendany_migrator',
        'ascendany_backup',
        'ascendany_catalog_publisher',
        'ascendanyd_login',
        'ascendany_migrator_login',
        'ascendany_backup_login',
        'ascendany_restore_login',
        'ascendany_catalog_publisher_login'
    ];
BEGIN
    -- Remove every direct edge touching a managed role, including edges
    -- recorded under a different grantor, then reconstruct six exact edges.
    -- ascendany_database_owner remains isolated from the membership graph.
    FOR membership IN
        SELECT granted_role.rolname AS role_name,
               member_role.rolname AS member_name,
               grantor_role.rolname AS grantor_name
        FROM pg_auth_members AS edge
        JOIN pg_roles AS granted_role ON granted_role.oid = edge.roleid
        JOIN pg_roles AS member_role ON member_role.oid = edge.member
        JOIN pg_roles AS grantor_role ON grantor_role.oid = edge.grantor
        WHERE granted_role.rolname = ANY(managed_roles)
           OR member_role.rolname = ANY(managed_roles)
    LOOP
        EXECUTE format(
            'REVOKE %I FROM %I GRANTED BY %I CASCADE',
            membership.role_name,
            membership.member_name,
            membership.grantor_name
        );
    END LOOP;
END
$closed_memberships$;

GRANT ascendany_runtime TO ascendanyd_login WITH ADMIN FALSE, INHERIT TRUE, SET TRUE;
GRANT ascendany_migrator TO ascendany_migrator_login WITH ADMIN FALSE, INHERIT FALSE, SET TRUE;
GRANT ascendany_backup TO ascendany_backup_login WITH ADMIN FALSE, INHERIT TRUE, SET TRUE;
GRANT ascendany_catalog_publisher TO ascendany_catalog_publisher_login WITH ADMIN FALSE, INHERIT TRUE, SET TRUE;
GRANT ascendany_owner TO ascendany_migrator WITH ADMIN FALSE, INHERIT FALSE, SET TRUE;
GRANT ascendany_owner TO ascendany_restore_login WITH ADMIN FALSE, INHERIT FALSE, SET TRUE;

CREATE SCHEMA IF NOT EXISTS ascendany AUTHORIZATION ascendany_owner;
ALTER SCHEMA ascendany OWNER TO ascendany_owner;
ALTER SCHEMA public OWNER TO pg_database_owner;

SELECT format('ALTER DATABASE %I OWNER TO ascendany_database_owner', current_database())
\gexec

-- Reconstruct the database ACL from zero direct non-owner privileges.
DO $database_acl$
DECLARE
    acl_entry record;
    grantee_sql text;
BEGIN
    FOR acl_entry IN
        SELECT DISTINCT exploded.grantee, grantee.rolname
        FROM pg_database AS database
        CROSS JOIN LATERAL aclexplode(database.datacl) AS exploded
        LEFT JOIN pg_roles AS grantee ON grantee.oid = exploded.grantee
        WHERE database.datname = current_database()
          AND exploded.grantee <> (SELECT oid FROM pg_roles WHERE rolname = 'ascendany_database_owner')
    LOOP
        grantee_sql := CASE
            WHEN acl_entry.grantee = 0 THEN 'PUBLIC'
            ELSE format('%I', acl_entry.rolname)
        END;
        EXECUTE format(
            'REVOKE ALL PRIVILEGES ON DATABASE %I FROM %s',
            current_database(),
            grantee_sql
        );
    END LOOP;
END
$database_acl$;

SELECT format('REVOKE ALL PRIVILEGES ON DATABASE %I FROM PUBLIC', current_database())
\gexec
SELECT format('REVOKE ALL PRIVILEGES ON DATABASE %I FROM ascendany_database_owner', current_database())
\gexec
SELECT format('GRANT ALL PRIVILEGES ON DATABASE %I TO ascendany_database_owner', current_database())
\gexec
SELECT format(
    'GRANT CONNECT ON DATABASE %I TO ascendanyd_login, ascendany_migrator_login, ascendany_backup_login, ascendany_catalog_publisher_login',
    current_database()
)
\gexec

-- The restore operator performs CREATEDB/DROP lifecycle work from the fixed
-- PostgreSQL maintenance database. Close its direct privileges there and
-- grant CONNECT explicitly so the operator never depends on PUBLIC CONNECT.
REVOKE ALL PRIVILEGES ON DATABASE postgres FROM ascendany_restore_login;
GRANT CONNECT ON DATABASE postgres TO ascendany_restore_login;

-- The public schema keeps PostgreSQL 17's owner plus PUBLIC USAGE contract.
-- The application schema exposes USAGE only to the runtime, backup, and
-- catalog publisher capability roles. Login principals receive no direct
-- schema ACL.
DO $schema_acl$
DECLARE
    acl_entry record;
    grantee_sql text;
BEGIN
    FOR acl_entry IN
        SELECT DISTINCT namespace.nspname,
               namespace.nspowner,
               exploded.grantee,
               grantee.rolname
        FROM pg_namespace AS namespace
        CROSS JOIN LATERAL aclexplode(namespace.nspacl) AS exploded
        LEFT JOIN pg_roles AS grantee ON grantee.oid = exploded.grantee
        WHERE namespace.nspname IN ('public', 'ascendany')
          AND exploded.grantee <> namespace.nspowner
    LOOP
        grantee_sql := CASE
            WHEN acl_entry.grantee = 0 THEN 'PUBLIC'
            ELSE format('%I', acl_entry.rolname)
        END;
        EXECUTE format(
            'REVOKE ALL PRIVILEGES ON SCHEMA %I FROM %s',
            acl_entry.nspname,
            grantee_sql
        );
    END LOOP;
END
$schema_acl$;

REVOKE ALL PRIVILEGES ON SCHEMA public FROM PUBLIC, pg_database_owner;
GRANT ALL PRIVILEGES ON SCHEMA public TO pg_database_owner;
GRANT USAGE ON SCHEMA public TO PUBLIC;

REVOKE ALL PRIVILEGES ON SCHEMA ascendany FROM PUBLIC, ascendany_owner;
GRANT ALL PRIVILEGES ON SCHEMA ascendany TO ascendany_owner;
GRANT USAGE ON SCHEMA ascendany TO ascendany_runtime, ascendany_backup, ascendany_catalog_publisher;

-- Remove every explicit relation ACL, including unknown grantees and direct
-- login grants, before rebuilding the table and sequence capability sets.
DO $relation_acl$
DECLARE
    acl_entry record;
    grantee_sql text;
    object_kind text;
BEGIN
    FOR acl_entry IN
        SELECT DISTINCT namespace.nspname,
               relation.relname,
               relation.relkind,
               exploded.grantee,
               grantee.rolname
        FROM pg_class AS relation
        JOIN pg_namespace AS namespace ON namespace.oid = relation.relnamespace
        CROSS JOIN LATERAL aclexplode(relation.relacl) AS exploded
        LEFT JOIN pg_roles AS grantee ON grantee.oid = exploded.grantee
        WHERE namespace.nspname = 'ascendany'
          AND relation.relkind IN ('r', 'p', 'v', 'm', 'f', 'S')
    LOOP
        grantee_sql := CASE
            WHEN acl_entry.grantee = 0 THEN 'PUBLIC'
            ELSE format('%I', acl_entry.rolname)
        END;
        object_kind := CASE WHEN acl_entry.relkind = 'S' THEN 'SEQUENCE' ELSE 'TABLE' END;
        EXECUTE format(
            'REVOKE ALL PRIVILEGES ON %s %I.%I FROM %s',
            object_kind,
            acl_entry.nspname,
            acl_entry.relname,
            grantee_sql
        );
    END LOOP;
END
$relation_acl$;

GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA ascendany TO ascendany_owner;
GRANT ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA ascendany TO ascendany_owner;
GRANT SELECT ON ALL TABLES IN SCHEMA ascendany TO ascendany_runtime, ascendany_backup;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA ascendany TO ascendany_runtime;
GRANT SELECT ON ALL SEQUENCES IN SCHEMA ascendany TO ascendany_backup;

DO $table_allowlists$
DECLARE
    relation record;
    runtime_insert_tables constant text[] := ARRAY[
        'artifacts',
        'logical_exams',
        'import_jobs',
        'import_job_events',
        'pintia_actors',
        'pintia_actor_identifiers',
        'auth_accounts',
        'auth_enrollment_grants',
        'auth_enrollment_events',
        'auth_sessions',
        'auth_refresh_tokens',
        'audit_events',
        'exam_snapshots',
        'pintia_snapshot_problems',
        'pintia_snapshot_participants',
        'pintia_rankings',
        'pintia_ranking_problem_results',
        'pintia_submission_identities',
        'pintia_snapshot_submissions',
        'pintia_submission_case_results',
        'analytics_generations',
        'analytics_generation_snapshots',
        'student_analytics',
        'problem_analytics',
        'configuration_items',
        'configuration_versions',
        'analytics_generation_events',
        'chat_threads',
        'chat_messages',
        'agent_runs',
        'agent_run_events',
        'agent_tool_calls',
        'agent_notes',
        'agent_note_revisions',
        'recommendation_model_releases',
        'recommendation_model_activation_events',
        'recommendation_model_head',
        'knowledge_catalog_publication_authorizations',
        'oj_problems',
        'oj_problem_versions',
        'oj_submissions',
        'oj_judge_jobs',
        'oj_judge_job_events',
        'oj_judge_results',
        'feedback_submissions',
        'feedback_attachments',
        'feedback_delivery_jobs',
        'feedback_delivery_events'
    ];
BEGIN
    FOR relation IN
        SELECT namespace.nspname, class.relname
        FROM pg_class AS class
        JOIN pg_namespace AS namespace ON namespace.oid = class.relnamespace
        WHERE namespace.nspname = 'ascendany'
          AND class.relkind IN ('r', 'p')
          AND class.relname = ANY(runtime_insert_tables)
    LOOP
        EXECUTE format(
            'GRANT INSERT ON TABLE %I.%I TO ascendany_runtime',
            relation.nspname,
            relation.relname
        );
    END LOOP;

    IF to_regclass('ascendany.achievement_rule_sets_achievement_rule_set_id_seq') IS NOT NULL THEN
        REVOKE ALL PRIVILEGES
        ON SEQUENCE ascendany.achievement_rule_sets_achievement_rule_set_id_seq
        FROM ascendany_runtime;
    END IF;
END
$table_allowlists$;

DO $catalog_publisher_table_allowlists$
DECLARE
    relation record;
    publisher_select_tables constant text[] := ARRAY[
        'schema_migrations_v2'
    ];
    publisher_insert_tables constant text[] := ARRAY[]::text[];
BEGIN
    FOR relation IN
        SELECT namespace.nspname, class.relname
        FROM pg_class AS class
        JOIN pg_namespace AS namespace ON namespace.oid = class.relnamespace
        WHERE namespace.nspname = 'ascendany'
          AND class.relkind IN ('r', 'p')
          AND class.relname = ANY(publisher_select_tables)
    LOOP
        EXECUTE format(
            'GRANT SELECT ON TABLE %I.%I TO ascendany_catalog_publisher',
            relation.nspname,
            relation.relname
        );
    END LOOP;

    FOR relation IN
        SELECT namespace.nspname, class.relname
        FROM pg_class AS class
        JOIN pg_namespace AS namespace ON namespace.oid = class.relnamespace
        WHERE namespace.nspname = 'ascendany'
          AND class.relkind IN ('r', 'p')
          AND class.relname = ANY(publisher_insert_tables)
    LOOP
        EXECUTE format(
            'GRANT INSERT ON TABLE %I.%I TO ascendany_catalog_publisher',
            relation.nspname,
            relation.relname
        );
    END LOOP;
END
$catalog_publisher_table_allowlists$;

-- Column privileges do not follow table-level REVOKE. Clear each explicit
-- column ACL and reconstruct the reviewed runtime UPDATE list.
DO $column_acl$
DECLARE
    acl_entry record;
    grantee_sql text;
    update_column text;
    table_name text;
    column_name text;
    runtime_update_columns constant text[] := ARRAY[
        'logical_exams.active_snapshot_id',
        'logical_exams.head_revision',
        'logical_exams.updated_at',
        'import_jobs.status',
        'import_jobs.stage',
        'import_jobs.attempt_count',
        'import_jobs.lease_owner',
        'import_jobs.lease_expires_at',
        'import_jobs.next_attempt_at',
        'import_jobs.snapshot_id',
        'import_jobs.error_code',
        'import_jobs.error_detail',
        'import_jobs.error_permanent',
        'import_jobs.started_at',
        'import_jobs.finished_at',
        'import_jobs.updated_at',
        'auth_accounts.password_phc',
        'auth_accounts.display_name',
        'auth_accounts.auth_revision',
        'auth_accounts.disabled_at',
        'auth_accounts.updated_at',
        'auth_sessions.last_seen_at',
        'auth_sessions.revoked_at',
        'auth_sessions.revocation_reason',
        'auth_refresh_tokens.used_at',
        'auth_refresh_tokens.replaced_by_token_id',
        'auth_refresh_tokens.revoked_at',
        'analytics_generations.status',
        'analytics_generations.error_code',
        'analytics_generations.error_detail',
        'analytics_generations.attempt_count',
        'analytics_generations.lease_owner',
        'analytics_generations.lease_expires_at',
        'analytics_generations.next_attempt_at',
        'analytics_generations.started_at',
        'analytics_generations.finished_at',
        'analytics_head.current_generation_id',
        'analytics_head.head_revision',
        'analytics_head.updated_at',
        'configuration_items.active_version_id',
        'configuration_items.head_revision',
        'configuration_items.updated_at',
        'chat_threads.head_revision',
        'chat_threads.updated_at',
        'agent_runs.output_message_id',
        'agent_runs.status',
        'agent_runs.attempt_count',
        'agent_runs.attempt_token',
        'agent_runs.lease_owner',
        'agent_runs.lease_expires_at',
        'agent_runs.next_attempt_at',
        'agent_runs.error_code',
        'agent_runs.error_detail',
        'agent_runs.started_at',
        'agent_runs.finished_at',
        'agent_runs.updated_at',
        'agent_notes.current_revision_id',
        'agent_notes.head_revision',
        'agent_notes.updated_at',
        'recommendation_model_head.current_release_id',
        'recommendation_model_head.head_revision',
        'recommendation_model_head.pending_catalog_publication_id',
        'recommendation_model_head.updated_at',
        'oj_problems.current_version_id',
        'oj_problems.head_revision',
        'oj_problems.updated_at',
        'oj_judge_jobs.status',
        'oj_judge_jobs.attempt_count',
        'oj_judge_jobs.attempt_token',
        'oj_judge_jobs.lease_owner',
        'oj_judge_jobs.lease_expires_at',
        'oj_judge_jobs.next_attempt_at',
        'oj_judge_jobs.judge_result_id',
        'oj_judge_jobs.error_code',
        'oj_judge_jobs.error_detail',
        'oj_judge_jobs.started_at',
        'oj_judge_jobs.finished_at',
        'oj_judge_jobs.updated_at',
        'feedback_delivery_jobs.status',
        'feedback_delivery_jobs.attempt_count',
        'feedback_delivery_jobs.attempt_token',
        'feedback_delivery_jobs.lease_owner',
        'feedback_delivery_jobs.lease_expires_at',
        'feedback_delivery_jobs.next_attempt_at',
        'feedback_delivery_jobs.provider_receipt_sha256',
        'feedback_delivery_jobs.error_code',
        'feedback_delivery_jobs.error_detail',
        'feedback_delivery_jobs.started_at',
        'feedback_delivery_jobs.finished_at',
        'feedback_delivery_jobs.updated_at'
    ];
BEGIN
    FOR acl_entry IN
        SELECT namespace.nspname,
               relation.relname,
               attribute.attname,
               exploded.privilege_type,
               exploded.grantee,
               grantee.rolname
        FROM pg_attribute AS attribute
        JOIN pg_class AS relation ON relation.oid = attribute.attrelid
        JOIN pg_namespace AS namespace ON namespace.oid = relation.relnamespace
        CROSS JOIN LATERAL aclexplode(attribute.attacl) AS exploded
        LEFT JOIN pg_roles AS grantee ON grantee.oid = exploded.grantee
        WHERE namespace.nspname = 'ascendany'
          AND relation.relkind IN ('r', 'p', 'v', 'm', 'f')
          AND attribute.attnum > 0
          AND NOT attribute.attisdropped
    LOOP
        grantee_sql := CASE
            WHEN acl_entry.grantee = 0 THEN 'PUBLIC'
            ELSE format('%I', acl_entry.rolname)
        END;
        EXECUTE format(
            'REVOKE %s (%I) ON TABLE %I.%I FROM %s',
            acl_entry.privilege_type,
            acl_entry.attname,
            acl_entry.nspname,
            acl_entry.relname,
            grantee_sql
        );
    END LOOP;

    FOREACH update_column IN ARRAY runtime_update_columns LOOP
        table_name := split_part(update_column, '.', 1);
        column_name := split_part(update_column, '.', 2);
        IF to_regclass(format('ascendany.%I', table_name)) IS NOT NULL THEN
            EXECUTE format(
                'GRANT UPDATE (%I) ON TABLE ascendany.%I TO ascendany_runtime',
                column_name,
                table_name
            );
        END IF;
    END LOOP;
END
$column_acl$;

-- Routine ACL cleanup handles functions, procedures, aggregates, and window
-- functions through PostgreSQL's ROUTINE object class. The stopped-runtime
-- publisher receives one exact atomic capability routine after the owner baseline is rebuilt.
DO $routine_acl$
DECLARE
    acl_entry record;
    grantee_sql text;
BEGIN
    FOR acl_entry IN
        SELECT DISTINCT namespace.nspname,
               procedure.proname,
               pg_get_function_identity_arguments(procedure.oid) AS identity_arguments,
               exploded.grantee,
               grantee.rolname
        FROM pg_proc AS procedure
        JOIN pg_namespace AS namespace ON namespace.oid = procedure.pronamespace
        CROSS JOIN LATERAL aclexplode(procedure.proacl) AS exploded
        LEFT JOIN pg_roles AS grantee ON grantee.oid = exploded.grantee
        WHERE namespace.nspname = 'ascendany'
    LOOP
        grantee_sql := CASE
            WHEN acl_entry.grantee = 0 THEN 'PUBLIC'
            ELSE format('%I', acl_entry.rolname)
        END;
        EXECUTE format(
            'REVOKE ALL PRIVILEGES ON ROUTINE %I.%I(%s) FROM %s',
            acl_entry.nspname,
            acl_entry.proname,
            acl_entry.identity_arguments,
            grantee_sql
        );
    END LOOP;
END
$routine_acl$;

REVOKE ALL PRIVILEGES ON ALL ROUTINES IN SCHEMA ascendany FROM PUBLIC, ascendany_owner;
GRANT ALL PRIVILEGES ON ALL ROUTINES IN SCHEMA ascendany TO ascendany_owner;

DO $catalog_publisher_routine_acl$
BEGIN
    IF to_regprocedure('ascendany.publish_authorized_knowledge_catalog(uuid,text,text)') IS NOT NULL THEN
        EXECUTE 'GRANT EXECUTE ON FUNCTION ascendany.publish_authorized_knowledge_catalog(uuid, text, text) TO ascendany_catalog_publisher';
    END IF;
END
$catalog_publisher_routine_acl$;

-- PostgreSQL creates array and composite types with every table. Close their
-- default PUBLIC USAGE and grant exact type access to runtime and backup.
DO $type_acl$
DECLARE
    type_entry record;
    acl_entry record;
    grantee_sql text;
BEGIN
    FOR type_entry IN
        SELECT type.oid, namespace.nspname, type.typname
        FROM pg_type AS type
        JOIN pg_namespace AS namespace ON namespace.oid = type.typnamespace
        WHERE namespace.nspname = 'ascendany'
          AND type.typelem = 0
    LOOP
        FOR acl_entry IN
            SELECT DISTINCT exploded.grantee, grantee.rolname
            FROM pg_type AS type
            CROSS JOIN LATERAL aclexplode(type.typacl) AS exploded
            LEFT JOIN pg_roles AS grantee ON grantee.oid = exploded.grantee
            WHERE type.oid = type_entry.oid
        LOOP
            grantee_sql := CASE
                WHEN acl_entry.grantee = 0 THEN 'PUBLIC'
                ELSE format('%I', acl_entry.rolname)
            END;
            EXECUTE format(
                'REVOKE ALL PRIVILEGES ON TYPE %I.%I FROM %s',
                type_entry.nspname,
                type_entry.typname,
                grantee_sql
            );
        END LOOP;

        EXECUTE format(
            'REVOKE ALL PRIVILEGES ON TYPE %I.%I FROM PUBLIC, ascendany_owner',
            type_entry.nspname,
            type_entry.typname
        );
        EXECUTE format(
            'GRANT USAGE ON TYPE %I.%I TO ascendany_owner, ascendany_runtime, ascendany_backup',
            type_entry.nspname,
            type_entry.typname
        );
    END LOOP;
END
$type_acl$;

-- Remove every default-ACL deviation in the dedicated v2 database. Then
-- create five reviewed rows: global routine/type closure and schema
-- table/sequence/type grants for owner-created objects.
DO $default_acl_cleanup$
DECLARE
    acl_entry record;
    grantee_sql text;
    schema_sql text;
    object_class text;
BEGIN
    FOR acl_entry IN
        SELECT DISTINCT creator.rolname AS creator_name,
               namespace.nspname,
               defaults.defaclobjtype,
               exploded.grantee,
               grantee.rolname AS grantee_name
        FROM pg_default_acl AS defaults
        JOIN pg_roles AS creator ON creator.oid = defaults.defaclrole
        LEFT JOIN pg_namespace AS namespace ON namespace.oid = defaults.defaclnamespace
        CROSS JOIN LATERAL aclexplode(defaults.defaclacl) AS exploded
        LEFT JOIN pg_roles AS grantee ON grantee.oid = exploded.grantee
    LOOP
        grantee_sql := CASE
            WHEN acl_entry.grantee = 0 THEN 'PUBLIC'
            ELSE format('%I', acl_entry.grantee_name)
        END;
        schema_sql := CASE
            WHEN acl_entry.nspname IS NULL THEN ''
            ELSE format(' IN SCHEMA %I', acl_entry.nspname)
        END;
        object_class := CASE acl_entry.defaclobjtype
            WHEN 'r' THEN 'TABLES'
            WHEN 'S' THEN 'SEQUENCES'
            WHEN 'f' THEN 'FUNCTIONS'
            WHEN 'T' THEN 'TYPES'
            WHEN 'n' THEN 'SCHEMAS'
            WHEN 'L' THEN 'LARGE OBJECTS'
            ELSE NULL
        END;
        IF object_class IS NULL THEN
            RAISE EXCEPTION 'unsupported default ACL object type: %', acl_entry.defaclobjtype;
        END IF;
        EXECUTE format(
            'ALTER DEFAULT PRIVILEGES FOR ROLE %I%s REVOKE ALL PRIVILEGES ON %s FROM %s',
            acl_entry.creator_name,
            schema_sql,
            object_class,
            grantee_sql
        );
    END LOOP;
END
$default_acl_cleanup$;

ALTER DEFAULT PRIVILEGES FOR ROLE ascendany_owner REVOKE EXECUTE ON FUNCTIONS FROM PUBLIC;
ALTER DEFAULT PRIVILEGES FOR ROLE ascendany_owner REVOKE USAGE ON TYPES FROM PUBLIC;
ALTER DEFAULT PRIVILEGES FOR ROLE ascendany_owner IN SCHEMA ascendany GRANT SELECT ON TABLES TO ascendany_runtime, ascendany_backup;
ALTER DEFAULT PRIVILEGES FOR ROLE ascendany_owner IN SCHEMA ascendany GRANT USAGE, SELECT ON SEQUENCES TO ascendany_runtime;
ALTER DEFAULT PRIVILEGES FOR ROLE ascendany_owner IN SCHEMA ascendany GRANT SELECT ON SEQUENCES TO ascendany_backup;
ALTER DEFAULT PRIVILEGES FOR ROLE ascendany_owner IN SCHEMA ascendany GRANT USAGE ON TYPES TO ascendany_runtime, ascendany_backup;

COMMIT;
