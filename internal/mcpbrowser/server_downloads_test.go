package mcpbrowser

import (
	"context"
	"net/http"

	"github.com/stretchr/testify/require"
)

func (s *ServerSuite) TestListDownloads() {
	tests := []struct {
		name    string
		resp    actionResponse
		isError bool
		text    string
	}{
		{name: "success", resp: actionResponse{Result: "Downloads in /d:\n- /d/a.pdf (4 bytes)\n"}, text: "- /d/a.pdf (4 bytes)"},
		{name: "error", resp: actionResponse{Error: "downloads are off for this browser"}, isError: true, text: "list downloads failed: downloads are off for this browser"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			_, session := setupTest(s.T(), func(w http.ResponseWriter, r *http.Request) {
				channelID, action, _ := decodeActionRequest(s.T(), r)
				require.Equal(s.T(), "test-ch", channelID)
				require.Equal(s.T(), "list_downloads", action)
				writeJSON(w, tt.resp)
			})
			res := callTool(s.T(), session, "list_downloads", nil)
			require.Equal(s.T(), tt.isError, res.IsError)
			require.Contains(s.T(), getText(s.T(), res), tt.text)
		})
	}
}

func (s *ServerSuite) TestNewDirectHasNoListDownloads() {
	// The standalone server drives the user's own Chrome, whose downloads the
	// agent can't see.
	session := connectClient(s.T(), NewDirect("ws://x", nil))
	tools, err := session.ListTools(context.Background(), nil)
	require.NoError(s.T(), err)
	for _, tool := range tools.Tools {
		require.NotEqual(s.T(), "list_downloads", tool.Name)
	}
}
