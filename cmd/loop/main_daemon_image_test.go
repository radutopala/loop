package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/api"
	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/container"
	"github.com/radutopala/loop/internal/daemon"
)

// --- daemon commands ---

func (s *MainSuite) TestNewDaemonStartCmd() {
	cmd := s.app.newDaemonStartCmd()
	require.Equal(s.T(), "daemon:start", cmd.Use)
	require.Equal(s.T(), []string{"d:start", "up"}, cmd.Aliases)
	require.NotNil(s.T(), cmd.RunE)
}

func (s *MainSuite) TestNewDaemonStopCmd() {
	cmd := s.app.newDaemonStopCmd()
	require.Equal(s.T(), "daemon:stop", cmd.Use)
	require.Equal(s.T(), []string{"d:stop", "down"}, cmd.Aliases)
	require.NotNil(s.T(), cmd.RunE)
}

func (s *MainSuite) TestNewDaemonStatusCmd() {
	cmd := s.app.newDaemonStatusCmd()
	require.Equal(s.T(), "daemon:status", cmd.Use)
	require.Equal(s.T(), []string{"d:status"}, cmd.Aliases)
	require.NotNil(s.T(), cmd.RunE)
}

func (s *MainSuite) TestDaemonStartSuccess() {
	s.app.configLoad = func() (*config.Config, error) { return testConfig(), nil }
	s.app.daemonStart = func(_ daemon.System, _ string) error { return nil }
	s.app.newSystem = func() daemon.System { return daemon.RealSystem{} }

	cmd := s.app.newDaemonStartCmd()
	err := cmd.Execute()
	require.NoError(s.T(), err)
}

func (s *MainSuite) TestDaemonStartError() {
	s.app.configLoad = func() (*config.Config, error) { return testConfig(), nil }
	s.app.daemonStart = func(_ daemon.System, _ string) error { return errors.New("start fail") }
	s.app.newSystem = func() daemon.System { return daemon.RealSystem{} }

	cmd := s.app.newDaemonStartCmd()
	err := cmd.Execute()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "start fail")
}

func (s *MainSuite) TestDaemonStartConfigError() {
	s.app.configLoad = func() (*config.Config, error) { return nil, errors.New("config fail") }

	cmd := s.app.newDaemonStartCmd()
	err := cmd.Execute()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "config fail")
}

func (s *MainSuite) TestDaemonStopSuccess() {
	s.app.daemonStop = func(_ daemon.System) error { return nil }
	s.app.newSystem = func() daemon.System { return daemon.RealSystem{} }

	cmd := s.app.newDaemonStopCmd()
	err := cmd.Execute()
	require.NoError(s.T(), err)
}

func (s *MainSuite) TestDaemonStopError() {
	s.app.daemonStop = func(_ daemon.System) error { return errors.New("stop fail") }
	s.app.newSystem = func() daemon.System { return daemon.RealSystem{} }

	cmd := s.app.newDaemonStopCmd()
	err := cmd.Execute()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "stop fail")
}

func (s *MainSuite) TestNewDaemonRestartCmd() {
	cmd := s.app.newDaemonRestartCmd()
	require.Equal(s.T(), "daemon:restart", cmd.Use)
	require.Equal(s.T(), []string{"d:restart", "restart"}, cmd.Aliases)
	require.NotNil(s.T(), cmd.RunE)
}

func (s *MainSuite) TestDaemonRestartSuccess() {
	s.app.configLoad = func() (*config.Config, error) { return testConfig(), nil }
	s.app.daemonStop = func(_ daemon.System) error { return nil }
	s.app.daemonStart = func(_ daemon.System, _ string) error { return nil }
	s.app.newSystem = func() daemon.System { return daemon.RealSystem{} }

	cmd := s.app.newDaemonRestartCmd()
	err := cmd.Execute()
	require.NoError(s.T(), err)
}

func (s *MainSuite) TestDaemonRestartSuccessWhenNotRunning() {
	s.app.configLoad = func() (*config.Config, error) { return testConfig(), nil }
	s.app.daemonStop = func(_ daemon.System) error { return errors.New("not running") }
	s.app.daemonStart = func(_ daemon.System, _ string) error { return nil }
	s.app.newSystem = func() daemon.System { return daemon.RealSystem{} }

	cmd := s.app.newDaemonRestartCmd()
	err := cmd.Execute()
	require.NoError(s.T(), err)
}

func (s *MainSuite) TestDaemonRestartStartError() {
	s.app.configLoad = func() (*config.Config, error) { return testConfig(), nil }
	s.app.daemonStop = func(_ daemon.System) error { return nil }
	s.app.daemonStart = func(_ daemon.System, _ string) error { return errors.New("start fail") }
	s.app.newSystem = func() daemon.System { return daemon.RealSystem{} }

	cmd := s.app.newDaemonRestartCmd()
	err := cmd.Execute()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "start fail")
}

func (s *MainSuite) TestDaemonRestartConfigError() {
	s.app.configLoad = func() (*config.Config, error) { return nil, errors.New("config fail") }

	cmd := s.app.newDaemonRestartCmd()
	err := cmd.Execute()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "config fail")
}

func (s *MainSuite) TestDaemonStatusSuccess() {
	s.app.daemonStatus = func(_ daemon.System) (string, error) { return "running", nil }
	s.app.newSystem = func() daemon.System { return daemon.RealSystem{} }

	cmd := s.app.newDaemonStatusCmd()
	err := cmd.Execute()
	require.NoError(s.T(), err)
}

func (s *MainSuite) TestDaemonStatusError() {
	s.app.daemonStatus = func(_ daemon.System) (string, error) { return "", errors.New("status fail") }
	s.app.newSystem = func() daemon.System { return daemon.RealSystem{} }

	cmd := s.app.newDaemonStatusCmd()
	err := cmd.Execute()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "status fail")
}

func (s *MainSuite) TestDefaultDaemonVars() {
	a := newApp()
	require.NotNil(s.T(), a.daemonStart)
	require.NotNil(s.T(), a.daemonStop)
	require.NotNil(s.T(), a.daemonStatus)
	require.NotNil(s.T(), a.newSystem)

	sys := a.newSystem()
	require.IsType(s.T(), daemon.RealSystem{}, sys)
}

// --- ensureImage tests ---

func (s *MainSuite) TestEnsureImageSkipsWhenExists() {
	dockerClient := new(mockDockerClient)
	dockerClient.On("ImageList", mock.Anything, "loop-agent:latest").Return([]string{"sha256:abc"}, nil)
	dockerClient.On("ImageList", mock.Anything, "loop-chrome:latest").Return([]string{"sha256:def"}, nil)

	cfg := &config.Config{
		LoopDir:        s.T().TempDir(),
		ContainerImage: "loop-agent:latest",
		Browser:        config.BrowserConfig{ChromeImage: "loop-chrome:latest"},
	}
	// Create container dir with Dockerfile so it doesn't try to write
	containerDir := filepath.Join(cfg.LoopDir, "container")
	require.NoError(s.T(), os.MkdirAll(containerDir, 0755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(containerDir, "Dockerfile"), []byte("FROM alpine"), 0644))
	require.NoError(s.T(), os.WriteFile(filepath.Join(containerDir, "chrome.Dockerfile"), []byte("FROM alpine"), 0644))

	s.app.sys = newPassthroughMock()
	err := s.app.defaultEnsureImage(context.Background(), dockerClient, cfg)
	require.NoError(s.T(), err)
	dockerClient.AssertExpectations(s.T())
}

func (s *MainSuite) TestEnsureImageBuildsWhenMissing() {
	dockerClient := new(mockDockerClient)
	dockerClient.On("ImageList", mock.Anything, "loop-agent:latest").Return([]string{}, nil)
	dockerClient.On("ImageBuild", mock.Anything, mock.Anything, "loop-agent:latest").Return(nil)
	dockerClient.On("ImageList", mock.Anything, "loop-chrome:latest").Return([]string{}, nil)
	dockerClient.On("ImageBuildFile", mock.Anything, mock.Anything, "chrome.Dockerfile", "loop-chrome:latest").Return(nil)
	dockerClient.On("PruneBuildCache", mock.Anything, 30*24*time.Hour).Return(uint64(0), nil)

	cfg := &config.Config{
		LoopDir:        s.T().TempDir(),
		ContainerImage: "loop-agent:latest",
		Browser:        config.BrowserConfig{ChromeImage: "loop-chrome:latest"},
	}
	// Create container dir with Dockerfile
	containerDir := filepath.Join(cfg.LoopDir, "container")
	require.NoError(s.T(), os.MkdirAll(containerDir, 0755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(containerDir, "Dockerfile"), []byte("FROM alpine"), 0644))
	require.NoError(s.T(), os.WriteFile(filepath.Join(containerDir, "chrome.Dockerfile"), []byte("FROM alpine"), 0644))

	s.app.sys = newPassthroughMock()
	err := s.app.defaultEnsureImage(context.Background(), dockerClient, cfg)
	require.NoError(s.T(), err)
	dockerClient.AssertExpectations(s.T())
}

func (s *MainSuite) TestEnsureImageWithBroadcastSuccess() {
	s.app.ensureImage = func(_ context.Context, _ container.DockerClient, _ *config.Config) error {
		return nil
	}
	hub := api.NewEventsHub(slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately so RunUpdateChecker exits

	dc := new(mockDockerClient)
	dc.On("LatestClaudeVersion").Return("1.0.0").Maybe()
	// The update checker's startup pass reads the installed version off the
	// image labels before the cancelled context stops it.
	dc.On("ImageInspectLabels", mock.Anything, "").Return(map[string]string(nil), errors.New("no such image")).Maybe()
	mgr := container.NewImageLifecycleManager(dc, hub, s.app.sys, nil, "", "", "", dc.LatestClaudeVersion)

	s.app.ensureImageWithBroadcast(ctx, dc, testConfig(), hub, mgr, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func (s *MainSuite) TestEnsureImageWithBroadcastError() {
	s.app.ensureImage = func(_ context.Context, _ container.DockerClient, _ *config.Config) error {
		return errors.New("build failed")
	}
	hub := api.NewEventsHub(slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	dc := new(mockDockerClient)
	dc.On("LatestClaudeVersion").Return("1.0.0").Maybe()
	// The update checker's startup pass reads the installed version off the
	// image labels before the cancelled context stops it.
	dc.On("ImageInspectLabels", mock.Anything, "").Return(map[string]string(nil), errors.New("no such image")).Maybe()
	mgr := container.NewImageLifecycleManager(dc, hub, s.app.sys, nil, "", "", "", dc.LatestClaudeVersion)

	s.app.ensureImageWithBroadcast(ctx, dc, testConfig(), hub, mgr, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func (s *MainSuite) TestEnsureImageListError() {
	dockerClient := new(mockDockerClient)
	dockerClient.On("ImageList", mock.Anything, "loop-agent:latest").Return(nil, errors.New("list error"))

	cfg := &config.Config{
		LoopDir:        s.T().TempDir(),
		ContainerImage: "loop-agent:latest",
		Browser:        config.BrowserConfig{ChromeImage: "loop-chrome:latest"},
	}
	containerDir := filepath.Join(cfg.LoopDir, "container")
	require.NoError(s.T(), os.MkdirAll(containerDir, 0755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(containerDir, "Dockerfile"), []byte("FROM alpine"), 0644))
	require.NoError(s.T(), os.WriteFile(filepath.Join(containerDir, "chrome.Dockerfile"), []byte("FROM alpine"), 0644))

	s.app.sys = newPassthroughMock()
	err := s.app.defaultEnsureImage(context.Background(), dockerClient, cfg)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "listing images")
	dockerClient.AssertExpectations(s.T())
}

func (s *MainSuite) TestEnsureImageAgentBuildError() {
	dockerClient := new(mockDockerClient)
	dockerClient.On("ImageList", mock.Anything, "loop-agent:latest").Return([]string{}, nil)
	dockerClient.On("ImageBuild", mock.Anything, mock.Anything, "loop-agent:latest").Return(errors.New("agent build failed"))

	cfg := &config.Config{
		LoopDir:        s.T().TempDir(),
		ContainerImage: "loop-agent:latest",
		Browser:        config.BrowserConfig{ChromeImage: "loop-chrome:latest"},
	}
	containerDir := filepath.Join(cfg.LoopDir, "container")
	require.NoError(s.T(), os.MkdirAll(containerDir, 0755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(containerDir, "Dockerfile"), []byte("FROM alpine"), 0644))
	require.NoError(s.T(), os.WriteFile(filepath.Join(containerDir, "chrome.Dockerfile"), []byte("FROM alpine"), 0644))

	s.app.sys = newPassthroughMock()
	err := s.app.defaultEnsureImage(context.Background(), dockerClient, cfg)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "agent build failed")
}

func (s *MainSuite) TestEnsureImageChromeListError() {
	dockerClient := new(mockDockerClient)
	dockerClient.On("ImageList", mock.Anything, "loop-agent:latest").Return([]string{"sha256:abc"}, nil)
	dockerClient.On("ImageList", mock.Anything, "loop-chrome:latest").Return(nil, errors.New("chrome list error"))

	cfg := &config.Config{
		LoopDir:        s.T().TempDir(),
		ContainerImage: "loop-agent:latest",
		Browser:        config.BrowserConfig{ChromeImage: "loop-chrome:latest"},
	}
	containerDir := filepath.Join(cfg.LoopDir, "container")
	require.NoError(s.T(), os.MkdirAll(containerDir, 0755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(containerDir, "Dockerfile"), []byte("FROM alpine"), 0644))
	require.NoError(s.T(), os.WriteFile(filepath.Join(containerDir, "chrome.Dockerfile"), []byte("FROM alpine"), 0644))

	s.app.sys = newPassthroughMock()
	err := s.app.defaultEnsureImage(context.Background(), dockerClient, cfg)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "listing chrome images")
}

func (s *MainSuite) TestEnsureImageChromeBuildError() {
	dockerClient := new(mockDockerClient)
	dockerClient.On("ImageList", mock.Anything, "loop-agent:latest").Return([]string{"sha256:abc"}, nil)
	dockerClient.On("ImageList", mock.Anything, "loop-chrome:latest").Return([]string{}, nil)
	dockerClient.On("ImageBuildFile", mock.Anything, mock.Anything, "chrome.Dockerfile", "loop-chrome:latest").Return(errors.New("chrome build failed"))

	cfg := &config.Config{
		LoopDir:        s.T().TempDir(),
		ContainerImage: "loop-agent:latest",
		Browser:        config.BrowserConfig{ChromeImage: "loop-chrome:latest"},
	}
	containerDir := filepath.Join(cfg.LoopDir, "container")
	require.NoError(s.T(), os.MkdirAll(containerDir, 0755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(containerDir, "Dockerfile"), []byte("FROM alpine"), 0644))
	require.NoError(s.T(), os.WriteFile(filepath.Join(containerDir, "chrome.Dockerfile"), []byte("FROM alpine"), 0644))

	s.app.sys = newPassthroughMock()
	err := s.app.defaultEnsureImage(context.Background(), dockerClient, cfg)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "chrome build failed")
}

func (s *MainSuite) TestEnsureImageRebuildsOnVersionMismatch() {
	dockerClient := new(mockDockerClient)
	dockerClient.On("ImageList", mock.Anything, "loop-agent:latest").Return([]string{"sha256:abc"}, nil)
	dockerClient.On("ImageInspectLabels", mock.Anything, "loop-agent:latest").Return(map[string]string{
		"loop.version": "1.0.0",
	}, nil)
	dockerClient.On("ImageBuild", mock.Anything, mock.Anything, "loop-agent:latest").Return(nil)
	dockerClient.On("ImageList", mock.Anything, "loop-chrome:latest").Return([]string{"sha256:def"}, nil)
	dockerClient.On("PruneBuildCache", mock.Anything, 30*24*time.Hour).Return(uint64(0), nil)

	cfg := &config.Config{
		LoopDir:        s.T().TempDir(),
		ContainerImage: "loop-agent:latest",
		Browser:        config.BrowserConfig{ChromeImage: "loop-chrome:latest"},
	}
	containerDir := filepath.Join(cfg.LoopDir, "container")
	require.NoError(s.T(), os.MkdirAll(containerDir, 0755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(containerDir, "Dockerfile"), []byte("FROM alpine"), 0644))
	require.NoError(s.T(), os.WriteFile(filepath.Join(containerDir, "chrome.Dockerfile"), []byte("FROM alpine"), 0644))

	s.app.version = "2.0.0" // differs from image label "1.0.0"
	s.app.sys = newPassthroughMock()
	err := s.app.defaultEnsureImage(context.Background(), dockerClient, cfg)
	require.NoError(s.T(), err)
	dockerClient.AssertCalled(s.T(), "ImageBuild", mock.Anything, mock.Anything, "loop-agent:latest")
}

// PruneBuildCache failure is logged-and-ignored — defaultEnsureImage must
// still return nil so a stale cache doesn't break daemon startup.
func (s *MainSuite) TestEnsureImagePruneBuildCacheErrorIsIgnored() {
	dockerClient := new(mockDockerClient)
	dockerClient.On("ImageList", mock.Anything, "loop-agent:latest").Return([]string{}, nil)
	dockerClient.On("ImageBuild", mock.Anything, mock.Anything, "loop-agent:latest").Return(nil)
	dockerClient.On("ImageList", mock.Anything, "loop-chrome:latest").Return([]string{}, nil)
	dockerClient.On("ImageBuildFile", mock.Anything, mock.Anything, "chrome.Dockerfile", "loop-chrome:latest").Return(nil)
	dockerClient.On("PruneBuildCache", mock.Anything, 30*24*time.Hour).Return(uint64(0), errors.New("prune broke"))

	cfg := &config.Config{
		LoopDir:        s.T().TempDir(),
		ContainerImage: "loop-agent:latest",
		Browser:        config.BrowserConfig{ChromeImage: "loop-chrome:latest"},
	}
	containerDir := filepath.Join(cfg.LoopDir, "container")
	require.NoError(s.T(), os.MkdirAll(containerDir, 0755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(containerDir, "Dockerfile"), []byte("FROM alpine"), 0644))
	require.NoError(s.T(), os.WriteFile(filepath.Join(containerDir, "chrome.Dockerfile"), []byte("FROM alpine"), 0644))

	s.app.sys = newPassthroughMock()
	err := s.app.defaultEnsureImage(context.Background(), dockerClient, cfg)
	require.NoError(s.T(), err)
	dockerClient.AssertExpectations(s.T())
}

func (s *MainSuite) TestEnsureImageSkipsRebuildWhenVersionMatches() {
	dockerClient := new(mockDockerClient)
	dockerClient.On("ImageList", mock.Anything, "loop-agent:latest").Return([]string{"sha256:abc"}, nil)
	dockerClient.On("ImageInspectLabels", mock.Anything, "loop-agent:latest").Return(map[string]string{
		"loop.version": "2.0.0",
	}, nil)
	dockerClient.On("ImageList", mock.Anything, "loop-chrome:latest").Return([]string{"sha256:def"}, nil)

	cfg := &config.Config{
		LoopDir:        s.T().TempDir(),
		ContainerImage: "loop-agent:latest",
		Browser:        config.BrowserConfig{ChromeImage: "loop-chrome:latest"},
	}
	containerDir := filepath.Join(cfg.LoopDir, "container")
	require.NoError(s.T(), os.MkdirAll(containerDir, 0755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(containerDir, "Dockerfile"), []byte("FROM alpine"), 0644))
	require.NoError(s.T(), os.WriteFile(filepath.Join(containerDir, "chrome.Dockerfile"), []byte("FROM alpine"), 0644))

	s.app.version = "2.0.0" // matches image label
	s.app.sys = newPassthroughMock()
	err := s.app.defaultEnsureImage(context.Background(), dockerClient, cfg)
	require.NoError(s.T(), err)
	dockerClient.AssertNotCalled(s.T(), "ImageBuild", mock.Anything, mock.Anything, mock.Anything)
}
