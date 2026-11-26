package mcpbrowser

import (
	"encoding/base64"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/stretchr/testify/require"
)

func (s *ServerSuite) TestSaveScreenshotSuccess() {
	imgData := []byte("fake-png-bytes")
	encoded := base64.StdEncoding.EncodeToString(imgData)
	_, session := setupTest(s.T(), func(w http.ResponseWriter, r *http.Request) {
		_, action, _ := decodeActionRequest(s.T(), r)
		require.Equal(s.T(), "screenshot", action)
		writeJSON(w, actionResponse{Image: encoded})
	})
	// Path includes a not-yet-existing parent dir to exercise MkdirAll.
	path := filepath.Join(s.T().TempDir(), "sub", "shot.png")
	res := callTool(s.T(), session, "save_screenshot", map[string]any{"path": path})

	require.False(s.T(), res.IsError)
	text := getText(s.T(), res)
	require.Contains(s.T(), text, "Saved screenshot to "+path)
	require.Contains(s.T(), text, "14 bytes")

	written, err := os.ReadFile(path)
	require.NoError(s.T(), err)
	require.Equal(s.T(), imgData, written)
}

func (s *ServerSuite) TestSaveScreenshotEmptyPath() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{Image: ""})
	})
	res := callTool(s.T(), session, "save_screenshot", map[string]any{"path": "  "})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "path is required")
}

func (s *ServerSuite) TestSaveScreenshotCaptureError() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{Error: "ss err"})
	})
	path := filepath.Join(s.T().TempDir(), "shot.png")
	res := callTool(s.T(), session, "save_screenshot", map[string]any{"path": path})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "screenshot failed: ss err")
}

func (s *ServerSuite) TestSaveScreenshotMkdirError() {
	encoded := base64.StdEncoding.EncodeToString([]byte("png"))
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{Image: encoded})
	})
	// A regular file used as a parent directory makes MkdirAll fail.
	file := filepath.Join(s.T().TempDir(), "afile")
	require.NoError(s.T(), os.WriteFile(file, []byte("x"), 0o644))
	res := callTool(s.T(), session, "save_screenshot", map[string]any{"path": filepath.Join(file, "shot.png")})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "creating directory")
}

func (s *ServerSuite) TestSaveScreenshotWriteError() {
	encoded := base64.StdEncoding.EncodeToString([]byte("png"))
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{Image: encoded})
	})
	// Targeting an existing directory makes WriteFile fail.
	dir := s.T().TempDir()
	res := callTool(s.T(), session, "save_screenshot", map[string]any{"path": dir})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "writing screenshot file")
}

func (s *ServerSuite) TestSaveScreenshotFullPage() {
	tests := []struct {
		name       string
		pageHeight int
		wantCut    bool
	}{
		{"fits", 9000, false},
		{"cut", 20000, true},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			_, session := setupTest(s.T(), func(w http.ResponseWriter, r *http.Request) {
				_, _, params := decodeActionRequest(s.T(), r)
				require.Equal(s.T(), map[string]any{"full_page": true, "max_height": float64(16384)}, params)
				writeJSON(w, actionResponse{Image: base64.StdEncoding.EncodeToString([]byte("png")), PageHeight: tt.pageHeight})
			})
			path := filepath.Join(s.T().TempDir(), "shot.png")
			res := callTool(s.T(), session, "save_screenshot", map[string]any{"path": path, "full_page": true})
			require.False(s.T(), res.IsError)
			text := getText(s.T(), res)
			require.Contains(s.T(), text, "Saved screenshot to "+path+" (3 bytes)")
			require.Equal(s.T(), tt.wantCut, strings.Contains(text, "; the page is 20000 px tall, the screenshot shows its top 16384 px"))
		})
	}
}
