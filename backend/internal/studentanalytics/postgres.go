package studentanalytics

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/kkkzbh/AscendAny/backend/internal/analytics"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

type PgxBeginner interface {
	BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
}

type readTx interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
	Commit(context.Context) error
	Rollback(context.Context) error
}

type beginReadTransaction func(context.Context, pgx.TxOptions) (readTx, error)

type PostgresRepository struct {
	begin beginReadTransaction
}

func NewPostgresRepository(pool PgxBeginner) (*PostgresRepository, error) {
	if pool == nil {
		return nil, studentAnalyticsError(ErrorInvalidConfiguration, "construct PostgreSQL repository", errors.New("database pool is required"))
	}
	return &PostgresRepository{
		begin: func(ctx context.Context, options pgx.TxOptions) (readTx, error) {
			return pool.BeginTx(ctx, options)
		},
	}, nil
}

func newPostgresRepository(begin beginReadTransaction) (*PostgresRepository, error) {
	if begin == nil {
		return nil, studentAnalyticsError(ErrorInvalidConfiguration, "construct PostgreSQL repository", errors.New("transaction beginner is required"))
	}
	return &PostgresRepository{begin: begin}, nil
}

func (repository *PostgresRepository) LoadSelf(ctx context.Context, query SelfQuery) (Result, error) {
	if err := validateSelfQuery(ctx, query); err != nil {
		return Result{}, err
	}
	var result Result
	err := repository.readTransaction(ctx, "load self analytics", func(tx readTx) error {
		resolved, err := resolvePrincipalAndHead(ctx, tx, query)
		if err != nil {
			return err
		}
		if resolved.GenerationID == nil {
			result = Result{State: StateNotGenerated, HeadRevision: 0}
			return nil
		}
		manifest, err := parseHeadManifest(resolved)
		if err != nil {
			return err
		}

		var ratingText string
		var metricsJSON string
		err = tx.QueryRow(ctx, `
SELECT rating::text, metrics::text
FROM ascendany.student_analytics
WHERE analytics_generation_id = $1
  AND actor_id = $2`, *resolved.GenerationID, resolved.ActorID).Scan(&ratingText, &metricsJSON)
		if errors.Is(err, pgx.ErrNoRows) {
			result = Result{State: StateNoObservations, HeadRevision: resolved.HeadRevision}
			return nil
		}
		if err != nil {
			return databaseFailure("load canonical student analytics row", err)
		}
		rating, err := parseCanonicalRating(ratingText)
		if err != nil {
			return storedDataFailure("validate canonical student analytics row", err)
		}
		metrics, err := analytics.DecodeStoredStudentMetrics([]byte(metricsJSON))
		if err != nil {
			return storedDataFailure("decode canonical student analytics row", err)
		}
		if err := validateMetricsAgainstManifest(metrics, manifest); err != nil {
			return storedDataFailure("validate student analytics generation membership", err)
		}
		if len(metrics.RatingHistory) == 0 || rating != metrics.RatingHistory[len(metrics.RatingHistory)-1].NewRating {
			return storedDataFailure("validate canonical student rating", errors.New("row rating differs from the final rating history point"))
		}

		start := len(metrics.ExamHistory) - query.HistoryLimit
		if start < 0 {
			start = 0
		}
		metadata, err := loadHistoryMetadata(
			ctx,
			tx,
			*resolved.GenerationID,
			resolved.ActorID,
			metrics.ExamHistory[start:],
			manifest,
		)
		if err != nil {
			return err
		}
		latestMetric := metrics.ExamHistory[len(metrics.ExamHistory)-1]
		peer, err := loadLatestExamPeer(
			ctx,
			tx,
			*resolved.GenerationID,
			resolved.ActorID,
			latestMetric.ExamID,
			latestMetric.SnapshotID,
		)
		if err != nil {
			return err
		}
		ready := buildReadyResult(rating, metrics, start, metadata)
		ready.LatestPeer = peer
		result = Result{State: StateReady, HeadRevision: resolved.HeadRevision, Ready: &ready}
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	return result, nil
}

func parseCanonicalRating(value string) (int64, error) {
	if value == "" || len(value) > 1 && value[0] == '0' {
		return 0, errors.New("rating must be a canonical non-negative int64")
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return 0, errors.New("rating must be a canonical non-negative int64")
		}
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, errors.New("rating must be a canonical non-negative int64")
	}
	return parsed, nil
}

func (repository *PostgresRepository) readTransaction(
	ctx context.Context,
	operation string,
	run func(readTx) error,
) (resultErr error) {
	tx, err := repository.begin(ctx, pgx.TxOptions{
		IsoLevel:   pgx.RepeatableRead,
		AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		return databaseFailure("begin "+operation, err)
	}
	finished := false
	defer func() {
		if finished {
			return
		}
		rollbackContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if rollbackErr := tx.Rollback(rollbackContext); rollbackErr != nil && !errors.Is(rollbackErr, pgx.ErrTxClosed) {
			wrapped := databaseFailure("rollback "+operation, rollbackErr)
			if resultErr == nil {
				resultErr = wrapped
			} else {
				resultErr = errors.Join(resultErr, wrapped)
			}
		}
	}()
	if err := run(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return databaseFailure("commit "+operation, err)
	}
	finished = true
	return nil
}

type resolvedHead struct {
	ActorID          int64
	GenerationID     *int64
	HeadRevision     int64
	BaseGenerationID *int64
	BaseHeadRevision int64
	TargetExamID     int64
	TargetSnapshotID int64
	TargetRevision   int64
	ManifestJSON     *string
	ManifestSHA256   *string
}

func resolvePrincipalAndHead(ctx context.Context, tx readTx, query SelfQuery) (resolvedHead, error) {
	var resolvedAccountID string
	var resolvedRevision int64
	var resolvedRole string
	var resolvedSessionID string
	var resolvedSessionRevision int64
	var accountActorID *int64
	var accountStudentNumber *string
	var identifierActorID *int64
	var identifierStudentNumber *string
	var headSingleton *bool
	var generationID *int64
	var headRevision *int64
	var generationStatus *string
	var baseGenerationID *int64
	var baseHeadRevision *int64
	var targetExamID *int64
	var targetSnapshotID *int64
	var targetRevision *int64
	var manifestJSON *string
	var manifestSHA256 *string
	err := tx.QueryRow(ctx, `
SELECT account.public_id::text,
       account.auth_revision,
       account.role,
       session.public_id::text,
       session.auth_revision,
       account.actor_id,
       account.student_number,
       identifier.actor_id,
       identifier.identifier_value,
       head.singleton,
       head.current_generation_id,
       head.head_revision,
       generation.status,
       generation.base_analytics_generation_id,
       generation.base_head_revision,
       generation.target_exam_id,
       generation.target_snapshot_id,
       generation.target_exam_head_revision,
       generation.input_manifest::text,
       generation.input_manifest_sha256
FROM ascendany.auth_accounts AS account
JOIN ascendany.auth_sessions AS session
  ON session.account_id = account.account_id
LEFT JOIN ascendany.pintia_actor_identifiers AS identifier
  ON identifier.actor_id = account.actor_id
 AND identifier.identifier_kind = 'student_number'
 AND identifier.identifier_value = account.student_number
LEFT JOIN ascendany.analytics_head AS head
  ON head.singleton
LEFT JOIN ascendany.analytics_generations AS generation
  ON generation.analytics_generation_id = head.current_generation_id
WHERE account.public_id = $1::uuid
  AND account.auth_revision = $2
  AND account.role = $3
  AND account.disabled_at IS NULL
  AND session.public_id = $4::uuid
  AND session.auth_revision = $2
  AND session.revoked_at IS NULL
  AND session.expires_at > transaction_timestamp()`, query.AccountID, query.ExpectedAuthRevision, string(query.ExpectedRole), query.SessionID).Scan(
		&resolvedAccountID,
		&resolvedRevision,
		&resolvedRole,
		&resolvedSessionID,
		&resolvedSessionRevision,
		&accountActorID,
		&accountStudentNumber,
		&identifierActorID,
		&identifierStudentNumber,
		&headSingleton,
		&generationID,
		&headRevision,
		&generationStatus,
		&baseGenerationID,
		&baseHeadRevision,
		&targetExamID,
		&targetSnapshotID,
		&targetRevision,
		&manifestJSON,
		&manifestSHA256,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return resolvedHead{}, studentAnalyticsError(ErrorPrincipalRejected, "resolve current student principal", errors.New("account, session, revision, role, or student identifier no longer matches"))
	}
	if err != nil {
		return resolvedHead{}, databaseFailure("resolve current student principal and analytics head", err)
	}
	if resolvedAccountID != query.AccountID || resolvedSessionID != query.SessionID ||
		resolvedRevision != query.ExpectedAuthRevision || resolvedSessionRevision != query.ExpectedAuthRevision ||
		auth.Role(resolvedRole) != query.ExpectedRole {
		return resolvedHead{}, storedDataFailure("validate resolved student principal", errors.New("resolved account identity is inconsistent"))
	}
	if accountActorID == nil || *accountActorID <= 0 || accountStudentNumber == nil || strings.TrimSpace(*accountStudentNumber) == "" ||
		identifierActorID == nil || *identifierActorID != *accountActorID || identifierStudentNumber == nil || *identifierStudentNumber != *accountStudentNumber {
		return resolvedHead{}, storedDataFailure("validate resolved student identifier", errors.New("account and student-number identifier are inconsistent"))
	}
	if headSingleton == nil || !*headSingleton {
		return resolvedHead{}, storedDataFailure("validate analytics head singleton", errors.New("analytics head singleton is missing"))
	}
	if headRevision == nil {
		return resolvedHead{}, storedDataFailure("validate analytics head singleton", errors.New("analytics head revision is missing"))
	}
	if generationID == nil {
		if *headRevision != 0 || generationStatus != nil || baseGenerationID != nil || baseHeadRevision != nil || targetExamID != nil || targetSnapshotID != nil || targetRevision != nil || manifestJSON != nil || manifestSHA256 != nil {
			return resolvedHead{}, storedDataFailure("validate empty analytics head", errors.New("empty analytics head has generation metadata or nonzero revision"))
		}
		return resolvedHead{ActorID: *accountActorID}, nil
	}
	if *generationID <= 0 || *headRevision <= 0 || generationStatus == nil || *generationStatus != "succeeded" || baseHeadRevision == nil || targetExamID == nil || targetSnapshotID == nil || targetRevision == nil || manifestJSON == nil || manifestSHA256 == nil {
		return resolvedHead{}, storedDataFailure("validate current analytics head", errors.New("current analytics generation is incomplete or not succeeded"))
	}
	if *baseHeadRevision < 0 || *baseHeadRevision != *headRevision-1 ||
		(baseGenerationID == nil && *baseHeadRevision != 0) ||
		(baseGenerationID != nil && (*baseGenerationID <= 0 || *baseHeadRevision <= 0)) ||
		*targetExamID <= 0 || *targetSnapshotID <= 0 || *targetRevision <= 0 {
		return resolvedHead{}, storedDataFailure("validate current analytics head", errors.New("analytics head and generation scalar columns are inconsistent"))
	}
	return resolvedHead{
		ActorID:          *accountActorID,
		GenerationID:     generationID,
		HeadRevision:     *headRevision,
		BaseGenerationID: baseGenerationID,
		BaseHeadRevision: *baseHeadRevision,
		TargetExamID:     *targetExamID,
		TargetSnapshotID: *targetSnapshotID,
		TargetRevision:   *targetRevision,
		ManifestJSON:     manifestJSON,
		ManifestSHA256:   manifestSHA256,
	}, nil
}

func parseHeadManifest(head resolvedHead) (analytics.ParsedManifest, error) {
	if head.ManifestJSON == nil || head.ManifestSHA256 == nil {
		return analytics.ParsedManifest{}, storedDataFailure("parse current analytics manifest", errors.New("manifest fields are required"))
	}
	manifest, err := analytics.ParseManifest([]byte(*head.ManifestJSON))
	if err != nil {
		return analytics.ParsedManifest{}, storedDataFailure("parse current analytics manifest", err)
	}
	if manifest.SHA256 != *head.ManifestSHA256 {
		return analytics.ParsedManifest{}, storedDataFailure("validate current analytics manifest", errors.New("manifest hash differs from canonical content"))
	}
	if !sameOptionalInt64(manifest.Value.BaseAnalyticsGenerationID, head.BaseGenerationID) ||
		manifest.Value.BaseHeadRevision != head.BaseHeadRevision ||
		manifest.Value.Target.ExamID != head.TargetExamID ||
		manifest.Value.Target.SnapshotID != head.TargetSnapshotID ||
		manifest.Value.Target.ExamHeadRevision != head.TargetRevision {
		return analytics.ParsedManifest{}, storedDataFailure("validate current analytics manifest", errors.New("manifest differs from generation scalar columns"))
	}
	return manifest, nil
}

func sameOptionalInt64(left, right *int64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func validateMetricsAgainstManifest(metrics analytics.StudentMetrics, manifest analytics.ParsedManifest) error {
	byExam := make(map[int64]analytics.ManifestSnapshot, len(manifest.Value.Snapshots))
	for _, snapshot := range manifest.Value.Snapshots {
		byExam[snapshot.ExamID] = snapshot
	}
	for index, point := range metrics.ExamHistory {
		snapshot, exists := byExam[point.ExamID]
		if !exists || snapshot.SnapshotID != point.SnapshotID {
			return fmt.Errorf("examHistory[%d] is outside the current generation manifest", index)
		}
	}
	return nil
}

type historyMetadata struct {
	ExamID           int64
	SnapshotID       int64
	DomainHash       string
	ExamPublicID     string
	SnapshotPublicID string
	Title            string
}

func loadHistoryMetadata(
	ctx context.Context,
	tx readTx,
	generationID int64,
	actorID int64,
	history []analytics.ExamMetricPoint,
	manifest analytics.ParsedManifest,
) ([]historyMetadata, error) {
	if len(history) == 0 {
		return nil, storedDataFailure("load student analytics history metadata", errors.New("canonical analytics row has no observations"))
	}
	examIDs := make([]int64, len(history))
	snapshotIDs := make([]int64, len(history))
	expectedDomains := make([]string, len(history))
	domainByExam := make(map[int64]string, len(manifest.Value.Snapshots))
	for _, snapshot := range manifest.Value.Snapshots {
		domainByExam[snapshot.ExamID] = snapshot.DomainHash
	}
	for index, point := range history {
		examIDs[index] = point.ExamID
		snapshotIDs[index] = point.SnapshotID
		expectedDomains[index] = domainByExam[point.ExamID]
	}
	rows, err := tx.Query(ctx, `
WITH requested AS (
    SELECT exam_id, snapshot_id, ordinal
    FROM unnest($3::bigint[], $4::bigint[]) WITH ORDINALITY
        AS value(exam_id, snapshot_id, ordinal)
)
SELECT requested.ordinal,
       requested.exam_id,
       requested.snapshot_id,
       manifest.domain_hash,
       exam.public_id::text,
       snapshot.public_id::text,
       snapshot.title
FROM requested
JOIN ascendany.analytics_generation_snapshots AS manifest
  ON manifest.analytics_generation_id = $1
 AND manifest.exam_id = requested.exam_id
 AND manifest.snapshot_id = requested.snapshot_id
JOIN ascendany.logical_exams AS exam
  ON exam.exam_id = manifest.exam_id
JOIN ascendany.exam_snapshots AS snapshot
  ON snapshot.snapshot_id = manifest.snapshot_id
 AND snapshot.exam_id = manifest.exam_id
JOIN ascendany.pintia_snapshot_participants AS participant
  ON participant.snapshot_id = manifest.snapshot_id
 AND participant.actor_id = $2
ORDER BY requested.ordinal`, generationID, actorID, examIDs, snapshotIDs)
	if err != nil {
		return nil, databaseFailure("query student analytics history metadata", err)
	}
	defer rows.Close()
	metadata := make([]historyMetadata, 0, len(history))
	for rows.Next() {
		var ordinal int64
		var item historyMetadata
		if err := rows.Scan(
			&ordinal,
			&item.ExamID,
			&item.SnapshotID,
			&item.DomainHash,
			&item.ExamPublicID,
			&item.SnapshotPublicID,
			&item.Title,
		); err != nil {
			return nil, databaseFailure("scan student analytics history metadata", err)
		}
		index := len(metadata)
		if index >= len(history) || ordinal != int64(index+1) || item.ExamID != history[index].ExamID || item.SnapshotID != history[index].SnapshotID || item.DomainHash != expectedDomains[index] ||
			!canonicalUUIDv4Pattern.MatchString(item.ExamPublicID) || !canonicalUUIDv4Pattern.MatchString(item.SnapshotPublicID) || strings.TrimSpace(item.Title) == "" {
			return nil, storedDataFailure("validate student analytics history metadata", fmt.Errorf("metadata row %d is out of order or invalid", index))
		}
		metadata = append(metadata, item)
	}
	if err := rows.Err(); err != nil {
		return nil, databaseFailure("iterate student analytics history metadata", err)
	}
	if len(metadata) != len(history) {
		return nil, storedDataFailure("validate student analytics history metadata", fmt.Errorf("loaded %d metadata rows for %d requested observations", len(metadata), len(history)))
	}
	return metadata, nil
}

func buildReadyResult(
	rating int64,
	metrics analytics.StudentMetrics,
	start int,
	metadata []historyMetadata,
) ReadyResult {
	examHistory := make([]ExamHistoryPoint, len(metadata))
	ratingHistory := make([]RatingHistoryPoint, len(metadata))
	for index, item := range metadata {
		exam := metrics.ExamHistory[start+index]
		ratingPoint := metrics.RatingHistory[start+index]
		examHistory[index] = ExamHistoryPoint{
			ExamID:     item.ExamPublicID,
			SnapshotID: item.SnapshotPublicID,
			Title:      item.Title,
			EventTime:  exam.EventTime,
			Values:     exam.Values,
		}
		ratingHistory[index] = RatingHistoryPoint{
			ExamID:      item.ExamPublicID,
			SnapshotID:  item.SnapshotPublicID,
			Title:       item.Title,
			EventTime:   ratingPoint.EventTime,
			Rank:        ratingPoint.Rank,
			OldRating:   ratingPoint.OldRating,
			Delta:       ratingPoint.Delta,
			NewRating:   ratingPoint.NewRating,
			Seed:        ratingPoint.Seed,
			Performance: ratingPoint.Performance,
		}
	}
	return ReadyResult{
		ReferenceTime: metrics.ReferenceTime,
		Rating:        rating,
		Current:       metrics.Current,
		ExamHistory:   examHistory,
		RatingHistory: ratingHistory,
	}
}
