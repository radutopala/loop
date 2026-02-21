//go:build darwin

package daemon

import (
	"errors"
	"os"
	"strings"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// --- Start tests ---

func (s *DaemonSuite) TestStartSuccess() {
	sys := new(mockSystem)
	sys.On("Executable").Return("/usr/local/bin/loop", nil)
	sys.On("UserHomeDir").Return("/home/test", nil)
	sys.On("MkdirAll", "/home/test/Library/LaunchAgents", os.FileMode(0o755)).Return(nil)
	sys.On("MkdirAll", "/home/test/.loop", os.FileMode(0o755)).Return(nil)
	sys.On("WriteFile", "/home/test/Library/LaunchAgents/com.loop.agent.plist", mock.Anything, os.FileMode(0o644)).Return(nil)
	sys.On("RunCommand", "launchctl", []string{"bootout", "gui/501", "/home/test/Library/LaunchAgents/com.loop.agent.plist"}).
		Return([]byte(""), nil)
	sys.On("RunCommand", "launchctl", []string{"bootstrap", "gui/501", "/home/test/Library/LaunchAgents/com.loop.agent.plist"}).
		Return([]byte(""), nil)

	err := Start(sys, "/home/test/.loop/loop.log")
	require.NoError(s.T(), err)
	sys.AssertExpectations(s.T())
}

func (s *DaemonSuite) TestStartWithProxyEnv() {
	osGetenv = func(key string) string {
		switch key {
		case "HTTP_PROXY":
			return "http://127.0.0.1:3128"
		case "HTTPS_PROXY":
			return "http://127.0.0.1:3128"
		default:
			return ""
		}
	}

	sys := new(mockSystem)
	sys.On("Executable").Return("/usr/local/bin/loop", nil)
	sys.On("UserHomeDir").Return("/home/test", nil)
	sys.On("MkdirAll", mock.Anything, mock.Anything).Return(nil)
	sys.On("WriteFile", "/home/test/Library/LaunchAgents/com.loop.agent.plist", mock.MatchedBy(func(data []byte) bool {
		s := string(data)
		return strings.Contains(s, "<key>HTTP_PROXY</key>") &&
			strings.Contains(s, "<key>HTTPS_PROXY</key>") &&
			strings.Contains(s, "http://127.0.0.1:3128")
	}), os.FileMode(0o644)).Return(nil)
	sys.On("RunCommand", "launchctl", mock.Anything).Return([]byte(""), nil)

	err := Start(sys, "/home/test/.loop/loop.log")
	require.NoError(s.T(), err)
	sys.AssertExpectations(s.T())
}

func (s *DaemonSuite) TestStartExecutableError() {
	sys := new(mockSystem)
	sys.On("Executable").Return("", errors.New("exec fail"))

	err := Start(sys, "/home/test/.loop/loop.log")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "resolving executable")
}

func (s *DaemonSuite) TestStartEvalSymlinksError() {
	evalSymlinks = func(_ string) (string, error) { return "", errors.New("symlink fail") }

	sys := new(mockSystem)
	sys.On("Executable").Return("/usr/local/bin/loop", nil)

	err := Start(sys, "/home/test/.loop/loop.log")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "resolving symlinks")
}

func (s *DaemonSuite) TestStartHomeDirError() {
	sys := new(mockSystem)
	sys.On("Executable").Return("/usr/local/bin/loop", nil)
	sys.On("UserHomeDir").Return("", errors.New("home fail"))

	err := Start(sys, "/home/test/.loop/loop.log")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "getting home directory")
}

func (s *DaemonSuite) TestStartMkdirLaunchAgentsError() {
	sys := new(mockSystem)
	sys.On("Executable").Return("/usr/local/bin/loop", nil)
	sys.On("UserHomeDir").Return("/home/test", nil)
	sys.On("MkdirAll", "/home/test/Library/LaunchAgents", os.FileMode(0o755)).Return(errors.New("mkdir fail"))

	err := Start(sys, "/home/test/.loop/loop.log")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "creating LaunchAgents directory")
}

func (s *DaemonSuite) TestStartMkdirLogDirError() {
	sys := new(mockSystem)
	sys.On("Executable").Return("/usr/local/bin/loop", nil)
	sys.On("UserHomeDir").Return("/home/test", nil)
	sys.On("MkdirAll", "/home/test/Library/LaunchAgents", os.FileMode(0o755)).Return(nil)
	sys.On("MkdirAll", "/home/test/.loop", os.FileMode(0o755)).Return(errors.New("logdir fail"))

	err := Start(sys, "/home/test/.loop/loop.log")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "creating log directory")
}

func (s *DaemonSuite) TestStartWriteError() {
	sys := new(mockSystem)
	sys.On("Executable").Return("/usr/local/bin/loop", nil)
	sys.On("UserHomeDir").Return("/home/test", nil)
	sys.On("MkdirAll", mock.Anything, mock.Anything).Return(nil)
	sys.On("WriteFile", mock.Anything, mock.Anything, mock.Anything).Return(errors.New("write fail"))

	err := Start(sys, "/home/test/.loop/loop.log")
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

	err := Start(sys, "/home/test/.loop/loop.log")
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

	err := Start(sys, "/home/test/.loop/loop.log")
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

func (s *DaemonSuite) TestStopInputOutputError() {
	sys := new(mockSystem)
	sys.On("UserHomeDir").Return("/home/test", nil)
	sys.On("RunCommand", "launchctl", mock.Anything).
		Return([]byte("Boot-out failed: 5: Input/output error"), errors.New("exit 5"))
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
	require.Contains(s.T(), err.Error(), "removing unit file")
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
	plist := generatePlist("/usr/local/bin/loop", "/home/test/.loop/loop.log", nil)
	require.Contains(s.T(), plist, "<string>com.loop.agent</string>")
	require.Contains(s.T(), plist, "<string>/usr/local/bin/loop</string>")
	require.Contains(s.T(), plist, "<string>serve</string>")
	require.Contains(s.T(), plist, "<key>KeepAlive</key>")
	require.Contains(s.T(), plist, "<key>RunAtLoad</key>")
	require.Contains(s.T(), plist, "/home/test/.loop/loop.log")
	require.NotContains(s.T(), plist, "loop.out.log")
	require.NotContains(s.T(), plist, "loop.err.log")
	require.Contains(s.T(), plist, "/opt/homebrew/bin")
}

func (s *DaemonSuite) TestGeneratePlistWithProxyEnv() {
	env := map[string]string{
		"HTTP_PROXY":  "http://127.0.0.1:3128",
		"HTTPS_PROXY": "http://127.0.0.1:3128",
	}
	plist := generatePlist("/usr/local/bin/loop", "/home/test/.loop/loop.log", env)
	require.Contains(s.T(), plist, "<key>HTTP_PROXY</key>")
	require.Contains(s.T(), plist, "<string>http://127.0.0.1:3128</string>")
	require.Contains(s.T(), plist, "<key>HTTPS_PROXY</key>")
	require.Contains(s.T(), plist, "<key>PATH</key>")
}

// --- constants test ---

func (s *DaemonSuite) TestConstants() {
	require.Equal(s.T(), "com.loop.agent", serviceLabel)
	require.True(s.T(), strings.HasSuffix(plistName, ".plist"))
}
