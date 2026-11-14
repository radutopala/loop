package mcpbrowser

import (
	"context"
	"net/http"

	"github.com/stretchr/testify/require"
)

func (s *ServerSuite) TestHandleDialog() {
	tests := []struct {
		name       string
		args       map[string]any
		wantParams map[string]any
		resp       actionResponse
		isError    bool
		text       string
	}{
		{
			name:       "accepts a prompt with text",
			args:       map[string]any{"accept": true, "prompt_text": "alice"},
			wantParams: map[string]any{"accept": true, "prompt_text": "alice"},
			resp:       actionResponse{Result: `Accepted the prompt "Name?"`},
			text:       `Accepted the prompt "Name?"`,
		},
		{
			name:       "dismisses",
			args:       map[string]any{"accept": false},
			wantParams: map[string]any{"accept": false, "prompt_text": ""},
			resp:       actionResponse{Error: "no JavaScript dialog is open on this tab"},
			isError:    true,
			text:       "handle dialog failed: no JavaScript dialog is open on this tab",
		},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			_, session := setupTest(s.T(), func(w http.ResponseWriter, r *http.Request) {
				channelID, action, params := decodeActionRequest(s.T(), r)
				require.Equal(s.T(), "test-ch", channelID)
				require.Equal(s.T(), "handle_dialog", action)
				require.Equal(s.T(), tt.wantParams, params)
				writeJSON(w, tt.resp)
			})
			res := callTool(s.T(), session, "handle_dialog", tt.args)
			require.Equal(s.T(), tt.isError, res.IsError)
			require.Contains(s.T(), getText(s.T(), res), tt.text)
		})
	}
}

func (s *ServerSuite) TestNewDirectHasNoHandleDialog() {
	// The standalone server attaches anew for every call, and only the
	// session that saw a dialog open can answer it.
	session := connectClient(s.T(), NewDirect("ws://x", nil))
	tools, err := session.ListTools(context.Background(), nil)
	require.NoError(s.T(), err)
	for _, tool := range tools.Tools {
		require.NotEqual(s.T(), "handle_dialog", tool.Name)
	}
}
