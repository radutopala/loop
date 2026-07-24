package mcpserver

import (
	"fmt"
	"io"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

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
	s.runToolErrorCases(toolErrorSpec{
		tool:      "toggle_task",
		args:      map[string]any{"task_id": float64(1), "enabled": false},
		apiStatus: http.StatusInternalServerError,
		apiBody:   "not found",
	})
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
	s.runToolErrorCases(toolErrorSpec{
		tool:         "create_channel",
		args:         map[string]any{"name": "trial"},
		apiStatus:    http.StatusInternalServerError,
		apiBody:      "create failed",
		decodeStatus: http.StatusCreated,
	})
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
	s.runToolErrorCases(toolErrorSpec{
		tool:         "create_thread",
		args:         map[string]any{"name": "my-thread", "message": "Do something"},
		apiStatus:    http.StatusInternalServerError,
		apiBody:      "parent not found",
		decodeStatus: http.StatusCreated,
	})
}

// --- create_worktree_thread ---

func (s *MCPServerSuite) TestCreateWorktreeThreadSuccess() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "POST", req.Method)
		require.Contains(s.T(), req.URL.String(), "/api/worktrees")
		body, _ := io.ReadAll(req.Body)
		require.Contains(s.T(), string(body), `"channel_id":"test-channel"`)
		require.Contains(s.T(), string(body), `"branch":"feat/foo"`)
		require.Contains(s.T(), string(body), `"name":"my-wt"`)
		return jsonResponse(http.StatusCreated, `{"thread_id":"thread-1","worktree_path":"/tmp/wt/my-wt"}`), nil
	}

	text, isError := s.callTool("create_worktree_thread", map[string]any{"branch": "feat/foo", "name": "my-wt"})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "thread-1")
	require.Contains(s.T(), text, "/tmp/wt/my-wt")
	require.Contains(s.T(), text, "feat/foo")
}

func (s *MCPServerSuite) TestCreateWorktreeThreadSuccessWithMessage() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(req.Body)
		require.Contains(s.T(), string(body), `"branch":"feat/foo"`)
		require.Contains(s.T(), string(body), `"message":"Implement the parser"`)
		return jsonResponse(http.StatusCreated, `{"thread_id":"thread-9","worktree_path":"/tmp/wt/wt-xx"}`), nil
	}

	text, isError := s.callTool("create_worktree_thread", map[string]any{"branch": "feat/foo", "message": "Implement the parser"})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "thread-9")
	require.Contains(s.T(), text, "agent has been triggered")
}

func (s *MCPServerSuite) TestCreateWorktreeThreadSuccessNoName() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(req.Body)
		require.Contains(s.T(), string(body), `"branch":"main"`)
		require.NotContains(s.T(), string(body), `"name"`)
		return jsonResponse(http.StatusCreated, `{"thread_id":"thread-2","worktree_path":"/tmp/wt/wt-abcd"}`), nil
	}

	text, isError := s.callTool("create_worktree_thread", map[string]any{"branch": "main"})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "thread-2")
}

func (s *MCPServerSuite) TestCreateWorktreeThreadSuccessWithAuthorID() {
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
		return jsonResponse(http.StatusCreated, `{"thread_id":"thread-7","worktree_path":"/tmp/wt/wt-z"}`), nil
	}

	text, isError := s.callTool("create_worktree_thread", map[string]any{"branch": "feat/foo"})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "thread-7")
}

func (s *MCPServerSuite) TestCreateWorktreeThreadEmptyBranch() {
	text, isError := s.callTool("create_worktree_thread", map[string]any{"branch": ""})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "branch is required")
}

func (s *MCPServerSuite) TestCreateWorktreeThreadErrors() {
	s.runToolErrorCases(toolErrorSpec{
		tool:         "create_worktree_thread",
		args:         map[string]any{"branch": "feat/foo"},
		apiStatus:    http.StatusInternalServerError,
		apiBody:      "channel not found or has no dir_path",
		decodeStatus: http.StatusCreated,
	})
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
	s.runToolErrorCases(toolErrorSpec{
		tool:      "delete_thread",
		args:      map[string]any{"thread_id": "thread-1"},
		apiStatus: http.StatusInternalServerError,
		apiBody:   "thread not found",
	})
}

// --- fork_thread ---

func (s *MCPServerSuite) TestForkThreadSuccess() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "POST", req.Method)
		require.Contains(s.T(), req.URL.String(), "/api/threads/thread-1/fork")
		return jsonResponse(http.StatusCreated, `{"thread_id":"thread-2"}`), nil
	}

	text, isError := s.callTool("fork_thread", map[string]any{"thread_id": "thread-1"})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "thread-2")
	require.Contains(s.T(), text, "forked")
	require.Contains(s.T(), text, "send_message")
}

func (s *MCPServerSuite) TestForkThreadSuccessWorktree() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Contains(s.T(), req.URL.String(), "/api/threads/wt-1/fork")
		return jsonResponse(http.StatusCreated, `{"thread_id":"thread-3","worktree_path":"/tmp/wt/wt-fork"}`), nil
	}

	text, isError := s.callTool("fork_thread", map[string]any{"thread_id": "wt-1"})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "thread-3")
	require.Contains(s.T(), text, "/tmp/wt/wt-fork")
	require.Contains(s.T(), text, "worktree")
}

func (s *MCPServerSuite) TestForkThreadEmptyID() {
	text, isError := s.callTool("fork_thread", map[string]any{"thread_id": ""})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "thread_id is required")
}

func (s *MCPServerSuite) TestForkThreadErrors() {
	s.runToolErrorCases(toolErrorSpec{
		tool:         "fork_thread",
		args:         map[string]any{"thread_id": "thread-1"},
		apiStatus:    http.StatusBadRequest,
		apiBody:      "thread not found",
		decodeStatus: http.StatusCreated,
	})
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
