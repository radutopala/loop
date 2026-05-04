package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/db"
)

func (s *TerminalHandlerSuite) TestUnknownMessageType() {
	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "bogus"})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "error", msg.Type)
	require.Contains(s.T(), msg.Message, "unknown message type: bogus")
	require.Equal(s.T(), wsErrCodeUnknownMessage, msg.ErrorCode)
}

func (s *TerminalHandlerSuite) TestInvalidJSON() {
	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	require.NoError(s.T(), conn.WriteMessage(websocket.TextMessage, []byte("not json")))

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "error", msg.Type)
	require.Contains(s.T(), msg.Message, "invalid JSON")
	require.Equal(s.T(), wsErrCodeInvalidJSON, msg.ErrorCode)
}

func (s *TerminalHandlerSuite) TestSessionClosedNotification() {
	outCh := make(chan []byte, 1)
	doneCh := make(chan struct{})
	s.terminal.On("CreateSession", mock.Anything, "ctr-1", ([]string)(nil)).
		Return("sess-1", (<-chan []byte)(outCh), ([]byte)(nil), (<-chan struct{})(doneCh), nil)
	s.terminal.On("DetachSession", mock.Anything, mock.Anything).Return(nil).Maybe()

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ContainerID: "ctr-1"})
	readStatusMsg(s.T(), conn) // created

	close(doneCh)

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "closed", msg.Type)
}

func (s *TerminalHandlerSuite) TestCreateSessionNoHistory() {
	outCh := make(chan []byte, 1)
	doneCh := make(chan struct{})
	s.terminal.On("CreateSession", mock.Anything, "ctr-1", ([]string)(nil)).
		Return("sess-1", (<-chan []byte)(outCh), ([]byte)(nil), (<-chan struct{})(doneCh), nil)
	s.terminal.On("DetachSession", mock.Anything, mock.Anything).Return(nil).Maybe()

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ContainerID: "ctr-1"})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "created", msg.Type)

	outCh <- []byte("live")
	data := readBinaryMsg(s.T(), conn)
	require.Equal(s.T(), []byte("live"), data)

	close(doneCh)
}

func (s *TerminalHandlerSuite) TestDetachOnNewCreate() {
	outCh1 := make(chan []byte, 1)
	doneCh1 := make(chan struct{})
	s.terminal.On("CreateSession", mock.Anything, "ctr-1", ([]string)(nil)).
		Return("sess-1", (<-chan []byte)(outCh1), ([]byte)(nil), (<-chan struct{})(doneCh1), nil).Once()

	outCh2 := make(chan []byte, 1)
	doneCh2 := make(chan struct{})
	s.terminal.On("CreateSession", mock.Anything, "ctr-2", ([]string)(nil)).
		Return("sess-2", (<-chan []byte)(outCh2), ([]byte)(nil), (<-chan struct{})(doneCh2), nil).Once()
	s.terminal.On("DetachSession", mock.Anything, mock.Anything).Return(nil).Maybe()

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ContainerID: "ctr-1"})
	readStatusMsg(s.T(), conn) // created sess-1

	close(doneCh1)
	time.Sleep(50 * time.Millisecond)

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ContainerID: "ctr-2"})

	// Drain messages until we get "created" for sess-2
	for {
		msg := readStatusMsg(s.T(), conn)
		if msg.Type == "created" {
			require.Equal(s.T(), "sess-2", msg.SessionID)
			break
		}
	}

	close(doneCh2)
}

func (s *TerminalHandlerSuite) TestDetachErrorLogged() {
	// Use a fresh mock so the default DetachSession→nil from SetupTest doesn't shadow the error return.
	s.terminal = new(MockTerminalManager)
	s.srv.SetTerminalManager(s.terminal)

	outCh := make(chan []byte, 1)
	doneCh := make(chan struct{})
	s.terminal.On("CreateSession", mock.Anything, "ctr-1", ([]string)(nil)).
		Return("sess-1", (<-chan []byte)(outCh), ([]byte)(nil), (<-chan struct{})(doneCh), nil).Once()

	outCh2 := make(chan []byte, 1)
	doneCh2 := make(chan struct{})
	s.terminal.On("CreateSession", mock.Anything, "ctr-2", ([]string)(nil)).
		Return("sess-2", (<-chan []byte)(outCh2), ([]byte)(nil), (<-chan struct{})(doneCh2), nil).Once()

	// DetachSession returns an error to exercise the warning log path.
	s.terminal.On("DetachSession", mock.Anything, mock.Anything).Return(errors.New("detach failed")).Maybe()

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	// Create first session.
	sendControl(s.T(), conn, wsControlMessage{Type: "create", ContainerID: "ctr-1"})
	readStatusMsg(s.T(), conn) // created sess-1

	close(doneCh)
	time.Sleep(50 * time.Millisecond)

	// Create second session — triggers detachCurrent with error.
	sendControl(s.T(), conn, wsControlMessage{Type: "create", ContainerID: "ctr-2"})

	for {
		msg := readStatusMsg(s.T(), conn)
		if msg.Type == "created" {
			require.Equal(s.T(), "sess-2", msg.SessionID)
			break
		}
	}

	close(doneCh2)
}

func (s *TerminalHandlerSuite) TestStopOnCloseWhenSessionIDOverride() {
	// When a create message includes session_id (sessions panel), the exec session
	// should be stopped (not just detached) when the WebSocket disconnects.
	s.terminal = new(MockTerminalManager)
	s.srv.SetTerminalManager(s.terminal)

	outCh := make(chan []byte, 1)
	doneCh := make(chan struct{})
	s.terminal.On("CreateSession", mock.Anything, "resolved-ctr", []string(nil)).
		Return("sess-preview", (<-chan []byte)(outCh), []byte(nil), (<-chan struct{})(doneCh), nil)
	inputSent := onSendInputCalled(s.terminal, "sess-preview", []byte("claude --dangerously-skip-permissions --resume sess-picked\n"))
	// On WS close: KillProcessGroup + StopSession (not DetachSession).
	s.terminal.On("KillProcessGroup", mock.Anything, "sess-preview").Return(nil).Maybe()
	stopCalled := make(chan struct{}, 1)
	s.terminal.On("StopSession", "sess-preview").Return("resolved-ctr", nil).Run(func(_ mock.Arguments) {
		stopCalled <- struct{}{}
	}).Maybe()

	store := new(MockChannelLister)
	store.On("GetChannel", mock.Anything, "ch-sess").
		Return(&db.Channel{ChannelID: "ch-sess", SessionID: "sess-stored"}, nil)
	s.srv.store = store

	finder := new(mockContainerManager)
	finder.On("FindOrCreateShell", mock.Anything, "ch-sess", mock.Anything, mock.Anything).Return("resolved-ctr", nil)
	s.srv.containerRegistry = finder

	builder := new(MockInteractiveCmdBuilder)
	builder.On("BuildInteractiveCmd", "ch-sess", "", "", "sess-picked", "", false, mock.Anything).
		Return("claude --dangerously-skip-permissions --resume sess-picked")
	s.srv.SetInteractiveCmdBuilder(builder)

	conn, ts := s.dialWS()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ChannelID: "ch-sess", SessionID: "sess-picked"})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "created", msg.Type)
	select {
	case <-inputSent:
	case <-time.After(time.Second):
		s.T().Fatal("timed out waiting for SendInput")
	}

	// Close WS — should trigger StopSession (not DetachSession).
	conn.Close()
	ts.Close()

	select {
	case <-stopCalled:
	case <-time.After(2 * time.Second):
		s.T().Fatal("timed out waiting for StopSession")
	}
}

func (s *TerminalHandlerSuite) TestStopOnCloseKillProcessGroupError() {
	// When KillProcessGroup returns an error, stopCurrentSession should log a warning
	// and still call StopSession.
	s.terminal = new(MockTerminalManager)
	s.srv.SetTerminalManager(s.terminal)

	outCh := make(chan []byte, 1)
	doneCh := make(chan struct{})
	s.terminal.On("CreateSession", mock.Anything, "resolved-ctr", []string(nil)).
		Return("sess-err", (<-chan []byte)(outCh), []byte(nil), (<-chan struct{})(doneCh), nil)
	inputSent := onSendInputCalled(s.terminal, "sess-err", []byte("claude --dangerously-skip-permissions --resume sess-picked\n"))
	// KillProcessGroup returns an error — StopSession should still be called.
	s.terminal.On("KillProcessGroup", mock.Anything, "sess-err").Return(errors.New("kill failed")).Maybe()
	stopCalled := make(chan struct{}, 1)
	s.terminal.On("StopSession", "sess-err").Return("resolved-ctr", nil).Run(func(_ mock.Arguments) {
		stopCalled <- struct{}{}
	}).Maybe()

	store := new(MockChannelLister)
	store.On("GetChannel", mock.Anything, "ch-sess").
		Return(&db.Channel{ChannelID: "ch-sess", SessionID: "sess-stored"}, nil)
	s.srv.store = store

	finder := new(mockContainerManager)
	finder.On("FindOrCreateShell", mock.Anything, "ch-sess", mock.Anything, mock.Anything).Return("resolved-ctr", nil)
	s.srv.containerRegistry = finder

	builder := new(MockInteractiveCmdBuilder)
	builder.On("BuildInteractiveCmd", "ch-sess", "", "", "sess-picked", "", false, mock.Anything).
		Return("claude --dangerously-skip-permissions --resume sess-picked")
	s.srv.SetInteractiveCmdBuilder(builder)

	conn, ts := s.dialWS()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ChannelID: "ch-sess", SessionID: "sess-picked"})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "created", msg.Type)
	select {
	case <-inputSent:
	case <-time.After(time.Second):
		s.T().Fatal("timed out waiting for SendInput")
	}

	// Close WS — triggers stopCurrentSession with KillProcessGroup error.
	conn.Close()
	ts.Close()

	select {
	case <-stopCalled:
	case <-time.After(2 * time.Second):
		s.T().Fatal("timed out waiting for StopSession after KillProcessGroup error")
	}
}

func (s *TerminalHandlerSuite) TestStopOnCloseStopSessionError() {
	// When StopSession returns an error, stopCurrentSession should log a warning
	// and still clear session state.
	s.terminal = new(MockTerminalManager)
	s.srv.SetTerminalManager(s.terminal)

	outCh := make(chan []byte, 1)
	doneCh := make(chan struct{})
	s.terminal.On("CreateSession", mock.Anything, "resolved-ctr", []string(nil)).
		Return("sess-stop-err", (<-chan []byte)(outCh), []byte(nil), (<-chan struct{})(doneCh), nil)
	inputSent := onSendInputCalled(s.terminal, "sess-stop-err", []byte("claude --dangerously-skip-permissions --resume sess-picked\n"))
	s.terminal.On("KillProcessGroup", mock.Anything, "sess-stop-err").Return(nil).Maybe()
	// StopSession returns an error.
	stopCalled := make(chan struct{}, 1)
	s.terminal.On("StopSession", "sess-stop-err").Return("", errors.New("stop failed")).Run(func(_ mock.Arguments) {
		stopCalled <- struct{}{}
	}).Maybe()

	store := new(MockChannelLister)
	store.On("GetChannel", mock.Anything, "ch-sess").
		Return(&db.Channel{ChannelID: "ch-sess", SessionID: "sess-stored"}, nil)
	s.srv.store = store

	finder := new(mockContainerManager)
	finder.On("FindOrCreateShell", mock.Anything, "ch-sess", mock.Anything, mock.Anything).Return("resolved-ctr", nil)
	s.srv.containerRegistry = finder

	builder := new(MockInteractiveCmdBuilder)
	builder.On("BuildInteractiveCmd", "ch-sess", "", "", "sess-picked", "", false, mock.Anything).
		Return("claude --dangerously-skip-permissions --resume sess-picked")
	s.srv.SetInteractiveCmdBuilder(builder)

	conn, ts := s.dialWS()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ChannelID: "ch-sess", SessionID: "sess-picked"})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "created", msg.Type)
	select {
	case <-inputSent:
	case <-time.After(time.Second):
		s.T().Fatal("timed out waiting for SendInput")
	}

	// Close WS — triggers stopCurrentSession with StopSession error.
	conn.Close()
	ts.Close()

	select {
	case <-stopCalled:
	case <-time.After(2 * time.Second):
		s.T().Fatal("timed out waiting for StopSession error path")
	}
}

func (s *TerminalHandlerSuite) TestUpgradeError() {
	// Send a plain HTTP GET (not a WebSocket upgrade) to trigger the upgrade error path.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/ws/terminal", s.srv.handleTerminalWS)

	req := httptest.NewRequest("GET", "/api/ws/terminal", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	// Upgrade fails because it's not a WebSocket request — handler returns without writing a status.
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *TerminalHandlerSuite) TestOutputChannelClosed() {
	// Test the path where the output channel is closed (not done channel).
	outCh := make(chan []byte, 1)
	doneCh := make(chan struct{})
	s.terminal.On("CreateSession", mock.Anything, "ctr-1", ([]string)(nil)).
		Return("sess-1", (<-chan []byte)(outCh), ([]byte)(nil), (<-chan struct{})(doneCh), nil)
	s.terminal.On("DetachSession", mock.Anything, mock.Anything).Return(nil).Maybe()

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ContainerID: "ctr-1"})
	readStatusMsg(s.T(), conn) // created

	// Close the output channel (not done) to trigger the !ok path.
	close(outCh)

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "closed", msg.Type)
}

func (s *TerminalHandlerSuite) TestStopChOnDisconnect() {
	// Test the stopCh path: close the WS while streaming is active.
	outCh := make(chan []byte, 64)
	doneCh := make(chan struct{})
	s.terminal.On("CreateSession", mock.Anything, "ctr-1", ([]string)(nil)).
		Return("sess-1", (<-chan []byte)(outCh), ([]byte)(nil), (<-chan struct{})(doneCh), nil)
	s.terminal.On("DetachSession", mock.Anything, mock.Anything).Return(nil).Maybe()

	conn, ts := s.dialWS()
	defer ts.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ContainerID: "ctr-1"})
	readStatusMsg(s.T(), conn) // created

	// Close the WS connection — this triggers stopCh via the defer.
	conn.Close()

	// Give handler time to process the disconnect.
	time.Sleep(100 * time.Millisecond)

	close(doneCh)
}

func (s *TerminalHandlerSuite) TestSendInputEmptyBase64() {
	outCh := make(chan []byte, 1)
	doneCh := make(chan struct{})
	s.terminal.On("CreateSession", mock.Anything, "ctr-1", ([]string)(nil)).
		Return("sess-1", (<-chan []byte)(outCh), ([]byte)(nil), (<-chan struct{})(doneCh), nil)
	s.terminal.On("SendInput", "sess-1", []byte{}).Return(nil)
	s.terminal.On("DetachSession", mock.Anything, mock.Anything).Return(nil).Maybe()

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ContainerID: "ctr-1"})
	readStatusMsg(s.T(), conn) // created

	// Empty string is valid base64 that decodes to empty bytes.
	sendControl(s.T(), conn, wsControlMessage{Type: "input", Data: ""})

	time.Sleep(50 * time.Millisecond)
	close(doneCh)
	time.Sleep(50 * time.Millisecond)
	s.terminal.AssertCalled(s.T(), "SendInput", "sess-1", []byte{})
}

func (s *TerminalHandlerSuite) TestRapidCreateSessions() {
	// Rapidly create sessions — each should detach the previous.
	var outChs []chan []byte
	var doneChs []chan struct{}
	for i := range 3 {
		outCh := make(chan []byte, 1)
		doneCh := make(chan struct{})
		outChs = append(outChs, outCh)
		doneChs = append(doneChs, doneCh)
		ctr := "ctr-" + string(rune('a'+i))
		sessID := "sess-" + string(rune('a'+i))
		s.terminal.On("CreateSession", mock.Anything, ctr, ([]string)(nil)).
			Return(sessID, (<-chan []byte)(outCh), ([]byte)(nil), (<-chan struct{})(doneCh), nil).Once()
	}
	s.terminal.On("DetachSession", mock.Anything, mock.Anything).Return(nil).Maybe()

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	// Create three sessions in rapid succession.
	for i := range 3 {
		ctr := "ctr-" + string(rune('a'+i))
		// Close done channel of previous session before creating the next one.
		if i > 0 {
			close(doneChs[i-1])
			time.Sleep(50 * time.Millisecond) // let streamOutput exit
		}
		sendControl(s.T(), conn, wsControlMessage{Type: "create", ContainerID: ctr})
		// Drain messages until we get "created" for this session.
		for {
			msg := readStatusMsg(s.T(), conn)
			if msg.Type == "created" {
				require.Equal(s.T(), "sess-"+string(rune('a'+i)), msg.SessionID)
				break
			}
		}
	}

	// The last session should be active — send output on it.
	outChs[2] <- []byte("final")
	data := readBinaryMsg(s.T(), conn)
	require.Equal(s.T(), []byte("final"), data)

	close(doneChs[2])
}

func (s *TerminalHandlerSuite) TestWriteJSONErrorLogged() {
	// Close the connection before the handler sends a response so writeJSON hits a write error.
	outCh := make(chan []byte, 1)
	doneCh := make(chan struct{})
	s.terminal.On("CreateSession", mock.Anything, "ctr-1", ([]string)(nil)).
		Return("sess-1", (<-chan []byte)(outCh), ([]byte)(nil), (<-chan struct{})(doneCh), nil)
	s.terminal.On("DetachSession", mock.Anything, mock.Anything).Return(nil).Maybe()

	conn, ts := s.dialWS()
	defer ts.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ContainerID: "ctr-1"})
	readStatusMsg(s.T(), conn) // created

	// Close conn, then push output to trigger writeBinary on a closed connection.
	conn.Close()
	outCh <- []byte("data after close")

	time.Sleep(100 * time.Millisecond)
	close(doneCh)
}

func (s *TerminalHandlerSuite) TestCreateSessionEmptyCmdArg() {
	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ContainerID: "ctr-1", Cmd: []string{"/bin/bash", ""}})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "error", msg.Type)
	require.Contains(s.T(), msg.Message, "cmd contains empty argument")
	require.Equal(s.T(), wsErrCodeInvalidInput, msg.ErrorCode)
}

func (s *TerminalHandlerSuite) TestCreateSessionTooManyCmdArgs() {
	args := make([]string, maxCmdArgs+1)
	for i := range args {
		args[i] = "arg"
	}

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ContainerID: "ctr-1", Cmd: args})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "error", msg.Type)
	require.Contains(s.T(), msg.Message, "cmd exceeds maximum arguments")
	require.Equal(s.T(), wsErrCodeInvalidInput, msg.ErrorCode)
}

func (s *TerminalHandlerSuite) TestWriteBinaryErrorLogged() {
	// Trigger writeBinary failure by closing connection before streaming output.
	outCh := make(chan []byte, 1)
	doneCh := make(chan struct{})
	s.terminal.On("CreateSession", mock.Anything, "ctr-1", ([]string)(nil)).
		Return("sess-1", (<-chan []byte)(outCh), []byte("history"), (<-chan struct{})(doneCh), nil)
	s.terminal.On("DetachSession", mock.Anything, mock.Anything).Return(nil).Maybe()

	// Use a custom server to control timing.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/ws/terminal", s.srv.handleTerminalWS)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	wsURL := "ws" + ts.URL[4:] + "/api/ws/terminal"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(s.T(), err)

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ContainerID: "ctr-1"})
	readStatusMsg(s.T(), conn) // created
	readBinaryMsg(s.T(), conn) // history

	// Close the conn, then send output — writeBinary will fail.
	conn.Close()
	outCh <- []byte("after close")
	time.Sleep(100 * time.Millisecond)
	close(doneCh)
}
