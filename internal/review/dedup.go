package review

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

// The final dedup pass. AddComment already drops a finding that repeats one
// on a nearby line in similar words, but a later round can reword a finding
// past that similarity threshold, anchor it further away or in another file,
// or report a symptom of an issue another finding names the cause of. After a
// multi-round review a model reads everything the session holds and groups
// the comments by root cause; every group keeps one comment and the rest are
// deleted (see ParseDedupReply).

// DedupCluster is one group of comments the model judged to report the same
// issue: Keep stays, Drop is deleted. Reason says what they share; Note is
// what the dropped comments add, to be appended to the kept one.
type DedupCluster struct {
	Keep   string   `json:"keep"`
	Drop   []string `json:"drop"`
	Reason string   `json:"reason,omitempty"`
	Note   string   `json:"note,omitempty"`
}

// DedupRelated is a group of comments about the same code path or behaviour
// that still need separate fixes. It is reported, and nothing is deleted.
type DedupRelated struct {
	IDs    []string `json:"ids"`
	Reason string   `json:"reason,omitempty"`
}

// DedupMove re-anchors a comment the model found on the wrong line: a
// review agent that reads the diff, which carries no line numbers, counts
// its way to a line and often lands a line or three off.
type DedupMove struct {
	ID   string `json:"id"`
	Line int    `json:"line"`
}

// DedupPlan is the model's reply once checked against the candidates.
type DedupPlan struct {
	Clusters []DedupCluster
	Related  []DedupRelated
	Moves    []DedupMove
}

// dedupReply is the JSON object the model answers with.
type dedupReply struct {
	Clusters []DedupCluster `json:"clusters"`
	Related  []DedupRelated `json:"related"`
	Moves    []DedupMove    `json:"moves"`
}

// dedupBodyMax caps each comment body in the prompt. Enough to tell two
// findings apart; a finding's summary comes first.
const dedupBodyMax = 600

// DedupCandidates returns the comments worth handing to the model, sorted by
// file then line. Duplicates can span files, so that's every anchored
// comment, as long as there are at least two and one of them is the review
// agent's: with fewer there's nothing to fold, and with only GitHub comments
// nothing the pass may delete. nil means no model run is needed.
func DedupCandidates(comments []*Comment) []*Comment {
	var out []*Comment
	for _, c := range comments {
		if c != nil && c.Path != "" {
			out = append(out, c)
		}
	}
	if len(out) < 2 || !slices.ContainsFunc(out, deletable) {
		return nil
	}
	slices.SortStableFunc(out, func(a, b *Comment) int {
		return cmp.Or(strings.Compare(a.Path, b.Path), cmp.Compare(a.Line, b.Line), strings.Compare(a.ID, b.ID))
	})
	return out
}

// deletable reports whether the dedup pass may delete c. Only the review
// agent's comments qualify: a GitHub comment is someone's, the user's own
// included, and is only there as context and as a possible keeper.
func deletable(c *Comment) bool {
	return c.Source != "github"
}

// BuildDedupPrompt renders the prompt for the model, listing cands (as
// returned by DedupCandidates) under a heading per file.
func BuildDedupPrompt(cands []*Comment) string {
	var b strings.Builder
	b.WriteString(`You are cleaning up a pull request review. Several review rounds each reported findings, and later rounds often report an issue again: on another line, in another file, or framed differently. Group the comments below by root cause.

Rules:
- Two comments are the same issue when they share a root cause, so that one change fixes both. That holds across files (say, where a value is built and where it is used), and when one comment describes the cause and the other a symptom or consequence of it. The same wording about separate places that each need their own fix is not the same issue.
- In each group, keep one comment. Keep a [github] comment if one covers the issue, since those are never removed. Otherwise keep the most severe and specific one: the one naming the worst concrete consequence, then the one anchored where the fix goes, then the clearer one.
- "reason": one short sentence on what the group has in common.
- "note": one or two sentences on what the dropped comments raise that the kept one does not, such as another consequence or another place the fix must cover. It is appended to the kept comment. Leave it empty when they add nothing.
- Only [agent] comments may be dropped.
- Comments about the same code path or behaviour that still need separate fixes are related, not duplicates: list them under "related" with a reason. Nothing is dropped for them.
- The line numbers were often counted from diff hunks and can be a few lines off. For every [agent] comment you don't drop, read its file with line numbers (the Read tool shows them) and check that its line is the statement the finding is about. When it isn't, as when it sits on a blank line, a lone closing brace, or a neighbouring statement, list the right line in the same file under "moves".
- You may read the files in your working directory. Do not change anything.

Reply with only a JSON object, no other text:
{"clusters":[{"keep":"<id>","drop":["<id>"],"reason":"...","note":"..."}],"related":[{"ids":["<id>","<id>"],"reason":"..."}],"moves":[{"id":"<id>","line":0}]}
Leave out comments that have no duplicate, nothing related, and the right line. If there are none, reply {"clusters":[],"related":[],"moves":[]}.

Comments, by file:
`)
	path := ""
	for _, c := range cands {
		if c.Path != path {
			path = c.Path
			fmt.Fprintf(&b, "\n## %s\n", path)
		}
		label := "agent"
		if !deletable(c) {
			label = "github"
		}
		fmt.Fprintf(&b, "- id=%s [%s] L%d (%s): %s\n", c.ID, label, c.Line, effectiveSide(c.Side), oneLine(c.Body, dedupBodyMax))
	}
	return b.String()
}

// oneLine collapses body's whitespace and caps it at limit bytes, backing up
// to a rune boundary.
func oneLine(body string, limit int) string {
	body = strings.Join(strings.Fields(body), " ")
	if len(body) <= limit {
		return body
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(body[cut]) {
		cut--
	}
	return body[:cut] + "..."
}

// ParseDedupReply parses the model's reply and checks it against cands
// rather than trusting it. A drop is ignored when its id is unknown, is a
// GitHub comment, or is some group's keeper, so a confused reply can't chain
// groups into deleting every copy of an issue; a group left with nothing to
// drop is left out. Related groups keep their known ids, with a dropped id
// standing for its keeper, and need two distinct ones. A move counts only
// for a kept agent comment, to a positive line within nearbyLines of where
// it was, since a correction is a nudge, not a new finding; the first move
// named for an id wins. An error means the reply held no parseable JSON
// object.
func ParseDedupReply(reply string, cands []*Comment) (DedupPlan, error) {
	start, end := strings.Index(reply, "{"), strings.LastIndex(reply, "}")
	if start < 0 || end < start {
		return DedupPlan{}, errors.New("dedup reply has no JSON object")
	}
	var parsed dedupReply
	if err := json.Unmarshal([]byte(reply[start:end+1]), &parsed); err != nil {
		return DedupPlan{}, fmt.Errorf("parsing dedup reply: %w", err)
	}
	byID := make(map[string]*Comment, len(cands))
	for _, c := range cands {
		byID[c.ID] = c
	}
	keepers := map[string]bool{}
	for _, cl := range parsed.Clusters {
		if byID[cl.Keep] != nil {
			keepers[cl.Keep] = true
		}
	}
	var plan DedupPlan
	keeperOf := map[string]string{}
	for _, cl := range parsed.Clusters {
		if byID[cl.Keep] == nil {
			continue
		}
		var drops []string
		for _, id := range cl.Drop {
			c := byID[id]
			if c == nil || keepers[id] || keeperOf[id] != "" || !deletable(c) {
				continue
			}
			keeperOf[id] = cl.Keep
			drops = append(drops, id)
		}
		if len(drops) > 0 {
			plan.Clusters = append(plan.Clusters, DedupCluster{Keep: cl.Keep, Drop: drops, Reason: strings.TrimSpace(cl.Reason), Note: strings.TrimSpace(cl.Note)})
		}
	}
	for _, rel := range parsed.Related {
		var ids []string
		for _, id := range rel.IDs {
			if keep := keeperOf[id]; keep != "" {
				id = keep
			}
			if byID[id] != nil && !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
		if len(ids) >= 2 {
			plan.Related = append(plan.Related, DedupRelated{IDs: ids, Reason: strings.TrimSpace(rel.Reason)})
		}
	}
	moved := map[string]bool{}
	for _, mv := range parsed.Moves {
		c := byID[mv.ID]
		if c == nil || !deletable(c) || keeperOf[mv.ID] != "" || moved[mv.ID] ||
			mv.Line <= 0 || mv.Line == c.Line || abs(mv.Line-c.Line) > nearbyLines {
			continue
		}
		moved[mv.ID] = true
		plan.Moves = append(plan.Moves, mv)
	}
	return plan, nil
}

// WithDedupNote returns body with note appended as its own paragraph, for a
// kept comment that absorbs what its dropped duplicates added.
func WithDedupNote(body, note string) string {
	return body + "\n\nAlso flagged: " + note
}
