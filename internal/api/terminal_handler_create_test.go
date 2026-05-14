package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/db"
)

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

func (s *TerminalHandlerSuite) TestCreateSessionWithLeafID() {
	// When the FE sends leaf_id, handleCreate must route through
	// CreateSessionWithEnv stamping LOOP_TERMINAL_LEAF=<leaf_id> so the
	// in-container dockerproxy can attribute approval prompts back to
	// this specific terminal pane.
	outCh := make(chan []byte, 1)
	doneCh := make(chan struct{})
	expectedEnv := []string{"LOOP_TERMINAL_LEAF=pane-7"}
	s.terminal.On("CreateSessionWithEnv", mock.Anything, "ctr-1", []string{"/bin/bash"}, expectedEnv).
		Return("sess-env", (<-chan []byte)(outCh), []byte(nil), (<-chan struct{})(doneCh), nil)
	s.terminal.On("DetachSession", mock.Anything, mock.Anything).Return(nil).Maybe()

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ContainerID: "ctr-1", Cmd: []string{"/bin/bash"}, LeafID: "pane-7"})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "created", msg.Type)
	require.Equal(s.T(), "sess-env", msg.SessionID)

	s.terminal.AssertCalled(s.T(), "CreateSessionWithEnv", mock.Anything, "ctr-1", []string{"/bin/bash"}, expectedEnv)
	s.terminal.AssertNotCalled(s.T(), "CreateSession", mock.Anything, mock.Anything, mock.Anything)
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

	finder := new(mockContainerManager)
	finder.On("FindOrCreateShell", mock.Anything, "ch-42", mock.Anything, mock.Anything).Return("resolved-container-123", nil)
	s.srv.containerRegistry = finder

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

	finder := new(mockContainerManager)
	finder.On("FindOrCreateShell", mock.Anything, "ch-proj", "/home/user/dev/loop", "").Return("resolved-container-456", nil)
	s.srv.containerRegistry = finder

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
	finder := new(mockContainerManager)
	finder.On("FindOrCreateShell", mock.Anything, "ch-missing", mock.Anything, mock.Anything).
		Return("", errors.New("no container found"))
	s.srv.containerRegistry = finder

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ChannelID: "ch-missing"})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "error", msg.Type)
	require.Contains(s.T(), msg.Message, "no running container")
	require.Equal(s.T(), wsErrCodeSessionFailed, msg.ErrorCode)
}

func (s *TerminalHandlerSuite) TestSetContainerRegistry() {
	srv := nilServer()
	require.Nil(s.T(), srv.containerRegistry)
	reg := new(mockContainerManager)
	srv.SetContainerRegistry(reg)
	require.NotNil(s.T(), srv.containerRegistry)
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

	finder := new(mockContainerManager)
	finder.On("FindOrCreateShell", mock.Anything, "ch-99", mock.Anything, mock.Anything).Return("resolved-ctr", nil)
	s.srv.containerRegistry = finder

	builder := new(MockInteractiveCmdBuilder)
	builder.On("BuildInteractiveCmd", "ch-99", "", "", "", "", false).Return("claude --dangerously-skip-permissions")
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

	finder := new(mockContainerManager)
	finder.On("FindOrCreateShell", mock.Anything, "ch-dir", "/projects/app", "").Return("resolved-ctr-dir", nil)
	s.srv.containerRegistry = finder

	builder := new(MockInteractiveCmdBuilder)
	builder.On("BuildInteractiveCmd", "ch-dir", "/projects/app", "", "", "", false).Return("claude --mcp-config /projects/app/.loop/mcp-ch-dir.json")
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

func (s *TerminalHandlerSuite) TestCreateSessionWorktreePassesParentDirPath() {
	outCh := make(chan []byte, 1)
	doneCh := make(chan struct{})
	s.terminal.On("CreateSession", mock.Anything, "resolved-ctr-wt", []string(nil)).
		Return("sess-wt", (<-chan []byte)(outCh), []byte(nil), (<-chan struct{})(doneCh), nil)
	inputSent := onSendInputCalled(s.terminal, "sess-wt", []byte("claude --worktree-cmd\n"))
	s.terminal.On("DetachSession", mock.Anything, mock.Anything).Return(nil).Maybe()

	store := new(MockChannelLister)
	// The worktree channel has Worktree=true and a ParentID.
	store.On("GetChannel", mock.Anything, "wt-ch").
		Return(&db.Channel{ChannelID: "wt-ch", DirPath: "/worktrees/wt-1", ParentID: "parent-ch", Worktree: true}, nil)
	// The parent channel provides the project dir.
	store.On("GetChannel", mock.Anything, "parent-ch").
		Return(&db.Channel{ChannelID: "parent-ch", DirPath: "/projects/app", SessionID: "sess-parent"}, nil)
	s.srv.store = store

	finder := new(mockContainerManager)
	// parentDirPath should be the parent's DirPath.
	finder.On("FindOrCreateShell", mock.Anything, "wt-ch", "/worktrees/wt-1", "/projects/app").Return("resolved-ctr-wt", nil)
	s.srv.containerRegistry = finder

	builder := new(MockInteractiveCmdBuilder)
	// BuildInteractiveCmd should receive parentDirPath="/projects/app".
	builder.On("BuildInteractiveCmd", "wt-ch", "/worktrees/wt-1", "/projects/app", "sess-parent", "", true).Return("claude --worktree-cmd")
	s.srv.SetInteractiveCmdBuilder(builder)

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ChannelID: "wt-ch"})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "created", msg.Type)
	require.Equal(s.T(), "sess-wt", msg.SessionID)
	select {
	case <-inputSent:
	case <-time.After(time.Second):
		s.T().Fatal("timed out waiting for SendInput")
	}
	finder.AssertExpectations(s.T())
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

	finder := new(mockContainerManager)
	finder.On("FindOrCreateShell", mock.Anything, "ch-resume", mock.Anything, mock.Anything).Return("resolved-ctr", nil)
	s.srv.containerRegistry = finder

	builder := new(MockInteractiveCmdBuilder)
	builder.On("BuildInteractiveCmd", "ch-resume", "", "", "sess-existing", "", false, mock.Anything).
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

func (s *TerminalHandlerSuite) TestCreateSessionOverridesWithMsgSessionID() {
	// Use a fresh mock so stopOnClose cleanup mocks don't conflict with SetupTest defaults.
	s.terminal = new(MockTerminalManager)
	s.srv.SetTerminalManager(s.terminal)

	outCh := make(chan []byte, 1)
	doneCh := make(chan struct{})
	s.terminal.On("CreateSession", mock.Anything, "resolved-ctr", []string(nil)).
		Return("sess-override", (<-chan []byte)(outCh), []byte(nil), (<-chan struct{})(doneCh), nil)
	// The override session ID should be used, not the channel's stored one.
	inputSent := onSendInputCalled(s.terminal, "sess-override", []byte("claude --dangerously-skip-permissions --resume sess-picked\n"))
	// stopOnClose triggers KillProcessGroup + StopSession on WS disconnect.
	s.terminal.On("KillProcessGroup", mock.Anything, "sess-override").Return(nil).Maybe()
	s.terminal.On("StopSession", "sess-override").Return("resolved-ctr", nil).Maybe()

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
	builder.AssertExpectations(s.T())

	conn.Close()
	ts.Close()
	time.Sleep(50 * time.Millisecond)
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

	finder := new(mockContainerManager)
	finder.On("FindOrCreateShell", mock.Anything, "thread-1", mock.Anything, mock.Anything).Return("resolved-ctr", nil)
	s.srv.containerRegistry = finder

	builder := new(MockInteractiveCmdBuilder)
	builder.On("BuildInteractiveCmd", "thread-1", "", "", "sess-parent", "", true, mock.Anything).
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

	finder := new(mockContainerManager)
	finder.On("FindOrCreateShell", mock.Anything, "thread-2", mock.Anything, mock.Anything).Return("resolved-ctr", nil)
	s.srv.containerRegistry = finder

	builder := new(MockInteractiveCmdBuilder)
	// Has its own session — uses resume, not fork.
	builder.On("BuildInteractiveCmd", "thread-2", "", "", "sess-thread-own", "", false, mock.Anything).
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

	finder := new(mockContainerManager)
	finder.On("FindOrCreateShell", mock.Anything, "ch-err", mock.Anything, mock.Anything).Return("resolved-ctr", nil)
	s.srv.containerRegistry = finder

	builder := new(MockInteractiveCmdBuilder)
	builder.On("BuildInteractiveCmd", "ch-err", "", "", "", "", false).Return("claude --dangerously-skip-permissions")
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

	finder := new(mockContainerManager)
	finder.On("FindOrCreateShell", mock.Anything, "ch-explicit", mock.Anything, mock.Anything).Return("resolved-ctr", nil)
	s.srv.containerRegistry = finder

	builder := new(MockInteractiveCmdBuilder)
	s.srv.SetInteractiveCmdBuilder(builder)

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ChannelID: "ch-explicit", Cmd: explicitCmd})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "created", msg.Type)
	builder.AssertNotCalled(s.T(), "BuildInteractiveCmd", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	s.terminal.AssertNotCalled(s.T(), "SendInput", mock.Anything, mock.Anything)
}
