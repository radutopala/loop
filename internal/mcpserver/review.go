package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// registerReviewTools adds the review-session tools. Registered
// unconditionally: outside a review run the daemon answers 404 (no review
// session for the channel) and the tool surfaces that as an error result.
// Every one of them acts on the agent's own channel; the daemon holds the
// ones that change comments to it too.
func (s *Server) registerReviewTools() {
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "report_review_findings",
		Description: "Report code-review findings for the current channel's PR review session. Each finding needs the repo-relative file path, the 1-based line number, and a body describing the bug, the concrete inputs/state that trigger it, and the wrong output or crash. Side is RIGHT for added/modified lines (default) or LEFT for lines removed from the base. Call once with the full list; a finding reported twice verbatim is skipped server-side.",
	}, s.handleReportReviewFindings)
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "get_review_comments",
		Description: "Read this channel's PR review session, as shown in the Review panel: the PR, head SHA, status, and every comment with its id, file, line, side, whether it was pushed to GitHub, and its source (agent, or github with the author and GitHub comment id), then its body. Filter to unpushed agent comments or one file. The PR diff is not included.",
	}, s.handleGetReviewComments)
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "dedup_review_findings",
		Description: "Run the final dedup pass over this channel's review session: an agent drops agent comments that repeat another comment, re-anchors misplaced ones, and notes related ones. GitHub comments are never deleted; removed comments that were already pushed are deleted from the PR too. Takes minutes; fails while a review run is in progress.",
	}, s.handleDedupReviewFindings)
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "delete_review_comment",
		Description: "Delete an agent comment from this channel's review session, by the id get_review_comments shows. A comment already pushed is deleted from the PR too. GitHub comments (source github) are refused.",
	}, s.handleDeleteReviewComment)
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "update_review_comment",
		Description: "Replace the body of an unpushed agent comment in this channel's review session, e.g. to fold in what a duplicate you are deleting adds. Pushed and GitHub comments can't be edited.",
	}, s.handleUpdateReviewComment)
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "push_review_comment",
		Description: "Post one agent comment from this channel's review session to the PR on GitHub, as the configured gh user. A comment already pushed is left as is.",
	}, s.handlePushReviewComment)
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "push_all_review_comments",
		Description: "Post every unpushed agent comment in this channel's review session to the PR on GitHub, as the configured gh user. Reports how many were pushed and which failed.",
	}, s.handlePushAllReviewComments)
}

type reviewFindingInput struct {
	Path string `json:"path" jsonschema:"required,Repo-relative file path the finding is in"`
	Line int    `json:"line" jsonschema:"required,1-based line number the finding anchors to"`
	Side string `json:"side,omitempty" jsonschema:"RIGHT for added/modified lines (default); LEFT only for lines removed from the base"`
	Body string `json:"body" jsonschema:"required,One paragraph: the bug's trigger and the wrong output or crash"`
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
	Pushed   bool   `json:"pushed"`
	Source   string `json:"source"`
	Author   string `json:"author"`
	GitHubID int64  `json:"github_id"`
	Outdated bool   `json:"outdated"`
	Resolved bool   `json:"resolved"`
}

type reviewSession struct {
	Present bool `json:"present"`
	Session struct {
		PR *struct {
			Number int    `json:"number"`
			URL    string `json:"url"`
			Title  string `json:"title"`
		} `json:"pr"`
		HeadSHA  string          `json:"head_sha"`
		Status   string          `json:"status"`
		Error    string          `json:"error"`
		Comments []reviewComment `json:"comments"`
	} `json:"session"`
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

	var shown []reviewComment
	for _, c := range sess.Comments {
		if input.UnpushedOnly && (c.Pushed || c.Source == "github") {
			continue
		}
		if input.Path != "" && c.Path != input.Path {
			continue
		}
		shown = append(shown, c)
	}
	fmt.Fprintf(&b, "\n%d of %d comment(s):\n", len(shown), len(sess.Comments))
	for _, c := range shown {
		fmt.Fprintf(&b, "\n[%s] %s:%d %s, %s\n", c.ID, c.Path, c.Line, c.Side, reviewCommentState(c))
		for line := range strings.SplitSeq(strings.TrimSpace(c.Body), "\n") {
			fmt.Fprintf(&b, "  %s\n", line)
		}
	}
	return textResult(b.String()), nil, nil
}

// reviewCommentState describes where a comment came from and where it is.
func reviewCommentState(c reviewComment) string {
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
	for _, m := range res.Moved {
		fmt.Fprintf(&b, "- moved %s from line %d to %d\n", m.ID, m.From, m.To)
	}
	for _, r := range res.Related {
		fmt.Fprintf(&b, "- related %s: %s\n", strings.Join(r.IDs, ", "), r.Reason)
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
	Body      string `json:"body" jsonschema:"required,The comment's new body, replacing the old one"`
}

func (s *Server) handleUpdateReviewComment(_ context.Context, _ *mcp.CallToolRequest, input updateReviewCommentInput) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "update_review_comment", "channel_id", s.channelID, "comment_id", input.CommentID)
	if input.CommentID == "" {
		return errorResult("comment_id is required"), nil, nil
	}
	if strings.TrimSpace(input.Body) == "" {
		return errorResult("body is required"), nil, nil
	}
	data, _ := json.Marshal(map[string]string{"body": input.Body})
	c, errResult, err := doAPICall[reviewComment](s, "PATCH", s.reviewCommentURL(input.CommentID, ""), http.StatusOK, data)
	if errResult != nil || err != nil {
		return errResult, nil, err
	}
	return textResult(fmt.Sprintf("Updated review comment %s at %s:%d.", c.ID, c.Path, c.Line)), nil, nil
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
		Pushed int      `json:"pushed"`
		Failed int      `json:"failed"`
		Errors []string `json:"errors"`
	}
	res, errResult, err := doAPICall[pushAllResult](s, "POST", s.reviewURL("/push-all"), http.StatusOK, nil)
	if errResult != nil || err != nil {
		return errResult, nil, err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Pushed %d review comment(s) to the PR; %d failed.\n", res.Pushed, res.Failed)
	for _, e := range res.Errors {
		fmt.Fprintf(&b, "- error: %s\n", e)
	}
	return textResult(b.String()), nil, nil
}
