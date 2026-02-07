package scheduler

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/radutopala/loop/internal/db"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// MockStore implements the db.Store interface for testing.
type MockStore struct {
	mock.Mock
}

func (m *MockStore) UpsertChannel(ctx context.Context, ch *db.Channel) error {
	return m.Called(ctx, ch).Error(0)
}

func (m *MockStore) GetChannel(ctx context.Context, channelID string) (*db.Channel, error) {
	args := m.Called(ctx, channelID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*db.Channel), args.Error(1)
}

func (m *MockStore) SetChannelActive(ctx context.Context, channelID string, active bool) error {
	return m.Called(ctx, channelID, active).Error(0)
}

func (m *MockStore) IsChannelActive(ctx context.Context, channelID string) (bool, error) {
	args := m.Called(ctx, channelID)
	return args.Bool(0), args.Error(1)
}

func (m *MockStore) UpdateSessionID(ctx context.Context, channelID string, sessionID string) error {
	return m.Called(ctx, channelID, sessionID).Error(0)
}

func (m *MockStore) InsertMessage(ctx context.Context, msg *db.Message) error {
	return m.Called(ctx, msg).Error(0)
}

func (m *MockStore) GetUnprocessedMessages(ctx context.Context, channelID string) ([]*db.Message, error) {
	args := m.Called(ctx, channelID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*db.Message), args.Error(1)
}

func (m *MockStore) MarkMessagesProcessed(ctx context.Context, ids []int64) error {
	return m.Called(ctx, ids).Error(0)
}

func (m *MockStore) GetRecentMessages(ctx context.Context, channelID string, limit int) ([]*db.Message, error) {
	args := m.Called(ctx, channelID, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*db.Message), args.Error(1)
}

func (m *MockStore) CreateScheduledTask(ctx context.Context, task *db.ScheduledTask) (int64, error) {
	args := m.Called(ctx, task)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockStore) GetDueTasks(ctx context.Context, now time.Time) ([]*db.ScheduledTask, error) {
	args := m.Called(ctx, now)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*db.ScheduledTask), args.Error(1)
}

func (m *MockStore) UpdateScheduledTask(ctx context.Context, task *db.ScheduledTask) error {
	return m.Called(ctx, task).Error(0)
}

func (m *MockStore) DeleteScheduledTask(ctx context.Context, id int64) error {
	return m.Called(ctx, id).Error(0)
}

func (m *MockStore) ListScheduledTasks(ctx context.Context, channelID string) ([]*db.ScheduledTask, error) {
	args := m.Called(ctx, channelID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*db.ScheduledTask), args.Error(1)
}

func (m *MockStore) InsertTaskRunLog(ctx context.Context, trl *db.TaskRunLog) (int64, error) {
	args := m.Called(ctx, trl)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockStore) UpdateTaskRunLog(ctx context.Context, trl *db.TaskRunLog) error {
	return m.Called(ctx, trl).Error(0)
}

func (m *MockStore) GetRegisteredChannels(ctx context.Context) ([]*db.Channel, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*db.Channel), args.Error(1)
}

func (m *MockStore) Close() error {
	return m.Called().Error(0)
}

// MockTaskExecutor implements TaskExecutor for testing.
type MockTaskExecutor struct {
	mock.Mock
}

func (m *MockTaskExecutor) ExecuteTask(ctx context.Context, task *db.ScheduledTask) (string, error) {
	args := m.Called(ctx, task)
	return args.String(0), args.Error(1)
}

// SchedulerSuite is the test suite for the scheduler package.
type SchedulerSuite struct {
	suite.Suite
	store    *MockStore
	executor *MockTaskExecutor
	logger   *slog.Logger
}

func TestSchedulerSuite(t *testing.T) {
	suite.Run(t, new(SchedulerSuite))
}

func (s *SchedulerSuite) SetupTest() {
	s.store = new(MockStore)
	s.executor = new(MockTaskExecutor)
	s.logger = slog.Default()
}

func (s *SchedulerSuite) TestNewTaskScheduler() {
	ts := NewTaskScheduler(s.store, s.executor, time.Second, s.logger)
	require.NotNil(s.T(), ts)
	require.Equal(s.T(), s.store, ts.store)
	require.Equal(s.T(), s.executor, ts.executor)
	require.Equal(s.T(), time.Second, ts.pollInterval)
	require.Equal(s.T(), s.logger, ts.logger)
}

func (s *SchedulerSuite) TestAddTaskCron() {
	task := &db.ScheduledTask{
		ChannelID: "ch1",
		GuildID:   "g1",
		Schedule:  "*/5 * * * *",
		Type:      db.TaskTypeCron,
		Prompt:    "do stuff",
	}

	s.store.On("CreateScheduledTask", mock.Anything, mock.MatchedBy(func(t *db.ScheduledTask) bool {
		return t.Enabled && !t.NextRunAt.IsZero()
	})).Return(int64(1), nil)

	ts := NewTaskScheduler(s.store, s.executor, time.Second, s.logger)
	id, err := ts.AddTask(context.Background(), task)

	require.NoError(s.T(), err)
	require.Equal(s.T(), int64(1), id)
	require.True(s.T(), task.Enabled)
	require.False(s.T(), task.NextRunAt.IsZero())
	s.store.AssertExpectations(s.T())
}

func (s *SchedulerSuite) TestAddTaskInterval() {
	task := &db.ScheduledTask{
		ChannelID: "ch1",
		GuildID:   "g1",
		Schedule:  "30m",
		Type:      db.TaskTypeInterval,
		Prompt:    "interval task",
	}

	s.store.On("CreateScheduledTask", mock.Anything, mock.MatchedBy(func(t *db.ScheduledTask) bool {
		return t.Enabled && t.NextRunAt.After(time.Now().Add(29*time.Minute))
	})).Return(int64(2), nil)

	ts := NewTaskScheduler(s.store, s.executor, time.Second, s.logger)
	id, err := ts.AddTask(context.Background(), task)

	require.NoError(s.T(), err)
	require.Equal(s.T(), int64(2), id)
	s.store.AssertExpectations(s.T())
}

func (s *SchedulerSuite) TestAddTaskOnce() {
	task := &db.ScheduledTask{
		ChannelID: "ch1",
		GuildID:   "g1",
		Schedule:  "5m",
		Type:      db.TaskTypeOnce,
		Prompt:    "once task",
	}

	s.store.On("CreateScheduledTask", mock.Anything, mock.MatchedBy(func(t *db.ScheduledTask) bool {
		return t.Enabled && !t.NextRunAt.IsZero()
	})).Return(int64(3), nil)

	ts := NewTaskScheduler(s.store, s.executor, time.Second, s.logger)
	id, err := ts.AddTask(context.Background(), task)

	require.NoError(s.T(), err)
	require.Equal(s.T(), int64(3), id)
	s.store.AssertExpectations(s.T())
}

func (s *SchedulerSuite) TestAddTaskInvalidCron() {
	task := &db.ScheduledTask{
		ChannelID: "ch1",
		Schedule:  "invalid cron",
		Type:      db.TaskTypeCron,
	}

	ts := NewTaskScheduler(s.store, s.executor, time.Second, s.logger)
	_, err := ts.AddTask(context.Background(), task)

	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "calculating next run")
}

func (s *SchedulerSuite) TestAddTaskInvalidInterval() {
	task := &db.ScheduledTask{
		ChannelID: "ch1",
		Schedule:  "not-a-duration",
		Type:      db.TaskTypeInterval,
	}

	ts := NewTaskScheduler(s.store, s.executor, time.Second, s.logger)
	_, err := ts.AddTask(context.Background(), task)

	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "calculating next run")
}

func (s *SchedulerSuite) TestAddTaskStoreError() {
	task := &db.ScheduledTask{
		ChannelID: "ch1",
		Schedule:  "10m",
		Type:      db.TaskTypeInterval,
	}

	s.store.On("CreateScheduledTask", mock.Anything, mock.Anything).Return(int64(0), errors.New("db error"))

	ts := NewTaskScheduler(s.store, s.executor, time.Second, s.logger)
	_, err := ts.AddTask(context.Background(), task)

	require.Error(s.T(), err)
	require.Equal(s.T(), "db error", err.Error())
	s.store.AssertExpectations(s.T())
}

func (s *SchedulerSuite) TestRemoveTask() {
	s.store.On("DeleteScheduledTask", mock.Anything, int64(42)).Return(nil)

	ts := NewTaskScheduler(s.store, s.executor, time.Second, s.logger)
	err := ts.RemoveTask(context.Background(), 42)

	require.NoError(s.T(), err)
	s.store.AssertExpectations(s.T())
}

func (s *SchedulerSuite) TestRemoveTaskError() {
	s.store.On("DeleteScheduledTask", mock.Anything, int64(42)).Return(errors.New("delete error"))

	ts := NewTaskScheduler(s.store, s.executor, time.Second, s.logger)
	err := ts.RemoveTask(context.Background(), 42)

	require.Error(s.T(), err)
	require.Equal(s.T(), "delete error", err.Error())
	s.store.AssertExpectations(s.T())
}

func (s *SchedulerSuite) TestListTasks() {
	expected := []*db.ScheduledTask{
		{ID: 1, ChannelID: "ch1"},
		{ID: 2, ChannelID: "ch1"},
	}
	s.store.On("ListScheduledTasks", mock.Anything, "ch1").Return(expected, nil)

	ts := NewTaskScheduler(s.store, s.executor, time.Second, s.logger)
	tasks, err := ts.ListTasks(context.Background(), "ch1")

	require.NoError(s.T(), err)
	require.Equal(s.T(), expected, tasks)
	s.store.AssertExpectations(s.T())
}

func (s *SchedulerSuite) TestListTasksError() {
	s.store.On("ListScheduledTasks", mock.Anything, "ch1").Return(nil, errors.New("list error"))

	ts := NewTaskScheduler(s.store, s.executor, time.Second, s.logger)
	tasks, err := ts.ListTasks(context.Background(), "ch1")

	require.Error(s.T(), err)
	require.Nil(s.T(), tasks)
	s.store.AssertExpectations(s.T())
}

func (s *SchedulerSuite) TestStartAndStop() {
	s.store.On("GetDueTasks", mock.Anything, mock.Anything).Return([]*db.ScheduledTask{}, nil).Maybe()

	ts := NewTaskScheduler(s.store, s.executor, 10*time.Millisecond, s.logger)
	err := ts.Start(context.Background())
	require.NoError(s.T(), err)

	time.Sleep(50 * time.Millisecond)

	err = ts.Stop()
	require.NoError(s.T(), err)
}

func (s *SchedulerSuite) TestStopWithoutStart() {
	ts := NewTaskScheduler(s.store, s.executor, time.Second, s.logger)
	err := ts.Stop()
	require.NoError(s.T(), err)
}

func (s *SchedulerSuite) TestPollLoopExecutesTask() {
	cronTask := &db.ScheduledTask{
		ID:        1,
		ChannelID: "ch1",
		Schedule:  "*/5 * * * *",
		Type:      db.TaskTypeCron,
		Prompt:    "test prompt",
		Enabled:   true,
	}

	executed := make(chan struct{}, 1)

	s.store.On("GetDueTasks", mock.Anything, mock.Anything).Return([]*db.ScheduledTask{cronTask}, nil).Once()
	s.store.On("GetDueTasks", mock.Anything, mock.Anything).Return([]*db.ScheduledTask{}, nil).Maybe()
	s.store.On("InsertTaskRunLog", mock.Anything, mock.MatchedBy(func(trl *db.TaskRunLog) bool {
		return trl.TaskID == 1 && trl.Status == db.RunStatusRunning
	})).Return(int64(10), nil)
	s.executor.On("ExecuteTask", mock.Anything, cronTask).Return("done", nil).Run(func(args mock.Arguments) {
		select {
		case executed <- struct{}{}:
		default:
		}
	})
	s.store.On("UpdateTaskRunLog", mock.Anything, mock.MatchedBy(func(trl *db.TaskRunLog) bool {
		return trl.Status == db.RunStatusSuccess && trl.ResponseText == "done"
	})).Return(nil)
	s.store.On("UpdateScheduledTask", mock.Anything, mock.MatchedBy(func(t *db.ScheduledTask) bool {
		return t.ID == 1 && !t.NextRunAt.IsZero()
	})).Return(nil)

	ts := NewTaskScheduler(s.store, s.executor, 10*time.Millisecond, s.logger)
	err := ts.Start(context.Background())
	require.NoError(s.T(), err)

	select {
	case <-executed:
	case <-time.After(2 * time.Second):
		s.T().Fatal("timed out waiting for task execution")
	}

	err = ts.Stop()
	require.NoError(s.T(), err)

	s.store.AssertExpectations(s.T())
	s.executor.AssertExpectations(s.T())
}

func (s *SchedulerSuite) TestPollLoopExecutorError() {
	task := &db.ScheduledTask{
		ID:        2,
		ChannelID: "ch1",
		Schedule:  "1h",
		Type:      db.TaskTypeInterval,
		Prompt:    "fail task",
		Enabled:   true,
	}

	executed := make(chan struct{}, 1)

	s.store.On("GetDueTasks", mock.Anything, mock.Anything).Return([]*db.ScheduledTask{task}, nil).Once()
	s.store.On("GetDueTasks", mock.Anything, mock.Anything).Return([]*db.ScheduledTask{}, nil).Maybe()
	s.store.On("InsertTaskRunLog", mock.Anything, mock.Anything).Return(int64(20), nil)
	s.executor.On("ExecuteTask", mock.Anything, task).Return("", errors.New("exec failed")).Run(func(args mock.Arguments) {
		select {
		case executed <- struct{}{}:
		default:
		}
	})
	s.store.On("UpdateTaskRunLog", mock.Anything, mock.MatchedBy(func(trl *db.TaskRunLog) bool {
		return trl.Status == db.RunStatusFailed && trl.ErrorText == "exec failed"
	})).Return(nil)
	s.store.On("UpdateScheduledTask", mock.Anything, mock.MatchedBy(func(t *db.ScheduledTask) bool {
		return t.ID == 2
	})).Return(nil)

	ts := NewTaskScheduler(s.store, s.executor, 10*time.Millisecond, s.logger)
	err := ts.Start(context.Background())
	require.NoError(s.T(), err)

	select {
	case <-executed:
	case <-time.After(2 * time.Second):
		s.T().Fatal("timed out waiting for task execution")
	}

	err = ts.Stop()
	require.NoError(s.T(), err)

	s.store.AssertExpectations(s.T())
}

func (s *SchedulerSuite) TestPollLoopOnceTaskDisabled() {
	task := &db.ScheduledTask{
		ID:        3,
		ChannelID: "ch1",
		Schedule:  "5m",
		Type:      db.TaskTypeOnce,
		Prompt:    "once task",
		Enabled:   true,
	}

	executed := make(chan struct{}, 1)

	s.store.On("GetDueTasks", mock.Anything, mock.Anything).Return([]*db.ScheduledTask{task}, nil).Once()
	s.store.On("GetDueTasks", mock.Anything, mock.Anything).Return([]*db.ScheduledTask{}, nil).Maybe()
	s.store.On("InsertTaskRunLog", mock.Anything, mock.Anything).Return(int64(30), nil)
	s.executor.On("ExecuteTask", mock.Anything, task).Return("once done", nil).Run(func(args mock.Arguments) {
		select {
		case executed <- struct{}{}:
		default:
		}
	})
	s.store.On("UpdateTaskRunLog", mock.Anything, mock.MatchedBy(func(trl *db.TaskRunLog) bool {
		return trl.Status == db.RunStatusSuccess
	})).Return(nil)
	s.store.On("UpdateScheduledTask", mock.Anything, mock.MatchedBy(func(t *db.ScheduledTask) bool {
		return t.ID == 3 && !t.Enabled
	})).Return(nil)

	ts := NewTaskScheduler(s.store, s.executor, 10*time.Millisecond, s.logger)
	err := ts.Start(context.Background())
	require.NoError(s.T(), err)

	select {
	case <-executed:
	case <-time.After(2 * time.Second):
		s.T().Fatal("timed out waiting for task execution")
	}

	err = ts.Stop()
	require.NoError(s.T(), err)

	s.store.AssertExpectations(s.T())
}

func (s *SchedulerSuite) TestPollLoopGetDueTasksError() {
	s.store.On("GetDueTasks", mock.Anything, mock.Anything).Return(nil, errors.New("db down")).Maybe()

	ts := NewTaskScheduler(s.store, s.executor, 10*time.Millisecond, s.logger)
	err := ts.Start(context.Background())
	require.NoError(s.T(), err)

	time.Sleep(50 * time.Millisecond)

	err = ts.Stop()
	require.NoError(s.T(), err)
}

func (s *SchedulerSuite) TestPollLoopInsertRunLogError() {
	task := &db.ScheduledTask{
		ID:        4,
		ChannelID: "ch1",
		Schedule:  "10m",
		Type:      db.TaskTypeInterval,
		Enabled:   true,
	}

	inserted := make(chan struct{}, 1)

	s.store.On("GetDueTasks", mock.Anything, mock.Anything).Return([]*db.ScheduledTask{task}, nil).Once()
	s.store.On("GetDueTasks", mock.Anything, mock.Anything).Return([]*db.ScheduledTask{}, nil).Maybe()
	s.store.On("InsertTaskRunLog", mock.Anything, mock.Anything).Return(int64(0), errors.New("insert log fail")).Run(func(args mock.Arguments) {
		select {
		case inserted <- struct{}{}:
		default:
		}
	})

	ts := NewTaskScheduler(s.store, s.executor, 10*time.Millisecond, s.logger)
	err := ts.Start(context.Background())
	require.NoError(s.T(), err)

	select {
	case <-inserted:
	case <-time.After(2 * time.Second):
		s.T().Fatal("timed out")
	}

	err = ts.Stop()
	require.NoError(s.T(), err)

	s.executor.AssertNotCalled(s.T(), "ExecuteTask", mock.Anything, mock.Anything)
}

func (s *SchedulerSuite) TestPollLoopUpdateRunLogError() {
	task := &db.ScheduledTask{
		ID:        5,
		ChannelID: "ch1",
		Schedule:  "10m",
		Type:      db.TaskTypeInterval,
		Enabled:   true,
	}

	updated := make(chan struct{}, 1)

	s.store.On("GetDueTasks", mock.Anything, mock.Anything).Return([]*db.ScheduledTask{task}, nil).Once()
	s.store.On("GetDueTasks", mock.Anything, mock.Anything).Return([]*db.ScheduledTask{}, nil).Maybe()
	s.store.On("InsertTaskRunLog", mock.Anything, mock.Anything).Return(int64(50), nil)
	s.executor.On("ExecuteTask", mock.Anything, task).Return("ok", nil)
	s.store.On("UpdateTaskRunLog", mock.Anything, mock.Anything).Return(errors.New("update log fail")).Run(func(args mock.Arguments) {
		select {
		case updated <- struct{}{}:
		default:
		}
	})
	s.store.On("UpdateScheduledTask", mock.Anything, mock.Anything).Return(nil)

	ts := NewTaskScheduler(s.store, s.executor, 10*time.Millisecond, s.logger)
	err := ts.Start(context.Background())
	require.NoError(s.T(), err)

	select {
	case <-updated:
	case <-time.After(2 * time.Second):
		s.T().Fatal("timed out")
	}

	err = ts.Stop()
	require.NoError(s.T(), err)
}

func (s *SchedulerSuite) TestPollLoopUpdateScheduledTaskError() {
	task := &db.ScheduledTask{
		ID:        6,
		ChannelID: "ch1",
		Schedule:  "10m",
		Type:      db.TaskTypeInterval,
		Enabled:   true,
	}

	taskUpdated := make(chan struct{}, 1)

	s.store.On("GetDueTasks", mock.Anything, mock.Anything).Return([]*db.ScheduledTask{task}, nil).Once()
	s.store.On("GetDueTasks", mock.Anything, mock.Anything).Return([]*db.ScheduledTask{}, nil).Maybe()
	s.store.On("InsertTaskRunLog", mock.Anything, mock.Anything).Return(int64(60), nil)
	s.executor.On("ExecuteTask", mock.Anything, task).Return("ok", nil)
	s.store.On("UpdateTaskRunLog", mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpdateScheduledTask", mock.Anything, mock.Anything).Return(errors.New("update task fail")).Run(func(args mock.Arguments) {
		select {
		case taskUpdated <- struct{}{}:
		default:
		}
	})

	ts := NewTaskScheduler(s.store, s.executor, 10*time.Millisecond, s.logger)
	err := ts.Start(context.Background())
	require.NoError(s.T(), err)

	select {
	case <-taskUpdated:
	case <-time.After(2 * time.Second):
		s.T().Fatal("timed out")
	}

	err = ts.Stop()
	require.NoError(s.T(), err)
}

func (s *SchedulerSuite) TestPollLoopCronParseError() {
	task := &db.ScheduledTask{
		ID:        7,
		ChannelID: "ch1",
		Schedule:  "bad cron",
		Type:      db.TaskTypeCron,
		Enabled:   true,
	}

	executed := make(chan struct{}, 1)

	s.store.On("GetDueTasks", mock.Anything, mock.Anything).Return([]*db.ScheduledTask{task}, nil).Once()
	s.store.On("GetDueTasks", mock.Anything, mock.Anything).Return([]*db.ScheduledTask{}, nil).Maybe()
	s.store.On("InsertTaskRunLog", mock.Anything, mock.Anything).Return(int64(70), nil)
	s.executor.On("ExecuteTask", mock.Anything, task).Return("ok", nil).Run(func(args mock.Arguments) {
		select {
		case executed <- struct{}{}:
		default:
		}
	})
	s.store.On("UpdateTaskRunLog", mock.Anything, mock.Anything).Return(nil)

	ts := NewTaskScheduler(s.store, s.executor, 10*time.Millisecond, s.logger)
	err := ts.Start(context.Background())
	require.NoError(s.T(), err)

	select {
	case <-executed:
	case <-time.After(2 * time.Second):
		s.T().Fatal("timed out")
	}

	// Give time for the cron parse error path to complete
	time.Sleep(20 * time.Millisecond)

	err = ts.Stop()
	require.NoError(s.T(), err)

	// UpdateScheduledTask should NOT be called because cron parse fails
	s.store.AssertNotCalled(s.T(), "UpdateScheduledTask", mock.Anything, mock.Anything)
}

func (s *SchedulerSuite) TestPollLoopIntervalParseError() {
	task := &db.ScheduledTask{
		ID:        8,
		ChannelID: "ch1",
		Schedule:  "not-a-duration",
		Type:      db.TaskTypeInterval,
		Enabled:   true,
	}

	executed := make(chan struct{}, 1)

	s.store.On("GetDueTasks", mock.Anything, mock.Anything).Return([]*db.ScheduledTask{task}, nil).Once()
	s.store.On("GetDueTasks", mock.Anything, mock.Anything).Return([]*db.ScheduledTask{}, nil).Maybe()
	s.store.On("InsertTaskRunLog", mock.Anything, mock.Anything).Return(int64(80), nil)
	s.executor.On("ExecuteTask", mock.Anything, task).Return("ok", nil).Run(func(args mock.Arguments) {
		select {
		case executed <- struct{}{}:
		default:
		}
	})
	s.store.On("UpdateTaskRunLog", mock.Anything, mock.Anything).Return(nil)

	ts := NewTaskScheduler(s.store, s.executor, 10*time.Millisecond, s.logger)
	err := ts.Start(context.Background())
	require.NoError(s.T(), err)

	select {
	case <-executed:
	case <-time.After(2 * time.Second):
		s.T().Fatal("timed out")
	}

	time.Sleep(20 * time.Millisecond)

	err = ts.Stop()
	require.NoError(s.T(), err)

	s.store.AssertNotCalled(s.T(), "UpdateScheduledTask", mock.Anything, mock.Anything)
}

func (s *SchedulerSuite) TestCalculateNextRunCron() {
	now := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	next, err := calculateNextRun(db.TaskTypeCron, "*/5 * * * *", now)
	require.NoError(s.T(), err)
	require.Equal(s.T(), time.Date(2025, 1, 1, 12, 5, 0, 0, time.UTC), next)
}

func (s *SchedulerSuite) TestCalculateNextRunInterval() {
	now := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	next, err := calculateNextRun(db.TaskTypeInterval, "30m", now)
	require.NoError(s.T(), err)
	require.Equal(s.T(), time.Date(2025, 1, 1, 12, 30, 0, 0, time.UTC), next)
}

func (s *SchedulerSuite) TestCalculateNextRunOnce() {
	now := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	next, err := calculateNextRun(db.TaskTypeOnce, "10m", now)
	require.NoError(s.T(), err)
	require.Equal(s.T(), time.Date(2025, 1, 1, 12, 10, 0, 0, time.UTC), next)
}

func (s *SchedulerSuite) TestCalculateNextRunInvalidOnce() {
	_, err := calculateNextRun(db.TaskTypeOnce, "bad", time.Now())
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "parsing once schedule")
}

func (s *SchedulerSuite) TestCalculateNextRunInvalidCron() {
	_, err := calculateNextRun(db.TaskTypeCron, "bad", time.Now())
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "parsing cron schedule")
}

func (s *SchedulerSuite) TestCalculateNextRunInvalidInterval() {
	_, err := calculateNextRun(db.TaskTypeInterval, "not-valid", time.Now())
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "parsing interval")
}

func (s *SchedulerSuite) TestCalculateNextRunUnknownType() {
	_, err := calculateNextRun(db.TaskType("unknown"), "", time.Now())
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "unknown task type")
}

func (s *SchedulerSuite) TestAddTaskUnknownType() {
	task := &db.ScheduledTask{
		ChannelID: "ch1",
		Schedule:  "",
		Type:      db.TaskType("unknown"),
	}

	ts := NewTaskScheduler(s.store, s.executor, time.Second, s.logger)
	_, err := ts.AddTask(context.Background(), task)

	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "calculating next run")
}

func (s *SchedulerSuite) TestSchedulerImplementsInterface() {
	var _ Scheduler = (*TaskScheduler)(nil)
}
