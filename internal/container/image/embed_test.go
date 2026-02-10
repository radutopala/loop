package image

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

func (s *EmbedSuite) TestDockerfileNotEmpty() {
	require.NotEmpty(s.T(), Dockerfile)
	require.Contains(s.T(), string(Dockerfile), "FROM golang:")
	require.Contains(s.T(), string(Dockerfile), "ENTRYPOINT")
	require.Contains(s.T(), string(Dockerfile), "go install")
}

func (s *EmbedSuite) TestEntrypointNotEmpty() {
	require.NotEmpty(s.T(), Entrypoint)
	require.Contains(s.T(), string(Entrypoint), "#!/bin/sh")
	require.Contains(s.T(), string(Entrypoint), `su-exec "$AGENT_USER" "$@"`)
}

func (s *EmbedSuite) TestSetupNotEmpty() {
	require.NotEmpty(s.T(), Setup)
	require.Contains(s.T(), string(Setup), "#!/bin/sh")
}
