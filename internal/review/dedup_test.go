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
		{
			name:     "one comment per file",
			comments: []*Comment{agent("a", "x.go", 1), agent("b", "y.go", 1)},
			want:     []string{},
		},
		{
			name:     "only github comments in the file",
			comments: []*Comment{gh("g1", "x.go", 1), gh("g2", "x.go", 5)},
			want:     []string{},
		},
		{
			name: "files with an agent comment and a second one, by file then line",
			comments: []*Comment{
				agent("b", "y.go", 9), nil, agent("a", "y.go", 3),
				gh("g", "x.go", 40), agent("c", "x.go", 2),
				agent("lone", "z.go", 1), {ID: "nopath", Line: 1}, {ID: "nopath2", Line: 2},
			},
			want: []string{"c", "g", "a", "b"},
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
	require.Contains(s.T(), got, `{"clusters":[{"keep":"<id>","drop":["<id>"]}]}`)
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

func (s *DedupSuite) TestDedupDrops() {
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
		want    []string
		wantErr string
	}{
		{name: "no clusters", reply: `{"clusters":[]}`, want: nil},
		{
			name:  "prose and fences around the object",
			reply: "Here you go:\n```json\n{\"clusters\":[{\"keep\":\"a\",\"drop\":[\"b\",\"c\"]}]}\n```",
			want:  []string{"b", "c"},
		},
		{
			name:  "github keeper drops agent copies",
			reply: `{"clusters":[{"keep":"g","drop":["a"]}]}`,
			want:  []string{"a"},
		},
		{
			name:  "github comments are never dropped",
			reply: `{"clusters":[{"keep":"a","drop":["g","b"]}]}`,
			want:  []string{"b"},
		},
		{
			name:  "unknown keeper ignores its group",
			reply: `{"clusters":[{"keep":"zz","drop":["a"]}]}`,
			want:  nil,
		},
		{
			name:  "unknown, self and cross-file drops are ignored",
			reply: `{"clusters":[{"keep":"a","drop":["zz","a","d"]},{"keep":"d","drop":["e"]}]}`,
			want:  []string{"e"},
		},
		{
			name:  "a keeper is never dropped, so chains can't delete every copy",
			reply: `{"clusters":[{"keep":"a","drop":["b"]},{"keep":"b","drop":["a","c"]}]}`,
			want:  []string{"c"},
		},
		{
			name:  "a drop named twice is returned once",
			reply: `{"clusters":[{"keep":"a","drop":["c","c"]},{"keep":"b","drop":["c"]}]}`,
			want:  []string{"c"},
		},
		{name: "no object", reply: "nothing to dedup", wantErr: "no JSON object"},
		{name: "brace order reversed", reply: "} then {", wantErr: "no JSON object"},
		{name: "bad json", reply: `{"clusters": [}`, wantErr: "parsing dedup reply"},
	}
	for _, tc := range cases {
		s.Run(tc.name, func() {
			got, err := DedupDrops(tc.reply, cands)
			if tc.wantErr != "" {
				require.ErrorContains(s.T(), err, tc.wantErr)
				return
			}
			require.NoError(s.T(), err)
			require.Equal(s.T(), tc.want, got)
		})
	}
}
