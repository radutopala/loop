package browser

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"testing"

	containertypes "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/go-connections/nat"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// mockDockerClient implements DockerClient for testing.
type mockDockerClient struct {
	mock.Mock
}

func (m *mockDockerClient) ContainerCreate(ctx context.Context, config *containertypes.Config, hostConfig *containertypes.HostConfig, networkingConfig *network.NetworkingConfig, platform *ocispec.Platform, containerName string) (containertypes.CreateResponse, error) {
	args := m.Called(ctx, config, hostConfig, networkingConfig, platform, containerName)
	return args.Get(0).(containertypes.CreateResponse), args.Error(1)
}

func (m *mockDockerClient) ContainerStart(ctx context.Context, container string, options containertypes.StartOptions) error {
	args := m.Called(ctx, container, options)
	return args.Error(0)
}

func (m *mockDockerClient) ContainerStop(ctx context.Context, containerID string, options containertypes.StopOptions) error {
	args := m.Called(ctx, containerID, options)
	return args.Error(0)
}

func (m *mockDockerClient) ContainerRemove(ctx context.Context, container string, options containertypes.RemoveOptions) error {
	args := m.Called(ctx, container, options)
	return args.Error(0)
}

func (m *mockDockerClient) ContainerInspect(ctx context.Context, containerID string) (containertypes.InspectResponse, error) {
	args := m.Called(ctx, containerID)
	return args.Get(0).(containertypes.InspectResponse), args.Error(1)
}

func (m *mockDockerClient) ContainerList(ctx context.Context, options containertypes.ListOptions) ([]containertypes.Summary, error) {
	args := m.Called(ctx, options)
	return args.Get(0).([]containertypes.Summary), args.Error(1)
}

type ManagerSuite struct {
	suite.Suite
	api *mockDockerClient
	mgr *DockerProvider
}

func TestManagerSuite(t *testing.T) {
	suite.Run(t, new(ManagerSuite))
}

func (s *ManagerSuite) SetupTest() {
	s.api = new(mockDockerClient)
	s.mgr = NewDockerProvider(s.api, "loop-agent:latest", "1920,1080", slog.Default())
}

// inspectResponseWithPort returns a ContainerInspect response with the given host port mapping.
func inspectResponseWithPort(hostPort string) containertypes.InspectResponse {
	resp := containertypes.InspectResponse{
		ContainerJSONBase: &containertypes.ContainerJSONBase{
			State: &containertypes.State{Running: true},
		},
		NetworkSettings: &containertypes.NetworkSettings{},
	}
	resp.NetworkSettings.Ports = nat.PortMap{
		"9222/tcp": []nat.PortBinding{{HostIP: "127.0.0.1", HostPort: hostPort}},
	}
	return resp
}

func inspectRunning() containertypes.InspectResponse {
	return containertypes.InspectResponse{
		ContainerJSONBase: &containertypes.ContainerJSONBase{
			State: &containertypes.State{Running: true},
		},
	}
}

func inspectStopped() containertypes.InspectResponse {
	return containertypes.InspectResponse{
		ContainerJSONBase: &containertypes.ContainerJSONBase{
			State: &containertypes.State{Running: false},
		},
	}
}

func (s *ManagerSuite) TestNewDockerProvider() {
	mgr := NewDockerProvider(s.api, "loop-agent:latest", "1920,1080", slog.Default())
	require.NotNil(s.T(), mgr)
	require.Equal(s.T(), "1920,1080", mgr.screen)
	require.Equal(s.T(), "loop-agent:latest", mgr.image)
}

func (s *ManagerSuite) TestIsHostMode() {
	require.False(s.T(), s.mgr.IsHostMode())
}

func (s *ManagerSuite) TestChromeArgs() {
	args := s.mgr.chromeArgs()
	require.Equal(s.T(), []string{"--window-size=1920,1080", "about:blank"}, args)
}

func (s *ManagerSuite) TestEnsureBrowserNewSession() {
	ctx := context.Background()

	s.api.On("ContainerList", ctx, mock.Anything).
		Return([]containertypes.Summary{}, nil)
	s.api.On("ContainerCreate", ctx, mock.Anything, mock.Anything, (*network.NetworkingConfig)(nil), (*ocispec.Platform)(nil), "loop-chrome-ch-1").
		Return(containertypes.CreateResponse{ID: "chrome-ctr-1"}, nil)
	s.api.On("ContainerStart", ctx, "chrome-ctr-1", containertypes.StartOptions{}).
		Return(nil)
	s.api.On("ContainerInspect", ctx, "chrome-ctr-1").
		Return(inspectResponseWithPort("49152"), nil)

	err := s.mgr.EnsureBrowser(ctx, "ch-1", "")
	require.NoError(s.T(), err)

	cid, ok := s.mgr.GetContainerID("ch-1")
	require.True(s.T(), ok)
	require.Equal(s.T(), "chrome-ctr-1", cid)
	require.Equal(s.T(), "ws://127.0.0.1:49152", s.mgr.GetCDPEndpoint("ch-1"))

	s.api.AssertExpectations(s.T())
}

func (s *ManagerSuite) TestEnsureBrowserAlreadyRunning() {
	ctx := context.Background()

	s.mgr.sessions["ch-1"] = &browserSession{chromeContainerID: "chrome-ctr-1", hostPort: "49152"}
	s.api.On("ContainerInspect", ctx, "chrome-ctr-1").
		Return(inspectRunning(), nil)

	err := s.mgr.EnsureBrowser(ctx, "ch-1", "")
	require.NoError(s.T(), err)

	s.api.AssertNotCalled(s.T(), "ContainerCreate", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func (s *ManagerSuite) TestEnsureBrowserStaleSession() {
	ctx := context.Background()

	s.mgr.sessions["ch-1"] = &browserSession{chromeContainerID: "chrome-old", hostPort: "49000"}
	s.api.On("ContainerInspect", ctx, "chrome-old").
		Return(inspectStopped(), nil)
	s.api.On("ContainerList", ctx, mock.Anything).
		Return([]containertypes.Summary{}, nil)
	s.api.On("ContainerCreate", ctx, mock.Anything, mock.Anything, (*network.NetworkingConfig)(nil), (*ocispec.Platform)(nil), "loop-chrome-ch-1").
		Return(containertypes.CreateResponse{ID: "chrome-new"}, nil)
	s.api.On("ContainerStart", ctx, "chrome-new", containertypes.StartOptions{}).
		Return(nil)
	s.api.On("ContainerInspect", ctx, "chrome-new").
		Return(inspectResponseWithPort("49200"), nil)

	err := s.mgr.EnsureBrowser(ctx, "ch-1", "")
	require.NoError(s.T(), err)

	cid, ok := s.mgr.GetContainerID("ch-1")
	require.True(s.T(), ok)
	require.Equal(s.T(), "chrome-new", cid)
}

func (s *ManagerSuite) TestEnsureBrowserCreateError() {
	ctx := context.Background()

	s.api.On("ContainerList", ctx, mock.Anything).
		Return([]containertypes.Summary{}, nil)
	s.api.On("ContainerCreate", ctx, mock.Anything, mock.Anything, (*network.NetworkingConfig)(nil), (*ocispec.Platform)(nil), "loop-chrome-ch-1").
		Return(containertypes.CreateResponse{}, errors.New("create boom"))

	err := s.mgr.EnsureBrowser(ctx, "ch-1", "")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "creating chrome container")
}

func (s *ManagerSuite) TestEnsureBrowserStartError() {
	ctx := context.Background()

	s.api.On("ContainerList", ctx, mock.Anything).
		Return([]containertypes.Summary{}, nil)
	s.api.On("ContainerCreate", ctx, mock.Anything, mock.Anything, (*network.NetworkingConfig)(nil), (*ocispec.Platform)(nil), "loop-chrome-ch-1").
		Return(containertypes.CreateResponse{ID: "chrome-ctr-1"}, nil)
	s.api.On("ContainerStart", ctx, "chrome-ctr-1", containertypes.StartOptions{}).
		Return(errors.New("start boom"))

	err := s.mgr.EnsureBrowser(ctx, "ch-1", "")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "starting chrome container")
}

func (s *ManagerSuite) TestEnsureBrowserInspectError() {
	ctx := context.Background()

	s.api.On("ContainerList", ctx, mock.Anything).
		Return([]containertypes.Summary{}, nil)
	s.api.On("ContainerCreate", ctx, mock.Anything, mock.Anything, (*network.NetworkingConfig)(nil), (*ocispec.Platform)(nil), "loop-chrome-ch-1").
		Return(containertypes.CreateResponse{ID: "chrome-ctr-1"}, nil)
	s.api.On("ContainerStart", ctx, "chrome-ctr-1", containertypes.StartOptions{}).
		Return(nil)
	s.api.On("ContainerInspect", ctx, "chrome-ctr-1").
		Return(containertypes.InspectResponse{}, errors.New("inspect boom"))

	err := s.mgr.EnsureBrowser(ctx, "ch-1", "")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "getting host port")
}

func (s *ManagerSuite) TestEnsureBrowserNoPortMapping() {
	ctx := context.Background()

	s.api.On("ContainerList", ctx, mock.Anything).
		Return([]containertypes.Summary{}, nil)
	s.api.On("ContainerCreate", ctx, mock.Anything, mock.Anything, (*network.NetworkingConfig)(nil), (*ocispec.Platform)(nil), "loop-chrome-ch-1").
		Return(containertypes.CreateResponse{ID: "chrome-ctr-1"}, nil)
	s.api.On("ContainerStart", ctx, "chrome-ctr-1", containertypes.StartOptions{}).
		Return(nil)
	s.api.On("ContainerInspect", ctx, "chrome-ctr-1").
		Return(containertypes.InspectResponse{
			ContainerJSONBase: &containertypes.ContainerJSONBase{
				State: &containertypes.State{Running: true},
			},
			NetworkSettings: &containertypes.NetworkSettings{},
		}, nil)

	err := s.mgr.EnsureBrowser(ctx, "ch-1", "")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "no host port mapping")
}

func (s *ManagerSuite) TestStopBrowserRunning() {
	ctx := context.Background()

	s.mgr.sessions["ch-1"] = &browserSession{chromeContainerID: "chrome-ctr-1", hostPort: "49152"}

	timeout := 5
	s.api.On("ContainerStop", ctx, "chrome-ctr-1", containertypes.StopOptions{Timeout: &timeout}).
		Return(nil)
	s.api.On("ContainerRemove", ctx, "chrome-ctr-1", containertypes.RemoveOptions{Force: true}).
		Return(nil)

	err := s.mgr.StopBrowser(ctx, "ch-1")
	require.NoError(s.T(), err)

	_, ok := s.mgr.GetContainerID("ch-1")
	require.False(s.T(), ok)
}

func (s *ManagerSuite) TestStopBrowserNoSession() {
	err := s.mgr.StopBrowser(context.Background(), "ch-nonexistent")
	require.NoError(s.T(), err)
}

func (s *ManagerSuite) TestIsRunningTrue() {
	ctx := context.Background()
	s.mgr.sessions["ch-1"] = &browserSession{chromeContainerID: "chrome-ctr-1"}
	s.api.On("ContainerInspect", ctx, "chrome-ctr-1").
		Return(inspectRunning(), nil)
	require.True(s.T(), s.mgr.IsRunning(ctx, "ch-1"))
}

func (s *ManagerSuite) TestIsRunningFalse() {
	ctx := context.Background()
	s.mgr.sessions["ch-1"] = &browserSession{chromeContainerID: "chrome-ctr-1"}
	s.api.On("ContainerInspect", ctx, "chrome-ctr-1").
		Return(inspectStopped(), nil)
	require.False(s.T(), s.mgr.IsRunning(ctx, "ch-1"))
}

func (s *ManagerSuite) TestIsRunningNoSession() {
	require.False(s.T(), s.mgr.IsRunning(context.Background(), "ch-nonexistent"))
}

func (s *ManagerSuite) TestIsRunningInspectError() {
	ctx := context.Background()
	s.mgr.sessions["ch-1"] = &browserSession{chromeContainerID: "chrome-ctr-1"}
	s.api.On("ContainerInspect", ctx, "chrome-ctr-1").
		Return(containertypes.InspectResponse{}, errors.New("inspect error"))
	require.False(s.T(), s.mgr.IsRunning(ctx, "ch-1"))
}

func (s *ManagerSuite) TestGetCDPEndpointWithSession() {
	s.mgr.sessions["ch-1"] = &browserSession{chromeContainerID: "chrome-ctr-1", hostPort: "49152"}
	require.Equal(s.T(), "ws://127.0.0.1:49152", s.mgr.GetCDPEndpoint("ch-1"))
}

func (s *ManagerSuite) TestGetCDPEndpointNoSession() {
	require.Equal(s.T(), "ws://127.0.0.1:9222", s.mgr.GetCDPEndpoint("any-channel"))
}

func (s *ManagerSuite) TestGetCDPEndpointEmptyPort() {
	s.mgr.sessions["ch-1"] = &browserSession{chromeContainerID: "chrome-ctr-1", hostPort: ""}
	require.Equal(s.T(), "ws://127.0.0.1:9222", s.mgr.GetCDPEndpoint("ch-1"))
}

func (s *ManagerSuite) TestGetContainerIDExists() {
	s.mgr.sessions["ch-1"] = &browserSession{chromeContainerID: "chrome-ctr-1"}
	cid, ok := s.mgr.GetContainerID("ch-1")
	require.True(s.T(), ok)
	require.Equal(s.T(), "chrome-ctr-1", cid)
}

func (s *ManagerSuite) TestGetContainerIDNotFound() {
	cid, ok := s.mgr.GetContainerID("ch-nonexistent")
	require.False(s.T(), ok)
	require.Empty(s.T(), cid)
}

func (s *ManagerSuite) TestCleanup() {
	ctx := context.Background()
	s.mgr.sessions["ch-1"] = &browserSession{chromeContainerID: "chrome-ctr-1", hostPort: "49152"}
	s.mgr.sessions["ch-2"] = &browserSession{chromeContainerID: "chrome-ctr-2", hostPort: "49153"}

	timeout := 5
	s.api.On("ContainerStop", ctx, mock.Anything, containertypes.StopOptions{Timeout: &timeout}).Return(nil)
	s.api.On("ContainerRemove", ctx, mock.Anything, containertypes.RemoveOptions{Force: true}).Return(nil)

	s.mgr.Cleanup(ctx)
	require.Empty(s.T(), s.mgr.sessions)
}

func (s *ManagerSuite) TestCleanupWithStopError() {
	ctx := context.Background()
	s.mgr.sessions["ch-1"] = &browserSession{chromeContainerID: "chrome-ctr-1", hostPort: "49152"}

	timeout := 5
	s.api.On("ContainerStop", ctx, "chrome-ctr-1", containertypes.StopOptions{Timeout: &timeout}).
		Return(errors.New("stop error"))
	s.api.On("ContainerRemove", ctx, "chrome-ctr-1", containertypes.RemoveOptions{Force: true}).
		Return(nil)

	s.mgr.Cleanup(ctx)
	require.Empty(s.T(), s.mgr.sessions)
}

func (s *ManagerSuite) TestSessionChannels() {
	require.Empty(s.T(), s.mgr.SessionChannels())

	s.mgr.sessions["ch-1"] = &browserSession{chromeContainerID: "chrome-ctr-1"}
	s.mgr.sessions["ch-2"] = &browserSession{chromeContainerID: "chrome-ctr-2"}

	channels := s.mgr.SessionChannels()
	require.Len(s.T(), channels, 2)
	require.ElementsMatch(s.T(), []string{"ch-1", "ch-2"}, channels)
}

func (s *ManagerSuite) TestChromeBinaryPath() {
	require.Equal(s.T(), "chromium-browser", ChromeBinaryPath())
}

func (s *ManagerSuite) TestCDPPort() {
	require.Equal(s.T(), 9222, CDPPort)
}

func (s *ManagerSuite) TestCDPAddress() {
	require.Equal(s.T(), "0.0.0.0", CDPAddress())
}

func (s *ManagerSuite) TestChromeHostname() {
	require.Equal(s.T(), "loop-chrome-ch-1", ChromeHostname("ch-1"))
	require.Equal(s.T(), "loop-chrome-c0ag3q1gh0q-1773661657-701029", ChromeHostname("C0AG3Q1GH0Q:1773661657.701029"))
}

func (s *ManagerSuite) TestSanitizeID() {
	require.Equal(s.T(), "abc-123", sanitizeID("abc-123"))
	require.Equal(s.T(), "c0ag-1773661657-701029", sanitizeID("C0AG:1773661657.701029"))
	long := "abcdefghijklmnopqrstuvwxyz-1234567890-extra-stuff"
	result := sanitizeID(long)
	require.LessOrEqual(s.T(), len(result), 40)
	require.NotEqual(s.T(), "-", string(result[len(result)-1]))
	require.Equal(s.T(), "hello", sanitizeID("---hello---"))
}

func (s *ManagerSuite) TestEnsureBrowserReusesExistingContainer() {
	ctx := context.Background()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(s.T(), err)
	defer ln.Close()
	_, chromePort, _ := net.SplitHostPort(ln.Addr().String())

	s.api.On("ContainerList", ctx, mock.Anything).
		Return([]containertypes.Summary{
			{ID: "existing-ctr", State: "running"},
		}, nil)
	s.api.On("ContainerInspect", ctx, "existing-ctr").
		Return(inspectResponseWithPort(chromePort), nil)

	err = s.mgr.EnsureBrowser(ctx, "ch-1", "")
	require.NoError(s.T(), err)

	cid, ok := s.mgr.GetContainerID("ch-1")
	require.True(s.T(), ok)
	require.Equal(s.T(), "existing-ctr", cid)

	s.api.AssertNotCalled(s.T(), "ContainerCreate", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func (s *ManagerSuite) TestEnsureBrowserReusesStoppedExistingContainer() {
	ctx := context.Background()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(s.T(), err)
	defer ln.Close()
	_, chromePort, _ := net.SplitHostPort(ln.Addr().String())

	s.api.On("ContainerList", ctx, mock.Anything).
		Return([]containertypes.Summary{
			{ID: "stopped-ctr", State: "exited"},
		}, nil)
	s.api.On("ContainerStart", ctx, "stopped-ctr", containertypes.StartOptions{}).
		Return(nil)
	s.api.On("ContainerInspect", ctx, "stopped-ctr").
		Return(inspectResponseWithPort(chromePort), nil)

	err = s.mgr.EnsureBrowser(ctx, "ch-1", "")
	require.NoError(s.T(), err)

	cid, ok := s.mgr.GetContainerID("ch-1")
	require.True(s.T(), ok)
	require.Equal(s.T(), "stopped-ctr", cid)
}

func (s *ManagerSuite) TestEnsureBrowserRemovesStaleContainer() {
	ctx := context.Background()

	s.api.On("ContainerList", ctx, mock.Anything).
		Return([]containertypes.Summary{
			{ID: "stale-ctr", State: "running"},
		}, nil)
	s.api.On("ContainerInspect", ctx, "stale-ctr").
		Return(inspectResponseWithPort("1"), nil)
	s.api.On("ContainerRemove", ctx, "stale-ctr", containertypes.RemoveOptions{Force: true}).Return(nil)

	s.api.On("ContainerCreate", ctx, mock.Anything, mock.Anything, (*network.NetworkingConfig)(nil), (*ocispec.Platform)(nil), "loop-chrome-ch-1").
		Return(containertypes.CreateResponse{ID: "new-ctr"}, nil)
	s.api.On("ContainerStart", ctx, "new-ctr", containertypes.StartOptions{}).
		Return(nil)
	ln, lnErr := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(s.T(), lnErr)
	defer ln.Close()
	_, newPort, _ := net.SplitHostPort(ln.Addr().String())
	s.api.On("ContainerInspect", ctx, "new-ctr").
		Return(inspectResponseWithPort(newPort), nil)

	err := s.mgr.EnsureBrowser(ctx, "ch-1", "")
	require.NoError(s.T(), err)
	s.api.AssertCalled(s.T(), "ContainerRemove", ctx, "stale-ctr", containertypes.RemoveOptions{Force: true})
}

func (s *ManagerSuite) TestFindExistingChromeStartFails() {
	ctx := context.Background()

	s.api.On("ContainerList", ctx, mock.Anything).
		Return([]containertypes.Summary{
			{ID: "stopped-ctr", State: "exited"},
		}, nil)
	s.api.On("ContainerStart", ctx, "stopped-ctr", containertypes.StartOptions{}).
		Return(errors.New("start failed"))

	s.api.On("ContainerCreate", ctx, mock.Anything, mock.Anything, (*network.NetworkingConfig)(nil), (*ocispec.Platform)(nil), "loop-chrome-ch-1").
		Return(containertypes.CreateResponse{ID: "new-ctr"}, nil)
	s.api.On("ContainerStart", ctx, "new-ctr", containertypes.StartOptions{}).
		Return(nil)
	s.api.On("ContainerInspect", ctx, "new-ctr").
		Return(inspectResponseWithPort("49700"), nil)

	err := s.mgr.EnsureBrowser(ctx, "ch-1", "")
	require.NoError(s.T(), err)

	cid, ok := s.mgr.GetContainerID("ch-1")
	require.True(s.T(), ok)
	require.Equal(s.T(), "new-ctr", cid)
}

func (s *ManagerSuite) TestFindExistingChromeContainerListError() {
	ctx := context.Background()

	s.api.On("ContainerList", ctx, mock.Anything).
		Return([]containertypes.Summary{}, errors.New("list failed"))

	s.api.On("ContainerCreate", ctx, mock.Anything, mock.Anything, (*network.NetworkingConfig)(nil), (*ocispec.Platform)(nil), "loop-chrome-ch-1").
		Return(containertypes.CreateResponse{ID: "new-ctr"}, nil)
	s.api.On("ContainerStart", ctx, "new-ctr", containertypes.StartOptions{}).
		Return(nil)
	s.api.On("ContainerInspect", ctx, "new-ctr").
		Return(inspectResponseWithPort("49800"), nil)

	err := s.mgr.EnsureBrowser(ctx, "ch-1", "")
	require.NoError(s.T(), err)

	cid, ok := s.mgr.GetContainerID("ch-1")
	require.True(s.T(), ok)
	require.Equal(s.T(), "new-ctr", cid)
}

func (s *ManagerSuite) TestIsChromeReachableEmptyPort() {
	require.False(s.T(), isChromeReachable(""))
}

func (s *ManagerSuite) TestIsChromeReachableConnectionRefused() {
	require.False(s.T(), isChromeReachable("1"))
}

func (s *ManagerSuite) TestIsChromeReachableSuccess() {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(s.T(), err)
	defer ln.Close()
	require.True(s.T(), isChromeReachable(ln.Addr().String()))
}
