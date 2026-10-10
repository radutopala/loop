package api

import (
	"encoding/json"
	"net/http"
	"strings"

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

	agentID := r.PathValue("id") // always non-empty: mux pattern requires {id}

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

	agentID := r.PathValue("id") // always non-empty: mux pattern requires {id}
	channelID := r.URL.Query().Get("channel_id")
	if channelID == "" {
		http.Error(w, "channel_id required", http.StatusBadRequest)
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

	toAgentID := r.PathValue("id") // always non-empty: mux pattern requires {id}

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

	if s.agentRegistry.Get(body.ChannelID, toAgentID) == nil {
		http.Error(w, "agent "+toAgentID+" not found in channel "+body.ChannelID, http.StatusNotFound)
		return
	}

	sid := s.agentRegistry.Terminal(body.ChannelID, toAgentID)
	if sid == "" || s.termManager == nil {
		http.Error(w, "agent "+toAgentID+" can't take messages: it has no terminal", http.StatusConflict)
		return
	}
	// Typed as one paste, so a multi-line message stays one prompt. A paste
	// marker in it would end the paste early and type the rest as keystrokes.
	if strings.Contains(body.Content, pasteStart) || strings.Contains(body.Content, pasteEnd) {
		http.Error(w, "content contains a bracketed-paste marker", http.StatusBadRequest)
		return
	}
	text := body.Content
	if body.FromAgentID != "" {
		text = "[from " + body.FromAgentID + "] " + text
	}
	// The trailing newline keeps messages queued while the agent is busy on
	// lines of their own: Claude Code joins queued prompts as is.
	input := pasteStart + text + "\n" + pasteEnd + "\r"
	if err := s.termManager.SendInput(sid, []byte(input)); err != nil {
		s.agentRegistry.ClearTerminal(body.ChannelID, toAgentID, sid)
		http.Error(w, "agent "+toAgentID+"'s terminal is gone: "+err.Error(), http.StatusConflict)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// Bracketed-paste markers: a terminal app takes what's between them as
// pasted text rather than keystrokes.
const (
	pasteStart = "\x1b[200~"
	pasteEnd   = "\x1b[201~"
)
