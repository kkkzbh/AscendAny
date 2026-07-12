package achievement

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/kkkzbh/AscendAny/backend/internal/analytics"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
	"github.com/kkkzbh/AscendAny/backend/internal/principalguard"
)

var canonicalNonnegativeDecimal = regexp.MustCompile(`^(0|[1-9][0-9]*)([.][0-9]+)?$`)

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
		return nil, achievementError(ErrorInvalidConfiguration, "construct achievement PostgreSQL repository", errors.New("database pool is required"))
	}
	return &PostgresRepository{begin: func(ctx context.Context, options pgx.TxOptions) (readTx, error) {
		return pool.BeginTx(ctx, options)
	}}, nil
}

func newPostgresRepository(begin beginReadTransaction) (*PostgresRepository, error) {
	if begin == nil {
		return nil, achievementError(ErrorInvalidConfiguration, "construct achievement PostgreSQL repository", errors.New("transaction beginner is required"))
	}
	return &PostgresRepository{begin: begin}, nil
}

func (repository *PostgresRepository) LoadSelf(ctx context.Context, query SelfQuery) (RepositorySnapshot, error) {
	if err := validateSelfQuery(ctx, query); err != nil {
		return RepositorySnapshot{}, err
	}
	var snapshot RepositorySnapshot
	err := repository.readTransaction(ctx, "load student achievements", func(tx readTx) error {
		resolved, err := principalguard.Resolve(ctx, tx, query.Principal, principalguard.Roles(auth.RoleStudent))
		if err != nil {
			return mapPrincipalError(err)
		}
		if resolved.ActorID == nil || *resolved.ActorID <= 0 {
			return storedDataFailure("resolve achievement student actor", errors.New("student principal has no actor binding"))
		}

		var generationID *int64
		var generationStatus *string
		var ruleSetID int64
		err = tx.QueryRow(ctx, `
SELECT analytics_head.current_generation_id,
       analytics_head.head_revision,
       generation.status,
       rule_head.current_rule_set_id,
       rule_head.head_revision,
       rule_set.version
FROM ascendany.analytics_head AS analytics_head
LEFT JOIN ascendany.analytics_generations AS generation
  ON generation.analytics_generation_id = analytics_head.current_generation_id
JOIN ascendany.achievement_rule_head AS rule_head
  ON rule_head.singleton
JOIN ascendany.achievement_rule_sets AS rule_set
  ON rule_set.achievement_rule_set_id = rule_head.current_rule_set_id
WHERE analytics_head.singleton`).Scan(
			&generationID,
			&snapshot.AnalyticsHeadRevision,
			&generationStatus,
			&ruleSetID,
			&snapshot.RuleHeadRevision,
			&snapshot.RuleSetVersion,
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return storedDataFailure("load achievement and analytics heads", errors.New("required singleton head is missing"))
		}
		if err != nil {
			return databaseFailure("load achievement and analytics heads", err)
		}
		if ruleSetID <= 0 || snapshot.RuleHeadRevision <= 0 || snapshot.RuleSetVersion <= 0 {
			return storedDataFailure("validate active achievement rule head", errors.New("rule head scalar columns are invalid"))
		}
		if generationID == nil {
			if snapshot.AnalyticsHeadRevision != 0 || generationStatus != nil {
				return storedDataFailure("validate empty analytics head", errors.New("empty analytics head has generation metadata or nonzero revision"))
			}
		} else if *generationID <= 0 || snapshot.AnalyticsHeadRevision <= 0 || generationStatus == nil || *generationStatus != "succeeded" {
			return storedDataFailure("validate current analytics head", errors.New("current analytics generation is invalid or incomplete"))
		}

		rules, err := loadRules(ctx, tx, ruleSetID)
		if err != nil {
			return err
		}
		snapshot.Rules = rules

		err = tx.QueryRow(ctx, `
SELECT count(*)::bigint
FROM ascendany.agent_runs
WHERE owner_account_id = $1
  AND run_kind = 'reply'
  AND status = 'succeeded'`, resolved.AccountDatabaseID).Scan(&snapshot.AIDialogueCount)
		if err != nil {
			return databaseFailure("count successful student reply runs", err)
		}
		if snapshot.AIDialogueCount < 0 {
			return storedDataFailure("validate successful student reply count", errors.New("database returned a negative count"))
		}

		if generationID == nil {
			return nil
		}
		var metricsJSON string
		err = tx.QueryRow(ctx, `
SELECT metrics::text
FROM ascendany.student_analytics
WHERE analytics_generation_id = $1
  AND actor_id = $2`, *generationID, *resolved.ActorID).Scan(&metricsJSON)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return databaseFailure("load current student achievement metrics", err)
		}
		metrics, err := analytics.DecodeStoredStudentMetrics([]byte(metricsJSON))
		if err != nil {
			return storedDataFailure("decode current student achievement metrics", err)
		}
		snapshot.Metrics = &metrics
		return nil
	})
	if err != nil {
		return RepositorySnapshot{}, err
	}
	return snapshot, nil
}

func loadRules(ctx context.Context, tx readTx, ruleSetID int64) ([]Rule, error) {
	rows, err := tx.Query(ctx, `
SELECT achievement_code,
       title,
       description,
       progress_key,
       bronze_target::text,
       silver_target::text,
       gold_target::text,
       sort_order
FROM ascendany.achievement_rules
WHERE achievement_rule_set_id = $1
ORDER BY sort_order ASC, achievement_code ASC`, ruleSetID)
	if err != nil {
		return nil, databaseFailure("query active achievement rules", err)
	}
	defer rows.Close()
	rules := make([]Rule, 0, 32)
	for rows.Next() {
		var rule Rule
		var progressKey string
		var bronzeText, silverText, goldText string
		if err := rows.Scan(
			&rule.Code,
			&rule.Title,
			&rule.Description,
			&progressKey,
			&bronzeText,
			&silverText,
			&goldText,
			&rule.SortOrder,
		); err != nil {
			return nil, databaseFailure("scan active achievement rule", err)
		}
		rule.ProgressKey = ProgressKey(progressKey)
		rule.BronzeTarget, err = parseCanonicalTarget(bronzeText)
		if err == nil {
			rule.SilverTarget, err = parseCanonicalTarget(silverText)
		}
		if err == nil {
			rule.GoldTarget, err = parseCanonicalTarget(goldText)
		}
		if err != nil {
			return nil, storedDataFailure("parse active achievement rule thresholds", err)
		}
		rules = append(rules, rule)
	}
	if err := rows.Err(); err != nil {
		return nil, databaseFailure("iterate active achievement rules", err)
	}
	return rules, nil
}

func parseCanonicalTarget(raw string) (float64, error) {
	if len(raw) == 0 || len(raw) > 128 || !canonicalNonnegativeDecimal.MatchString(raw) {
		return 0, errors.New("achievement target is not a canonical non-negative decimal")
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || !finiteNonnegative(value) {
		return 0, errors.New("achievement target is outside the supported numeric range")
	}
	return value, nil
}

func mapPrincipalError(err error) error {
	switch principalguard.CodeOf(err) {
	case principalguard.ErrorRejected, principalguard.ErrorInvalidPrincipal:
		return achievementError(ErrorPrincipalRejected, "revalidate achievement principal", err)
	case principalguard.ErrorCanceled:
		return achievementError(ErrorCanceled, "revalidate achievement principal", err)
	case principalguard.ErrorDatabase:
		return achievementError(ErrorDatabase, "revalidate achievement principal", err)
	default:
		return achievementError(ErrorStoredDataInvalid, "revalidate achievement principal", err)
	}
}

func (repository *PostgresRepository) readTransaction(
	ctx context.Context,
	operation string,
	run func(readTx) error,
) (resultErr error) {
	tx, err := repository.begin(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
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
