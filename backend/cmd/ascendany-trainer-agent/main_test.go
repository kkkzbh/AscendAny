package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/kkkzbh/AscendAny/backend/internal/traineragent"
)

func TestValidateCommand(t *testing.T) {
	t.Parallel()
	if err := validateCommand([]string{"run"}); err != nil {
		t.Fatalf("validateCommand(run) error = %v", err)
	}
	if err := validateCommand([]string{"verify-acceptance"}); err != nil {
		t.Fatalf("validateCommand(verify-acceptance) error = %v", err)
	}
	if err := validateCommand([]string{"verify-runtime"}); err != nil {
		t.Fatalf("validateCommand(verify-runtime) error = %v", err)
	}
	for _, args := range [][]string{nil, {}, {"serve"}, {"run", "extra"}, {"verify-acceptance", "extra"}, {"verify-runtime", "extra"}} {
		if err := validateCommand(args); err == nil {
			t.Fatalf("validateCommand(%q) error = nil", args)
		}
	}
}

func TestVerifyAcceptanceCommandIsOfflineBoundedAndSilentOnSuccess(t *testing.T) {
	t.Parallel()
	const valid = `{"agentId":"rtx-01","attemptToken":"123e4567-e89b-42d3-a456-426614174101","claimAt":"2030-01-02T03:04:05Z","disposition":"activated","heartbeatAt":"2030-01-02T03:04:06Z","hostCapabilitySha256":"4444444444444444444444444444444444444444444444444444444444444444","inputManifestSHA256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","modelId":"123e4567-e89b-42d3-a456-426614174102","origin":"https://trainer.example","outputBundleSHA256":"dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd","releaseCommit":"eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee","releaseVersion":"1.2.3","requestSHA256":"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","runId":"123e4567-e89b-42d3-a456-426614174100","runtimeAttestationSha256":"5555555555555555555555555555555555555555555555555555555555555555","runtimeConstructionSha256":"1111111111111111111111111111111111111111111111111111111111111111","runtimeProvenanceSha256":"2222222222222222222222222222222222222222222222222222222222222222","runtimeTreeSha256":"3333333333333333333333333333333333333333333333333333333333333333","schema":"ascendany.trainer.acceptance.v3","uploadAt":"2030-01-02T03:04:07Z"}`
	var stderr bytes.Buffer
	if code := runWithIO([]string{"verify-acceptance"}, strings.NewReader(valid), &stderr); code != 0 {
		t.Fatalf("success exit = %d stderr = %s", code, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("success stderr = %s", stderr.String())
	}

	stderr.Reset()
	if code := runWithIO([]string{"verify-acceptance"}, strings.NewReader(" "+valid), &stderr); code != 1 {
		t.Fatalf("noncanonical exit = %d stderr = %s", code, stderr.String())
	}
	if stderr.Len() == 0 {
		t.Fatal("noncanonical verification emitted no diagnostic")
	}

	stderr.Reset()
	oversized := strings.NewReader(strings.Repeat("x", traineragent.MaximumAcceptanceCandidateBytes+1))
	if code := runWithIO([]string{"verify-acceptance"}, oversized, &stderr); code != 1 {
		t.Fatalf("oversized exit = %d stderr = %s", code, stderr.String())
	}
}
