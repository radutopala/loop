package api

import (
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/browser"
)

func (s *BrowserHandlerSuite) TestWSShowsAndAnswersFileChoosers() {
	files := []browser.ChosenFile{{Name: "a.txt", Type: "text/plain", Data: "aGk="}}
	tests := []struct {
		name      string
		sent      []browser.ChosenFile
		want      []browser.ChosenFile
		err       error
		wantError string
	}{
		{name: "files picked", sent: files, want: files},
		{name: "cancelled", sent: nil, want: nil},
		{name: "an empty pick cancels", sent: []browser.ChosenFile{}, want: nil},
		{name: "the input is gone", sent: files, want: files, err: errors.New("the file input is gone"),
			wantError: "sending the files failed: the file input is gone"},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			ws, ts, mockCDP := s.startBrowserWS()
			defer ts.Close()
			defer ws.Close()

			mockCDP.openFileChooser(browser.FileChooser{TargetID: "test-target", Multiple: true, NodeID: 5})
			resp := s.readUntil(ws, bwsRespFileChooser)
			require.Equal(s.T(), "test-target", resp.TargetID)
			require.Equal(s.T(), &browser.FileChooser{TargetID: "test-target", Multiple: true}, resp.FileChooser,
				"the pane never sees the node")

			called := make(chan struct{})
			mockCDP.On("SetChosenFiles", mock.Anything, cdp.BackendNodeID(5), tc.want).
				Run(func(mock.Arguments) { close(called) }).Return(tc.err).Once()
			require.NoError(s.T(), ws.WriteJSON(browserWSMessage{Type: bwsMsgFiles, TargetID: "test-target", Files: tc.sent}))
			// The pane hears the chooser close and, when it failed, why — in
			// either order, as the two travel separately.
			require.NoError(s.T(), ws.SetReadDeadline(time.Now().Add(2*time.Second)))
			var closed bool
			var gotError string
			for !closed || (tc.wantError != "" && gotError == "") {
				resp = s.readResp(ws)
				switch resp.Type {
				case bwsRespFilesDone:
					require.Equal(s.T(), "test-target", resp.TargetID)
					closed = true
				case bwsRespError:
					gotError = resp.Message
				}
			}
			require.Equal(s.T(), tc.wantError, gotError)
			select {
			case <-called:
			case <-time.After(time.Second):
				s.T().Fatal("the files never reached the page")
			}
		})
	}
}

// Called directly: the over-limit pick is too big to send through a socket
// quickly under -race.
func (s *BrowserHandlerSuite) TestRefusesFilesOverTheLimit() {
	client, bc := s.serverWSConn()
	mockCDP := new(mockCDPSession)
	mockCDP.On("TargetID").Return("test-target").Maybe()
	bc.cdpMgr = s.newSyncTestManager("ch-1", mockCDP)

	big := strings.Repeat("A", fileChooserMaxBytes/3*4+8)
	bc.answerFileChooser(browserWSMessage{Type: bwsMsgFiles, TargetID: "test-target",
		Files: []browser.ChosenFile{{Name: "big.bin", Data: big}}})
	require.Equal(s.T(), "the files are over the 20 MB limit", s.readUntil(client, bwsRespError).Message)
	mockCDP.AssertNotCalled(s.T(), "SetChosenFiles", mock.Anything, mock.Anything, mock.Anything)
}

func (s *BrowserHandlerSuite) TestWSStartShowsAFileChooserAlreadyOpen() {
	initial := new(mockCDPSession)
	initial.On("ResetScreencast").Return()
	initial.On("SwitchTarget", "tab-1").Return(nil)
	initial.On("StopScreencast").Return().Maybe()
	initial.On("Close").Return().Maybe()
	s.browserMgr.On("EnsureBrowser", mock.Anything, "ch-1", "").Return(nil)
	s.browserMgr.On("GetCDPEndpoint", "ch-1").Return("ws://127.0.0.1:9222")
	initial.On("TargetID").Return("tab-1").Maybe()
	mgr := s.newSyncTestManager("ch-1", initial)
	initial.openFileChooser(browser.FileChooser{TargetID: "tab-1", NodeID: 3})
	require.Eventually(s.T(), func() bool { _, ok := mgr.PendingFileChooser("tab-1"); return ok }, time.Second, 5*time.Millisecond)

	ws, ts := s.dialBrowserWS()
	defer ts.Close()
	defer ws.Close()
	require.NoError(s.T(), ws.WriteJSON(browserWSMessage{Type: bwsMsgStart, ChannelID: "ch-1"}))
	resp := s.readUntil(ws, bwsRespFileChooser)
	require.Equal(s.T(), &browser.FileChooser{TargetID: "tab-1"}, resp.FileChooser)
}

func (s *BrowserHandlerSuite) TestAnswerFileChooserWithoutABrowserIsANoop() {
	bc := &browserWSConn{}
	require.NotPanics(s.T(), func() { bc.answerFileChooser(browserWSMessage{Type: bwsMsgFiles, TargetID: "t1"}) })
}

func (s *BrowserHandlerSuite) TestWSForwardsFileChooserEvents() {
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

	events <- browser.TabEvent{Kind: browser.FileChooserOpened, Tab: browser.TabInfo{TargetID: "t1"}, FileChooser: browser.FileChooser{TargetID: "t1", Multiple: true, NodeID: 9}}
	resp := s.readResp(client)
	require.Equal(s.T(), bwsRespFileChooser, resp.Type)
	require.Equal(s.T(), &browser.FileChooser{TargetID: "t1", Multiple: true}, resp.FileChooser)

	events <- browser.TabEvent{Kind: browser.FileChooserClosed, Tab: browser.TabInfo{TargetID: "t1"}}
	resp = s.readResp(client)
	require.Equal(s.T(), bwsRespFilesDone, resp.Type)
	require.Equal(s.T(), "t1", resp.TargetID)
}
