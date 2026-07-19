package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/browser"
)

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

	s.srv.browser.cdpManagersMu.Lock()
	if s.srv.browser.cdpManagers == nil {
		s.srv.browser.cdpManagers = make(map[string]*browser.CDPManager)
	}
	s.srv.browser.cdpManagers["ch-1|docker"] = cdpMgr
	s.srv.browser.cdpManagersMu.Unlock()

	s.browserMgr.On("StopBrowser", mock.Anything, "ch-1").Return("", nil)

	s.srv.browser.cleanIdleBrowserSessions(context.Background(), time.Minute)

	s.srv.browser.cdpManagersMu.Lock()
	_, exists := s.srv.browser.cdpManagers["ch-1|docker"]
	s.srv.browser.cdpManagersMu.Unlock()
	require.False(s.T(), exists)
}

func (s *BrowserHandlerSuite) TestCleanIdleBrowserSessionsSchedulesRemove() {
	cdpMgr := browser.NewCDPManager("ws://test:9222", browser.CDPManagerConfig{}, slog.Default())
	browser.SetTimeNowForTest(cdpMgr, func() time.Time {
		return time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	})
	cdpMgr.Touch()

	s.srv.browser.cdpManagersMu.Lock()
	if s.srv.browser.cdpManagers == nil {
		s.srv.browser.cdpManagers = make(map[string]*browser.CDPManager)
	}
	s.srv.browser.cdpManagers["ch-1|docker"] = cdpMgr
	s.srv.browser.cdpManagersMu.Unlock()

	s.browserMgr.On("StopBrowser", mock.Anything, "ch-1").Return("chrome-c1", nil)

	reg := new(mockContainerManager)
	reg.On("ScheduleRemove", "chrome-c1", 5*time.Minute)
	s.srv.SetContainerRegistry(reg)
	s.srv.browser.setKeepAlive(5 * time.Minute)

	s.srv.browser.cleanIdleBrowserSessions(context.Background(), time.Minute)

	reg.AssertCalled(s.T(), "ScheduleRemove", "chrome-c1", 5*time.Minute)
}

func (s *BrowserHandlerSuite) TestSetBrowserKeepAlive() {
	s.srv.browser.setKeepAlive(10 * time.Minute)
	require.Equal(s.T(), 10*time.Minute, s.srv.browser.keepAlive)
}

// --- activeMode ---

func (s *BrowserHandlerSuite) TestActiveMode() {
	require.Equal(s.T(), "docker", s.srv.browser.modeFor("ch-1"))

	s.srv.browser.modeMu.Lock()
	if s.srv.browser.activeMode == nil {
		s.srv.browser.activeMode = make(map[string]string)
	}
	s.srv.browser.activeMode["ch-1"] = "host"
	s.srv.browser.modeMu.Unlock()

	require.Equal(s.T(), "host", s.srv.browser.modeFor("ch-1"))
}

// --- getOrCreateCDPManager ---

func (s *BrowserHandlerSuite) TestGetOrCreateCDPManager() {
	s.browserMgr.On("GetCDPEndpoint", "ch-1").Return("ws://127.0.0.1:9222").Maybe()
	mgr := s.srv.browser.getOrCreateCDPManager("ch-1", "docker", s.browserMgr)
	require.NotNil(s.T(), mgr)

	// Second call should return the same manager.
	mgr2 := s.srv.browser.getOrCreateCDPManager("ch-1", "docker", s.browserMgr)
	require.Equal(s.T(), mgr, mgr2)
}

func (s *BrowserHandlerSuite) TestGetActiveCDPManagerNotFound() {
	require.Nil(s.T(), s.srv.browser.getActiveCDPManager("nonexistent"))
}

// --- mockHostBrowserProvider ---

type mockHostBrowserProvider struct {
	mock.Mock
}

func (m *mockHostBrowserProvider) EnsureBrowser(ctx context.Context, channelID, containerID string) error {
	return m.Called(ctx, channelID, containerID).Error(0)
}
func (m *mockHostBrowserProvider) StopBrowser(ctx context.Context, channelID string) (string, error) {
	args := m.Called(ctx, channelID)
	return args.String(0), args.Error(1)
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

	mgr := s.srv.browser.getOrCreateCDPManager("ch-1", "host", hostProvider)
	require.NotNil(s.T(), mgr)
	// Host mode: DiscoverExisting should be false, MaxRetries should be 1.
	require.False(s.T(), mgr.DiscoverExisting())
}

func (s *BrowserHandlerSuite) TestGetOrCreateCDPManagerNilMap() {
	// Ensure it works when cdpManagers map is nil.
	srv := nilServer()
	srv.browser.setProviders(s.browserMgr, srv.browser.hostProvider)
	s.browserMgr.On("GetCDPEndpoint", "ch-1").Return("ws://127.0.0.1:9222").Maybe()
	mgr := srv.browser.getOrCreateCDPManager("ch-1", "docker", s.browserMgr)
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

	s.srv.browser.cdpManagersMu.Lock()
	if s.srv.browser.cdpManagers == nil {
		s.srv.browser.cdpManagers = make(map[string]*browser.CDPManager)
	}
	s.srv.browser.cdpManagers["ch-2|docker"] = cdpMgr
	s.srv.browser.cdpManagersMu.Unlock()

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
	srv.browser.setProviders(srv.browser.dockerProvider, hostProvider)
	srv.browser.setProviders(s.browserMgr, srv.browser.hostProvider)

	// Set mode to host for this channel.
	srv.browser.modeMu.Lock()
	srv.browser.activeMode = map[string]string{"ch-host": "host"}
	srv.browser.modeMu.Unlock()

	// Create and pre-connect a CDPManager.
	cdpMgr := browser.NewCDPManager("ws://127.0.0.1:9222", browser.CDPManagerConfig{
		DiscoverExisting: false,
		MaxRetries:       1,
		RetryDelay:       time.Millisecond,
	}, slog.Default())
	browser.SetCDPFactoryForTest(cdpMgr, func(_ context.Context, _ string, _ *slog.Logger, _ ...browser.CDPOption) (browser.CDPSession, error) {
		return mockCDP, nil
	})

	srv.browser.cdpManagersMu.Lock()
	srv.browser.cdpManagers = map[string]*browser.CDPManager{"ch-host|host": cdpMgr}
	srv.browser.cdpManagersMu.Unlock()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/ws/browser", srv.browser.handleBrowserWS)
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

	s.srv.browser.cdpManagersMu.Lock()
	if s.srv.browser.cdpManagers == nil {
		s.srv.browser.cdpManagers = make(map[string]*browser.CDPManager)
	}
	s.srv.browser.cdpManagers["ch-cache|docker"] = cdpMgr
	s.srv.browser.cdpManagersMu.Unlock()

	cdpCl, err := s.srv.browser.getBrowserCDP(context.Background(), "ch-cache")
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

	s.srv.browser.cdpManagersMu.Lock()
	if s.srv.browser.cdpManagers == nil {
		s.srv.browser.cdpManagers = make(map[string]*browser.CDPManager)
	}
	s.srv.browser.cdpManagers["ch-empty|docker"] = cdpMgr
	s.srv.browser.cdpManagersMu.Unlock()

	// Even with empty target ID, Connect sets activeClient, so getBrowserCDP succeeds.
	cdp, err := s.srv.browser.getBrowserCDP(context.Background(), "ch-empty")
	require.NoError(s.T(), err)
	require.NotNil(s.T(), cdp)
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

	s.srv.browser.cdpManagersMu.Lock()
	if s.srv.browser.cdpManagers == nil {
		s.srv.browser.cdpManagers = make(map[string]*browser.CDPManager)
	}
	s.srv.browser.cdpManagers["ch-idle|docker"] = cdpMgr
	s.srv.browser.cdpManagersMu.Unlock()

	s.browserMgr.On("StopBrowser", mock.Anything, "ch-idle").Return("chrome-idle-1", nil)

	done := make(chan struct{})
	go func() {
		// Use very short ticker interval so it fires within the test.
		s.srv.browser.runIdleMonitor(ctx, time.Nanosecond, time.Millisecond)
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

	s.srv.browser.cdpManagersMu.Lock()
	if s.srv.browser.cdpManagers == nil {
		s.srv.browser.cdpManagers = make(map[string]*browser.CDPManager)
	}
	s.srv.browser.cdpManagers["ch-1|host"] = cdpMgr
	s.srv.browser.cdpManagersMu.Unlock()

	// Host mode — should NOT call StopBrowser.
	s.srv.browser.cleanIdleBrowserSessions(context.Background(), time.Minute)

	s.srv.browser.cdpManagersMu.Lock()
	_, exists := s.srv.browser.cdpManagers["ch-1|host"]
	s.srv.browser.cdpManagersMu.Unlock()
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
	srv.browser.setProviders(s.browserMgr, hostProvider)

	srv.browser.modeMu.Lock()
	srv.browser.activeMode = map[string]string{"ch-host": "host"}
	srv.browser.modeMu.Unlock()

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

	srv.browser.cdpManagersMu.Lock()
	srv.browser.cdpManagers = map[string]*browser.CDPManager{"ch-host|host": cdpMgr}
	srv.browser.cdpManagersMu.Unlock()

	data, _ := json.Marshal(browserActionRequest{ChannelID: "ch-host", Action: "list_tabs"})
	r := httptest.NewRequest(http.MethodPost, "/api/browser/action", strings.NewReader(string(data)))
	w := httptest.NewRecorder()
	srv.browser.handleBrowserAction(w, r)

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

	s.srv.browser.cdpManagersMu.Lock()
	if s.srv.browser.cdpManagers == nil {
		s.srv.browser.cdpManagers = make(map[string]*browser.CDPManager)
	}
	s.srv.browser.cdpManagers["ch-mode|docker"] = cdpMgr
	s.srv.browser.cdpManagersMu.Unlock()

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
	s.srv.browser.modeMu.Lock()
	require.Equal(s.T(), "docker", s.srv.browser.activeMode["ch-mode"])
	s.srv.browser.modeMu.Unlock()
}

// --- list_tabs: active tab marking ---

func (s *BrowserHandlerSuite) TestBrowserActionListTabsWithActiveMarking() {
	mockCDP := new(mockCDPSession)
	s.setupActionMocks(mockCDP)

	s.srv.browser.cdpManagersMu.Lock()
	cdpMgr := s.srv.browser.cdpManagers["ch-1|docker"]
	s.srv.browser.cdpManagersMu.Unlock()
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
	s.srv.browser.cdpManagersMu.Lock()
	cdpMgr := s.srv.browser.cdpManagers["ch-1|docker"]
	s.srv.browser.cdpManagersMu.Unlock()
	require.True(s.T(), cdpMgr.IsTrackedTab("new-t-id"))
}
