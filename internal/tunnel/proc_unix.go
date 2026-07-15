//go:build !windows

package tunnel

import (
	"os/exec"
	"syscall"
	"time"
)

// binExeSuffix is the executable extension for cloudflared on this platform.
const binExeSuffix = ""

// configureProcAttr puts the child in its own process group so we can signal
// the whole group (cloudflared may spawn helpers).
func configureProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessGroup sends SIGTERM to the process group, waits up to timeout for
// a clean exit, then escalates to SIGKILL. Mirrors the teardown in
// internal/terminal/host_unix.go.
func killProcessGroup(cmd *exec.Cmd, timeout time.Duration) {
	if cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		<-done
	}
}
