package terminal

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

var testLogger = slog.Default()

// mockExecClient implements ExecClient for testing.
type mockExecClient struct {
	mock.Mock
}

func (m *mockExecClient) ExecCreate(ctx context.Context, containerID string, cmd []string, tty bool) (string, error) {
	args := m.Called(ctx, containerID, cmd, tty)
	return args.String(0), args.Error(1)
}

func (m *mockExecClient) ExecAttach(ctx context.Context, execID string) (io.ReadWriteCloser, error) {
	args := m.Called(ctx, execID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(io.ReadWriteCloser), args.Error(1)
}

func (m *mockExecClient) ExecResize(ctx context.Context, execID string, height, width uint) error {
	args := m.Called(ctx, execID, height, width)
	return args.Error(0)
}

func (m *mockExecClient) ExecInspectPid(ctx context.Context, execID string) (int, error) {
	args := m.Called(ctx, execID)
	return args.Int(0), args.Error(1)
}

func (m *mockExecClient) DefaultShellCmd(pidFile string) []string {
	return []string{"/bin/sh", "-c", fmt.Sprintf("echo $$ > %s; exec /bin/sh", pidFile)}
}

// mockHostExecClient implements ExecClient but NOT PidFileShellCmd,
// simulating host-based clients that manage processes directly.
type mockHostExecClient struct {
	mock.Mock
}

func (m *mockHostExecClient) ExecCreate(ctx context.Context, containerID string, cmd []string, tty bool) (string, error) {
	args := m.Called(ctx, containerID, cmd, tty)
	return args.String(0), args.Error(1)
}

func (m *mockHostExecClient) ExecAttach(ctx context.Context, execID string) (io.ReadWriteCloser, error) {
	args := m.Called(ctx, execID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(io.ReadWriteCloser), args.Error(1)
}

func (m *mockHostExecClient) ExecResize(ctx context.Context, execID string, height, width uint) error {
	args := m.Called(ctx, execID, height, width)
	return args.Error(0)
}

func (m *mockHostExecClient) ExecInspectPid(ctx context.Context, execID string) (int, error) {
	args := m.Called(ctx, execID)
	return args.Int(0), args.Error(1)
}

// mockConn is a mock ReadWriteCloser backed by a pipe.
type mockConn struct {
	r       io.Reader
	w       io.Writer
	closeFn func() error
}

func (c *mockConn) Read(p []byte) (int, error)  { return c.r.Read(p) }
func (c *mockConn) Write(p []byte) (int, error) { return c.w.Write(p) }
func (c *mockConn) Close() error {
	if c.closeFn != nil {
		return c.closeFn()
	}
	return nil
}

type TerminalSuite struct {
	suite.Suite
}

func TestTerminalSuite(t *testing.T) {
	suite.Run(t, new(TerminalSuite))
}

func (s *TerminalSuite) TestGenerateID() {
	id := generateID(rand.Read)
	require.Len(s.T(), id, 8)
}

func (s *TerminalSuite) TestCreateSession() {
	client := new(mockExecClient)
	pr, pw := io.Pipe()
	conn := &mockConn{r: pr, w: io.Discard}

	client.On("ExecCreate", mock.Anything, "ctr-1", mock.Anything, true).Return("exec-1", nil)
	client.On("ExecAttach", mock.Anything, "exec-1").Return(conn, nil)

	mgr := NewManager(client, testLogger)
	sess, err := mgr.CreateSession(context.Background(), "ctr-1", nil)
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), sess.ID())
	require.Equal(s.T(), "ctr-1", sess.ContainerID())

	pw.Close()
	<-sess.Done()

	client.AssertExpectations(s.T())
}

func (s *TerminalSuite) TestCreateSessionHostNoPidFile() {
	// mockHostExecClient does NOT implement PidFileShellCmd,
	// so CreateSession should pass empty cmd through and set pidFile="".
	client := new(mockHostExecClient)
	pr, pw := io.Pipe()
	conn := &mockConn{r: pr, w: io.Discard}

	client.On("ExecCreate", mock.Anything, "/home/user", []string(nil), true).Return("exec-1", nil)
	client.On("ExecAttach", mock.Anything, "exec-1").Return(conn, nil)

	mgr := NewManager(client, testLogger)
	sess, err := mgr.CreateSession(context.Background(), "/home/user", nil)
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), sess.ID())
	require.Empty(s.T(), sess.pidFile, "host sessions should have no pidFile")

	pw.Close()
	<-sess.Done()

	client.AssertExpectations(s.T())
}

func (s *TerminalSuite) TestCreateSessionWithCmd() {
	client := new(mockExecClient)
	pr, pw := io.Pipe()
	conn := &mockConn{r: pr, w: io.Discard}

	client.On("ExecCreate", mock.Anything, "ctr-1", []string{"/bin/bash"}, true).Return("exec-1", nil)
	client.On("ExecAttach", mock.Anything, "exec-1").Return(conn, nil)

	mgr := NewManager(client, testLogger)
	sess, err := mgr.CreateSession(context.Background(), "ctr-1", []string{"/bin/bash"})
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), sess.ID())

	pw.Close()
	<-sess.Done()

	client.AssertExpectations(s.T())
}

// mockEnvExecClient is mockExecClient + EnvExecClient. CreateSessionWithEnv
// must dispatch to ExecCreateWithEnv when the client satisfies the optional
// interface and env is non-empty; otherwise it falls back to ExecCreate
// (covered by the existing TestCreateSession*).
type mockEnvExecClient struct {
	mockExecClient
}

func (m *mockEnvExecClient) ExecCreateWithEnv(ctx context.Context, targetID string, cmd, env []string, tty bool) (string, error) {
	args := m.Called(ctx, targetID, cmd, env, tty)
	return args.String(0), args.Error(1)
}

func (s *TerminalSuite) TestCreateSessionWithEnvUsesEnvExecClient() {
	client := new(mockEnvExecClient)
	pr, pw := io.Pipe()
	conn := &mockConn{r: pr, w: io.Discard}

	envVars := []string{"LOOP_TERMINAL_LEAF=leaf-9"}
	client.On("ExecCreateWithEnv", mock.Anything, "ctr-1", mock.Anything, envVars, true).Return("exec-env", nil)
	client.On("ExecAttach", mock.Anything, "exec-env").Return(conn, nil)

	mgr := NewManager(client, testLogger)
	sess, err := mgr.CreateSessionWithEnv(context.Background(), "ctr-1", nil, envVars)
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), sess.ID())

	pw.Close()
	<-sess.Done()
	client.AssertExpectations(s.T())
}

func (s *TerminalSuite) TestCreateSessionWithEnvFallbackWhenNoEnv() {
	// Client implements EnvExecClient but caller passes len(env)==0.
	// Manager must route to plain ExecCreate, not ExecCreateWithEnv —
	// preserves the cheap path for the common no-env case.
	client := new(mockEnvExecClient)
	pr, pw := io.Pipe()
	conn := &mockConn{r: pr, w: io.Discard}

	client.On("ExecCreate", mock.Anything, "ctr-1", mock.Anything, true).Return("exec-1", nil)
	client.On("ExecAttach", mock.Anything, "exec-1").Return(conn, nil)

	mgr := NewManager(client, testLogger)
	_, err := mgr.CreateSessionWithEnv(context.Background(), "ctr-1", nil, nil)
	require.NoError(s.T(), err)

	pw.Close()
	client.AssertExpectations(s.T())
	client.AssertNotCalled(s.T(), "ExecCreateWithEnv", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func (s *TerminalSuite) TestCreateSessionWithEnvErrorPropagates() {
	client := new(mockEnvExecClient)
	client.On("ExecCreateWithEnv", mock.Anything, "ctr-1", mock.Anything, []string{"K=V"}, true).Return("", errors.New("env exec failed"))

	mgr := NewManager(client, testLogger)
	_, err := mgr.CreateSessionWithEnv(context.Background(), "ctr-1", nil, []string{"K=V"})
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "creating exec")
	client.AssertExpectations(s.T())
}

func (s *TerminalSuite) TestCreateSessionExecCreateError() {
	client := new(mockExecClient)
	client.On("ExecCreate", mock.Anything, "ctr-1", mock.Anything, true).Return("", errors.New("exec failed"))

	mgr := NewManager(client, testLogger)
	_, err := mgr.CreateSession(context.Background(), "ctr-1", nil)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "creating exec")

	client.AssertExpectations(s.T())
}

func (s *TerminalSuite) TestCreateSessionAttachError() {
	client := new(mockExecClient)
	client.On("ExecCreate", mock.Anything, "ctr-1", mock.Anything, true).Return("exec-1", nil)
	client.On("ExecAttach", mock.Anything, "exec-1").Return(nil, errors.New("attach failed"))

	mgr := NewManager(client, testLogger)
	_, err := mgr.CreateSession(context.Background(), "ctr-1", nil)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "attaching exec")

	client.AssertExpectations(s.T())
}

func (s *TerminalSuite) TestAttachDetach() {
	client := new(mockExecClient)
	pr, pw := io.Pipe()
	conn := &mockConn{r: pr, w: io.Discard}

	client.On("ExecCreate", mock.Anything, "ctr-1", mock.Anything, true).Return("exec-1", nil)
	client.On("ExecAttach", mock.Anything, "exec-1").Return(conn, nil)

	mgr := NewManager(client, testLogger)
	sess, err := mgr.CreateSession(context.Background(), "ctr-1", nil)
	require.NoError(s.T(), err)

	ch, history := sess.Attach()
	require.Empty(s.T(), history)

	// Write some data via the pipe.
	_, _ = pw.Write([]byte("hello"))

	// Read from the channel.
	select {
	case data := <-ch:
		require.Equal(s.T(), []byte("hello"), data)
	case <-time.After(time.Second):
		s.T().Fatal("timeout waiting for data")
	}

	err = sess.Detach(ch)
	require.NoError(s.T(), err)

	// Channel should be closed after detach.
	_, ok := <-ch
	require.False(s.T(), ok)

	pw.Close()
	<-sess.Done()

	client.AssertExpectations(s.T())
}

func (s *TerminalSuite) TestAttachReceivesHistory() {
	client := new(mockExecClient)
	pr, pw := io.Pipe()
	conn := &mockConn{r: pr, w: io.Discard}

	client.On("ExecCreate", mock.Anything, "ctr-1", mock.Anything, true).Return("exec-1", nil)
	client.On("ExecAttach", mock.Anything, "exec-1").Return(conn, nil)

	mgr := NewManager(client, testLogger)
	mgr.SetRingBufSize(1024)
	sess, err := mgr.CreateSession(context.Background(), "ctr-1", nil)
	require.NoError(s.T(), err)

	// Write data before attaching.
	_, _ = pw.Write([]byte("previous"))
	time.Sleep(50 * time.Millisecond) // let readLoop consume

	ch, history := sess.Attach()
	require.Equal(s.T(), []byte("previous"), history)

	require.NoError(s.T(), sess.Detach(ch))
	pw.Close()
	<-sess.Done()

	client.AssertExpectations(s.T())
}

func (s *TerminalSuite) TestDetachUnknownChannel() {
	client := new(mockExecClient)
	pr, pw := io.Pipe()
	conn := &mockConn{r: pr, w: io.Discard}

	client.On("ExecCreate", mock.Anything, "ctr-1", mock.Anything, true).Return("exec-1", nil)
	client.On("ExecAttach", mock.Anything, "exec-1").Return(conn, nil)

	mgr := NewManager(client, testLogger)
	sess, err := mgr.CreateSession(context.Background(), "ctr-1", nil)
	require.NoError(s.T(), err)

	// Detach a channel that was never attached — should return error.
	unknownCh := make(chan []byte)
	err = sess.Detach(unknownCh)
	require.ErrorIs(s.T(), err, ErrClientNotFound)

	pw.Close()
	<-sess.Done()

	client.AssertExpectations(s.T())
}

func (s *TerminalSuite) TestSendInput() {
	client := new(mockExecClient)
	pr, pw := io.Pipe()
	var inputBuf bytes.Buffer
	conn := &mockConn{r: pr, w: &inputBuf}

	client.On("ExecCreate", mock.Anything, "ctr-1", mock.Anything, true).Return("exec-1", nil)
	client.On("ExecAttach", mock.Anything, "exec-1").Return(conn, nil)

	mgr := NewManager(client, testLogger)
	sess, err := mgr.CreateSession(context.Background(), "ctr-1", nil)
	require.NoError(s.T(), err)

	err = mgr.SendInput(sess.ID(), []byte("ls\n"))
	require.NoError(s.T(), err)
	require.Equal(s.T(), "ls\n", inputBuf.String())

	pw.Close()
	<-sess.Done()

	client.AssertExpectations(s.T())
}

func (s *TerminalSuite) TestSendInputNotFound() {
	client := new(mockExecClient)
	mgr := NewManager(client, testLogger)

	err := mgr.SendInput("nonexistent", []byte("data"))
	require.ErrorIs(s.T(), err, ErrSessionNotFound)
}

func (s *TerminalSuite) TestSendInputWriteError() {
	client := new(mockExecClient)
	pr, pw := io.Pipe()
	errWriter := &errReadWriteCloser{
		Reader:   pr,
		writeErr: errors.New("write failed"),
	}

	client.On("ExecCreate", mock.Anything, "ctr-1", mock.Anything, true).Return("exec-1", nil)
	client.On("ExecAttach", mock.Anything, "exec-1").Return(errWriter, nil)

	mgr := NewManager(client, testLogger)
	sess, err := mgr.CreateSession(context.Background(), "ctr-1", nil)
	require.NoError(s.T(), err)

	err = mgr.SendInput(sess.ID(), []byte("data"))
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "writing input")

	pw.Close()
	<-sess.Done()

	client.AssertExpectations(s.T())
}

type errReadWriteCloser struct {
	io.Reader
	writeErr error
}

func (e *errReadWriteCloser) Write([]byte) (int, error) { return 0, e.writeErr }
func (e *errReadWriteCloser) Close() error              { return nil }

func (s *TerminalSuite) TestResize() {
	client := new(mockExecClient)
	pr, pw := io.Pipe()
	conn := &mockConn{r: pr, w: io.Discard}

	client.On("ExecCreate", mock.Anything, "ctr-1", mock.Anything, true).Return("exec-1", nil)
	client.On("ExecAttach", mock.Anything, "exec-1").Return(conn, nil)
	client.On("ExecResize", mock.Anything, "exec-1", uint(24), uint(80)).Return(nil)

	mgr := NewManager(client, testLogger)
	sess, err := mgr.CreateSession(context.Background(), "ctr-1", nil)
	require.NoError(s.T(), err)

	err = mgr.Resize(context.Background(), sess.ID(), 24, 80)
	require.NoError(s.T(), err)

	pw.Close()
	<-sess.Done()

	client.AssertExpectations(s.T())
}

func (s *TerminalSuite) TestResizeNotFound() {
	client := new(mockExecClient)
	mgr := NewManager(client, testLogger)

	err := mgr.Resize(context.Background(), "nonexistent", 24, 80)
	require.ErrorIs(s.T(), err, ErrSessionNotFound)
}

func (s *TerminalSuite) TestResizeError() {
	client := new(mockExecClient)
	pr, pw := io.Pipe()
	conn := &mockConn{r: pr, w: io.Discard}

	client.On("ExecCreate", mock.Anything, "ctr-1", mock.Anything, true).Return("exec-1", nil)
	client.On("ExecAttach", mock.Anything, "exec-1").Return(conn, nil)
	client.On("ExecResize", mock.Anything, "exec-1", uint(24), uint(80)).Return(errors.New("resize failed"))

	mgr := NewManager(client, testLogger)
	sess, err := mgr.CreateSession(context.Background(), "ctr-1", nil)
	require.NoError(s.T(), err)

	err = mgr.Resize(context.Background(), sess.ID(), 24, 80)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "resizing exec")

	pw.Close()
	<-sess.Done()

	client.AssertExpectations(s.T())
}

func (s *TerminalSuite) TestStopSession() {
	client := new(mockExecClient)
	pr, pw := io.Pipe()
	closed := false
	conn := &mockConn{
		r: pr,
		w: io.Discard,
		closeFn: func() error {
			closed = true
			pw.Close()
			return nil
		},
	}

	client.On("ExecCreate", mock.Anything, "ctr-1", mock.Anything, true).Return("exec-1", nil)
	client.On("ExecAttach", mock.Anything, "exec-1").Return(conn, nil)

	mgr := NewManager(client, testLogger)
	sess, err := mgr.CreateSession(context.Background(), "ctr-1", nil)
	require.NoError(s.T(), err)

	// Attach a client.
	ch, _ := sess.Attach()

	containerID, err := mgr.StopSession(sess.ID())
	require.NoError(s.T(), err)
	require.Equal(s.T(), "ctr-1", containerID)
	require.True(s.T(), closed)

	// Client channel should be closed.
	_, ok := <-ch
	require.False(s.T(), ok)

	// Session should be removed.
	_, err = mgr.GetSession(sess.ID())
	require.ErrorIs(s.T(), err, ErrSessionNotFound)

	client.AssertExpectations(s.T())
}

func (s *TerminalSuite) TestStopSessionNotFound() {
	client := new(mockExecClient)
	mgr := NewManager(client, testLogger)

	_, err := mgr.StopSession("nonexistent")
	require.ErrorIs(s.T(), err, ErrSessionNotFound)
}

func (s *TerminalSuite) TestGetSessionNotFound() {
	client := new(mockExecClient)
	mgr := NewManager(client, testLogger)

	_, err := mgr.GetSession("nonexistent")
	require.ErrorIs(s.T(), err, ErrSessionNotFound)
}

func (s *TerminalSuite) TestListSessions() {
	client := new(mockExecClient)
	pr1, pw1 := io.Pipe()
	pr2, pw2 := io.Pipe()
	conn1 := &mockConn{r: pr1, w: io.Discard}
	conn2 := &mockConn{r: pr2, w: io.Discard}

	client.On("ExecCreate", mock.Anything, "ctr-1", mock.Anything, true).Return("exec-1", nil).Once()
	client.On("ExecAttach", mock.Anything, "exec-1").Return(conn1, nil).Once()
	client.On("ExecCreate", mock.Anything, "ctr-2", mock.Anything, true).Return("exec-2", nil).Once()
	client.On("ExecAttach", mock.Anything, "exec-2").Return(conn2, nil).Once()

	mgr := NewManager(client, testLogger)

	sess1, err := mgr.CreateSession(context.Background(), "ctr-1", nil)
	require.NoError(s.T(), err)
	sess2, err := mgr.CreateSession(context.Background(), "ctr-2", nil)
	require.NoError(s.T(), err)

	ids := mgr.ListSessions()
	require.Len(s.T(), ids, 2)
	require.ElementsMatch(s.T(), []string{sess1.ID(), sess2.ID()}, ids)

	pw1.Close()
	pw2.Close()
	<-sess1.Done()
	<-sess2.Done()

	client.AssertExpectations(s.T())
}

func (s *TerminalSuite) TestMultipleClients() {
	client := new(mockExecClient)
	pr, pw := io.Pipe()
	conn := &mockConn{r: pr, w: io.Discard}

	client.On("ExecCreate", mock.Anything, "ctr-1", mock.Anything, true).Return("exec-1", nil)
	client.On("ExecAttach", mock.Anything, "exec-1").Return(conn, nil)

	mgr := NewManager(client, testLogger)
	sess, err := mgr.CreateSession(context.Background(), "ctr-1", nil)
	require.NoError(s.T(), err)

	ch1, _ := sess.Attach()
	ch2, _ := sess.Attach()

	_, _ = pw.Write([]byte("broadcast"))

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		data := <-ch1
		require.Equal(s.T(), []byte("broadcast"), data)
	}()
	go func() {
		defer wg.Done()
		data := <-ch2
		require.Equal(s.T(), []byte("broadcast"), data)
	}()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		s.T().Fatal("timeout waiting for broadcast")
	}

	require.NoError(s.T(), sess.Detach(ch1))
	require.NoError(s.T(), sess.Detach(ch2))
	pw.Close()
	<-sess.Done()

	client.AssertExpectations(s.T())
}

func (s *TerminalSuite) TestSetRingBufSize() {
	client := new(mockExecClient)
	mgr := NewManager(client, testLogger)
	mgr.SetRingBufSize(128)
	require.Equal(s.T(), 128, mgr.ringBufSize)
}

func (s *TerminalSuite) TestReadLoopClosesOnEOF() {
	client := new(mockExecClient)
	pr, pw := io.Pipe()
	conn := &mockConn{r: pr, w: io.Discard}

	client.On("ExecCreate", mock.Anything, "ctr-1", mock.Anything, true).Return("exec-1", nil)
	client.On("ExecAttach", mock.Anything, "exec-1").Return(conn, nil)

	mgr := NewManager(client, testLogger)
	sess, err := mgr.CreateSession(context.Background(), "ctr-1", nil)
	require.NoError(s.T(), err)

	pw.Close()

	select {
	case <-sess.Done():
	case <-time.After(time.Second):
		s.T().Fatal("timeout waiting for session done")
	}

	client.AssertExpectations(s.T())
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) contains(str string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return bytes.Contains(b.buf.Bytes(), []byte(str))
}

func (s *TerminalSuite) TestIdleTimeout() {
	client := new(mockExecClient)
	pr, _ := io.Pipe()
	conn := &mockConn{r: pr, w: io.Discard}

	client.On("ExecCreate", mock.Anything, "ctr-1", mock.Anything, true).Return("exec-1", nil)
	client.On("ExecAttach", mock.Anything, "exec-1").Return(conn, nil)

	mgr := NewManager(client, testLogger)
	mgr.SetIdleTimeout(100 * time.Millisecond)

	sess, err := mgr.CreateSession(context.Background(), "ctr-1", nil)
	require.NoError(s.T(), err)

	// Session should close after idle timeout with no output.
	select {
	case <-sess.Done():
	case <-time.After(2 * time.Second):
		s.T().Fatal("timeout waiting for idle session to close")
	}

	client.AssertExpectations(s.T())
}

func (s *TerminalSuite) TestIdleTimeoutResetOnOutput() {
	client := new(mockExecClient)
	pr, pw := io.Pipe()
	conn := &mockConn{r: pr, w: io.Discard}

	client.On("ExecCreate", mock.Anything, "ctr-1", mock.Anything, true).Return("exec-1", nil)
	client.On("ExecAttach", mock.Anything, "exec-1").Return(conn, nil)

	mgr := NewManager(client, testLogger)
	mgr.SetIdleTimeout(200 * time.Millisecond)

	sess, err := mgr.CreateSession(context.Background(), "ctr-1", nil)
	require.NoError(s.T(), err)

	// Write data before timeout expires to reset the timer.
	time.Sleep(100 * time.Millisecond)
	_, _ = pw.Write([]byte("keep alive"))

	// Session should still be active right after the write.
	select {
	case <-sess.Done():
		s.T().Fatal("session closed too early")
	case <-time.After(50 * time.Millisecond):
		// Good — still alive.
	}

	// Now let it idle out.
	select {
	case <-sess.Done():
	case <-time.After(2 * time.Second):
		s.T().Fatal("timeout waiting for idle session to close")
	}

	client.AssertExpectations(s.T())
}

func (s *TerminalSuite) TestNoIdleTimeoutByDefault() {
	client := new(mockExecClient)
	pr, pw := io.Pipe()
	conn := &mockConn{r: pr, w: io.Discard}

	client.On("ExecCreate", mock.Anything, "ctr-1", mock.Anything, true).Return("exec-1", nil)
	client.On("ExecAttach", mock.Anything, "exec-1").Return(conn, nil)

	mgr := NewManager(client, testLogger)
	// No SetIdleTimeout — default is 0 (disabled).

	sess, err := mgr.CreateSession(context.Background(), "ctr-1", nil)
	require.NoError(s.T(), err)

	// Session should NOT close after a brief wait (no timeout configured).
	select {
	case <-sess.Done():
		s.T().Fatal("session closed unexpectedly")
	case <-time.After(200 * time.Millisecond):
		// Good — still alive.
	}

	pw.Close()
	<-sess.Done()

	client.AssertExpectations(s.T())
}

func (s *TerminalSuite) TestSetIdleTimeout() {
	client := new(mockExecClient)
	mgr := NewManager(client, testLogger)
	mgr.SetIdleTimeout(5 * time.Minute)
	require.Equal(s.T(), 5*time.Minute, mgr.idleTimeout)
}

func (s *TerminalSuite) TestConcurrentAttachDetach() {
	client := new(mockExecClient)
	pr, pw := io.Pipe()
	conn := &mockConn{r: pr, w: io.Discard}

	client.On("ExecCreate", mock.Anything, "ctr-1", mock.Anything, true).Return("exec-1", nil)
	client.On("ExecAttach", mock.Anything, "exec-1").Return(conn, nil)

	mgr := NewManager(client, testLogger)
	sess, err := mgr.CreateSession(context.Background(), "ctr-1", nil)
	require.NoError(s.T(), err)

	const goroutines = 20
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for range goroutines {
		go func() {
			defer wg.Done()
			ch, _ := sess.Attach()
			// Read one message to ensure the channel is live.
			select {
			case <-ch:
			case <-time.After(200 * time.Millisecond):
			}
			_ = sess.Detach(ch)
		}()
	}

	// Feed data while goroutines attach/detach concurrently.
	go func() {
		for range 50 {
			_, _ = pw.Write([]byte("x"))
			time.Sleep(time.Millisecond)
		}
	}()

	wg.Wait()

	// All clients detached — no clients should remain.
	sess.mu.Lock()
	require.Empty(s.T(), sess.clients)
	sess.mu.Unlock()

	pw.Close()
	<-sess.Done()

	client.AssertExpectations(s.T())
}

func (s *TerminalSuite) TestSlowConsumerDrop() {
	logBuf := &syncBuffer{}
	logger := slog.New(slog.NewTextHandler(logBuf, nil))

	client := new(mockExecClient)
	pr, pw := io.Pipe()
	conn := &mockConn{r: pr, w: io.Discard}

	client.On("ExecCreate", mock.Anything, "ctr-1", mock.Anything, true).Return("exec-1", nil)
	client.On("ExecAttach", mock.Anything, "exec-1").Return(conn, nil)

	mgr := NewManager(client, logger)
	sess, err := mgr.CreateSession(context.Background(), "ctr-1", nil)
	require.NoError(s.T(), err)

	// Attach a client but never read from the channel.
	ch, _ := sess.Attach()

	// Fill the channel buffer (capacity 64) then trigger a drop.
	for i := 0; i < 65; i++ {
		_, _ = pw.Write([]byte("x"))
		time.Sleep(time.Millisecond)
	}

	// Verify the warning was logged.
	require.Eventually(s.T(), func() bool {
		return logBuf.contains("slow consumer")
	}, time.Second, 10*time.Millisecond)

	require.NoError(s.T(), sess.Detach(ch))
	pw.Close()
	<-sess.Done()

	client.AssertExpectations(s.T())
}

func (s *TerminalSuite) TestKillProcessGroupSessionNotFound() {
	client := new(mockExecClient)
	mgr := NewManager(client, testLogger)

	err := mgr.KillProcessGroup(context.Background(), "nonexistent")
	require.ErrorIs(s.T(), err, ErrSessionNotFound)
}

func (s *TerminalSuite) TestKillProcessGroupNoPidFile() {
	client := new(mockExecClient)
	pr, pw := io.Pipe()
	conn := &mockConn{r: pr, w: io.Discard}

	// Create with explicit cmd so pidFile stays default but we override it.
	client.On("ExecCreate", mock.Anything, "ctr-1", []string{"/bin/bash"}, true).Return("exec-1", nil)
	client.On("ExecAttach", mock.Anything, "exec-1").Return(conn, nil)

	mgr := NewManager(client, testLogger)
	sess, err := mgr.CreateSession(context.Background(), "ctr-1", []string{"/bin/bash"})
	require.NoError(s.T(), err)

	// Manually clear the pidFile to simulate a session created with explicit cmd.
	mgr.mu.Lock()
	mgr.sessions[sess.ID()].pidFile = ""
	mgr.mu.Unlock()

	// Should return nil (no pidFile means nothing to kill).
	err = mgr.KillProcessGroup(context.Background(), sess.ID())
	require.NoError(s.T(), err)

	pw.Close()
	<-sess.Done()
	client.AssertExpectations(s.T())
}

func (s *TerminalSuite) TestKillProcessGroupExecCreateError() {
	client := new(mockExecClient)
	pr, pw := io.Pipe()
	conn := &mockConn{r: pr, w: io.Discard}

	client.On("ExecCreate", mock.Anything, "ctr-1", mock.Anything, true).Return("exec-1", nil)
	client.On("ExecAttach", mock.Anything, "exec-1").Return(conn, nil)

	mgr := NewManager(client, testLogger)
	sess, err := mgr.CreateSession(context.Background(), "ctr-1", nil)
	require.NoError(s.T(), err)

	// Mock ExecCreate for the kill command to fail.
	client.On("ExecCreate", mock.Anything, "ctr-1", mock.Anything, false).Return("", errors.New("exec create failed"))

	err = mgr.KillProcessGroup(context.Background(), sess.ID())
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "creating kill exec")

	pw.Close()
	<-sess.Done()
	client.AssertExpectations(s.T())
}

func (s *TerminalSuite) TestKillProcessGroupExecAttachError() {
	client := new(mockExecClient)
	pr, pw := io.Pipe()
	conn := &mockConn{r: pr, w: io.Discard}

	client.On("ExecCreate", mock.Anything, "ctr-1", mock.Anything, true).Return("exec-1", nil)
	client.On("ExecAttach", mock.Anything, "exec-1").Return(conn, nil)

	mgr := NewManager(client, testLogger)
	sess, err := mgr.CreateSession(context.Background(), "ctr-1", nil)
	require.NoError(s.T(), err)

	// Mock ExecCreate for the kill command to succeed.
	client.On("ExecCreate", mock.Anything, "ctr-1", mock.Anything, false).Return("kill-exec-1", nil)
	// Mock ExecAttach for the kill exec to fail.
	client.On("ExecAttach", mock.Anything, "kill-exec-1").Return(nil, errors.New("attach failed"))

	err = mgr.KillProcessGroup(context.Background(), sess.ID())
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "attaching kill exec")

	pw.Close()
	<-sess.Done()
	client.AssertExpectations(s.T())
}

func (s *TerminalSuite) TestKillProcessGroupSuccess() {
	client := new(mockExecClient)
	pr, pw := io.Pipe()
	conn := &mockConn{r: pr, w: io.Discard}

	client.On("ExecCreate", mock.Anything, "ctr-1", mock.Anything, true).Return("exec-1", nil)
	client.On("ExecAttach", mock.Anything, "exec-1").Return(conn, nil)

	mgr := NewManager(client, testLogger)
	sess, err := mgr.CreateSession(context.Background(), "ctr-1", nil)
	require.NoError(s.T(), err)

	// Mock ExecCreate for the kill command.
	client.On("ExecCreate", mock.Anything, "ctr-1", mock.Anything, false).Return("kill-exec-1", nil)
	// Mock ExecAttach for the kill exec with a connection that can be closed.
	killConn := &mockConn{r: pr, w: io.Discard}
	client.On("ExecAttach", mock.Anything, "kill-exec-1").Return(killConn, nil)

	err = mgr.KillProcessGroup(context.Background(), sess.ID())
	require.NoError(s.T(), err)

	pw.Close()
	<-sess.Done()
	client.AssertExpectations(s.T())
}
