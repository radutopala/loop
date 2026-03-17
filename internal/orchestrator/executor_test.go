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
	"github.com/radutopala/loop/internal/bot"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/events"
	"github.com/radutopala/loop/internal/testutil"
	"github.com/radutopala/loop/internal/types"
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

// allowBotInserts adds an InsertMessage expectation for bot messages from storeBotMessage.
func (s *TaskExecutorSuite) allowBotInserts() {
	s.store.On("InsertMessage", mock.Anything, mock.MatchedBy(func(msg *db.Message) bool {
		return msg.IsBot
	})).Return(nil).Maybe()
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
	s.allowBotInserts()
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
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(msg *bot.OutgoingMessage) bool {
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
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(msg *bot.OutgoingMessage) bool {
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

func (s *TaskExecutorSuite) TestSoftErrorsStillSucceed() {
	tests := []struct {
		name       string
		channelID  string
		setupMocks func()
	}{
		{
			name:      "session upsert error",
			channelID: "ch5",
			setupMocks: func() {
				s.store.On("GetChannel", s.ctx, "ch5").Return(nil, nil)
				s.store.On("UpdateSessionID", s.ctx, mock.Anything, mock.Anything).Return(errors.New("upsert failed"))
			},
		},
		{
			name:      "bot send error",
			channelID: "ch6",
			setupMocks: func() {
				s.store.On("GetChannel", s.ctx, "ch6").Return(nil, nil)
				s.store.On("UpdateSessionID", s.ctx, mock.Anything, mock.Anything).Return(nil)
				s.bot.On("SendMessage", s.ctx, mock.Anything).Return(errors.New("send failed"))
			},
		},
		{
			name:      "get session error",
			channelID: "ch7",
			setupMocks: func() {
				s.store.On("GetChannel", s.ctx, "ch7").Return(nil, errors.New("session err"))
				s.store.On("UpdateSessionID", s.ctx, mock.Anything, mock.Anything).Return(nil)
			},
		},
	}

	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			tc.setupMocks()
			s.runner.On("Run", mock.Anything, mock.Anything).Return(&agent.AgentResponse{
				Response: "ok", SessionID: "sess",
			}, nil)
			s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(msg *bot.OutgoingMessage) bool {
				return msg.ChannelID == tc.channelID && msg.Content == "ok"
			})).Return(nil).Maybe()

			resp, err := s.executor.ExecuteTask(s.ctx, &db.ScheduledTask{
				ID: 5, ChannelID: tc.channelID, Prompt: "test",
				Type: db.TaskTypeCron, Schedule: "0 * * * *",
			})
			require.NoError(s.T(), err)
			require.Equal(s.T(), "ok", resp)
		})
	}
}

func (s *TaskExecutorSuite) TestStreamingCreatesThread() {
	s.executor.streamingEnabled = true
	s.allowBotInserts()

	task := &db.ScheduledTask{
		ID:        9,
		ChannelID: "ch9",
		Prompt:    "stream task",
		Type:      db.TaskTypeCron,
		Schedule:  "0 * * * *",
	}

	s.store.On("GetChannel", mock.Anything, mock.Anything).Return(nil, nil)

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
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(msg *bot.OutgoingMessage) bool {
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
	s.store.On("GetChannel", s.ctx, "ch10").Return(nil, nil)
	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		return req.OnTurn == nil
	})).Return(&agent.AgentResponse{Response: "Result", SessionID: "sess-nostream"}, nil)
	s.store.On("UpdateSessionID", s.ctx, "ch10", "sess-nostream").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(msg *bot.OutgoingMessage) bool {
		return msg.ChannelID == "ch10" && msg.Content == "Result"
	})).Return(nil).Once()

	resp, err := s.executor.ExecuteTask(s.ctx, &db.ScheduledTask{
		ID: 10, ChannelID: "ch10", Prompt: "no stream",
		Type: db.TaskTypeCron, Schedule: "0 * * * *",
	})
	require.NoError(s.T(), err)
	require.Equal(s.T(), "Result", resp)
	s.bot.AssertNumberOfCalls(s.T(), "SendMessage", 1)
	s.runner.AssertExpectations(s.T())
}

func (s *TaskExecutorSuite) TestStreamingFinalSentWhenDifferent() {
	s.executor.streamingEnabled = true
	s.allowBotInserts()

	task := &db.ScheduledTask{
		ID:        11,
		ChannelID: "ch11",
		Prompt:    "stream diff",
		Type:      db.TaskTypeInterval,
		Schedule:  "5m",
	}

	s.store.On("GetChannel", mock.Anything, mock.Anything).Return(nil, nil)

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
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(msg *bot.OutgoingMessage) bool {
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
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(msg *bot.OutgoingMessage) bool {
		return msg.ChannelID == "ch12" && msg.Content == "Turn 1"
	})).Return(nil).Once()
	// Second turn also goes to channel (threadID never set)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(msg *bot.OutgoingMessage) bool {
		return msg.ChannelID == "ch12" && msg.Content == "Turn 2"
	})).Return(nil).Once()

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "Turn 2", resp)

	// 2 SendMessage calls (both fallback to channel), final skipped (duplicate)
	s.bot.AssertNumberOfCalls(s.T(), "SendMessage", 2)
	s.bot.AssertExpectations(s.T())
}

func (s *TaskExecutorSuite) TestStreamingSendMessageErrorIsLogged() {
	s.executor.streamingEnabled = true

	task := &db.ScheduledTask{
		ID:        14,
		ChannelID: "ch14",
		Prompt:    "send err task",
		Type:      db.TaskTypeCron,
		Schedule:  "0 * * * *",
	}

	s.store.On("GetChannel", s.ctx, "ch14").Return(nil, nil)

	// Thread creation fails → first turn falls back to channel, second turn hits else branch
	s.bot.On("CreateSimpleThread", s.ctx, "ch14", mock.Anything, mock.Anything).Return("", errors.New("thread error")).Once()

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		if req.OnTurn == nil {
			return false
		}
		req.OnTurn("Turn 1") // goes through CreateSimpleThread fallback
		req.OnTurn("Turn 2") // goes through else branch (SendMessage) which fails
		return true
	})).Return(&agent.AgentResponse{
		Response:  "Turn 2",
		SessionID: "sess-senderr",
	}, nil)

	s.store.On("UpdateSessionID", s.ctx, "ch14", "sess-senderr").Return(nil)

	// First SendMessage (fallback from thread creation failure) succeeds
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(msg *bot.OutgoingMessage) bool {
		return msg.ChannelID == "ch14" && msg.Content == "Turn 1"
	})).Return(nil).Once()
	// Second SendMessage (else branch) fails — error is logged, not fatal
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(msg *bot.OutgoingMessage) bool {
		return msg.ChannelID == "ch14" && msg.Content == "Turn 2"
	})).Return(errors.New("send failed")).Once()

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "Turn 2", resp)

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

func (s *TaskExecutorSuite) TestEphemeralInstructionInSystemPrompt() {
	tests := []struct {
		name       string
		delSec     int
		wantMarker bool
	}{
		{"included when auto-delete set", 60, true},
		{"excluded when auto-delete zero", 0, false},
	}

	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			chID := "ch-prompt"
			s.store.On("GetChannel", s.ctx, chID).Return(nil, nil)
			s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
				return strings.Contains(req.SystemPrompt, "[EPHEMERAL]") == tc.wantMarker
			})).Return(&agent.AgentResponse{Response: "ok", SessionID: "sess"}, nil)
			s.store.On("UpdateSessionID", s.ctx, chID, "sess").Return(nil)
			s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil).Once()

			_, err := s.executor.ExecuteTask(s.ctx, &db.ScheduledTask{
				ID: 20, ChannelID: chID, Prompt: "check prompt",
				Type: db.TaskTypeCron, Schedule: "0 * * * *", AutoDeleteSec: tc.delSec,
			})
			require.NoError(s.T(), err)
			s.runner.AssertExpectations(s.T())
		})
	}
}

func (s *TaskExecutorSuite) TestAutoDeleteTimerFires() {
	s.executor.streamingEnabled = true
	eb := new(MockEventBroadcaster)
	s.executor.SetEventBroadcaster(eb)

	task := &db.ScheduledTask{
		ID:            15,
		ChannelID:     "ch15",
		Prompt:        "auto-del task",
		Type:          db.TaskTypeCron,
		Schedule:      "0 * * * *",
		AutoDeleteSec: 60,
	}

	s.store.On("GetChannel", s.ctx, "ch15").Return(nil, nil)
	s.allowBotInserts()
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
	// First turn broadcasts channel created + message to thread
	eb.On("BroadcastChannelCreated", "ch15", "thread-auto-del").Once()
	eb.On("BroadcastMessageCreated", "thread-auto-del", mock.Anything).Maybe()
	s.bot.On("RenameThread", s.ctx, "thread-auto-del", "💨 task #15 (`0 * * * *`) auto-del task").Return(nil).Once()
	s.bot.On("DeleteThread", mock.Anything, "thread-auto-del").Return(nil).Once()
	eb.On("BroadcastChannelDeleted", "thread-auto-del")

	var capturedDelay time.Duration
	var callbackCalled bool
	s.executor.timeAfterFunc = func(d time.Duration, f func()) *time.Timer {
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
	eb.AssertCalled(s.T(), "BroadcastChannelDeleted", "thread-auto-del")
	s.bot.AssertExpectations(s.T())
	eb.AssertExpectations(s.T())
}

func (s *TaskExecutorSuite) TestAutoDeleteEphemeralVariants() {
	tests := []struct {
		name      string
		channelID string
		threadID  string
		response  string
		wantResp  string
		delSec    int
		renameErr error
		deleteErr error
	}{
		{
			name: "suffix marker", channelID: "ch23", threadID: "thread-suffix",
			response: "Nothing to report.\n\n[EPHEMERAL]", wantResp: "Nothing to report.",
			delSec: 60,
		},
		{
			name: "rename error", channelID: "ch22", threadID: "thread-rename-err",
			response: "[EPHEMERAL] Nothing", wantResp: "Nothing",
			delSec: 60, renameErr: errors.New("rename failed"),
		},
		{
			name: "delete error", channelID: "ch18", threadID: "thread-del-err",
			response: "[EPHEMERAL] Nothing", wantResp: "Nothing",
			delSec: 90, deleteErr: errors.New("delete failed"),
		},
	}

	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.executor.streamingEnabled = true

			s.store.On("GetChannel", s.ctx, tc.channelID).Return(nil, nil)
			s.bot.On("CreateSimpleThread", s.ctx, tc.channelID, mock.Anything, mock.Anything).Return(tc.threadID, nil).Once()
			s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
				if req.OnTurn == nil {
					return false
				}
				req.OnTurn(tc.response)
				return true
			})).Return(&agent.AgentResponse{
				Response: tc.response, SessionID: "sess",
			}, nil)
			s.store.On("UpdateSessionID", s.ctx, tc.channelID, "sess").Return(nil)
			s.bot.On("RenameThread", s.ctx, tc.threadID, mock.Anything).Return(tc.renameErr).Once()
			s.bot.On("DeleteThread", mock.Anything, tc.threadID).Return(tc.deleteErr).Once()

			var capturedDelay time.Duration
			s.executor.timeAfterFunc = func(d time.Duration, f func()) *time.Timer {
				capturedDelay = d
				f()
				return time.NewTimer(0)
			}

			resp, err := s.executor.ExecuteTask(s.ctx, &db.ScheduledTask{
				ID: 22, ChannelID: tc.channelID, Prompt: "task",
				Type: db.TaskTypeCron, Schedule: "0 * * * *", AutoDeleteSec: tc.delSec,
			})
			require.NoError(s.T(), err)
			require.Equal(s.T(), tc.wantResp, resp)
			require.Equal(s.T(), time.Duration(tc.delSec)*time.Second, capturedDelay)
			s.bot.AssertCalled(s.T(), "DeleteThread", mock.Anything, tc.threadID)
		})
	}
}

func (s *TaskExecutorSuite) TestAutoDeleteNonEphemeralNoRename() {
	s.executor.streamingEnabled = true

	task := &db.ScheduledTask{
		ID: 19, ChannelID: "ch19", Prompt: "important task",
		Type: db.TaskTypeCron, Schedule: "0 * * * *", AutoDeleteSec: 60,
	}

	s.store.On("GetChannel", s.ctx, "ch19").Return(nil, nil)
	s.bot.On("CreateSimpleThread", s.ctx, "ch19", mock.Anything, mock.Anything).Return("thread-del", nil).Once()
	s.store.On("UpdateSessionID", s.ctx, "ch19", mock.Anything).Return(nil)
	s.bot.On("DeleteThread", mock.Anything, "thread-del").Return(nil).Once()

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		if req.OnTurn == nil {
			return false
		}
		req.OnTurn("Important result")
		return true
	})).Return(&agent.AgentResponse{
		Response: "Important result", SessionID: "sess",
	}, nil)

	var capturedDelay time.Duration
	s.executor.timeAfterFunc = func(d time.Duration, f func()) *time.Timer {
		capturedDelay = d
		f()
		return time.NewTimer(0)
	}

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "Important result", resp)
	require.Equal(s.T(), 60*time.Second, capturedDelay)

	// Should delete but NOT rename (not ephemeral)
	s.bot.AssertNotCalled(s.T(), "RenameThread", mock.Anything, mock.Anything, mock.Anything)
	s.bot.AssertCalled(s.T(), "DeleteThread", mock.Anything, "thread-del")
}

func (s *TaskExecutorSuite) TestAutoDeleteSkipped() {
	tests := []struct {
		name       string
		streaming  bool
		task       *db.ScheduledTask
		response   string
		setupMocks func()
	}{
		{
			name:      "auto delete sec is zero",
			streaming: true,
			task: &db.ScheduledTask{
				ID: 16, ChannelID: "ch16", Prompt: "no-del task",
				Type: db.TaskTypeCron, Schedule: "0 * * * *", AutoDeleteSec: 0,
			},
			response: "Turn 1",
			setupMocks: func() {
				s.store.On("GetChannel", s.ctx, "ch16").Return(nil, nil)
				s.bot.On("CreateSimpleThread", s.ctx, "ch16", mock.Anything, mock.Anything).Return("thread-no-del", nil).Once()
				s.store.On("UpdateSessionID", s.ctx, "ch16", mock.Anything).Return(nil)
			},
		},
		{
			name:      "no thread created",
			streaming: false,
			task: &db.ScheduledTask{
				ID: 17, ChannelID: "ch17", Prompt: "no-thread task",
				Type: db.TaskTypeCron, Schedule: "0 * * * *", AutoDeleteSec: 120,
			},
			response: "[EPHEMERAL] Result",
			setupMocks: func() {
				s.store.On("GetChannel", s.ctx, "ch17").Return(nil, nil)
				s.store.On("UpdateSessionID", s.ctx, "ch17", mock.Anything).Return(nil)
				s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(msg *bot.OutgoingMessage) bool {
					return msg.ChannelID == "ch17" && msg.Content == "Result"
				})).Return(nil).Once()
			},
		},
	}

	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.executor.streamingEnabled = tc.streaming
			tc.setupMocks()

			s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
				if tc.streaming && req.OnTurn != nil {
					req.OnTurn(tc.response)
					return true
				}
				return !tc.streaming && req.OnTurn == nil
			})).Return(&agent.AgentResponse{
				Response: tc.response, SessionID: "sess",
			}, nil)

			var timerCalled bool
			s.executor.timeAfterFunc = func(d time.Duration, f func()) *time.Timer {
				timerCalled = true
				return time.NewTimer(0)
			}

			resp, err := s.executor.ExecuteTask(s.ctx, tc.task)
			require.NoError(s.T(), err)
			require.NotEmpty(s.T(), resp)
			require.False(s.T(), timerCalled, "timeAfterFunc should NOT have been called")
			s.bot.AssertNotCalled(s.T(), "DeleteThread", mock.Anything, mock.Anything)
		})
	}
}

// --- storeBotMessage tests ---

func (s *TaskExecutorSuite) TestStoreBotMessage() {
	eb := new(MockEventBroadcaster)

	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{ID: 5, ChannelID: "ch1"}, nil)
	s.store.On("InsertMessage", s.ctx, mock.MatchedBy(func(m *db.Message) bool {
		return m.ChatID == 5 && m.ChannelID == "ch1" && m.Content == "task done" && m.IsBot
	})).Return(nil)
	eb.On("BroadcastMessageCreated", "ch1", mock.MatchedBy(func(d events.MessageEventData) bool {
		return d.Content == "task done" && d.IsBot && d.AuthorName == "agent"
	}))

	storeBotMessage(s.ctx, s.store, eb, "ch1", "task done")

	s.store.AssertExpectations(s.T())
	eb.AssertExpectations(s.T())
}

func (s *TaskExecutorSuite) TestStoreBotMessageGetChannelError() {
	eb := new(MockEventBroadcaster)

	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, errors.New("db error"))
	eb.On("BroadcastMessageCreated", "ch1", mock.Anything)

	storeBotMessage(s.ctx, s.store, eb, "ch1", "task done")

	s.store.AssertNotCalled(s.T(), "InsertMessage", mock.Anything, mock.Anything)
	eb.AssertExpectations(s.T())
}

func (s *TaskExecutorSuite) TestSetEventBroadcaster() {
	require.Nil(s.T(), s.executor.events)

	eb := new(MockEventBroadcaster)
	s.executor.SetEventBroadcaster(eb)

	require.Same(s.T(), eb, s.executor.events)
}

func (s *TaskExecutorSuite) TestFinalResponseBroadcasts() {
	eb := new(MockEventBroadcaster)
	s.executor.SetEventBroadcaster(eb)

	task := &db.ScheduledTask{
		ID: 1, ChannelID: "ch1", Prompt: "check", Type: db.TaskTypeCron, Schedule: "0 * * * *",
	}

	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{ID: 5, ChannelID: "ch1"}, nil)
	s.runner.On("Run", mock.Anything, mock.Anything).Return(&agent.AgentResponse{
		Response: "all good", SessionID: "s1",
	}, nil)
	s.store.On("UpdateSessionID", s.ctx, "ch1", "s1").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(msg *bot.OutgoingMessage) bool {
		return msg.ChannelID == "ch1" && msg.Content == "all good"
	})).Return(nil)
	s.store.On("InsertMessage", s.ctx, mock.MatchedBy(func(m *db.Message) bool {
		return m.ChatID == 5 && m.Content == "all good" && m.IsBot
	})).Return(nil)
	eb.On("BroadcastMessageCreated", "ch1", mock.MatchedBy(func(d events.MessageEventData) bool {
		return d.Content == "all good" && d.IsBot
	}))

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "all good", resp)

	s.store.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
	eb.AssertExpectations(s.T())
}

func (s *TaskExecutorSuite) TestStreamingThreadBroadcastsToThread() {
	s.executor.streamingEnabled = true
	eb := new(MockEventBroadcaster)
	s.executor.SetEventBroadcaster(eb)

	task := &db.ScheduledTask{
		ID: 20, ChannelID: "ch20", Prompt: "check",
		Type: db.TaskTypeCron, Schedule: "0 * * * *",
	}

	s.store.On("GetChannel", mock.Anything, mock.Anything).Return(&db.Channel{ID: 5, ChannelID: "ch20"}, nil)
	s.store.On("UpsertChannel", s.ctx, mock.MatchedBy(func(ch *db.Channel) bool {
		return ch.ChannelID == "thread-20" && ch.ParentID == "ch20"
	})).Return(nil)

	// Thread creation succeeds
	s.bot.On("CreateSimpleThread", s.ctx, "ch20",
		"🧵 task #20 (`0 * * * *`) check",
		"🧵 task #20 (`0 * * * *`) Turn 1",
	).Return("thread-20", nil).Once()

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		if req.OnTurn == nil {
			return false
		}
		req.OnTurn("Turn 1")
		return true
	})).Return(&agent.AgentResponse{
		Response: "Final", SessionID: "s20",
	}, nil)
	s.store.On("UpdateSessionID", s.ctx, "ch20", "s20").Return(nil)

	// Final response goes to thread
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(msg *bot.OutgoingMessage) bool {
		return msg.ChannelID == "thread-20" && msg.Content == "Final"
	})).Return(nil).Once()
	s.store.On("InsertMessage", mock.Anything, mock.MatchedBy(func(m *db.Message) bool {
		return m.IsBot
	})).Return(nil).Maybe()

	// Channel created broadcast goes to parent
	eb.On("BroadcastChannelCreated", "ch20", "thread-20").Once()
	// First turn broadcast goes to THREAD, not parent channel
	eb.On("BroadcastMessageCreated", "thread-20", mock.MatchedBy(func(d events.MessageEventData) bool {
		return d.Content == "🧵 task #20 (`0 * * * *`) Turn 1" && d.IsBot
	})).Once()
	// Final response broadcast goes to thread
	eb.On("BroadcastMessageCreated", "thread-20", mock.MatchedBy(func(d events.MessageEventData) bool {
		return d.Content == "Final" && d.IsBot
	})).Once()

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "Final", resp)

	// Verify NO broadcast to the parent channel ch20
	for _, call := range eb.Calls {
		if call.Method == "BroadcastMessageCreated" {
			require.NotEqual(s.T(), "ch20", call.Arguments[0],
				"should not broadcast to parent channel")
		}
	}

	s.bot.AssertExpectations(s.T())
	eb.AssertExpectations(s.T())
}

func (s *TaskExecutorSuite) TestStreamingOnToolUseBroadcasts() {
	s.executor.streamingEnabled = true
	eb := new(MockEventBroadcaster)
	s.executor.SetEventBroadcaster(eb)

	task := &db.ScheduledTask{
		ID: 25, ChannelID: "ch25", Prompt: "check tools",
		Type: db.TaskTypeCron, Schedule: "0 * * * *",
	}

	s.store.On("GetChannel", mock.Anything, "ch25").Return(nil, nil)

	s.bot.On("CreateSimpleThread", s.ctx, "ch25", mock.Anything, mock.Anything).Return("thread-25", nil).Once()

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		if req.OnToolUse == nil {
			return false
		}
		// OnToolUse should broadcast to thread once created
		req.OnTurn("Turn 1") // creates thread
		req.OnToolUse("Read", "/tmp/foo.go")
		return true
	})).Return(&agent.AgentResponse{
		Response: "Turn 1", SessionID: "sess-tu",
	}, nil)

	s.store.On("UpdateSessionID", s.ctx, "ch25", "sess-tu").Return(nil)

	eb.On("BroadcastChannelCreated", "ch25", "thread-25").Once()
	eb.On("BroadcastMessageCreated", "thread-25", mock.Anything).Maybe()
	eb.On("BroadcastToolUse", "thread-25", mock.MatchedBy(func(d events.ToolUseEventData) bool {
		return d.ToolName == "Read" && d.Input == "/tmp/foo.go"
	})).Once()

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "Turn 1", resp)

	eb.AssertCalled(s.T(), "BroadcastToolUse", "thread-25", mock.Anything)
	eb.AssertExpectations(s.T())
}

func (s *TaskExecutorSuite) TestStreamingOnToolUseAskUserQuestion() {
	s.executor.streamingEnabled = true
	eb := new(MockEventBroadcaster)
	s.executor.SetEventBroadcaster(eb)

	task := &db.ScheduledTask{
		ID: 27, ChannelID: "ch27", Prompt: "ask questions",
		Type: db.TaskTypeCron, Schedule: "0 * * * *",
	}

	s.store.On("GetChannel", mock.Anything, "ch27").Return(nil, nil)
	s.bot.On("CreateSimpleThread", s.ctx, "ch27", mock.Anything, mock.Anything).Return("thread-27", nil).Once()

	askInput := `{"questions":[{"question":"What next?","header":"Task","options":[{"label":"A"}]}]}`
	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		if req.OnToolUse == nil {
			return false
		}
		req.OnTurn("Turn 1")
		req.OnToolUse("AskUserQuestion", askInput)
		return true
	})).Return(&agent.AgentResponse{Response: "Turn 1", SessionID: "sess-ask"}, nil)

	s.store.On("UpdateSessionID", s.ctx, "ch27", "sess-ask").Return(nil)

	eb.On("BroadcastChannelCreated", "ch27", "thread-27").Once()
	eb.On("BroadcastMessageCreated", "thread-27", mock.Anything).Maybe()
	eb.On("BroadcastToolUse", "thread-27", mock.Anything).Once()
	eb.On("BroadcastAskUser", "thread-27", mock.MatchedBy(func(d events.AskUserQuestionEventData) bool {
		return len(d.Questions) == 1 && d.Questions[0].Question == "What next?"
	})).Once()

	_, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	eb.AssertCalled(s.T(), "BroadcastAskUser", "thread-27", mock.Anything)
	eb.AssertExpectations(s.T())
}

func (s *TaskExecutorSuite) TestStreamingOnToolUseExitPlanMode() {
	s.executor.streamingEnabled = true
	eb := new(MockEventBroadcaster)
	s.executor.SetEventBroadcaster(eb)

	task := &db.ScheduledTask{
		ID: 28, ChannelID: "ch28", Prompt: "plan something",
		Type: db.TaskTypeCron, Schedule: "0 * * * *",
	}

	s.store.On("GetChannel", mock.Anything, "ch28").Return(nil, nil)
	s.bot.On("CreateSimpleThread", s.ctx, "ch28", mock.Anything, mock.Anything).Return("thread-28", nil).Once()

	exitInput := `{"plan":"# My Plan\nDo stuff","planFilePath":"/tmp/plan.md"}`
	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		if req.OnToolUse == nil {
			return false
		}
		req.OnTurn("Turn 1")
		req.OnToolUse("ExitPlanMode", exitInput)
		return true
	})).Return(&agent.AgentResponse{Response: "Turn 1", SessionID: "sess-exit"}, nil)

	s.store.On("UpdateSessionID", s.ctx, "ch28", "sess-exit").Return(nil)

	eb.On("BroadcastChannelCreated", "ch28", "thread-28").Once()
	eb.On("BroadcastMessageCreated", "thread-28", mock.Anything).Maybe()
	eb.On("BroadcastToolUse", "thread-28", mock.Anything).Once()
	eb.On("BroadcastExitPlan", "thread-28", mock.MatchedBy(func(d events.ExitPlanModeEventData) bool {
		return d.Plan == "# My Plan\nDo stuff" && d.PlanFilePath == "/tmp/plan.md"
	})).Once()

	_, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	eb.AssertCalled(s.T(), "BroadcastExitPlan", "thread-28", mock.Anything)
	eb.AssertExpectations(s.T())
}

func (s *TaskExecutorSuite) TestStreamingOnToolUseBroadcastsBeforeThread() {
	s.executor.streamingEnabled = true
	eb := new(MockEventBroadcaster)
	s.executor.SetEventBroadcaster(eb)

	task := &db.ScheduledTask{
		ID: 26, ChannelID: "ch26", Prompt: "tools before thread",
		Type: db.TaskTypeCron, Schedule: "0 * * * *",
	}

	s.store.On("GetChannel", mock.Anything, "ch26").Return(nil, nil)

	s.bot.On("CreateSimpleThread", s.ctx, "ch26", mock.Anything, mock.Anything).Return("thread-26", nil).Once()

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		if req.OnToolUse == nil {
			return false
		}
		// OnToolUse fires BEFORE any OnTurn — threadID is empty, uses task.ChannelID
		req.OnToolUse("Bash", "ls")
		req.OnTurn("Turn 1")
		return true
	})).Return(&agent.AgentResponse{
		Response: "Turn 1", SessionID: "sess-tu2",
	}, nil)

	s.store.On("UpdateSessionID", s.ctx, "ch26", "sess-tu2").Return(nil)

	eb.On("BroadcastChannelCreated", "ch26", "thread-26").Once()
	eb.On("BroadcastMessageCreated", "thread-26", mock.Anything).Maybe()
	// Before thread is created, tool use broadcasts to parent channel
	eb.On("BroadcastToolUse", "ch26", mock.MatchedBy(func(d events.ToolUseEventData) bool {
		return d.ToolName == "Bash" && d.Input == "ls"
	})).Once()

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "Turn 1", resp)

	eb.AssertCalled(s.T(), "BroadcastToolUse", "ch26", mock.Anything)
	eb.AssertExpectations(s.T())
}

func (s *TaskExecutorSuite) TestStreamingOnActivityBroadcasts() {
	s.executor.streamingEnabled = true
	eb := new(MockEventBroadcaster)
	s.executor.SetEventBroadcaster(eb)

	task := &db.ScheduledTask{
		ID: 27, ChannelID: "ch27", Prompt: "check activity",
		Type: db.TaskTypeCron, Schedule: "0 * * * *",
	}

	s.store.On("GetChannel", mock.Anything, "ch27").Return(nil, nil)

	s.bot.On("CreateSimpleThread", s.ctx, "ch27", mock.Anything, mock.Anything).Return("thread-27", nil).Once()

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		if req.OnActivity == nil {
			return false
		}
		req.OnActivity("model", "claude-opus-4-6")
		req.OnTurn("Turn 1")
		req.OnActivity("subagent_started", "Sub task")
		return true
	})).Return(&agent.AgentResponse{
		Response: "Turn 1", SessionID: "sess-act",
	}, nil)

	s.store.On("UpdateSessionID", s.ctx, "ch27", "sess-act").Return(nil)

	eb.On("BroadcastChannelCreated", "ch27", "thread-27").Once()
	eb.On("BroadcastMessageCreated", "thread-27", mock.Anything).Maybe()
	// Model activity fires before thread (uses channel ID)
	eb.On("BroadcastAgentActivity", "ch27", mock.MatchedBy(func(d events.AgentActivityEventData) bool {
		return d.Activity == "model" && d.Model == "claude-opus-4-6"
	})).Once()
	// Subagent activity fires after thread (uses thread ID)
	eb.On("BroadcastAgentActivity", "thread-27", mock.MatchedBy(func(d events.AgentActivityEventData) bool {
		return d.Activity == "subagent_started" && d.Description == "Sub task"
	})).Once()

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "Turn 1", resp)

	eb.AssertNumberOfCalls(s.T(), "BroadcastAgentActivity", 2)
	eb.AssertExpectations(s.T())
}

func (s *TaskExecutorSuite) TestStreamingInvitesPermissionUsersToThread() {
	s.executor.streamingEnabled = true
	s.allowBotInserts()

	task := &db.ScheduledTask{
		ID: 21, ChannelID: "ch21", Prompt: "check perms",
		Type: db.TaskTypeCron, Schedule: "0 * * * *",
	}

	s.store.On("GetChannel", mock.Anything, "ch21").Return(&db.Channel{
		ChannelID: "ch21",
		Permissions: types.Permissions{
			Owners:  types.RoleGrant{Users: []string{"owner-1"}},
			Members: types.RoleGrant{Users: []string{"member-1", "member-2"}},
		},
	}, nil)

	s.bot.On("CreateSimpleThread", s.ctx, "ch21", mock.Anything, mock.Anything).Return("thread-21", nil).Once()
	s.store.On("UpsertChannel", s.ctx, mock.MatchedBy(func(ch *db.Channel) bool {
		return ch.ChannelID == "thread-21" && ch.ParentID == "ch21"
	})).Return(nil)
	s.bot.On("InviteUserToChannel", s.ctx, "thread-21", "owner-1").Return(nil).Once()
	s.bot.On("InviteUserToChannel", s.ctx, "thread-21", "member-1").Return(nil).Once()
	s.bot.On("InviteUserToChannel", s.ctx, "thread-21", "member-2").Return(nil).Once()

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		if req.OnTurn == nil {
			return false
		}
		req.OnTurn("Turn 1")
		return true
	})).Return(&agent.AgentResponse{
		Response: "Turn 1", SessionID: "s21",
	}, nil)
	s.store.On("UpdateSessionID", s.ctx, "ch21", "s21").Return(nil)

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "Turn 1", resp)

	s.bot.AssertCalled(s.T(), "InviteUserToChannel", s.ctx, "thread-21", "owner-1")
	s.bot.AssertCalled(s.T(), "InviteUserToChannel", s.ctx, "thread-21", "member-1")
	s.bot.AssertCalled(s.T(), "InviteUserToChannel", s.ctx, "thread-21", "member-2")
	s.bot.AssertExpectations(s.T())
}

func (s *TaskExecutorSuite) TestStreamingInviteErrorsAreLogged() {
	s.executor.streamingEnabled = true
	s.allowBotInserts()

	task := &db.ScheduledTask{
		ID: 22, ChannelID: "ch22", Prompt: "check perms err",
		Type: db.TaskTypeCron, Schedule: "0 * * * *",
	}

	s.store.On("GetChannel", mock.Anything, "ch22").Return(&db.Channel{
		ChannelID: "ch22",
		Permissions: types.Permissions{
			Owners:  types.RoleGrant{Users: []string{"owner-bad"}},
			Members: types.RoleGrant{Users: []string{"member-bad"}},
		},
	}, nil)

	s.bot.On("CreateSimpleThread", s.ctx, "ch22", mock.Anything, mock.Anything).Return("thread-22", nil).Once()
	s.store.On("UpsertChannel", s.ctx, mock.MatchedBy(func(ch *db.Channel) bool {
		return ch.ChannelID == "thread-22" && ch.ParentID == "ch22"
	})).Return(nil)
	s.bot.On("InviteUserToChannel", s.ctx, "thread-22", "owner-bad").Return(errors.New("invite failed")).Once()
	s.bot.On("InviteUserToChannel", s.ctx, "thread-22", "member-bad").Return(errors.New("invite failed")).Once()

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		if req.OnTurn == nil {
			return false
		}
		req.OnTurn("Turn 1")
		return true
	})).Return(&agent.AgentResponse{
		Response: "Turn 1", SessionID: "s22",
	}, nil)
	s.store.On("UpdateSessionID", s.ctx, "ch22", "s22").Return(nil)

	// Should not fail — invite errors are just logged
	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "Turn 1", resp)
	s.bot.AssertExpectations(s.T())
}
