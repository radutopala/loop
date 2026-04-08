package mcpserver

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/stretchr/testify/require"
)

// --- prompt_shortcut tool ---

func (s *MCPServerSuite) TestShortcutList() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "GET", req.Method)
		require.Contains(s.T(), req.URL.String(), "/api/shortcuts")
		require.Contains(s.T(), req.URL.String(), "channel_id=test-channel")
		items := []map[string]string{
			{"name": "lint", "description": "Run linter", "prompt": "make lint"},
		}
		data, _ := json.Marshal(items)
		return jsonResponse(http.StatusOK, string(data)), nil
	}

	text, isError := s.callTool("prompt_shortcut", map[string]any{
		"action": "list",
	})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "lint")
	require.Contains(s.T(), text, "Run linter")
}

func (s *MCPServerSuite) TestShortcutListEmpty() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, "[]"), nil
	}

	text, isError := s.callTool("prompt_shortcut", map[string]any{
		"action": "list",
	})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "No prompt shortcuts configured")
}

func (s *MCPServerSuite) TestShortcutListAPIError() {
	s.httpClient.doFunc = func(_ *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusInternalServerError, `{"error":"boom"}`), nil
	}

	text, isError := s.callTool("prompt_shortcut", map[string]any{
		"action": "list",
	})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "500")
}

func (s *MCPServerSuite) TestShortcutListDecodeError() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `not json`), nil
	}

	text, isError := s.callTool("prompt_shortcut", map[string]any{
		"action": "list",
	})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "decoding response")
}

func (s *MCPServerSuite) TestShortcutListNetworkError() {
	s.httpClient.doFunc = func(_ *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("connection refused")
	}

	text, isError := s.callTool("prompt_shortcut", map[string]any{
		"action": "list",
	})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "calling API")
}

func (s *MCPServerSuite) TestShortcutAdd() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "POST", req.Method)
		require.Contains(s.T(), req.URL.String(), "/api/shortcuts")
		body, _ := io.ReadAll(req.Body)
		var payload map[string]string
		require.NoError(s.T(), json.Unmarshal(body, &payload))
		require.Equal(s.T(), "add", payload["action"])
		require.Equal(s.T(), "lint", payload["name"])
		require.Equal(s.T(), "Run linter", payload["description"])
		require.Equal(s.T(), "make lint", payload["prompt"])
		require.Equal(s.T(), "global", payload["scope"])
		return noContentResponse(http.StatusNoContent), nil
	}

	text, isError := s.callTool("prompt_shortcut", map[string]any{
		"action":      "add",
		"name":        "lint",
		"description": "Run linter",
		"prompt":      "make lint",
	})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "Added")
	require.Contains(s.T(), text, "lint")
	require.Contains(s.T(), text, "global")
}

func (s *MCPServerSuite) TestShortcutAddProjectScope() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(req.Body)
		var payload map[string]string
		require.NoError(s.T(), json.Unmarshal(body, &payload))
		require.Equal(s.T(), "project", payload["scope"])
		require.Equal(s.T(), "test-channel", payload["channel_id"])
		return noContentResponse(http.StatusNoContent), nil
	}

	text, isError := s.callTool("prompt_shortcut", map[string]any{
		"action": "add",
		"name":   "build",
		"prompt": "make build",
		"scope":  "project",
	})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "Added")
	require.Contains(s.T(), text, "project")
}

func (s *MCPServerSuite) TestShortcutAddWithPromptPath() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(req.Body)
		var payload map[string]string
		require.NoError(s.T(), json.Unmarshal(body, &payload))
		require.Equal(s.T(), "review-code.md", payload["prompt_path"])
		require.Empty(s.T(), payload["prompt"])
		return noContentResponse(http.StatusNoContent), nil
	}

	text, isError := s.callTool("prompt_shortcut", map[string]any{
		"action":      "add",
		"name":        "review",
		"prompt_path": "review-code.md",
	})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "Added")
}

func (s *MCPServerSuite) TestShortcutAddMissingName() {
	text, isError := s.callTool("prompt_shortcut", map[string]any{
		"action": "add",
		"prompt": "do stuff",
	})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "name is required")
}

func (s *MCPServerSuite) TestShortcutAddAPIError() {
	s.httpClient.doFunc = func(_ *http.Request) (*http.Response, error) {
		return stringResponse(http.StatusConflict, "already exists"), nil
	}

	text, isError := s.callTool("prompt_shortcut", map[string]any{
		"action": "add",
		"name":   "dup",
		"prompt": "x",
	})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "409")
}

func (s *MCPServerSuite) TestShortcutUpdate() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(req.Body)
		var payload map[string]string
		require.NoError(s.T(), json.Unmarshal(body, &payload))
		require.Equal(s.T(), "update", payload["action"])
		require.Equal(s.T(), "lint", payload["name"])
		require.Equal(s.T(), "make lint --fix", payload["prompt"])
		return noContentResponse(http.StatusNoContent), nil
	}

	text, isError := s.callTool("prompt_shortcut", map[string]any{
		"action": "update",
		"name":   "lint",
		"prompt": "make lint --fix",
	})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "Updated")
	require.Contains(s.T(), text, "lint")
}

func (s *MCPServerSuite) TestShortcutUpdateNotFound() {
	s.httpClient.doFunc = func(_ *http.Request) (*http.Response, error) {
		return stringResponse(http.StatusNotFound, "not found"), nil
	}

	text, isError := s.callTool("prompt_shortcut", map[string]any{
		"action": "update",
		"name":   "nope",
		"prompt": "x",
	})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "404")
}

func (s *MCPServerSuite) TestShortcutDelete() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(req.Body)
		var payload map[string]string
		require.NoError(s.T(), json.Unmarshal(body, &payload))
		require.Equal(s.T(), "delete", payload["action"])
		require.Equal(s.T(), "lint", payload["name"])
		return noContentResponse(http.StatusNoContent), nil
	}

	text, isError := s.callTool("prompt_shortcut", map[string]any{
		"action": "delete",
		"name":   "lint",
	})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "Deleted")
	require.Contains(s.T(), text, "lint")
}

func (s *MCPServerSuite) TestShortcutDeleteMissingName() {
	text, isError := s.callTool("prompt_shortcut", map[string]any{
		"action": "delete",
	})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "name is required")
}

func (s *MCPServerSuite) TestShortcutDeleteNotFound() {
	s.httpClient.doFunc = func(_ *http.Request) (*http.Response, error) {
		return stringResponse(http.StatusNotFound, "not found"), nil
	}

	text, isError := s.callTool("prompt_shortcut", map[string]any{
		"action": "delete",
		"name":   "nope",
	})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "404")
}

func (s *MCPServerSuite) TestShortcutInvalidAction() {
	text, isError := s.callTool("prompt_shortcut", map[string]any{
		"action": "invalid",
	})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "action must be one of")
}

func (s *MCPServerSuite) TestShortcutDeleteNetworkError() {
	s.httpClient.doFunc = func(_ *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("connection refused")
	}

	text, isError := s.callTool("prompt_shortcut", map[string]any{
		"action": "delete",
		"name":   "lint",
	})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "calling API")
}
