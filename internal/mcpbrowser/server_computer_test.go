package mcpbrowser

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/chromedp/cdproto/cdp"
	"github.com/radutopala/loop/internal/browser"
)

func (s *ServerSuite) TestComputerClick() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, r *http.Request) {
		_, action, params := decodeActionRequest(s.T(), r)
		require.Equal(s.T(), "mouse_click", action)
		require.Equal(s.T(), float64(10), params["x"])
		require.Equal(s.T(), float64(20), params["y"])
		require.Equal(s.T(), "left", params["button"])
		require.Equal(s.T(), float64(1), params["click_count"])
		writeJSON(w, actionResponse{Result: "ok"})
	})
	res := callTool(s.T(), session, "computer", map[string]any{"action": "click", "x": 10.0, "y": 20.0})
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "Clicked at (10, 20)")
}

func (s *ServerSuite) TestComputerClickError() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{Error: "click err"})
	})
	res := callTool(s.T(), session, "computer", map[string]any{"action": "click", "x": 10.0, "y": 20.0})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "click failed: click err")
}

func (s *ServerSuite) TestComputerClickWithButton() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, r *http.Request) {
		_, action, params := decodeActionRequest(s.T(), r)
		require.Equal(s.T(), "mouse_click", action)
		require.Equal(s.T(), "right", params["button"])
		writeJSON(w, actionResponse{Result: "ok"})
	})
	res := callTool(s.T(), session, "computer", map[string]any{"action": "click", "button": "right"})
	require.False(s.T(), res.IsError)
}

func (s *ServerSuite) TestComputerDoubleClick() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, r *http.Request) {
		_, _, params := decodeActionRequest(s.T(), r)
		require.Equal(s.T(), float64(2), params["click_count"])
		writeJSON(w, actionResponse{Result: "ok"})
	})
	res := callTool(s.T(), session, "computer", map[string]any{"action": "double_click", "x": 50.0, "y": 60.0})
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "Double-clicked at (50, 60)")
}

func (s *ServerSuite) TestComputerDoubleClickError() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{Error: "dc err"})
	})
	res := callTool(s.T(), session, "computer", map[string]any{"action": "double_click"})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "double click failed: dc err")
}

func (s *ServerSuite) TestComputerTripleClick() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, r *http.Request) {
		_, _, params := decodeActionRequest(s.T(), r)
		require.Equal(s.T(), float64(3), params["click_count"])
		writeJSON(w, actionResponse{Result: "ok"})
	})
	res := callTool(s.T(), session, "computer", map[string]any{"action": "triple_click", "x": 10.0, "y": 20.0})
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "Triple-clicked at (10, 20)")
}

func (s *ServerSuite) TestComputerTripleClickError() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{Error: "tc err"})
	})
	res := callTool(s.T(), session, "computer", map[string]any{"action": "triple_click"})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "triple click failed: tc err")
}

func (s *ServerSuite) TestComputerType() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, r *http.Request) {
		_, action, params := decodeActionRequest(s.T(), r)
		require.Equal(s.T(), "type_text", action)
		require.Equal(s.T(), "hello world", params["text"])
		writeJSON(w, actionResponse{Result: "ok"})
	})
	res := callTool(s.T(), session, "computer", map[string]any{"action": "type", "text": "hello world"})
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), `Typed "hello world"`)
}

func (s *ServerSuite) TestComputerTypeError() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{Error: "type err"})
	})
	res := callTool(s.T(), session, "computer", map[string]any{"action": "type", "text": "x"})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "type failed: type err")
}

func (s *ServerSuite) TestComputerTypeNoText() {
	srv := New("http://x", "ch", nil)
	res, _, err := srv.handleComputer(context.Background(), computerInput{Action: "type"})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "text is required for type action")
}

func (s *ServerSuite) TestComputerKey() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, r *http.Request) {
		_, action, params := decodeActionRequest(s.T(), r)
		require.Equal(s.T(), "key_press", action)
		require.Equal(s.T(), "Enter", params["key"])
		writeJSON(w, actionResponse{Result: "ok"})
	})
	res := callTool(s.T(), session, "computer", map[string]any{"action": "key", "text": "Enter"})
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), `Pressed key "Enter"`)
}

func (s *ServerSuite) TestComputerKeyError() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{Error: "key err"})
	})
	res := callTool(s.T(), session, "computer", map[string]any{"action": "key", "text": "Tab"})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "key failed: key err")
}

func (s *ServerSuite) TestComputerKeyNoText() {
	srv := New("http://x", "ch", nil)
	res, _, err := srv.handleComputer(context.Background(), computerInput{Action: "key"})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "text is required for key action")
}

func (s *ServerSuite) TestComputerScrollDefault() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, r *http.Request) {
		_, action, params := decodeActionRequest(s.T(), r)
		require.Equal(s.T(), "mouse_scroll", action)
		require.Equal(s.T(), float64(-3), params["delta_y"])
		writeJSON(w, actionResponse{Result: "ok"})
	})
	res := callTool(s.T(), session, "computer", map[string]any{"action": "scroll"})
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "Scrolled at (0, 0)")
}

func (s *ServerSuite) TestComputerScrollExplicit() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, r *http.Request) {
		_, action, params := decodeActionRequest(s.T(), r)
		require.Equal(s.T(), "mouse_scroll", action)
		require.Equal(s.T(), float64(5), params["delta_y"])
		writeJSON(w, actionResponse{Result: "ok"})
	})
	res := callTool(s.T(), session, "computer", map[string]any{"action": "scroll", "x": 50.0, "y": 60.0, "delta_x": 1.0, "delta_y": 5.0})
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "Scrolled at (50, 60)")
}

func (s *ServerSuite) TestComputerScrollError() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{Error: "scroll err"})
	})
	res := callTool(s.T(), session, "computer", map[string]any{"action": "scroll"})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "scroll failed: scroll err")
}

func (s *ServerSuite) TestComputerMove() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, r *http.Request) {
		_, action, _ := decodeActionRequest(s.T(), r)
		require.Equal(s.T(), "mouse_move", action)
		writeJSON(w, actionResponse{Result: "ok"})
	})
	res := callTool(s.T(), session, "computer", map[string]any{"action": "move", "x": 300.0, "y": 400.0})
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "Moved to (300, 400)")
}

func (s *ServerSuite) TestComputerMoveError() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{Error: "move err"})
	})
	res := callTool(s.T(), session, "computer", map[string]any{"action": "move"})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "move failed: move err")
}

func (s *ServerSuite) TestComputerHover() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, r *http.Request) {
		_, action, _ := decodeActionRequest(s.T(), r)
		require.Equal(s.T(), "mouse_move", action)
		writeJSON(w, actionResponse{Result: "ok"})
	})
	res := callTool(s.T(), session, "computer", map[string]any{"action": "hover", "x": 100.0, "y": 200.0})
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "Moved to (100, 200)")
}

func (s *ServerSuite) TestComputerHoverError() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{Error: "hover err"})
	})
	res := callTool(s.T(), session, "computer", map[string]any{"action": "hover"})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "move failed: hover err")
}

func (s *ServerSuite) TestComputerRightClick() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, r *http.Request) {
		_, _, params := decodeActionRequest(s.T(), r)
		require.Equal(s.T(), "right", params["button"])
		writeJSON(w, actionResponse{Result: "ok"})
	})
	res := callTool(s.T(), session, "computer", map[string]any{"action": "right_click", "x": 50.0, "y": 60.0})
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "Right-clicked at (50, 60)")
}

func (s *ServerSuite) TestComputerRightClickError() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{Error: "rc err"})
	})
	res := callTool(s.T(), session, "computer", map[string]any{"action": "right_click"})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "right click failed: rc err")
}

func (s *ServerSuite) TestComputerRightClickIgnoresButtonParam() {
	// right_click always uses "right" regardless of button param.
	_, session := setupTest(s.T(), func(w http.ResponseWriter, r *http.Request) {
		_, _, params := decodeActionRequest(s.T(), r)
		require.Equal(s.T(), "right", params["button"])
		writeJSON(w, actionResponse{Result: "ok"})
	})
	res := callTool(s.T(), session, "computer", map[string]any{"action": "right_click", "button": "left"})
	require.False(s.T(), res.IsError)
}

func (s *ServerSuite) TestComputerScreenshot() {
	imgData := []byte("fake-png-bytes")
	encoded := base64.StdEncoding.EncodeToString(imgData)
	_, session := setupTest(s.T(), func(w http.ResponseWriter, r *http.Request) {
		_, action, _ := decodeActionRequest(s.T(), r)
		require.Equal(s.T(), "screenshot", action)
		writeJSON(w, actionResponse{Image: encoded})
	})
	res := callTool(s.T(), session, "computer", map[string]any{"action": "screenshot"})
	require.False(s.T(), res.IsError)
	ic, ok := res.Content[0].(*mcp.ImageContent)
	require.True(s.T(), ok)
	require.Equal(s.T(), imgData, ic.Data)
}

func (s *ServerSuite) TestComputerScreenshotError() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{Error: "ss err"})
	})
	res := callTool(s.T(), session, "computer", map[string]any{"action": "screenshot"})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "screenshot failed: ss err")
}

func (s *ServerSuite) TestComputerScreenshotDecodeError() {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{Image: "!!!not-valid-base64!!!"})
	}))
	defer ts.Close()
	srv := New(ts.URL, "ch", nil)
	srv.httpClient = ts.Client()
	res, _, err := srv.handleComputer(context.Background(), computerInput{Action: "screenshot"})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "screenshot decode failed")
}

func (s *ServerSuite) TestComputerScreenshotFilePath() {
	dir := s.T().TempDir()
	fpath := filepath.Join(dir, "screenshot.png")
	require.NoError(s.T(), os.WriteFile(fpath, []byte("computer-png"), 0o644))

	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{ScreenshotPath: fpath})
	})
	res := callTool(s.T(), session, "computer", map[string]any{"action": "screenshot"})
	require.False(s.T(), res.IsError)
	ic, ok := res.Content[0].(*mcp.ImageContent)
	require.True(s.T(), ok)
	require.Equal(s.T(), []byte("computer-png"), ic.Data)

	// File should have been removed.
	_, err := os.Stat(fpath)
	require.True(s.T(), os.IsNotExist(err))
}

func (s *ServerSuite) TestComputerScreenshotFilePathReadError() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{ScreenshotPath: "/nonexistent/file.png"})
	})
	res := callTool(s.T(), session, "computer", map[string]any{"action": "screenshot"})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "reading screenshot file")
}

func (s *ServerSuite) TestComputerWait() {
	srv := New("http://x", "ch", nil)
	res, _, err := srv.handleComputer(context.Background(), computerInput{Action: "wait"})
	require.NoError(s.T(), err)
	require.False(s.T(), res.IsError)
	require.Equal(s.T(), "Waited", getText(s.T(), res))
}

func (s *ServerSuite) TestComputerUnknownAction() {
	srv := New("http://x", "ch", nil)
	res, _, err := srv.handleComputer(context.Background(), computerInput{Action: "fly"})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "unknown action: fly")
}

func (s *ServerSuite) TestComputerRefOutOfRange() {
	srv := New("http://x", "ch", nil)
	srv.refs = []browser.ElementRef{{RefID: "ref_1"}}
	res, _, err := srv.handleComputer(context.Background(), computerInput{Action: "click", Ref: 5})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "ref 5 out of range")
}

func (s *ServerSuite) TestComputerRefResolution() {
	// Ref 1 -> center is (100+20, 200+10) = (120, 210)
	srv, session := setupTest(s.T(), func(w http.ResponseWriter, r *http.Request) {
		_, _, params := decodeActionRequest(s.T(), r)
		require.Equal(s.T(), float64(120), params["x"])
		require.Equal(s.T(), float64(210), params["y"])
		writeJSON(w, actionResponse{Result: "ok"})
	})
	srv.refs = []browser.ElementRef{
		{RefID: "ref_1", X: 100, Y: 200, Width: 40, Height: 20},
	}
	res := callTool(s.T(), session, "computer", map[string]any{"action": "click", "ref": 1})
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "Clicked at (120, 210)")
}

func (s *ServerSuite) TestComputerLeftClickDrag() {
	callCount := 0
	actions := []string{}
	_, session := setupTest(s.T(), func(w http.ResponseWriter, r *http.Request) {
		_, action, _ := decodeActionRequest(s.T(), r)
		callCount++
		actions = append(actions, action)
		writeJSON(w, actionResponse{Result: "ok"})
	})
	res := callTool(s.T(), session, "computer", map[string]any{
		"action":  "left_click_drag",
		"start_x": 10.0,
		"start_y": 20.0,
		"x":       100.0,
		"y":       200.0,
	})
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "Dragged from (10, 20) to (100, 200)")
	require.Equal(s.T(), 4, callCount)
	require.Equal(s.T(), []string{"mouse_move", "mouse_down", "mouse_move", "mouse_up"}, actions)
}

func (s *ServerSuite) TestComputerLeftClickDragMoveToStartError() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{Error: "start err"})
	})
	res := callTool(s.T(), session, "computer", map[string]any{"action": "left_click_drag", "start_x": 10.0, "start_y": 20.0, "x": 100.0, "y": 200.0})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "move to start failed: start err")
}

func (s *ServerSuite) TestComputerLeftClickDragMouseDownError() {
	callCount := 0
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		callCount++
		if callCount == 1 {
			writeJSON(w, actionResponse{Result: "ok"}) // move to start ok
		} else {
			writeJSON(w, actionResponse{Error: "down err"}) // mouse_down fails
		}
	})
	res := callTool(s.T(), session, "computer", map[string]any{"action": "left_click_drag", "start_x": 10.0, "start_y": 20.0, "x": 100.0, "y": 200.0})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "mouse down failed: down err")
}

func (s *ServerSuite) TestComputerLeftClickDragMoveError() {
	callCount := 0
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		callCount++
		if callCount <= 2 {
			writeJSON(w, actionResponse{Result: "ok"}) // move to start + mouse_down ok
		} else {
			writeJSON(w, actionResponse{Error: "move err"}) // drag move fails
		}
	})
	res := callTool(s.T(), session, "computer", map[string]any{"action": "left_click_drag", "start_x": 10.0, "start_y": 20.0, "x": 100.0, "y": 200.0})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "drag move failed: move err")
}

func (s *ServerSuite) TestComputerLeftClickDragMouseUpError() {
	callCount := 0
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		callCount++
		if callCount < 4 {
			writeJSON(w, actionResponse{Result: "ok"})
		} else {
			writeJSON(w, actionResponse{Error: "up err"})
		}
	})
	res := callTool(s.T(), session, "computer", map[string]any{"action": "left_click_drag", "start_x": 10.0, "start_y": 20.0, "x": 100.0, "y": 200.0})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "mouse up failed: up err")
}

func (s *ServerSuite) TestComputerScrollTo() {
	srv, session := setupTest(s.T(), func(w http.ResponseWriter, r *http.Request) {
		_, action, params := decodeActionRequest(s.T(), r)
		require.Equal(s.T(), "scroll_into_view", action)
		require.Equal(s.T(), float64(42), params["backend_node_id"])
		writeJSON(w, actionResponse{Result: "ok"})
	})
	srv.refs = []browser.ElementRef{
		{RefID: "ref_1", Role: "button", Name: "Submit", BackendDOMNodeID: cdp.BackendNodeID(42)},
	}
	res := callTool(s.T(), session, "computer", map[string]any{"action": "scroll_to", "ref": 1})
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "Scrolled ref 1")
}

func (s *ServerSuite) TestComputerScrollToOutOfRange() {
	srv := New("http://x", "ch", nil)
	srv.refs = []browser.ElementRef{}
	res, _, err := srv.handleComputer(context.Background(), computerInput{Action: "scroll_to", Ref: 5})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "out of range")
}

func (s *ServerSuite) TestComputerScrollToRefZero() {
	// Ref=0 skips the top coordinate-resolution check (which only fires when Ref > 0)
	// and reaches the scroll_to case where Ref < 1 triggers the out-of-range error.
	srv := New("http://x", "ch", nil)
	srv.refs = []browser.ElementRef{{RefID: "ref_1", Role: "button", Name: "OK"}}
	res, _, err := srv.handleComputer(context.Background(), computerInput{Action: "scroll_to", Ref: 0})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "out of range")
}

func (s *ServerSuite) TestComputerScrollToError() {
	srv, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{Error: "scroll failed"})
	})
	srv.refs = []browser.ElementRef{
		{RefID: "ref_1", Role: "link", Name: "Home"},
	}
	res := callTool(s.T(), session, "computer", map[string]any{"action": "scroll_to", "ref": 1})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "scroll_to failed")
}

// ==================== form_input ====================
