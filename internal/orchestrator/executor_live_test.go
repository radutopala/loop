package orchestrator

import (
	"sync"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/bot"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/types"
)

type mockLiveEnder struct {
	mock.Mock
}

func (m *mockLiveEnder) EndLiveSession(channelID, reason string) error {
	return m.Called(channelID, reason).Error(0)
}

// A task run ends its thread's live session once it holds the thread's
// lock, so the session doesn't write to the thread's session while the
// task's one-shot run does; the run's session is written back for the next
// chat turn to resume.
func (s *TaskExecutorSuite) TestTaskRunEndsThreadLiveSession() {
	tests := []struct {
		name     string
		threadID string
		// threadMissing makes the task's thread unreadable, so the run
		// creates a new one.
		threadMissing bool
		// freshThread is the thread a concurrent run persisted since the
		// task was loaded.
		freshThread string
		ended       []string
	}{
		{name: "existing thread", threadID: "live-thread", ended: []string{"live-thread"}},
		{name: "first run creates the thread", ended: []string{"new-thread"}},
		{name: "thread persisted by a concurrent run", freshThread: "fresh-thread", ended: []string{"fresh-thread"}},
		{name: "deleted thread is replaced", threadID: "old-thread", threadMissing: true, ended: []string{"old-thread", "new-thread"}},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			locks := &sync.Map{}
			s.executor.SetChannelLocks(locks)
			live := new(mockLiveEnder)
			s.executor.SetLiveSessions(live)
			require.Same(s.T(), live, s.executor.live)

			task := &db.ScheduledTask{
				ID: 90, ChannelID: "ch-live", Prompt: "check", Type: db.TaskTypeInterval, Schedule: "5m",
				ThreadID: tt.threadID,
			}
			runIn := tt.threadID
			s.allowBotInserts()
			s.store.On("GetChannel", mock.Anything, "ch-live").Return(&db.Channel{ChannelID: "ch-live", Platform: types.PlatformLocal}, nil)
			switch {
			case tt.freshThread != "":
				runIn = tt.freshThread
				s.store.On("GetScheduledTask", mock.Anything, int64(90)).Return(&db.ScheduledTask{ID: 90, ThreadID: tt.freshThread}, nil)
				s.store.On("GetChannel", mock.Anything, tt.freshThread).Return(&db.Channel{
					ChannelID: tt.freshThread, ParentID: "ch-live", Platform: types.PlatformLocal, SessionID: "s-chat",
				}, nil)
			default:
				s.store.On("GetScheduledTask", mock.Anything, int64(90)).Return(nil, nil)
			}
			switch {
			case tt.freshThread != "":
			case tt.threadID == "":
				runIn = "new-thread"
				s.expectTaskThread(task, runIn, true, nil)
			case tt.threadMissing:
				runIn = "new-thread"
				s.store.On("GetChannel", mock.Anything, tt.threadID).Return(nil, nil)
				s.expectTaskThread(task, runIn, true, nil)
			default:
				s.store.On("GetChannel", mock.Anything, tt.threadID).Return(&db.Channel{
					ChannelID: tt.threadID, ParentID: "ch-live", Platform: types.PlatformLocal, SessionID: "s-chat",
				}, nil)
			}
			for _, id := range tt.ended {
				live.On("EndLiveSession", id, "scheduled run").Run(func(mock.Arguments) {
					require.False(s.T(), threadLockFree(locks, id), "ended after taking %s's lock", id)
				}).Return(nil).Once()
			}
			s.store.On("UpdateSessionID", mock.Anything, runIn, "s-task").Return(nil).Once()
			s.bot.On("SendMessage", mock.Anything, mock.MatchedBy(func(m *bot.OutgoingMessage) bool {
				return m.ChannelID == runIn
			})).Return(nil)
			s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
				return req.ChannelID == runIn
			})).Return(&agent.AgentResponse{Response: "done", SessionID: "s-task"}, nil).Once()

			_, err := s.executor.ExecuteTask(s.ctx, task)
			require.NoError(s.T(), err)
			live.AssertExpectations(s.T())
			live.AssertNumberOfCalls(s.T(), "EndLiveSession", len(tt.ended))
			s.store.AssertCalled(s.T(), "UpdateSessionID", mock.Anything, runIn, "s-task")
		})
	}
}

// Without a live runner, or without a thread, nothing is ended.
func (s *TaskExecutorSuite) TestEndLiveSkips() {
	s.executor.endLive("thread-1")

	live := new(mockLiveEnder)
	s.executor.SetLiveSessions(live)
	s.executor.endLive("")
	live.AssertNotCalled(s.T(), "EndLiveSession", mock.Anything, mock.Anything)
}
