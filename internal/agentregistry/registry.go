// Package agentregistry tracks active agent instances per channel,
// enabling inter-agent discovery and messaging.
package agentregistry

import (
	"sync"
	"time"
)

// AgentInfo holds metadata about a running agent instance.
type AgentInfo struct {
	AgentID     string    `json:"agent_id"`
	ChannelID   string    `json:"channel_id"`
	SessionID   string    `json:"session_id"`
	Name        string    `json:"name"`
	Status      string    `json:"status"` // "idle", "running", "completed", "error"
	WorkSummary string    `json:"work_summary"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// Registry tracks active agents and their terminal sessions.
// All methods are goroutine-safe.
type Registry struct {
	mu     sync.RWMutex
	agents map[string]map[string]*AgentInfo // channelID -> agentID -> info
	// terminals holds the terminal session each agent pane runs in. It's kept
	// apart from agents: the session starts before the agent registers, and
	// outlives the agent's restarts.
	terminals map[string]map[string]string // channelID -> agentID -> terminal session ID
	timeNow   func() time.Time
}

// New creates a new agent registry.
func New() *Registry {
	return &Registry{
		agents:    make(map[string]map[string]*AgentInfo),
		terminals: make(map[string]map[string]string),
		timeNow:   time.Now,
	}
}

// Register adds or updates an agent in the registry.
func (r *Registry) Register(info *AgentInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := r.timeNow()

	if _, ok := r.agents[info.ChannelID]; !ok {
		r.agents[info.ChannelID] = make(map[string]*AgentInfo)
	}

	if existing, ok := r.agents[info.ChannelID][info.AgentID]; ok {
		// Update existing — preserve CreatedAt.
		existing.SessionID = info.SessionID
		existing.Name = info.Name
		existing.Status = info.Status
		existing.WorkSummary = info.WorkSummary
		existing.UpdatedAt = now
		return
	}

	info.CreatedAt = now
	info.UpdatedAt = now
	if info.Status == "" {
		info.Status = "idle"
	}
	r.agents[info.ChannelID][info.AgentID] = info
}

// Unregister removes an agent.
// Idempotent — safe to call multiple times for the same agent.
func (r *Registry) Unregister(channelID, agentID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	channelAgents, ok := r.agents[channelID]
	if !ok {
		return
	}
	delete(channelAgents, agentID)
	if len(channelAgents) == 0 {
		delete(r.agents, channelID)
	}
}

// List returns all agents for a channel. Returns an empty slice (not nil) if none.
func (r *Registry) List(channelID string) []*AgentInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	channelAgents := r.agents[channelID]
	result := make([]*AgentInfo, 0, len(channelAgents))
	for _, a := range channelAgents {
		result = append(result, a)
	}
	return result
}

// Get returns a single agent's info, or nil if not found.
func (r *Registry) Get(channelID, agentID string) *AgentInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if channelAgents, ok := r.agents[channelID]; ok {
		return channelAgents[agentID]
	}
	return nil
}

// UpdateStatus updates an agent's status, work summary, and/or name.
// Returns the updated info, or nil if the agent is not found.
func (r *Registry) UpdateStatus(channelID, agentID, status, workSummary, name string) *AgentInfo {
	r.mu.Lock()
	defer r.mu.Unlock()

	channelAgents, ok := r.agents[channelID]
	if !ok {
		return nil
	}
	agent, ok := channelAgents[agentID]
	if !ok {
		return nil
	}

	if status != "" {
		agent.Status = status
	}
	if workSummary != "" {
		agent.WorkSummary = workSummary
	}
	if name != "" {
		agent.Name = name
	}
	agent.UpdatedAt = r.timeNow()
	return agent
}

// SetTerminal records the terminal session an agent pane runs in.
func (r *Registry) SetTerminal(channelID, agentID, sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.terminals[channelID]; !ok {
		r.terminals[channelID] = make(map[string]string)
	}
	r.terminals[channelID][agentID] = sessionID
}

// Terminal returns the terminal session an agent pane runs in, or "" if none.
func (r *Registry) Terminal(channelID, agentID string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.terminals[channelID][agentID]
}

// ClearTerminal forgets an agent's terminal session, if it's still sessionID.
func (r *Registry) ClearTerminal(channelID, agentID, sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	channelTerminals := r.terminals[channelID]
	if channelTerminals[agentID] != sessionID {
		return
	}
	delete(channelTerminals, agentID)
	if len(channelTerminals) == 0 {
		delete(r.terminals, channelID)
	}
}

// ReleaseTerminal forgets the agents whose pane ran in terminal session
// sessionID, which ended, and returns the ones that were registered. An
// agent's own unregistering never runs then: closing the pane kills it.
func (r *Registry) ReleaseTerminal(sessionID string) []*AgentInfo {
	r.mu.Lock()
	defer r.mu.Unlock()

	var released []*AgentInfo
	for channelID, channelTerminals := range r.terminals {
		for agentID, sid := range channelTerminals {
			if sid != sessionID {
				continue
			}
			delete(channelTerminals, agentID)
			if info := r.agents[channelID][agentID]; info != nil {
				released = append(released, info)
				delete(r.agents[channelID], agentID)
				if len(r.agents[channelID]) == 0 {
					delete(r.agents, channelID)
				}
			}
		}
		if len(channelTerminals) == 0 {
			delete(r.terminals, channelID)
		}
	}
	return released
}
