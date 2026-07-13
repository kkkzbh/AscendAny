package pintia

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
)

func validateSemantics(snapshot *Snapshot) error {
	if snapshot.Schema != SchemaV2 {
		return validationError(ErrorSemanticViolation, "$.schema", "unsupported schema %q", snapshot.Schema)
	}
	if snapshot.Exam.StartsAt != nil && snapshot.Exam.EndsAt != nil && snapshot.Exam.StartsAt.After(snapshot.Exam.EndsAt.Time) {
		return validationError(ErrorSemanticViolation, "$.exam", "startsAt must not be after endsAt")
	}
	if strings.TrimSpace(snapshot.Exam.Title) == "" {
		return validationError(ErrorSemanticViolation, "$.exam.title", "must contain a non-whitespace character")
	}
	if err := validateExamSourceURL(snapshot.Exam.SourceURL, snapshot.Exam.ProblemSetID); err != nil {
		return err
	}

	problemSetProblemIDs := make(map[string]int, len(snapshot.Problems))
	problemIDs := make(map[string]int, len(snapshot.Problems))
	for index, problem := range snapshot.Problems {
		if strings.TrimSpace(problem.Title) == "" {
			return validationError(
				ErrorSemanticViolation,
				fmt.Sprintf("$.problems[%d].title", index),
				"must contain a non-whitespace character",
			)
		}
		if previous, exists := problemSetProblemIDs[problem.ProblemSetProblemID]; exists {
			return validationError(
				ErrorSemanticViolation,
				fmt.Sprintf("$.problems[%d].problemSetProblemId", index),
				"duplicates $.problems[%d].problemSetProblemId",
				previous,
			)
		}
		problemSetProblemIDs[problem.ProblemSetProblemID] = index
		if previous, exists := problemIDs[problem.ProblemID]; exists {
			return validationError(
				ErrorSemanticViolation,
				fmt.Sprintf("$.problems[%d].problemId", index),
				"duplicates $.problems[%d].problemId",
				previous,
			)
		}
		problemIDs[problem.ProblemID] = index
	}

	participantIDs := make(map[string]int, len(snapshot.Participants))
	unionIDs := make(map[string]struct{}, len(snapshot.Participants))
	rankingCount := uint64(0)
	for participantIndex, participant := range snapshot.Participants {
		if participant.StudentNumber != nil && strings.TrimSpace(*participant.StudentNumber) == "" {
			return validationError(
				ErrorSemanticViolation,
				fmt.Sprintf("$.participants[%d].studentNumber", participantIndex),
				"must be null or contain a non-whitespace character",
			)
		}
		if previous, exists := participantIDs[participant.UserID]; exists {
			return validationError(
				ErrorSemanticViolation,
				fmt.Sprintf("$.participants[%d].userId", participantIndex),
				"duplicates $.participants[%d].userId",
				previous,
			)
		}
		participantIDs[participant.UserID] = participantIndex
		if participant.Ranking == nil {
			continue
		}
		unionIDs[participant.UserID] = struct{}{}
		rankingCount++
		rankingProblemIDs := make(map[string]int, len(participant.Ranking.ProblemResults))
		for resultIndex, result := range participant.Ranking.ProblemResults {
			if _, exists := problemSetProblemIDs[result.ProblemSetProblemID]; !exists {
				return validationError(
					ErrorSemanticViolation,
					fmt.Sprintf("$.participants[%d].ranking.problemResults[%d].problemSetProblemId", participantIndex, resultIndex),
					"references unknown problemSetProblemId %q",
					result.ProblemSetProblemID,
				)
			}
			if previous, exists := rankingProblemIDs[result.ProblemSetProblemID]; exists {
				return validationError(
					ErrorSemanticViolation,
					fmt.Sprintf("$.participants[%d].ranking.problemResults[%d].problemSetProblemId", participantIndex, resultIndex),
					"duplicates problem result at index %d",
					previous,
				)
			}
			rankingProblemIDs[result.ProblemSetProblemID] = resultIndex

			problem := snapshot.Problems[problemSetProblemIDs[result.ProblemSetProblemID]]
			resultPath := fmt.Sprintf("$.participants[%d].ranking.problemResults[%d]", participantIndex, resultIndex)
			if err := validateRankingPassed(result, problem.MaxScore, resultPath); err != nil {
				return err
			}
		}
	}

	submissionIDs := make(map[string]int, len(snapshot.Submissions))
	for submissionIndex, submission := range snapshot.Submissions {
		if strings.TrimSpace(submission.Verdict) == "" {
			return validationError(
				ErrorSemanticViolation,
				fmt.Sprintf("$.submissions[%d].verdict", submissionIndex),
				"must contain a non-whitespace character",
			)
		}
		if previous, exists := submissionIDs[submission.SubmissionID]; exists {
			return validationError(
				ErrorSemanticViolation,
				fmt.Sprintf("$.submissions[%d].submissionId", submissionIndex),
				"duplicates $.submissions[%d].submissionId",
				previous,
			)
		}
		submissionIDs[submission.SubmissionID] = submissionIndex
		if _, exists := problemSetProblemIDs[submission.ProblemSetProblemID]; !exists {
			return validationError(
				ErrorSemanticViolation,
				fmt.Sprintf("$.submissions[%d].problemSetProblemId", submissionIndex),
				"references unknown problemSetProblemId %q",
				submission.ProblemSetProblemID,
			)
		}
		if _, exists := participantIDs[submission.UserID]; !exists {
			return validationError(
				ErrorSemanticViolation,
				fmt.Sprintf("$.submissions[%d].userId", submissionIndex),
				"references unknown userId %q",
				submission.UserID,
			)
		}
		unionIDs[submission.UserID] = struct{}{}

		if submission.Code == "" {
			return validationError(
				ErrorSemanticViolation,
				fmt.Sprintf("$.submissions[%d].code", submissionIndex),
				"programming submission code must be non-empty",
			)
		}
		codeDigest := sha256.Sum256([]byte(submission.Code))
		actualCodeDigest := hex.EncodeToString(codeDigest[:])
		if actualCodeDigest != submission.CodeSHA256 {
			return validationError(
				ErrorSemanticViolation,
				fmt.Sprintf("$.submissions[%d].codeSha256", submissionIndex),
				"got %q, want SHA-256(code) %q",
				submission.CodeSHA256,
				actualCodeDigest,
			)
		}

		caseIDs := make(map[string]int, len(submission.CaseResults))
		for caseIndex, result := range submission.CaseResults {
			if previous, exists := caseIDs[result.CaseID]; exists {
				return validationError(
					ErrorSemanticViolation,
					fmt.Sprintf("$.submissions[%d].caseResults[%d].caseId", submissionIndex, caseIndex),
					"duplicates case result at index %d",
					previous,
				)
			}
			caseIDs[result.CaseID] = caseIndex
		}
	}

	for participantIndex, participant := range snapshot.Participants {
		if _, exists := unionIDs[participant.UserID]; !exists {
			return validationError(
				ErrorSemanticViolation,
				fmt.Sprintf("$.participants[%d].userId", participantIndex),
				"participant is outside the ranking/submission user union",
			)
		}
	}
	if len(unionIDs) != len(participantIDs) {
		return validationError(ErrorSemanticViolation, "$.participants", "participant IDs do not equal the ranking/submission user union")
	}

	if err := validateCollectionCompleteness("problems", snapshot.Completeness.Problems, uint64(len(snapshot.Problems))); err != nil {
		return err
	}
	if err := validateCollectionCompleteness("rankings", snapshot.Completeness.Rankings, rankingCount); err != nil {
		return err
	}
	if err := validateCollectionCompleteness("submissions", snapshot.Completeness.Submissions, uint64(len(snapshot.Submissions))); err != nil {
		return err
	}
	if got := snapshot.Completeness.Participants.ExportedCount.Uint64(); got != uint64(len(snapshot.Participants)) {
		return validationError(
			ErrorSemanticViolation,
			"$.completeness.participants.exportedCount",
			"got %d, want participants length %d",
			got,
			len(snapshot.Participants),
		)
	}
	return validatePersistenceRepresentability(snapshot)
}

func validateExamSourceURL(sourceURL string, problemSetID string) error {
	parsed, err := url.Parse(sourceURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host != "pintia.cn" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.ForceQuery || strings.Contains(sourceURL, "#") {
		return validationError(
			ErrorSemanticViolation,
			"$.exam.sourceUrl",
			"must be an absolute https://pintia.cn problem-set URL without query or fragment",
		)
	}
	segments := strings.Split(strings.TrimPrefix(parsed.EscapedPath(), "/"), "/")
	if len(segments) < 2 || segments[0] != "problem-sets" {
		return validationError(
			ErrorSemanticViolation,
			"$.exam.sourceUrl",
			"must identify problemSetId %q",
			problemSetID,
		)
	}
	pathProblemSetID, err := url.PathUnescape(segments[1])
	if err != nil || pathProblemSetID != problemSetID {
		return validationError(
			ErrorSemanticViolation,
			"$.exam.sourceUrl",
			"must identify problemSetId %q",
			problemSetID,
		)
	}
	return nil
}

func validateRankingPassed(result RankingProblemResult, maxScore *Decimal, path string) error {
	if result.Score == nil || maxScore == nil {
		if result.Passed != nil {
			return validationError(ErrorSemanticViolation, path+".passed", "must be null when score or problem maxScore is null")
		}
		return nil
	}
	expected := compareNonNegativeDecimals(*result.Score, *maxScore) >= 0
	if result.Passed == nil || *result.Passed != expected {
		return validationError(
			ErrorSemanticViolation,
			path+".passed",
			"must equal score >= problem maxScore (%t)",
			expected,
		)
	}
	return nil
}

func compareNonNegativeDecimals(left Decimal, right Decimal) int {
	return compareNonNegativeDecimalText(left.String(), right.String())
}

func validatePersistenceRepresentability(snapshot *Snapshot) error {
	validateInteger := func(value *NonNegativeInteger, path string) error {
		if value == nil {
			return nil
		}
		if _, err := value.Int64(); err != nil {
			return validationError(ErrorSemanticViolation, path, "%v", err)
		}
		return nil
	}
	validateRequiredInteger := func(value NonNegativeInteger, path string) error {
		return validateInteger(&value, path)
	}
	validateNumeric := func(value *Decimal, path string) error {
		if value == nil {
			return nil
		}
		if _, err := value.AnalyticsFloat64(); err != nil {
			return validationError(ErrorSemanticViolation, path, "%v", err)
		}
		if _, err := value.PostgreSQLNumeric(); err != nil {
			return validationError(ErrorSemanticViolation, path, "%v", err)
		}
		return nil
	}

	if err := validateNumeric(snapshot.Exam.TotalScore, "$.exam.totalScore"); err != nil {
		return err
	}
	for problemIndex, problem := range snapshot.Problems {
		basePath := fmt.Sprintf("$.problems[%d]", problemIndex)
		if err := validateNumeric(problem.MaxScore, basePath+".maxScore"); err != nil {
			return err
		}
		if err := validateInteger(problem.TimeLimitMS, basePath+".timeLimitMs"); err != nil {
			return err
		}
		if err := validateInteger(problem.MemoryLimitBytes, basePath+".memoryLimitBytes"); err != nil {
			return err
		}
	}
	for participantIndex, participant := range snapshot.Participants {
		if participant.Ranking == nil {
			continue
		}
		basePath := fmt.Sprintf("$.participants[%d].ranking", participantIndex)
		if err := validateRequiredInteger(participant.Ranking.Rank, basePath+".rank"); err != nil {
			return err
		}
		if err := validateNumeric(participant.Ranking.TotalScore, basePath+".totalScore"); err != nil {
			return err
		}
		if err := validateInteger(participant.Ranking.TimeUsedSeconds, basePath+".timeUsedSeconds"); err != nil {
			return err
		}
		for resultIndex, result := range participant.Ranking.ProblemResults {
			resultPath := fmt.Sprintf("%s.problemResults[%d]", basePath, resultIndex)
			if err := validateNumeric(result.Score, resultPath+".score"); err != nil {
				return err
			}
			if err := validateInteger(result.ValidSubmissionCount, resultPath+".validSubmissionCount"); err != nil {
				return err
			}
			if err := validateRequiredInteger(result.AcceptTimeSeconds, resultPath+".acceptTimeSeconds"); err != nil {
				return err
			}
		}
	}
	for submissionIndex, submission := range snapshot.Submissions {
		basePath := fmt.Sprintf("$.submissions[%d]", submissionIndex)
		if err := validateNumeric(submission.Score, basePath+".score"); err != nil {
			return err
		}
		if err := validateInteger(submission.TimeMS, basePath+".timeMs"); err != nil {
			return err
		}
		if err := validateInteger(submission.MemoryBytes, basePath+".memoryBytes"); err != nil {
			return err
		}
		for caseIndex, result := range submission.CaseResults {
			casePath := fmt.Sprintf("%s.caseResults[%d]", basePath, caseIndex)
			if err := validateNumeric(result.Score, casePath+".score"); err != nil {
				return err
			}
			if err := validateInteger(result.TimeMS, casePath+".timeMs"); err != nil {
				return err
			}
			if err := validateInteger(result.MemoryBytes, casePath+".memoryBytes"); err != nil {
				return err
			}
		}
	}
	collections := []struct {
		path  string
		value CollectionCompleteness
	}{
		{"$.completeness.problems", snapshot.Completeness.Problems},
		{"$.completeness.rankings", snapshot.Completeness.Rankings},
		{"$.completeness.submissions", snapshot.Completeness.Submissions},
	}
	for _, collection := range collections {
		if err := validateInteger(collection.value.SourceReportedCount, collection.path+".sourceReportedCount"); err != nil {
			return err
		}
		if err := validateRequiredInteger(collection.value.ObservedCount, collection.path+".observedCount"); err != nil {
			return err
		}
		if err := validateRequiredInteger(collection.value.ExportedCount, collection.path+".exportedCount"); err != nil {
			return err
		}
	}
	return validateRequiredInteger(
		snapshot.Completeness.Participants.ExportedCount,
		"$.completeness.participants.exportedCount",
	)
}

func validateCollectionCompleteness(name string, completeness CollectionCompleteness, expectedExported uint64) error {
	path := "$.completeness." + name
	if !completeness.PaginationExhausted {
		return validationError(ErrorSemanticViolation, path+".paginationExhausted", "must be true")
	}
	exported := completeness.ExportedCount.Uint64()
	observed := completeness.ObservedCount.Uint64()
	if exported != expectedExported {
		return validationError(
			ErrorSemanticViolation,
			path+".exportedCount",
			"got %d, want exported array count %d",
			exported,
			expectedExported,
		)
	}
	if observed < exported {
		return validationError(
			ErrorSemanticViolation,
			path+".observedCount",
			"got %d, which is below exportedCount %d",
			observed,
			exported,
		)
	}
	if completeness.SourceReportedCount != nil && completeness.SourceReportedCount.Uint64() != observed {
		return validationError(
			ErrorSemanticViolation,
			path+".sourceReportedCount",
			"got %d, want observedCount %d for exhausted pagination",
			completeness.SourceReportedCount.Uint64(),
			observed,
		)
	}
	return nil
}
