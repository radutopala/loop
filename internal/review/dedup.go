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

// The dedup pass. AddComment drops only a finding reported twice verbatim
// (an agent retry); a later round re-derives its findings, so it rewords
// them, anchors them elsewhere, or reports a symptom of an issue another
// finding names the cause of. Judging that takes a model, so after a
// multi-round review one reads everything the session holds and groups
// the comments by root cause; every group keeps one comment and the rest are
// deleted (see ParseDedupReply). A comment that bundles several issues, one
// of which another comment covers, can't be dropped without losing the
// rest, so the model may trim it to the part nothing else covers instead.
//
// The same pass checks each agent comment it keeps against the code and
// gives it a verdict (DedupVerdict), recorded on the comment. Nothing is
// deleted for a verdict: a false positive stays for the user to judge.

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

// DedupTrim rewrites a comment that bundles several issues to only the
// ones nothing else covers: CoveredBy is the comment that already reports
// the part cut out, Body the rewritten comment.
type DedupTrim struct {
	ID        string `json:"id"`
	CoveredBy string `json:"covered_by"`
	Body      string `json:"body"`
	Reason    string `json:"reason,omitempty"`
}

// The verdicts the dedup pass gives an agent comment once it has read the
// code the comment points at.
const (
	VerdictReal          = "real"
	VerdictFalsePositive = "false_positive"
	VerdictAlreadyFixed  = "already_fixed"
)

// DedupVerdict is the model's check of one agent comment against the code:
// Verdict is one of the Verdict constants, Reason says why.
type DedupVerdict struct {
	ID      string `json:"id"`
	Verdict string `json:"verdict"`
	Reason  string `json:"reason,omitempty"`
}

// DedupPlan is the model's reply once checked against the candidates.
type DedupPlan struct {
	Clusters []DedupCluster
	Related  []DedupRelated
	Moves    []DedupMove
	Trims    []DedupTrim
	Verdicts []DedupVerdict
}

// dedupReply is the JSON object the model answers with.
type dedupReply struct {
	Clusters []DedupCluster `json:"clusters"`
	Related  []DedupRelated `json:"related"`
	Moves    []DedupMove    `json:"moves"`
	Trims    []DedupTrim    `json:"trims"`
	Verdicts []DedupVerdict `json:"verdicts"`
}

// dedupBodyMax caps each comment body in the prompt. Enough to tell two
// findings apart; a finding's summary comes first.
const dedupBodyMax = 600

// DedupCandidates returns the comments worth handing to the model, sorted by
// file then line. Duplicates can span files, so that's every anchored
// comment, as long as one of them is the review agent's: that one needs a
// verdict even when it is alone, while with only GitHub comments there is
// nothing to check or delete. nil means no model run is needed.
func DedupCandidates(comments []*Comment) []*Comment {
	var out []*Comment
	for _, c := range comments {
		if c != nil && c.Path != "" {
			out = append(out, c)
		}
	}
	if !slices.ContainsFunc(out, deletable) {
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

// droppable reports whether the pass may fold c into another comment. A pass
// after a review run (fresh non-empty) runs without anyone asking for it, so
// it also leaves pushed comments alone: dropping one would delete it, and
// the replies under it, from the pull request. They are shown to the model
// as [github] comments, which it may keep but never drop.
func droppable(c *Comment, fresh map[string]bool) bool {
	return deletable(c) && (len(fresh) == 0 || !c.Pushed)
}

// DedupFresh returns the ids of the cands the pass may delete that aren't
// in before, the ids a review run started from: the comments the run
// added. A pass given them checks each against every other comment instead
// of regrouping the whole session, which an earlier pass already did. Empty
// means the run added nothing to fold.
func DedupFresh(cands []*Comment, before map[string]bool) map[string]bool {
	fresh := map[string]bool{}
	for _, c := range cands {
		if deletable(c) && !before[c.ID] {
			fresh[c.ID] = true
		}
	}
	return fresh
}

// BuildDedupPrompt renders the prompt for the model, listing cands (as
// returned by DedupCandidates) under a heading per file. When fresh (see
// DedupFresh) is non-empty, those comments are marked new and the model is
// asked to check them against the rest rather than regroup everything.
func BuildDedupPrompt(cands []*Comment, fresh map[string]bool) string {
	var b strings.Builder
	b.WriteString(`You are cleaning up a pull request review. Several review rounds each reported findings, and later rounds often report an issue again: on another line, in another file, or framed differently. Group the comments below by root cause, then check the ones you keep against the code.

Rules:
- Two comments are the same issue when they share a root cause, so that one change fixes both. That holds across files (say, where a value is built and where it is used), and when one comment describes the cause and the other a symptom or consequence of it. The same wording about separate places that each need their own fix is not the same issue.
- If fixing the kept comment's root cause also resolves the other comment, the other is a duplicate, even when a narrower fix for it alone exists.
- A comment can bundle several issues. It duplicates any comment that reports one of them. Prefer keeping the bundled comment and dropping the single-issue one into it. When the single-issue one should be kept instead (a [github] comment, or more severe or specific), keep it and list the bundled [agent] comment under "trims": "covered_by" is the comment that reports the shared issue, and "body" is the bundled comment rewritten to only the issues nothing else covers, in its own words. Never trim a comment down to nothing; drop it instead.
- In each group, keep one comment. Keep a [github] comment if one covers the issue, since those are never removed. Otherwise keep the most severe and specific one: the one naming the worst concrete consequence, then the one anchored where the fix goes, then the clearer one.
- "reason": one short sentence on what the group has in common.
- "note": one or two sentences on what the dropped comments raise that the kept one does not, such as another consequence or another place the fix must cover. It is appended to the kept comment. Leave it empty when they add nothing.
- Only [agent] comments may be dropped.
- Comments about the same code path or behaviour that still need separate fixes, where fixing either one leaves the other standing, are related, not duplicates: list them under "related" with a reason. Nothing is dropped for them.
- For every [agent] comment you don't drop, open its file with the Read tool around its line and decide whether the finding holds: "real" (the issue is there), "false_positive" (the code doesn't do what the comment says, or the behaviour is intended, handled elsewhere, or exactly what the pull request sets out to do), or "already_fixed" (it was real, but this checkout no longer has it). List it under "verdicts" with a one-sentence "reason" citing the file:line you read that decided it. A verdict deletes nothing. Verdicts and moves for a comment whose file you did not Read are discarded, so read before you judge; never judge from the comment text alone.
- The line numbers were often counted from diff hunks and can be a few lines off. For every [agent] comment you don't drop, read its file with line numbers (the Read tool shows them) and check that its line is the statement the finding is about. When it isn't, as when it sits on a blank line, a lone closing brace, or a neighbouring statement, list the right line in the same file under "moves".
- A body longer than ` + fmt.Sprint(dedupBodyMax) + ` characters is cut and ends in "...". When the cut part matters to your call, read the full body with the get_review_comments tool (pass "path" to list one file's comments); the ids are the ones below.
- You may read the files in your working directory. Do not change anything.
`)
	if len(fresh) > 0 {
		b.WriteString(`- The comments marked [agent, new] are what the latest review round added; an earlier pass already grouped the rest. Check every new comment against every other comment, the [github] ones included, and group it with the comment it repeats. Compare root causes, not wording: a new comment usually rewords an issue another comment already raised, often on another line or in another file. Only the new comments need their lines checked and a verdict. You need not regroup the older comments among themselves, though you may when you spot a duplicate.
`)
	}
	b.WriteString(`
Reply with only a JSON object, no other text:
{"clusters":[{"keep":"<id>","drop":["<id>"],"reason":"...","note":"..."}],"trims":[{"id":"<id>","covered_by":"<id>","body":"...","reason":"..."}],"related":[{"ids":["<id>","<id>"],"reason":"..."}],"moves":[{"id":"<id>","line":0}],"verdicts":[{"id":"<id>","verdict":"real","reason":"..."}]}
Outside "verdicts", leave out comments that have no duplicate, nothing related, and the right line; use [] for an empty list.

Comments, by file:
`)
	path := ""
	for _, c := range cands {
		if c.Path != path {
			path = c.Path
			fmt.Fprintf(&b, "\n## %s\n", path)
		}
		label := "agent"
		switch {
		case !droppable(c, fresh):
			label = "github"
		case fresh[c.ID]:
			label = "agent, new"
		}
		fmt.Fprintf(&b, "- id=%s [%s] L%d (%s): %s\n", c.ID, label, c.Line, effectiveSide(c.Side), PromptBody(c.Body))
	}
	return b.String()
}

// PromptBody renders a comment body as one capped line for the dedup pass's
// prompt, which lists comments. A finding
// is summary + blank line + failure scenario (see ParseReportFindings), and
// written verbatim it spills over several lines, which stops reading as a
// list.
func PromptBody(body string) string {
	return oneLine(body, dedupBodyMax)
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
// GitHub comment (or, with fresh non-empty, a pushed one; see droppable),
// or is some group's keeper, so a confused reply can't chain
// groups into deleting every copy of an issue; a group left with nothing to
// drop is left out. Related groups keep their known ids, with a dropped id
// standing for its keeper, and need two distinct ones. A move counts only
// for a kept agent comment, to a positive line within nearbyLines of where
// it was, since a correction is a nudge, not a new finding; the first move
// named for an id wins. A trim counts only for a kept agent comment, covered
// by another known comment (a dropped one standing for its keeper), to a
// non-empty body shorter than the one it replaces; a comment is trimmed
// once, and a trimmed comment can't cover another trim, so two bundles
// can't each cut the issue they share and lose it. A verdict counts only
// for a kept agent comment and a known Verdict value; the first one named
// for an id wins. An error means the reply held no parseable JSON object.
func ParseDedupReply(reply string, cands []*Comment, fresh map[string]bool) (DedupPlan, error) {
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
			if c == nil || keepers[id] || keeperOf[id] != "" || !droppable(c, fresh) {
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
	trimmed, covers := map[string]bool{}, map[string]bool{}
	for _, tr := range parsed.Trims {
		c := byID[tr.ID]
		cover := tr.CoveredBy
		if keep := keeperOf[cover]; keep != "" {
			cover = keep
		}
		body := strings.TrimSpace(tr.Body)
		if c == nil || !deletable(c) || keeperOf[tr.ID] != "" || trimmed[tr.ID] || covers[tr.ID] ||
			byID[cover] == nil || cover == tr.ID || trimmed[cover] ||
			body == "" || len(body) >= len(strings.TrimSpace(c.Body)) {
			continue
		}
		trimmed[tr.ID], covers[cover] = true, true
		plan.Trims = append(plan.Trims, DedupTrim{ID: tr.ID, CoveredBy: cover, Body: body, Reason: strings.TrimSpace(tr.Reason)})
	}
	judged := map[string]bool{}
	for _, v := range parsed.Verdicts {
		c := byID[v.ID]
		if c == nil || !deletable(c) || keeperOf[v.ID] != "" || judged[v.ID] || !knownVerdict(v.Verdict) {
			continue
		}
		judged[v.ID] = true
		plan.Verdicts = append(plan.Verdicts, DedupVerdict{ID: v.ID, Verdict: v.Verdict, Reason: strings.TrimSpace(v.Reason)})
	}
	return plan, nil
}

// knownVerdict reports whether v is one of the Verdict constants.
func knownVerdict(v string) bool {
	return v == VerdictReal || v == VerdictFalsePositive || v == VerdictAlreadyFixed
}

// WithDedupNote returns body with note appended as its own paragraph, for a
// kept comment that absorbs what its dropped duplicates added.
func WithDedupNote(body, note string) string {
	return body + "\n\nAlso flagged: " + note
}
