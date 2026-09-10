package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/browser"
	"github.com/radutopala/loop/internal/browsercookies"
	"github.com/radutopala/loop/internal/config"
)

// fakeCookieReader stands in for a real browser profile and Keychain. Func
// fields rather than a mock: every test wants a different canned answer, and
// none of them care about call counts.
type fakeCookieReader struct {
	sources []browsercookies.Source
	cookies map[string][]browsercookies.Cookie
	err     error
}

func (f *fakeCookieReader) Sources() []browsercookies.Source { return f.sources }

func (f *fakeCookieReader) FindSource(id string) (browsercookies.Source, error) {
	for _, s := range f.sources {
		if s.ID() == id {
			return s, nil
		}
	}
	return browsercookies.Source{}, errors.New("no browser profile " + id)
}

func (f *fakeCookieReader) Cookies(src browsercookies.Source) ([]browsercookies.Cookie, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.cookies[src.ID()], nil
}

// secretValue is what must never appear in a response body or a log line.
const secretValue = "s3cr3t-session-token"

func (s *BrowserHandlerSuite) newCookieReader() *fakeCookieReader {
	return &fakeCookieReader{
		sources: []browsercookies.Source{
			{Browser: "chrome", Name: "Default", Path: "/p/chrome"},
			{Browser: "firefox", Name: "work", Path: "/p/firefox"},
		},
		cookies: map[string][]browsercookies.Cookie{
			"chrome:Default": {
				{Domain: ".example.com", Name: "sid", Value: secretValue, Path: "/", Expires: 1893456000, Secure: true, SameSite: "Lax"},
				{Domain: ".example.com", Name: "other", Value: "v2", Path: "/"},
				{Domain: "stripe.com", Name: "card", Value: "v3", Path: "/"},
				{Domain: "my-bank.example", Name: "b", Value: "v4", Path: "/"},
			},
			"firefox:work": {
				{Domain: "other.example", Name: "n", Value: "v", Path: "/"},
			},
		},
	}
}

// captureLogs points the server's logger at a buffer so a test can assert on
// what was — and was not — written.
func (s *BrowserHandlerSuite) captureLogs() *bytes.Buffer {
	buf := &bytes.Buffer{}
	s.srv.logger = slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return buf
}

func (s *BrowserHandlerSuite) getCookieSources(query string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/browser/cookies/sources"+query, nil)
	s.srv.browser.handleBrowserCookieSources(w, r)
	return w
}

func (s *BrowserHandlerSuite) postCookieImport(body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/browser/cookies/import", strings.NewReader(body))
	s.srv.browser.handleBrowserCookieImport(w, r)
	return w
}

/* ---------- sources ---------- */

func (s *BrowserHandlerSuite) TestCookieSources() {
	s.srv.browser.cookieReader = s.newCookieReader()

	w := s.getCookieSources("?channel_id=ch-1")
	require.Equal(s.T(), http.StatusOK, w.Code)

	var got []cookieSourceResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &got))
	require.Len(s.T(), got, 2)

	require.Equal(s.T(), "chrome:Default", got[0].ID)
	require.Empty(s.T(), got[0].Error)
	require.Equal(s.T(), []browsercookies.DomainSummary{
		{Domain: "example.com", Count: 2},
		{Domain: "my-bank.example", Count: 1},
		{Domain: "stripe.com", Count: 1},
	}, got[0].Domains)
	require.Equal(s.T(), "firefox:work", got[1].ID)

	// The promise the dialog makes: nothing but domains and counts crosses
	// this boundary.
	require.NotContains(s.T(), w.Body.String(), secretValue)
	require.NotContains(s.T(), w.Body.String(), "sid")
}

// One profile with a locked Keychain must not hide the others: the failure
// rides along on its own row.
func (s *BrowserHandlerSuite) TestCookieSourcesPerSourceError() {
	logs := s.captureLogs()
	reader := s.newCookieReader()
	reader.err = errors.New("the user denied Keychain access")
	s.srv.browser.cookieReader = reader

	w := s.getCookieSources("")
	require.Equal(s.T(), http.StatusOK, w.Code)

	var got []cookieSourceResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &got))
	require.Len(s.T(), got, 2)
	for _, src := range got {
		require.Contains(s.T(), src.Error, "denied Keychain access")
		require.Empty(s.T(), src.Domains)
	}
	require.Contains(s.T(), logs.String(), "reading profile failed")
}

func (s *BrowserHandlerSuite) TestCookieSourcesHostMode() {
	s.srv.browser.cookieReader = s.newCookieReader()
	s.srv.browser.modeMu.Lock()
	s.srv.browser.activeMode = map[string]string{"ch-1": "host"}
	s.srv.browser.modeMu.Unlock()

	w := s.getCookieSources("?channel_id=ch-1")
	require.Equal(s.T(), http.StatusConflict, w.Code)
	require.Contains(s.T(), w.Body.String(), "already uses your own browser profile")
}

func (s *BrowserHandlerSuite) TestCookieSourcesWithoutReader() {
	s.srv.browser.cookieReader = nil

	w := s.getCookieSources("")
	require.Equal(s.T(), http.StatusServiceUnavailable, w.Code)
}

/* ---------- import ---------- */

func (s *BrowserHandlerSuite) TestCookieImport() {
	logs := s.captureLogs()
	mockCDP := new(mockCDPSession)
	s.setupActionMocks(mockCDP)
	s.srv.browser.cookieReader = s.newCookieReader()

	mockCDP.On("SetCookies", mock.Anything, []browser.Cookie{
		{Domain: ".example.com", Name: "sid", Value: secretValue, Path: "/", Expires: 1893456000, Secure: true, SameSite: "Lax"},
		{Domain: ".example.com", Name: "other", Value: "v2", Path: "/"},
	}).Return(nil)

	w := s.postCookieImport(`{"channel_id":"ch-1","source":"chrome:Default","domains":["example.com"]}`)
	require.Equal(s.T(), http.StatusOK, w.Code)

	var resp cookieImportResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(s.T(), cookieImportResponse{Imported: 2, Domains: 1}, resp)

	// Unticked sites stay behind, and neither the body nor the log carries a
	// cookie value.
	mockCDP.AssertExpectations(s.T())
	require.NotContains(s.T(), w.Body.String(), secretValue)
	require.NotContains(s.T(), logs.String(), secretValue)
	require.Contains(s.T(), logs.String(), "cookie import")
}

func (s *BrowserHandlerSuite) TestCookieImportBadRequests() {
	s.srv.browser.cookieReader = s.newCookieReader()

	tests := []struct {
		name string
		body string
		want string
	}{
		{"not JSON", `{`, "invalid JSON"},
		{"no channel", `{"source":"chrome:Default","domains":["a"]}`, "channel_id required"},
		{"no source", `{"channel_id":"ch-1","domains":["a"]}`, "source required"},
		{"no domains", `{"channel_id":"ch-1","source":"chrome:Default"}`, "domains required"},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			w := s.postCookieImport(tt.body)
			require.Equal(s.T(), http.StatusBadRequest, w.Code)
			require.Contains(s.T(), w.Body.String(), tt.want)
		})
	}
}

func (s *BrowserHandlerSuite) TestCookieImportHostMode() {
	s.srv.browser.cookieReader = s.newCookieReader()
	s.srv.browser.modeMu.Lock()
	s.srv.browser.activeMode = map[string]string{"ch-1": "host"}
	s.srv.browser.modeMu.Unlock()

	w := s.postCookieImport(`{"channel_id":"ch-1","source":"chrome:Default","domains":["example.com"]}`)
	require.Equal(s.T(), http.StatusConflict, w.Code)
}

func (s *BrowserHandlerSuite) TestCookieImportWithoutReader() {
	s.srv.browser.cookieReader = nil

	w := s.postCookieImport(`{"channel_id":"ch-1","source":"chrome:Default","domains":["example.com"]}`)
	require.Equal(s.T(), http.StatusServiceUnavailable, w.Code)
}

func (s *BrowserHandlerSuite) TestCookieImportFailures() {
	unreadable := s.newCookieReader()
	unreadable.err = errors.New("keychain locked")

	tests := []struct {
		name   string
		reader CookieReader
		body   string
		want   string
	}{
		{
			name:   "the profile is gone",
			reader: s.newCookieReader(),
			body:   `{"channel_id":"ch-1","source":"chrome:Gone","domains":["example.com"]}`,
			want:   "no browser profile",
		},
		{
			name:   "the store will not open",
			reader: unreadable,
			body:   `{"channel_id":"ch-1","source":"chrome:Default","domains":["example.com"]}`,
			want:   "keychain locked",
		},
		{
			name:   "the chosen sites hold nothing",
			reader: s.newCookieReader(),
			body:   `{"channel_id":"ch-1","source":"chrome:Default","domains":["unvisited.example"]}`,
			want:   "no cookies found for the selected sites",
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.srv.browser.cookieReader = tt.reader
			w := s.postCookieImport(tt.body)
			require.Equal(s.T(), http.StatusInternalServerError, w.Code)
			require.Contains(s.T(), w.Body.String(), tt.want)
		})
	}
}

// The sidecar has to come up before anything can be put into it; if it will
// not, the import fails rather than reporting a success nobody got.
func (s *BrowserHandlerSuite) TestCookieImportBrowserUnavailable() {
	s.srv.browser.cookieReader = s.newCookieReader()
	s.browserMgr.On("EnsureBrowser", mock.Anything, "ch-1", "").Return(errors.New("no docker"))

	w := s.postCookieImport(`{"channel_id":"ch-1","source":"chrome:Default","domains":["example.com"]}`)
	require.Equal(s.T(), http.StatusInternalServerError, w.Code)
	require.Contains(s.T(), w.Body.String(), "ensuring browser")
}

func (s *BrowserHandlerSuite) TestCookieImportSetCookiesError() {
	mockCDP := new(mockCDPSession)
	s.setupActionMocks(mockCDP)
	s.srv.browser.cookieReader = s.newCookieReader()
	mockCDP.On("SetCookies", mock.Anything, mock.Anything).Return(errors.New("browser closed"))

	w := s.postCookieImport(`{"channel_id":"ch-1","source":"chrome:Default","domains":["example.com"]}`)
	require.Equal(s.T(), http.StatusInternalServerError, w.Code)
	require.Contains(s.T(), w.Body.String(), "browser closed")
}

/* ---------- auto-import ---------- */

// autoCookieConfig returns a config with the auto-import block filled in.
func autoCookieConfig(ci config.CookieImportConfig) func() (*config.Config, error) {
	return func() (*config.Config, error) {
		return &config.Config{Browser: config.BrowserConfig{CookieImport: ci}}, nil
	}
}

func (s *BrowserHandlerSuite) TestAutoImportCookies() {
	logs := s.captureLogs()
	s.srv.browser.cookieReader = s.newCookieReader()
	s.srv.configs.load = autoCookieConfig(config.CookieImportConfig{
		Source: "firefox:work", Domains: []string{"other.example"}, Auto: true,
	})

	mockCDP := new(mockCDPSession)
	mockCDP.On("SetCookies", mock.Anything, []browser.Cookie{
		{Domain: "other.example", Name: "n", Value: "v", Path: "/"},
	}).Return(nil)

	s.srv.browser.autoImportCookies(context.Background(), "ch-1", mockCDP)

	mockCDP.AssertExpectations(s.T())
	require.Contains(s.T(), logs.String(), "cookie auto-import")
	require.NotContains(s.T(), logs.String(), "value=v")
}

// Nothing here may stop a browser from starting, so every failure is a
// warning and the caller is never told.
func (s *BrowserHandlerSuite) TestAutoImportCookiesNeverFails() {
	unreadable := s.newCookieReader()
	unreadable.err = errors.New("keychain locked")

	tests := []struct {
		name       string
		reader     CookieReader
		cfg        func() (*config.Config, error)
		setCookies error
		wantLog    string
	}{
		{
			name:   "no reader on this machine",
			reader: nil,
			cfg:    autoCookieConfig(config.CookieImportConfig{Source: "firefox:work", Domains: []string{"other.example"}, Auto: true}),
		},
		{
			name:   "config will not load",
			reader: s.newCookieReader(),
			cfg:    func() (*config.Config, error) { return nil, errors.New("broken config") },
		},
		{
			name:   "auto is off",
			reader: s.newCookieReader(),
			cfg:    autoCookieConfig(config.CookieImportConfig{Source: "firefox:work", Domains: []string{"other.example"}}),
		},
		{
			name:   "auto is on but nothing was chosen",
			reader: s.newCookieReader(),
			cfg:    autoCookieConfig(config.CookieImportConfig{Auto: true}),
		},
		{
			name:    "the browser was uninstalled",
			reader:  s.newCookieReader(),
			cfg:     autoCookieConfig(config.CookieImportConfig{Source: "chrome:Gone", Domains: []string{"a"}, Auto: true}),
			wantLog: "cookie auto-import skipped",
		},
		{
			name:    "the store will not open",
			reader:  unreadable,
			cfg:     autoCookieConfig(config.CookieImportConfig{Source: "firefox:work", Domains: []string{"other.example"}, Auto: true}),
			wantLog: "cookie auto-import skipped",
		},
		{
			name:    "the chosen sites are gone from the profile",
			reader:  s.newCookieReader(),
			cfg:     autoCookieConfig(config.CookieImportConfig{Source: "firefox:work", Domains: []string{"stale.example"}, Auto: true}),
			wantLog: "cookie auto-import found nothing",
		},
		{
			name:       "the sidecar rejects them",
			reader:     s.newCookieReader(),
			cfg:        autoCookieConfig(config.CookieImportConfig{Source: "firefox:work", Domains: []string{"other.example"}, Auto: true}),
			setCookies: errors.New("browser closed"),
			wantLog:    "cookie auto-import failed",
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			logs := s.captureLogs()
			s.srv.browser.cookieReader = tt.reader
			s.srv.configs.load = tt.cfg

			mockCDP := new(mockCDPSession)
			mockCDP.On("SetCookies", mock.Anything, mock.Anything).Return(tt.setCookies).Maybe()

			s.srv.browser.autoImportCookies(context.Background(), "ch-1", mockCDP)

			if tt.wantLog != "" {
				require.Contains(s.T(), logs.String(), tt.wantLog)
			}
			require.NotContains(s.T(), logs.String(), "cookie auto-import channel_id")
		})
	}
}

// A channel whose sidecar never came up has no client to import into.
func (s *BrowserHandlerSuite) TestAutoImportCookiesWithoutClient() {
	s.srv.browser.cookieReader = s.newCookieReader()
	s.srv.browser.autoImportCookies(context.Background(), "ch-1", nil)
}
