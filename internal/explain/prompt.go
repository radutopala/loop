// Package explain builds what an explain run is told: the system prompt
// that teaches the explain agent what a turn's explanation holds, and the
// message that starts each run.
package explain

import (
	_ "embed"
	"errors"
	"fmt"
	"strings"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/types"
)

// AgentID is the explain agent's MCP agent id. It gives the run its own MCP
// config file, one with no Loop tools that change anything.
const AgentID = "explain"

// Errors an explanation can't be started with.
var (
	// ErrNotATurn: the message isn't a bot message in the channel, so it
	// doesn't end a turn there.
	ErrNotATurn = errors.New("not a bot message in this channel")
	// ErrNoSession: the channel has no session to fork yet.
	ErrNoSession = errors.New("the channel has no session to explain")
	// ErrUnavailable: the channel isn't one explanations run for, a Slack
	// or Discord channel, a task thread, or a hidden thread.
	ErrUnavailable = errors.New("explain is not available in this channel")
)

// Unavailable says why turns in ch can't be explained, or "" when they can.
// Only desktop app channels are, since the Explain pane is in the desktop
// app; task threads aren't, nor learn or explain threads, whose runs are
// Loop's own.
func Unavailable(ch *db.Channel) string {
	switch {
	case ch.Platform != types.PlatformLocal:
		return "explain runs only in desktop app channels"
	case ch.TaskID != 0:
		return "explain doesn't run in task threads"
	case db.IsHiddenKind(ch.Kind):
		return "explain doesn't run in learn or explain threads"
	}
	return ""
}

//go:embed prompt.md
var basePrompt string

// SystemPrompt returns the explain agent's system prompt: the built-in
// instructions, then the config's explain.prompt.
func SystemPrompt(extra string) string {
	extra = strings.TrimSpace(extra)
	if extra == "" {
		return basePrompt
	}
	return basePrompt + "\n## Additional instructions\n\n" + extra + "\n"
}

// The first line of a trigger message is triggerLead, the quoted channel
// name, then triggerTail.
const (
	triggerLead = "Explain the turn in "
	triggerTail = " that ended with the reply below."
)

// maxQuoted caps how much of the turn's prompt and reply the trigger
// quotes; enough to find the turn in the session.
const maxQuoted = 2000

// TriggerMessage is the message that starts an explain run over the turn
// in channelName that prompt started and reply ended.
func TriggerMessage(channelName, prompt, reply string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s%q%s", triggerLead, channelName, triggerTail)
	if p := strings.TrimSpace(prompt); p != "" {
		b.WriteString("\n\nIts prompt was:\n\n")
		b.WriteString(quote(truncate(p)))
	}
	b.WriteString("\n\nIts final reply was:\n\n")
	b.WriteString(quote(truncate(strings.TrimSpace(reply))))
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

// truncate cuts text to maxQuoted runes, marking the cut.
func truncate(text string) string {
	r := []rune(text)
	if len(r) <= maxQuoted {
		return text
	}
	return string(r[:maxQuoted]) + " …"
}

// quote renders text as a markdown blockquote.
func quote(text string) string {
	return "> " + strings.ReplaceAll(text, "\n", "\n> ")
}
