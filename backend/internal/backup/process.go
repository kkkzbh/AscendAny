package backup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
)

const maximumCommandErrorBytes = 16 << 10

type commandExecutor interface {
	Dump(context.Context, CreateConfig, string, string, string) error
	ListDump(context.Context, ToolConfig, string) error
	Restore(context.Context, RestoreConfig, string, string) error
}

type systemCommandExecutor struct{}

func (systemCommandExecutor) Dump(ctx context.Context, config CreateConfig, snapshotID, dumpPath, pgpassPath string) error {
	command := exec.CommandContext(
		ctx,
		config.Tools.PGDump,
		"--format=custom",
		"--no-password",
		"--snapshot="+snapshotID,
		"--file="+dumpPath,
		"--dbname="+config.DatabaseURL,
	)
	command.Env = postgresCommandEnvironment(pgpassPath)
	return runCommand(command, "pg_dump")
}

func (systemCommandExecutor) ListDump(ctx context.Context, tools ToolConfig, dumpPath string) error {
	command := exec.CommandContext(ctx, tools.PGRestore, "--list", dumpPath)
	command.Env = closedCommandEnvironment()
	return runCommand(command, "pg_restore list")
}

func (systemCommandExecutor) Restore(ctx context.Context, config RestoreConfig, dumpPath, pgpassPath string) error {
	command := exec.CommandContext(
		ctx,
		config.Tools.PGRestore,
		"--exit-on-error",
		"--single-transaction",
		"--no-password",
		"--role="+RestoreDatabaseRole,
		"--dbname="+config.DatabaseURL,
		dumpPath,
	)
	command.Env = postgresCommandEnvironment(pgpassPath)
	return runCommand(command, "pg_restore")
}

func runCommand(command *exec.Cmd, operation string) error {
	var stderr limitedBuffer
	command.Stdout = nil
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			return fmt.Errorf("%s failed: %w", operation, err)
		}
		return fmt.Errorf("%s failed: %w: %s", operation, err, detail)
	}
	return nil
}

type limitedBuffer struct {
	buffer bytes.Buffer
}

func (buffer *limitedBuffer) Write(value []byte) (int, error) {
	original := len(value)
	remaining := maximumCommandErrorBytes - buffer.buffer.Len()
	if remaining > 0 {
		if len(value) > remaining {
			value = value[:remaining]
		}
		_, _ = buffer.buffer.Write(value)
	}
	return original, nil
}

func (buffer *limitedBuffer) String() string {
	return buffer.buffer.String()
}

func closedCommandEnvironment() []string {
	return []string{
		"PATH=/usr/bin:/bin",
		"LC_ALL=C",
	}
}

func postgresCommandEnvironment(pgpassPath string) []string {
	return append(closedCommandEnvironment(), "PGPASSFILE="+pgpassPath)
}

func writePGPass(root *os.Root, name, databaseURL, password string) error {
	if root == nil {
		return errors.New("runtime root is required for pgpass")
	}
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		return errors.New("parse database URL for pgpass")
	}
	if parsed.User == nil {
		return errors.New("database URL user is required for pgpass")
	}
	line := strings.Join([]string{
		escapePGPass(parsed.Hostname()),
		escapePGPass(parsed.Port()),
		escapePGPass(strings.TrimPrefix(parsed.Path, "/")),
		escapePGPass(parsed.User.Username()),
		escapePGPass(password),
	}, ":") + "\n"
	file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return errors.New("create private pgpass file")
	}
	complete := false
	defer func() {
		if !complete {
			_ = file.Close()
			_ = root.Remove(name)
		}
	}()
	if _, err := file.WriteString(line); err != nil {
		return errors.New("write private pgpass file")
	}
	if err := file.Close(); err != nil {
		return errors.New("close private pgpass file")
	}
	complete = true
	return nil
}

func escapePGPass(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	return strings.ReplaceAll(value, ":", `\:`)
}
