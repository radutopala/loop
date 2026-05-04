package container

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/config"
)

func (s *RunnerSuite) TestCurrentConfigReloads() {
	s.runner.configLoad = func() (*config.Config, error) {
		return &config.Config{
			ClaudeBinPath:  "new-claude",
			ContainerImage: "new-image:latest",
			LoopDir:        "/new/loop",
		}, nil
	}

	cfg := s.runner.currentConfig()
	require.Equal(s.T(), "new-claude", cfg.ClaudeBinPath)
	require.Equal(s.T(), "new-image:latest", cfg.ContainerImage)
	// Verify it was stored as fallback.
	require.Equal(s.T(), "new-claude", s.runner.cfg.Load().ClaudeBinPath)
}

func (s *RunnerSuite) TestCurrentConfigFallbackOnError() {
	s.runner.configLoad = func() (*config.Config, error) {
		return nil, errors.New("reload failed")
	}

	cfg := s.runner.currentConfig()
	// Falls back to the original config from SetupTest.
	require.Equal(s.T(), "claude", cfg.ClaudeBinPath)
	require.Equal(s.T(), "loop-agent:latest", cfg.ContainerImage)
}

func (s *RunnerSuite) TestCurrentConfigNilLoader() {
	// configLoad is nil by default from SetupTest.
	cfg := s.runner.currentConfig()
	require.Equal(s.T(), s.cfg, cfg)
}

func (s *RunnerSuite) TestRunBashHappyPath() {
	ctx := context.Background()

	reg := new(MockContainerRegistry)
	s.runner.SetContainerRegistry(reg)
	reg.On("Register", mock.MatchedBy(func(info *ContainerInfo) bool {
		return info.ContainerID == testContainerID &&
			info.ChannelID == "ch-1" &&
			info.Type == ContainerTypeAgent &&
			info.ContainerName == testContainerName
	})).Once()
	reg.On("ScheduleRemove", testContainerID, 5*time.Minute).Once()

	waitCh := make(chan WaitResponse, 1)
	waitCh <- WaitResponse{StatusCode: 0}
	errCh := make(chan error, 1)

	s.client.On("ContainerCreate", ctx, mock.MatchedBy(func(cfg *ContainerConfig) bool {
		return slices.Contains(cfg.Cmd, "/bin/sh") &&
			slices.Contains(cfg.Cmd, "-c") &&
			slices.Contains(cfg.Cmd, "echo hello")
	}), testContainerName).Return(testContainerID, nil)
	s.client.On("ContainerStart", ctx, testContainerID).Return(nil)
	s.client.On("ContainerWait", ctx, testContainerID).Return((<-chan WaitResponse)(waitCh), (<-chan error)(errCh))
	s.client.On("ContainerLogs", ctx, testContainerID).Return(strings.NewReader("hello\n"), nil)

	output, err := s.runner.RunBash(ctx, "echo hello", "ch-1", "")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "hello\n", output)

	s.client.AssertExpectations(s.T())
	reg.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunBashCreateFails() {
	ctx := context.Background()

	s.client.On("ContainerCreate", ctx, mock.AnythingOfType("*container.ContainerConfig"), testContainerName).Return("", errors.New("docker create failed"))

	output, err := s.runner.RunBash(ctx, "echo hello", "ch-1", "")
	require.Error(s.T(), err)
	require.Empty(s.T(), output)
	require.Contains(s.T(), err.Error(), "creating container")

	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunBashWaitError() {
	ctx := context.Background()

	reg := new(MockContainerRegistry)
	s.runner.SetContainerRegistry(reg)
	reg.On("Register", mock.MatchedBy(func(info *ContainerInfo) bool {
		return info.ContainerID == testContainerID &&
			info.ChannelID == "ch-1" &&
			info.Type == ContainerTypeAgent &&
			info.ContainerName == testContainerName
	})).Once()
	reg.On("ScheduleRemove", testContainerID, 5*time.Minute).Once()

	waitCh := make(chan WaitResponse, 1)
	errCh := make(chan error, 1)
	errCh <- errors.New("wait error")

	s.client.On("ContainerCreate", ctx, mock.AnythingOfType("*container.ContainerConfig"), testContainerName).Return(testContainerID, nil)
	s.client.On("ContainerStart", ctx, testContainerID).Return(nil)
	s.client.On("ContainerWait", ctx, testContainerID).Return((<-chan WaitResponse)(waitCh), (<-chan error)(errCh))

	output, err := s.runner.RunBash(ctx, "echo hello", "ch-1", "")
	require.Error(s.T(), err)
	require.Empty(s.T(), output)
	require.Contains(s.T(), err.Error(), "waiting for container")

	s.client.AssertExpectations(s.T())
	reg.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunBashNonZeroExit() {
	ctx := context.Background()

	reg := new(MockContainerRegistry)
	s.runner.SetContainerRegistry(reg)
	reg.On("Register", mock.MatchedBy(func(info *ContainerInfo) bool {
		return info.ContainerID == testContainerID &&
			info.ChannelID == "ch-1" &&
			info.Type == ContainerTypeAgent &&
			info.ContainerName == testContainerName
	})).Once()
	reg.On("ScheduleRemove", testContainerID, 5*time.Minute).Once()

	waitCh := make(chan WaitResponse, 1)
	waitCh <- WaitResponse{StatusCode: 1}
	errCh := make(chan error, 1)

	s.client.On("ContainerCreate", ctx, mock.AnythingOfType("*container.ContainerConfig"), testContainerName).Return(testContainerID, nil)
	s.client.On("ContainerStart", ctx, testContainerID).Return(nil)
	s.client.On("ContainerWait", ctx, testContainerID).Return((<-chan WaitResponse)(waitCh), (<-chan error)(errCh))
	s.client.On("ContainerLogs", ctx, testContainerID).Return(strings.NewReader("some output\n"), nil)

	output, err := s.runner.RunBash(ctx, "exit 1", "ch-1", "")
	require.Error(s.T(), err)
	require.Equal(s.T(), "some output\n", output)
	require.Contains(s.T(), err.Error(), "script exited with status 1")

	s.client.AssertExpectations(s.T())
	reg.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunBashLogsFails() {
	ctx := context.Background()

	reg := new(MockContainerRegistry)
	s.runner.SetContainerRegistry(reg)
	reg.On("Register", mock.MatchedBy(func(info *ContainerInfo) bool {
		return info.ContainerID == testContainerID &&
			info.ChannelID == "ch-1" &&
			info.Type == ContainerTypeAgent &&
			info.ContainerName == testContainerName
	})).Once()
	reg.On("ScheduleRemove", testContainerID, 5*time.Minute).Once()

	waitCh := make(chan WaitResponse, 1)
	waitCh <- WaitResponse{StatusCode: 0}
	errCh := make(chan error, 1)

	s.client.On("ContainerCreate", ctx, mock.AnythingOfType("*container.ContainerConfig"), testContainerName).Return(testContainerID, nil)
	s.client.On("ContainerStart", ctx, testContainerID).Return(nil)
	s.client.On("ContainerWait", ctx, testContainerID).Return((<-chan WaitResponse)(waitCh), (<-chan error)(errCh))
	s.client.On("ContainerLogs", ctx, testContainerID).Return(nil, errors.New("logs failed"))

	output, err := s.runner.RunBash(ctx, "echo hello", "ch-1", "")
	require.Error(s.T(), err)
	require.Empty(s.T(), output)
	require.Contains(s.T(), err.Error(), "reading container logs")

	s.client.AssertExpectations(s.T())
	reg.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunBashContextCancelled() {
	ctx, cancel := context.WithCancel(context.Background())

	reg := new(MockContainerRegistry)
	s.runner.SetContainerRegistry(reg)
	reg.On("Register", mock.MatchedBy(func(info *ContainerInfo) bool {
		return info.ContainerID == testContainerID &&
			info.ChannelID == "ch-1" &&
			info.Type == ContainerTypeAgent &&
			info.ContainerName == testContainerName
	})).Once()
	reg.On("ScheduleRemove", testContainerID, 5*time.Minute).Once()

	waitCh := make(chan WaitResponse) // never written to
	errCh := make(chan error)         // never written to

	s.client.On("ContainerCreate", ctx, mock.AnythingOfType("*container.ContainerConfig"), testContainerName).Return(testContainerID, nil)
	s.client.On("ContainerStart", ctx, testContainerID).Return(nil)
	s.client.On("ContainerWait", ctx, testContainerID).Return((<-chan WaitResponse)(waitCh), (<-chan error)(errCh))

	// Cancel context before waiting can complete.
	cancel()

	output, err := s.runner.RunBash(ctx, "sleep 999", "ch-1", "")
	require.Error(s.T(), err)
	require.Empty(s.T(), output)
	require.ErrorIs(s.T(), err, context.Canceled)

	s.client.AssertExpectations(s.T())
	reg.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunBashContainerError() {
	ctx := context.Background()

	reg := new(MockContainerRegistry)
	s.runner.SetContainerRegistry(reg)
	reg.On("Register", mock.MatchedBy(func(info *ContainerInfo) bool {
		return info.ContainerID == testContainerID &&
			info.ChannelID == "ch-1" &&
			info.Type == ContainerTypeAgent &&
			info.ContainerName == testContainerName
	})).Once()
	reg.On("ScheduleRemove", testContainerID, 5*time.Minute).Once()

	waitCh := make(chan WaitResponse, 1)
	waitCh <- WaitResponse{StatusCode: 1, Error: errors.New("OOM killed")}
	errCh := make(chan error, 1)

	s.client.On("ContainerCreate", ctx, mock.AnythingOfType("*container.ContainerConfig"), testContainerName).Return(testContainerID, nil)
	s.client.On("ContainerStart", ctx, testContainerID).Return(nil)
	s.client.On("ContainerWait", ctx, testContainerID).Return((<-chan WaitResponse)(waitCh), (<-chan error)(errCh))

	output, err := s.runner.RunBash(ctx, "stress --vm 1", "ch-1", "")
	require.Error(s.T(), err)
	require.Empty(s.T(), output)
	require.Contains(s.T(), err.Error(), "container error")

	s.client.AssertExpectations(s.T())
	reg.AssertExpectations(s.T())
}
