\set ON_ERROR_STOP on

\if :{?ascendany_verification_profile}
SELECT set_config(
    'ascendany.verification_profile',
    :'ascendany_verification_profile',
    false
);
\else
SELECT set_config('ascendany.verification_profile', 'source_admin', false);
\endif

-- ascendany-go-verifier-begin
DO $verify$
DECLARE
    verification_profile constant text := current_setting('ascendany.verification_profile', false);
    role_name text;
    capability_roles constant text[] := ARRAY[
        'ascendany_database_owner',
        'ascendany_owner',
        'ascendany_runtime',
        'ascendany_migrator',
        'ascendany_backup'
    ];
    login_roles constant text[] := ARRAY[
        'ascendanyd_login',
        'ascendany_migrator_login',
        'ascendany_backup_login',
        'ascendany_restore_login'
    ];
    managed_roles constant text[] := capability_roles || login_roles;
    expected_memberships constant text[] := ARRAY[
        'ascendany_runtime>ascendanyd_login>false>true>true',
        'ascendany_migrator>ascendany_migrator_login>false>false>true',
        'ascendany_backup>ascendany_backup_login>false>true>true',
        'ascendany_owner>ascendany_migrator>false>false>true',
        'ascendany_owner>ascendany_restore_login>false>false>true'
    ];
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
    runtime_no_usage_sequences constant text[] := ARRAY[
        'achievement_rule_sets_achievement_rule_set_id_seq'
    ];
    violation_count bigint;
    distinct_verifier_count bigint;
    database_owner_oid oid;
    owner_oid oid;
BEGIN
    IF verification_profile NOT IN ('source_admin', 'source_snapshot', 'restore') THEN
        RAISE EXCEPTION 'unknown database verification profile %', verification_profile;
    END IF;
    IF verification_profile IN ('source_admin', 'source_snapshot')
       AND current_database() <> 'ascendany_v2' THEN
        RAISE EXCEPTION 'source verification requires database ascendany_v2; connected to %', current_database();
    END IF;
    IF verification_profile = 'restore'
       AND current_database() <> 'ascendany_v2_restore_verify' THEN
        RAISE EXCEPTION 'restore verification requires database ascendany_v2_restore_verify; connected to %', current_database();
    END IF;

    SELECT oid INTO owner_oid
    FROM pg_roles
    WHERE rolname = 'ascendany_owner';

    IF owner_oid IS NULL THEN
        RAISE EXCEPTION 'ascendany_owner is unavailable';
    END IF;

    IF verification_profile = 'source_admin' THEN
        SELECT oid INTO database_owner_oid
        FROM pg_roles
        WHERE rolname = 'ascendany_database_owner';

    -- Exact pg_roles row contract. Password hashes and validity timestamps are
    -- provisioned externally; every behavioral flag, connection limit, and
    -- per-role setting is closed here.
    SELECT count(*)
    INTO violation_count
    FROM (
        VALUES
            ('ascendany_database_owner', false, false, false),
            ('ascendany_owner', false, false, false),
            ('ascendany_runtime', false, true, false),
            ('ascendany_migrator', false, false, false),
            ('ascendany_backup', false, true, false),
            ('ascendanyd_login', true, true, false),
            ('ascendany_migrator_login', true, false, false),
            ('ascendany_backup_login', true, true, false),
            ('ascendany_restore_login', true, false, true)
    ) AS expected(rolname, can_login, inherits_privileges, can_create_database)
    LEFT JOIN pg_roles AS actual ON actual.rolname = expected.rolname
    WHERE actual.oid IS NULL
       OR actual.rolcanlogin IS DISTINCT FROM expected.can_login
       OR actual.rolinherit IS DISTINCT FROM expected.inherits_privileges
       OR actual.rolcreatedb IS DISTINCT FROM expected.can_create_database
       OR actual.rolsuper
       OR actual.rolcreaterole
       OR actual.rolreplication
       OR actual.rolbypassrls
       OR actual.rolconnlimit <> -1
       OR actual.rolconfig IS NOT NULL;
    IF violation_count <> 0 THEN
        RAISE EXCEPTION '% managed role rows differ from the exact v2 contract', violation_count;
    END IF;

    IF EXISTS (
        SELECT 1
        FROM pg_roles
        WHERE rolname !~ '^pg_'
          AND rolname <> ALL(managed_roles)
          AND rolname <> 'postgres'
    ) THEN
        RAISE EXCEPTION 'cluster contains an unowned non-system role';
    END IF;

    IF EXISTS (
        SELECT 1
        FROM pg_db_role_setting
        WHERE setrole IN (SELECT oid FROM pg_roles WHERE rolname = ANY(managed_roles))
    ) THEN
        RAISE EXCEPTION 'managed role has a per-database configuration row';
    END IF;

    SELECT count(*), count(DISTINCT rolpassword)
    INTO violation_count, distinct_verifier_count
    FROM pg_authid
    WHERE rolname = ANY(login_roles)
      AND rolpassword ~ '^SCRAM-SHA-256\$4096:[A-Za-z0-9+/]+={0,2}\$[A-Za-z0-9+/]+={0,2}:[A-Za-z0-9+/]+={0,2}$';
    IF violation_count <> cardinality(login_roles)
       OR distinct_verifier_count <> cardinality(login_roles) THEN
        RAISE EXCEPTION 'managed login SCRAM verifier shape or distinctness differs from the exact contract';
    END IF;

    WITH actual AS (
        SELECT granted_role.rolname || '>' || member_role.rolname || '>' ||
               membership.admin_option::text || '>' || membership.inherit_option::text || '>' ||
               membership.set_option::text AS membership_key
        FROM pg_auth_members AS membership
        JOIN pg_roles AS granted_role ON granted_role.oid = membership.roleid
        JOIN pg_roles AS member_role ON member_role.oid = membership.member
        WHERE granted_role.rolname = ANY(managed_roles)
           OR member_role.rolname = ANY(managed_roles)
    ), expected AS (
        SELECT membership_key
        FROM unnest(expected_memberships) AS entry(membership_key)
    ), difference AS (
        (SELECT membership_key FROM actual EXCEPT ALL SELECT membership_key FROM expected)
        UNION ALL
        (SELECT membership_key FROM expected EXCEPT ALL SELECT membership_key FROM actual)
    )
    SELECT count(*) INTO violation_count FROM difference;
    IF violation_count <> 0 THEN
        RAISE EXCEPTION 'managed role membership graph or membership options differ from the exact v2 contract';
    END IF;

    IF NOT pg_has_role('ascendany_restore_login', 'ascendany_owner', 'MEMBER')
       OR pg_has_role('ascendany_restore_login', 'ascendany_owner', 'USAGE') THEN
        RAISE EXCEPTION 'restore operator must reach owner only through explicit SET ROLE';
    END IF;

    IF EXISTS (
        SELECT 1
        FROM pg_auth_members AS membership
        WHERE membership.roleid = database_owner_oid
           OR membership.member = database_owner_oid
    ) THEN
        RAISE EXCEPTION 'database owner role must have no membership edges';
    END IF;

    WITH actual AS (
        SELECT acl.privilege_type,
               acl.is_grantable
        FROM pg_database AS database
        CROSS JOIN LATERAL aclexplode(database.datacl) AS acl
        WHERE database.datname = 'postgres'
          AND acl.grantee = (SELECT oid FROM pg_roles WHERE rolname = 'ascendany_restore_login')
    ), expected(privilege_type, is_grantable) AS (
        VALUES ('CONNECT', false)
    ), difference AS (
        (SELECT * FROM actual EXCEPT ALL SELECT * FROM expected)
        UNION ALL
        (SELECT * FROM expected EXCEPT ALL SELECT * FROM actual)
    )
    SELECT count(*) INTO violation_count FROM difference;
    IF violation_count <> 0 OR NOT EXISTS (
        SELECT 1 FROM pg_database WHERE datname = 'postgres' AND datallowconn
    ) THEN
        RAISE EXCEPTION 'restore maintenance database CONNECT contract differs from the exact v2 boundary';
    END IF;
    END IF;

    IF NOT EXISTS (
        SELECT 1
        FROM pg_database AS database
        JOIN pg_roles AS owner ON owner.oid = database.datdba
        WHERE database.datname = current_database()
          AND owner.rolname = CASE verification_profile
              WHEN 'restore' THEN 'ascendany_owner'
              ELSE 'ascendany_database_owner'
          END
    ) THEN
        RAISE EXCEPTION 'database owner differs from verification profile %', verification_profile;
    END IF;

    WITH actual AS (
        SELECT CASE WHEN acl.grantee = 0 THEN 'PUBLIC' ELSE grantee.rolname END AS grantee_name,
               acl.privilege_type,
               acl.is_grantable
        FROM pg_database AS database
        CROSS JOIN LATERAL aclexplode(COALESCE(database.datacl, acldefault('d', database.datdba))) AS acl
        LEFT JOIN pg_roles AS grantee ON grantee.oid = acl.grantee
        WHERE database.datname = current_database()
    ), expected(grantee_name, privilege_type, is_grantable) AS (
        SELECT *
        FROM (VALUES
            ('ascendany_database_owner', 'CONNECT', false),
            ('ascendany_database_owner', 'CREATE', false),
            ('ascendany_database_owner', 'TEMPORARY', false),
            ('ascendanyd_login', 'CONNECT', false),
            ('ascendany_migrator_login', 'CONNECT', false),
            ('ascendany_backup_login', 'CONNECT', false)
        ) AS source_acl(grantee_name, privilege_type, is_grantable)
        WHERE verification_profile IN ('source_admin', 'source_snapshot')

        UNION ALL

        SELECT *
        FROM (VALUES
            ('ascendany_owner', 'CONNECT', false),
            ('ascendany_owner', 'CREATE', false),
            ('ascendany_owner', 'TEMPORARY', false),
            ('ascendany_restore_login', 'CONNECT', false)
        ) AS restore_acl(grantee_name, privilege_type, is_grantable)
        WHERE verification_profile = 'restore'
    ), difference AS (
        (SELECT * FROM actual EXCEPT ALL SELECT * FROM expected)
        UNION ALL
        (SELECT * FROM expected EXCEPT ALL SELECT * FROM actual)
    )
    SELECT count(*) INTO violation_count FROM difference;
    IF violation_count <> 0 THEN
        RAISE EXCEPTION 'database ACL differs from the exact v2 contract';
    END IF;

    IF NOT EXISTS (
        SELECT 1
        FROM pg_namespace
        WHERE nspname = 'ascendany'
          AND nspowner = owner_oid
    ) OR NOT EXISTS (
        SELECT 1
        FROM pg_namespace AS namespace
        JOIN pg_roles AS owner ON owner.oid = namespace.nspowner
        WHERE namespace.nspname = 'public'
          AND owner.rolname = 'pg_database_owner'
    ) THEN
        RAISE EXCEPTION 'public or ascendany schema ownership differs from the PostgreSQL 17 contract';
    END IF;

    WITH actual AS (
        SELECT namespace.nspname,
               CASE WHEN acl.grantee = 0 THEN 'PUBLIC' ELSE grantee.rolname END AS grantee_name,
               acl.privilege_type,
               acl.is_grantable
        FROM pg_namespace AS namespace
        CROSS JOIN LATERAL aclexplode(COALESCE(namespace.nspacl, acldefault('n', namespace.nspowner))) AS acl
        LEFT JOIN pg_roles AS grantee ON grantee.oid = acl.grantee
        WHERE namespace.nspname IN ('public', 'ascendany')
    ), expected(nspname, grantee_name, privilege_type, is_grantable) AS (
        VALUES
            ('public', 'pg_database_owner', 'CREATE', false),
            ('public', 'pg_database_owner', 'USAGE', false),
            ('public', 'PUBLIC', 'USAGE', false),
            ('ascendany', 'ascendany_owner', 'CREATE', false),
            ('ascendany', 'ascendany_owner', 'USAGE', false),
            ('ascendany', 'ascendany_runtime', 'USAGE', false),
            ('ascendany', 'ascendany_backup', 'USAGE', false)
    ), difference AS (
        (SELECT * FROM actual EXCEPT ALL SELECT * FROM expected)
        UNION ALL
        (SELECT * FROM expected EXCEPT ALL SELECT * FROM actual)
    )
    SELECT count(*) INTO violation_count FROM difference;
    IF violation_count <> 0 THEN
        RAISE EXCEPTION 'public or ascendany schema ACL differs from the exact v2 contract';
    END IF;

    SELECT count(*)
    INTO violation_count
    FROM (
        SELECT relation.oid
        FROM pg_class AS relation
        JOIN pg_namespace AS namespace ON namespace.oid = relation.relnamespace
        WHERE namespace.nspname = 'ascendany'
          AND relation.relowner <> owner_oid
        UNION ALL
        SELECT procedure.oid
        FROM pg_proc AS procedure
        JOIN pg_namespace AS namespace ON namespace.oid = procedure.pronamespace
        WHERE namespace.nspname = 'ascendany'
          AND procedure.proowner <> owner_oid
        UNION ALL
        SELECT type.oid
        FROM pg_type AS type
        JOIN pg_namespace AS namespace ON namespace.oid = type.typnamespace
        WHERE namespace.nspname = 'ascendany'
          AND type.typowner <> owner_oid
    ) AS wrong_owner;
    IF violation_count <> 0 THEN
        RAISE EXCEPTION '% schema objects are not owned by ascendany_owner', violation_count;
    END IF;

    SELECT count(*)
    INTO violation_count
    FROM unnest(runtime_insert_tables) AS expected(table_name)
    LEFT JOIN pg_namespace AS namespace ON namespace.nspname = 'ascendany'
    LEFT JOIN pg_class AS relation
      ON relation.relnamespace = namespace.oid
     AND relation.relname = expected.table_name
     AND relation.relkind IN ('r', 'p')
    WHERE relation.oid IS NULL;
    IF violation_count <> 0 THEN
        RAISE EXCEPTION '% runtime INSERT allowlist tables are missing', violation_count;
    END IF;

    SELECT count(*)
    INTO violation_count
    FROM unnest(runtime_no_usage_sequences) AS expected(sequence_name)
    LEFT JOIN pg_namespace AS namespace ON namespace.nspname = 'ascendany'
    LEFT JOIN pg_class AS relation
      ON relation.relnamespace = namespace.oid
     AND relation.relname = expected.sequence_name
     AND relation.relkind = 'S'
    WHERE relation.oid IS NULL;
    IF violation_count <> 0 THEN
        RAISE EXCEPTION '% runtime sequence deny-list entries are missing', violation_count;
    END IF;

    WITH relations AS (
        SELECT relation.*
        FROM pg_class AS relation
        JOIN pg_namespace AS namespace ON namespace.oid = relation.relnamespace
        WHERE namespace.nspname = 'ascendany'
          AND relation.relkind IN ('r', 'p', 'v', 'm', 'f', 'S')
    ), actual AS (
        SELECT relation.oid AS object_oid,
               CASE WHEN acl.grantee = 0 THEN 'PUBLIC' ELSE grantee.rolname END AS grantee_name,
               acl.privilege_type,
               acl.is_grantable
        FROM relations AS relation
        CROSS JOIN LATERAL aclexplode(
            COALESCE(
                relation.relacl,
                acldefault(CASE WHEN relation.relkind = 'S' THEN 's'::"char" ELSE 'r'::"char" END, relation.relowner)
            )
        ) AS acl
        LEFT JOIN pg_roles AS grantee ON grantee.oid = acl.grantee
    ), expected AS (
        SELECT relation.oid AS object_oid,
               'ascendany_owner'::text AS grantee_name,
               privilege_type,
               false AS is_grantable
        FROM relations AS relation
        CROSS JOIN LATERAL unnest(
            CASE WHEN relation.relkind = 'S'
                THEN ARRAY['SELECT', 'UPDATE', 'USAGE']::text[]
                ELSE ARRAY['DELETE', 'INSERT', 'MAINTAIN', 'REFERENCES', 'SELECT', 'TRIGGER', 'TRUNCATE', 'UPDATE']::text[]
            END
        ) AS owner_privileges(privilege_type)

        UNION ALL

        SELECT relation.oid, 'ascendany_runtime', 'SELECT', false
        FROM relations AS relation
        WHERE relation.relkind <> 'S'

        UNION ALL

        SELECT relation.oid, 'ascendany_runtime', 'INSERT', false
        FROM relations AS relation
        WHERE relation.relkind IN ('r', 'p')
          AND relation.relname = ANY(runtime_insert_tables)

        UNION ALL

        SELECT relation.oid, 'ascendany_backup', 'SELECT', false
        FROM relations AS relation
        WHERE relation.relkind <> 'S'

        UNION ALL

        SELECT relation.oid, 'ascendany_runtime', runtime_privilege, false
        FROM relations AS relation
        CROSS JOIN LATERAL unnest(ARRAY['SELECT', 'USAGE']::text[]) AS privileges(runtime_privilege)
        WHERE relation.relkind = 'S'
          AND NOT (relation.relname = ANY(runtime_no_usage_sequences))

        UNION ALL

        SELECT relation.oid, 'ascendany_backup', 'SELECT', false
        FROM relations AS relation
        WHERE relation.relkind = 'S'
    ), difference AS (
        (SELECT * FROM actual EXCEPT ALL SELECT * FROM expected)
        UNION ALL
        (SELECT * FROM expected EXCEPT ALL SELECT * FROM actual)
    )
    SELECT count(*) INTO violation_count FROM difference;
    IF violation_count <> 0 THEN
        RAISE EXCEPTION '% relation ACL entries differ from the exact v2 contract', violation_count;
    END IF;

    SELECT count(*)
    INTO violation_count
    FROM unnest(runtime_update_columns) AS expected(column_name)
    LEFT JOIN pg_namespace AS namespace ON namespace.nspname = 'ascendany'
    LEFT JOIN pg_class AS relation
      ON relation.relnamespace = namespace.oid
     AND relation.relname = split_part(expected.column_name, '.', 1)
     AND relation.relkind IN ('r', 'p')
    LEFT JOIN pg_attribute AS attribute
      ON attribute.attrelid = relation.oid
     AND attribute.attname = split_part(expected.column_name, '.', 2)
     AND attribute.attnum > 0
     AND NOT attribute.attisdropped
    WHERE attribute.attnum IS NULL;
    IF violation_count <> 0 THEN
        RAISE EXCEPTION '% runtime UPDATE allowlist columns are missing', violation_count;
    END IF;

    WITH actual AS (
        SELECT attribute.attrelid AS object_oid,
               attribute.attnum,
               CASE WHEN acl.grantee = 0 THEN 'PUBLIC' ELSE grantee.rolname END AS grantee_name,
               acl.privilege_type,
               acl.is_grantable
        FROM pg_attribute AS attribute
        JOIN pg_class AS relation ON relation.oid = attribute.attrelid
        JOIN pg_namespace AS namespace ON namespace.oid = relation.relnamespace
        CROSS JOIN LATERAL aclexplode(attribute.attacl) AS acl
        LEFT JOIN pg_roles AS grantee ON grantee.oid = acl.grantee
        WHERE namespace.nspname = 'ascendany'
          AND relation.relkind IN ('r', 'p', 'v', 'm', 'f')
          AND attribute.attnum > 0
          AND NOT attribute.attisdropped
    ), expected AS (
        SELECT relation.oid AS object_oid,
               attribute.attnum,
               'ascendany_runtime'::text AS grantee_name,
               'UPDATE'::text AS privilege_type,
               false AS is_grantable
        FROM unnest(runtime_update_columns) AS expected(column_name)
        JOIN pg_namespace AS namespace ON namespace.nspname = 'ascendany'
        JOIN pg_class AS relation
          ON relation.relnamespace = namespace.oid
         AND relation.relname = split_part(expected.column_name, '.', 1)
         AND relation.relkind IN ('r', 'p')
        JOIN pg_attribute AS attribute
          ON attribute.attrelid = relation.oid
         AND attribute.attname = split_part(expected.column_name, '.', 2)
         AND attribute.attnum > 0
         AND NOT attribute.attisdropped
    ), difference AS (
        (SELECT * FROM actual EXCEPT ALL SELECT * FROM expected)
        UNION ALL
        (SELECT * FROM expected EXCEPT ALL SELECT * FROM actual)
    )
    SELECT count(*) INTO violation_count FROM difference;
    IF violation_count <> 0 THEN
        RAISE EXCEPTION '% column ACL entries differ from the exact v2 contract', violation_count;
    END IF;

    WITH routines AS (
        SELECT procedure.*
        FROM pg_proc AS procedure
        JOIN pg_namespace AS namespace ON namespace.oid = procedure.pronamespace
        WHERE namespace.nspname = 'ascendany'
    ), actual AS (
        SELECT procedure.oid AS object_oid,
               CASE WHEN acl.grantee = 0 THEN 'PUBLIC' ELSE grantee.rolname END AS grantee_name,
               acl.privilege_type,
               acl.is_grantable
        FROM routines AS procedure
        CROSS JOIN LATERAL aclexplode(COALESCE(procedure.proacl, acldefault('f', procedure.proowner))) AS acl
        LEFT JOIN pg_roles AS grantee ON grantee.oid = acl.grantee
    ), expected AS (
        SELECT procedure.oid AS object_oid,
               'ascendany_owner'::text AS grantee_name,
               'EXECUTE'::text AS privilege_type,
               false AS is_grantable
        FROM routines AS procedure
    ), difference AS (
        (SELECT * FROM actual EXCEPT ALL SELECT * FROM expected)
        UNION ALL
        (SELECT * FROM expected EXCEPT ALL SELECT * FROM actual)
    )
    SELECT count(*) INTO violation_count FROM difference;
    IF violation_count <> 0 THEN
        RAISE EXCEPTION '% routine ACL entries differ from the owner-only contract', violation_count;
    END IF;

    WITH types AS (
        SELECT type.*
        FROM pg_type AS type
        JOIN pg_namespace AS namespace ON namespace.oid = type.typnamespace
        WHERE namespace.nspname = 'ascendany'
          AND type.typelem = 0
    ), actual AS (
        SELECT type.oid AS object_oid,
               CASE WHEN acl.grantee = 0 THEN 'PUBLIC' ELSE grantee.rolname END AS grantee_name,
               acl.privilege_type,
               acl.is_grantable
        FROM types AS type
        CROSS JOIN LATERAL aclexplode(COALESCE(type.typacl, acldefault('T', type.typowner))) AS acl
        LEFT JOIN pg_roles AS grantee ON grantee.oid = acl.grantee
    ), expected AS (
        SELECT type.oid AS object_oid,
               grantee_name,
               'USAGE'::text AS privilege_type,
               false AS is_grantable
        FROM types AS type
        CROSS JOIN LATERAL unnest(
            ARRAY['ascendany_owner', 'ascendany_runtime', 'ascendany_backup']::text[]
        ) AS grantees(grantee_name)
    ), difference AS (
        (SELECT * FROM actual EXCEPT ALL SELECT * FROM expected)
        UNION ALL
        (SELECT * FROM expected EXCEPT ALL SELECT * FROM actual)
    )
    SELECT count(*) INTO violation_count FROM difference;
    IF violation_count <> 0 THEN
        RAISE EXCEPTION '% type ACL entries differ from the exact v2 contract', violation_count;
    END IF;

    SELECT count(*)
    INTO violation_count
    FROM pg_type AS type
    JOIN pg_namespace AS namespace ON namespace.oid = type.typnamespace
    WHERE namespace.nspname = 'ascendany'
      AND type.typelem <> 0
      AND (
          type.typacl IS NOT NULL
          OR NOT has_type_privilege('ascendany_owner', type.oid, 'USAGE')
          OR NOT has_type_privilege('ascendany_runtime', type.oid, 'USAGE')
          OR NOT has_type_privilege('ascendany_backup', type.oid, 'USAGE')
      );
    IF violation_count <> 0 THEN
        RAISE EXCEPTION '% array types do not derive the exact element-type ACL', violation_count;
    END IF;

    WITH actual AS (
        SELECT creator.rolname AS creator_name,
               namespace.nspname,
               defaults.defaclobjtype
        FROM pg_default_acl AS defaults
        JOIN pg_roles AS creator ON creator.oid = defaults.defaclrole
        LEFT JOIN pg_namespace AS namespace ON namespace.oid = defaults.defaclnamespace
    ), expected(creator_name, nspname, defaclobjtype) AS (
        VALUES
            ('ascendany_owner', NULL::name, 'f'::"char"),
            ('ascendany_owner', NULL::name, 'T'::"char"),
            ('ascendany_owner', 'ascendany'::name, 'r'::"char"),
            ('ascendany_owner', 'ascendany'::name, 'S'::"char"),
            ('ascendany_owner', 'ascendany'::name, 'T'::"char")
    ), difference AS (
        (SELECT * FROM actual EXCEPT ALL SELECT * FROM expected)
        UNION ALL
        (SELECT * FROM expected EXCEPT ALL SELECT * FROM actual)
    )
    SELECT count(*) INTO violation_count FROM difference;
    IF violation_count <> 0 THEN
        RAISE EXCEPTION 'default ACL row set differs from the exact v2 contract';
    END IF;

    WITH actual AS (
        SELECT creator.rolname AS creator_name,
               namespace.nspname,
               defaults.defaclobjtype,
               CASE WHEN acl.grantee = 0 THEN 'PUBLIC' ELSE grantee.rolname END AS grantee_name,
               acl.privilege_type,
               acl.is_grantable
        FROM pg_default_acl AS defaults
        JOIN pg_roles AS creator ON creator.oid = defaults.defaclrole
        LEFT JOIN pg_namespace AS namespace ON namespace.oid = defaults.defaclnamespace
        CROSS JOIN LATERAL aclexplode(defaults.defaclacl) AS acl
        LEFT JOIN pg_roles AS grantee ON grantee.oid = acl.grantee
    ), expected(creator_name, nspname, defaclobjtype, grantee_name, privilege_type, is_grantable) AS (
        VALUES
            ('ascendany_owner', 'ascendany'::name, 'r'::"char", 'ascendany_runtime', 'SELECT', false),
            ('ascendany_owner', 'ascendany'::name, 'r'::"char", 'ascendany_backup', 'SELECT', false),
            ('ascendany_owner', 'ascendany'::name, 'S'::"char", 'ascendany_runtime', 'SELECT', false),
            ('ascendany_owner', 'ascendany'::name, 'S'::"char", 'ascendany_runtime', 'USAGE', false),
            ('ascendany_owner', 'ascendany'::name, 'S'::"char", 'ascendany_backup', 'SELECT', false),
            ('ascendany_owner', 'ascendany'::name, 'T'::"char", 'ascendany_runtime', 'USAGE', false),
            ('ascendany_owner', 'ascendany'::name, 'T'::"char", 'ascendany_backup', 'USAGE', false)
    ), difference AS (
        (SELECT * FROM actual EXCEPT ALL SELECT * FROM expected)
        UNION ALL
        (SELECT * FROM expected EXCEPT ALL SELECT * FROM actual)
    )
    SELECT count(*) INTO violation_count FROM difference;
    IF violation_count <> 0 THEN
        RAISE EXCEPTION '% default ACL entries differ from the exact v2 contract', violation_count;
    END IF;
END
$verify$;

SELECT 'v2 PostgreSQL role boundary verified' AS result;
