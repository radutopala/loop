package mcpbrowser

import (
	"encoding/base64"
	"net/http"
	"os"
	"path/filepath"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/browser"
)

func (s *ServerSuite) TestFormInput() {
	inView := actionResponse{Result: "[0,0]"}
	ok := actionResponse{Result: "ok"}
	tests := []struct {
		name    string
		ref     int
		resps   []actionResponse // one per call, in order
		actions []string
		isError bool
		text    string
	}{
		{
			name:    "types into a field",
			ref:     1,
			resps:   []actionResponse{inView, {Result: ""}, ok, ok, ok},
			actions: []string{"evaluate_js", "evaluate_js", "mouse_click", "key_press", "type_text"},
			text:    `Entered "France" in ref_1`,
		},
		{
			name:    "picks a select's option",
			ref:     1,
			resps:   []actionResponse{inView, {Result: "selected"}},
			actions: []string{"evaluate_js", "evaluate_js"},
			text:    `Selected "France" in ref_1`,
		},
		{
			name:    "a select without the option",
			ref:     1,
			resps:   []actionResponse{inView, {Result: `no option "France"; the options are: Romania`}},
			actions: []string{"evaluate_js", "evaluate_js"},
			isError: true,
			text:    `form_input failed: no option "France"; the options are: Romania`,
		},
		{name: "ref out of range", ref: 5, isError: true, text: "ref 5 out of range (1-1)"},
		{name: "ref zero", ref: 0, isError: true, text: "ref 0 out of range (1-1)"},
		{
			name:    "scrolling fails",
			ref:     1,
			resps:   []actionResponse{{Error: "tab gone"}},
			actions: []string{"evaluate_js"},
			isError: true,
			text:    "scrolling ref_1 into view failed: tab gone",
		},
		{
			name:    "the select check fails",
			ref:     1,
			resps:   []actionResponse{inView, {Error: "eval err"}},
			actions: []string{"evaluate_js", "evaluate_js"},
			isError: true,
			text:    "form_input failed: eval err",
		},
		{
			name:    "click fails",
			ref:     1,
			resps:   []actionResponse{inView, {Result: ""}, {Error: "click err"}},
			actions: []string{"evaluate_js", "evaluate_js", "mouse_click"},
			isError: true,
			text:    "click failed: click err",
		},
		{
			name:    "select all fails",
			ref:     1,
			resps:   []actionResponse{inView, {Result: ""}, ok, {Error: "key err"}},
			actions: []string{"evaluate_js", "evaluate_js", "mouse_click", "key_press"},
			isError: true,
			text:    "select all failed: key err",
		},
		{
			name:    "typing fails",
			ref:     1,
			resps:   []actionResponse{inView, {Result: ""}, ok, ok, {Error: "type err"}},
			actions: []string{"evaluate_js", "evaluate_js", "mouse_click", "key_press", "type_text"},
			isError: true,
			text:    "type failed: type err",
		},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			var actions []string
			var click map[string]any
			srv, session := setupTest(s.T(), func(w http.ResponseWriter, r *http.Request) {
				_, action, params := decodeActionRequest(s.T(), r)
				if action == "mouse_click" {
					click = params
				}
				writeJSON(w, tt.resps[len(actions)])
				actions = append(actions, action)
			})
			srv.refs = []browser.ElementRef{{RefID: "ref_1", Role: "combobox", Name: "Country", X: 10, Y: 20, Width: 100, Height: 30}}
			res := callTool(s.T(), session, "form_input", map[string]any{"ref": tt.ref, "value": "France"})
			require.Equal(s.T(), tt.isError, res.IsError)
			require.Contains(s.T(), getText(s.T(), res), tt.text)
			require.Equal(s.T(), tt.actions, actions)
			if click != nil {
				require.Equal(s.T(), float64(60), click["x"])
				require.Equal(s.T(), float64(35), click["y"])
			}
		})
	}
}

// ==================== screenshot ====================

func (s *ServerSuite) TestScreenshotSuccess() {
	imgData := []byte("png-bytes")
	encoded := base64.StdEncoding.EncodeToString(imgData)
	_, session := setupTest(s.T(), func(w http.ResponseWriter, r *http.Request) {
		_, action, _ := decodeActionRequest(s.T(), r)
		require.Equal(s.T(), "screenshot", action)
		writeJSON(w, actionResponse{Image: encoded})
	})
	res := callTool(s.T(), session, "screenshot", nil)
	require.False(s.T(), res.IsError)
	ic, ok := res.Content[0].(*mcp.ImageContent)
	require.True(s.T(), ok)
	require.Equal(s.T(), imgData, ic.Data)
}

func (s *ServerSuite) TestScreenshotError() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{Error: "ss err"})
	})
	res := callTool(s.T(), session, "screenshot", nil)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "screenshot failed: ss err")
}

func (s *ServerSuite) TestScreenshotDecodeError() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{Image: "!!!not-valid-base64!!!"})
	})
	res := callTool(s.T(), session, "screenshot", nil)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "screenshot decode failed")
}

func (s *ServerSuite) TestScreenshotFilePath() {
	// Write a temp file to simulate a file-based screenshot.
	dir := s.T().TempDir()
	fpath := filepath.Join(dir, "screenshot.png")
	require.NoError(s.T(), os.WriteFile(fpath, []byte("png-file-data"), 0o644))

	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{ScreenshotPath: fpath})
	})
	res := callTool(s.T(), session, "screenshot", nil)
	require.False(s.T(), res.IsError)
	ic, ok := res.Content[0].(*mcp.ImageContent)
	require.True(s.T(), ok)
	require.Equal(s.T(), []byte("png-file-data"), ic.Data)

	// File should have been removed after reading.
	_, err := os.Stat(fpath)
	require.True(s.T(), os.IsNotExist(err))
}

func (s *ServerSuite) TestScreenshotFilePathReadError() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{ScreenshotPath: "/nonexistent/screenshot.png"})
	})
	res := callTool(s.T(), session, "screenshot", nil)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "reading screenshot file")
}

// ==================== go_back ====================

func (s *ServerSuite) TestScreenshotFullPage() {
	imgData := []byte("png-bytes")
	encoded := base64.StdEncoding.EncodeToString(imgData)
	tests := []struct {
		name       string
		tool       string
		args       map[string]any
		pageHeight int
		wantNote   string
	}{
		{"fits", "screenshot", map[string]any{"full_page": true}, 3000, ""},
		{"cut", "screenshot", map[string]any{"full_page": true}, 12000, "the page is 12000 px tall, the screenshot shows its top 8000 px; save_screenshot with full_page goes down to 16384 px"},
		{"computer", "computer", map[string]any{"action": "screenshot", "full_page": true}, 9000, "the page is 9000 px tall, the screenshot shows its top 8000 px"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			_, session := setupTest(s.T(), func(w http.ResponseWriter, r *http.Request) {
				_, action, params := decodeActionRequest(s.T(), r)
				require.Equal(s.T(), "screenshot", action)
				require.Equal(s.T(), map[string]any{"full_page": true, "max_height": float64(8000)}, params)
				writeJSON(w, actionResponse{Image: encoded, PageHeight: tt.pageHeight})
			})
			res := callTool(s.T(), session, tt.tool, tt.args)
			require.False(s.T(), res.IsError)
			ic, ok := res.Content[0].(*mcp.ImageContent)
			require.True(s.T(), ok)
			require.Equal(s.T(), imgData, ic.Data)
			if tt.wantNote == "" {
				require.Len(s.T(), res.Content, 1)
				return
			}
			require.Len(s.T(), res.Content, 2)
			require.Contains(s.T(), res.Content[1].(*mcp.TextContent).Text, tt.wantNote)
		})
	}
}

func (s *ServerSuite) TestScreenshotViewportSendsNoParams() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, r *http.Request) {
		_, _, params := decodeActionRequest(s.T(), r)
		require.Empty(s.T(), params)
		writeJSON(w, actionResponse{Image: base64.StdEncoding.EncodeToString([]byte("x"))})
	})
	res := callTool(s.T(), session, "screenshot", nil)
	require.False(s.T(), res.IsError)
	require.Len(s.T(), res.Content, 1)
}
