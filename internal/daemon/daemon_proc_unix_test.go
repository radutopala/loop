//go:build !windows

package daemon

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/stretchr/testify/require"
)

func (s *DaemonSuite) TestRealSystemStartDetached() {
	rs := RealSystem{}
	logFile := filepath.Join(s.T().TempDir(), "loop.log")
	require.NoError(s.T(), os.WriteFile(logFile, []byte("earlier\n"), 0o644))

	pid, err := rs.StartDetached("sh", []string{"-c", "echo out; echo err >&2"}, logFile)
	require.NoError(s.T(), err)
	require.Positive(s.T(), pid)

	require.Eventually(s.T(), func() bool {
		data, _ := os.ReadFile(logFile)
		return strings.Contains(string(data), "out\n") && strings.Contains(string(data), "err\n")
	}, 5*time.Second, 10*time.Millisecond)
	data, err := os.ReadFile(logFile)
	require.NoError(s.T(), err)
	require.True(s.T(), strings.HasPrefix(string(data), "earlier\n"), "log is appended, not truncated")
}

func (s *DaemonSuite) TestRealSystemStartDetachedOwnSession() {
	rs := RealSystem{}
	logFile := filepath.Join(s.T().TempDir(), "loop.log")

	pid, err := rs.StartDetached("sleep", []string{"30"}, logFile)
	require.NoError(s.T(), err)

	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	require.NoError(s.T(), err)
	// Fields after the parenthesised comm: state ppid pgrp session ...
	fields := strings.Fields(string(stat[strings.LastIndexByte(string(stat), ')')+1:]))
	require.Equal(s.T(), strconv.Itoa(pid), fields[3], "process leads its own session")

	require.NoError(s.T(), rs.Terminate(pid))
}

func (s *DaemonSuite) TestRealSystemStartDetachedErrors() {
	rs := RealSystem{}
	tmp := s.T().TempDir()

	_, err := rs.StartDetached("sh", nil, filepath.Join(tmp, "missing", "loop.log"))
	require.Error(s.T(), err)

	_, err = rs.StartDetached(filepath.Join(tmp, "no-such-binary"), nil, filepath.Join(tmp, "loop.log"))
	require.Error(s.T(), err)
}

func (s *DaemonSuite) TestRealSystemTerminateNoProcess() {
	require.Error(s.T(), RealSystem{}.Terminate(1<<22+1))
}
