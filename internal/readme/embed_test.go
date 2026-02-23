package readme

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type EmbedSuite struct {
	suite.Suite
}

func TestEmbedSuite(t *testing.T) {
	suite.Run(t, new(EmbedSuite))
}

func (s *EmbedSuite) TestContentNotEmpty() {
	require.NotEmpty(s.T(), Content)
}

func (s *EmbedSuite) TestContentIsMarkdown() {
	require.Contains(s.T(), Content, "# ")
}
