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
	      ({
	        schema: "ascendany.knowledge_catalog.publication-receipt.v1",
	        authorizationId: "77777777-7777-4777-8777-777777777777",
	        knowledgeCatalogPublicationId: "1",
	        targetModelReleaseId: "1",
	        catalogSha256: $installed.manifest.knowledgeCatalogSha256,
	        modelArtifactSha256: $artifactSHA256,
	        modelId: $installed.manifest.modelId,
	        targetApplicationVersion: $applicationVersion,
	        targetApplicationCommit: $applicationCommit,
	        targetApplicationBuildTime: $applicationBuildTime,
	        configurationKey: "recommendation.catalog.active",
	        configurationId: "11111111-1111-4111-8111-111111111111",
	        expectedConfigurationHeadRevision: 0,
	        configurationHeadRevision: 1,
	        configurationVersionId: "1",
	        configurationVersionNumber: 1,
	        analyticsGenerationId: "1",
	        analyticsHeadRevision: 1,
	        inputManifestSha256: ("a" * 64),
	        currentModelHeadRevision: 1,
	        currentModelArtifactSha256: $artifactSHA256,
	        publishedByAccountId: "22222222-2222-4222-8222-222222222222",
	        publishedBySessionId: "33333333-3333-4333-8333-333333333333",
	        publishedAt: "2026-07-13T08:03:00Z",
	        auditEventId: "1",
	        configurationMutated: true
	      }) as $publication |
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
	            {version: 6, name: "six", sha256: ("6" * 64)},
	            {version: 7, name: "seven", sha256: ("7" * 64)},
	            {version: 8, name: "eight", sha256: ("8" * 64)},
	            {version: 9, name: "nine", sha256: ("9" * 64)},
	            {version: 10, name: "ten", sha256: ("a" * 64)}
	          ],
	          knowledgeCatalogPublicationIds: ["1"],
	          knowledgeCatalogPublications: [$publication],
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
	        },
	        catalogPublicationReceipts: {
	          file: {filename: "catalog-receipts.tar.zst", format: "tar+zstd", sha256: ("3" * 64), sizeBytes: 1},
	          count: 1,
	          totalBytes: 1024,
	          entries: [{publicationId: "1", path: "1.json", sha256: ("4" * 64), sizeBytes: 1024, mode: 416}]
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
	      --arg releaseVersion "$release_manifest_version" \
	      --arg catalogReceiptRoot "$restore_catalog_receipt_root" '
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
	          catalogReceiptRoot: $catalogReceiptRoot,
	          catalogReceiptCount: $bundle.catalogPublicationReceipts.count,
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
    local pending_publication_id="${10:-}"
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
      "$model_id|$release_model_sha256|$model_size|420|ascendany.recommendation.inference-model.v1|production|knowledge_mirt_feature_v1|ascendany.recommendation.inference.v1|$catalog_sha|$revision|$pending_publication_id|$release_model_sha256|$event_version|$event_commit|$event_build_time|$catalog_kind_count|$catalog_key_count|$catalog_key|$catalog_kind|$catalog_revision|$catalog_revision|$catalog_schema|$catalog_document_sha|"
  }

  deployment_transition=forward
  validation_phase=staged
  fixture_runtime_result="$(fixture_model_binding_row 2.0.0 "$old_application_commit" 2026-07-12T08:00:00Z 2)"
  failures=0
  check_recommendation_model_binding
  [[ "$failures" == 0 && "$observed_forward_model_head_revision" == 2 ]]

  validation_phase=smoke
  expected_forward_model_head_revision=2
  expected_forward_model_artifact_sha256="$release_model_sha256"
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
  validation_phase=staged
  fixture_runtime_result='0|0|0'
  failures=0
  check_recommendation_model_binding
  [[ "$failures" == 0 ]]
  validation_phase=smoke
  failures=0
  check_recommendation_model_binding
  [[ "$failures" == 0 ]]
  fixture_runtime_result="$(fixture_model_binding_row "$release_manifest_version" "$release_manifest_commit" "$release_manifest_build_time" 1 0 0)"
  failures=0
  check_recommendation_model_binding
  [[ "$failures" == 1 ]]

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

  validation_phase=catalog
  failures=0
  check_forward_database_state
  [[ "$failures" == 1 ]]
  fingerprint_business="$(printf '4%.0s' {1..64})"
  failures=0
  check_forward_database_state
  [[ "$failures" == 0 && "$observed_forward_database_fingerprint" == "$fingerprint_full" &&
     "$observed_forward_business_fingerprint" == "$fingerprint_business" ]]

  validation_phase=activation
  expected_forward_database_fingerprint="$fingerprint_full"
  expected_forward_business_fingerprint="$fingerprint_business"
  failures=0
  check_forward_database_state
  [[ "$failures" == 1 ]]
  fingerprint_full="$(printf '5%.0s' {1..64})"
  failures=0
  check_forward_database_state
  [[ "$failures" == 0 ]]
  fingerprint_business="$(printf '6%.0s' {1..64})"
  failures=0
  check_forward_database_state
  [[ "$failures" == 1 ]]

  validation_phase=production
  failures=0
  check_forward_database_state
  [[ "$failures" == 0 ]]

  agent_receipt_fixture="$WORK_ROOT/agent-acceptance-receipt.json"
  agent_model_document_sha256=ad58a2558e7a27fc624cf6d4166363d4ec0459072a015bc7f41fbc6e15cd4fc2
  agent_provider_credential_sha256="$(printf 'c%.0s' {1..64})"
  agent_probe_checked_at="$(date -u -d '1 second ago' '+%Y-%m-%dT%H:%M:%SZ')"
  agent_accepted_at="$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
  jq -n -Sc \
    --arg promptDocumentSHA256 "$agent_prompt_document_sha256" \
    --arg modelDocumentSHA256 "$agent_model_document_sha256" \
    --arg providerCredentialSHA256 "$agent_provider_credential_sha256" \
    --arg acceptedAt "$agent_accepted_at" \
    --arg probeCheckedAt "$agent_probe_checked_at" \
    --arg targetApplicationVersion "$release_manifest_version" \
    --arg targetApplicationCommit "$release_manifest_commit" \
    --arg targetApplicationBuildTime "$release_manifest_build_time" '
      {
        schema: "ascendany.production-agent-acceptance-receipt.v1",
        acceptedAt: $acceptedAt,
        administratorAccountId: "00000000-0000-4000-8000-000000000201",
        acceptanceStudentAccountId: "00000000-0000-4000-8000-000000000202",
        acceptanceStudentUsername: "acceptance_student",
        acceptanceStudentNumber: "20260001",
        targetApplicationVersion: $targetApplicationVersion,
        targetApplicationCommit: $targetApplicationCommit,
        targetApplicationBuildTime: $targetApplicationBuildTime,
        providerCredentialSha256: $providerCredentialSHA256,
        promptConfiguration: {
          key: "agent.prompt.default", configurationId: "00000000-0000-4000-8000-000000000203",
          headRevision: 1, versionId: "41", versionNumber: 1,
          schemaId: "ascendany.prompt.chat.v1", documentSha256: $promptDocumentSHA256,
          credentialRef: null, state: "created"
        },
        modelConfiguration: {
          key: "agent.model.default", configurationId: "00000000-0000-4000-8000-000000000204",
          headRevision: 1, versionId: "42", versionNumber: 1,
          schemaId: "ascendany.model_connection.openai_compatible.v1",
          documentSha256: $modelDocumentSHA256, credentialRef: "models.primary", state: "created"
        },
        modelProbe: {
          configurationKey: "agent.model.default", configurationHeadRevision: 1,
          configurationVersion: 1, configurationSha256: $modelDocumentSHA256,
          authority: "models.example:443", model: "reasoner-v1",
          checkedAt: $probeCheckedAt, latencyMilliseconds: 25
        },
        replyAcceptance: {
          created: true,
          runId: "00000000-0000-4000-8000-000000000205",
          threadId: "00000000-0000-4000-8000-000000000206",
          inputMessageId: "00000000-0000-4000-8000-000000000207",
          outputMessageId: "00000000-0000-4000-8000-000000000208",
          replySha256: ("a" * 64), eventCount: 4, terminalDoneCount: 1
        },
        autoAnalysisAcceptance: {
          created: false,
          runId: "00000000-0000-4000-8000-000000000209",
          threadId: "00000000-0000-4000-8000-00000000020a",
          inputMessageId: "00000000-0000-4000-8000-00000000020b",
          outputMessageId: "00000000-0000-4000-8000-00000000020c",
          replySha256: ("b" * 64), eventCount: 4, terminalDoneCount: 1
        }
      }
    ' >"$agent_receipt_fixture"
  chmod 0400 "$agent_receipt_fixture"
  stat() {
    local target="${!#}"
    if [[ "$target" == "$agent_receipt_fixture" && "$1" == -Lc && "$2" == '%u:%g:%a:%h' ]]; then
      printf '%s\n' '0:0:400:1'
      return
    fi
    command stat "$@"
  }
  check_root_owned_ancestry() { return 0; }
  encrypted_credential_sha256() {
    [[ "$1" == models_primary &&
       "$2" == /etc/ascendany/credentials/models_primary.cred ]]
    printf '%s\n' "$agent_provider_credential_sha256"
  }
  fixture_agent_database_match=1
  run_runtime_psql() {
    printf '%s\n' "$*" >"$WORK_ROOT/agent-acceptance-query.args"
    command cat >"$WORK_ROOT/agent-acceptance-query.sql"
    printf '%s\n' "$fixture_agent_database_match"
  }
  deployment_transition=initial
  validation_phase=production
  agent_acceptance_receipt_path="$agent_receipt_fixture"
  runtime_provider_bindings=(
    'ASCENDANY_CREDENTIAL_FILE_REF_HEX_6D6F64656C732E7072696D617279_AUTHORITY_HEX_6D6F64656C732E6578616D706C653A343433=models_primary'
  )
  failures=0
  check_agent_acceptance_receipt
  [[ "$failures" == 0 ]]
  grep -F -- '-v prompt_configuration_id=00000000-0000-4000-8000-000000000203' \
    "$WORK_ROOT/agent-acceptance-query.args" >/dev/null
  grep -F -- '-v model_version_id=42' "$WORK_ROOT/agent-acceptance-query.args" >/dev/null
  grep -F -- '-v reply_run_id=00000000-0000-4000-8000-000000000205' \
    "$WORK_ROOT/agent-acceptance-query.args" >/dev/null
  grep -F -- '-v auto_created=false' "$WORK_ROOT/agent-acceptance-query.args" >/dev/null
  grep -F "model_version.credential_ref = :'model_credential_ref'" \
    "$WORK_ROOT/agent-acceptance-query.sql" >/dev/null
  grep -F "run.analytics_generation_id = analytics.analytics_generation_id" \
    "$WORK_ROOT/agent-acceptance-query.sql" >/dev/null
  grep -F "tool.tool_name = 'analytics.get_self'" \
    "$WORK_ROOT/agent-acceptance-query.sql" >/dev/null
  jq -Sc \
    '.promptConfiguration.state = "matched" | .modelConfiguration.state = "matched"' \
    "$agent_receipt_fixture" >"$WORK_ROOT/agent-acceptance-retry.json"
  mv -- "$WORK_ROOT/agent-acceptance-retry.json" "$agent_receipt_fixture"
  chmod 0400 "$agent_receipt_fixture"
  failures=0
  check_agent_acceptance_receipt
  [[ "$failures" == 0 ]]
  agent_provider_credential_sha256="$(printf 'd%.0s' {1..64})"
  failures=0
  check_agent_acceptance_receipt
  [[ "$failures" == 1 ]]
  agent_provider_credential_sha256="$(printf 'c%.0s' {1..64})"
  fixture_agent_database_match=0
  failures=0
  check_agent_acceptance_receipt
  [[ "$failures" == 1 ]]

  id() {
    [[ "$1" == -u ]]
    printf '%s\n' 0
  }
  expected_runtime_provider_credential_bindings=''
  expected_forward_database_fingerprint=''
  expected_forward_business_fingerprint=''
  expected_forward_model_head_revision=''
  expected_forward_model_artifact_sha256=''
  agent_acceptance_receipt_path=''
  deployment_transition=initial
  validation_phase=staged
  failures=0
  validate_input_contract
  [[ "$failures" == 0 ]]
  agent_acceptance_receipt_path=/var/lib/ascendany-acceptance/forbidden.json
  failures=0
  validate_input_contract || true
  [[ "$failures" == 1 ]]
  agent_acceptance_receipt_path=''
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
  expected_forward_model_artifact_sha256="$(printf '3%.0s' {1..64})"
  failures=0
  validate_input_contract
  [[ "$failures" == 0 ]]
  validation_phase=activation
  failures=0
  validate_input_contract
  [[ "$failures" == 0 ]]
  validation_phase=production
  agent_acceptance_receipt_path=''
  failures=0
  validate_input_contract || true
  [[ "$failures" == 1 ]]
  agent_acceptance_receipt_path=/var/lib/ascendany-acceptance/agent-acceptance-receipt.json
  failures=0
  validate_input_contract
  [[ "$failures" == 0 ]]
)

printf 'production v2 state validator fixtures: PASS\n'
