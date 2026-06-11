package browser

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/chromedp/cdproto/cdp"
)

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
	NewTab(ctx context.Context, url string) (string, error)
	CloseTab(ctx context.Context, targetID string) error
	Close()
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
	KeyPress(ctx context.Context, key string) error
	TypeText(ctx context.Context, text string) error
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
func (m *CDPManager) GetOrCreate(targetID string) (CDPSession, error) {
	m.mu.Lock()
	initial := m.client
	m.mu.Unlock()

	if initial == nil {
		return nil, fmt.Errorf("no CDP connection for target %s (call Connect first)", targetID)
	}

	newClient, err := initial.NewContextForTarget(targetID)
	if err != nil {
		return nil, fmt.Errorf("attaching to target %s: %w", targetID, err)
	}

	m.mu.Lock()
	m.activeClient = newClient
	m.mu.Unlock()
	return newClient, nil
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
