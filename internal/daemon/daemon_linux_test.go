//go:build linux

package daemon

import (
	"errors"
	"os"
	"strings"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// withSystemd stubs a reachable systemd user manager and no detached daemon.
func withSystemd(sys *mockSystem) {
	sys.On("RunCommand", "systemctl", []string{"--user", "show-environment"}).Return([]byte(""), nil).Maybe()
	sys.On("ReadPIDFile", "/home/test/.loop/daemon.pid").Return(nil, os.ErrNotExist).Maybe()
	sys.On("RemoveFile", "/home/test/.loop/daemon.pid").Return(os.ErrNotExist).Maybe()
}

// --- Start tests ---

func (s *DaemonSuite) TestStartSuccess() {
	sys := new(mockSystem)
	withSystemd(sys)
	sys.On("Executable").Return("/usr/local/bin/loop", nil)
	sys.On("EvalSymlinks", "/usr/local/bin/loop").Return("/usr/local/bin/loop", nil)
	sys.On("UserHomeDir").Return("/home/test", nil)
	sys.On("MkdirAll", "/home/test/.config/systemd/user", os.FileMode(0o755)).Return(nil)
	sys.On("MkdirAll", "/home/test/.loop", os.FileMode(0o755)).Return(nil)
	sys.On("Getenv", mock.Anything).Return("")
	sys.On("WriteFile", "/home/test/.config/systemd/user/loop.service", mock.Anything, os.FileMode(0o644)).Return(nil)
	sys.On("RunCommand", "systemctl", []string{"--user", "daemon-reload"}).Return([]byte(""), nil)
	sys.On("RunCommand", "systemctl", []string{"--user", "enable", "--now", "loop"}).Return([]byte(""), nil)
	sys.On("RunCommand", "loginctl", []string{"enable-linger"}).Return([]byte(""), nil)

	err := Start(sys, "/home/test/.loop/loop.log")
	require.NoError(s.T(), err)
	sys.AssertExpectations(s.T())
}

func (s *DaemonSuite) TestStartIgnoresProxyEnv() {
	sys := new(mockSystem)
	withSystemd(sys)
	sys.On("Executable").Return("/usr/local/bin/loop", nil)
	sys.On("EvalSymlinks", "/usr/local/bin/loop").Return("/usr/local/bin/loop", nil)
	sys.On("UserHomeDir").Return("/home/test", nil)
	sys.On("MkdirAll", mock.Anything, mock.Anything).Return(nil)
	sys.On("Getenv", "HTTP_PROXY").Return("http://127.0.0.1:3128").Maybe()
	sys.On("Getenv", "HTTPS_PROXY").Return("http://127.0.0.1:3128").Maybe()
	sys.On("Getenv", mock.Anything).Return("")
	// The shell's proxy must not be pinned into the unit: the daemon
	// resolves its proxy from config per request.
	sys.On("WriteFile", "/home/test/.config/systemd/user/loop.service", mock.MatchedBy(func(data []byte) bool {
		return !strings.Contains(strings.ToUpper(string(data)), "PROXY")
	}), os.FileMode(0o644)).Return(nil)
	sys.On("RunCommand", "systemctl", mock.Anything).Return([]byte(""), nil)
	sys.On("RunCommand", "loginctl", mock.Anything).Return([]byte(""), nil)

	err := Start(sys, "/home/test/.loop/loop.log")
	require.NoError(s.T(), err)
	sys.AssertExpectations(s.T())
}

func (s *DaemonSuite) TestStartWithShellEnv() {
	sys := new(mockSystem)
	withSystemd(sys)
	sys.On("Executable").Return("/usr/local/bin/loop", nil)
	sys.On("EvalSymlinks", "/usr/local/bin/loop").Return("/usr/local/bin/loop", nil)
	sys.On("UserHomeDir").Return("/home/test", nil)
	sys.On("MkdirAll", mock.Anything, mock.Anything).Return(nil)
	sys.On("Getenv", "SHELL").Return("/bin/zsh")
	sys.On("Getenv", mock.Anything).Return("")
	sys.On("WriteFile", "/home/test/.config/systemd/user/loop.service", mock.MatchedBy(func(data []byte) bool {
		return strings.Contains(string(data), "Environment=SHELL=/bin/zsh")
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
	sys := new(mockSystem)
	sys.On("Executable").Return("/usr/local/bin/loop", nil)
	sys.On("EvalSymlinks", "/usr/local/bin/loop").Return("", errors.New("symlink fail"))

	err := Start(sys, "/home/test/.loop/loop.log")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "resolving symlinks")
}

func (s *DaemonSuite) TestStartHomeDirError() {
	sys := new(mockSystem)
	sys.On("Executable").Return("/usr/local/bin/loop", nil)
	sys.On("EvalSymlinks", "/usr/local/bin/loop").Return("/usr/local/bin/loop", nil)
	sys.On("UserHomeDir").Return("", errors.New("home fail"))

	err := Start(sys, "/home/test/.loop/loop.log")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "getting home directory")
}

func (s *DaemonSuite) TestStartMkdirUnitDirError() {
	sys := new(mockSystem)
	withSystemd(sys)
	sys.On("Executable").Return("/usr/local/bin/loop", nil)
	sys.On("EvalSymlinks", "/usr/local/bin/loop").Return("/usr/local/bin/loop", nil)
	sys.On("UserHomeDir").Return("/home/test", nil)
	sys.On("MkdirAll", "/home/test/.config/systemd/user", os.FileMode(0o755)).Return(errors.New("mkdir fail"))

	err := Start(sys, "/home/test/.loop/loop.log")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "creating systemd user unit directory")
}

func (s *DaemonSuite) TestStartMkdirLogDirError() {
	sys := new(mockSystem)
	withSystemd(sys)
	sys.On("Executable").Return("/usr/local/bin/loop", nil)
	sys.On("EvalSymlinks", "/usr/local/bin/loop").Return("/usr/local/bin/loop", nil)
	sys.On("UserHomeDir").Return("/home/test", nil)
	sys.On("MkdirAll", "/home/test/.config/systemd/user", os.FileMode(0o755)).Return(nil)
	sys.On("MkdirAll", "/home/test/.loop", os.FileMode(0o755)).Return(errors.New("logdir fail"))

	err := Start(sys, "/home/test/.loop/loop.log")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "creating log directory")
}

func (s *DaemonSuite) TestStartWriteError() {
	sys := new(mockSystem)
	withSystemd(sys)
	sys.On("Executable").Return("/usr/local/bin/loop", nil)
	sys.On("EvalSymlinks", "/usr/local/bin/loop").Return("/usr/local/bin/loop", nil)
	sys.On("UserHomeDir").Return("/home/test", nil)
	sys.On("MkdirAll", mock.Anything, mock.Anything).Return(nil)
	sys.On("Getenv", mock.Anything).Return("")
	sys.On("WriteFile", mock.Anything, mock.Anything, mock.Anything).Return(errors.New("write fail"))

	err := Start(sys, "/home/test/.loop/loop.log")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "writing unit file")
}

func (s *DaemonSuite) TestStartDaemonReloadError() {
	sys := new(mockSystem)
	withSystemd(sys)
	sys.On("Executable").Return("/usr/local/bin/loop", nil)
	sys.On("EvalSymlinks", "/usr/local/bin/loop").Return("/usr/local/bin/loop", nil)
	sys.On("UserHomeDir").Return("/home/test", nil)
	sys.On("MkdirAll", mock.Anything, mock.Anything).Return(nil)
	sys.On("Getenv", mock.Anything).Return("")
	sys.On("WriteFile", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	sys.On("RunCommand", "systemctl", []string{"--user", "daemon-reload"}).
		Return([]byte("Failed to connect to bus"), errors.New("exit 1"))

	err := Start(sys, "/home/test/.loop/loop.log")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "systemctl daemon-reload")
}

func (s *DaemonSuite) TestStartEnableError() {
	sys := new(mockSystem)
	withSystemd(sys)
	sys.On("Executable").Return("/usr/local/bin/loop", nil)
	sys.On("EvalSymlinks", "/usr/local/bin/loop").Return("/usr/local/bin/loop", nil)
	sys.On("UserHomeDir").Return("/home/test", nil)
	sys.On("MkdirAll", mock.Anything, mock.Anything).Return(nil)
	sys.On("Getenv", mock.Anything).Return("")
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
	withSystemd(sys)
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
	withSystemd(sys)
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
	withSystemd(sys)
	sys.On("UserHomeDir").Return("/home/test", nil)
	sys.On("RunCommand", "systemctl", []string{"--user", "disable", "--now", "loop"}).
		Return([]byte("Failed to connect to bus"), errors.New("exit 1"))

	err := Stop(sys)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "systemctl disable")
}

func (s *DaemonSuite) TestStopRemoveFileError() {
	sys := new(mockSystem)
	withSystemd(sys)
	sys.On("UserHomeDir").Return("/home/test", nil)
	sys.On("RunCommand", "systemctl", []string{"--user", "disable", "--now", "loop"}).Return([]byte(""), nil)
	sys.On("RemoveFile", mock.Anything).Return(errors.New("permission denied"))

	err := Stop(sys)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "removing unit file")
}

func (s *DaemonSuite) TestStopDaemonReloadError() {
	sys := new(mockSystem)
	withSystemd(sys)
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
	withSystemd(sys)
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
	withSystemd(sys)
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
	withSystemd(sys)
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
	withSystemd(sys)
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
	withSystemd(sys)
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

// --- detached fallback (no systemd user manager) ---

const (
	testPIDFile = "/home/test/.loop/daemon.pid"
)

var serveCmdline = []byte("/usr/local/bin/loop\x00serve\x00")

// withoutSystemd stubs an unreachable systemd user manager.
func withoutSystemd(sys *mockSystem) {
	sys.On("RunCommand", "systemctl", []string{"--user", "show-environment"}).
		Return([]byte("Failed to connect to bus: Connection refused"), errors.New("exit 1"))
}

func newDetachedStartSystem() *mockSystem {
	sys := new(mockSystem)
	sys.On("Executable").Return("/usr/local/bin/loop", nil)
	sys.On("EvalSymlinks", "/usr/local/bin/loop").Return("/usr/local/bin/loop", nil)
	sys.On("UserHomeDir").Return("/home/test", nil)
	withoutSystemd(sys)
	return sys
}

func (s *DaemonSuite) TestStartDetachedSuccess() {
	sys := newDetachedStartSystem()
	sys.On("ReadPIDFile", testPIDFile).Return(nil, os.ErrNotExist)
	sys.On("MkdirAll", "/var/log/loop", os.FileMode(0o755)).Return(nil)
	sys.On("MkdirAll", "/home/test/.loop", os.FileMode(0o755)).Return(nil)
	sys.On("StartDetached", "/usr/local/bin/loop", []string{"serve"}, "/var/log/loop/loop.log").Return(42, nil)
	sys.On("WriteFile", testPIDFile, []byte("42\n"), os.FileMode(0o644)).Return(nil)

	require.NoError(s.T(), Start(sys, "/var/log/loop/loop.log"))
	sys.AssertExpectations(s.T())
	sys.AssertNotCalled(s.T(), "WriteFile", "/home/test/.config/systemd/user/loop.service", mock.Anything, mock.Anything)
}

func (s *DaemonSuite) TestStartDetachedAlreadyRunning() {
	sys := newDetachedStartSystem()
	sys.On("ReadPIDFile", testPIDFile).Return([]byte("42\n"), nil)
	sys.On("ProcCmdline", 42).Return(serveCmdline, nil)

	require.NoError(s.T(), Start(sys, "/home/test/.loop/loop.log"))
	sys.AssertNotCalled(s.T(), "StartDetached", mock.Anything, mock.Anything, mock.Anything)
}

func (s *DaemonSuite) TestStartDetachedErrors() {
	tests := []struct {
		name    string
		setup   func(sys *mockSystem)
		wantErr string
	}{
		{
			name: "log dir",
			setup: func(sys *mockSystem) {
				sys.On("MkdirAll", "/var/log/loop", mock.Anything).Return(errors.New("mkdir fail"))
			},
			wantErr: "creating log directory",
		},
		{
			name: "pid dir",
			setup: func(sys *mockSystem) {
				sys.On("MkdirAll", "/var/log/loop", mock.Anything).Return(nil)
				sys.On("MkdirAll", "/home/test/.loop", mock.Anything).Return(errors.New("mkdir fail"))
			},
			wantErr: "creating pid directory",
		},
		{
			name: "start",
			setup: func(sys *mockSystem) {
				sys.On("MkdirAll", mock.Anything, mock.Anything).Return(nil)
				sys.On("StartDetached", mock.Anything, mock.Anything, mock.Anything).Return(0, errors.New("exec fail"))
			},
			wantErr: "starting daemon",
		},
		{
			name: "pid file",
			setup: func(sys *mockSystem) {
				sys.On("MkdirAll", mock.Anything, mock.Anything).Return(nil)
				sys.On("StartDetached", mock.Anything, mock.Anything, mock.Anything).Return(42, nil)
				sys.On("WriteFile", testPIDFile, mock.Anything, mock.Anything).Return(errors.New("disk full"))
				sys.On("Terminate", 42).Return(nil).Once()
			},
			wantErr: "writing pid file",
		},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			sys := newDetachedStartSystem()
			sys.On("ReadPIDFile", testPIDFile).Return(nil, os.ErrNotExist)
			tt.setup(sys)

			err := Start(sys, "/var/log/loop/loop.log")
			require.ErrorContains(s.T(), err, tt.wantErr)
			sys.AssertExpectations(s.T())
		})
	}
}

func (s *DaemonSuite) TestStopDetachedSuccess() {
	sys := new(mockSystem)
	sys.On("UserHomeDir").Return("/home/test", nil)
	sys.On("ReadPIDFile", testPIDFile).Return([]byte("42\n"), nil)
	sys.On("ProcCmdline", 42).Return(serveCmdline, nil).Twice()
	sys.On("ProcCmdline", 42).Return(nil, os.ErrNotExist).Once()
	sys.On("Terminate", 42).Return(nil).Once()
	sys.On("Sleep", stopPollInterval).Return().Once()
	sys.On("RemoveFile", testPIDFile).Return(nil).Once()
	withoutSystemd(sys)

	require.NoError(s.T(), Stop(sys))
	sys.AssertExpectations(s.T())
	sys.AssertNotCalled(s.T(), "RunCommand", "systemctl", []string{"--user", "disable", "--now", "loop"})
}

func (s *DaemonSuite) TestStopNoDaemonWithoutSystemd() {
	sys := new(mockSystem)
	sys.On("UserHomeDir").Return("/home/test", nil)
	sys.On("ReadPIDFile", testPIDFile).Return(nil, os.ErrNotExist)
	sys.On("RemoveFile", testPIDFile).Return(os.ErrNotExist)
	withoutSystemd(sys)

	require.NoError(s.T(), Stop(sys))
	sys.AssertNotCalled(s.T(), "Terminate", mock.Anything)
}

func (s *DaemonSuite) TestStopDetachedErrors() {
	tests := []struct {
		name    string
		setup   func(sys *mockSystem)
		wantErr string
	}{
		{
			name: "terminate",
			setup: func(sys *mockSystem) {
				sys.On("ProcCmdline", 42).Return(serveCmdline, nil)
				sys.On("Terminate", 42).Return(errors.New("operation not permitted"))
			},
			wantErr: "stopping daemon",
		},
		{
			name: "never exits",
			setup: func(sys *mockSystem) {
				sys.On("ProcCmdline", 42).Return(serveCmdline, nil)
				sys.On("Terminate", 42).Return(nil)
				sys.On("Sleep", stopPollInterval).Return().Times(stopPolls)
			},
			wantErr: "daemon (pid 42) did not exit",
		},
		{
			name: "remove pid file",
			setup: func(sys *mockSystem) {
				sys.On("ProcCmdline", 42).Return(nil, os.ErrNotExist)
				sys.On("RemoveFile", testPIDFile).Return(errors.New("permission denied"))
			},
			wantErr: "removing pid file",
		},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			sys := new(mockSystem)
			sys.On("UserHomeDir").Return("/home/test", nil)
			sys.On("ReadPIDFile", testPIDFile).Return([]byte("42"), nil)
			tt.setup(sys)

			err := Stop(sys)
			require.ErrorContains(s.T(), err, tt.wantErr)
			sys.AssertExpectations(s.T())
		})
	}
}

func (s *DaemonSuite) TestStatusDetachedRunning() {
	sys := new(mockSystem)
	sys.On("UserHomeDir").Return("/home/test", nil)
	sys.On("ReadPIDFile", testPIDFile).Return([]byte("42\n"), nil)
	sys.On("ProcCmdline", 42).Return(serveCmdline, nil)

	status, err := Status(sys)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "running", status)
	sys.AssertNotCalled(s.T(), "Stat", mock.Anything)
}

func (s *DaemonSuite) TestRunningPID() {
	tests := []struct {
		name    string
		pidFile []byte
		cmdline []byte
		wantPID int
		wantOK  bool
	}{
		{name: "serve", pidFile: []byte("42\n"), cmdline: serveCmdline, wantPID: 42, wantOK: true},
		{name: "recycled pid", pidFile: []byte("42"), cmdline: []byte("/usr/bin/vim\x00notes\x00"), wantPID: 42},
		{name: "no args", pidFile: []byte("42"), cmdline: []byte("/usr/local/bin/loop\x00"), wantPID: 42},
		{name: "exited", pidFile: []byte("42"), wantPID: 42},
		{name: "garbage", pidFile: []byte("abc")},
		{name: "zero", pidFile: []byte("0")},
		{name: "negative", pidFile: []byte("-1")},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			sys := new(mockSystem)
			sys.On("ReadPIDFile", testPIDFile).Return(tt.pidFile, nil)
			if tt.cmdline != nil {
				sys.On("ProcCmdline", 42).Return(tt.cmdline, nil)
			} else {
				sys.On("ProcCmdline", 42).Return(nil, os.ErrNotExist).Maybe()
			}

			pid, ok := runningPID(sys, testPIDFile)
			require.Equal(s.T(), tt.wantPID, pid)
			require.Equal(s.T(), tt.wantOK, ok)
		})
	}
}

func (s *DaemonSuite) TestRealSystemProcCmdline() {
	cmdline, err := RealSystem{}.ProcCmdline(os.Getpid())
	require.NoError(s.T(), err)
	require.Contains(s.T(), string(cmdline), "\x00")

	_, err = RealSystem{}.ProcCmdline(1<<22 + 1)
	require.Error(s.T(), err)
}
