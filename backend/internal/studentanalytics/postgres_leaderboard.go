package studentanalytics

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/kkkzbh/AscendAny/backend/internal/analytics"
	"github.com/kkkzbh/AscendAny/backend/internal/auth"
)

func (repository *PostgresRepository) LoadLeaderboard(
	ctx context.Context,
	query LeaderboardQuery,
) (LeaderboardResult, error) {
	if err := validateLeaderboardQuery(ctx, query); err != nil {
		return LeaderboardResult{}, err
	}
	var result LeaderboardResult
	err := repository.readTransaction(ctx, "load student leaderboard", func(tx readTx) error {
		resolved, err := resolvePrincipalAndHead(ctx, tx, SelfQuery{
			AccountID:            query.AccountID,
			SessionID:            query.SessionID,
			ExpectedAuthRevision: query.ExpectedAuthRevision,
			ExpectedRole:         query.ExpectedRole,
			HistoryLimit:         1,
		})
		if err != nil {
			return err
		}
		if resolved.GenerationID == nil {
			result = LeaderboardResult{State: StateNotGenerated}
			return nil
		}
		manifest, err := parseHeadManifest(resolved)
		if err != nil {
			return err
		}

		rows, err := tx.Query(ctx, `
WITH eligible AS (
    SELECT analytics.actor_id,
           identifier.identifier_value AS student_number,
           COALESCE(account.display_name, participant.display_name) AS display_name,
           analytics.rating,
           analytics.metrics
    FROM ascendany.student_analytics AS analytics
    JOIN ascendany.pintia_actor_identifiers AS identifier
      ON identifier.actor_id = analytics.actor_id
     AND identifier.identifier_kind = 'student_number'
    LEFT JOIN ascendany.auth_accounts AS account
      ON account.actor_id = analytics.actor_id
     AND account.role = 'student'
     AND account.disabled_at IS NULL
    LEFT JOIN LATERAL (
        SELECT snapshot_participant.display_name
        FROM ascendany.analytics_generation_snapshots AS generation_snapshot
        JOIN ascendany.pintia_snapshot_participants AS snapshot_participant
          ON snapshot_participant.snapshot_id = generation_snapshot.snapshot_id
         AND snapshot_participant.actor_id = analytics.actor_id
        WHERE generation_snapshot.analytics_generation_id = analytics.analytics_generation_id
          AND snapshot_participant.display_name IS NOT NULL
        ORDER BY generation_snapshot.snapshot_id DESC
        LIMIT 1
    ) AS participant ON true
    WHERE analytics.analytics_generation_id = $1
), ranked AS (
    SELECT actor_id,
           student_number,
           display_name,
           rating,
           metrics,
           rank() OVER (ORDER BY rating DESC) AS leaderboard_rank,
           count(*) OVER () AS population
    FROM eligible
)
SELECT student_number,
       display_name,
       rating::text,
       metrics::text,
       leaderboard_rank,
       population
FROM ranked
ORDER BY rating DESC, student_number ASC, actor_id ASC
LIMIT $2`, *resolved.GenerationID, query.Limit)
		if err != nil {
			return databaseFailure("query student leaderboard", err)
		}
		defer rows.Close()

		items := make([]LeaderboardItem, 0, query.Limit)
		var population int64
		for rows.Next() {
			var item LeaderboardItem
			var ratingText string
			var metricsText string
			var rowPopulation int64
			if err := rows.Scan(
				&item.StudentNumber,
				&item.DisplayName,
				&ratingText,
				&metricsText,
				&item.Rank,
				&rowPopulation,
			); err != nil {
				return databaseFailure("scan student leaderboard", err)
			}
			if rowPopulation <= 0 || population != 0 && population != rowPopulation {
				return storedDataFailure("validate student leaderboard population", errors.New("window population is invalid or inconsistent"))
			}
			population = rowPopulation
			item.Rating, err = parseCanonicalRating(ratingText)
			if err != nil {
				return storedDataFailure("validate student leaderboard rating", err)
			}
			metrics, err := analytics.DecodeStoredStudentMetrics([]byte(metricsText))
			if err != nil {
				return storedDataFailure("decode student leaderboard metrics", err)
			}
			if err := validateMetricsAgainstManifest(metrics, manifest); err != nil {
				return storedDataFailure("validate student leaderboard generation membership", err)
			}
			if len(metrics.RatingHistory) == 0 || item.Rating != metrics.RatingHistory[len(metrics.RatingHistory)-1].NewRating {
				return storedDataFailure("validate student leaderboard rating", errors.New("rating differs from final history point"))
			}
			item.Metrics = metrics.Current
			if strings.TrimSpace(item.StudentNumber) != item.StudentNumber || item.StudentNumber == "" ||
				len(item.StudentNumber) > auth.MaxStudentNumberBytes {
				return storedDataFailure("validate student leaderboard identity", errors.New("student number is not canonical"))
			}
			if item.DisplayName != nil && (strings.TrimSpace(*item.DisplayName) != *item.DisplayName || *item.DisplayName == "" ||
				len(*item.DisplayName) > auth.MaxDisplayNameBytes) {
				return storedDataFailure("validate student leaderboard identity", errors.New("display name is not canonical"))
			}
			items = append(items, item)
		}
		if err := rows.Err(); err != nil {
			return databaseFailure("iterate student leaderboard", err)
		}
		if len(items) == 0 {
			result = LeaderboardResult{State: StateNoObservations, HeadRevision: resolved.HeadRevision}
			return nil
		}
		if population < int64(len(items)) {
			return storedDataFailure("validate student leaderboard population", fmt.Errorf("population %d is smaller than page size %d", population, len(items)))
		}
		result = LeaderboardResult{
			State:        StateReady,
			HeadRevision: resolved.HeadRevision,
			Population:   population,
			Items:        items,
		}
		return nil
	})
	if err != nil {
		return LeaderboardResult{}, err
	}
	return result, nil
}
