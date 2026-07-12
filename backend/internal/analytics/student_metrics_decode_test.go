package analytics

import (
	"strings"
	"testing"
	"time"
)

const validStoredStudentMetricsJSON = `{
  "protocol":"student_analytics_v1",
  "referenceTime":"2026-01-03T00:00:00Z",
  "current":{"knowledge":80,"accuracy":null,"quality":75.5,"flexibility":0,"proficiency":100},
  "examHistory":[
    {"examId":1,"snapshotId":11,"eventTime":"2026-01-01T00:00:00Z","values":{"knowledge":70,"accuracy":60,"quality":null,"flexibility":50,"proficiency":40}},
    {"examId":2,"snapshotId":22,"eventTime":"2026-01-02T00:00:00Z","values":{"knowledge":80,"accuracy":70,"quality":60,"flexibility":50,"proficiency":40}}
  ],
  "ratingHistory":[
    {"examId":1,"snapshotId":11,"eventTime":"2026-01-01T00:00:00Z","rank":2,"oldRating":1500,"delta":10,"newRating":1510,"seed":1.5,"performance":1520.25},
    {"examId":2,"snapshotId":22,"eventTime":"2026-01-02T00:00:00Z","rank":1,"oldRating":1510,"delta":-5,"newRating":1505,"seed":1.25,"performance":1500}
  ]
}`

func TestDecodeStoredStudentMetricsAcceptsExactProtocol(t *testing.T) {
	t.Parallel()

	metrics, err := DecodeStoredStudentMetrics([]byte(validStoredStudentMetricsJSON))
	if err != nil {
		t.Fatalf("DecodeStoredStudentMetrics() error = %v", err)
	}
	if metrics.Protocol != StudentMetricsProtocolV1 || len(metrics.ExamHistory) != 2 || len(metrics.RatingHistory) != 2 {
		t.Fatalf("metrics = %#v", metrics)
	}
	if !metrics.ReferenceTime.Equal(time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)) || metrics.ReferenceTime.Location() != time.UTC {
		t.Fatalf("referenceTime = %v (%v)", metrics.ReferenceTime, metrics.ReferenceTime.Location())
	}
	if metrics.Current.Accuracy != nil || metrics.Current.Knowledge == nil || *metrics.Current.Knowledge != 80 {
		t.Fatalf("current = %#v", metrics.Current)
	}
	if metrics.RatingHistory[1].NewRating != 1505 {
		t.Fatalf("ratingHistory = %#v", metrics.RatingHistory)
	}
}

func TestDecodeStoredStudentMetricsNormalizesZeroOffsetUTC(t *testing.T) {
	t.Parallel()

	document := strings.ReplaceAll(validStoredStudentMetricsJSON, "Z", "+00:00")
	metrics, err := DecodeStoredStudentMetrics([]byte(document))
	if err != nil {
		t.Fatalf("DecodeStoredStudentMetrics() error = %v", err)
	}
	if metrics.ReferenceTime.Location() != time.UTC {
		t.Fatalf("referenceTime location = %v", metrics.ReferenceTime.Location())
	}
	for index := range metrics.ExamHistory {
		if metrics.ExamHistory[index].EventTime.Location() != time.UTC || metrics.RatingHistory[index].EventTime.Location() != time.UTC {
			t.Fatalf("history %d was not normalized to UTC", index)
		}
	}
}

func TestDecodeStoredStudentMetricsRejectsClosedJSONAndSemanticCorruption(t *testing.T) {
	t.Parallel()

	emptyHistories := strings.Replace(validStoredStudentMetricsJSON, `"examHistory":[
    {"examId":1,"snapshotId":11,"eventTime":"2026-01-01T00:00:00Z","values":{"knowledge":70,"accuracy":60,"quality":null,"flexibility":50,"proficiency":40}},
    {"examId":2,"snapshotId":22,"eventTime":"2026-01-02T00:00:00Z","values":{"knowledge":80,"accuracy":70,"quality":60,"flexibility":50,"proficiency":40}}
  ]`, `"examHistory":[]`, 1)
	emptyHistories = strings.Replace(emptyHistories, `"ratingHistory":[
    {"examId":1,"snapshotId":11,"eventTime":"2026-01-01T00:00:00Z","rank":2,"oldRating":1500,"delta":10,"newRating":1510,"seed":1.5,"performance":1520.25},
    {"examId":2,"snapshotId":22,"eventTime":"2026-01-02T00:00:00Z","rank":1,"oldRating":1510,"delta":-5,"newRating":1505,"seed":1.25,"performance":1500}
  ]`, `"ratingHistory":[]`, 1)
	tests := map[string]string{
		"unknown field":           strings.Replace(validStoredStudentMetricsJSON, `"protocol":`, `"extra":1,"protocol":`, 1),
		"duplicate key":           strings.Replace(validStoredStudentMetricsJSON, `"protocol":`, `"protocol":"student_analytics_v1","protocol":`, 1),
		"wrong top-level casing":  strings.Replace(validStoredStudentMetricsJSON, `"protocol":`, `"Protocol":`, 1),
		"wrong nested casing":     strings.Replace(validStoredStudentMetricsJSON, `"examId":1`, `"examID":1`, 1),
		"wrong metric casing":     strings.Replace(validStoredStudentMetricsJSON, `"knowledge":80`, `"Knowledge":80`, 1),
		"case alias duplicate":    strings.Replace(validStoredStudentMetricsJSON, `"protocol":`, `"protocol":"student_analytics_v1","Protocol":`, 1),
		"missing top level":       strings.Replace(validStoredStudentMetricsJSON, `  "protocol":"student_analytics_v1",`+"\n", "", 1),
		"null container":          strings.Replace(validStoredStudentMetricsJSON, `"current":{"knowledge":80,"accuracy":null,"quality":75.5,"flexibility":0,"proficiency":100}`, `"current":null`, 1),
		"missing nullable metric": strings.Replace(validStoredStudentMetricsJSON, `"knowledge":80,`, "", 1),
		"wrong protocol":          strings.Replace(validStoredStudentMetricsJSON, StudentMetricsProtocolV1, "student_analytics_v2", 1),
		"non UTC reference":       strings.Replace(validStoredStudentMetricsJSON, `2026-01-03T00:00:00Z`, `2026-01-03T08:00:00+08:00`, 1),
		"metric above range":      strings.Replace(validStoredStudentMetricsJSON, `"knowledge":80`, `"knowledge":101`, 1),
		"non finite metric":       strings.Replace(validStoredStudentMetricsJSON, `"knowledge":80`, `"knowledge":1e999`, 1),
		"unaligned length": strings.Replace(validStoredStudentMetricsJSON, `,
    {"examId":2,"snapshotId":22,"eventTime":"2026-01-02T00:00:00Z","rank":1,"oldRating":1510,"delta":-5,"newRating":1505,"seed":1.25,"performance":1500}`,
			"", 1),
		"duplicate exam":          strings.Replace(validStoredStudentMetricsJSON, `{"examId":2,"snapshotId":22,"eventTime":"2026-01-02T00:00:00Z","values"`, `{"examId":1,"snapshotId":22,"eventTime":"2026-01-02T00:00:00Z","values"`, 1),
		"duplicate snapshot":      strings.Replace(validStoredStudentMetricsJSON, `{"examId":2,"snapshotId":22,"eventTime":"2026-01-02T00:00:00Z","values"`, `{"examId":2,"snapshotId":11,"eventTime":"2026-01-02T00:00:00Z","values"`, 1),
		"descending history":      strings.Replace(validStoredStudentMetricsJSON, `2026-01-02T00:00:00Z`, `2025-12-31T00:00:00Z`, 2),
		"non UTC event":           strings.Replace(validStoredStudentMetricsJSON, `2026-01-01T00:00:00Z`, `2026-01-01T08:00:00+08:00`, 2),
		"non UTC rating event":    strings.Replace(validStoredStudentMetricsJSON, `"eventTime":"2026-01-01T00:00:00Z","rank"`, `"eventTime":"2026-01-01T08:00:00+08:00","rank"`, 1),
		"misaligned identity":     strings.Replace(validStoredStudentMetricsJSON, `{"examId":2,"snapshotId":22,"eventTime":"2026-01-02T00:00:00Z","rank"`, `{"examId":2,"snapshotId":23,"eventTime":"2026-01-02T00:00:00Z","rank"`, 1),
		"zero rank":               strings.Replace(validStoredStudentMetricsJSON, `"rank":2`, `"rank":0`, 1),
		"negative rating":         strings.Replace(validStoredStudentMetricsJSON, `"oldRating":1500`, `"oldRating":-1`, 1),
		"wrong delta":             strings.Replace(validStoredStudentMetricsJSON, `"delta":10`, `"delta":11`, 1),
		"non finite seed":         strings.Replace(validStoredStudentMetricsJSON, `"seed":1.5`, `"seed":1e999`, 1),
		"rating discontinuity":    strings.Replace(validStoredStudentMetricsJSON, `"oldRating":1510`, `"oldRating":1511`, 1),
		"event exceeds reference": strings.Replace(validStoredStudentMetricsJSON, `2026-01-02T00:00:00Z`, `2026-01-04T00:00:00Z`, 2),
		"empty histories":         emptyHistories,
	}
	for name, document := range tests {
		document := document
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := DecodeStoredStudentMetrics([]byte(document))
			if err == nil {
				t.Fatal("DecodeStoredStudentMetrics() error = nil")
			}
			if code, ok := CodeOf(err); !ok || code != ErrorInvalidDataset || !IsPermanent(err) {
				t.Fatalf("error = %v, code = %q/%t, permanent = %t", err, code, ok, IsPermanent(err))
			}
		})
	}
}
