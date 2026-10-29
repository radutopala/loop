//go:build integration

package browser

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	containertypes "github.com/docker/docker/api/types/container"
	dockerclient "github.com/docker/docker/client"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/container"
)

// DownloadsIntegrationSuite proves a real sidecar saves a download where the
// agent container can read it: into the channel's downloads dir on the host.
type DownloadsIntegrationSuite struct {
	suite.Suite
	api       *dockerclient.Client
	provider  *DockerProvider
	loopDir   string
	channelID string
	testURL   string
}

func TestDownloadsIntegration(t *testing.T) {
	suite.Run(t, new(DownloadsIntegrationSuite))
}

func (s *DownloadsIntegrationSuite) SetupSuite() {
	api, err := dockerclient.NewClientWithOpts(dockerclient.FromEnv, dockerclient.WithAPIVersionNegotiation())
	require.NoError(s.T(), err)
	s.api = api

	s.loopDir = s.T().TempDir()
	s.provider = NewDockerProvider(api, DockerProviderConfig{Image: "loop-chrome:latest", Screen: "1280,800"}, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	s.provider.EnableDownloads(s.loopDir)
	s.channelID = fmt.Sprintf("downloads-it-%d", time.Now().UnixNano())
	// A download link needs a real http origin; Chrome's own DevTools HTTP
	// endpoint is one.
	s.testURL = fmt.Sprintf("http://127.0.0.1:%d/json/version", CDPPort)
}

func (s *DownloadsIntegrationSuite) TearDownSuite() {
	if s.provider != nil {
		ctx := context.Background()
		if containerID, _ := s.provider.StopBrowser(ctx, s.channelID); containerID != "" {
			_ = s.api.ContainerRemove(ctx, containerID, containertypes.RemoveOptions{Force: true})
		}
	}
	if s.api != nil {
		_ = s.api.Close()
	}
}

// download clicks a link that downloads name with content body.
func (s *DownloadsIntegrationSuite) download(client *CDPClient, name, body string) {
	ctx := context.Background()
	require.NoError(s.T(), client.Navigate(ctx, s.testURL))
	_, err := client.EvaluateJS(ctx, fmt.Sprintf(`(() => {
		const a = document.createElement("a");
		a.href = "data:text/plain,%s";
		a.download = %q;
		document.body.appendChild(a);
		a.click();
		return "clicked";
	})()`, body, name))
	require.NoError(s.T(), err)
}

func (s *DownloadsIntegrationSuite) requireDownloaded(name, body string) {
	path := filepath.Join(container.DownloadsDir(s.loopDir, s.channelID), name)
	require.Eventually(s.T(), func() bool {
		got, err := os.ReadFile(path)
		return err == nil && string(got) == body
	}, 15*time.Second, 100*time.Millisecond, "%s never arrived", path)
}

func (s *DownloadsIntegrationSuite) TestDownloadLandsInChannelDir() {
	ctx := context.Background()
	require.NoError(s.T(), s.provider.EnsureBrowser(ctx, s.channelID, ""))
	endpoint := s.provider.GetCDPEndpoint(s.channelID)

	first := dialCDP(s.T(), endpoint)
	require.NoError(s.T(), first.SetDownloadDir(ctx, ChromeDownloadsDir))
	s.download(first, "first.txt", "hello-first")
	s.requireDownloaded("first.txt", "hello-first")

	// A tab opened later, by a client that never set anything, downloads to
	// the same dir while the first client is attached.
	second := dialCDP(s.T(), endpoint)
	s.download(second, "second.txt", "hello-second")
	s.requireDownloaded("second.txt", "hello-second")
	second.Close()
	first.Close()
}

// TestReplacesSidecarFromBeforeDownloads covers a sidecar an older daemon
// created without the downloads mount and stopped on exit: the next start
// must replace it, not start it again, or downloads vanish into it.
func (s *DownloadsIntegrationSuite) TestReplacesSidecarFromBeforeDownloads() {
	ctx := context.Background()
	channelID := s.channelID + "-old"
	old := NewDockerProvider(s.api, DockerProviderConfig{Image: "loop-chrome:latest", Screen: "1280,800"}, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	require.NoError(s.T(), old.EnsureBrowser(ctx, channelID, ""))
	oldID, _ := old.StopBrowser(ctx, channelID)
	require.NotEmpty(s.T(), oldID)

	provider := NewDockerProvider(s.api, DockerProviderConfig{Image: "loop-chrome:latest", Screen: "1280,800"}, slog.New(slog.NewTextHandler(os.Stderr, nil)))
	provider.EnableDownloads(s.loopDir)
	require.NoError(s.T(), provider.EnsureBrowser(ctx, channelID, ""))
	defer func() {
		if id, _ := provider.StopBrowser(ctx, channelID); id != "" {
			_ = s.api.ContainerRemove(ctx, id, containertypes.RemoveOptions{Force: true})
		}
	}()
	newID, _ := provider.GetContainerID(channelID)
	require.NotEqual(s.T(), oldID, newID)

	client := dialCDP(s.T(), provider.GetCDPEndpoint(channelID))
	defer client.Close()
	require.NoError(s.T(), client.SetDownloadDir(ctx, ChromeDownloadsDir))
	s.download(client, "after.txt", "hello-after")
	path := filepath.Join(container.DownloadsDir(s.loopDir, channelID), "after.txt")
	require.Eventually(s.T(), func() bool {
		got, err := os.ReadFile(path)
		return err == nil && string(got) == "hello-after"
	}, 15*time.Second, 100*time.Millisecond, "%s never arrived", path)
}

// TestInProgressName pins the name Chrome gives a download it is still
// receiving, from a host server that holds back half the body.
func (s *DownloadsIntegrationSuite) TestInProgressName() {
	ctx := context.Background()
	release := make(chan struct{})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(s.T(), err)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Disposition", `attachment; filename="slow.bin"`)
		w.Header().Set("Content-Length", "2048")
		_, _ = w.Write(make([]byte, 1024))
		w.(http.Flusher).Flush()
		<-release
		_, _ = w.Write(make([]byte, 1024))
	})}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	require.NoError(s.T(), s.provider.EnsureBrowser(ctx, s.channelID, ""))
	client := dialCDP(s.T(), s.provider.GetCDPEndpoint(s.channelID))
	defer client.Close()
	require.NoError(s.T(), client.SetDownloadDir(ctx, ChromeDownloadsDir))
	port := ln.Addr().(*net.TCPAddr).Port
	go func() { _ = client.Navigate(ctx, fmt.Sprintf("http://host.docker.internal:%d/slow", port)) }()

	// Until the body is complete, Chrome holds the file as <name>.crdownload,
	// which is what list_downloads reports as still downloading.
	partial := filepath.Join(container.DownloadsDir(s.loopDir, s.channelID), "slow.bin.crdownload")
	require.Eventually(s.T(), func() bool {
		_, err := os.Stat(partial)
		return err == nil
	}, 15*time.Second, 100*time.Millisecond)
	close(release)
	s.requireDownloaded("slow.bin", string(make([]byte, 2048)))
}
