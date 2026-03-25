package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/radutopala/loop/internal/agentregistry"
	"github.com/radutopala/loop/internal/events"
)

// SetAgentRegistry configures the agent registry.
func (s *Server) SetAgentRegistry(r *agentregistry.Registry) {
	s.agentRegistry = r
}

// handleRegisterAgent handles POST /api/agents.
// Called by the MCP server on startup to register itself in the agent registry.
func (s *Server) handleRegisterAgent(w http.ResponseWriter, r *http.Request) {
	if s.agentRegistry == nil {
		http.Error(w, "agent registry not configured", http.StatusServiceUnavailable)
		return
	}

	var body struct {
		ChannelID string `json:"channel_id"`
		AgentID   string `json:"agent_id"`
		Name      string `json:"name"`
		Status    string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if body.ChannelID == "" || body.AgentID == "" {
		http.Error(w, "channel_id and agent_id required", http.StatusBadRequest)
		return
	}

	s.agentRegistry.Register(&agentregistry.AgentInfo{
		AgentID:   body.AgentID,
		ChannelID: body.ChannelID,
		Name:      body.Name,
		Status:    body.Status,
	})
	if s.eventsHub != nil {
		s.eventsHub.BroadcastAgentInstanceRegistered(body.ChannelID, events.AgentInstanceEventData{
			AgentID:   body.AgentID,
			ChannelID: body.ChannelID,
			Name:      body.Name,
		})
	}

	w.WriteHeader(http.StatusCreated)
}

// handleListAgents handles GET /api/agents?channel_id=X.
func (s *Server) handleListAgents(w http.ResponseWriter, r *http.Request) {
	if s.agentRegistry == nil {
		http.Error(w, "agent registry not configured", http.StatusServiceUnavailable)
		return
	}

	channelID := r.URL.Query().Get("channel_id")
	if channelID == "" {
		http.Error(w, "channel_id required", http.StatusBadRequest)
		return
	}

	agents := s.agentRegistry.List(channelID)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(agents) //nolint:errcheck
}

// handleUpdateAgent handles PATCH /api/agents/{id}.
func (s *Server) handleUpdateAgent(w http.ResponseWriter, r *http.Request) {
	if s.agentRegistry == nil {
		http.Error(w, "agent registry not configured", http.StatusServiceUnavailable)
		return
	}

	agentID := r.PathValue("id")
	if agentID == "" {
		http.Error(w, "agent ID required", http.StatusBadRequest)
		return
	}

	var body struct {
		ChannelID   string `json:"channel_id"`
		Status      string `json:"status"`
		WorkSummary string `json:"work_summary"`
		Name        string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if body.ChannelID == "" {
		http.Error(w, "channel_id required", http.StatusBadRequest)
		return
	}

	updated := s.agentRegistry.UpdateStatus(body.ChannelID, agentID, body.Status, body.WorkSummary, body.Name)
	if updated == nil {
		http.Error(w, "agent not found", http.StatusNotFound)
		return
	}

	// Broadcast metadata update to frontend.
	if s.eventsHub != nil {
		s.eventsHub.BroadcastAgentInstanceMetadata(body.ChannelID, events.AgentInstanceEventData{
			AgentID:     agentID,
			ChannelID:   body.ChannelID,
			Name:        updated.Name,
			Status:      updated.Status,
			WorkSummary: updated.WorkSummary,
		})
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(updated) //nolint:errcheck
}

// handleDeleteAgent handles DELETE /api/agents/{id}?channel_id=X.
// Called by the MCP server on graceful shutdown to unregister the agent.
func (s *Server) handleDeleteAgent(w http.ResponseWriter, r *http.Request) {
	if s.agentRegistry == nil {
		http.Error(w, "agent registry not configured", http.StatusServiceUnavailable)
		return
	}

	agentID := r.PathValue("id")
	channelID := r.URL.Query().Get("channel_id")
	if agentID == "" || channelID == "" {
		http.Error(w, "agent ID and channel_id required", http.StatusBadRequest)
		return
	}

	s.agentRegistry.Unregister(channelID, agentID)
	if s.eventsHub != nil {
		s.eventsHub.BroadcastAgentInstanceUnregistered(channelID, events.AgentInstanceEventData{
			AgentID:   agentID,
			ChannelID: channelID,
		})
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleSendAgentMessage handles POST /api/agents/{id}/message.
func (s *Server) handleSendAgentMessage(w http.ResponseWriter, r *http.Request) {
	if s.agentRegistry == nil {
		http.Error(w, "agent registry not configured", http.StatusServiceUnavailable)
		return
	}

	toAgentID := r.PathValue("id")
	if toAgentID == "" {
		http.Error(w, "agent ID required", http.StatusBadRequest)
		return
	}

	var body struct {
		ChannelID   string `json:"channel_id"`
		FromAgentID string `json:"from_agent_id"`
		Content     string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if body.ChannelID == "" || body.Content == "" {
		http.Error(w, "channel_id and content required", http.StatusBadRequest)
		return
	}

	if err := s.agentRegistry.SendMessage(body.ChannelID, body.FromAgentID, toAgentID, body.Content); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleAgentChannelWS handles GET /api/ws/agent-channel?agent_id=X&channel_id=Y.
// MCP servers inside agent containers connect here to receive pushed messages.
func (s *Server) handleAgentChannelWS(w http.ResponseWriter, r *http.Request) {
	if s.agentRegistry == nil {
		http.Error(w, "agent registry not configured", http.StatusServiceUnavailable)
		return
	}

	agentID := r.URL.Query().Get("agent_id")
	channelID := r.URL.Query().Get("channel_id")
	if agentID == "" || channelID == "" {
		http.Error(w, "agent_id and channel_id required", http.StatusBadRequest)
		return
	}

	// Retry Subscribe with a short poll — the push receiver may connect
	// before the terminal handler registers the agent.
	var ch <-chan *agentregistry.AgentMessage
	var err error
	for range 15 {
		ch, err = s.agentRegistry.Subscribe(channelID, agentID)
		if err == nil {
			break
		}
		time.Sleep(time.Second)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return // gorilla/websocket writes the error response
	}
	defer conn.Close()

	// Read pump: detect client disconnect.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	// Write pump: forward messages from the mailbox to the WebSocket.
	for {
		select {
		case msg, ok := <-ch:
			if !ok {
				// Channel closed — agent unregistered.
				return
			}
			if err := conn.WriteJSON(msg); err != nil {
				return
			}
		case <-done:
			return
		}
	}
}
