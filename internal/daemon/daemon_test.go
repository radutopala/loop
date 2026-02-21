package daemon

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// --- mockSystem ---

type mockSystem struct {
	mock.Mock
}

func (m *mockSystem) Executable() (string, error) {
	args := m.Called()
	return args.String(0), args.Error(1)
}

func (m *mockSystem) UserHomeDir() (string, error) {
	args := m.Called()
	return args.String(0), args.Error(1)
}

func (m *mockSystem) MkdirAll(path string, perm os.FileMode) error {
	return m.Called(path, perm).Error(0)
}

func (m *mockSystem) WriteFile(name string, data []byte, perm os.FileMode) error {
	return m.Called(name, data, perm).Error(0)
}

func (m *mockSystem) RemoveFile(name string) error {
	return m.Called(name).Error(0)
}

func (m *mockSystem) RunCommand(name string, args ...string) ([]byte, error) {
	callArgs := m.Called(name, args)
	if callArgs.Get(0) == nil {
		return nil, callArgs.Error(1)
	}
	return callArgs.Get(0).([]byte), callArgs.Error(1)
}

func (m *mockSystem) Stat(name string) (os.FileInfo, error) {
	args := m.Called(name)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(os.FileInfo), args.Error(1)
}

// --- Test Suite ---

type DaemonSuite struct {
	suite.Suite
	origGetUID       func() int
	origEvalSymlinks func(string) (string, error)
	origOsGetenv     func(string) string
}

func TestDaemonSuite(t *testing.T) {
	suite.Run(t, new(DaemonSuite))
}

func (s *DaemonSuite) SetupTest() {
	s.origGetUID = getUID
	s.origEvalSymlinks = evalSymlinks
	s.origOsGetenv = osGetenv
	getUID = func() int { return 501 }
	evalSymlinks = func(path string) (string, error) { return path, nil }
	osGetenv = func(string) string { return "" }
}

func (s *DaemonSuite) TearDownTest() {
	getUID = s.origGetUID
	evalSymlinks = s.origEvalSymlinks
	osGetenv = s.origOsGetenv
}

// --- removeIfExists tests ---

func (s *DaemonSuite) TestRemoveIfExistsSuccess() {
	sys := new(mockSystem)
	sys.On("RemoveFile", "/some/path").Return(nil)
	err := removeIfExists(sys, "/some/path")
	require.NoError(s.T(), err)
}

func (s *DaemonSuite) TestRemoveIfExistsNotExist() {
	sys := new(mockSystem)
	sys.On("RemoveFile", "/some/path").Return(os.ErrNotExist)
	err := removeIfExists(sys, "/some/path")
	require.NoError(s.T(), err)
}

func (s *DaemonSuite) TestRemoveIfExistsError() {
	sys := new(mockSystem)
	sys.On("RemoveFile", "/some/path").Return(os.ErrPermission)
	err := removeIfExists(sys, "/some/path")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "removing unit file")
}

// --- RealSystem tests ---

func (s *DaemonSuite) TestRealSystemExecutable() {
	rs := RealSystem{}
	exe, err := rs.Executable()
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), exe)
}

func (s *DaemonSuite) TestRealSystemUserHomeDir() {
	rs := RealSystem{}
	home, err := rs.UserHomeDir()
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), home)
}

func (s *DaemonSuite) TestRealSystemMkdirAll() {
	rs := RealSystem{}
	tmp := s.T().TempDir()
	err := rs.MkdirAll(filepath.Join(tmp, "a", "b"), 0o755)
	require.NoError(s.T(), err)
}

func (s *DaemonSuite) TestRealSystemWriteFile() {
	rs := RealSystem{}
	tmp := s.T().TempDir()
	path := filepath.Join(tmp, "test.txt")
	err := rs.WriteFile(path, []byte("hello"), 0o644)
	require.NoError(s.T(), err)
	data, err := os.ReadFile(path)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "hello", string(data))
}

func (s *DaemonSuite) TestRealSystemRemoveFile() {
	rs := RealSystem{}
	tmp := s.T().TempDir()
	path := filepath.Join(tmp, "test.txt")
	require.NoError(s.T(), os.WriteFile(path, []byte("hello"), 0o644))
	err := rs.RemoveFile(path)
	require.NoError(s.T(), err)
}

func (s *DaemonSuite) TestRealSystemRunCommand() {
	rs := RealSystem{}
	out, err := rs.RunCommand("echo", "hello")
	require.NoError(s.T(), err)
	require.Contains(s.T(), string(out), "hello")
}

func (s *DaemonSuite) TestRealSystemStat() {
	rs := RealSystem{}
	tmp := s.T().TempDir()
	path := filepath.Join(tmp, "test.txt")
	require.NoError(s.T(), os.WriteFile(path, []byte("hello"), 0o644))
	info, err := rs.Stat(path)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "test.txt", info.Name())
}

// --- helpers ---

type fakeFileInfo struct {
	os.FileInfo
}

func (fakeFileInfo) Name() string { return "loop.service" }
