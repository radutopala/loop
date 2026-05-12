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
	p.gitBranch = func(_ context.Context, _ string) string { return branch.get() }
	p.gitCommit = func(_ context.Context, _ string) string { return "abc1234" }
	p.gitDiff = func(_ context.Context, _ string) (int, int) { return 0, 0 }

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
	p.gitBranch = func(_ context.Context, dir string) string {
		seenDir.set(dir)
		return "main"
	}
	p.gitCommit = func(_ context.Context, _ string) string { return "" }
	p.gitDiff = func(_ context.Context, _ string) (int, int) { return 0, 0 }

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
	p.gitBranch = func(_ context.Context, _ string) string { called = true; return "main" }
	p.gitCommit = func(_ context.Context, _ string) string { return "" }
	p.gitDiff = func(_ context.Context, _ string) (int, int) { return 0, 0 }

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
	p.gitBranch = func(_ context.Context, _ string) string { return "main" }
	p.gitCommit = func(_ context.Context, _ string) string { return "" }
	p.gitDiff = func(_ context.Context, _ string) (int, int) { return 0, 0 }

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
