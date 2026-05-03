package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type mockHTTPClient struct {
	doFunc func(req *http.Request) (*http.Response, error)
}

func (m *mockHTTPClient) Do(req *http.Request) (*http.Response, error) {
	return m.doFunc(req)
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(bytes.NewBufferString(body)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}
}

type MCPServerSuite struct {
	suite.Suite
	httpClient *mockHTTPClient
	srv        *Server
	ctx        context.Context
	session    *mcp.ClientSession
	cleanup    func()
}

func TestMCPServerSuite(t *testing.T) {
	suite.Run(t, new(MCPServerSuite))
}

func (s *MCPServerSuite) SetupTest() {
	s.httpClient = &mockHTTPClient{}
	s.srv = New("test-channel", "http://localhost:8222", "", s.httpClient, nil)
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

func (s *MCPServerSuite) TearDownTest() {
	if s.cleanup != nil {
		s.cleanup()
	}
}

// callTool is a helper that calls a tool and returns (text, isError).
func (s *MCPServerSuite) callTool(name string, args map[string]any) (string, bool) {
	s.T().Helper()
	res, err := s.session.CallTool(s.ctx, &mcp.CallToolParams{
		Name:      name,
		Arguments: args,
	})
	require.NoError(s.T(), err)
	require.Len(s.T(), res.Content, 1)
	return res.Content[0].(*mcp.TextContent).Text, res.IsError
}

// noContentResponse returns an empty response with the given status code.
func noContentResponse(status int) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(bytes.NewReader(nil)),
	}
}

// stringResponse returns a response with the given status and body string.
func stringResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(bytes.NewReader([]byte(body))),
	}
}

func (s *MCPServerSuite) TestNew() {
	require.NotNil(s.T(), s.srv)
	require.Equal(s.T(), "test-channel", s.srv.channelID)
	require.Equal(s.T(), "http://localhost:8222", s.srv.apiURL)
	require.NotNil(s.T(), s.srv.mcpServer)
}

// TestRunWithChannelTransport covers Server.Run's channelTransport != nil
// branch: when WithAgentTools is set, Run threads the supplied transport
// into channelTransport.inner, starts the push-receiver goroutine, and
// cancels the receiver ctx on return so no goroutine leaks.
func (s *MCPServerSuite) TestRunWithChannelTransport() {
	srv := New("ch-1", "http://127.0.0.1:1", "author-1", http.DefaultClient, nil, WithAgentTools("agent-0"))
	// Tiny backoffs so the receiver goroutine cycles quickly during the test.
	srv.channelTransport.dialBackoff = 5 * time.Millisecond
	srv.channelTransport.reconnectDelay = 5 * time.Millisecond

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1.0.0"}, nil)
	t1, t2 := mcp.NewInMemoryTransports()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runDone := make(chan error, 1)
	go func() {
		runDone <- srv.Run(ctx, t1)
	}()

	session, err := client.Connect(ctx, t2, nil)
	require.NoError(s.T(), err)

	// Verify the threaded transport actually serves MCP traffic.
	res, err := session.ListTools(ctx, nil)
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), res.Tools)

	require.NoError(s.T(), session.Close())
	cancel()

	select {
	case <-runDone:
	case <-time.After(2 * time.Second):
		s.T().Fatal("Run did not return after ctx cancel + session close")
	}
}

func (s *MCPServerSuite) TestMCPServer() {
	require.Equal(s.T(), s.srv.mcpServer, s.srv.MCPServer())
}

// --- ListTools ---

func (s *MCPServerSuite) TestListTools() {
	res, err := s.session.ListTools(s.ctx, nil)
	require.NoError(s.T(), err)
	require.Len(s.T(), res.Tools, 25) // 12 base + 2 playground + 1 shortcut + 10 quality

	names := make(map[string]bool)
	for _, t := range res.Tools {
		names[t.Name] = true
	}
	require.True(s.T(), names["schedule_task"])
	require.True(s.T(), names["list_tasks"])
	require.True(s.T(), names["show_task"])
	require.True(s.T(), names["cancel_task"])
	require.True(s.T(), names["toggle_task"])
	require.True(s.T(), names["edit_task"])
	require.True(s.T(), names["create_channel"])
	require.True(s.T(), names["create_thread"])
	require.True(s.T(), names["delete_thread"])
	require.True(s.T(), names["search_channels"])
	require.True(s.T(), names["send_message"])
	require.True(s.T(), names["get_readme"])
	require.True(s.T(), names["playground"])
	require.True(s.T(), names["playground_file"])
	require.True(s.T(), names["prompt_shortcut"])
	require.True(s.T(), names["quality_scan"])
	require.True(s.T(), names["quality_snapshot"])
	require.True(s.T(), names["quality_cycles"])
	require.True(s.T(), names["quality_metrics"])
	require.True(s.T(), names["quality_diagnostics"])
	require.True(s.T(), names["quality_rules"])
	require.True(s.T(), names["quality_whatif"])
	require.True(s.T(), names["quality_evolution"])
	require.True(s.T(), names["quality_bugfactor"])
	require.True(s.T(), names["quality_c4"])
}

// --- schedule_task ---

func (s *MCPServerSuite) TestScheduleTaskSuccess() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "POST", req.Method)
		require.Contains(s.T(), req.URL.String(), "/api/tasks")
		return jsonResponse(http.StatusCreated, `{"id":42}`), nil
	}

	text, isError := s.callTool("schedule_task", map[string]any{
		"schedule": "0 9 * * *",
		"type":     "cron",
		"prompt":   "check standup",
	})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "ID: 42")
}

func (s *MCPServerSuite) TestScheduleTaskWithTemplateName() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "POST", req.Method)
		body, _ := io.ReadAll(req.Body)
		require.Contains(s.T(), string(body), `"template_name":"tk-auto-worker"`)
		return jsonResponse(http.StatusCreated, `{"id":55}`), nil
	}

	text, isError := s.callTool("schedule_task", map[string]any{
		"schedule":      "* * * * *",
		"type":          "cron",
		"prompt":        "dispatch",
		"template_name": "tk-auto-worker",
	})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "ID: 55")
}

func (s *MCPServerSuite) TestScheduleTaskWithoutTemplateName() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(req.Body)
		require.NotContains(s.T(), string(body), "template_name")
		return jsonResponse(http.StatusCreated, `{"id":56}`), nil
	}

	text, isError := s.callTool("schedule_task", map[string]any{
		"schedule": "0 9 * * *",
		"type":     "cron",
		"prompt":   "test",
	})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "ID: 56")
}

func (s *MCPServerSuite) TestScheduleTaskWithWorktree() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(req.Body)
		require.Contains(s.T(), string(body), `"worktree":true`)
		return jsonResponse(http.StatusCreated, `{"id":99}`), nil
	}

	text, isError := s.callTool("schedule_task", map[string]any{
		"schedule": "0 9 * * *",
		"type":     "cron",
		"prompt":   "test",
		"worktree": true,
	})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "ID: 99")
}

func (s *MCPServerSuite) TestScheduleTaskWithWorktreeFields() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(req.Body)
		var payload map[string]any
		require.NoError(s.T(), json.Unmarshal(body, &payload))
		require.Equal(s.T(), "main", payload["origin_branch"])
		require.Equal(s.T(), true, payload["update_before_run"])
		return jsonResponse(http.StatusCreated, `{"id":99}`), nil
	}

	text, isError := s.callTool("schedule_task", map[string]any{
		"schedule":          "0 9 * * *",
		"type":              "cron",
		"prompt":            "test",
		"origin_branch":     "main",
		"update_before_run": true,
	})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "ID: 99")
}

func (s *MCPServerSuite) TestScheduleTaskScheduleValidation() {
	tests := []struct {
		name     string
		schedule string
		taskType string
		wantText string
	}{
		{"invalid once", "5m", "once", "RFC3339"},
		{"invalid interval", "*/5 * * * *", "interval", "time.Duration"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			text, isError := s.callTool("schedule_task", map[string]any{
				"schedule": tt.schedule,
				"type":     tt.taskType,
				"prompt":   "test",
			})
			require.True(s.T(), isError)
			require.Contains(s.T(), text, "invalid schedule for type")
			require.Contains(s.T(), text, tt.wantText)
		})
	}
}

func (s *MCPServerSuite) TestScheduleTaskValidSchedules() {
	tests := []struct {
		name     string
		schedule string
		taskType string
		respID   int
	}{
		{"valid RFC3339 once", "2026-02-09T14:30:00Z", "once", 10},
		{"valid duration interval", "1h", "interval", 2},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
				return jsonResponse(http.StatusCreated, fmt.Sprintf(`{"id":%d}`, tt.respID)), nil
			}
			text, isError := s.callTool("schedule_task", map[string]any{
				"schedule": tt.schedule,
				"type":     tt.taskType,
				"prompt":   "test",
			})
			require.False(s.T(), isError)
			require.Contains(s.T(), text, fmt.Sprintf("ID: %d", tt.respID))
		})
	}
}

func (s *MCPServerSuite) TestScheduleTaskErrors() {
	tests := []struct {
		name     string
		doFunc   func(*http.Request) (*http.Response, error)
		wantText string
	}{
		{"API error", func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusInternalServerError, "bad schedule"), nil
		}, "API error"},
		{"HTTP error", func(*http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("connection refused")
		}, "calling API"},
		{"invalid response JSON", func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusCreated, "not json"), nil
		}, "decoding response"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.httpClient.doFunc = tt.doFunc
			text, isError := s.callTool("schedule_task", map[string]any{
				"schedule": "0 9 * * *",
				"type":     "cron",
				"prompt":   "test",
			})
			require.True(s.T(), isError)
			require.Contains(s.T(), text, tt.wantText)
		})
	}
}

func (s *MCPServerSuite) TestScheduleTaskWithWorkflow() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "POST", req.Method)
		body, _ := io.ReadAll(req.Body)
		require.Contains(s.T(), string(body), `"workflow_name":"validate"`)
		require.Contains(s.T(), string(body), `"workflow_inputs":"{\"branch\":\"main\"}"`)
		return jsonResponse(http.StatusCreated, `{"id":99}`), nil
	}

	text, isError := s.callTool("schedule_task", map[string]any{
		"schedule":        "0 9 * * *",
		"type":            "cron",
		"workflow_name":   "validate",
		"workflow_inputs": `{"branch":"main"}`,
	})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "ID: 99")
}

// --- list_tasks ---

func (s *MCPServerSuite) TestListTasksSuccess() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "GET", req.Method)
		require.Contains(s.T(), req.URL.String(), "channel_id=test-channel")
		return jsonResponse(http.StatusOK, `[{"id":1,"schedule":"0 9 * * *","type":"cron","prompt":"standup","enabled":true,"next_run_at":"2025-01-01T09:00:00Z","template_name":"my-tmpl"},{"id":2,"schedule":"5m","type":"interval","prompt":"check","enabled":false,"next_run_at":"2025-01-01T10:00:00Z","template_name":""},{"id":3,"schedule":"1h","type":"interval","prompt":"cleanup","enabled":true,"next_run_at":"2025-01-01T11:00:00Z","template_name":"","auto_delete_sec":120}]`), nil
	}

	text, isError := s.callTool("list_tasks", map[string]any{})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "ID 1")
	require.Contains(s.T(), text, "standup")
	require.Contains(s.T(), text, "template_name: my-tmpl")
	require.Contains(s.T(), text, "ID 2")
	require.NotContains(s.T(), text, "template_name: \n")
	require.Contains(s.T(), text, "ID 3")
	require.Contains(s.T(), text, "auto_delete: 120s")
}

func (s *MCPServerSuite) TestListTasksWithWorktree() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `[{"id":1,"schedule":"0 9 * * *","type":"cron","prompt":"wt task","enabled":true,"next_run_at":"2025-01-01T09:00:00Z","worktree":true}]`), nil
	}

	text, isError := s.callTool("list_tasks", map[string]any{})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "worktree: true")
}

func (s *MCPServerSuite) TestListTasksWithWorktreeFields() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `[{"id":1,"schedule":"0 9 * * *","type":"cron","prompt":"wt task","enabled":true,"next_run_at":"2025-01-01T09:00:00Z","worktree":true,"origin_branch":"develop","update_before_run":true}]`), nil
	}

	text, isError := s.callTool("list_tasks", map[string]any{})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "branch: develop")
	require.Contains(s.T(), text, "update_before_run: true")
}

func (s *MCPServerSuite) TestListTasksWithWorkflow() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `[{"id":1,"schedule":"0 9 * * *","type":"cron","prompt":"","enabled":true,"next_run_at":"2025-01-01T09:00:00Z","workflow_name":"validate","workflow_inputs":"{\"branch\":\"main\"}"}]`), nil
	}

	text, isError := s.callTool("list_tasks", map[string]any{})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "workflow:validate")
	require.Contains(s.T(), text, "inputs:")
}

func (s *MCPServerSuite) TestListTasksWithRunning() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `[{"id":1,"schedule":"0 9 * * *","type":"cron","prompt":"busy task","enabled":true,"next_run_at":"2025-01-01T09:00:00Z","running":true}]`), nil
	}

	text, isError := s.callTool("list_tasks", map[string]any{})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "running: true")
}

func (s *MCPServerSuite) TestListTasksEmpty() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `[]`), nil
	}

	text, isError := s.callTool("list_tasks", map[string]any{})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "No scheduled tasks")
}

func (s *MCPServerSuite) TestListTasksErrors() {
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
		{"invalid response JSON", func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusOK, "not json"), nil
		}, "decoding response"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.httpClient.doFunc = tt.doFunc
			text, isError := s.callTool("list_tasks", map[string]any{})
			require.True(s.T(), isError)
			require.Contains(s.T(), text, tt.wantText)
		})
	}
}

// --- show_task ---

func (s *MCPServerSuite) TestShowTaskSuccess() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "GET", req.Method)
		require.Contains(s.T(), req.URL.String(), "/api/tasks/42")
		return jsonResponse(http.StatusOK, `{"id":42,"schedule":"0 9 * * *","type":"cron","prompt":"full prompt text here","enabled":true,"next_run_at":"2025-01-01T09:00:00Z","template_name":"my-tmpl","auto_delete_sec":60}`), nil
	}

	text, isError := s.callTool("show_task", map[string]any{"task_id": float64(42)})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "Task 42")
	require.Contains(s.T(), text, "Type: cron")
	require.Contains(s.T(), text, "Schedule: 0 9 * * *")
	require.Contains(s.T(), text, "Status: enabled")
	require.Contains(s.T(), text, "Template: my-tmpl")
	require.Contains(s.T(), text, "Auto-delete: 60s")
	require.Contains(s.T(), text, "Prompt:\nfull prompt text here")
}

func (s *MCPServerSuite) TestShowTaskWithWorktree() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"id":42,"schedule":"0 9 * * *","type":"cron","prompt":"wt task","enabled":true,"next_run_at":"2025-01-01T09:00:00Z","worktree":true}`), nil
	}

	text, isError := s.callTool("show_task", map[string]any{"task_id": float64(42)})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "Worktree: true")
}

func (s *MCPServerSuite) TestShowTaskWithWorktreeFields() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"id":42,"schedule":"0 9 * * *","type":"cron","prompt":"wt task","enabled":true,"next_run_at":"2025-01-01T09:00:00Z","worktree":true,"origin_branch":"develop","update_before_run":true}`), nil
	}

	text, isError := s.callTool("show_task", map[string]any{"task_id": float64(42)})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "Origin branch: develop")
	require.Contains(s.T(), text, "Update before run: true")
}

func (s *MCPServerSuite) TestShowTaskRunning() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"id":42,"schedule":"0 9 * * *","type":"cron","prompt":"busy task","enabled":true,"next_run_at":"2025-01-01T09:00:00Z","running":true}`), nil
	}

	text, isError := s.callTool("show_task", map[string]any{"task_id": float64(42)})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "Running: true")
}

func (s *MCPServerSuite) TestShowTaskWithWorkflow() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"id":42,"schedule":"0 9 * * *","type":"cron","prompt":"","enabled":true,"next_run_at":"2025-01-01T09:00:00Z","workflow_name":"validate","workflow_inputs":"{\"branch\":\"main\"}"}`), nil
	}

	text, isError := s.callTool("show_task", map[string]any{"task_id": float64(42)})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "Workflow: validate")
	require.Contains(s.T(), text, "Workflow inputs:")
	require.NotContains(s.T(), text, "Prompt:")
}

func (s *MCPServerSuite) TestShowTaskDisabled() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"id":1,"schedule":"5m","type":"interval","prompt":"test","enabled":false,"next_run_at":"2025-01-01T09:00:00Z"}`), nil
	}

	text, isError := s.callTool("show_task", map[string]any{"task_id": float64(1)})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "Status: disabled")
	require.NotContains(s.T(), text, "Template:")
	require.NotContains(s.T(), text, "Auto-delete:")
}

func (s *MCPServerSuite) TestShowTaskErrors() {
	tests := []struct {
		name     string
		doFunc   func(*http.Request) (*http.Response, error)
		wantText string
	}{
		{"API error", func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusNotFound, "task not found"), nil
		}, "API error"},
		{"HTTP error", func(*http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("connection refused")
		}, "calling API"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.httpClient.doFunc = tt.doFunc
			text, isError := s.callTool("show_task", map[string]any{"task_id": float64(42)})
			require.True(s.T(), isError)
			require.Contains(s.T(), text, tt.wantText)
		})
	}
}

// --- cancel_task ---

func (s *MCPServerSuite) TestCancelTaskSuccess() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "DELETE", req.Method)
		require.Contains(s.T(), req.URL.String(), "/api/tasks/42")
		return noContentResponse(http.StatusNoContent), nil
	}

	text, isError := s.callTool("cancel_task", map[string]any{"task_id": float64(42)})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "Task 42 cancelled")
}

func (s *MCPServerSuite) TestCancelTaskErrors() {
	tests := []struct {
		name     string
		doFunc   func(*http.Request) (*http.Response, error)
		wantText string
	}{
		{"API error", func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusInternalServerError, "not found"), nil
		}, "API error"},
		{"HTTP error", func(*http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("connection refused")
		}, "calling API"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.httpClient.doFunc = tt.doFunc
			text, isError := s.callTool("cancel_task", map[string]any{"task_id": float64(1)})
			require.True(s.T(), isError)
			require.Contains(s.T(), text, tt.wantText)
		})
	}
}

// --- edit_task ---

func (s *MCPServerSuite) TestEditTaskSuccess() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "PATCH", req.Method)
		require.Contains(s.T(), req.URL.String(), "/api/tasks/42")
		return noContentResponse(http.StatusOK), nil
	}

	text, isError := s.callTool("edit_task", map[string]any{"task_id": float64(42), "prompt": "new prompt"})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "Task 42 updated")
}

func (s *MCPServerSuite) TestEditTaskWithTypeAndSchedule() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "PATCH", req.Method)
		require.Contains(s.T(), req.URL.String(), "/api/tasks/10")
		body, _ := io.ReadAll(req.Body)
		require.Contains(s.T(), string(body), `"type"`)
		require.Contains(s.T(), string(body), `"schedule"`)
		return noContentResponse(http.StatusOK), nil
	}

	text, isError := s.callTool("edit_task", map[string]any{"task_id": float64(10), "type": "interval", "schedule": "30m"})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "Task 10 updated")
}

func (s *MCPServerSuite) TestEditTaskWithAutoDeleteSec() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "PATCH", req.Method)
		require.Contains(s.T(), req.URL.String(), "/api/tasks/42")
		body, _ := io.ReadAll(req.Body)
		require.Contains(s.T(), string(body), `"auto_delete_sec"`)
		require.Contains(s.T(), string(body), `120`)
		return noContentResponse(http.StatusOK), nil
	}

	text, isError := s.callTool("edit_task", map[string]any{"task_id": float64(42), "auto_delete_sec": float64(120)})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "Task 42 updated")
}

func (s *MCPServerSuite) TestEditTaskWithWorktree() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(req.Body)
		require.Contains(s.T(), string(body), `"worktree":true`)
		return noContentResponse(http.StatusOK), nil
	}

	text, isError := s.callTool("edit_task", map[string]any{"task_id": float64(42), "worktree": true})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "Task 42 updated")
}

func (s *MCPServerSuite) TestEditTaskWithOriginBranch() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(req.Body)
		var payload map[string]any
		require.NoError(s.T(), json.Unmarshal(body, &payload))
		require.Equal(s.T(), "main", payload["origin_branch"])
		return noContentResponse(http.StatusOK), nil
	}

	text, isError := s.callTool("edit_task", map[string]any{"task_id": float64(42), "origin_branch": "main"})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "Task 42 updated")
}

func (s *MCPServerSuite) TestEditTaskWithUpdateBeforeRun() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(req.Body)
		var payload map[string]any
		require.NoError(s.T(), json.Unmarshal(body, &payload))
		require.Equal(s.T(), true, payload["update_before_run"])
		return noContentResponse(http.StatusOK), nil
	}

	text, isError := s.callTool("edit_task", map[string]any{"task_id": float64(42), "update_before_run": true})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "Task 42 updated")
}

func (s *MCPServerSuite) TestEditTaskWithWorkflow() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(req.Body)
		var payload map[string]any
		require.NoError(s.T(), json.Unmarshal(body, &payload))
		require.Equal(s.T(), "validate", payload["workflow_name"])
		require.Equal(s.T(), `{"branch":"main"}`, payload["workflow_inputs"])
		return noContentResponse(http.StatusOK), nil
	}

	text, isError := s.callTool("edit_task", map[string]any{
		"task_id":         float64(42),
		"workflow_name":   "validate",
		"workflow_inputs": `{"branch":"main"}`,
	})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "updated")
}

func (s *MCPServerSuite) TestEditTaskScheduleValidation() {
	tests := []struct {
		name     string
		args     map[string]any
		wantText string
	}{
		{"invalid once", map[string]any{"task_id": float64(1), "type": "once", "schedule": "not-valid"}, "RFC3339"},
		{"invalid interval", map[string]any{"task_id": float64(1), "type": "interval", "schedule": "not-valid"}, "time.Duration"},
		{"no fields", map[string]any{"task_id": float64(1)}, "at least one"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			text, isError := s.callTool("edit_task", tt.args)
			require.True(s.T(), isError)
			require.Contains(s.T(), text, tt.wantText)
		})
	}
}

func (s *MCPServerSuite) TestEditTaskValidRFC3339Once() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "PATCH", req.Method)
		return noContentResponse(http.StatusOK), nil
	}

	text, isError := s.callTool("edit_task", map[string]any{"task_id": float64(1), "type": "once", "schedule": "2026-02-09T14:30:00Z"})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "Task 1 updated")
}

func (s *MCPServerSuite) TestEditTaskErrors() {
	tests := []struct {
		name     string
		doFunc   func(*http.Request) (*http.Response, error)
		wantText string
	}{
		{"API error", func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusInternalServerError, "not found"), nil
		}, "API error"},
		{"HTTP error", func(*http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("connection refused")
		}, "calling API"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.httpClient.doFunc = tt.doFunc
			text, isError := s.callTool("edit_task", map[string]any{"task_id": float64(1), "prompt": "new"})
			require.True(s.T(), isError)
			require.Contains(s.T(), text, tt.wantText)
		})
	}
}

// --- toggle_task ---

func (s *MCPServerSuite) TestToggleTaskSuccess() {
	tests := []struct {
		name     string
		taskID   float64
		enabled  bool
		wantText string
	}{
		{"disable", float64(42), false, "Task 42 disabled"},
		{"enable", float64(10), true, "Task 10 enabled"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
				require.Equal(s.T(), "PATCH", req.Method)
				require.Contains(s.T(), req.URL.String(), fmt.Sprintf("/api/tasks/%.0f", tt.taskID))
				return noContentResponse(http.StatusOK), nil
			}
			text, isError := s.callTool("toggle_task", map[string]any{"task_id": tt.taskID, "enabled": tt.enabled})
			require.False(s.T(), isError)
			require.Contains(s.T(), text, tt.wantText)
		})
	}
}

func (s *MCPServerSuite) TestToggleTaskErrors() {
	tests := []struct {
		name     string
		doFunc   func(*http.Request) (*http.Response, error)
		wantText string
	}{
		{"API error", func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusInternalServerError, "not found"), nil
		}, "API error"},
		{"HTTP error", func(*http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("connection refused")
		}, "calling API"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.httpClient.doFunc = tt.doFunc
			text, isError := s.callTool("toggle_task", map[string]any{"task_id": float64(1), "enabled": false})
			require.True(s.T(), isError)
			require.Contains(s.T(), text, tt.wantText)
		})
	}
}

// --- create_channel ---

func (s *MCPServerSuite) TestCreateChannelSuccess() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "POST", req.Method)
		require.Contains(s.T(), req.URL.String(), "/api/channels/create")
		body, _ := io.ReadAll(req.Body)
		require.Contains(s.T(), string(body), `"name":"trial"`)
		require.Contains(s.T(), string(body), `"channel_id":"test-channel"`)
		require.NotContains(s.T(), string(body), `"author_id"`)
		return jsonResponse(http.StatusCreated, `{"channel_id":"ch-new"}`), nil
	}

	text, isError := s.callTool("create_channel", map[string]any{"name": "trial"})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "ID: ch-new")
}

func (s *MCPServerSuite) TestCreateChannelSuccessWithAuthorID() {
	// Re-create the server with an authorID
	s.cleanup()
	s.srv = New("test-channel", "http://localhost:8222", "user-42", s.httpClient, nil)
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1.0.0"}, nil)
	t1, t2 := mcp.NewInMemoryTransports()
	go func() { _ = s.srv.Run(s.ctx, t1) }()
	session, err := client.Connect(s.ctx, t2, nil)
	require.NoError(s.T(), err)
	s.session = session
	s.cleanup = func() { session.Close() }

	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(req.Body)
		require.Contains(s.T(), string(body), `"author_id":"user-42"`)
		return jsonResponse(http.StatusCreated, `{"channel_id":"ch-new"}`), nil
	}

	text, isError := s.callTool("create_channel", map[string]any{"name": "trial"})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "ID: ch-new")
}

func (s *MCPServerSuite) TestCreateChannelEmptyName() {
	text, isError := s.callTool("create_channel", map[string]any{"name": ""})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "name is required")
}

func (s *MCPServerSuite) TestCreateChannelErrors() {
	tests := []struct {
		name     string
		doFunc   func(*http.Request) (*http.Response, error)
		wantText string
	}{
		{"API error", func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusInternalServerError, "create failed"), nil
		}, "API error"},
		{"HTTP error", func(*http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("connection refused")
		}, "calling API"},
		{"invalid response JSON", func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusCreated, "not json"), nil
		}, "decoding response"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.httpClient.doFunc = tt.doFunc
			text, isError := s.callTool("create_channel", map[string]any{"name": "trial"})
			require.True(s.T(), isError)
			require.Contains(s.T(), text, tt.wantText)
		})
	}
}

// --- create_thread ---

func (s *MCPServerSuite) TestCreateThreadSuccessWithAuthorID() {
	// Re-create the server with an authorID
	s.cleanup()
	s.srv = New("test-channel", "http://localhost:8222", "user-42", s.httpClient, nil)
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1.0.0"}, nil)
	t1, t2 := mcp.NewInMemoryTransports()
	go func() { _ = s.srv.Run(s.ctx, t1) }()
	session, err := client.Connect(s.ctx, t2, nil)
	require.NoError(s.T(), err)
	s.session = session
	s.cleanup = func() { session.Close() }

	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "POST", req.Method)
		body, _ := io.ReadAll(req.Body)
		require.Contains(s.T(), string(body), `"author_id":"user-42"`)
		return jsonResponse(http.StatusCreated, `{"thread_id":"thread-1"}`), nil
	}

	text, isError := s.callTool("create_thread", map[string]any{"name": "my-thread", "message": "Do something"})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "ID: thread-1")
}

func (s *MCPServerSuite) TestCreateThreadSuccess() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "POST", req.Method)
		require.Contains(s.T(), req.URL.String(), "/api/threads")
		body, _ := io.ReadAll(req.Body)
		require.Contains(s.T(), string(body), `"channel_id":"test-channel"`)
		require.Contains(s.T(), string(body), `"name":"my-thread"`)
		require.Contains(s.T(), string(body), `"message":"Check the status"`)
		return jsonResponse(http.StatusCreated, `{"thread_id":"thread-1"}`), nil
	}

	text, isError := s.callTool("create_thread", map[string]any{"name": "my-thread", "message": "Check the status"})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "ID: thread-1")
}

func (s *MCPServerSuite) TestCreateThreadEmptyMessage() {
	text, isError := s.callTool("create_thread", map[string]any{"name": "my-thread", "message": ""})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "message is required")
}

func (s *MCPServerSuite) TestCreateThreadSuccessWithMessage() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "POST", req.Method)
		body, _ := io.ReadAll(req.Body)
		require.Contains(s.T(), string(body), `"channel_id":"test-channel"`)
		require.Contains(s.T(), string(body), `"name":"my-thread"`)
		require.Contains(s.T(), string(body), `"message":"Do the task"`)
		return jsonResponse(http.StatusCreated, `{"thread_id":"thread-1"}`), nil
	}

	text, isError := s.callTool("create_thread", map[string]any{"name": "my-thread", "message": "Do the task"})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "ID: thread-1")
}

func (s *MCPServerSuite) TestCreateThreadEmptyName() {
	text, isError := s.callTool("create_thread", map[string]any{"name": "", "message": "Do something"})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "name is required")
}

func (s *MCPServerSuite) TestCreateThreadErrors() {
	tests := []struct {
		name     string
		doFunc   func(*http.Request) (*http.Response, error)
		wantText string
	}{
		{"API error", func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusInternalServerError, "parent not found"), nil
		}, "API error"},
		{"HTTP error", func(*http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("connection refused")
		}, "calling API"},
		{"invalid response JSON", func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusCreated, "not json"), nil
		}, "decoding response"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.httpClient.doFunc = tt.doFunc
			text, isError := s.callTool("create_thread", map[string]any{"name": "my-thread", "message": "Do something"})
			require.True(s.T(), isError)
			require.Contains(s.T(), text, tt.wantText)
		})
	}
}

// --- delete_thread ---

func (s *MCPServerSuite) TestDeleteThreadSuccess() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "DELETE", req.Method)
		require.Contains(s.T(), req.URL.String(), "/api/threads/thread-1")
		return noContentResponse(http.StatusNoContent), nil
	}

	text, isError := s.callTool("delete_thread", map[string]any{"thread_id": "thread-1"})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "Thread thread-1 deleted")
}

func (s *MCPServerSuite) TestDeleteThreadEmptyID() {
	text, isError := s.callTool("delete_thread", map[string]any{"thread_id": ""})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "thread_id is required")
}

func (s *MCPServerSuite) TestDeleteThreadErrors() {
	tests := []struct {
		name     string
		doFunc   func(*http.Request) (*http.Response, error)
		wantText string
	}{
		{"API error", func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusInternalServerError, "thread not found"), nil
		}, "API error"},
		{"HTTP error", func(*http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("connection refused")
		}, "calling API"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.httpClient.doFunc = tt.doFunc
			text, isError := s.callTool("delete_thread", map[string]any{"thread_id": "thread-1"})
			require.True(s.T(), isError)
			require.Contains(s.T(), text, tt.wantText)
		})
	}
}

// --- search_channels ---

func (s *MCPServerSuite) TestSearchChannelsSuccess() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "GET", req.Method)
		require.Contains(s.T(), req.URL.String(), "/api/channels")
		return jsonResponse(http.StatusOK, `[{"channel_id":"ch-1","name":"general","dir_path":"/home/user/general","parent_id":"","active":true},{"channel_id":"ch-2","name":"thread-1","dir_path":"/home/user/general","parent_id":"ch-1","active":true}]`), nil
	}

	text, isError := s.callTool("search_channels", map[string]any{})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "general")
	require.Contains(s.T(), text, "[channel]")
	require.Contains(s.T(), text, "thread-1")
	require.Contains(s.T(), text, "[thread]")
}

func (s *MCPServerSuite) TestSearchChannelsWithQuery() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "GET", req.Method)
		require.Contains(s.T(), req.URL.String(), "query=gen")
		return jsonResponse(http.StatusOK, `[{"channel_id":"ch-1","name":"general","dir_path":"/home/user/general","parent_id":"","active":true}]`), nil
	}

	text, isError := s.callTool("search_channels", map[string]any{"query": "gen"})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "general")
}

func (s *MCPServerSuite) TestSearchChannelsEmpty() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `[]`), nil
	}

	text, isError := s.callTool("search_channels", map[string]any{})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "No channels found")
}

func (s *MCPServerSuite) TestSearchChannelsErrors() {
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
		{"invalid response JSON", func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusOK, "not json"), nil
		}, "decoding response"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.httpClient.doFunc = tt.doFunc
			text, isError := s.callTool("search_channels", map[string]any{})
			require.True(s.T(), isError)
			require.Contains(s.T(), text, tt.wantText)
		})
	}
}

// --- send_message ---

func (s *MCPServerSuite) TestSendMessageSuccess() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "POST", req.Method)
		require.Contains(s.T(), req.URL.String(), "/api/messages")
		body, _ := io.ReadAll(req.Body)
		require.Contains(s.T(), string(body), `"channel_id":"ch-1"`)
		require.Contains(s.T(), string(body), `"content":"hello world"`)
		return noContentResponse(http.StatusNoContent), nil
	}

	text, isError := s.callTool("send_message", map[string]any{"channel_id": "ch-1", "content": "hello world"})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "Message sent successfully")
}

func (s *MCPServerSuite) TestSendMessageValidation() {
	tests := []struct {
		name     string
		args     map[string]any
		wantText string
	}{
		{"empty channel_id", map[string]any{"channel_id": "", "content": "hello"}, "channel_id is required"},
		{"empty content", map[string]any{"channel_id": "ch-1", "content": ""}, "content is required"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			text, isError := s.callTool("send_message", tt.args)
			require.True(s.T(), isError)
			require.Contains(s.T(), text, tt.wantText)
		})
	}
}

func (s *MCPServerSuite) TestSendMessageErrors() {
	tests := []struct {
		name     string
		doFunc   func(*http.Request) (*http.Response, error)
		wantText string
	}{
		{"API error", func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusInternalServerError, "send failed"), nil
		}, "API error"},
		{"HTTP error", func(*http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("connection refused")
		}, "calling API"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.httpClient.doFunc = tt.doFunc
			text, isError := s.callTool("send_message", map[string]any{"channel_id": "ch-1", "content": "hello"})
			require.True(s.T(), isError)
			require.Contains(s.T(), text, tt.wantText)
		})
	}
}

// --- get_readme ---

func (s *MCPServerSuite) TestGetReadmeSuccess() {
	text, isError := s.callTool("get_readme", map[string]any{})
	require.False(s.T(), isError)
	require.NotEmpty(s.T(), text)
}

// --- search_memory / index_memory ---

// MCPMemorySuite tests memory tools separately because they need a server created with WithMemoryAPI.
type MCPMemorySuite struct {
	suite.Suite
	httpClient *mockHTTPClient
	srv        *Server
	ctx        context.Context
	session    *mcp.ClientSession
	cleanup    func()
}

func TestMCPMemorySuite(t *testing.T) {
	suite.Run(t, new(MCPMemorySuite))
}

func (s *MCPMemorySuite) SetupTest() {
	s.httpClient = &mockHTTPClient{}
	s.srv = New("test-channel", "http://localhost:8222", "", s.httpClient, nil,
		WithMemoryAPI("/tmp/project"),
	)
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

func (s *MCPMemorySuite) TearDownTest() {
	if s.cleanup != nil {
		s.cleanup()
	}
}

// callTool is a helper that calls a tool and returns (text, isError).
func (s *MCPMemorySuite) callTool(name string, args map[string]any) (string, bool) {
	s.T().Helper()
	res, err := s.session.CallTool(s.ctx, &mcp.CallToolParams{
		Name:      name,
		Arguments: args,
	})
	require.NoError(s.T(), err)
	require.Len(s.T(), res.Content, 1)
	return res.Content[0].(*mcp.TextContent).Text, res.IsError
}

func (s *MCPMemorySuite) TestListToolsIncludesMemory() {
	res, err := s.session.ListTools(s.ctx, nil)
	require.NoError(s.T(), err)
	require.Len(s.T(), res.Tools, 27) // 12 base + 2 memory + 2 playground + 1 shortcut + 10 quality

	names := make(map[string]bool)
	for _, t := range res.Tools {
		names[t.Name] = true
	}
	require.True(s.T(), names["search_memory"])
	require.True(s.T(), names["index_memory"])
}

func (s *MCPMemorySuite) TestSearchMemorySuccess() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "POST", req.Method)
		require.Contains(s.T(), req.URL.String(), "/api/memory/search")
		body, _ := io.ReadAll(req.Body)
		require.Contains(s.T(), string(body), `"query":"docker cleanup"`)
		require.Contains(s.T(), string(body), `"top_k":3`)
		require.Contains(s.T(), string(body), `"dir_path":"/tmp/project"`)
		return jsonResponse(http.StatusOK, `{"results":[{"file_path":"/tmp/memory/MEMORY.md","content":"Container cleanup tips","score":0.95},{"file_path":"/tmp/memory/debugging.md","content":"Debug notes","score":0.82}]}`), nil
	}

	text, isError := s.callTool("search_memory", map[string]any{
		"query": "docker cleanup",
		"top_k": float64(3),
	})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "score: 0.950")
	require.Contains(s.T(), text, "MEMORY.md")
	require.Contains(s.T(), text, "Container cleanup tips")
	require.Contains(s.T(), text, "debugging.md")
}

func (s *MCPMemorySuite) TestSearchMemoryEmptyResults() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"results":[]}`), nil
	}

	text, isError := s.callTool("search_memory", map[string]any{"query": "nonexistent topic"})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "No results found")
}

func (s *MCPMemorySuite) TestSearchMemoryEmptyQuery() {
	text, isError := s.callTool("search_memory", map[string]any{"query": ""})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "query is required")
}

func (s *MCPMemorySuite) TestSearchMemoryErrors() {
	tests := []struct {
		name     string
		doFunc   func(*http.Request) (*http.Response, error)
		wantText string
	}{
		{"API error", func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusInternalServerError, "indexer error"), nil
		}, "API error"},
		{"HTTP error", func(*http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("connection refused")
		}, "calling API"},
		{"invalid response JSON", func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusOK, "not json"), nil
		}, "decoding response"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.httpClient.doFunc = tt.doFunc
			text, isError := s.callTool("search_memory", map[string]any{"query": "test"})
			require.True(s.T(), isError)
			require.Contains(s.T(), text, tt.wantText)
		})
	}
}

func (s *MCPMemorySuite) TestSearchMemorySingleResult() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"results":[{"file_path":"/tmp/memory/notes.md","content":"Some notes","score":0.75}]}`), nil
	}

	text, isError := s.callTool("search_memory", map[string]any{"query": "notes"})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "notes.md")
	require.Contains(s.T(), text, "Some notes")
}

func (s *MCPMemorySuite) TestSearchMemoryResultWithoutContent() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"results":[{"file_path":"/tmp/memory/MEMORY.md","content":"","score":0.90}]}`), nil
	}

	text, isError := s.callTool("search_memory", map[string]any{"query": "memory"})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "MEMORY.md")
	require.Contains(s.T(), text, "score: 0.900")
}

func (s *MCPMemorySuite) TestIndexMemorySuccess() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "POST", req.Method)
		require.Contains(s.T(), req.URL.String(), "/api/memory/index")
		body, _ := io.ReadAll(req.Body)
		require.Contains(s.T(), string(body), `"dir_path":"/tmp/project"`)
		return jsonResponse(http.StatusOK, `{"count":15}`), nil
	}

	text, isError := s.callTool("index_memory", map[string]any{})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "Indexed 15 chunks")
}

func (s *MCPMemorySuite) TestIndexMemoryErrors() {
	tests := []struct {
		name     string
		doFunc   func(*http.Request) (*http.Response, error)
		wantText string
	}{
		{"API error", func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusInternalServerError, "disk full"), nil
		}, "API error"},
		{"HTTP error", func(*http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("connection refused")
		}, "calling API"},
		{"invalid response JSON", func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusOK, "not json"), nil
		}, "decoding response"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.httpClient.doFunc = tt.doFunc
			text, isError := s.callTool("index_memory", map[string]any{})
			require.True(s.T(), isError)
			require.Contains(s.T(), text, tt.wantText)
		})
	}
}

func (s *MCPMemorySuite) TestWithMemoryAPIOption() {
	require.True(s.T(), s.srv.memoryEnabled)
	require.Equal(s.T(), "/tmp/project", s.srv.dirPath)
}

func (s *MCPMemorySuite) TestDirPath() {
	require.Equal(s.T(), "/tmp/project", s.srv.DirPath())
}

// MCPMemoryChannelIDSuite tests memory tools when only channel_id is available (no dirPath).
type MCPMemoryChannelIDSuite struct {
	suite.Suite
	httpClient *mockHTTPClient
	srv        *Server
	ctx        context.Context
	session    *mcp.ClientSession
	cleanup    func()
}

func TestMCPMemoryChannelIDSuite(t *testing.T) {
	suite.Run(t, new(MCPMemoryChannelIDSuite))
}

func (s *MCPMemoryChannelIDSuite) SetupTest() {
	s.httpClient = &mockHTTPClient{}
	s.srv = New("test-channel", "http://localhost:8222", "", s.httpClient, nil,
		WithMemoryAPI(""),
	)
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

func (s *MCPMemoryChannelIDSuite) TearDownTest() {
	if s.cleanup != nil {
		s.cleanup()
	}
}

func (s *MCPMemoryChannelIDSuite) TestMemoryEnabledWithEmptyDirPath() {
	require.True(s.T(), s.srv.memoryEnabled)
	require.Empty(s.T(), s.srv.dirPath)
}

func (s *MCPMemoryChannelIDSuite) TestListToolsIncludesMemory() {
	res, err := s.session.ListTools(s.ctx, nil)
	require.NoError(s.T(), err)
	require.Len(s.T(), res.Tools, 27)

	names := make(map[string]bool)
	for _, t := range res.Tools {
		names[t.Name] = true
	}
	require.True(s.T(), names["search_memory"])
	require.True(s.T(), names["index_memory"])
}

func (s *MCPMemoryChannelIDSuite) TestSearchMemorySendsChannelID() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "POST", req.Method)
		require.Contains(s.T(), req.URL.String(), "/api/memory/search")
		body, _ := io.ReadAll(req.Body)
		require.Contains(s.T(), string(body), `"channel_id":"test-channel"`)
		require.NotContains(s.T(), string(body), `"dir_path"`)
		return jsonResponse(http.StatusOK, `{"results":[]}`), nil
	}

	res, err := s.session.CallTool(s.ctx, &mcp.CallToolParams{
		Name:      "search_memory",
		Arguments: map[string]any{"query": "test"},
	})
	require.NoError(s.T(), err)
	require.False(s.T(), res.IsError)
}

func (s *MCPMemoryChannelIDSuite) TestIndexMemorySendsChannelID() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "POST", req.Method)
		require.Contains(s.T(), req.URL.String(), "/api/memory/index")
		body, _ := io.ReadAll(req.Body)
		require.Contains(s.T(), string(body), `"channel_id":"test-channel"`)
		require.NotContains(s.T(), string(body), `"dir_path"`)
		return jsonResponse(http.StatusOK, `{"count":5}`), nil
	}

	res, err := s.session.CallTool(s.ctx, &mcp.CallToolParams{
		Name:      "index_memory",
		Arguments: map[string]any{},
	})
	require.NoError(s.T(), err)
	require.False(s.T(), res.IsError)
}

// --- doRequest ---

func (s *MCPServerSuite) TestDoRequestInvalidMethod() {
	_, _, err := s.srv.doRequest("INVALID METHOD", "http://localhost", nil)
	require.Error(s.T(), err)
}

// --- errorResult ---

func (s *MCPServerSuite) TestErrorResult() {
	result := errorResult("test error")
	require.True(s.T(), result.IsError)
	require.Len(s.T(), result.Content, 1)
	require.Equal(s.T(), "test error", result.Content[0].(*mcp.TextContent).Text)
}
