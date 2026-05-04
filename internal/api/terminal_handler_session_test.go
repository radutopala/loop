package api

import (
	"encoding/base64"
	"errors"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
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

	// RemoveContainer should NOT be called for close (unlike stop).
	time.Sleep(50 * time.Millisecond)
	reg.AssertNotCalled(s.T(), "RemoveContainer", mock.Anything, mock.Anything)

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
