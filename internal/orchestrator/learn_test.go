package orchestrator

import (
	"context"
	"errors"
	"strings"
	"time"

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
			got, root := s.orch.learnConfig(s.ctx, tc.ch)
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
		{"lookup error", &db.Channel{ChannelID: "ch1", DirPath: "/project", Platform: types.PlatformLocal}, learnOn, func() {
			s.store.On("GetLearnChannel", s.ctx, "ch1").Return(nil, errors.New("db down"))
		}},
		{"channel deleted before its learn thread was made", &db.Channel{ChannelID: "ch1", DirPath: "/project", Platform: types.PlatformLocal}, learnOn, func() {
			s.store.On("GetLearnChannel", s.ctx, "ch1").Return(nil, nil)
			s.store.On("InsertLearnChannel", s.ctx, mock.Anything).Return(db.ErrLearnParentGone)
		}},
		{"create error", &db.Channel{ChannelID: "ch1", DirPath: "/project", Platform: types.PlatformLocal}, learnOn, func() {
			s.store.On("GetLearnChannel", s.ctx, "ch1").Return(nil, nil)
			s.store.On("InsertLearnChannel", s.ctx, mock.Anything).Return(errors.New("db down"))
		}},
		{"learn pass running", &db.Channel{ChannelID: "ch1", DirPath: "/project", Platform: types.PlatformLocal}, learnOn, func() {
			s.store.On("GetLearnChannel", s.ctx, "ch1").Return(&db.Channel{ChannelID: "learn-1", Name: "learn: "}, nil)
			s.orch.activeRuns.Store("learn-1", context.CancelFunc(func() {}))
		}},
		{"fork error", &db.Channel{ChannelID: "ch1", DirPath: "/project", Platform: types.PlatformLocal}, learnOn, func() {
			s.store.On("GetLearnChannel", s.ctx, "ch1").Return(&db.Channel{ChannelID: "learn-1", Name: "learn: "}, nil)
			s.store.On("MarkSessionForkPending", s.ctx, "learn-1", "sess-1").Return(false, errors.New("db down"))
		}},
		{"learn thread deleted meanwhile", &db.Channel{ChannelID: "ch1", DirPath: "/project", Platform: types.PlatformLocal}, learnOn, func() {
			s.store.On("GetLearnChannel", s.ctx, "ch1").Return(&db.Channel{ChannelID: "learn-1", Name: "learn: "}, nil)
			s.store.On("MarkSessionForkPending", s.ctx, "learn-1", "sess-1").Return(false, nil)
		}},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.setupLearnOn()
			tc.setup()
			// Unless the case reloads it differently, ch is unchanged.
			s.store.On("GetChannel", s.ctx, tc.ch.ChannelID).Return(tc.ch, nil).Maybe()
			s.orch.maybeLearn(s.ctx, tc.ch, &bot.IncomingMessage{Content: "hi"}, tc.resp)
			s.store.AssertExpectations(s.T())
			// Only the busy thread keeps the run, to review it next.
			_, held := s.orch.learnSlots["learn-1"]
			require.Equal(s.T(), tc.name == "learn pass running", held)
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

	ch := &db.Channel{ChannelID: "ch1", GuildID: "g1", Name: "api", DirPath: "/project", Platform: types.PlatformLocal}
	var created *db.Channel
	s.store.On("GetLearnChannel", s.ctx, "ch1").Return(nil, nil)
	s.store.On("InsertLearnChannel", s.ctx, mock.MatchedBy(func(l *db.Channel) bool {
		created = l
		return true
	})).Return(nil)
	s.store.On("MarkSessionForkPending", s.ctx, mock.MatchedBy(func(id string) bool {
		return strings.HasPrefix(id, "learn-")
	}), "sess-1").Return(true, nil)
	s.store.On("IsChannelActive", s.ctx, mock.Anything).Return(true, nil)
	s.store.On("GetChannel", s.ctx, "ch1").Return(ch, nil)
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

func (s *OrchestratorSuite) TestLearnQueue() {
	p1 := &learnPass{parent: &db.Channel{ChannelID: "ch1", Name: "api"}, prompt: "one", sessionID: "sess-1"}
	p2 := &learnPass{parent: p1.parent, prompt: "two", sessionID: "sess-2"}
	p3 := &learnPass{parent: p1.parent, prompt: "three", sessionID: "sess-3"}

	s.Run("later runs fold into the newest while a pass is queued", func() {
		s.SetupTest()
		require.False(s.T(), s.orch.queueLearn("learn-1", p1))
		require.True(s.T(), s.orch.queueLearn("learn-1", p2))
		require.True(s.T(), s.orch.queueLearn("learn-1", p3))
		require.True(s.T(), s.orch.learnSlots["learn-1"].triggered)
		require.Same(s.T(), p3, s.orch.learnSlots["learn-1"].next)
	})

	s.Run("a user's run in the learn thread holds the pass back", func() {
		s.SetupTest()
		s.orch.activeRuns.Store("learn-1", context.CancelFunc(func() {}))
		require.True(s.T(), s.orch.queueLearn("learn-1", p1))
		require.False(s.T(), s.orch.learnSlots["learn-1"].triggered)
		require.Same(s.T(), p1, s.orch.learnSlots["learn-1"].next)
	})

	s.Run("a trigger that never ran is given up after learnTriggerLost", func() {
		s.SetupTest()
		now := s.orch.timeNow()
		s.orch.learnSlots = map[string]*learnSlot{"learn-1": {triggered: true, triggeredAt: now.Add(-learnTriggerLost - time.Second)}}
		require.False(s.T(), s.orch.queueLearn("learn-1", p1))
		require.Nil(s.T(), s.orch.learnSlots["learn-1"].next)
	})
}

func (s *OrchestratorSuite) TestLearnRunDone() {
	pass := &learnPass{parent: &db.Channel{ChannelID: "ch1", Name: "api"}, prompt: "fix it", sessionID: "sess-2"}
	tests := []struct {
		name     string
		slot     *learnSlot
		authorID string
		setup    func()
		wantSlot *learnSlot
	}{
		{name: "not a learn thread", authorID: learnAuthorID},
		{name: "pass done, nothing waiting", slot: &learnSlot{triggered: true}, authorID: learnAuthorID},
		{
			name:     "user run ends before the queued pass",
			slot:     &learnSlot{triggered: true, next: pass},
			authorID: "user-1",
			wantSlot: &learnSlot{triggered: true, next: pass},
		},
		{
			name:     "learn thread lookup fails",
			slot:     &learnSlot{triggered: true, next: pass},
			authorID: learnAuthorID,
			setup: func() {
				s.store.On("GetChannel", s.ctx, "learn-1").Return(nil, errors.New("db down"))
			},
		},
		{
			name:     "waiting pass fails to fork",
			slot:     &learnSlot{next: pass},
			authorID: "user-1",
			setup: func() {
				s.store.On("GetChannel", s.ctx, "learn-1").Return(&db.Channel{ChannelID: "learn-1"}, nil)
				s.store.On("MarkSessionForkPending", s.ctx, "learn-1", "sess-2").Return(false, errors.New("db down"))
			},
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.orch.learnSlots = map[string]*learnSlot{}
			if tc.slot != nil {
				s.orch.learnSlots["learn-1"] = tc.slot
			}
			if tc.setup != nil {
				tc.setup()
			}
			s.orch.learnRunDone(s.ctx, "learn-1", tc.authorID)
			s.store.AssertExpectations(s.T())
			require.Equal(s.T(), tc.wantSlot, s.orch.learnSlots["learn-1"])
		})
	}
}

// TestLearnPassRunning covers IsLearnPassRunning: only a pass's own run marks
// its thread running, not a user's reply there or a pass still queued, and
// the mark clears when the pass's run ends.
func (s *OrchestratorSuite) TestLearnPassRunning() {
	tests := []struct {
		name     string
		slot     *learnSlot
		authorID string
		want     bool
	}{
		{name: "no slot, user run", authorID: "user-1"},
		{name: "queued pass, user run", slot: &learnSlot{triggered: true}, authorID: "user-1"},
		{name: "queued pass starts", slot: &learnSlot{triggered: true}, authorID: learnAuthorID, want: true},
		{name: "pass resumed after a restart", authorID: learnAuthorID, want: true},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			if tc.slot != nil {
				s.orch.learnSlots = map[string]*learnSlot{"learn-1": tc.slot}
			}
			require.False(s.T(), s.orch.IsLearnPassRunning("learn-1"))
			s.orch.learnRunStarted("learn-1", tc.authorID)
			require.Equal(s.T(), tc.want, s.orch.IsLearnPassRunning("learn-1"))
			if tc.want {
				require.True(s.T(), s.orch.learnSlots["learn-1"].triggered)
			}
			s.orch.learnRunDone(s.ctx, "learn-1", tc.authorID)
			require.False(s.T(), s.orch.IsLearnPassRunning("learn-1"))
		})
	}
}

// TestLearnRunDoneStartsWaitingPass covers the pass queued behind a running
// one: when that run ends, the waiting run's session is forked and its
// trigger queued, and the thread stays claimed for it.
func (s *OrchestratorSuite) TestLearnRunDoneStartsWaitingPass() {
	s.orch.drainSpawn = func(func()) {}
	eb := new(MockEventBroadcaster)
	eb.On("BroadcastMessageCreated", mock.Anything, mock.Anything).Return()
	eb.On("BroadcastLearnStarted", "ch1", "learn-1").Return()
	s.orch.SetEventBroadcaster(eb)
	pass := &learnPass{parent: &db.Channel{ChannelID: "ch1", Name: "api"}, prompt: "fix it", sessionID: "sess-2"}
	s.orch.learnSlots = map[string]*learnSlot{"learn-1": {triggered: true, next: pass}}

	s.store.On("GetChannel", s.ctx, "learn-1").Return(&db.Channel{ID: 9, ChannelID: "learn-1", Platform: types.PlatformLocal, Kind: db.ChannelKindLearn}, nil)
	s.store.On("MarkSessionForkPending", s.ctx, "learn-1", "sess-2").Return(true, nil)
	s.store.On("IsChannelActive", s.ctx, "learn-1").Return(true, nil)
	s.store.On("InsertMessage", s.ctx, mock.MatchedBy(func(m *db.Message) bool {
		return m.AuthorID == learnAuthorID && m.Content == learn.TriggerMessage("api", "fix it")
	})).Return(nil)

	s.orch.learnRunDone(s.ctx, "learn-1", learnAuthorID)

	s.store.AssertExpectations(s.T())
	eb.AssertCalled(s.T(), "BroadcastLearnStarted", "ch1", "learn-1")
	slot := s.orch.learnSlots["learn-1"]
	require.True(s.T(), slot.triggered)
	require.Nil(s.T(), slot.next)
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
	s.orch.applyLearnRequest(s.ctx, req, parent)

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
			s.store.On("GetLearnChannel", s.ctx, "ch1").Return(&db.Channel{ChannelID: "learn-1", Name: tc.current}, nil)
			if tc.wantCall {
				s.store.On("UpdateChannelName", s.ctx, "learn-1", "learn: api v2").Return(tc.renameErr)
			}
			l, err := s.orch.ensureLearnChannel(s.ctx, ch)
			require.NoError(s.T(), err)
			require.Equal(s.T(), "learn-1", l.ChannelID)
			require.Equal(s.T(), tc.wantName, l.Name)
			s.store.AssertExpectations(s.T())
		})
	}
}

// TestStopLearn checks a deleted learn thread's queued pass is dropped and
// its running one cancelled.
func (s *OrchestratorSuite) TestStopLearn() {
	cancelled := false
	s.orch.activeRuns.Store("learn-1", context.CancelFunc(func() { cancelled = true }))
	s.orch.learnSlots = map[string]*learnSlot{"learn-1": {triggered: true, next: &learnPass{}}}

	s.orch.StopLearn("learn-1")

	require.True(s.T(), cancelled)
	require.NotContains(s.T(), s.orch.learnSlots, "learn-1")
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
			req, _, _, err := s.orch.prepareAgentRequest(s.ctx, &bot.IncomingMessage{ChannelID: "learn-1", AuthorName: "radu", Content: "why?"})
			require.ErrorContains(s.T(), err, "learn thread learn-1")
			require.Nil(s.T(), req)
		})
	}
}
