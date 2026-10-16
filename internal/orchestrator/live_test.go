package orchestrator

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/bot"
	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/events"
	"github.com/radutopala/loop/internal/types"
)

type MockLiveRunner struct {
	mock.Mock
}

func (m *MockLiveRunner) StartLive(ctx context.Context, req *agent.AgentRequest, hooks agent.LiveHooks) error {
	return m.Called(ctx, req, hooks).Error(0)
}

func (m *MockLiveRunner) StopLive(channelID, reason string) error {
	return m.Called(channelID, reason).Error(0)
}

func (m *MockLiveRunner) StopLiveTask(channelID, taskID string) error {
	return m.Called(channelID, taskID).Error(0)
}

func (m *MockLiveRunner) InterruptLive(channelID string) error {
	return m.Called(channelID).Error(0)
}

func (m *MockLiveRunner) LiveInfo(channelID string) (agent.LiveInfo, bool) {
	args := m.Called(channelID)
	return args.Get(0).(agent.LiveInfo), args.Bool(1)
}

func (m *MockLiveRunner) LiveChannels() []string {
	args := m.Called()
	return args.Get(0).([]string)
}

// livePublished is one live.status event the orchestrator published.
type livePublished struct {
	channelID string
	eventType string
	data      any
}

// liveRecorder records the orchestrator's published live events.
type liveRecorder struct {
	mu     sync.Mutex
	events []livePublished
}

func (r *liveRecorder) publish(channelID, eventType string, data any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, livePublished{channelID, eventType, data})
}

func (r *liveRecorder) all() []livePublished {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]livePublished(nil), r.events...)
}

// withLive wires a mock live runner and a recorder of published events,
// with live sessions on for every platform.
func (s *OrchestratorSuite) withLive() (*MockLiveRunner, *liveRecorder) {
	lr := new(MockLiveRunner)
	rec := &liveRecorder{}
	s.orch.SetLiveRunner(lr, rec.publish)
	s.orch.cfg.Store(&config.Config{LiveSessions: true, ContainerTimeout: time.Minute})
	return lr, rec
}

func (s *OrchestratorSuite) TestSetLiveRunnerAndEndLiveSession() {
	require.ErrorIs(s.T(), s.orch.EndLiveSession("ch-1", "why"), ErrLiveUnavailable)

	lr, _ := s.withLive()
	require.Same(s.T(), lr, s.orch.live)
	require.NotNil(s.T(), s.orch.livePublish)

	lr.On("StopLive", "ch-1", "why").Return(errors.New("stop failed")).Once()
	require.EqualError(s.T(), s.orch.EndLiveSession("ch-1", "why"), "stop failed")
	lr.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestStopLiveTask() {
	require.ErrorIs(s.T(), s.orch.StopLiveTask("ch-1", "b1"), ErrLiveUnavailable)

	lr, _ := s.withLive()
	lr.On("StopLiveTask", "ch-1", "b1").Return(errors.New("no task")).Once()
	require.EqualError(s.T(), s.orch.StopLiveTask("ch-1", "b1"), "no task")
	lr.AssertExpectations(s.T())
}

func (s *OrchestratorSuite) TestSweepLiveSessions() {
	// Without a live runner there is nothing to sweep.
	s.orch.SweepLiveSessions(s.ctx)
	s.store.AssertNotCalled(s.T(), "GetChannel", mock.Anything, mock.Anything)

	lr, _ := s.withLive()
	lr.On("LiveChannels").Return([]string{"gone", "kept", "broken"})
	s.store.On("GetChannel", s.ctx, "gone").Return(nil, nil)
	s.store.On("GetChannel", s.ctx, "kept").Return(&db.Channel{ChannelID: "kept"}, nil)
	s.store.On("GetChannel", s.ctx, "broken").Return(nil, errors.New("db error"))
	lr.On("StopLive", "gone", "deleted").Return(nil).Once()

	s.orch.SweepLiveSessions(s.ctx)
	lr.AssertExpectations(s.T())
	lr.AssertNumberOfCalls(s.T(), "StopLive", 1)
}

// Deleting a thread or a channel ends the live sessions of channels gone
// with it.
func (s *OrchestratorSuite) TestHandleChannelDeleteSweepsLiveSessions() {
	tests := []struct {
		name     string
		isThread bool
		setup    func()
	}{
		{
			name: "thread", isThread: true,
			setup: func() {
				s.store.On("DeleteChannel", s.ctx, "gone").Return(nil)
			},
		},
		{
			name: "channel",
			setup: func() {
				s.store.On("DeleteChannelsByParentID", s.ctx, "gone").Return(nil)
				s.store.On("DeleteChannel", s.ctx, "gone").Return(nil)
			},
		},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			lr, _ := s.withLive()
			s.orch.removeMCPConfig = func(string, string) error { return nil }
			// Found while being deleted, gone when swept.
			s.store.On("GetChannel", s.ctx, "gone").Return(&db.Channel{ChannelID: "gone"}, nil).Once()
			s.store.On("ListChannelIDsByParentID", s.ctx, "gone").Return([]string{}, nil).Maybe()
			s.store.On("GetChannel", s.ctx, "gone").Return(nil, nil)
			tt.setup()
			lr.On("LiveChannels").Return([]string{"gone"})
			lr.On("StopLive", "gone", "deleted").Return(nil).Once()

			s.orch.HandleChannelDelete(s.ctx, "gone", tt.isThread)
			lr.AssertExpectations(s.T())
		})
	}
}

func (s *OrchestratorSuite) TestLiveSession() {
	started := time.Unix(1700000000, 0)
	info := agent.LiveInfo{ChannelID: "ch-1", StartedAt: started, SessionID: "sess-1"}
	tests := []struct {
		name      string
		noRunner  bool
		cfg       config.Config
		running   bool
		channel   *db.Channel
		channelEr error
		want      LiveStatus
	}{
		{name: "no live runner", noRunner: true, want: LiveStatus{}},
		{
			name: "running and enabled", cfg: config.Config{LiveSessions: true}, running: true,
			channel: &db.Channel{ChannelID: "ch-1", Platform: types.PlatformLocal},
			want:    LiveStatus{Running: true, Enabled: true, LiveInfo: info},
		},
		{
			name:    "not running, enabled for listed platform",
			cfg:     config.Config{LiveSessions: true, LiveSessionPlatforms: []types.Platform{types.PlatformLocal}},
			channel: &db.Channel{ChannelID: "ch-1", Platform: types.PlatformLocal},
			want:    LiveStatus{Enabled: true},
		},
		{
			name: "platform not listed", running: true,
			cfg:     config.Config{LiveSessions: true, LiveSessionPlatforms: []types.Platform{types.PlatformLocal}},
			channel: &db.Channel{ChannelID: "ch-1", Platform: types.PlatformSlack},
			want:    LiveStatus{Running: true, LiveInfo: info},
		},
		{
			name: "turned off", cfg: config.Config{}, running: true,
			channel: &db.Channel{ChannelID: "ch-1", Platform: types.PlatformLocal},
			want:    LiveStatus{Running: true, LiveInfo: info},
		},
		{
			name: "hidden kind", cfg: config.Config{LiveSessions: true},
			channel: &db.Channel{ChannelID: "ch-1", Platform: types.PlatformLocal, Kind: db.ChannelKindLearn},
			want:    LiveStatus{},
		},
		{
			name: "channel missing", cfg: config.Config{LiveSessions: true}, running: true,
			want: LiveStatus{Running: true, LiveInfo: info},
		},
		{
			name: "channel lookup error", cfg: config.Config{LiveSessions: true}, running: true,
			channelEr: errors.New("db error"),
			want:      LiveStatus{Running: true, LiveInfo: info},
		},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			if !tt.noRunner {
				lr, _ := s.withLive()
				cfg := tt.cfg
				s.orch.cfg.Store(&cfg)
				if tt.running {
					lr.On("LiveInfo", "ch-1").Return(info, true)
				} else {
					lr.On("LiveInfo", "ch-1").Return(agent.LiveInfo{}, false)
				}
				s.store.On("GetChannel", s.ctx, "ch-1").Return(tt.channel, tt.channelEr)
			}
			require.Equal(s.T(), tt.want, s.orch.LiveSession(s.ctx, "ch-1"))
		})
	}
}

func (s *OrchestratorSuite) TestUseLive() {
	local := &db.Channel{ChannelID: "ch-1", Platform: types.PlatformLocal}
	chat := func() *agent.AgentRequest { return &agent.AgentRequest{AgentID: agent.ChatAgentID} }
	user := &bot.IncomingMessage{AuthorID: "user-1"}
	tests := []struct {
		name     string
		noRunner bool
		cfg      config.Config
		req      *agent.AgentRequest
		msg      *bot.IncomingMessage
		channel  *db.Channel
		want     bool
	}{
		{name: "plain chat turn", cfg: config.Config{LiveSessions: true}, req: chat(), msg: user, channel: local, want: true},
		{
			name: "listed platform",
			cfg:  config.Config{LiveSessions: true, LiveSessionPlatforms: []types.Platform{types.PlatformLocal}},
			req:  chat(), msg: user, channel: local, want: true,
		},
		{name: "no live runner", noRunner: true, cfg: config.Config{LiveSessions: true}, req: chat(), msg: user, channel: local},
		{name: "kill switch", cfg: config.Config{}, req: chat(), msg: user, channel: local},
		{
			name: "platform not listed",
			cfg:  config.Config{LiveSessions: true, LiveSessionPlatforms: []types.Platform{types.PlatformDiscord}},
			req:  chat(), msg: user, channel: local,
		},
		{
			name: "hidden learn thread", cfg: config.Config{LiveSessions: true}, req: chat(), msg: user,
			channel: &db.Channel{ChannelID: "ch-1", Platform: types.PlatformLocal, Kind: db.ChannelKindLearn},
		},
		{
			name: "hidden plugin thread", cfg: config.Config{LiveSessions: true}, req: chat(), msg: user,
			channel: &db.Channel{ChannelID: "ch-1", Platform: types.PlatformLocal, Kind: db.PluginKind("p1")},
		},
		{name: "other agent", cfg: config.Config{LiveSessions: true}, req: &agent.AgentRequest{AgentID: "reviewer"}, msg: user, channel: local},
		{name: "read only", cfg: config.Config{LiveSessions: true}, req: &agent.AgentRequest{AgentID: agent.ChatAgentID, ReadOnly: true}, msg: user, channel: local},
		{name: "review mode", cfg: config.Config{LiveSessions: true}, req: &agent.AgentRequest{AgentID: agent.ChatAgentID, ReviewMode: true}, msg: user, channel: local},
		{name: "own system prompt", cfg: config.Config{LiveSessions: true}, req: &agent.AgentRequest{AgentID: agent.ChatAgentID, SystemPrompt: "be brief"}, msg: user, channel: local},
		{name: "extra MCP args", cfg: config.Config{LiveSessions: true}, req: &agent.AgentRequest{AgentID: agent.ChatAgentID, MCPArgs: []string{"--x"}}, msg: user, channel: local},
		{name: "learn author", cfg: config.Config{LiveSessions: true}, req: chat(), msg: &bot.IncomingMessage{AuthorID: learnAuthorID}, channel: local},
		{name: "explain author", cfg: config.Config{LiveSessions: true}, req: chat(), msg: &bot.IncomingMessage{AuthorID: explainAuthorID}, channel: local},
		{name: "plugin author", cfg: config.Config{LiveSessions: true}, req: chat(), msg: &bot.IncomingMessage{AuthorID: pluginAuthorID}, channel: local},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			if !tt.noRunner {
				s.withLive()
			}
			cfg := tt.cfg
			s.orch.cfg.Store(&cfg)
			require.Equal(s.T(), tt.want, s.orch.useLive(tt.req, tt.msg, tt.channel))
		})
	}
}

func (s *OrchestratorSuite) TestRunChat() {
	running := agent.LiveInfo{ChannelID: "ch-1", SessionID: "sess-1"}
	tests := []struct {
		name     string
		noRunner bool
		req      *agent.AgentRequest
		setup    func(lr *MockLiveRunner)
		check    func(lr *MockLiveRunner, rec *liveRecorder)
	}{
		{
			name: "no live runner", noRunner: true,
			req: &agent.AgentRequest{ChannelID: "ch-1", Live: true},
		},
		{
			name: "live turn starts the session and publishes it",
			req:  &agent.AgentRequest{ChannelID: "ch-1", Live: true, SessionID: "sess-1"},
			setup: func(lr *MockLiveRunner) {
				lr.On("LiveInfo", "ch-1").Return(agent.LiveInfo{}, false).Once()
				lr.On("StartLive", mock.Anything, mock.Anything, mock.Anything).Return(nil).Once()
				lr.On("LiveInfo", "ch-1").Return(running, true).Once()
				s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1", Platform: types.PlatformLocal}, nil)
			},
			check: func(lr *MockLiveRunner, rec *liveRecorder) {
				require.Equal(s.T(), []livePublished{{
					channelID: "ch-1", eventType: EventLiveStatus,
					data: LiveStatus{Running: true, Enabled: true, LiveInfo: running},
				}}, rec.all())
			},
		},
		{
			name: "start failure runs one-shot",
			req:  &agent.AgentRequest{ChannelID: "ch-1", Live: true},
			setup: func(lr *MockLiveRunner) {
				lr.On("LiveInfo", "ch-1").Return(agent.LiveInfo{}, false)
				lr.On("StartLive", mock.Anything, mock.Anything, mock.Anything).Return(errors.New("no docker")).Once()
			},
			check: func(_ *MockLiveRunner, rec *liveRecorder) {
				require.Empty(s.T(), rec.all())
			},
		},
		{
			name: "live turn with a running session",
			req:  &agent.AgentRequest{ChannelID: "ch-1", Live: true, SessionID: "sess-1"},
			setup: func(lr *MockLiveRunner) {
				lr.On("LiveInfo", "ch-1").Return(running, true)
			},
		},
		{
			name: "one-shot run on the live session ends it",
			req:  &agent.AgentRequest{ChannelID: "ch-1", SessionID: "sess-1"},
			setup: func(lr *MockLiveRunner) {
				lr.On("LiveInfo", "ch-1").Return(running, true)
				lr.On("StopLive", "ch-1", "one-shot run").Return(nil).Once()
			},
		},
		{
			name: "one-shot fork keeps the session",
			req:  &agent.AgentRequest{ChannelID: "ch-1", SessionID: "sess-1", ForkSession: true},
			setup: func(lr *MockLiveRunner) {
				lr.On("LiveInfo", "ch-1").Return(running, true)
			},
		},
		{
			name: "one-shot run on another session keeps the session",
			req:  &agent.AgentRequest{ChannelID: "ch-1", SessionID: "sess-2"},
			setup: func(lr *MockLiveRunner) {
				lr.On("LiveInfo", "ch-1").Return(running, true)
			},
		},
		{
			name: "one-shot run without a session",
			req:  &agent.AgentRequest{ChannelID: "ch-1", SessionID: "sess-1"},
			setup: func(lr *MockLiveRunner) {
				lr.On("LiveInfo", "ch-1").Return(agent.LiveInfo{}, false)
			},
		},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			var (
				lr  *MockLiveRunner
				rec *liveRecorder
			)
			if !tt.noRunner {
				lr, rec = s.withLive()
				tt.setup(lr)
			}
			want := &agent.AgentResponse{Response: "ok"}
			s.runner.On("Run", mock.Anything, tt.req).Return(want, nil).Once()

			resp, err := s.orch.runChat(s.ctx, tt.req)
			require.NoError(s.T(), err)
			require.Same(s.T(), want, resp)
			if lr != nil {
				lr.AssertExpectations(s.T())
				if tt.check != nil {
					tt.check(lr, rec)
				} else {
					lr.AssertNotCalled(s.T(), "StartLive", mock.Anything, mock.Anything, mock.Anything)
				}
			}
			s.runner.AssertExpectations(s.T())
		})
	}
}

// startedHooks starts ch-1's live session through runChat and returns the
// hooks it was started with.
func (s *OrchestratorSuite) startedHooks(lr *MockLiveRunner) agent.LiveHooks {
	var hooks agent.LiveHooks
	lr.On("LiveInfo", "ch-1").Return(agent.LiveInfo{}, false)
	lr.On("StartLive", mock.Anything, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		hooks = args.Get(2).(agent.LiveHooks)
	}).Return(nil).Once()
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1", Platform: types.PlatformLocal, SessionID: "sess-1"}, nil)
	s.runner.On("Run", mock.Anything, mock.Anything).Return(&agent.AgentResponse{}, nil).Once()
	_, err := s.orch.runChat(s.ctx, &agent.AgentRequest{ChannelID: "ch-1", Live: true})
	require.NoError(s.T(), err)
	require.NotNil(s.T(), hooks.BeginTurn)
	return hooks
}

func (s *OrchestratorSuite) TestLiveHooksActivityAndEnded() {
	lr, rec := s.withLive()
	hooks := s.startedHooks(lr)

	// Without an event broadcaster activity goes nowhere.
	hooks.Activity("model", "opus")
	hooks.BackgroundTasks(nil)

	ev := new(MockEventBroadcaster)
	s.orch.SetEventBroadcaster(ev)
	ev.On("BroadcastAgentActivity", "ch-1", events.AgentActivityEventData{Activity: "waiting", Description: "2 background tasks"}).Once()
	hooks.Activity("waiting", "2 background tasks")
	tasks := []agent.BackgroundTask{{ID: "b1", Type: "monitor", Description: "watch CI", StartedAt: time.Unix(10, 0)}}
	ev.On("BroadcastAgentActivity", "ch-1", events.AgentActivityEventData{
		Activity: "background_tasks", Description: "1 background task: watch CI", Tasks: tasks,
	}).Once()
	hooks.BackgroundTasks(tasks)
	ev.AssertExpectations(s.T())

	hooks.Ended("idle")
	got := rec.all()
	require.Equal(s.T(), livePublished{
		channelID: "ch-1", eventType: EventLiveStatus,
		data: LiveStatus{LiveInfo: agent.LiveInfo{ChannelID: "ch-1"}, Reason: "idle"},
	}, got[len(got)-1])
}

// A turn the session starts while a run holds the channel goes unstreamed;
// only its answer is posted.
func (s *OrchestratorSuite) TestBeginLiveTurnChannelBusy() {
	lr, _ := s.withLive()
	hooks := s.startedHooks(lr)

	lock := channelLock(&s.orch.channelLocks, "ch-1")
	lock.Lock()
	defer lock.Unlock()

	tests := []struct {
		name    string
		resp    *agent.AgentResponse
		err     error
		sendErr error
		posted  bool
	}{
		{name: "answer is posted", resp: &agent.AgentResponse{Response: "background done"}, posted: true},
		{name: "send failure still stores", resp: &agent.AgentResponse{Response: "background done"}, sendErr: errors.New("send failed"), posted: true},
		{name: "empty answer", resp: &agent.AgentResponse{}},
		{name: "failed turn", err: errors.New("boom")},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.bot.ExpectedCalls = nil
			s.bot.Calls = nil
			if tt.posted {
				s.bot.On("SendMessage", mock.Anything, &bot.OutgoingMessage{ChannelID: "ch-1", Content: "background done"}).Return(tt.sendErr).Once()
			}
			req, done := hooks.BeginTurn()
			require.Nil(s.T(), req)
			done(tt.resp, tt.err)
			s.bot.AssertExpectations(s.T())
			if !tt.posted {
				s.bot.AssertNotCalled(s.T(), "SendMessage", mock.Anything, mock.Anything)
			}
		})
	}
}

// A turn the session starts by itself while the channel is free runs as a
// run of its own: streamed, holding the channel until it ends, its answer
// delivered like a message's.
func (s *OrchestratorSuite) TestBeginLiveTurnRuns() {
	lr, _ := s.withLive()
	hooks := s.startedHooks(lr)
	ev := new(MockEventBroadcaster)
	s.orch.SetEventBroadcaster(ev)
	ev.On("BroadcastMessageCreated", "ch-1", mock.Anything).Maybe()
	ev.On("BroadcastAgentStatus", "ch-1", mock.MatchedBy(func(d events.AgentStatusEventData) bool {
		return d.Status == "running" && d.Trigger == "live"
	})).Once()
	ev.On("BroadcastAgentStatus", "ch-1", mock.MatchedBy(func(d events.AgentStatusEventData) bool {
		return d.Status == "completed" && d.Trigger == "live"
	})).Once()
	s.bot.On("SendMessage", mock.Anything, mock.MatchedBy(func(m *bot.OutgoingMessage) bool {
		return m.ChannelID == "ch-1" && m.Content == "working on it"
	})).Return(nil).Once()
	s.bot.On("SendMessage", mock.Anything, mock.MatchedBy(func(m *bot.OutgoingMessage) bool {
		return m.ChannelID == "ch-1" && m.Content == "all done"
	})).Return(nil).Once()
	s.store.On("UpdateSessionID", mock.Anything, "ch-1", "sess-2").Return(nil).Once()
	s.store.On("MarkMessagesProcessed", mock.Anything, mock.Anything).Return(nil).Once()

	req, done := hooks.BeginTurn()
	require.NotNil(s.T(), req)
	require.Equal(s.T(), "ch-1", req.ChannelID)
	require.Equal(s.T(), "sess-1", req.SessionID)
	require.False(s.T(), threadLockFree(&s.orch.channelLocks, "ch-1"), "the turn holds the channel")
	_, registered := s.orch.activeRuns.Load("ch-1")
	require.True(s.T(), registered, "the turn can be stopped")

	req.OnTurn("working on it", agent.TurnRef{})
	done(&agent.AgentResponse{Response: "all done", SessionID: "sess-2"}, nil)
	s.orch.drainWG.Wait()

	require.True(s.T(), threadLockFree(&s.orch.channelLocks, "ch-1"), "released once the turn ends")
	_, registered = s.orch.activeRuns.Load("ch-1")
	require.False(s.T(), registered)
	ev.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
	s.store.AssertExpectations(s.T())
}

// A failed turn is reported as a failed run of the channel.
func (s *OrchestratorSuite) TestBeginLiveTurnFails() {
	tests := []struct {
		name   string
		events bool
	}{
		{name: "with events", events: true},
		{name: "without events"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			s.withLive()
			hooks := s.orch.liveHooks("ch-1")
			// The channel can't be read when the turn starts.
			s.store.On("GetChannel", mock.Anything, "ch-1").Return(nil, errors.New("db error"))
			var ev *MockEventBroadcaster
			if tt.events {
				ev = new(MockEventBroadcaster)
				s.orch.SetEventBroadcaster(ev)
				ev.On("BroadcastMessageCreated", "ch-1", mock.Anything).Maybe()
				ev.On("BroadcastAgentStatus", "ch-1", mock.MatchedBy(func(d events.AgentStatusEventData) bool {
					return d.Status == "running" && d.Trigger == "live"
				})).Once()
				ev.On("BroadcastAgentStatus", "ch-1", mock.MatchedBy(func(d events.AgentStatusEventData) bool {
					return d.Status == "error" && d.Trigger == "live" && d.Error == "boom"
				})).Once()
			}
			s.bot.On("SendMessage", mock.Anything, mock.MatchedBy(func(m *bot.OutgoingMessage) bool {
				return m.ChannelID == "ch-1" && m.Content == runErrorNotice("boom")
			})).Return(nil).Once()

			req, done := hooks.BeginTurn()
			require.NotNil(s.T(), req)
			require.Empty(s.T(), req.SessionID)
			done(nil, errors.New("boom"))
			s.orch.drainWG.Wait()

			require.True(s.T(), threadLockFree(&s.orch.channelLocks, "ch-1"))
			s.bot.AssertExpectations(s.T())
			if ev != nil {
				ev.AssertExpectations(s.T())
			}
		})
	}
}

// Stopping a turn the session started interrupts it; the turn still ends
// with its outcome, reported as stopped.
func (s *OrchestratorSuite) TestBeginLiveTurnStopped() {
	tests := []struct {
		name         string
		interruptErr error
		outcomeErr   error
	}{
		{name: "interrupted", interruptErr: errors.New("not running")},
		{name: "turn reports its own error", outcomeErr: errors.New("aborted")},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			lr, _ := s.withLive()
			hooks := s.startedHooks(lr)
			interrupted := make(chan struct{})
			lr.On("InterruptLive", "ch-1").Run(func(mock.Arguments) { close(interrupted) }).Return(tt.interruptErr).Once()
			s.bot.On("SendMessage", mock.Anything, mock.MatchedBy(func(m *bot.OutgoingMessage) bool {
				return m.ChannelID == "ch-1" && m.Content == "Run stopped."
			})).Return(nil).Once()

			_, done := hooks.BeginTurn()
			cancel, ok := s.orch.activeRuns.Load("ch-1")
			require.True(s.T(), ok)
			cancel.(context.CancelFunc)()
			<-interrupted
			done(&agent.AgentResponse{Response: "partial"}, tt.outcomeErr)
			s.orch.drainWG.Wait()

			lr.AssertExpectations(s.T())
			s.bot.AssertExpectations(s.T())
			require.True(s.T(), threadLockFree(&s.orch.channelLocks, "ch-1"))
		})
	}
}

func (s *OrchestratorSuite) TestWarmLiveSession() {
	started, err := s.orch.WarmLiveSession(s.ctx, "ch-1", "user-1")
	require.ErrorIs(s.T(), err, ErrLiveUnavailable)
	require.False(s.T(), started)

	local := &db.Channel{ChannelID: "ch-1", Platform: types.PlatformLocal, SessionID: "sess-1"}
	tests := []struct {
		name     string
		running  bool
		busy     bool
		channel  *db.Channel
		chErr    error
		recent   error
		startErr error
		activity string
		started  bool
		wantErr  string
	}{
		{name: "already running", running: true},
		{name: "channel busy", busy: true},
		{name: "channel error", chErr: errors.New("db error"), wantErr: "db error"},
		{name: "channel gone"},
		{name: "hidden channel", channel: &db.Channel{ChannelID: "ch-1", Kind: db.ChannelKindLearn}},
		{name: "request error", channel: local, recent: errors.New("recent failed"), wantErr: "recent failed"},
		{name: "not live", channel: &db.Channel{ChannelID: "ch-1", Platform: types.PlatformDiscord}},
		{name: "start fails", channel: local, startErr: errors.New("spawn failed"), wantErr: "spawn failed"},
		// The turn being typed starts the session, and shows the build.
		{name: "image build", channel: local, activity: "image_build", startErr: context.Canceled},
		{name: "started", channel: local, activity: "image_ready", started: true},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			lr, rec := s.withLive()
			s.orch.cfg.Store(&config.Config{LiveSessions: true, LiveSessionPlatforms: []types.Platform{types.PlatformLocal}, ContainerTimeout: time.Minute})
			lr.On("LiveInfo", "ch-1").Return(agent.LiveInfo{}, tt.running).Once()
			if tt.busy {
				lock := channelLock(&s.orch.channelLocks, "ch-1")
				lock.Lock()
				defer lock.Unlock()
			}
			s.store.On("GetChannel", mock.Anything, "ch-1").Return(tt.channel, tt.chErr)
			s.store.On("GetRecentMessages", mock.Anything, "ch-1", mock.Anything).Return([]*db.Message{}, tt.recent)
			var startCtx context.Context
			lr.On("StartLive", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
				return req.Live && req.AuthorID == "user-1" && req.SessionID == "sess-1"
			}), mock.Anything).Run(func(args mock.Arguments) {
				startCtx = args.Get(0).(context.Context)
				if tt.activity != "" {
					args.Get(1).(*agent.AgentRequest).OnActivity(tt.activity, "")
				}
			}).Return(tt.startErr)
			lr.On("LiveInfo", "ch-1").Return(agent.LiveInfo{ChannelID: "ch-1", SessionID: "sess-1"}, true)

			started, err := s.orch.WarmLiveSession(s.ctx, "ch-1", "user-1")
			if startCtx != nil {
				require.Equal(s.T(), tt.activity == "image_build", startCtx.Err() != nil)
			}
			if tt.wantErr != "" {
				require.EqualError(s.T(), err, tt.wantErr)
			} else {
				require.NoError(s.T(), err)
			}
			require.Equal(s.T(), tt.started, started)
			if tt.started {
				require.Len(s.T(), rec.all(), 1)
				require.True(s.T(), rec.all()[0].data.(LiveStatus).Running)
			} else {
				require.Empty(s.T(), rec.all())
			}
			// The lock is released for the channel's next run.
			if !tt.busy {
				require.True(s.T(), channelLock(&s.orch.channelLocks, "ch-1").TryLock())
				channelLock(&s.orch.channelLocks, "ch-1").Unlock()
			}
		})
	}
}
