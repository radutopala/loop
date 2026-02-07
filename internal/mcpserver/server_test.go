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
	s.srv = New("test-channel", "http://localhost:8222", s.httpClient, nil)
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
	require.Len(s.T(), res.Tools, 3)

	names := make(map[string]bool)
	for _, t := range res.Tools {
		names[t.Name] = true
	}
	require.True(s.T(), names["schedule_task"])
	require.True(s.T(), names["list_tasks"])
	require.True(s.T(), names["cancel_task"])
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
