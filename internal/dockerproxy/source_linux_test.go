//go:build linux

package dockerproxy

import (
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type SourceLinuxSuite struct {
	suite.Suite
}

func TestSourceLinuxSuite(t *testing.T) {
	suite.Run(t, new(SourceLinuxSuite))
}

// fakeProc builds an os.ReadFile-shaped stub backed by an in-memory
// /proc tree. environ entries are joined with NUL like real /proc.
func fakeProc(t *testing.T, tree map[int]struct {
	ppid int
	env  []string
}) func(string) ([]byte, error) {
	t.Helper()
	return func(path string) ([]byte, error) {
		var pid int
		var kind string
		if _, err := fmt.Sscanf(path, "/proc/%d/%s", &pid, &kind); err != nil {
			return nil, fmt.Errorf("unrecognized path: %s", path)
		}
		node, ok := tree[pid]
		if !ok {
			return nil, os.ErrNotExist
		}
		switch kind {
		case "environ":
			var buf []byte
			for i, kv := range node.env {
				if i > 0 {
					buf = append(buf, 0)
				}
				buf = append(buf, []byte(kv)...)
			}
			return buf, nil
		case "status":
			return fmt.Appendf(nil, "Name:\tx\nPid:\t%d\nPPid:\t%d\n", pid, node.ppid), nil
		default:
			return nil, fmt.Errorf("unknown kind: %s", kind)
		}
	}
}

// --- walkProcSource ---

func (s *SourceLinuxSuite) TestWalkProcMarkerOnPeerItself() {
	read := fakeProc(s.T(), map[int]struct {
		ppid int
		env  []string
	}{
		200: {ppid: 1, env: []string{"LOOP_TERMINAL_LEAF=leaf-direct", "PATH=/usr/bin"}},
	})
	require.Equal(s.T(), "terminal:leaf-direct", walkProcSource(200, read))
}

func (s *SourceLinuxSuite) TestWalkProcMarkerOnAncestor() {
	// shell (pid 300) inherits env from exec'd parent (pid 200); the
	// marker lives on the parent. The walker must climb PPid to find it.
	read := fakeProc(s.T(), map[int]struct {
		ppid int
		env  []string
	}{
		300: {ppid: 200, env: []string{"PATH=/usr/bin"}},
		200: {ppid: 1, env: []string{"LOOP_TERMINAL_LEAF=leaf-via-parent"}},
	})
	require.Equal(s.T(), "terminal:leaf-via-parent", walkProcSource(300, read))
}

func (s *SourceLinuxSuite) TestWalkProcNoMarkerReturnsEmpty() {
	// PID 1 (container entrypoint) has no marker — the walker terminates
	// at pid==1 and returns "" so the caller defaults to "chat".
	read := fakeProc(s.T(), map[int]struct {
		ppid int
		env  []string
	}{
		400: {ppid: 1, env: []string{"PATH=/usr/bin"}},
		1:   {ppid: 0, env: []string{"PATH=/usr/bin"}},
	})
	require.Equal(s.T(), "", walkProcSource(400, read))
}

func (s *SourceLinuxSuite) TestWalkProcStopsOnPPidReadError() {
	// environ readable on the peer but status (PPid) errors → walker
	// can't continue → "" (and we don't loop forever).
	read := func(path string) ([]byte, error) {
		if path == "/proc/500/environ" {
			return []byte("PATH=/usr/bin"), nil
		}
		return nil, errors.New("proc gone")
	}
	require.Equal(s.T(), "", walkProcSource(500, read))
}

func (s *SourceLinuxSuite) TestWalkProcStopsOnSelfParent() {
	// Pathological /proc state: PPid == self. Walker must not infinite-loop.
	read := fakeProc(s.T(), map[int]struct {
		ppid int
		env  []string
	}{
		600: {ppid: 600, env: []string{"PATH=/usr/bin"}},
	})
	require.Equal(s.T(), "", walkProcSource(600, read))
}

func (s *SourceLinuxSuite) TestWalkProcEnvironReadErrorContinuesUp() {
	// /proc/<peer>/environ is gone (process died between SO_PEERCRED and
	// our read), but its parent is still readable and carries the marker.
	read := func(path string) ([]byte, error) {
		switch path {
		case "/proc/700/environ":
			return nil, errors.New("vanished")
		case "/proc/700/status":
			return []byte("PPid:\t650\n"), nil
		case "/proc/650/environ":
			return []byte("LOOP_TERMINAL_LEAF=leaf-recover"), nil
		case "/proc/650/status":
			return []byte("PPid:\t1\n"), nil
		}
		return nil, os.ErrNotExist
	}
	require.Equal(s.T(), "terminal:leaf-recover", walkProcSource(700, read))
}

func (s *SourceLinuxSuite) TestWalkProcBoundedDepth() {
	// 32-deep chain with the marker only at the very top. The walker
	// caps at 16 hops, so a too-deep chain must return "" — proves the
	// depth bound holds even with no other terminator.
	tree := map[int]struct {
		ppid int
		env  []string
	}{}
	for i := 2; i <= 33; i++ {
		tree[i] = struct {
			ppid int
			env  []string
		}{ppid: i - 1, env: []string{"PATH=/x"}}
	}
	tree[2] = struct {
		ppid int
		env  []string
	}{ppid: 1, env: []string{"LOOP_TERMINAL_LEAF=top"}}
	require.Equal(s.T(), "", walkProcSource(33, fakeProc(s.T(), tree)))
}

func (s *SourceLinuxSuite) TestWalkProcStartingAtInitReturnsEmpty() {
	// peerPID==1 — loop never enters (pid > 1 false). Confirms we don't
	// crash and don't claim a source for the entrypoint.
	require.Equal(s.T(), "", walkProcSource(1, func(string) ([]byte, error) {
		s.T().Fatal("readFile should not have been called for pid=1")
		return nil, nil
	}))
}

// --- readProcPPID ---

func (s *SourceLinuxSuite) TestReadProcPPIDParsesValue() {
	read := func(path string) ([]byte, error) {
		require.Equal(s.T(), "/proc/123/status", path)
		return []byte("Name:\tbash\nUmask:\t0022\nState:\tS\nPPid:\t77\nTracerPid:\t0\n"), nil
	}
	ppid, err := readProcPPID(123, read)
	require.NoError(s.T(), err)
	require.Equal(s.T(), 77, ppid)
}

func (s *SourceLinuxSuite) TestReadProcPPIDMissingField() {
	read := func(string) ([]byte, error) { return []byte("Name:\tx\n"), nil }
	_, err := readProcPPID(123, read)
	require.Error(s.T(), err)
}

func (s *SourceLinuxSuite) TestReadProcPPIDReadError() {
	read := func(string) ([]byte, error) { return nil, errors.New("nope") }
	_, err := readProcPPID(123, read)
	require.Error(s.T(), err)
}

func (s *SourceLinuxSuite) TestReadProcPPIDInvalidNumber() {
	read := func(string) ([]byte, error) { return []byte("PPid:\tabc\n"), nil }
	_, err := readProcPPID(123, read)
	require.Error(s.T(), err)
}

// --- defaultPeerSource ---

func (s *SourceLinuxSuite) TestDefaultPeerSourceHitsRealProcSelf() {
	// Our own process won't have LOOP_TERMINAL_LEAF set, but the real
	// /proc walk should still terminate cleanly and return "". Exercises
	// the os.ReadFile binding so we don't ship dead code.
	require.Equal(s.T(), "", defaultPeerSource(os.Getpid()))
}

// --- readPeerPID ---

// --- peerPIDFromRaw (error precedence) ---

// fakeRaw is a rawSyscallConn fake. controlErr is returned from Control;
// when nil, getPID is invoked with fd=0 and its outcome is captured.
// Read/Write are stubs so fakeRaw also satisfies syscall.RawConn (needed
// by the readPeerPIDWith happy-path test).
type fakeRaw struct {
	controlErr error
	invoke     bool
}

func (f *fakeRaw) Control(fn func(fd uintptr)) error {
	if f.controlErr != nil {
		return f.controlErr
	}
	f.invoke = true
	fn(0)
	return nil
}

func (f *fakeRaw) Read(_ func(fd uintptr) bool) error  { return nil }
func (f *fakeRaw) Write(_ func(fd uintptr) bool) error { return nil }

func (s *SourceLinuxSuite) TestPeerPIDFromRawHappyPath() {
	raw := &fakeRaw{}
	pid, err := peerPIDFromRaw(raw, func(uintptr) (int, error) { return 1234, nil })
	require.NoError(s.T(), err)
	require.Equal(s.T(), 1234, pid)
	require.True(s.T(), raw.invoke)
}

func (s *SourceLinuxSuite) TestPeerPIDFromRawControlError() {
	// Control itself errors — getPID must not be consulted.
	called := false
	_, err := peerPIDFromRaw(&fakeRaw{controlErr: errors.New("control failed")}, func(uintptr) (int, error) {
		called = true
		return 0, nil
	})
	require.Error(s.T(), err)
	require.False(s.T(), called)
}

func (s *SourceLinuxSuite) TestPeerPIDFromRawGetsockoptError() {
	// Control succeeds, but the syscall inside fails — surfaces as the
	// final error after Control returns nil.
	_, err := peerPIDFromRaw(&fakeRaw{}, func(uintptr) (int, error) {
		return 0, errors.New("getsockopt failed")
	})
	require.Error(s.T(), err)
}

func (s *SourceLinuxSuite) TestReadPeerPIDWithSyscallConnError() {
	// rawConn errors → readPeerPIDWith returns it without consulting getPID.
	called := false
	_, err := readPeerPIDWith(
		func() (syscall.RawConn, error) { return nil, errors.New("syscall conn failed") },
		func(uintptr) (int, error) { called = true; return 0, nil },
	)
	require.Error(s.T(), err)
	require.False(s.T(), called)
}

func (s *SourceLinuxSuite) TestReadPeerPIDWithHappyPath() {
	// Both rawConn and getPID succeed → returned pid is the getPID result.
	raw := &fakeRaw{}
	pid, err := readPeerPIDWith(
		func() (syscall.RawConn, error) { return raw, nil },
		func(uintptr) (int, error) { return 9001, nil },
	)
	require.NoError(s.T(), err)
	require.Equal(s.T(), 9001, pid)
}

// --- getsockoptUcredPID (production binding) ---

func (s *SourceLinuxSuite) TestGetsockoptUcredPIDErrorsOnInvalidFD() {
	// fd=-1 is never a valid socket → syscall returns EBADF or similar.
	// Exercises the err != nil branch in the production wrapper.
	_, err := getsockoptUcredPID(^uintptr(0))
	require.Error(s.T(), err)
}

func (s *SourceLinuxSuite) TestReadPeerPIDOnClosedConnErrors() {
	// SyscallConn() on a closed *net.UnixConn returns an error; covers the
	// "uc.SyscallConn() != nil" early-return branch.
	dir, err := os.MkdirTemp("", "loop-px-closed")
	require.NoError(s.T(), err)
	defer func() { _ = os.RemoveAll(dir) }()

	ln, err := net.Listen("unix", dir+"/s")
	require.NoError(s.T(), err)
	defer func() { _ = ln.Close() }()

	type result struct {
		err error
	}
	got := make(chan result, 1)
	go func() {
		c, aerr := ln.Accept()
		if aerr != nil {
			got <- result{err: aerr}
			return
		}
		uc := c.(*net.UnixConn)
		_ = uc.Close() // force SyscallConn to fail
		_, perr := readPeerPID(uc)
		got <- result{err: perr}
	}()

	client, err := net.Dial("unix", dir+"/s")
	require.NoError(s.T(), err)
	defer func() { _ = client.Close() }()

	r := <-got
	require.Error(s.T(), r.err)
}

func (s *SourceLinuxSuite) TestReadPeerPIDOnLivePair() {
	dir, err := os.MkdirTemp("", "loop-px-readpid")
	require.NoError(s.T(), err)
	defer func() { _ = os.RemoveAll(dir) }()

	ln, err := net.Listen("unix", dir+"/s")
	require.NoError(s.T(), err)
	defer func() { _ = ln.Close() }()

	type result struct {
		pid int
		err error
	}
	got := make(chan result, 1)
	go func() {
		c, aerr := ln.Accept()
		if aerr != nil {
			got <- result{err: aerr}
			return
		}
		defer func() { _ = c.Close() }()
		uc, ok := c.(*net.UnixConn)
		if !ok {
			got <- result{err: errors.New("not a unix conn")}
			return
		}
		pid, perr := readPeerPID(uc)
		got <- result{pid: pid, err: perr}
	}()

	client, err := net.Dial("unix", dir+"/s")
	require.NoError(s.T(), err)
	defer func() { _ = client.Close() }()

	r := <-got
	require.NoError(s.T(), r.err)
	require.Equal(s.T(), os.Getpid(), r.pid)
}
