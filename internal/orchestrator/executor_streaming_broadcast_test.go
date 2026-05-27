package orchestrator

import (
	"errors"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/bot"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/events"
	"github.com/radutopala/loop/internal/types"
)

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

func (s *TaskExecutorSuite) TestStreamingOnToolUseTaskCreate() {
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

	taskInput := `{"subject":"Fix bug","activeForm":"Fixing bug","description":""}`
	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		if req.OnToolUse == nil || req.OnToolResult == nil {
			return false
		}
		req.OnTurn("Turn 1")
		req.OnToolUse("toolu_t", "TaskCreate", taskInput)
		req.OnToolResult("toolu_t", "Task #1 created successfully: Fix bug", false)
		return true
	})).Return(&agent.AgentResponse{Response: "Turn 1", SessionID: "sess-task"}, nil)

	s.store.On("UpdateSessionID", s.ctx, "thread-29", "sess-task").Return(nil)

	eb.On("BroadcastChannelCreated", "ch29", "thread-29").Once()
	eb.On("BroadcastMessageCreated", "thread-29", mock.Anything).Maybe()
	eb.On("BroadcastToolUse", "thread-29", mock.Anything).Once()
	eb.On("BroadcastToolResult", "thread-29", mock.Anything).Once()
	eb.On("BroadcastAgentTasks", "thread-29", mock.MatchedBy(func(d events.AgentTasksEventData) bool {
		return len(d.Tasks) == 1 && d.Tasks[0].Subject == "Fix bug" && d.Tasks[0].ID == "1"
	})).Once()

	_, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	eb.AssertCalled(s.T(), "BroadcastAgentTasks", "thread-29", mock.Anything)
	eb.AssertExpectations(s.T())
}

func (s *TaskExecutorSuite) TestStreamingOnToolUseTaskUpdate() {
	eb := new(MockEventBroadcaster)
	s.executor.SetEventBroadcaster(eb)
	allowStatusBroadcasts(eb)

	task := &db.ScheduledTask{
		ID: 30, ChannelID: "ch30", Prompt: "update tasks",
		Type: db.TaskTypeCron, Schedule: "0 * * * *",
	}

	// Seed the registry under the thread id the test will assign so applyUpdate finds the task.
	_, _ = s.executor.tasks.applyCreate("thread-30",
		`{"subject":"Fix bug","activeForm":"Fixing bug"}`,
		"Task #1 created successfully: Fix bug",
	)

	s.store.On("GetChannel", mock.Anything, "ch30").Return(nil, nil)
	s.store.On("GetChannel", mock.Anything, "thread-30").Return(nil, nil).Maybe()
	s.store.On("GetScheduledTask", s.ctx, int64(30)).Return(&db.ScheduledTask{ID: 30, Type: db.TaskTypeCron}, nil)
	s.bot.On("CreateSimpleThread", s.ctx, "ch30", mock.Anything, mock.Anything).Return("thread-30", nil).Once()
	s.store.On("UpdateScheduledTaskThreadID", s.ctx, int64(30), "thread-30").Return(nil)

	updateInput := `{"taskId":"1","status":"in_progress"}`
	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		if req.OnToolUse == nil {
			return false
		}
		req.OnTurn("Turn 1")
		req.OnToolUse("toolu_u", "TaskUpdate", updateInput)
		return true
	})).Return(&agent.AgentResponse{Response: "Turn 1", SessionID: "sess-upd"}, nil)

	s.store.On("UpdateSessionID", s.ctx, "thread-30", "sess-upd").Return(nil)

	eb.On("BroadcastChannelCreated", "ch30", "thread-30").Once()
	eb.On("BroadcastMessageCreated", "thread-30", mock.Anything).Maybe()
	eb.On("BroadcastToolUse", "thread-30", mock.Anything).Once()
	eb.On("BroadcastAgentTasks", "thread-30", mock.MatchedBy(func(d events.AgentTasksEventData) bool {
		return len(d.Tasks) == 1 && d.Tasks[0].Status == "in_progress"
	})).Once()

	_, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	eb.AssertCalled(s.T(), "BroadcastAgentTasks", "thread-30", mock.Anything)
	eb.AssertExpectations(s.T())
}

func (s *TaskExecutorSuite) TestStreamingOnToolUseBroadcastsBeforeThread() {
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
