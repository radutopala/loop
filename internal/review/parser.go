package review

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// ReportFindingsTool is the built-in Claude Code tool the code-review
// command calls to hand its findings to the host. Loop watches the agent's
// stream for a tool_use block with this name and turns its input into
// review comments — see ParseReportFindings.
const ReportFindingsTool = "ReportFindings"

// reportFindings mirrors the ReportFindings tool's input schema. Only the
// fields the review panel renders are decoded; `level`, `short_summary`,
// `category`, `verdict` and `outcome` are ignored.
type reportFindings struct {
	Findings []struct {
		File            string `json:"file"`
		Line            int    `json:"line"`
		Summary         string `json:"summary"`
		FailureScenario string `json:"failure_scenario"`
	} `json:"findings"`
}

// ParseReportFindings converts a ReportFindings tool_use input into review
// comments. Findings the panel can't place — no file, no positive line, no
// text — are dropped rather than failing the batch, matching NewComment's
// best-effort contract. A body pairs the one-line summary with the concrete
// failure scenario, which is what the panel shows per comment. The tool
// carries no LEFT/RIGHT side; every finding is reported against the head
// revision, so NewComment's RIGHT default is correct.
func ParseReportFindings(input string) []*Comment {
	var parsed reportFindings
	if err := json.Unmarshal([]byte(input), &parsed); err != nil {
		return nil
	}
	comments := make([]*Comment, 0, len(parsed.Findings))
	for _, f := range parsed.Findings {
		body := strings.TrimSpace(f.Summary)
		if scenario := strings.TrimSpace(f.FailureScenario); scenario != "" {
			if body != "" {
				body += "\n\n"
			}
			body += scenario
		}
		if c := NewComment(f.File, f.Line, "", body); c != nil {
			comments = append(comments, c)
		}
	}
	return comments
}

// NewComment builds a Comment from the raw finding fields the agent
// reports through the report_review_findings MCP tool, normalizing side
// (anything but LEFT becomes RIGHT) and deriving the stable dedup ID.
// Returns nil when a required field is missing or line is not positive —
// best-effort: the FE just sees the findings the agent got right, rather
// than the whole batch failing.
func NewComment(path string, line int, side, body string) *Comment {
	path = strings.TrimSpace(path)
	body = strings.TrimSpace(body)
	if path == "" || body == "" || line <= 0 {
		return nil
	}
	side = strings.ToUpper(strings.TrimSpace(side))
	if side != "LEFT" {
		side = "RIGHT"
	}
	return &Comment{
		ID:   commentID(path, line, body),
		Path: path,
		Line: line,
		Side: side,
		Body: body,
	}
}

// commentID produces a stable id from the comment's identifying triple
// so reporting a finding twice (e.g. agent retries) results in the same
// id and dedupe at write time is possible. A SHA1 truncated to 12 hex
// chars is more than enough uniqueness for an ephemeral per-session list.
func commentID(path string, line int, body string) string {
	h := sha1.Sum(fmt.Appendf(nil, "%s\x00%d\x00%s", path, line, body))
	return hex.EncodeToString(h[:6])
}
