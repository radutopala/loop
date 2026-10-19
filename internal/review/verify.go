package review

import (
	"fmt"
	"slices"
	"strings"
)

// The verification step. Once a review is over (a single run, or the last
// iteration of a review loop), the daemon runs the dedup pass over the whole
// session and then an agent that reads the code each agent comment points at
// and records a verdict on it with the update_review_comment tool, and
// assesses the PR's unresolved GitHub threads a person started with the
// assess_review_thread tool. The agent's reply, a per-comment summary, is
// posted to the channel with the PR outcome (see DeriveOutcome).

// DefaultVerifyPrompt is the verification agent's instructions when the
// config sets none. The daemon adds the context the agent needs after it
// (see BuildVerifyPrompt), so a custom prompt doesn't have to carry it.
const DefaultVerifyPrompt = `Check the review comments on this PR and decide whether it can be approved. The dedup pass has already run; its outcome is in the context below.

1. Read the comments with the get_review_comments tool. Replies on GitHub are listed under the comment that starts their thread.
2. For each agent comment, open the file it points at with the Read tool, in the checkout named below, which is at the reviewed commit, and decide whether it is a real issue (real), a false positive (false_positive), or already fixed (already_fixed). For a pushed one, weigh the replies under it too: an author's reply can show it is intended, or point at the commit that fixed it.
3. Record your verdict on each one with the update_review_comment tool: its comment_id, verdict and a one-sentence reason citing the line you read. Never pass a body. Your verdict replaces the dedup pass's. With real, pass its impact: ` + ImpactRule + `
4. For each unresolved thread a person started on GitHub (source github, not resolved), read the code it points at and the replies under it, and record with the assess_review_thread tool whether it is addressed (a later commit or a reply settles it; cite the line or the reply) or still open, with its impact as above. Don't judge whether the person is right: a thread you think is wrong is still open, as nice_to_have unless it names a blocking problem.
5. Reply with one entry per comment and thread: its id, path:line, category (when it has one), verdict or status, impact and reason. Link a pushed comment as <PR URL>#discussion_r<its github_id>. Only show a severity the comment itself states; don't rate them yourself beyond the impact.
6. End with what the dedup pass removed, trimmed, moved and marked related, and how many comments got each verdict and impact. The daemon adds the PR outcome after your reply.

Don't change any code. Apart from recording verdicts and thread assessments, don't edit, delete or push any comment.`

// VerifyTools are the Loop tools the verification agent may call on top of
// a read-only run's: recording verdicts and thread assessments are the only
// changes it makes.
var VerifyTools = []string{"mcp__loop__update_review_comment", "mcp__loop__assess_review_thread"}

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

// NeedsVerify reports whether comments hold something the verification
// step judges: an agent comment, or the start of an unresolved GitHub
// thread a person started.
func NeedsVerify(comments []*Comment) bool {
	return slices.ContainsFunc(comments, func(c *Comment) bool {
		return c != nil && (deletable(c) || (c.ReplyTo == 0 && c.GitHubID > 0 && !c.Resolved))
	})
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
