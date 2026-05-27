package mcpserver

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/stretchr/testify/require"
)

// --- ListTools ---

func (s *MCPServerSuite) TestListTools() {
	res, err := s.session.ListTools(s.ctx, nil)
	require.NoError(s.T(), err)
	require.Len(s.T(), res.Tools, 29) // 13 base + 2 playground + 2 shortcut + 12 quality

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
	require.True(s.T(), names["create_worktree_thread"])
	require.True(s.T(), names["delete_thread"])
	require.True(s.T(), names["search_channels"])
	require.True(s.T(), names["send_message"])
	require.True(s.T(), names["get_readme"])
	require.True(s.T(), names["playground"])
	require.True(s.T(), names["playground_file"])
	require.True(s.T(), names["prompt_shortcut"])
	require.True(s.T(), names["bash_shortcut"])
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
	require.True(s.T(), names["quality_complexity"])
	require.True(s.T(), names["quality_clones"])
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
