package recommendation

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/kkkzbh/AscendAny/backend/internal/canonicaljson"
)

func parseStudentRecommendationResultV2(raw json.RawMessage, expectedSHA256 string) (StudentRecommendationResultV2, error) {
	canonical, digest, err := canonicaljson.Object(raw, maxStudentResultBytes)
	if err != nil || !bytes.Equal(canonical, raw) || digest != expectedSHA256 {
		return StudentRecommendationResultV2{}, errors.New("student result body is noncanonical or its hash differs")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(canonical, &fields); err != nil {
		return StudentRecommendationResultV2{}, err
	}
	var body studentResultBodyV2
	if err := decodeStrict(canonical, &body); err != nil {
		return StudentRecommendationResultV2{}, err
	}
	expectedFields := []string{"evidence", "knowledgeMastery", "sourceRating", "status"}
	switch body.Status {
	case RecommendationResultReady:
		expectedFields = append(expectedFields, "learningPath")
	case RecommendationResultInsufficient:
		expectedFields = append(expectedFields, "insufficiency")
	default:
		return StudentRecommendationResultV2{}, errors.New("student result status is unsupported")
	}
	if len(fields) != len(expectedFields) {
		return StudentRecommendationResultV2{}, errors.New("student result fields differ from its status contract")
	}
	for _, field := range expectedFields {
		if _, exists := fields[field]; !exists {
			return StudentRecommendationResultV2{}, fmt.Errorf("student result field %q is required", field)
		}
	}
	result := StudentRecommendationResultV2{
		Schema: ResultSchemaV2, SHA256: expectedSHA256, Status: body.Status,
		SourceRating: body.SourceRating, Evidence: body.Evidence,
		KnowledgeMastery: body.KnowledgeMastery, LearningPath: body.LearningPath,
		Insufficiency: body.Insufficiency,
	}
	if err := ValidateStudentRecommendationResultV2(result); err != nil {
		return StudentRecommendationResultV2{}, err
	}
	return result, nil
}

// ValidateStudentRecommendationResultV2 verifies the complete public result,
// including the SHA-256 of the canonical database body that excludes schema
// and sha256.
func ValidateStudentRecommendationResultV2(value StudentRecommendationResultV2) error {
	if value.Schema != ResultSchemaV2 || !lowercaseSHA256Pattern.MatchString(value.SHA256) {
		return errors.New("student result schema or hash is invalid")
	}
	if !validResultNumber(value.SourceRating, floatPointer(0), floatPointer(1000000)) {
		return errors.New("student result source rating is invalid")
	}
	if value.Evidence.TrainInteractionCount < 0 || value.Evidence.ValidationInteractionCount < 0 ||
		value.Evidence.DistinctProblemCount < 0 || value.Evidence.PassedProblemCount < 0 ||
		value.Evidence.PassedProblemCount > value.Evidence.DistinctProblemCount {
		return errors.New("student result evidence is invalid")
	}
	masteryByID, err := validateKnowledgeMasteryV2(value.KnowledgeMastery)
	if err != nil {
		return err
	}
	switch value.Status {
	case RecommendationResultReady:
		if value.Insufficiency != nil {
			return errors.New("ready student result contains insufficiency")
		}
		if err := validateLearningPathV2(value.LearningPath, masteryByID); err != nil {
			return err
		}
	case RecommendationResultInsufficient:
		if len(value.LearningPath) != 0 || value.Insufficiency == nil {
			return errors.New("insufficient student result contains a learning path or lacks its reason")
		}
		if err := validateInsufficiencyV2(*value.Insufficiency, masteryByID); err != nil {
			return err
		}
	default:
		return errors.New("student result status is unsupported")
	}
	body := studentResultBodyV2{
		Status: value.Status, SourceRating: value.SourceRating, Evidence: value.Evidence,
		KnowledgeMastery: value.KnowledgeMastery, LearningPath: value.LearningPath,
		Insufficiency: value.Insufficiency,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	_, digest, err := canonicaljson.Object(raw, maxStudentResultBytes)
	if err != nil || digest != value.SHA256 {
		return errors.New("student result hash differs from its canonical body")
	}
	return nil
}

func validateKnowledgeMasteryV2(values []RecommendationKnowledgeMasteryV2) (map[string]RecommendationKnowledgeMasteryV2, error) {
	if len(values) == 0 || len(values) > maximumKnowledgePoints {
		return nil, errors.New("student result knowledge mastery count is invalid")
	}
	byID := make(map[string]RecommendationKnowledgeMasteryV2, len(values))
	for index, value := range values {
		if !configurationKeyPattern.MatchString(value.KnowledgePointID) ||
			!canonicalText(value.Label, 256) || !canonicalText(value.Description, 4096) ||
			value.TrainInteractionCount < 0 || !validResultNumber(value.Mastery, floatPointer(0), floatPointer(1)) ||
			(index > 0 && value.KnowledgePointID <= values[index-1].KnowledgePointID) {
			return nil, fmt.Errorf("student result knowledge mastery %d is invalid", index)
		}
		for prerequisiteIndex, prerequisite := range value.PrerequisiteIDs {
			if !configurationKeyPattern.MatchString(prerequisite) || prerequisite == value.KnowledgePointID ||
				(prerequisiteIndex > 0 && prerequisite <= value.PrerequisiteIDs[prerequisiteIndex-1]) {
				return nil, fmt.Errorf("student result knowledge mastery %d has invalid prerequisites", index)
			}
		}
		byID[value.KnowledgePointID] = value
	}
	for _, value := range values {
		for _, prerequisite := range value.PrerequisiteIDs {
			if _, exists := byID[prerequisite]; !exists {
				return nil, errors.New("student result knowledge mastery has a dangling prerequisite")
			}
		}
	}
	return byID, nil
}

func validateLearningPathV2(path []RecommendationLearningPathStepV2, masteryByID map[string]RecommendationKnowledgeMasteryV2) error {
	if len(path) < 2 || len(path) > 8 {
		return errors.New("ready student result path length is invalid")
	}
	seenKnowledge := make(map[string]struct{}, len(path))
	seenSources := make(map[string]struct{})
	for index, step := range path {
		mastery, exists := masteryByID[step.KnowledgePointID]
		if !exists || step.Order != int64(index+1) || step.Label != mastery.Label ||
			step.Description != mastery.Description || !slices.Equal(step.PrerequisiteIDs, mastery.PrerequisiteIDs) ||
			string(step.Mastery) != string(mastery.Mastery) ||
			!validResultNumber(step.TargetMastery, floatPointer(math.SmallestNonzeroFloat64), floatPointer(math.Nextafter(1, 0))) ||
			len(step.RecommendedProblems) == 0 || len(step.RecommendedProblems) > 20 {
			return fmt.Errorf("student result learning path step %d is invalid", index)
		}
		if step.ReasonCode != "knowledge_gap" && step.ReasonCode != "prerequisite" {
			return fmt.Errorf("student result learning path step %d reason is invalid", index)
		}
		if _, duplicate := seenKnowledge[step.KnowledgePointID]; duplicate {
			return errors.New("student result learning path repeats a knowledge point")
		}
		for _, prerequisite := range step.PrerequisiteIDs {
			if _, ordered := seenKnowledge[prerequisite]; !ordered {
				return errors.New("student result learning path violates prerequisite order")
			}
		}
		seenKnowledge[step.KnowledgePointID] = struct{}{}
		for problemIndex, problem := range step.RecommendedProblems {
			if err := validateRecommendationProblemV2(problem, seenSources); err != nil {
				return fmt.Errorf("student result learning path step %d problem %d: %w", index, problemIndex, err)
			}
		}
	}
	return nil
}

func validateRecommendationProblemV2(value RecommendationProblemV2, seenSources map[string]struct{}) error {
	if value.Platform != "pintia" || !canonicalSourceID(value.ProblemID) ||
		value.SourceProblemKey != "pintia:"+value.ProblemID ||
		!strings.HasPrefix(value.ProblemKey, value.SourceProblemKey+":") ||
		!lowercaseSHA256Pattern.MatchString(strings.TrimPrefix(value.ProblemKey, value.SourceProblemKey+":")) ||
		!canonicalText(value.Title, 1024) || len(value.SourceProblemSets) == 0 ||
		!validResultNumber(value.PredictedSuccessProbability, floatPointer(0), floatPointer(1)) ||
		!validResultNumber(value.RecommendationScore, nil, nil) ||
		!validResultNumber(value.RankingEvidence.KnowledgeGap, floatPointer(0), nil) ||
		!validResultNumber(value.RankingEvidence.SuccessDistance, floatPointer(0), floatPointer(1)) ||
		!validResultNumber(value.RankingEvidence.StepKnowledgeWeight, floatPointer(math.SmallestNonzeroFloat64), floatPointer(1)) {
		return errors.New("recommended problem fields are invalid")
	}
	if _, duplicate := seenSources[value.SourceProblemKey]; duplicate {
		return errors.New("recommended source problem is repeated across the learning path")
	}
	seenSources[value.SourceProblemKey] = struct{}{}
	for index, source := range value.SourceProblemSets {
		if !canonicalDecimalID(source.ProblemSetID) || !canonicalPintiaURL(source.SourceURL) ||
			(index > 0 && compareSourceProblemSet(value.SourceProblemSets[index-1], source) >= 0) {
			return errors.New("recommended problem source set is invalid")
		}
	}
	return nil
}

func validateInsufficiencyV2(value RecommendationInsufficiencyV2, masteryByID map[string]RecommendationKnowledgeMasteryV2) error {
	if value.MinimumPathSteps < 2 || value.MinimumPathSteps > 8 || value.CandidatePathSteps < 0 ||
		value.CandidatePathSteps > maximumKnowledgePoints || value.ProblemsPerStep < 1 || value.ProblemsPerStep > 20 ||
		value.EligibleProblemCount < 0 {
		return errors.New("student result insufficiency counts are invalid")
	}
	seen := make(map[string]struct{}, len(value.BlockedKnowledgePointIDs))
	for _, knowledgeID := range value.BlockedKnowledgePointIDs {
		if _, exists := masteryByID[knowledgeID]; !exists {
			return errors.New("student result insufficiency references an unknown knowledge point")
		}
		if _, duplicate := seen[knowledgeID]; duplicate {
			return errors.New("student result insufficiency repeats a blocked knowledge point")
		}
		seen[knowledgeID] = struct{}{}
	}
	switch value.ReasonCode {
	case "mastery_target_satisfied":
		if value.CandidatePathSteps != 0 || value.EligibleProblemCount != 0 || len(value.BlockedKnowledgePointIDs) != 0 {
			return errors.New("mastery-target insufficiency fields are inconsistent")
		}
	case "path_below_minimum":
		if value.CandidatePathSteps >= value.MinimumPathSteps || len(value.BlockedKnowledgePointIDs) != 0 {
			return errors.New("short-path insufficiency fields are inconsistent")
		}
	case "path_exceeds_maximum":
		if value.CandidatePathSteps <= value.MinimumPathSteps || len(value.BlockedKnowledgePointIDs) != 0 {
			return errors.New("overlong-path insufficiency fields are inconsistent")
		}
	case "problem_candidates_below_minimum":
		if value.CandidatePathSteps < value.MinimumPathSteps || len(value.BlockedKnowledgePointIDs) == 0 {
			return errors.New("problem-candidate insufficiency fields are inconsistent")
		}
	default:
		return errors.New("student result insufficiency reason is unsupported")
	}
	return nil
}

func validResultNumber(value json.Number, minimum, maximum *float64) bool {
	if value == "" || strings.TrimSpace(string(value)) != string(value) {
		return false
	}
	parsed, err := strconv.ParseFloat(string(value), 64)
	if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) ||
		(minimum != nil && parsed < *minimum) || (maximum != nil && parsed > *maximum) {
		return false
	}
	encoded, err := json.Marshal(value)
	return err == nil && string(encoded) == string(value)
}

func floatPointer(value float64) *float64 {
	return &value
}
