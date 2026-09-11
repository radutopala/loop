package api

import (
	"context"
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
	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
)

type mockBrowserProvider struct {
	mock.Mock
}

func (m *mockBrowserProvider) EnsureBrowser(ctx context.Context, channelID, containerID string) error {
	return m.Called(ctx, channelID, containerID).Error(0)
}

func (m *mockBrowserProvider) StopBrowser(ctx context.Context, channelID string) (string, error) {
	args := m.Called(ctx, channelID)
	return args.String(0), args.Error(1)
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

func (m *mockBrowserProvider) RemoveProfile(ctx context.Context, channelID string) error {
	return m.Called(ctx, channelID).Error(0)
}

func (m *mockBrowserProvider) Cleanup(ctx context.Context) {
	m.Called(ctx)
}

type BrowserHandlerSuite struct {
	suite.Suite
	browserMgr *mockBrowserProvider
	srv        *Server
}

func TestBrowserHandlerSuite(t *testing.T) {
	suite.Run(t, new(BrowserHandlerSuite))
}

func (s *BrowserHandlerSuite) SetupTest() {
	s.browserMgr = new(mockBrowserProvider)
	s.srv = nilServer()
	s.srv.browser.setProviders(s.browserMgr, s.srv.browser.hostProvider)
}

// The browser block lives in per-project config while the providers are built
// once, at daemon start, from the global layer — so a project's overrides only
// reach its channels if the providers ask per channel.
func (s *BrowserHandlerSuite) TestChannelBrowserConfig() {
	store := new(MockChannelLister)
	store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: "/home/user/project"}, nil)
	srv := NewServer(nil, nil, nil, store, nil, testLogger())
	srv.configs.load = func() (*config.Config, error) {
		return &config.Config{Browser: config.BrowserConfig{
			Enabled:     true,
			ChromeImage: "loop-chrome:latest",
			HostCDPPort: 9222,
			MemoryMB:    512,
		}}, nil
	}
	srv.configs.loadProject = func(_ string, base *config.Config) (*config.Config, error) {
		merged := *base
		merged.Browser.ChromeImage = "project-chrome:latest"
		merged.Browser.PersistProfile = true
		merged.Browser.Extensions = []string{"/host/ublock"}
		merged.Browser.HostCDPPort = 9333
		merged.Browser.MemoryMB = 2048
		return &merged, nil
	}
	ctx := context.Background()

	cs, ok := srv.browser.channelBrowserSettings(ctx, "ch-1")
	require.True(s.T(), ok)
	require.Equal(s.T(), browser.ChannelSettings{
		Image:          "project-chrome:latest",
		PersistProfile: true,
		Extensions:     []string{"/host/ublock"},
		MemoryMB:       2048,
	}, cs)

	port, ok := srv.browser.channelHostCDPPort(ctx, "ch-1")
	require.True(s.T(), ok)
	require.Equal(s.T(), 9333, port)

	require.True(s.T(), srv.browser.browserEnabledFor(ctx, "ch-1"))
}

// A project that turns the browser off gets no sidecar for its channels, which
// is the whole point of the flag; every other channel is untouched.
func (s *BrowserHandlerSuite) TestBrowserEnabledForDisabledProject() {
	store := new(MockChannelLister)
	store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: "/home/user/project"}, nil)
	srv := NewServer(nil, nil, nil, store, nil, testLogger())
	srv.configs.load = func() (*config.Config, error) {
		return &config.Config{Browser: config.BrowserConfig{Enabled: true}}, nil
	}
	srv.configs.loadProject = func(_ string, base *config.Config) (*config.Config, error) {
		merged := *base
		merged.Browser.Enabled = false
		return &merged, nil
	}

	require.False(s.T(), srv.browser.browserEnabledFor(context.Background(), "ch-1"))
}

func (s *BrowserHandlerSuite) TestChannelBrowserConfigUnresolvableChannel() {
	// No store to look the channel up in: the providers keep what they have,
	// and a browser that has always worked keeps working.
	ctx := context.Background()

	cs, ok := s.srv.browser.channelBrowserSettings(ctx, "ch-1")
	require.False(s.T(), ok)
	require.Zero(s.T(), cs)

	port, ok := s.srv.browser.channelHostCDPPort(ctx, "ch-1")
	require.False(s.T(), ok)
	require.Zero(s.T(), port)

	require.True(s.T(), s.srv.browser.browserEnabledFor(ctx, "ch-1"))
}

func (s *BrowserHandlerSuite) TestChannelBrowserConfigUnreadable() {
	store := new(MockChannelLister)
	store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: "/home/user/project"}, nil)
	srv := NewServer(nil, nil, nil, store, nil, testLogger())
	srv.configs.load = func() (*config.Config, error) { return nil, errors.New("unreadable") }
	ctx := context.Background()

	cs, ok := srv.browser.channelBrowserSettings(ctx, "ch-1")
	require.False(s.T(), ok)
	require.Zero(s.T(), cs)

	port, ok := srv.browser.channelHostCDPPort(ctx, "ch-1")
	require.False(s.T(), ok)
	require.Zero(s.T(), port)

	require.True(s.T(), srv.browser.browserEnabledFor(ctx, "ch-1"))
}

// setProviders hands both providers the lookup, so the values a channel
// resolves to are the values its browser is built from.
func (s *BrowserHandlerSuite) TestSetProvidersInstallsResolvers() {
	store := new(MockChannelLister)
	store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: "/home/user/project"}, nil)
	srv := NewServer(nil, nil, nil, store, nil, testLogger())
	srv.configs.load = func() (*config.Config, error) {
		return &config.Config{Browser: config.BrowserConfig{
			ChromeImage: "project-chrome:latest",
			HostCDPPort: 9333,
			MemoryMB:    2048,
		}}, nil
	}
	ctx := context.Background()

	dp := browser.NewDockerProvider(nil, browser.DockerProviderConfig{Image: "loop-chrome:latest", MemoryMB: 512}, testLogger())
	hp := browser.NewHostProvider(9222, testLogger())
	srv.browser.setProviders(dp, hp)

	cs := dp.SettingsFor(ctx, "ch-1")
	require.Equal(s.T(), "project-chrome:latest", cs.Image)
	require.Equal(s.T(), int64(2048), cs.MemoryMB)
	require.Equal(s.T(), 9333, hp.PortFor(ctx, "ch-1"))
}

func (s *BrowserHandlerSuite) dialBrowserWS() (*websocket.Conn, *httptest.Server) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/ws/browser", s.srv.browser.handleBrowserWS)
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

func (s *BrowserHandlerSuite) TestBrowserServiceDockerProviderField() {
	srv := nilServer()
	require.Nil(s.T(), srv.browser.dockerProvider)

	mgr := new(mockBrowserProvider)
	srv.browser.setProviders(mgr, srv.browser.hostProvider)
	require.NotNil(s.T(), srv.browser.dockerProvider)
}

func (s *BrowserHandlerSuite) TestBrowserServiceHostProviderField() {
	srv := nilServer()
	require.Nil(s.T(), srv.browser.hostProvider)

	mgr := new(mockBrowserProvider)
	srv.browser.setProviders(srv.browser.dockerProvider, mgr)
	require.NotNil(s.T(), srv.browser.hostProvider)
}

func (s *BrowserHandlerSuite) TestActiveBrowserProviderDefault() {
	srv := nilServer()
	dockerMgr := new(mockBrowserProvider)
	srv.browser.setProviders(dockerMgr, srv.browser.hostProvider)
	require.Equal(s.T(), dockerMgr, srv.browser.activeBrowserProvider("ch-1"))
}

func (s *BrowserHandlerSuite) TestActiveBrowserProviderHostMode() {
	srv := nilServer()
	dockerMgr := new(mockBrowserProvider)
	hostMgr := new(mockBrowserProvider)
	srv.browser.setProviders(dockerMgr, hostMgr)

	srv.browser.modeMu.Lock()
	srv.browser.activeMode = map[string]string{"ch-1": "host"}
	srv.browser.modeMu.Unlock()

	require.Equal(s.T(), hostMgr, srv.browser.activeBrowserProvider("ch-1"))
	require.Equal(s.T(), dockerMgr, srv.browser.activeBrowserProvider("ch-2"))
}

func (s *BrowserHandlerSuite) TestHandleBrowserModeSwitch() {
	srv := nilServer()
	srv.logger = slog.Default()
	dockerMgr := new(mockBrowserProvider)
	hostMgr := new(mockBrowserProvider)
	srv.browser.setProviders(dockerMgr, hostMgr)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/browser/mode", srv.browser.handleBrowserMode)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	body := `{"channel_id":"ch-1","mode":"host"}`
	resp, err := http.Post(ts.URL+"/api/browser/mode", "application/json", strings.NewReader(body))
	require.NoError(s.T(), err)
	defer resp.Body.Close()
	require.Equal(s.T(), http.StatusOK, resp.StatusCode)

	srv.browser.modeMu.Lock()
	require.Equal(s.T(), "host", srv.browser.activeMode["ch-1"])
	srv.browser.modeMu.Unlock()
}

func (s *BrowserHandlerSuite) TestHandleBrowserModeInvalidMode() {
	srv := nilServer()
	srv.logger = slog.Default()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/browser/mode", srv.browser.handleBrowserMode)
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
	mux.HandleFunc("POST /api/browser/mode", srv.browser.handleBrowserMode)
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
	srv.browser.setProviders(new(mockBrowserProvider), srv.browser.hostProvider)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/browser/mode", srv.browser.handleBrowserMode)
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
	mux.HandleFunc("POST /api/browser/mode", srv.browser.handleBrowserMode)
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
	mux.HandleFunc("GET /api/ws/browser", srv.browser.handleBrowserWS)
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

// Disabling the browser for a project has to stop the pane from starting one,
// not just hide the MCP server from its agents.
func (s *BrowserHandlerSuite) TestStartBrowserDisabledForProject() {
	store := new(MockChannelLister)
	store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: "/home/user/project"}, nil)
	srv := NewServer(nil, nil, nil, store, nil, testLogger())
	srv.configs.load = func() (*config.Config, error) {
		return &config.Config{Browser: config.BrowserConfig{Enabled: false}}, nil
	}
	srv.browser.setProviders(s.browserMgr, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/ws/browser", srv.browser.handleBrowserWS)
	ts := httptest.NewServer(mux)
	defer ts.Close()
	ws, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http")+"/api/ws/browser", nil)
	require.NoError(s.T(), err)
	defer ws.Close()

	require.NoError(s.T(), ws.WriteJSON(browserWSMessage{Type: bwsMsgStart, ChannelID: "ch-1"}))

	resp := s.readResp(ws)
	require.Equal(s.T(), bwsRespError, resp.Type)
	require.Contains(s.T(), resp.Message, "browser is disabled for this project")
	s.browserMgr.AssertNotCalled(s.T(), "EnsureBrowser", mock.Anything, mock.Anything, mock.Anything)
}

// Same gate on the action path the agent's MCP tools come through.
func (s *BrowserHandlerSuite) TestGetBrowserCDPDisabledForProject() {
	store := new(MockChannelLister)
	store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: "/home/user/project"}, nil)
	srv := NewServer(nil, nil, nil, store, nil, testLogger())
	srv.configs.load = func() (*config.Config, error) {
		return &config.Config{Browser: config.BrowserConfig{Enabled: false}}, nil
	}
	srv.browser.setProviders(s.browserMgr, nil)

	cdpCl, err := srv.browser.getBrowserCDP(context.Background(), "ch-1")
	require.ErrorIs(s.T(), err, errBrowserDisabled)
	require.Nil(s.T(), cdpCl)
	s.browserMgr.AssertNotCalled(s.T(), "EnsureBrowser", mock.Anything, mock.Anything, mock.Anything)
}

func (s *BrowserHandlerSuite) TestStopNoSession() {
	ws, ts := s.dialBrowserWS()
	defer ts.Close()
	defer ws.Close()

	s.browserMgr.On("StopBrowser", mock.Anything, "ch-1").Return("", nil)

	err := ws.WriteJSON(browserWSMessage{Type: bwsMsgStop, ChannelID: "ch-1"})
	require.NoError(s.T(), err)

	resp := s.readResp(ws)
	require.Equal(s.T(), bwsRespStopped, resp.Type)
}

func (s *BrowserHandlerSuite) TestStopSchedulesRemoval() {
	reg := new(mockContainerManager)
	reg.On("ScheduleRemove", "chrome-stop-1", 5*time.Minute)
	s.srv.SetContainerRegistry(reg)
	s.srv.browser.setKeepAlive(5 * time.Minute)

	s.browserMgr.On("StopBrowser", mock.Anything, "ch-1").Return("chrome-stop-1", nil)

	ws, ts := s.dialBrowserWS()
	defer ts.Close()
	defer ws.Close()

	err := ws.WriteJSON(browserWSMessage{Type: bwsMsgStop, ChannelID: "ch-1"})
	require.NoError(s.T(), err)

	resp := s.readResp(ws)
	require.Equal(s.T(), bwsRespStopped, resp.Type)

	reg.AssertCalled(s.T(), "ScheduleRemove", "chrome-stop-1", 5*time.Minute)
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
	srv.browser.setProviders(s.browserMgr, srv.browser.hostProvider)

	err := srv.Start("127.0.0.1:0")
	require.NoError(s.T(), err)
	defer func() { _ = srv.Stop(context.Background()) }()
}

var errTestAPI = errors.New("test error")

// --- Mock CDP Client ---

type mockCDPSession struct {
	mock.Mock
	closeBrowserErr error
	// dead makes Alive report a session whose context died with its container.
	// A field rather than an expectation, so the many tests that never exercise
	// liveness need no extra setup.
	dead bool
	// attachFn models attaching to another tab; nil attaches to this session,
	// which is what almost every test wants.
	attachFn func(targetID string) (browser.CDPSession, error)
	// favicons is what Favicons returns; nil for the tabs that have none.
	favicons map[string]string
}

func (m *mockCDPSession) Alive() bool { return !m.dead }

// closeBrowserErr is what CloseBrowser returns; a field, since the manager
// calls it on the way down in tests that are not about the flush.
func (m *mockCDPSession) CloseBrowser(_ context.Context) error { return m.closeBrowserErr }

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
func (m *mockCDPSession) KeyPress(ctx context.Context, key string, modifiers int) error {
	return m.Called(ctx, key, modifiers).Error(0)
}
func (m *mockCDPSession) InsertText(ctx context.Context, text string) error {
	return m.Called(ctx, text).Error(0)
}
func (m *mockCDPSession) ReadSelection(ctx context.Context) (string, error) {
	args := m.Called(ctx)
	return args.String(0), args.Error(1)
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

// favicons is a field rather than an expectation: the tab strip asks for
// icons on every tabs message, and almost no test cares what comes back.
func (m *mockCDPSession) Favicons() map[string]string { return m.favicons }

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
func (m *mockCDPSession) NewContextForTarget(targetID string) (browser.CDPSession, error) {
	if m.attachFn != nil {
		return m.attachFn(targetID)
	}
	return m, nil
}
func (m *mockCDPSession) SetCookies(ctx context.Context, cookies []browser.Cookie) error {
	return m.Called(ctx, cookies).Error(0)
}

// --- Helper: start browser and get WS with CDP mock ---

// newTestCDPManager creates a CDPManager with a mock factory for testing.
// The mock CDP session is wired in separately by the caller via
// browser.SetCDPFactoryForTest, so this helper takes no arguments.
func newTestCDPManager() *browser.CDPManager {
	mgr := browser.NewCDPManager("ws://127.0.0.1:9222", browser.CDPManagerConfig{
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
	cdpMgr := newTestCDPManager()
	s.srv.browser.cdpManagersMu.Lock()
	if s.srv.browser.cdpManagers == nil {
		s.srv.browser.cdpManagers = make(map[string]*browser.CDPManager)
	}
	s.srv.browser.cdpManagers["ch-1|docker"] = cdpMgr
	s.srv.browser.cdpManagersMu.Unlock()
	// Instead of complex injection, use a simpler approach:
	// Override the server's getOrCreateCDPManager to return a pre-connected manager.
	ws, ts := s.dialBrowserWS()

	// Directly manipulate: create a connected CDPManager by injecting
	// the mock CDP client before the WS start message.
	s.srv.browser.cdpManagersMu.Lock()
	mgr := s.srv.browser.cdpManagers["ch-1|docker"]
	if mgr == nil {
		mgr = newTestCDPManager()
		s.srv.browser.cdpManagers["ch-1|docker"] = mgr
	}
	s.srv.browser.cdpManagersMu.Unlock()

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
