package analytics

import (
	"strings"
	"testing"
)

const validConfigJSON = `{
  "algorithmVersion":"ascendany_analytics_v1",
  "acceptedVerdicts":["ACCEPTED","答案正确"],
  "winsor":{"low":0.05,"high":0.95},
  "halfLifeDays":{"knowledge":45,"accuracy":21,"quality":45,"flexibility":21,"proficiency":21},
  "rating":{"initial":800,"binarySearchMin":-2000,"binarySearchMax":8000,"binarySearchSteps":30}
}`

func TestParseConfigProducesCanonicalOrderIndependentHash(t *testing.T) {
	t.Parallel()

	first, err := ParseConfig([]byte(validConfigJSON))
	if err != nil {
		t.Fatalf("ParseConfig(first) error = %v", err)
	}
	second, err := ParseConfig([]byte(`{"rating":{"binarySearchSteps":30,"binarySearchMax":8000,"binarySearchMin":-2000,"initial":800},"halfLifeDays":{"proficiency":21,"flexibility":21,"quality":45,"accuracy":21,"knowledge":45},"winsor":{"high":0.95,"low":0.05},"acceptedVerdicts":["答案正确","ACCEPTED"],"algorithmVersion":"ascendany_analytics_v1"}`))
	if err != nil {
		t.Fatalf("ParseConfig(second) error = %v", err)
	}
	if string(first.Canonical) != string(second.Canonical) || first.SHA256 != second.SHA256 {
		t.Fatalf("canonical configs differ:\n%s\n%s", first.Canonical, second.Canonical)
	}
	if first.Value.AcceptedVerdicts[0] != "ACCEPTED" {
		t.Fatalf("accepted verdicts = %v", first.Value.AcceptedVerdicts)
	}
}

func TestParseConfigRejectsUnknownMissingDuplicateAndOutOfRangeFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data string
		want string
	}{
		{name: "unknown", data: strings.Replace(validConfigJSON, `"algorithmVersion"`, `"unknown":1,"algorithmVersion"`, 1), want: "unknown field"},
		{name: "missing", data: strings.Replace(validConfigJSON, `"algorithmVersion":"ascendany_analytics_v1",`, "", 1), want: "every top-level"},
		{name: "duplicate", data: strings.Replace(validConfigJSON, `"algorithmVersion":"ascendany_analytics_v1",`, `"algorithmVersion":"ascendany_analytics_v1","algorithmVersion":"ascendany_analytics_v1",`, 1), want: "duplicate JSON object key"},
		{name: "unsupported algorithm", data: strings.Replace(validConfigJSON, AlgorithmV1, "future_algorithm", 1), want: "algorithmVersion"},
		{name: "winsor", data: strings.Replace(validConfigJSON, `"low":0.05`, `"low":0.75`, 1), want: "winsor bounds"},
		{name: "half life", data: strings.Replace(validConfigJSON, `"knowledge":45`, `"knowledge":0`, 1), want: "halfLifeDays.knowledge"},
		{name: "rating range", data: strings.Replace(validConfigJSON, `"binarySearchMax":8000`, `"binarySearchMax":800`, 1), want: "binarySearchMax"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := ParseConfig([]byte(test.data))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ParseConfig() error = %v, want %q", err, test.want)
			}
			if !IsPermanent(err) {
				t.Fatalf("ParseConfig() error is not permanent: %v", err)
			}
		})
	}
}
