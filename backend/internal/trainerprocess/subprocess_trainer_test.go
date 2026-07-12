package trainerprocess

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestTrainerPackageRootHasOneExactExecutableSourceSet(t *testing.T) {
	t.Parallel()
	root := filepath.Join(t.TempDir(), "recommendation")
	packageDirectory := filepath.Join(root, "ascendany_recommendation_trainer")
	if err := os.MkdirAll(packageDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range trainerPackageSourceFiles {
		if err := os.WriteFile(filepath.Join(packageDirectory, name), []byte("# reviewed source\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := validateTrainerPackageRoot(root); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packageDirectory, "unexpected.py"), []byte("pass\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validateTrainerPackageRoot(root); err == nil {
		t.Fatal("unexpected trainer source was accepted")
	}
}

func TestTrainerCommandExplicitlyBindsAndBootstrapsReviewedPackage(t *testing.T) {
	t.Parallel()
	trainer := SubprocessTrainer{config: SubprocessTrainerConfig{
		PythonExecutable:   "/runtime/bin/python",
		TrainerPackageRoot: "/release/trainers/recommendation",
		MaximumInputBytes:  128,
		MaximumOutputBytes: 256,
	}}
	arguments := trainer.commandArguments("/work/output", TrainingRequest{
		InputManifestSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	})
	bind := []string{"--ro-bind", "/release/trainers/recommendation", sandboxTrainerPackageRoot}
	if !containsArgumentSequence(arguments, bind) {
		t.Fatalf("arguments do not contain package bind: %#v", arguments)
	}
	wantTail := []string{"--", "/runtime/bin/python", "-B", "-s", "-P", "-c", trainerBootstrap}
	if len(arguments) < len(wantTail) || !slices.Equal(arguments[len(arguments)-len(wantTail):], wantTail) {
		t.Fatalf("command tail = %#v", arguments)
	}
	if slices.Contains(arguments, "-I") || slices.Contains(arguments, "-E") {
		t.Fatalf("command ignores the fixed PYTHONHASHSEED environment: %#v", arguments)
	}
	if slices.Contains(arguments, "/trainer/cli.py") || slices.Contains(arguments, "-m") {
		t.Fatalf("command retained an obsolete script/module discovery path: %#v", arguments)
	}
}

func TestRuntimeAttestationOutputRequiresExactCanonicalFiveDigestIdentity(t *testing.T) {
	t.Parallel()
	expected := runtimeAttestationIdentity{
		HostCapabilitySHA256:      strings.Repeat("1", 64),
		RuntimeAttestationSHA256:  strings.Repeat("2", 64),
		RuntimeConstructionSHA256: strings.Repeat("3", 64),
		RuntimeProvenanceSHA256:   strings.Repeat("4", 64),
		RuntimeTreeSHA256:         strings.Repeat("5", 64),
	}
	raw, err := json.Marshal(expected)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateRuntimeAttestationOutput(raw, expected); err != nil {
		t.Fatalf("canonical attestation was rejected: %v", err)
	}

	mismatch := expected
	mismatch.RuntimeTreeSHA256 = strings.Repeat("6", 64)
	mismatchRaw, err := json.Marshal(mismatch)
	if err != nil {
		t.Fatal(err)
	}
	unknownRaw := append(slices.Clone(raw[:len(raw)-1]), []byte(`,"unexpected":true}`)...)
	invalid := expected
	invalid.RuntimeAttestationSHA256 = strings.Repeat("A", 64)
	invalidRaw, err := json.Marshal(invalid)
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string][]byte{
		"noncanonical newline": append(slices.Clone(raw), '\n'),
		"identity mismatch":    mismatchRaw,
		"unknown field":        unknownRaw,
		"uppercase digest":     invalidRaw,
	} {
		name, value := name, value
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := validateRuntimeAttestationOutput(value, expected); err == nil {
				t.Fatal("invalid runtime attestation output was accepted")
			}
		})
	}
}

func containsArgumentSequence(arguments, sequence []string) bool {
	for index := 0; index+len(sequence) <= len(arguments); index++ {
		if slices.Equal(arguments[index:index+len(sequence)], sequence) {
			return true
		}
	}
	return false
}
