package mcpserver

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type mockHTTPClient struct {
	doFunc func(req *http.Request) (*http.Response, error)
}

func (m *mockHTTPClient) Do(req *http.Request) (*http.Response, error) {
	return m.doFunc(req)
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(bytes.NewBufferString(body)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}
}

type MCPServerSuite struct {
	suite.Suite
	httpClient *mockHTTPClient
	srv        *Server
	ctx        context.Context
	session    *mcp.ClientSession
	cleanup    func()
}

func TestMCPServerSuite(t *testing.T) {
	suite.Run(t, new(MCPServerSuite))
}

func (s *MCPServerSuite) SetupTest() {
	s.httpClient = &mockHTTPClient{}
	s.srv = New("test-channel", "http://localhost:8222", "", s.httpClient, nil)
	s.ctx = context.Background()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1.0.0"}, nil)
	t1, t2 := mcp.NewInMemoryTransports()

	go func() {
		_ = s.srv.Run(s.ctx, t1)
	}()

	session, err := client.Connect(s.ctx, t2, nil)
	require.NoError(s.T(), err)

	s.session = session
	s.cleanup = func() {
		session.Close()
	}
}

func (s *MCPServerSuite) TearDownTest() {
	if s.cleanup != nil {
		s.cleanup()
	}
}

// callTool is a helper that calls a tool and returns (text, isError).
func (s *MCPServerSuite) callTool(name string, args map[string]any) (string, bool) {
	s.T().Helper()
	res, err := s.session.CallTool(s.ctx, &mcp.CallToolParams{
		Name:      name,
		Arguments: args,
	})
	require.NoError(s.T(), err)
	require.Len(s.T(), res.Content, 1)
	return res.Content[0].(*mcp.TextContent).Text, res.IsError
}

// noContentResponse returns an empty response with the given status code.
func noContentResponse(status int) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(bytes.NewReader(nil)),
	}
}

// stringResponse returns a response with the given status and body string.
func stringResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(bytes.NewReader([]byte(body))),
	}
}

func (s *MCPServerSuite) TestNew() {
	require.NotNil(s.T(), s.srv)
	require.Equal(s.T(), "test-channel", s.srv.channelID)
	require.Equal(s.T(), "http://localhost:8222", s.srv.apiURL)
	require.NotNil(s.T(), s.srv.mcpServer)
}

// TestRunWithChannelTransport covers Server.Run's channelTransport != nil
// branch: when WithAgentTools is set, Run threads the supplied transport
// into channelTransport.inner, starts the push-receiver goroutine, and
// cancels the receiver ctx on return so no goroutine leaks.
func (s *MCPServerSuite) TestRunWithChannelTransport() {
	srv := New("ch-1", "http://127.0.0.1:1", "author-1", http.DefaultClient, nil, WithAgentTools("agent-0"))
	// Tiny backoffs so the receiver goroutine cycles quickly during the test.
	srv.channelTransport.dialBackoff = 5 * time.Millisecond
	srv.channelTransport.reconnectDelay = 5 * time.Millisecond

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1.0.0"}, nil)
	t1, t2 := mcp.NewInMemoryTransports()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runDone := make(chan error, 1)
	go func() {
		runDone <- srv.Run(ctx, t1)
	}()

	session, err := client.Connect(ctx, t2, nil)
	require.NoError(s.T(), err)

	// Verify the threaded transport actually serves MCP traffic.
	res, err := session.ListTools(ctx, nil)
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), res.Tools)

	require.NoError(s.T(), session.Close())
	cancel()

	select {
	case <-runDone:
	case <-time.After(2 * time.Second):
		s.T().Fatal("Run did not return after ctx cancel + session close")
	}
}

func (s *MCPServerSuite) TestMCPServer() {
	require.Equal(s.T(), s.srv.mcpServer, s.srv.MCPServer())
}

// toolErrorCase is one standard failure-mode case run by runToolErrorCases.
type toolErrorCase struct {
	name     string
	doFunc   func(*http.Request) (*http.Response, error)
	wantText string
}

// toolErrorSpec drives runToolErrorCases: the tool + args to invoke and the
// shape of the standard failure responses. decodeStatus, when non-zero, adds
// the "invalid response JSON" case — a malformed body on an otherwise-
// successful response with that status — for tools that decode their reply.
type toolErrorSpec struct {
	tool         string
	args         map[string]any
	apiStatus    int
	apiBody      string
	decodeStatus int
}

// runToolErrorCases exercises the failure modes every MCP tool handles the
// same way: a non-2xx API response ("API error"), a transport failure
// ("calling API"), and optionally a malformed success body ("decoding
// response"). Each Test<Tool>Errors used to spell these cases out by hand;
// they differ only in the spec fields.
func runToolErrorCases(s *suite.Suite, client *mockHTTPClient, callTool func(string, map[string]any) (string, bool), spec toolErrorSpec) {
	cases := []toolErrorCase{
		{"API error", func(*http.Request) (*http.Response, error) {
			return jsonResponse(spec.apiStatus, spec.apiBody), nil
		}, "API error"},
		{"HTTP error", func(*http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("connection refused")
		}, "calling API"},
	}
	if spec.decodeStatus != 0 {
		cases = append(cases, toolErrorCase{"invalid response JSON", func(*http.Request) (*http.Response, error) {
			return jsonResponse(spec.decodeStatus, "not json"), nil
		}, "decoding response"})
	}
	for _, tt := range cases {
		s.Run(tt.name, func() {
			client.doFunc = tt.doFunc
			text, isError := callTool(spec.tool, spec.args)
			require.True(s.T(), isError)
			require.Contains(s.T(), text, tt.wantText)
		})
	}
}
