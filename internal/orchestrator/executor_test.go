package orchestrator

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/testutil"
)

type TaskExecutorSuite struct {
	suite.Suite
	store    *testutil.MockStore
	bot      *MockBot
	runner   *MockRunner
	executor *TaskExecutor
	ctx      context.Context
}

func TestTaskExecutorSuite(t *testing.T) {
	suite.Run(t, new(TaskExecutorSuite))
}

func (s *TaskExecutorSuite) SetupTest() {
	s.store = new(testutil.MockStore)
	s.bot = new(MockBot)
	s.runner = new(MockRunner)
	s.ctx = context.Background()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s.executor = NewTaskExecutor(s.runner, s.bot, s.store, logger, 5*time.Minute, false)
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

	s.store.On("GetChannel", s.ctx, "ch2").Return(nil, nil)
	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		return req.SessionID == "" && req.ChannelID == "ch2" && req.DirPath == ""
	})).Return(&agent.AgentResponse{
		Response:  "hi!",
		SessionID: "fresh-session",
	}, nil)
	s.store.On("UpdateSessionID", s.ctx, "ch2", "fresh-session").Return(nil)
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

	s.store.On("GetChannel", s.ctx, "ch3").Return(nil, nil)
	s.runner.On("Run", mock.Anything, mock.Anything).Return(nil, errors.New("runner broke"))

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "running agent")
	require.Empty(s.T(), resp)

	s.bot.AssertNotCalled(s.T(), "SendMessage", mock.Anything, mock.Anything)
}

func (s *TaskExecutorSuite) TestAgentResponseError() {
	task := &db.ScheduledTask{
		ID:        4,
		ChannelID: "ch4",
		Prompt:    "error",
		Type:      db.TaskTypeCron,
		Schedule:  "*/5 * * * *",
	}

	s.store.On("GetChannel", s.ctx, "ch4").Return(nil, nil)
	s.runner.On("Run", mock.Anything, mock.Anything).Return(&agent.AgentResponse{
		Error: "agent broke",
	}, nil)

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "agent error: agent broke")
	require.Empty(s.T(), resp)

	s.store.AssertNotCalled(s.T(), "UpdateSessionID", mock.Anything, mock.Anything, mock.Anything)
	s.bot.AssertNotCalled(s.T(), "SendMessage", mock.Anything, mock.Anything)
}

func (s *TaskExecutorSuite) TestSessionUpsertErrorStillSucceeds() {
	task := &db.ScheduledTask{
		ID:        5,
		ChannelID: "ch5",
		Prompt:    "test",
		Type:      db.TaskTypeInterval,
		Schedule:  "1h",
	}

	s.store.On("GetChannel", s.ctx, "ch5").Return(nil, nil)
	s.runner.On("Run", mock.Anything, mock.Anything).Return(&agent.AgentResponse{
		Response:  "ok",
		SessionID: "sess",
	}, nil)
	s.store.On("UpdateSessionID", s.ctx, mock.Anything, mock.Anything).Return(errors.New("upsert failed"))
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

	s.store.On("GetChannel", s.ctx, "ch7").Return(nil, errors.New("session err"))
	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		return req.SessionID == ""
	})).Return(&agent.AgentResponse{
		Response:  "ok",
		SessionID: "sess",
	}, nil)
	s.store.On("UpdateSessionID", s.ctx, mock.Anything, mock.Anything).Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(msg *OutgoingMessage) bool {
		return msg.ChannelID == "ch7" && msg.Content == "ok"
	})).Return(nil).Once()

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "ok", resp)

	s.store.AssertExpectations(s.T())
}

func (s *TaskExecutorSuite) TestStreamingCreatesThread() {
	s.executor.streamingEnabled = true

	task := &db.ScheduledTask{
		ID:        9,
		ChannelID: "ch9",
		Prompt:    "stream task",
		Type:      db.TaskTypeCron,
		Schedule:  "0 * * * *",
	}

	s.store.On("GetChannel", s.ctx, "ch9").Return(nil, nil)

	// First OnTurn creates a thread with the first turn text
	s.bot.On("CreateSimpleThread", s.ctx, "ch9", "🧵 task #9 (`0 * * * *`) stream task", "🧵 task #9 (`0 * * * *`) Intermediate").Return("thread-1", nil).Once()

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		if req.OnTurn == nil {
			return false
		}
		// Simulate streaming: first turn creates thread, empty skipped, second goes to thread
		req.OnTurn("Intermediate")
		req.OnTurn("") // empty text should be skipped
		req.OnTurn("Final answer")
		return true
	})).Return(&agent.AgentResponse{
		Response:  "Final answer", // Same as last OnTurn — final send skipped
		SessionID: "sess-stream",
	}, nil)

	s.store.On("UpdateSessionID", s.ctx, "ch9", "sess-stream").Return(nil)

	// Second OnTurn sends to thread
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(msg *OutgoingMessage) bool {
		return msg.ChannelID == "thread-1" && msg.Content == "Final answer"
	})).Return(nil).Once()

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "Final answer", resp)

	// 1 SendMessage call (second OnTurn to thread). Final skipped (duplicate).
	// First OnTurn goes via CreateSimpleThread, not SendMessage.
	s.bot.AssertNumberOfCalls(s.T(), "SendMessage", 1)
	s.bot.AssertNumberOfCalls(s.T(), "CreateSimpleThread", 1)
	s.runner.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *TaskExecutorSuite) TestStreamingDisabledNoOnTurn() {
	// streamingEnabled is false by default
	task := &db.ScheduledTask{
		ID:        10,
		ChannelID: "ch10",
		Prompt:    "no stream",
		Type:      db.TaskTypeCron,
		Schedule:  "0 * * * *",
	}

	s.store.On("GetChannel", s.ctx, "ch10").Return(nil, nil)

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		return req.OnTurn == nil
	})).Return(&agent.AgentResponse{
		Response:  "Result",
		SessionID: "sess-nostream",
	}, nil)

	s.store.On("UpdateSessionID", s.ctx, "ch10", "sess-nostream").Return(nil)

	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(msg *OutgoingMessage) bool {
		return msg.ChannelID == "ch10" && msg.Content == "Result"
	})).Return(nil).Once()

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "Result", resp)

	// 1 call: just the final response (no notification, no streaming)
	s.bot.AssertNumberOfCalls(s.T(), "SendMessage", 1)
	s.runner.AssertExpectations(s.T())
}

func (s *TaskExecutorSuite) TestStreamingFinalSentWhenDifferent() {
	s.executor.streamingEnabled = true

	task := &db.ScheduledTask{
		ID:        11,
		ChannelID: "ch11",
		Prompt:    "stream diff",
		Type:      db.TaskTypeInterval,
		Schedule:  "5m",
	}

	s.store.On("GetChannel", s.ctx, "ch11").Return(nil, nil)

	// First OnTurn creates thread
	s.bot.On("CreateSimpleThread", s.ctx, "ch11", "🧵 task #11 (`5m`) stream diff", "🧵 task #11 (`5m`) Intermediate").Return("thread-2", nil).Once()

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		if req.OnTurn == nil {
			return false
		}
		req.OnTurn("Intermediate")
		return true
	})).Return(&agent.AgentResponse{
		Response:  "Different final",
		SessionID: "sess-diff",
	}, nil)

	s.store.On("UpdateSessionID", s.ctx, "ch11", "sess-diff").Return(nil)

	// Final response (different from last streamed) goes to thread
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(msg *OutgoingMessage) bool {
		return msg.ChannelID == "thread-2" && msg.Content == "Different final"
	})).Return(nil).Once()

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "Different final", resp)

	// 1 SendMessage (final to thread) + 1 CreateSimpleThread
	s.bot.AssertNumberOfCalls(s.T(), "SendMessage", 1)
	s.bot.AssertNumberOfCalls(s.T(), "CreateSimpleThread", 1)
	s.runner.AssertExpectations(s.T())
}

func (s *TaskExecutorSuite) TestStreamingThreadCreationFailsFallsBack() {
	s.executor.streamingEnabled = true

	task := &db.ScheduledTask{
		ID:        12,
		ChannelID: "ch12",
		Prompt:    "fallback task",
		Type:      db.TaskTypeCron,
		Schedule:  "0 * * * *",
	}

	s.store.On("GetChannel", s.ctx, "ch12").Return(nil, nil)

	// Thread creation fails
	s.bot.On("CreateSimpleThread", s.ctx, "ch12", "🧵 task #12 (`0 * * * *`) fallback task", "🧵 task #12 (`0 * * * *`) Turn 1").Return("", errors.New("thread error")).Once()

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		if req.OnTurn == nil {
			return false
		}
		req.OnTurn("Turn 1")
		req.OnTurn("Turn 2")
		return true
	})).Return(&agent.AgentResponse{
		Response:  "Turn 2", // same as last OnTurn
		SessionID: "sess-fb",
	}, nil)

	s.store.On("UpdateSessionID", s.ctx, "ch12", "sess-fb").Return(nil)

	// Fallback: first turn goes to channel directly
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(msg *OutgoingMessage) bool {
		return msg.ChannelID == "ch12" && msg.Content == "Turn 1"
	})).Return(nil).Once()
	// Second turn also goes to channel (threadID never set)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(msg *OutgoingMessage) bool {
		return msg.ChannelID == "ch12" && msg.Content == "Turn 2"
	})).Return(nil).Once()

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "Turn 2", resp)

	// 2 SendMessage calls (both fallback to channel), final skipped (duplicate)
	s.bot.AssertNumberOfCalls(s.T(), "SendMessage", 2)
	s.bot.AssertExpectations(s.T())
}

func (s *TaskExecutorSuite) TestStreamingSingleTurnNoFinalDuplicate() {
	s.executor.streamingEnabled = true

	task := &db.ScheduledTask{
		ID:        13,
		ChannelID: "ch13",
		Prompt:    "single turn task",
		Type:      db.TaskTypeCron,
		Schedule:  "0 * * * *",
	}

	s.store.On("GetChannel", s.ctx, "ch13").Return(nil, nil)

	// Thread created for single turn
	s.bot.On("CreateSimpleThread", s.ctx, "ch13", "🧵 task #13 (`0 * * * *`) single turn task", "🧵 task #13 (`0 * * * *`) Only turn").Return("thread-3", nil).Once()

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		if req.OnTurn == nil {
			return false
		}
		req.OnTurn("Only turn")
		return true
	})).Return(&agent.AgentResponse{
		Response:  "Only turn", // Same as OnTurn — final skipped
		SessionID: "sess-single",
	}, nil)

	s.store.On("UpdateSessionID", s.ctx, "ch13", "sess-single").Return(nil)

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "Only turn", resp)

	// 0 SendMessage (final skipped, only turn went via CreateSimpleThread)
	s.bot.AssertNumberOfCalls(s.T(), "SendMessage", 0)
	s.bot.AssertNumberOfCalls(s.T(), "CreateSimpleThread", 1)
}

func (s *TaskExecutorSuite) TestNoStreamingNoTurns() {
	// streamingEnabled false, no OnTurn callbacks happen
	task := &db.ScheduledTask{
		ID:        14,
		ChannelID: "ch14",
		Prompt:    "direct response",
		Type:      db.TaskTypeOnce,
		Schedule:  "10s",
	}

	s.store.On("GetChannel", s.ctx, "ch14").Return(nil, nil)
	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		return req.OnTurn == nil
	})).Return(&agent.AgentResponse{
		Response:  "Direct result",
		SessionID: "sess-direct",
	}, nil)
	s.store.On("UpdateSessionID", s.ctx, "ch14", "sess-direct").Return(nil)

	// Final response goes to channel directly (no thread)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(msg *OutgoingMessage) bool {
		return msg.ChannelID == "ch14" && msg.Content == "Direct result"
	})).Return(nil).Once()

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "Direct result", resp)

	s.bot.AssertNumberOfCalls(s.T(), "SendMessage", 1)
	s.bot.AssertNotCalled(s.T(), "CreateSimpleThread", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func (s *TaskExecutorSuite) TestEphemeralInstructionInSystemPrompt() {
	task := &db.ScheduledTask{
		ID:            20,
		ChannelID:     "ch20",
		Prompt:        "check prompt",
		Type:          db.TaskTypeCron,
		Schedule:      "0 * * * *",
		AutoDeleteSec: 60,
	}

	s.store.On("GetChannel", s.ctx, "ch20").Return(nil, nil)

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		return strings.Contains(req.SystemPrompt, "[EPHEMERAL]")
	})).Return(&agent.AgentResponse{
		Response:  "ok",
		SessionID: "sess-prompt",
	}, nil)

	s.store.On("UpdateSessionID", s.ctx, "ch20", "sess-prompt").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil).Once()

	_, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	s.runner.AssertExpectations(s.T())
}

func (s *TaskExecutorSuite) TestNoEphemeralInstructionWhenAutoDeleteZero() {
	task := &db.ScheduledTask{
		ID:            21,
		ChannelID:     "ch21",
		Prompt:        "check prompt",
		Type:          db.TaskTypeCron,
		Schedule:      "0 * * * *",
		AutoDeleteSec: 0,
	}

	s.store.On("GetChannel", s.ctx, "ch21").Return(nil, nil)

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		return !strings.Contains(req.SystemPrompt, "[EPHEMERAL]")
	})).Return(&agent.AgentResponse{
		Response:  "ok",
		SessionID: "sess-prompt2",
	}, nil)

	s.store.On("UpdateSessionID", s.ctx, "ch21", "sess-prompt2").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil).Once()

	_, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	s.runner.AssertExpectations(s.T())
}

func (s *TaskExecutorSuite) TestAutoDeleteTimerFires() {
	s.executor.streamingEnabled = true

	task := &db.ScheduledTask{
		ID:            15,
		ChannelID:     "ch15",
		Prompt:        "auto-del task",
		Type:          db.TaskTypeCron,
		Schedule:      "0 * * * *",
		AutoDeleteSec: 60,
	}

	s.store.On("GetChannel", s.ctx, "ch15").Return(nil, nil)
	s.bot.On("CreateSimpleThread", s.ctx, "ch15", mock.Anything, mock.Anything).Return("thread-auto-del", nil).Once()

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		if req.OnTurn == nil {
			return false
		}
		req.OnTurn("[EPHEMERAL] Nothing to report")
		return true
	})).Return(&agent.AgentResponse{
		Response:  "[EPHEMERAL] Nothing to report",
		SessionID: "sess-auto-del",
	}, nil)

	s.store.On("UpdateSessionID", s.ctx, "ch15", "sess-auto-del").Return(nil)
	// [EPHEMERAL] is stripped before tracker records lastText, so IsDuplicate
	// returns true and no final SendMessage is needed.
	s.bot.On("RenameThread", s.ctx, "thread-auto-del", "💨 task #15 (`0 * * * *`) auto-del task").Return(nil).Once()
	s.bot.On("DeleteThread", mock.Anything, "thread-auto-del").Return(nil).Once()

	var capturedDelay time.Duration
	var callbackCalled bool
	origTimeAfterFunc := timeAfterFunc
	defer func() { timeAfterFunc = origTimeAfterFunc }()
	timeAfterFunc = func(d time.Duration, f func()) *time.Timer {
		capturedDelay = d
		callbackCalled = true
		f() // immediately invoke the callback
		return time.NewTimer(0)
	}

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "Nothing to report", resp)
	require.True(s.T(), callbackCalled, "timeAfterFunc should have been called")
	require.Equal(s.T(), 60*time.Second, capturedDelay)

	s.bot.AssertCalled(s.T(), "RenameThread", s.ctx, "thread-auto-del", "💨 task #15 (`0 * * * *`) auto-del task")
	s.bot.AssertCalled(s.T(), "DeleteThread", mock.Anything, "thread-auto-del")
	s.bot.AssertExpectations(s.T())
}

func (s *TaskExecutorSuite) TestAutoDeleteEphemeralSuffix() {
	s.executor.streamingEnabled = true

	task := &db.ScheduledTask{
		ID:            23,
		ChannelID:     "ch23",
		Prompt:        "suffix task",
		Type:          db.TaskTypeCron,
		Schedule:      "0 * * * *",
		AutoDeleteSec: 60,
	}

	s.store.On("GetChannel", s.ctx, "ch23").Return(nil, nil)
	s.bot.On("CreateSimpleThread", s.ctx, "ch23", mock.Anything, mock.Anything).Return("thread-suffix", nil).Once()

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		if req.OnTurn == nil {
			return false
		}
		req.OnTurn("Nothing to report.\n\n[EPHEMERAL]")
		return true
	})).Return(&agent.AgentResponse{
		Response:  "Nothing to report.\n\n[EPHEMERAL]",
		SessionID: "sess-suffix",
	}, nil)

	s.store.On("UpdateSessionID", s.ctx, "ch23", "sess-suffix").Return(nil)
	// IsDuplicate returns true — no final SendMessage
	s.bot.On("RenameThread", s.ctx, "thread-suffix", mock.Anything).Return(nil).Once()
	s.bot.On("DeleteThread", mock.Anything, "thread-suffix").Return(nil).Once()

	origTimeAfterFunc := timeAfterFunc
	defer func() { timeAfterFunc = origTimeAfterFunc }()
	timeAfterFunc = func(d time.Duration, f func()) *time.Timer {
		f()
		return time.NewTimer(0)
	}

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "Nothing to report.", resp)

	s.bot.AssertCalled(s.T(), "RenameThread", s.ctx, "thread-suffix", mock.Anything)
	s.bot.AssertCalled(s.T(), "DeleteThread", mock.Anything, "thread-suffix")
}

func (s *TaskExecutorSuite) TestAutoDeleteRenameThreadError() {
	s.executor.streamingEnabled = true

	task := &db.ScheduledTask{
		ID:            22,
		ChannelID:     "ch22",
		Prompt:        "rename-err task",
		Type:          db.TaskTypeCron,
		Schedule:      "0 * * * *",
		AutoDeleteSec: 60,
	}

	s.store.On("GetChannel", s.ctx, "ch22").Return(nil, nil)
	s.bot.On("CreateSimpleThread", s.ctx, "ch22", mock.Anything, mock.Anything).Return("thread-rename-err", nil).Once()

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		if req.OnTurn == nil {
			return false
		}
		req.OnTurn("[EPHEMERAL] Nothing")
		return true
	})).Return(&agent.AgentResponse{
		Response:  "[EPHEMERAL] Nothing",
		SessionID: "sess-rename-err",
	}, nil)

	s.store.On("UpdateSessionID", s.ctx, "ch22", "sess-rename-err").Return(nil)
	s.bot.On("RenameThread", s.ctx, "thread-rename-err", mock.Anything).Return(errors.New("rename failed")).Once()
	s.bot.On("DeleteThread", mock.Anything, "thread-rename-err").Return(nil).Once()

	origTimeAfterFunc := timeAfterFunc
	defer func() { timeAfterFunc = origTimeAfterFunc }()
	timeAfterFunc = func(d time.Duration, f func()) *time.Timer {
		f()
		return time.NewTimer(0)
	}

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "Nothing", resp)

	s.bot.AssertCalled(s.T(), "RenameThread", s.ctx, "thread-rename-err", mock.Anything)
	s.bot.AssertCalled(s.T(), "DeleteThread", mock.Anything, "thread-rename-err")
}

func (s *TaskExecutorSuite) TestAutoDeleteTimerDeleteThreadError() {
	s.executor.streamingEnabled = true

	task := &db.ScheduledTask{
		ID:            18,
		ChannelID:     "ch18",
		Prompt:        "auto-del-err task",
		Type:          db.TaskTypeCron,
		Schedule:      "0 * * * *",
		AutoDeleteSec: 90,
	}

	s.store.On("GetChannel", s.ctx, "ch18").Return(nil, nil)
	s.bot.On("CreateSimpleThread", s.ctx, "ch18", mock.Anything, mock.Anything).Return("thread-del-err", nil).Once()

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		if req.OnTurn == nil {
			return false
		}
		req.OnTurn("[EPHEMERAL] Nothing")
		return true
	})).Return(&agent.AgentResponse{
		Response:  "[EPHEMERAL] Nothing",
		SessionID: "sess-del-err",
	}, nil)

	s.store.On("UpdateSessionID", s.ctx, "ch18", "sess-del-err").Return(nil)
	s.bot.On("RenameThread", s.ctx, "thread-del-err", mock.Anything).Return(nil).Once()
	s.bot.On("DeleteThread", mock.Anything, "thread-del-err").Return(errors.New("delete failed")).Once()

	var capturedDelay time.Duration
	var callbackCalled bool
	origTimeAfterFunc := timeAfterFunc
	defer func() { timeAfterFunc = origTimeAfterFunc }()
	timeAfterFunc = func(d time.Duration, f func()) *time.Timer {
		capturedDelay = d
		callbackCalled = true
		f() // immediately invoke the callback
		return time.NewTimer(0)
	}

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "Nothing", resp)
	require.True(s.T(), callbackCalled, "timeAfterFunc should have been called")
	require.Equal(s.T(), 90*time.Second, capturedDelay)

	s.bot.AssertCalled(s.T(), "DeleteThread", mock.Anything, "thread-del-err")
	s.bot.AssertExpectations(s.T())
}

func (s *TaskExecutorSuite) TestAutoDeleteSkippedWhenNotEphemeral() {
	s.executor.streamingEnabled = true

	task := &db.ScheduledTask{
		ID:            19,
		ChannelID:     "ch19",
		Prompt:        "important task",
		Type:          db.TaskTypeCron,
		Schedule:      "0 * * * *",
		AutoDeleteSec: 60,
	}

	s.store.On("GetChannel", s.ctx, "ch19").Return(nil, nil)
	s.bot.On("CreateSimpleThread", s.ctx, "ch19", mock.Anything, mock.Anything).Return("thread-keep", nil).Once()

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		if req.OnTurn == nil {
			return false
		}
		req.OnTurn("Important result")
		return true
	})).Return(&agent.AgentResponse{
		Response:  "Important result",
		SessionID: "sess-keep",
	}, nil)

	s.store.On("UpdateSessionID", s.ctx, "ch19", "sess-keep").Return(nil)

	var timerCalled bool
	origTimeAfterFunc := timeAfterFunc
	defer func() { timeAfterFunc = origTimeAfterFunc }()
	timeAfterFunc = func(d time.Duration, f func()) *time.Timer {
		timerCalled = true
		return time.NewTimer(0)
	}

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "Important result", resp)
	require.False(s.T(), timerCalled, "timeAfterFunc should NOT have been called for non-ephemeral response")

	s.bot.AssertNotCalled(s.T(), "DeleteThread", mock.Anything, mock.Anything)
	s.bot.AssertNotCalled(s.T(), "RenameThread", mock.Anything, mock.Anything, mock.Anything)
}

func (s *TaskExecutorSuite) TestAutoDeleteSkippedWhenZero() {
	s.executor.streamingEnabled = true

	task := &db.ScheduledTask{
		ID:            16,
		ChannelID:     "ch16",
		Prompt:        "no-del task",
		Type:          db.TaskTypeCron,
		Schedule:      "0 * * * *",
		AutoDeleteSec: 0,
	}

	s.store.On("GetChannel", s.ctx, "ch16").Return(nil, nil)
	s.bot.On("CreateSimpleThread", s.ctx, "ch16", mock.Anything, mock.Anything).Return("thread-no-del", nil).Once()

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		if req.OnTurn == nil {
			return false
		}
		req.OnTurn("Turn 1")
		return true
	})).Return(&agent.AgentResponse{
		Response:  "Turn 1",
		SessionID: "sess-no-del",
	}, nil)

	s.store.On("UpdateSessionID", s.ctx, "ch16", "sess-no-del").Return(nil)

	var timerCalled bool
	origTimeAfterFunc := timeAfterFunc
	defer func() { timeAfterFunc = origTimeAfterFunc }()
	timeAfterFunc = func(d time.Duration, f func()) *time.Timer {
		timerCalled = true
		return time.NewTimer(0)
	}

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "Turn 1", resp)
	require.False(s.T(), timerCalled, "timeAfterFunc should NOT have been called when AutoDeleteSec is 0")

	s.bot.AssertNotCalled(s.T(), "DeleteThread", mock.Anything, mock.Anything)
}

func (s *TaskExecutorSuite) TestAutoDeleteSkippedWhenNoThread() {
	// streamingEnabled is false by default — no thread created
	task := &db.ScheduledTask{
		ID:            17,
		ChannelID:     "ch17",
		Prompt:        "no-thread task",
		Type:          db.TaskTypeCron,
		Schedule:      "0 * * * *",
		AutoDeleteSec: 120,
	}

	s.store.On("GetChannel", s.ctx, "ch17").Return(nil, nil)
	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		return req.OnTurn == nil
	})).Return(&agent.AgentResponse{
		Response:  "[EPHEMERAL] Result",
		SessionID: "sess-no-thread",
	}, nil)
	s.store.On("UpdateSessionID", s.ctx, "ch17", "sess-no-thread").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(msg *OutgoingMessage) bool {
		return msg.ChannelID == "ch17" && msg.Content == "Result"
	})).Return(nil).Once()

	var timerCalled bool
	origTimeAfterFunc := timeAfterFunc
	defer func() { timeAfterFunc = origTimeAfterFunc }()
	timeAfterFunc = func(d time.Duration, f func()) *time.Timer {
		timerCalled = true
		return time.NewTimer(0)
	}

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "Result", resp)
	require.False(s.T(), timerCalled, "timeAfterFunc should NOT have been called when no thread was created")

	s.bot.AssertNotCalled(s.T(), "DeleteThread", mock.Anything, mock.Anything)
	s.bot.AssertNotCalled(s.T(), "RenameThread", mock.Anything, mock.Anything, mock.Anything)
	s.bot.AssertNumberOfCalls(s.T(), "SendMessage", 1)
}
