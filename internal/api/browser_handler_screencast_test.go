package api

import (
	"context"
	"errors"
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
	mockCDP.On("ListTabs", mock.Anything).Return([]browser.TabInfo{{TargetID: "t-new"}}, nil)
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

// The strip draws what Chrome resolved for each tab; a tab Chrome has no icon
// for simply carries none, which is what the plain dot is for.
func (s *BrowserHandlerSuite) TestSendTabsResponseCarriesFavicons() {
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

	cdpMgr := browser.NewCDPManager("ws://test:9222", browser.CDPManagerConfig{}, slog.Default())
	cdpMgr.TrackTab("t1")
	cdpMgr.TrackTab("t2")

	mockCDP := new(mockCDPSession)
	mockCDP.favicons = map[string]string{"t1": "https://a.example/favicon.ico"}

	bc := &browserWSConn{
		conn:            <-connReady,
		browserProvider: s.browserMgr,
		logger:          slog.Default(),
		cdpMgr:          cdpMgr,
		cdp:             mockCDP,
		stopCh:          make(chan struct{}),
	}

	bc.sendTabsResponse([]browser.TabInfo{
		{TargetID: "t1", URL: "https://a.example", Title: "A"},
		{TargetID: "t2", URL: "https://b.example", Title: "B"},
	}, "t1")

	require.NoError(s.T(), clientWS.SetReadDeadline(time.Now().Add(2*time.Second)))
	var resp browserWSResponse
	require.NoError(s.T(), clientWS.ReadJSON(&resp))
	require.Len(s.T(), resp.Tabs, 2)
	require.Equal(s.T(), "https://a.example/favicon.ico", resp.Tabs[0].FaviconURL)
	require.Empty(s.T(), resp.Tabs[1].FaviconURL)
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
	mockCDP.On("ListTabs", mock.Anything).Return([]browser.TabInfo{{TargetID: "t-new"}}, nil)
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

// --- restartScreencastForTarget: the tab asked for is gone ---

// wsTestPair dials a WebSocket against a throwaway server and returns both
// ends, so the tests below can assert on what the pane was actually sent.
func (s *BrowserHandlerSuite) wsTestPair() (client *websocket.Conn, server *websocket.Conn) {
	connReady := make(chan *websocket.Conn, 1)
	tsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		connReady <- conn
	}))
	s.T().Cleanup(tsSrv.Close)

	wsURL := "ws" + strings.TrimPrefix(tsSrv.URL, "http") + "/"
	client, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(s.T(), err)
	s.T().Cleanup(func() { _ = client.Close() })
	return client, <-connReady
}

// goneTargetManager returns a manager whose browser client sees only t-live,
// so any attach to another target is an attach to a tab that has gone.
func (s *BrowserHandlerSuite) goneTargetManager() *browser.CDPManager {
	mgrClient := new(mockCDPSession)
	mgrClient.On("ListTabs", mock.Anything).Return([]browser.TabInfo{{TargetID: "t-live"}}, nil)
	mgrClient.On("Close").Return().Maybe()

	cdpMgr := browser.NewCDPManager("ws://test:9222", browser.CDPManagerConfig{
		MaxRetries: 1,
		RetryDelay: time.Millisecond,
	}, slog.Default())
	cdpMgr.SetClientForTarget("t-live", mgrClient)
	cdpMgr.TrackTab("t-live")
	return cdpMgr
}

func (s *BrowserHandlerSuite) TestRestartScreencastForTargetTabGoneKeepsCurrentTab() {
	current := new(mockCDPSession)
	current.On("TargetID").Return("t-live")
	current.On("ListTabs", mock.Anything).Return([]browser.TabInfo{
		{TargetID: "t-live", URL: "https://example.com", Title: "Live"},
	}, nil)
	current.On("Favicons").Return(map[string]string(nil)).Maybe()

	clientWS, serverConn := s.wsTestPair()
	oldStopCh := make(chan struct{})
	bc := &browserWSConn{
		conn:             serverConn,
		browserProvider:  s.browserMgr,
		logger:           slog.Default(),
		cdpMgr:           s.goneTargetManager(),
		cdp:              current,
		stopCh:           make(chan struct{}),
		screencastStopCh: oldStopCh,
	}

	bc.restartScreencastForTarget(context.Background(), current, "t-gone")

	// The pane was showing a tab that still works, so its stream must survive
	// a click on one that does not.
	select {
	case <-oldStopCh:
		s.T().Fatal("screencast for the live tab should not have been stopped")
	default:
	}

	require.NoError(s.T(), clientWS.SetReadDeadline(time.Now().Add(2*time.Second)))
	var errResp browserWSResponse
	require.NoError(s.T(), clientWS.ReadJSON(&errResp))
	require.Equal(s.T(), bwsRespError, errResp.Type)
	require.Equal(s.T(), "that tab is no longer open", errResp.Message)

	var tabsResp browserWSResponse
	require.NoError(s.T(), clientWS.ReadJSON(&tabsResp))
	require.Equal(s.T(), bwsRespTabs, tabsResp.Type)
	require.Equal(s.T(), "t-live", tabsResp.ActiveTargetID)
	require.Len(s.T(), tabsResp.Tabs, 1)
	require.Equal(s.T(), "t-live", tabsResp.Tabs[0].TargetID)
}

func (s *BrowserHandlerSuite) TestRestartScreencastForTargetTabGoneWithoutCurrentClient() {
	clientWS, serverConn := s.wsTestPair()
	bc := &browserWSConn{
		conn:            serverConn,
		browserProvider: s.browserMgr,
		logger:          slog.Default(),
		cdpMgr:          s.goneTargetManager(),
		stopCh:          make(chan struct{}),
	}

	bc.restartScreencastForTarget(context.Background(), nil, "t-gone")

	require.NoError(s.T(), clientWS.SetReadDeadline(time.Now().Add(2*time.Second)))
	var errResp browserWSResponse
	require.NoError(s.T(), clientWS.ReadJSON(&errResp))
	require.Equal(s.T(), bwsRespError, errResp.Type)
	// With no client there is no tab list to redraw, so the error stands alone.
	require.NoError(s.T(), clientWS.SetReadDeadline(time.Now().Add(200*time.Millisecond)))
	require.Error(s.T(), clientWS.ReadJSON(&errResp))
}

func (s *BrowserHandlerSuite) TestRestartScreencastForTargetTabGoneListingFails() {
	current := new(mockCDPSession)
	current.On("TargetID").Return("t-live").Maybe()
	current.On("ListTabs", mock.Anything).Return([]browser.TabInfo(nil), errors.New("no listing"))

	clientWS, serverConn := s.wsTestPair()
	bc := &browserWSConn{
		conn:            serverConn,
		browserProvider: s.browserMgr,
		logger:          slog.Default(),
		cdpMgr:          s.goneTargetManager(),
		cdp:             current,
		stopCh:          make(chan struct{}),
	}

	bc.restartScreencastForTarget(context.Background(), current, "t-gone")

	require.NoError(s.T(), clientWS.SetReadDeadline(time.Now().Add(2*time.Second)))
	var errResp browserWSResponse
	require.NoError(s.T(), clientWS.ReadJSON(&errResp))
	require.Equal(s.T(), bwsRespError, errResp.Type)
	require.NoError(s.T(), clientWS.SetReadDeadline(time.Now().Add(200*time.Millisecond)))
	require.Error(s.T(), clientWS.ReadJSON(&errResp))
}
