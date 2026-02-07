package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
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
}

func TestDaemonSuite(t *testing.T) {
	suite.Run(t, new(DaemonSuite))
}

func (s *DaemonSuite) SetupTest() {
	s.origGetUID = getUID
	s.origEvalSymlinks = evalSymlinks
	getUID = func() int { return 501 }
	evalSymlinks = func(path string) (string, error) { return path, nil }
}

func (s *DaemonSuite) TearDownTest() {
	getUID = s.origGetUID
	evalSymlinks = s.origEvalSymlinks
}

// --- Start tests ---

func (s *DaemonSuite) TestStartSuccess() {
	sys := new(mockSystem)
	sys.On("Executable").Return("/usr/local/bin/loop", nil)
	sys.On("UserHomeDir").Return("/home/test", nil)
	sys.On("MkdirAll", "/home/test/Library/LaunchAgents", os.FileMode(0o755)).Return(nil)
	sys.On("MkdirAll", "/home/test/.loop/logs", os.FileMode(0o755)).Return(nil)
	sys.On("WriteFile", "/home/test/Library/LaunchAgents/com.loop.agent.plist", mock.Anything, os.FileMode(0o644)).Return(nil)
	sys.On("RunCommand", "launchctl", []string{"bootout", "gui/501", "/home/test/Library/LaunchAgents/com.loop.agent.plist"}).
		Return([]byte(""), nil)
	sys.On("RunCommand", "launchctl", []string{"bootstrap", "gui/501", "/home/test/Library/LaunchAgents/com.loop.agent.plist"}).
		Return([]byte(""), nil)

	err := Start(sys)
	require.NoError(s.T(), err)
	sys.AssertExpectations(s.T())
}

func (s *DaemonSuite) TestStartExecutableError() {
	sys := new(mockSystem)
	sys.On("Executable").Return("", errors.New("exec fail"))

	err := Start(sys)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "resolving executable")
}

func (s *DaemonSuite) TestStartEvalSymlinksError() {
	evalSymlinks = func(_ string) (string, error) { return "", errors.New("symlink fail") }

	sys := new(mockSystem)
	sys.On("Executable").Return("/usr/local/bin/loop", nil)

	err := Start(sys)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "resolving symlinks")
}

func (s *DaemonSuite) TestStartHomeDirError() {
	sys := new(mockSystem)
	sys.On("Executable").Return("/usr/local/bin/loop", nil)
	sys.On("UserHomeDir").Return("", errors.New("home fail"))

	err := Start(sys)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "getting home directory")
}

func (s *DaemonSuite) TestStartMkdirLaunchAgentsError() {
	sys := new(mockSystem)
	sys.On("Executable").Return("/usr/local/bin/loop", nil)
	sys.On("UserHomeDir").Return("/home/test", nil)
	sys.On("MkdirAll", "/home/test/Library/LaunchAgents", os.FileMode(0o755)).Return(errors.New("mkdir fail"))

	err := Start(sys)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "creating LaunchAgents directory")
}

func (s *DaemonSuite) TestStartMkdirLogDirError() {
	sys := new(mockSystem)
	sys.On("Executable").Return("/usr/local/bin/loop", nil)
	sys.On("UserHomeDir").Return("/home/test", nil)
	sys.On("MkdirAll", "/home/test/Library/LaunchAgents", os.FileMode(0o755)).Return(nil)
	sys.On("MkdirAll", "/home/test/.loop/logs", os.FileMode(0o755)).Return(errors.New("logdir fail"))

	err := Start(sys)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "creating log directory")
}

func (s *DaemonSuite) TestStartWriteError() {
	sys := new(mockSystem)
	sys.On("Executable").Return("/usr/local/bin/loop", nil)
	sys.On("UserHomeDir").Return("/home/test", nil)
	sys.On("MkdirAll", mock.Anything, mock.Anything).Return(nil)
	sys.On("WriteFile", mock.Anything, mock.Anything, mock.Anything).Return(errors.New("write fail"))

	err := Start(sys)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "writing plist")
}

func (s *DaemonSuite) TestStartLaunchctlError() {
	sys := new(mockSystem)
	sys.On("Executable").Return("/usr/local/bin/loop", nil)
	sys.On("UserHomeDir").Return("/home/test", nil)
	sys.On("MkdirAll", mock.Anything, mock.Anything).Return(nil)
	sys.On("WriteFile", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	sys.On("RunCommand", "launchctl", mock.Anything).
		Return([]byte("Bootstrap failed: 5: Input/output error"), errors.New("exit 5"))

	err := Start(sys)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "launchctl bootstrap")
}

func (s *DaemonSuite) TestStartAlreadyBootstrapped() {
	sys := new(mockSystem)
	sys.On("Executable").Return("/usr/local/bin/loop", nil)
	sys.On("UserHomeDir").Return("/home/test", nil)
	sys.On("MkdirAll", mock.Anything, mock.Anything).Return(nil)
	sys.On("WriteFile", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	sys.On("RunCommand", "launchctl", mock.Anything).
		Return([]byte("Bootstrap failed: already bootstrapped"), errors.New("exit 5"))

	err := Start(sys)
	require.NoError(s.T(), err)
}

// --- Stop tests ---

func (s *DaemonSuite) TestStopSuccess() {
	sys := new(mockSystem)
	sys.On("UserHomeDir").Return("/home/test", nil)
	sys.On("RunCommand", "launchctl", []string{"bootout", "gui/501", "/home/test/Library/LaunchAgents/com.loop.agent.plist"}).
		Return([]byte(""), nil)
	sys.On("RemoveFile", "/home/test/Library/LaunchAgents/com.loop.agent.plist").Return(nil)

	err := Stop(sys)
	require.NoError(s.T(), err)
	sys.AssertExpectations(s.T())
}

func (s *DaemonSuite) TestStopHomeDirError() {
	sys := new(mockSystem)
	sys.On("UserHomeDir").Return("", errors.New("home fail"))

	err := Stop(sys)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "getting home directory")
}

func (s *DaemonSuite) TestStopLaunchctlError() {
	sys := new(mockSystem)
	sys.On("UserHomeDir").Return("/home/test", nil)
	sys.On("RunCommand", "launchctl", mock.Anything).
		Return([]byte("Bootout failed: something"), errors.New("exit 5"))

	err := Stop(sys)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "launchctl bootout")
}

func (s *DaemonSuite) TestStopAlreadyStopped() {
	sys := new(mockSystem)
	sys.On("UserHomeDir").Return("/home/test", nil)
	sys.On("RunCommand", "launchctl", mock.Anything).
		Return([]byte("Could not find service"), errors.New("exit 3"))
	sys.On("RemoveFile", mock.Anything).Return(os.ErrNotExist)

	err := Stop(sys)
	require.NoError(s.T(), err)
}

func (s *DaemonSuite) TestStopNoSuchFile() {
	sys := new(mockSystem)
	sys.On("UserHomeDir").Return("/home/test", nil)
	sys.On("RunCommand", "launchctl", mock.Anything).
		Return([]byte("No such file or directory"), errors.New("exit 3"))
	sys.On("RemoveFile", mock.Anything).Return(os.ErrNotExist)

	err := Stop(sys)
	require.NoError(s.T(), err)
}

func (s *DaemonSuite) TestStopNotFound() {
	sys := new(mockSystem)
	sys.On("UserHomeDir").Return("/home/test", nil)
	sys.On("RunCommand", "launchctl", mock.Anything).
		Return([]byte("service not found"), errors.New("exit 3"))
	sys.On("RemoveFile", mock.Anything).Return(os.ErrNotExist)

	err := Stop(sys)
	require.NoError(s.T(), err)
}

func (s *DaemonSuite) TestStopRemoveFileError() {
	sys := new(mockSystem)
	sys.On("UserHomeDir").Return("/home/test", nil)
	sys.On("RunCommand", "launchctl", mock.Anything).Return([]byte(""), nil)
	sys.On("RemoveFile", mock.Anything).Return(errors.New("permission denied"))

	err := Stop(sys)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "removing plist")
}

// --- Status tests ---

func (s *DaemonSuite) TestStatusRunning() {
	sys := new(mockSystem)
	sys.On("UserHomeDir").Return("/home/test", nil)
	sys.On("Stat", "/home/test/Library/LaunchAgents/com.loop.agent.plist").Return(fakeFileInfo{}, nil)
	sys.On("RunCommand", "launchctl", []string{"print", "gui/501/com.loop.agent"}).
		Return([]byte("state = running"), nil)

	status, err := Status(sys)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "running", status)
}

func (s *DaemonSuite) TestStatusStopped() {
	sys := new(mockSystem)
	sys.On("UserHomeDir").Return("/home/test", nil)
	sys.On("Stat", "/home/test/Library/LaunchAgents/com.loop.agent.plist").Return(fakeFileInfo{}, nil)
	sys.On("RunCommand", "launchctl", []string{"print", "gui/501/com.loop.agent"}).
		Return([]byte("state = not running"), nil)

	status, err := Status(sys)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "stopped", status)
}

func (s *DaemonSuite) TestStatusStoppedLaunchctlError() {
	sys := new(mockSystem)
	sys.On("UserHomeDir").Return("/home/test", nil)
	sys.On("Stat", "/home/test/Library/LaunchAgents/com.loop.agent.plist").Return(fakeFileInfo{}, nil)
	sys.On("RunCommand", "launchctl", mock.Anything).
		Return([]byte(""), errors.New("exit 3"))

	status, err := Status(sys)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "stopped", status)
}

func (s *DaemonSuite) TestStatusNotInstalled() {
	sys := new(mockSystem)
	sys.On("UserHomeDir").Return("/home/test", nil)
	sys.On("Stat", mock.Anything).Return(nil, os.ErrNotExist)

	status, err := Status(sys)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "not installed", status)
}

func (s *DaemonSuite) TestStatusHomeDirError() {
	sys := new(mockSystem)
	sys.On("UserHomeDir").Return("", errors.New("home fail"))

	_, err := Status(sys)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "getting home directory")
}

func (s *DaemonSuite) TestStatusStatError() {
	sys := new(mockSystem)
	sys.On("UserHomeDir").Return("/home/test", nil)
	sys.On("Stat", mock.Anything).Return(nil, errors.New("stat fail"))

	_, err := Status(sys)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "checking plist")
}

// --- generatePlist test ---

func (s *DaemonSuite) TestGeneratePlist() {
	plist := generatePlist("/usr/local/bin/loop", "/home/test/.loop/logs")
	require.Contains(s.T(), plist, "<string>com.loop.agent</string>")
	require.Contains(s.T(), plist, "<string>/usr/local/bin/loop</string>")
	require.Contains(s.T(), plist, "<string>serve</string>")
	require.Contains(s.T(), plist, "<key>KeepAlive</key>")
	require.Contains(s.T(), plist, "<key>RunAtLoad</key>")
	require.Contains(s.T(), plist, "/home/test/.loop/logs/loop.log")
	require.NotContains(s.T(), plist, "loop.out.log")
	require.NotContains(s.T(), plist, "loop.err.log")
	require.Contains(s.T(), plist, "/opt/homebrew/bin")
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

func (fakeFileInfo) Name() string { return "com.loop.agent.plist" }

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
	sys.On("RemoveFile", "/some/path").Return(errors.New("permission denied"))
	err := removeIfExists(sys, "/some/path")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "removing plist")
}

// --- constants test ---

func (s *DaemonSuite) TestConstants() {
	require.Equal(s.T(), "com.loop.agent", serviceLabel)
	require.True(s.T(), strings.HasSuffix(plistName, ".plist"))
}
