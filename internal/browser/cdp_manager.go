package browser

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
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
	DownloadDir      string        // if set, where Chrome saves downloads (a path in Chrome's filesystem)
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

	// subs fan tab changes out to every pane watching this manager. One
	// shared channel would hand each change to whichever pane read it first,
	// so a second pane — or a stale watcher — silently swallowed it.
	subs   map[int]chan TabEvent
	nextID int
	// stopTitlePoll ends the title poll that runs while anyone subscribes;
	// nil when none runs.
	stopTitlePoll chan struct{}

	// dialogs holds the JavaScript dialog each tab is blocked on, with the
	// client that saw it open — the only kind of session able to answer it.
	dialogs map[string]pendingDialog

	// fileChoosers holds the file chooser each tab has open, with the client
	// that saw it open, until the pane answers it.
	fileChoosers map[string]pendingFileChooser

	// tabInfo is the URL and title last published for each tab. Every
	// attached client sees a tab's change, so this keeps the panes from
	// hearing it once per client.
	tabInfo map[string]TabInfo

	// sessions holds the one client attached to each tab, which also
	// watches it. Every client attached to the same tab would hear each of
	// its events again, accepting an alert once per client, and none of
	// them can be let go while the tab lives: cancelling an attached
	// session closes its tab. So a tab is attached once and that client is
	// handed out from then on, until the tab goes.
	sessions map[string]CDPSession
	// attaching holds, per tab, the attach in flight; a second caller waits
	// for it rather than opening a session of its own.
	attaching map[string]chan struct{}

	// events runs the watched clients' events one at a time, in the order
	// they arrived; see enqueue.
	events eventQueue

	// titleRechecks are the waits, one after another, before looking up a
	// tab's title again after it navigates: Chrome reports the navigation
	// with the URL standing in for the title and stays quiet when the page
	// sets its real one.
	titleRechecks []time.Duration
	sleep         func(time.Duration)
	// background runs the title rechecks, off the event queue; tests run them
	// in place.
	background func(func())
	// titlePollEvery is how often the tabs are listed again while a pane
	// watches, for titles a page sets after the rechecks are over; after
	// times the waits, so tests can tick it by hand.
	titlePollEvery time.Duration
	after          func(time.Duration) <-chan time.Time

	timeNow func() time.Time // injectable clock
}

// CDPSession is the subset of CDPClient that CDPManager needs.
// In production this is *CDPClient; in tests it can be a mock.
type CDPSession interface {
	TargetID() string
	SwitchTarget(targetID string) error
	// SwitchTab brings a tab to the front without moving the session onto it.
	SwitchTab(ctx context.Context, targetID string) error
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
	StartScreencast(quality, maxWidth, maxHeight int, stop <-chan struct{}) <-chan []byte
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
	FullPageScreenshot(ctx context.Context, maxHeight int) ([]byte, int, error)
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
	// WatchTargets reports page tabs Chrome opens, with the tab that opened
	// them ("" when none did), tabs whose URL or title changed, and targets
	// that close. The callbacks run on CDP's event loop and must not block.
	WatchTargets(onOpened func(tab TabInfo, openerID string), onChanged func(tab TabInfo), onClosed func(targetID string))
	// WatchDialogs reports JavaScript dialogs opening and closing on the
	// client's tab. The callbacks run on CDP's event loop and must not block.
	WatchDialogs(onOpened func(Dialog), onClosed func(targetID string))
	// HandleDialog accepts or dismisses the dialog open on the client's tab.
	HandleDialog(ctx context.Context, accept bool, promptText string) error
	// InterceptFileChoosers has Chrome report the client tab's file choosers
	// to WatchFileChoosers instead of cancelling them.
	InterceptFileChoosers(ctx context.Context) error
	// WatchFileChoosers reports file choosers the client's tab opens. The
	// callback runs on CDP's event loop and must not block.
	WatchFileChoosers(onOpened func(FileChooser))
	// SetChosenFiles answers the file chooser nodeID opened, cancelling it
	// when files is nil.
	SetChosenFiles(ctx context.Context, nodeID cdp.BackendNodeID, files []ChosenFile) error
	// SetCookies installs cookies into the attached browser profile.
	SetCookies(ctx context.Context, cookies []Cookie) error
	// SetDownloadDir has Chrome save downloads into dir.
	SetDownloadDir(ctx context.Context, dir string) error
	// WatchDownloads reports downloads, from any tab, starting and finishing.
	// The callback runs on CDP's event loop and must not block.
	WatchDownloads(onChange func(Download))
}

// NewCDPManager creates a new CDPManager for the given WebSocket endpoint.
func NewCDPManager(wsEndpoint string, cfg CDPManagerConfig, logger *slog.Logger) *CDPManager {
	return &CDPManager{
		wsEndpoint:     wsEndpoint,
		cfg:            cfg,
		logger:         logger,
		cdpFactory:     defaultCDPFactory,
		subs:           map[int]chan TabEvent{},
		tabInfo:        map[string]TabInfo{},
		titleRechecks:  []time.Duration{500 * time.Millisecond, time.Second, 2500 * time.Millisecond},
		sleep:          time.Sleep,
		background:     func(fn func()) { go fn() },
		titlePollEvery: 2 * time.Second,
		after:          time.After,
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
	m.sessions = map[string]CDPSession{tid: client}
	m.watch(client)
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

	if m.cfg.DownloadDir != "" {
		// A browser that refuses downloads still browses, so a failure here
		// is logged rather than failing the connection.
		if err := client.SetDownloadDir(ctx, m.cfg.DownloadDir); err != nil {
			m.logger.Warn("setting the download dir failed", "dir", m.cfg.DownloadDir, "error", err)
		} else {
			// On a goroutine for the reason watch gives; the pane copes with
			// the events arriving out of order.
			client.WatchDownloads(func(d Download) { go m.publish(TabEvent{Kind: DownloadChanged, Download: d}) })
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

// GetOrCreate returns the CDP client for the given target, attaching one over
// the browser WS connection from Connect() the first time the target is
// asked for. Must be called after Connect().
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

	// A tab blocked on a dialog never finishes an attach, so the session that
	// saw the dialog open is reused rather than waiting out the deadline.
	m.mu.Lock()
	p, blocked := m.dialogs[targetID]
	if blocked {
		m.activeClient = p.client
	}
	m.mu.Unlock()
	if blocked {
		return p.client, nil
	}

	// Only a listing that succeeded is evidence: if Chrome will not say what
	// is open, attach and let the deadline decide, exactly as before.
	if tabs, err := initial.ListTabs(ctx); err == nil && !hasTarget(tabs, targetID) {
		return nil, fmt.Errorf("attaching to target %s: %w", targetID, ErrTargetGone)
	}
	return m.attach(initial, targetID)
}

// attach makes targetID's client the active one, opening a session on it over
// the browser connection unless the tab already has one, with no liveness
// check. Callers that already know the target is open use this: re-listing
// would cost a round-trip to learn what they just saw, and a tab opened
// moments ago may not be listed yet.
func (m *CDPManager) attach(initial CDPSession, targetID string) (CDPSession, error) {
	m.mu.Lock()
	for {
		if cached, ok := m.sessions[targetID]; ok {
			m.activeClient = cached
			m.mu.Unlock()
			return cached, nil
		}
		inFlight, ok := m.attaching[targetID]
		if !ok {
			break
		}
		// A second session on the tab could never be let go, since
		// cancelling it would close the tab, so wait for the first.
		m.mu.Unlock()
		<-inFlight
		m.mu.Lock()
	}
	done := make(chan struct{})
	if m.attaching == nil {
		m.attaching = map[string]chan struct{}{}
	}
	m.attaching[targetID] = done
	m.mu.Unlock()

	newClient, err := initial.NewContextForTarget(targetID)

	m.mu.Lock()
	delete(m.attaching, targetID)
	close(done)
	if err != nil {
		m.mu.Unlock()
		return nil, fmt.Errorf("attaching to target %s: %w", targetID, err)
	}
	if m.sessions == nil {
		m.sessions = map[string]CDPSession{}
	}
	m.sessions[targetID] = newClient
	m.activeClient = newClient
	m.mu.Unlock()
	m.watch(newClient)
	return newClient, nil
}

// watch follows, through client, the tabs Chrome opens and closes on its own
// and the dialogs its tab opens. One client per tab watches, since the
// events arrive on a tab's session and stop when that tab closes; the
// handlers ignore the repeats several tabs' sessions still bring.
//
// The handlers run off chromedp's event loop, since taking m.mu there would
// deadlock against Connect, which holds the lock across CDP calls whose
// replies come through that same loop. They run in order, though: a dialog
// closed before it was seen opening would otherwise stay open for good.
func (m *CDPManager) watch(client CDPSession) {
	client.WatchTargets(
		func(tab TabInfo, openerID string) { m.events.enqueue(func() { m.tabOpened(tab, openerID) }) },
		func(tab TabInfo) { m.events.enqueue(func() { m.tabChanged(client, tab) }) },
		func(targetID string) { m.events.enqueue(func() { m.tabDestroyed(targetID) }) },
	)
	client.WatchDialogs(
		func(d Dialog) { m.events.enqueue(func() { m.dialogOpened(client, d) }) },
		func(targetID string) { m.events.enqueue(func() { m.dialogClosed(targetID) }) },
	)
	client.WatchFileChoosers(func(fc FileChooser) { m.events.enqueue(func() { m.fileChooserOpened(client, fc) }) })
	// Off the caller's path: a tab blocked on a dialog answers nothing until
	// the dialog goes, and attaching must not wait on it.
	go func() {
		if err := client.InterceptFileChoosers(context.Background()); err != nil {
			m.logger.Debug("intercepting file choosers failed", "target_id", client.TargetID(), "error", err)
		}
	}()
}

// ErrNoFileChooser reports that the tab has no file chooser open to answer.
var ErrNoFileChooser = errors.New("no file chooser is open on this tab")

// pendingFileChooser is a file chooser waiting for files, and the client to
// send them through.
type pendingFileChooser struct {
	chooser FileChooser
	client  CDPSession
}

// fileChooserOpened holds a file chooser for the pane to answer, telling
// subscribers about it. Every session on the tab sees it open; the repeats
// are dropped.
func (m *CDPManager) fileChooserOpened(client CDPSession, fc FileChooser) {
	m.mu.Lock()
	prev, seen := m.fileChoosers[fc.TargetID]
	if m.fileChoosers == nil {
		m.fileChoosers = map[string]pendingFileChooser{}
	}
	m.fileChoosers[fc.TargetID] = pendingFileChooser{chooser: fc, client: client}
	m.mu.Unlock()
	if seen && prev.chooser == fc {
		return
	}
	m.publish(TabEvent{Kind: FileChooserOpened, Tab: TabInfo{TargetID: fc.TargetID}, FileChooser: fc})
}

// PendingFileChooser returns the file chooser open on targetID, if any.
func (m *CDPManager) PendingFileChooser(targetID string) (FileChooser, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.fileChoosers[targetID]
	return p.chooser, ok
}

// PendingFileChoosers returns every open file chooser, ordered by tab.
func (m *CDPManager) PendingFileChoosers() []FileChooser {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]FileChooser, 0, len(m.fileChoosers))
	for _, p := range m.fileChoosers {
		out = append(out, p.chooser)
	}
	slices.SortFunc(out, func(a, b FileChooser) int { return strings.Compare(a.TargetID, b.TargetID) })
	return out
}

// AnswerFileChooser gives the file chooser open on targetID the picked
// files, or cancels it when files is nil. The chooser is done with either
// way: an answer that fails found its input gone, and a retry would too.
func (m *CDPManager) AnswerFileChooser(ctx context.Context, targetID string, files []ChosenFile) error {
	m.mu.Lock()
	p, ok := m.fileChoosers[targetID]
	delete(m.fileChoosers, targetID)
	m.mu.Unlock()
	if !ok {
		return ErrNoFileChooser
	}
	m.publish(TabEvent{Kind: FileChooserClosed, Tab: TabInfo{TargetID: targetID}})
	return p.client.SetChosenFiles(ctx, p.chooser.NodeID, files)
}

// ErrNoDialog reports that the tab has no JavaScript dialog open to answer.
var ErrNoDialog = errors.New("no JavaScript dialog is open on this tab")

// pendingDialog is a dialog waiting for an answer, and the client to give it
// through.
type pendingDialog struct {
	dialog Dialog
	client CDPSession
}

// dialogOpened answers an alert at once — it asks nothing, and leaving it
// open freezes the page — and holds any other dialog for the user or the
// agent to answer, telling subscribers about it.
func (m *CDPManager) dialogOpened(client CDPSession, d Dialog) {
	if d.Type == dialogTypeAlert {
		m.logger.Info("accepting a JavaScript alert", "target_id", d.TargetID, "message", d.Message)
		// Two sessions on one tab both see the alert; the second answer finds
		// it gone, which is fine.
		if err := client.HandleDialog(context.Background(), true, ""); err != nil {
			m.logger.Debug("accepting a JavaScript alert failed", "target_id", d.TargetID, "error", err)
		}
		return
	}
	m.mu.Lock()
	prev, seen := m.dialogs[d.TargetID]
	if m.dialogs == nil {
		m.dialogs = map[string]pendingDialog{}
	}
	m.dialogs[d.TargetID] = pendingDialog{dialog: d, client: client}
	m.mu.Unlock()
	if seen && prev.dialog == d {
		return
	}
	m.publish(TabEvent{Kind: DialogOpened, Tab: TabInfo{TargetID: d.TargetID}, Dialog: d})
}

// dialogClosed forgets the dialog on targetID once Chrome says it is gone,
// however it was answered.
func (m *CDPManager) dialogClosed(targetID string) {
	m.mu.Lock()
	_, ok := m.dialogs[targetID]
	delete(m.dialogs, targetID)
	m.mu.Unlock()
	if ok {
		m.publish(TabEvent{Kind: DialogClosed, Tab: TabInfo{TargetID: targetID}})
	}
}

// PendingDialog returns the dialog targetID is blocked on, if any.
func (m *CDPManager) PendingDialog(targetID string) (Dialog, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.dialogs[targetID]
	return p.dialog, ok
}

// PendingDialogs returns every dialog a tab is blocked on, ordered by tab.
func (m *CDPManager) PendingDialogs() []Dialog {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Dialog, 0, len(m.dialogs))
	for _, p := range m.dialogs {
		out = append(out, p.dialog)
	}
	slices.SortFunc(out, func(a, b Dialog) int { return strings.Compare(a.TargetID, b.TargetID) })
	return out
}

// HandleDialog answers the dialog targetID is blocked on: accept or dismiss
// it, with promptText as a prompt's answer.
func (m *CDPManager) HandleDialog(ctx context.Context, targetID string, accept bool, promptText string) error {
	m.mu.Lock()
	p, ok := m.dialogs[targetID]
	m.mu.Unlock()
	if !ok {
		return ErrNoDialog
	}
	if err := p.client.HandleDialog(ctx, accept, promptText); err != nil {
		return err
	}
	// Chrome says so too, but a follow-up action must not race that event and
	// find the tab still blocked.
	m.dialogClosed(targetID)
	return nil
}

// tabOpened adopts a tab that a tracked tab opened — a target=_blank link or
// window.open — and makes it the active one, as Chrome brings it to the front.
// Left alone it stayed out of the strip and out of list_tabs, and the pane
// kept streaming the tab it covered, which Chrome stops painting: a frozen
// pane showing neither page.
func (m *CDPManager) tabOpened(tab TabInfo, openerID string) {
	m.mu.Lock()
	adopt := openerID != "" && m.isTrackedLocked(openerID) && !m.isTrackedLocked(tab.TargetID)
	if adopt {
		m.trackTabLocked(tab.TargetID)
	}
	m.mu.Unlock()
	if !adopt {
		return
	}
	m.logger.Info("page opened a tab, following it", "target_id", tab.TargetID, "opener_id", openerID)
	m.NotifyTabAdded(tab)
	m.NotifyTargetSwitch(tab.TargetID)
}

// tabChanged tells the panes a tracked tab navigated — the agent's
// navigations and the page's own, which the pane has no other way to hear
// about — and, since Chrome won't say when the new page sets its title, looks
// the title up again through client while the page loads.
//
// A file chooser does not outlive its page, and Chrome says nothing when it
// goes, so a navigation drops the tab's chooser.
func (m *CDPManager) tabChanged(client CDPSession, tab TabInfo) {
	if m.updateTab(tab) {
		m.dropFileChooser(tab.TargetID)
		// Off the event queue: the rechecks wait seconds between lookups.
		m.background(func() { m.recheckTitle(client, tab) })
	}
}

// dropFileChooser forgets targetID's file chooser, telling subscribers.
func (m *CDPManager) dropFileChooser(targetID string) {
	m.mu.Lock()
	_, ok := m.fileChoosers[targetID]
	delete(m.fileChoosers, targetID)
	m.mu.Unlock()
	if ok {
		m.publish(TabEvent{Kind: FileChooserClosed, Tab: TabInfo{TargetID: targetID}})
	}
}

// updateTab publishes tab when it differs from what the panes last heard,
// reporting whether its URL changed. An update with no title for the page
// the tab already shows keeps the title it had, rather than blanking it; a
// new page starts with what it reports.
func (m *CDPManager) updateTab(tab TabInfo) (navigated bool) {
	m.mu.Lock()
	last, seen := m.tabInfo[tab.TargetID]
	if seen && tab.Title == "" && tab.URL == last.URL {
		tab.Title = last.Title
	}
	news := m.isTrackedLocked(tab.TargetID) && (!seen || last != tab)
	if news {
		m.tabInfo[tab.TargetID] = tab
	}
	m.mu.Unlock()
	if news {
		m.publish(TabEvent{Kind: TabUpdated, Tab: tab})
	}
	return news && (!seen || last.URL != tab.URL)
}

// recheckTitle looks tab up again after each of titleRechecks, publishing
// what changed, until the tab closes or navigates on (that navigation runs
// its own rechecks).
func (m *CDPManager) recheckTitle(client CDPSession, tab TabInfo) {
	for _, wait := range m.titleRechecks {
		m.sleep(wait)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		tabs, err := client.ListTabs(ctx)
		cancel()
		if err != nil {
			return
		}
		i := slices.IndexFunc(tabs, func(t TabInfo) bool { return t.TargetID == tab.TargetID })
		if i < 0 || tabs[i].URL != tab.URL {
			return
		}
		m.updateTab(tabs[i])
	}
}

// tabDestroyed drops a tracked tab that closed without going through
// close_tab — window.close, a crashed renderer, the user's own Chrome — and
// moves to its neighbour when it was the active one.
func (m *CDPManager) tabDestroyed(targetID string) {
	m.mu.Lock()
	if !m.isTrackedLocked(targetID) {
		// Not in the strip, but it may still have been attached to.
		m.dropSessionLocked(targetID)
		m.mu.Unlock()
		return
	}
	next := ""
	if m.activeTargetID == targetID {
		next = m.nextTabLocked(targetID)
	}
	m.untrackTabLocked(targetID)
	m.mu.Unlock()
	m.NotifyTabRemoved(targetID)
	if next != "" {
		m.NotifyTargetSwitch(next)
	}
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
	delete(m.dialogs, targetID)
	delete(m.fileChoosers, targetID)
	delete(m.tabInfo, targetID)
	m.dropSessionLocked(targetID)
}

// dropSessionLocked lets go of the session attached to a tab that is gone:
// it holds nothing, and cancelling it ends its goroutines and listeners. Off
// the lock, since cancelling waits on Chrome. The connection's own client
// stays; Close lets it go, with the browser.
func (m *CDPManager) dropSessionLocked(targetID string) {
	if s, ok := m.sessions[targetID]; ok && s != m.client {
		m.background(s.Close)
	}
	delete(m.sessions, targetID)
}

// IsTrackedTab returns true if the target is in the tab order.
func (m *CDPManager) IsTrackedTab(targetID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.isTrackedLocked(targetID)
}

func (m *CDPManager) isTrackedLocked(targetID string) bool {
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
	return m.nextTabLocked(closedTargetID)
}

func (m *CDPManager) nextTabLocked(closedTargetID string) string {
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

// --- Tab events ---

// TabEventKind says what changed in a TabEvent.
type TabEventKind int

const (
	// TabSwitched means Tab.TargetID became the active tab.
	TabSwitched TabEventKind = iota
	// TabAdded means Tab was opened.
	TabAdded
	// TabRemoved means Tab.TargetID was closed.
	TabRemoved
	// DialogOpened means Dialog opened on Tab.TargetID and waits for an answer.
	DialogOpened
	// DialogClosed means the dialog on Tab.TargetID was answered.
	DialogClosed
	// DownloadChanged means Download started, completed or was canceled.
	DownloadChanged
	// TabUpdated means Tab navigated or changed its title.
	TabUpdated
	// FileChooserOpened means FileChooser opened on Tab.TargetID and waits
	// for files.
	FileChooserOpened
	// FileChooserClosed means the file chooser on Tab.TargetID was answered.
	FileChooserClosed
)

// dialogTypeAlert is the dialog type Chrome reports for window.alert.
const dialogTypeAlert = "alert"

// TabEvent is one change to the tabs, as Subscribe delivers it.
type TabEvent struct {
	Kind     TabEventKind
	Tab      TabInfo
	Dialog   Dialog   // set for DialogOpened
	Download Download // set for DownloadChanged
	// FileChooser is set for FileChooserOpened.
	FileChooser FileChooser
}

// tabEventBuffer is how many changes a subscriber can fall behind by. A pane
// busy restarting its screencast reads nothing for a moment, and an agent can
// open, navigate and switch tabs in that moment.
const tabEventBuffer = 64

// Subscribe returns a channel that receives every tab change from now on, in
// order, and a function that ends the subscription.
func (m *CDPManager) Subscribe() (<-chan TabEvent, func()) {
	ch := make(chan TabEvent, tabEventBuffer)
	m.mu.Lock()
	id := m.nextID
	m.nextID++
	m.subs[id] = ch
	if m.stopTitlePoll == nil {
		m.stopTitlePoll = make(chan struct{})
		go m.pollTitles(m.stopTitlePoll)
	}
	m.mu.Unlock()
	return ch, func() {
		m.mu.Lock()
		delete(m.subs, id)
		if len(m.subs) == 0 && m.stopTitlePoll != nil {
			close(m.stopTitlePoll)
			m.stopTitlePoll = nil
		}
		m.mu.Unlock()
	}
}

// pollTitles lists the tabs again every titlePollEvery until stop. Chrome
// says nothing when a page changes its title on its own — a chat counting
// unread messages, a page retitled long after it loaded — so a pane would
// otherwise keep showing the old one.
func (m *CDPManager) pollTitles(stop <-chan struct{}) {
	for {
		select {
		case <-stop:
			return
		case <-m.after(m.titlePollEvery):
		}
		m.refreshTabs()
	}
}

// refreshTabs hands every tab Chrome lists to the event queue as a change,
// in turn with the ones Chrome reports; tabs that look as they did are
// passed over there.
func (m *CDPManager) refreshTabs() {
	m.mu.Lock()
	client := m.client
	m.mu.Unlock()
	if client == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	tabs, err := client.ListTabs(ctx)
	cancel()
	if err != nil {
		m.logger.Debug("listing tabs for their titles failed", "error", err)
		return
	}
	for _, tab := range tabs {
		m.events.enqueue(func() { m.tabChanged(client, tab) })
	}
}

// publish hands ev to every subscriber. A subscriber that has fallen a whole
// buffer behind misses it rather than stalling the agent's tool call.
func (m *CDPManager) publish(ev TabEvent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, ch := range m.subs {
		select {
		case ch <- ev:
		default:
			m.logger.Warn("tab event dropped, subscriber is behind", "kind", ev.Kind, "target_id", ev.Tab.TargetID)
		}
	}
}

// NotifyTargetSwitch records targetID as the active tab and tells subscribers.
func (m *CDPManager) NotifyTargetSwitch(targetID string) {
	m.mu.Lock()
	m.activeTargetID = targetID
	m.mu.Unlock()
	m.publish(TabEvent{Kind: TabSwitched, Tab: TabInfo{TargetID: targetID}})
}

// NotifyTabAdded tells subscribers that tab was opened.
func (m *CDPManager) NotifyTabAdded(tab TabInfo) {
	m.publish(TabEvent{Kind: TabAdded, Tab: tab})
}

// NotifyTabRemoved tells subscribers that targetID was closed.
func (m *CDPManager) NotifyTabRemoved(targetID string) {
	m.publish(TabEvent{Kind: TabRemoved, Tab: TabInfo{TargetID: targetID}})
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
	m.dialogs = nil
	m.fileChoosers = nil
	m.tabInfo = map[string]TabInfo{}
	// The attached sessions hang off the connection's context and end with it.
	m.sessions = nil
}

// WSEndpoint returns the WebSocket endpoint this manager is configured for.
func (m *CDPManager) WSEndpoint() string {
	return m.wsEndpoint
}

// DiscoverExisting returns the DiscoverExisting config flag.
func (m *CDPManager) DiscoverExisting() bool {
	return m.cfg.DiscoverExisting
}

// DownloadDir returns the DownloadDir config value.
func (m *CDPManager) DownloadDir() string {
	return m.cfg.DownloadDir
}

// SetCDPFactoryForTest overrides the CDP factory for testing.
func SetCDPFactoryForTest(m *CDPManager, factory func(ctx context.Context, wsURL string, logger *slog.Logger, opts ...CDPOption) (CDPSession, error)) {
	m.cdpFactory = factory
}

// SetTimeNowForTest overrides the time function for testing.
func SetTimeNowForTest(m *CDPManager, fn func() time.Time) {
	m.timeNow = fn
}

// eventQueue runs functions one at a time, in the order they were queued, on
// a goroutine of its own that lives while there is work. Queueing never
// blocks, so chromedp's event loop, which queues, is never held up.
type eventQueue struct {
	mu      sync.Mutex
	pending []func()
	running bool
}

func (q *eventQueue) enqueue(fn func()) {
	q.mu.Lock()
	q.pending = append(q.pending, fn)
	start := !q.running
	q.running = true
	q.mu.Unlock()
	if start {
		go q.drain()
	}
}

func (q *eventQueue) drain() {
	for {
		q.mu.Lock()
		if len(q.pending) == 0 {
			q.running = false
			q.mu.Unlock()
			return
		}
		fn := q.pending[0]
		q.pending = q.pending[1:]
		q.mu.Unlock()
		fn()
	}
}
