package mcpbrowser

import (
	"net/http"

	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/browser"
)

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

func (s *ServerSuite) TestNewTab() {
	opened := actionResponse{Result: "Opened new tab t9"}
	tests := []struct {
		name    string
		url     string
		resps   []actionResponse // one per call, in order
		actions []string
		isError bool
		text    string
	}{
		{
			name:    "opens blank and navigates there",
			url:     "https://new.com",
			resps:   []actionResponse{opened, {Result: "ok", PageInfo: &browser.PageInfo{URL: "https://new.com/", Title: "New"}}},
			actions: []string{"new_tab", "navigate"},
			text:    "Opened new tab t9, at https://new.com/ — New",
		},
		{
			name:    "navigate without page info",
			url:     "https://new.com",
			resps:   []actionResponse{opened, {Result: "ok"}},
			actions: []string{"new_tab", "navigate"},
			text:    "Opened new tab t9, at https://new.com",
		},
		{
			name:    "no URL",
			resps:   []actionResponse{opened},
			actions: []string{"new_tab"},
			text:    "Opened new tab t9",
		},
		{
			name:    "about:blank",
			url:     "about:blank",
			resps:   []actionResponse{opened},
			actions: []string{"new_tab"},
			text:    "Opened new tab t9",
		},
		{
			name:    "opening fails",
			url:     "https://fail.com",
			resps:   []actionResponse{{Error: "tab err"}},
			actions: []string{"new_tab"},
			isError: true,
			text:    "new tab failed: tab err",
		},
		{
			name:    "navigating fails",
			url:     "https://fail.com",
			resps:   []actionResponse{opened, {Error: "nav err"}},
			actions: []string{"new_tab", "navigate"},
			isError: true,
			text:    "Opened new tab t9, but navigate failed: nav err",
		},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			var actions []string
			_, session := setupTest(s.T(), func(w http.ResponseWriter, r *http.Request) {
				_, action, params := decodeActionRequest(s.T(), r)
				if action == "new_tab" {
					require.Equal(s.T(), "about:blank", params["url"])
				} else {
					require.Equal(s.T(), tt.url, params["url"])
				}
				writeJSON(w, tt.resps[len(actions)])
				actions = append(actions, action)
			})
			res := callTool(s.T(), session, "new_tab", map[string]any{"url": tt.url})
			require.Equal(s.T(), tt.isError, res.IsError)
			require.Equal(s.T(), tt.text, getText(s.T(), res))
			require.Equal(s.T(), tt.actions, actions)
		})
	}
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
	require.Contains(s.T(), getText(s.T(), res), "index or target_id is required")
}

func (s *ServerSuite) TestTabIDAlias() {
	for _, tool := range []string{"switch_tab", "close_tab"} {
		s.Run(tool, func() {
			_, session := setupTest(s.T(), func(w http.ResponseWriter, r *http.Request) {
				_, action, params := decodeActionRequest(s.T(), r)
				require.Equal(s.T(), tool, action)
				require.Equal(s.T(), "t3", params["target_id"])
				writeJSON(w, actionResponse{Result: "ok"})
			})
			res := callTool(s.T(), session, tool, map[string]any{"tab_id": "t3"})
			require.False(s.T(), res.IsError, getText(s.T(), res))
			require.Contains(s.T(), getText(s.T(), res), "tab t3")
		})
	}
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
	require.Contains(s.T(), getText(s.T(), res), "index or target_id is required")
}

// ==================== page_info ====================
