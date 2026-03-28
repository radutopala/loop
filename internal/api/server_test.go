package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/agentregistry"
	"github.com/radutopala/loop/internal/bot"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/memory"
	"github.com/radutopala/loop/internal/testutil"
	"github.com/radutopala/loop/internal/types"
)

type MockChannelEnsurer struct {
	mock.Mock
}

func (m *MockChannelEnsurer) EnsureChannel(ctx context.Context, dirPath, platform string) (string, error) {
	args := m.Called(ctx, dirPath, platform)
	return args.String(0), args.Error(1)
}

func (m *MockChannelEnsurer) EnsureChannelAllPlatforms(ctx context.Context, dirPath string) ([]EnsureResult, error) {
	args := m.Called(ctx, dirPath)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]EnsureResult), args.Error(1)
}

func (m *MockChannelEnsurer) CreateChannel(ctx context.Context, name, authorID, sourceChannelID, platform string) (string, error) {
	args := m.Called(ctx, name, authorID, sourceChannelID, platform)
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

// MockChannelLister aliases testutil.MockStore which satisfies the ChannelLister interface.
type MockChannelLister = testutil.MockStore

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

type MockRunningChannelLister struct {
	mock.Mock
}

func (m *MockRunningChannelLister) RunningChannelIDs(ctx context.Context) (map[string]struct{}, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(map[string]struct{}), args.Error(1)
}

type MockActiveChatLister struct {
	mock.Mock
}

func (m *MockActiveChatLister) ActiveChatChannelIDs() map[string]struct{} {
	args := m.Called()
	if args.Get(0) == nil {
		return nil
	}
	return args.Get(0).(map[string]struct{})
}

type MockIncomingMessageHandler struct {
	mock.Mock
}

func (m *MockIncomingMessageHandler) HandleIncomingMessage(ctx context.Context, channelID, authorID, content, mode string) {
	m.Called(ctx, channelID, authorID, content, mode)
}

func (m *MockIncomingMessageHandler) HandleThreadCreated(ctx context.Context, threadID, authorID, message string) {
	m.Called(ctx, threadID, authorID, message)
}

type MockInteractionHandler struct {
	mock.Mock
	called chan struct{}
}

func newMockInteractionHandler() *MockInteractionHandler {
	return &MockInteractionHandler{called: make(chan struct{}, 1)}
}

func (m *MockInteractionHandler) HandleInteraction(ctx context.Context, inter *bot.Interaction) {
	m.Called(ctx, inter)
	select {
	case m.called <- struct{}{}:
	default:
	}
}

type ServerSuite struct {
	suite.Suite
	scheduler *testutil.MockScheduler
	channels  *MockChannelEnsurer
	threads   *MockThreadEnsurer
	store     *MockChannelLister
	messages  *MockMessageSender
	sys       *testutil.MockSystem
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

	s.sys = new(testutil.MockSystem)
	s.sys.On("ReadDir", mock.Anything).Return(nil, nil)
	s.sys.On("ReadFile", mock.Anything).Return(nil, nil)
	s.sys.On("WriteFile", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	s.sys.On("Stat", mock.Anything).Return(nil, os.ErrNotExist)
	s.sys.On("Remove", mock.Anything).Return(nil)
	s.sys.On("MkdirAll", mock.Anything, mock.Anything).Return(nil)
	s.sys.On("UserHomeDir").Return("/home/testuser", nil)

	s.mux = http.NewServeMux()
	s.mux.HandleFunc("GET /api/channels", s.srv.handleSearchChannels)
	s.mux.HandleFunc("POST /api/channels", s.srv.handleEnsureChannel)
	s.mux.HandleFunc("POST /api/channels/create", s.srv.handleCreateChannel)
	s.mux.HandleFunc("POST /api/channels/ensure-all", s.srv.handleEnsureAllChannels)
	s.mux.HandleFunc("POST /api/messages", s.srv.handleSendMessage)
	s.mux.HandleFunc("POST /api/threads", s.srv.handleCreateThread)
	s.mux.HandleFunc("DELETE /api/threads/{id}", s.srv.handleDeleteThread)
	s.mux.HandleFunc("DELETE /api/channels/{id}", s.srv.handleDeleteChannel)
	s.mux.HandleFunc("POST /api/tasks", s.srv.handleCreateTask)
	s.mux.HandleFunc("GET /api/tasks", s.srv.handleListTasks)
	s.mux.HandleFunc("GET /api/tasks/{id}", s.srv.handleGetTask)
	s.mux.HandleFunc("DELETE /api/tasks/{id}", s.srv.handleDeleteTask)
	s.mux.HandleFunc("PATCH /api/tasks/{id}", s.srv.handleUpdateTask)
	s.mux.HandleFunc("GET /api/channels/{id}/sessions", s.srv.handleListSessions)
	s.mux.HandleFunc("GET /api/channels/{id}/messages", s.srv.handleListMessages)
	s.mux.HandleFunc("GET /api/messages/search", s.srv.handleSearchMessages)
	s.mux.HandleFunc("POST /api/commands", s.srv.handleCommand)
	s.mux.HandleFunc("POST /api/memory/search", s.srv.handleMemorySearch)
	s.mux.HandleFunc("POST /api/memory/index", s.srv.handleMemoryIndex)
	s.mux.HandleFunc("GET /api/memory/files", s.srv.handleListMemoryFiles)
	s.mux.HandleFunc("GET /api/memory/files/search", s.srv.handleSearchMemoryFiles)
	s.mux.HandleFunc("GET /api/memory/file", s.srv.handleReadMemoryFile)
	s.mux.HandleFunc("PUT /api/memory/file", s.srv.handleWriteMemoryFile)
	s.mux.HandleFunc("GET /api/channels/{id}/roots", s.srv.handleListRoots)
	s.mux.HandleFunc("GET /api/channels/{id}/files", s.srv.handleListFiles)
	s.mux.HandleFunc("GET /api/channels/{id}/file", s.srv.handleReadFile)
	s.mux.HandleFunc("PUT /api/channels/{id}/file", s.srv.handleWriteFile)
	s.mux.HandleFunc("DELETE /api/channels/{id}/file", s.srv.handleDeleteFile)
	s.mux.HandleFunc("GET /api/readme", s.srv.handleGetReadme)
	s.mux.HandleFunc("GET /api/channels/{id}/branches", s.srv.handleListBranches)
	s.mux.HandleFunc("POST /api/channels/{id}/branches/switch", s.srv.handleSwitchBranch)
	s.mux.HandleFunc("POST /api/channels/{id}/branches/create", s.srv.handleCreateBranch)
	s.mux.HandleFunc("POST /api/worktrees", s.srv.handleCreateWorktree)
	s.mux.HandleFunc("POST /api/worktrees/import", s.srv.handleImportWorktree)
	s.mux.HandleFunc("POST /api/agents", s.srv.handleRegisterAgent)
	s.mux.HandleFunc("GET /api/agents", s.srv.handleListAgents)
	s.mux.HandleFunc("PATCH /api/agents/{id}", s.srv.handleUpdateAgent)
	s.mux.HandleFunc("DELETE /api/agents/{id}", s.srv.handleDeleteAgent)
	s.mux.HandleFunc("POST /api/agents/{id}/message", s.srv.handleSendAgentMessage)
	s.mux.HandleFunc("GET /api/ws/agent-channel", s.srv.handleAgentChannelWS)
	s.mux.HandleFunc("GET /api/config/schema", s.srv.handleConfigSchema)
	s.mux.HandleFunc("GET /api/config", s.srv.handleGetConfig)
	s.mux.HandleFunc("PUT /api/config", s.srv.handleSaveConfig)
	s.mux.HandleFunc("GET /api/config/project", s.srv.handleGetProjectConfig)
	s.mux.HandleFunc("PUT /api/config/project", s.srv.handleSaveProjectConfig)
	s.mux.HandleFunc("GET /api/health", handleHealth)
	s.mux.HandleFunc("GET /api/ws/terminal", s.srv.handleTerminalWS)
	s.mux.HandleFunc("GET /api/ws", s.srv.handleEventsWS)
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

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// nilServer creates a server with nil dependencies for testing not-implemented paths.
func nilServer() *Server {
	return NewServer(nil, nil, nil, nil, nil, testLogger())
}

func (s *ServerSuite) TestNewServer() {
	require.NotNil(s.T(), s.srv)
	require.NotNil(s.T(), s.srv.scheduler)
	require.NotNil(s.T(), s.srv.channels)
	require.NotNil(s.T(), s.srv.store)
	require.NotNil(s.T(), s.srv.messages)
	require.NotNil(s.T(), s.srv.logger)
}

func (s *ServerSuite) TestHealth() {
	rec := s.testRequest("GET", "/api/health", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	require.JSONEq(s.T(), `{"status":"ok"}`, rec.Body.String())
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
		{"EnsureAllChannels", "POST", "POST /api/channels/ensure-all", "/api/channels/ensure-all", `{"dir_path":"/path"}`},
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
			case "EnsureAllChannels":
				mux.HandleFunc(tt.pattern, srv.handleEnsureAllChannels)
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

func (s *ServerSuite) TestListTasksMissingChannelID() {
	rec := s.testRequest("GET", "/api/tasks", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
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

func (s *ServerSuite) TestStopWithInjectedError() {
	srv := nilServer()
	require.NoError(s.T(), srv.Start("127.0.0.1:0"))

	injectedErr := errors.New("injected stop error")
	srv.SetStopError(injectedErr)

	err := srv.Stop(context.Background())
	require.ErrorIs(s.T(), err, injectedErr)
}

func (s *ServerSuite) TestStopWithInjectedErrorNilHTTPServer() {
	srv := nilServer()
	injectedErr := errors.New("injected stop error")
	srv.SetStopError(injectedErr)

	err := srv.Stop(context.Background())
	require.ErrorIs(s.T(), err, injectedErr)
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

func (s *ServerSuite) TestBuildMux() {
	mux := s.srv.buildMux()
	require.NotNil(s.T(), mux)
}

func (s *ServerSuite) TestSetScreenshotDir() {
	s.srv.SetScreenshotDir("/tmp/screenshots")
	require.Equal(s.T(), "/tmp/screenshots", s.srv.screenshotDir)
}

// --- EnsureChannel tests ---

func (s *ServerSuite) TestEnsureChannelSuccess() {
	s.channels.On("EnsureChannel", mock.Anything, "/home/user/dev/loop", "").
		Return("ch-123", nil)

	rec := s.testRequest("POST", "/api/channels", `{"dir_path":"/home/user/dev/loop"}`)

	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp ensureChannelResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(s.T(), "ch-123", resp.ChannelID)
	s.channels.AssertExpectations(s.T())
}

func (s *ServerSuite) TestEnsureChannelWithPlatform() {
	s.channels.On("EnsureChannel", mock.Anything, "/home/user/dev/loop", "discord").
		Return("ch-discord-1", nil)

	rec := s.testRequest("POST", "/api/channels", `{"dir_path":"/home/user/dev/loop","platform":"discord"}`)

	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp ensureChannelResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(s.T(), "ch-discord-1", resp.ChannelID)
	s.channels.AssertExpectations(s.T())
}

func (s *ServerSuite) TestEnsureChannelMissingDirPath() {
	rec := s.testRequest("POST", "/api/channels", `{"dir_path":""}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestEnsureChannelError() {
	s.channels.On("EnsureChannel", mock.Anything, "/path", "").
		Return("", errors.New("ensure failed"))

	rec := s.testRequest("POST", "/api/channels", `{"dir_path":"/path"}`)

	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	s.channels.AssertExpectations(s.T())
}

// --- EnsureAllChannels tests ---

func (s *ServerSuite) TestEnsureAllChannelsSuccess() {
	s.channels.On("EnsureChannelAllPlatforms", mock.Anything, "/home/user/dev/loop").
		Return([]EnsureResult{
			{Platform: "local", ChannelID: "ch-local", Created: true},
			{Platform: "discord", ChannelID: "ch-discord", Created: false},
		}, nil)

	rec := s.testRequest("POST", "/api/channels/ensure-all", `{"dir_path":"/home/user/dev/loop"}`)
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp []EnsureResult
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(s.T(), resp, 2)
	s.channels.AssertExpectations(s.T())
}

func (s *ServerSuite) TestEnsureAllChannelsMissingDirPath() {
	rec := s.testRequest("POST", "/api/channels/ensure-all", `{"dir_path":""}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestEnsureAllChannelsBadJSON() {
	rec := s.testRequest("POST", "/api/channels/ensure-all", `not json`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestEnsureAllChannelsError() {
	s.channels.On("EnsureChannelAllPlatforms", mock.Anything, "/path").
		Return(nil, errors.New("ensure failed"))

	rec := s.testRequest("POST", "/api/channels/ensure-all", `{"dir_path":"/path"}`)
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	s.channels.AssertExpectations(s.T())
}

// --- CreateChannel tests ---

func (s *ServerSuite) TestCreateChannelSuccess() {
	s.channels.On("CreateChannel", mock.Anything, "trial", "", "", "").
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
	s.channels.On("CreateChannel", mock.Anything, "trial", "user-42", "", "").
		Return("ch-new", nil)

	rec := s.testRequest("POST", "/api/channels/create", `{"name":"trial","author_id":"user-42"}`)

	require.Equal(s.T(), http.StatusCreated, rec.Code)

	var resp createChannelResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(s.T(), "ch-new", resp.ChannelID)
	s.channels.AssertExpectations(s.T())
}

func (s *ServerSuite) TestCreateChannelWithChannelID() {
	s.channels.On("CreateChannel", mock.Anything, "trial", "", "source-ch", "").
		Return("ch-new", nil)

	rec := s.testRequest("POST", "/api/channels/create", `{"name":"trial","channel_id":"source-ch"}`)

	require.Equal(s.T(), http.StatusCreated, rec.Code)

	var resp createChannelResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(s.T(), "ch-new", resp.ChannelID)
	s.channels.AssertExpectations(s.T())
}

func (s *ServerSuite) TestCreateChannelError() {
	s.channels.On("CreateChannel", mock.Anything, "trial", "", "", "").
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

func (s *ServerSuite) TestCreateThreadLocalAutoTrigger() {
	// Message is NOT passed to CreateThread when msgHandler is set — it goes through HandleThreadCreated instead.
	s.threads.On("CreateThread", mock.Anything, "ch-1", "my-thread", "user-42", "").
		Return("thread-1", nil)

	handler := new(MockIncomingMessageHandler)
	s.srv.SetIncomingMessageHandler(handler)
	defer func() { s.srv.msgHandler = nil }()

	// Set eventsHub so BroadcastChannelCreated is exercised.
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	hub := NewEventsHub(logger)
	s.srv.SetEventsHub(hub)
	defer func() { s.srv.eventsHub = nil }()

	called := make(chan struct{}, 1)
	handler.On("HandleThreadCreated", mock.Anything, "thread-1", "user-42", "Do the task").
		Run(func(_ mock.Arguments) { called <- struct{}{} }).Return()

	rec := s.testRequest("POST", "/api/threads", `{"channel_id":"ch-1","name":"my-thread","author_id":"user-42","message":"Do the task"}`)

	require.Equal(s.T(), http.StatusCreated, rec.Code)

	var resp createThreadResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(s.T(), "thread-1", resp.ThreadID)

	select {
	case <-called:
	case <-time.After(time.Second):
		s.T().Fatal("HandleThreadCreated was not called within 1s")
	}

	handler.AssertExpectations(s.T())
	s.threads.AssertExpectations(s.T())
}

func (s *ServerSuite) TestCreateThreadLocalAutoTriggerDefaultAuthor() {
	s.threads.On("CreateThread", mock.Anything, "ch-1", "my-thread", "", "").
		Return("thread-1", nil)

	handler := new(MockIncomingMessageHandler)
	s.srv.SetIncomingMessageHandler(handler)
	defer func() { s.srv.msgHandler = nil }()

	called := make(chan struct{}, 1)
	handler.On("HandleThreadCreated", mock.Anything, "thread-1", "", "Do the task").
		Run(func(_ mock.Arguments) { called <- struct{}{} }).Return()

	rec := s.testRequest("POST", "/api/threads", `{"channel_id":"ch-1","name":"my-thread","message":"Do the task"}`)

	require.Equal(s.T(), http.StatusCreated, rec.Code)

	select {
	case <-called:
	case <-time.After(time.Second):
		s.T().Fatal("HandleThreadCreated was not called within 1s")
	}

	handler.AssertExpectations(s.T())
}

func (s *ServerSuite) TestCreateThreadLocalNoTriggerWithoutMessage() {
	s.threads.On("CreateThread", mock.Anything, "ch-1", "my-thread", "", "").
		Return("thread-1", nil)

	handler := new(MockIncomingMessageHandler)
	s.srv.SetIncomingMessageHandler(handler)
	defer func() { s.srv.msgHandler = nil }()

	rec := s.testRequest("POST", "/api/threads", `{"channel_id":"ch-1","name":"my-thread"}`)

	require.Equal(s.T(), http.StatusCreated, rec.Code)

	// No message means no auto-trigger.
	handler.AssertNotCalled(s.T(), "HandleThreadCreated", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
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

func (s *ServerSuite) TestCreateThreadWithSessionID() {
	s.threads.On("CreateThread", mock.Anything, "ch-1", "my-thread", "", "").
		Return("thread-1", nil)
	s.store.On("UpdateSessionID", mock.Anything, "thread-1", "imported-session-42").Return(nil)
	// importSessionMessages looks up parent channel — return nil to skip import in this test.
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(nil, nil).Maybe()

	rec := s.testRequest("POST", "/api/threads", `{"channel_id":"ch-1","name":"my-thread","session_id":"imported-session-42"}`)

	require.Equal(s.T(), http.StatusCreated, rec.Code)

	var resp createThreadResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(s.T(), "thread-1", resp.ThreadID)
	s.threads.AssertExpectations(s.T())
	s.store.AssertCalled(s.T(), "UpdateSessionID", mock.Anything, "thread-1", "imported-session-42")
}

func (s *ServerSuite) TestCreateThreadWithSessionIDNoStore() {
	threads := new(MockThreadEnsurer)
	threads.On("CreateThread", mock.Anything, "ch-1", "my-thread", "", "").
		Return("thread-1", nil)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := NewServer(nil, nil, threads, nil, nil, logger)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/threads", srv.handleCreateThread)

	req := httptest.NewRequest("POST", "/api/threads", bytes.NewBufferString(`{"channel_id":"ch-1","name":"my-thread","session_id":"sess-1"}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	// session_id is silently ignored when store is nil.
	require.Equal(s.T(), http.StatusCreated, rec.Code)
	threads.AssertExpectations(s.T())
}

func (s *ServerSuite) TestCreateThreadWithEmptySessionID() {
	s.threads.On("CreateThread", mock.Anything, "ch-1", "my-thread", "", "").
		Return("thread-1", nil)

	rec := s.testRequest("POST", "/api/threads", `{"channel_id":"ch-1","name":"my-thread","session_id":""}`)

	require.Equal(s.T(), http.StatusCreated, rec.Code)

	var resp createThreadResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(s.T(), "thread-1", resp.ThreadID)
	s.threads.AssertExpectations(s.T())
	// UpdateSessionID should NOT be called when session_id is empty.
	s.store.AssertNotCalled(s.T(), "UpdateSessionID", mock.Anything, mock.Anything, mock.Anything)
}

func (s *ServerSuite) TestCreateThreadWithSessionIDUpdateError() {
	s.threads.On("CreateThread", mock.Anything, "ch-1", "test", "desktop", "").Return("thread-1", nil)
	s.store.On("UpdateSessionID", mock.Anything, "thread-1", "bad-session").Return(errors.New("db error"))

	body := `{"channel_id":"ch-1","name":"test","author_id":"desktop","session_id":"bad-session"}`
	req := httptest.NewRequest("POST", "/api/threads", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusInternalServerError, w.Code)
}

func (s *ServerSuite) TestCreateThreadWithTraversalSessionID() {
	s.threads.On("CreateThread", mock.Anything, "ch-1", "test", "", "").
		Return("thread-1", nil)
	// filepath.Base strips traversal: "../../etc/passwd" → "passwd".
	s.store.On("UpdateSessionID", mock.Anything, "thread-1", "passwd").Return(nil)
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(nil, nil).Maybe()

	body := `{"channel_id":"ch-1","name":"test","session_id":"../../etc/passwd"}`
	rec := s.testRequest("POST", "/api/threads", body)

	require.Equal(s.T(), http.StatusCreated, rec.Code)
	// Stored value must be the sanitised base name, not the original traversal path.
	s.store.AssertCalled(s.T(), "UpdateSessionID", mock.Anything, "thread-1", "passwd")
}

// --- importSessionMessages tests ---

func (s *ServerSuite) TestImportSessionMessagesTraversalSessionID() {
	store := new(MockChannelLister)
	// filepath.Base strips traversal — "../../etc/passwd" → "passwd".
	// GetChannel is called with the sanitised value; parent lookup returns nil to stop the flow.
	store.On("GetChannel", mock.Anything, "ch-parent").Return(nil, nil)

	srv := NewServer(nil, nil, nil, store, nil, testLogger())
	srv.sys = s.sys
	srv.importSessionMessages(context.Background(), "ch-parent", "thread-1", "../../etc/passwd")
	store.AssertExpectations(s.T())
}

func (s *ServerSuite) TestImportSessionMessagesDotDotSessionID() {
	store := new(MockChannelLister)
	// A bare ".." sessionID should be rejected entirely — no store calls.
	srv := NewServer(nil, nil, nil, store, nil, testLogger())
	srv.sys = s.sys
	srv.importSessionMessages(context.Background(), "ch-parent", "thread-1", "..")
	store.AssertNotCalled(s.T(), "GetChannel")
}

func (s *ServerSuite) TestImportSessionMessagesNilStoreOrSys() {
	// When store is nil, importSessionMessages returns early.
	srv := NewServer(nil, nil, nil, nil, nil, testLogger())
	srv.importSessionMessages(context.Background(), "ch-1", "thread-1", "sess-1")
	// No panic = success.

	// When sys is nil, importSessionMessages returns early.
	store := new(MockChannelLister)
	srv2 := NewServer(nil, nil, nil, store, nil, testLogger())
	srv2.sys = nil
	srv2.importSessionMessages(context.Background(), "ch-1", "thread-1", "sess-1")
	// store.GetChannel should not be called when sys is nil.
	store.AssertNotCalled(s.T(), "GetChannel")
}

func (s *ServerSuite) TestImportSessionMessagesParentChannelNotFound() {
	store := new(MockChannelLister)
	store.On("GetChannel", mock.Anything, "ch-parent").Return(nil, nil)

	srv := NewServer(nil, nil, nil, store, nil, testLogger())
	srv.sys = s.sys
	srv.importSessionMessages(context.Background(), "ch-parent", "thread-1", "sess-1")
	store.AssertExpectations(s.T())
}

func (s *ServerSuite) TestImportSessionMessagesParentEmptyDirPath() {
	store := new(MockChannelLister)
	store.On("GetChannel", mock.Anything, "ch-parent").Return(&db.Channel{ChannelID: "ch-parent", DirPath: ""}, nil)

	srv := NewServer(nil, nil, nil, store, nil, testLogger())
	srv.sys = s.sys
	srv.importSessionMessages(context.Background(), "ch-parent", "thread-1", "sess-1")
	store.AssertExpectations(s.T())
}

func (s *ServerSuite) TestImportSessionMessagesParentGetError() {
	store := new(MockChannelLister)
	store.On("GetChannel", mock.Anything, "ch-parent").Return(nil, errors.New("db err"))

	srv := NewServer(nil, nil, nil, store, nil, testLogger())
	srv.sys = s.sys
	srv.importSessionMessages(context.Background(), "ch-parent", "thread-1", "sess-1")
	store.AssertExpectations(s.T())
}

func (s *ServerSuite) TestImportSessionMessagesThreadNotFound() {
	store := new(MockChannelLister)
	store.On("GetChannel", mock.Anything, "ch-parent").Return(&db.Channel{ChannelID: "ch-parent", DirPath: "/Users/test/dev/proj"}, nil)
	store.On("GetChannel", mock.Anything, "thread-1").Return(nil, nil)

	srv := NewServer(nil, nil, nil, store, nil, testLogger())
	srv.sys = s.sys
	srv.importSessionMessages(context.Background(), "ch-parent", "thread-1", "sess-1")
	store.AssertExpectations(s.T())
}

func (s *ServerSuite) TestImportSessionMessagesThreadGetError() {
	store := new(MockChannelLister)
	store.On("GetChannel", mock.Anything, "ch-parent").Return(&db.Channel{ChannelID: "ch-parent", DirPath: "/Users/test/dev/proj"}, nil)
	store.On("GetChannel", mock.Anything, "thread-1").Return(nil, errors.New("db err"))

	srv := NewServer(nil, nil, nil, store, nil, testLogger())
	srv.sys = s.sys
	srv.importSessionMessages(context.Background(), "ch-parent", "thread-1", "sess-1")
	store.AssertExpectations(s.T())
}

func (s *ServerSuite) TestImportSessionMessagesUserHomeDirError() {
	store := new(MockChannelLister)
	store.On("GetChannel", mock.Anything, "ch-parent").Return(&db.Channel{ChannelID: "ch-parent", DirPath: "/Users/test/dev/proj"}, nil)
	store.On("GetChannel", mock.Anything, "thread-1").Return(&db.Channel{ChannelID: "thread-1", ID: 42}, nil)

	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("", errors.New("no home"))

	srv := NewServer(nil, nil, nil, store, nil, testLogger())
	srv.sys = sys
	srv.importSessionMessages(context.Background(), "ch-parent", "thread-1", "sess-1")
	store.AssertExpectations(s.T())
}

func (s *ServerSuite) TestImportSessionMessagesFileNotFound() {
	store := new(MockChannelLister)
	store.On("GetChannel", mock.Anything, "ch-parent").Return(&db.Channel{ChannelID: "ch-parent", DirPath: "/Users/test/dev/proj"}, nil)
	store.On("GetChannel", mock.Anything, "thread-1").Return(&db.Channel{ChannelID: "thread-1", ID: 42}, nil)

	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("/home/testuser", nil)
	sys.On("Open", mock.Anything).Return(nil, os.ErrNotExist)

	srv := NewServer(nil, nil, nil, store, nil, testLogger())
	srv.sys = sys
	srv.importSessionMessages(context.Background(), "ch-parent", "thread-1", "sess-1")
	store.AssertExpectations(s.T())
}

func (s *ServerSuite) TestImportSessionMessagesSuccess() {
	tmpDir := s.T().TempDir()
	encodedPath := "-Users-test-dev-proj" // EncodeClaudeProjectPath for /Users/test/dev/proj
	projectDir := filepath.Join(tmpDir, ".claude", "projects", encodedPath)
	require.NoError(s.T(), os.MkdirAll(projectDir, 0755))

	// Build a JSONL file with various line types.
	lines := []string{
		`{"type":"system","subtype":"init"}`,
		`{"type":"user","message":{"role":"user","content":"Hello, world!"}}`,
		`{"type":"assistant","timestamp":"2025-06-15T10:30:00Z","message":{"content":[{"type":"text","text":"Hi there!"}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}}`,
		`not valid json`,
		`{"type":"result","subtype":"success"}`,
		`{"type":"user","message":{"role":"user","content":"Second question"}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":""}]}}`,
		``,
	}
	jsonlPath := filepath.Join(projectDir, "sess-import.jsonl")
	require.NoError(s.T(), os.WriteFile(jsonlPath, []byte(strings.Join(lines, "\n")), 0644))

	store := new(MockChannelLister)
	store.On("GetChannel", mock.Anything, "ch-parent").Return(&db.Channel{ChannelID: "ch-parent", DirPath: "/Users/test/dev/proj"}, nil)
	store.On("GetChannel", mock.Anything, "thread-1").Return(&db.Channel{ChannelID: "thread-1", ID: 42}, nil)
	store.On("InsertMessage", mock.Anything, mock.MatchedBy(func(msg *db.Message) bool {
		return msg.ChatID == 42 && msg.ChannelID == "thread-1"
	})).Return(nil)

	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return(tmpDir, nil)

	srv := NewServer(nil, nil, nil, store, nil, testLogger())
	srv.sys = &realOpenSys{sys}

	srv.importSessionMessages(context.Background(), "ch-parent", "thread-1", "sess-import")

	// Expected messages: "Hello, world!" (user), "Hi there!" (assistant), "Second question" (user).
	// tool_result user line, invalid json, result type, empty assistant text are all skipped.
	store.AssertNumberOfCalls(s.T(), "InsertMessage", 3)

	// Verify message details from the calls.
	calls := store.Calls
	var insertedMsgs []*db.Message
	for _, c := range calls {
		if c.Method == "InsertMessage" {
			insertedMsgs = append(insertedMsgs, c.Arguments.Get(1).(*db.Message))
		}
	}
	require.Len(s.T(), insertedMsgs, 3)

	// First message: user "Hello, world!"
	require.Equal(s.T(), "Hello, world!", insertedMsgs[0].Content)
	require.Equal(s.T(), "user", insertedMsgs[0].AuthorName)
	require.False(s.T(), insertedMsgs[0].IsBot)

	// Second message: assistant "Hi there!" with timestamp
	require.Equal(s.T(), "Hi there!", insertedMsgs[1].Content)
	require.Equal(s.T(), "agent", insertedMsgs[1].AuthorName)
	require.True(s.T(), insertedMsgs[1].IsBot)
	require.Equal(s.T(), time.Date(2025, 6, 15, 10, 30, 0, 0, time.UTC), insertedMsgs[1].CreatedAt)

	// Third message: user "Second question"
	require.Equal(s.T(), "Second question", insertedMsgs[2].Content)
	require.Equal(s.T(), "user", insertedMsgs[2].AuthorName)
	require.False(s.T(), insertedMsgs[2].IsBot)
}

func (s *ServerSuite) TestImportSessionMessagesReadAllError() {
	tmpDir := s.T().TempDir()
	encodedPath := "-Users-test-dev-proj"
	projectDir := filepath.Join(tmpDir, ".claude", "projects", encodedPath)
	require.NoError(s.T(), os.MkdirAll(projectDir, 0755))

	// Create a directory at the JSONL path so Open succeeds but ReadAll fails.
	jsonlDir := filepath.Join(projectDir, "sess-dir.jsonl")
	require.NoError(s.T(), os.MkdirAll(jsonlDir, 0755))

	store := new(MockChannelLister)
	store.On("GetChannel", mock.Anything, "ch-parent").Return(&db.Channel{ChannelID: "ch-parent", DirPath: "/Users/test/dev/proj"}, nil)
	store.On("GetChannel", mock.Anything, "thread-1").Return(&db.Channel{ChannelID: "thread-1", ID: 10}, nil)

	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return(tmpDir, nil)

	srv := NewServer(nil, nil, nil, store, nil, testLogger())
	srv.sys = &realOpenSys{sys}

	// Should return early because ReadAll fails on a directory fd.
	srv.importSessionMessages(context.Background(), "ch-parent", "thread-1", "sess-dir")
	store.AssertNotCalled(s.T(), "InsertMessage")
}

func (s *ServerSuite) TestImportSessionMessagesInsertError() {
	tmpDir := s.T().TempDir()
	encodedPath := "-Users-test-dev-proj"
	projectDir := filepath.Join(tmpDir, ".claude", "projects", encodedPath)
	require.NoError(s.T(), os.MkdirAll(projectDir, 0755))

	lines := []string{
		`{"type":"user","message":{"role":"user","content":"First"}}`,
		`{"type":"user","message":{"role":"user","content":"Second"}}`,
	}
	jsonlPath := filepath.Join(projectDir, "sess-fail.jsonl")
	require.NoError(s.T(), os.WriteFile(jsonlPath, []byte(strings.Join(lines, "\n")), 0644))

	store := new(MockChannelLister)
	store.On("GetChannel", mock.Anything, "ch-parent").Return(&db.Channel{ChannelID: "ch-parent", DirPath: "/Users/test/dev/proj"}, nil)
	store.On("GetChannel", mock.Anything, "thread-1").Return(&db.Channel{ChannelID: "thread-1", ID: 10}, nil)
	// First insert succeeds, second fails — should abort.
	store.On("InsertMessage", mock.Anything, mock.MatchedBy(func(msg *db.Message) bool {
		return msg.Content == "First"
	})).Return(nil).Once()
	store.On("InsertMessage", mock.Anything, mock.MatchedBy(func(msg *db.Message) bool {
		return msg.Content == "Second"
	})).Return(errors.New("insert failed")).Once()

	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return(tmpDir, nil)

	srv := NewServer(nil, nil, nil, store, nil, testLogger())
	srv.sys = &realOpenSys{sys}

	srv.importSessionMessages(context.Background(), "ch-parent", "thread-1", "sess-fail")
	// "Second" insert fails — only 2 InsertMessage calls total.
	store.AssertNumberOfCalls(s.T(), "InsertMessage", 2)
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

// --- DeleteChannel tests ---

func (s *ServerSuite) TestDeleteChannelNotConfigured() {
	srv := NewServer(nil, nil, nil, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	mux := http.NewServeMux()
	mux.HandleFunc("DELETE /api/channels/{id}", srv.handleDeleteChannel)

	req := httptest.NewRequest(http.MethodDelete, "/api/channels/ch-1", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusNotImplemented, w.Code)
}

func (s *ServerSuite) TestDeleteChannelSuccess() {
	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", Name: "test"}, nil)
	s.store.On("DeleteChannelsByParentID", mock.Anything, "ch-1").Return(nil)
	s.store.On("DeleteChannel", mock.Anything, "ch-1").Return(nil)

	req := httptest.NewRequest(http.MethodDelete, "/api/channels/ch-1", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusNoContent, w.Code)
}

func (s *ServerSuite) TestDeleteChannelNotFound() {
	s.store.On("GetChannel", mock.Anything, "missing").
		Return((*db.Channel)(nil), nil)

	req := httptest.NewRequest(http.MethodDelete, "/api/channels/missing", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusNotFound, w.Code)
}

func (s *ServerSuite) TestDeleteChannelGetError() {
	s.store.On("GetChannel", mock.Anything, "ch-err").
		Return((*db.Channel)(nil), errors.New("db error"))

	req := httptest.NewRequest(http.MethodDelete, "/api/channels/ch-err", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusInternalServerError, w.Code)
}

func (s *ServerSuite) TestDeleteChannelChildrenError() {
	s.store.On("GetChannel", mock.Anything, "ch-err").
		Return(&db.Channel{ChannelID: "ch-err"}, nil)
	s.store.On("DeleteChannelsByParentID", mock.Anything, "ch-err").
		Return(errors.New("db error"))

	req := httptest.NewRequest(http.MethodDelete, "/api/channels/ch-err", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusInternalServerError, w.Code)
}

func (s *ServerSuite) TestDeleteChannelDeleteError() {
	s.store.On("GetChannel", mock.Anything, "ch-err").
		Return(&db.Channel{ChannelID: "ch-err"}, nil)
	s.store.On("DeleteChannelsByParentID", mock.Anything, "ch-err").Return(nil)
	s.store.On("DeleteChannel", mock.Anything, "ch-err").
		Return(errors.New("db error"))

	req := httptest.NewRequest(http.MethodDelete, "/api/channels/ch-err", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusInternalServerError, w.Code)
}

// --- SearchChannels tests ---

func (s *ServerSuite) TestSearchChannelsSuccess() {
	channels := []*db.Channel{
		{ChannelID: "ch-1", Name: "general", DirPath: "/home/user/general", Active: true, Platform: types.PlatformLocal},
		{ChannelID: "ch-2", Name: "random", DirPath: "/home/user/random", ParentID: "ch-1", Active: false, Platform: types.PlatformLocal},
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
		{ChannelID: "ch-1", Name: "general", DirPath: "/home/user/general", Active: true, Platform: types.PlatformLocal},
		{ChannelID: "ch-2", Name: "random", DirPath: "/home/user/random", Active: true, Platform: types.PlatformLocal},
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
		{ChannelID: "ch-1", Name: "general", DirPath: "/home/user/general", Active: true, Platform: types.PlatformLocal},
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

func (s *ServerSuite) TestSearchChannelsFiltersByPlatform() {
	channels := []*db.Channel{
		{ChannelID: "ch-1", Name: "local-ch", Platform: types.PlatformLocal, Active: true},
		{ChannelID: "ch-2", Name: "discord-ch", Platform: types.PlatformDiscord, Active: true},
		{ChannelID: "ch-3", Name: "slack-ch", Platform: types.PlatformSlack, Active: true},
	}
	s.store.On("ListChannels", mock.Anything).Return(channels, nil)

	rec := s.testRequest("GET", "/api/channels?platform=local", "")

	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp []channelResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(s.T(), resp, 1)
	require.Equal(s.T(), "local-ch", resp[0].Name)
	s.store.AssertExpectations(s.T())
}

func (s *ServerSuite) TestSearchChannelsRunningFromContainers() {
	lister := new(MockRunningChannelLister)
	s.srv.SetRunningChannelLister(lister)

	channels := []*db.Channel{
		{ChannelID: "ch-1", Name: "running-ch", Platform: types.PlatformLocal, Active: true},
		{ChannelID: "ch-2", Name: "idle-ch", Platform: types.PlatformLocal, Active: true},
	}
	s.store.On("ListChannels", mock.Anything).Return(channels, nil)
	lister.On("RunningChannelIDs", mock.Anything).Return(map[string]struct{}{"ch-1": {}}, nil)

	rec := s.testRequest("GET", "/api/channels", "")

	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp []channelResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(s.T(), resp, 2)
	require.True(s.T(), resp[0].ContainerRunning)
	require.False(s.T(), resp[1].ContainerRunning)
	s.store.AssertExpectations(s.T())
	lister.AssertExpectations(s.T())
}

func (s *ServerSuite) TestSearchChannelsRunningListerError() {
	lister := new(MockRunningChannelLister)
	s.srv.SetRunningChannelLister(lister)

	channels := []*db.Channel{
		{ChannelID: "ch-1", Name: "ch", Platform: types.PlatformLocal, Active: true},
	}
	s.store.On("ListChannels", mock.Anything).Return(channels, nil)
	lister.On("RunningChannelIDs", mock.Anything).Return(nil, errors.New("docker error"))

	rec := s.testRequest("GET", "/api/channels", "")

	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp []channelResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(s.T(), resp, 1)
	require.False(s.T(), resp[0].ContainerRunning)
	s.store.AssertExpectations(s.T())
	lister.AssertExpectations(s.T())
}

func (s *ServerSuite) TestSearchChannelsAgentRunning() {
	chatLister := new(MockActiveChatLister)
	s.srv.SetActiveChatLister(chatLister)

	channels := []*db.Channel{
		{ChannelID: "ch-1", Name: "active-chat", Platform: types.PlatformLocal, Active: true},
		{ChannelID: "ch-2", Name: "idle-chat", Platform: types.PlatformLocal, Active: true},
	}
	s.store.On("ListChannels", mock.Anything).Return(channels, nil)
	chatLister.On("ActiveChatChannelIDs").Return(map[string]struct{}{"ch-1": {}})

	rec := s.testRequest("GET", "/api/channels", "")

	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp []channelResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(s.T(), resp, 2)
	require.True(s.T(), resp[0].AgentRunning)
	require.False(s.T(), resp[1].AgentRunning)
	s.store.AssertExpectations(s.T())
	chatLister.AssertExpectations(s.T())
}

func (s *ServerSuite) TestSearchChannelsError() {
	s.store.On("ListChannels", mock.Anything).Return(nil, errors.New("db error"))

	rec := s.testRequest("GET", "/api/channels", "")

	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	s.store.AssertExpectations(s.T())
}

func (s *ServerSuite) TestSearchChannelsDirPathFallback() {
	s.srv.SetLoopDir("/home/test/.loop")
	channels := []*db.Channel{
		{ChannelID: "ch-1", Name: "no-dir", Active: true, Platform: types.PlatformLocal},
		{ChannelID: "ch-2", Name: "has-dir", DirPath: "/custom/path", Active: true, Platform: types.PlatformLocal},
	}
	s.store.On("ListChannels", mock.Anything).Return(channels, nil)

	rec := s.testRequest("GET", "/api/channels", "")

	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp []channelResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(s.T(), resp, 2)
	require.Equal(s.T(), "/home/test/.loop/ch-1/work", resp[0].DirPath)
	require.Equal(s.T(), "/custom/path", resp[1].DirPath)
	s.srv.SetLoopDir("")
	s.store.AssertExpectations(s.T())
}

func (s *ServerSuite) TestSearchChannelsBranch() {
	// Create a temp git repo so gitBranch returns a real branch name.
	dir := s.T().TempDir()
	for _, args := range [][]string{
		{"git", "init"},
		{"git", "config", "user.email", "t@t.com"},
		{"git", "config", "user.name", "T"},
	} {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		require.NoError(s.T(), cmd.Run())
	}
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x"), 0o644))
	add := exec.Command("git", "add", ".")
	add.Dir = dir
	require.NoError(s.T(), add.Run())
	ci := exec.Command("git", "commit", "-m", "init")
	ci.Dir = dir
	require.NoError(s.T(), ci.Run())

	channels := []*db.Channel{
		{ChannelID: "ch-br", Name: "with-branch", DirPath: dir, Active: true, Platform: types.PlatformLocal},
	}
	s.store.On("ListChannels", mock.Anything).Return(channels, nil)

	rec := s.testRequest("GET", "/api/channels", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp []channelResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(s.T(), resp, 1)
	require.NotEmpty(s.T(), resp[0].Branch)
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

func (s *ServerSuite) TestSendMessageViaHandler() {
	handler := new(MockIncomingMessageHandler)
	s.srv.SetIncomingMessageHandler(handler)

	called := make(chan struct{}, 1)
	handler.On("HandleIncomingMessage", mock.Anything, "ch-1", "", "hello world", "").
		Run(func(_ mock.Arguments) { called <- struct{}{} }).Return()

	rec := s.testRequest("POST", "/api/messages", `{"channel_id":"ch-1","content":"hello world"}`)

	require.Equal(s.T(), http.StatusNoContent, rec.Code)

	// Wait for the goroutine to invoke the handler.
	select {
	case <-called:
	case <-time.After(time.Second):
		s.T().Fatal("HandleIncomingMessage was not called within 1s")
	}

	handler.AssertExpectations(s.T())
	// PostMessage must NOT be called when the handler is set.
	s.messages.AssertNotCalled(s.T(), "PostMessage")
}

func (s *ServerSuite) TestSendMessageViaHandlerPlanMode() {
	handler := new(MockIncomingMessageHandler)
	s.srv.SetIncomingMessageHandler(handler)

	called := make(chan struct{}, 1)
	handler.On("HandleIncomingMessage", mock.Anything, "ch-1", "", "plan this", "plan").
		Run(func(_ mock.Arguments) { called <- struct{}{} }).Return()

	rec := s.testRequest("POST", "/api/messages", `{"channel_id":"ch-1","content":"plan this","mode":"plan"}`)

	require.Equal(s.T(), http.StatusNoContent, rec.Code)

	select {
	case <-called:
	case <-time.After(time.Second):
		s.T().Fatal("HandleIncomingMessage was not called within 1s")
	}

	handler.AssertExpectations(s.T())
}

func (s *ServerSuite) TestSetIncomingMessageHandler() {
	require.Nil(s.T(), s.srv.msgHandler)

	handler := new(MockIncomingMessageHandler)
	s.srv.SetIncomingMessageHandler(handler)

	require.NotNil(s.T(), s.srv.msgHandler)
	require.Equal(s.T(), handler, s.srv.msgHandler)
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

// --- ListMessages tests ---

func (s *ServerSuite) TestListMessagesSuccess() {
	now := time.Now().UTC()
	msgs := []*db.Message{
		{ID: 10, ChannelID: "ch-1", MsgID: "m10", AuthorID: "u1", AuthorName: "alice", Content: "hello", IsBot: false, CreatedAt: now},
		{ID: 9, ChannelID: "ch-1", MsgID: "m9", AuthorID: "bot", AuthorName: "Bot", Content: "hi", IsBot: true, CreatedAt: now},
	}
	s.store.On("GetMessagesCursor", mock.Anything, "ch-1", int64(0), 51).Return(msgs, nil)

	rec := s.testRequest("GET", "/api/channels/ch-1/messages", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp messagesListResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(s.T(), resp.Messages, 2)
	require.Equal(s.T(), int64(10), resp.Messages[0].ID)
	require.Equal(s.T(), "hello", resp.Messages[0].Content)
	require.False(s.T(), resp.Messages[0].IsBot)
	require.True(s.T(), resp.Messages[1].IsBot)
	require.Nil(s.T(), resp.NextCursor)
}

func (s *ServerSuite) TestListMessagesWithCursor() {
	now := time.Now().UTC()
	msgs := []*db.Message{
		{ID: 5, ChannelID: "ch-1", MsgID: "m5", AuthorID: "u1", AuthorName: "alice", Content: "five", CreatedAt: now},
	}
	s.store.On("GetMessagesCursor", mock.Anything, "ch-1", int64(10), 51).Return(msgs, nil)

	rec := s.testRequest("GET", "/api/channels/ch-1/messages?cursor=10", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp messagesListResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(s.T(), resp.Messages, 1)
	require.Nil(s.T(), resp.NextCursor)
}

func (s *ServerSuite) TestListMessagesWithLimit() {
	now := time.Now().UTC()
	// Return limit+1 messages to trigger pagination
	msgs := []*db.Message{
		{ID: 3, ChannelID: "ch-1", MsgID: "m3", AuthorID: "u1", AuthorName: "alice", Content: "three", CreatedAt: now},
		{ID: 2, ChannelID: "ch-1", MsgID: "m2", AuthorID: "u1", AuthorName: "alice", Content: "two", CreatedAt: now},
		{ID: 1, ChannelID: "ch-1", MsgID: "m1", AuthorID: "u1", AuthorName: "alice", Content: "one", CreatedAt: now},
	}
	s.store.On("GetMessagesCursor", mock.Anything, "ch-1", int64(0), 3).Return(msgs, nil)

	rec := s.testRequest("GET", "/api/channels/ch-1/messages?limit=2", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp messagesListResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(s.T(), resp.Messages, 2)
	require.NotNil(s.T(), resp.NextCursor)
	require.Equal(s.T(), int64(2), *resp.NextCursor)
}

func (s *ServerSuite) TestListMessagesInvalidLimit() {
	rec := s.testRequest("GET", "/api/channels/ch-1/messages?limit=abc", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)

	rec = s.testRequest("GET", "/api/channels/ch-1/messages?limit=0", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestListMessagesInvalidCursor() {
	rec := s.testRequest("GET", "/api/channels/ch-1/messages?cursor=abc", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)

	rec = s.testRequest("GET", "/api/channels/ch-1/messages?cursor=0", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestListMessagesLimitCap() {
	s.store.On("GetMessagesCursor", mock.Anything, "ch-1", int64(0), 201).Return([]*db.Message{}, nil)

	rec := s.testRequest("GET", "/api/channels/ch-1/messages?limit=500", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
}

func (s *ServerSuite) TestListMessagesError() {
	s.store.On("GetMessagesCursor", mock.Anything, "ch-1", int64(0), 51).Return(nil, errors.New("db error"))

	rec := s.testRequest("GET", "/api/channels/ch-1/messages", "")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
}

func (s *ServerSuite) TestListMessagesNotConfigured() {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := NewServer(nil, nil, nil, nil, nil, logger)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/channels/{id}/messages", srv.handleListMessages)
	req := httptest.NewRequest("GET", "/api/channels/ch-1/messages", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

// --- ListMessages around tests ---

func (s *ServerSuite) TestListMessagesAroundSuccess() {
	now := time.Now().UTC()
	msgs := []*db.Message{
		{ID: 8, ChannelID: "ch-1", MsgID: "m8", AuthorID: "u1", AuthorName: "alice", Content: "before", CreatedAt: now},
		{ID: 10, ChannelID: "ch-1", MsgID: "m10", AuthorID: "u1", AuthorName: "alice", Content: "target", CreatedAt: now},
		{ID: 12, ChannelID: "ch-1", MsgID: "m12", AuthorID: "bot", AuthorName: "assistant", Content: "after", IsBot: true, CreatedAt: now},
	}
	s.store.On("GetMessagesAround", mock.Anything, "ch-1", int64(10), 50).Return(msgs, nil)

	rec := s.testRequest("GET", "/api/channels/ch-1/messages?around=10", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp messagesListResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(s.T(), resp.Messages, 3)
	require.Equal(s.T(), int64(8), resp.Messages[0].ID)
	require.Equal(s.T(), int64(10), resp.Messages[1].ID)
	require.Equal(s.T(), int64(12), resp.Messages[2].ID)
	require.Nil(s.T(), resp.NextCursor)
}

func (s *ServerSuite) TestListMessagesAroundInvalid() {
	rec := s.testRequest("GET", "/api/channels/ch-1/messages?around=abc", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)

	rec = s.testRequest("GET", "/api/channels/ch-1/messages?around=0", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestListMessagesAroundError() {
	s.store.On("GetMessagesAround", mock.Anything, "ch-1", int64(5), 50).Return(nil, errors.New("db error"))

	rec := s.testRequest("GET", "/api/channels/ch-1/messages?around=5", "")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
}

func (s *ServerSuite) TestListMessagesAroundWithLimit() {
	now := time.Now().UTC()
	msgs := []*db.Message{
		{ID: 10, ChannelID: "ch-1", MsgID: "m10", AuthorID: "u1", AuthorName: "alice", Content: "target", CreatedAt: now},
	}
	s.store.On("GetMessagesAround", mock.Anything, "ch-1", int64(10), 20).Return(msgs, nil)

	rec := s.testRequest("GET", "/api/channels/ch-1/messages?around=10&limit=20", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp messagesListResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(s.T(), resp.Messages, 1)
}

func (s *ServerSuite) TestListMessagesAroundInvalidLimit() {
	rec := s.testRequest("GET", "/api/channels/ch-1/messages?around=10&limit=abc", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)

	rec = s.testRequest("GET", "/api/channels/ch-1/messages?around=10&limit=0", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestListMessagesAroundLimitCapped() {
	now := time.Now().UTC()
	msgs := []*db.Message{
		{ID: 10, ChannelID: "ch-1", MsgID: "m10", AuthorID: "u1", AuthorName: "alice", Content: "target", CreatedAt: now},
	}
	// Limit > maxMessageLimit should be capped to maxMessageLimit (200)
	s.store.On("GetMessagesAround", mock.Anything, "ch-1", int64(10), 200).Return(msgs, nil)

	rec := s.testRequest("GET", "/api/channels/ch-1/messages?around=10&limit=999", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
}

// --- SearchMessages tests ---

func (s *ServerSuite) TestSearchMessagesSuccess() {
	now := time.Now().UTC()
	msgs := []*db.Message{
		{ID: 10, ChannelID: "ch-1", MsgID: "m10", AuthorID: "u1", AuthorName: "alice", Content: "hello world", IsBot: false, CreatedAt: now},
		{ID: 5, ChannelID: "ch-2", MsgID: "m5", AuthorID: "bot", AuthorName: "assistant", Content: "hello there", IsBot: true, CreatedAt: now},
	}
	s.store.On("SearchMessages", mock.Anything, "hello", 20).Return(msgs, nil)

	rec := s.testRequest("GET", "/api/messages/search?q=hello", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var results []searchMessageResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&results))
	require.Len(s.T(), results, 2)
	require.Equal(s.T(), "hello world", results[0].Content)
	require.Equal(s.T(), "ch-1", results[0].ChannelID)
	require.False(s.T(), results[0].IsBot)
	require.Equal(s.T(), "hello there", results[1].Content)
	require.True(s.T(), results[1].IsBot)
}

func (s *ServerSuite) TestSearchMessagesEmptyQuery() {
	rec := s.testRequest("GET", "/api/messages/search?q=", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)

	rec = s.testRequest("GET", "/api/messages/search", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestSearchMessagesWithLimit() {
	s.store.On("SearchMessages", mock.Anything, "test", 5).Return([]*db.Message{}, nil)

	rec := s.testRequest("GET", "/api/messages/search?q=test&limit=5", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
}

func (s *ServerSuite) TestSearchMessagesInvalidLimit() {
	rec := s.testRequest("GET", "/api/messages/search?q=test&limit=abc", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)

	rec = s.testRequest("GET", "/api/messages/search?q=test&limit=0", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestSearchMessagesLimitCap() {
	s.store.On("SearchMessages", mock.Anything, "test", 50).Return([]*db.Message{}, nil)

	rec := s.testRequest("GET", "/api/messages/search?q=test&limit=100", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
}

func (s *ServerSuite) TestSearchMessagesError() {
	s.store.On("SearchMessages", mock.Anything, "fail", 20).Return(nil, errors.New("db error"))

	rec := s.testRequest("GET", "/api/messages/search?q=fail", "")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
}

func (s *ServerSuite) TestSearchMessagesNotConfigured() {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := NewServer(nil, nil, nil, nil, nil, logger)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/messages/search", srv.handleSearchMessages)
	req := httptest.NewRequest("GET", "/api/messages/search?q=hello", nil)
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

// --- writeHTTPJSON tests ---

func (s *ServerSuite) TestWriteJSONSuccess() {
	w := httptest.NewRecorder()
	writeHTTPJSON(w, http.StatusCreated, map[string]string{"id": "123"}, s.srv.logger)
	require.Equal(s.T(), http.StatusCreated, w.Code)
	require.Equal(s.T(), "application/json", w.Header().Get("Content-Type"))
	require.JSONEq(s.T(), `{"id":"123"}`, w.Body.String())
}

func (s *ServerSuite) TestWriteJSONEncodeError() {
	w := httptest.NewRecorder()
	// Channels are not JSON-encodable, so this triggers the error branch.
	writeHTTPJSON(w, http.StatusOK, make(chan int), s.srv.logger)
	require.Equal(s.T(), http.StatusOK, w.Code)
}

func (s *ServerSuite) TestCorsMiddleware() {
	handler := corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Regular GET request
	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusOK, w.Code)
	require.Equal(s.T(), "*", w.Header().Get("Access-Control-Allow-Origin"))
	require.Contains(s.T(), w.Header().Get("Access-Control-Allow-Methods"), "GET")
}

func (s *ServerSuite) TestCorsMiddlewareOptions() {
	handler := corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// OPTIONS preflight
	req := httptest.NewRequest("OPTIONS", "/test", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusNoContent, w.Code)
	require.Equal(s.T(), "*", w.Header().Get("Access-Control-Allow-Origin"))
}

// --- Command handler tests ---

func (s *ServerSuite) TestCommandNotConfigured() {
	rec := s.testRequest("POST", "/api/commands", `{"channel_id":"ch-1","command":"tasks"}`)
	require.Equal(s.T(), http.StatusServiceUnavailable, rec.Code)
}

func (s *ServerSuite) TestCommandInvalidJSON() {
	handler := newMockInteractionHandler()
	s.srv.SetInteractionHandler(handler)

	rec := s.testRequest("POST", "/api/commands", `{invalid}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestCommandMissingChannelID() {
	handler := newMockInteractionHandler()
	s.srv.SetInteractionHandler(handler)

	rec := s.testRequest("POST", "/api/commands", `{"command":"tasks"}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestCommandMissingCommand() {
	handler := newMockInteractionHandler()
	s.srv.SetInteractionHandler(handler)

	rec := s.testRequest("POST", "/api/commands", `{"channel_id":"ch-1"}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestCommandUnknownCommand() {
	handler := newMockInteractionHandler()
	s.srv.SetInteractionHandler(handler)

	rec := s.testRequest("POST", "/api/commands", `{"channel_id":"ch-1","command":"nonexistent"}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestCommandTasksSuccess() {
	handler := newMockInteractionHandler()
	s.srv.SetInteractionHandler(handler)

	handler.On("HandleInteraction", mock.Anything, mock.MatchedBy(func(inter *bot.Interaction) bool {
		return inter.ChannelID == "ch-1" &&
			inter.CommandName == "tasks" &&
			inter.AuthorID == "local-user"
	})).Return()

	rec := s.testRequest("POST", "/api/commands", `{"channel_id":"ch-1","command":"tasks"}`)
	require.Equal(s.T(), http.StatusNoContent, rec.Code)

	select {
	case <-handler.called:
	case <-time.After(time.Second):
		s.T().Fatal("HandleInteraction was not called within 1s")
	}
	handler.AssertExpectations(s.T())
}

func (s *ServerSuite) TestCommandWithAuthorID() {
	handler := newMockInteractionHandler()
	s.srv.SetInteractionHandler(handler)

	handler.On("HandleInteraction", mock.Anything, mock.MatchedBy(func(inter *bot.Interaction) bool {
		return inter.AuthorID == "user-42" && inter.CommandName == "status"
	})).Return()

	rec := s.testRequest("POST", "/api/commands", `{"channel_id":"ch-1","author_id":"user-42","command":"status"}`)
	require.Equal(s.T(), http.StatusNoContent, rec.Code)

	select {
	case <-handler.called:
	case <-time.After(time.Second):
		s.T().Fatal("HandleInteraction was not called within 1s")
	}
	handler.AssertExpectations(s.T())
}

func (s *ServerSuite) TestCommandScheduleWithOptions() {
	handler := newMockInteractionHandler()
	s.srv.SetInteractionHandler(handler)

	handler.On("HandleInteraction", mock.Anything, mock.MatchedBy(func(inter *bot.Interaction) bool {
		return inter.CommandName == "schedule" &&
			inter.Options["type"] == "cron" &&
			inter.Options["schedule"] == "0 9 * * *" &&
			inter.Options["prompt"] == "check status"
	})).Return()

	rec := s.testRequest("POST", "/api/commands",
		`{"channel_id":"ch-1","command":"schedule type=cron schedule=\"0 9 * * *\" prompt=\"check status\""}`)
	require.Equal(s.T(), http.StatusNoContent, rec.Code)

	select {
	case <-handler.called:
	case <-time.After(time.Second):
		s.T().Fatal("HandleInteraction was not called within 1s")
	}
	handler.AssertExpectations(s.T())
}

func (s *ServerSuite) TestCommandCancelWithTaskID() {
	handler := newMockInteractionHandler()
	s.srv.SetInteractionHandler(handler)

	handler.On("HandleInteraction", mock.Anything, mock.MatchedBy(func(inter *bot.Interaction) bool {
		return inter.CommandName == "cancel" && inter.Options["task_id"] == "5"
	})).Return()

	rec := s.testRequest("POST", "/api/commands", `{"channel_id":"ch-1","command":"cancel 5"}`)
	require.Equal(s.T(), http.StatusNoContent, rec.Code)

	select {
	case <-handler.called:
	case <-time.After(time.Second):
		s.T().Fatal("HandleInteraction was not called within 1s")
	}
	handler.AssertExpectations(s.T())
}

func (s *ServerSuite) TestSetInteractionHandler() {
	require.Nil(s.T(), s.srv.interactionHandler)

	handler := newMockInteractionHandler()
	s.srv.SetInteractionHandler(handler)

	require.NotNil(s.T(), s.srv.interactionHandler)
	require.Equal(s.T(), handler, s.srv.interactionHandler)
}

// --- SetAgentRegistry ---

func (s *ServerSuite) TestAgentSetAgentRegistry() {
	old := s.srv.agentRegistry
	defer func() { s.srv.agentRegistry = old }()
	s.srv.agentRegistry = nil
	require.Nil(s.T(), s.srv.agentRegistry)
	reg := agentregistry.New()
	s.srv.SetAgentRegistry(reg)
	require.NotNil(s.T(), s.srv.agentRegistry)
}

// --- handleListAgents ---

func (s *ServerSuite) TestAgentListAgentsSuccess() {
	reg := agentregistry.New()
	s.srv.SetAgentRegistry(reg)
	defer func() { s.srv.agentRegistry = nil }()

	reg.Register(&agentregistry.AgentInfo{AgentID: "a-0", ChannelID: "ch-1", Name: "Alpha"})
	reg.Register(&agentregistry.AgentInfo{AgentID: "a-1", ChannelID: "ch-1", Name: "Beta"})

	req := httptest.NewRequest("GET", "/api/agents?channel_id=ch-1", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusOK, w.Code)
	var agents []*agentregistry.AgentInfo
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &agents))
	require.Len(s.T(), agents, 2)
}

func (s *ServerSuite) TestAgentListAgentsEmpty() {
	reg := agentregistry.New()
	s.srv.SetAgentRegistry(reg)
	defer func() { s.srv.agentRegistry = nil }()

	req := httptest.NewRequest("GET", "/api/agents?channel_id=ch-1", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusOK, w.Code)
	var agents []*agentregistry.AgentInfo
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &agents))
	require.Empty(s.T(), agents)
}

func (s *ServerSuite) TestAgentListAgentsMissingChannelID() {
	reg := agentregistry.New()
	s.srv.SetAgentRegistry(reg)
	defer func() { s.srv.agentRegistry = nil }()

	req := httptest.NewRequest("GET", "/api/agents", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	require.Equal(s.T(), http.StatusBadRequest, w.Code)
}

func (s *ServerSuite) TestAgentListAgentsNotConfigured() {
	req := httptest.NewRequest("GET", "/api/agents?channel_id=ch-1", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	require.Equal(s.T(), http.StatusServiceUnavailable, w.Code)
}

// --- handleUpdateAgent ---

func (s *ServerSuite) TestAgentUpdateAgentSuccess() {
	reg := agentregistry.New()
	s.srv.SetAgentRegistry(reg)
	defer func() { s.srv.agentRegistry = nil }()

	reg.Register(&agentregistry.AgentInfo{AgentID: "a-0", ChannelID: "ch-1", Status: "idle"})
	s.srv.SetEventsHub(NewEventsHub(slog.Default()))
	defer func() { s.srv.eventsHub = nil }()

	body := `{"channel_id":"ch-1","status":"running","work_summary":"indexing","name":"Worker"}`
	req := httptest.NewRequest("PATCH", "/api/agents/a-0", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusOK, w.Code)
	var updated agentregistry.AgentInfo
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &updated))
	require.Equal(s.T(), "running", updated.Status)
	require.Equal(s.T(), "indexing", updated.WorkSummary)
	require.Equal(s.T(), "Worker", updated.Name)
}

func (s *ServerSuite) TestAgentUpdateAgentNotFound() {
	reg := agentregistry.New()
	s.srv.SetAgentRegistry(reg)
	defer func() { s.srv.agentRegistry = nil }()

	body := `{"channel_id":"ch-1","status":"running"}`
	req := httptest.NewRequest("PATCH", "/api/agents/nope", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	require.Equal(s.T(), http.StatusNotFound, w.Code)
}

func (s *ServerSuite) TestAgentUpdateAgentMissingChannelID() {
	reg := agentregistry.New()
	s.srv.SetAgentRegistry(reg)
	defer func() { s.srv.agentRegistry = nil }()

	body := `{"status":"running"}`
	req := httptest.NewRequest("PATCH", "/api/agents/a-0", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	require.Equal(s.T(), http.StatusBadRequest, w.Code)
}

func (s *ServerSuite) TestAgentUpdateAgentInvalidJSON() {
	reg := agentregistry.New()
	s.srv.SetAgentRegistry(reg)
	defer func() { s.srv.agentRegistry = nil }()

	req := httptest.NewRequest("PATCH", "/api/agents/a-0", strings.NewReader("{bad"))
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	require.Equal(s.T(), http.StatusBadRequest, w.Code)
}

func (s *ServerSuite) TestAgentUpdateAgentNotConfigured() {
	body := `{"channel_id":"ch-1","status":"running"}`
	req := httptest.NewRequest("PATCH", "/api/agents/a-0", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	require.Equal(s.T(), http.StatusServiceUnavailable, w.Code)
}

// --- handleSendAgentMessage ---

func (s *ServerSuite) TestAgentSendMessageSuccess() {
	reg := agentregistry.New()
	s.srv.SetAgentRegistry(reg)
	defer func() { s.srv.agentRegistry = nil }()

	reg.Register(&agentregistry.AgentInfo{AgentID: "a-0", ChannelID: "ch-1"})
	reg.Register(&agentregistry.AgentInfo{AgentID: "a-1", ChannelID: "ch-1"})

	body := `{"channel_id":"ch-1","from_agent_id":"a-0","content":"hello"}`
	req := httptest.NewRequest("POST", "/api/agents/a-1/message", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusNoContent, w.Code)
}

func (s *ServerSuite) TestAgentSendMessageTargetNotFound() {
	reg := agentregistry.New()
	s.srv.SetAgentRegistry(reg)
	defer func() { s.srv.agentRegistry = nil }()

	body := `{"channel_id":"ch-1","from_agent_id":"a-0","content":"hello"}`
	req := httptest.NewRequest("POST", "/api/agents/nope/message", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	require.Equal(s.T(), http.StatusNotFound, w.Code)
}

func (s *ServerSuite) TestAgentSendMessageMissingFields() {
	reg := agentregistry.New()
	s.srv.SetAgentRegistry(reg)
	defer func() { s.srv.agentRegistry = nil }()

	body := `{"channel_id":"ch-1"}`
	req := httptest.NewRequest("POST", "/api/agents/a-1/message", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	require.Equal(s.T(), http.StatusBadRequest, w.Code)
}

func (s *ServerSuite) TestAgentSendMessageInvalidJSON() {
	reg := agentregistry.New()
	s.srv.SetAgentRegistry(reg)
	defer func() { s.srv.agentRegistry = nil }()

	req := httptest.NewRequest("POST", "/api/agents/a-1/message", strings.NewReader("{bad"))
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	require.Equal(s.T(), http.StatusBadRequest, w.Code)
}

func (s *ServerSuite) TestAgentSendMessageNotConfigured() {
	body := `{"channel_id":"ch-1","from_agent_id":"a-0","content":"hello"}`
	req := httptest.NewRequest("POST", "/api/agents/a-1/message", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	require.Equal(s.T(), http.StatusServiceUnavailable, w.Code)
}

// --- handleAgentChannelWS ---

func (s *ServerSuite) TestAgentChannelWSSuccess() {
	reg := agentregistry.New()
	s.srv.SetAgentRegistry(reg)
	defer func() { s.srv.agentRegistry = nil }()

	reg.Register(&agentregistry.AgentInfo{AgentID: "a-0", ChannelID: "ch-1"})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/ws/agent-channel", s.srv.handleAgentChannelWS)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/ws/agent-channel?agent_id=a-0&channel_id=ch-1"
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(s.T(), err)
	defer ws.Close()

	// Send a message to the agent.
	require.NoError(s.T(), reg.SendMessage("ch-1", "a-1", "a-0", "hello"))

	// Read the message from WebSocket.
	var msg agentregistry.AgentMessage
	require.NoError(s.T(), ws.ReadJSON(&msg))
	require.Equal(s.T(), "a-1", msg.FromAgentID)
	require.Equal(s.T(), "hello", msg.Content)
}

func (s *ServerSuite) TestAgentChannelWSClosesOnUnregister() {
	reg := agentregistry.New()
	s.srv.SetAgentRegistry(reg)
	defer func() { s.srv.agentRegistry = nil }()

	reg.Register(&agentregistry.AgentInfo{AgentID: "a-0", ChannelID: "ch-1"})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/ws/agent-channel", s.srv.handleAgentChannelWS)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/ws/agent-channel?agent_id=a-0&channel_id=ch-1"
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(s.T(), err)
	defer ws.Close()

	// Unregister the agent — WebSocket should close.
	reg.Unregister("ch-1", "a-0")

	// Reading should return an error (connection closed).
	_, _, err = ws.ReadMessage()
	require.Error(s.T(), err)
}

func (s *ServerSuite) TestAgentChannelWSMissingParams() {
	reg := agentregistry.New()
	s.srv.SetAgentRegistry(reg)
	defer func() { s.srv.agentRegistry = nil }()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/ws/agent-channel", s.srv.handleAgentChannelWS)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/ws/agent-channel")
	require.NoError(s.T(), err)
	defer resp.Body.Close()
	require.Equal(s.T(), http.StatusBadRequest, resp.StatusCode)
}

func (s *ServerSuite) TestAgentChannelWSAgentNotFound() {
	reg := agentregistry.New()
	s.srv.SetAgentRegistry(reg)
	defer func() { s.srv.agentRegistry = nil }()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/ws/agent-channel", s.srv.handleAgentChannelWS)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/ws/agent-channel?agent_id=nope&channel_id=ch-1")
	require.NoError(s.T(), err)
	defer resp.Body.Close()
	require.Equal(s.T(), http.StatusNotFound, resp.StatusCode)
}

func (s *ServerSuite) TestAgentChannelWSUpgradeFail() {
	reg := agentregistry.New()
	s.srv.SetAgentRegistry(reg)
	defer func() { s.srv.agentRegistry = nil }()

	// Agent exists but request is a regular HTTP GET (not WS upgrade).
	reg.Register(&agentregistry.AgentInfo{AgentID: "a-0", ChannelID: "ch-1"})
	s.srv.logger = slog.Default()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/ws/agent-channel", s.srv.handleAgentChannelWS)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/ws/agent-channel?agent_id=a-0&channel_id=ch-1")
	require.NoError(s.T(), err)
	defer resp.Body.Close()
	// Upgrade fails — returns 400 (Bad Request from gorilla/websocket).
	require.Equal(s.T(), http.StatusBadRequest, resp.StatusCode)
}

func (s *ServerSuite) TestAgentChannelWSNotConfigured() {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/ws/agent-channel", s.srv.handleAgentChannelWS)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/ws/agent-channel?agent_id=a-0&channel_id=ch-1")
	require.NoError(s.T(), err)
	defer resp.Body.Close()
	require.Equal(s.T(), http.StatusServiceUnavailable, resp.StatusCode)
}

// --- integration: send + receive via WS ---

func (s *ServerSuite) TestAgentChannelWSMultipleMessages() {
	reg := agentregistry.New()
	s.srv.SetAgentRegistry(reg)
	defer func() { s.srv.agentRegistry = nil }()

	reg.Register(&agentregistry.AgentInfo{AgentID: "a-0", ChannelID: "ch-1"})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/ws/agent-channel", s.srv.handleAgentChannelWS)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/ws/agent-channel?agent_id=a-0&channel_id=ch-1"
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(s.T(), err)
	defer ws.Close()

	// Send 3 messages.
	for i := range 3 {
		require.NoError(s.T(), reg.SendMessage("ch-1", "sender", "a-0", strings.Repeat("x", i+1)))
	}

	// Read all 3.
	for i := range 3 {
		require.NoError(s.T(), ws.SetReadDeadline(time.Now().Add(time.Second)))
		var msg agentregistry.AgentMessage
		require.NoError(s.T(), ws.ReadJSON(&msg))
		require.Equal(s.T(), strings.Repeat("x", i+1), msg.Content)
	}
}

func (s *ServerSuite) TestAgentChannelWSWriteError() {
	reg := agentregistry.New()
	s.srv.SetAgentRegistry(reg)
	s.srv.agentWSWriteJSON = func(v any) error { return errors.New("write failed") }

	reg.Register(&agentregistry.AgentInfo{AgentID: "a-0", ChannelID: "ch-1"})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/ws/agent-channel", s.srv.handleAgentChannelWS)
	ts := httptest.NewServer(mux)

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/ws/agent-channel?agent_id=a-0&channel_id=ch-1"
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(s.T(), err)

	// Send a message; the injected writeJSON returns an error, exercising the error branch.
	require.NoError(s.T(), reg.SendMessage("ch-1", "sender", "a-0", "boom"))
	time.Sleep(50 * time.Millisecond)

	// Close WS + server before test cleanup to avoid race on agentWSWriteJSON.
	ws.Close()
	ts.Close()
}

// --- handleDeleteAgent ---

func (s *ServerSuite) TestAgentDeleteAgent() {
	reg := agentregistry.New()
	s.srv.SetAgentRegistry(reg)
	defer func() { s.srv.agentRegistry = nil }()

	reg.Register(&agentregistry.AgentInfo{AgentID: "a-0", ChannelID: "ch-1", Name: "a-0"})
	require.NotNil(s.T(), reg.Get("ch-1", "a-0"))

	req := httptest.NewRequest("DELETE", "/api/agents/a-0?channel_id=ch-1", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusNoContent, w.Code)
	require.Nil(s.T(), reg.Get("ch-1", "a-0"))
}

func (s *ServerSuite) TestAgentDeleteAgentMissingParams() {
	reg := agentregistry.New()
	s.srv.SetAgentRegistry(reg)
	defer func() { s.srv.agentRegistry = nil }()

	req := httptest.NewRequest("DELETE", "/api/agents/a-0", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusBadRequest, w.Code)
}

func (s *ServerSuite) TestAgentDeleteAgentNoRegistry() {
	req := httptest.NewRequest("DELETE", "/api/agents/a-0?channel_id=ch-1", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	require.Equal(s.T(), http.StatusServiceUnavailable, w.Code)
}

func (s *ServerSuite) TestAgentDeleteAgentBroadcastsEvent() {
	reg := agentregistry.New()
	s.srv.SetAgentRegistry(reg)
	defer func() { s.srv.agentRegistry = nil }()

	hub := NewEventsHub(slog.Default())
	s.srv.SetEventsHub(hub)
	defer func() { s.srv.eventsHub = nil }()

	reg.Register(&agentregistry.AgentInfo{AgentID: "a-0", ChannelID: "ch-1", Name: "a-0"})

	req := httptest.NewRequest("DELETE", "/api/agents/a-0?channel_id=ch-1", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusNoContent, w.Code)
	require.Nil(s.T(), reg.Get("ch-1", "a-0"))
}

// --- handleRegisterAgent ---

func (s *ServerSuite) TestAgentRegisterAgent() {
	reg := agentregistry.New()
	s.srv.SetAgentRegistry(reg)
	defer func() { s.srv.agentRegistry = nil }()

	body := `{"channel_id":"ch-1","agent_id":"a-0","name":"a-0","status":"idle"}`
	req := httptest.NewRequest("POST", "/api/agents", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusCreated, w.Code)
	agent := reg.Get("ch-1", "a-0")
	require.NotNil(s.T(), agent)
	require.Equal(s.T(), "idle", agent.Status)
}

func (s *ServerSuite) TestAgentRegisterAgentMissingFields() {
	reg := agentregistry.New()
	s.srv.SetAgentRegistry(reg)
	defer func() { s.srv.agentRegistry = nil }()

	body := `{"channel_id":"ch-1"}`
	req := httptest.NewRequest("POST", "/api/agents", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusBadRequest, w.Code)
}

func (s *ServerSuite) TestAgentRegisterAgentInvalidJSON() {
	reg := agentregistry.New()
	s.srv.SetAgentRegistry(reg)
	defer func() { s.srv.agentRegistry = nil }()

	req := httptest.NewRequest("POST", "/api/agents", strings.NewReader("not json"))
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusBadRequest, w.Code)
}

func (s *ServerSuite) TestAgentRegisterAgentBroadcastsEvent() {
	reg := agentregistry.New()
	s.srv.SetAgentRegistry(reg)
	defer func() { s.srv.agentRegistry = nil }()

	hub := NewEventsHub(slog.Default())
	s.srv.SetEventsHub(hub)
	defer func() { s.srv.eventsHub = nil }()

	body := `{"channel_id":"ch-1","agent_id":"a-0","name":"a-0","status":"idle"}`
	req := httptest.NewRequest("POST", "/api/agents", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusCreated, w.Code)
}

func (s *ServerSuite) TestAgentRegisterAgentNoRegistry() {
	body := `{"channel_id":"ch-1","agent_id":"a-0"}`
	req := httptest.NewRequest("POST", "/api/agents", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusServiceUnavailable, w.Code)
}

// --- ListSessions tests ---

// mockDirEntry implements fs.DirEntry for testing.
type mockDirEntry struct {
	name    string
	isDir   bool
	modTime time.Time
	infoErr error
}

func (m *mockDirEntry) Name() string      { return m.name }
func (m *mockDirEntry) IsDir() bool       { return m.isDir }
func (m *mockDirEntry) Type() fs.FileMode { return 0 }
func (m *mockDirEntry) Info() (fs.FileInfo, error) {
	if m.infoErr != nil {
		return nil, m.infoErr
	}
	return &mockFileInfo{name: m.name, modTime: m.modTime}, nil
}

type mockFileInfo struct {
	name    string
	size    int64
	modTime time.Time
}

func (m *mockFileInfo) Name() string       { return m.name }
func (m *mockFileInfo) Size() int64        { return m.size }
func (m *mockFileInfo) Mode() fs.FileMode  { return 0 }
func (m *mockFileInfo) ModTime() time.Time { return m.modTime }
func (m *mockFileInfo) IsDir() bool        { return false }
func (m *mockFileInfo) Sys() any           { return nil }

// realOpenSys wraps MockSystem but delegates Open to os.Open (for real temp files in tests).
type realOpenSys struct{ *testutil.MockSystem }

func (r *realOpenSys) Open(name string) (*os.File, error) { return os.Open(name) }

func (s *ServerSuite) TestSessionListSuccess() {
	// Create a temp dir to simulate the Claude projects directory.
	tmpDir := s.T().TempDir()
	projectDir := filepath.Join(tmpDir, ".claude", "projects", "-Users-test-dev-myproject")
	require.NoError(s.T(), os.MkdirAll(projectDir, 0755))

	// Create .jsonl files with different mod times.
	t1 := time.Now().Add(-2 * time.Hour)
	t2 := time.Now().Add(-1 * time.Hour)
	t3 := time.Now()

	f1 := filepath.Join(projectDir, "session-aaa.jsonl")
	f2 := filepath.Join(projectDir, "session-bbb.jsonl")
	f3 := filepath.Join(projectDir, "session-ccc.jsonl")

	userLine := `{"type":"user","message":{"role":"user","content":"What is Go?"}}`
	require.NoError(s.T(), os.WriteFile(f1, []byte(userLine+"\n"), 0644))
	assistantLine := `{"type":"assistant","message":{"content":[{"type":"text","text":"Hello from Claude!"}]}}`
	require.NoError(s.T(), os.WriteFile(f2, []byte(assistantLine+"\n"), 0644))
	require.NoError(s.T(), os.WriteFile(f3, []byte("{}"), 0644))

	require.NoError(s.T(), os.Chtimes(f1, t1, t1))
	require.NoError(s.T(), os.Chtimes(f2, t2, t2))
	require.NoError(s.T(), os.Chtimes(f3, t3, t3))

	// Also create a non-.jsonl file and a directory that should be skipped.
	require.NoError(s.T(), os.WriteFile(filepath.Join(projectDir, "notes.txt"), []byte("hi"), 0644))
	require.NoError(s.T(), os.MkdirAll(filepath.Join(projectDir, "subdir"), 0755))

	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{
		ChannelID: "ch-1",
		DirPath:   "/Users/test/dev/myproject",
		SessionID: "session-bbb",
	}, nil)

	// Override sys mocks for this test.
	sys := new(testutil.MockSystem)
	s.srv.sys = sys
	sys.On("UserHomeDir").Return(tmpDir, nil)

	// Use real ReadDir and Open since we have real temp files.
	realEntries, err := os.ReadDir(projectDir)
	require.NoError(s.T(), err)
	sys.On("ReadDir", projectDir).Return(realEntries, nil)
	sys.On("Open", mock.Anything).Return(nil, os.ErrNotExist).Maybe()

	// Wrap the mock so Open delegates to os.Open (for real temp files).
	s.srv.sys = &realOpenSys{sys}

	// Mock ListChannels for imported_session_ids — include a thread with a session.
	s.store.On("ListChannels", mock.Anything).Return([]*db.Channel{
		{ChannelID: "thread-1", ParentID: "ch-1", SessionID: "session-bbb"},
	}, nil).Maybe()

	req := httptest.NewRequest("GET", "/api/channels/ch-1/sessions", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusOK, w.Code)

	var resp listSessionsResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(s.T(), "session-bbb", resp.CurrentSessionID)
	require.Len(s.T(), resp.Sessions, 3)

	// Newest first.
	require.Equal(s.T(), "session-ccc", resp.Sessions[0].SessionID)
	require.Equal(s.T(), "session-bbb", resp.Sessions[1].SessionID)
	require.Equal(s.T(), "session-aaa", resp.Sessions[2].SessionID)

	// Verify last_message extraction.
	require.Equal(s.T(), "Hello from Claude!", resp.Sessions[1].LastMessage) // session-bbb (assistant)
	require.Equal(s.T(), "What is Go?", resp.Sessions[2].LastMessage)        // session-aaa (user prompt)
	require.Empty(s.T(), resp.Sessions[0].LastMessage)                       // session-ccc (empty)

	// Verify imported_session_ids — session-bbb is already a thread.
	require.Equal(s.T(), []string{"session-bbb"}, resp.ImportedSessionIDs)
}

func (s *ServerSuite) TestFindLastMessage() {
	// Assistant text block is last.
	data := `{"type":"system","subtype":"init"}` + "\n" +
		`{"type":"user","message":{"role":"user","content":"Hello"}}` + "\n" +
		`{"type":"assistant","message":{"content":[{"type":"text","text":"Hi there!"}]}}` + "\n"
	require.Equal(s.T(), "Hi there!", findLastMessage([]byte(data)))

	// User prompt is last.
	data = `{"type":"assistant","message":{"content":[{"type":"text","text":"earlier"}]}}` + "\n" +
		`{"type":"user","message":{"role":"user","content":"What next?"}}` + "\n"
	require.Equal(s.T(), "What next?", findLastMessage([]byte(data)))

	// Tool result user line is skipped — falls through to assistant.
	data = `{"type":"assistant","message":{"content":[{"type":"text","text":"check this"}]}}` + "\n" +
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}}` + "\n"
	require.Equal(s.T(), "check this", findLastMessage([]byte(data)))

	// Long text is truncated.
	longText := strings.Repeat("x", 300)
	data = `{"type":"assistant","message":{"content":[{"type":"text","text":"` + longText + `"}]}}` + "\n"
	result := findLastMessage([]byte(data))
	require.Equal(s.T(), maxLastMessageLen+3, len(result))
	require.True(s.T(), strings.HasSuffix(result, "..."))

	// Empty input.
	require.Empty(s.T(), findLastMessage([]byte("")))

	// Only system events.
	require.Empty(s.T(), findLastMessage([]byte(`{"type":"system","subtype":"init"}`+"\n")))

	// Malformed JSON is skipped.
	data = `{"type":"assistant","message":{"content":[{"type":"text","text":"good"}]}}` + "\n" + "not json\n"
	require.Equal(s.T(), "good", findLastMessage([]byte(data)))

	// Invalid content array falls through.
	data = `{"type":"user","message":{"role":"user","content":"fallback"}}` + "\n" +
		`{"type":"assistant","message":{"content":"not an array"}}` + "\n"
	require.Equal(s.T(), "fallback", findLastMessage([]byte(data)))
}

func (s *ServerSuite) TestReadLastMessageTextFileNotFound() {
	require.Empty(s.T(), readLastMessageText(realSys{}, "/nonexistent/path.jsonl"))
}

func (s *ServerSuite) TestFindLastMessageFromReaderStatError() {
	require.Empty(s.T(), findLastMessageFromReader(&failStatReader{}, tailReadSize))
}

func (s *ServerSuite) TestFindLastMessageFromReaderSeekError() {
	// File "larger" than maxBytes triggers Seek.
	require.Empty(s.T(), findLastMessageFromReader(&failSeekReader{size: tailReadSize + 100}, tailReadSize))
}

func (s *ServerSuite) TestFindLastMessageFromReaderReadError() {
	require.Empty(s.T(), findLastMessageFromReader(&failReadReader{size: 10}, tailReadSize))
}

type failStatReader struct{}

func (f *failStatReader) Stat() (os.FileInfo, error)     { return nil, errors.New("stat err") }
func (f *failStatReader) Read([]byte) (int, error)       { return 0, nil }
func (f *failStatReader) Seek(int64, int) (int64, error) { return 0, nil }

type failSeekReader struct{ size int64 }

func (f *failSeekReader) Stat() (os.FileInfo, error)     { return &mockFileInfo{size: f.size}, nil }
func (f *failSeekReader) Read([]byte) (int, error)       { return 0, nil }
func (f *failSeekReader) Seek(int64, int) (int64, error) { return 0, errors.New("seek err") }

type failReadReader struct{ size int64 }

func (f *failReadReader) Stat() (os.FileInfo, error)     { return &mockFileInfo{size: f.size}, nil }
func (f *failReadReader) Read([]byte) (int, error)       { return 0, errors.New("read err") }
func (f *failReadReader) Seek(int64, int) (int64, error) { return 0, nil }

// realSys delegates Open to os.Open for testing readLastMessageText.
type realSys struct{}

func (realSys) Open(name string) (*os.File, error) { return os.Open(name) }

func (s *ServerSuite) TestSessionListEmptyDirPath() {
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{
		ChannelID: "ch-1",
		DirPath:   "",
		SessionID: "sess-1",
	}, nil)

	req := httptest.NewRequest("GET", "/api/channels/ch-1/sessions", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusOK, w.Code)

	var resp listSessionsResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(s.T(), "sess-1", resp.CurrentSessionID)
	require.Empty(s.T(), resp.Sessions)
}

func (s *ServerSuite) TestSessionListChannelNotFound() {
	s.store.On("GetChannel", mock.Anything, "unknown-ch").Return(nil, nil)

	req := httptest.NewRequest("GET", "/api/channels/unknown-ch/sessions", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusNotFound, w.Code)
}

func (s *ServerSuite) TestSessionListGetChannelError() {
	s.store.On("GetChannel", mock.Anything, "err-ch").Return(nil, errors.New("db error"))

	req := httptest.NewRequest("GET", "/api/channels/err-ch/sessions", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusInternalServerError, w.Code)
}

func (s *ServerSuite) TestSessionListNoProjectDir() {
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{
		ChannelID: "ch-1",
		DirPath:   "/Users/test/dev/myproject",
		SessionID: "sess-1",
	}, nil)

	tmpDir := s.T().TempDir()

	// Override sys mocks for this test.
	sys := new(testutil.MockSystem)
	s.srv.sys = sys
	sys.On("UserHomeDir").Return(tmpDir, nil)

	// ReadDir will fail because the directory doesn't exist.
	projectDir := filepath.Join(tmpDir, ".claude", "projects", "-Users-test-dev-myproject")
	sys.On("ReadDir", projectDir).Return(nil, os.ErrNotExist)

	req := httptest.NewRequest("GET", "/api/channels/ch-1/sessions", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusOK, w.Code)

	var resp listSessionsResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(s.T(), "sess-1", resp.CurrentSessionID)
	require.Empty(s.T(), resp.Sessions)
}

func (s *ServerSuite) TestSessionListStoreNotConfigured() {
	oldStore := s.srv.store
	defer func() { s.srv.store = oldStore }()
	s.srv.store = nil

	req := httptest.NewRequest("GET", "/api/channels/ch-1/sessions", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusNotImplemented, w.Code)
}

func (s *ServerSuite) TestSessionListHomeDirError() {
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{
		ChannelID: "ch-1",
		DirPath:   "/Users/test/dev/myproject",
		SessionID: "sess-1",
	}, nil)

	// Override sys mocks for this test.
	sys := new(testutil.MockSystem)
	s.srv.sys = sys
	sys.On("UserHomeDir").Return("", os.ErrNotExist)

	req := httptest.NewRequest("GET", "/api/channels/ch-1/sessions", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusInternalServerError, w.Code)
}

func (s *ServerSuite) TestSessionListEntryInfoError() {
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{
		ChannelID: "ch-1",
		DirPath:   "/Users/test/dev/myproject",
		SessionID: "sess-1",
	}, nil)

	tmpDir := s.T().TempDir()

	// Override sys mocks for this test.
	sys := new(testutil.MockSystem)
	s.srv.sys = sys
	sys.On("UserHomeDir").Return(tmpDir, nil)

	projectDir := filepath.Join(tmpDir, ".claude", "projects", "-Users-test-dev-myproject")
	// Return a mock DirEntry whose Info() returns an error.
	badEntry := &mockDirEntry{name: "bad.jsonl", isDir: false, infoErr: errors.New("stat failed")}
	goodEntry := &mockDirEntry{name: "good.jsonl", isDir: false, modTime: time.Now()}
	sys.On("ReadDir", projectDir).Return([]fs.DirEntry{badEntry, goodEntry}, nil)
	sys.On("Open", mock.Anything).Return(nil, os.ErrNotExist).Maybe()
	s.store.On("ListChannels", mock.Anything).Return([]*db.Channel{}, nil).Maybe()

	req := httptest.NewRequest("GET", "/api/channels/ch-1/sessions", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusOK, w.Code)

	var resp listSessionsResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	// Bad entry skipped, good entry included.
	require.Len(s.T(), resp.Sessions, 1)
	require.Equal(s.T(), "good", resp.Sessions[0].SessionID)
}

func (s *ServerSuite) TestSessionListEmptyDir() {
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{
		ChannelID: "ch-1",
		DirPath:   "/Users/test/dev/myproject",
		SessionID: "",
	}, nil)

	tmpDir := s.T().TempDir()

	// Override sys mocks for this test.
	sys := new(testutil.MockSystem)
	s.srv.sys = sys
	sys.On("UserHomeDir").Return(tmpDir, nil)

	projectDir := filepath.Join(tmpDir, ".claude", "projects", "-Users-test-dev-myproject")
	sys.On("ReadDir", projectDir).Return([]fs.DirEntry{}, nil)
	s.store.On("ListChannels", mock.Anything).Return([]*db.Channel{}, nil).Maybe()

	req := httptest.NewRequest("GET", "/api/channels/ch-1/sessions", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusOK, w.Code)

	var resp listSessionsResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(s.T(), "", resp.CurrentSessionID)
	require.Empty(s.T(), resp.Sessions)
}

// --- handleListRoots tests ---

func (s *ServerSuite) TestListRootsNotConfigured() {
	srv := nilServer()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/channels/{id}/roots", srv.handleListRoots)

	req := httptest.NewRequest("GET", "/api/channels/ch-1/roots", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusNotImplemented, w.Code)
}

func (s *ServerSuite) TestListRootsChannelNotFound() {
	s.store.On("GetChannel", mock.Anything, "missing").
		Return((*db.Channel)(nil), nil)

	rec := s.testRequest("GET", "/api/channels/missing/roots", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

// --- resolveRootDir tests ---

func (s *ServerSuite) TestResolveRootDirDefaultRoot() {
	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: "/home/user/project"}, nil)

	req := httptest.NewRequest("GET", "/api/channels/ch-1/files", nil)
	dirPath, err := s.srv.resolveRootDir(context.Background(), "ch-1", req)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "/home/user/project", dirPath)
}

func (s *ServerSuite) TestResolveRootDirChannelNotFound() {
	s.store.On("GetChannel", mock.Anything, "missing").
		Return((*db.Channel)(nil), nil)

	req := httptest.NewRequest("GET", "/api/channels/missing/files", nil)
	_, err := s.srv.resolveRootDir(context.Background(), "missing", req)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "not found")
}

func (s *ServerSuite) TestResolveRootDirFallbackToLoopDir() {
	s.srv.SetLoopDir("/home/test/.loop")

	s.store.On("GetChannel", mock.Anything, "ch-nodir").
		Return(&db.Channel{ChannelID: "ch-nodir", DirPath: ""}, nil)

	req := httptest.NewRequest("GET", "/api/channels/ch-nodir/files", nil)
	dirPath, err := s.srv.resolveRootDir(context.Background(), "ch-nodir", req)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "/home/test/.loop/ch-nodir/work", dirPath)

	s.srv.SetLoopDir("")
}

// --- allDirPaths tests ---

func (s *ServerSuite) TestAllDirPathsWithExtraDirs() {
	tmpDir := s.T().TempDir()
	loopDir := filepath.Join(tmpDir, ".loop")
	require.NoError(s.T(), os.MkdirAll(loopDir, 0755))
	require.NoError(s.T(), os.WriteFile(
		filepath.Join(loopDir, "config.json"),
		[]byte(`{"extra_dirs": ["/home/user/lib"]}`),
		0644,
	))

	s.store.On("GetChannel", mock.Anything, "ch-multi").
		Return(&db.Channel{ChannelID: "ch-multi", DirPath: tmpDir}, nil)

	paths, err := s.srv.allDirPaths(context.Background(), "ch-multi")
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{tmpDir, "/home/user/lib"}, paths)
}

func (s *ServerSuite) TestAllDirPathsNoExtraDirs() {
	tmpDir := s.T().TempDir()
	// No .loop/config.json — extra dirs should be empty.

	s.store.On("GetChannel", mock.Anything, "ch-noextra").
		Return(&db.Channel{ChannelID: "ch-noextra", DirPath: tmpDir}, nil)

	paths, err := s.srv.allDirPaths(context.Background(), "ch-noextra")
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{tmpDir}, paths)
}

func (s *ServerSuite) TestAllDirPathsChannelError() {
	s.store.On("GetChannel", mock.Anything, "ch-err").
		Return((*db.Channel)(nil), nil)

	_, err := s.srv.allDirPaths(context.Background(), "ch-err")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "not found")
}

// --- resolveRootDir with extra dirs ---

func (s *ServerSuite) TestResolveRootDirWithExtraDir() {
	tmpDir := s.T().TempDir()
	loopDir := filepath.Join(tmpDir, ".loop")
	require.NoError(s.T(), os.MkdirAll(loopDir, 0755))
	require.NoError(s.T(), os.WriteFile(
		filepath.Join(loopDir, "config.json"),
		[]byte(`{"extra_dirs": ["/home/user/lib", "/home/user/common"]}`),
		0644,
	))

	s.store.On("GetChannel", mock.Anything, "ch-extra").
		Return(&db.Channel{ChannelID: "ch-extra", DirPath: tmpDir}, nil)

	req := httptest.NewRequest("GET", "/api/channels/ch-extra/files?root=1", nil)
	dirPath, err := s.srv.resolveRootDir(context.Background(), "ch-extra", req)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "/home/user/lib", dirPath)
}

func (s *ServerSuite) TestResolveRootDirInvalidIndex() {
	tmpDir := s.T().TempDir()
	loopDir := filepath.Join(tmpDir, ".loop")
	require.NoError(s.T(), os.MkdirAll(loopDir, 0755))
	require.NoError(s.T(), os.WriteFile(
		filepath.Join(loopDir, "config.json"),
		[]byte(`{"extra_dirs": ["/home/user/lib"]}`),
		0644,
	))

	s.store.On("GetChannel", mock.Anything, "ch-badidx").
		Return(&db.Channel{ChannelID: "ch-badidx", DirPath: tmpDir}, nil)

	req := httptest.NewRequest("GET", "/api/channels/ch-badidx/files?root=5", nil)
	_, err := s.srv.resolveRootDir(context.Background(), "ch-badidx", req)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "invalid root index")
}

func (s *ServerSuite) TestResolveRootDirAllDirPathsError() {
	s.store.On("GetChannel", mock.Anything, "ch-missing").
		Return((*db.Channel)(nil), nil)

	req := httptest.NewRequest("GET", "/api/channels/ch-missing/files?root=1", nil)
	_, err := s.srv.resolveRootDir(context.Background(), "ch-missing", req)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "not found")
}

func (s *ServerSuite) TestResolveRootDirNegativeIndex() {
	tmpDir := s.T().TempDir()
	loopDir := filepath.Join(tmpDir, ".loop")
	require.NoError(s.T(), os.MkdirAll(loopDir, 0755))
	require.NoError(s.T(), os.WriteFile(
		filepath.Join(loopDir, "config.json"),
		[]byte(`{"extra_dirs": ["/home/user/lib"]}`),
		0644,
	))

	s.store.On("GetChannel", mock.Anything, "ch-neg").
		Return(&db.Channel{ChannelID: "ch-neg", DirPath: tmpDir}, nil)

	req := httptest.NewRequest("GET", "/api/channels/ch-neg/files?root=-1", nil)
	_, err := s.srv.resolveRootDir(context.Background(), "ch-neg", req)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "invalid root index")
}

// --- handleListRoots success ---

func (s *ServerSuite) TestListRootsSuccess() {
	tmpDir := s.T().TempDir()
	loopDir := filepath.Join(tmpDir, ".loop")
	require.NoError(s.T(), os.MkdirAll(loopDir, 0755))
	require.NoError(s.T(), os.WriteFile(
		filepath.Join(loopDir, "config.json"),
		[]byte(`{"extra_dirs": ["/home/user/lib"]}`),
		0644,
	))

	s.store.On("GetChannel", mock.Anything, "ch-roots").
		Return(&db.Channel{ChannelID: "ch-roots", DirPath: tmpDir}, nil)

	rec := s.testRequest("GET", "/api/channels/ch-roots/roots", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp listRootsResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(s.T(), resp.Roots, 2)
	require.Equal(s.T(), 0, resp.Roots[0].Index)
	require.Equal(s.T(), tmpDir, resp.Roots[0].Path)
	require.Equal(s.T(), filepath.Base(tmpDir), resp.Roots[0].Name)
	require.Equal(s.T(), 1, resp.Roots[1].Index)
	require.Equal(s.T(), "/home/user/lib", resp.Roots[1].Path)
	require.Equal(s.T(), "lib", resp.Roots[1].Name)
}

func (s *ServerSuite) TestListRootsSkipsEmptyPaths() {
	tmpDir := s.T().TempDir()
	loopDir := filepath.Join(tmpDir, ".loop")
	require.NoError(s.T(), os.MkdirAll(loopDir, 0755))
	require.NoError(s.T(), os.WriteFile(
		filepath.Join(loopDir, "config.json"),
		[]byte(`{"extra_dirs": ["", "/home/user/lib"]}`),
		0644,
	))

	s.store.On("GetChannel", mock.Anything, "ch-empty").
		Return(&db.Channel{ChannelID: "ch-empty", DirPath: tmpDir}, nil)

	rec := s.testRequest("GET", "/api/channels/ch-empty/roots", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp listRootsResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	// Empty path should be skipped, so only tmpDir and /home/user/lib remain.
	require.Len(s.T(), resp.Roots, 2)
	require.Equal(s.T(), 0, resp.Roots[0].Index)
	require.Equal(s.T(), 2, resp.Roots[1].Index) // index 1 was empty, so index 2 is lib
}
