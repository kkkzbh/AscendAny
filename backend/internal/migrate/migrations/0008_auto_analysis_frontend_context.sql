SET LOCAL search_path = ascendany, pg_catalog;

DO $preflight$
DECLARE
    schema_owner text;
    prerequisite_count bigint;
    existing_auto_analysis_requests bigint;
BEGIN
    IF current_database() <> 'ascendany_v2' THEN
        RAISE EXCEPTION 'automatic analysis frontend context migration requires database ascendany_v2';
    END IF;
    IF current_user <> 'ascendany_owner' THEN
        RAISE EXCEPTION 'automatic analysis frontend context migration requires current role ascendany_owner';
    END IF;
    SELECT pg_get_userbyid(nspowner)
    INTO schema_owner
    FROM pg_namespace
    WHERE nspname = 'ascendany';
    IF schema_owner IS DISTINCT FROM 'ascendany_owner' THEN
        RAISE EXCEPTION 'schema ascendany owner drift: %', schema_owner;
    END IF;
    SELECT count(*)
    INTO prerequisite_count
    FROM ascendany.schema_migrations_v2
    WHERE version = 7
      AND name = 'catalog_publication_provenance';
    IF prerequisite_count <> 1 THEN
        RAISE EXCEPTION 'automatic analysis frontend context migration requires schema version 7';
    END IF;
    SELECT count(*)
    INTO existing_auto_analysis_requests
    FROM ascendany.chat_messages
    WHERE message_kind = 'auto_analysis_request';
    IF existing_auto_analysis_requests <> 0 THEN
        RAISE EXCEPTION 'automatic analysis frontend context migration requires zero existing automatic analysis requests';
    END IF;
END
$preflight$;

ALTER TABLE ascendany.chat_messages
DROP CONSTRAINT chat_messages_auto_analysis_content_fixed;

ALTER TABLE ascendany.chat_messages
ADD CONSTRAINT chat_messages_auto_analysis_content_fixed CHECK ((
    CASE
        WHEN message_kind <> 'auto_analysis_request' THEN true
        WHEN NOT pg_input_is_valid(content, 'jsonb') THEN false
        WHEN jsonb_typeof(content::jsonb) IS DISTINCT FROM 'object' THEN false
        WHEN content::jsonb - ARRAY['context', 'instruction', 'schema']::text[] <> '{}'::jsonb THEN false
        WHEN jsonb_typeof(content::jsonb -> 'context') IS DISTINCT FROM 'object' THEN false
        WHEN (content::jsonb -> 'context') - ARRAY[
            'latestExamId',
            'notes',
            'notesLocked',
            'notesTitle',
            'ptaNickname',
            'roleId',
            'roleName',
            'roleSystemPrompt',
            'studentId'
        ]::text[] <> '{}'::jsonb THEN false
        ELSE
            jsonb_typeof(content::jsonb -> 'schema') = 'string'
            AND content::jsonb ->> 'schema' = 'ascendany.agent.auto-analysis.frontend-context.v1'
            AND jsonb_typeof(content::jsonb -> 'instruction') = 'string'
            AND content::jsonb ->> 'instruction' = 'Analyze the student''s current published analytics snapshot and provide a concise, actionable progress review.'
            AND jsonb_typeof(content::jsonb -> 'context' -> 'studentId') = 'string'
            AND octet_length(content::jsonb -> 'context' ->> 'studentId') <= 256
            AND jsonb_typeof(content::jsonb -> 'context' -> 'ptaNickname') = 'string'
            AND octet_length(content::jsonb -> 'context' ->> 'ptaNickname') <= 256
            AND jsonb_typeof(content::jsonb -> 'context' -> 'roleId') = 'string'
            AND octet_length(content::jsonb -> 'context' ->> 'roleId') <= 256
            AND jsonb_typeof(content::jsonb -> 'context' -> 'roleName') = 'string'
            AND octet_length(content::jsonb -> 'context' ->> 'roleName') <= 4096
            AND jsonb_typeof(content::jsonb -> 'context' -> 'roleSystemPrompt') = 'string'
            AND octet_length(content::jsonb -> 'context' ->> 'roleSystemPrompt') <= 131072
            AND jsonb_typeof(content::jsonb -> 'context' -> 'latestExamId') = 'string'
            AND (content::jsonb -> 'context' ->> 'latestExamId') ~ '^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$'
            AND jsonb_typeof(content::jsonb -> 'context' -> 'notes') = 'string'
            AND octet_length(content::jsonb -> 'context' ->> 'notes') <= 131072
            AND jsonb_typeof(content::jsonb -> 'context' -> 'notesTitle') = 'string'
            AND octet_length(content::jsonb -> 'context' ->> 'notesTitle') <= 4096
            AND jsonb_typeof(content::jsonb -> 'context' -> 'notesLocked') = 'boolean'
    END
) IS TRUE);

ALTER TABLE ascendany.agent_runs
ADD COLUMN auto_analysis_exam_id uuid,
ADD COLUMN auto_analysis_role_id text;

ALTER TABLE ascendany.agent_runs
ADD CONSTRAINT agent_runs_auto_analysis_exam_fk
FOREIGN KEY (auto_analysis_exam_id)
REFERENCES ascendany.logical_exams (public_id)
ON DELETE RESTRICT;

DROP INDEX ascendany.agent_runs_owner_analytics_auto_analysis_unique;

ALTER TABLE ascendany.agent_runs
DROP CONSTRAINT agent_runs_kind_input_consistent;

ALTER TABLE ascendany.agent_runs
ADD CONSTRAINT agent_runs_kind_input_consistent CHECK (
    (
        run_kind = 'reply'
        AND input_message_kind = 'user'
        AND auto_analysis_exam_id IS NULL
        AND auto_analysis_role_id IS NULL
    )
    OR (
        run_kind = 'auto_analysis'
        AND input_message_kind = 'auto_analysis_request'
        AND analytics_generation_id IS NOT NULL
        AND auto_analysis_exam_id IS NOT NULL
        AND auto_analysis_role_id IS NOT NULL
    )
);

ALTER TABLE ascendany.agent_runs
ADD CONSTRAINT agent_runs_auto_analysis_identity_consistent CHECK (
    (
        run_kind = 'reply'
        AND auto_analysis_exam_id IS NULL
        AND auto_analysis_role_id IS NULL
    )
    OR (
        run_kind = 'auto_analysis'
        AND auto_analysis_exam_id IS NOT NULL
        AND auto_analysis_role_id = btrim(auto_analysis_role_id)
        AND octet_length(auto_analysis_role_id) BETWEEN 1 AND 256
    )
);

CREATE UNIQUE INDEX agent_runs_owner_exam_role_auto_analysis_unique
ON ascendany.agent_runs (
    owner_account_id,
    auto_analysis_exam_id,
    auto_analysis_role_id
)
WHERE run_kind = 'auto_analysis';
