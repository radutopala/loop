package mcpserver

import (
	"context"
	"io"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
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

// --- set_thread_description ---

func (s *MCPServerSuite) TestSetThreadDescription() {
	tests := []struct {
		name     string
		args     map[string]any
		wantPath string
		wantBody string
		wantText string
	}{
		{
			name:     "given thread",
			args:     map[string]any{"thread_id": "thread-1", "description": "fixes login"},
			wantPath: "/api/channels/thread-1/description",
			wantBody: `{"description":"fixes login"}`,
			wantText: "Description of thread thread-1 set.",
		},
		{
			name:     "defaults to the current thread",
			args:     map[string]any{"description": "fixes login"},
			wantPath: "/api/channels/test-channel/description",
			wantBody: `{"description":"fixes login"}`,
			wantText: "Description of thread test-channel set.",
		},
		{
			name:     "blank clears it",
			args:     map[string]any{"thread_id": "thread-1", "description": "  "},
			wantPath: "/api/channels/thread-1/description",
			wantBody: `{"description":"  "}`,
			wantText: "Description of thread thread-1 cleared.",
		},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
				require.Equal(s.T(), "POST", req.Method)
				require.Equal(s.T(), tt.wantPath, req.URL.Path)
				body, _ := io.ReadAll(req.Body)
				require.JSONEq(s.T(), tt.wantBody, string(body))
				return jsonResponse(http.StatusOK, `{}`), nil
			}
			text, isError := s.callTool("set_thread_description", tt.args)
			require.False(s.T(), isError, text)
			require.Equal(s.T(), tt.wantText, text)
		})
	}
}

func (s *MCPServerSuite) TestSetThreadDescriptionErrors() {
	s.runToolErrorCases(toolErrorSpec{
		tool:      "set_thread_description",
		args:      map[string]any{"thread_id": "thread-1", "description": "x"},
		apiStatus: http.StatusBadRequest,
		apiBody:   "description is longer than 500 characters",
	})
}

// A server with no thread of its own and no thread_id has nothing to describe.
func (s *MCPServerSuite) TestSetThreadDescriptionNoThread() {
	srv := New("", "http://localhost:8222", "", s.httpClient, nil)
	res, _, err := srv.handleSetThreadDescription(context.Background(), nil, setThreadDescriptionInput{Description: "x"})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), res.Content[0].(*mcp.TextContent).Text, "thread_id is required")
}
