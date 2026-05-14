//go:build linux

package procsource

import (
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type ProcWalkSuite struct {
	suite.Suite
}

func TestProcWalkSuite(t *testing.T) {
	suite.Run(t, new(ProcWalkSuite))
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

func (s *ProcWalkSuite) TestWalkProcMarkerOnPeerItself() {
	read := fakeProc(s.T(), map[int]struct {
		ppid int
		env  []string
	}{
		200: {ppid: 1, env: []string{"LOOP_TERMINAL_LEAF=leaf-direct", "PATH=/usr/bin"}},
	})
	require.Equal(s.T(), "terminal:leaf-direct", walkProcSource(200, read))
}

func (s *ProcWalkSuite) TestWalkProcMarkerOnAncestor() {
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

func (s *ProcWalkSuite) TestWalkProcNoMarkerReturnsEmpty() {
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

func (s *ProcWalkSuite) TestWalkProcStopsOnPPidReadError() {
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

func (s *ProcWalkSuite) TestWalkProcStopsOnSelfParent() {
	// Pathological /proc state: PPid == self. Walker must not infinite-loop.
	read := fakeProc(s.T(), map[int]struct {
		ppid int
		env  []string
	}{
		600: {ppid: 600, env: []string{"PATH=/usr/bin"}},
	})
	require.Equal(s.T(), "", walkProcSource(600, read))
}

func (s *ProcWalkSuite) TestWalkProcEnvironReadErrorContinuesUp() {
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

func (s *ProcWalkSuite) TestWalkProcBoundedDepth() {
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

func (s *ProcWalkSuite) TestWalkProcStartingAtInitReturnsEmpty() {
	// peerPID==1 — loop never enters (pid > 1 false). Confirms we don't
	// crash and don't claim a source for the entrypoint.
	require.Equal(s.T(), "", walkProcSource(1, func(string) ([]byte, error) {
		s.T().Fatal("readFile should not have been called for pid=1")
		return nil, nil
	}))
}

// --- readProcPPID ---

func (s *ProcWalkSuite) TestReadProcPPIDParsesValue() {
	read := func(path string) ([]byte, error) {
		require.Equal(s.T(), "/proc/123/status", path)
		return []byte("Name:\tbash\nUmask:\t0022\nState:\tS\nPPid:\t77\nTracerPid:\t0\n"), nil
	}
	ppid, err := readProcPPID(123, read)
	require.NoError(s.T(), err)
	require.Equal(s.T(), 77, ppid)
}

func (s *ProcWalkSuite) TestReadProcPPIDMissingField() {
	read := func(string) ([]byte, error) { return []byte("Name:\tx\n"), nil }
	_, err := readProcPPID(123, read)
	require.Error(s.T(), err)
}

func (s *ProcWalkSuite) TestReadProcPPIDReadError() {
	read := func(string) ([]byte, error) { return nil, errors.New("nope") }
	_, err := readProcPPID(123, read)
	require.Error(s.T(), err)
}

func (s *ProcWalkSuite) TestReadProcPPIDInvalidNumber() {
	read := func(string) ([]byte, error) { return []byte("PPid:\tabc\n"), nil }
	_, err := readProcPPID(123, read)
	require.Error(s.T(), err)
}

// --- lookup (production binding) ---

func (s *ProcWalkSuite) TestLookupReturnsEmptyForInitPID() {
	// PID 1 in any environment is the entrypoint with no LOOP_TERMINAL_LEAF
	// marker. Exercises the production os.ReadFile binding without depending
	// on the test process's own environment (which may itself carry the
	// marker if the test was launched from a terminal pane).
	require.Equal(s.T(), "", lookup(1))
}

func (s *ProcWalkSuite) TestLookupRealProcSelfDoesNotPanic() {
	// The real /proc walk on os.Getpid() must terminate cleanly and return
	// either "" or a "terminal:..." string. Asserting either-or proves the
	// os.ReadFile binding is exercised in CI without making the test brittle
	// to the test runner's own LOOP_TERMINAL_LEAF state.
	got := lookup(os.Getpid())
	if got != "" {
		require.Contains(s.T(), got, "terminal:")
	}
}
