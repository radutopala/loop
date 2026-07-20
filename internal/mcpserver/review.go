package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// registerReviewTools adds the review-findings reporting tool. Registered
// unconditionally: outside a review run the daemon answers 404 (no review
// session for the channel) and the tool surfaces that as an error result.
func (s *Server) registerReviewTools() {
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "report_review_findings",
		Description: "Report code-review findings for the current channel's PR review session. Each finding needs the repo-relative file path, the 1-based line number, and a body describing the bug, the concrete inputs/state that trigger it, and the wrong output or crash. Side is RIGHT for added/modified lines (default) or LEFT for lines removed from the base. Call once with the full list; duplicates are skipped server-side.",
	}, s.handleReportReviewFindings)
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
