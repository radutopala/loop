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

func (s *ParserSuite) TestParseCommentsEmpty() {
	require.Nil(s.T(), ParseComments(""))
	require.Nil(s.T(), ParseComments("just some text, no tags"))
}

func (s *ParserSuite) TestParseSingleComment() {
	text := `Here is my note:
<review-comment path="foo/bar.go" line="42">
This loop allocates each iteration — hoist the slice.
</review-comment>`
	got := ParseComments(text)
	require.Len(s.T(), got, 1)
	require.Equal(s.T(), "foo/bar.go", got[0].Path)
	require.Equal(s.T(), 42, got[0].Line)
	require.Equal(s.T(), "RIGHT", got[0].Side)
	require.Equal(s.T(), "This loop allocates each iteration — hoist the slice.", got[0].Body)
	require.NotEmpty(s.T(), got[0].ID)
}

func (s *ParserSuite) TestParseMultipleComments() {
	text := `<review-comment path="a.go" line="1">A</review-comment>
also: <review-comment path="b.go" line="2" side="LEFT">B</review-comment>`
	got := ParseComments(text)
	require.Len(s.T(), got, 2)
	require.Equal(s.T(), "a.go", got[0].Path)
	require.Equal(s.T(), "RIGHT", got[0].Side)
	require.Equal(s.T(), "b.go", got[1].Path)
	require.Equal(s.T(), "LEFT", got[1].Side)
}

func (s *ParserSuite) TestParseSkipsMissingAttributes() {
	tests := []struct {
		name string
		text string
	}{
		{"no path", `<review-comment line="1">body</review-comment>`},
		{"no line", `<review-comment path="x">body</review-comment>`},
		{"empty body", `<review-comment path="x" line="1"></review-comment>`},
		{"non-numeric line", `<review-comment path="x" line="abc">body</review-comment>`},
		{"zero line", `<review-comment path="x" line="0">body</review-comment>`},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			require.Empty(s.T(), ParseComments(tt.text))
		})
	}
}

func (s *ParserSuite) TestParseNormalisesSide() {
	text := `<review-comment path="a" line="1" side="right">x</review-comment>
<review-comment path="b" line="2" side="left">y</review-comment>
<review-comment path="c" line="3" side="bogus">z</review-comment>`
	got := ParseComments(text)
	require.Len(s.T(), got, 3)
	require.Equal(s.T(), "RIGHT", got[0].Side)
	require.Equal(s.T(), "LEFT", got[1].Side)
	require.Equal(s.T(), "RIGHT", got[2].Side) // bogus → default RIGHT
}

func (s *ParserSuite) TestParseStableIDs() {
	a := ParseComments(`<review-comment path="x" line="1">same</review-comment>`)
	b := ParseComments(`<review-comment path="x" line="1">same</review-comment>`)
	require.Len(s.T(), a, 1)
	require.Len(s.T(), b, 1)
	require.Equal(s.T(), a[0].ID, b[0].ID)

	c := ParseComments(`<review-comment path="x" line="2">same</review-comment>`)
	require.NotEqual(s.T(), a[0].ID, c[0].ID)
}
