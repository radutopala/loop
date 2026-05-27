package orchestrator

import (
	"errors"
	"strings"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/bot"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/types"
)

func (s *TaskExecutorSuite) TestStreamingCreatesThread() {
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

func (s *TaskExecutorSuite) TestStreamingFinalSentWhenDifferent() {
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
