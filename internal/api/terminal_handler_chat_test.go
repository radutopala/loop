package api

import (
	"errors"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/container"
)

// --- Shell in the chat's container ---

// chatContainers is a registry holding ch-1's shell container, its stopped
// old chat container, and its running chat container.
func chatContainers() *mockContainerManager {
	return &mockContainerManager{byChannel: []*container.ContainerInfo{
		{ContainerID: "shell-1", ChannelID: "ch-1", Type: container.ContainerTypeShell, Status: container.ContainerStatusRunning},
		{ContainerID: "agent-old", ChannelID: "ch-1", Type: container.ContainerTypeAgent, Status: container.ContainerStatusStopped},
		{ContainerID: "agent-1", ChannelID: "ch-1", Type: container.ContainerTypeAgent, Status: container.ContainerStatusRunning},
	}}
}

func (s *TerminalHandlerSuite) TestCreateChatShellErrors() {
	tests := []struct {
		name     string
		registry bool
		msg      wsControlMessage
		create   error
		wantMsg  string
		wantCode string
	}{
		{name: "no channel", registry: true, msg: wsControlMessage{}, wantMsg: "channel_id required", wantCode: wsErrCodeMissingField},
		{name: "empty arg", registry: true, msg: wsControlMessage{ChannelID: "ch-1", Cmd: []string{"bash", ""}}, wantMsg: "cmd contains empty argument", wantCode: wsErrCodeInvalidInput},
		{name: "no chat container", registry: true, msg: wsControlMessage{ChannelID: "ch-2"}, wantMsg: "the chat's container isn't running", wantCode: wsErrCodeSessionFailed},
		{name: "no registry", msg: wsControlMessage{ChannelID: "ch-1"}, wantMsg: "the chat's container isn't running", wantCode: wsErrCodeSessionFailed},
		{name: "exec fails", registry: true, msg: wsControlMessage{ChannelID: "ch-1"}, create: errors.New("exec failed"), wantMsg: "exec failed", wantCode: wsErrCodeSessionFailed},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			if tt.registry {
				s.srv.containerRegistry = chatContainers()
			}
			if tt.create != nil {
				s.terminal.On("CreateSession", mock.Anything, "agent-1", []string{"/bin/bash"}).
					Return("", (<-chan []byte)(nil), []byte(nil), (<-chan struct{})(nil), tt.create)
			}
			conn, ts := s.dialWS()
			defer ts.Close()
			defer conn.Close()

			tt.msg.Type = "create"
			tt.msg.Target = targetChat
			sendControl(s.T(), conn, tt.msg)

			msg := readStatusMsg(s.T(), conn)
			require.Equal(s.T(), "error", msg.Type)
			require.Equal(s.T(), tt.wantMsg, msg.Message)
			require.Equal(s.T(), tt.wantCode, msg.ErrorCode)
			s.terminal.AssertExpectations(s.T())
		})
	}
}

func (s *TerminalHandlerSuite) TestChatShellStopKeepsContainer() {
	tests := []struct {
		name      string
		cmd       []string
		wantCmd   []string
		resizeErr error
		killErr   error
	}{
		{name: "default shell", wantCmd: []string{"/bin/bash"}},
		{name: "given cmd", cmd: []string{"/bin/sh"}, wantCmd: []string{"/bin/sh"}},
		{name: "resize and kill fail", wantCmd: []string{"/bin/bash"}, resizeErr: errors.New("resize failed"), killErr: errors.New("kill failed")},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			reg := chatContainers()
			s.srv.containerRegistry = reg
			outCh := make(chan []byte, 1)
			doneCh := make(chan struct{})
			defer close(doneCh)
			s.terminal.On("CreateSession", mock.Anything, "agent-1", tt.wantCmd).
				Return("sess-1", (<-chan []byte)(outCh), []byte(nil), (<-chan struct{})(doneCh), nil)
			s.terminal.On("Resize", mock.Anything, "sess-1", uint(24), uint(80)).Return(tt.resizeErr)
			s.terminal.On("KillProcessGroup", mock.Anything, "sess-1").Return(tt.killErr)
			s.terminal.On("StopSession", "sess-1").Return("agent-1", nil)

			conn, ts := s.dialWS()
			defer ts.Close()
			defer conn.Close()

			sendControl(s.T(), conn, wsControlMessage{Type: "create", ChannelID: "ch-1", Target: targetChat, Cmd: tt.cmd, Rows: 24, Cols: 80})
			msg := readStatusMsg(s.T(), conn)
			require.Equal(s.T(), "created", msg.Type)
			require.Equal(s.T(), "sess-1", msg.SessionID)

			sendControl(s.T(), conn, wsControlMessage{Type: "stop"})
			require.Equal(s.T(), "stopped", readStatusMsg(s.T(), conn).Type)

			// The chat still runs in its container: the shell ends, the
			// container stays.
			reg.AssertNotCalled(s.T(), "RemoveContainer", mock.Anything, mock.Anything)
			s.terminal.AssertExpectations(s.T())
		})
	}
}

func (s *TerminalHandlerSuite) TestStopNeverRemovesChatContainer() {
	// A session created on a chat's container by id is ended without
	// removing it either.
	reg := chatContainers()
	s.srv.containerRegistry = reg
	outCh := make(chan []byte, 1)
	doneCh := make(chan struct{})
	defer close(doneCh)
	s.terminal.On("CreateSession", mock.Anything, "agent-1", []string{"/bin/bash"}).
		Return("sess-1", (<-chan []byte)(outCh), []byte(nil), (<-chan struct{})(doneCh), nil)
	s.terminal.On("StopSession", "sess-1").Return("agent-1", nil)

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "create", ContainerID: "agent-1", Cmd: []string{"/bin/bash"}})
	require.Equal(s.T(), "created", readStatusMsg(s.T(), conn).Type)
	sendControl(s.T(), conn, wsControlMessage{Type: "stop"})
	require.Equal(s.T(), "stopped", readStatusMsg(s.T(), conn).Type)

	reg.AssertNotCalled(s.T(), "RemoveContainer", mock.Anything, mock.Anything)
	s.terminal.AssertNotCalled(s.T(), "KillProcessGroup", mock.Anything, mock.Anything)
}

func (s *TerminalHandlerSuite) TestAttachChatShell() {
	outCh := make(chan []byte, 1)
	doneCh := make(chan struct{})
	defer close(doneCh)
	s.terminal.On("AttachSession", "sess-1").Return((<-chan []byte)(outCh), []byte(nil), (<-chan struct{})(doneCh), nil)
	resized := make(chan struct{})
	s.terminal.On("Resize", mock.Anything, "sess-1", uint(30), uint(100)).
		Run(func(mock.Arguments) { close(resized) }).Return(nil)

	conn, ts := s.dialWS()
	defer ts.Close()
	defer conn.Close()

	sendControl(s.T(), conn, wsControlMessage{Type: "attach", SessionID: "sess-1", Target: targetChat})
	msg := readStatusMsg(s.T(), conn)
	require.Equal(s.T(), "attached", msg.Type)
	require.Equal(s.T(), "sess-1", msg.SessionID)

	// The attached session takes the docker manager's calls.
	sendControl(s.T(), conn, wsControlMessage{Type: "resize", Rows: 30, Cols: 100})
	select {
	case <-resized:
	case <-time.After(2 * time.Second):
		s.T().Fatal("resize never reached the docker manager")
	}
}
