//go:build unix

package buildctx

import (
	"path/filepath"
	"syscall"

	"github.com/stretchr/testify/require"
)

func (s *BuildCtxSuite) TestFIFORefused() {
	s.write("Dockerfile", "FROM x\n", 0o644)
	require.NoError(s.T(), syscall.Mkfifo(filepath.Join(s.dir, "pipe"), 0o644))
	_, err := new(Cache).Digest(s.root())
	require.ErrorIs(s.T(), err, errNotRegular)
}
