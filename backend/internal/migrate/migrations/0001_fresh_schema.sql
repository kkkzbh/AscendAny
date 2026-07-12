SET LOCAL search_path = ascendany, pg_catalog;

DO $preflight$
DECLARE
    schema_owner text;
BEGIN
    IF current_database() <> 'ascendany_v2' THEN
        RAISE EXCEPTION 'fresh schema migration requires database ascendany_v2';
    END IF;
    IF current_user <> 'ascendany_owner' THEN
        RAISE EXCEPTION 'fresh schema migration requires current role ascendany_owner';
    END IF;

    SELECT pg_get_userbyid(nspowner)
    INTO schema_owner
    FROM pg_namespace
    WHERE nspname = 'ascendany';

    IF schema_owner IS NULL THEN
        RAISE EXCEPTION 'required schema ascendany is missing; run the v2 role bootstrap';
    END IF;
    IF schema_owner <> 'ascendany_owner' THEN
        RAISE EXCEPTION 'schema ascendany owner drift: %', schema_owner;
    END IF;
END
$preflight$;

CREATE FUNCTION ascendany.reject_immutable_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $function$
BEGIN
    RAISE EXCEPTION '% is immutable', TG_TABLE_SCHEMA || '.' || TG_TABLE_NAME
        USING ERRCODE = '55000';
END
$function$;

REVOKE ALL ON FUNCTION ascendany.reject_immutable_mutation() FROM PUBLIC;

CREATE TABLE ascendany.schema_migrations_v2 (
    version bigint PRIMARY KEY,
    name text NOT NULL UNIQUE,
    sha256 text NOT NULL,
    applied_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT schema_migrations_v2_version_positive CHECK (version > 0),
    CONSTRAINT schema_migrations_v2_name_format CHECK (name ~ '^[a-z][a-z0-9_]*$'),
    CONSTRAINT schema_migrations_v2_sha256_format CHECK (sha256 ~ '^[0-9a-f]{64}$')
);

CREATE TRIGGER schema_migrations_v2_immutable_rows
BEFORE UPDATE OR DELETE ON ascendany.schema_migrations_v2
FOR EACH ROW EXECUTE FUNCTION ascendany.reject_immutable_mutation();

CREATE TRIGGER schema_migrations_v2_immutable_truncate
BEFORE TRUNCATE ON ascendany.schema_migrations_v2
FOR EACH STATEMENT EXECUTE FUNCTION ascendany.reject_immutable_mutation();

REVOKE ALL ON TABLE ascendany.schema_migrations_v2 FROM ascendany_runtime, ascendany_backup;
GRANT SELECT ON TABLE ascendany.schema_migrations_v2 TO ascendany_runtime, ascendany_backup;

CREATE TABLE ascendany.artifacts (
    artifact_id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    sha256 text NOT NULL UNIQUE,
    size_bytes bigint NOT NULL,
    media_type text NOT NULL,
    storage_key text NOT NULL UNIQUE,
    published_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT artifacts_sha256_format CHECK (sha256 ~ '^[0-9a-f]{64}$'),
    CONSTRAINT artifacts_size_positive CHECK (size_bytes > 0),
    CONSTRAINT artifacts_media_type_nonempty CHECK (btrim(media_type) <> ''),
    CONSTRAINT artifacts_content_addressed_key CHECK (
        storage_key = 'sha256/' || substr(sha256, 1, 2) || '/' || sha256
    )
);

CREATE TABLE ascendany.logical_exams (
    exam_id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    public_id uuid NOT NULL UNIQUE,
    platform text NOT NULL,
    source_exam_id text NOT NULL,
    active_snapshot_id bigint,
    head_revision bigint NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT logical_exams_public_id_nonzero CHECK (public_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    CONSTRAINT logical_exams_platform_pintia CHECK (platform = 'pintia'),
    CONSTRAINT logical_exams_source_id_nonempty CHECK (btrim(source_exam_id) <> ''),
    CONSTRAINT logical_exams_head_revision_nonnegative CHECK (head_revision >= 0),
    CONSTRAINT logical_exams_active_head_consistent CHECK (
        (active_snapshot_id IS NULL AND head_revision = 0)
        OR (active_snapshot_id IS NOT NULL AND head_revision > 0)
    ),
    CONSTRAINT logical_exams_source_unique UNIQUE (platform, source_exam_id),
    CONSTRAINT logical_exams_id_active_unique UNIQUE (exam_id, active_snapshot_id)
);

CREATE TABLE ascendany.import_jobs (
    import_job_id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    public_id uuid NOT NULL UNIQUE,
    artifact_id bigint NOT NULL,
    job_kind text NOT NULL,
    status text NOT NULL,
    stage text NOT NULL,
    attempt_count integer NOT NULL DEFAULT 0,
    lease_owner text,
    lease_expires_at timestamptz,
    next_attempt_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    snapshot_id bigint,
    error_code text,
    error_detail text,
    error_permanent boolean,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    started_at timestamptz,
    finished_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT import_jobs_public_id_nonzero CHECK (public_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    CONSTRAINT import_jobs_artifact_fk FOREIGN KEY (artifact_id)
        REFERENCES ascendany.artifacts (artifact_id) ON DELETE RESTRICT,
    CONSTRAINT import_jobs_kind_v2 CHECK (job_kind = 'pintia_snapshot_v2'),
    CONSTRAINT import_jobs_status_valid CHECK (
        status IN ('queued', 'running', 'succeeded', 'failed', 'superseded')
    ),
    CONSTRAINT import_jobs_stage_valid CHECK (
        stage IN ('received', 'validating', 'importing', 'analyzing', 'completed', 'failed', 'superseded')
    ),
    CONSTRAINT import_jobs_status_stage_consistent CHECK (
        (status = 'queued' AND stage = 'received')
        OR (status = 'running' AND stage IN ('validating', 'importing', 'analyzing'))
        OR (status = 'succeeded' AND stage = 'completed')
        OR (status = 'failed' AND stage = 'failed')
        OR (status = 'superseded' AND stage = 'superseded')
    ),
    CONSTRAINT import_jobs_attempt_nonnegative CHECK (attempt_count >= 0),
    CONSTRAINT import_jobs_lease_consistent CHECK (
        (lease_owner IS NULL AND lease_expires_at IS NULL)
        OR (lease_owner IS NOT NULL AND btrim(lease_owner) <> '' AND lease_expires_at IS NOT NULL)
    ),
    CONSTRAINT import_jobs_finished_consistent CHECK (
        (status IN ('succeeded', 'failed', 'superseded') AND finished_at IS NOT NULL)
        OR (status IN ('queued', 'running') AND finished_at IS NULL)
    ),
    CONSTRAINT import_jobs_error_consistent CHECK (
        (
            status = 'failed'
            AND error_code IS NOT NULL
            AND btrim(error_code) <> ''
            AND error_detail IS NOT NULL
            AND btrim(error_detail) <> ''
            AND error_permanent IS NOT NULL
        )
        OR (
            status <> 'failed'
            AND error_code IS NULL
            AND error_detail IS NULL
            AND error_permanent IS NULL
        )
    ),
    CONSTRAINT import_jobs_artifact_kind_unique UNIQUE (artifact_id, job_kind),
    CONSTRAINT import_jobs_id_artifact_unique UNIQUE (import_job_id, artifact_id)
);

CREATE INDEX import_jobs_dispatch_idx
ON ascendany.import_jobs (next_attempt_at, import_job_id)
WHERE status = 'queued';

CREATE INDEX import_jobs_expired_lease_idx
ON ascendany.import_jobs (lease_expires_at, import_job_id)
WHERE status = 'running';

CREATE TABLE ascendany.import_job_events (
    import_job_id bigint NOT NULL,
    event_sequence bigint NOT NULL,
    event_type text NOT NULL,
    payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (import_job_id, event_sequence),
    CONSTRAINT import_job_events_job_fk FOREIGN KEY (import_job_id)
        REFERENCES ascendany.import_jobs (import_job_id) ON DELETE RESTRICT,
    CONSTRAINT import_job_events_sequence_positive CHECK (event_sequence > 0),
    CONSTRAINT import_job_events_type_format CHECK (event_type ~ '^[a-z][a-z0-9_.-]{0,63}$'),
    CONSTRAINT import_job_events_payload_object CHECK (jsonb_typeof(payload) = 'object')
);

CREATE INDEX import_job_events_created_idx
ON ascendany.import_job_events (import_job_id, created_at, event_sequence);

CREATE TABLE ascendany.pintia_actors (
    actor_id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id text NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT pintia_actors_user_id_nonempty CHECK (btrim(user_id) <> '')
);

CREATE TABLE ascendany.pintia_actor_identifiers (
    identifier_kind text NOT NULL,
    identifier_value text NOT NULL,
    actor_id bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (identifier_kind, identifier_value),
    CONSTRAINT pintia_actor_identifiers_actor_fk FOREIGN KEY (actor_id)
        REFERENCES ascendany.pintia_actors (actor_id) ON DELETE RESTRICT,
    CONSTRAINT pintia_actor_identifiers_kind_valid CHECK (
        identifier_kind IN ('student_user_id', 'student_number')
    ),
    CONSTRAINT pintia_actor_identifiers_value_nonempty CHECK (btrim(identifier_value) <> ''),
    CONSTRAINT pintia_actor_identifiers_one_per_kind UNIQUE (actor_id, identifier_kind),
    CONSTRAINT pintia_actor_identifiers_binding_unique UNIQUE (
        identifier_kind,
        identifier_value,
        actor_id
    )
);

CREATE INDEX pintia_actor_identifiers_actor_idx
ON ascendany.pintia_actor_identifiers (actor_id, identifier_kind);

CREATE TABLE ascendany.auth_accounts (
    account_id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    public_id uuid NOT NULL UNIQUE,
    username text NOT NULL UNIQUE,
    password_phc text NOT NULL,
    display_name text NOT NULL,
    student_number text UNIQUE,
    actor_id bigint UNIQUE,
    student_identifier_kind text GENERATED ALWAYS AS (
        CASE WHEN role = 'student' THEN 'student_number'::text ELSE NULL END
    ) STORED,
    role text NOT NULL,
    auth_revision bigint NOT NULL,
    disabled_at timestamptz,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    CONSTRAINT auth_accounts_public_id_nonzero CHECK (
        public_id <> '00000000-0000-0000-0000-000000000000'::uuid
    ),
    CONSTRAINT auth_accounts_username_format CHECK (username ~ '^[a-z0-9_]{3,32}$'),
    CONSTRAINT auth_accounts_display_name_valid CHECK (
        display_name = btrim(display_name)
        AND octet_length(display_name) BETWEEN 1 AND 64
    ),
    CONSTRAINT auth_accounts_actor_fk FOREIGN KEY (actor_id)
        REFERENCES ascendany.pintia_actors (actor_id) ON DELETE RESTRICT,
    CONSTRAINT auth_accounts_student_identifier_fk FOREIGN KEY (
        student_identifier_kind,
        student_number,
        actor_id
    ) REFERENCES ascendany.pintia_actor_identifiers (
        identifier_kind,
        identifier_value,
        actor_id
    ) MATCH FULL ON DELETE RESTRICT,
    CONSTRAINT auth_accounts_role_student_number_consistent CHECK (
        (
            role = 'student'
            AND actor_id IS NOT NULL
            AND student_number IS NOT NULL
            AND student_number = btrim(student_number)
            AND octet_length(student_number) BETWEEN 1 AND 64
        )
        OR (role = 'admin' AND actor_id IS NULL AND student_number IS NULL)
    ),
    CONSTRAINT auth_accounts_revision_positive CHECK (auth_revision > 0),
    CONSTRAINT auth_accounts_disabled_after_creation CHECK (
        disabled_at IS NULL OR disabled_at >= created_at
    ),
    CONSTRAINT auth_accounts_time_order CHECK (updated_at >= created_at),
    CONSTRAINT auth_accounts_id_role_unique UNIQUE (account_id, role),
    CONSTRAINT auth_accounts_id_actor_unique UNIQUE (account_id, actor_id)
);

CREATE TABLE ascendany.auth_enrollment_grants (
    enrollment_grant_id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    public_id uuid NOT NULL UNIQUE,
    secret_digest bytea NOT NULL UNIQUE,
    username text NOT NULL,
    display_name text NOT NULL,
    student_number text NOT NULL,
    actor_id bigint NOT NULL,
    student_identifier_kind text GENERATED ALWAYS AS ('student_number'::text) STORED,
    issuer_account_id bigint NOT NULL,
    issuer_role text NOT NULL,
    issued_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    CONSTRAINT auth_enrollment_grants_public_id_nonzero CHECK (
        public_id <> '00000000-0000-0000-0000-000000000000'::uuid
    ),
    CONSTRAINT auth_enrollment_grants_secret_digest_sha256 CHECK (
        octet_length(secret_digest) = 32
    ),
    CONSTRAINT auth_enrollment_grants_username_format CHECK (
        username ~ '^[a-z0-9_]{3,32}$'
    ),
    CONSTRAINT auth_enrollment_grants_display_name_valid CHECK (
        display_name = btrim(display_name)
        AND octet_length(display_name) BETWEEN 1 AND 64
    ),
    CONSTRAINT auth_enrollment_grants_student_number_valid CHECK (
        student_number = btrim(student_number)
        AND octet_length(student_number) BETWEEN 1 AND 64
    ),
    CONSTRAINT auth_enrollment_grants_student_identifier_fk FOREIGN KEY (
        student_identifier_kind,
        student_number,
        actor_id
    ) REFERENCES ascendany.pintia_actor_identifiers (
        identifier_kind,
        identifier_value,
        actor_id
    ) ON DELETE RESTRICT,
    CONSTRAINT auth_enrollment_grants_issuer_admin CHECK (issuer_role = 'admin'),
    CONSTRAINT auth_enrollment_grants_issuer_fk FOREIGN KEY (issuer_account_id, issuer_role)
        REFERENCES ascendany.auth_accounts (account_id, role) ON DELETE RESTRICT,
    CONSTRAINT auth_enrollment_grants_expiry_order CHECK (expires_at > issued_at),
    CONSTRAINT auth_enrollment_grants_max_lifetime CHECK (
        expires_at <= issued_at + interval '7 days'
    )
);

CREATE INDEX auth_enrollment_grants_username_expiry_idx
ON ascendany.auth_enrollment_grants (username, expires_at DESC, enrollment_grant_id DESC);

CREATE INDEX auth_enrollment_grants_student_expiry_idx
ON ascendany.auth_enrollment_grants (student_number, expires_at DESC, enrollment_grant_id DESC);

CREATE INDEX auth_enrollment_grants_actor_expiry_idx
ON ascendany.auth_enrollment_grants (actor_id, expires_at DESC, enrollment_grant_id DESC);

CREATE TABLE ascendany.auth_sessions (
    session_id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    public_id uuid NOT NULL UNIQUE,
    account_id bigint NOT NULL,
    auth_revision bigint NOT NULL,
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL,
    revoked_at timestamptz,
    revocation_reason text,
    CONSTRAINT auth_sessions_public_id_nonzero CHECK (
        public_id <> '00000000-0000-0000-0000-000000000000'::uuid
    ),
    CONSTRAINT auth_sessions_account_fk FOREIGN KEY (account_id)
        REFERENCES ascendany.auth_accounts (account_id) ON DELETE RESTRICT,
    CONSTRAINT auth_sessions_revision_positive CHECK (auth_revision > 0),
    CONSTRAINT auth_sessions_expiry_order CHECK (expires_at > created_at),
    CONSTRAINT auth_sessions_last_seen_order CHECK (
        last_seen_at >= created_at AND last_seen_at < expires_at
    ),
    CONSTRAINT auth_sessions_revoked_after_creation CHECK (
        revoked_at IS NULL OR revoked_at >= created_at
    ),
    CONSTRAINT auth_sessions_revocation_consistent CHECK (
        (revoked_at IS NULL) = (revocation_reason IS NULL)
    ),
    CONSTRAINT auth_sessions_id_account_unique UNIQUE (session_id, account_id)
);

CREATE INDEX auth_sessions_account_created_idx
ON ascendany.auth_sessions (account_id, created_at);

CREATE INDEX auth_sessions_active_idx
ON ascendany.auth_sessions (account_id, expires_at)
WHERE revoked_at IS NULL;

ALTER TABLE ascendany.auth_enrollment_grants
ADD COLUMN issuer_session_id bigint NOT NULL,
ADD CONSTRAINT auth_enrollment_grants_issuer_session_fk FOREIGN KEY (
    issuer_session_id,
    issuer_account_id
) REFERENCES ascendany.auth_sessions (
    session_id,
    account_id
) ON DELETE RESTRICT,
ADD CONSTRAINT auth_enrollment_grants_issuer_binding_unique UNIQUE (
    enrollment_grant_id,
    issuer_account_id,
    issuer_session_id
),
ADD CONSTRAINT auth_enrollment_grants_id_actor_unique UNIQUE (
    enrollment_grant_id,
    actor_id
);

CREATE TABLE ascendany.auth_enrollment_events (
    enrollment_event_id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    enrollment_grant_id bigint NOT NULL,
    event_type text NOT NULL,
    event_slot smallint GENERATED ALWAYS AS (
        CASE event_type
            WHEN 'issued' THEN 0
            WHEN 'revoked' THEN 1
            WHEN 'consumed' THEN 1
        END
    ) STORED NOT NULL,
    actor_account_id bigint NOT NULL,
    actor_role text NOT NULL,
    session_id bigint NOT NULL,
    subject_actor_id bigint,
    issued_grant_id bigint GENERATED ALWAYS AS (
        CASE WHEN event_type = 'issued' THEN enrollment_grant_id ELSE NULL END
    ) STORED,
    issued_actor_account_id bigint GENERATED ALWAYS AS (
        CASE WHEN event_type = 'issued' THEN actor_account_id ELSE NULL END
    ) STORED,
    issued_session_id bigint GENERATED ALWAYS AS (
        CASE WHEN event_type = 'issued' THEN session_id ELSE NULL END
    ) STORED,
    occurred_at timestamptz NOT NULL,
    CONSTRAINT auth_enrollment_events_grant_fk FOREIGN KEY (enrollment_grant_id)
        REFERENCES ascendany.auth_enrollment_grants (enrollment_grant_id) ON DELETE RESTRICT,
    CONSTRAINT auth_enrollment_events_actor_fk FOREIGN KEY (actor_account_id, actor_role)
        REFERENCES ascendany.auth_accounts (account_id, role) ON DELETE RESTRICT,
    CONSTRAINT auth_enrollment_events_session_fk FOREIGN KEY (session_id, actor_account_id)
        REFERENCES ascendany.auth_sessions (session_id, account_id) ON DELETE RESTRICT,
    CONSTRAINT auth_enrollment_events_issued_binding_fk FOREIGN KEY (
        issued_grant_id,
        issued_actor_account_id,
        issued_session_id
    ) REFERENCES ascendany.auth_enrollment_grants (
        enrollment_grant_id,
        issuer_account_id,
        issuer_session_id
    ) MATCH FULL ON DELETE RESTRICT,
    CONSTRAINT auth_enrollment_events_consumed_grant_actor_fk FOREIGN KEY (
        enrollment_grant_id,
        subject_actor_id
    ) REFERENCES ascendany.auth_enrollment_grants (
        enrollment_grant_id,
        actor_id
    ) ON DELETE RESTRICT,
    CONSTRAINT auth_enrollment_events_consumed_account_actor_fk FOREIGN KEY (
        actor_account_id,
        subject_actor_id
    ) REFERENCES ascendany.auth_accounts (
        account_id,
        actor_id
    ) ON DELETE RESTRICT,
    CONSTRAINT auth_enrollment_events_type_valid CHECK (
        event_type IN ('issued', 'revoked', 'consumed')
    ),
    CONSTRAINT auth_enrollment_events_slot_valid CHECK (event_slot IN (0, 1)),
    CONSTRAINT auth_enrollment_events_actor_role_consistent CHECK (
        (event_type IN ('issued', 'revoked') AND actor_role = 'admin')
        OR (event_type = 'consumed' AND actor_role = 'student')
    ),
    CONSTRAINT auth_enrollment_events_subject_actor_consistent CHECK (
        (event_type = 'consumed' AND subject_actor_id IS NOT NULL)
        OR (event_type IN ('issued', 'revoked') AND subject_actor_id IS NULL)
    ),
    CONSTRAINT auth_enrollment_events_one_issued_one_terminal UNIQUE (
        enrollment_grant_id,
        event_slot
    )
);

CREATE INDEX auth_enrollment_events_occurred_idx
ON ascendany.auth_enrollment_events (enrollment_grant_id, occurred_at, enrollment_event_id);

CREATE FUNCTION ascendany.validate_auth_enrollment_state()
RETURNS trigger
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $function$
DECLARE
    target_grant_id bigint;
    grant_issued_at timestamptz;
    grant_expires_at timestamptz;
    issued_count bigint;
    issued_event_at timestamptz;
    invalid_terminal_count bigint;
    invalid_actor_session_count bigint;
BEGIN
    target_grant_id := NEW.enrollment_grant_id;

    SELECT enrollment.issued_at, enrollment.expires_at
    INTO grant_issued_at, grant_expires_at
    FROM ascendany.auth_enrollment_grants AS enrollment
    WHERE enrollment.enrollment_grant_id = target_grant_id;

    IF NOT FOUND THEN
        RAISE EXCEPTION 'enrollment grant % is missing', target_grant_id
            USING ERRCODE = '23514';
    END IF;

    SELECT count(*), min(event.occurred_at)
    INTO issued_count, issued_event_at
    FROM ascendany.auth_enrollment_events AS event
    WHERE event.enrollment_grant_id = target_grant_id
      AND event.event_type = 'issued';

    IF issued_count <> 1 OR issued_event_at <> grant_issued_at THEN
        RAISE EXCEPTION 'enrollment grant % must own exactly one matching issued event', target_grant_id
            USING ERRCODE = '23514';
    END IF;

    SELECT count(*)
    INTO invalid_terminal_count
    FROM ascendany.auth_enrollment_events AS event
    WHERE event.enrollment_grant_id = target_grant_id
      AND event.event_slot = 1
      AND (
          event.occurred_at < grant_issued_at
          OR event.occurred_at >= grant_expires_at
      );

    IF invalid_terminal_count <> 0 THEN
        RAISE EXCEPTION 'enrollment grant % owns a terminal event outside its active interval', target_grant_id
            USING ERRCODE = '23514';
    END IF;

    IF TG_TABLE_NAME = 'auth_enrollment_events' THEN
        SELECT count(*)
        INTO invalid_actor_session_count
        FROM ascendany.auth_enrollment_events AS event
        JOIN ascendany.auth_sessions AS session
          ON session.session_id = event.session_id
         AND session.account_id = event.actor_account_id
        JOIN ascendany.auth_accounts AS account
          ON account.account_id = event.actor_account_id
        WHERE event.enrollment_event_id = NEW.enrollment_event_id
          AND (
              event.occurred_at < session.created_at
              OR event.occurred_at >= session.expires_at
              OR (session.revoked_at IS NOT NULL AND event.occurred_at >= session.revoked_at)
              OR session.auth_revision <> account.auth_revision
              OR (account.disabled_at IS NOT NULL AND event.occurred_at >= account.disabled_at)
          );

        IF invalid_actor_session_count <> 0 THEN
            RAISE EXCEPTION 'enrollment grant % owns a new event outside its actor session', target_grant_id
                USING ERRCODE = '23514';
        END IF;
    END IF;

    RETURN NULL;
END
$function$;

REVOKE ALL ON FUNCTION ascendany.validate_auth_enrollment_state() FROM PUBLIC;

CREATE CONSTRAINT TRIGGER auth_enrollment_grants_complete_state
AFTER INSERT ON ascendany.auth_enrollment_grants
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION ascendany.validate_auth_enrollment_state();

CREATE CONSTRAINT TRIGGER auth_enrollment_events_complete_state
AFTER INSERT ON ascendany.auth_enrollment_events
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION ascendany.validate_auth_enrollment_state();

CREATE TABLE ascendany.auth_refresh_tokens (
    refresh_token_id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    public_id uuid NOT NULL UNIQUE,
    session_id bigint NOT NULL,
    secret_digest bytea NOT NULL,
    csrf_digest bytea NOT NULL,
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    used_at timestamptz,
    replaced_by_token_id bigint,
    revoked_at timestamptz,
    CONSTRAINT auth_refresh_tokens_public_id_nonzero CHECK (
        public_id <> '00000000-0000-0000-0000-000000000000'::uuid
    ),
    CONSTRAINT auth_refresh_tokens_session_fk FOREIGN KEY (session_id)
        REFERENCES ascendany.auth_sessions (session_id) ON DELETE RESTRICT,
    CONSTRAINT auth_refresh_tokens_id_session_unique UNIQUE (refresh_token_id, session_id),
    CONSTRAINT auth_refresh_tokens_replacement_fk FOREIGN KEY (
        replaced_by_token_id,
        session_id
    ) REFERENCES ascendany.auth_refresh_tokens (
        refresh_token_id,
        session_id
    ) ON DELETE RESTRICT,
    CONSTRAINT auth_refresh_tokens_secret_digest_sha256 CHECK (octet_length(secret_digest) = 32),
    CONSTRAINT auth_refresh_tokens_csrf_digest_sha256 CHECK (octet_length(csrf_digest) = 32),
    CONSTRAINT auth_refresh_tokens_expiry_order CHECK (expires_at > created_at),
    CONSTRAINT auth_refresh_tokens_use_replacement_consistent CHECK (
        (used_at IS NULL) = (replaced_by_token_id IS NULL)
    ),
    CONSTRAINT auth_refresh_tokens_replacement_not_self CHECK (
        replaced_by_token_id IS NULL OR replaced_by_token_id <> refresh_token_id
    ),
    CONSTRAINT auth_refresh_tokens_used_time_valid CHECK (
        used_at IS NULL OR (used_at >= created_at AND used_at < expires_at)
    ),
    CONSTRAINT auth_refresh_tokens_revoked_after_creation CHECK (
        revoked_at IS NULL OR revoked_at >= created_at
    )
);

CREATE INDEX auth_refresh_tokens_session_created_idx
ON ascendany.auth_refresh_tokens (session_id, created_at);

CREATE INDEX auth_refresh_tokens_active_idx
ON ascendany.auth_refresh_tokens (session_id, expires_at)
WHERE used_at IS NULL AND revoked_at IS NULL;

CREATE TABLE ascendany.audit_events (
    audit_event_id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    account_id bigint,
    session_id bigint,
    event_type text NOT NULL,
    occurred_at timestamptz NOT NULL,
    payload jsonb NOT NULL,
    CONSTRAINT audit_events_account_fk FOREIGN KEY (account_id)
        REFERENCES ascendany.auth_accounts (account_id) ON DELETE RESTRICT,
    CONSTRAINT audit_events_session_requires_account CHECK (
        session_id IS NULL OR account_id IS NOT NULL
    ),
    CONSTRAINT audit_events_session_fk FOREIGN KEY (session_id, account_id)
        REFERENCES ascendany.auth_sessions (session_id, account_id) ON DELETE RESTRICT,
    CONSTRAINT audit_events_type_nonempty CHECK (btrim(event_type) <> ''),
    CONSTRAINT audit_events_payload_object CHECK (jsonb_typeof(payload) = 'object')
);

CREATE INDEX audit_events_account_occurred_idx
ON ascendany.audit_events (account_id, occurred_at, audit_event_id);

CREATE INDEX audit_events_session_occurred_idx
ON ascendany.audit_events (session_id, occurred_at, audit_event_id);

CREATE TABLE ascendany.exam_snapshots (
    snapshot_id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    public_id uuid NOT NULL UNIQUE,
    exam_id bigint NOT NULL,
    snapshot_sequence bigint NOT NULL,
    source_artifact_id bigint NOT NULL UNIQUE,
    import_job_id bigint NOT NULL UNIQUE,
    contract_schema text NOT NULL,
    contract_schema_sha256 text NOT NULL,
    domain_hash_protocol text NOT NULL,
    domain_hash text NOT NULL,
    exporter_name text NOT NULL,
    exporter_version text NOT NULL,
    exported_at timestamptz NOT NULL,
    title text NOT NULL,
    source_url text NOT NULL,
    starts_at timestamptz,
    ends_at timestamptz,
    total_score numeric,
    problems_source_count bigint,
    problems_observed_count bigint NOT NULL,
    problems_exported_count bigint NOT NULL,
    problems_pagination_exhausted boolean NOT NULL,
    rankings_source_count bigint,
    rankings_observed_count bigint NOT NULL,
    rankings_exported_count bigint NOT NULL,
    rankings_pagination_exhausted boolean NOT NULL,
    submissions_source_count bigint,
    submissions_observed_count bigint NOT NULL,
    submissions_exported_count bigint NOT NULL,
    submissions_pagination_exhausted boolean NOT NULL,
    participants_exported_count bigint NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT exam_snapshots_public_id_nonzero CHECK (public_id <> '00000000-0000-0000-0000-000000000000'::uuid),
    CONSTRAINT exam_snapshots_exam_fk FOREIGN KEY (exam_id)
        REFERENCES ascendany.logical_exams (exam_id) ON DELETE RESTRICT,
    CONSTRAINT exam_snapshots_artifact_job_fk FOREIGN KEY (import_job_id, source_artifact_id)
        REFERENCES ascendany.import_jobs (import_job_id, artifact_id) ON DELETE RESTRICT,
    CONSTRAINT exam_snapshots_sequence_positive CHECK (snapshot_sequence > 0),
    CONSTRAINT exam_snapshots_contract_v2 CHECK (contract_schema = 'ascendany.pintia.snapshot.v2'),
    CONSTRAINT exam_snapshots_contract_hash CHECK (contract_schema_sha256 ~ '^[0-9a-f]{64}$'),
    CONSTRAINT exam_snapshots_domain_protocol_v1 CHECK (domain_hash_protocol = 'domain_hash_proto_v1'),
    CONSTRAINT exam_snapshots_domain_hash CHECK (domain_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT exam_snapshots_exporter CHECK (exporter_name = 'ascendany-pintia-exporter'),
    CONSTRAINT exam_snapshots_title_nonempty CHECK (btrim(title) <> ''),
    CONSTRAINT exam_snapshots_source_url_pintia CHECK (source_url ~ '^https://pintia[.]cn/'),
    CONSTRAINT exam_snapshots_time_order CHECK (starts_at IS NULL OR ends_at IS NULL OR starts_at <= ends_at),
    CONSTRAINT exam_snapshots_total_score_nonnegative CHECK (total_score IS NULL OR total_score >= 0),
    CONSTRAINT exam_snapshots_problem_counts CHECK (
        problems_observed_count >= problems_exported_count
        AND problems_exported_count > 0
        AND problems_pagination_exhausted
        AND (problems_source_count IS NULL OR problems_source_count = problems_observed_count)
    ),
    CONSTRAINT exam_snapshots_ranking_counts CHECK (
        rankings_observed_count >= rankings_exported_count
        AND rankings_exported_count >= 0
        AND rankings_pagination_exhausted
        AND (rankings_source_count IS NULL OR rankings_source_count = rankings_observed_count)
    ),
    CONSTRAINT exam_snapshots_submission_counts CHECK (
        submissions_observed_count >= submissions_exported_count
        AND submissions_exported_count >= 0
        AND submissions_pagination_exhausted
        AND (submissions_source_count IS NULL OR submissions_source_count = submissions_observed_count)
    ),
    CONSTRAINT exam_snapshots_participant_count_nonnegative CHECK (participants_exported_count >= 0),
    CONSTRAINT exam_snapshots_exam_sequence_unique UNIQUE (exam_id, snapshot_sequence),
    CONSTRAINT exam_snapshots_exam_domain_unique UNIQUE (exam_id, domain_hash_protocol, domain_hash),
    CONSTRAINT exam_snapshots_id_domain_unique UNIQUE (snapshot_id, domain_hash),
    CONSTRAINT exam_snapshots_exam_id_unique UNIQUE (exam_id, snapshot_id),
    CONSTRAINT exam_snapshots_id_exam_unique UNIQUE (snapshot_id, exam_id)
);

ALTER TABLE ascendany.logical_exams
ADD CONSTRAINT logical_exams_active_snapshot_fk
FOREIGN KEY (exam_id, active_snapshot_id)
REFERENCES ascendany.exam_snapshots (exam_id, snapshot_id)
ON DELETE RESTRICT
DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE ascendany.import_jobs
ADD CONSTRAINT import_jobs_snapshot_fk
FOREIGN KEY (snapshot_id)
REFERENCES ascendany.exam_snapshots (snapshot_id)
ON DELETE RESTRICT
DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE ascendany.import_jobs
ADD CONSTRAINT import_jobs_success_snapshot_consistent
CHECK ((status = 'succeeded') = (snapshot_id IS NOT NULL));

CREATE INDEX exam_snapshots_exam_created_idx
ON ascendany.exam_snapshots (exam_id, created_at DESC, snapshot_id DESC);

CREATE TABLE ascendany.pintia_snapshot_problems (
    snapshot_id bigint NOT NULL,
    problem_set_problem_id text NOT NULL,
    problem_id text NOT NULL,
    label text,
    title text NOT NULL,
    problem_type text NOT NULL,
    max_score numeric,
    content_html text,
    time_limit_ms bigint,
    memory_limit_bytes bigint,
    PRIMARY KEY (snapshot_id, problem_set_problem_id),
    CONSTRAINT pintia_snapshot_problems_snapshot_fk FOREIGN KEY (snapshot_id)
        REFERENCES ascendany.exam_snapshots (snapshot_id) ON DELETE RESTRICT,
    CONSTRAINT pintia_snapshot_problems_ids_nonempty CHECK (
        btrim(problem_set_problem_id) <> '' AND btrim(problem_id) <> ''
    ),
    CONSTRAINT pintia_snapshot_problems_title_nonempty CHECK (btrim(title) <> ''),
    CONSTRAINT pintia_snapshot_problems_type_programming CHECK (problem_type = 'PROGRAMMING'),
    CONSTRAINT pintia_snapshot_problems_score_nonnegative CHECK (max_score IS NULL OR max_score >= 0),
    CONSTRAINT pintia_snapshot_problems_time_nonnegative CHECK (time_limit_ms IS NULL OR time_limit_ms >= 0),
    CONSTRAINT pintia_snapshot_problems_memory_nonnegative CHECK (memory_limit_bytes IS NULL OR memory_limit_bytes >= 0),
    CONSTRAINT pintia_snapshot_problems_source_unique UNIQUE (snapshot_id, problem_id)
);

CREATE TABLE ascendany.pintia_snapshot_participants (
    snapshot_id bigint NOT NULL,
    actor_id bigint NOT NULL,
    student_user_id text,
    student_number text,
    display_name text,
    group_name text,
    PRIMARY KEY (snapshot_id, actor_id),
    CONSTRAINT pintia_snapshot_participants_snapshot_fk FOREIGN KEY (snapshot_id)
        REFERENCES ascendany.exam_snapshots (snapshot_id) ON DELETE RESTRICT,
    CONSTRAINT pintia_snapshot_participants_actor_fk FOREIGN KEY (actor_id)
        REFERENCES ascendany.pintia_actors (actor_id) ON DELETE RESTRICT,
    CONSTRAINT pintia_snapshot_participants_student_user_nonempty CHECK (
        student_user_id IS NULL OR btrim(student_user_id) <> ''
    ),
    CONSTRAINT pintia_snapshot_participants_student_number_nonempty CHECK (
        student_number IS NULL OR btrim(student_number) <> ''
    )
);

CREATE INDEX pintia_snapshot_participants_actor_idx
ON ascendany.pintia_snapshot_participants (actor_id, snapshot_id);

CREATE TABLE ascendany.pintia_rankings (
    snapshot_id bigint NOT NULL,
    actor_id bigint NOT NULL,
    rank bigint NOT NULL,
    total_score numeric,
    time_used_seconds bigint,
    PRIMARY KEY (snapshot_id, actor_id),
    CONSTRAINT pintia_rankings_participant_fk FOREIGN KEY (snapshot_id, actor_id)
        REFERENCES ascendany.pintia_snapshot_participants (snapshot_id, actor_id) ON DELETE RESTRICT,
    CONSTRAINT pintia_rankings_rank_positive CHECK (rank > 0),
    CONSTRAINT pintia_rankings_score_nonnegative CHECK (total_score IS NULL OR total_score >= 0),
    CONSTRAINT pintia_rankings_time_nonnegative CHECK (time_used_seconds IS NULL OR time_used_seconds >= 0)
);

CREATE INDEX pintia_rankings_rank_idx
ON ascendany.pintia_rankings (snapshot_id, rank, actor_id);

CREATE TABLE ascendany.pintia_ranking_problem_results (
    snapshot_id bigint NOT NULL,
    actor_id bigint NOT NULL,
    problem_set_problem_id text NOT NULL,
    score numeric,
    passed boolean,
    valid_submission_count bigint,
    accept_time_seconds bigint NOT NULL,
    PRIMARY KEY (snapshot_id, actor_id, problem_set_problem_id),
    CONSTRAINT pintia_ranking_results_ranking_fk FOREIGN KEY (snapshot_id, actor_id)
        REFERENCES ascendany.pintia_rankings (snapshot_id, actor_id) ON DELETE RESTRICT,
    CONSTRAINT pintia_ranking_results_problem_fk FOREIGN KEY (snapshot_id, problem_set_problem_id)
        REFERENCES ascendany.pintia_snapshot_problems (snapshot_id, problem_set_problem_id) ON DELETE RESTRICT,
    CONSTRAINT pintia_ranking_results_score_nonnegative CHECK (score IS NULL OR score >= 0),
    CONSTRAINT pintia_ranking_results_count_nonnegative CHECK (
        valid_submission_count IS NULL OR valid_submission_count >= 0
    ),
    CONSTRAINT pintia_ranking_results_accept_time_nonnegative CHECK (accept_time_seconds >= 0)
);

CREATE TABLE ascendany.pintia_submission_identities (
    submission_identity_id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    submission_id text NOT NULL UNIQUE,
    exam_id bigint NOT NULL,
    actor_id bigint NOT NULL,
    problem_set_problem_id text NOT NULL,
    submitted_at timestamptz NOT NULL,
    code text NOT NULL,
    code_sha256 text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT pintia_submission_identities_exam_fk FOREIGN KEY (exam_id)
        REFERENCES ascendany.logical_exams (exam_id) ON DELETE RESTRICT,
    CONSTRAINT pintia_submission_identities_actor_fk FOREIGN KEY (actor_id)
        REFERENCES ascendany.pintia_actors (actor_id) ON DELETE RESTRICT,
    CONSTRAINT pintia_submission_identities_source_id_nonempty CHECK (btrim(submission_id) <> ''),
    CONSTRAINT pintia_submission_identities_problem_id_nonempty CHECK (btrim(problem_set_problem_id) <> ''),
    CONSTRAINT pintia_submission_identities_code_hash CHECK (code_sha256 ~ '^[0-9a-f]{64}$'),
    CONSTRAINT pintia_submission_identities_invariant_unique UNIQUE (
        submission_identity_id,
        exam_id,
        actor_id,
        problem_set_problem_id
    )
);

CREATE INDEX pintia_submission_identities_exam_time_idx
ON ascendany.pintia_submission_identities (exam_id, submitted_at, submission_identity_id);

CREATE INDEX pintia_submission_identities_actor_time_idx
ON ascendany.pintia_submission_identities (actor_id, submitted_at, submission_identity_id);

CREATE TABLE ascendany.pintia_snapshot_submissions (
    snapshot_id bigint NOT NULL,
    exam_id bigint NOT NULL,
    submission_identity_id bigint NOT NULL,
    actor_id bigint NOT NULL,
    problem_set_problem_id text NOT NULL,
    language text,
    compiler text,
    verdict text NOT NULL,
    score numeric,
    time_ms bigint,
    memory_bytes bigint,
    compile_log text,
    PRIMARY KEY (snapshot_id, submission_identity_id),
    CONSTRAINT pintia_snapshot_submissions_snapshot_exam_fk FOREIGN KEY (snapshot_id, exam_id)
        REFERENCES ascendany.exam_snapshots (snapshot_id, exam_id) ON DELETE RESTRICT,
    CONSTRAINT pintia_snapshot_submissions_identity_fk FOREIGN KEY (
        submission_identity_id,
        exam_id,
        actor_id,
        problem_set_problem_id
    ) REFERENCES ascendany.pintia_submission_identities (
        submission_identity_id,
        exam_id,
        actor_id,
        problem_set_problem_id
    ) ON DELETE RESTRICT,
    CONSTRAINT pintia_snapshot_submissions_participant_fk FOREIGN KEY (snapshot_id, actor_id)
        REFERENCES ascendany.pintia_snapshot_participants (snapshot_id, actor_id) ON DELETE RESTRICT,
    CONSTRAINT pintia_snapshot_submissions_problem_fk FOREIGN KEY (snapshot_id, problem_set_problem_id)
        REFERENCES ascendany.pintia_snapshot_problems (snapshot_id, problem_set_problem_id) ON DELETE RESTRICT,
    CONSTRAINT pintia_snapshot_submissions_verdict_nonempty CHECK (btrim(verdict) <> ''),
    CONSTRAINT pintia_snapshot_submissions_score_nonnegative CHECK (score IS NULL OR score >= 0),
    CONSTRAINT pintia_snapshot_submissions_time_nonnegative CHECK (time_ms IS NULL OR time_ms >= 0),
    CONSTRAINT pintia_snapshot_submissions_memory_nonnegative CHECK (memory_bytes IS NULL OR memory_bytes >= 0)
);

CREATE INDEX pintia_snapshot_submissions_problem_idx
ON ascendany.pintia_snapshot_submissions (snapshot_id, problem_set_problem_id, submission_identity_id);

CREATE INDEX pintia_snapshot_submissions_actor_idx
ON ascendany.pintia_snapshot_submissions (snapshot_id, actor_id, submission_identity_id);

CREATE TABLE ascendany.pintia_submission_case_results (
    snapshot_id bigint NOT NULL,
    submission_identity_id bigint NOT NULL,
    case_id text NOT NULL,
    verdict text,
    score numeric,
    time_ms bigint,
    memory_bytes bigint,
    message text,
    PRIMARY KEY (snapshot_id, submission_identity_id, case_id),
    CONSTRAINT pintia_submission_case_results_submission_fk FOREIGN KEY (
        snapshot_id,
        submission_identity_id
    ) REFERENCES ascendany.pintia_snapshot_submissions (
        snapshot_id,
        submission_identity_id
    ) ON DELETE RESTRICT,
    CONSTRAINT pintia_submission_case_results_case_nonempty CHECK (btrim(case_id) <> ''),
    CONSTRAINT pintia_submission_case_results_score_nonnegative CHECK (score IS NULL OR score >= 0),
    CONSTRAINT pintia_submission_case_results_time_nonnegative CHECK (time_ms IS NULL OR time_ms >= 0),
    CONSTRAINT pintia_submission_case_results_memory_nonnegative CHECK (memory_bytes IS NULL OR memory_bytes >= 0)
);

CREATE TABLE ascendany.analytics_generations (
    analytics_generation_id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    status text NOT NULL,
    base_analytics_generation_id bigint,
    base_head_revision bigint NOT NULL,
    target_exam_id bigint NOT NULL,
    target_snapshot_id bigint NOT NULL,
    target_exam_head_revision bigint NOT NULL,
    input_manifest jsonb NOT NULL,
    input_manifest_sha256 text NOT NULL,
    algorithm_version text NOT NULL,
    config_sha256 text NOT NULL,
    error_code text,
    error_detail text,
    attempt_count integer NOT NULL DEFAULT 0,
    lease_owner text,
    lease_expires_at timestamptz,
    next_attempt_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    started_at timestamptz,
    finished_at timestamptz,
    CONSTRAINT analytics_generations_base_fk FOREIGN KEY (base_analytics_generation_id)
        REFERENCES ascendany.analytics_generations (analytics_generation_id) ON DELETE RESTRICT,
    CONSTRAINT analytics_generations_target_fk FOREIGN KEY (target_exam_id, target_snapshot_id)
        REFERENCES ascendany.exam_snapshots (exam_id, snapshot_id) ON DELETE RESTRICT,
    CONSTRAINT analytics_generations_status_valid CHECK (
        status IN ('queued', 'running', 'succeeded', 'superseded', 'failed')
    ),
    CONSTRAINT analytics_generations_attempt_nonnegative CHECK (attempt_count >= 0),
    CONSTRAINT analytics_generations_attempt_state_consistent CHECK (
        (status = 'queued' AND attempt_count = 0 AND started_at IS NULL)
        OR (status <> 'queued' AND attempt_count > 0 AND started_at IS NOT NULL)
    ),
    CONSTRAINT analytics_generations_lease_consistent CHECK (
        (
            status = 'running'
            AND lease_owner IS NOT NULL
            AND btrim(lease_owner) <> ''
            AND lease_expires_at IS NOT NULL
        )
        OR (
            status <> 'running'
            AND lease_owner IS NULL
            AND lease_expires_at IS NULL
        )
    ),
    CONSTRAINT analytics_generations_base_revision_nonnegative CHECK (base_head_revision >= 0),
    CONSTRAINT analytics_generations_target_revision_positive CHECK (target_exam_head_revision > 0),
    CONSTRAINT analytics_generations_base_consistent CHECK (
        (base_analytics_generation_id IS NULL AND base_head_revision = 0)
        OR (base_analytics_generation_id IS NOT NULL AND base_head_revision > 0)
    ),
    CONSTRAINT analytics_generations_manifest_object CHECK (jsonb_typeof(input_manifest) = 'object'),
    CONSTRAINT analytics_generations_manifest_hash CHECK (input_manifest_sha256 ~ '^[0-9a-f]{64}$'),
    CONSTRAINT analytics_generations_algorithm_nonempty CHECK (btrim(algorithm_version) <> ''),
    CONSTRAINT analytics_generations_config_hash CHECK (config_sha256 ~ '^[0-9a-f]{64}$'),
    CONSTRAINT analytics_generations_finished_consistent CHECK (
        (status IN ('succeeded', 'superseded', 'failed') AND finished_at IS NOT NULL)
        OR (status IN ('queued', 'running') AND finished_at IS NULL)
    ),
    CONSTRAINT analytics_generations_failure_consistent CHECK (
        (
            status = 'failed'
            AND error_code IS NOT NULL
            AND btrim(error_code) <> ''
        )
        OR (
            status <> 'failed'
            AND error_code IS NULL
            AND error_detail IS NULL
        )
    ),
    CONSTRAINT analytics_generations_input_unique UNIQUE (
        target_snapshot_id,
        input_manifest_sha256,
        algorithm_version,
        config_sha256
    )
);

CREATE INDEX analytics_generations_queued_claim_idx
ON ascendany.analytics_generations (next_attempt_at, analytics_generation_id)
WHERE status = 'queued';

CREATE INDEX analytics_generations_expired_lease_idx
ON ascendany.analytics_generations (lease_expires_at, analytics_generation_id)
WHERE status = 'running';

CREATE TABLE ascendany.analytics_generation_snapshots (
    analytics_generation_id bigint NOT NULL,
    exam_id bigint NOT NULL,
    snapshot_id bigint NOT NULL,
    domain_hash text NOT NULL,
    PRIMARY KEY (analytics_generation_id, exam_id),
    CONSTRAINT analytics_generation_snapshots_generation_fk FOREIGN KEY (analytics_generation_id)
        REFERENCES ascendany.analytics_generations (analytics_generation_id) ON DELETE RESTRICT,
    CONSTRAINT analytics_generation_snapshots_snapshot_fk FOREIGN KEY (exam_id, snapshot_id)
        REFERENCES ascendany.exam_snapshots (exam_id, snapshot_id) ON DELETE RESTRICT,
    CONSTRAINT analytics_generation_snapshots_domain_fk FOREIGN KEY (snapshot_id, domain_hash)
        REFERENCES ascendany.exam_snapshots (snapshot_id, domain_hash) ON DELETE RESTRICT,
    CONSTRAINT analytics_generation_snapshots_domain_hash CHECK (domain_hash ~ '^[0-9a-f]{64}$'),
    CONSTRAINT analytics_generation_snapshots_snapshot_unique UNIQUE (analytics_generation_id, snapshot_id)
);

CREATE TABLE ascendany.student_analytics (
    analytics_generation_id bigint NOT NULL,
    actor_id bigint NOT NULL,
    rating numeric NOT NULL,
    metrics jsonb NOT NULL,
    PRIMARY KEY (analytics_generation_id, actor_id),
    CONSTRAINT student_analytics_generation_fk FOREIGN KEY (analytics_generation_id)
        REFERENCES ascendany.analytics_generations (analytics_generation_id) ON DELETE RESTRICT,
    CONSTRAINT student_analytics_actor_fk FOREIGN KEY (actor_id)
        REFERENCES ascendany.pintia_actors (actor_id) ON DELETE RESTRICT,
    CONSTRAINT student_analytics_rating_finite CHECK (rating >= 0),
    CONSTRAINT student_analytics_metrics_object CHECK (jsonb_typeof(metrics) = 'object')
);

CREATE TABLE ascendany.problem_analytics (
    analytics_generation_id bigint NOT NULL,
    snapshot_id bigint NOT NULL,
    problem_set_problem_id text NOT NULL,
    metrics jsonb NOT NULL,
    PRIMARY KEY (analytics_generation_id, snapshot_id, problem_set_problem_id),
    CONSTRAINT problem_analytics_generation_fk FOREIGN KEY (analytics_generation_id)
        REFERENCES ascendany.analytics_generations (analytics_generation_id) ON DELETE RESTRICT,
    CONSTRAINT problem_analytics_problem_fk FOREIGN KEY (snapshot_id, problem_set_problem_id)
        REFERENCES ascendany.pintia_snapshot_problems (snapshot_id, problem_set_problem_id) ON DELETE RESTRICT,
    CONSTRAINT problem_analytics_metrics_object CHECK (jsonb_typeof(metrics) = 'object')
);

CREATE TABLE ascendany.analytics_head (
    singleton boolean PRIMARY KEY DEFAULT true,
    current_generation_id bigint,
    head_revision bigint NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT analytics_head_singleton CHECK (singleton),
    CONSTRAINT analytics_head_generation_fk FOREIGN KEY (current_generation_id)
        REFERENCES ascendany.analytics_generations (analytics_generation_id) ON DELETE RESTRICT,
    CONSTRAINT analytics_head_revision_nonnegative CHECK (head_revision >= 0),
    CONSTRAINT analytics_head_consistent CHECK (
        (current_generation_id IS NULL AND head_revision = 0)
        OR (current_generation_id IS NOT NULL AND head_revision > 0)
    )
);

INSERT INTO ascendany.analytics_head (singleton, current_generation_id, head_revision)
VALUES (true, NULL, 0);

CREATE TRIGGER artifacts_immutable_rows
BEFORE UPDATE OR DELETE ON ascendany.artifacts
FOR EACH ROW EXECUTE FUNCTION ascendany.reject_immutable_mutation();
CREATE TRIGGER artifacts_immutable_truncate
BEFORE TRUNCATE ON ascendany.artifacts
FOR EACH STATEMENT EXECUTE FUNCTION ascendany.reject_immutable_mutation();

CREATE TRIGGER import_job_events_immutable_rows
BEFORE UPDATE OR DELETE ON ascendany.import_job_events
FOR EACH ROW EXECUTE FUNCTION ascendany.reject_immutable_mutation();
CREATE TRIGGER import_job_events_immutable_truncate
BEFORE TRUNCATE ON ascendany.import_job_events
FOR EACH STATEMENT EXECUTE FUNCTION ascendany.reject_immutable_mutation();

CREATE TRIGGER audit_events_immutable_rows
BEFORE UPDATE OR DELETE ON ascendany.audit_events
FOR EACH ROW EXECUTE FUNCTION ascendany.reject_immutable_mutation();
CREATE TRIGGER audit_events_immutable_truncate
BEFORE TRUNCATE ON ascendany.audit_events
FOR EACH STATEMENT EXECUTE FUNCTION ascendany.reject_immutable_mutation();

CREATE TRIGGER auth_enrollment_grants_immutable_rows
BEFORE UPDATE OR DELETE ON ascendany.auth_enrollment_grants
FOR EACH ROW EXECUTE FUNCTION ascendany.reject_immutable_mutation();
CREATE TRIGGER auth_enrollment_grants_immutable_truncate
BEFORE TRUNCATE ON ascendany.auth_enrollment_grants
FOR EACH STATEMENT EXECUTE FUNCTION ascendany.reject_immutable_mutation();

CREATE TRIGGER auth_enrollment_events_immutable_rows
BEFORE UPDATE OR DELETE ON ascendany.auth_enrollment_events
FOR EACH ROW EXECUTE FUNCTION ascendany.reject_immutable_mutation();
CREATE TRIGGER auth_enrollment_events_immutable_truncate
BEFORE TRUNCATE ON ascendany.auth_enrollment_events
FOR EACH STATEMENT EXECUTE FUNCTION ascendany.reject_immutable_mutation();

CREATE TRIGGER pintia_actors_immutable_rows
BEFORE UPDATE OR DELETE ON ascendany.pintia_actors
FOR EACH ROW EXECUTE FUNCTION ascendany.reject_immutable_mutation();
CREATE TRIGGER pintia_actors_immutable_truncate
BEFORE TRUNCATE ON ascendany.pintia_actors
FOR EACH STATEMENT EXECUTE FUNCTION ascendany.reject_immutable_mutation();

CREATE TRIGGER pintia_actor_identifiers_immutable_rows
BEFORE UPDATE OR DELETE ON ascendany.pintia_actor_identifiers
FOR EACH ROW EXECUTE FUNCTION ascendany.reject_immutable_mutation();
CREATE TRIGGER pintia_actor_identifiers_immutable_truncate
BEFORE TRUNCATE ON ascendany.pintia_actor_identifiers
FOR EACH STATEMENT EXECUTE FUNCTION ascendany.reject_immutable_mutation();

CREATE TRIGGER exam_snapshots_immutable_rows
BEFORE UPDATE OR DELETE ON ascendany.exam_snapshots
FOR EACH ROW EXECUTE FUNCTION ascendany.reject_immutable_mutation();
CREATE TRIGGER exam_snapshots_immutable_truncate
BEFORE TRUNCATE ON ascendany.exam_snapshots
FOR EACH STATEMENT EXECUTE FUNCTION ascendany.reject_immutable_mutation();

CREATE TRIGGER pintia_snapshot_problems_immutable_rows
BEFORE UPDATE OR DELETE ON ascendany.pintia_snapshot_problems
FOR EACH ROW EXECUTE FUNCTION ascendany.reject_immutable_mutation();
CREATE TRIGGER pintia_snapshot_problems_immutable_truncate
BEFORE TRUNCATE ON ascendany.pintia_snapshot_problems
FOR EACH STATEMENT EXECUTE FUNCTION ascendany.reject_immutable_mutation();

CREATE TRIGGER pintia_snapshot_participants_immutable_rows
BEFORE UPDATE OR DELETE ON ascendany.pintia_snapshot_participants
FOR EACH ROW EXECUTE FUNCTION ascendany.reject_immutable_mutation();
CREATE TRIGGER pintia_snapshot_participants_immutable_truncate
BEFORE TRUNCATE ON ascendany.pintia_snapshot_participants
FOR EACH STATEMENT EXECUTE FUNCTION ascendany.reject_immutable_mutation();

CREATE TRIGGER pintia_rankings_immutable_rows
BEFORE UPDATE OR DELETE ON ascendany.pintia_rankings
FOR EACH ROW EXECUTE FUNCTION ascendany.reject_immutable_mutation();
CREATE TRIGGER pintia_rankings_immutable_truncate
BEFORE TRUNCATE ON ascendany.pintia_rankings
FOR EACH STATEMENT EXECUTE FUNCTION ascendany.reject_immutable_mutation();

CREATE TRIGGER pintia_ranking_problem_results_immutable_rows
BEFORE UPDATE OR DELETE ON ascendany.pintia_ranking_problem_results
FOR EACH ROW EXECUTE FUNCTION ascendany.reject_immutable_mutation();
CREATE TRIGGER pintia_ranking_problem_results_immutable_truncate
BEFORE TRUNCATE ON ascendany.pintia_ranking_problem_results
FOR EACH STATEMENT EXECUTE FUNCTION ascendany.reject_immutable_mutation();

CREATE TRIGGER pintia_submission_identities_immutable_rows
BEFORE UPDATE OR DELETE ON ascendany.pintia_submission_identities
FOR EACH ROW EXECUTE FUNCTION ascendany.reject_immutable_mutation();
CREATE TRIGGER pintia_submission_identities_immutable_truncate
BEFORE TRUNCATE ON ascendany.pintia_submission_identities
FOR EACH STATEMENT EXECUTE FUNCTION ascendany.reject_immutable_mutation();

CREATE TRIGGER pintia_snapshot_submissions_immutable_rows
BEFORE UPDATE OR DELETE ON ascendany.pintia_snapshot_submissions
FOR EACH ROW EXECUTE FUNCTION ascendany.reject_immutable_mutation();
CREATE TRIGGER pintia_snapshot_submissions_immutable_truncate
BEFORE TRUNCATE ON ascendany.pintia_snapshot_submissions
FOR EACH STATEMENT EXECUTE FUNCTION ascendany.reject_immutable_mutation();

CREATE TRIGGER pintia_submission_case_results_immutable_rows
BEFORE UPDATE OR DELETE ON ascendany.pintia_submission_case_results
FOR EACH ROW EXECUTE FUNCTION ascendany.reject_immutable_mutation();
CREATE TRIGGER pintia_submission_case_results_immutable_truncate
BEFORE TRUNCATE ON ascendany.pintia_submission_case_results
FOR EACH STATEMENT EXECUTE FUNCTION ascendany.reject_immutable_mutation();

CREATE TRIGGER analytics_generation_snapshots_immutable_rows
BEFORE UPDATE OR DELETE ON ascendany.analytics_generation_snapshots
FOR EACH ROW EXECUTE FUNCTION ascendany.reject_immutable_mutation();
CREATE TRIGGER analytics_generation_snapshots_immutable_truncate
BEFORE TRUNCATE ON ascendany.analytics_generation_snapshots
FOR EACH STATEMENT EXECUTE FUNCTION ascendany.reject_immutable_mutation();

CREATE TRIGGER student_analytics_immutable_rows
BEFORE UPDATE OR DELETE ON ascendany.student_analytics
FOR EACH ROW EXECUTE FUNCTION ascendany.reject_immutable_mutation();
CREATE TRIGGER student_analytics_immutable_truncate
BEFORE TRUNCATE ON ascendany.student_analytics
FOR EACH STATEMENT EXECUTE FUNCTION ascendany.reject_immutable_mutation();

CREATE TRIGGER problem_analytics_immutable_rows
BEFORE UPDATE OR DELETE ON ascendany.problem_analytics
FOR EACH ROW EXECUTE FUNCTION ascendany.reject_immutable_mutation();
CREATE TRIGGER problem_analytics_immutable_truncate
BEFORE TRUNCATE ON ascendany.problem_analytics
FOR EACH STATEMENT EXECUTE FUNCTION ascendany.reject_immutable_mutation();

REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA ascendany FROM ascendany_runtime;
GRANT SELECT ON ALL TABLES IN SCHEMA ascendany TO ascendany_runtime;
GRANT INSERT ON TABLE
    ascendany.artifacts,
    ascendany.logical_exams,
    ascendany.import_jobs,
    ascendany.import_job_events,
    ascendany.pintia_actors,
    ascendany.pintia_actor_identifiers,
    ascendany.auth_accounts,
    ascendany.auth_enrollment_grants,
    ascendany.auth_enrollment_events,
    ascendany.auth_sessions,
    ascendany.auth_refresh_tokens,
    ascendany.audit_events,
    ascendany.exam_snapshots,
    ascendany.pintia_snapshot_problems,
    ascendany.pintia_snapshot_participants,
    ascendany.pintia_rankings,
    ascendany.pintia_ranking_problem_results,
    ascendany.pintia_submission_identities,
    ascendany.pintia_snapshot_submissions,
    ascendany.pintia_submission_case_results,
    ascendany.analytics_generations,
    ascendany.analytics_generation_snapshots,
    ascendany.student_analytics,
    ascendany.problem_analytics
TO ascendany_runtime;
GRANT UPDATE (
    active_snapshot_id,
    head_revision,
    updated_at
) ON TABLE ascendany.logical_exams TO ascendany_runtime;

GRANT UPDATE (
    status,
    stage,
    attempt_count,
    lease_owner,
    lease_expires_at,
    next_attempt_at,
    snapshot_id,
    error_code,
    error_detail,
    error_permanent,
    started_at,
    finished_at,
    updated_at
) ON TABLE ascendany.import_jobs TO ascendany_runtime;

GRANT UPDATE (
    password_phc,
    display_name,
    auth_revision,
    disabled_at,
    updated_at
) ON TABLE ascendany.auth_accounts TO ascendany_runtime;

GRANT UPDATE (
    last_seen_at,
    revoked_at,
    revocation_reason
) ON TABLE ascendany.auth_sessions TO ascendany_runtime;

GRANT UPDATE (
    used_at,
    replaced_by_token_id,
    revoked_at
) ON TABLE ascendany.auth_refresh_tokens TO ascendany_runtime;

GRANT UPDATE (
    status,
    error_code,
    error_detail,
    attempt_count,
    lease_owner,
    lease_expires_at,
    next_attempt_at,
    started_at,
    finished_at
) ON TABLE ascendany.analytics_generations TO ascendany_runtime;

GRANT UPDATE (
    current_generation_id,
    head_revision,
    updated_at
) ON TABLE ascendany.analytics_head TO ascendany_runtime;

REVOKE ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA ascendany FROM ascendany_runtime;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA ascendany TO ascendany_runtime;

REVOKE ALL PRIVILEGES ON ALL TABLES IN SCHEMA ascendany FROM ascendany_backup;
GRANT SELECT ON ALL TABLES IN SCHEMA ascendany TO ascendany_backup;
REVOKE ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA ascendany FROM ascendany_backup;
GRANT SELECT ON ALL SEQUENCES IN SCHEMA ascendany TO ascendany_backup;
