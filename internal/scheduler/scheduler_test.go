package scheduler

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/testutil"
)

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
	store    *testutil.MockStore
	executor *MockTaskExecutor
	logger   *slog.Logger
}

func TestSchedulerSuite(t *testing.T) {
	suite.Run(t, new(SchedulerSuite))
}

func (s *SchedulerSuite) SetupTest() {
	s.store = new(testutil.MockStore)
	s.executor = new(MockTaskExecutor)
	s.logger = slog.Default()
}

// setupDueTasks configures the mock store to return tasks once, then empty.
func setupDueTasks(store *testutil.MockStore, tasks []*db.ScheduledTask) {
	store.On("GetDueTasks", mock.Anything, mock.Anything).Return(tasks, nil).Once()
	store.On("GetDueTasks", mock.Anything, mock.Anything).Return([]*db.ScheduledTask{}, nil).Maybe()
}

// signalDone returns a channel and a mock.Run callback that signals it.
func signalDone() (chan struct{}, func(mock.Arguments)) {
	ch := make(chan struct{}, 1)
	return ch, func(mock.Arguments) {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (s *SchedulerSuite) TestNewTaskScheduler() {
	ts := NewTaskScheduler(s.store, s.executor, time.Second, s.logger)
	require.NotNil(s.T(), ts)
	require.Equal(s.T(), s.store, ts.store)
	require.Equal(s.T(), s.executor, ts.executor)
	require.Equal(s.T(), time.Second, ts.pollInterval)
	require.Equal(s.T(), s.logger, ts.logger)
}

func (s *SchedulerSuite) TestAddTask() {
	cases := []struct {
		name    string
		task    *db.ScheduledTask
		storeID int64
		wantErr string
	}{
		{
			name:    "cron",
			task:    &db.ScheduledTask{ChannelID: "ch1", GuildID: "g1", Schedule: "*/5 * * * *", Type: db.TaskTypeCron, Prompt: "do stuff"},
			storeID: 1,
		},
		{
			name:    "interval",
			task:    &db.ScheduledTask{ChannelID: "ch1", GuildID: "g1", Schedule: "30m", Type: db.TaskTypeInterval, Prompt: "interval task"},
			storeID: 2,
		},
		{
			name:    "once",
			task:    &db.ScheduledTask{ChannelID: "ch1", GuildID: "g1", Schedule: "2026-02-09T14:30:00Z", Type: db.TaskTypeOnce, Prompt: "once task"},
			storeID: 3,
		},
		{
			name:    "invalid cron",
			task:    &db.ScheduledTask{ChannelID: "ch1", Schedule: "invalid cron", Type: db.TaskTypeCron},
			wantErr: "calculating next run",
		},
		{
			name:    "invalid interval",
			task:    &db.ScheduledTask{ChannelID: "ch1", Schedule: "not-a-duration", Type: db.TaskTypeInterval},
			wantErr: "calculating next run",
		},
		{
			name:    "unknown type",
			task:    &db.ScheduledTask{ChannelID: "ch1", Schedule: "", Type: db.TaskType("unknown")},
			wantErr: "calculating next run",
		},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			store := new(testutil.MockStore)
			if tc.wantErr == "" {
				store.On("CreateScheduledTask", mock.Anything, mock.MatchedBy(func(t *db.ScheduledTask) bool {
					return t.Enabled && !t.NextRunAt.IsZero()
				})).Return(tc.storeID, nil)
			}

			ts := NewTaskScheduler(store, s.executor, time.Second, s.logger)
			id, err := ts.AddTask(context.Background(), tc.task)

			if tc.wantErr != "" {
				require.Error(s.T(), err)
				require.Contains(s.T(), err.Error(), tc.wantErr)
			} else {
				require.NoError(s.T(), err)
				require.Equal(s.T(), tc.storeID, id)
				store.AssertExpectations(s.T())
			}
		})
	}
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
	cases := []struct {
		name     string
		storeErr error
	}{
		{"success", nil},
		{"error", errors.New("delete error")},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			store := new(testutil.MockStore)
			store.On("DeleteScheduledTask", mock.Anything, int64(42)).Return(tc.storeErr)

			ts := NewTaskScheduler(store, s.executor, time.Second, s.logger)
			err := ts.RemoveTask(context.Background(), 42)

			if tc.storeErr != nil {
				require.Error(s.T(), err)
				require.Equal(s.T(), tc.storeErr.Error(), err.Error())
			} else {
				require.NoError(s.T(), err)
			}
			store.AssertExpectations(s.T())
		})
	}
}

func (s *SchedulerSuite) TestListTasks() {
	expected := []*db.ScheduledTask{
		{ID: 1, ChannelID: "ch1"},
		{ID: 2, ChannelID: "ch1"},
	}
	s.Run("success", func() {
		store := new(testutil.MockStore)
		store.On("ListScheduledTasks", mock.Anything, "ch1").Return(expected, nil)

		ts := NewTaskScheduler(store, s.executor, time.Second, s.logger)
		tasks, err := ts.ListTasks(context.Background(), "ch1")
		require.NoError(s.T(), err)
		require.Equal(s.T(), expected, tasks)
		store.AssertExpectations(s.T())
	})

	s.Run("error", func() {
		store := new(testutil.MockStore)
		store.On("ListScheduledTasks", mock.Anything, "ch1").Return(nil, errors.New("list error"))

		ts := NewTaskScheduler(store, s.executor, time.Second, s.logger)
		tasks, err := ts.ListTasks(context.Background(), "ch1")
		require.Error(s.T(), err)
		require.Nil(s.T(), tasks)
		store.AssertExpectations(s.T())
	})
}

func (s *SchedulerSuite) TestStartAndStop() {
	setupDueTasks(s.store, []*db.ScheduledTask{})

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
		ID: 1, ChannelID: "ch1", Schedule: "*/5 * * * *",
		Type: db.TaskTypeCron, Prompt: "test prompt", Enabled: true,
	}
	done, signal := signalDone()

	setupDueTasks(s.store, []*db.ScheduledTask{cronTask})
	s.store.On("InsertTaskRunLog", mock.Anything, mock.MatchedBy(func(trl *db.TaskRunLog) bool {
		return trl.TaskID == 1 && trl.Status == db.RunStatusRunning
	})).Return(int64(10), nil)
	s.executor.On("ExecuteTask", mock.Anything, cronTask).Return("done", nil).Run(signal)
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
	case <-done:
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
		ID: 2, ChannelID: "ch1", Schedule: "1h",
		Type: db.TaskTypeInterval, Prompt: "fail task", Enabled: true,
	}
	done, signal := signalDone()

	setupDueTasks(s.store, []*db.ScheduledTask{task})
	s.store.On("InsertTaskRunLog", mock.Anything, mock.Anything).Return(int64(20), nil)
	s.executor.On("ExecuteTask", mock.Anything, task).Return("", errors.New("exec failed")).Run(signal)
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
	case <-done:
	case <-time.After(2 * time.Second):
		s.T().Fatal("timed out waiting for task execution")
	}

	err = ts.Stop()
	require.NoError(s.T(), err)

	s.store.AssertExpectations(s.T())
}

func (s *SchedulerSuite) TestPollLoopOnceTaskDisabled() {
	task := &db.ScheduledTask{
		ID: 3, ChannelID: "ch1", Schedule: "2026-02-09T14:30:00Z",
		Type: db.TaskTypeOnce, Prompt: "once task", Enabled: true,
	}
	done, signal := signalDone()

	setupDueTasks(s.store, []*db.ScheduledTask{task})
	s.store.On("InsertTaskRunLog", mock.Anything, mock.Anything).Return(int64(30), nil)
	s.executor.On("ExecuteTask", mock.Anything, task).Return("once done", nil).Run(signal)
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
	case <-done:
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
		ID: 4, ChannelID: "ch1", Schedule: "10m",
		Type: db.TaskTypeInterval, Enabled: true,
	}
	done, signal := signalDone()

	setupDueTasks(s.store, []*db.ScheduledTask{task})
	s.store.On("InsertTaskRunLog", mock.Anything, mock.Anything).Return(int64(0), errors.New("insert log fail")).Run(signal)

	ts := NewTaskScheduler(s.store, s.executor, 10*time.Millisecond, s.logger)
	err := ts.Start(context.Background())
	require.NoError(s.T(), err)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		s.T().Fatal("timed out")
	}

	err = ts.Stop()
	require.NoError(s.T(), err)

	s.executor.AssertNotCalled(s.T(), "ExecuteTask", mock.Anything, mock.Anything)
}

func (s *SchedulerSuite) TestPollLoopUpdateRunLogError() {
	task := &db.ScheduledTask{
		ID: 5, ChannelID: "ch1", Schedule: "10m",
		Type: db.TaskTypeInterval, Enabled: true,
	}
	done, signal := signalDone()

	setupDueTasks(s.store, []*db.ScheduledTask{task})
	s.store.On("InsertTaskRunLog", mock.Anything, mock.Anything).Return(int64(50), nil)
	s.executor.On("ExecuteTask", mock.Anything, task).Return("ok", nil)
	s.store.On("UpdateTaskRunLog", mock.Anything, mock.Anything).Return(errors.New("update log fail")).Run(signal)
	s.store.On("UpdateScheduledTask", mock.Anything, mock.Anything).Return(nil)

	ts := NewTaskScheduler(s.store, s.executor, 10*time.Millisecond, s.logger)
	err := ts.Start(context.Background())
	require.NoError(s.T(), err)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		s.T().Fatal("timed out")
	}

	err = ts.Stop()
	require.NoError(s.T(), err)
}

func (s *SchedulerSuite) TestPollLoopUpdateScheduledTaskError() {
	task := &db.ScheduledTask{
		ID: 6, ChannelID: "ch1", Schedule: "10m",
		Type: db.TaskTypeInterval, Enabled: true,
	}
	done, signal := signalDone()

	setupDueTasks(s.store, []*db.ScheduledTask{task})
	s.store.On("InsertTaskRunLog", mock.Anything, mock.Anything).Return(int64(60), nil)
	s.executor.On("ExecuteTask", mock.Anything, task).Return("ok", nil)
	s.store.On("UpdateTaskRunLog", mock.Anything, mock.Anything).Return(nil)
	s.store.On("UpdateScheduledTask", mock.Anything, mock.Anything).Return(errors.New("update task fail")).Run(signal)

	ts := NewTaskScheduler(s.store, s.executor, 10*time.Millisecond, s.logger)
	err := ts.Start(context.Background())
	require.NoError(s.T(), err)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		s.T().Fatal("timed out")
	}

	err = ts.Stop()
	require.NoError(s.T(), err)
}

func (s *SchedulerSuite) TestPollLoopParseError() {
	cases := []struct {
		name string
		task *db.ScheduledTask
	}{
		{"cron", &db.ScheduledTask{ID: 7, ChannelID: "ch1", Schedule: "bad cron", Type: db.TaskTypeCron, Enabled: true}},
		{"interval", &db.ScheduledTask{ID: 8, ChannelID: "ch1", Schedule: "not-a-duration", Type: db.TaskTypeInterval, Enabled: true}},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			store := new(testutil.MockStore)
			executor := new(MockTaskExecutor)
			done, signal := signalDone()

			setupDueTasks(store, []*db.ScheduledTask{tc.task})
			store.On("InsertTaskRunLog", mock.Anything, mock.Anything).Return(int64(70), nil)
			executor.On("ExecuteTask", mock.Anything, tc.task).Return("ok", nil).Run(signal)
			store.On("UpdateTaskRunLog", mock.Anything, mock.Anything).Return(nil)

			ts := NewTaskScheduler(store, executor, 10*time.Millisecond, s.logger)
			err := ts.Start(context.Background())
			require.NoError(s.T(), err)

			select {
			case <-done:
			case <-time.After(2 * time.Second):
				s.T().Fatal("timed out")
			}

			time.Sleep(20 * time.Millisecond)

			err = ts.Stop()
			require.NoError(s.T(), err)

			// UpdateScheduledTask should NOT be called because schedule parse fails
			store.AssertNotCalled(s.T(), "UpdateScheduledTask", mock.Anything, mock.Anything)
		})
	}
}

func (s *SchedulerSuite) TestCalculateNextRun() {
	now := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name     string
		taskType db.TaskType
		schedule string
		now      time.Time
		expected time.Time
		errMsg   string
	}{
		{"cron", db.TaskTypeCron, "*/5 * * * *", now, time.Date(2025, 1, 1, 12, 5, 0, 0, time.UTC), ""},
		{"interval", db.TaskTypeInterval, "30m", now, time.Date(2025, 1, 1, 12, 30, 0, 0, time.UTC), ""},
		{"once", db.TaskTypeOnce, "2026-02-09T14:30:00Z", now, time.Date(2026, 2, 9, 14, 30, 0, 0, time.UTC), ""},
		{"invalid cron", db.TaskTypeCron, "bad", now, time.Time{}, "parsing cron schedule"},
		{"invalid interval", db.TaskTypeInterval, "not-valid", now, time.Time{}, "parsing interval"},
		{"invalid once", db.TaskTypeOnce, "bad", now, time.Time{}, "parsing once schedule"},
		{"unknown type", db.TaskType("unknown"), "", now, time.Time{}, "unknown task type"},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			next, err := calculateNextRun(tc.taskType, tc.schedule, tc.now)
			if tc.errMsg != "" {
				require.Error(s.T(), err)
				require.Contains(s.T(), err.Error(), tc.errMsg)
			} else {
				require.NoError(s.T(), err)
				require.Equal(s.T(), tc.expected, next)
			}
		})
	}
}

func (s *SchedulerSuite) TestParseOnceSchedule() {
	tz := time.FixedZone("", 2*60*60)
	cases := []struct {
		name     string
		input    string
		expected time.Time
		wantErr  string
	}{
		{"rfc3339", "2026-02-09T14:30:00Z", time.Date(2026, 2, 9, 14, 30, 0, 0, time.UTC), ""},
		{"with offset", "2026-02-09T14:30:00+02:00", time.Date(2026, 2, 9, 14, 30, 0, 0, tz), ""},
		{"invalid", "not-valid", time.Time{}, "RFC3339"},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			result, err := parseOnceSchedule(tc.input)
			if tc.wantErr != "" {
				require.Error(s.T(), err)
				require.Contains(s.T(), err.Error(), tc.wantErr)
			} else {
				require.NoError(s.T(), err)
				require.True(s.T(), tc.expected.Equal(result))
			}
		})
	}
}

func (s *SchedulerSuite) TestSetTaskEnabled() {
	cases := []struct {
		name     string
		enabled  bool
		storeErr error
	}{
		{"success", false, nil},
		{"error", true, errors.New("db error")},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			store := new(testutil.MockStore)
			store.On("UpdateScheduledTaskEnabled", mock.Anything, int64(42), tc.enabled).Return(tc.storeErr)

			ts := NewTaskScheduler(store, s.executor, time.Second, s.logger)
			err := ts.SetTaskEnabled(context.Background(), 42, tc.enabled)

			if tc.storeErr != nil {
				require.Error(s.T(), err)
				require.Equal(s.T(), tc.storeErr.Error(), err.Error())
			} else {
				require.NoError(s.T(), err)
			}
			store.AssertExpectations(s.T())
		})
	}
}

func (s *SchedulerSuite) TestToggleTask() {
	cases := []struct {
		name      string
		taskID    int64
		task      *db.ScheduledTask
		getErr    error
		updateErr error
		wantState bool
		wantErr   string
	}{
		{
			name:      "enable",
			taskID:    1,
			task:      &db.ScheduledTask{ID: 1, Enabled: false},
			wantState: true,
		},
		{
			name:   "disable",
			taskID: 1,
			task:   &db.ScheduledTask{ID: 1, Enabled: true},
		},
		{
			name:    "not found",
			taskID:  99,
			wantErr: "not found",
		},
		{
			name:    "get error",
			taskID:  1,
			getErr:  errors.New("db error"),
			wantErr: "getting task",
		},
		{
			name:      "update error",
			taskID:    1,
			task:      &db.ScheduledTask{ID: 1, Enabled: true},
			updateErr: errors.New("update error"),
			wantErr:   "update error",
		},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			store := new(testutil.MockStore)
			store.On("GetScheduledTask", mock.Anything, tc.taskID).Return(tc.task, tc.getErr)
			if tc.task != nil && tc.getErr == nil {
				store.On("UpdateScheduledTaskEnabled", mock.Anything, tc.taskID, !tc.task.Enabled).Return(tc.updateErr)
			}

			ts := NewTaskScheduler(store, s.executor, time.Second, s.logger)
			newState, err := ts.ToggleTask(context.Background(), tc.taskID)

			if tc.wantErr != "" {
				require.Error(s.T(), err)
				require.Contains(s.T(), err.Error(), tc.wantErr)
			} else {
				require.NoError(s.T(), err)
				require.Equal(s.T(), tc.wantState, newState)
			}
			store.AssertExpectations(s.T())
		})
	}
}

func (s *SchedulerSuite) TestEditTaskPromptOnly() {
	task := &db.ScheduledTask{
		ID: 1, ChannelID: "ch1", Schedule: "*/5 * * * *",
		Type: db.TaskTypeCron, Prompt: "old prompt", Enabled: true,
	}
	s.store.On("GetScheduledTask", mock.Anything, int64(1)).Return(task, nil)
	s.store.On("UpdateScheduledTask", mock.Anything, mock.MatchedBy(func(t *db.ScheduledTask) bool {
		return t.Prompt == "new prompt" && t.Schedule == "*/5 * * * *"
	})).Return(nil)

	ts := NewTaskScheduler(s.store, s.executor, time.Second, s.logger)
	err := ts.EditTask(context.Background(), 1, nil, nil, new("new prompt"), nil)

	require.NoError(s.T(), err)
	s.store.AssertExpectations(s.T())
}

func (s *SchedulerSuite) TestEditTaskScheduleChange() {
	task := &db.ScheduledTask{
		ID: 1, ChannelID: "ch1", Schedule: "*/5 * * * *",
		Type: db.TaskTypeCron, Prompt: "prompt", Enabled: true,
	}
	s.store.On("GetScheduledTask", mock.Anything, int64(1)).Return(task, nil)
	s.store.On("UpdateScheduledTask", mock.Anything, mock.MatchedBy(func(t *db.ScheduledTask) bool {
		return t.Schedule == "0 9 * * *" && !t.NextRunAt.IsZero()
	})).Return(nil)

	ts := NewTaskScheduler(s.store, s.executor, time.Second, s.logger)
	err := ts.EditTask(context.Background(), 1, new("0 9 * * *"), nil, nil, nil)

	require.NoError(s.T(), err)
	s.store.AssertExpectations(s.T())
}

func (s *SchedulerSuite) TestEditTaskAutoDeleteSec() {
	task := &db.ScheduledTask{
		ID: 1, ChannelID: "ch1", Schedule: "*/5 * * * *",
		Type: db.TaskTypeCron, Prompt: "prompt", Enabled: true,
	}
	s.store.On("GetScheduledTask", mock.Anything, int64(1)).Return(task, nil)
	s.store.On("UpdateScheduledTask", mock.Anything, mock.MatchedBy(func(t *db.ScheduledTask) bool {
		return t.AutoDeleteSec == 300 && t.Prompt == "prompt"
	})).Return(nil)

	ts := NewTaskScheduler(s.store, s.executor, time.Second, s.logger)
	err := ts.EditTask(context.Background(), 1, nil, nil, nil, new(300))

	require.NoError(s.T(), err)
	s.store.AssertExpectations(s.T())
}

func (s *SchedulerSuite) TestEditTaskNotFound() {
	s.store.On("GetScheduledTask", mock.Anything, int64(99)).Return(nil, nil)

	ts := NewTaskScheduler(s.store, s.executor, time.Second, s.logger)
	err := ts.EditTask(context.Background(), 99, nil, nil, nil, nil)

	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "not found")
	s.store.AssertExpectations(s.T())
}

func (s *SchedulerSuite) TestEditTaskGetError() {
	s.store.On("GetScheduledTask", mock.Anything, int64(1)).Return(nil, errors.New("db error"))

	ts := NewTaskScheduler(s.store, s.executor, time.Second, s.logger)
	err := ts.EditTask(context.Background(), 1, nil, nil, nil, nil)

	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "getting task")
	s.store.AssertExpectations(s.T())
}

func (s *SchedulerSuite) TestEditTaskInvalidSchedule() {
	task := &db.ScheduledTask{
		ID: 1, ChannelID: "ch1", Schedule: "*/5 * * * *",
		Type: db.TaskTypeCron, Prompt: "prompt", Enabled: true,
	}
	s.store.On("GetScheduledTask", mock.Anything, int64(1)).Return(task, nil)

	ts := NewTaskScheduler(s.store, s.executor, time.Second, s.logger)
	err := ts.EditTask(context.Background(), 1, new("invalid"), nil, nil, nil)

	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "calculating next run")
	s.store.AssertExpectations(s.T())
}

func (s *SchedulerSuite) TestEditTaskTypeChange() {
	task := &db.ScheduledTask{
		ID: 1, ChannelID: "ch1", Schedule: "2026-02-09T14:30:00Z",
		Type: db.TaskTypeInterval, Prompt: "prompt", Enabled: true,
	}
	s.store.On("GetScheduledTask", mock.Anything, int64(1)).Return(task, nil)
	s.store.On("UpdateScheduledTask", mock.Anything, mock.MatchedBy(func(t *db.ScheduledTask) bool {
		return t.Type == db.TaskTypeOnce && !t.NextRunAt.IsZero()
	})).Return(nil)

	ts := NewTaskScheduler(s.store, s.executor, time.Second, s.logger)
	err := ts.EditTask(context.Background(), 1, nil, new(string(db.TaskTypeOnce)), nil, nil)

	require.NoError(s.T(), err)
	s.store.AssertExpectations(s.T())
}

func (s *SchedulerSuite) TestEditTaskUpdateError() {
	task := &db.ScheduledTask{
		ID: 1, ChannelID: "ch1", Schedule: "*/5 * * * *",
		Type: db.TaskTypeCron, Prompt: "prompt", Enabled: true,
	}
	s.store.On("GetScheduledTask", mock.Anything, int64(1)).Return(task, nil)
	s.store.On("UpdateScheduledTask", mock.Anything, mock.Anything).Return(errors.New("update error"))

	ts := NewTaskScheduler(s.store, s.executor, time.Second, s.logger)
	err := ts.EditTask(context.Background(), 1, nil, nil, new("new"), nil)

	require.Error(s.T(), err)
	require.Equal(s.T(), "update error", err.Error())
	s.store.AssertExpectations(s.T())
}

func (s *SchedulerSuite) TestSchedulerImplementsInterface() {
	var _ Scheduler = (*TaskScheduler)(nil)
}
