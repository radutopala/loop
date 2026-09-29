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
	local := types.PlatformLocal
	tests := []struct {
		name   string
		ch     *db.Channel
		turns  int
		cfg    config.LearnConfig
		parked bool
		want   string
	}{
		{"learns", &db.Channel{Platform: local}, 3, on, false, ""},
		{"override on beats config off", &db.Channel{Platform: local, LearnOverride: db.LearnOn}, 5, config.LearnConfig{MinTurns: 3}, false, ""},
		{"slack channel", &db.Channel{Platform: types.PlatformSlack}, 5, on, false, "not a desktop channel"},
		{"task thread", &db.Channel{Platform: local, TaskID: 7}, 5, on, false, "task thread"},
		{"parked", &db.Channel{Platform: local}, 5, on, true, "parked on a plan or question"},
		{"override off", &db.Channel{Platform: local, LearnOverride: db.LearnOff}, 5, on, false, "learn off"},
		{"config off", &db.Channel{Platform: local}, 5, config.LearnConfig{MinTurns: 3}, false, "learn off"},
		{"too short", &db.Channel{Platform: local}, 2, on, false, "2 turns, below min_turns 3"},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			got := learnSkipReason(tc.ch, &agent.AgentResponse{NumTurns: tc.turns}, tc.cfg, tc.parked)
			require.Equal(s.T(), tc.want, got)
		})
	}
}

func (s *OrchestratorSuite) TestLearnConfig() {
	global := &config.Config{Learn: config.LearnConfig{MinTurns: 3}}
	project := &config.Config{Learn: config.LearnConfig{Enabled: true, MinTurns: 1}}
	worktree := &config.Config{Learn: config.LearnConfig{Enabled: true, MinTurns: 2}}
	plain := &db.Channel{ChannelID: "ch1", DirPath: "/project"}
	wt := &db.Channel{ChannelID: "wt", ParentID: "root", Worktree: true, DirPath: "/project/.worktrees/wt"}
	s.store.On("GetChannel", s.ctx, "wt").Return(wt, nil)
	tests := []struct {
		name     string
		ch       *db.Channel
		loadErr  error
		want     *config.Config
		wantRoot string
	}{
		{"no dir uses global", &db.Channel{ChannelID: "ch1"}, nil, global, ""},
		{"project merged", plain, nil, project, "/project"},
		{"load error falls back to global", plain, errors.New("bad hjson"), global, "/project"},
		{"worktree merges root then worktree", wt, nil, worktree, "/project"},
		{"worktree load error falls back to global", wt, errors.New("bad hjson"), global, "/project"},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.orch.cfg.Store(global)
			s.store.On("GetChannel", s.ctx, "root").Return(&db.Channel{ChannelID: "root", DirPath: "/project"}, nil)
			s.orch.loadProjectConfig = func(dir string, main *config.Config) (*config.Config, error) {
				require.Equal(s.T(), "/project", dir)
				require.Same(s.T(), global, main)
				return project, tc.loadErr
			}
			s.orch.loadWorktreeProjectConfig = func(wtDir, rootDir string, main *config.Config) (*config.Config, error) {
				require.Equal(s.T(), "/project/.worktrees/wt", wtDir)
				require.Equal(s.T(), "/project", rootDir)
				require.Same(s.T(), global, main)
				return worktree, tc.loadErr
			}
			got, root := s.orch.resolvedConfig(s.ctx, tc.ch)
			require.Same(s.T(), tc.want, got)
			require.Equal(s.T(), tc.wantRoot, root)
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
		{"too short", &db.Channel{ChannelID: "ch1", DirPath: "/project", Platform: types.PlatformLocal}, &agent.AgentResponse{SessionID: "sess-1", NumTurns: 1}, func() {}},
		{"no session", &db.Channel{ChannelID: "ch1", DirPath: "/project", Platform: types.PlatformLocal}, &agent.AgentResponse{NumTurns: 5}, func() {}},
		{"channel deleted during the run", &db.Channel{ChannelID: "ch1", DirPath: "/project", Platform: types.PlatformLocal}, learnOn, func() {
			s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
		}},
		{"channel reload error", &db.Channel{ChannelID: "ch1", DirPath: "/project", Platform: types.PlatformLocal}, learnOn, func() {
			s.store.On("GetChannel", s.ctx, "ch1").Return(nil, errors.New("db down"))
		}},
		{"switched off during the run", &db.Channel{ChannelID: "ch1", DirPath: "/project", Platform: types.PlatformLocal}, learnOn, func() {
			s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: "/project", Platform: types.PlatformLocal, LearnOverride: db.LearnOff}, nil)
		}},
		{"switched on during the run", &db.Channel{ChannelID: "ch1", DirPath: "/project", Platform: types.PlatformLocal, LearnOverride: db.LearnOff}, learnOn, func() {
			s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: "/project", Platform: types.PlatformLocal, LearnOverride: db.LearnOn}, nil)
			s.store.On("GetHiddenThread", s.ctx, "ch1", db.ChannelKindLearn).Return(nil, errors.New("db down"))
		}},
		{"lookup error", &db.Channel{ChannelID: "ch1", DirPath: "/project", Platform: types.PlatformLocal}, learnOn, func() {
			s.store.On("GetHiddenThread", s.ctx, "ch1", db.ChannelKindLearn).Return(nil, errors.New("db down"))
		}},
		{"channel deleted before its learn thread was made", &db.Channel{ChannelID: "ch1", DirPath: "/project", Platform: types.PlatformLocal}, learnOn, func() {
			s.store.On("GetHiddenThread", s.ctx, "ch1", db.ChannelKindLearn).Return(nil, nil)
			s.store.On("InsertHiddenThread", s.ctx, mock.Anything).Return(db.ErrParentGone)
		}},
		{"create error", &db.Channel{ChannelID: "ch1", DirPath: "/project", Platform: types.PlatformLocal}, learnOn, func() {
			s.store.On("GetHiddenThread", s.ctx, "ch1", db.ChannelKindLearn).Return(nil, nil)
			s.store.On("InsertHiddenThread", s.ctx, mock.Anything).Return(errors.New("db down"))
		}},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.setupLearnOn()
			tc.setup()
			// Unless the case reloads it differently, ch is unchanged.
			s.store.On("GetChannel", s.ctx, tc.ch.ChannelID).Return(tc.ch, nil).Maybe()
			s.orch.maybeLearn(s.ctx, tc.ch, &bot.IncomingMessage{MessageID: "u1", Content: "hi"}, tc.resp)
			s.store.AssertExpectations(s.T())
			s.store.AssertNotCalled(s.T(), "IsChannelActive", mock.Anything, mock.Anything)
		})
	}
}

// TestMaybeLearnStarts covers a first learn pass: the learn thread is created
// under the channel, the pass recorded as queued, and the trigger message
// handed to the thread's queue. Nothing is forked until the pass runs.
func (s *OrchestratorSuite) TestMaybeLearnStarts() {
	s.setupLearnOn()
	var drains int
	s.orch.drainSpawn = func(func()) { drains++ }
	eb := new(MockEventBroadcaster)
	eb.On("BroadcastMessageCreated", mock.Anything, mock.Anything).Return()
	eb.On("BroadcastLearnStarted", "ch1", mock.Anything).Return()
	eb.On("BroadcastLearnPass", mock.Anything).Return()
	s.orch.SetEventBroadcaster(eb)

	ch := &db.Channel{ChannelID: "ch1", GuildID: "g1", Name: "api", DirPath: "/project", Platform: types.PlatformLocal}
	s.store.On("LastBotMessage", s.ctx, "ch1", "u1").Return(&db.Message{MsgID: "b1"}, nil)
	var recorded *db.LearnPass
	row := &db.LearnPass{ID: 4, ChannelID: "ch1", MessageID: "b1", Status: db.LearnPassQueued}
	s.store.On("InsertLearnPass", s.ctx, mock.MatchedBy(func(p *db.LearnPass) bool {
		recorded = p
		return true
	})).Return(row, nil)
	var created *db.Channel
	s.store.On("GetHiddenThread", s.ctx, "ch1", db.ChannelKindLearn).Return(nil, nil)
	s.store.On("InsertHiddenThread", s.ctx, mock.MatchedBy(func(l *db.Channel) bool {
		created = l
		return true
	})).Return(nil)
	s.store.On("IsChannelActive", s.ctx, mock.Anything).Return(true, nil)
	s.store.On("GetChannel", s.ctx, "ch1").Return(ch, nil)
	s.store.On("GetChannel", s.ctx, mock.Anything).Return(&db.Channel{ID: 9, Platform: types.PlatformLocal, Kind: db.ChannelKindLearn}, nil)
	var trigger *db.Message
	s.store.On("InsertMessage", s.ctx, mock.MatchedBy(func(m *db.Message) bool {
		trigger = m
		return !m.IsBot
	})).Return(nil)

	s.orch.maybeLearn(s.ctx, ch, &bot.IncomingMessage{MessageID: "u1", Content: "fix the tests"}, &agent.AgentResponse{SessionID: "sess-1", NumTurns: 2})

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
	require.Equal(s.T(), &db.LearnPass{ChannelID: "ch1", MessageID: "b1", LearnChannelID: created.ChannelID, TriggerMsgID: trigger.MsgID}, recorded)
	require.NotEmpty(s.T(), trigger.MsgID)
	eb.AssertCalled(s.T(), "BroadcastLearnPass", row)
	require.Equal(s.T(), 1, drains)
	s.store.AssertExpectations(s.T())
	eb.AssertCalled(s.T(), "BroadcastMessageCreated", created.ChannelID, mock.MatchedBy(func(d events.MessageEventData) bool {
		return d.Content == trigger.Content
	}))
	eb.AssertCalled(s.T(), "BroadcastLearnStarted", "ch1", created.ChannelID)
	s.store.AssertNotCalled(s.T(), "MarkSessionForkPending", mock.Anything, mock.Anything, mock.Anything)
}

// TestMaybeLearnQueuesUnrecorded covers a pass that can't be recorded: its
// trigger is still queued behind whatever the learn thread is running, and
// it forks the channel's session as it is when the pass starts.
func (s *OrchestratorSuite) TestMaybeLearnQueuesUnrecorded() {
	tests := []struct {
		name  string
		setup func()
	}{
		{"no reply to review", func() {
			s.store.On("LastBotMessage", s.ctx, "ch1", "u1").Return(nil, nil)
		}},
		{"reply lookup fails", func() {
			s.store.On("LastBotMessage", s.ctx, "ch1", "u1").Return(nil, errors.New("db down"))
		}},
		{"recording the pass fails", func() {
			s.store.On("LastBotMessage", s.ctx, "ch1", "u1").Return(&db.Message{MsgID: "b1"}, nil)
			s.store.On("InsertLearnPass", s.ctx, mock.Anything).Return(nil, errors.New("db down"))
		}},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.setupLearnOn()
			s.orch.drainSpawn = func(func()) {}
			eb := new(MockEventBroadcaster)
			eb.On("BroadcastMessageCreated", mock.Anything, mock.Anything).Return()
			eb.On("BroadcastLearnStarted", "ch1", "learn-1").Return()
			s.orch.SetEventBroadcaster(eb)
			// A pass of the thread's is already running: the new one queues
			// behind it rather than replacing anything.
			s.orch.activeRuns.Store("learn-1", context.CancelFunc(func() {}))
			ch := &db.Channel{ChannelID: "ch1", Name: "api", DirPath: "/project", Platform: types.PlatformLocal}
			s.store.On("GetChannel", s.ctx, "ch1").Return(ch, nil)
			s.store.On("GetHiddenThread", s.ctx, "ch1", db.ChannelKindLearn).Return(&db.Channel{ChannelID: "learn-1", Name: "learn: api"}, nil)
			s.store.On("IsChannelActive", s.ctx, "learn-1").Return(true, nil)
			s.store.On("GetChannel", s.ctx, "learn-1").Return(&db.Channel{ID: 9, ChannelID: "learn-1", Platform: types.PlatformLocal, Kind: db.ChannelKindLearn}, nil)
			s.store.On("InsertMessage", s.ctx, mock.MatchedBy(func(m *db.Message) bool {
				return m.ChannelID == "learn-1" && m.AuthorID == learnAuthorID && m.Content == learn.TriggerMessage("api", "fix it")
			})).Return(nil)
			tc.setup()

			s.orch.maybeLearn(s.ctx, ch, &bot.IncomingMessage{MessageID: "u1", Content: "fix it"}, &agent.AgentResponse{SessionID: "sess-1", NumTurns: 5})

			s.store.AssertExpectations(s.T())
			eb.AssertCalled(s.T(), "BroadcastLearnStarted", "ch1", "learn-1")
			eb.AssertNotCalled(s.T(), "BroadcastLearnPass", mock.Anything)
			s.store.AssertNotCalled(s.T(), "UpdateLearnPass", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
		})
	}
}

// TestLearnPassRun covers a learn pass's bookkeeping: its row is marked
// running when its run starts, then done or failed.
func (s *OrchestratorSuite) TestLearnPassRun() {
	msg := &bot.IncomingMessage{ChannelID: "learn-1", MessageID: "x1", AuthorID: learnAuthorID}
	tests := []struct {
		name        string
		msg         *bot.IncomingMessage
		lookup      *db.LearnPass
		lookupErr   error
		updateErr   error
		runErr      error
		wantUpdates [][2]string // status, error
	}{
		{name: "not a learn pass", msg: &bot.IncomingMessage{ChannelID: "learn-1", AuthorID: "radu"}},
		{name: "pass not recorded", msg: msg},
		{name: "lookup fails", msg: msg, lookupErr: errors.New("db down")},
		{name: "done", msg: msg, lookup: &db.LearnPass{ID: 3},
			wantUpdates: [][2]string{{db.LearnPassRunning, ""}, {db.LearnPassDone, ""}}},
		{name: "run fails", msg: msg, lookup: &db.LearnPass{ID: 3}, runErr: errors.New("agent error: boom"),
			wantUpdates: [][2]string{{db.LearnPassRunning, ""}, {db.LearnPassFailed, "agent error: boom"}}},
		{name: "update fails", msg: msg, lookup: &db.LearnPass{ID: 3}, updateErr: errors.New("db down"),
			wantUpdates: [][2]string{{db.LearnPassRunning, ""}, {db.LearnPassDone, ""}}},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			eb := new(MockEventBroadcaster)
			var broadcast []string
			eb.On("BroadcastLearnPass", mock.Anything).Run(func(args mock.Arguments) {
				broadcast = append(broadcast, args.Get(0).(*db.LearnPass).Status)
			}).Return()
			s.orch.SetEventBroadcaster(eb)
			s.store.On("GetLearnPassByTrigger", s.ctx, "learn-1", "x1").Return(tc.lookup, tc.lookupErr)
			var updates [][2]string
			s.store.On("UpdateLearnPass", s.ctx, int64(3), mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
				updates = append(updates, [2]string{args.String(2), args.String(3)})
			}).Return(tc.updateErr)

			p := s.orch.learnPassStarted(s.ctx, tc.msg)
			s.orch.learnPassDone(s.ctx, p, tc.runErr)

			require.Equal(s.T(), tc.wantUpdates, updates)
			if tc.updateErr != nil || tc.wantUpdates == nil {
				require.Empty(s.T(), broadcast)
				return
			}
			require.Equal(s.T(), []string{db.LearnPassRunning, tc.wantUpdates[1][0]}, broadcast)
			require.Equal(s.T(), tc.wantUpdates[1][1], p.Error)
		})
	}
}

// TestApplyLearnRequestNestedThreadTasks checks a nested thread's pass sees
// the tasks where an applied scheduled_task proposal would land: under the
// thread's parent, not the thread itself.
func (s *OrchestratorSuite) TestApplyLearnRequestNestedThreadTasks() {
	s.orch.cfg.Store(&config.Config{})
	parent := &db.Channel{ChannelID: "t2", ParentID: "t1", Name: "nested"}
	s.store.On("GetChannel", s.ctx, "t2").Return(parent, nil)
	s.store.On("GetChannel", s.ctx, "t1").Return(&db.Channel{ChannelID: "t1", ParentID: "ch1"}, nil)
	s.store.On("ListScheduledTasks", s.ctx, "t1").Return([]*db.ScheduledTask{{Type: db.TaskTypeCron, Schedule: "0 9 * * *", Prompt: "nightly deps"}}, nil)
	s.store.On("ListLearnProposals", s.ctx, "t2").Return([]*db.LearnProposal(nil), nil)

	req := &agent.AgentRequest{}
	require.NoError(s.T(), s.orch.applyLearnRequest(s.ctx, req, parent, false, ""))

	require.Contains(s.T(), req.SystemPrompt, "nightly deps")
	s.store.AssertExpectations(s.T())
}

// TestEnsureLearnChannelRenames checks an existing learn thread follows its
// channel's name, and a failed rename still returns the thread.
func (s *OrchestratorSuite) TestEnsureLearnChannelRenames() {
	ch := &db.Channel{ChannelID: "ch1", Name: "api v2"}
	tests := []struct {
		name      string
		current   string
		renameErr error
		wantName  string
		wantCall  bool
	}{
		{"name unchanged", "learn: api v2", nil, "learn: api v2", false},
		{"renamed", "learn: api", nil, "learn: api v2", true},
		{"rename fails", "learn: api", errors.New("db down"), "learn: api", true},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.store.On("GetHiddenThread", s.ctx, "ch1", db.ChannelKindLearn).Return(&db.Channel{ChannelID: "learn-1", Name: tc.current}, nil)
			if tc.wantCall {
				s.store.On("UpdateChannelName", s.ctx, "learn-1", "learn: api v2").Return(tc.renameErr)
			}
			l, err := s.orch.ensureHiddenThread(s.ctx, ch, db.ChannelKindLearn)
			require.NoError(s.T(), err)
			require.Equal(s.T(), "learn-1", l.ChannelID)
			require.Equal(s.T(), tc.wantName, l.Name)
			s.store.AssertExpectations(s.T())
		})
	}
}

func (s *OrchestratorSuite) TestRunTrigger() {
	s.bot.ExpectedCalls = nil
	s.bot.On("IsBotUser", "bot-1").Return(true)
	s.bot.On("IsBotUser", mock.Anything).Return(false)
	chat := &db.Channel{ChannelID: "ch1"}
	learnThread := &db.Channel{ChannelID: "learn-1", Kind: db.ChannelKindLearn}
	tests := []struct {
		name     string
		ch       *db.Channel
		authorID string
		want     string
	}{
		{name: "learn pass", ch: learnThread, authorID: learnAuthorID, want: "learn"},
		{name: "explanation", ch: &db.Channel{ChannelID: "explain-1", Kind: db.ChannelKindExplain}, authorID: explainAuthorID, want: "explain"},
		{name: "bot", ch: chat, authorID: "bot-1", want: "bot"},
		{name: "user", ch: chat, authorID: "user-1", want: ""},
		{name: "user in a learn thread", ch: learnThread, authorID: "user-1", want: "learn-reply"},
		{name: "bot in a learn thread", ch: learnThread, authorID: "bot-1", want: "bot"},
		{name: "no channel", authorID: "user-1", want: ""},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			require.Equal(s.T(), tc.want, s.orch.runTrigger(tc.ch, tc.authorID))
		})
	}
}

// TestMaybeLearnWorktreeUsesRootConfig checks a worktree thread's learn
// config is merged like its runs': the root checkout's, then the worktree's.
func (s *OrchestratorSuite) TestMaybeLearnWorktreeUsesRootConfig() {
	s.orch.cfg.Store(&config.Config{})
	var loaded []string
	s.orch.loadWorktreeProjectConfig = func(wtDir, rootDir string, main *config.Config) (*config.Config, error) {
		loaded = append(loaded, wtDir, rootDir)
		return main, nil
	}
	s.store.On("GetChannel", s.ctx, "root").Return(&db.Channel{ChannelID: "root", DirPath: "/project"}, nil)

	wt := &db.Channel{ChannelID: "wt", ParentID: "root", Worktree: true, DirPath: "/project/.worktrees/wt"}
	s.store.On("GetChannel", s.ctx, "wt").Return(wt, nil)
	s.orch.maybeLearn(s.ctx, wt, &bot.IncomingMessage{}, &agent.AgentResponse{SessionID: "sess-1", NumTurns: 9})

	require.Equal(s.T(), []string{"/project/.worktrees/wt", "/project"}, loaded)
	s.store.AssertNotCalled(s.T(), "GetHiddenThread", mock.Anything, mock.Anything, mock.Anything)
}

// TestPrepareAgentRequestLearnThread checks a learn thread's run: it forks
// the parent's session, runs as the learn agent with learn mode's tool
// denials, and its prompt carries the parent's state.
func (s *OrchestratorSuite) TestPrepareAgentRequestLearnThread() {
	tests := []struct {
		name       string
		learnCfg   config.LearnConfig
		tasksErr   error
		propsErr   error
		wantModel  string
		wantEffort string
		wantTask   bool
	}{
		{"learn model and effort", config.LearnConfig{Model: "opus", Effort: "high"}, nil, nil, "opus", "high", true},
		{"falls back to parent overrides", config.LearnConfig{}, nil, nil, "sonnet", "low", true},
		{"task listing error still runs", config.LearnConfig{}, errors.New("db down"), nil, "sonnet", "low", false},
		{"proposal listing error still runs", config.LearnConfig{}, nil, errors.New("db down"), "sonnet", "low", true},
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
			var proposals []*db.LearnProposal
			if tc.propsErr == nil {
				proposals = []*db.LearnProposal{{Kind: db.LearnKindBashShortcut, Title: "Add a vitest shortcut", Payload: "{}", Status: db.LearnPending}}
			}
			s.store.On("ListLearnProposals", s.ctx, "ch1").Return(proposals, tc.propsErr)

			req, _, _, err := s.orch.prepareAgentRequest(s.ctx, &bot.IncomingMessage{ChannelID: "learn-1", AuthorName: learnAuthorName, Content: "review"}, "")
			require.NoError(s.T(), err)
			require.True(s.T(), req.ForkSession)
			require.Equal(s.T(), "sess-parent", req.SessionID)
			require.Equal(s.T(), learn.AgentID, req.AgentID)
			require.True(s.T(), req.ReadOnly)
			require.Equal(s.T(), tc.wantModel, req.Model)
			require.Equal(s.T(), tc.wantEffort, req.Effort)
			require.Contains(s.T(), req.SystemPrompt, `- Channel: "api"`)
			require.Contains(s.T(), req.SystemPrompt, `- Description: "the API"`)
			require.Contains(s.T(), req.SystemPrompt, "- Ticket URL: https://tracker.example.com/T-1")
			require.Contains(s.T(), req.SystemPrompt, "`/project/.loop/config.json`")
			require.Equal(s.T(), tc.wantTask, strings.Contains(req.SystemPrompt, "nightly deps"))
			require.Equal(s.T(), tc.propsErr == nil, strings.Contains(req.SystemPrompt, "Add a vitest shortcut"))
		})
	}
}

// TestPrepareAgentRequestLearnThreadNoParent checks a learn thread whose
// parent can't be loaded doesn't run at all, rather than as a chat run with
// the full tool set.
func (s *OrchestratorSuite) TestPrepareAgentRequestLearnThreadNoParent() {
	tests := []struct {
		name  string
		ch    *db.Channel
		setup func()
	}{
		{"parent lookup error", &db.Channel{ChannelID: "learn-1", ParentID: "ch1", Kind: db.ChannelKindLearn}, func() {
			s.store.On("GetChannel", s.ctx, "ch1").Return(nil, errors.New("db down"))
		}},
		{"parent gone", &db.Channel{ChannelID: "learn-1", ParentID: "ch1", Kind: db.ChannelKindLearn}, func() {
			s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
		}},
		{"no parent", &db.Channel{ChannelID: "learn-1", Kind: db.ChannelKindLearn}, func() {}},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.store.On("GetRecentMessages", s.ctx, "learn-1", recentMessageLimit).Return([]*db.Message{}, nil)
			s.store.On("GetChannel", s.ctx, "learn-1").Return(tc.ch, nil)
			tc.setup()
			req, _, _, err := s.orch.prepareAgentRequest(s.ctx, &bot.IncomingMessage{ChannelID: "learn-1", AuthorName: "radu", Content: "why?"}, "")
			require.ErrorContains(s.T(), err, "learn thread learn-1")
			require.Nil(s.T(), req)
		})
	}
}

// TestLearnTurnRefusesOrReturnsExisting covers what LearnTurn does before
// it queues anything: refuses what it can't learn from, or returns the pass
// over the turn already queued or running.
func (s *OrchestratorSuite) TestLearnTurnRefusesOrReturnsExisting() {
	ch := &db.Channel{ChannelID: "ch1", Name: "api", Platform: types.PlatformLocal, SessionID: "sess-1"}
	reply := &db.Message{MsgID: "b1", IsBot: true, TriggerMsgID: "u1", Content: "Fixed the tests."}
	existing := &db.LearnPass{ID: 7, ChannelID: "ch1", MessageID: "b1", Status: db.LearnPassRunning}
	dbErr := errors.New("db down")
	turn := func() {
		s.store.On("GetChatMessage", s.ctx, "ch1", "b1").Return(reply, nil)
	}
	fresh := func() {
		turn()
		s.store.On("ActiveLearnPass", s.ctx, "ch1", "b1").Return(nil, nil)
		s.store.On("GetChatMessage", s.ctx, "ch1", "u1").Return(nil, nil)
	}
	tests := []struct {
		name    string
		ch      *db.Channel
		setup   func()
		wantErr error
		want    *db.LearnPass
	}{
		{name: "slack channel", ch: &db.Channel{ChannelID: "ch1", Platform: types.PlatformSlack, SessionID: "s"}, wantErr: learn.ErrUnavailable},
		{name: "task thread", ch: &db.Channel{ChannelID: "ch1", Platform: types.PlatformLocal, TaskID: 3, SessionID: "s"}, wantErr: learn.ErrUnavailable},
		{name: "learn thread", ch: &db.Channel{ChannelID: "ch1", Platform: types.PlatformLocal, Kind: db.ChannelKindLearn, SessionID: "s"}, wantErr: learn.ErrUnavailable},
		{name: "message lookup fails", ch: ch, setup: func() {
			s.store.On("GetChatMessage", s.ctx, "ch1", "b1").Return(nil, dbErr)
		}, wantErr: dbErr},
		{name: "no such message", ch: ch, setup: func() {
			s.store.On("GetChatMessage", s.ctx, "ch1", "b1").Return(nil, nil)
		}, wantErr: learn.ErrNotATurn},
		{name: "a user message", ch: ch, setup: func() {
			s.store.On("GetChatMessage", s.ctx, "ch1", "b1").Return(&db.Message{MsgID: "b1"}, nil)
		}, wantErr: learn.ErrNotATurn},
		{name: "a notice outside a turn", ch: ch, setup: func() {
			s.store.On("GetChatMessage", s.ctx, "ch1", "b1").Return(&db.Message{MsgID: "b1", IsBot: true}, nil)
		}, wantErr: learn.ErrNotATurn},
		{name: "no session", ch: &db.Channel{ChannelID: "ch1", Platform: types.PlatformLocal}, setup: turn, wantErr: learn.ErrNoSession},
		{name: "existing lookup fails", ch: ch, setup: func() {
			turn()
			s.store.On("ActiveLearnPass", s.ctx, "ch1", "b1").Return(nil, dbErr)
		}, wantErr: dbErr},
		{name: "already queued or running", ch: ch, setup: func() {
			turn()
			s.store.On("ActiveLearnPass", s.ctx, "ch1", "b1").Return(existing, nil)
		}, want: existing},
		{name: "learn thread fails", ch: ch, setup: func() {
			fresh()
			s.store.On("GetHiddenThread", s.ctx, "ch1", db.ChannelKindLearn).Return(nil, dbErr)
		}, wantErr: dbErr},
		{name: "channel deleted before its learn thread was made", ch: ch, setup: func() {
			fresh()
			s.store.On("GetHiddenThread", s.ctx, "ch1", db.ChannelKindLearn).Return(nil, nil)
			s.store.On("InsertHiddenThread", s.ctx, mock.Anything).Return(db.ErrParentGone)
		}, wantErr: db.ErrParentGone},
		{name: "recording the pass fails", ch: ch, setup: func() {
			fresh()
			s.store.On("GetHiddenThread", s.ctx, "ch1", db.ChannelKindLearn).Return(&db.Channel{ChannelID: "learn-1", Name: "learn: api"}, nil)
			s.store.On("InsertLearnPass", s.ctx, mock.Anything).Return(nil, dbErr)
		}, wantErr: dbErr},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			eb := new(MockEventBroadcaster)
			s.orch.SetEventBroadcaster(eb)
			if tc.setup != nil {
				tc.setup()
			}

			p, err := s.orch.LearnTurn(s.ctx, tc.ch, "b1")

			if tc.wantErr != nil {
				require.ErrorIs(s.T(), err, tc.wantErr)
				require.Nil(s.T(), p)
			} else {
				require.NoError(s.T(), err)
				require.Same(s.T(), tc.want, p)
			}
			s.store.AssertExpectations(s.T())
			eb.AssertNotCalled(s.T(), "BroadcastLearnPass", mock.Anything)
		})
	}
}

// TestLearnTurnStarts covers a pass the user asked for with learning off:
// it's recorded against the turn and its trigger message handed to the
// learn thread's queue, behind any pass already running there. A turn with
// its own session and no channel session still learns, and the turn's
// prompt failing to load leaves it out.
func (s *OrchestratorSuite) TestLearnTurnStarts() {
	tests := []struct {
		name       string
		session    string
		reply      *db.Message
		prompt     *db.Message
		promptErr  error
		wantPrompt string
	}{
		{name: "forks the channel's session", session: "sess-9",
			reply:  &db.Message{MsgID: "b1", IsBot: true, TriggerMsgID: "u1", Content: "Fixed the tests."},
			prompt: &db.Message{Content: "fix the tests"}, wantPrompt: "fix the tests"},
		{name: "the turn's own session",
			reply:  &db.Message{MsgID: "b1", IsBot: true, TriggerMsgID: "u1", Content: "Fixed the tests.", SessionID: "sess-0", TranscriptUUID: "uuid-1"},
			prompt: &db.Message{Content: "fix the tests"}, wantPrompt: "fix the tests"},
		{name: "prompt fails to load", session: "sess-9",
			reply:     &db.Message{MsgID: "b1", IsBot: true, TriggerMsgID: "u1", Content: "Fixed the tests."},
			promptErr: errors.New("db down")},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.orch.cfg.Store(&config.Config{})
			var drains int
			s.orch.drainSpawn = func(func()) { drains++ }
			eb := new(MockEventBroadcaster)
			eb.On("BroadcastMessageCreated", mock.Anything, mock.Anything).Return()
			eb.On("BroadcastLearnStarted", "ch1", "learn-1").Return()
			eb.On("BroadcastLearnPass", mock.Anything).Return()
			s.orch.SetEventBroadcaster(eb)
			s.orch.activeRuns.Store("learn-1", context.CancelFunc(func() {}))

			ch := &db.Channel{ChannelID: "ch1", GuildID: "g1", Name: "api", Platform: types.PlatformLocal, SessionID: tc.session, LearnOverride: db.LearnOff}
			s.store.On("GetChatMessage", s.ctx, "ch1", "b1").Return(tc.reply, nil)
			s.store.On("GetChatMessage", s.ctx, "ch1", "u1").Return(tc.prompt, tc.promptErr)
			s.store.On("ActiveLearnPass", s.ctx, "ch1", "b1").Return(nil, nil)
			s.store.On("GetHiddenThread", s.ctx, "ch1", db.ChannelKindLearn).Return(&db.Channel{ChannelID: "learn-1", GuildID: "g1", Name: "learn: api", Kind: db.ChannelKindLearn}, nil)
			var recorded *db.LearnPass
			row := &db.LearnPass{ID: 4, ChannelID: "ch1", MessageID: "b1", LearnChannelID: "learn-1", Status: db.LearnPassQueued}
			s.store.On("InsertLearnPass", s.ctx, mock.MatchedBy(func(p *db.LearnPass) bool {
				recorded = p
				return true
			})).Return(row, nil)
			s.store.On("IsChannelActive", s.ctx, "learn-1").Return(true, nil)
			s.store.On("GetChannel", s.ctx, "learn-1").Return(&db.Channel{ID: 9, ChannelID: "learn-1", Platform: types.PlatformLocal, Kind: db.ChannelKindLearn}, nil)
			var trigger *db.Message
			s.store.On("InsertMessage", s.ctx, mock.MatchedBy(func(m *db.Message) bool {
				trigger = m
				return !m.IsBot
			})).Return(nil)

			p, err := s.orch.LearnTurn(s.ctx, ch, "b1")

			require.NoError(s.T(), err)
			require.Same(s.T(), row, p)
			require.NotNil(s.T(), trigger)
			require.Equal(s.T(), "learn-1", trigger.ChannelID)
			require.Equal(s.T(), learnAuthorID, trigger.AuthorID)
			require.Equal(s.T(), learn.TurnTriggerMessage("api", tc.wantPrompt, "Fixed the tests."), trigger.Content)
			require.True(s.T(), learn.IsTrigger(trigger.Content))
			require.Equal(s.T(), &db.LearnPass{ChannelID: "ch1", MessageID: "b1", LearnChannelID: "learn-1", TriggerMsgID: trigger.MsgID}, recorded)
			require.Equal(s.T(), 1, drains)
			s.store.AssertExpectations(s.T())
			eb.AssertCalled(s.T(), "BroadcastLearnPass", row)
			eb.AssertCalled(s.T(), "BroadcastLearnStarted", "ch1", "learn-1")
			s.store.AssertNotCalled(s.T(), "MarkSessionForkPending", mock.Anything, mock.Anything, mock.Anything)
			s.store.AssertNotCalled(s.T(), "UpdateLearnPass", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
		})
	}
}

// TestPrepareAgentRequestLearnPass checks what a learn thread's run forks:
// a pass forks its turn's session cut where the turn ended, else the
// parent's whole current session, and fails with none; a user's reply
// resumes the learn thread's own session, the latest pass's fork.
func (s *OrchestratorSuite) TestPrepareAgentRequestLearnPass() {
	tests := []struct {
		name      string
		authorID  string
		turnID    string
		own       string
		parentSID string
		turn      *db.Message
		turnErr   error
		wantErr   error
		wantSess  string
		wantAt    string
		wantFork  bool
	}{
		{name: "pass forks at its turn", authorID: learnAuthorID, turnID: "b1", own: "fork-1", parentSID: "sess-parent",
			turn: &db.Message{SessionID: "sess-turn", TranscriptUUID: "uuid-1"}, wantSess: "sess-turn", wantAt: "uuid-1", wantFork: true},
		{name: "turn without a ref forks the parent's session", authorID: learnAuthorID, turnID: "b1", parentSID: "sess-parent",
			turn: &db.Message{SessionID: "sess-turn"}, wantSess: "sess-parent", wantFork: true},
		{name: "turn lookup fails", authorID: learnAuthorID, turnID: "b1", parentSID: "sess-parent",
			turnErr: errors.New("db down"), wantSess: "sess-parent", wantFork: true},
		{name: "unrecorded pass forks the parent's session", authorID: learnAuthorID, parentSID: "sess-parent", wantSess: "sess-parent", wantFork: true},
		{name: "pass with no session", authorID: learnAuthorID, own: "fork-1", wantErr: learn.ErrNoSession},
		{name: "reply resumes the thread's fork", authorID: "radu", turnID: "b1", own: "fork-1", parentSID: "sess-parent", wantSess: "fork-1"},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.orch.cfg.Store(&config.Config{})
			s.store.On("GetRecentMessages", s.ctx, "learn-1", recentMessageLimit).Return([]*db.Message{}, nil)
			s.store.On("GetChannel", s.ctx, "learn-1").Return(&db.Channel{ChannelID: "learn-1", ParentID: "ch1", Kind: db.ChannelKindLearn, SessionID: tc.own}, nil)
			s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{ChannelID: "ch1", SessionID: tc.parentSID}, nil)
			s.store.On("GetChatMessage", s.ctx, "ch1", "b1").Return(tc.turn, tc.turnErr).Maybe()
			s.store.On("ListScheduledTasks", s.ctx, "ch1").Return([]*db.ScheduledTask(nil), nil).Maybe()
			s.store.On("ListLearnProposals", s.ctx, "ch1").Return([]*db.LearnProposal(nil), nil).Maybe()

			req, _, _, err := s.orch.prepareAgentRequest(s.ctx, &bot.IncomingMessage{ChannelID: "learn-1", AuthorID: tc.authorID, AuthorName: "loop", Content: "review"}, tc.turnID)

			if tc.wantErr != nil {
				require.ErrorIs(s.T(), err, tc.wantErr)
				require.Nil(s.T(), req)
				return
			}
			require.NoError(s.T(), err)
			require.Equal(s.T(), tc.wantSess, req.SessionID)
			require.Equal(s.T(), tc.wantAt, req.ResumeAt)
			require.Equal(s.T(), tc.wantFork, req.ForkSession)
			require.Equal(s.T(), learn.AgentID, req.AgentID)
			if tc.authorID != learnAuthorID {
				s.store.AssertNotCalled(s.T(), "GetChatMessage", mock.Anything, mock.Anything, mock.Anything)
			}
		})
	}
}

func (s *OrchestratorSuite) TestCanForkTurn() {
	tests := []struct {
		name  string
		ch    string
		reply *db.Message
		want  bool
	}{
		{"channel session", "sess-1", &db.Message{}, true},
		{"turn's own", "", &db.Message{SessionID: "sess-0", TranscriptUUID: "uuid-1"}, true},
		{"turn session without a uuid", "", &db.Message{SessionID: "sess-0"}, false},
		{"none", "", &db.Message{}, false},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			require.Equal(s.T(), tc.want, canForkTurn(&db.Channel{SessionID: tc.ch}, tc.reply))
		})
	}
}
