// Package agentregistry tracks active agent instances per channel,
// enabling inter-agent discovery and push-based messaging.
package agentregistry

import (
	"fmt"
	"sync"
	"time"
)

// mailboxSize is the buffer capacity for each agent's message channel.
const mailboxSize = 64

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

// AgentMessage is a push-based message from one agent to another.
type AgentMessage struct {
	FromAgentID string    `json:"from_agent_id"`
	Content     string    `json:"content"`
	Timestamp   time.Time `json:"timestamp"`
}

// Registry tracks active agents and their mailboxes.
// All methods are goroutine-safe.
type Registry struct {
	mu        sync.RWMutex
	agents    map[string]map[string]*AgentInfo         // channelID -> agentID -> info
	mailboxes map[string]map[string]chan *AgentMessage // channelID -> agentID -> push channel
	timeNow   func() time.Time
}

// New creates a new agent registry.
func New() *Registry {
	return &Registry{
		agents:    make(map[string]map[string]*AgentInfo),
		mailboxes: make(map[string]map[string]chan *AgentMessage),
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
		r.mailboxes[info.ChannelID] = make(map[string]chan *AgentMessage)
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
	r.mailboxes[info.ChannelID][info.AgentID] = make(chan *AgentMessage, mailboxSize)
}

// Unregister removes an agent and closes its mailbox.
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

	if channelMailboxes, ok := r.mailboxes[channelID]; ok {
		if ch, ok := channelMailboxes[agentID]; ok {
			close(ch)
			delete(channelMailboxes, agentID)
		}
		if len(channelMailboxes) == 0 {
			delete(r.mailboxes, channelID)
		}
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

// SendMessage pushes a message to the target agent's mailbox.
// Non-blocking — drops the message if the mailbox is full.
func (r *Registry) SendMessage(channelID, fromAgentID, toAgentID, content string) error {
	r.mu.RLock()
	defer r.mu.RUnlock()

	channelMailboxes, ok := r.mailboxes[channelID]
	if !ok {
		return fmt.Errorf("agent %s not found in channel %s", toAgentID, channelID)
	}
	ch, ok := channelMailboxes[toAgentID]
	if !ok {
		return fmt.Errorf("agent %s not found in channel %s", toAgentID, channelID)
	}

	msg := &AgentMessage{
		FromAgentID: fromAgentID,
		Content:     content,
		Timestamp:   r.timeNow(),
	}

	select {
	case ch <- msg:
		return nil
	default:
		return fmt.Errorf("agent %s mailbox full, message dropped", toAgentID)
	}
}

// Subscribe returns the read end of an agent's mailbox channel.
// The channel is closed when the agent unregisters.
func (r *Registry) Subscribe(channelID, agentID string) (<-chan *AgentMessage, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	channelMailboxes, ok := r.mailboxes[channelID]
	if !ok {
		return nil, fmt.Errorf("agent %s not found in channel %s", agentID, channelID)
	}
	ch, ok := channelMailboxes[agentID]
	if !ok {
		return nil, fmt.Errorf("agent %s not found in channel %s", agentID, channelID)
	}
	return ch, nil
}
