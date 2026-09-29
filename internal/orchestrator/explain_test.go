package orchestrator

import (
	"cmp"
	"errors"
	"strings"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/bot"
	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/explain"
	"github.com/radutopala/loop/internal/types"
)

func (s *OrchestratorSuite) TestExplainSkipReason() {
	on := config.ExplainConfig{Enabled: true}
	local := types.PlatformLocal
	tests := []struct {
		name   string
		ch     *db.Channel
		cfg    config.ExplainConfig
		parked bool
		want   string
	}{
		{"explains", &db.Channel{Platform: local, SessionID: "s"}, on, false, ""},
		{"override on beats config off", &db.Channel{Platform: local, SessionID: "s", ExplainOverride: db.LearnOn}, config.ExplainConfig{}, false, ""},
		{"slack channel", &db.Channel{Platform: types.PlatformSlack, SessionID: "s"}, on, false, "explain runs only in desktop app channels"},
		{"task thread", &db.Channel{Platform: local, SessionID: "s", TaskID: 7}, on, false, "explain doesn't run in task threads"},
		{"learn thread", &db.Channel{Platform: local, SessionID: "s", Kind: db.ChannelKindLearn}, on, false, "explain doesn't run in learn or explain threads"},
		{"parked", &db.Channel{Platform: local, SessionID: "s"}, on, true, "parked on a plan or question"},
		{"override off", &db.Channel{Platform: local, SessionID: "s", ExplainOverride: db.LearnOff}, on, false, "explain off"},
		{"config off", &db.Channel{Platform: local, SessionID: "s"}, config.ExplainConfig{}, false, "explain off"},
		{"no session", &db.Channel{Platform: local}, on, false, "no session"},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			require.Equal(s.T(), tc.want, explainSkipReason(tc.ch, tc.cfg, tc.parked))
		})
	}
}

// TestExplain covers what Explain does with a turn: returns its existing
// explanation, refuses what it can't explain, or queues a new one in the
// explain thread and wakes that thread's queue with the trigger message.
func (s *OrchestratorSuite) TestExplain() {
	ch := &db.Channel{ChannelID: "ch1", GuildID: "g1", Name: "api", DirPath: "/project", Platform: types.PlatformLocal, SessionID: "sess-1"}
	reply := &db.Message{ID: 42, MsgID: "b1", IsBot: true, TriggerMsgID: "u1", Content: "Fixed the tests."}
	thread := &db.Channel{ChannelID: "explain-1", GuildID: "g1", Name: "explain: api", Kind: db.ChannelKindExplain}
	existing := &db.Explanation{ID: 7, ChannelID: "ch1", MessageID: "b1", Status: db.ExplainDone}
	dbErr := errors.New("db down")
	queued := func(q bool) func() {
		return func() {
			s.store.On("GetChatMessage", s.ctx, "ch1", "b1").Return(reply, nil)
			s.store.On("GetChatMessage", s.ctx, "ch1", "u1").Return(&db.Message{Content: "fix the tests"}, nil)
			s.store.On("GetHiddenThread", s.ctx, "ch1", db.ChannelKindExplain).Return(thread, nil)
			s.store.On("QueueExplanation", s.ctx, mock.Anything).Return(&db.Explanation{ID: 8, ChannelID: "ch1", MessageID: "b1", Status: db.ExplainQueued}, q, nil)
		}
	}
	tests := []struct {
		name        string
		ch          *db.Channel
		force       bool
		setup       func()
		wantErr     error
		wantErrText string
		wantID      int64
		wantTrigger string // the trigger's content, "" for none
	}{
		{name: "unavailable", ch: &db.Channel{ChannelID: "ch1", Platform: types.PlatformSlack}, wantErr: explain.ErrUnavailable},
		{name: "existing", ch: ch, setup: func() {
			s.store.On("GetExplanation", s.ctx, "ch1", "b1").Return(existing, nil)
		}, wantID: 7},
		{name: "existing lookup fails", ch: ch, setup: func() {
			s.store.On("GetExplanation", s.ctx, "ch1", "b1").Return(nil, dbErr)
		}, wantErr: dbErr},
		{name: "message lookup fails", ch: ch, force: true, setup: func() {
			s.store.On("GetChatMessage", s.ctx, "ch1", "b1").Return(nil, dbErr)
		}, wantErr: dbErr},
		{name: "no such message", ch: ch, force: true, setup: func() {
			s.store.On("GetChatMessage", s.ctx, "ch1", "b1").Return(nil, nil)
		}, wantErr: explain.ErrNotATurn},
		{name: "a user message", ch: ch, force: true, setup: func() {
			s.store.On("GetChatMessage", s.ctx, "ch1", "b1").Return(&db.Message{MsgID: "b1"}, nil)
		}, wantErr: explain.ErrNotATurn},
		{name: "a notice outside a turn", ch: ch, force: true, setup: func() {
			s.store.On("GetChatMessage", s.ctx, "ch1", "b1").Return(&db.Message{MsgID: "b1", IsBot: true}, nil)
		}, wantErr: explain.ErrNotATurn},
		{name: "no session", ch: &db.Channel{ChannelID: "ch1", Platform: types.PlatformLocal}, force: true, setup: func() {
			s.store.On("GetChatMessage", s.ctx, "ch1", "b1").Return(reply, nil)
		}, wantErr: explain.ErrNoSession},
		{name: "no session but the turn's own", ch: &db.Channel{ChannelID: "ch1", Name: "api", Platform: types.PlatformLocal}, force: true, setup: func() {
			s.store.On("GetChatMessage", s.ctx, "ch1", "b1").Return(&db.Message{ID: 42, MsgID: "b1", IsBot: true, TriggerMsgID: "u1", Content: "Fixed the tests.",
				SessionID: "sess-0", TranscriptUUID: "uuid-1"}, nil)
			s.store.On("GetChatMessage", s.ctx, "ch1", "u1").Return(&db.Message{Content: "fix the tests"}, nil)
			s.store.On("GetHiddenThread", s.ctx, "ch1", db.ChannelKindExplain).Return(thread, nil)
			s.store.On("QueueExplanation", s.ctx, mock.Anything).Return(&db.Explanation{ID: 8, ChannelID: "ch1", MessageID: "b1", Status: db.ExplainQueued}, true, nil)
		}, wantID: 8, wantTrigger: explain.TriggerMessage("api", "fix the tests", "Fixed the tests.")},
		{name: "explain thread fails", ch: ch, force: true, setup: func() {
			s.store.On("GetChatMessage", s.ctx, "ch1", "b1").Return(reply, nil)
			s.store.On("GetChatMessage", s.ctx, "ch1", "u1").Return(nil, nil)
			s.store.On("GetHiddenThread", s.ctx, "ch1", db.ChannelKindExplain).Return(nil, dbErr)
		}, wantErr: dbErr},
		{name: "queueing fails", ch: ch, force: true, setup: func() {
			s.store.On("GetChatMessage", s.ctx, "ch1", "b1").Return(reply, nil)
			s.store.On("GetChatMessage", s.ctx, "ch1", "u1").Return(nil, nil)
			s.store.On("GetHiddenThread", s.ctx, "ch1", db.ChannelKindExplain).Return(thread, nil)
			s.store.On("QueueExplanation", s.ctx, mock.Anything).Return(nil, false, dbErr)
		}, wantErr: dbErr},
		{name: "already queued or running", ch: ch, force: true, setup: queued(false), wantID: 8},
		{name: "queues a new one", ch: ch, setup: func() {
			s.store.On("GetExplanation", s.ctx, "ch1", "b1").Return(nil, nil)
			queued(true)()
		}, wantID: 8, wantTrigger: explain.TriggerMessage("api", "fix the tests", "Fixed the tests.")},
		{name: "re-explains without the prompt", ch: ch, force: true, setup: func() {
			s.store.On("GetChatMessage", s.ctx, "ch1", "b1").Return(reply, nil)
			s.store.On("GetChatMessage", s.ctx, "ch1", "u1").Return(nil, dbErr)
			s.store.On("GetHiddenThread", s.ctx, "ch1", db.ChannelKindExplain).Return(thread, nil)
			s.store.On("QueueExplanation", s.ctx, mock.Anything).Return(&db.Explanation{ID: 8, ChannelID: "ch1", MessageID: "b1"}, true, nil)
		}, wantID: 8, wantTrigger: explain.TriggerMessage("api", "", "Fixed the tests.")},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			var drains int
			s.orch.drainSpawn = func(func()) { drains++ }
			eb := new(MockEventBroadcaster)
			eb.On("BroadcastMessageCreated", mock.Anything, mock.Anything).Return()
			eb.On("BroadcastExplainUpdated", mock.Anything).Return()
			s.orch.SetEventBroadcaster(eb)
			s.store.On("IsChannelActive", s.ctx, "explain-1").Return(true, nil)
			s.store.On("GetChannel", s.ctx, "explain-1").Return(&db.Channel{ID: 9, ChannelID: "explain-1", Kind: db.ChannelKindExplain}, nil)
			var trigger *db.Message
			s.store.On("InsertMessage", s.ctx, mock.MatchedBy(func(m *db.Message) bool {
				trigger = m
				return !m.IsBot
			})).Return(nil)
			if tc.setup != nil {
				tc.setup()
			}

			e, err := s.orch.Explain(s.ctx, tc.ch, "b1", tc.force)

			if tc.wantErr != nil {
				require.ErrorIs(s.T(), err, tc.wantErr)
				require.Nil(s.T(), e)
				return
			}
			require.NoError(s.T(), err)
			require.Equal(s.T(), tc.wantID, e.ID)
			if tc.wantTrigger == "" {
				require.Nil(s.T(), trigger)
				eb.AssertNotCalled(s.T(), "BroadcastExplainUpdated", mock.Anything)
				return
			}
			require.Equal(s.T(), int64(42), e.MessageRowID)
			require.Equal(s.T(), "Fixed the tests.", e.Reply)
			require.NotNil(s.T(), trigger)
			require.Equal(s.T(), "explain-1", trigger.ChannelID)
			require.True(s.T(), trigger.IsTriggered)
			require.Equal(s.T(), explainAuthorID, trigger.AuthorID)
			require.Equal(s.T(), tc.wantTrigger, trigger.Content)
			require.Equal(s.T(), 1, drains)
			// The queued row points at the trigger, which is how its run
			// finds it.
			queuedWith := s.store.Calls[len(s.store.Calls)-1]
			for _, c := range s.store.Calls {
				if c.Method == "QueueExplanation" {
					queuedWith = c
				}
			}
			q := queuedWith.Arguments.Get(1).(*db.Explanation)
			require.Equal(s.T(), "explain-1", q.ExplainChannelID)
			require.Equal(s.T(), trigger.MsgID, q.TriggerMsgID)
			require.True(s.T(), strings.HasPrefix(q.TriggerMsgID, "ask-"))
			eb.AssertCalled(s.T(), "BroadcastExplainUpdated", e)
		})
	}
}

// TestEnsureExplainThreadCreates checks the explain thread is made like a
// learn thread: hidden, under the channel, in its directory.
func (s *OrchestratorSuite) TestEnsureExplainThreadCreates() {
	ch := &db.Channel{ChannelID: "ch1", GuildID: "g1", Name: "api", DirPath: "/project"}
	var created *db.Channel
	s.store.On("GetHiddenThread", s.ctx, "ch1", db.ChannelKindExplain).Return(nil, nil)
	s.store.On("InsertHiddenThread", s.ctx, mock.MatchedBy(func(c *db.Channel) bool {
		created = c
		return true
	})).Return(nil)

	x, err := s.orch.ensureHiddenThread(s.ctx, ch, db.ChannelKindExplain)

	require.NoError(s.T(), err)
	require.Same(s.T(), created, x)
	require.True(s.T(), strings.HasPrefix(x.ChannelID, "explain-"))
	require.Equal(s.T(), "explain: api", x.Name)
	require.Equal(s.T(), "ch1", x.ParentID)
	require.Equal(s.T(), "/project", x.DirPath)
	require.Equal(s.T(), types.PlatformLocal, x.Platform)
	require.Equal(s.T(), db.ChannelKindExplain, x.Kind)
}

// TestMaybeExplain covers explaining a finished turn on its own: only when
// the reloaded channel's Explain switch is on and the turn has a reply.
func (s *OrchestratorSuite) TestMaybeExplain() {
	ch := &db.Channel{ChannelID: "ch1", Name: "api", Platform: types.PlatformLocal, SessionID: "sess-1"}
	on := &db.Channel{ChannelID: "ch1", Name: "api", Platform: types.PlatformLocal, SessionID: "sess-2", ExplainOverride: db.LearnOn}
	tests := []struct {
		name      string
		ch        *db.Channel
		setup     func()
		wantQueue bool
	}{
		{name: "hidden thread", ch: &db.Channel{ChannelID: "ch1", Kind: db.ChannelKindExplain}},
		{name: "channel gone", ch: ch, setup: func() {
			s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)
		}},
		{name: "explain off", ch: ch, setup: func() {
			s.store.On("GetChannel", s.ctx, "ch1").Return(ch, nil)
		}},
		{name: "no reply", ch: ch, setup: func() {
			s.store.On("GetChannel", s.ctx, "ch1").Return(on, nil)
			s.store.On("LastBotMessage", s.ctx, "ch1", "u1").Return(nil, nil)
		}},
		{name: "explain fails", ch: ch, setup: func() {
			s.store.On("GetChannel", s.ctx, "ch1").Return(on, nil)
			s.store.On("LastBotMessage", s.ctx, "ch1", "u1").Return(&db.Message{MsgID: "b2"}, nil)
			s.store.On("GetExplanation", s.ctx, "ch1", "b2").Return(nil, errors.New("db down"))
		}, wantQueue: true},
		{name: "explains the turn's last reply", ch: ch, setup: func() {
			s.store.On("GetChannel", s.ctx, "ch1").Return(on, nil)
			s.store.On("LastBotMessage", s.ctx, "ch1", "u1").Return(&db.Message{MsgID: "b2"}, nil)
			s.store.On("GetExplanation", s.ctx, "ch1", "b2").Return(&db.Explanation{ID: 1}, nil)
		}, wantQueue: true},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.orch.cfg.Store(&config.Config{})
			if tc.setup != nil {
				tc.setup()
			}
			s.orch.maybeExplain(s.ctx, tc.ch, &bot.IncomingMessage{ChannelID: "ch1", MessageID: "u1"})
			s.store.AssertExpectations(s.T())
			if !tc.wantQueue {
				s.store.AssertNotCalled(s.T(), "GetExplanation", mock.Anything, mock.Anything, mock.Anything)
			}
		})
	}
}

// TestPrepareAgentRequestExplainThread checks an explain thread's run: it
// forks read-only as the explain agent, cut where the explained turn ended
// when its reply records that, else the parent's whole current session; and
// nothing else runs there.
func (s *OrchestratorSuite) TestPrepareAgentRequestExplainThread() {
	thread := &db.Channel{ChannelID: "explain-1", ParentID: "ch1", DirPath: "/project", Kind: db.ChannelKindExplain, SessionID: "sess-old"}
	parent := &db.Channel{ChannelID: "ch1", Name: "api", DirPath: "/project", SessionID: "sess-parent", ModelOverride: "sonnet", EffortOverride: "low"}
	tests := []struct {
		name       string
		thread     *db.Channel
		authorID   string
		parent     *db.Channel
		parentErr  error
		cfg        config.ExplainConfig
		turn       *db.Message
		turnErr    error
		wantErr    string
		wantModel  string
		wantEffort string
		wantSess   string
		wantAt     string
	}{
		{name: "forks at the turn", thread: thread, authorID: explainAuthorID, parent: parent, wantModel: "sonnet", wantEffort: "low",
			turn: &db.Message{MsgID: "b1", SessionID: "sess-turn", TranscriptUUID: "uuid-1"}, wantSess: "sess-turn", wantAt: "uuid-1"},
		{name: "turn without a ref forks the whole session", thread: thread, authorID: explainAuthorID, parent: parent, wantModel: "sonnet", wantEffort: "low",
			turn: &db.Message{MsgID: "b1", SessionID: "sess-turn"}},
		{name: "turn lookup fails", thread: thread, authorID: explainAuthorID, parent: parent, wantModel: "sonnet", wantEffort: "low",
			turnErr: errors.New("db down")},
		{name: "turn's own session without the parent's", thread: thread, authorID: explainAuthorID, parent: &db.Channel{ChannelID: "ch1"},
			turn: &db.Message{MsgID: "b1", SessionID: "sess-turn", TranscriptUUID: "uuid-1"}, wantSess: "sess-turn", wantAt: "uuid-1"},
		{name: "explain model, effort and prompt", thread: thread, authorID: explainAuthorID, parent: parent,
			cfg: config.ExplainConfig{Model: "opus", Effort: "high", Prompt: "Mention the API."}, wantModel: "opus", wantEffort: "high"},
		{name: "falls back to the parent's overrides", thread: thread, authorID: explainAuthorID, parent: parent, wantModel: "sonnet", wantEffort: "low"},
		{name: "a user's message", thread: thread, authorID: "radu", parent: parent, wantErr: "explain thread explain-1 only runs explanations"},
		{name: "parent gone", thread: thread, authorID: explainAuthorID, wantErr: `explain thread explain-1: parent "ch1" not found`},
		{name: "parent lookup fails", thread: thread, authorID: explainAuthorID, parentErr: errors.New("db down"), wantErr: `explain thread explain-1: parent "ch1" not found`},
		{name: "parent without a session", thread: thread, authorID: explainAuthorID, parent: &db.Channel{ChannelID: "ch1"}, wantErr: explain.ErrNoSession.Error()},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.orch.cfg.Store(&config.Config{Explain: tc.cfg})
			s.orch.loadProjectConfig = func(_ string, main *config.Config) (*config.Config, error) {
				return main, nil
			}
			s.store.On("GetRecentMessages", s.ctx, "explain-1", recentMessageLimit).Return([]*db.Message{}, nil)
			s.store.On("GetChannel", s.ctx, "explain-1").Return(tc.thread, nil)
			s.store.On("GetChannel", s.ctx, "ch1").Return(tc.parent, tc.parentErr)
			s.store.On("GetChatMessage", s.ctx, "ch1", "b1").Return(tc.turn, tc.turnErr)

			req, _, _, err := s.orch.prepareAgentRequest(s.ctx, &bot.IncomingMessage{ChannelID: "explain-1", AuthorID: tc.authorID, AuthorName: "loop", Content: "explain"}, "b1")

			if tc.wantErr != "" {
				require.EqualError(s.T(), err, tc.wantErr)
				require.Nil(s.T(), req)
				return
			}
			require.NoError(s.T(), err)
			require.True(s.T(), req.ForkSession)
			require.Equal(s.T(), cmp.Or(tc.wantSess, "sess-parent"), req.SessionID)
			require.Equal(s.T(), tc.wantAt, req.ResumeAt)
			require.Equal(s.T(), explain.AgentID, req.AgentID)
			require.True(s.T(), req.ReadOnly)
			require.Equal(s.T(), tc.wantModel, req.Model)
			require.Equal(s.T(), tc.wantEffort, req.Effort)
			require.Equal(s.T(), explain.SystemPrompt(tc.cfg.Prompt), req.SystemPrompt)
		})
	}
}

// TestExplainRun covers an explain run's bookkeeping: its explanation is
// marked running when it starts, then done with the final reply or failed.
func (s *OrchestratorSuite) TestExplainRun() {
	msg := &bot.IncomingMessage{ChannelID: "explain-1", MessageID: "ask-1", AuthorID: explainAuthorID}
	tests := []struct {
		name        string
		msg         *bot.IncomingMessage
		lookup      *db.Explanation
		lookupErr   error
		updateErr   error
		content     string
		runErr      error
		wantUpdates [][3]string // status, content, error
	}{
		{name: "not an explain run", msg: &bot.IncomingMessage{ChannelID: "ch1", AuthorID: "radu"}},
		{name: "explanation gone", msg: msg},
		{name: "lookup fails", msg: msg, lookupErr: errors.New("db down")},
		{name: "done", msg: msg, lookup: &db.Explanation{ID: 3}, content: "## Summary",
			wantUpdates: [][3]string{{db.ExplainRunning, "", ""}, {db.ExplainDone, "## Summary", ""}}},
		{name: "empty reply fails", msg: msg, lookup: &db.Explanation{ID: 3}, content: "  ",
			wantUpdates: [][3]string{{db.ExplainRunning, "", ""}, {db.ExplainFailed, "", "the run ended without an explanation"}}},
		{name: "run fails", msg: msg, lookup: &db.Explanation{ID: 3}, runErr: errors.New("agent error: boom"),
			wantUpdates: [][3]string{{db.ExplainRunning, "", ""}, {db.ExplainFailed, "", "agent error: boom"}}},
		{name: "update fails", msg: msg, lookup: &db.Explanation{ID: 3}, updateErr: errors.New("db down"), content: "x",
			wantUpdates: [][3]string{{db.ExplainRunning, "", ""}, {db.ExplainDone, "x", ""}}},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			eb := new(MockEventBroadcaster)
			var broadcast []string
			eb.On("BroadcastExplainUpdated", mock.Anything).Run(func(args mock.Arguments) {
				broadcast = append(broadcast, args.Get(0).(*db.Explanation).Status)
			}).Return()
			s.orch.SetEventBroadcaster(eb)
			s.store.On("GetExplanationByTrigger", s.ctx, "explain-1", "ask-1").Return(tc.lookup, tc.lookupErr)
			var updates [][3]string
			s.store.On("UpdateExplanation", s.ctx, int64(3), mock.Anything, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
				updates = append(updates, [3]string{args.String(2), args.String(3), args.String(4)})
			}).Return(tc.updateErr)

			e := s.orch.explainRunStarted(s.ctx, tc.msg)
			s.orch.explainRunDone(s.ctx, e, tc.content, tc.runErr)

			require.Equal(s.T(), tc.wantUpdates, updates)
			if tc.updateErr != nil || tc.wantUpdates == nil {
				require.Empty(s.T(), broadcast)
				return
			}
			require.Equal(s.T(), []string{db.ExplainRunning, tc.wantUpdates[1][0]}, broadcast)
			require.Equal(s.T(), tc.wantUpdates[1][1], e.Content)
			require.Equal(s.T(), tc.wantUpdates[1][2], e.Error)
		})
	}
}

// TestHandleMessageExplainTriggerNeverAutoCreates covers an explanation's
// trigger whose explain thread was deleted meanwhile: it's dropped rather
// than auto-creating a plain channel where the run would be unrestricted.
func (s *OrchestratorSuite) TestHandleMessageExplainTriggerNeverAutoCreates() {
	s.store.On("IsChannelActive", s.ctx, "explain-1").Return(false, nil)
	s.bot.On("GetChannelParentID", s.ctx, "explain-1").Return("", nil)

	s.orch.HandleMessage(s.ctx, &bot.IncomingMessage{
		ChannelID: "explain-1",
		AuthorID:  explainAuthorID,
		Content:   "explain",
		HasPrefix: true,
		Platform:  types.PlatformLocal,
	})

	s.store.AssertNotCalled(s.T(), "UpsertChannel", mock.Anything, mock.Anything)
	s.store.AssertNotCalled(s.T(), "GetChannel", mock.Anything, mock.Anything)
}
