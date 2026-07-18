SET LOCAL search_path = ascendany, pg_catalog;

DO $preflight$
DECLARE
    schema_owner text;
    prerequisite_count bigint;
BEGIN
    IF current_database() <> 'ascendany_v2' THEN
        RAISE EXCEPTION 'auth PTA nickname migration requires database ascendany_v2';
    END IF;
    IF current_user <> 'ascendany_owner' THEN
        RAISE EXCEPTION 'auth PTA nickname migration requires current role ascendany_owner';
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
    WHERE version = 8
      AND name = 'auto_analysis_frontend_context';
    IF prerequisite_count <> 1 THEN
        RAISE EXCEPTION 'auth PTA nickname migration requires schema version 8';
    END IF;
END
$preflight$;

ALTER TABLE ascendany.auth_accounts
ADD COLUMN pta_nickname text;

ALTER TABLE ascendany.auth_accounts
ADD CONSTRAINT auth_accounts_pta_nickname_consistent CHECK (
    (
        role = 'student'
        AND (
            pta_nickname IS NULL
            OR (
                pta_nickname = btrim(pta_nickname)
                AND octet_length(pta_nickname) BETWEEN 1 AND 256
            )
        )
    )
    OR (role = 'admin' AND pta_nickname IS NULL)
);
