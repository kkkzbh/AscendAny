package analytics

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type generationRecord struct {
	BaseAnalyticsGenerationID *int64
	BaseHeadRevision          int64
	TargetExamID              int64
	TargetSnapshotID          int64
	TargetExamHeadRevision    int64
	ManifestJSON              []byte
	ManifestSHA256            string
	AlgorithmVersion          string
	ConfigSHA256              string
}

func (repository *PostgresRepository) Load(
	ctx context.Context,
	claim Claim,
	configuration ParsedConfig,
) (item WorkItem, resultErr error) {
	resultErr = repository.transaction(ctx, "load analytics generation", pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	}, func(tx analyticsTx) error {
		record, err := loadClaimedGeneration(ctx, tx, claim)
		if err != nil {
			return err
		}
		manifest, err := validateGenerationContract(record, claim, configuration)
		if err != nil {
			return err
		}
		if err := validateRelationalManifest(ctx, tx, claim.GenerationID, manifest.Value.Snapshots); err != nil {
			return err
		}
		dataset, err := loadDataset(ctx, tx, manifest.Value.Snapshots)
		if err != nil {
			return err
		}
		item = WorkItem{Claim: claim, Manifest: manifest, Dataset: dataset}
		return nil
	})
	return item, resultErr
}

func loadClaimedGeneration(ctx context.Context, tx analyticsTx, claim Claim) (generationRecord, error) {
	if claim.GenerationID <= 0 || claim.LeaseOwner == "" || claim.AttemptCount <= 0 {
		return generationRecord{}, analyticsError(ErrorStateConflict, false, "load claimed generation", errors.New("claim identity is invalid"))
	}
	record := generationRecord{}
	err := tx.QueryRow(ctx, `
SELECT base_analytics_generation_id,
       base_head_revision,
       target_exam_id,
       target_snapshot_id,
       target_exam_head_revision,
       input_manifest::text,
       input_manifest_sha256,
       algorithm_version,
       config_sha256
FROM ascendany.analytics_generations
WHERE analytics_generation_id = $1
  AND status = 'running'
  AND lease_owner = $2
  AND attempt_count = $3
  AND lease_expires_at > clock_timestamp()`, claim.GenerationID, claim.LeaseOwner, claim.AttemptCount).Scan(
		&record.BaseAnalyticsGenerationID,
		&record.BaseHeadRevision,
		&record.TargetExamID,
		&record.TargetSnapshotID,
		&record.TargetExamHeadRevision,
		&record.ManifestJSON,
		&record.ManifestSHA256,
		&record.AlgorithmVersion,
		&record.ConfigSHA256,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return generationRecord{}, analyticsError(ErrorLeaseLost, false, "load claimed generation", errors.New("analytics lease is no longer active"))
	}
	if err != nil {
		return generationRecord{}, databaseError("load claimed generation", err)
	}
	return record, nil
}

func validateGenerationContract(record generationRecord, claim Claim, configuration ParsedConfig) (ParsedManifest, error) {
	if !sameOptionalInt64(record.BaseAnalyticsGenerationID, claim.BaseAnalyticsGenerationID) ||
		record.BaseHeadRevision != claim.BaseHeadRevision ||
		record.TargetExamID != claim.TargetExamID ||
		record.TargetSnapshotID != claim.TargetSnapshotID ||
		record.TargetExamHeadRevision != claim.TargetExamHeadRevision ||
		record.ManifestSHA256 != claim.ManifestSHA256 ||
		record.AlgorithmVersion != claim.AlgorithmVersion ||
		record.ConfigSHA256 != claim.ConfigSHA256 {
		return ParsedManifest{}, analyticsError(ErrorStateConflict, false, "validate generation contract", errors.New("claimed columns changed"))
	}
	manifest, err := ParseManifest(record.ManifestJSON)
	if err != nil {
		return ParsedManifest{}, err
	}
	if manifest.SHA256 != record.ManifestSHA256 {
		return ParsedManifest{}, analyticsError(ErrorInvalidManifest, true, "validate generation contract", errors.New("manifest SHA-256 differs from canonical content"))
	}
	if !sameOptionalInt64(manifest.Value.BaseAnalyticsGenerationID, record.BaseAnalyticsGenerationID) ||
		manifest.Value.BaseHeadRevision != record.BaseHeadRevision ||
		manifest.Value.Target.ExamID != record.TargetExamID ||
		manifest.Value.Target.SnapshotID != record.TargetSnapshotID ||
		manifest.Value.Target.ExamHeadRevision != record.TargetExamHeadRevision {
		return ParsedManifest{}, analyticsError(ErrorInvalidManifest, true, "validate generation contract", errors.New("manifest identity differs from generation columns"))
	}
	if record.AlgorithmVersion != configuration.Value.AlgorithmVersion {
		return ParsedManifest{}, analyticsError(ErrorAlgorithmMismatch, true, "validate generation contract", fmt.Errorf("generation algorithm %q differs from runtime algorithm %q", record.AlgorithmVersion, configuration.Value.AlgorithmVersion))
	}
	if record.ConfigSHA256 != configuration.SHA256 {
		return ParsedManifest{}, analyticsError(ErrorConfigMismatch, true, "validate generation contract", fmt.Errorf("generation config %s differs from runtime config %s", record.ConfigSHA256, configuration.SHA256))
	}
	return manifest, nil
}

func validateRelationalManifest(
	ctx context.Context,
	tx analyticsTx,
	generationID int64,
	expected []ManifestSnapshot,
) error {
	rows, err := tx.Query(ctx, `
SELECT exam_id, snapshot_id, domain_hash
FROM ascendany.analytics_generation_snapshots
WHERE analytics_generation_id = $1
ORDER BY exam_id`, generationID)
	if err != nil {
		return databaseError("load relational analytics manifest", err)
	}
	defer rows.Close()
	actual := make([]ManifestSnapshot, 0, len(expected))
	for rows.Next() {
		entry := ManifestSnapshot{}
		if err := rows.Scan(&entry.ExamID, &entry.SnapshotID, &entry.DomainHash); err != nil {
			return databaseError("scan relational analytics manifest", err)
		}
		actual = append(actual, entry)
	}
	if err := rows.Err(); err != nil {
		return databaseError("iterate relational analytics manifest", err)
	}
	if !slices.Equal(actual, expected) {
		return analyticsError(ErrorInvalidManifest, true, "validate relational analytics manifest", errors.New("relational snapshot rows differ from canonical manifest"))
	}
	return nil
}

func loadDataset(ctx context.Context, tx analyticsTx, expected []ManifestSnapshot) (Dataset, error) {
	snapshotIDs := make([]int64, len(expected))
	bySnapshot := make(map[int64]*SnapshotData, len(expected))
	dataset := Dataset{Snapshots: make([]SnapshotData, len(expected))}
	for index, entry := range expected {
		snapshotIDs[index] = entry.SnapshotID
		dataset.Snapshots[index] = SnapshotData{ExamID: entry.ExamID, SnapshotID: entry.SnapshotID, DomainHash: entry.DomainHash}
		bySnapshot[entry.SnapshotID] = &dataset.Snapshots[index]
	}
	if err := loadSnapshotHeaders(ctx, tx, expected, snapshotIDs, bySnapshot); err != nil {
		return Dataset{}, err
	}
	if err := loadProblems(ctx, tx, snapshotIDs, bySnapshot); err != nil {
		return Dataset{}, err
	}
	participants, err := loadParticipantsAndRankings(ctx, tx, snapshotIDs, bySnapshot)
	if err != nil {
		return Dataset{}, err
	}
	if err := loadRankingProblemResults(ctx, tx, snapshotIDs, participants); err != nil {
		return Dataset{}, err
	}
	if err := loadSubmissions(ctx, tx, snapshotIDs, bySnapshot); err != nil {
		return Dataset{}, err
	}
	return dataset, nil
}

func loadSnapshotHeaders(
	ctx context.Context,
	tx analyticsTx,
	expected []ManifestSnapshot,
	snapshotIDs []int64,
	bySnapshot map[int64]*SnapshotData,
) error {
	rows, err := tx.Query(ctx, `
SELECT exam_id,
       snapshot_id,
       domain_hash,
       starts_at,
       ends_at,
       total_score,
       problems_exported_count,
       participants_exported_count,
       rankings_exported_count,
       submissions_exported_count
FROM ascendany.exam_snapshots
WHERE snapshot_id = ANY($1::bigint[])
ORDER BY exam_id`, snapshotIDs)
	if err != nil {
		return databaseError("load analytics snapshot headers", err)
	}
	defer rows.Close()
	index := 0
	for rows.Next() {
		var examID int64
		var snapshotID int64
		var domainHash string
		var startsAt pgtype.Timestamptz
		var endsAt pgtype.Timestamptz
		var totalScore pgtype.Numeric
		var problems int64
		var participants int64
		var rankings int64
		var submissions int64
		if err := rows.Scan(&examID, &snapshotID, &domainHash, &startsAt, &endsAt, &totalScore, &problems, &participants, &rankings, &submissions); err != nil {
			return databaseError("scan analytics snapshot header", err)
		}
		if index >= len(expected) || expected[index].ExamID != examID || expected[index].SnapshotID != snapshotID || expected[index].DomainHash != domainHash {
			return analyticsError(ErrorInvalidDataset, true, "validate analytics snapshot headers", errors.New("loaded snapshot header differs from manifest"))
		}
		snapshot := bySnapshot[snapshotID]
		if snapshot == nil {
			return analyticsError(ErrorInvalidDataset, true, "validate analytics snapshot headers", errors.New("loaded an unexpected snapshot"))
		}
		var conversionErr error
		snapshot.StartsAt, conversionErr = optionalTimestamp(startsAt, fmt.Sprintf("snapshot %d starts_at", snapshotID))
		if conversionErr != nil {
			return conversionErr
		}
		snapshot.EndsAt, conversionErr = optionalTimestamp(endsAt, fmt.Sprintf("snapshot %d ends_at", snapshotID))
		if conversionErr != nil {
			return conversionErr
		}
		snapshot.TotalScore, conversionErr = optionalNumeric(totalScore, fmt.Sprintf("snapshot %d total_score", snapshotID))
		if conversionErr != nil {
			return conversionErr
		}
		snapshot.ExpectedProblems = problems
		snapshot.ExpectedParticipants = participants
		snapshot.ExpectedRankings = rankings
		snapshot.ExpectedSubmissions = submissions
		index++
	}
	if err := rows.Err(); err != nil {
		return databaseError("iterate analytics snapshot headers", err)
	}
	if index != len(expected) {
		return analyticsError(ErrorInvalidDataset, true, "validate analytics snapshot headers", errors.New("one or more manifest snapshots are missing"))
	}
	return nil
}

func loadProblems(ctx context.Context, tx analyticsTx, snapshotIDs []int64, bySnapshot map[int64]*SnapshotData) error {
	rows, err := tx.Query(ctx, `
SELECT snapshot_id, problem_set_problem_id, max_score
FROM ascendany.pintia_snapshot_problems
WHERE snapshot_id = ANY($1::bigint[])
ORDER BY snapshot_id, problem_set_problem_id`, snapshotIDs)
	if err != nil {
		return databaseError("load analytics problems", err)
	}
	defer rows.Close()
	for rows.Next() {
		var snapshotID int64
		var problemID string
		var maxScore pgtype.Numeric
		if err := rows.Scan(&snapshotID, &problemID, &maxScore); err != nil {
			return databaseError("scan analytics problem", err)
		}
		snapshot := bySnapshot[snapshotID]
		if snapshot == nil {
			return analyticsError(ErrorInvalidDataset, true, "load analytics problems", errors.New("problem references an unexpected snapshot"))
		}
		value, err := optionalNumeric(maxScore, fmt.Sprintf("snapshot %d problem %q max_score", snapshotID, problemID))
		if err != nil {
			return err
		}
		snapshot.Problems = append(snapshot.Problems, ProblemData{ProblemSetProblemID: problemID, MaxScore: value})
	}
	if err := rows.Err(); err != nil {
		return databaseError("iterate analytics problems", err)
	}
	return nil
}

type participantKey struct {
	snapshotID int64
	actorID    int64
}

type participantLocation struct {
	snapshot *SnapshotData
	index    int
}

func loadParticipantsAndRankings(
	ctx context.Context,
	tx analyticsTx,
	snapshotIDs []int64,
	bySnapshot map[int64]*SnapshotData,
) (map[participantKey]participantLocation, error) {
	rows, err := tx.Query(ctx, `
SELECT participant.snapshot_id,
       participant.actor_id,
       ranking.rank,
       ranking.total_score,
       ranking.time_used_seconds
FROM ascendany.pintia_snapshot_participants AS participant
LEFT JOIN ascendany.pintia_rankings AS ranking
  ON ranking.snapshot_id = participant.snapshot_id
 AND ranking.actor_id = participant.actor_id
WHERE participant.snapshot_id = ANY($1::bigint[])
ORDER BY participant.snapshot_id, participant.actor_id`, snapshotIDs)
	if err != nil {
		return nil, databaseError("load analytics participants", err)
	}
	defer rows.Close()
	participants := make(map[participantKey]participantLocation)
	for rows.Next() {
		var snapshotID int64
		var actorID int64
		var rank pgtype.Int8
		var totalScore pgtype.Numeric
		var timeUsed pgtype.Int8
		if err := rows.Scan(&snapshotID, &actorID, &rank, &totalScore, &timeUsed); err != nil {
			return nil, databaseError("scan analytics participant", err)
		}
		snapshot := bySnapshot[snapshotID]
		if snapshot == nil {
			return nil, analyticsError(ErrorInvalidDataset, true, "load analytics participants", errors.New("participant references an unexpected snapshot"))
		}
		participant := ParticipantData{ActorID: actorID}
		if rank.Valid {
			score, err := optionalNumeric(totalScore, fmt.Sprintf("snapshot %d actor %d total_score", snapshotID, actorID))
			if err != nil {
				return nil, err
			}
			participant.Ranking = &RankingData{Rank: rank.Int64, TotalScore: score, TimeUsedSeconds: optionalInt64(timeUsed)}
		} else if totalScore.Valid || timeUsed.Valid {
			return nil, analyticsError(ErrorInvalidDataset, true, "load analytics participants", errors.New("ranking detail exists without a rank"))
		}
		snapshot.Participants = append(snapshot.Participants, participant)
		key := participantKey{snapshotID: snapshotID, actorID: actorID}
		if _, exists := participants[key]; exists {
			return nil, analyticsError(ErrorInvalidDataset, true, "load analytics participants", errors.New("participant row is duplicated"))
		}
		participants[key] = participantLocation{snapshot: snapshot, index: len(snapshot.Participants) - 1}
	}
	if err := rows.Err(); err != nil {
		return nil, databaseError("iterate analytics participants", err)
	}
	return participants, nil
}

func loadRankingProblemResults(
	ctx context.Context,
	tx analyticsTx,
	snapshotIDs []int64,
	participants map[participantKey]participantLocation,
) error {
	rows, err := tx.Query(ctx, `
SELECT snapshot_id,
       actor_id,
       problem_set_problem_id,
       score,
       passed,
       valid_submission_count
FROM ascendany.pintia_ranking_problem_results
WHERE snapshot_id = ANY($1::bigint[])
ORDER BY snapshot_id, actor_id, problem_set_problem_id`, snapshotIDs)
	if err != nil {
		return databaseError("load analytics ranking problem results", err)
	}
	defer rows.Close()
	for rows.Next() {
		var snapshotID int64
		var actorID int64
		var problemID string
		var score pgtype.Numeric
		var passed pgtype.Bool
		var count pgtype.Int8
		if err := rows.Scan(&snapshotID, &actorID, &problemID, &score, &passed, &count); err != nil {
			return databaseError("scan analytics ranking problem result", err)
		}
		location, exists := participants[participantKey{snapshotID: snapshotID, actorID: actorID}]
		if !exists {
			return analyticsError(ErrorInvalidDataset, true, "load analytics ranking problem results", errors.New("ranking result references an unexpected participant"))
		}
		participant := &location.snapshot.Participants[location.index]
		if participant.Ranking == nil {
			return analyticsError(ErrorInvalidDataset, true, "load analytics ranking problem results", errors.New("ranking result references a participant without ranking"))
		}
		scoreValue, err := optionalNumeric(score, fmt.Sprintf("snapshot %d actor %d problem %q score", snapshotID, actorID, problemID))
		if err != nil {
			return err
		}
		participant.ProblemResults = append(participant.ProblemResults, RankingProblemResultData{
			ProblemSetProblemID:  problemID,
			Score:                scoreValue,
			Passed:               optionalBool(passed),
			ValidSubmissionCount: optionalInt64(count),
		})
	}
	if err := rows.Err(); err != nil {
		return databaseError("iterate analytics ranking problem results", err)
	}
	return nil
}

func loadSubmissions(ctx context.Context, tx analyticsTx, snapshotIDs []int64, bySnapshot map[int64]*SnapshotData) error {
	rows, err := tx.Query(ctx, `
SELECT submission.snapshot_id,
       submission.submission_identity_id,
       submission.actor_id,
       submission.problem_set_problem_id,
       identity.submitted_at,
       submission.verdict,
       submission.score,
       submission.time_ms,
       submission.memory_bytes
FROM ascendany.pintia_snapshot_submissions AS submission
JOIN ascendany.pintia_submission_identities AS identity
  ON identity.submission_identity_id = submission.submission_identity_id
WHERE submission.snapshot_id = ANY($1::bigint[])
ORDER BY submission.snapshot_id, submission.submission_identity_id`, snapshotIDs)
	if err != nil {
		return databaseError("load analytics submissions", err)
	}
	defer rows.Close()
	for rows.Next() {
		var snapshotID int64
		var submissionID int64
		var actorID int64
		var problemID string
		var submittedAt time.Time
		var verdict string
		var score pgtype.Numeric
		var timeMS pgtype.Int8
		var memoryBytes pgtype.Int8
		if err := rows.Scan(&snapshotID, &submissionID, &actorID, &problemID, &submittedAt, &verdict, &score, &timeMS, &memoryBytes); err != nil {
			return databaseError("scan analytics submission", err)
		}
		snapshot := bySnapshot[snapshotID]
		if snapshot == nil {
			return analyticsError(ErrorInvalidDataset, true, "load analytics submissions", errors.New("submission references an unexpected snapshot"))
		}
		scoreValue, err := optionalNumeric(score, fmt.Sprintf("snapshot %d submission %d score", snapshotID, submissionID))
		if err != nil {
			return err
		}
		snapshot.Submissions = append(snapshot.Submissions, SubmissionData{
			SubmissionIdentityID: submissionID,
			ActorID:              actorID,
			ProblemSetProblemID:  problemID,
			SubmittedAt:          submittedAt.UTC(),
			Verdict:              verdict,
			Score:                scoreValue,
			TimeMS:               optionalInt64(timeMS),
			MemoryBytes:          optionalInt64(memoryBytes),
		})
	}
	if err := rows.Err(); err != nil {
		return databaseError("iterate analytics submissions", err)
	}
	return nil
}

func optionalNumeric(value pgtype.Numeric, field string) (*float64, error) {
	if !value.Valid {
		return nil, nil
	}
	converted, err := value.Float64Value()
	if err != nil || !converted.Valid || !finite(converted.Float64) {
		if err == nil {
			err = errors.New("numeric value is not finite")
		}
		return nil, analyticsError(ErrorInvalidDataset, true, "decode "+field, err)
	}
	result := converted.Float64
	return &result, nil
}

func optionalTimestamp(value pgtype.Timestamptz, field string) (*time.Time, error) {
	if !value.Valid {
		return nil, nil
	}
	if value.InfinityModifier != pgtype.Finite || value.Time.IsZero() {
		return nil, analyticsError(ErrorInvalidDataset, true, "decode "+field, errors.New("timestamp must be finite and nonzero"))
	}
	result := value.Time.UTC()
	return &result, nil
}

func optionalInt64(value pgtype.Int8) *int64 {
	if !value.Valid {
		return nil
	}
	result := value.Int64
	return &result
}

func optionalBool(value pgtype.Bool) *bool {
	if !value.Valid {
		return nil
	}
	result := value.Bool
	return &result
}

func sameOptionalInt64(left, right *int64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}
