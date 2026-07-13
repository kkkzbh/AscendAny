#!/usr/bin/env bash
set -Eeuo pipefail

export LC_ALL=C
umask 077

readonly REPOSITORY_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)"
readonly VALIDATOR="$REPOSITORY_ROOT/deploy/v2/scripts/validate-production.sh"
readonly MODEL_FIXTURE="$REPOSITORY_ROOT/contracts/recommendation/fixtures/synthetic-test-only.inference-model.v1.json"
readonly WORK_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/ascendany-validator-state-fixture.XXXXXX")"
trap 'rm -rf -- "$WORK_ROOT"' EXIT

(
  # shellcheck source=../../deploy/v2/scripts/validate-production.sh
  source "$VALIDATOR"
  trap - EXIT
  trap 'printf "state fixture failed at line %s\n" "$LINENO" >&2' ERR
  pass() { :; }
  fail() { failures=$((failures + 1)); }

  declare -A fixture_enabled=(
    [ascendany-api.service]=masked
    [pgbouncer.service]=masked
  )
  declare -A fixture_active=(
    [ascendany-api.service]=inactive
    [pgbouncer.service]=inactive
  )
  declare -A fixture_main_pid=(
    [ascendany-api.service]=0
    [pgbouncer.service]=0
  )
  fixture_retired_listener=''

  systemctl() {
    local operation="$1" unit="$2"
    case "$operation" in
      is-enabled) printf '%s\n' "${fixture_enabled[$unit]}" ;;
      is-active) printf '%s\n' "${fixture_active[$unit]}" ;;
      *) return 1 ;;
    esac
  }
  unit_property() {
    local unit="$1" property="$2"
    [[ "$property" == MainPID ]]
    printf '%s\n' "${fixture_main_pid[$unit]}"
  }
  ss() {
    [[ "$*" == '-H -ltn sport = :8000' ]]
    [[ -z "$fixture_retired_listener" ]] || printf '%s\n' "$fixture_retired_listener"
  }
  failures=0
  check_retired_runtime_boundary
  [[ "$failures" == 0 ]]

  fixture_enabled[ascendany-api.service]=disabled
  failures=0
  check_retired_runtime_boundary
  [[ "$failures" == 1 ]]
  fixture_enabled[ascendany-api.service]=masked

  fixture_active[ascendany-api.service]=active
  failures=0
  check_retired_runtime_boundary
  [[ "$failures" == 1 ]]
  fixture_active[ascendany-api.service]=inactive

  fixture_main_pid[ascendany-api.service]=4242
  failures=0
  check_retired_runtime_boundary
  [[ "$failures" == 1 ]]
  fixture_main_pid[ascendany-api.service]=0

  fixture_retired_listener='LISTEN 0 4096 127.0.0.1:8000 0.0.0.0:*'
  failures=0
  check_retired_runtime_boundary
  [[ "$failures" == 1 ]]
  fixture_retired_listener=''

  failures=0
  check_pgbouncer_service_ownership
  [[ "$failures" == 0 ]]

  fixture_enabled[pgbouncer.service]=disabled
  failures=0
  check_pgbouncer_service_ownership
  [[ "$failures" == 1 ]]
  fixture_enabled[pgbouncer.service]=masked

  fixture_active[pgbouncer.service]=active
  fixture_main_pid[pgbouncer.service]=4242
  failures=0
  check_pgbouncer_service_ownership
  [[ "$failures" == 1 ]]
  fixture_active[pgbouncer.service]=inactive
  fixture_main_pid[pgbouncer.service]=0

  release_root="$WORK_ROOT/release"
  install -d -m 0755 "$release_root/bin" "$release_root/models"
  printf '%s\n' '#!/usr/bin/bash' 'exit 0' >"$release_root/bin/ascendany-model"
  chmod 0755 "$release_root/bin/ascendany-model"
  install -m 0644 "$MODEL_FIXTURE" "$release_root/models/recommendation-model.json"
  jq -jSc '.manifest.purpose = "production"' \
    "$release_root/models/recommendation-model.json" >"$release_root/models/recommendation-model.production.json"
  mv -- "$release_root/models/recommendation-model.production.json" "$release_root/models/recommendation-model.json"
  chmod 0644 "$release_root/models/recommendation-model.json"
  release_manifest_commit="$(printf 'b%.0s' {1..40})"
  release_manifest_version="2.0.0"
  release_manifest_build_time="2026-07-13T08:00:00Z"
  release_manifest_purpose="production"
  release_model_sha256="$(sha256sum "$release_root/models/recommendation-model.json" | awk '{print $1}')"
  model_size="$(stat -Lc '%s' "$release_root/models/recommendation-model.json")"
  model_manifest_sha256="$(jq -jSc '{
      schema: .schema,
      modelId: .manifest.modelId,
      purpose: .manifest.purpose,
      trainedAt: .manifest.trainedAt,
      algorithm: .manifest.algorithm,
      inferenceContract: .manifest.inferenceContract,
      trainingProvenanceSha256: .manifest.trainingProvenanceSha256,
      featureSchemaSha256: .manifest.featureSchemaSha256,
      knowledgeCatalogSha256: .manifest.knowledgeCatalogSha256,
      parameterSha256: .manifest.parameterSha256,
      goldenVectorsSha256: .manifest.goldenVectorsSha256,
      actorFeatureIds: .manifest.actorFeatureIds,
      problemFeatureIds: .manifest.problemFeatureIds,
      knowledgePointIds: .manifest.knowledgePointIds
    }' "$release_root/models/recommendation-model.json" | sha256sum | awk '{print $1}')"
  backup_id="backup-20260713T081000Z-0123456789abcdef"
  backup_manifest="$WORK_ROOT/manifest.json"
  restore_evidence_fixture="$WORK_ROOT/restore-evidence.json"

  jq -n \
    --slurpfile model "$release_root/models/recommendation-model.json" \
    --arg backupId "$backup_id" \
    --arg artifactSHA256 "$release_model_sha256" \
    --argjson artifactSizeBytes "$model_size" \
    --arg modelManifestSHA256 "$model_manifest_sha256" \
    --arg applicationVersion "$release_manifest_version" \
    --arg applicationCommit "$release_manifest_commit" \
    --arg applicationBuildTime "$release_manifest_build_time" '
      $model[0] as $installed |
      ($installed | {
        schema: .schema,
        modelId: .manifest.modelId,
        purpose: .manifest.purpose,
        trainedAt: .manifest.trainedAt,
        algorithm: .manifest.algorithm,
        inferenceContract: .manifest.inferenceContract,
        trainingProvenanceSha256: .manifest.trainingProvenanceSha256,
        featureSchemaSha256: .manifest.featureSchemaSha256,
        knowledgeCatalogSha256: .manifest.knowledgeCatalogSha256,
        parameterSha256: .manifest.parameterSha256,
        goldenVectorsSha256: .manifest.goldenVectorsSha256,
        actorFeatureIds: .manifest.actorFeatureIds,
        problemFeatureIds: .manifest.problemFeatureIds,
        knowledgePointIds: .manifest.knowledgePointIds
      }) as $manifest |
      {
        schema: "ascendany.backup.bundle.v2",
        backupId: $backupId,
        createdAt: "2026-07-13T08:10:00Z",
        database: {
          databaseName: "ascendany_v2",
          file: {filename: "database.dump", format: "postgresql-custom", sha256: ("1" * 64), sizeBytes: 1},
          migrations: [
            {version: 1, name: "one", sha256: ("1" * 64)},
            {version: 2, name: "two", sha256: ("2" * 64)},
            {version: 3, name: "three", sha256: ("3" * 64)},
            {version: 4, name: "four", sha256: ("4" * 64)},
            {version: 5, name: "five", sha256: ("5" * 64)},
            {version: 6, name: "six", sha256: ("6" * 64)}
          ],
          recommendationModel: {
            releaseId: 1,
            headRevision: 2,
            modelId: $installed.manifest.modelId,
            modelPurpose: $installed.manifest.purpose,
            artifactSha256: $artifactSHA256,
            artifactSizeBytes: $artifactSizeBytes,
            artifactMode: 420,
            modelSchema: $installed.schema,
            algorithm: $installed.manifest.algorithm,
            inferenceContract: $installed.manifest.inferenceContract,
            trainedAt: $installed.manifest.trainedAt,
            trainingProvenanceSha256: $installed.manifest.trainingProvenanceSha256,
            featureSchemaSha256: $installed.manifest.featureSchemaSha256,
            knowledgeCatalogSha256: $installed.manifest.knowledgeCatalogSha256,
            parameterSha256: $installed.manifest.parameterSha256,
            goldenVectorsSha256: $installed.manifest.goldenVectorsSha256,
            manifest: $manifest,
            manifestSha256: $modelManifestSHA256,
            releaseCreatedAt: "2026-07-13T08:01:00Z",
            applicationVersion: $applicationVersion,
            applicationCommit: $applicationCommit,
            applicationBuildTime: $applicationBuildTime,
            activatedAt: "2026-07-13T08:02:00Z",
            headUpdatedAt: "2026-07-13T08:02:00Z"
          }
        },
        artifacts: {
          file: {filename: "artifacts.tar.zst", format: "tar+zstd", sha256: ("2" * 64), sizeBytes: 1},
          count: 0,
          totalBytes: 0,
          entries: []
        }
      }
    ' >"$backup_manifest"

  write_evidence() {
    local manifest_path="$1" output_path="$2" manifest_sha256
    manifest_sha256="$(sha256sum "$manifest_path" | awk '{print $1}')"
    jq -n \
      --slurpfile backup "$manifest_path" \
      --arg manifestSHA256 "$manifest_sha256" \
      --arg releaseCommit "$release_manifest_commit" \
      --arg releaseVersion "$release_manifest_version" '
        $backup[0] as $bundle |
        $bundle.database.recommendationModel as $model |
        {
          level: "INFO",
          msg: "backup restore verified",
          backupId: $bundle.backupId,
          manifestSHA256: $manifestSHA256,
          artifactCount: $bundle.artifacts.count,
          databaseName: "ascendany_v2_restore_verify",
          releaseCommit: $releaseCommit,
          releaseVersion: $releaseVersion,
          modelId: $model.modelId,
          modelPurpose: $model.modelPurpose,
          modelArtifactSHA256: $model.artifactSha256,
          modelHeadRevision: $model.headRevision,
          modelApplicationVersion: $model.applicationVersion,
          modelApplicationCommit: $model.applicationCommit,
          modelApplicationBuildTime: $model.applicationBuildTime,
          modelFeatureSchemaSHA256: $model.featureSchemaSha256,
          modelKnowledgeCatalogSHA256: $model.knowledgeCatalogSha256,
          modelManifestSHA256: $model.manifestSha256,
          time: "2026-07-13T08:20:00Z"
        }
      ' >"$output_path"
  }

  write_evidence "$backup_manifest" "$restore_evidence_fixture"
  failures=0
  check_backup_model_provenance "$backup_id" "$backup_manifest" "$restore_evidence_fixture"
  [[ "$failures" == 0 ]]

  jq '.schema = "ascendany.backup.bundle.v1"' "$backup_manifest" >"$WORK_ROOT/manifest-v1.json"
  write_evidence "$WORK_ROOT/manifest-v1.json" "$restore_evidence_fixture"
  failures=0
  check_backup_model_provenance "$backup_id" "$WORK_ROOT/manifest-v1.json" "$restore_evidence_fixture" || true
  [[ "$failures" == 1 ]]

  jq '.database.recommendationModel.trainingProvenanceSha256 = ("f" * 64)' \
    "$backup_manifest" >"$WORK_ROOT/manifest-provenance-drift.json"
  write_evidence "$WORK_ROOT/manifest-provenance-drift.json" "$restore_evidence_fixture"
  failures=0
  check_backup_model_provenance "$backup_id" "$WORK_ROOT/manifest-provenance-drift.json" "$restore_evidence_fixture" || true
  [[ "$failures" == 1 ]]

  write_evidence "$backup_manifest" "$restore_evidence_fixture"
  jq '.modelHeadRevision += 1' "$restore_evidence_fixture" >"$WORK_ROOT/evidence-drift.json"
  failures=0
  check_backup_model_provenance "$backup_id" "$backup_manifest" "$WORK_ROOT/evidence-drift.json" || true
  [[ "$failures" == 1 ]]

  release_manifest_version="2.0.1"
  failures=0
  check_backup_model_provenance "$backup_id" "$backup_manifest" "$restore_evidence_fixture" || true
  [[ "$failures" == 1 ]]

  fixture_runtime_result="$(jq -r '
    .database.recommendationModel |
    [
      .modelId, .artifactSha256, (.headRevision | tostring), .applicationVersion,
      .applicationCommit, .applicationBuildTime, .knowledgeCatalogSha256, .manifestSha256
    ] | join("|")
  ' "$backup_manifest")"
  run_runtime_psql() { printf '%s\n' "$fixture_runtime_result"; }
  failures=0
  check_retained_backup_model_provenance "$backup_id" "$backup_manifest" "$restore_evidence_fixture"
  [[ "$failures" == 0 ]]
  fixture_runtime_result="$(jq -r '
    .database.recommendationModel |
    [
      .modelId, .artifactSha256, ((.headRevision + 1) | tostring), .applicationVersion,
      .applicationCommit, .applicationBuildTime, .knowledgeCatalogSha256, .manifestSha256
    ] | join("|")
  ' "$backup_manifest")"
  failures=0
  check_retained_backup_model_provenance "$backup_id" "$backup_manifest" "$restore_evidence_fixture" || true
  [[ "$failures" == 1 ]]

  deployment_transition=forward
  validation_phase=staged
  PGPASSFILE="$WORK_ROOT/mock-runtime.pgpass"
  fixture_runtime_result='1|1|1|1|1'
  failures=0
  check_admin_bootstrap_database
  [[ "$failures" == 0 ]]
  deployment_transition=initial
  fixture_runtime_result='0|0|0|0|0'
  failures=0
  check_admin_bootstrap_database
  [[ "$failures" == 0 ]]
  fixture_runtime_result='1|1|1|1|1'
  failures=0
  check_admin_bootstrap_database
  [[ "$failures" == 1 ]]

  model_id="$(jq -r '.manifest.modelId' "$release_root/models/recommendation-model.json")"
  catalog_sha="$(jq -r '.manifest.knowledgeCatalogSha256' "$release_root/models/recommendation-model.json")"
  old_application_commit="$(printf 'a%.0s' {1..40})"
  fixture_model_binding_row() {
    local event_version="$1" event_commit="$2" event_build_time="$3" revision="$4"
    local catalog_kind_count="${5:-1}" catalog_key_count="${6:-1}"
    local catalog_kind="${7:-knowledge_catalog}" catalog_document_sha="${8:-$catalog_sha}"
    local catalog_revision="${9:-1}"
    local catalog_key='recommendation.catalog.active'
    local catalog_schema='ascendany.knowledge_catalog.recommendation.v1'
    if [[ "$catalog_kind_count:$catalog_key_count" == 0:0 ]]; then
      catalog_key=''
      catalog_kind=''
      catalog_revision=''
      catalog_schema=''
      catalog_document_sha=''
    fi
    printf '%s\n' \
      "$model_id|$release_model_sha256|$model_size|420|ascendany.recommendation.inference-model.v1|production|knowledge_mirt_feature_v1|ascendany.recommendation.inference.v1|$catalog_sha|$revision|$release_model_sha256|$event_version|$event_commit|$event_build_time|$catalog_kind_count|$catalog_key_count|$catalog_key|$catalog_kind|$catalog_revision|$catalog_revision|$catalog_schema|$catalog_document_sha|"
  }

  deployment_transition=forward
  validation_phase=staged
  fixture_runtime_result="$(fixture_model_binding_row 2.0.0 "$old_application_commit" 2026-07-12T08:00:00Z 2)"
  failures=0
  check_recommendation_model_binding
  [[ "$failures" == 0 && "$observed_forward_model_head_revision" == 2 ]]

  validation_phase=smoke
  expected_forward_model_head_revision=2
  failures=0
  check_recommendation_model_binding
  [[ "$failures" == 0 && "$observed_forward_model_head_revision" == 2 ]]

  fixture_runtime_result="$(fixture_model_binding_row 2.0.0 "$old_application_commit" 2026-07-12T08:00:00Z 2 1 1 knowledge_catalog "$catalog_sha" 7)"
  failures=0
  check_recommendation_model_binding
  [[ "$failures" == 0 ]]

  fixture_runtime_result="$(fixture_model_binding_row 2.0.0 "$old_application_commit" 2026-07-12T08:00:00Z 2 1 1 prompt)"
  failures=0
  check_recommendation_model_binding
  [[ "$failures" == 1 ]]

  fixture_runtime_result="$(fixture_model_binding_row 2.0.0 "$old_application_commit" 2026-07-12T08:00:00Z 2 1 1 knowledge_catalog "$(printf 'f%.0s' {1..64})")"
  failures=0
  check_recommendation_model_binding
  [[ "$failures" == 1 ]]

  validation_phase=activation
  fixture_runtime_result="$(fixture_model_binding_row "$release_manifest_version" "$release_manifest_commit" "$release_manifest_build_time" 3)"
  failures=0
  check_recommendation_model_binding
  [[ "$failures" == 0 && "$observed_forward_model_head_revision" == 3 ]]
  fixture_runtime_result="$(fixture_model_binding_row "$release_manifest_version" "$release_manifest_commit" "$release_manifest_build_time" 4)"
  failures=0
  check_recommendation_model_binding
  [[ "$failures" == 1 ]]

  deployment_transition=initial
  validation_phase=activation
  fixture_runtime_result="$(fixture_model_binding_row "$release_manifest_version" "$release_manifest_commit" "$release_manifest_build_time" 1 0 0)"
  failures=0
  check_recommendation_model_binding
  [[ "$failures" == 0 ]]
  deployment_transition=forward

  fingerprint_full="$(printf '1%.0s' {1..64})"
  fingerprint_business="$(printf '2%.0s' {1..64})"
  database_fingerprint_sha256() {
    case "$1" in
      full) printf '%s\n' "$fingerprint_full" ;;
      business) printf '%s\n' "$fingerprint_business" ;;
    esac
  }
  validation_phase=staged
  observed_forward_model_head_revision=2
  failures=0
  check_forward_database_state
  [[ "$failures" == 0 && "$observed_forward_database_fingerprint" == "$fingerprint_full" &&
     "$observed_forward_business_fingerprint" == "$fingerprint_business" ]]

  validation_phase=smoke
  expected_forward_database_fingerprint="$fingerprint_full"
  expected_forward_business_fingerprint="$fingerprint_business"
  failures=0
  check_forward_database_state
  [[ "$failures" == 0 ]]
  fingerprint_full="$(printf '3%.0s' {1..64})"
  failures=0
  check_forward_database_state
  [[ "$failures" == 1 ]]

  validation_phase=activation
  failures=0
  check_forward_database_state
  [[ "$failures" == 0 ]]
  fingerprint_business="$(printf '4%.0s' {1..64})"
  failures=0
  check_forward_database_state
  [[ "$failures" == 1 ]]

  validation_phase=production
  failures=0
  check_forward_database_state
  [[ "$failures" == 0 ]]

  id() {
    [[ "$1" == -u ]]
    printf '%s\n' 0
  }
  expected_runtime_feedback_credential_bindings=''
  expected_forward_database_fingerprint=''
  expected_forward_business_fingerprint=''
  expected_forward_model_head_revision=''
  deployment_transition=initial
  validation_phase=staged
  failures=0
  validate_input_contract
  [[ "$failures" == 0 ]]
  validation_phase=activation
  failures=0
  validate_input_contract
  [[ "$failures" == 0 ]]
  deployment_transition=forward
  validation_phase=smoke
  failures=0
  validate_input_contract || true
  [[ "$failures" == 1 ]]
  expected_forward_database_fingerprint="$(printf '1%.0s' {1..64})"
  expected_forward_business_fingerprint="$(printf '2%.0s' {1..64})"
  expected_forward_model_head_revision=2
  failures=0
  validate_input_contract
  [[ "$failures" == 0 ]]
  validation_phase=activation
  failures=0
  validate_input_contract
  [[ "$failures" == 0 ]]
)

printf 'production v2 state validator fixtures: PASS\n'
