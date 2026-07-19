package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/browser"
)

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
	s.srv.browser.cdpManagersMu.Lock()
	cdpMgr := s.srv.browser.cdpManagers["ch-1|docker"]
	s.srv.browser.cdpManagersMu.Unlock()

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

	s.srv.browser.cdpManagersMu.Lock()
	cdpMgr := s.srv.browser.cdpManagers["ch-1|docker"]
	s.srv.browser.cdpManagersMu.Unlock()

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
	s.srv.browser.handleBrowserAction(w, r)
	return w
}

func (s *BrowserHandlerSuite) TestBrowserActionNoBrowserProvider() {
	srv := nilServer()
	body := strings.NewReader(`{"channel_id":"ch-1","action":"navigate","params":{"url":"https://example.com"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/browser/action", body)
	w := httptest.NewRecorder()
	srv.browser.handleBrowserAction(w, req)
	require.Equal(s.T(), http.StatusServiceUnavailable, w.Code)
}

func (s *BrowserHandlerSuite) TestBrowserActionMissingChannelID() {
	body := strings.NewReader(`{"action":"navigate","params":{"url":"https://example.com"}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/browser/action", body)
	w := httptest.NewRecorder()
	s.srv.browser.handleBrowserAction(w, req)
	require.Equal(s.T(), http.StatusBadRequest, w.Code)
}

func (s *BrowserHandlerSuite) TestBrowserActionInvalidJSON() {
	body := strings.NewReader(`not json`)
	req := httptest.NewRequest(http.MethodPost, "/api/browser/action", body)
	w := httptest.NewRecorder()
	s.srv.browser.handleBrowserAction(w, req)
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

	s.srv.browser.cdpManagersMu.Lock()
	if s.srv.browser.cdpManagers == nil {
		s.srv.browser.cdpManagers = make(map[string]*browser.CDPManager)
	}
	s.srv.browser.cdpManagers["ch-1|docker"] = cdpMgr
	s.srv.browser.cdpManagersMu.Unlock()
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
	cdpMgr := s.srv.browser.cdpManagers["ch-1|docker"]
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
	s.srv.browser.cdpManagersMu.Lock()
	if s.srv.browser.cdpManagers == nil {
		s.srv.browser.cdpManagers = make(map[string]*browser.CDPManager)
	}
	s.srv.browser.cdpManagers["ch-1|docker"] = cdpMgr
	s.srv.browser.cdpManagersMu.Unlock()

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
	s.srv.browser.ensureBrowserCapture(context.Background(), "ch-1", mockCDP)
	// Second call with the same client should not rewire.
	s.srv.browser.ensureBrowserCapture(context.Background(), "ch-1", mockCDP)

	mockCDP.AssertNumberOfCalls(s.T(), "EnableConsoleCapture", 1)
	mockCDP.AssertNumberOfCalls(s.T(), "EnableNetworkCapture", 1)
}

func (s *BrowserHandlerSuite) TestEnsureBrowserCaptureRewireOnNewClient() {
	mockCDP1 := new(mockCDPSession)
	mockCDP1.On("EnableConsoleCapture", mock.Anything, mock.Anything).Return(nil)
	mockCDP1.On("EnableNetworkCapture", mock.Anything, mock.Anything).Return(nil)

	// First call initializes capture with client 1.
	s.srv.browser.ensureBrowserCapture(context.Background(), "ch-1", mockCDP1)

	// Second call with a different client should rewire capture.
	mockCDP2 := new(mockCDPSession)
	mockCDP2.On("EnableConsoleCapture", mock.Anything, mock.Anything).Return(nil)
	mockCDP2.On("EnableNetworkCapture", mock.Anything, mock.Anything).Return(nil)

	s.srv.browser.ensureBrowserCapture(context.Background(), "ch-1", mockCDP2)

	mockCDP2.AssertCalled(s.T(), "EnableConsoleCapture", mock.Anything, mock.Anything)
	mockCDP2.AssertCalled(s.T(), "EnableNetworkCapture", mock.Anything, mock.Anything)
}

func (s *BrowserHandlerSuite) TestEnsureBrowserCaptureConsoleCaptureError() {
	mockCDP := new(mockCDPSession)
	mockCDP.On("EnableConsoleCapture", mock.Anything, mock.Anything).Return(errors.New("console cap fail"))
	mockCDP.On("EnableNetworkCapture", mock.Anything, mock.Anything).Return(nil)

	s.srv.browser.ensureBrowserCapture(context.Background(), "ch-1", mockCDP)

	mockCDP.AssertCalled(s.T(), "EnableConsoleCapture", mock.Anything, mock.Anything)
	mockCDP.AssertCalled(s.T(), "EnableNetworkCapture", mock.Anything, mock.Anything)
}

func (s *BrowserHandlerSuite) TestEnsureBrowserCaptureNetworkCaptureError() {
	mockCDP := new(mockCDPSession)
	mockCDP.On("EnableConsoleCapture", mock.Anything, mock.Anything).Return(nil)
	mockCDP.On("EnableNetworkCapture", mock.Anything, mock.Anything).Return(errors.New("net cap fail"))

	s.srv.browser.ensureBrowserCapture(context.Background(), "ch-1", mockCDP)

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

	s.srv.browser.ensureBrowserCapture(context.Background(), "ch-goroutines", mockCDP)

	require.Eventually(s.T(), func() bool {
		s.srv.browser.capturesMu.Lock()
		cs := s.srv.browser.captures["ch-goroutines"]
		s.srv.browser.capturesMu.Unlock()
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

	s.srv.browser.capturesMu.Lock()
	cs := s.srv.browser.captures["ch-goroutines"]
	s.srv.browser.capturesMu.Unlock()
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

	s.srv.browser.capturesMu.Lock()
	if s.srv.browser.captures == nil {
		s.srv.browser.captures = make(map[string]*browser.CaptureState)
	}
	cs := &browser.CaptureState{Started: true}
	cs.ConsoleMsgs = []browser.ConsoleMessage{
		{Level: "log", Text: "hello world", Time: time.Now()},
		{Level: "error", Text: "something failed", Time: time.Now()},
	}
	s.srv.browser.captures["ch-1"] = cs
	s.srv.browser.capturesMu.Unlock()

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

	s.srv.browser.capturesMu.Lock()
	if s.srv.browser.captures == nil {
		s.srv.browser.captures = make(map[string]*browser.CaptureState)
	}
	cs := &browser.CaptureState{Started: true}
	cs.NetworkReqs = []browser.NetworkRequest{
		{URL: "https://api.example.com/v1", Method: "GET", Status: 200, StatusText: "OK", Time: time.Now()},
	}
	s.srv.browser.captures["ch-1"] = cs
	s.srv.browser.capturesMu.Unlock()

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
