package mcpbrowser

import (
	"context"
	"fmt"
	"log/slog"
	"testing"

	cdpproto "github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/browser"
)

type mockDirectCDP struct{ mock.Mock }

func (m *mockDirectCDP) Navigate(ctx context.Context, url string) error {
	return m.Called(ctx, url).Error(0)
}
func (m *mockDirectCDP) Reload(ctx context.Context) error { return m.Called(ctx).Error(0) }
func (m *mockDirectCDP) GoBack(ctx context.Context) error { return m.Called(ctx).Error(0) }
func (m *mockDirectCDP) GoForward(ctx context.Context) error {
	return m.Called(ctx).Error(0)
}
func (m *mockDirectCDP) GetPageInfo(ctx context.Context) (*browser.PageInfo, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*browser.PageInfo), args.Error(1)
}
func (m *mockDirectCDP) GetElementRefs(ctx context.Context) ([]browser.ElementRef, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]browser.ElementRef), args.Error(1)
}
func (m *mockDirectCDP) MouseClick(ctx context.Context, x, y float64, button string, clickCount int) error {
	return m.Called(ctx, x, y, button, clickCount).Error(0)
}
func (m *mockDirectCDP) MouseMove(ctx context.Context, x, y float64, buttons int) error {
	return m.Called(ctx, x, y, buttons).Error(0)
}
func (m *mockDirectCDP) MouseScroll(ctx context.Context, x, y, deltaX, deltaY float64) error {
	return m.Called(ctx, x, y, deltaX, deltaY).Error(0)
}
func (m *mockDirectCDP) KeyPress(ctx context.Context, key string) error {
	return m.Called(ctx, key).Error(0)
}
func (m *mockDirectCDP) TypeText(ctx context.Context, text string) error {
	return m.Called(ctx, text).Error(0)
}
func (m *mockDirectCDP) Screenshot(ctx context.Context) ([]byte, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]byte), args.Error(1)
}
func (m *mockDirectCDP) EvaluateJS(ctx context.Context, expression string) (string, error) {
	args := m.Called(ctx, expression)
	return args.String(0), args.Error(1)
}
func (m *mockDirectCDP) ListTabs(ctx context.Context) ([]browser.TabInfo, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]browser.TabInfo), args.Error(1)
}
func (m *mockDirectCDP) NewTab(ctx context.Context, url string) (string, error) {
	args := m.Called(ctx, url)
	return args.String(0), args.Error(1)
}
func (m *mockDirectCDP) SwitchTarget(targetID string) error {
	return m.Called(targetID).Error(0)
}
func (m *mockDirectCDP) CloseTab(ctx context.Context, targetID string) error {
	return m.Called(ctx, targetID).Error(0)
}
func (m *mockDirectCDP) ResizeWindow(ctx context.Context, width, height int) error {
	return m.Called(ctx, width, height).Error(0)
}
func (m *mockDirectCDP) ClickRef(ctx context.Context, refs []browser.ElementRef, refIndex int) error {
	return m.Called(ctx, refs, refIndex).Error(0)
}
func (m *mockDirectCDP) MouseDown(ctx context.Context, x, y float64, button string) error {
	return m.Called(ctx, x, y, button).Error(0)
}
func (m *mockDirectCDP) MouseUp(ctx context.Context, x, y float64, button string) error {
	return m.Called(ctx, x, y, button).Error(0)
}
func (m *mockDirectCDP) EnableConsoleCapture(ctx context.Context, ch chan<- browser.ConsoleMessage) error {
	return m.Called(ctx, ch).Error(0)
}
func (m *mockDirectCDP) EnableNetworkCapture(ctx context.Context, ch chan<- browser.NetworkRequest) error {
	return m.Called(ctx, ch).Error(0)
}
func (m *mockDirectCDP) ScrollIntoView(ctx context.Context, backendNodeID cdpproto.BackendNodeID) error {
	return m.Called(ctx, backendNodeID).Error(0)
}

type CDPDispatcherSuite struct {
	suite.Suite
	mock *mockDirectCDP
	d    *cdpDispatcher
}

func TestCDPDispatcherSuite(t *testing.T) {
	suite.Run(t, new(CDPDispatcherSuite))
}

func (s *CDPDispatcherSuite) SetupTest() {
	s.mock = new(mockDirectCDP)
	s.d = &cdpDispatcher{
		cdpEndpoint: "ws://x",
		logger:      slog.Default(),
	}
	s.d.cdp = s.mock // inject mock, skip ensureCDP
}

func (s *CDPDispatcherSuite) TestNavigate() {
	s.mock.On("Navigate", mock.Anything, "https://example.com").Return(nil)
	s.mock.On("GetPageInfo", mock.Anything).Return(&browser.PageInfo{URL: "https://example.com", Title: "E"}, nil)
	resp, err := s.d.dispatch(context.Background(), "navigate", map[string]any{"url": "https://example.com"})
	require.NoError(s.T(), err)
	require.Contains(s.T(), resp.Result, "Navigated to https://example.com")
}

func (s *CDPDispatcherSuite) TestNavigateError() {
	s.mock.On("Navigate", mock.Anything, mock.Anything).Return(fmt.Errorf("nav err"))
	_, err := s.d.dispatch(context.Background(), "navigate", map[string]any{"url": "x"})
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "navigate failed")
}

func (s *CDPDispatcherSuite) TestNavigatePageInfoError() {
	s.mock.On("Navigate", mock.Anything, "x").Return(nil)
	s.mock.On("GetPageInfo", mock.Anything).Return(nil, fmt.Errorf("info err"))
	resp, err := s.d.dispatch(context.Background(), "navigate", map[string]any{"url": "x"})
	require.NoError(s.T(), err)
	require.Equal(s.T(), "Navigated", resp.Result)
}

func (s *CDPDispatcherSuite) TestReload() {
	s.mock.On("Reload", mock.Anything).Return(nil)
	resp, err := s.d.dispatch(context.Background(), "reload", nil)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "Page reloaded", resp.Result)
}

func (s *CDPDispatcherSuite) TestReloadError() {
	s.mock.On("Reload", mock.Anything).Return(fmt.Errorf("err"))
	_, err := s.d.dispatch(context.Background(), "reload", nil)
	require.ErrorContains(s.T(), err, "reload failed")
}

func (s *CDPDispatcherSuite) TestGoBack() {
	s.mock.On("GoBack", mock.Anything).Return(nil)
	resp, err := s.d.dispatch(context.Background(), "go_back", nil)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "Navigated back", resp.Result)
}

func (s *CDPDispatcherSuite) TestGoBackError() {
	s.mock.On("GoBack", mock.Anything).Return(fmt.Errorf("err"))
	_, err := s.d.dispatch(context.Background(), "go_back", nil)
	require.ErrorContains(s.T(), err, "go back failed")
}

func (s *CDPDispatcherSuite) TestGoForward() {
	s.mock.On("GoForward", mock.Anything).Return(nil)
	resp, err := s.d.dispatch(context.Background(), "go_forward", nil)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "Navigated forward", resp.Result)
}

func (s *CDPDispatcherSuite) TestGoForwardError() {
	s.mock.On("GoForward", mock.Anything).Return(fmt.Errorf("err"))
	_, err := s.d.dispatch(context.Background(), "go_forward", nil)
	require.ErrorContains(s.T(), err, "go forward failed")
}

func (s *CDPDispatcherSuite) TestGetPageInfo() {
	s.mock.On("GetPageInfo", mock.Anything).Return(&browser.PageInfo{URL: "u", Title: "t"}, nil)
	resp, err := s.d.dispatch(context.Background(), "get_page_info", nil)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "u", resp.PageInfo.URL)
}

func (s *CDPDispatcherSuite) TestGetPageInfoError() {
	s.mock.On("GetPageInfo", mock.Anything).Return(nil, fmt.Errorf("err"))
	_, err := s.d.dispatch(context.Background(), "get_page_info", nil)
	require.ErrorContains(s.T(), err, "get page info failed")
}

func (s *CDPDispatcherSuite) TestGetElementRefs() {
	refs := []browser.ElementRef{{RefID: "ref_1", Role: "button"}}
	s.mock.On("GetElementRefs", mock.Anything).Return(refs, nil)
	resp, err := s.d.dispatch(context.Background(), "get_element_refs", nil)
	require.NoError(s.T(), err)
	require.Len(s.T(), resp.ElementRefs, 1)
}

func (s *CDPDispatcherSuite) TestGetElementRefsError() {
	s.mock.On("GetElementRefs", mock.Anything).Return(nil, fmt.Errorf("err"))
	_, err := s.d.dispatch(context.Background(), "get_element_refs", nil)
	require.ErrorContains(s.T(), err, "get element refs failed")
}

func (s *CDPDispatcherSuite) TestMouseClick() {
	s.mock.On("MouseClick", mock.Anything, 10.0, 20.0, "left", 1).Return(nil)
	resp, err := s.d.dispatch(context.Background(), "mouse_click", map[string]any{"x": 10.0, "y": 20.0})
	require.NoError(s.T(), err)
	require.Contains(s.T(), resp.Result, "Clicked at (10, 20)")
}

func (s *CDPDispatcherSuite) TestMouseClickError() {
	s.mock.On("MouseClick", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(fmt.Errorf("err"))
	_, err := s.d.dispatch(context.Background(), "mouse_click", map[string]any{"x": 0.0, "y": 0.0})
	require.ErrorContains(s.T(), err, "mouse click failed")
}

func (s *CDPDispatcherSuite) TestMouseMove() {
	s.mock.On("MouseMove", mock.Anything, 5.0, 6.0, 0).Return(nil)
	resp, err := s.d.dispatch(context.Background(), "mouse_move", map[string]any{"x": 5.0, "y": 6.0})
	require.NoError(s.T(), err)
	require.Contains(s.T(), resp.Result, "Moved to (5, 6)")
}

func (s *CDPDispatcherSuite) TestMouseMoveWithButtons() {
	s.mock.On("MouseMove", mock.Anything, 5.0, 6.0, 1).Return(nil)
	resp, err := s.d.dispatch(context.Background(), "mouse_move", map[string]any{"x": 5.0, "y": 6.0, "buttons": 1.0})
	require.NoError(s.T(), err)
	require.Contains(s.T(), resp.Result, "Moved to (5, 6)")
}

func (s *CDPDispatcherSuite) TestMouseMoveError() {
	s.mock.On("MouseMove", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(fmt.Errorf("err"))
	_, err := s.d.dispatch(context.Background(), "mouse_move", map[string]any{"x": 0.0, "y": 0.0})
	require.ErrorContains(s.T(), err, "mouse move failed")
}

func (s *CDPDispatcherSuite) TestMouseScroll() {
	s.mock.On("MouseScroll", mock.Anything, 0.0, 0.0, 0.0, 100.0).Return(nil)
	resp, err := s.d.dispatch(context.Background(), "mouse_scroll", map[string]any{"delta_y": 100.0})
	require.NoError(s.T(), err)
	require.Equal(s.T(), "Scrolled", resp.Result)
}

func (s *CDPDispatcherSuite) TestMouseScrollError() {
	s.mock.On("MouseScroll", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(fmt.Errorf("err"))
	_, err := s.d.dispatch(context.Background(), "mouse_scroll", nil)
	require.ErrorContains(s.T(), err, "scroll failed")
}

func (s *CDPDispatcherSuite) TestKeyPress() {
	s.mock.On("KeyPress", mock.Anything, "Enter").Return(nil)
	resp, err := s.d.dispatch(context.Background(), "key_press", map[string]any{"key": "Enter"})
	require.NoError(s.T(), err)
	require.Contains(s.T(), resp.Result, "Pressed Enter")
}

func (s *CDPDispatcherSuite) TestKeyPressError() {
	s.mock.On("KeyPress", mock.Anything, mock.Anything).Return(fmt.Errorf("err"))
	_, err := s.d.dispatch(context.Background(), "key_press", map[string]any{"key": "x"})
	require.ErrorContains(s.T(), err, "key press failed")
}

func (s *CDPDispatcherSuite) TestTypeText() {
	s.mock.On("TypeText", mock.Anything, "hello").Return(nil)
	resp, err := s.d.dispatch(context.Background(), "type_text", map[string]any{"text": "hello"})
	require.NoError(s.T(), err)
	require.Equal(s.T(), "Typed text", resp.Result)
}

func (s *CDPDispatcherSuite) TestTypeTextError() {
	s.mock.On("TypeText", mock.Anything, mock.Anything).Return(fmt.Errorf("err"))
	_, err := s.d.dispatch(context.Background(), "type_text", map[string]any{"text": "x"})
	require.ErrorContains(s.T(), err, "type text failed")
}

func (s *CDPDispatcherSuite) TestScreenshot() {
	s.mock.On("Screenshot", mock.Anything).Return([]byte{1, 2, 3}, nil)
	resp, err := s.d.dispatch(context.Background(), "screenshot", nil)
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), resp.Image)
}

func (s *CDPDispatcherSuite) TestScreenshotError() {
	s.mock.On("Screenshot", mock.Anything).Return(nil, fmt.Errorf("err"))
	_, err := s.d.dispatch(context.Background(), "screenshot", nil)
	require.ErrorContains(s.T(), err, "screenshot failed")
}

func (s *CDPDispatcherSuite) TestEvaluateJS() {
	s.mock.On("EvaluateJS", mock.Anything, "1+1").Return("2", nil)
	resp, err := s.d.dispatch(context.Background(), "evaluate_js", map[string]any{"expression": "1+1"})
	require.NoError(s.T(), err)
	require.Equal(s.T(), "2", resp.Result)
}

func (s *CDPDispatcherSuite) TestEvaluateJSError() {
	s.mock.On("EvaluateJS", mock.Anything, mock.Anything).Return("", fmt.Errorf("err"))
	_, err := s.d.dispatch(context.Background(), "evaluate_js", map[string]any{"expression": "x"})
	require.ErrorContains(s.T(), err, "evaluate failed")
}

func (s *CDPDispatcherSuite) TestListTabs() {
	tabs := []browser.TabInfo{{TargetID: "t1", URL: "u"}}
	s.mock.On("ListTabs", mock.Anything).Return(tabs, nil)
	resp, err := s.d.dispatch(context.Background(), "list_tabs", nil)
	require.NoError(s.T(), err)
	require.Len(s.T(), resp.Tabs, 1)
}

func (s *CDPDispatcherSuite) TestListTabsError() {
	s.mock.On("ListTabs", mock.Anything).Return(nil, fmt.Errorf("err"))
	_, err := s.d.dispatch(context.Background(), "list_tabs", nil)
	require.ErrorContains(s.T(), err, "list tabs failed")
}

func (s *CDPDispatcherSuite) TestNewTab() {
	newMock := new(mockDirectCDP)
	s.mock.On("NewTab", mock.Anything, "https://x.com").Return("t2", nil)
	s.d.newContextFn = func(targetID string) (directCDP, error) {
		require.Equal(s.T(), "t2", targetID)
		return newMock, nil
	}
	newMock.On("SwitchTarget", "t2").Return(nil)
	newMock.On("EnableConsoleCapture", mock.Anything, mock.Anything).Return(nil)
	newMock.On("EnableNetworkCapture", mock.Anything, mock.Anything).Return(nil)
	s.d.capture = &browser.CaptureState{Started: true}
	resp, err := s.d.dispatch(context.Background(), "new_tab", map[string]any{"url": "https://x.com"})
	require.NoError(s.T(), err)
	require.Contains(s.T(), resp.Result, "Opened new tab t2")
	newMock.AssertCalled(s.T(), "EnableConsoleCapture", mock.Anything, mock.Anything)
	newMock.AssertCalled(s.T(), "EnableNetworkCapture", mock.Anything, mock.Anything)
}

func (s *CDPDispatcherSuite) TestNewTabDefault() {
	newMock := new(mockDirectCDP)
	s.mock.On("NewTab", mock.Anything, "about:blank").Return("t2", nil)
	s.d.newContextFn = func(targetID string) (directCDP, error) {
		return newMock, nil
	}
	newMock.On("SwitchTarget", "t2").Return(nil)
	newMock.On("EnableConsoleCapture", mock.Anything, mock.Anything).Return(nil)
	newMock.On("EnableNetworkCapture", mock.Anything, mock.Anything).Return(nil)
	s.d.capture = &browser.CaptureState{Started: true}
	resp, err := s.d.dispatch(context.Background(), "new_tab", nil)
	require.NoError(s.T(), err)
	require.Contains(s.T(), resp.Result, "t2")
}

func (s *CDPDispatcherSuite) TestNewTabError() {
	s.mock.On("NewTab", mock.Anything, mock.Anything).Return("", fmt.Errorf("err"))
	_, err := s.d.dispatch(context.Background(), "new_tab", nil)
	require.ErrorContains(s.T(), err, "new tab failed")
}

func (s *CDPDispatcherSuite) TestNewTabSwitchError() {
	s.mock.On("NewTab", mock.Anything, "https://x.com").Return("t2", nil)
	s.d.newContextFn = func(_ string) (directCDP, error) {
		return nil, fmt.Errorf("switch err")
	}
	_, err := s.d.dispatch(context.Background(), "new_tab", map[string]any{"url": "https://x.com"})
	require.ErrorContains(s.T(), err, "new tab created but switch failed")
}

func (s *CDPDispatcherSuite) TestSwitchTab() {
	newMock := new(mockDirectCDP)
	s.d.newContextFn = func(targetID string) (directCDP, error) {
		require.Equal(s.T(), "t1", targetID)
		return newMock, nil
	}
	newMock.On("SwitchTarget", "t1").Return(nil)
	newMock.On("EnableConsoleCapture", mock.Anything, mock.Anything).Return(nil)
	newMock.On("EnableNetworkCapture", mock.Anything, mock.Anything).Return(nil)
	s.d.capture = &browser.CaptureState{Started: true}
	resp, err := s.d.dispatch(context.Background(), "switch_tab", map[string]any{"target_id": "t1"})
	require.NoError(s.T(), err)
	require.Contains(s.T(), resp.Result, "Switched to tab t1")
	newMock.AssertCalled(s.T(), "EnableConsoleCapture", mock.Anything, mock.Anything)
	newMock.AssertCalled(s.T(), "EnableNetworkCapture", mock.Anything, mock.Anything)
}

func (s *CDPDispatcherSuite) TestSwitchTabNewContextError() {
	s.d.newContextFn = func(_ string) (directCDP, error) {
		return nil, fmt.Errorf("attach err")
	}
	_, err := s.d.dispatch(context.Background(), "switch_tab", map[string]any{"target_id": "t1"})
	require.ErrorContains(s.T(), err, "switch tab failed")
}

func (s *CDPDispatcherSuite) TestSwitchTabActivateError() {
	newMock := new(mockDirectCDP)
	s.d.newContextFn = func(_ string) (directCDP, error) {
		return newMock, nil
	}
	newMock.On("SwitchTarget", "t1").Return(fmt.Errorf("activate err"))
	_, err := s.d.dispatch(context.Background(), "switch_tab", map[string]any{"target_id": "t1"})
	require.ErrorContains(s.T(), err, "switch tab failed")
}

func (s *CDPDispatcherSuite) TestSwitchTabNoConnection() {
	s.d.newContextFn = nil
	_, err := s.d.dispatch(context.Background(), "switch_tab", map[string]any{"target_id": "t1"})
	require.ErrorContains(s.T(), err, "no CDP connection")
}

func (s *CDPDispatcherSuite) TestCloseTab() {
	s.mock.On("CloseTab", mock.Anything, "t1").Return(nil)
	resp, err := s.d.dispatch(context.Background(), "close_tab", map[string]any{"target_id": "t1"})
	require.NoError(s.T(), err)
	require.Contains(s.T(), resp.Result, "Closed tab t1")
}

func (s *CDPDispatcherSuite) TestCloseTabError() {
	s.mock.On("CloseTab", mock.Anything, mock.Anything).Return(fmt.Errorf("err"))
	_, err := s.d.dispatch(context.Background(), "close_tab", map[string]any{"target_id": "t1"})
	require.ErrorContains(s.T(), err, "close tab failed")
}

func (s *CDPDispatcherSuite) TestResizeWindow() {
	s.mock.On("ResizeWindow", mock.Anything, 800, 600).Return(nil)
	resp, err := s.d.dispatch(context.Background(), "resize_window", map[string]any{"width": 800.0, "height": 600.0})
	require.NoError(s.T(), err)
	require.Contains(s.T(), resp.Result, "Resized to 800x600")
}

func (s *CDPDispatcherSuite) TestResizeWindowError() {
	s.mock.On("ResizeWindow", mock.Anything, mock.Anything, mock.Anything).Return(fmt.Errorf("err"))
	_, err := s.d.dispatch(context.Background(), "resize_window", map[string]any{"width": 100.0, "height": 100.0})
	require.ErrorContains(s.T(), err, "resize failed")
}

func (s *CDPDispatcherSuite) TestUnknownAction() {
	_, err := s.d.dispatch(context.Background(), "nonexistent", nil)
	require.ErrorContains(s.T(), err, "unknown action: nonexistent")
}

func (s *CDPDispatcherSuite) TestMouseClickCustomButton() {
	s.mock.On("MouseClick", mock.Anything, 0.0, 0.0, "right", 2).Return(nil)
	resp, err := s.d.dispatch(context.Background(), "mouse_click", map[string]any{"button": "right", "click_count": 2.0})
	require.NoError(s.T(), err)
	require.Contains(s.T(), resp.Result, "Clicked")
}

func (s *CDPDispatcherSuite) TestReadConsoleNoCapture() {
	resp, err := s.d.dispatch(context.Background(), "read_console", nil)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "No console messages", resp.Result)
}

func (s *CDPDispatcherSuite) TestReadConsoleWithCapture() {
	cs := &browser.CaptureState{Started: true}
	cs.ConsoleMsgs = []browser.ConsoleMessage{
		{Level: "log", Text: "hello"},
	}
	s.d.capture = cs

	resp, err := s.d.dispatch(context.Background(), "read_console", nil)
	require.NoError(s.T(), err)
	require.Contains(s.T(), resp.Result, "hello")
}

func (s *CDPDispatcherSuite) TestReadConsoleWithParams() {
	cs := &browser.CaptureState{Started: true}
	cs.ConsoleMsgs = []browser.ConsoleMessage{
		{Level: "log", Text: "info msg"},
		{Level: "error", Text: "error msg"},
	}
	s.d.capture = cs

	resp, err := s.d.dispatch(context.Background(), "read_console", map[string]any{
		"only_errors": true,
		"limit":       10.0,
		"clear":       true,
	})
	require.NoError(s.T(), err)
	require.Contains(s.T(), resp.Result, "error msg")
	require.NotContains(s.T(), resp.Result, "info msg")
}

func (s *CDPDispatcherSuite) TestReadConsolePatternError() {
	cs := &browser.CaptureState{Started: true}
	s.d.capture = cs

	_, err := s.d.dispatch(context.Background(), "read_console", map[string]any{
		"pattern": "[invalid",
	})
	require.ErrorContains(s.T(), err, "read console failed")
}

func (s *CDPDispatcherSuite) TestReadNetworkNoCapture() {
	resp, err := s.d.dispatch(context.Background(), "read_network", nil)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "No network requests", resp.Result)
}

func (s *CDPDispatcherSuite) TestReadNetworkWithCapture() {
	cs := &browser.CaptureState{Started: true}
	cs.NetworkReqs = []browser.NetworkRequest{
		{URL: "https://example.com", Method: "GET", Status: 200},
	}
	s.d.capture = cs

	resp, err := s.d.dispatch(context.Background(), "read_network", nil)
	require.NoError(s.T(), err)
	require.Contains(s.T(), resp.Result, "example.com")
}

func (s *CDPDispatcherSuite) TestReadNetworkWithParams() {
	cs := &browser.CaptureState{Started: true}
	cs.NetworkReqs = []browser.NetworkRequest{
		{URL: "https://api.example.com", Method: "GET", Status: 200},
		{URL: "https://cdn.example.com", Method: "GET", Status: 200},
	}
	s.d.capture = cs

	resp, err := s.d.dispatch(context.Background(), "read_network", map[string]any{
		"pattern": "api",
		"limit":   10.0,
		"clear":   true,
	})
	require.NoError(s.T(), err)
	require.Contains(s.T(), resp.Result, "api.example.com")
	require.NotContains(s.T(), resp.Result, "cdn.example.com")
}

func (s *CDPDispatcherSuite) TestReadNetworkPatternError() {
	cs := &browser.CaptureState{Started: true}
	s.d.capture = cs

	_, err := s.d.dispatch(context.Background(), "read_network", map[string]any{
		"pattern": "[invalid",
	})
	require.ErrorContains(s.T(), err, "read network failed")
}

func (s *CDPDispatcherSuite) TestScrollIntoView() {
	s.mock.On("ScrollIntoView", mock.Anything, cdpproto.BackendNodeID(42)).Return(nil)
	resp, err := s.d.dispatch(context.Background(), "scroll_into_view", map[string]any{"backend_node_id": 42.0})
	require.NoError(s.T(), err)
	require.Equal(s.T(), "Scrolled into view", resp.Result)
}

func (s *CDPDispatcherSuite) TestScrollIntoViewError() {
	s.mock.On("ScrollIntoView", mock.Anything, mock.Anything).Return(fmt.Errorf("err"))
	_, err := s.d.dispatch(context.Background(), "scroll_into_view", map[string]any{"backend_node_id": 1.0})
	require.ErrorContains(s.T(), err, "scroll into view failed")
}

func (s *CDPDispatcherSuite) TestMouseDown() {
	s.mock.On("MouseDown", mock.Anything, 10.0, 20.0, "left").Return(nil)
	resp, err := s.d.dispatch(context.Background(), "mouse_down", map[string]any{"x": 10.0, "y": 20.0, "button": "left"})
	require.NoError(s.T(), err)
	require.Equal(s.T(), "Mouse down", resp.Result)
}

func (s *CDPDispatcherSuite) TestMouseDownError() {
	s.mock.On("MouseDown", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(fmt.Errorf("err"))
	_, err := s.d.dispatch(context.Background(), "mouse_down", map[string]any{"x": 0.0, "y": 0.0, "button": "left"})
	require.ErrorContains(s.T(), err, "mouse down failed")
}

func (s *CDPDispatcherSuite) TestMouseUp() {
	s.mock.On("MouseUp", mock.Anything, 10.0, 20.0, "left").Return(nil)
	resp, err := s.d.dispatch(context.Background(), "mouse_up", map[string]any{"x": 10.0, "y": 20.0, "button": "left"})
	require.NoError(s.T(), err)
	require.Equal(s.T(), "Mouse up", resp.Result)
}

func (s *CDPDispatcherSuite) TestMouseUpError() {
	s.mock.On("MouseUp", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(fmt.Errorf("err"))
	_, err := s.d.dispatch(context.Background(), "mouse_up", map[string]any{"x": 0.0, "y": 0.0, "button": "left"})
	require.ErrorContains(s.T(), err, "mouse up failed")
}

func (s *CDPDispatcherSuite) TestClickRef() {
	refs := []browser.ElementRef{{RefID: "ref_1", Role: "button"}}
	s.mock.On("ClickRef", mock.Anything, refs, 0).Return(nil)
	resp, err := s.d.dispatch(context.Background(), "click_ref", map[string]any{
		"refs":      refs,
		"ref_index": 0,
	})
	require.NoError(s.T(), err)
	require.Equal(s.T(), "Clicked ref", resp.Result)
}

func (s *CDPDispatcherSuite) TestClickRefError() {
	s.mock.On("ClickRef", mock.Anything, mock.Anything, mock.Anything).Return(fmt.Errorf("err"))
	resp, err := s.d.dispatch(context.Background(), "click_ref", map[string]any{
		"refs":      []browser.ElementRef{},
		"ref_index": 0,
	})
	require.Nil(s.T(), resp)
	require.ErrorContains(s.T(), err, "click ref failed")
}

func (s *CDPDispatcherSuite) TestEnsureCDPFactorySuccess() {
	// Exercise the factory success path of ensureCDP, including the
	// newContextFn closure it creates.
	noopRun := func(_ context.Context, _ ...chromedp.Action) error { return nil }
	noopExec := func(_ context.Context, _ string, _, _ any) error { return nil }

	d := &cdpDispatcher{
		cdpEndpoint: "ws://127.0.0.1:0",
		logger:      slog.Default(),
		factory: func(ctx context.Context, wsURL string, logger *slog.Logger, opts ...browser.CDPOption) (*browser.CDPClient, error) {
			opts = append(opts,
				browser.WithAllocator(func(parent context.Context, _ string) (context.Context, context.CancelFunc) {
					return context.WithCancel(parent)
				}),
				browser.WithRunFunc(noopRun),
				browser.WithExec(noopExec),
				browser.WithTargetID("fake-target"),
			)
			return browser.NewCDPClient(ctx, wsURL, logger, opts...)
		},
	}

	cdp, err := d.ensureCDP()
	require.NoError(s.T(), err)
	require.NotNil(s.T(), cdp)
	require.NotNil(s.T(), d.capture)
	require.NotNil(s.T(), d.newContextFn)

	// Call the newContextFn closure to cover its body (lines 84-93).
	newCDP, err := d.newContextFn("other-target")
	require.NoError(s.T(), err)
	require.NotNil(s.T(), newCDP)
}

func (s *CDPDispatcherSuite) TestEnsureCDPNewContextFnError() {
	// Exercise the newContextFn error path: NewContextForTarget fails
	// when the run function returns an error on the second invocation.
	callCount := 0
	failOnSecondRun := func(_ context.Context, _ ...chromedp.Action) error {
		callCount++
		if callCount > 1 {
			return fmt.Errorf("attach failed")
		}
		return nil
	}
	noopExec := func(_ context.Context, _ string, _, _ any) error { return nil }

	d := &cdpDispatcher{
		cdpEndpoint: "ws://127.0.0.1:0",
		logger:      slog.Default(),
		factory: func(ctx context.Context, wsURL string, logger *slog.Logger, opts ...browser.CDPOption) (*browser.CDPClient, error) {
			opts = append(opts,
				browser.WithAllocator(func(parent context.Context, _ string) (context.Context, context.CancelFunc) {
					return context.WithCancel(parent)
				}),
				browser.WithRunFunc(failOnSecondRun),
				browser.WithExec(noopExec),
				browser.WithTargetID("fake-target"),
			)
			return browser.NewCDPClient(ctx, wsURL, logger, opts...)
		},
	}

	_, err := d.ensureCDP()
	require.NoError(s.T(), err)
	require.NotNil(s.T(), d.newContextFn)

	// Second call hits the error path inside newContextFn.
	_, err = d.newContextFn("bad-target")
	require.Error(s.T(), err)
}

func (s *CDPDispatcherSuite) TestEnsureCDPFactoryError() {
	d := &cdpDispatcher{
		cdpEndpoint: "ws://127.0.0.1:0",
		logger:      slog.Default(),
		factory: func(_ context.Context, _ string, _ *slog.Logger, _ ...browser.CDPOption) (*browser.CDPClient, error) {
			return nil, fmt.Errorf("connection refused")
		},
	}

	_, err := d.ensureCDP()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "connecting to Chrome")
}
