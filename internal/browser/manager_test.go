package browser

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
	mgr *Manager
}

func TestManagerSuite(t *testing.T) {
	suite.Run(t, new(ManagerSuite))
}

func (s *ManagerSuite) SetupTest() {
	s.api = new(mockDockerClient)
	s.mgr = NewManager(s.api, "loop-agent:latest", "1920,1080", slog.Default())
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

// inspectRunning returns a ContainerInspect response showing a running container.
func inspectRunning() containertypes.InspectResponse {
	return containertypes.InspectResponse{
		ContainerJSONBase: &containertypes.ContainerJSONBase{
			State: &containertypes.State{Running: true},
		},
	}
}

// inspectStopped returns a ContainerInspect response showing a stopped container.
func inspectStopped() containertypes.InspectResponse {
	return containertypes.InspectResponse{
		ContainerJSONBase: &containertypes.ContainerJSONBase{
			State: &containertypes.State{Running: false},
		},
	}
}

func (s *ManagerSuite) TestNewManager() {
	mgr := NewManager(s.api, "loop-agent:latest", "1920,1080", slog.Default())
	require.NotNil(s.T(), mgr)
	require.Equal(s.T(), "1920,1080", mgr.screen)
	require.Equal(s.T(), "loop-agent:latest", mgr.image)
}

func (s *ManagerSuite) TestChromeArgs() {
	args := s.mgr.chromeArgs()
	require.Equal(s.T(), []string{"--window-size=1920,1080", "about:blank"}, args)
}

func (s *ManagerSuite) TestEnsureBrowserNewSession() {
	ctx := context.Background()

	// ContainerList returns no existing container.
	s.api.On("ContainerList", ctx, mock.Anything).
		Return([]containertypes.Summary{}, nil)

	// ContainerCreate succeeds (no network config — nil passed).
	s.api.On("ContainerCreate", ctx, mock.Anything, mock.Anything, (*network.NetworkingConfig)(nil), (*ocispec.Platform)(nil), "loop-chrome-ch-1").
		Return(containertypes.CreateResponse{ID: "chrome-ctr-1"}, nil)

	// ContainerStart succeeds.
	s.api.On("ContainerStart", ctx, "chrome-ctr-1", containertypes.StartOptions{}).
		Return(nil)

	// ContainerInspect returns port mapping.
	s.api.On("ContainerInspect", ctx, "chrome-ctr-1").
		Return(inspectResponseWithPort("49152"), nil)

	err := s.mgr.EnsureBrowser(ctx, "ch-1", "")
	require.NoError(s.T(), err)

	// Verify session was stored.
	cid, ok := s.mgr.GetContainerID("ch-1")
	require.True(s.T(), ok)
	require.Equal(s.T(), "chrome-ctr-1", cid)

	// Verify CDP endpoint uses the host port.
	require.Equal(s.T(), "ws://127.0.0.1:49152", s.mgr.GetCDPEndpoint("ch-1"))

	s.api.AssertExpectations(s.T())
}

func (s *ManagerSuite) TestEnsureBrowserAlreadyRunning() {
	ctx := context.Background()

	// Pre-populate session.
	s.mgr.sessions["ch-1"] = &browserSession{chromeContainerID: "chrome-ctr-1", hostPort: "49152"}

	// ContainerInspect shows running.
	s.api.On("ContainerInspect", ctx, "chrome-ctr-1").
		Return(inspectRunning(), nil)

	err := s.mgr.EnsureBrowser(ctx, "ch-1", "")
	require.NoError(s.T(), err)

	// No create should have been called.
	s.api.AssertNotCalled(s.T(), "ContainerCreate", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func (s *ManagerSuite) TestEnsureBrowserStaleSession() {
	ctx := context.Background()

	// Pre-populate a stale session (container no longer running).
	s.mgr.sessions["ch-1"] = &browserSession{chromeContainerID: "chrome-old", hostPort: "49000"}

	// ContainerInspect shows not running.
	s.api.On("ContainerInspect", ctx, "chrome-old").
		Return(inspectStopped(), nil)

	// ContainerList returns no existing container.
	s.api.On("ContainerList", ctx, mock.Anything).
		Return([]containertypes.Summary{}, nil)

	// ContainerCreate succeeds (no network config).
	s.api.On("ContainerCreate", ctx, mock.Anything, mock.Anything, (*network.NetworkingConfig)(nil), (*ocispec.Platform)(nil), "loop-chrome-ch-1").
		Return(containertypes.CreateResponse{ID: "chrome-new"}, nil)

	// ContainerStart succeeds.
	s.api.On("ContainerStart", ctx, "chrome-new", containertypes.StartOptions{}).
		Return(nil)

	// ContainerInspect for port mapping on new container.
	s.api.On("ContainerInspect", ctx, "chrome-new").
		Return(inspectResponseWithPort("49200"), nil)

	err := s.mgr.EnsureBrowser(ctx, "ch-1", "")
	require.NoError(s.T(), err)

	cid, ok := s.mgr.GetContainerID("ch-1")
	require.True(s.T(), ok)
	require.Equal(s.T(), "chrome-new", cid)

	s.api.AssertExpectations(s.T())
}

func (s *ManagerSuite) TestEnsureBrowserCreateError() {
	ctx := context.Background()

	// ContainerList returns no existing container.
	s.api.On("ContainerList", ctx, mock.Anything).
		Return([]containertypes.Summary{}, nil)

	// ContainerCreate fails.
	s.api.On("ContainerCreate", ctx, mock.Anything, mock.Anything, (*network.NetworkingConfig)(nil), (*ocispec.Platform)(nil), "loop-chrome-ch-1").
		Return(containertypes.CreateResponse{}, errors.New("create boom"))

	err := s.mgr.EnsureBrowser(ctx, "ch-1", "")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "creating chrome container")
}

func (s *ManagerSuite) TestEnsureBrowserStartError() {
	ctx := context.Background()

	// ContainerList returns no existing container.
	s.api.On("ContainerList", ctx, mock.Anything).
		Return([]containertypes.Summary{}, nil)

	// ContainerCreate succeeds.
	s.api.On("ContainerCreate", ctx, mock.Anything, mock.Anything, (*network.NetworkingConfig)(nil), (*ocispec.Platform)(nil), "loop-chrome-ch-1").
		Return(containertypes.CreateResponse{ID: "chrome-ctr-1"}, nil)

	// ContainerStart fails.
	s.api.On("ContainerStart", ctx, "chrome-ctr-1", containertypes.StartOptions{}).
		Return(errors.New("start boom"))

	err := s.mgr.EnsureBrowser(ctx, "ch-1", "")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "starting chrome container")
}

func (s *ManagerSuite) TestEnsureBrowserInspectError() {
	ctx := context.Background()

	// ContainerList returns no existing container.
	s.api.On("ContainerList", ctx, mock.Anything).
		Return([]containertypes.Summary{}, nil)

	// ContainerCreate succeeds.
	s.api.On("ContainerCreate", ctx, mock.Anything, mock.Anything, (*network.NetworkingConfig)(nil), (*ocispec.Platform)(nil), "loop-chrome-ch-1").
		Return(containertypes.CreateResponse{ID: "chrome-ctr-1"}, nil)

	// ContainerStart succeeds.
	s.api.On("ContainerStart", ctx, "chrome-ctr-1", containertypes.StartOptions{}).
		Return(nil)

	// ContainerInspect fails.
	s.api.On("ContainerInspect", ctx, "chrome-ctr-1").
		Return(containertypes.InspectResponse{}, errors.New("inspect boom"))

	err := s.mgr.EnsureBrowser(ctx, "ch-1", "")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "getting host port")
}

func (s *ManagerSuite) TestEnsureBrowserNoPortMapping() {
	ctx := context.Background()

	// ContainerList returns no existing container.
	s.api.On("ContainerList", ctx, mock.Anything).
		Return([]containertypes.Summary{}, nil)

	// ContainerCreate succeeds.
	s.api.On("ContainerCreate", ctx, mock.Anything, mock.Anything, (*network.NetworkingConfig)(nil), (*ocispec.Platform)(nil), "loop-chrome-ch-1").
		Return(containertypes.CreateResponse{ID: "chrome-ctr-1"}, nil)

	// ContainerStart succeeds.
	s.api.On("ContainerStart", ctx, "chrome-ctr-1", containertypes.StartOptions{}).
		Return(nil)

	// ContainerInspect returns no port mapping.
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

	// Session should be removed.
	_, ok := s.mgr.GetContainerID("ch-1")
	require.False(s.T(), ok)

	s.api.AssertExpectations(s.T())
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
	s.api.AssertExpectations(s.T())
	s.api.AssertNotCalled(s.T(), "NetworkRemove", mock.Anything, mock.Anything)
}

func (s *ManagerSuite) TestCleanupWithStopError() {
	ctx := context.Background()

	s.mgr.sessions["ch-1"] = &browserSession{chromeContainerID: "chrome-ctr-1", hostPort: "49152"}

	timeout := 5
	s.api.On("ContainerStop", ctx, "chrome-ctr-1", containertypes.StopOptions{Timeout: &timeout}).
		Return(errors.New("stop error"))
	s.api.On("ContainerRemove", ctx, "chrome-ctr-1", containertypes.RemoveOptions{Force: true}).
		Return(nil)

	// Should not panic; error is logged.
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
	// Slack thread IDs with colons and dots are sanitized.
	require.Equal(s.T(), "loop-chrome-c0ag3q1gh0q-1773661657-701029", ChromeHostname("C0AG3Q1GH0Q:1773661657.701029"))
}

func (s *ManagerSuite) TestSanitizeID() {
	// Basic passthrough.
	require.Equal(s.T(), "abc-123", sanitizeID("abc-123"))
	// Uppercase lowered, special chars replaced.
	require.Equal(s.T(), "c0ag-1773661657-701029", sanitizeID("C0AG:1773661657.701029"))
	// Long IDs truncated to 40 chars with trailing hyphens trimmed.
	long := "abcdefghijklmnopqrstuvwxyz-1234567890-extra-stuff"
	result := sanitizeID(long)
	require.LessOrEqual(s.T(), len(result), 40)
	require.NotEqual(s.T(), "-", string(result[len(result)-1]))
	// Leading/trailing special chars trimmed.
	require.Equal(s.T(), "hello", sanitizeID("---hello---"))
}

func (s *ManagerSuite) TestEnsureBrowserReusesExistingContainer() {
	ctx := context.Background()

	// Start a fake Chrome CDP endpoint so isChromeReachable returns true.
	chromeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"Browser":"Chrome"}`)
	}))
	defer chromeSrv.Close()
	// Extract the port from the test server URL.
	chromePort := strings.SplitAfter(chromeSrv.URL, ":")[len(strings.SplitAfter(chromeSrv.URL, ":"))-1]

	// ContainerList finds an existing container, already running.
	s.api.On("ContainerList", ctx, mock.Anything).
		Return([]containertypes.Summary{
			{ID: "existing-ctr", State: "running"},
		}, nil)

	// ContainerInspect returns port mapping with the real test server port.
	s.api.On("ContainerInspect", ctx, "existing-ctr").
		Return(inspectResponseWithPort(chromePort), nil)

	err := s.mgr.EnsureBrowser(ctx, "ch-1", "")
	require.NoError(s.T(), err)

	cid, ok := s.mgr.GetContainerID("ch-1")
	require.True(s.T(), ok)
	require.Equal(s.T(), "existing-ctr", cid)

	// No ContainerCreate should have been called.
	s.api.AssertNotCalled(s.T(), "ContainerCreate", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func (s *ManagerSuite) TestEnsureBrowserReusesStoppedExistingContainer() {
	ctx := context.Background()

	// Start a fake Chrome CDP endpoint so isChromeReachable returns true.
	chromeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"Browser":"Chrome"}`)
	}))
	defer chromeSrv.Close()
	chromePort := strings.SplitAfter(chromeSrv.URL, ":")[len(strings.SplitAfter(chromeSrv.URL, ":"))-1]

	// ContainerList finds an existing container, stopped.
	s.api.On("ContainerList", ctx, mock.Anything).
		Return([]containertypes.Summary{
			{ID: "stopped-ctr", State: "exited"},
		}, nil)

	// ContainerStart to restart it.
	s.api.On("ContainerStart", ctx, "stopped-ctr", containertypes.StartOptions{}).
		Return(nil)

	// ContainerInspect returns port mapping with the real test server port.
	s.api.On("ContainerInspect", ctx, "stopped-ctr").
		Return(inspectResponseWithPort(chromePort), nil)

	err := s.mgr.EnsureBrowser(ctx, "ch-1", "")
	require.NoError(s.T(), err)

	cid, ok := s.mgr.GetContainerID("ch-1")
	require.True(s.T(), ok)
	require.Equal(s.T(), "stopped-ctr", cid)
}

func (s *ManagerSuite) TestEnsureBrowserRemovesStaleContainer() {
	ctx := context.Background()

	// ContainerList finds an existing running container, but Chrome is unreachable.
	s.api.On("ContainerList", ctx, mock.Anything).
		Return([]containertypes.Summary{
			{ID: "stale-ctr", State: "running"},
		}, nil)
	// Port 1 is unreachable — isChromeReachable returns false.
	s.api.On("ContainerInspect", ctx, "stale-ctr").
		Return(inspectResponseWithPort("1"), nil)

	// Stale container should be removed.
	s.api.On("ContainerRemove", ctx, "stale-ctr", containertypes.RemoveOptions{Force: true}).Return(nil)

	// Then a new container is created.
	s.api.On("ContainerCreate", ctx, mock.Anything, mock.Anything, (*network.NetworkingConfig)(nil), (*ocispec.Platform)(nil), "loop-chrome-ch-1").
		Return(containertypes.CreateResponse{ID: "new-ctr"}, nil)
	s.api.On("ContainerStart", ctx, "new-ctr", containertypes.StartOptions{}).
		Return(nil)
	chromeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer chromeSrv.Close()
	newPort := strings.SplitAfter(chromeSrv.URL, ":")[len(strings.SplitAfter(chromeSrv.URL, ":"))-1]
	s.api.On("ContainerInspect", ctx, "new-ctr").
		Return(inspectResponseWithPort(newPort), nil)

	err := s.mgr.EnsureBrowser(ctx, "ch-1", "")
	require.NoError(s.T(), err)
	s.api.AssertCalled(s.T(), "ContainerRemove", ctx, "stale-ctr", containertypes.RemoveOptions{Force: true})
}

func (s *ManagerSuite) TestFindExistingChromeStartFails() {
	ctx := context.Background()

	// ContainerList finds an existing container that is stopped.
	s.api.On("ContainerList", ctx, mock.Anything).
		Return([]containertypes.Summary{
			{ID: "stopped-ctr", State: "exited"},
		}, nil)

	// ContainerStart fails — findExistingChrome should return "", "".
	s.api.On("ContainerStart", ctx, "stopped-ctr", containertypes.StartOptions{}).
		Return(errors.New("start failed"))

	// Since findExistingChrome returns "", "", EnsureBrowser proceeds to create a new container.
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

	s.api.AssertExpectations(s.T())
}

// --- SetTargetID / GetTargetID ---

func (s *ManagerSuite) TestSetTargetIDAndGet() {
	s.mgr.sessions["ch-1"] = &browserSession{chromeContainerID: "chrome-ctr-1"}

	s.mgr.SetTargetID("ch-1", "page-target-42")
	require.Equal(s.T(), "page-target-42", s.mgr.GetTargetID("ch-1"))
}

func (s *ManagerSuite) TestSetTargetIDNoSession() {
	// Should not panic when session doesn't exist.
	s.mgr.SetTargetID("nonexistent", "target-1")
}

func (s *ManagerSuite) TestGetTargetIDNoSession() {
	require.Equal(s.T(), "", s.mgr.GetTargetID("nonexistent"))
}

// --- SetCDPForTarget / GetCDPForTarget / RemoveCDPForTarget / GetActiveCDP ---

func (s *ManagerSuite) TestSetCDPForTargetAndGet() {
	s.mgr.sessions["ch-1"] = &browserSession{
		chromeContainerID: "chrome-ctr-1",
		cdpTargets:        make(map[string]any),
	}

	type fakeCDP struct{ name string }
	client := &fakeCDP{name: "test-cdp"}
	s.mgr.SetCDPForTarget("ch-1", "t1", client)
	got := s.mgr.GetCDPForTarget("ch-1", "t1")
	require.Equal(s.T(), client, got)
}

func (s *ManagerSuite) TestSetCDPForTargetNoSession() {
	// Should not panic when session doesn't exist.
	s.mgr.SetCDPForTarget("nonexistent", "t1", "some-cdp")
}

func (s *ManagerSuite) TestGetCDPForTargetNoSession() {
	require.Nil(s.T(), s.mgr.GetCDPForTarget("nonexistent", "t1"))
}

func (s *ManagerSuite) TestGetCDPForTargetNilCDP() {
	s.mgr.sessions["ch-1"] = &browserSession{
		chromeContainerID: "chrome-ctr-1",
		cdpTargets:        make(map[string]any),
	}
	// Target not in map.
	require.Nil(s.T(), s.mgr.GetCDPForTarget("ch-1", "t1"))
}

func (s *ManagerSuite) TestRemoveCDPForTarget() {
	s.mgr.sessions["ch-1"] = &browserSession{
		chromeContainerID: "chrome-ctr-1",
		cdpTargets:        make(map[string]any),
	}

	type fakeCDP struct{ name string }
	client := &fakeCDP{name: "test-cdp"}
	s.mgr.SetCDPForTarget("ch-1", "t1", client)

	removed := s.mgr.RemoveCDPForTarget("ch-1", "t1")
	require.Equal(s.T(), client, removed)

	// Should be gone now.
	require.Nil(s.T(), s.mgr.GetCDPForTarget("ch-1", "t1"))
}

func (s *ManagerSuite) TestRemoveCDPForTargetNoSession() {
	require.Nil(s.T(), s.mgr.RemoveCDPForTarget("nonexistent", "t1"))
}

func (s *ManagerSuite) TestRemoveCDPForTargetNotInMap() {
	s.mgr.sessions["ch-1"] = &browserSession{
		chromeContainerID: "chrome-ctr-1",
		cdpTargets:        make(map[string]any),
	}
	require.Nil(s.T(), s.mgr.RemoveCDPForTarget("ch-1", "t-missing"))
}

func (s *ManagerSuite) TestGetActiveCDP() {
	s.mgr.sessions["ch-1"] = &browserSession{
		chromeContainerID: "chrome-ctr-1",
		cdpTargets:        make(map[string]any),
		activeTargetID:    "t1",
	}

	type fakeCDP struct{ name string }
	client := &fakeCDP{name: "active-cdp"}
	s.mgr.SetCDPForTarget("ch-1", "t1", client)

	got := s.mgr.GetActiveCDP("ch-1")
	require.Equal(s.T(), client, got)
}

func (s *ManagerSuite) TestGetActiveCDPNoSession() {
	require.Nil(s.T(), s.mgr.GetActiveCDP("nonexistent"))
}

func (s *ManagerSuite) TestGetActiveCDPNoActiveTarget() {
	s.mgr.sessions["ch-1"] = &browserSession{
		chromeContainerID: "chrome-ctr-1",
		cdpTargets:        make(map[string]any),
		activeTargetID:    "",
	}
	require.Nil(s.T(), s.mgr.GetActiveCDP("ch-1"))
}

func (s *ManagerSuite) TestGetActiveCDPActiveTargetNotInMap() {
	s.mgr.sessions["ch-1"] = &browserSession{
		chromeContainerID: "chrome-ctr-1",
		cdpTargets:        make(map[string]any),
		activeTargetID:    "t-missing",
	}
	require.Nil(s.T(), s.mgr.GetActiveCDP("ch-1"))
}

func (s *ManagerSuite) TestMultipleCDPTargets() {
	s.mgr.sessions["ch-1"] = &browserSession{
		chromeContainerID: "chrome-ctr-1",
		cdpTargets:        make(map[string]any),
		activeTargetID:    "t1",
	}

	type fakeCDP struct{ name string }
	c1 := &fakeCDP{name: "cdp-1"}
	c2 := &fakeCDP{name: "cdp-2"}
	s.mgr.SetCDPForTarget("ch-1", "t1", c1)
	s.mgr.SetCDPForTarget("ch-1", "t2", c2)

	require.Equal(s.T(), c1, s.mgr.GetCDPForTarget("ch-1", "t1"))
	require.Equal(s.T(), c2, s.mgr.GetCDPForTarget("ch-1", "t2"))
	require.Equal(s.T(), c1, s.mgr.GetActiveCDP("ch-1"))

	// Switch active target.
	s.mgr.SetTargetID("ch-1", "t2")
	require.Equal(s.T(), c2, s.mgr.GetActiveCDP("ch-1"))
}

func (s *ManagerSuite) TestFindExistingChromeContainerListError() {
	ctx := context.Background()

	// ContainerList returns an error — findExistingChrome should return "", "".
	s.api.On("ContainerList", ctx, mock.Anything).
		Return([]containertypes.Summary{}, errors.New("list failed"))

	// Since findExistingChrome returns "", "", EnsureBrowser proceeds to create a new container.
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

	s.api.AssertExpectations(s.T())
}

func (s *ManagerSuite) TestIsChromeReachableEmptyPort() {
	require.False(s.T(), isChromeReachable(""))
}

func (s *ManagerSuite) TestIsChromeReachableConnectionRefused() {
	// Port 1 is unlikely to be listening.
	require.False(s.T(), isChromeReachable("1"))
}

func (s *ManagerSuite) TestIsChromeReachableNon200() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	// Extract host port from test server URL (e.g. "http://127.0.0.1:PORT").
	parts := strings.SplitAfter(srv.URL, ":")
	port := parts[len(parts)-1]

	require.False(s.T(), isChromeReachable(port))
}

func (s *ManagerSuite) TestIsChromeReachableSuccess() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"Browser":"Chrome"}`)
	}))
	defer srv.Close()

	parts := strings.SplitAfter(srv.URL, ":")
	port := parts[len(parts)-1]

	require.True(s.T(), isChromeReachable(port))
}

// --- TouchBrowser / PaneConnected / PaneDisconnected / idle monitor ---

// setupEnsuredSession creates a browser session via EnsureBrowser with the standard mock setup.
func (s *ManagerSuite) setupEnsuredSession(ctx context.Context, channelID string) {
	s.api.On("ContainerList", ctx, mock.Anything).
		Return([]containertypes.Summary{}, nil)
	s.api.On("ContainerCreate", ctx, mock.Anything, mock.Anything, (*network.NetworkingConfig)(nil), (*ocispec.Platform)(nil), ChromeHostname(channelID)).
		Return(containertypes.CreateResponse{ID: "chrome-ctr-1"}, nil)
	s.api.On("ContainerStart", ctx, "chrome-ctr-1", containertypes.StartOptions{}).
		Return(nil)
	s.api.On("ContainerInspect", ctx, "chrome-ctr-1").
		Return(inspectResponseWithPort("49152"), nil)

	err := s.mgr.EnsureBrowser(ctx, channelID, "")
	require.NoError(s.T(), err)
}

func (s *ManagerSuite) TestTouchBrowser() {
	ctx := context.Background()
	s.setupEnsuredSession(ctx, "ch-1")

	// Inject a fixed clock that returns a known future time.
	touchTime := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	s.mgr.timeNow = func() time.Time { return touchTime }

	s.mgr.TouchBrowser("ch-1")

	sess := s.mgr.sessions["ch-1"]
	require.Equal(s.T(), touchTime, sess.lastUsedAt)
}

func (s *ManagerSuite) TestTouchBrowserNoSession() {
	// Calling TouchBrowser on a non-existent channel should not panic.
	s.mgr.TouchBrowser("nonexistent")
}

func (s *ManagerSuite) TestPaneConnected() {
	ctx := context.Background()
	s.setupEnsuredSession(ctx, "ch-1")

	connectTime := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	s.mgr.timeNow = func() time.Time { return connectTime }

	s.mgr.PaneConnected("ch-1")

	sess := s.mgr.sessions["ch-1"]
	require.Equal(s.T(), 1, sess.paneCount)
	require.Equal(s.T(), connectTime, sess.lastUsedAt)
}

func (s *ManagerSuite) TestPaneDisconnected() {
	ctx := context.Background()
	s.setupEnsuredSession(ctx, "ch-1")

	s.mgr.PaneConnected("ch-1")
	require.Equal(s.T(), 1, s.mgr.sessions["ch-1"].paneCount)

	s.mgr.PaneDisconnected("ch-1")
	require.Equal(s.T(), 0, s.mgr.sessions["ch-1"].paneCount)
}

func (s *ManagerSuite) TestPaneDisconnectedGuardsZero() {
	// Inject a session with paneCount=0.
	s.mgr.sessions["ch-1"] = &browserSession{
		chromeContainerID: "chrome-ctr-1",
		paneCount:         0,
	}

	// PaneDisconnected should not go negative.
	s.mgr.PaneDisconnected("ch-1")
	require.Equal(s.T(), 0, s.mgr.sessions["ch-1"].paneCount)
}

func (s *ManagerSuite) TestStopIdleSessionsIdle() {
	ctx := context.Background()

	// Inject a session that has been idle for longer than the timeout.
	oldTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.mgr.sessions["ch-1"] = &browserSession{
		chromeContainerID: "chrome-ctr-1",
		hostPort:          "49152",
		lastUsedAt:        oldTime,
		paneCount:         0,
	}

	// timeNow returns a time well past the timeout.
	s.mgr.timeNow = func() time.Time {
		return oldTime.Add(30 * time.Minute)
	}

	// StopBrowser will call ContainerStop + ContainerRemove.
	timeout := 5
	s.api.On("ContainerStop", ctx, "chrome-ctr-1", containertypes.StopOptions{Timeout: &timeout}).
		Return(nil)
	s.api.On("ContainerRemove", ctx, "chrome-ctr-1", containertypes.RemoveOptions{Force: true}).
		Return(nil)

	s.mgr.stopIdleSessions(ctx, 10*time.Minute)

	// Session should be removed.
	_, ok := s.mgr.GetContainerID("ch-1")
	require.False(s.T(), ok)

	s.api.AssertExpectations(s.T())
}

func (s *ManagerSuite) TestStopIdleSessionsRecentlyUsed() {
	ctx := context.Background()

	now := time.Date(2026, 3, 16, 12, 0, 0, 0, time.UTC)
	s.mgr.timeNow = func() time.Time { return now }

	// Session was used recently (5 minutes ago, timeout is 10 minutes).
	s.mgr.sessions["ch-1"] = &browserSession{
		chromeContainerID: "chrome-ctr-1",
		hostPort:          "49152",
		lastUsedAt:        now.Add(-5 * time.Minute),
		paneCount:         0,
	}

	s.mgr.stopIdleSessions(ctx, 10*time.Minute)

	// Session should still exist — not stopped.
	cid, ok := s.mgr.GetContainerID("ch-1")
	require.True(s.T(), ok)
	require.Equal(s.T(), "chrome-ctr-1", cid)

	// ContainerStop should NOT have been called.
	s.api.AssertNotCalled(s.T(), "ContainerStop", mock.Anything, mock.Anything, mock.Anything)
}

func (s *ManagerSuite) TestStopIdleSessionsPaneActive() {
	ctx := context.Background()

	oldTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.mgr.timeNow = func() time.Time {
		return oldTime.Add(30 * time.Minute)
	}

	// Session is old but has an active pane — should NOT be stopped.
	s.mgr.sessions["ch-1"] = &browserSession{
		chromeContainerID: "chrome-ctr-1",
		hostPort:          "49152",
		lastUsedAt:        oldTime,
		paneCount:         1,
	}

	s.mgr.stopIdleSessions(ctx, 10*time.Minute)

	// Session should still exist.
	cid, ok := s.mgr.GetContainerID("ch-1")
	require.True(s.T(), ok)
	require.Equal(s.T(), "chrome-ctr-1", cid)

	// ContainerStop should NOT have been called.
	s.api.AssertNotCalled(s.T(), "ContainerStop", mock.Anything, mock.Anything, mock.Anything)
}

func (s *ManagerSuite) TestRunIdleMonitorCancelledContext() {
	// Create a context that is already cancelled so RunIdleMonitor returns immediately.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Should return without error and not block.
	s.mgr.RunIdleMonitor(ctx, 10*time.Minute)
}

func (s *ManagerSuite) TestRunIdleMonitorTickerFires() {
	// Inject a very short ticker interval so it fires quickly.
	s.mgr.idleCheckInterval = 10 * time.Millisecond
	now := time.Now()
	s.mgr.timeNow = func() time.Time { return now.Add(30 * time.Minute) }

	// Inject an idle session (old lastUsedAt, no pane).
	s.mgr.sessions["ch-idle"] = &browserSession{
		chromeContainerID: "chrome-idle",
		lastUsedAt:        now,
	}

	// Mock stop calls.
	s.api.On("ContainerStop", mock.Anything, "chrome-idle", mock.Anything).Return(nil)
	s.api.On("ContainerRemove", mock.Anything, "chrome-idle", mock.Anything).Return(nil)

	// Run with a short timeout so the ticker fires at least once, then cancel.
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	s.mgr.RunIdleMonitor(ctx, 5*time.Minute)

	// Session should have been idle-stopped.
	s.mgr.mu.Lock()
	_, exists := s.mgr.sessions["ch-idle"]
	s.mgr.mu.Unlock()
	require.False(s.T(), exists, "idle session should have been removed")
}

// --- NotifyTargetSwitch / TargetSwitchCh ---

func (s *ManagerSuite) TestNotifyTargetSwitch() {
	s.mgr.sessions["ch-1"] = &browserSession{
		chromeContainerID: "chrome-ctr-1",
		targetSwitchCh:    make(chan string, 1),
		tabAddedCh:        make(chan TabInfo, 1),
		tabRemovedCh:      make(chan string, 1),
	}

	s.mgr.NotifyTargetSwitch("ch-1", "target-42")

	// TargetID should be updated.
	require.Equal(s.T(), "target-42", s.mgr.GetTargetID("ch-1"))

	// Channel should have the signal.
	ch := s.mgr.TargetSwitchCh("ch-1")
	require.NotNil(s.T(), ch)
	select {
	case tid := <-ch:
		require.Equal(s.T(), "target-42", tid)
	default:
		s.T().Fatal("expected target switch signal")
	}
}

func (s *ManagerSuite) TestNotifyTargetSwitchNoSession() {
	// Should not panic.
	s.mgr.NotifyTargetSwitch("nonexistent", "target-1")
}

func (s *ManagerSuite) TestNotifyTargetSwitchDropsWhenFull() {
	s.mgr.sessions["ch-1"] = &browserSession{
		chromeContainerID: "chrome-ctr-1",
		targetSwitchCh:    make(chan string, 1),
		tabAddedCh:        make(chan TabInfo, 1),
		tabRemovedCh:      make(chan string, 1),
	}

	// Fill the channel.
	s.mgr.NotifyTargetSwitch("ch-1", "target-1")
	// Second call should not block (drops stale signal).
	s.mgr.NotifyTargetSwitch("ch-1", "target-2")

	ch := s.mgr.TargetSwitchCh("ch-1")
	tid := <-ch
	require.Equal(s.T(), "target-1", tid) // first value preserved
}

func (s *ManagerSuite) TestTargetSwitchChNoSession() {
	require.Nil(s.T(), s.mgr.TargetSwitchCh("nonexistent"))
}

// --- NotifyTabAdded / TabAddedCh ---

func (s *ManagerSuite) TestNotifyTabAdded() {
	s.mgr.sessions["ch-1"] = &browserSession{
		chromeContainerID: "chrome-ctr-1",
		targetSwitchCh:    make(chan string, 1),
		tabAddedCh:        make(chan TabInfo, 1),
		tabRemovedCh:      make(chan string, 1),
	}

	tab := TabInfo{TargetID: "t-1", URL: "https://a.com", Title: "A"}
	s.mgr.NotifyTabAdded("ch-1", tab)

	ch := s.mgr.TabAddedCh("ch-1")
	require.NotNil(s.T(), ch)
	select {
	case got := <-ch:
		require.Equal(s.T(), tab, got)
	default:
		s.T().Fatal("expected tab added signal")
	}
}

func (s *ManagerSuite) TestNotifyTabAddedNoSession() {
	// Should not panic.
	s.mgr.NotifyTabAdded("nonexistent", TabInfo{})
}

func (s *ManagerSuite) TestNotifyTabAddedDropsWhenFull() {
	s.mgr.sessions["ch-1"] = &browserSession{
		chromeContainerID: "chrome-ctr-1",
		targetSwitchCh:    make(chan string, 1),
		tabAddedCh:        make(chan TabInfo, 1),
		tabRemovedCh:      make(chan string, 1),
	}

	s.mgr.NotifyTabAdded("ch-1", TabInfo{TargetID: "t-1"})
	s.mgr.NotifyTabAdded("ch-1", TabInfo{TargetID: "t-2"}) // should not block
}

func (s *ManagerSuite) TestTabAddedChNoSession() {
	require.Nil(s.T(), s.mgr.TabAddedCh("nonexistent"))
}

// --- NotifyTabRemoved / TabRemovedCh ---

func (s *ManagerSuite) TestNotifyTabRemoved() {
	s.mgr.sessions["ch-1"] = &browserSession{
		chromeContainerID: "chrome-ctr-1",
		targetSwitchCh:    make(chan string, 1),
		tabAddedCh:        make(chan TabInfo, 1),
		tabRemovedCh:      make(chan string, 1),
	}

	s.mgr.NotifyTabRemoved("ch-1", "t-1")

	ch := s.mgr.TabRemovedCh("ch-1")
	require.NotNil(s.T(), ch)
	select {
	case got := <-ch:
		require.Equal(s.T(), "t-1", got)
	default:
		s.T().Fatal("expected tab removed signal")
	}
}

func (s *ManagerSuite) TestNotifyTabRemovedNoSession() {
	// Should not panic.
	s.mgr.NotifyTabRemoved("nonexistent", "t-1")
}

func (s *ManagerSuite) TestNotifyTabRemovedDropsWhenFull() {
	s.mgr.sessions["ch-1"] = &browserSession{
		chromeContainerID: "chrome-ctr-1",
		targetSwitchCh:    make(chan string, 1),
		tabAddedCh:        make(chan TabInfo, 1),
		tabRemovedCh:      make(chan string, 1),
	}

	s.mgr.NotifyTabRemoved("ch-1", "t-1")
	s.mgr.NotifyTabRemoved("ch-1", "t-2") // should not block
}

func (s *ManagerSuite) TestTabRemovedChNoSession() {
	require.Nil(s.T(), s.mgr.TabRemovedCh("nonexistent"))
}

// --- TrackTab / UntrackTab / OrderTabs ---

func (s *ManagerSuite) TestTrackTab() {
	s.mgr.sessions["ch-1"] = &browserSession{chromeContainerID: "chrome-ctr-1"}

	s.mgr.TrackTab("ch-1", "t1")
	s.mgr.TrackTab("ch-1", "t2")
	require.Equal(s.T(), []string{"t1", "t2"}, s.mgr.sessions["ch-1"].tabOrder)
}

func (s *ManagerSuite) TestTrackTabDuplicate() {
	s.mgr.sessions["ch-1"] = &browserSession{chromeContainerID: "chrome-ctr-1"}

	s.mgr.TrackTab("ch-1", "t1")
	s.mgr.TrackTab("ch-1", "t1") // duplicate
	require.Equal(s.T(), []string{"t1"}, s.mgr.sessions["ch-1"].tabOrder)
}

func (s *ManagerSuite) TestTrackTabNoSession() {
	// Should not panic.
	s.mgr.TrackTab("nonexistent", "t1")
}

func (s *ManagerSuite) TestUntrackTab() {
	s.mgr.sessions["ch-1"] = &browserSession{
		chromeContainerID: "chrome-ctr-1",
		tabOrder:          []string{"t1", "t2", "t3"},
	}

	s.mgr.UntrackTab("ch-1", "t2")
	require.Equal(s.T(), []string{"t1", "t3"}, s.mgr.sessions["ch-1"].tabOrder)
}

func (s *ManagerSuite) TestUntrackTabNoSession() {
	// Should not panic.
	s.mgr.UntrackTab("nonexistent", "t1")
}

func (s *ManagerSuite) TestOrderTabs() {
	s.mgr.sessions["ch-1"] = &browserSession{
		chromeContainerID: "chrome-ctr-1",
		tabOrder:          []string{"t2", "t1"},
	}

	tabs := []TabInfo{
		{TargetID: "t1", Title: "A"},
		{TargetID: "t2", Title: "B"},
	}
	result := s.mgr.OrderTabs("ch-1", tabs)
	require.Len(s.T(), result, 2)
	require.Equal(s.T(), "t2", result[0].TargetID)
	require.Equal(s.T(), "t1", result[1].TargetID)
}

func (s *ManagerSuite) TestOrderTabsUntrackedAppended() {
	s.mgr.sessions["ch-1"] = &browserSession{
		chromeContainerID: "chrome-ctr-1",
		tabOrder:          []string{"t1"},
	}

	tabs := []TabInfo{
		{TargetID: "t1", Title: "A"},
		{TargetID: "t3", Title: "C"}, // not tracked
	}
	result := s.mgr.OrderTabs("ch-1", tabs)
	require.Len(s.T(), result, 2)
	require.Equal(s.T(), "t1", result[0].TargetID)
	require.Equal(s.T(), "t3", result[1].TargetID)
}

func (s *ManagerSuite) TestOrderTabsNoSession() {
	tabs := []TabInfo{{TargetID: "t1", Title: "A"}}
	result := s.mgr.OrderTabs("nonexistent", tabs)
	require.Equal(s.T(), tabs, result)
}

func (s *ManagerSuite) TestOrderTabsEmptyTabOrder() {
	s.mgr.sessions["ch-1"] = &browserSession{
		chromeContainerID: "chrome-ctr-1",
		tabOrder:          nil,
	}

	tabs := []TabInfo{{TargetID: "t1", Title: "A"}}
	result := s.mgr.OrderTabs("ch-1", tabs)
	require.Equal(s.T(), tabs, result)
}

// --- NextTabID ---

func (s *ManagerSuite) TestNextTabIDMiddleTab() {
	s.mgr.sessions["ch-1"] = &browserSession{
		tabOrder: []string{"t1", "t2", "t3"},
	}
	// Close middle tab -> returns tab before it.
	require.Equal(s.T(), "t1", s.mgr.NextTabID("ch-1", "t2"))
}

func (s *ManagerSuite) TestNextTabIDFirstTab() {
	s.mgr.sessions["ch-1"] = &browserSession{
		tabOrder: []string{"t1", "t2", "t3"},
	}
	// Close first tab -> returns tab after it.
	require.Equal(s.T(), "t2", s.mgr.NextTabID("ch-1", "t1"))
}

func (s *ManagerSuite) TestNextTabIDOnlyTab() {
	s.mgr.sessions["ch-1"] = &browserSession{
		tabOrder: []string{"t1"},
	}
	// Close only tab -> returns "".
	require.Equal(s.T(), "", s.mgr.NextTabID("ch-1", "t1"))
}

func (s *ManagerSuite) TestNextTabIDNotInOrder() {
	s.mgr.sessions["ch-1"] = &browserSession{
		tabOrder: []string{"t1", "t2"},
	}
	// Close tab not in order -> returns "".
	require.Equal(s.T(), "", s.mgr.NextTabID("ch-1", "t-unknown"))
}

func (s *ManagerSuite) TestNextTabIDNoSession() {
	// No session for channel -> returns "".
	require.Equal(s.T(), "", s.mgr.NextTabID("nonexistent", "t1"))
}
