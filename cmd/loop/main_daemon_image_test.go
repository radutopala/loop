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
	err := s.app.defaultEnsureImage(context.Background(), dockerClient, cfg, nil)
	require.NoError(s.T(), err)
	dockerClient.AssertExpectations(s.T())
}

func (s *MainSuite) TestEnsureImageBuildsWhenMissing() {
	dockerClient := new(mockDockerClient)
	dockerClient.On("ImageList", mock.Anything, "loop-agent:latest").Return([]string{}, nil)
	dockerClient.On("ImageBuild", mock.Anything, mock.Anything, "loop-agent:latest").Return(nil)
	dockerClient.On("ImageList", mock.Anything, "loop-chrome:latest").Return([]string{}, nil)
	dockerClient.On("ImageBuildFileFresh", mock.Anything, mock.Anything, "chrome.Dockerfile", "loop-chrome:latest", mock.Anything).Return(nil)
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
	err := s.app.defaultEnsureImage(context.Background(), dockerClient, cfg, nil)
	require.NoError(s.T(), err)
	dockerClient.AssertExpectations(s.T())
}

func (s *MainSuite) TestEnsureImageWithBroadcastSuccess() {
	s.app.ensureImage = func(_ context.Context, _ container.DockerClient, _ *config.Config, _ func(string)) error {
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
	s.app.ensureImage = func(_ context.Context, _ container.DockerClient, _ *config.Config, _ func(string)) error {
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
	err := s.app.defaultEnsureImage(context.Background(), dockerClient, cfg, nil)
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
	err := s.app.defaultEnsureImage(context.Background(), dockerClient, cfg, nil)
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
	err := s.app.defaultEnsureImage(context.Background(), dockerClient, cfg, nil)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "listing chrome images")
}

func (s *MainSuite) TestEnsureImageChromeBuildError() {
	dockerClient := new(mockDockerClient)
	dockerClient.On("ImageList", mock.Anything, "loop-agent:latest").Return([]string{"sha256:abc"}, nil)
	dockerClient.On("ImageList", mock.Anything, "loop-chrome:latest").Return([]string{}, nil)
	dockerClient.On("ImageBuildFileFresh", mock.Anything, mock.Anything, "chrome.Dockerfile", "loop-chrome:latest", mock.Anything).Return(errors.New("chrome build failed"))

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
	err := s.app.defaultEnsureImage(context.Background(), dockerClient, cfg, nil)
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
	dockerClient.On("ImageInspectLabels", mock.Anything, "loop-chrome:latest").Return(map[string]string{
		"loop.version": "2.0.0",
	}, nil)
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
	err := s.app.defaultEnsureImage(context.Background(), dockerClient, cfg, nil)
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
	dockerClient.On("ImageBuildFileFresh", mock.Anything, mock.Anything, "chrome.Dockerfile", "loop-chrome:latest", mock.Anything).Return(nil)
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
	err := s.app.defaultEnsureImage(context.Background(), dockerClient, cfg, nil)
	require.NoError(s.T(), err)
	dockerClient.AssertExpectations(s.T())
}

// chromeImageTestConfig builds the on-disk container dir the chrome branch of
// defaultEnsureImage expects, and returns the matching config.
func (s *MainSuite) chromeImageTestConfig() *config.Config {
	cfg := &config.Config{
		LoopDir:        s.T().TempDir(),
		ContainerImage: "loop-agent:latest",
		Browser:        config.BrowserConfig{ChromeImage: "loop-chrome:latest"},
	}
	containerDir := filepath.Join(cfg.LoopDir, "container")
	require.NoError(s.T(), os.MkdirAll(containerDir, 0755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(containerDir, "Dockerfile"), []byte("FROM alpine"), 0644))
	require.NoError(s.T(), os.WriteFile(filepath.Join(containerDir, "chrome.Dockerfile"), []byte("FROM alpine"), 0644))
	return cfg
}

// The chrome sidecar image is rebuilt on the same release trigger as the agent
// image: missing, unlabelled, or labelled with a different loop version.
func (s *MainSuite) TestEnsureImageChromeRebuildTrigger() {
	tests := []struct {
		name        string
		version     string
		labels      map[string]string
		labelsErr   error
		wantRebuild bool
	}{
		{name: "label matches version", version: "2.0.0", labels: map[string]string{"loop.version": "2.0.0"}},
		{name: "label differs from version", version: "2.0.0", labels: map[string]string{"loop.version": "1.0.0"}, wantRebuild: true},
		{name: "no labels at all", version: "2.0.0", labels: nil, wantRebuild: true},
		{name: "inspect fails", version: "2.0.0", labelsErr: errors.New("no such image")},
		{name: "dev build never checks", version: "dev", labels: map[string]string{"loop.version": "1.0.0"}},
		{name: "dirty build never checks", version: "2.0.0-dirty", labels: map[string]string{"loop.version": "1.0.0"}},
		{name: "git build never checks", version: "2.0.0-g1234567", labels: map[string]string{"loop.version": "1.0.0"}},
		{name: "empty version never checks", version: "", labels: map[string]string{"loop.version": "1.0.0"}},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			dockerClient := new(mockDockerClient)
			dockerClient.On("ImageList", mock.Anything, "loop-agent:latest").Return([]string{"sha256:abc"}, nil)
			dockerClient.On("ImageInspectLabels", mock.Anything, "loop-agent:latest").
				Return(map[string]string{"loop.version": tt.version}, nil).Maybe()
			dockerClient.On("ImageList", mock.Anything, "loop-chrome:latest").Return([]string{"sha256:def"}, nil)
			dockerClient.On("ImageInspectLabels", mock.Anything, "loop-chrome:latest").
				Return(tt.labels, tt.labelsErr).Maybe()
			if tt.wantRebuild {
				dockerClient.On("ImageBuildFileFresh", mock.Anything, mock.Anything, "chrome.Dockerfile", "loop-chrome:latest", mock.Anything).Return(nil)
				dockerClient.On("PruneBuildCache", mock.Anything, 30*24*time.Hour).Return(uint64(0), nil)
			}

			s.app.version = tt.version
			s.app.sys = newPassthroughMock()
			require.NoError(s.T(), s.app.defaultEnsureImage(context.Background(), dockerClient, s.chromeImageTestConfig(), nil))

			if tt.wantRebuild {
				dockerClient.AssertExpectations(s.T())
			} else {
				dockerClient.AssertNotCalled(s.T(), "ImageBuildFileFresh", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
			}
		})
	}
}

// The build is stamped so the next start can judge the image's age. Only a real
// release contributes loop.version — a dev build would otherwise pin the image
// to a version that never matches.
func (s *MainSuite) TestEnsureImageChromeLabels() {
	tests := []struct {
		name        string
		version     string
		wantVersion bool
	}{
		{name: "release stamps version", version: "2.0.0", wantVersion: true},
		{name: "dev build omits version", version: "dev"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			var got map[string]string
			dockerClient := new(mockDockerClient)
			dockerClient.On("ImageList", mock.Anything, "loop-agent:latest").Return([]string{"sha256:abc"}, nil)
			dockerClient.On("ImageInspectLabels", mock.Anything, "loop-agent:latest").
				Return(map[string]string{"loop.version": tt.version}, nil).Maybe()
			dockerClient.On("ImageList", mock.Anything, "loop-chrome:latest").Return([]string{}, nil)
			dockerClient.On("ImageBuildFileFresh", mock.Anything, mock.Anything, "chrome.Dockerfile", "loop-chrome:latest", mock.Anything).
				Run(func(args mock.Arguments) { got = args.Get(4).(map[string]string) }).Return(nil)
			dockerClient.On("PruneBuildCache", mock.Anything, 30*24*time.Hour).Return(uint64(0), nil)

			s.app.version = tt.version
			s.app.sys = newPassthroughMock()
			require.NoError(s.T(), s.app.defaultEnsureImage(context.Background(), dockerClient, s.chromeImageTestConfig(), nil))

			require.NotEmpty(s.T(), got["loop.built_at"])
			if tt.wantVersion {
				require.Equal(s.T(), tt.version, got["loop.version"])
			} else {
				require.NotContains(s.T(), got, "loop.version")
			}
		})
	}
}

// setPhase lets the caller show "browser" in the UI instead of appearing to
// stall on the agent image. It is optional — nil callers must not panic.
func (s *MainSuite) TestEnsureImageChromeReportsBrowserPhase() {
	var phases []string
	dockerClient := new(mockDockerClient)
	dockerClient.On("ImageList", mock.Anything, "loop-agent:latest").Return([]string{"sha256:abc"}, nil)
	dockerClient.On("ImageList", mock.Anything, "loop-chrome:latest").Return([]string{}, nil)
	dockerClient.On("ImageBuildFileFresh", mock.Anything, mock.Anything, "chrome.Dockerfile", "loop-chrome:latest", mock.Anything).Return(nil)
	dockerClient.On("PruneBuildCache", mock.Anything, 30*24*time.Hour).Return(uint64(0), nil)

	s.app.sys = newPassthroughMock()
	err := s.app.defaultEnsureImage(context.Background(), dockerClient, s.chromeImageTestConfig(),
		func(p string) { phases = append(phases, p) })
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{"browser"}, phases)
}

func (s *MainSuite) TestIsReleaseVersion() {
	tests := []struct {
		version string
		want    bool
	}{
		{"2.0.0", true},
		{"v2026.9.5", true},
		{"", false},
		{"dev", false},
		{"2.0.0-dirty", false},
		{"2.0.0-g1234567", false},
	}
	for _, tt := range tests {
		s.Run(tt.version, func() {
			require.Equal(s.T(), tt.want, isReleaseVersion(tt.version))
		})
	}
}

func (s *MainSuite) TestEnsureImageSkipsRebuildWhenVersionMatches() {
	dockerClient := new(mockDockerClient)
	dockerClient.On("ImageList", mock.Anything, "loop-agent:latest").Return([]string{"sha256:abc"}, nil)
	dockerClient.On("ImageInspectLabels", mock.Anything, "loop-agent:latest").Return(map[string]string{
		"loop.version": "2.0.0",
	}, nil)
	dockerClient.On("ImageList", mock.Anything, "loop-chrome:latest").Return([]string{"sha256:def"}, nil)
	dockerClient.On("ImageInspectLabels", mock.Anything, "loop-chrome:latest").Return(map[string]string{
		"loop.version": "2.0.0",
	}, nil)

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
	err := s.app.defaultEnsureImage(context.Background(), dockerClient, cfg, nil)
	require.NoError(s.T(), err)
	dockerClient.AssertNotCalled(s.T(), "ImageBuild", mock.Anything, mock.Anything, mock.Anything)
}
