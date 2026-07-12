package analytics

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type rawStudentMetrics struct {
	Protocol      *string                  `json:"protocol"`
	ReferenceTime *time.Time               `json:"referenceTime"`
	Current       *rawMetricValues         `json:"current"`
	ExamHistory   *[]rawExamMetricPoint    `json:"examHistory"`
	RatingHistory *[]rawRatingHistoryPoint `json:"ratingHistory"`
}

type rawMetricValues struct {
	Knowledge   nullableMetric `json:"knowledge"`
	Accuracy    nullableMetric `json:"accuracy"`
	Quality     nullableMetric `json:"quality"`
	Flexibility nullableMetric `json:"flexibility"`
	Proficiency nullableMetric `json:"proficiency"`
}

type nullableMetric struct {
	present bool
	value   *float64
}

func (metric *nullableMetric) UnmarshalJSON(data []byte) error {
	metric.present = true
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		metric.value = nil
		return nil
	}
	var value float64
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("metric must be null or a JSON number: %w", err)
	}
	metric.value = &value
	return nil
}

type rawExamMetricPoint struct {
	ExamID     *int64           `json:"examId"`
	SnapshotID *int64           `json:"snapshotId"`
	EventTime  *time.Time       `json:"eventTime"`
	Values     *rawMetricValues `json:"values"`
}

type rawRatingHistoryPoint struct {
	ExamID      *int64     `json:"examId"`
	SnapshotID  *int64     `json:"snapshotId"`
	EventTime   *time.Time `json:"eventTime"`
	Rank        *int64     `json:"rank"`
	OldRating   *int64     `json:"oldRating"`
	Delta       *int64     `json:"delta"`
	NewRating   *int64     `json:"newRating"`
	Seed        *float64   `json:"seed"`
	Performance *float64   `json:"performance"`
}

// DecodeStoredStudentMetrics decodes the only persisted student analytics
// protocol. It rejects missing fields, unknown or duplicate object keys, null
// containers, non-UTC timestamps, and semantic history corruption.
func DecodeStoredStudentMetrics(data []byte) (StudentMetrics, error) {
	if err := validateExactStudentMetricsKeys(data); err != nil {
		return StudentMetrics{}, analyticsError(ErrorInvalidDataset, true, "decode stored student metrics", err)
	}
	var raw rawStudentMetrics
	if err := decodeClosedJSON(data, &raw); err != nil {
		return StudentMetrics{}, analyticsError(ErrorInvalidDataset, true, "decode stored student metrics", err)
	}
	if raw.Protocol == nil || raw.ReferenceTime == nil || raw.Current == nil || raw.ExamHistory == nil || raw.RatingHistory == nil {
		return StudentMetrics{}, analyticsError(ErrorInvalidDataset, true, "decode stored student metrics", errors.New("every top-level student metrics field is required and containers must not be null"))
	}
	if err := validateUTCTime(*raw.ReferenceTime, "referenceTime"); err != nil {
		return StudentMetrics{}, analyticsError(ErrorInvalidDataset, true, "decode stored student metrics", err)
	}
	current, err := convertRawMetricValues(*raw.Current, "current")
	if err != nil {
		return StudentMetrics{}, analyticsError(ErrorInvalidDataset, true, "decode stored student metrics", err)
	}
	examHistory := make([]ExamMetricPoint, 0, len(*raw.ExamHistory))
	for index, point := range *raw.ExamHistory {
		if point.ExamID == nil || point.SnapshotID == nil || point.EventTime == nil || point.Values == nil {
			return StudentMetrics{}, analyticsError(ErrorInvalidDataset, true, "decode stored student metrics", fmt.Errorf("examHistory[%d] requires every field", index))
		}
		if err := validateUTCTime(*point.EventTime, fmt.Sprintf("examHistory[%d].eventTime", index)); err != nil {
			return StudentMetrics{}, analyticsError(ErrorInvalidDataset, true, "decode stored student metrics", err)
		}
		values, err := convertRawMetricValues(*point.Values, fmt.Sprintf("examHistory[%d].values", index))
		if err != nil {
			return StudentMetrics{}, analyticsError(ErrorInvalidDataset, true, "decode stored student metrics", err)
		}
		examHistory = append(examHistory, ExamMetricPoint{
			ExamID:     *point.ExamID,
			SnapshotID: *point.SnapshotID,
			EventTime:  point.EventTime.UTC(),
			Values:     values,
		})
	}
	ratingHistory := make([]RatingHistoryPoint, 0, len(*raw.RatingHistory))
	for index, point := range *raw.RatingHistory {
		if point.ExamID == nil || point.SnapshotID == nil || point.EventTime == nil || point.Rank == nil || point.OldRating == nil || point.Delta == nil || point.NewRating == nil || point.Seed == nil || point.Performance == nil {
			return StudentMetrics{}, analyticsError(ErrorInvalidDataset, true, "decode stored student metrics", fmt.Errorf("ratingHistory[%d] requires every field", index))
		}
		if err := validateUTCTime(*point.EventTime, fmt.Sprintf("ratingHistory[%d].eventTime", index)); err != nil {
			return StudentMetrics{}, analyticsError(ErrorInvalidDataset, true, "decode stored student metrics", err)
		}
		ratingHistory = append(ratingHistory, RatingHistoryPoint{
			ExamID:      *point.ExamID,
			SnapshotID:  *point.SnapshotID,
			EventTime:   point.EventTime.UTC(),
			Rank:        *point.Rank,
			OldRating:   *point.OldRating,
			Delta:       *point.Delta,
			NewRating:   *point.NewRating,
			Seed:        *point.Seed,
			Performance: *point.Performance,
		})
	}
	metrics := StudentMetrics{
		Protocol:      *raw.Protocol,
		ReferenceTime: raw.ReferenceTime.UTC(),
		Current:       current,
		ExamHistory:   examHistory,
		RatingHistory: ratingHistory,
	}
	if err := validateStudentMetrics(metrics); err != nil {
		return StudentMetrics{}, analyticsError(ErrorInvalidDataset, true, "decode stored student metrics", err)
	}
	return metrics, nil
}

func validateExactStudentMetricsKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var document any
	if err := decoder.Decode(&document); err != nil {
		return fmt.Errorf("decode student metrics JSON shape: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return err
	}
	root, err := requireExactObject(document, "$", "protocol", "referenceTime", "current", "examHistory", "ratingHistory")
	if err != nil {
		return err
	}
	if _, err := requireExactObject(root["current"], "current", "knowledge", "accuracy", "quality", "flexibility", "proficiency"); err != nil {
		return err
	}
	examHistory, err := requireJSONArray(root["examHistory"], "examHistory")
	if err != nil {
		return err
	}
	for index, value := range examHistory {
		path := fmt.Sprintf("examHistory[%d]", index)
		point, err := requireExactObject(value, path, "examId", "snapshotId", "eventTime", "values")
		if err != nil {
			return err
		}
		if _, err := requireExactObject(point["values"], path+".values", "knowledge", "accuracy", "quality", "flexibility", "proficiency"); err != nil {
			return err
		}
	}
	ratingHistory, err := requireJSONArray(root["ratingHistory"], "ratingHistory")
	if err != nil {
		return err
	}
	for index, value := range ratingHistory {
		if _, err := requireExactObject(
			value,
			fmt.Sprintf("ratingHistory[%d]", index),
			"examId",
			"snapshotId",
			"eventTime",
			"rank",
			"oldRating",
			"delta",
			"newRating",
			"seed",
			"performance",
		); err != nil {
			return err
		}
	}
	return nil
}

func requireExactObject(value any, path string, expected ...string) (map[string]any, error) {
	object, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s must be a JSON object", path)
	}
	allowed := make(map[string]struct{}, len(expected))
	for _, key := range expected {
		allowed[key] = struct{}{}
	}
	for key := range object {
		if _, exists := allowed[key]; !exists {
			return nil, fmt.Errorf("%s contains unknown or incorrectly cased field %q", path, key)
		}
	}
	for _, key := range expected {
		if _, exists := object[key]; !exists {
			return nil, fmt.Errorf("%s.%s is required with exact casing", path, key)
		}
	}
	return object, nil
}

func requireJSONArray(value any, path string) ([]any, error) {
	array, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("%s must be a JSON array", path)
	}
	return array, nil
}

func convertRawMetricValues(raw rawMetricValues, name string) (MetricValues, error) {
	fields := []struct {
		name   string
		metric nullableMetric
	}{
		{name: "knowledge", metric: raw.Knowledge},
		{name: "accuracy", metric: raw.Accuracy},
		{name: "quality", metric: raw.Quality},
		{name: "flexibility", metric: raw.Flexibility},
		{name: "proficiency", metric: raw.Proficiency},
	}
	for _, field := range fields {
		if !field.metric.present {
			return MetricValues{}, fmt.Errorf("%s.%s is required", name, field.name)
		}
	}
	return MetricValues{
		Knowledge:   raw.Knowledge.value,
		Accuracy:    raw.Accuracy.value,
		Quality:     raw.Quality.value,
		Flexibility: raw.Flexibility.value,
		Proficiency: raw.Proficiency.value,
	}, nil
}
