package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type AgentsToolsSuite struct {
	suite.Suite
	srv    *Server
	apiSrv *httptest.Server
}

func TestAgentsToolsSuite(t *testing.T) {
	suite.Run(t, new(AgentsToolsSuite))
}

func (s *AgentsToolsSuite) SetupTest() {
	s.apiSrv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/agents":
			agents := []map[string]string{
				{"agent_id": "agent-0", "name": "Alpha", "status": "running", "work_summary": "indexing"},
				{"agent_id": "agent-1", "name": "Beta", "status": "idle", "work_summary": ""},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(agents) //nolint:errcheck
		case r.Method == "POST" && r.URL.Path == "/api/agents/agent-1/message":
			w.WriteHeader(http.StatusNoContent)
		case r.Method == "POST" && r.URL.Path == "/api/agents/nonexistent/message":
			w.WriteHeader(http.StatusNotFound)
		case r.Method == "PATCH" && r.URL.Path == "/api/agents/agent-0":
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, `{"status":"ok"}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))

	s.srv = New("ch-1", s.apiSrv.URL, "author-1", s.apiSrv.Client(), nil, WithAgentTools("agent-0"))
}

func (s *AgentsToolsSuite) TearDownTest() {
	s.apiSrv.Close()
}

func (s *AgentsToolsSuite) TestListAgents() {
	result, _, err := s.srv.handleListAgents(context.TODO(), nil, listAgentsInput{})
	require.NoError(s.T(), err)
	require.False(s.T(), result.IsError)
	text := result.Content[0].(*mcp.TextContent).Text
	require.Contains(s.T(), text, "2 agent(s)")
	require.Contains(s.T(), text, "* [agent-0]") // current agent marked
	require.Contains(s.T(), text, "  [agent-1]")
	require.Contains(s.T(), text, "(indexing)")
}

func (s *AgentsToolsSuite) TestListAgentsEmpty() {
	emptySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `[]`)
	}))
	defer emptySrv.Close()

	srv := New("ch-1", emptySrv.URL, "author-1", emptySrv.Client(), nil, WithAgentTools("agent-0"))
	result, _, err := srv.handleListAgents(context.TODO(), nil, listAgentsInput{})
	require.NoError(s.T(), err)
	require.Contains(s.T(), result.Content[0].(*mcp.TextContent).Text, "No agents active")
}

func (s *AgentsToolsSuite) TestListAgentsError() {
	srv := New("ch-1", "http://127.0.0.1:1", "author-1", http.DefaultClient, nil, WithAgentTools("agent-0"))
	result, _, err := srv.handleListAgents(context.TODO(), nil, listAgentsInput{})
	require.NoError(s.T(), err)
	require.True(s.T(), result.IsError)
}

func (s *AgentsToolsSuite) TestSendAgentMessage() {
	result, _, err := s.srv.handleSendAgentMessage(context.TODO(), nil, sendAgentMessageInput{
		ToAgentID: "agent-1",
		Content:   "hello",
	})
	require.NoError(s.T(), err)
	require.False(s.T(), result.IsError)
	require.Contains(s.T(), result.Content[0].(*mcp.TextContent).Text, "Message sent to agent-1")
}

func (s *AgentsToolsSuite) TestSendAgentMessageMissingFields() {
	result, _, err := s.srv.handleSendAgentMessage(context.TODO(), nil, sendAgentMessageInput{})
	require.NoError(s.T(), err)
	require.True(s.T(), result.IsError)
	require.Contains(s.T(), result.Content[0].(*mcp.TextContent).Text, "required")
}

func (s *AgentsToolsSuite) TestSendAgentMessageNotFound() {
	result, _, err := s.srv.handleSendAgentMessage(context.TODO(), nil, sendAgentMessageInput{
		ToAgentID: "nonexistent",
		Content:   "hello",
	})
	require.NoError(s.T(), err)
	require.True(s.T(), result.IsError)
	require.Contains(s.T(), result.Content[0].(*mcp.TextContent).Text, "HTTP 404")
}

func (s *AgentsToolsSuite) TestSendAgentMessageError() {
	srv := New("ch-1", "http://127.0.0.1:1", "author-1", http.DefaultClient, nil, WithAgentTools("agent-0"))
	result, _, err := srv.handleSendAgentMessage(context.TODO(), nil, sendAgentMessageInput{
		ToAgentID: "agent-1",
		Content:   "hello",
	})
	require.NoError(s.T(), err)
	require.True(s.T(), result.IsError)
}

func (s *AgentsToolsSuite) TestUpdateAgentStatus() {
	result, _, err := s.srv.handleUpdateAgentStatus(context.TODO(), nil, updateAgentStatusInput{
		Name:        "Worker",
		WorkSummary: "indexing files",
	})
	require.NoError(s.T(), err)
	require.False(s.T(), result.IsError)
	require.Contains(s.T(), result.Content[0].(*mcp.TextContent).Text, "status updated")
}

func (s *AgentsToolsSuite) TestUpdateAgentStatusMissingFields() {
	result, _, err := s.srv.handleUpdateAgentStatus(context.TODO(), nil, updateAgentStatusInput{})
	require.NoError(s.T(), err)
	require.True(s.T(), result.IsError)
	require.Contains(s.T(), result.Content[0].(*mcp.TextContent).Text, "required")
}

func (s *AgentsToolsSuite) TestUpdateAgentStatusHTTPError() {
	errSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer errSrv.Close()

	srv := New("ch-1", errSrv.URL, "author-1", errSrv.Client(), nil, WithAgentTools("agent-0"))
	result, _, err := srv.handleUpdateAgentStatus(context.TODO(), nil, updateAgentStatusInput{Name: "x"})
	require.NoError(s.T(), err)
	require.True(s.T(), result.IsError)
	require.Contains(s.T(), result.Content[0].(*mcp.TextContent).Text, "HTTP 500")
}

func (s *AgentsToolsSuite) TestUpdateAgentStatusError() {
	srv := New("ch-1", "http://127.0.0.1:1", "author-1", http.DefaultClient, nil, WithAgentTools("agent-0"))
	result, _, err := srv.handleUpdateAgentStatus(context.TODO(), nil, updateAgentStatusInput{Name: "x"})
	require.NoError(s.T(), err)
	require.True(s.T(), result.IsError)
}

func (s *AgentsToolsSuite) TestWithAgentToolsSetsID() {
	require.Equal(s.T(), "agent-0", s.srv.agentID)
}

func (s *AgentsToolsSuite) TestRegisterAgent() {
	var called bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(s.T(), "POST", r.Method)
		require.Equal(s.T(), "/api/agents", r.URL.Path)
		var body map[string]string
		require.NoError(s.T(), json.NewDecoder(r.Body).Decode(&body))
		require.Equal(s.T(), "ch-1", body["channel_id"])
		require.Equal(s.T(), "agent-0", body["agent_id"])
		require.Equal(s.T(), "idle", body["status"])
		called = true
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	mcpSrv := New("ch-1", srv.URL, "author-1", srv.Client(), nil, WithAgentTools("agent-0"))
	mcpSrv.RegisterAgent()
	require.True(s.T(), called)
}

func (s *AgentsToolsSuite) TestRegisterAgentNoAgentID() {
	srv := New("ch-1", "http://127.0.0.1:1", "author-1", http.DefaultClient, nil)
	srv.RegisterAgent() // should not panic
}

func (s *AgentsToolsSuite) TestRegisterAgentError() {
	srv := New("ch-1", "http://127.0.0.1:1", "author-1", http.DefaultClient, nil, WithAgentTools("agent-0"))
	srv.RegisterAgent() // unreachable — should log warning, not panic
}

func (s *AgentsToolsSuite) TestRegisterAgentBadStatus() {
	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "error", http.StatusInternalServerError)
	}))
	defer apiSrv.Close()

	srv := New("ch-1", apiSrv.URL, "author-1", apiSrv.Client(), nil, WithAgentTools("agent-0"))
	srv.RegisterAgent() // should log warning, not panic
}

func (s *AgentsToolsSuite) TestUnregisterAgent() {
	var called bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(s.T(), "DELETE", r.Method)
		require.Contains(s.T(), r.URL.Path, "/api/agents/agent-0")
		require.Equal(s.T(), "ch-1", r.URL.Query().Get("channel_id"))
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	mcpSrv := New("ch-1", srv.URL, "author-1", srv.Client(), nil, WithAgentTools("agent-0"))
	mcpSrv.UnregisterAgent()
	require.True(s.T(), called)
}

func (s *AgentsToolsSuite) TestUnregisterAgentNoAgentID() {
	srv := New("ch-1", "http://127.0.0.1:1", "author-1", http.DefaultClient, nil)
	// Should not panic or make any request.
	srv.UnregisterAgent()
}

func (s *AgentsToolsSuite) TestUnregisterAgentError() {
	srv := New("ch-1", "http://127.0.0.1:1", "author-1", http.DefaultClient, nil, WithAgentTools("agent-0"))
	// Unreachable URL — should log warning, not panic.
	srv.UnregisterAgent()
}

func (s *AgentsToolsSuite) TestUnregisterAgentBadStatus() {
	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer apiSrv.Close()

	srv := New("ch-1", apiSrv.URL, "author-1", apiSrv.Client(), nil, WithAgentTools("agent-0"))
	// Should log warning about unexpected status, not panic.
	srv.UnregisterAgent()
}
