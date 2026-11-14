package browser

import (
	"log/slog"
	"sync"
	"time"

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

func (s *CDPManagerSuite) TestAttachWatchesATabOnce() {
	mgr, mockClient, _ := s.connectedManager()
	first := &mockCDPSession{}
	first.On("TargetID").Return("t2").Maybe()
	second := &mockCDPSession{}
	second.On("TargetID").Return("t2").Maybe()
	reopened := &mockCDPSession{}
	reopened.On("TargetID").Return("t2").Maybe()
	mockClient.On("NewContextForTarget", "t2").Return(first, nil).Once()
	mockClient.On("NewContextForTarget", "t2").Return(second, nil).Once()
	mockClient.On("NewContextForTarget", "t2").Return(reopened, nil).Once()

	_, err := mgr.attach(mockClient, "t2")
	require.NoError(s.T(), err)
	require.NotNil(s.T(), first.changeWatcher(), "the first client on a tab watches it")

	got, err := mgr.attach(mockClient, "t2")
	require.NoError(s.T(), err)
	require.Same(s.T(), second, got)
	require.Nil(s.T(), second.changeWatcher(), "a tab already watched must not be watched twice")

	// A tab that closes and comes back under the same id is watched again.
	mgr.TrackTab("t2")
	mgr.tabDestroyed("t2")
	_, err = mgr.attach(mockClient, "t2")
	require.NoError(s.T(), err)
	require.NotNil(s.T(), reopened.changeWatcher())
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
