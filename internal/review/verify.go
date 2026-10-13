package review

import (
	"fmt"
	"slices"
	"strings"
)

// The verification step. Once a review is over (a single run, or the last
// iteration of a review loop), the daemon runs the dedup pass over the whole
// session and then an agent that reads the code each agent comment points at
// and records a verdict on it with the update_review_comment tool. The
// agent's reply, a per-comment summary, is posted to the channel.

// DefaultVerifyPrompt is the verification agent's instructions when the
// config sets none. The daemon adds the context the agent needs after it
// (see BuildVerifyPrompt), so a custom prompt doesn't have to carry it.
const DefaultVerifyPrompt = `Check the review comments this review session reported on the PR. The dedup pass has already run; its outcome is in the context below.

1. Read the comments with the get_review_comments tool. Skip the ones whose source is github.
2. For each one, open the file it points at with the Read tool, in the checkout named below, which is at the reviewed commit, and decide whether it is a real issue (real), a false positive (false_positive), or already fixed (already_fixed).
3. Record your verdict on each one with the update_review_comment tool: its comment_id, verdict and a one-sentence reason citing the line you read. Never pass a body. Your verdict replaces the dedup pass's.
4. Reply with one entry per comment: its id, path:line, category (when it has one), verdict and reason. Link a pushed comment as <PR URL>#discussion_r<its github_id>. Only show a severity the comment itself states; don't rate them yourself.
5. End with what the dedup pass removed, trimmed, moved and marked related, and how many comments got each verdict.

Don't change any code. Apart from recording verdicts, don't edit, delete or push any comment.`

// VerifyTools are the Loop tools the verification agent may call on top of
// a read-only run's: recording a verdict is the one change it makes.
var VerifyTools = []string{"mcp__loop__update_review_comment"}

// VerifyContext is what the verification agent is told about the review it
// checks. Dedup describes the dedup pass that ran before it: its result, or
// why it failed or was skipped.
type VerifyContext struct {
	PRNumber int
	PRURL    string
	HeadSHA  string
	Checkout string
	Dedup    string
}

// HasAgentComments reports whether comments hold one the review agent
// reported, the only kind the verification step checks.
func HasAgentComments(comments []*Comment) bool {
	return slices.ContainsFunc(comments, func(c *Comment) bool { return c != nil && deletable(c) })
}

// BuildVerifyPrompt returns instructions (DefaultVerifyPrompt when empty)
// followed by vc.
func BuildVerifyPrompt(instructions string, vc VerifyContext) string {
	if strings.TrimSpace(instructions) == "" {
		instructions = DefaultVerifyPrompt
	}
	var b strings.Builder
	b.WriteString(strings.TrimSpace(instructions))
	b.WriteString("\n\nReview context:\n")
	if vc.PRNumber > 0 {
		fmt.Fprintf(&b, "- PR: #%d\n", vc.PRNumber)
	}
	if vc.PRURL != "" {
		fmt.Fprintf(&b, "- PR URL: %s\n", vc.PRURL)
	}
	if vc.HeadSHA != "" {
		fmt.Fprintf(&b, "- Reviewed commit: %s\n", vc.HeadSHA)
	}
	if vc.Checkout != "" {
		fmt.Fprintf(&b, "- Checkout: %s\n", vc.Checkout)
	}
	fmt.Fprintf(&b, "- Dedup pass: %s\n", vc.Dedup)
	b.WriteString("\nYour reply is posted to the channel as is.")
	return b.String()
}
