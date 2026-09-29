package mcpserver

import (
	"context"
	"io"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func (s *MCPServerSuite) TestListSessions() {
	long := strings.Repeat("x", 130)
	tests := []struct {
		name     string
		args     map[string]any
		wantPath string
		body     string
		want     []string
	}{
		{
			name:     "empty",
			args:     map[string]any{},
			wantPath: "/api/channels/test-channel/sessions",
			body:     `{"current_session_id":"","sessions":[]}`,
			want:     []string{"No sessions"},
		},
		{
			name:     "current, held and free",
			args:     map[string]any{"channel_id": "other"},
			wantPath: "/api/channels/other/sessions",
			body: `{"current_session_id":"s1","imported_session_ids":["s1","s2"],"sessions":[` +
				`{"session_id":"s1","last_modified":"2026-09-29T10:00:00Z","last_message":"fixed\nit"},` +
				`{"session_id":"s2","last_modified":"2026-09-28T10:00:00Z"},` +
				`{"session_id":"s3","last_modified":"2026-09-27T10:00:00Z","last_message":"` + long + `"}]}`,
			want: []string{
				"3 session(s)",
				"- s1 * 2026-09-29T10:00:00Z fixed it",
				"- s2 [thread] 2026-09-28T10:00:00Z",
				"- s3 2026-09-27T10:00:00Z " + strings.Repeat("x", 120) + "…",
			},
		},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
				require.Equal(s.T(), "GET", req.Method)
				require.Equal(s.T(), tt.wantPath, req.URL.Path)
				return jsonResponse(http.StatusOK, tt.body), nil
			}
			text, isError := s.callTool("list_sessions", tt.args)
			require.False(s.T(), isError)
			for _, w := range tt.want {
				require.Contains(s.T(), text, w)
			}
		})
	}
}

func (s *MCPServerSuite) TestListSessionsErrors() {
	s.runToolErrorCases(toolErrorSpec{
		tool:         "list_sessions",
		args:         map[string]any{},
		apiStatus:    http.StatusInternalServerError,
		apiBody:      "list failed",
		decodeStatus: http.StatusOK,
	})
}

func (s *MCPServerSuite) TestResumeSession() {
	tests := []struct {
		name     string
		args     map[string]any
		wantPath string
		respBody string
		want     string
	}{
		{
			name:     "switches now",
			args:     map[string]any{"session_id": "s2", "channel_id": "other"},
			wantPath: "/api/channels/other/session",
			respBody: `{"deferred":false}`,
			want:     "Switched to session s2; the channel's next message resumes it.",
		},
		{
			name:     "own channel waits for the run",
			args:     map[string]any{"session_id": "s2"},
			wantPath: "/api/channels/test-channel/session",
			respBody: `{"deferred":true}`,
			want:     "Session s2 will become the channel's once its current run ends",
		},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
				require.Equal(s.T(), "PUT", req.Method)
				require.Equal(s.T(), tt.wantPath, req.URL.Path)
				body, err := io.ReadAll(req.Body)
				require.NoError(s.T(), err)
				require.JSONEq(s.T(), `{"session_id":"s2"}`, string(body))
				return jsonResponse(http.StatusOK, tt.respBody), nil
			}
			text, isError := s.callTool("resume_session", tt.args)
			require.False(s.T(), isError)
			require.Contains(s.T(), text, tt.want)
		})
	}
}

func (s *MCPServerSuite) TestResumeSessionErrors() {
	s.runToolErrorCases(toolErrorSpec{
		tool:         "resume_session",
		args:         map[string]any{"session_id": "s2"},
		apiStatus:    http.StatusNotFound,
		apiBody:      "session not found",
		decodeStatus: http.StatusOK,
	})

	text, isError := s.callTool("resume_session", map[string]any{"session_id": ""})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "session_id is required")
}

// TestSessionsNoChannel covers an agent with no channel of its own and none
// given.
func (s *MCPServerSuite) TestSessionsNoChannel() {
	srv := New("", "http://localhost:8222", "", s.httpClient, nil)
	res, _, err := srv.handleListSessions(context.Background(), nil, listSessionsInput{})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), res.Content[0].(*mcp.TextContent).Text, "channel_id is required")

	res, _, err = srv.handleResumeSession(context.Background(), nil, resumeSessionInput{SessionID: "s2"})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), res.Content[0].(*mcp.TextContent).Text, "channel_id is required")
}
