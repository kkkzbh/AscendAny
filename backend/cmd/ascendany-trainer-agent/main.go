package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/kkkzbh/AscendAny/backend/internal/logging"
	"github.com/kkkzbh/AscendAny/backend/internal/traineragent"
	"github.com/kkkzbh/AscendAny/backend/internal/version"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	return runWithIO(args, os.Stdin, os.Stderr)
}

func runWithIO(args []string, stdin io.Reader, stderr io.Writer) int {
	bootstrapLogger, _ := logging.New(stderr, "info")
	if err := validateCommand(args); err != nil {
		bootstrapLogger.Error("command rejected", "usage", "ascendany-trainer-agent run | ascendany-trainer-agent verify-acceptance | ascendany-trainer-agent verify-runtime")
		return 2
	}
	if args[0] == "verify-acceptance" {
		if _, err := traineragent.VerifyAcceptanceCandidateReader(stdin); err != nil {
			bootstrapLogger.Error("trainer acceptance candidate rejected", "error", err)
			return 1
		}
		return 0
	}
	configuration, err := traineragent.LoadConfig(os.LookupEnv, os.ReadFile)
	if err != nil {
		bootstrapLogger.Error("trainer-agent configuration rejected", "error", err)
		return 1
	}
	logger, err := logging.New(stderr, configuration.LogLevel)
	if err != nil {
		bootstrapLogger.Error("trainer-agent logger configuration rejected", "error", err)
		return 1
	}
	slog.SetDefault(logger)
	if args[0] == "verify-runtime" {
		if err := traineragent.VerifyProductionRuntime(context.Background(), configuration); err != nil {
			logger.Error("trainer-agent runtime verification failed", "error", err)
			return 1
		}
		return 0
	}
	release := version.Current()
	runtime, err := traineragent.NewProductionRuntime(configuration, traineragent.ReleaseIdentity{
		Version: release.Version,
		Commit:  release.Commit,
	}, logger)
	if err != nil {
		logger.Error("trainer-agent runtime initialization failed", "error", err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := runtime.Run(ctx); err != nil {
		logger.Error("trainer-agent runtime stopped with an error", "error", err)
		return 1
	}
	return 0
}

func validateCommand(args []string) error {
	if len(args) != 1 || (args[0] != "run" && args[0] != "verify-acceptance" && args[0] != "verify-runtime") {
		return errors.New("usage: ascendany-trainer-agent run | ascendany-trainer-agent verify-acceptance | ascendany-trainer-agent verify-runtime")
	}
	return nil
}
