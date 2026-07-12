package recommendation

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/kkkzbh/AscendAny/backend/internal/canonicaljson"
)

type problemFactWire struct {
	Platform         string          `json:"platform"`
	ProblemID        string          `json:"problemId"`
	Title            string          `json:"title"`
	ContentHTML      *string         `json:"contentHtml"`
	MaxScore         json.RawMessage `json:"maxScore"`
	TimeLimitMS      *int64          `json:"timeLimitMs"`
	MemoryLimitBytes *int64          `json:"memoryLimitBytes"`
}

func buildReviewProblemCandidates(rows []TrainingProblem) ([]ReviewProblemCandidate, error) {
	if len(rows) == 0 || len(rows) > maximumTrainingProblems {
		return nil, domainError(ErrorStoredDataInvalid, true, "build recommendation review context", errors.New("bounded nonempty problem rows are required"))
	}
	byKey := make(map[string]*ReviewProblemCandidate)
	sourceSets := make(map[string]map[TrainingSourceProblemSet]struct{})
	seenInstances := make(map[snapshotProblemKey]struct{}, len(rows))
	for index, row := range rows {
		if row.SnapshotID <= 0 || !canonicalDecimalID(row.ProblemSetID) || !canonicalSourceID(row.ProblemSetProblemID) || !canonicalPintiaURL(row.SourceURL) {
			return nil, domainError(ErrorStoredDataInvalid, true, "build recommendation review context", fmt.Errorf("problem instance %d has invalid provenance", index))
		}
		instance := snapshotProblemKey{snapshotID: row.SnapshotID, problemSetProblemID: row.ProblemSetProblemID}
		if _, exists := seenInstances[instance]; exists {
			return nil, domainError(ErrorStoredDataInvalid, true, "build recommendation review context", errors.New("snapshot problem identity is duplicated"))
		}
		seenInstances[instance] = struct{}{}
		fact, err := buildCanonicalProblemFact(row)
		if err != nil {
			return nil, domainError(ErrorStoredDataInvalid, true, "build recommendation review context", fmt.Errorf("problem instance %d: %w", index, err))
		}
		if _, exists := byKey[fact.ProblemKey]; !exists {
			byKey[fact.ProblemKey] = &ReviewProblemCandidate{
				ProblemKey: fact.ProblemKey, SourceProblemKey: fact.SourceProblemKey,
				ProblemFactSHA256: fact.ProblemFactSHA256, Platform: row.Platform, ProblemID: row.ProblemID,
				Title: row.Title,
			}
			sourceSets[fact.ProblemKey] = make(map[TrainingSourceProblemSet]struct{})
		}
		sourceSets[fact.ProblemKey][TrainingSourceProblemSet{ProblemSetID: row.ProblemSetID, SourceURL: row.SourceURL}] = struct{}{}
	}
	result := make([]ReviewProblemCandidate, 0, len(byKey))
	for key, candidate := range byKey {
		sets := make([]TrainingSourceProblemSet, 0, len(sourceSets[key]))
		for source := range sourceSets[key] {
			sets = append(sets, source)
		}
		slices.SortFunc(sets, compareSourceProblemSet)
		candidate.SourceProblemSets = sets
		result = append(result, *candidate)
	}
	slices.SortFunc(result, func(left, right ReviewProblemCandidate) int {
		return strings.Compare(left.ProblemKey, right.ProblemKey)
	})
	return result, nil
}

type canonicalProblemFact struct {
	SourceProblemKey  string
	ProblemKey        string
	ProblemFactSHA256 string
	MaxScore          json.Number
}

// buildCanonicalProblemFact is the single owner of the immutable problem-fact
// identity used by both catalog review and training bundle construction.
func buildCanonicalProblemFact(row TrainingProblem) (canonicalProblemFact, error) {
	if row.Platform != "pintia" || !canonicalSourceID(row.ProblemID) || !canonicalText(row.Title, 4096) {
		return canonicalProblemFact{}, errors.New("problem fact has invalid identity or title")
	}
	if row.ContentHTML != nil && (len(*row.ContentHTML) > maximumContentHTMLBytes || strings.ContainsRune(*row.ContentHTML, 0)) {
		return canonicalProblemFact{}, errors.New("problem fact contentHtml is invalid")
	}
	if row.TimeLimitMS != nil && *row.TimeLimitMS < 0 || row.MemoryLimitBytes != nil && *row.MemoryLimitBytes < 0 {
		return canonicalProblemFact{}, errors.New("problem fact has negative limits")
	}
	if row.MaxScore == nil {
		return canonicalProblemFact{}, errors.New("problem fact has no maxScore")
	}
	maxScore, err := canonicalPositiveNumber(*row.MaxScore, "maxScore")
	if err != nil {
		return canonicalProblemFact{}, err
	}
	factBytes, err := json.Marshal(problemFactWire{
		Platform: row.Platform, ProblemID: row.ProblemID, Title: row.Title, ContentHTML: row.ContentHTML,
		MaxScore: maxScore, TimeLimitMS: row.TimeLimitMS, MemoryLimitBytes: row.MemoryLimitBytes,
	})
	if err != nil {
		return canonicalProblemFact{}, fmt.Errorf("encode problem fact: %w", err)
	}
	_, digest, err := canonicaljson.Object(factBytes, maximumContentHTMLBytes+16<<10)
	if err != nil {
		return canonicalProblemFact{}, fmt.Errorf("canonicalize problem fact: %w", err)
	}
	sourceProblemKey := row.Platform + ":" + row.ProblemID
	return canonicalProblemFact{
		SourceProblemKey:  sourceProblemKey,
		ProblemKey:        sourceProblemKey + ":" + digest,
		ProblemFactSHA256: digest,
		MaxScore:          json.Number(maxScore),
	}, nil
}
