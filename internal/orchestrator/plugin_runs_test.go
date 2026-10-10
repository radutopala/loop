package orchestrator

import (
	"encoding/json"
	"errors"
	"strings"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/bot"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/plugin"
	"github.com/radutopala/loop/internal/types"
)

func (s *OrchestratorSuite) TestStartPluginRun() {
	parent := &db.Channel{ChannelID: "ch1", GuildID: "g1", Name: "api", DirPath: "/project", Platform: types.PlatformLocal}
	thread := &db.Channel{ChannelID: "plugin-1", GuildID: "g1", Name: "plugin:echo: api", Kind: db.PluginKind("echo")}
	run := PluginRun{PluginID: "echo", Agent: "say", ReadOnly: true, Prompt: "say hi"}

	s.Run("thread fails", func() {
		s.SetupTest()
		s.store.On("GetHiddenThread", s.ctx, "ch1", db.PluginKind("echo")).Return(nil, errors.New("db down"))
		_, _, err := s.orch.StartPluginRun(s.ctx, parent, run)
		require.EqualError(s.T(), err, "db down")
	})
	s.Run("queues the run", func() {
		s.SetupTest()
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
	})
}

// TestPluginThreadRuns checks what runs in a plugin's hidden thread: only
// the runs StartPluginRun queued there, each shaped as the plugin's agent
// in a fresh session, with the plugin's tools.
func (s *OrchestratorSuite) TestPluginThreadRuns() {
	thread := &db.Channel{ChannelID: "plugin-1", ParentID: "ch1", DirPath: "/project", Kind: db.PluginKind("echo"), SessionID: "old"}
	parent := &db.Channel{ChannelID: "ch1", Name: "api", DirPath: "/project", ModelOverride: "opus", EffortOverride: "high"}
	tools := []plugin.Tool{{Name: "echo", Description: "Echoes", InputSchema: json.RawMessage(`{"type":"object"}`)}}
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
			name:    "tools that don't encode",
			run:     &PluginRun{PluginID: "echo", threadID: "plugin-1", Tools: []plugin.Tool{{Name: "bad", InputSchema: json.RawMessage(`{`)}}},
			msg:     bot.IncomingMessage{MessageID: "run-1", AuthorID: pluginAuthorID},
			wantErr: "encoding the plugin's tools: ",
		},
		{
			name: "the channel's model",
			run:  &PluginRun{PluginID: "echo", Agent: "say", ReadOnly: true, Tools: tools, threadID: "plugin-1"},
			msg:  bot.IncomingMessage{MessageID: "run-1", AuthorID: pluginAuthorID},
			check: func(req *agent.AgentRequest) {
				require.Equal(s.T(), "plugin-echo-say", req.AgentID)
				require.True(s.T(), req.ReadOnly)
				require.Empty(s.T(), req.SessionID)
				require.False(s.T(), req.ForkSession)
				require.Equal(s.T(), "opus", req.Model)
				require.Equal(s.T(), "high", req.Effort)
				require.Contains(s.T(), req.SystemPrompt, `plugin "echo"`)
				require.Equal(s.T(), []string{"--plugin-id", "echo", "--plugin-tools", `[{"name":"echo","description":"Echoes","input_schema":{"type":"object"}}]`}, req.MCPArgs)
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
			s.orch.pluginRunDone(&bot.IncomingMessage{ChannelID: "plugin-1", MessageID: "run-1", AuthorID: "user-1"})
			s.orch.pluginRunDone(&bot.IncomingMessage{ChannelID: "plugin-1", MessageID: "run-2", AuthorID: pluginAuthorID})
			_, ok = s.orch.ActivePluginRun("plugin-1")
			require.True(s.T(), ok)
			s.orch.pluginRunDone(&bot.IncomingMessage{ChannelID: "plugin-1", MessageID: "run-1", AuthorID: pluginAuthorID})
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
