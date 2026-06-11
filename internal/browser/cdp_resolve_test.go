package browser

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type ResolveWSURLSuite struct {
	suite.Suite
}

func TestResolveWSURLSuite(t *testing.T) {
	suite.Run(t, new(ResolveWSURLSuite))
}

// A full browser-level URL is returned unchanged (no /json/version lookup).
func (s *ResolveWSURLSuite) TestAlreadyFullURL() {
	in := "ws://127.0.0.1:9222/devtools/browser/abc-123"
	require.Equal(s.T(), in, resolveBrowserWSURL(in, slog.Default()))
}

// An unparseable / hostless URL is returned unchanged.
func (s *ResolveWSURLSuite) TestUnparseableOrHostless() {
	require.Equal(s.T(), "://nope", resolveBrowserWSURL("://nope", slog.Default()))
	require.Equal(s.T(), "ws://", resolveBrowserWSURL("ws://", slog.Default()))
}

// A connection error falls back to the bare URL (and exercises the logger branch).
func (s *ResolveWSURLSuite) TestConnectionErrorFallsBack() {
	in := "ws://127.0.0.1:1" // nothing listens on port 1
	require.Equal(s.T(), in, resolveBrowserWSURL(in, slog.Default()))
	// nil logger path is also safe.
	require.Equal(s.T(), in, resolveBrowserWSURL(in, nil))
}

// Malformed JSON or an empty webSocketDebuggerUrl falls back to the bare URL.
func (s *ResolveWSURLSuite) TestBadOrEmptyResponseFallsBack() {
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer bad.Close()
	badWS := "ws://" + strings.TrimPrefix(bad.URL, "http://")
	require.Equal(s.T(), badWS, resolveBrowserWSURL(badWS, slog.Default()))

	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"webSocketDebuggerUrl":""}`))
	}))
	defer empty.Close()
	emptyWS := "ws://" + strings.TrimPrefix(empty.URL, "http://")
	require.Equal(s.T(), emptyWS, resolveBrowserWSURL(emptyWS, slog.Default()))
}

// A valid /json/version response yields the full browser-level WS URL.
func (s *ResolveWSURLSuite) TestSuccessReturnsBrowserURL() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(s.T(), "/json/version", r.URL.Path)
		_, _ = w.Write([]byte(`{"webSocketDebuggerUrl":"ws://chrome:9222/devtools/browser/xyz"}`))
	}))
	defer srv.Close()
	in := "ws://" + strings.TrimPrefix(srv.URL, "http://")
	require.Equal(s.T(), "ws://chrome:9222/devtools/browser/xyz", resolveBrowserWSURL(in, slog.Default()))
}

// --- discoverFirstPageTarget ---

func (s *ResolveWSURLSuite) TestDiscoverHostless() {
	require.Equal(s.T(), "", discoverFirstPageTarget("://nope", slog.Default()))
}

func (s *ResolveWSURLSuite) TestDiscoverConnectionError() {
	require.Equal(s.T(), "", discoverFirstPageTarget("ws://127.0.0.1:1", slog.Default()))
	require.Equal(s.T(), "", discoverFirstPageTarget("ws://127.0.0.1:1", nil)) // nil logger path
}

func (s *ResolveWSURLSuite) TestDiscoverBadJSON() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()
	require.Equal(s.T(), "", discoverFirstPageTarget("ws://"+strings.TrimPrefix(srv.URL, "http://"), slog.Default()))
}

func (s *ResolveWSURLSuite) TestDiscoverNoPageTarget() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"type":"background_page","id":"bg-1"}]`))
	}))
	defer srv.Close()
	require.Equal(s.T(), "", discoverFirstPageTarget("ws://"+strings.TrimPrefix(srv.URL, "http://"), slog.Default()))
}

func (s *ResolveWSURLSuite) TestDiscoverReturnsFirstPageID() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(s.T(), "/json/list", r.URL.Path)
		_, _ = w.Write([]byte(`[{"type":"background_page","id":"bg-1"},{"type":"page","id":"PAGE-T0"},{"type":"page","id":"PAGE-T1"}]`))
	}))
	defer srv.Close()
	require.Equal(s.T(), "PAGE-T0", discoverFirstPageTarget("ws://"+strings.TrimPrefix(srv.URL, "http://"), slog.Default()))
}
