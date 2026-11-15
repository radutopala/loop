package browser

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func (s *CDPManagerSuite) TestEventQueueRunsInOrder() {
	var q eventQueue
	var mu sync.Mutex
	var got []int
	want := make([]int, 100)
	for i := range want {
		want[i] = i
		q.enqueue(func() {
			mu.Lock()
			got = append(got, i)
			mu.Unlock()
		})
	}
	require.Eventually(s.T(), func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(got) == len(want)
	}, time.Second, 5*time.Millisecond)
	require.Equal(s.T(), want, got)
}

func (s *CDPManagerSuite) TestBackgroundRunsOffTheCaller() {
	mgr := NewCDPManager("ws://test:9222", CDPManagerConfig{}, slog.Default())
	done := make(chan struct{})
	mgr.background(func() { close(done) })
	select {
	case <-done:
	case <-time.After(time.Second):
		s.T().Fatal("background never ran the function")
	}
}

func (s *CDPManagerSuite) TestAttachReusesATabsClient() {
	mgr, mockClient, _ := s.connectedManager()
	first := &mockCDPSession{}
	first.On("TargetID").Return("t2").Maybe()
	reopened := &mockCDPSession{}
	reopened.On("TargetID").Return("t2").Maybe()
	mockClient.On("NewContextForTarget", "t2").Return(first, nil).Once()
	mockClient.On("NewContextForTarget", "t2").Return(reopened, nil).Once()

	got, err := mgr.attach(mockClient, "t2")
	require.NoError(s.T(), err)
	require.Same(s.T(), first, got)
	require.NotNil(s.T(), first.changeWatcher(), "the client on a tab watches it")

	// Asking again hands out the same client: a second one could never be
	// let go, since cancelling it closes the tab.
	mgr.SwitchActive("t1")
	again, err := mgr.attach(mockClient, "t2")
	require.NoError(s.T(), err)
	require.Same(s.T(), first, again)
	require.Same(s.T(), first, mgr.ActiveClient())

	// A tab that closes lets its client go, and one that comes back under the
	// same id is attached and watched again.
	first.On("Close").Return().Once()
	mgr.TrackTab("t2")
	mgr.tabDestroyed("t2")
	first.AssertExpectations(s.T())
	got, err = mgr.attach(mockClient, "t2")
	require.NoError(s.T(), err)
	require.Same(s.T(), reopened, got)
	require.NotNil(s.T(), reopened.changeWatcher())
	mockClient.AssertExpectations(s.T())
}

func (s *CDPManagerSuite) TestAttachToTheConnectionsTabReusesIt() {
	mgr, mockClient, _ := s.connectedManager()
	got, err := mgr.attach(mockClient, "t1")
	require.NoError(s.T(), err)
	require.Same(s.T(), mockClient, got)
	mockClient.AssertNotCalled(s.T(), "NewContextForTarget", "t1")

	// Its tab closing leaves the connection's client alone: Close lets it go.
	mgr.tabDestroyed("t1")
	mockClient.AssertNotCalled(s.T(), "Close")
}

func (s *CDPManagerSuite) TestClosingAnUntrackedTabLetsItsClientGo() {
	mgr, mockClient, _ := s.connectedManager()
	t3 := &mockCDPSession{}
	t3.On("TargetID").Return("t3").Maybe()
	t3.On("Close").Return().Once()
	mockClient.On("NewContextForTarget", "t3").Return(t3, nil).Once()
	_, err := mgr.attach(mockClient, "t3")
	require.NoError(s.T(), err)
	require.False(s.T(), mgr.IsTrackedTab("t3"))

	mgr.tabDestroyed("t3")
	t3.AssertExpectations(s.T())
	// Gone for good: a later tabDestroyed has nothing to let go.
	mgr.tabDestroyed("t3")
}

func (s *CDPManagerSuite) TestConcurrentAttachesShareOneClient() {
	mgr, mockClient, _ := s.connectedManager()
	t2 := &mockCDPSession{}
	t2.On("TargetID").Return("t2").Maybe()
	release := make(chan struct{})
	mockClient.On("NewContextForTarget", "t2").Run(func(mock.Arguments) { <-release }).Return(t2, nil).Once()

	first := make(chan CDPSession, 1)
	go func() {
		c, err := mgr.attach(mockClient, "t2")
		require.NoError(s.T(), err)
		first <- c
	}()
	require.Eventually(s.T(), func() bool {
		mgr.mu.Lock()
		defer mgr.mu.Unlock()
		return mgr.attaching["t2"] != nil
	}, time.Second, time.Millisecond)
	// The second caller finds the first one attaching and waits for it.
	time.AfterFunc(50*time.Millisecond, func() { close(release) })
	got, err := mgr.attach(mockClient, "t2")
	require.NoError(s.T(), err)
	require.Same(s.T(), t2, got)
	require.Same(s.T(), t2, <-first)
	mockClient.AssertNumberOfCalls(s.T(), "NewContextForTarget", 1)
}

func (s *CDPManagerSuite) TestFailedAttachLetsTheNextOneTry() {
	mgr, mockClient, _ := s.connectedManager()
	t2 := &mockCDPSession{}
	t2.On("TargetID").Return("t2").Maybe()
	mockClient.On("NewContextForTarget", "t2").Return(nil, errors.New("wedged")).Once()
	mockClient.On("NewContextForTarget", "t2").Return(t2, nil).Once()

	_, err := mgr.attach(mockClient, "t2")
	require.ErrorContains(s.T(), err, "wedged")
	got, err := mgr.attach(mockClient, "t2")
	require.NoError(s.T(), err)
	require.Same(s.T(), t2, got)
}

func (s *CDPManagerSuite) TestNavigatingDropsTheFileChooser() {
	mgr, mockClient, events := s.connectedManager()
	mgr.titleRechecks = nil
	mgr.fileChooserOpened(mockClient, chooserOn("t1"))
	drain(events)

	moved := TabInfo{TargetID: "t1", URL: "https://b.com", Title: "B"}
	mgr.tabChanged(mockClient, moved)
	require.Equal(s.T(), []TabEvent{
		{Kind: TabUpdated, Tab: moved},
		{Kind: FileChooserClosed, Tab: TabInfo{TargetID: "t1"}},
	}, drain(events))
	_, pending := mgr.PendingFileChooser("t1")
	require.False(s.T(), pending, "the page that asked for files is gone")

	// A title change on the same page keeps the chooser.
	mgr.fileChooserOpened(mockClient, chooserOn("t1"))
	drain(events)
	mgr.tabChanged(mockClient, TabInfo{TargetID: "t1", URL: "https://b.com", Title: "B2"})
	_, pending = mgr.PendingFileChooser("t1")
	require.True(s.T(), pending)
}

func (s *CDPManagerSuite) TestPendingListsEveryTab() {
	mgr, mockClient, _ := s.connectedManager()
	require.Empty(s.T(), mgr.PendingDialogs())
	require.Empty(s.T(), mgr.PendingFileChoosers())

	mgr.dialogOpened(mockClient, confirmOn("t2"))
	mgr.dialogOpened(mockClient, confirmOn("t1"))
	mgr.fileChooserOpened(mockClient, chooserOn("t3"))
	mgr.fileChooserOpened(mockClient, chooserOn("t1"))

	require.Equal(s.T(), []Dialog{confirmOn("t1"), confirmOn("t2")}, mgr.PendingDialogs())
	require.Equal(s.T(), []FileChooser{chooserOn("t1"), chooserOn("t3")}, mgr.PendingFileChoosers())
}

func (s *CDPManagerSuite) TestTitlePollPublishesTitlesChromeKeepsQuietAbout() {
	mgr, mockClient := s.newTestManager(false)
	mgr.titleRechecks = nil
	tick := make(chan time.Time)
	mgr.after = func(time.Duration) <-chan time.Time { return tick }
	mockClient.On("TargetID").Return("t1")
	require.NoError(s.T(), mgr.Connect(context.Background()))
	shown := TabInfo{TargetID: "t1", URL: "https://a.com", Title: "A"}
	mgr.tabChanged(mockClient, shown)
	events, unsubscribe := mgr.Subscribe()
	defer unsubscribe()

	// A failed listing, then one where nothing tracked changed, tell the panes
	// nothing; the page retitling itself later does.
	retitled := TabInfo{TargetID: "t1", URL: "https://a.com", Title: "A (3 unread)"}
	mockClient.On("ListTabs", mock.Anything).Return(nil, errors.New("gone")).Once()
	mockClient.On("ListTabs", mock.Anything).Return([]TabInfo{shown, {TargetID: "untracked", URL: "https://x.com", Title: "X"}}, nil).Once()
	mockClient.On("ListTabs", mock.Anything).Return([]TabInfo{retitled}, nil).Once()
	for range 3 {
		tick <- time.Time{}
	}
	select {
	case ev := <-events:
		require.Equal(s.T(), TabEvent{Kind: TabUpdated, Tab: retitled}, ev)
	case <-time.After(time.Second):
		s.T().Fatal("the later title never reached the pane")
	}
	require.Empty(s.T(), drain(events))
	mockClient.AssertExpectations(s.T())
}

func (s *CDPManagerSuite) TestTitlePollRunsWhileAnyoneSubscribes() {
	mgr, _ := s.newTestManager(false)
	polling := func() bool {
		mgr.mu.Lock()
		defer mgr.mu.Unlock()
		return mgr.stopTitlePoll != nil
	}
	require.False(s.T(), polling())
	_, first := mgr.Subscribe()
	_, second := mgr.Subscribe()
	require.True(s.T(), polling())
	first()
	require.True(s.T(), polling(), "a pane still watches")
	second()
	require.False(s.T(), polling())
	_, again := mgr.Subscribe()
	require.True(s.T(), polling())
	again()

	// Before it connects there is nothing to list.
	mgr.refreshTabs()
}
