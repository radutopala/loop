package api

import (
	"encoding/base64"
	"errors"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/db"
)

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
	builder.AssertNotCalled(s.T(), "BuildInteractiveCmd", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
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

	reg := new(mockContainerManager)
	s.srv.containerRegistry = reg

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ChannelID: "ch-1", Target: "host"})
	readStatusMsg(s.T(), conn)

	sendControl(s.T(), conn, wsControlMessage{Type: "stop"})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "stopped", msg.Type)

	time.Sleep(50 * time.Millisecond)
	reg.AssertNotCalled(s.T(), "RemoveContainer", mock.Anything, mock.Anything)
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
