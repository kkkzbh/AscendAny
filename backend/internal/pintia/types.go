package pintia

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

const (
	// SchemaV2 is the only snapshot schema accepted by this package.
	SchemaV2 = "ascendany.pintia.snapshot.v2"
	// ExpectedSchemaSHA256 binds the binary to the reviewed contract bytes.
	ExpectedSchemaSHA256 = "85b8277dc4485019499ff3bcceb1715ea73f58197ebdff9487c9a5fb8f3ccdfa"

	// DomainHashProtocolV1 identifies the deterministic typed encoding hashed by
	// DomainHash. Changing the encoding requires a new protocol identifier.
	DomainHashProtocolV1 = "domain_hash_proto_v1"
)

// Snapshot is the typed representation of the Pintia snapshot v2 contract.
// Nullable contract fields use pointers so null remains distinct in the
// deterministic domain encoding.
type Snapshot struct {
	Schema       string        `json:"schema"`
	SchemaSHA256 string        `json:"schemaSha256"`
	Exporter     Exporter      `json:"exporter"`
	Exam         Exam          `json:"exam"`
	Problems     []Problem     `json:"problems"`
	Participants []Participant `json:"participants"`
	Submissions  []Submission  `json:"submissions"`
	Completeness Completeness  `json:"completeness"`
}

type Exporter struct {
	Name       string  `json:"name"`
	Version    string  `json:"version"`
	ExportedAt Instant `json:"exportedAt"`
}

type Exam struct {
	Platform     string   `json:"platform"`
	ProblemSetID string   `json:"problemSetId"`
	Title        string   `json:"title"`
	SourceURL    string   `json:"sourceUrl"`
	StartsAt     *Instant `json:"startsAt"`
	EndsAt       *Instant `json:"endsAt"`
	TotalScore   *Decimal `json:"totalScore"`
}

type Problem struct {
	ProblemSetProblemID string              `json:"problemSetProblemId"`
	ProblemID           string              `json:"problemId"`
	Label               *string             `json:"label"`
	Title               string              `json:"title"`
	Type                string              `json:"type"`
	MaxScore            *Decimal            `json:"maxScore"`
	ContentHTML         *string             `json:"contentHtml"`
	TimeLimitMS         *NonNegativeInteger `json:"timeLimitMs"`
	MemoryLimitBytes    *NonNegativeInteger `json:"memoryLimitBytes"`
}

type Participant struct {
	UserID        string  `json:"userId"`
	StudentUserID *string `json:"studentUserId"`
	StudentNumber *string `json:"studentNumber"`
	// DisplayName is exporter-defined snapshot data. Registration-capable official
	// exporter provenance (SemVer >=2.2.3 within major 2) guarantees PTA user.nickname.
	DisplayName *string  `json:"displayName"`
	GroupName   *string  `json:"groupName"`
	Ranking     *Ranking `json:"ranking"`
}

type Ranking struct {
	Rank            NonNegativeInteger     `json:"rank"`
	TotalScore      *Decimal               `json:"totalScore"`
	TimeUsedSeconds *NonNegativeInteger    `json:"timeUsedSeconds"`
	ProblemResults  []RankingProblemResult `json:"problemResults"`
}

type RankingProblemResult struct {
	ProblemSetProblemID  string              `json:"problemSetProblemId"`
	Score                *Decimal            `json:"score"`
	Passed               *bool               `json:"passed"`
	ValidSubmissionCount *NonNegativeInteger `json:"validSubmissionCount"`
	AcceptTimeSeconds    NonNegativeInteger  `json:"acceptTimeSeconds"`
}

type Submission struct {
	SubmissionID        string              `json:"submissionId"`
	ProblemSetProblemID string              `json:"problemSetProblemId"`
	UserID              string              `json:"userId"`
	SubmittedAt         Instant             `json:"submittedAt"`
	Language            *string             `json:"language"`
	Compiler            *string             `json:"compiler"`
	Verdict             string              `json:"verdict"`
	Score               *Decimal            `json:"score"`
	TimeMS              *NonNegativeInteger `json:"timeMs"`
	MemoryBytes         *NonNegativeInteger `json:"memoryBytes"`
	Code                string              `json:"code"`
	CodeSHA256          string              `json:"codeSha256"`
	CompileLog          *string             `json:"compileLog"`
	CaseResults         []CaseResult        `json:"caseResults"`
}

type CaseResult struct {
	CaseID      string              `json:"caseId"`
	Verdict     *string             `json:"verdict"`
	Score       *Decimal            `json:"score"`
	TimeMS      *NonNegativeInteger `json:"timeMs"`
	MemoryBytes *NonNegativeInteger `json:"memoryBytes"`
	Message     *string             `json:"message"`
}

type Completeness struct {
	Problems     CollectionCompleteness  `json:"problems"`
	Rankings     CollectionCompleteness  `json:"rankings"`
	Submissions  CollectionCompleteness  `json:"submissions"`
	Participants ParticipantCompleteness `json:"participants"`
}

type CollectionCompleteness struct {
	SourceReportedCount *NonNegativeInteger `json:"sourceReportedCount"`
	ObservedCount       NonNegativeInteger  `json:"observedCount"`
	ExportedCount       NonNegativeInteger  `json:"exportedCount"`
	PaginationExhausted bool                `json:"paginationExhausted"`
}

type ParticipantCompleteness struct {
	ExportedCount NonNegativeInteger `json:"exportedCount"`
}

// Instant stores a parsed RFC 3339 instant normalized to UTC. Validation only
// accepts offsets whose effective UTC offset is zero.
type Instant struct {
	time.Time
}

func (i *Instant) UnmarshalJSON(data []byte) error {
	var value string
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("timestamp must be an RFC 3339 string: %w", err)
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return fmt.Errorf("invalid RFC 3339 timestamp: %w", err)
	}
	_, offset := parsed.Zone()
	if offset != 0 {
		return fmt.Errorf("timestamp must use UTC offset zero")
	}
	i.Time = parsed.UTC()
	return nil
}

func (i Instant) MarshalJSON() ([]byte, error) {
	if i.Time.IsZero() {
		return nil, fmt.Errorf("timestamp is zero")
	}
	return json.Marshal(i.Time.UTC().Format(time.RFC3339Nano))
}

// NonNegativeInteger accepts every non-negative JSON number whose mathematical
// value is an integer and fits in uint64. This keeps 100, 100.0, and 1e2
// equivalent, as required by JSON Schema's numeric model.
type NonNegativeInteger struct {
	value uint64
}

func NewNonNegativeInteger(value uint64) NonNegativeInteger {
	return NonNegativeInteger{value: value}
}

func (n NonNegativeInteger) Uint64() uint64 {
	return n.value
}

// Int64 returns the exact PostgreSQL bigint representation. Snapshot semantic
// validation rejects values for which this conversion would fail.
func (n NonNegativeInteger) Int64() (int64, error) {
	if n.value > math.MaxInt64 {
		return 0, fmt.Errorf("non-negative integer %d exceeds PostgreSQL bigint", n.value)
	}
	return int64(n.value), nil
}

func (n *NonNegativeInteger) UnmarshalJSON(data []byte) error {
	decimal, err := parseDecimalBytes(data)
	if err != nil {
		return err
	}
	canonical := decimal.String()
	if strings.Contains(canonical, ".") || canonical[0] == '-' {
		return fmt.Errorf("number is not a non-negative integer")
	}
	value, err := strconv.ParseUint(canonical, 10, 64)
	if err != nil {
		return fmt.Errorf("non-negative integer exceeds uint64: %w", err)
	}
	n.value = value
	return nil
}

func (n NonNegativeInteger) MarshalJSON() ([]byte, error) {
	return []byte(strconv.FormatUint(n.value, 10)), nil
}
