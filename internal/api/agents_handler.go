package api

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/agentregistry"
	"github.com/radutopala/loop/internal/events"
)

// validAgentID matches the agent IDs the registry takes: pane IDs such as
// "docker-agent-0". Messages are labelled with their sender's ID, which
// keeps a label from closing early or starting a new line.
var validAgentID = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

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
	if !validAgentID.MatchString(body.AgentID) {
		http.Error(w, "invalid agent_id", http.StatusBadRequest)
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

	// A side chat's agents are its parent's: the terminals and chats it
	// works with.
	group, self, err := s.agentGroup(r.Context(), channelID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	chats, err := s.chatAgents(r.Context(), group)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	agents := []agentListItem{}
	for _, a := range chats {
		agents = append(agents, agentListItem{AgentInfo: a, Self: a.AgentID == self})
	}
	for _, a := range s.agentRegistry.List(group) {
		agents = append(agents, agentListItem{AgentInfo: a})
	}
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

// releaseAgentTerminal unregisters the agents whose pane ran in a terminal
// session that ended. Closing a pane kills its agent, which never gets to
// unregister itself.
func (s *Server) releaseAgentTerminal(sessionID string) {
	if s.agentRegistry == nil {
		return
	}
	for _, a := range s.agentRegistry.ReleaseTerminal(sessionID) {
		if s.eventsHub != nil {
			s.eventsHub.BroadcastAgentInstanceUnregistered(a.ChannelID, events.AgentInstanceEventData{
				AgentID:   a.AgentID,
				ChannelID: a.ChannelID,
			})
		}
	}
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
	group, self, err := s.agentGroup(r.Context(), body.ChannelID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// The sender's label is all the target goes by, so it names the
	// sending chat, whose agent is "chat" in each one, or an agent of the
	// channel.
	from := body.FromAgentID
	switch {
	case from == agent.ChatAgentID:
		from = self
	case from != "" && (!validAgentID.MatchString(from) || s.agentRegistry.Get(group, from) == nil):
		http.Error(w, "from_agent_id "+strconv.Quote(from)+" isn't an agent in channel "+group, http.StatusBadRequest)
		return
	}
	text := body.Content
	if from != "" {
		text = "[from " + from + "] " + text
	}

	// A chat takes the message as its next turn, queued behind its run.
	chat, err := s.chatTarget(r.Context(), group, toAgentID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if chat != "" {
		if toAgentID == from {
			http.Error(w, "an agent can't message itself; queue_message queues a prompt for yourself", http.StatusBadRequest)
			return
		}
		if s.msgHandler == nil {
			http.Error(w, "agent "+toAgentID+" can't take messages: messages are not configured", http.StatusConflict)
			return
		}
		s.enqueueMessage(r.Context(), chat, text, "", false)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if s.agentRegistry.Get(group, toAgentID) == nil {
		http.Error(w, "agent "+toAgentID+" not found in channel "+group+"; to message another channel or thread, use send_message with its channel_id", http.StatusNotFound)
		return
	}

	sid := s.agentRegistry.Terminal(group, toAgentID)
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
	// The trailing newline keeps messages queued while the agent is busy on
	// lines of their own: Claude Code joins queued prompts as is.
	input := pasteStart + text + "\n" + pasteEnd + "\r"
	if err := s.termManager.SendInput(sid, []byte(input)); err != nil {
		s.agentRegistry.ClearTerminal(group, toAgentID, sid)
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
