#!/usr/bin/env bash
set -Eeuo pipefail

export LC_ALL=C
umask 077

readonly REPOSITORY_ROOT="$(cd -- "$(dirname -- "$0")/../.." && pwd -P)"
readonly VALIDATOR="$REPOSITORY_ROOT/deploy/v2/scripts/validate-production.sh"
readonly WORK_ROOT="$(mktemp -d /tmp/ascendany-catalog-validator.XXXXXX)"
trap 'rm -rf -- "$WORK_ROOT"' EXIT

(
  # shellcheck source=../../deploy/v2/scripts/validate-production.sh
  source "$VALIDATOR"
  trap - EXIT
  trap 'printf "catalog fixture failed at line %s\n" "$LINENO" >&2' ERR
  pass() { :; }
  fail() { failures=$((failures + 1)); }

  release_root="$WORK_ROOT/release"
  catalog_publisher_state_root="$WORK_ROOT/catalog-publisher"
  catalog_receipt_root="$catalog_publisher_state_root/receipts"
  install -d -m 0750 "$catalog_receipt_root"
  install -d -m 0755 "$release_root/models"

  readonly TARGET_MODEL_ID=11111111-1111-4111-8111-111111111111
  readonly HISTORICAL_MODEL_ID=aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa
  readonly CONFIGURATION_ID=22222222-2222-4222-8222-222222222222
  readonly ACCOUNT_ID=33333333-3333-4333-8333-333333333333
  readonly SESSION_ID=44444444-4444-4444-8444-444444444444
  readonly INPUT_MANIFEST_SHA256="$(printf 'a%.0s' {1..64})"
  readonly TARGET_MODEL_SHA256="$(printf 'b%.0s' {1..64})"
  readonly PRIOR_MODEL_SHA256="$(printf 'c%.0s' {1..64})"
  readonly HISTORICAL_MODEL_SHA256="$(printf 'd%.0s' {1..64})"
  readonly HISTORICAL_CATALOG_SHA256="$(printf 'e%.0s' {1..64})"
  readonly TARGET_VERSION=0.2.1
  readonly TARGET_COMMIT="$(printf '1%.0s' {1..40})"
  readonly TARGET_BUILD_TIME=2026-07-13T01:02:03Z
  readonly HISTORICAL_VERSION=0.2.0
  readonly HISTORICAL_COMMIT="$(printf '0%.0s' {1..40})"
  readonly HISTORICAL_BUILD_TIME=2026-07-12T01:02:03Z

  printf '%s' '{"knowledgePoints":[],"problemAssignments":[]}' \
    >"$release_root/models/recommendation-knowledge-catalog.json"
  printf '%s' '{"manifest":{"modelId":"11111111-1111-4111-8111-111111111111"}}' \
    >"$release_root/models/recommendation-model.json"
  release_catalog_sha256="$(sha256sum "$release_root/models/recommendation-knowledge-catalog.json" | awk '{print $1}')"
  release_model_sha256="$TARGET_MODEL_SHA256"
  release_manifest_version="$TARGET_VERSION"
  release_manifest_commit="$TARGET_COMMIT"
  release_manifest_build_time="$TARGET_BUILD_TIME"

  write_receipt() {
    local publication_id="$1"
    local expected_head="$2"
    local head="$3"
    local configuration_mutated="$4"
    local catalog_sha="$5"
    local target_model_sha="$6"
    local target_model_id="$7"
    local current_revision="$8"
    local current_model_sha="$9"
    shift 9
    local target_version="$1"
    local target_commit="$2"
    local target_build_time="$3"
    local target_model_release_id="${4:-$publication_id}"
    local authorization_id
    printf -v authorization_id '77777777-7777-4777-8777-%012d' "$publication_id"
    jq -jScn \
      --arg publicationId "$publication_id" \
      --arg authorizationId "$authorization_id" \
      --arg catalogSha "$catalog_sha" \
      --arg modelSha "$target_model_sha" \
      --arg modelId "$target_model_id" \
      --arg configurationId "$CONFIGURATION_ID" \
      --arg inputManifestSha "$INPUT_MANIFEST_SHA256" \
      --arg currentModelSha "$current_model_sha" \
      --arg accountId "$ACCOUNT_ID" \
      --arg sessionId "$SESSION_ID" \
      --arg targetVersion "$target_version" \
      --arg targetCommit "$target_commit" \
      --arg targetBuildTime "$target_build_time" \
      --argjson expectedHead "$expected_head" \
      --argjson head "$head" \
      --argjson currentRevision "$current_revision" \
      --arg targetModelReleaseId "$target_model_release_id" \
      --argjson configurationMutated "$configuration_mutated" '
        {
          schema: "ascendany.knowledge_catalog.publication-receipt.v1",
          authorizationId: $authorizationId,
          knowledgeCatalogPublicationId: $publicationId,
          targetModelReleaseId: $targetModelReleaseId,
          catalogSha256: $catalogSha,
          modelArtifactSha256: $modelSha,
          modelId: $modelId,
          targetApplicationVersion: $targetVersion,
          targetApplicationCommit: $targetCommit,
          targetApplicationBuildTime: $targetBuildTime,
          configurationKey: "recommendation.catalog.active",
          configurationId: $configurationId,
          expectedConfigurationHeadRevision: $expectedHead,
          configurationHeadRevision: $head,
          configurationVersionId: "1",
          configurationVersionNumber: 1,
          analyticsGenerationId: "7",
          analyticsHeadRevision: 3,
          inputManifestSha256: $inputManifestSha,
          currentModelHeadRevision: $currentRevision,
          currentModelArtifactSha256: $currentModelSha,
          publishedByAccountId: $accountId,
          publishedBySessionId: $sessionId,
          publishedAt: "2026-07-13T09:10:11.123Z",
          auditEventId: "9",
          configurationMutated: $configurationMutated
        }
      ' >"$catalog_receipt_root/$publication_id.json"
  }

  write_target_receipt() {
    write_receipt 1 0 1 true "$release_catalog_sha256" "$TARGET_MODEL_SHA256" \
      "$TARGET_MODEL_ID" 1 "$TARGET_MODEL_SHA256" \
      "$TARGET_VERSION" "$TARGET_COMMIT" "$TARGET_BUILD_TIME"
  }

  fixture_root_owner=ascendany-catalog-publisher:ascendany-catalog-readers
  fixture_file_owner=ascendany-catalog-publisher:ascendany-catalog-readers
  stat() {
    local format="$2"
    local path="${!#}"
    if [[ "$format" == '%U:%G:%a' && "$path" == "$catalog_receipt_root" ]]; then
      printf '%s\n' "$fixture_root_owner:750"
      return
    fi
    if [[ "$format" == '%U:%G:%a:%h' && "$path" == "$catalog_receipt_root/"*.json ]]; then
      printf '%s\n' "$fixture_file_owner:640:1"
      return
    fi
    /usr/bin/stat -Lc "$format" -- "$path"
  }

  fixture_database_match=1
  fixture_database_ids=1
  fixture_expected_prior_revision=1
  fixture_expected_prior_sha="$TARGET_MODEL_SHA256"
  fixture_target_state="1|1|1|1|$TARGET_MODEL_SHA256|1|0|0"
  fixture_activation_state='1|1|1|1|1|0|1|1|0'
  run_runtime_psql() {
    local arguments="$*"
    local sql=''
    if [[ "$arguments" != *ascendany-validator:* ]]; then
      sql="$(cat)"
      arguments+=" $sql"
    fi
    case "$arguments" in
      *ascendany-validator:catalog-publication-receipt*)
        if [[ "$arguments" == *'knowledge_catalog_publication_authorizations'* &&
              "$arguments" == *'consumed_publication_id = publication.knowledge_catalog_publication_id'* &&
              "$arguments" == *'consumed_at = publication.published_at'* &&
              "$arguments" == *'-v publication_authorization_id='* ]]; then
          printf '%s\n' "$fixture_database_match"
        else
          return 1
        fi
        ;;
      *ascendany-validator:catalog-publication-ids*)
        printf '%s\n' "$fixture_database_ids"
        ;;
      *ascendany-validator:catalog-publication-target*)
        if [[ " $arguments " == *" -v expected_prior_revision=$fixture_expected_prior_revision "* &&
              " $arguments " == *" -v expected_prior_sha=$fixture_expected_prior_sha "* ]]; then
          printf '%s\n' "$fixture_target_state"
        else
          return 1
        fi
        ;;
      *ascendany-validator:catalog-publication-activation-state*)
        printf '%s\n' "$fixture_activation_state"
        ;;
      *)
        return 1
        ;;
    esac
  }

  reset_initial_fixture() {
    rm -f -- "$catalog_receipt_root"/*
    deployment_transition=initial
    validation_phase=catalog
    observed_forward_model_head_revision=1
    observed_forward_model_artifact_sha256="$TARGET_MODEL_SHA256"
    expected_forward_model_head_revision=''
    expected_forward_model_artifact_sha256=''
    fixture_database_match=1
    fixture_database_ids=1
    fixture_expected_prior_revision=1
    fixture_expected_prior_sha="$TARGET_MODEL_SHA256"
    fixture_target_state="1|1|1|1|$TARGET_MODEL_SHA256|1|0|0"
    fixture_activation_state='1|1|1|1|1|1|0|1|1|0'
    fixture_root_owner=ascendany-catalog-publisher:ascendany-catalog-readers
    fixture_file_owner=ascendany-catalog-publisher:ascendany-catalog-readers
    write_target_receipt
  }

  reset_initial_fixture
  failures=0
  check_catalog_publication_binding
  [[ "$failures" == 0 ]]

  reset_initial_fixture
  validation_phase=production
  observed_forward_model_head_revision=2
  fixture_target_state="1|1|1|1|$TARGET_MODEL_SHA256|1|1|2"
  fixture_activation_state='2||2|1|2|2|0|1|1|0'
  failures=0
  check_catalog_publication_binding
  [[ "$failures" == 0 ]]

  rm -f -- "$catalog_receipt_root"/*
  write_receipt 1 0 1 true "$release_catalog_sha256" "$TARGET_MODEL_SHA256" \
    "$TARGET_MODEL_ID" 1 "$TARGET_MODEL_SHA256" \
    "$HISTORICAL_VERSION" "$HISTORICAL_COMMIT" "$HISTORICAL_BUILD_TIME"
  write_receipt 2 1 1 false "$release_catalog_sha256" "$TARGET_MODEL_SHA256" \
    "$TARGET_MODEL_ID" 2 "$TARGET_MODEL_SHA256" \
    "$TARGET_VERSION" "$TARGET_COMMIT" "$TARGET_BUILD_TIME" 1
  deployment_transition=initial
  validation_phase=production
  observed_forward_model_head_revision=3
  observed_forward_model_artifact_sha256="$TARGET_MODEL_SHA256"
  fixture_expected_prior_revision=2
  fixture_expected_prior_sha="$TARGET_MODEL_SHA256"
  fixture_database_ids=$'1\n2'
  fixture_target_state="1|2|1|2|$TARGET_MODEL_SHA256|1|1|3"
  fixture_activation_state='3||3|1|3|3|0|1|2|0'
  failures=0
  check_catalog_publication_binding
  [[ "$failures" -ge 1 ]]

  reset_initial_fixture
  printf '\n' >>"$catalog_receipt_root/1.json"
  failures=0
  check_catalog_publication_binding
  [[ "$failures" -ge 1 ]]

  reset_initial_fixture
  jq -jSc '.idempotent = false' \
    "$catalog_receipt_root/1.json" >"$WORK_ROOT/legacy-field.json"
  mv "$WORK_ROOT/legacy-field.json" "$catalog_receipt_root/1.json"
  failures=0
  check_catalog_publication_binding
  [[ "$failures" -ge 1 ]]

  reset_initial_fixture
  jq -jSc '.authorizationId = "77777777-7777-4777-7777-000000000001"' \
    "$catalog_receipt_root/1.json" >"$WORK_ROOT/noncanonical-authorization.json"
  mv "$WORK_ROOT/noncanonical-authorization.json" "$catalog_receipt_root/1.json"
  failures=0
  check_catalog_publication_binding
  [[ "$failures" -ge 1 ]]

  reset_initial_fixture
  jq -jSc '.modelArtifactSha256 = "'"$HISTORICAL_MODEL_SHA256"'"' \
    "$catalog_receipt_root/1.json" >"$WORK_ROOT/wrong-sha.json"
  mv "$WORK_ROOT/wrong-sha.json" "$catalog_receipt_root/1.json"
  failures=0
  check_catalog_publication_binding
  [[ "$failures" -ge 1 ]]

  reset_initial_fixture
  jq -jSc '.targetModelReleaseId = "9"' \
    "$catalog_receipt_root/1.json" >"$WORK_ROOT/wrong-release-id.json"
  mv "$WORK_ROOT/wrong-release-id.json" "$catalog_receipt_root/1.json"
  failures=0
  check_catalog_publication_binding
  [[ "$failures" -ge 1 ]]

  reset_initial_fixture
  fixture_database_ids=2
  failures=0
  check_catalog_publication_binding
  [[ "$failures" -ge 1 ]]

  reset_initial_fixture
  fixture_database_match=0
  failures=0
  check_catalog_publication_binding
  [[ "$failures" -ge 1 ]]

  reset_initial_fixture
  fixture_file_owner=ascendany:ascendany
  failures=0
  check_catalog_publication_binding
  [[ "$failures" -ge 1 ]]

  reset_initial_fixture
  mv "$catalog_receipt_root/1.json" "$catalog_receipt_root/01.json"
  failures=0
  check_catalog_publication_binding
  [[ "$failures" -ge 1 ]]

  reset_initial_fixture
  fixture_activation_state='1|1|1|1|1|1|0|1|1|1'
  failures=0
  check_catalog_publication_binding
  [[ "$failures" -ge 1 ]]

  rm -f -- "$catalog_receipt_root"/*
  write_receipt 1 0 1 true "$HISTORICAL_CATALOG_SHA256" "$HISTORICAL_MODEL_SHA256" \
    "$HISTORICAL_MODEL_ID" 1 "$HISTORICAL_MODEL_SHA256" \
    "$HISTORICAL_VERSION" "$HISTORICAL_COMMIT" "$HISTORICAL_BUILD_TIME"
  write_receipt 2 1 1 false "$release_catalog_sha256" "$TARGET_MODEL_SHA256" \
    "$TARGET_MODEL_ID" 2 "$PRIOR_MODEL_SHA256" \
    "$TARGET_VERSION" "$TARGET_COMMIT" "$TARGET_BUILD_TIME"
  deployment_transition=forward
  validation_phase=catalog
  expected_forward_model_head_revision=2
  expected_forward_model_artifact_sha256="$PRIOR_MODEL_SHA256"
  fixture_expected_prior_revision=2
  fixture_expected_prior_sha="$PRIOR_MODEL_SHA256"
  fixture_database_ids=$'1\n2'
  fixture_target_state="1|2|2|2|$PRIOR_MODEL_SHA256|1|0|0"
  fixture_activation_state='2|2|2|1|2|2|0|1|2|0'
  failures=0
  check_catalog_publication_binding
  [[ "$failures" == 0 ]]
)

printf 'production catalog publication validator fixtures: PASS\n'
