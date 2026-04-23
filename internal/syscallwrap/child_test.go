//go:build linux

package syscallwrap

import (
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
	"golang.org/x/sys/unix"

	"github.com/radutopala/loop/internal/agentgate"
)

type ChildSuite struct {
	suite.Suite
}

func TestChildSuite(t *testing.T) {
	suite.Run(t, new(ChildSuite))
}

// --- sendHandshake ---

func (s *ChildSuite) TestSendHandshakeWireFormat() {
	client, server := socketPair(s.T())
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()

	fd := memfdForTest(s.T())
	defer func() { _ = unix.Close(fd) }()

	s.Require().NoError(sendHandshake(client, "chan-42", fd))

	hdr := make([]byte, 4)
	oob := make([]byte, unix.CmsgSpace(4))
	s.Require().NoError(server.SetReadDeadline(time.Now().Add(2 * time.Second)))
	n, oobn, _, _, err := server.ReadMsgUnix(hdr, oob)
	s.Require().NoError(err)
	s.Require().Equal(4, n)
	s.Require().Equal(uint32(len("chan-42")), binary.BigEndian.Uint32(hdr))

	body := make([]byte, len("chan-42"))
	_, err = io.ReadFull(server, body)
	s.Require().NoError(err)
	s.Require().Equal("chan-42", string(body))

	msgs, err := unix.ParseSocketControlMessage(oob[:oobn])
	s.Require().NoError(err)
	s.Require().Len(msgs, 1)
	fds, err := unix.ParseUnixRights(&msgs[0])
	s.Require().NoError(err)
	s.Require().Len(fds, 1)
	s.Require().GreaterOrEqual(fds[0], 0)
	_ = unix.Close(fds[0])
}

func (s *ChildSuite) TestSendHandshakeEmptyChannelID() {
	client, server := socketPair(s.T())
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()

	fd := memfdForTest(s.T())
	defer func() { _ = unix.Close(fd) }()

	s.Require().NoError(sendHandshake(client, "", fd))
	hdr := make([]byte, 4)
	oob := make([]byte, unix.CmsgSpace(4))
	s.Require().NoError(server.SetReadDeadline(time.Now().Add(2 * time.Second)))
	_, _, _, _, err := server.ReadMsgUnix(hdr, oob)
	s.Require().NoError(err)
	s.Require().Equal(uint32(0), binary.BigEndian.Uint32(hdr))
}

func (s *ChildSuite) TestSendHandshakeErrorPropagates() {
	client, server := socketPair(s.T())
	s.Require().NoError(server.Close())

	fd := memfdForTest(s.T())
	defer func() { _ = unix.Close(fd) }()

	err := sendHandshake(client, "x", fd)
	_ = client.Close()
	s.Require().Error(err)
}

// --- readAck ---

func (s *ChildSuite) TestReadAckAccepts() {
	client, server := socketPair(s.T())
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()

	_, err := server.Write([]byte{agentgate.AckByte})
	s.Require().NoError(err)
	s.Require().NoError(client.SetReadDeadline(time.Now().Add(2 * time.Second)))
	s.Require().NoError(readAck(client))
}

func (s *ChildSuite) TestReadAckRejectsOtherByte() {
	client, server := socketPair(s.T())
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()

	_, err := server.Write([]byte{0x02})
	s.Require().NoError(err)
	s.Require().NoError(client.SetReadDeadline(time.Now().Add(2 * time.Second)))

	err = readAck(client)
	s.Require().Error(err)
	s.Require().Contains(err.Error(), "unexpected ack")
}

func (s *ChildSuite) TestReadAckReadErrorPropagates() {
	client, server := socketPair(s.T())
	defer func() { _ = client.Close() }()
	s.Require().NoError(server.Close())

	s.Require().NoError(client.SetReadDeadline(time.Now().Add(2 * time.Second)))
	err := readAck(client)
	s.Require().Error(err)
}

// --- defaultSetPdeathsig ---

// Calling prctl(PR_SET_PDEATHSIG, 0, ...) un-sets the parent-death signal —
// we set SIGKILL, then reset to 0 so a future test parent death doesn't kill
// the test binary.
func (s *ChildSuite) TestDefaultSetPdeathsigSetAndReset() {
	s.Require().NoError(defaultSetPdeathsig(syscall.SIGKILL))
	s.Require().NoError(defaultSetPdeathsig(0))
}

// --- runChild orchestration ---

// fakeChildDeps builds an *app wired to in-memory fakes for the child path.
// Each runChild step is triggered by setting the relevant *Err field.
type fakeChildDeps struct {
	t             *testing.T
	env           map[string]string
	args          []string
	environResult []string

	locked         bool
	pdeathsigCalls []syscall.Signal
	pdeathsigErr   error

	parentConnCalled int
	parentConnErr    error

	installCalled int
	installedFD   int
	installErr    error

	sendCalled  int
	sendErr     error
	sentChannel string
	sentFD      int

	readAckErr    error
	readAckCalled int

	closeErr error
	closed   []int

	lookPathCalls []string
	lookPathMap   map[string]string
	lookPathErr   error

	execErr   error
	execArgv0 string
	execArgv  []string
	execEnv   []string

	// The conn we return from parentConn, kept alive so deferred Close() in
	// runChild has something real to close without nil-panicking.
	conn *net.UnixConn
	peer *net.UnixConn

	// call-order recorder: appended by each step so tests can assert the
	// lockOSThread → setPdeathsig → install sequence.
	order []string
}

func newFakeChildDeps(t *testing.T) *fakeChildDeps {
	t.Helper()
	return &fakeChildDeps{t: t, env: map[string]string{}}
}

func (f *fakeChildDeps) wire() *app {
	return &app{
		getenv:  func(k string) string { return f.env[k] },
		args:    f.args,
		environ: func() []string { return f.environResult },
		lockOSThread: func() {
			f.locked = true
			f.order = append(f.order, "lock")
		},
		setPdeathsig: func(sig syscall.Signal) error {
			f.pdeathsigCalls = append(f.pdeathsigCalls, sig)
			f.order = append(f.order, "pdeathsig")
			return f.pdeathsigErr
		},
		parentConn: func() (*net.UnixConn, error) {
			f.parentConnCalled++
			if f.parentConnErr != nil {
				return nil, f.parentConnErr
			}
			f.conn, f.peer = socketPair(f.t)
			return f.conn, nil
		},
		install: func() (int, error) {
			f.installCalled++
			f.order = append(f.order, "install")
			if f.installErr != nil {
				return -1, f.installErr
			}
			return f.installedFD, nil
		},
		send: func(_ *net.UnixConn, channelID string, fd int) error {
			f.sendCalled++
			f.sentChannel = channelID
			f.sentFD = fd
			return f.sendErr
		},
		readAck: func(*net.UnixConn) error {
			f.readAckCalled++
			return f.readAckErr
		},
		closeFD: func(fd int) error {
			f.closed = append(f.closed, fd)
			return f.closeErr
		},
		lookPath: func(name string) (string, error) {
			f.lookPathCalls = append(f.lookPathCalls, name)
			if f.lookPathErr != nil {
				return "", f.lookPathErr
			}
			if resolved, ok := f.lookPathMap[name]; ok {
				return resolved, nil
			}
			return name, nil
		},
		exec: func(argv0 string, argv, env []string) error {
			f.execArgv0 = argv0
			f.execArgv = argv
			f.execEnv = env
			return f.execErr
		},
	}
}

func (f *fakeChildDeps) cleanup() {
	if f.peer != nil {
		_ = f.peer.Close()
	}
}

func (s *ChildSuite) TestRunChildHappyPath() {
	f := newFakeChildDeps(s.T())
	defer f.cleanup()
	f.env[envChannelID] = "ch1"
	f.args = []string{"loop-syscallwrap", "--", "claude", "-p"}
	f.environResult = []string{"HOME=/home/agent"}
	f.installedFD = 9
	// Bare command name → PATH resolver fires and rewrites the exec path
	// (but not argv) to the absolute binary location.
	f.lookPathMap = map[string]string{"claude": "/usr/local/bin/claude"}

	s.Require().NoError(f.wire().runChild())
	s.Require().True(f.locked)
	s.Require().Equal([]syscall.Signal{syscall.SIGKILL}, f.pdeathsigCalls)
	s.Require().Equal(1, f.parentConnCalled)
	s.Require().Equal("ch1", f.sentChannel)
	s.Require().Equal(9, f.sentFD)
	s.Require().Equal([]int{9}, f.closed)
	s.Require().Equal(1, f.readAckCalled)
	s.Require().Equal([]string{"claude"}, f.lookPathCalls)
	s.Require().Equal("/usr/local/bin/claude", f.execArgv0)
	s.Require().Equal([]string{"claude", "-p"}, f.execArgv)
	s.Require().Equal([]string{"HOME=/home/agent"}, f.execEnv)
	// Ordering invariant: lock → pdeathsig → install.
	s.Require().Equal([]string{"lock", "pdeathsig", "install"}, f.order)
}

func (s *ChildSuite) TestRunChildAbsoluteTargetSkipsLookPath() {
	f := newFakeChildDeps(s.T())
	defer f.cleanup()
	f.args = []string{"loop-syscallwrap", "--", "/bin/sh", "-c", "echo hi"}
	f.environResult = []string{}
	f.installedFD = 4

	s.Require().NoError(f.wire().runChild())
	s.Require().Empty(f.lookPathCalls, "paths with a slash must not consult PATH")
	s.Require().Equal("/bin/sh", f.execArgv0)
	s.Require().Equal([]string{"/bin/sh", "-c", "echo hi"}, f.execArgv)
}

func (s *ChildSuite) TestRunChildLookPathErrorShortCircuits() {
	f := newFakeChildDeps(s.T())
	defer f.cleanup()
	f.args = []string{"loop-syscallwrap", "--", "not-a-real-cmd"}
	f.lookPathErr = errors.New("executable file not found in $PATH")

	err := f.wire().runChild()
	s.Require().Error(err)
	s.Require().Contains(err.Error(), "lookup target")
	s.Require().Contains(err.Error(), "not-a-real-cmd")
	s.Require().False(f.locked, "failure before lockOSThread — lock must not run")
	s.Require().Equal(0, f.parentConnCalled)
}

func (s *ChildSuite) TestRunChildParseArgsError() {
	f := newFakeChildDeps(s.T())
	defer f.cleanup()
	f.args = []string{"x"}
	err := f.wire().runChild()
	s.Require().Error(err)
	s.Require().Contains(err.Error(), "usage:")
	s.Require().Equal(0, f.parentConnCalled)
}

func (s *ChildSuite) TestRunChildParentConnError() {
	f := newFakeChildDeps(s.T())
	defer f.cleanup()
	f.args = []string{"x", "--", "c"}
	f.parentConnErr = errors.New("no fd")

	err := f.wire().runChild()
	s.Require().Error(err)
	s.Require().Contains(err.Error(), "open parent fd")
	s.Require().False(f.locked) // lock happens after parentConn
}

func (s *ChildSuite) TestRunChildPdeathsigError() {
	f := newFakeChildDeps(s.T())
	defer f.cleanup()
	f.args = []string{"x", "--", "c"}
	f.pdeathsigErr = errors.New("prctl eperm")

	err := f.wire().runChild()
	s.Require().Error(err)
	s.Require().Contains(err.Error(), "set pdeathsig")
	s.Require().True(f.locked) // lock happens before pdeathsig
	s.Require().Equal(0, f.installCalled)
}

func (s *ChildSuite) TestRunChildInstallError() {
	f := newFakeChildDeps(s.T())
	defer f.cleanup()
	f.args = []string{"x", "--", "c"}
	f.installErr = errors.New("einval")

	err := f.wire().runChild()
	s.Require().Error(err)
	s.Require().Contains(err.Error(), "install filter")
	s.Require().True(f.locked)
	s.Require().Len(f.pdeathsigCalls, 1) // pdeathsig happens before install
}

func (s *ChildSuite) TestRunChildSendErrorClosesFD() {
	f := newFakeChildDeps(s.T())
	defer f.cleanup()
	f.args = []string{"x", "--", "c"}
	f.installedFD = 11
	f.sendErr = errors.New("broken pipe")

	err := f.wire().runChild()
	s.Require().Error(err)
	s.Require().Contains(err.Error(), "handshake send")
	s.Require().Equal([]int{11}, f.closed)
	s.Require().Equal(0, f.readAckCalled)
}

func (s *ChildSuite) TestRunChildCloseFDErrorSurfaces() {
	f := newFakeChildDeps(s.T())
	defer f.cleanup()
	f.args = []string{"x", "--", "c"}
	f.installedFD = 5
	f.closeErr = errors.New("ebadf")

	err := f.wire().runChild()
	s.Require().Error(err)
	s.Require().Contains(err.Error(), "close local notify fd")
}

func (s *ChildSuite) TestRunChildAckError() {
	f := newFakeChildDeps(s.T())
	defer f.cleanup()
	f.args = []string{"x", "--", "c"}
	f.installedFD = 7
	f.readAckErr = errors.New("eof")

	err := f.wire().runChild()
	s.Require().Error(err)
	s.Require().Contains(err.Error(), "handshake ack")
}

func (s *ChildSuite) TestRunChildExecError() {
	f := newFakeChildDeps(s.T())
	defer f.cleanup()
	f.args = []string{"x", "--", "c"}
	f.installedFD = 1
	f.execErr = errors.New("no such file")

	err := f.wire().runChild()
	s.Require().Error(err)
	s.Require().Equal([]string{"c"}, f.execArgv)
}

// --- newApp child-path wiring ---

func (s *ChildSuite) TestNewAppSendWiringWritesOverPair() {
	client, server := socketPair(s.T())
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()

	fd := memfdForTest(s.T())
	defer func() { _ = unix.Close(fd) }()

	a := newApp()
	s.Require().NoError(a.send(client, "ch", fd))

	hdr := make([]byte, 4)
	oob := make([]byte, unix.CmsgSpace(4))
	s.Require().NoError(server.SetReadDeadline(time.Now().Add(2 * time.Second)))
	_, oobn, _, _, err := server.ReadMsgUnix(hdr, oob)
	s.Require().NoError(err)
	msgs, err := unix.ParseSocketControlMessage(oob[:oobn])
	s.Require().NoError(err)
	fds, err := unix.ParseUnixRights(&msgs[0])
	s.Require().NoError(err)
	_ = unix.Close(fds[0])
}

func (s *ChildSuite) TestNewAppReadAckWiringReadsRealAck() {
	client, server := socketPair(s.T())
	defer func() { _ = client.Close() }()
	defer func() { _ = server.Close() }()

	_, err := server.Write([]byte{agentgate.AckByte})
	s.Require().NoError(err)
	s.Require().NoError(client.SetReadDeadline(time.Now().Add(2 * time.Second)))

	a := newApp()
	s.Require().NoError(a.readAck(client))
}

// TestNewAppExecWiringIsSyscallExec proves .exec is wired to syscall.Exec by
// calling it against a nonexistent binary — syscall.Exec returns ENOENT
// without replacing our process. Any other wiring would not return ENOENT.
func (s *ChildSuite) TestNewAppExecWiringIsSyscallExec() {
	a := newApp()
	err := a.exec("/nonexistent/loop-syscallwrap-wiring-probe", []string{"x"}, []string{})
	s.Require().Error(err)
	s.Require().True(errors.Is(err, syscall.ENOENT))
}

// TestNewAppSetPdeathsigWiringInvokesPrctl exercises the real syscall path
// end-to-end: SIGKILL succeeds, then reset to 0 so this test's parent (the
// go test binary) doesn't accidentally kill the test process if it dies.
func (s *ChildSuite) TestNewAppSetPdeathsigWiringInvokesPrctl() {
	a := newApp()
	s.Require().NoError(a.setPdeathsig(syscall.SIGKILL))
	s.Require().NoError(a.setPdeathsig(0))
}

// TestDefaultParentConnReturnsErrorWhenFDInvalid covers the not-a-unix-conn
// error path. We open an arbitrary file at fd=childHandshakeFD (3) using dup2
// and confirm defaultParentConn rejects it as not a *net.UnixConn (regular
// files don't become UnixConn via FileConn). This also indirectly covers the
// happy-path extraction since it proves os.NewFile works on fd 3.
func (s *ChildSuite) TestDefaultParentConnRejectsNonSocket() {
	// Open a regular file and dup it to fd 3.
	tmp, err := os.CreateTemp("", "loop-syscallwrap-fd3-*")
	s.Require().NoError(err)
	defer func() { _ = os.Remove(tmp.Name()) }()
	defer func() { _ = tmp.Close() }()

	// Save whatever's at fd 3 currently (may be invalid).
	savedFD3, savedErr := unix.Dup(childHandshakeFD)
	restore := func() {
		if savedErr == nil {
			_ = unix.Dup2(savedFD3, childHandshakeFD)
			_ = unix.Close(savedFD3)
		} else {
			// fd 3 was invalid → close it to leave state consistent.
			_ = unix.Close(childHandshakeFD)
		}
	}
	defer restore()

	s.Require().NoError(unix.Dup2(int(tmp.Fd()), childHandshakeFD))

	_, err = defaultParentConn()
	s.Require().Error(err)
}

// TestDefaultParentConnRejectsTCPFD covers the !ok type-assertion branch:
// net.FileConn on a TCP socket returns *net.TCPConn, not *net.UnixConn.
func (s *ChildSuite) TestDefaultParentConnRejectsTCPFD() {
	tcpFD, err := unix.Socket(unix.AF_INET, unix.SOCK_STREAM, 0)
	s.Require().NoError(err)

	savedFD3, savedErr := unix.Dup(childHandshakeFD)
	restore := func() {
		if savedErr == nil {
			_ = unix.Dup2(savedFD3, childHandshakeFD)
			_ = unix.Close(savedFD3)
		} else {
			_ = unix.Close(childHandshakeFD)
		}
	}
	defer restore()

	s.Require().NoError(unix.Dup2(tcpFD, childHandshakeFD))
	_ = unix.Close(tcpFD)

	_, err = defaultParentConn()
	s.Require().Error(err)
	s.Require().Contains(err.Error(), "not a unix conn")
}

// TestDefaultParentConnHappyOnSocketpair exercises the happy path by placing
// a real unix socketpair fd at fd 3.
//
// Skipped when running as a syscallwrap child: the outer gate parent owns
// fd 3 and has already registered it with Go's net poller for its own
// handshake socket. A dup2 over fd 3 here would disturb the live gate
// connection, and even when it succeeds the poller's cached state against
// the original fd makes net.FileConn return EINVAL. The path this test
// covers runs fine in the intended host/CI environment.
func (s *ChildSuite) TestDefaultParentConnHappyOnSocketpair() {
	if os.Getenv(envMode) == modeChild {
		s.T().Skip("fd 3 is owned by the outer syscallwrap parent")
	}

	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	s.Require().NoError(err)
	// fd[1] is our reference "other end"; leave it intact for cleanup.
	defer func() { _ = unix.Close(fds[1]) }()

	// Save whatever's at fd 3 currently.
	savedFD3, savedErr := unix.Dup(childHandshakeFD)
	restore := func() {
		if savedErr == nil {
			_ = unix.Dup2(savedFD3, childHandshakeFD)
			_ = unix.Close(savedFD3)
		} else {
			_ = unix.Close(childHandshakeFD)
		}
	}
	defer restore()

	// Dup fds[0] to fd 3 — note this closes the original fds[0].
	s.Require().NoError(unix.Dup2(fds[0], childHandshakeFD))
	_ = unix.Close(fds[0])

	conn, err := defaultParentConn()
	s.Require().NoError(err)
	s.Require().NotNil(conn)
	_ = conn.Close()
}
