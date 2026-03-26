package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type ChannelSuite struct {
	suite.Suite
}

func TestChannelSuite(t *testing.T) {
	suite.Run(t, new(ChannelSuite))
}

func (s *ChannelSuite) TestNewChannelTransport() {
	t := newChannelTransport()
	require.NotNil(s.T(), t)
	require.NotNil(s.T(), t.inner)
	require.NotNil(s.T(), t.writer)
}

func (s *ChannelSuite) TestWriteNotification() {
	var buf bytes.Buffer
	t := newChannelTransport()
	t.writer = &buf

	err := t.WriteNotification("hello from agent-0", map[string]string{"from_agent": "agent-0"})
	require.NoError(s.T(), err)

	// Parse the written JSON-RPC notification.
	data := buf.Bytes()
	require.True(s.T(), len(data) > 0)

	msg, err := jsonrpc.DecodeMessage(bytes.TrimSpace(data))
	require.NoError(s.T(), err)

	req, ok := msg.(*jsonrpc.Request)
	require.True(s.T(), ok)
	require.Equal(s.T(), "notifications/claude/channel", req.Method)
	// Notification: ID should be zero-value.
	require.Empty(s.T(), req.ID)

	var params struct {
		Content string            `json:"content"`
		Meta    map[string]string `json:"meta"`
	}
	require.NoError(s.T(), json.Unmarshal(req.Params, &params))
	require.Equal(s.T(), "hello from agent-0", params.Content)
	require.Equal(s.T(), "agent-0", params.Meta["from_agent"])
}

func (s *ChannelSuite) TestWriteNotificationWriteError() {
	t := newChannelTransport()
	t.writer = &errorWriter{}

	err := t.WriteNotification("test", nil)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "writing notification")
}

type errorWriter struct{}

func (w *errorWriter) Write(_ []byte) (int, error) {
	return 0, fmt.Errorf("write failed")
}

func (s *ChannelSuite) TestStartPushReceiver() {
	// Set up a WebSocket server that sends one message then closes.
	upgrader := websocket.Upgrader{}
	wsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.WriteJSON(map[string]string{
			"from_agent_id": "agent-1",
			"content":       "hey there",
		})
		// Wait a bit for the receiver to process before closing.
		time.Sleep(100 * time.Millisecond)
	}))
	defer wsSrv.Close()

	// Use a thread-safe buffer via the transport's mutex.
	transport := newChannelTransport()
	safeBuf := &syncBuffer{}
	transport.writer = safeBuf

	apiURL := "http" + strings.TrimPrefix(wsSrv.URL, "http")
	startPushReceiver(apiURL, "ch-1", "agent-0", transport, slog.Default())

	// Wait for the message to be processed.
	time.Sleep(200 * time.Millisecond)

	// Verify a notification was written.
	data := safeBuf.Bytes()
	require.True(s.T(), len(data) > 0)
	msg, err := jsonrpc.DecodeMessage(bytes.TrimSpace(data))
	require.NoError(s.T(), err)
	req := msg.(*jsonrpc.Request)
	require.Equal(s.T(), "notifications/claude/channel", req.Method)
}

func (s *ChannelSuite) TestStartPushReceiverReconnects() {
	// Server that accepts connections, sends a message on the second connect.
	var connectCount int32
	var mu sync.Mutex
	upgrader := websocket.Upgrader{}
	wsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		mu.Lock()
		connectCount++
		n := connectCount
		mu.Unlock()

		if n == 1 {
			// First connection: close immediately to simulate disconnect.
			return
		}
		// Second connection: send a message.
		_ = conn.WriteJSON(map[string]string{
			"from_agent_id": "agent-1",
			"content":       "reconnected",
		})
		time.Sleep(100 * time.Millisecond)
	}))
	defer wsSrv.Close()

	transport := newChannelTransport()
	safeBuf := &syncBuffer{}
	transport.writer = safeBuf

	apiURL := "http" + strings.TrimPrefix(wsSrv.URL, "http")
	startPushReceiver(apiURL, "ch-1", "agent-0", transport, slog.Default())

	// Wait for reconnect + message processing (1s reconnect delay + processing).
	require.Eventually(s.T(), func() bool {
		return safeBuf.Len() > 0
	}, 5*time.Second, 100*time.Millisecond)

	data := safeBuf.Bytes()
	msg, err := jsonrpc.DecodeMessage(bytes.TrimSpace(data))
	require.NoError(s.T(), err)
	req := msg.(*jsonrpc.Request)
	require.Equal(s.T(), "notifications/claude/channel", req.Method)

	mu.Lock()
	require.GreaterOrEqual(s.T(), connectCount, int32(2))
	mu.Unlock()
}

// syncBuffer is a thread-safe bytes.Buffer for testing.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Bytes()
}

func (b *syncBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Len()
}

func (s *ChannelSuite) TestConnectSuccess() {
	t := newChannelTransport()
	t.inner = &mockTransport{conn: &mockConn{sessionID: "test-session"}}

	conn, err := t.Connect(context.Background())
	require.NoError(s.T(), err)
	require.NotNil(s.T(), conn)
	require.Equal(s.T(), "test-session", conn.SessionID())
}

func (s *ChannelSuite) TestConnectError() {
	t := newChannelTransport()
	t.inner = &mockTransport{err: fmt.Errorf("connect failed")}

	conn, err := t.Connect(context.Background())
	require.Error(s.T(), err)
	require.Nil(s.T(), conn)
}

type mockTransport struct {
	conn mcp.Connection
	err  error
}

func (m *mockTransport) Connect(_ context.Context) (mcp.Connection, error) {
	return m.conn, m.err
}

func (s *ChannelSuite) TestStartPushReceiverWriteNotificationError() {
	// Server sends a message; the transport writer fails, exercising the error log path.
	upgrader := websocket.Upgrader{}
	wsSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.WriteJSON(map[string]string{
			"from_agent_id": "agent-1",
			"content":       "will fail",
		})
		time.Sleep(100 * time.Millisecond)
	}))
	defer wsSrv.Close()

	transport := newChannelTransport()
	transport.writer = &errorWriter{} // makes WriteNotification return an error

	apiURL := "http" + strings.TrimPrefix(wsSrv.URL, "http")
	startPushReceiver(apiURL, "ch-1", "agent-0", transport, slog.Default())
	time.Sleep(200 * time.Millisecond)
}

func (s *ChannelSuite) TestStartPushReceiverDialError() {
	transport := newChannelTransport()
	safeBuf := &syncBuffer{}
	transport.writer = safeBuf

	// Unreachable URL — should not panic, will keep retrying in background.
	startPushReceiver("http://127.0.0.1:1", "ch-1", "agent-0", transport, slog.Default())
	time.Sleep(50 * time.Millisecond)
	require.Equal(s.T(), 0, safeBuf.Len())
}

func (s *ChannelSuite) TestChannelConnDelegates() {
	// Create a mock connection.
	inner := &mockConn{}
	t := newChannelTransport()
	cc := &channelConn{inner: inner, transport: t}

	// Test Read delegates.
	inner.readMsg = &jsonrpc.Request{Method: "test"}
	msg, err := cc.Read(context.Background())
	require.NoError(s.T(), err)
	require.Equal(s.T(), "test", msg.(*jsonrpc.Request).Method)

	// Test Write delegates (through mutex).
	writeMsg := &jsonrpc.Request{Method: "response"}
	require.NoError(s.T(), cc.Write(context.Background(), writeMsg))
	require.Equal(s.T(), writeMsg, inner.lastWrite)

	// Test SessionID delegates.
	inner.sessionID = "sess-123"
	require.Equal(s.T(), "sess-123", cc.SessionID())

	// Test Close delegates.
	require.NoError(s.T(), cc.Close())
	require.True(s.T(), inner.closed)
}

type mockConn struct {
	readMsg   jsonrpc.Message
	lastWrite jsonrpc.Message
	sessionID string
	closed    bool
}

func (m *mockConn) Read(_ context.Context) (jsonrpc.Message, error) {
	return m.readMsg, nil
}

func (m *mockConn) Write(_ context.Context, msg jsonrpc.Message) error {
	m.lastWrite = msg
	return nil
}

func (m *mockConn) Close() error {
	m.closed = true
	return nil
}

func (m *mockConn) SessionID() string {
	return m.sessionID
}
