package browser

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// mockCDPSession implements CDPSession for testing.
type mockCDPSession struct {
	mock.Mock
	watchMu        sync.Mutex
	onOpened       func(TabInfo, string)
	onChanged      func(TabInfo)
	onClosed       func(string)
	onDialog       func(Dialog)
	onDialogClosed func(string)
	onDownload     func(Download)
	onFileChooser  func(FileChooser)
	// interceptErr is what InterceptFileChoosers returns; intercepts counts
	// the calls. Every watched client makes one, so it isn't an expectation.
	interceptErr      error
	intercepts        int
	closeBrowserErr   error
	closeBrowserCalls int
	// dead makes Alive report a session whose context died with its container.
	dead bool
}

func (m *mockCDPSession) Alive() bool { return !m.dead }

// closeBrowserErr is what CloseBrowser returns; a field, since the manager
// calls it on the way down in tests that are not about the flush.
func (m *mockCDPSession) CloseBrowser(_ context.Context) error {
	m.closeBrowserCalls++
	return m.closeBrowserErr
}

func (m *mockCDPSession) TargetID() string                   { return m.Called().String(0) }
func (m *mockCDPSession) SwitchTarget(targetID string) error { return m.Called(targetID).Error(0) }
func (m *mockCDPSession) SwitchTab(ctx context.Context, targetID string) error {
	return m.Called(ctx, targetID).Error(0)
}
func (m *mockCDPSession) ListTabs(ctx context.Context) ([]TabInfo, error) {
	a := m.Called(ctx)
	t, _ := a.Get(0).([]TabInfo)
	return t, a.Error(1)
}
func (m *mockCDPSession) Favicons() map[string]string { return nil }

func (m *mockCDPSession) NewTab(ctx context.Context, url string) (string, error) {
	a := m.Called(ctx, url)
	return a.String(0), a.Error(1)
}
func (m *mockCDPSession) CloseTab(ctx context.Context, targetID string) error {
	return m.Called(ctx, targetID).Error(0)
}
func (m *mockCDPSession) Close()           { m.Called() }
func (m *mockCDPSession) ResetScreencast() { m.Called() }
func (m *mockCDPSession) StartScreencast(quality, maxWidth, maxHeight int, _ <-chan struct{}) <-chan []byte {
	a := m.Called(quality, maxWidth, maxHeight)
	ch, _ := a.Get(0).(<-chan []byte)
	return ch
}
func (m *mockCDPSession) StopScreencast() { m.Called() }
func (m *mockCDPSession) Navigate(ctx context.Context, url string) error {
	return m.Called(ctx, url).Error(0)
}
func (m *mockCDPSession) Reload(ctx context.Context) error    { return m.Called(ctx).Error(0) }
func (m *mockCDPSession) GoBack(ctx context.Context) error    { return m.Called(ctx).Error(0) }
func (m *mockCDPSession) GoForward(ctx context.Context) error { return m.Called(ctx).Error(0) }
func (m *mockCDPSession) GetPageInfo(ctx context.Context) (*PageInfo, error) {
	a := m.Called(ctx)
	p, _ := a.Get(0).(*PageInfo)
	return p, a.Error(1)
}
func (m *mockCDPSession) MouseClick(ctx context.Context, x, y float64, button string, clickCount int) error {
	return m.Called(ctx, x, y, button, clickCount).Error(0)
}
func (m *mockCDPSession) MouseMove(ctx context.Context, x, y float64, buttons int) error {
	return m.Called(ctx, x, y, buttons).Error(0)
}
func (m *mockCDPSession) MouseScroll(ctx context.Context, x, y, deltaX, deltaY float64) error {
	return m.Called(ctx, x, y, deltaX, deltaY).Error(0)
}
func (m *mockCDPSession) KeyPress(ctx context.Context, key string, modifiers int) error {
	return m.Called(ctx, key, modifiers).Error(0)
}
func (m *mockCDPSession) InsertText(ctx context.Context, text string) error {
	return m.Called(ctx, text).Error(0)
}
func (m *mockCDPSession) ReadSelection(ctx context.Context) (string, error) {
	args := m.Called(ctx)
	return args.String(0), args.Error(1)
}
func (m *mockCDPSession) TypeText(ctx context.Context, text string) error {
	return m.Called(ctx, text).Error(0)
}
func (m *mockCDPSession) EvaluateJS(ctx context.Context, expression string) (string, error) {
	a := m.Called(ctx, expression)
	return a.String(0), a.Error(1)
}
func (m *mockCDPSession) Screenshot(ctx context.Context) ([]byte, error) {
	a := m.Called(ctx)
	d, _ := a.Get(0).([]byte)
	return d, a.Error(1)
}
func (m *mockCDPSession) GetElementRefs(ctx context.Context) ([]ElementRef, error) {
	a := m.Called(ctx)
	r, _ := a.Get(0).([]ElementRef)
	return r, a.Error(1)
}
func (m *mockCDPSession) ClickRef(ctx context.Context, refs []ElementRef, refIndex int) error {
	return m.Called(ctx, refs, refIndex).Error(0)
}
func (m *mockCDPSession) EnableConsoleCapture(ctx context.Context, ch chan<- ConsoleMessage) error {
	return m.Called(ctx, ch).Error(0)
}
func (m *mockCDPSession) EnableNetworkCapture(ctx context.Context, ch chan<- NetworkRequest) error {
	return m.Called(ctx, ch).Error(0)
}
func (m *mockCDPSession) ResizeWindow(ctx context.Context, width, height int) error {
	return m.Called(ctx, width, height).Error(0)
}
func (m *mockCDPSession) ScrollIntoView(ctx context.Context, backendNodeID cdp.BackendNodeID) error {
	return m.Called(ctx, backendNodeID).Error(0)
}
func (m *mockCDPSession) MouseDown(ctx context.Context, x, y float64, button string) error {
	return m.Called(ctx, x, y, button).Error(0)
}
func (m *mockCDPSession) MouseUp(ctx context.Context, x, y float64, button string) error {
	return m.Called(ctx, x, y, button).Error(0)
}
func (m *mockCDPSession) NewContextForTarget(targetID string) (CDPSession, error) {
	a := m.Called(targetID)
	c, _ := a.Get(0).(CDPSession)
	return c, a.Error(1)
}
func (m *mockCDPSession) SetCookies(ctx context.Context, cookies []Cookie) error {
	return m.Called(ctx, cookies).Error(0)
}

func (m *mockCDPSession) SetDownloadDir(ctx context.Context, dir string) error {
	return m.Called(ctx, dir).Error(0)
}

// WatchTargets keeps the handlers so a test can play Chrome opening and
// closing tabs; a field rather than an expectation, as every Connect calls it.
func (m *mockCDPSession) WatchTargets(onOpened func(tab TabInfo, openerID string), onChanged func(tab TabInfo), onClosed func(targetID string)) {
	m.watchMu.Lock()
	defer m.watchMu.Unlock()
	m.onOpened, m.onChanged, m.onClosed = onOpened, onChanged, onClosed
}

// changeWatcher returns the handler WatchTargets was given for tab changes.
func (m *mockCDPSession) changeWatcher() func(TabInfo) {
	m.watchMu.Lock()
	defer m.watchMu.Unlock()
	return m.onChanged
}

// WatchDialogs keeps the handlers so a test can play a page opening and
// closing dialogs.
func (m *mockCDPSession) WatchDialogs(onOpened func(Dialog), onClosed func(string)) {
	m.watchMu.Lock()
	defer m.watchMu.Unlock()
	m.onDialog, m.onDialogClosed = onOpened, onClosed
}

// WatchFileChoosers keeps the handler so a test can play a page opening a
// file chooser.
func (m *mockCDPSession) WatchFileChoosers(onOpened func(FileChooser)) {
	m.watchMu.Lock()
	defer m.watchMu.Unlock()
	m.onFileChooser = onOpened
}

func (m *mockCDPSession) fileChooserWatcher() func(FileChooser) {
	m.watchMu.Lock()
	defer m.watchMu.Unlock()
	return m.onFileChooser
}

func (m *mockCDPSession) InterceptFileChoosers(context.Context) error {
	m.watchMu.Lock()
	defer m.watchMu.Unlock()
	m.intercepts++
	return m.interceptErr
}

func (m *mockCDPSession) interceptCount() int {
	m.watchMu.Lock()
	defer m.watchMu.Unlock()
	return m.intercepts
}

func (m *mockCDPSession) SetChosenFiles(ctx context.Context, nodeID cdp.BackendNodeID, files []ChosenFile) error {
	return m.Called(ctx, nodeID, files).Error(0)
}

// WatchDownloads keeps the handler so a test can play Chrome saving a file.
func (m *mockCDPSession) WatchDownloads(onChange func(Download)) {
	m.watchMu.Lock()
	defer m.watchMu.Unlock()
	m.onDownload = onChange
}

// downloadWatcher returns the handler WatchDownloads was given.
func (m *mockCDPSession) downloadWatcher() func(Download) {
	m.watchMu.Lock()
	defer m.watchMu.Unlock()
	return m.onDownload
}

// dialogWatchers returns the handlers WatchDialogs was given.
func (m *mockCDPSession) dialogWatchers() (func(Dialog), func(string)) {
	m.watchMu.Lock()
	defer m.watchMu.Unlock()
	return m.onDialog, m.onDialogClosed
}

func (m *mockCDPSession) HandleDialog(ctx context.Context, accept bool, promptText string) error {
	return m.Called(ctx, accept, promptText).Error(0)
}

// watchers returns the handlers WatchTargets was given.
func (m *mockCDPSession) watchers() (func(TabInfo, string), func(string)) {
	m.watchMu.Lock()
	defer m.watchMu.Unlock()
	return m.onOpened, m.onClosed
}

type CDPManagerSuite struct {
	suite.Suite
}

func TestCDPManagerSuite(t *testing.T) {
	suite.Run(t, new(CDPManagerSuite))
}

func (s *CDPManagerSuite) newTestManager(discoverExisting bool) (*CDPManager, *mockCDPSession) {
	mockClient := new(mockCDPSession)
	mgr := NewCDPManager("ws://test:9222", CDPManagerConfig{
		DiscoverExisting: discoverExisting,
		MaxRetries:       1,
		RetryDelay:       time.Millisecond,
	}, slog.Default())
	mgr.cdpFactory = func(_ context.Context, _ string, _ *slog.Logger, _ ...CDPOption) (CDPSession, error) {
		return mockClient, nil
	}
	mgr.timeNow = func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	mgr.background = func(fn func()) { fn() }
	// The title poll waits for good unless a test ticks it.
	mgr.after = func(time.Duration) <-chan time.Time { return nil }
	return mgr, mockClient
}

func (s *CDPManagerSuite) TestNewCDPManager() {
	mgr := NewCDPManager("ws://test:9222", CDPManagerConfig{
		DiscoverExisting: true,
		MaxRetries:       5,
		RetryDelay:       time.Second,
		DownloadDir:      "/downloads",
	}, slog.Default())
	require.NotNil(s.T(), mgr)
	require.Equal(s.T(), "ws://test:9222", mgr.WSEndpoint())
	require.True(s.T(), mgr.DiscoverExisting())
	require.Equal(s.T(), "/downloads", mgr.DownloadDir())
	require.False(s.T(), mgr.IsConnected())
	require.Nil(s.T(), mgr.ActiveClient())
}

func (s *CDPManagerSuite) TestConnectSuccess() {
	mgr, mockClient := s.newTestManager(false)
	mockClient.On("TargetID").Return("t1")
	mockClient.On("Close").Return().Maybe()

	err := mgr.Connect(context.Background())
	require.NoError(s.T(), err)
	require.True(s.T(), mgr.IsConnected())
	require.NotNil(s.T(), mgr.ActiveClient())
	require.Equal(s.T(), "t1", mgr.ActiveTargetID())
	require.True(s.T(), mgr.IsTrackedTab("t1"))
}

func (s *CDPManagerSuite) TestConnectWithDiscoverExisting() {
	// Docker mode: only our connected tab is tracked, other existing tabs are ignored.
	mgr, mockClient := s.newTestManager(true)
	mockClient.On("TargetID").Return("t-auto")
	mockClient.On("SwitchTarget", "t-auto").Return(nil)
	mockClient.On("Close").Return().Maybe()

	err := mgr.Connect(context.Background())
	require.NoError(s.T(), err)
	require.True(s.T(), mgr.IsTrackedTab("t-auto"))
	require.Equal(s.T(), "t-auto", mgr.ActiveTargetID())
	// The tab is brought to the foreground, or Chrome would never acknowledge a
	// wheel event dispatched to it.
	mockClient.AssertCalled(s.T(), "SwitchTarget", "t-auto")
}

func (s *CDPManagerSuite) TestConnectActivationFailureIsNotFatal() {
	mgr, mockClient := s.newTestManager(true)
	mockClient.On("TargetID").Return("t-auto")
	mockClient.On("SwitchTarget", "t-auto").Return(errors.New("activate failed"))
	mockClient.On("Close").Return().Maybe()

	require.NoError(s.T(), mgr.Connect(context.Background()))
	require.True(s.T(), mgr.IsConnected())
}

func (s *CDPManagerSuite) TestConnectSetsDownloadDir() {
	tests := []struct {
		name string
		err  error
	}{
		{name: "set"},
		// A browser that refuses downloads still browses.
		{name: "failure is not fatal", err: errors.New("cdp gone")},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			mgr, mockClient := s.newTestManager(false)
			mgr.cfg.DownloadDir = "/downloads"
			mockClient.On("TargetID").Return("t1")
			mockClient.On("SetDownloadDir", mock.Anything, "/downloads").Return(tt.err).Once()

			require.NoError(s.T(), mgr.Connect(context.Background()))
			require.True(s.T(), mgr.IsConnected())
			mockClient.AssertExpectations(s.T())
			// Downloads are only watched once Chrome agreed to save them.
			require.Equal(s.T(), tt.err == nil, mockClient.downloadWatcher() != nil)
		})
	}
}

func (s *CDPManagerSuite) TestDownloadsArePublished() {
	mgr, mockClient := s.newTestManager(false)
	mgr.cfg.DownloadDir = "/downloads"
	mockClient.On("TargetID").Return("t1")
	mockClient.On("SetDownloadDir", mock.Anything, "/downloads").Return(nil)
	require.NoError(s.T(), mgr.Connect(context.Background()))

	events, unsubscribe := mgr.Subscribe()
	defer unsubscribe()
	d := Download{GUID: "g1", Filename: "r.pdf", State: DownloadCompleted}
	mockClient.downloadWatcher()(d)
	select {
	case ev := <-events:
		require.Equal(s.T(), TabEvent{Kind: DownloadChanged, Download: d}, ev)
	case <-time.After(time.Second):
		s.T().Fatal("no download event")
	}
}

func (s *CDPManagerSuite) TestConnectFailure() {
	mgr := NewCDPManager("ws://test:9222", CDPManagerConfig{
		MaxRetries: 1,
		RetryDelay: time.Millisecond,
	}, slog.Default())
	mgr.cdpFactory = func(_ context.Context, _ string, _ *slog.Logger, _ ...CDPOption) (CDPSession, error) {
		return nil, errors.New("connect failed")
	}

	err := mgr.Connect(context.Background())
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "connecting CDP after 1 attempts")
	require.False(s.T(), mgr.IsConnected())
}

func (s *CDPManagerSuite) TestConnectRetrySuccess() {
	attempt := 0
	mockClient := new(mockCDPSession)
	mockClient.On("TargetID").Return("t1")

	mgr := NewCDPManager("ws://test:9222", CDPManagerConfig{
		MaxRetries: 3,
		RetryDelay: time.Millisecond,
	}, slog.Default())
	mgr.cdpFactory = func(_ context.Context, _ string, _ *slog.Logger, _ ...CDPOption) (CDPSession, error) {
		attempt++
		if attempt < 3 {
			return nil, errors.New("not ready")
		}
		return mockClient, nil
	}

	err := mgr.Connect(context.Background())
	require.NoError(s.T(), err)
	require.Equal(s.T(), 3, attempt)
}

func (s *CDPManagerSuite) TestConnectRetryContextCancel() {
	ctx, cancel := context.WithCancel(context.Background())
	mgr := NewCDPManager("ws://test:9222", CDPManagerConfig{
		MaxRetries: 5,
		RetryDelay: time.Second,
	}, slog.Default())
	mgr.cdpFactory = func(_ context.Context, _ string, _ *slog.Logger, _ ...CDPOption) (CDPSession, error) {
		cancel()
		return nil, errors.New("not ready")
	}

	err := mgr.Connect(ctx)
	require.Error(s.T(), err)
}

func (s *CDPManagerSuite) TestSwitchActive() {
	mgr, mockClient := s.newTestManager(false)
	mockClient.On("TargetID").Return("t1")

	err := mgr.Connect(context.Background())
	require.NoError(s.T(), err)

	mgr.SwitchActive("t2")
	require.Equal(s.T(), "t2", mgr.ActiveTargetID())
}

func (s *CDPManagerSuite) TestSetAndRemoveClientForTarget() {
	mgr, _ := s.newTestManager(false)
	client := new(mockCDPSession)

	mgr.SetClientForTarget("t5", client)
	mgr.SwitchActive("t5")
	require.Equal(s.T(), client, mgr.ActiveClient())

	removed := mgr.RemoveClientForTarget("t5")
	require.Equal(s.T(), client, removed)
	require.Nil(s.T(), mgr.ActiveClient())
}

func (s *CDPManagerSuite) TestRemoveClientForTargetNotFound() {
	mgr, _ := s.newTestManager(false)
	require.Nil(s.T(), mgr.RemoveClientForTarget("nonexistent"))
}

func (s *CDPManagerSuite) TestPaneConnectedDisconnected() {
	mgr, _ := s.newTestManager(false)

	require.Equal(s.T(), 0, mgr.PaneCount())

	mgr.PaneConnected()
	require.Equal(s.T(), 1, mgr.PaneCount())

	mgr.PaneDisconnected()
	require.Equal(s.T(), 0, mgr.PaneCount())

	// Should not go below 0.
	mgr.PaneDisconnected()
	require.Equal(s.T(), 0, mgr.PaneCount())
}

func (s *CDPManagerSuite) TestTouch() {
	mgr, _ := s.newTestManager(false)
	newTime := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	mgr.timeNow = func() time.Time { return newTime }

	mgr.Touch()
	require.Equal(s.T(), newTime, mgr.LastUsedAt())
}

func (s *CDPManagerSuite) TestTrackTab() {
	mgr, _ := s.newTestManager(false)

	mgr.TrackTab("t1")
	mgr.TrackTab("t2")
	mgr.TrackTab("t1") // duplicate
	require.True(s.T(), mgr.IsTrackedTab("t1"))
	require.True(s.T(), mgr.IsTrackedTab("t2"))
	require.False(s.T(), mgr.IsTrackedTab("t3"))
}

func (s *CDPManagerSuite) TestUntrackTab() {
	mgr, _ := s.newTestManager(false)

	mgr.TrackTab("t1")
	mgr.TrackTab("t2")
	mgr.TrackTab("t3")

	mgr.UntrackTab("t2")
	require.True(s.T(), mgr.IsTrackedTab("t1"))
	require.False(s.T(), mgr.IsTrackedTab("t2"))
	require.True(s.T(), mgr.IsTrackedTab("t3"))
}

func (s *CDPManagerSuite) TestNextTabIDMiddle() {
	mgr, _ := s.newTestManager(false)
	mgr.TrackTab("t1")
	mgr.TrackTab("t2")
	mgr.TrackTab("t3")

	require.Equal(s.T(), "t1", mgr.NextTabID("t2"))
}

func (s *CDPManagerSuite) TestNextTabIDFirst() {
	mgr, _ := s.newTestManager(false)
	mgr.TrackTab("t1")
	mgr.TrackTab("t2")
	mgr.TrackTab("t3")

	require.Equal(s.T(), "t2", mgr.NextTabID("t1"))
}

func (s *CDPManagerSuite) TestNextTabIDOnly() {
	mgr, _ := s.newTestManager(false)
	mgr.TrackTab("t1")

	require.Equal(s.T(), "", mgr.NextTabID("t1"))
}

func (s *CDPManagerSuite) TestNextTabIDNotFound() {
	mgr, _ := s.newTestManager(false)
	mgr.TrackTab("t1")

	require.Equal(s.T(), "", mgr.NextTabID("t-unknown"))
}

func (s *CDPManagerSuite) TestOrderTabs() {
	mgr, _ := s.newTestManager(false)
	mgr.TrackTab("t2")
	mgr.TrackTab("t1")

	tabs := []TabInfo{
		{TargetID: "t1", Title: "A"},
		{TargetID: "t2", Title: "B"},
	}
	result := mgr.OrderTabs(tabs)
	require.Len(s.T(), result, 2)
	require.Equal(s.T(), "t2", result[0].TargetID)
	require.Equal(s.T(), "t1", result[1].TargetID)
}

func (s *CDPManagerSuite) TestOrderTabsUntrackedAppended() {
	mgr, _ := s.newTestManager(false)
	mgr.TrackTab("t1")

	tabs := []TabInfo{
		{TargetID: "t1", Title: "A"},
		{TargetID: "t3", Title: "C"},
	}
	result := mgr.OrderTabs(tabs)
	require.Len(s.T(), result, 2)
	require.Equal(s.T(), "t1", result[0].TargetID)
	require.Equal(s.T(), "t3", result[1].TargetID)
}

func (s *CDPManagerSuite) TestOrderTabsEmpty() {
	mgr, _ := s.newTestManager(false)
	tabs := []TabInfo{{TargetID: "t1"}}
	result := mgr.OrderTabs(tabs)
	require.Equal(s.T(), tabs, result)
}

// drain reads the events already queued on ch.
func drain(ch <-chan TabEvent) []TabEvent {
	var got []TabEvent
	for {
		select {
		case ev := <-ch:
			got = append(got, ev)
		default:
			return got
		}
	}
}

func (s *CDPManagerSuite) TestNotifyReachesEverySubscriberInOrder() {
	mgr, _ := s.newTestManager(false)
	first, _ := mgr.Subscribe()
	second, _ := mgr.Subscribe()

	mgr.NotifyTabAdded(TabInfo{TargetID: "t2", URL: "https://a.com"})
	mgr.NotifyTargetSwitch("t2")
	mgr.NotifyTabRemoved("t1")

	want := []TabEvent{
		{Kind: TabAdded, Tab: TabInfo{TargetID: "t2", URL: "https://a.com"}},
		{Kind: TabSwitched, Tab: TabInfo{TargetID: "t2"}},
		{Kind: TabRemoved, Tab: TabInfo{TargetID: "t1"}},
	}
	require.Equal(s.T(), want, drain(first))
	require.Equal(s.T(), want, drain(second), "a second pane must see the same changes, not race the first for them")
	require.Equal(s.T(), "t2", mgr.ActiveTargetID())
}

func (s *CDPManagerSuite) TestUnsubscribeStopsDelivery() {
	mgr, _ := s.newTestManager(false)
	ch, unsubscribe := mgr.Subscribe()
	unsubscribe()

	mgr.NotifyTargetSwitch("t1")
	require.Empty(s.T(), drain(ch))
}

func (s *CDPManagerSuite) TestNotifyDoesNotBlockOnASubscriberThatIsBehind() {
	mgr, _ := s.newTestManager(false)
	ch, _ := mgr.Subscribe()

	for i := range tabEventBuffer + 1 {
		mgr.NotifyTabRemoved(fmt.Sprintf("t%d", i))
	}
	require.Len(s.T(), drain(ch), tabEventBuffer)
}

func (s *CDPManagerSuite) TestConnectFollowsATabThePageOpens() {
	mgr, mockClient := s.newTestManager(false)
	mockClient.On("TargetID").Return("t1")
	require.NoError(s.T(), mgr.Connect(context.Background()))
	events, _ := mgr.Subscribe()

	onOpened, _ := mockClient.watchers()
	require.NotNil(s.T(), onOpened, "Connect must watch for tabs Chrome opens")
	onOpened(TabInfo{TargetID: "popup", URL: "https://b.com"}, "t1")

	var got []TabEvent
	require.Eventually(s.T(), func() bool {
		got = append(got, drain(events)...)
		return len(got) == 2
	}, time.Second, 5*time.Millisecond)
	require.Equal(s.T(), []TabEvent{
		{Kind: TabAdded, Tab: TabInfo{TargetID: "popup", URL: "https://b.com"}},
		{Kind: TabSwitched, Tab: TabInfo{TargetID: "popup"}},
	}, got)
	require.True(s.T(), mgr.IsTrackedTab("popup"))
	require.Equal(s.T(), "popup", mgr.ActiveTargetID())
}

func (s *CDPManagerSuite) TestTabOpenedIgnoresTabsItDoesNotOwn() {
	tests := []struct {
		name   string
		tab    string
		opener string
	}{
		{"opened by nobody, as our own new_tab is", "t9", ""},
		{"opened by a tab the strip does not show", "t9", "untracked"},
		{"already in the strip", "t2", "t1"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			mgr, _ := s.newTestManager(false)
			mgr.TrackTab("t1")
			mgr.TrackTab("t2")
			events, _ := mgr.Subscribe()

			mgr.tabOpened(TabInfo{TargetID: tt.tab}, tt.opener)

			require.Empty(s.T(), drain(events))
			require.Equal(s.T(), tt.tab == "t2", mgr.IsTrackedTab(tt.tab))
		})
	}
}

func (s *CDPManagerSuite) TestTabDestroyed() {
	tests := []struct {
		name   string
		tabs   []string
		active string
		closed string
		want   []TabEvent
	}{
		{
			name: "the active tab moves to its neighbour", tabs: []string{"t1", "t2"}, active: "t2", closed: "t2",
			want: []TabEvent{{Kind: TabRemoved, Tab: TabInfo{TargetID: "t2"}}, {Kind: TabSwitched, Tab: TabInfo{TargetID: "t1"}}},
		},
		{
			name: "a background tab only leaves the strip", tabs: []string{"t1", "t2"}, active: "t1", closed: "t2",
			want: []TabEvent{{Kind: TabRemoved, Tab: TabInfo{TargetID: "t2"}}},
		},
		{
			name: "the last tab has nowhere to move", tabs: []string{"t1"}, active: "t1", closed: "t1",
			want: []TabEvent{{Kind: TabRemoved, Tab: TabInfo{TargetID: "t1"}}},
		},
		{
			name: "a tab the strip never showed is ignored", tabs: []string{"t1"}, active: "t1", closed: "worker",
		},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			mgr, _ := s.newTestManager(false)
			for _, id := range tt.tabs {
				mgr.TrackTab(id)
			}
			mgr.SwitchActive(tt.active)
			events, _ := mgr.Subscribe()

			mgr.tabDestroyed(tt.closed)

			require.Equal(s.T(), tt.want, drain(events))
			require.False(s.T(), mgr.IsTrackedTab(tt.closed))
		})
	}
}

func (s *CDPManagerSuite) TestTabClosedByChromeReachesSubscribers() {
	mgr, mockClient := s.newTestManager(false)
	mockClient.On("TargetID").Return("t1")
	require.NoError(s.T(), mgr.Connect(context.Background()))
	events, _ := mgr.Subscribe()

	_, onClosed := mockClient.watchers()
	onClosed("t1")

	// The tab leaves the strip before the event goes out, so wait on the event.
	var got []TabEvent
	require.Eventually(s.T(), func() bool {
		got = append(got, drain(events)...)
		return len(got) == 1
	}, time.Second, 5*time.Millisecond)
	require.Equal(s.T(), []TabEvent{{Kind: TabRemoved, Tab: TabInfo{TargetID: "t1"}}}, got)
	require.False(s.T(), mgr.IsTrackedTab("t1"))
}

func (s *CDPManagerSuite) TestTabChanged() {
	mgr, mockClient := s.newTestManager(false)
	mgr.titleRechecks = []time.Duration{0}
	mgr.sleep = func(time.Duration) {}
	mockClient.On("ListTabs", mock.Anything).Return(nil, errors.New("gone"))
	mgr.TrackTab("t1")
	events, _ := mgr.Subscribe()

	a := TabInfo{TargetID: "t1", URL: "https://a.com", Title: "A"}
	mgr.tabChanged(mockClient, a)
	mgr.tabChanged(mockClient, a) // every attached client hears it: published once
	mgr.tabChanged(mockClient, TabInfo{TargetID: "worker", URL: "https://w.com"})
	b := TabInfo{TargetID: "t1", URL: "https://a.com", Title: "B"}
	mgr.tabChanged(mockClient, b) // a new title alone needs no recheck

	require.Equal(s.T(), []TabEvent{{Kind: TabUpdated, Tab: a}, {Kind: TabUpdated, Tab: b}}, drain(events))
	mockClient.AssertNumberOfCalls(s.T(), "ListTabs", 1)

	// A tab that closes and comes back under the same id is news again.
	mgr.tabDestroyed("t1")
	mgr.TrackTab("t1")
	drain(events)
	mgr.tabChanged(mockClient, b)
	require.Equal(s.T(), []TabEvent{{Kind: TabUpdated, Tab: b}}, drain(events))
	mockClient.AssertNumberOfCalls(s.T(), "ListTabs", 2)
}

func (s *CDPManagerSuite) TestTabChangedWithoutATitleKeepsTheOneItHad() {
	mgr, mockClient := s.newTestManager(false)
	mgr.titleRechecks = nil
	mgr.TrackTab("t1")
	events, _ := mgr.Subscribe()

	a := TabInfo{TargetID: "t1", URL: "https://a.com", Title: "A"}
	mgr.tabChanged(mockClient, a)
	// Same page, no title: nothing new to tell.
	mgr.tabChanged(mockClient, TabInfo{TargetID: "t1", URL: "https://a.com"})
	// A new page with no title yet starts blank.
	moved := TabInfo{TargetID: "t1", URL: "https://b.com"}
	mgr.tabChanged(mockClient, moved)
	require.Equal(s.T(), []TabEvent{{Kind: TabUpdated, Tab: a}, {Kind: TabUpdated, Tab: moved}}, drain(events))
}

func (s *CDPManagerSuite) TestTabChangedRechecksTheTitle() {
	nav := TabInfo{TargetID: "t1", URL: "https://a.com/", Title: "a.com/"}
	titled := TabInfo{TargetID: "t1", URL: "https://a.com/", Title: "A"}
	tests := []struct {
		name    string
		lookups [][]TabInfo
		err     error
		want    []TabEvent
		calls   int
	}{
		{
			name:    "publishes the title the page sets, once",
			lookups: [][]TabInfo{{nav}, {titled, {TargetID: "t2"}}, {titled}},
			want:    []TabEvent{{Kind: TabUpdated, Tab: nav}, {Kind: TabUpdated, Tab: titled}},
			calls:   3,
		},
		{
			name:    "stops when the tab navigates on",
			lookups: [][]TabInfo{{{TargetID: "t1", URL: "https://b.com/", Title: "B"}}},
			want:    []TabEvent{{Kind: TabUpdated, Tab: nav}},
			calls:   1,
		},
		{
			name:    "stops when the tab is gone",
			lookups: [][]TabInfo{{}},
			want:    []TabEvent{{Kind: TabUpdated, Tab: nav}},
			calls:   1,
		},
		{
			name:  "stops when Chrome can't say",
			err:   errors.New("closed"),
			want:  []TabEvent{{Kind: TabUpdated, Tab: nav}},
			calls: 1,
		},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			mgr, mockClient := s.newTestManager(false)
			mgr.titleRechecks = []time.Duration{time.Millisecond, time.Second, time.Minute}
			var slept []time.Duration
			mgr.sleep = func(d time.Duration) { slept = append(slept, d) }
			if tt.err != nil {
				mockClient.On("ListTabs", mock.Anything).Return(nil, tt.err)
			}
			for _, tabs := range tt.lookups {
				mockClient.On("ListTabs", mock.Anything).Return(tabs, nil).Once()
			}
			mgr.TrackTab("t1")
			events, _ := mgr.Subscribe()

			mgr.tabChanged(mockClient, nav)

			require.Equal(s.T(), tt.want, drain(events))
			mockClient.AssertNumberOfCalls(s.T(), "ListTabs", tt.calls)
			require.Equal(s.T(), mgr.titleRechecks[:tt.calls], slept)
		})
	}
}

func (s *CDPManagerSuite) TestTabChangedByChromeReachesSubscribers() {
	mgr, mockClient := s.newTestManager(false)
	mockClient.On("TargetID").Return("t1")
	require.NoError(s.T(), mgr.Connect(context.Background()))
	mgr.titleRechecks = nil
	events, _ := mgr.Subscribe()

	onChanged := mockClient.changeWatcher()
	require.NotNil(s.T(), onChanged, "Connect must watch for tabs navigating")
	onChanged(TabInfo{TargetID: "t1", URL: "https://b.com", Title: "B"})

	var got []TabEvent
	require.Eventually(s.T(), func() bool {
		got = append(got, drain(events)...)
		return len(got) == 1
	}, time.Second, 5*time.Millisecond)
	require.Equal(s.T(), []TabEvent{{Kind: TabUpdated, Tab: TabInfo{TargetID: "t1", URL: "https://b.com", Title: "B"}}}, got)
}

func (s *CDPManagerSuite) TestClose() {
	mgr, mockClient := s.newTestManager(false)
	mockClient.On("TargetID").Return("t1")
	mockClient.On("Close").Return()

	err := mgr.Connect(context.Background())
	require.NoError(s.T(), err)

	mgr.Close()
	require.False(s.T(), mgr.IsConnected())
	require.Nil(s.T(), mgr.ActiveClient())
	require.Equal(s.T(), "", mgr.ActiveTargetID())
	mockClient.AssertCalled(s.T(), "Close")
}

func (s *CDPManagerSuite) TestCloseShutsChromeDownInDockerMode() {
	mgr, mockClient := s.newTestManager(true)
	mockClient.On("TargetID").Return("t1")
	mockClient.On("SwitchTarget", "t1").Return(nil)
	mockClient.On("Close").Return()

	require.NoError(s.T(), mgr.Connect(context.Background()))
	mgr.Close()

	// Chrome only writes the profile out at a clean shutdown, so closing the
	// manager has to ask for one before the container is stopped.
	require.Equal(s.T(), 1, mockClient.closeBrowserCalls)
}

func (s *CDPManagerSuite) TestCloseSurvivesShutdownFailure() {
	mgr, mockClient := s.newTestManager(true)
	mockClient.closeBrowserErr = errors.New("already gone")
	mockClient.On("TargetID").Return("t1")
	mockClient.On("SwitchTarget", "t1").Return(nil)
	mockClient.On("Close").Return()

	require.NoError(s.T(), mgr.Connect(context.Background()))
	mgr.Close()
	require.False(s.T(), mgr.IsConnected())
}

func (s *CDPManagerSuite) TestCloseLeavesHostChromeRunning() {
	mgr, mockClient := s.newTestManager(false)
	mockClient.On("TargetID").Return("t1")
	mockClient.On("Close").Return()

	require.NoError(s.T(), mgr.Connect(context.Background()))
	mgr.Close()

	// Host mode drives the user's own browser; closing it is not ours to do.
	require.Equal(s.T(), 0, mockClient.closeBrowserCalls)
}

func (s *CDPManagerSuite) TestCloseSkipsShutdownForDeadClient() {
	mgr, mockClient := s.newTestManager(true)
	mockClient.On("TargetID").Return("t1")
	mockClient.On("SwitchTarget", "t1").Return(nil)
	mockClient.On("Close").Return()

	require.NoError(s.T(), mgr.Connect(context.Background()))
	// The container is already gone — there is nothing left to ask.
	mockClient.dead = true
	mgr.Close()

	require.Equal(s.T(), 0, mockClient.closeBrowserCalls)
}

func (s *CDPManagerSuite) TestGetOrCreateAlwaysCreatesFresh() {
	mgr, mockClient := s.newTestManager(false)
	mockClient.On("TargetID").Return("t-initial")
	mockClient.On("Close").Return().Maybe()

	// Connect to set up the initial client.
	require.NoError(s.T(), mgr.Connect(context.Background()))

	// GetOrCreate creates a new context from the initial client.
	newMock := new(mockCDPSession)
	mockClient.On("ListTabs", mock.Anything).Return([]TabInfo{{TargetID: "t-other"}}, nil)
	mockClient.On("NewContextForTarget", "t-other").Return(newMock, nil)

	got, err := mgr.GetOrCreate(context.Background(), "t-other")
	require.NoError(s.T(), err)
	require.Equal(s.T(), newMock, got)
	// activeClient is updated.
	require.Equal(s.T(), newMock, mgr.ActiveClient())
}

func (s *CDPManagerSuite) TestConnectEmptyTargetID() {
	mgr := NewCDPManager("ws://test:9222", CDPManagerConfig{
		MaxRetries: 1,
		RetryDelay: time.Millisecond,
	}, slog.Default())
	mockClient := new(mockCDPSession)
	mockClient.On("TargetID").Return("")
	mgr.cdpFactory = func(_ context.Context, _ string, _ *slog.Logger, _ ...CDPOption) (CDPSession, error) {
		return mockClient, nil
	}

	err := mgr.Connect(context.Background())
	require.NoError(s.T(), err)
	require.True(s.T(), mgr.IsConnected())
	require.Equal(s.T(), "", mgr.ActiveTargetID())
}

func (s *CDPManagerSuite) TestDefaultCDPFactory() {
	// Just verify it doesn't panic — actual CDP connection would fail.
	_, err := defaultCDPFactory(context.Background(), "ws://127.0.0.1:19999", slog.Default())
	require.Error(s.T(), err) // expected: no Chrome running
}

func (s *CDPManagerSuite) TestGetOrCreateReusesAllocator() {
	// When an existing client is in cdpTargets, GetOrCreate should call
	// NewContextForTarget instead of the factory (reuses browser WS connection).
	existingMock := new(mockCDPSession)
	newMock := new(mockCDPSession)
	newMock.On("TargetID").Return("t-new").Maybe()

	existingMock.On("ListTabs", mock.Anything).Return([]TabInfo{{TargetID: "t-new"}}, nil)
	existingMock.On("NewContextForTarget", "t-new").Return(newMock, nil)

	mgr := NewCDPManager("ws://test:9222", CDPManagerConfig{
		MaxRetries: 1,
		RetryDelay: time.Millisecond,
	}, slog.Default())
	mgr.SetClientForTarget("t-existing", existingMock)

	got, err := mgr.GetOrCreate(context.Background(), "t-new")
	require.NoError(s.T(), err)
	require.Equal(s.T(), newMock, got)
	existingMock.AssertCalled(s.T(), "NewContextForTarget", "t-new")
	onOpened, onClosed := newMock.watchers()
	require.NotNil(s.T(), onOpened, "an attached tab must be watched too: the first one may close")
	require.NotNil(s.T(), onClosed)
}

func (s *CDPManagerSuite) TestGetOrCreateAttachError() {
	existingMock := new(mockCDPSession)
	existingMock.On("ListTabs", mock.Anything).Return([]TabInfo{{TargetID: "t-new"}}, nil)
	existingMock.On("NewContextForTarget", "t-new").Return(nil, errors.New("attach failed"))

	mgr := NewCDPManager("ws://test:9222", CDPManagerConfig{}, slog.Default())
	mgr.SetClientForTarget("t-existing", existingMock)

	_, err := mgr.GetOrCreate(context.Background(), "t-new")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "attaching to target t-new")
}

func (s *CDPManagerSuite) TestGetOrCreateNoExistingClient() {
	mgr := NewCDPManager("ws://test:9222", CDPManagerConfig{}, slog.Default())

	_, err := mgr.GetOrCreate(context.Background(), "t-new")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "call Connect first")
}

func (s *CDPManagerSuite) TestSetCDPFactoryForTest() {
	mgr := NewCDPManager("ws://test:9222", CDPManagerConfig{}, slog.Default())
	called := false
	SetCDPFactoryForTest(mgr, func(_ context.Context, _ string, _ *slog.Logger, _ ...CDPOption) (CDPSession, error) {
		called = true
		return nil, nil
	})
	_, _ = mgr.cdpFactory(context.Background(), "", slog.Default())
	require.True(s.T(), called)
}

func (s *CDPManagerSuite) TestSetTimeNowForTest() {
	mgr := NewCDPManager("ws://test:9222", CDPManagerConfig{}, slog.Default())
	fixed := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
	SetTimeNowForTest(mgr, func() time.Time { return fixed })
	require.Equal(s.T(), fixed, mgr.timeNow())
}

// ensureLiveManager wires a manager whose active client is attached to
// "t-gone", the tab the tests then make disappear.
func (s *CDPManagerSuite) ensureLiveManager() (*CDPManager, *mockCDPSession) {
	mgr, mockClient := s.newTestManager(false)
	mgr.SetClientForTarget("t-gone", mockClient)
	return mgr, mockClient
}

func (s *CDPManagerSuite) TestEnsureLiveTargetWithoutClient() {
	mgr, _ := s.newTestManager(false)

	client, err := mgr.EnsureLiveTarget(context.Background())
	s.Require().NoError(err)
	s.Nil(client)
}

// A client with no active target has no page to have lost, and listing tabs
// to discover that would be a round trip on every action.
func (s *CDPManagerSuite) TestEnsureLiveTargetWithoutActiveTarget() {
	mgr, mockClient := s.newTestManager(false)
	mgr.SetClientForTarget("", mockClient)

	client, err := mgr.EnsureLiveTarget(context.Background())
	s.Require().NoError(err)
	s.Equal(mockClient, client)
	mockClient.AssertNotCalled(s.T(), "ListTabs", mock.Anything)
}

func (s *CDPManagerSuite) TestEnsureLiveTargetStillThere() {
	mgr, mockClient := s.ensureLiveManager()
	mockClient.On("ListTabs", mock.Anything).Return([]TabInfo{{TargetID: "t-other"}, {TargetID: "t-gone"}}, nil)

	client, err := mgr.EnsureLiveTarget(context.Background())
	s.Require().NoError(err)
	s.Equal(mockClient, client)
	s.Equal("t-gone", mgr.ActiveTargetID())
	mockClient.AssertNotCalled(s.T(), "NewContextForTarget", mock.Anything)
}

// A listing that fails says nothing about the target, so the client stands.
func (s *CDPManagerSuite) TestEnsureLiveTargetListError() {
	mgr, mockClient := s.ensureLiveManager()
	mockClient.On("ListTabs", mock.Anything).Return(nil, errors.New("no connection"))

	client, err := mgr.EnsureLiveTarget(context.Background())
	s.Require().NoError(err)
	s.Equal(mockClient, client)
}

func (s *CDPManagerSuite) TestEnsureLiveTargetReattaches() {
	mgr, mockClient := s.ensureLiveManager()
	fresh := new(mockCDPSession)
	mockClient.On("ListTabs", mock.Anything).Return([]TabInfo{{TargetID: "t-live"}}, nil)
	mockClient.On("NewContextForTarget", "t-live").Return(fresh, nil)

	client, err := mgr.EnsureLiveTarget(context.Background())
	s.Require().NoError(err)
	s.Equal(fresh, client)
	s.Equal("t-live", mgr.ActiveTargetID())
	s.Contains(mgr.tabOrder, "t-live")
}

// Chrome with every tab closed is still a usable browser; it just needs one.
func (s *CDPManagerSuite) TestEnsureLiveTargetOpensATab() {
	mgr, mockClient := s.ensureLiveManager()
	fresh := new(mockCDPSession)
	mockClient.On("ListTabs", mock.Anything).Return([]TabInfo{}, nil)
	mockClient.On("NewTab", mock.Anything, "about:blank").Return("t-new", nil)
	mockClient.On("NewContextForTarget", "t-new").Return(fresh, nil)

	client, err := mgr.EnsureLiveTarget(context.Background())
	s.Require().NoError(err)
	s.Equal(fresh, client)
	s.Equal("t-new", mgr.ActiveTargetID())
}

func (s *CDPManagerSuite) TestEnsureLiveTargetNewTabError() {
	mgr, mockClient := s.ensureLiveManager()
	mockClient.On("ListTabs", mock.Anything).Return(nil, nil)
	mockClient.On("NewTab", mock.Anything, "about:blank").Return("", errors.New("browser closing"))

	_, err := mgr.EnsureLiveTarget(context.Background())
	s.ErrorContains(err, "opening a tab after the last one closed")
	s.ErrorContains(err, "browser closing")
}

func (s *CDPManagerSuite) TestEnsureLiveTargetAttachError() {
	mgr, mockClient := s.ensureLiveManager()
	mockClient.On("ListTabs", mock.Anything).Return([]TabInfo{{TargetID: "t-live"}}, nil)
	mockClient.On("NewContextForTarget", "t-live").Return(nil, errors.New("target closed again"))

	_, err := mgr.EnsureLiveTarget(context.Background())
	s.ErrorContains(err, "target closed again")
	s.Equal("t-gone", mgr.ActiveTargetID())
}

func (s *CDPManagerSuite) TestGetOrCreateTargetGone() {
	existingMock := new(mockCDPSession)
	existingMock.On("ListTabs", mock.Anything).Return([]TabInfo{{TargetID: "t-live"}}, nil)

	mgr := NewCDPManager("ws://test:9222", CDPManagerConfig{}, slog.Default())
	mgr.SetClientForTarget("t-live", existingMock)

	_, err := mgr.GetOrCreate(context.Background(), "t-closed")
	require.ErrorIs(s.T(), err, ErrTargetGone)
	// Attaching to a dead target hangs rather than failing, so the point of
	// the check is that the attach is never attempted.
	existingMock.AssertNotCalled(s.T(), "NewContextForTarget", "t-closed")
}

func (s *CDPManagerSuite) TestGetOrCreateAttachesWhenListingFails() {
	newMock := new(mockCDPSession)
	existingMock := new(mockCDPSession)
	existingMock.On("ListTabs", mock.Anything).Return([]TabInfo(nil), errors.New("no listing"))
	existingMock.On("NewContextForTarget", "t-new").Return(newMock, nil)

	mgr := NewCDPManager("ws://test:9222", CDPManagerConfig{}, slog.Default())
	mgr.SetClientForTarget("t-existing", existingMock)

	// A listing that failed says nothing about the target, so the attach goes
	// ahead exactly as it did before the check existed.
	got, err := mgr.GetOrCreate(context.Background(), "t-new")
	require.NoError(s.T(), err)
	require.Equal(s.T(), newMock, got)
}
