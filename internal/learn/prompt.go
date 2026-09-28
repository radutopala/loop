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
	// Config is the channel's merged config, as its runs see it: global →
	// ProjectDir → the worktree's for a worktree thread, global →
	// ProjectDir otherwise. It must not be nil.
	Config *config.Config
	// Tasks are the channel's scheduled tasks.
	Tasks []*db.ScheduledTask
	// Proposals are the channel's earlier proposals, newest first.
	Proposals []*db.LearnProposal
}

// maxSettled caps how many dismissed proposals the prompt lists, and how
// many withdrawn ones.
const maxSettled = 20

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
	writeSection(&b, "Prompt shortcuts", cfg.PromptShortcuts)
	writeSection(&b, "Bash shortcuts", cfg.BashShortcuts)
	writeSection(&b, "Scheduled tasks in this channel", taskSummaries(st.Tasks))
	writeSection(&b, "Task templates", cfg.TaskTemplates)
	writeSection(&b, "Agentgate command rules", cfg.Gates.Agentgate.CommandRules)
	writeSection(&b, "Agentgate file rules", cfg.Gates.Agentgate.FileRules)
	writeSection(&b, "Agentgate path rules", cfg.Gates.Agentgate.PathRules)
	writeSection(&b, "Mounts", cfg.Mounts)
	sums := proposalSummaries(st.Proposals)
	writeSection(&b, "Proposals waiting for the user", sums.waiting)
	writeSection(&b, "Proposals the user dismissed", sums.dismissed)
	writeSection(&b, "Proposals earlier passes withdrew", sums.withdrawn)
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
// spot a repeat. A waiting one carries its id and status, so the agent can
// withdraw it; a withdrawn one, why it was.
type proposalSummary struct {
	ID      int64           `json:"id,omitempty"`
	Status  string          `json:"status,omitempty"`
	Kind    string          `json:"kind"`
	Title   string          `json:"title"`
	Payload json.RawMessage `json:"payload"`
	Reason  string          `json:"withdrawn_reason,omitempty"`
}

// proposalSets are a channel's earlier proposals as the prompt lists them.
type proposalSets struct {
	waiting, dismissed, withdrawn []proposalSummary
}

// proposalSummaries splits earlier proposals into those still waiting on the
// user (pending, applying or failed), and the most recent dismissed and
// withdrawn ones. Applied proposals show up in the state above already.
func proposalSummaries(proposals []*db.LearnProposal) proposalSets {
	var sets proposalSets
	for _, p := range proposals {
		sum := proposalSummary{Kind: p.Kind, Title: p.Title, Payload: json.RawMessage(p.Payload)}
		switch p.Status {
		case db.LearnApplied:
		case db.LearnDismissed:
			if len(sets.dismissed) < maxSettled {
				sets.dismissed = append(sets.dismissed, sum)
			}
		case db.LearnWithdrawn:
			if len(sets.withdrawn) < maxSettled {
				sum.Reason = p.WithdrawnReason
				sets.withdrawn = append(sets.withdrawn, sum)
			}
		default:
			sum.ID, sum.Status = p.ID, p.Status
			sets.waiting = append(sets.waiting, sum)
		}
	}
	return sets
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

// The first line of a trigger message is triggerLead, the quoted channel
// name, then triggerTail.
const (
	triggerLead = "The run in "
	triggerTail = " just finished. Review it and propose what Loop should learn from it."
)

// TriggerMessage is the message that starts a learn pass over the run that
// just finished in channelName, whose last prompt was lastPrompt.
func TriggerMessage(channelName, lastPrompt string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s%q%s", triggerLead, channelName, triggerTail)
	if p := strings.TrimSpace(lastPrompt); p != "" {
		b.WriteString("\n\nIts last prompt was:\n\n")
		b.WriteString(quote(p))
	}
	return b.String()
}

// dirHint starts the paragraph a worktree run's prompt is prefixed with.
const dirHint = "IMPORTANT: Your working directory is "

// IsTrigger reports whether prompt is a TriggerMessage, bare or as the
// agent got it: behind its author's "name: " prefix, and in a worktree
// behind the working directory hint. %q keeps the channel name on the first
// line, so that line alone tells.
func IsTrigger(prompt string) bool {
	if strings.HasPrefix(prompt, dirHint) {
		_, prompt, _ = strings.Cut(prompt, "\n\n")
	}
	line, _, _ := strings.Cut(prompt, "\n")
	if _, rest, ok := strings.Cut(line, ": "); ok && strings.HasPrefix(rest, triggerLead) {
		line = rest
	}
	return strings.HasPrefix(line, triggerLead+`"`) && strings.HasSuffix(line, `"`+triggerTail)
}

// quote renders text as a markdown blockquote.
func quote(text string) string {
	return "> " + strings.ReplaceAll(text, "\n", "\n> ")
}
