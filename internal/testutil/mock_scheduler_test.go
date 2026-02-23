package testutil

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/db"
)

type MockSchedulerSuite struct {
	suite.Suite
	sched *MockScheduler
	ctx   context.Context
}

func TestMockSchedulerSuite(t *testing.T) {
	suite.Run(t, new(MockSchedulerSuite))
}

func (s *MockSchedulerSuite) SetupTest() {
	s.sched = new(MockScheduler)
	s.ctx = context.Background()
}

func (s *MockSchedulerSuite) TestStart() {
	s.sched.On("Start", s.ctx).Return(nil)
	require.NoError(s.T(), s.sched.Start(s.ctx))
	s.sched.AssertExpectations(s.T())
}

func (s *MockSchedulerSuite) TestStop() {
	s.sched.On("Stop").Return(nil)
	require.NoError(s.T(), s.sched.Stop())
	s.sched.AssertExpectations(s.T())
}

func (s *MockSchedulerSuite) TestAddTask() {
	task := &db.ScheduledTask{ChannelID: "ch1"}
	s.sched.On("AddTask", s.ctx, task).Return(int64(1), nil)
	id, err := s.sched.AddTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), int64(1), id)
}

func (s *MockSchedulerSuite) TestRemoveTask() {
	s.sched.On("RemoveTask", s.ctx, int64(1)).Return(nil)
	require.NoError(s.T(), s.sched.RemoveTask(s.ctx, int64(1)))
}

func (s *MockSchedulerSuite) TestListTasks() {
	tasks := []*db.ScheduledTask{{ID: 1}}
	s.sched.On("ListTasks", s.ctx, "ch1").Return(tasks, nil)
	got, err := s.sched.ListTasks(s.ctx, "ch1")
	require.NoError(s.T(), err)
	require.Equal(s.T(), tasks, got)
}

func (s *MockSchedulerSuite) TestListTasksNil() {
	s.sched.On("ListTasks", s.ctx, "ch1").Return(nil, nil)
	got, err := s.sched.ListTasks(s.ctx, "ch1")
	require.NoError(s.T(), err)
	require.Nil(s.T(), got)
}

func (s *MockSchedulerSuite) TestSetTaskEnabled() {
	s.sched.On("SetTaskEnabled", s.ctx, int64(1), true).Return(nil)
	require.NoError(s.T(), s.sched.SetTaskEnabled(s.ctx, int64(1), true))
}

func (s *MockSchedulerSuite) TestToggleTask() {
	s.sched.On("ToggleTask", s.ctx, int64(1)).Return(true, nil)
	enabled, err := s.sched.ToggleTask(s.ctx, int64(1))
	require.NoError(s.T(), err)
	require.True(s.T(), enabled)
}

func (s *MockSchedulerSuite) TestEditTask() {
	sched := "0 9 * * *"
	s.sched.On("EditTask", s.ctx, int64(1), &sched, (*string)(nil), (*string)(nil), (*int)(nil)).Return(nil)
	require.NoError(s.T(), s.sched.EditTask(s.ctx, int64(1), &sched, nil, nil, nil))
}
