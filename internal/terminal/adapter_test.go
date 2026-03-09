package terminal

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type AdapterSuite struct {
	suite.Suite
	mgr     *Manager
	adapter *ManagerAdapter
	mock    *adapterMockExecClient
}

func TestAdapterSuite(t *testing.T) {
	suite.Run(t, new(AdapterSuite))
}

func (s *AdapterSuite) SetupTest() {
	s.mock = &adapterMockExecClient{}
	s.mgr = NewManager(s.mock, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.adapter = NewManagerAdapter(s.mgr)
}

func (s *AdapterSuite) TestCreateSession() {
	s.mock.execID = "exec-1"
	s.mock.conn = newFakeConn()

	sid, output, _, done, err := s.adapter.CreateSession(context.Background(), "container-1", []string{"/bin/sh"})
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), sid)
	require.NotNil(s.T(), output)
	require.NotNil(s.T(), done)

	// Cleanup
	_, err2 := s.adapter.StopSession(sid)
	require.NoError(s.T(), err2)
}

func (s *AdapterSuite) TestCreateSessionError() {
	s.mock.createErr = io.ErrUnexpectedEOF

	_, _, _, _, err := s.adapter.CreateSession(context.Background(), "container-1", nil)
	require.Error(s.T(), err)
}

func (s *AdapterSuite) TestAttachSession() {
	s.mock.execID = "exec-1"
	s.mock.conn = newFakeConn()

	sid, _, _, _, err := s.adapter.CreateSession(context.Background(), "container-1", nil)
	require.NoError(s.T(), err)

	output, _, done, err := s.adapter.AttachSession(sid)
	require.NoError(s.T(), err)
	require.NotNil(s.T(), output)
	require.NotNil(s.T(), done)

	_, err2 := s.adapter.StopSession(sid)
	require.NoError(s.T(), err2)
}

func (s *AdapterSuite) TestAttachSessionNotFound() {
	_, _, _, err := s.adapter.AttachSession("nonexistent")
	require.ErrorIs(s.T(), err, ErrSessionNotFound)
}

func (s *AdapterSuite) TestDetachSession() {
	s.mock.execID = "exec-1"
	s.mock.conn = newFakeConn()

	sid, output, _, _, err := s.adapter.CreateSession(context.Background(), "container-1", nil)
	require.NoError(s.T(), err)

	err = s.adapter.DetachSession(sid, output)
	require.NoError(s.T(), err)

	_, err2 := s.adapter.StopSession(sid)
	require.NoError(s.T(), err2)
}

func (s *AdapterSuite) TestDetachSessionNotFound() {
	err := s.adapter.DetachSession("nonexistent", nil)
	require.ErrorIs(s.T(), err, ErrSessionNotFound)
}

func (s *AdapterSuite) TestSendInput() {
	s.mock.execID = "exec-1"
	conn := newFakeConn()
	s.mock.conn = conn

	sid, _, _, _, err := s.adapter.CreateSession(context.Background(), "container-1", nil)
	require.NoError(s.T(), err)

	err = s.adapter.SendInput(sid, []byte("hello"))
	require.NoError(s.T(), err)

	_, err2 := s.adapter.StopSession(sid)
	require.NoError(s.T(), err2)
}

func (s *AdapterSuite) TestResize() {
	s.mock.execID = "exec-1"
	s.mock.conn = newFakeConn()

	sid, _, _, _, err := s.adapter.CreateSession(context.Background(), "container-1", nil)
	require.NoError(s.T(), err)

	err = s.adapter.Resize(context.Background(), sid, 24, 80)
	require.NoError(s.T(), err)

	_, err2 := s.adapter.StopSession(sid)
	require.NoError(s.T(), err2)
}

func (s *AdapterSuite) TestStopSessionNotFound() {
	_, err := s.adapter.StopSession("nonexistent")
	require.ErrorIs(s.T(), err, ErrSessionNotFound)
}

// adapterMockExecClient for adapter tests.
type adapterMockExecClient struct {
	execID    string
	conn      io.ReadWriteCloser
	createErr error
}

func (m *adapterMockExecClient) ContainerExecCreate(_ context.Context, _ string, _ []string, _ bool) (string, error) {
	if m.createErr != nil {
		return "", m.createErr
	}
	return m.execID, nil
}

func (m *adapterMockExecClient) ContainerExecAttach(_ context.Context, _ string) (io.ReadWriteCloser, error) {
	return m.conn, nil
}

func (m *adapterMockExecClient) ContainerExecResize(_ context.Context, _ string, _, _ uint) error {
	return nil
}

// fakeConn is a simple ReadWriteCloser that blocks on read.
type fakeConn struct {
	closed chan struct{}
}

func newFakeConn() *fakeConn {
	return &fakeConn{closed: make(chan struct{})}
}

func (f *fakeConn) Read(p []byte) (int, error) {
	<-f.closed
	return 0, io.EOF
}

func (f *fakeConn) Write(p []byte) (int, error) {
	return len(p), nil
}

func (f *fakeConn) Close() error {
	select {
	case <-f.closed:
	default:
		close(f.closed)
	}
	return nil
}
