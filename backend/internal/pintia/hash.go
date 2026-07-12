package pintia

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

// CanonicalDomainV1 returns the exact deterministic typed encoding used by
// DomainHash. It is exposed so protocol fixtures can be reproduced by other
// implementations.
func CanonicalDomainV1(ctx context.Context, snapshot *Snapshot) ([]byte, error) {
	if ctx == nil {
		return nil, fmt.Errorf("domain hash context is required")
	}
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	if snapshot == nil {
		return nil, fmt.Errorf("snapshot is nil")
	}
	if err := validateSemantics(snapshot); err != nil {
		return nil, fmt.Errorf("snapshot is not semantically valid: %w", err)
	}
	if err := validateDomainPrimitives(snapshot); err != nil {
		return nil, err
	}

	problems := make([]domainProblemV1, 0, len(snapshot.Problems))
	for _, problem := range snapshot.Problems {
		if err := context.Cause(ctx); err != nil {
			return nil, err
		}
		problems = append(problems, domainProblemV1{
			ProblemSetProblemID: problem.ProblemSetProblemID,
			ProblemID:           problem.ProblemID,
			Label:               problem.Label,
			Title:               problem.Title,
			Type:                problem.Type,
			MaxScore:            domainDecimal(problem.MaxScore),
			ContentHTML:         problem.ContentHTML,
			TimeLimitMS:         domainInteger(problem.TimeLimitMS),
			MemoryLimitBytes:    domainInteger(problem.MemoryLimitBytes),
		})
	}
	sort.Slice(problems, func(i, j int) bool {
		return problems[i].ProblemSetProblemID < problems[j].ProblemSetProblemID
	})

	participants := make([]domainParticipantV1, 0, len(snapshot.Participants))
	for _, participant := range snapshot.Participants {
		if err := context.Cause(ctx); err != nil {
			return nil, err
		}
		participants = append(participants, domainParticipantV1{
			UserID:        participant.UserID,
			StudentUserID: participant.StudentUserID,
			StudentNumber: participant.StudentNumber,
			DisplayName:   participant.DisplayName,
			GroupName:     participant.GroupName,
			Ranking:       domainRanking(participant.Ranking),
		})
	}
	sort.Slice(participants, func(i, j int) bool {
		return participants[i].UserID < participants[j].UserID
	})

	submissions := make([]domainSubmissionV1, 0, len(snapshot.Submissions))
	for _, submission := range snapshot.Submissions {
		if err := context.Cause(ctx); err != nil {
			return nil, err
		}
		caseResults := make([]domainCaseResultV1, 0, len(submission.CaseResults))
		for _, result := range submission.CaseResults {
			if err := context.Cause(ctx); err != nil {
				return nil, err
			}
			caseResults = append(caseResults, domainCaseResultV1{
				CaseID:      result.CaseID,
				Verdict:     result.Verdict,
				Score:       domainDecimal(result.Score),
				TimeMS:      domainInteger(result.TimeMS),
				MemoryBytes: domainInteger(result.MemoryBytes),
				Message:     result.Message,
			})
		}
		sort.Slice(caseResults, func(i, j int) bool {
			return caseResults[i].CaseID < caseResults[j].CaseID
		})
		submissions = append(submissions, domainSubmissionV1{
			SubmissionID:        submission.SubmissionID,
			ProblemSetProblemID: submission.ProblemSetProblemID,
			UserID:              submission.UserID,
			SubmittedAtEpochMS:  submission.SubmittedAt.UnixMilli(),
			Language:            submission.Language,
			Compiler:            submission.Compiler,
			Verdict:             submission.Verdict,
			Score:               domainDecimal(submission.Score),
			TimeMS:              domainInteger(submission.TimeMS),
			MemoryBytes:         domainInteger(submission.MemoryBytes),
			Code:                submission.Code,
			CodeSHA256:          submission.CodeSHA256,
			CompileLog:          submission.CompileLog,
			CaseResults:         caseResults,
		})
	}
	sort.Slice(submissions, func(i, j int) bool {
		return submissions[i].SubmissionID < submissions[j].SubmissionID
	})

	domain := domainEnvelopeV1{
		Protocol: DomainHashProtocolV1,
		Exam: domainExamV1{
			Platform:     snapshot.Exam.Platform,
			ProblemSetID: snapshot.Exam.ProblemSetID,
			Title:        snapshot.Exam.Title,
			StartsAt:     domainInstant(snapshot.Exam.StartsAt),
			EndsAt:       domainInstant(snapshot.Exam.EndsAt),
			TotalScore:   domainDecimal(snapshot.Exam.TotalScore),
		},
		Problems:     problems,
		Participants: participants,
		Submissions:  submissions,
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(domain); err != nil {
		return nil, fmt.Errorf("encode %s: %w", DomainHashProtocolV1, err)
	}
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	encoded := bytes.TrimSuffix(buffer.Bytes(), []byte{'\n'})
	return append([]byte(nil), encoded...), nil
}

func DomainHash(ctx context.Context, snapshot *Snapshot) (string, error) {
	encoded, err := CanonicalDomainV1(ctx, snapshot)
	if err != nil {
		return "", err
	}
	if err := context.Cause(ctx); err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func validateDomainPrimitives(snapshot *Snapshot) error {
	if snapshot.Exam.StartsAt != nil && snapshot.Exam.StartsAt.IsZero() {
		return fmt.Errorf("$.exam.startsAt is zero")
	}
	if snapshot.Exam.EndsAt != nil && snapshot.Exam.EndsAt.IsZero() {
		return fmt.Errorf("$.exam.endsAt is zero")
	}
	if err := validateDecimal(snapshot.Exam.TotalScore, "$.exam.totalScore"); err != nil {
		return err
	}
	for problemIndex, problem := range snapshot.Problems {
		if err := validateDecimal(problem.MaxScore, fmt.Sprintf("$.problems[%d].maxScore", problemIndex)); err != nil {
			return err
		}
	}
	for participantIndex, participant := range snapshot.Participants {
		if participant.Ranking == nil {
			continue
		}
		if err := validateDecimal(participant.Ranking.TotalScore, fmt.Sprintf("$.participants[%d].ranking.totalScore", participantIndex)); err != nil {
			return err
		}
		for resultIndex, result := range participant.Ranking.ProblemResults {
			if err := validateDecimal(result.Score, fmt.Sprintf("$.participants[%d].ranking.problemResults[%d].score", participantIndex, resultIndex)); err != nil {
				return err
			}
		}
	}
	for submissionIndex, submission := range snapshot.Submissions {
		if submission.SubmittedAt.IsZero() {
			return fmt.Errorf("$.submissions[%d].submittedAt is zero", submissionIndex)
		}
		if err := validateDecimal(submission.Score, fmt.Sprintf("$.submissions[%d].score", submissionIndex)); err != nil {
			return err
		}
		for caseIndex, result := range submission.CaseResults {
			if err := validateDecimal(result.Score, fmt.Sprintf("$.submissions[%d].caseResults[%d].score", submissionIndex, caseIndex)); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateDecimal(value *Decimal, path string) error {
	if value == nil {
		return nil
	}
	if _, err := value.PostgreSQLNumeric(); err != nil {
		return fmt.Errorf("%s cannot be represented by PostgreSQL numeric: %w", path, err)
	}
	return nil
}

func domainDecimal(value *Decimal) *string {
	if value == nil {
		return nil
	}
	canonical := value.String()
	return &canonical
}

func domainInteger(value *NonNegativeInteger) *uint64 {
	if value == nil {
		return nil
	}
	integer := value.Uint64()
	return &integer
}

func domainInstant(value *Instant) *int64 {
	if value == nil {
		return nil
	}
	milliseconds := value.UnixMilli()
	return &milliseconds
}

func domainRanking(ranking *Ranking) *domainRankingV1 {
	if ranking == nil {
		return nil
	}
	results := make([]domainRankingProblemResultV1, 0, len(ranking.ProblemResults))
	for _, result := range ranking.ProblemResults {
		results = append(results, domainRankingProblemResultV1{
			ProblemSetProblemID:  result.ProblemSetProblemID,
			Score:                domainDecimal(result.Score),
			Passed:               result.Passed,
			ValidSubmissionCount: domainInteger(result.ValidSubmissionCount),
			AcceptTimeSeconds:    result.AcceptTimeSeconds.Uint64(),
		})
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].ProblemSetProblemID < results[j].ProblemSetProblemID
	})
	return &domainRankingV1{
		Rank:            ranking.Rank.Uint64(),
		TotalScore:      domainDecimal(ranking.TotalScore),
		TimeUsedSeconds: domainInteger(ranking.TimeUsedSeconds),
		ProblemResults:  results,
	}
}

type domainEnvelopeV1 struct {
	Protocol     string                `json:"protocol"`
	Exam         domainExamV1          `json:"exam"`
	Problems     []domainProblemV1     `json:"problems"`
	Participants []domainParticipantV1 `json:"participants"`
	Submissions  []domainSubmissionV1  `json:"submissions"`
}

type domainExamV1 struct {
	Platform     string  `json:"platform"`
	ProblemSetID string  `json:"problemSetId"`
	Title        string  `json:"title"`
	StartsAt     *int64  `json:"startsAtEpochMs"`
	EndsAt       *int64  `json:"endsAtEpochMs"`
	TotalScore   *string `json:"totalScoreDecimal"`
}

type domainProblemV1 struct {
	ProblemSetProblemID string  `json:"problemSetProblemId"`
	ProblemID           string  `json:"problemId"`
	Label               *string `json:"label"`
	Title               string  `json:"title"`
	Type                string  `json:"type"`
	MaxScore            *string `json:"maxScoreDecimal"`
	ContentHTML         *string `json:"contentHtml"`
	TimeLimitMS         *uint64 `json:"timeLimitMs"`
	MemoryLimitBytes    *uint64 `json:"memoryLimitBytes"`
}

type domainParticipantV1 struct {
	UserID        string           `json:"userId"`
	StudentUserID *string          `json:"studentUserId"`
	StudentNumber *string          `json:"studentNumber"`
	DisplayName   *string          `json:"displayName"`
	GroupName     *string          `json:"groupName"`
	Ranking       *domainRankingV1 `json:"ranking"`
}

type domainRankingV1 struct {
	Rank            uint64                         `json:"rank"`
	TotalScore      *string                        `json:"totalScoreDecimal"`
	TimeUsedSeconds *uint64                        `json:"timeUsedSeconds"`
	ProblemResults  []domainRankingProblemResultV1 `json:"problemResults"`
}

type domainRankingProblemResultV1 struct {
	ProblemSetProblemID  string  `json:"problemSetProblemId"`
	Score                *string `json:"scoreDecimal"`
	Passed               *bool   `json:"passed"`
	ValidSubmissionCount *uint64 `json:"validSubmissionCount"`
	AcceptTimeSeconds    uint64  `json:"acceptTimeSeconds"`
}

type domainSubmissionV1 struct {
	SubmissionID        string               `json:"submissionId"`
	ProblemSetProblemID string               `json:"problemSetProblemId"`
	UserID              string               `json:"userId"`
	SubmittedAtEpochMS  int64                `json:"submittedAtEpochMs"`
	Language            *string              `json:"language"`
	Compiler            *string              `json:"compiler"`
	Verdict             string               `json:"verdict"`
	Score               *string              `json:"scoreDecimal"`
	TimeMS              *uint64              `json:"timeMs"`
	MemoryBytes         *uint64              `json:"memoryBytes"`
	Code                string               `json:"code"`
	CodeSHA256          string               `json:"codeSha256"`
	CompileLog          *string              `json:"compileLog"`
	CaseResults         []domainCaseResultV1 `json:"caseResults"`
}

type domainCaseResultV1 struct {
	CaseID      string  `json:"caseId"`
	Verdict     *string `json:"verdict"`
	Score       *string `json:"scoreDecimal"`
	TimeMS      *uint64 `json:"timeMs"`
	MemoryBytes *uint64 `json:"memoryBytes"`
	Message     *string `json:"message"`
}
