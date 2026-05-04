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

func (s *ServerSuite) TestFormInputSuccess() {
	callCount := 0
	srv, session := setupTest(s.T(), func(w http.ResponseWriter, r *http.Request) {
		_, action, _ := decodeActionRequest(s.T(), r)
		callCount++
		switch action {
		case "click_ref":
		case "key_press":
		case "type_text":
		}
		writeJSON(w, actionResponse{Result: "ok"})
	})
	srv.refs = []browser.ElementRef{{RefID: "ref_1", Role: "textbox", Name: "Name"}}
	res := callTool(s.T(), session, "form_input", map[string]any{"ref": 1, "value": "John"})
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), `Entered "John" in ref_1`)
	require.Equal(s.T(), 3, callCount)
}

func (s *ServerSuite) TestFormInputRefOutOfRange() {
	srv, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{Result: "ok"})
	})
	srv.refs = []browser.ElementRef{{RefID: "ref_1"}}
	res := callTool(s.T(), session, "form_input", map[string]any{"ref": 5, "value": "x"})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "ref 5 out of range")
}

func (s *ServerSuite) TestFormInputRefZero() {
	srv, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{Result: "ok"})
	})
	srv.refs = []browser.ElementRef{{RefID: "ref_1"}}
	res := callTool(s.T(), session, "form_input", map[string]any{"ref": 0, "value": "x"})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "ref 0 out of range")
}

func (s *ServerSuite) TestFormInputClickError() {
	srv, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{Error: "click err"})
	})
	srv.refs = []browser.ElementRef{{RefID: "ref_1"}}
	res := callTool(s.T(), session, "form_input", map[string]any{"ref": 1, "value": "x"})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "click failed: click err")
}

func (s *ServerSuite) TestFormInputKeyPressError() {
	callCount := 0
	srv, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		callCount++
		if callCount == 1 {
			writeJSON(w, actionResponse{Result: "ok"}) // click ok
		} else {
			writeJSON(w, actionResponse{Error: "key err"})
		}
	})
	srv.refs = []browser.ElementRef{{RefID: "ref_1"}}
	res := callTool(s.T(), session, "form_input", map[string]any{"ref": 1, "value": "x"})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "select all failed: key err")
}

func (s *ServerSuite) TestFormInputTypeError() {
	callCount := 0
	srv, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		callCount++
		if callCount < 3 {
			writeJSON(w, actionResponse{Result: "ok"})
		} else {
			writeJSON(w, actionResponse{Error: "type err"})
		}
	})
	srv.refs = []browser.ElementRef{{RefID: "ref_1"}}
	res := callTool(s.T(), session, "form_input", map[string]any{"ref": 1, "value": "x"})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "type failed: type err")
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
