//go:build linux

package syscallwrap

import (
	"bytes"
	"errors"
	"net"
	"os"
	"runtime"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"golang.org/x/sys/unix"
)

type AppSuite struct {
	suite.Suite
}

func TestAppSuite(t *testing.T) {
	suite.Run(t, new(AppSuite))
}

// socketPair returns two connected *net.UnixConn endpoints backed by a real
// kernel socketpair. Shared helper used by child_test.go and parent_test.go.
func socketPair(t testing.TB) (*net.UnixConn, *net.UnixConn) {
	t.Helper()
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	require.NoError(t, err)
	return fdToUnixConn(t, fds[0], "a"), fdToUnixConn(t, fds[1], "b")
}

func fdToUnixConn(t testing.TB, fd int, name string) *net.UnixConn {
	t.Helper()
	f := os.NewFile(uintptr(fd), name)
	c, err := net.FileConn(f)
	require.NoError(t, err)
	require.NoError(t, f.Close()) // FileConn dup'd the fd
	uc, ok := c.(*net.UnixConn)
	require.True(t, ok)
	return uc
}

// memfdForTest returns a disposable real fd we can pass over SCM_RIGHTS.
func memfdForTest(t testing.TB) int {
	t.Helper()
	fd, err := unix.MemfdCreate("loop-syscallwrap-test", 0)
	require.NoError(t, err)
	return fd
}

// --- parseArgs ---

func (s *AppSuite) TestParseArgsRequiresCommand() {
	_, err := parseArgs([]string{"loop-syscallwrap"})
	s.Require().Error(err)
}

func (s *AppSuite) TestParseArgsStripsDoubleDash() {
	got, err := parseArgs([]string{"loop-syscallwrap", "--", "claude", "-p", "hi"})
	s.Require().NoError(err)
	s.Require().Equal([]string{"claude", "-p", "hi"}, got)
}

func (s *AppSuite) TestParseArgsWithoutDoubleDash() {
	got, err := parseArgs([]string{"loop-syscallwrap", "claude", "-p", "hi"})
	s.Require().NoError(err)
	s.Require().Equal([]string{"claude", "-p", "hi"}, got)
}

func (s *AppSuite) TestParseArgsEmptyAfterDoubleDash() {
	_, err := parseArgs([]string{"loop-syscallwrap", "--"})
	s.Require().Error(err)
}

// --- run() dispatcher ---

// TestRunDispatchesToChildWhenModeChild: envMode=child triggers runChild, which
// fails at parentConn (injected to error) — proving we took the child branch.
// Parent branch would error earlier on missing envPolicyFile.
func (s *AppSuite) TestRunDispatchesToChildWhenModeChild() {
	a := &app{
		getenv: func(k string) string {
			if k == envMode {
				return modeChild
			}
			return ""
		},
		args:       []string{"x", "--", "claude"},
		lookPath:   func(name string) (string, error) { return "/bin/" + name, nil },
		parentConn: func() (*net.UnixConn, error) { return nil, errors.New("boom") },
	}
	err := a.run()
	s.Require().Error(err)
	s.Require().Contains(err.Error(), "open parent fd")
}

// TestRunDispatchesToParentByDefault: envMode unset triggers runParent, which
// errors on missing envPolicyFile — proving we took the parent branch.
func (s *AppSuite) TestRunDispatchesToParentByDefault() {
	a := &app{
		getenv: func(string) string { return "" },
		args:   []string{"x", "--", "claude"},
	}
	err := a.run()
	s.Require().Error(err)
	s.Require().Contains(err.Error(), envPolicyFile)
}

// --- runMain ---

func (s *AppSuite) TestRunMainHappyReturnsZero() {
	a := &app{
		getenv: func(k string) string {
			if k == envMode {
				return modeChild
			}
			return ""
		},
		args:         []string{"x", "--", "claude"},
		environ:      func() []string { return nil },
		lockOSThread: func() {},
		setPdeathsig: func(syscall.Signal) error { return nil },
		parentConn: func() (*net.UnixConn, error) {
			c, _ := socketPair(s.T())
			return c, nil
		},
		install:  func() (int, error) { return memfdForTest(s.T()), nil },
		send:     func(*net.UnixConn, string, int) error { return nil },
		readAck:  func(*net.UnixConn) error { return nil },
		closeFD:  func(int) error { return nil },
		lookPath: func(name string) (string, error) { return "/" + name, nil },
		exec:     func(string, []string, []string) error { return nil },
	}
	var buf bytes.Buffer
	s.Require().Equal(0, runMain(&buf, a))
	s.Require().Empty(buf.String())
}

func (s *AppSuite) TestRunMainErrorReturnsOneAndWrites() {
	a := &app{
		getenv: func(string) string { return "" },
		args:   []string{"x", "--", "claude"},
	}
	var buf bytes.Buffer
	s.Require().Equal(1, runMain(&buf, a))
	s.Require().Contains(buf.String(), "loop-syscallwrap:")
	s.Require().Contains(buf.String(), envPolicyFile)
}

// --- newApp wires production defaults ---

func (s *AppSuite) TestNewAppWiresDefaults() {
	a := newApp()
	// Shared
	s.Require().NotNil(a.getenv)
	s.Require().NotNil(a.environ)
	// Child mode
	s.Require().NotNil(a.lockOSThread)
	s.Require().NotNil(a.setPdeathsig)
	s.Require().NotNil(a.parentConn)
	s.Require().NotNil(a.install)
	s.Require().NotNil(a.send)
	s.Require().NotNil(a.readAck)
	s.Require().NotNil(a.closeFD)
	s.Require().NotNil(a.lookPath)
	s.Require().NotNil(a.exec)
	// Parent mode
	s.Require().NotNil(a.readFile)
	s.Require().NotNil(a.lookupUser)
	s.Require().NotNil(a.socketpair)
	s.Require().NotNil(a.startChild)
	s.Require().NotNil(a.newApprover)
	s.Require().NotNil(a.newGateServer)
	s.Require().NotNil(a.receiveHS)
	s.Require().NotNil(a.sendAck)
	s.Require().NotNil(a.waitChild)
	s.Require().NotNil(a.notifyContext)
	s.Require().NotNil(a.selfExe)
	s.Require().NotNil(a.exitCode)
	s.Require().NotNil(a.stderr)
}

func (s *AppSuite) TestNewAppGetenvProxiesOS() {
	s.T().Setenv("LOOP_SYSCALLWRAP_TESTVAR", "hi")
	a := newApp()
	s.Require().Equal("hi", a.getenv("LOOP_SYSCALLWRAP_TESTVAR"))
}

func (s *AppSuite) TestNewAppEnvironNonEmpty() {
	a := newApp()
	s.Require().NotEmpty(a.environ())
}

func (s *AppSuite) TestNewAppLockOSThreadUsable() {
	a := newApp()
	// Run on a throwaway goroutine so the lock doesn't bleed into other tests.
	done := make(chan struct{})
	go func() {
		defer close(done)
		a.lockOSThread()
		runtime.UnlockOSThread()
	}()
	<-done
}

func (s *AppSuite) TestNewAppCloseFDRejectsInvalid() {
	a := newApp()
	err := a.closeFD(-1)
	s.Require().Error(err)
	s.Require().ErrorIs(err, unix.EBADF)
}

func (s *AppSuite) TestNewAppSelfExeReturnsProcSelfExe() {
	a := newApp()
	s.Require().Equal("/proc/self/exe", a.selfExe())
}

// --- Run() exported wrapper ---

// TestRunExportedErrorPath exercises Run() — the exported entry point used by
// the cmd/loop syscallwrap subcommand. With no env set the parent branch
// runs and fails on missing LOOP_GATE_POLICY_FILE, returning exit code 1 and
// writing the diagnostic to the provided stderr.
//
// Hermeticity: Run() wires getenv=os.Getenv, so ambient env leaks in. When
// this test is itself invoked from within a syscallwrap child (e.g. an
// in-container `make coverage-check` self-test), LOOP_SYSCALLWRAP_MODE=child
// is already set and run() would dispatch to the child branch. t.Setenv
// forces the parent branch for the duration of the test.
func TestRunExportedErrorPath(t *testing.T) {
	t.Setenv(envMode, "")
	t.Setenv(envPolicyFile, "")

	var buf bytes.Buffer
	// forwardArgs contains the target command after the optional "--".
	code := Run(&buf, []string{"--", "claude"}, []string{"loop", "syscallwrap", "--", "claude"})
	require.Equal(t, 1, code)
	require.Contains(t, buf.String(), "loop-syscallwrap:")
	require.Contains(t, buf.String(), envPolicyFile)
}
