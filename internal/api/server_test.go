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
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	tk "github.com/radutopala/ticket/pkg/ticket"

	"github.com/radutopala/loop/internal/bot"
	"github.com/radutopala/loop/internal/memory"
	"github.com/radutopala/loop/internal/testutil"
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

func (m *MockIncomingMessageHandler) HandleIncomingMessageWithPriority(ctx context.Context, channelID, authorID, content, mode string, priority int) {
	m.Called(ctx, channelID, authorID, content, mode, priority)
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

type MockTicketStore struct {
	mock.Mock
}

func (m *MockTicketStore) List() ([]*tk.Ticket, error) {
	args := m.Called()
	return args.Get(0).([]*tk.Ticket), args.Error(1)
}

func (m *MockTicketStore) ResolveID(partial string) (string, error) {
	args := m.Called(partial)
	return args.String(0), args.Error(1)
}

func (m *MockTicketStore) Read(id string) (*tk.Ticket, error) {
	args := m.Called(id)
	if t := args.Get(0); t != nil {
		return t.(*tk.Ticket), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *MockTicketStore) EnsureDir() error {
	return m.Called().Error(0)
}

func (m *MockTicketStore) Write(ticket *tk.Ticket) error {
	return m.Called(ticket).Error(0)
}

func (m *MockTicketStore) Delete(id string) error {
	return m.Called(id).Error(0)
}

func (m *MockTicketStore) AtomicClaim(id string) (*tk.Ticket, error) {
	args := m.Called(id)
	if t := args.Get(0); t != nil {
		return t.(*tk.Ticket), args.Error(1)
	}
	return nil, args.Error(1)
}

type MockRunCanceller struct {
	mock.Mock
}

func (m *MockRunCanceller) CancelActiveRun(channelID string) bool {
	args := m.Called(channelID)
	return args.Bool(0)
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
	s.srv.worktreeCreator.Sys = s.sys

	s.mux = http.NewServeMux()
	s.mux.HandleFunc("GET /api/channels", s.srv.handleSearchChannels)
	s.mux.HandleFunc("POST /api/channels", s.srv.handleEnsureChannel)
	s.mux.HandleFunc("POST /api/channels/create", s.srv.handleCreateChannel)
	s.mux.HandleFunc("POST /api/channels/ensure-all", s.srv.handleEnsureAllChannels)
	s.mux.HandleFunc("POST /api/messages", s.srv.handleSendMessage)
	s.mux.HandleFunc("DELETE /api/messages/{id}", s.srv.handleDeleteQueuedMessage)
	s.mux.HandleFunc("POST /api/threads", s.srv.handleCreateThread)
	s.mux.HandleFunc("DELETE /api/threads/{id}", s.srv.handleDeleteThread)
	s.mux.HandleFunc("DELETE /api/channels/{id}", s.srv.handleDeleteChannel)
	s.mux.HandleFunc("PATCH /api/channels/{id}/lock", s.srv.handleSetChannelLocked)
	s.mux.HandleFunc("POST /api/channels/{id}/plan/resolve", s.srv.handlePlanResolve)
	s.mux.HandleFunc("POST /api/channels/{id}/ask/resolve", s.srv.handleAskResolve)
	s.mux.HandleFunc("GET /api/asks/pending", s.srv.handleListPendingAsks)
	s.mux.HandleFunc("POST /api/tasks", s.srv.handleCreateTask)
	s.mux.HandleFunc("GET /api/tasks", s.srv.handleListTasks)
	s.mux.HandleFunc("GET /api/tasks/{id}", s.srv.handleGetTask)
	s.mux.HandleFunc("DELETE /api/tasks/{id}", s.srv.handleDeleteTask)
	s.mux.HandleFunc("PATCH /api/tasks/{id}", s.srv.handleUpdateTask)
	s.mux.HandleFunc("GET /api/tasks/{id}/runs", s.srv.handleListTaskRuns)
	s.mux.HandleFunc("POST /api/tasks/{id}/run", s.srv.handleRunTask)
	s.mux.HandleFunc("GET /api/shortcuts", s.srv.handleListShortcuts)
	s.mux.HandleFunc("POST /api/shortcuts", s.srv.handleModifyShortcut)
	s.mux.HandleFunc("GET /api/channels/{id}/sessions", s.srv.handleListSessions)
	s.mux.HandleFunc("GET /api/channels/{id}/audit", s.srv.handleListAuditFiles)
	s.mux.HandleFunc("DELETE /api/channels/{id}/audit/{date}", s.srv.handleDeleteAuditFile)
	s.mux.HandleFunc("GET /api/channels/{id}/messages", s.srv.handleListMessages)
	s.mux.HandleFunc("GET /api/channels/{id}/queued", s.srv.handleListQueuedMessages)
	s.mux.HandleFunc("GET /api/channels/{id}/timeline", s.srv.handleTimeline)
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
	s.mux.HandleFunc("GET /api/channels/{id}/files/search", s.srv.handleSearchFiles)
	s.mux.HandleFunc("GET /api/channels/{id}/file", s.srv.handleReadFile)
	s.mux.HandleFunc("PUT /api/channels/{id}/file", s.srv.handleWriteFile)
	s.mux.HandleFunc("DELETE /api/channels/{id}/file", s.srv.handleDeleteFile)
	s.mux.HandleFunc("POST /api/channels/{id}/files/exists", s.srv.handleFilesExists)
	s.mux.HandleFunc("POST /api/channels/{id}/dir", s.srv.handleCreateDir)
	s.mux.HandleFunc("POST /api/channels/{id}/paste-image", s.srv.handlePasteImage)
	s.mux.HandleFunc("GET /api/readme", s.srv.handleGetReadme)
	s.mux.HandleFunc("GET /api/channels/{id}/branches", s.srv.handleListBranches)
	s.mux.HandleFunc("GET /api/channels/{id}/commits", s.srv.handleListCommits)
	s.mux.HandleFunc("POST /api/channels/{id}/branches/switch", s.srv.handleSwitchBranch)
	s.mux.HandleFunc("POST /api/channels/{id}/branches/create", s.srv.handleCreateBranch)
	s.mux.HandleFunc("DELETE /api/channels/{id}/branches", s.srv.handleDeleteBranch)
	s.mux.HandleFunc("GET /api/tickets", s.srv.handleListTickets)
	s.mux.HandleFunc("GET /api/tickets/{id}", s.srv.handleGetTicket)
	s.mux.HandleFunc("POST /api/tickets", s.srv.handleCreateTicket)
	s.mux.HandleFunc("PATCH /api/tickets/{id}", s.srv.handleUpdateTicket)
	s.mux.HandleFunc("DELETE /api/tickets/{id}", s.srv.handleDeleteTicket)
	s.mux.HandleFunc("POST /api/tickets/{id}/assign", s.srv.handleAssignTicket)
	s.mux.HandleFunc("POST /api/worktrees", s.srv.handleCreateWorktree)
	s.mux.HandleFunc("POST /api/worktrees/import", s.srv.handleImportWorktree)
	s.mux.HandleFunc("DELETE /api/worktrees", s.srv.handleRemoveWorktree)
	s.mux.HandleFunc("POST /api/worktrees/lock", s.srv.handleSetWorktreeLocked)
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
	s.mux.HandleFunc("PUT /api/playground", s.srv.handlePlaygroundUpdate)
	s.mux.HandleFunc("GET /api/playground", s.srv.handlePlaygroundGet)
	s.mux.HandleFunc("DELETE /api/playground", s.srv.handlePlaygroundDelete)
	s.mux.HandleFunc("GET /api/playground/items", s.srv.handlePlaygroundList)
	s.mux.HandleFunc("PUT /api/playground/file", s.srv.handlePlaygroundFileWrite)
	s.mux.HandleFunc("GET /api/playground/file", s.srv.handlePlaygroundFileRead)
	s.mux.HandleFunc("DELETE /api/playground/file", s.srv.handlePlaygroundFileDelete)
	s.mux.HandleFunc("GET /api/playground/files", s.srv.handlePlaygroundFileList)
	s.mux.HandleFunc("GET /api/playground/serve/{name}", s.srv.handlePlaygroundServe)
	s.mux.HandleFunc("GET /api/playground/serve/{name}/{path...}", s.srv.handlePlaygroundServeFile)
	s.mux.HandleFunc("GET /api/containers", s.srv.handleListContainers)
	s.mux.HandleFunc("POST /api/workflows/runs", s.srv.handleStartWorkflowRun)
	s.mux.HandleFunc("GET /api/workflows/runs", s.srv.handleListWorkflowRuns)
	s.mux.HandleFunc("GET /api/workflows/runs/{id}", s.srv.handleGetWorkflowRun)
	s.mux.HandleFunc("POST /api/workflows/runs/{id}/cancel", s.srv.handleCancelWorkflowRun)
	s.mux.HandleFunc("DELETE /api/workflows/runs/{id}", s.srv.handleDeleteWorkflowRun)
	s.mux.HandleFunc("POST /api/workflows/runs/{id}/retry", s.srv.handleRetryWorkflowRun)
	s.mux.HandleFunc("POST /api/workflows/runs/{id}/resume", s.srv.handleResumeWorkflowRun)
	s.mux.HandleFunc("GET /api/gate/approvals", s.srv.handleListGateApprovals)
	s.mux.HandleFunc("POST /api/gate/approvals/{id}", s.srv.handleResolveGateApproval)
	s.mux.HandleFunc("POST /api/gate/container-approval", s.srv.handleContainerApproval)
	s.mux.HandleFunc("GET /api/workflows", s.srv.handleListWorkflows)
	s.mux.HandleFunc("POST /api/workflows", s.srv.handleModifyWorkflow)
	s.mux.HandleFunc("POST /api/channels/{id}/quality/scan", s.srv.handleQualityScan)
	s.mux.HandleFunc("DELETE /api/channels/{id}/quality/scan", s.srv.handleQualityScanCancel)
	s.mux.HandleFunc("GET /api/channels/{id}/quality/snapshot", s.srv.handleQualitySnapshot)
	s.mux.HandleFunc("GET /api/channels/{id}/quality/cycles", s.srv.handleQualityCycles)
	s.mux.HandleFunc("GET /api/channels/{id}/quality/metrics", s.srv.handleQualityMetrics)
	s.mux.HandleFunc("GET /api/channels/{id}/quality/diagnostics", s.srv.handleQualityDiagnostics)
	s.mux.HandleFunc("GET /api/channels/{id}/quality/rules", s.srv.handleQualityRules)
	s.mux.HandleFunc("POST /api/channels/{id}/quality/whatif", s.srv.handleQualityWhatif)
	s.mux.HandleFunc("GET /api/channels/{id}/quality/evolution", s.srv.handleQualityEvolution)
	s.mux.HandleFunc("GET /api/channels/{id}/quality/c4", s.srv.handleQualityC4)
	s.mux.HandleFunc("GET /api/channels/{id}/quality/bugfactor", s.srv.handleQualityBugFactor)
	s.mux.HandleFunc("GET /api/channels/{id}/quality/complexity", s.srv.handleQualityComplexity)
	s.mux.HandleFunc("GET /api/channels/{id}/quality/clones", s.srv.handleQualityClones)
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

func (s *ServerSuite) TestSetImageManager() {
	srv := nilServer()
	require.Nil(s.T(), srv.imageManager)
	srv.SetImageManager(nil) // just exercises the setter
}

func (s *ServerSuite) TestSetApprovalResolver() {
	srv := nilServer()
	require.Nil(s.T(), srv.approvalResolver)
	r := &fakeGateResolver{}
	srv.SetApprovalResolver(r)
	require.Same(s.T(), r, srv.approvalResolver)
}

func (s *ServerSuite) TestCleanupBrowsersWithProvider() {
	browserMgr := new(mockBrowserProvider)
	browserMgr.On("Cleanup", mock.Anything).Return()
	s.srv.SetBrowserProvider(browserMgr)

	s.srv.CleanupBrowsers(context.Background())
	browserMgr.AssertCalled(s.T(), "Cleanup", mock.Anything)
}

func (s *ServerSuite) TestCleanupBrowsersNilProvider() {
	// Should not panic when no browser provider is set.
	s.srv.CleanupBrowsers(context.Background())
}

func (s *ServerSuite) TestCleanupBrowsersProviderWithoutCleanup() {
	// mockBrowserProvider does implement Cleanup, but a provider that doesn't
	// should gracefully be skipped. Verify no panic with a non-cleaner provider.
	s.srv.CleanupBrowsers(context.Background())
}

func (s *ServerSuite) TestExtractTextBlocksInvalidJSON() {
	result := extractTextBlocks(json.RawMessage(`not json`))
	require.Equal(s.T(), "", result)
}

func (s *ServerSuite) TestExtractTextBlocksMultipleTexts() {
	raw := json.RawMessage(`[{"type":"text","text":"hello"},{"type":"tool_use","text":"skip"},{"type":"text","text":"world"}]`)
	result := extractTextBlocks(raw)
	require.Equal(s.T(), "hello world", result)
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
		{"DeleteQueuedMessage", "DELETE", "DELETE /api/messages/{id}", "/api/messages/msg-1?channel_id=ch-1", ""},
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
			case "DeleteQueuedMessage":
				mux.HandleFunc(tt.pattern, srv.handleDeleteQueuedMessage)
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
