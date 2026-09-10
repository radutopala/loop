package browser

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/chromedp/cdproto/cdp"
)

// ErrTargetGone reports that the tab a caller asked for is no longer open.
// Callers that have somewhere else to go — the pane, which is already showing
// a working tab — can tell this apart from a connection that is broken.
var ErrTargetGone = errors.New("tab is no longer open")

// hasTarget reports whether targetID is among the open page targets.
func hasTarget(tabs []TabInfo, targetID string) bool {
	for _, t := range tabs {
		if t.TargetID == targetID {
			return true
		}
	}
	return false
}

// CDPManagerConfig configures a CDPManager instance.
type CDPManagerConfig struct {
	DiscoverExisting bool          // if true, discover+track existing Chrome tabs on Connect
	MaxRetries       int           // max retry attempts for CDP connection
	RetryDelay       time.Duration // delay between retries
}

// CDPManager owns the CDP connection and all tab state for a single
// channelID+mode pair.  It replaces the sessionManager's CDP/tab-tracking
// responsibilities that were previously spread across providers.
type CDPManager struct {
	wsEndpoint string
	cfg        CDPManagerConfig
	logger     *slog.Logger

	// Factory — set to real NewCDPClient by default, injectable for tests.
	cdpFactory func(ctx context.Context, wsURL string, logger *slog.Logger, opts ...CDPOption) (CDPSession, error)

	mu             sync.Mutex
	connected      bool
	activeTargetID string
	client         CDPSession // the initial CDP client (from Connect), used to create child contexts
	activeClient   CDPSession // the currently active tab's client
	tabOrder       []string   // ordered target IDs
	paneCount      int
	lastUsedAt     time.Time

	targetSwitchCh chan string  // signals MCP-initiated tab switches
	tabAddedCh     chan TabInfo // signals MCP-initiated tab additions
	tabRemovedCh   chan string  // signals MCP-initiated tab removals

	timeNow func() time.Time // injectable clock
}

// CDPSession is the subset of CDPClient that CDPManager needs.
// In production this is *CDPClient; in tests it can be a mock.
type CDPSession interface {
	TargetID() string
	SwitchTarget(targetID string) error
	ListTabs(ctx context.Context) ([]TabInfo, error)
	// Favicons maps page target ID to the icon Chrome resolved for that tab.
	Favicons() map[string]string
	NewTab(ctx context.Context, url string) (string, error)
	CloseTab(ctx context.Context, targetID string) error
	Close()
	// Alive reports whether the session can still be used; a cached client whose
	// context died with its container must be dropped rather than reused.
	Alive() bool
	// CloseBrowser shuts Chrome down cleanly so it commits its profile to disk.
	CloseBrowser(ctx context.Context) error
	ResetScreencast()
	StartScreencast(quality, maxWidth, maxHeight int) <-chan []byte
	StopScreencast()
	Navigate(ctx context.Context, url string) error
	Reload(ctx context.Context) error
	GoBack(ctx context.Context) error
	GoForward(ctx context.Context) error
	GetPageInfo(ctx context.Context) (*PageInfo, error)
	MouseClick(ctx context.Context, x, y float64, button string, clickCount int) error
	MouseMove(ctx context.Context, x, y float64, buttons int) error
	MouseScroll(ctx context.Context, x, y, deltaX, deltaY float64) error
	KeyPress(ctx context.Context, key string, modifiers int) error
	TypeText(ctx context.Context, text string) error
	InsertText(ctx context.Context, text string) error
	ReadSelection(ctx context.Context) (string, error)
	EvaluateJS(ctx context.Context, expression string) (string, error)
	Screenshot(ctx context.Context) ([]byte, error)
	GetElementRefs(ctx context.Context) ([]ElementRef, error)
	ClickRef(ctx context.Context, refs []ElementRef, refIndex int) error
	EnableConsoleCapture(ctx context.Context, ch chan<- ConsoleMessage) error
	EnableNetworkCapture(ctx context.Context, ch chan<- NetworkRequest) error
	ResizeWindow(ctx context.Context, width, height int) error
	ScrollIntoView(ctx context.Context, backendNodeID cdp.BackendNodeID) error
	MouseDown(ctx context.Context, x, y float64, button string) error
	MouseUp(ctx context.Context, x, y float64, button string) error
	// NewContextForTarget creates a new CDP client for a different target,
	// reusing the existing browser WS connection (no new dial / permission prompt).
	NewContextForTarget(targetID string) (CDPSession, error)
	// SetCookies installs cookies into the attached browser profile.
	SetCookies(ctx context.Context, cookies []Cookie) error
}

// NewCDPManager creates a new CDPManager for the given WebSocket endpoint.
func NewCDPManager(wsEndpoint string, cfg CDPManagerConfig, logger *slog.Logger) *CDPManager {
	return &CDPManager{
		wsEndpoint:     wsEndpoint,
		cfg:            cfg,
		logger:         logger,
		cdpFactory:     defaultCDPFactory,
		targetSwitchCh: make(chan string, 1),
		tabAddedCh:     make(chan TabInfo, 1),
		tabRemovedCh:   make(chan string, 1),
		timeNow:        time.Now,
	}
}

// defaultCDPFactory wraps NewCDPClient to match the cdpFactory signature.
func defaultCDPFactory(ctx context.Context, wsURL string, logger *slog.Logger, opts ...CDPOption) (CDPSession, error) {
	return NewCDPClient(ctx, wsURL, logger, opts...)
}

// Connect establishes the initial CDP connection with retries.
// On success, the active client is available via ActiveClient().
func (m *CDPManager) Connect(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var cdpOpts []CDPOption
	if m.cfg.DiscoverExisting {
		// Docker mode: attach to Chrome's existing first page target so the panel
		// shares the SAME tab the agent's mcp-browser tools drive.
		cdpOpts = append(cdpOpts, WithDiscoverExisting())
	} else {
		// Host mode: always create a new target.
		cdpOpts = append(cdpOpts, WithNewTarget())
	}

	var client CDPSession
	var lastErr error

	for attempt := range m.cfg.MaxRetries {
		client, lastErr = m.cdpFactory(context.Background(), m.wsEndpoint, m.logger, cdpOpts...)
		if lastErr == nil {
			break
		}
		if attempt == m.cfg.MaxRetries-1 {
			return fmt.Errorf("connecting CDP after %d attempts: %w", m.cfg.MaxRetries, lastErr)
		}
		m.logger.Debug("CDP not ready, retrying", "attempt", attempt+1, "error", lastErr)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(m.cfg.RetryDelay):
		}
	}

	tid := client.TargetID()
	m.client = client
	m.activeClient = client
	if tid != "" {
		m.activeTargetID = tid
		m.trackTabLocked(tid)
		if m.cfg.DiscoverExisting {
			// Bring the sidecar's tab to the foreground. Headless Chrome never
			// acknowledges a mouse wheel event dispatched to a backgrounded
			// target, so Input.dispatchMouseEvent blocks forever — a scroll from
			// an agent tool would hang with no timeout to rescue it. The browser
			// pane already does this after connecting; doing it here covers the
			// case where no pane is ever opened. Host mode is left alone: that
			// tab belongs to the user, and stealing focus is not ours to do.
			if err := client.SwitchTarget(tid); err != nil {
				m.logger.Warn("activating CDP target failed", "target_id", tid, "error", err)
			}
		}
	}

	m.connected = true
	m.lastUsedAt = m.timeNow()
	return nil
}

// IsConnected returns true if Connect() has been successfully called.
func (m *CDPManager) IsConnected() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.connected
}

// ActiveClient returns the CDPSession for the active target, or nil.
func (m *CDPManager) ActiveClient() CDPSession {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.activeClient
}

// ActiveTargetID returns the currently active target ID.
func (m *CDPManager) ActiveTargetID() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.activeTargetID
}

// GetOrCreate creates a new CDP client for the given target by reusing the
// browser WS connection from Connect(). Each call creates a fresh context —
// no caching. Must be called after Connect().
//
// A target that is no longer open is reported as ErrTargetGone instead of
// being attached to. Attaching to a dead target does not fail, it hangs: the
// attach sits there until its deadline, so a click on a tab whose renderer
// was killed would freeze the pane for fifteen seconds before saying
// anything. Chrome can answer "that tab is gone" immediately, so ask.
func (m *CDPManager) GetOrCreate(ctx context.Context, targetID string) (CDPSession, error) {
	m.mu.Lock()
	initial := m.client
	m.mu.Unlock()

	if initial == nil {
		return nil, fmt.Errorf("no CDP connection for target %s (call Connect first)", targetID)
	}

	// Only a listing that succeeded is evidence: if Chrome will not say what
	// is open, attach and let the deadline decide, exactly as before.
	if tabs, err := initial.ListTabs(ctx); err == nil && !hasTarget(tabs, targetID) {
		return nil, fmt.Errorf("attaching to target %s: %w", targetID, ErrTargetGone)
	}
	return m.attach(initial, targetID)
}

// attach opens a session on targetID over the browser connection and makes it
// the active client, with no liveness check. Callers that already know the
// target is open use this: re-listing would cost a round-trip to learn what
// they just saw, and a tab opened moments ago may not be listed yet.
func (m *CDPManager) attach(initial CDPSession, targetID string) (CDPSession, error) {
	newClient, err := initial.NewContextForTarget(targetID)
	if err != nil {
		return nil, fmt.Errorf("attaching to target %s: %w", targetID, err)
	}

	m.mu.Lock()
	m.activeClient = newClient
	m.mu.Unlock()
	return newClient, nil
}

// EnsureLiveTarget re-points the active client when the tab it is attached to
// has gone away.
//
// Alive only reports on the connection, so a client whose page target was
// closed — by window.close, a crashed renderer, or anything driving CDP
// alongside us — stays "alive" and every action on it silently goes nowhere:
// the pane keeps asking a dead session for a screencast and shows a blank
// rectangle for as long as the daemon runs. The browser itself is fine, so
// dropping the whole manager is the wrong cure; re-attaching is the right
// one, and Chrome with no tabs at all gets one.
func (m *CDPManager) EnsureLiveTarget(ctx context.Context) (CDPSession, error) {
	m.mu.Lock()
	client, want, initial := m.activeClient, m.activeTargetID, m.client
	m.mu.Unlock()

	// No active target means there is nothing to check: the client was never
	// pointed at a page, so it cannot have lost one.
	if client == nil || want == "" {
		return client, nil
	}
	tabs, err := client.ListTabs(ctx)
	if err != nil {
		// Reuse the client rather than guess: a failed listing is not
		// evidence the target is gone.
		return client, nil
	}
	if hasTarget(tabs, want) {
		return client, nil
	}

	tid := ""
	if len(tabs) > 0 {
		tid = tabs[0].TargetID
	} else if tid, err = client.NewTab(ctx, "about:blank"); err != nil {
		return nil, fmt.Errorf("opening a tab after the last one closed: %w", err)
	}

	m.logger.Info("CDP target vanished, re-attaching", "gone_target_id", want, "target_id", tid)
	fresh, err := m.attach(initial, tid)
	if err != nil {
		return nil, err
	}
	m.SwitchActive(tid)
	m.TrackTab(tid)
	return fresh, nil
}

// SwitchActive sets the active target ID and updates lastUsedAt.
func (m *CDPManager) SwitchActive(targetID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.activeTargetID = targetID
	m.lastUsedAt = m.timeNow()
}

// SetClientForTarget sets the active client and target ID.
func (m *CDPManager) SetClientForTarget(targetID string, client CDPSession) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.activeClient = client
	m.activeTargetID = targetID
	if client != nil && m.client == nil {
		m.client = client // first client becomes the initial connection
	}
}

// RemoveClientForTarget clears the active client if it matches the target.
func (m *CDPManager) RemoveClientForTarget(targetID string) CDPSession {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.activeTargetID == targetID {
		old := m.activeClient
		m.activeClient = nil
		return old
	}
	return nil
}

// --- Pane tracking ---

// PaneConnected increments the pane count and updates lastUsedAt.
func (m *CDPManager) PaneConnected() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.paneCount++
	m.lastUsedAt = m.timeNow()
}

// PaneDisconnected decrements the pane count (min 0).
func (m *CDPManager) PaneDisconnected() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.paneCount > 0 {
		m.paneCount--
	}
}

// PaneCount returns the current pane count.
func (m *CDPManager) PaneCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.paneCount
}

// LastUsedAt returns the last usage timestamp.
func (m *CDPManager) LastUsedAt() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastUsedAt
}

// Touch updates lastUsedAt to now.
func (m *CDPManager) Touch() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastUsedAt = m.timeNow()
}

// --- Tab tracking ---

func (m *CDPManager) trackTabLocked(targetID string) {
	for _, id := range m.tabOrder {
		if id == targetID {
			return
		}
	}
	m.tabOrder = append(m.tabOrder, targetID)
}

// TrackTab adds a target to the tab order (idempotent).
func (m *CDPManager) TrackTab(targetID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.trackTabLocked(targetID)
}

// UntrackTab removes a target from the tab order.
func (m *CDPManager) UntrackTab(targetID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.untrackTabLocked(targetID)
}

func (m *CDPManager) untrackTabLocked(targetID string) {
	filtered := make([]string, 0, len(m.tabOrder))
	for _, id := range m.tabOrder {
		if id != targetID {
			filtered = append(filtered, id)
		}
	}
	m.tabOrder = filtered
}

// IsTrackedTab returns true if the target is in the tab order.
func (m *CDPManager) IsTrackedTab(targetID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, id := range m.tabOrder {
		if id == targetID {
			return true
		}
	}
	return false
}

// NextTabID returns the adjacent tab when a tab is being closed.
func (m *CDPManager) NextTabID(closedTargetID string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, id := range m.tabOrder {
		if id == closedTargetID {
			if i > 0 {
				return m.tabOrder[i-1]
			}
			if i+1 < len(m.tabOrder) {
				return m.tabOrder[i+1]
			}
			return ""
		}
	}
	return ""
}

// OrderTabs reorders tabs according to the tracked tab order.
func (m *CDPManager) OrderTabs(tabs []TabInfo) []TabInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.tabOrder) == 0 {
		return tabs
	}

	byID := make(map[string]TabInfo, len(tabs))
	for _, t := range tabs {
		byID[t.TargetID] = t
	}

	ordered := make([]TabInfo, 0, len(tabs))
	for _, id := range m.tabOrder {
		if t, ok := byID[id]; ok {
			ordered = append(ordered, t)
			delete(byID, id)
		}
	}
	for _, t := range tabs {
		if _, exists := byID[t.TargetID]; exists {
			ordered = append(ordered, t)
			m.tabOrder = append(m.tabOrder, t.TargetID)
		}
	}
	return ordered
}

// --- Notification channels ---

// NotifyTargetSwitch signals a target switch to watching goroutines.
func (m *CDPManager) NotifyTargetSwitch(targetID string) {
	m.mu.Lock()
	m.activeTargetID = targetID
	m.mu.Unlock()
	select {
	case m.targetSwitchCh <- targetID:
	default:
	}
}

// TargetSwitchCh returns the channel for target switch notifications.
func (m *CDPManager) TargetSwitchCh() <-chan string {
	return m.targetSwitchCh
}

// NotifyTabAdded signals a tab addition to watching goroutines.
func (m *CDPManager) NotifyTabAdded(tab TabInfo) {
	select {
	case m.tabAddedCh <- tab:
	default:
	}
}

// TabAddedCh returns the channel for tab added notifications.
func (m *CDPManager) TabAddedCh() <-chan TabInfo {
	return m.tabAddedCh
}

// NotifyTabRemoved signals a tab removal to watching goroutines.
func (m *CDPManager) NotifyTabRemoved(targetID string) {
	select {
	case m.tabRemovedCh <- targetID:
	default:
	}
}

// TabRemovedCh returns the channel for tab removed notifications.
func (m *CDPManager) TabRemovedCh() <-chan string {
	return m.tabRemovedCh
}

// Close closes the CDP connection and resets state.
func (m *CDPManager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.client != nil {
		// Every caller closes the manager on its way to stopping the sidecar, so
		// give Chrome the chance to write the profile out first — it does not
		// flush when the container is stopped from outside. Host mode is
		// excluded: that browser belongs to the user and closing it is not ours
		// to do.
		if m.cfg.DiscoverExisting && m.client.Alive() {
			if err := m.client.CloseBrowser(context.Background()); err != nil {
				m.logger.Warn("closing Chrome cleanly failed", "error", err)
			}
		}
		m.client.Close()
	}
	m.client = nil
	m.activeClient = nil
	m.connected = false
	m.activeTargetID = ""
	m.tabOrder = nil
}

// WSEndpoint returns the WebSocket endpoint this manager is configured for.
func (m *CDPManager) WSEndpoint() string {
	return m.wsEndpoint
}

// DiscoverExisting returns the DiscoverExisting config flag.
func (m *CDPManager) DiscoverExisting() bool {
	return m.cfg.DiscoverExisting
}

// SetCDPFactoryForTest overrides the CDP factory for testing.
func SetCDPFactoryForTest(m *CDPManager, factory func(ctx context.Context, wsURL string, logger *slog.Logger, opts ...CDPOption) (CDPSession, error)) {
	m.cdpFactory = factory
}

// SetTimeNowForTest overrides the time function for testing.
func SetTimeNowForTest(m *CDPManager, fn func() time.Time) {
	m.timeNow = fn
}
