package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/browser"
)

var confirmDialog = browser.Dialog{TargetID: "tab-1", Type: "confirm", Message: "Delete it?"}

// blockedManager registers a connected manager for ch-1 whose active tab,
// tab-1, is blocked on d.
func (s *BrowserHandlerSuite) blockedManager(initial *mockCDPSession, d browser.Dialog) *browser.CDPManager {
	initial.On("TargetID").Return("tab-1").Maybe()
	mgr := s.newSyncTestManager("ch-1", initial)
	initial.openDialog(d)
	require.Eventually(s.T(), func() bool { _, ok := mgr.PendingDialog("tab-1"); return ok }, time.Second, 5*time.Millisecond)
	return mgr
}

func (s *BrowserHandlerSuite) actionResp(req browserActionRequest) browserActionResponse {
	w := s.postBrowserAction(req)
	require.Equal(s.T(), http.StatusOK, w.Code)
	var resp browserActionResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	return resp
}

func (s *BrowserHandlerSuite) TestHandleDialogActionWithNothingOpen() {
	tests := []struct {
		name  string
		setup func()
	}{
		{"no browser yet", func() {}},
		{"a browser with no dialog", func() {
			initial := new(mockCDPSession)
			initial.On("TargetID").Return("tab-1").Maybe()
			s.newSyncTestManager("ch-1", initial)
		}},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			tc.setup()
			resp := s.actionResp(browserActionRequest{ChannelID: "ch-1", Action: "handle_dialog", Params: map[string]any{"accept": true}})
			require.Equal(s.T(), browser.ErrNoDialog.Error(), resp.Error)
		})
	}
}

func (s *BrowserHandlerSuite) TestHandleDialogAction() {
	tests := []struct {
		name       string
		params     map[string]any
		accept     bool
		text       string
		err        error
		wantResult string
		wantError  string
		stillOpen  bool
	}{
		{name: "accepted", params: map[string]any{"accept": true}, accept: true, wantResult: `Accepted the confirm "Delete it?"`},
		{name: "dismissed", params: map[string]any{}, wantResult: `Dismissed the confirm "Delete it?"`},
		{name: "a prompt gets its text", params: map[string]any{"accept": true, "prompt_text": "alice"}, accept: true, text: "alice", wantResult: `Accepted the confirm "Delete it?"`},
		{name: "answering fails", params: map[string]any{"accept": true}, accept: true, err: errors.New("gone"), wantError: "handling the dialog failed: gone", stillOpen: true},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			initial := new(mockCDPSession)
			mgr := s.blockedManager(initial, confirmDialog)
			initial.On("HandleDialog", mock.Anything, tc.accept, tc.text).Return(tc.err).Once()

			resp := s.actionResp(browserActionRequest{ChannelID: "ch-1", Action: "handle_dialog", Params: tc.params})
			require.Equal(s.T(), tc.wantResult, resp.Result)
			require.Equal(s.T(), tc.wantError, resp.Error)
			initial.AssertExpectations(s.T())
			_, open := mgr.PendingDialog("tab-1")
			require.Equal(s.T(), tc.stillOpen, open)
		})
	}
}

func (s *BrowserHandlerSuite) TestPageActionsRefusedUnderDialog() {
	initial := new(mockCDPSession)
	s.blockedManager(initial, confirmDialog)

	for _, action := range []string{"screenshot", "page_info", "computer", "navigate"} {
		resp := s.actionResp(browserActionRequest{ChannelID: "ch-1", Action: action})
		require.Contains(s.T(), resp.Error, `a JavaScript confirm is open on this tab and blocks it: "Delete it?"`, action)
		require.Contains(s.T(), resp.Error, "handle_dialog", action)
	}
	// Nothing touched the page: a blocked page would hang the call.
	initial.AssertNotCalled(s.T(), "Screenshot", mock.Anything)
	initial.AssertNotCalled(s.T(), "GetPageInfo", mock.Anything)
}

func (s *BrowserHandlerSuite) TestTabActionsWorkUnderDialog() {
	for _, action := range []string{"list_tabs", "new_tab", "switch_tab", "close_tab"} {
		require.True(s.T(), worksUnderDialog(action), action)
	}
	require.False(s.T(), worksUnderDialog("screenshot"))

	initial := new(mockCDPSession)
	s.blockedManager(initial, confirmDialog)
	s.browserMgr.On("EnsureBrowser", mock.Anything, "ch-1", "").Return(nil).Maybe()
	s.browserMgr.On("GetCDPEndpoint", "ch-1").Return("ws://127.0.0.1:9222").Maybe()
	initial.On("EnableConsoleCapture", mock.Anything, mock.Anything).Return(nil).Maybe()
	initial.On("EnableNetworkCapture", mock.Anything, mock.Anything).Return(nil).Maybe()

	resp := s.actionResp(browserActionRequest{ChannelID: "ch-1", Action: "list_tabs"})
	require.Empty(s.T(), resp.Error)
	require.NotEmpty(s.T(), resp.Tabs, "the tab list still comes back")
}

// readUntil reads WS responses until one of type want arrives.
func (s *BrowserHandlerSuite) readUntil(ws *websocket.Conn, want string) browserWSResponse {
	require.NoError(s.T(), ws.SetReadDeadline(time.Now().Add(2*time.Second)))
	for {
		resp := s.readResp(ws)
		if resp.Type == want {
			return resp
		}
	}
}

func (s *BrowserHandlerSuite) TestWSShowsAndAnswersDialogs() {
	ws, ts, mockCDP := s.startBrowserWS()
	defer ts.Close()
	defer ws.Close()

	d := browser.Dialog{TargetID: "test-target", Type: "prompt", Message: "Name?", DefaultPrompt: "bob"}
	mockCDP.openDialog(d)
	resp := s.readUntil(ws, bwsRespDialog)
	require.Equal(s.T(), "test-target", resp.TargetID)
	require.Equal(s.T(), &d, resp.Dialog)

	mockCDP.On("HandleDialog", mock.Anything, true, "carol").Return(errors.New("gone")).Once()
	require.NoError(s.T(), ws.WriteJSON(browserWSMessage{Type: bwsMsgDialog, TargetID: "test-target", Accept: true, Text: "carol"}))
	resp = s.readUntil(ws, bwsRespError)
	require.Equal(s.T(), "answering the dialog failed: gone", resp.Message)

	mockCDP.On("HandleDialog", mock.Anything, true, "carol").Return(nil).Once()
	require.NoError(s.T(), ws.WriteJSON(browserWSMessage{Type: bwsMsgDialog, TargetID: "test-target", Accept: true, Text: "carol"}))
	resp = s.readUntil(ws, bwsRespDialogDone)
	require.Equal(s.T(), "test-target", resp.TargetID)
	mockCDP.AssertExpectations(s.T())
}

// The pane learns of every tab's dialog and chooser, not just the active
// tab's: switching to another tab must find it asked.
func (s *BrowserHandlerSuite) TestWSStartShowsEveryDialogAlreadyOpen() {
	initial := new(mockCDPSession)
	initial.On("ResetScreencast").Return()
	initial.On("SwitchTarget", "tab-1").Return(nil)
	initial.On("StopScreencast").Return().Maybe()
	initial.On("Close").Return().Maybe()
	s.browserMgr.On("EnsureBrowser", mock.Anything, "ch-1", "").Return(nil)
	s.browserMgr.On("GetCDPEndpoint", "ch-1").Return("ws://127.0.0.1:9222")
	mgr := s.blockedManager(initial, confirmDialog)
	other := browser.Dialog{TargetID: "tab-2", Type: "prompt", Message: "Name?"}
	chooser := browser.FileChooser{TargetID: "tab-2", NodeID: 7}
	initial.openDialog(other)
	initial.openFileChooser(chooser)
	require.Eventually(s.T(), func() bool {
		return len(mgr.PendingDialogs()) == 2 && len(mgr.PendingFileChoosers()) == 1
	}, time.Second, 5*time.Millisecond)

	ws, ts := s.dialBrowserWS()
	defer ts.Close()
	defer ws.Close()
	require.NoError(s.T(), ws.WriteJSON(browserWSMessage{Type: bwsMsgStart, ChannelID: "ch-1"}))
	require.Equal(s.T(), bwsRespStarted, s.readUntil(ws, bwsRespStarted).Type)
	require.Equal(s.T(), &confirmDialog, s.readUntil(ws, bwsRespDialog).Dialog)
	require.Equal(s.T(), &other, s.readUntil(ws, bwsRespDialog).Dialog)
	require.Equal(s.T(), "tab-2", s.readUntil(ws, bwsRespFileChooser).TargetID)
}

func (s *BrowserHandlerSuite) TestAnswerDialogWithoutABrowserIsANoop() {
	bc := &browserWSConn{}
	require.NotPanics(s.T(), func() { bc.answerDialog(browserWSMessage{Type: bwsMsgDialog, TargetID: "t1", Accept: true}) })
}

func (s *BrowserHandlerSuite) TestWSForwardsDownloads() {
	connReady := make(chan *websocket.Conn, 1)
	tsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		connReady <- conn
	}))
	defer tsSrv.Close()
	client, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(tsSrv.URL, "http")+"/", nil)
	require.NoError(s.T(), err)
	defer client.Close()
	server := <-connReady
	defer server.Close()

	bc := &browserWSConn{conn: server, logger: slog.Default(), stopCh: make(chan struct{})}
	events := make(chan browser.TabEvent, 1)
	stop := make(chan struct{})
	defer close(stop)
	go bc.watchTabEvents(events, stop)

	d := browser.Download{GUID: "g1", URL: "https://a.com/r.pdf", Filename: "r.pdf", State: browser.DownloadStarted}
	events <- browser.TabEvent{Kind: browser.DownloadChanged, Download: d}
	resp := s.readResp(client)
	require.Equal(s.T(), bwsRespDownload, resp.Type)
	require.Equal(s.T(), &d, resp.Download)

	events <- browser.TabEvent{Kind: browser.TabUpdated, Tab: browser.TabInfo{TargetID: "t1", URL: "https://b.com/", Title: "B"}}
	resp = s.readResp(client)
	require.Equal(s.T(), bwsRespTabUpdated, resp.Type)
	require.Equal(s.T(), "t1", resp.TargetID)
	require.Equal(s.T(), "https://b.com/", resp.URL)
	require.Equal(s.T(), "B", resp.Title)
}
