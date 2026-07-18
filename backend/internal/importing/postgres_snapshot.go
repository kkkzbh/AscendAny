package importing

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"

	"github.com/kkkzbh/AscendAny/backend/internal/pintia"
)

func (r *PostgresRepository) ImportSnapshot(
	ctx context.Context,
	request ImportRequest,
) (outcome ImportOutcome, resultErr error) {
	if err := validateImportRequest(request); err != nil {
		return ImportOutcome{}, err
	}
	resultErr = r.transaction(ctx, "import Pintia snapshot", func(tx dbTx) error {
		if err := lockImportingJob(ctx, tx, request.Claim); err != nil {
			return err
		}

		exam, err := lockLogicalExam(ctx, tx, request)
		if err != nil {
			return err
		}
		duplicate, err := findDomainDuplicate(ctx, tx, exam.ID, request.DomainHash)
		if err != nil {
			return err
		}
		if duplicate != nil {
			if err := supersedeDuplicateJob(ctx, tx, request.Claim, *duplicate, request.DomainHash); err != nil {
				return err
			}
			outcome = ImportOutcome{
				Disposition:      ImportDuplicate,
				SnapshotID:       &duplicate.ID,
				SnapshotPublicID: &duplicate.PublicID,
			}
			return nil
		}

		actorIDs, err := ensureActorsAndIdentifiers(ctx, tx, request.Snapshot.Participants)
		if err != nil {
			return err
		}
		revision, err := nextRevision(exam.HeadRevision, "advance logical exam head")
		if err != nil {
			return err
		}
		snapshotID, err := insertSnapshot(ctx, tx, request, exam.ID, revision)
		if err != nil {
			return err
		}
		if err := insertSnapshotContents(ctx, tx, request.Snapshot, exam.ID, snapshotID, actorIDs); err != nil {
			return err
		}
		if err := publishExamHead(ctx, tx, exam, snapshotID, revision); err != nil {
			return err
		}

		generationID, err := enqueueAnalytics(ctx, tx, request, exam.ID, snapshotID, revision)
		if err != nil {
			return err
		}
		if err := markJobAnalyzing(ctx, tx, request.Claim, snapshotID, request.PublicIDs.Snapshot, generationID, request.DomainHash); err != nil {
			return err
		}
		outcome = ImportOutcome{
			Disposition:           ImportCreated,
			SnapshotID:            &snapshotID,
			SnapshotPublicID:      &request.PublicIDs.Snapshot,
			AnalyticsGenerationID: &generationID,
		}
		return nil
	})
	return outcome, resultErr
}

func lockParticipantIdentityPublication(ctx context.Context, tx dbTx) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, pintia.ParticipantIdentityAdvisoryLockID); err != nil {
		return databaseError("lock participant identity publication", err)
	}
	return nil
}

func publishExamHead(ctx context.Context, tx dbTx, exam lockedExam, snapshotID, revision int64) error {
	if err := lockParticipantIdentityPublication(ctx, tx); err != nil {
		return err
	}
	return compareAndSwapExamHead(ctx, tx, exam, snapshotID, revision)
}

type lockedExam struct {
	ID               int64
	ActiveSnapshotID *int64
	HeadRevision     int64
}

type existingSnapshot struct {
	ID       int64
	PublicID string
}

func validateImportRequest(request ImportRequest) error {
	if request.Snapshot == nil {
		return importError(ErrorValidation, true, "validate import request", errors.New("snapshot is required"))
	}
	if !lowercaseSHA256Pattern.MatchString(request.DomainHash) {
		return importError(ErrorValidation, true, "validate import request", errors.New("domain hash must be lowercase SHA-256"))
	}
	if request.PublicIDs.LogicalExam == "" || request.PublicIDs.Snapshot == "" {
		return importError(ErrorUUIDGeneration, false, "validate import request", errors.New("logical exam and snapshot UUIDs are required"))
	}
	if request.Analytics.AlgorithmVersion == "" {
		return importError(ErrorInvalidConfiguration, false, "validate import request", errors.New("analytics algorithm version is required"))
	}
	if !lowercaseSHA256Pattern.MatchString(request.Analytics.ConfigSHA256) {
		return importError(ErrorInvalidConfiguration, false, "validate import request", errors.New("analytics config SHA-256 is invalid"))
	}
	if request.Claim.Status != JobRunning || request.Claim.Stage != StageImporting {
		return importError(ErrorStateConflict, false, "validate import request", errors.New("an active importing claim is required"))
	}
	if _, err := requireClaimAttempt(request.Claim, "validate import request"); err != nil {
		return err
	}
	return nil
}

func lockImportingJob(ctx context.Context, tx dbTx, claim Claim) error {
	attempt, err := requireClaimAttempt(claim, "lock importing job")
	if err != nil {
		return err
	}
	var status JobStatus
	var stage JobStage
	var leaseActive bool
	var artifactID int64
	err = tx.QueryRow(ctx, `
SELECT status,
       stage,
       lease_expires_at > clock_timestamp(),
       artifact_id
FROM ascendany.import_jobs
WHERE import_job_id = $1
  AND public_id = $2::uuid
  AND lease_owner = $3
  AND attempt_count = $4
FOR UPDATE`, claim.ID, claim.PublicID, attempt.owner, attempt.count).Scan(&status, &stage, &leaseActive, &artifactID)
	if errors.Is(err, pgx.ErrNoRows) {
		return importError(ErrorLeaseLost, false, "lock importing job", errors.New("importing claim attempt is no longer active"))
	}
	if err != nil {
		return databaseError("lock importing job", err)
	}
	if status != JobRunning || stage != StageImporting || !leaseActive {
		return importError(ErrorLeaseLost, false, "lock importing job", errors.New("importing lease is no longer active"))
	}
	if artifactID != claim.ArtifactID {
		return importError(ErrorArtifactMetadata, true, "lock importing job", errors.New("job artifact changed"))
	}
	return nil
}

func lockLogicalExam(ctx context.Context, tx dbTx, request ImportRequest) (lockedExam, error) {
	if _, err := tx.Exec(ctx, `
INSERT INTO ascendany.logical_exams (public_id, platform, source_exam_id)
VALUES ($1::uuid, 'pintia', $2)
ON CONFLICT (platform, source_exam_id) DO NOTHING`, request.PublicIDs.LogicalExam, request.Snapshot.Exam.ProblemSetID); err != nil {
		return lockedExam{}, databaseError("insert logical exam", err)
	}
	var exam lockedExam
	err := tx.QueryRow(ctx, `
SELECT exam_id, active_snapshot_id, head_revision
FROM ascendany.logical_exams
WHERE platform = 'pintia'
  AND source_exam_id = $1
FOR UPDATE`, request.Snapshot.Exam.ProblemSetID).Scan(&exam.ID, &exam.ActiveSnapshotID, &exam.HeadRevision)
	if err != nil {
		return lockedExam{}, databaseError("lock logical exam", err)
	}
	return exam, nil
}

func findDomainDuplicate(ctx context.Context, tx dbTx, examID int64, domainHash string) (*existingSnapshot, error) {
	var duplicate existingSnapshot
	err := tx.QueryRow(ctx, `
SELECT snapshot_id, public_id::text
FROM ascendany.exam_snapshots
WHERE exam_id = $1
  AND domain_hash_protocol = 'domain_hash_proto_v1'
  AND domain_hash = $2`, examID, domainHash).Scan(&duplicate.ID, &duplicate.PublicID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, databaseError("find domain duplicate", err)
	}
	return &duplicate, nil
}

func supersedeDuplicateJob(
	ctx context.Context,
	tx dbTx,
	claim Claim,
	duplicate existingSnapshot,
	domainHash string,
) error {
	attempt, err := requireClaimAttempt(claim, "supersede duplicate job")
	if err != nil {
		return err
	}
	commandTag, err := tx.Exec(ctx, `
UPDATE ascendany.import_jobs
SET status = 'superseded',
    stage = 'superseded',
    lease_owner = NULL,
    lease_expires_at = NULL,
    finished_at = clock_timestamp(),
    updated_at = clock_timestamp()
WHERE import_job_id = $1
  AND public_id = $2::uuid
  AND status = 'running'
  AND stage = 'importing'
  AND lease_owner = $3
  AND attempt_count = $4
  AND lease_expires_at > clock_timestamp()`, claim.ID, claim.PublicID, attempt.owner, attempt.count)
	if err != nil {
		return databaseError("supersede duplicate job", err)
	}
	if commandTag.RowsAffected() != 1 {
		return importError(ErrorLeaseLost, false, "supersede duplicate job", errors.New("job claim attempt changed"))
	}
	_, err = appendEvent(ctx, tx, claim.ID, "superseded", struct {
		DomainHash             string `json:"domainHash"`
		ExistingSnapshotID     int64  `json:"existingSnapshotId"`
		ExistingSnapshotPublic string `json:"existingSnapshotPublicId"`
	}{domainHash, duplicate.ID, duplicate.PublicID})
	return err
}

func insertSnapshot(
	ctx context.Context,
	tx dbTx,
	request ImportRequest,
	examID int64,
	revision int64,
) (int64, error) {
	snapshot := request.Snapshot
	totalScore, err := postgresDecimal(snapshot.Exam.TotalScore, "$.exam.totalScore")
	if err != nil {
		return 0, err
	}
	problemCounts, err := collectionCounts(snapshot.Completeness.Problems, "$.completeness.problems")
	if err != nil {
		return 0, err
	}
	rankingCounts, err := collectionCounts(snapshot.Completeness.Rankings, "$.completeness.rankings")
	if err != nil {
		return 0, err
	}
	submissionCounts, err := collectionCounts(snapshot.Completeness.Submissions, "$.completeness.submissions")
	if err != nil {
		return 0, err
	}
	participantCount, err := requiredPostgresInteger(
		snapshot.Completeness.Participants.ExportedCount,
		"$.completeness.participants.exportedCount",
	)
	if err != nil {
		return 0, err
	}

	var snapshotID int64
	err = tx.QueryRow(ctx, `
INSERT INTO ascendany.exam_snapshots (
    public_id,
    exam_id,
    snapshot_sequence,
    source_artifact_id,
    import_job_id,
    contract_schema,
    contract_schema_sha256,
    domain_hash_protocol,
    domain_hash,
    exporter_name,
    exporter_version,
    exported_at,
    title,
    source_url,
    starts_at,
    ends_at,
    total_score,
    problems_source_count,
    problems_observed_count,
    problems_exported_count,
    problems_pagination_exhausted,
    rankings_source_count,
    rankings_observed_count,
    rankings_exported_count,
    rankings_pagination_exhausted,
    submissions_source_count,
    submissions_observed_count,
    submissions_exported_count,
    submissions_pagination_exhausted,
    participants_exported_count
)
VALUES (
    $1::uuid, $2, $3, $4, $5,
    $6, $7, $8, $9,
    $10, $11, $12,
    $13, $14, $15, $16, $17::numeric,
    $18, $19, $20, $21,
    $22, $23, $24, $25,
    $26, $27, $28, $29,
    $30
)
RETURNING snapshot_id`,
		request.PublicIDs.Snapshot,
		examID,
		revision,
		request.Claim.ArtifactID,
		request.Claim.ID,
		snapshot.Schema,
		snapshot.SchemaSHA256,
		pintia.DomainHashProtocolV1,
		request.DomainHash,
		snapshot.Exporter.Name,
		snapshot.Exporter.Version,
		normalizeInstant(snapshot.Exporter.ExportedAt.Time),
		snapshot.Exam.Title,
		snapshot.Exam.SourceURL,
		postgresInstant(snapshot.Exam.StartsAt),
		postgresInstant(snapshot.Exam.EndsAt),
		totalScore,
		problemCounts.Source,
		problemCounts.Observed,
		problemCounts.Exported,
		snapshot.Completeness.Problems.PaginationExhausted,
		rankingCounts.Source,
		rankingCounts.Observed,
		rankingCounts.Exported,
		snapshot.Completeness.Rankings.PaginationExhausted,
		submissionCounts.Source,
		submissionCounts.Observed,
		submissionCounts.Exported,
		snapshot.Completeness.Submissions.PaginationExhausted,
		participantCount,
	).Scan(&snapshotID)
	if err != nil {
		return 0, databaseError("insert exam snapshot", err)
	}
	return snapshotID, nil
}

type databaseCollectionCounts struct {
	Source   any
	Observed int64
	Exported int64
}

func collectionCounts(value pintia.CollectionCompleteness, path string) (databaseCollectionCounts, error) {
	source, err := postgresInteger(value.SourceReportedCount, path+".sourceReportedCount")
	if err != nil {
		return databaseCollectionCounts{}, err
	}
	observed, err := requiredPostgresInteger(value.ObservedCount, path+".observedCount")
	if err != nil {
		return databaseCollectionCounts{}, err
	}
	exported, err := requiredPostgresInteger(value.ExportedCount, path+".exportedCount")
	if err != nil {
		return databaseCollectionCounts{}, err
	}
	return databaseCollectionCounts{Source: source, Observed: observed, Exported: exported}, nil
}

func compareAndSwapExamHead(
	ctx context.Context,
	tx dbTx,
	exam lockedExam,
	snapshotID int64,
	revision int64,
) error {
	commandTag, err := tx.Exec(ctx, `
UPDATE ascendany.logical_exams
SET active_snapshot_id = $2,
    head_revision = $3,
    updated_at = clock_timestamp()
WHERE exam_id = $1
  AND head_revision = $4
  AND active_snapshot_id IS NOT DISTINCT FROM $5`,
		exam.ID,
		snapshotID,
		revision,
		exam.HeadRevision,
		exam.ActiveSnapshotID,
	)
	if err != nil {
		return databaseError("compare and swap exam head", err)
	}
	if commandTag.RowsAffected() != 1 {
		return importError(ErrorHeadConflict, false, "compare and swap exam head", fmt.Errorf("logical exam %d head changed", exam.ID))
	}
	return nil
}

func markJobAnalyzing(
	ctx context.Context,
	tx dbTx,
	claim Claim,
	snapshotID int64,
	snapshotPublicID string,
	generationID int64,
	domainHash string,
) error {
	attempt, err := requireClaimAttempt(claim, "mark job analyzing")
	if err != nil {
		return err
	}
	commandTag, err := tx.Exec(ctx, `
UPDATE ascendany.import_jobs
SET stage = 'analyzing',
    lease_owner = NULL,
    lease_expires_at = NULL,
    updated_at = clock_timestamp()
WHERE import_job_id = $1
  AND public_id = $2::uuid
  AND status = 'running'
  AND stage = 'importing'
  AND lease_owner = $3
  AND attempt_count = $4
  AND lease_expires_at > clock_timestamp()`, claim.ID, claim.PublicID, attempt.owner, attempt.count)
	if err != nil {
		return databaseError("mark job analyzing", err)
	}
	if commandTag.RowsAffected() != 1 {
		return importError(ErrorLeaseLost, false, "mark job analyzing", errors.New("job claim attempt changed"))
	}
	_, err = appendEvent(ctx, tx, claim.ID, "snapshot_imported", struct {
		AnalyticsGenerationID int64  `json:"analyticsGenerationId"`
		DomainHash            string `json:"domainHash"`
		SnapshotID            int64  `json:"snapshotId"`
		SnapshotPublicID      string `json:"snapshotPublicId"`
	}{generationID, domainHash, snapshotID, snapshotPublicID})
	return err
}

func sortedParticipants(values []pintia.Participant) []pintia.Participant {
	result := append([]pintia.Participant(nil), values...)
	sort.Slice(result, func(left, right int) bool { return result[left].UserID < result[right].UserID })
	return result
}
