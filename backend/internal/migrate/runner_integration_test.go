package migrate

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestPostgresFreshMigrationAndIdempotentReentry(t *testing.T) {
	databaseURL := os.Getenv("ASCENDANY_MIGRATE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("ASCENDANY_MIGRATE_TEST_DATABASE_URL is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	configuration := Config{
		DatabaseURL:    databaseURL,
		Password:       "local-rehearsal-password",
		LockTimeout:    5 * time.Second,
		ConnectTimeout: 5 * time.Second,
	}
	if err := Up(ctx, configuration); err != nil {
		t.Fatalf("Up(fresh) error = %v", err)
	}
	if err := Up(ctx, configuration); err != nil {
		t.Fatalf("Up(idempotent) error = %v", err)
	}
	connectionConfig, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	connectionConfig.Password = configuration.Password
	connection, err := pgx.ConnectConfig(ctx, connectionConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close(context.Background())
	if _, err := connection.Exec(ctx, `SET ROLE ascendany_owner`); err != nil {
		t.Fatal(err)
	}
	history, err := readHistory(ctx, connection)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != len(embeddedManifest) {
		t.Fatalf("history length = %d, want %d", len(history), len(embeddedManifest))
	}
	for index := range embeddedManifest {
		if history[index] != embeddedManifest[index] {
			t.Fatalf("history[%d] = %#v, want %#v", index, history[index], embeddedManifest[index])
		}
	}
}
