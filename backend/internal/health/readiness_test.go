package health

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/database"
)

type fakeDatabase struct {
	err error
}

func (database fakeDatabase) Ping(context.Context) error {
	return database.err
}

type fakeMigrations struct {
	state database.MigrationState
	err   error
	calls int
}

func (migrations *fakeMigrations) State(context.Context) (database.MigrationState, error) {
	migrations.calls++
	return migrations.state, migrations.err
}

func TestReadinessRequiresDatabaseAndExactSchemaVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		databaseError error
		state         database.MigrationState
		migrationErr  error
		wantStatus    string
		wantMessage   string
		wantCalls     int
	}{
		{
			name:          "database unavailable",
			databaseError: errors.New("offline"),
			wantStatus:    StatusNotReady,
			wantMessage:   "migration state unavailable",
			wantCalls:     0,
		},
		{
			name:         "migration table unavailable",
			migrationErr: errors.New("missing table"),
			wantStatus:   StatusNotReady,
			wantMessage:  "migration state unavailable",
			wantCalls:    1,
		},
		{
			name:        "version mismatch",
			state:       database.MigrationState{Version: 6},
			wantStatus:  StatusNotReady,
			wantMessage: "database schema version does not match the binary",
			wantCalls:   1,
		},
		{
			name:       "ready",
			state:      database.MigrationState{Version: 10},
			wantStatus: StatusReady,
			wantCalls:  1,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			migrations := &fakeMigrations{state: test.state, err: test.migrationErr}
			readiness := NewReadiness(fakeDatabase{err: test.databaseError}, migrations, 10, time.Second)

			report := readiness.Check(context.Background())
			if report.Status != test.wantStatus {
				t.Fatalf("status = %q, want %q", report.Status, test.wantStatus)
			}
			if report.Checks["migrations"].Message != test.wantMessage {
				t.Fatalf("migration message = %q, want %q", report.Checks["migrations"].Message, test.wantMessage)
			}
			if migrations.calls != test.wantCalls {
				t.Fatalf("migration calls = %d, want %d", migrations.calls, test.wantCalls)
			}
		})
	}
}
