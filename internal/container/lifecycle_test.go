package container

import (
	"context"
	"errors"
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

func (s *LifecycleSuite) TestVersions_ReadsFromLabelsWithBuiltAt() {
	m := s.newManager(func() string { return "" })

	s.client.On("ImageInspectLabels", mock.Anything, s.imageName).Return(map[string]string{
		"loop.version":        "3.0.0",
		"loop.claude_version": "5.0.0",
		"loop.built_at":       "2026-03-28T10:00:00Z",
	}, nil)

	v := m.Versions()
	require.Equal(s.T(), "3.0.0", v.LoopVersion)
	require.Equal(s.T(), "5.0.0", v.ClaudeVersion)
	require.Equal(s.T(), 2026, v.BuiltAt.Year())
}

func (s *LifecycleSuite) TestVersions_ReadsFromLabels() {
	m := s.newManager(func() string { return "" })

	s.client.On("ImageInspectLabels", mock.Anything, s.imageName).Return(map[string]string{
		"loop.version":        "3.0.0",
		"loop.claude_version": "5.0.0",
	}, nil)

	v := m.Versions()
	require.Equal(s.T(), "3.0.0", v.LoopVersion)
	require.Equal(s.T(), "5.0.0", v.ClaudeVersion)
}

func (s *LifecycleSuite) TestVersions_InspectLabelsError_FallsBackToCached() {
	m := s.newManager(func() string { return "" })
	m.mu.Lock()
	m.versions = ImageVersions{LoopVersion: "1.0.0", ClaudeVersion: "2.0.0"}
	m.mu.Unlock()

	s.client.On("ImageInspectLabels", mock.Anything, s.imageName).Return(nil, errors.New("inspect failed"))

	v := m.Versions()
	require.Equal(s.T(), "1.0.0", v.LoopVersion)
	require.Equal(s.T(), "2.0.0", v.ClaudeVersion)
}

func (s *LifecycleSuite) TestVersions_InspectLabelsNilMap_FallsBackToCached() {
	m := s.newManager(func() string { return "" })

	s.client.On("ImageInspectLabels", mock.Anything, s.imageName).Return(nil, nil)

	v := m.Versions()
	require.Empty(s.T(), v.LoopVersion)
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

func (s *LifecycleSuite) TestRemoveImage_UnregistersContainers() {
	m := s.newManager(func() string { return "" })

	reg := NewRegistry(nil)
	reg.Register(&ContainerInfo{ContainerID: "c1", ChannelID: "ch-1", Type: ContainerTypeAgent})
	reg.Register(&ContainerInfo{ContainerID: "c2", ChannelID: "ch-2", Type: ContainerTypeShell})
	m.SetContainerRegistry(reg)

	s.client.On("RemoveImageAndContainers", mock.Anything, s.imageName).Return(nil)

	err := m.RemoveImage(context.Background())
	require.NoError(s.T(), err)

	// Registry should be empty after image removal.
	require.Empty(s.T(), reg.List())
}

func (s *LifecycleSuite) TestRemoveImage_ErrorDoesNotUnregister() {
	m := s.newManager(func() string { return "" })

	reg := NewRegistry(nil)
	reg.Register(&ContainerInfo{ContainerID: "c1", ChannelID: "ch-1", Type: ContainerTypeAgent})
	m.SetContainerRegistry(reg)

	s.client.On("RemoveImageAndContainers", mock.Anything, s.imageName).Return(errors.New("fail"))

	err := m.RemoveImage(context.Background())
	require.Error(s.T(), err)

	// Registry should still have entries — removal failed.
	require.Len(s.T(), reg.List(), 1)
}

// --- ReclaimSpace ---

func (s *LifecycleSuite) TestReclaimSpace_Success() {
	m := s.newManager(func() string { return "" })

	s.client.On("PruneBuildCache", mock.Anything, time.Duration(0)).Return(uint64(4096), nil)
	s.client.On("PruneDanglingImages", mock.Anything).Return(uint64(8192), nil)

	result, err := m.ReclaimSpace(context.Background())
	require.NoError(s.T(), err)
	require.Equal(s.T(), uint64(4096), result.BuildCacheReclaimed)
	require.Equal(s.T(), uint64(8192), result.ImagesReclaimed)
	require.Equal(s.T(), uint64(12288), result.TotalReclaimed)
	s.client.AssertExpectations(s.T())
}

func (s *LifecycleSuite) TestReclaimSpace_BuildCacheError() {
	m := s.newManager(func() string { return "" })

	s.client.On("PruneBuildCache", mock.Anything, time.Duration(0)).Return(uint64(0), errors.New("cache fail"))

	result, err := m.ReclaimSpace(context.Background())
	require.EqualError(s.T(), err, "cache fail")
	require.Zero(s.T(), result.TotalReclaimed)
	// Images prune must not run once the cache prune fails.
	s.client.AssertNotCalled(s.T(), "PruneDanglingImages", mock.Anything)
}

func (s *LifecycleSuite) TestReclaimSpace_ImagesErrorStillReportsCache() {
	m := s.newManager(func() string { return "" })

	s.client.On("PruneBuildCache", mock.Anything, time.Duration(0)).Return(uint64(4096), nil)
	s.client.On("PruneDanglingImages", mock.Anything).Return(uint64(0), errors.New("images fail"))

	result, err := m.ReclaimSpace(context.Background())
	require.EqualError(s.T(), err, "images fail")
	require.Equal(s.T(), uint64(4096), result.BuildCacheReclaimed)
	require.Equal(s.T(), uint64(4096), result.TotalReclaimed)
	require.Zero(s.T(), result.ImagesReclaimed)
}

func (s *LifecycleSuite) TestSetContainerRegistry() {
	m := s.newManager(func() string { return "" })
	require.Nil(s.T(), m.registry)

	reg := NewRegistry(nil)
	m.SetContainerRegistry(reg)
	require.NotNil(s.T(), m.registry)
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

	// UpdateAvailable should be cached.
	ua := m.UpdateAvailable()
	require.NotNil(s.T(), ua)
	require.Equal(s.T(), "3.0.0", ua.LatestVersion)
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

func (s *LifecycleSuite) TestSetStatus() {
	m := s.newManager(func() string { return "" })
	m.SetStatus(ImageBuildStatus{State: "building", Phase: "pulling"})

	m.mu.Lock()
	require.Equal(s.T(), "building", m.status.State)
	require.Equal(s.T(), "pulling", m.status.Phase)
	m.mu.Unlock()
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

func (s *LifecycleSuite) TestRebuildChildrenNilRebuilderIsNoop() {
	m := s.newManager(nil)
	m.RebuildChildren(context.Background())
}

func (s *LifecycleSuite) TestRebuildChildrenInvokesWiredRebuilder() {
	m := s.newManager(nil)
	called := false
	m.SetChildRebuilder(func(context.Context) { called = true })
	m.RebuildChildren(context.Background())
	require.True(s.T(), called)
}
