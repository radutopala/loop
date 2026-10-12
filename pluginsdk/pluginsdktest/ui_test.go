package pluginsdktest

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// MockBrowser stands in for Chrome; a test's Run plays the host page.
type MockBrowser struct {
	mock.Mock
}

func (m *MockBrowser) Open(ctx context.Context, url string) (func(), error) {
	args := m.Called(ctx, url)
	closePage, _ := args.Get(0).(func())
	return closePage, args.Error(1)
}

func chromeAt(path string) []chromedp.ExecAllocatorOption {
	return []chromedp.ExecAllocatorOption{chromedp.ExecPath(path)}
}

type UISuite struct {
	suite.Suite
	ctx     context.Context
	browser *MockBrowser
	closed  bool
}

func TestUISuite(t *testing.T) {
	suite.Run(t, new(UISuite))
}

func (s *UISuite) SetupTest() {
	s.ctx = context.Background()
	s.browser = new(MockBrowser)
	s.closed = false
}

func (s *UISuite) TearDownTest() {
	s.browser.AssertExpectations(s.T())
}

// host is the check's server as the host page sees it.
type host struct {
	t    *testing.T
	base string
}

func (h host) get(path string) (int, http.Header, string) {
	resp, err := http.Get(h.base + path)
	require.NoError(h.t, err)
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	require.NoError(h.t, err)
	return resp.StatusCode, resp.Header, string(body)
}

func (h host) post(path, body string) (int, string) {
	resp, err := http.Post(h.base+path, "application/json", strings.NewReader(body))
	require.NoError(h.t, err)
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	require.NoError(h.t, err)
	return resp.StatusCode, string(data)
}

func (h host) report(page int, kind, msg string) {
	data, err := json.Marshal(map[string]any{"page": page, "kind": kind, "message": msg})
	require.NoError(h.t, err)
	code, _ := h.post("/report", string(data))
	require.Equal(h.t, http.StatusNoContent, code)
}

// open expects one Open, which runs play as the host page.
func (s *UISuite) open(play func(h host)) {
	s.browser.On("Open", mock.Anything, mock.AnythingOfType("string")).Return(func() { s.closed = true }, nil).Once().
		Run(func(args mock.Arguments) {
			play(host{t: s.T(), base: strings.TrimSuffix(args.String(1), "/")})
		})
}

// plugin writes a /loop-plugin dir with manifest and files.
func (s *UISuite) plugin(manifest string, files map[string]string) string {
	dir := s.T().TempDir()
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(manifest), 0o644))
	for name, body := range files {
		p := filepath.Join(dir, name)
		require.NoError(s.T(), os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(s.T(), os.WriteFile(p, []byte(body), 0o644))
	}
	return dir
}

const uiManifest = `{"id": "p", "capabilities": ["ui.panel"],
	"settings": {"n": {"type": "number", "default": 2}, "s": {"type": "string"}, "x": null},
	"ui": {"panels": [{"id": "main", "entry": "ui/main.html"}], "cards": [{"id": "c", "entry": "ui/card.html"}]}}`

func (s *UISuite) TestServes() {
	dir := s.plugin(uiManifest, map[string]string{
		"ui/main.html":       "<!DOCTYPE html><html><HEAD><title>x</title></head></html>",
		"ui/card.html":       "<p>card</p>",
		"ui/app.js":          "export {}",
		"ui/sub/dir/keep.js": "",
		"secret.txt":         "no",
	})
	s.open(func(h host) {
		code, hdr, body := h.get("/")
		require.Equal(s.T(), http.StatusOK, code)
		require.Contains(s.T(), hdr.Get("Content-Type"), "text/html")
		require.Contains(s.T(), body, `<script src="/host.js">`)
		code, hdr, _ = h.get("/host.js")
		require.Equal(s.T(), http.StatusOK, code)
		require.Contains(s.T(), hdr.Get("Content-Type"), "javascript")

		code, hdr, body = h.get("/config")
		require.Equal(s.T(), http.StatusOK, code)
		require.Equal(s.T(), "application/json", hdr.Get("Content-Type"))
		require.JSONEq(s.T(), `{"pluginId": "p", "capabilities": ["ui.panel"], "settings": {"n": 2}, "pages": [
			{"kind": "panel", "id": "main", "entry": "ui/main.html", "url": "/p/0/ui/main.html"},
			{"kind": "card", "id": "c", "entry": "ui/card.html", "url": "/p/1/ui/card.html", "data": {"total": 1}}]}`, body)

		code, hdr, body = h.get("/p/0/ui/main.html")
		require.Equal(s.T(), http.StatusOK, code)
		require.Equal(s.T(), `<!DOCTYPE html><html><HEAD><script src="/p/0/.loop-check/probe.js"></script><title>x</title></head></html>`, body)
		require.Equal(s.T(), panelCSP(h.base+"/p/0/"), hdr.Get("Content-Security-Policy"))
		require.Equal(s.T(), "nosniff", hdr.Get("X-Content-Type-Options"))
		require.Equal(s.T(), "no-store", hdr.Get("Cache-Control"))
		require.Equal(s.T(), "*", hdr.Get("Access-Control-Allow-Origin"))
		_, _, body = h.get("/p/1/ui/card.html")
		require.Equal(s.T(), `<script src="/p/1/.loop-check/probe.js"></script><p>card</p>`, body)
		_, hdr, body = h.get("/p/0/ui/app.js")
		require.Equal(s.T(), "export {}", body, "only the entry gets the probe")
		require.Contains(s.T(), hdr.Get("Content-Type"), "javascript")
		code, hdr, body = h.get("/p/1/.loop-check/probe.js")
		require.Equal(s.T(), http.StatusOK, code)
		require.Contains(s.T(), body, "securitypolicyviolation")
		require.Equal(s.T(), panelCSP(h.base+"/p/1/"), hdr.Get("Content-Security-Policy"))

		for _, path := range []string{"/p/0/ui/nope.js", "/p/0/secret.txt", "/p/0/ui/sub", "/p/0/ui/../secret.txt", "/p/1/ui/%2e%2e/secret.txt"} {
			code, _, _ = h.get(path)
			require.Equal(s.T(), http.StatusNotFound, code, path)
		}
		for _, path := range []string{"/p/2/ui/main.html", "/p/x/ui/main.html", "/p/-1/ui/main.html"} {
			code, _, _ = h.get(path)
			require.Equal(s.T(), http.StatusNotFound, code, path)
		}
		h.report(0, "loaded", "")
		h.report(0, "connected", "")
		h.report(1, "loaded", "")
		h.report(1, "connected", "")
	})
	problems, err := CheckUI(s.ctx, dir, UIOptions{Browser: s.browser, Settle: time.Millisecond, CardData: map[string]any{"c": map[string]any{"total": 1}}})
	require.NoError(s.T(), err)
	require.True(s.T(), s.closed)
	var lines []string
	for _, p := range problems {
		lines = append(lines, p.String())
	}
	require.Equal(s.T(), []string{
		"panel main: load: GET ui/nope.js: no such file in the bundle",
		"panel main: load: GET secret.txt: outside ui/, which is all Loop serves",
		"panel main: load: GET ui/sub: no such file in the bundle",
		"panel main: load: GET secret.txt: outside ui/, which is all Loop serves",
		"card c: load: GET ui/../secret.txt: no such file in the bundle",
	}, lines)
}

func (s *UISuite) TestReports() {
	dir := s.plugin(uiManifest, nil)
	s.open(func(h host) {
		h.report(0, "csp", "script-src blocked inline")
		h.report(1, "error", "boom")
		h.report(1, "loaded", "")
		for _, body := range []string{"{", `{"page": 2}`, `{"page": -1}`} {
			code, _ := h.post("/report", body)
			require.Equal(s.T(), http.StatusBadRequest, code, body)
		}
	})
	problems, err := CheckUI(s.ctx, dir, UIOptions{Browser: s.browser, Settings: map[string]any{"a": true}, Timeout: 10 * time.Millisecond, Settle: time.Millisecond})
	require.NoError(s.T(), err)
	require.Equal(s.T(), []UIProblem{
		{Page: "panel main", Kind: UIProblemCSP, Message: "script-src blocked inline"},
		{Page: "card c", Kind: UIProblemError, Message: "boom"},
		{Page: "panel main", Kind: UIProblemLoad, Message: "the page didn't load within 10ms"},
		{Page: "card c", Kind: UIProblemBridge, Message: "the page didn't connect to the bridge within 10ms"},
	}, problems)
}

func (s *UISuite) TestNoBridge() {
	dir := s.plugin(uiManifest, nil)
	s.open(func(h host) {
		h.report(0, "loaded", "")
		h.report(1, "loaded", "")
	})
	problems, err := CheckUI(s.ctx, dir, UIOptions{Browser: s.browser, NoBridge: true, Settle: time.Millisecond})
	require.NoError(s.T(), err)
	require.Empty(s.T(), problems)

	s.open(func(h host) { h.report(0, "loaded", "") })
	problems, err = CheckUI(s.ctx, dir, UIOptions{Browser: s.browser, NoBridge: true, Timeout: time.Millisecond, Settle: time.Millisecond})
	require.NoError(s.T(), err)
	require.Equal(s.T(), []UIProblem{{Page: "card c", Kind: UIProblemLoad, Message: "the page didn't load within 1ms"}}, problems)
}

func (s *UISuite) TestAPI() {
	dir := s.plugin(uiManifest, nil)
	var got []string
	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got = append(got, r.Method+" "+r.URL.String()+" "+r.Header.Get("Content-Type")+" "+string(body))
		switch r.URL.Path {
		case "/json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"a":1}`))
		case "/broken":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{`))
		default:
			http.Error(w, "nope", http.StatusTeapot)
		}
	})
	s.open(func(h host) {
		for _, tt := range []struct {
			call string
			code int
			want string
		}{
			{`{"method": "post", "path": "json?x=1", "body": {"b": 2}}`, http.StatusOK, `{"status": 200, "body": {"a": 1}}`},
			{`{"method": "PUT", "path": "/text", "body": "hi"}`, http.StatusOK, `{"status": 418, "body": "nope\n"}`},
			{`{"method": "GET", "path": "/broken", "body": null}`, http.StatusOK, `{"status": 200, "body": "{"}`},
			{`{"method": "BAD METHOD", "path": "/"}`, http.StatusBadRequest, ""},
			{`{`, http.StatusBadRequest, ""},
		} {
			code, body := h.post("/api", tt.call)
			require.Equal(s.T(), tt.code, code, tt.call)
			if tt.want != "" {
				require.JSONEq(s.T(), tt.want, body, tt.call)
			}
		}
		h.report(0, "loaded", "")
		h.report(0, "connected", "")
		h.report(1, "loaded", "")
		h.report(1, "connected", "")
	})
	problems, err := CheckUI(s.ctx, dir, UIOptions{Browser: s.browser, API: api, Settle: time.Millisecond})
	require.NoError(s.T(), err)
	require.Empty(s.T(), problems)
	require.Equal(s.T(), []string{
		`POST /json?x=1 application/json {"b": 2}`,
		"PUT /text text/plain hi",
		"GET /broken  ",
	}, got)

	s.open(func(h host) {
		code, body := h.post("/api", `{"method": "GET", "path": "/x"}`)
		require.Equal(s.T(), http.StatusOK, code)
		require.JSONEq(s.T(), `{"status": 404, "body": "404 page not found\n"}`, body)
	})
	_, err = CheckUI(s.ctx, dir, UIOptions{Browser: s.browser, Timeout: time.Millisecond, Settle: time.Millisecond})
	require.NoError(s.T(), err)
}

func (s *UISuite) TestCanceled() {
	dir := s.plugin(uiManifest, nil)
	ctx, cancel := context.WithCancel(s.ctx)
	s.open(func(host) { cancel() })
	_, err := CheckUI(ctx, dir, UIOptions{Browser: s.browser})
	require.ErrorIs(s.T(), err, context.Canceled)

	ctx, cancel = context.WithCancel(s.ctx)
	s.open(func(h host) {
		h.report(0, "loaded", "")
		h.report(0, "connected", "")
		h.report(1, "loaded", "")
		h.report(1, "connected", "")
		cancel()
	})
	_, err = CheckUI(ctx, dir, UIOptions{Browser: s.browser, Settle: time.Hour})
	require.ErrorIs(s.T(), err, context.Canceled)
}

func (s *UISuite) TestFails() {
	dir := s.plugin(uiManifest, nil)
	s.browser.On("Open", mock.Anything, mock.Anything).Return(nil, errors.New("no browser")).Once()
	_, err := CheckUI(s.ctx, dir, UIOptions{Browser: s.browser})
	require.EqualError(s.T(), err, "no browser")

	_, err = CheckUI(s.ctx, dir, UIOptions{Browser: s.browser, Settings: map[string]any{"f": func() {}}})
	require.ErrorContains(s.T(), err, "encoding the pages' settings and data")

	_, err = CheckUI(s.ctx, s.T().TempDir(), UIOptions{Browser: s.browser})
	require.ErrorContains(s.T(), err, "plugin.json")

	s.open(func(h host) {
		code, _, _ := h.get("/p/0/ui/main.html")
		require.Equal(s.T(), http.StatusNotFound, code)
	})
	problems, err := CheckUI(s.ctx, dir, UIOptions{Browser: s.browser, Timeout: time.Millisecond, Settle: time.Millisecond})
	require.NoError(s.T(), err)
	require.Contains(s.T(), problems, UIProblem{Page: "panel main", Kind: UIProblemLoad, Message: "GET ui/main.html: the bundle has no ui/ directory"})

	for _, m := range []string{`{"id": "p"}`, `{"id": "p", "ui": {}}`} {
		_, err = CheckUI(s.ctx, s.plugin(m, nil), UIOptions{Browser: s.browser})
		require.EqualError(s.T(), err, "the manifest has no panels or cards", m)
	}
}

func (s *UISuite) TestWithProbe() {
	for _, tt := range []struct{ page, want string }{
		{"<html><head><title>t</title>", "<html><head>P<title>t</title>"},
		{"<!doctype html><html lang=en><body>", "<!doctype html><html lang=en>P<body>"},
		{"<!doctype html><p>", "<!doctype html>P<p>"},
		{"<p>", "P<p>"},
		{"<head", "P<head"},
	} {
		got := string(withProbe([]byte(tt.page), "s"))
		require.Equal(s.T(), strings.Replace(tt.want, "P", `<script src="s"></script>`, 1), got, tt.page)
	}
}

func (s *UISuite) TestChrome() {
	c := Chrome{run: func(context.Context, ...chromedp.Action) error { return nil }}
	closePage, err := c.Open(s.ctx, "http://127.0.0.1/")
	require.NoError(s.T(), err)
	closePage()

	for _, tt := range []struct {
		err  error
		want string
	}{
		{&exec.Error{Name: "chrome", Err: exec.ErrNotFound}, "no Chrome or Chromium found"},
		{os.ErrNotExist, "no Chrome or Chromium found"},
		{errors.New("crashed"), "loading the check's page in Chrome: crashed"},
	} {
		c := Chrome{run: func(context.Context, ...chromedp.Action) error { return tt.err }}
		_, err := c.Open(s.ctx, "http://127.0.0.1/")
		require.ErrorContains(s.T(), err, tt.want)
	}

	_, err = Chrome{Options: chromeAt(filepath.Join(s.T().TempDir(), "chrome"))}.Open(s.ctx, "http://127.0.0.1/")
	require.ErrorContains(s.T(), err, "no Chrome or Chromium found")
}

func (s *UISuite) TestUIProblemString() {
	require.Equal(s.T(), "panel main: csp: blocked", UIProblem{Page: "panel main", Kind: UIProblemCSP, Message: "blocked"}.String())
}
