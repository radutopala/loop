package agentgate

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/types"
)

type ServerSuite struct {
	suite.Suite
}

func TestServerSuite(t *testing.T) {
	suite.Run(t, new(ServerSuite))
}

// --- fake Transport ---

// scriptedTransport delivers a canned sequence of (trap, err) pairs from Recv
// and captures every Send into a slice. sendErr (when non-nil) is returned
// from the Nth Send where sendAt == N — leaving the default sendErr=nil for
// the happy-path dispatcher tests.
type scriptedTransport struct {
	mu       sync.Mutex
	recv     []recvEvent
	recvIdx  int
	sent     []TrapResponse
	sendErr  error
	sendAt   int
	closeErr error
	closed   int
}

type recvEvent struct {
	trap Trap
	err  error
}

func (t *scriptedTransport) Recv(_ context.Context) (Trap, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.recvIdx >= len(t.recv) {
		return Trap{}, io.EOF
	}
	ev := t.recv[t.recvIdx]
	t.recvIdx++
	return ev.trap, ev.err
}

func (t *scriptedTransport) Send(_ context.Context, resp TrapResponse) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sent = append(t.sent, resp)
	if t.sendErr != nil && len(t.sent) == t.sendAt {
		return t.sendErr
	}
	return nil
}

func (t *scriptedTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.closed++
	return t.closeErr
}

// --- helpers ---

func (s *ServerSuite) mustPolicy(def types.Decision, pathRules []types.PathRule, cmdRules []types.CommandRule, fileRules []types.FileRule) *Policy {
	p, err := CompilePolicy(def, pathRules, cmdRules, fileRules)
	require.NoError(s.T(), err)
	return p
}

// newServer builds a Server whose three handlers share one tracee.
// Tests pass nil for handlers they don't care about.
func (s *ServerSuite) newServer(tr *FakeTracee, execve *ExecveHandler, file *FileHandler, connect *ConnectHandler) *Server {
	return &Server{
		Transport: nil, // not used by Dispatch tests
		Factory:   func(_ int) Tracee { return tr },
		Execve:    execve,
		File:      file,
		Connect:   connect,
		ChannelID: "chan-A",
	}
}

// littleEndianU64 emits the raw bytes of v in LE order. Used to build open_how
// blobs that readOpenatFlags will decode.
func littleEndianU64(v uint64) []byte {
	out := make([]byte, 8)
	for i := range 8 {
		out[i] = byte(v >> (8 * i))
	}
	return out
}

// atFdcwd is AtFDCWD (-100) wrapped into uint64 with sign extension, matching
// the kernel's ABI for a negative int32 dirfd riding in a syscall arg slot.
// Wrapped in a function so the conversion happens at runtime — a bare
// `uint64(AtFDCWD)` at package scope is a constant expression the compiler
// rejects as overflow.
var atFdcwd = u64FromI32(AtFDCWD)

func u64FromI32(v int32) uint64 { return uint64(int64(v)) }

// --- reply helpers ---

func (s *ServerSuite) TestAllowRespSetsContinueSemantics() {
	got := allowResp(42)
	s.Require().Equal(uint64(42), got.ID)
	s.Require().True(got.Allow)
	s.Require().Equal(int32(0), got.ErrorNum)
}

func (s *ServerSuite) TestDenyRespCopiesErrno() {
	got := denyResp(7, syscall.EACCES)
	s.Require().Equal(uint64(7), got.ID)
	s.Require().False(got.Allow)
	s.Require().Equal(int32(syscall.EACCES), got.ErrorNum)
}

func (s *ServerSuite) TestDecisionRespMapsAllow() {
	got := decisionResp(9, types.DecisionAllow)
	s.Require().True(got.Allow)
	s.Require().Equal(uint64(9), got.ID)
}

func (s *ServerSuite) TestDecisionRespMapsDenyToEPERM() {
	got := decisionResp(9, types.DecisionDeny)
	s.Require().False(got.Allow)
	s.Require().Equal(int32(syscall.EPERM), got.ErrorNum)
}

// --- absolutize ---

func (s *ServerSuite) TestAbsolutizeLeavesAbsolutePathAlone() {
	tr := &FakeTracee{}
	got, err := absolutize("/work/x", 5, tr)
	s.Require().NoError(err)
	s.Require().Equal("/work/x", got)
}

func (s *ServerSuite) TestAbsolutizeRelativeJoinsDirfd() {
	tr := &FakeTracee{Dirfds: map[int32]string{7: "/work/sub"}}
	got, err := absolutize("file.txt", 7, tr)
	s.Require().NoError(err)
	s.Require().Equal("/work/sub/file.txt", got)
}

func (s *ServerSuite) TestAbsolutizeEmptyPathReturnsDirfd() {
	tr := &FakeTracee{Dirfds: map[int32]string{7: "/work"}}
	got, err := absolutize("", 7, tr)
	s.Require().NoError(err)
	s.Require().Equal("/work", got)
}

func (s *ServerSuite) TestAbsolutizeDirfdLookupFails() {
	tr := &FakeTracee{}
	_, err := absolutize("x", 99, tr)
	s.Require().ErrorIs(err, ErrTraceeGone)
}

// --- Dispatch: unknown syscall ---

func (s *ServerSuite) TestDispatchUnknownSyscallDeniesEPERM() {
	srv := s.newServer(&FakeTracee{}, nil, nil, nil)
	got := srv.Dispatch(context.Background(), Trap{ID: 1, PID: 100, Syscall: "ptrace"})
	s.Require().False(got.Allow)
	s.Require().Equal(int32(syscall.EPERM), got.ErrorNum)
}

// --- Dispatch: execve ---

func (s *ServerSuite) TestDispatchExecveNilHandlerDenies() {
	srv := s.newServer(&FakeTracee{}, nil, nil, nil)
	got := srv.Dispatch(context.Background(), Trap{ID: 1, Syscall: "execve"})
	s.Require().False(got.Allow)
}

func (s *ServerSuite) TestDispatchExecveHappyPath() {
	tr := &FakeTracee{
		Strings:      map[uintptr]string{0x100: "/bin/ls"},
		PointerLists: map[uintptr][]string{0x200: {"/bin/ls", "-al"}},
	}
	policy := s.mustPolicy(types.DecisionAllow, nil, nil, nil)
	srv := s.newServer(tr, NewExecveHandler(policy, nil), nil, nil)

	got := srv.Dispatch(context.Background(), Trap{
		ID: 10, PID: 42, Syscall: "execve",
		Args: [6]uint64{0x100, 0x200},
	})
	s.Require().True(got.Allow)
	s.Require().Equal(uint64(10), got.ID)
}

func (s *ServerSuite) TestDispatchExecveReadStringFails() {
	tr := &FakeTracee{} // no strings mapped
	srv := s.newServer(tr, NewExecveHandler(s.mustPolicy(types.DecisionAllow, nil, nil, nil), nil), nil, nil)
	got := srv.Dispatch(context.Background(), Trap{
		ID: 11, Syscall: "execve",
		Args: [6]uint64{0xBAD, 0x200},
	})
	s.Require().False(got.Allow)
	s.Require().Equal(int32(syscall.EPERM), got.ErrorNum)
}

func (s *ServerSuite) TestDispatchExecveArgvReadFails() {
	tr := &FakeTracee{Strings: map[uintptr]string{0x100: "/bin/ls"}}
	// Argv address 0x200 is not in PointerLists → ErrTraceeGone from ReadPointerArray.
	srv := s.newServer(tr, NewExecveHandler(s.mustPolicy(types.DecisionAllow, nil, nil, nil), nil), nil, nil)
	got := srv.Dispatch(context.Background(), Trap{
		ID: 12, Syscall: "execve",
		Args: [6]uint64{0x100, 0x200},
	})
	s.Require().False(got.Allow)
}

func (s *ServerSuite) TestDispatchExecveatResolvesAtEmptyPath() {
	tr := &FakeTracee{
		Strings:      map[uintptr]string{0x100: ""}, // empty filename
		PointerLists: map[uintptr][]string{0x200: {"memfd:payload"}},
		Dirfds:       map[int32]string{7: "memfd:payload"},
	}
	// ExecveHandler denies memfd-prefixed filenames; this exercises the
	// AT_EMPTY_PATH → ResolveDirfd rewrite path.
	srv := s.newServer(tr, NewExecveHandler(s.mustPolicy(types.DecisionAllow, nil, nil, nil), nil), nil, nil)
	got := srv.Dispatch(context.Background(), Trap{
		ID: 13, PID: 999, Syscall: "execveat",
		Args: [6]uint64{7, 0x100, 0x200, 0, uint64(AtEmptyPath)},
	})
	s.Require().False(got.Allow, "memfd target must be denied")
	s.Require().Equal(int32(syscall.EPERM), got.ErrorNum)
}

func (s *ServerSuite) TestDispatchExecveatResolveDirfdFails() {
	tr := &FakeTracee{
		Strings: map[uintptr]string{0x100: ""}, // empty filename
		// No Dirfds → ResolveDirfd returns ErrTraceeGone.
	}
	srv := s.newServer(tr, NewExecveHandler(s.mustPolicy(types.DecisionAllow, nil, nil, nil), nil), nil, nil)
	got := srv.Dispatch(context.Background(), Trap{
		ID: 14, Syscall: "execveat",
		Args: [6]uint64{7, 0x100, 0x200, 0, uint64(AtEmptyPath)},
	})
	s.Require().False(got.Allow)
	s.Require().Equal(int32(syscall.EPERM), got.ErrorNum)
}

func (s *ServerSuite) TestDispatchExecveatWithFilenameUsesArgsLayout() {
	// execveat(dirfd=5, filename="/bin/sh", argv, envp, flags=0) — filename is
	// not empty, so AT_EMPTY_PATH branch is skipped. Confirms arg indices 1
	// and 2 carry filename and argv for execveat.
	tr := &FakeTracee{
		Strings:      map[uintptr]string{0x100: "/bin/sh"},
		PointerLists: map[uintptr][]string{0x200: {"/bin/sh"}},
	}
	srv := s.newServer(tr, NewExecveHandler(s.mustPolicy(types.DecisionAllow, nil, nil, nil), nil), nil, nil)
	got := srv.Dispatch(context.Background(), Trap{
		ID: 15, Syscall: "execveat",
		Args: [6]uint64{5, 0x100, 0x200, 0, 0},
	})
	s.Require().True(got.Allow)
}

// --- Dispatch: connect ---

func (s *ServerSuite) TestDispatchConnectNilHandlerDenies() {
	srv := s.newServer(&FakeTracee{}, nil, nil, nil)
	got := srv.Dispatch(context.Background(), Trap{ID: 1, Syscall: "connect"})
	s.Require().False(got.Allow)
}

func (s *ServerSuite) TestDispatchConnectShortAddrLenAllowsThrough() {
	// addrLen < 2 — kernel would reject; we let it through unchanged so errno
	// is kernel-native (EINVAL), not our EPERM.
	srv := s.newServer(&FakeTracee{}, nil, nil, NewConnectHandler(s.mustPolicy(types.DecisionDeny, nil, nil, nil), nil))
	got := srv.Dispatch(context.Background(), Trap{
		ID: 20, Syscall: "connect",
		Args: [6]uint64{3, 0x100, 1},
	})
	s.Require().True(got.Allow)
}

func (s *ServerSuite) TestDispatchConnectReadBytesFails() {
	tr := &FakeTracee{} // no bytes mapped
	srv := s.newServer(tr, nil, nil, NewConnectHandler(s.mustPolicy(types.DecisionAllow, nil, nil, nil), nil))
	got := srv.Dispatch(context.Background(), Trap{
		ID: 21, Syscall: "connect",
		Args: [6]uint64{3, 0x100, 8},
	})
	s.Require().False(got.Allow)
	s.Require().Equal(int32(syscall.EPERM), got.ErrorNum)
}

func (s *ServerSuite) TestDispatchConnectNonUnixAllows() {
	// AF_INET (family=2, LE) — non-unix. v1 does not gate these.
	tr := &FakeTracee{Bytes: map[uintptr][]byte{0x100: {2, 0, 0, 0, 0, 0, 0, 0}}}
	srv := s.newServer(tr, nil, nil, NewConnectHandler(s.mustPolicy(types.DecisionDeny, nil, nil, nil), nil))
	got := srv.Dispatch(context.Background(), Trap{
		ID: 22, Syscall: "connect",
		Args: [6]uint64{3, 0x100, 8},
	})
	s.Require().True(got.Allow)
}

func (s *ServerSuite) TestDispatchConnectUnixMatchesPolicy() {
	// AF_UNIX (family=1, LE) + pathname "/var/run/docker.sock" + padding.
	addr := append([]byte{1, 0}, []byte("/var/run/docker.sock")...)
	addr = append(addr, make([]byte, 30)...)
	tr := &FakeTracee{Bytes: map[uintptr][]byte{0x100: addr}}
	policy := s.mustPolicy(
		types.DecisionAllow,
		[]types.PathRule{{Pattern: "/var/run/docker.sock", Decision: types.DecisionDeny, Message: "no docker"}},
		nil, nil,
	)
	srv := s.newServer(tr, nil, nil, NewConnectHandler(policy, nil))
	got := srv.Dispatch(context.Background(), Trap{
		ID: 23, Syscall: "connect",
		Args: [6]uint64{3, 0x100, uint64(len(addr))},
	})
	s.Require().False(got.Allow)
	s.Require().Equal(int32(syscall.EPERM), got.ErrorNum)
}

func (s *ServerSuite) TestDispatchConnectCapsAddrLen() {
	// Supply an addrLen far larger than SunPathMax+2. The dispatcher caps it
	// before ReadBytes; stored bytes need only the capped length to succeed.
	addr := append([]byte{1, 0}, []byte("/x.sock")...)
	addr = append(addr, make([]byte, SunPathMax)...) // fills to SunPathMax+2
	tr := &FakeTracee{Bytes: map[uintptr][]byte{0x100: addr}}
	srv := s.newServer(tr, nil, nil, NewConnectHandler(s.mustPolicy(types.DecisionAllow, nil, nil, nil), nil))
	got := srv.Dispatch(context.Background(), Trap{
		ID: 24, Syscall: "connect",
		Args: [6]uint64{3, 0x100, 99999}, // caller-supplied addrLen is absurdly large
	})
	s.Require().True(got.Allow)
}

// --- Dispatch: file ---

func (s *ServerSuite) TestDispatchFileNilHandlerDenies() {
	srv := s.newServer(&FakeTracee{}, nil, nil, nil)
	got := srv.Dispatch(context.Background(), Trap{ID: 1, Syscall: syscallOpenat})
	s.Require().False(got.Allow)
}

func (s *ServerSuite) TestDispatchFileOpenatReadAllowed() {
	tr := &FakeTracee{
		Strings: map[uintptr]string{0x100: "/work/x"},
	}
	policy := s.mustPolicy(types.DecisionAllow, nil, nil, nil)
	srv := s.newServer(tr, nil, NewFileHandler(policy, nil, 8), nil)
	got := srv.Dispatch(context.Background(), Trap{
		ID: 30, Syscall: syscallOpenat,
		Args: [6]uint64{atFdcwd, 0x100, 0, 0},
	})
	s.Require().True(got.Allow)
}

func (s *ServerSuite) TestDispatchFileOpenatReadStringFails() {
	tr := &FakeTracee{}
	srv := s.newServer(tr, nil, NewFileHandler(s.mustPolicy(types.DecisionAllow, nil, nil, nil), nil, 8), nil)
	got := srv.Dispatch(context.Background(), Trap{
		ID: 31, Syscall: syscallOpenat,
		Args: [6]uint64{atFdcwd, 0xBAD, 0, 0},
	})
	s.Require().False(got.Allow)
}

func (s *ServerSuite) TestDispatchFileOpenatRelativePathResolves() {
	tr := &FakeTracee{
		Strings: map[uintptr]string{0x100: "rel.go"},
		Dirfds:  map[int32]string{9: "/work"},
	}
	// Rule denies writes under /work/**; we request a write to "rel.go" with
	// dirfd=9 which resolves to /work/rel.go.
	policy := s.mustPolicy(types.DecisionAllow, nil, nil, []types.FileRule{
		{Paths: []string{"/work/**"}, Operations: []string{OpWrite}, Decision: types.DecisionDeny},
	})
	srv := s.newServer(tr, nil, NewFileHandler(policy, nil, 8), nil)
	got := srv.Dispatch(context.Background(), Trap{
		ID: 32, Syscall: syscallOpenat,
		Args: [6]uint64{9, 0x100, oWRONLY, 0},
	})
	s.Require().False(got.Allow)
}

func (s *ServerSuite) TestDispatchFileOpenatDirfdResolveFails() {
	tr := &FakeTracee{Strings: map[uintptr]string{0x100: "rel.go"}}
	// dirfd=9 not registered → absolutize fails → deny.
	srv := s.newServer(tr, nil, NewFileHandler(s.mustPolicy(types.DecisionAllow, nil, nil, nil), nil, 8), nil)
	got := srv.Dispatch(context.Background(), Trap{
		ID: 33, Syscall: syscallOpenat,
		Args: [6]uint64{9, 0x100, 0, 0},
	})
	s.Require().False(got.Allow)
}

func (s *ServerSuite) TestDispatchFileSymlinkResolveFallsBackToAbs() {
	// EvalSymlinks fails (dangling link, missing component) → dispatcher
	// evaluates policy against the cleaned abs path and, on allow, returns
	// allowResp so the kernel gets to run the syscall and surface its native
	// ENOENT. Covers the ld.so library-search case where /lib/libX.so.N
	// misses but /usr/lib/libX.so.N is the real target.
	sentinel := errors.New("eval boom")
	tr := &FakeTracee{
		Strings: map[uintptr]string{0x100: "/lib/libreadline.so.8"},
	}
	wrapped := &evalErrTracee{Tracee: tr, err: sentinel}

	// Deny a write on /etc/** so the allow-default doesn't mask a missed
	// evaluation: the fallback path is /lib/libreadline.so.8, which doesn't
	// match the deny rule, so we expect Allow.
	policy := s.mustPolicy(types.DecisionAllow, nil, nil, []types.FileRule{
		{Paths: []string{"/etc/**"}, Operations: []string{OpWrite}, Decision: types.DecisionDeny},
	})
	srv := &Server{
		Factory: func(_ int) Tracee { return wrapped },
		File:    NewFileHandler(policy, nil, 8),
	}
	got := srv.Dispatch(context.Background(), Trap{
		ID: 34, Syscall: syscallOpenat,
		Args: [6]uint64{atFdcwd, 0x100, 0, 0},
	})
	s.Require().True(got.Allow, "read probe on missing lib must allow through so kernel can return ENOENT")
}

func (s *ServerSuite) TestDispatchFileSymlinkResolveFallsBackAndPolicyStillDenies() {
	// Even when EvalSymlinks fails, the cleaned abs path is still subjected
	// to policy — a rule matching the pre-resolution path keeps denying.
	sentinel := errors.New("eval boom")
	tr := &FakeTracee{
		Strings: map[uintptr]string{0x100: "/etc/shadow"},
	}
	wrapped := &evalErrTracee{Tracee: tr, err: sentinel}

	policy := s.mustPolicy(types.DecisionAllow, nil, nil, []types.FileRule{
		{Paths: []string{"/etc/shadow"}, Operations: []string{OpRead}, Decision: types.DecisionDeny},
	})
	srv := &Server{
		Factory: func(_ int) Tracee { return wrapped },
		File:    NewFileHandler(policy, nil, 8),
	}
	got := srv.Dispatch(context.Background(), Trap{
		ID: 34, Syscall: syscallOpenat,
		Args: [6]uint64{atFdcwd, 0x100, 0, 0},
	})
	s.Require().False(got.Allow)
}

func (s *ServerSuite) TestDispatchFileOpenat2ReadsFlagsFromOpenHow() {
	// openat2 places flags at *a[2] offset 0 as LE u64. We stash the open_how
	// prefix at 0x400 and the path at 0x100.
	tr := &FakeTracee{
		Strings: map[uintptr]string{0x100: "/work/x"},
		Bytes:   map[uintptr][]byte{0x400: littleEndianU64(oWRONLY)},
	}
	policy := s.mustPolicy(types.DecisionAllow, nil, nil, []types.FileRule{
		{Paths: []string{"/work/**"}, Operations: []string{OpWrite}, Decision: types.DecisionDeny},
	})
	srv := s.newServer(tr, nil, NewFileHandler(policy, nil, 8), nil)
	got := srv.Dispatch(context.Background(), Trap{
		ID: 35, Syscall: syscallOpenat2,
		Args: [6]uint64{atFdcwd, 0x100, 0x400, 24},
	})
	s.Require().False(got.Allow, "WRONLY open_how should trip the deny rule")
}

func (s *ServerSuite) TestDispatchFileOpenat2ReadBytesFails() {
	tr := &FakeTracee{
		Strings: map[uintptr]string{0x100: "/work/x"},
		// No bytes at 0x400 → ReadBytes returns ErrTraceeGone.
	}
	srv := s.newServer(tr, nil, NewFileHandler(s.mustPolicy(types.DecisionAllow, nil, nil, nil), nil, 8), nil)
	got := srv.Dispatch(context.Background(), Trap{
		ID: 36, Syscall: syscallOpenat2,
		Args: [6]uint64{atFdcwd, 0x100, 0x400, 24},
	})
	s.Require().False(got.Allow)
}

func (s *ServerSuite) TestDispatchFileUnknownSyscallDenies() {
	// Dispatch only routes names from syscallTable here; called directly
	// with anything else, dispatchFile works off a zero spec and fails
	// closed on the unreadable path.
	srv := s.newServer(&FakeTracee{}, nil, NewFileHandler(s.mustPolicy(types.DecisionAllow, nil, nil, nil), nil, 8), nil)
	got := srv.dispatchFile(context.Background(), Trap{ID: 37, Syscall: "not-a-real-syscall"}, &FakeTracee{})
	s.Require().False(got.Allow)
}

func (s *ServerSuite) TestDispatchFileRenameat2EvaluatesSecondaryPath() {
	tr := &FakeTracee{
		Strings: map[uintptr]string{
			0x100: "/work/old.go",
			0x300: "/etc/shadow", // secondary path hits a deny rule
		},
	}
	policy := s.mustPolicy(types.DecisionAllow, nil, nil, []types.FileRule{
		{Paths: []string{"/etc/**"}, Operations: []string{OpCreate}, Decision: types.DecisionDeny},
	})
	srv := s.newServer(tr, nil, NewFileHandler(policy, nil, 8), nil)
	got := srv.Dispatch(context.Background(), Trap{
		ID: 40, Syscall: syscallRenameat2,
		// renameat2(olddirfd, oldpath, newdirfd, newpath, flags)
		Args: [6]uint64{atFdcwd, 0x100, atFdcwd, 0x300, 0},
	})
	s.Require().False(got.Allow, "secondary create on /etc/** must deny")
}

func (s *ServerSuite) TestDispatchFileRenameat2PrimaryDeniesWithoutSecondary() {
	tr := &FakeTracee{
		Strings: map[uintptr]string{
			0x100: "/etc/hosts", // primary delete hits deny first
			0x300: "/work/new",
		},
	}
	policy := s.mustPolicy(types.DecisionAllow, nil, nil, []types.FileRule{
		{Paths: []string{"/etc/**"}, Operations: []string{OpDelete}, Decision: types.DecisionDeny},
	})
	srv := s.newServer(tr, nil, NewFileHandler(policy, nil, 8), nil)
	got := srv.Dispatch(context.Background(), Trap{
		ID: 41, Syscall: syscallRenameat2,
		Args: [6]uint64{atFdcwd, 0x100, atFdcwd, 0x300, 0},
	})
	s.Require().False(got.Allow)
}

func (s *ServerSuite) TestDispatchFileRenameat2SecondaryReadFails() {
	tr := &FakeTracee{
		Strings: map[uintptr]string{0x100: "/work/old"},
		// Secondary path at 0x300 missing → ReadString fails → deny.
	}
	srv := s.newServer(tr, nil, NewFileHandler(s.mustPolicy(types.DecisionAllow, nil, nil, nil), nil, 8), nil)
	got := srv.Dispatch(context.Background(), Trap{
		ID: 42, Syscall: syscallRenameat2,
		Args: [6]uint64{atFdcwd, 0x100, atFdcwd, 0x300, 0},
	})
	s.Require().False(got.Allow)
}

func (s *ServerSuite) TestDispatchFileRenameat2SecondaryAbsolutizeFails() {
	// Primary resolves fine; secondary is relative with an unmapped dirfd.
	tr := &FakeTracee{
		Strings: map[uintptr]string{
			0x100: "/work/old",
			0x300: "rel.go",
		},
		// Dirfds has no entry for 11 → absolutize fails for the secondary.
	}
	srv := s.newServer(tr, nil, NewFileHandler(s.mustPolicy(types.DecisionAllow, nil, nil, nil), nil, 8), nil)
	got := srv.Dispatch(context.Background(), Trap{
		ID: 42, Syscall: syscallRenameat2,
		Args: [6]uint64{atFdcwd, 0x100, 11, 0x300, 0},
	})
	s.Require().False(got.Allow)
}

func (s *ServerSuite) TestDispatchFileRenameat2SecondaryEvalSymlinksFallsBackToAbs() {
	// EvalSymlinks failure on the secondary path must not fail closed — the
	// dispatcher evaluates the cleaned abs path and denies only if policy
	// says so. Here the fallback path /etc/shadow trips the deny rule, so
	// the trap is rejected for the right reason (policy), not because the
	// resolver blew up.
	sentinel := errors.New("eval boom secondary")
	base := &FakeTracee{
		Strings: map[uintptr]string{
			0x100: "/work/old",
			0x300: "/etc/shadow",
		},
	}
	wrapped := &selectiveEvalErrTracee{Tracee: base, failFor: "/etc/shadow", err: sentinel}
	policy := s.mustPolicy(types.DecisionAllow, nil, nil, []types.FileRule{
		{Paths: []string{"/etc/**"}, Operations: []string{OpCreate}, Decision: types.DecisionDeny},
	})
	srv := &Server{
		Factory: func(_ int) Tracee { return wrapped },
		File:    NewFileHandler(policy, nil, 8),
	}
	got := srv.Dispatch(context.Background(), Trap{
		ID: 42, Syscall: syscallRenameat2,
		Args: [6]uint64{atFdcwd, 0x100, atFdcwd, 0x300, 0},
	})
	s.Require().False(got.Allow)
}

func (s *ServerSuite) TestDispatchFileRenameat2SecondaryEvalSymlinksFallbackAllows() {
	// Same failure path, but the fallback path lands in an allow-policy
	// region: the dispatcher must return Allow so the kernel can surface its
	// own errno.
	sentinel := errors.New("eval boom secondary")
	base := &FakeTracee{
		Strings: map[uintptr]string{
			0x100: "/work/old",
			0x300: "/work/new",
		},
	}
	wrapped := &selectiveEvalErrTracee{Tracee: base, failFor: "/work/new", err: sentinel}
	srv := &Server{
		Factory: func(_ int) Tracee { return wrapped },
		File:    NewFileHandler(s.mustPolicy(types.DecisionAllow, nil, nil, nil), nil, 8),
	}
	got := srv.Dispatch(context.Background(), Trap{
		ID: 42, Syscall: syscallRenameat2,
		Args: [6]uint64{atFdcwd, 0x100, atFdcwd, 0x300, 0},
	})
	s.Require().True(got.Allow)
}

func (s *ServerSuite) TestDispatchFileRenameat2HappyPath() {
	tr := &FakeTracee{
		Strings: map[uintptr]string{
			0x100: "/work/a",
			0x300: "/work/b",
		},
	}
	srv := s.newServer(tr, nil, NewFileHandler(s.mustPolicy(types.DecisionAllow, nil, nil, nil), nil, 8), nil)
	got := srv.Dispatch(context.Background(), Trap{
		ID: 43, Syscall: syscallRenameat2,
		Args: [6]uint64{atFdcwd, 0x100, atFdcwd, 0x300, 0},
	})
	s.Require().True(got.Allow)
}

func (s *ServerSuite) TestDispatchFileUnlinkatDenies() {
	tr := &FakeTracee{Strings: map[uintptr]string{0x100: "/etc/thing"}}
	policy := s.mustPolicy(types.DecisionAllow, nil, nil, []types.FileRule{
		{Paths: []string{"/etc/**"}, Operations: []string{OpDelete}, Decision: types.DecisionDeny},
	})
	srv := s.newServer(tr, nil, NewFileHandler(policy, nil, 8), nil)
	got := srv.Dispatch(context.Background(), Trap{
		ID: 44, Syscall: syscallUnlinkat,
		Args: [6]uint64{atFdcwd, 0x100, 0, 0},
	})
	s.Require().False(got.Allow)
}

func (s *ServerSuite) TestDispatchFileUnlinkatLeafLinkNotResolved() {
	// Regression: a venv symlink at /Users/u/.cache/.../bin/python3 →
	// /usr/bin/python3.13. The kernel removes the cache-side directory
	// entry on unlinkat; it never touches the target. Resolving the leaf
	// would trip the system-path deny rule and break pre-commit cleanup.
	tr := &FakeTracee{
		Strings: map[uintptr]string{
			0x100: "/Users/u/.cache/pre-commit/abc/py_env-python3.12/bin/python3",
		},
		Symlinks: map[string]string{
			"/Users/u/.cache/pre-commit/abc/py_env-python3.12/bin/python3": "/usr/bin/python3.13",
		},
	}
	policy := s.mustPolicy(types.DecisionAllow, nil, nil, []types.FileRule{
		{Paths: []string{"/usr/**"}, Operations: []string{OpDelete}, Decision: types.DecisionDeny},
	})
	srv := s.newServer(tr, nil, NewFileHandler(policy, nil, 8), nil)
	got := srv.Dispatch(context.Background(), Trap{
		ID: 70, Syscall: syscallUnlinkat,
		Args: [6]uint64{atFdcwd, 0x100, 0, 0},
	})
	s.Require().True(got.Allow, "unlinkat on a cache-side symlink must not be denied based on the link target")
}

func (s *ServerSuite) TestDispatchFileUnlinkatParentLinkResolved() {
	// Parent components are still dereferenced — only the leaf is opaque.
	// Here /opt/cache is a symlink to /etc; the unlinkat path resolves to
	// /etc/passwd which the system-path rule denies.
	tr := &FakeTracee{
		Strings: map[uintptr]string{0x100: "/opt/cache/passwd"},
		Symlinks: map[string]string{
			"/opt/cache": "/etc",
		},
	}
	policy := s.mustPolicy(types.DecisionAllow, nil, nil, []types.FileRule{
		{Paths: []string{"/etc/**"}, Operations: []string{OpDelete}, Decision: types.DecisionDeny},
	})
	srv := s.newServer(tr, nil, NewFileHandler(policy, nil, 8), nil)
	got := srv.Dispatch(context.Background(), Trap{
		ID: 71, Syscall: syscallUnlinkat,
		Args: [6]uint64{atFdcwd, 0x100, 0, 0},
	})
	s.Require().False(got.Allow, "parent symlink resolution must still surface the canonical path to policy")
}

func (s *ServerSuite) TestDispatchFileRenameat2LeafLinksNotResolved() {
	// Both source and destination of renameat2 are link operations — the
	// kernel renames the directory entry, not the target. A venv symlink
	// being renamed during cleanup must not trip /usr/** deny.
	tr := &FakeTracee{
		Strings: map[uintptr]string{
			0x100: "/Users/u/.cache/pre-commit/abc/py_env/bin/python3",
			0x300: "/Users/u/.cache/pre-commit/abc/py_env/bin/python3.bak",
		},
		Symlinks: map[string]string{
			"/Users/u/.cache/pre-commit/abc/py_env/bin/python3":     "/usr/bin/python3.13",
			"/Users/u/.cache/pre-commit/abc/py_env/bin/python3.bak": "/usr/bin/python3.13",
		},
	}
	policy := s.mustPolicy(types.DecisionAllow, nil, nil, []types.FileRule{
		{Paths: []string{"/usr/**"}, Operations: []string{OpDelete, OpCreate}, Decision: types.DecisionDeny},
	})
	srv := s.newServer(tr, nil, NewFileHandler(policy, nil, 8), nil)
	got := srv.Dispatch(context.Background(), Trap{
		ID: 72, Syscall: syscallRenameat2,
		Args: [6]uint64{atFdcwd, 0x100, atFdcwd, 0x300, 0},
	})
	s.Require().True(got.Allow, "renameat2 on cache-side symlinks must not be denied based on link targets")
}

func (s *ServerSuite) TestDispatchFileFchmodatLeafLinkResolved() {
	// chmod is the counterpoint to the no-follow-leaf rule: the Linux
	// kernel always follows the leaf for fchmodat (AT_SYMLINK_NOFOLLOW is
	// unimplemented), so the gate must too — otherwise the gate would
	// allow what the kernel will then attempt against the target.
	tr := &FakeTracee{
		Strings: map[uintptr]string{0x100: "/Users/u/.cache/pre-commit/abc/py_env/bin/python3"},
		Symlinks: map[string]string{
			"/Users/u/.cache/pre-commit/abc/py_env/bin/python3": "/usr/bin/python3.13",
		},
	}
	policy := s.mustPolicy(types.DecisionAllow, nil, nil, []types.FileRule{
		{Paths: []string{"/usr/**"}, Operations: []string{OpChmod}, Decision: types.DecisionDeny},
	})
	srv := s.newServer(tr, nil, NewFileHandler(policy, nil, 8), nil)
	got := srv.Dispatch(context.Background(), Trap{
		ID: 73, Syscall: syscallFchmodat,
		Args: [6]uint64{atFdcwd, 0x100, 0, 0, 0},
	})
	s.Require().False(got.Allow, "fchmodat must keep resolving the leaf because the kernel follows it")
}

func (s *ServerSuite) TestDispatchFileUnlinkatRootPathHandled() {
	// Edge: an unlinkat path that cleans to "/" has no leaf to preserve.
	// Verify the resolver doesn't choke on the empty-leaf split.
	tr := &FakeTracee{Strings: map[uintptr]string{0x100: "/"}}
	srv := s.newServer(tr, nil, NewFileHandler(s.mustPolicy(types.DecisionAllow, nil, nil, nil), nil, 8), nil)
	got := srv.Dispatch(context.Background(), Trap{
		ID: 74, Syscall: syscallUnlinkat,
		Args: [6]uint64{atFdcwd, 0x100, 0, 0},
	})
	s.Require().True(got.Allow)
}

func (s *ServerSuite) TestDispatchFileUnlinkatParentEvalSymlinksFailFallsBack() {
	// EvalSymlinks failure on the parent must not fail closed; we evaluate
	// against the cleaned abs path so the kernel still gets to surface
	// ENOENT/ELOOP for the leaf if that's what would actually happen.
	sentinel := errors.New("eval boom parent")
	base := &FakeTracee{Strings: map[uintptr]string{0x100: "/some/parent/leaf"}}
	wrapped := &selectiveEvalErrTracee{Tracee: base, failFor: "/some/parent", err: sentinel}
	srv := &Server{
		Factory: func(_ int) Tracee { return wrapped },
		File:    NewFileHandler(s.mustPolicy(types.DecisionAllow, nil, nil, nil), nil, 8),
	}
	got := srv.Dispatch(context.Background(), Trap{
		ID: 75, Syscall: syscallUnlinkat,
		Args: [6]uint64{atFdcwd, 0x100, 0, 0},
	})
	s.Require().True(got.Allow)
}

// Walk through the remaining outer-switch cases. Each only needs to confirm
// the syscall name routes into dispatchFile (the per-spec arg layout is tested
// exhaustively in file_syscalls.go's companion tests).
func (s *ServerSuite) TestDispatchFileCoversOtherSyscalls() {
	cases := []struct {
		syscall string
		args    [6]uint64
		path    uintptr
	}{
		{syscall: syscallLinkat, args: [6]uint64{atFdcwd, 0x100, atFdcwd, 0x200, 0}, path: 0x200},
		{syscall: syscallSymlinkat, args: [6]uint64{0x100, atFdcwd, 0x200, 0, 0}, path: 0x200},
		{syscall: syscallFchmodat, args: [6]uint64{atFdcwd, 0x100, 0, 0, 0}, path: 0x100},
		{syscall: syscallFchownat, args: [6]uint64{atFdcwd, 0x100, 0, 0, 0}, path: 0x100},
		{syscall: syscallMkdirat, args: [6]uint64{atFdcwd, 0x100, 0, 0, 0}, path: 0x100},
		{syscall: syscallRenameat, args: [6]uint64{atFdcwd, 0x100, atFdcwd, 0x100}, path: 0x100},
		{syscall: syscallMknodat, args: [6]uint64{atFdcwd, 0x100, 0, 0}, path: 0x100},
		{syscall: syscallTruncate, args: [6]uint64{0x100, 0}, path: 0x100},
		{syscall: syscallFchmodat2, args: [6]uint64{atFdcwd, 0x100, 0, 0}, path: 0x100},
		{syscall: syscallOpen, args: [6]uint64{0x100, oWRONLY, 0}, path: 0x100},
		{syscall: syscallCreat, args: [6]uint64{0x100, 0o644}, path: 0x100},
		{syscall: syscallRename, args: [6]uint64{0x100, 0x100}, path: 0x100},
		{syscall: syscallMkdir, args: [6]uint64{0x100, 0o755}, path: 0x100},
		{syscall: syscallRmdir, args: [6]uint64{0x100}, path: 0x100},
		{syscall: syscallLink, args: [6]uint64{0x200, 0x100}, path: 0x100},
		{syscall: syscallUnlink, args: [6]uint64{0x100}, path: 0x100},
		{syscall: syscallSymlink, args: [6]uint64{0x200, 0x100}, path: 0x100},
		{syscall: syscallChmod, args: [6]uint64{0x100, 0o644}, path: 0x100},
		{syscall: syscallChown, args: [6]uint64{0x100, 0, 0}, path: 0x100},
		{syscall: syscallLchown, args: [6]uint64{0x100, 0, 0}, path: 0x100},
		{syscall: syscallMknod, args: [6]uint64{0x100, 0, 0}, path: 0x100},
	}
	for _, tc := range cases {
		tr := &FakeTracee{Strings: map[uintptr]string{tc.path: "/work/x"}}
		srv := s.newServer(tr, nil, NewFileHandler(s.mustPolicy(types.DecisionAllow, nil, nil, nil), nil, 8), nil)
		got := srv.Dispatch(context.Background(), Trap{ID: 50, Syscall: tc.syscall, Args: tc.args})
		s.Require().Truef(got.Allow, "%s should allow under default-allow policy", tc.syscall)
	}
}

func (s *ServerSuite) TestDispatchFileLegacySyscallsResolveAgainstCwd() {
	// The legacy syscalls have no dirfd: a relative path joins the cwd
	// (AT_FDCWD), for the primary and the secondary path alike.
	tr := &FakeTracee{
		Strings: map[uintptr]string{0x100: "old", 0x200: "new"},
		Dirfds:  map[int32]string{AtFDCWD: "/etc"},
	}
	policy := s.mustPolicy(types.DecisionAllow, nil, nil, []types.FileRule{
		{Paths: []string{"/etc/new"}, Operations: []string{OpCreate}, Decision: types.DecisionDeny},
	})
	srv := s.newServer(tr, nil, NewFileHandler(policy, nil, 8), nil)
	got := srv.Dispatch(context.Background(), Trap{ID: 51, Syscall: syscallRename, Args: [6]uint64{0x100, 0x200}})
	s.Require().False(got.Allow, "rename's relative newpath must resolve to /etc/new")
	got = srv.Dispatch(context.Background(), Trap{ID: 52, Syscall: syscallUnlink, Args: [6]uint64{0x100}})
	s.Require().True(got.Allow)
}

func (s *ServerSuite) TestDirfdArg() {
	trap := Trap{Args: [6]uint64{7, atFdcwd}}
	s.Require().Equal(int32(7), dirfdArg(trap, 0))
	s.Require().Equal(AtFDCWD, dirfdArg(trap, 1))
	s.Require().Equal(AtFDCWD, dirfdArg(trap, -1))
}

func (s *ServerSuite) TestPerformedResp() {
	s.Require().Equal(TrapResponse{ID: 5, Performed: true}, performedResp(5))
}

// --- Dispatch: git guard ---

// guardedServer wires a default-allow file handler plus a git guard over
// /work backed by fs.
func (s *ServerSuite) guardedServer(tr Tracee, fs *fakeGuardFS, auditor *collectAuditor) *Server {
	return &Server{
		Factory:   func(_ int) Tracee { return tr },
		File:      NewFileHandler(s.mustPolicy(types.DecisionAllow, nil, nil, nil), nil, 8),
		Guard:     &GitGuard{Roots: []string{"/work"}, FS: fs, Auditor: auditor},
		ChannelID: "chan-A",
	}
}

func (s *ServerSuite) TestDispatchGuardRefusesInPlaceWrites() {
	cases := []struct {
		name    string
		syscall string
		args    [6]uint64
		path    string
		target  string
	}{
		{"openat write", syscallOpenat, [6]uint64{atFdcwd, 0x100, oWRONLY}, "/work/.git/config", "write /work/.git/config"},
		{"openat create", syscallOpenat, [6]uint64{atFdcwd, 0x100, oCreat | oWRONLY}, "/work/.git/hooks/pre-commit", "create /work/.git/hooks/pre-commit"},
		{"open append", syscallOpen, [6]uint64{0x100, oAppend | oWRONLY}, "/work/.git/config", "write /work/.git/config"},
		{"creat", syscallCreat, [6]uint64{0x100, 0o755}, "/work/.git/hooks/post-checkout", "create /work/.git/hooks/post-checkout"},
		{"truncate relative to cwd", syscallTruncate, [6]uint64{0x100, 0}, "config", "write /work/.git/config"},
		{"mknodat", syscallMknodat, [6]uint64{atFdcwd, 0x100, 0, 0}, "/work/.git/hooks/x", "create /work/.git/hooks/x"},
		{"linkat onto a hook", syscallLinkat, [6]uint64{atFdcwd, 0x200, atFdcwd, 0x100, 0}, "/work/.git/hooks/pre-push", "link /work/.git/hooks/pre-push"},
		{"symlink as .git", syscallSymlink, [6]uint64{0x200, 0x100}, "/work/sub/.git", "link /work/sub/.git"},
		{"symlink via a case-folded name", syscallSymlinkat, [6]uint64{0x200, atFdcwd, 0x100}, "/work/.GIT/Hooks/pre-commit", "link /work/.GIT/Hooks/pre-commit"},
	}
	for _, c := range cases {
		s.Run(c.name, func() {
			tr := &FakeTracee{
				Strings: map[uintptr]string{0x100: c.path, 0x200: "/tmp/evil"},
				Dirfds:  map[int32]string{AtFDCWD: "/work/.git"},
			}
			fs, auditor := &fakeGuardFS{}, &collectAuditor{}
			got := s.guardedServer(tr, fs, auditor).Dispatch(context.Background(), Trap{ID: 60, PID: 9, Syscall: c.syscall, Args: c.args})
			s.Require().Equal(TrapResponse{ID: 60, ErrorNum: int32(syscall.EPERM)}, got)
			s.Require().Len(auditor.entries, 1)
			s.Require().Equal(c.target, auditor.entries[0].Target)
			s.Require().Equal(gitGuardRuleID, auditor.entries[0].RuleID)
			s.Require().Equal("chan-A", auditor.entries[0].Channel)
			s.Require().Equal(9, auditor.entries[0].PID)
			s.Require().Zero(fs.snapshots)
		})
	}
}

// git worktree add and submodule checkouts write a .git file and commondir
// in place; those ask with the process's command line, after the file
// rules. mknod of a pointer stays refused.
func (s *ServerSuite) TestDispatchGuardAsksForInPlacePointers() {
	lookup := func(int) (ProcessInfo, error) {
		return ProcessInfo{Exe: "/usr/bin/git", Cmdline: []string{"git", "worktree", "add", "../wt"}, StartTime: 5}, nil
	}
	cases := []struct {
		name     string
		syscall  string
		args     [6]uint64
		path     string
		rules    []types.FileRule
		decision types.Decision
		want     TrapResponse
		asked    string
	}{
		{"create a .git file", syscallOpenat, [6]uint64{atFdcwd, 0x100, oCreat | oWRONLY}, "/work/wt/.git", nil,
			types.DecisionAllow, TrapResponse{ID: 61, Allow: true}, "create /work/wt/.git"},
		{"create commondir", syscallOpenat, [6]uint64{atFdcwd, 0x100, oCreat | oWRONLY}, "/work/.git/worktrees/wt/commondir", nil,
			types.DecisionAllow, TrapResponse{ID: 61, Allow: true}, "create /work/.git/worktrees/wt/commondir"},
		{"denied on the card", syscallOpenat, [6]uint64{atFdcwd, 0x100, oWRONLY}, "/work/wt/.git", nil,
			types.DecisionDeny, TrapResponse{ID: 61, ErrorNum: int32(syscall.EPERM)}, "write /work/wt/.git"},
		{"a deny rule wins", syscallOpenat, [6]uint64{atFdcwd, 0x100, oCreat | oWRONLY}, "/work/wt/.git",
			[]types.FileRule{{Paths: []string{"/work/wt/.git"}, Operations: []string{OpCreate}, Decision: types.DecisionDeny}},
			types.DecisionAllow, TrapResponse{ID: 61, ErrorNum: int32(syscall.EPERM)}, ""},
		{"mknodat a .git file", syscallMknodat, [6]uint64{atFdcwd, 0x100, 0, 0}, "/work/wt/.git", nil,
			types.DecisionAllow, TrapResponse{ID: 61, ErrorNum: int32(syscall.EPERM)}, ""},
	}
	for _, c := range cases {
		s.Run(c.name, func() {
			tr := &FakeTracee{Strings: map[uintptr]string{0x100: c.path}}
			fs, auditor := &fakeGuardFS{}, &collectAuditor{}
			approver := &stubApprover{out: Outcome{Decision: c.decision, Actor: "u"}}
			srv := s.guardedServer(tr, fs, auditor)
			srv.Guard.Approver, srv.Guard.Process = approver, lookup
			if c.rules != nil {
				srv.File = NewFileHandler(s.mustPolicy(types.DecisionAllow, nil, nil, c.rules), nil, 8)
			}
			got := srv.Dispatch(context.Background(), Trap{ID: 61, PID: 9, Syscall: c.syscall, Args: c.args})
			s.Require().Equal(c.want, got)
			s.Require().Equal(c.asked, approver.got.Target)
			if c.asked != "" {
				s.Require().Equal("git worktree add ../wt", approver.got.Details["command"])
			}
		})
	}
}

// A rename onto a path an approve rule covers asks with a diff through the
// rename review; the source's check folds into that card unless a rule
// denies it. Git paths, and renames the review can't show, go the usual way.
func (s *ServerSuite) TestDispatchRenameReview() {
	const tmp, cfg = "/work/.loop/config.json.tmp.1.ab", "/work/.loop/config.json"
	approveCfg := types.FileRule{Paths: []string{"/work/.loop/config.json", "/work/.loop/keep"}, Operations: []string{OpCreate, OpDelete}, Decision: types.DecisionApprove, Message: "project config"}
	cases := []struct {
		name     string
		src, dst string
		rules    []types.FileRule
		noReview bool
		snap     *GuardSnapshot
		strErr   bool
		want     TrapResponse
		card     string // CacheKey prefix of the card asked, "" for none
		reviewed bool
	}{
		{"reviewed", tmp, cfg, []types.FileRule{approveCfg}, false, &GuardSnapshot{Data: []byte("{}\n")},
			false, TrapResponse{ID: 62, Performed: true}, "file:review:" + cfg + ":", true},
		{"source approve folds in", "/work/.loop/keep", cfg, []types.FileRule{approveCfg}, false, &GuardSnapshot{Data: []byte("{}\n")},
			false, TrapResponse{ID: 62, Performed: true}, "file:review:" + cfg + ":", true},
		{"target not under an approve rule", tmp, "/work/other", []types.FileRule{approveCfg}, false, nil,
			false, TrapResponse{ID: 62, Allow: true}, "", false},
		{"source denied", tmp, cfg, []types.FileRule{{Paths: []string{tmp}, Operations: []string{OpDelete}, Decision: types.DecisionDeny}, approveCfg}, false, nil,
			false, TrapResponse{ID: 62, ErrorNum: int32(syscall.EPERM)}, "", false},
		{"no review", tmp, cfg, []types.FileRule{approveCfg}, true, nil,
			false, TrapResponse{ID: 62, Allow: true}, "file:create:" + cfg, false},
		{"content can't be shown", tmp, cfg, []types.FileRule{approveCfg}, false, &GuardSnapshot{Data: []byte{0}},
			false, TrapResponse{ID: 62, Allow: true}, "file:create:" + cfg, false},
		{"into a git dir", "/work/x", "/work/.git/refs/heads/x", []types.FileRule{{Paths: []string{"/work/.git/**"}, Operations: []string{OpCreate}, Decision: types.DecisionApprove}}, false, nil,
			false, TrapResponse{ID: 62, Allow: true}, "file:create:/work/.git/refs/heads/x", false},
		{"out of a git dir", "/work/.git/x", "/work/y", []types.FileRule{{Paths: []string{"/work/y"}, Operations: []string{OpCreate}, Decision: types.DecisionApprove}}, false, nil,
			false, TrapResponse{ID: 62, Allow: true}, "file:create:/work/y", false},
		{"onto a guarded git path", "/work/x", "/work/.git/config", []types.FileRule{{Paths: []string{"/work/.git/config"}, Operations: []string{OpCreate}, Decision: types.DecisionApprove}}, false, &GuardSnapshot{Data: []byte("[core]\n\tbare = false\n")},
			false, TrapResponse{ID: 62, Performed: true}, "file:create:/work/.git/config", false},
		{"unreadable target", tmp, cfg, []types.FileRule{approveCfg}, false, nil,
			true, TrapResponse{ID: 62, ErrorNum: int32(syscall.EPERM)}, "", false},
	}
	for _, c := range cases {
		s.Run(c.name, func() {
			strs := map[uintptr]string{0x100: c.src, 0x200: c.dst}
			if c.strErr {
				delete(strs, 0x200)
			}
			tr := &FakeTracee{Strings: strs, UID: 501, GID: 20}
			fs, auditor := &fakeGuardFS{snap: c.snap, regular: map[string]bool{c.src: true}}, &collectAuditor{}
			approver := &stubApprover{out: Outcome{Decision: types.DecisionAllow, Actor: "u"}}
			srv := s.guardedServer(tr, fs, auditor)
			srv.File = NewFileHandler(s.mustPolicy(types.DecisionAllow, nil, nil, c.rules), approver, 8)
			if !c.noReview {
				srv.Review = &RenameReview{FS: fs, Approver: approver, Auditor: auditor}
			}
			got := srv.Dispatch(context.Background(), Trap{ID: 62, PID: 9, Syscall: syscallRenameat2, Args: [6]uint64{atFdcwd, 0x100, atFdcwd, 0x200, 0}})
			s.Require().Equal(c.want, got)
			if c.card == "" {
				s.Require().Empty(approver.got.CacheKey)
			} else {
				s.Require().True(strings.HasPrefix(approver.got.CacheKey, c.card), approver.got.CacheKey)
			}
			if c.reviewed {
				s.Require().Equal(c.src, approver.got.Details["source"])
				s.Require().Equal(1, fs.installs)
			}
		})
	}
}

// A hardlink gives a guarded file a second, unguarded name the agent could
// write in place, so the link's existing path is checked too — both with
// and without following a final symlink.
func (s *ServerSuite) TestDispatchGuardLinkSource() {
	cases := []struct {
		name     string
		syscall  string
		args     [6]uint64
		strings  map[uintptr]string
		symlinks map[string]string
		want     TrapResponse
		target   string
	}{
		{"linkat out of config", syscallLinkat, [6]uint64{atFdcwd, 0x100, atFdcwd, 0x200, 0},
			map[uintptr]string{0x100: "/work/.git/config", 0x200: "/work/x"}, nil,
			TrapResponse{ID: 64, ErrorNum: int32(syscall.EPERM)}, "link /work/.git/config"},
		{"linkat relative to olddirfd", syscallLinkat, [6]uint64{7, 0x100, atFdcwd, 0x200, 0},
			map[uintptr]string{0x100: "pre-commit", 0x200: "/work/x"}, nil,
			TrapResponse{ID: 64, ErrorNum: int32(syscall.EPERM)}, "link /work/.git/hooks/pre-commit"},
		{"legacy link out of a hook", syscallLink, [6]uint64{0x100, 0x200},
			map[uintptr]string{0x100: "/work/.git/hooks/pre-push", 0x200: "/work/x"}, nil,
			TrapResponse{ID: 64, ErrorNum: int32(syscall.EPERM)}, "link /work/.git/hooks/pre-push"},
		{"through a symlink (AT_SYMLINK_FOLLOW)", syscallLinkat, [6]uint64{atFdcwd, 0x100, atFdcwd, 0x200, 0x400},
			map[uintptr]string{0x100: "/work/cfg", 0x200: "/work/x"}, map[string]string{"/work/cfg": "/work/.git/config"},
			TrapResponse{ID: 64, ErrorNum: int32(syscall.EPERM)}, "link /work/.git/config"},
		{"unguarded source", syscallLinkat, [6]uint64{atFdcwd, 0x100, atFdcwd, 0x200, 0},
			map[uintptr]string{0x100: "/work/a", 0x200: "/work/b"}, nil,
			TrapResponse{ID: 64, Allow: true}, ""},
		{"unreadable source", syscallLinkat, [6]uint64{atFdcwd, 0x100, atFdcwd, 0x200, 0},
			map[uintptr]string{0x200: "/work/b"}, nil,
			TrapResponse{ID: 64, ErrorNum: int32(syscall.EPERM)}, ""},
		{"source dirfd gone", syscallLinkat, [6]uint64{9, 0x100, atFdcwd, 0x200, 0},
			map[uintptr]string{0x100: "rel", 0x200: "/work/b"}, nil,
			TrapResponse{ID: 64, ErrorNum: int32(syscall.EPERM)}, ""},
	}
	for _, c := range cases {
		s.Run(c.name, func() {
			tr := &FakeTracee{
				Strings:  c.strings,
				Dirfds:   map[int32]string{AtFDCWD: "/work", 7: "/work/.git/hooks"},
				Symlinks: c.symlinks,
			}
			fs, auditor := &fakeGuardFS{}, &collectAuditor{}
			got := s.guardedServer(tr, fs, auditor).Dispatch(context.Background(), Trap{ID: 64, Syscall: c.syscall, Args: c.args})
			s.Require().Equal(c.want, got)
			if c.target == "" {
				s.Require().Empty(auditor.entries)
				return
			}
			s.Require().Len(auditor.entries, 1)
			s.Require().Equal(c.target, auditor.entries[0].Target)
		})
	}
}

// The file rules see a guarded rename's target first: a deny rule on the
// path wins over the guard, which then never reads or installs anything.
func (s *ServerSuite) TestDispatchGuardRenameHonoursFileRules() {
	tr := &FakeTracee{Strings: map[uintptr]string{0x100: "/work/.git/config.lock", 0x200: "/work/.git/config"}}
	fs, auditor := &fakeGuardFS{regular: map[string]bool{"/work/.git/config.lock": true}}, &collectAuditor{}
	srv := s.guardedServer(tr, fs, auditor)
	srv.File = NewFileHandler(s.mustPolicy(types.DecisionAllow, nil, nil, []types.FileRule{{
		Paths: []string{"/work/.git/config"}, Operations: []string{OpCreate}, Decision: types.DecisionDeny,
	}}), nil, 8)
	got := srv.Dispatch(context.Background(), Trap{ID: 65, Syscall: syscallRenameat2, Args: [6]uint64{atFdcwd, 0x100, atFdcwd, 0x200, 0}})
	s.Require().Equal(TrapResponse{ID: 65, ErrorNum: int32(syscall.EPERM)}, got)
	s.Require().Zero(fs.snapshots)
	s.Require().Zero(fs.installs)
}

func (s *ServerSuite) TestDispatchGuardLetsSafeInPlaceOpsThrough() {
	cases := []struct {
		name    string
		syscall string
		args    [6]uint64
		path    string
	}{
		{"read config", syscallOpenat, [6]uint64{atFdcwd, 0x100, oRDONLY}, "/work/.git/config"},
		{"mkdir hooks", syscallMkdirat, [6]uint64{atFdcwd, 0x100, 0o755}, "/work/.git/hooks"},
		{"legacy mkdir .git", syscallMkdir, [6]uint64{0x100, 0o755}, "/work/sub/.git"},
		{"delete a hook", syscallUnlinkat, [6]uint64{atFdcwd, 0x100, 0}, "/work/.git/hooks/pre-commit"},
		{"chmod a hook", syscallFchmodat2, [6]uint64{atFdcwd, 0x100, 0o755, 0}, "/work/.git/hooks/pre-commit"},
		{"write a sample hook", syscallOpenat, [6]uint64{atFdcwd, 0x100, oCreat | oWRONLY}, "/work/.git/hooks/pre-commit.sample"},
		{"write outside the roots", syscallOpenat, [6]uint64{atFdcwd, 0x100, oWRONLY}, "/tmp/scratch/.git/config"},
	}
	for _, c := range cases {
		s.Run(c.name, func() {
			tr := &FakeTracee{Strings: map[uintptr]string{0x100: c.path}}
			auditor := &collectAuditor{}
			got := s.guardedServer(tr, &fakeGuardFS{}, auditor).Dispatch(context.Background(), Trap{ID: 61, Syscall: c.syscall, Args: c.args})
			s.Require().True(got.Allow)
			s.Require().Empty(auditor.entries)
		})
	}
}

func (s *ServerSuite) TestDispatchGuardPerformsRenameOntoGuardedPath() {
	cases := []struct {
		name      string
		syscall   string
		args      [6]uint64
		noReplace bool
	}{
		{"renameat2", syscallRenameat2, [6]uint64{atFdcwd, 0x100, atFdcwd, 0x200, 0}, false},
		{"renameat2 noreplace", syscallRenameat2, [6]uint64{atFdcwd, 0x100, atFdcwd, 0x200, renameNoReplace}, true},
		{"renameat", syscallRenameat, [6]uint64{atFdcwd, 0x100, atFdcwd, 0x200}, false},
		{"legacy rename", syscallRename, [6]uint64{0x100, 0x200}, false},
	}
	for _, c := range cases {
		s.Run(c.name, func() {
			tr := &FakeTracee{
				Strings: map[uintptr]string{0x100: "config.lock", 0x200: "config"},
				Dirfds:  map[int32]string{AtFDCWD: "/work/.git"},
				UID:     501, GID: 20,
			}
			fs := &fakeGuardFS{
				snap:    &GuardSnapshot{Dst: "/work/.git/config", Data: []byte(gitInitConfig)},
				regular: map[string]bool{"/work/.git/config.lock": true},
			}
			got := s.guardedServer(tr, fs, &collectAuditor{}).Dispatch(context.Background(), Trap{ID: 62, Syscall: c.syscall, Args: c.args})
			s.Require().Equal(TrapResponse{ID: 62, Performed: true}, got)
			s.Require().Equal("/work/.git/config.lock", fs.gotSrc)
			s.Require().Equal("/work/.git/config", fs.gotDst)
			s.Require().Equal(501, fs.gotUID)
			s.Require().Equal(20, fs.gotGID)
			s.Require().Equal(1, fs.installs)
			s.Require().Equal(c.noReplace, fs.gotNoReplace)
		})
	}
}

func (s *ServerSuite) TestDispatchGuardExchangeWithGuardedSourceIsRefused() {
	tr := &FakeTracee{Strings: map[uintptr]string{0x100: "/work/.git/config", 0x200: "/work/evil"}}
	fs, auditor := &fakeGuardFS{}, &collectAuditor{}
	got := s.guardedServer(tr, fs, auditor).Dispatch(context.Background(), Trap{
		ID: 63, Syscall: syscallRenameat2,
		Args: [6]uint64{atFdcwd, 0x100, atFdcwd, 0x200, renameExchange},
	})
	s.Require().Equal(TrapResponse{ID: 63, ErrorNum: int32(syscall.EPERM)}, got)
	s.Require().Zero(fs.snapshots)
	s.Require().Equal("write /work/evil", auditor.entries[0].Target)
}

func (s *ServerSuite) TestDispatchGuardUnguardedRenamesUsePolicy() {
	cases := []struct {
		name  string
		src   string
		dst   string
		flags uint64
	}{
		{"plain rename", "/work/a", "/work/b", 0},
		{"exchange between unguarded paths", "/work/a", "/work/b", renameExchange},
		{"moving a guarded file away", "/work/.git/hooks/pre-commit", "/work/b", 0},
	}
	for _, c := range cases {
		s.Run(c.name, func() {
			tr := &FakeTracee{Strings: map[uintptr]string{0x100: c.src, 0x200: c.dst}}
			fs := &fakeGuardFS{}
			got := s.guardedServer(tr, fs, &collectAuditor{}).Dispatch(context.Background(), Trap{
				ID: 64, Syscall: syscallRenameat2,
				Args: [6]uint64{atFdcwd, 0x100, atFdcwd, 0x200, c.flags},
			})
			s.Require().True(got.Allow)
			s.Require().Zero(fs.snapshots)
		})
	}
}

func (s *ServerSuite) TestDispatchGuardTreeRenames() {
	cases := []struct {
		name     string
		src, dst string
		flags    uint64
		regular  bool
		policy   []types.FileRule
		want     TrapResponse
	}{
		{
			name: "directory onto a submodule git dir is refused",
			src:  "/w/staging", dst: "/w/.git/modules/foo",
			want: TrapResponse{ID: 67, ErrorNum: int32(syscall.EPERM)},
		},
		{
			name: "file onto a ref goes to policy (allow)",
			src:  "/w/.git/refs/heads/main.lock", dst: "/w/.git/refs/heads/main", regular: true,
			want: TrapResponse{ID: 67, Allow: true},
		},
		{
			name: "file onto a ref goes to policy (deny)",
			src:  "/w/.git/refs/heads/main.lock", dst: "/w/.git/refs/heads/main", regular: true,
			policy: []types.FileRule{{Paths: []string{"/w/.git/refs/**"}, Operations: []string{OpCreate}, Decision: types.DecisionDeny}},
			want:   TrapResponse{ID: 67, ErrorNum: int32(syscall.EPERM)},
		},
		{
			name: "exchange of a git dir entry is refused",
			src:  "/w/.git/modules/foo", dst: "/w/elsewhere", flags: renameExchange,
			want: TrapResponse{ID: 67, ErrorNum: int32(syscall.EPERM)},
		},
	}
	for _, c := range cases {
		s.Run(c.name, func() {
			tr := &FakeTracee{Strings: map[uintptr]string{0x100: c.src, 0x200: c.dst}}
			fs, auditor := &fakeGuardFS{regular: map[string]bool{c.src: c.regular}}, &collectAuditor{}
			srv := &Server{
				Factory:   func(_ int) Tracee { return tr },
				File:      NewFileHandler(s.mustPolicy(types.DecisionAllow, nil, nil, c.policy), nil, 8),
				Guard:     &GitGuard{Roots: []string{"/w"}, FS: fs, Auditor: auditor},
				ChannelID: "chan-A",
			}
			got := srv.Dispatch(context.Background(), Trap{
				ID: 67, Syscall: syscallRenameat2,
				Args: [6]uint64{atFdcwd, 0x100, atFdcwd, 0x200, c.flags},
			})
			s.Require().Equal(c.want, got)
			s.Require().Zero(fs.snapshots, "tree renames never snapshot")
		})
	}
}

func (s *ServerSuite) TestDispatchWithoutGuardIgnoresGitPaths() {
	tr := &FakeTracee{Strings: map[uintptr]string{0x100: "/work/.git/config.lock", 0x200: "/work/.git/config"}}
	srv := s.newServer(tr, nil, NewFileHandler(s.mustPolicy(types.DecisionAllow, nil, nil, nil), nil, 8), nil)
	got := srv.Dispatch(context.Background(), Trap{ID: 65, Syscall: syscallOpenat, Args: [6]uint64{atFdcwd, 0x200, oWRONLY}})
	s.Require().True(got.Allow)
	got = srv.Dispatch(context.Background(), Trap{ID: 66, Syscall: syscallRenameat2, Args: [6]uint64{atFdcwd, 0x100, atFdcwd, 0x200, 0}})
	s.Require().True(got.Allow)
}

// --- Run loop ---

func (s *ServerSuite) TestRunLoopDispatchesAndSends() {
	tr := &FakeTracee{
		Strings:      map[uintptr]string{0x100: "/bin/ls"},
		PointerLists: map[uintptr][]string{0x200: {"/bin/ls"}},
	}
	transport := &scriptedTransport{
		recv: []recvEvent{
			{trap: Trap{ID: 1, Syscall: "execve", Args: [6]uint64{0x100, 0x200}}},
		},
	}
	srv := &Server{
		Transport: transport,
		Factory:   func(_ int) Tracee { return tr },
		Execve:    NewExecveHandler(s.mustPolicy(types.DecisionAllow, nil, nil, nil), nil),
	}
	err := srv.Run(context.Background())
	s.Require().NoError(err)
	s.Require().Len(transport.sent, 1)
	s.Require().True(transport.sent[0].Allow)
	s.Require().Equal(uint64(1), transport.sent[0].ID)
}

func (s *ServerSuite) TestRunReturnsCleanlyOnEOF() {
	transport := &scriptedTransport{} // Recv returns io.EOF immediately
	srv := &Server{
		Transport: transport,
		Factory:   func(_ int) Tracee { return &FakeTracee{} },
	}
	s.Require().NoError(srv.Run(context.Background()))
}

func (s *ServerSuite) TestRunReturnsCleanlyOnContextCanceledFromRecv() {
	transport := &scriptedTransport{
		recv: []recvEvent{{err: context.Canceled}},
	}
	srv := &Server{
		Transport: transport,
		Factory:   func(_ int) Tracee { return &FakeTracee{} },
	}
	s.Require().NoError(srv.Run(context.Background()))
}

func (s *ServerSuite) TestRunWrapsRecvError() {
	boom := errors.New("recv-boom")
	transport := &scriptedTransport{recv: []recvEvent{{err: boom}}}
	srv := &Server{
		Transport: transport,
		Factory:   func(_ int) Tracee { return &FakeTracee{} },
	}
	err := srv.Run(context.Background())
	s.Require().Error(err)
	s.Require().ErrorIs(err, boom)
	s.Require().Contains(err.Error(), "agentgate: transport recv")
}

func (s *ServerSuite) TestRunWrapsSendError() {
	tr := &FakeTracee{
		Strings:      map[uintptr]string{0x100: "/bin/ls"},
		PointerLists: map[uintptr][]string{0x200: {"/bin/ls"}},
	}
	boom := errors.New("send-boom")
	transport := &scriptedTransport{
		recv: []recvEvent{
			{trap: Trap{ID: 1, Syscall: "execve", Args: [6]uint64{0x100, 0x200}}},
		},
		sendErr: boom,
		sendAt:  1,
	}
	srv := &Server{
		Transport: transport,
		Factory:   func(_ int) Tracee { return tr },
		Execve:    NewExecveHandler(s.mustPolicy(types.DecisionAllow, nil, nil, nil), nil),
	}
	err := srv.Run(context.Background())
	s.Require().ErrorIs(err, boom)
	s.Require().Contains(err.Error(), "agentgate: transport send")
}

func (s *ServerSuite) TestRunExitsOnContextCanceledBeforeRecv() {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	transport := &scriptedTransport{
		// Recv would happily keep returning EOF, but the loop must check
		// ctx.Err() at the top and return the context error first.
	}
	srv := &Server{
		Transport: transport,
		Factory:   func(_ int) Tracee { return &FakeTracee{} },
	}
	err := srv.Run(ctx)
	s.Require().ErrorIs(err, context.Canceled)
	s.Require().Empty(transport.sent)
}

// TestServerCloseDelegatesToTransport: Server.Close is the gate's shutdown
// lever — it must reach into Transport.Close so the parent can release the
// notify fd when the child exits. Bug repro: without this, runParent hangs
// waiting on Run to observe a ctx cancel that the kernel ioctl can't see.
func (s *ServerSuite) TestServerCloseDelegatesToTransport() {
	transport := &scriptedTransport{}
	srv := &Server{Transport: transport}
	s.Require().NoError(srv.Close())
	s.Require().Equal(1, transport.closed)
}

// TestServerCloseForwardsTransportError: a Close error must surface so the
// caller can log it; we don't swallow ENOSPC / EBADF / etc. on shutdown.
func (s *ServerSuite) TestServerCloseForwardsTransportError() {
	boom := errors.New("close-boom")
	transport := &scriptedTransport{closeErr: boom}
	srv := &Server{Transport: transport}
	s.Require().ErrorIs(srv.Close(), boom)
}

// TestServerCloseNilTransportIsNoop: a Server constructed without a
// Transport (e.g. partial test wiring) must not panic on Close.
func (s *ServerSuite) TestServerCloseNilTransportIsNoop() {
	srv := &Server{}
	s.Require().NoError(srv.Close())
}

// --- evalErrTracee (used by one test above) ---

// evalErrTracee delegates to an inner Tracee for everything except EvalSymlinks,
// which always returns the injected error. Cheaper than threading an error
// field into FakeTracee for a single test.
type evalErrTracee struct {
	Tracee
	err error
}

func (t *evalErrTracee) EvalSymlinks(_ string) (string, error) {
	return "", t.err
}

// selectiveEvalErrTracee fails EvalSymlinks only for a specific input path,
// letting other paths pass through the delegated Tracee unchanged. Used to
// exercise the secondary-path branch of resolveSecondaryPath without tripping
// the primary-path branch first.
type selectiveEvalErrTracee struct {
	Tracee
	failFor string
	err     error
}

func (t *selectiveEvalErrTracee) EvalSymlinks(path string) (string, error) {
	if path == t.failFor {
		return "", t.err
	}
	return t.Tracee.EvalSymlinks(path)
}
