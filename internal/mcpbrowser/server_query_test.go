package mcpbrowser

import (
	"net/http"

	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/browser"
)

func (s *ServerSuite) TestGoBackSuccess() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, r *http.Request) {
		_, action, _ := decodeActionRequest(s.T(), r)
		require.Equal(s.T(), "go_back", action)
		writeJSON(w, actionResponse{Result: "ok"})
	})
	res := callTool(s.T(), session, "go_back", nil)
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "Navigated back")
}

func (s *ServerSuite) TestGoBackError() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{Error: "back err"})
	})
	res := callTool(s.T(), session, "go_back", nil)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "back failed: back err")
}

// ==================== go_forward ====================

func (s *ServerSuite) TestGoForwardSuccess() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, r *http.Request) {
		_, action, _ := decodeActionRequest(s.T(), r)
		require.Equal(s.T(), "go_forward", action)
		writeJSON(w, actionResponse{Result: "ok"})
	})
	res := callTool(s.T(), session, "go_forward", nil)
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "Navigated forward")
}

func (s *ServerSuite) TestGoForwardError() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{Error: "fwd err"})
	})
	res := callTool(s.T(), session, "go_forward", nil)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "forward failed: fwd err")
}

// ==================== reload ====================

func (s *ServerSuite) TestReloadSuccess() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, r *http.Request) {
		_, action, _ := decodeActionRequest(s.T(), r)
		require.Equal(s.T(), "reload", action)
		writeJSON(w, actionResponse{Result: "ok"})
	})
	res := callTool(s.T(), session, "reload", nil)
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "Page reloaded")
}

func (s *ServerSuite) TestReloadError() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{Error: "reload err"})
	})
	res := callTool(s.T(), session, "reload", nil)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "reload failed: reload err")
}

// ==================== evaluate ====================

func (s *ServerSuite) TestEvaluateSuccess() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, r *http.Request) {
		_, action, params := decodeActionRequest(s.T(), r)
		require.Equal(s.T(), "evaluate_js", action)
		require.Equal(s.T(), "1+1", params["expression"])
		writeJSON(w, actionResponse{Result: "2"})
	})
	res := callTool(s.T(), session, "evaluate", map[string]any{"expression": "1+1"})
	require.False(s.T(), res.IsError)
	require.Equal(s.T(), "2", getText(s.T(), res))
}

func (s *ServerSuite) TestEvaluateError() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{Error: "eval err"})
	})
	res := callTool(s.T(), session, "evaluate", map[string]any{"expression": "bad()"})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "evaluate failed: eval err")
}

func (s *ServerSuite) TestEvaluateEmptyExpression() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{})
	})
	res := callTool(s.T(), session, "evaluate", map[string]any{"expression": ""})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "expression is required")
}

// ==================== list_tabs ====================

func (s *ServerSuite) TestListTabsSuccess() {
	tabs := []browser.TabInfo{
		{TargetID: "t1", URL: "https://a.com", Title: "A"},
		{TargetID: "t2", URL: "https://b.com", Title: "B"},
	}
	_, session := setupTest(s.T(), func(w http.ResponseWriter, r *http.Request) {
		_, action, _ := decodeActionRequest(s.T(), r)
		require.Equal(s.T(), "list_tabs", action)
		writeJSON(w, actionResponse{Tabs: tabs})
	})
	res := callTool(s.T(), session, "list_tabs", nil)
	require.False(s.T(), res.IsError)
	text := getText(s.T(), res)
	require.Contains(s.T(), text, "[1] A")
	require.Contains(s.T(), text, "(id: t1)")
	require.Contains(s.T(), text, "[2] B")
}

func (s *ServerSuite) TestListTabsEmpty() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{Tabs: []browser.TabInfo{}})
	})
	res := callTool(s.T(), session, "list_tabs", nil)
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "No tabs open")
}

func (s *ServerSuite) TestListTabsError() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{Error: "tabs err"})
	})
	res := callTool(s.T(), session, "list_tabs", nil)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "list tabs failed: tabs err")
}

// ==================== new_tab ====================

func (s *ServerSuite) TestNewTabSuccess() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, r *http.Request) {
		_, action, params := decodeActionRequest(s.T(), r)
		require.Equal(s.T(), "new_tab", action)
		require.Equal(s.T(), "https://new.com", params["url"])
		writeJSON(w, actionResponse{Result: "Opened new tab (id: t99) at https://new.com"})
	})
	res := callTool(s.T(), session, "new_tab", map[string]any{"url": "https://new.com"})
	require.False(s.T(), res.IsError)
	text := getText(s.T(), res)
	require.Contains(s.T(), text, "Opened new tab (id: t99)")
	require.Contains(s.T(), text, "https://new.com")
}

func (s *ServerSuite) TestNewTabEmptyURL() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, r *http.Request) {
		_, _, params := decodeActionRequest(s.T(), r)
		require.Equal(s.T(), "about:blank", params["url"])
		writeJSON(w, actionResponse{Result: "Opened new tab (id: t0) at about:blank"})
	})
	res := callTool(s.T(), session, "new_tab", map[string]any{"url": ""})
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "about:blank")
}

func (s *ServerSuite) TestNewTabError() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{Error: "tab err"})
	})
	res := callTool(s.T(), session, "new_tab", map[string]any{"url": "https://fail.com"})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "new tab failed: tab err")
}

// ==================== switch_tab ====================

func (s *ServerSuite) TestSwitchTabSuccess() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, r *http.Request) {
		_, action, params := decodeActionRequest(s.T(), r)
		require.Equal(s.T(), "switch_tab", action)
		require.Equal(s.T(), "t1", params["target_id"])
		writeJSON(w, actionResponse{Result: "ok"})
	})
	res := callTool(s.T(), session, "switch_tab", map[string]any{"target_id": "t1"})
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "Switched to tab t1")
}

func (s *ServerSuite) TestSwitchTabError() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{Error: "switch err"})
	})
	res := callTool(s.T(), session, "switch_tab", map[string]any{"target_id": "t1"})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "switch tab failed: switch err")
}

func (s *ServerSuite) TestSwitchTabEmptyTargetID() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{})
	})
	res := callTool(s.T(), session, "switch_tab", map[string]any{"target_id": ""})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "target_id is required")
}

// ==================== close_tab ====================

func (s *ServerSuite) TestCloseTabSuccess() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, r *http.Request) {
		_, action, params := decodeActionRequest(s.T(), r)
		require.Equal(s.T(), "close_tab", action)
		require.Equal(s.T(), "t2", params["target_id"])
		writeJSON(w, actionResponse{Result: "ok"})
	})
	res := callTool(s.T(), session, "close_tab", map[string]any{"target_id": "t2"})
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "Closed tab t2")
}

func (s *ServerSuite) TestCloseTabError() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{Error: "close err"})
	})
	res := callTool(s.T(), session, "close_tab", map[string]any{"target_id": "t2"})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "close tab failed: close err")
}

func (s *ServerSuite) TestCloseTabEmptyTargetID() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{})
	})
	res := callTool(s.T(), session, "close_tab", map[string]any{"target_id": ""})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "target_id is required")
}

// ==================== page_info ====================
