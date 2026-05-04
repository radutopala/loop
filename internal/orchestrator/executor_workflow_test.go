package orchestrator

import (
	"context"
	"errors"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/workflow"
)

// --- Workflow Engine mock (used only in scheduled workflow tests) ---

type mockWorkflowEngine struct {
	mock.Mock
}

func (m *mockWorkflowEngine) StartRun(ctx context.Context, opts workflow.StartRunOptions) (string, error) {
	args := m.Called(ctx, opts)
	return args.String(0), args.Error(1)
}

func (m *mockWorkflowEngine) ResumeRun(ctx context.Context, runID, response string) error {
	return m.Called(ctx, runID, response).Error(0)
}

func (m *mockWorkflowEngine) CancelRun(ctx context.Context, runID string) error {
	return m.Called(ctx, runID).Error(0)
}

func (m *mockWorkflowEngine) DeleteRun(ctx context.Context, runID string) error {
	return m.Called(ctx, runID).Error(0)
}

func (m *mockWorkflowEngine) RetryRun(ctx context.Context, runID string) (string, error) {
	args := m.Called(ctx, runID)
	return args.String(0), args.Error(1)
}

func (m *mockWorkflowEngine) GetRun(ctx context.Context, runID string) (*db.WorkflowRun, []*db.NodeRun, error) {
	args := m.Called(ctx, runID)
	var run *db.WorkflowRun
	if args.Get(0) != nil {
		run = args.Get(0).(*db.WorkflowRun)
	}
	var nodes []*db.NodeRun
	if args.Get(1) != nil {
		nodes = args.Get(1).([]*db.NodeRun)
	}
	return run, nodes, args.Error(2)
}

func (m *mockWorkflowEngine) ListRuns(ctx context.Context, channelID string, limit, offset int) ([]*db.WorkflowRun, error) {
	args := m.Called(ctx, channelID, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*db.WorkflowRun), args.Error(1)
}

func (m *mockWorkflowEngine) ListWorkflows(ctx context.Context, dirPath, parentDirPath string) ([]config.WorkflowDef, error) {
	args := m.Called(ctx, dirPath, parentDirPath)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]config.WorkflowDef), args.Error(1)
}

func (m *mockWorkflowEngine) RecoverRuns(ctx context.Context) error {
	return m.Called(ctx).Error(0)
}

// --- Scheduled workflow execution tests ---

func (s *TaskExecutorSuite) TestExecuteWorkflowTask() {
	task := &db.ScheduledTask{
		ID:             1,
		ChannelID:      "ch1",
		WorkflowName:   "fix-issue",
		WorkflowInputs: `{"issue_url":"https://github.com/org/repo/issues/42"}`,
	}

	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ChannelID: "ch1",
		DirPath:   "/work/project",
	}, nil)

	wfEngine := new(mockWorkflowEngine)
	wfEngine.On("StartRun", s.ctx, workflow.StartRunOptions{
		WorkflowName: "fix-issue",
		ChannelID:    "ch1",
		DirPath:      "/work/project",
		Inputs:       map[string]string{"issue_url": "https://github.com/org/repo/issues/42"},
	}).Return("run-abc123", nil)

	s.executor.SetWorkflowEngine(wfEngine)

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Contains(s.T(), resp, "run-abc123")

	wfEngine.AssertExpectations(s.T())
	s.store.AssertExpectations(s.T())
}

func (s *TaskExecutorSuite) TestExecuteWorkflowTaskNoInputs() {
	task := &db.ScheduledTask{
		ID:           2,
		ChannelID:    "ch1",
		WorkflowName: "validate",
	}

	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ChannelID: "ch1",
		DirPath:   "/work/project",
	}, nil)

	wfEngine := new(mockWorkflowEngine)
	wfEngine.On("StartRun", s.ctx, workflow.StartRunOptions{
		WorkflowName: "validate",
		ChannelID:    "ch1",
		DirPath:      "/work/project",
		Inputs:       nil,
	}).Return("run-xyz789", nil)

	s.executor.SetWorkflowEngine(wfEngine)

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Contains(s.T(), resp, "run-xyz789")

	wfEngine.AssertExpectations(s.T())
}

func (s *TaskExecutorSuite) TestExecuteWorkflowTaskEngineNotConfigured() {
	task := &db.ScheduledTask{
		ID:           3,
		ChannelID:    "ch1",
		WorkflowName: "fix-issue",
	}

	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ChannelID: "ch1",
		DirPath:   "/work/project",
	}, nil)

	// workflowEngine not set — should error.
	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "workflow engine not configured")
	require.Empty(s.T(), resp)
}

func (s *TaskExecutorSuite) TestExecuteWorkflowTaskStartRunError() {
	task := &db.ScheduledTask{
		ID:           4,
		ChannelID:    "ch1",
		WorkflowName: "broken",
	}

	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ChannelID: "ch1",
		DirPath:   "/work/project",
	}, nil)

	wfEngine := new(mockWorkflowEngine)
	wfEngine.On("StartRun", s.ctx, mock.Anything).Return("", errors.New("workflow not found"))

	s.executor.SetWorkflowEngine(wfEngine)

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "starting workflow")
	require.Empty(s.T(), resp)
}

func (s *TaskExecutorSuite) TestExecuteWorkflowTaskInvalidInputs() {
	task := &db.ScheduledTask{
		ID:             5,
		ChannelID:      "ch1",
		WorkflowName:   "fix-issue",
		WorkflowInputs: `not-valid-json`,
	}

	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{
		ChannelID: "ch1",
		DirPath:   "/work/project",
	}, nil)

	wfEngine := new(mockWorkflowEngine)
	s.executor.SetWorkflowEngine(wfEngine)

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "parsing workflow inputs")
	require.Empty(s.T(), resp)
}
