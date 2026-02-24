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

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/memory"
	"github.com/radutopala/loop/internal/testutil"
)

type MockChannelEnsurer struct {
	mock.Mock
}

func (m *MockChannelEnsurer) EnsureChannel(ctx context.Context, dirPath string) (string, error) {
	args := m.Called(ctx, dirPath)
	return args.String(0), args.Error(1)
}

func (m *MockChannelEnsurer) CreateChannel(ctx context.Context, name, authorID string) (string, error) {
	args := m.Called(ctx, name, authorID)
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

func (m *MockChannelLister) GetChannel(ctx context.Context, channelID string) (*db.Channel, error) {
	args := m.Called(ctx, channelID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*db.Channel), args.Error(1)
}

type MockMessageSender struct {
	mock.Mock
}

func (m *MockMessageSender) PostMessage(ctx context.Context, channelID, content string) error {
	return m.Called(ctx, channelID, content).Error(0)
}

type MockMemoryIndexer struct {
	mock.Mock
}

func (m *MockMemoryIndexer) Search(ctx context.Context, memoryDir, query string, topK int) ([]memory.SearchResult, error) {
	args := m.Called(ctx, memoryDir, query, topK)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]memory.SearchResult), args.Error(1)
}

func (m *MockMemoryIndexer) Index(ctx context.Context, memoryDir string) (int, error) {
	args := m.Called(ctx, memoryDir)
	return args.Int(0), args.Error(1)
}

type ServerSuite struct {
	suite.Suite
	scheduler *testutil.MockScheduler
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
	s.scheduler = new(testutil.MockScheduler)
	s.channels = new(MockChannelEnsurer)
	s.threads = new(MockThreadEnsurer)
	s.store = new(MockChannelLister)
	s.messages = new(MockMessageSender)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s.srv = NewServer(s.scheduler, s.channels, s.threads, s.store, s.messages, logger)

	s.mux = http.NewServeMux()
	s.mux.HandleFunc("GET /api/channels", s.srv.handleSearchChannels)
	s.mux.HandleFunc("POST /api/channels", s.srv.handleEnsureChannel)
	s.mux.HandleFunc("POST /api/channels/create", s.srv.handleCreateChannel)
	s.mux.HandleFunc("POST /api/messages", s.srv.handleSendMessage)
	s.mux.HandleFunc("POST /api/threads", s.srv.handleCreateThread)
	s.mux.HandleFunc("DELETE /api/threads/{id}", s.srv.handleDeleteThread)
	s.mux.HandleFunc("POST /api/tasks", s.srv.handleCreateTask)
	s.mux.HandleFunc("GET /api/tasks", s.srv.handleListTasks)
	s.mux.HandleFunc("DELETE /api/tasks/{id}", s.srv.handleDeleteTask)
	s.mux.HandleFunc("PATCH /api/tasks/{id}", s.srv.handleUpdateTask)
	s.mux.HandleFunc("POST /api/memory/search", s.srv.handleMemorySearch)
	s.mux.HandleFunc("POST /api/memory/index", s.srv.handleMemoryIndex)
	s.mux.HandleFunc("GET /api/readme", s.srv.handleGetReadme)
}

// testRequest is a helper that sends an HTTP request and returns the recorder.
func (s *ServerSuite) testRequest(method, path, body string) *httptest.ResponseRecorder {
	var req *http.Request
	if body != "" {
		req = httptest.NewRequest(method, path, bytes.NewBufferString(body))
	} else {
		req = httptest.NewRequest(method, path, nil)
	}
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	return rec
}

// nilServer creates a server with nil dependencies for testing not-implemented paths.
func nilServer() *Server {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewServer(nil, nil, nil, nil, nil, logger)
}

func (s *ServerSuite) TestNewServer() {
	require.NotNil(s.T(), s.srv)
	require.NotNil(s.T(), s.srv.scheduler)
	require.NotNil(s.T(), s.srv.channels)
	require.NotNil(s.T(), s.srv.store)
	require.NotNil(s.T(), s.srv.messages)
	require.NotNil(s.T(), s.srv.logger)
}

// --- Invalid JSON body tests (table-driven) ---

func (s *ServerSuite) TestInvalidBodyReturns400() {
	tests := []struct {
		name   string
		method string
		path   string
	}{
		{"CreateTask", "POST", "/api/tasks"},
		{"UpdateTask", "PATCH", "/api/tasks/42"},
		{"EnsureChannel", "POST", "/api/channels"},
		{"CreateChannel", "POST", "/api/channels/create"},
		{"CreateThread", "POST", "/api/threads"},
		{"SendMessage", "POST", "/api/messages"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			rec := s.testRequest(tt.method, tt.path, "not json")
			require.Equal(s.T(), http.StatusBadRequest, rec.Code)
		})
	}
}

func (s *ServerSuite) TestInvalidBodyMemoryEndpoints() {
	indexer := new(MockMemoryIndexer)
	s.srv.SetMemoryIndexer(indexer)

	for _, path := range []string{"/api/memory/search", "/api/memory/index"} {
		s.Run(path, func() {
			rec := s.testRequest("POST", path, "not json")
			require.Equal(s.T(), http.StatusBadRequest, rec.Code)
		})
	}
}

// --- Nil dependency tests (table-driven) ---

func (s *ServerSuite) TestNilDependencyReturns501() {
	srv := nilServer()

	tests := []struct {
		name    string
		method  string
		pattern string
		path    string
		body    string
	}{
		{"EnsureChannel", "POST", "POST /api/channels", "/api/channels", `{"dir_path":"/path"}`},
		{"CreateChannel", "POST", "POST /api/channels/create", "/api/channels/create", `{"name":"trial"}`},
		{"CreateThread", "POST", "POST /api/threads", "/api/threads", `{"channel_id":"ch-1","name":"my-thread"}`},
		{"DeleteThread", "DELETE", "DELETE /api/threads/{id}", "/api/threads/thread-1", ""},
		{"SearchChannels", "GET", "GET /api/channels", "/api/channels", ""},
		{"SendMessage", "POST", "POST /api/messages", "/api/messages", `{"channel_id":"ch-1","content":"hello"}`},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			mux := http.NewServeMux()
			switch tt.name {
			case "EnsureChannel":
				mux.HandleFunc(tt.pattern, srv.handleEnsureChannel)
			case "CreateChannel":
				mux.HandleFunc(tt.pattern, srv.handleCreateChannel)
			case "CreateThread":
				mux.HandleFunc(tt.pattern, srv.handleCreateThread)
			case "DeleteThread":
				mux.HandleFunc(tt.pattern, srv.handleDeleteThread)
			case "SearchChannels":
				mux.HandleFunc(tt.pattern, srv.handleSearchChannels)
			case "SendMessage":
				mux.HandleFunc(tt.pattern, srv.handleSendMessage)
			}

			var req *http.Request
			if tt.body != "" {
				req = httptest.NewRequest(tt.method, tt.path, bytes.NewBufferString(tt.body))
			} else {
				req = httptest.NewRequest(tt.method, tt.path, nil)
			}
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
		})
	}
}

// --- CreateTask tests ---

func (s *ServerSuite) TestCreateTaskSuccess() {
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

func (s *ServerSuite) TestCreateTaskSchedulerError() {
	s.scheduler.On("AddTask", mock.Anything, mock.Anything).Return(int64(0), errors.New("bad schedule"))

	rec := s.testRequest("POST", "/api/tasks", `{"channel_id":"ch1","schedule":"bad","type":"cron","prompt":"test"}`)

	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	s.scheduler.AssertExpectations(s.T())
}

// --- ListTasks tests ---

func (s *ServerSuite) TestListTasksSuccess() {
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
	s.scheduler.On("ListTasks", mock.Anything, "ch1").Return([]*db.ScheduledTask{}, nil)

	rec := s.testRequest("GET", "/api/tasks?channel_id=ch1", "")

	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp []taskResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Empty(s.T(), resp)
	s.scheduler.AssertExpectations(s.T())
}

func (s *ServerSuite) TestListTasksMissingChannelID() {
	rec := s.testRequest("GET", "/api/tasks", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestListTasksSchedulerError() {
	s.scheduler.On("ListTasks", mock.Anything, "ch1").Return(nil, errors.New("db error"))

	rec := s.testRequest("GET", "/api/tasks?channel_id=ch1", "")

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
	}), (*int)(nil)).Return(nil)

	rec := s.testRequest("PATCH", "/api/tasks/42", `{"prompt":"new prompt"}`)

	require.Equal(s.T(), http.StatusOK, rec.Code)
	s.scheduler.AssertExpectations(s.T())
}

func (s *ServerSuite) TestUpdateTaskEditSchedulerError() {
	s.scheduler.On("EditTask", mock.Anything, int64(42), mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("edit error"))

	rec := s.testRequest("PATCH", "/api/tasks/42", `{"prompt":"new"}`)

	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	s.scheduler.AssertExpectations(s.T())
}

func (s *ServerSuite) TestUpdateTaskSchedulerError() {
	s.scheduler.On("SetTaskEnabled", mock.Anything, int64(42), true).Return(errors.New("db error"))

	rec := s.testRequest("PATCH", "/api/tasks/42", `{"enabled":true}`)

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
	srv2 := nilServer()
	err = srv2.Start("invalid-addr-no-port")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "listening on")
	_ = addr
}

func (s *ServerSuite) TestStopNilServer() {
	srv := nilServer()
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

	rec := s.testRequest("POST", "/api/channels", `{"dir_path":"/home/user/dev/loop"}`)

	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp ensureChannelResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(s.T(), "ch-123", resp.ChannelID)
	s.channels.AssertExpectations(s.T())
}

func (s *ServerSuite) TestEnsureChannelMissingDirPath() {
	rec := s.testRequest("POST", "/api/channels", `{"dir_path":""}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestEnsureChannelError() {
	s.channels.On("EnsureChannel", mock.Anything, "/path").
		Return("", errors.New("ensure failed"))

	rec := s.testRequest("POST", "/api/channels", `{"dir_path":"/path"}`)

	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	s.channels.AssertExpectations(s.T())
}

// --- CreateChannel tests ---

func (s *ServerSuite) TestCreateChannelSuccess() {
	s.channels.On("CreateChannel", mock.Anything, "trial", "").
		Return("ch-new", nil)

	rec := s.testRequest("POST", "/api/channels/create", `{"name":"trial"}`)

	require.Equal(s.T(), http.StatusCreated, rec.Code)

	var resp createChannelResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(s.T(), "ch-new", resp.ChannelID)
	s.channels.AssertExpectations(s.T())
}

func (s *ServerSuite) TestCreateChannelMissingName() {
	rec := s.testRequest("POST", "/api/channels/create", `{"name":""}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestCreateChannelWithAuthorID() {
	s.channels.On("CreateChannel", mock.Anything, "trial", "user-42").
		Return("ch-new", nil)

	rec := s.testRequest("POST", "/api/channels/create", `{"name":"trial","author_id":"user-42"}`)

	require.Equal(s.T(), http.StatusCreated, rec.Code)

	var resp createChannelResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(s.T(), "ch-new", resp.ChannelID)
	s.channels.AssertExpectations(s.T())
}

func (s *ServerSuite) TestCreateChannelError() {
	s.channels.On("CreateChannel", mock.Anything, "trial", "").
		Return("", errors.New("create failed"))

	rec := s.testRequest("POST", "/api/channels/create", `{"name":"trial"}`)

	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	s.channels.AssertExpectations(s.T())
}

// --- CreateThread tests ---

func (s *ServerSuite) TestCreateThreadSuccess() {
	s.threads.On("CreateThread", mock.Anything, "ch-1", "my-thread", "", "").
		Return("thread-1", nil)

	rec := s.testRequest("POST", "/api/threads", `{"channel_id":"ch-1","name":"my-thread"}`)

	require.Equal(s.T(), http.StatusCreated, rec.Code)

	var resp createThreadResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(s.T(), "thread-1", resp.ThreadID)
	s.threads.AssertExpectations(s.T())
}

func (s *ServerSuite) TestCreateThreadSuccessWithAuthorID() {
	s.threads.On("CreateThread", mock.Anything, "ch-1", "my-thread", "user-42", "").
		Return("thread-1", nil)

	rec := s.testRequest("POST", "/api/threads", `{"channel_id":"ch-1","name":"my-thread","author_id":"user-42"}`)

	require.Equal(s.T(), http.StatusCreated, rec.Code)

	var resp createThreadResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(s.T(), "thread-1", resp.ThreadID)
	s.threads.AssertExpectations(s.T())
}

func (s *ServerSuite) TestCreateThreadSuccessWithMessage() {
	s.threads.On("CreateThread", mock.Anything, "ch-1", "my-thread", "", "Do the task").
		Return("thread-1", nil)

	rec := s.testRequest("POST", "/api/threads", `{"channel_id":"ch-1","name":"my-thread","message":"Do the task"}`)

	require.Equal(s.T(), http.StatusCreated, rec.Code)

	var resp createThreadResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(s.T(), "thread-1", resp.ThreadID)
	s.threads.AssertExpectations(s.T())
}

func (s *ServerSuite) TestCreateThreadMissingFields() {
	tests := []struct {
		name string
		body string
	}{
		{"MissingChannelID", `{"name":"my-thread"}`},
		{"MissingName", `{"channel_id":"ch-1"}`},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			rec := s.testRequest("POST", "/api/threads", tt.body)
			require.Equal(s.T(), http.StatusBadRequest, rec.Code)
		})
	}
}

func (s *ServerSuite) TestCreateThreadError() {
	s.threads.On("CreateThread", mock.Anything, "ch-1", "my-thread", "", "").
		Return("", errors.New("create failed"))

	rec := s.testRequest("POST", "/api/threads", `{"channel_id":"ch-1","name":"my-thread"}`)

	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	s.threads.AssertExpectations(s.T())
}

// --- DeleteThread tests ---

func (s *ServerSuite) TestDeleteThreadSuccess() {
	s.threads.On("DeleteThread", mock.Anything, "thread-1").Return(nil)

	rec := s.testRequest("DELETE", "/api/threads/thread-1", "")

	require.Equal(s.T(), http.StatusNoContent, rec.Code)
	s.threads.AssertExpectations(s.T())
}

func (s *ServerSuite) TestDeleteThreadError() {
	s.threads.On("DeleteThread", mock.Anything, "thread-1").
		Return(errors.New("delete failed"))

	rec := s.testRequest("DELETE", "/api/threads/thread-1", "")

	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	s.threads.AssertExpectations(s.T())
}

// --- SearchChannels tests ---

func (s *ServerSuite) TestSearchChannelsSuccess() {
	channels := []*db.Channel{
		{ChannelID: "ch-1", Name: "general", DirPath: "/home/user/general", Active: true},
		{ChannelID: "ch-2", Name: "random", DirPath: "/home/user/random", ParentID: "ch-1", Active: false},
	}
	s.store.On("ListChannels", mock.Anything).Return(channels, nil)

	rec := s.testRequest("GET", "/api/channels", "")

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

	rec := s.testRequest("GET", "/api/channels?query=gen", "")

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

	rec := s.testRequest("GET", "/api/channels?query=nonexistent", "")

	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp []channelResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Empty(s.T(), resp)
	s.store.AssertExpectations(s.T())
}

func (s *ServerSuite) TestSearchChannelsEmpty() {
	s.store.On("ListChannels", mock.Anything).Return([]*db.Channel{}, nil)

	rec := s.testRequest("GET", "/api/channels", "")

	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp []channelResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Empty(s.T(), resp)
	s.store.AssertExpectations(s.T())
}

func (s *ServerSuite) TestSearchChannelsError() {
	s.store.On("ListChannels", mock.Anything).Return(nil, errors.New("db error"))

	rec := s.testRequest("GET", "/api/channels", "")

	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	s.store.AssertExpectations(s.T())
}

// --- SendMessage tests ---

func (s *ServerSuite) TestSendMessageSuccess() {
	s.messages.On("PostMessage", mock.Anything, "ch-1", "hello world").Return(nil)

	rec := s.testRequest("POST", "/api/messages", `{"channel_id":"ch-1","content":"hello world"}`)

	require.Equal(s.T(), http.StatusNoContent, rec.Code)
	s.messages.AssertExpectations(s.T())
}

func (s *ServerSuite) TestSendMessageMissingFields() {
	tests := []struct {
		name string
		body string
	}{
		{"MissingChannelID", `{"content":"hello"}`},
		{"MissingContent", `{"channel_id":"ch-1"}`},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			rec := s.testRequest("POST", "/api/messages", tt.body)
			require.Equal(s.T(), http.StatusBadRequest, rec.Code)
		})
	}
}

func (s *ServerSuite) TestSendMessageError() {
	s.messages.On("PostMessage", mock.Anything, "ch-1", "hello").Return(errors.New("send failed"))

	rec := s.testRequest("POST", "/api/messages", `{"channel_id":"ch-1","content":"hello"}`)

	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	s.messages.AssertExpectations(s.T())
}

// --- MemorySearch tests ---

func (s *ServerSuite) TestMemorySearchSuccess() {
	indexer := new(MockMemoryIndexer)
	s.srv.SetMemoryIndexer(indexer)

	indexer.On("Search", mock.Anything, "/tmp/memory", "docker tips", 3).
		Return([]memory.SearchResult{
			{FilePath: "/tmp/memory/MEMORY.md", Content: "Tips", Score: 0.95},
		}, nil)

	rec := s.testRequest("POST", "/api/memory/search", `{"query":"docker tips","top_k":3,"dir_path":"/tmp/memory"}`)

	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp memorySearchResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(s.T(), resp.Results, 1)
	require.Equal(s.T(), "/tmp/memory/MEMORY.md", resp.Results[0].FilePath)
	require.InDelta(s.T(), 0.95, float64(resp.Results[0].Score), 0.001)
	indexer.AssertExpectations(s.T())
}

func (s *ServerSuite) TestMemorySearchNotConfigured() {
	rec := s.testRequest("POST", "/api/memory/search", `{"query":"test","dir_path":"/tmp/memory"}`)
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

func (s *ServerSuite) TestMemorySearchValidation() {
	indexer := new(MockMemoryIndexer)
	s.srv.SetMemoryIndexer(indexer)

	tests := []struct {
		name string
		body string
	}{
		{"EmptyQuery", `{"query":"","dir_path":"/tmp/memory"}`},
		{"EmptyDirPathAndChannelID", `{"query":"test"}`},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			rec := s.testRequest("POST", "/api/memory/search", tt.body)
			require.Equal(s.T(), http.StatusBadRequest, rec.Code)
		})
	}
}

func (s *ServerSuite) TestMemorySearchByChannelID() {
	indexer := new(MockMemoryIndexer)
	s.srv.SetMemoryIndexer(indexer)

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: "/home/user/project"}, nil)
	indexer.On("Search", mock.Anything, "/home/user/project", "docker tips", 5).
		Return([]memory.SearchResult{
			{FilePath: "/tmp/mem/MEMORY.md", Content: "Tips", Score: 0.9},
		}, nil)

	rec := s.testRequest("POST", "/api/memory/search", `{"query":"docker tips","top_k":5,"channel_id":"ch-1"}`)

	require.Equal(s.T(), http.StatusOK, rec.Code)
	var resp memorySearchResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(s.T(), resp.Results, 1)
	s.store.AssertExpectations(s.T())
	indexer.AssertExpectations(s.T())
}

func (s *ServerSuite) TestMemorySearchByChannelIDNotFound() {
	indexer := new(MockMemoryIndexer)
	s.srv.SetMemoryIndexer(indexer)

	s.store.On("GetChannel", mock.Anything, "ch-unknown").
		Return(nil, nil)

	rec := s.testRequest("POST", "/api/memory/search", `{"query":"test","channel_id":"ch-unknown"}`)

	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
	s.store.AssertExpectations(s.T())
}

func (s *ServerSuite) TestMemorySearchError() {
	indexer := new(MockMemoryIndexer)
	s.srv.SetMemoryIndexer(indexer)

	indexer.On("Search", mock.Anything, "/tmp/memory", "test", 0).
		Return(nil, errors.New("search failed"))

	rec := s.testRequest("POST", "/api/memory/search", `{"query":"test","dir_path":"/tmp/memory"}`)

	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	indexer.AssertExpectations(s.T())
}

// --- MemoryIndex tests ---

func (s *ServerSuite) TestMemoryIndexSuccess() {
	indexer := new(MockMemoryIndexer)
	s.srv.SetMemoryIndexer(indexer)

	indexer.On("Index", mock.Anything, "/tmp/memory").Return(15, nil)

	rec := s.testRequest("POST", "/api/memory/index", `{"dir_path":"/tmp/memory"}`)

	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp memoryIndexResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(s.T(), 15, resp.Count)
	indexer.AssertExpectations(s.T())
}

func (s *ServerSuite) TestMemoryIndexNotConfigured() {
	rec := s.testRequest("POST", "/api/memory/index", `{"dir_path":"/tmp/memory"}`)
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

func (s *ServerSuite) TestMemoryIndexEmptyDirPathAndChannelID() {
	indexer := new(MockMemoryIndexer)
	s.srv.SetMemoryIndexer(indexer)

	rec := s.testRequest("POST", "/api/memory/index", `{}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestMemoryIndexByChannelID() {
	indexer := new(MockMemoryIndexer)
	s.srv.SetMemoryIndexer(indexer)

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: "/home/user/project"}, nil)
	indexer.On("Index", mock.Anything, "/home/user/project").Return(10, nil)

	rec := s.testRequest("POST", "/api/memory/index", `{"channel_id":"ch-1"}`)

	require.Equal(s.T(), http.StatusOK, rec.Code)
	var resp memoryIndexResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(s.T(), 10, resp.Count)
	s.store.AssertExpectations(s.T())
	indexer.AssertExpectations(s.T())
}

func (s *ServerSuite) TestMemoryIndexError() {
	indexer := new(MockMemoryIndexer)
	s.srv.SetMemoryIndexer(indexer)

	indexer.On("Index", mock.Anything, "/tmp/memory").Return(0, errors.New("index failed"))

	rec := s.testRequest("POST", "/api/memory/index", `{"dir_path":"/tmp/memory"}`)

	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	indexer.AssertExpectations(s.T())
}

func (s *ServerSuite) TestMemorySearchByChannelIDLookupError() {
	indexer := new(MockMemoryIndexer)
	s.srv.SetMemoryIndexer(indexer)

	s.store.On("GetChannel", mock.Anything, "ch-err").
		Return(nil, errors.New("db error"))

	rec := s.testRequest("POST", "/api/memory/search", `{"query":"test","channel_id":"ch-err"}`)

	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
	s.store.AssertExpectations(s.T())
}

func (s *ServerSuite) TestMemorySearchByChannelIDEmptyDirPath() {
	indexer := new(MockMemoryIndexer)
	s.srv.SetMemoryIndexer(indexer)

	s.store.On("GetChannel", mock.Anything, "ch-nodir").
		Return(&db.Channel{ChannelID: "ch-nodir", DirPath: ""}, nil)

	// Without loopDir set, should return error.
	rec := s.testRequest("POST", "/api/memory/search", `{"query":"test","channel_id":"ch-nodir"}`)

	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
	s.store.AssertExpectations(s.T())
}

func (s *ServerSuite) TestMemorySearchByChannelIDEmptyDirPathFallback() {
	indexer := new(MockMemoryIndexer)
	s.srv.SetMemoryIndexer(indexer)
	s.srv.SetLoopDir("/home/test/.loop")

	s.store.On("GetChannel", mock.Anything, "ch-nodir").
		Return(&db.Channel{ChannelID: "ch-nodir", DirPath: ""}, nil)
	indexer.On("Search", mock.Anything, "/home/test/.loop/ch-nodir/work", "test", 0).
		Return([]memory.SearchResult{}, nil)

	rec := s.testRequest("POST", "/api/memory/search", `{"query":"test","channel_id":"ch-nodir"}`)

	require.Equal(s.T(), http.StatusOK, rec.Code)
	indexer.AssertExpectations(s.T())
	s.store.AssertExpectations(s.T())

	// Clean up loopDir for other tests.
	s.srv.SetLoopDir("")
}

func (s *ServerSuite) TestMemorySearchByChannelIDNilStore() {
	srv := nilServer()
	indexer := new(MockMemoryIndexer)
	srv.SetMemoryIndexer(indexer)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/memory/search", srv.handleMemorySearch)

	req := httptest.NewRequest("POST", "/api/memory/search", bytes.NewBufferString(`{"query":"test","channel_id":"ch-1"}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

// --- SetMemoryIndexer ---

func (s *ServerSuite) TestSetMemoryIndexer() {
	indexer := new(MockMemoryIndexer)
	s.srv.SetMemoryIndexer(indexer)
	require.NotNil(s.T(), s.srv.memoryIndexer)
}

// --- GetReadme tests ---

func (s *ServerSuite) TestGetReadme() {
	rec := s.testRequest("GET", "/api/readme", "")

	require.Equal(s.T(), http.StatusOK, rec.Code)
	require.Equal(s.T(), "text/plain; charset=utf-8", rec.Header().Get("Content-Type"))
	require.NotEmpty(s.T(), rec.Body.String())
}

// --- containsFold tests ---

func (s *ServerSuite) TestContainsFold() {
	require.True(s.T(), containsFold("General", "gen"))
	require.True(s.T(), containsFold("GENERAL", "general"))
	require.True(s.T(), containsFold("general", "GENERAL"))
	require.False(s.T(), containsFold("general", "random"))
	require.True(s.T(), containsFold("abc", ""))
}
