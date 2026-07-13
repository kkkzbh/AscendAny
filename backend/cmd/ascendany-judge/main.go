package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"os/user"
	"strconv"
	"syscall"
	"time"

	"github.com/kkkzbh/AscendAny/backend/internal/judgecontract"
	"github.com/kkkzbh/AscendAny/backend/internal/judgerunner"
)

type options struct {
	jobID          string
	socketPath     string
	workRoot       string
	allowedUID     uint32
	podmanBinary   string
	hooksDirectory string
	compilerImage  string
	runtimeImage   string
	cgroupRoot     string
	acceptTimeout  time.Duration
	sessionTimeout time.Duration
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], lookupUID); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "ascendany-judge:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, arguments []string, lookup func(string) (uint32, error)) error {
	if os.Geteuid() == 0 {
		return errors.New("refusing to run judge as root")
	}
	parsed, err := parseOptions(arguments, lookup)
	if err != nil {
		return err
	}
	if err := judgerunner.PrepareDelegatedCgroup(parsed.cgroupRoot); err != nil {
		return fmt.Errorf("prepare delegated cgroup: %w", err)
	}
	compilerEngine, err := judgerunner.NewPodmanEngine(parsed.podmanBinary, parsed.compilerImage, parsed.hooksDirectory)
	if err != nil {
		return fmt.Errorf("configure compiler Podman engine: %w", err)
	}
	runtimeEngine, err := judgerunner.NewPodmanEngine(parsed.podmanBinary, parsed.runtimeImage, parsed.hooksDirectory)
	if err != nil {
		return fmt.Errorf("configure runtime Podman engine: %w", err)
	}
	runnerConfig := judgerunner.DefaultConfig(parsed.jobID, parsed.workRoot)
	runner, err := judgerunner.New(compilerEngine, runtimeEngine, runnerConfig)
	if err != nil {
		return fmt.Errorf("configure judge runner: %w", err)
	}
	server, err := judgerunner.NewServer(runner, judgerunner.ServerConfig{
		SocketPath: parsed.socketPath, AllowedClientUID: parsed.allowedUID,
		AcceptTimeout: parsed.acceptTimeout, MaximumSessionDuration: parsed.sessionTimeout,
	})
	if err != nil {
		return fmt.Errorf("configure judge server: %w", err)
	}
	if err := server.ServeOne(ctx); err != nil {
		return fmt.Errorf("serve judge job: %w", err)
	}
	return nil
}

func parseOptions(arguments []string, lookup func(string) (uint32, error)) (options, error) {
	var parsed options
	if len(arguments) == 0 || arguments[0] != "run" {
		return parsed, errors.New("usage: ascendany-judge run [flags]")
	}
	set := flag.NewFlagSet("ascendany-judge run", flag.ContinueOnError)
	set.SetOutput(io.Discard)
	allowedUser := ""
	set.StringVar(&parsed.jobID, "job-id", "", "canonical OJ judge job UUID")
	set.StringVar(&parsed.socketPath, "control-socket", "", "per-job Unix socket path")
	set.StringVar(&parsed.workRoot, "work-root", "", "per-job private work root")
	set.StringVar(&allowedUser, "allowed-client-user", "", "ascendanyd OS user")
	set.StringVar(&parsed.podmanBinary, "podman-binary", "/usr/bin/podman", "absolute Podman binary path")
	set.StringVar(&parsed.hooksDirectory, "hooks-directory", "/var/empty", "root-owned empty OCI hooks directory")
	set.StringVar(&parsed.compilerImage, "compiler-image", "", "digest-pinned reviewed C++20 compiler image")
	set.StringVar(&parsed.runtimeImage, "runtime-image", "", "digest-pinned empty execution image")
	set.StringVar(&parsed.cgroupRoot, "delegated-cgroup-root", "", "private delegated cgroup v2 root")
	set.DurationVar(&parsed.acceptTimeout, "accept-timeout", 30*time.Second, "Unix client accept timeout")
	set.DurationVar(&parsed.sessionTimeout, "session-timeout", 30*time.Minute, "whole-job hard timeout")
	if err := set.Parse(arguments[1:]); err != nil {
		return parsed, errors.New("invalid command flags")
	}
	if len(set.Args()) != 0 || !judgecontract.ValidPublicID(parsed.jobID) || allowedUser == "" || lookup == nil ||
		parsed.cgroupRoot != judgerunner.ProductionDelegatedCgroupRoot {
		return parsed, errors.New("canonical job ID, closed flags, and allowed client user are required")
	}
	uid, err := lookup(allowedUser)
	if err != nil || uid == 0 {
		return parsed, errors.New("allowed client user must resolve to a non-root UID")
	}
	parsed.allowedUID = uid
	return parsed, nil
}

func lookupUID(name string) (uint32, error) {
	account, err := user.Lookup(name)
	if err != nil {
		return 0, err
	}
	value, err := strconv.ParseUint(account.Uid, 10, 32)
	if err != nil {
		return 0, err
	}
	return uint32(value), nil
}
