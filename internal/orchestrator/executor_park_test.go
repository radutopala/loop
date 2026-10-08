package orchestrator

import (
	"context"
	"errors"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/events"
)

type mockCardParker struct {
	mock.Mock
}

func (m *mockCardParker) markAskedChannel(ctx context.Context, channelID, mode string, data events.AskUserQuestionEventData) {
	m.Called(ctx, channelID, mode, data)
}

func (m *mockCardParker) markPlannedChannel(ctx context.Context, channelID string, data events.ExitPlanModeEventData) {
	m.Called(ctx, channelID, data)
}

func (s *TaskExecutorSuite) TestSetCardParker() {
	p := new(mockCardParker)
	s.executor.SetCardParker(p)
	require.Same(s.T(), p, s.executor.parker)
}

func (s *TaskExecutorSuite) TestTaskRunParksOnCard() {
	const (
		askInput  = `{"questions":[{"question":"What next?","header":"Task","options":[{"label":"A"}]}]}`
		planInput = `{"plan":"# My Plan\nDo stuff","planFilePath":"/tmp/plan.md"}`
	)
	isAsk := func(d events.AskUserQuestionEventData) bool {
		return d.ToolUseID == "toolu_1" && len(d.Questions) == 1 && d.Questions[0].Question == "What next?"
	}
	isPlan := func(d events.ExitPlanModeEventData) bool {
		return d.ToolUseID == "toolu_1" && d.Plan == "# My Plan\nDo stuff" && d.PlanFilePath == "/tmp/plan.md"
	}
	tests := []struct {
		name       string
		tool       string
		input      string
		noThread   bool
		noParker   bool
		session    string
		sessionErr error
		cardErr    error
	}{
		{name: "ask", tool: "AskUserQuestion", input: askInput, session: "sess-1"},
		{name: "plan", tool: "ExitPlanMode", input: planInput, session: "sess-1"},
		{name: "card goes to the channel without a thread", tool: "AskUserQuestion", input: askInput, noThread: true, session: "sess-1"},
		{name: "card still sent without a parker", tool: "AskUserQuestion", input: askInput, noParker: true, session: "sess-1"},
		{name: "ask card send error", tool: "AskUserQuestion", input: askInput, session: "sess-1", cardErr: errors.New("boom")},
		{name: "plan card send error", tool: "ExitPlanMode", input: planInput, session: "sess-1", cardErr: errors.New("boom")},
		{name: "no session reported", tool: "AskUserQuestion", input: askInput},
		{name: "session save error", tool: "AskUserQuestion", input: askInput, session: "sess-1", sessionErr: errors.New("db")},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			eb := new(MockEventBroadcaster)
			s.executor.SetEventBroadcaster(eb)
			parker := new(mockCardParker)
			if !tc.noParker {
				s.executor.SetCardParker(parker)
			}

			task := &db.ScheduledTask{ID: 30, ChannelID: "ch30", Prompt: "ask", Type: db.TaskTypeCron, Schedule: "0 * * * *"}
			target := "thread-30"
			s.store.On("GetChannel", mock.Anything, "ch30").Return(nil, nil)
			s.store.On("GetScheduledTask", s.ctx, int64(30)).Return(&db.ScheduledTask{ID: 30, Type: db.TaskTypeCron}, nil)
			if tc.noThread {
				target = "ch30"
				s.bot.On("CreateSimpleThread", mock.Anything, "ch30", mock.Anything, "").Return("", errors.New("no thread")).Once()
			} else {
				s.expectTaskThread(task, target, false, nil)
				eb.On("BroadcastChannelCreated", "ch30", target).Once()
			}
			s.store.On("InsertMessage", mock.Anything, mock.Anything).Return(nil).Maybe()
			eb.On("BroadcastToolUse", target, mock.Anything).Once()
			eb.On("BroadcastToolResult", target, mock.Anything).Once()
			eb.On("BroadcastAgentStatus", target, mock.MatchedBy(func(d events.AgentStatusEventData) bool {
				return d.Status == "completed"
			})).Once()
			eb.On("BroadcastAgentStatus", mock.Anything, mock.Anything).Maybe()

			if tc.tool == "AskUserQuestion" {
				parker.On("markAskedChannel", mock.Anything, target, "", mock.MatchedBy(isAsk)).Maybe()
				s.bot.On("SendAskCard", mock.Anything, target, "", mock.MatchedBy(isAsk)).Return(tc.cardErr).Once()
			} else {
				parker.On("markPlannedChannel", mock.Anything, target, mock.MatchedBy(isPlan)).Maybe()
				s.bot.On("SendPlanCard", mock.Anything, target, "", mock.MatchedBy(isPlan)).Return(tc.cardErr).Once()
			}
			if tc.session != "" {
				s.store.On("UpdateSessionID", s.ctx, target, tc.session).Return(tc.sessionErr).Once()
			}

			var cancelled error
			s.runner.On("Run", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
				ctx := args.Get(0).(context.Context)
				req := args.Get(1).(*agent.AgentRequest)
				if tc.session != "" {
					req.OnSession(tc.session)
				}
				req.OnToolUse("toolu_1", tc.tool, tc.input)
				req.OnToolResult("toolu_1", "denied", true)
				cancelled = ctx.Err()
			}).Return(nil, context.Canceled)

			resp, err := s.executor.ExecuteTask(s.ctx, task)
			require.NoError(s.T(), err)
			require.Empty(s.T(), resp)
			require.ErrorIs(s.T(), cancelled, context.Canceled)
			switch {
			case tc.noParker:
				parker.AssertNotCalled(s.T(), "markAskedChannel", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
			case tc.tool == "AskUserQuestion":
				parker.AssertCalled(s.T(), "markAskedChannel", mock.Anything, target, "", mock.Anything)
			default:
				parker.AssertCalled(s.T(), "markPlannedChannel", mock.Anything, target, mock.Anything)
			}
			if tc.session == "" {
				s.store.AssertNotCalled(s.T(), "UpdateSessionID", mock.Anything, mock.Anything, mock.Anything)
			}
			s.bot.AssertExpectations(s.T())
			s.store.AssertExpectations(s.T())
			eb.AssertExpectations(s.T())
		})
	}
}

// A run that ends on its own after showing a card (no cancel reached it)
// finishes like any other run, so its reply and session are kept.
func (s *TaskExecutorSuite) TestTaskRunEndsOnItsOwnAfterCard() {
	eb := new(MockEventBroadcaster)
	s.executor.SetEventBroadcaster(eb)
	allowStatusBroadcasts(eb)

	task := &db.ScheduledTask{ID: 31, ChannelID: "ch31", Prompt: "ask", Type: db.TaskTypeCron, Schedule: "0 * * * *"}
	s.store.On("GetChannel", mock.Anything, "ch31").Return(nil, nil)
	s.store.On("GetScheduledTask", s.ctx, int64(31)).Return(&db.ScheduledTask{ID: 31, Type: db.TaskTypeCron}, nil)
	s.expectTaskThread(task, "thread-31", false, nil)
	s.store.On("InsertMessage", mock.Anything, mock.Anything).Return(nil).Maybe()
	eb.On("BroadcastChannelCreated", "ch31", "thread-31").Once()
	eb.On("BroadcastToolUse", "thread-31", mock.Anything).Once()
	eb.On("BroadcastMessageCreated", "thread-31", mock.Anything).Maybe()
	s.bot.On("SendAskCard", mock.Anything, "thread-31", "", mock.Anything).Return(nil).Once()
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil).Once()
	s.store.On("UpdateSessionID", s.ctx, "thread-31", "sess-2").Return(nil).Once()
	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		req.OnToolUse("toolu_q", "AskUserQuestion", `{"questions":[{"question":"Q?","options":[{"label":"A"}]}]}`)
		return true
	})).Return(&agent.AgentResponse{Response: "Waiting.", SessionID: "sess-2"}, nil)

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "Waiting.", resp)
	s.bot.AssertExpectations(s.T())
	s.store.AssertExpectations(s.T())
}
