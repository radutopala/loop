package api

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/browser"
)

// --- readConsoleMessages additional coverage ---

func (s *BrowserHandlerSuite) TestReadConsoleMessagesNilCapture() {
	// Call readConsoleMessages directly (not through handleBrowserAction)
	// to test the cs == nil path. handleBrowserAction always calls
	// ensureBrowserCapture first, so cs is never nil through the normal flow.
	resp := s.srv.browser.readConsoleMessages("no-capture-channel", nil)
	require.Contains(s.T(), resp.Result, "No console messages")
}

func (s *BrowserHandlerSuite) TestReadConsoleMessagesWithFilter() {
	mockCDP := new(mockCDPSession)
	s.setupActionMocks(mockCDP)

	s.srv.browser.capturesMu.Lock()
	if s.srv.browser.captures == nil {
		s.srv.browser.captures = make(map[string]*browser.CaptureState)
	}
	cs := &browser.CaptureState{Started: true}
	cs.ConsoleMsgs = []browser.ConsoleMessage{
		{Level: "log", Text: "hello world", Time: time.Now()},
		{Level: "error", Text: "critical error", Time: time.Now()},
		{Level: "log", Text: "other msg", Time: time.Now()},
	}
	s.srv.browser.captures["ch-1"] = cs
	s.srv.browser.capturesMu.Unlock()

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

	s.srv.browser.capturesMu.Lock()
	if s.srv.browser.captures == nil {
		s.srv.browser.captures = make(map[string]*browser.CaptureState)
	}
	cs := &browser.CaptureState{Started: true}
	cs.ConsoleMsgs = []browser.ConsoleMessage{
		{Level: "log", Text: "info msg", Time: time.Now()},
		{Level: "error", Text: "err msg", Time: time.Now()},
	}
	s.srv.browser.captures["ch-1"] = cs
	s.srv.browser.capturesMu.Unlock()

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

	s.srv.browser.capturesMu.Lock()
	if s.srv.browser.captures == nil {
		s.srv.browser.captures = make(map[string]*browser.CaptureState)
	}
	cs := &browser.CaptureState{Started: true}
	cs.ConsoleMsgs = []browser.ConsoleMessage{
		{Level: "log", Text: "msg", Time: time.Now()},
	}
	s.srv.browser.captures["ch-1"] = cs
	s.srv.browser.capturesMu.Unlock()

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

	s.srv.browser.capturesMu.Lock()
	if s.srv.browser.captures == nil {
		s.srv.browser.captures = make(map[string]*browser.CaptureState)
	}
	s.srv.browser.captures["ch-1"] = &browser.CaptureState{Started: true}
	s.srv.browser.capturesMu.Unlock()

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
	s.srv.browser.capturesMu.Lock()
	if s.srv.browser.captures == nil {
		s.srv.browser.captures = make(map[string]*browser.CaptureState)
	}
	cs := &browser.CaptureState{Started: true, ConsoleMsgs: msgs}
	s.srv.browser.captures["ch-1"] = cs
	s.srv.browser.capturesMu.Unlock()

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

	s.srv.browser.capturesMu.Lock()
	if s.srv.browser.captures == nil {
		s.srv.browser.captures = make(map[string]*browser.CaptureState)
	}
	s.srv.browser.captures["ch-1"] = &browser.CaptureState{Started: true}
	s.srv.browser.capturesMu.Unlock()

	w := s.postBrowserAction(browserActionRequest{ChannelID: "ch-1", Action: "read_console"})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Result, "No console messages")
}

// --- readNetworkRequests additional coverage ---

func (s *BrowserHandlerSuite) TestReadNetworkRequestsNilCapture() {
	resp := s.srv.browser.readNetworkRequests("no-capture-channel", nil)
	require.Contains(s.T(), resp.Result, "No network requests")
}

func (s *BrowserHandlerSuite) TestReadNetworkRequestsWithFilter() {
	mockCDP := new(mockCDPSession)
	s.setupActionMocks(mockCDP)

	s.srv.browser.capturesMu.Lock()
	if s.srv.browser.captures == nil {
		s.srv.browser.captures = make(map[string]*browser.CaptureState)
	}
	cs := &browser.CaptureState{Started: true}
	cs.NetworkReqs = []browser.NetworkRequest{
		{URL: "https://api.example.com/v1", Method: "GET", Status: 200, StatusText: "OK", Time: time.Now()},
		{URL: "https://cdn.example.com/asset.js", Method: "GET", Status: 200, StatusText: "OK", Time: time.Now()},
	}
	s.srv.browser.captures["ch-1"] = cs
	s.srv.browser.capturesMu.Unlock()

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

	s.srv.browser.capturesMu.Lock()
	if s.srv.browser.captures == nil {
		s.srv.browser.captures = make(map[string]*browser.CaptureState)
	}
	cs := &browser.CaptureState{Started: true}
	cs.NetworkReqs = []browser.NetworkRequest{
		{URL: "https://a.com", Method: "GET", Status: 200, StatusText: "OK", Time: time.Now()},
	}
	s.srv.browser.captures["ch-1"] = cs
	s.srv.browser.capturesMu.Unlock()

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

	s.srv.browser.capturesMu.Lock()
	if s.srv.browser.captures == nil {
		s.srv.browser.captures = make(map[string]*browser.CaptureState)
	}
	s.srv.browser.captures["ch-1"] = &browser.CaptureState{Started: true}
	s.srv.browser.capturesMu.Unlock()

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
	s.srv.browser.capturesMu.Lock()
	if s.srv.browser.captures == nil {
		s.srv.browser.captures = make(map[string]*browser.CaptureState)
	}
	cs := &browser.CaptureState{Started: true, NetworkReqs: reqs}
	s.srv.browser.captures["ch-1"] = cs
	s.srv.browser.capturesMu.Unlock()

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

	s.srv.browser.capturesMu.Lock()
	if s.srv.browser.captures == nil {
		s.srv.browser.captures = make(map[string]*browser.CaptureState)
	}
	s.srv.browser.captures["ch-1"] = &browser.CaptureState{Started: true}
	s.srv.browser.capturesMu.Unlock()

	w := s.postBrowserAction(browserActionRequest{ChannelID: "ch-1", Action: "read_network"})
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Contains(s.T(), resp.Result, "No network requests")
}

// --- RunBrowserIdleMonitor: ticker fires ---
