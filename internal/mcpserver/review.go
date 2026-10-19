package mcpserver

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// registerReviewTools adds the review-session tools. Registered
// unconditionally: outside a review run the daemon answers 404 (no review
// session for the channel) and the tool surfaces that as an error result.
// Every one of them but run_review acts on the agent's own channel; the
// daemon holds the ones that change comments to it too.
func (s *Server) registerReviewTools() {
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "run_review",
		Description: "Start a PR review, as the Review panel's Run button does: loads the PR into the channel's review session (unless it is already loaded) and starts the review-loop workflow, or review-fix-loop with mode review-fix. Model and effort default to the channel's own; empty there means the config default. The review always forks a session, never resumes it: fork picks the chat session, a fresh one or a session id, as the panel's session dropdown does. Defaults to this channel; give another channel or thread's id to review there, so several threads can each review a PR at once. Returns the workflow run id; follow it with get_workflow_run, and read the findings with get_review_comments once it finishes. Fails while that channel has a review loading or running.",
	}, s.handleRunReview)
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "report_review_findings",
		Description: "Report code-review findings for the current channel's PR review session. Each finding needs the repo-relative file path, the 1-based line number, and a body describing the bug, the concrete inputs/state that trigger it, and the wrong output or crash, plus an optional category slug. Side is RIGHT for added/modified lines (default) or LEFT for lines removed from the base. Call once with the full list; a finding reported twice verbatim is skipped server-side.",
	}, s.handleReportReviewFindings)
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "get_review_comments",
		Description: "Read this channel's PR review session, as shown in the Review panel: the PR, head SHA, status, the PR outcome (approve, approve_with_nits, request_changes or unknown, with the comments that decide it), and every comment with its id, file, line, side, whether it was pushed to GitHub, and its source (agent, or github with the author and GitHub comment id), its category when the review gave one, its verdict (real, false_positive or already_fixed) and a real one's impact (blocking or nice_to_have), or for a GitHub thread its assessment (addressed or open), then its body, the reason and the Claude transcripts of the agent passes that reported or changed it (read one to see why a finding was made). GitHub replies are listed under the comment that starts their thread. Filter to unpushed agent comments or one file. The PR diff is not included.",
	}, s.handleGetReviewComments)
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "dedup_review_findings",
		Description: "Run the dedup pass over this channel's review session: an agent drops agent comments that repeat another comment, trims a comment that bundles an issue another comment covers, re-anchors misplaced ones, notes related ones, and records a verdict (real, false_positive or already_fixed) on the agent comments it keeps, which deletes nothing. GitHub comments are never deleted; removed comments that were already pushed are deleted from the PR too. Takes minutes; fails while a review run is in progress.",
	}, s.handleDedupReviewFindings)
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "delete_review_comment",
		Description: "Delete an agent comment from this channel's review session, by the id get_review_comments shows. A comment already pushed is deleted from the PR too. GitHub comments (source github) are refused.",
	}, s.handleDeleteReviewComment)
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "update_review_comment",
		Description: "Change an agent comment in this channel's review session: replace its body, e.g. to fold in what a duplicate you are deleting adds, and/or record your verdict on it (real, false_positive or already_fixed, with a one-sentence reason), which replaces the dedup pass's. With real, give its impact: blocking when merging it as is would do harm, nice_to_have when it would only make working code better. Give body, verdict or both. Only an unpushed comment's body can change; GitHub comments can't change at all. Address all and Push all skip false_positive and already_fixed comments.",
	}, s.handleUpdateReviewComment)
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "assess_review_thread",
		Description: "Record whether an unresolved GitHub thread a person started on this channel's PR still stands, by the id of its first comment as get_review_comments shows it: addressed when a later commit or a reply settles it, or open with its impact (blocking or nice_to_have), plus a one-sentence reason. The PR outcome counts open threads. An agent comment's thread takes a verdict with update_review_comment instead.",
	}, s.handleAssessReviewThread)
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "push_review_comment",
		Description: "Post one agent comment from this channel's review session to the PR on GitHub, as the configured gh user. A comment already pushed is left as is.",
	}, s.handlePushReviewComment)
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "push_all_review_comments",
		Description: "Post every unpushed agent comment in this channel's review session to the PR on GitHub, as the configured gh user. Comments the dedup pass marked false_positive or already_fixed are skipped. Reports how many were pushed, skipped and which failed.",
	}, s.handlePushAllReviewComments)
}

type runReviewInput struct {
	PRNumber      int    `json:"pr_number" jsonschema:"required,Number of the pull request to review"`
	Model         string `json:"model,omitempty" jsonschema:"Model for the review, e.g. opus or sonnet; defaults to the channel's"`
	Effort        string `json:"effort,omitempty" jsonschema:"Reasoning effort: low, medium, high, xhigh or max; defaults to the channel's"`
	Mode          string `json:"mode,omitempty" jsonschema:"review (default) to only review, or review-fix to fix the findings and review again"`
	MaxIterations int    `json:"max_iterations,omitempty" jsonschema:"Number of passes, 1-10 (default 3)"`
	ChannelID     string `json:"channel_id,omitempty" jsonschema:"Channel or thread to run the review in; defaults to this one"`
	Fork          string `json:"fork,omitempty" jsonschema:"Session the review forks: chat (that channel's chat session), fresh (a new session) or a Claude session id; defaults to the choice set in the Review panel, chat for a newly loaded PR"`
	Scope         string `json:"scope,omitempty" jsonschema:"What each run reviews: full (the whole PR), incremental (only the commits since the last reviewed head) or auto (incremental when those are a small part of the PR); defaults to the choice set in the Review panel, auto for a newly loaded PR"`
}

func (s *Server) handleRunReview(_ context.Context, _ *mcp.CallToolRequest, input runReviewInput) (*mcp.CallToolResult, any, error) {
	channelID := cmp.Or(input.ChannelID, s.channelID)
	s.logger.Info("mcp tool call", "tool", "run_review", "channel_id", channelID, "pr", input.PRNumber)

	data, _ := json.Marshal(map[string]any{
		"pr_number":      input.PRNumber,
		"model":          input.Model,
		"effort":         input.Effort,
		"mode":           input.Mode,
		"max_iterations": input.MaxIterations,
		"fork":           input.Fork,
		"scope":          input.Scope,
	})
	apiURL := fmt.Sprintf("%s/api/channels/%s/review/start", s.apiURL, url.PathEscape(channelID))
	type startResult struct {
		RunID        string `json:"run_id"`
		WorkflowName string `json:"workflow_name"`
		Model        string `json:"model"`
		Effort       string `json:"effort"`
	}
	result, errResult, err := doAPICall[startResult](s, "POST", apiURL, http.StatusCreated, data)
	if errResult != nil || err != nil {
		return errResult, nil, err
	}
	return textResult(fmt.Sprintf("Review of PR #%d started in channel %s: workflow %s, run ID %s, model %s, effort %s. Use get_workflow_run to check progress.",
		input.PRNumber, channelID, result.WorkflowName, result.RunID, cmp.Or(result.Model, "default"), cmp.Or(result.Effort, "default"))), nil, nil
}

type reviewFindingInput struct {
	Path     string `json:"path" jsonschema:"required,Repo-relative file path the finding is in"`
	Line     int    `json:"line" jsonschema:"required,1-based line number the finding anchors to"`
	Side     string `json:"side,omitempty" jsonschema:"RIGHT for added/modified lines (default); LEFT only for lines removed from the base"`
	Body     string `json:"body" jsonschema:"required,One paragraph: the bug's trigger and the wrong output or crash"`
	Category string `json:"category,omitempty" jsonschema:"Short kebab-case slug of the finding type, e.g. correctness, security, efficiency, test-coverage"`
}

type reportReviewFindingsInput struct {
	Findings []reviewFindingInput `json:"findings" jsonschema:"required,The findings to report; empty list is a no-op"`
}

func (s *Server) handleReportReviewFindings(_ context.Context, _ *mcp.CallToolRequest, input reportReviewFindingsInput) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "report_review_findings", "count", len(input.Findings))

	data, _ := json.Marshal(map[string]any{"findings": input.Findings})
	apiURL := fmt.Sprintf("%s/api/channels/%s/review/comments", s.apiURL, url.PathEscape(s.channelID))

	type ingestResult struct {
		Added   int `json:"added"`
		Skipped int `json:"skipped"`
	}
	result, errResult, err := doAPICall[ingestResult](s, "POST", apiURL, 200, data)
	if errResult != nil || err != nil {
		return errResult, nil, err
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: fmt.Sprintf("Recorded %d finding(s) (%d duplicate/invalid skipped). They now appear in the Review panel.", result.Added, result.Skipped)},
		},
	}, nil, nil
}

type getReviewCommentsInput struct {
	UnpushedOnly bool   `json:"unpushed_only,omitempty" jsonschema:"Only agent comments not yet pushed to GitHub"`
	Path         string `json:"path,omitempty" jsonschema:"Only comments on this repo-relative file path"`
}

// reviewComment is the part of a review comment get_review_comments shows.
type reviewComment struct {
	ID       string `json:"id"`
	Path     string `json:"path"`
	Line     int    `json:"line"`
	Side     string `json:"side"`
	Body     string `json:"body"`
	Category string `json:"category"`
	Pushed   bool   `json:"pushed"`
	Source   string `json:"source"`
	Author   string `json:"author"`
	GitHubID int64  `json:"github_id"`
	Outdated bool   `json:"outdated"`
	Resolved bool   `json:"resolved"`

	Verdict       string   `json:"verdict"`
	VerdictReason string   `json:"verdict_reason"`
	Impact        string   `json:"impact"`
	ReplyTo       int64    `json:"reply_to"`
	Transcripts   []string `json:"transcripts"`
}

// reviewThread is the verification step's assessment of a GitHub thread.
type reviewThread struct {
	Status string `json:"status"`
	Impact string `json:"impact,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// reviewOutcome is whether the PR can be approved.
type reviewOutcome struct {
	State    string   `json:"state"`
	Blocking []string `json:"blocking"`
	Nits     []string `json:"nits"`
	Pending  []string `json:"pending"`
	Reason   string   `json:"reason"`
}

type reviewSession struct {
	Present bool `json:"present"`
	Session struct {
		PR *struct {
			Number int    `json:"number"`
			URL    string `json:"url"`
			Title  string `json:"title"`
		} `json:"pr"`
		HeadSHA     string                  `json:"head_sha"`
		Status      string                  `json:"status"`
		Error       string                  `json:"error"`
		Comments    []reviewComment         `json:"comments"`
		Threads     map[string]reviewThread `json:"threads"`
		ApprovedSHA string                  `json:"approved_sha"`
	} `json:"session"`
	Outcome *reviewOutcome `json:"outcome"`
}

// reviewURL is the API URL of the agent's own review session, plus suffix.
func (s *Server) reviewURL(suffix string) string {
	return fmt.Sprintf("%s/api/channels/%s/review%s", s.apiURL, url.PathEscape(s.channelID), suffix)
}

// reviewCommentURL is the API URL of one comment in that session, plus suffix.
func (s *Server) reviewCommentURL(commentID, suffix string) string {
	return s.reviewURL("/comments/" + url.PathEscape(commentID) + suffix)
}

func (s *Server) handleGetReviewComments(_ context.Context, _ *mcp.CallToolRequest, input getReviewCommentsInput) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "get_review_comments", "channel_id", s.channelID)

	apiURL := s.reviewURL("?diff=false")
	resp, errResult, err := doAPICall[reviewSession](s, "GET", apiURL, http.StatusOK, nil)
	if errResult != nil || err != nil {
		return errResult, nil, err
	}
	if !resp.Present {
		return textResult("No review session for this channel. Load a PR in the Review panel first."), nil, nil
	}
	sess := resp.Session

	var b strings.Builder
	if sess.PR != nil {
		fmt.Fprintf(&b, "PR #%d %s\n%s\n", sess.PR.Number, sess.PR.Title, sess.PR.URL)
	}
	fmt.Fprintf(&b, "head_sha: %s\nstatus: %s\n", sess.HeadSHA, sess.Status)
	if sess.Error != "" {
		fmt.Fprintf(&b, "error: %s\n", sess.Error)
	}
	if o := resp.Outcome; o != nil {
		fmt.Fprintf(&b, "outcome: %s\n", reviewOutcomeLine(o))
	}
	if sess.ApprovedSHA != "" {
		fmt.Fprintf(&b, "approved at: %s\n", sess.ApprovedSHA)
	}

	// Replies are shown under the comment their thread starts with.
	replies := map[int64][]reviewComment{}
	var shown []reviewComment
	total := 0
	for _, c := range sess.Comments {
		if c.ReplyTo != 0 {
			replies[c.ReplyTo] = append(replies[c.ReplyTo], c)
			continue
		}
		total++
		if input.UnpushedOnly && (c.Pushed || c.Source == "github") {
			continue
		}
		if input.Path != "" && c.Path != input.Path {
			continue
		}
		shown = append(shown, c)
	}
	fmt.Fprintf(&b, "\n%d of %d comment(s):\n", len(shown), total)
	for _, c := range shown {
		thread, assessed := sess.Threads[strconv.FormatInt(c.GitHubID, 10)]
		if c.Source != "github" || c.GitHubID == 0 {
			assessed = false
		}
		fmt.Fprintf(&b, "\n[%s] %s:%d %s, %s\n", c.ID, c.Path, c.Line, c.Side, reviewCommentState(c, thread, assessed))
		for line := range strings.SplitSeq(strings.TrimSpace(c.Body), "\n") {
			fmt.Fprintf(&b, "  %s\n", line)
		}
		if c.VerdictReason != "" {
			fmt.Fprintf(&b, "  (verdict: %s)\n", c.VerdictReason)
		}
		if assessed && thread.Reason != "" {
			fmt.Fprintf(&b, "  (assessment: %s)\n", thread.Reason)
		}
		for _, t := range c.Transcripts {
			fmt.Fprintf(&b, "  (transcript: %s)\n", t)
		}
		if c.GitHubID == 0 {
			continue
		}
		for _, r := range replies[c.GitHubID] {
			fmt.Fprintf(&b, "  reply by %s (github id %d):\n", r.Author, r.GitHubID)
			for line := range strings.SplitSeq(strings.TrimSpace(r.Body), "\n") {
				fmt.Fprintf(&b, "    %s\n", line)
			}
		}
	}
	return textResult(b.String()), nil, nil
}

// reviewOutcomeLine is o in one line: the state, then the comments that
// decide it.
func reviewOutcomeLine(o *reviewOutcome) string {
	switch {
	case len(o.Blocking) > 0:
		return fmt.Sprintf("%s (blocking: %s)", o.State, strings.Join(o.Blocking, ", "))
	case o.Reason != "":
		return fmt.Sprintf("%s (%s)", o.State, o.Reason)
	case len(o.Nits) > 0:
		return fmt.Sprintf("%s (nice to have: %s)", o.State, strings.Join(o.Nits, ", "))
	default:
		return o.State
	}
}

// reviewCommentState describes where a comment came from and where it is,
// with thread, its assessment when assessed.
func reviewCommentState(c reviewComment, thread reviewThread, assessed bool) string {
	var parts []string
	if c.Source == "github" {
		parts = append(parts, fmt.Sprintf("github by %s (id %d)", c.Author, c.GitHubID))
	} else {
		parts = append(parts, "agent")
		if c.Pushed {
			parts = append(parts, fmt.Sprintf("pushed (id %d)", c.GitHubID))
		} else {
			parts = append(parts, "unpushed")
		}
	}
	if c.Category != "" {
		parts = append(parts, "category "+c.Category)
	}
	if c.Verdict != "" {
		parts = append(parts, "verdict "+c.Verdict)
	}
	if c.Impact != "" {
		parts = append(parts, "impact "+c.Impact)
	}
	if assessed {
		parts = append(parts, "thread "+thread.Status)
		if thread.Impact != "" {
			parts = append(parts, "impact "+thread.Impact)
		}
	}
	if c.Outdated {
		parts = append(parts, "outdated")
	}
	if c.Resolved {
		parts = append(parts, "resolved")
	}
	return strings.Join(parts, ", ")
}

type dedupReviewFindingsInput struct{}

type reviewDedupResult struct {
	Removed  []string `json:"removed"`
	Clusters []struct {
		Kept    string   `json:"kept"`
		Removed []string `json:"removed"`
		Reason  string   `json:"reason"`
	} `json:"clusters"`
	Related []struct {
		IDs    []string `json:"ids"`
		Reason string   `json:"reason"`
	} `json:"related"`
	Moved []struct {
		ID   string `json:"id"`
		From int    `json:"from"`
		To   int    `json:"to"`
	} `json:"moved"`
	Trimmed []struct {
		ID        string `json:"id"`
		CoveredBy string `json:"covered_by"`
		Reason    string `json:"reason"`
	} `json:"trimmed"`
	Verdicts []struct {
		ID      string `json:"id"`
		Verdict string `json:"verdict"`
		Reason  string `json:"reason"`
	} `json:"verdicts"`
	Checked int      `json:"checked"`
	Errors  []string `json:"errors"`
}

func (s *Server) handleDedupReviewFindings(_ context.Context, _ *mcp.CallToolRequest, _ dedupReviewFindingsInput) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "dedup_review_findings", "channel_id", s.channelID)

	res, errResult, err := doAPICall[reviewDedupResult](s, "POST", s.reviewURL("/dedup"), http.StatusOK, nil)
	if errResult != nil || err != nil {
		return errResult, nil, err
	}
	if res.Checked == 0 {
		return textResult("Nothing to dedup: no file has an agent comment next to another comment."), nil, nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Checked %d comment(s); removed %d.\n", res.Checked, len(res.Removed))
	for _, c := range res.Clusters {
		fmt.Fprintf(&b, "- kept %s, removed %s: %s\n", c.Kept, strings.Join(c.Removed, ", "), c.Reason)
	}
	for _, t := range res.Trimmed {
		fmt.Fprintf(&b, "- trimmed %s to what %s doesn't cover: %s\n", t.ID, t.CoveredBy, t.Reason)
	}
	for _, m := range res.Moved {
		fmt.Fprintf(&b, "- moved %s from line %d to %d\n", m.ID, m.From, m.To)
	}
	for _, r := range res.Related {
		fmt.Fprintf(&b, "- related %s: %s\n", strings.Join(r.IDs, ", "), r.Reason)
	}
	for _, v := range res.Verdicts {
		fmt.Fprintf(&b, "- verdict %s %s: %s\n", v.ID, v.Verdict, v.Reason)
	}
	for _, e := range res.Errors {
		fmt.Fprintf(&b, "- error: %s\n", e)
	}
	return textResult(b.String()), nil, nil
}

type reviewCommentIDInput struct {
	CommentID string `json:"comment_id" jsonschema:"required,The comment id, as get_review_comments shows it"`
}

func (s *Server) handleDeleteReviewComment(_ context.Context, _ *mcp.CallToolRequest, input reviewCommentIDInput) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "delete_review_comment", "channel_id", s.channelID, "comment_id", input.CommentID)
	if input.CommentID == "" {
		return errorResult("comment_id is required"), nil, nil
	}
	if errResult, err := doAPICallNoBody(s, "DELETE", s.reviewCommentURL(input.CommentID, ""), http.StatusNoContent, nil); errResult != nil || err != nil {
		return errResult, nil, err
	}
	return textResult(fmt.Sprintf("Deleted review comment %s.", input.CommentID)), nil, nil
}

type updateReviewCommentInput struct {
	CommentID string `json:"comment_id" jsonschema:"required,The comment id, as get_review_comments shows it"`
	Body      string `json:"body,omitempty" jsonschema:"The comment's new body, replacing the old one"`
	Verdict   string `json:"verdict,omitempty" jsonschema:"Your verdict on the comment: real, false_positive or already_fixed"`
	Impact    string `json:"impact,omitempty" jsonschema:"With verdict real: blocking or nice_to_have"`
	Reason    string `json:"reason,omitempty" jsonschema:"One sentence on why, citing the line you read"`
}

func (s *Server) handleUpdateReviewComment(_ context.Context, _ *mcp.CallToolRequest, input updateReviewCommentInput) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "update_review_comment", "channel_id", s.channelID, "comment_id", input.CommentID)
	if input.CommentID == "" {
		return errorResult("comment_id is required"), nil, nil
	}
	if strings.TrimSpace(input.Body) == "" && input.Verdict == "" {
		return errorResult("body or verdict is required"), nil, nil
	}
	data, _ := json.Marshal(struct {
		Body    string `json:"body,omitempty"`
		Verdict string `json:"verdict,omitempty"`
		Impact  string `json:"impact,omitempty"`
		Reason  string `json:"verdict_reason,omitempty"`
	}{input.Body, input.Verdict, input.Impact, input.Reason})
	c, errResult, err := doAPICall[reviewComment](s, "PATCH", s.reviewCommentURL(input.CommentID, ""), http.StatusOK, data)
	if errResult != nil || err != nil {
		return errResult, nil, err
	}
	if c.Impact != "" {
		return textResult(fmt.Sprintf("Updated review comment %s at %s:%d; verdict %s, impact %s.", c.ID, c.Path, c.Line, c.Verdict, c.Impact)), nil, nil
	}
	if input.Verdict != "" {
		return textResult(fmt.Sprintf("Updated review comment %s at %s:%d; verdict %s.", c.ID, c.Path, c.Line, c.Verdict)), nil, nil
	}
	return textResult(fmt.Sprintf("Updated review comment %s at %s:%d.", c.ID, c.Path, c.Line)), nil, nil
}

type assessReviewThreadInput struct {
	CommentID string `json:"comment_id" jsonschema:"required,The id of the thread's first comment, as get_review_comments shows it"`
	Status    string `json:"status" jsonschema:"required,addressed or open"`
	Impact    string `json:"impact,omitempty" jsonschema:"With status open: blocking or nice_to_have"`
	Reason    string `json:"reason,omitempty" jsonschema:"One sentence on why, citing the line or reply that decided it"`
}

func (s *Server) handleAssessReviewThread(_ context.Context, _ *mcp.CallToolRequest, input assessReviewThreadInput) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "assess_review_thread", "channel_id", s.channelID, "comment_id", input.CommentID)
	if input.CommentID == "" {
		return errorResult("comment_id is required"), nil, nil
	}
	data, _ := json.Marshal(reviewThread{Status: input.Status, Impact: input.Impact, Reason: input.Reason})
	t, errResult, err := doAPICall[reviewThread](s, "POST", s.reviewCommentURL(input.CommentID, "/assess"), http.StatusOK, data)
	if errResult != nil || err != nil {
		return errResult, nil, err
	}
	if t.Impact != "" {
		return textResult(fmt.Sprintf("Assessed thread %s: %s, impact %s.", input.CommentID, t.Status, t.Impact)), nil, nil
	}
	return textResult(fmt.Sprintf("Assessed thread %s: %s.", input.CommentID, t.Status)), nil, nil
}

func (s *Server) handlePushReviewComment(_ context.Context, _ *mcp.CallToolRequest, input reviewCommentIDInput) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "push_review_comment", "channel_id", s.channelID, "comment_id", input.CommentID)
	if input.CommentID == "" {
		return errorResult("comment_id is required"), nil, nil
	}
	type pushResult struct {
		Already bool `json:"already"`
	}
	res, errResult, err := doAPICall[pushResult](s, "POST", s.reviewCommentURL(input.CommentID, "/push"), http.StatusOK, nil)
	if errResult != nil || err != nil {
		return errResult, nil, err
	}
	if res.Already {
		return textResult(fmt.Sprintf("Review comment %s was already pushed.", input.CommentID)), nil, nil
	}
	return textResult(fmt.Sprintf("Pushed review comment %s to the PR.", input.CommentID)), nil, nil
}

func (s *Server) handlePushAllReviewComments(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "push_all_review_comments", "channel_id", s.channelID)
	type pushAllResult struct {
		Pushed  int      `json:"pushed"`
		Skipped int      `json:"skipped"`
		Failed  int      `json:"failed"`
		Errors  []string `json:"errors"`
	}
	res, errResult, err := doAPICall[pushAllResult](s, "POST", s.reviewURL("/push-all"), http.StatusOK, nil)
	if errResult != nil || err != nil {
		return errResult, nil, err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Pushed %d review comment(s) to the PR; %d failed.\n", res.Pushed, res.Failed)
	if res.Skipped > 0 {
		fmt.Fprintf(&b, "Skipped %d marked false_positive or already_fixed.\n", res.Skipped)
	}
	for _, e := range res.Errors {
		fmt.Fprintf(&b, "- error: %s\n", e)
	}
	return textResult(b.String()), nil, nil
}
