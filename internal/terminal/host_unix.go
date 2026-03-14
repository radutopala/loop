//go:build darwin || linux

package terminal

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
)

// hostExec holds state for a single host exec process.
type hostExec struct {
	cmd *exec.Cmd
	pty *os.File
}

// defaultShell returns the user's preferred shell.
var defaultShell = func() string {
	if s := os.Getenv("SHELL"); s != "" {
		return s
	}
	if _, err := exec.LookPath("/bin/zsh"); err == nil {
		return "/bin/zsh"
	}
	return "/bin/sh"
}

// defaultShellArgs returns the default arguments for the shell.
var defaultShellArgs = func() []string {
	return []string{"-l"}
}

// ptyStart wraps pty.Start for testing.
var ptyStart = pty.Start

// ptySetsize wraps pty.Setsize for testing.
var ptySetsize = pty.Setsize

// ExecCreate creates a new exec process. The dirPath parameter
// is used as the working directory. The process is not started until
// ExecAttach is called.
func (c *HostExecClient) ExecCreate(_ context.Context, dirPath string, cmd []string, _ bool) (string, error) {
	if len(cmd) == 0 {
		shell := defaultShell()
		cmd = append([]string{shell}, defaultShellArgs()...)
	}

	// Validate the command resolves to an actual executable on PATH.
	resolvedPath, err := lookPath(cmd[0])
	if err != nil {
		return "", fmt.Errorf("command not found: %s", cmd[0])
	}

	command := exec.Command(resolvedPath, cmd[1:]...) // #nosec G204 — host terminal; user runs commands on their own machine
	command.Dir = dirPath
	command.Env = os.Environ()
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}

	id := generateID()
	c.mu.Lock()
	c.execs[id] = &hostExec{cmd: command}
	c.mu.Unlock()

	return id, nil
}

// processCleanupTimeout is the maximum time to wait for a process to exit
// after SIGHUP before sending SIGKILL.
var processCleanupTimeout = 3 * time.Second

// ExecAttach starts the exec process with a PTY and returns
// a ReadWriteCloser wrapping the PTY file descriptor. Close sends
// SIGHUP to the process group, waits briefly, then SIGKILL.
func (c *HostExecClient) ExecAttach(_ context.Context, execID string) (io.ReadWriteCloser, error) {
	c.mu.Lock()
	he, ok := c.execs[execID]
	c.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("exec %s not found", execID)
	}

	ptmx, err := ptyStart(he.cmd)
	if err != nil {
		return nil, fmt.Errorf("starting pty: %w", err)
	}

	c.mu.Lock()
	he.pty = ptmx
	c.mu.Unlock()

	return &hostPTYConn{
		pty: ptmx,
		cmd: he.cmd,
	}, nil
}

// ExecResize changes the PTY dimensions of the exec process.
func (c *HostExecClient) ExecResize(_ context.Context, execID string, height, width uint) error {
	c.mu.Lock()
	he, ok := c.execs[execID]
	c.mu.Unlock()
	if !ok {
		return fmt.Errorf("exec %s not found", execID)
	}
	if he.pty == nil {
		return fmt.Errorf("exec %s not attached", execID)
	}
	return ptySetsize(he.pty, &pty.Winsize{
		Rows: uint16(height),
		Cols: uint16(width),
	})
}

// hostPTYConn wraps a PTY file descriptor and its associated command
// as an io.ReadWriteCloser. Close cleans up the process group.
type hostPTYConn struct {
	pty       *os.File
	cmd       *exec.Cmd
	closeOnce sync.Once
}

func (h *hostPTYConn) Read(p []byte) (int, error) {
	return h.pty.Read(p)
}

func (h *hostPTYConn) Write(p []byte) (int, error) {
	return h.pty.Write(p)
}

func (h *hostPTYConn) Close() error {
	var closeErr error
	h.closeOnce.Do(func() {
		// Send SIGHUP to the process group.
		if h.cmd.Process != nil {
			_ = syscall.Kill(-h.cmd.Process.Pid, syscall.SIGHUP)

			// Wait with timeout, then force kill.
			done := make(chan struct{})
			go func() {
				_ = h.cmd.Wait()
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(processCleanupTimeout):
				_ = syscall.Kill(-h.cmd.Process.Pid, syscall.SIGKILL)
				<-done
			}
		}
		closeErr = h.pty.Close()
	})
	return closeErr
}
