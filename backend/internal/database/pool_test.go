package database

import (
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestParsePoolConfigIsSafeForPgBouncerTransactionPooling(t *testing.T) {
	t.Parallel()

	got, err := ParsePoolConfig(PoolOptions{
		URL:                   "postgres://ascendany@127.0.0.1:6432/ascendany",
		Password:              "database-password",
		MaxConnections:        12,
		MinConnections:        1,
		ConnectTimeout:        4 * time.Second,
		MaxConnectionLifetime: 20 * time.Minute,
		MaxConnectionIdleTime: 2 * time.Minute,
	})
	if err != nil {
		t.Fatalf("ParsePoolConfig() error = %v", err)
	}
	if got.ConnConfig.DefaultQueryExecMode != pgx.QueryExecModeExec {
		t.Fatalf("query mode = %v, want QueryExecModeExec", got.ConnConfig.DefaultQueryExecMode)
	}
	if got.ConnConfig.Password != "database-password" {
		t.Fatal("database password was not applied to the pgx connection config")
	}
	if got.ConnConfig.StatementCacheCapacity != 0 || got.ConnConfig.DescriptionCacheCapacity != 0 {
		t.Fatalf("cache capacities = %d/%d, want 0/0", got.ConnConfig.StatementCacheCapacity, got.ConnConfig.DescriptionCacheCapacity)
	}
	if got.MaxConns != 12 || got.MinConns != 1 {
		t.Fatalf("pool bounds = %d..%d", got.MinConns, got.MaxConns)
	}
	if got.ConnConfig.ConnectTimeout != 4*time.Second {
		t.Fatalf("connect timeout = %s", got.ConnConfig.ConnectTimeout)
	}
}

func TestIntegrationRuntimeURLIsSafeForPgBouncerTransactionPooling(t *testing.T) {
	t.Parallel()

	databaseURL := os.Getenv("ASCENDANY_INTEGRATION_RUNTIME_DATABASE_URL_CONTRACT")
	if databaseURL == "" {
		t.Skip("integration runtime database URL contract is not configured")
	}
	got, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse integration runtime database URL: %v", err)
	}
	if got.ConnConfig.DefaultQueryExecMode != pgx.QueryExecModeExec {
		t.Fatalf("query mode = %v, want QueryExecModeExec", got.ConnConfig.DefaultQueryExecMode)
	}
	if got.ConnConfig.StatementCacheCapacity != 0 || got.ConnConfig.DescriptionCacheCapacity != 0 {
		t.Fatalf("cache capacities = %d/%d, want 0/0", got.ConnConfig.StatementCacheCapacity, got.ConnConfig.DescriptionCacheCapacity)
	}
}
