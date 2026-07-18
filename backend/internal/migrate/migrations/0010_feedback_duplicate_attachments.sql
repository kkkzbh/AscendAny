SET LOCAL search_path = ascendany, pg_catalog;

DO $preflight$
DECLARE
    schema_owner text;
    prerequisite_count bigint;
BEGIN
    IF current_database() <> 'ascendany_v2' THEN
        RAISE EXCEPTION 'feedback duplicate attachments migration requires database ascendany_v2';
    END IF;
    IF current_user <> 'ascendany_owner' THEN
        RAISE EXCEPTION 'feedback duplicate attachments migration requires current role ascendany_owner';
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
    WHERE version = 9
      AND name = 'auth_pta_nickname';
    IF prerequisite_count <> 1 THEN
        RAISE EXCEPTION 'feedback duplicate attachments migration requires schema version 9';
    END IF;
END
$preflight$;

ALTER TABLE ascendany.feedback_attachments
DROP CONSTRAINT feedback_attachments_feedback_artifact_unique;
