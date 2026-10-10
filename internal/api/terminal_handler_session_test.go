package api

import (
	"encoding/base64"
	"errors"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/container"
	"github.com/radutopala/loop/internal/terminal"
)

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

func (s *TerminalHandlerSuite) TestAttachWithAgentIDEnablesAutoAccept() {
	outCh := make(chan []byte, 1)
	doneCh := make(chan struct{})
	// History contains the trust prompt trigger.
	history := []byte("Entertoconfirm · Esc to cancel")
	s.terminal.On("AttachSession", "sess-1").
		Return((<-chan []byte)(outCh), history, (<-chan struct{})(doneCh), nil)
	s.terminal.On("DetachSession", mock.Anything, mock.Anything).Return(nil).Maybe()
	s.terminal.On("SendInput", "sess-1", []byte("\r")).Return(nil).Maybe()

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "attach", SessionID: "sess-1", AgentID: "agent-0", ChannelID: "ch-1"})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "attached", msg.Type)

	// Wait for auto-accept to fire from history scan (first retry at 500ms).
	time.Sleep(700 * time.Millisecond)
	s.terminal.AssertCalled(s.T(), "SendInput", "sess-1", []byte("\r"))

	close(doneCh)
}

func (s *TerminalHandlerSuite) TestAttachSessionError() {
	tests := []struct {
		name     string
		err      error
		wantCode string
	}{
		{name: "session gone", err: terminal.ErrSessionNotFound, wantCode: wsErrCodeSessionGone},
		{name: "other failure", err: errors.New("attach failed"), wantCode: wsErrCodeSessionFailed},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			s.terminal.On("AttachSession", "bad-sess").Return(nil, nil, nil, tt.err)

			conn, ts := s.dialWS()
			defer ts.Close()
			defer conn.Close()

			sendControl(s.T(), conn, wsControlMessage{Type: "attach", SessionID: "bad-sess"})

			msg := readStatusMsg(s.T(), conn)
			require.Equal(s.T(), "error", msg.Type)
			require.Equal(s.T(), tt.err.Error(), msg.Message)
			require.Equal(s.T(), tt.wantCode, msg.ErrorCode)
		})
	}
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

	reg := new(mockContainerManager)
	reg.On("RemoveContainer", mock.Anything, "ctr-1").Return(nil)
	s.srv.containerRegistry = reg

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
	reg.AssertCalled(s.T(), "RemoveContainer", mock.Anything, "ctr-1")

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

	reg := new(mockContainerManager)
	reg.On("RemoveContainer", mock.Anything, "ctr-1").Return(errors.New("remove failed"))
	s.srv.containerRegistry = reg

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ContainerID: "ctr-1"})
	readStatusMsg(s.T(), conn)

	sendControl(s.T(), conn, wsControlMessage{Type: "stop"})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "stopped", msg.Type)

	time.Sleep(50 * time.Millisecond)
	reg.AssertCalled(s.T(), "RemoveContainer", mock.Anything, "ctr-1")

	close(doneCh)
}

func (s *TerminalHandlerSuite) TestCloseSession() {
	outCh := make(chan []byte, 1)
	doneCh := make(chan struct{})
	s.terminal.On("CreateSession", mock.Anything, "ctr-1", ([]string)(nil)).
		Return("sess-1", (<-chan []byte)(outCh), ([]byte)(nil), (<-chan struct{})(doneCh), nil)
	s.terminal.On("KillProcessGroup", mock.Anything, "sess-1").Return(nil)
	s.terminal.On("StopSession", "sess-1").Return("ctr-1", nil)

	reg := new(mockContainerManager)
	s.srv.containerRegistry = reg

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ContainerID: "ctr-1"})
	readStatusMsg(s.T(), conn) // created

	sendControl(s.T(), conn, wsControlMessage{Type: "close"})

	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "stopped", msg.Type)
	// The shell is killed, or it would run on in the shared container.
	s.terminal.AssertCalled(s.T(), "KillProcessGroup", mock.Anything, "sess-1")

	// RemoveContainer should NOT be called for close (unlike stop).
	time.Sleep(50 * time.Millisecond)
	reg.AssertNotCalled(s.T(), "RemoveContainer", mock.Anything, mock.Anything)

	close(doneCh)
}

func (s *TerminalHandlerSuite) TestCloseSessionReleasesShell() {
	shell := func(status container.ContainerStatus) *container.ContainerInfo {
		return &container.ContainerInfo{ContainerID: "ctr-1", ChannelID: "ch-1", Type: container.ContainerTypeShell, Status: status}
	}
	tests := []struct {
		name       string
		info       *container.ContainerInfo
		live       int
		released   bool
		noRegistry bool
	}{
		{name: "last session in the shell", info: shell(container.ContainerStatusRunning), released: true},
		{name: "another pane still uses the shell", info: shell(container.ContainerStatusRunning), live: 1},
		{name: "shell already pending removal", info: shell(container.ContainerStatusPendingRemoval)},
		{name: "agent run container", info: &container.ContainerInfo{ContainerID: "ctr-1", Type: container.ContainerTypeAgent, Status: container.ContainerStatusRunning}},
		{name: "untracked container"},
		{name: "no container registry", noRegistry: true},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			outCh := make(chan []byte, 1)
			doneCh := make(chan struct{})
			defer close(doneCh)
			s.terminal.On("CreateSession", mock.Anything, "ctr-1", ([]string)(nil)).
				Return("sess-1", (<-chan []byte)(outCh), ([]byte)(nil), (<-chan struct{})(doneCh), nil)
			s.terminal.On("KillProcessGroup", mock.Anything, "sess-1").Return(nil)
			s.terminal.On("StopSession", "sess-1").Return("ctr-1", nil)
			s.terminal.On("LiveSessions", "ctr-1").Return(tt.live).Maybe()

			reg := new(mockContainerManager)
			if tt.info != nil {
				reg.containers = []*container.ContainerInfo{tt.info}
			}
			reg.On("ScheduleRemove", "ctr-1", 3*time.Minute).Return().Maybe()
			reg.On("Reclaim", "ctr-1").Return(true).Maybe()
			if !tt.noRegistry {
				s.srv.containerRegistry = reg
			}
			s.srv.containerKeepAlive = 3 * time.Minute

			conn, ts := s.dialWS()
			defer ts.Close()
			defer conn.Close()

			sendControl(s.T(), conn, wsControlMessage{Type: "create", ContainerID: "ctr-1"})
			readStatusMsg(s.T(), conn)
			sendControl(s.T(), conn, wsControlMessage{Type: "close"})
			require.Equal(s.T(), "stopped", readStatusMsg(s.T(), conn).Type)

			// The release runs before "stopped" is written.
			if tt.released {
				reg.AssertCalled(s.T(), "ScheduleRemove", "ctr-1", 3*time.Minute)
			} else {
				reg.AssertNotCalled(s.T(), "ScheduleRemove", mock.Anything, mock.Anything)
			}
			reg.AssertNotCalled(s.T(), "RemoveContainer", mock.Anything, mock.Anything)
		})
	}
}

func (s *TerminalHandlerSuite) TestCreateSessionClaimsShell() {
	shell := func(status container.ContainerStatus) *container.ContainerInfo {
		return &container.ContainerInfo{ContainerID: "ctr-1", ChannelID: "ch-1", Type: container.ContainerTypeShell, Status: status}
	}
	tests := []struct {
		name       string
		info       *container.ContainerInfo
		claimed    bool
		noRegistry bool
	}{
		{name: "shell released meanwhile", info: shell(container.ContainerStatusPendingRemoval), claimed: true},
		{name: "running shell", info: shell(container.ContainerStatusRunning)},
		{name: "agent run container", info: &container.ContainerInfo{ContainerID: "ctr-1", Type: container.ContainerTypeAgent, Status: container.ContainerStatusPendingRemoval}},
		{name: "untracked container"},
		{name: "no container registry", noRegistry: true},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			outCh := make(chan []byte, 1)
			doneCh := make(chan struct{})
			defer close(doneCh)
			s.terminal.On("CreateSession", mock.Anything, "ctr-1", ([]string)(nil)).
				Return("sess-1", (<-chan []byte)(outCh), ([]byte)(nil), (<-chan struct{})(doneCh), nil)

			reg := new(mockContainerManager)
			if tt.info != nil {
				reg.containers = []*container.ContainerInfo{tt.info}
			}
			reg.On("Reclaim", "ctr-1").Return(true).Maybe()
			if !tt.noRegistry {
				s.srv.containerRegistry = reg
			}

			conn, ts := s.dialWS()
			defer ts.Close()
			defer conn.Close()

			sendControl(s.T(), conn, wsControlMessage{Type: "create", ContainerID: "ctr-1"})
			require.Equal(s.T(), "created", readStatusMsg(s.T(), conn).Type)

			// The claim runs before "created" is written.
			if tt.claimed {
				reg.AssertCalled(s.T(), "Reclaim", "ctr-1")
			} else {
				reg.AssertNotCalled(s.T(), "Reclaim", mock.Anything)
			}
		})
	}
}

// exitingTerminalManager is a MockTerminalManager that reports session exits.
type exitingTerminalManager struct {
	*MockTerminalManager
	onExit func(containerID string)
}

func (m *exitingTerminalManager) SetOnExit(fn func(containerID string)) { m.onExit = fn }

func (s *TerminalHandlerSuite) TestSessionExitReleasesShell() {
	mgr := &exitingTerminalManager{MockTerminalManager: new(MockTerminalManager)}
	mgr.On("LiveSessions", "ctr-1").Return(0)
	reg := new(mockContainerManager)
	reg.containers = []*container.ContainerInfo{{ContainerID: "ctr-1", ChannelID: "ch-1", Type: container.ContainerTypeShell, Status: container.ContainerStatusRunning}}
	reg.On("ScheduleRemove", "ctr-1", 2*time.Minute).Return()
	s.srv.containerRegistry = reg
	s.srv.containerKeepAlive = 2 * time.Minute

	s.srv.SetTerminalManager(mgr)
	require.NotNil(s.T(), mgr.onExit)
	mgr.onExit("ctr-1")
	reg.AssertCalled(s.T(), "ScheduleRemove", "ctr-1", 2*time.Minute)

	// Without a terminal manager there's nothing to count sessions with.
	s.srv.termManager = nil
	s.srv.releaseShell("ctr-1")
	reg.AssertNumberOfCalls(s.T(), "ScheduleRemove", 1)
}

func (s *TerminalHandlerSuite) TestCloseDetachedSession() {
	tests := []struct {
		name     string
		target   string
		noHost   bool
		wantType string
	}{
		{name: "agent shell", target: "agent", wantType: "stopped"},
		{name: "host shell", target: "host", wantType: "stopped"},
		{name: "host shell without a host manager", target: "host", noHost: true, wantType: "error"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			host := new(MockTerminalManager)
			if !tt.noHost {
				s.srv.SetHostTerminalManager(host)
			}
			mgr := s.terminal
			if tt.target == "host" {
				mgr = host
			}
			mgr.On("StopSession", "sess-9").Return("ctr-1", nil)
			mgr.On("KillProcessGroup", mock.Anything, "sess-9").Return(nil).Maybe()

			conn, ts := s.dialWS()
			defer ts.Close()
			defer conn.Close()

			sendControl(s.T(), conn, wsControlMessage{Type: "close", SessionID: "sess-9", Target: tt.target})
			require.Equal(s.T(), tt.wantType, readStatusMsg(s.T(), conn).Type)
			if tt.wantType == "stopped" {
				mgr.AssertCalled(s.T(), "StopSession", "sess-9")
			}
			// A host shell's process ends with its session.
			if tt.target == "host" {
				mgr.AssertNotCalled(s.T(), "KillProcessGroup", mock.Anything, mock.Anything)
			} else {
				mgr.AssertCalled(s.T(), "KillProcessGroup", mock.Anything, "sess-9")
			}
		})
	}
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
	// A failed kill is only logged, and the session is still stopped.
	s.terminal.On("KillProcessGroup", mock.Anything, "sess-1").Return(errors.New("kill failed"))
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
