package mcpserver

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// --- search_memory / index_memory ---

// MCPMemorySuite tests memory tools separately because they need a server created with WithMemoryAPI.
type MCPMemorySuite struct {
	suite.Suite
	httpClient *mockHTTPClient
	srv        *Server
	ctx        context.Context
	session    *mcp.ClientSession
	cleanup    func()
}

func TestMCPMemorySuite(t *testing.T) {
	suite.Run(t, new(MCPMemorySuite))
}

func (s *MCPMemorySuite) SetupTest() {
	s.httpClient = &mockHTTPClient{}
	s.srv = New("test-channel", "http://localhost:8222", "", s.httpClient, nil,
		WithMemoryAPI("/tmp/project"),
	)
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

func (s *MCPMemorySuite) TearDownTest() {
	if s.cleanup != nil {
		s.cleanup()
	}
}

// callTool is a helper that calls a tool and returns (text, isError).
func (s *MCPMemorySuite) callTool(name string, args map[string]any) (string, bool) {
	s.T().Helper()
	res, err := s.session.CallTool(s.ctx, &mcp.CallToolParams{
		Name:      name,
		Arguments: args,
	})
	require.NoError(s.T(), err)
	require.Len(s.T(), res.Content, 1)
	return res.Content[0].(*mcp.TextContent).Text, res.IsError
}

func (s *MCPMemorySuite) TestListToolsIncludesMemory() {
	res, err := s.session.ListTools(s.ctx, nil)
	require.NoError(s.T(), err)
	require.Len(s.T(), res.Tools, 36) // 15 base + 2 memory + 3 playground + 2 shortcut + 12 quality + 2 rename

	names := make(map[string]bool)
	for _, t := range res.Tools {
		names[t.Name] = true
	}
	require.True(s.T(), names["search_memory"])
	require.True(s.T(), names["index_memory"])
}

func (s *MCPMemorySuite) TestSearchMemorySuccess() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "POST", req.Method)
		require.Contains(s.T(), req.URL.String(), "/api/memory/search")
		body, _ := io.ReadAll(req.Body)
		require.Contains(s.T(), string(body), `"query":"docker cleanup"`)
		require.Contains(s.T(), string(body), `"top_k":3`)
		require.Contains(s.T(), string(body), `"dir_path":"/tmp/project"`)
		return jsonResponse(http.StatusOK, `{"results":[{"file_path":"/tmp/memory/MEMORY.md","content":"Container cleanup tips","score":0.95},{"file_path":"/tmp/memory/debugging.md","content":"Debug notes","score":0.82}]}`), nil
	}

	text, isError := s.callTool("search_memory", map[string]any{
		"query": "docker cleanup",
		"top_k": float64(3),
	})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "score: 0.950")
	require.Contains(s.T(), text, "MEMORY.md")
	require.Contains(s.T(), text, "Container cleanup tips")
	require.Contains(s.T(), text, "debugging.md")
}

func (s *MCPMemorySuite) TestSearchMemoryEmptyResults() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"results":[]}`), nil
	}

	text, isError := s.callTool("search_memory", map[string]any{"query": "nonexistent topic"})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "No results found")
}

func (s *MCPMemorySuite) TestSearchMemoryEmptyQuery() {
	text, isError := s.callTool("search_memory", map[string]any{"query": ""})
	require.True(s.T(), isError)
	require.Contains(s.T(), text, "query is required")
}

func (s *MCPMemorySuite) TestSearchMemoryErrors() {
	runToolErrorCases(&s.Suite, s.httpClient, s.callTool, toolErrorSpec{
		tool:         "search_memory",
		args:         map[string]any{"query": "test"},
		apiStatus:    http.StatusInternalServerError,
		apiBody:      "indexer error",
		decodeStatus: http.StatusOK,
	})
}

func (s *MCPMemorySuite) TestSearchMemorySingleResult() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"results":[{"file_path":"/tmp/memory/notes.md","content":"Some notes","score":0.75}]}`), nil
	}

	text, isError := s.callTool("search_memory", map[string]any{"query": "notes"})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "notes.md")
	require.Contains(s.T(), text, "Some notes")
}

func (s *MCPMemorySuite) TestSearchMemoryResultWithoutContent() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{"results":[{"file_path":"/tmp/memory/MEMORY.md","content":"","score":0.90}]}`), nil
	}

	text, isError := s.callTool("search_memory", map[string]any{"query": "memory"})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "MEMORY.md")
	require.Contains(s.T(), text, "score: 0.900")
}

func (s *MCPMemorySuite) TestIndexMemorySuccess() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "POST", req.Method)
		require.Contains(s.T(), req.URL.String(), "/api/memory/index")
		body, _ := io.ReadAll(req.Body)
		require.Contains(s.T(), string(body), `"dir_path":"/tmp/project"`)
		return jsonResponse(http.StatusOK, `{"count":15}`), nil
	}

	text, isError := s.callTool("index_memory", map[string]any{})
	require.False(s.T(), isError)
	require.Contains(s.T(), text, "Indexed 15 chunks")
}

func (s *MCPMemorySuite) TestIndexMemoryErrors() {
	tests := []struct {
		name     string
		doFunc   func(*http.Request) (*http.Response, error)
		wantText string
	}{
		{"API error", func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusInternalServerError, "disk full"), nil
		}, "API error"},
		{"HTTP error", func(*http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("connection refused")
		}, "calling API"},
		{"invalid response JSON", func(*http.Request) (*http.Response, error) {
			return jsonResponse(http.StatusOK, "not json"), nil
		}, "decoding response"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.httpClient.doFunc = tt.doFunc
			text, isError := s.callTool("index_memory", map[string]any{})
			require.True(s.T(), isError)
			require.Contains(s.T(), text, tt.wantText)
		})
	}
}

func (s *MCPMemorySuite) TestWithMemoryAPIOption() {
	require.True(s.T(), s.srv.memoryEnabled)
	require.Equal(s.T(), "/tmp/project", s.srv.dirPath)
}

func (s *MCPMemorySuite) TestDirPath() {
	require.Equal(s.T(), "/tmp/project", s.srv.DirPath())
}

// MCPMemoryChannelIDSuite tests memory tools when only channel_id is available (no dirPath).
type MCPMemoryChannelIDSuite struct {
	suite.Suite
	httpClient *mockHTTPClient
	srv        *Server
	ctx        context.Context
	session    *mcp.ClientSession
	cleanup    func()
}

func TestMCPMemoryChannelIDSuite(t *testing.T) {
	suite.Run(t, new(MCPMemoryChannelIDSuite))
}

func (s *MCPMemoryChannelIDSuite) SetupTest() {
	s.httpClient = &mockHTTPClient{}
	s.srv = New("test-channel", "http://localhost:8222", "", s.httpClient, nil,
		WithMemoryAPI(""),
	)
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

func (s *MCPMemoryChannelIDSuite) TearDownTest() {
	if s.cleanup != nil {
		s.cleanup()
	}
}

func (s *MCPMemoryChannelIDSuite) TestMemoryEnabledWithEmptyDirPath() {
	require.True(s.T(), s.srv.memoryEnabled)
	require.Empty(s.T(), s.srv.dirPath)
}

func (s *MCPMemoryChannelIDSuite) TestListToolsIncludesMemory() {
	res, err := s.session.ListTools(s.ctx, nil)
	require.NoError(s.T(), err)
	require.Len(s.T(), res.Tools, 36)

	names := make(map[string]bool)
	for _, t := range res.Tools {
		names[t.Name] = true
	}
	require.True(s.T(), names["search_memory"])
	require.True(s.T(), names["index_memory"])
}

func (s *MCPMemoryChannelIDSuite) TestSearchMemorySendsChannelID() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "POST", req.Method)
		require.Contains(s.T(), req.URL.String(), "/api/memory/search")
		body, _ := io.ReadAll(req.Body)
		require.Contains(s.T(), string(body), `"channel_id":"test-channel"`)
		require.NotContains(s.T(), string(body), `"dir_path"`)
		return jsonResponse(http.StatusOK, `{"results":[]}`), nil
	}

	res, err := s.session.CallTool(s.ctx, &mcp.CallToolParams{
		Name:      "search_memory",
		Arguments: map[string]any{"query": "test"},
	})
	require.NoError(s.T(), err)
	require.False(s.T(), res.IsError)
}

func (s *MCPMemoryChannelIDSuite) TestIndexMemorySendsChannelID() {
	s.httpClient.doFunc = func(req *http.Request) (*http.Response, error) {
		require.Equal(s.T(), "POST", req.Method)
		require.Contains(s.T(), req.URL.String(), "/api/memory/index")
		body, _ := io.ReadAll(req.Body)
		require.Contains(s.T(), string(body), `"channel_id":"test-channel"`)
		require.NotContains(s.T(), string(body), `"dir_path"`)
		return jsonResponse(http.StatusOK, `{"count":5}`), nil
	}

	res, err := s.session.CallTool(s.ctx, &mcp.CallToolParams{
		Name:      "index_memory",
		Arguments: map[string]any{},
	})
	require.NoError(s.T(), err)
	require.False(s.T(), res.IsError)
}

// --- doRequest ---

func (s *MCPServerSuite) TestDoRequestInvalidMethod() {
	_, _, err := s.srv.doRequest("INVALID METHOD", "http://localhost", nil)
	require.Error(s.T(), err)
}

// --- errorResult ---

func (s *MCPServerSuite) TestErrorResult() {
	result := errorResult("test error")
	require.True(s.T(), result.IsError)
	require.Len(s.T(), result.Content, 1)
	require.Equal(s.T(), "test error", result.Content[0].(*mcp.TextContent).Text)
}
