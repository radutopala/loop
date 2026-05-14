//go:build linux

package dockerproxy

import (
	"errors"
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
