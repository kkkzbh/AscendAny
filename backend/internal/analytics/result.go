package analytics

import (
	"errors"
	"fmt"
	"time"
)

func validateResult(result Result) error {
	if err := validateUTCTime(result.ReferenceTime, "reference time"); err != nil {
		return analyticsError(ErrorInvalidDataset, true, "validate analytics result", err)
	}
	var previousActorID int64
	for index, student := range result.Students {
		if student.ActorID <= previousActorID || student.Rating < 0 {
			return analyticsError(ErrorInvalidDataset, true, "validate analytics result", fmt.Errorf("student %d is unsorted or invalid", index))
		}
		previousActorID = student.ActorID
		if !student.Metrics.ReferenceTime.Equal(result.ReferenceTime) {
			return analyticsError(ErrorInvalidDataset, true, "validate analytics result", fmt.Errorf("student %d metadata differs from result", student.ActorID))
		}
		if err := validateStudentMetrics(student.Metrics); err != nil {
			return analyticsError(ErrorInvalidDataset, true, "validate analytics result", fmt.Errorf("student %d metrics: %w", student.ActorID, err))
		}
		if len(student.Metrics.RatingHistory) == 0 || student.Rating != student.Metrics.RatingHistory[len(student.Metrics.RatingHistory)-1].NewRating {
			return analyticsError(ErrorInvalidDataset, true, "validate analytics result", fmt.Errorf("student %d canonical rating differs from rating history", student.ActorID))
		}
	}
	var previousSnapshotID int64
	previousProblemID := ""
	for index, problem := range result.Problems {
		if problem.SnapshotID <= 0 || problem.ProblemSetProblemID == "" ||
			problem.SnapshotID < previousSnapshotID ||
			(problem.SnapshotID == previousSnapshotID && problem.ProblemSetProblemID <= previousProblemID) {
			return analyticsError(ErrorInvalidDataset, true, "validate analytics result", fmt.Errorf("problem %d is unsorted or invalid", index))
		}
		previousSnapshotID = problem.SnapshotID
		previousProblemID = problem.ProblemSetProblemID
		if err := validateProblemMetrics(problem.Metrics); err != nil {
			return analyticsError(ErrorInvalidDataset, true, "validate analytics result", fmt.Errorf("problem %d/%s: %w", problem.SnapshotID, problem.ProblemSetProblemID, err))
		}
	}
	return nil
}

func validateStudentMetrics(metrics StudentMetrics) error {
	if metrics.Protocol != StudentMetricsProtocolV1 {
		return fmt.Errorf("protocol must be %q", StudentMetricsProtocolV1)
	}
	if err := validateUTCTime(metrics.ReferenceTime, "referenceTime"); err != nil {
		return err
	}
	if err := validateMetricValues(metrics.Current); err != nil {
		return fmt.Errorf("current metrics: %w", err)
	}
	if len(metrics.ExamHistory) != len(metrics.RatingHistory) {
		return errors.New("examHistory and ratingHistory must be aligned")
	}
	if len(metrics.ExamHistory) == 0 {
		return errors.New("stored student metrics must contain at least one observation")
	}
	seenExamIDs := make(map[int64]struct{}, len(metrics.ExamHistory))
	seenSnapshotIDs := make(map[int64]struct{}, len(metrics.ExamHistory))
	var previousExam ExamMetricPoint
	var previousRating RatingHistoryPoint
	for index := range metrics.ExamHistory {
		exam := metrics.ExamHistory[index]
		rating := metrics.RatingHistory[index]
		if exam.ExamID <= 0 || exam.SnapshotID <= 0 {
			return fmt.Errorf("examHistory[%d] has invalid identity", index)
		}
		if err := validateUTCTime(exam.EventTime, fmt.Sprintf("examHistory[%d].eventTime", index)); err != nil {
			return err
		}
		if exam.EventTime.After(metrics.ReferenceTime) {
			return fmt.Errorf("examHistory[%d].eventTime exceeds referenceTime", index)
		}
		if _, exists := seenExamIDs[exam.ExamID]; exists {
			return fmt.Errorf("examHistory[%d] duplicates examId %d", index, exam.ExamID)
		}
		seenExamIDs[exam.ExamID] = struct{}{}
		if _, exists := seenSnapshotIDs[exam.SnapshotID]; exists {
			return fmt.Errorf("examHistory[%d] duplicates snapshotId %d", index, exam.SnapshotID)
		}
		seenSnapshotIDs[exam.SnapshotID] = struct{}{}
		if index > 0 && !historyIdentityBefore(previousExam.EventTime, previousExam.ExamID, previousExam.SnapshotID, exam.EventTime, exam.ExamID, exam.SnapshotID) {
			return errors.New("examHistory must be strictly ascending by eventTime, examId, and snapshotId")
		}
		if err := validateMetricValues(exam.Values); err != nil {
			return fmt.Errorf("examHistory[%d].values: %w", index, err)
		}
		if err := validateUTCTime(rating.EventTime, fmt.Sprintf("ratingHistory[%d].eventTime", index)); err != nil {
			return err
		}
		if rating.ExamID != exam.ExamID || rating.SnapshotID != exam.SnapshotID || !rating.EventTime.Equal(exam.EventTime) {
			return fmt.Errorf("ratingHistory[%d] is not aligned with examHistory", index)
		}
		if rating.Rank <= 0 || rating.OldRating < 0 || rating.NewRating < 0 || rating.NewRating-rating.OldRating != rating.Delta || !finite(rating.Seed) || !finite(rating.Performance) {
			return fmt.Errorf("ratingHistory[%d] has invalid rank, rating, delta, seed, or performance", index)
		}
		if index > 0 && rating.OldRating != previousRating.NewRating {
			return fmt.Errorf("ratingHistory[%d].oldRating does not continue the previous rating", index)
		}
		previousExam = exam
		previousRating = rating
	}
	return nil
}

func validateUTCTime(value time.Time, name string) error {
	if value.IsZero() {
		return fmt.Errorf("%s is required", name)
	}
	_, offset := value.Zone()
	if offset != 0 {
		return fmt.Errorf("%s must use UTC", name)
	}
	return nil
}

func historyIdentityBefore(
	leftTime time.Time,
	leftExamID int64,
	leftSnapshotID int64,
	rightTime time.Time,
	rightExamID int64,
	rightSnapshotID int64,
) bool {
	if !leftTime.Equal(rightTime) {
		return leftTime.Before(rightTime)
	}
	if leftExamID != rightExamID {
		return leftExamID < rightExamID
	}
	return leftSnapshotID < rightSnapshotID
}

func validateResultSnapshots(result Result, snapshots []ManifestSnapshot) error {
	allowed := make(map[int64]struct{}, len(snapshots))
	for _, snapshot := range snapshots {
		allowed[snapshot.SnapshotID] = struct{}{}
	}
	for _, problem := range result.Problems {
		if _, exists := allowed[problem.SnapshotID]; !exists {
			return analyticsError(ErrorInvalidDataset, true, "validate analytics result", fmt.Errorf("problem references snapshot %d outside the generation manifest", problem.SnapshotID))
		}
	}
	return nil
}

func validateMetricValues(values MetricValues) error {
	for _, metric := range []struct {
		name  string
		value *float64
	}{
		{name: "knowledge", value: values.Knowledge},
		{name: "accuracy", value: values.Accuracy},
		{name: "quality", value: values.Quality},
		{name: "flexibility", value: values.Flexibility},
		{name: "proficiency", value: values.Proficiency},
	} {
		if metric.value != nil && (!finite(*metric.value) || *metric.value < 0 || *metric.value > 100) {
			return fmt.Errorf("%s must be nil or in [0, 100]", metric.name)
		}
	}
	return nil
}

func validateProblemMetrics(metrics ProblemMetrics) error {
	if metrics.Protocol != problemMetricsProtocolV1 ||
		metrics.ParticipantCount < 0 || metrics.SubmissionCount < 0 || metrics.AcceptedSubmissionCount < 0 ||
		metrics.AttemptingActorCount < 0 || metrics.AcceptedActorCount < 0 ||
		metrics.AcceptedSubmissionCount > metrics.SubmissionCount ||
		metrics.AcceptedActorCount > metrics.AttemptingActorCount ||
		metrics.AttemptingActorCount > metrics.ParticipantCount ||
		!unitInterval(metrics.SubmissionAcceptanceRate) || !unitInterval(metrics.ActorAcceptanceRate) {
		return errors.New("counts, protocol, or acceptance rates are invalid")
	}
	if err := validateDistribution(metrics.AcceptedRuntimeMS); err != nil {
		return fmt.Errorf("accepted runtime: %w", err)
	}
	if err := validateDistribution(metrics.AcceptedMemoryBytes); err != nil {
		return fmt.Errorf("accepted memory: %w", err)
	}
	return nil
}

func validateDistribution(value *DistributionStats) error {
	if value == nil {
		return nil
	}
	if value.Count <= 0 || value.Min < 0 || value.Max < value.Min || !finite(value.Median) || !finite(value.P95) || value.Median < float64(value.Min) || value.Median > float64(value.Max) || value.P95 < float64(value.Min) || value.P95 > float64(value.Max) {
		return errors.New("distribution is invalid")
	}
	return nil
}

func unitInterval(value float64) bool {
	return finite(value) && value >= 0 && value <= 1
}
