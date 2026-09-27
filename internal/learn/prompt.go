// Package learn builds what a learn pass is told: the system prompt that
// teaches the learn agent Loop's proposal kinds and the current config, and
// the message that starts each pass.
package learn

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
)

// AgentID is the learn agent's MCP agent id. It gives the pass its own MCP
// config file and is what unlocks the propose_learnings tool.
const AgentID = "learn"

//go:embed prompt.md
var basePrompt string

// Kinds are the proposal kinds, in the order the prompt documents them.
var Kinds = []string{
	db.LearnKindPromptShortcut,
	db.LearnKindBashShortcut,
	db.LearnKindScheduledTask,
	db.LearnKindGateRule,
	db.LearnKindMount,
	db.LearnKindRename,
	db.LearnKindDescription,
	db.LearnKindTicketURL,
}

// State is what already exists for the channel being learned from, so the
// learn agent doesn't propose it again.
type State struct {
	ChannelName string
	Description string
	TicketURL   string
	// Worktree says the channel is a worktree thread; its name is a display
	// name only.
	Worktree bool
	// ProjectDir is the root checkout whose .loop/config.json config
	// proposals are written to.
	ProjectDir string
	// Config is the merged config for ProjectDir.
	Config *config.Config
	// Tasks are the channel's scheduled tasks.
	Tasks []*db.ScheduledTask
	// Proposals are the channel's earlier proposals, newest first.
	Proposals []*db.LearnProposal
}

// maxDismissed caps how many dismissed proposals the prompt lists.
const maxDismissed = 20

// SystemPrompt returns the learn agent's system prompt: the built-in
// instructions, the current state, then the config's learn.prompt.
func SystemPrompt(st State) string {
	var b strings.Builder
	b.WriteString(basePrompt)
	b.WriteString("\n## Current state\n\n")
	fmt.Fprintf(&b, "- Channel: %q", st.ChannelName)
	if st.Worktree {
		b.WriteString(" (a worktree thread)")
	}
	b.WriteString("\n")
	if st.Description != "" {
		fmt.Fprintf(&b, "- Description: %q\n", st.Description)
	} else {
		b.WriteString("- Description: none\n")
	}
	if st.TicketURL != "" {
		fmt.Fprintf(&b, "- Ticket URL: %s\n", st.TicketURL)
	} else {
		b.WriteString("- Ticket URL: none\n")
	}
	if st.ProjectDir != "" {
		fmt.Fprintf(&b, "- Project config: `%s/.loop/config.json`\n", st.ProjectDir)
	}
	cfg := st.Config
	if cfg == nil {
		cfg = &config.Config{}
	}
	writeSection(&b, "Prompt shortcuts", cfg.PromptShortcuts)
	writeSection(&b, "Bash shortcuts", cfg.BashShortcuts)
	writeSection(&b, "Scheduled tasks in this channel", taskSummaries(st.Tasks))
	writeSection(&b, "Task templates", cfg.TaskTemplates)
	writeSection(&b, "Agentgate command rules", cfg.Gates.Agentgate.CommandRules)
	writeSection(&b, "Agentgate file rules", cfg.Gates.Agentgate.FileRules)
	writeSection(&b, "Agentgate path rules", cfg.Gates.Agentgate.PathRules)
	writeSection(&b, "Mounts", cfg.Mounts)
	waiting, dismissed := proposalSummaries(st.Proposals)
	writeSection(&b, "Proposals waiting for the user", waiting)
	writeSection(&b, "Proposals the user dismissed", dismissed)
	if extra := strings.TrimSpace(cfg.Learn.Prompt); extra != "" {
		b.WriteString("\n## Additional instructions\n\n")
		b.WriteString(extra)
		b.WriteString("\n")
	}
	return b.String()
}

// taskSummary is the part of a scheduled task the learn agent needs to spot a
// duplicate.
type taskSummary struct {
	Type       string `json:"type"`
	Schedule   string `json:"schedule,omitempty"`
	Prompt     string `json:"prompt,omitempty"`
	BashScript string `json:"bash_script,omitempty"`
	Workflow   string `json:"workflow_name,omitempty"`
	Enabled    bool   `json:"enabled"`
}

func taskSummaries(tasks []*db.ScheduledTask) []taskSummary {
	out := make([]taskSummary, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, taskSummary{
			Type:       string(t.Type),
			Schedule:   t.Schedule,
			Prompt:     t.Prompt,
			BashScript: t.BashScript,
			Workflow:   t.WorkflowName,
			Enabled:    t.Enabled,
		})
	}
	return out
}

// proposalSummary is the part of an earlier proposal the learn agent needs to
// spot a repeat.
type proposalSummary struct {
	Kind    string          `json:"kind"`
	Title   string          `json:"title"`
	Payload json.RawMessage `json:"payload"`
}

// proposalSummaries splits earlier proposals into those still waiting on the
// user (pending, applying or failed) and the most recent dismissed ones.
// Applied proposals show up in the state above already.
func proposalSummaries(proposals []*db.LearnProposal) (waiting, dismissed []proposalSummary) {
	for _, p := range proposals {
		sum := proposalSummary{Kind: p.Kind, Title: p.Title, Payload: json.RawMessage(p.Payload)}
		if !json.Valid(sum.Payload) {
			sum.Payload, _ = json.Marshal(p.Payload)
		}
		switch p.Status {
		case db.LearnApplied:
		case db.LearnDismissed:
			if len(dismissed) < maxDismissed {
				dismissed = append(dismissed, sum)
			}
		default:
			waiting = append(waiting, sum)
		}
	}
	return waiting, dismissed
}

// writeSection writes a titled JSON dump of items, or "none" when empty.
func writeSection[T any](b *strings.Builder, title string, items []T) {
	fmt.Fprintf(b, "\n### %s\n\n", title)
	if len(items) == 0 {
		b.WriteString("none\n")
		return
	}
	// The items are plain config/state structs; they always marshal.
	data, _ := json.MarshalIndent(items, "", "  ")
	b.WriteString("```json\n")
	b.Write(data)
	b.WriteString("\n```\n")
}

// TriggerMessage is the message that starts a learn pass over the run that
// just finished in channelName, whose last prompt was lastPrompt.
func TriggerMessage(channelName, lastPrompt string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "The run in %q just finished. Review it and propose what Loop should learn from it.", channelName)
	if p := strings.TrimSpace(lastPrompt); p != "" {
		b.WriteString("\n\nIts last prompt was:\n\n")
		b.WriteString(quote(p))
	}
	return b.String()
}

// quote renders text as a markdown blockquote.
func quote(text string) string {
	return "> " + strings.ReplaceAll(text, "\n", "\n> ")
}
