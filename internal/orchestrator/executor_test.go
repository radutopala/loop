package orchestrator

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/db"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type TaskExecutorSuite struct {
	suite.Suite
	store    *MockStore
	bot      *MockBot
	runner   *MockRunner
	executor *TaskExecutor
	ctx      context.Context
}

func TestTaskExecutorSuite(t *testing.T) {
	suite.Run(t, new(TaskExecutorSuite))
}

func (s *TaskExecutorSuite) SetupTest() {
	s.store = new(MockStore)
	s.bot = new(MockBot)
	s.runner = new(MockRunner)
	s.ctx = context.Background()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s.executor = NewTaskExecutor(s.runner, s.bot, s.store, logger, 5*time.Minute)
}

func (s *TaskExecutorSuite) TestNew() {
	require.NotNil(s.T(), s.executor)
	require.NotNil(s.T(), s.executor.runner)
	require.NotNil(s.T(), s.executor.bot)
	require.NotNil(s.T(), s.executor.store)
	require.NotNil(s.T(), s.executor.logger)
}

func (s *TaskExecutorSuite) TestHappyPathWithSession() {
	task := &db.ScheduledTask{
		ID:        1,
		ChannelID: "ch1",
		Prompt:    "do stuff",
		Type:      db.TaskTypeCron,
		Schedule:  "0 * * * *",
	}

	// Expect notification message first
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(msg *OutgoingMessage) bool {
		return msg.ChannelID == "ch1" && msg.Content != "" && msg.Content != "done!"
	})).Return(nil).Once()

	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ChannelID: "ch1",
		SessionID: "existing-session",
		DirPath:   "/home/user/project",
	}, nil)
	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		return req.SessionID == "existing-session" &&
			req.ChannelID == "ch1" &&
			req.DirPath == "/home/user/project" &&
			len(req.Messages) == 1 &&
			req.Messages[0].Role == "user" &&
			req.Messages[0].Content == "do stuff"
	})).Return(&agent.AgentResponse{
		Response:  "done!",
		SessionID: "new-session",
	}, nil)
	s.store.On("UpdateSessionID", s.ctx, "ch1", "new-session").Return(nil)
	// Expect response message second
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(msg *OutgoingMessage) bool {
		return msg.ChannelID == "ch1" && msg.Content == "done!"
	})).Return(nil).Once()

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "done!", resp)

	s.store.AssertExpectations(s.T())
	s.runner.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *TaskExecutorSuite) TestHappyPathWithoutSession() {
	task := &db.ScheduledTask{
		ID:        2,
		ChannelID: "ch2",
		Prompt:    "hello",
		Type:      db.TaskTypeInterval,
		Schedule:  "5m",
	}

	// Expect notification message first
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(msg *OutgoingMessage) bool {
		return msg.ChannelID == "ch2" && msg.Content != "" && msg.Content != "hi!"
	})).Return(nil).Once()

	s.store.On("GetChannel", s.ctx, "ch2").Return(nil, nil)
	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		return req.SessionID == "" && req.ChannelID == "ch2" && req.DirPath == ""
	})).Return(&agent.AgentResponse{
		Response:  "hi!",
		SessionID: "fresh-session",
	}, nil)
	s.store.On("UpdateSessionID", s.ctx, "ch2", "fresh-session").Return(nil)
	// Expect response message second
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(msg *OutgoingMessage) bool {
		return msg.ChannelID == "ch2" && msg.Content == "hi!"
	})).Return(nil).Once()

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "hi!", resp)

	s.store.AssertExpectations(s.T())
}

func (s *TaskExecutorSuite) TestRunnerError() {
	task := &db.ScheduledTask{
		ID:        3,
		ChannelID: "ch3",
		Prompt:    "fail",
		Type:      db.TaskTypeOnce,
		Schedule:  "10s",
	}

	// Expect notification message to be sent even though runner will fail
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(msg *OutgoingMessage) bool {
		return msg.ChannelID == "ch3"
	})).Return(nil).Once()

	s.store.On("GetChannel", s.ctx, "ch3").Return(nil, nil)
	s.runner.On("Run", mock.Anything, mock.Anything).Return(nil, errors.New("runner broke"))

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "running agent")
	require.Empty(s.T(), resp)

	s.bot.AssertExpectations(s.T())
}

func (s *TaskExecutorSuite) TestAgentResponseError() {
	task := &db.ScheduledTask{
		ID:        4,
		ChannelID: "ch4",
		Prompt:    "error",
		Type:      db.TaskTypeCron,
		Schedule:  "*/5 * * * *",
	}

	// Expect notification message to be sent even though agent will return error
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(msg *OutgoingMessage) bool {
		return msg.ChannelID == "ch4"
	})).Return(nil).Once()

	s.store.On("GetChannel", s.ctx, "ch4").Return(nil, nil)
	s.runner.On("Run", mock.Anything, mock.Anything).Return(&agent.AgentResponse{
		Error: "agent broke",
	}, nil)

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "agent error: agent broke")
	require.Empty(s.T(), resp)

	s.store.AssertNotCalled(s.T(), "UpdateSessionID", mock.Anything, mock.Anything, mock.Anything)
	s.bot.AssertExpectations(s.T())
}

func (s *TaskExecutorSuite) TestSessionUpsertErrorStillSucceeds() {
	task := &db.ScheduledTask{
		ID:        5,
		ChannelID: "ch5",
		Prompt:    "test",
		Type:      db.TaskTypeInterval,
		Schedule:  "1h",
	}

	// Expect notification message first
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(msg *OutgoingMessage) bool {
		return msg.ChannelID == "ch5" && msg.Content != "ok"
	})).Return(nil).Once()

	s.store.On("GetChannel", s.ctx, "ch5").Return(nil, nil)
	s.runner.On("Run", mock.Anything, mock.Anything).Return(&agent.AgentResponse{
		Response:  "ok",
		SessionID: "sess",
	}, nil)
	s.store.On("UpdateSessionID", s.ctx, mock.Anything, mock.Anything).Return(errors.New("upsert failed"))
	// Expect response message second
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(msg *OutgoingMessage) bool {
		return msg.ChannelID == "ch5" && msg.Content == "ok"
	})).Return(nil).Once()

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "ok", resp)

	s.store.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *TaskExecutorSuite) TestBotSendErrorStillSucceeds() {
	task := &db.ScheduledTask{
		ID:        6,
		ChannelID: "ch6",
		Prompt:    "test",
		Type:      db.TaskTypeOnce,
		Schedule:  "30s",
	}

	// Both notification and response messages may fail
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(errors.New("send failed"))

	s.store.On("GetChannel", s.ctx, "ch6").Return(nil, nil)
	s.runner.On("Run", mock.Anything, mock.Anything).Return(&agent.AgentResponse{
		Response:  "ok",
		SessionID: "sess",
	}, nil)
	s.store.On("UpdateSessionID", s.ctx, mock.Anything, mock.Anything).Return(nil)

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "ok", resp)

	s.bot.AssertExpectations(s.T())
}

func (s *TaskExecutorSuite) TestGetSessionErrorStillWorks() {
	task := &db.ScheduledTask{
		ID:        7,
		ChannelID: "ch7",
		Prompt:    "test",
		Type:      db.TaskTypeCron,
		Schedule:  "0 0 * * *",
	}

	// Expect notification message first
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(msg *OutgoingMessage) bool {
		return msg.ChannelID == "ch7" && msg.Content != "ok"
	})).Return(nil).Once()

	s.store.On("GetChannel", s.ctx, "ch7").Return(nil, errors.New("session err"))
	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		return req.SessionID == ""
	})).Return(&agent.AgentResponse{
		Response:  "ok",
		SessionID: "sess",
	}, nil)
	s.store.On("UpdateSessionID", s.ctx, mock.Anything, mock.Anything).Return(nil)
	// Expect response message second
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(msg *OutgoingMessage) bool {
		return msg.ChannelID == "ch7" && msg.Content == "ok"
	})).Return(nil).Once()

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "ok", resp)

	s.store.AssertExpectations(s.T())
}

func (s *TaskExecutorSuite) TestNotificationFailureDoesNotStopExecution() {
	task := &db.ScheduledTask{
		ID:        8,
		ChannelID: "ch8",
		Prompt:    "task prompt",
		Type:      db.TaskTypeInterval,
		Schedule:  "15m",
	}

	// Notification fails but execution continues
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(msg *OutgoingMessage) bool {
		return msg.ChannelID == "ch8" && msg.Content != "success"
	})).Return(errors.New("notification failed")).Once()

	s.store.On("GetChannel", s.ctx, "ch8").Return(nil, nil)
	s.runner.On("Run", mock.Anything, mock.Anything).Return(&agent.AgentResponse{
		Response:  "success",
		SessionID: "sess8",
	}, nil)
	s.store.On("UpdateSessionID", s.ctx, "ch8", "sess8").Return(nil)
	// Response message should still be sent
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(msg *OutgoingMessage) bool {
		return msg.ChannelID == "ch8" && msg.Content == "success"
	})).Return(nil).Once()

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "success", resp)

	s.runner.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}
