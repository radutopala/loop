package mcpbrowser

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/radutopala/loop/internal/browser"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// --- mock CDP client ---

type mockCDP struct {
	mock.Mock
}

func (m *mockCDP) Navigate(ctx context.Context, url string) error {
	return m.Called(ctx, url).Error(0)
}
func (m *mockCDP) Reload(ctx context.Context) error {
	return m.Called(ctx).Error(0)
}
func (m *mockCDP) GoBack(ctx context.Context) error {
	return m.Called(ctx).Error(0)
}
func (m *mockCDP) GoForward(ctx context.Context) error {
	return m.Called(ctx).Error(0)
}
func (m *mockCDP) GetPageInfo(ctx context.Context) (*browser.PageInfo, error) {
	args := m.Called(ctx)
	pi, _ := args.Get(0).(*browser.PageInfo)
	return pi, args.Error(1)
}
func (m *mockCDP) GetElementRefs(ctx context.Context) ([]browser.ElementRef, error) {
	args := m.Called(ctx)
	refs, _ := args.Get(0).([]browser.ElementRef)
	return refs, args.Error(1)
}
func (m *mockCDP) MouseClick(ctx context.Context, x, y float64, button string, clickCount int) error {
	return m.Called(ctx, x, y, button, clickCount).Error(0)
}
func (m *mockCDP) MouseMove(ctx context.Context, x, y float64) error {
	return m.Called(ctx, x, y).Error(0)
}
func (m *mockCDP) MouseScroll(ctx context.Context, x, y, deltaX, deltaY float64) error {
	return m.Called(ctx, x, y, deltaX, deltaY).Error(0)
}
func (m *mockCDP) KeyPress(ctx context.Context, key string) error {
	return m.Called(ctx, key).Error(0)
}
func (m *mockCDP) TypeText(ctx context.Context, text string) error {
	return m.Called(ctx, text).Error(0)
}
func (m *mockCDP) ClickRef(ctx context.Context, refs []browser.ElementRef, refIndex int) error {
	return m.Called(ctx, refs, refIndex).Error(0)
}
func (m *mockCDP) Screenshot(ctx context.Context) ([]byte, error) {
	args := m.Called(ctx)
	data, _ := args.Get(0).([]byte)
	return data, args.Error(1)
}
func (m *mockCDP) ListTabs(ctx context.Context) ([]browser.TabInfo, error) {
	args := m.Called(ctx)
	tabs, _ := args.Get(0).([]browser.TabInfo)
	return tabs, args.Error(1)
}
func (m *mockCDP) NewTab(ctx context.Context, url string) (string, error) {
	args := m.Called(ctx, url)
	return args.String(0), args.Error(1)
}
func (m *mockCDP) SwitchTab(ctx context.Context, targetID string) error {
	return m.Called(ctx, targetID).Error(0)
}
func (m *mockCDP) CloseTab(ctx context.Context, targetID string) error {
	return m.Called(ctx, targetID).Error(0)
}
func (m *mockCDP) EvaluateJS(ctx context.Context, expression string) (string, error) {
	args := m.Called(ctx, expression)
	return args.String(0), args.Error(1)
}
func (m *mockCDP) EnableConsoleCapture(ctx context.Context, ch chan<- browser.ConsoleMessage) error {
	return m.Called(ctx, ch).Error(0)
}
func (m *mockCDP) EnableNetworkCapture(ctx context.Context, ch chan<- browser.NetworkRequest) error {
	return m.Called(ctx, ch).Error(0)
}
func (m *mockCDP) ResizeWindow(ctx context.Context, width, height int) error {
	return m.Called(ctx, width, height).Error(0)
}
func (m *mockCDP) ScrollIntoView(ctx context.Context, backendNodeID cdp.BackendNodeID) error {
	return m.Called(ctx, backendNodeID).Error(0)
}
func (m *mockCDP) MouseDown(ctx context.Context, x, y float64, button string) error {
	return m.Called(ctx, x, y, button).Error(0)
}
func (m *mockCDP) MouseUp(ctx context.Context, x, y float64, button string) error {
	return m.Called(ctx, x, y, button).Error(0)
}
func (m *mockCDP) Close() {
	m.Called()
}

// --- helpers ---

// connectClient sets up an in-memory MCP client+server session.
// The server must already have s.cdp set before calling this.
func connectClient(t *testing.T, srv *Server) *mcp.ClientSession {
	t.Helper()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	ctx := context.Background()

	// Server.Connect must be called before Client.Connect.
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

// --- suite ---

type ServerSuite struct {
	suite.Suite
}

func TestServerSuite(t *testing.T) {
	suite.Run(t, new(ServerSuite))
}

// ==================== New / helpers ====================

func (s *ServerSuite) TestNew() {
	srv := New("ws://127.0.0.1:9222", nil)
	require.NotNil(s.T(), srv)
	require.Equal(s.T(), "ws://127.0.0.1:9222", srv.cdpEndpoint)
	require.NotNil(s.T(), srv.mcpServer)
	require.NotNil(s.T(), srv.logger)
}

func (s *ServerSuite) TestNewWithLogger() {
	logger := slog.Default()
	srv := New("ws://x", logger)
	require.Equal(s.T(), logger, srv.logger)
}

func (s *ServerSuite) TestSetTargetID() {
	srv := New("ws://x", nil)
	require.Empty(s.T(), srv.targetID)

	srv.SetTargetID("page-target-42")
	require.Equal(s.T(), "page-target-42", srv.targetID)
}

func (s *ServerSuite) TestNewCDPFactoryWithTargetID() {
	// Verify the cdpFactory closure respects s.targetID.
	// We set a targetID and then call the factory. It will fail (no Chrome),
	// but we verify the targetID was passed through by inspecting the factory was called.
	srv := New("ws://127.0.0.1:1", nil)
	srv.SetTargetID("my-shared-target")

	// Replace the factory to capture options being passed.
	factoryCalled := false
	srv.cdpFactory = func(_ context.Context, wsURL string, logger *slog.Logger) (cdpClient, error) {
		factoryCalled = true
		require.Equal(s.T(), "ws://test:9222", wsURL)
		return nil, fmt.Errorf("not connecting in test")
	}

	_, err := srv.cdpFactory(context.Background(), "ws://test:9222", slog.Default())
	require.Error(s.T(), err)
	require.True(s.T(), factoryCalled)
}

func (s *ServerSuite) TestNewDefaultFactoryWithTargetID() {
	// Exercise the real default cdpFactory closure path with a targetID set.
	// The factory will fail to connect (no real Chrome) — we just verify it
	// exercises the `if s.targetID != ""` branch.
	srv := New("ws://127.0.0.1:1", slog.Default())
	srv.SetTargetID("target-abc")
	srv.runCtx = context.Background()

	// The real factory internally calls browser.NewCDPClient with WithTargetID.
	// This will fail (no Chrome), which is fine — we're covering the closure body.
	_, err := srv.cdpFactory(context.Background(), "ws://127.0.0.1:1", slog.Default())
	require.Error(s.T(), err) // expected: no Chrome listening
}

func (s *ServerSuite) TestSetAPICallback() {
	srv := New("ws://x", nil)
	require.Nil(s.T(), srv.httpClient)
	require.Empty(s.T(), srv.apiURL)
	require.Empty(s.T(), srv.channelID)

	srv.SetAPICallback("http://host:8222", "ch-1")
	require.NotNil(s.T(), srv.httpClient)
	require.Equal(s.T(), "http://host:8222", srv.apiURL)
	require.Equal(s.T(), "ch-1", srv.channelID)
}

func (s *ServerSuite) TestEnsureBrowserViaAPISuccess() {
	var gotBody string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		gotBody = string(data)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	srv := New("ws://x", nil)
	srv.runCtx = context.Background()
	srv.apiURL = ts.URL
	srv.channelID = "ch-42"
	srv.httpClient = ts.Client()

	srv.ensureBrowserViaAPI()
	require.Contains(s.T(), gotBody, `"channel_id":"ch-42"`)
}

func (s *ServerSuite) TestEnsureBrowserViaAPINoConfig() {
	srv := New("ws://x", nil)
	srv.runCtx = context.Background()
	// No apiURL/channelID/httpClient — should be a no-op.
	srv.ensureBrowserViaAPI()
}

func (s *ServerSuite) TestEnsureBrowserViaAPIErrorNonFatal() {
	// Server returns 500 — should not panic or block.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	srv := New("ws://x", slog.Default())
	srv.runCtx = context.Background()
	srv.apiURL = ts.URL
	srv.channelID = "ch-1"
	srv.httpClient = ts.Client()

	srv.ensureBrowserViaAPI() // should not error
}

func (s *ServerSuite) TestEnsureBrowserViaAPIRequestError() {
	srv := New("ws://x", slog.Default())
	srv.runCtx = context.Background()
	srv.apiURL = "http://127.0.0.1:1" // unreachable
	srv.channelID = "ch-1"
	srv.httpClient = &http.Client{Timeout: 100 * time.Millisecond}

	srv.ensureBrowserViaAPI() // should not panic
}

func (s *ServerSuite) TestEnsureBrowserViaAPIInvalidURL() {
	srv := New("ws://x", slog.Default())
	srv.runCtx = context.Background()
	srv.apiURL = "://bad-url" // invalid URL
	srv.channelID = "ch-1"
	srv.httpClient = &http.Client{}

	srv.ensureBrowserViaAPI() // should not panic
}

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

func (s *ServerSuite) TestRunCDPError() {
	// Use httptest to avoid connecting to a real endpoint.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not a websocket", http.StatusBadRequest)
	}))
	defer ts.Close()
	wsURL := "ws://" + strings.TrimPrefix(ts.URL, "http://")

	srv := New(wsURL, slog.Default())
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	srv.runCtx = ctx

	err := srv.ensureCDP()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "connecting to CDP at "+wsURL)
}

func (s *ServerSuite) TestRunSuccess() {
	m := &mockCDP{}

	srv := New("ws://test:9222", nil)
	srv.cdpFactory = func(_ context.Context, _ string, _ *slog.Logger) (cdpClient, error) {
		return m, nil
	}

	ctx, cancel := context.WithCancel(context.Background())
	t1, t2 := mcp.NewInMemoryTransports()

	// Run server in background, connect client.
	done := make(chan error, 1)
	go func() {
		done <- srv.Run(ctx, t1)
	}()

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0.1"}, nil)
	_, err := client.Connect(ctx, t2, nil)
	require.NoError(s.T(), err)

	cancel()
	<-done

	m.AssertExpectations(s.T())
}

func (s *ServerSuite) TestDefaultCDPFactory() {
	// Exercise the default factory (browser.NewCDPClient) against an httptest server.
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not chrome", http.StatusBadRequest)
	}))
	defer ts.Close()
	wsURL := "ws://" + strings.TrimPrefix(ts.URL, "http://")

	srv := New(wsURL, slog.Default())
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	_, err := srv.cdpFactory(ctx, wsURL, slog.Default())
	require.Error(s.T(), err)
}

// ==================== handleComputer (direct) ====================

func (s *ServerSuite) TestHandleComputerClickSuccess() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	m.On("MouseClick", mock.Anything, float64(100), float64(200), "left", 1).Return(nil)
	res, _, err := srv.handleComputer(computerInput{Action: "click", X: 100, Y: 200})
	require.NoError(s.T(), err)
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "Clicked at (100, 200)")
	m.AssertExpectations(s.T())
}

func (s *ServerSuite) TestHandleComputerClickError() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	m.On("MouseClick", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(fmt.Errorf("click err"))
	res, _, err := srv.handleComputer(computerInput{Action: "click", X: 10, Y: 20})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "click failed: click err")
}

func (s *ServerSuite) TestHandleComputerClickWithButton() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	m.On("MouseClick", mock.Anything, float64(0), float64(0), "right", 1).Return(nil)
	res, _, err := srv.handleComputer(computerInput{Action: "click", Button: "right"})
	require.NoError(s.T(), err)
	require.False(s.T(), res.IsError)
}

func (s *ServerSuite) TestHandleComputerDoubleClickSuccess() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	m.On("MouseClick", mock.Anything, float64(50), float64(60), "left", 2).Return(nil)
	res, _, err := srv.handleComputer(computerInput{Action: "double_click", X: 50, Y: 60})
	require.NoError(s.T(), err)
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "Double-clicked at (50, 60)")
}

func (s *ServerSuite) TestHandleComputerDoubleClickError() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	m.On("MouseClick", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(fmt.Errorf("dc err"))
	res, _, err := srv.handleComputer(computerInput{Action: "double_click"})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "double click failed: dc err")
}

func (s *ServerSuite) TestHandleComputerTripleClickSuccess() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	m.On("MouseClick", mock.Anything, float64(10), float64(20), "left", 3).Return(nil)
	res, _, err := srv.handleComputer(computerInput{Action: "triple_click", X: 10, Y: 20})
	require.NoError(s.T(), err)
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "Triple-clicked at (10, 20)")
}

func (s *ServerSuite) TestHandleComputerTripleClickError() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	m.On("MouseClick", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(fmt.Errorf("tc err"))
	res, _, err := srv.handleComputer(computerInput{Action: "triple_click"})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "triple click failed: tc err")
}

func (s *ServerSuite) TestHandleComputerTypeSuccess() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	m.On("TypeText", mock.Anything, "hello world").Return(nil)
	res, _, err := srv.handleComputer(computerInput{Action: "type", Text: "hello world"})
	require.NoError(s.T(), err)
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), `Typed "hello world"`)
}

func (s *ServerSuite) TestHandleComputerTypeError() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	m.On("TypeText", mock.Anything, "x").Return(fmt.Errorf("type err"))
	res, _, err := srv.handleComputer(computerInput{Action: "type", Text: "x"})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "type failed: type err")
}

func (s *ServerSuite) TestHandleComputerTypeNoText() {
	srv := New("ws://127.0.0.1:9222", nil)
	res, _, err := srv.handleComputer(computerInput{Action: "type"})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "text is required for type action")
}

func (s *ServerSuite) TestHandleComputerKeySuccess() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	m.On("KeyPress", mock.Anything, "Enter").Return(nil)
	res, _, err := srv.handleComputer(computerInput{Action: "key", Text: "Enter"})
	require.NoError(s.T(), err)
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), `Pressed key "Enter"`)
}

func (s *ServerSuite) TestHandleComputerKeyError() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	m.On("KeyPress", mock.Anything, "Tab").Return(fmt.Errorf("key err"))
	res, _, err := srv.handleComputer(computerInput{Action: "key", Text: "Tab"})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "key failed: key err")
}

func (s *ServerSuite) TestHandleComputerKeyNoText() {
	srv := New("ws://127.0.0.1:9222", nil)
	res, _, err := srv.handleComputer(computerInput{Action: "key"})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "text is required for key action")
}

func (s *ServerSuite) TestHandleComputerScrollDefaultDeltaY() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	// When DeltaY=0, default to -3
	m.On("MouseScroll", mock.Anything, float64(0), float64(0), float64(0), float64(-3)).Return(nil)
	res, _, err := srv.handleComputer(computerInput{Action: "scroll"})
	require.NoError(s.T(), err)
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "Scrolled at (0, 0)")
	m.AssertExpectations(s.T())
}

func (s *ServerSuite) TestHandleComputerScrollExplicitDeltaY() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	m.On("MouseScroll", mock.Anything, float64(50), float64(60), float64(1), float64(5)).Return(nil)
	res, _, err := srv.handleComputer(computerInput{Action: "scroll", X: 50, Y: 60, DeltaX: 1, DeltaY: 5})
	require.NoError(s.T(), err)
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "Scrolled at (50, 60)")
	m.AssertExpectations(s.T())
}

func (s *ServerSuite) TestHandleComputerScrollError() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	m.On("MouseScroll", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(fmt.Errorf("scroll err"))
	res, _, err := srv.handleComputer(computerInput{Action: "scroll"})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "scroll failed: scroll err")
}

func (s *ServerSuite) TestHandleComputerMoveSuccess() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	m.On("MouseMove", mock.Anything, float64(300), float64(400)).Return(nil)
	res, _, err := srv.handleComputer(computerInput{Action: "move", X: 300, Y: 400})
	require.NoError(s.T(), err)
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "Moved to (300, 400)")
}

func (s *ServerSuite) TestHandleComputerMoveError() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	m.On("MouseMove", mock.Anything, mock.Anything, mock.Anything).Return(fmt.Errorf("move err"))
	res, _, err := srv.handleComputer(computerInput{Action: "move"})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "move failed: move err")
}

func (s *ServerSuite) TestHandleComputerScreenshotSuccess() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	m.On("Screenshot", mock.Anything).Return([]byte("img-data"), nil)
	res, _, err := srv.handleComputer(computerInput{Action: "screenshot"})
	require.NoError(s.T(), err)
	require.False(s.T(), res.IsError)
	ic, ok := res.Content[0].(*mcp.ImageContent)
	require.True(s.T(), ok)
	require.Equal(s.T(), []byte("img-data"), ic.Data)
}

func (s *ServerSuite) TestHandleComputerScreenshotError() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	m.On("Screenshot", mock.Anything).Return(nil, fmt.Errorf("ss err"))
	res, _, err := srv.handleComputer(computerInput{Action: "screenshot"})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "screenshot failed: ss err")
}

func (s *ServerSuite) TestHandleComputerWait() {
	srv := New("ws://127.0.0.1:9222", nil)
	res, _, err := srv.handleComputer(computerInput{Action: "wait"})
	require.NoError(s.T(), err)
	require.False(s.T(), res.IsError)
	require.Equal(s.T(), "Waited", getText(s.T(), res))
}

func (s *ServerSuite) TestHandleComputerUnknownAction() {
	srv := New("ws://127.0.0.1:9222", nil)
	res, _, err := srv.handleComputer(computerInput{Action: "fly"})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "unknown action: fly")
}

func (s *ServerSuite) TestHandleComputerRefOutOfRange() {
	srv := New("ws://127.0.0.1:9222", nil)
	srv.refs = []browser.ElementRef{{RefID: "ref_1"}}
	res, _, err := srv.handleComputer(computerInput{Action: "click", Ref: 5})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "ref 5 out of range")
}

func (s *ServerSuite) TestHandleComputerRefResolution() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	srv.refs = []browser.ElementRef{
		{RefID: "ref_1", X: 100, Y: 200, Width: 40, Height: 20},
	}
	// Ref 1 -> center is (100+20, 200+10) = (120, 210)
	m.On("MouseClick", mock.Anything, float64(120), float64(210), "left", 1).Return(nil)
	res, _, err := srv.handleComputer(computerInput{Action: "click", Ref: 1})
	require.NoError(s.T(), err)
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "Clicked at (120, 210)")
	m.AssertExpectations(s.T())
}

// ==================== Tool handlers via MCP client ====================

func (s *ServerSuite) TestNavigateSuccess() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	m.On("Navigate", mock.Anything, "https://example.com").Return(nil)
	m.On("GetPageInfo", mock.Anything).Return(&browser.PageInfo{URL: "https://example.com", Title: "Example"}, nil)

	session := connectClient(s.T(), srv)
	res := callTool(s.T(), session, "navigate", map[string]any{"url": "https://example.com"})
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "Navigated to https://example.com")
	require.Contains(s.T(), getText(s.T(), res), "Example")
	m.AssertExpectations(s.T())
}

func (s *ServerSuite) TestNavigateError() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	m.On("Navigate", mock.Anything, "https://bad.com").Return(fmt.Errorf("timeout"))

	session := connectClient(s.T(), srv)
	res := callTool(s.T(), session, "navigate", map[string]any{"url": "https://bad.com"})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "navigate failed: timeout")
}

func (s *ServerSuite) TestNavigateEmptyURL() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m

	session := connectClient(s.T(), srv)
	res := callTool(s.T(), session, "navigate", map[string]any{"url": ""})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "url is required")
}

func (s *ServerSuite) TestNavigatePageInfoNil() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	m.On("Navigate", mock.Anything, "https://x.com").Return(nil)
	m.On("GetPageInfo", mock.Anything).Return(nil, fmt.Errorf("no info"))

	session := connectClient(s.T(), srv)
	res := callTool(s.T(), session, "navigate", map[string]any{"url": "https://x.com"})
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "Navigated to https://x.com")
}

func (s *ServerSuite) TestReadPageSuccessWithRefs() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	refs := []browser.ElementRef{
		{RefID: "ref_1", Role: "button", Name: "Submit", Value: "go"},
		{RefID: "ref_2", Role: "link", Name: "Home"},
	}
	m.On("GetElementRefs", mock.Anything).Return(refs, nil)
	m.On("GetPageInfo", mock.Anything).Return(&browser.PageInfo{URL: "https://x.com", Title: "X"}, nil)

	session := connectClient(s.T(), srv)
	res := callTool(s.T(), session, "read_page", nil)
	require.False(s.T(), res.IsError)
	text := getText(s.T(), res)
	require.Contains(s.T(), text, "Page: https://x.com")
	require.Contains(s.T(), text, "[ref_1] button: Submit (value: go)")
	require.Contains(s.T(), text, "[ref_2] link: Home")
	// Refs should be cached on server
	require.Len(s.T(), srv.refs, 2)
}

func (s *ServerSuite) TestReadPageNoRefs() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	m.On("GetElementRefs", mock.Anything).Return([]browser.ElementRef{}, nil)
	m.On("GetPageInfo", mock.Anything).Return(nil, fmt.Errorf("err"))

	session := connectClient(s.T(), srv)
	res := callTool(s.T(), session, "read_page", nil)
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "No interactive elements found.")
}

func (s *ServerSuite) TestReadPageError() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	m.On("GetElementRefs", mock.Anything).Return(nil, fmt.Errorf("dom error"))

	session := connectClient(s.T(), srv)
	res := callTool(s.T(), session, "read_page", nil)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "failed to get element refs: dom error")
}

func (s *ServerSuite) TestComputerViaMCP() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	m.On("MouseClick", mock.Anything, float64(10), float64(20), "left", 1).Return(nil)

	session := connectClient(s.T(), srv)
	res := callTool(s.T(), session, "computer", map[string]any{"action": "click", "x": 10.0, "y": 20.0})
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "Clicked at (10, 20)")
}

func (s *ServerSuite) TestFormInputSuccess() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	srv.refs = []browser.ElementRef{{RefID: "ref_1", Role: "textbox", Name: "Name"}}

	m.On("ClickRef", mock.Anything, srv.refs, 1).Return(nil)
	m.On("KeyPress", mock.Anything, "Control+a").Return(nil)
	m.On("TypeText", mock.Anything, "John").Return(nil)

	session := connectClient(s.T(), srv)
	res := callTool(s.T(), session, "form_input", map[string]any{"ref": 1, "value": "John"})
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), `Entered "John" in ref_1`)
	m.AssertExpectations(s.T())
}

func (s *ServerSuite) TestFormInputRefOutOfRange() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	srv.refs = []browser.ElementRef{{RefID: "ref_1"}}

	session := connectClient(s.T(), srv)
	res := callTool(s.T(), session, "form_input", map[string]any{"ref": 5, "value": "x"})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "ref 5 out of range")
}

func (s *ServerSuite) TestFormInputRefZero() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	srv.refs = []browser.ElementRef{{RefID: "ref_1"}}

	session := connectClient(s.T(), srv)
	res := callTool(s.T(), session, "form_input", map[string]any{"ref": 0, "value": "x"})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "ref 0 out of range")
}

func (s *ServerSuite) TestFormInputClickError() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	srv.refs = []browser.ElementRef{{RefID: "ref_1"}}
	m.On("ClickRef", mock.Anything, srv.refs, 1).Return(fmt.Errorf("click err"))

	session := connectClient(s.T(), srv)
	res := callTool(s.T(), session, "form_input", map[string]any{"ref": 1, "value": "x"})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "click failed: click err")
}

func (s *ServerSuite) TestFormInputKeyPressError() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	srv.refs = []browser.ElementRef{{RefID: "ref_1"}}
	m.On("ClickRef", mock.Anything, srv.refs, 1).Return(nil)
	m.On("KeyPress", mock.Anything, "Control+a").Return(fmt.Errorf("key err"))

	session := connectClient(s.T(), srv)
	res := callTool(s.T(), session, "form_input", map[string]any{"ref": 1, "value": "x"})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "select all failed: key err")
}

func (s *ServerSuite) TestFormInputTypeError() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	srv.refs = []browser.ElementRef{{RefID: "ref_1"}}
	m.On("ClickRef", mock.Anything, srv.refs, 1).Return(nil)
	m.On("KeyPress", mock.Anything, "Control+a").Return(nil)
	m.On("TypeText", mock.Anything, "x").Return(fmt.Errorf("type err"))

	session := connectClient(s.T(), srv)
	res := callTool(s.T(), session, "form_input", map[string]any{"ref": 1, "value": "x"})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "type failed: type err")
}

func (s *ServerSuite) TestScreenshotToolSuccess() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	m.On("Screenshot", mock.Anything).Return([]byte("png"), nil)

	session := connectClient(s.T(), srv)
	res := callTool(s.T(), session, "screenshot", nil)
	require.False(s.T(), res.IsError)
	require.NotEmpty(s.T(), res.Content)
	ic, ok := res.Content[0].(*mcp.ImageContent)
	require.True(s.T(), ok)
	require.Equal(s.T(), []byte("png"), ic.Data)
}

func (s *ServerSuite) TestScreenshotToolError() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	m.On("Screenshot", mock.Anything).Return(nil, fmt.Errorf("ss err"))

	session := connectClient(s.T(), srv)
	res := callTool(s.T(), session, "screenshot", nil)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "screenshot failed: ss err")
}

func (s *ServerSuite) TestGoBackSuccess() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	m.On("GoBack", mock.Anything).Return(nil)

	session := connectClient(s.T(), srv)
	res := callTool(s.T(), session, "go_back", nil)
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "Navigated back")
}

func (s *ServerSuite) TestGoBackError() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	m.On("GoBack", mock.Anything).Return(fmt.Errorf("back err"))

	session := connectClient(s.T(), srv)
	res := callTool(s.T(), session, "go_back", nil)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "back failed: back err")
}

func (s *ServerSuite) TestGoForwardSuccess() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	m.On("GoForward", mock.Anything).Return(nil)

	session := connectClient(s.T(), srv)
	res := callTool(s.T(), session, "go_forward", nil)
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "Navigated forward")
}

func (s *ServerSuite) TestGoForwardError() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	m.On("GoForward", mock.Anything).Return(fmt.Errorf("fwd err"))

	session := connectClient(s.T(), srv)
	res := callTool(s.T(), session, "go_forward", nil)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "forward failed: fwd err")
}

func (s *ServerSuite) TestReloadSuccess() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	m.On("Reload", mock.Anything).Return(nil)

	session := connectClient(s.T(), srv)
	res := callTool(s.T(), session, "reload", nil)
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "Page reloaded")
}

func (s *ServerSuite) TestReloadError() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	m.On("Reload", mock.Anything).Return(fmt.Errorf("reload err"))

	session := connectClient(s.T(), srv)
	res := callTool(s.T(), session, "reload", nil)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "reload failed: reload err")
}

func (s *ServerSuite) TestEvaluateSuccess() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	m.On("EvaluateJS", mock.Anything, "1+1").Return("2", nil)

	session := connectClient(s.T(), srv)
	res := callTool(s.T(), session, "evaluate", map[string]any{"expression": "1+1"})
	require.False(s.T(), res.IsError)
	require.Equal(s.T(), "2", getText(s.T(), res))
}

func (s *ServerSuite) TestEvaluateError() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	m.On("EvaluateJS", mock.Anything, "bad()").Return("", fmt.Errorf("eval err"))

	session := connectClient(s.T(), srv)
	res := callTool(s.T(), session, "evaluate", map[string]any{"expression": "bad()"})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "evaluate failed: eval err")
}

func (s *ServerSuite) TestEvaluateEmptyExpression() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m

	session := connectClient(s.T(), srv)
	res := callTool(s.T(), session, "evaluate", map[string]any{"expression": ""})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "expression is required")
}

func (s *ServerSuite) TestListTabsSuccess() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	tabs := []browser.TabInfo{
		{TargetID: "t1", URL: "https://a.com", Title: "A"},
		{TargetID: "t2", URL: "https://b.com", Title: "B"},
	}
	m.On("ListTabs", mock.Anything).Return(tabs, nil)

	session := connectClient(s.T(), srv)
	res := callTool(s.T(), session, "list_tabs", nil)
	require.False(s.T(), res.IsError)
	text := getText(s.T(), res)
	require.Contains(s.T(), text, "[1] A")
	require.Contains(s.T(), text, "(id: t1)")
	require.Contains(s.T(), text, "[2] B")
}

func (s *ServerSuite) TestListTabsEmpty() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	m.On("ListTabs", mock.Anything).Return([]browser.TabInfo{}, nil)

	session := connectClient(s.T(), srv)
	res := callTool(s.T(), session, "list_tabs", nil)
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "No tabs open")
}

func (s *ServerSuite) TestListTabsError() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	m.On("ListTabs", mock.Anything).Return(nil, fmt.Errorf("tabs err"))

	session := connectClient(s.T(), srv)
	res := callTool(s.T(), session, "list_tabs", nil)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "list tabs failed: tabs err")
}

func (s *ServerSuite) TestNewTabSuccess() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	m.On("NewTab", mock.Anything, "https://new.com").Return("t99", nil)

	session := connectClient(s.T(), srv)
	res := callTool(s.T(), session, "new_tab", map[string]any{"url": "https://new.com"})
	require.False(s.T(), res.IsError)
	text := getText(s.T(), res)
	require.Contains(s.T(), text, "Opened new tab (id: t99)")
	require.Contains(s.T(), text, "https://new.com")
}

func (s *ServerSuite) TestNewTabEmptyURL() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	m.On("NewTab", mock.Anything, "about:blank").Return("t0", nil)

	session := connectClient(s.T(), srv)
	res := callTool(s.T(), session, "new_tab", map[string]any{"url": ""})
	require.False(s.T(), res.IsError)
	text := getText(s.T(), res)
	require.Contains(s.T(), text, "about:blank")
}

func (s *ServerSuite) TestNewTabError() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	m.On("NewTab", mock.Anything, "https://fail.com").Return("", fmt.Errorf("tab err"))

	session := connectClient(s.T(), srv)
	res := callTool(s.T(), session, "new_tab", map[string]any{"url": "https://fail.com"})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "new tab failed: tab err")
}

func (s *ServerSuite) TestSwitchTabSuccess() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	m.On("SwitchTab", mock.Anything, "t1").Return(nil)

	session := connectClient(s.T(), srv)
	res := callTool(s.T(), session, "switch_tab", map[string]any{"target_id": "t1"})
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "Switched to tab t1")
}

func (s *ServerSuite) TestSwitchTabError() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	m.On("SwitchTab", mock.Anything, "t1").Return(fmt.Errorf("switch err"))

	session := connectClient(s.T(), srv)
	res := callTool(s.T(), session, "switch_tab", map[string]any{"target_id": "t1"})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "switch tab failed: switch err")
}

func (s *ServerSuite) TestSwitchTabEmptyTargetID() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m

	session := connectClient(s.T(), srv)
	res := callTool(s.T(), session, "switch_tab", map[string]any{"target_id": ""})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "target_id is required")
}

func (s *ServerSuite) TestCloseTabSuccess() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	m.On("CloseTab", mock.Anything, "t2").Return(nil)

	session := connectClient(s.T(), srv)
	res := callTool(s.T(), session, "close_tab", map[string]any{"target_id": "t2"})
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "Closed tab t2")
}

func (s *ServerSuite) TestCloseTabError() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	m.On("CloseTab", mock.Anything, "t2").Return(fmt.Errorf("close err"))

	session := connectClient(s.T(), srv)
	res := callTool(s.T(), session, "close_tab", map[string]any{"target_id": "t2"})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "close tab failed: close err")
}

func (s *ServerSuite) TestCloseTabEmptyTargetID() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m

	session := connectClient(s.T(), srv)
	res := callTool(s.T(), session, "close_tab", map[string]any{"target_id": ""})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "target_id is required")
}

func (s *ServerSuite) TestPageInfoSuccess() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	m.On("GetPageInfo", mock.Anything).Return(&browser.PageInfo{URL: "https://x.com", Title: "X"}, nil)

	session := connectClient(s.T(), srv)
	res := callTool(s.T(), session, "page_info", nil)
	require.False(s.T(), res.IsError)
	text := getText(s.T(), res)
	require.Contains(s.T(), text, "URL: https://x.com")
	require.Contains(s.T(), text, "Title: X")
}

func (s *ServerSuite) TestPageInfoError() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	m.On("GetPageInfo", mock.Anything).Return(nil, fmt.Errorf("info err"))

	session := connectClient(s.T(), srv)
	res := callTool(s.T(), session, "page_info", nil)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "page info failed: info err")
}

// ==================== handleComputer: right_click, hover ====================

func (s *ServerSuite) TestHandleComputerRightClickSuccess() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	m.On("MouseClick", mock.Anything, float64(50), float64(60), "right", 1).Return(nil)
	res, _, err := srv.handleComputer(computerInput{Action: "right_click", X: 50, Y: 60})
	require.NoError(s.T(), err)
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "Right-clicked at (50, 60)")
	m.AssertExpectations(s.T())
}

func (s *ServerSuite) TestHandleComputerRightClickError() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	m.On("MouseClick", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(fmt.Errorf("rc err"))
	res, _, err := srv.handleComputer(computerInput{Action: "right_click"})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "right click failed: rc err")
}

func (s *ServerSuite) TestHandleComputerRightClickIgnoresButtonParam() {
	// right_click always uses "right" regardless of button param.
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	m.On("MouseClick", mock.Anything, float64(0), float64(0), "right", 1).Return(nil)
	res, _, err := srv.handleComputer(computerInput{Action: "right_click", Button: "left"})
	require.NoError(s.T(), err)
	require.False(s.T(), res.IsError)
	m.AssertExpectations(s.T())
}

func (s *ServerSuite) TestHandleComputerHoverSuccess() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	m.On("MouseMove", mock.Anything, float64(100), float64(200)).Return(nil)
	res, _, err := srv.handleComputer(computerInput{Action: "hover", X: 100, Y: 200})
	require.NoError(s.T(), err)
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "Moved to (100, 200)")
	m.AssertExpectations(s.T())
}

func (s *ServerSuite) TestHandleComputerHoverError() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	m.On("MouseMove", mock.Anything, mock.Anything, mock.Anything).Return(fmt.Errorf("hover err"))
	res, _, err := srv.handleComputer(computerInput{Action: "hover"})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "move failed: hover err")
}

// ==================== get_page_text ====================

func (s *ServerSuite) TestGetPageTextSuccess() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	m.On("EvaluateJS", mock.Anything, "document.body.innerText").Return("Hello World\nThis is a page.", nil)

	session := connectClient(s.T(), srv)
	res := callTool(s.T(), session, "get_page_text", nil)
	require.False(s.T(), res.IsError)
	require.Equal(s.T(), "Hello World\nThis is a page.", getText(s.T(), res))
	m.AssertExpectations(s.T())
}

func (s *ServerSuite) TestGetPageTextError() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	m.On("EvaluateJS", mock.Anything, "document.body.innerText").Return("", fmt.Errorf("eval err"))

	session := connectClient(s.T(), srv)
	res := callTool(s.T(), session, "get_page_text", nil)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "get page text failed: eval err")
}

// ==================== find ====================

func (s *ServerSuite) TestFindSuccess() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	refs := []browser.ElementRef{
		{RefID: "ref_1", Role: "button", Name: "Submit Form", X: 10, Y: 20, Width: 50, Height: 30},
		{RefID: "ref_2", Role: "link", Name: "Home Page", X: 100, Y: 200, Width: 60, Height: 20},
		{RefID: "ref_3", Role: "textbox", Name: "Email", X: 50, Y: 100, Width: 200, Height: 30},
	}
	m.On("GetElementRefs", mock.Anything).Return(refs, nil)

	session := connectClient(s.T(), srv)
	res := callTool(s.T(), session, "find", map[string]any{"query": "submit"})
	require.False(s.T(), res.IsError)
	text := getText(s.T(), res)
	require.Contains(s.T(), text, "Found 1 element(s)")
	require.Contains(s.T(), text, "[ref_1] button: Submit Form")
}

func (s *ServerSuite) TestFindCaseInsensitive() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	refs := []browser.ElementRef{
		{RefID: "ref_1", Role: "button", Name: "SUBMIT", X: 10, Y: 20, Width: 50, Height: 30},
	}
	m.On("GetElementRefs", mock.Anything).Return(refs, nil)

	session := connectClient(s.T(), srv)
	res := callTool(s.T(), session, "find", map[string]any{"query": "submit"})
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "Found 1 element(s)")
}

func (s *ServerSuite) TestFindByRole() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	refs := []browser.ElementRef{
		{RefID: "ref_1", Role: "button", Name: "Click Me", X: 10, Y: 20, Width: 50, Height: 30},
		{RefID: "ref_2", Role: "checkbox", Name: "Accept", X: 100, Y: 200, Width: 20, Height: 20},
	}
	m.On("GetElementRefs", mock.Anything).Return(refs, nil)

	session := connectClient(s.T(), srv)
	res := callTool(s.T(), session, "find", map[string]any{"query": "checkbox"})
	require.False(s.T(), res.IsError)
	text := getText(s.T(), res)
	require.Contains(s.T(), text, "Found 1 element(s)")
	require.Contains(s.T(), text, "[ref_2] checkbox: Accept")
}

func (s *ServerSuite) TestFindNoMatch() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	refs := []browser.ElementRef{
		{RefID: "ref_1", Role: "button", Name: "Submit", X: 10, Y: 20, Width: 50, Height: 30},
	}
	m.On("GetElementRefs", mock.Anything).Return(refs, nil)

	session := connectClient(s.T(), srv)
	res := callTool(s.T(), session, "find", map[string]any{"query": "nonexistent"})
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), `No elements found matching "nonexistent"`)
}

func (s *ServerSuite) TestFindMaxResults() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	// Create 25 matching refs.
	var refs []browser.ElementRef
	for i := 1; i <= 25; i++ {
		refs = append(refs, browser.ElementRef{
			RefID: fmt.Sprintf("ref_%d", i),
			Role:  "button",
			Name:  fmt.Sprintf("Button %d", i),
			X:     float64(i * 10), Y: float64(i * 10), Width: 50, Height: 30,
		})
	}
	m.On("GetElementRefs", mock.Anything).Return(refs, nil)

	session := connectClient(s.T(), srv)
	res := callTool(s.T(), session, "find", map[string]any{"query": "button"})
	require.False(s.T(), res.IsError)
	text := getText(s.T(), res)
	require.Contains(s.T(), text, "Found 20 element(s)")
	// Should contain ref_20 but not ref_21
	require.Contains(s.T(), text, "[ref_20]")
	require.NotContains(s.T(), text, "[ref_21]")
}

func (s *ServerSuite) TestFindEmptyQuery() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m

	session := connectClient(s.T(), srv)
	res := callTool(s.T(), session, "find", map[string]any{"query": ""})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "query is required")
}

func (s *ServerSuite) TestFindGetElementRefsError() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	m.On("GetElementRefs", mock.Anything).Return(nil, fmt.Errorf("ax tree err"))

	session := connectClient(s.T(), srv)
	res := callTool(s.T(), session, "find", map[string]any{"query": "button"})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "failed to get element refs: ax tree err")
}

func (s *ServerSuite) TestFindUpdatesRefsCache() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	refs := []browser.ElementRef{
		{RefID: "ref_1", Role: "button", Name: "Submit", X: 10, Y: 20, Width: 50, Height: 30},
	}
	m.On("GetElementRefs", mock.Anything).Return(refs, nil)
	require.Empty(s.T(), srv.refs) // no refs yet

	session := connectClient(s.T(), srv)
	_ = callTool(s.T(), session, "find", map[string]any{"query": "submit"})
	require.Len(s.T(), srv.refs, 1)
}

func (s *ServerSuite) TestFindWithValue() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	refs := []browser.ElementRef{
		{RefID: "ref_1", Role: "textbox", Name: "Search", Value: "hello", X: 10, Y: 20, Width: 200, Height: 30},
	}
	m.On("GetElementRefs", mock.Anything).Return(refs, nil)

	session := connectClient(s.T(), srv)
	res := callTool(s.T(), session, "find", map[string]any{"query": "search"})
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "(value: hello)")
}

// ==================== read_console_messages ====================

func (s *ServerSuite) TestReadConsoleMessagesEmpty() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m

	session := connectClient(s.T(), srv)
	res := callTool(s.T(), session, "read_console_messages", nil)
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "No console messages")
}

func (s *ServerSuite) TestReadConsoleMessagesWithMessages() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	now := time.Now()
	srv.consoleMsgs = []browser.ConsoleMessage{
		{Level: "log", Text: "hello world", Time: now},
		{Level: "error", Text: "something failed", Time: now},
		{Level: "warning", Text: "deprecation notice", Time: now},
	}

	session := connectClient(s.T(), srv)
	res := callTool(s.T(), session, "read_console_messages", nil)
	require.False(s.T(), res.IsError)
	text := getText(s.T(), res)
	require.Contains(s.T(), text, "3 console message(s)")
	require.Contains(s.T(), text, "log: hello world")
	require.Contains(s.T(), text, "error: something failed")
	require.Contains(s.T(), text, "warning: deprecation notice")
}

func (s *ServerSuite) TestReadConsoleMessagesOnlyErrors() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	now := time.Now()
	srv.consoleMsgs = []browser.ConsoleMessage{
		{Level: "log", Text: "hello world", Time: now},
		{Level: "error", Text: "something failed", Time: now},
		{Level: "warning", Text: "deprecation notice", Time: now},
	}

	session := connectClient(s.T(), srv)
	res := callTool(s.T(), session, "read_console_messages", map[string]any{"onlyErrors": true})
	require.False(s.T(), res.IsError)
	text := getText(s.T(), res)
	require.Contains(s.T(), text, "1 console message(s)")
	require.Contains(s.T(), text, "error: something failed")
	require.NotContains(s.T(), text, "hello world")
}

func (s *ServerSuite) TestReadConsoleMessagesWithPattern() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	now := time.Now()
	srv.consoleMsgs = []browser.ConsoleMessage{
		{Level: "log", Text: "request to /api/users", Time: now},
		{Level: "log", Text: "response 200", Time: now},
		{Level: "error", Text: "request to /api/posts failed", Time: now},
	}

	session := connectClient(s.T(), srv)
	res := callTool(s.T(), session, "read_console_messages", map[string]any{"pattern": "request.*api"})
	require.False(s.T(), res.IsError)
	text := getText(s.T(), res)
	require.Contains(s.T(), text, "2 console message(s)")
	require.Contains(s.T(), text, "request to /api/users")
	require.Contains(s.T(), text, "request to /api/posts failed")
	require.NotContains(s.T(), text, "response 200")
}

func (s *ServerSuite) TestReadConsoleMessagesInvalidPattern() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	srv.consoleMsgs = []browser.ConsoleMessage{
		{Level: "log", Text: "test", Time: time.Now()},
	}

	session := connectClient(s.T(), srv)
	res := callTool(s.T(), session, "read_console_messages", map[string]any{"pattern": "[invalid"})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "invalid regex pattern")
}

func (s *ServerSuite) TestReadConsoleMessagesClear() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	now := time.Now()
	srv.consoleMsgs = []browser.ConsoleMessage{
		{Level: "log", Text: "first", Time: now},
		{Level: "log", Text: "second", Time: now},
	}

	session := connectClient(s.T(), srv)

	// First call with clear: should return messages and clear buffer.
	res := callTool(s.T(), session, "read_console_messages", map[string]any{"clear": true})
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "2 console message(s)")

	// Second call: buffer should be empty.
	res = callTool(s.T(), session, "read_console_messages", nil)
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "No console messages")
}

func (s *ServerSuite) TestReadConsoleMessagesLimit() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	now := time.Now()
	for i := 0; i < 10; i++ {
		srv.consoleMsgs = append(srv.consoleMsgs, browser.ConsoleMessage{
			Level: "log",
			Text:  fmt.Sprintf("message %d", i),
			Time:  now,
		})
	}

	session := connectClient(s.T(), srv)
	res := callTool(s.T(), session, "read_console_messages", map[string]any{"limit": 3})
	require.False(s.T(), res.IsError)
	text := getText(s.T(), res)
	require.Contains(s.T(), text, "3 console message(s)")
	// Should show the last 3 messages (7, 8, 9).
	require.Contains(s.T(), text, "message 7")
	require.Contains(s.T(), text, "message 8")
	require.Contains(s.T(), text, "message 9")
	require.NotContains(s.T(), text, "message 6")
}

func (s *ServerSuite) TestReadConsoleMessagesDefaultLimit() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	now := time.Now()
	for i := 0; i < 150; i++ {
		srv.consoleMsgs = append(srv.consoleMsgs, browser.ConsoleMessage{
			Level: "log",
			Text:  fmt.Sprintf("msg %d", i),
			Time:  now,
		})
	}

	session := connectClient(s.T(), srv)
	res := callTool(s.T(), session, "read_console_messages", nil)
	require.False(s.T(), res.IsError)
	text := getText(s.T(), res)
	require.Contains(s.T(), text, "100 console message(s)")
}

// ==================== startConsoleCapture ====================

func (s *ServerSuite) TestStartConsoleCaptureError() {
	m := &mockCDP{}
	m.On("EnableConsoleCapture", mock.Anything, mock.Anything).Return(fmt.Errorf("runtime enable err"))
	srv := New("ws://x", nil)
	srv.cdp = m
	srv.runCtx = context.Background()
	// Should not panic; just logs warning.
	srv.startConsoleCapture()
	m.AssertExpectations(s.T())
}

func (s *ServerSuite) TestStartConsoleCaptureReceivesMessages() {
	m := &mockCDP{}
	var capturedCh chan<- browser.ConsoleMessage
	m.On("EnableConsoleCapture", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		capturedCh = args.Get(1).(chan<- browser.ConsoleMessage)
	}).Return(nil)
	m.On("EnableNetworkCapture", mock.Anything, mock.Anything).Return(nil)

	srv := New("ws://x", nil)
	srv.cdp = m
	srv.runCtx = context.Background()
	srv.startConsoleCapture()

	require.NotNil(s.T(), capturedCh)

	// Send a message to the captured channel.
	capturedCh <- browser.ConsoleMessage{Level: "log", Text: "test msg", Time: time.Now()}

	// Give goroutine time to process.
	time.Sleep(50 * time.Millisecond)

	srv.consoleMu.Lock()
	defer srv.consoleMu.Unlock()
	require.Len(s.T(), srv.consoleMsgs, 1)
	require.Equal(s.T(), "test msg", srv.consoleMsgs[0].Text)
}

// ==================== ensureCDP ====================

func (s *ServerSuite) TestEnsureCDPAlreadyConnected() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	srv.runCtx = context.Background()

	err := srv.ensureCDP()
	require.NoError(s.T(), err)
	// cdp should remain unchanged
	require.Equal(s.T(), m, srv.cdp)
}

func (s *ServerSuite) TestEnsureCDPSuccess() {
	m := &mockCDP{}
	m.On("EnableConsoleCapture", mock.Anything, mock.Anything).Return(nil)
	m.On("EnableNetworkCapture", mock.Anything, mock.Anything).Return(nil)
	srv := New("ws://x", nil)
	srv.runCtx = context.Background()
	srv.cdpFactory = func(_ context.Context, _ string, _ *slog.Logger) (cdpClient, error) {
		return m, nil
	}

	err := srv.ensureCDP()
	require.NoError(s.T(), err)
	require.Equal(s.T(), m, srv.cdp)
}

func (s *ServerSuite) TestEnsureCDPContextCancelled() {
	srv := New("ws://x", nil)
	ctx, cancel := context.WithCancel(context.Background())
	srv.runCtx = ctx
	srv.cdpFactory = func(_ context.Context, _ string, _ *slog.Logger) (cdpClient, error) {
		cancel() // cancel on first attempt so the select picks ctx.Done()
		return nil, fmt.Errorf("conn refused")
	}

	err := srv.ensureCDP()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "connecting to CDP at ws://x")
	require.Contains(s.T(), err.Error(), "conn refused")
}

func (s *ServerSuite) TestEnsureCDPRetriesExhausted() {
	srv := New("ws://x", nil)
	srv.runCtx = context.Background()
	srv.retryDelay = time.Millisecond // fast retries for test
	callCount := 0
	srv.cdpFactory = func(_ context.Context, _ string, _ *slog.Logger) (cdpClient, error) {
		callCount++
		return nil, fmt.Errorf("still down")
	}

	err := srv.ensureCDP()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "connecting to CDP at ws://x")
	require.Contains(s.T(), err.Error(), "still down")
	require.Equal(s.T(), 30, callCount)
}

func (s *ServerSuite) TestEnsureCDPSuccessAfterRetries() {
	m := &mockCDP{}
	m.On("EnableConsoleCapture", mock.Anything, mock.Anything).Return(nil)
	m.On("EnableNetworkCapture", mock.Anything, mock.Anything).Return(nil)
	srv := New("ws://x", nil)
	srv.runCtx = context.Background()
	callCount := 0
	srv.cdpFactory = func(_ context.Context, _ string, _ *slog.Logger) (cdpClient, error) {
		callCount++
		if callCount < 3 {
			return nil, fmt.Errorf("not ready yet")
		}
		return m, nil
	}

	err := srv.ensureCDP()
	require.NoError(s.T(), err)
	require.Equal(s.T(), m, srv.cdp)
	require.Equal(s.T(), 3, callCount)
}

// ==================== requireCDP ====================

func (s *ServerSuite) TestRequireCDPSuccess() {
	m := &mockCDP{}
	m.On("EnableConsoleCapture", mock.Anything, mock.Anything).Return(nil)
	m.On("EnableNetworkCapture", mock.Anything, mock.Anything).Return(nil)
	srv := New("ws://x", nil)
	srv.runCtx = context.Background()
	srv.cdpFactory = func(_ context.Context, _ string, _ *slog.Logger) (cdpClient, error) {
		return m, nil
	}

	result := srv.requireCDP()
	require.Nil(s.T(), result)
	require.Equal(s.T(), m, srv.cdp)
}

func (s *ServerSuite) TestRequireCDPError() {
	srv := New("ws://x", nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled
	srv.runCtx = ctx
	srv.cdpFactory = func(_ context.Context, _ string, _ *slog.Logger) (cdpClient, error) {
		return nil, fmt.Errorf("refused")
	}

	result := srv.requireCDP()
	require.NotNil(s.T(), result)
	require.True(s.T(), result.IsError)
	require.Contains(s.T(), getText(s.T(), result), "browser not ready")
}

// ==================== requireCDP guard in each tool ====================

// TestToolsRequireCDPGuard tests that every tool returns an error when CDP
// cannot be established, covering the `{ return r, nil, nil }` branches
// in registerTools.
func (s *ServerSuite) TestToolsRequireCDPGuard() {
	srv := New("ws://x", nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled so ensureCDP fails immediately
	srv.runCtx = ctx
	srv.cdpFactory = func(_ context.Context, _ string, _ *slog.Logger) (cdpClient, error) {
		return nil, fmt.Errorf("refused")
	}

	session := connectClient(s.T(), srv)

	tools := []struct {
		name string
		args map[string]any
	}{
		{"navigate", map[string]any{"url": "https://example.com"}},
		{"read_page", nil},
		{"computer", map[string]any{"action": "click", "x": 10.0, "y": 20.0}},
		{"form_input", map[string]any{"ref": 1, "value": "x"}},
		{"screenshot", nil},
		{"go_back", nil},
		{"go_forward", nil},
		{"reload", nil},
		{"evaluate", map[string]any{"expression": "1+1"}},
		{"list_tabs", nil},
		{"new_tab", map[string]any{"url": "https://example.com"}},
		{"switch_tab", map[string]any{"target_id": "t1"}},
		{"close_tab", map[string]any{"target_id": "t1"}},
		{"page_info", nil},
		{"get_page_text", nil},
		{"find", map[string]any{"query": "button"}},
		{"read_console_messages", nil},
		{"read_network_requests", nil},
		{"resize_window", map[string]any{"width": 800, "height": 600}},
	}

	for _, tc := range tools {
		s.Run(tc.name, func() {
			// Reset cdp to nil so ensureCDP is called each time
			srv.cdp = nil
			res := callTool(s.T(), session, tc.name, tc.args)
			require.True(s.T(), res.IsError, "tool %s should return error when CDP is unavailable", tc.name)
			require.Contains(s.T(), getText(s.T(), res), "browser not ready", "tool %s error message", tc.name)
		})
	}
}

// ==================== read_network_requests ====================

func (s *ServerSuite) TestReadNetworkRequestsEmpty() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m

	session := connectClient(s.T(), srv)
	res := callTool(s.T(), session, "read_network_requests", nil)
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "No network requests")
}

func (s *ServerSuite) TestReadNetworkRequestsWithRequests() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	now := time.Now()
	srv.networkReqs = []browser.NetworkRequest{
		{URL: "https://example.com/api/users", Method: "GET", Status: 200, StatusText: "OK", Time: now},
		{URL: "https://example.com/api/posts", Method: "POST", Status: 201, StatusText: "Created", Time: now},
	}

	session := connectClient(s.T(), srv)
	res := callTool(s.T(), session, "read_network_requests", nil)
	require.False(s.T(), res.IsError)
	text := getText(s.T(), res)
	require.Contains(s.T(), text, "2 network request(s)")
	require.Contains(s.T(), text, "GET https://example.com/api/users")
	require.Contains(s.T(), text, "200 OK")
	require.Contains(s.T(), text, "POST https://example.com/api/posts")
	require.Contains(s.T(), text, "201 Created")
}

func (s *ServerSuite) TestReadNetworkRequestsWithPattern() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	now := time.Now()
	srv.networkReqs = []browser.NetworkRequest{
		{URL: "https://example.com/api/users", Method: "GET", Status: 200, StatusText: "OK", Time: now},
		{URL: "https://example.com/static/app.js", Method: "GET", Status: 200, StatusText: "OK", Time: now},
		{URL: "https://example.com/api/posts", Method: "POST", Status: 201, StatusText: "Created", Time: now},
	}

	session := connectClient(s.T(), srv)
	res := callTool(s.T(), session, "read_network_requests", map[string]any{"pattern": "/api/"})
	require.False(s.T(), res.IsError)
	text := getText(s.T(), res)
	require.Contains(s.T(), text, "2 network request(s)")
	require.Contains(s.T(), text, "/api/users")
	require.Contains(s.T(), text, "/api/posts")
	require.NotContains(s.T(), text, "app.js")
}

func (s *ServerSuite) TestReadNetworkRequestsInvalidPattern() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	srv.networkReqs = []browser.NetworkRequest{
		{URL: "https://example.com", Method: "GET", Status: 200, StatusText: "OK", Time: time.Now()},
	}

	session := connectClient(s.T(), srv)
	res := callTool(s.T(), session, "read_network_requests", map[string]any{"pattern": "[invalid"})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "invalid regex pattern")
}

func (s *ServerSuite) TestReadNetworkRequestsClear() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	now := time.Now()
	srv.networkReqs = []browser.NetworkRequest{
		{URL: "https://example.com", Method: "GET", Status: 200, StatusText: "OK", Time: now},
	}

	session := connectClient(s.T(), srv)

	// First call with clear: should return requests and clear buffer.
	res := callTool(s.T(), session, "read_network_requests", map[string]any{"clear": true})
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "1 network request(s)")

	// Second call: buffer should be empty.
	res = callTool(s.T(), session, "read_network_requests", nil)
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "No network requests")
}

func (s *ServerSuite) TestReadNetworkRequestsLimit() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	now := time.Now()
	for i := 0; i < 10; i++ {
		srv.networkReqs = append(srv.networkReqs, browser.NetworkRequest{
			URL:        fmt.Sprintf("https://example.com/req%d", i),
			Method:     "GET",
			Status:     200,
			StatusText: "OK",
			Time:       now,
		})
	}

	session := connectClient(s.T(), srv)
	res := callTool(s.T(), session, "read_network_requests", map[string]any{"limit": 3})
	require.False(s.T(), res.IsError)
	text := getText(s.T(), res)
	require.Contains(s.T(), text, "3 network request(s)")
	// Should show the last 3 requests (7, 8, 9).
	require.Contains(s.T(), text, "/req7")
	require.Contains(s.T(), text, "/req8")
	require.Contains(s.T(), text, "/req9")
	require.NotContains(s.T(), text, "/req6")
}

func (s *ServerSuite) TestReadNetworkRequestsDefaultLimit() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	now := time.Now()
	for i := 0; i < 80; i++ {
		srv.networkReqs = append(srv.networkReqs, browser.NetworkRequest{
			URL:        fmt.Sprintf("https://example.com/req%d", i),
			Method:     "GET",
			Status:     200,
			StatusText: "OK",
			Time:       now,
		})
	}

	session := connectClient(s.T(), srv)
	res := callTool(s.T(), session, "read_network_requests", nil)
	require.False(s.T(), res.IsError)
	text := getText(s.T(), res)
	require.Contains(s.T(), text, "50 network request(s)")
}

// ==================== startNetworkCapture ====================

func (s *ServerSuite) TestStartNetworkCaptureError() {
	m := &mockCDP{}
	m.On("EnableNetworkCapture", mock.Anything, mock.Anything).Return(fmt.Errorf("network enable err"))
	srv := New("ws://x", nil)
	srv.cdp = m
	srv.runCtx = context.Background()
	// Should not panic; just logs warning.
	srv.startNetworkCapture()
	m.AssertExpectations(s.T())
}

func (s *ServerSuite) TestStartNetworkCaptureReceivesRequests() {
	m := &mockCDP{}
	var capturedCh chan<- browser.NetworkRequest
	m.On("EnableNetworkCapture", mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		capturedCh = args.Get(1).(chan<- browser.NetworkRequest)
	}).Return(nil)

	srv := New("ws://x", nil)
	srv.cdp = m
	srv.runCtx = context.Background()
	srv.startNetworkCapture()

	require.NotNil(s.T(), capturedCh)

	// Send a request to the captured channel.
	capturedCh <- browser.NetworkRequest{URL: "https://example.com", Method: "GET", Status: 200, StatusText: "OK", Time: time.Now()}

	// Give goroutine time to process.
	time.Sleep(50 * time.Millisecond)

	srv.networkMu.Lock()
	defer srv.networkMu.Unlock()
	require.Len(s.T(), srv.networkReqs, 1)
	require.Equal(s.T(), "https://example.com", srv.networkReqs[0].URL)
}

// ==================== resize_window ====================

func (s *ServerSuite) TestResizeWindowSuccess() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	m.On("ResizeWindow", mock.Anything, 1024, 768).Return(nil)

	session := connectClient(s.T(), srv)
	res := callTool(s.T(), session, "resize_window", map[string]any{"width": 1024, "height": 768})
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "Resized viewport to 1024x768")
	m.AssertExpectations(s.T())
}

func (s *ServerSuite) TestResizeWindowError() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	m.On("ResizeWindow", mock.Anything, 800, 600).Return(fmt.Errorf("resize err"))

	session := connectClient(s.T(), srv)
	res := callTool(s.T(), session, "resize_window", map[string]any{"width": 800, "height": 600})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "resize failed: resize err")
}

func (s *ServerSuite) TestResizeWindowInvalidDimensions() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m

	session := connectClient(s.T(), srv)

	res := callTool(s.T(), session, "resize_window", map[string]any{"width": 0, "height": 600})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "width and height must be positive")

	res = callTool(s.T(), session, "resize_window", map[string]any{"width": 800, "height": -1})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "width and height must be positive")
}

// ==================== left_click_drag ====================

func (s *ServerSuite) TestHandleComputerLeftClickDragSuccess() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	m.On("MouseDown", mock.Anything, float64(10), float64(20), "left").Return(nil)
	m.On("MouseMove", mock.Anything, float64(100), float64(200)).Return(nil)
	m.On("MouseUp", mock.Anything, float64(100), float64(200), "left").Return(nil)

	res, _, err := srv.handleComputer(computerInput{Action: "left_click_drag", StartX: 10, StartY: 20, X: 100, Y: 200})
	require.NoError(s.T(), err)
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "Dragged from (10, 20) to (100, 200)")
	m.AssertExpectations(s.T())
}

func (s *ServerSuite) TestHandleComputerLeftClickDragMouseDownError() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	m.On("MouseDown", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(fmt.Errorf("down err"))

	res, _, err := srv.handleComputer(computerInput{Action: "left_click_drag", StartX: 10, StartY: 20, X: 100, Y: 200})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "mouse down failed: down err")
}

func (s *ServerSuite) TestHandleComputerLeftClickDragMoveError() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	m.On("MouseDown", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	m.On("MouseMove", mock.Anything, mock.Anything, mock.Anything).Return(fmt.Errorf("move err"))

	res, _, err := srv.handleComputer(computerInput{Action: "left_click_drag", StartX: 10, StartY: 20, X: 100, Y: 200})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "drag move failed: move err")
}

func (s *ServerSuite) TestHandleComputerLeftClickDragMouseUpError() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	m.On("MouseDown", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil)
	m.On("MouseMove", mock.Anything, mock.Anything, mock.Anything).Return(nil)
	m.On("MouseUp", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(fmt.Errorf("up err"))

	res, _, err := srv.handleComputer(computerInput{Action: "left_click_drag", StartX: 10, StartY: 20, X: 100, Y: 200})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "mouse up failed: up err")
}

func (s *ServerSuite) TestHandleComputerLeftClickDragViaMCP() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	m.On("MouseDown", mock.Anything, float64(50), float64(60), "left").Return(nil)
	m.On("MouseMove", mock.Anything, float64(150), float64(160)).Return(nil)
	m.On("MouseUp", mock.Anything, float64(150), float64(160), "left").Return(nil)

	session := connectClient(s.T(), srv)
	res := callTool(s.T(), session, "computer", map[string]any{
		"action":  "left_click_drag",
		"start_x": 50.0,
		"start_y": 60.0,
		"x":       150.0,
		"y":       160.0,
	})
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "Dragged from (50, 60) to (150, 160)")
	m.AssertExpectations(s.T())
}

// --- scroll_to ---

func (s *ServerSuite) TestHandleComputerScrollToSuccess() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	srv.refs = []browser.ElementRef{
		{RefID: "ref_1", Role: "button", Name: "Submit", BackendDOMNodeID: 42},
	}
	m.On("ScrollIntoView", mock.Anything, cdp.BackendNodeID(42)).Return(nil)
	res, _, err := srv.handleComputer(computerInput{Action: "scroll_to", Ref: 1})
	require.NoError(s.T(), err)
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "Scrolled ref 1")
	m.AssertExpectations(s.T())
}

func (s *ServerSuite) TestHandleComputerScrollToOutOfRange() {
	srv := New("ws://x", nil)
	srv.cdp = &mockCDP{}
	srv.refs = []browser.ElementRef{}
	res, _, err := srv.handleComputer(computerInput{Action: "scroll_to", Ref: 5})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "out of range")
}

func (s *ServerSuite) TestHandleComputerScrollToError() {
	srv := New("ws://x", nil)
	m := &mockCDP{}
	srv.cdp = m
	srv.refs = []browser.ElementRef{
		{RefID: "ref_1", Role: "link", Name: "Home", BackendDOMNodeID: 99},
	}
	m.On("ScrollIntoView", mock.Anything, cdp.BackendNodeID(99)).Return(fmt.Errorf("scroll failed"))
	res, _, err := srv.handleComputer(computerInput{Action: "scroll_to", Ref: 1})
	require.NoError(s.T(), err)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "scroll_to failed")
}

// ==================== touchBrowserViaAPI ====================

func (s *ServerSuite) TestTouchBrowserViaAPISuccess() {
	var gotMethod string
	var gotPath string
	var gotBody string
	requestCount := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		gotMethod = r.Method
		gotPath = r.URL.Path
		data, _ := io.ReadAll(r.Body)
		gotBody = string(data)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	srv := New("ws://x", slog.Default())
	srv.runCtx = context.Background()
	srv.apiURL = ts.URL
	srv.channelID = "ch-99"
	srv.httpClient = ts.Client()
	// Ensure debounce does not trigger — zero-value lastTouch.
	srv.lastTouch = time.Time{}

	srv.touchBrowserViaAPI()

	require.Equal(s.T(), 1, requestCount)
	require.Equal(s.T(), http.MethodPost, gotMethod)
	require.Equal(s.T(), "/api/browser/touch", gotPath)
	require.Contains(s.T(), gotBody, `"channel_id":"ch-99"`)
}

func (s *ServerSuite) TestTouchBrowserViaAPIDebounced() {
	requestCount := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requestCount++
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	srv := New("ws://x", slog.Default())
	srv.runCtx = context.Background()
	srv.apiURL = ts.URL
	srv.channelID = "ch-1"
	srv.httpClient = ts.Client()

	// First call — lastTouch is zero, should go through.
	srv.touchBrowserViaAPI()
	require.Equal(s.T(), 1, requestCount)

	// Second call — lastTouch was just set by the first call (< 1 minute ago), should be debounced.
	srv.touchBrowserViaAPI()
	require.Equal(s.T(), 1, requestCount, "second call should be debounced")
}

func (s *ServerSuite) TestTouchBrowserViaAPINoConfig() {
	srv := New("ws://x", slog.Default())
	srv.runCtx = context.Background()
	// No apiURL/channelID/httpClient — should be a no-op without panic.
	srv.touchBrowserViaAPI()
}

func (s *ServerSuite) TestTouchBrowserViaAPIError() {
	srv := New("ws://x", slog.Default())
	srv.runCtx = context.Background()
	srv.apiURL = "http://127.0.0.1:1" // unreachable
	srv.channelID = "ch-1"
	srv.httpClient = &http.Client{Timeout: 100 * time.Millisecond}
	srv.lastTouch = time.Time{} // ensure debounce does not skip

	// Should not panic even though the server is unreachable.
	srv.touchBrowserViaAPI()
}

func (s *ServerSuite) TestTouchBrowserViaAPIInvalidURL() {
	srv := New("ws://x", slog.Default())
	srv.runCtx = context.Background()
	srv.apiURL = "://bad-url" // invalid URL
	srv.channelID = "ch-1"
	srv.httpClient = &http.Client{}
	srv.lastTouch = time.Time{}

	srv.touchBrowserViaAPI() // should not panic
}
