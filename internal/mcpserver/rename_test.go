package mcpserver

import (
	"io"
	"net/http"

	"github.com/stretchr/testify/require"
)

// --- rename_thread ---

func (s *MCPServerSuite) TestRenameThreadSuccess() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "POST", req.Method)
		require.Contains(s.T(), req.URL.String(), "/api/channels/thread-1/rename")
		body, _ := io.ReadAll(req.Body)
		require.Contains(s.T(), string(body), `"name":"my-new-name"`)
		return jsonResponse(http.StatusOK, `{"channel_id":"thread-1","name":"my-new-name"}`), nil
	}

	text, isError := s.callTool("rename_thread", map[string]any{
		"thread_id": "thread-1",
		"name":      "my-new-name",
	})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "thread-1")
	require.Contains(s.T(), text, "my-new-name")
}

func (s *MCPServerSuite) TestRenameThreadEmptyID() {
	text, isError := s.callTool("rename_thread", map[string]any{
		"thread_id": "",
		"name":      "my-new-name",
	})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "thread_id is required")
}

func (s *MCPServerSuite) TestRenameThreadEmptyName() {
	text, isError := s.callTool("rename_thread", map[string]any{
		"thread_id": "thread-1",
		"name":      "",
	})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "name is required")
}

func (s *MCPServerSuite) TestRenameThreadErrors() {
	runToolErrorCases(&s.Suite, s.httpClient, s.callTool, toolErrorSpec{
		tool:      "rename_thread",
		args:      map[string]any{"thread_id": "thread-1", "name": "my-new-name"},
		apiStatus: http.StatusNotFound,
		apiBody:   "thread not found",
	})
}

// --- rename_worktree_thread ---

func (s *MCPServerSuite) TestRenameWorktreeThreadSuccess() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "POST", req.Method)
		require.Contains(s.T(), req.URL.String(), "/api/worktrees/move")
		body, _ := io.ReadAll(req.Body)
		require.Contains(s.T(), string(body), `"channel_id":"wt-thread-1"`)
		require.Contains(s.T(), string(body), `"new_name":"wt-new"`)
		return jsonResponse(http.StatusOK, `{"channel_id":"wt-thread-1","name":"wt-new","dir_path":"/proj/.worktrees/wt-new"}`), nil
	}

	text, isError := s.callTool("rename_worktree_thread", map[string]any{
		"thread_id": "wt-thread-1",
		"new_name":  "wt-new",
	})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "wt-thread-1")
	require.Contains(s.T(), text, "wt-new")
	require.Contains(s.T(), text, "/proj/.worktrees/wt-new")
}

func (s *MCPServerSuite) TestRenameWorktreeThreadEmptyID() {
	text, isError := s.callTool("rename_worktree_thread", map[string]any{
		"thread_id": "",
		"new_name":  "wt-new",
	})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "thread_id is required")
}

func (s *MCPServerSuite) TestRenameWorktreeThreadEmptyNewName() {
	text, isError := s.callTool("rename_worktree_thread", map[string]any{
		"thread_id": "wt-thread-1",
		"new_name":  "",
	})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "new_name is required")
}

func (s *MCPServerSuite) TestRenameWorktreeThreadErrors() {
	runToolErrorCases(&s.Suite, s.httpClient, s.callTool, toolErrorSpec{
		tool:         "rename_worktree_thread",
		args:         map[string]any{"thread_id": "wt-thread-1", "new_name": "wt-new"},
		apiStatus:    http.StatusInternalServerError,
		apiBody:      "active run",
		decodeStatus: http.StatusOK,
	})
}
