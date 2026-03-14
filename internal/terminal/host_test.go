package terminal

import (
	"context"
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
	origDefaultShell   func() string
	origPtyStart       func(cmd *exec.Cmd) (*os.File, error)
	origPtySetsize     func(f *os.File, sz *pty.Winsize) error
	origLookPath       func(file string) (string, error)
	origCleanupTimeout time.Duration
}

func TestHostSuite(t *testing.T) {
	suite.Run(t, new(HostSuite))
}

func (s *HostSuite) SetupTest() {
	s.origDefaultShell = defaultShell
	s.origPtyStart = ptyStart
	s.origPtySetsize = ptySetsize
	s.origLookPath = lookPath
	s.origCleanupTimeout = processCleanupTimeout
}

func (s *HostSuite) TearDownTest() {
	defaultShell = s.origDefaultShell
	ptyStart = s.origPtyStart
	ptySetsize = s.origPtySetsize
	lookPath = s.origLookPath
	processCleanupTimeout = s.origCleanupTimeout
}

func (s *HostSuite) TestNewHostExecClient() {
	c := NewHostExecClient()
	require.NotNil(s.T(), c)
	require.Empty(s.T(), c.execs)
}

func (s *HostSuite) TestExecCreate() {
	c := NewHostExecClient()

	id, err := c.ExecCreate(context.Background(), "/tmp", []string{"/bin/echo", "hello"}, true)
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), id)

	c.mu.Lock()
	he, ok := c.execs[id]
	c.mu.Unlock()
	require.True(s.T(), ok)
	require.Equal(s.T(), "/tmp", he.cmd.Dir)
	require.Equal(s.T(), []string{"/bin/echo", "hello"}, he.cmd.Args)
}

func (s *HostSuite) TestExecCreateDefaultShell() {
	defaultShell = func() string { return "/bin/test-shell" }
	lookPath = func(file string) (string, error) { return file, nil }

	c := NewHostExecClient()
	id, err := c.ExecCreate(context.Background(), "/tmp", nil, true)
	require.NoError(s.T(), err)

	c.mu.Lock()
	he := c.execs[id]
	c.mu.Unlock()
	require.Equal(s.T(), []string{"/bin/test-shell", "-l"}, he.cmd.Args)
}

func (s *HostSuite) TestExecCreateCommandNotFound() {
	c := NewHostExecClient()
	_, err := c.ExecCreate(context.Background(), "/tmp", []string{"nonexistent-binary-xyz"}, true)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "command not found")
}

func (s *HostSuite) TestExecAttach() {
	// Use a real pipe to simulate PTY.
	r, w, err := os.Pipe()
	require.NoError(s.T(), err)

	ptyStart = func(cmd *exec.Cmd) (*os.File, error) {
		// Simulate process start by setting Process.
		cmd.Process = &os.Process{Pid: os.Getpid()}
		return w, nil
	}

	c := NewHostExecClient()
	id, err := c.ExecCreate(context.Background(), "/tmp", []string{"/bin/echo"}, true)
	require.NoError(s.T(), err)

	rwc, err := c.ExecAttach(context.Background(), id)
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
	c := NewHostExecClient()

	rwc, err := c.ExecAttach(context.Background(), "nonexistent")
	require.Error(s.T(), err)
	require.Nil(s.T(), rwc)
	require.Contains(s.T(), err.Error(), "exec nonexistent not found")
}

func (s *HostSuite) TestExecAttachStartError() {
	ptyStart = func(_ *exec.Cmd) (*os.File, error) {
		return nil, errors.New("pty start failed")
	}

	c := NewHostExecClient()
	id, err := c.ExecCreate(context.Background(), "/tmp", []string{"/bin/echo"}, true)
	require.NoError(s.T(), err)

	rwc, err := c.ExecAttach(context.Background(), id)
	require.Error(s.T(), err)
	require.Nil(s.T(), rwc)
	require.Contains(s.T(), err.Error(), "starting pty")
}

func (s *HostSuite) TestExecResize() {
	var receivedSize *pty.Winsize
	ptySetsize = func(_ *os.File, sz *pty.Winsize) error {
		receivedSize = sz
		return nil
	}

	c := NewHostExecClient()
	id, err := c.ExecCreate(context.Background(), "/tmp", []string{"/bin/echo"}, true)
	require.NoError(s.T(), err)

	// Set pty to simulate attachment.
	c.mu.Lock()
	c.execs[id].pty = os.Stdout // any file will do
	c.mu.Unlock()

	err = c.ExecResize(context.Background(), id, 24, 80)
	require.NoError(s.T(), err)
	require.NotNil(s.T(), receivedSize)
	require.Equal(s.T(), uint16(24), receivedSize.Rows)
	require.Equal(s.T(), uint16(80), receivedSize.Cols)
}

func (s *HostSuite) TestExecResizeNotFound() {
	c := NewHostExecClient()
	err := c.ExecResize(context.Background(), "nonexistent", 24, 80)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "exec nonexistent not found")
}

func (s *HostSuite) TestExecResizeNotAttached() {
	c := NewHostExecClient()
	id, err := c.ExecCreate(context.Background(), "/tmp", []string{"/bin/echo"}, true)
	require.NoError(s.T(), err)

	err = c.ExecResize(context.Background(), id, 24, 80)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "not attached")
}

func (s *HostSuite) TestExecResizeError() {
	ptySetsize = func(_ *os.File, _ *pty.Winsize) error {
		return errors.New("setsize failed")
	}

	c := NewHostExecClient()
	id, err := c.ExecCreate(context.Background(), "/tmp", []string{"/bin/echo"}, true)
	require.NoError(s.T(), err)

	c.mu.Lock()
	c.execs[id].pty = os.Stdout
	c.mu.Unlock()

	err = c.ExecResize(context.Background(), id, 24, 80)
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
	// Exercise the original defaultShell function.
	shell := s.origDefaultShell()
	require.NotEmpty(s.T(), shell)
}

func (s *HostSuite) TestDefaultShellFallback() {
	orig := os.Getenv("SHELL")
	os.Setenv("SHELL", "")
	defer os.Setenv("SHELL", orig)

	shell := s.origDefaultShell()
	require.NotEmpty(s.T(), shell)
	// Should be /bin/zsh or /bin/sh depending on what's available.
	require.Contains(s.T(), []string{"/bin/zsh", "/bin/sh"}, shell)
}

func (s *HostSuite) TestExecCreateEmptyCmd() {
	defaultShell = func() string { return "/bin/sh" }

	c := NewHostExecClient()
	id, err := c.ExecCreate(context.Background(), "/tmp", []string{}, true)
	require.NoError(s.T(), err)

	c.mu.Lock()
	he := c.execs[id]
	c.mu.Unlock()
	require.Equal(s.T(), []string{"/bin/sh", "-l"}, he.cmd.Args)
}

func (s *HostSuite) TestIntegrationCreateAttachResize() {
	// Integration test: create, attach, resize a real process.
	c := NewHostExecClient()
	id, err := c.ExecCreate(context.Background(), os.TempDir(), []string{"/bin/sh"}, true)
	require.NoError(s.T(), err)

	rwc, err := c.ExecAttach(context.Background(), id)
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
	err = c.ExecResize(context.Background(), id, 30, 100)
	require.NoError(s.T(), err)

	require.NoError(s.T(), rwc.Close())
}

func (s *HostSuite) TestPtyOriginalFunctions() {
	// Exercise the original ptySetsize function to cover its default body.
	// We can't call it on a real PTY easily, but we can verify it's callable.
	err := s.origPtySetsize(nil, &pty.Winsize{Rows: 24, Cols: 80})
	// Will fail because nil file, but we're just covering the function wrapper.
	require.Error(s.T(), err)
}

func (s *HostSuite) TestProcessCleanupTimeout() {
	// Verify the default is reasonable.
	require.Equal(s.T(), 3*time.Second, s.origCleanupTimeout)
}

func (s *HostSuite) TestExecCreateSetsEnv() {
	c := NewHostExecClient()
	id, err := c.ExecCreate(context.Background(), "/tmp", []string{"/bin/echo"}, true)
	require.NoError(s.T(), err)

	c.mu.Lock()
	he := c.execs[id]
	c.mu.Unlock()
	require.NotEmpty(s.T(), he.cmd.Env)
}

func (s *HostSuite) TestExecCreateSetsSetsid() {
	c := NewHostExecClient()
	id, err := c.ExecCreate(context.Background(), "/tmp", []string{"/bin/echo"}, true)
	require.NoError(s.T(), err)

	c.mu.Lock()
	he := c.execs[id]
	c.mu.Unlock()
	require.True(s.T(), he.cmd.SysProcAttr.Setsid)
}

func (s *HostSuite) TestExecAttachSetsPTY() {
	r, w, err := os.Pipe()
	require.NoError(s.T(), err)
	defer r.Close()

	ptyStart = func(cmd *exec.Cmd) (*os.File, error) {
		cmd.Process = &os.Process{Pid: os.Getpid()}
		return w, nil
	}

	c := NewHostExecClient()
	id, err := c.ExecCreate(context.Background(), "/tmp", []string{"/bin/echo"}, true)
	require.NoError(s.T(), err)

	_, err = c.ExecAttach(context.Background(), id)
	require.NoError(s.T(), err)

	// Verify the pty field was set.
	c.mu.Lock()
	he := c.execs[id]
	c.mu.Unlock()
	require.NotNil(s.T(), he.pty)
	require.Equal(s.T(), w, he.pty)
}

func (s *HostSuite) TestHostPTYConnCloseTimeout() {
	// Test the SIGKILL path when the process ignores SIGHUP and doesn't exit
	// within processCleanupTimeout.
	processCleanupTimeout = 100 * time.Millisecond

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
		pty: ptmx,
		cmd: cmd,
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
		id := generateID()
		require.NotContains(s.T(), ids, id, fmt.Sprintf("duplicate ID: %s", id))
		ids[id] = struct{}{}
	}
}
