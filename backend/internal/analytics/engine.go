package analytics

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"
)

const (
	StudentMetricsProtocolV1 = "student_analytics_v1"
	problemMetricsProtocolV1 = "problem_analytics_v1"
)

type snapshotComputation struct {
	snapshot  SnapshotData
	eventTime time.Time
	metrics   map[int64]MetricValues
	ranks     map[int64]int64
	problems  []ProblemResult
}

type ratingComputation struct {
	actorID     int64
	rank        int64
	oldRating   int64
	delta       int64
	newRating   int64
	seed        float64
	performance float64
}

func Compute(ctx context.Context, configuration Config, dataset Dataset) (Result, error) {
	if ctx == nil {
		return Result{}, analyticsError(ErrorInvalidConfiguration, true, "compute", errors.New("context is required"))
	}
	if err := computationContextError(ctx); err != nil {
		return Result{}, err
	}
	configurationCopy := configuration
	configurationCopy.AcceptedVerdicts = append([]string(nil), configuration.AcceptedVerdicts...)
	if err := validateConfig(&configurationCopy); err != nil {
		return Result{}, analyticsError(ErrorInvalidConfiguration, true, "compute", err)
	}
	if err := computationContextError(ctx); err != nil {
		return Result{}, err
	}
	snapshots, err := validateAndOrderDataset(ctx, dataset)
	if err != nil {
		if _, ok := CodeOf(err); ok {
			return Result{}, err
		}
		return Result{}, analyticsError(ErrorInvalidDataset, true, "compute", err)
	}
	if err := computationContextError(ctx); err != nil {
		return Result{}, err
	}

	accepted := make(map[string]struct{}, len(configurationCopy.AcceptedVerdicts))
	for _, verdict := range configurationCopy.AcceptedVerdicts {
		accepted[verdict] = struct{}{}
	}
	computations := make([]snapshotComputation, 0, len(snapshots))
	for _, snapshot := range snapshots {
		metrics, ranks, problemResults, err := computeSnapshot(ctx, snapshot.snapshot, accepted, configurationCopy.Winsor)
		if err != nil {
			return Result{}, err
		}
		computations = append(computations, snapshotComputation{
			snapshot:  snapshot.snapshot,
			eventTime: snapshot.eventTime,
			metrics:   metrics,
			ranks:     ranks,
			problems:  problemResults,
		})
	}

	currentRatings := make(map[int64]int64)
	metricHistory := make(map[int64][]ExamMetricPoint)
	ratingHistory := make(map[int64][]RatingHistoryPoint)
	actorSet := make(map[int64]struct{})
	problemResults := make([]ProblemResult, 0)
	referenceTime := computations[len(computations)-1].eventTime

	for _, computation := range computations {
		if err := computationContextError(ctx); err != nil {
			return Result{}, err
		}
		for actorID, values := range computation.metrics {
			actorSet[actorID] = struct{}{}
			metricHistory[actorID] = append(metricHistory[actorID], ExamMetricPoint{
				ExamID:     computation.snapshot.ExamID,
				SnapshotID: computation.snapshot.SnapshotID,
				EventTime:  computation.eventTime,
				Values:     values,
			})
		}
		ratings, err := computeRatings(ctx, computation, currentRatings, configurationCopy.Rating)
		if err != nil {
			return Result{}, err
		}
		for _, rating := range ratings {
			actorSet[rating.actorID] = struct{}{}
			currentRatings[rating.actorID] = rating.newRating
			ratingHistory[rating.actorID] = append(ratingHistory[rating.actorID], RatingHistoryPoint{
				ExamID:      computation.snapshot.ExamID,
				SnapshotID:  computation.snapshot.SnapshotID,
				EventTime:   computation.eventTime,
				Rank:        rating.rank,
				OldRating:   rating.oldRating,
				Delta:       rating.delta,
				NewRating:   rating.newRating,
				Seed:        round6(rating.seed),
				Performance: round6(rating.performance),
			})
		}
		problemResults = append(problemResults, computation.problems...)
	}

	actorIDs := make([]int64, 0, len(actorSet))
	for actorID := range actorSet {
		actorIDs = append(actorIDs, actorID)
	}
	sort.Slice(actorIDs, func(i, j int) bool { return actorIDs[i] < actorIDs[j] })
	students := make([]StudentResult, 0, len(actorIDs))
	for _, actorID := range actorIDs {
		if err := computationContextError(ctx); err != nil {
			return Result{}, err
		}
		history := metricHistory[actorID]
		students = append(students, StudentResult{
			ActorID: actorID,
			Rating:  ratingOrInitial(currentRatings, actorID, configurationCopy.Rating.Initial),
			Metrics: StudentMetrics{
				Protocol:      StudentMetricsProtocolV1,
				ReferenceTime: referenceTime,
				Current:       fuseMetrics(history, referenceTime, configurationCopy.HalfLifeDays),
				ExamHistory:   history,
				RatingHistory: ratingHistory[actorID],
			},
		})
	}
	sort.Slice(problemResults, func(i, j int) bool {
		if problemResults[i].SnapshotID != problemResults[j].SnapshotID {
			return problemResults[i].SnapshotID < problemResults[j].SnapshotID
		}
		return problemResults[i].ProblemSetProblemID < problemResults[j].ProblemSetProblemID
	})
	return Result{ReferenceTime: referenceTime, Students: students, Problems: problemResults}, nil
}

func computationContextError(ctx context.Context) error {
	cause := context.Cause(ctx)
	if cause == nil {
		return nil
	}
	return analyticsError(ErrorCanceled, false, "compute", cause)
}

type orderedSnapshot struct {
	snapshot  SnapshotData
	eventTime time.Time
}

func validateAndOrderDataset(ctx context.Context, dataset Dataset) ([]orderedSnapshot, error) {
	if err := computationContextError(ctx); err != nil {
		return nil, err
	}
	if len(dataset.Snapshots) == 0 {
		return nil, errors.New("dataset must contain at least one snapshot")
	}
	ordered := make([]orderedSnapshot, 0, len(dataset.Snapshots))
	seenExams := make(map[int64]struct{}, len(dataset.Snapshots))
	seenSnapshots := make(map[int64]struct{}, len(dataset.Snapshots))
	seenSubmissionIDs := make(map[int64]struct{})
	for snapshotIndex, snapshot := range dataset.Snapshots {
		if err := computationContextError(ctx); err != nil {
			return nil, err
		}
		if snapshot.ExamID <= 0 || snapshot.SnapshotID <= 0 || !lowercaseSHA256Pattern.MatchString(snapshot.DomainHash) {
			return nil, fmt.Errorf("snapshot[%d] has invalid identity", snapshotIndex)
		}
		if _, exists := seenExams[snapshot.ExamID]; exists {
			return nil, fmt.Errorf("exam ID %d is duplicated", snapshot.ExamID)
		}
		seenExams[snapshot.ExamID] = struct{}{}
		if _, exists := seenSnapshots[snapshot.SnapshotID]; exists {
			return nil, fmt.Errorf("snapshot ID %d is duplicated", snapshot.SnapshotID)
		}
		seenSnapshots[snapshot.SnapshotID] = struct{}{}
		if snapshot.ExpectedProblems != int64(len(snapshot.Problems)) || snapshot.ExpectedParticipants != int64(len(snapshot.Participants)) || snapshot.ExpectedSubmissions != int64(len(snapshot.Submissions)) {
			return nil, fmt.Errorf("snapshot %d exported counts differ from loaded rows", snapshot.SnapshotID)
		}
		if snapshot.TotalScore != nil && (!finite(*snapshot.TotalScore) || *snapshot.TotalScore < 0) {
			return nil, fmt.Errorf("snapshot %d has invalid total score", snapshot.SnapshotID)
		}
		problemIDs := make(map[string]struct{}, len(snapshot.Problems))
		for _, problem := range snapshot.Problems {
			if err := computationContextError(ctx); err != nil {
				return nil, err
			}
			if problem.ProblemSetProblemID == "" {
				return nil, fmt.Errorf("snapshot %d has an empty problem ID", snapshot.SnapshotID)
			}
			if _, exists := problemIDs[problem.ProblemSetProblemID]; exists {
				return nil, fmt.Errorf("snapshot %d duplicates problem %q", snapshot.SnapshotID, problem.ProblemSetProblemID)
			}
			problemIDs[problem.ProblemSetProblemID] = struct{}{}
			if problem.MaxScore != nil && (!finite(*problem.MaxScore) || *problem.MaxScore < 0) {
				return nil, fmt.Errorf("snapshot %d problem %q has invalid max score", snapshot.SnapshotID, problem.ProblemSetProblemID)
			}
		}
		actorIDs := make(map[int64]struct{}, len(snapshot.Participants))
		rankingCount := int64(0)
		for _, participant := range snapshot.Participants {
			if err := computationContextError(ctx); err != nil {
				return nil, err
			}
			if participant.ActorID <= 0 {
				return nil, fmt.Errorf("snapshot %d has invalid actor ID", snapshot.SnapshotID)
			}
			if _, exists := actorIDs[participant.ActorID]; exists {
				return nil, fmt.Errorf("snapshot %d duplicates actor %d", snapshot.SnapshotID, participant.ActorID)
			}
			actorIDs[participant.ActorID] = struct{}{}
			if participant.Ranking != nil {
				rankingCount++
				if participant.Ranking.Rank <= 0 || (participant.Ranking.TotalScore != nil && (!finite(*participant.Ranking.TotalScore) || *participant.Ranking.TotalScore < 0)) || (participant.Ranking.TimeUsedSeconds != nil && *participant.Ranking.TimeUsedSeconds < 0) {
					return nil, fmt.Errorf("snapshot %d actor %d has invalid ranking", snapshot.SnapshotID, participant.ActorID)
				}
			}
			seenResults := make(map[string]struct{}, len(participant.ProblemResults))
			for _, result := range participant.ProblemResults {
				if err := computationContextError(ctx); err != nil {
					return nil, err
				}
				if _, exists := problemIDs[result.ProblemSetProblemID]; !exists {
					return nil, fmt.Errorf("snapshot %d actor %d references unknown ranking problem", snapshot.SnapshotID, participant.ActorID)
				}
				if _, exists := seenResults[result.ProblemSetProblemID]; exists {
					return nil, fmt.Errorf("snapshot %d actor %d duplicates ranking problem", snapshot.SnapshotID, participant.ActorID)
				}
				seenResults[result.ProblemSetProblemID] = struct{}{}
				if result.Score != nil && (!finite(*result.Score) || *result.Score < 0) {
					return nil, fmt.Errorf("snapshot %d actor %d has invalid problem score", snapshot.SnapshotID, participant.ActorID)
				}
				if result.ValidSubmissionCount != nil && *result.ValidSubmissionCount < 0 {
					return nil, fmt.Errorf("snapshot %d actor %d has invalid submission count", snapshot.SnapshotID, participant.ActorID)
				}
			}
		}
		if snapshot.ExpectedRankings != rankingCount {
			return nil, fmt.Errorf("snapshot %d ranking count differs from loaded rows", snapshot.SnapshotID)
		}
		for _, submission := range snapshot.Submissions {
			if err := computationContextError(ctx); err != nil {
				return nil, err
			}
			if submission.SubmissionIdentityID <= 0 || submission.ActorID <= 0 || submission.ProblemSetProblemID == "" || submission.SubmittedAt.IsZero() {
				return nil, fmt.Errorf("snapshot %d has invalid submission identity", snapshot.SnapshotID)
			}
			if _, exists := seenSubmissionIDs[submission.SubmissionIdentityID]; exists {
				return nil, fmt.Errorf("submission identity %d is duplicated", submission.SubmissionIdentityID)
			}
			seenSubmissionIDs[submission.SubmissionIdentityID] = struct{}{}
			if _, exists := actorIDs[submission.ActorID]; !exists {
				return nil, fmt.Errorf("snapshot %d submission references unknown actor", snapshot.SnapshotID)
			}
			if _, exists := problemIDs[submission.ProblemSetProblemID]; !exists {
				return nil, fmt.Errorf("snapshot %d submission references unknown problem", snapshot.SnapshotID)
			}
			if submission.Score != nil && (!finite(*submission.Score) || *submission.Score < 0) {
				return nil, fmt.Errorf("snapshot %d submission has invalid score", snapshot.SnapshotID)
			}
			if submission.TimeMS != nil && *submission.TimeMS < 0 || submission.MemoryBytes != nil && *submission.MemoryBytes < 0 {
				return nil, fmt.Errorf("snapshot %d submission has invalid resource usage", snapshot.SnapshotID)
			}
		}
		eventTime, err := snapshotEventTime(snapshot)
		if err != nil {
			return nil, err
		}
		ordered = append(ordered, orderedSnapshot{snapshot: snapshot, eventTime: eventTime})
	}
	sort.Slice(ordered, func(i, j int) bool {
		if !ordered[i].eventTime.Equal(ordered[j].eventTime) {
			return ordered[i].eventTime.Before(ordered[j].eventTime)
		}
		if ordered[i].snapshot.ExamID != ordered[j].snapshot.ExamID {
			return ordered[i].snapshot.ExamID < ordered[j].snapshot.ExamID
		}
		return ordered[i].snapshot.SnapshotID < ordered[j].snapshot.SnapshotID
	})
	return ordered, nil
}

func snapshotEventTime(snapshot SnapshotData) (time.Time, error) {
	if snapshot.EndsAt != nil && !snapshot.EndsAt.IsZero() {
		return snapshot.EndsAt.UTC(), nil
	}
	var latest time.Time
	for _, submission := range snapshot.Submissions {
		if submission.SubmittedAt.After(latest) {
			latest = submission.SubmittedAt
		}
	}
	if !latest.IsZero() {
		return latest.UTC(), nil
	}
	if snapshot.StartsAt != nil && !snapshot.StartsAt.IsZero() {
		return snapshot.StartsAt.UTC(), nil
	}
	return time.Time{}, fmt.Errorf("snapshot %d has no deterministic event time", snapshot.SnapshotID)
}
