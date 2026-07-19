package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type WorkflowsSuite struct {
	suite.Suite
	httpClient *mockHTTPClient
	srv        *Server
	ctx        context.Context
	session    *mcp.ClientSession
	cleanup    func()
}

func TestWorkflowsSuite(t *testing.T) {
	suite.Run(t, new(WorkflowsSuite))
}

func (s *WorkflowsSuite) SetupTest() {
	s.httpClient = &mockHTTPClient{}
	s.srv = New("test-channel", "http://localhost:8222", "", s.httpClient, nil, WithWorkflowAPI())
	s.ctx = context.Background()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1.0.0"}, nil)
	t1, t2 := mcp.NewInMemoryTransports()

	go func() {
		_ = s.srv.Run(s.ctx, t1)
	}()

	session, err := client.Connect(s.ctx, t2, nil)
	require.NoError(s.T(), err)

	s.session = session
	s.cleanup = func() {
		session.Close()
	}
}

func (s *WorkflowsSuite) TearDownTest() {
	if s.cleanup != nil {
		s.cleanup()
	}
}

// callTool is a helper that calls a tool and returns (text, isError).
func (s *WorkflowsSuite) callTool(name string, args map[string]any) (string, bool) {
	s.T().Helper()
	res, err := s.session.CallTool(s.ctx, &mcp.CallToolParams{
		Name:      name,
		Arguments: args,
	})
	require.NoError(s.T(), err)
	require.Len(s.T(), res.Content, 1)
	return res.Content[0].(*mcp.TextContent).Text, res.IsError
}

// --- WorkflowAPI option ---

func TestWithWorkflowAPI_RegistersTools(t *testing.T) {
	httpClient := &mockHTTPClient{}
	srv := New("ch", "http://localhost:8222", "", httpClient, nil, WithWorkflowAPI())
	require.True(t, srv.workflowsEnabled)

	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1.0.0"}, nil)
	t1, t2 := mcp.NewInMemoryTransports()
	go func() { _ = srv.Run(ctx, t1) }()
	session, err := client.Connect(ctx, t2, nil)
	require.NoError(t, err)
	defer session.Close()

	res, err := session.ListTools(ctx, nil)
	require.NoError(t, err)
	names := make(map[string]bool)
	for _, tool := range res.Tools {
		names[tool.Name] = true
	}
	require.True(t, names["run_workflow"])
	require.True(t, names["get_workflow_run"])
	require.True(t, names["list_workflows"])
	require.True(t, names["list_workflow_runs"])
	require.True(t, names["cancel_workflow_run"])
	require.True(t, names["resume_workflow_run"])
	require.True(t, names["delete_workflow_run"])
	require.True(t, names["retry_workflow_run"])
	require.True(t, names["save_workflow"])
	require.True(t, names["delete_workflow"])
}

// --- run_workflow ---

func (s *WorkflowsSuite) TestRunWorkflowSuccess() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "POST", req.Method)
		require.Contains(s.T(), req.URL.String(), "/api/workflows/runs")
		return jsonResponse(http.StatusCreated, `{"run_id":"run-abc-123"}`), nil
	}

	text, isError := s.callTool("run_workflow", map[string]any{
		"workflow_name": "my-workflow",
	})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, `"my-workflow"`)
	require.Contains(s.T(), text, "run-abc-123")
	require.Contains(s.T(), text, "get_workflow_run")
}

func (s *WorkflowsSuite) TestRunWorkflowWithInputs() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		body := make([]byte, req.ContentLength)
		_, _ = req.Body.Read(body)
		require.Contains(s.T(), string(body), `"inputs"`)
		return jsonResponse(http.StatusCreated, `{"run_id":"run-with-inputs"}`), nil
	}

	text, isError := s.callTool("run_workflow", map[string]any{
		"workflow_name": "pipeline",
		"inputs":        map[string]any{"env": "staging", "version": "1.2.3"},
	})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "run-with-inputs")
}

func (s *WorkflowsSuite) TestRunWorkflowWithDirPath() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusCreated, `{"run_id":"run-dir"}`), nil
	}

	text, isError := s.callTool("run_workflow", map[string]any{
		"workflow_name": "deploy",
		"dir_path":      "/some/project",
	})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "run-dir")
}

func (s *WorkflowsSuite) TestRunWorkflowUsesFallbackDirPath() {
	// Create a server with a dirPath set via WithMemoryAPI so we can verify the
	// fallback dir_path behaviour when the tool input omits dir_path.
	httpClient := &mockHTTPClient{}
	called := false
	httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		called = true
		return jsonResponse(http.StatusCreated, `{"run_id":"run-fallback"}`), nil
	}

	srv := New("ch", "http://localhost:8222", "", httpClient, nil,
		WithWorkflowAPI(),
		WithMemoryAPI("/fallback/dir"),
	)
	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1.0.0"}, nil)
	t1, t2 := mcp.NewInMemoryTransports()
	go func() { _ = srv.Run(ctx, t1) }()
	session, err := client.Connect(ctx, t2, nil)
	require.NoError(s.T(), err)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "run_workflow",
		Arguments: map[string]any{"workflow_name": "build"},
	})
	require.NoError(s.T(), err)
	require.Len(s.T(), res.Content, 1)
	text := res.Content[0].(*mcp.TextContent).Text
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), text, "run-fallback")
	require.True(s.T(), called)
}

func (s *WorkflowsSuite) TestRunWorkflowErrors() {
	runToolErrorCases(&s.Suite, s.httpClient, s.callTool, toolErrorSpec{
		tool:         "run_workflow",
		args:         map[string]any{"workflow_name": "fail-workflow"},
		apiStatus:    http.StatusInternalServerError,
		apiBody:      "internal error",
		decodeStatus: http.StatusCreated,
	})
}

// --- get_workflow_run ---

func (s *WorkflowsSuite) TestGetWorkflowRunSuccess() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "GET", req.Method)
		require.Contains(s.T(), req.URL.String(), "/api/workflows/runs/run-abc-123")
		return jsonResponse(http.StatusOK, `{"run":{"id":"run-abc-123","status":"completed"},"node_runs":[{"node":"step1","output":"ok"}]}`), nil
	}

	text, isError := s.callTool("get_workflow_run", map[string]any{
		"run_id": "run-abc-123",
	})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "run-abc-123")
	require.Contains(s.T(), text, "completed")
	require.Contains(s.T(), text, "step1")
}

func (s *WorkflowsSuite) TestGetWorkflowRunURLEscaping() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Contains(s.T(), req.URL.String(), "/api/workflows/runs/run%2Fslash")
		return jsonResponse(http.StatusOK, `{"run":{},"node_runs":[]}`), nil
	}

	text, isError := s.callTool("get_workflow_run", map[string]any{
		"run_id": "run/slash",
	})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "run")
}

func (s *WorkflowsSuite) TestGetWorkflowRunErrors() {
	runToolErrorCases(&s.Suite, s.httpClient, s.callTool, toolErrorSpec{
		tool:         "get_workflow_run",
		args:         map[string]any{"run_id": "run-xyz"},
		apiStatus:    http.StatusNotFound,
		apiBody:      "not found",
		decodeStatus: http.StatusOK,
	})
}

// --- list_workflows ---

func (s *WorkflowsSuite) TestListWorkflowsSuccess() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "GET", req.Method)
		require.Contains(s.T(), req.URL.String(), "/api/workflows")
		return jsonResponse(http.StatusOK, `[{"name":"deploy","description":"Deploy to env","inputs":{"env":"string"},"nodes":[]},{"name":"build","description":"Build project","nodes":[]}]`), nil
	}

	text, isError := s.callTool("list_workflows", map[string]any{})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "deploy")
	require.Contains(s.T(), text, "Deploy to env")
	require.Contains(s.T(), text, "build")
}

func (s *WorkflowsSuite) TestListWorkflowsEmpty() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `[]`), nil
	}

	text, isError := s.callTool("list_workflows", map[string]any{})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "[]")
}

func (s *WorkflowsSuite) TestListWorkflowsWithDirPath() {
	// Build a server that has dirPath set so list_workflows appends dir_path + channel_id query params.
	httpClient := &mockHTTPClient{}
	httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Contains(s.T(), req.URL.RawQuery, "dir_path=")
		require.Contains(s.T(), req.URL.RawQuery, "%2Fmy%2Fproject")
		require.Contains(s.T(), req.URL.RawQuery, "channel_id=ch")
		return jsonResponse(http.StatusOK, `[{"name":"proj-workflow","description":"project wf","nodes":[]}]`), nil
	}

	srv := New("ch", "http://localhost:8222", "", httpClient, nil,
		WithWorkflowAPI(),
		WithMemoryAPI("/my/project"),
	)
	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1.0.0"}, nil)
	t1, t2 := mcp.NewInMemoryTransports()
	go func() { _ = srv.Run(ctx, t1) }()
	session, err := client.Connect(ctx, t2, nil)
	require.NoError(s.T(), err)
	defer session.Close()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "list_workflows",
		Arguments: map[string]any{},
	})
	require.NoError(s.T(), err)
	require.Len(s.T(), res.Content, 1)
	text := res.Content[0].(*mcp.TextContent).Text
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), text, "proj-workflow")
}

func (s *WorkflowsSuite) TestListWorkflowsNoDirPath() {
	// When no dirPath is configured, the URL must not contain dir_path param
	// but should still contain channel_id.
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.NotContains(s.T(), req.URL.RawQuery, "dir_path")
		require.Contains(s.T(), req.URL.RawQuery, "channel_id=test-channel")
		return jsonResponse(http.StatusOK, `[]`), nil
	}

	_, isError := s.callTool("list_workflows", map[string]any{})
	require.False(s.T(), isError)
}

func (s *WorkflowsSuite) TestListWorkflowsErrors() {
	tests := []struct {
		name     string
		doFunc   func(*http.Request) (*http.Response, error)
		wantText string
	}{
		{"API error", func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusInternalServerError, "server error"), nil
		}, "API error"},
		{"HTTP error", func(*http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("connection refused")
		}, "calling API"},
		{"invalid JSON", func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusOK, "not json"), nil
		}, "decoding response"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.httpClient.doFunc = tt.doFunc
			text, isError := s.callTool("list_workflows", map[string]any{})
			require.True(s.T(), isError)
			require.Contains(s.T(), text, tt.wantText)
		})
	}
}

// --- list_workflow_runs ---

func (s *WorkflowsSuite) TestListWorkflowRunsSuccess() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "GET", req.Method)
		require.Contains(s.T(), req.URL.String(), "/api/workflows/runs")
		require.Contains(s.T(), req.URL.String(), "limit=20")
		return jsonResponse(http.StatusOK, `[{"id":"run-1","workflow_name":"deploy","status":"completed","started_at":"2026-01-01T10:00:00Z"},{"id":"run-2","workflow_name":"build","status":"running","started_at":"2026-01-01T11:00:00Z"}]`), nil
	}

	text, isError := s.callTool("list_workflow_runs", map[string]any{})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "run-1")
	require.Contains(s.T(), text, "deploy")
	require.Contains(s.T(), text, "completed")
	require.Contains(s.T(), text, "run-2")
	require.Contains(s.T(), text, "running")
}

func (s *WorkflowsSuite) TestListWorkflowRunsWithChannelID() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Contains(s.T(), req.URL.String(), "channel_id=my-chan")
		require.Contains(s.T(), req.URL.String(), "limit=20")
		return jsonResponse(http.StatusOK, `[{"id":"run-chan","workflow_name":"wf","status":"completed","started_at":"2026-01-01T09:00:00Z"}]`), nil
	}

	text, isError := s.callTool("list_workflow_runs", map[string]any{
		"channel_id": "my-chan",
	})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "run-chan")
}

func (s *WorkflowsSuite) TestListWorkflowRunsWithCustomLimit() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Contains(s.T(), req.URL.String(), "limit=5")
		return jsonResponse(http.StatusOK, `[]`), nil
	}

	_, isError := s.callTool("list_workflow_runs", map[string]any{
		"limit": float64(5),
	})
	require.False(s.T(), isError)
}

func (s *WorkflowsSuite) TestListWorkflowRunsDefaultLimit() {
	// When limit is 0 (omitted), the default of 20 should be used.
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Contains(s.T(), req.URL.String(), "limit=20")
		return jsonResponse(http.StatusOK, `[]`), nil
	}

	_, isError := s.callTool("list_workflow_runs", map[string]any{})
	require.False(s.T(), isError)
}

func (s *WorkflowsSuite) TestListWorkflowRunsEmpty() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `[]`), nil
	}

	text, isError := s.callTool("list_workflow_runs", map[string]any{})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "[]")
}

func (s *WorkflowsSuite) TestListWorkflowRunsWithChannelIDAndLimit() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Contains(s.T(), req.URL.String(), "channel_id=proj-chan")
		require.Contains(s.T(), req.URL.String(), "limit=10")
		return jsonResponse(http.StatusOK, `[{"id":"run-x","workflow_name":"ci","status":"failed","started_at":"2026-02-01T08:00:00Z"}]`), nil
	}

	text, isError := s.callTool("list_workflow_runs", map[string]any{
		"channel_id": "proj-chan",
		"limit":      float64(10),
	})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "run-x")
	require.Contains(s.T(), text, "failed")
}

func (s *WorkflowsSuite) TestListWorkflowRunsErrors() {
	tests := []struct {
		name     string
		doFunc   func(*http.Request) (*http.Response, error)
		wantText string
	}{
		{"API error", func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusInternalServerError, "db error"), nil
		}, "API error"},
		{"HTTP error", func(*http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("connection refused")
		}, "calling API"},
		{"invalid JSON", func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusOK, "not json"), nil
		}, "decoding response"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.httpClient.doFunc = tt.doFunc
			text, isError := s.callTool("list_workflow_runs", map[string]any{})
			require.True(s.T(), isError)
			require.Contains(s.T(), text, tt.wantText)
		})
	}
}

// --- cancel_workflow_run ---

func (s *WorkflowsSuite) TestCancelWorkflowRunSuccess() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "POST", req.Method)
		require.Contains(s.T(), req.URL.String(), "/api/workflows/runs/run-cancel-123/cancel")
		return jsonResponse(http.StatusNoContent, ""), nil
	}

	text, isError := s.callTool("cancel_workflow_run", map[string]any{
		"run_id": "run-cancel-123",
	})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "run-cancel-123")
	require.Contains(s.T(), text, "cancelled")
}

func (s *WorkflowsSuite) TestCancelWorkflowRunErrors() {
	runToolErrorCases(&s.Suite, s.httpClient, s.callTool, toolErrorSpec{
		tool:      "cancel_workflow_run",
		args:      map[string]any{"run_id": "run-fail"},
		apiStatus: http.StatusInternalServerError,
		apiBody:   "not found",
	})
}

// --- resume_workflow_run ---

func (s *WorkflowsSuite) TestResumeWorkflowRunSuccess() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "POST", req.Method)
		require.Contains(s.T(), req.URL.String(), "/api/workflows/runs/run-resume-1/resume")
		return jsonResponse(http.StatusNoContent, ""), nil
	}

	text, isError := s.callTool("resume_workflow_run", map[string]any{
		"run_id":   "run-resume-1",
		"response": "approved",
	})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "run-resume-1")
	require.Contains(s.T(), text, "resumed")
}

func (s *WorkflowsSuite) TestResumeWorkflowRunNoResponse() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusNoContent, ""), nil
	}

	text, isError := s.callTool("resume_workflow_run", map[string]any{
		"run_id": "run-resume-2",
	})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "resumed")
}

func (s *WorkflowsSuite) TestResumeWorkflowRunErrors() {
	runToolErrorCases(&s.Suite, s.httpClient, s.callTool, toolErrorSpec{
		tool:      "resume_workflow_run",
		args:      map[string]any{"run_id": "run-fail"},
		apiStatus: http.StatusInternalServerError,
		apiBody:   "no pending approval",
	})
}

// --- save_workflow ---

func (s *WorkflowsSuite) TestSaveWorkflowAdvertisedSchemaIncludesNodeFields() {
	res, err := s.session.ListTools(s.ctx, nil)
	require.NoError(s.T(), err)

	var saveTool *mcp.Tool
	for _, tool := range res.Tools {
		if tool.Name == "save_workflow" {
			saveTool = tool
			break
		}
	}
	require.NotNil(s.T(), saveTool, "save_workflow tool missing")
	require.NotNil(s.T(), saveTool.InputSchema)

	// Re-serialize to JSON so we can do string-based assertions without
	// traversing the nested schema struct by hand.
	raw, err := json.Marshal(saveTool.InputSchema)
	require.NoError(s.T(), err)
	schema := string(raw)

	require.Contains(s.T(), schema, `"script"`)
	require.Contains(s.T(), schema, `"prompt"`)
	require.Contains(s.T(), schema, `"prompt_path"`)
	require.Contains(s.T(), schema, `"depends_on"`)
}

func (s *WorkflowsSuite) TestSaveWorkflowAddSuccess() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "POST", req.Method)
		require.Contains(s.T(), req.URL.String(), "/api/workflows")
		return jsonResponse(http.StatusNoContent, ""), nil
	}

	text, isError := s.callTool("save_workflow", map[string]any{
		"action": "add",
		"workflow": map[string]any{
			"name":        "my-wf",
			"description": "A test workflow",
			"nodes":       []any{map[string]any{"id": "n1", "type": "bash", "script": "echo hi"}},
		},
	})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "Added")
	require.Contains(s.T(), text, "my-wf")
	require.Contains(s.T(), text, "global")
}

func (s *WorkflowsSuite) TestSaveWorkflowUpdateSuccess() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusNoContent, ""), nil
	}

	text, isError := s.callTool("save_workflow", map[string]any{
		"action": "update",
		"workflow": map[string]any{
			"name":        "existing-wf",
			"description": "Updated desc",
			"nodes":       []any{},
		},
	})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "Updated")
	require.Contains(s.T(), text, "existing-wf")
}

func (s *WorkflowsSuite) TestSaveWorkflowProjectScope() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusNoContent, ""), nil
	}

	text, isError := s.callTool("save_workflow", map[string]any{
		"action": "add",
		"scope":  "project",
		"workflow": map[string]any{
			"name":  "proj-wf",
			"nodes": []any{},
		},
	})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "project")
}

func (s *WorkflowsSuite) TestSaveWorkflowInvalidAction() {
	text, isError := s.callTool("save_workflow", map[string]any{
		"action":   "delete",
		"workflow": map[string]any{"name": "wf", "nodes": []any{}},
	})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "action must be")
}

func (s *WorkflowsSuite) TestSaveWorkflowErrors() {
	tests := []struct {
		name     string
		doFunc   func(*http.Request) (*http.Response, error)
		wantText string
	}{
		{"API conflict", func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusConflict, "workflow with this name already exists"), nil
		}, "API error"},
		{"HTTP error", func(*http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("connection refused")
		}, "calling API"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.httpClient.doFunc = tt.doFunc
			text, isError := s.callTool("save_workflow", map[string]any{
				"action":   "add",
				"workflow": map[string]any{"name": "wf", "nodes": []any{}},
			})
			require.True(s.T(), isError)
			require.Contains(s.T(), text, tt.wantText)
		})
	}
}

// --- delete_workflow ---

func (s *WorkflowsSuite) TestDeleteWorkflowSuccess() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "POST", req.Method)
		require.Contains(s.T(), req.URL.String(), "/api/workflows")
		return jsonResponse(http.StatusNoContent, ""), nil
	}

	text, isError := s.callTool("delete_workflow", map[string]any{
		"name": "old-wf",
	})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "Deleted")
	require.Contains(s.T(), text, "old-wf")
	require.Contains(s.T(), text, "global")
}

func (s *WorkflowsSuite) TestDeleteWorkflowProjectScope() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusNoContent, ""), nil
	}

	text, isError := s.callTool("delete_workflow", map[string]any{
		"name":  "proj-wf",
		"scope": "project",
	})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "project")
}

func (s *WorkflowsSuite) TestDeleteWorkflowEmptyName() {
	text, isError := s.callTool("delete_workflow", map[string]any{
		"name": "",
	})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "name is required")
}

func (s *WorkflowsSuite) TestDeleteWorkflowErrors() {
	runToolErrorCases(&s.Suite, s.httpClient, s.callTool, toolErrorSpec{
		tool:      "delete_workflow",
		args:      map[string]any{"name": "fail-wf"},
		apiStatus: http.StatusNotFound,
		apiBody:   "workflow not found",
	})
}

// --- delete_workflow_run ---

func (s *WorkflowsSuite) TestDeleteWorkflowRunSuccess() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "DELETE", req.Method)
		require.Contains(s.T(), req.URL.String(), "/api/workflows/runs/run-del-123")
		return jsonResponse(http.StatusNoContent, ""), nil
	}

	text, isError := s.callTool("delete_workflow_run", map[string]any{
		"run_id": "run-del-123",
	})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "run-del-123")
	require.Contains(s.T(), text, "deleted")
}

func (s *WorkflowsSuite) TestDeleteWorkflowRunURLEscaping() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Contains(s.T(), req.URL.String(), "/api/workflows/runs/run%2Fslash")
		return jsonResponse(http.StatusNoContent, ""), nil
	}

	_, isError := s.callTool("delete_workflow_run", map[string]any{
		"run_id": "run/slash",
	})
	require.False(s.T(), isError)
}

func (s *WorkflowsSuite) TestDeleteWorkflowRunErrors() {
	runToolErrorCases(&s.Suite, s.httpClient, s.callTool, toolErrorSpec{
		tool:      "delete_workflow_run",
		args:      map[string]any{"run_id": "run-fail"},
		apiStatus: http.StatusInternalServerError,
		apiBody:   "not found",
	})
}

// --- retry_workflow_run ---

func (s *WorkflowsSuite) TestRetryWorkflowRunSuccess() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "POST", req.Method)
		require.Contains(s.T(), req.URL.String(), "/api/workflows/runs/run-retry-123/retry")
		return jsonResponse(http.StatusCreated, `{"run_id":"run-new-456"}`), nil
	}

	text, isError := s.callTool("retry_workflow_run", map[string]any{
		"run_id": "run-retry-123",
	})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "run-new-456")
	require.Contains(s.T(), text, "get_workflow_run")
}

func (s *WorkflowsSuite) TestRetryWorkflowRunURLEscaping() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Contains(s.T(), req.URL.String(), "/api/workflows/runs/run%2Fslash/retry")
		return jsonResponse(http.StatusCreated, `{"run_id":"run-new-789"}`), nil
	}

	text, isError := s.callTool("retry_workflow_run", map[string]any{
		"run_id": "run/slash",
	})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "run-new-789")
}

func (s *WorkflowsSuite) TestRetryWorkflowRunErrors() {
	runToolErrorCases(&s.Suite, s.httpClient, s.callTool, toolErrorSpec{
		tool:         "retry_workflow_run",
		args:         map[string]any{"run_id": "run-fail"},
		apiStatus:    http.StatusInternalServerError,
		apiBody:      "run not found",
		decodeStatus: http.StatusCreated,
	})
}
