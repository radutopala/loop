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

	"github.com/radutopala/loop/internal/db"
)

type MockTerminalManager struct {
	mock.Mock
}

type MockContainerFinder struct {
	mock.Mock
}

func (m *MockContainerFinder) FindContainerByChannel(ctx context.Context, channelID, dirPath string) (string, error) {
	args := m.Called(ctx, channelID, dirPath)
	return args.String(0), args.Error(1)
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

type MockContainerStopper struct {
	mock.Mock
}

func (m *MockContainerStopper) ContainerRemove(ctx context.Context, containerID string) error {
	return m.Called(ctx, containerID).Error(0)
}

type MockInteractiveCmdBuilder struct {
	mock.Mock
}

func (m *MockInteractiveCmdBuilder) BuildInteractiveCmd(channelID, dirPath, sessionID string, forkSession bool) string {
	return m.Called(channelID, dirPath, sessionID, forkSession).String(0)
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

func (s *TerminalHandlerSuite) TestTerminalNotConfigured() {
	srv := nilServer() // no terminal manager or host manager
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/ws/terminal", srv.handleTerminalWS)

	req := httptest.NewRequest("GET", "/api/ws/terminal", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

func (s *TerminalHandlerSuite) TestTerminalAllowedWithOnlyHostManager() {
	// Should NOT return 501 when only host manager is configured.
	srv := nilServer()
	hostMgr := new(MockTerminalManager)
	srv.SetHostTerminalManager(hostMgr)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/ws/terminal", srv.handleTerminalWS)

	// Non-WebSocket request should return 400 (upgrade error), not 501.
	req := httptest.NewRequest("GET", "/api/ws/terminal", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
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

func (s *TerminalHandlerSuite) TestCreateSessionWithInitialResize() {
	outCh := make(chan []byte, 1)
	doneCh := make(chan struct{})
	s.terminal.On("CreateSession", mock.Anything, "ctr-1", []string{"/bin/bash"}).
		Return("sess-1", (<-chan []byte)(outCh), []byte(nil), (<-chan struct{})(doneCh), nil)
	s.terminal.On("Resize", mock.Anything, "sess-1", uint(40), uint(120)).Return(nil)
	s.terminal.On("DetachSession", mock.Anything, mock.Anything).Return(nil).Maybe()

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ContainerID: "ctr-1", Cmd: []string{"/bin/bash"}, Rows: 40, Cols: 120})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "created", msg.Type)
	require.Equal(s.T(), "sess-1", msg.SessionID)

	time.Sleep(50 * time.Millisecond)
	s.terminal.AssertCalled(s.T(), "Resize", mock.Anything, "sess-1", uint(40), uint(120))
	close(doneCh)
}

func (s *TerminalHandlerSuite) TestCreateSessionWithInitialResizeError() {
	outCh := make(chan []byte, 1)
	doneCh := make(chan struct{})
	s.terminal.On("CreateSession", mock.Anything, "ctr-1", ([]string)(nil)).
		Return("sess-1", (<-chan []byte)(outCh), []byte(nil), (<-chan struct{})(doneCh), nil)
	s.terminal.On("Resize", mock.Anything, "sess-1", uint(30), uint(100)).Return(errors.New("resize failed"))
	s.terminal.On("DetachSession", mock.Anything, mock.Anything).Return(nil).Maybe()

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ContainerID: "ctr-1", Rows: 30, Cols: 100})

	// Session is still created successfully despite initial resize error.
	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "created", msg.Type)
	require.Equal(s.T(), "sess-1", msg.SessionID)

	time.Sleep(50 * time.Millisecond)
	s.terminal.AssertCalled(s.T(), "Resize", mock.Anything, "sess-1", uint(30), uint(100))
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
	require.Contains(s.T(), msg.Message, "container_id or channel_id required")
	require.Equal(s.T(), wsErrCodeMissingField, msg.ErrorCode)
}

func (s *TerminalHandlerSuite) TestCreateSessionWithChannelID() {
	outCh := make(chan []byte, 1)
	doneCh := make(chan struct{})
	s.terminal.On("CreateSession", mock.Anything, "resolved-container-123", []string(nil)).
		Return("sess-new", (<-chan []byte)(outCh), []byte(nil), (<-chan struct{})(doneCh), nil)
	s.terminal.On("DetachSession", mock.Anything, mock.Anything).Return(nil).Maybe()

	finder := new(MockContainerFinder)
	finder.On("FindContainerByChannel", mock.Anything, "ch-42", mock.Anything).Return("resolved-container-123", nil)
	s.srv.SetContainerFinder(finder)

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ChannelID: "ch-42"})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "created", msg.Type)
	require.Equal(s.T(), "sess-new", msg.SessionID)
	finder.AssertExpectations(s.T())
}

func (s *TerminalHandlerSuite) TestCreateSessionWithChannelIDResolvesDirPath() {
	outCh := make(chan []byte, 1)
	doneCh := make(chan struct{})
	s.terminal.On("CreateSession", mock.Anything, "resolved-container-456", []string(nil)).
		Return("sess-dir", (<-chan []byte)(outCh), []byte(nil), (<-chan struct{})(doneCh), nil)
	s.terminal.On("DetachSession", mock.Anything, mock.Anything).Return(nil).Maybe()

	store := new(MockChannelLister)
	store.On("GetChannel", mock.Anything, "ch-proj").
		Return(&db.Channel{ChannelID: "ch-proj", DirPath: "/home/user/dev/loop"}, nil)
	s.srv.store = store

	finder := new(MockContainerFinder)
	finder.On("FindContainerByChannel", mock.Anything, "ch-proj", "/home/user/dev/loop").Return("resolved-container-456", nil)
	s.srv.SetContainerFinder(finder)

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ChannelID: "ch-proj"})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "created", msg.Type)
	require.Equal(s.T(), "sess-dir", msg.SessionID)
	store.AssertExpectations(s.T())
	finder.AssertExpectations(s.T())
}

func (s *TerminalHandlerSuite) TestCreateSessionWithChannelIDNotFound() {
	finder := new(MockContainerFinder)
	finder.On("FindContainerByChannel", mock.Anything, "ch-missing", mock.Anything).
		Return("", errors.New("no container found"))
	s.srv.SetContainerFinder(finder)

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ChannelID: "ch-missing"})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "error", msg.Type)
	require.Contains(s.T(), msg.Message, "no running container")
	require.Equal(s.T(), wsErrCodeSessionFailed, msg.ErrorCode)
}

func (s *TerminalHandlerSuite) TestSetContainerFinder() {
	srv := nilServer()
	require.Nil(s.T(), srv.containerFinder)
	finder := new(MockContainerFinder)
	srv.SetContainerFinder(finder)
	require.NotNil(s.T(), srv.containerFinder)
}

func (s *TerminalHandlerSuite) TestSetContainerStopper() {
	srv := nilServer()
	require.Nil(s.T(), srv.containerStopper)
	stopper := new(MockContainerStopper)
	srv.SetContainerStopper(stopper)
	require.NotNil(s.T(), srv.containerStopper)
}

func (s *TerminalHandlerSuite) TestSetInteractiveCmdBuilder() {
	srv := nilServer()
	require.Nil(s.T(), srv.cmdBuilder)
	builder := new(MockInteractiveCmdBuilder)
	srv.SetInteractiveCmdBuilder(builder)
	require.NotNil(s.T(), srv.cmdBuilder)
}

func (s *TerminalHandlerSuite) TestCreateSessionSendsInteractiveCmd() {
	outCh := make(chan []byte, 1)
	doneCh := make(chan struct{})
	s.terminal.On("CreateSession", mock.Anything, "resolved-ctr", []string(nil)).
		Return("sess-claude", (<-chan []byte)(outCh), []byte(nil), (<-chan struct{})(doneCh), nil)
	inputSent := onSendInputCalled(s.terminal, "sess-claude", []byte("claude --dangerously-skip-permissions\n"))
	s.terminal.On("DetachSession", mock.Anything, mock.Anything).Return(nil).Maybe()

	finder := new(MockContainerFinder)
	finder.On("FindContainerByChannel", mock.Anything, "ch-99", mock.Anything).Return("resolved-ctr", nil)
	s.srv.SetContainerFinder(finder)

	builder := new(MockInteractiveCmdBuilder)
	builder.On("BuildInteractiveCmd", "ch-99", "", "", false).Return("claude --dangerously-skip-permissions")
	s.srv.SetInteractiveCmdBuilder(builder)

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ChannelID: "ch-99"})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "created", msg.Type)
	require.Equal(s.T(), "sess-claude", msg.SessionID)
	select {
	case <-inputSent:
	case <-time.After(time.Second):
		s.T().Fatal("timed out waiting for SendInput")
	}
	builder.AssertExpectations(s.T())
}

func (s *TerminalHandlerSuite) TestCreateSessionSendsInteractiveCmdWithDirPath() {
	outCh := make(chan []byte, 1)
	doneCh := make(chan struct{})
	s.terminal.On("CreateSession", mock.Anything, "resolved-ctr-dir", []string(nil)).
		Return("sess-dir", (<-chan []byte)(outCh), []byte(nil), (<-chan struct{})(doneCh), nil)
	inputSent := onSendInputCalled(s.terminal, "sess-dir", []byte("claude --mcp-config /projects/app/.loop/mcp-ch-dir.json\n"))
	s.terminal.On("DetachSession", mock.Anything, mock.Anything).Return(nil).Maybe()

	store := new(MockChannelLister)
	store.On("GetChannel", mock.Anything, "ch-dir").
		Return(&db.Channel{ChannelID: "ch-dir", DirPath: "/projects/app"}, nil)
	s.srv.store = store

	finder := new(MockContainerFinder)
	finder.On("FindContainerByChannel", mock.Anything, "ch-dir", "/projects/app").Return("resolved-ctr-dir", nil)
	s.srv.SetContainerFinder(finder)

	builder := new(MockInteractiveCmdBuilder)
	builder.On("BuildInteractiveCmd", "ch-dir", "/projects/app", "", false).Return("claude --mcp-config /projects/app/.loop/mcp-ch-dir.json")
	s.srv.SetInteractiveCmdBuilder(builder)

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ChannelID: "ch-dir"})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "created", msg.Type)
	require.Equal(s.T(), "sess-dir", msg.SessionID)
	select {
	case <-inputSent:
	case <-time.After(time.Second):
		s.T().Fatal("timed out waiting for SendInput")
	}
	builder.AssertExpectations(s.T())
}

func (s *TerminalHandlerSuite) TestCreateSessionResumesChannelSession() {
	outCh := make(chan []byte, 1)
	doneCh := make(chan struct{})
	s.terminal.On("CreateSession", mock.Anything, "resolved-ctr", []string(nil)).
		Return("sess-resume", (<-chan []byte)(outCh), []byte(nil), (<-chan struct{})(doneCh), nil)
	inputSent := onSendInputCalled(s.terminal, "sess-resume", []byte("claude --dangerously-skip-permissions --resume sess-existing\n"))
	s.terminal.On("DetachSession", mock.Anything, mock.Anything).Return(nil).Maybe()

	store := new(MockChannelLister)
	store.On("GetChannel", mock.Anything, "ch-resume").
		Return(&db.Channel{ChannelID: "ch-resume", SessionID: "sess-existing"}, nil)
	s.srv.store = store

	finder := new(MockContainerFinder)
	finder.On("FindContainerByChannel", mock.Anything, "ch-resume", mock.Anything).Return("resolved-ctr", nil)
	s.srv.SetContainerFinder(finder)

	builder := new(MockInteractiveCmdBuilder)
	builder.On("BuildInteractiveCmd", "ch-resume", "", "sess-existing", false).
		Return("claude --dangerously-skip-permissions --resume sess-existing")
	s.srv.SetInteractiveCmdBuilder(builder)

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ChannelID: "ch-resume"})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "created", msg.Type)
	select {
	case <-inputSent:
	case <-time.After(time.Second):
		s.T().Fatal("timed out waiting for SendInput")
	}
	builder.AssertExpectations(s.T())
}

func (s *TerminalHandlerSuite) TestCreateSessionForksThreadFromParent() {
	outCh := make(chan []byte, 1)
	doneCh := make(chan struct{})
	s.terminal.On("CreateSession", mock.Anything, "resolved-ctr", []string(nil)).
		Return("sess-fork", (<-chan []byte)(outCh), []byte(nil), (<-chan struct{})(doneCh), nil)
	inputSent := onSendInputCalled(s.terminal, "sess-fork", []byte("claude --dangerously-skip-permissions --resume sess-parent --fork-session\n"))
	s.terminal.On("DetachSession", mock.Anything, mock.Anything).Return(nil).Maybe()

	store := new(MockChannelLister)
	// Thread inherited the parent's session ID at creation time.
	store.On("GetChannel", mock.Anything, "thread-1").
		Return(&db.Channel{ChannelID: "thread-1", ParentID: "ch-parent", SessionID: "sess-parent"}, nil)
	// Parent channel has the same session ID — should fork.
	store.On("GetChannel", mock.Anything, "ch-parent").
		Return(&db.Channel{ChannelID: "ch-parent", SessionID: "sess-parent"}, nil)
	s.srv.store = store

	finder := new(MockContainerFinder)
	finder.On("FindContainerByChannel", mock.Anything, "thread-1", mock.Anything).Return("resolved-ctr", nil)
	s.srv.SetContainerFinder(finder)

	builder := new(MockInteractiveCmdBuilder)
	builder.On("BuildInteractiveCmd", "thread-1", "", "sess-parent", true).
		Return("claude --dangerously-skip-permissions --resume sess-parent --fork-session")
	s.srv.SetInteractiveCmdBuilder(builder)

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ChannelID: "thread-1"})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "created", msg.Type)
	select {
	case <-inputSent:
	case <-time.After(time.Second):
		s.T().Fatal("timed out waiting for SendInput")
	}
	builder.AssertExpectations(s.T())
	store.AssertCalled(s.T(), "GetChannel", mock.Anything, "ch-parent")
}

func (s *TerminalHandlerSuite) TestCreateSessionThreadWithOwnSession() {
	outCh := make(chan []byte, 1)
	doneCh := make(chan struct{})
	s.terminal.On("CreateSession", mock.Anything, "resolved-ctr", []string(nil)).
		Return("sess-thread", (<-chan []byte)(outCh), []byte(nil), (<-chan struct{})(doneCh), nil)
	inputSent := onSendInputCalled(s.terminal, "sess-thread", []byte("claude --dangerously-skip-permissions --resume sess-thread-own\n"))
	s.terminal.On("DetachSession", mock.Anything, mock.Anything).Return(nil).Maybe()

	store := new(MockChannelLister)
	// Thread already has its own session ID (was forked previously).
	store.On("GetChannel", mock.Anything, "thread-2").
		Return(&db.Channel{ChannelID: "thread-2", ParentID: "ch-parent", SessionID: "sess-thread-own"}, nil)
	// Parent has a different session — thread's session diverged after fork.
	store.On("GetChannel", mock.Anything, "ch-parent").
		Return(&db.Channel{ChannelID: "ch-parent", SessionID: "sess-parent"}, nil)
	s.srv.store = store

	finder := new(MockContainerFinder)
	finder.On("FindContainerByChannel", mock.Anything, "thread-2", mock.Anything).Return("resolved-ctr", nil)
	s.srv.SetContainerFinder(finder)

	builder := new(MockInteractiveCmdBuilder)
	// Has its own session — uses resume, not fork.
	builder.On("BuildInteractiveCmd", "thread-2", "", "sess-thread-own", false).
		Return("claude --dangerously-skip-permissions --resume sess-thread-own")
	s.srv.SetInteractiveCmdBuilder(builder)

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ChannelID: "thread-2"})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "created", msg.Type)
	select {
	case <-inputSent:
	case <-time.After(time.Second):
		s.T().Fatal("timed out waiting for SendInput")
	}
	builder.AssertExpectations(s.T())
}

func (s *TerminalHandlerSuite) TestCreateSessionInteractiveCmdSendInputError() {
	outCh := make(chan []byte, 1)
	doneCh := make(chan struct{})
	s.terminal.On("CreateSession", mock.Anything, "resolved-ctr", []string(nil)).
		Return("sess-err", (<-chan []byte)(outCh), []byte(nil), (<-chan struct{})(doneCh), nil)
	s.terminal.On("SendInput", "sess-err", mock.Anything).Return(errors.New("write failed"))
	s.terminal.On("DetachSession", mock.Anything, mock.Anything).Return(nil).Maybe()

	finder := new(MockContainerFinder)
	finder.On("FindContainerByChannel", mock.Anything, "ch-err", mock.Anything).Return("resolved-ctr", nil)
	s.srv.SetContainerFinder(finder)

	builder := new(MockInteractiveCmdBuilder)
	builder.On("BuildInteractiveCmd", "ch-err", "", "", false).Return("claude --dangerously-skip-permissions")
	s.srv.SetInteractiveCmdBuilder(builder)

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ChannelID: "ch-err"})

	// Session is still created successfully despite SendInput error.
	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "created", msg.Type)
	require.Equal(s.T(), "sess-err", msg.SessionID)
}

func (s *TerminalHandlerSuite) TestCreateSessionExplicitCmdSkipsInteractiveCmd() {
	outCh := make(chan []byte, 1)
	doneCh := make(chan struct{})
	explicitCmd := []string{"/bin/bash"}
	s.terminal.On("CreateSession", mock.Anything, "resolved-ctr", explicitCmd).
		Return("sess-bash", (<-chan []byte)(outCh), []byte(nil), (<-chan struct{})(doneCh), nil)
	s.terminal.On("DetachSession", mock.Anything, mock.Anything).Return(nil).Maybe()

	finder := new(MockContainerFinder)
	finder.On("FindContainerByChannel", mock.Anything, "ch-explicit", mock.Anything).Return("resolved-ctr", nil)
	s.srv.SetContainerFinder(finder)

	builder := new(MockInteractiveCmdBuilder)
	s.srv.SetInteractiveCmdBuilder(builder)

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ChannelID: "ch-explicit", Cmd: explicitCmd})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "created", msg.Type)
	builder.AssertNotCalled(s.T(), "BuildInteractiveCmd", mock.Anything, mock.Anything, mock.Anything)
	s.terminal.AssertNotCalled(s.T(), "SendInput", mock.Anything, mock.Anything)
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
	s.terminal.On("StopSession", "sess-1").Return("ctr-1", nil)

	stopper := new(MockContainerStopper)
	stopper.On("ContainerRemove", mock.Anything, "ctr-1").Return(nil)
	s.srv.SetContainerStopper(stopper)

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ContainerID: "ctr-1"})
	readStatusMsg(s.T(), conn) // created

	sendControl(s.T(), conn, wsControlMessage{Type: "stop"})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "stopped", msg.Type)

	// Allow goroutine cleanup.
	time.Sleep(50 * time.Millisecond)
	stopper.AssertCalled(s.T(), "ContainerRemove", mock.Anything, "ctr-1")

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
	s.terminal.On("StopSession", "sess-1").Return("", errors.New("stop failed"))

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

func (s *TerminalHandlerSuite) TestStopSessionContainerRemoveError() {
	outCh := make(chan []byte, 1)
	doneCh := make(chan struct{})
	s.terminal.On("CreateSession", mock.Anything, "ctr-1", ([]string)(nil)).
		Return("sess-1", (<-chan []byte)(outCh), ([]byte)(nil), (<-chan struct{})(doneCh), nil)
	s.terminal.On("StopSession", "sess-1").Return("ctr-1", nil)

	stopper := new(MockContainerStopper)
	stopper.On("ContainerRemove", mock.Anything, "ctr-1").Return(errors.New("remove failed"))
	s.srv.SetContainerStopper(stopper)

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ContainerID: "ctr-1"})
	readStatusMsg(s.T(), conn)

	sendControl(s.T(), conn, wsControlMessage{Type: "stop"})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "stopped", msg.Type)

	time.Sleep(50 * time.Millisecond)
	stopper.AssertCalled(s.T(), "ContainerRemove", mock.Anything, "ctr-1")

	close(doneCh)
}

func (s *TerminalHandlerSuite) TestCloseSession() {
	outCh := make(chan []byte, 1)
	doneCh := make(chan struct{})
	s.terminal.On("CreateSession", mock.Anything, "ctr-1", ([]string)(nil)).
		Return("sess-1", (<-chan []byte)(outCh), ([]byte)(nil), (<-chan struct{})(doneCh), nil)
	s.terminal.On("StopSession", "sess-1").Return("ctr-1", nil)

	stopper := new(MockContainerStopper)
	s.srv.SetContainerStopper(stopper)

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ContainerID: "ctr-1"})
	readStatusMsg(s.T(), conn) // created

	sendControl(s.T(), conn, wsControlMessage{Type: "close"})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "stopped", msg.Type)

	// ContainerRemove should NOT be called for close (unlike stop).
	time.Sleep(50 * time.Millisecond)
	stopper.AssertNotCalled(s.T(), "ContainerRemove", mock.Anything, mock.Anything)

	close(doneCh)
}

func (s *TerminalHandlerSuite) TestCloseSessionNoSession() {
	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "close"})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "error", msg.Type)
	require.Contains(s.T(), msg.Message, "no active session")
	require.Equal(s.T(), wsErrCodeNoSession, msg.ErrorCode)
}

func (s *TerminalHandlerSuite) TestCloseSessionError() {
	outCh := make(chan []byte, 1)
	doneCh := make(chan struct{})
	s.terminal.On("CreateSession", mock.Anything, "ctr-1", ([]string)(nil)).
		Return("sess-1", (<-chan []byte)(outCh), ([]byte)(nil), (<-chan struct{})(doneCh), nil)
	s.terminal.On("StopSession", "sess-1").Return("", errors.New("close failed"))

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ContainerID: "ctr-1"})
	readStatusMsg(s.T(), conn)

	sendControl(s.T(), conn, wsControlMessage{Type: "close"})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "error", msg.Type)
	require.Contains(s.T(), msg.Message, "close failed")
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

// --- Host terminal tests ---

func (s *TerminalHandlerSuite) TestSetHostTerminalManager() {
	srv := nilServer()
	require.Nil(s.T(), srv.hostTermManager)
	mgr := new(MockTerminalManager)
	srv.SetHostTerminalManager(mgr)
	require.NotNil(s.T(), srv.hostTermManager)
}

func (s *TerminalHandlerSuite) TestCreateHostSession() {
	hostMgr := new(MockTerminalManager)
	s.srv.SetHostTerminalManager(hostMgr)

	outCh := make(chan []byte, 1)
	doneCh := make(chan struct{})
	hostMgr.On("CreateSession", mock.Anything, mock.AnythingOfType("string"), []string(nil)).
		Return("host-sess-1", (<-chan []byte)(outCh), []byte(nil), (<-chan struct{})(doneCh), nil)
	hostMgr.On("DetachSession", mock.Anything, mock.Anything).Return(nil).Maybe()

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ChannelID: "ch-1", Target: "host"})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "created", msg.Type)
	require.Equal(s.T(), "host-sess-1", msg.SessionID)

	// Agent manager should not have been called.
	s.terminal.AssertNotCalled(s.T(), "CreateSession", mock.Anything, mock.Anything, mock.Anything)
	close(doneCh)
}

func (s *TerminalHandlerSuite) TestCreateHostSessionWithDirPath() {
	hostMgr := new(MockTerminalManager)
	s.srv.SetHostTerminalManager(hostMgr)

	store := new(MockChannelLister)
	store.On("GetChannel", mock.Anything, "ch-proj").
		Return(&db.Channel{ChannelID: "ch-proj", DirPath: "/home/user/projects"}, nil)
	s.srv.store = store

	outCh := make(chan []byte, 1)
	doneCh := make(chan struct{})
	hostMgr.On("CreateSession", mock.Anything, "/home/user/projects", []string(nil)).
		Return("host-sess-2", (<-chan []byte)(outCh), []byte(nil), (<-chan struct{})(doneCh), nil)
	hostMgr.On("DetachSession", mock.Anything, mock.Anything).Return(nil).Maybe()

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ChannelID: "ch-proj", Target: "host"})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "created", msg.Type)
	require.Equal(s.T(), "host-sess-2", msg.SessionID)
	store.AssertExpectations(s.T())
	close(doneCh)
}

func (s *TerminalHandlerSuite) TestCreateHostSessionThreadInheritsParentDirPath() {
	hostMgr := new(MockTerminalManager)
	s.srv.SetHostTerminalManager(hostMgr)

	store := new(MockChannelLister)
	// Thread has no dir_path, parent has one.
	store.On("GetChannel", mock.Anything, "thread-1").
		Return(&db.Channel{ChannelID: "thread-1", ParentID: "ch-parent"}, nil)
	store.On("GetChannel", mock.Anything, "ch-parent").
		Return(&db.Channel{ChannelID: "ch-parent", DirPath: "/home/user/project"}, nil)
	s.srv.store = store

	outCh := make(chan []byte, 1)
	doneCh := make(chan struct{})
	hostMgr.On("CreateSession", mock.Anything, "/home/user/project", []string(nil)).
		Return("host-thread-sess", (<-chan []byte)(outCh), []byte(nil), (<-chan struct{})(doneCh), nil)
	hostMgr.On("DetachSession", mock.Anything, mock.Anything).Return(nil).Maybe()

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ChannelID: "thread-1", Target: "host"})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "created", msg.Type)
	require.Equal(s.T(), "host-thread-sess", msg.SessionID)
	store.AssertExpectations(s.T())
	close(doneCh)
}

func (s *TerminalHandlerSuite) TestCreateHostSessionError() {
	hostMgr := new(MockTerminalManager)
	s.srv.SetHostTerminalManager(hostMgr)

	hostMgr.On("CreateSession", mock.Anything, mock.AnythingOfType("string"), []string(nil)).
		Return("", nil, nil, nil, errors.New("shell failed"))

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ChannelID: "ch-1", Target: "host"})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "error", msg.Type)
	require.Contains(s.T(), msg.Message, "shell failed")
}

func (s *TerminalHandlerSuite) TestCreateHostSessionNotConfigured() {
	// No host manager configured.
	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ChannelID: "ch-1", Target: "host"})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "error", msg.Type)
	require.Contains(s.T(), msg.Message, "host terminal not configured")
}

func (s *TerminalHandlerSuite) TestCreateHostSessionNoAutoCmd() {
	// Host sessions should NOT send interactive Claude command.
	hostMgr := new(MockTerminalManager)
	s.srv.SetHostTerminalManager(hostMgr)

	builder := new(MockInteractiveCmdBuilder)
	s.srv.SetInteractiveCmdBuilder(builder)

	outCh := make(chan []byte, 1)
	doneCh := make(chan struct{})
	hostMgr.On("CreateSession", mock.Anything, mock.AnythingOfType("string"), []string(nil)).
		Return("host-sess-3", (<-chan []byte)(outCh), []byte(nil), (<-chan struct{})(doneCh), nil)
	hostMgr.On("DetachSession", mock.Anything, mock.Anything).Return(nil).Maybe()

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ChannelID: "ch-1", Target: "host"})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "created", msg.Type)

	time.Sleep(50 * time.Millisecond)
	builder.AssertNotCalled(s.T(), "BuildInteractiveCmd", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	hostMgr.AssertNotCalled(s.T(), "SendInput", mock.Anything, mock.Anything)
	close(doneCh)
}

func (s *TerminalHandlerSuite) TestCreateHostSessionWithResize() {
	hostMgr := new(MockTerminalManager)
	s.srv.SetHostTerminalManager(hostMgr)

	outCh := make(chan []byte, 1)
	doneCh := make(chan struct{})
	hostMgr.On("CreateSession", mock.Anything, mock.AnythingOfType("string"), []string(nil)).
		Return("host-sess-4", (<-chan []byte)(outCh), []byte(nil), (<-chan struct{})(doneCh), nil)
	hostMgr.On("Resize", mock.Anything, "host-sess-4", uint(40), uint(120)).Return(nil)
	hostMgr.On("DetachSession", mock.Anything, mock.Anything).Return(nil).Maybe()

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ChannelID: "ch-1", Target: "host", Rows: 40, Cols: 120})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "created", msg.Type)

	time.Sleep(50 * time.Millisecond)
	hostMgr.AssertCalled(s.T(), "Resize", mock.Anything, "host-sess-4", uint(40), uint(120))
	close(doneCh)
}

func (s *TerminalHandlerSuite) TestHostSessionInputUsesHostManager() {
	hostMgr := new(MockTerminalManager)
	s.srv.SetHostTerminalManager(hostMgr)

	outCh := make(chan []byte, 1)
	doneCh := make(chan struct{})
	hostMgr.On("CreateSession", mock.Anything, mock.AnythingOfType("string"), []string(nil)).
		Return("host-sess-5", (<-chan []byte)(outCh), []byte(nil), (<-chan struct{})(doneCh), nil)
	hostMgr.On("SendInput", "host-sess-5", []byte("ls\n")).Return(nil)
	hostMgr.On("DetachSession", mock.Anything, mock.Anything).Return(nil).Maybe()

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ChannelID: "ch-1", Target: "host"})
	readStatusMsg(s.T(), conn)

	encoded := base64.StdEncoding.EncodeToString([]byte("ls\n"))
	sendControl(s.T(), conn, wsControlMessage{Type: "input", Data: encoded})

	time.Sleep(50 * time.Millisecond)
	hostMgr.AssertCalled(s.T(), "SendInput", "host-sess-5", []byte("ls\n"))
	s.terminal.AssertNotCalled(s.T(), "SendInput", mock.Anything, mock.Anything)
	close(doneCh)
}

func (s *TerminalHandlerSuite) TestHostSessionResizeUsesHostManager() {
	hostMgr := new(MockTerminalManager)
	s.srv.SetHostTerminalManager(hostMgr)

	outCh := make(chan []byte, 1)
	doneCh := make(chan struct{})
	hostMgr.On("CreateSession", mock.Anything, mock.AnythingOfType("string"), []string(nil)).
		Return("host-sess-6", (<-chan []byte)(outCh), []byte(nil), (<-chan struct{})(doneCh), nil)
	hostMgr.On("Resize", mock.Anything, "host-sess-6", uint(30), uint(100)).Return(nil)
	hostMgr.On("DetachSession", mock.Anything, mock.Anything).Return(nil).Maybe()

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ChannelID: "ch-1", Target: "host"})
	readStatusMsg(s.T(), conn)

	sendControl(s.T(), conn, wsControlMessage{Type: "resize", Rows: 30, Cols: 100})

	time.Sleep(50 * time.Millisecond)
	hostMgr.AssertCalled(s.T(), "Resize", mock.Anything, "host-sess-6", uint(30), uint(100))
	close(doneCh)
}

func (s *TerminalHandlerSuite) TestHostSessionStopSkipsContainerRemove() {
	hostMgr := new(MockTerminalManager)
	s.srv.SetHostTerminalManager(hostMgr)

	outCh := make(chan []byte, 1)
	doneCh := make(chan struct{})
	hostMgr.On("CreateSession", mock.Anything, mock.AnythingOfType("string"), []string(nil)).
		Return("host-sess-7", (<-chan []byte)(outCh), []byte(nil), (<-chan struct{})(doneCh), nil)
	hostMgr.On("StopSession", "host-sess-7").Return("", nil)

	stopper := new(MockContainerStopper)
	s.srv.SetContainerStopper(stopper)

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ChannelID: "ch-1", Target: "host"})
	readStatusMsg(s.T(), conn)

	sendControl(s.T(), conn, wsControlMessage{Type: "stop"})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "stopped", msg.Type)

	time.Sleep(50 * time.Millisecond)
	stopper.AssertNotCalled(s.T(), "ContainerRemove", mock.Anything, mock.Anything)
	close(doneCh)
}

func (s *TerminalHandlerSuite) TestAttachHostSession() {
	// When attaching, should try agent manager first, then host manager.
	hostMgr := new(MockTerminalManager)
	s.srv.SetHostTerminalManager(hostMgr)

	outCh := make(chan []byte, 1)
	doneCh := make(chan struct{})
	// Agent manager fails.
	s.terminal.On("AttachSession", "host-sess-8").
		Return(nil, nil, nil, errors.New("session not found"))
	// Host manager succeeds.
	hostMgr.On("AttachSession", "host-sess-8").
		Return((<-chan []byte)(outCh), []byte("host output"), (<-chan struct{})(doneCh), nil)
	hostMgr.On("DetachSession", mock.Anything, mock.Anything).Return(nil).Maybe()

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "attach", SessionID: "host-sess-8"})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "attached", msg.Type)
	require.Equal(s.T(), "host-sess-8", msg.SessionID)

	data := readBinaryMsg(s.T(), conn)
	require.Equal(s.T(), []byte("host output"), data)

	close(doneCh)
}

func (s *TerminalHandlerSuite) TestAttachWithNilAgentManager() {
	// When agent manager is nil, should use host manager directly.
	s.srv.termManager = nil
	hostMgr := new(MockTerminalManager)
	s.srv.SetHostTerminalManager(hostMgr)

	outCh := make(chan []byte, 1)
	doneCh := make(chan struct{})
	hostMgr.On("AttachSession", "host-sess-9").
		Return((<-chan []byte)(outCh), []byte(nil), (<-chan struct{})(doneCh), nil)
	hostMgr.On("DetachSession", mock.Anything, mock.Anything).Return(nil).Maybe()

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "attach", SessionID: "host-sess-9"})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "attached", msg.Type)
	close(doneCh)
}

func (s *TerminalHandlerSuite) TestCreateHostSessionEmptyCmdArg() {
	hostMgr := new(MockTerminalManager)
	s.srv.SetHostTerminalManager(hostMgr)

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ChannelID: "ch-1", Target: "host", Cmd: []string{"/bin/sh", ""}})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "error", msg.Type)
	require.Contains(s.T(), msg.Message, "cmd contains empty argument")
}

func (s *TerminalHandlerSuite) TestCreateHostSessionTooManyCmdArgs() {
	hostMgr := new(MockTerminalManager)
	s.srv.SetHostTerminalManager(hostMgr)

	args := make([]string, maxCmdArgs+1)
	for i := range args {
		args[i] = "arg"
	}

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ChannelID: "ch-1", Target: "host", Cmd: args})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "error", msg.Type)
	require.Contains(s.T(), msg.Message, "cmd exceeds maximum arguments")
}

func (s *TerminalHandlerSuite) TestCreateHostSessionInitialResizeError() {
	hostMgr := new(MockTerminalManager)
	s.srv.SetHostTerminalManager(hostMgr)

	outCh := make(chan []byte, 1)
	doneCh := make(chan struct{})
	hostMgr.On("CreateSession", mock.Anything, mock.AnythingOfType("string"), []string(nil)).
		Return("host-sess-re", (<-chan []byte)(outCh), []byte(nil), (<-chan struct{})(doneCh), nil)
	hostMgr.On("Resize", mock.Anything, "host-sess-re", uint(30), uint(100)).Return(errors.New("resize failed"))
	hostMgr.On("DetachSession", mock.Anything, mock.Anything).Return(nil).Maybe()

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ChannelID: "ch-1", Target: "host", Rows: 30, Cols: 100})

	// Session is still created successfully despite initial resize error.
	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "created", msg.Type)
	close(doneCh)
}

func (s *TerminalHandlerSuite) TestStopSessionByExplicitIDHost() {
	hostMgr := new(MockTerminalManager)
	s.srv.SetHostTerminalManager(hostMgr)

	outCh := make(chan []byte, 1)
	doneCh := make(chan struct{})
	hostMgr.On("CreateSession", mock.Anything, mock.AnythingOfType("string"), []string(nil)).
		Return("host-sess-10", (<-chan []byte)(outCh), []byte(nil), (<-chan struct{})(doneCh), nil)
	hostMgr.On("StopSession", "host-sess-10").Return("", nil)
	hostMgr.On("DetachSession", mock.Anything, mock.Anything).Return(nil).Maybe()

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ChannelID: "ch-1", Target: "host"})
	readStatusMsg(s.T(), conn)

	sendControl(s.T(), conn, wsControlMessage{Type: "stop"})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "stopped", msg.Type)
	close(doneCh)
}

// --- Kill (container removal by channel_id, no session required) ---

func (s *TerminalHandlerSuite) TestKillRemovesContainer() {
	store := new(MockChannelLister)
	store.On("GetChannel", mock.Anything, "ch-kill").
		Return(&db.Channel{ChannelID: "ch-kill", DirPath: "/projects/app"}, nil)
	s.srv.store = store

	finder := new(MockContainerFinder)
	finder.On("FindContainerByChannel", mock.Anything, "ch-kill", "/projects/app").Return("ctr-kill", nil)
	s.srv.SetContainerFinder(finder)

	stopper := new(MockContainerStopper)
	stopper.On("ContainerRemove", mock.Anything, "ctr-kill").Return(nil)
	s.srv.SetContainerStopper(stopper)

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "kill", ChannelID: "ch-kill"})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "stopped", msg.Type)

	finder.AssertCalled(s.T(), "FindContainerByChannel", mock.Anything, "ch-kill", "/projects/app")
	stopper.AssertCalled(s.T(), "ContainerRemove", mock.Anything, "ctr-kill")
}

func (s *TerminalHandlerSuite) TestKillAlsoStopsBrowser() {
	store := new(MockChannelLister)
	store.On("GetChannel", mock.Anything, "ch-kill-br").
		Return(&db.Channel{ChannelID: "ch-kill-br", DirPath: "/projects/app"}, nil)
	s.srv.store = store

	finder := new(MockContainerFinder)
	finder.On("FindContainerByChannel", mock.Anything, "ch-kill-br", "/projects/app").Return("ctr-kill", nil)
	s.srv.SetContainerFinder(finder)

	stopper := new(MockContainerStopper)
	stopper.On("ContainerRemove", mock.Anything, "ctr-kill").Return(nil)
	s.srv.SetContainerStopper(stopper)

	browserMgr := new(mockBrowserProvider)
	browserMgr.On("StopBrowser", mock.Anything, "ch-kill-br").Return(nil)
	s.srv.SetBrowserProvider(browserMgr)

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "kill", ChannelID: "ch-kill-br"})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "stopped", msg.Type)

	browserMgr.AssertCalled(s.T(), "StopBrowser", mock.Anything, "ch-kill-br")
}

func (s *TerminalHandlerSuite) TestKillNoContainerFound() {
	finder := new(MockContainerFinder)
	finder.On("FindContainerByChannel", mock.Anything, "ch-gone", "").
		Return("", errors.New("not found"))
	s.srv.SetContainerFinder(finder)

	stopper := new(MockContainerStopper)
	s.srv.SetContainerStopper(stopper)

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "kill", ChannelID: "ch-gone"})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "stopped", msg.Type)

	stopper.AssertNotCalled(s.T(), "ContainerRemove", mock.Anything, mock.Anything)
}

func (s *TerminalHandlerSuite) TestKillMissingChannelID() {
	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "kill"})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "error", msg.Type)
	require.Contains(s.T(), msg.Message, "channel_id required")
	require.Equal(s.T(), wsErrCodeMissingField, msg.ErrorCode)
}

func (s *TerminalHandlerSuite) TestKillWithActiveSession() {
	outCh := make(chan []byte, 1)
	doneCh := make(chan struct{})
	s.terminal.On("CreateSession", mock.Anything, "ctr-1", ([]string)(nil)).
		Return("sess-1", (<-chan []byte)(outCh), ([]byte)(nil), (<-chan struct{})(doneCh), nil)
	s.terminal.On("StopSession", "sess-1").Return("ctr-1", nil)

	store := new(MockChannelLister)
	store.On("GetChannel", mock.Anything, "ch-active").
		Return(&db.Channel{ChannelID: "ch-active"}, nil)
	s.srv.store = store

	finder := new(MockContainerFinder)
	finder.On("FindContainerByChannel", mock.Anything, "ch-active", "").Return("ctr-1", nil)
	s.srv.SetContainerFinder(finder)

	stopper := new(MockContainerStopper)
	stopper.On("ContainerRemove", mock.Anything, "ctr-1").Return(nil)
	s.srv.SetContainerStopper(stopper)

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ContainerID: "ctr-1"})
	readStatusMsg(s.T(), conn) // created

	sendControl(s.T(), conn, wsControlMessage{Type: "kill", ChannelID: "ch-active"})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "stopped", msg.Type)

	s.terminal.AssertCalled(s.T(), "StopSession", "sess-1")
	stopper.AssertCalled(s.T(), "ContainerRemove", mock.Anything, "ctr-1")

	close(doneCh)
}

func (s *TerminalHandlerSuite) TestHostSessionDetachedOnDisconnect() {
	// Host shell sessions should be detached (not killed) when the WS disconnects,
	// so they can be reattached later.
	hostMgr := new(MockTerminalManager)
	s.srv.SetHostTerminalManager(hostMgr)

	outCh := make(chan []byte, 64)
	doneCh := make(chan struct{})
	hostMgr.On("CreateSession", mock.Anything, mock.AnythingOfType("string"), []string(nil)).
		Return("host-sess-dc", (<-chan []byte)(outCh), []byte(nil), (<-chan struct{})(doneCh), nil)
	hostMgr.On("DetachSession", "host-sess-dc", mock.Anything).Return(nil)

	conn, ts := s.dialWS()
	defer ts.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ChannelID: "ch-1", Target: "host"})
	readStatusMsg(s.T(), conn) // created

	// Close the WS — should trigger DetachSession for host, not StopSession.
	conn.Close()
	time.Sleep(100 * time.Millisecond)

	hostMgr.AssertCalled(s.T(), "DetachSession", "host-sess-dc", mock.Anything)
	hostMgr.AssertNotCalled(s.T(), "StopSession", mock.Anything)

	close(doneCh)
}

func (s *TerminalHandlerSuite) TestAgentSessionDetachedOnDisconnect() {
	// Agent sessions should only be detached (not stopped) on WS disconnect —
	// container lifecycle is managed separately.
	outCh := make(chan []byte, 64)
	doneCh := make(chan struct{})
	s.terminal.On("CreateSession", mock.Anything, "ctr-1", ([]string)(nil)).
		Return("sess-1", (<-chan []byte)(outCh), ([]byte)(nil), (<-chan struct{})(doneCh), nil)
	s.terminal.On("DetachSession", "sess-1", mock.Anything).Return(nil)

	conn, ts := s.dialWS()
	defer ts.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ContainerID: "ctr-1"})
	readStatusMsg(s.T(), conn) // created

	// Close the WS — agent sessions should be detached, not stopped.
	conn.Close()
	time.Sleep(100 * time.Millisecond)

	s.terminal.AssertCalled(s.T(), "DetachSession", "sess-1", mock.Anything)
	s.terminal.AssertNotCalled(s.T(), "StopSession", mock.Anything)

	close(doneCh)
}

func (s *TerminalHandlerSuite) TestHostCreateParentFallbackLoopDir() {
	// When the channel is a thread and parent lookup fails, should fall back to loopDir.
	hostMgr := new(MockTerminalManager)
	s.srv.SetHostTerminalManager(hostMgr)
	s.srv.loopDir = "/tmp/loop-test"

	store := new(MockChannelLister)
	store.On("GetChannel", mock.Anything, "ch-thread").
		Return(&db.Channel{ChannelID: "ch-thread", ParentID: "ch-parent"}, nil)
	store.On("GetChannel", mock.Anything, "ch-parent").
		Return(nil, errors.New("not found"))
	s.srv.store = store

	outCh := make(chan []byte, 1)
	doneCh := make(chan struct{})
	hostMgr.On("CreateSession", mock.Anything, "/tmp/loop-test/ch-thread/work", []string(nil)).
		Return("host-fallback-1", (<-chan []byte)(outCh), []byte(nil), (<-chan struct{})(doneCh), nil)
	hostMgr.On("DetachSession", mock.Anything, mock.Anything).Return(nil).Maybe()

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ChannelID: "ch-thread", Target: "host"})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "created", msg.Type)
	close(doneCh)
}

func (s *TerminalHandlerSuite) TestHostCreateLoopDirFallback() {
	// When channel has no DirPath and no ParentID, should fall back to loopDir.
	hostMgr := new(MockTerminalManager)
	s.srv.SetHostTerminalManager(hostMgr)
	s.srv.loopDir = "/tmp/loop-test"

	store := new(MockChannelLister)
	store.On("GetChannel", mock.Anything, "ch-nodir").
		Return(&db.Channel{ChannelID: "ch-nodir"}, nil)
	s.srv.store = store

	outCh := make(chan []byte, 1)
	doneCh := make(chan struct{})
	hostMgr.On("CreateSession", mock.Anything, "/tmp/loop-test/ch-nodir/work", []string(nil)).
		Return("host-fallback-2", (<-chan []byte)(outCh), []byte(nil), (<-chan struct{})(doneCh), nil)
	hostMgr.On("DetachSession", mock.Anything, mock.Anything).Return(nil).Maybe()

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ChannelID: "ch-nodir", Target: "host"})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "created", msg.Type)
	close(doneCh)
}

func (s *TerminalHandlerSuite) TestKillNotConfigured() {
	// When containerFinder or containerStopper is nil, kill should return an error.
	s.srv.containerFinder = nil
	s.srv.containerStopper = nil

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "kill", ChannelID: "ch-1"})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "error", msg.Type)
	require.Contains(s.T(), msg.Message, "container management not configured")
}

func (s *TerminalHandlerSuite) TestKillWithActiveSessionStopError() {
	// When kill has an active agent session and StopSession fails, it should
	// still proceed with container removal.
	outCh := make(chan []byte, 1)
	doneCh := make(chan struct{})
	s.terminal.On("CreateSession", mock.Anything, "ctr-1", ([]string)(nil)).
		Return("sess-err", (<-chan []byte)(outCh), ([]byte)(nil), (<-chan struct{})(doneCh), nil)
	s.terminal.On("StopSession", "sess-err").Return("", errors.New("stop failed"))

	store := new(MockChannelLister)
	store.On("GetChannel", mock.Anything, "ch-err").
		Return(&db.Channel{ChannelID: "ch-err"}, nil)
	s.srv.store = store

	finder := new(MockContainerFinder)
	finder.On("FindContainerByChannel", mock.Anything, "ch-err", "").Return("ctr-1", nil)
	s.srv.SetContainerFinder(finder)

	stopper := new(MockContainerStopper)
	stopper.On("ContainerRemove", mock.Anything, "ctr-1").Return(nil)
	s.srv.SetContainerStopper(stopper)

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ContainerID: "ctr-1"})
	readStatusMsg(s.T(), conn) // created

	sendControl(s.T(), conn, wsControlMessage{Type: "kill", ChannelID: "ch-err"})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "stopped", msg.Type)

	s.terminal.AssertCalled(s.T(), "StopSession", "sess-err")
	stopper.AssertCalled(s.T(), "ContainerRemove", mock.Anything, "ctr-1")
	close(doneCh)
}

func (s *TerminalHandlerSuite) TestKillContainerRemoveError() {
	// When ContainerRemove fails during kill, it should still report stopped.
	store := new(MockChannelLister)
	store.On("GetChannel", mock.Anything, "ch-rm-err").
		Return(&db.Channel{ChannelID: "ch-rm-err"}, nil)
	s.srv.store = store

	finder := new(MockContainerFinder)
	finder.On("FindContainerByChannel", mock.Anything, "ch-rm-err", "").Return("ctr-rm", nil)
	s.srv.SetContainerFinder(finder)

	stopper := new(MockContainerStopper)
	stopper.On("ContainerRemove", mock.Anything, "ctr-rm").Return(errors.New("remove failed"))
	s.srv.SetContainerStopper(stopper)

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "kill", ChannelID: "ch-rm-err"})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "stopped", msg.Type)

	stopper.AssertCalled(s.T(), "ContainerRemove", mock.Anything, "ctr-rm")
}

func (s *TerminalHandlerSuite) TestWriteMessageError() {
	// Deterministic test: inject a connWriteMessage that always returns an error.
	t := &terminalWSConn{
		connWriteMessage: func(int, []byte) error { return errors.New("write error") },
		logger:           s.srv.logger,
		stopCh:           make(chan struct{}),
	}
	t.writeMessage(websocket.TextMessage, []byte("test"))
}
