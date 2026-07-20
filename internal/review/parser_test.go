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
