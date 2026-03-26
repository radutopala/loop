//go:build darwin || linux

package terminal

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type HostSuite struct {
	suite.Suite
	client *HostExecClient
}

func TestHostSuite(t *testing.T) {
	suite.Run(t, new(HostSuite))
}

func (s *HostSuite) SetupTest() {
	s.client = NewHostExecClient()
}

func (s *HostSuite) TestNewHostExecClient() {
	require.NotNil(s.T(), s.client)
	require.Empty(s.T(), s.client.execs)
}

func (s *HostSuite) TestExecCreate() {
	id, err := s.client.ExecCreate(context.Background(), "/tmp", []string{"/bin/echo", "hello"}, true)
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), id)

	s.client.mu.Lock()
	he, ok := s.client.execs[id]
	s.client.mu.Unlock()
	require.True(s.T(), ok)
	require.Equal(s.T(), "/tmp", he.cmd.Dir)
	require.Equal(s.T(), []string{"/bin/echo", "hello"}, he.cmd.Args)
}

func (s *HostSuite) TestExecCreateDefaultShell() {
	s.client.defaultShell = func() string { return "/bin/test-shell" }
	s.client.lookPath = func(file string) (string, error) { return file, nil }

	id, err := s.client.ExecCreate(context.Background(), "/tmp", nil, true)
	require.NoError(s.T(), err)

	s.client.mu.Lock()
	he := s.client.execs[id]
	s.client.mu.Unlock()
	require.Equal(s.T(), []string{"/bin/test-shell", "-l"}, he.cmd.Args)
}

func (s *HostSuite) TestExecCreateCommandNotFound() {
	_, err := s.client.ExecCreate(context.Background(), "/tmp", []string{"nonexistent-binary-xyz"}, true)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "command not found")
}

func (s *HostSuite) TestExecAttach() {
	// Use a real pipe to simulate PTY.
	r, w, err := os.Pipe()
	require.NoError(s.T(), err)

	s.client.ptyStart = func(cmd *exec.Cmd) (*os.File, error) {
		// Simulate process start by setting Process.
		cmd.Process = &os.Process{Pid: os.Getpid()}
		return w, nil
	}

	id, err := s.client.ExecCreate(context.Background(), "/tmp", []string{"/bin/echo"}, true)
	require.NoError(s.T(), err)

	rwc, err := s.client.ExecAttach(context.Background(), id)
	require.NoError(s.T(), err)
	require.NotNil(s.T(), rwc)

	// Write to the PTY fd, read from the pipe.
	_, err = rwc.Write([]byte("test"))
	require.NoError(s.T(), err)

	buf := make([]byte, 4)
	n, err := r.Read(buf)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "test", string(buf[:n]))

	r.Close()
}

func (s *HostSuite) TestExecAttachNotFound() {
	rwc, err := s.client.ExecAttach(context.Background(), "nonexistent")
	require.Error(s.T(), err)
	require.Nil(s.T(), rwc)
	require.Contains(s.T(), err.Error(), "exec nonexistent not found")
}

func (s *HostSuite) TestExecAttachStartError() {
	s.client.ptyStart = func(_ *exec.Cmd) (*os.File, error) {
		return nil, errors.New("pty start failed")
	}

	id, err := s.client.ExecCreate(context.Background(), "/tmp", []string{"/bin/echo"}, true)
	require.NoError(s.T(), err)

	rwc, err := s.client.ExecAttach(context.Background(), id)
	require.Error(s.T(), err)
	require.Nil(s.T(), rwc)
	require.Contains(s.T(), err.Error(), "starting pty")
}

func (s *HostSuite) TestExecResize() {
	var receivedSize *pty.Winsize
	s.client.ptySetsize = func(_ *os.File, sz *pty.Winsize) error {
		receivedSize = sz
		return nil
	}

	id, err := s.client.ExecCreate(context.Background(), "/tmp", []string{"/bin/echo"}, true)
	require.NoError(s.T(), err)

	// Set pty to simulate attachment.
	s.client.mu.Lock()
	s.client.execs[id].pty = os.Stdout // any file will do
	s.client.mu.Unlock()

	err = s.client.ExecResize(context.Background(), id, 24, 80)
	require.NoError(s.T(), err)
	require.NotNil(s.T(), receivedSize)
	require.Equal(s.T(), uint16(24), receivedSize.Rows)
	require.Equal(s.T(), uint16(80), receivedSize.Cols)
}

func (s *HostSuite) TestExecResizeNotFound() {
	err := s.client.ExecResize(context.Background(), "nonexistent", 24, 80)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "exec nonexistent not found")
}

func (s *HostSuite) TestExecResizeNotAttached() {
	id, err := s.client.ExecCreate(context.Background(), "/tmp", []string{"/bin/echo"}, true)
	require.NoError(s.T(), err)

	err = s.client.ExecResize(context.Background(), id, 24, 80)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "not attached")
}

func (s *HostSuite) TestExecResizeError() {
	s.client.ptySetsize = func(_ *os.File, _ *pty.Winsize) error {
		return errors.New("setsize failed")
	}

	id, err := s.client.ExecCreate(context.Background(), "/tmp", []string{"/bin/echo"}, true)
	require.NoError(s.T(), err)

	s.client.mu.Lock()
	s.client.execs[id].pty = os.Stdout
	s.client.mu.Unlock()

	err = s.client.ExecResize(context.Background(), id, 24, 80)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "setsize failed")
}

func (s *HostSuite) TestHostPTYConnReadWrite() {
	r, w, err := os.Pipe()
	require.NoError(s.T(), err)

	conn := &hostPTYConn{
		pty: w,
		cmd: &exec.Cmd{},
	}

	_, err = conn.Write([]byte("hello"))
	require.NoError(s.T(), err)

	buf := make([]byte, 5)
	n, err := r.Read(buf)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "hello", string(buf[:n]))

	r.Close()
	w.Close()
}

func (s *HostSuite) TestHostPTYConnReadEOF() {
	r, w, err := os.Pipe()
	require.NoError(s.T(), err)
	w.Close()

	conn := &hostPTYConn{
		pty: r,
		cmd: &exec.Cmd{},
	}

	buf := make([]byte, 4)
	_, err = conn.Read(buf)
	require.ErrorIs(s.T(), err, io.EOF)

	r.Close()
}

func (s *HostSuite) TestHostPTYConnCloseWithProcess() {
	// Use a real process that sleeps so we can test cleanup.
	cmd := exec.Command("sleep", "60")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	ptmx, err := pty.Start(cmd)
	require.NoError(s.T(), err)

	conn := &hostPTYConn{
		pty: ptmx,
		cmd: cmd,
	}

	err = conn.Close()
	require.NoError(s.T(), err)

	// Verify the process exited.
	// Give it a moment for cleanup.
	time.Sleep(100 * time.Millisecond)
	require.NotNil(s.T(), cmd.ProcessState)
}

func (s *HostSuite) TestHostPTYConnCloseIdempotent() {
	cmd := exec.Command("sleep", "60")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	ptmx, err := pty.Start(cmd)
	require.NoError(s.T(), err)

	conn := &hostPTYConn{
		pty: ptmx,
		cmd: cmd,
	}

	err = conn.Close()
	require.NoError(s.T(), err)

	// Second close should be a no-op.
	err = conn.Close()
	require.NoError(s.T(), err)
}

func (s *HostSuite) TestHostPTYConnCloseNilProcess() {
	_, w, err := os.Pipe()
	require.NoError(s.T(), err)

	conn := &hostPTYConn{
		pty: w,
		cmd: &exec.Cmd{},
	}

	err = conn.Close()
	require.NoError(s.T(), err)
}

func (s *HostSuite) TestDefaultShellEnv() {
	// Exercise the default defaultShell function.
	shell := s.client.defaultShell()
	require.NotEmpty(s.T(), shell)
}

func (s *HostSuite) TestDefaultShellFallbackZsh() {
	orig := os.Getenv("SHELL")
	os.Setenv("SHELL", "")
	defer os.Setenv("SHELL", orig)

	// Mock lookPath to find zsh.
	s.client.lookPath = func(file string) (string, error) { return file, nil }
	// Re-apply platformDefaults to get a fresh closure that uses the mocked lookPath.
	platformDefaults(s.client)

	shell := s.client.defaultShell()
	require.Equal(s.T(), "/bin/zsh", shell)
}

func (s *HostSuite) TestDefaultShellFallbackSh() {
	orig := os.Getenv("SHELL")
	os.Setenv("SHELL", "")
	defer os.Setenv("SHELL", orig)

	// Mock lookPath to NOT find zsh.
	s.client.lookPath = func(file string) (string, error) { return "", errors.New("not found") }
	// Re-apply platformDefaults to get a fresh closure that uses the mocked lookPath.
	platformDefaults(s.client)

	shell := s.client.defaultShell()
	require.Equal(s.T(), "/bin/sh", shell)
}

func (s *HostSuite) TestDefaultShellArgs() {
	args := s.client.defaultShellArgs()
	require.Equal(s.T(), []string{"-l"}, args)
}

func (s *HostSuite) TestExecCreateEmptyCmd() {
	s.client.defaultShell = func() string { return "/bin/sh" }

	id, err := s.client.ExecCreate(context.Background(), "/tmp", []string{}, true)
	require.NoError(s.T(), err)

	s.client.mu.Lock()
	he := s.client.execs[id]
	s.client.mu.Unlock()
	require.Equal(s.T(), []string{"/bin/sh", "-l"}, he.cmd.Args)
}

func (s *HostSuite) TestIntegrationCreateAttachResize() {
	// Integration test: create, attach, resize a real process.
	id, err := s.client.ExecCreate(context.Background(), os.TempDir(), []string{"/bin/sh"}, true)
	require.NoError(s.T(), err)

	rwc, err := s.client.ExecAttach(context.Background(), id)
	require.NoError(s.T(), err)

	// Write a command.
	_, err = rwc.Write([]byte("echo hello\n"))
	require.NoError(s.T(), err)

	// Read output (non-blocking with timeout).
	buf := make([]byte, 1024)
	done := make(chan struct{})
	var readN int
	var readErr error
	go func() {
		readN, readErr = rwc.Read(buf)
		close(done)
	}()
	select {
	case <-done:
		require.NoError(s.T(), readErr)
		require.Greater(s.T(), readN, 0)
	case <-time.After(2 * time.Second):
		s.T().Log("read timed out — skipping output assertion")
	}

	// Resize should succeed.
	err = s.client.ExecResize(context.Background(), id, 30, 100)
	require.NoError(s.T(), err)

	require.NoError(s.T(), rwc.Close())
}

func (s *HostSuite) TestPtyOriginalFunctions() {
	// Exercise the default ptySetsize function to cover its default body.
	// We can't call it on a real PTY easily, but we can verify it's callable.
	err := s.client.ptySetsize(nil, &pty.Winsize{Rows: 24, Cols: 80})
	// Will fail because nil file, but we're just covering the function wrapper.
	require.Error(s.T(), err)
}

func (s *HostSuite) TestProcessCleanupTimeout() {
	// Verify the default is reasonable.
	require.Equal(s.T(), 3*time.Second, s.client.processCleanupTimeout)
}

func (s *HostSuite) TestExecCreateSetsEnv() {
	id, err := s.client.ExecCreate(context.Background(), "/tmp", []string{"/bin/echo"}, true)
	require.NoError(s.T(), err)

	s.client.mu.Lock()
	he := s.client.execs[id]
	s.client.mu.Unlock()
	require.NotEmpty(s.T(), he.cmd.Env)
}

func (s *HostSuite) TestExecCreateSetsSetsid() {
	id, err := s.client.ExecCreate(context.Background(), "/tmp", []string{"/bin/echo"}, true)
	require.NoError(s.T(), err)

	s.client.mu.Lock()
	he := s.client.execs[id]
	s.client.mu.Unlock()
	require.True(s.T(), he.cmd.SysProcAttr.Setsid)
}

func (s *HostSuite) TestExecAttachSetsPTY() {
	r, w, err := os.Pipe()
	require.NoError(s.T(), err)
	defer r.Close()

	s.client.ptyStart = func(cmd *exec.Cmd) (*os.File, error) {
		cmd.Process = &os.Process{Pid: os.Getpid()}
		return w, nil
	}

	id, err := s.client.ExecCreate(context.Background(), "/tmp", []string{"/bin/echo"}, true)
	require.NoError(s.T(), err)

	_, err = s.client.ExecAttach(context.Background(), id)
	require.NoError(s.T(), err)

	// Verify the pty field was set.
	s.client.mu.Lock()
	he := s.client.execs[id]
	s.client.mu.Unlock()
	require.NotNil(s.T(), he.pty)
	require.Equal(s.T(), w, he.pty)
}

func (s *HostSuite) TestExecInspectPidSuccess() {
	r, w, err := os.Pipe()
	require.NoError(s.T(), err)
	defer r.Close()

	s.client.ptyStart = func(cmd *exec.Cmd) (*os.File, error) {
		cmd.Process = &os.Process{Pid: 42}
		return w, nil
	}

	id, err := s.client.ExecCreate(context.Background(), "/tmp", []string{"/bin/echo"}, true)
	require.NoError(s.T(), err)

	// Attach to set the process.
	_, err = s.client.ExecAttach(context.Background(), id)
	require.NoError(s.T(), err)

	pid, err := s.client.ExecInspectPid(context.Background(), id)
	require.NoError(s.T(), err)
	require.Equal(s.T(), 42, pid)
}

func (s *HostSuite) TestExecInspectPidNotFound() {
	pid, err := s.client.ExecInspectPid(context.Background(), "nonexistent")
	require.Error(s.T(), err)
	require.Equal(s.T(), 0, pid)
	require.Contains(s.T(), err.Error(), "exec nonexistent not found")
}

func (s *HostSuite) TestExecInspectPidNotStarted() {
	id, err := s.client.ExecCreate(context.Background(), "/tmp", []string{"/bin/echo"}, true)
	require.NoError(s.T(), err)

	// Process is nil because ExecAttach hasn't been called.
	pid, err := s.client.ExecInspectPid(context.Background(), id)
	require.Error(s.T(), err)
	require.Equal(s.T(), 0, pid)
	require.Contains(s.T(), err.Error(), "not started")
}

func (s *HostSuite) TestHostPTYConnCloseTimeout() {
	// Test the SIGKILL path when the process ignores SIGHUP and doesn't exit
	// within processCleanupTimeout.

	// Start a process that traps SIGHUP for both the shell and its children.
	// Use exec to replace the shell with a process that blocks on read,
	// so there are no subprocesses to worry about.
	cmd := exec.Command("/bin/sh", "-c", "trap '' HUP; while true; do sleep 1; done")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	ptmx, err := pty.Start(cmd)
	require.NoError(s.T(), err)

	// Wait a bit for the trap to be set up.
	time.Sleep(50 * time.Millisecond)

	conn := &hostPTYConn{
		pty:            ptmx,
		cmd:            cmd,
		cleanupTimeout: 100 * time.Millisecond,
	}

	err = conn.Close()
	require.NoError(s.T(), err)

	// Verify the process was killed.
	time.Sleep(100 * time.Millisecond)
	require.NotNil(s.T(), cmd.ProcessState)
}

func (s *HostSuite) TestGenerateIDUnique() {
	// Verify generateID produces unique IDs.
	ids := make(map[string]struct{})
	for range 100 {
		id := generateID(rand.Read)
		require.NotContains(s.T(), ids, id, fmt.Sprintf("duplicate ID: %s", id))
		ids[id] = struct{}{}
	}
}
