package mcpbrowser

import (
	"fmt"
	"net/http"

	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/browser"
)

func (s *ServerSuite) TestPageInfoSuccess() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, r *http.Request) {
		_, action, _ := decodeActionRequest(s.T(), r)
		require.Equal(s.T(), "get_page_info", action)
		writeJSON(w, actionResponse{PageInfo: &browser.PageInfo{URL: "https://x.com", Title: "X"}})
	})
	res := callTool(s.T(), session, "page_info", nil)
	require.False(s.T(), res.IsError)
	text := getText(s.T(), res)
	require.Contains(s.T(), text, "URL: https://x.com")
	require.Contains(s.T(), text, "Title: X")
}

func (s *ServerSuite) TestPageInfoError() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{Error: "info err"})
	})
	res := callTool(s.T(), session, "page_info", nil)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "page info failed: info err")
}

func (s *ServerSuite) TestPageInfoNilPageInfo() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{Result: "ok"})
	})
	res := callTool(s.T(), session, "page_info", nil)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "page info failed")
}

// ==================== get_page_text ====================

func (s *ServerSuite) TestGetPageTextSuccess() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, r *http.Request) {
		_, action, params := decodeActionRequest(s.T(), r)
		require.Equal(s.T(), "evaluate_js", action)
		require.Equal(s.T(), "document.body.innerText", params["expression"])
		writeJSON(w, actionResponse{Result: "Hello World\nThis is a page."})
	})
	res := callTool(s.T(), session, "get_page_text", nil)
	require.False(s.T(), res.IsError)
	require.Equal(s.T(), "Hello World\nThis is a page.", getText(s.T(), res))
}

func (s *ServerSuite) TestGetPageTextError() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{Error: "eval err"})
	})
	res := callTool(s.T(), session, "get_page_text", nil)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "get page text failed: eval err")
}

// ==================== find ====================

func (s *ServerSuite) TestFindSuccess() {
	refs := []browser.ElementRef{
		{RefID: "ref_1", Role: "button", Name: "Submit Form"},
		{RefID: "ref_2", Role: "link", Name: "Home Page"},
		{RefID: "ref_3", Role: "textbox", Name: "Email"},
	}
	srv, session := setupTest(s.T(), func(w http.ResponseWriter, r *http.Request) {
		_, action, _ := decodeActionRequest(s.T(), r)
		require.Equal(s.T(), "get_element_refs", action)
		writeJSON(w, actionResponse{ElementRefs: refs})
	})
	res := callTool(s.T(), session, "find", map[string]any{"query": "submit"})
	require.False(s.T(), res.IsError)
	text := getText(s.T(), res)
	require.Contains(s.T(), text, "Found 1 element(s)")
	require.Contains(s.T(), text, "[ref_1] button: Submit Form")
	require.Len(s.T(), srv.refs, 3)
}

func (s *ServerSuite) TestFindCaseInsensitive() {
	refs := []browser.ElementRef{
		{RefID: "ref_1", Role: "button", Name: "SUBMIT"},
	}
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{ElementRefs: refs})
	})
	res := callTool(s.T(), session, "find", map[string]any{"query": "submit"})
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "Found 1 element(s)")
}

func (s *ServerSuite) TestFindByRole() {
	refs := []browser.ElementRef{
		{RefID: "ref_1", Role: "button", Name: "Click Me"},
		{RefID: "ref_2", Role: "checkbox", Name: "Accept"},
	}
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{ElementRefs: refs})
	})
	res := callTool(s.T(), session, "find", map[string]any{"query": "checkbox"})
	require.False(s.T(), res.IsError)
	text := getText(s.T(), res)
	require.Contains(s.T(), text, "Found 1 element(s)")
	require.Contains(s.T(), text, "[ref_2] checkbox: Accept")
}

func (s *ServerSuite) TestFindNoMatch() {
	refs := []browser.ElementRef{
		{RefID: "ref_1", Role: "button", Name: "Submit"},
	}
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{ElementRefs: refs})
	})
	res := callTool(s.T(), session, "find", map[string]any{"query": "nonexistent"})
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), `No elements found matching "nonexistent"`)
}

func (s *ServerSuite) TestFindMaxResults() {
	var refs []browser.ElementRef
	for i := 1; i <= 25; i++ {
		refs = append(refs, browser.ElementRef{
			RefID: fmt.Sprintf("ref_%d", i),
			Role:  "button",
			Name:  fmt.Sprintf("Button %d", i),
		})
	}
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{ElementRefs: refs})
	})
	res := callTool(s.T(), session, "find", map[string]any{"query": "button"})
	require.False(s.T(), res.IsError)
	text := getText(s.T(), res)
	require.Contains(s.T(), text, "Found 20 element(s)")
	require.Contains(s.T(), text, "[ref_20]")
	require.NotContains(s.T(), text, "[ref_21]")
}

func (s *ServerSuite) TestFindEmptyQuery() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{})
	})
	res := callTool(s.T(), session, "find", map[string]any{"query": ""})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "query is required")
}

func (s *ServerSuite) TestFindError() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{Error: "ax tree err"})
	})
	res := callTool(s.T(), session, "find", map[string]any{"query": "button"})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "failed to get element refs: ax tree err")
}

func (s *ServerSuite) TestFindWithValue() {
	refs := []browser.ElementRef{
		{RefID: "ref_1", Role: "textbox", Name: "Search", Value: "hello"},
	}
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{ElementRefs: refs})
	})
	res := callTool(s.T(), session, "find", map[string]any{"query": "search"})
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "(value: hello)")
}

// ==================== read_console_messages ====================

func (s *ServerSuite) TestReadConsoleMessagesSuccess() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, r *http.Request) {
		_, action, params := decodeActionRequest(s.T(), r)
		require.Equal(s.T(), "read_console", action)
		require.Equal(s.T(), "error.*", params["pattern"])
		require.Equal(s.T(), true, params["only_errors"])
		require.Equal(s.T(), true, params["clear"])
		require.Equal(s.T(), float64(50), params["limit"])
		writeJSON(w, actionResponse{Result: "2 console message(s):\n[10:00:00] error: boom\n"})
	})
	res := callTool(s.T(), session, "read_console_messages", map[string]any{
		"pattern":    "error.*",
		"onlyErrors": true,
		"clear":      true,
		"limit":      50,
	})
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "2 console message(s)")
}

func (s *ServerSuite) TestReadConsoleMessagesEmpty() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{Result: "No console messages"})
	})
	res := callTool(s.T(), session, "read_console_messages", nil)
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "No console messages")
}

func (s *ServerSuite) TestReadConsoleMessagesError() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{Error: "console err"})
	})
	res := callTool(s.T(), session, "read_console_messages", nil)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "read console failed: console err")
}

// ==================== read_network_requests ====================

func (s *ServerSuite) TestReadNetworkRequestsSuccess() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, r *http.Request) {
		_, action, params := decodeActionRequest(s.T(), r)
		require.Equal(s.T(), "read_network", action)
		require.Equal(s.T(), "/api/", params["pattern"])
		require.Equal(s.T(), true, params["clear"])
		require.Equal(s.T(), float64(10), params["limit"])
		writeJSON(w, actionResponse{Result: "2 network request(s):\n[10:00:00] GET /api/users — 200 OK\n"})
	})
	res := callTool(s.T(), session, "read_network_requests", map[string]any{
		"pattern": "/api/",
		"clear":   true,
		"limit":   10,
	})
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "2 network request(s)")
}

func (s *ServerSuite) TestReadNetworkRequestsEmpty() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{Result: "No network requests"})
	})
	res := callTool(s.T(), session, "read_network_requests", nil)
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "No network requests")
}

func (s *ServerSuite) TestReadNetworkRequestsError() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{Error: "network err"})
	})
	res := callTool(s.T(), session, "read_network_requests", nil)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "read network failed: network err")
}

// ==================== resize_window ====================
