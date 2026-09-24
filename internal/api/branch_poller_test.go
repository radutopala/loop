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
	"github.com/radutopala/loop/internal/events"
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
	p.gitInfo = func(_ context.Context, _, _ string, _ gitState) gitState {
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

// TestTickPassesBaseAndPrev checks a worktree thread's dir is read against
// its base branch, every dir gets the last tick's state for reuse, and a
// change broadcasts the full state.
func (s *BranchPollerSuite) TestTickPassesBaseAndPrev() {
	store := &testutil.MockStore{}
	hub, caps := newCaptureHub()

	store.On("ListChannels", mock.Anything).Return([]*db.Channel{
		{ChannelID: "ch", DirPath: "/repo"},
		{ChannelID: "wt", DirPath: "/repo/.worktrees/wt", ParentID: "ch", Worktree: true, BaseBranch: "main"},
		{ChannelID: "task", DirPath: "/repo/.worktrees/wt", ParentID: "wt"},
	}, nil)

	type call struct {
		base string
		prev gitState
	}
	var mu sync.Mutex
	calls := map[string][]call{}
	commit := "aaaaaaa"
	p := NewBranchPoller(store, hub, "", 10*time.Millisecond, testLogger())
	p.gitInfo = func(_ context.Context, dir, base string, prev gitState) gitState {
		mu.Lock()
		defer mu.Unlock()
		calls[dir] = append(calls[dir], call{base, prev})
		return gitState{
			Branch: "b", Commit: commit, Subject: "s " + commit,
			Upstream: "origin/b", Ahead: 1, Behind: 2, SyncBase: base, BaseAhead: 3, BaseBehind: 4,
		}
	}

	p.tick(context.Background(), true)
	first := gitState{Branch: "b", Commit: "aaaaaaa", Subject: "s aaaaaaa", Upstream: "origin/b", Ahead: 1, Behind: 2, BaseAhead: 3, BaseBehind: 4}
	firstWt := first
	firstWt.SyncBase = "main"
	commit = "bbbbbbb"
	p.tick(context.Background(), false)

	require.Equal(s.T(), []call{{"", gitState{}}, {"", first}}, calls["/repo"])
	require.Equal(s.T(), []call{{"main", gitState{}}, {"main", firstWt}}, calls["/repo/.worktrees/wt"])

	evts := caps.snapshot()
	require.Len(s.T(), evts, 3)
	require.Equal(s.T(), events.ChannelUpdatedData{
		ChannelID: "wt", Branch: "b", Commit: "bbbbbbb", Subject: "s bbbbbbb",
		Upstream: "origin/b", Ahead: 1, Behind: 2, SyncBase: "main", BaseAhead: 3, BaseBehind: 4,
	}, evts[1].Data)
}

func (s *BranchPollerSuite) TestTickFallsBackToLoopDir() {
	store := &testutil.MockStore{}
	hub, caps := newCaptureHub()

	store.On("ListChannels", mock.Anything).Return([]*db.Channel{
		{ChannelID: "ch-2", DirPath: ""},
	}, nil)

	var seenDir atomicString
	p := NewBranchPoller(store, hub, "/loop", 10*time.Millisecond, testLogger())
	p.gitInfo = func(_ context.Context, dir, _ string, _ gitState) gitState {
		seenDir.set(dir)
		return gitState{Branch: "main"}
	}

	p.tick(context.Background(), false)
	require.Equal(s.T(), "/loop/ch-2/work", seenDir.get())
	// First seen after the prime tick: its state is broadcast, since the
	// sidebar's copy dates from when the channel was created.
	evs := caps.snapshot()
	require.Len(s.T(), evs, 1)
	require.Equal(s.T(), "ch-2", evs[0].ChannelID)
}

// TestTickNewChannelSkipsDirChange keeps the PR-cache invalidation to real
// changes: a channel's first observation isn't one.
func (s *BranchPollerSuite) TestTickNewChannelSkipsDirChange() {
	store := &testutil.MockStore{}
	hub, _ := newCaptureHub()
	store.On("ListChannels", mock.Anything).Return([]*db.Channel{{ChannelID: "ch-1", DirPath: "/repo"}}, nil)

	p := NewBranchPoller(store, hub, "", 10*time.Millisecond, testLogger())
	p.gitInfo = func(_ context.Context, _, _ string, _ gitState) gitState { return gitState{Branch: "main"} }
	var changed []string
	p.SetOnDirChange(func(dir string) { changed = append(changed, dir) })

	p.tick(context.Background(), false)
	require.Empty(s.T(), changed)
}

func (s *BranchPollerSuite) TestTickSkipsEmptyDir() {
	store := &testutil.MockStore{}
	hub, caps := newCaptureHub()

	store.On("ListChannels", mock.Anything).Return([]*db.Channel{
		{ChannelID: "ch-no-dir", DirPath: ""},
	}, nil)

	p := NewBranchPoller(store, hub, "", 10*time.Millisecond, testLogger())
	called := false
	p.gitInfo = func(_ context.Context, _, _ string, _ gitState) gitState {
		called = true
		return gitState{Branch: "main"}
	}

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
	p.gitInfo = func(_ context.Context, _, _ string, _ gitState) gitState { return gitState{Branch: "main"} }

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
	p.gitInfo = func(_ context.Context, dir, _ string, _ gitState) gitState {
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

	p.gitInfo = func(_ context.Context, _, _ string, _ gitState) gitState {
		return gitState{Branch: "main", Commit: "abc1234"}
	}
	p.tick(context.Background(), true)

	st, ok := p.Snapshot("/repo/a")
	require.True(s.T(), ok)
	require.Equal(s.T(), gitState{Branch: "main", Commit: "abc1234"}, st)

	_, ok = p.Snapshot("/repo/unknown")
	require.False(s.T(), ok)
}

// TestTickFiresOnDirChange verifies the per-dir change hook: fired once per
// changed dir per tick (deduped across channels sharing the dir), not fired
// on prime or when nothing changed.
func (s *BranchPollerSuite) TestTickFiresOnDirChange() {
	store := &testutil.MockStore{}
	hub, _ := newCaptureHub()
	store.On("ListChannels", mock.Anything).Return([]*db.Channel{
		{ChannelID: "a", DirPath: "/repo/x"},
		{ChannelID: "b", DirPath: "/repo/x"},
	}, nil)

	var branch atomicString
	branch.set("main")
	p := NewBranchPoller(store, hub, "", 10*time.Millisecond, testLogger())
	p.gitInfo = func(_ context.Context, _, _ string, _ gitState) gitState { return gitState{Branch: branch.get()} }

	var mu sync.Mutex
	fired := map[string]int{}
	p.SetOnDirChange(func(dir string) {
		mu.Lock()
		fired[dir]++
		mu.Unlock()
	})

	p.tick(context.Background(), true)  // prime: no hook
	p.tick(context.Background(), false) // unchanged: no hook
	mu.Lock()
	require.Empty(s.T(), fired)
	mu.Unlock()

	branch.set("feat/y")
	p.tick(context.Background(), false) // changed: fires ONCE for the shared dir
	mu.Lock()
	require.Equal(s.T(), map[string]int{"/repo/x": 1}, fired)
	mu.Unlock()
}
