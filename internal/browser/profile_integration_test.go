//go:build integration

package browser

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	containertypes "github.com/docker/docker/api/types/container"
	dockerclient "github.com/docker/docker/client"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// ProfileIntegrationSuite proves the thing unit tests cannot: that a login
// really does outlive the sidecar. A cookie is set, the container is destroyed,
// a fresh one is built on the same named volume, and the cookie is still there
// — then RemoveProfile wipes it.
type ProfileIntegrationSuite struct {
	suite.Suite
	api       *dockerclient.Client
	provider  *DockerProvider
	testURL   string
	channelID string
}

func TestProfileIntegration(t *testing.T) {
	suite.Run(t, new(ProfileIntegrationSuite))
}

func (s *ProfileIntegrationSuite) SetupSuite() {
	api, err := dockerclient.NewClientWithOpts(dockerclient.FromEnv, dockerclient.WithAPIVersionNegotiation())
	require.NoError(s.T(), err)
	s.api = api

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	s.provider = NewDockerProvider(api, "loop-chrome:latest", "1280,800", true, logger)
	s.channelID = fmt.Sprintf("profile-it-%d", time.Now().UnixNano())

	// document.cookie needs a real http origin — about:blank and data: URLs are
	// opaque. Chrome's own DevTools HTTP endpoint is one, and it is reachable
	// from inside the sidecar without any host networking.
	s.testURL = fmt.Sprintf("http://127.0.0.1:%d/json/version", CDPPort)
}

func (s *ProfileIntegrationSuite) TearDownSuite() {
	if s.provider != nil {
		// The container must go before the volume: a stopped container still
		// holds a reference, and force does not override that.
		s.tearDownSidecar()
		_ = s.provider.RemoveProfile(context.Background(), s.channelID)
	}
	if s.api != nil {
		_ = s.api.Close()
	}
}

// tearDownSidecar stops and removes the container so the volume is detached —
// Docker refuses to remove a volume that is still in use.
func (s *ProfileIntegrationSuite) tearDownSidecar() {
	ctx := context.Background()
	containerID, _ := s.provider.StopBrowser(ctx, s.channelID)
	if containerID != "" {
		_ = s.api.ContainerRemove(ctx, containerID, containertypes.RemoveOptions{Force: true})
	}
}

// cookieRoundTrip brings up a sidecar, runs js against the test page and
// returns its result as a string, then tears the sidecar back down.
func (s *ProfileIntegrationSuite) cookieRoundTrip(js string) string {
	ctx := context.Background()
	require.NoError(s.T(), s.provider.EnsureBrowser(ctx, s.channelID, ""))

	endpoint := s.provider.GetCDPEndpoint(s.channelID)
	allowDirectCDP(s.T(), endpoint)

	client, err := NewCDPClient(ctx, endpoint, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	require.NoError(s.T(), err)
	defer client.Close()

	require.NoError(s.T(), client.Navigate(ctx, s.testURL))
	time.Sleep(500 * time.Millisecond)

	out, err := client.EvaluateJS(ctx, js)
	require.NoError(s.T(), err)
	return out
}

func (s *ProfileIntegrationSuite) TestProfileSurvivesContainerRemoval() {
	// 1. Set a cookie, then destroy the container it lives in.
	s.cookieRoundTrip(`document.cookie = "loopprofile=kept; max-age=3600; path=/"; document.cookie`)
	s.tearDownSidecar()

	// 2. A brand new container on the same named volume still has it.
	require.Contains(s.T(), s.cookieRoundTrip(`document.cookie`), "loopprofile=kept")
	s.tearDownSidecar()

	// 3. Resetting the profile wipes it.
	require.NoError(s.T(), s.provider.RemoveProfile(context.Background(), s.channelID))
	require.NotContains(s.T(), s.cookieRoundTrip(`document.cookie`), "loopprofile=kept")
}
