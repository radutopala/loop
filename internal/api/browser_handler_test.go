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

	"github.com/gorilla/websocket"
	"github.com/radutopala/loop/internal/browser"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
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

func (m *MockBrowserManager) SetCDP(channelID string, cdp any) {
	m.Called(channelID, cdp)
}

func (m *MockBrowserManager) GetCDP(channelID string) any {
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

func (s *BrowserHandlerSuite) TestEnsureBrowserSuccess() {
	s.browserMgr.On("EnsureBrowser", mock.Anything, "ch-1", "").Return(nil)

	body := strings.NewReader(`{"channel_id":"ch-1"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/browser/ensure", body)
	w := httptest.NewRecorder()
	s.srv.handleEnsureBrowser(w, req)

	require.Equal(s.T(), http.StatusOK, w.Code)
	s.browserMgr.AssertExpectations(s.T())
}

func (s *BrowserHandlerSuite) TestEnsureBrowserNoBrowserManager() {
	srv := nilServer() // no browser manager

	body := strings.NewReader(`{"channel_id":"ch-1"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/browser/ensure", body)
	w := httptest.NewRecorder()
	srv.handleEnsureBrowser(w, req)

	require.Equal(s.T(), http.StatusServiceUnavailable, w.Code)
}

func (s *BrowserHandlerSuite) TestEnsureBrowserMissingChannelID() {
	body := strings.NewReader(`{}`)
	req := httptest.NewRequest(http.MethodPost, "/api/browser/ensure", body)
	w := httptest.NewRecorder()
	s.srv.handleEnsureBrowser(w, req)

	require.Equal(s.T(), http.StatusBadRequest, w.Code)
}

func (s *BrowserHandlerSuite) TestEnsureBrowserInvalidJSON() {
	body := strings.NewReader(`not json`)
	req := httptest.NewRequest(http.MethodPost, "/api/browser/ensure", body)
	w := httptest.NewRecorder()
	s.srv.handleEnsureBrowser(w, req)

	require.Equal(s.T(), http.StatusBadRequest, w.Code)
}

func (s *BrowserHandlerSuite) TestTouchBrowserSuccess() {
	s.browserMgr.On("TouchBrowser", "ch-1").Return()

	body := strings.NewReader(`{"channel_id":"ch-1"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/browser/touch", body)
	w := httptest.NewRecorder()
	s.srv.handleTouchBrowser(w, req)

	require.Equal(s.T(), http.StatusOK, w.Code)
	s.browserMgr.AssertExpectations(s.T())
}

func (s *BrowserHandlerSuite) TestTouchBrowserNoBrowserManager() {
	srv := nilServer() // no browser manager

	body := strings.NewReader(`{"channel_id":"ch-1"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/browser/touch", body)
	w := httptest.NewRecorder()
	srv.handleTouchBrowser(w, req)

	require.Equal(s.T(), http.StatusServiceUnavailable, w.Code)
}

func (s *BrowserHandlerSuite) TestTouchBrowserMissingChannelID() {
	body := strings.NewReader(`{}`)
	req := httptest.NewRequest(http.MethodPost, "/api/browser/touch", body)
	w := httptest.NewRecorder()
	s.srv.handleTouchBrowser(w, req)

	require.Equal(s.T(), http.StatusBadRequest, w.Code)
}

func (s *BrowserHandlerSuite) TestTouchBrowserInvalidJSON() {
	body := strings.NewReader(`not json`)
	req := httptest.NewRequest(http.MethodPost, "/api/browser/touch", body)
	w := httptest.NewRecorder()
	s.srv.handleTouchBrowser(w, req)

	require.Equal(s.T(), http.StatusBadRequest, w.Code)
}

func (s *BrowserHandlerSuite) TestEnsureBrowserError() {
	s.browserMgr.On("EnsureBrowser", mock.Anything, "ch-2", "").Return(errors.New("chrome failed"))

	body := strings.NewReader(`{"channel_id":"ch-2"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/browser/ensure", body)
	w := httptest.NewRecorder()
	s.srv.handleEnsureBrowser(w, req)

	require.Equal(s.T(), http.StatusInternalServerError, w.Code)
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

func (s *BrowserHandlerSuite) TestNavigateNoBrowser() {
	ws, ts := s.dialBrowserWS()
	defer ts.Close()
	defer ws.Close()

	err := ws.WriteJSON(browserWSMessage{Type: bwsMsgNavigate, URL: "https://example.com"})
	require.NoError(s.T(), err)

	resp := s.readResp(ws)
	require.Equal(s.T(), bwsRespError, resp.Type)
	require.Contains(s.T(), resp.Message, "browser not started")
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

func (s *BrowserHandlerSuite) TestPageInfoNoBrowser() {
	ws, ts := s.dialBrowserWS()
	defer ts.Close()
	defer ws.Close()

	err := ws.WriteJSON(browserWSMessage{Type: bwsMsgPageInfo})
	require.NoError(s.T(), err)

	resp := s.readResp(ws)
	require.Equal(s.T(), bwsRespError, resp.Type)
	require.Contains(s.T(), resp.Message, "browser not started")
}

func (s *BrowserHandlerSuite) TestReloadNoBrowser() {
	ws, ts := s.dialBrowserWS()
	defer ts.Close()
	defer ws.Close()

	err := ws.WriteJSON(browserWSMessage{Type: bwsMsgReload})
	require.NoError(s.T(), err)

	resp := s.readResp(ws)
	require.Equal(s.T(), bwsRespError, resp.Type)
	require.Contains(s.T(), resp.Message, "browser not started")
}

func (s *BrowserHandlerSuite) TestBackNoBrowser() {
	ws, ts := s.dialBrowserWS()
	defer ts.Close()
	defer ws.Close()

	err := ws.WriteJSON(browserWSMessage{Type: bwsMsgBack})
	require.NoError(s.T(), err)

	resp := s.readResp(ws)
	require.Equal(s.T(), bwsRespError, resp.Type)
	require.Contains(s.T(), resp.Message, "browser not started")
}

func (s *BrowserHandlerSuite) TestForwardNoBrowser() {
	ws, ts := s.dialBrowserWS()
	defer ts.Close()
	defer ws.Close()

	err := ws.WriteJSON(browserWSMessage{Type: bwsMsgForward})
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

func (s *BrowserHandlerSuite) TestBrowserWSResponseJSON() {
	resp := browserWSResponse{
		Type:    bwsRespPageInfo,
		URL:     "https://example.com",
		Title:   "Example",
		Message: "",
	}

	data, err := json.Marshal(resp)
	require.NoError(s.T(), err)

	var decoded browserWSResponse
	require.NoError(s.T(), json.Unmarshal(data, &decoded))
	require.Equal(s.T(), resp, decoded)
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
func (m *MockCDPClient) StopScreencast() { m.Called() }
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
func (m *MockCDPClient) Close()           { m.Called() }

// --- Helper: start browser and get WS with CDP mock ---

func (s *BrowserHandlerSuite) startBrowserWS() (*websocket.Conn, *httptest.Server, *MockCDPClient) {
	mockCDP := new(MockCDPClient)
	s.srv.browserCDPFactory = func(_ context.Context, _ string, _ *slog.Logger) (browserCDPClient, error) {
		return mockCDP, nil
	}

	s.browserMgr.On("EnsureBrowser", mock.Anything, "ch-1", "").Return(nil)
	s.browserMgr.On("GetCDP", "ch-1").Return(nil)
	s.browserMgr.On("GetCDPEndpoint", "ch-1").Return("ws://127.0.0.1:9222")
	s.browserMgr.On("SetCDP", "ch-1", mock.Anything).Return().Maybe()
	s.browserMgr.On("SetTargetID", "ch-1", mock.Anything).Return().Maybe()

	mockCDP.On("TargetID").Return("").Maybe()

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
	mockCDP.On("StopScreencast").Return().Maybe()
	mockCDP.On("Close").Return().Maybe()
	mockCDP.On("TargetID").Return("cached-target").Maybe()

	s.browserMgr.On("EnsureBrowser", mock.Anything, "ch-1", "").Return(nil)
	// GetCDP returns the cached mock client.
	s.browserMgr.On("GetCDP", "ch-1").Return(mockCDP)

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
	s.srv.browserCDPFactory = func(_ context.Context, _ string, _ *slog.Logger) (browserCDPClient, error) {
		return mockCDP, nil
	}

	s.browserMgr.On("EnsureBrowser", mock.Anything, "ch-1", "").Return(nil)
	s.browserMgr.On("GetCDP", "ch-1").Return(nil)
	s.browserMgr.On("GetCDPEndpoint", "ch-1").Return("ws://127.0.0.1:9222")
	s.browserMgr.On("SetCDP", "ch-1", mock.Anything).Return().Maybe()
	s.browserMgr.On("SetTargetID", "ch-1", "my-target-id").Return().Maybe()

	mockCDP.On("TargetID").Return("my-target-id")
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
	s.srv.browserCDPFactory = func(_ context.Context, _ string, _ *slog.Logger) (browserCDPClient, error) {
		return nil, errors.New("cdp connect failed")
	}

	s.browserMgr.On("EnsureBrowser", mock.Anything, "ch-1", "").Return(nil)
	s.browserMgr.On("GetCDP", "ch-1").Return(nil)
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
	s.srv.browserCDPFactory = func(_ context.Context, _ string, _ *slog.Logger) (browserCDPClient, error) {
		cancel() // Cancel context on first attempt so the retry loop exits.
		return nil, errors.New("not ready")
	}
	s.srv.browserCDPRetries = 3
	s.srv.browserCDPDelay = time.Second // Would sleep, but context is cancelled.

	s.browserMgr.On("EnsureBrowser", mock.Anything, "ch-1", "").Return(nil)
	s.browserMgr.On("GetCDP", "ch-1").Return(nil)
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
	s.srv.browserCDPFactory = func(_ context.Context, _ string, _ *slog.Logger) (browserCDPClient, error) {
		attempt++
		if attempt == 1 {
			return nil, errors.New("not ready")
		}
		return mockCDP, nil
	}
	s.srv.browserCDPRetries = 3
	s.srv.browserCDPDelay = time.Millisecond

	s.browserMgr.On("EnsureBrowser", mock.Anything, "ch-1", "").Return(nil)
	s.browserMgr.On("GetCDP", "ch-1").Return(nil)
	s.browserMgr.On("GetCDPEndpoint", "ch-1").Return("ws://172.17.0.2:9222")
	s.browserMgr.On("SetCDP", "ch-1", mock.Anything).Return().Maybe()
	s.browserMgr.On("SetTargetID", "ch-1", mock.Anything).Return().Maybe()

	mockCDP.On("TargetID").Return("").Maybe()

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

// --- Navigate success ---

func (s *BrowserHandlerSuite) TestNavigateSuccess() {
	ws, ts, mockCDP := s.startBrowserWS()
	defer ts.Close()
	defer ws.Close()

	mockCDP.On("Navigate", mock.Anything, "https://example.com").Return(nil)
	mockCDP.On("GetPageInfo", mock.Anything).Return(&browser.PageInfo{URL: "https://example.com", Title: "Example"}, nil)

	require.NoError(s.T(), ws.WriteJSON(browserWSMessage{Type: bwsMsgNavigate, URL: "https://example.com"}))
	resp := s.readResp(ws)
	require.Equal(s.T(), bwsRespPageInfo, resp.Type)
	require.Equal(s.T(), "https://example.com", resp.URL)
	require.Equal(s.T(), "Example", resp.Title)
}

func (s *BrowserHandlerSuite) TestNavigateError() {
	ws, ts, mockCDP := s.startBrowserWS()
	defer ts.Close()
	defer ws.Close()

	mockCDP.On("Navigate", mock.Anything, "https://bad.com").Return(errors.New("nav fail"))

	require.NoError(s.T(), ws.WriteJSON(browserWSMessage{Type: bwsMsgNavigate, URL: "https://bad.com"}))
	resp := s.readResp(ws)
	require.Equal(s.T(), bwsRespError, resp.Type)
	require.Contains(s.T(), resp.Message, "navigate failed")
}

// --- PageInfo success ---

func (s *BrowserHandlerSuite) TestPageInfoSuccess() {
	ws, ts, mockCDP := s.startBrowserWS()
	defer ts.Close()
	defer ws.Close()

	mockCDP.On("GetPageInfo", mock.Anything).Return(&browser.PageInfo{URL: "https://x.com", Title: "X"}, nil)

	require.NoError(s.T(), ws.WriteJSON(browserWSMessage{Type: bwsMsgPageInfo}))
	resp := s.readResp(ws)
	require.Equal(s.T(), bwsRespPageInfo, resp.Type)
	require.Equal(s.T(), "https://x.com", resp.URL)
}

func (s *BrowserHandlerSuite) TestPageInfoError() {
	ws, ts, mockCDP := s.startBrowserWS()
	defer ts.Close()
	defer ws.Close()

	mockCDP.On("GetPageInfo", mock.Anything).Return(nil, errors.New("info fail"))

	require.NoError(s.T(), ws.WriteJSON(browserWSMessage{Type: bwsMsgPageInfo}))
	resp := s.readResp(ws)
	require.Equal(s.T(), bwsRespError, resp.Type)
	require.Contains(s.T(), resp.Message, "page info failed")
}

// --- Reload/Back/Forward success ---

func (s *BrowserHandlerSuite) TestReloadSuccess() {
	ws, ts, mockCDP := s.startBrowserWS()
	defer ts.Close()
	defer ws.Close()

	mockCDP.On("Reload", mock.Anything).Return(nil)
	require.NoError(s.T(), ws.WriteJSON(browserWSMessage{Type: bwsMsgReload}))
	// No response on success, verify connection still works.
	require.NoError(s.T(), ws.WriteJSON(browserWSMessage{Type: "unknown"}))
	resp := s.readResp(ws)
	require.Equal(s.T(), bwsRespError, resp.Type)
}

func (s *BrowserHandlerSuite) TestReloadError() {
	ws, ts, mockCDP := s.startBrowserWS()
	defer ts.Close()
	defer ws.Close()

	mockCDP.On("Reload", mock.Anything).Return(errors.New("reload fail"))
	require.NoError(s.T(), ws.WriteJSON(browserWSMessage{Type: bwsMsgReload}))
	resp := s.readResp(ws)
	require.Equal(s.T(), bwsRespError, resp.Type)
	require.Contains(s.T(), resp.Message, "reload failed")
}

func (s *BrowserHandlerSuite) TestBackSuccess() {
	ws, ts, mockCDP := s.startBrowserWS()
	defer ts.Close()
	defer ws.Close()

	mockCDP.On("GoBack", mock.Anything).Return(nil)
	require.NoError(s.T(), ws.WriteJSON(browserWSMessage{Type: bwsMsgBack}))
	require.NoError(s.T(), ws.WriteJSON(browserWSMessage{Type: "unknown"}))
	resp := s.readResp(ws)
	require.Equal(s.T(), bwsRespError, resp.Type)
}

func (s *BrowserHandlerSuite) TestBackError() {
	ws, ts, mockCDP := s.startBrowserWS()
	defer ts.Close()
	defer ws.Close()

	// GoBack errors are silently swallowed (timeout can fire even on success).
	mockCDP.On("GoBack", mock.Anything).Return(errors.New("back fail"))
	require.NoError(s.T(), ws.WriteJSON(browserWSMessage{Type: bwsMsgBack}))
	// Send a follow-up to verify no hang.
	require.NoError(s.T(), ws.WriteJSON(browserWSMessage{Type: "unknown"}))
	resp := s.readResp(ws)
	require.Equal(s.T(), bwsRespError, resp.Type)
	require.Contains(s.T(), resp.Message, "unknown message type")
}

func (s *BrowserHandlerSuite) TestForwardSuccess() {
	ws, ts, mockCDP := s.startBrowserWS()
	defer ts.Close()
	defer ws.Close()

	mockCDP.On("GoForward", mock.Anything).Return(nil)
	require.NoError(s.T(), ws.WriteJSON(browserWSMessage{Type: bwsMsgForward}))
	require.NoError(s.T(), ws.WriteJSON(browserWSMessage{Type: "unknown"}))
	resp := s.readResp(ws)
	require.Equal(s.T(), bwsRespError, resp.Type)
}

func (s *BrowserHandlerSuite) TestForwardError() {
	ws, ts, mockCDP := s.startBrowserWS()
	defer ts.Close()
	defer ws.Close()

	// GoForward errors are silently swallowed (timeout can fire even on success).
	mockCDP.On("GoForward", mock.Anything).Return(errors.New("fwd fail"))
	require.NoError(s.T(), ws.WriteJSON(browserWSMessage{Type: bwsMsgForward}))
	require.NoError(s.T(), ws.WriteJSON(browserWSMessage{Type: "unknown"}))
	resp := s.readResp(ws)
	require.Equal(s.T(), bwsRespError, resp.Type)
	require.Contains(s.T(), resp.Message, "unknown message type")
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

// --- Full flow: start → navigate → screencast → input → page_info → reload → stop ---

func (s *BrowserHandlerSuite) TestFullBrowserFlow() {
	ws, ts, mockCDP := s.startBrowserWS()
	defer ts.Close()
	defer ws.Close()

	// 1. Navigate.
	mockCDP.On("Navigate", mock.Anything, "https://example.com").Return(nil)
	mockCDP.On("GetPageInfo", mock.Anything).Return(&browser.PageInfo{URL: "https://example.com/", Title: "Example Domain"}, nil)
	require.NoError(s.T(), ws.WriteJSON(browserWSMessage{Type: bwsMsgNavigate, URL: "https://example.com"}))
	resp := s.readResp(ws)
	require.Equal(s.T(), bwsRespPageInfo, resp.Type)
	require.Equal(s.T(), "Example Domain", resp.Title)

	// 2. Input — click (fire-and-forget, no response).
	mockCDP.On("MouseClick", mock.Anything, float64(100), float64(200), "left", 1).Return(nil)
	require.NoError(s.T(), ws.WriteJSON(browserWSMessage{
		Type: bwsMsgInput, InputType: "click",
		X: 100, Y: 200, Button: "left", ClickCount: 1,
	}))

	// 4. Input — type text.
	mockCDP.On("TypeText", mock.Anything, "hello").Return(nil)
	require.NoError(s.T(), ws.WriteJSON(browserWSMessage{
		Type: bwsMsgInput, InputType: "typetext", Text: "hello",
	}))

	// 5. Input — keypress.
	mockCDP.On("KeyPress", mock.Anything, "Enter").Return(nil)
	require.NoError(s.T(), ws.WriteJSON(browserWSMessage{
		Type: bwsMsgInput, InputType: "keypress", Key: "Enter",
	}))

	// 6. Input — scroll.
	mockCDP.On("MouseScroll", mock.Anything, float64(100), float64(100), float64(0), float64(50)).Return(nil)
	require.NoError(s.T(), ws.WriteJSON(browserWSMessage{
		Type: bwsMsgInput, InputType: "scroll",
		X: 100, Y: 100, DeltaY: 50,
	}))

	// 7. Input — mousemove.
	mockCDP.On("MouseMove", mock.Anything, float64(300), float64(400)).Return(nil)
	require.NoError(s.T(), ws.WriteJSON(browserWSMessage{
		Type: bwsMsgInput, InputType: "mousemove",
		X: 300, Y: 400,
	}))

	// Wait for fire-and-forget inputs to process.
	time.Sleep(100 * time.Millisecond)

	// 8. Page info (has response).
	require.NoError(s.T(), ws.WriteJSON(browserWSMessage{Type: bwsMsgPageInfo}))
	resp = s.readResp(ws)
	require.Equal(s.T(), bwsRespPageInfo, resp.Type)
	require.Equal(s.T(), "https://example.com/", resp.URL)

	// 8. Reload (fire-and-forget on success).
	mockCDP.On("Reload", mock.Anything).Return(nil)
	require.NoError(s.T(), ws.WriteJSON(browserWSMessage{Type: bwsMsgReload}))

	// 9. Back / Forward (fire-and-forget on success).
	mockCDP.On("GoBack", mock.Anything).Return(nil)
	require.NoError(s.T(), ws.WriteJSON(browserWSMessage{Type: bwsMsgBack}))

	mockCDP.On("GoForward", mock.Anything).Return(nil)
	require.NoError(s.T(), ws.WriteJSON(browserWSMessage{Type: bwsMsgForward}))

	time.Sleep(100 * time.Millisecond)

	// 10. Stop.
	s.browserMgr.On("StopBrowser", mock.Anything, "ch-1").Return(nil)
	require.NoError(s.T(), ws.WriteJSON(browserWSMessage{Type: bwsMsgStop, ChannelID: "ch-1"}))
	resp = s.readResp(ws)
	require.Equal(s.T(), bwsRespStopped, resp.Type)

	time.Sleep(50 * time.Millisecond)
	mockCDP.AssertExpectations(s.T())
}
