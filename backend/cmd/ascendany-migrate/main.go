package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/kkkzbh/AscendAny/backend/internal/migrate"
)

type upFunc func(context.Context, migrate.Config) error

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.LookupEnv, os.ReadFile, os.Stderr, migrate.Up))
}

func run(
	ctx context.Context,
	args []string,
	lookup migrate.LookupEnv,
	readFile migrate.ReadFile,
	stderr io.Writer,
	up upFunc,
) int {
	logger := slog.New(slog.NewJSONHandler(stderr, nil))
	if err := validateCommand(args); err != nil {
		logger.Error("command rejected", "error", err)
		return 2
	}
	if up == nil {
		logger.Error("migration runner is required")
		return 1
	}

	configuration, err := migrate.LoadConfig(lookup, readFile)
	if err != nil {
		logger.Error("configuration rejected", "error", err)
		return 1
	}
	if err := up(ctx, configuration); err != nil {
		logger.Error("migration failed", "error", err)
		return 1
	}
	logger.Info("migration completed", "schemaVersion", migrate.CurrentVersion())
	return 0
}

func validateCommand(args []string) error {
	if len(args) != 1 || args[0] != "up" {
		return errors.New("usage: ascendany-migrate up")
	}
	return nil
}
