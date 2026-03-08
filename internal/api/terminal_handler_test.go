package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type MockTerminalManager struct {
	mock.Mock
}

func (m *MockTerminalManager) CreateSession(ctx context.Context, containerID string, cmd []string) (string, <-chan []byte, []byte, <-chan struct{}, error) {
	args := m.Called(ctx, containerID, cmd)
	var outCh <-chan []byte
	if args.Get(1) != nil {
		outCh = args.Get(1).(<-chan []byte)
	}
	var history []byte
	if args.Get(2) != nil {
		history = args.Get(2).([]byte)
	}
	var doneCh <-chan struct{}
	if args.Get(3) != nil {
		doneCh = args.Get(3).(<-chan struct{})
	}
	return args.String(0), outCh, history, doneCh, args.Error(4)
}

func (m *MockTerminalManager) AttachSession(sessionID string) (<-chan []byte, []byte, <-chan struct{}, error) {
	args := m.Called(sessionID)
	var outCh <-chan []byte
	if args.Get(0) != nil {
		outCh = args.Get(0).(<-chan []byte)
	}
	var history []byte
	if args.Get(1) != nil {
		history = args.Get(1).([]byte)
	}
	var doneCh <-chan struct{}
	if args.Get(2) != nil {
		doneCh = args.Get(2).(<-chan struct{})
	}
	return outCh, history, doneCh, args.Error(3)
}

func (m *MockTerminalManager) DetachSession(sessionID string, output <-chan []byte) error {
	return m.Called(sessionID, output).Error(0)
}

func (m *MockTerminalManager) SendInput(sessionID string, data []byte) error {
	return m.Called(sessionID, data).Error(0)
}

func (m *MockTerminalManager) Resize(ctx context.Context, sessionID string, rows, cols uint) error {
	return m.Called(ctx, sessionID, rows, cols).Error(0)
}

func (m *MockTerminalManager) StopSession(sessionID string) error {
	return m.Called(sessionID).Error(0)
}

type TerminalHandlerSuite struct {
	suite.Suite
	terminal *MockTerminalManager
	srv      *Server
}

func TestTerminalHandlerSuite(t *testing.T) {
	suite.Run(t, new(TerminalHandlerSuite))
}

func (s *TerminalHandlerSuite) SetupTest() {
	s.terminal = new(MockTerminalManager)
	s.srv = nilServer()
	s.srv.SetTerminalManager(s.terminal)
}

// dialWS creates a test HTTP server with the terminal WS handler and returns a connected websocket.
func (s *TerminalHandlerSuite) dialWS() (*websocket.Conn, *httptest.Server) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/ws/terminal", s.srv.handleTerminalWS)
	ts := httptest.NewServer(mux)

	wsURL := "ws" + ts.URL[4:] + "/api/ws/terminal"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(s.T(), err)
	return conn, ts
}

func readStatusMsg(t *testing.T, conn *websocket.Conn) wsStatusMessage {
	t.Helper()
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(2*time.Second)))
	_, data, err := conn.ReadMessage()
	require.NoError(t, err)
	var msg wsStatusMessage
	require.NoError(t, json.Unmarshal(data, &msg))
	return msg
}

func readBinaryMsg(t *testing.T, conn *websocket.Conn) []byte {
	t.Helper()
	require.NoError(t, conn.SetReadDeadline(time.Now().Add(2*time.Second)))
	msgType, data, err := conn.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, websocket.BinaryMessage, msgType)
	return data
}

func sendControl(t *testing.T, conn *websocket.Conn, msg wsControlMessage) {
	t.Helper()
	data, err := json.Marshal(msg)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, data))
}

// --- Tests ---

func (s *TerminalHandlerSuite) TestTerminalNotConfigured() {
	srv := nilServer() // no terminal manager
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/ws/terminal", srv.handleTerminalWS)

	req := httptest.NewRequest("GET", "/api/ws/terminal", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

func (s *TerminalHandlerSuite) TestSetTerminalManager() {
	srv := nilServer()
	require.Nil(s.T(), srv.termManager)
	mgr := new(MockTerminalManager)
	srv.SetTerminalManager(mgr)
	require.NotNil(s.T(), srv.termManager)
}

func (s *TerminalHandlerSuite) TestCreateSession() {
	outCh := make(chan []byte, 1)
	doneCh := make(chan struct{})
	s.terminal.On("CreateSession", mock.Anything, "ctr-1", []string{"/bin/bash"}).
		Return("sess-1", (<-chan []byte)(outCh), []byte("history"), (<-chan struct{})(doneCh), nil)
	s.terminal.On("DetachSession", mock.Anything, mock.Anything).Return(nil).Maybe()

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ContainerID: "ctr-1", Cmd: []string{"/bin/bash"}})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "created", msg.Type)
	require.Equal(s.T(), "sess-1", msg.SessionID)

	data := readBinaryMsg(s.T(), conn)
	require.Equal(s.T(), []byte("history"), data)

	outCh <- []byte("output data")
	data = readBinaryMsg(s.T(), conn)
	require.Equal(s.T(), []byte("output data"), data)

	close(doneCh)
}

func (s *TerminalHandlerSuite) TestCreateSessionError() {
	s.terminal.On("CreateSession", mock.Anything, "ctr-bad", ([]string)(nil)).
		Return("", nil, nil, nil, errors.New("exec failed"))

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ContainerID: "ctr-bad"})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "error", msg.Type)
	require.Contains(s.T(), msg.Message, "exec failed")
	require.Equal(s.T(), wsErrCodeSessionFailed, msg.ErrorCode)
}

func (s *TerminalHandlerSuite) TestCreateSessionMissingContainerID() {
	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "create"})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "error", msg.Type)
	require.Contains(s.T(), msg.Message, "container_id required")
	require.Equal(s.T(), wsErrCodeMissingField, msg.ErrorCode)
}

func (s *TerminalHandlerSuite) TestAttachSession() {
	outCh := make(chan []byte, 1)
	doneCh := make(chan struct{})
	s.terminal.On("AttachSession", "sess-1").
		Return((<-chan []byte)(outCh), []byte("old output"), (<-chan struct{})(doneCh), nil)
	s.terminal.On("DetachSession", mock.Anything, mock.Anything).Return(nil).Maybe()

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "attach", SessionID: "sess-1"})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "attached", msg.Type)
	require.Equal(s.T(), "sess-1", msg.SessionID)

	data := readBinaryMsg(s.T(), conn)
	require.Equal(s.T(), []byte("old output"), data)

	close(doneCh)
}

func (s *TerminalHandlerSuite) TestAttachSessionError() {
	s.terminal.On("AttachSession", "bad-sess").
		Return(nil, nil, nil, errors.New("session not found"))

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "attach", SessionID: "bad-sess"})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "error", msg.Type)
	require.Contains(s.T(), msg.Message, "session not found")
	require.Equal(s.T(), wsErrCodeSessionFailed, msg.ErrorCode)
}

func (s *TerminalHandlerSuite) TestAttachSessionMissingID() {
	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "attach"})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "error", msg.Type)
	require.Contains(s.T(), msg.Message, "session_id required")
	require.Equal(s.T(), wsErrCodeMissingField, msg.ErrorCode)
}

func (s *TerminalHandlerSuite) TestSendInput() {
	outCh := make(chan []byte, 1)
	doneCh := make(chan struct{})
	s.terminal.On("CreateSession", mock.Anything, "ctr-1", ([]string)(nil)).
		Return("sess-1", (<-chan []byte)(outCh), ([]byte)(nil), (<-chan struct{})(doneCh), nil)
	s.terminal.On("SendInput", "sess-1", []byte("hello")).Return(nil)
	s.terminal.On("DetachSession", mock.Anything, mock.Anything).Return(nil).Maybe()

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ContainerID: "ctr-1"})
	readStatusMsg(s.T(), conn) // created

	encoded := base64.StdEncoding.EncodeToString([]byte("hello"))
	sendControl(s.T(), conn, wsControlMessage{Type: "input", Data: encoded})

	time.Sleep(50 * time.Millisecond)
	close(doneCh)
	time.Sleep(50 * time.Millisecond)
	s.terminal.AssertCalled(s.T(), "SendInput", "sess-1", []byte("hello"))
}

func (s *TerminalHandlerSuite) TestSendInputNoSession() {
	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "input", Data: "aGVsbG8="})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "error", msg.Type)
	require.Contains(s.T(), msg.Message, "no active session")
	require.Equal(s.T(), wsErrCodeNoSession, msg.ErrorCode)
}

func (s *TerminalHandlerSuite) TestSendInputInvalidBase64() {
	outCh := make(chan []byte, 1)
	doneCh := make(chan struct{})
	s.terminal.On("CreateSession", mock.Anything, "ctr-1", ([]string)(nil)).
		Return("sess-1", (<-chan []byte)(outCh), ([]byte)(nil), (<-chan struct{})(doneCh), nil)
	s.terminal.On("DetachSession", mock.Anything, mock.Anything).Return(nil).Maybe()

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ContainerID: "ctr-1"})
	readStatusMsg(s.T(), conn)

	sendControl(s.T(), conn, wsControlMessage{Type: "input", Data: "not-valid-base64!!!"})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "error", msg.Type)
	require.Contains(s.T(), msg.Message, "invalid base64")
	require.Equal(s.T(), wsErrCodeInvalidInput, msg.ErrorCode)

	close(doneCh)
}

func (s *TerminalHandlerSuite) TestSendInputError() {
	outCh := make(chan []byte, 1)
	doneCh := make(chan struct{})
	s.terminal.On("CreateSession", mock.Anything, "ctr-1", ([]string)(nil)).
		Return("sess-1", (<-chan []byte)(outCh), ([]byte)(nil), (<-chan struct{})(doneCh), nil)
	s.terminal.On("SendInput", "sess-1", []byte("x")).Return(errors.New("write failed"))
	s.terminal.On("DetachSession", mock.Anything, mock.Anything).Return(nil).Maybe()

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ContainerID: "ctr-1"})
	readStatusMsg(s.T(), conn)

	sendControl(s.T(), conn, wsControlMessage{Type: "input", Data: base64.StdEncoding.EncodeToString([]byte("x"))})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "error", msg.Type)
	require.Contains(s.T(), msg.Message, "write failed")
	require.Equal(s.T(), wsErrCodeSessionFailed, msg.ErrorCode)

	close(doneCh)
}

func (s *TerminalHandlerSuite) TestResize() {
	outCh := make(chan []byte, 1)
	doneCh := make(chan struct{})
	s.terminal.On("CreateSession", mock.Anything, "ctr-1", ([]string)(nil)).
		Return("sess-1", (<-chan []byte)(outCh), ([]byte)(nil), (<-chan struct{})(doneCh), nil)
	s.terminal.On("Resize", mock.Anything, "sess-1", uint(24), uint(80)).Return(nil)
	s.terminal.On("DetachSession", mock.Anything, mock.Anything).Return(nil).Maybe()

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ContainerID: "ctr-1"})
	readStatusMsg(s.T(), conn)

	sendControl(s.T(), conn, wsControlMessage{Type: "resize", Rows: 24, Cols: 80})

	time.Sleep(50 * time.Millisecond)
	close(doneCh)
	time.Sleep(50 * time.Millisecond)
	s.terminal.AssertCalled(s.T(), "Resize", mock.Anything, "sess-1", uint(24), uint(80))
}

func (s *TerminalHandlerSuite) TestResizeNoSession() {
	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "resize", Rows: 24, Cols: 80})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "error", msg.Type)
	require.Contains(s.T(), msg.Message, "no active session")
	require.Equal(s.T(), wsErrCodeNoSession, msg.ErrorCode)
}

func (s *TerminalHandlerSuite) TestResizeZeroDimensions() {
	outCh := make(chan []byte, 1)
	doneCh := make(chan struct{})
	s.terminal.On("CreateSession", mock.Anything, "ctr-1", ([]string)(nil)).
		Return("sess-1", (<-chan []byte)(outCh), ([]byte)(nil), (<-chan struct{})(doneCh), nil)
	s.terminal.On("DetachSession", mock.Anything, mock.Anything).Return(nil).Maybe()

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ContainerID: "ctr-1"})
	readStatusMsg(s.T(), conn)

	sendControl(s.T(), conn, wsControlMessage{Type: "resize", Rows: 0, Cols: 80})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "error", msg.Type)
	require.Contains(s.T(), msg.Message, "rows and cols required")
	require.Equal(s.T(), wsErrCodeMissingField, msg.ErrorCode)

	close(doneCh)
}

func (s *TerminalHandlerSuite) TestResizeError() {
	outCh := make(chan []byte, 1)
	doneCh := make(chan struct{})
	s.terminal.On("CreateSession", mock.Anything, "ctr-1", ([]string)(nil)).
		Return("sess-1", (<-chan []byte)(outCh), ([]byte)(nil), (<-chan struct{})(doneCh), nil)
	s.terminal.On("Resize", mock.Anything, "sess-1", uint(24), uint(80)).Return(errors.New("resize failed"))
	s.terminal.On("DetachSession", mock.Anything, mock.Anything).Return(nil).Maybe()

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ContainerID: "ctr-1"})
	readStatusMsg(s.T(), conn)

	sendControl(s.T(), conn, wsControlMessage{Type: "resize", Rows: 24, Cols: 80})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "error", msg.Type)
	require.Contains(s.T(), msg.Message, "resize failed")
	require.Equal(s.T(), wsErrCodeSessionFailed, msg.ErrorCode)

	close(doneCh)
}

func (s *TerminalHandlerSuite) TestStopSession() {
	outCh := make(chan []byte, 1)
	doneCh := make(chan struct{})
	s.terminal.On("CreateSession", mock.Anything, "ctr-1", ([]string)(nil)).
		Return("sess-1", (<-chan []byte)(outCh), ([]byte)(nil), (<-chan struct{})(doneCh), nil)
	s.terminal.On("StopSession", "sess-1").Return(nil)
	s.terminal.On("DetachSession", mock.Anything, mock.Anything).Return(nil).Maybe()

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ContainerID: "ctr-1"})
	readStatusMsg(s.T(), conn) // created

	sendControl(s.T(), conn, wsControlMessage{Type: "stop"})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "stopped", msg.Type)

	close(doneCh)
}

func (s *TerminalHandlerSuite) TestStopSessionNoSession() {
	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "stop"})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "error", msg.Type)
	require.Contains(s.T(), msg.Message, "no active session")
	require.Equal(s.T(), wsErrCodeNoSession, msg.ErrorCode)
}

func (s *TerminalHandlerSuite) TestStopSessionError() {
	outCh := make(chan []byte, 1)
	doneCh := make(chan struct{})
	s.terminal.On("CreateSession", mock.Anything, "ctr-1", ([]string)(nil)).
		Return("sess-1", (<-chan []byte)(outCh), ([]byte)(nil), (<-chan struct{})(doneCh), nil)
	s.terminal.On("StopSession", "sess-1").Return(errors.New("stop failed"))
	s.terminal.On("DetachSession", mock.Anything, mock.Anything).Return(nil).Maybe()

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ContainerID: "ctr-1"})
	readStatusMsg(s.T(), conn)

	sendControl(s.T(), conn, wsControlMessage{Type: "stop"})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "error", msg.Type)
	require.Contains(s.T(), msg.Message, "stop failed")
	require.Equal(s.T(), wsErrCodeSessionFailed, msg.ErrorCode)

	close(doneCh)
}

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
