package terminal

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
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

type DockerSuite struct {
	suite.Suite
	origNewFunc func() (dockerExecAPI, error)
}

func TestDockerSuite(t *testing.T) {
	suite.Run(t, new(DockerSuite))
}

func (s *DockerSuite) SetupTest() {
	s.origNewFunc = newDockerExecClientFunc
}

func (s *DockerSuite) TearDownTest() {
	newDockerExecClientFunc = s.origNewFunc
}

func (s *DockerSuite) TestNewDockerExecClient() {
	api := new(mockDockerExecAPI)
	newDockerExecClientFunc = func() (dockerExecAPI, error) {
		return api, nil
	}

	c, err := NewDockerExecClient()
	require.NoError(s.T(), err)
	require.NotNil(s.T(), c)
}

func (s *DockerSuite) TestNewDockerExecClientError() {
	newDockerExecClientFunc = func() (dockerExecAPI, error) {
		return nil, errors.New("connection failed")
	}

	c, err := NewDockerExecClient()
	require.Error(s.T(), err)
	require.Nil(s.T(), c)
}

func (s *DockerSuite) TestContainerExecCreate() {
	api := new(mockDockerExecAPI)
	client := &DockerExecClient{api: api}

	expectedOpts := containertypes.ExecOptions{
		Cmd:          []string{"/bin/sh"},
		Tty:          true,
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
	}

	api.On("ContainerExecCreate", mock.Anything, "ctr-1", expectedOpts).
		Return(containertypes.ExecCreateResponse{ID: "exec-123"}, nil)

	id, err := client.ContainerExecCreate(context.Background(), "ctr-1", []string{"/bin/sh"}, true)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "exec-123", id)

	api.AssertExpectations(s.T())
}

func (s *DockerSuite) TestContainerExecCreateError() {
	api := new(mockDockerExecAPI)
	client := &DockerExecClient{api: api}

	api.On("ContainerExecCreate", mock.Anything, "ctr-1", mock.Anything).
		Return(containertypes.ExecCreateResponse{}, errors.New("create failed"))

	_, err := client.ContainerExecCreate(context.Background(), "ctr-1", []string{"/bin/sh"}, true)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "create failed")

	api.AssertExpectations(s.T())
}

func (s *DockerSuite) TestContainerExecAttach() {
	api := new(mockDockerExecAPI)
	client := &DockerExecClient{api: api}

	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()

	hijacked := types.NewHijackedResponse(clientConn, "")

	api.On("ContainerExecAttach", mock.Anything, "exec-1", containertypes.ExecAttachOptions{Tty: true}).
		Return(hijacked, nil)

	rwc, err := client.ContainerExecAttach(context.Background(), "exec-1")
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

func (s *DockerSuite) TestContainerExecAttachError() {
	api := new(mockDockerExecAPI)
	client := &DockerExecClient{api: api}

	api.On("ContainerExecAttach", mock.Anything, "exec-1", mock.Anything).
		Return(types.HijackedResponse{}, errors.New("attach failed"))

	rwc, err := client.ContainerExecAttach(context.Background(), "exec-1")
	require.Error(s.T(), err)
	require.Nil(s.T(), rwc)

	api.AssertExpectations(s.T())
}

func (s *DockerSuite) TestContainerExecResize() {
	api := new(mockDockerExecAPI)
	client := &DockerExecClient{api: api}

	api.On("ContainerExecResize", mock.Anything, "exec-1", containertypes.ResizeOptions{
		Height: 24,
		Width:  80,
	}).Return(nil)

	err := client.ContainerExecResize(context.Background(), "exec-1", 24, 80)
	require.NoError(s.T(), err)

	api.AssertExpectations(s.T())
}

func (s *DockerSuite) TestContainerExecResizeError() {
	api := new(mockDockerExecAPI)
	client := &DockerExecClient{api: api}

	api.On("ContainerExecResize", mock.Anything, "exec-1", mock.Anything).
		Return(errors.New("resize failed"))

	err := client.ContainerExecResize(context.Background(), "exec-1", 24, 80)
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

func (s *DockerSuite) TestContainerExecCreateNoTTY() {
	api := new(mockDockerExecAPI)
	client := &DockerExecClient{api: api}

	expectedOpts := containertypes.ExecOptions{
		Cmd:          []string{"ls", "-la"},
		Tty:          false,
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
	}

	api.On("ContainerExecCreate", mock.Anything, "ctr-1", expectedOpts).
		Return(containertypes.ExecCreateResponse{ID: "exec-456"}, nil)

	id, err := client.ContainerExecCreate(context.Background(), "ctr-1", []string{"ls", "-la"}, false)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "exec-456", id)

	api.AssertExpectations(s.T())
}
