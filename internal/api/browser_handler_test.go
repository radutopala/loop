package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/browser"
)

type MockBrowserManager struct {
	mock.Mock
}

func (m *MockBrowserManager) EnsureBrowser(ctx context.Context, channelID, containerID string) error {
	return m.Called(ctx, channelID, containerID).Error(0)
}

func (m *MockBrowserManager) StopBrowser(ctx context.Context, channelID string) error {
	return m.Called(ctx, channelID).Error(0)
}

func (m *MockBrowserManager) IsRunning(ctx context.Context, channelID string) bool {
	return m.Called(ctx, channelID).Bool(0)
}

func (m *MockBrowserManager) GetCDPEndpoint(channelID string) string {
	return m.Called(channelID).String(0)
}

func (m *MockBrowserManager) GetContainerID(channelID string) (string, bool) {
	args := m.Called(channelID)
	return args.String(0), args.Bool(1)
}

func (m *MockBrowserManager) SetTargetID(channelID, targetID string) {
	m.Called(channelID, targetID)
}

func (m *MockBrowserManager) GetTargetID(channelID string) string {
	return m.Called(channelID).String(0)
}

func (m *MockBrowserManager) SetCDPForTarget(channelID, targetID string, cdp any) {
	m.Called(channelID, targetID, cdp)
}

func (m *MockBrowserManager) GetCDPForTarget(channelID, targetID string) any {
	return m.Called(channelID, targetID).Get(0)
}

func (m *MockBrowserManager) RemoveCDPForTarget(channelID, targetID string) any {
	return m.Called(channelID, targetID).Get(0)
}

func (m *MockBrowserManager) GetActiveCDP(channelID string) any {
	return m.Called(channelID).Get(0)
}

func (m *MockBrowserManager) TouchBrowser(channelID string) {
	m.Called(channelID)
}

func (m *MockBrowserManager) PaneConnected(channelID string) {
	m.Called(channelID)
}

func (m *MockBrowserManager) PaneDisconnected(channelID string) {
	m.Called(channelID)
}

func (m *MockBrowserManager) RunIdleMonitor(_ context.Context, _ time.Duration) {
	// no-op in tests
}

func (m *MockBrowserManager) NotifyTargetSwitch(channelID, targetID string) {
	m.Called(channelID, targetID)
}

func (m *MockBrowserManager) TargetSwitchCh(channelID string) <-chan string {
	args := m.Called(channelID)
	ch, _ := args.Get(0).(<-chan string)
	return ch
}

func (m *MockBrowserManager) NotifyTabAdded(channelID string, tab browser.TabInfo) {
	m.Called(channelID, tab)
}

func (m *MockBrowserManager) TabAddedCh(channelID string) <-chan browser.TabInfo {
	args := m.Called(channelID)
	ch, _ := args.Get(0).(<-chan browser.TabInfo)
	return ch
}

func (m *MockBrowserManager) NotifyTabRemoved(channelID, targetID string) {
	m.Called(channelID, targetID)
}

func (m *MockBrowserManager) TabRemovedCh(channelID string) <-chan string {
	args := m.Called(channelID)
	ch, _ := args.Get(0).(<-chan string)
	return ch
}

type BrowserHandlerSuite struct {
	suite.Suite
	browserMgr *MockBrowserManager
	cFinder    *MockContainerFinder
	srv        *Server
}

func TestBrowserHandlerSuite(t *testing.T) {
	suite.Run(t, new(BrowserHandlerSuite))
}

func (s *BrowserHandlerSuite) SetupTest() {
	s.browserMgr = new(MockBrowserManager)
	s.browserMgr.On("PaneConnected", mock.Anything).Maybe().Return()
	s.browserMgr.On("PaneDisconnected", mock.Anything).Maybe().Return()
	s.browserMgr.On("TargetSwitchCh", mock.Anything).Maybe().Return((<-chan string)(nil))
	s.browserMgr.On("TabAddedCh", mock.Anything).Maybe().Return((<-chan browser.TabInfo)(nil))
	s.browserMgr.On("TabRemovedCh", mock.Anything).Maybe().Return((<-chan string)(nil))
	s.browserMgr.On("TrackTab", mock.Anything, mock.Anything).Maybe().Return()
	s.browserMgr.On("UntrackTab", mock.Anything, mock.Anything).Maybe().Return()
	s.browserMgr.On("NextTabID", mock.Anything, mock.Anything).Maybe().Return("")
	s.cFinder = new(MockContainerFinder)
	s.srv = nilServer()
	s.srv.SetBrowserManager(s.browserMgr)
	s.srv.containerFinder = s.cFinder
	// Use 1 retry with no delay so error tests don't sleep.
	s.srv.browserCDPRetries = 1
	s.srv.browserCDPDelay = 0
}

func (s *BrowserHandlerSuite) dialBrowserWS() (*websocket.Conn, *httptest.Server) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/ws/browser", s.srv.handleBrowserWS)
	ts := httptest.NewServer(mux)
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/ws/browser"
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(s.T(), err)
	return ws, ts
}

func (s *BrowserHandlerSuite) readResp(ws *websocket.Conn) browserWSResponse {
	var resp browserWSResponse
	err := ws.ReadJSON(&resp)
	require.NoError(s.T(), err)
	return resp
}

func (s *BrowserHandlerSuite) TestSetBrowserManager() {
	srv := nilServer()
	require.Nil(s.T(), srv.browserManager)

	mgr := new(MockBrowserManager)
	srv.SetBrowserManager(mgr)
	require.NotNil(s.T(), srv.browserManager)
}

func (s *BrowserHandlerSuite) TestBrowserWSNotConfigured() {
	srv := nilServer()
	// No browser manager set.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/ws/browser", srv.handleBrowserWS)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/ws/browser"
	_, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.Error(s.T(), err)
	require.Equal(s.T(), http.StatusServiceUnavailable, resp.StatusCode)
}

func (s *BrowserHandlerSuite) TestUnknownMessageType() {
	ws, ts := s.dialBrowserWS()
	defer ts.Close()
	defer ws.Close()

	err := ws.WriteJSON(browserWSMessage{Type: "unknown"})
	require.NoError(s.T(), err)

	resp := s.readResp(ws)
	require.Equal(s.T(), bwsRespError, resp.Type)
	require.Contains(s.T(), resp.Message, "unknown message type")
}

func (s *BrowserHandlerSuite) TestInvalidJSON() {
	ws, ts := s.dialBrowserWS()
	defer ts.Close()
	defer ws.Close()

	err := ws.WriteMessage(websocket.TextMessage, []byte("not json"))
	require.NoError(s.T(), err)

	resp := s.readResp(ws)
	require.Equal(s.T(), bwsRespError, resp.Type)
	require.Contains(s.T(), resp.Message, "invalid JSON")
}

func (s *BrowserHandlerSuite) TestStartNoChannelID() {
	ws, ts := s.dialBrowserWS()
	defer ts.Close()
	defer ws.Close()

	err := ws.WriteJSON(browserWSMessage{Type: bwsMsgStart})
	require.NoError(s.T(), err)

	resp := s.readResp(ws)
	require.Equal(s.T(), bwsRespError, resp.Type)
	require.Contains(s.T(), resp.Message, "channel_id required")
}

func (s *BrowserHandlerSuite) TestStartEnsureBrowserError() {
	ws, ts := s.dialBrowserWS()
	defer ts.Close()
	defer ws.Close()

	s.browserMgr.On("EnsureBrowser", mock.Anything, "ch-1", "").
		Return(errTestAPI)

	err := ws.WriteJSON(browserWSMessage{Type: bwsMsgStart, ChannelID: "ch-1"})
	require.NoError(s.T(), err)

	resp := s.readResp(ws)
	require.Equal(s.T(), bwsRespError, resp.Type)
	require.Contains(s.T(), resp.Message, "failed to start browser")
}

func (s *BrowserHandlerSuite) TestStopNoSession() {
	ws, ts := s.dialBrowserWS()
	defer ts.Close()
	defer ws.Close()

	s.browserMgr.On("StopBrowser", mock.Anything, "ch-1").Return(nil)

	err := ws.WriteJSON(browserWSMessage{Type: bwsMsgStop, ChannelID: "ch-1"})
	require.NoError(s.T(), err)

	resp := s.readResp(ws)
	require.Equal(s.T(), bwsRespStopped, resp.Type)
}

func (s *BrowserHandlerSuite) TestStopNoChannelID() {
	ws, ts := s.dialBrowserWS()
	defer ts.Close()
	defer ws.Close()

	err := ws.WriteJSON(browserWSMessage{Type: bwsMsgStop})
	require.NoError(s.T(), err)

	resp := s.readResp(ws)
	require.Equal(s.T(), bwsRespStopped, resp.Type)
}

func (s *BrowserHandlerSuite) TestScreencastNoBrowser() {
	ws, ts := s.dialBrowserWS()
	defer ts.Close()
	defer ws.Close()

	err := ws.WriteJSON(browserWSMessage{Type: bwsMsgScreencast})
	require.NoError(s.T(), err)

	resp := s.readResp(ws)
	require.Equal(s.T(), bwsRespError, resp.Type)
	require.Contains(s.T(), resp.Message, "browser not started")
}

func (s *BrowserHandlerSuite) TestInputNoBrowser() {
	ws, ts := s.dialBrowserWS()
	defer ts.Close()
	defer ws.Close()

	err := ws.WriteJSON(browserWSMessage{
		Type:      bwsMsgInput,
		InputType: "click",
		X:         100,
		Y:         200,
	})
	require.NoError(s.T(), err)

	// Input with no CDP should silently fail (no error sent back).
	// Send another message to verify connection still works.
	err = ws.WriteJSON(browserWSMessage{Type: "unknown"})
	require.NoError(s.T(), err)

	resp := s.readResp(ws)
	require.Equal(s.T(), bwsRespError, resp.Type)
}

func (s *BrowserHandlerSuite) TestBrowserWSRoute() {
	// Verify the route is registered in Start().
	srv := nilServer()
	srv.SetBrowserManager(s.browserMgr)

	err := srv.Start("127.0.0.1:0")
	require.NoError(s.T(), err)
	defer func() { _ = srv.Stop(context.Background()) }()
}

var errTestAPI = errors.New("test error")

// --- Mock CDP Client ---

type MockCDPClient struct {
	mock.Mock
}

func (m *MockCDPClient) Navigate(ctx context.Context, url string) error {
	return m.Called(ctx, url).Error(0)
}
func (m *MockCDPClient) Reload(ctx context.Context) error    { return m.Called(ctx).Error(0) }
func (m *MockCDPClient) GoBack(ctx context.Context) error    { return m.Called(ctx).Error(0) }
func (m *MockCDPClient) GoForward(ctx context.Context) error { return m.Called(ctx).Error(0) }
func (m *MockCDPClient) GetPageInfo(ctx context.Context) (*browser.PageInfo, error) {
	args := m.Called(ctx)
	pi, _ := args.Get(0).(*browser.PageInfo)
	return pi, args.Error(1)
}
func (m *MockCDPClient) StartScreencast(quality, maxWidth, maxHeight int) <-chan []byte {
	args := m.Called(quality, maxWidth, maxHeight)
	ch, _ := args.Get(0).(<-chan []byte)
	return ch
}
func (m *MockCDPClient) StopScreencast()  { m.Called() }
func (m *MockCDPClient) ResetScreencast() { m.Called() }
func (m *MockCDPClient) MouseClick(ctx context.Context, x, y float64, button string, clickCount int) error {
	return m.Called(ctx, x, y, button, clickCount).Error(0)
}
func (m *MockCDPClient) MouseMove(ctx context.Context, x, y float64) error {
	return m.Called(ctx, x, y).Error(0)
}
func (m *MockCDPClient) MouseScroll(ctx context.Context, x, y, deltaX, deltaY float64) error {
	return m.Called(ctx, x, y, deltaX, deltaY).Error(0)
}
func (m *MockCDPClient) KeyPress(ctx context.Context, key string) error {
	return m.Called(ctx, key).Error(0)
}
func (m *MockCDPClient) TypeText(ctx context.Context, text string) error {
	return m.Called(ctx, text).Error(0)
}
func (m *MockCDPClient) TargetID() string { return m.Called().String(0) }
func (m *MockCDPClient) SwitchTarget(targetID string) error {
	return m.Called(targetID).Error(0)
}
func (m *MockCDPClient) ListTabs(ctx context.Context) ([]browser.TabInfo, error) {
	args := m.Called(ctx)
	tabs, _ := args.Get(0).([]browser.TabInfo)
	return tabs, args.Error(1)
}
func (m *MockCDPClient) NewTab(ctx context.Context, url string) (string, error) {
	args := m.Called(ctx, url)
	return args.String(0), args.Error(1)
}
func (m *MockCDPClient) CloseTab(ctx context.Context, targetID string) error {
	return m.Called(ctx, targetID).Error(0)
}
func (m *MockCDPClient) EvaluateJS(ctx context.Context, expression string) (string, error) {
	args := m.Called(ctx, expression)
	return args.String(0), args.Error(1)
}
func (m *MockCDPClient) Close() { m.Called() }
func (m *MockCDPClient) GetElementRefs(ctx context.Context) ([]browser.ElementRef, error) {
	args := m.Called(ctx)
	refs, _ := args.Get(0).([]browser.ElementRef)
	return refs, args.Error(1)
}
func (m *MockCDPClient) ClickRef(ctx context.Context, refs []browser.ElementRef, refIndex int) error {
	return m.Called(ctx, refs, refIndex).Error(0)
}
func (m *MockCDPClient) Screenshot(ctx context.Context) ([]byte, error) {
	args := m.Called(ctx)
	data, _ := args.Get(0).([]byte)
	return data, args.Error(1)
}
func (m *MockCDPClient) EnableConsoleCapture(ctx context.Context, ch chan<- browser.ConsoleMessage) error {
	return m.Called(ctx, ch).Error(0)
}
func (m *MockCDPClient) EnableNetworkCapture(ctx context.Context, ch chan<- browser.NetworkRequest) error {
	return m.Called(ctx, ch).Error(0)
}
func (m *MockCDPClient) ResizeWindow(ctx context.Context, width, height int) error {
	return m.Called(ctx, width, height).Error(0)
}
func (m *MockCDPClient) ScrollIntoView(ctx context.Context, backendNodeID cdp.BackendNodeID) error {
	return m.Called(ctx, backendNodeID).Error(0)
}
func (m *MockCDPClient) MouseDown(ctx context.Context, x, y float64, button string) error {
	return m.Called(ctx, x, y, button).Error(0)
}
func (m *MockCDPClient) MouseUp(ctx context.Context, x, y float64, button string) error {
	return m.Called(ctx, x, y, button).Error(0)
}

// --- Helper: start browser and get WS with CDP mock ---

func (s *BrowserHandlerSuite) startBrowserWS() (*websocket.Conn, *httptest.Server, *MockCDPClient) {
	mockCDP := new(MockCDPClient)
	s.srv.browserCDPFactory = func(_ context.Context, _ string, _ *slog.Logger, _ ...browser.CDPOption) (browserCDPClient, error) {
		return mockCDP, nil
	}

	s.browserMgr.On("EnsureBrowser", mock.Anything, "ch-1", "").Return(nil)
	s.browserMgr.On("GetActiveCDP", "ch-1").Return(nil)
	s.browserMgr.On("GetCDPEndpoint", "ch-1").Return("ws://127.0.0.1:9222")
	s.browserMgr.On("SetCDPForTarget", "ch-1", mock.Anything, mock.Anything).Return().Maybe()
	s.browserMgr.On("SetTargetID", "ch-1", mock.Anything).Return().Maybe()

	mockCDP.On("TargetID").Return("").Maybe()
	mockCDP.On("ListTabs", mock.Anything).Return([]browser.TabInfo(nil), nil).Maybe()

	ws, ts := s.dialBrowserWS()

	// Send start message.
	require.NoError(s.T(), ws.WriteJSON(browserWSMessage{Type: bwsMsgStart, ChannelID: "ch-1"}))
	resp := s.readResp(ws)
	require.Equal(s.T(), bwsRespStarted, resp.Type)

	mockCDP.On("Close").Return().Maybe()
	mockCDP.On("StopScreencast").Return().Maybe()
	return ws, ts, mockCDP
}

// --- Start success ---

func (s *BrowserHandlerSuite) TestStartSuccess() {
	ws, ts, _ := s.startBrowserWS()
	defer ts.Close()
	defer ws.Close()
}

func (s *BrowserHandlerSuite) TestStartCachedCDPReuse() {
	mockCDP := new(MockCDPClient)
	mockCDP.On("ResetScreencast").Return().Maybe()
	mockCDP.On("StopScreencast").Return().Maybe()
	mockCDP.On("Close").Return().Maybe()
	mockCDP.On("TargetID").Return("cached-target").Maybe()

	s.browserMgr.On("EnsureBrowser", mock.Anything, "ch-1", "").Return(nil)
	// GetActiveCDP returns the cached mock client.
	s.browserMgr.On("GetActiveCDP", "ch-1").Return(mockCDP)

	ws, ts := s.dialBrowserWS()
	defer ts.Close()
	defer ws.Close()

	require.NoError(s.T(), ws.WriteJSON(browserWSMessage{Type: bwsMsgStart, ChannelID: "ch-1"}))
	resp := s.readResp(ws)
	require.Equal(s.T(), bwsRespStarted, resp.Type)

	// Verify that the factory was NOT called (no new CDP creation).
	s.browserMgr.AssertNotCalled(s.T(), "GetCDPEndpoint", mock.Anything)
}

func (s *BrowserHandlerSuite) TestStartWithNonNilTargetID() {
	mockCDP := new(MockCDPClient)
	s.srv.browserCDPFactory = func(_ context.Context, _ string, _ *slog.Logger, _ ...browser.CDPOption) (browserCDPClient, error) {
		return mockCDP, nil
	}

	s.browserMgr.On("EnsureBrowser", mock.Anything, "ch-1", "").Return(nil)
	s.browserMgr.On("GetActiveCDP", "ch-1").Return(nil)
	s.browserMgr.On("GetCDPEndpoint", "ch-1").Return("ws://127.0.0.1:9222")
	s.browserMgr.On("SetCDPForTarget", "ch-1", "my-target-id", mock.Anything).Return().Maybe()
	s.browserMgr.On("SetTargetID", "ch-1", "my-target-id").Return().Maybe()

	mockCDP.On("TargetID").Return("my-target-id")
	mockCDP.On("ListTabs", mock.Anything).Return([]browser.TabInfo(nil), nil).Maybe()
	mockCDP.On("Close").Return().Maybe()
	mockCDP.On("StopScreencast").Return().Maybe()

	ws, ts := s.dialBrowserWS()
	defer ts.Close()
	defer ws.Close()

	require.NoError(s.T(), ws.WriteJSON(browserWSMessage{Type: bwsMsgStart, ChannelID: "ch-1"}))
	resp := s.readResp(ws)
	require.Equal(s.T(), bwsRespStarted, resp.Type)

	// SetTargetID should have been called with the target ID.
	time.Sleep(20 * time.Millisecond)
	s.browserMgr.AssertCalled(s.T(), "SetTargetID", "ch-1", "my-target-id")
}

func (s *BrowserHandlerSuite) TestStartCDPConnectError() {
	s.srv.browserCDPFactory = func(_ context.Context, _ string, _ *slog.Logger, _ ...browser.CDPOption) (browserCDPClient, error) {
		return nil, errors.New("cdp connect failed")
	}

	s.browserMgr.On("EnsureBrowser", mock.Anything, "ch-1", "").Return(nil)
	s.browserMgr.On("GetActiveCDP", "ch-1").Return(nil)
	s.browserMgr.On("GetCDPEndpoint", "ch-1").Return("ws://127.0.0.1:9222")

	ws, ts := s.dialBrowserWS()
	defer ts.Close()
	defer ws.Close()

	require.NoError(s.T(), ws.WriteJSON(browserWSMessage{Type: bwsMsgStart, ChannelID: "ch-1"}))
	resp := s.readResp(ws)
	require.Equal(s.T(), bwsRespError, resp.Type)
	require.Contains(s.T(), resp.Message, "failed to connect CDP")
}

func (s *BrowserHandlerSuite) TestStartCDPRetryContextCancelled() {
	ctx, cancel := context.WithCancel(context.Background())
	s.srv.browserCDPFactory = func(_ context.Context, _ string, _ *slog.Logger, _ ...browser.CDPOption) (browserCDPClient, error) {
		cancel() // Cancel context on first attempt so the retry loop exits.
		return nil, errors.New("not ready")
	}
	s.srv.browserCDPRetries = 3
	s.srv.browserCDPDelay = time.Second // Would sleep, but context is cancelled.

	s.browserMgr.On("EnsureBrowser", mock.Anything, "ch-1", "").Return(nil)
	s.browserMgr.On("GetActiveCDP", "ch-1").Return(nil)
	s.browserMgr.On("GetCDPEndpoint", "ch-1").Return("ws://172.17.0.2:9222")

	// Use a custom server with a handler that uses a cancellable context.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/ws/browser", func(w http.ResponseWriter, r *http.Request) {
		s.srv.handleBrowserWS(w, r.WithContext(ctx))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/ws/browser"
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(s.T(), err)
	defer ws.Close()

	require.NoError(s.T(), ws.WriteJSON(browserWSMessage{Type: bwsMsgStart, ChannelID: "ch-1"}))
	resp := s.readResp(ws)
	require.Equal(s.T(), bwsRespError, resp.Type)
	require.Contains(s.T(), resp.Message, "context cancelled")
}

func (s *BrowserHandlerSuite) TestStartCDPRetrySuccess() {
	mockCDP := new(MockCDPClient)
	attempt := 0
	s.srv.browserCDPFactory = func(_ context.Context, _ string, _ *slog.Logger, _ ...browser.CDPOption) (browserCDPClient, error) {
		attempt++
		if attempt == 1 {
			return nil, errors.New("not ready")
		}
		return mockCDP, nil
	}
	s.srv.browserCDPRetries = 3
	s.srv.browserCDPDelay = time.Millisecond

	s.browserMgr.On("EnsureBrowser", mock.Anything, "ch-1", "").Return(nil)
	s.browserMgr.On("GetActiveCDP", "ch-1").Return(nil)
	s.browserMgr.On("GetCDPEndpoint", "ch-1").Return("ws://172.17.0.2:9222")
	s.browserMgr.On("SetCDPForTarget", "ch-1", mock.Anything, mock.Anything).Return().Maybe()
	s.browserMgr.On("SetTargetID", "ch-1", mock.Anything).Return().Maybe()

	mockCDP.On("TargetID").Return("").Maybe()
	mockCDP.On("ListTabs", mock.Anything).Return([]browser.TabInfo(nil), nil).Maybe()

	ws, ts := s.dialBrowserWS()
	defer ts.Close()
	defer ws.Close()

	mockCDP.On("Close").Return().Maybe()
	mockCDP.On("StopScreencast").Return().Maybe()

	require.NoError(s.T(), ws.WriteJSON(browserWSMessage{Type: bwsMsgStart, ChannelID: "ch-1"}))
	resp := s.readResp(ws)
	require.Equal(s.T(), bwsRespStarted, resp.Type)
	require.Equal(s.T(), 2, attempt)
}

// --- Screencast success ---

func (s *BrowserHandlerSuite) TestScreencastSendsBinaryFrames() {
	ws, ts, mockCDP := s.startBrowserWS()
	defer ts.Close()
	defer ws.Close()

	frameCh := make(chan []byte, 2)
	mockCDP.On("StartScreencast", 60, 1280, 900).Return((<-chan []byte)(frameCh))

	require.NoError(s.T(), ws.WriteJSON(browserWSMessage{Type: bwsMsgScreencast}))

	// Push two JPEG-like frames into the channel.
	frame1 := []byte{0xFF, 0xD8, 0x01, 0x02, 0x03}
	frame2 := []byte{0xFF, 0xD8, 0x04, 0x05, 0x06}
	frameCh <- frame1
	frameCh <- frame2

	// Read both binary messages from the WebSocket.
	require.NoError(s.T(), ws.SetReadDeadline(time.Now().Add(2*time.Second)))
	msgType, data, err := ws.ReadMessage()
	require.NoError(s.T(), err)
	require.Equal(s.T(), websocket.BinaryMessage, msgType)
	require.Equal(s.T(), frame1, data)

	msgType, data, err = ws.ReadMessage()
	require.NoError(s.T(), err)
	require.Equal(s.T(), websocket.BinaryMessage, msgType)
	require.Equal(s.T(), frame2, data)

	// Close frameCh so pipeFrames goroutine exits cleanly.
	close(frameCh)
	time.Sleep(20 * time.Millisecond)
}

func (s *BrowserHandlerSuite) TestWsFrameSenderStopCh() {
	stopCh := make(chan struct{})
	bc := &browserWSConn{stopCh: stopCh}
	sender := &wsFrameSender{bc: bc, stopCh: stopCh}
	ch := sender.StopCh()
	require.NotNil(s.T(), ch)
	close(stopCh)
	<-ch // should not block
}

// --- dispatchInput all types ---

func (s *BrowserHandlerSuite) TestInputClick() {
	ws, ts, mockCDP := s.startBrowserWS()
	defer ts.Close()
	defer ws.Close()

	mockCDP.On("MouseClick", mock.Anything, float64(100), float64(200), "left", 1).Return(nil)
	require.NoError(s.T(), ws.WriteJSON(browserWSMessage{Type: bwsMsgInput, InputType: "click", X: 100, Y: 200}))
	time.Sleep(20 * time.Millisecond)
	mockCDP.AssertCalled(s.T(), "MouseClick", mock.Anything, float64(100), float64(200), "left", 1)
}

func (s *BrowserHandlerSuite) TestInputClickWithButtonAndCount() {
	ws, ts, mockCDP := s.startBrowserWS()
	defer ts.Close()
	defer ws.Close()

	mockCDP.On("MouseClick", mock.Anything, float64(10), float64(20), "right", 2).Return(nil)
	require.NoError(s.T(), ws.WriteJSON(browserWSMessage{Type: bwsMsgInput, InputType: "click", X: 10, Y: 20, Button: "right", ClickCount: 2}))
	time.Sleep(20 * time.Millisecond)
	mockCDP.AssertCalled(s.T(), "MouseClick", mock.Anything, float64(10), float64(20), "right", 2)
}

func (s *BrowserHandlerSuite) TestInputMouseMove() {
	ws, ts, mockCDP := s.startBrowserWS()
	defer ts.Close()
	defer ws.Close()

	mockCDP.On("MouseMove", mock.Anything, float64(50), float64(60)).Return(nil)
	require.NoError(s.T(), ws.WriteJSON(browserWSMessage{Type: bwsMsgInput, InputType: "mousemove", X: 50, Y: 60}))
	time.Sleep(20 * time.Millisecond)
	mockCDP.AssertCalled(s.T(), "MouseMove", mock.Anything, float64(50), float64(60))
}

func (s *BrowserHandlerSuite) TestInputScroll() {
	ws, ts, mockCDP := s.startBrowserWS()
	defer ts.Close()
	defer ws.Close()

	mockCDP.On("MouseScroll", mock.Anything, float64(0), float64(0), float64(0), float64(-3)).Return(nil)
	require.NoError(s.T(), ws.WriteJSON(browserWSMessage{Type: bwsMsgInput, InputType: "scroll", DeltaY: -3}))
	time.Sleep(20 * time.Millisecond)
	mockCDP.AssertCalled(s.T(), "MouseScroll", mock.Anything, float64(0), float64(0), float64(0), float64(-3))
}

func (s *BrowserHandlerSuite) TestInputKeyPress() {
	ws, ts, mockCDP := s.startBrowserWS()
	defer ts.Close()
	defer ws.Close()

	mockCDP.On("KeyPress", mock.Anything, "Enter").Return(nil)
	require.NoError(s.T(), ws.WriteJSON(browserWSMessage{Type: bwsMsgInput, InputType: "keypress", Key: "Enter"}))
	time.Sleep(20 * time.Millisecond)
	mockCDP.AssertCalled(s.T(), "KeyPress", mock.Anything, "Enter")
}

func (s *BrowserHandlerSuite) TestInputTypeText() {
	ws, ts, mockCDP := s.startBrowserWS()
	defer ts.Close()
	defer ws.Close()

	mockCDP.On("TypeText", mock.Anything, "hello").Return(nil)
	require.NoError(s.T(), ws.WriteJSON(browserWSMessage{Type: bwsMsgInput, InputType: "typetext", Text: "hello"}))
	time.Sleep(20 * time.Millisecond)
	mockCDP.AssertCalled(s.T(), "TypeText", mock.Anything, "hello")
}

func (s *BrowserHandlerSuite) TestInputDispatchError() {
	ws, ts, mockCDP := s.startBrowserWS()
	defer ts.Close()
	defer ws.Close()

	mockCDP.On("MouseClick", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("click fail"))
	require.NoError(s.T(), ws.WriteJSON(browserWSMessage{Type: bwsMsgInput, InputType: "click", X: 1, Y: 2}))
	time.Sleep(20 * time.Millisecond)
	// Error is logged, no response sent. Connection still works.
	require.NoError(s.T(), ws.WriteJSON(browserWSMessage{Type: "unknown"}))
	resp := s.readResp(ws)
	require.Equal(s.T(), bwsRespError, resp.Type)
}

// --- pipeFrames ---

// mockFrameSender implements frameSender for deterministic pipeFrames tests.
type mockFrameSender struct {
	sendErr error
	stopCh  chan struct{}
}

func (m *mockFrameSender) SendFrame([]byte) error  { return m.sendErr }
func (m *mockFrameSender) StopCh() <-chan struct{} { return m.stopCh }

func (s *BrowserHandlerSuite) TestPipeFramesStopCh() {
	bc := &browserWSConn{
		logger: slog.Default(),
		stopCh: make(chan struct{}),
	}

	ms := &mockFrameSender{stopCh: make(chan struct{})}
	frameCh := make(chan []byte, 1)

	done := make(chan struct{})
	go func() {
		bc.pipeFrames(frameCh, ms)
		close(done)
	}()

	close(bc.stopCh)
	select {
	case <-done:
	case <-time.After(time.Second):
		s.T().Fatal("pipeFrames did not exit on stopCh close")
	}
}

func (s *BrowserHandlerSuite) TestPipeFramesChannelClosed() {
	bc := &browserWSConn{
		logger: slog.Default(),
		stopCh: make(chan struct{}),
	}

	ms := &mockFrameSender{stopCh: make(chan struct{})}
	frameCh := make(chan []byte, 1)

	done := make(chan struct{})
	go func() {
		bc.pipeFrames(frameCh, ms)
		close(done)
	}()

	close(frameCh)
	select {
	case <-done:
	case <-time.After(time.Second):
		s.T().Fatal("pipeFrames did not exit on frameCh close")
	}
}

func (s *BrowserHandlerSuite) TestPipeFramesSendFrameError() {
	bc := &browserWSConn{
		logger: slog.Default(),
		stopCh: make(chan struct{}),
	}

	// SendFrame always returns error; StopCh is never closed.
	ms := &mockFrameSender{
		sendErr: errors.New("send failed"),
		stopCh:  make(chan struct{}),
	}

	frameCh := make(chan []byte, 1)
	frameCh <- []byte("frame-data")

	done := make(chan struct{})
	go func() {
		bc.pipeFrames(frameCh, ms)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		s.T().Fatal("pipeFrames did not exit on SendFrame error")
	}
}

func (s *BrowserHandlerSuite) TestPipeFramesSendFrameSuccess() {
	bc := &browserWSConn{
		logger: slog.Default(),
		stopCh: make(chan struct{}),
	}

	// SendFrame succeeds (nil error); StopCh is never closed.
	ms := &mockFrameSender{stopCh: make(chan struct{})}

	frameCh := make(chan []byte, 1)
	frameCh <- []byte("frame-data")

	done := make(chan struct{})
	go func() {
		bc.pipeFrames(frameCh, ms)
		close(done)
	}()

	// pipeFrames reads the frame, SendFrame returns nil, loops back.
	// Now close bc.stopCh to let it exit.
	time.Sleep(10 * time.Millisecond)
	close(bc.stopCh)

	select {
	case <-done:
	case <-time.After(time.Second):
		s.T().Fatal("pipeFrames did not exit")
	}
}

func (s *BrowserHandlerSuite) TestPipeFramesStreamStopCh() {
	bc := &browserWSConn{
		logger: slog.Default(),
		stopCh: make(chan struct{}),
	}

	ms := &mockFrameSender{stopCh: make(chan struct{})}
	frameCh := make(chan []byte, 1)

	done := make(chan struct{})
	go func() {
		bc.pipeFrames(frameCh, ms)
		close(done)
	}()

	close(ms.stopCh)
	select {
	case <-done:
	case <-time.After(time.Second):
		s.T().Fatal("pipeFrames did not exit on stream StopCh close")
	}
}

// --- cleanup with stream ---

func (s *BrowserHandlerSuite) TestCleanupWithStream() {
	ws, ts, _ := s.startBrowserWS()
	defer ts.Close()
	// Closing ws triggers cleanup via deferred bc.cleanup() in handleBrowserWS.
	ws.Close()
	time.Sleep(50 * time.Millisecond)
}

// --- Default factory vars coverage ---

func (s *BrowserHandlerSuite) TestBrowserWSUpgradeError() {
	srv := nilServer()
	mgr := new(MockBrowserManager)
	srv.SetBrowserManager(mgr)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/ws/browser", srv.handleBrowserWS)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	// Send a plain GET request (not a WebSocket upgrade) — triggers upgrade error.
	resp, err := http.Get(ts.URL + "/api/ws/browser")
	require.NoError(s.T(), err)
	resp.Body.Close()
}

func (s *BrowserHandlerSuite) TestSendJSONWriteError() {
	// Start a browser WS, then close the underlying connection before sending.
	ws, ts, _ := s.startBrowserWS()
	defer ts.Close()

	// Close the WS connection so the next server-side sendJSON fails.
	ws.Close()
	time.Sleep(20 * time.Millisecond)

	// The handleBrowserWS loop will exit on ReadMessage error.
	// The sendJSON error path is covered when the connection drops mid-write.
	// We can't easily trigger it from outside since the loop exited, but the
	// upgrade error test above covers the log-only path adequately.
	// Instead, test sendJSON directly by creating a browserWSConn with a closed conn.
	connReady := make(chan *websocket.Conn, 1)
	tsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		connReady <- conn
	}))
	defer tsSrv.Close()

	wsURL := "ws" + strings.TrimPrefix(tsSrv.URL, "http") + "/"
	clientWS, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(s.T(), err)
	defer clientWS.Close()

	serverConn := <-connReady
	serverConn.Close()

	bc := &browserWSConn{
		conn:   serverConn,
		logger: slog.Default(),
		stopCh: make(chan struct{}),
	}
	// sendJSON should log the error but not panic.
	bc.sendJSON(browserWSResponse{Type: bwsRespError, Message: "test"})
}

func (s *BrowserHandlerSuite) TestSetBrowserManagerFactories() {
	srv := nilServer()
	mgr := new(MockBrowserManager)
	srv.SetBrowserManager(mgr)

	// Verify CDP factory was set and exercises the real constructor.
	require.NotNil(s.T(), srv.browserCDPFactory)
	_, err := srv.browserCDPFactory(context.Background(), "ws://127.0.0.1:9222", slog.Default())
	_ = err // expected to fail (no Chrome)
}

// --- watchMCPTabChanges ---

func (s *BrowserHandlerSuite) TestWatchMCPTabChangesStopCh() {
	bc := &browserWSConn{
		logger: slog.Default(),
		bMgr:   s.browserMgr,
		stopCh: make(chan struct{}),
	}

	// Return nil channels — goroutine should exit on stopCh.
	s.browserMgr.On("TargetSwitchCh", "ch-1").Return((<-chan string)(nil))
	s.browserMgr.On("TabAddedCh", "ch-1").Return((<-chan browser.TabInfo)(nil))
	s.browserMgr.On("TabRemovedCh", "ch-1").Return((<-chan string)(nil))

	done := make(chan struct{})
	go func() {
		bc.watchMCPTabChanges("ch-1")
		close(done)
	}()

	close(bc.stopCh)
	select {
	case <-done:
	case <-time.After(time.Second):
		s.T().Fatal("watchMCPTabChanges did not exit on stopCh")
	}
}

func (s *BrowserHandlerSuite) TestWatchMCPTabChangesSwitchChClosed() {
	switchCh := make(chan string)
	tabAddedCh := make(chan browser.TabInfo)
	tabRemovedCh := make(chan string)

	mgr := new(MockBrowserManager)
	mgr.On("TargetSwitchCh", "ch-w").Return((<-chan string)(switchCh))
	mgr.On("TabAddedCh", "ch-w").Return((<-chan browser.TabInfo)(tabAddedCh))
	mgr.On("TabRemovedCh", "ch-w").Return((<-chan string)(tabRemovedCh))

	bc := &browserWSConn{
		logger: slog.Default(),
		bMgr:   mgr,
		stopCh: make(chan struct{}),
	}

	done := make(chan struct{})
	go func() {
		bc.watchMCPTabChanges("ch-w")
		close(done)
	}()

	close(switchCh)
	select {
	case <-done:
	case <-time.After(time.Second):
		s.T().Fatal("watchMCPTabChanges did not exit on switchCh close")
	}
}

func (s *BrowserHandlerSuite) TestWatchMCPTabChangesTabAddedChClosed() {
	switchCh := make(chan string)
	tabAddedCh := make(chan browser.TabInfo)
	tabRemovedCh := make(chan string)

	mgr := new(MockBrowserManager)
	mgr.On("TargetSwitchCh", "ch-w").Return((<-chan string)(switchCh))
	mgr.On("TabAddedCh", "ch-w").Return((<-chan browser.TabInfo)(tabAddedCh))
	mgr.On("TabRemovedCh", "ch-w").Return((<-chan string)(tabRemovedCh))

	bc := &browserWSConn{
		logger: slog.Default(),
		bMgr:   mgr,
		stopCh: make(chan struct{}),
	}

	done := make(chan struct{})
	go func() {
		bc.watchMCPTabChanges("ch-w")
		close(done)
	}()

	close(tabAddedCh)
	select {
	case <-done:
	case <-time.After(time.Second):
		s.T().Fatal("watchMCPTabChanges did not exit on tabAddedCh close")
	}
}

func (s *BrowserHandlerSuite) TestWatchMCPTabChangesTabRemovedChClosed() {
	switchCh := make(chan string)
	tabAddedCh := make(chan browser.TabInfo)
	tabRemovedCh := make(chan string)

	mgr := new(MockBrowserManager)
	mgr.On("TargetSwitchCh", "ch-w").Return((<-chan string)(switchCh))
	mgr.On("TabAddedCh", "ch-w").Return((<-chan browser.TabInfo)(tabAddedCh))
	mgr.On("TabRemovedCh", "ch-w").Return((<-chan string)(tabRemovedCh))

	bc := &browserWSConn{
		logger: slog.Default(),
		bMgr:   mgr,
		stopCh: make(chan struct{}),
	}

	done := make(chan struct{})
	go func() {
		bc.watchMCPTabChanges("ch-w")
		close(done)
	}()

	close(tabRemovedCh)
	select {
	case <-done:
	case <-time.After(time.Second):
		s.T().Fatal("watchMCPTabChanges did not exit on tabRemovedCh close")
	}
}

func (s *BrowserHandlerSuite) TestWatchMCPTabChangesSwitchNoCDP() {
	switchCh := make(chan string, 1)
	tabAddedCh := make(chan browser.TabInfo)
	tabRemovedCh := make(chan string)

	mgr := new(MockBrowserManager)
	mgr.On("TargetSwitchCh", "ch-w").Return((<-chan string)(switchCh))
	mgr.On("TabAddedCh", "ch-w").Return((<-chan browser.TabInfo)(tabAddedCh))
	mgr.On("TabRemovedCh", "ch-w").Return((<-chan string)(tabRemovedCh))

	bc := &browserWSConn{
		logger: slog.Default(),
		bMgr:   mgr,
		stopCh: make(chan struct{}),
		// cdp is nil — should exit when it reads from switchCh.
	}

	done := make(chan struct{})
	go func() {
		bc.watchMCPTabChanges("ch-w")
		close(done)
	}()

	switchCh <- "t-new"
	select {
	case <-done:
	case <-time.After(time.Second):
		s.T().Fatal("watchMCPTabChanges did not exit when cdp is nil")
	}
}

// TestWatchMCPTabChangesTabAddedSendsWS verifies that a tab_added channel
// signal results in a tab_created WS message (no CDP needed).
func (s *BrowserHandlerSuite) TestWatchMCPTabChangesTabAddedSendsWS() {
	tabAddedCh := make(chan browser.TabInfo, 1)
	tabRemovedCh := make(chan string)

	s.browserMgr.ExpectedCalls = filterCalls(s.browserMgr.ExpectedCalls, "TargetSwitchCh")
	s.browserMgr.ExpectedCalls = filterCalls(s.browserMgr.ExpectedCalls, "TabAddedCh")
	s.browserMgr.ExpectedCalls = filterCalls(s.browserMgr.ExpectedCalls, "TabRemovedCh")
	s.browserMgr.On("TargetSwitchCh", "ch-1").Return((<-chan string)(nil))
	s.browserMgr.On("TabAddedCh", "ch-1").Return((<-chan browser.TabInfo)(tabAddedCh))
	s.browserMgr.On("TabRemovedCh", "ch-1").Return((<-chan string)(tabRemovedCh))

	ws, ts, _ := s.startBrowserWS()
	defer ts.Close()
	defer ws.Close()

	// Trigger tab added.
	tabAddedCh <- browser.TabInfo{TargetID: "t-new", URL: "https://new.com", Title: "New"}

	// Read the tab_created response sent by the watcher goroutine.
	require.NoError(s.T(), ws.SetReadDeadline(time.Now().Add(2*time.Second)))
	resp := s.readResp(ws)
	require.Equal(s.T(), bwsRespTabCreated, resp.Type)
	require.Equal(s.T(), "t-new", resp.TargetID)
	require.Equal(s.T(), "https://new.com", resp.URL)
	require.Equal(s.T(), "New", resp.Title)
}

// TestWatchMCPTabChangesTabRemovedSendsWS verifies that a tab_removed channel
// signal results in a tab_closed WS message (no CDP needed).
func (s *BrowserHandlerSuite) TestWatchMCPTabChangesTabRemovedSendsWS() {
	tabAddedCh := make(chan browser.TabInfo)
	tabRemovedCh := make(chan string, 1)

	s.browserMgr.ExpectedCalls = filterCalls(s.browserMgr.ExpectedCalls, "TargetSwitchCh")
	s.browserMgr.ExpectedCalls = filterCalls(s.browserMgr.ExpectedCalls, "TabAddedCh")
	s.browserMgr.ExpectedCalls = filterCalls(s.browserMgr.ExpectedCalls, "TabRemovedCh")
	s.browserMgr.On("TargetSwitchCh", "ch-1").Return((<-chan string)(nil))
	s.browserMgr.On("TabAddedCh", "ch-1").Return((<-chan browser.TabInfo)(tabAddedCh))
	s.browserMgr.On("TabRemovedCh", "ch-1").Return((<-chan string)(tabRemovedCh))

	ws, ts, _ := s.startBrowserWS()
	defer ts.Close()
	defer ws.Close()

	// Trigger tab removed.
	tabRemovedCh <- "t-old"

	// Read the tab_closed response sent by the watcher goroutine.
	require.NoError(s.T(), ws.SetReadDeadline(time.Now().Add(2*time.Second)))
	resp := s.readResp(ws)
	require.Equal(s.T(), bwsRespTabClosed, resp.Type)
	require.Equal(s.T(), "t-old", resp.TargetID)
}

// Note: TestWatchMCPTabChangesTabAddedSendsWS and TestWatchMCPTabChangesTabRemovedSendsWS
// above test the new tab_added/tab_removed WS notifications. No ListTabs call needed.

// filterCalls removes mock expectations for a given method name.
func filterCalls(calls []*mock.Call, method string) []*mock.Call {
	var filtered []*mock.Call
	for _, c := range calls {
		if c.Method != method {
			filtered = append(filtered, c)
		}
	}
	return filtered
}

func (s *BrowserHandlerSuite) TestWatchMCPTabChangesSwitchWithCDP() {
	switchCh := make(chan string, 1)

	s.browserMgr.ExpectedCalls = filterCalls(s.browserMgr.ExpectedCalls, "TargetSwitchCh")
	s.browserMgr.ExpectedCalls = filterCalls(s.browserMgr.ExpectedCalls, "TabAddedCh")
	s.browserMgr.ExpectedCalls = filterCalls(s.browserMgr.ExpectedCalls, "TabRemovedCh")
	s.browserMgr.On("TargetSwitchCh", "ch-1").Return((<-chan string)(switchCh))
	s.browserMgr.On("TabAddedCh", "ch-1").Return((<-chan browser.TabInfo)(nil))
	s.browserMgr.On("TabRemovedCh", "ch-1").Return((<-chan string)(nil))

	ws, ts, _ := s.startBrowserWS()
	defer ts.Close()
	defer ws.Close()

	// Factory returns a new mock CDP for the switched target.
	newMockCDP := new(MockCDPClient)
	newMockCDP.On("TargetID").Return("t-mcp").Maybe()
	newMockCDP.On("ResetScreencast").Return().Maybe()
	newMockCDP.On("StartScreencast", 60, 1920, 1080).Return((<-chan []byte)(make(chan []byte)))
	newMockCDP.On("ListTabs", mock.Anything).Return([]browser.TabInfo{
		{TargetID: "t-mcp", URL: "https://mcp.com", Title: "MCP"},
	}, nil)
	newMockCDP.On("Close").Return().Maybe()
	newMockCDP.On("StopScreencast").Return().Maybe()
	newMockCDP.On("Reload", mock.Anything).Return(nil).Maybe()
	newMockCDP.On("EvaluateJS", mock.Anything, mock.Anything).Return("", nil).Maybe()

	s.srv.browserCDPFactory = func(_ context.Context, _ string, _ *slog.Logger, _ ...browser.CDPOption) (browserCDPClient, error) {
		return newMockCDP, nil
	}
	s.browserMgr.On("RemoveCDPForTarget", "ch-1", "t-mcp").Return(nil)
	s.browserMgr.On("GetCDPEndpoint", "ch-1").Return("ws://127.0.0.1:9222")
	s.browserMgr.On("SetCDPForTarget", "ch-1", "t-mcp", newMockCDP).Return()
	s.browserMgr.On("SetTargetID", "ch-1", "t-mcp").Return()

	// Trigger MCP-initiated switch.
	switchCh <- "t-mcp"

	// Read the tab_switched and tabs responses.
	require.NoError(s.T(), ws.SetReadDeadline(time.Now().Add(2*time.Second)))
	resp := s.readResp(ws)
	require.Equal(s.T(), bwsRespTabSwitched, resp.Type)
	require.Equal(s.T(), "t-mcp", resp.TargetID)

	resp = s.readResp(ws)
	require.Equal(s.T(), bwsRespTabs, resp.Type)
}

func (m *MockBrowserManager) TrackTab(channelID, targetID string)   { m.Called(channelID, targetID) }
func (m *MockBrowserManager) UntrackTab(channelID, targetID string) { m.Called(channelID, targetID) }
func (m *MockBrowserManager) NextTabID(channelID, closedTargetID string) string {
	args := m.Called(channelID, closedTargetID)
	return args.String(0)
}
func (m *MockBrowserManager) OrderTabs(_ string, tabs []browser.TabInfo) []browser.TabInfo {
	return tabs
}

// --- handleBrowserAction tests ---

func (s *BrowserHandlerSuite) postBrowserAction(req browserActionRequest) *httptest.ResponseRecorder {
	data, _ := json.Marshal(req)
	r := httptest.NewRequest(http.MethodPost, "/api/browser/action", strings.NewReader(string(data)))
	w := httptest.NewRecorder()
	s.srv.handleBrowserAction(w, r)
	return w
}

func (s *BrowserHandlerSuite) TestBrowserActionNoBrowserManager() {
	srv := nilServer()
	body := strings.NewReader(`{"channel_id":"ch-1","action":"navigate","params":{"url":"https://example.com"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/browser/action", body)
	w := httptest.NewRecorder()
	srv.handleBrowserAction(w, req)
	require.Equal(s.T(), http.StatusServiceUnavailable, w.Code)
}

func (s *BrowserHandlerSuite) TestBrowserActionMissingChannelID() {
	body := strings.NewReader(`{"action":"navigate","params":{"url":"https://example.com"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/browser/action", body)
	w := httptest.NewRecorder()
	s.srv.handleBrowserAction(w, req)
	require.Equal(s.T(), http.StatusBadRequest, w.Code)
}

func (s *BrowserHandlerSuite) TestBrowserActionInvalidJSON() {
	body := strings.NewReader(`not json`)
	req := httptest.NewRequest(http.MethodPost, "/api/browser/action", body)
	w := httptest.NewRecorder()
	s.srv.handleBrowserAction(w, req)
	require.Equal(s.T(), http.StatusBadRequest, w.Code)
}

// setupActionMocks sets up common mocks for handleBrowserAction tests that create a new CDP client.
func (s *BrowserHandlerSuite) setupActionMocks(mockCDP *MockCDPClient) {
	s.browserMgr.On("GetActiveCDP", "ch-1").Return(nil).Maybe()
	s.browserMgr.On("EnsureBrowser", mock.Anything, "ch-1", "").Return(nil).Maybe()
	s.browserMgr.On("GetCDPEndpoint", "ch-1").Return("ws://127.0.0.1:9222").Maybe()
	s.browserMgr.On("SetCDPForTarget", "ch-1", mock.Anything, mock.Anything).Return().Maybe()
	s.browserMgr.On("SetTargetID", "ch-1", mock.Anything).Return().Maybe()
	s.browserMgr.On("GetTargetID", "ch-1").Return("").Maybe()
	s.browserMgr.On("TouchBrowser", "ch-1").Return().Maybe()

	mockCDP.On("TargetID").Return("").Maybe()
	mockCDP.On("EnableConsoleCapture", mock.Anything, mock.Anything).Return(nil).Maybe()
	mockCDP.On("EnableNetworkCapture", mock.Anything, mock.Anything).Return(nil).Maybe()

	s.srv.browserCDPFactory = func(_ context.Context, _ string, _ *slog.Logger, _ ...browser.CDPOption) (browserCDPClient, error) {
		return mockCDP, nil
	}
}

func (s *BrowserHandlerSuite) TestBrowserActionNavigateSuccess() {
	mockCDP := new(MockCDPClient)
	s.setupActionMocks(mockCDP)

	mockCDP.On("Navigate", mock.Anything, "https://example.com").Return(nil)
	mockCDP.On("GetPageInfo", mock.Anything).Return(&browser.PageInfo{URL: "https://example.com", Title: "Example"}, nil)

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "navigate",
		Params:    map[string]any{"url": "https://example.com"},
	})

	require.Equal(s.T(), http.StatusOK, w.Code)
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Empty(s.T(), resp.Error)
	require.NotNil(s.T(), resp.PageInfo)
	require.Equal(s.T(), "https://example.com", resp.PageInfo.URL)
}

func (s *BrowserHandlerSuite) TestBrowserActionNavigateError() {
	mockCDP := new(MockCDPClient)
	s.setupActionMocks(mockCDP)

	mockCDP.On("Navigate", mock.Anything, "https://bad.com").Return(errors.New("nav fail"))

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "navigate",
		Params:    map[string]any{"url": "https://bad.com"},
	})

	require.Equal(s.T(), http.StatusOK, w.Code)
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Error, "navigate failed")
}

func (s *BrowserHandlerSuite) TestBrowserActionScreenshot() {
	mockCDP := new(MockCDPClient)
	s.setupActionMocks(mockCDP)

	mockCDP.On("Screenshot", mock.Anything).Return([]byte{0x89, 0x50, 0x4E, 0x47}, nil)

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "screenshot",
	})

	require.Equal(s.T(), http.StatusOK, w.Code)
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Empty(s.T(), resp.Error)
	require.NotEmpty(s.T(), resp.Image)
}

func (s *BrowserHandlerSuite) TestBrowserActionListTabs() {
	mockCDP := new(MockCDPClient)
	s.setupActionMocks(mockCDP)

	mockCDP.On("ListTabs", mock.Anything).Return([]browser.TabInfo{
		{TargetID: "t1", URL: "https://a.com", Title: "A"},
	}, nil)

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "list_tabs",
	})

	require.Equal(s.T(), http.StatusOK, w.Code)
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Empty(s.T(), resp.Error)
	require.Len(s.T(), resp.Tabs, 1)
	require.Equal(s.T(), "t1", resp.Tabs[0].TargetID)
}

func (s *BrowserHandlerSuite) TestBrowserActionNewTab() {
	mockCDP := new(MockCDPClient)
	s.setupActionMocks(mockCDP)

	mockCDP.On("NewTab", mock.Anything, "about:blank").Return("new-target", nil)
	s.browserMgr.On("NotifyTabAdded", "ch-1", mock.Anything).Return().Maybe()
	s.browserMgr.On("NotifyTargetSwitch", "ch-1", "new-target").Return().Maybe()

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "new_tab",
	})

	require.Equal(s.T(), http.StatusOK, w.Code)
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Empty(s.T(), resp.Error)
	require.Contains(s.T(), resp.Result, "new-target")
}

func (s *BrowserHandlerSuite) TestBrowserActionCloseTab() {
	mockCDP := new(MockCDPClient)
	s.setupActionMocks(mockCDP)

	mockCDP.On("CloseTab", mock.Anything, "t1").Return(nil)
	s.browserMgr.On("NotifyTabRemoved", "ch-1", "t1").Return().Maybe()

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "close_tab",
		Params:    map[string]any{"target_id": "t1"},
	})

	require.Equal(s.T(), http.StatusOK, w.Code)
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Empty(s.T(), resp.Error)
	require.Contains(s.T(), resp.Result, "t1")
}

func (s *BrowserHandlerSuite) TestBrowserActionReadConsole() {
	mockCDP := new(MockCDPClient)
	s.setupActionMocks(mockCDP)

	// Pre-populate capture state so we have messages to read.
	s.srv.browserCapturesMu.Lock()
	if s.srv.browserCaptures == nil {
		s.srv.browserCaptures = make(map[string]*browserCaptureState)
	}
	cs := &browserCaptureState{started: true}
	cs.consoleMsgs = []browser.ConsoleMessage{
		{Level: "log", Text: "hello world", Time: time.Now()},
		{Level: "error", Text: "something failed", Time: time.Now()},
	}
	s.srv.browserCaptures["ch-1"] = cs
	s.srv.browserCapturesMu.Unlock()

	// Use a cached CDP so setupActionMocks doesn't override capture state.
	s.browserMgr.ExpectedCalls = filterCalls(s.browserMgr.ExpectedCalls, "GetActiveCDP")
	s.browserMgr.On("GetActiveCDP", "ch-1").Return(mockCDP)

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "read_console",
		Params:    map[string]any{"limit": float64(10)},
	})

	require.Equal(s.T(), http.StatusOK, w.Code)
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Empty(s.T(), resp.Error)
	require.Contains(s.T(), resp.Result, "console message")
}

func (s *BrowserHandlerSuite) TestBrowserActionReadNetwork() {
	mockCDP := new(MockCDPClient)
	s.setupActionMocks(mockCDP)

	// Pre-populate capture state so we have requests to read.
	s.srv.browserCapturesMu.Lock()
	if s.srv.browserCaptures == nil {
		s.srv.browserCaptures = make(map[string]*browserCaptureState)
	}
	cs := &browserCaptureState{started: true}
	cs.networkReqs = []browser.NetworkRequest{
		{URL: "https://api.example.com/v1", Method: "GET", Status: 200, StatusText: "OK", Time: time.Now()},
	}
	s.srv.browserCaptures["ch-1"] = cs
	s.srv.browserCapturesMu.Unlock()

	// Use a cached CDP so setupActionMocks doesn't override capture state.
	s.browserMgr.ExpectedCalls = filterCalls(s.browserMgr.ExpectedCalls, "GetActiveCDP")
	s.browserMgr.On("GetActiveCDP", "ch-1").Return(mockCDP)

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "read_network",
	})

	require.Equal(s.T(), http.StatusOK, w.Code)
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Empty(s.T(), resp.Error)
	require.Contains(s.T(), resp.Result, "network request")
}

func (s *BrowserHandlerSuite) TestBrowserActionUnknownAction() {
	mockCDP := new(MockCDPClient)
	s.setupActionMocks(mockCDP)

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "does_not_exist",
	})

	require.Equal(s.T(), http.StatusOK, w.Code)
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Error, "unknown action")
}

func (s *BrowserHandlerSuite) TestBrowserActionGetBrowserCDPCached() {
	mockCDP := new(MockCDPClient)
	mockCDP.On("TargetID").Return("t-cached").Maybe()
	mockCDP.On("GetPageInfo", mock.Anything).Return(&browser.PageInfo{URL: "https://example.com", Title: "X"}, nil)

	// GetCDP returns a cached client — factory should NOT be called.
	s.browserMgr.On("GetActiveCDP", "ch-1").Return(mockCDP)
	s.browserMgr.On("TouchBrowser", "ch-1").Return().Maybe()

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "get_page_info",
	})

	require.Equal(s.T(), http.StatusOK, w.Code)
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Empty(s.T(), resp.Error)
	require.NotNil(s.T(), resp.PageInfo)

	// Factory should not have been called.
	s.browserMgr.AssertNotCalled(s.T(), "EnsureBrowser", mock.Anything, mock.Anything, mock.Anything)
}

func (s *BrowserHandlerSuite) TestBrowserActionGetBrowserCDPNew() {
	mockCDP := new(MockCDPClient)
	s.setupActionMocks(mockCDP)

	mockCDP.On("GetPageInfo", mock.Anything).Return(&browser.PageInfo{URL: "https://example.com", Title: "X"}, nil)

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "get_page_info",
	})

	require.Equal(s.T(), http.StatusOK, w.Code)
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Empty(s.T(), resp.Error)
	require.NotNil(s.T(), resp.PageInfo)
	s.browserMgr.AssertCalled(s.T(), "EnsureBrowser", mock.Anything, "ch-1", "")
}

// --- getBrowserCDP error paths ---

func (s *BrowserHandlerSuite) TestBrowserActionGetBrowserCDPEnsureError() {
	s.browserMgr.On("GetActiveCDP", "ch-1").Return(nil)
	s.browserMgr.On("EnsureBrowser", mock.Anything, "ch-1", "").Return(errors.New("ensure fail"))
	s.browserMgr.On("TouchBrowser", "ch-1").Return().Maybe()

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "get_page_info",
	})

	require.Equal(s.T(), http.StatusOK, w.Code)
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Error, "ensuring browser")
}

func (s *BrowserHandlerSuite) TestBrowserActionGetBrowserCDPNoEndpoint() {
	s.browserMgr.On("GetActiveCDP", "ch-1").Return(nil)
	s.browserMgr.On("EnsureBrowser", mock.Anything, "ch-1", "").Return(nil)
	s.browserMgr.On("GetCDPEndpoint", "ch-1").Return("")
	s.browserMgr.On("TouchBrowser", "ch-1").Return().Maybe()

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "get_page_info",
	})

	require.Equal(s.T(), http.StatusOK, w.Code)
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Error, "no CDP endpoint")
}

func (s *BrowserHandlerSuite) TestBrowserActionGetBrowserCDPRetryAndFail() {
	// browserCDPRetries is 1 by default in SetupTest — factory always fails → returns error immediately.
	s.browserMgr.On("GetActiveCDP", "ch-1").Return(nil)
	s.browserMgr.On("EnsureBrowser", mock.Anything, "ch-1", "").Return(nil)
	s.browserMgr.On("GetCDPEndpoint", "ch-1").Return("ws://127.0.0.1:9222")
	s.browserMgr.On("TouchBrowser", "ch-1").Return().Maybe()

	s.srv.browserCDPFactory = func(_ context.Context, _ string, _ *slog.Logger, _ ...browser.CDPOption) (browserCDPClient, error) {
		return nil, errors.New("cdp connect fail")
	}

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "get_page_info",
	})

	require.Equal(s.T(), http.StatusOK, w.Code)
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Error, "connecting CDP")
}

func (s *BrowserHandlerSuite) TestBrowserActionGetBrowserCDPRetryContextCancel() {
	// Set retries > 1 so the retry delay select is reached.
	s.srv.browserCDPRetries = 3
	s.srv.browserCDPDelay = time.Second // long delay, will be cancelled

	ctx, cancel := context.WithCancel(context.Background())

	s.browserMgr.On("GetActiveCDP", "ch-1").Return(nil)
	s.browserMgr.On("EnsureBrowser", mock.Anything, "ch-1", "").Return(nil)
	s.browserMgr.On("GetCDPEndpoint", "ch-1").Return("ws://127.0.0.1:9222")
	s.browserMgr.On("TouchBrowser", "ch-1").Return().Maybe()

	s.srv.browserCDPFactory = func(_ context.Context, _ string, _ *slog.Logger, _ ...browser.CDPOption) (browserCDPClient, error) {
		cancel() // cancel context on first attempt so retry select hits ctx.Done()
		return nil, errors.New("not ready")
	}

	body, _ := json.Marshal(browserActionRequest{ChannelID: "ch-1", Action: "get_page_info"})
	req := httptest.NewRequest(http.MethodPost, "/api/browser/action", strings.NewReader(string(body))).WithContext(ctx)
	w := httptest.NewRecorder()
	s.srv.handleBrowserAction(w, req)

	require.Equal(s.T(), http.StatusOK, w.Code)
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.NotEmpty(s.T(), resp.Error)
}

// --- ensureBrowserCapture paths ---

func (s *BrowserHandlerSuite) TestEnsureBrowserCaptureAlreadyStarted() {
	mockCDP := new(MockCDPClient)
	// Pre-populate as already started.
	s.srv.browserCapturesMu.Lock()
	s.srv.browserCaptures = map[string]*browserCaptureState{
		"ch-1": {started: true},
	}
	s.srv.browserCapturesMu.Unlock()

	// ensureBrowserCapture should be a no-op — EnableConsoleCapture not called.
	s.srv.ensureBrowserCapture(context.Background(), "ch-1", mockCDP)

	mockCDP.AssertNotCalled(s.T(), "EnableConsoleCapture", mock.Anything, mock.Anything)
	mockCDP.AssertNotCalled(s.T(), "EnableNetworkCapture", mock.Anything, mock.Anything)
}

func (s *BrowserHandlerSuite) TestEnsureBrowserCaptureConsoleCaptureError() {
	mockCDP := new(MockCDPClient)
	mockCDP.On("EnableConsoleCapture", mock.Anything, mock.Anything).Return(errors.New("console cap fail"))
	mockCDP.On("EnableNetworkCapture", mock.Anything, mock.Anything).Return(nil)

	s.srv.ensureBrowserCapture(context.Background(), "ch-1", mockCDP)

	mockCDP.AssertCalled(s.T(), "EnableConsoleCapture", mock.Anything, mock.Anything)
	// Network capture should still proceed even if console fails.
	mockCDP.AssertCalled(s.T(), "EnableNetworkCapture", mock.Anything, mock.Anything)
}

func (s *BrowserHandlerSuite) TestEnsureBrowserCaptureNetworkCaptureError() {
	mockCDP := new(MockCDPClient)
	mockCDP.On("EnableConsoleCapture", mock.Anything, mock.Anything).Return(nil)
	mockCDP.On("EnableNetworkCapture", mock.Anything, mock.Anything).Return(errors.New("net cap fail"))

	s.srv.ensureBrowserCapture(context.Background(), "ch-1", mockCDP)

	mockCDP.AssertCalled(s.T(), "EnableConsoleCapture", mock.Anything, mock.Anything)
	mockCDP.AssertCalled(s.T(), "EnableNetworkCapture", mock.Anything, mock.Anything)
}

func (s *BrowserHandlerSuite) TestEnsureBrowserCaptureGoroutinesBodies() {
	// Use Run to send a message into the channel passed to EnableConsoleCapture
	// so the goroutine body (the for-range loop) executes.
	mockCDP := new(MockCDPClient)
	mockCDP.On("EnableConsoleCapture", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			ch := args.Get(1).(chan<- browser.ConsoleMessage)
			ch <- browser.ConsoleMessage{Level: "log", Text: "test-msg", Time: time.Now()}
		}).Return(nil)
	mockCDP.On("EnableNetworkCapture", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			ch := args.Get(1).(chan<- browser.NetworkRequest)
			ch <- browser.NetworkRequest{URL: "https://test.com", Method: "GET", Status: 200, StatusText: "OK", Time: time.Now()}
		}).Return(nil)

	s.srv.ensureBrowserCapture(context.Background(), "ch-goroutines", mockCDP)

	// Wait for goroutines to process the messages.
	require.Eventually(s.T(), func() bool {
		s.srv.browserCapturesMu.Lock()
		cs := s.srv.browserCaptures["ch-goroutines"]
		s.srv.browserCapturesMu.Unlock()
		if cs == nil {
			return false
		}
		cs.consoleMu.Lock()
		nConsole := len(cs.consoleMsgs)
		cs.consoleMu.Unlock()
		cs.networkMu.Lock()
		nNetwork := len(cs.networkReqs)
		cs.networkMu.Unlock()
		return nConsole == 1 && nNetwork == 1
	}, time.Second, 5*time.Millisecond, "goroutines did not process messages in time")

	s.srv.browserCapturesMu.Lock()
	cs := s.srv.browserCaptures["ch-goroutines"]
	s.srv.browserCapturesMu.Unlock()
	require.Equal(s.T(), "test-msg", cs.consoleMsgs[0].Text)
	require.Equal(s.T(), "https://test.com", cs.networkReqs[0].URL)
}

// --- getBrowserCDP: SetTargetID/TrackTab via handleBrowserAction ---

func (s *BrowserHandlerSuite) TestBrowserActionGetBrowserCDPWithNonEmptyTargetID() {
	mockCDP := new(MockCDPClient)
	s.browserMgr.On("GetActiveCDP", "ch-1").Return(nil)
	s.browserMgr.On("EnsureBrowser", mock.Anything, "ch-1", "").Return(nil)
	s.browserMgr.On("GetCDPEndpoint", "ch-1").Return("ws://127.0.0.1:9222")
	s.browserMgr.On("SetCDPForTarget", "ch-1", "my-target", mock.Anything).Return().Maybe()
	s.browserMgr.On("SetTargetID", "ch-1", "my-target").Return()
	s.browserMgr.On("TouchBrowser", "ch-1").Return().Maybe()

	mockCDP.On("TargetID").Return("my-target")
	mockCDP.On("EnableConsoleCapture", mock.Anything, mock.Anything).Return(nil)
	mockCDP.On("EnableNetworkCapture", mock.Anything, mock.Anything).Return(nil)
	mockCDP.On("GetPageInfo", mock.Anything).Return(&browser.PageInfo{URL: "https://x.com", Title: "X"}, nil)

	s.srv.browserCDPFactory = func(_ context.Context, _ string, _ *slog.Logger, _ ...browser.CDPOption) (browserCDPClient, error) {
		return mockCDP, nil
	}

	w := s.postBrowserAction(browserActionRequest{ChannelID: "ch-1", Action: "get_page_info"})
	require.Equal(s.T(), http.StatusOK, w.Code)
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Empty(s.T(), resp.Error)

	s.browserMgr.AssertCalled(s.T(), "SetTargetID", "ch-1", "my-target")
}

// --- dispatchBrowserAction: missing action types ---

func (s *BrowserHandlerSuite) TestBrowserActionReload() {
	mockCDP := new(MockCDPClient)
	s.setupActionMocks(mockCDP)
	mockCDP.On("Reload", mock.Anything).Return(nil)

	w := s.postBrowserAction(browserActionRequest{ChannelID: "ch-1", Action: "reload"})
	require.Equal(s.T(), http.StatusOK, w.Code)
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Empty(s.T(), resp.Error)
	require.Equal(s.T(), "Page reloaded", resp.Result)
}

func (s *BrowserHandlerSuite) TestBrowserActionReloadError() {
	mockCDP := new(MockCDPClient)
	s.setupActionMocks(mockCDP)
	mockCDP.On("Reload", mock.Anything).Return(errors.New("reload fail"))

	w := s.postBrowserAction(browserActionRequest{ChannelID: "ch-1", Action: "reload"})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Error, "reload failed")
}

func (s *BrowserHandlerSuite) TestBrowserActionGoBack() {
	mockCDP := new(MockCDPClient)
	s.setupActionMocks(mockCDP)
	mockCDP.On("GoBack", mock.Anything).Return(nil)

	w := s.postBrowserAction(browserActionRequest{ChannelID: "ch-1", Action: "go_back"})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Empty(s.T(), resp.Error)
	require.Equal(s.T(), "Navigated back", resp.Result)
}

func (s *BrowserHandlerSuite) TestBrowserActionGoBackError() {
	mockCDP := new(MockCDPClient)
	s.setupActionMocks(mockCDP)
	mockCDP.On("GoBack", mock.Anything).Return(errors.New("back fail"))

	w := s.postBrowserAction(browserActionRequest{ChannelID: "ch-1", Action: "go_back"})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Error, "go back failed")
}

func (s *BrowserHandlerSuite) TestBrowserActionGoForward() {
	mockCDP := new(MockCDPClient)
	s.setupActionMocks(mockCDP)
	mockCDP.On("GoForward", mock.Anything).Return(nil)

	w := s.postBrowserAction(browserActionRequest{ChannelID: "ch-1", Action: "go_forward"})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Empty(s.T(), resp.Error)
	require.Equal(s.T(), "Navigated forward", resp.Result)
}

func (s *BrowserHandlerSuite) TestBrowserActionGoForwardError() {
	mockCDP := new(MockCDPClient)
	s.setupActionMocks(mockCDP)
	mockCDP.On("GoForward", mock.Anything).Return(errors.New("fwd fail"))

	w := s.postBrowserAction(browserActionRequest{ChannelID: "ch-1", Action: "go_forward"})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Error, "go forward failed")
}

func (s *BrowserHandlerSuite) TestBrowserActionGetPageInfoError() {
	mockCDP := new(MockCDPClient)
	s.setupActionMocks(mockCDP)
	mockCDP.On("GetPageInfo", mock.Anything).Return((*browser.PageInfo)(nil), errors.New("info fail"))

	w := s.postBrowserAction(browserActionRequest{ChannelID: "ch-1", Action: "get_page_info"})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Error, "get page info failed")
}

func (s *BrowserHandlerSuite) TestBrowserActionNavigateGetPageInfoError() {
	mockCDP := new(MockCDPClient)
	s.setupActionMocks(mockCDP)
	mockCDP.On("Navigate", mock.Anything, "https://example.com").Return(nil)
	mockCDP.On("GetPageInfo", mock.Anything).Return((*browser.PageInfo)(nil), errors.New("page info fail"))

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "navigate",
		Params:    map[string]any{"url": "https://example.com"},
	})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Error, "get page info failed")
}

func (s *BrowserHandlerSuite) TestBrowserActionGetElementRefs() {
	mockCDP := new(MockCDPClient)
	s.setupActionMocks(mockCDP)
	refs := []browser.ElementRef{{RefID: "ref-1", Description: "button"}}
	mockCDP.On("GetElementRefs", mock.Anything).Return(refs, nil)

	w := s.postBrowserAction(browserActionRequest{ChannelID: "ch-1", Action: "get_element_refs"})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Empty(s.T(), resp.Error)
	require.Len(s.T(), resp.ElementRefs, 1)
}

func (s *BrowserHandlerSuite) TestBrowserActionGetElementRefsError() {
	mockCDP := new(MockCDPClient)
	s.setupActionMocks(mockCDP)
	mockCDP.On("GetElementRefs", mock.Anything).Return(([]browser.ElementRef)(nil), errors.New("refs fail"))

	w := s.postBrowserAction(browserActionRequest{ChannelID: "ch-1", Action: "get_element_refs"})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Error, "get element refs failed")
}

func (s *BrowserHandlerSuite) TestBrowserActionMouseClick() {
	mockCDP := new(MockCDPClient)
	s.setupActionMocks(mockCDP)
	// Uses paramFloat for x and y — exercises that code path.
	mockCDP.On("MouseClick", mock.Anything, float64(100), float64(200), "left", 1).Return(nil)

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "mouse_click",
		Params:    map[string]any{"x": float64(100), "y": float64(200)},
	})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Empty(s.T(), resp.Error)
	require.Contains(s.T(), resp.Result, "Clicked at")
}

func (s *BrowserHandlerSuite) TestBrowserActionMouseClickWithButton() {
	mockCDP := new(MockCDPClient)
	s.setupActionMocks(mockCDP)
	mockCDP.On("MouseClick", mock.Anything, float64(10), float64(20), "right", 2).Return(nil)

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "mouse_click",
		Params:    map[string]any{"x": float64(10), "y": float64(20), "button": "right", "click_count": float64(2)},
	})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Empty(s.T(), resp.Error)
}

func (s *BrowserHandlerSuite) TestBrowserActionMouseClickError() {
	mockCDP := new(MockCDPClient)
	s.setupActionMocks(mockCDP)
	mockCDP.On("MouseClick", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("click fail"))

	w := s.postBrowserAction(browserActionRequest{ChannelID: "ch-1", Action: "mouse_click"})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Error, "mouse click failed")
}

func (s *BrowserHandlerSuite) TestBrowserActionMouseMove() {
	mockCDP := new(MockCDPClient)
	s.setupActionMocks(mockCDP)
	mockCDP.On("MouseMove", mock.Anything, float64(50), float64(75)).Return(nil)

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "mouse_move",
		Params:    map[string]any{"x": float64(50), "y": float64(75)},
	})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Empty(s.T(), resp.Error)
	require.Contains(s.T(), resp.Result, "Moved mouse")
}

func (s *BrowserHandlerSuite) TestBrowserActionMouseMoveError() {
	mockCDP := new(MockCDPClient)
	s.setupActionMocks(mockCDP)
	mockCDP.On("MouseMove", mock.Anything, mock.Anything, mock.Anything).Return(errors.New("move fail"))

	w := s.postBrowserAction(browserActionRequest{ChannelID: "ch-1", Action: "mouse_move"})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Error, "mouse move failed")
}

func (s *BrowserHandlerSuite) TestBrowserActionMouseScroll() {
	mockCDP := new(MockCDPClient)
	s.setupActionMocks(mockCDP)
	mockCDP.On("MouseScroll", mock.Anything, float64(0), float64(0), float64(0), float64(-100)).Return(nil)

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "mouse_scroll",
		Params:    map[string]any{"delta_y": float64(-100)},
	})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Empty(s.T(), resp.Error)
	require.Contains(s.T(), resp.Result, "Scrolled at")
}

func (s *BrowserHandlerSuite) TestBrowserActionMouseScrollError() {
	mockCDP := new(MockCDPClient)
	s.setupActionMocks(mockCDP)
	mockCDP.On("MouseScroll", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("scroll fail"))

	w := s.postBrowserAction(browserActionRequest{ChannelID: "ch-1", Action: "mouse_scroll"})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Error, "mouse scroll failed")
}

func (s *BrowserHandlerSuite) TestBrowserActionMouseDown() {
	mockCDP := new(MockCDPClient)
	s.setupActionMocks(mockCDP)
	mockCDP.On("MouseDown", mock.Anything, float64(10), float64(20), "left").Return(nil)

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "mouse_down",
		Params:    map[string]any{"x": float64(10), "y": float64(20)},
	})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Empty(s.T(), resp.Error)
	require.Contains(s.T(), resp.Result, "Mouse down")
}

func (s *BrowserHandlerSuite) TestBrowserActionMouseDownWithButton() {
	mockCDP := new(MockCDPClient)
	s.setupActionMocks(mockCDP)
	mockCDP.On("MouseDown", mock.Anything, float64(5), float64(15), "right").Return(nil)

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "mouse_down",
		Params:    map[string]any{"x": float64(5), "y": float64(15), "button": "right"},
	})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Empty(s.T(), resp.Error)
}

func (s *BrowserHandlerSuite) TestBrowserActionMouseDownError() {
	mockCDP := new(MockCDPClient)
	s.setupActionMocks(mockCDP)
	mockCDP.On("MouseDown", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("down fail"))

	w := s.postBrowserAction(browserActionRequest{ChannelID: "ch-1", Action: "mouse_down"})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Error, "mouse down failed")
}

func (s *BrowserHandlerSuite) TestBrowserActionMouseUp() {
	mockCDP := new(MockCDPClient)
	s.setupActionMocks(mockCDP)
	mockCDP.On("MouseUp", mock.Anything, float64(30), float64(40), "left").Return(nil)

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "mouse_up",
		Params:    map[string]any{"x": float64(30), "y": float64(40)},
	})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Empty(s.T(), resp.Error)
	require.Contains(s.T(), resp.Result, "Mouse up")
}

func (s *BrowserHandlerSuite) TestBrowserActionMouseUpWithButton() {
	mockCDP := new(MockCDPClient)
	s.setupActionMocks(mockCDP)
	mockCDP.On("MouseUp", mock.Anything, float64(1), float64(2), "middle").Return(nil)

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "mouse_up",
		Params:    map[string]any{"x": float64(1), "y": float64(2), "button": "middle"},
	})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Empty(s.T(), resp.Error)
}

func (s *BrowserHandlerSuite) TestBrowserActionMouseUpError() {
	mockCDP := new(MockCDPClient)
	s.setupActionMocks(mockCDP)
	mockCDP.On("MouseUp", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("up fail"))

	w := s.postBrowserAction(browserActionRequest{ChannelID: "ch-1", Action: "mouse_up"})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Error, "mouse up failed")
}

func (s *BrowserHandlerSuite) TestBrowserActionKeyPress() {
	mockCDP := new(MockCDPClient)
	s.setupActionMocks(mockCDP)
	mockCDP.On("KeyPress", mock.Anything, "Enter").Return(nil)

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "key_press",
		Params:    map[string]any{"key": "Enter"},
	})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Empty(s.T(), resp.Error)
	require.Contains(s.T(), resp.Result, "Enter")
}

func (s *BrowserHandlerSuite) TestBrowserActionKeyPressError() {
	mockCDP := new(MockCDPClient)
	s.setupActionMocks(mockCDP)
	mockCDP.On("KeyPress", mock.Anything, mock.Anything).Return(errors.New("key fail"))

	w := s.postBrowserAction(browserActionRequest{ChannelID: "ch-1", Action: "key_press"})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Error, "key press failed")
}

func (s *BrowserHandlerSuite) TestBrowserActionTypeText() {
	mockCDP := new(MockCDPClient)
	s.setupActionMocks(mockCDP)
	mockCDP.On("TypeText", mock.Anything, "hello").Return(nil)

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "type_text",
		Params:    map[string]any{"text": "hello"},
	})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Empty(s.T(), resp.Error)
	require.Contains(s.T(), resp.Result, "hello")
}

func (s *BrowserHandlerSuite) TestBrowserActionTypeTextError() {
	mockCDP := new(MockCDPClient)
	s.setupActionMocks(mockCDP)
	mockCDP.On("TypeText", mock.Anything, mock.Anything).Return(errors.New("type fail"))

	w := s.postBrowserAction(browserActionRequest{ChannelID: "ch-1", Action: "type_text"})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Error, "type text failed")
}

func (s *BrowserHandlerSuite) TestBrowserActionClickRef() {
	mockCDP := new(MockCDPClient)
	s.setupActionMocks(mockCDP)
	mockCDP.On("ClickRef", mock.Anything, mock.Anything, 0).Return(nil)

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "click_ref",
		Params: map[string]any{
			"refs": []any{
				map[string]any{"BackendNodeID": float64(42), "Description": "button"},
			},
			"ref_index": float64(0),
		},
	})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Empty(s.T(), resp.Error)
	require.Contains(s.T(), resp.Result, "Clicked ref")
}

func (s *BrowserHandlerSuite) TestBrowserActionClickRefError() {
	mockCDP := new(MockCDPClient)
	s.setupActionMocks(mockCDP)
	mockCDP.On("ClickRef", mock.Anything, mock.Anything, mock.Anything).Return(errors.New("click ref fail"))

	w := s.postBrowserAction(browserActionRequest{ChannelID: "ch-1", Action: "click_ref"})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Error, "click ref failed")
}

func (s *BrowserHandlerSuite) TestBrowserActionScreenshotError() {
	mockCDP := new(MockCDPClient)
	s.setupActionMocks(mockCDP)
	mockCDP.On("Screenshot", mock.Anything).Return(([]byte)(nil), errors.New("screenshot fail"))

	w := s.postBrowserAction(browserActionRequest{ChannelID: "ch-1", Action: "screenshot"})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Error, "screenshot failed")
}

func (s *BrowserHandlerSuite) TestBrowserActionScreenshotFileBased() {
	dir := s.T().TempDir()
	s.srv.screenshotDir = dir

	mockCDP := new(MockCDPClient)
	s.setupActionMocks(mockCDP)
	mockCDP.On("Screenshot", mock.Anything).Return([]byte{0x89, 0x50, 0x4E, 0x47}, nil)

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "screenshot",
	})

	require.Equal(s.T(), http.StatusOK, w.Code)
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Empty(s.T(), resp.Error)
	require.Empty(s.T(), resp.Image)
	require.NotEmpty(s.T(), resp.ScreenshotPath)
	require.Contains(s.T(), resp.ScreenshotPath, dir)
}

func (s *BrowserHandlerSuite) TestBrowserActionScreenshotFileWriteError() {
	// Use a non-existent directory so WriteFile fails.
	s.srv.screenshotDir = "/nonexistent/dir"

	mockCDP := new(MockCDPClient)
	s.setupActionMocks(mockCDP)
	mockCDP.On("Screenshot", mock.Anything).Return([]byte{0x89, 0x50, 0x4E, 0x47}, nil)

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "screenshot",
	})

	require.Equal(s.T(), http.StatusOK, w.Code)
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Error, "writing screenshot file")
}

func (s *BrowserHandlerSuite) TestBrowserActionEvaluateJS() {
	mockCDP := new(MockCDPClient)
	s.setupActionMocks(mockCDP)
	mockCDP.On("EvaluateJS", mock.Anything, "document.title").Return("My Title", nil)

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "evaluate_js",
		Params:    map[string]any{"expression": "document.title"},
	})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Empty(s.T(), resp.Error)
	require.Equal(s.T(), "My Title", resp.Result)
}

func (s *BrowserHandlerSuite) TestBrowserActionEvaluateJSError() {
	mockCDP := new(MockCDPClient)
	s.setupActionMocks(mockCDP)
	mockCDP.On("EvaluateJS", mock.Anything, mock.Anything).Return("", errors.New("js fail"))

	w := s.postBrowserAction(browserActionRequest{ChannelID: "ch-1", Action: "evaluate_js"})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Error, "evaluate JS failed")
}

func (s *BrowserHandlerSuite) TestBrowserActionListTabsError() {
	mockCDP := new(MockCDPClient)
	s.setupActionMocks(mockCDP)
	mockCDP.On("ListTabs", mock.Anything).Return(([]browser.TabInfo)(nil), errors.New("list fail"))

	w := s.postBrowserAction(browserActionRequest{ChannelID: "ch-1", Action: "list_tabs"})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Error, "list tabs failed")
}

func (s *BrowserHandlerSuite) TestBrowserActionNewTabWithURL() {
	mockCDP := new(MockCDPClient)
	s.setupActionMocks(mockCDP)
	mockCDP.On("NewTab", mock.Anything, "https://example.com").Return("t-ex", nil)
	s.browserMgr.On("NotifyTabAdded", "ch-1", mock.Anything).Return().Maybe()
	s.browserMgr.On("NotifyTargetSwitch", "ch-1", "t-ex").Return().Maybe()

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "new_tab",
		Params:    map[string]any{"url": "https://example.com"},
	})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Empty(s.T(), resp.Error)
	require.Contains(s.T(), resp.Result, "t-ex")
}

func (s *BrowserHandlerSuite) TestBrowserActionNewTabError() {
	mockCDP := new(MockCDPClient)
	s.setupActionMocks(mockCDP)
	mockCDP.On("NewTab", mock.Anything, mock.Anything).Return("", errors.New("new tab fail"))

	w := s.postBrowserAction(browserActionRequest{ChannelID: "ch-1", Action: "new_tab"})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Error, "new tab failed")
}

func (s *BrowserHandlerSuite) TestBrowserActionSwitchTabMissingTargetID() {
	mockCDP := new(MockCDPClient)
	s.setupActionMocks(mockCDP)

	w := s.postBrowserAction(browserActionRequest{ChannelID: "ch-1", Action: "switch_tab"})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Error, "target_id required")
}

func (s *BrowserHandlerSuite) TestBrowserActionSwitchTab() {
	mockCDP := new(MockCDPClient)
	s.setupActionMocks(mockCDP)
	mockCDP.On("SwitchTarget", "t-1").Return(nil)
	s.browserMgr.On("NotifyTargetSwitch", "ch-1", "t-1").Return().Maybe()

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "switch_tab",
		Params:    map[string]any{"target_id": "t-1"},
	})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Empty(s.T(), resp.Error)
	require.Contains(s.T(), resp.Result, "t-1")
}

func (s *BrowserHandlerSuite) TestBrowserActionSwitchTabError() {
	mockCDP := new(MockCDPClient)
	s.setupActionMocks(mockCDP)
	mockCDP.On("SwitchTarget", "t-bad").Return(errors.New("switch fail"))
	s.browserMgr.On("NotifyTargetSwitch", "ch-1", mock.Anything).Return().Maybe()

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "switch_tab",
		Params:    map[string]any{"target_id": "t-bad"},
	})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Error, "switch tab failed")
}

func (s *BrowserHandlerSuite) TestBrowserActionCloseTabMissingTargetID() {
	mockCDP := new(MockCDPClient)
	s.setupActionMocks(mockCDP)

	w := s.postBrowserAction(browserActionRequest{ChannelID: "ch-1", Action: "close_tab"})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Error, "target_id required")
}

func (s *BrowserHandlerSuite) TestBrowserActionCloseTabError() {
	mockCDP := new(MockCDPClient)
	s.setupActionMocks(mockCDP)
	mockCDP.On("CloseTab", mock.Anything, "t-bad").Return(errors.New("close fail"))
	s.browserMgr.On("NotifyTabRemoved", "ch-1", mock.Anything).Return().Maybe()

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "close_tab",
		Params:    map[string]any{"target_id": "t-bad"},
	})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Error, "close tab failed")
}

func (s *BrowserHandlerSuite) TestBrowserActionResizeWindow() {
	mockCDP := new(MockCDPClient)
	s.setupActionMocks(mockCDP)
	mockCDP.On("ResizeWindow", mock.Anything, 1280, 720).Return(nil)

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "resize_window",
		Params:    map[string]any{"width": float64(1280), "height": float64(720)},
	})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Empty(s.T(), resp.Error)
	require.Contains(s.T(), resp.Result, "1280x720")
}

func (s *BrowserHandlerSuite) TestBrowserActionResizeWindowError() {
	mockCDP := new(MockCDPClient)
	s.setupActionMocks(mockCDP)
	mockCDP.On("ResizeWindow", mock.Anything, mock.Anything, mock.Anything).Return(errors.New("resize fail"))

	w := s.postBrowserAction(browserActionRequest{ChannelID: "ch-1", Action: "resize_window"})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Error, "resize window failed")
}

func (s *BrowserHandlerSuite) TestBrowserActionScrollIntoView() {
	mockCDP := new(MockCDPClient)
	s.setupActionMocks(mockCDP)
	mockCDP.On("ScrollIntoView", mock.Anything, cdp.BackendNodeID(42)).Return(nil)

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "scroll_into_view",
		Params:    map[string]any{"backend_node_id": float64(42)},
	})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Empty(s.T(), resp.Error)
	require.Contains(s.T(), resp.Result, "Scrolled element")
}

func (s *BrowserHandlerSuite) TestBrowserActionScrollIntoViewError() {
	mockCDP := new(MockCDPClient)
	s.setupActionMocks(mockCDP)
	mockCDP.On("ScrollIntoView", mock.Anything, mock.Anything).Return(errors.New("scroll fail"))

	w := s.postBrowserAction(browserActionRequest{ChannelID: "ch-1", Action: "scroll_into_view"})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Error, "scroll into view failed")
}

// --- readConsoleMessages additional paths ---

func (s *BrowserHandlerSuite) TestReadConsoleMessagesNilCapture() {
	// No capture state exists for this channel.
	result := s.srv.readConsoleMessages("no-such-channel", nil)
	require.Equal(s.T(), "No console messages", result.Result)
}

func (s *BrowserHandlerSuite) TestReadConsoleMessagesInvalidRegex() {
	s.srv.browserCapturesMu.Lock()
	s.srv.browserCaptures = map[string]*browserCaptureState{
		"ch-1": {started: true, consoleMsgs: []browser.ConsoleMessage{
			{Level: "log", Text: "hello", Time: time.Now()},
		}},
	}
	s.srv.browserCapturesMu.Unlock()

	result := s.srv.readConsoleMessages("ch-1", map[string]any{"pattern": "["})
	require.Contains(s.T(), result.Error, "invalid regex pattern")
}

func (s *BrowserHandlerSuite) TestReadConsoleMessagesOnlyErrors() {
	s.srv.browserCapturesMu.Lock()
	s.srv.browserCaptures = map[string]*browserCaptureState{
		"ch-1": {started: true, consoleMsgs: []browser.ConsoleMessage{
			{Level: "log", Text: "info msg", Time: time.Now()},
			{Level: "error", Text: "error msg", Time: time.Now()},
		}},
	}
	s.srv.browserCapturesMu.Unlock()

	result := s.srv.readConsoleMessages("ch-1", map[string]any{"only_errors": true})
	require.Empty(s.T(), result.Error)
	require.Contains(s.T(), result.Result, "error msg")
	require.NotContains(s.T(), result.Result, "info msg")
}

func (s *BrowserHandlerSuite) TestReadConsoleMessagesClear() {
	s.srv.browserCapturesMu.Lock()
	cs := &browserCaptureState{started: true, consoleMsgs: []browser.ConsoleMessage{
		{Level: "log", Text: "msg1", Time: time.Now()},
	}}
	s.srv.browserCaptures = map[string]*browserCaptureState{"ch-1": cs}
	s.srv.browserCapturesMu.Unlock()

	result := s.srv.readConsoleMessages("ch-1", map[string]any{"clear": true})
	require.Contains(s.T(), result.Result, "msg1")

	// Second read should be empty after clear.
	result2 := s.srv.readConsoleMessages("ch-1", nil)
	require.Equal(s.T(), "No console messages", result2.Result)
}

func (s *BrowserHandlerSuite) TestReadConsoleMessagesLimitTruncates() {
	msgs := make([]browser.ConsoleMessage, 10)
	for i := range msgs {
		msgs[i] = browser.ConsoleMessage{Level: "log", Text: "msg", Time: time.Now()}
	}
	s.srv.browserCapturesMu.Lock()
	s.srv.browserCaptures = map[string]*browserCaptureState{
		"ch-1": {started: true, consoleMsgs: msgs},
	}
	s.srv.browserCapturesMu.Unlock()

	// Limit to 3 — should return only last 3.
	result := s.srv.readConsoleMessages("ch-1", map[string]any{"limit": float64(3)})
	require.Empty(s.T(), result.Error)
	require.Contains(s.T(), result.Result, "3 console message(s)")
}

func (s *BrowserHandlerSuite) TestReadConsoleMessagesPatternFilter() {
	s.srv.browserCapturesMu.Lock()
	s.srv.browserCaptures = map[string]*browserCaptureState{
		"ch-1": {started: true, consoleMsgs: []browser.ConsoleMessage{
			{Level: "log", Text: "match this", Time: time.Now()},
			{Level: "log", Text: "skip this", Time: time.Now()},
		}},
	}
	s.srv.browserCapturesMu.Unlock()

	result := s.srv.readConsoleMessages("ch-1", map[string]any{"pattern": "match"})
	require.Empty(s.T(), result.Error)
	require.Contains(s.T(), result.Result, "match this")
	require.NotContains(s.T(), result.Result, "skip this")
}

func (s *BrowserHandlerSuite) TestReadConsoleMessagesEmpty() {
	s.srv.browserCapturesMu.Lock()
	s.srv.browserCaptures = map[string]*browserCaptureState{
		"ch-1": {started: true},
	}
	s.srv.browserCapturesMu.Unlock()

	result := s.srv.readConsoleMessages("ch-1", nil)
	require.Equal(s.T(), "No console messages", result.Result)
}

// --- readNetworkRequests additional paths ---

func (s *BrowserHandlerSuite) TestReadNetworkRequestsNilCapture() {
	result := s.srv.readNetworkRequests("no-such-channel", nil)
	require.Equal(s.T(), "No network requests", result.Result)
}

func (s *BrowserHandlerSuite) TestReadNetworkRequestsInvalidRegex() {
	s.srv.browserCapturesMu.Lock()
	s.srv.browserCaptures = map[string]*browserCaptureState{
		"ch-1": {started: true, networkReqs: []browser.NetworkRequest{
			{URL: "https://api.example.com", Method: "GET", Status: 200, StatusText: "OK", Time: time.Now()},
		}},
	}
	s.srv.browserCapturesMu.Unlock()

	result := s.srv.readNetworkRequests("ch-1", map[string]any{"pattern": "["})
	require.Contains(s.T(), result.Error, "invalid regex pattern")
}

func (s *BrowserHandlerSuite) TestReadNetworkRequestsClear() {
	s.srv.browserCapturesMu.Lock()
	cs := &browserCaptureState{started: true, networkReqs: []browser.NetworkRequest{
		{URL: "https://api.example.com", Method: "GET", Status: 200, StatusText: "OK", Time: time.Now()},
	}}
	s.srv.browserCaptures = map[string]*browserCaptureState{"ch-1": cs}
	s.srv.browserCapturesMu.Unlock()

	result := s.srv.readNetworkRequests("ch-1", map[string]any{"clear": true})
	require.Contains(s.T(), result.Result, "api.example.com")

	result2 := s.srv.readNetworkRequests("ch-1", nil)
	require.Equal(s.T(), "No network requests", result2.Result)
}

func (s *BrowserHandlerSuite) TestReadNetworkRequestsLimitTruncates() {
	reqs := make([]browser.NetworkRequest, 10)
	for i := range reqs {
		reqs[i] = browser.NetworkRequest{URL: "https://api.example.com", Method: "GET", Status: 200, StatusText: "OK", Time: time.Now()}
	}
	s.srv.browserCapturesMu.Lock()
	s.srv.browserCaptures = map[string]*browserCaptureState{
		"ch-1": {started: true, networkReqs: reqs},
	}
	s.srv.browserCapturesMu.Unlock()

	result := s.srv.readNetworkRequests("ch-1", map[string]any{"limit": float64(2)})
	require.Empty(s.T(), result.Error)
	require.Contains(s.T(), result.Result, "2 network request(s)")
}

func (s *BrowserHandlerSuite) TestReadNetworkRequestsPatternFilter() {
	s.srv.browserCapturesMu.Lock()
	s.srv.browserCaptures = map[string]*browserCaptureState{
		"ch-1": {started: true, networkReqs: []browser.NetworkRequest{
			{URL: "https://api.match.com", Method: "GET", Status: 200, StatusText: "OK", Time: time.Now()},
			{URL: "https://skip.com", Method: "GET", Status: 200, StatusText: "OK", Time: time.Now()},
		}},
	}
	s.srv.browserCapturesMu.Unlock()

	result := s.srv.readNetworkRequests("ch-1", map[string]any{"pattern": "match"})
	require.Empty(s.T(), result.Error)
	require.Contains(s.T(), result.Result, "api.match.com")
	require.NotContains(s.T(), result.Result, "skip.com")
}

func (s *BrowserHandlerSuite) TestReadNetworkRequestsEmpty() {
	s.srv.browserCapturesMu.Lock()
	s.srv.browserCaptures = map[string]*browserCaptureState{
		"ch-1": {started: true},
	}
	s.srv.browserCapturesMu.Unlock()

	result := s.srv.readNetworkRequests("ch-1", nil)
	require.Equal(s.T(), "No network requests", result.Result)
}

// --- paramBool false branch ---

func (s *BrowserHandlerSuite) TestParamBoolFalseBranch() {
	// When key is absent, paramBool returns false.
	result := paramBool(map[string]any{}, "missing_key")
	require.False(s.T(), result)

	// When value is not bool type, paramBool returns false.
	result = paramBool(map[string]any{"k": "not-a-bool"}, "k")
	require.False(s.T(), result)

	// When value is true bool, paramBool returns true.
	result = paramBool(map[string]any{"k": true}, "k")
	require.True(s.T(), result)
}

// --- paramFloat coverage ---

func (s *BrowserHandlerSuite) TestParamFloat() {
	require.Equal(s.T(), float64(3.14), paramFloat(map[string]any{"k": float64(3.14)}, "k"))
	require.Equal(s.T(), float64(0), paramFloat(map[string]any{}, "k"))
	require.Equal(s.T(), float64(0), paramFloat(map[string]any{"k": "not-float"}, "k"))
}

// --- sendJSON: broken pipe suppression ---

func (s *BrowserHandlerSuite) TestSendJSONBrokenPipeSuppressed() {
	// Create a WS pair where the server-side conn's write returns "broken pipe".
	connReady := make(chan *websocket.Conn, 1)
	tsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		connReady <- conn
	}))
	defer tsSrv.Close()

	wsURL := "ws" + strings.TrimPrefix(tsSrv.URL, "http") + "/"
	clientWS, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(s.T(), err)

	serverConn := <-connReady

	// Close the client so the server write gets broken pipe.
	clientWS.Close()
	time.Sleep(10 * time.Millisecond)

	bc := &browserWSConn{
		conn:   serverConn,
		logger: slog.Default(),
		stopCh: make(chan struct{}),
	}
	// sendJSON on broken pipe should not panic and should suppress the error log.
	bc.sendJSON(browserWSResponse{Type: bwsRespError, Message: "test"})

	serverConn.Close()
}

// --- sendJSON: non-suppressed write error (e.g. timeout) ---

func (s *BrowserHandlerSuite) TestSendJSONNonSuppressedWriteError() {
	// Create a WS pair and set a write deadline in the past to trigger a timeout error.
	connReady := make(chan *websocket.Conn, 1)
	tsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		connReady <- conn
	}))
	defer tsSrv.Close()

	wsURL := "ws" + strings.TrimPrefix(tsSrv.URL, "http") + "/"
	clientWS, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(s.T(), err)
	defer clientWS.Close()

	serverConn := <-connReady

	// Set write deadline in the past to force a timeout error on write.
	require.NoError(s.T(), serverConn.SetWriteDeadline(time.Now().Add(-time.Second)))

	bc := &browserWSConn{
		conn:   serverConn,
		logger: slog.Default(),
		stopCh: make(chan struct{}),
	}
	// sendJSON should log the error (not suppress it — "timeout" does not match "broken pipe"/"use of closed").
	bc.sendJSON(browserWSResponse{Type: bwsRespError, Message: "test"})

	serverConn.Close()
}

// --- restartScreencastForTarget: cdpFactory error ---

func (s *BrowserHandlerSuite) TestRestartScreencastForTargetCDPFactoryError() {
	s.browserMgr.On("RemoveCDPForTarget", "ch-1", "t-fail").Return(nil)
	s.browserMgr.On("GetCDPEndpoint", "ch-1").Return("ws://127.0.0.1:9222")

	connReady := make(chan *websocket.Conn, 1)
	tsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		connReady <- conn
	}))
	defer tsSrv.Close()

	wsURL := "ws" + strings.TrimPrefix(tsSrv.URL, "http") + "/"
	clientWS, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(s.T(), err)
	defer clientWS.Close()

	serverConn := <-connReady

	bc := &browserWSConn{
		conn:      serverConn,
		bMgr:      s.browserMgr,
		logger:    slog.Default(),
		stopCh:    make(chan struct{}),
		channelID: "ch-1",
		cdpFactory: func(_ context.Context, _ string, _ *slog.Logger, _ ...browser.CDPOption) (browserCDPClient, error) {
			return nil, errors.New("cdp factory boom")
		},
	}

	bc.restartScreencastForTarget(context.Background(), nil, "t-fail")

	// Read the error response from the client side.
	require.NoError(s.T(), clientWS.SetReadDeadline(time.Now().Add(2*time.Second)))
	var resp browserWSResponse
	require.NoError(s.T(), clientWS.ReadJSON(&resp))
	require.Equal(s.T(), bwsRespError, resp.Type)
	require.Contains(s.T(), resp.Message, "switch target failed")
}

// --- handleStart: ListTabs error path ---

func (s *BrowserHandlerSuite) TestStartListTabsError() {
	mockCDP := new(MockCDPClient)
	s.srv.browserCDPFactory = func(_ context.Context, _ string, _ *slog.Logger, _ ...browser.CDPOption) (browserCDPClient, error) {
		return mockCDP, nil
	}

	s.browserMgr.On("EnsureBrowser", mock.Anything, "ch-1", "").Return(nil)
	s.browserMgr.On("GetActiveCDP", "ch-1").Return(nil)
	s.browserMgr.On("GetCDPEndpoint", "ch-1").Return("ws://127.0.0.1:9222")
	s.browserMgr.On("SetCDPForTarget", "ch-1", mock.Anything, mock.Anything).Return().Maybe()
	s.browserMgr.On("SetTargetID", "ch-1", mock.Anything).Return().Maybe()

	mockCDP.On("TargetID").Return("").Maybe()
	// ListTabs returns an error — should not prevent startup.
	mockCDP.On("ListTabs", mock.Anything).Return([]browser.TabInfo(nil), errors.New("list tabs fail"))
	mockCDP.On("Close").Return().Maybe()
	mockCDP.On("StopScreencast").Return().Maybe()

	ws, ts := s.dialBrowserWS()
	defer ts.Close()
	defer ws.Close()

	require.NoError(s.T(), ws.WriteJSON(browserWSMessage{Type: bwsMsgStart, ChannelID: "ch-1"}))
	resp := s.readResp(ws)
	require.Equal(s.T(), bwsRespStarted, resp.Type)
}

// --- handleStart: cached CDP reuse starts watchMCPTabChanges goroutine ---

func (s *BrowserHandlerSuite) TestStartCachedCDPReuseStartsWatchMCPTabChanges() {
	switchCh := make(chan string, 1)
	mockCDP := new(MockCDPClient)
	mockCDP.On("ResetScreencast").Return().Maybe()
	mockCDP.On("StopScreencast").Return().Maybe()
	mockCDP.On("Close").Return().Maybe()
	mockCDP.On("TargetID").Return("cached-target").Maybe()

	s.browserMgr.On("EnsureBrowser", mock.Anything, "ch-1", "").Return(nil)
	s.browserMgr.On("GetActiveCDP", "ch-1").Return(mockCDP)

	// Override TargetSwitchCh to return a real channel.
	s.browserMgr.ExpectedCalls = filterCalls(s.browserMgr.ExpectedCalls, "TargetSwitchCh")
	s.browserMgr.On("TargetSwitchCh", "ch-1").Return((<-chan string)(switchCh))

	ws, ts := s.dialBrowserWS()
	defer ts.Close()
	defer ws.Close()

	require.NoError(s.T(), ws.WriteJSON(browserWSMessage{Type: bwsMsgStart, ChannelID: "ch-1"}))
	resp := s.readResp(ws)
	require.Equal(s.T(), bwsRespStarted, resp.Type)

	// Trigger a switch on the channel — it sends a target ID into switchCh.
	// The watcher goroutine picks it up and calls restartScreencastForTarget.
	// Since cdp is non-nil, the goroutine won't exit (it calls restartScreencast).
	// Just verify the goroutine is alive by sending and having it process the message.
	// We need cdpFactory for the restartScreencast call.
	newMockCDP := new(MockCDPClient)
	newMockCDP.On("ResetScreencast").Return().Maybe()
	newMockCDP.On("StartScreencast", 60, 1920, 1080).Return((<-chan []byte)(make(chan []byte)))
	newMockCDP.On("ListTabs", mock.Anything).Return([]browser.TabInfo{}, nil).Maybe()
	newMockCDP.On("EvaluateJS", mock.Anything, mock.Anything).Return("", nil).Maybe()
	newMockCDP.On("Close").Return().Maybe()
	newMockCDP.On("StopScreencast").Return().Maybe()

	s.srv.browserCDPFactory = func(_ context.Context, _ string, _ *slog.Logger, _ ...browser.CDPOption) (browserCDPClient, error) {
		return newMockCDP, nil
	}
	s.browserMgr.On("RemoveCDPForTarget", "ch-1", "t-switch").Return(nil)
	s.browserMgr.On("GetCDPEndpoint", "ch-1").Return("ws://127.0.0.1:9222")
	s.browserMgr.On("SetCDPForTarget", "ch-1", "t-switch", newMockCDP).Return()
	s.browserMgr.On("SetTargetID", "ch-1", "t-switch").Return()

	switchCh <- "t-switch"

	// Read the tab_switched response — proves the watcher goroutine was started.
	require.NoError(s.T(), ws.SetReadDeadline(time.Now().Add(2*time.Second)))
	resp = s.readResp(ws)
	require.Equal(s.T(), bwsRespTabSwitched, resp.Type)
	require.Equal(s.T(), "t-switch", resp.TargetID)
}

// --- dispatchBrowserAction: close_tab last tab, about:blank creation ---

func (s *BrowserHandlerSuite) TestBrowserActionCloseTabLastTabCreatesAboutBlank() {
	mockCDP := new(MockCDPClient)
	s.setupActionMocks(mockCDP)

	// NextTabID returns "" — last tab.
	s.browserMgr.ExpectedCalls = filterCalls(s.browserMgr.ExpectedCalls, "NextTabID")
	s.browserMgr.On("NextTabID", "ch-1", "t-last").Return("")

	mockCDP.On("CloseTab", mock.Anything, "t-last").Return(nil)
	s.browserMgr.On("NotifyTabRemoved", "ch-1", "t-last").Return().Maybe()

	// Set up a fake Chrome /json/new endpoint.
	chromeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"t-new-blank"}`))
	}))
	defer chromeSrv.Close()

	// GetCDPEndpoint returns ws://host:port — we replace ws:// with http:// for the /json/new URL.
	chromeWSURL := "ws://" + strings.TrimPrefix(chromeSrv.URL, "http://")
	s.browserMgr.ExpectedCalls = filterCalls(s.browserMgr.ExpectedCalls, "GetCDPEndpoint")
	s.browserMgr.On("GetCDPEndpoint", "ch-1").Return(chromeWSURL)
	s.browserMgr.On("NotifyTabAdded", "ch-1", mock.Anything).Return().Maybe()
	s.browserMgr.On("NotifyTargetSwitch", "ch-1", "t-new-blank").Return().Maybe()

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "close_tab",
		Params:    map[string]any{"target_id": "t-last"},
	})

	require.Equal(s.T(), http.StatusOK, w.Code)
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Empty(s.T(), resp.Error)
	require.Contains(s.T(), resp.Result, "t-last")

	// Verify TrackTab was called with the new blank tab's target ID.
	time.Sleep(20 * time.Millisecond)
	s.browserMgr.AssertCalled(s.T(), "TrackTab", "ch-1", "t-new-blank")
}

// --- handleStart: ListTabs returns tabs that get tracked ---

func (s *BrowserHandlerSuite) TestStartListTabsTracksTabs() {
	mockCDP := new(MockCDPClient)
	s.srv.browserCDPFactory = func(_ context.Context, _ string, _ *slog.Logger, _ ...browser.CDPOption) (browserCDPClient, error) {
		return mockCDP, nil
	}

	s.browserMgr.On("EnsureBrowser", mock.Anything, "ch-1", "").Return(nil)
	s.browserMgr.On("GetActiveCDP", "ch-1").Return(nil)
	s.browserMgr.On("GetCDPEndpoint", "ch-1").Return("ws://127.0.0.1:9222")
	s.browserMgr.On("SetCDPForTarget", "ch-1", mock.Anything, mock.Anything).Return().Maybe()
	s.browserMgr.On("SetTargetID", "ch-1", mock.Anything).Return().Maybe()

	mockCDP.On("TargetID").Return("").Maybe()
	// ListTabs returns actual tabs that should be tracked.
	mockCDP.On("ListTabs", mock.Anything).Return([]browser.TabInfo{
		{TargetID: "t-a", URL: "https://a.com", Title: "A"},
		{TargetID: "t-b", URL: "https://b.com", Title: "B"},
	}, nil)
	mockCDP.On("Close").Return().Maybe()
	mockCDP.On("StopScreencast").Return().Maybe()

	ws, ts := s.dialBrowserWS()
	defer ts.Close()
	defer ws.Close()

	require.NoError(s.T(), ws.WriteJSON(browserWSMessage{Type: bwsMsgStart, ChannelID: "ch-1"}))
	resp := s.readResp(ws)
	require.Equal(s.T(), bwsRespStarted, resp.Type)

	time.Sleep(20 * time.Millisecond)
	s.browserMgr.AssertCalled(s.T(), "TrackTab", "ch-1", "t-a")
	s.browserMgr.AssertCalled(s.T(), "TrackTab", "ch-1", "t-b")
}

// --- restartScreencastForTarget: with existing screencastStopCh and activate URL ---

func (s *BrowserHandlerSuite) TestRestartScreencastForTargetWithExistingStopCh() {
	newMockCDP := new(MockCDPClient)
	newMockCDP.On("ResetScreencast").Return().Maybe()
	newMockCDP.On("StartScreencast", 60, 1920, 1080).Return((<-chan []byte)(make(chan []byte)))
	newMockCDP.On("ListTabs", mock.Anything).Return([]browser.TabInfo{}, nil).Maybe()
	newMockCDP.On("EvaluateJS", mock.Anything, mock.Anything).Return("", nil).Maybe()

	// Set up a fake Chrome HTTP server so the activate URL succeeds.
	chromeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer chromeSrv.Close()
	chromeWSURL := "ws://" + strings.TrimPrefix(chromeSrv.URL, "http://")

	s.browserMgr.On("RemoveCDPForTarget", "ch-1", "t-switch").Return(nil)
	s.browserMgr.On("GetCDPEndpoint", "ch-1").Return(chromeWSURL)
	s.browserMgr.On("SetCDPForTarget", "ch-1", "t-switch", newMockCDP).Return()
	s.browserMgr.On("SetTargetID", "ch-1", "t-switch").Return()

	connReady := make(chan *websocket.Conn, 1)
	tsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		connReady <- conn
	}))
	defer tsSrv.Close()

	wsURL := "ws" + strings.TrimPrefix(tsSrv.URL, "http") + "/"
	clientWS, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(s.T(), err)
	defer clientWS.Close()

	serverConn := <-connReady

	existingStopCh := make(chan struct{})
	bc := &browserWSConn{
		conn:             serverConn,
		bMgr:             s.browserMgr,
		logger:           slog.Default(),
		stopCh:           make(chan struct{}),
		channelID:        "ch-1",
		screencastStopCh: existingStopCh,
		cdpFactory: func(_ context.Context, _ string, _ *slog.Logger, _ ...browser.CDPOption) (browserCDPClient, error) {
			return newMockCDP, nil
		},
	}

	bc.restartScreencastForTarget(context.Background(), nil, "t-switch")

	// Verify old screencastStopCh was closed.
	select {
	case <-existingStopCh:
		// OK
	default:
		s.T().Fatal("existing screencastStopCh should have been closed")
	}

	// Read the tab_switched response.
	require.NoError(s.T(), clientWS.SetReadDeadline(time.Now().Add(2*time.Second)))
	var resp browserWSResponse
	require.NoError(s.T(), clientWS.ReadJSON(&resp))
	require.Equal(s.T(), bwsRespTabSwitched, resp.Type)
}

// --- dispatchBrowserAction: close_tab with nextTab (switch to adjacent) ---

func (s *BrowserHandlerSuite) TestBrowserActionCloseTabWithNextTab() {
	mockCDP := new(MockCDPClient)
	s.setupActionMocks(mockCDP)

	// NextTabID returns a valid tab.
	s.browserMgr.ExpectedCalls = filterCalls(s.browserMgr.ExpectedCalls, "NextTabID")
	s.browserMgr.On("NextTabID", "ch-1", "t-close").Return("t-next")

	mockCDP.On("CloseTab", mock.Anything, "t-close").Return(nil)
	s.browserMgr.On("NotifyTabRemoved", "ch-1", "t-close").Return().Maybe()
	s.browserMgr.On("NotifyTargetSwitch", "ch-1", "t-next").Return().Maybe()

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "close_tab",
		Params:    map[string]any{"target_id": "t-close"},
	})

	require.Equal(s.T(), http.StatusOK, w.Code)
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Empty(s.T(), resp.Error)
	require.Contains(s.T(), resp.Result, "t-close")

	s.browserMgr.AssertCalled(s.T(), "NotifyTargetSwitch", "ch-1", "t-next")
}

// --- getBrowserCDP: retry delay path ---

func (s *BrowserHandlerSuite) TestBrowserActionGetBrowserCDPRetryDelaySuccess() {
	s.srv.browserCDPRetries = 3
	s.srv.browserCDPDelay = time.Millisecond // very short delay so test doesn't slow down

	mockCDP := new(MockCDPClient)
	attempt := 0
	s.srv.browserCDPFactory = func(_ context.Context, _ string, _ *slog.Logger, _ ...browser.CDPOption) (browserCDPClient, error) {
		attempt++
		if attempt == 1 {
			return nil, errors.New("not ready")
		}
		return mockCDP, nil
	}

	s.browserMgr.On("GetActiveCDP", "ch-1").Return(nil)
	s.browserMgr.On("EnsureBrowser", mock.Anything, "ch-1", "").Return(nil)
	s.browserMgr.On("GetCDPEndpoint", "ch-1").Return("ws://127.0.0.1:9222")
	s.browserMgr.On("SetCDPForTarget", "ch-1", mock.Anything, mock.Anything).Return().Maybe()
	s.browserMgr.On("SetTargetID", "ch-1", mock.Anything).Return().Maybe()
	s.browserMgr.On("GetTargetID", "ch-1").Return("").Maybe()
	s.browserMgr.On("TouchBrowser", "ch-1").Return().Maybe()

	mockCDP.On("TargetID").Return("").Maybe()
	mockCDP.On("EnableConsoleCapture", mock.Anything, mock.Anything).Return(nil).Maybe()
	mockCDP.On("EnableNetworkCapture", mock.Anything, mock.Anything).Return(nil).Maybe()
	mockCDP.On("GetPageInfo", mock.Anything).Return(&browser.PageInfo{URL: "https://x.com", Title: "X"}, nil)

	w := s.postBrowserAction(browserActionRequest{ChannelID: "ch-1", Action: "get_page_info"})
	require.Equal(s.T(), http.StatusOK, w.Code)
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Empty(s.T(), resp.Error)
	require.Equal(s.T(), 2, attempt) // First failed, delay, second succeeded.
}

// --- dispatchBrowserAction: list_tabs with active target ---

func (s *BrowserHandlerSuite) TestBrowserActionListTabsWithActiveTarget() {
	mockCDP := new(MockCDPClient)
	s.setupActionMocks(mockCDP)

	// Override GetTargetID to return a matching target.
	s.browserMgr.ExpectedCalls = filterCalls(s.browserMgr.ExpectedCalls, "GetTargetID")
	s.browserMgr.On("GetTargetID", "ch-1").Return("t2")

	mockCDP.On("ListTabs", mock.Anything).Return([]browser.TabInfo{
		{TargetID: "t1", URL: "https://a.com", Title: "A"},
		{TargetID: "t2", URL: "https://b.com", Title: "B"},
	}, nil)

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "list_tabs",
	})

	require.Equal(s.T(), http.StatusOK, w.Code)
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Empty(s.T(), resp.Error)
	require.Len(s.T(), resp.Tabs, 2)
	require.False(s.T(), resp.Tabs[0].Active)
	require.True(s.T(), resp.Tabs[1].Active)
}
