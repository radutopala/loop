//go:build linux

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
	sys.On("MkdirAll", "/home/test/.config/systemd/user", os.FileMode(0o755)).Return(nil)
	sys.On("MkdirAll", "/home/test/.loop", os.FileMode(0o755)).Return(nil)
	sys.On("WriteFile", "/home/test/.config/systemd/user/loop.service", mock.Anything, os.FileMode(0o644)).Return(nil)
	sys.On("RunCommand", "systemctl", []string{"--user", "daemon-reload"}).Return([]byte(""), nil)
	sys.On("RunCommand", "systemctl", []string{"--user", "enable", "--now", "loop"}).Return([]byte(""), nil)
	sys.On("RunCommand", "loginctl", []string{"enable-linger"}).Return([]byte(""), nil)

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
	sys.On("WriteFile", "/home/test/.config/systemd/user/loop.service", mock.MatchedBy(func(data []byte) bool {
		s := string(data)
		return strings.Contains(s, "Environment=HTTP_PROXY=http://127.0.0.1:3128") &&
			strings.Contains(s, "Environment=HTTPS_PROXY=http://127.0.0.1:3128")
	}), os.FileMode(0o644)).Return(nil)
	sys.On("RunCommand", "systemctl", mock.Anything).Return([]byte(""), nil)
	sys.On("RunCommand", "loginctl", mock.Anything).Return([]byte(""), nil)

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

func (s *DaemonSuite) TestStartMkdirUnitDirError() {
	sys := new(mockSystem)
	sys.On("Executable").Return("/usr/local/bin/loop", nil)
	sys.On("UserHomeDir").Return("/home/test", nil)
	sys.On("MkdirAll", "/home/test/.config/systemd/user", os.FileMode(0o755)).Return(errors.New("mkdir fail"))

	err := Start(sys, "/home/test/.loop/loop.log")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "creating systemd user unit directory")
}

func (s *DaemonSuite) TestStartMkdirLogDirError() {
	sys := new(mockSystem)
	sys.On("Executable").Return("/usr/local/bin/loop", nil)
	sys.On("UserHomeDir").Return("/home/test", nil)
	sys.On("MkdirAll", "/home/test/.config/systemd/user", os.FileMode(0o755)).Return(nil)
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
	require.Contains(s.T(), err.Error(), "writing unit file")
}

func (s *DaemonSuite) TestStartDaemonReloadError() {
	sys := new(mockSystem)
	sys.On("Executable").Return("/usr/local/bin/loop", nil)
	sys.On("UserHomeDir").Return("/home/test", nil)
	sys.On("MkdirAll", mock.Anything, mock.Anything).Return(nil)
	sys.On("WriteFile", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	sys.On("RunCommand", "systemctl", []string{"--user", "daemon-reload"}).
		Return([]byte("Failed to connect to bus"), errors.New("exit 1"))

	err := Start(sys, "/home/test/.loop/loop.log")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "systemctl daemon-reload")
}

func (s *DaemonSuite) TestStartEnableError() {
	sys := new(mockSystem)
	sys.On("Executable").Return("/usr/local/bin/loop", nil)
	sys.On("UserHomeDir").Return("/home/test", nil)
	sys.On("MkdirAll", mock.Anything, mock.Anything).Return(nil)
	sys.On("WriteFile", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	sys.On("RunCommand", "systemctl", []string{"--user", "daemon-reload"}).Return([]byte(""), nil)
	sys.On("RunCommand", "systemctl", []string{"--user", "enable", "--now", "loop"}).
		Return([]byte("Failed to enable unit"), errors.New("exit 1"))

	err := Start(sys, "/home/test/.loop/loop.log")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "systemctl enable")
}

// --- Stop tests ---

func (s *DaemonSuite) TestStopSuccess() {
	sys := new(mockSystem)
	sys.On("UserHomeDir").Return("/home/test", nil)
	sys.On("RunCommand", "systemctl", []string{"--user", "disable", "--now", "loop"}).Return([]byte(""), nil)
	sys.On("RemoveFile", "/home/test/.config/systemd/user/loop.service").Return(nil)
	sys.On("RunCommand", "systemctl", []string{"--user", "daemon-reload"}).Return([]byte(""), nil)
	sys.On("RunCommand", "loginctl", []string{"disable-linger"}).Return([]byte(""), nil)

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

func (s *DaemonSuite) TestStopNotLoaded() {
	sys := new(mockSystem)
	sys.On("UserHomeDir").Return("/home/test", nil)
	sys.On("RunCommand", "systemctl", []string{"--user", "disable", "--now", "loop"}).
		Return([]byte("Unit loop.service is not loaded"), errors.New("exit 1"))
	sys.On("RemoveFile", mock.Anything).Return(os.ErrNotExist)
	sys.On("RunCommand", "systemctl", []string{"--user", "daemon-reload"}).Return([]byte(""), nil)
	sys.On("RunCommand", "loginctl", []string{"disable-linger"}).Return([]byte(""), nil)

	err := Stop(sys)
	require.NoError(s.T(), err)
}

func (s *DaemonSuite) TestStopDisableError() {
	sys := new(mockSystem)
	sys.On("UserHomeDir").Return("/home/test", nil)
	sys.On("RunCommand", "systemctl", []string{"--user", "disable", "--now", "loop"}).
		Return([]byte("Failed to connect to bus"), errors.New("exit 1"))

	err := Stop(sys)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "systemctl disable")
}

func (s *DaemonSuite) TestStopRemoveFileError() {
	sys := new(mockSystem)
	sys.On("UserHomeDir").Return("/home/test", nil)
	sys.On("RunCommand", "systemctl", []string{"--user", "disable", "--now", "loop"}).Return([]byte(""), nil)
	sys.On("RemoveFile", mock.Anything).Return(errors.New("permission denied"))

	err := Stop(sys)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "removing unit file")
}

func (s *DaemonSuite) TestStopDaemonReloadError() {
	sys := new(mockSystem)
	sys.On("UserHomeDir").Return("/home/test", nil)
	sys.On("RunCommand", "systemctl", []string{"--user", "disable", "--now", "loop"}).Return([]byte(""), nil)
	sys.On("RemoveFile", mock.Anything).Return(nil)
	sys.On("RunCommand", "systemctl", []string{"--user", "daemon-reload"}).
		Return([]byte("Failed to connect to bus"), errors.New("exit 1"))

	err := Stop(sys)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "systemctl daemon-reload")
}

// --- Status tests ---

func (s *DaemonSuite) TestStatusRunning() {
	sys := new(mockSystem)
	sys.On("UserHomeDir").Return("/home/test", nil)
	sys.On("Stat", "/home/test/.config/systemd/user/loop.service").Return(fakeFileInfo{}, nil)
	sys.On("RunCommand", "systemctl", []string{"--user", "is-active", "loop"}).
		Return([]byte("active\n"), nil)

	status, err := Status(sys)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "running", status)
}

func (s *DaemonSuite) TestStatusStopped() {
	sys := new(mockSystem)
	sys.On("UserHomeDir").Return("/home/test", nil)
	sys.On("Stat", "/home/test/.config/systemd/user/loop.service").Return(fakeFileInfo{}, nil)
	sys.On("RunCommand", "systemctl", []string{"--user", "is-active", "loop"}).
		Return([]byte("inactive\n"), errors.New("exit 3"))

	status, err := Status(sys)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "stopped", status)
}

func (s *DaemonSuite) TestStatusFailed() {
	sys := new(mockSystem)
	sys.On("UserHomeDir").Return("/home/test", nil)
	sys.On("Stat", "/home/test/.config/systemd/user/loop.service").Return(fakeFileInfo{}, nil)
	sys.On("RunCommand", "systemctl", []string{"--user", "is-active", "loop"}).
		Return([]byte("failed\n"), errors.New("exit 3"))

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
	require.Contains(s.T(), err.Error(), "checking unit file")
}

// --- generateUnit test ---

func (s *DaemonSuite) TestGenerateUnit() {
	unit := generateUnit("/usr/local/bin/loop", "/home/test/.loop/loop.log", nil)
	require.Contains(s.T(), unit, "ExecStart=/usr/local/bin/loop serve")
	require.Contains(s.T(), unit, "Restart=always")
	require.Contains(s.T(), unit, "StandardOutput=append:/home/test/.loop/loop.log")
	require.Contains(s.T(), unit, "StandardError=append:/home/test/.loop/loop.log")
	require.Contains(s.T(), unit, "WantedBy=default.target")
	require.Contains(s.T(), unit, "Environment=PATH=")
}

func (s *DaemonSuite) TestGenerateUnitWithProxyEnv() {
	env := map[string]string{
		"HTTP_PROXY":  "http://127.0.0.1:3128",
		"HTTPS_PROXY": "http://127.0.0.1:3128",
	}
	unit := generateUnit("/usr/local/bin/loop", "/home/test/.loop/loop.log", env)
	require.Contains(s.T(), unit, "Environment=HTTP_PROXY=http://127.0.0.1:3128")
	require.Contains(s.T(), unit, "Environment=HTTPS_PROXY=http://127.0.0.1:3128")
	require.Contains(s.T(), unit, "Environment=PATH=")
}

// --- constants test ---

func (s *DaemonSuite) TestConstants() {
	require.Equal(s.T(), "loop", serviceLabel)
	require.True(s.T(), strings.HasSuffix(unitName, ".service"))
}
