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
func (s *Server) registerReviewTools() {
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "report_review_findings",
		Description: "Report code-review findings for the current channel's PR review session. Each finding needs the repo-relative file path, the 1-based line number, and a body describing the bug, the concrete inputs/state that trigger it, and the wrong output or crash. Side is RIGHT for added/modified lines (default) or LEFT for lines removed from the base. Call once with the full list; duplicates are skipped server-side.",
	}, s.handleReportReviewFindings)
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "get_review_findings",
		Description: "Read a channel's PR review session, as shown in the Review panel: the PR, head SHA, status, and every comment with its id, file, line, side, whether it was pushed to GitHub, and its source (agent, or github with the author and GitHub comment id). Filter to unpushed agent comments or one file. The PR diff is not included.",
	}, s.handleGetReviewFindings)
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "dedup_review_findings",
		Description: "Run the final dedup pass over a channel's review session: an agent drops agent comments that repeat another comment on the same file, re-anchors misplaced ones, and notes related ones. GitHub comments are never deleted; removed comments that were already pushed are deleted from the PR too. Takes minutes; fails while a review run is in progress.",
	}, s.handleDedupReviewFindings)
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

type getReviewFindingsInput struct {
	ChannelID    string `json:"channel_id,omitempty" jsonschema:"The channel or thread whose review session to read. Optional — defaults to the current channel/thread this agent is running in."`
	UnpushedOnly bool   `json:"unpushed_only,omitempty" jsonschema:"Only agent comments not yet pushed to GitHub"`
	Path         string `json:"path,omitempty" jsonschema:"Only comments on this repo-relative file path"`
}

// reviewComment is the part of a review comment get_review_findings shows.
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

// reviewChannel returns the channel a review tool acts on: the one asked
// for, or the agent's own.
func (s *Server) reviewChannel(channelID string) string {
	if channelID == "" {
		return s.channelID
	}
	return channelID
}

func (s *Server) handleGetReviewFindings(_ context.Context, _ *mcp.CallToolRequest, input getReviewFindingsInput) (*mcp.CallToolResult, any, error) {
	channelID := s.reviewChannel(input.ChannelID)
	s.logger.Info("mcp tool call", "tool", "get_review_findings", "channel_id", channelID)

	apiURL := fmt.Sprintf("%s/api/channels/%s/review?diff=false", s.apiURL, url.PathEscape(channelID))
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

type dedupReviewFindingsInput struct {
	ChannelID string `json:"channel_id,omitempty" jsonschema:"The channel or thread whose review session to dedup. Optional — defaults to the current channel/thread this agent is running in."`
}

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

func (s *Server) handleDedupReviewFindings(_ context.Context, _ *mcp.CallToolRequest, input dedupReviewFindingsInput) (*mcp.CallToolResult, any, error) {
	channelID := s.reviewChannel(input.ChannelID)
	s.logger.Info("mcp tool call", "tool", "dedup_review_findings", "channel_id", channelID)

	apiURL := fmt.Sprintf("%s/api/channels/%s/review/dedup", s.apiURL, url.PathEscape(channelID))
	res, errResult, err := doAPICall[reviewDedupResult](s, "POST", apiURL, http.StatusOK, nil)
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
