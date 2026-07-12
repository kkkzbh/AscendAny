package health

import (
	"context"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/database"
)

const (
	StatusReady    = "ready"
	StatusNotReady = "not_ready"
	StatusPass     = "pass"
	StatusFail     = "fail"
)

type DatabasePinger interface {
	Ping(context.Context) error
}

type MigrationStateReader interface {
	State(context.Context) (database.MigrationState, error)
}

type Check struct {
	Status          string `json:"status"`
	Message         string `json:"message,omitempty"`
	CurrentVersion  *int64 `json:"currentVersion,omitempty"`
	ExpectedVersion *int64 `json:"expectedVersion,omitempty"`
}

type Report struct {
	Status string           `json:"status"`
	Checks map[string]Check `json:"checks"`
}

type Readiness struct {
	database              DatabasePinger
	migrations            MigrationStateReader
	expectedSchemaVersion int64
	timeout               time.Duration
}

func NewReadiness(database DatabasePinger, migrations MigrationStateReader, expectedSchemaVersion int64, timeout time.Duration) *Readiness {
	return &Readiness{
		database:              database,
		migrations:            migrations,
		expectedSchemaVersion: expectedSchemaVersion,
		timeout:               timeout,
	}
}

func (readiness *Readiness) Check(ctx context.Context) Report {
	ctx, cancel := context.WithTimeout(ctx, readiness.timeout)
	defer cancel()

	report := Report{
		Status: StatusNotReady,
		Checks: make(map[string]Check, 2),
	}
	expected := readiness.expectedSchemaVersion

	if err := readiness.database.Ping(ctx); err != nil {
		report.Checks["database"] = Check{Status: StatusFail, Message: "database ping failed"}
		report.Checks["migrations"] = Check{
			Status:          StatusFail,
			Message:         "migration state unavailable",
			ExpectedVersion: &expected,
		}
		return report
	}
	report.Checks["database"] = Check{Status: StatusPass}

	state, err := readiness.migrations.State(ctx)
	if err != nil {
		report.Checks["migrations"] = Check{
			Status:          StatusFail,
			Message:         "migration state unavailable",
			ExpectedVersion: &expected,
		}
		return report
	}

	current := state.Version
	migrationCheck := Check{
		Status:          StatusPass,
		CurrentVersion:  &current,
		ExpectedVersion: &expected,
	}
	if state.Version != readiness.expectedSchemaVersion {
		migrationCheck.Status = StatusFail
		migrationCheck.Message = "database schema version does not match the binary"
	} else {
		report.Status = StatusReady
	}
	report.Checks["migrations"] = migrationCheck
	return report
}
