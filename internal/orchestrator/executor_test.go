package orchestrator

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
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
	s.executor = NewTaskExecutor(s.runner, s.bot, s.store, logger, 5*time.Minute, nil)
}

// allowStatusBroadcasts adds BroadcastAgentStatus expectations for task execution.
func allowStatusBroadcasts(eb *MockEventBroadcaster) {
	eb.On("BroadcastAgentStatus", mock.Anything, mock.Anything).Maybe()
}

// allowTaskPromptBroadcast permits the scheduled-task prompt user-message
// broadcast the executor emits on local platforms (storeUserTaskPrompt).
func allowTaskPromptBroadcast(eb *MockEventBroadcaster) {
	eb.On("BroadcastMessageCreated", mock.Anything, mock.MatchedBy(func(d events.MessageEventData) bool {
		return d.AuthorID == "scheduled-task"
	})).Maybe()
}

// allowBotInserts adds an InsertMessage expectation for executor-emitted rows:
// bot messages from storeBotMessage, and the scheduled-task prompt user message
// from storeUserTaskPrompt (inserted on the local-platform thread so the chat
// shows the prompt that kicked off the run).
func (s *TaskExecutorSuite) allowBotInserts() {
	s.store.On("InsertMessage", mock.Anything, mock.MatchedBy(func(msg *db.Message) bool {
		return msg.IsBot || msg.AuthorID == "scheduled-task"
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

	task := &db.ScheduledTask{
		ID: 61, ChannelID: "ch-stop", Prompt: "long task",
		Type: db.TaskTypeInterval, Schedule: "5m",
		ThreadID: "stop-thread",
	}
	s.allowBotInserts() // existing-thread run injects the prompt as a user message
	localCh := &db.Channel{ChannelID: "ch-stop", Platform: types.PlatformLocal}
	s.store.On("GetChannel", mock.Anything, "ch-stop").Return(localCh, nil)
	s.store.On("GetChannel", mock.Anything, "stop-thread").Return(&db.Channel{
		ChannelID: "stop-thread", ParentID: "ch-stop", Platform: types.PlatformLocal, SessionID: "s-thread",
	}, nil)
	s.store.On("GetScheduledTask", mock.Anything, mock.Anything).Return(nil, nil).Maybe()

	eb := new(MockEventBroadcaster)
	s.executor.SetEventBroadcaster(eb)
	allowStatusBroadcasts(eb)
	allowTaskPromptBroadcast(eb)

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
	allowTaskPromptBroadcast(eb)

	task := &db.ScheduledTask{
		ID: 52, ChannelID: "ch-parent", Prompt: "fail", Type: db.TaskTypeInterval, Schedule: "5m",
		ThreadID: "existing-thread",
	}
	s.allowBotInserts() // existing-thread run injects the prompt as a user message
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
	allowTaskPromptBroadcast(eb)

	task := &db.ScheduledTask{
		ID: 51, ChannelID: "ch-err2", Prompt: "fail", Type: db.TaskTypeInterval, Schedule: "5m",
		ThreadID: "err-thread",
	}
	s.allowBotInserts() // existing-thread run injects the prompt as a user message
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
