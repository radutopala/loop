package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/testutil"
	"github.com/radutopala/loop/internal/workflow"
)

type MockWorkflowEngine struct {
	mock.Mock
}

func (m *MockWorkflowEngine) StartRun(ctx context.Context, opts workflow.StartRunOptions) (string, error) {
	args := m.Called(ctx, opts)
	return args.String(0), args.Error(1)
}

func (m *MockWorkflowEngine) ResumeRun(ctx context.Context, runID, response string) error {
	return m.Called(ctx, runID, response).Error(0)
}

func (m *MockWorkflowEngine) CancelRun(ctx context.Context, runID string) error {
	return m.Called(ctx, runID).Error(0)
}

func (m *MockWorkflowEngine) DeleteRun(ctx context.Context, runID string) error {
	return m.Called(ctx, runID).Error(0)
}

func (m *MockWorkflowEngine) RetryRun(ctx context.Context, runID string) (string, error) {
	args := m.Called(ctx, runID)
	return args.String(0), args.Error(1)
}

func (m *MockWorkflowEngine) GetRun(ctx context.Context, runID string) (*db.WorkflowRun, []*db.NodeRun, error) {
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

func (m *MockWorkflowEngine) ListRuns(ctx context.Context, channelID string, limit int) ([]*db.WorkflowRun, error) {
	args := m.Called(ctx, channelID, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*db.WorkflowRun), args.Error(1)
}

func (m *MockWorkflowEngine) ListWorkflows(ctx context.Context, dirPath, parentDirPath string) ([]config.WorkflowDef, error) {
	args := m.Called(ctx, dirPath, parentDirPath)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]config.WorkflowDef), args.Error(1)
}

func (m *MockWorkflowEngine) RecoverRuns(ctx context.Context) error {
	return m.Called(ctx).Error(0)
}

// --- tests ---

func (s *ServerSuite) TestStartWorkflowRun_NotConfigured() {
	rec := s.testRequest("POST", "/api/workflows/runs", `{"workflow_name":"test"}`)
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

func (s *ServerSuite) TestStartWorkflowRun_MissingName() {
	wfe := new(MockWorkflowEngine)
	s.srv.SetWorkflowEngine(wfe)
	defer func() { s.srv.workflowEngine = nil }()

	rec := s.testRequest("POST", "/api/workflows/runs", `{}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestStartWorkflowRun_Success() {
	wfe := new(MockWorkflowEngine)
	s.srv.SetWorkflowEngine(wfe)
	defer func() { s.srv.workflowEngine = nil }()

	// Channel lookup for parentDirPath resolution
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{
		ChannelID: "ch1", DirPath: "/projects/myapp",
	}, nil).Once()

	wfe.On("StartRun", mock.Anything, workflow.StartRunOptions{
		WorkflowName: "fix-issue",
		ChannelID:    "ch1",
		DirPath:      "/projects/myapp",
		Inputs:       map[string]string{"url": "https://example.com"},
	}).Return("wfr-abc123", nil)

	rec := s.testRequest("POST", "/api/workflows/runs", `{"workflow_name":"fix-issue","channel_id":"ch1","inputs":{"url":"https://example.com"}}`)
	require.Equal(s.T(), http.StatusCreated, rec.Code)

	var resp startWorkflowRunResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(s.T(), "wfr-abc123", resp.RunID)
}

func (s *ServerSuite) TestStartWorkflowRun_EngineError() {
	wfe := new(MockWorkflowEngine)
	s.srv.SetWorkflowEngine(wfe)
	defer func() { s.srv.workflowEngine = nil }()

	wfe.On("StartRun", mock.Anything, mock.Anything).Return("", errors.New("workflow not found: nope"))

	rec := s.testRequest("POST", "/api/workflows/runs", `{"workflow_name":"nope"}`)
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
}

func (s *ServerSuite) TestGetWorkflowRun_Success() {
	wfe := new(MockWorkflowEngine)
	s.srv.SetWorkflowEngine(wfe)
	defer func() { s.srv.workflowEngine = nil }()

	run := &db.WorkflowRun{ID: "wfr-1", WorkflowName: "test", Status: db.WorkflowRunStatusCompleted}
	nodes := []*db.NodeRun{{RunID: "wfr-1", NodeID: "n1", Status: db.NodeRunStatusSuccess}}
	wfe.On("GetRun", mock.Anything, "wfr-1").Return(run, nodes, nil)

	rec := s.testRequest("GET", "/api/workflows/runs/wfr-1", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp map[string]json.RawMessage
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Contains(s.T(), resp, "run")
	require.Contains(s.T(), resp, "node_runs")
}

func (s *ServerSuite) TestGetWorkflowRun_NotFound() {
	wfe := new(MockWorkflowEngine)
	s.srv.SetWorkflowEngine(wfe)
	defer func() { s.srv.workflowEngine = nil }()

	wfe.On("GetRun", mock.Anything, "missing").Return(nil, nil, nil)

	rec := s.testRequest("GET", "/api/workflows/runs/missing", "")
	require.Equal(s.T(), http.StatusNotFound, rec.Code)
}

func (s *ServerSuite) TestListWorkflowRuns_Success() {
	wfe := new(MockWorkflowEngine)
	s.srv.SetWorkflowEngine(wfe)
	defer func() { s.srv.workflowEngine = nil }()

	runs := []*db.WorkflowRun{{ID: "wfr-1"}, {ID: "wfr-2"}}
	wfe.On("ListRuns", mock.Anything, "", 50).Return(runs, nil)

	rec := s.testRequest("GET", "/api/workflows/runs", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp []*db.WorkflowRun
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(s.T(), resp, 2)
}

func (s *ServerSuite) TestListWorkflowRuns_WithFilter() {
	wfe := new(MockWorkflowEngine)
	s.srv.SetWorkflowEngine(wfe)
	defer func() { s.srv.workflowEngine = nil }()

	wfe.On("ListRuns", mock.Anything, "ch1", 10).Return([]*db.WorkflowRun{}, nil)

	rec := s.testRequest("GET", "/api/workflows/runs?channel_id=ch1&limit=10", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
}

func (s *ServerSuite) TestCancelWorkflowRun_Success() {
	wfe := new(MockWorkflowEngine)
	s.srv.SetWorkflowEngine(wfe)
	defer func() { s.srv.workflowEngine = nil }()

	wfe.On("CancelRun", mock.Anything, "wfr-1").Return(nil)

	rec := s.testRequest("POST", "/api/workflows/runs/wfr-1/cancel", "")
	require.Equal(s.T(), http.StatusNoContent, rec.Code)
}

func (s *ServerSuite) TestCancelWorkflowRun_Error() {
	wfe := new(MockWorkflowEngine)
	s.srv.SetWorkflowEngine(wfe)
	defer func() { s.srv.workflowEngine = nil }()

	wfe.On("CancelRun", mock.Anything, "wfr-1").Return(errors.New("not found"))

	rec := s.testRequest("POST", "/api/workflows/runs/wfr-1/cancel", "")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
}

func (s *ServerSuite) TestListWorkflows_Success() {
	wfe := new(MockWorkflowEngine)
	s.srv.SetWorkflowEngine(wfe)
	defer func() { s.srv.workflowEngine = nil }()

	wfs := []config.WorkflowDef{{Name: "fix-issue", Description: "Fix it"}}
	wfe.On("ListWorkflows", mock.Anything, "", "").Return(wfs, nil)

	rec := s.testRequest("GET", "/api/workflows", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp []config.WorkflowDef
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(s.T(), resp, 1)
	require.Equal(s.T(), "fix-issue", resp[0].Name)
}

func (s *ServerSuite) TestListWorkflows_NotConfigured() {
	rec := s.testRequest("GET", "/api/workflows", "")
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

func (s *ServerSuite) TestStartWorkflowRun_InvalidJSON() {
	wfe := new(MockWorkflowEngine)
	s.srv.SetWorkflowEngine(wfe)
	defer func() { s.srv.workflowEngine = nil }()

	rec := s.testRequest("POST", "/api/workflows/runs", "not json")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestGetWorkflowRun_NotConfigured() {
	rec := s.testRequest("GET", "/api/workflows/runs/wfr-1", "")
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

func (s *ServerSuite) TestGetWorkflowRun_EngineError() {
	wfe := new(MockWorkflowEngine)
	s.srv.SetWorkflowEngine(wfe)
	defer func() { s.srv.workflowEngine = nil }()

	wfe.On("GetRun", mock.Anything, "wfr-1").Return(nil, nil, errors.New("db error"))

	rec := s.testRequest("GET", "/api/workflows/runs/wfr-1", "")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
}

func (s *ServerSuite) TestListWorkflowRuns_NotConfigured() {
	rec := s.testRequest("GET", "/api/workflows/runs", "")
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

func (s *ServerSuite) TestListWorkflowRuns_EngineError() {
	wfe := new(MockWorkflowEngine)
	s.srv.SetWorkflowEngine(wfe)
	defer func() { s.srv.workflowEngine = nil }()

	wfe.On("ListRuns", mock.Anything, "", 50).Return(nil, errors.New("db error"))

	rec := s.testRequest("GET", "/api/workflows/runs", "")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
}

func (s *ServerSuite) TestCancelWorkflowRun_NotConfigured() {
	rec := s.testRequest("POST", "/api/workflows/runs/wfr-1/cancel", "")
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

func (s *ServerSuite) TestResumeWorkflowRun_Success() {
	wfe := new(MockWorkflowEngine)
	s.srv.SetWorkflowEngine(wfe)
	defer func() { s.srv.workflowEngine = nil }()

	wfe.On("ResumeRun", mock.Anything, "wfr-1", "approved").Return(nil)

	rec := s.testRequest("POST", "/api/workflows/runs/wfr-1/resume", `{"response":"approved"}`)
	require.Equal(s.T(), http.StatusNoContent, rec.Code)
}

func (s *ServerSuite) TestResumeWorkflowRun_Error() {
	wfe := new(MockWorkflowEngine)
	s.srv.SetWorkflowEngine(wfe)
	defer func() { s.srv.workflowEngine = nil }()

	wfe.On("ResumeRun", mock.Anything, "wfr-1", "").Return(errors.New("no pending approval"))

	rec := s.testRequest("POST", "/api/workflows/runs/wfr-1/resume", `{"response":""}`)
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
}

func (s *ServerSuite) TestResumeWorkflowRun_NotConfigured() {
	rec := s.testRequest("POST", "/api/workflows/runs/wfr-1/resume", `{"response":"ok"}`)
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

func (s *ServerSuite) TestResumeWorkflowRun_InvalidJSON() {
	wfe := new(MockWorkflowEngine)
	s.srv.SetWorkflowEngine(wfe)
	defer func() { s.srv.workflowEngine = nil }()

	rec := s.testRequest("POST", "/api/workflows/runs/wfr-1/resume", "not json")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestListWorkflows_EngineError() {
	wfe := new(MockWorkflowEngine)
	s.srv.SetWorkflowEngine(wfe)
	defer func() { s.srv.workflowEngine = nil }()

	wfe.On("ListWorkflows", mock.Anything, "", "").Return(nil, errors.New("config error"))

	rec := s.testRequest("GET", "/api/workflows", "")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
}

func (s *ServerSuite) TestListWorkflows_WithChannelID() {
	wfe := new(MockWorkflowEngine)
	s.srv.SetWorkflowEngine(wfe)
	defer func() { s.srv.workflowEngine = nil }()

	// Regular channel with dir_path
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{
		ChannelID: "ch1", DirPath: "/projects/myapp",
	}, nil).Once()
	wfs := []config.WorkflowDef{{Name: "project-wf"}}
	wfe.On("ListWorkflows", mock.Anything, "/projects/myapp", "").Return(wfs, nil)

	rec := s.testRequest("GET", "/api/workflows?channel_id=ch1", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
}

func (s *ServerSuite) TestListWorkflows_WorktreeChannelID() {
	wfe := new(MockWorkflowEngine)
	s.srv.SetWorkflowEngine(wfe)
	defer func() { s.srv.workflowEngine = nil }()

	// Worktree channel resolves parentDirPath from parent
	s.store.On("GetChannel", mock.Anything, "wt-1").Return(&db.Channel{
		ChannelID: "wt-1", DirPath: "/worktrees/wt-1", Worktree: true, ParentID: "ch1",
	}, nil).Once()
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{
		ChannelID: "ch1", DirPath: "/projects/myapp",
	}, nil).Once()

	wfs := []config.WorkflowDef{{Name: "merged-wf"}}
	wfe.On("ListWorkflows", mock.Anything, "/worktrees/wt-1", "/projects/myapp").Return(wfs, nil)

	rec := s.testRequest("GET", "/api/workflows?channel_id=wt-1", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
}

func (s *ServerSuite) TestStartWorkflowRun_WorktreeResolvesParent() {
	wfe := new(MockWorkflowEngine)
	s.srv.SetWorkflowEngine(wfe)
	defer func() { s.srv.workflowEngine = nil }()

	// Worktree channel → parent resolution
	s.store.On("GetChannel", mock.Anything, "wt-1").Return(&db.Channel{
		ChannelID: "wt-1", DirPath: "/worktrees/wt-1", Worktree: true, ParentID: "ch1",
	}, nil).Once()
	s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{
		ChannelID: "ch1", DirPath: "/projects/myapp",
	}, nil).Once()

	wfe.On("StartRun", mock.Anything, workflow.StartRunOptions{
		WorkflowName:  "fix-issue",
		ChannelID:     "wt-1",
		DirPath:       "/worktrees/wt-1",
		ParentDirPath: "/projects/myapp",
		Inputs:        map[string]string{"url": "https://example.com"},
	}).Return("wfr-wt", nil)

	rec := s.testRequest("POST", "/api/workflows/runs", `{"workflow_name":"fix-issue","channel_id":"wt-1","dir_path":"/worktrees/wt-1","inputs":{"url":"https://example.com"}}`)
	require.Equal(s.T(), http.StatusCreated, rec.Code)
}

// --- resolveWorkflowConfigPaths tests ---

func (s *ServerSuite) TestResolveWorkflowConfigPaths_EmptyChannelID() {
	_, _, err := s.srv.resolveWorkflowConfigPaths(context.Background(), "")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "channel_id is required")
}

func (s *ServerSuite) TestResolveWorkflowConfigPaths_NilStore() {
	savedStore := s.srv.store
	s.srv.store = nil
	defer func() { s.srv.store = savedStore }()

	_, _, err := s.srv.resolveWorkflowConfigPaths(context.Background(), "ch1")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "channel lookup not configured")
}

func (s *ServerSuite) TestResolveWorkflowConfigPaths_ChannelNotFound() {
	s.store.On("GetChannel", mock.Anything, "missing").Return(nil, nil).Once()

	_, _, err := s.srv.resolveWorkflowConfigPaths(context.Background(), "missing")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "channel missing not found")
}

func (s *ServerSuite) TestResolveWorkflowConfigPaths_ChannelLookupError() {
	s.store.On("GetChannel", mock.Anything, "err-ch").Return(nil, errors.New("db error")).Once()

	_, _, err := s.srv.resolveWorkflowConfigPaths(context.Background(), "err-ch")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "looking up channel")
}

func (s *ServerSuite) TestResolveWorkflowConfigPaths_EmptyDirPathWithLoopDir() {
	s.srv.loopDir = "/data/loop"
	defer func() { s.srv.loopDir = "" }()

	s.store.On("GetChannel", mock.Anything, "no-dir").Return(&db.Channel{
		ChannelID: "no-dir", DirPath: "",
	}, nil).Once()

	dirPath, parentDirPath, err := s.srv.resolveWorkflowConfigPaths(context.Background(), "no-dir")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "/data/loop/no-dir/work", dirPath)
	require.Empty(s.T(), parentDirPath)
}

func (s *ServerSuite) TestResolveWorkflowConfigPaths_EmptyDirPathNoLoopDir() {
	s.store.On("GetChannel", mock.Anything, "no-dir").Return(&db.Channel{
		ChannelID: "no-dir", DirPath: "",
	}, nil).Once()

	_, _, err := s.srv.resolveWorkflowConfigPaths(context.Background(), "no-dir")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "has no dir_path")
}

func (s *ServerSuite) TestResolveWorkflowConfigPaths_WorktreeParentLookupError() {
	// Worktree parent lookup fails — graceful fallback to just dirPath
	s.store.On("GetChannel", mock.Anything, "wt-err").Return(&db.Channel{
		ChannelID: "wt-err", DirPath: "/worktrees/wt-err", Worktree: true, ParentID: "parent-err",
	}, nil).Once()
	s.store.On("GetChannel", mock.Anything, "parent-err").Return(nil, errors.New("db error")).Once()

	dirPath, parentDirPath, err := s.srv.resolveWorkflowConfigPaths(context.Background(), "wt-err")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "/worktrees/wt-err", dirPath)
	require.Empty(s.T(), parentDirPath) // graceful fallback
}

// --- Modify Workflow tests ---

func (s *ServerSuite) TestModifyWorkflowAddGlobal() {
	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("/home/testuser", nil)
	sys.On("ReadFile", "/home/testuser/.loop/config.json").Return([]byte(`{"platforms":["local"]}`), nil)
	var written []byte
	sys.On("WriteFile", "/home/testuser/.loop/config.json", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { written = args.Get(1).([]byte) }).Return(nil)
	s.srv.sys = sys

	rec := s.testRequest("POST", "/api/workflows", `{
		"action": "add",
		"scope": "global",
		"workflow": {"name":"hello","description":"Say hello","nodes":[{"id":"greet","type":"prompt","prompt":"Hello"}]}
	}`)

	require.Equal(s.T(), http.StatusNoContent, rec.Code)
	require.NotNil(s.T(), written)
	var cfg map[string]any
	require.NoError(s.T(), json.Unmarshal(written, &cfg))
	workflows := cfg["workflows"].([]any)
	require.Len(s.T(), workflows, 1)
	wf := workflows[0].(map[string]any)
	require.Equal(s.T(), "hello", wf["name"])
	require.Equal(s.T(), "Say hello", wf["description"])
}

func (s *ServerSuite) TestModifyWorkflowAddDuplicate() {
	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("/home/testuser", nil)
	sys.On("ReadFile", "/home/testuser/.loop/config.json").Return(
		[]byte(`{"workflows":[{"name":"hello","nodes":[]}]}`), nil)
	s.srv.sys = sys

	rec := s.testRequest("POST", "/api/workflows", `{
		"action": "add",
		"workflow": {"name":"hello","nodes":[]}
	}`)

	require.Equal(s.T(), http.StatusConflict, rec.Code)
}

func (s *ServerSuite) TestModifyWorkflowUpdate() {
	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("/home/testuser", nil)
	sys.On("ReadFile", "/home/testuser/.loop/config.json").Return(
		[]byte(`{"workflows":[{"name":"hello","description":"old","nodes":[]}]}`), nil)
	var written []byte
	sys.On("WriteFile", "/home/testuser/.loop/config.json", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { written = args.Get(1).([]byte) }).Return(nil)
	s.srv.sys = sys

	rec := s.testRequest("POST", "/api/workflows", `{
		"action": "update",
		"workflow": {"name":"hello","description":"updated","nodes":[{"id":"n1","type":"bash","script":"echo hi"}]}
	}`)

	require.Equal(s.T(), http.StatusNoContent, rec.Code)
	var cfg map[string]any
	require.NoError(s.T(), json.Unmarshal(written, &cfg))
	wf := cfg["workflows"].([]any)[0].(map[string]any)
	require.Equal(s.T(), "updated", wf["description"])
}

func (s *ServerSuite) TestModifyWorkflowUpdateNotFound() {
	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("/home/testuser", nil)
	sys.On("ReadFile", "/home/testuser/.loop/config.json").Return([]byte(`{}`), nil)
	s.srv.sys = sys

	rec := s.testRequest("POST", "/api/workflows", `{
		"action": "update",
		"workflow": {"name":"nonexistent","nodes":[]}
	}`)

	require.Equal(s.T(), http.StatusNotFound, rec.Code)
}

func (s *ServerSuite) TestModifyWorkflowDelete() {
	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("/home/testuser", nil)
	sys.On("ReadFile", "/home/testuser/.loop/config.json").Return(
		[]byte(`{"workflows":[{"name":"hello","nodes":[]},{"name":"keep","nodes":[]}]}`), nil)
	var written []byte
	sys.On("WriteFile", "/home/testuser/.loop/config.json", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { written = args.Get(1).([]byte) }).Return(nil)
	s.srv.sys = sys

	rec := s.testRequest("POST", "/api/workflows", `{
		"action": "delete",
		"name": "hello"
	}`)

	require.Equal(s.T(), http.StatusNoContent, rec.Code)
	var cfg map[string]any
	require.NoError(s.T(), json.Unmarshal(written, &cfg))
	workflows := cfg["workflows"].([]any)
	require.Len(s.T(), workflows, 1)
	require.Equal(s.T(), "keep", workflows[0].(map[string]any)["name"])
}

func (s *ServerSuite) TestModifyWorkflowDeleteNotFound() {
	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("/home/testuser", nil)
	sys.On("ReadFile", "/home/testuser/.loop/config.json").Return([]byte(`{}`), nil)
	s.srv.sys = sys

	rec := s.testRequest("POST", "/api/workflows", `{
		"action": "delete",
		"name": "nonexistent"
	}`)

	require.Equal(s.T(), http.StatusNotFound, rec.Code)
}

func (s *ServerSuite) TestModifyWorkflowInvalidAction() {
	rec := s.testRequest("POST", "/api/workflows", `{
		"action": "invalid",
		"name": "hello"
	}`)

	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestModifyWorkflowMissingWorkflow() {
	rec := s.testRequest("POST", "/api/workflows", `{
		"action": "add"
	}`)

	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestModifyWorkflowMissingName() {
	rec := s.testRequest("POST", "/api/workflows", `{
		"action": "add",
		"workflow": {"nodes":[]}
	}`)

	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestModifyWorkflowDeleteMissingName() {
	rec := s.testRequest("POST", "/api/workflows", `{
		"action": "delete"
	}`)

	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestModifyWorkflowInvalidJSON() {
	rec := s.testRequest("POST", "/api/workflows", "not json")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestModifyWorkflowInvalidWorkflowJSON() {
	rec := s.testRequest("POST", "/api/workflows", `{
		"action": "add",
		"workflow": "not an object"
	}`)

	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestModifyWorkflowProjectScope() {
	sys := new(testutil.MockSystem)
	sys.On("MkdirAll", "/project/.loop", os.FileMode(0755)).Return(nil)
	sys.On("ReadFile", "/project/.loop/config.json").Return([]byte(`{}`), nil)
	var written []byte
	sys.On("WriteFile", "/project/.loop/config.json", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { written = args.Get(1).([]byte) }).Return(nil)
	s.srv.sys = sys
	s.store.On("GetChannel", mock.Anything, "ch-test").Return(&db.Channel{ChannelID: "ch-test", DirPath: "/project"}, nil)

	rec := s.testRequest("POST", "/api/workflows", `{
		"action": "add",
		"scope": "project",
		"channel_id": "ch-test",
		"workflow": {"name":"proj-wf","description":"Project workflow","nodes":[]}
	}`)

	require.Equal(s.T(), http.StatusNoContent, rec.Code)
	require.NotNil(s.T(), written)
	var cfg map[string]any
	require.NoError(s.T(), json.Unmarshal(written, &cfg))
	workflows := cfg["workflows"].([]any)
	require.Len(s.T(), workflows, 1)
	require.Equal(s.T(), "proj-wf", workflows[0].(map[string]any)["name"])
}

func (s *ServerSuite) TestModifyWorkflowProjectScopeMissingChannel() {
	rec := s.testRequest("POST", "/api/workflows", `{
		"action": "add",
		"scope": "project",
		"workflow": {"name":"hello","nodes":[]}
	}`)

	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestModifyWorkflowHomeDirError() {
	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("", errors.New("no home"))
	s.srv.sys = sys

	rec := s.testRequest("POST", "/api/workflows", `{
		"action": "add",
		"workflow": {"name":"hello","nodes":[]}
	}`)

	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
}

func (s *ServerSuite) TestModifyWorkflowNewConfigFile() {
	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("/home/testuser", nil)
	sys.On("ReadFile", "/home/testuser/.loop/config.json").Return(nil, os.ErrNotExist)
	var written []byte
	sys.On("WriteFile", "/home/testuser/.loop/config.json", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) { written = args.Get(1).([]byte) }).Return(nil)
	s.srv.sys = sys

	rec := s.testRequest("POST", "/api/workflows", `{
		"action": "add",
		"workflow": {"name":"hello","nodes":[]}
	}`)

	require.Equal(s.T(), http.StatusNoContent, rec.Code)
	require.NotNil(s.T(), written)
}

func (s *ServerSuite) TestModifyWorkflowReadFileError() {
	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("/home/testuser", nil)
	sys.On("ReadFile", "/home/testuser/.loop/config.json").Return(nil, errors.New("permission denied"))
	s.srv.sys = sys

	rec := s.testRequest("POST", "/api/workflows", `{
		"action": "add",
		"workflow": {"name":"hello","nodes":[]}
	}`)

	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
}

func (s *ServerSuite) TestModifyWorkflowInvalidHJSON() {
	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("/home/testuser", nil)
	sys.On("ReadFile", "/home/testuser/.loop/config.json").Return([]byte("not valid hjson {{{"), nil)
	s.srv.sys = sys

	rec := s.testRequest("POST", "/api/workflows", `{
		"action": "add",
		"workflow": {"name":"hello","nodes":[]}
	}`)

	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
}

func (s *ServerSuite) TestModifyWorkflowUnmarshalError() {
	orig := jsonUnmarshalFn
	jsonUnmarshalFn = func(data []byte, v any) error { return errors.New("unmarshal fail") }
	defer func() { jsonUnmarshalFn = orig }()

	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("/home/testuser", nil)
	sys.On("ReadFile", "/home/testuser/.loop/config.json").Return([]byte(`{}`), nil)
	s.srv.sys = sys

	rec := s.testRequest("POST", "/api/workflows", `{
		"action": "add",
		"workflow": {"name":"hello","nodes":[]}
	}`)

	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
}

func (s *ServerSuite) TestModifyWorkflowMarshalError() {
	orig := jsonMarshalIndent
	jsonMarshalIndent = func(v any, prefix, indent string) ([]byte, error) {
		return nil, errors.New("marshal fail")
	}
	defer func() { jsonMarshalIndent = orig }()

	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("/home/testuser", nil)
	sys.On("ReadFile", "/home/testuser/.loop/config.json").Return([]byte(`{}`), nil)
	s.srv.sys = sys

	rec := s.testRequest("POST", "/api/workflows", `{
		"action": "add",
		"workflow": {"name":"hello","nodes":[]}
	}`)

	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
}

func (s *ServerSuite) TestModifyWorkflowWriteFileError() {
	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("/home/testuser", nil)
	sys.On("ReadFile", "/home/testuser/.loop/config.json").Return([]byte(`{}`), nil)
	sys.On("WriteFile", "/home/testuser/.loop/config.json", mock.Anything, mock.Anything).Return(errors.New("disk full"))
	s.srv.sys = sys

	rec := s.testRequest("POST", "/api/workflows", `{
		"action": "add",
		"workflow": {"name":"hello","nodes":[]}
	}`)

	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
}

// --- handleDeleteWorkflowRun tests ---

func (s *ServerSuite) TestDeleteWorkflowRun_Success() {
	wfe := new(MockWorkflowEngine)
	s.srv.SetWorkflowEngine(wfe)
	defer func() { s.srv.workflowEngine = nil }()

	wfe.On("DeleteRun", mock.Anything, "wfr-1").Return(nil)

	rec := s.testRequest("DELETE", "/api/workflows/runs/wfr-1", "")
	require.Equal(s.T(), http.StatusNoContent, rec.Code)
}

func (s *ServerSuite) TestDeleteWorkflowRun_EngineError() {
	wfe := new(MockWorkflowEngine)
	s.srv.SetWorkflowEngine(wfe)
	defer func() { s.srv.workflowEngine = nil }()

	wfe.On("DeleteRun", mock.Anything, "wfr-1").Return(errors.New("not found"))

	rec := s.testRequest("DELETE", "/api/workflows/runs/wfr-1", "")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
}

func (s *ServerSuite) TestDeleteWorkflowRun_NotConfigured() {
	rec := s.testRequest("DELETE", "/api/workflows/runs/wfr-1", "")
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

// --- handleRetryWorkflowRun tests ---

func (s *ServerSuite) TestRetryWorkflowRun_Success() {
	wfe := new(MockWorkflowEngine)
	s.srv.SetWorkflowEngine(wfe)
	defer func() { s.srv.workflowEngine = nil }()

	wfe.On("RetryRun", mock.Anything, "wfr-1").Return("wfr-new-1", nil)

	rec := s.testRequest("POST", "/api/workflows/runs/wfr-1/retry", "")
	require.Equal(s.T(), http.StatusCreated, rec.Code)

	var resp retryWorkflowRunResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(s.T(), "wfr-new-1", resp.RunID)
}

func (s *ServerSuite) TestRetryWorkflowRun_EngineError() {
	wfe := new(MockWorkflowEngine)
	s.srv.SetWorkflowEngine(wfe)
	defer func() { s.srv.workflowEngine = nil }()

	wfe.On("RetryRun", mock.Anything, "wfr-1").Return("", errors.New("run not found"))

	rec := s.testRequest("POST", "/api/workflows/runs/wfr-1/retry", "")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
}

func (s *ServerSuite) TestRetryWorkflowRun_NotConfigured() {
	rec := s.testRequest("POST", "/api/workflows/runs/wfr-1/retry", "")
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

func (s *ServerSuite) TestListWorkflowRuns_LimitCapped() {
	wfe := new(MockWorkflowEngine)
	s.srv.SetWorkflowEngine(wfe)
	defer func() { s.srv.workflowEngine = nil }()

	// Request limit=999999, expect it to be capped to 1000.
	wfe.On("ListRuns", mock.Anything, "", 1000).Return([]*db.WorkflowRun{}, nil)

	rec := s.testRequest("GET", "/api/workflows/runs?limit=999999", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
}

func (s *ServerSuite) TestStartWorkflowRun_ChannelResolutionError() {
	wfe := new(MockWorkflowEngine)
	s.srv.SetWorkflowEngine(wfe)
	defer func() { s.srv.workflowEngine = nil }()

	// Channel resolution fails — run should still start (with empty dir path).
	s.store.On("GetChannel", mock.Anything, "bad-ch").Return(nil, errors.New("db error")).Once()

	wfe.On("StartRun", mock.Anything, workflow.StartRunOptions{
		WorkflowName: "wf",
		ChannelID:    "bad-ch",
	}).Return("wfr-1", nil)

	rec := s.testRequest("POST", "/api/workflows/runs", `{"workflow_name":"wf","channel_id":"bad-ch"}`)
	require.Equal(s.T(), http.StatusCreated, rec.Code)
}
