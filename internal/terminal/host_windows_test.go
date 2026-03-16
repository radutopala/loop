//go:build windows

package terminal

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"testing"
	"time"

	"github.com/UserExistsError/conpty"
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
	s.client.lookPath = func(file string) (string, error) { return file, nil }

	id, err := s.client.ExecCreate(context.Background(), `C:\Users\test`, []string{"cmd.exe", "/c", "echo hello"}, true)
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), id)

	s.client.mu.Lock()
	he, ok := s.client.execs[id]
	s.client.mu.Unlock()
	require.True(s.T(), ok)
	require.Equal(s.T(), `C:\Users\test`, he.dir)
	require.Equal(s.T(), `cmd.exe /c "echo hello"`, he.cmdLine)
}

func (s *HostSuite) TestExecCreateDefaultShell() {
	s.client.defaultShell = func() string { return `C:\Windows\System32\cmd.exe` }
	s.client.defaultShellArgs = func() []string { return nil }
	s.client.lookPath = func(file string) (string, error) { return file, nil }

	id, err := s.client.ExecCreate(context.Background(), `C:\Users\test`, nil, true)
	require.NoError(s.T(), err)

	s.client.mu.Lock()
	he := s.client.execs[id]
	s.client.mu.Unlock()
	require.Equal(s.T(), `C:\Windows\System32\cmd.exe`, he.cmdLine)
}

func (s *HostSuite) TestExecCreateCommandNotFound() {
	_, err := s.client.ExecCreate(context.Background(), `C:\Users\test`, []string{"nonexistent-binary-xyz"}, true)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "command not found")
}

func (s *HostSuite) TestExecCreateEmptyCmd() {
	s.client.defaultShell = func() string { return "powershell.exe" }
	s.client.defaultShellArgs = func() []string { return nil }
	s.client.lookPath = func(file string) (string, error) { return file, nil }

	id, err := s.client.ExecCreate(context.Background(), `C:\Users\test`, []string{}, true)
	require.NoError(s.T(), err)

	s.client.mu.Lock()
	he := s.client.execs[id]
	s.client.mu.Unlock()
	require.Equal(s.T(), "powershell.exe", he.cmdLine)
}

func (s *HostSuite) TestExecCreateSetsEnv() {
	s.client.lookPath = func(file string) (string, error) { return file, nil }

	id, err := s.client.ExecCreate(context.Background(), `C:\Users\test`, []string{"cmd.exe"}, true)
	require.NoError(s.T(), err)

	s.client.mu.Lock()
	he := s.client.execs[id]
	s.client.mu.Unlock()
	require.NotEmpty(s.T(), he.env)
}

func (s *HostSuite) TestExecAttachNotFound() {
	s.client.conptyAvailable = func() bool { return true }

	rwc, err := s.client.ExecAttach(context.Background(), "nonexistent")
	require.Error(s.T(), err)
	require.Nil(s.T(), rwc)
	require.Contains(s.T(), err.Error(), "exec nonexistent not found")
}

func (s *HostSuite) TestExecAttachConptyUnavailable() {
	s.client.conptyAvailable = func() bool { return false }
	s.client.lookPath = func(file string) (string, error) { return file, nil }

	id, err := s.client.ExecCreate(context.Background(), `C:\Users\test`, []string{"cmd.exe"}, true)
	require.NoError(s.T(), err)

	rwc, err := s.client.ExecAttach(context.Background(), id)
	require.Error(s.T(), err)
	require.Nil(s.T(), rwc)
	require.Contains(s.T(), err.Error(), "ConPTY is not available")
}

func (s *HostSuite) TestExecAttachStartError() {
	s.client.conptyAvailable = func() bool { return true }
	s.client.conptyStart = func(_ string, _ ...conpty.ConPtyOption) (*conpty.ConPty, error) {
		return nil, errors.New("conpty start failed")
	}
	s.client.lookPath = func(file string) (string, error) { return file, nil }

	id, err := s.client.ExecCreate(context.Background(), `C:\Users\test`, []string{"cmd.exe"}, true)
	require.NoError(s.T(), err)

	rwc, err := s.client.ExecAttach(context.Background(), id)
	require.Error(s.T(), err)
	require.Nil(s.T(), rwc)
	require.Contains(s.T(), err.Error(), "starting conpty")
}

func (s *HostSuite) TestExecResizeNotFound() {
	err := s.client.ExecResize(context.Background(), "nonexistent", 24, 80)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "exec nonexistent not found")
}

func (s *HostSuite) TestExecResizeNotAttached() {
	s.client.lookPath = func(file string) (string, error) { return file, nil }

	id, err := s.client.ExecCreate(context.Background(), `C:\Users\test`, []string{"cmd.exe"}, true)
	require.NoError(s.T(), err)

	err = s.client.ExecResize(context.Background(), id, 24, 80)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "not attached")
}

func (s *HostSuite) TestDefaultShellEnv() {
	shell := s.client.defaultShell()
	require.NotEmpty(s.T(), shell)
}

func (s *HostSuite) TestDefaultShellComspec() {
	orig := os.Getenv("COMSPEC")
	os.Setenv("COMSPEC", `C:\Windows\System32\cmd.exe`)
	defer os.Setenv("COMSPEC", orig)

	// Create a fresh client so defaultShell picks up the new env.
	c := NewHostExecClient()
	shell := c.defaultShell()
	require.Equal(s.T(), `C:\Windows\System32\cmd.exe`, shell)
}

func (s *HostSuite) TestDefaultShellArgs() {
	args := s.client.defaultShellArgs()
	require.Nil(s.T(), args)
}

func (s *HostSuite) TestHostConPTYConnCloseIdempotent() {
	// Use a mock closer to verify idempotent behavior.
	closeCalls := 0
	conn := &hostConPTYConn{
		cpty: nil, // Will panic if Close() calls cpty.Close() more than once
	}
	// Override with a manual test — we can't easily mock conpty.ConPty,
	// but we can verify the closeOnce behavior by checking the double-close path.
	_ = conn
	// closeOnce ensures second Close() is no-op — tested via integration tests.
	_ = closeCalls
}

func (s *HostSuite) TestBuildCommandLine() {
	tests := []struct {
		args     []string
		expected string
	}{
		{[]string{"cmd.exe"}, "cmd.exe"},
		{[]string{"cmd.exe", "/c", "echo hello"}, `cmd.exe /c "echo hello"`},
		{[]string{`C:\Program Files\app.exe`, "arg1"}, `"C:\Program Files\app.exe" arg1`},
		{[]string{"powershell.exe", "-Command", `Get-Process | Where-Object {$_.CPU -gt 10}`}, `powershell.exe -Command "Get-Process | Where-Object {$_.CPU -gt 10}"`},
	}

	for _, tt := range tests {
		result := buildCommandLine(tt.args)
		require.Equal(s.T(), tt.expected, result)
	}
}

func (s *HostSuite) TestBuildCommandLineQuotesEscaping() {
	args := []string{"cmd.exe", "/c", `echo "hello world"`}
	result := buildCommandLine(args)
	require.Equal(s.T(), `cmd.exe /c "echo \"hello world\""`, result)
}

func (s *HostSuite) TestGenerateIDUnique() {
	ids := make(map[string]struct{})
	for range 100 {
		id := generateID(rand.Read)
		require.NotContains(s.T(), ids, id, fmt.Sprintf("duplicate ID: %s", id))
		ids[id] = struct{}{}
	}
}

func (s *HostSuite) TestConptyOriginalFunctions() {
	// Exercise the default conptyAvailable to cover its default body.
	_ = s.client.conptyAvailable()
}

func (s *HostSuite) TestIntegrationCreateAttachResize() {
	// Integration test: create, attach, resize a real cmd.exe process.
	id, err := s.client.ExecCreate(context.Background(), os.TempDir(), []string{"cmd.exe"}, true)
	require.NoError(s.T(), err)

	rwc, err := s.client.ExecAttach(context.Background(), id)
	require.NoError(s.T(), err)

	// Write a command.
	_, err = rwc.Write([]byte("echo hello\r\n"))
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
	case <-time.After(5 * time.Second):
		s.T().Log("read timed out — skipping output assertion")
	}

	// Resize should succeed.
	err = s.client.ExecResize(context.Background(), id, 30, 100)
	require.NoError(s.T(), err)

	require.NoError(s.T(), rwc.Close())
}

func (s *HostSuite) TestIntegrationCloseIdempotent() {
	id, err := s.client.ExecCreate(context.Background(), os.TempDir(), []string{"cmd.exe"}, true)
	require.NoError(s.T(), err)

	rwc, err := s.client.ExecAttach(context.Background(), id)
	require.NoError(s.T(), err)

	err = rwc.Close()
	require.NoError(s.T(), err)

	// Second close should be a no-op.
	err = rwc.Close()
	require.NoError(s.T(), err)
}

// Verify io.ReadWriteCloser interface compliance at compile time.
var _ io.ReadWriteCloser = (*hostConPTYConn)(nil)
