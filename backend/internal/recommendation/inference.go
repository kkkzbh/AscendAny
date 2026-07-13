package recommendation

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"

	"github.com/kkkzbh/AscendAny/backend/internal/analytics"
	"github.com/kkkzbh/AscendAny/backend/internal/canonicaljson"
	"github.com/kkkzbh/AscendAny/backend/internal/inferencemodel"
)

const (
	targetMastery            = 0.8
	targetSuccessProbability = 0.7
	maximumPathSteps         = 5
	minimumPathSteps         = 2
	problemsPerStep          = 3
)

type inferenceCandidate struct {
	ProblemKey        string
	SourceProblemKey  string
	Platform          string
	ProblemID         string
	Title             string
	SourceProblemSets []RecommendationSourceSet
	Features          []inferencemodel.FeatureValue
	Weights           []inferencemodel.KnowledgeWeight
	weightByKnowledge map[string]float64
	probability       float64
}

type problemAggregate struct {
	fact               problemFact
	row                problemRow
	sets               map[RecommendationSourceSet]struct{}
	participantCount   int64
	submissionCount    int64
	acceptedSubmission int64
	attemptingActors   int64
	acceptedActors     int64
}

type observationEvidence struct {
	Evidence        RecommendationInferenceEvidence
	KnowledgeCounts map[string]int64
	PassedSources   map[string]struct{}
}

func buildActorFeatures(ratingRaw string, metricsRaw json.RawMessage) ([]inferencemodel.FeatureValue, float64, error) {
	_, rating, err := nonnegativeFiniteNumber(ratingRaw, "student rating")
	if err != nil || rating > 1_000_000 {
		return nil, 0, errors.New("student rating is outside [0,1000000]")
	}
	metrics, err := analytics.DecodeStoredStudentMetrics(metricsRaw)
	if err != nil {
		return nil, 0, fmt.Errorf("student analytics metrics: %w", err)
	}
	values := make([]float64, 0, len(actorFeatureIDs))
	values = append(values, math.Log1p(rating))
	for _, value := range []*float64{
		metrics.Current.Knowledge, metrics.Current.Accuracy, metrics.Current.Quality,
		metrics.Current.Flexibility, metrics.Current.Proficiency,
	} {
		if value == nil {
			values = append(values, 0, 0)
		} else {
			values = append(values, *value, 1)
		}
	}
	values = append(values, math.Log1p(float64(len(metrics.ExamHistory))), math.Log1p(float64(len(metrics.RatingHistory))))
	features := make([]inferencemodel.FeatureValue, len(actorFeatureIDs))
	for index, featureID := range actorFeatureIDs {
		if !finite(values[index]) {
			return nil, 0, errors.New("actor feature is non-finite")
		}
		features[index] = inferencemodel.FeatureValue{FeatureID: featureID, Value: values[index]}
	}
	return features, rating, nil
}

func buildCandidates(rows []problemRow, catalog knowledgeCatalog, knowledgeIDs []string, passed map[string]struct{}) ([]inferenceCandidate, error) {
	aggregates := make(map[string]*problemAggregate)
	for index, row := range rows {
		fact, err := buildProblemFact(row)
		if err != nil {
			return nil, fmt.Errorf("problem %d: %w", index, err)
		}
		metrics, err := parseProblemMetrics(row.MetricsJSON)
		if err != nil {
			return nil, fmt.Errorf("problem %s metrics: %w", fact.ProblemKey, err)
		}
		aggregate := aggregates[fact.ProblemKey]
		if aggregate == nil {
			aggregate = &problemAggregate{fact: fact, row: row, sets: make(map[RecommendationSourceSet]struct{})}
			aggregates[fact.ProblemKey] = aggregate
		} else if aggregate.row.Title != row.Title || aggregate.fact.SourceProblemKey != fact.SourceProblemKey {
			return nil, errors.New("equal problem facts contain inconsistent public fields")
		}
		aggregate.sets[RecommendationSourceSet{ProblemSetID: row.ProblemSetID, SourceURL: row.SourceURL}] = struct{}{}
		for destination, value := range map[*int64]int64{
			&aggregate.participantCount:   metrics.ParticipantCount,
			&aggregate.submissionCount:    metrics.SubmissionCount,
			&aggregate.acceptedSubmission: metrics.AcceptedSubmissionCount,
			&aggregate.attemptingActors:   metrics.AttemptingActorCount,
			&aggregate.acceptedActors:     metrics.AcceptedActorCount,
		} {
			if value > math.MaxInt64-*destination {
				return nil, errors.New("aggregated problem metric count overflows int64")
			}
			*destination += value
		}
	}
	assignments := make(map[string]problemAssignment, len(catalog.Assignments))
	for _, assignment := range catalog.Assignments {
		key := pintiaProblemKey(assignment.ProblemID, assignment.ProblemFactSHA256)
		assignments[key] = assignment
	}
	candidates := make([]inferenceCandidate, 0)
	for key, aggregate := range aggregates {
		if _, excluded := passed[aggregate.fact.SourceProblemKey]; excluded {
			continue
		}
		assignment, assigned := assignments[key]
		if !assigned {
			continue
		}
		if !canonicalText(aggregate.row.Title, 1024) {
			return nil, errors.New("assigned problem title exceeds the inference result contract")
		}
		sets := make([]RecommendationSourceSet, 0, len(aggregate.sets))
		for source := range aggregate.sets {
			sets = append(sets, source)
		}
		slices.SortFunc(sets, compareSourceSet)
		weights := make([]inferencemodel.KnowledgeWeight, len(knowledgeIDs))
		weightByID := make(map[string]float64, len(assignment.Knowledge))
		assignmentIndex := 0
		for index, knowledgeID := range knowledgeIDs {
			weight := 0.0
			if assignmentIndex < len(assignment.Knowledge) && assignment.Knowledge[assignmentIndex].KnowledgePointID == knowledgeID {
				weight = assignment.Knowledge[assignmentIndex].Weight
				weightByID[knowledgeID] = weight
				assignmentIndex++
			}
			weights[index] = inferencemodel.KnowledgeWeight{KnowledgePointID: knowledgeID, Weight: weight}
		}
		if assignmentIndex != len(assignment.Knowledge) {
			return nil, errors.New("problem assignment references knowledge outside the model manifest")
		}
		features := problemFeatures(aggregate)
		candidates = append(candidates, inferenceCandidate{
			ProblemKey: key, SourceProblemKey: aggregate.fact.SourceProblemKey, Platform: aggregate.row.Platform,
			ProblemID: aggregate.row.ProblemID, Title: aggregate.row.Title, SourceProblemSets: sets,
			Features: features, Weights: weights, weightByKnowledge: weightByID,
		})
	}
	slices.SortFunc(candidates, func(left, right inferenceCandidate) int { return strings.Compare(left.ProblemKey, right.ProblemKey) })
	return candidates, nil
}

func problemFeatures(aggregate *problemAggregate) []inferencemodel.FeatureValue {
	maxScoreValue, maxScorePresent := 0.0, 0.0
	if aggregate.fact.MaxScore != nil {
		maxScoreValue, maxScorePresent = math.Log1p(*aggregate.fact.MaxScore), 1
	}
	timeValue, timePresent := 0.0, 0.0
	if aggregate.row.TimeLimitMS != nil {
		timeValue, timePresent = math.Log1p(float64(*aggregate.row.TimeLimitMS)), 1
	}
	memoryValue, memoryPresent := 0.0, 0.0
	if aggregate.row.MemoryLimitBytes != nil {
		memoryValue, memoryPresent = math.Log1p(float64(*aggregate.row.MemoryLimitBytes)), 1
	}
	values := []float64{
		ratio(aggregate.acceptedActors, aggregate.attemptingActors),
		ratio(aggregate.acceptedSubmission, aggregate.submissionCount),
		math.Log1p(float64(aggregate.participantCount)), math.Log1p(float64(aggregate.submissionCount)),
		math.Log1p(float64(aggregate.acceptedActors)), math.Log1p(float64(aggregate.acceptedSubmission)),
		math.Log1p(float64(aggregate.attemptingActors)), maxScoreValue, maxScorePresent,
		timeValue, timePresent, memoryValue, memoryPresent,
	}
	features := make([]inferencemodel.FeatureValue, len(problemFeatureIDs))
	for index := range features {
		features[index] = inferencemodel.FeatureValue{FeatureID: problemFeatureIDs[index], Value: values[index]}
	}
	return features
}

func buildObservationEvidence(rows []observationRow, catalog knowledgeCatalog) (observationEvidence, error) {
	assignmentByKey := make(map[string]problemAssignment, len(catalog.Assignments))
	for _, assignment := range catalog.Assignments {
		assignmentByKey[pintiaProblemKey(assignment.ProblemID, assignment.ProblemFactSHA256)] = assignment
	}
	result := observationEvidence{KnowledgeCounts: make(map[string]int64), PassedSources: make(map[string]struct{})}
	distinctSources := make(map[string]struct{})
	for index, row := range rows {
		observed := row.Score != nil || row.Passed != nil || row.RankingValidCount != nil && *row.RankingValidCount > 0 || row.ExportedSubmissionCount > 0
		if !observed {
			continue
		}
		fact, err := buildProblemFact(row.Problem)
		if err != nil {
			return observationEvidence{}, fmt.Errorf("observation %d: %w", index, err)
		}
		result.Evidence.ObservationCount++
		distinctSources[fact.SourceProblemKey] = struct{}{}
		if row.Passed != nil && *row.Passed {
			result.PassedSources[fact.SourceProblemKey] = struct{}{}
		}
		if assignment, exists := assignmentByKey[fact.ProblemKey]; exists {
			for _, weight := range assignment.Knowledge {
				result.KnowledgeCounts[weight.KnowledgePointID]++
			}
		}
	}
	result.Evidence.DistinctProblemCount = int64(len(distinctSources))
	result.Evidence.PassedProblemCount = int64(len(result.PassedSources))
	return result, nil
}

func materializeInference(
	model *inferencemodel.Model,
	catalog knowledgeCatalog,
	actorFeatures []inferencemodel.FeatureValue,
	rating float64,
	candidates []inferenceCandidate,
	evidence observationEvidence,
) (StudentRecommendationInferenceResult, error) {
	if len(candidates) == 0 {
		return StudentRecommendationInferenceResult{}, errors.New("inference requires an eligible problem")
	}
	manifest := model.Manifest()
	var mastery []inferencemodel.KnowledgeMastery
	for index := range candidates {
		inference, err := model.Evaluate(inferencemodel.Input{
			FeatureSchemaSHA256: FeatureSchemaSHA256(), KnowledgeCatalogSHA256: manifest.KnowledgeCatalogSHA256,
			ActorFeatures: actorFeatures, ProblemFeatures: candidates[index].Features, KnowledgeWeights: candidates[index].Weights,
		})
		if err != nil {
			return StudentRecommendationInferenceResult{}, fmt.Errorf("evaluate problem %q: %w", candidates[index].ProblemKey, err)
		}
		candidates[index].probability = inference.Probability
		if index == 0 {
			mastery = inference.KnowledgeMastery
		} else if !equalMastery(mastery, inference.KnowledgeMastery) {
			return StudentRecommendationInferenceResult{}, errors.New("model returned problem-dependent knowledge mastery")
		}
	}
	masteryOutput := make([]RecommendationKnowledgeMastery, len(catalog.Points))
	masteryValues := make([]float64, len(catalog.Points))
	for index, point := range catalog.Points {
		if mastery[index].KnowledgePointID != point.ID || !unitInterval(mastery[index].Probability) {
			return StudentRecommendationInferenceResult{}, errors.New("model mastery identity or probability is invalid")
		}
		masteryValues[index] = mastery[index].Probability
		masteryOutput[index] = RecommendationKnowledgeMastery{
			KnowledgePointID: point.ID, Label: point.Label, Description: point.Description,
			PrerequisiteIDs: slices.Clone(point.PrerequisiteIDs), Mastery: mastery[index].Probability,
			ObservationCount: evidence.KnowledgeCounts[point.ID],
		}
	}
	path, direct, pathReason := buildKnowledgePath(catalog.Points, masteryValues)
	result := StudentRecommendationInferenceResult{
		Schema: ResultSchemaV1, SourceRating: rating, Evidence: evidence.Evidence, KnowledgeMastery: masteryOutput,
	}
	if pathReason != "" {
		result.Status = RecommendationResultInsufficient
		eligible := int64(0)
		if pathReason != "mastery_target_satisfied" {
			eligible = eligibleProblemCount(catalog.Points, candidates, path)
		}
		result.Insufficiency = &RecommendationInsufficiency{
			ReasonCode: pathReason, MinimumPathSteps: minimumPathSteps, CandidatePathSteps: int64(len(path)),
			ProblemsPerStep: problemsPerStep, EligibleProblemCount: eligible, BlockedKnowledgePointIDs: []string{},
		}
	} else {
		steps, blocked := rankPathProblems(catalog.Points, masteryValues, candidates, path, direct)
		if len(blocked) > 0 {
			result.Status = RecommendationResultInsufficient
			result.Insufficiency = &RecommendationInsufficiency{
				ReasonCode: "problem_candidates_below_minimum", MinimumPathSteps: minimumPathSteps,
				CandidatePathSteps: int64(len(path)), ProblemsPerStep: problemsPerStep,
				EligibleProblemCount: eligibleProblemCount(catalog.Points, candidates, path), BlockedKnowledgePointIDs: blocked,
			}
		} else {
			result.Status = RecommendationResultReady
			result.LearningPath = steps
		}
	}
	if err := setResultSHA256(&result); err != nil {
		return StudentRecommendationInferenceResult{}, err
	}
	return result, nil
}

func equalMastery(left, right []inferencemodel.KnowledgeMastery) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

type knowledgeGap struct {
	index int
	gap   float64
}

func buildKnowledgePath(points []knowledgePoint, mastery []float64) ([]int, map[int]struct{}, string) {
	gaps := make([]knowledgeGap, 0)
	for index, value := range mastery {
		if gap := targetMastery - value; gap > 0 {
			gaps = append(gaps, knowledgeGap{index: index, gap: gap})
		}
	}
	if len(gaps) == 0 {
		return nil, map[int]struct{}{}, "mastery_target_satisfied"
	}
	slices.SortFunc(gaps, func(left, right knowledgeGap) int {
		if left.gap > right.gap {
			return -1
		}
		if left.gap < right.gap {
			return 1
		}
		return strings.Compare(points[left.index].ID, points[right.index].ID)
	})
	if len(gaps) > maximumPathSteps {
		gaps = gaps[:maximumPathSteps]
	}
	indices := make(map[string]int, len(points))
	for index, point := range points {
		indices[point.ID] = index
	}
	direct := make(map[int]struct{}, len(gaps))
	closure := make(map[int]struct{})
	var include func(int)
	include = func(index int) {
		if _, exists := closure[index]; exists {
			return
		}
		closure[index] = struct{}{}
		for _, prerequisite := range points[index].PrerequisiteIDs {
			include(indices[prerequisite])
		}
	}
	for _, gap := range gaps {
		direct[gap.index] = struct{}{}
		include(gap.index)
		if len(closure) > maximumPathSteps {
			return topologicalPath(points, closure), direct, "path_exceeds_maximum"
		}
	}
	path := topologicalPath(points, closure)
	if len(path) < minimumPathSteps {
		return path, direct, "path_below_minimum"
	}
	return path, direct, ""
}

func topologicalPath(points []knowledgePoint, nodes map[int]struct{}) []int {
	indices := make(map[string]int, len(points))
	for index, point := range points {
		indices[point.ID] = index
	}
	indegree := make(map[int]int, len(nodes))
	dependents := make(map[int][]int, len(nodes))
	for index := range nodes {
		for _, prerequisiteID := range points[index].PrerequisiteIDs {
			prerequisite := indices[prerequisiteID]
			if _, included := nodes[prerequisite]; included {
				indegree[index]++
				dependents[prerequisite] = append(dependents[prerequisite], index)
			}
		}
	}
	available := make([]int, 0)
	for index := range nodes {
		if indegree[index] == 0 {
			available = append(available, index)
		}
	}
	sort.Slice(available, func(i, j int) bool { return points[available[i]].ID < points[available[j]].ID })
	result := make([]int, 0, len(nodes))
	for len(available) > 0 {
		index := available[0]
		available = available[1:]
		result = append(result, index)
		for _, dependent := range dependents[index] {
			indegree[dependent]--
			if indegree[dependent] == 0 {
				available = append(available, dependent)
			}
		}
		sort.Slice(available, func(i, j int) bool { return points[available[i]].ID < points[available[j]].ID })
	}
	return result
}

type rankedCandidate struct {
	index      int
	score      float64
	gap        float64
	distance   float64
	stepWeight float64
}

func rankPathProblems(points []knowledgePoint, mastery []float64, candidates []inferenceCandidate, path []int, direct map[int]struct{}) ([]RecommendationLearningPathStep, []string) {
	rankedByStep := make([][]rankedCandidate, len(path))
	for order, knowledgeIndex := range path {
		point := points[knowledgeIndex]
		ranked := make([]rankedCandidate, 0)
		for index, candidate := range candidates {
			stepWeight := candidate.weightByKnowledge[point.ID]
			if stepWeight <= 0 {
				continue
			}
			gap := 0.0
			for masteryIndex, knowledge := range points {
				gap += candidate.weightByKnowledge[knowledge.ID] * math.Max(targetMastery-mastery[masteryIndex], 0)
			}
			distance := math.Abs(candidate.probability - targetSuccessProbability)
			ranked = append(ranked, rankedCandidate{index: index, score: gap - distance, gap: gap, distance: distance, stepWeight: stepWeight})
		}
		slices.SortFunc(ranked, func(left, right rankedCandidate) int { return compareRankedCandidate(left, right, candidates) })
		rankedByStep[order] = ranked
	}

	selectedByStep, blockedOrders := assignPathCandidates(candidates, rankedByStep)
	if len(blockedOrders) > 0 {
		blocked := make([]string, len(blockedOrders))
		for index, order := range blockedOrders {
			blocked[index] = points[path[order]].ID
		}
		return nil, blocked
	}

	steps := make([]RecommendationLearningPathStep, len(path))
	for order, knowledgeIndex := range path {
		point := points[knowledgeIndex]
		selected := make([]RecommendationProblem, 0, problemsPerStep)
		for _, value := range selectedByStep[order] {
			candidate := candidates[value.index]
			selected = append(selected, RecommendationProblem{
				ProblemKey: candidate.ProblemKey, SourceProblemKey: candidate.SourceProblemKey, Platform: candidate.Platform,
				ProblemID: candidate.ProblemID, Title: candidate.Title, SourceProblemSets: slices.Clone(candidate.SourceProblemSets),
				PredictedSuccessProbability: candidate.probability, RecommendationScore: value.score,
				RankingEvidence: RecommendationRankingEvidence{KnowledgeGap: value.gap, SuccessDistance: value.distance, StepKnowledgeWeight: value.stepWeight},
			})
		}
		reason := "prerequisite"
		if _, isDirect := direct[knowledgeIndex]; isDirect {
			reason = "knowledge_gap"
		}
		steps[order] = RecommendationLearningPathStep{
			Order: int64(order + 1), KnowledgePointID: point.ID, Label: point.Label, Description: point.Description,
			PrerequisiteIDs: slices.Clone(point.PrerequisiteIDs), Mastery: mastery[knowledgeIndex], TargetMastery: targetMastery,
			ReasonCode: reason, RecommendedProblems: selected,
		}
	}
	return steps, nil
}

func compareRankedCandidate(left, right rankedCandidate, candidates []inferenceCandidate) int {
	if left.score > right.score {
		return -1
	}
	if left.score < right.score {
		return 1
	}
	return strings.Compare(candidates[left.index].ProblemKey, candidates[right.index].ProblemKey)
}

type pathCandidateChoice struct {
	value     rankedCandidate
	available bool
}

// assignPathCandidates solves the complete path as one capacity-constrained
// assignment. Each path step owns three slots and each source problem can fill
// at most one slot across the whole path. The rectangular Hungarian assignment
// maximizes the total recommendation score; sorted source and problem keys make
// equal-score outcomes deterministic.
func assignPathCandidates(candidates []inferenceCandidate, rankedByStep [][]rankedCandidate) ([][]rankedCandidate, []int) {
	rowCount := len(rankedByStep) * problemsPerStep
	if rowCount == 0 {
		return [][]rankedCandidate{}, nil
	}
	sourceSet := make(map[string]struct{})
	for _, ranked := range rankedByStep {
		for _, value := range ranked {
			sourceSet[candidates[value.index].SourceProblemKey] = struct{}{}
		}
	}
	sources := make([]string, 0, len(sourceSet))
	for source := range sourceSet {
		sources = append(sources, source)
	}
	slices.Sort(sources)
	sourceIndex := make(map[string]int, len(sources))
	for index, source := range sources {
		sourceIndex[source] = index
	}

	choices := make([][]pathCandidateChoice, len(rankedByStep))
	for step, ranked := range rankedByStep {
		choices[step] = make([]pathCandidateChoice, len(sources))
		for _, value := range ranked {
			column := sourceIndex[candidates[value.index].SourceProblemKey]
			current := choices[step][column]
			if !current.available || compareRankedCandidate(value, current.value, candidates) < 0 {
				choices[step][column] = pathCandidateChoice{value: value, available: true}
			}
		}
	}

	columnCount := max(len(sources), rowCount)
	const unavailableCost = 1_000_000.0
	costs := make([][]float64, rowCount)
	for row := range costs {
		costs[row] = make([]float64, columnCount)
		for column := range costs[row] {
			costs[row][column] = unavailableCost
		}
		step := row / problemsPerStep
		for column, choice := range choices[step] {
			if choice.available {
				costs[row][column] = -choice.value.score
			}
		}
	}

	columnsByRow := minimumCostAssignment(costs)
	selected := make([][]rankedCandidate, len(rankedByStep))
	blockedSet := make(map[int]struct{})
	for row, column := range columnsByRow {
		step := row / problemsPerStep
		if column < 0 || column >= len(sources) || !choices[step][column].available {
			blockedSet[step] = struct{}{}
			continue
		}
		selected[step] = append(selected[step], choices[step][column].value)
	}
	blocked := make([]int, 0, len(blockedSet))
	for step := range rankedByStep {
		if _, missing := blockedSet[step]; missing || len(selected[step]) != problemsPerStep {
			blocked = append(blocked, step)
		}
		slices.SortFunc(selected[step], func(left, right rankedCandidate) int {
			return compareRankedCandidate(left, right, candidates)
		})
	}
	return selected, blocked
}

// minimumCostAssignment returns one distinct column for every row. It uses the
// O(rows^2*columns) rectangular Hungarian algorithm and requires rows <= columns.
func minimumCostAssignment(costs [][]float64) []int {
	rowCount := len(costs)
	columnCount := len(costs[0])
	rowPotential := make([]float64, rowCount+1)
	columnPotential := make([]float64, columnCount+1)
	rowByColumn := make([]int, columnCount+1)
	predecessor := make([]int, columnCount+1)

	for row := 1; row <= rowCount; row++ {
		rowByColumn[0] = row
		column := 0
		minimum := make([]float64, columnCount+1)
		for index := 1; index <= columnCount; index++ {
			minimum[index] = math.Inf(1)
		}
		used := make([]bool, columnCount+1)
		for {
			used[column] = true
			activeRow := rowByColumn[column]
			delta := math.Inf(1)
			nextColumn := 0
			for candidateColumn := 1; candidateColumn <= columnCount; candidateColumn++ {
				if used[candidateColumn] {
					continue
				}
				reduced := costs[activeRow-1][candidateColumn-1] - rowPotential[activeRow] - columnPotential[candidateColumn]
				if reduced < minimum[candidateColumn] {
					minimum[candidateColumn] = reduced
					predecessor[candidateColumn] = column
				}
				if minimum[candidateColumn] < delta {
					delta = minimum[candidateColumn]
					nextColumn = candidateColumn
				}
			}
			for candidateColumn := 0; candidateColumn <= columnCount; candidateColumn++ {
				if used[candidateColumn] {
					rowPotential[rowByColumn[candidateColumn]] += delta
					columnPotential[candidateColumn] -= delta
				} else {
					minimum[candidateColumn] -= delta
				}
			}
			column = nextColumn
			if rowByColumn[column] == 0 {
				break
			}
		}
		for {
			previousColumn := predecessor[column]
			rowByColumn[column] = rowByColumn[previousColumn]
			column = previousColumn
			if column == 0 {
				break
			}
		}
	}

	assignment := make([]int, rowCount)
	for row := range assignment {
		assignment[row] = -1
	}
	for column := 1; column <= columnCount; column++ {
		if rowByColumn[column] > 0 {
			assignment[rowByColumn[column]-1] = column - 1
		}
	}
	return assignment
}

func eligibleProblemCount(points []knowledgePoint, candidates []inferenceCandidate, path []int) int64 {
	if len(path) == 0 {
		return 0
	}
	pathIDs := make(map[string]struct{}, len(path))
	for _, index := range path {
		pathIDs[points[index].ID] = struct{}{}
	}
	sources := make(map[string]struct{})
	for _, candidate := range candidates {
		for knowledgeID, weight := range candidate.weightByKnowledge {
			if weight > 0 {
				if _, included := pathIDs[knowledgeID]; included {
					sources[candidate.SourceProblemKey] = struct{}{}
					break
				}
			}
		}
	}
	return int64(len(sources))
}

type inferenceResultBody struct {
	Status           RecommendationResultStatus       `json:"status"`
	SourceRating     float64                          `json:"sourceRating"`
	Evidence         RecommendationInferenceEvidence  `json:"evidence"`
	KnowledgeMastery []RecommendationKnowledgeMastery `json:"knowledgeMastery"`
	LearningPath     []RecommendationLearningPathStep `json:"learningPath,omitempty"`
	Insufficiency    *RecommendationInsufficiency     `json:"insufficiency,omitempty"`
}

func setResultSHA256(result *StudentRecommendationInferenceResult) error {
	body := inferenceResultBody{
		Status: result.Status, SourceRating: result.SourceRating, Evidence: result.Evidence,
		KnowledgeMastery: result.KnowledgeMastery, LearningPath: result.LearningPath, Insufficiency: result.Insufficiency,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	_, digest, err := canonicaljson.Object(raw, maximumResultBytes)
	if err != nil {
		return err
	}
	result.SHA256 = digest
	return nil
}
