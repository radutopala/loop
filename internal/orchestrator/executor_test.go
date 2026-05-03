package orchestrator

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/bot"
	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/events"
	"github.com/radutopala/loop/internal/testutil"
	"github.com/radutopala/loop/internal/types"
	"github.com/radutopala/loop/internal/workflow"
	"github.com/radutopala/loop/internal/worktree"
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
	s.executor = NewTaskExecutor(s.runner, s.bot, s.store, logger, 5*time.Minute, false, nil)
}

// allowStatusBroadcasts adds BroadcastAgentStatus expectations for task execution.
func allowStatusBroadcasts(eb *MockEventBroadcaster) {
	eb.On("BroadcastAgentStatus", mock.Anything, mock.Anything).Maybe()
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
	s.store.On("GetScheduledTask", s.ctx, int64(1)).Return(&db.ScheduledTask{ID: 1, Type: db.TaskTypeCron}, nil)
	s.allowBotInserts()
	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		return req.SessionID == "existing-session" &&
			req.ForkSession == true &&
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
	s.store.On("GetScheduledTask", s.ctx, int64(2)).Return(&db.ScheduledTask{ID: 2, Type: db.TaskTypeInterval}, nil)
	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		return req.SessionID == "" && req.ForkSession == false && req.ChannelID == "ch2" && req.DirPath == ""
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

func (s *TaskExecutorSuite) TestActiveRunsRegisteredDuringExecution() {
	activeRuns := &sync.Map{}
	s.executor.SetActiveRuns(activeRuns)

	task := &db.ScheduledTask{
		ID: 60, ChannelID: "ch-active", Prompt: "run", Type: db.TaskTypeCron, Schedule: "* * * * *",
	}
	s.store.On("GetChannel", s.ctx, "ch-active").Return(nil, nil)
	s.store.On("GetScheduledTask", s.ctx, int64(60)).Return(&db.ScheduledTask{ID: 60, Type: db.TaskTypeCron}, nil)
	s.store.On("UpdateSessionID", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.bot.On("SendMessage", mock.Anything, mock.Anything).Return(nil)
	s.store.On("InsertMessage", mock.Anything, mock.Anything).Return(nil)

	registered := false
	s.runner.On("Run", mock.Anything, mock.Anything).Run(func(_ mock.Arguments) {
		// During execution, activeRuns should have our channel registered.
		_, ok := activeRuns.Load("ch-active")
		registered = ok
	}).Return(&agent.AgentResponse{Response: "ok", SessionID: "s1"}, nil)

	_, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.True(s.T(), registered, "activeRuns should have been set during execution")

	// After execution, it should be cleaned up.
	_, ok := activeRuns.Load("ch-active")
	require.False(s.T(), ok, "activeRuns should be cleaned up after execution")
}

func (s *TaskExecutorSuite) TestActiveRunsStopCancelsTaskRun() {
	activeRuns := &sync.Map{}
	s.executor.SetActiveRuns(activeRuns)
	s.executor.streamingEnabled.Store(true)

	task := &db.ScheduledTask{
		ID: 61, ChannelID: "ch-stop", Prompt: "long task",
		Type: db.TaskTypeInterval, Schedule: "5m",
		ThreadID: "stop-thread",
	}
	localCh := &db.Channel{ChannelID: "ch-stop", Platform: types.PlatformLocal}
	s.store.On("GetChannel", mock.Anything, "ch-stop").Return(localCh, nil)
	s.store.On("GetChannel", mock.Anything, "stop-thread").Return(&db.Channel{
		ChannelID: "stop-thread", ParentID: "ch-stop", Platform: types.PlatformLocal, SessionID: "s-thread",
	}, nil)
	s.store.On("GetScheduledTask", mock.Anything, mock.Anything).Return(nil, nil).Maybe()

	eb := new(MockEventBroadcaster)
	s.executor.SetEventBroadcaster(eb)
	allowStatusBroadcasts(eb)

	// Simulate stop: during Run, cancel via activeRuns entry.
	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		return req.ChannelID == "stop-thread" // registered under thread
	})).Run(func(args mock.Arguments) {
		// Stop button: look up and call cancel under the thread ID.
		val, ok := activeRuns.Load("stop-thread")
		require.True(s.T(), ok, "should be registered under thread ID")
		cancel := val.(context.CancelFunc)
		cancel()
	}).Return(nil, context.Canceled)

	_, err := s.executor.ExecuteTask(s.ctx, task)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "context canceled")
}

func (s *TaskExecutorSuite) TestRunnerErrorBroadcastsStatus() {
	eb := new(MockEventBroadcaster)
	s.executor.SetEventBroadcaster(eb)
	eb.On("BroadcastAgentStatus", "ch-err", mock.MatchedBy(func(d events.AgentStatusEventData) bool {
		return d.Status == "running" && d.RunID != ""
	})).Once()
	eb.On("BroadcastAgentStatus", "ch-err", mock.MatchedBy(func(d events.AgentStatusEventData) bool {
		return d.Status == "error" && strings.Contains(d.Error, "runner broke") && d.RunID != ""
	})).Once()

	task := &db.ScheduledTask{ID: 50, ChannelID: "ch-err", Prompt: "fail", Type: db.TaskTypeCron, Schedule: "0 * * * *"}
	s.store.On("GetChannel", s.ctx, "ch-err").Return(nil, nil)
	s.store.On("GetScheduledTask", s.ctx, int64(50)).Return(&db.ScheduledTask{ID: 50, Type: db.TaskTypeCron}, nil)
	s.runner.On("Run", mock.Anything, mock.Anything).Return(nil, errors.New("runner broke"))

	_, err := s.executor.ExecuteTask(s.ctx, task)
	require.Error(s.T(), err)
	eb.AssertExpectations(s.T())
}

func (s *TaskExecutorSuite) TestRunnerErrorBroadcastsToThreadAndParent() {
	eb := new(MockEventBroadcaster)
	s.executor.SetEventBroadcaster(eb)
	s.executor.streamingEnabled.Store(true)

	task := &db.ScheduledTask{
		ID: 52, ChannelID: "ch-parent", Prompt: "fail", Type: db.TaskTypeInterval, Schedule: "5m",
		ThreadID: "existing-thread",
	}
	localCh := &db.Channel{ChannelID: "ch-parent", Platform: types.PlatformLocal}
	s.store.On("GetChannel", mock.Anything, "ch-parent").Return(localCh, nil)
	s.store.On("GetChannel", mock.Anything, "existing-thread").Return(&db.Channel{ChannelID: "existing-thread", ParentID: "ch-parent", Platform: types.PlatformLocal, SessionID: "thread-sess"}, nil)
	s.store.On("GetScheduledTask", mock.Anything, mock.Anything).Return(nil, nil).Maybe()
	// Running: to both thread and parent (with thread_id set)
	eb.On("BroadcastAgentStatus", "existing-thread", mock.MatchedBy(func(d events.AgentStatusEventData) bool {
		return d.Status == "running" && d.ThreadID == "existing-thread"
	})).Once()
	eb.On("BroadcastAgentStatus", "ch-parent", mock.MatchedBy(func(d events.AgentStatusEventData) bool {
		return d.Status == "running" && d.ThreadID == "existing-thread"
	})).Once()
	// Error: to both thread and parent (with thread_id set)
	eb.On("BroadcastAgentStatus", "existing-thread", mock.MatchedBy(func(d events.AgentStatusEventData) bool {
		return d.Status == "error" && d.ThreadID == "existing-thread"
	})).Once()
	eb.On("BroadcastAgentStatus", "ch-parent", mock.MatchedBy(func(d events.AgentStatusEventData) bool {
		return d.Status == "error" && d.ThreadID == "existing-thread"
	})).Once()

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		return req.ChannelID == "existing-thread" // agent registered under thread
	})).Return(nil, errors.New("boom"))

	_, err := s.executor.ExecuteTask(s.ctx, task)
	require.Error(s.T(), err)
	eb.AssertExpectations(s.T())
}

func (s *TaskExecutorSuite) TestAgentResponseErrorBroadcastsStatus() {
	eb := new(MockEventBroadcaster)
	s.executor.SetEventBroadcaster(eb)
	s.executor.streamingEnabled.Store(true)

	task := &db.ScheduledTask{
		ID: 51, ChannelID: "ch-err2", Prompt: "fail", Type: db.TaskTypeInterval, Schedule: "5m",
		ThreadID: "err-thread",
	}
	localCh := &db.Channel{ChannelID: "ch-err2", Platform: types.PlatformLocal}
	s.store.On("GetChannel", mock.Anything, "ch-err2").Return(localCh, nil)
	s.store.On("GetChannel", mock.Anything, "err-thread").Return(&db.Channel{ChannelID: "err-thread", ParentID: "ch-err2", Platform: types.PlatformLocal, SessionID: "err-thread-sess"}, nil)
	s.store.On("GetScheduledTask", mock.Anything, mock.Anything).Return(nil, nil).Maybe()
	// Running: to both thread and parent (with thread_id set)
	eb.On("BroadcastAgentStatus", "err-thread", mock.MatchedBy(func(d events.AgentStatusEventData) bool {
		return d.Status == "running" && d.ThreadID == "err-thread"
	})).Once()
	eb.On("BroadcastAgentStatus", "ch-err2", mock.MatchedBy(func(d events.AgentStatusEventData) bool {
		return d.Status == "running" && d.ThreadID == "err-thread"
	})).Once()
	// Error: to both thread and parent (with thread_id set)
	eb.On("BroadcastAgentStatus", "err-thread", mock.MatchedBy(func(d events.AgentStatusEventData) bool {
		return d.Status == "error" && d.ThreadID == "err-thread"
	})).Once()
	eb.On("BroadcastAgentStatus", "ch-err2", mock.MatchedBy(func(d events.AgentStatusEventData) bool {
		return d.Status == "error" && d.ThreadID == "err-thread"
	})).Once()

	s.runner.On("Run", mock.Anything, mock.Anything).Return(&agent.AgentResponse{Error: "agent broke"}, nil)

	_, err := s.executor.ExecuteTask(s.ctx, task)
	require.Error(s.T(), err)
	eb.AssertExpectations(s.T())
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
	s.store.On("GetScheduledTask", s.ctx, int64(4)).Return(&db.ScheduledTask{ID: 4, Type: db.TaskTypeCron}, nil)
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
			s.store.On("GetScheduledTask", s.ctx, int64(5)).Return(&db.ScheduledTask{ID: 5, Type: db.TaskTypeCron}, nil)
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
	s.executor.streamingEnabled.Store(true)
	s.allowBotInserts()

	task := &db.ScheduledTask{
		ID:        9,
		ChannelID: "ch9",
		Prompt:    "stream task",
		Type:      db.TaskTypeCron,
		Schedule:  "0 * * * *",
	}

	s.store.On("GetChannel", mock.Anything, mock.Anything).Return(nil, nil)
	s.store.On("GetScheduledTask", s.ctx, int64(9)).Return(&db.ScheduledTask{ID: 9, Type: db.TaskTypeCron}, nil)

	// First OnTurn creates a thread with the first turn text
	s.bot.On("CreateSimpleThread", s.ctx, "ch9", "⏱ task #9 (`0 * * * *`) stream task", "⏱ task #9 (`0 * * * *`) Intermediate").Return("thread-1", nil).Once()
	s.store.On("UpdateScheduledTaskThreadID", s.ctx, int64(9), "thread-1").Return(nil)

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

	s.store.On("UpdateSessionID", s.ctx, "thread-1", "sess-stream").Return(nil)

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

func (s *TaskExecutorSuite) TestStreamingLocalPlatformPersistsThreadID() {
	s.executor.streamingEnabled.Store(true)

	task := &db.ScheduledTask{
		ID:        30,
		ChannelID: "ch-local",
		Prompt:    "local task",
		Type:      db.TaskTypeCron,
		Schedule:  "0 * * * *",
	}

	localChannel := &db.Channel{ChannelID: "ch-local", Platform: types.PlatformLocal, DirPath: "/work"}
	s.store.On("GetChannel", mock.Anything, "ch-local").Return(localChannel, nil)
	// Post-run JSONL ingest looks up the session-target channel for chat_id.
	s.store.On("GetChannel", mock.Anything, "local-thread-1").Return(&db.Channel{ID: 9001, ChannelID: "local-thread-1"}, nil).Maybe()
	s.store.On("GetScheduledTask", mock.Anything, mock.Anything).Return(nil, nil).Maybe()
	s.bot.On("CreateSimpleThread", s.ctx, "ch-local", mock.Anything, mock.Anything).Return("local-thread-1", nil).Once()
	s.store.On("LinkTaskThread", s.ctx, mock.MatchedBy(func(ch *db.Channel) bool {
		return ch.ChannelID == "local-thread-1" && ch.ParentID == "ch-local"
	}), int64(30), "local-thread-1").Return(nil).Once()

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		if req.OnTurn == nil {
			return false
		}
		req.OnTurn("Result")
		return true
	})).Return(&agent.AgentResponse{Response: "Result", SessionID: "s1"}, nil)
	s.store.On("UpdateSessionID", s.ctx, "local-thread-1", "s1").Return(nil)

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "Result", resp)
	s.store.AssertCalled(s.T(), "LinkTaskThread", s.ctx, mock.MatchedBy(func(ch *db.Channel) bool {
		return ch.ChannelID == "local-thread-1" && ch.ParentID == "ch-local"
	}), int64(30), "local-thread-1")
}

func (s *TaskExecutorSuite) TestStreamingLocalPlatformReusesThreadID() {
	s.executor.streamingEnabled.Store(true)

	task := &db.ScheduledTask{
		ID:        31,
		ChannelID: "ch-local2",
		Prompt:    "recurring task",
		Type:      db.TaskTypeInterval,
		Schedule:  "5m",
		ThreadID:  "existing-thread",
	}

	localChannel := &db.Channel{ChannelID: "ch-local2", Platform: types.PlatformLocal, DirPath: "/work"}
	s.store.On("GetChannel", mock.Anything, "ch-local2").Return(localChannel, nil)
	threadChannel := &db.Channel{ChannelID: "existing-thread", ParentID: "ch-local2", Platform: types.PlatformLocal, SessionID: "thread-session"}
	s.store.On("GetChannel", mock.Anything, "existing-thread").Return(threadChannel, nil)
	// Re-fetch finds the thread_id persisted by a prior execution.
	s.store.On("GetScheduledTask", s.ctx, int64(31)).Return(&db.ScheduledTask{ID: 31, ThreadID: "existing-thread", Type: db.TaskTypeInterval}, nil)
	s.allowBotInserts()

	// Should NOT create a new thread — reuses existing-thread
	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		if req.OnTurn == nil {
			return false
		}
		if req.SessionID != "thread-session" || req.ForkSession != false {
			return false
		}
		req.OnTurn("Update")
		return true
	})).Return(&agent.AgentResponse{Response: "Update", SessionID: "s2"}, nil)
	s.store.On("UpdateSessionID", s.ctx, "existing-thread", "s2").Return(nil)

	// Second OnTurn goes to the existing thread
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(msg *bot.OutgoingMessage) bool {
		return msg.ChannelID == "existing-thread" && msg.Content == "Update"
	})).Return(nil)

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "Update", resp)
	s.bot.AssertNotCalled(s.T(), "CreateSimpleThread", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func (s *TaskExecutorSuite) TestStreamingDanglingThreadCreatesReplacement() {
	// Task has ThreadID pointing at a channel that no longer exists (e.g. the
	// thread was deleted from the UI without clearing the task's thread_id).
	// The executor must fall back to first-run behavior and create a new
	// replacement thread instead of streaming output to the dead ID.
	s.executor.streamingEnabled.Store(true)

	task := &db.ScheduledTask{
		ID:        33,
		ChannelID: "ch-dangling",
		Prompt:    "recurring task",
		Type:      db.TaskTypeInterval,
		Schedule:  "5m",
		ThreadID:  "deleted-thread",
	}

	localChannel := &db.Channel{ChannelID: "ch-dangling", Platform: types.PlatformLocal, DirPath: "/work"}
	s.store.On("GetChannel", mock.Anything, "ch-dangling").Return(localChannel, nil)
	// Dangling: deleted-thread no longer exists in the channels table.
	s.store.On("GetChannel", mock.Anything, "deleted-thread").Return(nil, nil)
	// Post-run JSONL ingest looks up the session-target channel for chat_id.
	s.store.On("GetChannel", mock.Anything, "new-thread").Return(&db.Channel{ID: 9002, ChannelID: "new-thread"}, nil).Maybe()
	// DB still has the stale thread_id; refresh restores it.
	s.store.On("GetScheduledTask", s.ctx, int64(33)).Return(&db.ScheduledTask{ID: 33, ThreadID: "deleted-thread", Type: db.TaskTypeInterval}, nil)
	s.allowBotInserts()

	// Should create a new replacement thread.
	s.bot.On("CreateSimpleThread", s.ctx, "ch-dangling", mock.Anything, mock.Anything).Return("new-thread", nil).Once()
	s.store.On("LinkTaskThread", s.ctx, mock.MatchedBy(func(ch *db.Channel) bool {
		return ch.ChannelID == "new-thread" && ch.ParentID == "ch-dangling"
	}), int64(33), "new-thread").Return(nil).Once()

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		if req.OnTurn == nil {
			return false
		}
		req.OnTurn("Update")
		return true
	})).Return(&agent.AgentResponse{Response: "Update", SessionID: "s2"}, nil)
	s.store.On("UpdateSessionID", s.ctx, "new-thread", "s2").Return(nil)

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "Update", resp)
	s.store.AssertCalled(s.T(), "LinkTaskThread", s.ctx, mock.MatchedBy(func(ch *db.Channel) bool {
		return ch.ChannelID == "new-thread" && ch.ParentID == "ch-dangling"
	}), int64(33), "new-thread")
}

func (s *TaskExecutorSuite) TestStreamingDiscordReusesThread() {
	s.executor.streamingEnabled.Store(true)

	task := &db.ScheduledTask{
		ID:        32,
		ChannelID: "ch-discord",
		Prompt:    "discord task",
		Type:      db.TaskTypeCron,
		Schedule:  "0 * * * *",
		ThreadID:  "old-discord-thread",
	}

	discordChannel := &db.Channel{ChannelID: "ch-discord", Platform: types.PlatformDiscord}
	s.store.On("GetChannel", mock.Anything, "ch-discord").Return(discordChannel, nil)
	oldThreadChannel := &db.Channel{ChannelID: "old-discord-thread", ParentID: "ch-discord", Platform: types.PlatformDiscord, SessionID: "old-thread-session"}
	s.store.On("GetChannel", mock.Anything, "old-discord-thread").Return(oldThreadChannel, nil)
	s.store.On("GetScheduledTask", s.ctx, int64(32)).Return(&db.ScheduledTask{ID: 32, ThreadID: "old-discord-thread", Type: db.TaskTypeCron}, nil)
	s.allowBotInserts()

	// Should reuse existing thread — no CreateSimpleThread call
	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		if req.OnTurn == nil {
			return false
		}
		if req.SessionID != "old-thread-session" || req.ForkSession != false {
			return false
		}
		req.OnTurn("Discord result")
		return true
	})).Return(&agent.AgentResponse{Response: "Discord result", SessionID: "s3"}, nil)
	s.store.On("UpdateSessionID", s.ctx, "old-discord-thread", "s3").Return(nil)

	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(msg *bot.OutgoingMessage) bool {
		return msg.ChannelID == "old-discord-thread" && msg.Content == "Discord result"
	})).Return(nil)

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "Discord result", resp)
	s.bot.AssertNotCalled(s.T(), "CreateSimpleThread", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func (s *TaskExecutorSuite) TestStreamingDisabledNoOnTurn() {
	// streamingEnabled is false by default
	s.store.On("GetChannel", s.ctx, "ch10").Return(nil, nil)
	s.store.On("GetScheduledTask", s.ctx, int64(10)).Return(&db.ScheduledTask{ID: 10, Type: db.TaskTypeCron}, nil)
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
	s.executor.streamingEnabled.Store(true)
	s.allowBotInserts()

	task := &db.ScheduledTask{
		ID:        11,
		ChannelID: "ch11",
		Prompt:    "stream diff",
		Type:      db.TaskTypeInterval,
		Schedule:  "5m",
	}

	s.store.On("GetChannel", mock.Anything, mock.Anything).Return(nil, nil)
	s.store.On("GetScheduledTask", s.ctx, int64(11)).Return(&db.ScheduledTask{ID: 11, Type: db.TaskTypeInterval}, nil)

	// First OnTurn creates thread
	s.bot.On("CreateSimpleThread", s.ctx, "ch11", "⏱ task #11 (`5m`) stream diff", "⏱ task #11 (`5m`) Intermediate").Return("thread-2", nil).Once()
	s.store.On("UpdateScheduledTaskThreadID", s.ctx, int64(11), "thread-2").Return(nil)

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

	s.store.On("UpdateSessionID", s.ctx, "thread-2", "sess-diff").Return(nil)

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
	s.executor.streamingEnabled.Store(true)

	task := &db.ScheduledTask{
		ID:        12,
		ChannelID: "ch12",
		Prompt:    "fallback task",
		Type:      db.TaskTypeCron,
		Schedule:  "0 * * * *",
	}

	s.store.On("GetChannel", s.ctx, "ch12").Return(nil, nil)
	s.store.On("GetScheduledTask", s.ctx, int64(12)).Return(&db.ScheduledTask{ID: 12, Type: db.TaskTypeCron}, nil)

	// Thread creation fails
	s.bot.On("CreateSimpleThread", s.ctx, "ch12", "⏱ task #12 (`0 * * * *`) fallback task", "⏱ task #12 (`0 * * * *`) Turn 1").Return("", errors.New("thread error")).Once()

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
	s.executor.streamingEnabled.Store(true)

	task := &db.ScheduledTask{
		ID:        14,
		ChannelID: "ch14",
		Prompt:    "send err task",
		Type:      db.TaskTypeCron,
		Schedule:  "0 * * * *",
	}

	s.store.On("GetChannel", s.ctx, "ch14").Return(nil, nil)
	s.store.On("GetScheduledTask", s.ctx, int64(14)).Return(&db.ScheduledTask{ID: 14, Type: db.TaskTypeCron}, nil)

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
	s.executor.streamingEnabled.Store(true)

	task := &db.ScheduledTask{
		ID:        13,
		ChannelID: "ch13",
		Prompt:    "single turn task",
		Type:      db.TaskTypeCron,
		Schedule:  "0 * * * *",
	}

	s.store.On("GetChannel", s.ctx, "ch13").Return(nil, nil)
	s.store.On("GetScheduledTask", s.ctx, int64(13)).Return(&db.ScheduledTask{ID: 13, Type: db.TaskTypeCron}, nil)

	// Thread created for single turn
	s.bot.On("CreateSimpleThread", s.ctx, "ch13", "⏱ task #13 (`0 * * * *`) single turn task", "⏱ task #13 (`0 * * * *`) Only turn").Return("thread-3", nil).Once()
	s.store.On("UpdateScheduledTaskThreadID", s.ctx, int64(13), "thread-3").Return(nil)

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

	s.store.On("UpdateSessionID", s.ctx, "thread-3", "sess-single").Return(nil)

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
			s.store.On("GetScheduledTask", s.ctx, int64(20)).Return(&db.ScheduledTask{ID: 20, Type: db.TaskTypeCron}, nil)
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
	s.executor.streamingEnabled.Store(true)
	eb := new(MockEventBroadcaster)
	s.executor.SetEventBroadcaster(eb)
	allowStatusBroadcasts(eb)

	task := &db.ScheduledTask{
		ID:            15,
		ChannelID:     "ch15",
		Prompt:        "auto-del task",
		Type:          db.TaskTypeCron,
		Schedule:      "0 * * * *",
		AutoDeleteSec: 60,
	}

	s.store.On("GetChannel", s.ctx, "ch15").Return(nil, nil)
	s.store.On("GetScheduledTask", s.ctx, int64(15)).Return(&db.ScheduledTask{ID: 15, Type: db.TaskTypeCron}, nil)
	s.allowBotInserts()
	s.bot.On("CreateSimpleThread", s.ctx, "ch15", mock.Anything, mock.Anything).Return("thread-auto-del", nil).Once()
	s.store.On("UpdateScheduledTaskThreadID", s.ctx, int64(15), "thread-auto-del").Return(nil)

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

	s.store.On("UpdateSessionID", s.ctx, "thread-auto-del", "sess-auto-del").Return(nil)
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

func (s *TaskExecutorSuite) TestAutoDeleteEphemeralLocalPlatform() {
	s.executor.streamingEnabled.Store(true)
	eb := new(MockEventBroadcaster)
	s.executor.events = eb
	eb.On("BroadcastAgentStatus", mock.Anything, mock.Anything).Maybe()
	eb.On("BroadcastToolUse", mock.Anything, mock.Anything).Maybe()
	eb.On("BroadcastAgentActivity", mock.Anything, mock.Anything).Maybe()

	task := &db.ScheduledTask{
		ID: 40, ChannelID: "ch-local-eph", Prompt: "check stuff",
		Type: db.TaskTypeCron, Schedule: "0 * * * *", AutoDeleteSec: 30,
	}

	localCh := &db.Channel{ChannelID: "ch-local-eph", Platform: types.PlatformLocal, DirPath: "/work"}
	s.store.On("GetChannel", mock.Anything, "ch-local-eph").Return(localCh, nil)
	// Post-run JSONL ingest looks up the session-target channel for chat_id.
	s.store.On("GetChannel", mock.Anything, "local-eph-thread").Return(&db.Channel{ID: 9003, ChannelID: "local-eph-thread"}, nil).Maybe()
	s.store.On("GetScheduledTask", mock.Anything, mock.Anything).Return(nil, nil).Maybe()
	s.store.On("LinkTaskThread", s.ctx, mock.Anything, int64(40), "local-eph-thread").Return(nil)
	s.allowBotInserts()

	s.bot.On("CreateSimpleThread", s.ctx, "ch-local-eph", mock.Anything, mock.Anything).Return("local-eph-thread", nil).Once()

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		if req.OnTurn == nil {
			return false
		}
		req.OnTurn("[EPHEMERAL] Nothing new")
		return true
	})).Return(&agent.AgentResponse{Response: "[EPHEMERAL] Nothing new", SessionID: "s-eph"}, nil)

	s.store.On("UpdateSessionID", s.ctx, "local-eph-thread", "s-eph").Return(nil)
	eb.On("BroadcastChannelCreated", "ch-local-eph", "local-eph-thread").Once()
	eb.On("BroadcastMessageCreated", "local-eph-thread", mock.Anything).Maybe()
	// Local platform: ephemeral rename uses [ephemeral] prefix
	s.bot.On("RenameThread", s.ctx, "local-eph-thread", "[ephemeral] task #40 (`0 * * * *`) check stuff").Return(nil).Once()
	s.bot.On("DeleteThread", mock.Anything, "local-eph-thread").Return(nil).Once()
	eb.On("BroadcastChannelDeleted", "local-eph-thread")

	s.executor.timeAfterFunc = func(_ time.Duration, f func()) *time.Timer {
		f()
		return time.NewTimer(0)
	}

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "Nothing new", resp)
	s.bot.AssertCalled(s.T(), "RenameThread", s.ctx, "local-eph-thread", "[ephemeral] task #40 (`0 * * * *`) check stuff")
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
			s.executor.streamingEnabled.Store(true)

			s.store.On("GetChannel", s.ctx, tc.channelID).Return(nil, nil)
			s.store.On("GetScheduledTask", s.ctx, int64(22)).Return(&db.ScheduledTask{ID: 22, Type: db.TaskTypeCron}, nil)
			s.bot.On("CreateSimpleThread", s.ctx, tc.channelID, mock.Anything, mock.Anything).Return(tc.threadID, nil).Once()
			s.store.On("UpdateScheduledTaskThreadID", s.ctx, int64(22), tc.threadID).Return(nil)
			s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
				if req.OnTurn == nil {
					return false
				}
				req.OnTurn(tc.response)
				return true
			})).Return(&agent.AgentResponse{
				Response: tc.response, SessionID: "sess",
			}, nil)
			s.store.On("UpdateSessionID", s.ctx, tc.threadID, "sess").Return(nil)
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
	s.executor.streamingEnabled.Store(true)

	task := &db.ScheduledTask{
		ID: 19, ChannelID: "ch19", Prompt: "important task",
		Type: db.TaskTypeCron, Schedule: "0 * * * *", AutoDeleteSec: 60,
	}

	s.store.On("GetChannel", s.ctx, "ch19").Return(nil, nil)
	s.store.On("GetScheduledTask", s.ctx, int64(19)).Return(&db.ScheduledTask{ID: 19, Type: db.TaskTypeCron}, nil)
	s.bot.On("CreateSimpleThread", s.ctx, "ch19", mock.Anything, mock.Anything).Return("thread-del", nil).Once()
	s.store.On("UpdateScheduledTaskThreadID", s.ctx, int64(19), "thread-del").Return(nil)
	s.store.On("UpdateSessionID", s.ctx, "thread-del", mock.Anything).Return(nil)
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
				s.store.On("GetScheduledTask", s.ctx, int64(16)).Return(&db.ScheduledTask{ID: 16, Type: db.TaskTypeCron}, nil)
				s.bot.On("CreateSimpleThread", s.ctx, "ch16", mock.Anything, mock.Anything).Return("thread-no-del", nil).Once()
				s.store.On("UpdateScheduledTaskThreadID", s.ctx, int64(16), "thread-no-del").Return(nil)
				s.store.On("UpdateSessionID", s.ctx, "thread-no-del", mock.Anything).Return(nil)
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
				s.store.On("GetScheduledTask", s.ctx, int64(17)).Return(&db.ScheduledTask{ID: 17, Type: db.TaskTypeCron}, nil)
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
			s.executor.streamingEnabled.Store(tc.streaming)
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
	allowStatusBroadcasts(eb)

	require.Same(s.T(), eb, s.executor.events)
}

func (s *TaskExecutorSuite) TestFinalResponseBroadcasts() {
	eb := new(MockEventBroadcaster)
	s.executor.SetEventBroadcaster(eb)
	allowStatusBroadcasts(eb)

	task := &db.ScheduledTask{
		ID: 1, ChannelID: "ch1", Prompt: "check", Type: db.TaskTypeCron, Schedule: "0 * * * *",
	}

	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{ID: 5, ChannelID: "ch1"}, nil)
	s.store.On("GetScheduledTask", s.ctx, int64(1)).Return(&db.ScheduledTask{ID: 1, Type: db.TaskTypeCron}, nil)
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
	s.executor.streamingEnabled.Store(true)
	eb := new(MockEventBroadcaster)
	s.executor.SetEventBroadcaster(eb)
	allowStatusBroadcasts(eb)

	task := &db.ScheduledTask{
		ID: 20, ChannelID: "ch20", Prompt: "check",
		Type: db.TaskTypeCron, Schedule: "0 * * * *",
	}

	s.store.On("GetChannel", mock.Anything, mock.Anything).Return(&db.Channel{ID: 5, ChannelID: "ch20"}, nil)
	s.store.On("GetScheduledTask", s.ctx, int64(20)).Return(&db.ScheduledTask{ID: 20, Type: db.TaskTypeCron}, nil)
	s.store.On("LinkTaskThread", s.ctx, mock.MatchedBy(func(ch *db.Channel) bool {
		return ch.ChannelID == "thread-20" && ch.ParentID == "ch20"
	}), int64(20), "thread-20").Return(nil)

	// Thread creation succeeds
	s.bot.On("CreateSimpleThread", s.ctx, "ch20",
		"⏱ task #20 (`0 * * * *`) check",
		"⏱ task #20 (`0 * * * *`) Turn 1",
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
	s.store.On("UpdateSessionID", s.ctx, "thread-20", "s20").Return(nil)

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
		return d.Content == "⏱ task #20 (`0 * * * *`) Turn 1" && d.IsBot
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
	s.executor.streamingEnabled.Store(true)
	eb := new(MockEventBroadcaster)
	s.executor.SetEventBroadcaster(eb)
	allowStatusBroadcasts(eb)

	task := &db.ScheduledTask{
		ID: 25, ChannelID: "ch25", Prompt: "check tools",
		Type: db.TaskTypeCron, Schedule: "0 * * * *",
	}

	s.store.On("GetChannel", mock.Anything, "ch25").Return(nil, nil)
	// Return a non-nil channel so resolveTargetChatID's lazy threadChatID
	// resolution sets a non-zero chatID (covers the `threadChatID = ch.ID` branch).
	s.store.On("GetChannel", mock.Anything, "thread-25").Return(&db.Channel{ID: 250, ChannelID: "thread-25"}, nil).Maybe()
	s.store.On("GetScheduledTask", s.ctx, int64(25)).Return(&db.ScheduledTask{ID: 25, Type: db.TaskTypeCron}, nil)

	s.bot.On("CreateSimpleThread", s.ctx, "ch25", mock.Anything, mock.Anything).Return("thread-25", nil).Once()
	s.store.On("UpdateScheduledTaskThreadID", s.ctx, int64(25), "thread-25").Return(nil)
	s.store.On("InsertAgentEvent", mock.Anything, mock.Anything).Return(nil).Maybe()

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		if req.OnToolUse == nil {
			return false
		}
		// OnToolUse should broadcast to thread once created
		req.OnTurn("Turn 1") // creates thread
		req.OnToolUse("toolu_a", "Read", "/tmp/foo.go")
		return true
	})).Return(&agent.AgentResponse{
		Response: "Turn 1", SessionID: "sess-tu",
	}, nil)

	s.store.On("UpdateSessionID", s.ctx, "thread-25", "sess-tu").Return(nil)

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
	s.executor.streamingEnabled.Store(true)
	eb := new(MockEventBroadcaster)
	s.executor.SetEventBroadcaster(eb)
	allowStatusBroadcasts(eb)

	task := &db.ScheduledTask{
		ID: 27, ChannelID: "ch27", Prompt: "ask questions",
		Type: db.TaskTypeCron, Schedule: "0 * * * *",
	}

	s.store.On("GetChannel", mock.Anything, "ch27").Return(nil, nil)
	s.store.On("GetChannel", mock.Anything, "thread-27").Return(nil, nil).Maybe()
	s.store.On("GetScheduledTask", s.ctx, int64(27)).Return(&db.ScheduledTask{ID: 27, Type: db.TaskTypeCron}, nil)
	s.bot.On("CreateSimpleThread", s.ctx, "ch27", mock.Anything, mock.Anything).Return("thread-27", nil).Once()
	s.store.On("UpdateScheduledTaskThreadID", s.ctx, int64(27), "thread-27").Return(nil)

	askInput := `{"questions":[{"question":"What next?","header":"Task","options":[{"label":"A"}]}]}`
	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		if req.OnToolUse == nil {
			return false
		}
		req.OnTurn("Turn 1")
		req.OnToolUse("toolu_q", "AskUserQuestion", askInput)
		return true
	})).Return(&agent.AgentResponse{Response: "Turn 1", SessionID: "sess-ask"}, nil)

	s.store.On("UpdateSessionID", s.ctx, "thread-27", "sess-ask").Return(nil)

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
	s.executor.streamingEnabled.Store(true)
	eb := new(MockEventBroadcaster)
	s.executor.SetEventBroadcaster(eb)
	allowStatusBroadcasts(eb)

	task := &db.ScheduledTask{
		ID: 28, ChannelID: "ch28", Prompt: "plan something",
		Type: db.TaskTypeCron, Schedule: "0 * * * *",
	}

	s.store.On("GetChannel", mock.Anything, "ch28").Return(nil, nil)
	s.store.On("GetChannel", mock.Anything, "thread-28").Return(nil, nil).Maybe()
	s.store.On("GetScheduledTask", s.ctx, int64(28)).Return(&db.ScheduledTask{ID: 28, Type: db.TaskTypeCron}, nil)
	s.bot.On("CreateSimpleThread", s.ctx, "ch28", mock.Anything, mock.Anything).Return("thread-28", nil).Once()
	s.store.On("UpdateScheduledTaskThreadID", s.ctx, int64(28), "thread-28").Return(nil)

	exitInput := `{"plan":"# My Plan\nDo stuff","planFilePath":"/tmp/plan.md"}`
	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		if req.OnToolUse == nil {
			return false
		}
		req.OnTurn("Turn 1")
		req.OnToolUse("toolu_p", "ExitPlanMode", exitInput)
		return true
	})).Return(&agent.AgentResponse{Response: "Turn 1", SessionID: "sess-exit"}, nil)

	s.store.On("UpdateSessionID", s.ctx, "thread-28", "sess-exit").Return(nil)

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

func (s *TaskExecutorSuite) TestStreamingOnToolUseTodoWrite() {
	s.executor.streamingEnabled.Store(true)
	eb := new(MockEventBroadcaster)
	s.executor.SetEventBroadcaster(eb)
	allowStatusBroadcasts(eb)

	task := &db.ScheduledTask{
		ID: 29, ChannelID: "ch29", Prompt: "do tasks",
		Type: db.TaskTypeCron, Schedule: "0 * * * *",
	}

	s.store.On("GetChannel", mock.Anything, "ch29").Return(nil, nil)
	s.store.On("GetChannel", mock.Anything, "thread-29").Return(nil, nil).Maybe()
	s.store.On("GetScheduledTask", s.ctx, int64(29)).Return(&db.ScheduledTask{ID: 29, Type: db.TaskTypeCron}, nil)
	s.bot.On("CreateSimpleThread", s.ctx, "ch29", mock.Anything, mock.Anything).Return("thread-29", nil).Once()
	s.store.On("UpdateScheduledTaskThreadID", s.ctx, int64(29), "thread-29").Return(nil)

	todoInput := `{"todos":[{"content":"Fix bug","status":"in_progress","activeForm":"Fixing bug"}]}`
	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		if req.OnToolUse == nil {
			return false
		}
		req.OnTurn("Turn 1")
		req.OnToolUse("toolu_t", "TodoWrite", todoInput)
		return true
	})).Return(&agent.AgentResponse{Response: "Turn 1", SessionID: "sess-todo"}, nil)

	s.store.On("UpdateSessionID", s.ctx, "thread-29", "sess-todo").Return(nil)

	eb.On("BroadcastChannelCreated", "ch29", "thread-29").Once()
	eb.On("BroadcastMessageCreated", "thread-29", mock.Anything).Maybe()
	eb.On("BroadcastToolUse", "thread-29", mock.Anything).Once()
	eb.On("BroadcastTodoWrite", "thread-29", mock.MatchedBy(func(d events.TodoWriteEventData) bool {
		return len(d.Todos) == 1 && d.Todos[0].Content == "Fix bug"
	})).Once()

	_, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	eb.AssertCalled(s.T(), "BroadcastTodoWrite", "thread-29", mock.Anything)
	eb.AssertExpectations(s.T())
}

func (s *TaskExecutorSuite) TestStreamingOnToolUseBroadcastsBeforeThread() {
	s.executor.streamingEnabled.Store(true)
	eb := new(MockEventBroadcaster)
	s.executor.SetEventBroadcaster(eb)
	allowStatusBroadcasts(eb)

	task := &db.ScheduledTask{
		ID: 26, ChannelID: "ch26", Prompt: "tools before thread",
		Type: db.TaskTypeCron, Schedule: "0 * * * *",
	}

	s.store.On("GetChannel", mock.Anything, "ch26").Return(nil, nil)
	s.store.On("GetScheduledTask", s.ctx, int64(26)).Return(&db.ScheduledTask{ID: 26, Type: db.TaskTypeCron}, nil)

	s.bot.On("CreateSimpleThread", s.ctx, "ch26", mock.Anything, mock.Anything).Return("thread-26", nil).Once()
	s.store.On("UpdateScheduledTaskThreadID", s.ctx, int64(26), "thread-26").Return(nil)

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		if req.OnToolUse == nil {
			return false
		}
		// OnToolUse fires BEFORE any OnTurn — threadID is empty, uses task.ChannelID
		req.OnToolUse("toolu_b", "Bash", "ls")
		req.OnTurn("Turn 1")
		return true
	})).Return(&agent.AgentResponse{
		Response: "Turn 1", SessionID: "sess-tu2",
	}, nil)

	s.store.On("UpdateSessionID", s.ctx, "thread-26", "sess-tu2").Return(nil)

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
	s.executor.streamingEnabled.Store(true)
	eb := new(MockEventBroadcaster)
	s.executor.SetEventBroadcaster(eb)
	allowStatusBroadcasts(eb)

	task := &db.ScheduledTask{
		ID: 27, ChannelID: "ch27", Prompt: "check activity",
		Type: db.TaskTypeCron, Schedule: "0 * * * *",
	}

	s.store.On("GetChannel", mock.Anything, "ch27").Return(nil, nil)
	s.store.On("GetScheduledTask", s.ctx, int64(27)).Return(&db.ScheduledTask{ID: 27, Type: db.TaskTypeCron}, nil)

	s.bot.On("CreateSimpleThread", s.ctx, "ch27", mock.Anything, mock.Anything).Return("thread-27", nil).Once()
	s.store.On("UpdateScheduledTaskThreadID", s.ctx, int64(27), "thread-27").Return(nil)

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

	s.store.On("UpdateSessionID", s.ctx, "thread-27", "sess-act").Return(nil)

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

// TestStreamingOnCompactingPersistsRow asserts that a "compacting" activity
// event in the scheduled-task path writes a kind=compacting row via
// InsertAgentEvent (mirrors how thinking/tool_result are persisted) so the
// marker survives run completion / page reload. Also asserts that
// non-compacting activities do NOT trigger persistence — only the broadcast.
func (s *TaskExecutorSuite) TestStreamingOnCompactingPersistsRow() {
	s.executor.streamingEnabled.Store(true)
	eb := new(MockEventBroadcaster)
	s.executor.SetEventBroadcaster(eb)
	allowStatusBroadcasts(eb)

	task := &db.ScheduledTask{
		ID: 81, ChannelID: "ch81", Prompt: "compact me",
		Type: db.TaskTypeOnce, Schedule: "10s",
	}

	parent := &db.Channel{ID: 300, ChannelID: "ch81", Platform: types.PlatformLocal, DirPath: "/work"}
	s.store.On("GetChannel", mock.Anything, "ch81").Return(parent, nil)
	s.store.On("GetChannel", mock.Anything, "thread-81").Return(nil, nil).Maybe()

	s.bot.On("CreateSimpleThread", s.ctx, "ch81", mock.Anything, mock.Anything).Return("thread-81", nil).Once()
	s.store.On("UpsertChannel", s.ctx, mock.MatchedBy(func(ch *db.Channel) bool {
		return ch.ChannelID == "thread-81" && ch.ParentID == "ch81"
	})).Return(nil).Once()

	// Pre-thread compacting persists with chat_id=300 (the parent channel)
	// and ChannelID="ch81" (the task's channel).
	s.store.On("InsertAgentEvent", mock.Anything, mock.MatchedBy(func(m *db.Message) bool {
		return m.ChannelID == "ch81" && m.ChatID == 300 && m.Kind == db.MessageKindCompacting
	})).Return(nil).Once()

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		if req.OnActivity == nil {
			return false
		}
		// Pre-thread compacting fires before OnTurn — exercises both the
		// broadcast path and the storeAgentEvent path against parentChatID.
		req.OnActivity("compacting", "")
		// Non-compacting activity must NOT trigger storeAgentEvent (verified
		// by InsertAgentEvent expecting only one call total).
		req.OnActivity("model", "claude-opus-4-6")
		req.OnTurn("Turn 1")
		return true
	})).Return(&agent.AgentResponse{
		Response: "Turn 1", SessionID: "sess-cmp",
	}, nil)

	s.store.On("UpdateSessionID", s.ctx, "thread-81", "sess-cmp").Return(nil)

	eb.On("BroadcastChannelCreated", "ch81", "thread-81").Once()
	eb.On("BroadcastMessageCreated", "thread-81", mock.Anything).Maybe()
	eb.On("BroadcastAgentActivity", "ch81", mock.MatchedBy(func(d events.AgentActivityEventData) bool {
		return d.Activity == "compacting"
	})).Once()
	eb.On("BroadcastAgentActivity", "ch81", mock.MatchedBy(func(d events.AgentActivityEventData) bool {
		return d.Activity == "model" && d.Model == "claude-opus-4-6"
	})).Once()

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "Turn 1", resp)

	eb.AssertNumberOfCalls(s.T(), "BroadcastAgentActivity", 2)
	s.store.AssertNumberOfCalls(s.T(), "InsertAgentEvent", 1)
	eb.AssertExpectations(s.T())
}

func (s *TaskExecutorSuite) TestStreamingOnThinkingAndToolResultBroadcasts() {
	s.executor.streamingEnabled.Store(true)
	eb := new(MockEventBroadcaster)
	s.executor.SetEventBroadcaster(eb)
	allowStatusBroadcasts(eb)

	task := &db.ScheduledTask{
		ID: 28, ChannelID: "ch28", Prompt: "think + tool_result",
		Type: db.TaskTypeCron, Schedule: "0 * * * *",
	}

	s.store.On("GetChannel", mock.Anything, "ch28").Return(nil, nil)
	s.store.On("GetChannel", mock.Anything, "thread-28").Return(nil, nil).Maybe()
	s.store.On("GetScheduledTask", s.ctx, int64(28)).Return(&db.ScheduledTask{ID: 28, Type: db.TaskTypeCron}, nil)

	s.bot.On("CreateSimpleThread", s.ctx, "ch28", mock.Anything, mock.Anything).Return("thread-28", nil).Once()
	s.store.On("UpdateScheduledTaskThreadID", s.ctx, int64(28), "thread-28").Return(nil)

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		if req.OnThinking == nil || req.OnToolResult == nil {
			return false
		}
		// Pre-thread fires use task.ChannelID
		req.OnThinking("pre-thread plan")
		req.OnToolResult("toolu_x", "pre-out", false)
		req.OnTurn("Turn 1")
		// Post-thread fires use threadID
		req.OnThinking("post-thread plan")
		req.OnToolResult("toolu_y", "post-out", true)
		return true
	})).Return(&agent.AgentResponse{
		Response: "Turn 1", SessionID: "sess-think-tr",
	}, nil)

	s.store.On("UpdateSessionID", s.ctx, "thread-28", "sess-think-tr").Return(nil)

	eb.On("BroadcastChannelCreated", "ch28", "thread-28").Once()
	eb.On("BroadcastMessageCreated", "thread-28", mock.Anything).Maybe()
	eb.On("BroadcastAgentThinking", "ch28", events.AgentThinkingEventData{Text: "pre-thread plan"}).Once()
	eb.On("BroadcastToolResult", "ch28", events.ToolResultEventData{ToolUseID: "toolu_x", Output: "pre-out"}).Once()
	eb.On("BroadcastAgentThinking", "thread-28", events.AgentThinkingEventData{Text: "post-thread plan"}).Once()
	eb.On("BroadcastToolResult", "thread-28", events.ToolResultEventData{ToolUseID: "toolu_y", Output: "post-out", IsError: true}).Once()

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "Turn 1", resp)

	eb.AssertNumberOfCalls(s.T(), "BroadcastAgentThinking", 2)
	eb.AssertNumberOfCalls(s.T(), "BroadcastToolResult", 2)
	eb.AssertExpectations(s.T())
}

// TestStreamingResolvesThreadChatID covers resolveTargetChatID's lazy lookup:
// once a thread is created and a tool callback fires for that thread, the
// executor calls GetChannel(threadID) once and stamps the resulting chat_id
// onto the inserted agent event.
func (s *TaskExecutorSuite) TestStreamingResolvesThreadChatID() {
	s.executor.streamingEnabled.Store(true)
	eb := new(MockEventBroadcaster)
	s.executor.SetEventBroadcaster(eb)
	allowStatusBroadcasts(eb)

	task := &db.ScheduledTask{
		ID: 71, ChannelID: "ch71", Prompt: "resolve thread chat id",
		Type: db.TaskTypeCron, Schedule: "0 * * * *",
	}

	parent := &db.Channel{ID: 100, ChannelID: "ch71", Platform: types.PlatformLocal}
	threadCh := &db.Channel{ID: 999, ChannelID: "thread-71", ParentID: "ch71", Platform: types.PlatformLocal}
	s.store.On("GetChannel", mock.Anything, "ch71").Return(parent, nil)
	s.store.On("GetChannel", mock.Anything, "thread-71").Return(threadCh, nil).Once()
	s.store.On("GetScheduledTask", s.ctx, int64(71)).Return(&db.ScheduledTask{ID: 71, Type: db.TaskTypeCron}, nil)

	s.bot.On("CreateSimpleThread", s.ctx, "ch71", mock.Anything, mock.Anything).Return("thread-71", nil).Once()
	s.store.On("LinkTaskThread", s.ctx, mock.MatchedBy(func(ch *db.Channel) bool {
		return ch.ChannelID == "thread-71" && ch.ParentID == "ch71"
	}), int64(71), "thread-71").Return(nil)

	// The agent event must be inserted with chat_id=999 (the resolved thread chat id).
	s.store.On("InsertAgentEvent", mock.Anything, mock.MatchedBy(func(m *db.Message) bool {
		return m.ChatID == 999 && m.ChannelID == "thread-71" && m.Kind == db.MessageKindToolUse
	})).Return(nil).Once()

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		if req.OnTurn == nil || req.OnToolUse == nil {
			return false
		}
		req.OnTurn("Turn 1")
		req.OnToolUse("toolu_r", "Read", "/x")
		// Second tool call exercises the cached path (threadChatIDResolved=true).
		req.OnToolUse("toolu_s", "Read", "/y")
		return true
	})).Return(&agent.AgentResponse{Response: "Turn 1", SessionID: "s71"}, nil)
	s.store.On("UpdateSessionID", s.ctx, "thread-71", "s71").Return(nil)

	eb.On("BroadcastChannelCreated", "ch71", "thread-71").Once()
	eb.On("BroadcastMessageCreated", "thread-71", mock.Anything).Maybe()
	eb.On("BroadcastToolUse", "thread-71", mock.Anything).Twice()

	// The second tool call hits the cached branch but still inserts an agent event.
	s.store.On("InsertAgentEvent", mock.Anything, mock.MatchedBy(func(m *db.Message) bool {
		return m.ChatID == 999 && m.Kind == db.MessageKindToolUse
	})).Return(nil).Maybe()

	_, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	// GetChannel("thread-71") must be called exactly once (lazy + cached).
	s.store.AssertNumberOfCalls(s.T(), "GetChannel", 2) // ch71 + thread-71
}

// TestStreamingOnceTaskUpsertsChannel covers the TaskTypeOnce branch in the
// thread-creation path: one-shot tasks call UpsertChannel for the thread row
// instead of LinkTaskThread (which is reserved for recurring tasks).
func (s *TaskExecutorSuite) TestStreamingOnceTaskUpsertsChannel() {
	s.executor.streamingEnabled.Store(true)
	eb := new(MockEventBroadcaster)
	s.executor.SetEventBroadcaster(eb)
	allowStatusBroadcasts(eb)

	task := &db.ScheduledTask{
		ID: 72, ChannelID: "ch72", Prompt: "one-shot",
		Type: db.TaskTypeOnce, Schedule: "10s",
	}

	parent := &db.Channel{ID: 200, ChannelID: "ch72", Platform: types.PlatformLocal, DirPath: "/work"}
	s.store.On("GetChannel", mock.Anything, "ch72").Return(parent, nil)
	s.store.On("GetChannel", mock.Anything, "thread-72").Return(nil, nil).Maybe()

	s.bot.On("CreateSimpleThread", s.ctx, "ch72", mock.Anything, mock.Anything).Return("thread-72", nil).Once()
	// One-shot tasks must NOT call LinkTaskThread — they call UpsertChannel.
	s.store.On("UpsertChannel", s.ctx, mock.MatchedBy(func(ch *db.Channel) bool {
		return ch.ChannelID == "thread-72" && ch.ParentID == "ch72"
	})).Return(nil).Once()

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		if req.OnTurn == nil {
			return false
		}
		req.OnTurn("Done")
		return true
	})).Return(&agent.AgentResponse{Response: "Done", SessionID: "s72"}, nil)
	s.store.On("UpdateSessionID", s.ctx, "thread-72", "s72").Return(nil)

	eb.On("BroadcastChannelCreated", "ch72", "thread-72").Once()
	eb.On("BroadcastMessageCreated", "thread-72", mock.Anything).Maybe()

	_, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	s.store.AssertCalled(s.T(), "UpsertChannel", s.ctx, mock.Anything)
	s.store.AssertNotCalled(s.T(), "LinkTaskThread", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	s.store.AssertNotCalled(s.T(), "UpdateScheduledTaskThreadID", mock.Anything, mock.Anything, mock.Anything)
}

func (s *TaskExecutorSuite) TestStreamingInvitesPermissionUsersToThread() {
	s.executor.streamingEnabled.Store(true)
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
	s.store.On("GetScheduledTask", s.ctx, int64(21)).Return(&db.ScheduledTask{ID: 21, Type: db.TaskTypeCron}, nil)

	s.bot.On("CreateSimpleThread", s.ctx, "ch21", mock.Anything, mock.Anything).Return("thread-21", nil).Once()
	s.store.On("LinkTaskThread", s.ctx, mock.MatchedBy(func(ch *db.Channel) bool {
		return ch.ChannelID == "thread-21" && ch.ParentID == "ch21"
	}), int64(21), "thread-21").Return(nil)
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
	s.store.On("UpdateSessionID", s.ctx, "thread-21", "s21").Return(nil)

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "Turn 1", resp)

	s.bot.AssertCalled(s.T(), "InviteUserToChannel", s.ctx, "thread-21", "owner-1")
	s.bot.AssertCalled(s.T(), "InviteUserToChannel", s.ctx, "thread-21", "member-1")
	s.bot.AssertCalled(s.T(), "InviteUserToChannel", s.ctx, "thread-21", "member-2")
	s.bot.AssertExpectations(s.T())
}

func (s *TaskExecutorSuite) TestStreamingInviteErrorsAreLogged() {
	s.executor.streamingEnabled.Store(true)
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
	s.store.On("GetScheduledTask", s.ctx, int64(22)).Return(&db.ScheduledTask{ID: 22, Type: db.TaskTypeCron}, nil)

	s.bot.On("CreateSimpleThread", s.ctx, "ch22", mock.Anything, mock.Anything).Return("thread-22", nil).Once()
	s.store.On("LinkTaskThread", s.ctx, mock.MatchedBy(func(ch *db.Channel) bool {
		return ch.ChannelID == "thread-22" && ch.ParentID == "ch22"
	}), int64(22), "thread-22").Return(nil)
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
	s.store.On("UpdateSessionID", s.ctx, "thread-22", "s22").Return(nil)

	// Should not fail — invite errors are just logged
	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "Turn 1", resp)
	s.bot.AssertExpectations(s.T())
}

// --- Worktree tests ---

func (s *TaskExecutorSuite) TestWorktreeFirstRun() {
	task := &db.ScheduledTask{
		ID: 10, ChannelID: "ch1", Prompt: "build", Type: db.TaskTypeCron,
		Schedule: "0 * * * *", Worktree: true,
	}

	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ChannelID: "ch1", DirPath: "/proj", SessionID: "sess-1", Platform: types.PlatformLocal,
	}, nil)
	s.store.On("GetScheduledTask", s.ctx, int64(10)).Return(&db.ScheduledTask{ID: 10, Type: db.TaskTypeCron}, nil)
	s.allowBotInserts()

	// Mock worktree creator
	s.executor.SetWorktreeCreator(&worktree.Creator{
		Sys: &mockWorktreeSys{},
		Run: func(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
			if name == "git" && len(args) > 0 && args[0] == "rev-parse" {
				if args[1] == "--abbrev-ref" {
					return []byte("main\n"), nil
				}
			}
			return nil, nil // git worktree add succeeds
		},
	})

	s.store.On("UpdateScheduledTaskOriginBranch", s.ctx, int64(10), "main").Return(nil)

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		return strings.Contains(req.DirPath, ".worktrees/task-10-")
	})).Return(&agent.AgentResponse{Response: "done", SessionID: "s2"}, nil)

	s.store.On("UpdateSessionID", s.ctx, "ch1", "s2").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "done", resp)
}

func (s *TaskExecutorSuite) TestWorktreeFirstRunOriginBranchPersistError() {
	task := &db.ScheduledTask{
		ID: 10, ChannelID: "ch1", Prompt: "build", Type: db.TaskTypeCron,
		Schedule: "0 * * * *", Worktree: true,
	}

	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ChannelID: "ch1", DirPath: "/proj", SessionID: "sess-1", Platform: types.PlatformLocal,
	}, nil)
	s.store.On("GetScheduledTask", s.ctx, int64(10)).Return(&db.ScheduledTask{ID: 10, Type: db.TaskTypeCron}, nil)
	s.allowBotInserts()

	s.executor.SetWorktreeCreator(&worktree.Creator{
		Sys: &mockWorktreeSys{},
		Run: func(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
			if name == "git" && len(args) > 0 && args[0] == "rev-parse" {
				if args[1] == "--abbrev-ref" {
					return []byte("main\n"), nil
				}
			}
			return nil, nil
		},
	})

	// Error persisting origin branch — should log but continue.
	s.store.On("UpdateScheduledTaskOriginBranch", s.ctx, int64(10), "main").Return(errors.New("db error"))

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		return strings.Contains(req.DirPath, ".worktrees/task-10-")
	})).Return(&agent.AgentResponse{Response: "done", SessionID: "s2"}, nil)

	s.store.On("UpdateSessionID", s.ctx, "ch1", "s2").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "done", resp)
}

func (s *TaskExecutorSuite) TestWorktreeSubsequentRun() {
	task := &db.ScheduledTask{
		ID: 10, ChannelID: "ch1", Prompt: "build", Type: db.TaskTypeCron,
		Schedule: "0 * * * *", Worktree: true, ThreadID: "wt-thread",
	}

	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ChannelID: "ch1", DirPath: "/proj", Platform: types.PlatformLocal,
	}, nil)
	// Worktree subsequent run: get thread channel to get its DirPath
	s.store.On("GetChannel", s.ctx, "wt-thread").Return(&db.Channel{
		ChannelID: "wt-thread", DirPath: "/proj/.worktrees/task-10-abc", SessionID: "sess-wt",
	}, nil)
	s.store.On("GetScheduledTask", s.ctx, int64(10)).Return(&db.ScheduledTask{
		ID: 10, Type: db.TaskTypeCron, ThreadID: "wt-thread",
	}, nil)
	s.allowBotInserts()

	s.executor.SetWorktreeCreator(&worktree.Creator{
		Sys: &mockWorktreeSys{},
		Run: func(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
			return nil, nil
		},
	})

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		return req.DirPath == "/proj/.worktrees/task-10-abc" && req.SessionID == "sess-wt"
	})).Return(&agent.AgentResponse{Response: "done2", SessionID: "s3"}, nil)

	s.store.On("UpdateSessionID", s.ctx, "wt-thread", "s3").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "done2", resp)
}

func (s *TaskExecutorSuite) TestWorktreeDanglingThreadCreatesNewWorktree() {
	// Task has ThreadID pointing at a channel that no longer exists (e.g. the
	// thread was deleted from the UI without clearing the task's thread_id).
	// The executor must fall back to first-run behavior: create a new worktree
	// instead of reusing the parent channel's dirPath (which would cause a
	// duplicate Docker mount because dirPath == parentDirPath).
	task := &db.ScheduledTask{
		ID: 10, ChannelID: "ch1", Prompt: "build", Type: db.TaskTypeCron,
		Schedule: "0 * * * *", Worktree: true, ThreadID: "stale-thread",
	}

	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ChannelID: "ch1", DirPath: "/proj", SessionID: "sess-1", Platform: types.PlatformLocal,
	}, nil)
	// Dangling ThreadID — channel lookup returns nil.
	s.store.On("GetChannel", s.ctx, "stale-thread").Return(nil, nil)
	s.store.On("GetScheduledTask", s.ctx, int64(10)).Return(&db.ScheduledTask{ID: 10, Type: db.TaskTypeCron}, nil)
	s.allowBotInserts()

	s.executor.SetWorktreeCreator(&worktree.Creator{
		Sys: &mockWorktreeSys{},
		Run: func(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
			if name == "git" && len(args) > 0 && args[0] == "rev-parse" && len(args) > 1 && args[1] == "--abbrev-ref" {
				return []byte("main\n"), nil
			}
			return nil, nil
		},
	})

	s.store.On("UpdateScheduledTaskOriginBranch", s.ctx, int64(10), "main").Return(nil)

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		// DirPath must be the NEW worktree, not the parent channel's path.
		return strings.Contains(req.DirPath, ".worktrees/task-10-") && req.DirPath != "/proj"
	})).Return(&agent.AgentResponse{Response: "done", SessionID: "s2"}, nil)

	s.store.On("UpdateSessionID", s.ctx, "ch1", "s2").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "done", resp)
}

func (s *TaskExecutorSuite) TestWorktreeCreationError() {
	task := &db.ScheduledTask{
		ID: 10, ChannelID: "ch1", Prompt: "build", Type: db.TaskTypeCron,
		Schedule: "0 * * * *", Worktree: true,
	}

	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ChannelID: "ch1", DirPath: "/proj",
	}, nil)

	s.store.On("UpdateScheduledTaskOriginBranch", s.ctx, int64(10), "main").Return(nil)

	s.executor.SetWorktreeCreator(&worktree.Creator{
		Sys: &mockWorktreeSys{},
		Run: func(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
			if name == "git" && len(args) > 0 && args[0] == "rev-parse" {
				return []byte("main\n"), nil
			}
			return []byte("fatal: error"), errors.New("exit 1")
		},
	})

	_, err := s.executor.ExecuteTask(s.ctx, task)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "creating worktree for task 10")
}

func (s *TaskExecutorSuite) TestWorktreeBranchDetectionError() {
	task := &db.ScheduledTask{
		ID: 10, ChannelID: "ch1", Prompt: "build", Type: db.TaskTypeCron,
		Schedule: "0 * * * *", Worktree: true,
	}

	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ChannelID: "ch1", DirPath: "/proj",
	}, nil)

	s.executor.SetWorktreeCreator(&worktree.Creator{
		Sys: &mockWorktreeSys{},
		Run: func(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
			return nil, errors.New("not a git repo")
		},
	})

	_, err := s.executor.ExecuteTask(s.ctx, task)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "getting current branch")
}

func (s *TaskExecutorSuite) TestWorktreeDetachedHead() {
	task := &db.ScheduledTask{
		ID: 10, ChannelID: "ch1", Prompt: "build", Type: db.TaskTypeCron,
		Schedule: "0 * * * *", Worktree: true,
	}

	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ChannelID: "ch1", DirPath: "/proj", Platform: types.PlatformLocal,
	}, nil)
	s.store.On("GetScheduledTask", s.ctx, int64(10)).Return(&db.ScheduledTask{ID: 10, Type: db.TaskTypeCron}, nil)
	s.allowBotInserts()

	s.executor.SetWorktreeCreator(&worktree.Creator{
		Sys: &mockWorktreeSys{},
		Run: func(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
			if name == "git" && len(args) > 1 && args[0] == "rev-parse" && args[1] == "--abbrev-ref" {
				return []byte("HEAD\n"), nil
			}
			if name == "git" && len(args) > 0 && args[0] == "rev-parse" {
				return []byte("abc123\n"), nil
			}
			return nil, nil
		},
	})

	s.store.On("UpdateScheduledTaskOriginBranch", s.ctx, int64(10), "abc123").Return(nil)

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		return strings.Contains(req.DirPath, ".worktrees/task-10-")
	})).Return(&agent.AgentResponse{Response: "ok", SessionID: "s4"}, nil)
	s.store.On("UpdateSessionID", s.ctx, "ch1", "s4").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "ok", resp)
}

func (s *TaskExecutorSuite) TestWorktreeDetachedHeadFallbackError() {
	task := &db.ScheduledTask{
		ID: 10, ChannelID: "ch1", Prompt: "build", Type: db.TaskTypeCron,
		Schedule: "0 * * * *", Worktree: true,
	}

	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ChannelID: "ch1", DirPath: "/proj",
	}, nil)

	s.executor.SetWorktreeCreator(&worktree.Creator{
		Sys: &mockWorktreeSys{},
		Run: func(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
			if name == "git" && len(args) > 1 && args[0] == "rev-parse" && args[1] == "--abbrev-ref" {
				return []byte("HEAD\n"), nil
			}
			// Second rev-parse HEAD fails
			return nil, errors.New("git error")
		},
	})

	_, err := s.executor.ExecuteTask(s.ctx, task)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "getting current branch")
}

func (s *TaskExecutorSuite) TestWorktreeFalsePreservesExisting() {
	task := &db.ScheduledTask{
		ID: 10, ChannelID: "ch1", Prompt: "build", Type: db.TaskTypeCron,
		Schedule: "0 * * * *", Worktree: false,
	}

	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ChannelID: "ch1", DirPath: "/proj",
	}, nil)
	s.store.On("GetScheduledTask", s.ctx, int64(10)).Return(&db.ScheduledTask{ID: 10, Type: db.TaskTypeCron}, nil)
	s.allowBotInserts()

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		return req.DirPath == "/proj"
	})).Return(&agent.AgentResponse{Response: "ok", SessionID: "s5"}, nil)
	s.store.On("UpdateSessionID", s.ctx, "ch1", "s5").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "ok", resp)
}

func (s *TaskExecutorSuite) TestWorktreeTaskSetsParentDirPath() {
	task := &db.ScheduledTask{
		ID: 10, ChannelID: "ch1", Prompt: "build", Type: db.TaskTypeCron,
		Schedule: "0 * * * *", Worktree: true,
	}

	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ChannelID: "ch1", DirPath: "/proj", SessionID: "sess-1", Platform: types.PlatformLocal,
	}, nil)
	s.store.On("GetScheduledTask", s.ctx, int64(10)).Return(&db.ScheduledTask{ID: 10, Type: db.TaskTypeCron}, nil)
	s.allowBotInserts()

	s.executor.SetWorktreeCreator(&worktree.Creator{
		Sys: &mockWorktreeSys{},
		Run: func(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
			if name == "git" && len(args) > 0 && args[0] == "rev-parse" {
				return []byte("main\n"), nil
			}
			return nil, nil
		},
	})

	s.store.On("UpdateScheduledTaskOriginBranch", s.ctx, int64(10), "main").Return(nil)

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		return req.ParentDirPath == "/proj" && strings.Contains(req.DirPath, ".worktrees/task-10-")
	})).Return(&agent.AgentResponse{Response: "done", SessionID: "s2"}, nil)

	s.store.On("UpdateSessionID", s.ctx, "ch1", "s2").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "done", resp)
}

func (s *TaskExecutorSuite) TestNonWorktreeTaskNoParentDirPath() {
	task := &db.ScheduledTask{
		ID: 10, ChannelID: "ch1", Prompt: "build", Type: db.TaskTypeCron,
		Schedule: "0 * * * *", Worktree: false,
	}

	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ChannelID: "ch1", DirPath: "/proj",
	}, nil)
	s.store.On("GetScheduledTask", s.ctx, int64(10)).Return(&db.ScheduledTask{ID: 10, Type: db.TaskTypeCron}, nil)
	s.allowBotInserts()

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		return req.ParentDirPath == "" && req.DirPath == "/proj"
	})).Return(&agent.AgentResponse{Response: "ok", SessionID: "s5"}, nil)
	s.store.On("UpdateSessionID", s.ctx, "ch1", "s5").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "ok", resp)
}

func (s *TaskExecutorSuite) TestNonWorktreeTaskOnWorktreeChannelSetsParentDirPath() {
	task := &db.ScheduledTask{
		ID: 10, ChannelID: "wt-ch", Prompt: "build", Type: db.TaskTypeCron,
		Schedule: "0 * * * *", Worktree: false,
	}

	// The task's channel is itself a worktree channel.
	s.store.On("GetChannel", s.ctx, "wt-ch").Return(&db.Channel{
		ChannelID: "wt-ch", DirPath: "/proj/.worktrees/wt-1", ParentID: "parent-ch", Worktree: true,
	}, nil)
	// Parent channel lookup returns the original project dir.
	s.store.On("GetChannel", s.ctx, "parent-ch").Return(&db.Channel{
		ChannelID: "parent-ch", DirPath: "/proj",
	}, nil)
	s.store.On("GetScheduledTask", s.ctx, int64(10)).Return(&db.ScheduledTask{ID: 10, Type: db.TaskTypeCron}, nil)
	s.allowBotInserts()

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		return req.ParentDirPath == "/proj" && req.DirPath == "/proj/.worktrees/wt-1"
	})).Return(&agent.AgentResponse{Response: "ok", SessionID: "s6"}, nil)
	s.store.On("UpdateSessionID", s.ctx, "wt-ch", "s6").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "ok", resp)
}

func (s *TaskExecutorSuite) TestNonWorktreeTaskOnWorktreeChannelParentLookupError() {
	task := &db.ScheduledTask{
		ID: 10, ChannelID: "wt-ch", Prompt: "build", Type: db.TaskTypeCron,
		Schedule: "0 * * * *", Worktree: false,
	}

	s.store.On("GetChannel", s.ctx, "wt-ch").Return(&db.Channel{
		ChannelID: "wt-ch", DirPath: "/proj/.worktrees/wt-1", ParentID: "parent-ch", Worktree: true,
	}, nil)
	// Parent lookup fails — parentDirPath stays empty (graceful fallback).
	s.store.On("GetChannel", s.ctx, "parent-ch").Return(nil, errors.New("db error"))
	s.store.On("GetScheduledTask", s.ctx, int64(10)).Return(&db.ScheduledTask{ID: 10, Type: db.TaskTypeCron}, nil)
	s.allowBotInserts()

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		return req.ParentDirPath == "" && req.DirPath == "/proj/.worktrees/wt-1"
	})).Return(&agent.AgentResponse{Response: "ok", SessionID: "s7"}, nil)
	s.store.On("UpdateSessionID", s.ctx, "wt-ch", "s7").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "ok", resp)
}

func (s *TaskExecutorSuite) TestWorktreeFirstRunWithOriginBranch() {
	task := &db.ScheduledTask{
		ID: 10, ChannelID: "ch1", Prompt: "build", Type: db.TaskTypeCron,
		Schedule: "0 * * * *", Worktree: true, OriginBranch: "develop",
	}

	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ChannelID: "ch1", DirPath: "/proj", SessionID: "sess-1", Platform: types.PlatformLocal,
	}, nil)
	s.store.On("GetScheduledTask", s.ctx, int64(10)).Return(&db.ScheduledTask{ID: 10, Type: db.TaskTypeCron}, nil)
	s.allowBotInserts()

	var createdBranch string
	s.executor.SetWorktreeCreator(&worktree.Creator{
		Sys: &mockWorktreeSys{},
		Run: func(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
			// Should NOT be called for rev-parse since OriginBranch is set.
			if name == "git" && len(args) > 0 && args[0] == "rev-parse" {
				s.Fail("getCurrentBranch should not be called when OriginBranch is set")
			}
			// Capture the branch used for worktree add.
			if name == "git" && len(args) > 2 && args[0] == "worktree" {
				createdBranch = args[len(args)-1] // last arg is the branch
			}
			return nil, nil
		},
	})

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		return strings.Contains(req.DirPath, ".worktrees/task-10-")
	})).Return(&agent.AgentResponse{Response: "done", SessionID: "s2"}, nil)
	s.store.On("UpdateSessionID", s.ctx, "ch1", "s2").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "done", resp)
	require.Equal(s.T(), "develop", createdBranch)
}

func (s *TaskExecutorSuite) TestWorktreeFirstRunPersistsDetectedBranch() {
	task := &db.ScheduledTask{
		ID: 10, ChannelID: "ch1", Prompt: "build", Type: db.TaskTypeCron,
		Schedule: "0 * * * *", Worktree: true, OriginBranch: "", // empty — auto-detect
	}

	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ChannelID: "ch1", DirPath: "/proj", SessionID: "sess-1", Platform: types.PlatformLocal,
	}, nil)
	s.store.On("GetScheduledTask", s.ctx, int64(10)).Return(&db.ScheduledTask{ID: 10, Type: db.TaskTypeCron}, nil)
	s.store.On("UpdateScheduledTaskOriginBranch", s.ctx, int64(10), "main").Return(nil)
	s.allowBotInserts()

	s.executor.SetWorktreeCreator(&worktree.Creator{
		Sys: &mockWorktreeSys{},
		Run: func(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
			if name == "git" && len(args) > 0 && args[0] == "rev-parse" {
				return []byte("main\n"), nil
			}
			return nil, nil
		},
	})

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		return strings.Contains(req.DirPath, ".worktrees/task-10-")
	})).Return(&agent.AgentResponse{Response: "done", SessionID: "s2"}, nil)
	s.store.On("UpdateSessionID", s.ctx, "ch1", "s2").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)

	_, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	s.store.AssertCalled(s.T(), "UpdateScheduledTaskOriginBranch", s.ctx, int64(10), "main")
}

func (s *TaskExecutorSuite) TestWorktreeUpdateBeforeRunSystemPrompt() {
	task := &db.ScheduledTask{
		ID: 10, ChannelID: "ch1", Prompt: "build", Type: db.TaskTypeCron,
		Schedule: "0 * * * *", Worktree: true, OriginBranch: "main",
		UpdateBeforeRun: true, ThreadID: "thread-1",
	}

	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ChannelID: "ch1", DirPath: "/proj",
	}, nil)
	s.store.On("GetChannel", s.ctx, "thread-1").Return(&db.Channel{
		ChannelID: "thread-1", DirPath: "/proj/.worktrees/task-10-abc", SessionID: "s-thread",
	}, nil)
	s.store.On("GetScheduledTask", s.ctx, int64(10)).Return(&db.ScheduledTask{ID: 10, Type: db.TaskTypeCron}, nil)
	s.allowBotInserts()

	s.executor.SetWorktreeCreator(&worktree.Creator{Sys: &mockWorktreeSys{}})

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		msg := req.Messages[0].Content
		return strings.Contains(msg, "git rebase origin/main") &&
			strings.Contains(msg, "git fetch origin main") &&
			strings.Contains(msg, "git stash") &&
			strings.HasSuffix(msg, "build") &&
			!strings.Contains(req.SystemPrompt, "git rebase")
	})).Return(&agent.AgentResponse{Response: "done", SessionID: "s3"}, nil)
	s.store.On("UpdateSessionID", s.ctx, "thread-1", "s3").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "done", resp)
}

func (s *TaskExecutorSuite) TestWorktreeNoUpdatePromptWhenDisabled() {
	task := &db.ScheduledTask{
		ID: 10, ChannelID: "ch1", Prompt: "build", Type: db.TaskTypeCron,
		Schedule: "0 * * * *", Worktree: true, OriginBranch: "main",
		UpdateBeforeRun: false, ThreadID: "thread-1",
	}

	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ChannelID: "ch1", DirPath: "/proj",
	}, nil)
	s.store.On("GetChannel", s.ctx, "thread-1").Return(&db.Channel{
		ChannelID: "thread-1", DirPath: "/proj/.worktrees/task-10-abc", SessionID: "s-thread",
	}, nil)
	s.store.On("GetScheduledTask", s.ctx, int64(10)).Return(&db.ScheduledTask{ID: 10, Type: db.TaskTypeCron}, nil)
	s.allowBotInserts()

	s.executor.SetWorktreeCreator(&worktree.Creator{Sys: &mockWorktreeSys{}})

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		return req.Messages[0].Content == "build" && !strings.Contains(req.Messages[0].Content, "git rebase")
	})).Return(&agent.AgentResponse{Response: "done", SessionID: "s3"}, nil)
	s.store.On("UpdateSessionID", s.ctx, "thread-1", "s3").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil)

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "done", resp)
}

// mockWorktreeSys is a minimal System implementation for worktree tests.
type mockWorktreeSys struct{}

func (m *mockWorktreeSys) MkdirAll(string, os.FileMode) error          { return nil }
func (m *mockWorktreeSys) WriteFile(string, []byte, os.FileMode) error { return nil }
func (m *mockWorktreeSys) ReadFile(string) ([]byte, error)             { return nil, nil }
func (m *mockWorktreeSys) UserHomeDir() (string, error)                { return "/home/test", nil }

func (s *TaskExecutorSuite) TestRefreshConfigReloads() {
	called := false
	s.executor.configLoad = func() (*config.Config, error) {
		called = true
		return &config.Config{
			ContainerTimeout: 99 * time.Second,
			StreamingEnabled: true,
		}, nil
	}

	timeout, streaming := s.executor.refreshConfig()
	require.True(s.T(), called)
	require.Equal(s.T(), 99*time.Second, timeout)
	require.True(s.T(), streaming)
}

func (s *TaskExecutorSuite) TestRefreshConfigFallbackOnError() {
	// Set initial values.
	s.executor.containerTimeout.Store(int64(30 * time.Second))
	s.executor.streamingEnabled.Store(true)

	s.executor.configLoad = func() (*config.Config, error) {
		return nil, errors.New("reload failed")
	}

	timeout, streaming := s.executor.refreshConfig()
	require.Equal(s.T(), 30*time.Second, timeout)
	require.True(s.T(), streaming)
}

func (s *TaskExecutorSuite) TestRefreshConfigNilLoader() {
	s.executor.containerTimeout.Store(int64(42 * time.Second))
	s.executor.streamingEnabled.Store(false)

	// configLoad is already nil from SetupTest (passed nil).
	timeout, streaming := s.executor.refreshConfig()
	require.Equal(s.T(), 42*time.Second, timeout)
	require.False(s.T(), streaming)
}

// --- Workflow Engine mock (used only in scheduled workflow tests) ---

type mockWorkflowEngine struct {
	mock.Mock
}

func (m *mockWorkflowEngine) StartRun(ctx context.Context, opts workflow.StartRunOptions) (string, error) {
	args := m.Called(ctx, opts)
	return args.String(0), args.Error(1)
}

func (m *mockWorkflowEngine) ResumeRun(ctx context.Context, runID, response string) error {
	return m.Called(ctx, runID, response).Error(0)
}

func (m *mockWorkflowEngine) CancelRun(ctx context.Context, runID string) error {
	return m.Called(ctx, runID).Error(0)
}

func (m *mockWorkflowEngine) DeleteRun(ctx context.Context, runID string) error {
	return m.Called(ctx, runID).Error(0)
}

func (m *mockWorkflowEngine) RetryRun(ctx context.Context, runID string) (string, error) {
	args := m.Called(ctx, runID)
	return args.String(0), args.Error(1)
}

func (m *mockWorkflowEngine) GetRun(ctx context.Context, runID string) (*db.WorkflowRun, []*db.NodeRun, error) {
	args := m.Called(ctx, runID)
	var run *db.WorkflowRun
	if args.Get(0) != nil {
		run = args.Get(0).(*db.WorkflowRun)
	}
	var nodes []*db.NodeRun
	if args.Get(1) != nil {
		nodes = args.Get(1).([]*db.NodeRun)
	}
	return run, nodes, args.Error(2)
}

func (m *mockWorkflowEngine) ListRuns(ctx context.Context, channelID string, limit, offset int) ([]*db.WorkflowRun, error) {
	args := m.Called(ctx, channelID, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*db.WorkflowRun), args.Error(1)
}

func (m *mockWorkflowEngine) ListWorkflows(ctx context.Context, dirPath, parentDirPath string) ([]config.WorkflowDef, error) {
	args := m.Called(ctx, dirPath, parentDirPath)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]config.WorkflowDef), args.Error(1)
}

func (m *mockWorkflowEngine) RecoverRuns(ctx context.Context) error {
	return m.Called(ctx).Error(0)
}

// --- Scheduled workflow execution tests ---

func (s *TaskExecutorSuite) TestExecuteWorkflowTask() {
	task := &db.ScheduledTask{
		ID:             1,
		ChannelID:      "ch1",
		WorkflowName:   "fix-issue",
		WorkflowInputs: `{"issue_url":"https://github.com/org/repo/issues/42"}`,
	}

	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ChannelID: "ch1",
		DirPath:   "/work/project",
	}, nil)

	wfEngine := new(mockWorkflowEngine)
	wfEngine.On("StartRun", s.ctx, workflow.StartRunOptions{
		WorkflowName: "fix-issue",
		ChannelID:    "ch1",
		DirPath:      "/work/project",
		Inputs:       map[string]string{"issue_url": "https://github.com/org/repo/issues/42"},
	}).Return("run-abc123", nil)

	s.executor.SetWorkflowEngine(wfEngine)

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Contains(s.T(), resp, "run-abc123")

	wfEngine.AssertExpectations(s.T())
	s.store.AssertExpectations(s.T())
}

func (s *TaskExecutorSuite) TestExecuteWorkflowTaskNoInputs() {
	task := &db.ScheduledTask{
		ID:           2,
		ChannelID:    "ch1",
		WorkflowName: "validate",
	}

	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ChannelID: "ch1",
		DirPath:   "/work/project",
	}, nil)

	wfEngine := new(mockWorkflowEngine)
	wfEngine.On("StartRun", s.ctx, workflow.StartRunOptions{
		WorkflowName: "validate",
		ChannelID:    "ch1",
		DirPath:      "/work/project",
		Inputs:       nil,
	}).Return("run-xyz789", nil)

	s.executor.SetWorkflowEngine(wfEngine)

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Contains(s.T(), resp, "run-xyz789")

	wfEngine.AssertExpectations(s.T())
}

func (s *TaskExecutorSuite) TestExecuteWorkflowTaskEngineNotConfigured() {
	task := &db.ScheduledTask{
		ID:           3,
		ChannelID:    "ch1",
		WorkflowName: "fix-issue",
	}

	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ChannelID: "ch1",
		DirPath:   "/work/project",
	}, nil)

	// workflowEngine not set — should error.
	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "workflow engine not configured")
	require.Empty(s.T(), resp)
}

func (s *TaskExecutorSuite) TestExecuteWorkflowTaskStartRunError() {
	task := &db.ScheduledTask{
		ID:           4,
		ChannelID:    "ch1",
		WorkflowName: "broken",
	}

	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ChannelID: "ch1",
		DirPath:   "/work/project",
	}, nil)

	wfEngine := new(mockWorkflowEngine)
	wfEngine.On("StartRun", s.ctx, mock.Anything).Return("", errors.New("workflow not found"))

	s.executor.SetWorkflowEngine(wfEngine)

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "starting workflow")
	require.Empty(s.T(), resp)
}

func (s *TaskExecutorSuite) TestExecuteWorkflowTaskInvalidInputs() {
	task := &db.ScheduledTask{
		ID:             5,
		ChannelID:      "ch1",
		WorkflowName:   "fix-issue",
		WorkflowInputs: `not-valid-json`,
	}

	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ChannelID: "ch1",
		DirPath:   "/work/project",
	}, nil)

	wfEngine := new(mockWorkflowEngine)
	s.executor.SetWorkflowEngine(wfEngine)

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "parsing workflow inputs")
	require.Empty(s.T(), resp)
}

// --- OrchestratorCommandsSuite: tests for commands.go functions ---

type OrchestratorCommandsSuite struct {
	suite.Suite
	store *testutil.MockStore
	bot   *MockBot
	sched *testutil.MockScheduler
	orch  *Orchestrator
	ctx   context.Context
}

func TestOrchestratorCommandsSuite(t *testing.T) {
	suite.Run(t, new(OrchestratorCommandsSuite))
}

func (s *OrchestratorCommandsSuite) SetupTest() {
	s.store = new(testutil.MockStore)
	s.bot = new(MockBot)
	s.sched = new(testutil.MockScheduler)
	s.ctx = context.Background()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s.orch = New(s.store, s.bot, new(MockRunner), s.sched, logger, config.Config{}, nil)

	// Default: bot inserts are noisy in command tests; allow silently.
	s.store.On("InsertMessage", mock.Anything, mock.MatchedBy(func(msg *db.Message) bool {
		return msg.IsBot
	})).Return(nil).Maybe()
	// GetChannel for storeBotMessage calls.
	s.store.On("GetChannel", mock.Anything, mock.Anything).Return(nil, nil).Maybe()
}

// sendMessageMatcher returns a mock.MatchedBy predicate that checks content contains the given substring.
func sendMessageContains(sub string) any {
	return mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return strings.Contains(out.Content, sub)
	})
}

// --- SetWorkflowEngine ---

func (s *OrchestratorCommandsSuite) TestSetWorkflowEngine() {
	require.Nil(s.T(), s.orch.workflowEngine)
	wfe := new(mockWorkflowEngine)
	s.orch.SetWorkflowEngine(wfe)
	require.Same(s.T(), wfe, s.orch.workflowEngine)
}

// --- channelConfig ---

func (s *OrchestratorCommandsSuite) TestChannelConfigNilChannel() {
	s.orch.cfg.Store(&config.Config{LoopDir: "/loop"})
	cfg := s.orch.channelConfig(nil)
	require.Equal(s.T(), "/loop", cfg.LoopDir)
}

func (s *OrchestratorCommandsSuite) TestChannelConfigNoDirPath() {
	s.orch.cfg.Store(&config.Config{LoopDir: "/loop"})
	ch := &db.Channel{ChannelID: "ch1", DirPath: ""}
	cfg := s.orch.channelConfig(ch)
	require.Equal(s.T(), "/loop", cfg.LoopDir)
}

func (s *OrchestratorCommandsSuite) TestChannelConfigWithDirPath() {
	base := &config.Config{LoopDir: "/loop", PromptShortcuts: []config.PromptShortcut{{Name: "global", Prompt: "global prompt"}}}
	s.orch.cfg.Store(base)
	s.orch.loadProjectConfig = func(dirPath string, mainCfg *config.Config) (*config.Config, error) {
		merged := *mainCfg
		merged.LoopDir = "/project/loop"
		return &merged, nil
	}
	ch := &db.Channel{ChannelID: "ch1", DirPath: "/project"}
	cfg := s.orch.channelConfig(ch)
	require.Equal(s.T(), "/project/loop", cfg.LoopDir)
}

func (s *OrchestratorCommandsSuite) TestChannelConfigLoadProjectConfigError() {
	base := &config.Config{LoopDir: "/loop"}
	s.orch.cfg.Store(base)
	s.orch.loadProjectConfig = func(_ string, _ *config.Config) (*config.Config, error) {
		return nil, errors.New("load failed")
	}
	ch := &db.Channel{ChannelID: "ch1", DirPath: "/project"}
	cfg := s.orch.channelConfig(ch)
	// Falls back to global config.
	require.Equal(s.T(), "/loop", cfg.LoopDir)
}

// --- handleShortcutsInteraction ---

func (s *OrchestratorCommandsSuite) TestHandleShortcutsInteractionNoShortcuts() {
	s.orch.cfg.Store(&config.Config{PromptShortcuts: nil})
	s.bot.On("SendMessage", s.ctx, sendMessageContains("No prompt shortcuts configured.")).Return(nil)

	s.orch.handleShortcutsInteraction(s.ctx, &bot.Interaction{ChannelID: "ch1"}, nil)

	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorCommandsSuite) TestHandleShortcutsInteractionListsShortcuts() {
	s.orch.cfg.Store(&config.Config{
		PromptShortcuts: []config.PromptShortcut{
			{Name: "review", Description: "Review code", Prompt: "Review this"},
			{Name: "test", Prompt: "Run tests please"},
		},
	})
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return strings.Contains(out.Content, "Available shortcuts:") &&
			strings.Contains(out.Content, "**review**") &&
			strings.Contains(out.Content, "Review code") &&
			strings.Contains(out.Content, "**test**") &&
			strings.Contains(out.Content, "Run tests please")
	})).Return(nil)

	s.orch.handleShortcutsInteraction(s.ctx, &bot.Interaction{ChannelID: "ch1"}, nil)

	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorCommandsSuite) TestHandleShortcutsInteractionDescriptionTruncatedFromPrompt() {
	longPrompt := strings.Repeat("x", 100)
	s.orch.cfg.Store(&config.Config{
		PromptShortcuts: []config.PromptShortcut{
			{Name: "long", Prompt: longPrompt},
		},
	})
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		// Prompt is truncated to 60 chars when no description.
		return strings.Contains(out.Content, "**long**") &&
			!strings.Contains(out.Content, longPrompt)
	})).Return(nil)

	s.orch.handleShortcutsInteraction(s.ctx, &bot.Interaction{ChannelID: "ch1"}, nil)

	s.bot.AssertExpectations(s.T())
}

// --- handleShortcutInteraction ---

func (s *OrchestratorCommandsSuite) TestHandleShortcutInteractionNoName() {
	s.bot.On("SendMessage", s.ctx, sendMessageContains("Usage:")).Return(nil)

	s.orch.handleShortcutInteraction(s.ctx, &bot.Interaction{
		ChannelID: "ch1",
		Options:   map[string]string{"name": ""},
	}, nil)

	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorCommandsSuite) TestHandleShortcutInteractionUnknown() {
	s.orch.cfg.Store(&config.Config{PromptShortcuts: []config.PromptShortcut{{Name: "review", Prompt: "Review this"}}})
	s.bot.On("SendMessage", s.ctx, sendMessageContains("Unknown shortcut: missing")).Return(nil)

	s.orch.handleShortcutInteraction(s.ctx, &bot.Interaction{
		ChannelID: "ch1",
		Options:   map[string]string{"name": "missing"},
	}, nil)

	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorCommandsSuite) TestHandleShortcutInteractionHappyPath() {
	s.orch.cfg.Store(&config.Config{
		PromptShortcuts: []config.PromptShortcut{
			{Name: "review", Prompt: "Please review the code"},
		},
	})
	s.bot.On("HandleIncomingMessage", s.ctx, "ch1", "user1", "Please review the code", "").Once()

	s.orch.handleShortcutInteraction(s.ctx, &bot.Interaction{
		ChannelID: "ch1",
		AuthorID:  "user1",
		Options:   map[string]string{"name": "review"},
	}, nil)

	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorCommandsSuite) TestHandleShortcutInteractionResolveError() {
	// PromptShortcut with both Prompt and PromptPath set triggers a resolve error.
	s.orch.cfg.Store(&config.Config{
		PromptShortcuts: []config.PromptShortcut{
			{Name: "bad", Prompt: "inline", PromptPath: "file.md"},
		},
	})
	s.bot.On("SendMessage", s.ctx, sendMessageContains("Failed to resolve shortcut prompt:")).Return(nil)

	s.orch.handleShortcutInteraction(s.ctx, &bot.Interaction{
		ChannelID: "ch1",
		AuthorID:  "user1",
		Options:   map[string]string{"name": "bad"},
	}, nil)

	s.bot.AssertExpectations(s.T())
}

// --- handleWorkflowsInteraction ---

func (s *OrchestratorCommandsSuite) TestHandleWorkflowsInteractionNoEngine() {
	// workflowEngine is nil by default.
	s.bot.On("SendMessage", s.ctx, sendMessageContains("Workflow engine not configured.")).Return(nil)

	s.orch.handleWorkflowsInteraction(s.ctx, &bot.Interaction{ChannelID: "ch1"}, nil)

	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorCommandsSuite) TestHandleWorkflowsInteractionListError() {
	wfe := new(mockWorkflowEngine)
	s.orch.SetWorkflowEngine(wfe)
	wfe.On("ListWorkflows", s.ctx, "", "").Return(nil, errors.New("db error"))
	s.bot.On("SendMessage", s.ctx, sendMessageContains("Failed to list workflows.")).Return(nil)

	s.orch.handleWorkflowsInteraction(s.ctx, &bot.Interaction{ChannelID: "ch1"}, nil)

	wfe.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorCommandsSuite) TestHandleWorkflowsInteractionEmpty() {
	wfe := new(mockWorkflowEngine)
	s.orch.SetWorkflowEngine(wfe)
	wfe.On("ListWorkflows", s.ctx, "", "").Return([]config.WorkflowDef{}, nil)
	s.bot.On("SendMessage", s.ctx, sendMessageContains("No workflows configured.")).Return(nil)

	s.orch.handleWorkflowsInteraction(s.ctx, &bot.Interaction{ChannelID: "ch1"}, nil)

	wfe.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorCommandsSuite) TestHandleWorkflowsInteractionListsWorkflows() {
	wfe := new(mockWorkflowEngine)
	s.orch.SetWorkflowEngine(wfe)
	wfe.On("ListWorkflows", s.ctx, "/project", "").Return([]config.WorkflowDef{
		{Name: "fix-issue", Description: "Fix a GitHub issue"},
		{Name: "validate", Nodes: []config.NodeDef{{}, {}}},
	}, nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return strings.Contains(out.Content, "Available workflows:") &&
			strings.Contains(out.Content, "**fix-issue**") &&
			strings.Contains(out.Content, "Fix a GitHub issue") &&
			strings.Contains(out.Content, "**validate**") &&
			strings.Contains(out.Content, "2 nodes")
	})).Return(nil)

	ch := &db.Channel{ChannelID: "ch1", DirPath: "/project"}
	s.orch.handleWorkflowsInteraction(s.ctx, &bot.Interaction{ChannelID: "ch1"}, ch)

	wfe.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorCommandsSuite) TestHandleWorkflowsInteractionWithParentDirPath() {
	wfe := new(mockWorkflowEngine)
	s.orch.SetWorkflowEngine(wfe)

	// Override the maybe GetChannel to return a parent channel.
	s.store.ExpectedCalls = nil
	s.store.On("InsertMessage", mock.Anything, mock.Anything).Return(nil).Maybe()
	s.store.On("GetChannel", mock.Anything, "parent-ch").Return(&db.Channel{ChannelID: "parent-ch", DirPath: "/parent"}, nil)
	s.store.On("GetChannel", mock.Anything, mock.Anything).Return(nil, nil).Maybe()

	wfe.On("ListWorkflows", s.ctx, "/project", "/parent").Return([]config.WorkflowDef{
		{Name: "deploy", Description: "Deploy app"},
	}, nil)
	s.bot.On("SendMessage", s.ctx, sendMessageContains("**deploy**")).Return(nil)

	ch := &db.Channel{ChannelID: "ch1", DirPath: "/project", ParentID: "parent-ch"}
	s.orch.handleWorkflowsInteraction(s.ctx, &bot.Interaction{ChannelID: "ch1"}, ch)

	wfe.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

// --- handleWorkflowRunInteraction ---

func (s *OrchestratorCommandsSuite) TestHandleWorkflowRunInteractionNoEngine() {
	s.bot.On("SendMessage", s.ctx, sendMessageContains("Workflow engine not configured.")).Return(nil)

	s.orch.handleWorkflowRunInteraction(s.ctx, &bot.Interaction{ChannelID: "ch1"}, nil)

	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorCommandsSuite) TestHandleWorkflowRunInteractionNoName() {
	wfe := new(mockWorkflowEngine)
	s.orch.SetWorkflowEngine(wfe)
	s.bot.On("SendMessage", s.ctx, sendMessageContains("Usage:")).Return(nil)

	s.orch.handleWorkflowRunInteraction(s.ctx, &bot.Interaction{
		ChannelID: "ch1",
		Options:   map[string]string{"name": ""},
	}, nil)

	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorCommandsSuite) TestHandleWorkflowRunInteractionHappyPath() {
	wfe := new(mockWorkflowEngine)
	s.orch.SetWorkflowEngine(wfe)
	wfe.On("StartRun", s.ctx, workflow.StartRunOptions{
		WorkflowName: "fix-issue",
		ChannelID:    "ch1",
		DirPath:      "/project",
	}).Return("run-abc", nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return strings.Contains(out.Content, `"fix-issue"`) &&
			strings.Contains(out.Content, "run-abc")
	})).Return(nil)

	ch := &db.Channel{ChannelID: "ch1", DirPath: "/project"}
	s.orch.handleWorkflowRunInteraction(s.ctx, &bot.Interaction{
		ChannelID: "ch1",
		Options:   map[string]string{"name": "fix-issue"},
	}, ch)

	wfe.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorCommandsSuite) TestHandleWorkflowRunInteractionStartError() {
	wfe := new(mockWorkflowEngine)
	s.orch.SetWorkflowEngine(wfe)
	wfe.On("StartRun", s.ctx, mock.Anything).Return("", errors.New("workflow not found"))
	s.bot.On("SendMessage", s.ctx, sendMessageContains("Failed to start workflow:")).Return(nil)

	s.orch.handleWorkflowRunInteraction(s.ctx, &bot.Interaction{
		ChannelID: "ch1",
		Options:   map[string]string{"name": "missing"},
	}, nil)

	wfe.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

// --- handleWorkflowCancelInteraction ---

func (s *OrchestratorCommandsSuite) TestHandleWorkflowCancelInteractionNoEngine() {
	s.bot.On("SendMessage", s.ctx, sendMessageContains("Workflow engine not configured.")).Return(nil)

	s.orch.handleWorkflowCancelInteraction(s.ctx, &bot.Interaction{ChannelID: "ch1"})

	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorCommandsSuite) TestHandleWorkflowCancelInteractionNoRunID() {
	wfe := new(mockWorkflowEngine)
	s.orch.SetWorkflowEngine(wfe)
	s.bot.On("SendMessage", s.ctx, sendMessageContains("Usage:")).Return(nil)

	s.orch.handleWorkflowCancelInteraction(s.ctx, &bot.Interaction{
		ChannelID: "ch1",
		Options:   map[string]string{"run_id": ""},
	})

	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorCommandsSuite) TestHandleWorkflowCancelInteractionHappyPath() {
	wfe := new(mockWorkflowEngine)
	s.orch.SetWorkflowEngine(wfe)
	wfe.On("CancelRun", s.ctx, "run-123").Return(nil)
	s.bot.On("SendMessage", s.ctx, sendMessageContains("run-123 cancelled")).Return(nil)

	s.orch.handleWorkflowCancelInteraction(s.ctx, &bot.Interaction{
		ChannelID: "ch1",
		Options:   map[string]string{"run_id": "run-123"},
	})

	wfe.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorCommandsSuite) TestHandleWorkflowCancelInteractionError() {
	wfe := new(mockWorkflowEngine)
	s.orch.SetWorkflowEngine(wfe)
	wfe.On("CancelRun", s.ctx, "run-123").Return(errors.New("not found"))
	s.bot.On("SendMessage", s.ctx, sendMessageContains("Failed to cancel workflow run:")).Return(nil)

	s.orch.handleWorkflowCancelInteraction(s.ctx, &bot.Interaction{
		ChannelID: "ch1",
		Options:   map[string]string{"run_id": "run-123"},
	})

	wfe.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

// --- handleWorkflowDeleteInteraction ---

func (s *OrchestratorCommandsSuite) TestHandleWorkflowDeleteInteractionNoEngine() {
	s.bot.On("SendMessage", s.ctx, sendMessageContains("Workflow engine not configured.")).Return(nil)

	s.orch.handleWorkflowDeleteInteraction(s.ctx, &bot.Interaction{ChannelID: "ch1"})

	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorCommandsSuite) TestHandleWorkflowDeleteInteractionNoRunID() {
	wfe := new(mockWorkflowEngine)
	s.orch.SetWorkflowEngine(wfe)
	s.bot.On("SendMessage", s.ctx, sendMessageContains("Usage:")).Return(nil)

	s.orch.handleWorkflowDeleteInteraction(s.ctx, &bot.Interaction{
		ChannelID: "ch1",
		Options:   map[string]string{"run_id": ""},
	})

	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorCommandsSuite) TestHandleWorkflowDeleteInteractionHappyPath() {
	wfe := new(mockWorkflowEngine)
	s.orch.SetWorkflowEngine(wfe)
	wfe.On("DeleteRun", s.ctx, "run-456").Return(nil)
	s.bot.On("SendMessage", s.ctx, sendMessageContains("run-456 deleted")).Return(nil)

	s.orch.handleWorkflowDeleteInteraction(s.ctx, &bot.Interaction{
		ChannelID: "ch1",
		Options:   map[string]string{"run_id": "run-456"},
	})

	wfe.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorCommandsSuite) TestHandleWorkflowDeleteInteractionError() {
	wfe := new(mockWorkflowEngine)
	s.orch.SetWorkflowEngine(wfe)
	wfe.On("DeleteRun", s.ctx, "run-456").Return(errors.New("not found"))
	s.bot.On("SendMessage", s.ctx, sendMessageContains("Failed to delete workflow run:")).Return(nil)

	s.orch.handleWorkflowDeleteInteraction(s.ctx, &bot.Interaction{
		ChannelID: "ch1",
		Options:   map[string]string{"run_id": "run-456"},
	})

	wfe.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

// --- handleWorkflowRetryInteraction ---

func (s *OrchestratorCommandsSuite) TestHandleWorkflowRetryInteractionNoEngine() {
	s.bot.On("SendMessage", s.ctx, sendMessageContains("Workflow engine not configured.")).Return(nil)

	s.orch.handleWorkflowRetryInteraction(s.ctx, &bot.Interaction{ChannelID: "ch1"})

	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorCommandsSuite) TestHandleWorkflowRetryInteractionNoRunID() {
	wfe := new(mockWorkflowEngine)
	s.orch.SetWorkflowEngine(wfe)
	s.bot.On("SendMessage", s.ctx, sendMessageContains("Usage:")).Return(nil)

	s.orch.handleWorkflowRetryInteraction(s.ctx, &bot.Interaction{
		ChannelID: "ch1",
		Options:   map[string]string{"run_id": ""},
	})

	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorCommandsSuite) TestHandleWorkflowRetryInteractionHappyPath() {
	wfe := new(mockWorkflowEngine)
	s.orch.SetWorkflowEngine(wfe)
	wfe.On("RetryRun", s.ctx, "run-old").Return("run-new", nil)
	s.bot.On("SendMessage", s.ctx, sendMessageContains("run-new")).Return(nil)

	s.orch.handleWorkflowRetryInteraction(s.ctx, &bot.Interaction{
		ChannelID: "ch1",
		Options:   map[string]string{"run_id": "run-old"},
	})

	wfe.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorCommandsSuite) TestHandleWorkflowRetryInteractionError() {
	wfe := new(mockWorkflowEngine)
	s.orch.SetWorkflowEngine(wfe)
	wfe.On("RetryRun", s.ctx, "run-old").Return("", errors.New("run not found"))
	s.bot.On("SendMessage", s.ctx, sendMessageContains("Failed to retry workflow run:")).Return(nil)

	s.orch.handleWorkflowRetryInteraction(s.ctx, &bot.Interaction{
		ChannelID: "ch1",
		Options:   map[string]string{"run_id": "run-old"},
	})

	wfe.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

// --- handleWorkflowRunsInteraction ---

func (s *OrchestratorCommandsSuite) TestHandleWorkflowRunsInteractionNoEngine() {
	s.bot.On("SendMessage", s.ctx, sendMessageContains("Workflow engine not configured.")).Return(nil)

	s.orch.handleWorkflowRunsInteraction(s.ctx, &bot.Interaction{ChannelID: "ch1"})

	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorCommandsSuite) TestHandleWorkflowRunsInteractionListError() {
	wfe := new(mockWorkflowEngine)
	s.orch.SetWorkflowEngine(wfe)
	wfe.On("ListRuns", s.ctx, "", 10, 0).Return(nil, errors.New("db error"))
	s.bot.On("SendMessage", s.ctx, sendMessageContains("Failed to list workflow runs.")).Return(nil)

	s.orch.handleWorkflowRunsInteraction(s.ctx, &bot.Interaction{ChannelID: "ch1"})

	wfe.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorCommandsSuite) TestHandleWorkflowRunsInteractionEmpty() {
	wfe := new(mockWorkflowEngine)
	s.orch.SetWorkflowEngine(wfe)
	wfe.On("ListRuns", s.ctx, "", 10, 0).Return([]*db.WorkflowRun{}, nil)
	s.bot.On("SendMessage", s.ctx, sendMessageContains("No recent workflow runs.")).Return(nil)

	s.orch.handleWorkflowRunsInteraction(s.ctx, &bot.Interaction{ChannelID: "ch1"})

	wfe.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorCommandsSuite) TestHandleWorkflowRunsInteractionListsRuns() {
	wfe := new(mockWorkflowEngine)
	s.orch.SetWorkflowEngine(wfe)
	wfe.On("ListRuns", s.ctx, "", 10, 0).Return([]*db.WorkflowRun{
		{ID: "run-abc", WorkflowName: "fix-issue", Status: "completed", StartedAt: time.Now().Add(-5 * time.Minute)},
		{ID: "run-def", WorkflowName: "validate", Status: "running", StartedAt: time.Now().Add(-1 * time.Minute)},
	}, nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(out *bot.OutgoingMessage) bool {
		return strings.Contains(out.Content, "Recent workflow runs:") &&
			strings.Contains(out.Content, "**fix-issue**") &&
			strings.Contains(out.Content, "run-abc") &&
			strings.Contains(out.Content, "**validate**") &&
			strings.Contains(out.Content, "run-def")
	})).Return(nil)

	s.orch.handleWorkflowRunsInteraction(s.ctx, &bot.Interaction{ChannelID: "ch1"})

	wfe.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

// --- HandleInteraction dispatch for new commands ---

func (s *OrchestratorCommandsSuite) TestHandleInteractionShortcuts() {
	s.store.ExpectedCalls = nil
	s.store.On("InsertMessage", mock.Anything, mock.Anything).Return(nil).Maybe()
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)

	s.orch.cfg.Store(&config.Config{
		PromptShortcuts: []config.PromptShortcut{{Name: "deploy", Prompt: "Deploy the app"}},
	})
	s.bot.On("SendMessage", s.ctx, sendMessageContains("**deploy**")).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "shortcuts",
	})

	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorCommandsSuite) TestHandleInteractionShortcut() {
	s.store.ExpectedCalls = nil
	s.store.On("InsertMessage", mock.Anything, mock.Anything).Return(nil).Maybe()
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)

	s.orch.cfg.Store(&config.Config{
		PromptShortcuts: []config.PromptShortcut{{Name: "review", Prompt: "Review the code"}},
	})
	s.bot.On("HandleIncomingMessage", s.ctx, "ch1", "user1", "Review the code", "").Once()

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		AuthorID:    "user1",
		CommandName: "shortcut",
		Options:     map[string]string{"name": "review"},
	})

	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorCommandsSuite) TestHandleInteractionWorkflows() {
	s.store.ExpectedCalls = nil
	s.store.On("InsertMessage", mock.Anything, mock.Anything).Return(nil).Maybe()
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)

	wfe := new(mockWorkflowEngine)
	s.orch.SetWorkflowEngine(wfe)
	wfe.On("ListWorkflows", s.ctx, "", "").Return([]config.WorkflowDef{
		{Name: "my-flow", Description: "My workflow"},
	}, nil)
	s.bot.On("SendMessage", s.ctx, sendMessageContains("**my-flow**")).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "workflows",
	})

	wfe.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorCommandsSuite) TestHandleInteractionWorkflowRun() {
	s.store.ExpectedCalls = nil
	s.store.On("InsertMessage", mock.Anything, mock.Anything).Return(nil).Maybe()
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)

	wfe := new(mockWorkflowEngine)
	s.orch.SetWorkflowEngine(wfe)
	wfe.On("StartRun", s.ctx, workflow.StartRunOptions{
		WorkflowName: "deploy",
		ChannelID:    "ch1",
		DirPath:      "",
	}).Return("run-xyz", nil)
	s.bot.On("SendMessage", s.ctx, sendMessageContains("run-xyz")).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "workflow-run",
		Options:     map[string]string{"name": "deploy"},
	})

	wfe.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorCommandsSuite) TestHandleInteractionWorkflowCancel() {
	s.store.ExpectedCalls = nil
	s.store.On("InsertMessage", mock.Anything, mock.Anything).Return(nil).Maybe()
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)

	wfe := new(mockWorkflowEngine)
	s.orch.SetWorkflowEngine(wfe)
	wfe.On("CancelRun", s.ctx, "run-to-cancel").Return(nil)
	s.bot.On("SendMessage", s.ctx, sendMessageContains("run-to-cancel cancelled")).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "workflow-cancel",
		Options:     map[string]string{"run_id": "run-to-cancel"},
	})

	wfe.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorCommandsSuite) TestHandleInteractionWorkflowDelete() {
	s.store.ExpectedCalls = nil
	s.store.On("InsertMessage", mock.Anything, mock.Anything).Return(nil).Maybe()
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)

	wfe := new(mockWorkflowEngine)
	s.orch.SetWorkflowEngine(wfe)
	wfe.On("DeleteRun", s.ctx, "run-to-delete").Return(nil)
	s.bot.On("SendMessage", s.ctx, sendMessageContains("run-to-delete deleted")).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "workflow-delete",
		Options:     map[string]string{"run_id": "run-to-delete"},
	})

	wfe.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorCommandsSuite) TestHandleInteractionWorkflowRetry() {
	s.store.ExpectedCalls = nil
	s.store.On("InsertMessage", mock.Anything, mock.Anything).Return(nil).Maybe()
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)

	wfe := new(mockWorkflowEngine)
	s.orch.SetWorkflowEngine(wfe)
	wfe.On("RetryRun", s.ctx, "run-old").Return("run-new-2", nil)
	s.bot.On("SendMessage", s.ctx, sendMessageContains("run-new-2")).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "workflow-retry",
		Options:     map[string]string{"run_id": "run-old"},
	})

	wfe.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}

func (s *OrchestratorCommandsSuite) TestHandleInteractionWorkflowRuns() {
	s.store.ExpectedCalls = nil
	s.store.On("InsertMessage", mock.Anything, mock.Anything).Return(nil).Maybe()
	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, nil)

	wfe := new(mockWorkflowEngine)
	s.orch.SetWorkflowEngine(wfe)
	wfe.On("ListRuns", s.ctx, "", 10, 0).Return([]*db.WorkflowRun{
		{ID: "run-1", WorkflowName: "build", Status: "completed", StartedAt: time.Now().Add(-10 * time.Minute)},
	}, nil)
	s.bot.On("SendMessage", s.ctx, sendMessageContains("**build**")).Return(nil)

	s.orch.HandleInteraction(s.ctx, &bot.Interaction{
		ChannelID:   "ch1",
		CommandName: "workflow-runs",
	})

	wfe.AssertExpectations(s.T())
	s.bot.AssertExpectations(s.T())
}
