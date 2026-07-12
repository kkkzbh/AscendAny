package traineragent

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestAcceptanceCandidateWriterPublishesCanonicalAtomicMode0600File(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	path := filepath.Join(directory, "trainer-latest.json")
	writer := newTestAcceptanceCandidateWriter(t, path)
	outcome := acceptanceTestOutcome(WorkerActivated)
	if err := writer.Record(outcome); err != nil {
		t.Fatal(err)
	}
	firstInfo, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !firstInfo.Mode().IsRegular() || firstInfo.Mode().Perm() != 0o600 {
		t.Fatalf("candidate mode = %s", firstInfo.Mode())
	}
	value, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	expected := `{"agentId":"rtx-01","attemptToken":"123e4567-e89b-42d3-a456-426614174101","claimAt":"2030-01-02T03:04:05Z","disposition":"activated","heartbeatAt":"2030-01-02T03:04:06Z","hostCapabilitySha256":"4444444444444444444444444444444444444444444444444444444444444444","inputManifestSHA256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","modelId":"123e4567-e89b-42d3-a456-426614174102","origin":"https://trainer.example","outputBundleSHA256":"dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd","releaseCommit":"eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee","releaseVersion":"1.2.3","requestSHA256":"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","runId":"123e4567-e89b-42d3-a456-426614174100","runtimeAttestationSha256":"5555555555555555555555555555555555555555555555555555555555555555","runtimeConstructionSha256":"1111111111111111111111111111111111111111111111111111111111111111","runtimeProvenanceSha256":"2222222222222222222222222222222222222222222222222222222222222222","runtimeTreeSha256":"3333333333333333333333333333333333333333333333333333333333333333","schema":"ascendany.trainer.acceptance.v3","uploadAt":"2030-01-02T03:04:07Z"}`
	if string(value) != expected {
		t.Fatalf("candidate bytes = %s", value)
	}

	outcome = acceptanceTestOutcome(WorkerSuperseded)
	if err := writer.Record(outcome); err != nil {
		t.Fatal(err)
	}
	secondInfo, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	firstStat, firstOK := firstInfo.Sys().(*syscall.Stat_t)
	secondStat, secondOK := secondInfo.Sys().(*syscall.Stat_t)
	secondValue, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !firstOK || !secondOK || firstStat.Ino != secondStat.Ino || string(secondValue) != expected {
		t.Fatalf("sealed candidate changed: first=%#v second=%#v bytes=%s", firstInfo.Sys(), secondInfo.Sys(), secondValue)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(path) {
		t.Fatalf("candidate directory entries = %#v", entries)
	}
}

func TestAcceptanceCandidateWriterSealsAcrossSameReleaseRestart(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	path := filepath.Join(directory, "trainer-latest.json")
	writer := newTestAcceptanceCandidateWriter(t, path)
	if err := writer.Record(acceptanceTestOutcome(WorkerActivated)); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	beforeInfo, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}

	restarted, err := NewAcceptanceCandidateWriter(acceptanceTestConfig(path))
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.Record(acceptanceTestOutcome(WorkerSuperseded)); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	afterInfo, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	beforeStat := beforeInfo.Sys().(*syscall.Stat_t)
	afterStat := afterInfo.Sys().(*syscall.Stat_t)
	if !bytes.Equal(before, after) || beforeStat.Ino != afterStat.Ino {
		t.Fatalf("restart changed sealed candidate: before=%s after=%s", before, after)
	}
}

func TestAcceptanceCandidateWriterRejectsDifferentOrCorruptExistingCandidate(t *testing.T) {
	t.Parallel()
	for name, mutate := range map[string]func(*AcceptanceCandidateConfig){
		"release version": func(config *AcceptanceCandidateConfig) { config.Release.Version = "1.2.4" },
		"release commit":  func(config *AcceptanceCandidateConfig) { config.Release.Commit = strings.Repeat("f", 40) },
		"agent":           func(config *AcceptanceCandidateConfig) { config.AgentID = "rtx-02" },
		"origin":          func(config *AcceptanceCandidateConfig) { config.Origin = "https://other.example" },
	} {
		t.Run("different "+name, func(t *testing.T) {
			directory := t.TempDir()
			path := filepath.Join(directory, "trainer-latest.json")
			writer := newTestAcceptanceCandidateWriter(t, path)
			if err := writer.Record(acceptanceTestOutcome(WorkerActivated)); err != nil {
				t.Fatal(err)
			}
			configuration := acceptanceTestConfig(path)
			mutate(&configuration)
			if _, err := NewAcceptanceCandidateWriter(configuration); err == nil ||
				!strings.Contains(err.Error(), "different release") {
				t.Fatalf("different identity error = %v", err)
			}
		})
	}
	t.Run("corrupt", func(t *testing.T) {
		directory := t.TempDir()
		path := filepath.Join(directory, "trainer-latest.json")
		if err := os.WriteFile(path, []byte(`{"schema":"ascendany.trainer.acceptance.v3"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := NewAcceptanceCandidateWriter(acceptanceTestConfig(path)); err == nil ||
			!strings.Contains(err.Error(), "verify existing") {
			t.Fatalf("corrupt candidate error = %v", err)
		}
	})
}

func TestVerifyAcceptanceCandidateRejectsNoncanonicalOrInvalidDocuments(t *testing.T) {
	t.Parallel()
	valid := canonicalAcceptanceTestCandidate(t)
	if candidate, err := VerifyAcceptanceCandidate(valid); err != nil || candidate.Schema != AcceptanceCandidateSchemaV3 {
		t.Fatalf("verified candidate = %#v error = %v", candidate, err)
	}
	tests := map[string][]byte{
		"noncanonical JSON": append([]byte(" "), valid...),
		"duplicate key": []byte(strings.Replace(
			string(valid), `{"agentId":"rtx-01",`, `{"agentId":"rtx-01","agentId":"rtx-01",`, 1,
		)),
		"unknown key": []byte(strings.Replace(
			string(valid), `,"uploadAt":`, `,"unexpected":true,"uploadAt":`, 1,
		)),
		"noncanonical timestamp": []byte(strings.Replace(
			string(valid), `"claimAt":"2030-01-02T03:04:05Z"`, `"claimAt":"2030-01-02T03:04:05.000000000Z"`, 1,
		)),
		"nanosecond reverse": []byte(strings.Replace(
			string(valid), `"heartbeatAt":"2030-01-02T03:04:06Z"`, `"heartbeatAt":"2030-01-02T03:04:07.000000001Z"`, 1,
		)),
		"missing required key": []byte(strings.Replace(
			string(valid), `"modelId":"123e4567-e89b-42d3-a456-426614174102",`, ``, 1,
		)),
	}
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := VerifyAcceptanceCandidate(raw); err == nil {
				t.Fatalf("document unexpectedly verified: %s", raw)
			}
		})
	}
}

func TestAcceptanceCandidateReleaseVersionHas128ByteLimit(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	path := filepath.Join(directory, "candidate.json")
	configuration := acceptanceTestConfig(path)
	configuration.Release.Version = "1.2.3+" + strings.Repeat("a", MaximumAcceptanceReleaseVersionBytes-len("1.2.3+"))
	if _, err := NewAcceptanceCandidateWriter(configuration); err != nil {
		t.Fatalf("128-byte release rejected: %v", err)
	}
	configuration.Release.Version += "a"
	if _, err := NewAcceptanceCandidateWriter(configuration); err == nil || !strings.Contains(err.Error(), "at most 128") {
		t.Fatalf("oversized release error = %v", err)
	}

	raw := canonicalAcceptanceTestCandidate(t)
	oversized := "1.2.3+" + strings.Repeat("a", MaximumAcceptanceReleaseVersionBytes-len("1.2.3+")+1)
	raw = []byte(strings.Replace(string(raw), `"releaseVersion":"1.2.3"`, `"releaseVersion":"`+oversized+`"`, 1))
	if _, err := VerifyAcceptanceCandidate(raw); err == nil || !strings.Contains(err.Error(), "at most 128") {
		t.Fatalf("oversized document release error = %v", err)
	}
}

func TestAcceptanceCandidateWriterRecoversSafeCrashOrphan(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	path := filepath.Join(directory, "trainer-latest.json")
	orphan := filepath.Join(directory, ".trainer-latest.json.tmp-"+strings.Repeat("a", 32))
	if err := os.WriteFile(orphan, []byte(`{"partial":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	writer := newTestAcceptanceCandidateWriter(t, path)
	if _, err := os.Lstat(orphan); !os.IsNotExist(err) {
		t.Fatalf("orphan stat error = %v", err)
	}
	if err := writer.Record(acceptanceTestOutcome(WorkerActivated)); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(path) {
		t.Fatalf("entries after recovery = %#v", entries)
	}
}

func TestAcceptanceCandidateWriterRejectsForeignOrUnsafeOrphanEntry(t *testing.T) {
	t.Parallel()
	t.Run("foreign", func(t *testing.T) {
		directory := t.TempDir()
		foreign := filepath.Join(directory, "notes")
		if err := os.WriteFile(foreign, []byte("foreign"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := NewAcceptanceCandidateWriter(acceptanceTestConfig(filepath.Join(directory, "candidate.json"))); err == nil ||
			!strings.Contains(err.Error(), "foreign entry") {
			t.Fatalf("foreign entry error = %v", err)
		}
	})
	t.Run("unsafe orphan", func(t *testing.T) {
		directory := t.TempDir()
		orphan := filepath.Join(directory, ".candidate.json.tmp-"+strings.Repeat("b", 32))
		if err := os.WriteFile(orphan, []byte("partial"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := NewAcceptanceCandidateWriter(acceptanceTestConfig(filepath.Join(directory, "candidate.json"))); err == nil ||
			!strings.Contains(err.Error(), "violates its file contract") {
			t.Fatalf("unsafe orphan error = %v", err)
		}
		if _, err := os.Lstat(orphan); err != nil {
			t.Fatalf("unsafe orphan was removed: %v", err)
		}
	})
}

func TestAcceptanceCandidateWriterRejectsLinkedDestination(t *testing.T) {
	t.Parallel()
	for name, prepare := range map[string]func(*testing.T, string) string{
		"symlink": func(t *testing.T, directory string) string {
			t.Helper()
			target := filepath.Join(filepath.Dir(directory), "target")
			if err := os.WriteFile(target, []byte("protected"), 0o600); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(directory, "trainer-latest.json")
			if err := os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}
			return target
		},
		"hardlink": func(t *testing.T, directory string) string {
			t.Helper()
			target := filepath.Join(filepath.Dir(directory), "target")
			if err := os.WriteFile(target, []byte("protected"), 0o600); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(directory, "trainer-latest.json")
			if err := os.Link(target, path); err != nil {
				t.Fatal(err)
			}
			return target
		},
	} {
		t.Run(name+" at startup", func(t *testing.T) {
			directory := acceptanceTestDirectory(t)
			target := prepare(t, directory)
			if _, err := NewAcceptanceCandidateWriter(acceptanceTestConfig(filepath.Join(directory, "trainer-latest.json"))); err == nil {
				t.Fatal("linked destination error = nil")
			}
			assertProtectedAcceptanceTarget(t, target)
		})
		t.Run(name+" before record", func(t *testing.T) {
			directory := acceptanceTestDirectory(t)
			writer := newTestAcceptanceCandidateWriter(t, filepath.Join(directory, "trainer-latest.json"))
			target := prepare(t, directory)
			if err := writer.Record(acceptanceTestOutcome(WorkerActivated)); err == nil {
				t.Fatal("linked destination error = nil")
			}
			assertProtectedAcceptanceTarget(t, target)
		})
	}
}

func acceptanceTestDirectory(t *testing.T) string {
	t.Helper()
	directory := filepath.Join(t.TempDir(), "acceptance")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	return directory
}

func assertProtectedAcceptanceTarget(t *testing.T, target string) {
	t.Helper()
	value, err := os.ReadFile(target)
	if err != nil || string(value) != "protected" {
		t.Fatalf("protected target = %q error = %v", value, err)
	}
}

func TestAcceptanceCandidateWriterRejectsUnsafeOrLinkedParent(t *testing.T) {
	t.Parallel()
	t.Run("group writable", func(t *testing.T) {
		directory := t.TempDir()
		if err := os.Chmod(directory, 0o770); err != nil {
			t.Fatal(err)
		}
		_, err := NewAcceptanceCandidateWriter(acceptanceTestConfig(filepath.Join(directory, "candidate.json")))
		if err == nil || !strings.Contains(err.Error(), "without group or other write") {
			t.Fatalf("unsafe parent error = %v", err)
		}
	})
	t.Run("symlink ancestry", func(t *testing.T) {
		root := t.TempDir()
		actual := filepath.Join(root, "actual")
		if err := os.Mkdir(actual, 0o700); err != nil {
			t.Fatal(err)
		}
		linked := filepath.Join(root, "linked")
		if err := os.Symlink(actual, linked); err != nil {
			t.Fatal(err)
		}
		if _, err := NewAcceptanceCandidateWriter(acceptanceTestConfig(filepath.Join(linked, "candidate.json"))); err == nil {
			t.Fatal("symlink parent error = nil")
		}
	})
	t.Run("missing", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "missing", "candidate.json")
		if _, err := NewAcceptanceCandidateWriter(acceptanceTestConfig(path)); err == nil {
			t.Fatal("missing parent error = nil")
		}
	})
}

func TestAcceptanceCandidateWriterRejectsInvalidOutcomeWithoutCreatingFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "candidate.json")
	writer := newTestAcceptanceCandidateWriter(t, path)
	for name, mutate := range map[string]func(*WorkerOutcome){
		"failed":               func(outcome *WorkerOutcome) { outcome.Disposition = WorkerFailed },
		"missing digest":       func(outcome *WorkerOutcome) { outcome.RequestSHA256 = "" },
		"unordered timestamps": func(outcome *WorkerOutcome) { outcome.HeartbeatAt = outcome.UploadedAt.Add(time.Second) },
	} {
		t.Run(name, func(t *testing.T) {
			outcome := acceptanceTestOutcome(WorkerActivated)
			mutate(&outcome)
			if err := writer.Record(outcome); err == nil {
				t.Fatal("invalid outcome error = nil")
			}
			if _, err := os.Lstat(path); !os.IsNotExist(err) {
				t.Fatalf("candidate stat error = %v", err)
			}
		})
	}
}

func newTestAcceptanceCandidateWriter(t *testing.T, path string) *AcceptanceCandidateWriter {
	t.Helper()
	writer, err := NewAcceptanceCandidateWriter(acceptanceTestConfig(path))
	if err != nil {
		t.Fatal(err)
	}
	return writer
}

func acceptanceTestConfig(path string) AcceptanceCandidateConfig {
	return AcceptanceCandidateConfig{
		Path: path, Release: ReleaseIdentity{Version: "1.2.3", Commit: strings.Repeat("e", 40)},
		AgentID: "rtx-01", Origin: "https://trainer.example",
	}
}

func acceptanceTestOutcome(disposition WorkerDisposition) WorkerOutcome {
	modelID := testModelID
	return WorkerOutcome{
		RunID: testRunID, AttemptToken: testAttemptToken,
		InputManifestSHA256: strings.Repeat("a", 64), OutputBundleSHA256: strings.Repeat("d", 64),
		RuntimeConstructionSHA256: strings.Repeat("1", 64), RuntimeProvenanceSHA256: strings.Repeat("2", 64),
		RuntimeTreeSHA256: strings.Repeat("3", 64), HostCapabilitySHA256: strings.Repeat("4", 64),
		RuntimeAttestationSHA256: strings.Repeat("5", 64),
		RequestSHA256:            strings.Repeat("c", 64), Disposition: disposition, ModelID: &modelID,
		ClaimedAt:   time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC),
		HeartbeatAt: time.Date(2030, 1, 2, 3, 4, 6, 0, time.UTC),
		UploadedAt:  time.Date(2030, 1, 2, 3, 4, 7, 0, time.UTC),
	}
}

func canonicalAcceptanceTestCandidate(t *testing.T) []byte {
	t.Helper()
	writer := &AcceptanceCandidateWriter{config: acceptanceTestConfig("/unused/candidate.json")}
	document, err := writer.document(acceptanceTestOutcome(WorkerActivated))
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := canonicalAcceptanceCandidate(document)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}
