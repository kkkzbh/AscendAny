package analytics

import (
	"context"
	"math"
	"sort"
	"time"
)

func computeSnapshot(
	ctx context.Context,
	snapshot SnapshotData,
	acceptedVerdicts map[string]struct{},
	winsor WinsorConfig,
) (map[int64]MetricValues, map[int64]int64, []ProblemResult, error) {
	participantByActor := make(map[int64]ParticipantData, len(snapshot.Participants))
	submissionsByActor := make(map[int64][]SubmissionData, len(snapshot.Participants))
	acceptedProblems := make(map[int64]map[string]struct{}, len(snapshot.Participants))
	for _, participant := range snapshot.Participants {
		if err := computationContextError(ctx); err != nil {
			return nil, nil, nil, err
		}
		participantByActor[participant.ActorID] = participant
		acceptedProblems[participant.ActorID] = make(map[string]struct{})
		for _, result := range participant.ProblemResults {
			if result.Passed != nil && *result.Passed {
				acceptedProblems[participant.ActorID][result.ProblemSetProblemID] = struct{}{}
			}
		}
	}

	problemSubmissions := make(map[string][]SubmissionData, len(snapshot.Problems))
	bestAcceptedRuntime := make(map[int64]map[string]int64)
	for _, submission := range snapshot.Submissions {
		if err := computationContextError(ctx); err != nil {
			return nil, nil, nil, err
		}
		submissionsByActor[submission.ActorID] = append(submissionsByActor[submission.ActorID], submission)
		problemSubmissions[submission.ProblemSetProblemID] = append(problemSubmissions[submission.ProblemSetProblemID], submission)
		if _, accepted := acceptedVerdicts[submission.Verdict]; !accepted {
			continue
		}
		acceptedProblems[submission.ActorID][submission.ProblemSetProblemID] = struct{}{}
		if submission.TimeMS != nil {
			byProblem := bestAcceptedRuntime[submission.ActorID]
			if byProblem == nil {
				byProblem = make(map[string]int64)
				bestAcceptedRuntime[submission.ActorID] = byProblem
			}
			current, exists := byProblem[submission.ProblemSetProblemID]
			if !exists || *submission.TimeMS < current {
				byProblem[submission.ProblemSetProblemID] = *submission.TimeMS
			}
		}
	}
	for actorID := range submissionsByActor {
		sort.Slice(submissionsByActor[actorID], func(i, j int) bool {
			left := submissionsByActor[actorID][i]
			right := submissionsByActor[actorID][j]
			if !left.SubmittedAt.Equal(right.SubmittedAt) {
				return left.SubmittedAt.Before(right.SubmittedAt)
			}
			return left.SubmissionIdentityID < right.SubmissionIdentityID
		})
	}

	problemRuntimeMedians := make(map[string]float64, len(snapshot.Problems))
	for _, problem := range snapshot.Problems {
		if err := computationContextError(ctx); err != nil {
			return nil, nil, nil, err
		}
		values := make([]float64, 0)
		for _, byProblem := range bestAcceptedRuntime {
			if value, exists := byProblem[problem.ProblemSetProblemID]; exists {
				values = append(values, float64(value))
			}
		}
		if len(values) > 0 {
			problemRuntimeMedians[problem.ProblemSetProblemID] = median(values)
		}
	}

	rawKnowledge := make(map[int64]float64, len(snapshot.Participants))
	rawAccuracy := make(map[int64]float64)
	rawQuality := make(map[int64]float64)
	rawFlexibility := make(map[int64]float64)
	rawProficiency := make(map[int64]float64)
	fallbackRankInputs := make([]rankInput, 0, len(snapshot.Participants))

	for _, participant := range snapshot.Participants {
		if err := computationContextError(ctx); err != nil {
			return nil, nil, nil, err
		}
		actorID := participant.ActorID
		acceptedCount := len(acceptedProblems[actorID])
		knowledge := float64(acceptedCount) / float64(maxInt(1, len(snapshot.Problems)))
		fallbackScore := float64(acceptedCount)
		var fallbackTime *int64
		if participant.Ranking != nil {
			fallbackTime = participant.Ranking.TimeUsedSeconds
			if participant.Ranking.TotalScore != nil {
				fallbackScore = *participant.Ranking.TotalScore
				if snapshot.TotalScore != nil && *snapshot.TotalScore > 0 {
					knowledge = fallbackScore / *snapshot.TotalScore
				}
			}
		}
		rawKnowledge[actorID] = knowledge
		actorSubmissions := submissionsByActor[actorID]
		if len(actorSubmissions) > 0 {
			rawAccuracy[actorID] = float64(acceptedCount) / float64(len(actorSubmissions))
			distinctProblems := make(map[string]struct{})
			switches := 0
			for index, submission := range actorSubmissions {
				distinctProblems[submission.ProblemSetProblemID] = struct{}{}
				if index > 0 && actorSubmissions[index-1].ProblemSetProblemID != submission.ProblemSetProblemID {
					switches++
				}
			}
			switchRate := 0.0
			if len(actorSubmissions) > 1 {
				switchRate = float64(switches) / float64(len(actorSubmissions)-1)
			}
			efficiency := float64(acceptedCount) / float64(len(actorSubmissions))
			coverage := float64(len(distinctProblems)) / float64(maxInt(1, len(snapshot.Problems)))
			rawFlexibility[actorID] = efficiency * (0.5 + 0.5*switchRate) * (0.5 + 0.5*coverage)
		}

		runtimeRatios := make([]float64, 0)
		for problemID, runtimeMS := range bestAcceptedRuntime[actorID] {
			problemMedian := problemRuntimeMedians[problemID]
			if problemMedian > 0 {
				runtimeRatios = append(runtimeRatios, math.Max(1e-9, float64(runtimeMS)/problemMedian))
			}
		}
		if len(runtimeRatios) > 0 {
			rawQuality[actorID] = 1 / median(runtimeRatios)
		}

		minutes := 0.0
		if participant.Ranking != nil && participant.Ranking.TimeUsedSeconds != nil && *participant.Ranking.TimeUsedSeconds > 0 {
			minutes = float64(*participant.Ranking.TimeUsedSeconds) / 60
		} else if len(actorSubmissions) > 0 {
			minutes = actorSubmissions[len(actorSubmissions)-1].SubmittedAt.Sub(actorSubmissions[0].SubmittedAt).Minutes()
			minutes = math.Max(1, minutes)
		}
		if minutes > 0 {
			rawProficiency[actorID] = fallbackScore / minutes
		}
		fallbackRankInputs = append(fallbackRankInputs, rankInput{actorID: actorID, score: fallbackScore, timeUsed: fallbackTime})
	}

	knowledgeScores := percentileScores(rawKnowledge, winsor.Low, winsor.High)
	accuracyScores := percentileScores(rawAccuracy, winsor.Low, winsor.High)
	qualityScores := percentileScores(rawQuality, winsor.Low, winsor.High)
	flexibilityScores := percentileScores(rawFlexibility, winsor.Low, winsor.High)
	proficiencyScores := percentileScores(rawProficiency, winsor.Low, winsor.High)
	metrics := make(map[int64]MetricValues, len(snapshot.Participants))
	for _, participant := range snapshot.Participants {
		if err := computationContextError(ctx); err != nil {
			return nil, nil, nil, err
		}
		metrics[participant.ActorID] = MetricValues{
			Knowledge:   scorePointer(knowledgeScores, participant.ActorID),
			Accuracy:    scorePointer(accuracyScores, participant.ActorID),
			Quality:     scorePointer(qualityScores, participant.ActorID),
			Flexibility: scorePointer(flexibilityScores, participant.ActorID),
			Proficiency: scorePointer(proficiencyScores, participant.ActorID),
		}
	}

	fallbackRanks := competitionRanks(fallbackRankInputs)
	ranks := make(map[int64]int64, len(snapshot.Participants))
	for _, participant := range snapshot.Participants {
		if participant.Ranking != nil {
			ranks[participant.ActorID] = participant.Ranking.Rank
		} else {
			ranks[participant.ActorID] = fallbackRanks[participant.ActorID]
		}
	}
	problemMetrics, err := computeProblemMetrics(ctx, snapshot, problemSubmissions, acceptedVerdicts)
	if err != nil {
		return nil, nil, nil, err
	}
	return metrics, ranks, problemMetrics, nil
}

func computeProblemMetrics(
	ctx context.Context,
	snapshot SnapshotData,
	byProblem map[string][]SubmissionData,
	acceptedVerdicts map[string]struct{},
) ([]ProblemResult, error) {
	result := make([]ProblemResult, 0, len(snapshot.Problems))
	for _, problem := range snapshot.Problems {
		if err := computationContextError(ctx); err != nil {
			return nil, err
		}
		submissions := byProblem[problem.ProblemSetProblemID]
		attemptingActors := make(map[int64]struct{})
		acceptedActors := make(map[int64]struct{})
		runtimes := make([]int64, 0)
		memory := make([]int64, 0)
		acceptedCount := int64(0)
		for _, submission := range submissions {
			if err := computationContextError(ctx); err != nil {
				return nil, err
			}
			attemptingActors[submission.ActorID] = struct{}{}
			if _, accepted := acceptedVerdicts[submission.Verdict]; !accepted {
				continue
			}
			acceptedCount++
			acceptedActors[submission.ActorID] = struct{}{}
			if submission.TimeMS != nil {
				runtimes = append(runtimes, *submission.TimeMS)
			}
			if submission.MemoryBytes != nil {
				memory = append(memory, *submission.MemoryBytes)
			}
		}
		submissionRate := 0.0
		if len(submissions) > 0 {
			submissionRate = float64(acceptedCount) / float64(len(submissions))
		}
		actorRate := 0.0
		if len(attemptingActors) > 0 {
			actorRate = float64(len(acceptedActors)) / float64(len(attemptingActors))
		}
		result = append(result, ProblemResult{
			SnapshotID:          snapshot.SnapshotID,
			ProblemSetProblemID: problem.ProblemSetProblemID,
			Metrics: ProblemMetrics{
				Protocol:                 problemMetricsProtocolV1,
				ParticipantCount:         int64(len(snapshot.Participants)),
				SubmissionCount:          int64(len(submissions)),
				AcceptedSubmissionCount:  acceptedCount,
				AttemptingActorCount:     int64(len(attemptingActors)),
				AcceptedActorCount:       int64(len(acceptedActors)),
				SubmissionAcceptanceRate: round6(submissionRate),
				ActorAcceptanceRate:      round6(actorRate),
				AcceptedRuntimeMS:        integerDistribution(runtimes),
				AcceptedMemoryBytes:      integerDistribution(memory),
			},
		})
	}
	return result, nil
}

type rankInput struct {
	actorID  int64
	score    float64
	timeUsed *int64
}

func competitionRanks(inputs []rankInput) map[int64]int64 {
	ordered := append([]rankInput(nil), inputs...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].score != ordered[j].score {
			return ordered[i].score > ordered[j].score
		}
		leftTime := int64(math.MaxInt64)
		rightTime := int64(math.MaxInt64)
		if ordered[i].timeUsed != nil {
			leftTime = *ordered[i].timeUsed
		}
		if ordered[j].timeUsed != nil {
			rightTime = *ordered[j].timeUsed
		}
		if leftTime != rightTime {
			return leftTime < rightTime
		}
		return ordered[i].actorID < ordered[j].actorID
	})
	ranks := make(map[int64]int64, len(ordered))
	previousRank := int64(0)
	previousScore := 0.0
	previousTime := int64(0)
	for index, input := range ordered {
		currentTime := int64(math.MaxInt64)
		if input.timeUsed != nil {
			currentTime = *input.timeUsed
		}
		if index == 0 || input.score != previousScore || currentTime != previousTime {
			previousRank = int64(index + 1)
		}
		ranks[input.actorID] = previousRank
		previousScore = input.score
		previousTime = currentTime
	}
	return ranks
}

func computeRatings(ctx context.Context, computation snapshotComputation, current map[int64]int64, configuration RatingConfig) ([]ratingComputation, error) {
	actorIDs := make([]int64, 0, len(computation.snapshot.Participants))
	for _, participant := range computation.snapshot.Participants {
		if err := computationContextError(ctx); err != nil {
			return nil, err
		}
		actorIDs = append(actorIDs, participant.ActorID)
	}
	sort.Slice(actorIDs, func(i, j int) bool { return actorIDs[i] < actorIDs[j] })
	if len(actorIDs) == 0 {
		return nil, nil
	}
	oldRatings := make(map[int64]int64, len(actorIDs))
	for _, actorID := range actorIDs {
		if err := computationContextError(ctx); err != nil {
			return nil, err
		}
		oldRatings[actorID] = ratingOrInitial(current, actorID, configuration.Initial)
	}
	deltas := make(map[int64]float64, len(actorIDs))
	seeds := make(map[int64]float64, len(actorIDs))
	performances := make(map[int64]float64, len(actorIDs))
	for _, actorID := range actorIDs {
		if err := computationContextError(ctx); err != nil {
			return nil, err
		}
		opponents := make([]int64, 0, len(actorIDs)-1)
		for _, otherID := range actorIDs {
			if err := computationContextError(ctx); err != nil {
				return nil, err
			}
			if otherID != actorID {
				opponents = append(opponents, oldRatings[otherID])
			}
		}
		seed := expectedRank(float64(oldRatings[actorID]), opponents)
		targetRank := math.Sqrt(seed * float64(computation.ranks[actorID]))
		performance := solvePerformance(targetRank, opponents, configuration)
		deltas[actorID] = (performance - float64(oldRatings[actorID])) / 2
		seeds[actorID] = seed
		performances[actorID] = performance
	}
	sumDelta := 0.0
	for _, actorID := range actorIDs {
		if err := computationContextError(ctx); err != nil {
			return nil, err
		}
		sumDelta += deltas[actorID]
	}
	inc1 := (-1 - sumDelta) / float64(len(actorIDs))
	for _, actorID := range actorIDs {
		deltas[actorID] += inc1
	}
	top := append([]int64(nil), actorIDs...)
	sort.Slice(top, func(i, j int) bool {
		if oldRatings[top[i]] != oldRatings[top[j]] {
			return oldRatings[top[i]] > oldRatings[top[j]]
		}
		return top[i] < top[j]
	})
	topN := minInt(len(top), maxInt(1, int(4*math.Sqrt(float64(len(top))))))
	topSum := 0.0
	for _, actorID := range top[:topN] {
		topSum += deltas[actorID]
	}
	inc2 := math.Max(-10, math.Min(0, -topSum/float64(topN)))
	result := make([]ratingComputation, 0, len(actorIDs))
	for _, actorID := range actorIDs {
		delta := int64(math.Round(deltas[actorID] + inc2))
		newRating := maxInt64(0, oldRatings[actorID]+delta)
		result = append(result, ratingComputation{
			actorID:     actorID,
			rank:        computation.ranks[actorID],
			oldRating:   oldRatings[actorID],
			delta:       newRating - oldRatings[actorID],
			newRating:   newRating,
			seed:        seeds[actorID],
			performance: performances[actorID],
		})
	}
	return result, nil
}

func expectedRank(candidate float64, opponents []int64) float64 {
	result := 1.0
	for _, opponent := range opponents {
		result += 1 / (1 + math.Pow(10, (candidate-float64(opponent))/400))
	}
	return result
}

func solvePerformance(targetRank float64, opponents []int64, configuration RatingConfig) float64 {
	low := float64(configuration.BinarySearchMin)
	high := float64(configuration.BinarySearchMax)
	for range configuration.BinarySearchSteps {
		middle := (low + high) / 2
		if expectedRank(middle, opponents) < targetRank {
			high = middle
		} else {
			low = middle
		}
	}
	return (low + high) / 2
}

func fuseMetrics(history []ExamMetricPoint, reference time.Time, halfLife HalfLifeDaysConfig) MetricValues {
	return MetricValues{
		Knowledge:   weightedMetric(history, reference, halfLife.Knowledge, func(values MetricValues) *float64 { return values.Knowledge }),
		Accuracy:    weightedMetric(history, reference, halfLife.Accuracy, func(values MetricValues) *float64 { return values.Accuracy }),
		Quality:     weightedMetric(history, reference, halfLife.Quality, func(values MetricValues) *float64 { return values.Quality }),
		Flexibility: weightedMetric(history, reference, halfLife.Flexibility, func(values MetricValues) *float64 { return values.Flexibility }),
		Proficiency: weightedMetric(history, reference, halfLife.Proficiency, func(values MetricValues) *float64 { return values.Proficiency }),
	}
}

func weightedMetric(history []ExamMetricPoint, reference time.Time, halfLifeDays float64, selectValue func(MetricValues) *float64) *float64 {
	numerator := 0.0
	denominator := 0.0
	for _, point := range history {
		value := selectValue(point.Values)
		if value == nil {
			continue
		}
		ageDays := math.Max(0, reference.Sub(point.EventTime).Hours()/24)
		weight := math.Exp(-math.Ln2 * ageDays / halfLifeDays)
		numerator += weight * *value
		denominator += weight
	}
	if denominator == 0 {
		return nil
	}
	return pointerFloat(numerator / denominator)
}

func scorePointer(scores map[int64]float64, actorID int64) *float64 {
	value, exists := scores[actorID]
	if !exists {
		return nil
	}
	return pointerFloat(value)
}

func ratingOrInitial(ratings map[int64]int64, actorID, initial int64) int64 {
	if value, exists := ratings[actorID]; exists {
		return value
	}
	return initial
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func maxInt64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}
