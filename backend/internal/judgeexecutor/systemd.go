package judgeexecutor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/kkkzbh/AscendAny/backend/internal/oj"
)

type SystemdLauncher struct {
	systemctl string
}

func NewSystemdLauncher(systemctl string) (*SystemdLauncher, error) {
	if systemctl == "" || !filepath.IsAbs(systemctl) || filepath.Clean(systemctl) != systemctl {
		return nil, errors.New("absolute systemctl path is required")
	}
	info, err := os.Stat(systemctl)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return nil, errors.New("systemctl must be an executable regular file")
	}
	return &SystemdLauncher{systemctl: systemctl}, nil
}

func (launcher *SystemdLauncher) Start(ctx context.Context, jobID string) error {
	return launcher.control(ctx, "start", jobID)
}

func (launcher *SystemdLauncher) Stop(ctx context.Context, jobID string) error {
	return launcher.control(ctx, "stop", jobID)
}

func (launcher *SystemdLauncher) control(ctx context.Context, verb, jobID string) error {
	if ctx == nil || !oj.ValidPublicID(jobID) || (verb != "start" && verb != "stop") {
		return errors.New("canonical job ID and systemd verb are required")
	}
	unit := "ascendany-judge@" + jobID + ".service"
	arguments := []string{"--system"}
	if verb == "start" {
		arguments = append(arguments, "--no-block")
	}
	arguments = append(arguments, verb, unit)
	command := exec.CommandContext(ctx, launcher.systemctl, arguments...)
	command.Env = []string{"PATH=/usr/bin:/bin", "LANG=C.UTF-8"}
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	output, err := command.CombinedOutput()
	if err != nil {
		if len(output) > 512 {
			output = output[:512]
		}
		return fmt.Errorf("systemctl %s judge unit: %w: %s", verb, err, strings.ToValidUTF8(string(output), "�"))
	}
	return nil
}
