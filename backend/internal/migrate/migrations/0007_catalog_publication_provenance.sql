SET LOCAL search_path = ascendany, pg_catalog;

DO $preflight$
DECLARE
    schema_owner text;
    prerequisite_count bigint;
BEGIN
    IF current_database() <> 'ascendany_v2' THEN
        RAISE EXCEPTION 'catalog publication provenance migration requires database ascendany_v2';
    END IF;
    IF current_user <> 'ascendany_owner' THEN
        RAISE EXCEPTION 'catalog publication provenance migration requires current role ascendany_owner';
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
    WHERE version = 6
      AND name = 'inference_model_runtime';
    IF prerequisite_count <> 1 THEN
        RAISE EXCEPTION 'catalog publication provenance migration requires schema version 6';
    END IF;
END
$preflight$;

ALTER TABLE ascendany.recommendation_model_releases
ADD CONSTRAINT recommendation_model_release_catalog_identity_unique UNIQUE (
    recommendation_model_release_id,
    artifact_sha256,
    model_id,
    knowledge_catalog_sha256
);

ALTER TABLE ascendany.recommendation_model_activation_events
ADD CONSTRAINT recommendation_model_activation_events_head_artifact_unique UNIQUE (
    head_revision,
    artifact_sha256
);

CREATE TABLE ascendany.knowledge_catalog_publication_authorizations (
    public_id uuid PRIMARY KEY,
    access_jwt_id uuid NOT NULL,
    access_token_sha256 text NOT NULL,
    request_canonical_json text NOT NULL,
    configuration_public_id uuid NOT NULL,
    expected_configuration_head_revision bigint NOT NULL,
    expected_analytics_generation_id bigint NOT NULL,
    expected_analytics_head_revision bigint NOT NULL,
    expected_input_manifest_sha256 text NOT NULL,
    expected_current_model_head_revision bigint NOT NULL,
    expected_current_model_artifact_sha256 text NOT NULL,
    catalog_schema_id text NOT NULL,
    catalog_document jsonb NOT NULL,
    catalog_sha256 text NOT NULL,
    target_model_release_id bigint NOT NULL,
    target_model_id uuid NOT NULL,
    target_model_artifact_sha256 text NOT NULL,
    target_application_version text NOT NULL,
    target_application_commit text NOT NULL,
    target_application_build_time text NOT NULL,
    authorized_account_id bigint NOT NULL,
    authorized_session_id bigint NOT NULL,
    authorized_auth_revision bigint NOT NULL,
    access_token_expires_at timestamptz NOT NULL,
    authorized_at timestamptz NOT NULL,
    consumed_publication_id bigint,
    consumed_at timestamptz,
    CONSTRAINT catalog_publication_auth_jwt_unique UNIQUE (access_jwt_id),
    CONSTRAINT catalog_publication_auth_request_unique UNIQUE (request_canonical_json),
    CONSTRAINT catalog_publication_auth_consumed_unique UNIQUE (consumed_publication_id),
    CONSTRAINT catalog_publication_authorizations_public_id_nonzero CHECK (
        public_id <> '00000000-0000-0000-0000-000000000000'::uuid
    ),
    CONSTRAINT catalog_publication_authorizations_jwt_id_nonzero CHECK (
        access_jwt_id <> '00000000-0000-0000-0000-000000000000'::uuid
    ),
    CONSTRAINT catalog_publication_authorizations_token_hash CHECK (
        access_token_sha256 ~ '^[0-9a-f]{64}$'
    ),
    CONSTRAINT catalog_publication_authorizations_configuration_id_nonzero CHECK (
        configuration_public_id <> '00000000-0000-0000-0000-000000000000'::uuid
    ),
    CONSTRAINT catalog_publication_authorizations_expected_heads CHECK (
        expected_configuration_head_revision >= 0
        AND expected_analytics_generation_id > 0
        AND expected_analytics_head_revision > 0
        AND expected_current_model_head_revision > 0
    ),
    CONSTRAINT catalog_publication_authorizations_expected_hashes CHECK (
        expected_input_manifest_sha256 ~ '^[0-9a-f]{64}$'
        AND expected_current_model_artifact_sha256 ~ '^[0-9a-f]{64}$'
        AND catalog_sha256 ~ '^[0-9a-f]{64}$'
        AND target_model_artifact_sha256 ~ '^[0-9a-f]{64}$'
    ),
    CONSTRAINT catalog_publication_authorizations_catalog_contract CHECK (
        catalog_schema_id = 'ascendany.knowledge_catalog.recommendation.v1'
        AND (jsonb_typeof(catalog_document) = 'object') IS TRUE
        AND octet_length(catalog_document::text) <= 262144
    ),
    CONSTRAINT catalog_publication_authorizations_target_model_fk FOREIGN KEY (
        target_model_release_id,
        target_model_artifact_sha256,
        target_model_id,
        catalog_sha256
    ) REFERENCES ascendany.recommendation_model_releases (
        recommendation_model_release_id,
        artifact_sha256,
        model_id,
        knowledge_catalog_sha256
    ) ON DELETE RESTRICT,
    CONSTRAINT catalog_publication_authorizations_actor_session_fk FOREIGN KEY (
        authorized_session_id,
        authorized_account_id
    ) REFERENCES ascendany.auth_sessions (
        session_id,
        account_id
    ) ON DELETE RESTRICT,
    CONSTRAINT catalog_publication_authorizations_application_identity CHECK (
        target_application_version = btrim(target_application_version)
        AND target_application_version <> ''
        AND octet_length(target_application_version) <= 128
        AND target_application_commit = btrim(target_application_commit)
        AND target_application_commit <> ''
        AND octet_length(target_application_commit) <= 128
        AND target_application_build_time = btrim(target_application_build_time)
        AND target_application_build_time <> ''
        AND octet_length(target_application_build_time) <= 128
    ),
    CONSTRAINT catalog_publication_authorizations_time_order CHECK (
        access_token_expires_at > authorized_at
        AND (consumed_at IS NULL OR consumed_at >= authorized_at)
    ),
    CONSTRAINT catalog_publication_authorizations_consumption_pair CHECK (
        (consumed_publication_id IS NULL AND consumed_at IS NULL)
        OR (consumed_publication_id IS NOT NULL AND consumed_at IS NOT NULL)
    ),
    CONSTRAINT catalog_publication_authorizations_request_contract CHECK ((
        octet_length(request_canonical_json) BETWEEN 1 AND 4096
        AND request_canonical_json IS JSON OBJECT WITH UNIQUE KEYS
        AND jsonb_typeof(request_canonical_json::jsonb) = 'object'
        AND request_canonical_json::jsonb ?& ARRAY[
            'schema',
            'authorizationId',
            'expectedConfigurationHeadRevision',
            'expectedAnalyticsGenerationId',
            'expectedAnalyticsHeadRevision',
            'expectedInputManifestSha256',
            'expectedCurrentModelHeadRevision',
            'expectedCurrentModelArtifactSha256',
            'targetCatalogSha256',
            'targetModelArtifactSha256',
            'targetApplicationVersion',
            'targetApplicationCommit',
            'targetApplicationBuildTime'
        ]::text[]
        AND request_canonical_json::jsonb - ARRAY[
            'schema',
            'authorizationId',
            'expectedConfigurationHeadRevision',
            'expectedAnalyticsGenerationId',
            'expectedAnalyticsHeadRevision',
            'expectedInputManifestSha256',
            'expectedCurrentModelHeadRevision',
            'expectedCurrentModelArtifactSha256',
            'targetCatalogSha256',
            'targetModelArtifactSha256',
            'targetApplicationVersion',
            'targetApplicationCommit',
            'targetApplicationBuildTime'
        ]::text[] = '{}'::jsonb
        AND jsonb_typeof(request_canonical_json::jsonb -> 'schema') = 'string'
        AND jsonb_typeof(request_canonical_json::jsonb -> 'authorizationId') = 'string'
        AND jsonb_typeof(request_canonical_json::jsonb -> 'expectedConfigurationHeadRevision') = 'number'
        AND jsonb_typeof(request_canonical_json::jsonb -> 'expectedAnalyticsGenerationId') = 'string'
        AND jsonb_typeof(request_canonical_json::jsonb -> 'expectedAnalyticsHeadRevision') = 'number'
        AND jsonb_typeof(request_canonical_json::jsonb -> 'expectedInputManifestSha256') = 'string'
        AND jsonb_typeof(request_canonical_json::jsonb -> 'expectedCurrentModelHeadRevision') = 'number'
        AND jsonb_typeof(request_canonical_json::jsonb -> 'expectedCurrentModelArtifactSha256') = 'string'
        AND jsonb_typeof(request_canonical_json::jsonb -> 'targetCatalogSha256') = 'string'
        AND jsonb_typeof(request_canonical_json::jsonb -> 'targetModelArtifactSha256') = 'string'
        AND jsonb_typeof(request_canonical_json::jsonb -> 'targetApplicationVersion') = 'string'
        AND jsonb_typeof(request_canonical_json::jsonb -> 'targetApplicationCommit') = 'string'
        AND jsonb_typeof(request_canonical_json::jsonb -> 'targetApplicationBuildTime') = 'string'
        AND request_canonical_json::jsonb ->> 'schema' = 'ascendany.knowledge_catalog.publication-request.v1'
        AND request_canonical_json::jsonb ->> 'authorizationId' = public_id::text
        AND request_canonical_json::jsonb ->> 'expectedConfigurationHeadRevision' ~ '^(0|[1-9][0-9]*)$'
        AND (request_canonical_json::jsonb ->> 'expectedConfigurationHeadRevision')::bigint = expected_configuration_head_revision
        AND request_canonical_json::jsonb ->> 'expectedAnalyticsGenerationId' ~ '^[1-9][0-9]*$'
        AND (request_canonical_json::jsonb ->> 'expectedAnalyticsGenerationId')::bigint = expected_analytics_generation_id
        AND request_canonical_json::jsonb ->> 'expectedAnalyticsHeadRevision' ~ '^[1-9][0-9]*$'
        AND (request_canonical_json::jsonb ->> 'expectedAnalyticsHeadRevision')::bigint = expected_analytics_head_revision
        AND request_canonical_json::jsonb ->> 'expectedInputManifestSha256' = expected_input_manifest_sha256
        AND request_canonical_json::jsonb ->> 'expectedCurrentModelHeadRevision' ~ '^[1-9][0-9]*$'
        AND (request_canonical_json::jsonb ->> 'expectedCurrentModelHeadRevision')::bigint = expected_current_model_head_revision
        AND request_canonical_json::jsonb ->> 'expectedCurrentModelArtifactSha256' = expected_current_model_artifact_sha256
        AND request_canonical_json::jsonb ->> 'targetCatalogSha256' = catalog_sha256
        AND request_canonical_json::jsonb ->> 'targetModelArtifactSha256' = target_model_artifact_sha256
        AND request_canonical_json::jsonb ->> 'targetApplicationVersion' = target_application_version
        AND request_canonical_json::jsonb ->> 'targetApplicationCommit' = target_application_commit
        AND request_canonical_json::jsonb ->> 'targetApplicationBuildTime' = target_application_build_time
    ) IS TRUE)
);

CREATE TABLE ascendany.knowledge_catalog_publications (
    knowledge_catalog_publication_id bigint GENERATED ALWAYS AS IDENTITY (
        SEQUENCE NAME ascendany.knowledge_catalog_publication_ids_seq
    ) PRIMARY KEY,
    publication_authorization_id uuid NOT NULL,
    configuration_item_id bigint NOT NULL,
    configuration_version_id bigint NOT NULL,
    expected_configuration_head_revision bigint NOT NULL,
    configuration_head_revision bigint NOT NULL,
    configuration_mutated boolean NOT NULL,
    catalog_sha256 text NOT NULL,
    target_model_release_id bigint NOT NULL,
    target_model_id uuid NOT NULL,
    target_model_artifact_sha256 text NOT NULL,
    target_application_version text NOT NULL,
    target_application_commit text NOT NULL,
    target_application_build_time text NOT NULL,
    analytics_generation_id bigint NOT NULL,
    analytics_head_revision bigint NOT NULL,
    input_manifest_sha256 text NOT NULL,
    current_model_head_revision bigint NOT NULL,
    current_model_artifact_sha256 text NOT NULL,
    published_by_account_id bigint NOT NULL,
    published_by_session_id bigint NOT NULL,
    published_at timestamptz NOT NULL,
    audit_event_id bigint NOT NULL,
    CONSTRAINT knowledge_catalog_publications_auth_unique UNIQUE (
        publication_authorization_id
    ),
    CONSTRAINT knowledge_catalog_publications_authorization_fk FOREIGN KEY (
        publication_authorization_id
    ) REFERENCES ascendany.knowledge_catalog_publication_authorizations (
        public_id
    ) ON DELETE RESTRICT,
    CONSTRAINT knowledge_catalog_publications_configuration_version_fk FOREIGN KEY (
        configuration_item_id,
        configuration_version_id
    ) REFERENCES ascendany.configuration_versions (
        configuration_item_id,
        configuration_version_id
    ) ON DELETE RESTRICT,
    CONSTRAINT knowledge_catalog_publications_analytics_generation_fk FOREIGN KEY (
        analytics_generation_id
    ) REFERENCES ascendany.analytics_generations (
        analytics_generation_id
    ) ON DELETE RESTRICT,
    CONSTRAINT knowledge_catalog_publications_actor_session_fk FOREIGN KEY (
        published_by_session_id,
        published_by_account_id
    ) REFERENCES ascendany.auth_sessions (
        session_id,
        account_id
    ) ON DELETE RESTRICT,
    CONSTRAINT knowledge_catalog_publications_target_model_fk FOREIGN KEY (
        target_model_release_id,
        target_model_artifact_sha256,
        target_model_id,
        catalog_sha256
    ) REFERENCES ascendany.recommendation_model_releases (
        recommendation_model_release_id,
        artifact_sha256,
        model_id,
        knowledge_catalog_sha256
    ) ON DELETE RESTRICT,
    CONSTRAINT knowledge_catalog_publications_current_model_activation_fk FOREIGN KEY (
        current_model_head_revision,
        current_model_artifact_sha256
    ) REFERENCES ascendany.recommendation_model_activation_events (
        head_revision,
        artifact_sha256
    ) ON DELETE RESTRICT,
    CONSTRAINT knowledge_catalog_publications_audit_event_fk FOREIGN KEY (
        audit_event_id
    ) REFERENCES ascendany.audit_events (
        audit_event_id
    ) ON DELETE RESTRICT,
    CONSTRAINT knowledge_catalog_publications_head_positive CHECK (
        expected_configuration_head_revision >= 0
        AND (
            (configuration_mutated AND configuration_head_revision = expected_configuration_head_revision + 1)
            OR
            (NOT configuration_mutated AND configuration_head_revision = expected_configuration_head_revision)
        )
    ),
    CONSTRAINT knowledge_catalog_publications_catalog_hash CHECK (
        catalog_sha256 ~ '^[0-9a-f]{64}$'
    ),
    CONSTRAINT knowledge_catalog_publications_target_model_id_nonzero CHECK (
        target_model_id <> '00000000-0000-0000-0000-000000000000'::uuid
    ),
    CONSTRAINT knowledge_catalog_publications_target_model_hash CHECK (
        target_model_artifact_sha256 ~ '^[0-9a-f]{64}$'
    ),
    CONSTRAINT knowledge_catalog_publications_target_application_identity CHECK (
        target_application_version = btrim(target_application_version)
        AND target_application_version <> ''
        AND octet_length(target_application_version) <= 128
        AND target_application_commit = btrim(target_application_commit)
        AND target_application_commit <> ''
        AND octet_length(target_application_commit) <= 128
        AND target_application_build_time = btrim(target_application_build_time)
        AND target_application_build_time <> ''
        AND octet_length(target_application_build_time) <= 128
    ),
    CONSTRAINT knowledge_catalog_publications_analytics_positive CHECK (
        analytics_generation_id > 0
        AND analytics_head_revision > 0
    ),
    CONSTRAINT knowledge_catalog_publications_input_manifest_hash CHECK (
        input_manifest_sha256 ~ '^[0-9a-f]{64}$'
    ),
    CONSTRAINT knowledge_catalog_publications_current_model_positive CHECK (
        current_model_head_revision > 0
    ),
    CONSTRAINT knowledge_catalog_publications_current_model_hash CHECK (
        current_model_artifact_sha256 ~ '^[0-9a-f]{64}$'
    ),
    CONSTRAINT knowledge_catalog_publications_activation_intent_unique UNIQUE (
        target_model_release_id,
        target_model_artifact_sha256,
        catalog_sha256,
        current_model_head_revision,
        current_model_artifact_sha256,
        target_application_version,
        target_application_commit,
        target_application_build_time
    ),
    CONSTRAINT knowledge_catalog_publications_activation_reference_unique UNIQUE (
        knowledge_catalog_publication_id,
        current_model_head_revision,
        target_model_release_id,
        target_model_artifact_sha256,
        target_application_version,
        target_application_commit,
        target_application_build_time
    ),
    CONSTRAINT knowledge_catalog_publications_intent_unique UNIQUE (
        configuration_item_id,
        configuration_version_id,
        expected_configuration_head_revision,
        configuration_head_revision,
        analytics_generation_id,
        analytics_head_revision,
        input_manifest_sha256,
        current_model_head_revision,
        current_model_artifact_sha256,
        catalog_sha256,
        target_model_release_id,
        target_model_id,
        target_model_artifact_sha256,
        target_application_version,
        target_application_commit,
        target_application_build_time
    ),
    CONSTRAINT knowledge_catalog_publications_audit_event_unique UNIQUE (
        audit_event_id
    )
);

ALTER TABLE ascendany.knowledge_catalog_publication_authorizations
ADD CONSTRAINT catalog_publication_authorizations_consumed_publication_fk FOREIGN KEY (
    consumed_publication_id
) REFERENCES ascendany.knowledge_catalog_publications (
    knowledge_catalog_publication_id
) ON DELETE RESTRICT;

CREATE FUNCTION ascendany.enforce_catalog_publication_authorization_transition()
RETURNS trigger
LANGUAGE plpgsql
AS $function$
DECLARE
    matching_rows bigint;
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'catalog publication authorization is permanent'
            USING ERRCODE = '55000';
    END IF;
    IF to_jsonb(NEW) - ARRAY['consumed_publication_id', 'consumed_at']::text[]
       IS DISTINCT FROM
       to_jsonb(OLD) - ARRAY['consumed_publication_id', 'consumed_at']::text[]
       OR OLD.consumed_publication_id IS NOT NULL
       OR OLD.consumed_at IS NOT NULL
       OR NEW.consumed_publication_id IS NULL
       OR NEW.consumed_at IS NULL THEN
        RAISE EXCEPTION 'catalog publication authorization transition is invalid'
            USING ERRCODE = '55000';
    END IF;
    SELECT count(*)
    INTO matching_rows
    FROM ascendany.knowledge_catalog_publications AS publication
    WHERE publication.knowledge_catalog_publication_id = NEW.consumed_publication_id
      AND publication.publication_authorization_id = NEW.public_id;
    IF matching_rows <> 1 THEN
        RAISE EXCEPTION 'catalog publication authorization consumption is unbound'
            USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END
$function$;

REVOKE ALL ON FUNCTION ascendany.enforce_catalog_publication_authorization_transition()
FROM PUBLIC;

CREATE TRIGGER catalog_publication_authorizations_transition
BEFORE UPDATE OR DELETE ON ascendany.knowledge_catalog_publication_authorizations
FOR EACH ROW EXECUTE FUNCTION ascendany.enforce_catalog_publication_authorization_transition();

CREATE TRIGGER catalog_publication_authorizations_immutable_truncate
BEFORE TRUNCATE ON ascendany.knowledge_catalog_publication_authorizations
FOR EACH STATEMENT EXECUTE FUNCTION ascendany.reject_immutable_mutation();

CREATE TRIGGER knowledge_catalog_publications_immutable_rows
BEFORE UPDATE OR DELETE ON ascendany.knowledge_catalog_publications
FOR EACH ROW EXECUTE FUNCTION ascendany.reject_immutable_mutation();

CREATE TRIGGER knowledge_catalog_publications_immutable_truncate
BEFORE TRUNCATE ON ascendany.knowledge_catalog_publications
FOR EACH STATEMENT EXECUTE FUNCTION ascendany.reject_immutable_mutation();

ALTER TABLE ascendany.recommendation_model_head
ADD COLUMN pending_catalog_publication_id bigint,
ADD CONSTRAINT recommendation_model_head_pending_publication_fk FOREIGN KEY (
    pending_catalog_publication_id
) REFERENCES ascendany.knowledge_catalog_publications (
    knowledge_catalog_publication_id
) ON DELETE RESTRICT,
ADD CONSTRAINT recommendation_model_head_pending_publication_unique UNIQUE (
    pending_catalog_publication_id
);

CREATE OR REPLACE FUNCTION ascendany.enforce_recommendation_model_head_transition()
RETURNS trigger
LANGUAGE plpgsql
AS $function$
DECLARE
    matching_rows bigint;
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'recommendation model head is permanent'
            USING ERRCODE = '55000';
    END IF;
    IF TG_OP = 'INSERT' THEN
        IF NEW.singleton IS DISTINCT FROM true
           OR NEW.head_revision <> 1
           OR NEW.pending_catalog_publication_id IS NOT NULL THEN
            RAISE EXCEPTION 'recommendation model head must start at revision 1 without a pending publication'
                USING ERRCODE = '23514';
        END IF;
        RETURN NEW;
    END IF;

    IF NEW.singleton IS NOT DISTINCT FROM true
       AND NEW.current_release_id = OLD.current_release_id
       AND NEW.head_revision = OLD.head_revision
       AND NEW.updated_at = OLD.updated_at
       AND OLD.pending_catalog_publication_id IS NULL
       AND NEW.pending_catalog_publication_id IS NOT NULL THEN
        SELECT count(*)
        INTO matching_rows
        FROM ascendany.knowledge_catalog_publications AS publication
        JOIN ascendany.recommendation_model_releases AS current_release
          ON current_release.recommendation_model_release_id = OLD.current_release_id
         AND current_release.artifact_sha256 = publication.current_model_artifact_sha256
        WHERE publication.knowledge_catalog_publication_id = NEW.pending_catalog_publication_id
          AND publication.current_model_head_revision = OLD.head_revision;
        IF matching_rows <> 1 THEN
            RAISE EXCEPTION 'pending catalog publication does not target the current model head'
                USING ERRCODE = '23514';
        END IF;
        RETURN NEW;
    END IF;

    IF NEW.singleton IS NOT DISTINCT FROM true
       AND NEW.head_revision = OLD.head_revision + 1
       AND NEW.updated_at >= OLD.updated_at
       AND OLD.pending_catalog_publication_id IS NOT NULL
       AND NEW.pending_catalog_publication_id IS NULL THEN
        SELECT count(*)
        INTO matching_rows
        FROM ascendany.recommendation_model_activation_events AS activation
        WHERE activation.head_revision = NEW.head_revision
          AND activation.recommendation_model_release_id = NEW.current_release_id
          AND activation.knowledge_catalog_publication_id = OLD.pending_catalog_publication_id;
        IF matching_rows <> 1 THEN
            RAISE EXCEPTION 'model head activation must consume its pending catalog publication'
                USING ERRCODE = '23514';
        END IF;
        RETURN NEW;
    END IF;

    RAISE EXCEPTION 'recommendation model head transition violates the publication state machine'
        USING ERRCODE = '40001';
END
$function$;

-- The head-completeness constraint trigger is deferred until transaction
-- commit. Run its cross-table invariant with schema-owner privileges so every
-- capability role can commit only through the same database-owned validator.
ALTER FUNCTION ascendany.validate_recommendation_model_activation()
SECURITY DEFINER;

ALTER FUNCTION ascendany.validate_recommendation_model_activation()
SET search_path = pg_catalog;

ALTER TABLE ascendany.recommendation_model_activation_events
ADD COLUMN knowledge_catalog_publication_id bigint,
ADD COLUMN publication_current_model_head_revision bigint GENERATED ALWAYS AS (
    head_revision - 1
) STORED,
ADD CONSTRAINT recommendation_model_activation_events_catalog_publication_fk FOREIGN KEY (
    knowledge_catalog_publication_id,
    publication_current_model_head_revision,
    recommendation_model_release_id,
    artifact_sha256,
    application_version,
    application_commit,
    application_build_time
) REFERENCES ascendany.knowledge_catalog_publications (
    knowledge_catalog_publication_id,
    current_model_head_revision,
    target_model_release_id,
    target_model_artifact_sha256,
    target_application_version,
    target_application_commit,
    target_application_build_time
) ON DELETE RESTRICT,
ADD CONSTRAINT recommendation_model_activation_catalog_publication_unique UNIQUE (
    knowledge_catalog_publication_id
),
ADD CONSTRAINT recommendation_model_activation_catalog_publication_required CHECK (
    (head_revision = 1 AND knowledge_catalog_publication_id IS NULL)
    OR
    (head_revision > 1 AND knowledge_catalog_publication_id IS NOT NULL)
);

CREATE FUNCTION ascendany.catalog_publication_result(
    requested_publication_id bigint,
    idempotent_result boolean
)
RETURNS jsonb
LANGUAGE sql
SECURITY DEFINER
SET search_path = pg_catalog
AS $function$
SELECT jsonb_build_object(
    'item', jsonb_build_object(
        'id', item.public_id::text,
        'key', item.configuration_key,
        'kind', item.configuration_kind,
        'headRevision', publication.configuration_head_revision,
        'activeVersion', jsonb_build_object(
            'id', version.configuration_version_id::text,
            'number', version.version_number,
            'schemaId', version.schema_id,
            'document', version.document,
            'documentSha256', version.document_sha256,
            'credentialRef', version.credential_ref,
            'createdByAccountId', version_account.public_id::text,
            'createdBySessionId', version_session.public_id::text,
            'createdAt', version.created_at
        ),
        'createdAt', item.created_at,
        'updatedAt', version.created_at
    ),
    'idempotent', idempotent_result,
    'auditEventId', publication.audit_event_id,
    'knowledgeCatalogPublication', jsonb_build_object(
        'authorizationId', publication.publication_authorization_id::text,
        'knowledgeCatalogPublicationId', publication.knowledge_catalog_publication_id::text,
        'targetModelReleaseId', publication.target_model_release_id::text,
        'catalogSha256', publication.catalog_sha256,
        'targetModelArtifactSha256', publication.target_model_artifact_sha256,
        'targetModelId', publication.target_model_id::text,
        'targetApplicationVersion', publication.target_application_version,
        'targetApplicationCommit', publication.target_application_commit,
        'targetApplicationBuildTime', publication.target_application_build_time,
        'configurationId', item.public_id::text,
        'expectedConfigurationHeadRevision', publication.expected_configuration_head_revision,
        'configurationHeadRevision', publication.configuration_head_revision,
        'configurationMutated', publication.configuration_mutated,
        'configurationVersionId', version.configuration_version_id::text,
        'configurationVersionNumber', version.version_number,
        'analyticsGenerationId', publication.analytics_generation_id::text,
        'analyticsHeadRevision', publication.analytics_head_revision,
        'inputManifestSha256', publication.input_manifest_sha256,
        'currentModelHeadRevision', publication.current_model_head_revision,
        'currentModelArtifactSha256', publication.current_model_artifact_sha256,
        'publishedByAccountId', publication_account.public_id::text,
        'publishedBySessionId', publication_session.public_id::text,
        'publishedAt', publication.published_at,
        'auditEventId', publication.audit_event_id
    )
)
FROM ascendany.knowledge_catalog_publications AS publication
JOIN ascendany.knowledge_catalog_publication_authorizations AS capability
  ON capability.public_id = publication.publication_authorization_id
 AND capability.consumed_publication_id = publication.knowledge_catalog_publication_id
JOIN ascendany.configuration_items AS item
  ON item.configuration_item_id = publication.configuration_item_id
  AND item.configuration_kind = 'knowledge_catalog'
JOIN ascendany.configuration_versions AS version
  ON version.configuration_version_id = publication.configuration_version_id
 AND version.configuration_item_id = publication.configuration_item_id
 AND version.configuration_kind = 'knowledge_catalog'
 AND version.document_sha256 = publication.catalog_sha256
JOIN ascendany.auth_accounts AS version_account
  ON version_account.account_id = version.created_by_account_id
JOIN ascendany.auth_sessions AS version_session
  ON version_session.session_id = version.created_by_session_id
 AND version_session.account_id = version.created_by_account_id
JOIN ascendany.auth_accounts AS publication_account
  ON publication_account.account_id = publication.published_by_account_id
JOIN ascendany.auth_sessions AS publication_session
  ON publication_session.session_id = publication.published_by_session_id
 AND publication_session.account_id = publication.published_by_account_id
WHERE publication.knowledge_catalog_publication_id = requested_publication_id;
$function$;

REVOKE ALL PRIVILEGES ON FUNCTION ascendany.catalog_publication_result(bigint, boolean)
FROM PUBLIC, ascendany_runtime, ascendany_backup, ascendany_catalog_publisher;

CREATE FUNCTION ascendany.publish_authorized_knowledge_catalog(
    authorization_public_id uuid,
    supplied_access_token_sha256 text,
    supplied_request_canonical_json text
)
RETURNS jsonb
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $function$
DECLARE
    capability ascendany.knowledge_catalog_publication_authorizations%ROWTYPE;
    current_model_head_revision bigint;
    current_model_release_id bigint;
    current_model_artifact_sha256 text;
    current_model_pending_publication_id bigint;
    current_analytics_generation_id bigint;
    current_analytics_head_revision bigint;
    current_input_manifest_sha256 text;
    current_analytics_status text;
    current_analytics_base_head_revision bigint;
    stored_configuration_item_id bigint;
    configuration_public_id uuid;
    configuration_kind text;
    configuration_active_version_id bigint;
    configuration_head_revision bigint;
    configuration_created_at timestamptz;
    configuration_updated_at timestamptz;
    stored_configuration_version_id bigint;
    configuration_version_number bigint;
    configuration_version_schema_id text;
    configuration_version_credential_ref text;
    configuration_mutated boolean;
    current_account_role text;
    current_account_auth_revision bigint;
    current_account_disabled_at timestamptz;
    current_session_auth_revision bigint;
    current_session_expires_at timestamptz;
    current_session_revoked_at timestamptz;
    authorized_use_at timestamptz;
    published_at timestamptz;
    stored_audit_event_id bigint;
    stored_publication_id bigint;
    affected_rows bigint;
    result jsonb;
BEGIN
    IF authorization_public_id IS NULL
       OR supplied_access_token_sha256 !~ '^[0-9a-f]{64}$'
       OR supplied_request_canonical_json IS NULL
       OR octet_length(supplied_request_canonical_json) NOT BETWEEN 1 AND 4096 THEN
        RAISE EXCEPTION 'catalog publication capability input is invalid'
            USING ERRCODE = '28000';
    END IF;

    PERFORM pg_catalog.pg_advisory_xact_lock(4707180034853717324);

    SELECT stored.*
    INTO capability
    FROM ascendany.knowledge_catalog_publication_authorizations AS stored
    WHERE stored.public_id = authorization_public_id
      AND stored.access_token_sha256 = supplied_access_token_sha256
      AND stored.request_canonical_json = supplied_request_canonical_json
    FOR UPDATE OF stored;

    IF NOT FOUND THEN
        RAISE EXCEPTION 'catalog publication capability is unauthorized'
            USING ERRCODE = '28000';
    END IF;

    IF capability.consumed_publication_id IS NOT NULL THEN
        SELECT ascendany.catalog_publication_result(
            capability.consumed_publication_id,
            true
        )
        INTO result;
        IF result IS NULL THEN
            RAISE EXCEPTION 'consumed catalog publication capability is inconsistent'
                USING ERRCODE = '23514';
        END IF;
        RETURN result;
    END IF;

    SELECT account.role,
           account.auth_revision,
           account.disabled_at,
           session.auth_revision,
           session.expires_at,
           session.revoked_at
    INTO current_account_role,
         current_account_auth_revision,
         current_account_disabled_at,
         current_session_auth_revision,
         current_session_expires_at,
         current_session_revoked_at
    FROM ascendany.auth_accounts AS account
    JOIN ascendany.auth_sessions AS session
      ON session.session_id = capability.authorized_session_id
     AND session.account_id = account.account_id
    WHERE account.account_id = capability.authorized_account_id
    FOR UPDATE OF account, session;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'catalog publication capability actor is missing'
            USING ERRCODE = '23514';
    END IF;

    authorized_use_at := pg_catalog.clock_timestamp();
    IF capability.consumed_at IS NOT NULL
       OR capability.access_token_expires_at <= authorized_use_at
       OR current_account_role <> 'admin'
       OR current_account_auth_revision <> capability.authorized_auth_revision
       OR current_account_disabled_at IS NOT NULL
       OR current_session_auth_revision <> capability.authorized_auth_revision
       OR current_session_revoked_at IS NOT NULL
       OR current_session_expires_at <= authorized_use_at THEN
        RAISE EXCEPTION 'catalog publication capability is expired, consumed, or unauthorized'
            USING ERRCODE = '28000';
    END IF;

    SELECT head.current_release_id,
           head.head_revision,
           release.artifact_sha256,
           head.pending_catalog_publication_id
    INTO current_model_release_id,
         current_model_head_revision,
         current_model_artifact_sha256,
         current_model_pending_publication_id
    FROM ascendany.recommendation_model_head AS head
    JOIN ascendany.recommendation_model_releases AS release
      ON release.recommendation_model_release_id = head.current_release_id
    JOIN ascendany.recommendation_model_activation_events AS activation
      ON activation.head_revision = head.head_revision
     AND activation.recommendation_model_release_id = head.current_release_id
     AND activation.artifact_sha256 = release.artifact_sha256
    WHERE head.singleton
    FOR UPDATE OF head;

    IF NOT FOUND
       OR current_model_pending_publication_id IS NOT NULL
       OR current_model_head_revision <> capability.expected_current_model_head_revision
       OR current_model_artifact_sha256 <> capability.expected_current_model_artifact_sha256 THEN
        RAISE EXCEPTION 'catalog publication current model binding changed'
            USING ERRCODE = '40001';
    END IF;

    SELECT head.current_generation_id,
           head.head_revision,
           generation.input_manifest_sha256,
           generation.status,
           generation.base_head_revision
    INTO current_analytics_generation_id,
         current_analytics_head_revision,
         current_input_manifest_sha256,
         current_analytics_status,
         current_analytics_base_head_revision
    FROM ascendany.analytics_head AS head
    JOIN ascendany.analytics_generations AS generation
      ON generation.analytics_generation_id = head.current_generation_id
    WHERE head.singleton
    FOR UPDATE OF head;

    IF NOT FOUND
       OR current_analytics_status <> 'succeeded'
       OR current_analytics_base_head_revision <> current_analytics_head_revision - 1
       OR current_analytics_generation_id <> capability.expected_analytics_generation_id
       OR current_analytics_head_revision <> capability.expected_analytics_head_revision
       OR current_input_manifest_sha256 <> capability.expected_input_manifest_sha256 THEN
        RAISE EXCEPTION 'catalog publication analytics binding changed'
            USING ERRCODE = '40001';
    END IF;

    SELECT item.configuration_item_id,
           item.public_id,
           item.configuration_kind,
           item.active_version_id,
           item.head_revision,
           item.created_at,
           item.updated_at
    INTO stored_configuration_item_id,
         configuration_public_id,
         configuration_kind,
         configuration_active_version_id,
         configuration_head_revision,
         configuration_created_at,
         configuration_updated_at
    FROM ascendany.configuration_items AS item
    WHERE item.configuration_key = 'recommendation.catalog.active'
    FOR UPDATE OF item;

    IF NOT FOUND THEN
        INSERT INTO ascendany.configuration_items (
            public_id,
            configuration_key,
            configuration_kind
        ) VALUES (
            capability.configuration_public_id,
            'recommendation.catalog.active',
            'knowledge_catalog'
        )
        RETURNING configuration_items.configuration_item_id,
                  configuration_items.public_id,
                  configuration_items.configuration_kind,
                  configuration_items.active_version_id,
                  configuration_items.head_revision,
                  configuration_items.created_at,
                  configuration_items.updated_at
        INTO stored_configuration_item_id,
             configuration_public_id,
             configuration_kind,
             configuration_active_version_id,
             configuration_head_revision,
             configuration_created_at,
             configuration_updated_at;
    END IF;

    IF configuration_public_id <> capability.configuration_public_id
       OR configuration_kind <> 'knowledge_catalog'
       OR configuration_head_revision <> capability.expected_configuration_head_revision THEN
        RAISE EXCEPTION 'catalog configuration head changed'
            USING ERRCODE = '40001';
    END IF;

    SELECT version.configuration_version_id,
           version.version_number,
           version.schema_id,
           version.credential_ref
    INTO stored_configuration_version_id,
         configuration_version_number,
         configuration_version_schema_id,
         configuration_version_credential_ref
    FROM ascendany.configuration_versions AS version
    WHERE version.configuration_item_id = stored_configuration_item_id
      AND version.document_sha256 = capability.catalog_sha256;

    IF FOUND THEN
        IF configuration_active_version_id IS DISTINCT FROM stored_configuration_version_id
           OR configuration_version_schema_id <> capability.catalog_schema_id
           OR configuration_version_credential_ref IS NOT NULL THEN
            RAISE EXCEPTION 'catalog document already exists with a conflicting binding'
                USING ERRCODE = '23514';
        END IF;
        configuration_mutated := false;
        published_at := pg_catalog.clock_timestamp();
    ELSE
        SELECT COALESCE(max(version.version_number), 0) + 1
        INTO configuration_version_number
        FROM ascendany.configuration_versions AS version
        WHERE version.configuration_item_id = stored_configuration_item_id;
        IF configuration_version_number <> capability.expected_configuration_head_revision + 1 THEN
            RAISE EXCEPTION 'catalog version sequence differs from the configuration head'
                USING ERRCODE = '23514';
        END IF;
        published_at := pg_catalog.clock_timestamp();
        INSERT INTO ascendany.configuration_versions (
            configuration_item_id,
            configuration_kind,
            version_number,
            schema_id,
            document,
            document_sha256,
            credential_ref,
            created_by_account_id,
            created_by_role,
            created_by_session_id,
            created_at
        ) VALUES (
            stored_configuration_item_id,
            'knowledge_catalog',
            configuration_version_number,
            capability.catalog_schema_id,
            capability.catalog_document,
            capability.catalog_sha256,
            NULL,
            capability.authorized_account_id,
            'admin',
            capability.authorized_session_id,
            published_at
        )
        RETURNING configuration_versions.configuration_version_id
        INTO stored_configuration_version_id;

        UPDATE ascendany.configuration_items
        SET active_version_id = stored_configuration_version_id,
            head_revision = head_revision + 1,
            updated_at = published_at
        WHERE configuration_items.configuration_item_id = stored_configuration_item_id
          AND configuration_items.head_revision = capability.expected_configuration_head_revision;
        GET DIAGNOSTICS affected_rows = ROW_COUNT;
        IF affected_rows <> 1 THEN
            RAISE EXCEPTION 'catalog configuration head changed during publication'
                USING ERRCODE = '40001';
        END IF;
        configuration_head_revision := capability.expected_configuration_head_revision + 1;
        configuration_active_version_id := stored_configuration_version_id;
        configuration_mutated := true;
    END IF;

    INSERT INTO ascendany.audit_events (
        account_id,
        session_id,
        event_type,
        occurred_at,
        payload
    ) VALUES (
        capability.authorized_account_id,
        capability.authorized_session_id,
        CASE WHEN configuration_mutated
            THEN 'admin.configuration_version_created'
            ELSE 'admin.knowledge_catalog_release_bound'
        END,
        published_at,
        jsonb_build_object(
            'authorizationId', capability.public_id::text,
            'configurationId', configuration_public_id::text,
            'key', 'recommendation.catalog.active',
            'kind', 'knowledge_catalog',
            'versionNumber', configuration_version_number,
            'schemaId', capability.catalog_schema_id,
            'documentSha256', capability.catalog_sha256,
            'headRevision', configuration_head_revision,
            'credentialRef', NULL,
            'analyticsGenerationId', capability.expected_analytics_generation_id::text,
            'analyticsHeadRevision', capability.expected_analytics_head_revision,
            'inputManifestSha256', capability.expected_input_manifest_sha256,
            'currentModelHeadRevision', capability.expected_current_model_head_revision,
            'currentModelArtifactSha256', capability.expected_current_model_artifact_sha256,
            'targetCatalogSha256', capability.catalog_sha256,
            'targetModelId', capability.target_model_id::text,
            'targetModelArtifactSha256', capability.target_model_artifact_sha256,
            'targetModelReleaseId', capability.target_model_release_id::text,
            'targetApplicationVersion', capability.target_application_version,
            'targetApplicationCommit', capability.target_application_commit,
            'targetApplicationBuildTime', capability.target_application_build_time,
            'expectedConfigurationHeadRevision', capability.expected_configuration_head_revision,
            'configurationMutated', configuration_mutated
        )
    )
    RETURNING audit_events.audit_event_id
    INTO stored_audit_event_id;

    INSERT INTO ascendany.knowledge_catalog_publications (
        publication_authorization_id,
        configuration_item_id,
        configuration_version_id,
        expected_configuration_head_revision,
        configuration_head_revision,
        configuration_mutated,
        catalog_sha256,
        target_model_release_id,
        target_model_id,
        target_model_artifact_sha256,
        target_application_version,
        target_application_commit,
        target_application_build_time,
        analytics_generation_id,
        analytics_head_revision,
        input_manifest_sha256,
        current_model_head_revision,
        current_model_artifact_sha256,
        published_by_account_id,
        published_by_session_id,
        published_at,
        audit_event_id
    ) VALUES (
        capability.public_id,
        stored_configuration_item_id,
        stored_configuration_version_id,
        capability.expected_configuration_head_revision,
        configuration_head_revision,
        configuration_mutated,
        capability.catalog_sha256,
        capability.target_model_release_id,
        capability.target_model_id,
        capability.target_model_artifact_sha256,
        capability.target_application_version,
        capability.target_application_commit,
        capability.target_application_build_time,
        capability.expected_analytics_generation_id,
        capability.expected_analytics_head_revision,
        capability.expected_input_manifest_sha256,
        capability.expected_current_model_head_revision,
        capability.expected_current_model_artifact_sha256,
        capability.authorized_account_id,
        capability.authorized_session_id,
        published_at,
        stored_audit_event_id
    )
    RETURNING knowledge_catalog_publications.knowledge_catalog_publication_id
    INTO stored_publication_id;

    UPDATE ascendany.recommendation_model_head AS head
    SET pending_catalog_publication_id = stored_publication_id
    FROM ascendany.recommendation_model_releases AS current_release
    WHERE head.singleton
      AND head.current_release_id = current_release.recommendation_model_release_id
      AND head.current_release_id = current_model_release_id
      AND head.head_revision = capability.expected_current_model_head_revision
      AND current_release.artifact_sha256 = capability.expected_current_model_artifact_sha256
      AND head.pending_catalog_publication_id IS NULL;
    GET DIAGNOSTICS affected_rows = ROW_COUNT;
    IF affected_rows <> 1 THEN
        RAISE EXCEPTION 'current model head changed during catalog publication'
            USING ERRCODE = '40001';
    END IF;

    UPDATE ascendany.knowledge_catalog_publication_authorizations
    SET consumed_publication_id = stored_publication_id,
        consumed_at = published_at
    WHERE public_id = capability.public_id
      AND consumed_publication_id IS NULL
      AND consumed_at IS NULL;
    GET DIAGNOSTICS affected_rows = ROW_COUNT;
    IF affected_rows <> 1 THEN
        RAISE EXCEPTION 'catalog publication capability consumption failed'
            USING ERRCODE = '40001';
    END IF;

    SELECT ascendany.catalog_publication_result(stored_publication_id, false)
    INTO result;
    IF result IS NULL THEN
        RAISE EXCEPTION 'catalog publication result is inconsistent'
            USING ERRCODE = '23514';
    END IF;
    RETURN result;
END
$function$;

REVOKE ALL PRIVILEGES ON FUNCTION ascendany.publish_authorized_knowledge_catalog(uuid, text, text)
FROM PUBLIC, ascendany_runtime, ascendany_backup, ascendany_catalog_publisher;

GRANT EXECUTE ON FUNCTION ascendany.publish_authorized_knowledge_catalog(uuid, text, text)
TO ascendany_catalog_publisher;

REVOKE ALL PRIVILEGES ON TABLE ascendany.knowledge_catalog_publication_authorizations
FROM ascendany_runtime, ascendany_backup, ascendany_catalog_publisher, PUBLIC;

GRANT SELECT, INSERT ON TABLE ascendany.knowledge_catalog_publication_authorizations
TO ascendany_runtime;

GRANT SELECT ON TABLE ascendany.knowledge_catalog_publication_authorizations
TO ascendany_backup;

REVOKE ALL PRIVILEGES ON TABLE ascendany.knowledge_catalog_publications
FROM ascendany_runtime, ascendany_backup, ascendany_catalog_publisher, PUBLIC;

GRANT SELECT ON TABLE ascendany.knowledge_catalog_publications
TO ascendany_runtime, ascendany_backup;

GRANT UPDATE (
    pending_catalog_publication_id
) ON TABLE ascendany.recommendation_model_head
TO ascendany_runtime;

REVOKE ALL PRIVILEGES ON SEQUENCE ascendany.knowledge_catalog_publication_ids_seq
FROM ascendany_runtime, ascendany_backup, ascendany_catalog_publisher, PUBLIC;

GRANT SELECT ON SEQUENCE ascendany.knowledge_catalog_publication_ids_seq
TO ascendany_backup;
