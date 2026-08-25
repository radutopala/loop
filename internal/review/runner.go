package review

import (
	"context"
	"errors"

	"github.com/radutopala/loop/internal/agent"
)

// AgentRunner is the subset of an agent runner the review runner needs.
// Matches the signature of container.Runner / orchestrator.Runner so the
// production wiring can reuse the existing Docker-backed runner.
type AgentRunner interface {
	Run(ctx context.Context, req *agent.AgentRequest) (*agent.AgentResponse, error)
}

// Runner drives a single review pass: it builds an AgentRequest pointed
// at the PR worktree and runs it to completion. Findings reach the daemon
// two ways, both landing in the same ingest path: the built-in
// ReportFindings tool, intercepted live off the agent's stream (onComment
// below), and — for a user-configured review prompt that doesn't run the
// built-in command — the report_review_findings MCP tool, which POSTs
// back into the daemon's review-comments endpoint.
type Runner struct {
	Agent AgentRunner
}

// Run executes the review prompt. dirPath is the PR worktree (the
// agent's CWD inside the container); parentDirPath is the channel's
// main repo dir, plumbed through to AgentRequest.ParentDirPath so the
// container runner also mounts the parent — without it the worktree's
// `.git` pointer file references a host path that's not visible inside
// the container, and the agent dies on startup. systemPrompt + prompt
// are passed straight through to the agent (the caller is expected to
// have resolved the configured review prompt and assembled the diff
// context). onComment, when set, receives each finding the agent reports
// through the built-in ReportFindings tool, in the order reported; it runs
// on the stream-reading goroutine, so it must not block for long.
//
// forkSessionID, when set, starts the review from a copy of that Claude
// session instead of a blank one, so the reviewer inherits the context of
// the conversation that produced the change. It is always paired with
// ForkSession: a plain --resume would append the review's turns to the
// session the user is still chatting in. The caller is responsible for
// having placed the session file in dirPath's Claude project dir.
func (r *Runner) Run(ctx context.Context, channelID, dirPath, parentDirPath, systemPrompt, prompt, forkSessionID string, onComment func(*Comment)) (*agent.AgentResponse, error) {
	if r.Agent == nil {
		return nil, errors.New("review runner: agent not configured")
	}
	req := &agent.AgentRequest{
		ChannelID:     channelID,
		DirPath:       dirPath,
		ParentDirPath: parentDirPath,
		SystemPrompt:  systemPrompt,
		Prompt:        prompt,
		ReviewMode:    true,
		SessionID:     forkSessionID,
		ForkSession:   forkSessionID != "",
	}
	if onComment != nil {
		// OnToolUseRaw, not OnToolUse: the latter carries a chat-facing
		// summary, which is empty for ReportFindings because the summarizer
		// has no case for it. Only the raw form has the findings to decode.
		req.OnToolUseRaw = func(_, name, rawInput string) {
			if name != ReportFindingsTool {
				return
			}
			for _, c := range ParseReportFindings(rawInput) {
				onComment(c)
			}
		}
	}
	return r.Agent.Run(ctx, req)
}
