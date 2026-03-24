package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

type mockBrowserProvider struct {
	mock.Mock
}

func (m *mockBrowserProvider) EnsureBrowser(ctx context.Context, channelID, containerID string) error {
	return m.Called(ctx, channelID, containerID).Error(0)
}

func (m *mockBrowserProvider) StopBrowser(ctx context.Context, channelID string) error {
	return m.Called(ctx, channelID).Error(0)
}

func (m *mockBrowserProvider) IsRunning(ctx context.Context, channelID string) bool {
	return m.Called(ctx, channelID).Bool(0)
}

func (m *mockBrowserProvider) GetCDPEndpoint(channelID string) string {
	return m.Called(channelID).String(0)
}

func (m *mockBrowserProvider) GetContainerID(channelID string) (string, bool) {
	args := m.Called(channelID)
	return args.String(0), args.Bool(1)
}

func (m *mockBrowserProvider) IsHostMode() bool {
	return false
}

type BrowserHandlerSuite struct {
	suite.Suite
	browserMgr *mockBrowserProvider
	cFinder    *MockContainerFinder
	srv        *Server
}

func TestBrowserHandlerSuite(t *testing.T) {
	suite.Run(t, new(BrowserHandlerSuite))
}

func (s *BrowserHandlerSuite) SetupTest() {
	s.browserMgr = new(mockBrowserProvider)
	s.cFinder = new(MockContainerFinder)
	s.srv = nilServer()
	s.srv.SetBrowserProvider(s.browserMgr)
	s.srv.containerFinder = s.cFinder
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

func (s *BrowserHandlerSuite) TestSetBrowserProvider() {
	srv := nilServer()
	require.Nil(s.T(), srv.dockerBrowserProvider)

	mgr := new(mockBrowserProvider)
	srv.SetBrowserProvider(mgr)
	require.NotNil(s.T(), srv.dockerBrowserProvider)
}

func (s *BrowserHandlerSuite) TestSetHostBrowserProvider() {
	srv := nilServer()
	require.Nil(s.T(), srv.hostBrowserProvider)

	mgr := new(mockBrowserProvider)
	srv.SetHostBrowserProvider(mgr)
	require.NotNil(s.T(), srv.hostBrowserProvider)
}

func (s *BrowserHandlerSuite) TestActiveBrowserProviderDefault() {
	srv := nilServer()
	dockerMgr := new(mockBrowserProvider)
	srv.SetBrowserProvider(dockerMgr)
	require.Equal(s.T(), dockerMgr, srv.activeBrowserProvider("ch-1"))
}

func (s *BrowserHandlerSuite) TestActiveBrowserProviderHostMode() {
	srv := nilServer()
	dockerMgr := new(mockBrowserProvider)
	hostMgr := new(mockBrowserProvider)
	srv.SetBrowserProvider(dockerMgr)
	srv.SetHostBrowserProvider(hostMgr)

	srv.browserModeMu.Lock()
	srv.activeBrowserMode = map[string]string{"ch-1": "host"}
	srv.browserModeMu.Unlock()

	require.Equal(s.T(), hostMgr, srv.activeBrowserProvider("ch-1"))
	require.Equal(s.T(), dockerMgr, srv.activeBrowserProvider("ch-2"))
}

func (s *BrowserHandlerSuite) TestHandleBrowserModeSwitch() {
	srv := nilServer()
	srv.logger = slog.Default()
	dockerMgr := new(mockBrowserProvider)
	hostMgr := new(mockBrowserProvider)
	srv.SetBrowserProvider(dockerMgr)
	srv.SetHostBrowserProvider(hostMgr)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/browser/mode", srv.handleBrowserMode)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	body := `{"channel_id":"ch-1","mode":"host"}`
	resp, err := http.Post(ts.URL+"/api/browser/mode", "application/json", strings.NewReader(body))
	require.NoError(s.T(), err)
	defer resp.Body.Close()
	require.Equal(s.T(), http.StatusOK, resp.StatusCode)

	srv.browserModeMu.Lock()
	require.Equal(s.T(), "host", srv.activeBrowserMode["ch-1"])
	srv.browserModeMu.Unlock()
}

func (s *BrowserHandlerSuite) TestHandleBrowserModeInvalidMode() {
	srv := nilServer()
	srv.logger = slog.Default()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/browser/mode", srv.handleBrowserMode)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	body := `{"channel_id":"ch-1","mode":"invalid"}`
	resp, err := http.Post(ts.URL+"/api/browser/mode", "application/json", strings.NewReader(body))
	require.NoError(s.T(), err)
	defer resp.Body.Close()
	require.Equal(s.T(), http.StatusBadRequest, resp.StatusCode)
}

func (s *BrowserHandlerSuite) TestHandleBrowserModeMissingChannelID() {
	srv := nilServer()
	srv.logger = slog.Default()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/browser/mode", srv.handleBrowserMode)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	body := `{"mode":"host"}`
	resp, err := http.Post(ts.URL+"/api/browser/mode", "application/json", strings.NewReader(body))
	require.NoError(s.T(), err)
	defer resp.Body.Close()
	require.Equal(s.T(), http.StatusBadRequest, resp.StatusCode)
}

func (s *BrowserHandlerSuite) TestHandleBrowserModeHostNotConfigured() {
	srv := nilServer()
	srv.logger = slog.Default()
	srv.SetBrowserProvider(new(mockBrowserProvider))
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/browser/mode", srv.handleBrowserMode)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	body := `{"channel_id":"ch-1","mode":"host"}`
	resp, err := http.Post(ts.URL+"/api/browser/mode", "application/json", strings.NewReader(body))
	require.NoError(s.T(), err)
	defer resp.Body.Close()
	require.Equal(s.T(), http.StatusServiceUnavailable, resp.StatusCode)
}

func (s *BrowserHandlerSuite) TestHandleBrowserModeInvalidJSON() {
	srv := nilServer()
	srv.logger = slog.Default()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/browser/mode", srv.handleBrowserMode)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/api/browser/mode", "application/json", strings.NewReader("{bad"))
	require.NoError(s.T(), err)
	defer resp.Body.Close()
	require.Equal(s.T(), http.StatusBadRequest, resp.StatusCode)
}

func (s *BrowserHandlerSuite) TestBrowserWSNotConfigured() {
	srv := nilServer()
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

	// Input with no CDP should silently fail.
	err = ws.WriteJSON(browserWSMessage{Type: "unknown"})
	require.NoError(s.T(), err)

	resp := s.readResp(ws)
	require.Equal(s.T(), bwsRespError, resp.Type)
}

func (s *BrowserHandlerSuite) TestBrowserWSRoute() {
	srv := nilServer()
	srv.SetBrowserProvider(s.browserMgr)

	err := srv.Start("127.0.0.1:0")
	require.NoError(s.T(), err)
	defer func() { _ = srv.Stop(context.Background()) }()
}

var errTestAPI = errors.New("test error")

// --- Mock CDP Client ---

type mockCDPSession struct {
	mock.Mock
}

func (m *mockCDPSession) Navigate(ctx context.Context, url string) error {
	return m.Called(ctx, url).Error(0)
}
func (m *mockCDPSession) Reload(ctx context.Context) error    { return m.Called(ctx).Error(0) }
func (m *mockCDPSession) GoBack(ctx context.Context) error    { return m.Called(ctx).Error(0) }
func (m *mockCDPSession) GoForward(ctx context.Context) error { return m.Called(ctx).Error(0) }
func (m *mockCDPSession) GetPageInfo(ctx context.Context) (*browser.PageInfo, error) {
	args := m.Called(ctx)
	pi, _ := args.Get(0).(*browser.PageInfo)
	return pi, args.Error(1)
}
func (m *mockCDPSession) StartScreencast(quality, maxWidth, maxHeight int) <-chan []byte {
	args := m.Called(quality, maxWidth, maxHeight)
	ch, _ := args.Get(0).(<-chan []byte)
	return ch
}
func (m *mockCDPSession) StopScreencast()  { m.Called() }
func (m *mockCDPSession) ResetScreencast() { m.Called() }
func (m *mockCDPSession) MouseClick(ctx context.Context, x, y float64, button string, clickCount int) error {
	return m.Called(ctx, x, y, button, clickCount).Error(0)
}
func (m *mockCDPSession) MouseMove(ctx context.Context, x, y float64, buttons int) error {
	return m.Called(ctx, x, y, buttons).Error(0)
}
func (m *mockCDPSession) MouseScroll(ctx context.Context, x, y, deltaX, deltaY float64) error {
	return m.Called(ctx, x, y, deltaX, deltaY).Error(0)
}
func (m *mockCDPSession) KeyPress(ctx context.Context, key string) error {
	return m.Called(ctx, key).Error(0)
}
func (m *mockCDPSession) TypeText(ctx context.Context, text string) error {
	return m.Called(ctx, text).Error(0)
}
func (m *mockCDPSession) TargetID() string { return m.Called().String(0) }
func (m *mockCDPSession) SwitchTarget(targetID string) error {
	return m.Called(targetID).Error(0)
}
func (m *mockCDPSession) ListTabs(ctx context.Context) ([]browser.TabInfo, error) {
	args := m.Called(ctx)
	tabs, _ := args.Get(0).([]browser.TabInfo)
	return tabs, args.Error(1)
}
func (m *mockCDPSession) NewTab(ctx context.Context, url string) (string, error) {
	args := m.Called(ctx, url)
	return args.String(0), args.Error(1)
}
func (m *mockCDPSession) CloseTab(ctx context.Context, targetID string) error {
	return m.Called(ctx, targetID).Error(0)
}
func (m *mockCDPSession) EvaluateJS(ctx context.Context, expression string) (string, error) {
	args := m.Called(ctx, expression)
	return args.String(0), args.Error(1)
}
func (m *mockCDPSession) Close() { m.Called() }
func (m *mockCDPSession) GetElementRefs(ctx context.Context) ([]browser.ElementRef, error) {
	args := m.Called(ctx)
	refs, _ := args.Get(0).([]browser.ElementRef)
	return refs, args.Error(1)
}
func (m *mockCDPSession) ClickRef(ctx context.Context, refs []browser.ElementRef, refIndex int) error {
	return m.Called(ctx, refs, refIndex).Error(0)
}
func (m *mockCDPSession) Screenshot(ctx context.Context) ([]byte, error) {
	args := m.Called(ctx)
	data, _ := args.Get(0).([]byte)
	return data, args.Error(1)
}
func (m *mockCDPSession) EnableConsoleCapture(ctx context.Context, ch chan<- browser.ConsoleMessage) error {
	return m.Called(ctx, ch).Error(0)
}
func (m *mockCDPSession) EnableNetworkCapture(ctx context.Context, ch chan<- browser.NetworkRequest) error {
	return m.Called(ctx, ch).Error(0)
}
func (m *mockCDPSession) ResizeWindow(ctx context.Context, width, height int) error {
	return m.Called(ctx, width, height).Error(0)
}
func (m *mockCDPSession) ScrollIntoView(ctx context.Context, backendNodeID cdp.BackendNodeID) error {
	return m.Called(ctx, backendNodeID).Error(0)
}
func (m *mockCDPSession) MouseDown(ctx context.Context, x, y float64, button string) error {
	return m.Called(ctx, x, y, button).Error(0)
}
func (m *mockCDPSession) MouseUp(ctx context.Context, x, y float64, button string) error {
	return m.Called(ctx, x, y, button).Error(0)
}
func (m *mockCDPSession) NewContextForTarget(_ string) (browser.CDPSession, error) {
	return m, nil
}

// --- Helper: start browser and get WS with CDP mock ---

// newTestCDPManager creates a CDPManager with a mock factory for testing.
func newTestCDPManager(mockCDP *mockCDPSession) *browser.CDPManager {
	mgr := browser.NewCDPManager("ws://test:9222", browser.CDPManagerConfig{
		DiscoverExisting: false,
		MaxRetries:       1,
		RetryDelay:       time.Millisecond,
	}, slog.Default())
	return mgr
}

func (s *BrowserHandlerSuite) startBrowserWS() (*websocket.Conn, *httptest.Server, *mockCDPSession) {
	mockCDP := new(mockCDPSession)

	s.browserMgr.On("EnsureBrowser", mock.Anything, "ch-1", "").Return(nil)
	s.browserMgr.On("GetCDPEndpoint", "ch-1").Return("ws://127.0.0.1:9222")

	mockCDP.On("TargetID").Return("test-target").Maybe()
	mockCDP.On("SwitchTarget", mock.Anything).Return(nil).Maybe()
	mockCDP.On("ListTabs", mock.Anything).Return([]browser.TabInfo(nil), nil).Maybe()

	// Inject a CDPManager with the mock CDP factory into the server.
	cdpMgr := newTestCDPManager(mockCDP)
	s.srv.cdpManagersMu.Lock()
	if s.srv.cdpManagers == nil {
		s.srv.cdpManagers = make(map[string]*browser.CDPManager)
	}
	s.srv.cdpManagers["ch-1|docker"] = cdpMgr
	s.srv.cdpManagersMu.Unlock()
	// Instead of complex injection, use a simpler approach:
	// Override the server's getOrCreateCDPManager to return a pre-connected manager.
	ws, ts := s.dialBrowserWS()

	// Directly manipulate: create a connected CDPManager by injecting
	// the mock CDP client before the WS start message.
	s.srv.cdpManagersMu.Lock()
	mgr := s.srv.cdpManagers["ch-1|docker"]
	if mgr == nil {
		mgr = newTestCDPManager(mockCDP)
		s.srv.cdpManagers["ch-1|docker"] = mgr
	}
	s.srv.cdpManagersMu.Unlock()

	// Override cdpFactory on the manager to return our mock.
	browser.SetCDPFactoryForTest(mgr, func(_ context.Context, _ string, _ *slog.Logger, _ ...browser.CDPOption) (browser.CDPSession, error) {
		return mockCDP, nil
	})

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

func (s *BrowserHandlerSuite) TestStartCDPConnectError() {
	s.browserMgr.On("EnsureBrowser", mock.Anything, "ch-1", "").Return(nil)
	s.browserMgr.On("GetCDPEndpoint", "ch-1").Return("ws://127.0.0.1:9222")

	// Create a CDPManager with a factory that always fails.
	cdpMgr := browser.NewCDPManager("ws://test:9222", browser.CDPManagerConfig{
		MaxRetries: 1,
		RetryDelay: time.Millisecond,
	}, slog.Default())
	browser.SetCDPFactoryForTest(cdpMgr, func(_ context.Context, _ string, _ *slog.Logger, _ ...browser.CDPOption) (browser.CDPSession, error) {
		return nil, errors.New("cdp connect failed")
	})
	s.srv.cdpManagersMu.Lock()
	if s.srv.cdpManagers == nil {
		s.srv.cdpManagers = make(map[string]*browser.CDPManager)
	}
	s.srv.cdpManagers["ch-1|docker"] = cdpMgr
	s.srv.cdpManagersMu.Unlock()

	ws, ts := s.dialBrowserWS()
	defer ts.Close()
	defer ws.Close()

	require.NoError(s.T(), ws.WriteJSON(browserWSMessage{Type: bwsMsgStart, ChannelID: "ch-1"}))
	resp := s.readResp(ws)
	require.Equal(s.T(), bwsRespError, resp.Type)
	require.Contains(s.T(), resp.Message, "failed to connect CDP")
}

// --- Screencast success ---

func (s *BrowserHandlerSuite) TestScreencastSendsBinaryFrames() {
	ws, ts, mockCDP := s.startBrowserWS()
	defer ts.Close()
	defer ws.Close()

	frameCh := make(chan []byte, 2)
	mockCDP.On("StartScreencast", 60, 1280, 900).Return((<-chan []byte)(frameCh))

	require.NoError(s.T(), ws.WriteJSON(browserWSMessage{Type: bwsMsgScreencast}))

	frame1 := []byte{0xFF, 0xD8, 0x01, 0x02, 0x03}
	frame2 := []byte{0xFF, 0xD8, 0x04, 0x05, 0x06}
	frameCh <- frame1
	frameCh <- frame2

	require.NoError(s.T(), ws.SetReadDeadline(time.Now().Add(2*time.Second)))
	msgType, data, err := ws.ReadMessage()
	require.NoError(s.T(), err)
	require.Equal(s.T(), websocket.BinaryMessage, msgType)
	require.Equal(s.T(), frame1, data)

	msgType, data, err = ws.ReadMessage()
	require.NoError(s.T(), err)
	require.Equal(s.T(), websocket.BinaryMessage, msgType)
	require.Equal(s.T(), frame2, data)

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

	mockCDP.On("MouseMove", mock.Anything, float64(50), float64(60), 0).Return(nil)
	require.NoError(s.T(), ws.WriteJSON(browserWSMessage{Type: bwsMsgInput, InputType: "mousemove", X: 50, Y: 60}))
	time.Sleep(20 * time.Millisecond)
	mockCDP.AssertCalled(s.T(), "MouseMove", mock.Anything, float64(50), float64(60), 0)
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
	require.NoError(s.T(), ws.WriteJSON(browserWSMessage{Type: "unknown"}))
	resp := s.readResp(ws)
	require.Equal(s.T(), bwsRespError, resp.Type)
}

// --- pipeFrames ---

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

	ms := &mockFrameSender{stopCh: make(chan struct{})}

	frameCh := make(chan []byte, 1)
	frameCh <- []byte("frame-data")

	done := make(chan struct{})
	go func() {
		bc.pipeFrames(frameCh, ms)
		close(done)
	}()

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

// --- cleanup ---

func (s *BrowserHandlerSuite) TestCleanupWithStream() {
	ws, ts, _ := s.startBrowserWS()
	defer ts.Close()
	ws.Close()
	time.Sleep(50 * time.Millisecond)
}

// --- WS upgrade error ---

func (s *BrowserHandlerSuite) TestBrowserWSUpgradeError() {
	srv := nilServer()
	mgr := new(mockBrowserProvider)
	srv.SetBrowserProvider(mgr)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/ws/browser", srv.handleBrowserWS)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/ws/browser")
	require.NoError(s.T(), err)
	resp.Body.Close()
}

func (s *BrowserHandlerSuite) TestSendJSONWriteError() {
	ws, ts, _ := s.startBrowserWS()
	defer ts.Close()

	ws.Close()
	time.Sleep(20 * time.Millisecond)

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
	bc.sendJSON(browserWSResponse{Type: bwsRespError, Message: "test"})
}

// --- watchMCPTabChanges ---

func (s *BrowserHandlerSuite) TestWatchMCPTabChangesStopCh() {
	cdpMgr := browser.NewCDPManager("ws://test:9222", browser.CDPManagerConfig{}, slog.Default())

	bc := &browserWSConn{
		logger: slog.Default(),
		cdpMgr: cdpMgr,
		stopCh: make(chan struct{}),
	}

	done := make(chan struct{})
	go func() {
		bc.watchMCPTabChanges()
		close(done)
	}()

	close(bc.stopCh)
	select {
	case <-done:
	case <-time.After(time.Second):
		s.T().Fatal("watchMCPTabChanges did not exit on stopCh")
	}
}

func (s *BrowserHandlerSuite) TestWatchMCPTabChangesSwitchNoCDP() {
	cdpMgr := browser.NewCDPManager("ws://test:9222", browser.CDPManagerConfig{}, slog.Default())

	bc := &browserWSConn{
		logger: slog.Default(),
		cdpMgr: cdpMgr,
		stopCh: make(chan struct{}),
	}

	done := make(chan struct{})
	go func() {
		bc.watchMCPTabChanges()
		close(done)
	}()

	cdpMgr.NotifyTargetSwitch("t-new")
	select {
	case <-done:
	case <-time.After(time.Second):
		s.T().Fatal("watchMCPTabChanges did not exit when cdp is nil")
	}
}

func (s *BrowserHandlerSuite) TestWatchMCPTabChangesTabAddedSendsWS() {
	ws, ts, _ := s.startBrowserWS()
	defer ts.Close()
	defer ws.Close()

	// Get the CDPManager and send a tab added notification.
	s.srv.cdpManagersMu.Lock()
	cdpMgr := s.srv.cdpManagers["ch-1|docker"]
	s.srv.cdpManagersMu.Unlock()

	cdpMgr.NotifyTabAdded(browser.TabInfo{TargetID: "t-new", URL: "https://new.com", Title: "New"})

	require.NoError(s.T(), ws.SetReadDeadline(time.Now().Add(2*time.Second)))
	resp := s.readResp(ws)
	require.Equal(s.T(), bwsRespTabCreated, resp.Type)
	require.Equal(s.T(), "t-new", resp.TargetID)
	require.Equal(s.T(), "https://new.com", resp.URL)
	require.Equal(s.T(), "New", resp.Title)
}

func (s *BrowserHandlerSuite) TestWatchMCPTabChangesTabRemovedSendsWS() {
	ws, ts, _ := s.startBrowserWS()
	defer ts.Close()
	defer ws.Close()

	s.srv.cdpManagersMu.Lock()
	cdpMgr := s.srv.cdpManagers["ch-1|docker"]
	s.srv.cdpManagersMu.Unlock()

	cdpMgr.NotifyTabRemoved("t-old")

	require.NoError(s.T(), ws.SetReadDeadline(time.Now().Add(2*time.Second)))
	resp := s.readResp(ws)
	require.Equal(s.T(), bwsRespTabClosed, resp.Type)
	require.Equal(s.T(), "t-old", resp.TargetID)
}

// --- handleBrowserAction tests ---

func (s *BrowserHandlerSuite) postBrowserAction(req browserActionRequest) *httptest.ResponseRecorder {
	data, _ := json.Marshal(req)
	r := httptest.NewRequest(http.MethodPost, "/api/browser/action", strings.NewReader(string(data)))
	w := httptest.NewRecorder()
	s.srv.handleBrowserAction(w, r)
	return w
}

func (s *BrowserHandlerSuite) TestBrowserActionNoBrowserProvider() {
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

// setupActionMocks sets up common mocks for handleBrowserAction tests.
func (s *BrowserHandlerSuite) setupActionMocks(mockCDP *mockCDPSession) {
	s.browserMgr.On("EnsureBrowser", mock.Anything, "ch-1", "").Return(nil).Maybe()
	s.browserMgr.On("GetCDPEndpoint", "ch-1").Return("ws://127.0.0.1:9222").Maybe()

	mockCDP.On("TargetID").Return("test-target").Maybe()
	mockCDP.On("EnableConsoleCapture", mock.Anything, mock.Anything).Return(nil).Maybe()
	mockCDP.On("EnableNetworkCapture", mock.Anything, mock.Anything).Return(nil).Maybe()

	// Create and inject a CDPManager with the mock factory.
	cdpMgr := browser.NewCDPManager("ws://127.0.0.1:9222", browser.CDPManagerConfig{
		DiscoverExisting: false,
		MaxRetries:       1,
		RetryDelay:       time.Millisecond,
	}, slog.Default())
	browser.SetCDPFactoryForTest(cdpMgr, func(_ context.Context, _ string, _ *slog.Logger, _ ...browser.CDPOption) (browser.CDPSession, error) {
		return mockCDP, nil
	})

	s.srv.cdpManagersMu.Lock()
	if s.srv.cdpManagers == nil {
		s.srv.cdpManagers = make(map[string]*browser.CDPManager)
	}
	s.srv.cdpManagers["ch-1|docker"] = cdpMgr
	s.srv.cdpManagersMu.Unlock()
}

func (s *BrowserHandlerSuite) TestBrowserActionNavigateSuccess() {
	mockCDP := new(mockCDPSession)
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
	mockCDP := new(mockCDPSession)
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
	mockCDP := new(mockCDPSession)
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
	mockCDP := new(mockCDPSession)
	s.setupActionMocks(mockCDP)

	// Track the tab so it passes the filter.
	cdpMgr := s.srv.cdpManagers["ch-1|docker"]
	cdpMgr.TrackTab("t1")

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
	mockCDP := new(mockCDPSession)
	s.setupActionMocks(mockCDP)

	mockCDP.On("NewTab", mock.Anything, "about:blank").Return("new-target", nil)

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
	mockCDP := new(mockCDPSession)
	s.setupActionMocks(mockCDP)

	mockCDP.On("CloseTab", mock.Anything, "t1").Return(nil)
	mockCDP.On("NewTab", mock.Anything, "about:blank").Return("t2", nil).Maybe()

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

func (s *BrowserHandlerSuite) TestBrowserActionUnknownAction() {
	mockCDP := new(mockCDPSession)
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

func (s *BrowserHandlerSuite) TestBrowserActionGetBrowserCDPEnsureError() {
	s.browserMgr.On("EnsureBrowser", mock.Anything, "ch-1", "").Return(errors.New("ensure fail"))
	s.browserMgr.On("GetCDPEndpoint", "ch-1").Return("ws://127.0.0.1:9222").Maybe()

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
	s.browserMgr.On("EnsureBrowser", mock.Anything, "ch-1", "").Return(nil)
	s.browserMgr.On("GetCDPEndpoint", "ch-1").Return("")

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
	s.browserMgr.On("EnsureBrowser", mock.Anything, "ch-1", "").Return(nil)
	s.browserMgr.On("GetCDPEndpoint", "ch-1").Return("ws://127.0.0.1:9222")

	// Create a CDPManager with a factory that always fails.
	cdpMgr := browser.NewCDPManager("ws://127.0.0.1:9222", browser.CDPManagerConfig{
		MaxRetries: 1,
		RetryDelay: time.Millisecond,
	}, slog.Default())
	browser.SetCDPFactoryForTest(cdpMgr, func(_ context.Context, _ string, _ *slog.Logger, _ ...browser.CDPOption) (browser.CDPSession, error) {
		return nil, errors.New("cdp connect fail")
	})
	s.srv.cdpManagersMu.Lock()
	if s.srv.cdpManagers == nil {
		s.srv.cdpManagers = make(map[string]*browser.CDPManager)
	}
	s.srv.cdpManagers["ch-1|docker"] = cdpMgr
	s.srv.cdpManagersMu.Unlock()

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "get_page_info",
	})

	require.Equal(s.T(), http.StatusOK, w.Code)
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Error, "connecting CDP")
}

// --- ensureBrowserCapture paths ---

func (s *BrowserHandlerSuite) TestEnsureBrowserCaptureAlreadyStarted() {
	mockCDP := new(mockCDPSession)
	mockCDP.On("EnableConsoleCapture", mock.Anything, mock.Anything).Return(nil)
	mockCDP.On("EnableNetworkCapture", mock.Anything, mock.Anything).Return(nil)

	// First call initializes capture and records the client.
	s.srv.ensureBrowserCapture(context.Background(), "ch-1", mockCDP)
	// Second call with the same client should not rewire.
	s.srv.ensureBrowserCapture(context.Background(), "ch-1", mockCDP)

	mockCDP.AssertNumberOfCalls(s.T(), "EnableConsoleCapture", 1)
	mockCDP.AssertNumberOfCalls(s.T(), "EnableNetworkCapture", 1)
}

func (s *BrowserHandlerSuite) TestEnsureBrowserCaptureRewireOnNewClient() {
	mockCDP1 := new(mockCDPSession)
	mockCDP1.On("EnableConsoleCapture", mock.Anything, mock.Anything).Return(nil)
	mockCDP1.On("EnableNetworkCapture", mock.Anything, mock.Anything).Return(nil)

	// First call initializes capture with client 1.
	s.srv.ensureBrowserCapture(context.Background(), "ch-1", mockCDP1)

	// Second call with a different client should rewire capture.
	mockCDP2 := new(mockCDPSession)
	mockCDP2.On("EnableConsoleCapture", mock.Anything, mock.Anything).Return(nil)
	mockCDP2.On("EnableNetworkCapture", mock.Anything, mock.Anything).Return(nil)

	s.srv.ensureBrowserCapture(context.Background(), "ch-1", mockCDP2)

	mockCDP2.AssertCalled(s.T(), "EnableConsoleCapture", mock.Anything, mock.Anything)
	mockCDP2.AssertCalled(s.T(), "EnableNetworkCapture", mock.Anything, mock.Anything)
}

func (s *BrowserHandlerSuite) TestEnsureBrowserCaptureConsoleCaptureError() {
	mockCDP := new(mockCDPSession)
	mockCDP.On("EnableConsoleCapture", mock.Anything, mock.Anything).Return(errors.New("console cap fail"))
	mockCDP.On("EnableNetworkCapture", mock.Anything, mock.Anything).Return(nil)

	s.srv.ensureBrowserCapture(context.Background(), "ch-1", mockCDP)

	mockCDP.AssertCalled(s.T(), "EnableConsoleCapture", mock.Anything, mock.Anything)
	mockCDP.AssertCalled(s.T(), "EnableNetworkCapture", mock.Anything, mock.Anything)
}

func (s *BrowserHandlerSuite) TestEnsureBrowserCaptureNetworkCaptureError() {
	mockCDP := new(mockCDPSession)
	mockCDP.On("EnableConsoleCapture", mock.Anything, mock.Anything).Return(nil)
	mockCDP.On("EnableNetworkCapture", mock.Anything, mock.Anything).Return(errors.New("net cap fail"))

	s.srv.ensureBrowserCapture(context.Background(), "ch-1", mockCDP)

	mockCDP.AssertCalled(s.T(), "EnableConsoleCapture", mock.Anything, mock.Anything)
	mockCDP.AssertCalled(s.T(), "EnableNetworkCapture", mock.Anything, mock.Anything)
}

func (s *BrowserHandlerSuite) TestEnsureBrowserCaptureGoroutinesBodies() {
	mockCDP := new(mockCDPSession)
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

	require.Eventually(s.T(), func() bool {
		s.srv.browserCapturesMu.Lock()
		cs := s.srv.browserCaptures["ch-goroutines"]
		s.srv.browserCapturesMu.Unlock()
		if cs == nil {
			return false
		}
		cs.ConsoleMu.Lock()
		nConsole := len(cs.ConsoleMsgs)
		cs.ConsoleMu.Unlock()
		cs.NetworkMu.Lock()
		nNetwork := len(cs.NetworkReqs)
		cs.NetworkMu.Unlock()
		return nConsole == 1 && nNetwork == 1
	}, time.Second, 5*time.Millisecond, "goroutines did not process messages in time")

	s.srv.browserCapturesMu.Lock()
	cs := s.srv.browserCaptures["ch-goroutines"]
	s.srv.browserCapturesMu.Unlock()
	require.Equal(s.T(), "test-msg", cs.ConsoleMsgs[0].Text)
	require.Equal(s.T(), "https://test.com", cs.NetworkReqs[0].URL)
}

// --- dispatchBrowserAction: action types ---

func (s *BrowserHandlerSuite) TestBrowserActionReload() {
	mockCDP := new(mockCDPSession)
	s.setupActionMocks(mockCDP)
	mockCDP.On("Reload", mock.Anything).Return(nil)

	w := s.postBrowserAction(browserActionRequest{ChannelID: "ch-1", Action: "reload"})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Empty(s.T(), resp.Error)
	require.Equal(s.T(), "Page reloaded", resp.Result)
}

func (s *BrowserHandlerSuite) TestBrowserActionReloadError() {
	mockCDP := new(mockCDPSession)
	s.setupActionMocks(mockCDP)
	mockCDP.On("Reload", mock.Anything).Return(errors.New("reload fail"))

	w := s.postBrowserAction(browserActionRequest{ChannelID: "ch-1", Action: "reload"})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Error, "reload failed")
}

func (s *BrowserHandlerSuite) TestBrowserActionGoBack() {
	mockCDP := new(mockCDPSession)
	s.setupActionMocks(mockCDP)
	mockCDP.On("GoBack", mock.Anything).Return(nil)

	w := s.postBrowserAction(browserActionRequest{ChannelID: "ch-1", Action: "go_back"})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Empty(s.T(), resp.Error)
	require.Equal(s.T(), "Navigated back", resp.Result)
}

func (s *BrowserHandlerSuite) TestBrowserActionGoBackError() {
	mockCDP := new(mockCDPSession)
	s.setupActionMocks(mockCDP)
	mockCDP.On("GoBack", mock.Anything).Return(errors.New("back fail"))

	w := s.postBrowserAction(browserActionRequest{ChannelID: "ch-1", Action: "go_back"})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Error, "go back failed")
}

func (s *BrowserHandlerSuite) TestBrowserActionGoForward() {
	mockCDP := new(mockCDPSession)
	s.setupActionMocks(mockCDP)
	mockCDP.On("GoForward", mock.Anything).Return(nil)

	w := s.postBrowserAction(browserActionRequest{ChannelID: "ch-1", Action: "go_forward"})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Empty(s.T(), resp.Error)
	require.Equal(s.T(), "Navigated forward", resp.Result)
}

func (s *BrowserHandlerSuite) TestBrowserActionGoForwardError() {
	mockCDP := new(mockCDPSession)
	s.setupActionMocks(mockCDP)
	mockCDP.On("GoForward", mock.Anything).Return(errors.New("fwd fail"))

	w := s.postBrowserAction(browserActionRequest{ChannelID: "ch-1", Action: "go_forward"})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Error, "go forward failed")
}

func (s *BrowserHandlerSuite) TestBrowserActionGetPageInfoError() {
	mockCDP := new(mockCDPSession)
	s.setupActionMocks(mockCDP)
	mockCDP.On("GetPageInfo", mock.Anything).Return((*browser.PageInfo)(nil), errors.New("info fail"))

	w := s.postBrowserAction(browserActionRequest{ChannelID: "ch-1", Action: "get_page_info"})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Error, "get page info failed")
}

func (s *BrowserHandlerSuite) TestBrowserActionNavigateGetPageInfoError() {
	mockCDP := new(mockCDPSession)
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
	mockCDP := new(mockCDPSession)
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
	mockCDP := new(mockCDPSession)
	s.setupActionMocks(mockCDP)
	mockCDP.On("GetElementRefs", mock.Anything).Return(([]browser.ElementRef)(nil), errors.New("refs fail"))

	w := s.postBrowserAction(browserActionRequest{ChannelID: "ch-1", Action: "get_element_refs"})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Error, "get element refs failed")
}

func (s *BrowserHandlerSuite) TestBrowserActionMouseClick() {
	mockCDP := new(mockCDPSession)
	s.setupActionMocks(mockCDP)
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

func (s *BrowserHandlerSuite) TestBrowserActionReadConsole() {
	mockCDP := new(mockCDPSession)
	s.setupActionMocks(mockCDP)

	s.srv.browserCapturesMu.Lock()
	if s.srv.browserCaptures == nil {
		s.srv.browserCaptures = make(map[string]*browser.CaptureState)
	}
	cs := &browser.CaptureState{Started: true}
	cs.ConsoleMsgs = []browser.ConsoleMessage{
		{Level: "log", Text: "hello world", Time: time.Now()},
		{Level: "error", Text: "something failed", Time: time.Now()},
	}
	s.srv.browserCaptures["ch-1"] = cs
	s.srv.browserCapturesMu.Unlock()

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
	mockCDP := new(mockCDPSession)
	s.setupActionMocks(mockCDP)

	s.srv.browserCapturesMu.Lock()
	if s.srv.browserCaptures == nil {
		s.srv.browserCaptures = make(map[string]*browser.CaptureState)
	}
	cs := &browser.CaptureState{Started: true}
	cs.NetworkReqs = []browser.NetworkRequest{
		{URL: "https://api.example.com/v1", Method: "GET", Status: 200, StatusText: "OK", Time: time.Now()},
	}
	s.srv.browserCaptures["ch-1"] = cs
	s.srv.browserCapturesMu.Unlock()

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

// --- RunBrowserIdleMonitor ---

func (s *BrowserHandlerSuite) TestRunBrowserIdleMonitorCancelledContext() {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s.srv.RunBrowserIdleMonitor(ctx, 10*time.Minute)
}

func (s *BrowserHandlerSuite) TestCleanIdleBrowserSessions() {
	cdpMgr := browser.NewCDPManager("ws://test:9222", browser.CDPManagerConfig{}, slog.Default())
	// Make lastUsedAt old by using a fixed time.
	browser.SetTimeNowForTest(cdpMgr, func() time.Time {
		return time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	})
	cdpMgr.Touch() // sets lastUsedAt to 2020

	s.srv.cdpManagersMu.Lock()
	if s.srv.cdpManagers == nil {
		s.srv.cdpManagers = make(map[string]*browser.CDPManager)
	}
	s.srv.cdpManagers["ch-1|docker"] = cdpMgr
	s.srv.cdpManagersMu.Unlock()

	s.browserMgr.On("StopBrowser", mock.Anything, "ch-1").Return(nil)

	s.srv.cleanIdleBrowserSessions(context.Background(), time.Minute)

	s.srv.cdpManagersMu.Lock()
	_, exists := s.srv.cdpManagers["ch-1|docker"]
	s.srv.cdpManagersMu.Unlock()
	require.False(s.T(), exists)
}

// --- activeMode ---

func (s *BrowserHandlerSuite) TestActiveMode() {
	require.Equal(s.T(), "docker", s.srv.activeMode("ch-1"))

	s.srv.browserModeMu.Lock()
	if s.srv.activeBrowserMode == nil {
		s.srv.activeBrowserMode = make(map[string]string)
	}
	s.srv.activeBrowserMode["ch-1"] = "host"
	s.srv.browserModeMu.Unlock()

	require.Equal(s.T(), "host", s.srv.activeMode("ch-1"))
}

// --- getOrCreateCDPManager ---

func (s *BrowserHandlerSuite) TestGetOrCreateCDPManager() {
	s.browserMgr.On("GetCDPEndpoint", "ch-1").Return("ws://127.0.0.1:9222").Maybe()
	mgr := s.srv.getOrCreateCDPManager("ch-1", "docker", s.browserMgr)
	require.NotNil(s.T(), mgr)

	// Second call should return the same manager.
	mgr2 := s.srv.getOrCreateCDPManager("ch-1", "docker", s.browserMgr)
	require.Equal(s.T(), mgr, mgr2)
}

func (s *BrowserHandlerSuite) TestGetActiveCDPManagerNotFound() {
	require.Nil(s.T(), s.srv.getActiveCDPManager("nonexistent"))
}

// --- mockHostBrowserProvider ---

type mockHostBrowserProvider struct {
	mock.Mock
}

func (m *mockHostBrowserProvider) EnsureBrowser(ctx context.Context, channelID, containerID string) error {
	return m.Called(ctx, channelID, containerID).Error(0)
}
func (m *mockHostBrowserProvider) StopBrowser(ctx context.Context, channelID string) error {
	return m.Called(ctx, channelID).Error(0)
}
func (m *mockHostBrowserProvider) IsRunning(ctx context.Context, channelID string) bool {
	return m.Called(ctx, channelID).Bool(0)
}
func (m *mockHostBrowserProvider) GetCDPEndpoint(channelID string) string {
	return m.Called(channelID).String(0)
}
func (m *mockHostBrowserProvider) GetContainerID(channelID string) (string, bool) {
	args := m.Called(channelID)
	return args.String(0), args.Bool(1)
}
func (m *mockHostBrowserProvider) IsHostMode() bool {
	return true
}

// --- getOrCreateCDPManager host mode ---

func (s *BrowserHandlerSuite) TestGetOrCreateCDPManagerHostMode() {
	hostProvider := new(mockHostBrowserProvider)
	hostProvider.On("GetCDPEndpoint", "ch-1").Return("ws://127.0.0.1:9222")

	mgr := s.srv.getOrCreateCDPManager("ch-1", "host", hostProvider)
	require.NotNil(s.T(), mgr)
	// Host mode: DiscoverExisting should be false, MaxRetries should be 1.
	require.False(s.T(), mgr.DiscoverExisting())
}

func (s *BrowserHandlerSuite) TestGetOrCreateCDPManagerNilMap() {
	// Ensure it works when cdpManagers map is nil.
	srv := nilServer()
	srv.SetBrowserProvider(s.browserMgr)
	s.browserMgr.On("GetCDPEndpoint", "ch-1").Return("ws://127.0.0.1:9222").Maybe()
	mgr := srv.getOrCreateCDPManager("ch-1", "docker", s.browserMgr)
	require.NotNil(s.T(), mgr)
}

// --- paramFloat / paramBool full coverage ---

func (s *BrowserHandlerSuite) TestParamFloatMissing() {
	require.Equal(s.T(), float64(0), paramFloat(nil, "key"))
	require.Equal(s.T(), float64(0), paramFloat(map[string]any{"other": "x"}, "key"))
}

func (s *BrowserHandlerSuite) TestParamFloatPresent() {
	require.Equal(s.T(), float64(42.5), paramFloat(map[string]any{"key": float64(42.5)}, "key"))
}

func (s *BrowserHandlerSuite) TestParamBoolMissing() {
	require.False(s.T(), paramBool(nil, "key"))
	require.False(s.T(), paramBool(map[string]any{"other": "x"}, "key"))
}

func (s *BrowserHandlerSuite) TestParamBoolPresent() {
	require.True(s.T(), paramBool(map[string]any{"key": true}, "key"))
}

// --- restartScreencastForTarget ---

func (s *BrowserHandlerSuite) TestRestartScreencastForTargetNoCDPMgr() {
	bc := &browserWSConn{
		logger: slog.Default(),
		stopCh: make(chan struct{}),
	}
	// No cdpMgr — should log error and return.
	bc.restartScreencastForTarget(context.Background(), nil, "t1")
}

func (s *BrowserHandlerSuite) TestRestartScreencastForTargetGetOrCreateError() {
	// Set up a CDPManager with no active clients.
	cdpMgr := browser.NewCDPManager("ws://test:9222", browser.CDPManagerConfig{
		MaxRetries: 1,
		RetryDelay: time.Millisecond,
	}, slog.Default())

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
		conn:   serverConn,
		logger: slog.Default(),
		cdpMgr: cdpMgr,
		stopCh: make(chan struct{}),
	}
	// GetOrCreate will fail because no active client exists.
	bc.restartScreencastForTarget(context.Background(), nil, "t-new")
}

func (s *BrowserHandlerSuite) TestRestartScreencastForTargetClosesOldStopCh() {
	mockCDP := new(mockCDPSession)
	mockCDP.On("TargetID").Return("t-new").Maybe()
	mockCDP.On("SwitchTarget", "t-new").Return(nil)
	mockCDP.On("ResetScreencast").Return()
	frameCh := make(chan []byte, 2)
	mockCDP.On("StartScreencast", 60, 1920, 1080).Return((<-chan []byte)(frameCh))
	mockCDP.On("EvaluateJS", mock.Anything, mock.Anything).Return("", nil)
	mockCDP.On("ListTabs", mock.Anything).Return([]browser.TabInfo(nil), nil)
	mockCDP.On("StopScreencast").Return().Maybe()
	mockCDP.On("Close").Return().Maybe()

	cdpMgr := browser.NewCDPManager("ws://test:9222", browser.CDPManagerConfig{
		MaxRetries: 1,
		RetryDelay: time.Millisecond,
	}, slog.Default())
	adapter := mockCDP
	cdpMgr.SetClientForTarget("t-new", adapter)

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

	// Set screencastStopCh to a non-nil channel so the close path is exercised.
	oldStopCh := make(chan struct{})
	bc := &browserWSConn{
		conn:             serverConn,
		browserProvider:  s.browserMgr,
		logger:           slog.Default(),
		cdpMgr:           cdpMgr,
		stopCh:           make(chan struct{}),
		screencastStopCh: oldStopCh,
	}

	bc.restartScreencastForTarget(context.Background(), mockCDP, "t-new")

	// Verify old stopCh was closed.
	select {
	case <-oldStopCh:
		// OK — closed.
	default:
		s.T().Fatal("old screencastStopCh should be closed")
	}

	close(frameCh)
}

func (s *BrowserHandlerSuite) TestRestartScreencastForTargetSuccess() {
	mockCDP := new(mockCDPSession)
	mockCDP.On("TargetID").Return("t-new").Maybe()
	mockCDP.On("SwitchTarget", "t-new").Return(nil)
	mockCDP.On("ResetScreencast").Return()
	frameCh := make(chan []byte, 2)
	mockCDP.On("StartScreencast", 60, 1920, 1080).Return((<-chan []byte)(frameCh))
	mockCDP.On("EvaluateJS", mock.Anything, mock.Anything).Return("", nil)
	mockCDP.On("ListTabs", mock.Anything).Return([]browser.TabInfo{
		{TargetID: "t-new", URL: "https://test.com"},
	}, nil)
	mockCDP.On("StopScreencast").Return().Maybe()
	mockCDP.On("Close").Return().Maybe()

	cdpMgr := browser.NewCDPManager("ws://test:9222", browser.CDPManagerConfig{
		MaxRetries: 1,
		RetryDelay: time.Millisecond,
	}, slog.Default())
	// Set up a mock client so GetOrCreate finds it.
	adapter := mockCDP
	cdpMgr.SetClientForTarget("t-new", adapter)

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
		conn:            serverConn,
		browserProvider: s.browserMgr,
		logger:          slog.Default(),
		cdpMgr:          cdpMgr,
		stopCh:          make(chan struct{}),
	}

	bc.restartScreencastForTarget(context.Background(), mockCDP, "t-new")

	// Verify tab_switched response was sent.
	require.NoError(s.T(), clientWS.SetReadDeadline(time.Now().Add(2*time.Second)))
	var resp browserWSResponse
	err = clientWS.ReadJSON(&resp)
	require.NoError(s.T(), err)
	require.Equal(s.T(), bwsRespTabSwitched, resp.Type)
	require.Equal(s.T(), "t-new", resp.TargetID)

	close(frameCh)
}

// --- sendTabsResponse ---

func (s *BrowserHandlerSuite) TestSendTabsResponseDockerMode() {
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

	cdpMgr := browser.NewCDPManager("ws://test:9222", browser.CDPManagerConfig{}, slog.Default())
	cdpMgr.TrackTab("t1")
	cdpMgr.TrackTab("t2")

	bc := &browserWSConn{
		conn:            serverConn,
		browserProvider: s.browserMgr,
		logger:          slog.Default(),
		cdpMgr:          cdpMgr,
		stopCh:          make(chan struct{}),
	}

	tabs := []browser.TabInfo{
		{TargetID: "t1", URL: "https://a.com", Title: "A"},
		{TargetID: "t2", URL: "https://b.com", Title: "B"},
	}
	bc.sendTabsResponse(tabs, "t1")

	require.NoError(s.T(), clientWS.SetReadDeadline(time.Now().Add(2*time.Second)))
	var resp browserWSResponse
	err = clientWS.ReadJSON(&resp)
	require.NoError(s.T(), err)
	require.Equal(s.T(), bwsRespTabs, resp.Type)
	require.Equal(s.T(), "t1", resp.ActiveTargetID)
	require.Len(s.T(), resp.Tabs, 2)
}

func (s *BrowserHandlerSuite) TestSendTabsResponseHostMode() {
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

	cdpMgr := browser.NewCDPManager("ws://test:9222", browser.CDPManagerConfig{}, slog.Default())
	cdpMgr.TrackTab("t1") // Only track t1, not t2.

	hostProvider := new(mockHostBrowserProvider)

	bc := &browserWSConn{
		conn:            serverConn,
		browserProvider: hostProvider,
		logger:          slog.Default(),
		cdpMgr:          cdpMgr,
		stopCh:          make(chan struct{}),
	}

	tabs := []browser.TabInfo{
		{TargetID: "t1", URL: "https://a.com", Title: "A"},
		{TargetID: "t2", URL: "https://b.com", Title: "B"}, // Not tracked — filtered out.
	}
	bc.sendTabsResponse(tabs, "t1")

	require.NoError(s.T(), clientWS.SetReadDeadline(time.Now().Add(2*time.Second)))
	var resp browserWSResponse
	err = clientWS.ReadJSON(&resp)
	require.NoError(s.T(), err)
	require.Equal(s.T(), bwsRespTabs, resp.Type)
	require.Len(s.T(), resp.Tabs, 1) // Only t1 (tracked in host mode).
	require.Equal(s.T(), "t1", resp.Tabs[0].TargetID)
}

func (s *BrowserHandlerSuite) TestSendTabsResponseNilCDPMgr() {
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
		conn:            serverConn,
		browserProvider: s.browserMgr,
		logger:          slog.Default(),
		stopCh:          make(chan struct{}),
	}

	tabs := []browser.TabInfo{{TargetID: "t1"}}
	bc.sendTabsResponse(tabs, "t1")

	require.NoError(s.T(), clientWS.SetReadDeadline(time.Now().Add(2*time.Second)))
	var resp browserWSResponse
	err = clientWS.ReadJSON(&resp)
	require.NoError(s.T(), err)
	require.Equal(s.T(), bwsRespTabs, resp.Type)
}

// --- watchMCPTabChanges: switch to different target (calls restartScreencastForTarget) ---

func (s *BrowserHandlerSuite) TestWatchMCPTabChangesSwitchDifferentTarget() {
	mockCDP := new(mockCDPSession)
	mockCDP.On("TargetID").Return("t-old")
	mockCDP.On("SwitchTarget", "t-new").Return(nil)
	mockCDP.On("ResetScreencast").Return()
	frameCh := make(chan []byte, 2)
	mockCDP.On("StartScreencast", 60, 1920, 1080).Return((<-chan []byte)(frameCh))
	mockCDP.On("EvaluateJS", mock.Anything, mock.Anything).Return("", nil)
	mockCDP.On("ListTabs", mock.Anything).Return([]browser.TabInfo(nil), nil)
	mockCDP.On("StopScreencast").Return().Maybe()
	mockCDP.On("Close").Return().Maybe()

	cdpMgr := browser.NewCDPManager("ws://test:9222", browser.CDPManagerConfig{
		MaxRetries: 1,
		RetryDelay: time.Millisecond,
	}, slog.Default())
	adapter := mockCDP
	cdpMgr.SetClientForTarget("t-new", adapter)

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
		conn:            serverConn,
		browserProvider: s.browserMgr,
		logger:          slog.Default(),
		cdpMgr:          cdpMgr,
		cdp:             mockCDP,
		stopCh:          make(chan struct{}),
	}

	done := make(chan struct{})
	go func() {
		bc.watchMCPTabChanges()
		close(done)
	}()

	// Notify switch to a DIFFERENT target.
	cdpMgr.NotifyTargetSwitch("t-new")

	// Wait for the switch to be processed and the tab_switched response to be sent.
	require.NoError(s.T(), clientWS.SetReadDeadline(time.Now().Add(2*time.Second)))
	var resp browserWSResponse
	err = clientWS.ReadJSON(&resp)
	require.NoError(s.T(), err)
	require.Equal(s.T(), bwsRespTabSwitched, resp.Type)
	require.Equal(s.T(), "t-new", resp.TargetID)

	close(bc.stopCh)
	<-done
	close(frameCh)
}

// --- watchMCPTabChanges: switch to same target (skip) ---

func (s *BrowserHandlerSuite) TestWatchMCPTabChangesSwitchSameTarget() {
	mockCDP := new(mockCDPSession)
	mockCDP.On("TargetID").Return("t-current")

	cdpMgr := browser.NewCDPManager("ws://test:9222", browser.CDPManagerConfig{}, slog.Default())

	bc := &browserWSConn{
		logger: slog.Default(),
		cdpMgr: cdpMgr,
		cdp:    mockCDP,
		stopCh: make(chan struct{}),
	}

	done := make(chan struct{})
	go func() {
		bc.watchMCPTabChanges()
		close(done)
	}()

	// Notify switch to same target — should be skipped, then stop.
	cdpMgr.NotifyTargetSwitch("t-current")
	time.Sleep(50 * time.Millisecond)
	close(bc.stopCh)

	select {
	case <-done:
	case <-time.After(time.Second):
		s.T().Fatal("watchMCPTabChanges did not exit")
	}
}

// --- watchMCPTabChanges: nil cdpMgr ---

func (s *BrowserHandlerSuite) TestWatchMCPTabChangesNilCDPMgr() {
	bc := &browserWSConn{
		logger: slog.Default(),
		stopCh: make(chan struct{}),
	}

	done := make(chan struct{})
	go func() {
		bc.watchMCPTabChanges()
		close(done)
	}()

	close(bc.stopCh)
	select {
	case <-done:
	case <-time.After(time.Second):
		s.T().Fatal("watchMCPTabChanges did not exit")
	}
}

// --- handleStart: reuse cached CDP ---

func (s *BrowserHandlerSuite) TestHandleStartReusesCachedCDP() {
	mockCDP := new(mockCDPSession)
	mockCDP.On("TargetID").Return("test-target").Maybe()
	mockCDP.On("ResetScreencast").Return()
	mockCDP.On("ListTabs", mock.Anything).Return([]browser.TabInfo(nil), nil).Maybe()
	mockCDP.On("StopScreencast").Return().Maybe()
	mockCDP.On("Close").Return().Maybe()

	s.browserMgr.On("EnsureBrowser", mock.Anything, "ch-2", "").Return(nil)
	s.browserMgr.On("GetCDPEndpoint", "ch-2").Return("ws://127.0.0.1:9222")

	// Create and pre-connect a CDPManager.
	cdpMgr := browser.NewCDPManager("ws://127.0.0.1:9222", browser.CDPManagerConfig{
		DiscoverExisting: false,
		MaxRetries:       1,
		RetryDelay:       time.Millisecond,
	}, slog.Default())
	browser.SetCDPFactoryForTest(cdpMgr, func(_ context.Context, _ string, _ *slog.Logger, _ ...browser.CDPOption) (browser.CDPSession, error) {
		return mockCDP, nil
	})
	// Connect it so IsConnected() returns true.
	require.NoError(s.T(), cdpMgr.Connect(context.Background()))

	s.srv.cdpManagersMu.Lock()
	if s.srv.cdpManagers == nil {
		s.srv.cdpManagers = make(map[string]*browser.CDPManager)
	}
	s.srv.cdpManagers["ch-2|docker"] = cdpMgr
	s.srv.cdpManagersMu.Unlock()

	ws, ts := s.dialBrowserWS()
	defer ts.Close()
	defer ws.Close()

	require.NoError(s.T(), ws.WriteJSON(browserWSMessage{Type: bwsMsgStart, ChannelID: "ch-2"}))
	resp := s.readResp(ws)
	require.Equal(s.T(), bwsRespStarted, resp.Type)

	mockCDP.AssertCalled(s.T(), "ResetScreencast")
}

// --- handleStart: host mode activates tab ---

func (s *BrowserHandlerSuite) TestHandleStartHostModeActivatesTab() {
	mockCDP := new(mockCDPSession)
	mockCDP.On("TargetID").Return("host-target").Maybe()
	mockCDP.On("SwitchTarget", "host-target").Return(nil)
	mockCDP.On("ListTabs", mock.Anything).Return([]browser.TabInfo(nil), nil).Maybe()
	mockCDP.On("StopScreencast").Return().Maybe()
	mockCDP.On("Close").Return().Maybe()

	hostProvider := new(mockHostBrowserProvider)
	hostProvider.On("EnsureBrowser", mock.Anything, "ch-host", "").Return(nil)
	hostProvider.On("GetCDPEndpoint", "ch-host").Return("ws://127.0.0.1:9222")

	srv := nilServer()
	srv.SetHostBrowserProvider(hostProvider)
	srv.SetBrowserProvider(s.browserMgr)
	srv.containerFinder = s.cFinder

	// Set mode to host for this channel.
	srv.browserModeMu.Lock()
	srv.activeBrowserMode = map[string]string{"ch-host": "host"}
	srv.browserModeMu.Unlock()

	// Create and pre-connect a CDPManager.
	cdpMgr := browser.NewCDPManager("ws://127.0.0.1:9222", browser.CDPManagerConfig{
		DiscoverExisting: false,
		MaxRetries:       1,
		RetryDelay:       time.Millisecond,
	}, slog.Default())
	browser.SetCDPFactoryForTest(cdpMgr, func(_ context.Context, _ string, _ *slog.Logger, _ ...browser.CDPOption) (browser.CDPSession, error) {
		return mockCDP, nil
	})

	srv.cdpManagersMu.Lock()
	srv.cdpManagers = map[string]*browser.CDPManager{"ch-host|host": cdpMgr}
	srv.cdpManagersMu.Unlock()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/ws/browser", srv.handleBrowserWS)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/ws/browser"
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(s.T(), err)
	defer ws.Close()

	require.NoError(s.T(), ws.WriteJSON(browserWSMessage{Type: bwsMsgStart, ChannelID: "ch-host"}))
	var resp browserWSResponse
	err = ws.ReadJSON(&resp)
	require.NoError(s.T(), err)
	require.Equal(s.T(), bwsRespStarted, resp.Type)

	time.Sleep(50 * time.Millisecond)
	mockCDP.AssertCalled(s.T(), "SwitchTarget", "host-target")
}

// --- getBrowserCDP: reuse cached ---

func (s *BrowserHandlerSuite) TestGetBrowserCDPReusesCached() {
	mockCDP := new(mockCDPSession)
	mockCDP.On("TargetID").Return("test-target").Maybe()
	mockCDP.On("EnableConsoleCapture", mock.Anything, mock.Anything).Return(nil).Maybe()
	mockCDP.On("EnableNetworkCapture", mock.Anything, mock.Anything).Return(nil).Maybe()

	cdpMgr := browser.NewCDPManager("ws://127.0.0.1:9222", browser.CDPManagerConfig{
		DiscoverExisting: false,
		MaxRetries:       1,
		RetryDelay:       time.Millisecond,
	}, slog.Default())
	browser.SetCDPFactoryForTest(cdpMgr, func(_ context.Context, _ string, _ *slog.Logger, _ ...browser.CDPOption) (browser.CDPSession, error) {
		return mockCDP, nil
	})
	require.NoError(s.T(), cdpMgr.Connect(context.Background()))

	s.srv.cdpManagersMu.Lock()
	if s.srv.cdpManagers == nil {
		s.srv.cdpManagers = make(map[string]*browser.CDPManager)
	}
	s.srv.cdpManagers["ch-cache|docker"] = cdpMgr
	s.srv.cdpManagersMu.Unlock()

	cdpCl, err := s.srv.getBrowserCDP(context.Background(), "ch-cache")
	require.NoError(s.T(), err)
	require.NotNil(s.T(), cdpCl)
}

func (s *BrowserHandlerSuite) TestGetBrowserCDPConnectWithEmptyTargetID() {
	s.browserMgr.On("EnsureBrowser", mock.Anything, "ch-empty", "").Return(nil)
	s.browserMgr.On("GetCDPEndpoint", "ch-empty").Return("ws://127.0.0.1:9222")

	// Factory returns a client with empty target ID — still usable.
	mockCDP := new(mockCDPSession)
	mockCDP.On("TargetID").Return("")
	mockCDP.On("SwitchTarget", mock.Anything).Return(nil).Maybe()
	mockCDP.On("EnableConsoleCapture", mock.Anything, mock.Anything).Return(nil).Maybe()
	mockCDP.On("EnableNetworkCapture", mock.Anything, mock.Anything).Return(nil).Maybe()

	cdpMgr := browser.NewCDPManager("ws://127.0.0.1:9222", browser.CDPManagerConfig{
		MaxRetries: 1,
		RetryDelay: time.Millisecond,
	}, slog.Default())
	browser.SetCDPFactoryForTest(cdpMgr, func(_ context.Context, _ string, _ *slog.Logger, _ ...browser.CDPOption) (browser.CDPSession, error) {
		return mockCDP, nil
	})

	s.srv.cdpManagersMu.Lock()
	if s.srv.cdpManagers == nil {
		s.srv.cdpManagers = make(map[string]*browser.CDPManager)
	}
	s.srv.cdpManagers["ch-empty|docker"] = cdpMgr
	s.srv.cdpManagersMu.Unlock()

	// Even with empty target ID, Connect sets activeClient, so getBrowserCDP succeeds.
	cdp, err := s.srv.getBrowserCDP(context.Background(), "ch-empty")
	require.NoError(s.T(), err)
	require.NotNil(s.T(), cdp)
}

// --- dispatchBrowserAction additional coverage ---

func (s *BrowserHandlerSuite) TestBrowserActionMouseClickError() {
	mockCDP := new(mockCDPSession)
	s.setupActionMocks(mockCDP)
	mockCDP.On("MouseClick", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("click fail"))

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "mouse_click",
		Params:    map[string]any{"x": float64(10), "y": float64(20)},
	})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Error, "mouse click failed")
}

func (s *BrowserHandlerSuite) TestBrowserActionMouseMove() {
	mockCDP := new(mockCDPSession)
	s.setupActionMocks(mockCDP)
	mockCDP.On("MouseMove", mock.Anything, float64(50), float64(60), 0).Return(nil)

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "mouse_move",
		Params:    map[string]any{"x": float64(50), "y": float64(60)},
	})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Result, "Moved mouse")
}

func (s *BrowserHandlerSuite) TestBrowserActionMouseMoveError() {
	mockCDP := new(mockCDPSession)
	s.setupActionMocks(mockCDP)
	mockCDP.On("MouseMove", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("move fail"))

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "mouse_move",
		Params:    map[string]any{"x": float64(50), "y": float64(60)},
	})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Error, "mouse move failed")
}

func (s *BrowserHandlerSuite) TestBrowserActionMouseScroll() {
	mockCDP := new(mockCDPSession)
	s.setupActionMocks(mockCDP)
	mockCDP.On("MouseScroll", mock.Anything, float64(0), float64(0), float64(0), float64(-100)).Return(nil)

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "mouse_scroll",
		Params:    map[string]any{"delta_y": float64(-100)},
	})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Result, "Scrolled")
}

func (s *BrowserHandlerSuite) TestBrowserActionMouseScrollError() {
	mockCDP := new(mockCDPSession)
	s.setupActionMocks(mockCDP)
	mockCDP.On("MouseScroll", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("scroll fail"))

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "mouse_scroll",
		Params:    map[string]any{"delta_y": float64(-100)},
	})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Error, "mouse scroll failed")
}

func (s *BrowserHandlerSuite) TestBrowserActionMouseDown() {
	mockCDP := new(mockCDPSession)
	s.setupActionMocks(mockCDP)
	mockCDP.On("MouseDown", mock.Anything, float64(10), float64(20), "left").Return(nil)

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "mouse_down",
		Params:    map[string]any{"x": float64(10), "y": float64(20)},
	})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Result, "Mouse down")
}

func (s *BrowserHandlerSuite) TestBrowserActionMouseDownError() {
	mockCDP := new(mockCDPSession)
	s.setupActionMocks(mockCDP)
	mockCDP.On("MouseDown", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("down fail"))

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "mouse_down",
		Params:    map[string]any{"x": float64(10), "y": float64(20)},
	})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Error, "mouse down failed")
}

func (s *BrowserHandlerSuite) TestBrowserActionMouseUp() {
	mockCDP := new(mockCDPSession)
	s.setupActionMocks(mockCDP)
	mockCDP.On("MouseUp", mock.Anything, float64(10), float64(20), "right").Return(nil)

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "mouse_up",
		Params:    map[string]any{"x": float64(10), "y": float64(20), "button": "right"},
	})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Result, "Mouse up")
}

func (s *BrowserHandlerSuite) TestBrowserActionMouseUpError() {
	mockCDP := new(mockCDPSession)
	s.setupActionMocks(mockCDP)
	mockCDP.On("MouseUp", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("up fail"))

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "mouse_up",
		Params:    map[string]any{"x": float64(10), "y": float64(20)},
	})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Error, "mouse up failed")
}

func (s *BrowserHandlerSuite) TestBrowserActionKeyPress() {
	mockCDP := new(mockCDPSession)
	s.setupActionMocks(mockCDP)
	mockCDP.On("KeyPress", mock.Anything, "Enter").Return(nil)

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "key_press",
		Params:    map[string]any{"key": "Enter"},
	})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Result, "Pressed key")
}

func (s *BrowserHandlerSuite) TestBrowserActionKeyPressError() {
	mockCDP := new(mockCDPSession)
	s.setupActionMocks(mockCDP)
	mockCDP.On("KeyPress", mock.Anything, mock.Anything).Return(errors.New("key fail"))

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "key_press",
		Params:    map[string]any{"key": "Escape"},
	})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Error, "key press failed")
}

func (s *BrowserHandlerSuite) TestBrowserActionTypeText() {
	mockCDP := new(mockCDPSession)
	s.setupActionMocks(mockCDP)
	mockCDP.On("TypeText", mock.Anything, "hello").Return(nil)

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "type_text",
		Params:    map[string]any{"text": "hello"},
	})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Result, "Typed")
}

func (s *BrowserHandlerSuite) TestBrowserActionTypeTextError() {
	mockCDP := new(mockCDPSession)
	s.setupActionMocks(mockCDP)
	mockCDP.On("TypeText", mock.Anything, mock.Anything).Return(errors.New("type fail"))

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "type_text",
		Params:    map[string]any{"text": "fail"},
	})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Error, "type text failed")
}

func (s *BrowserHandlerSuite) TestBrowserActionGetPageInfo() {
	mockCDP := new(mockCDPSession)
	s.setupActionMocks(mockCDP)
	mockCDP.On("GetPageInfo", mock.Anything).Return(&browser.PageInfo{URL: "https://example.com", Title: "Test"}, nil)

	w := s.postBrowserAction(browserActionRequest{ChannelID: "ch-1", Action: "get_page_info"})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Empty(s.T(), resp.Error)
	require.NotNil(s.T(), resp.PageInfo)
	require.Equal(s.T(), "https://example.com", resp.PageInfo.URL)
}

func (s *BrowserHandlerSuite) TestBrowserActionClickRef() {
	mockCDP := new(mockCDPSession)
	s.setupActionMocks(mockCDP)
	mockCDP.On("ClickRef", mock.Anything, mock.Anything, 1).Return(nil)

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "click_ref",
		Params: map[string]any{
			"refs":      []any{map[string]any{"ref_id": "ref_1", "x": float64(10), "y": float64(20), "width": float64(100), "height": float64(50)}},
			"ref_index": float64(1),
		},
	})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Result, "Clicked ref 1")
}

func (s *BrowserHandlerSuite) TestBrowserActionClickRefError() {
	mockCDP := new(mockCDPSession)
	s.setupActionMocks(mockCDP)
	mockCDP.On("ClickRef", mock.Anything, mock.Anything, mock.Anything).Return(errors.New("ref fail"))

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "click_ref",
		Params:    map[string]any{"refs": []any{map[string]any{"ref_id": "ref_1"}}, "ref_index": float64(1)},
	})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Error, "click ref failed")
}

func (s *BrowserHandlerSuite) TestBrowserActionScreenshotError() {
	mockCDP := new(mockCDPSession)
	s.setupActionMocks(mockCDP)
	mockCDP.On("Screenshot", mock.Anything).Return(([]byte)(nil), errors.New("screenshot fail"))

	w := s.postBrowserAction(browserActionRequest{ChannelID: "ch-1", Action: "screenshot"})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Error, "screenshot failed")
}

func (s *BrowserHandlerSuite) TestBrowserActionScreenshotToFile() {
	mockCDP := new(mockCDPSession)
	s.setupActionMocks(mockCDP)
	mockCDP.On("Screenshot", mock.Anything).Return([]byte{0x89, 0x50, 0x4E, 0x47}, nil)

	tmpDir := s.T().TempDir()
	s.srv.SetScreenshotDir(tmpDir)
	defer s.srv.SetScreenshotDir("")

	w := s.postBrowserAction(browserActionRequest{ChannelID: "ch-1", Action: "screenshot"})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Empty(s.T(), resp.Error)
	require.NotEmpty(s.T(), resp.ScreenshotPath)
	require.Contains(s.T(), resp.ScreenshotPath, tmpDir)
}

func (s *BrowserHandlerSuite) TestBrowserActionScreenshotToFileWriteError() {
	mockCDP := new(mockCDPSession)
	s.setupActionMocks(mockCDP)
	mockCDP.On("Screenshot", mock.Anything).Return([]byte{0x89, 0x50, 0x4E, 0x47}, nil)

	// Set screenshot dir to a non-existent directory to trigger write error.
	s.srv.SetScreenshotDir("/nonexistent-dir-12345")
	defer s.srv.SetScreenshotDir("")

	w := s.postBrowserAction(browserActionRequest{ChannelID: "ch-1", Action: "screenshot"})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Error, "writing screenshot file")
}

func (s *BrowserHandlerSuite) TestBrowserActionEvaluateJS() {
	mockCDP := new(mockCDPSession)
	s.setupActionMocks(mockCDP)
	mockCDP.On("EvaluateJS", mock.Anything, "1+1").Return("2", nil)

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "evaluate_js",
		Params:    map[string]any{"expression": "1+1"},
	})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(s.T(), "2", resp.Result)
}

func (s *BrowserHandlerSuite) TestBrowserActionEvaluateJSError() {
	mockCDP := new(mockCDPSession)
	s.setupActionMocks(mockCDP)
	mockCDP.On("EvaluateJS", mock.Anything, mock.Anything).Return("", errors.New("js fail"))

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "evaluate_js",
		Params:    map[string]any{"expression": "bad()"},
	})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Error, "evaluate JS failed")
}

func (s *BrowserHandlerSuite) TestBrowserActionListTabsError() {
	mockCDP := new(mockCDPSession)
	s.setupActionMocks(mockCDP)
	mockCDP.On("ListTabs", mock.Anything).Return(([]browser.TabInfo)(nil), errors.New("tabs fail"))

	w := s.postBrowserAction(browserActionRequest{ChannelID: "ch-1", Action: "list_tabs"})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Error, "list tabs failed")
}

func (s *BrowserHandlerSuite) TestBrowserActionNewTabError() {
	mockCDP := new(mockCDPSession)
	s.setupActionMocks(mockCDP)
	mockCDP.On("NewTab", mock.Anything, mock.Anything).Return("", errors.New("new tab fail"))

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "new_tab",
		Params:    map[string]any{"url": "https://example.com"},
	})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Error, "new tab failed")
}

func (s *BrowserHandlerSuite) TestBrowserActionSwitchTab() {
	mockCDP := new(mockCDPSession)
	s.setupActionMocks(mockCDP)

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "switch_tab",
		Params:    map[string]any{"target_id": "t-switch"},
	})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Result, "Switched to tab t-switch")
}

func (s *BrowserHandlerSuite) TestBrowserActionSwitchTabNoTargetID() {
	mockCDP := new(mockCDPSession)
	s.setupActionMocks(mockCDP)

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "switch_tab",
	})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Error, "target_id required")
}

func (s *BrowserHandlerSuite) TestBrowserActionCloseTabNoTargetID() {
	mockCDP := new(mockCDPSession)
	s.setupActionMocks(mockCDP)

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "close_tab",
	})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Error, "target_id required")
}

func (s *BrowserHandlerSuite) TestBrowserActionCloseTabError() {
	mockCDP := new(mockCDPSession)
	s.setupActionMocks(mockCDP)
	// NewTab called before CloseTab (replacement for last tab).
	mockCDP.On("NewTab", mock.Anything, "about:blank").Return("t-new", nil).Maybe()
	mockCDP.On("CloseTab", mock.Anything, "t1").Return(errors.New("close fail"))

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "close_tab",
		Params:    map[string]any{"target_id": "t1"},
	})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Error, "close tab failed")
}

func (s *BrowserHandlerSuite) TestBrowserActionCloseTabWithNextTab() {
	mockCDP := new(mockCDPSession)
	s.setupActionMocks(mockCDP)
	mockCDP.On("CloseTab", mock.Anything, "t2").Return(nil)

	// Pre-populate the CDPManager with tracked tabs so NextTabID returns a valid next tab.
	s.srv.cdpManagersMu.Lock()
	cdpMgr := s.srv.cdpManagers["ch-1|docker"]
	s.srv.cdpManagersMu.Unlock()
	cdpMgr.TrackTab("t1")
	cdpMgr.TrackTab("t2")
	cdpMgr.TrackTab("t3")

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "close_tab",
		Params:    map[string]any{"target_id": "t2"},
	})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Result, "Closed tab t2")
}

func (s *BrowserHandlerSuite) TestBrowserActionResizeWindow() {
	mockCDP := new(mockCDPSession)
	s.setupActionMocks(mockCDP)
	mockCDP.On("ResizeWindow", mock.Anything, 1024, 768).Return(nil)

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "resize_window",
		Params:    map[string]any{"width": float64(1024), "height": float64(768)},
	})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Result, "Resized viewport")
}

func (s *BrowserHandlerSuite) TestBrowserActionResizeWindowError() {
	mockCDP := new(mockCDPSession)
	s.setupActionMocks(mockCDP)
	mockCDP.On("ResizeWindow", mock.Anything, mock.Anything, mock.Anything).Return(errors.New("resize fail"))

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "resize_window",
		Params:    map[string]any{"width": float64(1024), "height": float64(768)},
	})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Error, "resize window failed")
}

func (s *BrowserHandlerSuite) TestBrowserActionScrollIntoView() {
	mockCDP := new(mockCDPSession)
	s.setupActionMocks(mockCDP)
	mockCDP.On("ScrollIntoView", mock.Anything, cdp.BackendNodeID(42)).Return(nil)

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "scroll_into_view",
		Params:    map[string]any{"backend_node_id": float64(42)},
	})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Result, "Scrolled element into view")
}

func (s *BrowserHandlerSuite) TestBrowserActionScrollIntoViewError() {
	mockCDP := new(mockCDPSession)
	s.setupActionMocks(mockCDP)
	mockCDP.On("ScrollIntoView", mock.Anything, mock.Anything).Return(errors.New("scroll fail"))

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "scroll_into_view",
		Params:    map[string]any{"backend_node_id": float64(42)},
	})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Error, "scroll into view failed")
}

// --- readConsoleMessages additional coverage ---

func (s *BrowserHandlerSuite) TestReadConsoleMessagesNilCapture() {
	// Call readConsoleMessages directly (not through handleBrowserAction)
	// to test the cs == nil path. handleBrowserAction always calls
	// ensureBrowserCapture first, so cs is never nil through the normal flow.
	resp := s.srv.readConsoleMessages("no-capture-channel", nil)
	require.Contains(s.T(), resp.Result, "No console messages")
}

func (s *BrowserHandlerSuite) TestReadConsoleMessagesWithFilter() {
	mockCDP := new(mockCDPSession)
	s.setupActionMocks(mockCDP)

	s.srv.browserCapturesMu.Lock()
	if s.srv.browserCaptures == nil {
		s.srv.browserCaptures = make(map[string]*browser.CaptureState)
	}
	cs := &browser.CaptureState{Started: true}
	cs.ConsoleMsgs = []browser.ConsoleMessage{
		{Level: "log", Text: "hello world", Time: time.Now()},
		{Level: "error", Text: "critical error", Time: time.Now()},
		{Level: "log", Text: "other msg", Time: time.Now()},
	}
	s.srv.browserCaptures["ch-1"] = cs
	s.srv.browserCapturesMu.Unlock()

	// Test pattern filter.
	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "read_console",
		Params:    map[string]any{"pattern": "critical"},
	})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Result, "1 console message")
	require.Contains(s.T(), resp.Result, "critical error")
}

func (s *BrowserHandlerSuite) TestReadConsoleMessagesOnlyErrors() {
	mockCDP := new(mockCDPSession)
	s.setupActionMocks(mockCDP)

	s.srv.browserCapturesMu.Lock()
	if s.srv.browserCaptures == nil {
		s.srv.browserCaptures = make(map[string]*browser.CaptureState)
	}
	cs := &browser.CaptureState{Started: true}
	cs.ConsoleMsgs = []browser.ConsoleMessage{
		{Level: "log", Text: "info msg", Time: time.Now()},
		{Level: "error", Text: "err msg", Time: time.Now()},
	}
	s.srv.browserCaptures["ch-1"] = cs
	s.srv.browserCapturesMu.Unlock()

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "read_console",
		Params:    map[string]any{"only_errors": true},
	})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Result, "1 console message")
	require.Contains(s.T(), resp.Result, "err msg")
}

func (s *BrowserHandlerSuite) TestReadConsoleMessagesClear() {
	mockCDP := new(mockCDPSession)
	s.setupActionMocks(mockCDP)

	s.srv.browserCapturesMu.Lock()
	if s.srv.browserCaptures == nil {
		s.srv.browserCaptures = make(map[string]*browser.CaptureState)
	}
	cs := &browser.CaptureState{Started: true}
	cs.ConsoleMsgs = []browser.ConsoleMessage{
		{Level: "log", Text: "msg", Time: time.Now()},
	}
	s.srv.browserCaptures["ch-1"] = cs
	s.srv.browserCapturesMu.Unlock()

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "read_console",
		Params:    map[string]any{"clear": true},
	})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Result, "1 console message")

	// After clear, messages should be empty.
	cs.ConsoleMu.Lock()
	require.Nil(s.T(), cs.ConsoleMsgs)
	cs.ConsoleMu.Unlock()
}

func (s *BrowserHandlerSuite) TestReadConsoleMessagesInvalidRegex() {
	mockCDP := new(mockCDPSession)
	s.setupActionMocks(mockCDP)

	s.srv.browserCapturesMu.Lock()
	if s.srv.browserCaptures == nil {
		s.srv.browserCaptures = make(map[string]*browser.CaptureState)
	}
	s.srv.browserCaptures["ch-1"] = &browser.CaptureState{Started: true}
	s.srv.browserCapturesMu.Unlock()

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "read_console",
		Params:    map[string]any{"pattern": "[invalid"},
	})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Error, "invalid regex pattern")
}

func (s *BrowserHandlerSuite) TestReadConsoleMessagesLimitExceeded() {
	mockCDP := new(mockCDPSession)
	s.setupActionMocks(mockCDP)

	msgs := make([]browser.ConsoleMessage, 5)
	for i := range msgs {
		msgs[i] = browser.ConsoleMessage{Level: "log", Text: fmt.Sprintf("msg-%d", i), Time: time.Now()}
	}
	s.srv.browserCapturesMu.Lock()
	if s.srv.browserCaptures == nil {
		s.srv.browserCaptures = make(map[string]*browser.CaptureState)
	}
	cs := &browser.CaptureState{Started: true, ConsoleMsgs: msgs}
	s.srv.browserCaptures["ch-1"] = cs
	s.srv.browserCapturesMu.Unlock()

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "read_console",
		Params:    map[string]any{"limit": float64(2)},
	})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Result, "2 console message")
}

func (s *BrowserHandlerSuite) TestReadConsoleMessagesEmpty() {
	mockCDP := new(mockCDPSession)
	s.setupActionMocks(mockCDP)

	s.srv.browserCapturesMu.Lock()
	if s.srv.browserCaptures == nil {
		s.srv.browserCaptures = make(map[string]*browser.CaptureState)
	}
	s.srv.browserCaptures["ch-1"] = &browser.CaptureState{Started: true}
	s.srv.browserCapturesMu.Unlock()

	w := s.postBrowserAction(browserActionRequest{ChannelID: "ch-1", Action: "read_console"})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Result, "No console messages")
}

// --- readNetworkRequests additional coverage ---

func (s *BrowserHandlerSuite) TestReadNetworkRequestsNilCapture() {
	resp := s.srv.readNetworkRequests("no-capture-channel", nil)
	require.Contains(s.T(), resp.Result, "No network requests")
}

func (s *BrowserHandlerSuite) TestReadNetworkRequestsWithFilter() {
	mockCDP := new(mockCDPSession)
	s.setupActionMocks(mockCDP)

	s.srv.browserCapturesMu.Lock()
	if s.srv.browserCaptures == nil {
		s.srv.browserCaptures = make(map[string]*browser.CaptureState)
	}
	cs := &browser.CaptureState{Started: true}
	cs.NetworkReqs = []browser.NetworkRequest{
		{URL: "https://api.example.com/v1", Method: "GET", Status: 200, StatusText: "OK", Time: time.Now()},
		{URL: "https://cdn.example.com/asset.js", Method: "GET", Status: 200, StatusText: "OK", Time: time.Now()},
	}
	s.srv.browserCaptures["ch-1"] = cs
	s.srv.browserCapturesMu.Unlock()

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "read_network",
		Params:    map[string]any{"pattern": "api\\.example"},
	})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Result, "1 network request")
}

func (s *BrowserHandlerSuite) TestReadNetworkRequestsClear() {
	mockCDP := new(mockCDPSession)
	s.setupActionMocks(mockCDP)

	s.srv.browserCapturesMu.Lock()
	if s.srv.browserCaptures == nil {
		s.srv.browserCaptures = make(map[string]*browser.CaptureState)
	}
	cs := &browser.CaptureState{Started: true}
	cs.NetworkReqs = []browser.NetworkRequest{
		{URL: "https://a.com", Method: "GET", Status: 200, StatusText: "OK", Time: time.Now()},
	}
	s.srv.browserCaptures["ch-1"] = cs
	s.srv.browserCapturesMu.Unlock()

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "read_network",
		Params:    map[string]any{"clear": true},
	})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Result, "1 network request")

	cs.NetworkMu.Lock()
	require.Nil(s.T(), cs.NetworkReqs)
	cs.NetworkMu.Unlock()
}

func (s *BrowserHandlerSuite) TestReadNetworkRequestsInvalidRegex() {
	mockCDP := new(mockCDPSession)
	s.setupActionMocks(mockCDP)

	s.srv.browserCapturesMu.Lock()
	if s.srv.browserCaptures == nil {
		s.srv.browserCaptures = make(map[string]*browser.CaptureState)
	}
	s.srv.browserCaptures["ch-1"] = &browser.CaptureState{Started: true}
	s.srv.browserCapturesMu.Unlock()

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "read_network",
		Params:    map[string]any{"pattern": "[invalid"},
	})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Error, "invalid regex pattern")
}

func (s *BrowserHandlerSuite) TestReadNetworkRequestsLimitExceeded() {
	mockCDP := new(mockCDPSession)
	s.setupActionMocks(mockCDP)

	reqs := make([]browser.NetworkRequest, 5)
	for i := range reqs {
		reqs[i] = browser.NetworkRequest{URL: fmt.Sprintf("https://req%d.com", i), Method: "GET", Status: 200, StatusText: "OK", Time: time.Now()}
	}
	s.srv.browserCapturesMu.Lock()
	if s.srv.browserCaptures == nil {
		s.srv.browserCaptures = make(map[string]*browser.CaptureState)
	}
	cs := &browser.CaptureState{Started: true, NetworkReqs: reqs}
	s.srv.browserCaptures["ch-1"] = cs
	s.srv.browserCapturesMu.Unlock()

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "read_network",
		Params:    map[string]any{"limit": float64(2)},
	})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Result, "2 network request")
}

func (s *BrowserHandlerSuite) TestReadNetworkRequestsEmpty() {
	mockCDP := new(mockCDPSession)
	s.setupActionMocks(mockCDP)

	s.srv.browserCapturesMu.Lock()
	if s.srv.browserCaptures == nil {
		s.srv.browserCaptures = make(map[string]*browser.CaptureState)
	}
	s.srv.browserCaptures["ch-1"] = &browser.CaptureState{Started: true}
	s.srv.browserCapturesMu.Unlock()

	w := s.postBrowserAction(browserActionRequest{ChannelID: "ch-1", Action: "read_network"})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Result, "No network requests")
}

// --- RunBrowserIdleMonitor: ticker fires ---

func (s *BrowserHandlerSuite) TestRunBrowserIdleMonitorTickerFires() {
	ctx, cancel := context.WithCancel(context.Background())

	// Set up an idle CDPManager.
	cdpMgr := browser.NewCDPManager("ws://test:9222", browser.CDPManagerConfig{}, slog.Default())
	browser.SetTimeNowForTest(cdpMgr, func() time.Time {
		return time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	})
	cdpMgr.Touch()

	s.srv.cdpManagersMu.Lock()
	if s.srv.cdpManagers == nil {
		s.srv.cdpManagers = make(map[string]*browser.CDPManager)
	}
	s.srv.cdpManagers["ch-idle|docker"] = cdpMgr
	s.srv.cdpManagersMu.Unlock()

	s.browserMgr.On("StopBrowser", mock.Anything, "ch-idle").Return(nil)

	done := make(chan struct{})
	go func() {
		// Use very short ticker interval so it fires within the test.
		s.srv.runBrowserIdleMonitorWithInterval(ctx, time.Nanosecond, time.Millisecond)
		close(done)
	}()

	// Wait for the monitor to process, then cancel.
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		s.T().Fatal("RunBrowserIdleMonitor did not exit")
	}
}

// --- cleanIdleBrowserSessions: host mode cleanup ---

func (s *BrowserHandlerSuite) TestCleanIdleBrowserSessionsHostMode() {
	cdpMgr := browser.NewCDPManager("ws://test:9222", browser.CDPManagerConfig{}, slog.Default())
	browser.SetTimeNowForTest(cdpMgr, func() time.Time {
		return time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	})
	cdpMgr.Touch()

	s.srv.cdpManagersMu.Lock()
	if s.srv.cdpManagers == nil {
		s.srv.cdpManagers = make(map[string]*browser.CDPManager)
	}
	s.srv.cdpManagers["ch-1|host"] = cdpMgr
	s.srv.cdpManagersMu.Unlock()

	// Host mode — should NOT call StopBrowser.
	s.srv.cleanIdleBrowserSessions(context.Background(), time.Minute)

	s.srv.cdpManagersMu.Lock()
	_, exists := s.srv.cdpManagers["ch-1|host"]
	s.srv.cdpManagersMu.Unlock()
	require.False(s.T(), exists)
}

// --- sendJSON: broken pipe silent ---

func (s *BrowserHandlerSuite) TestSendJSONBrokenPipe() {
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
	clientWS.Close()
	time.Sleep(20 * time.Millisecond)

	bc := &browserWSConn{
		conn:   serverConn,
		logger: slog.Default(),
		stopCh: make(chan struct{}),
	}
	// Should not panic — broken pipe is silently ignored.
	bc.sendJSON(browserWSResponse{Type: bwsRespError, Message: "test"})
}

// --- list_tabs with host mode filtering ---

func (s *BrowserHandlerSuite) TestBrowserActionListTabsHostMode() {
	mockCDP := new(mockCDPSession)
	mockCDP.On("TargetID").Return("test-target").Maybe()
	mockCDP.On("EnableConsoleCapture", mock.Anything, mock.Anything).Return(nil).Maybe()
	mockCDP.On("EnableNetworkCapture", mock.Anything, mock.Anything).Return(nil).Maybe()
	mockCDP.On("ListTabs", mock.Anything).Return([]browser.TabInfo{
		{TargetID: "t1", URL: "https://a.com"},
		{TargetID: "t2", URL: "https://b.com"},
	}, nil)

	hostProvider := new(mockHostBrowserProvider)
	hostProvider.On("EnsureBrowser", mock.Anything, "ch-host", "").Return(nil).Maybe()
	hostProvider.On("GetCDPEndpoint", "ch-host").Return("ws://127.0.0.1:9222").Maybe()

	srv := nilServer()
	srv.SetBrowserProvider(s.browserMgr)
	srv.SetHostBrowserProvider(hostProvider)
	srv.containerFinder = s.cFinder

	srv.browserModeMu.Lock()
	srv.activeBrowserMode = map[string]string{"ch-host": "host"}
	srv.browserModeMu.Unlock()

	cdpMgr := browser.NewCDPManager("ws://127.0.0.1:9222", browser.CDPManagerConfig{
		DiscoverExisting: false,
		MaxRetries:       1,
		RetryDelay:       time.Millisecond,
	}, slog.Default())
	browser.SetCDPFactoryForTest(cdpMgr, func(_ context.Context, _ string, _ *slog.Logger, _ ...browser.CDPOption) (browser.CDPSession, error) {
		return mockCDP, nil
	})
	require.NoError(s.T(), cdpMgr.Connect(context.Background()))
	cdpMgr.TrackTab("t1") // Only track t1.

	srv.cdpManagersMu.Lock()
	srv.cdpManagers = map[string]*browser.CDPManager{"ch-host|host": cdpMgr}
	srv.cdpManagersMu.Unlock()

	data, _ := json.Marshal(browserActionRequest{ChannelID: "ch-host", Action: "list_tabs"})
	r := httptest.NewRequest(http.MethodPost, "/api/browser/action", strings.NewReader(string(data)))
	w := httptest.NewRecorder()
	srv.handleBrowserAction(w, r)

	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Empty(s.T(), resp.Error)
	require.Len(s.T(), resp.Tabs, 1) // Only t1 (tracked).
	require.Equal(s.T(), "t1", resp.Tabs[0].TargetID)
}

// --- handleStart with Mode field (covers setMode callback) ---

func (s *BrowserHandlerSuite) TestHandleStartWithModeDocker() {
	mockCDP := new(mockCDPSession)
	mockCDP.On("TargetID").Return("test-target").Maybe()
	mockCDP.On("SwitchTarget", mock.Anything).Return(nil).Maybe()
	mockCDP.On("ListTabs", mock.Anything).Return([]browser.TabInfo(nil), nil).Maybe()
	mockCDP.On("StopScreencast").Return().Maybe()
	mockCDP.On("Close").Return().Maybe()

	s.browserMgr.On("EnsureBrowser", mock.Anything, "ch-mode", "").Return(nil)
	s.browserMgr.On("GetCDPEndpoint", "ch-mode").Return("ws://127.0.0.1:9222")

	cdpMgr := browser.NewCDPManager("ws://127.0.0.1:9222", browser.CDPManagerConfig{
		DiscoverExisting: false,
		MaxRetries:       1,
		RetryDelay:       time.Millisecond,
	}, slog.Default())
	browser.SetCDPFactoryForTest(cdpMgr, func(_ context.Context, _ string, _ *slog.Logger, _ ...browser.CDPOption) (browser.CDPSession, error) {
		return mockCDP, nil
	})

	s.srv.cdpManagersMu.Lock()
	if s.srv.cdpManagers == nil {
		s.srv.cdpManagers = make(map[string]*browser.CDPManager)
	}
	s.srv.cdpManagers["ch-mode|docker"] = cdpMgr
	s.srv.cdpManagersMu.Unlock()

	ws, ts := s.dialBrowserWS()
	defer ts.Close()
	defer ws.Close()

	// Send start with Mode set — this exercises the setMode callback.
	require.NoError(s.T(), ws.WriteJSON(browserWSMessage{
		Type:      bwsMsgStart,
		ChannelID: "ch-mode",
		Mode:      "docker",
	}))
	resp := s.readResp(ws)
	require.Equal(s.T(), bwsRespStarted, resp.Type)

	// Verify mode was set.
	s.srv.browserModeMu.Lock()
	require.Equal(s.T(), "docker", s.srv.activeBrowserMode["ch-mode"])
	s.srv.browserModeMu.Unlock()
}

// --- list_tabs: active tab marking ---

func (s *BrowserHandlerSuite) TestBrowserActionListTabsWithActiveMarking() {
	mockCDP := new(mockCDPSession)
	s.setupActionMocks(mockCDP)

	s.srv.cdpManagersMu.Lock()
	cdpMgr := s.srv.cdpManagers["ch-1|docker"]
	s.srv.cdpManagersMu.Unlock()
	cdpMgr.TrackTab("test-target")

	mockCDP.On("ListTabs", mock.Anything).Return([]browser.TabInfo{
		{TargetID: "test-target", URL: "https://a.com", Title: "A"},
	}, nil)

	w := s.postBrowserAction(browserActionRequest{ChannelID: "ch-1", Action: "list_tabs"})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Empty(s.T(), resp.Error)
	require.Len(s.T(), resp.Tabs, 1)
	require.Equal(s.T(), "test-target", resp.Tabs[0].TargetID)
	require.True(s.T(), resp.Tabs[0].Active)
}

// --- new_tab: cdpMgr tracking ---

func (s *BrowserHandlerSuite) TestBrowserActionNewTabWithCDPMgrTracking() {
	mockCDP := new(mockCDPSession)
	s.setupActionMocks(mockCDP)

	mockCDP.On("NewTab", mock.Anything, "https://example.com").Return("new-t-id", nil)

	w := s.postBrowserAction(browserActionRequest{
		ChannelID: "ch-1",
		Action:    "new_tab",
		Params:    map[string]any{"url": "https://example.com"},
	})

	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Empty(s.T(), resp.Error)
	require.Contains(s.T(), resp.Result, "new-t-id")

	// Verify the CDPManager tracked the new tab.
	s.srv.cdpManagersMu.Lock()
	cdpMgr := s.srv.cdpManagers["ch-1|docker"]
	s.srv.cdpManagersMu.Unlock()
	require.True(s.T(), cdpMgr.IsTrackedTab("new-t-id"))
}
