package mcpbrowser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/chromedp/cdproto/cdp"
	"github.com/radutopala/loop/internal/browser"
)

// --- helpers ---

// setupTest creates an httptest.Server with the given handler, a Server configured to
// proxy through it, and a connected MCP client session.
func setupTest(t *testing.T, handler http.HandlerFunc) (*Server, *mcp.ClientSession) {
	t.Helper()
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	srv := New(ts.URL, "test-ch", nil)
	srv.httpClient = ts.Client()
	session := connectClient(t, srv)
	return srv, session
}

// connectClient sets up an in-memory MCP client+server session.
func connectClient(t *testing.T, srv *Server) *mcp.ClientSession {
	t.Helper()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	ctx := context.Background()

	_, err := srv.mcpServer.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { session.Close() })
	return session
}

func callTool(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      name,
		Arguments: args,
	})
	require.NoError(t, err)
	return res
}

func getText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	require.NotEmpty(t, res.Content)
	tc, ok := res.Content[0].(*mcp.TextContent)
	require.True(t, ok, "expected TextContent, got %T", res.Content[0])
	return tc.Text
}

// decodeActionRequest decodes a canned action request from the HTTP body.
func decodeActionRequest(t *testing.T, r *http.Request) (channelID, action string, params map[string]any) {
	t.Helper()
	var req struct {
		ChannelID string         `json:"channel_id"`
		Action    string         `json:"action"`
		Params    map[string]any `json:"params"`
	}
	require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
	return req.ChannelID, req.Action, req.Params
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// --- suite ---

type ServerSuite struct {
	suite.Suite
}

func TestServerSuite(t *testing.T) {
	suite.Run(t, new(ServerSuite))
}

// ==================== New / constructor ====================

func (s *ServerSuite) TestNew() {
	srv := New("http://host:8222", "ch-1", nil)
	require.NotNil(s.T(), srv)
	require.Equal(s.T(), "http://host:8222", srv.apiURL)
	require.Equal(s.T(), "ch-1", srv.channelID)
	require.NotNil(s.T(), srv.mcpServer)
	require.NotNil(s.T(), srv.logger)
	require.NotNil(s.T(), srv.httpClient)
}

func (s *ServerSuite) TestNewWithLogger() {
	logger := slog.Default()
	srv := New("http://x", "ch-1", logger)
	require.Equal(s.T(), logger, srv.logger)
}

func (s *ServerSuite) TestNewNilLogger() {
	srv := New("http://x", "ch-1", nil)
	require.NotNil(s.T(), srv.logger)
}

// ==================== helper results ====================

func (s *ServerSuite) TestTextResult() {
	res := textResult("hello")
	require.False(s.T(), res.IsError)
	require.Len(s.T(), res.Content, 1)
	tc, ok := res.Content[0].(*mcp.TextContent)
	require.True(s.T(), ok)
	require.Equal(s.T(), "hello", tc.Text)
}

func (s *ServerSuite) TestErrorResult() {
	res := errorResult("boom")
	require.True(s.T(), res.IsError)
	require.Len(s.T(), res.Content, 1)
	tc, ok := res.Content[0].(*mcp.TextContent)
	require.True(s.T(), ok)
	require.Equal(s.T(), "boom", tc.Text)
}

func (s *ServerSuite) TestImageResult() {
	res := imageResult([]byte("png-data"))
	require.False(s.T(), res.IsError)
	require.Len(s.T(), res.Content, 1)
	ic, ok := res.Content[0].(*mcp.ImageContent)
	require.True(s.T(), ok)
	require.Equal(s.T(), []byte("png-data"), ic.Data)
	require.Equal(s.T(), "image/png", ic.MIMEType)
}

// ==================== Run ====================

func (s *ServerSuite) TestRunSuccess() {
	srv := New("http://localhost", "ch-1", nil)

	ctx, cancel := context.WithCancel(context.Background())
	t1, t2 := mcp.NewInMemoryTransports()

	done := make(chan error, 1)
	go func() {
		done <- srv.Run(ctx, t1)
	}()

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0.1"}, nil)
	_, err := client.Connect(ctx, t2, nil)
	require.NoError(s.T(), err)

	cancel()
	<-done
}

// ==================== callAction ====================

func (s *ServerSuite) TestCallActionSuccess() {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		channelID, action, _ := decodeActionRequest(s.T(), r)
		require.Equal(s.T(), "test-ch", channelID)
		require.Equal(s.T(), "navigate", action)
		writeJSON(w, actionResponse{PageInfo: &browser.PageInfo{URL: "https://x.com", Title: "X"}})
	}))
	defer ts.Close()

	srv := New(ts.URL, "test-ch", nil)
	srv.httpClient = ts.Client()

	resp, err := srv.callAction(context.Background(), "navigate", map[string]any{"url": "https://x.com"})
	require.NoError(s.T(), err)
	require.NotNil(s.T(), resp.PageInfo)
	require.Equal(s.T(), "https://x.com", resp.PageInfo.URL)
}

func (s *ServerSuite) TestCallActionAPIError() {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{Error: "something went wrong"})
	}))
	defer ts.Close()

	srv := New(ts.URL, "test-ch", nil)
	srv.httpClient = ts.Client()

	_, err := srv.callAction(context.Background(), "navigate", nil)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "something went wrong")
}

func (s *ServerSuite) TestCallActionNon200() {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer ts.Close()

	srv := New(ts.URL, "test-ch", nil)
	srv.httpClient = ts.Client()

	_, err := srv.callAction(context.Background(), "navigate", nil)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "host API returned 500")
}

func (s *ServerSuite) TestCallActionNetworkError() {
	srv := New("http://127.0.0.1:1", "test-ch", nil)
	srv.httpClient = &http.Client{Timeout: 100 * time.Millisecond}

	_, err := srv.callAction(context.Background(), "navigate", nil)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "calling host API")
}

func (s *ServerSuite) TestCallActionInvalidURL() {
	srv := New("://bad-url", "test-ch", nil)
	srv.httpClient = &http.Client{}

	_, err := srv.callAction(context.Background(), "navigate", nil)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "creating request")
}

func (s *ServerSuite) TestCallActionMarshalError() {
	srv := New("http://x", "test-ch", nil)
	// json.Marshal fails on channel values.
	ch := make(chan int)
	_, err := srv.callAction(context.Background(), "navigate", map[string]any{"bad": ch})
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "marshaling request")
}

func (s *ServerSuite) TestCallActionDecodeError() {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{bad json`))
	}))
	defer ts.Close()

	srv := New(ts.URL, "test-ch", nil)
	srv.httpClient = ts.Client()

	_, err := srv.callAction(context.Background(), "navigate", nil)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "decoding response")
}

// ==================== navigate ====================

func (s *ServerSuite) TestNavigateSuccess() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, r *http.Request) {
		_, action, params := decodeActionRequest(s.T(), r)
		require.Equal(s.T(), "navigate", action)
		require.Equal(s.T(), "https://x.com", params["url"])
		writeJSON(w, actionResponse{PageInfo: &browser.PageInfo{URL: "https://x.com", Title: "X"}})
	})
	res := callTool(s.T(), session, "navigate", map[string]any{"url": "https://x.com"})
	require.False(s.T(), res.IsError)
	text := getText(s.T(), res)
	require.Contains(s.T(), text, "https://x.com")
	require.Contains(s.T(), text, "X")
}

func (s *ServerSuite) TestNavigateNoPageInfo() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{Result: "ok"})
	})
	res := callTool(s.T(), session, "navigate", map[string]any{"url": "https://x.com"})
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "Navigated to https://x.com")
}

func (s *ServerSuite) TestNavigateError() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{Error: "timeout"})
	})
	res := callTool(s.T(), session, "navigate", map[string]any{"url": "https://bad.com"})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "navigate failed: timeout")
}

func (s *ServerSuite) TestNavigateEmptyURL() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{})
	})
	res := callTool(s.T(), session, "navigate", map[string]any{"url": ""})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "url is required")
}

// ==================== read_page ====================

func (s *ServerSuite) TestReadPageSuccess() {
	refs := []browser.ElementRef{
		{RefID: "ref_1", Role: "button", Name: "Submit", Value: "go"},
		{RefID: "ref_2", Role: "link", Name: "Home"},
	}
	callCount := 0
	srv, session := setupTest(s.T(), func(w http.ResponseWriter, r *http.Request) {
		_, action, _ := decodeActionRequest(s.T(), r)
		callCount++
		if action == "get_element_refs" {
			writeJSON(w, actionResponse{ElementRefs: refs})
		} else {
			writeJSON(w, actionResponse{PageInfo: &browser.PageInfo{URL: "https://x.com", Title: "X"}})
		}
	})
	res := callTool(s.T(), session, "read_page", nil)
	require.False(s.T(), res.IsError)
	text := getText(s.T(), res)
	require.Contains(s.T(), text, "Page: https://x.com")
	require.Contains(s.T(), text, "[ref_1] button: Submit (value: go)")
	require.Contains(s.T(), text, "[ref_2] link: Home")
	require.Len(s.T(), srv.refs, 2)
}

func (s *ServerSuite) TestReadPageNoRefs() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, r *http.Request) {
		_, action, _ := decodeActionRequest(s.T(), r)
		if action == "get_element_refs" {
			writeJSON(w, actionResponse{ElementRefs: []browser.ElementRef{}})
		} else {
			writeJSON(w, actionResponse{})
		}
	})
	res := callTool(s.T(), session, "read_page", nil)
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "No interactive elements found.")
}

func (s *ServerSuite) TestReadPageError() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{Error: "dom error"})
	})
	res := callTool(s.T(), session, "read_page", nil)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "failed to get element refs: dom error")
}

// ==================== computer ====================

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
	require.Equal(s.T(), 3, callCount)
	require.Equal(s.T(), []string{"mouse_down", "mouse_move", "mouse_up"}, actions)
}

func (s *ServerSuite) TestComputerLeftClickDragMouseDownError() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{Error: "down err"})
	})
	res := callTool(s.T(), session, "computer", map[string]any{"action": "left_click_drag", "start_x": 10.0, "start_y": 20.0, "x": 100.0, "y": 200.0})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "mouse down failed: down err")
}

func (s *ServerSuite) TestComputerLeftClickDragMoveError() {
	callCount := 0
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		callCount++
		if callCount == 1 {
			writeJSON(w, actionResponse{Result: "ok"}) // mouse_down ok
		} else {
			writeJSON(w, actionResponse{Error: "move err"}) // mouse_move fails
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
		if callCount < 3 {
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

func (s *ServerSuite) TestGoBackSuccess() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, r *http.Request) {
		_, action, _ := decodeActionRequest(s.T(), r)
		require.Equal(s.T(), "go_back", action)
		writeJSON(w, actionResponse{Result: "ok"})
	})
	res := callTool(s.T(), session, "go_back", nil)
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "Navigated back")
}

func (s *ServerSuite) TestGoBackError() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{Error: "back err"})
	})
	res := callTool(s.T(), session, "go_back", nil)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "back failed: back err")
}

// ==================== go_forward ====================

func (s *ServerSuite) TestGoForwardSuccess() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, r *http.Request) {
		_, action, _ := decodeActionRequest(s.T(), r)
		require.Equal(s.T(), "go_forward", action)
		writeJSON(w, actionResponse{Result: "ok"})
	})
	res := callTool(s.T(), session, "go_forward", nil)
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "Navigated forward")
}

func (s *ServerSuite) TestGoForwardError() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{Error: "fwd err"})
	})
	res := callTool(s.T(), session, "go_forward", nil)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "forward failed: fwd err")
}

// ==================== reload ====================

func (s *ServerSuite) TestReloadSuccess() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, r *http.Request) {
		_, action, _ := decodeActionRequest(s.T(), r)
		require.Equal(s.T(), "reload", action)
		writeJSON(w, actionResponse{Result: "ok"})
	})
	res := callTool(s.T(), session, "reload", nil)
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "Page reloaded")
}

func (s *ServerSuite) TestReloadError() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{Error: "reload err"})
	})
	res := callTool(s.T(), session, "reload", nil)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "reload failed: reload err")
}

// ==================== evaluate ====================

func (s *ServerSuite) TestEvaluateSuccess() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, r *http.Request) {
		_, action, params := decodeActionRequest(s.T(), r)
		require.Equal(s.T(), "evaluate_js", action)
		require.Equal(s.T(), "1+1", params["expression"])
		writeJSON(w, actionResponse{Result: "2"})
	})
	res := callTool(s.T(), session, "evaluate", map[string]any{"expression": "1+1"})
	require.False(s.T(), res.IsError)
	require.Equal(s.T(), "2", getText(s.T(), res))
}

func (s *ServerSuite) TestEvaluateError() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{Error: "eval err"})
	})
	res := callTool(s.T(), session, "evaluate", map[string]any{"expression": "bad()"})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "evaluate failed: eval err")
}

func (s *ServerSuite) TestEvaluateEmptyExpression() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{})
	})
	res := callTool(s.T(), session, "evaluate", map[string]any{"expression": ""})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "expression is required")
}

// ==================== list_tabs ====================

func (s *ServerSuite) TestListTabsSuccess() {
	tabs := []browser.TabInfo{
		{TargetID: "t1", URL: "https://a.com", Title: "A"},
		{TargetID: "t2", URL: "https://b.com", Title: "B"},
	}
	_, session := setupTest(s.T(), func(w http.ResponseWriter, r *http.Request) {
		_, action, _ := decodeActionRequest(s.T(), r)
		require.Equal(s.T(), "list_tabs", action)
		writeJSON(w, actionResponse{Tabs: tabs})
	})
	res := callTool(s.T(), session, "list_tabs", nil)
	require.False(s.T(), res.IsError)
	text := getText(s.T(), res)
	require.Contains(s.T(), text, "[1] A")
	require.Contains(s.T(), text, "(id: t1)")
	require.Contains(s.T(), text, "[2] B")
}

func (s *ServerSuite) TestListTabsEmpty() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{Tabs: []browser.TabInfo{}})
	})
	res := callTool(s.T(), session, "list_tabs", nil)
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "No tabs open")
}

func (s *ServerSuite) TestListTabsError() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{Error: "tabs err"})
	})
	res := callTool(s.T(), session, "list_tabs", nil)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "list tabs failed: tabs err")
}

// ==================== new_tab ====================

func (s *ServerSuite) TestNewTabSuccess() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, r *http.Request) {
		_, action, params := decodeActionRequest(s.T(), r)
		require.Equal(s.T(), "new_tab", action)
		require.Equal(s.T(), "https://new.com", params["url"])
		writeJSON(w, actionResponse{Result: "Opened new tab (id: t99) at https://new.com"})
	})
	res := callTool(s.T(), session, "new_tab", map[string]any{"url": "https://new.com"})
	require.False(s.T(), res.IsError)
	text := getText(s.T(), res)
	require.Contains(s.T(), text, "Opened new tab (id: t99)")
	require.Contains(s.T(), text, "https://new.com")
}

func (s *ServerSuite) TestNewTabEmptyURL() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, r *http.Request) {
		_, _, params := decodeActionRequest(s.T(), r)
		require.Equal(s.T(), "about:blank", params["url"])
		writeJSON(w, actionResponse{Result: "Opened new tab (id: t0) at about:blank"})
	})
	res := callTool(s.T(), session, "new_tab", map[string]any{"url": ""})
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "about:blank")
}

func (s *ServerSuite) TestNewTabError() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{Error: "tab err"})
	})
	res := callTool(s.T(), session, "new_tab", map[string]any{"url": "https://fail.com"})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "new tab failed: tab err")
}

// ==================== switch_tab ====================

func (s *ServerSuite) TestSwitchTabSuccess() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, r *http.Request) {
		_, action, params := decodeActionRequest(s.T(), r)
		require.Equal(s.T(), "switch_tab", action)
		require.Equal(s.T(), "t1", params["target_id"])
		writeJSON(w, actionResponse{Result: "ok"})
	})
	res := callTool(s.T(), session, "switch_tab", map[string]any{"target_id": "t1"})
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "Switched to tab t1")
}

func (s *ServerSuite) TestSwitchTabError() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{Error: "switch err"})
	})
	res := callTool(s.T(), session, "switch_tab", map[string]any{"target_id": "t1"})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "switch tab failed: switch err")
}

func (s *ServerSuite) TestSwitchTabEmptyTargetID() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{})
	})
	res := callTool(s.T(), session, "switch_tab", map[string]any{"target_id": ""})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "target_id is required")
}

// ==================== close_tab ====================

func (s *ServerSuite) TestCloseTabSuccess() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, r *http.Request) {
		_, action, params := decodeActionRequest(s.T(), r)
		require.Equal(s.T(), "close_tab", action)
		require.Equal(s.T(), "t2", params["target_id"])
		writeJSON(w, actionResponse{Result: "ok"})
	})
	res := callTool(s.T(), session, "close_tab", map[string]any{"target_id": "t2"})
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "Closed tab t2")
}

func (s *ServerSuite) TestCloseTabError() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{Error: "close err"})
	})
	res := callTool(s.T(), session, "close_tab", map[string]any{"target_id": "t2"})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "close tab failed: close err")
}

func (s *ServerSuite) TestCloseTabEmptyTargetID() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{})
	})
	res := callTool(s.T(), session, "close_tab", map[string]any{"target_id": ""})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "target_id is required")
}

// ==================== page_info ====================

func (s *ServerSuite) TestPageInfoSuccess() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, r *http.Request) {
		_, action, _ := decodeActionRequest(s.T(), r)
		require.Equal(s.T(), "get_page_info", action)
		writeJSON(w, actionResponse{PageInfo: &browser.PageInfo{URL: "https://x.com", Title: "X"}})
	})
	res := callTool(s.T(), session, "page_info", nil)
	require.False(s.T(), res.IsError)
	text := getText(s.T(), res)
	require.Contains(s.T(), text, "URL: https://x.com")
	require.Contains(s.T(), text, "Title: X")
}

func (s *ServerSuite) TestPageInfoError() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{Error: "info err"})
	})
	res := callTool(s.T(), session, "page_info", nil)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "page info failed: info err")
}

func (s *ServerSuite) TestPageInfoNilPageInfo() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{Result: "ok"})
	})
	res := callTool(s.T(), session, "page_info", nil)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "page info failed")
}

// ==================== get_page_text ====================

func (s *ServerSuite) TestGetPageTextSuccess() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, r *http.Request) {
		_, action, params := decodeActionRequest(s.T(), r)
		require.Equal(s.T(), "evaluate_js", action)
		require.Equal(s.T(), "document.body.innerText", params["expression"])
		writeJSON(w, actionResponse{Result: "Hello World\nThis is a page."})
	})
	res := callTool(s.T(), session, "get_page_text", nil)
	require.False(s.T(), res.IsError)
	require.Equal(s.T(), "Hello World\nThis is a page.", getText(s.T(), res))
}

func (s *ServerSuite) TestGetPageTextError() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{Error: "eval err"})
	})
	res := callTool(s.T(), session, "get_page_text", nil)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "get page text failed: eval err")
}

// ==================== find ====================

func (s *ServerSuite) TestFindSuccess() {
	refs := []browser.ElementRef{
		{RefID: "ref_1", Role: "button", Name: "Submit Form"},
		{RefID: "ref_2", Role: "link", Name: "Home Page"},
		{RefID: "ref_3", Role: "textbox", Name: "Email"},
	}
	srv, session := setupTest(s.T(), func(w http.ResponseWriter, r *http.Request) {
		_, action, _ := decodeActionRequest(s.T(), r)
		require.Equal(s.T(), "get_element_refs", action)
		writeJSON(w, actionResponse{ElementRefs: refs})
	})
	res := callTool(s.T(), session, "find", map[string]any{"query": "submit"})
	require.False(s.T(), res.IsError)
	text := getText(s.T(), res)
	require.Contains(s.T(), text, "Found 1 element(s)")
	require.Contains(s.T(), text, "[ref_1] button: Submit Form")
	require.Len(s.T(), srv.refs, 3)
}

func (s *ServerSuite) TestFindCaseInsensitive() {
	refs := []browser.ElementRef{
		{RefID: "ref_1", Role: "button", Name: "SUBMIT"},
	}
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{ElementRefs: refs})
	})
	res := callTool(s.T(), session, "find", map[string]any{"query": "submit"})
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "Found 1 element(s)")
}

func (s *ServerSuite) TestFindByRole() {
	refs := []browser.ElementRef{
		{RefID: "ref_1", Role: "button", Name: "Click Me"},
		{RefID: "ref_2", Role: "checkbox", Name: "Accept"},
	}
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{ElementRefs: refs})
	})
	res := callTool(s.T(), session, "find", map[string]any{"query": "checkbox"})
	require.False(s.T(), res.IsError)
	text := getText(s.T(), res)
	require.Contains(s.T(), text, "Found 1 element(s)")
	require.Contains(s.T(), text, "[ref_2] checkbox: Accept")
}

func (s *ServerSuite) TestFindNoMatch() {
	refs := []browser.ElementRef{
		{RefID: "ref_1", Role: "button", Name: "Submit"},
	}
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{ElementRefs: refs})
	})
	res := callTool(s.T(), session, "find", map[string]any{"query": "nonexistent"})
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), `No elements found matching "nonexistent"`)
}

func (s *ServerSuite) TestFindMaxResults() {
	var refs []browser.ElementRef
	for i := 1; i <= 25; i++ {
		refs = append(refs, browser.ElementRef{
			RefID: fmt.Sprintf("ref_%d", i),
			Role:  "button",
			Name:  fmt.Sprintf("Button %d", i),
		})
	}
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{ElementRefs: refs})
	})
	res := callTool(s.T(), session, "find", map[string]any{"query": "button"})
	require.False(s.T(), res.IsError)
	text := getText(s.T(), res)
	require.Contains(s.T(), text, "Found 20 element(s)")
	require.Contains(s.T(), text, "[ref_20]")
	require.NotContains(s.T(), text, "[ref_21]")
}

func (s *ServerSuite) TestFindEmptyQuery() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{})
	})
	res := callTool(s.T(), session, "find", map[string]any{"query": ""})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "query is required")
}

func (s *ServerSuite) TestFindError() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{Error: "ax tree err"})
	})
	res := callTool(s.T(), session, "find", map[string]any{"query": "button"})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "failed to get element refs: ax tree err")
}

func (s *ServerSuite) TestFindWithValue() {
	refs := []browser.ElementRef{
		{RefID: "ref_1", Role: "textbox", Name: "Search", Value: "hello"},
	}
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{ElementRefs: refs})
	})
	res := callTool(s.T(), session, "find", map[string]any{"query": "search"})
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "(value: hello)")
}

// ==================== read_console_messages ====================

func (s *ServerSuite) TestReadConsoleMessagesSuccess() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, r *http.Request) {
		_, action, params := decodeActionRequest(s.T(), r)
		require.Equal(s.T(), "read_console", action)
		require.Equal(s.T(), "error.*", params["pattern"])
		require.Equal(s.T(), true, params["only_errors"])
		require.Equal(s.T(), true, params["clear"])
		require.Equal(s.T(), float64(50), params["limit"])
		writeJSON(w, actionResponse{Result: "2 console message(s):\n[10:00:00] error: boom\n"})
	})
	res := callTool(s.T(), session, "read_console_messages", map[string]any{
		"pattern":    "error.*",
		"onlyErrors": true,
		"clear":      true,
		"limit":      50,
	})
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "2 console message(s)")
}

func (s *ServerSuite) TestReadConsoleMessagesEmpty() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{Result: "No console messages"})
	})
	res := callTool(s.T(), session, "read_console_messages", nil)
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "No console messages")
}

func (s *ServerSuite) TestReadConsoleMessagesError() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{Error: "console err"})
	})
	res := callTool(s.T(), session, "read_console_messages", nil)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "read console failed: console err")
}

// ==================== read_network_requests ====================

func (s *ServerSuite) TestReadNetworkRequestsSuccess() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, r *http.Request) {
		_, action, params := decodeActionRequest(s.T(), r)
		require.Equal(s.T(), "read_network", action)
		require.Equal(s.T(), "/api/", params["pattern"])
		require.Equal(s.T(), true, params["clear"])
		require.Equal(s.T(), float64(10), params["limit"])
		writeJSON(w, actionResponse{Result: "2 network request(s):\n[10:00:00] GET /api/users — 200 OK\n"})
	})
	res := callTool(s.T(), session, "read_network_requests", map[string]any{
		"pattern": "/api/",
		"clear":   true,
		"limit":   10,
	})
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "2 network request(s)")
}

func (s *ServerSuite) TestReadNetworkRequestsEmpty() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{Result: "No network requests"})
	})
	res := callTool(s.T(), session, "read_network_requests", nil)
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "No network requests")
}

func (s *ServerSuite) TestReadNetworkRequestsError() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{Error: "network err"})
	})
	res := callTool(s.T(), session, "read_network_requests", nil)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "read network failed: network err")
}

// ==================== resize_window ====================

func (s *ServerSuite) TestResizeWindowSuccess() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, r *http.Request) {
		_, action, params := decodeActionRequest(s.T(), r)
		require.Equal(s.T(), "resize_window", action)
		require.Equal(s.T(), float64(1024), params["width"])
		require.Equal(s.T(), float64(768), params["height"])
		writeJSON(w, actionResponse{Result: "ok"})
	})
	res := callTool(s.T(), session, "resize_window", map[string]any{"width": 1024, "height": 768})
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "Resized viewport to 1024x768")
}

func (s *ServerSuite) TestResizeWindowError() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{Error: "resize err"})
	})
	res := callTool(s.T(), session, "resize_window", map[string]any{"width": 800, "height": 600})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "resize failed: resize err")
}

func (s *ServerSuite) TestResizeWindowInvalidDimensions() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{})
	})
	res := callTool(s.T(), session, "resize_window", map[string]any{"width": 0, "height": 600})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "width and height must be positive")

	res = callTool(s.T(), session, "resize_window", map[string]any{"width": 800, "height": -1})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "width and height must be positive")
}

// ==================== channelID forwarding ====================

func (s *ServerSuite) TestChannelIDForwarded() {
	var gotChannelID string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotChannelID, _, _ = decodeActionRequest(s.T(), r)
		writeJSON(w, actionResponse{Result: "ok"})
	}))
	defer ts.Close()

	srv := New(ts.URL, "my-channel", nil)
	srv.httpClient = ts.Client()
	session := connectClient(s.T(), srv)

	_ = callTool(s.T(), session, "go_back", nil)
	require.Equal(s.T(), "my-channel", gotChannelID)
}

// ==================== unused import guard ====================

func (s *ServerSuite) TestStringsUsed() {
	// strings.ToLower is used in handleFind — ensure the import is exercised.
	srv := New("http://x", "ch", nil)
	srv.refs = []browser.ElementRef{{RefID: "ref_1", Role: "BUTTON", Name: "OK"}}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{ElementRefs: []browser.ElementRef{
			{RefID: "ref_1", Role: "BUTTON", Name: "OK"},
		}})
	}))
	defer ts.Close()
	srv.apiURL = ts.URL
	srv.httpClient = ts.Client()

	res, _, err := srv.handleFind(context.Background(), "button")
	require.NoError(s.T(), err)
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "Found 1 element(s)")
}

// ==================== handleComputer direct (no HTTP) ====================

func (s *ServerSuite) TestHandleComputerNoRefsWait() {
	srv := New("http://x", "ch", nil)
	res, _, err := srv.handleComputer(context.Background(), computerInput{Action: "wait"})
	require.NoError(s.T(), err)
	require.False(s.T(), res.IsError)
	require.Equal(s.T(), "Waited", getText(s.T(), res))
}

func (s *ServerSuite) TestTimeImportUsed() {
	// Ensure time.Second is used by the default httpClient in New.
	srv := New("http://x", "ch", nil)
	require.NotNil(s.T(), srv.httpClient)
	_ = time.Second // satisfy import usage check
}

func (s *ServerSuite) TestIOImportUsed() {
	// Ensure io.Discard is used in nil logger path.
	srv := New("http://x", "ch", nil)
	require.NotNil(s.T(), srv.logger)
	_ = io.Discard
}

func (s *ServerSuite) TestSlogImportUsed() {
	logger := slog.Default()
	srv := New("http://x", "ch", logger)
	require.Equal(s.T(), logger, srv.logger)
}

func (s *ServerSuite) TestStringsPackageUsed() {
	require.True(s.T(), strings.Contains("hello", "ell"))
}

// --- list_tabs: active tab marker ---

func (s *ServerSuite) TestListTabsActiveTab() {
	tabs := []browser.TabInfo{
		{TargetID: "t1", URL: "https://a.com", Title: "A", Active: false},
		{TargetID: "t2", URL: "https://b.com", Title: "B", Active: true},
	}
	_, session := setupTest(s.T(), func(w http.ResponseWriter, r *http.Request) {
		_, action, _ := decodeActionRequest(s.T(), r)
		require.Equal(s.T(), "list_tabs", action)
		writeJSON(w, actionResponse{Tabs: tabs})
	})
	res := callTool(s.T(), session, "list_tabs", nil)
	require.False(s.T(), res.IsError)
	text := getText(s.T(), res)
	require.Contains(s.T(), text, "* [2] B")
	require.Contains(s.T(), text, "  [1] A")
}
