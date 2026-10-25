package orchestrator

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/bot"
	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/testutil"
	"github.com/radutopala/loop/internal/types"
)

// threadLockFree reports whether nobody holds channelID's drain lock.
func threadLockFree(locks *sync.Map, channelID string) bool {
	lock := channelLock(locks, channelID)
	if !lock.TryLock() {
		return false
	}
	lock.Unlock()
	return true
}

// A task resuming its thread waits for a chat run already draining that
// thread, then holds the thread's lock until it's done, so messages sent to
// the thread meanwhile queue behind it.
func (s *TaskExecutorSuite) TestTaskRunWaitsForAndHoldsThreadLock() {
	locks := &sync.Map{}
	s.executor.SetChannelLocks(locks)

	task := &db.ScheduledTask{
		ID: 80, ChannelID: "ch-lock", Prompt: "check", Type: db.TaskTypeInterval, Schedule: "5m",
		ThreadID: "lock-thread",
	}
	s.allowBotInserts()
	s.store.On("GetChannel", mock.Anything, "ch-lock").Return(&db.Channel{ChannelID: "ch-lock", Platform: types.PlatformLocal}, nil)
	// The chat run moved the session on while the task waited; the task
	// must resume that one, so the session is read after the lock is taken.
	threadCh := &db.Channel{ChannelID: "lock-thread", ParentID: "ch-lock", Platform: types.PlatformLocal, SessionID: "s-before-chat"}
	s.store.On("GetChannel", mock.Anything, "lock-thread").Return(threadCh, nil)
	s.store.On("GetScheduledTask", mock.Anything, int64(80)).Return(nil, nil)
	s.store.On("UpdateSessionID", mock.Anything, "lock-thread", "s-task").Return(nil)
	s.bot.On("SendMessage", mock.Anything, mock.MatchedBy(func(m *bot.OutgoingMessage) bool {
		return m.ChannelID == "lock-thread"
	})).Return(nil)

	started := make(chan struct{})
	heldDuringRun := false
	s.runner.On("Run", mock.Anything, mock.MatchedBy(func(req *agent.AgentRequest) bool {
		return req.ChannelID == "lock-thread" && req.SessionID == "s-after-chat"
	})).Run(func(_ mock.Arguments) {
		close(started)
		heldDuringRun = !threadLockFree(locks, "lock-thread")
	}).Return(&agent.AgentResponse{Response: "done", SessionID: "s-task"}, nil)

	// A chat run is draining the thread.
	chat := channelLock(locks, "lock-thread")
	chat.Lock()

	done := make(chan error, 1)
	go func() {
		_, err := s.executor.ExecuteTask(s.ctx, task)
		done <- err
	}()

	select {
	case <-started:
		s.T().Fatal("task ran while a chat run held the thread")
	case <-time.After(50 * time.Millisecond):
	}
	threadCh.SessionID = "s-after-chat"
	chat.Unlock()

	require.NoError(s.T(), <-done)
	require.True(s.T(), heldDuringRun, "the task holds the thread's lock while it runs")
	require.True(s.T(), threadLockFree(locks, "lock-thread"), "released after the run")
}

// The thread a first run creates before the runner starts is held for the
// whole run, so a message sent to it meanwhile waits for the run.
func (s *TaskExecutorSuite) TestTaskRunHoldsLockOfThreadItCreates() {
	locks := &sync.Map{}
	s.executor.SetChannelLocks(locks)

	task := &db.ScheduledTask{ID: 81, ChannelID: "ch-new", Prompt: "go", Type: db.TaskTypeCron, Schedule: "0 * * * *"}
	s.allowBotInserts()
	s.store.On("GetChannel", mock.Anything, "ch-new").Return(nil, nil)
	s.store.On("GetScheduledTask", mock.Anything, int64(81)).Return(&db.ScheduledTask{ID: 81, Type: db.TaskTypeCron}, nil)
	s.expectTaskThread(task, "new-thread", false, nil)
	s.store.On("UpdateSessionID", mock.Anything, "new-thread", "s-new").Return(nil)
	s.bot.On("SendMessage", mock.Anything, mock.MatchedBy(func(m *bot.OutgoingMessage) bool {
		return m.ChannelID == "new-thread"
	})).Return(nil)

	heldBeforeTurn, heldAfterTurn := false, false
	s.runner.On("Run", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		req := args.Get(1).(*agent.AgentRequest)
		heldBeforeTurn = !threadLockFree(locks, "new-thread")
		req.OnTurn("first turn", agent.TurnRef{})
		heldAfterTurn = !threadLockFree(locks, "new-thread")
	}).Return(&agent.AgentResponse{Response: "final", SessionID: "s-new"}, nil)

	_, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.True(s.T(), heldBeforeTurn, "the new thread is held before the first turn")
	require.True(s.T(), heldAfterTurn, "the new thread stays held after the first turn")
	require.True(s.T(), threadLockFree(locks, "new-thread"), "released after the run")
}

// A chat drain keeps the thread's lock until its queue is empty, so a task run
// due meanwhile waits through every queued message, not just the current one.
func (s *OrchestratorSuite) TestTaskWaitsForWholeChatQueue() {
	store := new(testutil.MockStore)
	first := &db.Message{ID: 1, ChannelID: "busy-ch", MsgID: "m-1", IsTriggered: true, AuthorID: "u", AuthorName: "u", Content: "one"}
	second := &db.Message{ID: 2, ChannelID: "busy-ch", MsgID: "m-2", IsTriggered: true, AuthorID: "u", AuthorName: "u", Content: "two"}
	store.On("ClaimNextPending", mock.Anything, "busy-ch").Return(first, nil).Once()
	store.On("ClaimNextPending", mock.Anything, "busy-ch").Return(second, nil).Once()
	store.On("ClaimNextPending", mock.Anything, "busy-ch").Return(nil, nil).Once()
	store.On("ReleaseRunningMessage", mock.Anything, mock.Anything, true).Return(nil).Twice()

	orch := New(store, s.bot, s.runner, s.scheduler, slog.New(slog.NewTextHandler(io.Discard, nil)), config.Config{}, nil)
	orch.SetSynchronousDrain()
	task := &threadLocks{locks: &orch.channelLocks, held: map[string]*sync.Mutex{}}
	taskHolds, taskReleased := make(chan struct{}), make(chan struct{})

	// Each message's run ends early (no recent messages): enough to hold the
	// drain on it. The first blocks until the task is waiting.
	firstRunning, finishFirst := make(chan struct{}), make(chan struct{})
	heldDuringSecond := true
	store.On("GetRecentMessages", mock.Anything, "busy-ch", mock.Anything).Return(nil, errors.New("stop")).Run(func(mock.Arguments) {
		close(firstRunning)
		<-finishFirst
	}).Once()
	store.On("GetRecentMessages", mock.Anything, "busy-ch", mock.Anything).Return(nil, errors.New("stop")).Run(func(mock.Arguments) {
		select {
		case <-taskHolds:
		default:
			heldDuringSecond = false
		}
	}).Once()

	drained := make(chan struct{})
	go func() {
		orch.ResumeChannel(context.Background(), "busy-ch")
		close(drained)
	}()
	<-firstRunning
	// The task lets go once it has the thread, so a drain that wrongly
	// let it in mid-queue fails below instead of hanging.
	go func() {
		task.hold("busy-ch")
		close(taskHolds)
		task.releaseAll()
		close(taskReleased)
	}()
	select {
	case <-taskHolds:
		s.T().Fatal("the task took the thread while a chat message ran")
	case <-time.After(30 * time.Millisecond):
	}
	close(finishFirst)

	<-drained
	select {
	case <-taskHolds:
	case <-time.After(time.Second):
		s.T().Fatal("the task never got the thread after the queue emptied")
	}
	require.False(s.T(), heldDuringSecond, "the task waited for the second queued message too")
	<-taskReleased
	require.True(s.T(), threadLockFree(&orch.channelLocks, "busy-ch"))
	store.AssertExpectations(s.T())
}

func (s *TaskExecutorSuite) TestThreadLocksHold() {
	tests := []struct {
		name  string
		locks *sync.Map
		ids   []string
		held  []string
	}{
		{name: "not wired", locks: nil, ids: []string{"a"}},
		{name: "empty id", locks: &sync.Map{}, ids: []string{""}},
		{name: "same id twice is taken once", locks: &sync.Map{}, ids: []string{"a", "a"}, held: []string{"a"}},
		{name: "distinct ids", locks: &sync.Map{}, ids: []string{"a", "b"}, held: []string{"a", "b"}},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			t := &threadLocks{locks: tt.locks, held: map[string]*sync.Mutex{}}
			for _, id := range tt.ids {
				t.hold(id)
			}
			for _, id := range tt.held {
				require.False(s.T(), threadLockFree(tt.locks, id), id)
			}
			require.Len(s.T(), t.held, len(tt.held))

			t.releaseAll()
			require.Empty(s.T(), t.held)
			for _, id := range tt.held {
				require.True(s.T(), threadLockFree(tt.locks, id), id)
			}
		})
	}
}
