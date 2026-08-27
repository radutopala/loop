package fsmigrate

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	containerimage "github.com/radutopala/loop/internal/container/image"
)

// versionedContainerFilesDigest pins the contents of every file in
// versionedContainerFiles as of the most recent refreshContainerFiles
// migration. Editing an embedded container asset without appending a refresh
// migration leaves every existing install on the old copy — that is exactly
// how the golang:1.26 Dockerfile survived the Go 1.27 upgrade. Update this
// constant in the same commit as the new migration entry.
const versionedContainerFilesDigest = "fa9ecb53e7e40eaaa3f5ddb2d43e0c38ebd8e71feaa8ac104e109e1154666c7a"

type ContainerFilesGuardSuite struct {
	suite.Suite
}

func TestContainerFilesGuardSuite(t *testing.T) {
	suite.Run(t, new(ContainerFilesGuardSuite))
}

func (s *ContainerFilesGuardSuite) TestEmbeddedFilesMatchPinnedDigest() {
	h := sha256.New()
	for _, name := range versionedContainerFiles {
		h.Write([]byte(name))
		h.Write([]byte{0})
		h.Write(containerimage.MustRead(name))
	}
	require.Equal(s.T(), versionedContainerFilesDigest, hex.EncodeToString(h.Sum(nil)),
		"embedded container/ files changed: append a refreshContainerFiles entry to "+
			"migrations so existing ~/.loop installs pick the change up, then update "+
			"versionedContainerFilesDigest")
}
