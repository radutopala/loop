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

	"github.com/radutopala/loop/internal/testutil"
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

func (m *mockDockerAPI) ImageRemove(ctx context.Context, imageID string, options image.RemoveOptions) ([]image.DeleteResponse, error) {
	args := m.Called(ctx, imageID, options)
	return args.Get(0).([]image.DeleteResponse), args.Error(1)
}

func (m *mockDockerAPI) ImageInspectWithRaw(ctx context.Context, imageID string) (image.InspectResponse, []byte, error) {
	args := m.Called(ctx, imageID)
	return args.Get(0).(image.InspectResponse), args.Get(1).([]byte), args.Error(2)
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

func (m *mockDockerAPI) ContainerStop(ctx context.Context, containerID string, options containertypes.StopOptions) error {
	args := m.Called(ctx, containerID, options)
	return args.Error(0)
}

func (m *mockDockerAPI) ContainerInspect(ctx context.Context, containerID string) (containertypes.InspectResponse, error) {
	args := m.Called(ctx, containerID)
	return args.Get(0).(containertypes.InspectResponse), args.Error(1)
}

func (m *mockDockerAPI) NetworkCreate(ctx context.Context, name string, options network.CreateOptions) (network.CreateResponse, error) {
	args := m.Called(ctx, name, options)
	return args.Get(0).(network.CreateResponse), args.Error(1)
}

func (m *mockDockerAPI) NetworkRemove(ctx context.Context, networkID string) error {
	args := m.Called(ctx, networkID)
	return args.Error(0)
}

func (m *mockDockerAPI) Close() error {
	args := m.Called()
	return args.Error(0)
}

type ClientSuite struct {
	suite.Suite
	api    *mockDockerAPI
	sys    *testutil.MockSystem
	client *Client
}

func TestClientSuite(t *testing.T) {
	suite.Run(t, new(ClientSuite))
}

func (s *ClientSuite) SetupTest() {
	s.api = new(mockDockerAPI)
	s.sys = new(testutil.MockSystem)
	s.sys.On("UserHomeDir").Return("/home/testuser", nil)
	s.sys.On("Stat", mock.Anything).Return(nil, os.ErrNotExist)
	s.client = &Client{
		api:              s.api,
		sys:              s.sys,
		claudeVersionURL: "https://storage.googleapis.com/claude-code-dist-86c565f3-f756-42ad-8dfa-d59b1c096819/claude-code-releases/latest",
	}
	s.client.latestClaudeVersion = s.client.defaultLatestClaudeVersion
	s.client.dockerBuildCmd = s.client.defaultDockerBuildCmd
}

func (s *ClientSuite) TestNewDockerClientFuncDefault() {
	// Exercise the default factory to cover the production code path.
	// client.NewClientWithOpts succeeds without a running Docker daemon.
	c, err := NewClientWith(defaultDockerAPIFactory)
	require.NoError(s.T(), err)
	require.NotNil(s.T(), c)
	_ = c.Close()
}

func (s *ClientSuite) TestNewClient() {
	mockAPI := new(mockDockerAPI)

	c, err := NewClientWith(func() (dockerAPI, error) {
		return mockAPI, nil
	})
	require.NoError(s.T(), err)
	require.NotNil(s.T(), c)
	require.Equal(s.T(), mockAPI, c.api)
}

func (s *ClientSuite) TestNewClientError() {
	c, err := NewClientWith(func() (dockerAPI, error) {
		return nil, errors.New("connection refused")
	})
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
	initTrue := true
	expectedHostConfig := &containertypes.HostConfig{
		Resources: containertypes.Resources{
			Memory:    512 * 1024 * 1024,
			CPUQuota:  150000,
			CPUPeriod: 100000,
		},
		Binds:      nil,
		ExtraHosts: []string{"host.docker.internal:host-gateway"},
		Init:       &initTrue,
	}

	s.api.On("ContainerCreate", ctx, expectedConfig, expectedHostConfig, (*network.NetworkingConfig)(nil), (*ocispec.Platform)(nil), "my-container").
		Return(containertypes.CreateResponse{ID: "abc123"}, nil)

	id, err := s.client.ContainerCreate(ctx, cfg, "my-container")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "abc123", id)
	s.api.AssertExpectations(s.T())
}

// TestContainerCreateAlwaysSetsInit pins the unconditional HostConfig.Init=true
// wiring. tini-as-PID-1 reaps orphaned grandchildren that loop-syscallwrap (or
// claude pre-gate) would otherwise leave as zombies.
func (s *ClientSuite) TestContainerCreateAlwaysSetsInit() {
	ctx := context.Background()
	cfg := &ContainerConfig{Image: "img:latest", MemoryMB: 64, CPUs: 0.25}

	var capturedHost *containertypes.HostConfig
	s.api.On("ContainerCreate", ctx,
		mock.AnythingOfType("*container.Config"),
		mock.MatchedBy(func(h *containertypes.HostConfig) bool {
			capturedHost = h
			return true
		}),
		(*network.NetworkingConfig)(nil), (*ocispec.Platform)(nil), "init-test").
		Return(containertypes.CreateResponse{ID: "id"}, nil)

	_, err := s.client.ContainerCreate(ctx, cfg, "init-test")
	require.NoError(s.T(), err)
	require.NotNil(s.T(), capturedHost)
	require.NotNil(s.T(), capturedHost.Init, "HostConfig.Init must be set")
	require.True(s.T(), *capturedHost.Init, "HostConfig.Init must be true")
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

func (s *ClientSuite) TestContainerCreateWithLabels() {
	ctx := context.Background()
	cfg := &ContainerConfig{
		Image:  "img:latest",
		Labels: map[string]string{"loop-channel": "ch-1"},
	}

	s.api.On("ContainerCreate", ctx, mock.MatchedBy(func(c *containertypes.Config) bool {
		return c.Labels["app"] == "loop-agent" && c.Labels["loop-channel"] == "ch-1"
	}), mock.Anything, (*network.NetworkingConfig)(nil), (*ocispec.Platform)(nil), "test").
		Return(containertypes.CreateResponse{ID: "labeled-123"}, nil)

	id, err := s.client.ContainerCreate(ctx, cfg, "test")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "labeled-123", id)
	s.api.AssertExpectations(s.T())
}

func (s *ClientSuite) TestContainerCreateWithNetwork() {
	ctx := context.Background()
	cfg := &ContainerConfig{
		Image:       "my-image:latest",
		MemoryMB:    512,
		CPUs:        1.0,
		NetworkName: "loop-net",
		Hostname:    "agent-1",
		Env:         []string{"FOO=bar"},
	}

	s.api.On("ContainerCreate", ctx,
		mock.MatchedBy(func(c *containertypes.Config) bool {
			return c.Image == "my-image:latest" && c.Hostname == "agent-1"
		}),
		mock.AnythingOfType("*container.HostConfig"),
		mock.MatchedBy(func(nc *network.NetworkingConfig) bool {
			if nc == nil {
				return false
			}
			ep, ok := nc.EndpointsConfig["loop-net"]
			return ok && len(ep.Aliases) == 1 && ep.Aliases[0] == "agent-1"
		}),
		(*ocispec.Platform)(nil),
		"net-container",
	).Return(containertypes.CreateResponse{ID: "net-123"}, nil)

	id, err := s.client.ContainerCreate(ctx, cfg, "net-container")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "net-123", id)
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

func (s *ClientSuite) TestContainerStop() {
	ctx := context.Background()
	timeout := 10

	s.api.On("ContainerStop", ctx, "cid-1", containertypes.StopOptions{Timeout: &timeout}).Return(nil)

	err := s.client.ContainerStop(ctx, "cid-1")
	require.NoError(s.T(), err)
	s.api.AssertExpectations(s.T())
}

func (s *ClientSuite) TestContainerStopError() {
	ctx := context.Background()
	timeout := 10

	s.api.On("ContainerStop", ctx, "cid-1", containertypes.StopOptions{Timeout: &timeout}).Return(errors.New("stop failed"))

	err := s.client.ContainerStop(ctx, "cid-1")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "stop failed")
	s.api.AssertExpectations(s.T())
}

func (s *ClientSuite) TestContainerRemove() {
	ctx := context.Background()

	s.api.On("ContainerRemove", ctx, "cid-1", containertypes.RemoveOptions{Force: true, RemoveVolumes: true}).Return(nil)

	err := s.client.ContainerRemove(ctx, "cid-1")
	require.NoError(s.T(), err)
	s.api.AssertExpectations(s.T())
}

func (s *ClientSuite) TestContainerRemoveError() {
	ctx := context.Background()

	s.api.On("ContainerRemove", ctx, "cid-1", containertypes.RemoveOptions{Force: true, RemoveVolumes: true}).Return(errors.New("remove failed"))

	err := s.client.ContainerRemove(ctx, "cid-1")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "remove failed")
	s.api.AssertExpectations(s.T())
}

func (s *ClientSuite) TestNewClientDefault() {
	c, err := NewClient()
	require.NoError(s.T(), err)
	require.NotNil(s.T(), c)
}

func (s *ClientSuite) TestDockerStateToStatus() {
	require.Equal(s.T(), ContainerStatusRunning, dockerStateToStatus("running"))
	require.Equal(s.T(), ContainerStatusStopped, dockerStateToStatus("exited"))
	require.Equal(s.T(), ContainerStatusStopped, dockerStateToStatus("dead"))
	require.Equal(s.T(), ContainerStatusStopped, dockerStateToStatus("created"))
	require.Equal(s.T(), ContainerStatusStopped, dockerStateToStatus("paused"))
	require.Equal(s.T(), ContainerStatusStopped, dockerStateToStatus(""))
}

func (s *ClientSuite) TestImageBuild() {
	s.client.dockerBuildCmd = func(_ context.Context, contextDir, tag string) ([]byte, error) {
		require.Equal(s.T(), "/tmp/ctx", contextDir)
		require.Equal(s.T(), "test:latest", tag)
		return []byte("Successfully built abc123"), nil
	}

	err := s.client.ImageBuild(context.Background(), "/tmp/ctx", "test:latest")
	require.NoError(s.T(), err)
}

func (s *ClientSuite) TestImageBuildError() {
	s.client.dockerBuildCmd = func(_ context.Context, _, _ string) ([]byte, error) {
		return []byte("error: build failed"), errors.New("exit status 1")
	}

	err := s.client.ImageBuild(context.Background(), "/tmp/ctx", "test:latest")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "building image")
	require.Contains(s.T(), err.Error(), "error: build failed")
}

func (s *ClientSuite) TestImageBuildFile() {
	s.client.dockerBuildFileCmd = func(_ context.Context, contextDir, dockerfile, tag string) ([]byte, error) {
		require.Equal(s.T(), "/tmp/ctx", contextDir)
		require.Equal(s.T(), "chrome.Dockerfile", dockerfile)
		require.Equal(s.T(), "loop-chrome:latest", tag)
		return []byte("Successfully built"), nil
	}

	err := s.client.ImageBuildFile(context.Background(), "/tmp/ctx", "chrome.Dockerfile", "loop-chrome:latest")
	require.NoError(s.T(), err)
}

func (s *ClientSuite) TestImageBuildFileError() {
	s.client.dockerBuildFileCmd = func(_ context.Context, _, _, _ string) ([]byte, error) {
		return []byte("error: build failed"), errors.New("exit status 1")
	}

	err := s.client.ImageBuildFile(context.Background(), "/tmp/ctx", "chrome.Dockerfile", "loop-chrome:latest")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "building image")
}

func (s *ClientSuite) TestDefaultDockerBuildFileCmd() {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _ = s.client.defaultDockerBuildFileCmd(ctx, "/nonexistent", "chrome.Dockerfile", "test:latest")
}

func (s *ClientSuite) TestDefaultDockerBuildCmd() {
	// Ensure gitconfigSecretPath returns a path so the --secret branch is covered.
	s.sys.Override("Stat", mock.Anything).Return(nil, nil)
	// Exercise the default function with a cancelled context so it fails fast.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _ = s.client.defaultDockerBuildCmd(ctx, "/nonexistent", "test:latest")
}

func (s *ClientSuite) TestDefaultDockerBuildCmdWithLoopVersion() {
	s.client.SetLoopVersion("v2026.3.23")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _ = s.client.defaultDockerBuildCmd(ctx, "/nonexistent", "test:latest")
}

func (s *ClientSuite) TestSetLoopVersionDev() {
	s.client.SetLoopVersion("dev")
	require.Equal(s.T(), "dev", s.client.loopVersion)
}

func (s *ClientSuite) TestLatestClaudeVersion() {
	s.client.latestClaudeVersion = func() string { return "1.2.3" }
	require.Equal(s.T(), "1.2.3", s.client.LatestClaudeVersion())
}

func (s *ClientSuite) TestGitconfigSecretPath() {
	s.sys.Override("Stat", "/home/testuser/.gitconfig").Return(nil, nil)

	require.Equal(s.T(), "/home/testuser/.gitconfig", s.client.gitconfigSecretPath())
}

func (s *ClientSuite) TestGitconfigSecretPathNotExists() {
	// Default Stat returns os.ErrNotExist, no override needed.
	require.Empty(s.T(), s.client.gitconfigSecretPath())
}

func (s *ClientSuite) TestGitconfigSecretPathHomeDirError() {
	s.sys.Override("UserHomeDir").Return("", errors.New("no home"))

	require.Empty(s.T(), s.client.gitconfigSecretPath())
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
