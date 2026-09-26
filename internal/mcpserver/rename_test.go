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
	s.runToolErrorCases(toolErrorSpec{
		tool:      "rename_thread",
		args:      map[string]any{"thread_id": "thread-1", "name": "my-new-name"},
		apiStatus: http.StatusNotFound,
		apiBody:   "thread not found",
	})
}
