package container

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/events"
	"github.com/radutopala/loop/internal/testutil"
)

// MockImageBroadcaster implements ImageBroadcaster for testing.
type MockImageBroadcaster struct {
	mock.Mock
}

func (m *MockImageBroadcaster) BroadcastImageBuildStatus(data events.ImageBuildStatusData) {
	m.Called(data)
}

func (m *MockImageBroadcaster) BroadcastImageUpdateAvailable(data events.ImageUpdateAvailableData) {
	m.Called(data)
}

// LifecycleSuite tests ImageLifecycleManager.
type LifecycleSuite struct {
	suite.Suite
	client      *MockDockerClient
	sys         *testutil.MockSystem
	broadcaster *MockImageBroadcaster

	containerDir string
	imageName    string
	loopVersion  string
}

func TestLifecycleSuite(t *testing.T) {
	suite.Run(t, new(LifecycleSuite))
}

func (s *LifecycleSuite) SetupTest() {
	s.client = new(MockDockerClient)
	s.sys = new(testutil.MockSystem)
	s.broadcaster = new(MockImageBroadcaster)
	s.containerDir = "/tmp/container"
	s.imageName = "loop-agent:latest"
	s.loopVersion = "1.0.0"
}

// newManager creates a lifecycle manager for tests with standard defaults.
// It sets up the sys mock to return a home dir and no versions file.
func (s *LifecycleSuite) newManager(latestClaudeVersion func() string) *ImageLifecycleManager {
	s.sys.On("UserHomeDir").Return("/home/test", nil).Maybe()
	s.sys.On("ReadFile", "/home/test/.loop/image-versions.json").Return(nil, os.ErrNotExist).Maybe()
	return NewImageLifecycleManager(
		s.client, s.broadcaster, s.sys, nil,
		s.containerDir, s.imageName, s.loopVersion,
		latestClaudeVersion,
	)
}

// --- NewImageLifecycleManager ---

func (s *LifecycleSuite) TestNewImageLifecycleManager_DefaultState() {
	m := s.newManager(func() string { return "" })

	require.Equal(s.T(), "idle", m.Status().State)
	require.Equal(s.T(), s.containerDir, m.containerDir)
	require.Equal(s.T(), s.imageName, m.imageName)
	require.Equal(s.T(), s.loopVersion, m.loopVersion)
	require.NotNil(s.T(), m.logger)
}

func (s *LifecycleSuite) TestNewImageLifecycleManager_NilLogger() {
	s.sys.On("UserHomeDir").Return("/home/test", nil).Maybe()
	s.sys.On("ReadFile", "/home/test/.loop/image-versions.json").Return(nil, os.ErrNotExist).Maybe()

	m := NewImageLifecycleManager(
		s.client, s.broadcaster, s.sys, nil,
		s.containerDir, s.imageName, s.loopVersion,
		func() string { return "" },
	)
	require.NotNil(s.T(), m.logger, "nil logger should be replaced with discard logger")
}

func (s *LifecycleSuite) TestNewImageLifecycleManager_LoadsVersionsFromFile() {
	v := ImageVersions{
		LoopVersion:   "1.2.3",
		ClaudeVersion: "4.5.6",
		BuiltAt:       time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	data, err := json.Marshal(v)
	require.NoError(s.T(), err)

	s.sys.On("UserHomeDir").Return("/home/test", nil)
	s.sys.On("ReadFile", "/home/test/.loop/image-versions.json").Return(data, nil)

	m := NewImageLifecycleManager(
		s.client, s.broadcaster, s.sys, nil,
		s.containerDir, s.imageName, s.loopVersion,
		func() string { return "" },
	)

	require.Equal(s.T(), "1.2.3", m.versions.LoopVersion)
	require.Equal(s.T(), "4.5.6", m.versions.ClaudeVersion)
}

func (s *LifecycleSuite) TestNewImageLifecycleManager_LoadVersionsFileNotFound() {
	s.sys.On("UserHomeDir").Return("/home/test", nil)
	s.sys.On("ReadFile", "/home/test/.loop/image-versions.json").Return(nil, os.ErrNotExist)

	m := NewImageLifecycleManager(
		s.client, s.broadcaster, s.sys, nil,
		s.containerDir, s.imageName, s.loopVersion,
		func() string { return "" },
	)

	require.Empty(s.T(), m.versions.LoopVersion)
	require.Empty(s.T(), m.versions.ClaudeVersion)
}

func (s *LifecycleSuite) TestNewImageLifecycleManager_LoadVersionsHomeDirError() {
	s.sys.On("UserHomeDir").Return("", errors.New("no home"))

	m := NewImageLifecycleManager(
		s.client, s.broadcaster, s.sys, nil,
		s.containerDir, s.imageName, s.loopVersion,
		func() string { return "" },
	)

	require.Empty(s.T(), m.versions.LoopVersion)
}

func (s *LifecycleSuite) TestNewImageLifecycleManager_LoadVersionsInvalidJSON() {
	s.sys.On("UserHomeDir").Return("/home/test", nil)
	s.sys.On("ReadFile", "/home/test/.loop/image-versions.json").Return([]byte("not json"), nil)

	m := NewImageLifecycleManager(
		s.client, s.broadcaster, s.sys, nil,
		s.containerDir, s.imageName, s.loopVersion,
		func() string { return "" },
	)

	require.Empty(s.T(), m.versions.LoopVersion)
}

// --- Status ---

func (s *LifecycleSuite) TestStatus_ReturnsCurrentState() {
	m := s.newManager(func() string { return "" })

	require.Equal(s.T(), "idle", m.Status().State)

	m.mu.Lock()
	m.status = ImageBuildStatus{State: "building", Phase: "removing"}
	m.mu.Unlock()

	st := m.Status()
	require.Equal(s.T(), "building", st.State)
	require.Equal(s.T(), "removing", st.Phase)
}

// --- Versions ---

func (s *LifecycleSuite) TestVersions_ReturnsCachedVersions() {
	m := s.newManager(func() string { return "" })
	m.mu.Lock()
	m.versions = ImageVersions{LoopVersion: "1.0.0", ClaudeVersion: "2.0.0"}
	m.mu.Unlock()

	v := m.Versions()
	require.Equal(s.T(), "1.0.0", v.LoopVersion)
	require.Equal(s.T(), "2.0.0", v.ClaudeVersion)
}

func (s *LifecycleSuite) TestVersions_FallsBackToImageInspectLabels() {
	m := s.newManager(func() string { return "" })
	// versions are empty (default)

	s.client.On("ImageInspectLabels", mock.Anything, s.imageName).Return(map[string]string{
		"loop.version":        "3.0.0",
		"loop.claude_version": "5.0.0",
	}, nil)

	v := m.Versions()
	require.Equal(s.T(), "3.0.0", v.LoopVersion)
	require.Equal(s.T(), "5.0.0", v.ClaudeVersion)
	s.client.AssertCalled(s.T(), "ImageInspectLabels", mock.Anything, s.imageName)
}

func (s *LifecycleSuite) TestVersions_InspectLabelsError() {
	m := s.newManager(func() string { return "" })

	s.client.On("ImageInspectLabels", mock.Anything, s.imageName).Return(nil, errors.New("inspect failed"))

	v := m.Versions()
	require.Empty(s.T(), v.LoopVersion)
	require.Empty(s.T(), v.ClaudeVersion)
}

func (s *LifecycleSuite) TestVersions_InspectLabelsNilMap() {
	m := s.newManager(func() string { return "" })

	s.client.On("ImageInspectLabels", mock.Anything, s.imageName).Return(nil, nil)

	v := m.Versions()
	require.Empty(s.T(), v.LoopVersion)
	require.Empty(s.T(), v.ClaudeVersion)
}

func (s *LifecycleSuite) TestVersions_SkipsInspectWhenVersionsPopulated() {
	m := s.newManager(func() string { return "" })
	m.mu.Lock()
	m.versions = ImageVersions{LoopVersion: "1.0.0"} // only loop version set
	m.mu.Unlock()

	v := m.Versions()
	require.Equal(s.T(), "1.0.0", v.LoopVersion)
	// ImageInspectLabels should NOT be called since LoopVersion is non-empty
	s.client.AssertNotCalled(s.T(), "ImageInspectLabels", mock.Anything, mock.Anything)
}

// --- RemoveImage ---

func (s *LifecycleSuite) TestRemoveImage_Success() {
	m := s.newManager(func() string { return "" })

	s.client.On("RemoveImageAndContainers", mock.Anything, s.imageName).Return(nil)

	err := m.RemoveImage(context.Background())
	require.NoError(s.T(), err)
	s.client.AssertCalled(s.T(), "RemoveImageAndContainers", mock.Anything, s.imageName)
}

func (s *LifecycleSuite) TestRemoveImage_Error() {
	m := s.newManager(func() string { return "" })

	s.client.On("RemoveImageAndContainers", mock.Anything, s.imageName).Return(errors.New("remove failed"))

	err := m.RemoveImage(context.Background())
	require.EqualError(s.T(), err, "remove failed")
}

// --- Rebuild ---

func (s *LifecycleSuite) TestRebuild_AlreadyBuilding() {
	m := s.newManager(func() string { return "" })
	m.mu.Lock()
	m.status = ImageBuildStatus{State: "building"}
	m.mu.Unlock()

	err := m.Rebuild(context.Background())
	require.EqualError(s.T(), err, "build already in progress")
}

func (s *LifecycleSuite) TestRebuild_StartsAsyncBuild() {
	m := s.newManager(func() string { return "" })

	// Set up all mocks needed by doRebuild.
	s.client.On("RemoveImageAndContainers", mock.Anything, s.imageName).Return(nil)
	s.client.On("ImageBuild", mock.Anything, s.containerDir, s.imageName).Return(nil)
	s.client.On("ImageInspectLabels", mock.Anything, s.imageName).Return(map[string]string{
		"loop.version":        "1.0.0",
		"loop.claude_version": "2.0.0",
	}, nil)
	s.sys.On("WriteFile", "/home/test/.loop/image-versions.json", mock.Anything, os.FileMode(0o644)).Return(nil)
	s.broadcaster.On("BroadcastImageBuildStatus", mock.Anything).Return()

	err := m.Rebuild(context.Background())
	require.NoError(s.T(), err)

	// The status should be "building" immediately after Rebuild returns.
	st := m.Status()
	require.Equal(s.T(), "building", st.State)

	// Wait for the async goroutine to finish.
	require.Eventually(s.T(), func() bool {
		return m.Status().State == "completed"
	}, 2*time.Second, 10*time.Millisecond)
}

// --- doRebuild ---

func (s *LifecycleSuite) TestDoRebuild_BuildFailure() {
	m := s.newManager(func() string { return "" })
	m.mu.Lock()
	m.status = ImageBuildStatus{State: "building", Phase: "building"}
	m.mu.Unlock()

	s.client.On("ImageBuild", mock.Anything, s.containerDir, s.imageName).Return(errors.New("build exploded"))
	s.broadcaster.On("BroadcastImageBuildStatus", mock.Anything).Return()

	m.doRebuild(context.Background())

	st := m.Status()
	require.Equal(s.T(), "failed", st.State)
	require.Equal(s.T(), "build exploded", st.Error)
}

func (s *LifecycleSuite) TestDoRebuild_Success_WithLabels() {
	m := s.newManager(func() string { return "" })
	m.mu.Lock()
	m.status = ImageBuildStatus{State: "building", Phase: "building"}
	m.mu.Unlock()

	s.client.On("ImageBuild", mock.Anything, s.containerDir, s.imageName).Return(nil)
	s.client.On("ImageInspectLabels", mock.Anything, s.imageName).Return(map[string]string{
		"loop.version":        "1.1.0",
		"loop.claude_version": "3.0.0",
	}, nil)
	s.sys.On("WriteFile", "/home/test/.loop/image-versions.json", mock.Anything, os.FileMode(0o644)).Return(nil)
	s.broadcaster.On("BroadcastImageBuildStatus", mock.Anything).Return()

	m.doRebuild(context.Background())

	st := m.Status()
	require.Equal(s.T(), "completed", st.State)
	require.Equal(s.T(), "1.1.0", m.Versions().LoopVersion)
	require.Equal(s.T(), "3.0.0", m.Versions().ClaudeVersion)
}

func (s *LifecycleSuite) TestDoRebuild_Success_LabelInspectFails() {
	m := s.newManager(func() string { return "" })
	m.mu.Lock()
	m.status = ImageBuildStatus{State: "building", Phase: "building"}
	m.mu.Unlock()

	s.client.On("ImageBuild", mock.Anything, s.containerDir, s.imageName).Return(nil)
	s.client.On("ImageInspectLabels", mock.Anything, s.imageName).Return(nil, errors.New("inspect fail"))
	s.sys.On("WriteFile", "/home/test/.loop/image-versions.json", mock.Anything, os.FileMode(0o644)).Return(nil)
	s.broadcaster.On("BroadcastImageBuildStatus", mock.Anything).Return()

	m.doRebuild(context.Background())

	st := m.Status()
	require.Equal(s.T(), "completed", st.State)
	v := m.Versions()
	require.Equal(s.T(), s.loopVersion, v.LoopVersion)
	require.Equal(s.T(), "unknown", v.ClaudeVersion)
}

func (s *LifecycleSuite) TestDoRebuild_Success_LabelInspectNilMap() {
	m := s.newManager(func() string { return "" })
	m.mu.Lock()
	m.status = ImageBuildStatus{State: "building", Phase: "building"}
	m.mu.Unlock()

	s.client.On("ImageBuild", mock.Anything, s.containerDir, s.imageName).Return(nil)
	// nil labels, nil error — should fall back to loopVersion/unknown
	s.client.On("ImageInspectLabels", mock.Anything, s.imageName).Return(nil, nil)
	s.sys.On("WriteFile", "/home/test/.loop/image-versions.json", mock.Anything, os.FileMode(0o644)).Return(nil)
	s.broadcaster.On("BroadcastImageBuildStatus", mock.Anything).Return()

	m.doRebuild(context.Background())

	st := m.Status()
	require.Equal(s.T(), "completed", st.State)
	v := m.Versions()
	require.Equal(s.T(), s.loopVersion, v.LoopVersion)
	require.Equal(s.T(), "unknown", v.ClaudeVersion)
}

func (s *LifecycleSuite) TestDoRebuild_BroadcastStatusSequence() {
	m := s.newManager(func() string { return "" })
	m.mu.Lock()
	m.status = ImageBuildStatus{State: "building", Phase: "building"}
	m.mu.Unlock()

	s.client.On("ImageBuild", mock.Anything, s.containerDir, s.imageName).Return(nil)
	s.client.On("ImageInspectLabels", mock.Anything, s.imageName).Return(map[string]string{
		"loop.version":        "1.0.0",
		"loop.claude_version": "2.0.0",
	}, nil)
	s.sys.On("WriteFile", "/home/test/.loop/image-versions.json", mock.Anything, os.FileMode(0o644)).Return(nil)

	var statusCalls []events.ImageBuildStatusData
	s.broadcaster.On("BroadcastImageBuildStatus", mock.Anything).Run(func(args mock.Arguments) {
		statusCalls = append(statusCalls, args.Get(0).(events.ImageBuildStatusData))
	}).Return()

	m.doRebuild(context.Background())

	// Should broadcast "completed" after successful build.
	require.Len(s.T(), statusCalls, 1)
	require.Equal(s.T(), "completed", statusCalls[0].State)
}

func (s *LifecycleSuite) TestDoRebuild_BuildFailed_BroadcastStatusSequence() {
	m := s.newManager(func() string { return "" })
	m.mu.Lock()
	m.status = ImageBuildStatus{State: "building", Phase: "building"}
	m.mu.Unlock()

	s.client.On("ImageBuild", mock.Anything, s.containerDir, s.imageName).Return(errors.New("build err"))

	var statusCalls []events.ImageBuildStatusData
	s.broadcaster.On("BroadcastImageBuildStatus", mock.Anything).Run(func(args mock.Arguments) {
		statusCalls = append(statusCalls, args.Get(0).(events.ImageBuildStatusData))
	}).Return()

	m.doRebuild(context.Background())

	// Should broadcast "failed" after build error.
	require.Len(s.T(), statusCalls, 1)
	require.Equal(s.T(), "failed", statusCalls[0].State)
	require.Equal(s.T(), "build err", statusCalls[0].Error)
}

func (s *LifecycleSuite) TestDoRebuild_NilBroadcaster() {
	s.sys.On("UserHomeDir").Return("/home/test", nil).Maybe()
	s.sys.On("ReadFile", "/home/test/.loop/image-versions.json").Return(nil, os.ErrNotExist).Maybe()

	m := NewImageLifecycleManager(
		s.client, nil, s.sys, nil,
		s.containerDir, s.imageName, s.loopVersion,
		func() string { return "" },
	)
	m.mu.Lock()
	m.status = ImageBuildStatus{State: "building", Phase: "building"}
	m.mu.Unlock()

	s.client.On("ImageBuild", mock.Anything, s.containerDir, s.imageName).Return(nil)
	s.client.On("ImageInspectLabels", mock.Anything, s.imageName).Return(map[string]string{
		"loop.version":        "1.0.0",
		"loop.claude_version": "2.0.0",
	}, nil)
	s.sys.On("WriteFile", "/home/test/.loop/image-versions.json", mock.Anything, os.FileMode(0o644)).Return(nil)

	// Should not panic with nil broadcaster.
	m.doRebuild(context.Background())
	require.Equal(s.T(), "completed", m.Status().State)
}

// --- CheckClaudeUpdate ---

func (s *LifecycleSuite) TestCheckClaudeUpdate_CurrentEqualsLatest() {
	m := s.newManager(func() string { return "2.0.0" })
	m.mu.Lock()
	m.versions.ClaudeVersion = "2.0.0"
	m.mu.Unlock()

	latest, available := m.CheckClaudeUpdate()
	require.False(s.T(), available)
	require.Empty(s.T(), latest)
}

func (s *LifecycleSuite) TestCheckClaudeUpdate_EmptyLatest() {
	m := s.newManager(func() string { return "" })
	m.mu.Lock()
	m.versions.ClaudeVersion = "2.0.0"
	m.mu.Unlock()

	latest, available := m.CheckClaudeUpdate()
	require.False(s.T(), available)
	require.Empty(s.T(), latest)
}

func (s *LifecycleSuite) TestCheckClaudeUpdate_LatestTooLong() {
	longVersion := "123456789012345678901" // 21 chars > 20
	m := s.newManager(func() string { return longVersion })
	m.mu.Lock()
	m.versions.ClaudeVersion = "2.0.0"
	m.mu.Unlock()

	latest, available := m.CheckClaudeUpdate()
	require.False(s.T(), available)
	require.Empty(s.T(), latest)
}

func (s *LifecycleSuite) TestCheckClaudeUpdate_LatestExactly20Chars() {
	version20 := "12345678901234567890" // exactly 20 chars
	m := s.newManager(func() string { return version20 })
	m.mu.Lock()
	m.versions.ClaudeVersion = "2.0.0"
	m.mu.Unlock()

	latest, available := m.CheckClaudeUpdate()
	require.True(s.T(), available)
	require.Equal(s.T(), version20, latest)
}

func (s *LifecycleSuite) TestCheckClaudeUpdate_EmptyCurrent() {
	m := s.newManager(func() string { return "3.0.0" })
	// versions.ClaudeVersion is empty by default

	latest, available := m.CheckClaudeUpdate()
	require.False(s.T(), available)
	require.Empty(s.T(), latest)
}

func (s *LifecycleSuite) TestCheckClaudeUpdate_UpdateAvailable() {
	m := s.newManager(func() string { return "3.0.0" })
	m.mu.Lock()
	m.versions.ClaudeVersion = "2.0.0"
	m.mu.Unlock()

	latest, available := m.CheckClaudeUpdate()
	require.True(s.T(), available)
	require.Equal(s.T(), "3.0.0", latest)
}

// --- RunUpdateChecker ---

func (s *LifecycleSuite) TestRunUpdateChecker_CancelledContext() {
	m := s.newManager(func() string { return "" })

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	done := make(chan struct{})
	go func() {
		m.RunUpdateChecker(ctx, 1*time.Hour)
		close(done)
	}()

	select {
	case <-done:
		// Good, it returned.
	case <-time.After(2 * time.Second):
		s.T().Fatal("RunUpdateChecker did not return after context cancellation")
	}
}

func (s *LifecycleSuite) TestRunUpdateChecker_ChecksAtStartup() {
	var called atomic.Bool
	m := s.newManager(func() string { return "3.0.0" })
	m.mu.Lock()
	m.versions.ClaudeVersion = "2.0.0"
	m.mu.Unlock()

	s.broadcaster.On("BroadcastImageUpdateAvailable", mock.Anything).Run(func(_ mock.Arguments) {
		called.Store(true)
	}).Return()

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		m.RunUpdateChecker(ctx, 1*time.Hour)
		close(done)
	}()

	// Wait for the startup check to fire.
	require.Eventually(s.T(), called.Load, 2*time.Second, 10*time.Millisecond)

	cancel()
	<-done

	s.broadcaster.AssertCalled(s.T(), "BroadcastImageUpdateAvailable", events.ImageUpdateAvailableData{
		CurrentVersion: "2.0.0",
		LatestVersion:  "3.0.0",
		Component:      "claude_code",
	})
}

func (s *LifecycleSuite) TestRunUpdateChecker_TickerFires() {
	var callCount atomic.Int32
	m := s.newManager(func() string {
		callCount.Add(1)
		return "3.0.0"
	})
	m.mu.Lock()
	m.versions.ClaudeVersion = "2.0.0"
	m.mu.Unlock()

	s.broadcaster.On("BroadcastImageUpdateAvailable", mock.Anything).Return()

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		m.RunUpdateChecker(ctx, 10*time.Millisecond) // very short interval
		close(done)
	}()

	// Wait until latestClaudeVersion has been called at least twice
	// (once at startup, once from the ticker).
	require.Eventually(s.T(), func() bool {
		return callCount.Load() >= 2
	}, 2*time.Second, 5*time.Millisecond)

	cancel()
	<-done
}

// --- checkAndBroadcast ---

func (s *LifecycleSuite) TestCheckAndBroadcast_UpdateAvailable() {
	m := s.newManager(func() string { return "3.0.0" })
	m.mu.Lock()
	m.versions.ClaudeVersion = "2.0.0"
	m.mu.Unlock()

	s.broadcaster.On("BroadcastImageUpdateAvailable", mock.Anything).Return()

	m.checkAndBroadcast()

	s.broadcaster.AssertCalled(s.T(), "BroadcastImageUpdateAvailable", events.ImageUpdateAvailableData{
		CurrentVersion: "2.0.0",
		LatestVersion:  "3.0.0",
		Component:      "claude_code",
	})
}

func (s *LifecycleSuite) TestCheckAndBroadcast_NoUpdateAvailable() {
	m := s.newManager(func() string { return "2.0.0" })
	m.mu.Lock()
	m.versions.ClaudeVersion = "2.0.0"
	m.mu.Unlock()

	m.checkAndBroadcast()

	s.broadcaster.AssertNotCalled(s.T(), "BroadcastImageUpdateAvailable", mock.Anything)
}

func (s *LifecycleSuite) TestCheckAndBroadcast_NilBroadcaster() {
	s.sys.On("UserHomeDir").Return("/home/test", nil).Maybe()
	s.sys.On("ReadFile", "/home/test/.loop/image-versions.json").Return(nil, os.ErrNotExist).Maybe()

	m := NewImageLifecycleManager(
		s.client, nil, s.sys, nil,
		s.containerDir, s.imageName, s.loopVersion,
		func() string { return "3.0.0" },
	)
	m.mu.Lock()
	m.versions.ClaudeVersion = "2.0.0"
	m.mu.Unlock()

	// Should not panic with nil broadcaster.
	m.checkAndBroadcast()
}

// --- broadcastStatus ---

func (s *LifecycleSuite) TestBroadcastStatus_NilBroadcaster() {
	s.sys.On("UserHomeDir").Return("/home/test", nil).Maybe()
	s.sys.On("ReadFile", "/home/test/.loop/image-versions.json").Return(nil, os.ErrNotExist).Maybe()

	m := NewImageLifecycleManager(
		s.client, nil, s.sys, nil,
		s.containerDir, s.imageName, s.loopVersion,
		func() string { return "" },
	)

	// Should not panic.
	m.broadcastStatus()
}

func (s *LifecycleSuite) TestBroadcastStatus_WithBroadcaster() {
	m := s.newManager(func() string { return "" })
	m.mu.Lock()
	m.status = ImageBuildStatus{State: "building", Phase: "removing"}
	m.mu.Unlock()

	s.broadcaster.On("BroadcastImageBuildStatus", events.ImageBuildStatusData{
		State: "building",
		Phase: "removing",
	}).Return()

	m.broadcastStatus()

	s.broadcaster.AssertCalled(s.T(), "BroadcastImageBuildStatus", events.ImageBuildStatusData{
		State: "building",
		Phase: "removing",
	})
}

// --- loadVersions / saveVersions ---

func (s *LifecycleSuite) TestLoadVersions_Success() {
	v := ImageVersions{
		LoopVersion:   "1.2.3",
		ClaudeVersion: "4.5.6",
	}
	data, err := json.Marshal(v)
	require.NoError(s.T(), err)

	s.sys.On("UserHomeDir").Return("/home/test", nil)
	s.sys.On("ReadFile", "/home/test/.loop/image-versions.json").Return(data, nil)

	m := &ImageLifecycleManager{sys: s.sys}
	loaded := m.loadVersions()

	require.Equal(s.T(), "1.2.3", loaded.LoopVersion)
	require.Equal(s.T(), "4.5.6", loaded.ClaudeVersion)
}

func (s *LifecycleSuite) TestLoadVersions_HomeDirError() {
	s.sys.On("UserHomeDir").Return("", errors.New("no home"))

	m := &ImageLifecycleManager{sys: s.sys}
	loaded := m.loadVersions()

	require.Empty(s.T(), loaded.LoopVersion)
}

func (s *LifecycleSuite) TestLoadVersions_ReadFileError() {
	s.sys.On("UserHomeDir").Return("/home/test", nil)
	s.sys.On("ReadFile", "/home/test/.loop/image-versions.json").Return(nil, os.ErrNotExist)

	m := &ImageLifecycleManager{sys: s.sys}
	loaded := m.loadVersions()

	require.Empty(s.T(), loaded.LoopVersion)
}

func (s *LifecycleSuite) TestLoadVersions_InvalidJSON() {
	s.sys.On("UserHomeDir").Return("/home/test", nil)
	s.sys.On("ReadFile", "/home/test/.loop/image-versions.json").Return([]byte("{broken"), nil)

	m := &ImageLifecycleManager{sys: s.sys}
	loaded := m.loadVersions()

	require.Empty(s.T(), loaded.LoopVersion)
}

func (s *LifecycleSuite) TestSaveVersions_Success() {
	s.sys.On("UserHomeDir").Return("/home/test", nil)
	s.sys.On("WriteFile", "/home/test/.loop/image-versions.json", mock.Anything, os.FileMode(0o644)).Return(nil)

	m := &ImageLifecycleManager{
		sys:    s.sys,
		logger: slogDiscard(),
	}

	v := ImageVersions{LoopVersion: "1.0.0", ClaudeVersion: "2.0.0"}
	m.saveVersions(v)

	s.sys.AssertCalled(s.T(), "WriteFile", "/home/test/.loop/image-versions.json", mock.Anything, os.FileMode(0o644))
}

func (s *LifecycleSuite) TestSaveVersions_HomeDirError() {
	s.sys.On("UserHomeDir").Return("", errors.New("no home"))

	m := &ImageLifecycleManager{
		sys:    s.sys,
		logger: slogDiscard(),
	}

	// Should not panic and should return early.
	v := ImageVersions{LoopVersion: "1.0.0"}
	m.saveVersions(v)

	s.sys.AssertNotCalled(s.T(), "WriteFile", mock.Anything, mock.Anything, mock.Anything)
}

func (s *LifecycleSuite) TestSaveVersions_WriteFileError() {
	s.sys.On("UserHomeDir").Return("/home/test", nil)
	s.sys.On("WriteFile", "/home/test/.loop/image-versions.json", mock.Anything, os.FileMode(0o644)).Return(errors.New("write failed"))

	m := &ImageLifecycleManager{
		sys:    s.sys,
		logger: slogDiscard(),
	}

	// Should log warning but not panic.
	v := ImageVersions{LoopVersion: "1.0.0"}
	m.saveVersions(v)

	s.sys.AssertCalled(s.T(), "WriteFile", "/home/test/.loop/image-versions.json", mock.Anything, os.FileMode(0o644))
}

// --- SaveVersions (public) ---

func (s *LifecycleSuite) TestSaveVersions_Public_UpdatesAndPersists() {
	m := s.newManager(func() string { return "" })

	s.sys.On("WriteFile", "/home/test/.loop/image-versions.json", mock.Anything, os.FileMode(0o644)).Return(nil)

	v := ImageVersions{
		LoopVersion:   "5.0.0",
		ClaudeVersion: "6.0.0",
		BuiltAt:       time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC),
	}
	m.SaveVersions(v)

	got := m.Versions()
	require.Equal(s.T(), "5.0.0", got.LoopVersion)
	require.Equal(s.T(), "6.0.0", got.ClaudeVersion)

	s.sys.AssertCalled(s.T(), "WriteFile", "/home/test/.loop/image-versions.json", mock.Anything, os.FileMode(0o644))
}

// --- versionsFilePath ---

func (s *LifecycleSuite) TestVersionsFilePath_Success() {
	s.sys.On("UserHomeDir").Return("/home/test", nil)

	m := &ImageLifecycleManager{sys: s.sys}
	path := m.versionsFilePath()

	require.Equal(s.T(), "/home/test/.loop/image-versions.json", path)
}

func (s *LifecycleSuite) TestVersionsFilePath_HomeDirError() {
	s.sys.On("UserHomeDir").Return("", errors.New("no home"))

	m := &ImageLifecycleManager{sys: s.sys}
	path := m.versionsFilePath()

	require.Empty(s.T(), path)
}

// --- Rebuild integration test ---

func (s *LifecycleSuite) TestRebuild_SavesVersionsFile() {
	m := s.newManager(func() string { return "" })

	s.client.On("RemoveImageAndContainers", mock.Anything, s.imageName).Return(nil)
	s.client.On("ImageBuild", mock.Anything, s.containerDir, s.imageName).Return(nil)
	s.client.On("ImageInspectLabels", mock.Anything, s.imageName).Return(map[string]string{
		"loop.version":        "2.0.0",
		"loop.claude_version": "3.0.0",
	}, nil)
	s.broadcaster.On("BroadcastImageBuildStatus", mock.Anything).Return()

	var writtenData []byte
	s.sys.On("WriteFile", "/home/test/.loop/image-versions.json", mock.Anything, os.FileMode(0o644)).
		Run(func(args mock.Arguments) {
			writtenData = args.Get(1).([]byte)
		}).Return(nil)

	m.doRebuild(context.Background())

	require.NotEmpty(s.T(), writtenData)
	var saved ImageVersions
	require.NoError(s.T(), json.Unmarshal(writtenData, &saved))
	require.Equal(s.T(), "2.0.0", saved.LoopVersion)
	require.Equal(s.T(), "3.0.0", saved.ClaudeVersion)
	require.False(s.T(), saved.BuiltAt.IsZero())
}

// slogDiscard returns a discard slog.Logger for use in unit-level tests
// that construct ImageLifecycleManager directly (without NewImageLifecycleManager).
func slogDiscard() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
