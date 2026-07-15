//go:build windows

package tunnel

import (
	"os/exec"
	"time"
)

// binExeSuffix is the executable extension for cloudflared on this platform.
const binExeSuffix = ".exe"

// configureProcAttr is a no-op on Windows (no process groups via SysProcAttr
// the same way; the child is terminated directly).
func configureProcAttr(cmd *exec.Cmd) {}

// killProcessGroup kills the cloudflared process, waiting up to timeout for a
// clean exit before force-killing. Windows has no SIGTERM equivalent for a
// console child, so terminate directly.
func killProcessGroup(cmd *exec.Cmd, timeout time.Duration) {
	if cmd.Process == nil {
		return
	}
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	_ = cmd.Process.Kill()
	select {
	case <-done:
	case <-time.After(timeout):
		<-done
	}
}
