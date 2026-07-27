package mcpserver

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/stretchr/testify/require"
)

// --- bash_shortcut tool ---

func (s *MCPServerSuite) TestBashShortcutList() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "GET", req.Method)
		require.Contains(s.T(), req.URL.String(), "/api/bash-shortcuts")
		require.Contains(s.T(), req.URL.String(), "channel_id=test-channel")
		items := []map[string]string{
			{"name": "lint", "description": "Run linter", "command": "make lint"},
		}
		data, _ := json.Marshal(items)
		return jsonResponse(http.StatusOK, string(data)), nil
	}

	text, isError := s.callTool("bash_shortcut", map[string]any{
		"action": "list",
	})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "lint")
	require.Contains(s.T(), text, "Run linter")
}

func (s *MCPServerSuite) TestBashShortcutListEmpty() {
	s.httpClient.doFunc = func(_ *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, "[]"), nil
	}

	text, isError := s.callTool("bash_shortcut", map[string]any{
		"action": "list",
	})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "No bash shortcuts configured")
}

func (s *MCPServerSuite) TestBashShortcutListAPIError() {
	s.httpClient.doFunc = func(_ *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusInternalServerError, `{"error":"boom"}`), nil
	}

	text, isError := s.callTool("bash_shortcut", map[string]any{
		"action": "list",
	})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "500")
}

func (s *MCPServerSuite) TestBashShortcutListDecodeError() {
	s.httpClient.doFunc = func(_ *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `not json`), nil
	}

	text, isError := s.callTool("bash_shortcut", map[string]any{
		"action": "list",
	})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "decoding response")
}

func (s *MCPServerSuite) TestBashShortcutListNetworkError() {
	s.httpClient.doFunc = func(_ *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("connection refused")
	}

	text, isError := s.callTool("bash_shortcut", map[string]any{
		"action": "list",
	})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "calling API")
}

func (s *MCPServerSuite) TestBashShortcutAdd() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "POST", req.Method)
		require.Contains(s.T(), req.URL.String(), "/api/bash-shortcuts")
		body, _ := io.ReadAll(req.Body)
		var payload map[string]string
		require.NoError(s.T(), json.Unmarshal(body, &payload))
		require.Equal(s.T(), "add", payload["action"])
		require.Equal(s.T(), "lint", payload["name"])
		require.Equal(s.T(), "Run linter", payload["description"])
		require.Equal(s.T(), "make lint", payload["command"])
		require.Equal(s.T(), "global", payload["scope"])
		return noContentResponse(http.StatusNoContent), nil
	}

	text, isError := s.callTool("bash_shortcut", map[string]any{
		"action":      "add",
		"scope":       "global",
		"name":        "lint",
		"description": "Run linter",
		"command":     "make lint",
	})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "Added")
	require.Contains(s.T(), text, "lint")
	require.Contains(s.T(), text, "global")
}

func (s *MCPServerSuite) TestBashShortcutAddProjectScope() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(req.Body)
		var payload map[string]string
		require.NoError(s.T(), json.Unmarshal(body, &payload))
		require.Equal(s.T(), "project", payload["scope"])
		require.Equal(s.T(), "test-channel", payload["channel_id"])
		return noContentResponse(http.StatusNoContent), nil
	}

	text, isError := s.callTool("bash_shortcut", map[string]any{
		"action":  "add",
		"name":    "build",
		"command": "make build",
		"scope":   "project",
	})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "Added")
	require.Contains(s.T(), text, "project")
}

func (s *MCPServerSuite) TestBashShortcutAddWithCommandPath() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(req.Body)
		var payload map[string]string
		require.NoError(s.T(), json.Unmarshal(body, &payload))
		require.Equal(s.T(), "deploy.sh", payload["command_path"])
		require.Empty(s.T(), payload["command"])
		return noContentResponse(http.StatusNoContent), nil
	}

	text, isError := s.callTool("bash_shortcut", map[string]any{
		"action":       "add",
		"scope":        "global",
		"name":         "deploy",
		"command_path": "deploy.sh",
	})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "Added")
}

func (s *MCPServerSuite) TestBashShortcutAddMissingName() {
	text, isError := s.callTool("bash_shortcut", map[string]any{
		"action":  "add",
		"scope":   "global",
		"command": "echo hi",
	})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "name is required")
}

func (s *MCPServerSuite) TestBashShortcutAddAPIError() {
	s.httpClient.doFunc = func(_ *http.Request) (*http.Response, error) {
		return stringResponse(http.StatusConflict, "already exists"), nil
	}

	text, isError := s.callTool("bash_shortcut", map[string]any{
		"action":  "add",
		"scope":   "global",
		"name":    "dup",
		"command": "x",
	})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "409")
}

func (s *MCPServerSuite) TestBashShortcutUpdate() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(req.Body)
		var payload map[string]string
		require.NoError(s.T(), json.Unmarshal(body, &payload))
		require.Equal(s.T(), "update", payload["action"])
		require.Equal(s.T(), "lint", payload["name"])
		require.Equal(s.T(), "make lint-fix", payload["command"])
		return noContentResponse(http.StatusNoContent), nil
	}

	text, isError := s.callTool("bash_shortcut", map[string]any{
		"action":  "update",
		"scope":   "global",
		"name":    "lint",
		"command": "make lint-fix",
	})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "Updated")
	require.Contains(s.T(), text, "lint")
}

func (s *MCPServerSuite) TestBashShortcutUpdateNotFound() {
	s.httpClient.doFunc = func(_ *http.Request) (*http.Response, error) {
		return stringResponse(http.StatusNotFound, "not found"), nil
	}

	text, isError := s.callTool("bash_shortcut", map[string]any{
		"action":  "update",
		"scope":   "global",
		"name":    "nope",
		"command": "x",
	})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "404")
}

func (s *MCPServerSuite) TestBashShortcutDelete() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(req.Body)
		var payload map[string]string
		require.NoError(s.T(), json.Unmarshal(body, &payload))
		require.Equal(s.T(), "delete", payload["action"])
		require.Equal(s.T(), "lint", payload["name"])
		return noContentResponse(http.StatusNoContent), nil
	}

	text, isError := s.callTool("bash_shortcut", map[string]any{
		"action": "delete",
		"scope":  "global",
		"name":   "lint",
	})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "Deleted")
	require.Contains(s.T(), text, "lint")
}

func (s *MCPServerSuite) TestBashShortcutDeleteMissingName() {
	text, isError := s.callTool("bash_shortcut", map[string]any{
		"action": "delete",
		"scope":  "global",
	})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "name is required")
}

func (s *MCPServerSuite) TestBashShortcutDeleteNotFound() {
	s.httpClient.doFunc = func(_ *http.Request) (*http.Response, error) {
		return stringResponse(http.StatusNotFound, "not found"), nil
	}

	text, isError := s.callTool("bash_shortcut", map[string]any{
		"action": "delete",
		"scope":  "global",
		"name":   "nope",
	})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "404")
}

func (s *MCPServerSuite) TestBashShortcutInvalidAction() {
	text, isError := s.callTool("bash_shortcut", map[string]any{
		"action": "invalid",
	})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "action must be one of")
}

func (s *MCPServerSuite) TestBashShortcutDeleteNetworkError() {
	s.httpClient.doFunc = func(_ *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("connection refused")
	}

	text, isError := s.callTool("bash_shortcut", map[string]any{
		"action": "delete",
		"scope":  "global",
		"name":   "lint",
	})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "calling API")
}

// --- Scope enforcement (regression: a missing scope silently defaulted to
// global, so project-scoped shortcuts were invisible to update/delete) ---

func (s *MCPServerSuite) TestBashShortcutRequiresExplicitScope() {
	called := false
	s.httpClient.doFunc = func(_ *http.Request) (*http.Response, error) {
		called = true
		return noContentResponse(http.StatusNoContent), nil
	}

	for _, action := range []string{"add", "update", "delete"} {
		text, isError := s.callTool("bash_shortcut", map[string]any{
			"action":  action,
			"name":    "git pull",
			"command": "git pull",
		})
		require.True(s.T(), isError, "action %s must reject a missing scope", action)
		require.Contains(s.T(), text, "scope is required for "+action)
		require.Contains(s.T(), text, "list action first")
	}
	require.False(s.T(), called, "no API call should be made without an explicit scope")
}

func (s *MCPServerSuite) TestBashShortcutRejectsUnknownScope() {
	called := false
	s.httpClient.doFunc = func(_ *http.Request) (*http.Response, error) {
		called = true
		return noContentResponse(http.StatusNoContent), nil
	}

	text, isError := s.callTool("bash_shortcut", map[string]any{
		"action":  "delete",
		"name":    "lint",
		"scope":   "workspace",
		"command": "make lint",
	})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, `scope must be 'global' or 'project'`)
	require.Contains(s.T(), text, `"workspace"`)
	require.False(s.T(), called)
}

// A name with spaces round-trips untouched — no slugging anywhere in the path.
func (s *MCPServerSuite) TestBashShortcutDeleteNameWithSpaces() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(req.Body)
		var payload map[string]string
		require.NoError(s.T(), json.Unmarshal(body, &payload))
		require.Equal(s.T(), "git pull", payload["name"])
		require.Equal(s.T(), "project", payload["scope"])
		return noContentResponse(http.StatusNoContent), nil
	}

	text, isError := s.callTool("bash_shortcut", map[string]any{
		"action": "delete",
		"name":   "git pull",
		"scope":  "project",
	})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "Deleted")
	require.Contains(s.T(), text, "git pull")
}

func (s *MCPServerSuite) TestPromptShortcutRequiresExplicitScope() {
	text, isError := s.callTool("prompt_shortcut", map[string]any{
		"action": "delete",
		"name":   "lint",
	})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "scope is required for delete")
	require.Contains(s.T(), text, "prompt shortcut")
}

// list stays cross-scope: it must not demand a scope.
func (s *MCPServerSuite) TestBashShortcutListNeedsNoScope() {
	s.httpClient.doFunc = func(_ *http.Request) (*http.Response, error) {
		return stringResponse(http.StatusOK, `[{"name":"lint","command":"make lint","scope":"project"}]`), nil
	}

	text, isError := s.callTool("bash_shortcut", map[string]any{"action": "list"})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, `"scope": "project"`)
}
