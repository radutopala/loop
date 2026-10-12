package mcpserver

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/plugin"
)

type PluginToolSuite struct {
	baseToolSuite
}

func TestPluginToolSuite(t *testing.T) {
	s := new(PluginToolSuite)
	s.serverOpts = []MemoryOption{WithPluginTools("echo", []plugin.Tool{{
		Name:        "echo",
		Description: "Echoes text",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}}}`),
	}, {
		Name:         "score",
		Description:  "Scores",
		InputSchema:  json.RawMessage(`{"type":"object"}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"score":{"type":"integer"}},"required":["score"]}`),
	}})}
	suite.Run(t, s)
}

func (s *PluginToolSuite) TestListed() {
	res, err := s.session.ListTools(s.ctx, nil)
	require.NoError(s.T(), err)
	found := map[string]*mcp.Tool{}
	for _, t := range res.Tools {
		found[t.Name] = t
	}
	require.Contains(s.T(), found, "plugin_echo")
	require.Equal(s.T(), "Echoes text", found["plugin_echo"].Description)
	require.Nil(s.T(), found["plugin_echo"].OutputSchema)
	require.Contains(s.T(), found, "plugin_score")
	out, err := json.Marshal(found["plugin_score"].OutputSchema)
	require.NoError(s.T(), err)
	require.JSONEq(s.T(), `{"type":"object","properties":{"score":{"type":"integer"}},"required":["score"]}`, string(out))
}

func (s *PluginToolSuite) TestCallStructured() {
	s.httpClient.doFunc = func(*http.Request) (*http.Response, error) {
		return stringResponse(200, `{"content":"{\"score\":7}","is_error":false,"structured_content":{"score":7}}`), nil
	}
	res, err := s.session.CallTool(s.ctx, &mcp.CallToolParams{Name: "plugin_score", Arguments: map[string]any{}})
	require.NoError(s.T(), err)
	require.False(s.T(), res.IsError)
	require.Equal(s.T(), `{"score":7}`, res.Content[0].(*mcp.TextContent).Text)
	out, err := json.Marshal(res.StructuredContent)
	require.NoError(s.T(), err)
	require.JSONEq(s.T(), `{"score":7}`, string(out))
}

func (s *PluginToolSuite) TestCall() {
	tests := []struct {
		name     string
		args     map[string]any
		status   int
		response string
		err      error
		wantBody string
		wantText string
		wantErr  bool
	}{
		{
			name:     "answer",
			args:     map[string]any{"text": "hi"},
			status:   200,
			response: `{"content":"hi","is_error":false}`,
			wantBody: `{"input":{"text":"hi"}}`,
			wantText: "hi",
		},
		{
			name:     "no arguments",
			status:   200,
			response: `{"content":"empty","is_error":false}`,
			wantBody: `{"input":{}}`,
			wantText: "empty",
		},
		{
			name:     "plugin error",
			args:     map[string]any{"text": "hi"},
			status:   200,
			response: `{"content":"nope","is_error":true}`,
			wantBody: `{"input":{"text":"hi"}}`,
			wantText: "nope",
			wantErr:  true,
		},
		{
			name:     "api refusal",
			args:     map[string]any{"text": "hi"},
			status:   403,
			response: `refused`,
			wantBody: `{"input":{"text":"hi"}}`,
			wantText: "API error (status 403): refused",
			wantErr:  true,
		},
		{
			name:     "transport",
			args:     map[string]any{"text": "hi"},
			err:      errors.New("down"),
			wantBody: `{"input":{"text":"hi"}}`,
			wantText: "calling API: down",
			wantErr:  true,
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			var gotURL, gotBody string
			s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
				gotURL = req.URL.String()
				b, _ := io.ReadAll(req.Body)
				gotBody = string(b)
				if tc.err != nil {
					return nil, tc.err
				}
				return stringResponse(tc.status, tc.response), nil
			}
			text, isError := s.callTool("plugin_echo", tc.args)
			require.Equal(s.T(), "http://localhost:8222/api/plugins/echo/tools/echo", gotURL)
			require.JSONEq(s.T(), tc.wantBody, gotBody)
			require.Equal(s.T(), tc.wantText, text)
			require.Equal(s.T(), tc.wantErr, isError)
		})
	}
}
