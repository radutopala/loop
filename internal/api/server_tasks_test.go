package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/types"
)

// --- CreateTask tests ---

func (s *ServerSuite) TestCreateTaskSuccess() {
	s.store.On("GetChannel", mock.Anything, "ch1").Return(nil, nil)
	s.scheduler.On("AddTask", mock.Anything, mock.MatchedBy(func(task *db.ScheduledTask) bool {
		return task.ChannelID == "ch1" && task.Schedule == "0 9 * * *" &&
			task.Type == db.TaskTypeCron && task.Prompt == "check standup"
	})).Return(int64(42), nil)

	rec := s.testRequest("POST", "/api/tasks", `{"channel_id":"ch1","schedule":"0 9 * * *","type":"cron","prompt":"check standup"}`)

	require.Equal(s.T(), http.StatusCreated, rec.Code)

	var resp createTaskResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(s.T(), int64(42), resp.ID)
	s.scheduler.AssertExpectations(s.T())
}

func (s *ServerSuite) TestCreateTaskWithTemplateName() {
	s.store.On("GetChannel", mock.Anything, "ch1").Return(nil, nil)
	s.scheduler.On("AddTask", mock.Anything, mock.MatchedBy(func(task *db.ScheduledTask) bool {
		return task.ChannelID == "ch1" && task.Schedule == "* * * * *" &&
			task.Type == db.TaskTypeCron && task.Prompt == "dispatch" &&
			task.TemplateName == "tk-auto-worker"
	})).Return(int64(55), nil)

	rec := s.testRequest("POST", "/api/tasks", `{"channel_id":"ch1","schedule":"* * * * *","type":"cron","prompt":"dispatch","template_name":"tk-auto-worker"}`)

	require.Equal(s.T(), http.StatusCreated, rec.Code)

	var resp createTaskResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(s.T(), int64(55), resp.ID)
	s.scheduler.AssertExpectations(s.T())
}

func (s *ServerSuite) TestCreateTaskWithWorktree() {
	s.store.On("GetChannel", mock.Anything, "ch1").Return(nil, nil)
	s.scheduler.On("AddTask", mock.Anything, mock.MatchedBy(func(task *db.ScheduledTask) bool {
		return task.ChannelID == "ch1" && task.Worktree
	})).Return(int64(70), nil)

	rec := s.testRequest("POST", "/api/tasks", `{"channel_id":"ch1","schedule":"0 9 * * *","type":"cron","prompt":"test","worktree":true}`)

	require.Equal(s.T(), http.StatusCreated, rec.Code)
	s.scheduler.AssertExpectations(s.T())
}

func (s *ServerSuite) TestCreateTaskWithOriginBranch() {
	s.store.On("GetChannel", mock.Anything, "ch1").Return(nil, nil)
	s.scheduler.On("AddTask", mock.Anything, mock.MatchedBy(func(task *db.ScheduledTask) bool {
		return task.OriginBranch == "main" && task.UpdateBeforeRun
	})).Return(int64(1), nil)

	rec := s.testRequest("POST", "/api/tasks", `{"channel_id":"ch1","schedule":"0 9 * * *","type":"cron","prompt":"test","origin_branch":"main","update_before_run":true}`)

	require.Equal(s.T(), http.StatusCreated, rec.Code)
	s.scheduler.AssertExpectations(s.T())
}

func (s *ServerSuite) TestCreateTaskBroadcastsEvent() {
	s.store.On("GetChannel", mock.Anything, "ch1").Return(nil, nil)
	s.scheduler.On("AddTask", mock.Anything, mock.Anything).Return(int64(42), nil)

	hub := NewEventsHub(slog.Default())
	s.srv.SetEventsHub(hub)
	defer func() { s.srv.eventsHub = nil }()

	rec := s.testRequest("POST", "/api/tasks", `{"channel_id":"ch1","schedule":"0 9 * * *","type":"cron","prompt":"test"}`)

	require.Equal(s.T(), http.StatusCreated, rec.Code)
	s.scheduler.AssertExpectations(s.T())
}

func (s *ServerSuite) TestCreateTaskSchedulerError() {
	s.store.On("GetChannel", mock.Anything, "ch1").Return(nil, nil)
	s.scheduler.On("AddTask", mock.Anything, mock.Anything).Return(int64(0), errors.New("bad schedule"))

	rec := s.testRequest("POST", "/api/tasks", `{"channel_id":"ch1","schedule":"bad","type":"cron","prompt":"test"}`)

	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	s.scheduler.AssertExpectations(s.T())
}

func (s *ServerSuite) TestCreateTaskSubThreadResolvesToParent() {
	// sub-thread: its parent is also a thread (has parent_id)
	s.store.On("GetChannel", mock.Anything, "sub-thread").
		Return(&db.Channel{ChannelID: "sub-thread", ParentID: "thread-1"}, nil)
	s.store.On("GetChannel", mock.Anything, "thread-1").
		Return(&db.Channel{ChannelID: "thread-1", ParentID: "ch-root"}, nil)
	s.store.On("GetChannel", mock.Anything, "ch-root").
		Return(&db.Channel{ChannelID: "ch-root"}, nil)
	s.scheduler.On("AddTask", mock.Anything, mock.MatchedBy(func(task *db.ScheduledTask) bool {
		return task.ChannelID == "thread-1" // resolved to parent thread
	})).Return(int64(60), nil)

	rec := s.testRequest("POST", "/api/tasks", `{"channel_id":"sub-thread","schedule":"5m","type":"interval","prompt":"test"}`)

	require.Equal(s.T(), http.StatusCreated, rec.Code)
	s.scheduler.AssertExpectations(s.T())
}

func (s *ServerSuite) TestCreateTaskAllowsDirectThread() {
	// direct thread: its parent is a top-level channel (no parent_id)
	s.store.On("GetChannel", mock.Anything, "thread-ok").
		Return(&db.Channel{ChannelID: "thread-ok", ParentID: "ch-root"}, nil)
	s.store.On("GetChannel", mock.Anything, "ch-root").
		Return(&db.Channel{ChannelID: "ch-root", ParentID: ""}, nil)
	s.scheduler.On("AddTask", mock.Anything, mock.Anything).Return(int64(50), nil)

	rec := s.testRequest("POST", "/api/tasks", `{"channel_id":"thread-ok","schedule":"5m","type":"interval","prompt":"test"}`)

	require.Equal(s.T(), http.StatusCreated, rec.Code)
}

// --- ListTasks tests ---

func (s *ServerSuite) TestListTasksSuccess() {
	s.store.On("GetChannel", mock.Anything, "ch1").Return(nil, nil)
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	tasks := []*db.ScheduledTask{
		{ID: 1, ChannelID: "ch1", Schedule: "0 9 * * *", Type: db.TaskTypeCron, Prompt: "task1", Enabled: true, NextRunAt: now, TemplateName: "my-template"},
		{ID: 2, ChannelID: "ch1", Schedule: "5m", Type: db.TaskTypeInterval, Prompt: "task2", Enabled: true, NextRunAt: now},
	}
	s.scheduler.On("ListTasks", mock.Anything, "ch1").Return(tasks, nil)

	rec := s.testRequest("GET", "/api/tasks?channel_id=ch1", "")

	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp []taskResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(s.T(), resp, 2)
	require.Equal(s.T(), int64(1), resp[0].ID)
	require.Equal(s.T(), "task1", resp[0].Prompt)
	require.Equal(s.T(), "my-template", resp[0].TemplateName)
	require.Equal(s.T(), int64(2), resp[1].ID)
	require.Empty(s.T(), resp[1].TemplateName)
	s.scheduler.AssertExpectations(s.T())
}

func (s *ServerSuite) TestListTasksEmpty() {
	s.store.On("GetChannel", mock.Anything, "ch1").Return(nil, nil)
	s.scheduler.On("ListTasks", mock.Anything, "ch1").Return([]*db.ScheduledTask{}, nil)

	rec := s.testRequest("GET", "/api/tasks?channel_id=ch1", "")

	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp []taskResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Empty(s.T(), resp)
	s.scheduler.AssertExpectations(s.T())
}

func (s *ServerSuite) TestListAllTasks() {
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	tasks := []*db.ScheduledTask{
		{ID: 1, ChannelID: "ch1", Schedule: "0 9 * * *", Type: db.TaskTypeCron, Prompt: "task1", Enabled: true, NextRunAt: now},
		{ID: 2, ChannelID: "ch2", Schedule: "5m", Type: db.TaskTypeInterval, Prompt: "task2", Enabled: true, NextRunAt: now},
	}
	channels := []*db.Channel{
		{ChannelID: "ch1", Name: "general", DirPath: "/home/user/project", Platform: types.PlatformLocal},
		{ChannelID: "ch2", Name: "deploy", DirPath: "/home/user/deploy", Platform: types.PlatformLocal, Worktree: true},
	}
	s.store.On("ListAllScheduledTasks", mock.Anything).Return(tasks, nil)
	s.store.On("ListChannels", mock.Anything).Return(channels, nil)

	rec := s.testRequest("GET", "/api/tasks", "")

	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp []taskResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(s.T(), resp, 2)
	require.Equal(s.T(), "ch1", resp[0].ChannelID)
	require.Equal(s.T(), "general", resp[0].ChannelName)
	require.Equal(s.T(), "/home/user/project", resp[0].DirPath)
	require.False(s.T(), resp[0].ChannelWorktree)
	require.Equal(s.T(), "ch2", resp[1].ChannelID)
	require.Equal(s.T(), "deploy", resp[1].ChannelName)
	require.Equal(s.T(), "/home/user/deploy", resp[1].DirPath)
	require.True(s.T(), resp[1].ChannelWorktree)
	s.store.AssertExpectations(s.T())
}

func (s *ServerSuite) TestListAllTasksPlatformFilter() {
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	tasks := []*db.ScheduledTask{
		{ID: 1, ChannelID: "ch-local", Schedule: "0 9 * * *", Type: db.TaskTypeCron, Prompt: "local task", Enabled: true, NextRunAt: now},
		{ID: 2, ChannelID: "ch-discord", Schedule: "5m", Type: db.TaskTypeInterval, Prompt: "discord task", Enabled: true, NextRunAt: now},
	}
	channels := []*db.Channel{
		{ChannelID: "ch-local", Name: "dm", DirPath: "/home/user/project", Platform: types.PlatformLocal},
		{ChannelID: "ch-discord", Name: "bot-channel", DirPath: "/home/user/project", Platform: types.PlatformDiscord},
	}
	s.store.On("ListAllScheduledTasks", mock.Anything).Return(tasks, nil)
	s.store.On("ListChannels", mock.Anything).Return(channels, nil)

	rec := s.testRequest("GET", "/api/tasks?platform=local", "")

	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp []taskResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(s.T(), resp, 1)
	require.Equal(s.T(), "ch-local", resp[0].ChannelID)
	require.Equal(s.T(), "dm", resp[0].ChannelName)
}

func (s *ServerSuite) TestListAllTasksError() {
	s.store.On("ListAllScheduledTasks", mock.Anything).Return(nil, errors.New("db error"))

	rec := s.testRequest("GET", "/api/tasks", "")

	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
}

func (s *ServerSuite) TestListTasksSchedulerError() {
	s.store.On("GetChannel", mock.Anything, "ch1").Return(nil, nil)
	s.scheduler.On("ListTasks", mock.Anything, "ch1").Return(nil, errors.New("db error"))

	rec := s.testRequest("GET", "/api/tasks?channel_id=ch1", "")

	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	s.scheduler.AssertExpectations(s.T())
}

// --- GetTask tests ---

func (s *ServerSuite) TestGetTaskSuccess() {
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	task := &db.ScheduledTask{
		ID: 42, ChannelID: "ch1", Schedule: "0 9 * * *", Type: db.TaskTypeCron,
		Prompt: "full prompt text here", Enabled: true, NextRunAt: now,
		TemplateName: "my-template", AutoDeleteSec: 60,
	}
	s.scheduler.On("GetTask", mock.Anything, int64(42)).Return(task, nil)

	rec := s.testRequest("GET", "/api/tasks/42", "")

	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp taskResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(s.T(), int64(42), resp.ID)
	require.Equal(s.T(), "full prompt text here", resp.Prompt)
	require.Equal(s.T(), "my-template", resp.TemplateName)
	require.Equal(s.T(), 60, resp.AutoDeleteSec)
	s.scheduler.AssertExpectations(s.T())
}

func (s *ServerSuite) TestGetTaskWithWorktree() {
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	task := &db.ScheduledTask{
		ID: 43, ChannelID: "ch1", Schedule: "0 9 * * *", Type: db.TaskTypeCron,
		Prompt: "wt task", Enabled: true, NextRunAt: now, Worktree: true,
		ThreadID: "thread-abc",
	}
	s.scheduler.On("GetTask", mock.Anything, int64(43)).Return(task, nil)

	rec := s.testRequest("GET", "/api/tasks/43", "")

	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp taskResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.True(s.T(), resp.Worktree)
	require.Equal(s.T(), "thread-abc", resp.ThreadID)
	s.scheduler.AssertExpectations(s.T())
}

func (s *ServerSuite) TestGetTaskNotFound() {
	s.scheduler.On("GetTask", mock.Anything, int64(99)).Return(nil, nil)

	rec := s.testRequest("GET", "/api/tasks/99", "")

	require.Equal(s.T(), http.StatusNotFound, rec.Code)
	s.scheduler.AssertExpectations(s.T())
}

func (s *ServerSuite) TestGetTaskInvalidID() {
	rec := s.testRequest("GET", "/api/tasks/abc", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestGetTaskSchedulerError() {
	s.scheduler.On("GetTask", mock.Anything, int64(42)).Return(nil, errors.New("db error"))

	rec := s.testRequest("GET", "/api/tasks/42", "")

	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	s.scheduler.AssertExpectations(s.T())
}

// --- DeleteTask tests ---

func (s *ServerSuite) TestDeleteTaskSuccess() {
	s.scheduler.On("RemoveTask", mock.Anything, int64(42)).Return(nil)

	rec := s.testRequest("DELETE", "/api/tasks/42", "")

	require.Equal(s.T(), http.StatusNoContent, rec.Code)
	s.scheduler.AssertExpectations(s.T())
}

func (s *ServerSuite) TestDeleteTaskInvalidID() {
	rec := s.testRequest("DELETE", "/api/tasks/abc", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestDeleteTaskSchedulerError() {
	s.scheduler.On("RemoveTask", mock.Anything, int64(42)).Return(errors.New("not found"))

	rec := s.testRequest("DELETE", "/api/tasks/42", "")

	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	s.scheduler.AssertExpectations(s.T())
}

func (s *ServerSuite) TestDeleteTaskBroadcastsEvent() {
	s.scheduler.On("RemoveTask", mock.Anything, int64(42)).Return(nil)

	hub := NewEventsHub(slog.Default())
	s.srv.SetEventsHub(hub)
	defer func() { s.srv.eventsHub = nil }()

	rec := s.testRequest("DELETE", "/api/tasks/42", "")

	require.Equal(s.T(), http.StatusNoContent, rec.Code)
	s.scheduler.AssertExpectations(s.T())
}

// --- UpdateTask tests ---

func (s *ServerSuite) TestUpdateTaskToggle() {
	tests := []struct {
		name    string
		body    string
		enabled bool
	}{
		{"Disable", `{"enabled":false}`, false},
		{"Enable", `{"enabled":true}`, true},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.scheduler.On("SetTaskEnabled", mock.Anything, int64(42), tt.enabled).Return(nil).Once()

			rec := s.testRequest("PATCH", "/api/tasks/42", tt.body)

			require.Equal(s.T(), http.StatusOK, rec.Code)
			s.scheduler.AssertExpectations(s.T())
		})
	}
}

func (s *ServerSuite) TestUpdateTaskInvalidID() {
	rec := s.testRequest("PATCH", "/api/tasks/abc", `{"enabled":true}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestUpdateTaskNoFields() {
	rec := s.testRequest("PATCH", "/api/tasks/42", `{}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestUpdateTaskEditPrompt() {
	s.scheduler.On("EditTask", mock.Anything, int64(42), (*string)(nil), (*string)(nil), mock.MatchedBy(func(p *string) bool {
		return p != nil && *p == "new prompt"
	}), (*int)(nil), (*bool)(nil), (*string)(nil), (*bool)(nil), (*string)(nil), (*string)(nil)).Return(nil)

	rec := s.testRequest("PATCH", "/api/tasks/42", `{"prompt":"new prompt"}`)

	require.Equal(s.T(), http.StatusOK, rec.Code)
	s.scheduler.AssertExpectations(s.T())
}

func (s *ServerSuite) TestUpdateTaskEditSchedulerError() {
	s.scheduler.On("EditTask", mock.Anything, int64(42), mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("edit error"))

	rec := s.testRequest("PATCH", "/api/tasks/42", `{"prompt":"new"}`)

	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	s.scheduler.AssertExpectations(s.T())
}

func (s *ServerSuite) TestUpdateTaskEditWorktree() {
	s.scheduler.On("EditTask", mock.Anything, int64(42), (*string)(nil), (*string)(nil), (*string)(nil), (*int)(nil), mock.MatchedBy(func(w *bool) bool {
		return w != nil && *w
	}), (*string)(nil), (*bool)(nil), (*string)(nil), (*string)(nil)).Return(nil)

	rec := s.testRequest("PATCH", "/api/tasks/42", `{"worktree":true}`)

	require.Equal(s.T(), http.StatusOK, rec.Code)
	s.scheduler.AssertExpectations(s.T())
}

func (s *ServerSuite) TestUpdateTaskEditOriginBranch() {
	s.scheduler.On("EditTask", mock.Anything, int64(42), (*string)(nil), (*string)(nil), (*string)(nil), (*int)(nil), (*bool)(nil), mock.MatchedBy(func(ob *string) bool {
		return ob != nil && *ob == "develop"
	}), (*bool)(nil), (*string)(nil), (*string)(nil)).Return(nil)

	rec := s.testRequest("PATCH", "/api/tasks/42", `{"origin_branch":"develop"}`)

	require.Equal(s.T(), http.StatusOK, rec.Code)
	s.scheduler.AssertExpectations(s.T())
}

func (s *ServerSuite) TestUpdateTaskEditUpdateBeforeRun() {
	s.scheduler.On("EditTask", mock.Anything, int64(42), (*string)(nil), (*string)(nil), (*string)(nil), (*int)(nil), (*bool)(nil), (*string)(nil), mock.MatchedBy(func(ubr *bool) bool {
		return ubr != nil && *ubr
	}), (*string)(nil), (*string)(nil)).Return(nil)

	rec := s.testRequest("PATCH", "/api/tasks/42", `{"update_before_run":true}`)

	require.Equal(s.T(), http.StatusOK, rec.Code)
	s.scheduler.AssertExpectations(s.T())
}

func (s *ServerSuite) TestUpdateTaskSchedulerError() {
	s.scheduler.On("SetTaskEnabled", mock.Anything, int64(42), true).Return(errors.New("db error"))

	rec := s.testRequest("PATCH", "/api/tasks/42", `{"enabled":true}`)

	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	s.scheduler.AssertExpectations(s.T())
}

func (s *ServerSuite) TestUpdateTaskBroadcastsEvent() {
	s.scheduler.On("SetTaskEnabled", mock.Anything, int64(42), false).Return(nil)

	hub := NewEventsHub(slog.Default())
	s.srv.SetEventsHub(hub)
	defer func() { s.srv.eventsHub = nil }()

	rec := s.testRequest("PATCH", "/api/tasks/42", `{"enabled":false}`)

	require.Equal(s.T(), http.StatusOK, rec.Code)
	s.scheduler.AssertExpectations(s.T())
}

// --- ListTaskRuns tests ---

func (s *ServerSuite) TestListTaskRunsSuccess() {
	now := time.Date(2025, 6, 1, 10, 0, 0, 0, time.UTC)
	runs := []*db.TaskRunLog{
		{ID: 1, TaskID: 42, Status: db.RunStatusSuccess, ResponseText: "ok", StartedAt: now, FinishedAt: now.Add(time.Second)},
		{ID: 2, TaskID: 42, Status: db.RunStatusFailed, ErrorText: "boom", StartedAt: now.Add(time.Minute), FinishedAt: now.Add(time.Minute + time.Second)},
	}
	s.store.On("ListTaskRunLogs", mock.Anything, int64(42), 50).Return(runs, nil)

	rec := s.testRequest("GET", "/api/tasks/42/runs", "")

	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp []db.TaskRunLog
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(s.T(), resp, 2)
	require.Equal(s.T(), db.RunStatusSuccess, resp[0].Status)
	require.Equal(s.T(), "boom", resp[1].ErrorText)
	s.store.AssertExpectations(s.T())
}

func (s *ServerSuite) TestListTaskRunsInvalidID() {
	rec := s.testRequest("GET", "/api/tasks/abc/runs", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestListTaskRunsStoreError() {
	s.store.On("ListTaskRunLogs", mock.Anything, int64(42), 50).Return(nil, errors.New("db error"))

	rec := s.testRequest("GET", "/api/tasks/42/runs", "")

	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	s.store.AssertExpectations(s.T())
}

// --- RunTask tests ---

func (s *ServerSuite) TestRunTaskSuccess() {
	task := &db.ScheduledTask{ID: 42, ChannelID: "ch1"}
	s.scheduler.On("GetTask", mock.Anything, int64(42)).Return(task, nil)
	s.scheduler.On("RunNow", mock.Anything, int64(42)).Return(nil).Maybe()

	rec := s.testRequest("POST", "/api/tasks/42/run", "")

	require.Equal(s.T(), http.StatusAccepted, rec.Code)
	s.scheduler.AssertExpectations(s.T())
}

func (s *ServerSuite) TestRunTaskNotFound() {
	s.scheduler.On("GetTask", mock.Anything, int64(99)).Return(nil, nil)

	rec := s.testRequest("POST", "/api/tasks/99/run", "")

	require.Equal(s.T(), http.StatusNotFound, rec.Code)
}

func (s *ServerSuite) TestRunTaskInvalidID() {
	rec := s.testRequest("POST", "/api/tasks/abc/run", "")

	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestRunTaskAlreadyRunning() {
	task := &db.ScheduledTask{ID: 42, ChannelID: "ch1", Running: true}
	s.scheduler.On("GetTask", mock.Anything, int64(42)).Return(task, nil)

	rec := s.testRequest("POST", "/api/tasks/42/run", "")

	require.Equal(s.T(), http.StatusConflict, rec.Code)
}

func (s *ServerSuite) TestRunTaskGetTaskError() {
	s.scheduler.On("GetTask", mock.Anything, int64(42)).Return(nil, errors.New("db error"))

	rec := s.testRequest("POST", "/api/tasks/42/run", "")

	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
}

func (s *ServerSuite) TestRunTaskRunNowError() {
	task := &db.ScheduledTask{ID: 42, ChannelID: "ch1"}
	s.scheduler.On("GetTask", mock.Anything, int64(42)).Return(task, nil)
	called := make(chan struct{})
	s.scheduler.On("RunNow", mock.Anything, int64(42)).Return(errors.New("run error")).Run(func(_ mock.Arguments) {
		close(called)
	})

	rec := s.testRequest("POST", "/api/tasks/42/run", "")

	require.Equal(s.T(), http.StatusAccepted, rec.Code)
	select {
	case <-called:
	case <-time.After(time.Second):
		s.T().Fatal("timed out waiting for RunNow goroutine")
	}
}
