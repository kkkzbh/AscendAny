package database

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PoolOptions struct {
	URL                   string
	Password              string
	MaxConnections        int32
	MinConnections        int32
	ConnectTimeout        time.Duration
	MaxConnectionLifetime time.Duration
	MaxConnectionIdleTime time.Duration
}

func ParsePoolConfig(options PoolOptions) (*pgxpool.Config, error) {
	if options.URL == "" {
		return nil, fmt.Errorf("database URL is required")
	}
	if options.Password == "" {
		return nil, fmt.Errorf("database password is required")
	}
	if options.MaxConnections <= 0 {
		return nil, fmt.Errorf("maximum connections must be positive")
	}
	if options.MinConnections < 0 || options.MinConnections > options.MaxConnections {
		return nil, fmt.Errorf("minimum connections must be between zero and maximum connections")
	}
	if options.ConnectTimeout <= 0 || options.MaxConnectionLifetime <= 0 || options.MaxConnectionIdleTime <= 0 {
		return nil, fmt.Errorf("database connection durations must be positive")
	}

	poolConfig, err := pgxpool.ParseConfig(options.URL)
	if err != nil {
		return nil, fmt.Errorf("parse database pool configuration: %w", err)
	}

	// PgBouncer transaction pooling cannot preserve session-scoped prepared
	// statements. Exec mode keeps the extended protocol and sends the SQL text
	// on every execution without preparing named statements on the server.
	poolConfig.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeExec
	poolConfig.ConnConfig.Password = options.Password
	poolConfig.ConnConfig.StatementCacheCapacity = 0
	poolConfig.ConnConfig.DescriptionCacheCapacity = 0
	poolConfig.ConnConfig.ConnectTimeout = options.ConnectTimeout
	poolConfig.MaxConns = options.MaxConnections
	poolConfig.MinConns = options.MinConnections
	poolConfig.MaxConnLifetime = options.MaxConnectionLifetime
	poolConfig.MaxConnIdleTime = options.MaxConnectionIdleTime

	return poolConfig, nil
}

func Open(ctx context.Context, options PoolOptions) (*pgxpool.Pool, error) {
	poolConfig, err := ParsePoolConfig(options)
	if err != nil {
		return nil, err
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("create database pool: %w", err)
	}
	return pool, nil
}
