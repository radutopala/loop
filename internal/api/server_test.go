package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/radutopala/loop/internal/db"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

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

type MockChannelEnsurer struct {
	mock.Mock
}

func (m *MockChannelEnsurer) EnsureChannel(ctx context.Context, dirPath string) (string, error) {
	args := m.Called(ctx, dirPath)
	return args.String(0), args.Error(1)
}

type MockThreadEnsurer struct {
	mock.Mock
}

func (m *MockThreadEnsurer) CreateThread(ctx context.Context, channelID, name, authorID, message string) (string, error) {
	args := m.Called(ctx, channelID, name, authorID, message)
	return args.String(0), args.Error(1)
}

func (m *MockThreadEnsurer) DeleteThread(ctx context.Context, threadID string) error {
	return m.Called(ctx, threadID).Error(0)
}

type MockChannelLister struct {
	mock.Mock
}

func (m *MockChannelLister) ListChannels(ctx context.Context) ([]*db.Channel, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*db.Channel), args.Error(1)
}

type MockMessageSender struct {
	mock.Mock
}

func (m *MockMessageSender) PostMessage(ctx context.Context, channelID, content string) error {
	return m.Called(ctx, channelID, content).Error(0)
}

type ServerSuite struct {
	suite.Suite
	scheduler *MockScheduler
	channels  *MockChannelEnsurer
	threads   *MockThreadEnsurer
	store     *MockChannelLister
	messages  *MockMessageSender
	srv       *Server
	mux       *http.ServeMux
}

func TestServerSuite(t *testing.T) {
	suite.Run(t, new(ServerSuite))
}

func (s *ServerSuite) SetupTest() {
	s.scheduler = new(MockScheduler)
	s.channels = new(MockChannelEnsurer)
	s.threads = new(MockThreadEnsurer)
	s.store = new(MockChannelLister)
	s.messages = new(MockMessageSender)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s.srv = NewServer(s.scheduler, s.channels, s.threads, s.store, s.messages, logger)

	s.mux = http.NewServeMux()
	s.mux.HandleFunc("GET /api/channels", s.srv.handleSearchChannels)
	s.mux.HandleFunc("POST /api/channels", s.srv.handleEnsureChannel)
	s.mux.HandleFunc("POST /api/messages", s.srv.handleSendMessage)
	s.mux.HandleFunc("POST /api/threads", s.srv.handleCreateThread)
	s.mux.HandleFunc("DELETE /api/threads/{id}", s.srv.handleDeleteThread)
	s.mux.HandleFunc("POST /api/tasks", s.srv.handleCreateTask)
	s.mux.HandleFunc("GET /api/tasks", s.srv.handleListTasks)
	s.mux.HandleFunc("DELETE /api/tasks/{id}", s.srv.handleDeleteTask)
	s.mux.HandleFunc("PATCH /api/tasks/{id}", s.srv.handleUpdateTask)
}

func (s *ServerSuite) TestNewServer() {
	require.NotNil(s.T(), s.srv)
	require.NotNil(s.T(), s.srv.scheduler)
	require.NotNil(s.T(), s.srv.channels)
	require.NotNil(s.T(), s.srv.store)
	require.NotNil(s.T(), s.srv.messages)
	require.NotNil(s.T(), s.srv.logger)
}

// --- CreateTask tests ---

func (s *ServerSuite) TestCreateTaskSuccess() {
	s.scheduler.On("AddTask", mock.Anything, mock.MatchedBy(func(task *db.ScheduledTask) bool {
		return task.ChannelID == "ch1" && task.Schedule == "0 9 * * *" &&
			task.Type == db.TaskTypeCron && task.Prompt == "check standup"
	})).Return(int64(42), nil)

	body := `{"channel_id":"ch1","schedule":"0 9 * * *","type":"cron","prompt":"check standup"}`
	req := httptest.NewRequest("POST", "/api/tasks", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	s.mux.ServeHTTP(rec, req)

	require.Equal(s.T(), http.StatusCreated, rec.Code)

	var resp createTaskResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(s.T(), int64(42), resp.ID)
	s.scheduler.AssertExpectations(s.T())
}

func (s *ServerSuite) TestCreateTaskInvalidBody() {
	req := httptest.NewRequest("POST", "/api/tasks", bytes.NewBufferString("not json"))
	rec := httptest.NewRecorder()

	s.mux.ServeHTTP(rec, req)

	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestCreateTaskSchedulerError() {
	s.scheduler.On("AddTask", mock.Anything, mock.Anything).Return(int64(0), errors.New("bad schedule"))

	body := `{"channel_id":"ch1","schedule":"bad","type":"cron","prompt":"test"}`
	req := httptest.NewRequest("POST", "/api/tasks", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	s.mux.ServeHTTP(rec, req)

	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	s.scheduler.AssertExpectations(s.T())
}

// --- ListTasks tests ---

func (s *ServerSuite) TestListTasksSuccess() {
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	tasks := []*db.ScheduledTask{
		{ID: 1, ChannelID: "ch1", Schedule: "0 9 * * *", Type: db.TaskTypeCron, Prompt: "task1", Enabled: true, NextRunAt: now},
		{ID: 2, ChannelID: "ch1", Schedule: "5m", Type: db.TaskTypeInterval, Prompt: "task2", Enabled: true, NextRunAt: now},
	}
	s.scheduler.On("ListTasks", mock.Anything, "ch1").Return(tasks, nil)

	req := httptest.NewRequest("GET", "/api/tasks?channel_id=ch1", nil)
	rec := httptest.NewRecorder()

	s.mux.ServeHTTP(rec, req)

	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp []taskResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(s.T(), resp, 2)
	require.Equal(s.T(), int64(1), resp[0].ID)
	require.Equal(s.T(), "task1", resp[0].Prompt)
	require.Equal(s.T(), int64(2), resp[1].ID)
	s.scheduler.AssertExpectations(s.T())
}

func (s *ServerSuite) TestListTasksEmpty() {
	s.scheduler.On("ListTasks", mock.Anything, "ch1").Return([]*db.ScheduledTask{}, nil)

	req := httptest.NewRequest("GET", "/api/tasks?channel_id=ch1", nil)
	rec := httptest.NewRecorder()

	s.mux.ServeHTTP(rec, req)

	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp []taskResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Empty(s.T(), resp)
	s.scheduler.AssertExpectations(s.T())
}

func (s *ServerSuite) TestListTasksMissingChannelID() {
	req := httptest.NewRequest("GET", "/api/tasks", nil)
	rec := httptest.NewRecorder()

	s.mux.ServeHTTP(rec, req)

	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestListTasksSchedulerError() {
	s.scheduler.On("ListTasks", mock.Anything, "ch1").Return(nil, errors.New("db error"))

	req := httptest.NewRequest("GET", "/api/tasks?channel_id=ch1", nil)
	rec := httptest.NewRecorder()

	s.mux.ServeHTTP(rec, req)

	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	s.scheduler.AssertExpectations(s.T())
}

// --- DeleteTask tests ---

func (s *ServerSuite) TestDeleteTaskSuccess() {
	s.scheduler.On("RemoveTask", mock.Anything, int64(42)).Return(nil)

	req := httptest.NewRequest("DELETE", "/api/tasks/42", nil)
	rec := httptest.NewRecorder()

	s.mux.ServeHTTP(rec, req)

	require.Equal(s.T(), http.StatusNoContent, rec.Code)
	s.scheduler.AssertExpectations(s.T())
}

func (s *ServerSuite) TestDeleteTaskInvalidID() {
	req := httptest.NewRequest("DELETE", "/api/tasks/abc", nil)
	rec := httptest.NewRecorder()

	s.mux.ServeHTTP(rec, req)

	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestDeleteTaskSchedulerError() {
	s.scheduler.On("RemoveTask", mock.Anything, int64(42)).Return(errors.New("not found"))

	req := httptest.NewRequest("DELETE", "/api/tasks/42", nil)
	rec := httptest.NewRecorder()

	s.mux.ServeHTTP(rec, req)

	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	s.scheduler.AssertExpectations(s.T())
}

// --- UpdateTask tests ---

func (s *ServerSuite) TestUpdateTaskDisable() {
	s.scheduler.On("SetTaskEnabled", mock.Anything, int64(42), false).Return(nil)

	body := `{"enabled":false}`
	req := httptest.NewRequest("PATCH", "/api/tasks/42", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	s.mux.ServeHTTP(rec, req)

	require.Equal(s.T(), http.StatusOK, rec.Code)
	s.scheduler.AssertExpectations(s.T())
}

func (s *ServerSuite) TestUpdateTaskEnable() {
	s.scheduler.On("SetTaskEnabled", mock.Anything, int64(42), true).Return(nil)

	body := `{"enabled":true}`
	req := httptest.NewRequest("PATCH", "/api/tasks/42", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	s.mux.ServeHTTP(rec, req)

	require.Equal(s.T(), http.StatusOK, rec.Code)
	s.scheduler.AssertExpectations(s.T())
}

func (s *ServerSuite) TestUpdateTaskInvalidID() {
	req := httptest.NewRequest("PATCH", "/api/tasks/abc", bytes.NewBufferString(`{"enabled":true}`))
	rec := httptest.NewRecorder()

	s.mux.ServeHTTP(rec, req)

	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestUpdateTaskInvalidBody() {
	req := httptest.NewRequest("PATCH", "/api/tasks/42", bytes.NewBufferString("not json"))
	rec := httptest.NewRecorder()

	s.mux.ServeHTTP(rec, req)

	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestUpdateTaskNoFields() {
	body := `{}`
	req := httptest.NewRequest("PATCH", "/api/tasks/42", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	s.mux.ServeHTTP(rec, req)

	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestUpdateTaskEditPrompt() {
	s.scheduler.On("EditTask", mock.Anything, int64(42), (*string)(nil), (*string)(nil), mock.MatchedBy(func(p *string) bool {
		return p != nil && *p == "new prompt"
	})).Return(nil)

	body := `{"prompt":"new prompt"}`
	req := httptest.NewRequest("PATCH", "/api/tasks/42", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	s.mux.ServeHTTP(rec, req)

	require.Equal(s.T(), http.StatusOK, rec.Code)
	s.scheduler.AssertExpectations(s.T())
}

func (s *ServerSuite) TestUpdateTaskEditSchedulerError() {
	s.scheduler.On("EditTask", mock.Anything, int64(42), mock.Anything, mock.Anything, mock.Anything).Return(errors.New("edit error"))

	body := `{"prompt":"new"}`
	req := httptest.NewRequest("PATCH", "/api/tasks/42", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	s.mux.ServeHTTP(rec, req)

	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	s.scheduler.AssertExpectations(s.T())
}

func (s *ServerSuite) TestUpdateTaskSchedulerError() {
	s.scheduler.On("SetTaskEnabled", mock.Anything, int64(42), true).Return(errors.New("db error"))

	body := `{"enabled":true}`
	req := httptest.NewRequest("PATCH", "/api/tasks/42", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	s.mux.ServeHTTP(rec, req)

	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	s.scheduler.AssertExpectations(s.T())
}

// --- Start / Stop tests ---

func (s *ServerSuite) TestStartAndStop() {
	err := s.srv.Start("127.0.0.1:0")
	require.NoError(s.T(), err)

	err = s.srv.Stop(context.Background())
	require.NoError(s.T(), err)
}

func (s *ServerSuite) TestStartListenError() {
	// Start on port 0 to get a random port first
	err := s.srv.Start("127.0.0.1:0")
	require.NoError(s.T(), err)
	addr := s.srv.server.Addr
	defer func() { _ = s.srv.Stop(context.Background()) }()

	// Try to start another server — won't get the same port, but
	// we can test with an invalid address
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv2 := NewServer(s.scheduler, nil, nil, nil, nil, logger)
	err = srv2.Start("invalid-addr-no-port")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "listening on")
	_ = addr
}

func (s *ServerSuite) TestStopNilServer() {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := NewServer(s.scheduler, nil, nil, nil, nil, logger)

	err := srv.Stop(context.Background())
	require.NoError(s.T(), err)
}

func (s *ServerSuite) TestStartServeError() {
	// Exercise the goroutine error path by closing the listener underneath the server.
	err := s.srv.Start("127.0.0.1:0")
	require.NoError(s.T(), err)

	// Close the listener directly — this causes Serve to return a non-ErrServerClosed error.
	require.NoError(s.T(), s.srv.listener.Close())

	// Give the goroutine time to log the error.
	time.Sleep(50 * time.Millisecond)
}

// --- EnsureChannel tests ---

func (s *ServerSuite) TestEnsureChannelSuccess() {
	s.channels.On("EnsureChannel", mock.Anything, "/home/user/dev/loop").
		Return("ch-123", nil)

	body := `{"dir_path":"/home/user/dev/loop"}`
	req := httptest.NewRequest("POST", "/api/channels", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	s.mux.ServeHTTP(rec, req)

	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp ensureChannelResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(s.T(), "ch-123", resp.ChannelID)
	s.channels.AssertExpectations(s.T())
}

func (s *ServerSuite) TestEnsureChannelMissingDirPath() {
	body := `{"dir_path":""}`
	req := httptest.NewRequest("POST", "/api/channels", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	s.mux.ServeHTTP(rec, req)

	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestEnsureChannelInvalidBody() {
	req := httptest.NewRequest("POST", "/api/channels", bytes.NewBufferString("not json"))
	rec := httptest.NewRecorder()

	s.mux.ServeHTTP(rec, req)

	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestEnsureChannelError() {
	s.channels.On("EnsureChannel", mock.Anything, "/path").
		Return("", errors.New("ensure failed"))

	body := `{"dir_path":"/path"}`
	req := httptest.NewRequest("POST", "/api/channels", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	s.mux.ServeHTTP(rec, req)

	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	s.channels.AssertExpectations(s.T())
}

func (s *ServerSuite) TestEnsureChannelNilEnsurer() {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := NewServer(s.scheduler, nil, nil, nil, nil, logger)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/channels", srv.handleEnsureChannel)

	body := `{"dir_path":"/path"}`
	req := httptest.NewRequest("POST", "/api/channels", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

// --- CreateThread tests ---

func (s *ServerSuite) TestCreateThreadSuccess() {
	s.threads.On("CreateThread", mock.Anything, "ch-1", "my-thread", "", "").
		Return("thread-1", nil)

	body := `{"channel_id":"ch-1","name":"my-thread"}`
	req := httptest.NewRequest("POST", "/api/threads", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	s.mux.ServeHTTP(rec, req)

	require.Equal(s.T(), http.StatusCreated, rec.Code)

	var resp createThreadResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(s.T(), "thread-1", resp.ThreadID)
	s.threads.AssertExpectations(s.T())
}

func (s *ServerSuite) TestCreateThreadSuccessWithAuthorID() {
	s.threads.On("CreateThread", mock.Anything, "ch-1", "my-thread", "user-42", "").
		Return("thread-1", nil)

	body := `{"channel_id":"ch-1","name":"my-thread","author_id":"user-42"}`
	req := httptest.NewRequest("POST", "/api/threads", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	s.mux.ServeHTTP(rec, req)

	require.Equal(s.T(), http.StatusCreated, rec.Code)

	var resp createThreadResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(s.T(), "thread-1", resp.ThreadID)
	s.threads.AssertExpectations(s.T())
}

func (s *ServerSuite) TestCreateThreadSuccessWithMessage() {
	s.threads.On("CreateThread", mock.Anything, "ch-1", "my-thread", "", "Do the task").
		Return("thread-1", nil)

	body := `{"channel_id":"ch-1","name":"my-thread","message":"Do the task"}`
	req := httptest.NewRequest("POST", "/api/threads", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	s.mux.ServeHTTP(rec, req)

	require.Equal(s.T(), http.StatusCreated, rec.Code)

	var resp createThreadResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(s.T(), "thread-1", resp.ThreadID)
	s.threads.AssertExpectations(s.T())
}

func (s *ServerSuite) TestCreateThreadMissingChannelID() {
	body := `{"name":"my-thread"}`
	req := httptest.NewRequest("POST", "/api/threads", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	s.mux.ServeHTTP(rec, req)

	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestCreateThreadMissingName() {
	body := `{"channel_id":"ch-1"}`
	req := httptest.NewRequest("POST", "/api/threads", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	s.mux.ServeHTTP(rec, req)

	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestCreateThreadInvalidBody() {
	req := httptest.NewRequest("POST", "/api/threads", bytes.NewBufferString("not json"))
	rec := httptest.NewRecorder()

	s.mux.ServeHTTP(rec, req)

	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestCreateThreadError() {
	s.threads.On("CreateThread", mock.Anything, "ch-1", "my-thread", "", "").
		Return("", errors.New("create failed"))

	body := `{"channel_id":"ch-1","name":"my-thread"}`
	req := httptest.NewRequest("POST", "/api/threads", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	s.mux.ServeHTTP(rec, req)

	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	s.threads.AssertExpectations(s.T())
}

// --- DeleteThread tests ---

func (s *ServerSuite) TestDeleteThreadSuccess() {
	s.threads.On("DeleteThread", mock.Anything, "thread-1").Return(nil)

	req := httptest.NewRequest("DELETE", "/api/threads/thread-1", nil)
	rec := httptest.NewRecorder()

	s.mux.ServeHTTP(rec, req)

	require.Equal(s.T(), http.StatusNoContent, rec.Code)
	s.threads.AssertExpectations(s.T())
}

func (s *ServerSuite) TestDeleteThreadError() {
	s.threads.On("DeleteThread", mock.Anything, "thread-1").
		Return(errors.New("delete failed"))

	req := httptest.NewRequest("DELETE", "/api/threads/thread-1", nil)
	rec := httptest.NewRecorder()

	s.mux.ServeHTTP(rec, req)

	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	s.threads.AssertExpectations(s.T())
}

func (s *ServerSuite) TestDeleteThreadNilEnsurer() {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := NewServer(s.scheduler, nil, nil, nil, nil, logger)

	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/threads/{id}", srv.handleDeleteThread)

	req := httptest.NewRequest("DELETE", "/api/threads/thread-1", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

func (s *ServerSuite) TestCreateThreadNilEnsurer() {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := NewServer(s.scheduler, nil, nil, nil, nil, logger)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/threads", srv.handleCreateThread)

	body := `{"channel_id":"ch-1","name":"my-thread"}`
	req := httptest.NewRequest("POST", "/api/threads", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

// --- SearchChannels tests ---

func (s *ServerSuite) TestSearchChannelsSuccess() {
	channels := []*db.Channel{
		{ChannelID: "ch-1", Name: "general", DirPath: "/home/user/general", Active: true},
		{ChannelID: "ch-2", Name: "random", DirPath: "/home/user/random", ParentID: "ch-1", Active: false},
	}
	s.store.On("ListChannels", mock.Anything).Return(channels, nil)

	req := httptest.NewRequest("GET", "/api/channels", nil)
	rec := httptest.NewRecorder()

	s.mux.ServeHTTP(rec, req)

	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp []channelResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(s.T(), resp, 2)
	require.Equal(s.T(), "ch-1", resp[0].ChannelID)
	require.Equal(s.T(), "general", resp[0].Name)
	require.True(s.T(), resp[0].Active)
	require.Equal(s.T(), "ch-2", resp[1].ChannelID)
	require.Equal(s.T(), "ch-1", resp[1].ParentID)
	s.store.AssertExpectations(s.T())
}

func (s *ServerSuite) TestSearchChannelsWithQuery() {
	channels := []*db.Channel{
		{ChannelID: "ch-1", Name: "general", DirPath: "/home/user/general", Active: true},
		{ChannelID: "ch-2", Name: "random", DirPath: "/home/user/random", Active: true},
	}
	s.store.On("ListChannels", mock.Anything).Return(channels, nil)

	req := httptest.NewRequest("GET", "/api/channels?query=gen", nil)
	rec := httptest.NewRecorder()

	s.mux.ServeHTTP(rec, req)

	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp []channelResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(s.T(), resp, 1)
	require.Equal(s.T(), "general", resp[0].Name)
	s.store.AssertExpectations(s.T())
}

func (s *ServerSuite) TestSearchChannelsWithQueryNoMatch() {
	channels := []*db.Channel{
		{ChannelID: "ch-1", Name: "general", DirPath: "/home/user/general", Active: true},
	}
	s.store.On("ListChannels", mock.Anything).Return(channels, nil)

	req := httptest.NewRequest("GET", "/api/channels?query=nonexistent", nil)
	rec := httptest.NewRecorder()

	s.mux.ServeHTTP(rec, req)

	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp []channelResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Empty(s.T(), resp)
	s.store.AssertExpectations(s.T())
}

func (s *ServerSuite) TestSearchChannelsEmpty() {
	s.store.On("ListChannels", mock.Anything).Return([]*db.Channel{}, nil)

	req := httptest.NewRequest("GET", "/api/channels", nil)
	rec := httptest.NewRecorder()

	s.mux.ServeHTTP(rec, req)

	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp []channelResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Empty(s.T(), resp)
	s.store.AssertExpectations(s.T())
}

func (s *ServerSuite) TestSearchChannelsError() {
	s.store.On("ListChannels", mock.Anything).Return(nil, errors.New("db error"))

	req := httptest.NewRequest("GET", "/api/channels", nil)
	rec := httptest.NewRecorder()

	s.mux.ServeHTTP(rec, req)

	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	s.store.AssertExpectations(s.T())
}

func (s *ServerSuite) TestSearchChannelsNilStore() {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := NewServer(s.scheduler, nil, nil, nil, nil, logger)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/channels", srv.handleSearchChannels)

	req := httptest.NewRequest("GET", "/api/channels", nil)
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

// --- SendMessage tests ---

func (s *ServerSuite) TestSendMessageSuccess() {
	s.messages.On("PostMessage", mock.Anything, "ch-1", "hello world").Return(nil)

	body := `{"channel_id":"ch-1","content":"hello world"}`
	req := httptest.NewRequest("POST", "/api/messages", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	s.mux.ServeHTTP(rec, req)

	require.Equal(s.T(), http.StatusNoContent, rec.Code)
	s.messages.AssertExpectations(s.T())
}

func (s *ServerSuite) TestSendMessageMissingChannelID() {
	body := `{"content":"hello"}`
	req := httptest.NewRequest("POST", "/api/messages", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	s.mux.ServeHTTP(rec, req)

	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestSendMessageMissingContent() {
	body := `{"channel_id":"ch-1"}`
	req := httptest.NewRequest("POST", "/api/messages", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	s.mux.ServeHTTP(rec, req)

	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestSendMessageInvalidBody() {
	req := httptest.NewRequest("POST", "/api/messages", bytes.NewBufferString("not json"))
	rec := httptest.NewRecorder()

	s.mux.ServeHTTP(rec, req)

	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestSendMessageError() {
	s.messages.On("PostMessage", mock.Anything, "ch-1", "hello").Return(errors.New("send failed"))

	body := `{"channel_id":"ch-1","content":"hello"}`
	req := httptest.NewRequest("POST", "/api/messages", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	s.mux.ServeHTTP(rec, req)

	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	s.messages.AssertExpectations(s.T())
}

func (s *ServerSuite) TestSendMessageNilSender() {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := NewServer(s.scheduler, nil, nil, nil, nil, logger)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/messages", srv.handleSendMessage)

	body := `{"channel_id":"ch-1","content":"hello"}`
	req := httptest.NewRequest("POST", "/api/messages", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	mux.ServeHTTP(rec, req)

	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

// --- containsFold tests ---

func (s *ServerSuite) TestContainsFold() {
	require.True(s.T(), containsFold("General", "gen"))
	require.True(s.T(), containsFold("GENERAL", "general"))
	require.True(s.T(), containsFold("general", "GENERAL"))
	require.False(s.T(), containsFold("general", "random"))
	require.True(s.T(), containsFold("abc", ""))
}
