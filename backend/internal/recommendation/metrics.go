package recommendation

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
)

const problemMetricsProtocolV1 = "problem_analytics_v1"

type problemMetricsWire struct {
	Protocol                 *string         `json:"protocol"`
	ParticipantCount         *int64          `json:"participantCount"`
	SubmissionCount          *int64          `json:"submissionCount"`
	AcceptedSubmissionCount  *int64          `json:"acceptedSubmissionCount"`
	AttemptingActorCount     *int64          `json:"attemptingActorCount"`
	AcceptedActorCount       *int64          `json:"acceptedActorCount"`
	SubmissionAcceptanceRate *float64        `json:"submissionAcceptanceRate"`
	ActorAcceptanceRate      *float64        `json:"actorAcceptanceRate"`
	AcceptedRuntimeMS        json.RawMessage `json:"acceptedRuntimeMs"`
	AcceptedMemoryBytes      json.RawMessage `json:"acceptedMemoryBytes"`
}

type distributionWire struct {
	Count  *int64   `json:"count"`
	Min    *int64   `json:"min"`
	Median *float64 `json:"median"`
	P95    *float64 `json:"p95"`
	Max    *int64   `json:"max"`
}

type problemMetrics struct {
	ParticipantCount        int64
	SubmissionCount         int64
	AcceptedSubmissionCount int64
	AttemptingActorCount    int64
	AcceptedActorCount      int64
}

func parseProblemMetrics(raw json.RawMessage) (problemMetrics, error) {
	canonical, _, err := canonicalObject(raw, "", maximumConfigurationBytes, "problem analytics metrics")
	if err != nil {
		return problemMetrics{}, err
	}
	var wire problemMetricsWire
	if err := decodeClosed(canonical, &wire); err != nil {
		return problemMetrics{}, err
	}
	if wire.Protocol == nil || wire.ParticipantCount == nil || wire.SubmissionCount == nil || wire.AcceptedSubmissionCount == nil ||
		wire.AttemptingActorCount == nil || wire.AcceptedActorCount == nil || wire.SubmissionAcceptanceRate == nil || wire.ActorAcceptanceRate == nil ||
		len(wire.AcceptedRuntimeMS) == 0 || len(wire.AcceptedMemoryBytes) == 0 {
		return problemMetrics{}, errors.New("every problem analytics field is required")
	}
	if *wire.Protocol != problemMetricsProtocolV1 || *wire.ParticipantCount < 0 || *wire.SubmissionCount < 0 ||
		*wire.AcceptedSubmissionCount < 0 || *wire.AcceptedSubmissionCount > *wire.SubmissionCount ||
		*wire.AttemptingActorCount < 0 || *wire.AcceptedActorCount < 0 || *wire.AcceptedActorCount > *wire.AttemptingActorCount ||
		*wire.AttemptingActorCount > *wire.ParticipantCount || !unitInterval(*wire.SubmissionAcceptanceRate) || !unitInterval(*wire.ActorAcceptanceRate) {
		return problemMetrics{}, errors.New("problem analytics counts, rates, or protocol are invalid")
	}
	expectedSubmissionRate := roundSix(ratio(*wire.AcceptedSubmissionCount, *wire.SubmissionCount))
	expectedActorRate := roundSix(ratio(*wire.AcceptedActorCount, *wire.AttemptingActorCount))
	if *wire.SubmissionAcceptanceRate != expectedSubmissionRate || *wire.ActorAcceptanceRate != expectedActorRate {
		return problemMetrics{}, errors.New("problem analytics rates differ from their counts")
	}
	if err := validateDistribution(wire.AcceptedRuntimeMS, *wire.AcceptedSubmissionCount, "acceptedRuntimeMs"); err != nil {
		return problemMetrics{}, err
	}
	if err := validateDistribution(wire.AcceptedMemoryBytes, *wire.AcceptedSubmissionCount, "acceptedMemoryBytes"); err != nil {
		return problemMetrics{}, err
	}
	return problemMetrics{
		ParticipantCount: *wire.ParticipantCount, SubmissionCount: *wire.SubmissionCount,
		AcceptedSubmissionCount: *wire.AcceptedSubmissionCount, AttemptingActorCount: *wire.AttemptingActorCount,
		AcceptedActorCount: *wire.AcceptedActorCount,
	}, nil
}

func validateDistribution(raw json.RawMessage, acceptedCount int64, label string) error {
	if string(raw) == "null" {
		return nil
	}
	var wire distributionWire
	if err := decodeClosed(raw, &wire); err != nil {
		return fmt.Errorf("%s: %w", label, err)
	}
	if wire.Count == nil || wire.Min == nil || wire.Median == nil || wire.P95 == nil || wire.Max == nil ||
		*wire.Count <= 0 || *wire.Count > acceptedCount || *wire.Min < 0 || *wire.Max < *wire.Min ||
		!finite(*wire.Median) || !finite(*wire.P95) || *wire.Median < float64(*wire.Min) || *wire.Median > float64(*wire.Max) ||
		*wire.P95 < float64(*wire.Min) || *wire.P95 > float64(*wire.Max) {
		return fmt.Errorf("%s is invalid", label)
	}
	return nil
}

func ratio(numerator, denominator int64) float64 {
	if denominator == 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}

func roundSix(value float64) float64 {
	if value == 0 {
		return 0
	}
	return math.Round(value*1_000_000) / 1_000_000
}

func unitInterval(value float64) bool { return finite(value) && value >= 0 && value <= 1 }
func finite(value float64) bool       { return !math.IsNaN(value) && !math.IsInf(value, 0) }
