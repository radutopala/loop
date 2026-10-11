package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/bot"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/plugin"
	"github.com/radutopala/loop/internal/plugin/proxy"
	"github.com/radutopala/loop/internal/plugin/runtime"
	"github.com/radutopala/loop/internal/types"
)

func (s *OrchestratorSuite) TestStartPluginRun() {
	parent := &db.Channel{ChannelID: "ch1", GuildID: "g1", Name: "api", DirPath: "/project", Platform: types.PlatformLocal}
	thread := &db.Channel{ChannelID: "plugin-1", GuildID: "g1", Name: "plugin:echo: api", Kind: db.PluginKind("echo")}
	tools := []plugin.Tool{{Name: "echo", Description: "Echoes", InputSchema: json.RawMessage(`{"type":"object"}`)}}
	run := PluginRun{PluginID: "echo", Agent: "say", ReadOnly: true, Prompt: "say hi", Model: "sonnet", Effort: "low", Tools: tools}
	wantTools := `[{"name":"echo","description":"Echoes","input_schema":{"type":"object"}}]`

	s.Run("thread fails", func() {
		s.SetupTest()
		s.store.On("GetHiddenThread", s.ctx, "ch1", db.PluginKind("echo")).Return(nil, errors.New("db down"))
		_, _, err := s.orch.StartPluginRun(s.ctx, parent, run)
		require.EqualError(s.T(), err, "db down")
	})
	s.Run("tools that don't encode", func() {
		s.SetupTest()
		s.store.On("GetHiddenThread", s.ctx, "ch1", db.PluginKind("echo")).Return(thread, nil)
		bad := run
		bad.Tools = []plugin.Tool{{Name: "bad", InputSchema: json.RawMessage(`{`)}}
		_, _, err := s.orch.StartPluginRun(s.ctx, parent, bad)
		require.ErrorContains(s.T(), err, "encoding the plugin's tools: ")
	})
	s.Run("store fails", func() {
		s.SetupTest()
		rs := new(mockPluginRunStore)
		rs.On("InsertPluginRun", s.ctx, mock.Anything).Return(errors.New("disk full"))
		s.orch.SetPluginRunStore(rs)
		s.store.On("GetHiddenThread", s.ctx, "ch1", db.PluginKind("echo")).Return(thread, nil)
		_, _, err := s.orch.StartPluginRun(s.ctx, parent, run)
		require.EqualError(s.T(), err, "storing the run: disk full")
		s.store.AssertNotCalled(s.T(), "InsertMessage", mock.Anything, mock.Anything)
	})
	for _, stored := range []bool{false, true} {
		s.Run(fmt.Sprintf("queues the run, stored %v", stored), func() {
			s.SetupTest()
			rs := new(mockPluginRunStore)
			var kept *db.PluginRun
			if stored {
				rs.On("InsertPluginRun", s.ctx, mock.MatchedBy(func(r *db.PluginRun) bool {
					kept = r
					return true
				})).Return(nil).Once()
				s.orch.SetPluginRunStore(rs)
			}
			var drains int
			s.orch.drainSpawn = func(func()) { drains++ }
			eb := new(MockEventBroadcaster)
			eb.On("BroadcastMessageCreated", mock.Anything, mock.Anything).Return()
			s.orch.SetEventBroadcaster(eb)
			s.store.On("GetHiddenThread", s.ctx, "ch1", db.PluginKind("echo")).Return(thread, nil)
			s.store.On("IsChannelActive", s.ctx, "plugin-1").Return(true, nil)
			s.store.On("GetChannel", s.ctx, "plugin-1").Return(&db.Channel{ID: 9, ChannelID: "plugin-1", Kind: db.PluginKind("echo")}, nil)
			var trigger *db.Message
			s.store.On("InsertMessage", s.ctx, mock.MatchedBy(func(m *db.Message) bool {
				trigger = m
				return !m.IsBot
			})).Return(nil)

			runID, threadID, err := s.orch.StartPluginRun(s.ctx, parent, run)

			require.NoError(s.T(), err)
			require.Equal(s.T(), "plugin-1", threadID)
			require.True(s.T(), strings.HasPrefix(runID, "ask-"))
			require.NotNil(s.T(), trigger)
			require.Equal(s.T(), runID, trigger.MsgID)
			require.Equal(s.T(), pluginAuthorID, trigger.AuthorID)
			require.Equal(s.T(), "say hi", trigger.Content)
			require.Equal(s.T(), 1, drains)
			v, ok := s.orch.pluginRuns.Load(runID)
			require.True(s.T(), ok)
			require.Equal(s.T(), "plugin-1", v.(*PluginRun).threadID)
			require.Equal(s.T(), parent, v.(*PluginRun).parent)
			require.Equal(s.T(), wantTools, v.(*PluginRun).tools)
			rs.AssertExpectations(s.T())
			if stored {
				require.Equal(s.T(), &db.PluginRun{
					RunID: runID, PluginID: "echo", Agent: "say", ReadOnly: true, Prompt: "say hi", Model: "sonnet", Effort: "low",
					Tools: wantTools, ThreadID: "plugin-1", ChannelID: "ch1",
				}, kept)
			}
		})
	}
}

// TestPluginThreadRuns checks what runs in a plugin's hidden thread: only
// the runs StartPluginRun queued there, each shaped as the plugin's agent
// in a fresh session, with the plugin's tools.
func (s *OrchestratorSuite) TestPluginThreadRuns() {
	thread := &db.Channel{ChannelID: "plugin-1", ParentID: "ch1", DirPath: "/project", Kind: db.PluginKind("echo"), SessionID: "old"}
	parent := &db.Channel{ChannelID: "ch1", Name: "api", DirPath: "/project", ModelOverride: "opus", EffortOverride: "high"}
	tools := `[{"name":"echo","description":"Echoes","input_schema":{"type":"object"}}]`
	tests := []struct {
		name    string
		run     *PluginRun // registered under run-1
		msg     bot.IncomingMessage
		wantErr string
		check   func(req *agent.AgentRequest)
	}{
		{
			name:    "a user's message",
			run:     &PluginRun{PluginID: "echo", threadID: "plugin-1"},
			msg:     bot.IncomingMessage{MessageID: "run-1", AuthorID: "user-1"},
			wantErr: "plugin thread plugin-1: only a plugin's own agent runs run here",
		},
		{
			name:    "no such run",
			msg:     bot.IncomingMessage{MessageID: "run-1", AuthorID: pluginAuthorID},
			wantErr: "plugin thread plugin-1: only a plugin's own agent runs run here",
		},
		{
			name:    "a run queued in another thread",
			run:     &PluginRun{PluginID: "echo", threadID: "plugin-2"},
			msg:     bot.IncomingMessage{MessageID: "run-1", AuthorID: pluginAuthorID},
			wantErr: "plugin thread plugin-1: only a plugin's own agent runs run here",
		},
		{
			name: "the channel's model",
			run:  &PluginRun{PluginID: "echo", Agent: "say", ReadOnly: true, tools: tools, threadID: "plugin-1"},
			msg:  bot.IncomingMessage{MessageID: "run-1", AuthorID: pluginAuthorID},
			check: func(req *agent.AgentRequest) {
				require.Equal(s.T(), "plugin-echo-say", req.AgentID)
				require.True(s.T(), req.ReadOnly)
				require.Empty(s.T(), req.SessionID)
				require.False(s.T(), req.ForkSession)
				require.Equal(s.T(), "opus", req.Model)
				require.Equal(s.T(), "high", req.Effort)
				require.Contains(s.T(), req.SystemPrompt, `plugin "echo"`)
				require.Equal(s.T(), []string{"--plugin-id", "echo", "--plugin-tools", tools}, req.MCPArgs)
			},
		},
		{
			name: "the run's own model",
			run:  &PluginRun{PluginID: "echo", Agent: "writer", Model: "sonnet", Effort: "low", threadID: "plugin-1"},
			msg:  bot.IncomingMessage{MessageID: "run-1", AuthorID: pluginAuthorID},
			check: func(req *agent.AgentRequest) {
				require.Equal(s.T(), "plugin-echo-writer", req.AgentID)
				require.False(s.T(), req.ReadOnly)
				require.Equal(s.T(), "sonnet", req.Model)
				require.Equal(s.T(), "low", req.Effort)
			},
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			if tc.run != nil {
				s.orch.pluginRuns.Store("run-1", tc.run)
			}
			s.store.On("GetRecentMessages", s.ctx, "plugin-1", recentMessageLimit).Return([]*db.Message{}, nil)
			s.store.On("GetChannel", s.ctx, "plugin-1").Return(thread, nil)
			s.store.On("GetChannel", s.ctx, "ch1").Return(parent, nil)
			msg := tc.msg
			msg.ChannelID, msg.Content = "plugin-1", "say hi"

			req, _, _, err := s.orch.prepareAgentRequest(s.ctx, &msg, "")

			if tc.wantErr != "" {
				require.ErrorContains(s.T(), err, tc.wantErr)
				_, ok := s.orch.ActivePluginRun("plugin-1")
				require.False(s.T(), ok)
				return
			}
			require.NoError(s.T(), err)
			tc.check(req)
			active, ok := s.orch.ActivePluginRun("plugin-1")
			require.True(s.T(), ok)
			require.Equal(s.T(), "run-1", active.ID)
			require.Equal(s.T(), tc.run.Agent, active.Run.Agent)

			// Only the run's own end forgets it.
			s.orch.pluginRunDone(s.ctx, &bot.IncomingMessage{ChannelID: "plugin-1", MessageID: "run-1", AuthorID: "user-1"}, nil)
			s.orch.pluginRunDone(s.ctx, &bot.IncomingMessage{ChannelID: "plugin-1", MessageID: "run-2", AuthorID: pluginAuthorID}, nil)
			_, ok = s.orch.ActivePluginRun("plugin-1")
			require.True(s.T(), ok)
			s.orch.pluginRunDone(s.ctx, &bot.IncomingMessage{ChannelID: "plugin-1", MessageID: "run-1", AuthorID: pluginAuthorID}, nil)
			_, ok = s.orch.ActivePluginRun("plugin-1")
			require.False(s.T(), ok)
			_, ok = s.orch.pluginRuns.Load("run-1")
			require.False(s.T(), ok)
		})
	}
}

// TestHandleMessagePluginTriggerNeverAutoCreates covers a plugin run's
// trigger whose thread was deleted meanwhile: it's dropped, and the run
// forgotten, rather than auto-creating a plain channel where the run would
// be unrestricted.
func (s *OrchestratorSuite) TestHandleMessagePluginTriggerNeverAutoCreates() {
	s.store.On("IsChannelActive", s.ctx, "plugin-1").Return(false, nil)
	s.bot.On("GetChannelParentID", s.ctx, "plugin-1").Return("", nil)
	s.orch.pluginRuns.Store("run-1", &PluginRun{PluginID: "echo", threadID: "plugin-1"})

	s.orch.HandleMessage(s.ctx, &bot.IncomingMessage{
		ChannelID: "plugin-1",
		MessageID: "run-1",
		AuthorID:  pluginAuthorID,
		Content:   "say hi",
		HasPrefix: true,
		Platform:  types.PlatformLocal,
	})

	s.store.AssertNotCalled(s.T(), "UpsertChannel", mock.Anything, mock.Anything)
	_, ok := s.orch.pluginRuns.Load("run-1")
	require.False(s.T(), ok)
}

type mockPluginRunNotifier struct {
	mock.Mock
	called chan struct{}
}

func (m *mockPluginRunNotifier) RunDone(ctx context.Context, pluginID string, ch runtime.Channel, runID string, req proxy.RunDoneRequest) error {
	defer close(m.called)
	return m.Called(ctx, pluginID, ch, runID, req).Error(0)
}

// TestPluginRunDoneNotifies covers the plugin being told its run ended,
// in the background, with how it ended.
func (s *OrchestratorSuite) TestPluginRunDoneNotifies() {
	parent := &db.Channel{ChannelID: "ch1", DirPath: "/project"}
	ch := runtime.Channel{ID: "ch1", DirPath: "/project"}
	tests := []struct {
		name    string
		runErr  error
		want    proxy.RunDoneRequest
		callErr error
	}{
		{name: "done", want: proxy.RunDoneRequest{Status: proxy.RunStatusDone}},
		{name: "failed", runErr: errors.New("agent crashed"), want: proxy.RunDoneRequest{Status: proxy.RunStatusFailed, Error: "agent crashed"}},
		{name: "plugin unreachable", want: proxy.RunDoneRequest{Status: proxy.RunStatusDone}, callErr: errors.New("refused")},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			n := &mockPluginRunNotifier{called: make(chan struct{})}
			n.On("RunDone", mock.Anything, "echo", ch, "run-1", tc.want).Return(tc.callErr).Once()
			s.orch.SetPluginRunNotifier(n)
			s.orch.pluginRuns.Store("run-1", &PluginRun{PluginID: "echo", threadID: "plugin-1", parent: parent})
			s.orch.pluginRunDone(s.ctx, &bot.IncomingMessage{ChannelID: "plugin-1", MessageID: "run-1", AuthorID: pluginAuthorID}, tc.runErr)
			<-n.called
			n.AssertExpectations(s.T())
		})
	}

	s.Run("run unknown or no notifier", func() {
		s.SetupTest()
		n := &mockPluginRunNotifier{called: make(chan struct{})}
		s.orch.SetPluginRunNotifier(n)
		s.orch.pluginRunDone(s.ctx, &bot.IncomingMessage{ChannelID: "plugin-1", MessageID: "run-9", AuthorID: pluginAuthorID}, nil)
		s.orch.SetPluginRunNotifier(nil)
		s.orch.pluginRuns.Store("run-1", &PluginRun{PluginID: "echo", parent: parent})
		s.orch.pluginRunDone(s.ctx, &bot.IncomingMessage{ChannelID: "plugin-1", MessageID: "run-1", AuthorID: pluginAuthorID}, nil)
		n.AssertNotCalled(s.T(), "RunDone", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})
}

type mockPluginRunStore struct {
	mock.Mock
}

func (m *mockPluginRunStore) InsertPluginRun(ctx context.Context, r *db.PluginRun) error {
	return m.Called(ctx, r).Error(0)
}

func (m *mockPluginRunStore) DeletePluginRun(ctx context.Context, runID string) error {
	return m.Called(ctx, runID).Error(0)
}

func (m *mockPluginRunStore) ListPluginRuns(ctx context.Context) ([]*db.PluginRun, error) {
	args := m.Called(ctx)
	runs, _ := args.Get(0).([]*db.PluginRun)
	return runs, args.Error(1)
}

// TestPluginRunDoneDropsTheStoredRun covers a run's end dropping its row,
// even when that fails or its channel is gone and no plugin is told.
func (s *OrchestratorSuite) TestPluginRunDoneDropsTheStoredRun() {
	for _, deleteErr := range []error{nil, errors.New("db down")} {
		s.Run(fmt.Sprintf("delete error %v", deleteErr), func() {
			s.SetupTest()
			rs := new(mockPluginRunStore)
			rs.On("DeletePluginRun", s.ctx, "run-1").Return(deleteErr).Once()
			s.orch.SetPluginRunStore(rs)
			n := &mockPluginRunNotifier{called: make(chan struct{})}
			s.orch.SetPluginRunNotifier(n)
			s.orch.pluginRuns.Store("run-1", &PluginRun{PluginID: "echo", threadID: "plugin-1"})
			s.orch.pluginRunDone(s.ctx, &bot.IncomingMessage{ChannelID: "plugin-1", MessageID: "run-1", AuthorID: pluginAuthorID}, nil)
			rs.AssertExpectations(s.T())
			n.AssertNotCalled(s.T(), "RunDone", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
		})
	}
}

// TestPluginRunCutShortByShutdown covers a run that ends once Stop has
// begun: it stays stored, for the next start to fail and tell the plugin.
func (s *OrchestratorSuite) TestPluginRunCutShortByShutdown() {
	rs := new(mockPluginRunStore)
	s.orch.SetPluginRunStore(rs)
	n := &mockPluginRunNotifier{called: make(chan struct{})}
	s.orch.SetPluginRunNotifier(n)
	s.orch.pluginRuns.Store("run-1", &PluginRun{PluginID: "echo", threadID: "plugin-1", parent: &db.Channel{ChannelID: "ch1"}})
	s.orch.delayStopOnce.Do(func() { close(s.orch.delayStop) })

	s.orch.pluginRunDone(s.ctx, &bot.IncomingMessage{ChannelID: "plugin-1", MessageID: "run-1", AuthorID: pluginAuthorID}, errors.New("killed"))

	_, ok := s.orch.pluginRuns.Load("run-1")
	require.False(s.T(), ok)
	rs.AssertNotCalled(s.T(), "DeletePluginRun", mock.Anything, mock.Anything)
	n.AssertNotCalled(s.T(), "RunDone", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

// TestRecoverPluginRuns covers startup picking up the runs the prior
// daemon run left: those still queued resume, the rest fail and their
// plugin is told.
func (s *OrchestratorSuite) TestRecoverPluginRuns() {
	parent := &db.Channel{ChannelID: "ch1", DirPath: "/project"}
	stored := func(runID string, queued bool, channelID string) *db.PluginRun {
		return &db.PluginRun{
			RunID: runID, PluginID: "echo", Agent: "say", ReadOnly: true, Prompt: "say hi", Model: "sonnet", Effort: "low",
			Tools: `[{"name":"echo","description":"","input_schema":null}]`, ThreadID: "plugin-1", ChannelID: channelID, Queued: queued,
		}
	}

	s.Run("no store", func() {
		s.SetupTest()
		s.orch.RecoverPluginRuns(s.ctx)
	})
	s.Run("list fails", func() {
		s.SetupTest()
		rs := new(mockPluginRunStore)
		rs.On("ListPluginRuns", s.ctx).Return(nil, errors.New("db down")).Once()
		s.orch.SetPluginRunStore(rs)
		s.orch.RecoverPluginRuns(s.ctx)
		rs.AssertExpectations(s.T())
	})
	s.Run("recovers", func() {
		s.SetupTest()
		rs := new(mockPluginRunStore)
		rs.On("ListPluginRuns", s.ctx).Return([]*db.PluginRun{
			stored("queued", true, "ch1"),
			stored("lookup-fails", true, "ch2"),
			stored("channel-gone", true, "ch3"),
			stored("interrupted", false, "ch1"),
		}, nil).Once()
		rs.On("DeletePluginRun", s.ctx, "channel-gone").Return(nil).Once()
		rs.On("DeletePluginRun", s.ctx, "interrupted").Return(nil).Once()
		s.orch.SetPluginRunStore(rs)
		n := &mockPluginRunNotifier{called: make(chan struct{})}
		n.On("RunDone", mock.Anything, "echo", runtime.Channel{ID: "ch1", DirPath: "/project"}, "interrupted",
			proxy.RunDoneRequest{Status: proxy.RunStatusFailed, Error: "loop stopped before the run ended"}).Return(nil).Once()
		s.orch.SetPluginRunNotifier(n)
		s.store.On("GetChannel", s.ctx, "ch1").Return(parent, nil)
		s.store.On("GetChannel", s.ctx, "ch2").Return(nil, errors.New("db down"))
		s.store.On("GetChannel", s.ctx, "ch3").Return(nil, nil)

		s.orch.RecoverPluginRuns(s.ctx)

		<-n.called
		n.AssertExpectations(s.T())
		rs.AssertExpectations(s.T())
		v, ok := s.orch.pluginRuns.Load("queued")
		require.True(s.T(), ok, "a queued run resumes")
		require.Equal(s.T(), &PluginRun{
			PluginID: "echo", Agent: "say", ReadOnly: true, Prompt: "say hi", Model: "sonnet", Effort: "low",
			Tools: []plugin.Tool{{Name: "echo", InputSchema: json.RawMessage("null")}},
			tools: `[{"name":"echo","description":"","input_schema":null}]`, threadID: "plugin-1", parent: parent,
		}, v)
		require.NoError(s.T(), s.orch.admitPluginRun("plugin-1", &bot.IncomingMessage{MessageID: "queued", AuthorID: pluginAuthorID}))
		for _, id := range []string{"lookup-fails", "channel-gone", "interrupted"} {
			_, ok := s.orch.pluginRuns.Load(id)
			require.False(s.T(), ok, id)
		}
	})
}
