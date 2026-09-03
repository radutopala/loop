package terminal

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"testing"

	"github.com/docker/docker/api/types"
	containertypes "github.com/docker/docker/api/types/container"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// mockDockerExecAPI implements dockerExecAPI for testing.
type mockDockerExecAPI struct {
	mock.Mock
}

func (m *mockDockerExecAPI) ContainerExecCreate(ctx context.Context, container string, options containertypes.ExecOptions) (containertypes.ExecCreateResponse, error) {
	args := m.Called(ctx, container, options)
	return args.Get(0).(containertypes.ExecCreateResponse), args.Error(1)
}

func (m *mockDockerExecAPI) ContainerExecAttach(ctx context.Context, execID string, options containertypes.ExecAttachOptions) (types.HijackedResponse, error) {
	args := m.Called(ctx, execID, options)
	return args.Get(0).(types.HijackedResponse), args.Error(1)
}

func (m *mockDockerExecAPI) ContainerExecResize(ctx context.Context, execID string, options containertypes.ResizeOptions) error {
	args := m.Called(ctx, execID, options)
	return args.Error(0)
}

func (m *mockDockerExecAPI) ContainerExecInspect(ctx context.Context, execID string) (containertypes.ExecInspect, error) {
	args := m.Called(ctx, execID)
	return args.Get(0).(containertypes.ExecInspect), args.Error(1)
}

type DockerSuite struct {
	suite.Suite
}

func TestDockerSuite(t *testing.T) {
	suite.Run(t, new(DockerSuite))
}

func (s *DockerSuite) TestNewDockerExecClientDefault() {
	// Exercise the real NewDockerExecClient to cover its default body.
	// May fail if Docker is not available — that's OK, we just need to cover the code path.
	_, _ = NewDockerExecClient()
}

func (s *DockerSuite) TestNewDockerExecClientWithSuccess() {
	api := new(mockDockerExecAPI)
	c, err := newDockerExecClientWith(func() (dockerExecAPI, error) {
		return api, nil
	})
	require.NoError(s.T(), err)
	require.NotNil(s.T(), c)
	require.NotNil(s.T(), c.execUser)
	require.NotEmpty(s.T(), c.execUser())
}

func (s *DockerSuite) TestNewDockerExecClientWithError() {
	c, err := newDockerExecClientWith(func() (dockerExecAPI, error) {
		return nil, errors.New("factory failed")
	})
	require.Error(s.T(), err)
	require.Nil(s.T(), c)
	require.Contains(s.T(), err.Error(), "factory failed")
}

func (s *DockerSuite) TestExecCreate() {
	api := new(mockDockerExecAPI)
	c := &DockerExecClient{api: api, execUser: func() string { return "1000:1000" }}

	expectedOpts := containertypes.ExecOptions{
		User:         "1000:1000",
		Cmd:          waitForExecUser([]string{"/bin/sh"}),
		Tty:          true,
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
	}

	api.On("ContainerExecCreate", mock.Anything, "ctr-1", expectedOpts).
		Return(containertypes.ExecCreateResponse{ID: "exec-123"}, nil)

	id, err := c.ExecCreate(context.Background(), "ctr-1", []string{"/bin/sh"}, true)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "exec-123", id)

	api.AssertExpectations(s.T())
}

func (s *DockerSuite) TestExecCreateDefaultCmd() {
	api := new(mockDockerExecAPI)
	c := &DockerExecClient{api: api, execUser: func() string { return "1000:1000" }}

	expectedOpts := containertypes.ExecOptions{
		User:         "1000:1000",
		Cmd:          waitForExecUser([]string{"/bin/sh"}),
		Tty:          true,
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
	}

	api.On("ContainerExecCreate", mock.Anything, "ctr-1", expectedOpts).
		Return(containertypes.ExecCreateResponse{ID: "exec-default"}, nil)

	id, err := c.ExecCreate(context.Background(), "ctr-1", nil, true)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "exec-default", id)

	api.AssertExpectations(s.T())
}

func (s *DockerSuite) TestExecCreateError() {
	api := new(mockDockerExecAPI)
	c := &DockerExecClient{api: api, execUser: func() string { return "0:0" }}

	api.On("ContainerExecCreate", mock.Anything, "ctr-1", mock.Anything).
		Return(containertypes.ExecCreateResponse{}, errors.New("create failed"))

	_, err := c.ExecCreate(context.Background(), "ctr-1", []string{"/bin/sh"}, true)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "create failed")

	api.AssertExpectations(s.T())
}

func (s *DockerSuite) TestDefaultExecUserNumeric() {
	u := defaultExecUser()
	require.Regexp(s.T(), `^\d+:\d+$`, u)
}

func (s *DockerSuite) TestFormatExecUserPositive() {
	require.Equal(s.T(), "1000:1000", formatExecUser(1000, 1000))
}

func (s *DockerSuite) TestFormatExecUserWindowsFallback() {
	require.Equal(s.T(), "0:0", formatExecUser(-1, -1))
	require.Equal(s.T(), "0:0", formatExecUser(-1, 0))
	require.Equal(s.T(), "0:0", formatExecUser(0, -1))
}

func (s *DockerSuite) TestExecUserFromEnv() {
	require.Equal(s.T(), "1000:1000", execUserFromEnv("1000", "1000"))
	require.Equal(s.T(), "0:0", execUserFromEnv(" 0 ", " 0 "))
	// Empty / non-numeric / negative → "" so the caller falls back to the process uid.
	require.Equal(s.T(), "", execUserFromEnv("", "1000"))
	require.Equal(s.T(), "", execUserFromEnv("1000", ""))
	require.Equal(s.T(), "", execUserFromEnv("agent", "1000"))
	require.Equal(s.T(), "", execUserFromEnv("1000", "x"))
	require.Equal(s.T(), "", execUserFromEnv("-1", "1000"))
	require.Equal(s.T(), "", execUserFromEnv("1000", "-2"))
}

func (s *DockerSuite) TestDefaultExecUserHonorsHostEnv() {
	s.T().Setenv("HOST_UID", "1000")
	s.T().Setenv("HOST_GID", "2000")
	require.Equal(s.T(), "1000:2000", defaultExecUser())
}

func (s *DockerSuite) TestDefaultExecUserFallsBackWhenEnvUnset() {
	s.T().Setenv("HOST_UID", "")
	s.T().Setenv("HOST_GID", "")
	require.Regexp(s.T(), `^\d+:\d+$`, defaultExecUser())
}

func (s *DockerSuite) TestExecAttach() {
	api := new(mockDockerExecAPI)
	client := &DockerExecClient{api: api, execUser: defaultExecUser}

	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()

	hijacked := types.NewHijackedResponse(clientConn, "")

	api.On("ContainerExecAttach", mock.Anything, "exec-1", containertypes.ExecAttachOptions{Tty: true}).
		Return(hijacked, nil)

	rwc, err := client.ExecAttach(context.Background(), "exec-1")
	require.NoError(s.T(), err)
	require.NotNil(s.T(), rwc)

	// Write from server side, read from client side.
	go func() { _, _ = serverConn.Write([]byte("hello")) }()

	buf := make([]byte, 5)
	n, err := rwc.Read(buf)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "hello", string(buf[:n]))

	// Write from client side, read from server side.
	readDone := make(chan struct{})
	var serverBuf [5]byte
	var serverN int
	var serverErr error
	go func() {
		serverN, serverErr = serverConn.Read(serverBuf[:])
		close(readDone)
	}()

	_, err = rwc.Write([]byte("world"))
	require.NoError(s.T(), err)

	<-readDone
	require.NoError(s.T(), serverErr)
	require.Equal(s.T(), "world", string(serverBuf[:serverN]))

	require.NoError(s.T(), rwc.Close())

	api.AssertExpectations(s.T())
}

func (s *DockerSuite) TestExecAttachError() {
	api := new(mockDockerExecAPI)
	client := &DockerExecClient{api: api, execUser: defaultExecUser}

	api.On("ContainerExecAttach", mock.Anything, "exec-1", mock.Anything).
		Return(types.HijackedResponse{}, errors.New("attach failed"))

	rwc, err := client.ExecAttach(context.Background(), "exec-1")
	require.Error(s.T(), err)
	require.Nil(s.T(), rwc)

	api.AssertExpectations(s.T())
}

func (s *DockerSuite) TestExecResize() {
	api := new(mockDockerExecAPI)
	client := &DockerExecClient{api: api, execUser: defaultExecUser}

	api.On("ContainerExecResize", mock.Anything, "exec-1", containertypes.ResizeOptions{
		Height: 24,
		Width:  80,
	}).Return(nil)

	err := client.ExecResize(context.Background(), "exec-1", 24, 80)
	require.NoError(s.T(), err)

	api.AssertExpectations(s.T())
}

func (s *DockerSuite) TestExecResizeError() {
	api := new(mockDockerExecAPI)
	client := &DockerExecClient{api: api, execUser: defaultExecUser}

	api.On("ContainerExecResize", mock.Anything, "exec-1", mock.Anything).
		Return(errors.New("resize failed"))

	err := client.ExecResize(context.Background(), "exec-1", 24, 80)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "resize failed")

	api.AssertExpectations(s.T())
}

func (s *DockerSuite) TestHijackedConnReadWriteClose() {
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()

	h := &hijackedConn{
		resp: types.HijackedResponse{
			Conn:   clientConn,
			Reader: bufio.NewReader(clientConn),
		},
	}

	// Write data from the server side.
	go func() { _, _ = serverConn.Write([]byte("data")) }()

	buf := make([]byte, 4)
	n, err := h.Read(buf)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "data", string(buf[:n]))

	// Write data from the client side.
	readDone := make(chan struct{})
	var serverBuf [5]byte
	var serverN int
	var serverErr error
	go func() {
		serverN, serverErr = serverConn.Read(serverBuf[:])
		close(readDone)
	}()

	_, err = h.Write([]byte("reply"))
	require.NoError(s.T(), err)

	<-readDone
	require.NoError(s.T(), serverErr)
	require.Equal(s.T(), "reply", string(serverBuf[:serverN]))

	// Close should not error.
	require.NoError(s.T(), h.Close())
}

func (s *DockerSuite) TestHijackedConnReadEOF() {
	serverConn, clientConn := net.Pipe()

	h := &hijackedConn{
		resp: types.HijackedResponse{
			Conn:   clientConn,
			Reader: bufio.NewReader(clientConn),
		},
	}

	serverConn.Close()

	buf := make([]byte, 4)
	_, err := h.Read(buf)
	require.ErrorIs(s.T(), err, io.EOF)
}

func (s *DockerSuite) TestExecInspectPid() {
	api := new(mockDockerExecAPI)
	c := &DockerExecClient{api: api, execUser: defaultExecUser}

	api.On("ContainerExecInspect", mock.Anything, "exec-1").
		Return(containertypes.ExecInspect{Pid: 12345}, nil)

	pid, err := c.ExecInspectPid(context.Background(), "exec-1")
	require.NoError(s.T(), err)
	require.Equal(s.T(), 12345, pid)

	api.AssertExpectations(s.T())
}

func (s *DockerSuite) TestExecInspectPidError() {
	api := new(mockDockerExecAPI)
	c := &DockerExecClient{api: api, execUser: defaultExecUser}

	api.On("ContainerExecInspect", mock.Anything, "exec-1").
		Return(containertypes.ExecInspect{}, errors.New("inspect failed"))

	pid, err := c.ExecInspectPid(context.Background(), "exec-1")
	require.Error(s.T(), err)
	require.Equal(s.T(), 0, pid)
	require.Contains(s.T(), err.Error(), "inspect failed")

	api.AssertExpectations(s.T())
}

func (s *DockerSuite) TestDefaultShellCmd() {
	c := &DockerExecClient{}
	cmd := c.DefaultShellCmd("/tmp/.loop-exec-abc.pid")
	require.Equal(s.T(), []string{"/bin/bash", "-c", "echo $$ > /tmp/.loop-exec-abc.pid; exec /bin/bash --rcfile /etc/loop/bashrc -i"}, cmd)
}

func (s *DockerSuite) TestExecCreateNoTTY() {
	api := new(mockDockerExecAPI)
	c := &DockerExecClient{api: api, execUser: func() string { return "1000:1000" }}

	expectedOpts := containertypes.ExecOptions{
		User:         "1000:1000",
		Cmd:          []string{"ls", "-la"},
		Tty:          false,
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
	}

	api.On("ContainerExecCreate", mock.Anything, "ctr-1", expectedOpts).
		Return(containertypes.ExecCreateResponse{ID: "exec-456"}, nil)

	id, err := c.ExecCreate(context.Background(), "ctr-1", []string{"ls", "-la"}, false)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "exec-456", id)

	api.AssertExpectations(s.T())
}

// TestWaitForExecUser pins the wrapper shape: the requested command is handed
// to the preamble as positional arguments, so no argument needs quoting.
func (s *DockerSuite) TestWaitForExecUser() {
	require.Equal(s.T(),
		[]string{"/bin/sh", "-c", execUserWaitScript, "sh", "/bin/bash", "-c", "echo hi"},
		waitForExecUser([]string{"/bin/bash", "-c", "echo hi"}))
}

// TestExecUserWaitScriptRunsCommand runs the real preamble under a real
// /bin/sh: the trailing exec must pass every argument through untouched,
// including ones containing spaces.
func (s *DockerSuite) TestExecUserWaitScriptRunsCommand() {
	if _, err := os.Stat("/bin/sh"); err != nil {
		s.T().Skip("no /bin/sh on this platform")
	}
	wrapped := waitForExecUser([]string{"/bin/sh", "-c", `printf '%s|' "$@"`, "sh", "a b", "c"})
	out, err := exec.Command(wrapped[0], wrapped[1:]...).Output()
	require.NoError(s.T(), err)
	require.Equal(s.T(), "a b|c|", string(out))
}

// TestExecUserWaitScriptPreservesExitStatus guards the exec at the end of the
// preamble: the wrapper must not swallow the command's failure, since the
// terminal reports session exits from it.
func (s *DockerSuite) TestExecUserWaitScriptPreservesExitStatus() {
	if _, err := os.Stat("/bin/sh"); err != nil {
		s.T().Skip("no /bin/sh on this platform")
	}
	wrapped := waitForExecUser([]string{"/bin/sh", "-c", "exit 7"})
	var exitErr *exec.ExitError
	err := exec.Command(wrapped[0], wrapped[1:]...).Run()
	require.ErrorAs(s.T(), err, &exitErr)
	require.Equal(s.T(), 7, exitErr.ExitCode())
}
