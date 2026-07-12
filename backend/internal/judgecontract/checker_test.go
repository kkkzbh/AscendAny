package judgecontract

import (
	"encoding/json"
	"testing"
)

func TestParseProblemSpecRequiresClosedCanonicalV1(t *testing.T) {
	spec, err := ParseProblemSpec(json.RawMessage(`{"checker":"tokens","schema":"ascendany.oj.problem-spec.v1"}`))
	if err != nil || spec.Checker != CheckerTokens {
		t.Fatalf("ParseProblemSpec() = %#v, %v", spec, err)
	}
	for name, raw := range map[string]string{
		"missing schema": `{"checker":"exact"}`,
		"unknown":        `{"checker":"exact","schema":"ascendany.oj.problem-spec.v1","x":1}`,
		"noncanonical":   `{"schema":"ascendany.oj.problem-spec.v1","checker":"exact"}`,
		"duplicate":      `{"checker":"exact","checker":"tokens","schema":"ascendany.oj.problem-spec.v1"}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseProblemSpec(json.RawMessage(raw)); err == nil {
				t.Fatal("ParseProblemSpec() error = nil")
			}
		})
	}
}

func TestCompareOutputOwnsExactAndTokenSemantics(t *testing.T) {
	if !CompareOutput(CheckerExact, []byte("a\n"), []byte("a\n")) || CompareOutput(CheckerExact, []byte("a"), []byte("a\n")) {
		t.Fatal("exact checker mismatch")
	}
	if !CompareOutput(CheckerTokens, []byte("a  b\n"), []byte("a\nb")) {
		t.Fatal("token checker mismatch")
	}
}

func TestExecutionContractValidatesIdentifiersArtifactsAndVerdicts(t *testing.T) {
	if !ValidPublicID("11111111-1111-4111-8111-111111111111") || ValidPublicID("11111111-1111-1111-8111-111111111111") {
		t.Fatal("public ID validation mismatch")
	}
	if !ValidVerdict(VerdictAccepted) || ValidVerdict(Verdict("unknown")) {
		t.Fatal("verdict validation mismatch")
	}
	digest := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	artifact := Artifact{SHA256: digest, SizeBytes: 1, MediaType: CPP20SourceMediaType, StorageKey: "sha256/aa/" + digest}
	if err := ValidateArtifact(artifact, CPP20SourceMediaType, 1); err != nil {
		t.Fatal(err)
	}
	artifact.StorageKey = "sha256/bb/" + digest
	if err := ValidateArtifact(artifact, CPP20SourceMediaType, 1); err == nil {
		t.Fatal("ValidateArtifact() error = nil")
	}
}
