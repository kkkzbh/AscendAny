package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/lsp"
	"github.com/kkkzbh/AscendAny/backend/internal/lsprunner"
)

type options struct {
	sessionID      string
	controlSocket  string
	workspace      string
	connectTimeout time.Duration
	sessionTimeout time.Duration
	cleanupTimeout time.Duration
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "ascendany-lsp:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, arguments []string) error {
	if os.Geteuid() == 0 {
		return errors.New("refusing to run LSP worker as root")
	}
	parsed, err := parseOptions(arguments)
	if err != nil {
		return err
	}
	config := lsprunner.DefaultConfig(parsed.sessionID, parsed.controlSocket, parsed.workspace)
	config.ConnectTimeout = parsed.connectTimeout
	config.CleanupTimeout = parsed.cleanupTimeout
	config.Policy.MaximumSessionDuration = parsed.sessionTimeout
	runner, err := lsprunner.New(config)
	if err != nil {
		return fmt.Errorf("configure LSP worker: %w", err)
	}
	if err := runner.Serve(ctx); err != nil {
		return fmt.Errorf("serve LSP session: %w", err)
	}
	return nil
}

func parseOptions(arguments []string) (options, error) {
	var parsed options
	if len(arguments) == 0 || arguments[0] != "serve" {
		return parsed, errors.New("usage: ascendany-lsp serve [flags]")
	}
	set := flag.NewFlagSet("ascendany-lsp serve", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	set.StringVar(&parsed.sessionID, "session-id", "", "canonical LSP session UUID")
	set.StringVar(&parsed.controlSocket, "control-socket", "", "shared ascendanyd Unix control socket")
	set.StringVar(&parsed.workspace, "workspace", "", "per-session private workspace")
	set.DurationVar(&parsed.connectTimeout, "connect-timeout", 10*time.Second, "control socket connection timeout")
	set.DurationVar(&parsed.sessionTimeout, "session-timeout", 30*time.Minute, "whole-session hard timeout")
	set.DurationVar(&parsed.cleanupTimeout, "cleanup-timeout", 2*time.Second, "process and workspace cleanup timeout")
	if err := set.Parse(arguments[1:]); err != nil {
		return parsed, errors.New("invalid command flags")
	}
	if len(set.Args()) != 0 || !lsp.ValidPublicID(parsed.sessionID) {
		return parsed, errors.New("canonical session ID and closed flags are required")
	}
	return parsed, nil
}
