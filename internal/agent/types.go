package agent

import (
	"fmt"
	"strings"
)

// AgentRequest is the input sent to the agent runner.
type AgentRequest struct {
	SessionID     string         `json:"session_id"`
	ForkSession   bool           `json:"fork_session,omitempty"`
	Messages      []AgentMessage `json:"messages"`
	SystemPrompt  string         `json:"system_prompt"`
	ChannelID     string         `json:"channel_id"`
	AuthorID      string         `json:"author_id,omitempty"`
	DirPath       string         `json:"dir_path,omitempty"`
	ParentDirPath string         `json:"parent_dir_path,omitempty"`
	PlanMode      bool           `json:"plan_mode,omitempty"`
	Prompt        string         `json:"prompt,omitempty"`
	AgentID       string         `json:"agent_id,omitempty"`
	// Model / Effort override the merged config's claude_model / claude_effort
	// for this run when non-empty (per-channel on-demand override).
	Model  string `json:"model,omitempty"`
	Effort string `json:"effort,omitempty"`
	// OnTurn is called for each assistant turn's text content during streaming.
	// When set, the runner follows container logs in real-time instead of waiting
	// for the container to exit. When nil, the runner uses the existing
	// wait-then-read behavior.
	OnTurn func(text string) `json:"-"`
	// OnToolUse is called for each tool invocation in an assistant turn.
	// toolUseID is the per-block id from the assistant message; pairs with
	// the toolUseID delivered by OnToolResult.
	OnToolUse func(toolUseID, name, input string) `json:"-"`
	// OnActivity is called for model detection and system events (subagent progress).
	OnActivity func(activity, detail string) `json:"-"`
	// OnThinking is called for each "thinking" content block emitted by the
	// assistant (extended thinking). Empty blocks are not delivered.
	OnThinking func(text string) `json:"-"`
	// OnToolResult is called for each tool_result block emitted on a "user"
	// stream-json line. output is already truncated by the runner; isError
	// reflects the upstream is_error flag.
	OnToolResult func(toolUseID, output string, isError bool) `json:"-"`
}

// AgentMessage represents a single message in the conversation context.
type AgentMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// AgentResponse is the output from the agent runner.
type AgentResponse struct {
	Response   string `json:"response"`
	SessionID  string `json:"session_id"`
	Error      string `json:"error,omitempty"`
	DurationMs int    `json:"duration_ms,omitempty"`
	NumTurns   int    `json:"num_turns,omitempty"`
	StopReason string `json:"stop_reason,omitempty"`
	Model      string `json:"model,omitempty"`
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
		if len(r.Messages) == 0 {
			return r.Prompt
		}
		// A lone slash-command message must stay bare: a "role: " prefix
		// stops the CLI from expanding it as a user-invoked skill, which is
		// the only way to run skills shipped with disable-model-invocation.
		if len(r.Messages) == 1 && strings.HasPrefix(r.Messages[0].Content, "/") {
			return r.Messages[0].Content
		}
		var prompt string
		for _, msg := range r.Messages {
			prompt += fmt.Sprintf("%s: %s\n", msg.Role, msg.Content)
		}
		return prompt
	}
}
