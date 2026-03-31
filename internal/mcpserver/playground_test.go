package mcpserver

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/stretchr/testify/require"
)

// --- playground tool ---

func (s *MCPServerSuite) TestPlaygroundCreate() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "PUT", req.Method)
		require.Contains(s.T(), req.URL.String(), "/api/playground?name=my-app")
		body, _ := io.ReadAll(req.Body)
		var payload map[string]string
		require.NoError(s.T(), json.Unmarshal(body, &payload))
		require.Equal(s.T(), "<h1>Hi</h1>", payload["html"])
		require.Equal(s.T(), "My App", payload["title"])
		return noContentResponse(http.StatusOK), nil
	}

	text, isError := s.callTool("playground", map[string]any{
		"action":      "create",
		"name":        "my-app",
		"title":       "My App",
		"description": "A cool app",
		"html":        "<h1>Hi</h1>",
	})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "created")
}

func (s *MCPServerSuite) TestPlaygroundCreateMissingHTML() {
	text, isError := s.callTool("playground", map[string]any{
		"action":      "create",
		"name":        "bad",
		"title":       "Bad",
		"description": "No html",
	})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "html is required")
}

func (s *MCPServerSuite) TestPlaygroundCreateMissingTitle() {
	text, isError := s.callTool("playground", map[string]any{
		"action":      "create",
		"name":        "bad",
		"html":        "<div>hi</div>",
		"description": "No title",
	})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "title and description are required")
}

func (s *MCPServerSuite) TestPlaygroundUpdate() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "PUT", req.Method)
		return noContentResponse(http.StatusOK), nil
	}

	text, isError := s.callTool("playground", map[string]any{
		"action": "update",
		"name":   "my-app",
		"html":   "<h1>Updated</h1>",
	})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "updated")
}

func (s *MCPServerSuite) TestPlaygroundUpdateTitleOnly() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		return noContentResponse(http.StatusOK), nil
	}

	text, isError := s.callTool("playground", map[string]any{
		"action":      "update",
		"name":        "my-app",
		"title":       "New Title",
		"description": "New desc",
	})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "updated")
}

func (s *MCPServerSuite) TestPlaygroundUpdateEmpty() {
	text, isError := s.callTool("playground", map[string]any{
		"action": "update",
		"name":   "my-app",
	})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "at least one")
}

func (s *MCPServerSuite) TestPlaygroundDelete() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "DELETE", req.Method)
		return noContentResponse(http.StatusNoContent), nil
	}

	text, isError := s.callTool("playground", map[string]any{
		"action": "delete",
		"name":   "my-app",
	})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "deleted")
}

func (s *MCPServerSuite) TestPlaygroundInvalidAction() {
	text, isError := s.callTool("playground", map[string]any{
		"action": "invalid",
		"name":   "test",
	})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "invalid action")
}

func (s *MCPServerSuite) TestPlaygroundDeleteAPIError() {
	s.httpClient.doFunc = func(_ *http.Request) (*http.Response, error) {
		return noContentResponse(http.StatusNotFound), nil
	}

	text, isError := s.callTool("playground", map[string]any{
		"action": "delete",
		"name":   "nope",
	})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "404")
}

func (s *MCPServerSuite) TestPlaygroundUpdateAPIError() {
	s.httpClient.doFunc = func(_ *http.Request) (*http.Response, error) {
		return noContentResponse(http.StatusInternalServerError), nil
	}

	text, isError := s.callTool("playground", map[string]any{
		"action": "update",
		"name":   "err",
		"html":   "<p>fail</p>",
	})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "500")
}

func (s *MCPServerSuite) TestPlaygroundAPIError() {
	s.httpClient.doFunc = func(_ *http.Request) (*http.Response, error) {
		return noContentResponse(http.StatusInternalServerError), nil
	}

	text, isError := s.callTool("playground", map[string]any{
		"action":      "create",
		"name":        "err",
		"title":       "Error",
		"description": "fail",
		"html":        "<p>test</p>",
	})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "500")
}

// --- playground_file tool ---

func (s *MCPServerSuite) TestPlaygroundFileCreate() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "PUT", req.Method)
		require.Contains(s.T(), req.URL.String(), "path=script.js")
		body, _ := io.ReadAll(req.Body)
		require.Equal(s.T(), "console.log('hi')", string(body))
		return noContentResponse(http.StatusOK), nil
	}

	text, isError := s.callTool("playground_file", map[string]any{
		"action":  "create",
		"name":    "my-app",
		"path":    "script.js",
		"content": "console.log('hi')",
	})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "written")
}

func (s *MCPServerSuite) TestPlaygroundFileCreateMissingPath() {
	text, isError := s.callTool("playground_file", map[string]any{
		"action":  "create",
		"name":    "my-app",
		"content": "data",
	})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "path is required")
}

func (s *MCPServerSuite) TestPlaygroundFileCreateMissingContent() {
	text, isError := s.callTool("playground_file", map[string]any{
		"action": "create",
		"name":   "my-app",
		"path":   "script.js",
	})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "content is required")
}

func (s *MCPServerSuite) TestPlaygroundFileRead() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "GET", req.Method)
		return stringResponse(http.StatusOK, "console.log('hi')"), nil
	}

	text, isError := s.callTool("playground_file", map[string]any{
		"action": "read",
		"name":   "my-app",
		"path":   "script.js",
	})
	require.False(s.T(), isError)
	require.Equal(s.T(), "console.log('hi')", text)
}

func (s *MCPServerSuite) TestPlaygroundFileReadMissingPath() {
	text, isError := s.callTool("playground_file", map[string]any{
		"action": "read",
		"name":   "my-app",
	})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "path is required")
}

func (s *MCPServerSuite) TestPlaygroundFileDelete() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "DELETE", req.Method)
		return noContentResponse(http.StatusNoContent), nil
	}

	text, isError := s.callTool("playground_file", map[string]any{
		"action": "delete",
		"name":   "my-app",
		"path":   "old.js",
	})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "deleted")
}

func (s *MCPServerSuite) TestPlaygroundFileDeleteMissingPath() {
	text, isError := s.callTool("playground_file", map[string]any{
		"action": "delete",
		"name":   "my-app",
	})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "path is required")
}

func (s *MCPServerSuite) TestPlaygroundFileList() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "GET", req.Method)
		return stringResponse(http.StatusOK, `{"files":["index.html","script.js","style.css"]}`), nil
	}

	text, isError := s.callTool("playground_file", map[string]any{
		"action": "list",
		"name":   "my-app",
	})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "script.js")
	require.Contains(s.T(), text, "style.css")
}

func (s *MCPServerSuite) TestPlaygroundFileInvalidAction() {
	text, isError := s.callTool("playground_file", map[string]any{
		"action": "invalid",
		"name":   "test",
	})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "invalid action")
}

func (s *MCPServerSuite) TestPlaygroundFileReadNetworkError() {
	s.httpClient.doFunc = func(_ *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("connection refused")
	}

	text, isError := s.callTool("playground_file", map[string]any{
		"action": "read",
		"name":   "my-app",
		"path":   "script.js",
	})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "reading file")
}

func (s *MCPServerSuite) TestPlaygroundFileListNetworkError() {
	s.httpClient.doFunc = func(_ *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("connection refused")
	}

	text, isError := s.callTool("playground_file", map[string]any{
		"action": "list",
		"name":   "my-app",
	})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "listing files")
}

func (s *MCPServerSuite) TestPlaygroundFileReadError() {
	s.httpClient.doFunc = func(_ *http.Request) (*http.Response, error) {
		return stringResponse(http.StatusNotFound, "file not found"), nil
	}

	text, isError := s.callTool("playground_file", map[string]any{
		"action": "read",
		"name":   "my-app",
		"path":   "nope.js",
	})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "404")
}

func (s *MCPServerSuite) TestPlaygroundFileCreateError() {
	s.httpClient.doFunc = func(_ *http.Request) (*http.Response, error) {
		return noContentResponse(http.StatusInternalServerError), nil
	}

	text, isError := s.callTool("playground_file", map[string]any{
		"action":  "create",
		"name":    "my-app",
		"path":    "script.js",
		"content": "x",
	})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "500")
}

func (s *MCPServerSuite) TestPlaygroundFileDeleteError() {
	s.httpClient.doFunc = func(_ *http.Request) (*http.Response, error) {
		return noContentResponse(http.StatusNotFound), nil
	}

	text, isError := s.callTool("playground_file", map[string]any{
		"action": "delete",
		"name":   "my-app",
		"path":   "nope.js",
	})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "404")
}

func (s *MCPServerSuite) TestPlaygroundFileListError() {
	s.httpClient.doFunc = func(_ *http.Request) (*http.Response, error) {
		return stringResponse(http.StatusBadRequest, "bad"), nil
	}

	text, isError := s.callTool("playground_file", map[string]any{
		"action": "list",
		"name":   "my-app",
	})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "400")
}

func (s *MCPServerSuite) TestPlaygroundFileListEmpty() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		return stringResponse(http.StatusOK, `{"files":[]}`), nil
	}

	text, isError := s.callTool("playground_file", map[string]any{
		"action": "list",
		"name":   "my-app",
	})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "No files")
}

func (s *MCPServerSuite) TestPlaygroundCreateProjectScope() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "PUT", req.Method)
		require.Contains(s.T(), req.URL.String(), "&scope=project&channel_id=test-channel")
		return noContentResponse(http.StatusOK), nil
	}

	text, isError := s.callTool("playground", map[string]any{
		"action":      "create",
		"name":        "my-app",
		"title":       "My App",
		"description": "A scoped app",
		"html":        "<h1>Hi</h1>",
		"scope":       "project",
	})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "created")
}

func (s *MCPServerSuite) TestPlaygroundFileCreateProjectScope() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "PUT", req.Method)
		require.Contains(s.T(), req.URL.String(), "&scope=project&channel_id=test-channel")
		return noContentResponse(http.StatusOK), nil
	}

	text, isError := s.callTool("playground_file", map[string]any{
		"action":  "create",
		"name":    "my-app",
		"path":    "script.js",
		"content": "console.log('hi')",
		"scope":   "project",
	})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "written")
}
