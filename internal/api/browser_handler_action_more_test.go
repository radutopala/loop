package api

import (
	"encoding/json"
	"errors"

	"github.com/chromedp/cdproto/cdp"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/browser"
)

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
