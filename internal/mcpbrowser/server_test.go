package mcpbrowser

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// --- helpers ---

// setupTest creates an httptest.Server with the given handler, a Server configured to
// proxy through it, and a connected MCP client session.
func setupTest(t *testing.T, handler http.HandlerFunc) (*Server, *mcp.ClientSession) {
	t.Helper()
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	srv := New(ts.URL, "test-ch", nil)
	srv.httpClient = ts.Client()
	session := connectClient(t, srv)
	return srv, session
}

// connectClient sets up an in-memory MCP client+server session.
func connectClient(t *testing.T, srv *Server) *mcp.ClientSession {
	t.Helper()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	ctx := context.Background()

	_, err := srv.mcpServer.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { session.Close() })
	return session
}

func callTool(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      name,
		Arguments: args,
	})
	require.NoError(t, err)
	return res
}

func getText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	require.NotEmpty(t, res.Content)
	tc, ok := res.Content[0].(*mcp.TextContent)
	require.True(t, ok, "expected TextContent, got %T", res.Content[0])
	return tc.Text
}

// decodeActionRequest decodes a canned action request from the HTTP body.
func decodeActionRequest(t *testing.T, r *http.Request) (channelID, action string, params map[string]any) {
	t.Helper()
	var req struct {
		ChannelID string         `json:"channel_id"`
		Action    string         `json:"action"`
		Params    map[string]any `json:"params"`
	}
	require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
	return req.ChannelID, req.Action, req.Params
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// --- suite ---

type ServerSuite struct {
	suite.Suite
}

func TestServerSuite(t *testing.T) {
	suite.Run(t, new(ServerSuite))
}

// ==================== New / constructor ====================
