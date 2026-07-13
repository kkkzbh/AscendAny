SET LOCAL search_path = ascendany, pg_catalog;

DO $preflight$
DECLARE
    schema_owner text;
BEGIN
    IF current_database() <> 'ascendany_v2' THEN
        RAISE EXCEPTION 'product domains migration requires database ascendany_v2';
    END IF;
    IF current_user <> 'ascendany_owner' THEN
        RAISE EXCEPTION 'product domains migration requires current role ascendany_owner';
    END IF;

    SELECT pg_get_userbyid(nspowner)
    INTO schema_owner
    FROM pg_namespace
    WHERE nspname = 'ascendany';

    IF schema_owner IS DISTINCT FROM 'ascendany_owner' THEN
        RAISE EXCEPTION 'schema ascendany owner drift: %', schema_owner;
    END IF;
    IF NOT EXISTS (
        SELECT 1
        FROM ascendany.schema_migrations_v2
        WHERE version = 1
          AND name = 'fresh_schema'
    ) THEN
        RAISE EXCEPTION 'product domains migration requires fresh schema version 1';
    END IF;
END
$preflight$;

CREATE FUNCTION ascendany.require_single_head_revision_step()
RETURNS trigger
LANGUAGE plpgsql
AS $function$
BEGIN
    IF NEW.head_revision <> OLD.head_revision + 1 THEN
        RAISE EXCEPTION '% head revision must advance by exactly one', TG_TABLE_SCHEMA || '.' || TG_TABLE_NAME
            USING ERRCODE = '40001';
    END IF;
    RETURN NEW;
END
$function$;

REVOKE ALL ON FUNCTION ascendany.require_single_head_revision_step() FROM PUBLIC;

CREATE FUNCTION ascendany.reject_terminal_job_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $function$
BEGIN
    IF OLD.status = ANY (TG_ARGV) THEN
        RAISE EXCEPTION '% terminal job % is immutable', TG_TABLE_SCHEMA || '.' || TG_TABLE_NAME, OLD.status
            USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END
$function$;

REVOKE ALL ON FUNCTION ascendany.reject_terminal_job_mutation() FROM PUBLIC;

CREATE FUNCTION ascendany.enforce_fenced_job_transition()
RETURNS trigger
LANGUAGE plpgsql
AS $function$
BEGIN
    IF OLD.status = 'queued' AND NEW.status = 'running' THEN
        IF NEW.attempt_count <> OLD.attempt_count + 1
           OR NEW.attempt_token IS NULL
           OR NEW.lease_owner IS NULL
           OR NEW.lease_expires_at IS NULL
           OR NEW.started_at IS NULL THEN
            RAISE EXCEPTION '% claim must create one fenced attempt', TG_TABLE_SCHEMA || '.' || TG_TABLE_NAME
                USING ERRCODE = '40001';
        END IF;
    ELSIF OLD.status = 'running' AND NEW.status = 'running' THEN
        IF NEW.attempt_count = OLD.attempt_count THEN
            IF NEW.attempt_token IS DISTINCT FROM OLD.attempt_token
               OR NEW.lease_owner IS DISTINCT FROM OLD.lease_owner
               OR NEW.started_at IS DISTINCT FROM OLD.started_at THEN
                RAISE EXCEPTION '% lease renewal changed its attempt fence', TG_TABLE_SCHEMA || '.' || TG_TABLE_NAME
                    USING ERRCODE = '40001';
            END IF;
        ELSIF NEW.attempt_count = OLD.attempt_count + 1 THEN
            IF OLD.lease_expires_at > clock_timestamp()
               OR NEW.attempt_token IS NULL
               OR NEW.attempt_token IS NOT DISTINCT FROM OLD.attempt_token
               OR NEW.lease_owner IS NULL
               OR NEW.lease_expires_at IS NULL
               OR NEW.started_at IS DISTINCT FROM OLD.started_at THEN
                RAISE EXCEPTION '% reclaim did not replace an expired attempt fence', TG_TABLE_SCHEMA || '.' || TG_TABLE_NAME
                    USING ERRCODE = '40001';
            END IF;
        ELSE
            RAISE EXCEPTION '% running attempt count changed non-monotonically', TG_TABLE_SCHEMA || '.' || TG_TABLE_NAME
                USING ERRCODE = '40001';
        END IF;
    ELSIF OLD.status = 'running' AND NEW.status <> 'running' THEN
        IF NEW.attempt_count IS DISTINCT FROM OLD.attempt_count
           OR NEW.started_at IS DISTINCT FROM OLD.started_at THEN
            RAISE EXCEPTION '% completion or retry changed its attempt fence', TG_TABLE_SCHEMA || '.' || TG_TABLE_NAME
                USING ERRCODE = '40001';
        END IF;
    ELSE
        RAISE EXCEPTION '% invalid job transition % -> %',
            TG_TABLE_SCHEMA || '.' || TG_TABLE_NAME,
            OLD.status,
            NEW.status
            USING ERRCODE = '40001';
    END IF;
    RETURN NEW;
END
$function$;

REVOKE ALL ON FUNCTION ascendany.enforce_fenced_job_transition() FROM PUBLIC;

CREATE FUNCTION ascendany.enforce_initial_zero_head()
RETURNS trigger
LANGUAGE plpgsql
AS $function$
DECLARE
    row_state jsonb := to_jsonb(NEW);
BEGIN
    IF NEW.head_revision <> 0 THEN
        RAISE EXCEPTION '% must be inserted at head revision zero', TG_TABLE_SCHEMA || '.' || TG_TABLE_NAME
            USING ERRCODE = '23514';
    END IF;
    IF TG_NARGS = 1 AND row_state ->> TG_ARGV[0] IS NOT NULL THEN
        RAISE EXCEPTION '% initial head pointer must be null', TG_TABLE_SCHEMA || '.' || TG_TABLE_NAME
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END
$function$;

REVOKE ALL ON FUNCTION ascendany.enforce_initial_zero_head() FROM PUBLIC;

CREATE FUNCTION ascendany.enforce_initial_queued_job()
RETURNS trigger
LANGUAGE plpgsql
AS $function$
DECLARE
    row_state jsonb := to_jsonb(NEW);
BEGIN
    IF NEW.status <> 'queued'
       OR NEW.attempt_count <> 0
       OR NEW.lease_owner IS NOT NULL
       OR NEW.lease_expires_at IS NOT NULL
       OR NEW.started_at IS NOT NULL
       OR NEW.finished_at IS NOT NULL
       OR NEW.error_code IS NOT NULL
       OR NEW.error_detail IS NOT NULL
       OR (row_state ? 'attempt_token' AND row_state ->> 'attempt_token' IS NOT NULL)
       OR (TG_NARGS = 1 AND row_state ->> 'stage' IS DISTINCT FROM TG_ARGV[0]) THEN
        RAISE EXCEPTION '% must be inserted as an unclaimed queued job', TG_TABLE_SCHEMA || '.' || TG_TABLE_NAME
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END
$function$;

REVOKE ALL ON FUNCTION ascendany.enforce_initial_queued_job() FROM PUBLIC;

CREATE FUNCTION ascendany.enforce_import_job_transition()
RETURNS trigger
LANGUAGE plpgsql
AS $function$
BEGIN
    IF OLD.status IN ('succeeded', 'superseded', 'failed') THEN
        RAISE EXCEPTION '% terminal job % is immutable', TG_TABLE_SCHEMA || '.' || TG_TABLE_NAME, OLD.status
            USING ERRCODE = '55000';
    ELSIF OLD.status = 'queued' AND NEW.status = 'running' THEN
        IF OLD.stage <> 'received'
           OR NEW.stage <> 'validating'
           OR NEW.attempt_count <> OLD.attempt_count + 1
           OR NEW.lease_owner IS NULL
           OR NEW.lease_expires_at IS NULL
           OR NEW.started_at IS NULL
           OR (OLD.started_at IS NOT NULL AND NEW.started_at IS DISTINCT FROM OLD.started_at) THEN
            RAISE EXCEPTION 'import claim must create one validating leased attempt'
                USING ERRCODE = '40001';
        END IF;
    ELSIF OLD.status = 'running' AND NEW.status = 'running' THEN
        IF NEW.attempt_count = OLD.attempt_count THEN
            IF OLD.stage = 'importing' AND NEW.stage = 'analyzing' THEN
                IF OLD.lease_owner IS NULL
                   OR OLD.lease_expires_at IS NULL
                   OR OLD.lease_expires_at <= clock_timestamp()
                   OR NEW.lease_owner IS NOT NULL
                   OR NEW.lease_expires_at IS NOT NULL
                   OR NEW.started_at IS DISTINCT FROM OLD.started_at THEN
                    RAISE EXCEPTION 'import analyzing handoff did not release the active import lease'
                        USING ERRCODE = '40001';
                END IF;
            ELSIF (OLD.stage = 'validating' AND NEW.stage IN ('validating', 'importing'))
               OR (OLD.stage = 'importing' AND NEW.stage = 'importing') THEN
                IF NEW.lease_owner IS DISTINCT FROM OLD.lease_owner
                   OR NEW.lease_owner IS NULL
                   OR NEW.lease_expires_at IS NULL
                   OR NEW.started_at IS DISTINCT FROM OLD.started_at THEN
                    RAISE EXCEPTION 'import lease update changed its attempt identity'
                        USING ERRCODE = '40001';
                END IF;
            ELSE
                RAISE EXCEPTION 'invalid running import stage transition % -> %', OLD.stage, NEW.stage
                    USING ERRCODE = '40001';
            END IF;
        ELSIF NEW.attempt_count = OLD.attempt_count + 1 THEN
            IF OLD.stage NOT IN ('validating', 'importing')
               OR NEW.stage IS DISTINCT FROM OLD.stage
               OR OLD.lease_expires_at IS NULL
               OR OLD.lease_expires_at > clock_timestamp()
               OR NEW.lease_owner IS NULL
               OR NEW.lease_expires_at IS NULL
               OR NEW.started_at IS DISTINCT FROM OLD.started_at THEN
                RAISE EXCEPTION 'import reclaim did not replace one expired attempt'
                    USING ERRCODE = '40001';
            END IF;
        ELSE
            RAISE EXCEPTION 'import attempt count changed non-monotonically'
                USING ERRCODE = '40001';
        END IF;
    ELSIF OLD.status = 'running' AND NEW.status = 'queued' THEN
        IF NEW.attempt_count IS DISTINCT FROM OLD.attempt_count
           OR OLD.stage NOT IN ('validating', 'importing')
           OR NEW.stage <> 'received'
           OR NEW.lease_owner IS NOT NULL
           OR NEW.lease_expires_at IS NOT NULL
           OR NEW.started_at IS DISTINCT FROM OLD.started_at
           OR NEW.finished_at IS NOT NULL THEN
            RAISE EXCEPTION 'import retry did not return the active attempt to received'
                USING ERRCODE = '40001';
        END IF;
    ELSIF OLD.status = 'running' AND NEW.status IN ('succeeded', 'superseded', 'failed') THEN
        IF NEW.attempt_count IS DISTINCT FROM OLD.attempt_count
           OR NEW.started_at IS DISTINCT FROM OLD.started_at
           OR NEW.lease_owner IS NOT NULL
           OR NEW.lease_expires_at IS NOT NULL
           OR NEW.finished_at IS NULL
           OR (NEW.status = 'succeeded' AND (OLD.stage <> 'analyzing' OR NEW.stage <> 'completed'))
           OR (NEW.status = 'superseded' AND (OLD.stage NOT IN ('importing', 'analyzing') OR NEW.stage <> 'superseded'))
           OR (NEW.status = 'failed' AND NEW.stage <> 'failed') THEN
            RAISE EXCEPTION 'import completion violated the stage lifecycle'
                USING ERRCODE = '40001';
        END IF;
    ELSE
        RAISE EXCEPTION 'invalid import job transition %/% -> %/%',
            OLD.status, OLD.stage, NEW.status, NEW.stage
            USING ERRCODE = '40001';
    END IF;
    RETURN NEW;
END
$function$;

REVOKE ALL ON FUNCTION ascendany.enforce_import_job_transition() FROM PUBLIC;

CREATE FUNCTION ascendany.enforce_analytics_generation_transition()
RETURNS trigger
LANGUAGE plpgsql
AS $function$
BEGIN
    IF OLD.status IN ('succeeded', 'superseded', 'failed') THEN
        RAISE EXCEPTION 'terminal analytics generation % is immutable', OLD.status
            USING ERRCODE = '55000';
    ELSIF OLD.status = 'queued' AND NEW.status = 'running' THEN
        IF NEW.attempt_count <> OLD.attempt_count + 1
           OR NEW.lease_owner IS NULL
           OR NEW.lease_expires_at IS NULL
           OR NEW.started_at IS NULL
           OR (OLD.started_at IS NOT NULL AND NEW.started_at IS DISTINCT FROM OLD.started_at) THEN
            RAISE EXCEPTION 'analytics claim must create one leased attempt'
                USING ERRCODE = '40001';
        END IF;
    ELSIF OLD.status = 'running' AND NEW.status = 'running' THEN
        IF NEW.attempt_count = OLD.attempt_count THEN
            IF NEW.lease_owner IS DISTINCT FROM OLD.lease_owner
               OR NEW.lease_owner IS NULL
               OR NEW.lease_expires_at IS NULL
               OR NEW.started_at IS DISTINCT FROM OLD.started_at THEN
                RAISE EXCEPTION 'analytics lease renewal changed its attempt identity'
                    USING ERRCODE = '40001';
            END IF;
        ELSIF NEW.attempt_count = OLD.attempt_count + 1 THEN
            IF OLD.lease_expires_at IS NULL
               OR OLD.lease_expires_at > clock_timestamp()
               OR NEW.lease_owner IS NULL
               OR NEW.lease_expires_at IS NULL
               OR NEW.started_at IS DISTINCT FROM OLD.started_at THEN
                RAISE EXCEPTION 'analytics reclaim did not replace one expired attempt'
                    USING ERRCODE = '40001';
            END IF;
        ELSE
            RAISE EXCEPTION 'analytics attempt count changed non-monotonically'
                USING ERRCODE = '40001';
        END IF;
    ELSIF OLD.status = 'running' AND NEW.status IN ('succeeded', 'superseded', 'failed') THEN
        IF NEW.attempt_count IS DISTINCT FROM OLD.attempt_count
           OR NEW.started_at IS DISTINCT FROM OLD.started_at
           OR NEW.lease_owner IS NOT NULL
           OR NEW.lease_expires_at IS NOT NULL
           OR NEW.finished_at IS NULL THEN
            RAISE EXCEPTION 'analytics completion changed its attempt identity'
                USING ERRCODE = '40001';
        END IF;
    ELSE
        RAISE EXCEPTION 'invalid analytics generation transition % -> %', OLD.status, NEW.status
            USING ERRCODE = '40001';
    END IF;
    RETURN NEW;
END
$function$;

REVOKE ALL ON FUNCTION ascendany.enforce_analytics_generation_transition() FROM PUBLIC;

CREATE FUNCTION ascendany.enforce_analytics_head_advance()
RETURNS trigger
LANGUAGE plpgsql
AS $function$
DECLARE
    generation_status text;
    generation_base_id bigint;
    generation_base_revision bigint;
BEGIN
    IF NEW.head_revision <> OLD.head_revision + 1 OR NEW.current_generation_id IS NULL THEN
        RAISE EXCEPTION 'analytics head must advance by exactly one published generation'
            USING ERRCODE = '40001';
    END IF;
    SELECT status, base_analytics_generation_id, base_head_revision
    INTO generation_status, generation_base_id, generation_base_revision
    FROM ascendany.analytics_generations
    WHERE analytics_generation_id = NEW.current_generation_id;

    IF NOT FOUND
       OR generation_status <> 'succeeded'
       OR generation_base_id IS DISTINCT FROM OLD.current_generation_id
       OR generation_base_revision <> OLD.head_revision THEN
        RAISE EXCEPTION 'analytics head target does not continue the published head'
            USING ERRCODE = '40001';
    END IF;
    RETURN NEW;
END
$function$;

REVOKE ALL ON FUNCTION ascendany.enforce_analytics_head_advance() FROM PUBLIC;


CREATE TABLE ascendany.configuration_items (
    configuration_item_id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    public_id uuid NOT NULL UNIQUE,
    configuration_key text NOT NULL UNIQUE,
    configuration_kind text NOT NULL,
    active_version_id bigint,
    head_revision bigint NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT configuration_items_public_id_nonzero CHECK (
        public_id <> '00000000-0000-0000-0000-000000000000'::uuid
    ),
    CONSTRAINT configuration_items_key_format CHECK (
        configuration_key ~ '^[a-z][a-z0-9_.-]{0,127}$'
    ),
    CONSTRAINT configuration_items_kind_valid CHECK (
        configuration_kind IN (
            'prompt',
            'model_connection',
            'knowledge_catalog',
            'feedback_policy',
            'feedback_delivery'
        )
    ),
    CONSTRAINT configuration_items_head_revision_nonnegative CHECK (head_revision >= 0),
    CONSTRAINT configuration_items_head_consistent CHECK (
        (active_version_id IS NULL AND head_revision = 0)
        OR (active_version_id IS NOT NULL AND head_revision > 0)
    ),
    CONSTRAINT configuration_items_time_order CHECK (updated_at >= created_at),
    CONSTRAINT configuration_items_id_kind_unique UNIQUE (
        configuration_item_id,
        configuration_kind
    )
);

CREATE INDEX configuration_items_kind_key_idx
ON ascendany.configuration_items (configuration_kind, configuration_key);

CREATE TABLE ascendany.configuration_versions (
    configuration_version_id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    configuration_item_id bigint NOT NULL,
    configuration_kind text NOT NULL,
    version_number bigint NOT NULL,
    schema_id text NOT NULL,
    document jsonb NOT NULL,
    document_sha256 text NOT NULL,
    credential_ref text,
    created_by_account_id bigint NOT NULL,
    created_by_role text NOT NULL DEFAULT 'admin',
    created_by_session_id bigint,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT configuration_versions_item_kind_fk FOREIGN KEY (
        configuration_item_id,
        configuration_kind
    ) REFERENCES ascendany.configuration_items (
        configuration_item_id,
        configuration_kind
    ) ON DELETE RESTRICT,
    CONSTRAINT configuration_versions_creator_admin CHECK (created_by_role = 'admin'),
    CONSTRAINT configuration_versions_creator_fk FOREIGN KEY (
        created_by_account_id,
        created_by_role
    ) REFERENCES ascendany.auth_accounts (
        account_id,
        role
    ) ON DELETE RESTRICT,
    CONSTRAINT configuration_versions_creator_session_fk FOREIGN KEY (
        created_by_session_id,
        created_by_account_id
    ) REFERENCES ascendany.auth_sessions (
        session_id,
        account_id
    ) ON DELETE RESTRICT,
    CONSTRAINT configuration_versions_number_positive CHECK (version_number > 0),
    CONSTRAINT configuration_versions_schema_format CHECK (
        schema_id ~ '^ascendany[.][a-z][a-z0-9_.-]{0,126}[.]v[1-9][0-9]*$'
    ),
    CONSTRAINT configuration_versions_document_object CHECK (jsonb_typeof(document) = 'object'),
    CONSTRAINT configuration_versions_document_hash CHECK (
        document_sha256 ~ '^[0-9a-f]{64}$'
    ),
    CONSTRAINT configuration_versions_credential_ref_format CHECK (
        credential_ref IS NULL
        OR credential_ref ~ '^[a-z][a-z0-9_.-]{0,127}$'
    ),
    CONSTRAINT configuration_versions_secret_boundary CHECK (
        configuration_kind IN ('model_connection', 'feedback_delivery')
        OR credential_ref IS NULL
    ),
    CONSTRAINT configuration_versions_item_number_unique UNIQUE (
        configuration_item_id,
        version_number
    ),
    CONSTRAINT configuration_versions_item_hash_unique UNIQUE (
        configuration_item_id,
        document_sha256
    ),
    CONSTRAINT configuration_versions_item_id_unique UNIQUE (
        configuration_item_id,
        configuration_version_id
    ),
    CONSTRAINT configuration_versions_id_kind_unique UNIQUE (
        configuration_version_id,
        configuration_kind
    )
);

CREATE INDEX configuration_versions_item_number_idx
ON ascendany.configuration_versions (configuration_item_id, version_number DESC);

ALTER TABLE ascendany.configuration_items
ADD CONSTRAINT configuration_items_active_version_fk FOREIGN KEY (
    configuration_item_id,
    active_version_id
) REFERENCES ascendany.configuration_versions (
    configuration_item_id,
    configuration_version_id
) ON DELETE RESTRICT
DEFERRABLE INITIALLY IMMEDIATE;

CREATE TABLE ascendany.analytics_generation_events (
    analytics_generation_id bigint NOT NULL,
    event_sequence bigint NOT NULL,
    event_type text NOT NULL,
    payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (analytics_generation_id, event_sequence),
    CONSTRAINT analytics_generation_events_generation_fk FOREIGN KEY (
        analytics_generation_id
    ) REFERENCES ascendany.analytics_generations (
        analytics_generation_id
    ) ON DELETE RESTRICT,
    CONSTRAINT analytics_generation_events_sequence_positive CHECK (event_sequence > 0),
    CONSTRAINT analytics_generation_events_type_format CHECK (
        event_type ~ '^[a-z][a-z0-9_.-]{0,63}$'
    ),
    CONSTRAINT analytics_generation_events_payload_object CHECK (
        jsonb_typeof(payload) = 'object'
    )
);

CREATE INDEX analytics_generation_events_created_idx
ON ascendany.analytics_generation_events (
    analytics_generation_id,
    created_at,
    event_sequence
);

CREATE TABLE ascendany.chat_threads (
    chat_thread_id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    public_id uuid NOT NULL UNIQUE,
    owner_account_id bigint NOT NULL,
    owner_role text NOT NULL DEFAULT 'student',
    head_revision bigint NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT chat_threads_public_id_nonzero CHECK (
        public_id <> '00000000-0000-0000-0000-000000000000'::uuid
    ),
    CONSTRAINT chat_threads_owner_student CHECK (owner_role = 'student'),
    CONSTRAINT chat_threads_owner_fk FOREIGN KEY (
        owner_account_id,
        owner_role
    ) REFERENCES ascendany.auth_accounts (
        account_id,
        role
    ) ON DELETE RESTRICT,
    CONSTRAINT chat_threads_head_nonnegative CHECK (head_revision >= 0),
    CONSTRAINT chat_threads_time_order CHECK (updated_at >= created_at),
    CONSTRAINT chat_threads_id_owner_unique UNIQUE (
        chat_thread_id,
        owner_account_id
    )
);

CREATE INDEX chat_threads_owner_updated_idx
ON ascendany.chat_threads (
    owner_account_id,
    updated_at DESC,
    chat_thread_id DESC
);

CREATE TABLE ascendany.chat_messages (
    chat_message_id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    public_id uuid NOT NULL UNIQUE,
    chat_thread_id bigint NOT NULL,
    owner_account_id bigint NOT NULL,
    message_sequence bigint NOT NULL,
    message_kind text NOT NULL,
    content text NOT NULL,
    reasoning_content text,
    context_summary text,
    author_session_id bigint,
    agent_run_id bigint,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT chat_messages_public_id_nonzero CHECK (
        public_id <> '00000000-0000-0000-0000-000000000000'::uuid
    ),
    CONSTRAINT chat_messages_thread_owner_fk FOREIGN KEY (
        chat_thread_id,
        owner_account_id
    ) REFERENCES ascendany.chat_threads (
        chat_thread_id,
        owner_account_id
    ) ON DELETE RESTRICT,
    CONSTRAINT chat_messages_author_session_fk FOREIGN KEY (
        author_session_id,
        owner_account_id
    ) REFERENCES ascendany.auth_sessions (
        session_id,
        account_id
    ) ON DELETE RESTRICT,
    CONSTRAINT chat_messages_sequence_positive CHECK (message_sequence > 0),
    CONSTRAINT chat_messages_kind_valid CHECK (
        message_kind IN ('user', 'auto_analysis_request', 'assistant')
    ),
    CONSTRAINT chat_messages_actor_consistent CHECK (
        (
            message_kind IN ('user', 'auto_analysis_request')
            AND author_session_id IS NOT NULL
            AND agent_run_id IS NULL
            AND reasoning_content IS NULL
            AND context_summary IS NULL
        )
        OR (
            message_kind = 'assistant'
            AND author_session_id IS NULL
            AND agent_run_id IS NOT NULL
        )
    ),
    CONSTRAINT chat_messages_content_size CHECK (
        octet_length(content) BETWEEN 1 AND 131072
    ),
    CONSTRAINT chat_messages_reasoning_size CHECK (
        reasoning_content IS NULL OR octet_length(reasoning_content) <= 262144
    ),
    CONSTRAINT chat_messages_summary_size CHECK (
        context_summary IS NULL OR octet_length(context_summary) <= 65536
    ),
    CONSTRAINT chat_messages_thread_sequence_unique UNIQUE (
        chat_thread_id,
        message_sequence
    ),
    CONSTRAINT chat_messages_id_thread_kind_unique UNIQUE (
        chat_message_id,
        chat_thread_id,
        message_kind
    ),
    CONSTRAINT chat_messages_id_run_unique UNIQUE (
        chat_message_id,
        agent_run_id
    ),
    CONSTRAINT chat_messages_run_unique UNIQUE (agent_run_id)
);

CREATE INDEX chat_messages_thread_sequence_idx
ON ascendany.chat_messages (chat_thread_id, message_sequence);

CREATE TABLE ascendany.agent_runs (
    agent_run_id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    public_id uuid NOT NULL UNIQUE,
    chat_thread_id bigint NOT NULL,
    owner_account_id bigint NOT NULL,
    request_session_id bigint NOT NULL,
    client_request_id uuid NOT NULL,
    run_kind text NOT NULL,
    input_message_id bigint NOT NULL,
    input_message_kind text NOT NULL,
    output_message_id bigint,
    prompt_configuration_version_id bigint NOT NULL,
    prompt_configuration_kind text GENERATED ALWAYS AS ('prompt'::text) STORED,
    model_configuration_version_id bigint NOT NULL,
    model_configuration_kind text GENERATED ALWAYS AS ('model_connection'::text) STORED,
    note_revision_id bigint,
    analytics_generation_id bigint,
    status text NOT NULL,
    attempt_count integer NOT NULL DEFAULT 0,
    attempt_token uuid,
    lease_owner text,
    lease_expires_at timestamptz,
    next_attempt_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    error_code text,
    error_detail text,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    started_at timestamptz,
    finished_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT agent_runs_public_id_nonzero CHECK (
        public_id <> '00000000-0000-0000-0000-000000000000'::uuid
    ),
    CONSTRAINT agent_runs_thread_owner_fk FOREIGN KEY (
        chat_thread_id,
        owner_account_id
    ) REFERENCES ascendany.chat_threads (
        chat_thread_id,
        owner_account_id
    ) ON DELETE RESTRICT,
    CONSTRAINT agent_runs_request_session_fk FOREIGN KEY (
        request_session_id,
        owner_account_id
    ) REFERENCES ascendany.auth_sessions (
        session_id,
        account_id
    ) ON DELETE RESTRICT,
    CONSTRAINT agent_runs_input_message_fk FOREIGN KEY (
        input_message_id,
        chat_thread_id,
        input_message_kind
    ) REFERENCES ascendany.chat_messages (
        chat_message_id,
        chat_thread_id,
        message_kind
    ) ON DELETE RESTRICT,
    CONSTRAINT agent_runs_prompt_configuration_fk FOREIGN KEY (
        prompt_configuration_version_id,
        prompt_configuration_kind
    ) REFERENCES ascendany.configuration_versions (
        configuration_version_id,
        configuration_kind
    ) ON DELETE RESTRICT,
    CONSTRAINT agent_runs_model_configuration_fk FOREIGN KEY (
        model_configuration_version_id,
        model_configuration_kind
    ) REFERENCES ascendany.configuration_versions (
        configuration_version_id,
        configuration_kind
    ) ON DELETE RESTRICT,
    CONSTRAINT agent_runs_analytics_generation_fk FOREIGN KEY (
        analytics_generation_id
    ) REFERENCES ascendany.analytics_generations (
        analytics_generation_id
    ) ON DELETE RESTRICT,
    CONSTRAINT agent_runs_client_request_nonzero CHECK (
        client_request_id <> '00000000-0000-0000-0000-000000000000'::uuid
    ),
    CONSTRAINT agent_runs_kind_input_consistent CHECK (
        (run_kind = 'reply' AND input_message_kind = 'user')
        OR (
            run_kind = 'auto_analysis'
            AND input_message_kind = 'auto_analysis_request'
            AND analytics_generation_id IS NOT NULL
        )
    ),
    CONSTRAINT agent_runs_status_valid CHECK (
        status IN ('queued', 'running', 'succeeded', 'failed', 'superseded')
    ),
    CONSTRAINT agent_runs_attempt_nonnegative CHECK (attempt_count >= 0),
    CONSTRAINT agent_runs_execution_state_consistent CHECK (
        (
            status = 'queued'
            AND attempt_token IS NULL
            AND lease_owner IS NULL
            AND lease_expires_at IS NULL
            AND finished_at IS NULL
        )
        OR (
            status = 'running'
            AND attempt_count > 0
            AND attempt_token IS NOT NULL
            AND attempt_token <> '00000000-0000-0000-0000-000000000000'::uuid
            AND lease_owner IS NOT NULL
            AND btrim(lease_owner) <> ''
            AND lease_expires_at IS NOT NULL
            AND started_at IS NOT NULL
            AND finished_at IS NULL
        )
        OR (
            status IN ('succeeded', 'failed', 'superseded')
            AND attempt_count > 0
            AND attempt_token IS NULL
            AND lease_owner IS NULL
            AND lease_expires_at IS NULL
            AND started_at IS NOT NULL
            AND finished_at IS NOT NULL
        )
    ),
    CONSTRAINT agent_runs_output_consistent CHECK (
        (status = 'succeeded' AND output_message_id IS NOT NULL)
        OR (status <> 'succeeded' AND output_message_id IS NULL)
    ),
    CONSTRAINT agent_runs_error_consistent CHECK (
        (
            status = 'failed'
            AND error_code IS NOT NULL
            AND btrim(error_code) <> ''
            AND error_detail IS NOT NULL
            AND btrim(error_detail) <> ''
        )
        OR (
            status <> 'failed'
            AND error_code IS NULL
            AND error_detail IS NULL
        )
    ),
    CONSTRAINT agent_runs_time_order CHECK (updated_at >= created_at),
    CONSTRAINT agent_runs_owner_request_unique UNIQUE (
        owner_account_id,
        client_request_id
    ),
    CONSTRAINT agent_runs_input_message_unique UNIQUE (input_message_id),
    CONSTRAINT agent_runs_id_owner_unique UNIQUE (
        agent_run_id,
        owner_account_id
    )
);

CREATE INDEX agent_runs_queued_claim_idx
ON ascendany.agent_runs (next_attempt_at, agent_run_id)
WHERE status = 'queued';

CREATE INDEX agent_runs_expired_lease_idx
ON ascendany.agent_runs (lease_expires_at, agent_run_id)
WHERE status = 'running';

CREATE INDEX agent_runs_thread_created_idx
ON ascendany.agent_runs (chat_thread_id, created_at DESC, agent_run_id DESC);

ALTER TABLE ascendany.chat_messages
ADD CONSTRAINT chat_messages_agent_run_fk FOREIGN KEY (
    agent_run_id
) REFERENCES ascendany.agent_runs (
    agent_run_id
) ON DELETE RESTRICT
DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE ascendany.agent_runs
ADD CONSTRAINT agent_runs_output_message_fk FOREIGN KEY (
    output_message_id,
    agent_run_id
) REFERENCES ascendany.chat_messages (
    chat_message_id,
    agent_run_id
) ON DELETE RESTRICT
DEFERRABLE INITIALLY IMMEDIATE;

CREATE TABLE ascendany.agent_run_events (
    agent_run_id bigint NOT NULL,
    event_sequence bigint NOT NULL,
    event_type text NOT NULL,
    payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (agent_run_id, event_sequence),
    CONSTRAINT agent_run_events_run_fk FOREIGN KEY (agent_run_id)
        REFERENCES ascendany.agent_runs (agent_run_id) ON DELETE RESTRICT,
    CONSTRAINT agent_run_events_sequence_positive CHECK (event_sequence > 0),
    CONSTRAINT agent_run_events_type_format CHECK (
        event_type ~ '^[a-z][a-z0-9_.-]{0,63}$'
    ),
    CONSTRAINT agent_run_events_payload_object CHECK (jsonb_typeof(payload) = 'object')
);

CREATE INDEX agent_run_events_created_idx
ON ascendany.agent_run_events (agent_run_id, created_at, event_sequence);

CREATE TABLE ascendany.agent_tool_calls (
    agent_tool_call_id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    agent_run_id bigint NOT NULL,
    tool_sequence bigint NOT NULL,
    tool_call_key text NOT NULL,
    tool_name text NOT NULL,
    arguments_schema text NOT NULL,
    arguments jsonb NOT NULL,
    arguments_sha256 text NOT NULL,
    result_schema text,
    result jsonb,
    result_sha256 text,
    outcome text NOT NULL,
    error_code text,
    started_at timestamptz NOT NULL,
    finished_at timestamptz NOT NULL,
    CONSTRAINT agent_tool_calls_run_fk FOREIGN KEY (agent_run_id)
        REFERENCES ascendany.agent_runs (agent_run_id) ON DELETE RESTRICT,
    CONSTRAINT agent_tool_calls_sequence_positive CHECK (tool_sequence > 0),
    CONSTRAINT agent_tool_calls_key_format CHECK (
        tool_call_key ~ '^[A-Za-z0-9_.:-]{1,128}$'
    ),
    CONSTRAINT agent_tool_calls_name_format CHECK (
        tool_name ~ '^[a-z][a-z0-9_.-]{0,63}$'
    ),
    CONSTRAINT agent_tool_calls_arguments_object CHECK (jsonb_typeof(arguments) = 'object'),
    CONSTRAINT agent_tool_calls_arguments_hash CHECK (
        arguments_sha256 ~ '^[0-9a-f]{64}$'
    ),
    CONSTRAINT agent_tool_calls_outcome_valid CHECK (
        outcome IN ('succeeded', 'failed', 'denied')
    ),
    CONSTRAINT agent_tool_calls_result_consistent CHECK (
        (
            outcome = 'succeeded'
            AND result_schema IS NOT NULL
            AND result IS NOT NULL
            AND jsonb_typeof(result) = 'object'
            AND result_sha256 ~ '^[0-9a-f]{64}$'
            AND error_code IS NULL
        )
        OR (
            outcome IN ('failed', 'denied')
            AND result_schema IS NULL
            AND result IS NULL
            AND result_sha256 IS NULL
            AND error_code IS NOT NULL
            AND btrim(error_code) <> ''
        )
    ),
    CONSTRAINT agent_tool_calls_time_order CHECK (finished_at >= started_at),
    CONSTRAINT agent_tool_calls_run_sequence_unique UNIQUE (
        agent_run_id,
        tool_sequence
    ),
    CONSTRAINT agent_tool_calls_run_key_unique UNIQUE (
        agent_run_id,
        tool_call_key
    ),
    CONSTRAINT agent_tool_calls_id_run_unique UNIQUE (
        agent_tool_call_id,
        agent_run_id
    )
);

CREATE TABLE ascendany.agent_notes (
    agent_note_id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    public_id uuid NOT NULL UNIQUE,
    owner_account_id bigint NOT NULL,
    owner_role text NOT NULL DEFAULT 'student',
    current_revision_id bigint,
    head_revision bigint NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT agent_notes_public_id_nonzero CHECK (
        public_id <> '00000000-0000-0000-0000-000000000000'::uuid
    ),
    CONSTRAINT agent_notes_owner_student CHECK (owner_role = 'student'),
    CONSTRAINT agent_notes_owner_fk FOREIGN KEY (
        owner_account_id,
        owner_role
    ) REFERENCES ascendany.auth_accounts (
        account_id,
        role
    ) ON DELETE RESTRICT,
    CONSTRAINT agent_notes_head_revision_nonnegative CHECK (head_revision >= 0),
    CONSTRAINT agent_notes_head_consistent CHECK (
        (current_revision_id IS NULL AND head_revision = 0)
        OR (current_revision_id IS NOT NULL AND head_revision > 0)
    ),
    CONSTRAINT agent_notes_time_order CHECK (updated_at >= created_at),
    CONSTRAINT agent_notes_id_owner_unique UNIQUE (
        agent_note_id,
        owner_account_id
    )
);

CREATE INDEX agent_notes_owner_updated_idx
ON ascendany.agent_notes (
    owner_account_id,
    updated_at DESC,
    agent_note_id DESC
);

CREATE TABLE ascendany.agent_note_revisions (
    agent_note_revision_id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    agent_note_id bigint NOT NULL,
    owner_account_id bigint NOT NULL,
    revision_number bigint NOT NULL,
    mutation_id uuid NOT NULL,
    source_kind text NOT NULL,
    operation text NOT NULL,
    note_state text NOT NULL,
    title text NOT NULL,
    content text NOT NULL,
    content_sha256 text NOT NULL,
    actor_session_id bigint NOT NULL,
    agent_run_id bigint,
    agent_tool_call_id bigint,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT agent_note_revisions_note_owner_fk FOREIGN KEY (
        agent_note_id,
        owner_account_id
    ) REFERENCES ascendany.agent_notes (
        agent_note_id,
        owner_account_id
    ) ON DELETE RESTRICT,
    CONSTRAINT agent_note_revisions_actor_session_fk FOREIGN KEY (
        actor_session_id,
        owner_account_id
    ) REFERENCES ascendany.auth_sessions (
        session_id,
        account_id
    ) ON DELETE RESTRICT,
    CONSTRAINT agent_note_revisions_run_owner_fk FOREIGN KEY (
        agent_run_id,
        owner_account_id
    ) REFERENCES ascendany.agent_runs (
        agent_run_id,
        owner_account_id
    ) ON DELETE RESTRICT,
    CONSTRAINT agent_note_revisions_tool_run_fk FOREIGN KEY (
        agent_tool_call_id,
        agent_run_id
    ) REFERENCES ascendany.agent_tool_calls (
        agent_tool_call_id,
        agent_run_id
    ) ON DELETE RESTRICT,
    CONSTRAINT agent_note_revisions_revision_positive CHECK (revision_number > 0),
    CONSTRAINT agent_note_revisions_mutation_nonzero CHECK (
        mutation_id <> '00000000-0000-0000-0000-000000000000'::uuid
    ),
    CONSTRAINT agent_note_revisions_source_valid CHECK (
        source_kind IN ('user', 'agent')
    ),
    CONSTRAINT agent_note_revisions_operation_valid CHECK (
        operation IN ('create', 'replace', 'patch', 'archive', 'restore')
    ),
    CONSTRAINT agent_note_revisions_state_valid CHECK (
        note_state IN ('active', 'archived')
    ),
    CONSTRAINT agent_note_revisions_source_binding CHECK (
        (
            source_kind = 'user'
            AND agent_run_id IS NULL
            AND agent_tool_call_id IS NULL
        )
        OR (
            source_kind = 'agent'
            AND agent_run_id IS NOT NULL
            AND agent_tool_call_id IS NOT NULL
        )
    ),
    CONSTRAINT agent_note_revisions_archive_state_consistent CHECK (
        (operation = 'archive' AND note_state = 'archived')
        OR (operation <> 'archive' AND note_state = 'active')
    ),
    CONSTRAINT agent_note_revisions_title_size CHECK (
        octet_length(title) BETWEEN 1 AND 512
    ),
    CONSTRAINT agent_note_revisions_content_size CHECK (
        octet_length(content) <= 131072
    ),
    CONSTRAINT agent_note_revisions_content_hash CHECK (
        content_sha256 ~ '^[0-9a-f]{64}$'
    ),
    CONSTRAINT agent_note_revisions_note_revision_unique UNIQUE (
        agent_note_id,
        revision_number
    ),
    CONSTRAINT agent_note_revisions_note_number_id_unique UNIQUE (
        agent_note_id,
        revision_number,
        agent_note_revision_id
    ),
    CONSTRAINT agent_note_revisions_id_owner_unique UNIQUE (
        agent_note_revision_id,
        owner_account_id
    ),
    CONSTRAINT agent_note_revisions_owner_mutation_unique UNIQUE (
        owner_account_id,
        mutation_id
    ),
    CONSTRAINT agent_note_revisions_tool_unique UNIQUE (agent_tool_call_id)
);

CREATE INDEX agent_note_revisions_note_revision_idx
ON ascendany.agent_note_revisions (agent_note_id, revision_number DESC);

ALTER TABLE ascendany.agent_notes
ADD CONSTRAINT agent_notes_current_revision_fk FOREIGN KEY (
    agent_note_id,
    head_revision,
    current_revision_id
) REFERENCES ascendany.agent_note_revisions (
    agent_note_id,
    revision_number,
    agent_note_revision_id
) ON DELETE RESTRICT
DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE ascendany.agent_runs
ADD CONSTRAINT agent_runs_note_revision_fk FOREIGN KEY (
    note_revision_id,
    owner_account_id
) REFERENCES ascendany.agent_note_revisions (
    agent_note_revision_id,
    owner_account_id
) ON DELETE RESTRICT
DEFERRABLE INITIALLY IMMEDIATE;

CREATE FUNCTION ascendany.validate_agent_run_output()
RETURNS trigger
LANGUAGE plpgsql
AS $function$
DECLARE
    target_run_id bigint;
    run_status text;
    output_message_id bigint;
    assistant_count bigint;
    matching_output_count bigint;
BEGIN
    IF TG_TABLE_NAME = 'chat_messages' THEN
        target_run_id := NEW.agent_run_id;
        IF target_run_id IS NULL THEN
            RETURN NULL;
        END IF;
    ELSE
        target_run_id := NEW.agent_run_id;
    END IF;

    SELECT
        run.status,
        run.output_message_id,
        count(message.chat_message_id),
        count(message.chat_message_id) FILTER (
            WHERE message.chat_message_id = run.output_message_id
              AND message.message_kind = 'assistant'
        )
    INTO run_status, output_message_id, assistant_count, matching_output_count
    FROM ascendany.agent_runs AS run
    LEFT JOIN ascendany.chat_messages AS message
      ON message.agent_run_id = run.agent_run_id
    WHERE run.agent_run_id = target_run_id
    GROUP BY run.status, run.output_message_id;

    IF NOT FOUND THEN
        RAISE EXCEPTION 'agent run % is missing', target_run_id
            USING ERRCODE = '23514';
    END IF;
    IF run_status = 'succeeded' THEN
        IF output_message_id IS NULL
           OR assistant_count <> 1
           OR matching_output_count <> 1 THEN
            RAISE EXCEPTION 'succeeded agent run % must own exactly one output message', target_run_id
                USING ERRCODE = '23514';
        END IF;
    ELSIF assistant_count <> 0 THEN
        RAISE EXCEPTION 'non-succeeded agent run % cannot own an output message', target_run_id
            USING ERRCODE = '23514';
    END IF;
    RETURN NULL;
END
$function$;

REVOKE ALL ON FUNCTION ascendany.validate_agent_run_output() FROM PUBLIC;

CREATE CONSTRAINT TRIGGER agent_runs_output_complete
AFTER INSERT OR UPDATE ON ascendany.agent_runs
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION ascendany.validate_agent_run_output();

CREATE CONSTRAINT TRIGGER chat_messages_output_complete
AFTER INSERT ON ascendany.chat_messages
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION ascendany.validate_agent_run_output();

CREATE TABLE ascendany.oj_problems (
    oj_problem_id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    public_id uuid NOT NULL UNIQUE,
    slug text NOT NULL UNIQUE,
    current_version_id bigint,
    head_revision bigint NOT NULL DEFAULT 0,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT oj_problems_public_id_nonzero CHECK (
        public_id <> '00000000-0000-0000-0000-000000000000'::uuid
    ),
    CONSTRAINT oj_problems_slug_format CHECK (
        slug ~ '^[a-z0-9][a-z0-9_-]{0,127}$'
    ),
    CONSTRAINT oj_problems_head_revision_nonnegative CHECK (head_revision >= 0),
    CONSTRAINT oj_problems_head_consistent CHECK (
        (current_version_id IS NULL AND head_revision = 0)
        OR (current_version_id IS NOT NULL AND head_revision > 0)
    ),
    CONSTRAINT oj_problems_time_order CHECK (updated_at >= created_at)
);

CREATE TABLE ascendany.oj_problem_versions (
    oj_problem_version_id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    oj_problem_id bigint NOT NULL,
    version_number bigint NOT NULL,
    lifecycle text NOT NULL,
    title text NOT NULL,
    statement_markdown text NOT NULL,
    solution_markdown text,
    knowledge_tags text[] NOT NULL DEFAULT ARRAY[]::text[],
    time_limit_ms integer NOT NULL,
    memory_limit_bytes bigint NOT NULL,
    output_limit_bytes bigint NOT NULL,
    problem_schema text NOT NULL,
    problem_spec jsonb NOT NULL,
    test_bundle_artifact_id bigint NOT NULL,
    content_sha256 text NOT NULL,
    created_by_account_id bigint NOT NULL,
    created_by_role text NOT NULL DEFAULT 'admin',
    created_by_session_id bigint,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT oj_problem_versions_problem_fk FOREIGN KEY (oj_problem_id)
        REFERENCES ascendany.oj_problems (oj_problem_id) ON DELETE RESTRICT,
    CONSTRAINT oj_problem_versions_test_artifact_fk FOREIGN KEY (
        test_bundle_artifact_id
    ) REFERENCES ascendany.artifacts (
        artifact_id
    ) ON DELETE RESTRICT,
    CONSTRAINT oj_problem_versions_creator_admin CHECK (created_by_role = 'admin'),
    CONSTRAINT oj_problem_versions_creator_fk FOREIGN KEY (
        created_by_account_id,
        created_by_role
    ) REFERENCES ascendany.auth_accounts (
        account_id,
        role
    ) ON DELETE RESTRICT,
    CONSTRAINT oj_problem_versions_creator_session_fk FOREIGN KEY (
        created_by_session_id,
        created_by_account_id
    ) REFERENCES ascendany.auth_sessions (
        session_id,
        account_id
    ) ON DELETE RESTRICT,
    CONSTRAINT oj_problem_versions_number_positive CHECK (version_number > 0),
    CONSTRAINT oj_problem_versions_lifecycle_valid CHECK (
        lifecycle IN ('active', 'archived')
    ),
    CONSTRAINT oj_problem_versions_title_size CHECK (
        title = btrim(title)
        AND octet_length(title) BETWEEN 1 AND 512
    ),
    CONSTRAINT oj_problem_versions_statement_size CHECK (
        octet_length(statement_markdown) BETWEEN 1 AND 1048576
    ),
    CONSTRAINT oj_problem_versions_solution_size CHECK (
        solution_markdown IS NULL OR octet_length(solution_markdown) <= 1048576
    ),
    CONSTRAINT oj_problem_versions_tags_valid CHECK (
        array_position(knowledge_tags, NULL) IS NULL
        AND cardinality(knowledge_tags) <= 64
    ),
    CONSTRAINT oj_problem_versions_limits_positive CHECK (
        time_limit_ms > 0
        AND memory_limit_bytes > 0
        AND output_limit_bytes > 0
    ),
    CONSTRAINT oj_problem_versions_schema_format CHECK (
        problem_schema ~ '^ascendany[.]oj[.]problem[.]v[1-9][0-9]*$'
    ),
    CONSTRAINT oj_problem_versions_spec_object CHECK (jsonb_typeof(problem_spec) = 'object'),
    CONSTRAINT oj_problem_versions_content_hash CHECK (
        content_sha256 ~ '^[0-9a-f]{64}$'
    ),
    CONSTRAINT oj_problem_versions_problem_number_unique UNIQUE (
        oj_problem_id,
        version_number
    ),
    CONSTRAINT oj_problem_versions_problem_number_id_unique UNIQUE (
        oj_problem_id,
        version_number,
        oj_problem_version_id
    ),
    CONSTRAINT oj_problem_versions_problem_id_unique UNIQUE (
        oj_problem_id,
        oj_problem_version_id
    ),
    CONSTRAINT oj_problem_versions_problem_hash_unique UNIQUE (
        oj_problem_id,
        content_sha256
    )
);

CREATE INDEX oj_problem_versions_problem_number_idx
ON ascendany.oj_problem_versions (oj_problem_id, version_number DESC);

CREATE INDEX oj_problem_versions_knowledge_tags_idx
ON ascendany.oj_problem_versions USING gin (knowledge_tags);

ALTER TABLE ascendany.oj_problems
ADD CONSTRAINT oj_problems_current_version_fk FOREIGN KEY (
    oj_problem_id,
    head_revision,
    current_version_id
) REFERENCES ascendany.oj_problem_versions (
    oj_problem_id,
    version_number,
    oj_problem_version_id
) ON DELETE RESTRICT
DEFERRABLE INITIALLY IMMEDIATE;

CREATE TABLE ascendany.oj_submissions (
    oj_submission_id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    public_id uuid NOT NULL UNIQUE,
    account_id bigint NOT NULL,
    session_id bigint NOT NULL,
    client_request_id uuid NOT NULL,
    oj_problem_id bigint NOT NULL,
    oj_problem_version_id bigint NOT NULL,
    submission_mode text NOT NULL,
    language_id text NOT NULL,
    source_artifact_id bigint NOT NULL,
    stdin_artifact_id bigint,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT oj_submissions_public_id_nonzero CHECK (
        public_id <> '00000000-0000-0000-0000-000000000000'::uuid
    ),
    CONSTRAINT oj_submissions_account_fk FOREIGN KEY (account_id)
        REFERENCES ascendany.auth_accounts (account_id) ON DELETE RESTRICT,
    CONSTRAINT oj_submissions_session_fk FOREIGN KEY (
        session_id,
        account_id
    ) REFERENCES ascendany.auth_sessions (
        session_id,
        account_id
    ) ON DELETE RESTRICT,
    CONSTRAINT oj_submissions_problem_version_fk FOREIGN KEY (
        oj_problem_id,
        oj_problem_version_id
    ) REFERENCES ascendany.oj_problem_versions (
        oj_problem_id,
        oj_problem_version_id
    ) ON DELETE RESTRICT,
    CONSTRAINT oj_submissions_source_artifact_fk FOREIGN KEY (source_artifact_id)
        REFERENCES ascendany.artifacts (artifact_id) ON DELETE RESTRICT,
    CONSTRAINT oj_submissions_stdin_artifact_fk FOREIGN KEY (stdin_artifact_id)
        REFERENCES ascendany.artifacts (artifact_id) ON DELETE RESTRICT,
    CONSTRAINT oj_submissions_client_request_nonzero CHECK (
        client_request_id <> '00000000-0000-0000-0000-000000000000'::uuid
    ),
    CONSTRAINT oj_submissions_mode_valid CHECK (
        submission_mode IN ('run', 'submit')
    ),
    CONSTRAINT oj_submissions_mode_input_consistent CHECK (
        (submission_mode = 'run' AND stdin_artifact_id IS NOT NULL)
        OR (submission_mode = 'submit' AND stdin_artifact_id IS NULL)
    ),
    CONSTRAINT oj_submissions_language_format CHECK (
        language_id ~ '^[a-z][a-z0-9_+.-]{0,31}$'
    ),
    CONSTRAINT oj_submissions_account_request_unique UNIQUE (
        account_id,
        client_request_id
    )
);

CREATE INDEX oj_submissions_account_created_idx
ON ascendany.oj_submissions (account_id, created_at DESC, oj_submission_id DESC);

CREATE INDEX oj_submissions_problem_created_idx
ON ascendany.oj_submissions (oj_problem_id, created_at DESC, oj_submission_id DESC);

CREATE TABLE ascendany.oj_judge_jobs (
    judge_job_id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    public_id uuid NOT NULL UNIQUE,
    oj_submission_id bigint NOT NULL UNIQUE,
    status text NOT NULL,
    attempt_count integer NOT NULL DEFAULT 0,
    attempt_token uuid,
    lease_owner text,
    lease_expires_at timestamptz,
    next_attempt_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    judge_result_id bigint,
    error_code text,
    error_detail text,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    started_at timestamptz,
    finished_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT oj_judge_jobs_public_id_nonzero CHECK (
        public_id <> '00000000-0000-0000-0000-000000000000'::uuid
    ),
    CONSTRAINT oj_judge_jobs_submission_fk FOREIGN KEY (oj_submission_id)
        REFERENCES ascendany.oj_submissions (oj_submission_id) ON DELETE RESTRICT,
    CONSTRAINT oj_judge_jobs_status_valid CHECK (
        status IN ('queued', 'running', 'completed', 'system_error')
    ),
    CONSTRAINT oj_judge_jobs_attempt_nonnegative CHECK (attempt_count >= 0),
    CONSTRAINT oj_judge_jobs_execution_state_consistent CHECK (
        (
            status = 'queued'
            AND attempt_token IS NULL
            AND lease_owner IS NULL
            AND lease_expires_at IS NULL
            AND finished_at IS NULL
        )
        OR (
            status = 'running'
            AND attempt_count > 0
            AND attempt_token IS NOT NULL
            AND attempt_token <> '00000000-0000-0000-0000-000000000000'::uuid
            AND lease_owner IS NOT NULL
            AND btrim(lease_owner) <> ''
            AND lease_expires_at IS NOT NULL
            AND started_at IS NOT NULL
            AND finished_at IS NULL
        )
        OR (
            status IN ('completed', 'system_error')
            AND attempt_count > 0
            AND attempt_token IS NULL
            AND lease_owner IS NULL
            AND lease_expires_at IS NULL
            AND started_at IS NOT NULL
            AND finished_at IS NOT NULL
        )
    ),
    CONSTRAINT oj_judge_jobs_result_consistent CHECK (
        (status = 'completed' AND judge_result_id IS NOT NULL)
        OR (status <> 'completed' AND judge_result_id IS NULL)
    ),
    CONSTRAINT oj_judge_jobs_error_consistent CHECK (
        (
            status = 'system_error'
            AND error_code IS NOT NULL
            AND btrim(error_code) <> ''
            AND error_detail IS NOT NULL
            AND btrim(error_detail) <> ''
        )
        OR (
            status <> 'system_error'
            AND error_code IS NULL
            AND error_detail IS NULL
        )
    ),
    CONSTRAINT oj_judge_jobs_time_order CHECK (updated_at >= created_at),
    CONSTRAINT oj_judge_jobs_id_status_unique UNIQUE (judge_job_id, status)
);

CREATE INDEX oj_judge_jobs_queued_claim_idx
ON ascendany.oj_judge_jobs (next_attempt_at, judge_job_id)
WHERE status = 'queued';

CREATE INDEX oj_judge_jobs_expired_lease_idx
ON ascendany.oj_judge_jobs (lease_expires_at, judge_job_id)
WHERE status = 'running';

CREATE TABLE ascendany.oj_judge_job_events (
    judge_job_id bigint NOT NULL,
    event_sequence bigint NOT NULL,
    event_type text NOT NULL,
    payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (judge_job_id, event_sequence),
    CONSTRAINT oj_judge_job_events_job_fk FOREIGN KEY (judge_job_id)
        REFERENCES ascendany.oj_judge_jobs (judge_job_id) ON DELETE RESTRICT,
    CONSTRAINT oj_judge_job_events_sequence_positive CHECK (event_sequence > 0),
    CONSTRAINT oj_judge_job_events_type_format CHECK (
        event_type ~ '^[a-z][a-z0-9_.-]{0,63}$'
    ),
    CONSTRAINT oj_judge_job_events_payload_object CHECK (jsonb_typeof(payload) = 'object')
);

CREATE INDEX oj_judge_job_events_created_idx
ON ascendany.oj_judge_job_events (judge_job_id, created_at, event_sequence);

CREATE TABLE ascendany.oj_judge_results (
    judge_result_id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    judge_job_id bigint NOT NULL UNIQUE,
    judge_job_terminal_status text GENERATED ALWAYS AS ('completed'::text) STORED,
    verdict text NOT NULL,
    score_fraction numeric NOT NULL,
    passed_case_count bigint NOT NULL,
    total_case_count bigint NOT NULL,
    max_time_ms bigint NOT NULL,
    max_memory_bytes bigint NOT NULL,
    output_artifact_id bigint,
    result_schema text NOT NULL,
    result_manifest jsonb NOT NULL,
    result_sha256 text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT oj_judge_results_job_fk FOREIGN KEY (
        judge_job_id,
        judge_job_terminal_status
    ) REFERENCES ascendany.oj_judge_jobs (
        judge_job_id,
        status
    ) ON DELETE RESTRICT
      DEFERRABLE INITIALLY DEFERRED,
    CONSTRAINT oj_judge_results_output_artifact_fk FOREIGN KEY (output_artifact_id)
        REFERENCES ascendany.artifacts (artifact_id) ON DELETE RESTRICT,
    CONSTRAINT oj_judge_results_verdict_valid CHECK (
        verdict IN (
            'accepted',
            'wrong_answer',
            'compile_error',
            'runtime_error',
            'time_limit_exceeded',
            'memory_limit_exceeded',
            'output_limit_exceeded'
        )
    ),
    CONSTRAINT oj_judge_results_score_range CHECK (
        score_fraction >= 0 AND score_fraction <= 1
    ),
    CONSTRAINT oj_judge_results_case_counts CHECK (
        passed_case_count >= 0
        AND total_case_count >= 0
        AND passed_case_count <= total_case_count
    ),
    CONSTRAINT oj_judge_results_resources_nonnegative CHECK (
        max_time_ms >= 0 AND max_memory_bytes >= 0
    ),
    CONSTRAINT oj_judge_results_schema_format CHECK (
        result_schema ~ '^ascendany[.]oj[.]judge-result[.]v[1-9][0-9]*$'
    ),
    CONSTRAINT oj_judge_results_manifest_object CHECK (
        jsonb_typeof(result_manifest) = 'object'
    ),
    CONSTRAINT oj_judge_results_hash CHECK (
        result_sha256 ~ '^[0-9a-f]{64}$'
    ),
    CONSTRAINT oj_judge_results_id_job_unique UNIQUE (
        judge_result_id,
        judge_job_id
    )
);

ALTER TABLE ascendany.oj_judge_jobs
ADD CONSTRAINT oj_judge_jobs_result_fk FOREIGN KEY (
    judge_result_id,
    judge_job_id
) REFERENCES ascendany.oj_judge_results (
    judge_result_id,
    judge_job_id
) ON DELETE RESTRICT
DEFERRABLE INITIALLY DEFERRED;

CREATE TABLE ascendany.feedback_submissions (
    feedback_id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    public_id uuid NOT NULL UNIQUE,
    submission_mode text NOT NULL,
    account_id bigint,
    session_id bigint,
    policy_configuration_version_id bigint,
    policy_configuration_kind text GENERATED ALWAYS AS ('feedback_policy'::text) STORED,
    rate_limit_subject_digest bytea NOT NULL,
    client_request_id uuid NOT NULL,
    title text NOT NULL,
    content text NOT NULL,
    platform text,
    app_version text,
    user_agent text,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT feedback_submissions_public_id_nonzero CHECK (
        public_id <> '00000000-0000-0000-0000-000000000000'::uuid
    ),
    CONSTRAINT feedback_submissions_account_fk FOREIGN KEY (account_id)
        REFERENCES ascendany.auth_accounts (account_id) ON DELETE RESTRICT,
    CONSTRAINT feedback_submissions_session_fk FOREIGN KEY (
        session_id,
        account_id
    ) REFERENCES ascendany.auth_sessions (
        session_id,
        account_id
    ) ON DELETE RESTRICT,
    CONSTRAINT feedback_submissions_policy_fk FOREIGN KEY (
        policy_configuration_version_id,
        policy_configuration_kind
    ) REFERENCES ascendany.configuration_versions (
        configuration_version_id,
        configuration_kind
    ) ON DELETE RESTRICT,
    CONSTRAINT feedback_submissions_mode_valid CHECK (
        submission_mode IN ('authenticated', 'public_policy')
    ),
    CONSTRAINT feedback_submissions_mode_binding CHECK (
        (
            submission_mode = 'authenticated'
            AND account_id IS NOT NULL
            AND session_id IS NOT NULL
            AND policy_configuration_version_id IS NULL
        )
        OR (
            submission_mode = 'public_policy'
            AND account_id IS NULL
            AND session_id IS NULL
            AND policy_configuration_version_id IS NOT NULL
        )
    ),
    CONSTRAINT feedback_submissions_subject_digest_sha256 CHECK (
        octet_length(rate_limit_subject_digest) = 32
    ),
    CONSTRAINT feedback_submissions_client_request_nonzero CHECK (
        client_request_id <> '00000000-0000-0000-0000-000000000000'::uuid
    ),
    CONSTRAINT feedback_submissions_title_size CHECK (
        title = btrim(title)
        AND octet_length(title) BETWEEN 1 AND 800
    ),
    CONSTRAINT feedback_submissions_content_size CHECK (
        content = btrim(content)
        AND octet_length(content) BETWEEN 1 AND 40000
    ),
    CONSTRAINT feedback_submissions_platform_size CHECK (
        platform IS NULL OR octet_length(platform) <= 320
    ),
    CONSTRAINT feedback_submissions_app_version_size CHECK (
        app_version IS NULL OR octet_length(app_version) <= 320
    ),
    CONSTRAINT feedback_submissions_user_agent_size CHECK (
        user_agent IS NULL OR octet_length(user_agent) <= 2048
    ),
    CONSTRAINT feedback_submissions_subject_request_unique UNIQUE (
        rate_limit_subject_digest,
        client_request_id
    )
);

CREATE INDEX feedback_submissions_subject_created_idx
ON ascendany.feedback_submissions (
    rate_limit_subject_digest,
    created_at DESC,
    feedback_id DESC
);

CREATE INDEX feedback_submissions_account_created_idx
ON ascendany.feedback_submissions (
    account_id,
    created_at DESC,
    feedback_id DESC
)
WHERE account_id IS NOT NULL;

CREATE TABLE ascendany.feedback_attachments (
    feedback_id bigint NOT NULL,
    attachment_sequence smallint NOT NULL,
    artifact_id bigint NOT NULL,
    filename text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (feedback_id, attachment_sequence),
    CONSTRAINT feedback_attachments_feedback_fk FOREIGN KEY (feedback_id)
        REFERENCES ascendany.feedback_submissions (feedback_id) ON DELETE RESTRICT,
    CONSTRAINT feedback_attachments_artifact_fk FOREIGN KEY (artifact_id)
        REFERENCES ascendany.artifacts (artifact_id) ON DELETE RESTRICT,
    CONSTRAINT feedback_attachments_sequence_range CHECK (
        attachment_sequence BETWEEN 1 AND 8
    ),
    CONSTRAINT feedback_attachments_filename_size CHECK (
        filename = btrim(filename)
        AND octet_length(filename) BETWEEN 1 AND 640
    ),
    CONSTRAINT feedback_attachments_feedback_artifact_unique UNIQUE (
        feedback_id,
        artifact_id
    )
);

CREATE TABLE ascendany.feedback_delivery_jobs (
    feedback_delivery_job_id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    public_id uuid NOT NULL UNIQUE,
    feedback_id bigint NOT NULL UNIQUE,
    delivery_configuration_version_id bigint NOT NULL,
    delivery_configuration_kind text GENERATED ALWAYS AS ('feedback_delivery'::text) STORED,
    status text NOT NULL,
    attempt_count integer NOT NULL DEFAULT 0,
    attempt_token uuid,
    lease_owner text,
    lease_expires_at timestamptz,
    next_attempt_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    provider_receipt_sha256 text,
    error_code text,
    error_detail text,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    started_at timestamptz,
    finished_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT feedback_delivery_jobs_public_id_nonzero CHECK (
        public_id <> '00000000-0000-0000-0000-000000000000'::uuid
    ),
    CONSTRAINT feedback_delivery_jobs_feedback_fk FOREIGN KEY (feedback_id)
        REFERENCES ascendany.feedback_submissions (feedback_id) ON DELETE RESTRICT,
    CONSTRAINT feedback_delivery_jobs_configuration_fk FOREIGN KEY (
        delivery_configuration_version_id,
        delivery_configuration_kind
    ) REFERENCES ascendany.configuration_versions (
        configuration_version_id,
        configuration_kind
    ) ON DELETE RESTRICT,
    CONSTRAINT feedback_delivery_jobs_status_valid CHECK (
        status IN ('queued', 'running', 'succeeded', 'failed')
    ),
    CONSTRAINT feedback_delivery_jobs_attempt_nonnegative CHECK (attempt_count >= 0),
    CONSTRAINT feedback_delivery_jobs_execution_state_consistent CHECK (
        (
            status = 'queued'
            AND attempt_token IS NULL
            AND lease_owner IS NULL
            AND lease_expires_at IS NULL
            AND finished_at IS NULL
        )
        OR (
            status = 'running'
            AND attempt_count > 0
            AND attempt_token IS NOT NULL
            AND attempt_token <> '00000000-0000-0000-0000-000000000000'::uuid
            AND lease_owner IS NOT NULL
            AND btrim(lease_owner) <> ''
            AND lease_expires_at IS NOT NULL
            AND started_at IS NOT NULL
            AND finished_at IS NULL
        )
        OR (
            status IN ('succeeded', 'failed')
            AND attempt_count > 0
            AND attempt_token IS NULL
            AND lease_owner IS NULL
            AND lease_expires_at IS NULL
            AND started_at IS NOT NULL
            AND finished_at IS NOT NULL
        )
    ),
    CONSTRAINT feedback_delivery_jobs_receipt_consistent CHECK (
        (
            status = 'succeeded'
            AND provider_receipt_sha256 ~ '^[0-9a-f]{64}$'
        )
        OR (
            status <> 'succeeded'
            AND provider_receipt_sha256 IS NULL
        )
    ),
    CONSTRAINT feedback_delivery_jobs_error_consistent CHECK (
        (
            status = 'failed'
            AND error_code IS NOT NULL
            AND btrim(error_code) <> ''
            AND error_detail IS NOT NULL
            AND btrim(error_detail) <> ''
        )
        OR (
            status <> 'failed'
            AND error_code IS NULL
            AND error_detail IS NULL
        )
    ),
    CONSTRAINT feedback_delivery_jobs_time_order CHECK (updated_at >= created_at)
);

CREATE INDEX feedback_delivery_jobs_queued_claim_idx
ON ascendany.feedback_delivery_jobs (
    next_attempt_at,
    feedback_delivery_job_id
)
WHERE status = 'queued';

CREATE INDEX feedback_delivery_jobs_expired_lease_idx
ON ascendany.feedback_delivery_jobs (
    lease_expires_at,
    feedback_delivery_job_id
)
WHERE status = 'running';

CREATE TABLE ascendany.feedback_delivery_events (
    feedback_delivery_job_id bigint NOT NULL,
    event_sequence bigint NOT NULL,
    event_type text NOT NULL,
    payload jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (feedback_delivery_job_id, event_sequence),
    CONSTRAINT feedback_delivery_events_job_fk FOREIGN KEY (
        feedback_delivery_job_id
    ) REFERENCES ascendany.feedback_delivery_jobs (
        feedback_delivery_job_id
    ) ON DELETE RESTRICT,
    CONSTRAINT feedback_delivery_events_sequence_positive CHECK (event_sequence > 0),
    CONSTRAINT feedback_delivery_events_type_format CHECK (
        event_type ~ '^[a-z][a-z0-9_.-]{0,63}$'
    ),
    CONSTRAINT feedback_delivery_events_payload_object CHECK (
        jsonb_typeof(payload) = 'object'
    )
);

CREATE INDEX feedback_delivery_events_created_idx
ON ascendany.feedback_delivery_events (
    feedback_delivery_job_id,
    created_at,
    event_sequence
);

CREATE INDEX pintia_student_number_c_order_idx
ON ascendany.pintia_actor_identifiers (identifier_value COLLATE "C")
WHERE identifier_kind = 'student_number';

ALTER TABLE ascendany.audit_events
ADD CONSTRAINT audit_events_payload_size CHECK (
    octet_length(payload::text) <= 65536
);

ALTER TABLE ascendany.exam_snapshots
ADD CONSTRAINT exam_snapshots_total_score_finite CHECK (
    total_score IS NULL OR total_score::text NOT IN ('NaN', 'Infinity', '-Infinity')
);

ALTER TABLE ascendany.pintia_snapshot_problems
ADD CONSTRAINT pintia_snapshot_problems_max_score_finite CHECK (
    max_score IS NULL OR max_score::text NOT IN ('NaN', 'Infinity', '-Infinity')
);

ALTER TABLE ascendany.pintia_rankings
ADD CONSTRAINT pintia_rankings_total_score_finite CHECK (
    total_score IS NULL OR total_score::text NOT IN ('NaN', 'Infinity', '-Infinity')
);

ALTER TABLE ascendany.pintia_ranking_problem_results
ADD CONSTRAINT pintia_ranking_problem_results_score_finite CHECK (
    score IS NULL OR score::text NOT IN ('NaN', 'Infinity', '-Infinity')
);

ALTER TABLE ascendany.pintia_snapshot_submissions
ADD CONSTRAINT pintia_snapshot_submissions_score_finite CHECK (
    score IS NULL OR score::text NOT IN ('NaN', 'Infinity', '-Infinity')
);

ALTER TABLE ascendany.pintia_submission_case_results
ADD CONSTRAINT pintia_submission_case_results_score_finite CHECK (
    score IS NULL OR score::text NOT IN ('NaN', 'Infinity', '-Infinity')
);

ALTER TABLE ascendany.student_analytics
ADD CONSTRAINT student_analytics_rating_finite_v2 CHECK (
    rating::text NOT IN ('NaN', 'Infinity', '-Infinity')
);

ALTER TABLE ascendany.oj_judge_results
ADD CONSTRAINT oj_judge_results_score_fraction_finite CHECK (
    score_fraction::text NOT IN ('NaN', 'Infinity', '-Infinity')
);

CREATE TRIGGER logical_exams_initial_head
BEFORE INSERT ON ascendany.logical_exams
FOR EACH ROW EXECUTE FUNCTION ascendany.enforce_initial_zero_head('active_snapshot_id');

CREATE TRIGGER configuration_items_initial_head
BEFORE INSERT ON ascendany.configuration_items
FOR EACH ROW EXECUTE FUNCTION ascendany.enforce_initial_zero_head('active_version_id');

CREATE TRIGGER chat_threads_initial_head
BEFORE INSERT ON ascendany.chat_threads
FOR EACH ROW EXECUTE FUNCTION ascendany.enforce_initial_zero_head();

CREATE TRIGGER agent_notes_initial_head
BEFORE INSERT ON ascendany.agent_notes
FOR EACH ROW EXECUTE FUNCTION ascendany.enforce_initial_zero_head('current_revision_id');

CREATE TRIGGER oj_problems_initial_head
BEFORE INSERT ON ascendany.oj_problems
FOR EACH ROW EXECUTE FUNCTION ascendany.enforce_initial_zero_head('current_version_id');

CREATE TRIGGER import_jobs_initial_queue
BEFORE INSERT ON ascendany.import_jobs
FOR EACH ROW EXECUTE FUNCTION ascendany.enforce_initial_queued_job('received');

CREATE TRIGGER analytics_generations_initial_queue
BEFORE INSERT ON ascendany.analytics_generations
FOR EACH ROW EXECUTE FUNCTION ascendany.enforce_initial_queued_job();

CREATE TRIGGER agent_runs_initial_queue
BEFORE INSERT ON ascendany.agent_runs
FOR EACH ROW EXECUTE FUNCTION ascendany.enforce_initial_queued_job();

CREATE TRIGGER oj_judge_jobs_initial_queue
BEFORE INSERT ON ascendany.oj_judge_jobs
FOR EACH ROW EXECUTE FUNCTION ascendany.enforce_initial_queued_job();

CREATE TRIGGER feedback_delivery_jobs_initial_queue
BEFORE INSERT ON ascendany.feedback_delivery_jobs
FOR EACH ROW EXECUTE FUNCTION ascendany.enforce_initial_queued_job();

CREATE TRIGGER logical_exams_head_revision_step
BEFORE UPDATE ON ascendany.logical_exams
FOR EACH ROW EXECUTE FUNCTION ascendany.require_single_head_revision_step();

CREATE TRIGGER analytics_head_monotonic_advance
BEFORE UPDATE ON ascendany.analytics_head
FOR EACH ROW EXECUTE FUNCTION ascendany.enforce_analytics_head_advance();

CREATE TRIGGER configuration_items_head_revision_step
BEFORE UPDATE ON ascendany.configuration_items
FOR EACH ROW EXECUTE FUNCTION ascendany.require_single_head_revision_step();

CREATE TRIGGER chat_threads_head_revision_step
BEFORE UPDATE ON ascendany.chat_threads
FOR EACH ROW EXECUTE FUNCTION ascendany.require_single_head_revision_step();

CREATE TRIGGER agent_notes_head_revision_step
BEFORE UPDATE ON ascendany.agent_notes
FOR EACH ROW EXECUTE FUNCTION ascendany.require_single_head_revision_step();

CREATE TRIGGER oj_problems_head_revision_step
BEFORE UPDATE ON ascendany.oj_problems
FOR EACH ROW EXECUTE FUNCTION ascendany.require_single_head_revision_step();

CREATE TRIGGER agent_runs_fenced_transition
BEFORE UPDATE ON ascendany.agent_runs
FOR EACH ROW EXECUTE FUNCTION ascendany.enforce_fenced_job_transition();

CREATE TRIGGER oj_judge_jobs_fenced_transition
BEFORE UPDATE ON ascendany.oj_judge_jobs
FOR EACH ROW EXECUTE FUNCTION ascendany.enforce_fenced_job_transition();

CREATE TRIGGER feedback_delivery_jobs_fenced_transition
BEFORE UPDATE ON ascendany.feedback_delivery_jobs
FOR EACH ROW EXECUTE FUNCTION ascendany.enforce_fenced_job_transition();

CREATE TRIGGER import_jobs_fenced_transition
BEFORE UPDATE ON ascendany.import_jobs
FOR EACH ROW EXECUTE FUNCTION ascendany.enforce_import_job_transition();

CREATE TRIGGER analytics_generations_fenced_transition
BEFORE UPDATE ON ascendany.analytics_generations
FOR EACH ROW EXECUTE FUNCTION ascendany.enforce_analytics_generation_transition();

CREATE TRIGGER agent_runs_terminal_immutable
BEFORE UPDATE ON ascendany.agent_runs
FOR EACH ROW EXECUTE FUNCTION ascendany.reject_terminal_job_mutation(
    'succeeded',
    'failed',
    'superseded'
);

CREATE TRIGGER oj_judge_jobs_terminal_immutable
BEFORE UPDATE ON ascendany.oj_judge_jobs
FOR EACH ROW EXECUTE FUNCTION ascendany.reject_terminal_job_mutation(
    'completed',
    'system_error'
);

CREATE TRIGGER feedback_delivery_jobs_terminal_immutable
BEFORE UPDATE ON ascendany.feedback_delivery_jobs
FOR EACH ROW EXECUTE FUNCTION ascendany.reject_terminal_job_mutation(
    'succeeded',
    'failed'
);

CREATE TRIGGER import_jobs_terminal_immutable
BEFORE UPDATE ON ascendany.import_jobs
FOR EACH ROW EXECUTE FUNCTION ascendany.reject_terminal_job_mutation(
    'succeeded',
    'failed',
    'superseded'
);

CREATE TRIGGER analytics_generations_terminal_immutable
BEFORE UPDATE ON ascendany.analytics_generations
FOR EACH ROW EXECUTE FUNCTION ascendany.reject_terminal_job_mutation(
    'succeeded',
    'failed',
    'superseded'
);

DO $immutable_triggers$
DECLARE
    table_name text;
    immutable_tables constant text[] := ARRAY[
        'configuration_versions',
        'analytics_generation_events',
        'chat_messages',
        'agent_run_events',
        'agent_tool_calls',
        'agent_note_revisions',
        'oj_problem_versions',
        'oj_submissions',
        'oj_judge_job_events',
        'oj_judge_results',
        'feedback_submissions',
        'feedback_attachments',
        'feedback_delivery_events'
    ];
BEGIN
    FOREACH table_name IN ARRAY immutable_tables LOOP
        EXECUTE format(
            'CREATE TRIGGER %I BEFORE UPDATE OR DELETE ON ascendany.%I FOR EACH ROW EXECUTE FUNCTION ascendany.reject_immutable_mutation()',
            table_name || '_immutable_rows',
            table_name
        );
        EXECUTE format(
            'CREATE TRIGGER %I BEFORE TRUNCATE ON ascendany.%I FOR EACH STATEMENT EXECUTE FUNCTION ascendany.reject_immutable_mutation()',
            table_name || '_immutable_truncate',
            table_name
        );
    END LOOP;
END
$immutable_triggers$;

REVOKE ALL PRIVILEGES ON TABLE
    ascendany.configuration_items,
    ascendany.configuration_versions,
    ascendany.analytics_generation_events,
    ascendany.chat_threads,
    ascendany.chat_messages,
    ascendany.agent_runs,
    ascendany.agent_run_events,
    ascendany.agent_tool_calls,
    ascendany.agent_notes,
    ascendany.agent_note_revisions,
    ascendany.oj_problems,
    ascendany.oj_problem_versions,
    ascendany.oj_submissions,
    ascendany.oj_judge_jobs,
    ascendany.oj_judge_job_events,
    ascendany.oj_judge_results,
    ascendany.feedback_submissions,
    ascendany.feedback_attachments,
    ascendany.feedback_delivery_jobs,
    ascendany.feedback_delivery_events
FROM ascendany_runtime, ascendany_backup, PUBLIC;

GRANT SELECT ON TABLE
    ascendany.configuration_items,
    ascendany.configuration_versions,
    ascendany.analytics_generation_events,
    ascendany.chat_threads,
    ascendany.chat_messages,
    ascendany.agent_runs,
    ascendany.agent_run_events,
    ascendany.agent_tool_calls,
    ascendany.agent_notes,
    ascendany.agent_note_revisions,
    ascendany.oj_problems,
    ascendany.oj_problem_versions,
    ascendany.oj_submissions,
    ascendany.oj_judge_jobs,
    ascendany.oj_judge_job_events,
    ascendany.oj_judge_results,
    ascendany.feedback_submissions,
    ascendany.feedback_attachments,
    ascendany.feedback_delivery_jobs,
    ascendany.feedback_delivery_events
TO ascendany_runtime, ascendany_backup;

GRANT INSERT ON TABLE
    ascendany.configuration_items,
    ascendany.configuration_versions,
    ascendany.analytics_generation_events,
    ascendany.chat_threads,
    ascendany.chat_messages,
    ascendany.agent_runs,
    ascendany.agent_run_events,
    ascendany.agent_tool_calls,
    ascendany.agent_notes,
    ascendany.agent_note_revisions,
    ascendany.oj_problems,
    ascendany.oj_problem_versions,
    ascendany.oj_submissions,
    ascendany.oj_judge_jobs,
    ascendany.oj_judge_job_events,
    ascendany.oj_judge_results,
    ascendany.feedback_submissions,
    ascendany.feedback_attachments,
    ascendany.feedback_delivery_jobs,
    ascendany.feedback_delivery_events
TO ascendany_runtime;

GRANT UPDATE (
    active_version_id,
    head_revision,
    updated_at
) ON TABLE ascendany.configuration_items TO ascendany_runtime;

GRANT UPDATE (
    head_revision,
    updated_at
) ON TABLE ascendany.chat_threads TO ascendany_runtime;

GRANT UPDATE (
    output_message_id,
    status,
    attempt_count,
    attempt_token,
    lease_owner,
    lease_expires_at,
    next_attempt_at,
    error_code,
    error_detail,
    started_at,
    finished_at,
    updated_at
) ON TABLE ascendany.agent_runs TO ascendany_runtime;

GRANT UPDATE (
    current_revision_id,
    head_revision,
    updated_at
) ON TABLE ascendany.agent_notes TO ascendany_runtime;

GRANT UPDATE (
    current_version_id,
    head_revision,
    updated_at
) ON TABLE ascendany.oj_problems TO ascendany_runtime;

GRANT UPDATE (
    status,
    attempt_count,
    attempt_token,
    lease_owner,
    lease_expires_at,
    next_attempt_at,
    judge_result_id,
    error_code,
    error_detail,
    started_at,
    finished_at,
    updated_at
) ON TABLE ascendany.oj_judge_jobs TO ascendany_runtime;

GRANT UPDATE (
    status,
    attempt_count,
    attempt_token,
    lease_owner,
    lease_expires_at,
    next_attempt_at,
    provider_receipt_sha256,
    error_code,
    error_detail,
    started_at,
    finished_at,
    updated_at
) ON TABLE ascendany.feedback_delivery_jobs TO ascendany_runtime;

REVOKE ALL PRIVILEGES ON ALL SEQUENCES IN SCHEMA ascendany
FROM ascendany_runtime, ascendany_backup, PUBLIC;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA ascendany TO ascendany_runtime;
GRANT SELECT ON ALL SEQUENCES IN SCHEMA ascendany TO ascendany_backup;
