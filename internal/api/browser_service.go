package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/radutopala/loop/internal/browser"
	"github.com/radutopala/loop/internal/browsercookies"
	"github.com/radutopala/loop/internal/config"
)

// errBrowserDisabled is returned instead of starting a browser for a channel
// whose project sets browser.enabled to false.
var errBrowserDisabled = errors.New("browser is disabled for this project (browser.enabled)")

// BrowserProvider is the interface for managing browser lifecycle.
type BrowserProvider interface {
	EnsureBrowser(ctx context.Context, channelID, containerID string) error
	StopBrowser(ctx context.Context, channelID string) (string, error)
	IsRunning(ctx context.Context, channelID string) bool
	GetCDPEndpoint(channelID string) string
	GetContainerID(channelID string) (string, bool)
	IsHostMode() bool
	RemoveProfile(ctx context.Context, channelID string) error
}

// BrowserCleaner stops all browser sessions. Implemented by DockerProvider.
type BrowserCleaner interface {
	Cleanup(ctx context.Context)
}

// browserService owns the browser domain: the docker/host providers, the
// active mode per channel, CDPManager lifecycle, console/network capture
// state, and idle-session cleanup. It was extracted from Server so browser
// state is reachable only through this struct; shared daemon deps are
// accessed via deps.
//
// containerRegistry is held directly (not via serverDeps) because it is a
// Server-only field also used by unrelated domains (containers, terminal);
// Server.SetContainerRegistry mirrors it in here as a post-construction
// call, the same sanctioned pattern used for qEngine.SetProgress.
type browserService struct {
	deps *serverDeps // shared infrastructure; see serverDeps

	dockerProvider BrowserProvider
	hostProvider   BrowserProvider // for host Chrome mode

	activeMode map[string]string // channelID -> "docker"|"host"; nil defaults to docker
	modeMu     sync.Mutex        // protects activeMode

	cdpManagers   map[string]*browser.CDPManager // "channelID|mode" -> CDPManager
	cdpManagersMu sync.Mutex

	captures   map[string]*browser.CaptureState // channelID -> state
	capturesMu sync.Mutex

	// cookieReader reads the host browser's cookie stores for the import
	// flow. Struct field, not a package function, so tests inject a fake
	// instead of touching a real Keychain.
	cookieReader CookieReader

	containerRegistry ContainerManager // mirrored from Server.SetContainerRegistry
	keepAlive         time.Duration    // delay before removing idle browser containers
	screenshotDir     string           // if set, write screenshots to this dir instead of base64
}

// newBrowserService creates the browser domain with its state maps ready.
// Providers, keep-alive, and screenshot dir arrive later via the WithBrowser*
// options — the daemon builds the docker/host providers after config load.
func newBrowserService(deps *serverDeps) *browserService {
	return &browserService{
		deps:         deps,
		activeMode:   make(map[string]string),
		cdpManagers:  make(map[string]*browser.CDPManager),
		captures:     make(map[string]*browser.CaptureState),
		cookieReader: browsercookies.NewReader(),
	}
}

// setProviders wires the docker and host browser providers. docker may be
// nil when the daemon's docker provider failed to initialize; host mode
// stays usable via the always-initialized host provider.
func (s *browserService) setProviders(docker, host BrowserProvider) {
	s.dockerProvider = docker
	s.hostProvider = host
	// Both providers are built once at daemon start, so the only browser
	// config they can hold is the global layer. Hand them a lookup instead,
	// and a project's browser block reaches every channel that works in it.
	if dp, ok := docker.(*browser.DockerProvider); ok {
		dp.SetSettingsResolver(s.channelBrowserSettings)
	}
	if hp, ok := host.(*browser.HostProvider); ok {
		hp.SetPortResolver(s.channelHostCDPPort)
	}
}

// channelBrowserConfig resolves the browser block for a channel through the
// global → project → worktree layers. Reported as not-found when the channel
// has no directory to resolve against or the config will not load, leaving
// each caller on whatever it was built with.
//
// The layers are re-read per call, so an edited setting applies to the next
// sidecar without restarting the daemon.
func (s *browserService) channelBrowserConfig(ctx context.Context, channelID string) (config.BrowserConfig, bool) {
	dir, err := s.deps.workspace.resolveDirPath(ctx, "", channelID)
	if err != nil {
		return config.BrowserConfig{}, false
	}
	cfg := s.deps.configs.merged(dir, s.deps.workspace.resolveParentDirPath(ctx, channelID))
	if cfg == nil {
		return config.BrowserConfig{}, false
	}
	return cfg.Browser, true
}

// channelBrowserSettings is the docker provider's view of that block: what a
// sidecar is created from.
func (s *browserService) channelBrowserSettings(ctx context.Context, channelID string) (browser.ChannelSettings, bool) {
	bc, ok := s.channelBrowserConfig(ctx, channelID)
	if !ok {
		return browser.ChannelSettings{}, false
	}
	return browser.ChannelSettings{
		Image:          bc.ChromeImage,
		PersistProfile: bc.PersistProfile,
		Extensions:     bc.Extensions,
		MemoryMB:       bc.MemoryMB,
		CPUs:           bc.CPUs,
	}, true
}

// channelHostCDPPort is the host provider's view: the port this channel's own
// Chrome is expected to listen on.
func (s *browserService) channelHostCDPPort(ctx context.Context, channelID string) (int, bool) {
	bc, ok := s.channelBrowserConfig(ctx, channelID)
	if !ok {
		return 0, false
	}
	return bc.HostCDPPort, true
}

// browserEnabledFor reports whether a browser may be started for the channel.
//
// Fails open: a channel whose config cannot be read keeps the browser it has
// always had. Only an explicit browser.enabled = false takes one away, and
// only for channels working in the project that sets it — the daemon-wide
// flag is decided at startup, before any channel exists.
func (s *browserService) browserEnabledFor(ctx context.Context, channelID string) bool {
	bc, ok := s.channelBrowserConfig(ctx, channelID)
	if !ok {
		return true
	}
	return bc.Enabled
}

// setKeepAlive sets the delay before idle browser containers are removed.
func (s *browserService) setKeepAlive(d time.Duration) {
	s.keepAlive = d
}

// setScreenshotDir sets the directory for file-based screenshots. When set,
// screenshots are written as files instead of base64-encoded in JSON.
func (s *browserService) setScreenshotDir(dir string) {
	s.screenshotDir = dir
}

// WithBrowserProviders configures the browser domain's docker and host
// providers at construction. docker may be nil.
func WithBrowserProviders(docker, host BrowserProvider) Option {
	return func(s *Server) { s.browser.setProviders(docker, host) }
}

// WithBrowserKeepAlive sets the delay before idle browser containers are removed.
func WithBrowserKeepAlive(d time.Duration) Option {
	return func(s *Server) { s.browser.setKeepAlive(d) }
}

// WithScreenshotDir sets the directory for file-based screenshots.
func WithScreenshotDir(dir string) Option {
	return func(s *Server) { s.browser.setScreenshotDir(dir) }
}

// activeBrowserProvider returns the BrowserProvider for the given channel based on active mode.
func (s *browserService) activeBrowserProvider(channelID string) BrowserProvider {
	s.modeMu.Lock()
	mode := s.activeMode[channelID]
	s.modeMu.Unlock()

	if mode == "host" && s.hostProvider != nil {
		return s.hostProvider
	}
	return s.dockerProvider
}

// getOrCreateCDPManager returns or creates a CDPManager for the given channel+mode.
//
// A manager pins the endpoint it was built with, and a recreated sidecar comes
// back on a fresh ephemeral port, so a cached manager that never managed to
// connect would keep dialing the dead port for as long as it stays in the map —
// which is until the idle sweep, and every retry from the pane pushes that
// further out. Rebuild it against the endpoint the provider reports now.
func (s *browserService) getOrCreateCDPManager(channelID, mode string, provider BrowserProvider) *browser.CDPManager {
	wsEndpoint := provider.GetCDPEndpoint(channelID)
	key := channelID + "|" + mode

	s.cdpManagersMu.Lock()
	stale := s.cdpManagers[key]
	if stale != nil {
		if stale.WSEndpoint() == wsEndpoint || hasLiveClient(stale) {
			s.cdpManagersMu.Unlock()
			return stale
		}
		delete(s.cdpManagers, key)
	}
	isHost := provider.IsHostMode()
	cfg := browser.CDPManagerConfig{
		DiscoverExisting: !isHost,
		MaxRetries:       cdpMaxRetries,
		RetryDelay:       cdpRetryDelay,
	}
	if isHost {
		cfg.MaxRetries = 1
	}
	mgr := browser.NewCDPManager(wsEndpoint, cfg, s.deps.logger)
	s.cdpManagers[key] = mgr
	s.cdpManagersMu.Unlock()

	if stale != nil {
		s.deps.logger.Info("browser: CDP endpoint moved, rebuilding manager",
			"channel_id", channelID, "mode", mode, "old", stale.WSEndpoint(), "new", wsEndpoint)
		stale.Close()
	}
	return mgr
}

// hasLiveClient reports whether a manager holds a client that still works.
//
// That client is proof its endpoint is reachable, whatever the provider says
// now — host mode falls back to a bare port when DevToolsActivePort is briefly
// unreadable, and tearing a working pane down over that would be a worse bug
// than the one the endpoint check is here to fix.
func hasLiveClient(mgr *browser.CDPManager) bool {
	client := mgr.ActiveClient()
	return client != nil && client.Alive()
}

// getActiveCDPManager returns the CDPManager for the given channel's active mode.
func (s *browserService) getActiveCDPManager(channelID string) *browser.CDPManager {
	mode := s.modeFor(channelID)
	s.cdpManagersMu.Lock()
	defer s.cdpManagersMu.Unlock()
	return s.cdpManagers[channelID+"|"+mode]
}

// modeFor returns the active browser mode for a channel ("docker" or "host").
func (s *browserService) modeFor(channelID string) string {
	s.modeMu.Lock()
	mode := s.activeMode[channelID]
	s.modeMu.Unlock()
	if mode == "" {
		return "docker"
	}
	return mode
}

// getBrowserCDP returns the CDPClient for a channel, creating one if needed.
func (s *browserService) getBrowserCDP(ctx context.Context, channelID string) (browser.CDPSession, error) {
	// Check if there's an existing CDPManager with an active client.
	cdpMgr := s.getActiveCDPManager(channelID)
	if cdpMgr != nil {
		if activeClient := cdpMgr.ActiveClient(); activeClient != nil {
			if activeClient.Alive() {
				// The active tab can move ahead of the attached client: opening a
				// tab, or an agent switching one, records the new target before
				// anything attaches to it. Acting on the cached client then drives
				// a tab the user is not looking at — which is how a URL typed into
				// the pane ended up loading in the previous tab.
				synced, err := s.syncActiveClient(ctx, cdpMgr, activeClient)
				if err != nil {
					return nil, err
				}
				s.deps.logger.Info("getBrowserCDP: reusing cached CDP", "channel_id", channelID)
				s.ensureBrowserCapture(ctx, channelID, synced)
				return synced, nil
			}
			// The sidecar went away under us — stopped from the browser pane,
			// killed from the terminal, or crashed. The manager also carries the
			// dead container's endpoint, so it is dropped rather than reconnected
			// and the code below builds a new one against the new sidecar.
			s.deps.logger.Info("getBrowserCDP: dropping dead CDP", "channel_id", channelID)
			s.closeCDPManager(channelID, s.modeFor(channelID))
		}
	}

	// No cached client — ensure browser and create CDPManager.
	provider := s.activeBrowserProvider(channelID)
	isHost := provider.IsHostMode()
	s.deps.logger.Info("getBrowserCDP: no cached CDP, creating new", "channel_id", channelID, "host_mode", isHost)

	if !s.browserEnabledFor(ctx, channelID) {
		return nil, errBrowserDisabled
	}
	if err := provider.EnsureBrowser(ctx, channelID, ""); err != nil {
		return nil, fmt.Errorf("ensuring browser: %w", err)
	}
	s.deps.logger.Info("getBrowserCDP: browser ensured", "channel_id", channelID)

	cdpEndpoint := provider.GetCDPEndpoint(channelID)
	if cdpEndpoint == "" {
		return nil, fmt.Errorf("no CDP endpoint for channel %s", channelID)
	}

	mode := s.modeFor(channelID)
	cdpMgr = s.getOrCreateCDPManager(channelID, mode, provider)

	if err := cdpMgr.Connect(ctx); err != nil {
		return nil, err
	}

	// Connect always sets activeClient on success, so this is safe.
	cdpClient := cdpMgr.ActiveClient()

	// A freshly connected sidecar is the one moment an auto-import is worth
	// doing: the profile is either brand new or has been idle long enough
	// for its sessions to have gone stale.
	if !isHost {
		s.autoImportCookies(ctx, channelID, cdpClient)
	}

	// Start capture if needed.
	s.ensureBrowserCapture(ctx, channelID, cdpClient)

	return cdpClient, nil
}

// syncActiveClient returns a client attached to the manager's active target,
// attaching when the cached client sits on a different tab.
//
// Attaching can fail — a wedged tab refuses the handshake until the attach times
// out — and the error is returned rather than swallowed: falling back to the
// previously attached tab would silently act on the wrong page.
func (s *browserService) syncActiveClient(ctx context.Context, cdpMgr *browser.CDPManager, cached browser.CDPSession) (browser.CDPSession, error) {
	// The tab may not just have moved on — it may be gone, in which case the
	// cached client is attached to nothing and every action lands nowhere.
	live, err := cdpMgr.EnsureLiveTarget(ctx)
	if err != nil {
		return nil, err
	}
	if live != nil {
		cached = live
	}

	want := cdpMgr.ActiveTargetID()
	if want == "" || cached.TargetID() == want {
		return cached, nil
	}
	s.deps.logger.Info("getBrowserCDP: active tab moved, attaching",
		"target_id", want, "attached_target_id", cached.TargetID())
	client, err := cdpMgr.GetOrCreate(ctx, want)
	if err != nil {
		return nil, fmt.Errorf("attaching to active tab %s: %w", want, err)
	}
	cdpMgr.SwitchActive(want)
	return client, nil
}

// ensureBrowserCapture initializes console/network capture for a channel if not already started.
// If capture is already active, rewires to the current client (e.g. after a tab switch).
func (s *browserService) ensureBrowserCapture(ctx context.Context, channelID string, cdpCl browser.CDPSession) {
	s.capturesMu.Lock()
	defer s.capturesMu.Unlock()

	cs, exists := s.captures[channelID]
	if exists && cs.Started {
		// Rewire capture if the active client has changed (e.g. tab switch).
		// No-op if client is the same.
		cs.Rewire(ctx, cdpCl)
		return
	}

	cs = &browser.CaptureState{}
	s.captures[channelID] = cs
	cs.Enable(ctx, cdpCl)
}

// dispatchBrowserAction dispatches a browser action to the CDP client.
// Uses a detached context.Background() so the browser action survives
// the HTTP request's lifecycle (long CDP calls shouldn't abort if the
// caller navigates away).
func (s *browserService) dispatchBrowserAction(req browserActionRequest, cdpCl browser.CDPSession) browserActionResponse {
	bg := context.Background()
	params := req.Params

	switch req.Action {
	case "navigate":
		url := paramStr(params, "url")
		if err := cdpCl.Navigate(bg, url); err != nil {
			return browserActionResponse{Error: fmt.Sprintf("navigate failed: %v", err)}
		}
		info, err := cdpCl.GetPageInfo(bg)
		if err != nil {
			return browserActionResponse{Error: fmt.Sprintf("get page info failed: %v", err)}
		}
		// Notify the pane so it updates the tab bar URL/title.
		if cdpMgr := s.getActiveCDPManager(req.ChannelID); cdpMgr != nil {
			activeTarget := cdpMgr.ActiveTargetID()
			if activeTarget != "" {
				cdpMgr.NotifyTargetSwitch(activeTarget)
			}
		}
		return browserActionResponse{Result: fmt.Sprintf("Navigated to %s", info.URL), PageInfo: info}

	case "reload":
		if err := cdpCl.Reload(bg); err != nil {
			return browserActionResponse{Error: fmt.Sprintf("reload failed: %v", err)}
		}
		return browserActionResponse{Result: "Page reloaded"}

	case "go_back":
		if err := cdpCl.GoBack(bg); err != nil {
			return browserActionResponse{Error: fmt.Sprintf("go back failed: %v", err)}
		}
		return browserActionResponse{Result: "Navigated back"}

	case "go_forward":
		if err := cdpCl.GoForward(bg); err != nil {
			return browserActionResponse{Error: fmt.Sprintf("go forward failed: %v", err)}
		}
		return browserActionResponse{Result: "Navigated forward"}

	case "get_page_info":
		info, err := cdpCl.GetPageInfo(bg)
		if err != nil {
			return browserActionResponse{Error: fmt.Sprintf("get page info failed: %v", err)}
		}
		return browserActionResponse{PageInfo: info}

	case "get_element_refs":
		refs, err := cdpCl.GetElementRefs(bg)
		if err != nil {
			return browserActionResponse{Error: fmt.Sprintf("get element refs failed: %v", err)}
		}
		return browserActionResponse{ElementRefs: refs}

	case "mouse_click":
		x := paramFloat(params, "x")
		y := paramFloat(params, "y")
		button := paramStr(params, "button")
		if button == "" {
			button = "left"
		}
		clickCount := paramInt(params, "click_count")
		if clickCount == 0 {
			clickCount = 1
		}
		if err := cdpCl.MouseClick(bg, x, y, button, clickCount); err != nil {
			return browserActionResponse{Error: fmt.Sprintf("mouse click failed: %v", err)}
		}
		return browserActionResponse{Result: fmt.Sprintf("Clicked at (%.0f, %.0f)", x, y)}

	case "mouse_move":
		x := paramFloat(params, "x")
		y := paramFloat(params, "y")
		if err := cdpCl.MouseMove(bg, x, y, 0); err != nil {
			return browserActionResponse{Error: fmt.Sprintf("mouse move failed: %v", err)}
		}
		return browserActionResponse{Result: fmt.Sprintf("Moved mouse to (%.0f, %.0f)", x, y)}

	case "mouse_scroll":
		x := paramFloat(params, "x")
		y := paramFloat(params, "y")
		deltaX := paramFloat(params, "delta_x")
		deltaY := paramFloat(params, "delta_y")
		if err := cdpCl.MouseScroll(bg, x, y, deltaX, deltaY); err != nil {
			return browserActionResponse{Error: fmt.Sprintf("mouse scroll failed: %v", err)}
		}
		return browserActionResponse{Result: fmt.Sprintf("Scrolled at (%.0f, %.0f) by (%.0f, %.0f)", x, y, deltaX, deltaY)}

	case "mouse_down":
		x := paramFloat(params, "x")
		y := paramFloat(params, "y")
		button := paramStr(params, "button")
		if button == "" {
			button = "left"
		}
		if err := cdpCl.MouseDown(bg, x, y, button); err != nil {
			return browserActionResponse{Error: fmt.Sprintf("mouse down failed: %v", err)}
		}
		return browserActionResponse{Result: fmt.Sprintf("Mouse down at (%.0f, %.0f)", x, y)}

	case "mouse_up":
		x := paramFloat(params, "x")
		y := paramFloat(params, "y")
		button := paramStr(params, "button")
		if button == "" {
			button = "left"
		}
		if err := cdpCl.MouseUp(bg, x, y, button); err != nil {
			return browserActionResponse{Error: fmt.Sprintf("mouse up failed: %v", err)}
		}
		return browserActionResponse{Result: fmt.Sprintf("Mouse up at (%.0f, %.0f)", x, y)}

	case "key_press":
		key := paramStr(params, "key")
		if err := cdpCl.KeyPress(bg, key, 0); err != nil {
			return browserActionResponse{Error: fmt.Sprintf("key press failed: %v", err)}
		}
		return browserActionResponse{Result: fmt.Sprintf("Pressed key %q", key)}

	case "type_text":
		text := paramStr(params, "text")
		if err := cdpCl.TypeText(bg, text); err != nil {
			return browserActionResponse{Error: fmt.Sprintf("type text failed: %v", err)}
		}
		return browserActionResponse{Result: fmt.Sprintf("Typed %q", text)}

	case "click_ref":
		var refs []browser.ElementRef
		if refsRaw, ok := params["refs"].([]any); ok {
			for _, r := range refsRaw {
				if m, ok := r.(map[string]any); ok {
					data, _ := json.Marshal(m)
					var ref browser.ElementRef
					if err := json.Unmarshal(data, &ref); err == nil {
						refs = append(refs, ref)
					}
				}
			}
		}
		refIndex := paramInt(params, "ref_index")
		if err := cdpCl.ClickRef(bg, refs, refIndex); err != nil {
			return browserActionResponse{Error: fmt.Sprintf("click ref failed: %v", err)}
		}
		return browserActionResponse{Result: fmt.Sprintf("Clicked ref %d", refIndex)}

	case "screenshot":
		data, err := cdpCl.Screenshot(bg)
		if err != nil {
			return browserActionResponse{Error: fmt.Sprintf("screenshot failed: %v", err)}
		}
		if s.screenshotDir != "" {
			fname := fmt.Sprintf("screenshot-%d.png", time.Now().UnixNano())
			fpath := filepath.Join(s.screenshotDir, fname)
			if err := os.WriteFile(fpath, data, 0o644); err != nil {
				return browserActionResponse{Error: fmt.Sprintf("writing screenshot file: %v", err)}
			}
			return browserActionResponse{ScreenshotPath: fpath}
		}
		return browserActionResponse{Image: base64.StdEncoding.EncodeToString(data)}

	case "evaluate_js":
		expression := paramStr(params, "expression")
		result, err := cdpCl.EvaluateJS(bg, expression)
		if err != nil {
			return browserActionResponse{Error: fmt.Sprintf("evaluate JS failed: %v", err)}
		}
		return browserActionResponse{Result: result}

	case "list_tabs":
		cdpMgr := s.getActiveCDPManager(req.ChannelID)
		tabs, err := cdpCl.ListTabs(bg)
		if err != nil {
			return browserActionResponse{Error: fmt.Sprintf("list tabs failed: %v", err)}
		}
		// Filter to agent-tracked tabs only.
		if cdpMgr != nil {
			var filtered []browser.TabInfo
			for _, t := range tabs {
				if cdpMgr.IsTrackedTab(t.TargetID) {
					filtered = append(filtered, t)
				}
			}
			tabs = filtered
		}
		if cdpMgr != nil {
			tabs = cdpMgr.OrderTabs(tabs)
		}
		activeTarget := ""
		if cdpMgr != nil {
			activeTarget = cdpMgr.ActiveTargetID()
		}
		for i := range tabs {
			if tabs[i].TargetID == activeTarget {
				tabs[i].Active = true
			}
		}
		return browserActionResponse{Tabs: tabs}

	case "new_tab":
		url := paramStr(params, "url")
		if url == "" {
			url = "about:blank"
		}
		targetID, err := cdpCl.NewTab(bg, url)
		if err != nil {
			return browserActionResponse{Error: fmt.Sprintf("new tab failed: %v", err)}
		}
		if cdpMgr := s.getActiveCDPManager(req.ChannelID); cdpMgr != nil {
			cdpMgr.TrackTab(targetID)
			cdpMgr.NotifyTabAdded(browser.TabInfo{TargetID: targetID, URL: url})
			cdpMgr.NotifyTargetSwitch(targetID)
		}
		return browserActionResponse{Result: fmt.Sprintf("Created new tab with target ID %s", targetID)}

	case "switch_tab":
		targetID := paramStr(params, "target_id")
		if targetID == "" {
			return browserActionResponse{Error: "target_id required"}
		}
		if cdpMgr := s.getActiveCDPManager(req.ChannelID); cdpMgr != nil {
			cdpMgr.SwitchActive(targetID)
			cdpMgr.NotifyTargetSwitch(targetID)
		}
		return browserActionResponse{Result: fmt.Sprintf("Switched to tab %s", targetID)}

	case "close_tab":
		targetID := paramStr(params, "target_id")
		if targetID == "" {
			return browserActionResponse{Error: "target_id required"}
		}
		s.deps.logger.Info("dispatchBrowserAction: close_tab", "target_id", targetID, "channel_id", req.ChannelID)
		cdpMgr := s.getActiveCDPManager(req.ChannelID)
		nextTab := ""
		if cdpMgr != nil {
			nextTab = cdpMgr.NextTabID(targetID)
		}
		// When closing the last tab, create a replacement about:blank BEFORE
		// closing — the active CDP client's context dies with the closed tab,
		// so NewTab would fail after CloseTab.
		isLastTab := cdpMgr != nil && nextTab == ""
		if isLastTab {
			if newID, err := cdpCl.NewTab(bg, "about:blank"); err == nil {
				cdpMgr.TrackTab(newID)
				nextTab = newID
				s.deps.logger.Info("dispatchBrowserAction: created replacement tab", "new_target_id", newID)
			}
		}
		if err := cdpCl.CloseTab(bg, targetID); err != nil {
			return browserActionResponse{Error: fmt.Sprintf("close tab failed: %v", err)}
		}
		if cdpMgr != nil {
			cdpMgr.UntrackTab(targetID)
			cdpMgr.NotifyTabRemoved(targetID)
			if isLastTab && nextTab != "" {
				cdpMgr.NotifyTabAdded(browser.TabInfo{TargetID: nextTab, URL: "about:blank"})
			}
			if nextTab != "" {
				cdpMgr.NotifyTargetSwitch(nextTab)
			}
		}
		return browserActionResponse{Result: fmt.Sprintf("Closed tab %s", targetID)}

	case "resize_window":
		width := paramInt(params, "width")
		height := paramInt(params, "height")
		if err := cdpCl.ResizeWindow(bg, width, height); err != nil {
			return browserActionResponse{Error: fmt.Sprintf("resize window failed: %v", err)}
		}
		return browserActionResponse{Result: fmt.Sprintf("Resized viewport to %dx%d", width, height)}

	case "scroll_into_view":
		backendNodeID := cdp.BackendNodeID(paramInt(params, "backend_node_id"))
		if err := cdpCl.ScrollIntoView(bg, backendNodeID); err != nil {
			return browserActionResponse{Error: fmt.Sprintf("scroll into view failed: %v", err)}
		}
		return browserActionResponse{Result: "Scrolled element into view"}

	case "read_console":
		return s.readConsoleMessages(req.ChannelID, params)

	case "read_network":
		return s.readNetworkRequests(req.ChannelID, params)

	default:
		return browserActionResponse{Error: fmt.Sprintf("unknown action: %s", req.Action)}
	}
}

// paramStr extracts a string param from params map.
func paramStr(params map[string]any, key string) string {
	if v, ok := params[key].(string); ok {
		return v
	}
	return ""
}

// paramFloat extracts a float64 param from params map.
func paramFloat(params map[string]any, key string) float64 {
	if v, ok := params[key].(float64); ok {
		return v
	}
	return 0
}

// paramInt extracts an int param from params map (JSON numbers are float64).
func paramInt(params map[string]any, key string) int {
	if v, ok := params[key].(float64); ok {
		return int(v)
	}
	return 0
}

// paramBool extracts a bool param from params map.
func paramBool(params map[string]any, key string) bool {
	if v, ok := params[key].(bool); ok {
		return v
	}
	return false
}

// readConsoleMessages reads captured console messages with optional filtering.
func (s *browserService) readConsoleMessages(channelID string, params map[string]any) browserActionResponse {
	s.capturesMu.Lock()
	cs := s.captures[channelID]
	s.capturesMu.Unlock()

	if cs == nil {
		return browserActionResponse{Result: "No console messages"}
	}

	result, err := cs.ReadConsole(paramStr(params, "pattern"), paramBool(params, "only_errors"), paramInt(params, "limit"), paramBool(params, "clear"))
	if err != nil {
		return browserActionResponse{Error: err.Error()}
	}
	return browserActionResponse{Result: result}
}

// readNetworkRequests reads captured network requests with optional filtering.
func (s *browserService) readNetworkRequests(channelID string, params map[string]any) browserActionResponse {
	s.capturesMu.Lock()
	cs := s.captures[channelID]
	s.capturesMu.Unlock()

	if cs == nil {
		return browserActionResponse{Result: "No network requests"}
	}

	result, err := cs.ReadNetwork(paramStr(params, "pattern"), paramInt(params, "limit"), paramBool(params, "clear"))
	if err != nil {
		return browserActionResponse{Error: err.Error()}
	}
	return browserActionResponse{Result: result}
}

// runIdleMonitor periodically checks for idle browser sessions and stops them.
func (s *browserService) runIdleMonitor(ctx context.Context, timeout, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.cleanIdleBrowserSessions(ctx, timeout)
		}
	}
}

// cleanIdleBrowserSessions collects idle CDPManagers and cleans them up.
func (s *browserService) cleanIdleBrowserSessions(ctx context.Context, timeout time.Duration) {
	s.cdpManagersMu.Lock()
	now := time.Now()
	var idle []string
	for key, mgr := range s.cdpManagers {
		if mgr.PaneCount() == 0 && now.Sub(mgr.LastUsedAt()) > timeout {
			idle = append(idle, key)
		}
	}
	s.cdpManagersMu.Unlock()

	for _, key := range idle {
		parts := strings.SplitN(key, "|", 2)
		channelID, mode := parts[0], parts[1]

		s.cdpManagersMu.Lock()
		mgr := s.cdpManagers[key]
		delete(s.cdpManagers, key)
		s.cdpManagersMu.Unlock()

		if mgr != nil {
			mgr.Close()
		}
		if mode == "docker" && s.dockerProvider != nil {
			containerID, _ := s.dockerProvider.StopBrowser(ctx, channelID)
			if containerID != "" && s.containerRegistry != nil {
				s.containerRegistry.ScheduleRemove(containerID, s.keepAlive)
			}
		}
	}
}

// cleanup stops all docker browser containers during shutdown.
func (s *browserService) cleanup(ctx context.Context) {
	if c, ok := s.dockerProvider.(BrowserCleaner); ok {
		c.Cleanup(ctx)
	}
}
