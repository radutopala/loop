package orchestrator

import (
	"errors"
	"strings"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/bot"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/events"
	"github.com/radutopala/loop/internal/types"
)

// TestStreamingFirstRunCreatesThreadBeforeRun: a first run creates its thread
// before the runner is invoked (with no initial message), so every streamed
// turn — the first included, without a prefix — is a plain send to it.
func (s *TaskExecutorSuite) TestStreamingFirstRunCreatesThreadBeforeRun() {
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
	s.bot.On("CreateSimpleThread", s.ctx, "ch9", "⏱ task #9 (`0 * * * *`) stream task", "").Return("thread-1", nil).Once()
	s.store.On("UpdateScheduledTaskThreadID", s.ctx, int64(9), "thread-1").Return(nil).Once()

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		return req.OnTurn != nil
	})).Run(func(args mock.Arguments) {
		s.bot.AssertNumberOfCalls(s.T(), "CreateSimpleThread", 1)
		s.store.AssertNumberOfCalls(s.T(), "UpdateScheduledTaskThreadID", 1)
		req := args.Get(1).(*agent.AgentRequest)
		req.OnTurn("Intermediate", agent.TurnRef{})
		req.OnTurn("", agent.TurnRef{}) // empty text should be skipped
		req.OnTurn("Final answer", agent.TurnRef{})
	}).Return(&agent.AgentResponse{
		Response:  "Final answer", // Same as last OnTurn — final send skipped
		SessionID: "sess-stream",
	}, nil)

	s.store.On("UpdateSessionID", s.ctx, "thread-1", "sess-stream").Return(nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(msg *bot.OutgoingMessage) bool {
		return msg.ChannelID == "thread-1" && msg.Content == "Intermediate"
	})).Return(nil).Once()
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(msg *bot.OutgoingMessage) bool {
		return msg.ChannelID == "thread-1" && msg.Content == "Final answer"
	})).Return(nil).Once()

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "Final answer", resp)

	// 2 SendMessage calls (both turns to the thread). Final skipped (duplicate).
	s.bot.AssertNumberOfCalls(s.T(), "SendMessage", 2)
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
	s.allowBotInserts() // the thread gets the prompt + agent message
	s.store.On("GetChannel", mock.Anything, "ch-local").Return(localChannel, nil)
	// Post-run JSONL ingest looks up the session-target channel for chat_id.
	s.store.On("GetChannel", mock.Anything, "local-thread-1").Return(&db.Channel{ID: 9001, ChannelID: "local-thread-1"}, nil).Maybe()
	s.store.On("GetScheduledTask", mock.Anything, mock.Anything).Return(nil, nil).Maybe()
	s.bot.On("CreateSimpleThread", s.ctx, "ch-local", mock.Anything, "").Return("local-thread-1", nil).Once()
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(msg *bot.OutgoingMessage) bool {
		return msg.ChannelID == "local-thread-1" && msg.Content == "Result"
	})).Return(nil).Once()
	s.store.On("LinkTaskThread", s.ctx, mock.MatchedBy(func(ch *db.Channel) bool {
		return ch.ChannelID == "local-thread-1" && ch.ParentID == "ch-local"
	}), int64(30), "local-thread-1").Return(nil).Once()

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		if req.OnTurn == nil {
			return false
		}
		req.OnTurn("Result", agent.TurnRef{})
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
		req.OnTurn("Update", agent.TurnRef{})
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
	s.bot.On("CreateSimpleThread", s.ctx, "ch-dangling", mock.Anything, "").Return("new-thread", nil).Once()
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(msg *bot.OutgoingMessage) bool {
		return msg.ChannelID == "new-thread" && msg.Content == "Update"
	})).Return(nil).Once()
	s.store.On("LinkTaskThread", s.ctx, mock.MatchedBy(func(ch *db.Channel) bool {
		return ch.ChannelID == "new-thread" && ch.ParentID == "ch-dangling"
	}), int64(33), "new-thread").Return(nil).Once()

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		if req.OnTurn == nil {
			return false
		}
		req.OnTurn("Update", agent.TurnRef{})
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
		req.OnTurn("Discord result", agent.TurnRef{})
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

	s.bot.On("CreateSimpleThread", s.ctx, "ch11", "⏱ task #11 (`5m`) stream diff", "").Return("thread-2", nil).Once()
	s.store.On("UpdateScheduledTaskThreadID", s.ctx, int64(11), "thread-2").Return(nil)

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		if req.OnTurn == nil {
			return false
		}
		req.OnTurn("Intermediate", agent.TurnRef{})
		return true
	})).Return(&agent.AgentResponse{
		Response:  "Different final",
		SessionID: "sess-diff",
	}, nil)

	s.store.On("UpdateSessionID", s.ctx, "thread-2", "sess-diff").Return(nil)

	// The streamed turn and the final response (different from it) both go to the thread
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(msg *bot.OutgoingMessage) bool {
		return msg.ChannelID == "thread-2" && msg.Content == "Intermediate"
	})).Return(nil).Once()
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(msg *bot.OutgoingMessage) bool {
		return msg.ChannelID == "thread-2" && msg.Content == "Different final"
	})).Return(nil).Once()

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "Different final", resp)

	// 2 SendMessage (turn + final to thread) + 1 CreateSimpleThread
	s.bot.AssertNumberOfCalls(s.T(), "SendMessage", 2)
	s.bot.AssertNumberOfCalls(s.T(), "CreateSimpleThread", 1)
	s.runner.AssertExpectations(s.T())
}

// TestStreamingThreadCreationFailsFallsBack: when the pre-run thread creation
// fails, the run proceeds in the channel — turns, tool/thinking/result/activity
// events (stamped with the parent's chat id), session and statuses all target
// the channel.
func (s *TaskExecutorSuite) TestStreamingThreadCreationFailsFallsBack() {
	eb := new(MockEventBroadcaster)
	s.executor.SetEventBroadcaster(eb)

	task := &db.ScheduledTask{
		ID:        12,
		ChannelID: "ch12",
		Prompt:    "fallback task",
		Type:      db.TaskTypeCron,
		Schedule:  "0 * * * *",
	}

	s.store.On("GetChannel", mock.Anything, "ch12").Return(&db.Channel{ID: 120, ChannelID: "ch12", Platform: types.PlatformLocal}, nil)
	s.store.On("GetScheduledTask", s.ctx, int64(12)).Return(&db.ScheduledTask{ID: 12, Type: db.TaskTypeCron}, nil)
	s.allowBotInserts()

	// Thread creation fails
	s.bot.On("CreateSimpleThread", s.ctx, "ch12", "task #12 (`0 * * * *`) fallback task", "").Return("", errors.New("thread error")).Once()
	for _, kind := range []db.MessageKind{db.MessageKindToolUse, db.MessageKindThinking, db.MessageKindToolResult, db.MessageKindCompacting} {
		s.store.On("InsertAgentEvent", mock.Anything, mock.MatchedBy(func(m *db.Message) bool {
			return m.ChannelID == "ch12" && m.ChatID == 120 && m.Kind == kind
		})).Return(nil).Once()
	}

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		return req.ChannelID == "ch12" && req.OnTurn != nil
	})).Run(func(args mock.Arguments) {
		req := args.Get(1).(*agent.AgentRequest)
		req.OnToolUse("toolu_f", "Read", "/x")
		req.OnThinking("plan")
		req.OnToolResult("toolu_f", "contents", false)
		req.OnActivity("compacting", "")
		req.OnTurn("Turn 1", agent.TurnRef{})
		req.OnTurn("Turn 2", agent.TurnRef{})
	}).Return(&agent.AgentResponse{
		Response:  "Turn 2", // same as last OnTurn
		SessionID: "sess-fb",
	}, nil)

	s.store.On("UpdateSessionID", s.ctx, "ch12", "sess-fb").Return(nil)

	// Both turns go to the channel
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(msg *bot.OutgoingMessage) bool {
		return msg.ChannelID == "ch12" && msg.Content == "Turn 1"
	})).Return(nil).Once()
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(msg *bot.OutgoingMessage) bool {
		return msg.ChannelID == "ch12" && msg.Content == "Turn 2"
	})).Return(nil).Once()

	// No thread: statuses go to the channel only, with no thread id; no prompt
	// message is stored.
	eb.On("BroadcastAgentStatus", "ch12", mock.MatchedBy(func(d events.AgentStatusEventData) bool {
		return d.ThreadID == "" && (d.Status == "running" || d.Status == "completed")
	})).Twice()
	eb.On("BroadcastToolUse", "ch12", mock.Anything).Once()
	eb.On("BroadcastAgentThinking", "ch12", events.AgentThinkingEventData{Text: "plan"}).Once()
	eb.On("BroadcastToolResult", "ch12", events.ToolResultEventData{ToolUseID: "toolu_f", Output: "contents"}).Once()
	eb.On("BroadcastAgentActivity", "ch12", events.AgentActivityEventData{Activity: "compacting"}).Once()
	eb.On("BroadcastMessageCreated", "ch12", mock.MatchedBy(func(d events.MessageEventData) bool {
		return d.IsBot
	})).Twice()

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "Turn 2", resp)

	// 2 SendMessage calls (both to the channel), final skipped (duplicate)
	s.bot.AssertNumberOfCalls(s.T(), "SendMessage", 2)
	s.bot.AssertExpectations(s.T())
	s.store.AssertExpectations(s.T())
	eb.AssertExpectations(s.T())
	eb.AssertNotCalled(s.T(), "BroadcastChannelCreated", mock.Anything, mock.Anything)
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
	s.expectTaskThread(task, "thread-14", false, nil)

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		if req.OnTurn == nil {
			return false
		}
		req.OnTurn("Turn 1", agent.TurnRef{})
		req.OnTurn("Turn 2", agent.TurnRef{}) // send fails
		return true
	})).Return(&agent.AgentResponse{
		Response:  "Turn 2",
		SessionID: "sess-senderr",
	}, nil)

	s.store.On("UpdateSessionID", s.ctx, "thread-14", "sess-senderr").Return(nil)

	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(msg *bot.OutgoingMessage) bool {
		return msg.ChannelID == "thread-14" && msg.Content == "Turn 1"
	})).Return(nil).Once()
	// Second SendMessage fails — error is logged, not fatal
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(msg *bot.OutgoingMessage) bool {
		return msg.ChannelID == "thread-14" && msg.Content == "Turn 2"
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

	s.expectTaskThread(task, "thread-3", false, nil)
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(msg *bot.OutgoingMessage) bool {
		return msg.ChannelID == "thread-3" && msg.Content == "Only turn"
	})).Return(nil).Once()

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		if req.OnTurn == nil {
			return false
		}
		req.OnTurn("Only turn", agent.TurnRef{})
		return true
	})).Return(&agent.AgentResponse{
		Response:  "Only turn", // Same as OnTurn — final skipped
		SessionID: "sess-single",
	}, nil)

	s.store.On("UpdateSessionID", s.ctx, "thread-3", "sess-single").Return(nil)

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "Only turn", resp)

	// 1 SendMessage (the streamed turn); the final is skipped as a duplicate
	s.bot.AssertNumberOfCalls(s.T(), "SendMessage", 1)
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
			task := &db.ScheduledTask{
				ID: 20, ChannelID: chID, Prompt: "check prompt",
				Type: db.TaskTypeCron, Schedule: "0 * * * *", AutoDeleteSec: tc.delSec,
			}
			s.store.On("GetChannel", s.ctx, chID).Return(nil, nil)
			s.store.On("GetScheduledTask", s.ctx, int64(20)).Return(&db.ScheduledTask{ID: 20, Type: db.TaskTypeCron}, nil)
			s.expectTaskThread(task, "thread-prompt", false, nil)
			s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
				return strings.Contains(req.SystemPrompt, "[EPHEMERAL]") == tc.wantMarker
			})).Return(&agent.AgentResponse{Response: "ok", SessionID: "sess"}, nil)
			s.store.On("UpdateSessionID", s.ctx, "thread-prompt", "sess").Return(nil)
			s.bot.On("SendMessage", s.ctx, mock.Anything).Return(nil).Once()
			// Auto-delete schedules the new thread's removal; keep it inert.
			s.executor.timeAfterFunc = func(time.Duration, func()) *time.Timer { return time.NewTimer(0) }

			_, err := s.executor.ExecuteTask(s.ctx, task)
			require.NoError(s.T(), err)
			s.runner.AssertExpectations(s.T())
		})
	}
}

// TestStreamingLocalFirstRunRunsInThread locks the feature: a local task's
// first run lives in the thread it creates before the runner is invoked, just
// like later runs — the run is registered under the thread, the prompt is
// stored there as a user message (IsBot=false, AuthorID="scheduled-task")
// ahead of the agent's reply, and statuses go to both thread and parent. The
// reply carries no task prefix.
func (s *TaskExecutorSuite) TestStreamingLocalFirstRunRunsInThread() {
	eb := new(MockEventBroadcaster)
	s.executor.SetEventBroadcaster(eb)

	task := &db.ScheduledTask{
		ID: 130, ChannelID: "ch-fr", Prompt: "summarise the notes",
		Type: db.TaskTypeCron, Schedule: "0 9 * * *",
	}
	localChannel := &db.Channel{ID: 1, ChannelID: "ch-fr", Platform: types.PlatformLocal, DirPath: "/work", SessionID: "s-parent"}
	s.store.On("GetChannel", mock.Anything, "ch-fr").Return(localChannel, nil)
	s.store.On("GetScheduledTask", mock.Anything, mock.Anything).Return(nil, nil).Maybe()
	s.expectTaskThread(task, "thread-fr", true, &db.Channel{ID: 77, ChannelID: "thread-fr"})
	s.store.On("UpdateSessionID", mock.Anything, "thread-fr", "s").Return(nil).Once()
	s.bot.On("SendMessage", mock.Anything, mock.MatchedBy(func(msg *bot.OutgoingMessage) bool {
		return msg.ChannelID == "thread-fr" && msg.Content == "Here is the summary."
	})).Return(nil).Once()

	var inserted []*db.Message
	s.store.On("InsertMessage", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		inserted = append(inserted, args.Get(1).(*db.Message))
	}).Return(nil)

	eb.On("BroadcastChannelCreated", "ch-fr", "thread-fr").Once()
	eb.On("BroadcastMessageCreated", "thread-fr", mock.Anything).Twice()
	for _, status := range []string{"running", "completed"} {
		for _, target := range []string{"thread-fr", "ch-fr"} {
			eb.On("BroadcastAgentStatus", target, mock.MatchedBy(func(d events.AgentStatusEventData) bool {
				return d.Status == status && d.ThreadID == "thread-fr"
			})).Once()
		}
	}

	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		// Forks the parent's session, but runs in the thread.
		return req.ChannelID == "thread-fr" && req.SessionID == "s-parent" && req.ForkSession
	})).Run(func(args mock.Arguments) {
		// The thread exists and holds the prompt before the agent starts.
		s.bot.AssertNumberOfCalls(s.T(), "CreateSimpleThread", 1)
		require.Len(s.T(), inserted, 1)
		args.Get(1).(*agent.AgentRequest).OnTurn("Here is the summary.", agent.TurnRef{})
	}).Return(&agent.AgentResponse{Response: "Here is the summary.", SessionID: "s"}, nil)

	_, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)

	// First thread insert is the prompt as a user message; the agent reply follows.
	require.Len(s.T(), inserted, 2)
	require.False(s.T(), inserted[0].IsBot, "prompt must be a user message")
	require.Equal(s.T(), "summarise the notes", inserted[0].Content)
	require.Equal(s.T(), "scheduled-task", inserted[0].AuthorID)
	require.Equal(s.T(), "thread-fr", inserted[0].ChannelID)
	require.Equal(s.T(), int64(77), inserted[0].ChatID)
	require.True(s.T(), inserted[0].IsProcessed, "prompt must be inert (out of the drain queue)")
	require.True(s.T(), inserted[1].IsBot, "agent reply must be a bot message")
	require.Equal(s.T(), "Here is the summary.", inserted[1].Content)
	require.Equal(s.T(), "thread-fr", inserted[1].ChannelID)
	s.bot.AssertExpectations(s.T())
	s.store.AssertExpectations(s.T())
	eb.AssertExpectations(s.T())
}

// TestStreamingManualTaskThreadNameUsesManualLabel guards the thread-name
// label for manual tasks: they have no schedule, so the prefix must read
// "task #N (`manual`)" rather than empty backticks "task #N (“)".
func (s *TaskExecutorSuite) TestStreamingManualTaskThreadNameUsesManualLabel() {
	task := &db.ScheduledTask{
		ID: 140, ChannelID: "ch-m", Prompt: "say hi",
		Type: db.TaskTypeManual, Schedule: "",
	}
	localChannel := &db.Channel{ID: 1, ChannelID: "ch-m", Platform: types.PlatformLocal, DirPath: "/work"}
	s.allowBotInserts()
	s.store.On("GetChannel", mock.Anything, "ch-m").Return(localChannel, nil)
	s.store.On("GetChannel", mock.Anything, "thread-m").Return(&db.Channel{ID: 50, ChannelID: "thread-m"}, nil).Maybe()
	s.store.On("GetScheduledTask", mock.Anything, mock.Anything).Return(nil, nil).Maybe()
	s.bot.On("CreateSimpleThread", s.ctx, "ch-m", mock.MatchedBy(func(name string) bool {
		return strings.Contains(name, "task #140 (`manual`)")
	}), "").Return("thread-m", nil).Once()
	s.bot.On("SendMessage", s.ctx, mock.MatchedBy(func(msg *bot.OutgoingMessage) bool {
		return msg.ChannelID == "thread-m" && msg.Content == "hi"
	})).Return(nil).Once()
	s.store.On("LinkTaskThread", mock.Anything, mock.Anything, int64(140), "thread-m").Return(nil).Maybe()
	s.store.On("UpdateSessionID", mock.Anything, mock.Anything, mock.Anything).Return(nil).Maybe()
	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		if req.OnTurn == nil {
			return false
		}
		req.OnTurn("hi", agent.TurnRef{})
		return true
	})).Return(&agent.AgentResponse{Response: "hi", SessionID: "s"}, nil)

	_, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	s.bot.AssertExpectations(s.T())
}
