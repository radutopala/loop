package container

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type mockContainerBroadcaster struct {
	mock.Mock
}

func (m *mockContainerBroadcaster) BroadcastContainerRegistered(data ContainerEventData) {
	m.Called(data)
}

func (m *mockContainerBroadcaster) BroadcastContainerRemoved(data ContainerEventData) {
	m.Called(data)
}

func (m *mockContainerBroadcaster) BroadcastContainerStatusChanged(data ContainerEventData) {
	m.Called(data)
}

type callbackShellCreator struct {
	fn func(ctx context.Context, channelID, dirPath, parentDirPath string) (string, error)
}

func (c *callbackShellCreator) CreateShellContainer(ctx context.Context, channelID, dirPath, parentDirPath string) (string, error) {
	return c.fn(ctx, channelID, dirPath, parentDirPath)
}

type ContainerRegistrySuite struct {
	suite.Suite
	reg         *Registry
	broadcaster *mockContainerBroadcaster
	now         time.Time
}

func TestContainerRegistrySuite(t *testing.T) {
	suite.Run(t, new(ContainerRegistrySuite))
}

func (s *ContainerRegistrySuite) SetupTest() {
	s.broadcaster = new(mockContainerBroadcaster)
	s.now = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	s.reg = NewRegistry(s.broadcaster)
	s.reg.SetTimeNow(func() time.Time { return s.now })
	s.reg.SetLogger(slog.Default())
}

func (s *ContainerRegistrySuite) TestRegisterAndList() {
	s.broadcaster.On("BroadcastContainerRegistered", mock.Anything).Times(3)

	s.reg.Register(&ContainerInfo{ContainerID: "c1", ChannelID: "ch-1", Type: ContainerTypeAgent})
	s.reg.Register(&ContainerInfo{ContainerID: "c2", ChannelID: "ch-1", Type: ContainerTypeShell})
	s.reg.Register(&ContainerInfo{ContainerID: "c3", ChannelID: "ch-2", Type: ContainerTypeChrome})

	all := s.reg.List()
	require.Len(s.T(), all, 3)

	ch1 := s.reg.ListByChannel("ch-1")
	require.Len(s.T(), ch1, 2)

	ch2 := s.reg.ListByChannel("ch-2")
	require.Len(s.T(), ch2, 1)
	require.Equal(s.T(), "c3", ch2[0].ContainerID)
}

func (s *ContainerRegistrySuite) TestRegisterSetsDefaults() {
	s.broadcaster.On("BroadcastContainerRegistered", mock.Anything)

	s.reg.Register(&ContainerInfo{ContainerID: "c1", ChannelID: "ch-1", Type: ContainerTypeAgent})

	info := s.reg.Get("c1")
	require.NotNil(s.T(), info)
	require.Equal(s.T(), ContainerStatusRunning, info.Status)
	require.Equal(s.T(), s.now, info.CreatedAt)
	require.Equal(s.T(), s.now, info.UpdatedAt)
}

func (s *ContainerRegistrySuite) TestRegisterDuplicateUpdates() {
	s.broadcaster.On("BroadcastContainerRegistered", mock.Anything).Once()
	s.broadcaster.On("BroadcastContainerStatusChanged", mock.Anything).Once()

	s.reg.Register(&ContainerInfo{ContainerID: "c1", ChannelID: "ch-1", Type: ContainerTypeAgent, ContainerName: "v1"})
	created := s.reg.Get("c1").CreatedAt

	later := s.now.Add(time.Hour)
	s.reg.timeNow = func() time.Time { return later }

	// Re-register same ID — should update metadata and broadcast status change.
	s.reg.Register(&ContainerInfo{ContainerID: "c1", ChannelID: "ch-1", Type: ContainerTypeAgent, ContainerName: "v2"})

	info := s.reg.Get("c1")
	require.Equal(s.T(), "v2", info.ContainerName)
	require.Equal(s.T(), created, info.CreatedAt, "CreatedAt should be preserved")
	require.Equal(s.T(), later, info.UpdatedAt)
}

func (s *ContainerRegistrySuite) TestUnregister() {
	s.broadcaster.On("BroadcastContainerRegistered", mock.Anything)
	s.broadcaster.On("BroadcastContainerRemoved", mock.Anything)

	s.reg.Register(&ContainerInfo{ContainerID: "c1", ChannelID: "ch-1", Type: ContainerTypeAgent})
	s.reg.Unregister("c1")

	require.Nil(s.T(), s.reg.Get("c1"))
	require.Empty(s.T(), s.reg.List())
}

func (s *ContainerRegistrySuite) TestUnregisterIdempotent() {
	s.broadcaster.On("BroadcastContainerRegistered", mock.Anything)
	s.broadcaster.On("BroadcastContainerRemoved", mock.Anything).Once()

	s.reg.Register(&ContainerInfo{ContainerID: "c1", ChannelID: "ch-1", Type: ContainerTypeAgent})
	s.reg.Unregister("c1")
	s.reg.Unregister("c1")          // no panic, no extra broadcast
	s.reg.Unregister("nonexistent") // no panic
}

func (s *ContainerRegistrySuite) TestUnregisterCleansUpChannel() {
	s.broadcaster.On("BroadcastContainerRegistered", mock.Anything)
	s.broadcaster.On("BroadcastContainerRemoved", mock.Anything)

	s.reg.Register(&ContainerInfo{ContainerID: "c1", ChannelID: "ch-1", Type: ContainerTypeAgent})
	s.reg.Unregister("c1")

	s.reg.mu.RLock()
	_, hasChannel := s.reg.byChannel["ch-1"]
	s.reg.mu.RUnlock()

	require.False(s.T(), hasChannel, "channel should be removed from byChannel map")
}

func (s *ContainerRegistrySuite) TestUpdateStatus() {
	s.broadcaster.On("BroadcastContainerRegistered", mock.Anything)
	s.broadcaster.On("BroadcastContainerStatusChanged", mock.Anything)

	s.reg.Register(&ContainerInfo{ContainerID: "c1", ChannelID: "ch-1", Type: ContainerTypeAgent})

	later := s.now.Add(time.Hour)
	s.reg.timeNow = func() time.Time { return later }

	s.reg.UpdateStatus("c1", ContainerStatusPendingRemoval)

	info := s.reg.Get("c1")
	require.Equal(s.T(), ContainerStatusPendingRemoval, info.Status)
	require.Equal(s.T(), later, info.UpdatedAt)
}

func (s *ContainerRegistrySuite) TestUpdateStatusNonExistent() {
	// Should be a no-op, no broadcast.
	s.reg.UpdateStatus("nonexistent", ContainerStatusPendingRemoval)
}

func (s *ContainerRegistrySuite) TestGet() {
	s.broadcaster.On("BroadcastContainerRegistered", mock.Anything)

	s.reg.Register(&ContainerInfo{ContainerID: "c1", ChannelID: "ch-1", Type: ContainerTypeShell})

	info := s.reg.Get("c1")
	require.NotNil(s.T(), info)
	require.Equal(s.T(), "ch-1", info.ChannelID)
	require.Equal(s.T(), ContainerTypeShell, info.Type)
}

func (s *ContainerRegistrySuite) TestGetNonExistent() {
	require.Nil(s.T(), s.reg.Get("nonexistent"))
}

func (s *ContainerRegistrySuite) TestListByChannelEmpty() {
	result := s.reg.ListByChannel("nonexistent")
	require.NotNil(s.T(), result)
	require.Empty(s.T(), result)
}

func (s *ContainerRegistrySuite) TestListEmpty() {
	result := s.reg.List()
	require.NotNil(s.T(), result)
	require.Empty(s.T(), result)
}

func (s *ContainerRegistrySuite) TestRunningChannelIDs() {
	s.broadcaster.On("BroadcastContainerRegistered", mock.Anything).Times(3)
	s.broadcaster.On("BroadcastContainerStatusChanged", mock.Anything)

	s.reg.Register(&ContainerInfo{ContainerID: "c1", ChannelID: "ch-1", Type: ContainerTypeAgent})
	s.reg.Register(&ContainerInfo{ContainerID: "c2", ChannelID: "ch-2", Type: ContainerTypeShell})
	s.reg.Register(&ContainerInfo{ContainerID: "c3", ChannelID: "ch-3", Type: ContainerTypeChrome})

	ids := s.reg.RunningChannelIDs(context.Background())
	require.Len(s.T(), ids, 3)
	require.Contains(s.T(), ids, "ch-1")
	require.Contains(s.T(), ids, "ch-2")
	require.Contains(s.T(), ids, "ch-3")
}

func (s *ContainerRegistrySuite) TestRunningChannelIDsExcludesPendingRemoval() {
	s.broadcaster.On("BroadcastContainerRegistered", mock.Anything).Times(2)
	s.broadcaster.On("BroadcastContainerStatusChanged", mock.Anything)

	s.reg.Register(&ContainerInfo{ContainerID: "c1", ChannelID: "ch-1", Type: ContainerTypeAgent})
	s.reg.Register(&ContainerInfo{ContainerID: "c2", ChannelID: "ch-2", Type: ContainerTypeShell})

	s.reg.UpdateStatus("c1", ContainerStatusPendingRemoval)

	ids := s.reg.RunningChannelIDs(context.Background())
	require.Len(s.T(), ids, 1)
	require.Contains(s.T(), ids, "ch-2")
}

func (s *ContainerRegistrySuite) TestRunningChannelIDsEmpty() {
	ids := s.reg.RunningChannelIDs(context.Background())
	require.NotNil(s.T(), ids)
	require.Empty(s.T(), ids)
}

func (s *ContainerRegistrySuite) TestBroadcastOnRegister() {
	s.broadcaster.On("BroadcastContainerRegistered", ContainerEventData{
		ContainerID: "c1",
		ChannelID:   "ch-1",
		Type:        "agent",
		Status:      "running",
	}).Once()

	s.reg.Register(&ContainerInfo{ContainerID: "c1", ChannelID: "ch-1", Type: ContainerTypeAgent})

	s.broadcaster.AssertExpectations(s.T())
}

func (s *ContainerRegistrySuite) TestBroadcastOnUnregister() {
	s.broadcaster.On("BroadcastContainerRegistered", mock.Anything)
	s.broadcaster.On("BroadcastContainerRemoved", ContainerEventData{
		ContainerID: "c1",
		ChannelID:   "ch-1",
		Type:        "agent",
		Status:      "running",
	}).Once()

	s.reg.Register(&ContainerInfo{ContainerID: "c1", ChannelID: "ch-1", Type: ContainerTypeAgent})
	s.reg.Unregister("c1")

	s.broadcaster.AssertExpectations(s.T())
}

func (s *ContainerRegistrySuite) TestBroadcastOnUpdateStatus() {
	s.broadcaster.On("BroadcastContainerRegistered", mock.Anything)
	s.broadcaster.On("BroadcastContainerStatusChanged", ContainerEventData{
		ContainerID: "c1",
		ChannelID:   "ch-1",
		Type:        "agent",
		Status:      "pending-removal",
	}).Once()

	s.reg.Register(&ContainerInfo{ContainerID: "c1", ChannelID: "ch-1", Type: ContainerTypeAgent})
	s.reg.UpdateStatus("c1", ContainerStatusPendingRemoval)

	s.broadcaster.AssertExpectations(s.T())
}

func (s *ContainerRegistrySuite) TestNilBroadcaster() {
	reg := NewRegistry(nil)
	reg.timeNow = func() time.Time { return s.now }

	// All operations should work without panic.
	reg.Register(&ContainerInfo{ContainerID: "c1", ChannelID: "ch-1", Type: ContainerTypeAgent})
	reg.UpdateStatus("c1", ContainerStatusPendingRemoval)
	reg.Unregister("c1")
}

func (s *ContainerRegistrySuite) TestSetBroadcaster() {
	reg := NewRegistry(nil)
	reg.timeNow = func() time.Time { return s.now }

	// Register without broadcaster — no broadcast.
	reg.Register(&ContainerInfo{ContainerID: "c1", ChannelID: "ch-1", Type: ContainerTypeAgent})

	// Set broadcaster, subsequent ops should broadcast.
	bc := new(mockContainerBroadcaster)
	bc.On("BroadcastContainerRemoved", mock.Anything).Once()
	reg.SetBroadcaster(bc)

	reg.Unregister("c1")
	bc.AssertExpectations(s.T())
}

func (s *ContainerRegistrySuite) TestConcurrentAccess() {
	s.broadcaster.On("BroadcastContainerRegistered", mock.Anything).Maybe()
	s.broadcaster.On("BroadcastContainerRemoved", mock.Anything).Maybe()
	s.broadcaster.On("BroadcastContainerStatusChanged", mock.Anything).Maybe()

	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := fmt.Sprintf("c-%d", i)
			ch := fmt.Sprintf("ch-%d", i%3)
			s.reg.Register(&ContainerInfo{ContainerID: id, ChannelID: ch, Type: ContainerTypeAgent})
			s.reg.Get(id)
			s.reg.List()
			s.reg.ListByChannel(ch)
			s.reg.FindByChannelAndType(ch, ContainerTypeAgent)
			s.reg.UpdateStatus(id, ContainerStatusPendingRemoval)
			_ = s.reg.RunningChannelIDs(context.Background())
			s.reg.Unregister(id)
		}(i)
	}
	wg.Wait()
}

func (s *ContainerRegistrySuite) TestFindByChannelAndType() {
	s.broadcaster.On("BroadcastContainerRegistered", mock.Anything).Times(3)

	s.reg.Register(&ContainerInfo{ContainerID: "c1", ChannelID: "ch-1", Type: ContainerTypeAgent})
	s.reg.Register(&ContainerInfo{ContainerID: "c2", ChannelID: "ch-1", Type: ContainerTypeShell})
	s.reg.Register(&ContainerInfo{ContainerID: "c3", ChannelID: "ch-1", Type: ContainerTypeChrome})

	info := s.reg.FindByChannelAndType("ch-1", ContainerTypeShell)
	require.NotNil(s.T(), info)
	require.Equal(s.T(), "c2", info.ContainerID)

	info = s.reg.FindByChannelAndType("ch-1", ContainerTypeChrome)
	require.NotNil(s.T(), info)
	require.Equal(s.T(), "c3", info.ContainerID)
}

func (s *ContainerRegistrySuite) TestFindByChannelAndTypeNotFound() {
	require.Nil(s.T(), s.reg.FindByChannelAndType("ch-1", ContainerTypeShell))
}

func (s *ContainerRegistrySuite) TestFindByChannelAndTypeIgnoresPendingRemoval() {
	s.broadcaster.On("BroadcastContainerRegistered", mock.Anything)
	s.broadcaster.On("BroadcastContainerStatusChanged", mock.Anything)

	s.reg.Register(&ContainerInfo{ContainerID: "c1", ChannelID: "ch-1", Type: ContainerTypeShell})
	s.reg.UpdateStatus("c1", ContainerStatusPendingRemoval)

	require.Nil(s.T(), s.reg.FindByChannelAndType("ch-1", ContainerTypeShell))
}

func (s *ContainerRegistrySuite) TestRegisterSingletonReturnsExisting() {
	s.broadcaster.On("BroadcastContainerRegistered", mock.Anything).Once()

	// Register first shell.
	first := s.reg.Register(&ContainerInfo{ContainerID: "shell-1", ChannelID: "ch-1", Type: ContainerTypeShell})
	require.Equal(s.T(), "shell-1", first.ContainerID)

	// Attempt to register a second shell for the same channel — should get back the first.
	second := s.reg.Register(&ContainerInfo{ContainerID: "shell-2", ChannelID: "ch-1", Type: ContainerTypeShell})
	require.Equal(s.T(), "shell-1", second.ContainerID)

	// Only one container should be in the registry.
	require.Len(s.T(), s.reg.List(), 1)
}

func (s *ContainerRegistrySuite) TestRegisterSingletonChromeReturnsExisting() {
	s.broadcaster.On("BroadcastContainerRegistered", mock.Anything).Once()

	first := s.reg.Register(&ContainerInfo{ContainerID: "chrome-1", ChannelID: "ch-1", Type: ContainerTypeChrome})
	require.Equal(s.T(), "chrome-1", first.ContainerID)

	second := s.reg.Register(&ContainerInfo{ContainerID: "chrome-2", ChannelID: "ch-1", Type: ContainerTypeChrome})
	require.Equal(s.T(), "chrome-1", second.ContainerID)

	require.Len(s.T(), s.reg.List(), 1)
}

func (s *ContainerRegistrySuite) TestRegisterSingletonAllowsAfterPendingRemoval() {
	s.broadcaster.On("BroadcastContainerRegistered", mock.Anything).Times(2)
	s.broadcaster.On("BroadcastContainerStatusChanged", mock.Anything)

	s.reg.Register(&ContainerInfo{ContainerID: "shell-1", ChannelID: "ch-1", Type: ContainerTypeShell})
	s.reg.UpdateStatus("shell-1", ContainerStatusPendingRemoval)

	// Now a new shell can be registered (old one is pending-removal).
	result := s.reg.Register(&ContainerInfo{ContainerID: "shell-2", ChannelID: "ch-1", Type: ContainerTypeShell})
	require.Equal(s.T(), "shell-2", result.ContainerID)
	require.Len(s.T(), s.reg.List(), 2)
}

func (s *ContainerRegistrySuite) TestRegisterAgentNoSingletonConstraint() {
	s.broadcaster.On("BroadcastContainerRegistered", mock.Anything).Times(3)

	s.reg.Register(&ContainerInfo{ContainerID: "agent-1", ChannelID: "ch-1", Type: ContainerTypeAgent})
	s.reg.Register(&ContainerInfo{ContainerID: "agent-2", ChannelID: "ch-1", Type: ContainerTypeAgent})
	s.reg.Register(&ContainerInfo{ContainerID: "agent-3", ChannelID: "ch-1", Type: ContainerTypeAgent})

	require.Len(s.T(), s.reg.List(), 3)
}

func (s *ContainerRegistrySuite) TestRegisterReturnsInfo() {
	s.broadcaster.On("BroadcastContainerRegistered", mock.Anything)

	result := s.reg.Register(&ContainerInfo{ContainerID: "c1", ChannelID: "ch-1", Type: ContainerTypeAgent})
	require.Equal(s.T(), "c1", result.ContainerID)
	require.Equal(s.T(), ContainerStatusRunning, result.Status)
}

func (s *ContainerRegistrySuite) TestRestore() {
	containers := []*ContainerInfo{
		{ContainerID: "c1", ChannelID: "ch-1", Type: ContainerTypeAgent},
		{ContainerID: "c2", ChannelID: "ch-1", Type: ContainerTypeShell, ContainerName: "loop-shell-abc"},
		{ContainerID: "c3", ChannelID: "ch-2", Type: ContainerTypeChrome},
	}

	s.reg.Restore(containers)

	all := s.reg.List()
	require.Len(s.T(), all, 3)

	info := s.reg.Get("c1")
	require.NotNil(s.T(), info)
	require.Equal(s.T(), ContainerStatusRunning, info.Status)
	require.Equal(s.T(), s.now, info.CreatedAt)

	info = s.reg.Get("c2")
	require.Equal(s.T(), "loop-shell-abc", info.ContainerName)

	ch1 := s.reg.ListByChannel("ch-1")
	require.Len(s.T(), ch1, 2)
}

func (s *ContainerRegistrySuite) TestRestoreSkipsExisting() {
	s.broadcaster.On("BroadcastContainerRegistered", mock.Anything)

	s.reg.Register(&ContainerInfo{ContainerID: "c1", ChannelID: "ch-1", Type: ContainerTypeAgent, ContainerName: "original"})

	s.reg.Restore([]*ContainerInfo{
		{ContainerID: "c1", ChannelID: "ch-1", Type: ContainerTypeAgent, ContainerName: "restored"},
	})

	info := s.reg.Get("c1")
	require.Equal(s.T(), "original", info.ContainerName, "existing entry should not be overwritten")
}

func (s *ContainerRegistrySuite) TestRestorePreservesCreatedAt() {
	existing := time.Date(2025, 6, 15, 10, 0, 0, 0, time.UTC)
	s.reg.Restore([]*ContainerInfo{
		{ContainerID: "c1", ChannelID: "ch-1", Type: ContainerTypeAgent, CreatedAt: existing},
	})

	info := s.reg.Get("c1")
	require.Equal(s.T(), existing, info.CreatedAt, "restore should preserve provided CreatedAt")
}

func (s *ContainerRegistrySuite) TestRestoreNoBroadcast() {
	// No broadcasts should happen during restore.
	s.reg.Restore([]*ContainerInfo{
		{ContainerID: "c1", ChannelID: "ch-1", Type: ContainerTypeAgent},
	})

	s.broadcaster.AssertNotCalled(s.T(), "BroadcastContainerRegistered", mock.Anything)
}

func (s *ContainerRegistrySuite) TestRestoreEmpty() {
	s.reg.Restore(nil)
	require.Empty(s.T(), s.reg.List())
}

func (s *ContainerRegistrySuite) TestReconcileRemovesStaleEntries() {
	s.broadcaster.On("BroadcastContainerRegistered", mock.Anything).Times(3)
	s.broadcaster.On("BroadcastContainerRemoved", mock.Anything).Times(2)

	s.reg.Register(&ContainerInfo{ContainerID: "c1", ChannelID: "ch-1", Type: ContainerTypeAgent})
	s.reg.Register(&ContainerInfo{ContainerID: "c2", ChannelID: "ch-1", Type: ContainerTypeShell})
	s.reg.Register(&ContainerInfo{ContainerID: "c3", ChannelID: "ch-2", Type: ContainerTypeChrome})

	// Only c2 is still alive in Docker.
	liveIDs := map[string]struct{}{"c2": {}}
	removed := s.reg.Reconcile(liveIDs)

	require.Len(s.T(), removed, 2)
	require.ElementsMatch(s.T(), []string{"c1", "c3"}, removed)
	require.Len(s.T(), s.reg.List(), 1)
	require.NotNil(s.T(), s.reg.Get("c2"))
	require.Nil(s.T(), s.reg.Get("c1"))
	require.Nil(s.T(), s.reg.Get("c3"))
}

func (s *ContainerRegistrySuite) TestReconcileNoStaleEntries() {
	s.broadcaster.On("BroadcastContainerRegistered", mock.Anything).Once()

	s.reg.Register(&ContainerInfo{ContainerID: "c1", ChannelID: "ch-1", Type: ContainerTypeAgent})

	liveIDs := map[string]struct{}{"c1": {}}
	removed := s.reg.Reconcile(liveIDs)

	require.Empty(s.T(), removed)
	require.Len(s.T(), s.reg.List(), 1)
}

func (s *ContainerRegistrySuite) TestReconcileEmptyRegistry() {
	removed := s.reg.Reconcile(map[string]struct{}{})
	require.Empty(s.T(), removed)
}

type mockContainerInfoLister struct {
	mock.Mock
}

func (m *mockContainerInfoLister) ListContainerInfos(ctx context.Context) ([]*ContainerInfo, error) {
	args := m.Called(ctx)
	if v := args.Get(0); v != nil {
		return v.([]*ContainerInfo), args.Error(1)
	}
	return nil, args.Error(1)
}

func (s *ContainerRegistrySuite) TestRunReconcileLoop() {
	s.broadcaster.On("BroadcastContainerRegistered", mock.Anything).Times(2)
	s.broadcaster.On("BroadcastContainerRemoved", mock.Anything).Once()

	s.reg.Register(&ContainerInfo{ContainerID: "c1", ChannelID: "ch-1", Type: ContainerTypeAgent})
	s.reg.Register(&ContainerInfo{ContainerID: "c2", ChannelID: "ch-1", Type: ContainerTypeShell})

	lister := new(mockContainerInfoLister)
	// Only c1 is alive — c2 should be removed.
	lister.On("ListContainerInfos", mock.Anything).Return([]*ContainerInfo{
		{ContainerID: "c1", Status: ContainerStatusRunning},
	}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.reg.RunReconcileLoop(ctx, lister, 10*time.Millisecond, 5*time.Minute, slog.Default())
		close(done)
	}()

	// Wait for at least one tick to process.
	require.Eventually(s.T(), func() bool {
		return s.reg.Get("c2") == nil
	}, time.Second, 5*time.Millisecond)

	cancel()
	<-done

	require.NotNil(s.T(), s.reg.Get("c1"))
	require.Nil(s.T(), s.reg.Get("c2"))
}

func (s *ContainerRegistrySuite) TestRunReconcileLoopListError() {
	s.broadcaster.On("BroadcastContainerRegistered", mock.Anything).Once()

	s.reg.Register(&ContainerInfo{ContainerID: "c1", ChannelID: "ch-1", Type: ContainerTypeAgent})

	lister := new(mockContainerInfoLister)
	lister.On("ListContainerInfos", mock.Anything).Return(nil, fmt.Errorf("docker down"))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.reg.RunReconcileLoop(ctx, lister, 10*time.Millisecond, 5*time.Minute, slog.Default())
		close(done)
	}()

	// Wait for a tick to fire.
	time.Sleep(30 * time.Millisecond)
	cancel()
	<-done

	// Container should still be registered — error path does not remove entries.
	require.NotNil(s.T(), s.reg.Get("c1"))
}

func (s *ContainerRegistrySuite) TestRunReconcileLoopSchedulesRemovalForStopped() {
	s.broadcaster.On("BroadcastContainerRegistered", mock.Anything).Once()
	s.broadcaster.On("BroadcastContainerStatusChanged", mock.Anything)

	var scheduledDelay atomic.Value
	s.reg.SetAfterFunc(func(d time.Duration, _ func()) *time.Timer {
		scheduledDelay.Store(d)
		return time.NewTimer(time.Hour) // don't actually fire
	})

	s.reg.Register(&ContainerInfo{ContainerID: "c1", ChannelID: "ch-1", Type: ContainerTypeAgent})
	require.Equal(s.T(), ContainerStatusRunning, s.reg.Get("c1").Status)

	lister := new(mockContainerInfoLister)
	// Docker reports c1 as stopped.
	lister.On("ListContainerInfos", mock.Anything).Return([]*ContainerInfo{
		{ContainerID: "c1", Status: ContainerStatusStopped},
	}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.reg.RunReconcileLoop(ctx, lister, 10*time.Millisecond, 5*time.Minute, slog.Default())
		close(done)
	}()

	// ScheduleRemove should have been called, which sets pending-removal.
	require.Eventually(s.T(), func() bool {
		return s.reg.Get("c1").Status == ContainerStatusPendingRemoval
	}, time.Second, 5*time.Millisecond)

	cancel()
	<-done

	// Verify the timer was scheduled with the correct delay.
	require.Equal(s.T(), 5*time.Minute, scheduledDelay.Load())
}

func (s *ContainerRegistrySuite) TestRunReconcileLoopSkipsPendingRemoval() {
	s.broadcaster.On("BroadcastContainerRegistered", mock.Anything).Once()
	s.broadcaster.On("BroadcastContainerStatusChanged", mock.Anything).Once() // for UpdateStatus to pending-removal

	s.reg.Register(&ContainerInfo{ContainerID: "c1", ChannelID: "ch-1", Type: ContainerTypeAgent})
	s.reg.UpdateStatus("c1", ContainerStatusPendingRemoval)

	lister := new(mockContainerInfoLister)
	// Docker reports c1 as stopped, but registry has pending-removal — should not overwrite.
	lister.On("ListContainerInfos", mock.Anything).Return([]*ContainerInfo{
		{ContainerID: "c1", Status: ContainerStatusStopped},
	}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.reg.RunReconcileLoop(ctx, lister, 10*time.Millisecond, 5*time.Minute, slog.Default())
		close(done)
	}()

	// Wait for a tick to fire.
	time.Sleep(30 * time.Millisecond)
	cancel()
	<-done

	// Status should remain pending-removal, not overwritten to stopped.
	require.Equal(s.T(), ContainerStatusPendingRemoval, s.reg.Get("c1").Status)
}

// --- RemoveContainer tests ---

type mockRemover struct {
	mock.Mock
}

func (m *mockRemover) ContainerRemove(ctx context.Context, containerID string) error {
	return m.Called(ctx, containerID).Error(0)
}

func (s *ContainerRegistrySuite) TestRemoveContainerSuccess() {
	s.broadcaster.On("BroadcastContainerRegistered", mock.Anything)
	s.broadcaster.On("BroadcastContainerRemoved", mock.Anything)

	s.reg.Register(&ContainerInfo{ContainerID: "c1", ChannelID: "ch-1", Type: ContainerTypeAgent})

	remover := new(mockRemover)
	remover.On("ContainerRemove", mock.Anything, "c1").Return(nil)
	s.reg.SetContainerRemover(remover)

	err := s.reg.RemoveContainer(context.Background(), "c1")
	require.NoError(s.T(), err)
	require.Nil(s.T(), s.reg.Get("c1"), "container should be unregistered after removal")
	remover.AssertCalled(s.T(), "ContainerRemove", mock.Anything, "c1")
}

func (s *ContainerRegistrySuite) TestRemoveContainerDockerError() {
	s.broadcaster.On("BroadcastContainerRegistered", mock.Anything)

	s.reg.Register(&ContainerInfo{ContainerID: "c1", ChannelID: "ch-1", Type: ContainerTypeAgent})

	remover := new(mockRemover)
	remover.On("ContainerRemove", mock.Anything, "c1").Return(fmt.Errorf("remove failed"))
	s.reg.SetContainerRemover(remover)

	err := s.reg.RemoveContainer(context.Background(), "c1")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "remove failed")
	require.NotNil(s.T(), s.reg.Get("c1"), "container should remain registered when removal fails")
}

func (s *ContainerRegistrySuite) TestRemoveContainerNoRemover() {
	err := s.reg.RemoveContainer(context.Background(), "c1")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "container remover not configured")
}

func (s *ContainerRegistrySuite) TestSetContainerRemover() {
	require.Nil(s.T(), s.reg.remover)
	remover := new(mockRemover)
	s.reg.SetContainerRemover(remover)
	require.NotNil(s.T(), s.reg.remover)
}

// --- ScheduleRemove tests ---

func (s *ContainerRegistrySuite) TestScheduleRemoveFiresAfterDelay() {
	s.broadcaster.On("BroadcastContainerRegistered", mock.Anything)
	s.broadcaster.On("BroadcastContainerStatusChanged", mock.Anything)
	s.broadcaster.On("BroadcastContainerRemoved", mock.Anything)

	remover := new(mockRemover)
	remover.On("ContainerRemove", mock.Anything, "c1").Return(nil)
	s.reg.SetContainerRemover(remover)

	s.reg.Register(&ContainerInfo{ContainerID: "c1", ChannelID: "ch-1", Type: ContainerTypeAgent})

	var capturedDelay time.Duration
	var capturedFn func()
	s.reg.SetAfterFunc(func(d time.Duration, fn func()) *time.Timer {
		capturedDelay = d
		capturedFn = fn
		return time.NewTimer(0)
	})

	s.reg.ScheduleRemove("c1", 5*time.Minute)

	// Status should be pending-removal.
	info := s.reg.Get("c1")
	require.NotNil(s.T(), info)
	require.Equal(s.T(), ContainerStatusPendingRemoval, info.Status)
	require.Equal(s.T(), 5*time.Minute, capturedDelay)

	// Fire the timer callback.
	capturedFn()

	require.Nil(s.T(), s.reg.Get("c1"), "container should be unregistered after timer fires")
	remover.AssertCalled(s.T(), "ContainerRemove", mock.Anything, "c1")
}

func (s *ContainerRegistrySuite) TestScheduleRemoveRemoverError() {
	s.broadcaster.On("BroadcastContainerRegistered", mock.Anything)
	s.broadcaster.On("BroadcastContainerStatusChanged", mock.Anything)
	s.broadcaster.On("BroadcastContainerRemoved", mock.Anything)

	remover := new(mockRemover)
	remover.On("ContainerRemove", mock.Anything, "c1").Return(fmt.Errorf("container busy"))
	s.reg.SetContainerRemover(remover)
	s.reg.SetLogger(slog.Default())

	s.reg.Register(&ContainerInfo{ContainerID: "c1", ChannelID: "ch-1", Type: ContainerTypeAgent})

	var capturedFn func()
	s.reg.SetAfterFunc(func(d time.Duration, fn func()) *time.Timer {
		capturedFn = fn
		return time.NewTimer(0)
	})

	s.reg.ScheduleRemove("c1", time.Minute)
	capturedFn()

	// Container should still be unregistered even if Docker removal fails.
	require.Nil(s.T(), s.reg.Get("c1"))
	remover.AssertCalled(s.T(), "ContainerRemove", mock.Anything, "c1")
}

func (s *ContainerRegistrySuite) TestScheduleRemoveNoRemover() {
	s.broadcaster.On("BroadcastContainerRegistered", mock.Anything)
	s.broadcaster.On("BroadcastContainerStatusChanged", mock.Anything)
	s.broadcaster.On("BroadcastContainerRemoved", mock.Anything)

	s.reg.Register(&ContainerInfo{ContainerID: "c1", ChannelID: "ch-1", Type: ContainerTypeAgent})

	var capturedFn func()
	s.reg.SetAfterFunc(func(d time.Duration, fn func()) *time.Timer {
		capturedFn = fn
		return time.NewTimer(0)
	})

	// No remover configured — should still unregister.
	s.reg.ScheduleRemove("c1", time.Minute)
	capturedFn()

	require.Nil(s.T(), s.reg.Get("c1"))
}

func (s *ContainerRegistrySuite) TestScheduleRemoveCancelsPreviousTimer() {
	s.broadcaster.On("BroadcastContainerRegistered", mock.Anything)
	s.broadcaster.On("BroadcastContainerStatusChanged", mock.Anything)

	s.reg.Register(&ContainerInfo{ContainerID: "c1", ChannelID: "ch-1", Type: ContainerTypeAgent})

	var fns []func()
	s.reg.SetAfterFunc(func(d time.Duration, fn func()) *time.Timer {
		fns = append(fns, fn)
		return time.NewTimer(time.Hour) // won't fire
	})

	s.reg.ScheduleRemove("c1", time.Minute)
	s.reg.ScheduleRemove("c1", 2*time.Minute)

	// Two timer callbacks captured, but the first timer was stopped.
	require.Len(s.T(), fns, 2)
	// Container should still be registered (no timer fired yet).
	require.NotNil(s.T(), s.reg.Get("c1"))
}

func (s *ContainerRegistrySuite) TestRemoveContainerCancelsScheduledTimer() {
	s.broadcaster.On("BroadcastContainerRegistered", mock.Anything)
	s.broadcaster.On("BroadcastContainerStatusChanged", mock.Anything)
	s.broadcaster.On("BroadcastContainerRemoved", mock.Anything)

	remover := new(mockRemover)
	remover.On("ContainerRemove", mock.Anything, "c1").Return(nil)
	s.reg.SetContainerRemover(remover)

	s.reg.Register(&ContainerInfo{ContainerID: "c1", ChannelID: "ch-1", Type: ContainerTypeAgent})

	s.reg.SetAfterFunc(func(d time.Duration, fn func()) *time.Timer {
		return time.NewTimer(time.Hour) // won't fire
	})

	s.reg.ScheduleRemove("c1", 5*time.Minute)

	// Now remove immediately — should cancel the pending timer.
	err := s.reg.RemoveContainer(context.Background(), "c1")
	require.NoError(s.T(), err)
	require.Nil(s.T(), s.reg.Get("c1"))

	// Verify the timer was cleaned up from the map.
	s.reg.timersMu.Lock()
	_, hasTimer := s.reg.timers["c1"]
	s.reg.timersMu.Unlock()
	require.False(s.T(), hasTimer, "timer should be removed after RemoveContainer")
}

func (s *ContainerRegistrySuite) TestSetAfterFunc() {
	called := false
	custom := func(d time.Duration, fn func()) *time.Timer {
		called = true
		return time.NewTimer(0)
	}
	s.reg.SetAfterFunc(custom)

	s.broadcaster.On("BroadcastContainerRegistered", mock.Anything)
	s.broadcaster.On("BroadcastContainerStatusChanged", mock.Anything)

	s.reg.Register(&ContainerInfo{ContainerID: "c1", ChannelID: "ch-1", Type: ContainerTypeAgent})
	s.reg.ScheduleRemove("c1", time.Second)
	require.True(s.T(), called)
}

// --- FindOrCreateShell tests ---

type mockCreator struct {
	mock.Mock
}

func (m *mockCreator) CreateShellContainer(ctx context.Context, channelID, dirPath, parentDirPath string) (string, error) {
	args := m.Called(ctx, channelID, dirPath, parentDirPath)
	return args.String(0), args.Error(1)
}

func (s *ContainerRegistrySuite) TestFindOrCreateShellExisting() {
	s.broadcaster.On("BroadcastContainerRegistered", mock.Anything)

	s.reg.Register(&ContainerInfo{ContainerID: "existing-123", ChannelID: "ch-1", Type: ContainerTypeShell})

	creator := new(mockCreator)
	s.reg.SetShellCreator(creator)

	id, err := s.reg.FindOrCreateShell(context.Background(), "ch-1", "", "")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "existing-123", id)
	creator.AssertNotCalled(s.T(), "CreateShellContainer", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func (s *ContainerRegistrySuite) TestFindOrCreateShellCreate() {
	creator := new(mockCreator)
	creator.On("CreateShellContainer", mock.Anything, "ch-1", "/projects/app", "").Return("new-456", nil)
	s.reg.SetShellCreator(creator)

	id, err := s.reg.FindOrCreateShell(context.Background(), "ch-1", "/projects/app", "")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "new-456", id)
	creator.AssertCalled(s.T(), "CreateShellContainer", mock.Anything, "ch-1", "/projects/app", "")
}

func (s *ContainerRegistrySuite) TestFindOrCreateShellCreateError() {
	creator := new(mockCreator)
	creator.On("CreateShellContainer", mock.Anything, "ch-1", "", "").Return("", fmt.Errorf("create failed"))
	s.reg.SetShellCreator(creator)

	_, err := s.reg.FindOrCreateShell(context.Background(), "ch-1", "", "")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "create failed")
}

func (s *ContainerRegistrySuite) TestFindOrCreateShellNoCreator() {
	_, err := s.reg.FindOrCreateShell(context.Background(), "ch-1", "", "")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "shell creator not configured")
}

func (s *ContainerRegistrySuite) TestFindOrCreateShellIgnoresAgentContainers() {
	s.broadcaster.On("BroadcastContainerRegistered", mock.Anything)

	s.reg.Register(&ContainerInfo{ContainerID: "agent-1", ChannelID: "ch-1", Type: ContainerTypeAgent})

	creator := new(mockCreator)
	creator.On("CreateShellContainer", mock.Anything, "ch-1", "", "").Return("new-shell", nil)
	s.reg.SetShellCreator(creator)

	id, err := s.reg.FindOrCreateShell(context.Background(), "ch-1", "", "")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "new-shell", id)
}

func (s *ContainerRegistrySuite) TestFindOrCreateShellIgnoresPendingRemoval() {
	s.broadcaster.On("BroadcastContainerRegistered", mock.Anything)
	s.broadcaster.On("BroadcastContainerStatusChanged", mock.Anything)

	s.reg.Register(&ContainerInfo{ContainerID: "old-shell", ChannelID: "ch-1", Type: ContainerTypeShell})
	s.reg.UpdateStatus("old-shell", ContainerStatusPendingRemoval)

	creator := new(mockCreator)
	creator.On("CreateShellContainer", mock.Anything, "ch-1", "", "").Return("new-shell", nil)
	s.reg.SetShellCreator(creator)

	id, err := s.reg.FindOrCreateShell(context.Background(), "ch-1", "", "")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "new-shell", id)
}

func (s *ContainerRegistrySuite) TestFindOrCreateShellConcurrent() {
	s.broadcaster.On("BroadcastContainerRegistered", mock.Anything).Maybe()

	creatorStarted := make(chan struct{})
	creatorDone := make(chan struct{})
	var createCount int32

	creator := &callbackShellCreator{
		fn: func(_ context.Context, channelID, _, _ string) (string, error) {
			atomic.AddInt32(&createCount, 1)
			close(creatorStarted)
			<-creatorDone
			s.reg.Register(&ContainerInfo{ContainerID: "shell-1", ChannelID: channelID, Type: ContainerTypeShell})
			return "shell-1", nil
		},
	}
	s.reg.SetShellCreator(creator)

	// Goroutine 1: enters lock, starts creating.
	var id1 string
	var err1 error
	done1 := make(chan struct{})
	go func() {
		id1, err1 = s.reg.FindOrCreateShell(context.Background(), "ch-1", "", "")
		close(done1)
	}()

	<-creatorStarted

	// Goroutine 2: fast-path misses, blocks on lock.
	done2 := make(chan struct{})
	var id2 string
	var err2 error
	go func() {
		id2, err2 = s.reg.FindOrCreateShell(context.Background(), "ch-1", "", "")
		close(done2)
	}()

	time.Sleep(10 * time.Millisecond)
	close(creatorDone)

	<-done1
	<-done2
	require.NoError(s.T(), err1)
	require.NoError(s.T(), err2)
	require.Equal(s.T(), "shell-1", id1)
	require.Equal(s.T(), "shell-1", id2)
	require.Equal(s.T(), int32(1), atomic.LoadInt32(&createCount), "creator should be called exactly once")
}

func (s *ContainerRegistrySuite) TestSetShellCreator() {
	require.Nil(s.T(), s.reg.creator)
	creator := new(mockCreator)
	s.reg.SetShellCreator(creator)
	require.NotNil(s.T(), s.reg.creator)
}
