package review

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// commentBlockRE matches the grammar the review prompt instructs the
// agent to emit:
//
//	<review-comment path="..." line="N" side="RIGHT">
//	body text
//	</review-comment>
//
// side is optional and defaults to "RIGHT". We use a tolerant attribute
// regex so the agent doesn't need to escape quotes inside attribute
// values that are already plain identifiers (paths, integers, RIGHT/LEFT).
var commentBlockRE = regexp.MustCompile(`(?s)<review-comment\s+([^>]+)>(.*?)</review-comment>`)
var attrRE = regexp.MustCompile(`(\w+)\s*=\s*"([^"]*)"`)

// ParseComments scans text and returns every well-formed <review-comment>
// block it finds. Blocks with missing required attributes or a non-numeric
// line value are skipped (best-effort: the FE just sees the comments the
// agent got right, rather than the whole turn failing).
func ParseComments(text string) []*Comment {
	matches := commentBlockRE.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return nil
	}
	out := make([]*Comment, 0, len(matches))
	for _, m := range matches {
		attrs := parseAttrs(m[1])
		path := attrs["path"]
		lineStr := attrs["line"]
		side := strings.ToUpper(strings.TrimSpace(attrs["side"]))
		if path == "" || lineStr == "" {
			continue
		}
		line, err := strconv.Atoi(strings.TrimSpace(lineStr))
		if err != nil || line <= 0 {
			continue
		}
		if side != "LEFT" {
			side = "RIGHT"
		}
		body := strings.TrimSpace(m[2])
		if body == "" {
			continue
		}
		out = append(out, &Comment{
			ID:   commentID(path, line, body),
			Path: path,
			Line: line,
			Side: side,
			Body: body,
		})
	}
	return out
}

func parseAttrs(raw string) map[string]string {
	out := make(map[string]string)
	for _, m := range attrRE.FindAllStringSubmatch(raw, -1) {
		out[m[1]] = m[2]
	}
	return out
}

// commentID produces a stable id from the comment's identifying triple
// so streaming a comment twice (e.g. agent retries) results in the same
// id and dedupe at write time is possible. A SHA1 truncated to 12 hex
// chars is more than enough uniqueness for an ephemeral per-session list.
func commentID(path string, line int, body string) string {
	h := sha1.Sum(fmt.Appendf(nil, "%s\x00%d\x00%s", path, line, body))
	return hex.EncodeToString(h[:6])
}
