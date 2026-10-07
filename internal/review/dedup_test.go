package review

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type DedupSuite struct {
	suite.Suite
}

func TestDedupSuite(t *testing.T) {
	suite.Run(t, new(DedupSuite))
}

func ids(cs []*Comment) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		out = append(out, c.ID)
	}
	return out
}

func (s *DedupSuite) TestDedupCandidates() {
	agent := func(id, path string, line int) *Comment {
		return &Comment{ID: id, Path: path, Line: line, Source: "agent"}
	}
	gh := func(id, path string, line int) *Comment {
		return &Comment{ID: id, Path: path, Line: line, Source: "github"}
	}
	cases := []struct {
		name     string
		comments []*Comment
		want     []string
	}{
		{name: "empty", comments: nil, want: []string{}},
		{name: "a single comment", comments: []*Comment{agent("a", "x.go", 1)}, want: []string{}},
		{
			name:     "only github comments",
			comments: []*Comment{gh("g1", "x.go", 1), gh("g2", "y.go", 5)},
			want:     []string{},
		},
		{
			name:     "one comment per file still pairs across files",
			comments: []*Comment{agent("b", "y.go", 1), agent("a", "x.go", 1)},
			want:     []string{"a", "b"},
		},
		{
			name: "every anchored comment, by file then line",
			comments: []*Comment{
				agent("b", "y.go", 9), nil, agent("a", "y.go", 3),
				gh("g", "x.go", 40), agent("c", "x.go", 2),
				agent("lone", "z.go", 1), {ID: "nopath", Line: 1},
			},
			want: []string{"c", "g", "a", "b", "lone"},
		},
		{
			name:     "same line sorts by id",
			comments: []*Comment{agent("b", "x.go", 1), agent("a", "x.go", 1)},
			want:     []string{"a", "b"},
		},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			require.Equal(s.T(), tc.want, ids(DedupCandidates(tc.comments)))
		})
	}
}

func (s *DedupSuite) TestBuildDedupPrompt() {
	cands := []*Comment{
		{ID: "a1", Path: "x.go", Line: 3, Source: "agent", Body: "Nil deref\n\n  when the map is empty."},
		{ID: "gh-9", Path: "x.go", Line: 7, Side: "LEFT", Source: "github", Body: "same thing"},
		{ID: "b2", Path: "y.go", Line: 1, Body: strings.Repeat("é", dedupBodyMax)},
	}
	got := BuildDedupPrompt(cands)
	require.Contains(s.T(), got, `{"clusters":[{"keep":"<id>","drop":["<id>"],"reason":"...","note":"..."}],"trims":[{"id":"<id>","covered_by":"<id>","body":"...","reason":"..."}],"related":[{"ids":["<id>","<id>"],"reason":"..."}],"moves":[{"id":"<id>","line":0}]}`)
	require.Contains(s.T(), got, "a lone closing brace")
	require.Contains(s.T(), got, "share a root cause")
	// A symptom with a narrower fix of its own is still a duplicate of the
	// cause it follows from.
	require.Contains(s.T(), got, "If fixing the kept comment's root cause also resolves the other comment, the other is a duplicate, even when a narrower fix for it alone exists.")
	require.Contains(s.T(), got, "where fixing either one leaves the other standing, are related")
	// A bundled comment is folded rather than parked under related.
	require.Contains(s.T(), got, "A comment can bundle several issues. It duplicates any comment that reports one of them. Prefer keeping the bundled comment and dropping the single-issue one into it.")
	require.Contains(s.T(), got, `list the bundled [agent] comment under "trims"`)
	require.Contains(s.T(), got, "\n## x.go\n- id=a1 [agent] L3 (RIGHT): Nil deref when the map is empty.\n- id=gh-9 [github] L7 (LEFT): same thing\n")
	require.Contains(s.T(), got, "\n## y.go\n- id=b2 [agent] L1 (RIGHT): ")
	require.Equal(s.T(), 1, strings.Count(got, "## x.go"))
	require.True(s.T(), strings.HasSuffix(got, "...\n"))
}

func (s *DedupSuite) TestOneLine() {
	cases := []struct {
		name, body string
		limit      int
		want       string
	}{
		{name: "short", body: " a \n b ", limit: 10, want: "a b"},
		{name: "exact", body: "abcd", limit: 4, want: "abcd"},
		{name: "cut", body: "abcdef", limit: 4, want: "abcd..."},
		{name: "backs up to a rune start", body: "aé", limit: 2, want: "a..."},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			require.Equal(s.T(), tc.want, oneLine(tc.body, tc.limit))
		})
	}
}

func (s *DedupSuite) TestParseDedupReply() {
	cands := []*Comment{
		{ID: "a", Path: "x.go", Line: 10, Source: "agent"},
		{ID: "b", Path: "x.go", Line: 14, Source: "agent"},
		{ID: "c", Path: "x.go", Line: 40, Source: "agent"},
		{ID: "g", Path: "x.go", Line: 11, Source: "github"},
		{ID: "d", Path: "y.go", Line: 1, Source: "agent"},
		{ID: "e", Path: "y.go", Line: 2},
	}
	cases := []struct {
		name    string
		reply   string
		want    DedupPlan
		wantErr string
	}{
		{name: "nothing", reply: `{"clusters":[],"related":[]}`, want: DedupPlan{}},
		{
			name:  "prose and fences around the object; reason and note trimmed",
			reply: "Here you go:\n```json\n{\"clusters\":[{\"keep\":\"a\",\"drop\":[\"b\",\"c\"],\"reason\":\" nil map \",\"note\":\" also on reload \"}]}\n```",
			want:  DedupPlan{Clusters: []DedupCluster{{Keep: "a", Drop: []string{"b", "c"}, Reason: "nil map", Note: "also on reload"}}},
		},
		{
			name:  "a cluster may span files",
			reply: `{"clusters":[{"keep":"a","drop":["d"]}]}`,
			want:  DedupPlan{Clusters: []DedupCluster{{Keep: "a", Drop: []string{"d"}}}},
		},
		{
			name:  "github keeper drops agent copies",
			reply: `{"clusters":[{"keep":"g","drop":["a"]}]}`,
			want:  DedupPlan{Clusters: []DedupCluster{{Keep: "g", Drop: []string{"a"}}}},
		},
		{
			name:  "github comments are never dropped",
			reply: `{"clusters":[{"keep":"a","drop":["g","b"]}]}`,
			want:  DedupPlan{Clusters: []DedupCluster{{Keep: "a", Drop: []string{"b"}}}},
		},
		{
			name:  "unknown keeper ignores its group",
			reply: `{"clusters":[{"keep":"zz","drop":["a"]}]}`,
			want:  DedupPlan{},
		},
		{
			name:  "unknown and self drops are ignored, and a group left empty is left out",
			reply: `{"clusters":[{"keep":"a","drop":["zz","a"]},{"keep":"d","drop":["e"]}]}`,
			want:  DedupPlan{Clusters: []DedupCluster{{Keep: "d", Drop: []string{"e"}}}},
		},
		{
			name:  "a keeper is never dropped, so chains can't delete every copy",
			reply: `{"clusters":[{"keep":"a","drop":["b"]},{"keep":"b","drop":["a","c"]}]}`,
			want:  DedupPlan{Clusters: []DedupCluster{{Keep: "b", Drop: []string{"c"}}}},
		},
		{
			name:  "a drop named twice goes to its first group",
			reply: `{"clusters":[{"keep":"a","drop":["c","c"]},{"keep":"b","drop":["c","d"]}]}`,
			want:  DedupPlan{Clusters: []DedupCluster{{Keep: "a", Drop: []string{"c"}}, {Keep: "b", Drop: []string{"d"}}}},
		},
		{
			name:  "related groups map drops to keepers and need two known ids",
			reply: `{"clusters":[{"keep":"a","drop":["b"]}],"related":[{"ids":["b","d","zz","d"],"reason":" same read path "},{"ids":["a","b"]},{"ids":["zz","e"]}]}`,
			want: DedupPlan{
				Clusters: []DedupCluster{{Keep: "a", Drop: []string{"b"}}},
				Related:  []DedupRelated{{IDs: []string{"a", "d"}, Reason: "same read path"}},
			},
		},
		{
			name: "moves only for kept agent comments, within reach, first one wins",
			reply: `{"clusters":[{"keep":"a","drop":["b"]}],"moves":[` +
				`{"id":"a","line":11},{"id":"a","line":12},` +
				`{"id":"b","line":15},{"id":"g","line":12},{"id":"zz","line":3},` +
				`{"id":"c","line":40},{"id":"c","line":0},{"id":"c","line":61},{"id":"c","line":19},{"id":"c","line":20},` +
				`{"id":"d","line":2}]}`,
			want: DedupPlan{
				Clusters: []DedupCluster{{Keep: "a", Drop: []string{"b"}}},
				Moves:    []DedupMove{{ID: "a", Line: 11}, {ID: "c", Line: 20}, {ID: "d", Line: 2}},
			},
		},
		{name: "no object", reply: "nothing to dedup", wantErr: "no JSON object"},
		{name: "brace order reversed", reply: "} then {", wantErr: "no JSON object"},
		{name: "bad json", reply: `{"clusters": [}`, wantErr: "parsing dedup reply"},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			got, err := ParseDedupReply(tc.reply, cands)
			if tc.wantErr != "" {
				require.ErrorContains(s.T(), err, tc.wantErr)
				return
			}
			require.NoError(s.T(), err)
			require.Equal(s.T(), tc.want, got)
		})
	}
}

// A bundled comment covers two issues, one of which a single-issue comment
// reports too. The reply can fold the single one into the bundle, which is a
// plain cluster, or keep the single one and trim the bundle.
func (s *DedupSuite) TestParseDedupReplyBundledKeeper() {
	const (
		bundled = "The expiry check copies the cache's age-vs-limit logic and calls time.Now directly instead of an injected clock."
		single  = "isExpired calls time.Now directly, so the expiry boundary can't be tested deterministically."
		trimmed = "The expiry check copies the cache's age-vs-limit logic."
	)
	cands := []*Comment{
		{ID: "bundle", Path: "x.go", Line: 10, Source: "agent", Body: bundled},
		{ID: "single", Path: "x.go", Line: 10, Source: "agent", Body: single},
		{ID: "other", Path: "x.go", Line: 30, Source: "agent", Body: bundled},
		{ID: "gh", Path: "x.go", Line: 12, Source: "github", Body: single},
	}
	cases := []struct {
		name  string
		reply string
		want  DedupPlan
	}{
		{
			name:  "the single-issue comment drops into the bundled keeper",
			reply: `{"clusters":[{"keep":"bundle","drop":["single"],"reason":"time.Now instead of a clock"}]}`,
			want:  DedupPlan{Clusters: []DedupCluster{{Keep: "bundle", Drop: []string{"single"}, Reason: "time.Now instead of a clock"}}},
		},
		{
			name:  "the bundle is trimmed to its unique part, body and reason trimmed",
			reply: `{"trims":[{"id":"bundle","covered_by":"single","body":"  ` + trimmed + ` ","reason":" clock covered "}]}`,
			want:  DedupPlan{Trims: []DedupTrim{{ID: "bundle", CoveredBy: "single", Body: trimmed, Reason: "clock covered"}}},
		},
		{
			name:  "a github comment can cover a trim",
			reply: `{"trims":[{"id":"bundle","covered_by":"gh","body":"` + trimmed + `"}]}`,
			want:  DedupPlan{Trims: []DedupTrim{{ID: "bundle", CoveredBy: "gh", Body: trimmed}}},
		},
		{
			name:  "a dropped cover stands for its keeper",
			reply: `{"clusters":[{"keep":"gh","drop":["single"]}],"trims":[{"id":"bundle","covered_by":"single","body":"` + trimmed + `"}]}`,
			want: DedupPlan{
				Clusters: []DedupCluster{{Keep: "gh", Drop: []string{"single"}}},
				Trims:    []DedupTrim{{ID: "bundle", CoveredBy: "gh", Body: trimmed}},
			},
		},
		{
			name: "rejected trims",
			reply: `{"clusters":[{"keep":"single","drop":["other"]}],"trims":[` +
				`{"id":"zz","covered_by":"single","body":"x"},` + // unknown
				`{"id":"gh","covered_by":"single","body":"x"},` + // github comments aren't ours
				`{"id":"other","covered_by":"single","body":"x"},` + // already dropped
				`{"id":"bundle","covered_by":"zz","body":"x"},` + // unknown cover
				`{"id":"bundle","covered_by":"bundle","body":"x"},` + // covers itself
				`{"id":"bundle","covered_by":"single","body":"  "},` + // trimmed to nothing
				`{"id":"bundle","covered_by":"single","body":"` + bundled + ` And more."}]}`, // not a trim
			want: DedupPlan{Clusters: []DedupCluster{{Keep: "single", Drop: []string{"other"}}}},
		},
		{
			name:  "a cover dropped into the trimmed comment is the comment itself",
			reply: `{"clusters":[{"keep":"bundle","drop":["single"]}],"trims":[{"id":"bundle","covered_by":"single","body":"` + trimmed + `"}]}`,
			want:  DedupPlan{Clusters: []DedupCluster{{Keep: "bundle", Drop: []string{"single"}}}},
		},
		{
			name: "two bundles can't cut the issue they share from each other",
			reply: `{"trims":[{"id":"bundle","covered_by":"other","body":"` + trimmed + `"},` +
				`{"id":"other","covered_by":"bundle","body":"` + trimmed + `"},` +
				`{"id":"bundle","covered_by":"gh","body":"` + trimmed + `"}]}`,
			want: DedupPlan{Trims: []DedupTrim{{ID: "bundle", CoveredBy: "other", Body: trimmed}}},
		},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			got, err := ParseDedupReply(tc.reply, cands)
			require.NoError(s.T(), err)
			require.Equal(s.T(), tc.want, got)
		})
	}
}

// The cause and its symptom: the cause's fix also ends the symptom, though
// the symptom has a narrower fix of its own. The prompt calls that a
// duplicate, and the reply drops the symptom into the cause.
func (s *DedupSuite) TestParseDedupReplyRootCauseAbsorbsSymptom() {
	cands := []*Comment{
		{ID: "cause", Path: "store.go", Line: 92, Source: "agent", Body: "Staleness is measured from the source file's modification time, so a valid file that simply isn't rewritten goes stale."},
		{ID: "symptom", Path: "sync.go", Line: 627, Source: "agent", Body: "An identical re-upload skips the download, so the modification time is never refreshed."},
	}
	require.Contains(s.T(), BuildDedupPrompt(cands), "even when a narrower fix for it alone exists")
	got, err := ParseDedupReply(`{"clusters":[{"keep":"cause","drop":["symptom"],"reason":"staleness keyed on file mtime"}],"related":[{"ids":["cause","symptom"]}]}`, cands)
	require.NoError(s.T(), err)
	require.Equal(s.T(), []DedupCluster{{Keep: "cause", Drop: []string{"symptom"}, Reason: "staleness keyed on file mtime"}}, got.Clusters)
	// Once the symptom is folded in, the related group is the keeper alone.
	require.Empty(s.T(), got.Related)
}

func (s *DedupSuite) TestWithDedupNote() {
	require.Equal(s.T(), "Nil map write.\n\nAlso flagged: panics on reload too.", WithDedupNote("Nil map write.", "panics on reload too."))
}
