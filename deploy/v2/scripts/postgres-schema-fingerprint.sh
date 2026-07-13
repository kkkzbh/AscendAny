#!/usr/bin/bash -p
set -Eeuo pipefail

export LC_ALL=C

# This digest is generated from the canonical PostgreSQL 17 catalog stream
# emitted below after all embedded schema-v7 migrations have committed.
readonly EXPECTED_SCHEMA_SHA256='92b84cb8f870fa921ead412dd1f6fbd76a5ba9f32a8e19022f15993f0e348d65'

usage() {
  /usr/bin/printf '%s\n' \
    'Usage: postgres-schema-fingerprint.sh --emit-sql|--expected-sha256|--verify-sha256 SHA256' >&2
}

if [[ "$#" == 0 ]]; then
  usage
  exit 2
fi

case "$1" in
  --expected-sha256)
    [[ "$#" == 1 ]] || { usage; exit 2; }
    /usr/bin/printf '%s\n' "$EXPECTED_SCHEMA_SHA256"
    ;;
  --verify-sha256)
    [[ "$#" == 2 ]] || { usage; exit 2; }
    [[ "$2" =~ ^[0-9a-f]{64}$ && "$2" == "$EXPECTED_SCHEMA_SHA256" ]]
    ;;
  --emit-sql)
    [[ "$#" == 1 ]] || { usage; exit 2; }
    /usr/bin/cat <<'SQL'
SET search_path = pg_catalog;

SELECT 'contract|' || jsonb_build_object(
    'schema', 'ascendany.postgresql17.schema-fingerprint.v1',
    'postgresMajor', current_setting('server_version_num')::integer / 10000,
    'searchPath', current_setting('search_path')
)::text;

SELECT 'column|' || jsonb_build_object(
    'relation', format('%I.%I', namespace.nspname, relation.relname),
    'relationKind', relation.relkind,
    'position', attribute.attnum,
    'name', attribute.attname,
    'type', format_type(attribute.atttypid, attribute.atttypmod),
    'notNull', attribute.attnotnull,
    'default', pg_get_expr(default_value.adbin, default_value.adrelid, true),
    'identity', attribute.attidentity,
    'generated', attribute.attgenerated,
    'collation', CASE
        WHEN collation_row.oid IS NULL THEN NULL
        ELSE format('%I.%I', collation_namespace.nspname, collation_row.collname)
    END
)::text
FROM pg_attribute AS attribute
JOIN pg_class AS relation
  ON relation.oid = attribute.attrelid
JOIN pg_namespace AS namespace
  ON namespace.oid = relation.relnamespace
LEFT JOIN pg_attrdef AS default_value
  ON default_value.adrelid = attribute.attrelid
 AND default_value.adnum = attribute.attnum
LEFT JOIN pg_collation AS collation_row
  ON collation_row.oid = attribute.attcollation
 AND attribute.attcollation <> 0
LEFT JOIN pg_namespace AS collation_namespace
  ON collation_namespace.oid = collation_row.collnamespace
WHERE namespace.nspname = 'ascendany'
  AND relation.relkind IN ('r', 'p')
  AND attribute.attnum > 0
  AND NOT attribute.attisdropped;

SELECT 'constraint|' || jsonb_build_object(
    'namespace', namespace.nspname,
    'relation', CASE
        WHEN relation.oid IS NULL THEN NULL
        ELSE format('%I.%I', relation_namespace.nspname, relation.relname)
    END,
    'name', constraint_row.conname,
    'type', constraint_row.contype,
    'deferrable', constraint_row.condeferrable,
    'initiallyDeferred', constraint_row.condeferred,
    'validated', constraint_row.convalidated,
    'noInherit', constraint_row.connoinherit,
    'definition', pg_get_constraintdef(constraint_row.oid, true)
)::text
FROM pg_constraint AS constraint_row
JOIN pg_namespace AS namespace
  ON namespace.oid = constraint_row.connamespace
LEFT JOIN pg_class AS relation
  ON relation.oid = constraint_row.conrelid
LEFT JOIN pg_namespace AS relation_namespace
  ON relation_namespace.oid = relation.relnamespace
WHERE namespace.nspname = 'ascendany';

SELECT 'index|' || jsonb_build_object(
    'relation', format('%I.%I', table_namespace.nspname, table_relation.relname),
    'name', format('%I.%I', index_namespace.nspname, index_relation.relname),
    'accessMethod', access_method.amname,
    'unique', index_row.indisunique,
    'primary', index_row.indisprimary,
    'exclusion', index_row.indisexclusion,
    'immediate', index_row.indimmediate,
    'valid', index_row.indisvalid,
    'ready', index_row.indisready,
    'live', index_row.indislive,
    'replicaIdentity', index_row.indisreplident,
    'clustered', index_row.indisclustered,
    'nullsNotDistinct', index_row.indnullsnotdistinct,
    'options', COALESCE(to_jsonb(index_relation.reloptions), '[]'::jsonb),
    'definition', pg_get_indexdef(index_row.indexrelid, 0, true)
)::text
FROM pg_index AS index_row
JOIN pg_class AS table_relation
  ON table_relation.oid = index_row.indrelid
JOIN pg_namespace AS table_namespace
  ON table_namespace.oid = table_relation.relnamespace
JOIN pg_class AS index_relation
  ON index_relation.oid = index_row.indexrelid
JOIN pg_namespace AS index_namespace
  ON index_namespace.oid = index_relation.relnamespace
JOIN pg_am AS access_method
  ON access_method.oid = index_relation.relam
WHERE table_namespace.nspname = 'ascendany';

SELECT 'trigger|' || jsonb_build_object(
    'relation', format('%I.%I', namespace.nspname, relation.relname),
    'name', trigger_row.tgname,
    'enabled', trigger_row.tgenabled,
    'typeMask', trigger_row.tgtype,
    'definition', pg_get_triggerdef(trigger_row.oid, true)
)::text
FROM pg_trigger AS trigger_row
JOIN pg_class AS relation
  ON relation.oid = trigger_row.tgrelid
JOIN pg_namespace AS namespace
  ON namespace.oid = relation.relnamespace
WHERE namespace.nspname = 'ascendany'
  AND NOT trigger_row.tgisinternal;

SELECT 'routine|' || jsonb_build_object(
    'name', format('%I.%I', namespace.nspname, routine.proname),
    'kind', routine.prokind,
    'identityArguments', pg_get_function_identity_arguments(routine.oid),
    'arguments', pg_get_function_arguments(routine.oid),
    'result', pg_get_function_result(routine.oid),
    'language', language.lanname,
    'volatility', routine.provolatile,
    'strict', routine.proisstrict,
    'securityDefiner', routine.prosecdef,
    'leakproof', routine.proleakproof,
    'parallel', routine.proparallel,
    'returnsSet', routine.proretset,
    'cost', routine.procost,
    'rows', routine.prorows,
    'configuration', COALESCE(
        (SELECT jsonb_agg(setting ORDER BY setting)
         FROM unnest(routine.proconfig) AS setting),
        '[]'::jsonb
    ),
    'definition', pg_get_functiondef(routine.oid)
)::text
FROM pg_proc AS routine
JOIN pg_namespace AS namespace
  ON namespace.oid = routine.pronamespace
JOIN pg_language AS language
  ON language.oid = routine.prolang
WHERE namespace.nspname = 'ascendany'
  AND routine.prokind IN ('f', 'p');
SQL
    ;;
  *)
    usage
    exit 2
    ;;
esac
