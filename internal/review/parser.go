package review

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"strings"
)

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
