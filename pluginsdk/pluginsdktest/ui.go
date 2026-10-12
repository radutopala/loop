package pluginsdktest

import (
	"bytes"
	"cmp"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/chromedp"

	"github.com/radutopala/loop/pluginsdk"
)

// Defaults of UIOptions.
const (
	UITimeout = 10 * time.Second
	UISettle  = 500 * time.Millisecond
)

// UIProblemKind is what went wrong in a page.
type UIProblemKind string

const (
	// UIProblemCSP is a load or inline script the page's policy blocked.
	UIProblemCSP UIProblemKind = "csp"
	// UIProblemError is an uncaught error or unhandled rejection.
	UIProblemError UIProblemKind = "error"
	// UIProblemLoad is a file of the bundle that didn't load, or a page that
	// never did.
	UIProblemLoad UIProblemKind = "load"
	// UIProblemBridge is a bridge call that failed, or a page that never
	// connected to the bridge.
	UIProblemBridge UIProblemKind = "bridge"
)

// UIProblem is what CheckUI found wrong in a panel or card.
type UIProblem struct {
	// Page names the panel or card, as in "panel main".
	Page    string
	Kind    UIProblemKind
	Message string
}

func (p UIProblem) String() string {
	return p.Page + ": " + string(p.Kind) + ": " + p.Message
}

// Browser loads the page CheckUI serves.
type Browser interface {
	// Open loads url and keeps it open until close is called.
	Open(ctx context.Context, url string) (close func(), err error)
}

// UIOptions tunes CheckUI.
type UIOptions struct {
	// Browser loads the pages; nil is Chrome{}.
	Browser Browser
	// API answers the pages' api.request calls, as the plugin's
	// Server.X does: the path as the page sent it, without /x. nil
	// answers 404.
	API http.Handler
	// Settings is what settings.get answers; nil is the manifest's
	// defaults.
	Settings map[string]any
	// CardData is the data each card is drawn with, by card id.
	CardData map[string]any
	// Timeout is how long the pages have to load and connect to the
	// bridge; 0 is UITimeout.
	Timeout time.Duration
	// NoBridge skips the check that each page connects to the bridge.
	NoBridge bool
	// Settle is how long CheckUI keeps watching once the pages are up, for
	// late errors; 0 is UISettle.
	Settle time.Duration
}

//go:embed uicheck
var uiCheckFiles embed.FS

// panelCSP is the policy Loop serves panel and card pages with, under
// prefix.
func panelCSP(prefix string) string {
	return "sandbox allow-scripts allow-forms; default-src 'none'; " +
		"script-src " + prefix + " 'wasm-unsafe-eval'; style-src " + prefix + " 'unsafe-inline'; " +
		"img-src " + prefix + " data: blob:; font-src " + prefix + " data:; media-src " + prefix + " blob:; " +
		"worker-src " + prefix + " blob:; connect-src " + prefix + "; " +
		"form-action 'none'; frame-src 'none'; base-uri 'none'"
}

// uiPage is a panel or card CheckUI loads.
type uiPage struct {
	Kind  string `json:"kind"`
	ID    string `json:"id"`
	Entry string `json:"entry"`
	URL   string `json:"url"`
	Data  any    `json:"data,omitempty"`
}

func (p uiPage) name() string {
	return p.Kind + " " + p.ID
}

// uiConfig is what the host page draws and answers with.
type uiConfig struct {
	PluginID     string         `json:"pluginId"`
	Capabilities []string       `json:"capabilities"`
	Settings     map[string]any `json:"settings"`
	Pages        []uiPage       `json:"pages"`
}

// uiCheck is the state of one CheckUI run.
type uiCheck struct {
	dir    string
	config []byte
	pages  []uiPage
	api    http.Handler

	mu        sync.Mutex
	loaded    []bool
	connected []bool
	problems  []UIProblem
	changed   chan struct{}
}

// CheckUI loads each panel and card of the plugin whose /loop-plugin
// directory is dir the way Loop draws them: served with Loop's content
// security policy into a sandboxed frame, with a fake bridge. It returns
// what went wrong: a blocked load or inline script, an uncaught error or
// unhandled rejection, a file of the bundle that didn't load, a failed
// bridge call, and a page that didn't load or connect to the bridge within
// opts.Timeout.
func CheckUI(ctx context.Context, dir string, opts UIOptions) ([]UIProblem, error) {
	m, err := pluginsdk.ReadManifest(filepath.Join(dir, "plugin.json"))
	if err != nil {
		return nil, err
	}
	c := &uiCheck{dir: dir, api: opts.API, changed: make(chan struct{}, 1)}
	if m.UI != nil {
		for _, p := range m.UI.Panels {
			c.pages = append(c.pages, uiPage{Kind: "panel", ID: p.ID, Entry: p.Entry})
		}
		for _, cd := range m.UI.Cards {
			c.pages = append(c.pages, uiPage{Kind: "card", ID: cd.ID, Entry: cd.Entry, Data: opts.CardData[cd.ID]})
		}
	}
	if len(c.pages) == 0 {
		return nil, errors.New("the manifest has no panels or cards")
	}
	for i := range c.pages {
		c.pages[i].URL = "/p/" + strconv.Itoa(i) + "/" + c.pages[i].Entry
	}
	settings := opts.Settings
	if settings == nil {
		settings = map[string]any{}
		for name, s := range m.Settings {
			if s != nil && s.Default != nil {
				settings[name] = s.Default
			}
		}
	}
	c.config, err = json.Marshal(uiConfig{PluginID: m.ID, Capabilities: m.Capabilities, Settings: settings, Pages: c.pages})
	if err != nil {
		return nil, fmt.Errorf("encoding the pages' settings and data: %w", err)
	}
	c.loaded = make([]bool, len(c.pages))
	c.connected = make([]bool, len(c.pages))

	srv := httptest.NewServer(c.handler())
	defer srv.Close()
	closePage, err := cmp.Or[Browser](opts.Browser, Chrome{}).Open(ctx, srv.URL+"/")
	if err != nil {
		return nil, err
	}
	defer closePage()

	timeout := cmp.Or(opts.Timeout, UITimeout)
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for !c.ready(opts.NoBridge) {
		select {
		case <-c.changed:
		case <-deadline.C:
			c.timedOut(opts.NoBridge, timeout)
			return c.result(ctx, cmp.Or(opts.Settle, UISettle))
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return c.result(ctx, cmp.Or(opts.Settle, UISettle))
}

// ready reports whether every page loaded and, unless noBridge, connected
// to the bridge.
func (c *uiCheck) ready(noBridge bool) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.pages {
		if !c.loaded[i] || (!noBridge && !c.connected[i]) {
			return false
		}
	}
	return true
}

// timedOut records the pages that didn't load or connect in time.
func (c *uiCheck) timedOut(noBridge bool, timeout time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, p := range c.pages {
		switch {
		case !c.loaded[i]:
			c.problems = append(c.problems, UIProblem{Page: p.name(), Kind: UIProblemLoad, Message: "the page didn't load within " + timeout.String()})
		case !noBridge && !c.connected[i]:
			c.problems = append(c.problems, UIProblem{Page: p.name(), Kind: UIProblemBridge, Message: "the page didn't connect to the bridge within " + timeout.String()})
		}
	}
}

// result waits settle for late problems and returns them.
func (c *uiCheck) result(ctx context.Context, settle time.Duration) ([]UIProblem, error) {
	t := time.NewTimer(settle)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.problems), nil
}

func (c *uiCheck) add(i int, kind UIProblemKind, msg string) {
	c.mu.Lock()
	c.problems = append(c.problems, UIProblem{Page: c.pages[i].name(), Kind: kind, Message: msg})
	c.mu.Unlock()
}

func (c *uiCheck) notify() {
	select {
	case c.changed <- struct{}{}:
	default:
	}
}

func (c *uiCheck) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", c.serveCheckFile("host.html"))
	mux.HandleFunc("GET /host.js", c.serveCheckFile("host.js"))
	mux.HandleFunc("GET /config", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(c.config)
	})
	mux.HandleFunc("POST /report", c.report)
	mux.HandleFunc("POST /api", c.request)
	mux.HandleFunc("GET /p/{page}/{path...}", c.serveBundle)
	return mux
}

func (c *uiCheck) serveCheckFile(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		data, _ := uiCheckFiles.ReadFile("uicheck/" + name)
		http.ServeContent(w, r, name, time.Time{}, bytes.NewReader(data))
	}
}

// page returns the page whose index is s, or false.
func (c *uiCheck) page(s string) (int, bool) {
	i, err := strconv.Atoi(s)
	return i, err == nil && i >= 0 && i < len(c.pages)
}

// report records what the host page saw in a page: its frame loaded, it
// connected to the bridge, or a problem.
func (c *uiCheck) report(w http.ResponseWriter, r *http.Request) {
	var rep struct {
		Page    int           `json:"page"`
		Kind    UIProblemKind `json:"kind"`
		Message string        `json:"message"`
	}
	if err := json.NewDecoder(r.Body).Decode(&rep); err != nil || rep.Page < 0 || rep.Page >= len(c.pages) {
		http.Error(w, "bad report", http.StatusBadRequest)
		return
	}
	switch rep.Kind {
	case "loaded":
		c.mu.Lock()
		c.loaded[rep.Page] = true
		c.mu.Unlock()
	case "connected":
		c.mu.Lock()
		c.connected[rep.Page] = true
		c.mu.Unlock()
	default:
		c.add(rep.Page, rep.Kind, rep.Message)
	}
	c.notify()
	w.WriteHeader(http.StatusNoContent)
}

// request answers a page's api.request with opts.API, as {status, body}.
func (c *uiCheck) request(w http.ResponseWriter, r *http.Request) {
	var call struct {
		Method string          `json:"method"`
		Path   string          `json:"path"`
		Body   json.RawMessage `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&call); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var body io.Reader = http.NoBody
	ctype := ""
	if len(call.Body) > 0 && string(call.Body) != "null" {
		var s string
		if json.Unmarshal(call.Body, &s) == nil {
			body, ctype = strings.NewReader(s), "text/plain"
		} else {
			body, ctype = bytes.NewReader(call.Body), "application/json"
		}
	}
	req, err := http.NewRequestWithContext(r.Context(), strings.ToUpper(call.Method), "/"+strings.TrimLeft(call.Path, "/"), body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	rec := httptest.NewRecorder()
	if c.api == nil {
		http.NotFound(rec, req)
	} else {
		c.api.ServeHTTP(rec, req)
	}
	answer := struct {
		Status int `json:"status"`
		Body   any `json:"body"`
	}{Status: rec.Code, Body: rec.Body.String()}
	if strings.Contains(rec.Header().Get("Content-Type"), "json") && json.Valid(rec.Body.Bytes()) {
		answer.Body = json.RawMessage(rec.Body.Bytes())
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(answer)
}

// serveBundle serves a file of the bundle to page as Loop does: only under
// ui/, with Loop's policy for the page's prefix. The page's entry gets the
// probe that reports its problems to the host page.
func (c *uiCheck) serveBundle(w http.ResponseWriter, r *http.Request) {
	i, ok := c.page(r.PathValue("page"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	prefix := "http://" + r.Host + "/p/" + strconv.Itoa(i) + "/"
	w.Header().Set("Content-Security-Policy", panelCSP(prefix))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	// Loop serves the bundle to any origin, so the opaque-origin frame can
	// load module scripts.
	w.Header().Set("Access-Control-Allow-Origin", "*")
	rel := r.PathValue("path")
	if rel == ".loop-check/probe.js" {
		c.serveCheckFile("probe.js")(w, r)
		return
	}
	data, err := c.readBundle(rel)
	if err != nil {
		c.add(i, UIProblemLoad, "GET "+rel+": "+err.Error())
		c.notify()
		http.NotFound(w, r)
		return
	}
	if rel == path.Clean(c.pages[i].Entry) {
		data = withProbe(data, "/p/"+strconv.Itoa(i)+"/.loop-check/probe.js")
	}
	http.ServeContent(w, r, path.Base(rel), time.Time{}, bytes.NewReader(data))
}

// readBundle reads rel under dir, if it's a file under ui/.
func (c *uiCheck) readBundle(rel string) ([]byte, error) {
	ui, ok := strings.CutPrefix(rel, "ui/")
	if !ok {
		return nil, errors.New("outside ui/, which is all Loop serves")
	}
	root, err := os.OpenRoot(filepath.Join(c.dir, "ui"))
	if err != nil {
		return nil, errors.New("the bundle has no ui/ directory")
	}
	defer root.Close()
	data, err := root.ReadFile(ui)
	if err != nil {
		return nil, errors.New("no such file in the bundle")
	}
	return data, nil
}

// withProbe adds the script at src to page, to run before anything else.
func withProbe(page []byte, src string) []byte {
	probe := `<script src="` + src + `"></script>`
	at := 0
	lower := bytes.ToLower(page)
	for _, tag := range []string{"<head", "<html", "<!doctype"} {
		if j := bytes.Index(lower, []byte(tag)); j >= 0 {
			at = j + bytes.IndexByte(page[j:], '>') + 1
			break
		}
	}
	return slices.Concat(page[:at], []byte(probe), page[at:])
}

// Chrome is a headless Chrome or Chromium, started for each page it opens.
type Chrome struct {
	// Options are added to chromedp's defaults, such as chromedp.ExecPath
	// for a browser chromedp doesn't find, or chromedp.NoSandbox to run as
	// root in a container.
	Options []chromedp.ExecAllocatorOption

	// run is chromedp.Run when nil.
	run func(context.Context, ...chromedp.Action) error
}

// Open starts the browser and loads url.
func (c Chrome) Open(ctx context.Context, url string) (func(), error) {
	actx, cancelAlloc := chromedp.NewExecAllocator(ctx, slices.Concat(chromedp.DefaultExecAllocatorOptions[:], c.Options)...)
	bctx, cancelBrowser := chromedp.NewContext(actx)
	closeAll := func() {
		cancelBrowser()
		cancelAlloc()
	}
	run := c.run
	if run == nil {
		run = chromedp.Run
	}
	if err := run(bctx, chromedp.Navigate(url)); err != nil {
		closeAll()
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("no Chrome or Chromium found; install one, or set its path with chromedp.ExecPath in Chrome.Options: %w", err)
		}
		return nil, fmt.Errorf("loading the check's page in Chrome: %w", err)
	}
	return closeAll, nil
}
