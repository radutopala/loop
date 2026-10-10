package db

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

// TestAPITokensLifecycle stores, lists and deletes tokens against a real
// SQLite.
func (s *IntegrationSuite) TestAPITokensLifecycle() {
	store, err := NewSQLiteStore(filepath.Join(s.T().TempDir(), "loop.db"))
	require.NoError(s.T(), err)
	defer store.Close()
	ctx := context.Background()

	require.NoError(s.T(), store.InsertAPIToken(ctx, &APIToken{Hash: "h1", Kind: "agent", ContainerID: "c1", ChannelID: "ch1", DirPath: "/work"}))
	require.NoError(s.T(), store.InsertAPIToken(ctx, &APIToken{Hash: "h2", Kind: "plugin", PluginID: "cogtest", ContainerID: "c2", ChannelID: "ch2"}))
	require.Error(s.T(), store.InsertAPIToken(ctx, &APIToken{Hash: "h1", ContainerID: "c3", ChannelID: "ch3"}), "hash is unique")

	got, err := store.ListAPITokens(ctx)
	require.NoError(s.T(), err)
	require.Len(s.T(), got, 2)
	require.Equal(s.T(), "h1", got[0].Hash)
	require.Equal(s.T(), "agent", got[0].Kind)
	require.Empty(s.T(), got[0].PluginID)
	require.Equal(s.T(), "plugin", got[1].Kind)
	require.Equal(s.T(), "cogtest", got[1].PluginID)
	require.Equal(s.T(), "c1", got[0].ContainerID)
	require.Equal(s.T(), "ch1", got[0].ChannelID)
	require.Equal(s.T(), "/work", got[0].DirPath)
	require.False(s.T(), got[0].CreatedAt.IsZero())

	require.NoError(s.T(), store.DeleteAPITokens(ctx, "c1"))
	got, err = store.ListAPITokens(ctx)
	require.NoError(s.T(), err)
	require.Len(s.T(), got, 1)
	require.Equal(s.T(), "c2", got[0].ContainerID)

	// A row stored before tokens had a kind reads as an agent's.
	_, err = store.db.ExecContext(ctx, `INSERT INTO api_tokens (token_hash, container_id, channel_id, created_at) VALUES ('h3', 'c3', 'ch3', ?)`, time.Now())
	require.NoError(s.T(), err)
	got, err = store.ListAPITokens(ctx)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "agent", got[1].Kind)
}

func (s *StoreSuite) TestAPITokensErrors() {
	ctx := context.Background()
	boom := errors.New("boom")
	cols := []string{"token_hash", "kind", "plugin_id", "container_id", "channel_id", "dir_path", "created_at"}

	s.Run("list query", func() {
		s.mock.ExpectQuery(`FROM api_tokens`).WillReturnError(boom)
		_, err := s.store.ListAPITokens(ctx)
		require.ErrorIs(s.T(), err, boom)
	})
	s.Run("list scan", func() {
		s.mock.ExpectQuery(`FROM api_tokens`).
			WillReturnRows(sqlmock.NewRows(cols).AddRow("h", "agent", "", "c", "ch", "", "not a time"))
		_, err := s.store.ListAPITokens(ctx)
		require.Error(s.T(), err)
	})
	s.Run("list rows error", func() {
		s.mock.ExpectQuery(`FROM api_tokens`).
			WillReturnRows(sqlmock.NewRows(cols).AddRow("h", "agent", "", "c", "ch", "", time.Now()).RowError(0, boom))
		_, err := s.store.ListAPITokens(ctx)
		require.ErrorIs(s.T(), err, boom)
	})
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}
