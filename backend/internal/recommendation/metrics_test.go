package recommendation

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseProblemMetricsValidatesRoundedRatesAndClosedShape(t *testing.T) {
	t.Parallel()
	valid := json.RawMessage(`{"protocol":"problem_analytics_v1","participantCount":10,"submissionCount":3,"acceptedSubmissionCount":1,"attemptingActorCount":3,"acceptedActorCount":1,"submissionAcceptanceRate":0.333333,"actorAcceptanceRate":0.333333,"acceptedRuntimeMs":null,"acceptedMemoryBytes":null}`)
	metrics, err := parseProblemMetrics(valid)
	if err != nil || metrics.SubmissionCount != 3 || metrics.AcceptedActorCount != 1 {
		t.Fatalf("metrics=%#v error=%v", metrics, err)
	}
	if _, err := parseProblemMetrics(json.RawMessage(strings.Replace(string(valid), `"actorAcceptanceRate":0.333333`, `"actorAcceptanceRate":0.3`, 1))); err == nil {
		t.Fatal("inconsistent rounded rate was accepted")
	}
	if _, err := parseProblemMetrics(json.RawMessage(strings.Replace(string(valid), `"participantCount":10`, `"participantCount":10,"unknown":0`, 1))); err == nil {
		t.Fatal("unknown metrics field was accepted")
	}
}
