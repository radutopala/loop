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
// at the PR worktree and runs it to completion. Findings don't travel
// through the agent's stdout — the agent reports them itself via the
// report_review_findings MCP tool, which POSTs back into the daemon's
// review-comments endpoint. Persistence and broadcasting live there.
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
// context).
func (r *Runner) Run(ctx context.Context, channelID, dirPath, parentDirPath, systemPrompt, prompt string) (*agent.AgentResponse, error) {
	if r.Agent == nil {
		return nil, errors.New("review runner: agent not configured")
	}
	req := &agent.AgentRequest{
		ChannelID:     channelID,
		DirPath:       dirPath,
		ParentDirPath: parentDirPath,
		SystemPrompt:  systemPrompt,
		Prompt:        prompt,
	}
	return r.Agent.Run(ctx, req)
}
