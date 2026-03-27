package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/container"
	"github.com/radutopala/loop/internal/testutil"
)

// --- image:rebuild tests ---

func (s *MainSuite) TestNewImageRebuildCmd() {
	cmd := s.app.newImageRebuildCmd()
	require.Equal(s.T(), "image:rebuild", cmd.Use)
	require.Equal(s.T(), []string{"i:rebuild", "i:r"}, cmd.Aliases)
	require.NotNil(s.T(), cmd.RunE)
}

func (s *MainSuite) TestImageRebuildHappyPath() {
	tmpDir := s.T().TempDir()
	cfg := &config.Config{
		ContainerImage: "loop-agent:latest",
		LoopDir:        tmpDir,
	}
	containerDir := filepath.Join(tmpDir, "container")

	dc := new(mockDockerClient)
	dc.On("RemoveImageAndContainers", mock.Anything, cfg.ContainerImage).Return(nil)
	dc.On("ImageBuild", mock.Anything, containerDir, cfg.ContainerImage).Return(nil)
	dc.On("ImageInspectLabels", mock.Anything, cfg.ContainerImage).Return(map[string]string{
		"loop.version":        "1.2.3",
		"loop.claude_version": "4.0.0",
	}, nil)
	dc.On("LatestClaudeVersion").Return("4.0.0")

	sys := newPassthroughMock()
	sys.On("UserHomeDir").Unset()
	sys.On("UserHomeDir").Return(tmpDir, nil)
	sys.On("WriteFile", mock.Anything, mock.Anything, mock.Anything).Unset()
	sys.On("WriteFile", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.app.sys = sys
	s.app.version = "1.2.3"
	s.app.configLoad = func() (*config.Config, error) { return cfg, nil }
	s.app.newDockerClient = func() (container.DockerClient, error) { return dc, nil }

	cmd := s.app.newImageRebuildCmd()
	err := cmd.Execute()
	require.NoError(s.T(), err)

	dc.AssertCalled(s.T(), "RemoveImageAndContainers", mock.Anything, cfg.ContainerImage)
	dc.AssertCalled(s.T(), "ImageBuild", mock.Anything, containerDir, cfg.ContainerImage)
	dc.AssertCalled(s.T(), "ImageInspectLabels", mock.Anything, cfg.ContainerImage)

	// Verify versions file was written.
	versionFile := filepath.Join(tmpDir, ".loop", "image-versions.json")
	data, readErr := os.ReadFile(versionFile)
	if readErr == nil {
		var v container.ImageVersions
		require.NoError(s.T(), json.Unmarshal(data, &v))
		require.Equal(s.T(), "1.2.3", v.LoopVersion)
		require.Equal(s.T(), "4.0.0", v.ClaudeVersion)
	}
}

func (s *MainSuite) TestImageRebuildConfigLoadError() {
	s.app.configLoad = func() (*config.Config, error) {
		return nil, errors.New("config fail")
	}

	cmd := s.app.newImageRebuildCmd()
	err := cmd.Execute()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "loading config")
	require.Contains(s.T(), err.Error(), "config fail")
}

func (s *MainSuite) TestImageRebuildDockerClientError() {
	cfg := &config.Config{
		ContainerImage: "loop-agent:latest",
		LoopDir:        s.T().TempDir(),
	}
	s.app.configLoad = func() (*config.Config, error) { return cfg, nil }
	s.app.newDockerClient = func() (container.DockerClient, error) {
		return nil, errors.New("docker fail")
	}

	cmd := s.app.newImageRebuildCmd()
	err := cmd.Execute()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "creating docker client")
	require.Contains(s.T(), err.Error(), "docker fail")
}

func (s *MainSuite) TestImageRebuildImageBuildError() {
	tmpDir := s.T().TempDir()
	cfg := &config.Config{
		ContainerImage: "loop-agent:latest",
		LoopDir:        tmpDir,
	}
	containerDir := filepath.Join(tmpDir, "container")

	dc := new(mockDockerClient)
	dc.On("RemoveImageAndContainers", mock.Anything, cfg.ContainerImage).Return(nil)
	dc.On("ImageBuild", mock.Anything, containerDir, cfg.ContainerImage).Return(errors.New("build fail"))

	s.app.configLoad = func() (*config.Config, error) { return cfg, nil }
	s.app.newDockerClient = func() (container.DockerClient, error) { return dc, nil }

	cmd := s.app.newImageRebuildCmd()
	err := cmd.Execute()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "building image")
	require.Contains(s.T(), err.Error(), "build fail")

	// RemoveImageAndContainers should still have been called before the build.
	dc.AssertCalled(s.T(), "RemoveImageAndContainers", mock.Anything, cfg.ContainerImage)
}

func (s *MainSuite) TestImageRebuildRemoveWarningStillBuilds() {
	tmpDir := s.T().TempDir()
	cfg := &config.Config{
		ContainerImage: "loop-agent:latest",
		LoopDir:        tmpDir,
	}
	containerDir := filepath.Join(tmpDir, "container")

	dc := new(mockDockerClient)
	dc.On("RemoveImageAndContainers", mock.Anything, cfg.ContainerImage).Return(errors.New("remove warning"))
	dc.On("ImageBuild", mock.Anything, containerDir, cfg.ContainerImage).Return(nil)
	dc.On("ImageInspectLabels", mock.Anything, cfg.ContainerImage).Return(map[string]string{}, nil)
	dc.On("LatestClaudeVersion").Return("")

	sys := newPassthroughMock()
	sys.On("UserHomeDir").Unset()
	sys.On("UserHomeDir").Return(tmpDir, nil)
	sys.On("WriteFile", mock.Anything, mock.Anything, mock.Anything).Unset()
	sys.On("WriteFile", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	s.app.sys = sys
	s.app.configLoad = func() (*config.Config, error) { return cfg, nil }
	s.app.newDockerClient = func() (container.DockerClient, error) { return dc, nil }

	cmd := s.app.newImageRebuildCmd()
	err := cmd.Execute()
	require.NoError(s.T(), err)

	// Build should proceed despite remove error.
	dc.AssertCalled(s.T(), "ImageBuild", mock.Anything, containerDir, cfg.ContainerImage)
}

// --- image:status tests ---

func (s *MainSuite) TestNewImageStatusCmd() {
	cmd := s.app.newImageStatusCmd()
	require.Equal(s.T(), "image:status", cmd.Use)
	require.Equal(s.T(), []string{"i:status", "i:s"}, cmd.Aliases)
	require.NotNil(s.T(), cmd.RunE)
}

func (s *MainSuite) TestImageStatusImageFoundWithLabels() {
	tmpDir := s.T().TempDir()
	cfg := &config.Config{
		ContainerImage: "loop-agent:latest",
		LoopDir:        tmpDir,
	}

	dc := new(mockDockerClient)
	dc.On("ImageList", mock.Anything, cfg.ContainerImage).Return([]string{"sha256:abc123"}, nil)
	dc.On("ImageInspectLabels", mock.Anything, cfg.ContainerImage).Return(map[string]string{
		"loop.version":        "1.2.3",
		"loop.claude_version": "4.0.0",
	}, nil)

	// Write a versions file so the built_at path is exercised.
	loopDir := filepath.Join(tmpDir, ".loop")
	require.NoError(s.T(), os.MkdirAll(loopDir, 0o755))
	builtAt := time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC)
	versionsData, _ := json.Marshal(container.ImageVersions{
		LoopVersion:   "1.2.3",
		ClaudeVersion: "4.0.0",
		BuiltAt:       builtAt,
	})
	require.NoError(s.T(), os.WriteFile(filepath.Join(loopDir, "image-versions.json"), versionsData, 0o644))

	sys := newPassthroughMock()
	sys.On("UserHomeDir").Unset()
	sys.On("UserHomeDir").Return(tmpDir, nil)

	s.app.sys = sys
	s.app.configLoad = func() (*config.Config, error) { return cfg, nil }
	s.app.newDockerClient = func() (container.DockerClient, error) { return dc, nil }

	cmd := s.app.newImageStatusCmd()
	err := cmd.Execute()
	require.NoError(s.T(), err)

	dc.AssertCalled(s.T(), "ImageList", mock.Anything, cfg.ContainerImage)
	dc.AssertCalled(s.T(), "ImageInspectLabels", mock.Anything, cfg.ContainerImage)
}

func (s *MainSuite) TestImageStatusImageNotFound() {
	cfg := &config.Config{
		ContainerImage: "loop-agent:latest",
	}

	dc := new(mockDockerClient)
	dc.On("ImageList", mock.Anything, cfg.ContainerImage).Return([]string{}, nil)

	s.app.configLoad = func() (*config.Config, error) { return cfg, nil }
	s.app.newDockerClient = func() (container.DockerClient, error) { return dc, nil }

	cmd := s.app.newImageStatusCmd()
	err := cmd.Execute()
	require.NoError(s.T(), err)

	dc.AssertCalled(s.T(), "ImageList", mock.Anything, cfg.ContainerImage)
	// ImageInspectLabels should NOT be called when image is not found.
	dc.AssertNotCalled(s.T(), "ImageInspectLabels", mock.Anything, mock.Anything)
}

func (s *MainSuite) TestImageStatusConfigLoadError() {
	s.app.configLoad = func() (*config.Config, error) {
		return nil, errors.New("config fail")
	}

	cmd := s.app.newImageStatusCmd()
	err := cmd.Execute()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "loading config")
	require.Contains(s.T(), err.Error(), "config fail")
}

func (s *MainSuite) TestImageStatusDockerClientError() {
	cfg := &config.Config{
		ContainerImage: "loop-agent:latest",
	}
	s.app.configLoad = func() (*config.Config, error) { return cfg, nil }
	s.app.newDockerClient = func() (container.DockerClient, error) {
		return nil, errors.New("docker fail")
	}

	cmd := s.app.newImageStatusCmd()
	err := cmd.Execute()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "creating docker client")
	require.Contains(s.T(), err.Error(), "docker fail")
}

func (s *MainSuite) TestImageStatusImageListError() {
	cfg := &config.Config{
		ContainerImage: "loop-agent:latest",
	}

	dc := new(mockDockerClient)
	dc.On("ImageList", mock.Anything, cfg.ContainerImage).Return(nil, errors.New("list fail"))

	s.app.configLoad = func() (*config.Config, error) { return cfg, nil }
	s.app.newDockerClient = func() (container.DockerClient, error) { return dc, nil }

	cmd := s.app.newImageStatusCmd()
	err := cmd.Execute()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "listing images")
	require.Contains(s.T(), err.Error(), "list fail")
}

func (s *MainSuite) TestImageStatusHomeDirError() {
	cfg := &config.Config{
		ContainerImage: "loop-agent:latest",
	}

	dc := new(mockDockerClient)
	dc.On("ImageList", mock.Anything, cfg.ContainerImage).Return([]string{"sha256:abc123"}, nil)
	dc.On("ImageInspectLabels", mock.Anything, cfg.ContainerImage).Return(map[string]string{
		"loop.version": "1.2.3",
	}, nil)

	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("", errors.New("no home"))

	s.app.sys = sys
	s.app.configLoad = func() (*config.Config, error) { return cfg, nil }
	s.app.newDockerClient = func() (container.DockerClient, error) { return dc, nil }

	cmd := s.app.newImageStatusCmd()
	err := cmd.Execute()
	// Should succeed -- home dir error is non-fatal, just skips built_at.
	require.NoError(s.T(), err)
}

func (s *MainSuite) TestImageStatusInspectLabelsError() {
	cfg := &config.Config{
		ContainerImage: "loop-agent:latest",
	}

	dc := new(mockDockerClient)
	dc.On("ImageList", mock.Anything, cfg.ContainerImage).Return([]string{"sha256:abc123"}, nil)
	dc.On("ImageInspectLabels", mock.Anything, cfg.ContainerImage).Return(nil, errors.New("inspect fail"))

	sys := newPassthroughMock()
	sys.On("UserHomeDir").Unset()
	sys.On("UserHomeDir").Return(s.T().TempDir(), nil)

	s.app.sys = sys
	s.app.configLoad = func() (*config.Config, error) { return cfg, nil }
	s.app.newDockerClient = func() (container.DockerClient, error) { return dc, nil }

	cmd := s.app.newImageStatusCmd()
	err := cmd.Execute()
	// Should succeed -- label inspect error is non-fatal.
	require.NoError(s.T(), err)
}
