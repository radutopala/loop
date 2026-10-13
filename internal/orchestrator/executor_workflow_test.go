package orchestrator

import (
	"context"
	"errors"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/types"
	"github.com/radutopala/loop/internal/workflow"
	"github.com/radutopala/loop/internal/worktree"
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

// --- Scheduled plugin action tests ---

func (s *TaskExecutorSuite) TestExecutePluginTask() {
	task := &db.ScheduledTask{ID: 5, ChannelID: "ch1", PluginID: "quiz", PluginAction: "daily"}
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: "/work/project"}, nil)

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.EqualError(s.T(), err, "plugins are disabled")
	require.Empty(s.T(), resp)

	var got []any
	fail := errors.New("not enabled")
	s.executor.SetPluginActions(func(ctx context.Context, pluginID, action, channelID, dirPath string, taskID int64) error {
		got = []any{pluginID, action, channelID, dirPath, taskID}
		return fail
	})
	_, err = s.executor.ExecuteTask(s.ctx, task)
	require.ErrorIs(s.T(), err, fail)
	require.EqualError(s.T(), err, "running the quiz plugin's daily action: not enabled")
	require.Equal(s.T(), []any{"quiz", "daily", "ch1", "/work/project", int64(5)}, got)

	fail = nil
	resp, err = s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "quiz plugin's daily action started", resp)
}

func (s *TaskExecutorSuite) TestExecutePluginTaskWorktreeFirstRun() {
	eb := new(MockEventBroadcaster)
	s.executor.events = eb
	task := &db.ScheduledTask{
		ID: 7, ChannelID: "ch1", Type: db.TaskTypeCron, Schedule: "0 9 * * *",
		PluginID: "quiz", PluginAction: "daily", Worktree: true, AutoDeleteSec: 30,
	}
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: "/proj", Platform: types.PlatformLocal}, nil)
	s.store.On("UpdateScheduledTaskOriginBranch", s.ctx, int64(7), "main").Return(nil)
	s.expectTaskThread(task, "task-thread", true, nil)
	eb.On("BroadcastChannelCreated", "ch1", "task-thread").Once()
	s.bot.On("DeleteThread", mock.Anything, "task-thread").Return(errors.New("gone")).Once()
	eb.On("BroadcastChannelDeleted", "task-thread").Once()
	s.executor.SetWorktreeCreator(&worktree.Creator{
		Sys: &mockWorktreeSys{},
		Run: func(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
			if args[0] == "rev-parse" {
				return []byte("main\n"), nil
			}
			return nil, nil
		},
	})
	var delay time.Duration
	s.executor.timeAfterFunc = func(d time.Duration, f func()) *time.Timer {
		delay = d
		f()
		return time.NewTimer(0)
	}
	var channelID, dirPath string
	s.executor.SetPluginActions(func(ctx context.Context, pluginID, action, ch, dir string, taskID int64) error {
		channelID, dirPath = ch, dir
		return nil
	})

	resp, err := s.executor.ExecuteTask(s.ctx, task)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "quiz plugin's daily action started", resp)
	require.Equal(s.T(), "task-thread", channelID)
	require.Contains(s.T(), dirPath, ".worktrees/task-7-")
	require.Equal(s.T(), 30*time.Second, delay)
	s.bot.AssertExpectations(s.T())
	eb.AssertExpectations(s.T())
}

func (s *TaskExecutorSuite) TestExecutePluginTaskWorktreeThreadFails() {
	task := &db.ScheduledTask{ID: 7, ChannelID: "ch1", Type: db.TaskTypeCron, PluginID: "quiz", PluginAction: "daily", Worktree: true, OriginBranch: "main"}
	s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: "/proj"}, nil)
	s.bot.On("CreateSimpleThread", mock.Anything, "ch1", mock.Anything, "").Return("", errors.New("boom")).Once()
	s.executor.SetWorktreeCreator(&worktree.Creator{
		Sys: &mockWorktreeSys{},
		Run: func(context.Context, string, string, ...string) ([]byte, error) { return nil, nil },
	})
	s.executor.SetPluginActions(func(context.Context, string, string, string, string, int64) error {
		s.T().Fatal("the action must not run")
		return nil
	})

	_, err := s.executor.ExecuteTask(s.ctx, task)
	require.EqualError(s.T(), err, "creating the worktree thread of task 7")
}

func (s *TaskExecutorSuite) TestExecutePluginTaskWorktreeUpdate() {
	tests := []struct {
		name    string
		fail    string
		wantErr string
		wantRun []string
	}{
		{name: "fetch fails", fail: "fetch", wantErr: "fetching origin/main: no remote", wantRun: []string{"fetch"}},
		{name: "rebase fails", fail: "rebase", wantErr: "rebasing onto origin/main: conflict", wantRun: []string{"fetch", "rebase", "rebase"}},
		{name: "updated", wantRun: []string{"fetch", "rebase"}},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			task := &db.ScheduledTask{
				ID: 7, ChannelID: "ch1", Type: db.TaskTypeCron, PluginID: "quiz", PluginAction: "daily",
				Worktree: true, ThreadID: "th", UpdateBeforeRun: true, OriginBranch: "main",
			}
			s.store.On("GetChannel", s.ctx, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: "/proj"}, nil)
			s.store.On("GetChannel", s.ctx, "th").Return(&db.Channel{ChannelID: "th", DirPath: "/wt"}, nil)
			var ran []string
			s.executor.SetWorktreeCreator(&worktree.Creator{
				Sys: &mockWorktreeSys{},
				Run: func(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
					require.Equal(s.T(), "/wt", dir)
					ran = append(ran, args[0])
					switch {
					case tt.fail == "fetch" && args[0] == "fetch":
						return []byte("no remote\n"), errors.New("exit 1")
					case tt.fail == "rebase" && args[0] == "rebase" && args[1] == "--autostash":
						return []byte("conflict\n"), errors.New("exit 1")
					}
					return nil, nil
				},
			})
			var channelID, dirPath string
			s.executor.SetPluginActions(func(ctx context.Context, pluginID, action, ch, dir string, taskID int64) error {
				channelID, dirPath = ch, dir
				return nil
			})

			_, err := s.executor.ExecuteTask(s.ctx, task)
			require.Equal(s.T(), tt.wantRun, ran)
			if tt.wantErr != "" {
				require.EqualError(s.T(), err, tt.wantErr)
				require.Empty(s.T(), channelID)
				return
			}
			require.NoError(s.T(), err)
			require.Equal(s.T(), "th", channelID)
			require.Equal(s.T(), "/wt", dirPath)
		})
	}
}
