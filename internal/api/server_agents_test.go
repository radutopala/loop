package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/agentregistry"
)

// --- SetAgentRegistry ---

func (s *ServerSuite) TestAgentSetAgentRegistry() {
	old := s.srv.agentRegistry
	defer func() { s.srv.agentRegistry = old }()
	s.srv.agentRegistry = nil
	require.Nil(s.T(), s.srv.agentRegistry)
	reg := agentregistry.New()
	s.srv.SetAgentRegistry(reg)
	require.NotNil(s.T(), s.srv.agentRegistry)
}

// --- handleListAgents ---

func (s *ServerSuite) TestAgentListAgentsSuccess() {
	reg := agentregistry.New()
	s.srv.SetAgentRegistry(reg)
	defer func() { s.srv.agentRegistry = nil }()

	reg.Register(&agentregistry.AgentInfo{AgentID: "a-0", ChannelID: "ch-1", Name: "Alpha"})
	reg.Register(&agentregistry.AgentInfo{AgentID: "a-1", ChannelID: "ch-1", Name: "Beta"})

	req := httptest.NewRequest("GET", "/api/agents?channel_id=ch-1", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusOK, w.Code)
	var agents []*agentregistry.AgentInfo
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &agents))
	require.Len(s.T(), agents, 2)
}

func (s *ServerSuite) TestAgentListAgentsEmpty() {
	reg := agentregistry.New()
	s.srv.SetAgentRegistry(reg)
	defer func() { s.srv.agentRegistry = nil }()

	req := httptest.NewRequest("GET", "/api/agents?channel_id=ch-1", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusOK, w.Code)
	var agents []*agentregistry.AgentInfo
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &agents))
	require.Empty(s.T(), agents)
}

func (s *ServerSuite) TestAgentListAgentsMissingChannelID() {
	reg := agentregistry.New()
	s.srv.SetAgentRegistry(reg)
	defer func() { s.srv.agentRegistry = nil }()

	req := httptest.NewRequest("GET", "/api/agents", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	require.Equal(s.T(), http.StatusBadRequest, w.Code)
}

func (s *ServerSuite) TestAgentListAgentsNotConfigured() {
	req := httptest.NewRequest("GET", "/api/agents?channel_id=ch-1", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	require.Equal(s.T(), http.StatusServiceUnavailable, w.Code)
}

// --- handleUpdateAgent ---

func (s *ServerSuite) TestAgentUpdateAgentSuccess() {
	reg := agentregistry.New()
	s.srv.SetAgentRegistry(reg)
	defer func() { s.srv.agentRegistry = nil }()

	reg.Register(&agentregistry.AgentInfo{AgentID: "a-0", ChannelID: "ch-1", Status: "idle"})
	s.srv.SetEventsHub(NewEventsHub(slog.Default()))
	defer func() { s.srv.eventsHub = nil }()

	body := `{"channel_id":"ch-1","status":"running","work_summary":"indexing","name":"Worker"}`
	req := httptest.NewRequest("PATCH", "/api/agents/a-0", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusOK, w.Code)
	var updated agentregistry.AgentInfo
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &updated))
	require.Equal(s.T(), "running", updated.Status)
	require.Equal(s.T(), "indexing", updated.WorkSummary)
	require.Equal(s.T(), "Worker", updated.Name)
}

func (s *ServerSuite) TestAgentUpdateAgentNotFound() {
	reg := agentregistry.New()
	s.srv.SetAgentRegistry(reg)
	defer func() { s.srv.agentRegistry = nil }()

	body := `{"channel_id":"ch-1","status":"running"}`
	req := httptest.NewRequest("PATCH", "/api/agents/nope", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	require.Equal(s.T(), http.StatusNotFound, w.Code)
}

func (s *ServerSuite) TestAgentUpdateAgentMissingChannelID() {
	reg := agentregistry.New()
	s.srv.SetAgentRegistry(reg)
	defer func() { s.srv.agentRegistry = nil }()

	body := `{"status":"running"}`
	req := httptest.NewRequest("PATCH", "/api/agents/a-0", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	require.Equal(s.T(), http.StatusBadRequest, w.Code)
}

func (s *ServerSuite) TestAgentUpdateAgentInvalidJSON() {
	reg := agentregistry.New()
	s.srv.SetAgentRegistry(reg)
	defer func() { s.srv.agentRegistry = nil }()

	req := httptest.NewRequest("PATCH", "/api/agents/a-0", strings.NewReader("{bad"))
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	require.Equal(s.T(), http.StatusBadRequest, w.Code)
}

func (s *ServerSuite) TestAgentUpdateAgentNotConfigured() {
	body := `{"channel_id":"ch-1","status":"running"}`
	req := httptest.NewRequest("PATCH", "/api/agents/a-0", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	require.Equal(s.T(), http.StatusServiceUnavailable, w.Code)
}

// --- handleSendAgentMessage ---

func (s *ServerSuite) TestAgentSendMessageSuccess() {
	reg := agentregistry.New()
	s.srv.SetAgentRegistry(reg)
	defer func() { s.srv.agentRegistry = nil }()

	reg.Register(&agentregistry.AgentInfo{AgentID: "a-0", ChannelID: "ch-1"})
	reg.Register(&agentregistry.AgentInfo{AgentID: "a-1", ChannelID: "ch-1"})

	body := `{"channel_id":"ch-1","from_agent_id":"a-0","content":"hello"}`
	req := httptest.NewRequest("POST", "/api/agents/a-1/message", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusNoContent, w.Code)
}

func (s *ServerSuite) TestAgentSendMessageTargetNotFound() {
	reg := agentregistry.New()
	s.srv.SetAgentRegistry(reg)
	defer func() { s.srv.agentRegistry = nil }()

	body := `{"channel_id":"ch-1","from_agent_id":"a-0","content":"hello"}`
	req := httptest.NewRequest("POST", "/api/agents/nope/message", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	require.Equal(s.T(), http.StatusNotFound, w.Code)
}

func (s *ServerSuite) TestAgentSendMessageMissingFields() {
	reg := agentregistry.New()
	s.srv.SetAgentRegistry(reg)
	defer func() { s.srv.agentRegistry = nil }()

	body := `{"channel_id":"ch-1"}`
	req := httptest.NewRequest("POST", "/api/agents/a-1/message", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	require.Equal(s.T(), http.StatusBadRequest, w.Code)
}

func (s *ServerSuite) TestAgentSendMessageInvalidJSON() {
	reg := agentregistry.New()
	s.srv.SetAgentRegistry(reg)
	defer func() { s.srv.agentRegistry = nil }()

	req := httptest.NewRequest("POST", "/api/agents/a-1/message", strings.NewReader("{bad"))
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	require.Equal(s.T(), http.StatusBadRequest, w.Code)
}

func (s *ServerSuite) TestAgentSendMessageNotConfigured() {
	body := `{"channel_id":"ch-1","from_agent_id":"a-0","content":"hello"}`
	req := httptest.NewRequest("POST", "/api/agents/a-1/message", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	require.Equal(s.T(), http.StatusServiceUnavailable, w.Code)
}

// --- handleAgentChannelWS ---

func (s *ServerSuite) TestAgentChannelWSSuccess() {
	reg := agentregistry.New()
	s.srv.SetAgentRegistry(reg)
	defer func() { s.srv.agentRegistry = nil }()

	reg.Register(&agentregistry.AgentInfo{AgentID: "a-0", ChannelID: "ch-1"})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/ws/agent-channel", s.srv.handleAgentChannelWS)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/ws/agent-channel?agent_id=a-0&channel_id=ch-1"
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(s.T(), err)
	defer ws.Close()

	// Send a message to the agent.
	require.NoError(s.T(), reg.SendMessage("ch-1", "a-1", "a-0", "hello"))

	// Read the message from WebSocket.
	var msg agentregistry.AgentMessage
	require.NoError(s.T(), ws.ReadJSON(&msg))
	require.Equal(s.T(), "a-1", msg.FromAgentID)
	require.Equal(s.T(), "hello", msg.Content)
}

func (s *ServerSuite) TestAgentChannelWSClosesOnUnregister() {
	reg := agentregistry.New()
	s.srv.SetAgentRegistry(reg)
	defer func() { s.srv.agentRegistry = nil }()

	reg.Register(&agentregistry.AgentInfo{AgentID: "a-0", ChannelID: "ch-1"})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/ws/agent-channel", s.srv.handleAgentChannelWS)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/ws/agent-channel?agent_id=a-0&channel_id=ch-1"
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(s.T(), err)
	defer ws.Close()

	// Unregister the agent — WebSocket should close.
	reg.Unregister("ch-1", "a-0")

	// Reading should return an error (connection closed).
	_, _, err = ws.ReadMessage()
	require.Error(s.T(), err)
}

func (s *ServerSuite) TestAgentChannelWSMissingParams() {
	reg := agentregistry.New()
	s.srv.SetAgentRegistry(reg)
	defer func() { s.srv.agentRegistry = nil }()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/ws/agent-channel", s.srv.handleAgentChannelWS)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/ws/agent-channel")
	require.NoError(s.T(), err)
	defer resp.Body.Close()
	require.Equal(s.T(), http.StatusBadRequest, resp.StatusCode)
}

func (s *ServerSuite) TestAgentChannelWSAgentNotFound() {
	reg := agentregistry.New()
	s.srv.SetAgentRegistry(reg)
	defer func() { s.srv.agentRegistry = nil }()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/ws/agent-channel", s.srv.handleAgentChannelWS)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/ws/agent-channel?agent_id=nope&channel_id=ch-1")
	require.NoError(s.T(), err)
	defer resp.Body.Close()
	require.Equal(s.T(), http.StatusNotFound, resp.StatusCode)
}

func (s *ServerSuite) TestAgentChannelWSUpgradeFail() {
	reg := agentregistry.New()
	s.srv.SetAgentRegistry(reg)
	defer func() { s.srv.agentRegistry = nil }()

	// Agent exists but request is a regular HTTP GET (not WS upgrade).
	reg.Register(&agentregistry.AgentInfo{AgentID: "a-0", ChannelID: "ch-1"})
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

func (s *ServerSuite) TestAgentChannelWSNotConfigured() {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/ws/agent-channel", s.srv.handleAgentChannelWS)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/ws/agent-channel?agent_id=a-0&channel_id=ch-1")
	require.NoError(s.T(), err)
	defer resp.Body.Close()
	require.Equal(s.T(), http.StatusServiceUnavailable, resp.StatusCode)
}

// --- integration: send + receive via WS ---

func (s *ServerSuite) TestAgentChannelWSMultipleMessages() {
	reg := agentregistry.New()
	s.srv.SetAgentRegistry(reg)
	defer func() { s.srv.agentRegistry = nil }()

	reg.Register(&agentregistry.AgentInfo{AgentID: "a-0", ChannelID: "ch-1"})

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
		require.NoError(s.T(), reg.SendMessage("ch-1", "sender", "a-0", strings.Repeat("x", i+1)))
	}

	// Read all 3.
	for i := range 3 {
		require.NoError(s.T(), ws.SetReadDeadline(time.Now().Add(time.Second)))
		var msg agentregistry.AgentMessage
		require.NoError(s.T(), ws.ReadJSON(&msg))
		require.Equal(s.T(), strings.Repeat("x", i+1), msg.Content)
	}
}

func (s *ServerSuite) TestAgentChannelWSWriteError() {
	reg := agentregistry.New()
	s.srv.SetAgentRegistry(reg)
	s.srv.agentWSWriteJSON = func(v any) error { return errors.New("write failed") }

	reg.Register(&agentregistry.AgentInfo{AgentID: "a-0", ChannelID: "ch-1"})

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/ws/agent-channel", s.srv.handleAgentChannelWS)
	ts := httptest.NewServer(mux)

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/api/ws/agent-channel?agent_id=a-0&channel_id=ch-1"
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(s.T(), err)

	// Send a message; the injected writeJSON returns an error, exercising the error branch.
	require.NoError(s.T(), reg.SendMessage("ch-1", "sender", "a-0", "boom"))
	time.Sleep(50 * time.Millisecond)

	// Close WS + server before test cleanup to avoid race on agentWSWriteJSON.
	ws.Close()
	ts.Close()
}

// --- handleDeleteAgent ---

func (s *ServerSuite) TestAgentDeleteAgent() {
	reg := agentregistry.New()
	s.srv.SetAgentRegistry(reg)
	defer func() { s.srv.agentRegistry = nil }()

	reg.Register(&agentregistry.AgentInfo{AgentID: "a-0", ChannelID: "ch-1", Name: "a-0"})
	require.NotNil(s.T(), reg.Get("ch-1", "a-0"))

	req := httptest.NewRequest("DELETE", "/api/agents/a-0?channel_id=ch-1", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusNoContent, w.Code)
	require.Nil(s.T(), reg.Get("ch-1", "a-0"))
}

func (s *ServerSuite) TestAgentDeleteAgentMissingParams() {
	reg := agentregistry.New()
	s.srv.SetAgentRegistry(reg)
	defer func() { s.srv.agentRegistry = nil }()

	req := httptest.NewRequest("DELETE", "/api/agents/a-0", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusBadRequest, w.Code)
}

func (s *ServerSuite) TestAgentDeleteAgentNoRegistry() {
	req := httptest.NewRequest("DELETE", "/api/agents/a-0?channel_id=ch-1", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	require.Equal(s.T(), http.StatusServiceUnavailable, w.Code)
}

func (s *ServerSuite) TestAgentDeleteAgentBroadcastsEvent() {
	reg := agentregistry.New()
	s.srv.SetAgentRegistry(reg)
	defer func() { s.srv.agentRegistry = nil }()

	hub := NewEventsHub(slog.Default())
	s.srv.SetEventsHub(hub)
	defer func() { s.srv.eventsHub = nil }()

	reg.Register(&agentregistry.AgentInfo{AgentID: "a-0", ChannelID: "ch-1", Name: "a-0"})

	req := httptest.NewRequest("DELETE", "/api/agents/a-0?channel_id=ch-1", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusNoContent, w.Code)
	require.Nil(s.T(), reg.Get("ch-1", "a-0"))
}

// --- handleRegisterAgent ---

func (s *ServerSuite) TestAgentRegisterAgent() {
	reg := agentregistry.New()
	s.srv.SetAgentRegistry(reg)
	defer func() { s.srv.agentRegistry = nil }()

	body := `{"channel_id":"ch-1","agent_id":"a-0","name":"a-0","status":"idle"}`
	req := httptest.NewRequest("POST", "/api/agents", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusCreated, w.Code)
	agent := reg.Get("ch-1", "a-0")
	require.NotNil(s.T(), agent)
	require.Equal(s.T(), "idle", agent.Status)
}

func (s *ServerSuite) TestAgentRegisterAgentMissingFields() {
	reg := agentregistry.New()
	s.srv.SetAgentRegistry(reg)
	defer func() { s.srv.agentRegistry = nil }()

	body := `{"channel_id":"ch-1"}`
	req := httptest.NewRequest("POST", "/api/agents", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusBadRequest, w.Code)
}

func (s *ServerSuite) TestAgentRegisterAgentInvalidJSON() {
	reg := agentregistry.New()
	s.srv.SetAgentRegistry(reg)
	defer func() { s.srv.agentRegistry = nil }()

	req := httptest.NewRequest("POST", "/api/agents", strings.NewReader("not json"))
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusBadRequest, w.Code)
}

func (s *ServerSuite) TestAgentRegisterAgentBroadcastsEvent() {
	reg := agentregistry.New()
	s.srv.SetAgentRegistry(reg)
	defer func() { s.srv.agentRegistry = nil }()

	hub := NewEventsHub(slog.Default())
	s.srv.SetEventsHub(hub)
	defer func() { s.srv.eventsHub = nil }()

	body := `{"channel_id":"ch-1","agent_id":"a-0","name":"a-0","status":"idle"}`
	req := httptest.NewRequest("POST", "/api/agents", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusCreated, w.Code)
}

func (s *ServerSuite) TestAgentRegisterAgentNoRegistry() {
	body := `{"channel_id":"ch-1","agent_id":"a-0"}`
	req := httptest.NewRequest("POST", "/api/agents", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusServiceUnavailable, w.Code)
}
