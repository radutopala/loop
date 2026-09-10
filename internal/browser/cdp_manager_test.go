package browser

import (
	"context"
	"errors"
	"log/slog"
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
func (m *mockCDPSession) ListTabs(ctx context.Context) ([]TabInfo, error) {
	a := m.Called(ctx)
	t, _ := a.Get(0).([]TabInfo)
	return t, a.Error(1)
}
func (m *mockCDPSession) NewTab(ctx context.Context, url string) (string, error) {
	a := m.Called(ctx, url)
	return a.String(0), a.Error(1)
}
func (m *mockCDPSession) CloseTab(ctx context.Context, targetID string) error {
	return m.Called(ctx, targetID).Error(0)
}
func (m *mockCDPSession) Close()           { m.Called() }
func (m *mockCDPSession) ResetScreencast() { m.Called() }
func (m *mockCDPSession) StartScreencast(quality, maxWidth, maxHeight int) <-chan []byte {
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
	return mgr, mockClient
}

func (s *CDPManagerSuite) TestNewCDPManager() {
	mgr := NewCDPManager("ws://test:9222", CDPManagerConfig{
		DiscoverExisting: true,
		MaxRetries:       5,
		RetryDelay:       time.Second,
	}, slog.Default())
	require.NotNil(s.T(), mgr)
	require.Equal(s.T(), "ws://test:9222", mgr.WSEndpoint())
	require.True(s.T(), mgr.DiscoverExisting())
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

func (s *CDPManagerSuite) TestNotifyTargetSwitch() {
	mgr, _ := s.newTestManager(false)

	mgr.NotifyTargetSwitch("t42")
	require.Equal(s.T(), "t42", mgr.ActiveTargetID())

	select {
	case tid := <-mgr.TargetSwitchCh():
		require.Equal(s.T(), "t42", tid)
	default:
		s.T().Fatal("expected target switch signal")
	}
}

func (s *CDPManagerSuite) TestNotifyTargetSwitchDropsWhenFull() {
	mgr, _ := s.newTestManager(false)
	mgr.NotifyTargetSwitch("t1")
	mgr.NotifyTargetSwitch("t2") // should not block

	tid := <-mgr.TargetSwitchCh()
	require.Equal(s.T(), "t1", tid)
}

func (s *CDPManagerSuite) TestNotifyTabAdded() {
	mgr, _ := s.newTestManager(false)
	tab := TabInfo{TargetID: "t1", URL: "https://a.com"}
	mgr.NotifyTabAdded(tab)

	select {
	case got := <-mgr.TabAddedCh():
		require.Equal(s.T(), tab, got)
	default:
		s.T().Fatal("expected tab added signal")
	}
}

func (s *CDPManagerSuite) TestNotifyTabAddedDropsWhenFull() {
	mgr, _ := s.newTestManager(false)
	mgr.NotifyTabAdded(TabInfo{TargetID: "t1"})
	mgr.NotifyTabAdded(TabInfo{TargetID: "t2"}) // should not block
}

func (s *CDPManagerSuite) TestNotifyTabRemoved() {
	mgr, _ := s.newTestManager(false)
	mgr.NotifyTabRemoved("t1")

	select {
	case tid := <-mgr.TabRemovedCh():
		require.Equal(s.T(), "t1", tid)
	default:
		s.T().Fatal("expected tab removed signal")
	}
}

func (s *CDPManagerSuite) TestNotifyTabRemovedDropsWhenFull() {
	mgr, _ := s.newTestManager(false)
	mgr.NotifyTabRemoved("t1")
	mgr.NotifyTabRemoved("t2") // should not block
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
	mockClient.On("NewContextForTarget", "t-other").Return(newMock, nil)

	got, err := mgr.GetOrCreate("t-other")
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

	existingMock.On("NewContextForTarget", "t-new").Return(newMock, nil)

	mgr := NewCDPManager("ws://test:9222", CDPManagerConfig{
		MaxRetries: 1,
		RetryDelay: time.Millisecond,
	}, slog.Default())
	mgr.SetClientForTarget("t-existing", existingMock)

	got, err := mgr.GetOrCreate("t-new")
	require.NoError(s.T(), err)
	require.Equal(s.T(), newMock, got)
	existingMock.AssertCalled(s.T(), "NewContextForTarget", "t-new")
}

func (s *CDPManagerSuite) TestGetOrCreateAttachError() {
	existingMock := new(mockCDPSession)
	existingMock.On("NewContextForTarget", "t-new").Return(nil, errors.New("attach failed"))

	mgr := NewCDPManager("ws://test:9222", CDPManagerConfig{}, slog.Default())
	mgr.SetClientForTarget("t-existing", existingMock)

	_, err := mgr.GetOrCreate("t-new")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "attaching to target t-new")
}

func (s *CDPManagerSuite) TestGetOrCreateNoExistingClient() {
	mgr := NewCDPManager("ws://test:9222", CDPManagerConfig{}, slog.Default())

	_, err := mgr.GetOrCreate("t-new")
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
