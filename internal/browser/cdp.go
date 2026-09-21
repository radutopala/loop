package browser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/accessibility"
	cdpbrowser "github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/cdp"
	cdpdom "github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/input"
	cdpio "github.com/chromedp/cdproto/io"
	"github.com/chromedp/cdproto/network"
	cdppage "github.com/chromedp/cdproto/page"
	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"github.com/go-json-experiment/json/jsontext"
)

// CDPClient wraps a chromedp browser context for CDP operations.
// It handles screencast streaming, input dispatch, navigation, and accessibility.
type CDPClient struct {
	allocCtx    context.Context
	allocCancel context.CancelFunc
	ctxCancel   context.CancelFunc
	ctx         context.Context
	wsURL       string // Chrome CDP WebSocket URL for HTTP endpoint access
	logger      *slog.Logger

	// Function deps — set by constructor, overridable in tests via direct struct construction.
	runFn         func(context.Context, ...chromedp.Action) error
	targetsFunc   func(context.Context) ([]*target.Info, error)
	listenFunc    func(context.Context, func(any))
	axTreeFunc    func(context.Context) ([]*accessibility.Node, error)
	scanRefsFunc  func(context.Context) ([]ElementRef, error)
	boxModelFunc  func(context.Context, cdp.BackendNodeID) (*cdpdom.BoxModel, error)
	createTabFunc func(context.Context, string) (target.ID, error)
	activateFunc  func(context.Context, target.ID) error
	closeTabFunc  func(context.Context, string) error // injectable for testing

	targetID target.ID   // the page target this client is attached to
	exec     cdpExecutor // injectable cdp.Execute

	// attachTimeout bounds NewContextForTarget; zero means defaultAttachTimeout.
	attachTimeout time.Duration

	// screencastTimeout bounds the Page.startScreencast call; zero means
	// defaultScreencastTimeout.
	screencastTimeout time.Duration

	// commandTimeout bounds a single CDP command; zero means
	// defaultCommandTimeout.
	commandTimeout time.Duration

	// pageReadTimeout bounds a read of the whole page; zero means
	// defaultPageReadTimeout.
	pageReadTimeout time.Duration

	// navigationTimeout bounds a navigation; zero means
	// defaultNavigationTimeout.
	navigationTimeout time.Duration

	mu                 sync.Mutex
	faviconData        map[string]string // icon URL -> data URL, "" when it could not be fetched
	screencasting      bool
	listenerRegistered bool        // true after first listenFunc registration
	frameCh            chan []byte // decoded JPEG frames
	stopCh             chan struct{}
}

// TargetID returns the Chrome page target ID this client is attached to.
func (c *CDPClient) TargetID() string {
	return string(c.targetID)
}

// SwitchTarget activates a different page target in Chrome via CDP protocol.
func (c *CDPClient) SwitchTarget(targetID string) error {
	c.StopScreencast()

	activate := func() error { return c.activateFunc(c.ctx, target.ID(targetID)) }
	if err := c.runBounded(activate); err != nil {
		c.logger.Error("SwitchTarget: activate failed", "error", err)
		return err
	}
	c.logger.Info("SwitchTarget: activated", "target_id", targetID)

	c.targetID = target.ID(targetID)

	c.mu.Lock()
	c.frameCh = make(chan []byte, 2)
	c.stopCh = make(chan struct{})
	c.mu.Unlock()

	return nil
}

// defaultAttachTimeout bounds an attach to a page target.
//
// Chrome can accept Target.attachToTarget and then never finish the handshake —
// a renderer blocked on a modal dialog or a wedged page will not answer
// Page.enable, and the call has no deadline of its own. Observed in practice:
// four attaches to one tab sat for six minutes until the whole CDP connection
// was torn down, during which the pane kept showing, and acting on, the tab it
// was attached to before. Failing loudly is better than blocking.
const defaultAttachTimeout = 15 * time.Second

// attachDeadline returns the configured attach timeout, or the default.
func (c *CDPClient) attachDeadline() time.Duration {
	if c.attachTimeout > 0 {
		return c.attachTimeout
	}
	return defaultAttachTimeout
}

// defaultScreencastTimeout bounds the Page.startScreencast call.
//
// A backgrounded or wedged renderer accepts the command and never answers, and
// the call has no deadline of its own. The pane then holds its last frame with
// nothing logged anywhere — the failure mode is silence, which is the hardest
// kind to chase. Failing loudly is better than blocking.
const defaultScreencastTimeout = 10 * time.Second

// screencastDeadline returns the configured screencast timeout, or the default.
func (c *CDPClient) screencastDeadline() time.Duration {
	if c.screencastTimeout > 0 {
		return c.screencastTimeout
	}
	return defaultScreencastTimeout
}

// defaultCommandTimeout bounds a single CDP command.
//
// The deadline cannot ride on the caller's context: every command runs on
// c.ctx, the session's own context, and cancelling that closes the tab. The
// ctx parameters these methods take are therefore not deadlines and never
// were. Chrome meanwhile never acknowledges input dispatched to a
// backgrounded target, and one worker drains the pane's input queue in order,
// so an unbounded dispatch parks that worker for good: frames keep arriving
// on their own goroutine and the pane looks alive while every later click,
// keystroke and paste piles up undelivered. Five seconds is far longer than
// any of these commands takes when Chrome is answering at all.
const defaultCommandTimeout = 5 * time.Second

// commandDeadline returns the configured command timeout, or the default.
func (c *CDPClient) commandDeadline() time.Duration {
	if c.commandTimeout > 0 {
		return c.commandTimeout
	}
	return defaultCommandTimeout
}

// defaultPageReadTimeout bounds a read of the whole page: the accessibility
// tree walk behind find, and a screenshot capture.
//
// These are not single commands — the walk asks for the full tree and then for
// a box model per interactive element — so the command deadline is too short
// for them on a heavy page, but they still need one of their own. A renderer
// that dies mid-walk answers nothing further, and the call then has no reason
// to return: observed in practice on a crashed tab, find sat until the MCP
// client gave up two minutes later, twice in a row, four minutes spent to
// learn nothing about why.
const defaultPageReadTimeout = 30 * time.Second

// pageReadDeadline returns the configured page-read timeout, or the default.
func (c *CDPClient) pageReadDeadline() time.Duration {
	if c.pageReadTimeout > 0 {
		return c.pageReadTimeout
	}
	return defaultPageReadTimeout
}

// defaultNavigationTimeout bounds a navigation: Navigate and Reload wait for
// the load event, which a slow site legitimately takes tens of seconds to
// reach, so neither the command nor the page-read deadline fits them. A tab
// whose renderer has died reaches it never, and the wait is then only over
// when something further up gives up — the MCP client, two minutes later.
const defaultNavigationTimeout = 60 * time.Second

// navigationDeadline returns the configured navigation timeout, or the default.
func (c *CDPClient) navigationDeadline() time.Duration {
	if c.navigationTimeout > 0 {
		return c.navigationTimeout
	}
	return defaultNavigationTimeout
}

// boundedFor runs fn on its own goroutine and stops waiting once d passes or
// ctx is done. The call is abandoned rather than cancelled — cancelling would
// take the session, and with it the tab — so a wedged target costs one parked
// goroutine instead of the caller. fn owns everything it writes to, so a late
// return races nothing.
func boundedFor[T any](ctx context.Context, d time.Duration, fn func() (T, error)) (T, error) {
	type result struct {
		val T
		err error
	}
	done := make(chan result, 1)
	go func() {
		val, err := fn()
		done <- result{val: val, err: err}
	}()

	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case r := <-done:
		return r.val, r.err
	case <-ctx.Done():
		var zero T
		return zero, fmt.Errorf("cdp command abandoned: %w", ctx.Err())
	case <-timer.C:
		var zero T
		return zero, fmt.Errorf("cdp command timed out after %s", d)
	}
}

// bounded runs fn under the command deadline. Callers holding a context of
// their own want boundedFor instead.
func bounded[T any](c *CDPClient, fn func() (T, error)) (T, error) {
	return boundedFor(context.Background(), c.commandDeadline(), fn)
}

// runBounded runs fn under the command deadline, for calls with no result.
func (c *CDPClient) runBounded(fn func() error) error {
	_, err := bounded(c, func() (struct{}, error) { return struct{}{}, fn() })
	return err
}

// runBoundedFor is runBounded for a caller holding a context and a deadline of
// its own.
func runBoundedFor(ctx context.Context, d time.Duration, fn func() error) error {
	_, err := boundedFor(ctx, d, func() (struct{}, error) { return struct{}{}, fn() })
	return err
}

// runActions issues actions on the session context under the command deadline.
func (c *CDPClient) runActions(actions ...chromedp.Action) error {
	return c.runBounded(func() error { return c.runFn(c.ctx, actions...) })
}

// NewContextForTarget creates a new CDPClient attached to a different target,
// reusing the existing browser WebSocket connection. Uses Target.attachToTarget
// internally — no new WS dial, no Chrome permission prompt.
func (c *CDPClient) NewContextForTarget(targetID string) (CDPSession, error) {
	tid := target.ID(targetID)
	// Use c.ctx (target context), NOT c.allocCtx (allocator context).
	// NewContext(targetCtx) reuses the browser connection via attachToTarget.
	// NewContext(allocCtx) would dial a new WebSocket.
	cdpCtx, cdpCancel := chromedp.NewContext(c.ctx,
		chromedp.WithTargetID(tid))

	// The attach cannot simply run on a context.WithTimeout: chromedp keeps the
	// context it was handed for the session's lifetime, so a deadline would kill
	// the tab a few seconds after it was attached. Time out around the call
	// instead and cancel the whole target context, which unblocks it.
	done := make(chan error, 1)
	go func() { done <- c.runFn(cdpCtx) }()

	timer := time.NewTimer(c.attachDeadline())
	defer timer.Stop()

	select {
	case err := <-done:
		if err != nil {
			cdpCancel()
			return nil, fmt.Errorf("attaching to target %s: %w", targetID, err)
		}
	case <-timer.C:
		cdpCancel()
		return nil, fmt.Errorf("attaching to target %s: timed out after %s", targetID, c.attachDeadline())
	}

	return &CDPClient{
		allocCtx:          c.allocCtx,
		allocCancel:       func() {},
		ctxCancel:         cdpCancel,
		ctx:               cdpCtx,
		wsURL:             c.wsURL,
		targetID:          tid,
		exec:              c.exec,
		logger:            c.logger,
		attachTimeout:     c.attachTimeout,
		screencastTimeout: c.screencastTimeout,
		commandTimeout:    c.commandTimeout,
		runFn:             c.runFn,
		targetsFunc:       c.targetsFunc,
		listenFunc:        c.listenFunc,
		axTreeFunc:        makeAxTreeFuncWith(c.runFn, c.exec),
		scanRefsFunc:      makeScanRefsFuncWith(c.runFn, c.exec),
		createTabFunc:     c.createTabFunc,
		activateFunc:      c.activateFunc,
		closeTabFunc: func(_ context.Context, closeTID string) error {
			tabCtx, tabCancel := chromedp.NewContext(cdpCtx, chromedp.WithTargetID(target.ID(closeTID)))
			defer tabCancel()
			return c.runFn(tabCtx, chromedp.ActionFunc(func(ctx context.Context) error {
				return c.exec(ctx, "Page.close", nil, nil)
			}))
		},
		boxModelFunc: c.boxModelFunc,
		frameCh:      make(chan []byte, 2),
		stopCh:       make(chan struct{}),
	}, nil
}

// cdpConfig holds constructor-level dependencies, overridable via CDPOption.
type cdpConfig struct {
	allocFunc        func(context.Context, string) (context.Context, context.CancelFunc)
	runFunc          func(context.Context, ...chromedp.Action) error
	exec             cdpExecutor                             // injectable cdp.Execute for testability
	targetID         target.ID                               // attach to existing target instead of creating new
	reuseTarget      bool                                    // if true, reuse existing page target; if false, always create new
	discoverExisting bool                                    // attach to Chrome's first existing page target instead of creating a new one
	discoverFunc     func(string, *slog.Logger) string       // discovers the first existing page target ID (injectable for tests)
	fromContextFunc  func(context.Context) *chromedp.Context // extract target info from context
}

// CDPOption configures NewCDPClient.
type CDPOption func(*cdpConfig)

// WithAllocator overrides the remote allocator used to connect to Chrome.
func WithAllocator(fn func(context.Context, string) (context.Context, context.CancelFunc)) CDPOption {
	return func(c *cdpConfig) { c.allocFunc = fn }
}

// WithRunFunc overrides the chromedp.Run function used during construction.
func WithRunFunc(fn func(context.Context, ...chromedp.Action) error) CDPOption {
	return func(c *cdpConfig) { c.runFunc = fn }
}

// WithTargetID attaches to a specific page target by ID.
func WithTargetID(id string) CDPOption {
	return func(c *cdpConfig) { c.targetID = target.ID(id) }
}

// WithExec overrides the cdp.Execute function used internally.
func WithExec(fn cdpExecutor) CDPOption {
	return func(c *cdpConfig) { c.exec = fn }
}

// WithNewTarget forces creation of a new page target instead of reusing existing ones.
// Use this when another CDP client may already be attached to the existing target.
func WithNewTarget() CDPOption {
	return func(c *cdpConfig) { c.reuseTarget = false }
}

// WithDiscoverExisting makes the client attach to Chrome's first existing page
// target (the sidecar's initial about:blank) instead of creating a new one. This
// lets the desktop browser panel and the agent's mcp-browser tools share ONE tab
// — so the panel's screencast shows what the agent navigates, rather than each
// driving its own blank tab.
func WithDiscoverExisting() CDPOption {
	return func(c *cdpConfig) { c.discoverExisting = true }
}

// withDiscoverFunc overrides the existing-target discovery function (for tests).
func withDiscoverFunc(fn func(string, *slog.Logger) string) CDPOption {
	return func(c *cdpConfig) { c.discoverFunc = fn }
}

// resolveBrowserWSURL turns a bare "ws://host:port" CDP endpoint into the full
// "ws://host:port/devtools/browser/<id>" URL by querying /json/version with a
// DIRECT (proxy-bypassing) HTTP client.
//
// chromedp would otherwise make that /json/version query via http.DefaultClient,
// which honors HTTP_PROXY/HTTPS_PROXY. On a host behind a corporate proxy the
// loopback request to the Chrome sidecar then gets routed to the proxy and fails
// ("connection refused" / "failed to resolve <proxy>"), so the Docker Browser
// panel never attaches even though the agent's CDP tools work. Pre-resolving
// here with Proxy:nil bypasses the proxy; the returned URL already contains
// "/devtools/browser/", so chromedp skips its own proxied lookup and the
// subsequent gobwas/ws dial is direct (no proxy).
//
// Best effort: on any error it returns the original URL unchanged.
func resolveBrowserWSURL(wsURL string, logger *slog.Logger) string {
	if strings.Contains(wsURL, "/devtools/browser/") {
		return wsURL // already a full browser-level URL
	}
	u, err := url.Parse(wsURL)
	if err != nil || u.Host == "" {
		return wsURL
	}

	// Proxy:nil — never relay a loopback CDP request through an HTTP proxy.
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 5 * time.Second}
	resp, err := client.Get("http://" + u.Host + "/json/version")
	if err != nil {
		if logger != nil {
			logger.Debug("CDP ws resolve failed; using bare URL", "host", u.Host, "error", err)
		}
		return wsURL
	}
	defer resp.Body.Close()

	var v struct {
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil || v.WebSocketDebuggerURL == "" {
		return wsURL
	}
	return v.WebSocketDebuggerURL
}

// devtoolsTarget is the part of a /json/list entry loop reads. The list is
// Chrome's own view of its targets and carries one thing CDP does not offer:
// the favicon the tab is showing.
type devtoolsTarget struct {
	Type       string `json:"type"`
	ID         string `json:"id"`
	FaviconURL string `json:"faviconUrl"`
}

// devtoolsTargets fetches Chrome's /json/list for the browser behind wsURL.
//
// Direct, never through a proxy, for the reason spelled out on
// resolveBrowserWSURL: this is a loopback request to a sidecar, and a
// corporate HTTP_PROXY would swallow it.
func devtoolsTargets(wsURL string) ([]devtoolsTarget, error) {
	u, err := url.Parse(wsURL)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("no host in CDP endpoint %q", wsURL)
	}
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 5 * time.Second}
	resp, err := client.Get("http://" + u.Host + "/json/list")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var targets []devtoolsTarget
	if err := json.NewDecoder(resp.Body).Decode(&targets); err != nil {
		return nil, fmt.Errorf("decoding target list: %w", err)
	}
	return targets, nil
}

// faviconURLs maps page target ID to the icon Chrome is showing for it.
//
// Best effort by design: a tab with no icon, a page that has not loaded one
// yet and a browser that will not answer at all are all the same answer here —
// no entry, and the strip falls back to its plain dot.
func faviconURLs(wsURL string, logger *slog.Logger) map[string]string {
	targets, err := devtoolsTargets(wsURL)
	if err != nil {
		if logger != nil {
			logger.Debug("favicon lookup failed", "error", err)
		}
		return nil
	}
	out := make(map[string]string, len(targets))
	for _, t := range targets {
		if t.Type == "page" && t.FaviconURL != "" {
			out[t.ID] = t.FaviconURL
		}
	}
	return out
}

// discoverFirstPageTarget queries Chrome's /json/list endpoint (direct, no proxy)
// and returns the ID of the first existing page target — Chrome's initial
// about:blank tab. Attaching to it (instead of creating a new tab) lets the
// browser panel and the agent's tools share one tab. Returns "" on any error.
func discoverFirstPageTarget(wsURL string, logger *slog.Logger) string {
	targets, err := devtoolsTargets(wsURL)
	if err != nil {
		if logger != nil {
			logger.Debug("CDP target discovery failed", "ws_url", wsURL, "error", err)
		}
		return ""
	}
	for _, t := range targets {
		if t.Type == "page" {
			return t.ID
		}
	}
	return ""
}

// NewCDPClient connects to a Chrome instance via its CDP WebSocket URL.
func NewCDPClient(ctx context.Context, wsURL string, logger *slog.Logger, opts ...CDPOption) (*CDPClient, error) {
	cfg := cdpConfig{
		allocFunc: func(parent context.Context, ws string) (context.Context, context.CancelFunc) {
			return chromedp.NewRemoteAllocator(parent, resolveBrowserWSURL(ws, logger))
		},
		runFunc:         chromedp.Run,
		exec:            cdp.Execute,
		reuseTarget:     true,
		discoverFunc:    discoverFirstPageTarget,
		fromContextFunc: chromedp.FromContext,
	}
	for _, o := range opts {
		o(&cfg)
	}

	var contextOpts []chromedp.ContextOption
	var resolvedTargetID target.ID
	if cfg.targetID != "" {
		resolvedTargetID = cfg.targetID
	}

	// Attach to Chrome's first existing page target so the panel and the agent's
	// tools share one tab, instead of each chromedp.NewContext spawning a fresh
	// blank tab.
	if cfg.discoverExisting && resolvedTargetID == "" {
		if tid := cfg.discoverFunc(wsURL, logger); tid != "" {
			resolvedTargetID = target.ID(tid)
		}
	}

	if resolvedTargetID != "" {
		contextOpts = append(contextOpts, chromedp.WithTargetID(resolvedTargetID))
	}

	allocCtx, allocCancel := cfg.allocFunc(ctx, wsURL)

	// Suppress chromedp's error logging for Chrome 136+ unknown enum values.
	contextOpts = append(contextOpts, chromedp.WithErrorf(func(string, ...any) {}))
	cdpCtx, cdpCancel := chromedp.NewContext(allocCtx, contextOpts...)

	// Run a no-op action to establish the connection.
	// When wsURL is a browser-level URL (ws://host:port/devtools/browser/{id}),
	// chromedp connects at the browser level and creates a new page target.
	// When it's just ws://host:port, chromedp first queries /json/version internally.
	if err := cfg.runFunc(cdpCtx); err != nil {
		cdpCancel()
		allocCancel()
		return nil, fmt.Errorf("connecting to CDP at %s: %w", wsURL, err)
	}

	// If no target was pre-resolved, read the target ID that chromedp attached to.
	if resolvedTargetID == "" {
		if ci := cfg.fromContextFunc(cdpCtx); ci != nil && ci.Target != nil {
			resolvedTargetID = ci.Target.TargetID
		}
	}

	return &CDPClient{
		allocCtx:    allocCtx,
		allocCancel: allocCancel,
		ctxCancel:   cdpCancel,
		ctx:         cdpCtx,
		wsURL:       wsURL,
		targetID:    resolvedTargetID,
		exec:        cfg.exec,
		logger:      logger,
		runFn:       cfg.runFunc,
		targetsFunc: chromedp.Targets,
		listenFunc: func(ctx context.Context, fn func(any)) {
			chromedp.ListenTarget(ctx, fn)
		},
		axTreeFunc:   makeAxTreeFuncWith(cfg.runFunc, cfg.exec),
		scanRefsFunc: makeScanRefsFuncWith(cfg.runFunc, cfg.exec),
		boxModelFunc: func(ctx context.Context, nodeID cdp.BackendNodeID) (*cdpdom.BoxModel, error) {
			var returns cdpdom.GetBoxModelReturns
			err := cfg.runFunc(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
				return cfg.exec(ctx, string(cdpdom.CommandGetBoxModel),
					cdpdom.GetBoxModel().WithBackendNodeID(nodeID), &returns)
			}))
			return returns.Model, err
		},
		createTabFunc: func(ctx context.Context, url string) (target.ID, error) {
			var returns target.CreateTargetReturns
			err := cfg.runFunc(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
				return cfg.exec(ctx, string(target.CommandCreateTarget),
					target.CreateTarget(url), &returns)
			}))
			return returns.TargetID, err
		},
		activateFunc: func(ctx context.Context, id target.ID) error {
			return cfg.runFunc(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
				return cfg.exec(ctx, string(target.CommandActivateTarget),
					target.ActivateTarget(id), nil)
			}))
		},
		closeTabFunc: func(cdpCtx context.Context) func(context.Context, string) error {
			return func(_ context.Context, tid string) error {
				tabCtx, tabCancel := chromedp.NewContext(cdpCtx, chromedp.WithTargetID(target.ID(tid)))
				defer tabCancel()
				return cfg.runFunc(tabCtx, chromedp.ActionFunc(func(ctx context.Context) error {
					return cfg.exec(ctx, "Page.close", nil, nil)
				}))
			}
		}(cdpCtx),
		frameCh: make(chan []byte, 2),
		stopCh:  make(chan struct{}),
	}, nil
}

// Alive reports whether the connection is still usable. A client whose context
// has been canceled — because Close was called, or the sidecar it was talking to
// was stopped — fails every action with "context canceled", so callers holding a
// cached client need a way to tell it apart from a working one.
func (c *CDPClient) Alive() bool {
	return c.ctx != nil && c.ctx.Err() == nil
}

// CloseBrowser asks Chrome itself to shut down.
//
// Chrome commits pending profile writes — cookies above all — on a ~30 second
// timer or at a clean shutdown, and stopping the container from outside is
// neither: SIGTERM makes it exit without flushing. A sign-in performed seconds
// before the sidecar is stopped would be lost, which is exactly what the
// persistent profile exists to prevent.
func (c *CDPClient) CloseBrowser(ctx context.Context) error {
	return c.runFn(c.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		return cdpbrowser.Close().Do(ctx)
	}))
}

// Close shuts down the CDP connection and closes the page target.
func (c *CDPClient) Close() {
	c.mu.Lock()
	wasScreencasting := c.screencasting
	c.screencasting = false
	c.mu.Unlock()

	if wasScreencasting {
		close(c.stopCh)
		_ = c.runActions(cdppage.StopScreencast())
	}

	c.ctxCancel()
	c.allocCancel()
}

// Navigate navigates to the given URL.
func (c *CDPClient) Navigate(ctx context.Context, url string) error {
	return runBoundedFor(ctx, c.navigationDeadline(), func() error {
		return c.runFn(c.ctx, chromedp.Navigate(url))
	})
}

// Reload reloads the current page. Bounded like Navigate: it waits for the
// same load event.
func (c *CDPClient) Reload(ctx context.Context) error {
	return runBoundedFor(ctx, c.navigationDeadline(), func() error {
		return c.runFn(c.ctx, chromedp.Reload())
	})
}

// GoBack navigates back in history via window.history.back().
// This is a no-op if there is no history to go back to.
//
// The history call returns without waiting for whatever navigation it starts,
// so this is bounded as the single command it is rather than as a navigation.
func (c *CDPClient) GoBack(ctx context.Context) error {
	return runBoundedFor(ctx, c.commandDeadline(), func() error {
		return c.runFn(c.ctx, chromedp.Evaluate(`void(window.history.back())`, nil))
	})
}

// GoForward navigates forward in history via window.history.forward().
// This is a no-op if there is no history to go forward to.
func (c *CDPClient) GoForward(ctx context.Context) error {
	return runBoundedFor(ctx, c.commandDeadline(), func() error {
		return c.runFn(c.ctx, chromedp.Evaluate(`void(window.history.forward())`, nil))
	})
}

// PageInfo holds the current page URL and title.
type PageInfo struct {
	URL   string `json:"url"`
	Title string `json:"title"`
}

// GetPageInfo returns the current page URL and title.
func (c *CDPClient) GetPageInfo(ctx context.Context) (*PageInfo, error) {
	return boundedFor(ctx, c.commandDeadline(), func() (*PageInfo, error) {
		var url, title string
		if err := c.runFn(c.ctx,
			chromedp.Location(&url),
			chromedp.Title(&title),
		); err != nil {
			return nil, fmt.Errorf("getting page info: %w", err)
		}
		return &PageInfo{URL: url, Title: title}, nil
	})
}

// StartScreencast begins streaming JPEG frames from Chrome.
// Frames are sent to the returned channel. Call StopScreencast to stop.
// Each call returns a NEW channel — callers must not hold old references.
func (c *CDPClient) StartScreencast(quality, maxWidth, maxHeight int) <-chan []byte {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Always create a fresh frameCh so old pipeFrames goroutines can't
	// steal frames from the new one (two readers on one channel = race).
	c.frameCh = make(chan []byte, 2)
	c.logger.Info("StartScreencast", "already_screencasting", c.screencasting, "target_id", string(c.targetID))

	// Register the frame listener ONCE per CDP client lifetime.
	// chromedp.ListenTarget adds listeners — never removes them.
	// Multiple registrations cause duplicate acks that clog the queue.
	if !c.listenerRegistered {
		c.listenerRegistered = true
		c.listenFunc(c.ctx, func(ev any) {
			e, ok := ev.(*cdppage.EventScreencastFrame)
			if !ok {
				return
			}
			data, err := base64.StdEncoding.DecodeString(e.Data)
			if err != nil {
				c.logger.Error("failed to decode screencast frame", "error", err)
				return
			}

			go func() {
				if err := c.runFn(c.ctx, cdppage.ScreencastFrameAck(e.SessionID)); err != nil {
					c.logger.Debug("screencast ack failed", "error", err)
				}
			}()

			c.mu.Lock()
			ch := c.frameCh
			c.mu.Unlock()
			// Keep the newest frame, not the oldest. A full buffer means the
			// pane is behind; discarding the frame that just arrived would
			// hand it another stale one and hold the lag open, so an old
			// frame is dropped to make room instead.
			for {
				select {
				case ch <- data:
					return
				default:
				}
				select {
				case <-ch:
				default:
					// Drained by the consumer in between; try again.
				}
			}
		})
	}

	if !c.screencasting {
		c.screencasting = true
		c.stopCh = make(chan struct{})

		go func() {
			err := c.startScreencastCmd(quality, maxWidth, maxHeight)
			if targetCrashed(err) {
				err = c.restartCrashedScreencast(quality, maxWidth, maxHeight)
			}
			if err == nil {
				return
			}
			c.logger.Error("failed to start screencast", "error", err, "target_id", string(c.targetID))

			// Chrome is not streaming, so leaving the flag set would make every
			// later StartScreencast skip the command and hand back a channel
			// nothing ever writes to — a pane blank for as long as the client
			// lives. Clearing it costs at most a duplicate startScreencast,
			// which Chrome accepts.
			c.mu.Lock()
			c.screencasting = false
			c.mu.Unlock()
		}()
	}

	return c.frameCh
}

// startScreencastCmd issues Page.startScreencast under the screencast deadline.
func (c *CDPClient) startScreencastCmd(quality, maxWidth, maxHeight int) error {
	done := make(chan error, 1)
	go func() {
		done <- c.runFn(c.ctx,
			cdppage.StartScreencast().
				WithFormat(cdppage.ScreencastFormatJpeg).
				WithQuality(int64(quality)).
				WithMaxWidth(int64(maxWidth)).
				WithMaxHeight(int64(maxHeight)).
				WithEveryNthFrame(1),
		)
	}()

	timer := time.NewTimer(c.screencastDeadline())
	defer timer.Stop()

	select {
	case err := <-done:
		return err
	case <-timer.C:
		// Cancelling the call would mean cancelling the target context, which
		// takes the tab with it. Report instead, and let the caller's reset
		// leave the door open for a later attempt.
		return fmt.Errorf("timed out after %s", c.screencastDeadline())
	}
}

// restartCrashedScreencast reloads a tab whose renderer died, then asks for the
// screencast again.
//
// Chrome keeps the target after a renderer crash and answers every command on
// it the same way, so nothing about waiting makes the tab come back. Observed
// in practice: each reconnect logged "Target crashed" and left the pane on a
// dead frame, for minutes, until the agent happened to navigate on its own.
// Reloading is what the crashed tab's own button does.
func (c *CDPClient) restartCrashedScreencast(quality, maxWidth, maxHeight int) error {
	c.logger.Warn("screencast target crashed, reloading", "target_id", string(c.targetID))
	if err := c.runActions(cdppage.Reload()); err != nil {
		return fmt.Errorf("reloading crashed target: %w", err)
	}
	return c.startScreencastCmd(quality, maxWidth, maxHeight)
}

// targetCrashed reports whether err is Chrome's answer for a page whose
// renderer has died. The match is on the message: the error reaches here
// wrapped, and its -32000 code is the generic one every server-side CDP
// failure carries.
func targetCrashed(err error) bool {
	return err != nil && strings.Contains(err.Error(), "Target crashed")
}

// ResetScreencast marks the screencast as stopped without sending a CDP command.
// Use this when the WS connection was lost and the screencast state is stale.
func (c *CDPClient) ResetScreencast() {
	c.mu.Lock()
	c.screencasting = false
	c.mu.Unlock()
}

// StopScreencast stops the screencast stream.
func (c *CDPClient) StopScreencast() {
	c.mu.Lock()
	wasScreencasting := c.screencasting
	c.screencasting = false
	c.mu.Unlock()

	if wasScreencasting {
		close(c.stopCh)
		c.logger.Info("StopScreencast: sending CDP stop command")
		// SwitchTarget stops before it activates, so an unbounded stop here
		// meant one wedged tab could freeze every later tab switch.
		if err := c.runActions(cdppage.StopScreencast()); err != nil {
			c.logger.Error("StopScreencast: stop command failed", "error", err, "target_id", string(c.targetID))
		}
		c.logger.Info("StopScreencast: done")
	}
}

// parseMouseButton converts a button name ("left", "right", "middle") to input.MouseButton.
func parseMouseButton(button string) input.MouseButton {
	switch button {
	case "right":
		return input.Right
	case "middle":
		return input.Middle
	default:
		return input.Left
	}
}

// mouseButtonBitmask returns the CDP buttons bitmask for a given button name.
// See https://chromedevtools.github.io/devtools-protocol/tot/Input/#method-dispatchMouseEvent
func mouseButtonBitmask(button string) int64 {
	switch button {
	case "right":
		return 2
	case "middle":
		return 4
	default:
		return 1
	}
}

// MouseClick dispatches a mouse click at the given coordinates.
func (c *CDPClient) MouseClick(ctx context.Context, x, y float64, button string, clickCount int) error {
	btn := parseMouseButton(button)

	return c.runActions(
		input.DispatchMouseEvent(input.MousePressed, x, y).
			WithButton(btn).
			WithClickCount(int64(clickCount)),
		input.DispatchMouseEvent(input.MouseReleased, x, y).
			WithButton(btn).
			WithClickCount(int64(clickCount)),
	)
}

// MouseMove dispatches a mouse move event.
// buttons indicates which buttons are pressed (0=none, 1=left, 2=right, 4=middle).
func (c *CDPClient) MouseMove(ctx context.Context, x, y float64, buttons int) error {
	evt := input.DispatchMouseEvent(input.MouseMoved, x, y)
	if buttons > 0 {
		evt = evt.WithButtons(int64(buttons))
	}
	return c.runActions(evt)
}

// MouseScroll dispatches a mouse wheel event.
func (c *CDPClient) MouseScroll(ctx context.Context, x, y, deltaX, deltaY float64) error {
	return c.runActions(
		input.DispatchMouseEvent(input.MouseWheel, x, y).
			WithDeltaX(deltaX).
			WithDeltaY(deltaY),
	)
}

// modShift is the CDP modifier bit for Shift. Shift is the one modifier that
// still produces text, so it is excluded when deciding whether a key press is
// a shortcut.
const modShift = 8

// namedKey carries the DispatchKeyEvent fields Chrome needs for a key that is
// not a single printable character.
type namedKey struct {
	code string
	vk   int64
	// text is non-empty for keys that also produce input. Chrome raises keydown
	// without it, but never generates the char event, so Enter would submit a
	// form yet fail to insert a newline in a textarea.
	text string
}

// namedKeys mirrors chromedp's own keyboard table (chromedp/kb) for the keys
// the browser pane forwards.
var namedKeys = map[string]namedKey{
	"Backspace":  {"Backspace", 8, ""},
	"Tab":        {"Tab", 9, ""},
	"Enter":      {"Enter", 13, "\r"},
	"Escape":     {"Escape", 27, ""},
	"PageUp":     {"PageUp", 33, ""},
	"PageDown":   {"PageDown", 34, ""},
	"End":        {"End", 35, ""},
	"Home":       {"Home", 36, ""},
	"ArrowLeft":  {"ArrowLeft", 37, ""},
	"ArrowUp":    {"ArrowUp", 38, ""},
	"ArrowRight": {"ArrowRight", 39, ""},
	"ArrowDown":  {"ArrowDown", 40, ""},
	"Delete":     {"Delete", 46, ""},
}

// printableKey resolves the code and virtual key code for a single ASCII
// letter or digit. Chrome matches shortcuts against the uppercase code point,
// and `event.code` checks in a page need the physical-key name.
func printableKey(key string) (namedKey, bool) {
	if len(key) != 1 {
		return namedKey{}, false
	}
	switch ch := key[0]; {
	case ch >= 'a' && ch <= 'z':
		upper := ch - 'a' + 'A'
		return namedKey{"Key" + string(upper), int64(upper), key}, true
	case ch >= 'A' && ch <= 'Z':
		return namedKey{"Key" + key, int64(ch), key}, true
	case ch >= '0' && ch <= '9':
		return namedKey{"Digit" + key, int64(ch), key}, true
	}
	return namedKey{}, false
}

// resolveKey describes a key name for DispatchKeyEvent.
func resolveKey(key string) (namedKey, bool) {
	if nk, ok := namedKeys[key]; ok {
		return nk, true
	}
	return printableKey(key)
}

// KeyPress dispatches key down and key up events. modifiers is the CDP modifier
// bitmask (Alt=1, Ctrl=2, Meta=4, Shift=8); without it Chrome cannot recognise
// shortcuts such as Ctrl+A.
func (c *CDPClient) KeyPress(ctx context.Context, key string, modifiers int) error {
	down := input.DispatchKeyEvent(input.KeyDown).WithKey(key).WithModifiers(input.Modifier(modifiers))
	up := input.DispatchKeyEvent(input.KeyUp).WithKey(key).WithModifiers(input.Modifier(modifiers))
	nk, ok := resolveKey(key)
	if !ok {
		return c.runActions(down, up)
	}
	down = down.WithCode(nk.code).WithWindowsVirtualKeyCode(nk.vk).WithNativeVirtualKeyCode(nk.vk)
	up = up.WithCode(nk.code).WithWindowsVirtualKeyCode(nk.vk).WithNativeVirtualKeyCode(nk.vk)
	// Text is what makes the key produce input rather than only raise keydown.
	// A shortcut such as Ctrl+A must not carry it, or the page receives an "a".
	if nk.text != "" && modifiers&^modShift == 0 {
		down = down.WithText(nk.text).WithUnmodifiedText(nk.text)
	}
	return c.runActions(down, up)
}

// InsertText inserts text into the focused element in one shot, the way a paste
// does, instead of synthesizing a key event per character. The sidecar's own
// clipboard is unreachable from the host, so this is how host clipboard content
// crosses into the page.
func (c *CDPClient) InsertText(ctx context.Context, text string) error {
	return c.runActions(input.InsertText(text))
}

// selectionJS reads the current selection. window.getSelection() returns an
// empty string for selections inside form fields, so those are read off the
// active element instead.
const selectionJS = `(() => {
  const a = document.activeElement;
  if (a && (a.tagName === 'INPUT' || a.tagName === 'TEXTAREA') && a.selectionStart !== a.selectionEnd) {
    return a.value.substring(a.selectionStart, a.selectionEnd);
  }
  return String(window.getSelection() || '');
})()`

// ReadSelection returns the text currently selected in the page, so a copy in
// the remote browser can be handed back to the host clipboard.
// A copy arrives on the same worker as every other pane event, so an
// evaluation that never answers would stall the input queue behind it.
func (c *CDPClient) ReadSelection(ctx context.Context) (string, error) {
	return bounded(c, func() (string, error) { return c.EvaluateJS(ctx, selectionJS) })
}

// TypeText types text character by character.
func (c *CDPClient) TypeText(ctx context.Context, text string) error {
	for _, ch := range text {
		s := string(ch)
		if err := c.runActions(
			input.DispatchKeyEvent(input.KeyDown).WithText(s).WithKey(s),
			input.DispatchKeyEvent(input.KeyUp).WithKey(s),
		); err != nil {
			return fmt.Errorf("typing character %q: %w", s, err)
		}
	}
	return nil
}

// ElementRef represents an interactive element with a ref ID for precise interaction.
type ElementRef struct {
	RefID            string            `json:"ref_id"`
	Role             string            `json:"role"`
	Name             string            `json:"name"`
	Description      string            `json:"description,omitempty"`
	Value            string            `json:"value,omitempty"`
	X                float64           `json:"x"`
	Y                float64           `json:"y"`
	Width            float64           `json:"width"`
	Height           float64           `json:"height"`
	BackendDOMNodeID cdp.BackendNodeID `json:"backend_dom_node_id,omitempty"` // internal, used for scroll_to
}

// makeAxTreeFunc creates an accessibility tree function with lenient JSON parsing.
// Chrome 136+ adds PropertyName values that cdproto's strict enum unmarshaler
// rejects. This function parses nodes individually, ignoring unmarshal errors.
// Uses the provided runFn (instead of chromedp.Run directly) for testability.
// cdpExecutor abstracts cdp.Execute for testability.
type cdpExecutor func(ctx context.Context, method string, params, res any) error

func makeAxTreeFunc(runFn func(context.Context, ...chromedp.Action) error) func(context.Context) ([]*accessibility.Node, error) {
	return makeAxTreeFuncWith(runFn, cdp.Execute)
}

func makeAxTreeFuncWith(runFn func(context.Context, ...chromedp.Action) error, exec cdpExecutor) func(context.Context) ([]*accessibility.Node, error) {
	return func(ctx context.Context) ([]*accessibility.Node, error) {
		var nodes []*accessibility.Node
		err := runFn(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
			var raw struct {
				Nodes []json.RawMessage `json:"nodes"`
			}
			if e := exec(ctx, "Accessibility.getFullAXTree", nil, &raw); e != nil {
				return e
			}
			for _, nodeJSON := range raw.Nodes {
				// Try strict unmarshal first.
				var node accessibility.Node
				if err := json.Unmarshal(nodeJSON, &node); err == nil {
					nodes = append(nodes, &node)
					continue
				}
				// Fallback: extract only the fields we need for GetElementRefs.
				var m struct {
					NodeID           string            `json:"nodeId"`
					Ignored          bool              `json:"ignored"`
					BackendDOMNodeID cdp.BackendNodeID `json:"backendDOMNodeId"`
					Role             *struct {
						Value string `json:"value"`
					} `json:"role"`
					Name *struct {
						Value string `json:"value"`
					} `json:"name"`
					Description *struct {
						Value string `json:"value"`
					} `json:"description"`
					Value *struct {
						Value string `json:"value"`
					} `json:"value"`
				}
				if err := json.Unmarshal(nodeJSON, &m); err != nil {
					continue
				}
				n := &accessibility.Node{
					Ignored:          m.Ignored,
					BackendDOMNodeID: m.BackendDOMNodeID,
				}
				if m.Role != nil {
					n.Role = &accessibility.Value{Value: jsontext.Value(`"` + m.Role.Value + `"`)}
				}
				if m.Name != nil {
					n.Name = &accessibility.Value{Value: jsontext.Value(`"` + m.Name.Value + `"`)}
				}
				if m.Description != nil {
					n.Description = &accessibility.Value{Value: jsontext.Value(`"` + m.Description.Value + `"`)}
				}
				if m.Value != nil {
					n.Value = &accessibility.Value{Value: jsontext.Value(`"` + m.Value.Value + `"`)}
				}
				nodes = append(nodes, n)
			}
			return nil
		}))
		return nodes, err
	}
}

// interactiveRoles lists the accessibility roles that represent interactive elements.
var interactiveRoles = map[string]bool{
	"button":           true,
	"link":             true,
	"textbox":          true,
	"checkbox":         true,
	"radio":            true,
	"combobox":         true,
	"menuitem":         true,
	"tab":              true,
	"switch":           true,
	"slider":           true,
	"spinbutton":       true,
	"searchbox":        true,
	"option":           true,
	"menuitemcheckbox": true,
	"menuitemradio":    true,
}

// maxScannedRefs caps how many interactive elements one scan reports. find
// answers with at most twenty of them, and a page carrying more than this has
// already stopped being a page an agent navigates by ref — shipping the rest
// costs a megabyte of JSON per call and buys nothing.
const maxScannedRefs = 5000

// scanRefsJS collects the page's interactive elements in a single pass: role,
// accessible-ish name, and viewport rect, the same fields the accessibility
// walk produces.
//
// It exists because the walk asks Chrome for the whole accessibility tree,
// which the renderer has to compute and serialize in one piece. Measured on a
// synthetic page of 4000 cards (16k interactive elements), that cost 7.3s and
// ~270MB of renderer memory, plus 5.6s more for a box model per element; this
// scan returned the same 16000 elements in 146ms and no measurable memory. A
// sidecar sitting near its cap is then one page read away from a renderer that
// dies mid-walk, which is how a crashed tab was first seen here.
//
// The walk in axElementRefs stays as the fallback: it sees cross-origin frames,
// which script in the page cannot.
var scanRefsJS = fmt.Sprintf(scanRefsJSTemplate, scanRoleList, maxScannedRefs)

// scanRefsJSTemplate takes the interactive role list and the ref budget.
const scanRefsJSTemplate = `(() => {
  const roles = new Set([%s]);
  const text = (el) => (el.innerText || el.textContent || "").trim().replace(/\s+/g, " ").slice(0, 200);
  const named = (el) => {
    const label = el.getAttribute("aria-label");
    if (label) return label.trim();
    const by = (el.getAttribute("aria-labelledby") || "").split(/\s+/).filter(Boolean);
    if (by.length) {
      const parts = by.map((id) => {
        const t = el.ownerDocument.getElementById(id);
        return t ? text(t) : "";
      }).filter(Boolean);
      if (parts.length) return parts.join(" ");
    }
    const own = text(el);
    if (own) return own;
    return (el.getAttribute("placeholder") || el.getAttribute("title") || el.getAttribute("alt") || "").trim();
  };
  const roleOf = (el) => {
    const explicit = (el.getAttribute("role") || "").trim().toLowerCase();
    if (explicit) return explicit;
    const tag = el.tagName.toLowerCase();
    if (tag === "a") return el.hasAttribute("href") ? "link" : "";
    if (tag === "button" || tag === "summary") return "button";
    if (tag === "select") return el.multiple ? "listbox" : "combobox";
    if (tag === "textarea") return "textbox";
    if (tag === "option") return "option";
    if (tag === "input") {
      const t = (el.getAttribute("type") || "text").toLowerCase();
      if (t === "checkbox" || t === "radio" || t === "option") return t;
      if (t === "range") return "slider";
      if (t === "number") return "spinbutton";
      if (t === "search") return "searchbox";
      if (t === "hidden") return "";
      if (t === "button" || t === "submit" || t === "reset" || t === "image" || t === "file" || t === "color") return "button";
      return "textbox";
    }
    return el.isContentEditable ? "textbox" : "";
  };
  const out = [];
  const visit = (root, dx, dy) => {
    for (const el of root.querySelectorAll("*")) {
      if (out.length >= %d) return;
      if (el.shadowRoot) visit(el.shadowRoot, dx, dy);
      if (el.tagName === "IFRAME") {
        try {
          const doc = el.contentDocument;
          if (doc) {
            const box = el.getBoundingClientRect();
            visit(doc, dx + box.x, dy + box.y);
          }
        } catch (e) { /* cross-origin: the accessibility walk is the only way in */ }
      }
      const role = roleOf(el);
      if (!roles.has(role)) continue;
      if (el.checkVisibility && !el.checkVisibility({ visibilityProperty: true, opacityProperty: true, contentVisibilityAuto: true })) continue;
      const box = el.getBoundingClientRect();
      if (box.width <= 0 || box.height <= 0) continue;
      out.push({
        role: role,
        name: named(el),
        description: (el.getAttribute("aria-description") || el.getAttribute("title") || "").trim(),
        value: typeof el.value === "string" ? el.value.slice(0, 200) : "",
        x: box.x + dx,
        y: box.y + dy,
        width: box.width,
        height: box.height,
      });
    }
  };
  visit(document, 0, 0);
  return out;
})()`

// scanRoleList is the interactiveRoles keys as a JS array literal, so the scan
// and the accessibility walk agree on what counts as interactive.
var scanRoleList = func() string {
	names := make([]string, 0, len(interactiveRoles))
	for role := range interactiveRoles {
		names = append(names, `"`+role+`"`)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}()

// makeScanRefsFunc creates the in-page element scan. Like makeAxTreeFunc it
// goes through runFn and an injectable cdp.Execute rather than calling
// chromedp directly, so tests can answer it without a browser.
func makeScanRefsFunc(runFn func(context.Context, ...chromedp.Action) error) func(context.Context) ([]ElementRef, error) {
	return makeScanRefsFuncWith(runFn, cdp.Execute)
}

func makeScanRefsFuncWith(runFn func(context.Context, ...chromedp.Action) error, exec cdpExecutor) func(context.Context) ([]ElementRef, error) {
	return func(ctx context.Context) ([]ElementRef, error) {
		var refs []ElementRef
		err := runFn(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
			var res struct {
				Result struct {
					Value json.RawMessage `json:"value"`
				} `json:"result"`
				ExceptionDetails *struct {
					Text string `json:"text"`
				} `json:"exceptionDetails"`
			}
			params := cdpruntime.Evaluate(scanRefsJS).WithReturnByValue(true).WithAwaitPromise(false)
			if e := exec(ctx, string(cdpruntime.CommandEvaluate), params, &res); e != nil {
				return e
			}
			if res.ExceptionDetails != nil {
				return fmt.Errorf("scanning page: %s", res.ExceptionDetails.Text)
			}
			if len(res.Result.Value) == 0 {
				return nil
			}
			return json.Unmarshal(res.Result.Value, &refs)
		}))
		if err != nil {
			return nil, err
		}
		return refs, nil
	}
}

// GetElementRefs returns interactive elements from the accessibility tree with
// bounding boxes.
//
// The caller's context bounds the wait, alongside the page-read deadline. It
// cannot bound the work: that runs on c.ctx, the session's own context, and
// cancelling it would close the tab.
func (c *CDPClient) GetElementRefs(ctx context.Context) ([]ElementRef, error) {
	return boundedFor(ctx, c.pageReadDeadline(), c.elementRefs)
}

// elementRefs collects the page's interactive elements, in-page scan first.
//
// The accessibility walk answers only when the scan cannot: script in the page
// sees neither cross-origin frames nor a document that has already stopped
// answering, and both end here with no refs at all.
func (c *CDPClient) elementRefs() ([]ElementRef, error) {
	refs, err := c.scanRefsFunc(c.ctx)
	if err != nil {
		c.logger.Debug("in-page element scan failed, falling back to the accessibility tree",
			"error", err, "target_id", string(c.targetID))
	}
	if err == nil && len(refs) > 0 {
		for i := range refs {
			refs[i].RefID = fmt.Sprintf("ref_%d", i+1)
		}
		return refs, nil
	}
	return c.axElementRefs()
}

// axElementRefs walks the whole accessibility tree, asking for a box model per
// interactive node.
func (c *CDPClient) axElementRefs() ([]ElementRef, error) {
	// Get the accessibility tree.
	nodes, err := c.axTreeFunc(c.ctx)
	if err != nil {
		return nil, fmt.Errorf("getting accessibility tree: %w", err)
	}

	var refs []ElementRef
	refNum := 1

	for _, node := range nodes {
		if node.Ignored || node.Role == nil {
			continue
		}

		role := strings.Trim(fmt.Sprintf("%v", node.Role.Value), `"`)
		if !interactiveRoles[role] {
			continue
		}

		// Skip elements without a backend DOM node.
		if node.BackendDOMNodeID == 0 {
			continue
		}

		name := ""
		if node.Name != nil {
			name = strings.Trim(fmt.Sprintf("%v", node.Name.Value), `"`)
		}
		desc := ""
		if node.Description != nil {
			desc = strings.Trim(fmt.Sprintf("%v", node.Description.Value), `"`)
		}
		val := ""
		if node.Value != nil {
			val = strings.Trim(fmt.Sprintf("%v", node.Value.Value), `"`)
		}

		// Get bounding box via DOM.getBoxModel. Skip if not available
		// (element might be off-screen, hidden, or detached).
		boxModel, err := c.boxModelFunc(c.ctx, node.BackendDOMNodeID)
		if err != nil || boxModel == nil || len(boxModel.Content) < 8 {
			continue
		}

		// Content quad: [x1,y1, x2,y2, x3,y3, x4,y4]
		quad := boxModel.Content
		x := quad[0]
		y := quad[1]
		w := quad[2] - quad[0]
		h := quad[5] - quad[1]

		if w <= 0 || h <= 0 {
			continue
		}

		refs = append(refs, ElementRef{
			RefID:            fmt.Sprintf("ref_%d", refNum),
			Role:             role,
			Name:             name,
			Description:      desc,
			Value:            val,
			X:                x,
			Y:                y,
			Width:            w,
			Height:           h,
			BackendDOMNodeID: node.BackendDOMNodeID,
		})
		refNum++
	}

	return refs, nil
}

// ClickRef clicks the center of an element by its ref index (1-based).
func (c *CDPClient) ClickRef(ctx context.Context, refs []ElementRef, refIndex int) error {
	if refIndex < 1 || refIndex > len(refs) {
		return fmt.Errorf("ref index %d out of range (1-%d)", refIndex, len(refs))
	}
	ref := refs[refIndex-1]
	centerX := ref.X + ref.Width/2
	centerY := ref.Y + ref.Height/2
	return c.MouseClick(ctx, centerX, centerY, "left", 1)
}

// Screenshot captures a full-page screenshot as PNG. Bounded like
// GetElementRefs: a crashed renderer never answers the capture either.
func (c *CDPClient) Screenshot(ctx context.Context) ([]byte, error) {
	return boundedFor(ctx, c.pageReadDeadline(), func() ([]byte, error) {
		var buf []byte
		if err := c.runFn(c.ctx, chromedp.CaptureScreenshot(&buf)); err != nil {
			return nil, fmt.Errorf("capturing screenshot: %w", err)
		}
		return buf, nil
	})
}

// TabInfo holds information about a browser tab.
type TabInfo struct {
	TargetID   string `json:"target_id"`
	URL        string `json:"url"`
	Title      string `json:"title"`
	Active     bool   `json:"active,omitempty"`
	FaviconURL string `json:"favicon_url,omitempty"`
}

// ListTabs returns all open browser tabs via CDP protocol.
// The listing gates every tab switch and the pane's own liveness check, so it
// is bounded for the same reason the input path is: a target that stops
// answering must not take the caller with it.
func (c *CDPClient) ListTabs(_ context.Context) ([]TabInfo, error) {
	targets, err := bounded(c, func() ([]*target.Info, error) { return c.targetsFunc(c.ctx) })
	if err != nil {
		return nil, fmt.Errorf("listing targets: %w", err)
	}
	var tabs []TabInfo
	for _, t := range targets {
		if t.Type == "page" {
			tabs = append(tabs, TabInfo{
				TargetID: string(t.TargetID),
				URL:      t.URL,
				Title:    t.Title,
			})
		}
	}
	return tabs, nil
}

// maxFaviconBytes caps one inlined icon. A favicon is a few kilobytes; past
// this it is not an icon the strip needs, and the data URL rides the tab list
// on every refresh.
const maxFaviconBytes = 64 * 1024

// maxFaviconCache bounds how many icons one session keeps inlined. Reached
// only by a pane left open across dozens of sites; the map is then dropped
// whole rather than evicted entry by entry, since a refetch costs one CDP
// round trip.
const maxFaviconCache = 64

// maxFaviconChunks bounds the IO.read loop. The size cap already ends a
// well-behaved stream; this ends the one that answers with empty chunks and
// never sets EOF, which would otherwise spin a goroutine the command deadline
// has already stopped waiting for.
const maxFaviconChunks = 64

// Favicons maps page target ID to that tab's icon, inlined as a data: URL.
//
// The URLs come from Chrome's HTTP endpoint rather than CDP because there is
// no favicon anywhere in Target.getTargets — the DevTools list is the only
// place Chrome publishes what each tab resolved. The bytes are then fetched
// here rather than by the pane: the URL is named by whatever page the tab
// loaded, so an <img src> built from it would have the user's own machine
// contact a host that page chose, which is the isolation the sidecar exists
// to provide.
func (c *CDPClient) Favicons() map[string]string {
	urls := faviconURLs(c.wsURL, c.logger)
	if len(urls) == 0 {
		return nil
	}
	out := make(map[string]string, len(urls))
	for id, iconURL := range urls {
		if data := c.faviconAsData(iconURL); data != "" {
			out[id] = data
		}
	}
	return out
}

// faviconAsData returns iconURL inlined as a data: URL, fetching it once.
//
// Failures are cached as the empty string: an icon that 404s is asked for once
// per session rather than on every tab-list refresh.
func (c *CDPClient) faviconAsData(iconURL string) string {
	c.mu.Lock()
	data, cached := c.faviconData[iconURL]
	c.mu.Unlock()
	if cached {
		return data
	}

	data, err := c.fetchFavicon(iconURL)
	if err != nil {
		c.logger.Debug("favicon fetch failed", "url", iconURL, "error", err)
	}

	c.mu.Lock()
	if c.faviconData == nil || len(c.faviconData) >= maxFaviconCache {
		c.faviconData = make(map[string]string, maxFaviconCache)
	}
	c.faviconData[iconURL] = data
	c.mu.Unlock()
	return data
}

// fetchFavicon loads one icon through the browser's own network stack and
// returns it as a data: URL.
//
// Network.loadNetworkResource is what DevTools itself uses to pull a resource
// the page referenced: the request leaves from the browser, on the browser's
// network, so a sidecar in a container reaches the icon exactly the way the
// tab reached the page. Credentials stay off — an icon is not worth sending a
// cookie for.
func (c *CDPClient) fetchFavicon(iconURL string) (string, error) {
	var dataURL string
	err := c.runActions(chromedp.ActionFunc(func(ctx context.Context) error {
		tree, err := cdppage.GetFrameTree().Do(ctx)
		if err != nil {
			return fmt.Errorf("frame tree: %w", err)
		}

		res, err := network.LoadNetworkResource(iconURL, &network.LoadNetworkResourceOptions{
			DisableCache:       false,
			IncludeCredentials: false,
		}).WithFrameID(tree.Frame.ID).Do(ctx)
		if err != nil {
			return fmt.Errorf("loading icon: %w", err)
		}
		if !res.Success || res.Stream == "" {
			return fmt.Errorf("loading icon: %s, http status %d",
				res.NetErrorName, int(res.HTTPStatusCode))
		}
		defer func() { _ = cdpio.Close(res.Stream).Do(ctx) }()

		raw, err := c.readStream(ctx, res.Stream)
		if err != nil {
			return err
		}
		// Sniffed, not taken from Content-Type: the type decides how the pane
		// renders the bytes, and the server does not get to name it.
		mime := http.DetectContentType(raw)
		if !strings.HasPrefix(mime, "image/") {
			return fmt.Errorf("icon is %s, not an image", mime)
		}
		dataURL = "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(raw)
		return nil
	}))
	if err != nil {
		return "", err
	}
	return dataURL, nil
}

// readStream drains one IO stream, up to the icon size cap.
func (c *CDPClient) readStream(ctx context.Context, handle cdpio.StreamHandle) ([]byte, error) {
	var buf []byte
	for range maxFaviconChunks {
		// Raw rather than IO.read's typed helper: that one drops the
		// base64Encoded flag, and an icon arrives base64 or not depending on
		// what Chrome decides the stream holds.
		var res cdpio.ReadReturns
		if err := c.exec(ctx, cdpio.CommandRead,
			cdpio.Read(handle).WithSize(maxFaviconBytes), &res); err != nil {
			return nil, fmt.Errorf("reading icon: %w", err)
		}
		chunk := []byte(res.Data)
		if res.Base64encoded {
			decoded, err := base64.StdEncoding.DecodeString(res.Data)
			if err != nil {
				return nil, fmt.Errorf("decoding icon: %w", err)
			}
			chunk = decoded
		}
		buf = append(buf, chunk...)
		if len(buf) > maxFaviconBytes {
			return nil, fmt.Errorf("icon larger than %d bytes", maxFaviconBytes)
		}
		if res.EOF {
			return buf, nil
		}
	}
	return nil, fmt.Errorf("icon stream did not end within %d chunks", maxFaviconChunks)
}

// NewTab opens a new tab with the given URL.
func (c *CDPClient) NewTab(ctx context.Context, url string) (string, error) {
	tCtx, err := c.createTabFunc(c.ctx, url)
	if err != nil {
		return "", fmt.Errorf("creating new tab: %w", err)
	}
	return string(tCtx), nil
}

// SwitchTab switches to a tab by its target ID.
func (c *CDPClient) SwitchTab(ctx context.Context, targetID string) error {
	return c.activateFunc(c.ctx, target.ID(targetID))
}

// CloseTab closes a tab by its target ID via CDP Page.close.
func (c *CDPClient) CloseTab(ctx context.Context, targetID string) error {
	return c.closeTabFunc(ctx, targetID)
}

// EvaluateJS evaluates a JavaScript expression and returns the result as a string.
//
// The expression is the agent's own, and a page busy enough to take a while
// over it is ordinary, so this runs under the page-read deadline rather than
// the command one. It needs a deadline all the same: on a tab whose renderer
// had died, one evaluate sat for 73 seconds, and what ended it was the CDP
// connection dropping rather than anything here noticing.
func (c *CDPClient) EvaluateJS(ctx context.Context, expression string) (string, error) {
	return boundedFor(ctx, c.pageReadDeadline(), func() (string, error) {
		var result string
		if err := c.runFn(c.ctx, chromedp.Evaluate(expression, &result)); err != nil {
			return "", fmt.Errorf("evaluating JS: %w", err)
		}
		return result, nil
	})
}

// ConsoleMessage represents a captured browser console message.
type ConsoleMessage struct {
	Level string    `json:"level"` // "log", "info", "warning", "error", etc.
	Text  string    `json:"text"`
	Time  time.Time `json:"time"`
}

// EnableConsoleCapture enables the Runtime domain and listens for console API calls.
// Captured messages are sent to the provided channel. The caller owns the channel
// and should close it when done.
func (c *CDPClient) EnableConsoleCapture(ctx context.Context, ch chan<- ConsoleMessage) error {
	// Enable Runtime domain so we receive consoleAPICalled events.
	if err := c.runFn(c.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		return c.exec(ctx, cdpruntime.CommandEnable, nil, nil)
	})); err != nil {
		return fmt.Errorf("enabling runtime domain: %w", err)
	}

	c.listenFunc(c.ctx, func(ev any) {
		e, ok := ev.(*cdpruntime.EventConsoleAPICalled)
		if !ok {
			return
		}
		var parts []string
		for _, arg := range e.Args {
			if arg.Description != "" {
				parts = append(parts, arg.Description)
			} else {
				v := strings.Trim(string(arg.Value), `"`)
				if v != "" {
					parts = append(parts, v)
				}
			}
		}
		msg := ConsoleMessage{
			Level: string(e.Type),
			Text:  strings.Join(parts, " "),
			Time:  time.Now(),
		}
		// Non-blocking send; drop if channel is full.
		select {
		case ch <- msg:
		default:
		}
	})

	return nil
}

// NetworkRequest represents a captured network request with response metadata.
type NetworkRequest struct {
	URL        string    `json:"url"`
	Method     string    `json:"method"`
	Status     int64     `json:"status"`
	StatusText string    `json:"status_text"`
	Type       string    `json:"type"`
	Time       time.Time `json:"time"`
}

// EnableNetworkCapture enables the Network domain and listens for request/response events.
// Request metadata is sent to the provided channel. The caller owns the channel.
func (c *CDPClient) EnableNetworkCapture(ctx context.Context, ch chan<- NetworkRequest) error {
	if err := c.runFn(c.ctx, network.Enable()); err != nil {
		return fmt.Errorf("enabling network domain: %w", err)
	}

	// Track pending requests so we can pair responses with request metadata.
	pending := &sync.Map{}

	c.listenFunc(c.ctx, func(ev any) {
		switch e := ev.(type) {
		case *network.EventRequestWillBeSent:
			pending.Store(string(e.RequestID), NetworkRequest{
				URL:    e.Request.URL,
				Method: e.Request.Method,
				Type:   string(e.Type),
				Time:   time.Now(),
			})
		case *network.EventResponseReceived:
			val, ok := pending.LoadAndDelete(string(e.RequestID))
			if !ok {
				return
			}
			req := val.(NetworkRequest)
			req.Status = e.Response.Status
			req.StatusText = e.Response.StatusText
			req.Type = string(e.Type)
			select {
			case ch <- req:
			default:
			}
		}
	})

	return nil
}

// ResizeWindow overrides the device metrics to emulate a given viewport size.
func (c *CDPClient) ResizeWindow(ctx context.Context, width, height int) error {
	return c.runFn(c.ctx, emulation.SetDeviceMetricsOverride(int64(width), int64(height), 1.0, false))
}

// ScrollIntoView scrolls a DOM element identified by backendNodeID into view.
func (c *CDPClient) ScrollIntoView(ctx context.Context, backendNodeID cdp.BackendNodeID) error {
	return c.runFn(c.ctx, cdpdom.ScrollIntoViewIfNeeded().WithBackendNodeID(backendNodeID))
}

// MouseDown dispatches a mouse pressed event at the given coordinates.
func (c *CDPClient) MouseDown(ctx context.Context, x, y float64, button string) error {
	return c.runActions(input.DispatchMouseEvent(input.MousePressed, x, y).
		WithButton(parseMouseButton(button)).WithButtons(mouseButtonBitmask(button)).WithClickCount(1))
}

// MouseUp dispatches a mouse released event at the given coordinates.
func (c *CDPClient) MouseUp(ctx context.Context, x, y float64, button string) error {
	return c.runActions(input.DispatchMouseEvent(input.MouseReleased, x, y).
		WithButton(parseMouseButton(button)).WithClickCount(1))
}
