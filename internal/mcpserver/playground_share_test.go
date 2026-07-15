package mcpserver

import (
	"net/http"

	"github.com/stretchr/testify/require"
)

func (s *MCPServerSuite) TestPlaygroundShareSuccess() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "PUT", req.Method)
		require.Contains(s.T(), req.URL.String(), "/api/playground/share?name=demo")
		return jsonResponse(http.StatusOK, `{"url":"https://x.trycloudflare.com/p/abc","token":"abc"}`), nil
	}
	text, isError := s.callTool("playground_share", map[string]any{
		"action": "share",
		"name":   "demo",
	})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "https://x.trycloudflare.com/p/abc")
}

func (s *MCPServerSuite) TestPlaygroundShareUnshare() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "DELETE", req.Method)
		require.Contains(s.T(), req.URL.String(), "/api/playground/share?name=demo")
		return noContentResponse(http.StatusNoContent), nil
	}
	text, isError := s.callTool("playground_share", map[string]any{
		"action": "unshare",
		"name":   "demo",
	})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "no longer public")
}

func (s *MCPServerSuite) TestPlaygroundShareInvalidAction() {
	text, isError := s.callTool("playground_share", map[string]any{
		"action": "bogus",
		"name":   "demo",
	})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "invalid action")
}

func (s *MCPServerSuite) TestPlaygroundShareAPIError() {
	s.httpClient.doFunc = func(_ *http.Request) (*http.Response, error) {
		return stringResponse(http.StatusForbidden, "playground share is disabled"), nil
	}
	text, isError := s.callTool("playground_share", map[string]any{
		"action": "share",
		"name":   "demo",
	})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "status 403")
}

func (s *MCPServerSuite) TestPlaygroundUnshareAPIError() {
	s.httpClient.doFunc = func(_ *http.Request) (*http.Response, error) {
		return stringResponse(http.StatusInternalServerError, "boom"), nil
	}
	text, isError := s.callTool("playground_share", map[string]any{
		"action": "unshare",
		"name":   "demo",
	})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "status 500")
}
