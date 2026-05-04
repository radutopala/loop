package api

import (
	"context"
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
