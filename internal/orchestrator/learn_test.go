package orchestrator

import (
	"context"
	"errors"
	"strings"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/bot"
	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/events"
	"github.com/radutopala/loop/internal/learn"
	"github.com/radutopala/loop/internal/types"
)

func (s *OrchestratorSuite) TestLearnSkipReason() {
	on := config.LearnConfig{Enabled: true, MinTurns: 3}
	tests := []struct {
		name   string
		ch     *db.Channel
		turns  int
		cfg    config.LearnConfig
		parked bool
		want   string
	}{
		{"learns", &db.Channel{}, 3, on, false, ""},
		{"override on beats config off", &db.Channel{LearnOverride: db.LearnOn}, 5, config.LearnConfig{MinTurns: 3}, false, ""},
		{"learn thread", &db.Channel{Kind: db.ChannelKindLearn}, 5, on, false, "learn thread"},
		{"task thread", &db.Channel{TaskID: 7}, 5, on, false, "task thread"},
		{"parked", &db.Channel{}, 5, on, true, "parked on a plan or question"},
		{"override off", &db.Channel{LearnOverride: db.LearnOff}, 5, on, false, "learn off"},
		{"config off", &db.Channel{}, 5, config.LearnConfig{MinTurns: 3}, false, "learn off"},
		{"too short", &db.Channel{}, 2, on, false, "2 turns, below min_turns 3"},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			got := learnSkipReason(tc.ch, &agent.AgentResponse{NumTurns: tc.turns}, tc.cfg, tc.parked)
			require.Equal(s.T(), tc.want, got)
		})
	}
}

func (s *OrchestratorSuite) TestLearnConfigFor() {
	global := &config.Config{Learn: config.LearnConfig{MinTurns: 3}}
	project := &config.Config{Learn: config.LearnConfig{Enabled: true, MinTurns: 1}}
	tests := []struct {
		name    string
		dir     string
		loadErr error
		want    *config.Config
	}{
		{"no dir uses global", "", nil, global},
		{"project merged", "/project", nil, project},
		{"load error falls back to global", "/project", errors.New("bad hjson"), global},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.orch.cfg.Store(global)
			s.orch.loadProjectConfig = func(dir string, main *config.Config) (*config.Config, error) {
				require.Equal(s.T(), "/project", dir)
				require.Same(s.T(), global, main)
				return project, tc.loadErr
			}
			require.Same(s.T(), tc.want, s.orch.learnConfigFor(tc.dir))
		})
	}
}

// setupLearnOn turns learning on in the global config and makes project
// config loads return it unchanged.
func (s *OrchestratorSuite) setupLearnOn() {
	s.orch.cfg.Store(&config.Config{Learn: config.LearnConfig{Enabled: true, MinTurns: 2}})
	s.orch.loadProjectConfig = func(_ string, main *config.Config) (*config.Config, error) {
		return main, nil
	}
}

func (s *OrchestratorSuite) TestMaybeLearnSkips() {
	learnOn := &agent.AgentResponse{SessionID: "sess-1", NumTurns: 5}
	tests := []struct {
		name  string
		ch    *db.Channel
		resp  *agent.AgentResponse
		setup func()
	}{
		{"learn thread", &db.Channel{ChannelID: "learn-1", Kind: db.ChannelKindLearn}, learnOn, func() {}},
		{"too short", &db.Channel{ChannelID: "ch1", DirPath: "/project"}, &agent.AgentResponse{SessionID: "sess-1", NumTurns: 1}, func() {}},
		{"no session", &db.Channel{ChannelID: "ch1", DirPath: "/project"}, &agent.AgentResponse{NumTurns: 5}, func() {}},
		{"lookup error", &db.Channel{ChannelID: "ch1", DirPath: "/project"}, learnOn, func() {
			s.store.On("GetLearnChannel", s.ctx, "ch1").Return(nil, errors.New("db down"))
		}},
		{"create error", &db.Channel{ChannelID: "ch1", DirPath: "/project"}, learnOn, func() {
			s.store.On("GetLearnChannel", s.ctx, "ch1").Return(nil, nil)
			s.store.On("InsertLearnChannel", s.ctx, mock.Anything).Return(errors.New("db down"))
		}},
		{"learn pass running", &db.Channel{ChannelID: "ch1", DirPath: "/project"}, learnOn, func() {
			s.store.On("GetLearnChannel", s.ctx, "ch1").Return(&db.Channel{ChannelID: "learn-1"}, nil)
			s.orch.activeRuns.Store("learn-1", context.CancelFunc(func() {}))
		}},
		{"fork error", &db.Channel{ChannelID: "ch1", DirPath: "/project"}, learnOn, func() {
			s.store.On("GetLearnChannel", s.ctx, "ch1").Return(&db.Channel{ChannelID: "learn-1"}, nil)
			s.store.On("MarkSessionForkPending", s.ctx, "learn-1", "sess-1").Return(errors.New("db down"))
		}},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.setupLearnOn()
			tc.setup()
			s.orch.maybeLearn(s.ctx, tc.ch, &bot.IncomingMessage{Content: "hi"}, tc.resp)
			s.store.AssertExpectations(s.T())
			s.store.AssertNotCalled(s.T(), "IsChannelActive", mock.Anything, mock.Anything)
		})
	}
}

// TestMaybeLearnStarts covers a first learn pass: the learn thread is created
// under the channel, forked from the run's session and handed the trigger
// message, which wakes its queue.
func (s *OrchestratorSuite) TestMaybeLearnStarts() {
	s.setupLearnOn()
	var drains int
	s.orch.drainSpawn = func(func()) { drains++ }
	eb := new(MockEventBroadcaster)
	eb.On("BroadcastMessageCreated", mock.Anything, mock.Anything).Return()
	eb.On("BroadcastLearnStarted", "ch1", mock.Anything).Return()
	s.orch.SetEventBroadcaster(eb)

	ch := &db.Channel{ChannelID: "ch1", GuildID: "g1", Name: "api", DirPath: "/project"}
	var created *db.Channel
	s.store.On("GetLearnChannel", s.ctx, "ch1").Return(nil, nil)
	s.store.On("InsertLearnChannel", s.ctx, mock.MatchedBy(func(l *db.Channel) bool {
		created = l
		return true
	})).Return(nil)
	s.store.On("MarkSessionForkPending", s.ctx, mock.MatchedBy(func(id string) bool {
		return strings.HasPrefix(id, "learn-")
	}), "sess-1").Return(nil)
	s.store.On("IsChannelActive", s.ctx, mock.Anything).Return(true, nil)
	s.store.On("GetChannel", s.ctx, mock.Anything).Return(&db.Channel{ID: 9, Platform: types.PlatformLocal, Kind: db.ChannelKindLearn}, nil)
	var trigger *db.Message
	s.store.On("InsertMessage", s.ctx, mock.MatchedBy(func(m *db.Message) bool {
		trigger = m
		return !m.IsBot
	})).Return(nil)

	s.orch.maybeLearn(s.ctx, ch, &bot.IncomingMessage{Content: "fix the tests"}, &agent.AgentResponse{SessionID: "sess-1", NumTurns: 2})

	require.NotNil(s.T(), created)
	require.Equal(s.T(), "ch1", created.ParentID)
	require.Equal(s.T(), "g1", created.GuildID)
	require.Equal(s.T(), "/project", created.DirPath)
	require.Equal(s.T(), "learn: api", created.Name)
	require.Equal(s.T(), types.PlatformLocal, created.Platform)
	require.Equal(s.T(), db.ChannelKindLearn, created.Kind)

	require.NotNil(s.T(), trigger)
	require.Equal(s.T(), created.ChannelID, trigger.ChannelID)
	require.True(s.T(), trigger.IsTriggered)
	require.Equal(s.T(), learnAuthorID, trigger.AuthorID)
	require.Equal(s.T(), learnAuthorName, trigger.AuthorName)
	require.Equal(s.T(), learn.TriggerMessage("api", "fix the tests"), trigger.Content)
	require.Equal(s.T(), 1, drains)
	s.store.AssertExpectations(s.T())
	eb.AssertCalled(s.T(), "BroadcastMessageCreated", created.ChannelID, mock.MatchedBy(func(d events.MessageEventData) bool {
		return d.Content == trigger.Content
	}))
	eb.AssertCalled(s.T(), "BroadcastLearnStarted", "ch1", created.ChannelID)
}

func (s *OrchestratorSuite) TestRunTrigger() {
	s.bot.ExpectedCalls = nil
	s.bot.On("IsBotUser", "bot-1").Return(true)
	s.bot.On("IsBotUser", mock.Anything).Return(false)
	tests := []struct {
		name     string
		authorID string
		want     string
	}{
		{name: "learn pass", authorID: learnAuthorID, want: "learn"},
		{name: "bot", authorID: "bot-1", want: "bot"},
		{name: "user", authorID: "user-1", want: ""},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			require.Equal(s.T(), tc.want, s.orch.runTrigger(tc.authorID))
		})
	}
}

// TestMaybeLearnWorktreeUsesRootConfig checks a worktree thread's learn
// config comes from the root checkout, not the worktree.
func (s *OrchestratorSuite) TestMaybeLearnWorktreeUsesRootConfig() {
	s.orch.cfg.Store(&config.Config{})
	var loaded []string
	s.orch.loadProjectConfig = func(dir string, main *config.Config) (*config.Config, error) {
		loaded = append(loaded, dir)
		return main, nil
	}
	s.store.On("GetChannel", s.ctx, "root").Return(&db.Channel{ChannelID: "root", DirPath: "/project"}, nil)

	wt := &db.Channel{ChannelID: "wt", ParentID: "root", Worktree: true, DirPath: "/project/.worktrees/wt"}
	s.orch.maybeLearn(s.ctx, wt, &bot.IncomingMessage{}, &agent.AgentResponse{SessionID: "sess-1", NumTurns: 9})

	require.Equal(s.T(), []string{"/project"}, loaded)
	s.store.AssertNotCalled(s.T(), "GetLearnChannel", mock.Anything, mock.Anything)
}

// TestPrepareAgentRequestLearnThread checks a learn thread's run: it forks
// the parent's session, runs as the learn agent with learn mode's tool
// denials, and its prompt carries the parent's state.
func (s *OrchestratorSuite) TestPrepareAgentRequestLearnThread() {
	tests := []struct {
		name       string
		learnCfg   config.LearnConfig
		tasksErr   error
		wantModel  string
		wantEffort string
		wantTask   bool
	}{
		{"learn model and effort", config.LearnConfig{Model: "opus", Effort: "high"}, nil, "opus", "high", true},
		{"falls back to parent overrides", config.LearnConfig{}, nil, "sonnet", "low", true},
		{"task listing error still runs", config.LearnConfig{}, errors.New("db down"), "sonnet", "low", false},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.orch.cfg.Store(&config.Config{Learn: tc.learnCfg})
			s.orch.loadProjectConfig = func(_ string, main *config.Config) (*config.Config, error) {
				return main, nil
			}
			parent := &db.Channel{
				ChannelID: "ch1", Name: "api", Description: "the API", TicketURL: "https://tracker.example.com/T-1", DirPath: "/project",
				SessionID: "sess-parent", ModelOverride: "sonnet", EffortOverride: "low",
			}
			s.store.On("GetRecentMessages", s.ctx, "learn-1", recentMessageLimit).Return([]*db.Message{}, nil)
			s.store.On("GetChannel", s.ctx, "learn-1").Return(&db.Channel{
				ChannelID: "learn-1", ParentID: "ch1", DirPath: "/project", Kind: db.ChannelKindLearn,
				SessionID: "sess-parent", ForkPending: true,
			}, nil)
			s.store.On("GetChannel", s.ctx, "ch1").Return(parent, nil)
			var tasks []*db.ScheduledTask
			if tc.tasksErr == nil {
				tasks = []*db.ScheduledTask{{Type: db.TaskTypeCron, Schedule: "0 9 * * *", Prompt: "nightly deps"}}
			}
			s.store.On("ListScheduledTasks", s.ctx, "ch1").Return(tasks, tc.tasksErr)

			req, _, _, err := s.orch.prepareAgentRequest(s.ctx, &bot.IncomingMessage{ChannelID: "learn-1", AuthorName: learnAuthorName, Content: "review"})
			require.NoError(s.T(), err)
			require.True(s.T(), req.ForkSession)
			require.Equal(s.T(), "sess-parent", req.SessionID)
			require.Equal(s.T(), learn.AgentID, req.AgentID)
			require.True(s.T(), req.LearnMode)
			require.Equal(s.T(), tc.wantModel, req.Model)
			require.Equal(s.T(), tc.wantEffort, req.Effort)
			require.Contains(s.T(), req.SystemPrompt, `- Channel: "api"`)
			require.Contains(s.T(), req.SystemPrompt, `- Description: "the API"`)
			require.Contains(s.T(), req.SystemPrompt, "- Ticket URL: https://tracker.example.com/T-1")
			require.Contains(s.T(), req.SystemPrompt, "`/project/.loop/config.json`")
			require.Equal(s.T(), tc.wantTask, strings.Contains(req.SystemPrompt, "nightly deps"))
		})
	}
}
