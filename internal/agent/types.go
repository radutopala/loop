package agent

import (
	"fmt"
	"strings"
)

// AgentRequest is the input sent to the agent runner.
type AgentRequest struct {
	SessionID   string `json:"session_id"`
	ForkSession bool   `json:"fork_session,omitempty"`
	// ResumeAt is the transcript uuid a forked batch run resumes SessionID
	// at: the fork keeps the session's messages up to and including that
	// entry and drops the rest (--resume-session-at). Only honoured with
	// ForkSession; a learn pass or an explanation uses it to see a session
	// that ends with the turn it reviews.
	ResumeAt     string         `json:"resume_at,omitempty"`
	Messages     []AgentMessage `json:"messages"`
	SystemPrompt string         `json:"system_prompt"`
	// SubagentSystemPrompt is appended to the system prompt of every
	// Task-tool subagent the run spawns (and to nested ones), separately
	// from SystemPrompt — which the CLI applies to the main agent only.
	// Review runs need this: /code-review derives its candidate findings
	// in fan-out subagents, so context that only reaches the orchestrator
	// (the line rule) never reaches the agents doing the reviewing.
	SubagentSystemPrompt string `json:"subagent_system_prompt,omitempty"`
	ChannelID            string `json:"channel_id"`
	AuthorID             string `json:"author_id,omitempty"`
	DirPath              string `json:"dir_path,omitempty"`
	ParentDirPath        string `json:"parent_dir_path,omitempty"`
	PlanMode             bool   `json:"plan_mode,omitempty"`
	Prompt               string `json:"prompt,omitempty"`
	AgentID              string `json:"agent_id,omitempty"`
	// Model / Effort override the merged config's claude_model / claude_effort
	// for this run when non-empty (per-channel on-demand override).
	Model  string `json:"model,omitempty"`
	Effort string `json:"effort,omitempty"`
	// ReviewMode runs the request as a code-review pass, so that the built-in
	// /code-review command executes in the main session instead of forking a
	// subagent. It takes ReportFindings out of claude_batch_disallowed_tools
	// and passes --settings with two env keys (see reviewModeSettings):
	//
	//   - CLAUDE_CODE_REPORT_FINDINGS=1. The command runs inline only when
	//     this is set AND ReportFindings is available AND the output format is
	//     stream-json (batch runs always are); otherwise it forks. A fork emits
	//     nothing on the parent stream — the run looks hung for minutes — and
	//     its findings never reach a tool call the parent can see.
	//   - CLAUDE_CODE_SUBAGENT_MODEL=inherit, which every run passes (see
	//     runSettings), so any subagent that does get spawned runs on this
	//     request's model rather than a host ~/.claude/settings.json value.
	ReviewMode bool `json:"review_mode,omitempty"`
	// ReadOnly runs the request as a learn pass or an explanation over a
	// chat run: its built-in tools are limited to the read-only --tools
	// allowlist, and on top of the batch denials every Loop tool that
	// changes state is denied, so the run can only look (and a learn pass
	// propose).
	ReadOnly bool `json:"read_only,omitempty"`
	// OnTurn is called for each assistant turn's text content during streaming,
	// with where that text sits in the session's transcript (see TurnRef).
	// When set, the runner follows container logs in real-time instead of waiting
	// for the container to exit. When nil, the runner uses the existing
	// wait-then-read behavior.
	OnTurn func(text string, ref TurnRef) `json:"-"`
	// OnToolUse is called for each tool invocation in an assistant turn.
	// toolUseID is the per-block id from the assistant message; pairs with
	// the toolUseID delivered by OnToolResult.
	OnToolUse func(toolUseID, name, input string) `json:"-"`
	// OnToolUseRaw sees the same invocations as OnToolUse, but rawInput is the
	// tool's argument JSON verbatim instead of the chat-facing summary — which
	// is lossy, and empty for tools whose schema the summarizer doesn't know.
	// Set this (not OnToolUse) when the arguments have to be decoded.
	OnToolUseRaw func(toolUseID, name, rawInput string) `json:"-"`
	// OnActivity is called for model detection and system events (subagent progress).
	OnActivity func(activity, detail string) `json:"-"`
	// OnThinking is called for each "thinking" content block emitted by the
	// assistant (extended thinking). Empty blocks are not delivered.
	OnThinking func(text string) `json:"-"`
	// OnToolResult is called for each tool_result block emitted on a "user"
	// stream-json line. output is already truncated by the runner; isError
	// reflects the upstream is_error flag.
	OnToolResult func(toolUseID, output string, isError bool) `json:"-"`
	// OnSession is called with the session the run writes to, as soon as an
	// assistant event reports it and again whenever it changes. A forked run
	// writes to a new session before it has said anything, and a run stopped
	// at an ask or plan card may never send a text turn or a final result.
	OnSession func(sessionID string) `json:"-"`
}

// TurnRef locates an assistant text event in Claude Code's session
// transcript: the session it was written to and its entry's uuid. A turn's
// last text is the last entry of its chain, so resuming a fork at that uuid
// (see AgentRequest.ResumeAt) gives the session as it was when the turn
// ended. Both are empty when the event didn't carry them (a subagent's text,
// or an older CLI).
type TurnRef struct {
	SessionID string
	UUID      string
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
