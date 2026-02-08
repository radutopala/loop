package agent

import "fmt"

// AgentRequest is the input sent to the agent runner.
type AgentRequest struct {
	SessionID    string         `json:"session_id"`
	Messages     []AgentMessage `json:"messages"`
	SystemPrompt string         `json:"system_prompt"`
	ChannelID    string         `json:"channel_id"`
	DirPath      string         `json:"dir_path,omitempty"`
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

// BuildPrompt constructs a plain text prompt from messages and an optional system prompt.
func BuildPrompt(messages []AgentMessage, systemPrompt string) string {
	var prompt string
	if systemPrompt != "" {
		prompt = systemPrompt + "\n\n"
	}
	for _, msg := range messages {
		prompt += fmt.Sprintf("%s: %s\n", msg.Role, msg.Content)
	}
	return prompt
}
