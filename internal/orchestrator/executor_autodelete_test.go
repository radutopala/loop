package orchestrator

import (
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

	storeBotMessage(s.ctx, s.store, eb, "ch1", "task done", "")

	s.store.AssertExpectations(s.T())
	eb.AssertExpectations(s.T())
}

// TriggerMsgID links a bot reply back to the user message whose run produced
// it. Verify it lands on both the persisted row and the SSE payload so the FE
// can group reload-time timeline items by their trigger.
func (s *TaskExecutorSuite) TestStoreBotMessageStampsTriggerMsgID() {
	eb := new(MockEventBroadcaster)

	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{ID: 5, ChannelID: "ch1"}, nil)
	s.store.On("InsertMessage", s.ctx, mock.MatchedBy(func(m *db.Message) bool {
		return m.TriggerMsgID == "user-abc" && m.IsBot
	})).Return(nil)
	eb.On("BroadcastMessageCreated", "ch1", mock.MatchedBy(func(d events.MessageEventData) bool {
		return d.TriggerMsgID == "user-abc" && d.IsBot
	}))

	storeBotMessage(s.ctx, s.store, eb, "ch1", "reply", "user-abc")

	s.store.AssertExpectations(s.T())
	eb.AssertExpectations(s.T())
}

func (s *TaskExecutorSuite) TestStoreBotMessageGetChannelError() {
	eb := new(MockEventBroadcaster)

	s.store.On("GetChannel", s.ctx, "ch1").Return(nil, errors.New("db error"))
	eb.On("BroadcastMessageCreated", "ch1", mock.Anything)

	storeBotMessage(s.ctx, s.store, eb, "ch1", "task done", "")

	s.store.AssertNotCalled(s.T(), "InsertMessage", mock.Anything, mock.Anything)
	eb.AssertExpectations(s.T())
}
