package db

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

// TestConfigRevisions records config revisions against a real SQLite: the
// first one is initial, an unchanged hash stores nothing, each path keeps
// only its newest revisions, and lookups by path and id.
func (s *IntegrationSuite) TestConfigRevisions() {
	store, err := NewSQLiteStore(filepath.Join(s.T().TempDir(), "loop.db"))
	require.NoError(s.T(), err)
	defer store.Close()
	ctx := context.Background()

	insert := func(path, content, source string) bool {
		ok, err := store.InsertConfigRevision(ctx, &ConfigRevision{Path: path, Content: content, Hash: "h-" + content, Source: source}, 3)
		require.NoError(s.T(), err)
		return ok
	}
	require.True(s.T(), insert("/a", "1", ConfigSourceExternal))
	require.False(s.T(), insert("/a", "1", "settings"), "same hash as the newest")
	require.True(s.T(), insert("/a", "2", "settings"))
	require.True(s.T(), insert("/a", "3", ConfigSourceExternal))
	require.True(s.T(), insert("/a", "1", "restore:1"), "a hash older than the newest")
	require.True(s.T(), insert("/b", "x", "settings"), "a first revision Loop wrote keeps its source")

	revs, err := store.ListConfigRevisions(ctx, "/a")
	require.NoError(s.T(), err)
	require.Len(s.T(), revs, 3, "pruned to keep")
	var got []string
	for _, r := range revs {
		got = append(got, r.Content+":"+r.Source)
	}
	require.Equal(s.T(), []string{"1:restore:1", "3:external", "2:settings"}, got)

	b, err := store.ListConfigRevisions(ctx, "/b")
	require.NoError(s.T(), err)
	require.Len(s.T(), b, 1)
	require.Equal(s.T(), "settings", b[0].Source)

	first, err := store.ListConfigRevisions(ctx, "/c")
	require.NoError(s.T(), err)
	require.Empty(s.T(), first)
	require.True(s.T(), insert("/c", "y", ConfigSourceExternal))
	c, err := store.ListConfigRevisions(ctx, "/c")
	require.NoError(s.T(), err)
	require.Equal(s.T(), ConfigSourceInitial, c[0].Source, "the content Loop first saw")

	rev, err := store.GetConfigRevision(ctx, revs[1].ID)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "/a", rev.Path)
	require.Equal(s.T(), "3", rev.Content)
	require.Equal(s.T(), "h-3", rev.Hash)
	require.False(s.T(), rev.CreatedAt.IsZero())
	missing, err := store.GetConfigRevision(ctx, 9999)
	require.NoError(s.T(), err)
	require.Nil(s.T(), missing)
}

func (s *StoreSuite) TestConfigRevisionStoreErrors() {
	ctx := context.Background()
	boom := errors.New("boom")
	cols := []string{"id", "path", "content", "hash", "source", "created_at"}
	rev := &ConfigRevision{Path: "/a", Hash: "h"}

	s.Run("insert", func() {
		s.mock.ExpectExec(`INSERT INTO config_revisions`).WillReturnError(boom)
		_, err := s.store.InsertConfigRevision(ctx, rev, 3)
		require.ErrorIs(s.T(), err, boom)
	})
	s.Run("insert rows affected", func() {
		s.mock.ExpectExec(`INSERT INTO config_revisions`).WillReturnResult(sqlmock.NewErrorResult(boom))
		_, err := s.store.InsertConfigRevision(ctx, rev, 3)
		require.ErrorIs(s.T(), err, boom)
	})
	s.Run("prune", func() {
		s.mock.ExpectExec(`INSERT INTO config_revisions`).WillReturnResult(sqlmock.NewResult(1, 1))
		s.mock.ExpectExec(`DELETE FROM config_revisions`).WillReturnError(boom)
		ok, err := s.store.InsertConfigRevision(ctx, rev, 3)
		require.ErrorIs(s.T(), err, boom)
		require.True(s.T(), ok, "stored even when the prune fails")
	})
	s.Run("list query", func() {
		s.mock.ExpectQuery(`FROM config_revisions WHERE path = \?`).WillReturnError(boom)
		_, err := s.store.ListConfigRevisions(ctx, "/a")
		require.ErrorIs(s.T(), err, boom)
	})
	s.Run("list scan", func() {
		s.mock.ExpectQuery(`FROM config_revisions WHERE path = \?`).
			WillReturnRows(sqlmock.NewRows(cols).AddRow("x", "", "", "", "", time.Now()))
		_, err := s.store.ListConfigRevisions(ctx, "/a")
		require.Error(s.T(), err)
	})
	s.Run("get", func() {
		s.mock.ExpectQuery(`FROM config_revisions WHERE id = \?`).WillReturnError(boom)
		_, err := s.store.GetConfigRevision(ctx, 1)
		require.ErrorIs(s.T(), err, boom)
	})
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}
