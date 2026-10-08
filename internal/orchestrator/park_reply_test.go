package orchestrator

import (
	"context"
	"errors"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/bot"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/events"
	"github.com/radutopala/loop/internal/types"
)

func (s *OrchestratorSuite) TestIsChatPlatform() {
	require.True(s.T(), isChatPlatform(types.PlatformSlack))
	require.True(s.T(), isChatPlatform(types.PlatformDiscord))
	require.False(s.T(), isChatPlatform(types.PlatformLocal))
	require.False(s.T(), isChatPlatform(""))
}

func (s *OrchestratorSuite) TestAskAnswerFromReply() {
	single := events.AskUserQuestionEventData{Questions: []events.AskUserQuestion{{
		Question: "Which one?", Options: []events.AskUserOption{{Label: "A"}, {Label: "B"}, {Label: "C"}},
	}}}
	multi := events.AskUserQuestionEventData{Questions: []events.AskUserQuestion{{
		Question: "Which ones?", MultiSelect: true, Options: []events.AskUserOption{{Label: "A"}, {Label: "B"}, {Label: "C"}},
	}}}
	two := events.AskUserQuestionEventData{Questions: []events.AskUserQuestion{
		{Question: "Q1", Options: []events.AskUserOption{{Label: "A"}}},
		{Question: "Q2", Options: []events.AskUserOption{{Label: "B"}}},
	}}
	tests := []struct {
		name  string
		data  events.AskUserQuestionEventData
		reply string
		want  string
	}{
		{"option number", single, "2", "Here are my answers:\n\nQ: Which one?\nA: B"},
		{"option numbers on multi-select", multi, "1, 3", "Here are my answers:\n\nQ: Which ones?\nA: A, C"},
		{"several numbers on single-select", single, "1 2", "1 2"},
		{"number out of range", single, "4", "4"},
		{"free text", single, "something else", "something else"},
		{"blank", single, " ", " "},
		{"several questions", two, "1", "1"},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			require.Equal(s.T(), tc.want, askAnswerFromReply(tc.data, tc.reply))
		})
	}
}

func (s *OrchestratorSuite) TestResolveParkFromReply() {
	askData := events.AskUserQuestionEventData{ToolUseID: "toolu_1", Questions: []events.AskUserQuestion{{
		Question: "Which one?", Options: []events.AskUserOption{{Label: "A"}, {Label: "B"}},
	}}}
	planData := events.ExitPlanModeEventData{ToolUseID: "toolu_1", Plan: "p"}
	tests := []struct {
		name        string
		park        func()
		reply       string
		prioErr     error
		closeErr    error
		wantHandled bool
		wantRun     bool
		wantContent string
		wantMode    string
		wantPrio    int
		wantOutcome string
	}{
		{
			name:  "not parked",
			park:  func() {},
			reply: "hi", wantContent: "hi",
		},
		{
			name:        "ask skipped",
			park:        func() { s.orch.markAskedChannel(s.ctx, "ch1", "plan", askData) },
			reply:       " Skip. ",
			wantHandled: true, wantContent: " Skip. ", wantOutcome: "Skipped",
		},
		{
			name:        "ask answered with an option number",
			park:        func() { s.orch.markAskedChannel(s.ctx, "ch1", "plan", askData) },
			reply:       "2",
			wantHandled: true, wantRun: true,
			wantContent: "Here are my answers:\n\nQ: Which one?\nA: B", wantMode: "plan", wantPrio: 4,
			wantOutcome: "B",
		},
		{
			name:        "ask answered when the queue priority lookup fails",
			park:        func() { s.orch.markAskedChannel(s.ctx, "ch1", "", askData) },
			reply:       "my own answer",
			prioErr:     errors.New("db down"),
			wantHandled: true, wantRun: true, wantContent: "my own answer", wantOutcome: "Answered",
		},
		{
			name:        "plan rejected",
			park:        func() { s.orch.markPlannedChannel(s.ctx, "ch1", planData) },
			reply:       "Reject",
			wantHandled: true, wantContent: "Reject", wantOutcome: "Rejected",
		},
		{
			name: "plan approved",
			park: func() {
				s.orch.markPlannedChannel(s.ctx, "ch1", events.ExitPlanModeEventData{ToolUseID: "toolu_1", Plan: "p", PlanFilePath: "/work/p.md"})
			},
			reply:       "approve!",
			wantHandled: true, wantRun: true,
			wantContent: events.PlanApprovePrompt("/work/p.md"), wantPrio: 4, wantOutcome: "Approved",
		},
		{
			name:        "plan changes requested, closing the card fails",
			park:        func() { s.orch.markPlannedChannel(s.ctx, "ch1", planData) },
			reply:       "split step 2",
			closeErr:    errors.New("boom"),
			wantHandled: true, wantRun: true,
			wantContent: "split step 2", wantMode: "plan", wantPrio: 4, wantOutcome: "Changes requested",
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.store.On("UpsertPausedChannel", mock.Anything, mock.Anything).Return(nil).Maybe()
			s.store.On("DeletePausedChannel", mock.Anything, "ch1", mock.Anything).Return(nil).Maybe()
			s.store.On("MaxQueuedPriority", s.ctx, "ch1").Return(3, tc.prioErr).Maybe()
			if tc.wantHandled {
				s.bot.On("CloseCard", s.ctx, "ch1", "toolu_1", tc.wantOutcome, "U1").Return(tc.closeErr).Once()
			}
			tc.park()

			msg := &bot.IncomingMessage{ChannelID: "ch1", AuthorID: "U1", Content: tc.reply}
			handled, run := s.orch.resolveParkFromReply(s.ctx, msg)

			require.Equal(s.T(), tc.wantHandled, handled)
			require.Equal(s.T(), tc.wantRun, run)
			require.Equal(s.T(), tc.wantContent, msg.Content)
			require.Equal(s.T(), tc.wantMode, msg.Mode)
			require.Equal(s.T(), tc.wantPrio, msg.Priority)
			require.False(s.T(), s.orch.IsChannelAsked("ch1"))
			require.False(s.T(), s.orch.IsChannelPlanned("ch1"))
			s.bot.AssertExpectations(s.T())
		})
	}
}

func (s *OrchestratorSuite) TestOpenCardID() {
	require.Empty(s.T(), s.orch.openCardID("ch1"))
	s.store.On("UpsertPausedChannel", mock.Anything, mock.Anything).Return(nil)
	s.orch.markPlannedChannel(s.ctx, "ch1", events.ExitPlanModeEventData{ToolUseID: "toolu_p", Plan: "p"})
	require.Equal(s.T(), "toolu_p", s.orch.openCardID("ch1"))
	s.orch.markAskedChannel(s.ctx, "ch2", "", events.AskUserQuestionEventData{ToolUseID: "toolu_a"})
	require.Equal(s.T(), "toolu_a", s.orch.openCardID("ch2"))
}

// TestHandleMessageReplyResolvesPark covers a Slack reply in a channel parked
// on an ask card: "skip" is stored as plain history and drains what was
// queued, while any other reply runs as the answer.
func (s *OrchestratorSuite) TestHandleMessageReplyResolvesPark() {
	askData := events.AskUserQuestionEventData{ToolUseID: "toolu_1", Questions: []events.AskUserQuestion{{Question: "Which one?"}}}
	tests := []struct {
		name          string
		reply         string
		cardID        string
		wantTriggered bool
	}{
		{"skip drops the card", "skip", "", false},
		{"reply answers the card", "the first", "", true},
		{"click answers its card", "skip", "toolu_1", false},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.store.On("UpsertPausedChannel", mock.Anything, mock.Anything).Return(nil)
			s.store.On("DeletePausedChannel", mock.Anything, "ch1", db.PausedKindAsk).Return(nil)
			s.store.On("MaxQueuedPriority", s.ctx, "ch1").Return(0, nil).Maybe()
			s.store.On("IsChannelActive", s.ctx, "ch1").Return(true, nil)
			s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{ID: 1, ChannelID: "ch1", Platform: types.PlatformSlack, Active: true}, nil)
			s.store.On("InsertMessage", s.ctx, mock.MatchedBy(func(m *db.Message) bool {
				return !m.IsBot && m.IsTriggered == tc.wantTriggered
			})).Return(nil).Once()
			s.orch.markAskedChannel(s.ctx, "ch1", "", askData)
			s.bot.On("CloseCard", s.ctx, "ch1", "toolu_1", mock.Anything, "U1").Return(nil).Once()

			var ran bool
			if tc.wantTriggered {
				s.bot.On("SendTyping", mock.Anything, "ch1").Return(nil).Maybe()
				s.bot.On("SendMessage", mock.Anything, mock.Anything).Return(nil).Maybe()
				s.store.On("GetRecentMessages", mock.Anything, "ch1", 50).Return([]*db.Message{}, nil)
				s.store.On("MarkMessagesProcessed", mock.Anything, mock.Anything).Return(nil).Maybe()
				s.store.On("UpdateSessionID", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
				s.runner.On("Run", mock.Anything, mock.Anything).Run(func(mock.Arguments) { ran = true }).
					Return(&agent.AgentResponse{Response: "ok"}, nil).Once()
			}

			s.orch.HandleMessage(s.ctx, &bot.IncomingMessage{
				ChannelID: "ch1", AuthorID: "U1", Content: tc.reply, MessageID: "m1",
				Platform: types.PlatformSlack, IsBotMention: true, Timestamp: time.Now().UTC(),
				CardID: tc.cardID,
			})
			s.orch.WaitDrains()

			require.False(s.T(), s.orch.IsChannelAsked("ch1"))
			require.Equal(s.T(), tc.wantTriggered, ran)
			s.store.AssertExpectations(s.T())
		})
	}
}

// TestHandleMessageCardClickRefused covers clicks that must not resolve the
// card: one by a user without access, and one on a card no longer open.
func (s *OrchestratorSuite) TestHandleMessageCardClickRefused() {
	restricted := types.Permissions{Owners: types.RoleGrant{Users: []string{"U2"}}}
	tests := []struct {
		name      string
		perms     types.Permissions
		cardID    string
		wantReply bool
	}{
		{name: "user without access", perms: restricted, cardID: "toolu_1"},
		{name: "card no longer open", cardID: "toolu_old", wantReply: true},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.store.On("UpsertPausedChannel", mock.Anything, mock.Anything).Return(nil)
			s.store.On("IsChannelActive", s.ctx, "ch1").Return(true, nil)
			s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
				ID: 1, ChannelID: "ch1", Platform: types.PlatformSlack, Active: true, Permissions: tc.perms,
			}, nil)
			if tc.wantReply {
				s.bot.On("SendMessage", s.ctx, &bot.OutgoingMessage{ChannelID: "ch1", Content: cardClosedReply}).Return(nil).Once()
			}
			s.orch.markAskedChannel(s.ctx, "ch1", "", events.AskUserQuestionEventData{ToolUseID: "toolu_1"})

			s.orch.HandleMessage(s.ctx, &bot.IncomingMessage{
				ChannelID: "ch1", AuthorID: "U1", Content: "1", Platform: types.PlatformSlack,
				IsBotMention: true, Timestamp: time.Now().UTC(), CardID: tc.cardID,
			})

			require.True(s.T(), s.orch.IsChannelAsked("ch1"))
			s.store.AssertNotCalled(s.T(), "InsertMessage", mock.Anything, mock.Anything)
			s.bot.AssertExpectations(s.T())
		})
	}
}

// TestHandleMessageStreamingParkSendsCard verifies a run that parks on an ask
// or plan sends the card through the bot, and that a failed send only logs.
func (s *OrchestratorSuite) TestHandleMessageStreamingParkSendsCard() {
	askData := events.AskUserQuestionEventData{ToolUseID: "toolu_x", Questions: []events.AskUserQuestion{{
		Question: "Pick one", Options: []events.AskUserOption{{Label: "X"}},
	}}}
	planData := events.ExitPlanModeEventData{ToolUseID: "toolu_x", Plan: "Step 1"}
	tests := []struct {
		name    string
		tool    string
		input   string
		method  string
		data    any
		sendErr error
	}{
		{name: "ask", tool: "AskUserQuestion", input: `{"questions":[{"question":"Pick one","options":[{"label":"X"}]}]}`, method: "SendAskCard", data: askData},
		{name: "ask send fails", tool: "AskUserQuestion", input: `{"questions":[{"question":"Pick one","options":[{"label":"X"}]}]}`, method: "SendAskCard", data: askData, sendErr: errors.New("boom")},
		{name: "plan", tool: "ExitPlanMode", input: `{"plan":"Step 1"}`, method: "SendPlanCard", data: planData},
		{name: "plan send fails", tool: "ExitPlanMode", input: `{"plan":"Step 1"}`, method: "SendPlanCard", data: planData, sendErr: errors.New("boom")},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			cfg := s.orch.cfg.Load()
			cfg.ContainerTimeout = time.Minute
			s.orch.cfg.Store(cfg)
			eb := new(MockEventBroadcaster)
			s.orch.SetEventBroadcaster(eb)
			eb.On("BroadcastMessageCreated", mock.Anything, mock.Anything).Return().Maybe()
			eb.On("BroadcastToolUse", mock.Anything, mock.Anything).Return().Maybe()
			eb.On("BroadcastToolResult", mock.Anything, mock.Anything).Return().Maybe()
			eb.On("BroadcastAgentStatus", mock.Anything, mock.Anything).Return().Maybe()

			channel := &db.Channel{ID: 1, ChannelID: "ch1", Platform: types.PlatformSlack, Active: true}
			s.store.On("IsChannelActive", s.ctx, "ch1").Return(true, nil)
			s.store.On("GetChannel", mock.Anything, "ch1").Return(channel, nil)
			s.store.On("InsertMessage", s.ctx, mock.Anything).Return(nil)
			s.store.On("InsertAgentEvent", mock.Anything, mock.Anything).Return(nil).Maybe()
			s.store.On("UpsertPausedChannel", mock.Anything, mock.Anything).Return(nil)
			s.store.On("GetRecentMessages", s.ctx, "ch1", 50).Return([]*db.Message{}, nil)
			s.store.On("MarkMessagesProcessed", mock.Anything, mock.Anything).Return(nil).Maybe()
			s.bot.On("SendTyping", mock.Anything, "ch1").Return(nil).Maybe()
			s.bot.On(tc.method, mock.Anything, "ch1", "m1", tc.data).Return(tc.sendErr).Once()
			s.bot.On("SendMessage", mock.Anything, mock.Anything).Return(nil).Maybe()

			s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
				if req.OnToolUse == nil || req.OnToolResult == nil {
					return false
				}
				req.OnToolUse("toolu_x", tc.tool, tc.input)
				req.OnToolResult("toolu_x", "denied", true)
				return true
			})).Return((*agent.AgentResponse)(nil), context.Canceled)

			s.orch.HandleMessage(s.ctx, &bot.IncomingMessage{
				ChannelID: "ch1", AuthorID: "U1", Content: "go", MessageID: "m1",
				Platform: types.PlatformSlack, IsBotMention: true, Timestamp: time.Now().UTC(),
			})

			s.bot.AssertExpectations(s.T())
		})
	}
}
