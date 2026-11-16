// Package promptedit turns a composer draft and a spoken instruction into the
// message of a read-only agent run, and the run's reply into the new draft.
package promptedit

import (
	_ "embed"
	"errors"
	"fmt"
	"strings"
	"unicode/utf16"
)

// AgentID is the prompt edit agent's MCP agent id. It gives the run its own
// MCP config file, one with no Loop tools that change anything.
const AgentID = "prompt-edit"

//go:embed prompt.md
var systemPrompt string

// ErrNoInstruction: the instruction was empty, so there is nothing to do.
var ErrNoInstruction = errors.New("the instruction is empty")

// Request is one edit. Draft is the composer's text and Start..End the
// selection in it, in UTF-16 code units as the browser counts them; an empty
// selection edits the whole draft.
type Request struct {
	Draft       string
	Start       int
	End         int
	Instruction string
}

// Result is the edited draft and the span in it that the edit wrote: the
// whole text without a selection, the replaced selection with one.
type Result struct {
	Text string `json:"text"`
	From int    `json:"from"`
	To   int    `json:"to"`
}

// Edit is a request ready to run: Prompt is the run's message, and Apply
// turns the agent's reply into the new draft.
type Edit struct {
	Prompt     string
	draft      []uint16
	start, end int
}

// SystemPrompt is the edit run's system prompt.
func SystemPrompt() string { return systemPrompt }

// Prepare readies req for a run. It fails with ErrNoInstruction when there
// is no instruction.
func Prepare(req Request) (*Edit, error) {
	instruction := strings.TrimSpace(req.Instruction)
	if instruction == "" {
		return nil, ErrNoInstruction
	}
	draft := utf16.Encode([]rune(req.Draft))
	start, end := clampSelection(req.Start, req.End, len(draft))
	return &Edit{Prompt: buildPrompt(draft, start, end, instruction), draft: draft, start: start, end: end}, nil
}

// Apply returns the draft the agent's reply makes: the reply itself without
// a selection, the draft with the selection replaced by it with one.
func (e *Edit) Apply(reply string) Result {
	out := cleanReply(reply)
	if e.start == e.end {
		return Result{Text: out, From: 0, To: unitLen(out)}
	}
	before, after := decode(e.draft[:e.start]), decode(e.draft[e.end:])
	from := unitLen(before)
	return Result{Text: before + out + after, From: from, To: from + unitLen(out)}
}

// buildPrompt is the run's message: the draft, the selection when there is
// one, and the instruction.
func buildPrompt(draft []uint16, start, end int, instruction string) string {
	var b strings.Builder
	text := decode(draft)
	switch {
	case len(draft) == 0:
		b.WriteString("The draft is empty: write the prompt the instruction describes.\n\n")
	case start == end:
		fmt.Fprintf(&b, "<draft>\n%s\n</draft>\n\nRewrite the whole draft.\n\n", text)
	default:
		fmt.Fprintf(&b, "<draft>\n%s\n</draft>\n\n<selection>\n%s\n</selection>\n\n", text, decode(draft[start:end]))
		b.WriteString("Rewrite only the selection; the rest of the draft stays as it is.\n\n")
	}
	fmt.Fprintf(&b, "<instruction>\n%s\n</instruction>", instruction)
	return b.String()
}

// cleanReply drops the space around the reply and a code fence the model
// wrapped the whole of it in.
func cleanReply(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") || !strings.HasSuffix(s, "```") || len(s) < 6 {
		return s
	}
	body := strings.TrimSuffix(s, "```")
	nl := strings.IndexByte(body, '\n')
	if nl < 0 {
		return s
	}
	return strings.TrimSpace(body[nl+1:])
}

// clampSelection keeps start..end inside a draft n units long, start first.
func clampSelection(start, end, n int) (int, int) {
	start = min(max(start, 0), n)
	end = min(max(end, start), n)
	return start, end
}

// decode turns UTF-16 units back into a string.
func decode(u []uint16) string { return string(utf16.Decode(u)) }

// unitLen is s's length in UTF-16 units, as the browser counts it.
func unitLen(s string) int { return len(utf16.Encode([]rune(s))) }
