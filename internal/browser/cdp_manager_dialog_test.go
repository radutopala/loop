package browser

import (
	"context"
	"errors"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// connectedManager is a manager connected to t1, with a subscriber.
func (s *CDPManagerSuite) connectedManager() (*CDPManager, *mockCDPSession, <-chan TabEvent) {
	mgr, mockClient := s.newTestManager(false)
	mockClient.On("TargetID").Return("t1")
	require.NoError(s.T(), mgr.Connect(context.Background()))
	events, _ := mgr.Subscribe()
	return mgr, mockClient, events
}

func confirmOn(targetID string) Dialog {
	return Dialog{TargetID: targetID, Type: "confirm", Message: "Delete it?"}
}

func (s *CDPManagerSuite) TestConnectWatchesDialogs() {
	mgr, mockClient, events := s.connectedManager()
	onOpened, onClosed := mockClient.dialogWatchers()
	require.NotNil(s.T(), onOpened, "Connect must watch for dialogs")
	require.NotNil(s.T(), onClosed)

	onOpened(confirmOn("t1"))
	require.Eventually(s.T(), func() bool { _, ok := mgr.PendingDialog("t1"); return ok }, time.Second, 5*time.Millisecond)
	onClosed("t1")
	require.Eventually(s.T(), func() bool { _, ok := mgr.PendingDialog("t1"); return !ok }, time.Second, 5*time.Millisecond)

	var got []TabEvent
	require.Eventually(s.T(), func() bool {
		got = append(got, drain(events)...)
		return len(got) == 2
	}, time.Second, 5*time.Millisecond)
	require.Equal(s.T(), []TabEvent{
		{Kind: DialogOpened, Tab: TabInfo{TargetID: "t1"}, Dialog: confirmOn("t1")},
		{Kind: DialogClosed, Tab: TabInfo{TargetID: "t1"}},
	}, got)
}

func (s *CDPManagerSuite) TestAttachWatchesDialogs() {
	mgr, mockClient, _ := s.connectedManager()
	tab := new(mockCDPSession)
	mockClient.On("ListTabs", mock.Anything).Return([]TabInfo{{TargetID: "t2"}}, nil)
	mockClient.On("NewContextForTarget", "t2").Return(tab, nil)

	_, err := mgr.GetOrCreate(context.Background(), "t2")
	require.NoError(s.T(), err)
	onOpened, _ := tab.dialogWatchers()
	require.NotNil(s.T(), onOpened, "an attached tab must watch for dialogs")
}

func (s *CDPManagerSuite) TestAlertIsAcceptedAtOnce() {
	tests := []struct {
		name string
		err  error
	}{
		{"accepted", nil},
		{"answering fails", errors.New("no dialog is showing")},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			mgr, mockClient, events := s.connectedManager()
			mockClient.On("HandleDialog", mock.Anything, true, "").Return(tc.err).Once()

			mgr.dialogOpened(mockClient, Dialog{TargetID: "t1", Type: "alert", Message: "Saved"})

			mockClient.AssertExpectations(s.T())
			_, pending := mgr.PendingDialog("t1")
			require.False(s.T(), pending, "an alert is never held")
			require.Empty(s.T(), drain(events))
		})
	}
}

func (s *CDPManagerSuite) TestDialogOpenedPublishesOnce() {
	mgr, mockClient, events := s.connectedManager()

	mgr.dialogOpened(mockClient, confirmOn("t1"))
	mgr.dialogOpened(mockClient, confirmOn("t1"))
	got, ok := mgr.PendingDialog("t1")
	require.True(s.T(), ok)
	require.Equal(s.T(), confirmOn("t1"), got)
	require.Equal(s.T(), []TabEvent{{Kind: DialogOpened, Tab: TabInfo{TargetID: "t1"}, Dialog: confirmOn("t1")}}, drain(events))

	// A different dialog on the same tab replaces it and is reported.
	next := Dialog{TargetID: "t1", Type: "prompt", Message: "Name?"}
	mgr.dialogOpened(mockClient, next)
	got, _ = mgr.PendingDialog("t1")
	require.Equal(s.T(), next, got)
	require.Equal(s.T(), []TabEvent{{Kind: DialogOpened, Tab: TabInfo{TargetID: "t1"}, Dialog: next}}, drain(events))
}

func (s *CDPManagerSuite) TestDialogClosedWithNothingPendingIsQuiet() {
	mgr, _, events := s.connectedManager()
	mgr.dialogClosed("t1")
	require.Empty(s.T(), drain(events))
}

func (s *CDPManagerSuite) TestHandleDialog() {
	s.Run("nothing pending", func() {
		mgr, _, _ := s.connectedManager()
		require.ErrorIs(s.T(), mgr.HandleDialog(context.Background(), "t1", true, ""), ErrNoDialog)
	})
	s.Run("answering fails and the dialog stays", func() {
		mgr, mockClient, _ := s.connectedManager()
		mgr.dialogOpened(mockClient, confirmOn("t1"))
		mockClient.On("HandleDialog", mock.Anything, false, "").Return(errors.New("gone")).Once()
		require.ErrorContains(s.T(), mgr.HandleDialog(context.Background(), "t1", false, ""), "gone")
		_, pending := mgr.PendingDialog("t1")
		require.True(s.T(), pending)
	})
	s.Run("answered on the session that saw it", func() {
		mgr, mockClient, events := s.connectedManager()
		mgr.dialogOpened(mockClient, confirmOn("t1"))
		drain(events)
		mockClient.On("HandleDialog", mock.Anything, true, "alice").Return(nil).Once()
		require.NoError(s.T(), mgr.HandleDialog(context.Background(), "t1", true, "alice"))
		mockClient.AssertExpectations(s.T())
		_, pending := mgr.PendingDialog("t1")
		require.False(s.T(), pending)
		require.Equal(s.T(), []TabEvent{{Kind: DialogClosed, Tab: TabInfo{TargetID: "t1"}}}, drain(events))
	})
}

func (s *CDPManagerSuite) TestGetOrCreateReusesTheBlockedSession() {
	mgr, mockClient, _ := s.connectedManager()
	blocked := new(mockCDPSession)
	mgr.dialogOpened(blocked, confirmOn("t2"))

	got, err := mgr.GetOrCreate(context.Background(), "t2")
	require.NoError(s.T(), err)
	require.Same(s.T(), blocked, got)
	require.Same(s.T(), blocked, mgr.ActiveClient())
	// No attach was tried: it would hang on the blocked tab.
	mockClient.AssertNotCalled(s.T(), "NewContextForTarget", mock.Anything)
	mockClient.AssertNotCalled(s.T(), "ListTabs", mock.Anything)
}

func (s *CDPManagerSuite) TestDialogsAreForgottenWithTheTabAndOnClose() {
	mgr, mockClient, _ := s.connectedManager()
	mgr.dialogOpened(mockClient, confirmOn("t1"))
	mgr.dialogOpened(mockClient, confirmOn("t2"))

	mgr.UntrackTab("t1")
	_, pending := mgr.PendingDialog("t1")
	require.False(s.T(), pending)
	_, pending = mgr.PendingDialog("t2")
	require.True(s.T(), pending)

	mockClient.On("Close").Return()
	mgr.Close()
	_, pending = mgr.PendingDialog("t2")
	require.False(s.T(), pending)
}
