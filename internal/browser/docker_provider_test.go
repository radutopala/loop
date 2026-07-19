package browser

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"testing"
	"time"

	containertypes "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/go-connections/nat"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/container"
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
	// Default to host-run daemon (the common deployment): the CDP endpoint is the
	// mapped 127.0.0.1:hostPort and no extra inspect is needed. Containerized
	// behavior is exercised explicitly by setting inContainer=true per test.
	s.mgr.inContainer = false
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

	containerID, err := s.mgr.StopBrowser(ctx, "ch-1")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "chrome-ctr-1", containerID)

	_, ok := s.mgr.GetContainerID("ch-1")
	require.False(s.T(), ok)
}

func (s *ManagerSuite) TestStopBrowserNoSession() {
	containerID, err := s.mgr.StopBrowser(context.Background(), "ch-nonexistent")
	require.NoError(s.T(), err)
	require.Empty(s.T(), containerID)
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
	s.mgr.sessions["ch-1"] = &browserSession{chromeContainerID: "chrome-ctr-1", hostPort: "49152", cdpAddr: "127.0.0.1:49152"}
	require.Equal(s.T(), "ws://127.0.0.1:49152", s.mgr.GetCDPEndpoint("ch-1"))
}

// When loop runs inside a container, the endpoint uses the sidecar's bridge IP
// on the in-container CDP port, not the (unreachable) host-published port.
func (s *ManagerSuite) TestGetCDPEndpointContainerized() {
	s.mgr.sessions["ch-1"] = &browserSession{chromeContainerID: "chrome-ctr-1", hostPort: "49152", cdpAddr: "172.17.0.5:9222"}
	require.Equal(s.T(), "ws://172.17.0.5:9222", s.mgr.GetCDPEndpoint("ch-1"))
}

// resolveCDPHostPort: host-run daemon uses the mapped 127.0.0.1:hostPort (no inspect).
func (s *ManagerSuite) TestResolveCDPHostPortHostMode() {
	s.mgr.inContainer = false
	require.Equal(s.T(), "127.0.0.1:49152", s.mgr.resolveCDPHostPort(context.Background(), "ctr-1", "49152"))
}

// resolveCDPHostPort: containerized daemon uses the sidecar bridge IP:9222.
func (s *ManagerSuite) TestResolveCDPHostPortContainerized() {
	s.mgr.inContainer = true
	s.api.On("ContainerInspect", mock.Anything, "ctr-1").Return(containertypes.InspectResponse{
		NetworkSettings: &containertypes.NetworkSettings{
			Networks: map[string]*network.EndpointSettings{"bridge": {IPAddress: "172.18.0.7"}},
		},
	}, nil)
	require.Equal(s.T(), "172.18.0.7:9222", s.mgr.resolveCDPHostPort(context.Background(), "ctr-1", "49152"))
}

// resolveCDPHostPort: containerized but IP undiscoverable → host-port fallback.
func (s *ManagerSuite) TestResolveCDPHostPortContainerizedNoIP() {
	s.mgr.inContainer = true
	s.api.On("ContainerInspect", mock.Anything, "ctr-1").Return(containertypes.InspectResponse{
		NetworkSettings: &containertypes.NetworkSettings{},
	}, nil)
	require.Equal(s.T(), "127.0.0.1:49152", s.mgr.resolveCDPHostPort(context.Background(), "ctr-1", "49152"))
}

// getContainerIP: inspect error → "".
func (s *ManagerSuite) TestGetContainerIPInspectError() {
	s.api.On("ContainerInspect", mock.Anything, "ctr-1").Return(containertypes.InspectResponse{}, errors.New("boom"))
	require.Equal(s.T(), "", s.mgr.getContainerIP(context.Background(), "ctr-1"))
}

// getContainerIP: nil NetworkSettings → "".
func (s *ManagerSuite) TestGetContainerIPNilNetworkSettings() {
	s.api.On("ContainerInspect", mock.Anything, "ctr-1").Return(containertypes.InspectResponse{}, nil)
	require.Equal(s.T(), "", s.mgr.getContainerIP(context.Background(), "ctr-1"))
}

// getContainerIP: falls back to a non-bridge network when "bridge" is absent.
func (s *ManagerSuite) TestGetContainerIPNonBridgeNetwork() {
	s.api.On("ContainerInspect", mock.Anything, "ctr-1").Return(containertypes.InspectResponse{
		NetworkSettings: &containertypes.NetworkSettings{
			Networks: map[string]*network.EndpointSettings{"loopnet": {IPAddress: "10.5.0.3"}},
		},
	}, nil)
	require.Equal(s.T(), "10.5.0.3", s.mgr.getContainerIP(context.Background(), "ctr-1"))
}

// inDockerContainer probes for the /.dockerenv marker; just exercise it.
func (s *ManagerSuite) TestInDockerContainer() {
	_ = inDockerContainer()
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

	s.mgr.Cleanup(ctx)
	require.Empty(s.T(), s.mgr.sessions)
}

func (s *ManagerSuite) TestCleanupWithStopError() {
	ctx := context.Background()
	s.mgr.sessions["ch-1"] = &browserSession{chromeContainerID: "chrome-ctr-1", hostPort: "49152"}

	timeout := 5
	s.api.On("ContainerStop", ctx, "chrome-ctr-1", containertypes.StopOptions{Timeout: &timeout}).
		Return(errors.New("stop error"))

	s.mgr.Cleanup(ctx)
	require.Empty(s.T(), s.mgr.sessions)
}

func (s *ManagerSuite) TestCleanupWithRegistry() {
	ctx := context.Background()

	reg := new(mockContainerRegistry)
	s.mgr.SetContainerRegistry(reg)

	s.mgr.sessions["ch-1"] = &browserSession{chromeContainerID: "chrome-ctr-1", hostPort: "49152"}

	timeout := 5
	s.api.On("ContainerStop", ctx, "chrome-ctr-1", containertypes.StopOptions{Timeout: &timeout}).Return(nil)

	reg.On("UpdateStatus", "chrome-ctr-1", container.ContainerStatusStopped)
	reg.On("RemoveContainer", ctx, "chrome-ctr-1").Return(nil)

	s.mgr.Cleanup(ctx)
	require.Empty(s.T(), s.mgr.sessions)
	reg.AssertCalled(s.T(), "RemoveContainer", ctx, "chrome-ctr-1")
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
	// sanitizeID was consolidated into container.SanitizeName.
	require.Equal(s.T(), "abc-123", container.SanitizeName("abc-123"))
	require.Equal(s.T(), "c0ag-1773661657-701029", container.SanitizeName("C0AG:1773661657.701029"))
	long := "abcdefghijklmnopqrstuvwxyz-1234567890-extra-stuff"
	result := container.SanitizeName(long)
	require.LessOrEqual(s.T(), len(result), 40)
	require.NotEqual(s.T(), "-", string(result[len(result)-1]))
	require.Equal(s.T(), "hello", container.SanitizeName("---hello---"))
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

func (s *ManagerSuite) TestEnsureBrowserReusesExistingContainerRegistersInRegistry() {
	ctx := context.Background()

	reg := new(mockContainerRegistry)
	s.mgr.SetContainerRegistry(reg)

	reg.On("FindByChannelAndType", "ch-1", container.ContainerTypeChrome).
		Return((*container.ContainerInfo)(nil))
	reg.On("Register", mock.MatchedBy(func(info *container.ContainerInfo) bool {
		return info.ContainerID == "existing-ctr" &&
			info.ChannelID == "ch-1" &&
			info.Type == container.ContainerTypeChrome
	})).Once()

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
	reg.AssertExpectations(s.T())
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
	s.api.On("ContainerRemove", ctx, "stale-ctr", containertypes.RemoveOptions{Force: true, RemoveVolumes: true}).Return(nil)

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
	s.api.AssertCalled(s.T(), "ContainerRemove", ctx, "stale-ctr", containertypes.RemoveOptions{Force: true, RemoveVolumes: true})
}

func (s *ManagerSuite) TestEnsureBrowserRemovesStaleContainerUnregisters() {
	ctx := context.Background()

	reg := new(mockContainerRegistry)
	s.mgr.SetContainerRegistry(reg)

	// Registry returns nil — no fast path.
	reg.On("FindByChannelAndType", "ch-1", container.ContainerTypeChrome).
		Return((*container.ContainerInfo)(nil))
	// The stale container found via Docker list should be unregistered.
	reg.On("Unregister", "stale-ctr").Once()
	reg.On("Register", mock.Anything).Once()

	s.api.On("ContainerList", ctx, mock.Anything).
		Return([]containertypes.Summary{
			{ID: "stale-ctr", State: "running"},
		}, nil)
	s.api.On("ContainerInspect", ctx, "stale-ctr").
		Return(inspectResponseWithPort("1"), nil) // unreachable port
	s.api.On("ContainerRemove", ctx, "stale-ctr", containertypes.RemoveOptions{Force: true, RemoveVolumes: true}).Return(nil)

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
	reg.AssertCalled(s.T(), "Unregister", "stale-ctr")
	reg.AssertExpectations(s.T())
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

// mockContainerRegistry implements container.ContainerRegistry for testing.
type mockContainerRegistry struct {
	mock.Mock
}

func (m *mockContainerRegistry) Register(info *container.ContainerInfo) *container.ContainerInfo {
	m.Called(info)
	return info
}
func (m *mockContainerRegistry) Unregister(containerID string) { m.Called(containerID) }
func (m *mockContainerRegistry) UpdateStatus(containerID string, s container.ContainerStatus) {
	m.Called(containerID, s)
}
func (m *mockContainerRegistry) List() []*container.ContainerInfo                { return nil }
func (m *mockContainerRegistry) ListByChannel(string) []*container.ContainerInfo { return nil }
func (m *mockContainerRegistry) FindByChannelAndType(channelID string, ct container.ContainerType) *container.ContainerInfo {
	args := m.Called(channelID, ct)
	if v := args.Get(0); v != nil {
		return v.(*container.ContainerInfo)
	}
	return nil
}
func (m *mockContainerRegistry) RunningChannelIDs(context.Context) map[string]struct{} {
	return nil
}
func (m *mockContainerRegistry) RemoveContainer(ctx context.Context, containerID string) error {
	return m.Called(ctx, containerID).Error(0)
}
func (m *mockContainerRegistry) ScheduleRemove(string, time.Duration) {}
func (m *mockContainerRegistry) FindOrCreateShell(context.Context, string, string, string) (string, error) {
	return "", nil
}

func (s *ManagerSuite) TestEnsureBrowserRegistersContainer() {
	ctx := context.Background()

	reg := new(mockContainerRegistry)
	s.mgr.SetContainerRegistry(reg)

	reg.On("FindByChannelAndType", "ch-1", container.ContainerTypeChrome).
		Return((*container.ContainerInfo)(nil))
	reg.On("Register", mock.MatchedBy(func(info *container.ContainerInfo) bool {
		return info.ContainerID == "chrome-ctr-1" &&
			info.ChannelID == "ch-1" &&
			info.Type == container.ContainerTypeChrome &&
			info.ContainerName == "loop-chrome-ch-1"
	})).Once()

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

	reg.AssertExpectations(s.T())
}

func (s *ManagerSuite) TestEnsureBrowserRegistryFastPathReuse() {
	ctx := context.Background()

	reg := new(mockContainerRegistry)
	s.mgr.SetContainerRegistry(reg)

	// Start a listener so isChromeReachable returns true.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(s.T(), err)
	defer ln.Close()
	_, chromePort, _ := net.SplitHostPort(ln.Addr().String())

	reg.On("FindByChannelAndType", "ch-1", container.ContainerTypeChrome).
		Return(&container.ContainerInfo{
			ContainerID:   "registry-ctr",
			ChannelID:     "ch-1",
			Type:          container.ContainerTypeChrome,
			ContainerName: "loop-chrome-ch-1",
		})

	// Container is running and has a port mapping.
	s.api.On("ContainerInspect", ctx, "registry-ctr").
		Return(inspectResponseWithPort(chromePort), nil)

	err = s.mgr.EnsureBrowser(ctx, "ch-1", "")
	require.NoError(s.T(), err)

	cid, ok := s.mgr.GetContainerID("ch-1")
	require.True(s.T(), ok)
	require.Equal(s.T(), "registry-ctr", cid)

	// Should NOT call ContainerList — fast path skips Docker list query.
	s.api.AssertNotCalled(s.T(), "ContainerList", mock.Anything, mock.Anything)
	reg.AssertExpectations(s.T())
}

func (s *ManagerSuite) TestEnsureBrowserRegistryStaleContainerCleanup() {
	ctx := context.Background()

	reg := new(mockContainerRegistry)
	s.mgr.SetContainerRegistry(reg)

	// Registry says it exists but container is not running.
	reg.On("FindByChannelAndType", "ch-1", container.ContainerTypeChrome).
		Return(&container.ContainerInfo{
			ContainerID:   "stale-reg-ctr",
			ChannelID:     "ch-1",
			Type:          container.ContainerTypeChrome,
			ContainerName: "loop-chrome-ch-1",
		})
	reg.On("Unregister", "stale-reg-ctr").Once()
	reg.On("Register", mock.Anything).Once()

	// Container is not running.
	s.api.On("ContainerInspect", ctx, "stale-reg-ctr").
		Return(inspectStopped(), nil)
	// Clean up stale container.
	s.api.On("ContainerRemove", ctx, "stale-reg-ctr", containertypes.RemoveOptions{Force: true, RemoveVolumes: true}).Return(nil)

	// Falls through to findExistingChrome then create.
	s.api.On("ContainerList", ctx, mock.Anything).
		Return([]containertypes.Summary{}, nil)
	s.api.On("ContainerCreate", ctx, mock.Anything, mock.Anything, (*network.NetworkingConfig)(nil), (*ocispec.Platform)(nil), "loop-chrome-ch-1").
		Return(containertypes.CreateResponse{ID: "new-ctr"}, nil)
	s.api.On("ContainerStart", ctx, "new-ctr", containertypes.StartOptions{}).
		Return(nil)
	s.api.On("ContainerInspect", ctx, "new-ctr").
		Return(inspectResponseWithPort("49300"), nil)

	err := s.mgr.EnsureBrowser(ctx, "ch-1", "")
	require.NoError(s.T(), err)

	cid, ok := s.mgr.GetContainerID("ch-1")
	require.True(s.T(), ok)
	require.Equal(s.T(), "new-ctr", cid)

	s.api.AssertCalled(s.T(), "ContainerRemove", ctx, "stale-reg-ctr", containertypes.RemoveOptions{Force: true, RemoveVolumes: true})
	reg.AssertExpectations(s.T())
}

func (s *ManagerSuite) TestStopBrowserUpdatesStatusToStopped() {
	ctx := context.Background()

	reg := new(mockContainerRegistry)
	s.mgr.SetContainerRegistry(reg)

	reg.On("UpdateStatus", "chrome-ctr-1", container.ContainerStatusStopped).Once()

	s.mgr.sessions["ch-1"] = &browserSession{chromeContainerID: "chrome-ctr-1", hostPort: "49152"}

	timeout := 5
	s.api.On("ContainerStop", ctx, "chrome-ctr-1", containertypes.StopOptions{Timeout: &timeout}).Return(nil)

	containerID, err := s.mgr.StopBrowser(ctx, "ch-1")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "chrome-ctr-1", containerID)

	reg.AssertExpectations(s.T())
}

func (s *ManagerSuite) TestSetContainerRegistry() {
	require.Nil(s.T(), s.mgr.registry)

	reg := new(mockContainerRegistry)
	s.mgr.SetContainerRegistry(reg)
	require.NotNil(s.T(), s.mgr.registry)
}

func (s *ManagerSuite) TestEnsureBrowserNilRegistryNoError() {
	ctx := context.Background()

	// registry is nil by default — should not panic.
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
}

// listForChrome matches a ContainerList call filtering on the given Chrome
// container name, so per-channel expectations don't cross wires in the
// concurrency tests below.
func listForChrome(name string) any {
	return mock.MatchedBy(func(o containertypes.ListOptions) bool {
		vals := o.Filters.Get("name")
		return len(vals) == 1 && vals[0] == name
	})
}

// TestEnsureBrowserCrossChannelNotBlocked guards the per-channel locking:
// channel A's EnsureBrowser is parked inside a slow Docker call, and channel
// B's EnsureBrowser must still complete. Under the old whole-provider mutex
// this deadlocks (B waits on A's Docker I/O) and the test times out.
func (s *ManagerSuite) TestEnsureBrowserCrossChannelNotBlocked() {
	nameA := ChromeHostname("chan-a")
	nameB := ChromeHostname("chan-b")
	entered := make(chan struct{})
	release := make(chan struct{})

	// Channel A blocks in ContainerList until released, then creates.
	s.api.On("ContainerList", mock.Anything, listForChrome(nameA)).
		Run(func(mock.Arguments) { close(entered); <-release }).
		Return([]containertypes.Summary{}, nil).Once()
	s.api.On("ContainerCreate", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, nameA).
		Return(containertypes.CreateResponse{ID: "ctr-a"}, nil).Once()
	s.api.On("ContainerStart", mock.Anything, "ctr-a", mock.Anything).Return(nil).Once()
	s.api.On("ContainerInspect", mock.Anything, "ctr-a").Return(inspectResponseWithPort("40001"), nil)

	// Channel B completes promptly.
	s.api.On("ContainerList", mock.Anything, listForChrome(nameB)).
		Return([]containertypes.Summary{}, nil).Once()
	s.api.On("ContainerCreate", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, nameB).
		Return(containertypes.CreateResponse{ID: "ctr-b"}, nil).Once()
	s.api.On("ContainerStart", mock.Anything, "ctr-b", mock.Anything).Return(nil).Once()
	s.api.On("ContainerInspect", mock.Anything, "ctr-b").Return(inspectResponseWithPort("40002"), nil)

	done := make(chan error, 1)
	go func() { done <- s.mgr.EnsureBrowser(context.Background(), "chan-a", "") }()
	<-entered // A now holds only chan-a's lock, mid Docker I/O.

	require.NoError(s.T(), s.mgr.EnsureBrowser(context.Background(), "chan-b", ""),
		"channel B must not be blocked by channel A's in-flight Docker call")

	close(release)
	require.NoError(s.T(), <-done)
}

// TestEnsureBrowserSameChannelSerialized guards the other half of the locking
// contract: two concurrent ensures for the SAME channel must not race to
// create two containers — the second waits, then adopts the first's session.
func (s *ManagerSuite) TestEnsureBrowserSameChannelSerialized() {
	name := ChromeHostname("chan-serial")
	entered := make(chan struct{})
	release := make(chan struct{})

	// Only ONE create flow is expected (.Once() makes a second create fail).
	s.api.On("ContainerList", mock.Anything, listForChrome(name)).
		Run(func(mock.Arguments) { close(entered); <-release }).
		Return([]containertypes.Summary{}, nil).Once()
	s.api.On("ContainerCreate", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, name).
		Return(containertypes.CreateResponse{ID: "ctr-s"}, nil).Once()
	s.api.On("ContainerStart", mock.Anything, "ctr-s", mock.Anything).Return(nil).Once()
	// Inspect serves the first flow's port lookup AND the second flow's
	// running-check on the adopted session.
	s.api.On("ContainerInspect", mock.Anything, "ctr-s").Return(inspectResponseWithPort("40003"), nil)

	first := make(chan error, 1)
	second := make(chan error, 1)
	go func() { first <- s.mgr.EnsureBrowser(context.Background(), "chan-serial", "") }()
	<-entered
	go func() { second <- s.mgr.EnsureBrowser(context.Background(), "chan-serial", "") }()
	close(release)

	require.NoError(s.T(), <-first)
	require.NoError(s.T(), <-second)
	s.api.AssertExpectations(s.T())
}

// TestChannelLockSameMutex pins the per-channel lock identity: repeated calls
// for one channel return the same mutex; different channels get different ones.
func (s *ManagerSuite) TestChannelLockSameMutex() {
	a1 := s.mgr.channelLock("ch-a")
	a2 := s.mgr.channelLock("ch-a")
	b := s.mgr.channelLock("ch-b")
	require.Same(s.T(), a1, a2)
	require.NotSame(s.T(), a1, b)
}
