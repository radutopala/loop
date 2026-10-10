package mcpserver

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"

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
	}})}
	suite.Run(t, s)
}

func (s *PluginToolSuite) TestListed() {
	res, err := s.session.ListTools(s.ctx, nil)
	require.NoError(s.T(), err)
	var found bool
	for _, t := range res.Tools {
		if t.Name == "plugin_echo" {
			found = true
			require.Equal(s.T(), "Echoes text", t.Description)
		}
	}
	require.True(s.T(), found)
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
