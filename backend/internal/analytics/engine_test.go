package analytics

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestComputeIsDeterministicAcrossInputPermutations(t *testing.T) {
	t.Parallel()

	configuration := mustTestConfig(t)
	first := testDataset()
	second := testDataset()
	second.Snapshots[0], second.Snapshots[1] = second.Snapshots[1], second.Snapshots[0]
	for index := range second.Snapshots {
		snapshot := &second.Snapshots[index]
		reverseProblems(snapshot.Problems)
		reverseParticipants(snapshot.Participants)
		reverseSubmissions(snapshot.Submissions)
		for participantIndex := range snapshot.Participants {
			reverseProblemResults(snapshot.Participants[participantIndex].ProblemResults)
		}
	}
	firstResult, err := Compute(context.Background(), configuration, first)
	if err != nil {
		t.Fatalf("Compute(first) error = %v", err)
	}
	secondResult, err := Compute(context.Background(), configuration, second)
	if err != nil {
		t.Fatalf("Compute(second) error = %v", err)
	}
	firstJSON, err := json.Marshal(firstResult)
	if err != nil {
		t.Fatalf("Marshal(first) error = %v", err)
	}
	secondJSON, err := json.Marshal(secondResult)
	if err != nil {
		t.Fatalf("Marshal(second) error = %v", err)
	}
	if string(firstJSON) != string(secondJSON) {
		t.Fatalf("permuted result differs:\n%s\n%s", firstJSON, secondJSON)
	}
	if len(firstResult.Students) != 2 || len(firstResult.Students[0].Metrics.RatingHistory) != 2 {
		t.Fatalf("result histories = %#v", firstResult.Students)
	}
	for _, student := range firstResult.Students {
		if student.Rating < 0 {
			t.Fatalf("student rating = %d", student.Rating)
		}
		if err := validateMetricValues(student.Metrics.Current); err != nil {
			t.Fatalf("current metrics = %#v: %v", student.Metrics.Current, err)
		}
	}
}

func TestComputeStopsOnCanceledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Compute(ctx, mustTestConfig(t), testDataset())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Compute() error = %v", err)
	}
	if code, ok := CodeOf(err); !ok || code != ErrorCanceled {
		t.Fatalf("Compute() code = %q, %v", code, ok)
	}
}

func TestComputeDefinesTiesAndZeroSubmissionMetrics(t *testing.T) {
	t.Parallel()

	start := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	result, err := Compute(context.Background(), mustTestConfig(t), Dataset{Snapshots: []SnapshotData{{
		ExamID:               1,
		SnapshotID:           10,
		DomainHash:           repeatedHash('a'),
		StartsAt:             &start,
		ExpectedProblems:     1,
		ExpectedParticipants: 2,
		ExpectedRankings:     0,
		ExpectedSubmissions:  0,
		Problems:             []ProblemData{{ProblemSetProblemID: "p1"}},
		Participants:         []ParticipantData{{ActorID: 2}, {ActorID: 1}},
	}}})
	if err != nil {
		t.Fatalf("Compute() error = %v", err)
	}
	if !result.ReferenceTime.Equal(start) || len(result.Students) != 2 || len(result.Problems) != 1 {
		t.Fatalf("result = %#v", result)
	}
	for _, student := range result.Students {
		current := student.Metrics.Current
		if current.Knowledge == nil || *current.Knowledge != 50 || current.Accuracy != nil || current.Quality != nil || current.Flexibility != nil || current.Proficiency != nil {
			t.Fatalf("zero-submission current metrics = %#v", current)
		}
		if len(student.Metrics.RatingHistory) != 1 || student.Metrics.RatingHistory[0].Rank != 1 {
			t.Fatalf("tie rating history = %#v", student.Metrics.RatingHistory)
		}
	}
	problem := result.Problems[0].Metrics
	if problem.SubmissionCount != 0 || problem.AcceptedSubmissionCount != 0 || problem.SubmissionAcceptanceRate != 0 || problem.ActorAcceptanceRate != 0 || problem.AcceptedRuntimeMS != nil || problem.AcceptedMemoryBytes != nil {
		t.Fatalf("zero-submission problem metrics = %#v", problem)
	}
}

func TestComputeRejectsSnapshotWithoutDeterministicEventTime(t *testing.T) {
	t.Parallel()

	_, err := Compute(context.Background(), mustTestConfig(t), Dataset{Snapshots: []SnapshotData{{
		ExamID:           1,
		SnapshotID:       10,
		DomainHash:       repeatedHash('a'),
		ExpectedProblems: 1,
		Problems:         []ProblemData{{ProblemSetProblemID: "p1"}},
	}}})
	if err == nil || !IsPermanent(err) {
		t.Fatalf("Compute() error = %v", err)
	}
	if code, ok := CodeOf(err); !ok || code != ErrorInvalidDataset {
		t.Fatalf("Compute() code = %q, %v", code, ok)
	}
}

func mustTestConfig(t *testing.T) Config {
	t.Helper()
	parsed, err := ParseConfig([]byte(validConfigJSON))
	if err != nil {
		t.Fatalf("ParseConfig() error = %v", err)
	}
	return parsed.Value
}

func testDataset() Dataset {
	firstEnd := time.Date(2026, time.January, 1, 12, 0, 0, 0, time.UTC)
	secondEnd := firstEnd.Add(48 * time.Hour)
	firstScore := 100.0
	problemScore := 50.0
	rankOne := int64(1)
	rankTwo := int64(2)
	usedFast := int64(600)
	usedSlow := int64(900)
	accepted := true
	rejected := false
	countOne := int64(1)
	runtimeFast := int64(100)
	runtimeSlow := int64(200)
	memory := int64(1024)
	return Dataset{Snapshots: []SnapshotData{
		{
			ExamID: 1, SnapshotID: 11, DomainHash: repeatedHash('a'), EndsAt: &firstEnd, TotalScore: &firstScore,
			ExpectedProblems: 2, ExpectedParticipants: 2, ExpectedRankings: 2, ExpectedSubmissions: 3,
			Problems: []ProblemData{{ProblemSetProblemID: "p1", MaxScore: &problemScore}, {ProblemSetProblemID: "p2", MaxScore: &problemScore}},
			Participants: []ParticipantData{
				{ActorID: 1, Ranking: &RankingData{Rank: rankOne, TotalScore: &firstScore, TimeUsedSeconds: &usedFast}, ProblemResults: []RankingProblemResultData{{ProblemSetProblemID: "p1", Score: &problemScore, Passed: &accepted, ValidSubmissionCount: &countOne}, {ProblemSetProblemID: "p2", Score: &problemScore, Passed: &accepted, ValidSubmissionCount: &countOne}}},
				{ActorID: 2, Ranking: &RankingData{Rank: rankTwo, TotalScore: &problemScore, TimeUsedSeconds: &usedSlow}, ProblemResults: []RankingProblemResultData{{ProblemSetProblemID: "p1", Score: &problemScore, Passed: &accepted, ValidSubmissionCount: &countOne}, {ProblemSetProblemID: "p2", Passed: &rejected}}},
			},
			Submissions: []SubmissionData{
				{SubmissionIdentityID: 1, ActorID: 1, ProblemSetProblemID: "p1", SubmittedAt: firstEnd.Add(-30 * time.Minute), Verdict: "ACCEPTED", Score: &problemScore, TimeMS: &runtimeFast, MemoryBytes: &memory},
				{SubmissionIdentityID: 2, ActorID: 1, ProblemSetProblemID: "p2", SubmittedAt: firstEnd.Add(-20 * time.Minute), Verdict: "ACCEPTED", Score: &problemScore, TimeMS: &runtimeSlow, MemoryBytes: &memory},
				{SubmissionIdentityID: 3, ActorID: 2, ProblemSetProblemID: "p1", SubmittedAt: firstEnd.Add(-10 * time.Minute), Verdict: "WRONG_ANSWER", TimeMS: &runtimeSlow, MemoryBytes: &memory},
			},
		},
		{
			ExamID: 2, SnapshotID: 22, DomainHash: repeatedHash('b'), EndsAt: &secondEnd, TotalScore: &firstScore,
			ExpectedProblems: 1, ExpectedParticipants: 2, ExpectedRankings: 2, ExpectedSubmissions: 2,
			Problems: []ProblemData{{ProblemSetProblemID: "q1", MaxScore: &firstScore}},
			Participants: []ParticipantData{
				{ActorID: 1, Ranking: &RankingData{Rank: rankTwo, TotalScore: &problemScore, TimeUsedSeconds: &usedSlow}, ProblemResults: []RankingProblemResultData{{ProblemSetProblemID: "q1", Score: &problemScore, Passed: &rejected, ValidSubmissionCount: &countOne}}},
				{ActorID: 2, Ranking: &RankingData{Rank: rankOne, TotalScore: &firstScore, TimeUsedSeconds: &usedFast}, ProblemResults: []RankingProblemResultData{{ProblemSetProblemID: "q1", Score: &firstScore, Passed: &accepted, ValidSubmissionCount: &countOne}}},
			},
			Submissions: []SubmissionData{
				{SubmissionIdentityID: 4, ActorID: 1, ProblemSetProblemID: "q1", SubmittedAt: secondEnd.Add(-20 * time.Minute), Verdict: "WRONG_ANSWER", Score: &problemScore, TimeMS: &runtimeSlow, MemoryBytes: &memory},
				{SubmissionIdentityID: 5, ActorID: 2, ProblemSetProblemID: "q1", SubmittedAt: secondEnd.Add(-10 * time.Minute), Verdict: "ACCEPTED", Score: &firstScore, TimeMS: &runtimeFast, MemoryBytes: &memory},
			},
		},
	}}
}

func reverseProblems(values []ProblemData) {
	for left, right := 0, len(values)-1; left < right; left, right = left+1, right-1 {
		values[left], values[right] = values[right], values[left]
	}
}

func reverseParticipants(values []ParticipantData) {
	for left, right := 0, len(values)-1; left < right; left, right = left+1, right-1 {
		values[left], values[right] = values[right], values[left]
	}
}

func reverseSubmissions(values []SubmissionData) {
	for left, right := 0, len(values)-1; left < right; left, right = left+1, right-1 {
		values[left], values[right] = values[right], values[left]
	}
}

func reverseProblemResults(values []RankingProblemResultData) {
	for left, right := 0, len(values)-1; left < right; left, right = left+1, right-1 {
		values[left], values[right] = values[right], values[left]
	}
}

func repeatedHash(value byte) string {
	data := make([]byte, 64)
	for index := range data {
		data[index] = value
	}
	return string(data)
}
