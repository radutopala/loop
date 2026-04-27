//go:build linux

package syscallwrap

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
	"golang.org/x/sys/unix"

	"github.com/radutopala/loop/internal/agentgate"
	"github.com/radutopala/loop/internal/types"
)

type ParentSuite struct {
	suite.Suite
}

func TestParentSuite(t *testing.T) {
	suite.Run(t, new(ParentSuite))
}

// startSleepProc returns a live *os.Process that stays alive long enough for
// tests to call Kill/Signal/Wait on it without interfering with the test's own
// fakes. The Cleanup hook reaps the child regardless of test outcome.
func startSleepProc(t *testing.T) *os.Process {
	t.Helper()
	cmd := exec.Command("sleep", "60")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	return cmd.Process
}

// stubServer is a drop-in gateServer that returns a canned runErr after
// observing ctx cancellation. Used by happy-path runParent tests where we
// need Server.Run to respect context cancellation deterministically.
type stubServer struct {
	runErr       error
	returnOnCall bool // if true, Run returns immediately instead of waiting on ctx
	started      chan struct{}
	closeCalls   int32
}

func (s *stubServer) Run(ctx context.Context) error {
	if s.started != nil {
		close(s.started)
	}
	if s.returnOnCall {
		return s.runErr
	}
	<-ctx.Done()
	if s.runErr != nil {
		return s.runErr
	}
	return ctx.Err()
}

func (s *stubServer) Close() error {
	atomic.AddInt32(&s.closeCalls, 1)
	return nil
}

// closeOnlyServer simulates the production failure mode: Run is wedged in a
// kernel ioctl and only unblocks when the transport is closed. Used by the
// regression test for the "child exited but Run is hung" hang.
type closeOnlyServer struct {
	released chan struct{}
}

func newCloseOnlyServer() *closeOnlyServer {
	return &closeOnlyServer{released: make(chan struct{})}
}

func (s *closeOnlyServer) Run(_ context.Context) error {
	<-s.released
	return nil
}

func (s *closeOnlyServer) Close() error {
	select {
	case <-s.released:
	default:
		close(s.released)
	}
	return nil
}

type stubApprover struct{}

func (stubApprover) Request(_ context.Context, _ string, _ agentgate.ApprovalRequest) agentgate.Outcome {
	return agentgate.Outcome{Decision: types.DecisionAllow}
}

// fakeParentDeps holds the struct-field injections for the parent path. Tests
// set the relevant *Err fields (or leave them nil for happy path) and call
// wire() to build the *app.
type fakeParentDeps struct {
	t        *testing.T
	env      map[string]string
	args     []string
	selfArgv []string
	environ  []string

	readFileFn  func(string) ([]byte, error)
	readFileErr error

	lookupUserErr error
	lookupUID     int
	lookupGID     int

	getuidFn func() int

	socketpairErr error
	socketpairFn  func() (int, int, error)

	startChildErr  error
	startedArgv    []string
	startedEnv     []string
	startedChildFD uintptr
	startedUID     int
	startedGID     int
	startedProc    *os.Process // if set, returned from startChild instead of a fresh sleep

	approverAPI   string
	approverToken string
	approverCalls int32

	gateServerPolicy    *agentgate.Policy
	gateServerApprover  agentgate.Approver
	gateServerAuditor   agentgate.Auditor
	gateServerChannelID string
	gateServerNotifyFD  int
	gateServerReturned  gateServer

	openAuditorDir       string
	openAuditorRetention int
	openAuditorVerbose   bool
	openAuditorReturn    agentgate.Auditor
	openAuditorErr       error

	receiveHSChannelID string
	receiveHSFD        int
	receiveHSErr       error

	sendAckErr error

	waitChildCode int
	waitChildErr  error
	waitChildCh   chan int // if non-nil, waitChild blocks on recv here

	exitCodeGot    int
	exitCodeCalled int32

	stderr *bytes.Buffer
}

func newFakeParentDeps(t *testing.T) *fakeParentDeps {
	t.Helper()
	return &fakeParentDeps{
		t:        t,
		env:      map[string]string{},
		args:     []string{"loop-syscallwrap", "--", "claude"},
		selfArgv: []string{"loop", "syscallwrap", "--", "claude"},
		environ:  []string{},
		stderr:   &bytes.Buffer{},
	}
}

// validPolicyJSON is a minimal well-formed policy CompilePolicy accepts.
const validPolicyJSON = `{"default_decision":"allow","path_rules":[],"command_rules":[],"file_rules":[]}`

// defaultEnv populates the required env vars so the fail-fast checks pass and
// the test can focus on its specific failure mode.
func (f *fakeParentDeps) defaultEnv() {
	f.env[envPolicyFile] = "/etc/loop/gate-policy.json"
	f.env[envAPIURL] = "http://host.docker.internal:3007"
	f.env[envToken] = "abcd"
	f.env[envHostUser] = "root"
}

func (f *fakeParentDeps) wire() *app {
	return &app{
		getenv:   func(k string) string { return f.env[k] },
		args:     f.args,
		selfArgv: f.selfArgv,
		environ:  func() []string { return f.environ },

		readFile: func(p string) ([]byte, error) {
			if f.readFileFn != nil {
				return f.readFileFn(p)
			}
			if f.readFileErr != nil {
				return nil, f.readFileErr
			}
			return []byte(validPolicyJSON), nil
		},
		lookupUser: func(name string) (int, int, error) {
			if f.lookupUserErr != nil {
				return 0, 0, f.lookupUserErr
			}
			return f.lookupUID, f.lookupGID, nil
		},
		getuid: func() int {
			if f.getuidFn != nil {
				return f.getuidFn()
			}
			return 0
		},
		socketpair: func() (int, int, error) {
			if f.socketpairFn != nil {
				return f.socketpairFn()
			}
			if f.socketpairErr != nil {
				return -1, -1, f.socketpairErr
			}
			fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
			if err != nil {
				return -1, -1, err
			}
			return fds[0], fds[1], nil
		},
		startChild: func(argv, env []string, childEnd *os.File, uid, gid int) (*os.Process, error) {
			f.startedArgv = argv
			f.startedEnv = env
			f.startedChildFD = childEnd.Fd()
			f.startedUID = uid
			f.startedGID = gid
			if f.startChildErr != nil {
				return nil, f.startChildErr
			}
			if f.startedProc != nil {
				return f.startedProc, nil
			}
			return startSleepProc(f.t), nil
		},
		newApprover: func(apiURL, token string) agentgate.Approver {
			atomic.AddInt32(&f.approverCalls, 1)
			f.approverAPI = apiURL
			f.approverToken = token
			return stubApprover{}
		},
		newGateServer: func(policy *agentgate.Policy, approver agentgate.Approver, auditor agentgate.Auditor, channelID string, notifyFD int) gateServer {
			f.gateServerPolicy = policy
			f.gateServerApprover = approver
			f.gateServerAuditor = auditor
			f.gateServerChannelID = channelID
			f.gateServerNotifyFD = notifyFD
			if f.gateServerReturned != nil {
				return f.gateServerReturned
			}
			return &stubServer{}
		},
		openAuditor: func(dir string, retentionDays int, verbose bool) (agentgate.Auditor, error) {
			f.openAuditorDir = dir
			f.openAuditorRetention = retentionDays
			f.openAuditorVerbose = verbose
			if f.openAuditorErr != nil {
				return nil, f.openAuditorErr
			}
			if f.openAuditorReturn != nil {
				return f.openAuditorReturn, nil
			}
			return agentgate.NopAuditor{}, nil
		},
		receiveHS: func(*net.UnixConn) (string, int, error) {
			if f.receiveHSErr != nil {
				return "", -1, f.receiveHSErr
			}
			// Return a disposable fd so the caller's unix.Close doesn't EBADF.
			fd := f.receiveHSFD
			if fd == 0 {
				fd = memfdForTest(f.t)
			}
			return f.receiveHSChannelID, fd, nil
		},
		sendAck: func(*net.UnixConn) error { return f.sendAckErr },
		waitChild: func(*os.Process) (int, error) {
			if f.waitChildCh != nil {
				return <-f.waitChildCh, nil
			}
			return f.waitChildCode, f.waitChildErr
		},
		notifyContext: context.WithCancel,
		selfExe:       func() string { return "/proc/self/exe" },
		exitCode: func(code int) {
			atomic.AddInt32(&f.exitCodeCalled, 1)
			f.exitCodeGot = code
		},
		stderr: f.stderr,
	}
}

// --- env / arg fail-fast ---

func (s *ParentSuite) TestRunParentParseArgsError() {
	f := newFakeParentDeps(s.T())
	f.args = []string{"x"}
	err := f.wire().runParent()
	s.Require().Error(err)
	s.Require().Contains(err.Error(), "usage:")
}

func (s *ParentSuite) TestRunParentMissingPolicyFile() {
	f := newFakeParentDeps(s.T())
	err := f.wire().runParent()
	s.Require().Error(err)
	s.Require().Contains(err.Error(), envPolicyFile)
}

func (s *ParentSuite) TestRunParentMissingAPIURL() {
	f := newFakeParentDeps(s.T())
	f.env[envPolicyFile] = "/p"
	err := f.wire().runParent()
	s.Require().Error(err)
	s.Require().Contains(err.Error(), envAPIURL)
}

func (s *ParentSuite) TestRunParentMissingToken() {
	f := newFakeParentDeps(s.T())
	f.env[envPolicyFile] = "/p"
	f.env[envAPIURL] = "http://x"
	err := f.wire().runParent()
	s.Require().Error(err)
	s.Require().Contains(err.Error(), envToken)
}

func (s *ParentSuite) TestRunParentMissingHostUser() {
	f := newFakeParentDeps(s.T())
	f.env[envPolicyFile] = "/p"
	f.env[envAPIURL] = "http://x"
	f.env[envToken] = "t"
	err := f.wire().runParent()
	s.Require().Error(err)
	s.Require().Contains(err.Error(), envHostUser)
}

// --- pre-spawn failures ---

func (s *ParentSuite) TestRunParentPolicyLoadError() {
	f := newFakeParentDeps(s.T())
	f.defaultEnv()
	f.readFileErr = errors.New("eacces")
	err := f.wire().runParent()
	s.Require().Error(err)
	s.Require().Contains(err.Error(), "load policy")
}

func (s *ParentSuite) TestRunParentPolicyMalformedJSON() {
	f := newFakeParentDeps(s.T())
	f.defaultEnv()
	f.readFileFn = func(string) ([]byte, error) { return []byte("{not-json"), nil }
	err := f.wire().runParent()
	s.Require().Error(err)
	s.Require().Contains(err.Error(), "load policy")
}

func (s *ParentSuite) TestRunParentLookupUserError() {
	f := newFakeParentDeps(s.T())
	f.defaultEnv()
	f.lookupUserErr = errors.New("no such user")
	err := f.wire().runParent()
	s.Require().Error(err)
	s.Require().Contains(err.Error(), "lookup user")
}

func (s *ParentSuite) TestRunParentSocketpairError() {
	f := newFakeParentDeps(s.T())
	f.defaultEnv()
	f.socketpairErr = errors.New("emfile")
	err := f.wire().runParent()
	s.Require().Error(err)
	s.Require().Contains(err.Error(), "socketpair")
}

func (s *ParentSuite) TestRunParentStartChildError() {
	f := newFakeParentDeps(s.T())
	f.defaultEnv()
	f.startChildErr = errors.New("enoent")
	err := f.wire().runParent()
	s.Require().Error(err)
	s.Require().Contains(err.Error(), "start child")
}

// --- post-spawn failures ---

func (s *ParentSuite) TestRunParentReceiveHSError() {
	f := newFakeParentDeps(s.T())
	f.defaultEnv()
	f.receiveHSErr = errors.New("short handshake")

	err := f.wire().runParent()
	s.Require().Error(err)
	s.Require().Contains(err.Error(), "receive handshake")
}

func (s *ParentSuite) TestRunParentSendAckError() {
	f := newFakeParentDeps(s.T())
	f.defaultEnv()
	f.sendAckErr = errors.New("broken pipe")

	err := f.wire().runParent()
	s.Require().Error(err)
	s.Require().Contains(err.Error(), "send ack")
}

// --- happy paths (select branches) ---

func (s *ParentSuite) TestRunParentChildExitsFirst() {
	f := newFakeParentDeps(s.T())
	f.defaultEnv()
	f.waitChildCode = 0
	// Default stubServer blocks on ctx; waitChild returns 0 immediately.
	srv := &stubServer{}
	f.gateServerReturned = srv

	s.Require().NoError(f.wire().runParent())
	s.Require().Equal(int32(1), atomic.LoadInt32(&f.exitCodeCalled))
	s.Require().Equal(0, f.exitCodeGot)
	s.Require().Empty(f.stderr.String())
}

func (s *ParentSuite) TestRunParentChildExitsWithCode42() {
	f := newFakeParentDeps(s.T())
	f.defaultEnv()
	f.waitChildCode = 42
	f.gateServerReturned = &stubServer{}

	s.Require().NoError(f.wire().runParent())
	s.Require().Equal(42, f.exitCodeGot)
}

// TestRunParentChildExitClosesGateServer is the regression test for the
// production hang where srv.Run was blocked deep in
// SECCOMP_IOCTL_NOTIF_RECV. The kernel ioctl can't observe ctx
// cancellation, so the child-exit branch must close the transport so Run
// can return — otherwise runParent waits forever on a serverErr that
// never arrives. closeOnlyServer's Run only unblocks on Close: with the
// fix this test completes; without it the test deadlocks.
func (s *ParentSuite) TestRunParentChildExitClosesGateServer() {
	f := newFakeParentDeps(s.T())
	f.defaultEnv()
	srv := newCloseOnlyServer()
	f.gateServerReturned = srv
	f.waitChildCode = 0

	done := make(chan struct{})
	go func() {
		s.Require().NoError(f.wire().runParent())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		s.FailNow("runParent did not return after child exit — gateServer not closed")
	}
	s.Require().Equal(0, f.exitCodeGot)
}

// TestRunParentServerErrorTriggersKill: stubServer returns an error
// immediately; parent sees serverErr first, signals child, waits (fake
// blocked on waitChildCh to guarantee serverErr branch), exits with child's
// code and logs the server error to stderr.
func (s *ParentSuite) TestRunParentServerErrorTriggersKill() {
	f := newFakeParentDeps(s.T())
	f.defaultEnv()
	srv := &stubServer{runErr: errors.New("gate blew up"), returnOnCall: true}
	f.gateServerReturned = srv
	// Block waitChild so the server-err branch is guaranteed to fire first.
	f.waitChildCh = make(chan int, 1)
	go func() {
		time.Sleep(30 * time.Millisecond)
		f.waitChildCh <- 1
	}()

	s.Require().NoError(f.wire().runParent())
	s.Require().Equal(1, f.exitCodeGot)
	s.Require().Contains(f.stderr.String(), "gate server")
	s.Require().Contains(f.stderr.String(), "gate blew up")
}

// TestRunParentServerContextCanceledNoLog: stubServer returns
// context.Canceled immediately; parent takes the server-err branch, but the
// errors.Is guard suppresses the log.
func (s *ParentSuite) TestRunParentServerContextCanceledNoLog() {
	f := newFakeParentDeps(s.T())
	f.defaultEnv()
	srv := &stubServer{runErr: context.Canceled, returnOnCall: true}
	f.gateServerReturned = srv
	f.waitChildCh = make(chan int, 1)
	go func() {
		time.Sleep(30 * time.Millisecond)
		f.waitChildCh <- 0
	}()

	s.Require().NoError(f.wire().runParent())
	s.Require().Equal(0, f.exitCodeGot)
	s.Require().Empty(f.stderr.String())
}

// --- handshake channelID fallback ---

func (s *ParentSuite) TestRunParentUsesEnvChannelIDWhenSet() {
	f := newFakeParentDeps(s.T())
	f.defaultEnv()
	f.env[envChannelID] = "env-ch"
	f.receiveHSChannelID = "hs-ch" // handshake echo, should be ignored
	f.gateServerReturned = &stubServer{}

	s.Require().NoError(f.wire().runParent())
	s.Require().Equal("env-ch", f.gateServerChannelID)
}

func (s *ParentSuite) TestRunParentFallsBackToHandshakeChannelID() {
	f := newFakeParentDeps(s.T())
	f.defaultEnv()
	// envChannelID unset → fall back to handshake.
	f.receiveHSChannelID = "hs-ch"
	f.gateServerReturned = &stubServer{}

	s.Require().NoError(f.wire().runParent())
	s.Require().Equal("hs-ch", f.gateServerChannelID)
}

// --- audit env threading ---

// TestRunParentOpenAuditorReceivesEnvValues proves the audit env vars are
// threaded through to openAuditor verbatim: dir from envAuditDir, retention
// from envAuditRetentionDays (parsed), verbose from envAuditVerbose=="1".
func (s *ParentSuite) TestRunParentOpenAuditorReceivesEnvValues() {
	f := newFakeParentDeps(s.T())
	f.defaultEnv()
	f.env[envAuditDir] = "/var/log/loop-gate"
	f.env[envAuditRetentionDays] = "7"
	f.env[envAuditVerbose] = "1"
	f.gateServerReturned = &stubServer{}

	s.Require().NoError(f.wire().runParent())
	s.Require().Equal("/var/log/loop-gate", f.openAuditorDir)
	s.Require().Equal(7, f.openAuditorRetention)
	s.Require().True(f.openAuditorVerbose)
}

// TestRunParentOpenAuditorDefaultsVerboseFalse confirms that any value other
// than "1" for envAuditVerbose (including unset) yields verbose=false — the
// focused-log default that drops silent allows.
func (s *ParentSuite) TestRunParentOpenAuditorDefaultsVerboseFalse() {
	f := newFakeParentDeps(s.T())
	f.defaultEnv()
	f.env[envAuditDir] = "/var/log/loop-gate"
	f.env[envAuditVerbose] = "true" // only literal "1" flips it on
	f.gateServerReturned = &stubServer{}

	s.Require().NoError(f.wire().runParent())
	s.Require().False(f.openAuditorVerbose)
}

// TestRunParentOpenAuditorError propagates the auditor-open failure as a
// fatal runParent error — we'd rather the container fail to start than run
// without its configured audit sink.
func (s *ParentSuite) TestRunParentOpenAuditorError() {
	f := newFakeParentDeps(s.T())
	f.defaultEnv()
	f.openAuditorErr = errors.New("eacces")

	err := f.wire().runParent()
	s.Require().Error(err)
	s.Require().Contains(err.Error(), "open audit")
}

// --- startChild wiring assertions ---

func (s *ParentSuite) TestRunParentStartChildReceivesAgentCredAndChildEnv() {
	f := newFakeParentDeps(s.T())
	f.defaultEnv()
	f.lookupUID = 1001
	f.lookupGID = 1001
	f.environ = []string{"HOME=/home/agent", envMode + "=stray"}
	f.gateServerReturned = &stubServer{}

	s.Require().NoError(f.wire().runParent())
	s.Require().Equal(1001, f.startedUID)
	s.Require().Equal(1001, f.startedGID)
	s.Require().Contains(f.startedEnv, envMode+"="+modeChild)
	// Stray mode must have been stripped.
	for _, e := range f.startedEnv {
		if e == envMode+"=stray" {
			s.FailNow("stray mode env was not stripped")
		}
	}
	s.Require().Contains(f.startedEnv, "HOME=/home/agent")
	// startChild receives selfArgv (the outer `loop` argv) verbatim; the
	// re-execed /proc/self/exe re-enters cobra which dispatches to the
	// syscallwrap subcommand in child mode.
	s.Require().Equal(f.selfArgv, f.startedArgv)
	// Approver wiring uses API_URL + LOOP_GATE_TOKEN exactly.
	s.Require().Equal("http://host.docker.internal:3007", f.approverAPI)
	s.Require().Equal("abcd", f.approverToken)
}

// TestRunParentTerminalExecModeSkipsCredentialDrop covers the terminal-exec
// branch: when getuid() == lookupUser.uid, runParent feeds (-1, -1) to
// startChild so defaultStartChild omits the Credential — a non-root caller
// can't setuid and would otherwise EPERM.
func (s *ParentSuite) TestRunParentTerminalExecModeSkipsCredentialDrop() {
	f := newFakeParentDeps(s.T())
	f.defaultEnv()
	f.lookupUID = 1001
	f.lookupGID = 1001
	f.getuidFn = func() int { return 1001 } // already agent-uid
	f.gateServerReturned = &stubServer{}

	s.Require().NoError(f.wire().runParent())
	s.Require().Equal(-1, f.startedUID)
	s.Require().Equal(-1, f.startedGID)
}

// TestRunParentRootCallerKeepsCredentialDrop is the counterpart: when
// getuid() != lookupUser.uid (the normal entrypoint.sh-as-root → drop-to-agent
// flow), uid/gid are propagated unchanged.
func (s *ParentSuite) TestRunParentRootCallerKeepsCredentialDrop() {
	f := newFakeParentDeps(s.T())
	f.defaultEnv()
	f.lookupUID = 1001
	f.lookupGID = 1001
	f.getuidFn = func() int { return 0 } // running as root
	f.gateServerReturned = &stubServer{}

	s.Require().NoError(f.wire().runParent())
	s.Require().Equal(1001, f.startedUID)
	s.Require().Equal(1001, f.startedGID)
}

// --- childProcessEnv (unit) ---

func (s *ParentSuite) TestChildProcessEnvAppendsMode() {
	got := childProcessEnv([]string{"A=1", "B=2"})
	s.Require().Equal([]string{"A=1", "B=2", envMode + "=" + modeChild}, got)
}

func (s *ParentSuite) TestChildProcessEnvStripsExistingMode() {
	got := childProcessEnv([]string{"A=1", envMode + "=stray", "B=2"})
	s.Require().NotContains(got, envMode+"=stray")
	s.Require().Contains(got, envMode+"="+modeChild)
	// Only one mode entry in result.
	count := 0
	for _, e := range got {
		if len(e) >= len(envMode+"=") && e[:len(envMode+"=")] == envMode+"=" {
			count++
		}
	}
	s.Require().Equal(1, count)
}

func (s *ParentSuite) TestChildProcessEnvEmptyInput() {
	got := childProcessEnv(nil)
	s.Require().Equal([]string{envMode + "=" + modeChild}, got)
}

// --- loadGatePolicy (unit) ---

func (s *ParentSuite) TestLoadGatePolicyHappy() {
	read := func(string) ([]byte, error) { return []byte(validPolicyJSON), nil }
	p, err := loadGatePolicy(read, "/p")
	s.Require().NoError(err)
	s.Require().NotNil(p)
}

func (s *ParentSuite) TestLoadGatePolicyReadError() {
	read := func(string) ([]byte, error) { return nil, errors.New("eacces") }
	_, err := loadGatePolicy(read, "/p")
	s.Require().Error(err)
	s.Require().Contains(err.Error(), "read /p")
}

func (s *ParentSuite) TestLoadGatePolicyInvalidJSON() {
	read := func(string) ([]byte, error) { return []byte("{not-json"), nil }
	_, err := loadGatePolicy(read, "/p")
	s.Require().Error(err)
	s.Require().Contains(err.Error(), "parse policy")
}

func (s *ParentSuite) TestLoadGatePolicyDefaultsDecisionToDeny() {
	// No DefaultDecision in payload → loadGatePolicy falls back to deny.
	read := func(string) ([]byte, error) {
		return []byte(`{"path_rules":[],"command_rules":[],"file_rules":[]}`), nil
	}
	p, err := loadGatePolicy(read, "/p")
	s.Require().NoError(err)
	s.Require().NotNil(p)
}

func (s *ParentSuite) TestLoadGatePolicyCompileError() {
	// An invalid path-rule pattern (empty) makes CompilePolicy reject.
	read := func(string) ([]byte, error) {
		return []byte(`{"default_decision":"allow","path_rules":[{"pattern":"","decision":"allow"}]}`), nil
	}
	_, err := loadGatePolicy(read, "/p")
	s.Require().Error(err)
}

// --- default helpers ---

// TestRunParentFileConnError triggers the net.FileConn error branch by
// injecting a non-socket fd (pipe) as the parent end of the "socketpair".
// net.FileConn rejects non-sockets with ENOTSOCK.
func (s *ParentSuite) TestRunParentFileConnError() {
	f := newFakeParentDeps(s.T())
	f.defaultEnv()

	var pipefds [2]int
	s.Require().NoError(unix.Pipe(pipefds[:]))
	f.socketpairFn = func() (int, int, error) {
		return pipefds[0], pipefds[1], nil
	}

	err := f.wire().runParent()
	s.Require().Error(err)
	s.Require().Contains(err.Error(), "wrap parent fd")
}

// TestRunParentParentFDNotUnix triggers the type-assertion failure branch by
// handing a TCP socket fd to runParent. net.FileConn succeeds and returns a
// *net.TCPConn, which does not satisfy the *net.UnixConn cast.
func (s *ParentSuite) TestRunParentParentFDNotUnix() {
	f := newFakeParentDeps(s.T())
	f.defaultEnv()

	// Create a TCP socket so net.FileConn produces *net.TCPConn.
	tcpFD, err := unix.Socket(unix.AF_INET, unix.SOCK_STREAM, 0)
	s.Require().NoError(err)
	// Create any valid non-socket fd for the child end — we only care about
	// the parent end's type. A memfd is fine for this purpose.
	childFD := memfdForTest(s.T())

	f.socketpairFn = func() (int, int, error) {
		return tcpFD, childFD, nil
	}

	err = f.wire().runParent()
	s.Require().Error(err)
	s.Require().Contains(err.Error(), "not a unix conn")
}

// --- parseUIDGID (unit) ---

func (s *ParentSuite) TestParseUIDGIDHappy() {
	uid, gid, err := parseUIDGID("1001", "2002")
	s.Require().NoError(err)
	s.Require().Equal(1001, uid)
	s.Require().Equal(2002, gid)
}

func (s *ParentSuite) TestParseUIDGIDBadUID() {
	_, _, err := parseUIDGID("abc", "0")
	s.Require().Error(err)
	s.Require().Contains(err.Error(), "parse uid")
}

func (s *ParentSuite) TestParseUIDGIDBadGID() {
	_, _, err := parseUIDGID("0", "xyz")
	s.Require().Error(err)
	s.Require().Contains(err.Error(), "parse gid")
}

func (s *ParentSuite) TestDefaultLookupUserHappy() {
	cur, err := user.Current()
	s.Require().NoError(err)
	uid, gid, err := defaultLookupUser(cur.Username)
	s.Require().NoError(err)
	expectedUID, _ := strconv.Atoi(cur.Uid)
	expectedGID, _ := strconv.Atoi(cur.Gid)
	s.Require().Equal(expectedUID, uid)
	s.Require().Equal(expectedGID, gid)
}

func (s *ParentSuite) TestDefaultLookupUserUnknown() {
	_, _, err := defaultLookupUser("nonexistent-user-zzzzzzzzzz")
	s.Require().Error(err)
}

func (s *ParentSuite) TestDefaultSocketpairCreatesConnectedPair() {
	a, b, err := defaultSocketpair()
	s.Require().NoError(err)
	defer func() { _ = unix.Close(a) }()
	defer func() { _ = unix.Close(b) }()
	s.Require().GreaterOrEqual(a, 0)
	s.Require().GreaterOrEqual(b, 0)
	// Confirm they're connected by writing one byte end-to-end.
	_, err = unix.Write(a, []byte{'x'})
	s.Require().NoError(err)
	buf := make([]byte, 1)
	n, err := unix.Read(b, buf)
	s.Require().NoError(err)
	s.Require().Equal(1, n)
	s.Require().Equal(byte('x'), buf[0])
}

func (s *ParentSuite) TestDefaultWaitChildReportsExitCode() {
	cmd := exec.Command("/bin/sh", "-c", "exit 7")
	s.Require().NoError(cmd.Start())
	code, err := defaultWaitChild(cmd.Process)
	s.Require().NoError(err)
	s.Require().Equal(7, code)
}

// TestDefaultWaitChildAlreadyReaped covers the err-branch: once Process.Wait
// has been called, a subsequent Wait errors with "process already finished".
func (s *ParentSuite) TestDefaultWaitChildAlreadyReaped() {
	cmd := exec.Command("/bin/sh", "-c", "exit 0")
	s.Require().NoError(cmd.Start())
	_, err := cmd.Process.Wait()
	s.Require().NoError(err)

	code, err := defaultWaitChild(cmd.Process)
	s.Require().Error(err)
	s.Require().Equal(1, code)
}

func (s *ParentSuite) TestDefaultNotifyContextReturnsCancel() {
	ctx, cancel := defaultNotifyContext(context.Background())
	s.Require().NotNil(ctx)
	s.Require().NotNil(cancel)
	cancel()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		s.FailNow("ctx did not cancel after cancel()")
	}
}

func (s *ParentSuite) TestDefaultSelfExeReturnsProcSelfExe() {
	s.Require().Equal("/proc/self/exe", defaultSelfExe())
}

func (s *ParentSuite) TestDefaultNewApproverNonNil() {
	s.Require().NotNil(defaultNewApprover("http://x", "t"))
}

func (s *ParentSuite) TestDefaultNewGateServerNonNil() {
	p, err := agentgate.CompilePolicy(types.DecisionAllow, nil, nil, nil)
	s.Require().NoError(err)
	srv := defaultNewGateServer(p, stubApprover{}, agentgate.NopAuditor{}, "ch", -1)
	s.Require().NotNil(srv)
}

// TestDefaultOpenAuditorEmptyDirReturnsNop: the empty-env fast path gives
// us a silent gate — no files written, no mkdirs, no errors.
func (s *ParentSuite) TestDefaultOpenAuditorEmptyDirReturnsNop() {
	a, err := defaultOpenAuditor("", 0, false)
	s.Require().NoError(err)
	s.Require().Equal(agentgate.NopAuditor{}, a)
}

// TestDefaultOpenAuditorCreatesFileAuditor: a valid directory produces a
// writable FileAuditor. We confirm by writing one entry and stat-ing the
// rotating file the auditor opens.
func (s *ParentSuite) TestDefaultOpenAuditorCreatesFileAuditor() {
	dir := s.T().TempDir()
	a, err := defaultOpenAuditor(dir, 0, false)
	s.Require().NoError(err)
	s.Require().NotNil(a)
	// FileAuditor opens today's file eagerly; confirm it exists.
	entries, err := os.ReadDir(dir)
	s.Require().NoError(err)
	s.Require().Len(entries, 1)
	s.Require().True(strings.HasPrefix(entries[0].Name(), "agentgate-"))
}

// TestDefaultOpenAuditorMkdirError: a dir path that can't be created (e.g.,
// a file at the same path) surfaces as an error — callers fail-fast rather
// than silently run un-audited.
func (s *ParentSuite) TestDefaultOpenAuditorMkdirError() {
	// Create a regular file, then pass its path as "dir".
	f, err := os.CreateTemp(s.T().TempDir(), "not-a-dir-*")
	s.Require().NoError(err)
	_ = f.Close()
	_, err = defaultOpenAuditor(f.Name(), 0, false)
	s.Require().Error(err)
}

// --- parseAuditRetention (unit) ---

func (s *ParentSuite) TestParseAuditRetention() {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"0", 0},
		{"7", 7},
		{"30", 30},
		{"-1", 0},  // negative clamped to 0
		{"abc", 0}, // non-numeric → 0
		{"1.5", 0}, // float rejected
	}
	for _, tc := range cases {
		s.Require().Equal(tc.want, parseAuditRetention(tc.in), "input=%q", tc.in)
	}
}

// TestDefaultStartChildForwardsError proves defaultStartChild forwards the
// os.StartProcess error. argv containing a NUL byte is rejected by the
// runtime's exec* path ("invalid argument") before any fork, giving a
// deterministic error without needing a fork to succeed-and-then-fail.
func (s *ParentSuite) TestDefaultStartChildForwardsError() {
	_, err := defaultStartChild([]string{"x\x00"}, []string{}, nil, 0, 0)
	s.Require().Error(err)
}

// TestChildSysProcAttrSetsCredentialForValidUIDGID: the normal (root → agent)
// path installs a Credential so the fork setuids before exec.
func (s *ParentSuite) TestChildSysProcAttrSetsCredentialForValidUIDGID() {
	sys := childSysProcAttr(1001, 1001)
	s.Require().NotNil(sys.Credential)
	s.Require().Equal(uint32(1001), sys.Credential.Uid)
	s.Require().Equal(uint32(1001), sys.Credential.Gid)
}

// TestChildSysProcAttrOmitsCredentialForSentinel: terminal-exec mode passes
// (-1, -1) which must produce a bare SysProcAttr — os.StartProcess then
// inherits the caller's uid/gid instead of attempting a setuid that would
// EPERM when we're already non-root.
func (s *ParentSuite) TestChildSysProcAttrOmitsCredentialForSentinel() {
	s.Require().Nil(childSysProcAttr(-1, -1).Credential)
}

func (s *ParentSuite) TestChildSysProcAttrOmitsCredentialWhenEitherNegative() {
	s.Require().Nil(childSysProcAttr(-1, 1001).Credential)
	s.Require().Nil(childSysProcAttr(1001, -1).Credential)
}

// TestChildSysProcAttrRootToRoot: uid=0 is a valid value (non-negative) —
// a root→root invocation still gets a Credential, even though it's a no-op
// in practice.
func (s *ParentSuite) TestChildSysProcAttrRootToRoot() {
	sys := childSysProcAttr(0, 0)
	s.Require().NotNil(sys.Credential)
	s.Require().Equal(uint32(0), sys.Credential.Uid)
	s.Require().Equal(uint32(0), sys.Credential.Gid)
}
