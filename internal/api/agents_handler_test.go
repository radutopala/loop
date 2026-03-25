package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/agentregistry"
)

type AgentsHandlerSuite struct {
	suite.Suite
	srv *Server
	reg *agentregistry.Registry
}

func TestAgentsHandlerSuite(t *testing.T) {
	suite.Run(t, new(AgentsHandlerSuite))
}

func (s *AgentsHandlerSuite) SetupTest() {
	s.srv = nilServer()
	s.reg = agentregistry.New()
	s.srv.SetAgentRegistry(s.reg)
}

// --- SetAgentRegistry ---

func (s *AgentsHandlerSuite) TestSetAgentRegistry() {
	srv := nilServer()
	require.Nil(s.T(), srv.agentRegistry)
	reg := agentregistry.New()
	srv.SetAgentRegistry(reg)
	require.NotNil(s.T(), srv.agentRegistry)
}

// --- handleListAgents ---

func (s *AgentsHandlerSuite) TestListAgentsSuccess() {
	s.reg.Register(&agentregistry.AgentInfo{AgentID: "a-0", ChannelID: "ch-1", Name: "Alpha"})
	s.reg.Register(&agentregistry.AgentInfo{AgentID: "a-1", ChannelID: "ch-1", Name: "Beta"})

	req := httptest.NewRequest("GET", "/api/agents?channel_id=ch-1", nil)
	w := httptest.NewRecorder()
	s.srv.handleListAgents(w, req)

	require.Equal(s.T(), http.StatusOK, w.Code)
	var agents []*agentregistry.AgentInfo
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &agents))
	require.Len(s.T(), agents, 2)
}

func (s *AgentsHandlerSuite) TestListAgentsEmpty() {
	req := httptest.NewRequest("GET", "/api/agents?channel_id=ch-1", nil)
	w := httptest.NewRecorder()
	s.srv.handleListAgents(w, req)

	require.Equal(s.T(), http.StatusOK, w.Code)
	var agents []*agentregistry.AgentInfo
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &agents))
	require.Empty(s.T(), agents)
}

func (s *AgentsHandlerSuite) TestListAgentsMissingChannelID() {
	req := httptest.NewRequest("GET", "/api/agents", nil)
	w := httptest.NewRecorder()
	s.srv.handleListAgents(w, req)
	require.Equal(s.T(), http.StatusBadRequest, w.Code)
}

func (s *AgentsHandlerSuite) TestListAgentsNotConfigured() {
	srv := nilServer()
	req := httptest.NewRequest("GET", "/api/agents?channel_id=ch-1", nil)
	w := httptest.NewRecorder()
	srv.handleListAgents(w, req)
	require.Equal(s.T(), http.StatusServiceUnavailable, w.Code)
}

// --- handleUpdateAgent ---

func (s *AgentsHandlerSuite) TestUpdateAgentSuccess() {
	s.reg.Register(&agentregistry.AgentInfo{AgentID: "a-0", ChannelID: "ch-1", Status: "idle"})
	s.srv.SetEventsHub(NewEventsHub(slog.Default()))

	body := `{"channel_id":"ch-1","status":"running","work_summary":"indexing","name":"Worker"}`
	req := httptest.NewRequest("PATCH", "/api/agents/a-0", strings.NewReader(body))
	req.SetPathValue("id", "a-0")
	w := httptest.NewRecorder()
	s.srv.handleUpdateAgent(w, req)

	require.Equal(s.T(), http.StatusOK, w.Code)
	var updated agentregistry.AgentInfo
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &updated))
	require.Equal(s.T(), "running", updated.Status)
	require.Equal(s.T(), "indexing", updated.WorkSummary)
	require.Equal(s.T(), "Worker", updated.Name)
}

func (s *AgentsHandlerSuite) TestUpdateAgentNotFound() {
	body := `{"channel_id":"ch-1","status":"running"}`
	req := httptest.NewRequest("PATCH", "/api/agents/nope", strings.NewReader(body))
	req.SetPathValue("id", "nope")
	w := httptest.NewRecorder()
	s.srv.handleUpdateAgent(w, req)
	require.Equal(s.T(), http.StatusNotFound, w.Code)
}

func (s *AgentsHandlerSuite) TestUpdateAgentMissingChannelID() {
	body := `{"status":"running"}`
	req := httptest.NewRequest("PATCH", "/api/agents/a-0", strings.NewReader(body))
	req.SetPathValue("id", "a-0")
	w := httptest.NewRecorder()
	s.srv.handleUpdateAgent(w, req)
	require.Equal(s.T(), http.StatusBadRequest, w.Code)
}

func (s *AgentsHandlerSuite) TestUpdateAgentInvalidJSON() {
	req := httptest.NewRequest("PATCH", "/api/agents/a-0", strings.NewReader("{bad"))
	req.SetPathValue("id", "a-0")
	w := httptest.NewRecorder()
	s.srv.handleUpdateAgent(w, req)
	require.Equal(s.T(), http.StatusBadRequest, w.Code)
}

func (s *AgentsHandlerSuite) TestUpdateAgentMissingID() {
	body := `{"channel_id":"ch-1","status":"running"}`
	req := httptest.NewRequest("PATCH", "/api/agents/", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.srv.handleUpdateAgent(w, req)
	require.Equal(s.T(), http.StatusBadRequest, w.Code)
}

func (s *AgentsHandlerSuite) TestUpdateAgentNotConfigured() {
	srv := nilServer()
	body := `{"channel_id":"ch-1","status":"running"}`
	req := httptest.NewRequest("PATCH", "/api/agents/a-0", strings.NewReader(body))
	req.SetPathValue("id", "a-0")
	w := httptest.NewRecorder()
	srv.handleUpdateAgent(w, req)
	require.Equal(s.T(), http.StatusServiceUnavailable, w.Code)
}

// --- handleSendAgentMessage ---

func (s *AgentsHandlerSuite) TestSendMessageSuccess() {
	s.reg.Register(&agentregistry.AgentInfo{AgentID: "a-0", ChannelID: "ch-1"})
	s.reg.Register(&agentregistry.AgentInfo{AgentID: "a-1", ChannelID: "ch-1"})

	body := `{"channel_id":"ch-1","from_agent_id":"a-0","content":"hello"}`
	req := httptest.NewRequest("POST", "/api/agents/a-1/message", strings.NewReader(body))
	req.SetPathValue("id", "a-1")
	w := httptest.NewRecorder()
	s.srv.handleSendAgentMessage(w, req)

	require.Equal(s.T(), http.StatusNoContent, w.Code)
}

func (s *AgentsHandlerSuite) TestSendMessageTargetNotFound() {
	body := `{"channel_id":"ch-1","from_agent_id":"a-0","content":"hello"}`
	req := httptest.NewRequest("POST", "/api/agents/nope/message", strings.NewReader(body))
	req.SetPathValue("id", "nope")
	w := httptest.NewRecorder()
	s.srv.handleSendAgentMessage(w, req)
	require.Equal(s.T(), http.StatusNotFound, w.Code)
}

func (s *AgentsHandlerSuite) TestSendMessageMissingFields() {
	body := `{"channel_id":"ch-1"}`
	req := httptest.NewRequest("POST", "/api/agents/a-1/message", strings.NewReader(body))
	req.SetPathValue("id", "a-1")
	w := httptest.NewRecorder()
	s.srv.handleSendAgentMessage(w, req)
	require.Equal(s.T(), http.StatusBadRequest, w.Code)
}

func (s *AgentsHandlerSuite) TestSendMessageInvalidJSON() {
	req := httptest.NewRequest("POST", "/api/agents/a-1/message", strings.NewReader("{bad"))
	req.SetPathValue("id", "a-1")
	w := httptest.NewRecorder()
	s.srv.handleSendAgentMessage(w, req)
	require.Equal(s.T(), http.StatusBadRequest, w.Code)
}

func (s *AgentsHandlerSuite) TestSendMessageMissingID() {
	body := `{"channel_id":"ch-1","from_agent_id":"a-0","content":"hello"}`
	req := httptest.NewRequest("POST", "/api/agents//message", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.srv.handleSendAgentMessage(w, req)
	require.Equal(s.T(), http.StatusBadRequest, w.Code)
}

func (s *AgentsHandlerSuite) TestSendMessageNotConfigured() {
	srv := nilServer()
	body := `{"channel_id":"ch-1","from_agent_id":"a-0","content":"hello"}`
	req := httptest.NewRequest("POST", "/api/agents/a-1/message", strings.NewReader(body))
	req.SetPathValue("id", "a-1")
	w := httptest.NewRecorder()
	srv.handleSendAgentMessage(w, req)
	require.Equal(s.T(), http.StatusServiceUnavailable, w.Code)
}

// --- handleAgentChannelWS ---

func (s *AgentsHandlerSuite) TestAgentChannelWSSuccess() {
	s.reg.Register(&agentregistry.AgentInfo{AgentID: "a-0", ChannelID: "ch-1"})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/ws/agent-channel", s.srv.handleAgentChannelWS)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/ws/agent-channel?agent_id=a-0&channel_id=ch-1"
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(s.T(), err)
	defer ws.Close()

	// Send a message to the agent.
	require.NoError(s.T(), s.reg.SendMessage("ch-1", "a-1", "a-0", "hello"))

	// Read the message from WebSocket.
	var msg agentregistry.AgentMessage
	require.NoError(s.T(), ws.ReadJSON(&msg))
	require.Equal(s.T(), "a-1", msg.FromAgentID)
	require.Equal(s.T(), "hello", msg.Content)
}

func (s *AgentsHandlerSuite) TestAgentChannelWSClosesOnUnregister() {
	s.reg.Register(&agentregistry.AgentInfo{AgentID: "a-0", ChannelID: "ch-1"})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/ws/agent-channel", s.srv.handleAgentChannelWS)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/ws/agent-channel?agent_id=a-0&channel_id=ch-1"
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(s.T(), err)
	defer ws.Close()

	// Unregister the agent — WebSocket should close.
	s.reg.Unregister("ch-1", "a-0")

	// Reading should return an error (connection closed).
	_, _, err = ws.ReadMessage()
	require.Error(s.T(), err)
}

func (s *AgentsHandlerSuite) TestAgentChannelWSMissingParams() {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/ws/agent-channel", s.srv.handleAgentChannelWS)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/ws/agent-channel")
	require.NoError(s.T(), err)
	defer resp.Body.Close()
	require.Equal(s.T(), http.StatusBadRequest, resp.StatusCode)
}

func (s *AgentsHandlerSuite) TestAgentChannelWSAgentNotFound() {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/ws/agent-channel", s.srv.handleAgentChannelWS)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/ws/agent-channel?agent_id=nope&channel_id=ch-1")
	require.NoError(s.T(), err)
	defer resp.Body.Close()
	require.Equal(s.T(), http.StatusNotFound, resp.StatusCode)
}

func (s *AgentsHandlerSuite) TestAgentChannelWSUpgradeFail() {
	// Agent exists but request is a regular HTTP GET (not WS upgrade).
	s.reg.Register(&agentregistry.AgentInfo{AgentID: "a-0", ChannelID: "ch-1"})
	s.srv.logger = slog.Default()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/ws/agent-channel", s.srv.handleAgentChannelWS)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/ws/agent-channel?agent_id=a-0&channel_id=ch-1")
	require.NoError(s.T(), err)
	defer resp.Body.Close()
	// Upgrade fails — returns 400 (Bad Request from gorilla/websocket).
	require.Equal(s.T(), http.StatusBadRequest, resp.StatusCode)
}

func (s *AgentsHandlerSuite) TestAgentChannelWSNotConfigured() {
	srv := nilServer()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/ws/agent-channel", srv.handleAgentChannelWS)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/ws/agent-channel?agent_id=a-0&channel_id=ch-1")
	require.NoError(s.T(), err)
	defer resp.Body.Close()
	require.Equal(s.T(), http.StatusServiceUnavailable, resp.StatusCode)
}

// --- integration: send + receive via WS ---

func (s *AgentsHandlerSuite) TestAgentChannelWSMultipleMessages() {
	s.reg.Register(&agentregistry.AgentInfo{AgentID: "a-0", ChannelID: "ch-1"})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/ws/agent-channel", s.srv.handleAgentChannelWS)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/ws/agent-channel?agent_id=a-0&channel_id=ch-1"
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(s.T(), err)
	defer ws.Close()

	// Send 3 messages.
	for i := range 3 {
		require.NoError(s.T(), s.reg.SendMessage("ch-1", "sender", "a-0", strings.Repeat("x", i+1)))
	}

	// Read all 3.
	for i := range 3 {
		require.NoError(s.T(), ws.SetReadDeadline(time.Now().Add(time.Second)))
		var msg agentregistry.AgentMessage
		require.NoError(s.T(), ws.ReadJSON(&msg))
		require.Equal(s.T(), strings.Repeat("x", i+1), msg.Content)
	}
}

func (s *AgentsHandlerSuite) TestDeleteAgent() {
	s.reg.Register(&agentregistry.AgentInfo{AgentID: "a-0", ChannelID: "ch-1", Name: "a-0"})
	require.NotNil(s.T(), s.reg.Get("ch-1", "a-0"))

	req := httptest.NewRequest("DELETE", "/api/agents/a-0?channel_id=ch-1", nil)
	req.SetPathValue("id", "a-0")
	w := httptest.NewRecorder()
	s.srv.handleDeleteAgent(w, req)

	require.Equal(s.T(), http.StatusNoContent, w.Code)
	require.Nil(s.T(), s.reg.Get("ch-1", "a-0"))
}

func (s *AgentsHandlerSuite) TestDeleteAgentMissingParams() {
	req := httptest.NewRequest("DELETE", "/api/agents/a-0", nil)
	req.SetPathValue("id", "a-0")
	w := httptest.NewRecorder()
	s.srv.handleDeleteAgent(w, req)

	require.Equal(s.T(), http.StatusBadRequest, w.Code)
}

func (s *AgentsHandlerSuite) TestDeleteAgentNoRegistry() {
	srv := nilServer()
	req := httptest.NewRequest("DELETE", "/api/agents/a-0?channel_id=ch-1", nil)
	req.SetPathValue("id", "a-0")
	w := httptest.NewRecorder()
	srv.handleDeleteAgent(w, req)
	require.Equal(s.T(), http.StatusServiceUnavailable, w.Code)
}

func (s *AgentsHandlerSuite) TestDeleteAgentBroadcastsEvent() {
	hub := NewEventsHub(slog.Default())
	s.srv.SetEventsHub(hub)
	s.reg.Register(&agentregistry.AgentInfo{AgentID: "a-0", ChannelID: "ch-1", Name: "a-0"})

	req := httptest.NewRequest("DELETE", "/api/agents/a-0?channel_id=ch-1", nil)
	req.SetPathValue("id", "a-0")
	w := httptest.NewRecorder()
	s.srv.handleDeleteAgent(w, req)

	require.Equal(s.T(), http.StatusNoContent, w.Code)
	require.Nil(s.T(), s.reg.Get("ch-1", "a-0"))
}

func (s *AgentsHandlerSuite) TestRegisterAgent() {
	body := `{"channel_id":"ch-1","agent_id":"a-0","name":"a-0","status":"idle"}`
	req := httptest.NewRequest("POST", "/api/agents", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.srv.handleRegisterAgent(w, req)

	require.Equal(s.T(), http.StatusCreated, w.Code)
	agent := s.reg.Get("ch-1", "a-0")
	require.NotNil(s.T(), agent)
	require.Equal(s.T(), "idle", agent.Status)
}

func (s *AgentsHandlerSuite) TestRegisterAgentMissingFields() {
	body := `{"channel_id":"ch-1"}`
	req := httptest.NewRequest("POST", "/api/agents", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.srv.handleRegisterAgent(w, req)

	require.Equal(s.T(), http.StatusBadRequest, w.Code)
}

func (s *AgentsHandlerSuite) TestRegisterAgentInvalidJSON() {
	req := httptest.NewRequest("POST", "/api/agents", strings.NewReader("not json"))
	w := httptest.NewRecorder()
	s.srv.handleRegisterAgent(w, req)

	require.Equal(s.T(), http.StatusBadRequest, w.Code)
}

func (s *AgentsHandlerSuite) TestRegisterAgentBroadcastsEvent() {
	hub := NewEventsHub(slog.Default())
	s.srv.SetEventsHub(hub)

	body := `{"channel_id":"ch-1","agent_id":"a-0","name":"a-0","status":"idle"}`
	req := httptest.NewRequest("POST", "/api/agents", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.srv.handleRegisterAgent(w, req)

	require.Equal(s.T(), http.StatusCreated, w.Code)
}

func (s *AgentsHandlerSuite) TestRegisterAgentNoRegistry() {
	srv := nilServer()
	body := `{"channel_id":"ch-1","agent_id":"a-0"}`
	req := httptest.NewRequest("POST", "/api/agents", strings.NewReader(body))
	w := httptest.NewRecorder()
	srv.handleRegisterAgent(w, req)

	require.Equal(s.T(), http.StatusServiceUnavailable, w.Code)
}
