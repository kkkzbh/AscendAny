DO $preflight$
BEGIN
    IF current_database() <> 'ascendany_v2' THEN
        RAISE EXCEPTION 'auto analysis migration requires database ascendany_v2';
    END IF;
    IF current_user <> 'ascendany_owner' THEN
        RAISE EXCEPTION 'auto analysis migration requires current role ascendany_owner';
    END IF;
    IF NOT EXISTS (
        SELECT 1
        FROM ascendany.schema_migrations_v2
        WHERE version = 4
          AND name = 'achievement_rules'
    ) THEN
        RAISE EXCEPTION 'auto analysis migration requires schema version 4';
    END IF;
END
$preflight$;

ALTER TABLE ascendany.chat_threads
ADD COLUMN thread_kind text NOT NULL DEFAULT 'conversation';

ALTER TABLE ascendany.chat_threads
ALTER COLUMN thread_kind DROP DEFAULT;

ALTER TABLE ascendany.chat_threads
ADD CONSTRAINT chat_threads_kind_valid CHECK (
    thread_kind IN ('conversation', 'auto_analysis')
);

ALTER TABLE ascendany.chat_messages
ADD CONSTRAINT chat_messages_auto_analysis_content_fixed CHECK (
    message_kind <> 'auto_analysis_request'
    OR content = 'Analyze the student''s current published analytics snapshot and provide a concise, actionable progress review.'
);

CREATE UNIQUE INDEX chat_threads_owner_auto_analysis_unique
ON ascendany.chat_threads (owner_account_id)
WHERE thread_kind = 'auto_analysis';

CREATE UNIQUE INDEX agent_runs_owner_analytics_auto_analysis_unique
ON ascendany.agent_runs (owner_account_id, analytics_generation_id)
WHERE run_kind = 'auto_analysis';

CREATE FUNCTION ascendany.enforce_agent_run_thread_kind()
RETURNS trigger
LANGUAGE plpgsql
AS $function$
DECLARE
    stored_thread_kind text;
    expected_thread_kind text;
BEGIN
    SELECT thread_kind
    INTO STRICT stored_thread_kind
    FROM ascendany.chat_threads
    WHERE chat_thread_id = NEW.chat_thread_id
      AND owner_account_id = NEW.owner_account_id;

    expected_thread_kind := CASE NEW.run_kind
        WHEN 'reply' THEN 'conversation'
        WHEN 'auto_analysis' THEN 'auto_analysis'
        ELSE NULL
    END;

    IF expected_thread_kind IS NULL
       OR stored_thread_kind IS DISTINCT FROM expected_thread_kind THEN
        RAISE EXCEPTION 'agent run kind does not match chat thread kind'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END
$function$;

REVOKE ALL ON FUNCTION ascendany.enforce_agent_run_thread_kind() FROM PUBLIC;

CREATE FUNCTION ascendany.enforce_chat_message_thread_kind()
RETURNS trigger
LANGUAGE plpgsql
AS $function$
DECLARE
    stored_thread_kind text;
BEGIN
    SELECT thread_kind
    INTO STRICT stored_thread_kind
    FROM ascendany.chat_threads
    WHERE chat_thread_id = NEW.chat_thread_id
      AND owner_account_id = NEW.owner_account_id;

    IF NOT (
        (stored_thread_kind = 'conversation' AND NEW.message_kind IN ('user', 'assistant'))
        OR
        (stored_thread_kind = 'auto_analysis' AND NEW.message_kind IN ('auto_analysis_request', 'assistant'))
    ) THEN
        RAISE EXCEPTION 'chat message kind does not match chat thread kind'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END
$function$;

REVOKE ALL ON FUNCTION ascendany.enforce_chat_message_thread_kind() FROM PUBLIC;

CREATE TRIGGER chat_messages_thread_kind_consistent
BEFORE INSERT ON ascendany.chat_messages
FOR EACH ROW EXECUTE FUNCTION ascendany.enforce_chat_message_thread_kind();

CREATE TRIGGER agent_runs_thread_kind_consistent
BEFORE INSERT ON ascendany.agent_runs
FOR EACH ROW EXECUTE FUNCTION ascendany.enforce_agent_run_thread_kind();

CREATE TRIGGER chat_threads_kind_immutable
BEFORE UPDATE OF thread_kind ON ascendany.chat_threads
FOR EACH ROW EXECUTE FUNCTION ascendany.reject_immutable_mutation();
