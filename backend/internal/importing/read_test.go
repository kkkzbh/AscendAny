package importing

import "testing"

func TestValidPublicIDRequiresCanonicalUUIDv4(t *testing.T) {
	t.Parallel()
	tests := []struct {
		value string
		valid bool
	}{
		{value: "11111111-1111-4111-8111-111111111111", valid: true},
		{value: "11111111-1111-5111-8111-111111111111", valid: false},
		{value: "11111111-1111-4111-7111-111111111111", valid: false},
		{value: "11111111-1111-4111-8111-11111111111A", valid: false},
		{value: " 11111111-1111-4111-8111-111111111111", valid: false},
		{value: "", valid: false},
	}
	for _, test := range tests {
		if got := ValidPublicID(test.value); got != test.valid {
			t.Errorf("ValidPublicID(%q) = %t, want %t", test.value, got, test.valid)
		}
	}
}

func TestPublicFailureMessageDoesNotExposeStoredDetail(t *testing.T) {
	t.Parallel()
	tests := map[string]string{
		string(ErrorValidation):           "The Pintia snapshot failed validation.",
		string(ErrorIdentityConflict):     "The snapshot conflicts with immutable imported identity.",
		string(ErrorSubmissionConflict):   "The snapshot conflicts with immutable imported identity.",
		string(ErrorHeadConflict):         "The snapshot conflicts with immutable imported identity.",
		string(ErrorArtifactMetadata):     "The uploaded artifact could not be verified.",
		string(ErrorArtifactVerification): "The uploaded artifact could not be verified.",
		string(ErrorManifest):             "The analytics input manifest could not be created.",
		"database_failure":                "The import could not be completed.",
		"future_internal_code":            "The import could not be completed.",
	}
	for code, want := range tests {
		if got := publicFailureMessage(code); got != want {
			t.Errorf("publicFailureMessage(%q) = %q, want %q", code, got, want)
		}
	}
}

func TestTerminalStatus(t *testing.T) {
	t.Parallel()
	for _, status := range []JobStatus{JobSucceeded, JobFailed, JobSuperseded} {
		if !terminalStatus(status) {
			t.Errorf("terminalStatus(%q) = false", status)
		}
	}
	for _, status := range []JobStatus{JobQueued, JobRunning} {
		if terminalStatus(status) {
			t.Errorf("terminalStatus(%q) = true", status)
		}
	}
}
