package recommendation

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/kkkzbh/AscendAny/backend/internal/canonicaljson"
	"github.com/kkkzbh/AscendAny/backend/internal/inferencemodel"
)

const (
	KnowledgeCatalogSchemaV1 = "ascendany.knowledge_catalog.recommendation.v1"
	FeatureSchemaV1          = "ascendany.recommendation.feature-schema.v1"
	// MaximumKnowledgeCatalogBytes is the immutable release-file and
	// configuration-document boundary shared by catalog publication.
	MaximumKnowledgeCatalogBytes = 256 << 10

	maximumConfigurationBytes = MaximumKnowledgeCatalogBytes
	maximumManifestBytes      = 1 << 20
	maximumResultBytes        = 4 << 20
	maximumContentHTMLBytes   = 4 << 20
	maximumKnowledgePoints    = inferencemodel.MaximumKnowledgePoints
	maximumProblems           = 10000
)

var (
	configurationKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,127}$`)
	lowercaseSHA256Pattern  = regexp.MustCompile(`^[0-9a-f]{64}$`)
	canonicalUUIDv4Pattern  = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	canonicalWeightPattern  = regexp.MustCompile(`^(1|0\.[0-9]*[1-9])$`)
)

type featureSchemaWire struct {
	Schema          string                  `json:"schema"`
	ActorFeatures   []featureDefinitionWire `json:"actorFeatures"`
	ProblemFeatures []featureDefinitionWire `json:"problemFeatures"`
}

type featureDefinitionWire struct {
	ID          string                 `json:"id"`
	Source      featureSourceWire      `json:"source"`
	Aggregation featureAggregationWire `json:"aggregation"`
	Missing     featureMissingWire     `json:"missing"`
	Transform   featureTransformWire   `json:"transform"`
	Domain      featureDomainWire      `json:"domain"`
}

type featureSourceWire struct {
	Protocol string   `json:"protocol"`
	Fields   []string `json:"fields"`
}

type featureAggregationWire struct {
	Scope     string `json:"scope"`
	Operation string `json:"operation"`
}

type featureMissingWire struct {
	Policy          string  `json:"policy"`
	PairedFeatureID *string `json:"pairedFeatureId"`
}

type featureTransformWire struct {
	Operation string `json:"operation"`
}

type featureDomainWire struct {
	SourceType      string  `json:"sourceType"`
	SourceMinimum   *string `json:"sourceMinimum"`
	SourceMaximum   *string `json:"sourceMaximum"`
	OutputType      string  `json:"outputType"`
	OutputMinimum   *string `json:"outputMinimum"`
	OutputMaximum   *string `json:"outputMaximum"`
	DenominatorZero string  `json:"denominatorZero"`
}

const (
	studentAnalyticsProtocol = "student_analytics_v1"
	pintiaSnapshotProtocol   = "ascendany.pintia.snapshot.v2"
	actorAggregationScope    = "analytics_generation_actor"
	problemAggregationScope  = "analytics_generation_problem_fact"
	maximumInt64Decimal      = "9223372036854775807"
	maximumFloat64Decimal    = "1.7976931348623157e308"
)

var (
	log1pOneDecimal      = featureBoundDecimal(math.Log1p(1))
	log1pMillionDecimal  = featureBoundDecimal(math.Log1p(1_000_000))
	log1pMaxInt64Decimal = featureBoundDecimal(math.Log1p(math.MaxInt64))
)

var actorFeatureDefinitions = []featureDefinitionWire{
	featureDefinition("log1p_rating", studentAnalyticsProtocol, []string{"rating"}, actorAggregationScope, "select_current", "reject", "", "log1p", "finite_float64", "0", "1000000", "finite_float64", "0", log1pMillionDecimal, "not_applicable"),
	nullableMetricValueDefinition("knowledge"),
	nullableMetricPresenceDefinition("knowledge"),
	nullableMetricValueDefinition("accuracy"),
	nullableMetricPresenceDefinition("accuracy"),
	nullableMetricValueDefinition("quality"),
	nullableMetricPresenceDefinition("quality"),
	nullableMetricValueDefinition("flexibility"),
	nullableMetricPresenceDefinition("flexibility"),
	nullableMetricValueDefinition("proficiency"),
	nullableMetricPresenceDefinition("proficiency"),
	featureDefinition("log1p_exam_count", studentAnalyticsProtocol, []string{"examHistory"}, actorAggregationScope, "array_length", "reject", "", "log1p", "array", "1", maximumInt64Decimal, "finite_float64", log1pOneDecimal, log1pMaxInt64Decimal, "not_applicable"),
	featureDefinition("log1p_rating_history_count", studentAnalyticsProtocol, []string{"ratingHistory"}, actorAggregationScope, "array_length", "reject", "", "log1p", "array", "1", maximumInt64Decimal, "finite_float64", log1pOneDecimal, log1pMaxInt64Decimal, "not_applicable"),
}

var problemFeatureDefinitions = []featureDefinitionWire{
	ratioDefinition("actor_acceptance_rate", "acceptedActorCount", "attemptingActorCount"),
	ratioDefinition("submission_acceptance_rate", "acceptedSubmissionCount", "submissionCount"),
	problemCountDefinition("log1p_participant_count", "participantCount"),
	problemCountDefinition("log1p_submission_count", "submissionCount"),
	problemCountDefinition("log1p_accepted_actor_count", "acceptedActorCount"),
	problemCountDefinition("log1p_accepted_submission_count", "acceptedSubmissionCount"),
	problemCountDefinition("log1p_attempting_actor_count", "attemptingActorCount"),
	nullableProblemValueDefinition("log1p_max_score_value", "max_score_present", "problems[].maxScore", "nullable_finite_float64", maximumFloat64Decimal),
	nullableProblemPresenceDefinition("max_score_present", "log1p_max_score_value", "problems[].maxScore", "nullable_finite_float64", maximumFloat64Decimal),
	nullableProblemValueDefinition("log1p_time_limit_ms_value", "time_limit_ms_present", "problems[].timeLimitMs", "nullable_nonnegative_int64", maximumInt64Decimal),
	nullableProblemPresenceDefinition("time_limit_ms_present", "log1p_time_limit_ms_value", "problems[].timeLimitMs", "nullable_nonnegative_int64", maximumInt64Decimal),
	nullableProblemValueDefinition("log1p_memory_limit_bytes_value", "memory_limit_bytes_present", "problems[].memoryLimitBytes", "nullable_nonnegative_int64", maximumInt64Decimal),
	nullableProblemPresenceDefinition("memory_limit_bytes_present", "log1p_memory_limit_bytes_value", "problems[].memoryLimitBytes", "nullable_nonnegative_int64", maximumInt64Decimal),
}

var actorFeatureIDs = featureIDs(actorFeatureDefinitions)
var problemFeatureIDs = featureIDs(problemFeatureDefinitions)
var actorFeatureRanges = mustFeatureRanges(actorFeatureDefinitions)
var problemFeatureRanges = mustFeatureRanges(problemFeatureDefinitions)

func featureDefinition(
	id, protocol string,
	fields []string,
	scope, aggregation, missingPolicy, pairedFeatureID, transform, sourceType, sourceMinimum, sourceMaximum,
	outputType, outputMinimum, outputMaximum, denominatorZero string,
) featureDefinitionWire {
	return featureDefinitionWire{
		ID:          id,
		Source:      featureSourceWire{Protocol: protocol, Fields: slices.Clone(fields)},
		Aggregation: featureAggregationWire{Scope: scope, Operation: aggregation},
		Missing:     featureMissingWire{Policy: missingPolicy, PairedFeatureID: optionalContractString(pairedFeatureID)},
		Transform:   featureTransformWire{Operation: transform},
		Domain: featureDomainWire{
			SourceType: sourceType, SourceMinimum: optionalContractString(sourceMinimum), SourceMaximum: optionalContractString(sourceMaximum),
			OutputType: outputType, OutputMinimum: optionalContractString(outputMinimum), OutputMaximum: optionalContractString(outputMaximum),
			DenominatorZero: denominatorZero,
		},
	}
}

func nullableMetricValueDefinition(metric string) featureDefinitionWire {
	return featureDefinition(
		metric+"_value", studentAnalyticsProtocol, []string{"current." + metric}, actorAggregationScope, "select_current",
		"zero_with_presence", metric+"_present", "identity", "nullable_finite_float64", "0", "100",
		"finite_float64", "0", "100", "not_applicable",
	)
}

func nullableMetricPresenceDefinition(metric string) featureDefinitionWire {
	return featureDefinition(
		metric+"_present", studentAnalyticsProtocol, []string{"current." + metric}, actorAggregationScope, "select_current",
		"presence_indicator", metric+"_value", "is_present", "nullable_finite_float64", "0", "100",
		"binary_float64", "0", "1", "not_applicable",
	)
}

func ratioDefinition(id, numerator, denominator string) featureDefinitionWire {
	return featureDefinition(
		id, problemMetricsProtocolV1, []string{numerator, denominator}, problemAggregationScope, "sum_each_reject_int64_overflow",
		"reject", "", "ratio", "nonnegative_int64_pair", "0", maximumInt64Decimal,
		"finite_float64", "0", "1", "return_zero",
	)
}

func problemCountDefinition(id, field string) featureDefinitionWire {
	return featureDefinition(
		id, problemMetricsProtocolV1, []string{field}, problemAggregationScope, "sum_reject_int64_overflow",
		"reject", "", "log1p", "nonnegative_int64", "0", maximumInt64Decimal,
		"finite_float64", "0", log1pMaxInt64Decimal, "not_applicable",
	)
}

func nullableProblemValueDefinition(id, presenceID, field, sourceType, sourceMaximum string) featureDefinitionWire {
	return featureDefinition(
		id, pintiaSnapshotProtocol, []string{field}, problemAggregationScope, "assert_equal_by_problem_fact",
		"zero_with_presence", presenceID, "log1p", sourceType, "0", sourceMaximum,
		"finite_float64", "0", log1pSourceMaximum(sourceMaximum), "not_applicable",
	)
}

func nullableProblemPresenceDefinition(id, valueID, field, sourceType, sourceMaximum string) featureDefinitionWire {
	return featureDefinition(
		id, pintiaSnapshotProtocol, []string{field}, problemAggregationScope, "assert_equal_by_problem_fact",
		"presence_indicator", valueID, "is_present", sourceType, "0", sourceMaximum,
		"binary_float64", "0", "1", "not_applicable",
	)
}

func optionalContractString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func featureIDs(definitions []featureDefinitionWire) []string {
	identities := make([]string, len(definitions))
	for index := range definitions {
		identities[index] = definitions[index].ID
	}
	return identities
}

func featureBoundDecimal(value float64) string {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		panic("feature bound must be finite")
	}
	return strconv.FormatFloat(value, 'g', -1, 64)
}

func log1pSourceMaximum(sourceMaximum string) string {
	value, err := strconv.ParseFloat(sourceMaximum, 64)
	if err != nil || value < 0 || math.IsInf(value, 0) {
		panic("log1p source maximum must be a finite nonnegative float64")
	}
	return featureBoundDecimal(math.Log1p(value))
}

func mustFeatureRanges(definitions []featureDefinitionWire) []inferencemodel.FeatureDomain {
	ranges := make([]inferencemodel.FeatureDomain, len(definitions))
	for index, definition := range definitions {
		if definition.Domain.OutputMinimum == nil || definition.Domain.OutputMaximum == nil {
			panic("feature output domain must be closed")
		}
		minimum, minimumErr := strconv.ParseFloat(*definition.Domain.OutputMinimum, 64)
		maximum, maximumErr := strconv.ParseFloat(*definition.Domain.OutputMaximum, 64)
		if minimumErr != nil || maximumErr != nil || math.IsNaN(minimum) || math.IsInf(minimum, 0) ||
			math.IsNaN(maximum) || math.IsInf(maximum, 0) || minimum > maximum {
			panic("feature output domain must contain ordered finite float64 extrema")
		}
		ranges[index] = inferencemodel.FeatureDomain{FeatureID: definition.ID, Minimum: minimum, Maximum: maximum}
	}
	return ranges
}

var featureSchemaDocument, featureSchemaDigest = buildFeatureSchema()

func buildFeatureSchema() (json.RawMessage, string) {
	raw, err := json.Marshal(featureSchemaWire{
		Schema: FeatureSchemaV1, ActorFeatures: actorFeatureDefinitions, ProblemFeatures: problemFeatureDefinitions,
	})
	if err != nil {
		panic(err)
	}
	canonical, digest, err := canonicaljson.Object(raw, maximumManifestBytes)
	if err != nil {
		panic(err)
	}
	return canonical, digest
}

// FeatureSchemaDocument returns an independent canonical copy of the only
// feature schema accepted by the online runtime.
func FeatureSchemaDocument() json.RawMessage {
	return append(json.RawMessage(nil), featureSchemaDocument...)
}

func FeatureSchemaSHA256() string { return featureSchemaDigest }

func ActorFeatureIDs() []string   { return slices.Clone(actorFeatureIDs) }
func ProblemFeatureIDs() []string { return slices.Clone(problemFeatureIDs) }

func decodeClosed(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("JSON contains a trailing value")
		}
		return err
	}
	return nil
}

func canonicalObject(raw json.RawMessage, expectedDigest string, limit int, label string) (json.RawMessage, string, error) {
	canonical, digest, err := canonicaljson.Object(raw, limit)
	if err != nil {
		return nil, "", fmt.Errorf("%s: %w", label, err)
	}
	if expectedDigest != "" && digest != expectedDigest {
		return nil, "", fmt.Errorf("%s SHA-256 differs", label)
	}
	return canonical, digest, nil
}

func canonicalText(value string, maximumBytes int) bool {
	return value != "" && len(value) <= maximumBytes && strings.TrimSpace(value) == value && !strings.ContainsRune(value, 0)
}

func pintiaSourceProblemKey(problemID string) string {
	return "pintia:problem:" + strconv.Itoa(len(problemID)) + ":" + problemID
}

func pintiaProblemKey(problemID, problemFactSHA256 string) string {
	return pintiaSourceProblemKey(problemID) + ":" + problemFactSHA256
}

func canonicalPintiaURL(value string) bool {
	if value == "" || strings.TrimSpace(value) != value || len(value) > 2048 {
		return false
	}
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme == "https" && parsed.Host == "pintia.cn" && parsed.User == nil && parsed.Fragment == ""
}

func exactUnitWeightSum(values []catalogWeight) bool {
	total := new(big.Rat)
	for _, value := range values {
		rational, ok := new(big.Rat).SetString(value.raw)
		if !ok {
			return false
		}
		total.Add(total, rational)
	}
	return total.Cmp(big.NewRat(1, 1)) == 0
}

func sha256Bytes(raw []byte) string {
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}
