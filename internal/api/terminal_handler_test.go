package api

import (
	"context"
	"encoding/json"
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

func (m *MockTerminalManager) StopSession(sessionID string) (string, error) {
	args := m.Called(sessionID)
	return args.String(0), args.Error(1)
}

func (m *MockTerminalManager) KillProcessGroup(ctx context.Context, sessionID string) error {
	return m.Called(ctx, sessionID).Error(0)
}

type MockInteractiveCmdBuilder struct {
	mock.Mock
}

func (m *MockInteractiveCmdBuilder) BuildInteractiveCmd(channelID, dirPath, parentDirPath, sessionID, agentID string, forkSession bool) string {
	return m.Called(channelID, dirPath, parentDirPath, sessionID, agentID, forkSession).String(0)
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
	// Default DetachSession mock to prevent panics on WS close in all tests.
	s.terminal.On("DetachSession", mock.Anything, mock.Anything).Return(nil).Maybe()
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

// onSendInputCalled sets up a "SendInput" mock expectation that signals the
// returned channel when called. BuildInteractiveCmd + SendInput run
// asynchronously after the "created" status is written to the WebSocket,
// so tests must wait on this channel before asserting expectations.
func onSendInputCalled(m *MockTerminalManager, sessionID string, data []byte) <-chan struct{} {
	ch := make(chan struct{}, 1)
	m.On("SendInput", sessionID, data).Return(nil).Run(func(_ mock.Arguments) {
		ch <- struct{}{}
	})
	return ch
}

func sendControl(t *testing.T, conn *websocket.Conn, msg wsControlMessage) {
	t.Helper()
	data, err := json.Marshal(msg)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, data))
}

// --- Tests ---
