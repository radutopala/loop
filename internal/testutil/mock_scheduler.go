package testutil

import (
	"context"

	"github.com/stretchr/testify/mock"

	"github.com/radutopala/loop/internal/db"
)

// MockScheduler implements the scheduler.Scheduler / orchestrator.Scheduler interface for testing.
type MockScheduler struct {
	mock.Mock
}

func (m *MockScheduler) Start(ctx context.Context) error {
	return m.Called(ctx).Error(0)
}

func (m *MockScheduler) Stop() error {
	return m.Called().Error(0)
}

func (m *MockScheduler) AddTask(ctx context.Context, task *db.ScheduledTask) (int64, error) {
	args := m.Called(ctx, task)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockScheduler) RemoveTask(ctx context.Context, taskID int64) error {
	return m.Called(ctx, taskID).Error(0)
}

func (m *MockScheduler) ListTasks(ctx context.Context, channelID string) ([]*db.ScheduledTask, error) {
	args := m.Called(ctx, channelID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*db.ScheduledTask), args.Error(1)
}

func (m *MockScheduler) SetTaskEnabled(ctx context.Context, taskID int64, enabled bool) error {
	return m.Called(ctx, taskID, enabled).Error(0)
}

func (m *MockScheduler) ToggleTask(ctx context.Context, taskID int64) (bool, error) {
	args := m.Called(ctx, taskID)
	return args.Bool(0), args.Error(1)
}

func (m *MockScheduler) EditTask(ctx context.Context, taskID int64, schedule, taskType, prompt *string) error {
	return m.Called(ctx, taskID, schedule, taskType, prompt).Error(0)
}
