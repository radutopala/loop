package agentgate

import (
	"context"
	"errors"
	"io"
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
	// Drive the "SyscallByName missed" branch inside dispatchFile. The outer
	// Dispatch switch only lists real file syscalls, so we go straight to
	// dispatchFile with a crafted spec-miss name. Since dispatchFile is
	// unexported, we exercise it via a file-handler present + BPF-mismatch
	// scenario by calling Dispatch with a real case name but temporarily
	// clearing the table; the simpler path: poke Dispatch with a file-ish
	// name that is not in the switch — handled by the outer default branch
	// already covered by TestDispatchUnknownSyscallDeniesEPERM. We instead
	// test via a known file syscall after removing File handler, which hits
	// the nil-handler branch already. Combine coverage by directly calling
	// the private dispatchFile on a crafted server.
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
	}
	for _, tc := range cases {
		tr := &FakeTracee{Strings: map[uintptr]string{tc.path: "/work/x"}}
		srv := s.newServer(tr, nil, NewFileHandler(s.mustPolicy(types.DecisionAllow, nil, nil, nil), nil, 8), nil)
		got := srv.Dispatch(context.Background(), Trap{ID: 50, Syscall: tc.syscall, Args: tc.args})
		s.Require().Truef(got.Allow, "%s should allow under default-allow policy", tc.syscall)
	}
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
