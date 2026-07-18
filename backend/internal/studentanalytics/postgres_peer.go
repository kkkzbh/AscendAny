package studentanalytics

import (
	"context"
	"errors"
	"math"

	"github.com/jackc/pgx/v5"
	"github.com/kkkzbh/AscendAny/backend/internal/analytics"
)

func loadLatestExamPeer(
	ctx context.Context,
	tx readTx,
	generationID, actorID, examID, snapshotID int64,
) (*LatestExamPeer, error) {
	var peer LatestExamPeer
	var expectedParticipants int64
	var previousPosition, previousRank *int64
	var previousScore *float64
	var previousSolved *int64
	var previousValues analytics.MetricValues
	err := tx.QueryRow(ctx, `
WITH base AS (
    SELECT participant.actor_id,
           (rating.value ->> 'rank')::bigint AS rank,
           ranking.total_score::double precision AS score,
           count(result.problem_set_problem_id) FILTER (WHERE result.passed IS TRUE)::bigint AS solved,
           (metric.values ->> 'knowledge')::double precision AS knowledge,
           (metric.values ->> 'accuracy')::double precision AS accuracy,
           (metric.values ->> 'quality')::double precision AS quality,
           (metric.values ->> 'flexibility')::double precision AS flexibility,
           (metric.values ->> 'proficiency')::double precision AS proficiency,
           snapshot.participants_exported_count AS expected_participants
    FROM ascendany.exam_snapshots AS snapshot
    JOIN ascendany.pintia_snapshot_participants AS participant
      ON participant.snapshot_id = snapshot.snapshot_id
    JOIN ascendany.student_analytics AS student
      ON student.analytics_generation_id = $1
     AND student.actor_id = participant.actor_id
    JOIN LATERAL (
        SELECT point.value -> 'values' AS values
        FROM jsonb_array_elements(student.metrics -> 'examHistory') AS point(value)
        WHERE (point.value ->> 'examId')::bigint = $3
          AND (point.value ->> 'snapshotId')::bigint = $4
    ) AS metric ON true
    JOIN LATERAL (
        SELECT point.value
        FROM jsonb_array_elements(student.metrics -> 'ratingHistory') AS point(value)
        WHERE (point.value ->> 'examId')::bigint = $3
          AND (point.value ->> 'snapshotId')::bigint = $4
    ) AS rating ON true
    LEFT JOIN ascendany.pintia_rankings AS ranking
      ON ranking.snapshot_id = participant.snapshot_id
     AND ranking.actor_id = participant.actor_id
    LEFT JOIN ascendany.pintia_ranking_problem_results AS result
      ON result.snapshot_id = participant.snapshot_id
     AND result.actor_id = participant.actor_id
    WHERE snapshot.snapshot_id = $4
      AND snapshot.exam_id = $3
    GROUP BY participant.actor_id, rating.value, ranking.total_score, metric.values,
             snapshot.participants_exported_count
), ranked AS (
    SELECT base.*,
           row_number() OVER (ORDER BY rank ASC, score DESC NULLS LAST, actor_id ASC)::bigint AS position,
           count(*) OVER ()::bigint AS total
    FROM base
), self AS (
    SELECT * FROM ranked WHERE actor_id = $2
), first_thresholds AS (
    SELECT self.*,
           greatest(1::bigint, ceil(total::numeric * 0.1)::bigint) AS top_10_end
    FROM self
), thresholds AS (
    SELECT first_thresholds.*,
           greatest(top_10_end + 1, ceil(total::numeric * 0.3)::bigint) AS top_30_end
    FROM first_thresholds
), bounds AS (
    SELECT thresholds.*,
           greatest(top_30_end + 1, ceil(total::numeric * 0.7)::bigint) AS median_end
    FROM thresholds
), selected_band AS (
    SELECT ranked.*
    FROM ranked
    CROSS JOIN bounds
    WHERE ranked.position BETWEEN
        CASE
            WHEN bounds.position <= bounds.top_10_end THEN 1
            WHEN bounds.position <= bounds.top_30_end THEN bounds.top_10_end + 1
            WHEN bounds.position <= bounds.median_end THEN bounds.top_30_end + 1
            ELSE bounds.median_end + 1
        END
        AND
        CASE
            WHEN bounds.position <= bounds.top_10_end THEN bounds.top_10_end
            WHEN bounds.position <= bounds.top_30_end THEN bounds.top_30_end
            WHEN bounds.position <= bounds.median_end THEN bounds.median_end
            ELSE bounds.total
        END
), medians AS (
    SELECT percentile_cont(0.5) WITHIN GROUP (ORDER BY score)
               FILTER (WHERE score IS NOT NULL) AS score,
           percentile_cont(0.5) WITHIN GROUP (ORDER BY solved) AS solved,
           percentile_cont(0.5) WITHIN GROUP (ORDER BY knowledge)
               FILTER (WHERE knowledge IS NOT NULL) AS knowledge,
           percentile_cont(0.5) WITHIN GROUP (ORDER BY accuracy)
               FILTER (WHERE accuracy IS NOT NULL) AS accuracy,
           percentile_cont(0.5) WITHIN GROUP (ORDER BY quality)
               FILTER (WHERE quality IS NOT NULL) AS quality,
           percentile_cont(0.5) WITHIN GROUP (ORDER BY flexibility)
               FILTER (WHERE flexibility IS NOT NULL) AS flexibility,
           percentile_cont(0.5) WITHIN GROUP (ORDER BY proficiency)
               FILTER (WHERE proficiency IS NOT NULL) AS proficiency
    FROM selected_band
), previous AS (
    SELECT ranked.*
    FROM ranked
    CROSS JOIN self
    WHERE ranked.position = self.position - 1
)
SELECT self.total,
       self.expected_participants,
       self.position,
       self.rank,
       self.score,
       self.solved,
       medians.score,
       medians.solved,
       medians.knowledge,
       medians.accuracy,
       medians.quality,
       medians.flexibility,
       medians.proficiency,
       previous.position,
       previous.rank,
       previous.score,
       previous.solved,
       previous.knowledge,
       previous.accuracy,
       previous.quality,
       previous.flexibility,
       previous.proficiency
FROM self
CROSS JOIN medians
LEFT JOIN previous ON true`, generationID, actorID, examID, snapshotID).Scan(
		&peer.TotalParticipants,
		&expectedParticipants,
		&peer.Position,
		&peer.Rank,
		&peer.Score,
		&peer.Solved,
		&peer.BandMedian.Score,
		&peer.BandMedian.Solved,
		&peer.BandMedian.Values.Knowledge,
		&peer.BandMedian.Values.Accuracy,
		&peer.BandMedian.Values.Quality,
		&peer.BandMedian.Values.Flexibility,
		&peer.BandMedian.Values.Proficiency,
		&previousPosition,
		&previousRank,
		&previousScore,
		&previousSolved,
		&previousValues.Knowledge,
		&previousValues.Accuracy,
		&previousValues.Quality,
		&previousValues.Flexibility,
		&previousValues.Proficiency,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, databaseFailure("load latest exam peer context", err)
	}
	if expectedParticipants <= 0 || peer.TotalParticipants != expectedParticipants {
		return nil, storedDataFailure("validate latest exam peer context", errors.New("peer population differs from the immutable snapshot participant count"))
	}
	if previousPosition != nil {
		if previousRank == nil || previousSolved == nil {
			return nil, storedDataFailure("validate latest exam peer context", errors.New("previous participant is incomplete"))
		}
		peer.Previous = &PeerParticipant{
			Position: *previousPosition,
			Rank:     *previousRank,
			Score:    previousScore,
			Solved:   *previousSolved,
			Values:   previousValues,
		}
	} else if previousRank != nil || previousScore != nil || previousSolved != nil || metricValuesPresent(previousValues) {
		return nil, storedDataFailure("validate latest exam peer context", errors.New("previous participant columns are inconsistent"))
	}
	if err := validateLatestExamPeer(peer); err != nil {
		return nil, storedDataFailure("validate latest exam peer context", err)
	}
	return &peer, nil
}

func validateLatestExamPeer(peer LatestExamPeer) error {
	if peer.TotalParticipants <= 0 || peer.Position <= 0 || peer.Position > peer.TotalParticipants ||
		peer.Rank <= 0 || peer.Solved < 0 ||
		!validOptionalNonnegativeFloat(peer.Score) || !validOptionalNonnegativeFloat(peer.BandMedian.Score) ||
		peer.BandMedian.Solved == nil || !validOptionalNonnegativeFloat(peer.BandMedian.Solved) ||
		validateMetricValues(peer.BandMedian.Values) != nil {
		return errors.New("self or band values are invalid")
	}
	if (peer.Position == 1) != (peer.Previous == nil) {
		return errors.New("previous participant presence differs from self position")
	}
	if peer.Previous != nil {
		previous := peer.Previous
		if previous.Position != peer.Position-1 || previous.Rank <= 0 || previous.Solved < 0 ||
			!validOptionalNonnegativeFloat(previous.Score) || validateMetricValues(previous.Values) != nil {
			return errors.New("previous participant values are invalid")
		}
	}
	return nil
}

func validOptionalNonnegativeFloat(value *float64) bool {
	return value == nil || !math.IsNaN(*value) && !math.IsInf(*value, 0) && *value >= 0
}

func metricValuesPresent(values analytics.MetricValues) bool {
	return values.Knowledge != nil || values.Accuracy != nil || values.Quality != nil ||
		values.Flexibility != nil || values.Proficiency != nil
}
