package mcpbrowser

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/browser"
)

func (s *ServerSuite) TestNew() {
	srv := New("http://host:8222", "ch-1", nil)
	require.NotNil(s.T(), srv)
	require.Equal(s.T(), "http://host:8222", srv.apiURL)
	require.Equal(s.T(), "ch-1", srv.channelID)
	require.NotNil(s.T(), srv.mcpServer)
	require.NotNil(s.T(), srv.logger)
	require.NotNil(s.T(), srv.httpClient)
}

func (s *ServerSuite) TestNewWithLogger() {
	logger := slog.Default()
	srv := New("http://x", "ch-1", logger)
	require.Equal(s.T(), logger, srv.logger)
}

func (s *ServerSuite) TestNewNilLogger() {
	srv := New("http://x", "ch-1", nil)
	require.NotNil(s.T(), srv.logger)
}

// ==================== helper results ====================

func (s *ServerSuite) TestTextResult() {
	res := textResult("hello")
	require.False(s.T(), res.IsError)
	require.Len(s.T(), res.Content, 1)
	tc, ok := res.Content[0].(*mcp.TextContent)
	require.True(s.T(), ok)
	require.Equal(s.T(), "hello", tc.Text)
}

func (s *ServerSuite) TestErrorResult() {
	res := errorResult("boom")
	require.True(s.T(), res.IsError)
	require.Len(s.T(), res.Content, 1)
	tc, ok := res.Content[0].(*mcp.TextContent)
	require.True(s.T(), ok)
	require.Equal(s.T(), "boom", tc.Text)
}

func (s *ServerSuite) TestImageResult() {
	res := imageResult([]byte("png-data"))
	require.False(s.T(), res.IsError)
	require.Len(s.T(), res.Content, 1)
	ic, ok := res.Content[0].(*mcp.ImageContent)
	require.True(s.T(), ok)
	require.Equal(s.T(), []byte("png-data"), ic.Data)
	require.Equal(s.T(), "image/png", ic.MIMEType)
}

// ==================== Run ====================

func (s *ServerSuite) TestRunSuccess() {
	srv := New("http://localhost", "ch-1", nil)

	ctx, cancel := context.WithCancel(context.Background())
	t1, t2 := mcp.NewInMemoryTransports()

	done := make(chan error, 1)
	go func() {
		done <- srv.Run(ctx, t1)
	}()

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0.1"}, nil)
	_, err := client.Connect(ctx, t2, nil)
	require.NoError(s.T(), err)

	cancel()
	<-done
}

// ==================== callAction ====================

func (s *ServerSuite) TestCallActionSuccess() {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		channelID, action, _ := decodeActionRequest(s.T(), r)
		require.Equal(s.T(), "test-ch", channelID)
		require.Equal(s.T(), "navigate", action)
		writeJSON(w, actionResponse{PageInfo: &browser.PageInfo{URL: "https://x.com", Title: "X"}})
	}))
	defer ts.Close()

	srv := New(ts.URL, "test-ch", nil)
	srv.httpClient = ts.Client()

	resp, err := srv.callAction(context.Background(), "navigate", map[string]any{"url": "https://x.com"})
	require.NoError(s.T(), err)
	require.NotNil(s.T(), resp.PageInfo)
	require.Equal(s.T(), "https://x.com", resp.PageInfo.URL)
}

func (s *ServerSuite) TestCallActionAPIError() {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{Error: "something went wrong"})
	}))
	defer ts.Close()

	srv := New(ts.URL, "test-ch", nil)
	srv.httpClient = ts.Client()

	_, err := srv.callAction(context.Background(), "navigate", nil)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "something went wrong")
}

func (s *ServerSuite) TestCallActionNon200() {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	}))
	defer ts.Close()

	srv := New(ts.URL, "test-ch", nil)
	srv.httpClient = ts.Client()

	_, err := srv.callAction(context.Background(), "navigate", nil)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "host API returned 500")
}

func (s *ServerSuite) TestCallActionNetworkError() {
	srv := New("http://127.0.0.1:1", "test-ch", nil)
	srv.httpClient = &http.Client{Timeout: 100 * time.Millisecond}

	_, err := srv.callAction(context.Background(), "navigate", nil)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "calling host API")
}

func (s *ServerSuite) TestCallActionInvalidURL() {
	srv := New("://bad-url", "test-ch", nil)
	srv.httpClient = &http.Client{}

	_, err := srv.callAction(context.Background(), "navigate", nil)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "creating request")
}

func (s *ServerSuite) TestCallActionMarshalError() {
	srv := New("http://x", "test-ch", nil)
	// json.Marshal fails on channel values.
	ch := make(chan int)
	_, err := srv.callAction(context.Background(), "navigate", map[string]any{"bad": ch})
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "marshaling request")
}

func (s *ServerSuite) TestCallActionDecodeError() {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{bad json`))
	}))
	defer ts.Close()

	srv := New(ts.URL, "test-ch", nil)
	srv.httpClient = ts.Client()

	_, err := srv.callAction(context.Background(), "navigate", nil)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "decoding response")
}

// ==================== navigate ====================

func (s *ServerSuite) TestNavigateSuccess() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, r *http.Request) {
		_, action, params := decodeActionRequest(s.T(), r)
		require.Equal(s.T(), "navigate", action)
		require.Equal(s.T(), "https://x.com", params["url"])
		writeJSON(w, actionResponse{PageInfo: &browser.PageInfo{URL: "https://x.com", Title: "X"}})
	})
	res := callTool(s.T(), session, "navigate", map[string]any{"url": "https://x.com"})
	require.False(s.T(), res.IsError)
	text := getText(s.T(), res)
	require.Contains(s.T(), text, "https://x.com")
	require.Contains(s.T(), text, "X")
}

func (s *ServerSuite) TestNavigateNoPageInfo() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{Result: "ok"})
	})
	res := callTool(s.T(), session, "navigate", map[string]any{"url": "https://x.com"})
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "Navigated to https://x.com")
}

func (s *ServerSuite) TestNavigateError() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{Error: "timeout"})
	})
	res := callTool(s.T(), session, "navigate", map[string]any{"url": "https://bad.com"})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "navigate failed: timeout")
}

func (s *ServerSuite) TestNavigateEmptyURL() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{})
	})
	res := callTool(s.T(), session, "navigate", map[string]any{"url": ""})
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "url is required")
}

// ==================== read_page ====================

func (s *ServerSuite) TestReadPageSuccess() {
	refs := []browser.ElementRef{
		{RefID: "ref_1", Role: "button", Name: "Submit", Value: "go"},
		{RefID: "ref_2", Role: "link", Name: "Home"},
	}
	callCount := 0
	srv, session := setupTest(s.T(), func(w http.ResponseWriter, r *http.Request) {
		_, action, _ := decodeActionRequest(s.T(), r)
		callCount++
		if action == "get_element_refs" {
			writeJSON(w, actionResponse{ElementRefs: refs})
		} else {
			writeJSON(w, actionResponse{PageInfo: &browser.PageInfo{URL: "https://x.com", Title: "X"}})
		}
	})
	res := callTool(s.T(), session, "read_page", nil)
	require.False(s.T(), res.IsError)
	text := getText(s.T(), res)
	require.Contains(s.T(), text, "Page: https://x.com")
	require.Contains(s.T(), text, "[ref_1] button: Submit (value: go)")
	require.Contains(s.T(), text, "[ref_2] link: Home")
	require.Len(s.T(), srv.refs, 2)
}

func (s *ServerSuite) TestReadPageNoRefs() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, r *http.Request) {
		_, action, _ := decodeActionRequest(s.T(), r)
		if action == "get_element_refs" {
			writeJSON(w, actionResponse{ElementRefs: []browser.ElementRef{}})
		} else {
			writeJSON(w, actionResponse{})
		}
	})
	res := callTool(s.T(), session, "read_page", nil)
	require.False(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "No interactive elements found.")
}

func (s *ServerSuite) TestReadPageError() {
	_, session := setupTest(s.T(), func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, actionResponse{Error: "dom error"})
	})
	res := callTool(s.T(), session, "read_page", nil)
	require.True(s.T(), res.IsError)
	require.Contains(s.T(), getText(s.T(), res), "failed to get element refs: dom error")
}

// ==================== computer ====================
