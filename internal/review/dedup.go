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
// past that similarity threshold, or anchor it further away. After a
// multi-round review a model reads what the session holds, file by file, and
// groups the comments that report the same issue; every group keeps one
// comment and the rest are deleted (see DedupDrops).

// DedupCluster is one group of comments the model judged to report the same
// issue: Keep stays, Drop is deleted.
type DedupCluster struct {
	Keep string   `json:"keep"`
	Drop []string `json:"drop"`
}

// dedupReply is the JSON object the model answers with.
type dedupReply struct {
	Clusters []DedupCluster `json:"clusters"`
}

// dedupBodyMax caps each comment body in the prompt. Enough to tell two
// findings apart; a finding's summary comes first.
const dedupBodyMax = 600

// DedupCandidates returns the comments worth handing to the model, sorted by
// file then line: those in a file that holds at least two comments, one of
// them the review agent's. A file with a single comment has nothing to fold,
// and one with only GitHub comments has nothing the pass may delete. nil
// means there's nothing to dedup, and no model run is needed.
func DedupCandidates(comments []*Comment) []*Comment {
	byPath := map[string][]*Comment{}
	for _, c := range comments {
		if c == nil || c.Path == "" {
			continue
		}
		byPath[c.Path] = append(byPath[c.Path], c)
	}
	var out []*Comment
	for _, group := range byPath {
		if len(group) >= 2 && slices.ContainsFunc(group, deletable) {
			out = append(out, group...)
		}
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
	b.WriteString(`You are cleaning up a pull request review. Several review rounds each reported findings, and later rounds sometimes report an issue again: on a nearby line, or in different words. Group the comments below that describe the same underlying issue.

Rules:
- Only group comments on the same file.
- Two comments are the same issue when fixing one would fix the other. Related but distinct problems stay separate.
- In each group, keep one comment: the one anchored on the line where the problem is, then the clearer and more complete one. Prefer a [github] comment as the keeper when it covers the issue, since it will not be removed.
- Only [agent] comments may be dropped.
- You may read the files in your working directory to check where an issue is. Do not change anything.

Reply with only a JSON object, no other text:
{"clusters":[{"keep":"<id>","drop":["<id>"]}]}
Leave out comments that have no duplicate. If nothing is duplicated, reply {"clusters":[]}.

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

// DedupDrops parses the model's reply and returns the ids of the comments to
// delete, in the order the reply names them. The reply is checked against
// cands rather than trusted: a drop is ignored when its id is unknown, is a
// GitHub comment, is on a different file from its keeper, or is some
// group's keeper, so a confused reply can't chain groups into deleting every
// copy of an issue. An error means the reply held no parseable JSON object.
func DedupDrops(reply string, cands []*Comment) ([]string, error) {
	start, end := strings.Index(reply, "{"), strings.LastIndex(reply, "}")
	if start < 0 || end < start {
		return nil, errors.New("dedup reply has no JSON object")
	}
	var parsed dedupReply
	if err := json.Unmarshal([]byte(reply[start:end+1]), &parsed); err != nil {
		return nil, fmt.Errorf("parsing dedup reply: %w", err)
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
	var drops []string
	dropped := map[string]bool{}
	for _, cl := range parsed.Clusters {
		keep := byID[cl.Keep]
		if keep == nil {
			continue
		}
		for _, id := range cl.Drop {
			c := byID[id]
			if c == nil || keepers[id] || dropped[id] || !deletable(c) || c.Path != keep.Path {
				continue
			}
			dropped[id] = true
			drops = append(drops, id)
		}
	}
	return drops, nil
}
