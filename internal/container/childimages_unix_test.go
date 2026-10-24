//go:build unix

package container

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

// A context that can't be copied, like one holding a FIFO, leaves no copy
// behind.
func TestSnapshotContextCopyFails(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, syscall.Mkfifo(filepath.Join(dir, "fifo"), 0o644))
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	_, _, _, err := snapshotContext(dir, dir)
	require.Error(t, err)
	left, err := os.ReadDir(tmp)
	require.NoError(t, err)
	require.Empty(t, left)
}
