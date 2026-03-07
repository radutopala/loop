package terminal

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// mockExecClient implements ExecClient for testing.
type mockExecClient struct {
	mock.Mock
}

func (m *mockExecClient) ContainerExecCreate(ctx context.Context, containerID string, cmd []string, tty bool) (string, error) {
	args := m.Called(ctx, containerID, cmd, tty)
	return args.String(0), args.Error(1)
}

func (m *mockExecClient) ContainerExecAttach(ctx context.Context, execID string) (io.ReadWriteCloser, error) {
	args := m.Called(ctx, execID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(io.ReadWriteCloser), args.Error(1)
}

func (m *mockExecClient) ContainerExecResize(ctx context.Context, execID string, height, width uint) error {
	args := m.Called(ctx, execID, height, width)
	return args.Error(0)
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
	origRandRead func([]byte) (int, error)
}

func TestTerminalSuite(t *testing.T) {
	suite.Run(t, new(TerminalSuite))
}

func (s *TerminalSuite) SetupTest() {
	s.origRandRead = randRead
}

func (s *TerminalSuite) TearDownTest() {
	randRead = s.origRandRead
}

func (s *TerminalSuite) TestGenerateID() {
	id := generateID()
	require.Len(s.T(), id, 8)
}

func (s *TerminalSuite) TestCreateSession() {
	client := new(mockExecClient)
	pr, pw := io.Pipe()
	conn := &mockConn{r: pr, w: io.Discard}

	client.On("ContainerExecCreate", mock.Anything, "ctr-1", []string{"/bin/sh"}, true).Return("exec-1", nil)
	client.On("ContainerExecAttach", mock.Anything, "exec-1").Return(conn, nil)

	mgr := NewManager(client)
	sess, err := mgr.CreateSession(context.Background(), "ctr-1", nil)
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), sess.ID())
	require.Equal(s.T(), "ctr-1", sess.ContainerID())

	pw.Close()
	<-sess.Done()

	client.AssertExpectations(s.T())
}

func (s *TerminalSuite) TestCreateSessionWithCmd() {
	client := new(mockExecClient)
	pr, pw := io.Pipe()
	conn := &mockConn{r: pr, w: io.Discard}

	client.On("ContainerExecCreate", mock.Anything, "ctr-1", []string{"/bin/bash"}, true).Return("exec-1", nil)
	client.On("ContainerExecAttach", mock.Anything, "exec-1").Return(conn, nil)

	mgr := NewManager(client)
	sess, err := mgr.CreateSession(context.Background(), "ctr-1", []string{"/bin/bash"})
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), sess.ID())

	pw.Close()
	<-sess.Done()

	client.AssertExpectations(s.T())
}

func (s *TerminalSuite) TestCreateSessionExecCreateError() {
	client := new(mockExecClient)
	client.On("ContainerExecCreate", mock.Anything, "ctr-1", []string{"/bin/sh"}, true).Return("", errors.New("exec failed"))

	mgr := NewManager(client)
	_, err := mgr.CreateSession(context.Background(), "ctr-1", nil)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "creating exec")

	client.AssertExpectations(s.T())
}

func (s *TerminalSuite) TestCreateSessionAttachError() {
	client := new(mockExecClient)
	client.On("ContainerExecCreate", mock.Anything, "ctr-1", []string{"/bin/sh"}, true).Return("exec-1", nil)
	client.On("ContainerExecAttach", mock.Anything, "exec-1").Return(nil, errors.New("attach failed"))

	mgr := NewManager(client)
	_, err := mgr.CreateSession(context.Background(), "ctr-1", nil)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "attaching exec")

	client.AssertExpectations(s.T())
}

func (s *TerminalSuite) TestAttachDetach() {
	client := new(mockExecClient)
	pr, pw := io.Pipe()
	conn := &mockConn{r: pr, w: io.Discard}

	client.On("ContainerExecCreate", mock.Anything, "ctr-1", []string{"/bin/sh"}, true).Return("exec-1", nil)
	client.On("ContainerExecAttach", mock.Anything, "exec-1").Return(conn, nil)

	mgr := NewManager(client)
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

	sess.Detach(ch)

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

	client.On("ContainerExecCreate", mock.Anything, "ctr-1", []string{"/bin/sh"}, true).Return("exec-1", nil)
	client.On("ContainerExecAttach", mock.Anything, "exec-1").Return(conn, nil)

	mgr := NewManager(client)
	mgr.SetRingBufSize(1024)
	sess, err := mgr.CreateSession(context.Background(), "ctr-1", nil)
	require.NoError(s.T(), err)

	// Write data before attaching.
	_, _ = pw.Write([]byte("previous"))
	time.Sleep(50 * time.Millisecond) // let readLoop consume

	ch, history := sess.Attach()
	require.Equal(s.T(), []byte("previous"), history)

	sess.Detach(ch)
	pw.Close()
	<-sess.Done()

	client.AssertExpectations(s.T())
}

func (s *TerminalSuite) TestDetachUnknownChannel() {
	client := new(mockExecClient)
	pr, pw := io.Pipe()
	conn := &mockConn{r: pr, w: io.Discard}

	client.On("ContainerExecCreate", mock.Anything, "ctr-1", []string{"/bin/sh"}, true).Return("exec-1", nil)
	client.On("ContainerExecAttach", mock.Anything, "exec-1").Return(conn, nil)

	mgr := NewManager(client)
	sess, err := mgr.CreateSession(context.Background(), "ctr-1", nil)
	require.NoError(s.T(), err)

	// Detach a channel that was never attached — should not panic.
	unknownCh := make(chan []byte)
	sess.Detach(unknownCh)

	pw.Close()
	<-sess.Done()

	client.AssertExpectations(s.T())
}

func (s *TerminalSuite) TestSendInput() {
	client := new(mockExecClient)
	pr, pw := io.Pipe()
	var inputBuf bytes.Buffer
	conn := &mockConn{r: pr, w: &inputBuf}

	client.On("ContainerExecCreate", mock.Anything, "ctr-1", []string{"/bin/sh"}, true).Return("exec-1", nil)
	client.On("ContainerExecAttach", mock.Anything, "exec-1").Return(conn, nil)

	mgr := NewManager(client)
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
	mgr := NewManager(client)

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

	client.On("ContainerExecCreate", mock.Anything, "ctr-1", []string{"/bin/sh"}, true).Return("exec-1", nil)
	client.On("ContainerExecAttach", mock.Anything, "exec-1").Return(errWriter, nil)

	mgr := NewManager(client)
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

	client.On("ContainerExecCreate", mock.Anything, "ctr-1", []string{"/bin/sh"}, true).Return("exec-1", nil)
	client.On("ContainerExecAttach", mock.Anything, "exec-1").Return(conn, nil)
	client.On("ContainerExecResize", mock.Anything, "exec-1", uint(24), uint(80)).Return(nil)

	mgr := NewManager(client)
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
	mgr := NewManager(client)

	err := mgr.Resize(context.Background(), "nonexistent", 24, 80)
	require.ErrorIs(s.T(), err, ErrSessionNotFound)
}

func (s *TerminalSuite) TestResizeError() {
	client := new(mockExecClient)
	pr, pw := io.Pipe()
	conn := &mockConn{r: pr, w: io.Discard}

	client.On("ContainerExecCreate", mock.Anything, "ctr-1", []string{"/bin/sh"}, true).Return("exec-1", nil)
	client.On("ContainerExecAttach", mock.Anything, "exec-1").Return(conn, nil)
	client.On("ContainerExecResize", mock.Anything, "exec-1", uint(24), uint(80)).Return(errors.New("resize failed"))

	mgr := NewManager(client)
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

	client.On("ContainerExecCreate", mock.Anything, "ctr-1", []string{"/bin/sh"}, true).Return("exec-1", nil)
	client.On("ContainerExecAttach", mock.Anything, "exec-1").Return(conn, nil)

	mgr := NewManager(client)
	sess, err := mgr.CreateSession(context.Background(), "ctr-1", nil)
	require.NoError(s.T(), err)

	// Attach a client.
	ch, _ := sess.Attach()

	err = mgr.StopSession(sess.ID())
	require.NoError(s.T(), err)
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
	mgr := NewManager(client)

	err := mgr.StopSession("nonexistent")
	require.ErrorIs(s.T(), err, ErrSessionNotFound)
}

func (s *TerminalSuite) TestGetSessionNotFound() {
	client := new(mockExecClient)
	mgr := NewManager(client)

	_, err := mgr.GetSession("nonexistent")
	require.ErrorIs(s.T(), err, ErrSessionNotFound)
}

func (s *TerminalSuite) TestListSessions() {
	client := new(mockExecClient)
	pr1, pw1 := io.Pipe()
	pr2, pw2 := io.Pipe()
	conn1 := &mockConn{r: pr1, w: io.Discard}
	conn2 := &mockConn{r: pr2, w: io.Discard}

	client.On("ContainerExecCreate", mock.Anything, "ctr-1", []string{"/bin/sh"}, true).Return("exec-1", nil).Once()
	client.On("ContainerExecAttach", mock.Anything, "exec-1").Return(conn1, nil).Once()
	client.On("ContainerExecCreate", mock.Anything, "ctr-2", []string{"/bin/sh"}, true).Return("exec-2", nil).Once()
	client.On("ContainerExecAttach", mock.Anything, "exec-2").Return(conn2, nil).Once()

	mgr := NewManager(client)

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

	client.On("ContainerExecCreate", mock.Anything, "ctr-1", []string{"/bin/sh"}, true).Return("exec-1", nil)
	client.On("ContainerExecAttach", mock.Anything, "exec-1").Return(conn, nil)

	mgr := NewManager(client)
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

	sess.Detach(ch1)
	sess.Detach(ch2)
	pw.Close()
	<-sess.Done()

	client.AssertExpectations(s.T())
}

func (s *TerminalSuite) TestSetRingBufSize() {
	client := new(mockExecClient)
	mgr := NewManager(client)
	mgr.SetRingBufSize(128)
	require.Equal(s.T(), 128, mgr.ringBufSize)
}

func (s *TerminalSuite) TestReadLoopClosesOnEOF() {
	client := new(mockExecClient)
	pr, pw := io.Pipe()
	conn := &mockConn{r: pr, w: io.Discard}

	client.On("ContainerExecCreate", mock.Anything, "ctr-1", []string{"/bin/sh"}, true).Return("exec-1", nil)
	client.On("ContainerExecAttach", mock.Anything, "exec-1").Return(conn, nil)

	mgr := NewManager(client)
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
