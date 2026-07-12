package api

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/testutil"
)

type BranchPollerSuite struct {
	suite.Suite
}

func TestBranchPollerSuite(t *testing.T) {
	suite.Run(t, new(BranchPollerSuite))
}

// captureHub wraps EventsHub and records every emitted ChannelUpdated.
func newCaptureHub() (*EventsHub, *capturedEvents) {
	hub := NewEventsHub(testLogger())
	c := &capturedEvents{}
	hub.captureHook = func(e Event) {
		c.add(e)
	}
	return hub, c
}

type capturedEvents struct {
	mu     sync.Mutex
	events []Event
}

func (c *capturedEvents) add(e Event) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, e)
}

func (c *capturedEvents) snapshot() []Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Event, len(c.events))
	copy(out, c.events)
	return out
}

func (s *BranchPollerSuite) TestNewBranchPollerDefaultInterval() {
	p := NewBranchPoller(nil, nil, "", 0, testLogger())
	require.Equal(s.T(), 5*time.Second, p.interval)
}

func (s *BranchPollerSuite) TestNewBranchPollerCustomInterval() {
	p := NewBranchPoller(nil, nil, "", 250*time.Millisecond, testLogger())
	require.Equal(s.T(), 250*time.Millisecond, p.interval)
}

func (s *BranchPollerSuite) TestTickBroadcastsOnChange() {
	store := &testutil.MockStore{}
	hub, caps := newCaptureHub()

	store.On("ListChannels", mock.Anything).Return([]*db.Channel{
		{ChannelID: "ch-1", DirPath: "/repo/a"},
	}, nil)

	var branch atomicString
	branch.set("main")
	p := NewBranchPoller(store, hub, "", 10*time.Millisecond, testLogger())
	p.gitInfo = func(_ context.Context, _ string) gitState {
		return gitState{Branch: branch.get(), Commit: "abc1234"}
	}

	// Prime tick: no broadcast even though state differs from zero value.
	p.tick(context.Background(), true)
	require.Empty(s.T(), caps.snapshot())

	// Branch unchanged → no broadcast.
	p.tick(context.Background(), false)
	require.Empty(s.T(), caps.snapshot())

	// Branch changes → broadcast once.
	branch.set("feat/x")
	p.tick(context.Background(), false)
	evts := caps.snapshot()
	require.Len(s.T(), evts, 1)
	require.Equal(s.T(), EventChannelUpdated, evts[0].Type)
	require.Equal(s.T(), "ch-1", evts[0].ChannelID)

	// Second tick after broadcast — state matches, no further broadcast.
	p.tick(context.Background(), false)
	require.Len(s.T(), caps.snapshot(), 1)
}

func (s *BranchPollerSuite) TestTickFallsBackToLoopDir() {
	store := &testutil.MockStore{}
	hub, caps := newCaptureHub()

	store.On("ListChannels", mock.Anything).Return([]*db.Channel{
		{ChannelID: "ch-2", DirPath: ""},
	}, nil)

	var seenDir atomicString
	p := NewBranchPoller(store, hub, "/loop", 10*time.Millisecond, testLogger())
	p.gitInfo = func(_ context.Context, dir string) gitState {
		seenDir.set(dir)
		return gitState{Branch: "main"}
	}

	p.tick(context.Background(), false)
	require.Equal(s.T(), "/loop/ch-2/work", seenDir.get())
	require.Empty(s.T(), caps.snapshot()) // first observation, no prior state
}

func (s *BranchPollerSuite) TestTickSkipsEmptyDir() {
	store := &testutil.MockStore{}
	hub, caps := newCaptureHub()

	store.On("ListChannels", mock.Anything).Return([]*db.Channel{
		{ChannelID: "ch-no-dir", DirPath: ""},
	}, nil)

	p := NewBranchPoller(store, hub, "", 10*time.Millisecond, testLogger())
	called := false
	p.gitInfo = func(_ context.Context, _ string) gitState { called = true; return gitState{Branch: "main"} }

	p.tick(context.Background(), false)
	require.False(s.T(), called)
	require.Empty(s.T(), caps.snapshot())
}

func (s *BranchPollerSuite) TestTickHandlesStoreError() {
	store := &testutil.MockStore{}
	hub, caps := newCaptureHub()
	store.On("ListChannels", mock.Anything).Return(nil, errors.New("db down"))

	p := NewBranchPoller(store, hub, "", 10*time.Millisecond, testLogger())
	p.tick(context.Background(), false) // should not panic, no events
	require.Empty(s.T(), caps.snapshot())
}

func (s *BranchPollerSuite) TestTickNilStoreOrHub() {
	hub, _ := newCaptureHub()
	pNoStore := NewBranchPoller(nil, hub, "", 10*time.Millisecond, testLogger())
	pNoStore.tick(context.Background(), false)

	store := &testutil.MockStore{}
	pNoHub := NewBranchPoller(store, nil, "", 10*time.Millisecond, testLogger())
	pNoHub.tick(context.Background(), false)
	// no expectations on store: ListChannels must not have been called.
	store.AssertNotCalled(s.T(), "ListChannels")
}

func (s *BranchPollerSuite) TestTickPrunesStaleState() {
	store := &testutil.MockStore{}
	hub, _ := newCaptureHub()

	first := []*db.Channel{{ChannelID: "ch-a", DirPath: "/repo/a"}}
	second := []*db.Channel{}
	store.On("ListChannels", mock.Anything).Return(first, nil).Once()
	store.On("ListChannels", mock.Anything).Return(second, nil).Once()

	p := NewBranchPoller(store, hub, "", 10*time.Millisecond, testLogger())
	p.gitInfo = func(_ context.Context, _ string) gitState { return gitState{Branch: "main"} }

	p.tick(context.Background(), true)
	p.mu.Lock()
	_, present := p.state["ch-a"]
	p.mu.Unlock()
	require.True(s.T(), present)

	p.tick(context.Background(), false)
	p.mu.Lock()
	_, present = p.state["ch-a"]
	p.mu.Unlock()
	require.False(s.T(), present)
}

func (s *BranchPollerSuite) TestRunCancelsCleanly() {
	store := &testutil.MockStore{}
	hub, _ := newCaptureHub()
	store.On("ListChannels", mock.Anything).Return(([]*db.Channel)(nil), nil)

	p := NewBranchPoller(store, hub, "", 10*time.Millisecond, testLogger())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		p.Run(ctx)
		close(done)
	}()

	time.Sleep(30 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		s.T().Fatal("Run did not return after cancel")
	}
}

// atomicString is a tiny mutex-guarded string for test goroutines.
type atomicString struct {
	mu sync.Mutex
	v  string
}

func (a *atomicString) set(v string) { a.mu.Lock(); a.v = v; a.mu.Unlock() }
func (a *atomicString) get() string  { a.mu.Lock(); defer a.mu.Unlock(); return a.v }

// TestTickDedupesSharedDirs verifies the per-tick dir dedupe: channels and
// threads sharing a worktree dir must trigger a single gitInfo computation.
func (s *BranchPollerSuite) TestTickDedupesSharedDirs() {
	store := &testutil.MockStore{}
	hub, _ := newCaptureHub()

	store.On("ListChannels", mock.Anything).Return([]*db.Channel{
		{ChannelID: "wt", DirPath: "/repo/wt"},
		{ChannelID: "wt-thread-1", DirPath: "/repo/wt"},
		{ChannelID: "wt-thread-2", DirPath: "/repo/wt"},
		{ChannelID: "other", DirPath: "/repo/other"},
	}, nil)

	var mu sync.Mutex
	calls := map[string]int{}
	p := NewBranchPoller(store, hub, "", 10*time.Millisecond, testLogger())
	p.gitInfo = func(_ context.Context, dir string) gitState {
		mu.Lock()
		calls[dir]++
		mu.Unlock()
		return gitState{Branch: "main"}
	}

	p.tick(context.Background(), true)
	mu.Lock()
	defer mu.Unlock()
	require.Equal(s.T(), map[string]int{"/repo/wt": 1, "/repo/other": 1}, calls)
}

// TestSnapshot verifies the per-dir snapshot the /api/channels handler
// consumes: present after a tick, refreshed each tick, absent for unknown
// dirs.
func (s *BranchPollerSuite) TestSnapshot() {
	store := &testutil.MockStore{}
	hub, _ := newCaptureHub()
	store.On("ListChannels", mock.Anything).Return([]*db.Channel{
		{ChannelID: "ch-1", DirPath: "/repo/a"},
	}, nil)

	p := NewBranchPoller(store, hub, "", 10*time.Millisecond, testLogger())

	_, ok := p.Snapshot("/repo/a")
	require.False(s.T(), ok, "no snapshot before the first tick")

	p.gitInfo = func(_ context.Context, _ string) gitState { return gitState{Branch: "main", Commit: "abc1234"} }
	p.tick(context.Background(), true)

	st, ok := p.Snapshot("/repo/a")
	require.True(s.T(), ok)
	require.Equal(s.T(), gitState{Branch: "main", Commit: "abc1234"}, st)

	_, ok = p.Snapshot("/repo/unknown")
	require.False(s.T(), ok)
}
