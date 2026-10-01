//go:build !windows

package daemon

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func (RealSystem) StartDetached(name string, args []string, logFile string) (int, error) {
	f, err := os.OpenFile(logFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return 0, err
	}
	cmd := exec.Command(name, args...)
	cmd.Stdout, cmd.Stderr = f, f
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	// The child holds its own copy of the log descriptor; ours is closed
	// either way.
	if err := cmd.Start(); err != nil {
		return 0, errors.Join(err, f.Close())
	}
	pid := cmd.Process.Pid
	return pid, errors.Join(cmd.Process.Release(), f.Close())
}

func (RealSystem) Terminate(pid int) error { return syscall.Kill(pid, syscall.SIGTERM) }
