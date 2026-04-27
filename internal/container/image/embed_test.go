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

func (s *EmbedSuite) TestEntrypointGateBranch() {
	body := string(Entrypoint)
	// Gate branch flips on LOOP_GATE_ENABLED (per-container env set by
	// runner) and exec's the `loop syscallwrap` subcommand directly as
	// root — the parent stays privileged and drops the child to
	// $AGENT_USER via SysProcAttr.Credential so the agent can't signal
	// the notify-fd holder.
	require.Contains(s.T(), body, `"$LOOP_GATE_ENABLED" = "1"`)
	require.Contains(s.T(), body, `exec /usr/local/bin/loop syscallwrap -- "$@"`)
}

func (s *EmbedSuite) TestEntrypointDockerProxyBranch() {
	body := string(Entrypoint)
	// In-container docker proxy flips on LOOP_DOCKERPROXY_ENABLED and
	// requires the real daemon socket bind-mounted at .sock.host. The
	// proxy itself listens at /var/run/docker.sock (tmpfs inside the
	// container) and is invoked as the `loop dockerproxy` subcommand.
	require.Contains(s.T(), body, `"$LOOP_DOCKERPROXY_ENABLED" = "1"`)
	require.Contains(s.T(), body, `/var/run/docker.sock.host`)
	require.Contains(s.T(), body, `/usr/local/bin/loop dockerproxy`)
}

func (s *EmbedSuite) TestSetupNotEmpty() {
	require.NotEmpty(s.T(), Setup)
	require.Contains(s.T(), string(Setup), "#!/bin/sh")
}

func (s *EmbedSuite) TestMustReadReturnsEmbeddedFile() {
	data := MustRead("Dockerfile")
	require.Equal(s.T(), Dockerfile, data)
}

func (s *EmbedSuite) TestMustReadPanicsOnMissing() {
	require.PanicsWithValue(
		s.T(),
		`containerimage: missing embedded file "missing.txt": open missing.txt: file does not exist`,
		func() { MustRead("missing.txt") },
	)
}
