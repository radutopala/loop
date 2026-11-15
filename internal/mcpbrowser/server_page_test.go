package mcpbrowser

import (
	"context"
	"net/http"
	"testing"

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

// ==================== find_elements with a query ====================

// findPageHandler answers the page scan with refs and the page info with a
// fixed URL and title.
func findPageHandler(t *testing.T, refs []browser.ElementRef) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, action, _ := decodeActionRequest(t, r)
		if action == "get_element_refs" {
			writeJSON(w, actionResponse{ElementRefs: refs})
			return
		}
		writeJSON(w, actionResponse{PageInfo: &browser.PageInfo{URL: "https://x.com", Title: "X"}})
	}
}

func (s *ServerSuite) TestFindElementsQuery() {
	refs := []browser.ElementRef{
		{RefID: "ref_1", Role: "button", Name: "Submit Form"},
		{RefID: "ref_2", Role: "link", Name: "Home Page"},
		{RefID: "ref_3", Role: "textbox", Name: "Search", Value: "hello"},
		{RefID: "ref_4", Role: "button", Name: "Cancel"},
	}
	all := "[ref_1] button: Submit Form\n[ref_2] link: Home Page\n[ref_3] textbox: Search (value: hello)\n[ref_4] button: Cancel\n"
	tests := []struct {
		name  string
		query string
		want  string
	}{
		{name: "no query lists everything", query: "", want: all},
		{name: "a blank query lists everything", query: "  ", want: all},
		{
			name: "words from name and role", query: "submit button",
			want: "1 of 4 elements match \"submit button\":\n[ref_1] button: Submit Form\n",
		},
		{
			name: "a role keeps page order", query: "BUTTON",
			want: "2 of 4 elements match \"BUTTON\":\n[ref_1] button: Submit Form\n[ref_4] button: Cancel\n",
		},
		{
			name: "the value counts", query: "hello",
			want: "1 of 4 elements match \"hello\":\n[ref_3] textbox: Search (value: hello)\n",
		},
		{
			name: "no match lists everything", query: "close button or X button",
			want: "No element contains every word of \"close button or X button\". All 4 elements:\n" + all,
		},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			srv, session := setupTest(s.T(), findPageHandler(s.T(), refs))
			res := callTool(s.T(), session, "find_elements", map[string]any{"query": tt.query})
			require.False(s.T(), res.IsError)
			require.Equal(s.T(), "Page: https://x.com — X\n\n"+tt.want, getText(s.T(), res))
			require.Len(s.T(), srv.refs, 4, "the refs are kept for computer and form_input")
		})
	}
}

func (s *ServerSuite) TestBrowserToolList() {
	srv := New("http://x", "ch", nil)
	session := connectClient(s.T(), srv)
	res, err := session.ListTools(context.Background(), nil)
	require.NoError(s.T(), err)
	names := map[string]bool{}
	for _, tool := range res.Tools {
		names[tool.Name] = true
	}
	require.True(s.T(), names["find_elements"])
	require.True(s.T(), names["get_page_text"])
	for _, gone := range []string{"read_page", "find", "go_back", "go_forward"} {
		require.False(s.T(), names[gone], "%s is folded into other tools", gone)
	}
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
