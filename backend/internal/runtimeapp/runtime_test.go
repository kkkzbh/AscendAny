package runtimeapp

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/kkkzbh/AscendAny/backend/internal/analytics"
	"github.com/kkkzbh/AscendAny/backend/internal/config"
	"github.com/kkkzbh/AscendAny/backend/internal/feedback"
)

type inertDatabase struct{}

type inertFeedbackProvider struct{}

func (inertFeedbackProvider) Deliver(context.Context, feedback.DeliveryRequest) ([]byte, error) {
	panic("Deliver must not be called during construction")
}

func (inertDatabase) Begin(context.Context) (pgx.Tx, error) {
	panic("Begin must not be called during construction")
}

func (inertDatabase) BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error) {
	panic("BeginTx must not be called during construction")
}

func (inertDatabase) QueryRow(context.Context, string, ...any) pgx.Row {
	panic("QueryRow must not be called during construction")
}

func (inertDatabase) Query(context.Context, string, ...any) (pgx.Rows, error) {
	panic("Query must not be called during construction")
}

func TestNewBuildsExactWriteRuntime(t *testing.T) {
	t.Parallel()
	configuration := testConfiguration(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	components, err := New(inertDatabase{}, configuration, inertFeedbackProvider{}, logger)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if components.Artifacts == nil || components.Imports == nil || components.Workers == nil {
		t.Fatalf("components = %#v", components)
	}
	for _, directory := range []string{"incoming", "sha256", ".locks"} {
		if info, err := os.Stat(filepath.Join(configuration.Artifact.Root, directory)); err != nil || !info.IsDir() {
			t.Fatalf("artifact directory %q: info=%v error=%v", directory, info, err)
		}
	}
}

func TestNewRejectsDisabledWritesBeforeFilesystemMutation(t *testing.T) {
	t.Parallel()
	configuration := testConfiguration(t)
	configuration.Write.Enabled = false
	root := configuration.Artifact.Root
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	if _, err := New(inertDatabase{}, configuration, inertFeedbackProvider{}, logger); err == nil || err.Error() != "write runtime cannot start while writes are disabled" {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("artifact root exists after rejected construction: %v", err)
	}
}

func TestNewRejectsOversizedAnalyticsConfigBeforeArtifactInitialization(t *testing.T) {
	t.Parallel()
	configuration := testConfiguration(t)
	if err := os.WriteFile(configuration.Analytics.ConfigPath, []byte(strings.Repeat("x", analytics.MaxConfigBytes+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	if _, err := New(inertDatabase{}, configuration, inertFeedbackProvider{}, logger); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("New() error = %v", err)
	}
	if _, err := os.Stat(configuration.Artifact.Root); !os.IsNotExist(err) {
		t.Fatalf("artifact root exists after rejected analytics config: %v", err)
	}
}

func testConfiguration(t *testing.T) config.Config {
	t.Helper()
	base := t.TempDir()
	analyticsPath := filepath.Join(base, "analytics.json")
	data := []byte(`{
  "algorithmVersion": "ascendany_analytics_v1",
  "acceptedVerdicts": ["ACCEPTED"],
  "winsor": {"low": 0.05, "high": 0.95},
  "halfLifeDays": {"knowledge": 45, "accuracy": 21, "quality": 45, "flexibility": 21, "proficiency": 21},
  "rating": {"initial": 800, "binarySearchMin": -2000, "binarySearchMax": 8000, "binarySearchSteps": 30}
}`)
	if err := os.WriteFile(analyticsPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return config.Config{
		Artifact: config.ArtifactConfig{
			Root: filepath.Join(base, "artifacts"), MaxBytes: 64 << 20,
			OrphanMinAge: time.Hour, ReconcileInterval: time.Minute,
		},
		Pintia: config.PintiaConfig{
			MaxTotalNodes: 2_000_000, MaxTotalStringBytes: 32 << 20, MaxJSONDepth: 32, MaxStringBytes: 8 << 20,
			MaxProblems: 1_000, MaxParticipants: 20_000, MaxProblemResultsPerRanking: 1_000,
			MaxSubmissions: 200_000, MaxCaseResultsPerSubmission: 1_000, MaxCodeBytes: 1 << 20,
		},
		Import: config.ImportConfig{
			WorkerOwner: "test-import", LeaseDuration: time.Minute, RetryDelay: time.Second, PollInterval: time.Millisecond,
		},
		Analytics: config.AnalyticsConfig{
			ConfigPath: analyticsPath, WorkerOwner: "test-analytics", LeaseDuration: time.Minute, PollInterval: time.Millisecond,
		},
		Feedback: config.FeedbackConfig{
			RateWindow: time.Hour, RateMaximum: 5, DeliveryConfigurationKey: "feedback.delivery.default",
			WorkerOwner: "test-feedback", LeaseDuration: time.Minute, RetryDelay: time.Second, PollInterval: time.Millisecond,
		},
		Write: config.WriteConfig{Enabled: true},
	}
}
