//go:build windows

package daemon

import (
	"errors"
	"strings"

	"github.com/stretchr/testify/require"
)

// --- Start tests ---

func (s *DaemonSuite) TestStartSuccess() {
	sys := new(mockSystem)
	sys.On("Executable").Return(`C:\loop\loop.exe`, nil)
	sys.On("EvalSymlinks", `C:\loop\loop.exe`).Return(`C:\loop\loop.exe`, nil)
	sys.On("RunCommand", "sc.exe", []string{"create", serviceName, "binpath=", `"C:\loop\loop.exe" serve`, "start=", "auto", "displayname=", "Loop Agent Daemon"}).
		Return([]byte(""), nil)
	sys.On("RunCommand", "sc.exe", []string{"start", serviceName}).
		Return([]byte(""), nil)

	err := Start(sys, `C:\loop\loop.log`)
	require.NoError(s.T(), err)
	sys.AssertExpectations(s.T())
}

func (s *DaemonSuite) TestStartAlreadyExists() {
	sys := new(mockSystem)
	sys.On("Executable").Return(`C:\loop\loop.exe`, nil)
	sys.On("EvalSymlinks", `C:\loop\loop.exe`).Return(`C:\loop\loop.exe`, nil)
	sys.On("RunCommand", "sc.exe", []string{"create", serviceName, "binpath=", `"C:\loop\loop.exe" serve`, "start=", "auto", "displayname=", "Loop Agent Daemon"}).
		Return([]byte("The specified service already exists."), errors.New("exit 1"))
	sys.On("RunCommand", "sc.exe", []string{"start", serviceName}).
		Return([]byte(""), nil)

	err := Start(sys, `C:\loop\loop.log`)
	require.NoError(s.T(), err)
}

func (s *DaemonSuite) TestStartAlreadyRunning() {
	sys := new(mockSystem)
	sys.On("Executable").Return(`C:\loop\loop.exe`, nil)
	sys.On("EvalSymlinks", `C:\loop\loop.exe`).Return(`C:\loop\loop.exe`, nil)
	sys.On("RunCommand", "sc.exe", []string{"create", serviceName, "binpath=", `"C:\loop\loop.exe" serve`, "start=", "auto", "displayname=", "Loop Agent Daemon"}).
		Return([]byte(""), nil)
	sys.On("RunCommand", "sc.exe", []string{"start", serviceName}).
		Return([]byte("An instance of the service is already running."), errors.New("exit 1"))

	err := Start(sys, `C:\loop\loop.log`)
	require.NoError(s.T(), err)
}

func (s *DaemonSuite) TestStartExecutableError() {
	sys := new(mockSystem)
	sys.On("Executable").Return("", errors.New("exec fail"))

	err := Start(sys, `C:\loop\loop.log`)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "resolving executable")
}

func (s *DaemonSuite) TestStartEvalSymlinksError() {
	sys := new(mockSystem)
	sys.On("Executable").Return(`C:\loop\loop.exe`, nil)
	sys.On("EvalSymlinks", `C:\loop\loop.exe`).Return("", errors.New("symlink fail"))

	err := Start(sys, `C:\loop\loop.log`)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "resolving symlinks")
}

func (s *DaemonSuite) TestStartCreateError() {
	sys := new(mockSystem)
	sys.On("Executable").Return(`C:\loop\loop.exe`, nil)
	sys.On("EvalSymlinks", `C:\loop\loop.exe`).Return(`C:\loop\loop.exe`, nil)
	sys.On("RunCommand", "sc.exe", []string{"create", serviceName, "binpath=", `"C:\loop\loop.exe" serve`, "start=", "auto", "displayname=", "Loop Agent Daemon"}).
		Return([]byte("Access is denied."), errors.New("exit 5"))

	err := Start(sys, `C:\loop\loop.log`)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "sc create")
}

func (s *DaemonSuite) TestStartStartError() {
	sys := new(mockSystem)
	sys.On("Executable").Return(`C:\loop\loop.exe`, nil)
	sys.On("EvalSymlinks", `C:\loop\loop.exe`).Return(`C:\loop\loop.exe`, nil)
	sys.On("RunCommand", "sc.exe", []string{"create", serviceName, "binpath=", `"C:\loop\loop.exe" serve`, "start=", "auto", "displayname=", "Loop Agent Daemon"}).
		Return([]byte(""), nil)
	sys.On("RunCommand", "sc.exe", []string{"start", serviceName}).
		Return([]byte("The service did not start due to a logon failure."), errors.New("exit 1"))

	err := Start(sys, `C:\loop\loop.log`)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "sc start")
}

// --- Stop tests ---

func (s *DaemonSuite) TestStopSuccess() {
	sys := new(mockSystem)
	sys.On("RunCommand", "sc.exe", []string{"stop", serviceName}).Return([]byte(""), nil)
	sys.On("RunCommand", "sc.exe", []string{"delete", serviceName}).Return([]byte(""), nil)

	err := Stop(sys)
	require.NoError(s.T(), err)
	sys.AssertExpectations(s.T())
}

func (s *DaemonSuite) TestStopNotInstalled() {
	sys := new(mockSystem)
	sys.On("RunCommand", "sc.exe", []string{"stop", serviceName}).
		Return([]byte("The specified service does not exist as an installed service."), errors.New("exit 1"))
	sys.On("RunCommand", "sc.exe", []string{"delete", serviceName}).
		Return([]byte("The specified service does not exist as an installed service."), errors.New("exit 1"))

	err := Stop(sys)
	require.NoError(s.T(), err)
}

func (s *DaemonSuite) TestStopDeleteError1060() {
	sys := new(mockSystem)
	sys.On("RunCommand", "sc.exe", []string{"stop", serviceName}).Return([]byte(""), nil)
	sys.On("RunCommand", "sc.exe", []string{"delete", serviceName}).
		Return([]byte("FAILED 1060"), errors.New("exit 1"))

	err := Stop(sys)
	require.NoError(s.T(), err)
}

func (s *DaemonSuite) TestStopDeleteError() {
	sys := new(mockSystem)
	sys.On("RunCommand", "sc.exe", []string{"stop", serviceName}).Return([]byte(""), nil)
	sys.On("RunCommand", "sc.exe", []string{"delete", serviceName}).
		Return([]byte("Access is denied."), errors.New("exit 5"))

	err := Stop(sys)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "sc delete")
}

// --- Status tests ---

func (s *DaemonSuite) TestStatusRunning() {
	sys := new(mockSystem)
	sys.On("RunCommand", "sc.exe", []string{"query", serviceName}).
		Return([]byte("        STATE              : 4  RUNNING\n"), nil)

	status, err := Status(sys)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "running", status)
}

func (s *DaemonSuite) TestStatusStopped() {
	sys := new(mockSystem)
	sys.On("RunCommand", "sc.exe", []string{"query", serviceName}).
		Return([]byte("        STATE              : 1  STOPPED\n"), nil)

	status, err := Status(sys)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "stopped", status)
}

func (s *DaemonSuite) TestStatusNotInstalled() {
	sys := new(mockSystem)
	sys.On("RunCommand", "sc.exe", []string{"query", serviceName}).
		Return([]byte("[SC] OpenService FAILED 1060: The specified service does not exist"), errors.New("exit 1"))

	status, err := Status(sys)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "not installed", status)
}

func (s *DaemonSuite) TestStatusDoesNotExist() {
	sys := new(mockSystem)
	sys.On("RunCommand", "sc.exe", []string{"query", serviceName}).
		Return([]byte("The specified service does not exist as an installed service."), errors.New("exit 1"))

	status, err := Status(sys)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "not installed", status)
}

func (s *DaemonSuite) TestStatusQueryError() {
	sys := new(mockSystem)
	sys.On("RunCommand", "sc.exe", []string{"query", serviceName}).
		Return([]byte("Access is denied."), errors.New("exit 5"))

	_, err := Status(sys)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "sc query")
}

// --- constants test ---

func (s *DaemonSuite) TestConstants() {
	require.Equal(s.T(), "Loop", serviceName)
	require.True(s.T(), strings.EqualFold(serviceName, "loop"))
}
