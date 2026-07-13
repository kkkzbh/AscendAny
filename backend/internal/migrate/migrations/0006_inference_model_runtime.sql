SET LOCAL search_path = ascendany, pg_catalog;

DO $preflight$
DECLARE
    schema_owner text;
BEGIN
    IF current_database() <> 'ascendany_v2' THEN
        RAISE EXCEPTION 'inference model runtime migration requires database ascendany_v2';
    END IF;
    IF current_user <> 'ascendany_owner' THEN
        RAISE EXCEPTION 'inference model runtime migration requires current role ascendany_owner';
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
        WHERE version = 5
          AND name = 'auto_analysis_once'
    ) THEN
        RAISE EXCEPTION 'inference model runtime migration requires schema version 5';
    END IF;
END
$preflight$;

CREATE TABLE ascendany.recommendation_model_releases (
    recommendation_model_release_id bigint GENERATED ALWAYS AS IDENTITY (
        SEQUENCE NAME ascendany.recommendation_model_release_ids_seq
    ) PRIMARY KEY,
    model_id uuid NOT NULL UNIQUE,
    artifact_sha256 text NOT NULL UNIQUE,
    artifact_size_bytes bigint NOT NULL,
    artifact_mode integer NOT NULL,
    model_schema text NOT NULL,
    model_purpose text NOT NULL,
    algorithm text NOT NULL,
    inference_contract text NOT NULL,
    trained_at timestamptz NOT NULL,
    training_provenance_sha256 text NOT NULL,
    feature_schema_sha256 text NOT NULL,
    knowledge_catalog_sha256 text NOT NULL,
    parameter_sha256 text NOT NULL,
    golden_vectors_sha256 text NOT NULL,
    manifest jsonb NOT NULL,
    manifest_sha256 text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT recommendation_model_releases_model_id_nonzero CHECK (
        model_id <> '00000000-0000-0000-0000-000000000000'::uuid
    ),
    CONSTRAINT recommendation_model_releases_artifact_hash CHECK (
        artifact_sha256 ~ '^[0-9a-f]{64}$'
    ),
    CONSTRAINT recommendation_model_releases_artifact_size CHECK (
        artifact_size_bytes BETWEEN 1 AND 16777216
    ),
    CONSTRAINT recommendation_model_releases_artifact_mode CHECK (
        artifact_mode = 420
    ),
    CONSTRAINT recommendation_model_releases_schema CHECK (
        model_schema = 'ascendany.recommendation.inference-model.v1'
    ),
    CONSTRAINT recommendation_model_releases_purpose CHECK (
        model_purpose IN ('production', 'acceptance_test')
    ),
    CONSTRAINT recommendation_model_releases_algorithm CHECK (
        algorithm = 'knowledge_mirt_feature_v1'
    ),
    CONSTRAINT recommendation_model_releases_contract CHECK (
        inference_contract = 'ascendany.recommendation.inference.v1'
    ),
    CONSTRAINT recommendation_model_releases_hashes CHECK (
        training_provenance_sha256 ~ '^[0-9a-f]{64}$'
        AND feature_schema_sha256 ~ '^[0-9a-f]{64}$'
        AND knowledge_catalog_sha256 ~ '^[0-9a-f]{64}$'
        AND parameter_sha256 ~ '^[0-9a-f]{64}$'
        AND golden_vectors_sha256 ~ '^[0-9a-f]{64}$'
        AND manifest_sha256 ~ '^[0-9a-f]{64}$'
    ),
    CONSTRAINT recommendation_model_releases_manifest_object CHECK (
        jsonb_typeof(manifest) = 'object'
    ),
    CONSTRAINT recommendation_model_releases_id_artifact_unique UNIQUE (
        recommendation_model_release_id,
        artifact_sha256
    )
);

CREATE TABLE ascendany.recommendation_model_activation_events (
    head_revision bigint PRIMARY KEY,
    recommendation_model_release_id bigint NOT NULL,
    artifact_sha256 text NOT NULL,
    application_version text NOT NULL,
    application_commit text NOT NULL,
    application_build_time text NOT NULL,
    activated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT recommendation_model_activation_events_revision_positive CHECK (
        head_revision > 0
    ),
    CONSTRAINT recommendation_model_activation_events_release_fk FOREIGN KEY (
        recommendation_model_release_id,
        artifact_sha256
    ) REFERENCES ascendany.recommendation_model_releases (
        recommendation_model_release_id,
        artifact_sha256
    ) ON DELETE RESTRICT,
    CONSTRAINT recommendation_model_activation_events_application_identity CHECK (
        application_version = btrim(application_version)
        AND application_version <> ''
        AND octet_length(application_version) <= 128
        AND application_commit = btrim(application_commit)
        AND application_commit <> ''
        AND octet_length(application_commit) <= 128
        AND application_build_time = btrim(application_build_time)
        AND application_build_time <> ''
        AND octet_length(application_build_time) <= 128
    )
);

CREATE TABLE ascendany.recommendation_model_head (
    singleton boolean PRIMARY KEY DEFAULT true,
    current_release_id bigint NOT NULL,
    head_revision bigint NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CONSTRAINT recommendation_model_head_singleton CHECK (singleton),
    CONSTRAINT recommendation_model_head_release_fk FOREIGN KEY (
        current_release_id
    ) REFERENCES ascendany.recommendation_model_releases (
        recommendation_model_release_id
    ) ON DELETE RESTRICT,
    CONSTRAINT recommendation_model_head_revision_positive CHECK (
        head_revision > 0
    )
);

CREATE FUNCTION ascendany.enforce_recommendation_model_head_transition()
RETURNS trigger
LANGUAGE plpgsql
AS $function$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'recommendation model head is permanent'
            USING ERRCODE = '55000';
    END IF;
    IF TG_OP = 'INSERT' THEN
        IF NEW.singleton IS DISTINCT FROM true OR NEW.head_revision <> 1 THEN
            RAISE EXCEPTION 'recommendation model head must start at revision 1'
                USING ERRCODE = '23514';
        END IF;
        RETURN NEW;
    END IF;
    IF NEW.singleton IS DISTINCT FROM true
       OR NEW.head_revision <> OLD.head_revision + 1
       OR NEW.updated_at < OLD.updated_at THEN
        RAISE EXCEPTION 'recommendation model head must advance by one immutable activation revision'
            USING ERRCODE = '40001';
    END IF;
    RETURN NEW;
END
$function$;

REVOKE ALL ON FUNCTION ascendany.enforce_recommendation_model_head_transition() FROM PUBLIC;

CREATE FUNCTION ascendany.validate_recommendation_model_activation()
RETURNS trigger
LANGUAGE plpgsql
AS $function$
DECLARE
    matching_events bigint;
BEGIN
    SELECT count(*)
    INTO matching_events
    FROM ascendany.recommendation_model_activation_events
    WHERE head_revision = NEW.head_revision
      AND recommendation_model_release_id = NEW.current_release_id;
    IF matching_events <> 1 THEN
        RAISE EXCEPTION 'recommendation model head revision % requires one matching activation event', NEW.head_revision
            USING ERRCODE = '23514';
    END IF;
    RETURN NULL;
END
$function$;

REVOKE ALL ON FUNCTION ascendany.validate_recommendation_model_activation() FROM PUBLIC;

CREATE TRIGGER recommendation_model_head_transition
BEFORE INSERT OR UPDATE OR DELETE ON ascendany.recommendation_model_head
FOR EACH ROW EXECUTE FUNCTION ascendany.enforce_recommendation_model_head_transition();

CREATE CONSTRAINT TRIGGER recommendation_model_head_activation_complete
AFTER INSERT OR UPDATE ON ascendany.recommendation_model_head
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION ascendany.validate_recommendation_model_activation();

CREATE TRIGGER recommendation_model_releases_immutable_rows
BEFORE UPDATE OR DELETE ON ascendany.recommendation_model_releases
FOR EACH ROW EXECUTE FUNCTION ascendany.reject_immutable_mutation();

CREATE TRIGGER recommendation_model_releases_immutable_truncate
BEFORE TRUNCATE ON ascendany.recommendation_model_releases
FOR EACH STATEMENT EXECUTE FUNCTION ascendany.reject_immutable_mutation();

CREATE TRIGGER recommendation_model_activation_events_immutable_rows
BEFORE UPDATE OR DELETE ON ascendany.recommendation_model_activation_events
FOR EACH ROW EXECUTE FUNCTION ascendany.reject_immutable_mutation();

CREATE TRIGGER recommendation_model_activation_events_immutable_truncate
BEFORE TRUNCATE ON ascendany.recommendation_model_activation_events
FOR EACH STATEMENT EXECUTE FUNCTION ascendany.reject_immutable_mutation();

REVOKE ALL PRIVILEGES ON TABLE
    ascendany.recommendation_model_releases,
    ascendany.recommendation_model_activation_events,
    ascendany.recommendation_model_head
FROM ascendany_runtime, ascendany_backup, PUBLIC;

GRANT SELECT ON TABLE
    ascendany.recommendation_model_releases,
    ascendany.recommendation_model_activation_events,
    ascendany.recommendation_model_head
TO ascendany_runtime, ascendany_backup;

GRANT INSERT ON TABLE
    ascendany.recommendation_model_releases,
    ascendany.recommendation_model_activation_events,
    ascendany.recommendation_model_head
TO ascendany_runtime;

GRANT UPDATE (
    current_release_id,
    head_revision,
    updated_at
) ON TABLE ascendany.recommendation_model_head TO ascendany_runtime;

REVOKE ALL PRIVILEGES ON SEQUENCE ascendany.recommendation_model_release_ids_seq
FROM ascendany_runtime, ascendany_backup, PUBLIC;

GRANT USAGE, SELECT ON SEQUENCE ascendany.recommendation_model_release_ids_seq
TO ascendany_runtime;

GRANT SELECT ON SEQUENCE ascendany.recommendation_model_release_ids_seq
TO ascendany_backup;
