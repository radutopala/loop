package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// channelTransport wraps StdioTransport with a mutex-protected writer
// and the ability to inject channel notifications into the stdout stream.
type channelTransport struct {
	inner  mcp.Transport
	mu     sync.Mutex
	writer io.Writer
}

// newChannelTransport creates a transport that shares a mutex between
// the MCP server's normal JSON-RPC output and channel notifications.
func newChannelTransport() *channelTransport {
	return &channelTransport{
		inner:  &mcp.StdioTransport{},
		writer: os.Stdout,
	}
}

// Connect delegates to the inner transport.
func (t *channelTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	conn, err := t.inner.Connect(ctx)
	if err != nil {
		return nil, err
	}
	// Wrap the connection so writes go through our mutex.
	return &channelConn{
		inner:     conn,
		transport: t,
	}, nil
}

// WriteNotification writes a channel notification to stdout under the shared mutex.
// This is called by the push goroutine when a message arrives from the backend.
func (t *channelTransport) WriteNotification(content string, meta map[string]string) error {
	params := map[string]any{
		"content": content,
		"meta":    meta,
	}
	paramsJSON, _ := json.Marshal(params) //nolint:errcheck // params is always marshalable

	notification := &jsonrpc.Request{
		Method: "notifications/claude/channel",
		Params: paramsJSON,
	}
	data, _ := jsonrpc.EncodeMessage(notification) //nolint:errcheck // Request always encodes

	t.mu.Lock()
	defer t.mu.Unlock()
	if _, err := t.writer.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("writing notification: %w", err)
	}
	return nil
}

// channelConn wraps an MCP Connection to use the shared mutex for writes.
type channelConn struct {
	inner     mcp.Connection
	transport *channelTransport
}

func (c *channelConn) Read(ctx context.Context) (jsonrpc.Message, error) {
	return c.inner.Read(ctx)
}

func (c *channelConn) Write(ctx context.Context, msg jsonrpc.Message) error {
	// All writes go through the shared mutex to prevent interleaving
	// with channel notifications.
	c.transport.mu.Lock()
	defer c.transport.mu.Unlock()
	return c.inner.Write(ctx, msg)
}

func (c *channelConn) Close() error {
	return c.inner.Close()
}

func (c *channelConn) SessionID() string {
	return c.inner.SessionID()
}

// startPushReceiver connects to the backend WebSocket and forwards
// messages as channel notifications to the MCP stdout stream.
// It automatically reconnects when the connection drops (e.g. when the
// agent is unregistered/re-registered during a frontend tab switch).
func startPushReceiver(apiURL, channelID, agentID string, transport *channelTransport, logger *slog.Logger) {
	wsURL := "ws" + apiURL[4:] + "/api/ws/agent-channel?agent_id=" + agentID + "&channel_id=" + channelID

	go func() {
		for {
			logger.Info("channel push: connecting", "url", wsURL)
			conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
			if err != nil {
				logger.Error("channel push: dial failed, retrying", "error", err)
				time.Sleep(2 * time.Second)
				continue
			}

			// Read loop: forward messages until connection drops.
			for {
				var msg struct {
					FromAgentID string `json:"from_agent_id"`
					Content     string `json:"content"`
				}
				if err := conn.ReadJSON(&msg); err != nil {
					logger.Info("channel push: connection closed, reconnecting", "error", err)
					conn.Close()
					break
				}

				if err := transport.WriteNotification(msg.Content, map[string]string{
					"from_agent": msg.FromAgentID,
				}); err != nil {
					logger.Error("channel push: write notification failed", "error", err)
				}
			}

			time.Sleep(time.Second)
		}
	}()
}
