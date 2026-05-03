package quality

import (
	"os"
	"testing"

	"github.com/odvcencio/gotreesitter/grammars"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type GrammarsSuite struct {
	suite.Suite
}

func TestGrammarsSuite(t *testing.T) {
	suite.Run(t, new(GrammarsSuite))
}

func (s *GrammarsSuite) TestActiveGrammarSetEnvVar() {
	require.Equal(s.T(), activeGrammarSet, os.Getenv("GOTREESITTER_GRAMMAR_SET"))
}

func (s *GrammarsSuite) TestEmbeddedLanguageCacheLimitSet() {
	_, limit := grammars.EmbeddedLanguageCacheStats()
	require.Equal(s.T(), embeddedLanguageCacheLimit, limit)
}
