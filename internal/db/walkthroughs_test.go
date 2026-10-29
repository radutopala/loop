package db

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

// TestWalkthroughLifecycle stores, reads, lists and deletes walkthroughs
// against a real SQLite, and checks they go with their channel.
func (s *IntegrationSuite) TestWalkthroughLifecycle() {
	store, err := NewSQLiteStore(filepath.Join(s.T().TempDir(), "loop.db"))
	require.NoError(s.T(), err)
	defer store.Close()
	ctx := context.Background()
	seedChannel(s.T(), store, "c1")

	steps := []WalkthroughStep{
		{Path: "main.go", StartLine: 1, EndLine: 3, Anchor: "package main\n\nfunc main() {", Title: "Entry", Body: "Starts here."},
		{Path: "lib.go", StartLine: 7, EndLine: 7, Anchor: "x := 1", Body: "Sets x."},
	}
	first, err := store.CreateWalkthrough(ctx, &Walkthrough{ChannelID: "c1", Title: "Tour", Steps: steps})
	require.NoError(s.T(), err)
	require.NotZero(s.T(), first.ID)
	require.Equal(s.T(), 2, first.StepCount)
	second, err := store.CreateWalkthrough(ctx, &Walkthrough{ChannelID: "c1", Title: "Second", Steps: steps[:1]})
	require.NoError(s.T(), err)

	got, err := store.GetWalkthrough(ctx, first.ID)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "c1", got.ChannelID)
	require.Equal(s.T(), "Tour", got.Title)
	require.Equal(s.T(), steps, got.Steps)
	require.Equal(s.T(), 2, got.StepCount)
	missing, err := store.GetWalkthrough(ctx, 999)
	require.NoError(s.T(), err)
	require.Nil(s.T(), missing)

	list, err := store.ListWalkthroughs(ctx, "c1")
	require.NoError(s.T(), err)
	require.Len(s.T(), list, 2)
	require.Equal(s.T(), second.ID, list[0].ID, "newest first")
	require.Equal(s.T(), 1, list[0].StepCount)
	require.Equal(s.T(), 2, list[1].StepCount)
	require.Nil(s.T(), list[1].Steps, "a listing leaves the steps out")

	ok, err := store.DeleteWalkthrough(ctx, "c2", first.ID)
	require.NoError(s.T(), err)
	require.False(s.T(), ok, "another channel's")
	ok, err = store.DeleteWalkthrough(ctx, "c1", first.ID)
	require.NoError(s.T(), err)
	require.True(s.T(), ok)
	ok, err = store.DeleteWalkthrough(ctx, "c1", first.ID)
	require.NoError(s.T(), err)
	require.False(s.T(), ok, "already gone")

	require.NoError(s.T(), store.DeleteChannel(ctx, "c1"))
	list, err = store.ListWalkthroughs(ctx, "c1")
	require.NoError(s.T(), err)
	require.Empty(s.T(), list, "deleted with the channel")
}

func (s *StoreSuite) TestWalkthroughStoreErrors() {
	ctx := context.Background()
	boom := errors.New("boom")
	cols := []string{"id", "channel_id", "title", "steps", "created_at"}

	s.Run("create", func() {
		s.mock.ExpectExec(`INSERT INTO walkthroughs`).WillReturnError(boom)
		_, err := s.store.CreateWalkthrough(ctx, &Walkthrough{})
		require.ErrorIs(s.T(), err, boom)
	})
	s.Run("create id", func() {
		s.mock.ExpectExec(`INSERT INTO walkthroughs`).WillReturnResult(sqlmock.NewErrorResult(boom))
		_, err := s.store.CreateWalkthrough(ctx, &Walkthrough{})
		require.ErrorIs(s.T(), err, boom)
	})
	s.Run("get", func() {
		s.mock.ExpectQuery(`FROM walkthroughs WHERE id = \?`).WillReturnError(boom)
		_, err := s.store.GetWalkthrough(ctx, 1)
		require.ErrorIs(s.T(), err, boom)
	})
	s.Run("get bad steps", func() {
		s.mock.ExpectQuery(`FROM walkthroughs WHERE id = \?`).
			WillReturnRows(sqlmock.NewRows(cols).AddRow(1, "c1", "t", "{", time.Now()))
		_, err := s.store.GetWalkthrough(ctx, 1)
		require.Error(s.T(), err)
	})
	s.Run("list", func() {
		s.mock.ExpectQuery(`FROM walkthroughs\s+WHERE channel_id = \?`).WillReturnError(boom)
		_, err := s.store.ListWalkthroughs(ctx, "c1")
		require.ErrorIs(s.T(), err, boom)
	})
	s.Run("list scan", func() {
		s.mock.ExpectQuery(`FROM walkthroughs\s+WHERE channel_id = \?`).
			WillReturnRows(sqlmock.NewRows(cols).AddRow("x", "c1", "t", 1, time.Now()))
		_, err := s.store.ListWalkthroughs(ctx, "c1")
		require.Error(s.T(), err)
	})
	s.Run("delete", func() {
		s.mock.ExpectExec(`DELETE FROM walkthroughs WHERE id = \? AND channel_id = \?`).WillReturnError(boom)
		_, err := s.store.DeleteWalkthrough(ctx, "c1", 1)
		require.ErrorIs(s.T(), err, boom)
	})
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}
