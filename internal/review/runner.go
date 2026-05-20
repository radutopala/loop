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
// at the PR worktree, streams the agent's turns, parses `<review-comment>`
// blocks out of each turn, and dispatches each unique comment to the
// onComment callback. Persistence and broadcasting are the caller's job
// — the runner only orchestrates.
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
// context). For each `<review-comment>` block the agent emits,
// onComment fires with the parsed Comment exactly once — duplicate ids
// that arrive in a later turn are ignored so reruns don't double-
// broadcast.
func (r *Runner) Run(ctx context.Context, channelID, dirPath, parentDirPath, systemPrompt, prompt string, onComment func(*Comment)) (*agent.AgentResponse, error) {
	if r.Agent == nil {
		return nil, errors.New("review runner: agent not configured")
	}
	seen := make(map[string]struct{})
	req := &agent.AgentRequest{
		ChannelID:     channelID,
		DirPath:       dirPath,
		ParentDirPath: parentDirPath,
		SystemPrompt:  systemPrompt,
		Prompt:        prompt,
		OnTurn: func(text string) {
			for _, c := range ParseComments(text) {
				if _, dup := seen[c.ID]; dup {
					continue
				}
				seen[c.ID] = struct{}{}
				if onComment != nil {
					onComment(c)
				}
			}
		},
	}
	return r.Agent.Run(ctx, req)
}
