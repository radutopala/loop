package monitor

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type mockRunner struct{ mock.Mock }

func (m *mockRunner) RunBash(ctx context.Context, script, channelID, dirPath, parentDirPath string) (string, error) {
	args := m.Called(ctx, script, channelID, dirPath, parentDirPath)
	return args.String(0), args.Error(1)
}

type event struct {
	channelID string
	typ       string
	data      any
}

type MonitorSuite struct {
	suite.Suite
	runner *mockRunner
	m      *Manager
	mu     sync.Mutex
	events []event
	wakes  chan string
	ids    int
}

func TestMonitorSuite(t *testing.T) {
	suite.Run(t, new(MonitorSuite))
}

func (s *MonitorSuite) SetupTest() {
	s.runner = new(mockRunner)
	s.events = nil
	s.wakes = make(chan string, 20)
	s.ids = 0
	s.m = NewManager(s.runner,
		func(_ context.Context, channelID, content string, interrupt bool) {
			if interrupt {
				content = "(interrupt) " + content
			}
			s.wakes <- channelID + ": " + content
		},
		func(channelID, typ string, data any) {
			s.mu.Lock()
			s.events = append(s.events, event{channelID, typ, data})
			s.mu.Unlock()
		},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.m.newID = func() string {
		s.ids++
		return "mon-" + string(rune('a'+s.ids-1))
	}
	base := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	s.m.now = func() time.Time { return base }
}

// blockUntilDone makes RunBash for script wait for its ctx, as a real
// container does until it is stopped.
func (s *MonitorSuite) blockUntilDone(script string) {
	s.runner.On("RunBash", mock.Anything, script, "ch1", "/repo", "").
		Run(func(args mock.Arguments) { <-args.Get(0).(context.Context).Done() }).
		Return("", context.Canceled)
}

func (s *MonitorSuite) finished() []Finished {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Finished
	for _, e := range s.events {
		if e.typ == EventFinished {
			out = append(out, e.data.(Finished))
		}
	}
	return out
}

func (s *MonitorSuite) wake() string {
	select {
	case w := <-s.wakes:
		return w
	case <-time.After(5 * time.Second):
		s.FailNow("no wake-up")
		return ""
	}
}

func (s *MonitorSuite) TestWakesWithTheResult() {
	tests := []struct {
		name      string
		output    string
		err       error
		interrupt bool
		want      string
	}{
		{"success", "time=x level=INFO msg=\"loop-dockerproxy started\" socket=/s\nMERGED\n", nil, false, `ch1: [monitor mon-a] "pr merge" exited with status 0. Output:` + "\n```\nMERGED\n```"},
		{"interrupt", "MERGED", nil, true, `ch1: (interrupt) [monitor mon-a] "pr merge" exited with status 0. Output:` + "\n```\nMERGED\n```"},
		{"failure", "boom", errors.New("script exited with status 2"), false, `ch1: [monitor mon-a] "pr merge" exited with status 2. Output:` + "\n```\nboom\n```"},
		{"only preamble", `time=x level=INFO msg="loop-dockerproxy started"`, errors.New("waiting for container: gone"), false, `ch1: [monitor mon-a] "pr merge" waiting for container: gone. No output.`},
		{"no output", "", errors.New("waiting for container: gone"), false, `ch1: [monitor mon-a] "pr merge" waiting for container: gone. No output.`},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			s.runner.On("RunBash", mock.Anything, "until done; do sleep 30; done", "ch1", "/repo", "/root").Return(tt.output, tt.err)

			mon, err := s.m.Start("ch1", "/repo", "/root", "until done; do sleep 30; done", "pr merge", 0, tt.interrupt)
			require.NoError(s.T(), err)
			require.Equal(s.T(), "mon-a", mon.ID)
			require.Equal(s.T(), tt.interrupt, mon.Interrupt)
			require.Equal(s.T(), 3600, mon.TimeoutSec)

			require.Equal(s.T(), tt.want, s.wake())
			s.m.Stop()
			require.Empty(s.T(), s.m.List("ch1"))
			s.mu.Lock()
			require.Equal(s.T(), EventStarted, s.events[0].typ)
			require.Equal(s.T(), *mon, s.events[0].data)
			require.Equal(s.T(), EventFinished, s.events[1].typ)
			s.mu.Unlock()
		})
	}
}

func (s *MonitorSuite) TestTimesOut() {
	s.blockUntilDone("sleep 999")
	_, err := s.m.Start("ch1", "/repo", "", "sleep 999", "slow", time.Millisecond, false)
	require.NoError(s.T(), err)

	require.Equal(s.T(), `ch1: [monitor mon-a] "slow" timed out after 1ms. No output.`, s.wake())
	s.m.Stop()
	require.Equal(s.T(), []Finished{{ID: "mon-a", ChannelID: "ch1", Status: "timed out after 1ms"}}, s.finished())
}

func (s *MonitorSuite) TestCancelWakesNobody() {
	s.blockUntilDone("sleep 999")
	mon, err := s.m.Start("ch1", "/repo", "", "sleep 999", "slow", 2*MaxTimeout, false)
	require.NoError(s.T(), err)
	require.Equal(s.T(), int(MaxTimeout/time.Second), mon.TimeoutSec)

	require.False(s.T(), s.m.Cancel("ch2", mon.ID))
	require.False(s.T(), s.m.Cancel("ch1", "mon-zz"))
	require.True(s.T(), s.m.Cancel("ch1", mon.ID))
	s.m.Stop()

	require.Equal(s.T(), []Finished{{ID: "mon-a", ChannelID: "ch1", Status: "cancelled"}}, s.finished())
	require.Empty(s.T(), s.wakes)
}

func (s *MonitorSuite) TestStopCancelsAll() {
	s.blockUntilDone("sleep 999")
	for range 2 {
		_, err := s.m.Start("ch1", "/repo", "", "sleep 999", "slow", 0, false)
		require.NoError(s.T(), err)
	}
	s.m.Stop()

	require.Len(s.T(), s.finished(), 2)
	require.Empty(s.T(), s.wakes)
	_, err := s.m.Start("ch1", "/repo", "", "true", "late", 0, false)
	require.ErrorIs(s.T(), err, ErrStopped)
}

func (s *MonitorSuite) TestLimitPerChannel() {
	s.blockUntilDone("sleep 999")
	s.runner.On("RunBash", mock.Anything, "sleep 999", "ch2", "/repo", "").
		Run(func(args mock.Arguments) { <-args.Get(0).(context.Context).Done() }).
		Return("", context.Canceled)
	for range MaxPerChannel {
		_, err := s.m.Start("ch1", "/repo", "", "sleep 999", "slow", 0, false)
		require.NoError(s.T(), err)
	}
	_, err := s.m.Start("ch1", "/repo", "", "sleep 999", "one more", 0, false)
	require.ErrorIs(s.T(), err, ErrLimit)

	_, err = s.m.Start("ch2", "/repo", "", "sleep 999", "elsewhere", 0, false)
	require.NoError(s.T(), err)
	s.m.Stop()
}

func (s *MonitorSuite) TestListsOldestFirst() {
	s.blockUntilDone("sleep 999")
	s.runner.On("RunBash", mock.Anything, "sleep 999", "ch2", "/repo", "").
		Run(func(args mock.Arguments) { <-args.Get(0).(context.Context).Done() }).
		Return("", context.Canceled)
	times := []time.Time{time.Unix(200, 0), time.Unix(100, 0), time.Unix(100, 0), time.Unix(50, 0)}
	i := 0
	s.m.now = func() time.Time { i++; return times[i-1] }

	for _, ch := range []string{"ch1", "ch1", "ch1", "ch2"} {
		_, err := s.m.Start(ch, "/repo", "", "sleep 999", "slow", 0, false)
		require.NoError(s.T(), err)
	}
	var ids []string
	for _, mon := range s.m.List("ch1") {
		ids = append(ids, mon.ID)
	}
	require.Equal(s.T(), []string{"mon-b", "mon-c", "mon-a"}, ids)
	require.Empty(s.T(), s.m.List("ch3"))
	s.m.Stop()
}

func (s *MonitorSuite) TestWakeMessageKeepsTheTail() {
	out := strings.Repeat("x", outputTailBytes) + "END"
	msg := wakeMessage(Monitor{ID: "mon-a", Description: "logs"}, "exited with status 0", out)
	require.True(s.T(), strings.HasSuffix(msg, "xEND\n```"))
	require.Contains(s.T(), msg, "```\n…xxx")
	require.NotContains(s.T(), msg, strings.Repeat("x", outputTailBytes))
}

func (s *MonitorSuite) TestNewIDs() {
	m := NewManager(s.runner, nil, nil, nil)
	id := m.newID()
	require.True(s.T(), strings.HasPrefix(id, "mon-"))
	require.NotEqual(s.T(), id, m.newID())
	require.False(s.T(), m.now().IsZero())
}
