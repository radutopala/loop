package api

import (
	"errors"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/container"
	"github.com/radutopala/loop/internal/db"
)

// --- Kill (container removal by channel_id, no session required) ---

func (s *TerminalHandlerSuite) TestKillRemovesContainer() {
	reg := &mockContainerManager{
		byChannel: []*container.ContainerInfo{
			{ContainerID: "ctr-kill", ChannelID: "ch-kill", Type: container.ContainerTypeShell},
		},
	}
	reg.On("RemoveContainer", mock.Anything, "ctr-kill").Return(nil)
	s.srv.containerRegistry = reg

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "kill", ChannelID: "ch-kill"})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "stopped", msg.Type)

	reg.AssertCalled(s.T(), "RemoveContainer", mock.Anything, "ctr-kill")
}

func (s *TerminalHandlerSuite) TestKillAlsoStopsBrowser() {
	reg := &mockContainerManager{
		byChannel: []*container.ContainerInfo{
			{ContainerID: "ctr-kill", ChannelID: "ch-kill-br", Type: container.ContainerTypeShell},
			{ContainerID: "chrome-skip", ChannelID: "ch-kill-br", Type: container.ContainerTypeChrome}, // skipped
		},
	}
	reg.On("RemoveContainer", mock.Anything, "ctr-kill").Return(nil)
	s.srv.containerRegistry = reg

	browserMgr := new(mockBrowserProvider)
	browserMgr.On("StopBrowser", mock.Anything, "ch-kill-br").Return("chrome-kill-1", nil)
	reg.On("RemoveContainer", mock.Anything, "chrome-kill-1").Return(nil)
	s.srv.browser.setProviders(browserMgr, s.srv.browser.hostProvider)

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "kill", ChannelID: "ch-kill-br"})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "stopped", msg.Type)

	browserMgr.AssertCalled(s.T(), "StopBrowser", mock.Anything, "ch-kill-br")
}

func (s *TerminalHandlerSuite) TestKillNoContainerFound() {
	reg := &mockContainerManager{byChannel: []*container.ContainerInfo{}}
	s.srv.containerRegistry = reg

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "kill", ChannelID: "ch-gone"})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "stopped", msg.Type)

	reg.AssertNotCalled(s.T(), "RemoveContainer", mock.Anything, mock.Anything)
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

	reg := &mockContainerManager{
		byChannel: []*container.ContainerInfo{
			{ContainerID: "ctr-1", ChannelID: "ch-active", Type: container.ContainerTypeShell},
		},
	}
	reg.On("RemoveContainer", mock.Anything, "ctr-1").Return(nil)
	s.srv.containerRegistry = reg

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ContainerID: "ctr-1"})
	readStatusMsg(s.T(), conn) // created

	sendControl(s.T(), conn, wsControlMessage{Type: "kill", ChannelID: "ch-active"})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "stopped", msg.Type)

	s.terminal.AssertCalled(s.T(), "StopSession", "sess-1")
	reg.AssertCalled(s.T(), "RemoveContainer", mock.Anything, "ctr-1")

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
	// When containerRegistry is nil, kill should return an error.
	s.srv.containerRegistry = nil

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

	reg := &mockContainerManager{
		byChannel: []*container.ContainerInfo{
			{ContainerID: "ctr-1", ChannelID: "ch-err", Type: container.ContainerTypeShell},
		},
	}
	reg.On("RemoveContainer", mock.Anything, "ctr-1").Return(nil)
	s.srv.containerRegistry = reg

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ContainerID: "ctr-1"})
	readStatusMsg(s.T(), conn) // created

	sendControl(s.T(), conn, wsControlMessage{Type: "kill", ChannelID: "ch-err"})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "stopped", msg.Type)

	s.terminal.AssertCalled(s.T(), "StopSession", "sess-err")
	reg.AssertCalled(s.T(), "RemoveContainer", mock.Anything, "ctr-1")
	close(doneCh)
}

func (s *TerminalHandlerSuite) TestKillContainerRemoveError() {
	// When RemoveContainer fails during kill, it should still report stopped.
	reg := &mockContainerManager{
		byChannel: []*container.ContainerInfo{
			{ContainerID: "ctr-rm", ChannelID: "ch-rm-err", Type: container.ContainerTypeShell},
		},
	}
	reg.On("RemoveContainer", mock.Anything, "ctr-rm").Return(errors.New("remove failed"))
	s.srv.containerRegistry = reg

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "kill", ChannelID: "ch-rm-err"})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "stopped", msg.Type)

	reg.AssertCalled(s.T(), "RemoveContainer", mock.Anything, "ctr-rm")
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
