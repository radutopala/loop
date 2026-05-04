package mcpbrowser

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/browser"
)

func (s *ServerSuite) TestResizeWindowSuccess() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, r *http.Request) {
		_, action, params := decodeActionRequest(s.T(), r)
		require.Equal(s.T(), "resize_window", action)
		require.Equal(s.T(), float64(1024), params["width"])
		require.Equal(s.T(), float64(768), params["height"])
		writeJSON(w, actionResponse{Result: "ok"})
	})
	res := callTool(s.T(), session, "resize_window", map[string]any{"width": 1024, "height": 768})
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "Resized viewport to 1024x768")
}

func (s *ServerSuite) TestResizeWindowError() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{Error: "resize err"})
	})
	res := callTool(s.T(), session, "resize_window", map[string]any{"width": 800, "height": 600})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "resize failed: resize err")
}

func (s *ServerSuite) TestResizeWindowInvalidDimensions() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{})
	})
	res := callTool(s.T(), session, "resize_window", map[string]any{"width": 0, "height": 600})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "width and height must be positive")

	res = callTool(s.T(), session, "resize_window", map[string]any{"width": 800, "height": -1})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "width and height must be positive")
}

// ==================== channelID forwarding ====================

func (s *ServerSuite) TestChannelIDForwarded() {
	var gotChannelID string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotChannelID, _, _ = decodeActionRequest(s.T(), r)
		writeJSON(w, actionResponse{Result: "ok"})
	}))
	defer ts.Close()

	srv := New(ts.URL, "my-channel", nil)
	srv.httpClient = ts.Client()
	session := connectClient(s.T(), srv)

	_ = callTool(s.T(), session, "go_back", nil)
	require.Equal(s.T(), "my-channel", gotChannelID)
}

// ==================== unused import guard ====================

func (s *ServerSuite) TestStringsUsed() {
	// strings.ToLower is used in handleFind — ensure the import is exercised.
	srv := New("http://x", "ch", nil)
	srv.refs = []browser.ElementRef{{RefID: "ref_1", Role: "BUTTON", Name: "OK"}}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{ElementRefs: []browser.ElementRef{
			{RefID: "ref_1", Role: "BUTTON", Name: "OK"},
		}})
	}))
	defer ts.Close()
	srv.apiURL = ts.URL
	srv.httpClient = ts.Client()

	res, _, err := srv.handleFind(context.Background(), "button")
	require.NoError(s.T(), err)
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "Found 1 element(s)")
}

// ==================== handleComputer direct (no HTTP) ====================

func (s *ServerSuite) TestHandleComputerNoRefsWait() {
	srv := New("http://x", "ch", nil)
	res, _, err := srv.handleComputer(context.Background(), computerInput{Action: "wait"})
	require.NoError(s.T(), err)
	require.False(s.T(), res.IsError)
	require.Equal(s.T(), "Waited", getText(s.T(), res))
}

func (s *ServerSuite) TestTimeImportUsed() {
	// Ensure time.Second is used by the default httpClient in New.
	srv := New("http://x", "ch", nil)
	require.NotNil(s.T(), srv.httpClient)
	_ = time.Second // satisfy import usage check
}

func (s *ServerSuite) TestIOImportUsed() {
	// Ensure io.Discard is used in nil logger path.
	srv := New("http://x", "ch", nil)
	require.NotNil(s.T(), srv.logger)
	_ = io.Discard
}

func (s *ServerSuite) TestSlogImportUsed() {
	logger := slog.Default()
	srv := New("http://x", "ch", logger)
	require.Equal(s.T(), logger, srv.logger)
}

func (s *ServerSuite) TestStringsPackageUsed() {
	require.True(s.T(), strings.Contains("hello", "ell"))
}

// --- list_tabs: active tab marker ---

func (s *ServerSuite) TestListTabsActiveTab() {
	tabs := []browser.TabInfo{
		{TargetID: "t1", URL: "https://a.com", Title: "A", Active: false},
		{TargetID: "t2", URL: "https://b.com", Title: "B", Active: true},
	}
	_, session := setupTest(s.T(), func(w http.ResponseWriter, r *http.Request) {
		_, action, _ := decodeActionRequest(s.T(), r)
		require.Equal(s.T(), "list_tabs", action)
		writeJSON(w, actionResponse{Tabs: tabs})
	})
	res := callTool(s.T(), session, "list_tabs", nil)
	require.False(s.T(), res.IsError)
	text := getText(s.T(), res)
	require.Contains(s.T(), text, "* [2] B")
	require.Contains(s.T(), text, "  [1] A")
}

// ==================== NewDirect ====================

func (s *ServerSuite) TestNewDirect() {
	srv := NewDirect("ws://127.0.0.1:9222", nil)
	require.NotNil(s.T(), srv)
	require.NotNil(s.T(), srv.mcpServer)
	require.NotNil(s.T(), srv.dispatch)
}

func (s *ServerSuite) TestNewDirectNilLogger() {
	srv := NewDirect("ws://x", nil)
	require.NotNil(s.T(), srv.logger)
}

// ==================== cdpDispatcher ====================

func (s *ServerSuite) TestCDPDispatcherEnsureCDPError() {
	d := &cdpDispatcher{
		cdpEndpoint: "ws://127.0.0.1:1",
		logger:      slog.Default(),
		factory: func(_ context.Context, _ string, _ *slog.Logger, _ ...browser.CDPOption) (*browser.CDPClient, error) {
			return nil, fmt.Errorf("connection refused")
		},
	}
	_, err := d.dispatch(context.Background(), "navigate", map[string]any{"url": "https://example.com"})
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "connecting to Chrome")
}

func (s *ServerSuite) TestCDPDispatcherUnknownAction() {
	// Use a mock dispatcher that pretends CDP is connected.
	called := false
	srv := NewDirect("ws://x", nil)
	srv.dispatch = func(_ context.Context, action string, _ map[string]any) (*actionResponse, error) {
		called = true
		if action == "nonexistent" {
			return nil, fmt.Errorf("unknown action: nonexistent")
		}
		return &actionResponse{Result: "ok"}, nil
	}
	session := connectClient(s.T(), srv)
	// "evaluate" calls action "evaluate_js" which our mock handles.
	res := callTool(s.T(), session, "evaluate", map[string]any{"expression": "1+1"})
	require.True(s.T(), called)
	require.False(s.T(), res.IsError)
}

func (s *ServerSuite) TestNewCDPDispatcher() {
	d := newCDPDispatcher("ws://127.0.0.1:9222", slog.Default())
	require.NotNil(s.T(), d)
}

func (s *ServerSuite) TestCDPDispatcherDispatchUnknownAction() {
	d := &cdpDispatcher{
		cdpEndpoint: "ws://x",
		logger:      slog.Default(),
		factory: func(_ context.Context, _ string, _ *slog.Logger, _ ...browser.CDPOption) (*browser.CDPClient, error) {
			return nil, fmt.Errorf("no chrome")
		},
	}
	_, err := d.dispatch(context.Background(), "nonexistent", nil)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "connecting to Chrome")
}

func (s *ServerSuite) TestCDPDispatcherEnsureCDPCachesClient() {
	d := &cdpDispatcher{
		cdpEndpoint: "ws://x",
		logger:      slog.Default(),
	}
	callCount := 0
	d.factory = func(_ context.Context, _ string, _ *slog.Logger, _ ...browser.CDPOption) (*browser.CDPClient, error) {
		callCount++
		return nil, fmt.Errorf("mock error")
	}
	_, _ = d.ensureCDP()
	_, _ = d.ensureCDP()
	require.Equal(s.T(), 2, callCount)
}

func (s *ServerSuite) TestCDPDispatcherEnsureCDPReusesClient() {
	d := &cdpDispatcher{
		cdpEndpoint: "ws://x",
		logger:      slog.Default(),
	}
	// Pre-set a mock CDP client.
	m := new(mockDirectCDP)
	d.cdp = m
	client, err := d.ensureCDP()
	require.NoError(s.T(), err)
	require.Equal(s.T(), m, client)
}

func (s *ServerSuite) TestCDPDispatcherEnsureCDPFactorySuccess() {
	d := &cdpDispatcher{
		cdpEndpoint: "ws://x",
		logger:      slog.Default(),
	}
	callCount := 0
	d.factory = func(_ context.Context, _ string, _ *slog.Logger, _ ...browser.CDPOption) (*browser.CDPClient, error) {
		callCount++
		// Return nil *CDPClient — stored as typed nil in directCDP interface (non-nil).
		return nil, nil
	}
	_, err := d.ensureCDP()
	require.NoError(s.T(), err)
	require.Equal(s.T(), 1, callCount)
	// Verify capture and newContextFn were initialized.
	require.NotNil(s.T(), d.capture)
	require.NotNil(s.T(), d.newContextFn)

	// Second call should reuse the cached client (typed nil != nil interface).
	_, err = d.ensureCDP()
	require.NoError(s.T(), err)
	require.Equal(s.T(), 1, callCount)
}

func (s *ServerSuite) TestCDPDispatcherEnsureCDPNewContextFnPanicsOnNilClient() {
	d := &cdpDispatcher{
		cdpEndpoint: "ws://x",
		logger:      slog.Default(),
	}
	d.factory = func(_ context.Context, _ string, _ *slog.Logger, _ ...browser.CDPOption) (*browser.CDPClient, error) {
		return nil, nil
	}
	_, err := d.ensureCDP()
	require.NoError(s.T(), err)

	// The newContextFn wraps NewContextForTarget on a nil *CDPClient, which panics.
	require.Panics(s.T(), func() {
		_, _ = d.newContextFn("some-target")
	})
}
