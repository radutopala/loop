package mcpserver

import (
	"context"
	"io"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

// --- set_tags ---

func (s *MCPServerSuite) TestSetTags() {
	tests := []struct {
		name     string
		args     map[string]any
		reply    string
		wantPath string
		wantBody string
		wantText string
	}{
		{
			name:     "given channel",
			args:     map[string]any{"channel_id": "thread-1", "add": []string{"web"}, "remove": []string{"old"}},
			reply:    `{"channel_id":"thread-1","tags":["api","web"]}`,
			wantPath: "/api/channels/thread-1/tags",
			wantBody: `{"add":["web"],"remove":["old"]}`,
			wantText: "Tags of thread-1: api, web.",
		},
		{
			name:     "defaults to the current channel, last removed",
			args:     map[string]any{"remove": []string{"web"}},
			reply:    `{"channel_id":"test-channel","tags":[]}`,
			wantPath: "/api/channels/test-channel/tags",
			wantBody: `{"add":null,"remove":["web"]}`,
			wantText: "test-channel has no tags.",
		},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
				require.Equal(s.T(), "POST", req.Method)
				require.Equal(s.T(), tt.wantPath, req.URL.Path)
				body, _ := io.ReadAll(req.Body)
				require.JSONEq(s.T(), tt.wantBody, string(body))
				return jsonResponse(http.StatusOK, tt.reply), nil
			}
			text, isError := s.callTool("set_tags", tt.args)
			require.False(s.T(), isError, text)
			require.Equal(s.T(), tt.wantText, text)
		})
	}
}

func (s *MCPServerSuite) TestSetTagsErrors() {
	s.runToolErrorCases(toolErrorSpec{
		tool:         "set_tags",
		args:         map[string]any{"channel_id": "thread-1", "add": []string{"two words"}},
		apiStatus:    http.StatusBadRequest,
		apiBody:      "may hold only",
		decodeStatus: http.StatusOK,
	})
}

// A server with no channel of its own and no channel_id has nothing to tag.
func (s *MCPServerSuite) TestSetTagsNoChannel() {
	srv := New("", "http://localhost:8222", "", s.httpClient, nil)
	res, _, err := srv.handleSetTags(context.Background(), nil, setTagsInput{Add: []string{"web"}})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), res.Content[0].(*mcp.TextContent).Text, "channel_id is required")
}

// --- list_tags ---

func (s *MCPServerSuite) TestListTags() {
	tests := []struct {
		name     string
		reply    string
		wantText string
	}{
		{name: "lists", reply: `{"tags":[{"tag":"api","count":2},{"tag":"web","count":1}]}`, wantText: "Tags in use (channels and threads carrying each):\n- api (2)\n- web (1)"},
		{name: "none", reply: `{"tags":[]}`, wantText: "No tags in use."},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
				require.Equal(s.T(), "GET", req.Method)
				require.Equal(s.T(), "/api/tags", req.URL.Path)
				return jsonResponse(http.StatusOK, tt.reply), nil
			}
			text, isError := s.callTool("list_tags", map[string]any{})
			require.False(s.T(), isError, text)
			require.Equal(s.T(), tt.wantText, text)
		})
	}
}

func (s *MCPServerSuite) TestListTagsErrors() {
	s.runToolErrorCases(toolErrorSpec{
		tool:         "list_tags",
		args:         map[string]any{},
		apiStatus:    http.StatusInternalServerError,
		apiBody:      "db error",
		decodeStatus: http.StatusOK,
	})
}
