SET LOCAL search_path = ascendany, pg_catalog;

DO $preflight$
BEGIN
    IF current_database() <> 'ascendany_v2' THEN
        RAISE EXCEPTION 'recommendation catalog migration requires database ascendany_v2';
    END IF;
    IF current_user <> 'ascendany_owner' THEN
        RAISE EXCEPTION 'recommendation catalog migration requires current role ascendany_owner';
    END IF;
    IF NOT EXISTS (
        SELECT 1
        FROM ascendany.schema_migrations_v2
        WHERE version = 2
          AND name = 'product_domains'
    ) THEN
        RAISE EXCEPTION 'recommendation catalog migration requires schema version 2';
    END IF;
    IF to_regclass('ascendany.configuration_items') IS NULL
       OR to_regclass('ascendany.configuration_versions') IS NULL THEN
        RAISE EXCEPTION 'recommendation catalog migration requires configuration storage';
    END IF;
END
$preflight$;

ALTER TABLE ascendany.configuration_versions
ADD CONSTRAINT configuration_versions_recommendation_catalog_contract CHECK (
    configuration_kind <> 'knowledge_catalog'
    OR (
        schema_id = 'ascendany.knowledge_catalog.recommendation.v1'
        AND credential_ref IS NULL
    )
);

ALTER TABLE ascendany.configuration_items
ADD CONSTRAINT configuration_items_recommendation_catalog_identity CHECK (
    (configuration_kind = 'knowledge_catalog')
    = (configuration_key = 'recommendation.catalog.active')
);

CREATE INDEX recommendation_knowledge_catalog_digest_idx
ON ascendany.configuration_versions (
    document_sha256,
    configuration_version_id
)
WHERE configuration_kind = 'knowledge_catalog';
