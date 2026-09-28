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
		{"learn pass running", &db.Channel{ChannelID: "ch1", DirPath: "/project", Platform: types.PlatformLocal}, learnOn, func() {
			s.store.On("GetHiddenThread", s.ctx, "ch1", db.ChannelKindLearn).Return(&db.Channel{ChannelID: "learn-1", Name: "learn: "}, nil)
			s.store.On("LastBotMessage", s.ctx, "ch1", "u1").Return(nil, nil)
			s.orch.activeRuns.Store("learn-1", context.CancelFunc(func() {}))
		}},
		{"fork error", &db.Channel{ChannelID: "ch1", DirPath: "/project", Platform: types.PlatformLocal}, learnOn, func() {
			s.store.On("GetHiddenThread", s.ctx, "ch1", db.ChannelKindLearn).Return(&db.Channel{ChannelID: "learn-1", Name: "learn: "}, nil)
			s.store.On("LastBotMessage", s.ctx, "ch1", "u1").Return(&db.Message{MsgID: "b1"}, nil)
			s.store.On("InsertLearnPass", s.ctx, mock.Anything).Return(&db.LearnPass{ID: 4}, nil)
			s.store.On("MarkSessionForkPending", s.ctx, "learn-1", "sess-1").Return(false, errors.New("db down"))
			s.store.On("UpdateLearnPass", s.ctx, int64(4), db.LearnPassFailed, "db down").Return(nil)
		}},
		{"learn thread deleted meanwhile", &db.Channel{ChannelID: "ch1", DirPath: "/project", Platform: types.PlatformLocal}, learnOn, func() {
			s.store.On("GetHiddenThread", s.ctx, "ch1", db.ChannelKindLearn).Return(&db.Channel{ChannelID: "learn-1", Name: "learn: "}, nil)
			s.store.On("LastBotMessage", s.ctx, "ch1", "u1").Return(nil, errors.New("db down"))
			s.store.On("MarkSessionForkPending", s.ctx, "learn-1", "sess-1").Return(false, nil)
		}},
		{"recording the pass fails", &db.Channel{ChannelID: "ch1", DirPath: "/project", Platform: types.PlatformLocal}, learnOn, func() {
			s.store.On("GetHiddenThread", s.ctx, "ch1", db.ChannelKindLearn).Return(&db.Channel{ChannelID: "learn-1", Name: "learn: "}, nil)
			s.store.On("LastBotMessage", s.ctx, "ch1", "u1").Return(&db.Message{MsgID: "b1"}, nil)
			s.store.On("InsertLearnPass", s.ctx, mock.Anything).Return(nil, errors.New("db down"))
			s.store.On("MarkSessionForkPending", s.ctx, "learn-1", "sess-1").Return(false, errors.New("db down"))
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
}

func (s *OrchestratorSuite) TestLearnQueue() {
	p1 := &learnPass{parent: &db.Channel{ChannelID: "ch1", Name: "api"}, prompt: "one", sessionID: "sess-1"}
	p2 := &learnPass{parent: p1.parent, prompt: "two", sessionID: "sess-2"}
	p3 := &learnPass{parent: p1.parent, prompt: "three", sessionID: "sess-3"}

	s.Run("later runs fold into the newest while a pass is queued", func() {
		s.SetupTest()
		queued, replaced := s.orch.queueLearn("learn-1", p1)
		require.False(s.T(), queued)
		require.Nil(s.T(), replaced)
		queued, replaced = s.orch.queueLearn("learn-1", p2)
		require.True(s.T(), queued)
		require.Nil(s.T(), replaced)
		queued, replaced = s.orch.queueLearn("learn-1", p3)
		require.True(s.T(), queued)
		require.Same(s.T(), p2, replaced)
		require.True(s.T(), s.orch.learnSlots["learn-1"].triggered)
		require.Same(s.T(), p3, s.orch.learnSlots["learn-1"].next)
	})

	s.Run("a user's run in the learn thread holds the pass back", func() {
		s.SetupTest()
		s.orch.activeRuns.Store("learn-1", context.CancelFunc(func() {}))
		queued, replaced := s.orch.queueLearn("learn-1", p1)
		require.True(s.T(), queued)
		require.Nil(s.T(), replaced)
		require.False(s.T(), s.orch.learnSlots["learn-1"].triggered)
		require.Same(s.T(), p1, s.orch.learnSlots["learn-1"].next)
	})

	s.Run("a trigger that never ran is given up after learnTriggerLost", func() {
		s.SetupTest()
		now := s.orch.timeNow()
		s.orch.learnSlots = map[string]*learnSlot{"learn-1": {triggered: true, triggeredAt: now.Add(-learnTriggerLost - time.Second), next: p2}}
		queued, replaced := s.orch.queueLearn("learn-1", p1)
		require.False(s.T(), queued)
		require.Same(s.T(), p2, replaced, "the pass waiting behind the lost one is replaced too")
		require.Nil(s.T(), s.orch.learnSlots["learn-1"].next)
	})
}

// TestMaybeLearnSupersedesWaitingPass covers a run finishing while a pass
// waits for the busy learn thread: the new pass takes its place, and the
// waiting one's row is marked superseded.
func (s *OrchestratorSuite) TestMaybeLearnSupersedesWaitingPass() {
	s.setupLearnOn()
	eb := new(MockEventBroadcaster)
	eb.On("BroadcastLearnPass", mock.Anything).Return()
	s.orch.SetEventBroadcaster(eb)
	waiting := &db.LearnPass{ID: 3, ChannelID: "ch1", MessageID: "b0", Status: db.LearnPassQueued}
	s.orch.learnSlots = map[string]*learnSlot{"learn-1": {triggered: true, triggeredAt: s.orch.timeNow(), next: &learnPass{row: waiting}}}

	ch := &db.Channel{ChannelID: "ch1", DirPath: "/project", Platform: types.PlatformLocal}
	s.store.On("GetChannel", s.ctx, "ch1").Return(ch, nil)
	s.store.On("GetHiddenThread", s.ctx, "ch1", db.ChannelKindLearn).Return(&db.Channel{ChannelID: "learn-1", Name: "learn: "}, nil)
	s.store.On("LastBotMessage", s.ctx, "ch1", "u1").Return(&db.Message{MsgID: "b1"}, nil)
	row := &db.LearnPass{ID: 4, ChannelID: "ch1", MessageID: "b1", Status: db.LearnPassQueued}
	s.store.On("InsertLearnPass", s.ctx, mock.Anything).Return(row, nil)
	s.store.On("UpdateLearnPass", s.ctx, int64(3), db.LearnPassSuperseded, "").Return(nil)

	s.orch.maybeLearn(s.ctx, ch, &bot.IncomingMessage{MessageID: "u1"}, &agent.AgentResponse{SessionID: "sess-1", NumTurns: 5})

	s.store.AssertExpectations(s.T())
	require.Equal(s.T(), db.LearnPassSuperseded, waiting.Status)
	eb.AssertCalled(s.T(), "BroadcastLearnPass", waiting)
	eb.AssertCalled(s.T(), "BroadcastLearnPass", row)
	next := s.orch.learnSlots["learn-1"].next
	require.Same(s.T(), row, next.row)
	require.Equal(s.T(), "b1", next.messageID)
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

			req, _, _, err := s.orch.prepareAgentRequest(s.ctx, &bot.IncomingMessage{ChannelID: "learn-1", AuthorName: learnAuthorName, Content: "review"})
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
			req, _, _, err := s.orch.prepareAgentRequest(s.ctx, &bot.IncomingMessage{ChannelID: "learn-1", AuthorName: "radu", Content: "why?"})
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
			require.Empty(s.T(), s.orch.learnSlots)
		})
	}
}

// TestLearnTurnStarts covers a pass the user asked for with learning off:
// it's recorded against the turn, forks the channel's current session and
// is handed the turn's trigger message, which wakes the learn thread's
// queue.
func (s *OrchestratorSuite) TestLearnTurnStarts() {
	s.orch.cfg.Store(&config.Config{})
	var drains int
	s.orch.drainSpawn = func(func()) { drains++ }
	eb := new(MockEventBroadcaster)
	eb.On("BroadcastMessageCreated", mock.Anything, mock.Anything).Return()
	eb.On("BroadcastLearnStarted", "ch1", "learn-1").Return()
	eb.On("BroadcastLearnPass", mock.Anything).Return()
	s.orch.SetEventBroadcaster(eb)

	ch := &db.Channel{ChannelID: "ch1", GuildID: "g1", Name: "api", Platform: types.PlatformLocal, SessionID: "sess-9", LearnOverride: db.LearnOff}
	s.store.On("GetChatMessage", s.ctx, "ch1", "b1").Return(&db.Message{MsgID: "b1", IsBot: true, TriggerMsgID: "u1", Content: "Fixed the tests."}, nil)
	s.store.On("GetChatMessage", s.ctx, "ch1", "u1").Return(&db.Message{Content: "fix the tests"}, nil)
	s.store.On("ActiveLearnPass", s.ctx, "ch1", "b1").Return(nil, nil)
	s.store.On("GetHiddenThread", s.ctx, "ch1", db.ChannelKindLearn).Return(&db.Channel{ChannelID: "learn-1", GuildID: "g1", Name: "learn: api", Kind: db.ChannelKindLearn}, nil)
	var recorded *db.LearnPass
	row := &db.LearnPass{ID: 4, ChannelID: "ch1", MessageID: "b1", LearnChannelID: "learn-1", Status: db.LearnPassQueued}
	s.store.On("InsertLearnPass", s.ctx, mock.MatchedBy(func(p *db.LearnPass) bool {
		recorded = p
		return true
	})).Return(row, nil)
	s.store.On("MarkSessionForkPending", s.ctx, "learn-1", "sess-9").Return(true, nil)
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
	require.Equal(s.T(), learn.TurnTriggerMessage("api", "fix the tests", "Fixed the tests."), trigger.Content)
	require.True(s.T(), learn.IsTrigger(trigger.Content))
	require.Equal(s.T(), &db.LearnPass{ChannelID: "ch1", MessageID: "b1", LearnChannelID: "learn-1", TriggerMsgID: trigger.MsgID}, recorded)
	require.Equal(s.T(), 1, drains)
	s.store.AssertExpectations(s.T())
	eb.AssertCalled(s.T(), "BroadcastLearnPass", row)
	eb.AssertCalled(s.T(), "BroadcastLearnStarted", "ch1", "learn-1")
	require.True(s.T(), s.orch.learnSlots["learn-1"].triggered)
}

// TestLearnTurnQueuesBehindRunningPass covers a pass the user asked for
// while the learn thread is busy: it waits its turn in place of the waiting
// pass, which is marked superseded, and starts with the turn's trigger
// message once the thread is free. The turn's prompt failing to load
// leaves it out.
func (s *OrchestratorSuite) TestLearnTurnQueuesBehindRunningPass() {
	s.orch.drainSpawn = func(func()) {}
	eb := new(MockEventBroadcaster)
	eb.On("BroadcastLearnPass", mock.Anything).Return()
	eb.On("BroadcastMessageCreated", mock.Anything, mock.Anything).Return()
	eb.On("BroadcastLearnStarted", "ch1", "learn-1").Return()
	s.orch.SetEventBroadcaster(eb)
	waiting := &db.LearnPass{ID: 3, ChannelID: "ch1", MessageID: "b0", Status: db.LearnPassQueued}
	s.orch.learnSlots = map[string]*learnSlot{"learn-1": {triggered: true, triggeredAt: s.orch.timeNow(), next: &learnPass{row: waiting}}}

	ch := &db.Channel{ChannelID: "ch1", Name: "api", Platform: types.PlatformLocal, SessionID: "sess-9"}
	s.store.On("GetChatMessage", s.ctx, "ch1", "b1").Return(&db.Message{MsgID: "b1", IsBot: true, TriggerMsgID: "u1", Content: "Done."}, nil)
	s.store.On("GetChatMessage", s.ctx, "ch1", "u1").Return(nil, errors.New("db down"))
	s.store.On("ActiveLearnPass", s.ctx, "ch1", "b1").Return(nil, nil)
	s.store.On("GetHiddenThread", s.ctx, "ch1", db.ChannelKindLearn).Return(&db.Channel{ChannelID: "learn-1", Name: "learn: api"}, nil)
	row := &db.LearnPass{ID: 4, ChannelID: "ch1", MessageID: "b1", Status: db.LearnPassQueued}
	s.store.On("InsertLearnPass", s.ctx, mock.Anything).Return(row, nil)
	s.store.On("UpdateLearnPass", s.ctx, int64(3), db.LearnPassSuperseded, "").Return(nil)

	p, err := s.orch.LearnTurn(s.ctx, ch, "b1")

	require.NoError(s.T(), err)
	require.Same(s.T(), row, p)
	require.Equal(s.T(), db.LearnPassSuperseded, waiting.Status)
	eb.AssertCalled(s.T(), "BroadcastLearnPass", waiting)
	eb.AssertCalled(s.T(), "BroadcastLearnPass", row)
	next := s.orch.learnSlots["learn-1"].next
	require.Same(s.T(), row, next.row)
	s.store.AssertNotCalled(s.T(), "MarkSessionForkPending", mock.Anything, mock.Anything, mock.Anything)

	s.store.On("GetChannel", s.ctx, "learn-1").Return(&db.Channel{ID: 9, ChannelID: "learn-1", Platform: types.PlatformLocal, Kind: db.ChannelKindLearn}, nil)
	s.store.On("MarkSessionForkPending", s.ctx, "learn-1", "sess-9").Return(true, nil)
	s.store.On("IsChannelActive", s.ctx, "learn-1").Return(true, nil)
	s.store.On("InsertMessage", s.ctx, mock.MatchedBy(func(m *db.Message) bool {
		return m.AuthorID == learnAuthorID && m.Content == learn.TurnTriggerMessage("api", "", "Done.")
	})).Return(nil)

	s.orch.learnRunDone(s.ctx, "learn-1", learnAuthorID)

	s.store.AssertExpectations(s.T())
}
