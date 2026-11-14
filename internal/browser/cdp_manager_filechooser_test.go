package browser

import (
	"context"
	"errors"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func chooserOn(targetID string) FileChooser {
	return FileChooser{TargetID: targetID, Multiple: true, NodeID: 42}
}

func (s *CDPManagerSuite) TestConnectWatchesFileChoosers() {
	mgr, mockClient, events := s.connectedManager()
	onOpened := mockClient.fileChooserWatcher()
	require.NotNil(s.T(), onOpened, "Connect must watch for file choosers")
	require.Eventually(s.T(), func() bool { return mockClient.interceptCount() == 1 }, time.Second, 5*time.Millisecond,
		"Connect must have Chrome hand file choosers over")

	onOpened(chooserOn("t1"))
	var got []TabEvent
	require.Eventually(s.T(), func() bool {
		got = append(got, drain(events)...)
		return len(got) == 1
	}, time.Second, 5*time.Millisecond)
	require.Equal(s.T(), TabEvent{Kind: FileChooserOpened, Tab: TabInfo{TargetID: "t1"}, FileChooser: chooserOn("t1")}, got[0])
	fc, ok := mgr.PendingFileChooser("t1")
	require.True(s.T(), ok)
	require.Equal(s.T(), chooserOn("t1"), fc)
}

func (s *CDPManagerSuite) TestAttachInterceptsFileChoosers() {
	tests := []struct {
		name string
		err  error
	}{
		{"intercepted", nil},
		{"intercepting fails", errors.New("tab is blocked")},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			mgr, mockClient, _ := s.connectedManager()
			tab := &mockCDPSession{interceptErr: tc.err}
			tab.On("TargetID").Return("t2").Maybe()
			mockClient.On("ListTabs", mock.Anything).Return([]TabInfo{{TargetID: "t2"}}, nil)
			mockClient.On("NewContextForTarget", "t2").Return(tab, nil)

			_, err := mgr.GetOrCreate(context.Background(), "t2")
			require.NoError(s.T(), err)
			require.NotNil(s.T(), tab.fileChooserWatcher(), "an attached tab must watch for file choosers")
			require.Eventually(s.T(), func() bool { return tab.interceptCount() == 1 }, time.Second, 5*time.Millisecond)
		})
	}
}

func (s *CDPManagerSuite) TestFileChooserOpenedPublishesOnce() {
	mgr, mockClient, events := s.connectedManager()

	mgr.fileChooserOpened(mockClient, chooserOn("t1"))
	mgr.fileChooserOpened(new(mockCDPSession), chooserOn("t1"))
	require.Equal(s.T(), []TabEvent{{Kind: FileChooserOpened, Tab: TabInfo{TargetID: "t1"}, FileChooser: chooserOn("t1")}}, drain(events))

	// Another input on the same tab replaces it and is reported.
	next := FileChooser{TargetID: "t1", NodeID: 7}
	mgr.fileChooserOpened(mockClient, next)
	got, _ := mgr.PendingFileChooser("t1")
	require.Equal(s.T(), next, got)
	require.Equal(s.T(), []TabEvent{{Kind: FileChooserOpened, Tab: TabInfo{TargetID: "t1"}, FileChooser: next}}, drain(events))
}

func (s *CDPManagerSuite) TestAnswerFileChooser() {
	files := []ChosenFile{{Name: "a.txt", Type: "text/plain", Data: "aGk="}}
	tests := []struct {
		name  string
		files []ChosenFile
		err   error
	}{
		{"files picked", files, nil},
		{"cancelled", nil, nil},
		{"the input is gone", files, errors.New("the file input is gone")},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			mgr, mockClient, events := s.connectedManager()
			tab := new(mockCDPSession)
			mgr.fileChooserOpened(tab, chooserOn("t1"))
			drain(events)
			tab.On("SetChosenFiles", mock.Anything, chooserOn("t1").NodeID, tc.files).Return(tc.err).Once()

			err := mgr.AnswerFileChooser(context.Background(), "t1", tc.files)
			if tc.err != nil {
				require.ErrorIs(s.T(), err, tc.err)
			} else {
				require.NoError(s.T(), err)
			}
			tab.AssertExpectations(s.T())
			mockClient.AssertNotCalled(s.T(), "SetChosenFiles", mock.Anything, mock.Anything, mock.Anything)
			_, pending := mgr.PendingFileChooser("t1")
			require.False(s.T(), pending, "an answered chooser is done with, whatever came of it")
			require.Equal(s.T(), []TabEvent{{Kind: FileChooserClosed, Tab: TabInfo{TargetID: "t1"}}}, drain(events))
		})
	}
}

func (s *CDPManagerSuite) TestAnswerFileChooserWithNothingOpen() {
	mgr, _, events := s.connectedManager()
	require.ErrorIs(s.T(), mgr.AnswerFileChooser(context.Background(), "t1", nil), ErrNoFileChooser)
	require.Empty(s.T(), drain(events))
}

func (s *CDPManagerSuite) TestClosingATabForgetsItsFileChooser() {
	mgr, mockClient, _ := s.connectedManager()
	mgr.fileChooserOpened(mockClient, chooserOn("t1"))

	mgr.mu.Lock()
	mgr.untrackTabLocked("t1")
	mgr.mu.Unlock()

	_, pending := mgr.PendingFileChooser("t1")
	require.False(s.T(), pending)
}
