package agent

import (
	"fmt"
)

// AgentRequest is the input sent to the agent runner.
type AgentRequest struct {
	SessionID    string         `json:"session_id"`
	ForkSession  bool           `json:"fork_session,omitempty"`
	Messages     []AgentMessage `json:"messages"`
	SystemPrompt string         `json:"system_prompt"`
	ChannelID    string         `json:"channel_id"`
	AuthorID     string         `json:"author_id,omitempty"`
	DirPath      string         `json:"dir_path,omitempty"`
	Prompt       string         `json:"prompt,omitempty"`
	// OnTurn is called for each assistant turn's text content during streaming.
	// When set, the runner follows container logs in real-time instead of waiting
	// for the container to exit. When nil, the runner uses the existing
	// wait-then-read behavior.
	OnTurn func(text string) `json:"-"`
}

// AgentMessage represents a single message in the conversation context.
type AgentMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// AgentResponse is the output from the agent runner.
type AgentResponse struct {
	Response  string `json:"response"`
	SessionID string `json:"session_id"`
	Error     string `json:"error,omitempty"`
}

// BuildPrompt returns the prompt text for this request.
// When resuming a session, only the latest message is sent to avoid redundancy.
func (r *AgentRequest) BuildPrompt() string {
	switch {
	case r.SessionID != "" && r.Prompt != "":
		return r.Prompt
	case r.SessionID != "" && len(r.Messages) > 0:
		return r.Messages[len(r.Messages)-1].Content
	default:
		var prompt string
		for _, msg := range r.Messages {
			prompt += fmt.Sprintf("%s: %s\n", msg.Role, msg.Content)
		}
		return prompt
	}
}
