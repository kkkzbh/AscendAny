package recommendation

import (
	"bytes"
	"encoding/json"
	"math"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/kkkzbh/AscendAny/backend/internal/canonicaljson"
)

func TestParseOutputBundleV2MaterializesReadyPath(t *testing.T) {
	t.Parallel()
	input := outputTestInput(t, []int{2, 2}, json.Number("0.8"), 2, 4, 2, json.Number("0"))
	output := outputTestBundle(t, input)
	parsed, err := ParseOutputBundle(output, 8<<20, input)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.InputManifestSHA256 != input.ManifestSHA256 || parsed.Model.Schema != ModelSchemaV2 || len(parsed.Results) != 1 {
		t.Fatalf("parsed provenance=%#v", parsed)
	}
	if parsed.Results[0].ActorID != 11 || parsed.Results[0].Schema != ResultSchemaV2 {
		t.Fatalf("student output=%#v", parsed.Results[0])
	}
	_, digest, err := canonicaljson.Object(parsed.Results[0].Result, maxStudentResultBytes)
	if err != nil || digest != parsed.Results[0].ResultSHA256 {
		t.Fatalf("result digest=%q err=%v", digest, err)
	}
	stored, err := parseStudentRecommendationResultV2(parsed.Results[0].Result, parsed.Results[0].ResultSHA256)
	if err != nil {
		t.Fatal(err)
	}
	stored.SourceRating = "-1"
	if err := ValidateStudentRecommendationResultV2(stored); err == nil {
		t.Fatal("negative source rating was accepted")
	}
	var result studentResultBodyV2
	if err := decodeStrict(parsed.Results[0].Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != RecommendationResultReady || result.Insufficiency != nil || len(result.LearningPath) != 2 {
		t.Fatalf("result=%#v", result)
	}
	if result.Evidence.TrainInteractionCount != 4 || result.Evidence.ValidationInteractionCount != 4 ||
		result.Evidence.DistinctProblemCount != 4 || result.Evidence.PassedProblemCount != 0 {
		t.Fatalf("evidence=%#v", result.Evidence)
	}
	if result.LearningPath[0].KnowledgePointID != "k1" || result.LearningPath[1].KnowledgePointID != "k2" ||
		result.LearningPath[0].Order != 1 || result.LearningPath[1].Order != 2 {
		t.Fatalf("path order=%#v", result.LearningPath)
	}
	for _, step := range result.LearningPath {
		if step.ReasonCode != "knowledge_gap" || len(step.RecommendedProblems) != 2 {
			t.Fatalf("step=%#v", step)
		}
		for _, problem := range step.RecommendedProblems {
			if !closeNumber(problem.PredictedSuccessProbability, 0.5) ||
				!closeNumber(problem.RankingEvidence.KnowledgeGap, 0.3) ||
				!closeNumber(problem.RankingEvidence.SuccessDistance, 0.2) ||
				!closeNumber(problem.RankingEvidence.StepKnowledgeWeight, 1) ||
				!closeNumber(problem.RecommendationScore, 0.1) {
				t.Fatalf("ranked problem=%#v", problem)
			}
		}
	}
	if result.KnowledgeMastery[0].TrainInteractionCount != 2 || result.KnowledgeMastery[1].TrainInteractionCount != 2 {
		t.Fatalf("knowledge evidence=%#v", result.KnowledgeMastery)
	}
}

func TestParseOutputBundleV2MaterializesEveryInsufficiencyReason(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		problems       []int
		target         json.Number
		minimumSteps   int64
		maximumSteps   int64
		maximumTargets int64
		reason         string
		candidateSteps int64
		eligible       int64
		blocked        []string
	}{
		{name: "mastery target satisfied", problems: []int{2, 2}, target: "0.4", minimumSteps: 2, maximumSteps: 4, maximumTargets: 2, reason: "mastery_target_satisfied", candidateSteps: 0, eligible: 0, blocked: []string{}},
		{name: "path below minimum", problems: []int{2}, target: "0.8", minimumSteps: 2, maximumSteps: 4, maximumTargets: 1, reason: "path_below_minimum", candidateSteps: 1, eligible: 2, blocked: []string{}},
		{name: "path exceeds maximum", problems: []int{2, 2, 2, 2}, target: "0.8", minimumSteps: 2, maximumSteps: 2, maximumTargets: 4, reason: "path_exceeds_maximum", candidateSteps: 3, eligible: 6, blocked: []string{}},
		{name: "problem candidates below minimum", problems: []int{2, 1}, target: "0.8", minimumSteps: 2, maximumSteps: 4, maximumTargets: 2, reason: "problem_candidates_below_minimum", candidateSteps: 2, eligible: 3, blocked: []string{"k2"}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			input := outputTestInput(t, test.problems, test.target, test.minimumSteps, test.maximumSteps, test.maximumTargets, "0")
			parsed, err := ParseOutputBundle(outputTestBundle(t, input), 8<<20, input)
			if err != nil {
				t.Fatal(err)
			}
			var result studentResultBodyV2
			if err := decodeStrict(parsed.Results[0].Result, &result); err != nil {
				t.Fatal(err)
			}
			if result.Status != RecommendationResultInsufficient || result.Insufficiency == nil || result.LearningPath != nil {
				t.Fatalf("result=%#v", result)
			}
			insufficient := result.Insufficiency
			if insufficient.ReasonCode != test.reason || insufficient.CandidatePathSteps != test.candidateSteps ||
				insufficient.EligibleProblemCount != test.eligible ||
				!slices.Equal(insufficient.BlockedKnowledgePointIDs, test.blocked) {
				t.Fatalf("insufficiency=%#v", insufficient)
			}
		})
	}
}

func TestParseOutputBundleV2ExcludesEveryFactVersionOfPassedSource(t *testing.T) {
	t.Parallel()
	input := outputTestInput(t, []int{2, 2}, "0.8", 2, 4, 2, "0")
	input.Problems[1].SourceProblemKey = input.Problems[0].SourceProblemKey
	input.Problems[1].Platform = input.Problems[0].Platform
	input.Problems[1].ProblemID = input.Problems[0].ProblemID
	for index := range input.Interactions {
		if input.Interactions[index].Split == "validation" && input.Interactions[index].ProblemKey == input.Problems[0].ProblemKey {
			input.Interactions[index].Passed = true
			input.Interactions[index].TargetScoreRate = "1"
			break
		}
	}
	parsed, err := ParseOutputBundle(outputTestBundle(t, input), 8<<20, input)
	if err != nil {
		t.Fatal(err)
	}
	var result studentResultBodyV2
	if err := decodeStrict(parsed.Results[0].Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.Evidence.DistinctProblemCount != 3 || result.Evidence.PassedProblemCount != 1 ||
		result.Insufficiency == nil || result.Insufficiency.ReasonCode != "problem_candidates_below_minimum" ||
		result.Insufficiency.EligibleProblemCount != 2 || !slices.Equal(result.Insufficiency.BlockedKnowledgePointIDs, []string{"k1"}) {
		t.Fatalf("result=%#v", result)
	}
}

func TestParseOutputBundleV2RejectsMalformedHashProvenanceAndMetrics(t *testing.T) {
	t.Parallel()
	baseInput := outputTestInput(t, []int{2, 2}, "0.8", 2, 4, 2, "0")
	baseOutput := outputTestBundle(t, baseInput)
	tests := []struct {
		name   string
		input  ParsedInputBundle
		mutate func(map[string]any)
	}{
		{
			name:   "unknown field",
			mutate: func(value map[string]any) { value["unexpected"] = true },
		},
		{
			name: "parameter hash mismatch",
			mutate: func(value map[string]any) {
				problems := value["model"].(map[string]any)["parameters"].(map[string]any)["problems"].([]any)
				problems[0].(map[string]any)["difficultyResidual"] = json.Number("1")
			},
		},
		{
			name: "runtime provenance mismatch",
			mutate: func(value map[string]any) {
				value["model"].(map[string]any)["manifest"].(map[string]any)["torchVersion"] = "2.13.0"
			},
		},
		{
			name: "metric recomputation mismatch",
			mutate: func(value map[string]any) {
				value["model"].(map[string]any)["diagnostics"].(map[string]any)["reportedValidationLogLoss"] = json.Number("0.7")
			},
		},
		{
			name: "normalization mismatch with updated hash",
			mutate: func(value map[string]any) {
				model := value["model"].(map[string]any)
				parameters := model["parameters"].(map[string]any)
				parameters["normalization"].(map[string]any)["actorMeans"] = []any{1, 0, 0, 0}
				_, digest := outputCanonicalRaw(t, parameters)
				model["manifest"].(map[string]any)["parameterSha256"] = digest
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var value map[string]any
			decoder := json.NewDecoder(bytes.NewReader(baseOutput))
			decoder.UseNumber()
			if err := decoder.Decode(&value); err != nil {
				t.Fatal(err)
			}
			test.mutate(value)
			input := baseInput
			if test.input.ManifestSHA256 != "" {
				input = test.input
			}
			if _, err := ParseOutputBundle(outputCanonicalObject(t, value), 8<<20, input); CodeOf(err) != ErrorInvalidBundle {
				t.Fatalf("error=%v code=%q", err, CodeOf(err))
			}
		})
	}
	if _, err := ParseOutputBundle(append(slices.Clone(baseOutput), '\n'), 8<<20, baseInput); CodeOf(err) != ErrorInvalidBundle {
		t.Fatalf("noncanonical error=%v code=%q", err, CodeOf(err))
	}
	cpuInput := baseInput
	cpuInput.TrainingConfiguration.Accelerator = "cpu"
	if _, err := ParseOutputBundle(baseOutput, 8<<20, cpuInput); CodeOf(err) != ErrorInvalidBundle {
		t.Fatalf("CPU accelerator error=%v code=%q", err, CodeOf(err))
	}
}

func TestParseOutputBundleV2EnforcesConfiguredQualityGate(t *testing.T) {
	t.Parallel()
	input := outputTestInput(t, []int{2, 2}, "0.8", 2, 4, 2, "0.000000000001")
	if _, err := ParseOutputBundle(outputTestBundle(t, input), 8<<20, input); CodeOf(err) != ErrorInvalidBundle {
		t.Fatalf("quality error=%v code=%q", err, CodeOf(err))
	}
}

func TestOutputBundleV2UsesRelativeMetricTolerance(t *testing.T) {
	t.Parallel()
	initial := json.Number("100.00000005")
	final := json.Number("100")
	baseline := json.Number("100")
	validation := json.Number("100")
	brier := json.Number("0.25")
	reported := outputDiagnosticsV2Wire{
		InitialTrainLogLoss: &initial, FinalTrainLogLoss: &final,
		ReportedBaselineValidationLogLoss: &baseline, ReportedValidationLogLoss: &validation,
		ReportedValidationBrier: &brier,
	}
	actual := modelQualityMetricsV2{
		initialTrainLogLoss: 100, finalTrainLogLoss: 100, baselineValidationLogLoss: 100,
		validationLogLoss: 100, validationBrier: 0.25,
	}
	configuration := ParsedTrainingConfiguration{
		Validation: TrainingValidationConfiguration{MinRelativeLogLossImprovement: "0"},
	}
	if err := verifyModelQuality(reported, actual, configuration); err != nil {
		t.Fatal(err)
	}
	outside := json.Number("100.0000002")
	reported.InitialTrainLogLoss = &outside
	if err := verifyModelQuality(reported, actual, configuration); err == nil {
		t.Fatal("metric outside scaled 1e-9 tolerance was accepted")
	}
}

func TestOutputBundleV2AggregatesProblemKnowledgeGap(t *testing.T) {
	t.Parallel()
	evaluator := outputModelEvaluatorV2{
		problemKnowledge: [][]weightedKnowledgeIndex{{
			{index: 0, weight: 0.25},
			{index: 1, weight: 0.75},
		}},
	}
	gap := evaluator.problemKnowledgeGap(0, []float64{0.2, 0.7}, 0.8)
	if math.Abs(gap-0.225) > 1e-15 {
		t.Fatalf("gap=%v", gap)
	}
}

func outputTestInput(
	t *testing.T,
	problemsPerKnowledge []int,
	targetMastery json.Number,
	minimumSteps, maximumSteps, maximumTargets int64,
	minimumImprovement json.Number,
) ParsedInputBundle {
	t.Helper()
	knowledge := make([]TrainingKnowledgePoint, len(problemsPerKnowledge))
	for index := range knowledge {
		prerequisites := []string{}
		if index > 0 {
			prerequisites = []string{"k" + strconv.Itoa(index)}
		}
		knowledge[index] = TrainingKnowledgePoint{
			ID: "k" + strconv.Itoa(index+1), Label: "Knowledge " + strconv.Itoa(index+1),
			Description: "Description " + strconv.Itoa(index+1), PrerequisiteIDs: prerequisites,
		}
	}
	problems := make([]TrainingProblemInput, 0)
	interactions := make([]TrainingInteractionInput, 0)
	problemNumber := 0
	for knowledgeIndex, count := range problemsPerKnowledge {
		for range count {
			problemNumber++
			problemID := strconv.Itoa(100 + problemNumber)
			factHash := strings.Repeat(strconv.FormatInt(int64(problemNumber%15+1), 16), 64)
			sourceKey := "pintia:" + problemID
			problemKey := sourceKey + ":" + factHash
			problems = append(problems, TrainingProblemInput{
				ProblemKey: problemKey, SourceProblemKey: sourceKey, ProblemFactSHA256: factHash,
				Platform: "pintia", ProblemID: problemID, Title: "Problem " + problemID,
				StatementText: "Statement", SourceProblemSets: []TrainingSourceProblemSet{{ProblemSetID: "900", SourceURL: "https://pintia.cn/problem-sets/900"}},
				MaxScore: "100", KnowledgeWeights: []TrainingKnowledgeWeight{{KnowledgePointID: knowledge[knowledgeIndex].ID, Weight: "1"}},
				Features: []float64{0, 0, 0}, TrainActorCount: 1, TrainSubmissionCount: 1,
			})
			for _, split := range []string{"train", "validation"} {
				interactions = append(interactions, TrainingInteractionInput{
					InteractionID: strings.Repeat(strconv.FormatInt(int64(len(interactions)%15+1), 16), 64),
					SnapshotID:    "1", ActorID: "11", ProblemKey: problemKey, SubmissionCount: 1,
					ValidSubmissionCount: 1, TargetScoreRate: "0.5", Passed: false, Split: split,
				})
			}
		}
	}
	featureSchema := TrainingFeatureSchema{
		ActorFeatureIDs:   slices.Clone(trainingFeatureSchemaV2.ActorFeatureIDs),
		ProblemFeatureIDs: slices.Clone(trainingFeatureSchemaV2.ProblemFeatureIDs),
	}
	manifest := inputManifestWire{
		Protocol: TrainingBundleProtocolV2,
		Source: inputManifestSource{
			AnalyticsGenerationID: "1", AnalyticsHeadRevision: 1,
			AnalyticsInputManifestSHA256: strings.Repeat("1", 64), AlgorithmVersion: "analytics_v2",
			AnalyticsConfigurationSHA256: strings.Repeat("2", 64),
		},
		TrainingConfiguration: inputManifestConfiguration{
			VersionID: "1", Key: "recommendation.training.test", VersionNumber: 1,
			SchemaID: trainingConfigurationSchemaV2, DocumentSHA256: strings.Repeat("3", 64),
		},
		KnowledgeCatalog: inputManifestConfiguration{
			VersionID: "2", Key: "recommendation.catalog.test", VersionNumber: 1,
			SchemaID: knowledgeCatalogSchemaV1, DocumentSHA256: strings.Repeat("4", 64),
		},
		FeatureSchemaSHA256: strings.Repeat("5", 64), KnowledgePointCount: len(knowledge),
		KnowledgePointSetSHA256: strings.Repeat("6", 64), ActorCount: 1, ActorSetSHA256: strings.Repeat("7", 64),
		ProblemCount: len(problems), ProblemSetSHA256: strings.Repeat("8", 64),
		InteractionCount: len(interactions), InteractionSetSHA256: strings.Repeat("9", 64),
		TrainInteractionCount: len(problems), ValidationInteractionCount: len(problems), SplitSHA256: strings.Repeat("a", 64),
	}
	manifestJSON, manifestSHA256 := outputCanonicalRaw(t, manifest)
	return ParsedInputBundle{
		Manifest: manifestJSON, ManifestSHA256: manifestSHA256, FeatureSchema: featureSchema,
		TrainingConfiguration: ParsedTrainingConfiguration{
			Algorithm: trainingAlgorithmV2, KnowledgeCatalogVersionID: 2, Accelerator: productionAccelerator,
			Seed: 17, Epochs: 4, Patience: 2, BatchSize: 1, LearningRate: "0.01", WeightDecay: "0",
			MinTrainInteractions: 1, MinActorInteractions: 1, MinProblemInteractions: 1,
			Validation: TrainingValidationConfiguration{MinActors: 1, MinInteractions: 1, MinRelativeLogLossImprovement: minimumImprovement},
			PathPolicy: TrainingPathPolicyConfiguration{
				TargetMastery: targetMastery, MaxKnowledgeTargets: maximumTargets, MinSteps: minimumSteps,
				MaxSteps: maximumSteps, ProblemsPerStep: 2, TargetSuccessProbability: "0.7",
			},
			RankingWeights: TrainingRankingWeightsConfiguration{KnowledgeGap: "1", SuccessDistance: "1"},
		},
		KnowledgeCatalog: ParsedKnowledgeCatalog{TaxonomyID: "recommendation.test", KnowledgePoints: slices.Clone(knowledge)},
		Actors:           []TrainingActorInput{{ActorID: "11", CurrentRating: json.RawMessage("1000"), Features: []float64{0, 0, 0, 0}}},
		KnowledgePoints:  knowledge, Problems: problems, Interactions: interactions,
		ActorIDs: []int64{11},
	}
}

func outputTestBundle(t *testing.T, input ParsedInputBundle) json.RawMessage {
	t.Helper()
	actorFeatureCount := len(input.FeatureSchema.ActorFeatureIDs)
	problemFeatureCount := len(input.FeatureSchema.ProblemFeatureIDs)
	actorMeans, actorScales, err := populationNormalization(actorFeatureRows(input.Actors), actorFeatureCount)
	if err != nil {
		t.Fatal(err)
	}
	problemMeans, problemScales, err := populationNormalization(problemFeatureRows(input.Problems), problemFeatureCount)
	if err != nil {
		t.Fatal(err)
	}
	studentWeights := make([][]float64, len(input.KnowledgePoints))
	for index := range studentWeights {
		studentWeights[index] = make([]float64, actorFeatureCount)
	}
	actorResiduals := make([]any, len(input.Actors))
	for index, actor := range input.Actors {
		actorResiduals[index] = map[string]any{
			"actorId": actor.ActorID,
			"values":  make([]float64, len(input.KnowledgePoints)),
		}
	}
	rawDiscrimination := json.Number(strconv.FormatFloat(math.Log(math.Expm1(1-discriminationEpsilon)), 'g', -1, 64))
	discrimination := stableSoftplus(mustOutputFloat(t, rawDiscrimination)) + discriminationEpsilon
	trainTargets := make(map[string][]float64, len(input.Problems))
	validationTargets := make(map[string][]float64, len(input.Problems))
	for _, interaction := range input.Interactions {
		target := mustOutputFloat(t, interaction.TargetScoreRate)
		if interaction.Split == "train" {
			trainTargets[interaction.ProblemKey] = append(trainTargets[interaction.ProblemKey], target)
		} else if interaction.Split == "validation" {
			validationTargets[interaction.ProblemKey] = append(validationTargets[interaction.ProblemKey], target)
		}
	}
	problemParameters := make([]any, len(input.Problems))
	for index, problem := range input.Problems {
		probability := 0.0
		if targets := validationTargets[problem.ProblemKey]; len(targets) > 0 {
			probability = accuratelySum(targets) / float64(len(targets))
		} else {
			targets := trainTargets[problem.ProblemKey]
			probability = (accuratelySum(targets) + 1) / float64(len(targets)+2)
		}
		probability = math.Max(1e-6, math.Min(1-1e-6, probability))
		logit := math.Log(probability) - math.Log1p(-probability)
		problemParameters[index] = map[string]any{
			"problemKey": problem.ProblemKey, "difficultyResidual": resultNumber(-logit / discrimination),
			"rawDiscrimination": rawDiscrimination,
		}
	}
	parameters := map[string]any{
		"normalization": map[string]any{
			"actorMeans": actorMeans, "actorScales": actorScales,
			"problemMeans": problemMeans, "problemScales": problemScales,
		},
		"studentFeatureWeights": studentWeights,
		"actorResiduals":        actorResiduals,
		"problemFeatureWeights": make([]float64, problemFeatureCount), "problems": problemParameters,
	}
	parameterJSON, parameterSHA256 := outputCanonicalRaw(t, parameters)
	parsedParameters, err := parseOutputParameters(parameterJSON, 8<<20, parameterSHA256, input)
	if err != nil {
		t.Fatal(err)
	}
	evaluator, err := newOutputModelEvaluator(input, parsedParameters)
	if err != nil {
		t.Fatal(err)
	}
	metrics, err := evaluator.recomputeQualityMetrics()
	if err != nil {
		t.Fatal(err)
	}
	var inputManifest inputManifestWire
	if err := decodeStrict(input.Manifest, &inputManifest); err != nil {
		t.Fatal(err)
	}
	trainCount, validationCount := interactionSplitCounts(input.Interactions)
	return outputCanonicalObject(t, map[string]any{
		"protocol": TrainingOutputProtocolV2, "inputManifestSha256": input.ManifestSHA256,
		"model": map[string]any{
			"schema": ModelSchemaV2,
			"manifest": map[string]any{
				"algorithm": trainingAlgorithmV2, "parameterSchema": recommendationParameterSchemaV1,
				"parameterSha256": parameterSHA256, "inputManifestSha256": input.ManifestSHA256,
				"trainingConfigurationSha256": inputManifest.TrainingConfiguration.DocumentSHA256,
				"knowledgeCatalogSha256":      inputManifest.KnowledgeCatalog.DocumentSHA256,
				"featureSchemaSha256":         inputManifest.FeatureSchemaSHA256, "splitSha256": inputManifest.SplitSHA256,
				"knowledgePointCount": len(input.KnowledgePoints), "actorFeatureCount": len(input.FeatureSchema.ActorFeatureIDs),
				"problemFeatureCount": len(input.FeatureSchema.ProblemFeatureIDs), "actorCount": len(input.Actors),
				"problemCount": len(input.Problems), "trainInteractionCount": trainCount,
				"validationInteractionCount": validationCount, "torchVersion": productionTorchVersion,
				"runtimeConstructionSha256": strings.Repeat("a", 64),
				"runtimeProvenanceSha256":   strings.Repeat("b", 64),
				"runtimeTreeSha256":         strings.Repeat("c", 64),
				"hostCapabilitySha256":      strings.Repeat("d", 64),
				"runtimeAttestationSha256":  strings.Repeat("e", 64),
				"accelerator":               productionAccelerator, "seed": input.TrainingConfiguration.Seed,
				"configuredEpochs": input.TrainingConfiguration.Epochs, "bestEpoch": 1,
				"actorFeatureIds": input.FeatureSchema.ActorFeatureIDs, "problemFeatureIds": input.FeatureSchema.ProblemFeatureIDs,
			},
			"parameters": parameters,
			"diagnostics": map[string]any{
				"epochsCompleted": 1, "bestEpoch": 1, "initialTrainLogLoss": resultNumber(metrics.initialTrainLogLoss),
				"finalTrainLogLoss":                 resultNumber(metrics.finalTrainLogLoss),
				"reportedBaselineValidationLogLoss": resultNumber(metrics.baselineValidationLogLoss),
				"reportedValidationLogLoss":         resultNumber(metrics.validationLogLoss),
				"reportedValidationBrier":           resultNumber(metrics.validationBrier),
			},
		},
	})
}

func mustOutputFloat(t *testing.T, value json.Number) float64 {
	t.Helper()
	parsed, err := strconv.ParseFloat(string(value), 64)
	if err != nil || !finiteFloat(parsed) {
		t.Fatalf("invalid output test number %q", value)
	}
	return parsed
}

func outputCanonicalRaw(t *testing.T, value any) (json.RawMessage, string) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	canonical, digest, err := canonicaljson.Object(raw, 8<<20)
	if err != nil {
		t.Fatal(err)
	}
	return canonical, digest
}

func outputCanonicalObject(t *testing.T, value any) json.RawMessage {
	t.Helper()
	canonical, _ := outputCanonicalRaw(t, value)
	return canonical
}

func closeNumber(value json.Number, expected float64) bool {
	parsed, err := strconv.ParseFloat(string(value), 64)
	return err == nil && math.Abs(parsed-expected) < 1e-12
}
