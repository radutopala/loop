package db

import (
	"context"
	"path/filepath"

	"github.com/stretchr/testify/require"
)

// TestBackupInto snapshots a file database, WAL included, into a new file
// that opens as a database with the same rows.
func (s *IntegrationSuite) TestBackupInto() {
	dir := s.T().TempDir()
	store, err := NewSQLiteStore(filepath.Join(dir, "loop.db"))
	require.NoError(s.T(), err)
	defer store.Close()
	ctx := context.Background()
	_, err = store.InsertConfigRevision(ctx, &ConfigRevision{Path: "/r/config.json", Content: "{}", Hash: "h", Source: ConfigSourceExternal}, 10)
	require.NoError(s.T(), err)

	dest := filepath.Join(dir, "copy.db")
	require.NoError(s.T(), store.BackupInto(ctx, dest))
	// The destination must not exist.
	require.Error(s.T(), store.BackupInto(ctx, dest))

	copied, err := NewSQLiteStore(dest)
	require.NoError(s.T(), err)
	defer copied.Close()
	revs, err := copied.ListConfigRevisions(ctx, "/r/config.json")
	require.NoError(s.T(), err)
	require.Len(s.T(), revs, 1)
	require.Equal(s.T(), "{}", revs[0].Content)
}
