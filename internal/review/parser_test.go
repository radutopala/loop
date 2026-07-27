package review

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type ParserSuite struct {
	suite.Suite
}

func TestParserSuite(t *testing.T) {
	suite.Run(t, new(ParserSuite))
}

func (s *ParserSuite) TestNewCommentBuildsNormalizedComment() {
	c := NewComment(" foo/bar.go ", 42, "right", " This loop allocates each iteration. ")
	require.NotNil(s.T(), c)
	require.Equal(s.T(), "foo/bar.go", c.Path)
	require.Equal(s.T(), 42, c.Line)
	require.Equal(s.T(), "RIGHT", c.Side)
	require.Equal(s.T(), "This loop allocates each iteration.", c.Body)
	require.Len(s.T(), c.ID, 12)
}

func (s *ParserSuite) TestNewCommentLeftSide() {
	c := NewComment("a.go", 3, "left", "removed line bug")
	require.NotNil(s.T(), c)
	require.Equal(s.T(), "LEFT", c.Side)
}

func (s *ParserSuite) TestNewCommentUnknownSideDefaultsRight() {
	c := NewComment("a.go", 3, "bogus", "body")
	require.NotNil(s.T(), c)
	require.Equal(s.T(), "RIGHT", c.Side)
}

func (s *ParserSuite) TestNewCommentRejectsInvalid() {
	require.Nil(s.T(), NewComment("", 3, "", "body"), "empty path")
	require.Nil(s.T(), NewComment("a.go", 0, "", "body"), "zero line")
	require.Nil(s.T(), NewComment("a.go", -1, "", "body"), "negative line")
	require.Nil(s.T(), NewComment("a.go", 3, "", "  "), "blank body")
}

func (s *ParserSuite) TestNewCommentStableID() {
	a := NewComment("a.go", 3, "RIGHT", "body")
	b := NewComment("a.go", 3, "LEFT", "body")
	c := NewComment("a.go", 4, "RIGHT", "body")
	// Side is not part of the identity triple; path/line/body are — so a
	// re-report of the same finding dedups even if the side flips.
	require.Equal(s.T(), a.ID, b.ID)
	require.NotEqual(s.T(), a.ID, c.ID)
}

func (s *ParserSuite) TestParseReportFindingsBody() {
	tests := []struct {
		name    string
		finding string
		want    string
	}{
		{
			name:    "summary and scenario",
			finding: `{"file":"a.go","line":1,"summary":"leak","failure_scenario":"fd stays open"}`,
			want:    "leak\n\nfd stays open",
		},
		{
			name:    "summary only",
			finding: `{"file":"a.go","line":1,"summary":"leak"}`,
			want:    "leak",
		},
		{
			name:    "scenario only",
			finding: `{"file":"a.go","line":1,"failure_scenario":"fd stays open"}`,
			want:    "fd stays open",
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			got := ParseReportFindings(`{"findings":[` + tc.finding + `]}`)
			require.Len(s.T(), got, 1)
			require.Equal(s.T(), tc.want, got[0].Body)
			// ReportFindings carries no side; findings are against head.
			require.Equal(s.T(), "RIGHT", got[0].Side)
		})
	}
}

// A verbatim ReportFindings payload captured off a real review run. The
// first implementation parsed hand-written fixtures fine but ingested
// nothing in production, because the stream handed it a summarized (empty)
// input rather than this JSON — so pin the real shape, extra keys included.
func (s *ParserSuite) TestParseReportFindingsRealPayload() {
	const raw = `{"findings":[{"file":"internal/idfree/memory_store.go","line":48,"summary":"File-monitor/reload machinery duplicated verbatim across store packages","short_summary":"Duplicated file-monitor/reload machinery","failure_scenario":"MemoryStore.loadFile/checkAndReload/Monitor plus the pollInterval/openFile/lastMod fields are a line-for-line copy of internal/offer and internal/publisher.","category":"reuse"},{"file":"internal/idfree/memory_store.go","line":180,"summary":"parseModels accepts a JSON top-level null, silently yielding an empty store","short_summary":"Top-level JSON null yields empty store silently","failure_scenario":"If the file content is the literal ` + "`null`" + `, json.Unmarshal succeeds leaving parsed nil with no error.","category":"correctness"}]}`

	got := ParseReportFindings(raw)
	require.Len(s.T(), got, 2)
	require.Equal(s.T(), "internal/idfree/memory_store.go", got[0].Path)
	require.Equal(s.T(), 48, got[0].Line)
	require.Contains(s.T(), got[0].Body, "duplicated verbatim across store packages")
	require.Contains(s.T(), got[0].Body, "line-for-line copy")
	require.Equal(s.T(), 180, got[1].Line)
	require.NotEqual(s.T(), got[0].ID, got[1].ID)
}

func (s *ParserSuite) TestParseReportFindingsRejects() {
	require.Nil(s.T(), ParseReportFindings("{"), "malformed json")
	require.Empty(s.T(), ParseReportFindings(`{"findings":[]}`), "no findings")
	require.Empty(s.T(), ParseReportFindings(`{"findings":[{"file":"a.go","line":1}]}`), "no text")
	require.Empty(s.T(), ParseReportFindings(`{"findings":[{"line":1,"summary":"x"}]}`), "no file")
}
