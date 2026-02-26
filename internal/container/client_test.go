package container

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"testing"

	containertypes "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/pkg/stdcopy"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// mockDockerAPI implements the dockerAPI interface for testing.
type mockDockerAPI struct {
	mock.Mock
}

func (m *mockDockerAPI) ContainerCreate(ctx context.Context, config *containertypes.Config, hostConfig *containertypes.HostConfig, networkingConfig *network.NetworkingConfig, platform *ocispec.Platform, containerName string) (containertypes.CreateResponse, error) {
	args := m.Called(ctx, config, hostConfig, networkingConfig, platform, containerName)
	return args.Get(0).(containertypes.CreateResponse), args.Error(1)
}

func (m *mockDockerAPI) ContainerStart(ctx context.Context, container string, options containertypes.StartOptions) error {
	args := m.Called(ctx, container, options)
	return args.Error(0)
}

func (m *mockDockerAPI) ContainerWait(ctx context.Context, container string, condition containertypes.WaitCondition) (<-chan containertypes.WaitResponse, <-chan error) {
	args := m.Called(ctx, container, condition)
	return args.Get(0).(<-chan containertypes.WaitResponse), args.Get(1).(<-chan error)
}

func (m *mockDockerAPI) ContainerRemove(ctx context.Context, container string, options containertypes.RemoveOptions) error {
	args := m.Called(ctx, container, options)
	return args.Error(0)
}

func (m *mockDockerAPI) ContainerList(ctx context.Context, options containertypes.ListOptions) ([]containertypes.Summary, error) {
	args := m.Called(ctx, options)
	return args.Get(0).([]containertypes.Summary), args.Error(1)
}

func (m *mockDockerAPI) ImageList(ctx context.Context, options image.ListOptions) ([]image.Summary, error) {
	args := m.Called(ctx, options)
	return args.Get(0).([]image.Summary), args.Error(1)
}

func (m *mockDockerAPI) ImagePull(ctx context.Context, refStr string, options image.PullOptions) (io.ReadCloser, error) {
	args := m.Called(ctx, refStr, options)
	var rc io.ReadCloser
	if v := args.Get(0); v != nil {
		rc = v.(io.ReadCloser)
	}
	return rc, args.Error(1)
}

func (m *mockDockerAPI) ContainerLogs(ctx context.Context, container string, options containertypes.LogsOptions) (io.ReadCloser, error) {
	args := m.Called(ctx, container, options)
	var rc io.ReadCloser
	if v := args.Get(0); v != nil {
		rc = v.(io.ReadCloser)
	}
	return rc, args.Error(1)
}

func (m *mockDockerAPI) CopyToContainer(ctx context.Context, containerID, dstPath string, content io.Reader, options containertypes.CopyToContainerOptions) error {
	args := m.Called(ctx, containerID, dstPath, content, options)
	return args.Error(0)
}

func (m *mockDockerAPI) Close() error {
	args := m.Called()
	return args.Error(0)
}

type ClientSuite struct {
	suite.Suite
	api    *mockDockerAPI
	client *Client
}

func TestClientSuite(t *testing.T) {
	suite.Run(t, new(ClientSuite))
}

func (s *ClientSuite) SetupTest() {
	s.api = new(mockDockerAPI)
	s.client = &Client{api: s.api}
}

func (s *ClientSuite) TestNewDockerClientFuncDefault() {
	// Exercise the default newDockerClientFunc to cover the production code path.
	// client.NewClientWithOpts succeeds without a running Docker daemon.
	api, err := newDockerClientFunc()
	require.NoError(s.T(), err)
	require.NotNil(s.T(), api)
	_ = api.Close()
}

func (s *ClientSuite) TestNewClient() {
	original := newDockerClientFunc
	defer func() { newDockerClientFunc = original }()

	mockAPI := new(mockDockerAPI)
	newDockerClientFunc = func() (dockerAPI, error) {
		return mockAPI, nil
	}

	c, err := NewClient()
	require.NoError(s.T(), err)
	require.NotNil(s.T(), c)
	require.Equal(s.T(), mockAPI, c.api)
}

func (s *ClientSuite) TestNewClientError() {
	original := newDockerClientFunc
	defer func() { newDockerClientFunc = original }()

	newDockerClientFunc = func() (dockerAPI, error) {
		return nil, errors.New("connection refused")
	}

	c, err := NewClient()
	require.Nil(s.T(), c)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "creating docker client")
}

func (s *ClientSuite) TestClose() {
	s.api.On("Close").Return(nil)

	err := s.client.Close()
	require.NoError(s.T(), err)
	s.api.AssertExpectations(s.T())
}

func (s *ClientSuite) TestCloseError() {
	s.api.On("Close").Return(errors.New("close failed"))

	err := s.client.Close()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "close failed")
	s.api.AssertExpectations(s.T())
}

func (s *ClientSuite) TestContainerCreate() {
	ctx := context.Background()
	cfg := &ContainerConfig{
		Image:    "my-image:latest",
		MemoryMB: 512,
		CPUs:     1.5,
		Env:      []string{"FOO=bar"},
	}

	expectedConfig := &containertypes.Config{
		Image:        "my-image:latest",
		AttachStdout: true,
		AttachStderr: true,
		Labels:       map[string]string{"app": "loop-agent"},
		Env:          []string{"FOO=bar"},
	}
	expectedHostConfig := &containertypes.HostConfig{
		Resources: containertypes.Resources{
			Memory:    512 * 1024 * 1024,
			CPUQuota:  150000,
			CPUPeriod: 100000,
		},
		Binds:      nil,
		ExtraHosts: []string{"host.docker.internal:host-gateway"},
	}

	s.api.On("ContainerCreate", ctx, expectedConfig, expectedHostConfig, (*network.NetworkingConfig)(nil), (*ocispec.Platform)(nil), "my-container").
		Return(containertypes.CreateResponse{ID: "abc123"}, nil)

	id, err := s.client.ContainerCreate(ctx, cfg, "my-container")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "abc123", id)
	s.api.AssertExpectations(s.T())
}

func (s *ClientSuite) TestContainerCreateEmptyName() {
	ctx := context.Background()
	cfg := &ContainerConfig{
		Image:    "img:latest",
		MemoryMB: 256,
		CPUs:     0.5,
	}

	s.api.On("ContainerCreate", ctx, mock.AnythingOfType("*container.Config"), mock.AnythingOfType("*container.HostConfig"), (*network.NetworkingConfig)(nil), (*ocispec.Platform)(nil), "").
		Return(containertypes.CreateResponse{ID: "xyz789"}, nil)

	id, err := s.client.ContainerCreate(ctx, cfg, "")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "xyz789", id)
	s.api.AssertExpectations(s.T())
}

func (s *ClientSuite) TestContainerCreateError() {
	ctx := context.Background()
	cfg := &ContainerConfig{Image: "img:latest"}

	s.api.On("ContainerCreate", ctx, mock.Anything, mock.Anything, (*network.NetworkingConfig)(nil), (*ocispec.Platform)(nil), "").
		Return(containertypes.CreateResponse{}, errors.New("create failed"))

	id, err := s.client.ContainerCreate(ctx, cfg, "")
	require.Error(s.T(), err)
	require.Empty(s.T(), id)
	require.Contains(s.T(), err.Error(), "create failed")
	s.api.AssertExpectations(s.T())
}

func (s *ClientSuite) TestContainerLogs() {
	ctx := context.Background()

	// Build stdcopy-formatted stream data.
	var streamBuf bytes.Buffer
	w := stdcopy.NewStdWriter(&streamBuf, stdcopy.Stdout)
	_, err := w.Write([]byte("output"))
	require.NoError(s.T(), err)

	s.api.On("ContainerLogs", ctx, "cid-1", containertypes.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
	}).Return(io.NopCloser(&streamBuf), nil)

	r, err := s.client.ContainerLogs(ctx, "cid-1")
	require.NoError(s.T(), err)

	data, readErr := io.ReadAll(r)
	require.NoError(s.T(), readErr)
	require.Equal(s.T(), "output", string(data))

	s.api.AssertExpectations(s.T())
}

func (s *ClientSuite) TestContainerLogsError() {
	ctx := context.Background()

	s.api.On("ContainerLogs", ctx, "cid-1", containertypes.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
	}).Return(nil, errors.New("logs failed"))

	r, err := s.client.ContainerLogs(ctx, "cid-1")
	require.Error(s.T(), err)
	require.Nil(s.T(), r)
	require.Contains(s.T(), err.Error(), "logs failed")
	s.api.AssertExpectations(s.T())
}

func (s *ClientSuite) TestContainerLogsFollow() {
	ctx := context.Background()

	// Build stdcopy-formatted stream data.
	var streamBuf bytes.Buffer
	w := stdcopy.NewStdWriter(&streamBuf, stdcopy.Stdout)
	_, err := w.Write([]byte("streaming output"))
	require.NoError(s.T(), err)

	s.api.On("ContainerLogs", ctx, "cid-1", containertypes.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     true,
	}).Return(io.NopCloser(&streamBuf), nil)

	r, err := s.client.ContainerLogsFollow(ctx, "cid-1")
	require.NoError(s.T(), err)

	data, readErr := io.ReadAll(r)
	require.NoError(s.T(), readErr)
	require.Equal(s.T(), "streaming output", string(data))
	require.NoError(s.T(), r.Close())

	s.api.AssertExpectations(s.T())
}

func (s *ClientSuite) TestContainerLogsFollowError() {
	ctx := context.Background()

	s.api.On("ContainerLogs", ctx, "cid-1", containertypes.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     true,
	}).Return(nil, errors.New("follow failed"))

	r, err := s.client.ContainerLogsFollow(ctx, "cid-1")
	require.Error(s.T(), err)
	require.Nil(s.T(), r)
	require.Contains(s.T(), err.Error(), "follow failed")
	s.api.AssertExpectations(s.T())
}

func (s *ClientSuite) TestContainerStart() {
	ctx := context.Background()

	s.api.On("ContainerStart", ctx, "cid-1", containertypes.StartOptions{}).Return(nil)

	err := s.client.ContainerStart(ctx, "cid-1")
	require.NoError(s.T(), err)
	s.api.AssertExpectations(s.T())
}

func (s *ClientSuite) TestContainerStartError() {
	ctx := context.Background()

	s.api.On("ContainerStart", ctx, "cid-1", containertypes.StartOptions{}).Return(errors.New("start failed"))

	err := s.client.ContainerStart(ctx, "cid-1")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "start failed")
	s.api.AssertExpectations(s.T())
}

func (s *ClientSuite) TestContainerWaitSuccess() {
	ctx := context.Background()

	dockerWaitCh := make(chan containertypes.WaitResponse, 1)
	dockerErrCh := make(chan error, 1)
	dockerWaitCh <- containertypes.WaitResponse{StatusCode: 0}

	s.api.On("ContainerWait", ctx, "cid-1", containertypes.WaitConditionNotRunning).
		Return((<-chan containertypes.WaitResponse)(dockerWaitCh), (<-chan error)(dockerErrCh))

	waitCh, errCh := s.client.ContainerWait(ctx, "cid-1")

	wr := <-waitCh
	require.Equal(s.T(), int64(0), wr.StatusCode)
	require.NoError(s.T(), wr.Error)

	// errCh should be closed without error
	err, ok := <-errCh
	require.False(s.T(), ok)
	require.Nil(s.T(), err)

	s.api.AssertExpectations(s.T())
}

func (s *ClientSuite) TestContainerWaitWithExitError() {
	ctx := context.Background()

	dockerWaitCh := make(chan containertypes.WaitResponse, 1)
	dockerErrCh := make(chan error, 1)
	dockerWaitCh <- containertypes.WaitResponse{
		StatusCode: 1,
		Error:      &containertypes.WaitExitError{Message: "exit code 1"},
	}

	s.api.On("ContainerWait", ctx, "cid-1", containertypes.WaitConditionNotRunning).
		Return((<-chan containertypes.WaitResponse)(dockerWaitCh), (<-chan error)(dockerErrCh))

	waitCh, errCh := s.client.ContainerWait(ctx, "cid-1")

	wr := <-waitCh
	require.Equal(s.T(), int64(1), wr.StatusCode)
	require.Error(s.T(), wr.Error)
	require.Contains(s.T(), wr.Error.Error(), "exit code 1")

	// errCh should be closed without error
	err, ok := <-errCh
	require.False(s.T(), ok)
	require.Nil(s.T(), err)

	s.api.AssertExpectations(s.T())
}

func (s *ClientSuite) TestContainerWaitError() {
	ctx := context.Background()

	dockerWaitCh := make(chan containertypes.WaitResponse, 1)
	dockerErrCh := make(chan error, 1)
	dockerErrCh <- errors.New("wait failed")

	s.api.On("ContainerWait", ctx, "cid-1", containertypes.WaitConditionNotRunning).
		Return((<-chan containertypes.WaitResponse)(dockerWaitCh), (<-chan error)(dockerErrCh))

	waitCh, errCh := s.client.ContainerWait(ctx, "cid-1")

	err := <-errCh
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "wait failed")

	// waitCh should be closed without a response
	wr, ok := <-waitCh
	require.False(s.T(), ok)
	require.Zero(s.T(), wr)

	s.api.AssertExpectations(s.T())
}

func (s *ClientSuite) TestContainerWaitDockerWaitChClosed() {
	ctx := context.Background()

	dockerWaitCh := make(chan containertypes.WaitResponse)
	dockerErrCh := make(chan error)
	close(dockerWaitCh)

	s.api.On("ContainerWait", ctx, "cid-1", containertypes.WaitConditionNotRunning).
		Return((<-chan containertypes.WaitResponse)(dockerWaitCh), (<-chan error)(dockerErrCh))

	waitCh, errCh := s.client.ContainerWait(ctx, "cid-1")

	// Both channels should be closed
	_, ok := <-waitCh
	require.False(s.T(), ok)
	_, ok = <-errCh
	require.False(s.T(), ok)

	s.api.AssertExpectations(s.T())
}

func (s *ClientSuite) TestContainerWaitDockerErrChClosed() {
	ctx := context.Background()

	dockerWaitCh := make(chan containertypes.WaitResponse)
	dockerErrCh := make(chan error)
	close(dockerErrCh)

	s.api.On("ContainerWait", ctx, "cid-1", containertypes.WaitConditionNotRunning).
		Return((<-chan containertypes.WaitResponse)(dockerWaitCh), (<-chan error)(dockerErrCh))

	waitCh, errCh := s.client.ContainerWait(ctx, "cid-1")

	// Both channels should be closed
	_, ok := <-waitCh
	require.False(s.T(), ok)
	_, ok = <-errCh
	require.False(s.T(), ok)

	s.api.AssertExpectations(s.T())
}

func (s *ClientSuite) TestContainerRemove() {
	ctx := context.Background()

	s.api.On("ContainerRemove", ctx, "cid-1", containertypes.RemoveOptions{Force: true}).Return(nil)

	err := s.client.ContainerRemove(ctx, "cid-1")
	require.NoError(s.T(), err)
	s.api.AssertExpectations(s.T())
}

func (s *ClientSuite) TestContainerRemoveError() {
	ctx := context.Background()

	s.api.On("ContainerRemove", ctx, "cid-1", containertypes.RemoveOptions{Force: true}).Return(errors.New("remove failed"))

	err := s.client.ContainerRemove(ctx, "cid-1")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "remove failed")
	s.api.AssertExpectations(s.T())
}

func (s *ClientSuite) TestImageList() {
	ctx := context.Background()

	s.api.On("ImageList", ctx, mock.MatchedBy(func(opts image.ListOptions) bool {
		return opts.Filters.Get("reference")[0] == "my-image:latest"
	})).Return([]image.Summary{
		{ID: "sha256:aaa"},
		{ID: "sha256:bbb"},
	}, nil)

	ids, err := s.client.ImageList(ctx, "my-image:latest")
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{"sha256:aaa", "sha256:bbb"}, ids)
	s.api.AssertExpectations(s.T())
}

func (s *ClientSuite) TestImageListEmpty() {
	ctx := context.Background()

	s.api.On("ImageList", ctx, mock.Anything).Return([]image.Summary{}, nil)

	ids, err := s.client.ImageList(ctx, "nonexistent:latest")
	require.NoError(s.T(), err)
	require.Empty(s.T(), ids)
	s.api.AssertExpectations(s.T())
}

func (s *ClientSuite) TestImageListError() {
	ctx := context.Background()

	s.api.On("ImageList", ctx, mock.Anything).Return([]image.Summary(nil), errors.New("list failed"))

	ids, err := s.client.ImageList(ctx, "my-image:latest")
	require.Error(s.T(), err)
	require.Nil(s.T(), ids)
	require.Contains(s.T(), err.Error(), "list failed")
	s.api.AssertExpectations(s.T())
}

func (s *ClientSuite) TestImagePull() {
	ctx := context.Background()

	s.api.On("ImagePull", ctx, "my-image:latest", image.PullOptions{}).
		Return(io.NopCloser(bytes.NewReader([]byte("pulling..."))), nil)

	err := s.client.ImagePull(ctx, "my-image:latest")
	require.NoError(s.T(), err)
	s.api.AssertExpectations(s.T())
}

func (s *ClientSuite) TestImagePullError() {
	ctx := context.Background()

	s.api.On("ImagePull", ctx, "my-image:latest", image.PullOptions{}).
		Return(nil, errors.New("pull failed"))

	err := s.client.ImagePull(ctx, "my-image:latest")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "pull failed")
	s.api.AssertExpectations(s.T())
}

func (s *ClientSuite) TestImagePullDrainError() {
	ctx := context.Background()

	s.api.On("ImagePull", ctx, "my-image:latest", image.PullOptions{}).
		Return(io.NopCloser(&errReader{err: errors.New("read error")}), nil)

	err := s.client.ImagePull(ctx, "my-image:latest")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "read error")
	s.api.AssertExpectations(s.T())
}

func (s *ClientSuite) TestContainerList() {
	ctx := context.Background()

	s.api.On("ContainerList", ctx, mock.MatchedBy(func(opts containertypes.ListOptions) bool {
		return opts.All && opts.Filters.Get("label")[0] == "app=loop-agent"
	})).Return([]containertypes.Summary{
		{ID: "cid-1"},
		{ID: "cid-2"},
	}, nil)

	ids, err := s.client.ContainerList(ctx, "app", "loop-agent")
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{"cid-1", "cid-2"}, ids)
	s.api.AssertExpectations(s.T())
}

func (s *ClientSuite) TestContainerListEmpty() {
	ctx := context.Background()

	s.api.On("ContainerList", ctx, mock.Anything).Return([]containertypes.Summary{}, nil)

	ids, err := s.client.ContainerList(ctx, "app", "loop-agent")
	require.NoError(s.T(), err)
	require.Empty(s.T(), ids)
	s.api.AssertExpectations(s.T())
}

func (s *ClientSuite) TestContainerListError() {
	ctx := context.Background()

	s.api.On("ContainerList", ctx, mock.Anything).Return([]containertypes.Summary(nil), errors.New("list failed"))

	ids, err := s.client.ContainerList(ctx, "app", "loop-agent")
	require.Error(s.T(), err)
	require.Nil(s.T(), ids)
	require.Contains(s.T(), err.Error(), "list failed")
	s.api.AssertExpectations(s.T())
}

func (s *ClientSuite) TestImageBuild() {
	orig := dockerBuildCmd
	defer func() { dockerBuildCmd = orig }()

	dockerBuildCmd = func(_ context.Context, contextDir, tag string) ([]byte, error) {
		require.Equal(s.T(), "/tmp/ctx", contextDir)
		require.Equal(s.T(), "test:latest", tag)
		return []byte("Successfully built abc123"), nil
	}

	err := s.client.ImageBuild(context.Background(), "/tmp/ctx", "test:latest")
	require.NoError(s.T(), err)
}

func (s *ClientSuite) TestImageBuildError() {
	orig := dockerBuildCmd
	defer func() { dockerBuildCmd = orig }()

	dockerBuildCmd = func(_ context.Context, _, _ string) ([]byte, error) {
		return []byte("error: build failed"), errors.New("exit status 1")
	}

	err := s.client.ImageBuild(context.Background(), "/tmp/ctx", "test:latest")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "building image")
	require.Contains(s.T(), err.Error(), "error: build failed")
}

func (s *ClientSuite) TestDefaultDockerBuildCmd() {
	// Exercise the default function with a cancelled context so it fails fast.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _ = dockerBuildCmd(ctx, "/nonexistent", "test:latest")
}

func (s *ClientSuite) TestGitconfigSecretPath() {
	origHome := userHomeDir
	origStat := osStat
	defer func() {
		userHomeDir = origHome
		osStat = origStat
	}()

	userHomeDir = func() (string, error) { return "/home/testuser", nil }
	osStat = func(name string) (os.FileInfo, error) {
		if name == "/home/testuser/.gitconfig" {
			return nil, nil
		}
		return nil, os.ErrNotExist
	}

	require.Equal(s.T(), "/home/testuser/.gitconfig", gitconfigSecretPath())
}

func (s *ClientSuite) TestGitconfigSecretPathNotExists() {
	origHome := userHomeDir
	origStat := osStat
	defer func() {
		userHomeDir = origHome
		osStat = origStat
	}()

	userHomeDir = func() (string, error) { return "/home/testuser", nil }
	osStat = func(_ string) (os.FileInfo, error) { return nil, os.ErrNotExist }

	require.Empty(s.T(), gitconfigSecretPath())
}

func (s *ClientSuite) TestGitconfigSecretPathHomeDirError() {
	origHome := userHomeDir
	defer func() { userHomeDir = origHome }()

	userHomeDir = func() (string, error) { return "", errors.New("no home") }

	require.Empty(s.T(), gitconfigSecretPath())
}

func (s *ClientSuite) TestCopyToContainer() {
	ctx := context.Background()
	content := bytes.NewReader([]byte("data"))

	s.api.On("CopyToContainer", ctx, "cid-1", "/home/user", content, containertypes.CopyToContainerOptions{}).Return(nil)

	err := s.client.CopyToContainer(ctx, "cid-1", "/home/user", content)
	require.NoError(s.T(), err)
	s.api.AssertExpectations(s.T())
}

func (s *ClientSuite) TestCopyToContainerError() {
	ctx := context.Background()
	content := bytes.NewReader([]byte("data"))

	s.api.On("CopyToContainer", ctx, "cid-1", "/home/user", content, containertypes.CopyToContainerOptions{}).Return(errors.New("copy failed"))

	err := s.client.CopyToContainer(ctx, "cid-1", "/home/user", content)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "copy failed")
	s.api.AssertExpectations(s.T())
}
