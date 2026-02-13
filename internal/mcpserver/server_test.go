package mcpserver

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"

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

func (s *MCPServerSuite) TestNew() {
	require.NotNil(s.T(), s.srv)
	require.Equal(s.T(), "test-channel", s.srv.channelID)
	require.Equal(s.T(), "http://localhost:8222", s.srv.apiURL)
	require.NotNil(s.T(), s.srv.mcpServer)
}

func (s *MCPServerSuite) TestMCPServer() {
	require.Equal(s.T(), s.srv.mcpServer, s.srv.MCPServer())
}

// --- ListTools ---

func (s *MCPServerSuite) TestListTools() {
	res, err := s.session.ListTools(s.ctx, nil)
	require.NoError(s.T(), err)
	require.Len(s.T(), res.Tools, 10)

	names := make(map[string]bool)
	for _, t := range res.Tools {
		names[t.Name] = true
	}
	require.True(s.T(), names["schedule_task"])
	require.True(s.T(), names["list_tasks"])
	require.True(s.T(), names["cancel_task"])
	require.True(s.T(), names["toggle_task"])
	require.True(s.T(), names["edit_task"])
	require.True(s.T(), names["create_channel"])
	require.True(s.T(), names["create_thread"])
	require.True(s.T(), names["delete_thread"])
	require.True(s.T(), names["search_channels"])
	require.True(s.T(), names["send_message"])
}

// --- schedule_task ---

func (s *MCPServerSuite) TestScheduleTaskSuccess() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "POST", req.Method)
		require.Contains(s.T(), req.URL.String(), "/api/tasks")
		return jsonResponse(http.StatusCreated, `{"id":42}`), nil
	}

	res, err := s.session.CallTool(s.ctx, &mcp.CallToolParams{
		Name: "schedule_task",
		Arguments: map[string]any{
			"schedule": "0 9 * * *",
			"type":     "cron",
			"prompt":   "check standup",
		},
	})
	require.NoError(s.T(), err)
	require.False(s.T(), res.IsError)
	require.Len(s.T(), res.Content, 1)
	require.Contains(s.T(), res.Content[0].(*mcp.TextContent).Text, "ID: 42")
}

func (s *MCPServerSuite) TestScheduleTaskAPIError() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusInternalServerError, "bad schedule"), nil
	}

	res, err := s.session.CallTool(s.ctx, &mcp.CallToolParams{
		Name: "schedule_task",
		Arguments: map[string]any{
			"schedule": "bad",
			"type":     "cron",
			"prompt":   "test",
		},
	})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), res.Content[0].(*mcp.TextContent).Text, "API error")
}

func (s *MCPServerSuite) TestScheduleTaskInvalidOnce() {
	res, err := s.session.CallTool(s.ctx, &mcp.CallToolParams{
		Name: "schedule_task",
		Arguments: map[string]any{
			"schedule": "5m",
			"type":     "once",
			"prompt":   "test",
		},
	})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), res.Content[0].(*mcp.TextContent).Text, "invalid schedule for type")
	require.Contains(s.T(), res.Content[0].(*mcp.TextContent).Text, "RFC3339")
}

func (s *MCPServerSuite) TestScheduleTaskValidRFC3339Once() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusCreated, `{"id":10}`), nil
	}

	res, err := s.session.CallTool(s.ctx, &mcp.CallToolParams{
		Name: "schedule_task",
		Arguments: map[string]any{
			"schedule": "2026-02-09T14:30:00Z",
			"type":     "once",
			"prompt":   "test",
		},
	})
	require.NoError(s.T(), err)
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), res.Content[0].(*mcp.TextContent).Text, "ID: 10")
}

func (s *MCPServerSuite) TestScheduleTaskInvalidDurationInterval() {
	res, err := s.session.CallTool(s.ctx, &mcp.CallToolParams{
		Name: "schedule_task",
		Arguments: map[string]any{
			"schedule": "*/5 * * * *",
			"type":     "interval",
			"prompt":   "test",
		},
	})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), res.Content[0].(*mcp.TextContent).Text, "invalid schedule for type")
	require.Contains(s.T(), res.Content[0].(*mcp.TextContent).Text, "time.Duration")
}

func (s *MCPServerSuite) TestScheduleTaskValidDurationInterval() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusCreated, `{"id":2}`), nil
	}

	res, err := s.session.CallTool(s.ctx, &mcp.CallToolParams{
		Name: "schedule_task",
		Arguments: map[string]any{
			"schedule": "1h",
			"type":     "interval",
			"prompt":   "test",
		},
	})
	require.NoError(s.T(), err)
	require.False(s.T(), res.IsError)
}

func (s *MCPServerSuite) TestScheduleTaskHTTPError() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("connection refused")
	}

	res, err := s.session.CallTool(s.ctx, &mcp.CallToolParams{
		Name: "schedule_task",
		Arguments: map[string]any{
			"schedule": "0 9 * * *",
			"type":     "cron",
			"prompt":   "test",
		},
	})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), res.Content[0].(*mcp.TextContent).Text, "calling API")
}

func (s *MCPServerSuite) TestScheduleTaskInvalidResponseJSON() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusCreated, "not json"), nil
	}

	res, err := s.session.CallTool(s.ctx, &mcp.CallToolParams{
		Name: "schedule_task",
		Arguments: map[string]any{
			"schedule": "0 9 * * *",
			"type":     "cron",
			"prompt":   "test",
		},
	})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), res.Content[0].(*mcp.TextContent).Text, "decoding response")
}

// --- list_tasks ---

func (s *MCPServerSuite) TestListTasksSuccess() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "GET", req.Method)
		require.Contains(s.T(), req.URL.String(), "channel_id=test-channel")
		return jsonResponse(http.StatusOK, `[{"id":1,"schedule":"0 9 * * *","type":"cron","prompt":"standup","enabled":true,"next_run_at":"2025-01-01T09:00:00Z"}]`), nil
	}

	res, err := s.session.CallTool(s.ctx, &mcp.CallToolParams{
		Name:      "list_tasks",
		Arguments: map[string]any{},
	})
	require.NoError(s.T(), err)
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), res.Content[0].(*mcp.TextContent).Text, "ID 1")
	require.Contains(s.T(), res.Content[0].(*mcp.TextContent).Text, "standup")
}

func (s *MCPServerSuite) TestListTasksEmpty() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `[]`), nil
	}

	res, err := s.session.CallTool(s.ctx, &mcp.CallToolParams{
		Name:      "list_tasks",
		Arguments: map[string]any{},
	})
	require.NoError(s.T(), err)
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), res.Content[0].(*mcp.TextContent).Text, "No scheduled tasks")
}

func (s *MCPServerSuite) TestListTasksAPIError() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusInternalServerError, "db error"), nil
	}

	res, err := s.session.CallTool(s.ctx, &mcp.CallToolParams{
		Name:      "list_tasks",
		Arguments: map[string]any{},
	})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), res.Content[0].(*mcp.TextContent).Text, "API error")
}

func (s *MCPServerSuite) TestListTasksHTTPError() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("connection refused")
	}

	res, err := s.session.CallTool(s.ctx, &mcp.CallToolParams{
		Name:      "list_tasks",
		Arguments: map[string]any{},
	})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), res.Content[0].(*mcp.TextContent).Text, "calling API")
}

func (s *MCPServerSuite) TestListTasksInvalidResponseJSON() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, "not json"), nil
	}

	res, err := s.session.CallTool(s.ctx, &mcp.CallToolParams{
		Name:      "list_tasks",
		Arguments: map[string]any{},
	})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), res.Content[0].(*mcp.TextContent).Text, "decoding response")
}

// --- cancel_task ---

func (s *MCPServerSuite) TestCancelTaskSuccess() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "DELETE", req.Method)
		require.Contains(s.T(), req.URL.String(), "/api/tasks/42")
		return &http.Response{
			StatusCode: http.StatusNoContent,
			Body:       io.NopCloser(bytes.NewReader(nil)),
		}, nil
	}

	res, err := s.session.CallTool(s.ctx, &mcp.CallToolParams{
		Name:      "cancel_task",
		Arguments: map[string]any{"task_id": float64(42)},
	})
	require.NoError(s.T(), err)
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), res.Content[0].(*mcp.TextContent).Text, "Task 42 cancelled")
}

func (s *MCPServerSuite) TestCancelTaskAPIError() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusInternalServerError, "not found"), nil
	}

	res, err := s.session.CallTool(s.ctx, &mcp.CallToolParams{
		Name:      "cancel_task",
		Arguments: map[string]any{"task_id": float64(99)},
	})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), res.Content[0].(*mcp.TextContent).Text, "API error")
}

func (s *MCPServerSuite) TestCancelTaskHTTPError() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("connection refused")
	}

	res, err := s.session.CallTool(s.ctx, &mcp.CallToolParams{
		Name:      "cancel_task",
		Arguments: map[string]any{"task_id": float64(1)},
	})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), res.Content[0].(*mcp.TextContent).Text, "calling API")
}

// --- edit_task ---

func (s *MCPServerSuite) TestEditTaskSuccess() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "PATCH", req.Method)
		require.Contains(s.T(), req.URL.String(), "/api/tasks/42")
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(nil)),
		}, nil
	}

	res, err := s.session.CallTool(s.ctx, &mcp.CallToolParams{
		Name:      "edit_task",
		Arguments: map[string]any{"task_id": float64(42), "prompt": "new prompt"},
	})
	require.NoError(s.T(), err)
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), res.Content[0].(*mcp.TextContent).Text, "Task 42 updated")
}

func (s *MCPServerSuite) TestEditTaskAPIError() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusInternalServerError, "not found"), nil
	}

	res, err := s.session.CallTool(s.ctx, &mcp.CallToolParams{
		Name:      "edit_task",
		Arguments: map[string]any{"task_id": float64(99), "schedule": "bad"},
	})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), res.Content[0].(*mcp.TextContent).Text, "API error")
}

func (s *MCPServerSuite) TestEditTaskHTTPError() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("connection refused")
	}

	res, err := s.session.CallTool(s.ctx, &mcp.CallToolParams{
		Name:      "edit_task",
		Arguments: map[string]any{"task_id": float64(1), "prompt": "new"},
	})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), res.Content[0].(*mcp.TextContent).Text, "calling API")
}

func (s *MCPServerSuite) TestEditTaskWithTypeAndSchedule() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "PATCH", req.Method)
		require.Contains(s.T(), req.URL.String(), "/api/tasks/10")
		body, _ := io.ReadAll(req.Body)
		require.Contains(s.T(), string(body), `"type"`)
		require.Contains(s.T(), string(body), `"schedule"`)
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(nil)),
		}, nil
	}

	res, err := s.session.CallTool(s.ctx, &mcp.CallToolParams{
		Name:      "edit_task",
		Arguments: map[string]any{"task_id": float64(10), "type": "interval", "schedule": "30m"},
	})
	require.NoError(s.T(), err)
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), res.Content[0].(*mcp.TextContent).Text, "Task 10 updated")
}

func (s *MCPServerSuite) TestEditTaskInvalidOnce() {
	res, err := s.session.CallTool(s.ctx, &mcp.CallToolParams{
		Name:      "edit_task",
		Arguments: map[string]any{"task_id": float64(1), "type": "once", "schedule": "not-valid"},
	})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), res.Content[0].(*mcp.TextContent).Text, "invalid schedule for type")
	require.Contains(s.T(), res.Content[0].(*mcp.TextContent).Text, "RFC3339")
}

func (s *MCPServerSuite) TestEditTaskValidRFC3339Once() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "PATCH", req.Method)
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(nil)),
		}, nil
	}

	res, err := s.session.CallTool(s.ctx, &mcp.CallToolParams{
		Name:      "edit_task",
		Arguments: map[string]any{"task_id": float64(1), "type": "once", "schedule": "2026-02-09T14:30:00Z"},
	})
	require.NoError(s.T(), err)
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), res.Content[0].(*mcp.TextContent).Text, "Task 1 updated")
}

func (s *MCPServerSuite) TestEditTaskInvalidDurationInterval() {
	res, err := s.session.CallTool(s.ctx, &mcp.CallToolParams{
		Name:      "edit_task",
		Arguments: map[string]any{"task_id": float64(1), "type": "interval", "schedule": "not-valid"},
	})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), res.Content[0].(*mcp.TextContent).Text, "invalid schedule for type")
	require.Contains(s.T(), res.Content[0].(*mcp.TextContent).Text, "time.Duration")
}

func (s *MCPServerSuite) TestEditTaskNoFields() {
	res, err := s.session.CallTool(s.ctx, &mcp.CallToolParams{
		Name:      "edit_task",
		Arguments: map[string]any{"task_id": float64(1)},
	})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), res.Content[0].(*mcp.TextContent).Text, "at least one")
}

// --- toggle_task ---

func (s *MCPServerSuite) TestToggleTaskDisableSuccess() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "PATCH", req.Method)
		require.Contains(s.T(), req.URL.String(), "/api/tasks/42")
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(nil)),
		}, nil
	}

	res, err := s.session.CallTool(s.ctx, &mcp.CallToolParams{
		Name:      "toggle_task",
		Arguments: map[string]any{"task_id": float64(42), "enabled": false},
	})
	require.NoError(s.T(), err)
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), res.Content[0].(*mcp.TextContent).Text, "Task 42 disabled")
}

func (s *MCPServerSuite) TestToggleTaskEnableSuccess() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "PATCH", req.Method)
		require.Contains(s.T(), req.URL.String(), "/api/tasks/10")
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(bytes.NewReader(nil)),
		}, nil
	}

	res, err := s.session.CallTool(s.ctx, &mcp.CallToolParams{
		Name:      "toggle_task",
		Arguments: map[string]any{"task_id": float64(10), "enabled": true},
	})
	require.NoError(s.T(), err)
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), res.Content[0].(*mcp.TextContent).Text, "Task 10 enabled")
}

func (s *MCPServerSuite) TestToggleTaskAPIError() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusInternalServerError, "not found"), nil
	}

	res, err := s.session.CallTool(s.ctx, &mcp.CallToolParams{
		Name:      "toggle_task",
		Arguments: map[string]any{"task_id": float64(99), "enabled": true},
	})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), res.Content[0].(*mcp.TextContent).Text, "API error")
}

func (s *MCPServerSuite) TestToggleTaskHTTPError() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("connection refused")
	}

	res, err := s.session.CallTool(s.ctx, &mcp.CallToolParams{
		Name:      "toggle_task",
		Arguments: map[string]any{"task_id": float64(1), "enabled": false},
	})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), res.Content[0].(*mcp.TextContent).Text, "calling API")
}

// --- create_channel ---

func (s *MCPServerSuite) TestCreateChannelSuccess() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "POST", req.Method)
		require.Contains(s.T(), req.URL.String(), "/api/channels/create")
		body, _ := io.ReadAll(req.Body)
		require.Contains(s.T(), string(body), `"name":"trial"`)
		require.NotContains(s.T(), string(body), `"author_id"`)
		return jsonResponse(http.StatusCreated, `{"channel_id":"ch-new"}`), nil
	}

	res, err := s.session.CallTool(s.ctx, &mcp.CallToolParams{
		Name:      "create_channel",
		Arguments: map[string]any{"name": "trial"},
	})
	require.NoError(s.T(), err)
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), res.Content[0].(*mcp.TextContent).Text, "ID: ch-new")
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

	res, err := s.session.CallTool(s.ctx, &mcp.CallToolParams{
		Name:      "create_channel",
		Arguments: map[string]any{"name": "trial"},
	})
	require.NoError(s.T(), err)
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), res.Content[0].(*mcp.TextContent).Text, "ID: ch-new")
}

func (s *MCPServerSuite) TestCreateChannelEmptyName() {
	res, err := s.session.CallTool(s.ctx, &mcp.CallToolParams{
		Name:      "create_channel",
		Arguments: map[string]any{"name": ""},
	})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), res.Content[0].(*mcp.TextContent).Text, "name is required")
}

func (s *MCPServerSuite) TestCreateChannelAPIError() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusInternalServerError, "create failed"), nil
	}

	res, err := s.session.CallTool(s.ctx, &mcp.CallToolParams{
		Name:      "create_channel",
		Arguments: map[string]any{"name": "trial"},
	})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), res.Content[0].(*mcp.TextContent).Text, "API error")
}

func (s *MCPServerSuite) TestCreateChannelHTTPError() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("connection refused")
	}

	res, err := s.session.CallTool(s.ctx, &mcp.CallToolParams{
		Name:      "create_channel",
		Arguments: map[string]any{"name": "trial"},
	})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), res.Content[0].(*mcp.TextContent).Text, "calling API")
}

func (s *MCPServerSuite) TestCreateChannelInvalidResponseJSON() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusCreated, "not json"), nil
	}

	res, err := s.session.CallTool(s.ctx, &mcp.CallToolParams{
		Name:      "create_channel",
		Arguments: map[string]any{"name": "trial"},
	})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), res.Content[0].(*mcp.TextContent).Text, "decoding response")
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

	res, err := s.session.CallTool(s.ctx, &mcp.CallToolParams{
		Name:      "create_thread",
		Arguments: map[string]any{"name": "my-thread"},
	})
	require.NoError(s.T(), err)
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), res.Content[0].(*mcp.TextContent).Text, "ID: thread-1")
}

func (s *MCPServerSuite) TestCreateThreadSuccess() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "POST", req.Method)
		require.Contains(s.T(), req.URL.String(), "/api/threads")
		body, _ := io.ReadAll(req.Body)
		require.Contains(s.T(), string(body), `"channel_id":"test-channel"`)
		require.Contains(s.T(), string(body), `"name":"my-thread"`)
		require.NotContains(s.T(), string(body), `"message"`)
		return jsonResponse(http.StatusCreated, `{"thread_id":"thread-1"}`), nil
	}

	res, err := s.session.CallTool(s.ctx, &mcp.CallToolParams{
		Name:      "create_thread",
		Arguments: map[string]any{"name": "my-thread"},
	})
	require.NoError(s.T(), err)
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), res.Content[0].(*mcp.TextContent).Text, "ID: thread-1")
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

	res, err := s.session.CallTool(s.ctx, &mcp.CallToolParams{
		Name:      "create_thread",
		Arguments: map[string]any{"name": "my-thread", "message": "Do the task"},
	})
	require.NoError(s.T(), err)
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), res.Content[0].(*mcp.TextContent).Text, "ID: thread-1")
}

func (s *MCPServerSuite) TestCreateThreadEmptyName() {
	res, err := s.session.CallTool(s.ctx, &mcp.CallToolParams{
		Name:      "create_thread",
		Arguments: map[string]any{"name": ""},
	})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), res.Content[0].(*mcp.TextContent).Text, "name is required")
}

func (s *MCPServerSuite) TestCreateThreadAPIError() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusInternalServerError, "parent not found"), nil
	}

	res, err := s.session.CallTool(s.ctx, &mcp.CallToolParams{
		Name:      "create_thread",
		Arguments: map[string]any{"name": "my-thread"},
	})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), res.Content[0].(*mcp.TextContent).Text, "API error")
}

func (s *MCPServerSuite) TestCreateThreadHTTPError() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("connection refused")
	}

	res, err := s.session.CallTool(s.ctx, &mcp.CallToolParams{
		Name:      "create_thread",
		Arguments: map[string]any{"name": "my-thread"},
	})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), res.Content[0].(*mcp.TextContent).Text, "calling API")
}

func (s *MCPServerSuite) TestCreateThreadInvalidResponseJSON() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusCreated, "not json"), nil
	}

	res, err := s.session.CallTool(s.ctx, &mcp.CallToolParams{
		Name:      "create_thread",
		Arguments: map[string]any{"name": "my-thread"},
	})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), res.Content[0].(*mcp.TextContent).Text, "decoding response")
}

// --- delete_thread ---

func (s *MCPServerSuite) TestDeleteThreadSuccess() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "DELETE", req.Method)
		require.Contains(s.T(), req.URL.String(), "/api/threads/thread-1")
		return &http.Response{
			StatusCode: http.StatusNoContent,
			Body:       io.NopCloser(bytes.NewReader(nil)),
		}, nil
	}

	res, err := s.session.CallTool(s.ctx, &mcp.CallToolParams{
		Name:      "delete_thread",
		Arguments: map[string]any{"thread_id": "thread-1"},
	})
	require.NoError(s.T(), err)
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), res.Content[0].(*mcp.TextContent).Text, "Thread thread-1 deleted")
}

func (s *MCPServerSuite) TestDeleteThreadEmptyID() {
	res, err := s.session.CallTool(s.ctx, &mcp.CallToolParams{
		Name:      "delete_thread",
		Arguments: map[string]any{"thread_id": ""},
	})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), res.Content[0].(*mcp.TextContent).Text, "thread_id is required")
}

func (s *MCPServerSuite) TestDeleteThreadAPIError() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusInternalServerError, "thread not found"), nil
	}

	res, err := s.session.CallTool(s.ctx, &mcp.CallToolParams{
		Name:      "delete_thread",
		Arguments: map[string]any{"thread_id": "thread-1"},
	})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), res.Content[0].(*mcp.TextContent).Text, "API error")
}

func (s *MCPServerSuite) TestDeleteThreadHTTPError() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("connection refused")
	}

	res, err := s.session.CallTool(s.ctx, &mcp.CallToolParams{
		Name:      "delete_thread",
		Arguments: map[string]any{"thread_id": "thread-1"},
	})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), res.Content[0].(*mcp.TextContent).Text, "calling API")
}

// --- search_channels ---

func (s *MCPServerSuite) TestSearchChannelsSuccess() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "GET", req.Method)
		require.Contains(s.T(), req.URL.String(), "/api/channels")
		return jsonResponse(http.StatusOK, `[{"channel_id":"ch-1","name":"general","dir_path":"/home/user/general","parent_id":"","active":true},{"channel_id":"ch-2","name":"thread-1","dir_path":"/home/user/general","parent_id":"ch-1","active":true}]`), nil
	}

	res, err := s.session.CallTool(s.ctx, &mcp.CallToolParams{
		Name:      "search_channels",
		Arguments: map[string]any{},
	})
	require.NoError(s.T(), err)
	require.False(s.T(), res.IsError)
	text := res.Content[0].(*mcp.TextContent).Text
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

	res, err := s.session.CallTool(s.ctx, &mcp.CallToolParams{
		Name:      "search_channels",
		Arguments: map[string]any{"query": "gen"},
	})
	require.NoError(s.T(), err)
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), res.Content[0].(*mcp.TextContent).Text, "general")
}

func (s *MCPServerSuite) TestSearchChannelsEmpty() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `[]`), nil
	}

	res, err := s.session.CallTool(s.ctx, &mcp.CallToolParams{
		Name:      "search_channels",
		Arguments: map[string]any{},
	})
	require.NoError(s.T(), err)
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), res.Content[0].(*mcp.TextContent).Text, "No channels found")
}

func (s *MCPServerSuite) TestSearchChannelsAPIError() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusInternalServerError, "db error"), nil
	}

	res, err := s.session.CallTool(s.ctx, &mcp.CallToolParams{
		Name:      "search_channels",
		Arguments: map[string]any{},
	})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), res.Content[0].(*mcp.TextContent).Text, "API error")
}

func (s *MCPServerSuite) TestSearchChannelsHTTPError() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("connection refused")
	}

	res, err := s.session.CallTool(s.ctx, &mcp.CallToolParams{
		Name:      "search_channels",
		Arguments: map[string]any{},
	})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), res.Content[0].(*mcp.TextContent).Text, "calling API")
}

func (s *MCPServerSuite) TestSearchChannelsInvalidResponseJSON() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, "not json"), nil
	}

	res, err := s.session.CallTool(s.ctx, &mcp.CallToolParams{
		Name:      "search_channels",
		Arguments: map[string]any{},
	})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), res.Content[0].(*mcp.TextContent).Text, "decoding response")
}

// --- send_message ---

func (s *MCPServerSuite) TestSendMessageSuccess() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "POST", req.Method)
		require.Contains(s.T(), req.URL.String(), "/api/messages")
		body, _ := io.ReadAll(req.Body)
		require.Contains(s.T(), string(body), `"channel_id":"ch-1"`)
		require.Contains(s.T(), string(body), `"content":"hello world"`)
		return &http.Response{
			StatusCode: http.StatusNoContent,
			Body:       io.NopCloser(bytes.NewReader(nil)),
		}, nil
	}

	res, err := s.session.CallTool(s.ctx, &mcp.CallToolParams{
		Name:      "send_message",
		Arguments: map[string]any{"channel_id": "ch-1", "content": "hello world"},
	})
	require.NoError(s.T(), err)
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), res.Content[0].(*mcp.TextContent).Text, "Message sent successfully")
}

func (s *MCPServerSuite) TestSendMessageEmptyChannelID() {
	res, err := s.session.CallTool(s.ctx, &mcp.CallToolParams{
		Name:      "send_message",
		Arguments: map[string]any{"channel_id": "", "content": "hello"},
	})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), res.Content[0].(*mcp.TextContent).Text, "channel_id is required")
}

func (s *MCPServerSuite) TestSendMessageEmptyContent() {
	res, err := s.session.CallTool(s.ctx, &mcp.CallToolParams{
		Name:      "send_message",
		Arguments: map[string]any{"channel_id": "ch-1", "content": ""},
	})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), res.Content[0].(*mcp.TextContent).Text, "content is required")
}

func (s *MCPServerSuite) TestSendMessageAPIError() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusInternalServerError, "send failed"), nil
	}

	res, err := s.session.CallTool(s.ctx, &mcp.CallToolParams{
		Name:      "send_message",
		Arguments: map[string]any{"channel_id": "ch-1", "content": "hello"},
	})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), res.Content[0].(*mcp.TextContent).Text, "API error")
}

func (s *MCPServerSuite) TestSendMessageHTTPError() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("connection refused")
	}

	res, err := s.session.CallTool(s.ctx, &mcp.CallToolParams{
		Name:      "send_message",
		Arguments: map[string]any{"channel_id": "ch-1", "content": "hello"},
	})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), res.Content[0].(*mcp.TextContent).Text, "calling API")
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
