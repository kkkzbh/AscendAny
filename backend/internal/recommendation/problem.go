package recommendation

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"slices"
	"strconv"
	"strings"

	"github.com/kkkzbh/AscendAny/backend/internal/pintia"
)

type problemRow struct {
	SnapshotID          int64
	ProblemSetID        string
	ProblemSetProblemID string
	SourceURL           string
	Platform            string
	ProblemID           string
	Title               string
	ContentHTML         *string
	MaxScore            *string
	TimeLimitMS         *int64
	MemoryLimitBytes    *int64
	MetricsJSON         json.RawMessage
}

type problemFactWire struct {
	Platform         string          `json:"platform"`
	ProblemID        string          `json:"problemId"`
	Title            string          `json:"title"`
	ContentHTML      *string         `json:"contentHtml"`
	MaxScore         json.RawMessage `json:"maxScore"`
	TimeLimitMS      *int64          `json:"timeLimitMs"`
	MemoryLimitBytes *int64          `json:"memoryLimitBytes"`
}

type problemFact struct {
	ProblemKey        string
	SourceProblemKey  string
	ProblemFactSHA256 string
	MaxScore          *float64
}

func buildProblemFact(row problemRow) (problemFact, error) {
	if row.SnapshotID <= 0 || !pintia.ValidID(row.ProblemSetID) || !pintia.ValidID(row.ProblemSetProblemID) ||
		!canonicalPintiaURL(row.SourceURL) || row.Platform != "pintia" || !pintia.ValidID(row.ProblemID) || !canonicalText(row.Title, 4096) {
		return problemFact{}, errors.New("problem provenance or identity is invalid")
	}
	if row.ContentHTML != nil && (len(*row.ContentHTML) > maximumContentHTMLBytes || strings.ContainsRune(*row.ContentHTML, 0)) {
		return problemFact{}, errors.New("problem contentHtml is invalid")
	}
	if row.TimeLimitMS != nil && *row.TimeLimitMS < 0 || row.MemoryLimitBytes != nil && *row.MemoryLimitBytes < 0 {
		return problemFact{}, errors.New("problem resource limit is negative")
	}
	maxScoreJSON := json.RawMessage("null")
	var maxScore *float64
	if row.MaxScore != nil {
		canonical, parsed, err := nonnegativeFiniteNumber(*row.MaxScore, "maxScore")
		if err != nil {
			return problemFact{}, err
		}
		maxScoreJSON = canonical
		maxScore = &parsed
	}
	raw, err := json.Marshal(problemFactWire{
		Platform: row.Platform, ProblemID: row.ProblemID, Title: row.Title, ContentHTML: row.ContentHTML,
		MaxScore: maxScoreJSON, TimeLimitMS: row.TimeLimitMS, MemoryLimitBytes: row.MemoryLimitBytes,
	})
	if err != nil {
		return problemFact{}, fmt.Errorf("encode problem fact: %w", err)
	}
	_, digest, err := canonicalObject(raw, "", maximumContentHTMLBytes+16<<10, "problem fact")
	if err != nil {
		return problemFact{}, err
	}
	sourceKey := pintiaSourceProblemKey(row.ProblemID)
	return problemFact{
		ProblemKey: pintiaProblemKey(row.ProblemID, digest), SourceProblemKey: sourceKey,
		ProblemFactSHA256: digest, MaxScore: maxScore,
	}, nil
}

func nonnegativeFiniteNumber(value, label string) (json.RawMessage, float64, error) {
	rational, ok := new(big.Rat).SetString(value)
	parsed, err := strconv.ParseFloat(value, 64)
	if !ok || err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) || parsed < 0 || rational.Sign() < 0 {
		return nil, 0, fmt.Errorf("%s must be a finite nonnegative number", label)
	}
	raw := json.RawMessage(value)
	container := json.RawMessage(`{"value":` + value + `}`)
	canonical, _, err := canonicalObject(container, "", 16<<10, label)
	if err != nil {
		return nil, 0, err
	}
	var object struct {
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(canonical, &object); err != nil {
		return nil, 0, err
	}
	raw = append(json.RawMessage(nil), object.Value...)
	return raw, parsed, nil
}

func buildReviewCandidates(rows []problemRow) ([]ReviewProblemCandidate, error) {
	if len(rows) < 1 || len(rows) > maximumProblems {
		return nil, errors.New("review problem count is invalid")
	}
	byKey := make(map[string]*ReviewProblemCandidate)
	sourceSets := make(map[string]map[RecommendationSourceSet]struct{})
	seenInstances := make(map[string]struct{}, len(rows))
	for index, row := range rows {
		instance := strconv.FormatInt(row.SnapshotID, 10) + ":" + row.ProblemSetProblemID
		if _, duplicate := seenInstances[instance]; duplicate {
			return nil, errors.New("snapshot problem identity is duplicated")
		}
		seenInstances[instance] = struct{}{}
		fact, err := buildProblemFact(row)
		if err != nil {
			return nil, fmt.Errorf("problem %d: %w", index, err)
		}
		candidate := byKey[fact.ProblemKey]
		if candidate == nil {
			candidate = &ReviewProblemCandidate{
				ProblemKey: fact.ProblemKey, SourceProblemKey: fact.SourceProblemKey, Platform: row.Platform,
				ProblemID: row.ProblemID, ProblemFactSHA256: fact.ProblemFactSHA256, Title: row.Title,
			}
			byKey[fact.ProblemKey] = candidate
			sourceSets[fact.ProblemKey] = make(map[RecommendationSourceSet]struct{})
		} else if candidate.Title != row.Title || candidate.ProblemID != row.ProblemID {
			return nil, errors.New("equal problem fact hash has inconsistent fields")
		}
		sourceSets[fact.ProblemKey][RecommendationSourceSet{ProblemSetID: row.ProblemSetID, SourceURL: row.SourceURL}] = struct{}{}
	}
	result := make([]ReviewProblemCandidate, 0, len(byKey))
	for key, candidate := range byKey {
		sets := make([]RecommendationSourceSet, 0, len(sourceSets[key]))
		for source := range sourceSets[key] {
			sets = append(sets, source)
		}
		slices.SortFunc(sets, compareSourceSet)
		candidate.SourceProblemSets = sets
		result = append(result, *candidate)
	}
	slices.SortFunc(result, func(left, right ReviewProblemCandidate) int {
		return strings.Compare(left.ProblemKey, right.ProblemKey)
	})
	return result, nil
}

func compareSourceSet(left, right RecommendationSourceSet) int {
	if comparison := len(left.ProblemSetID) - len(right.ProblemSetID); comparison != 0 {
		return comparison
	}
	if comparison := strings.Compare(left.ProblemSetID, right.ProblemSetID); comparison != 0 {
		return comparison
	}
	return strings.Compare(left.SourceURL, right.SourceURL)
}
