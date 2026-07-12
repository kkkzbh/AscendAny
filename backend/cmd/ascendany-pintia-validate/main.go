package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/kkkzbh/AscendAny/backend/internal/pintia"
)

type openSnapshotFunc func(string) (io.ReadCloser, error)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], func(path string) (io.ReadCloser, error) {
		return os.Open(path)
	}, os.Stderr))
}

func run(ctx context.Context, args []string, openSnapshot openSnapshotFunc, stderr io.Writer) int {
	logger := slog.New(slog.NewJSONHandler(stderr, nil))
	if len(args) != 1 || args[0] == "" {
		logger.Error("command rejected", "error", "usage: ascendany-pintia-validate /absolute/snapshot.json")
		return 2
	}
	if openSnapshot == nil {
		logger.Error("snapshot opener is required")
		return 1
	}
	validator, err := pintia.NewEmbeddedValidator(pintia.DefaultLimits())
	if err != nil {
		logger.Error("validator initialization failed", "error", err)
		return 1
	}
	input, err := openSnapshot(args[0])
	if err != nil {
		logger.Error("snapshot open failed", "error", err)
		return 1
	}
	_, validationErr := validator.ValidateReader(ctx, input)
	closeErr := input.Close()
	if validationErr != nil {
		err = validationErr
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			logger.Error("snapshot validation interrupted", "error", err)
		} else {
			logger.Error("snapshot rejected", "error", err)
		}
		return 1
	}
	if closeErr != nil {
		logger.Error("snapshot close failed", "error", closeErr)
		return 1
	}
	logger.Info("snapshot accepted", "schema", pintia.SchemaV2, "schemaSHA256", validator.SchemaSHA256())
	return 0
}
