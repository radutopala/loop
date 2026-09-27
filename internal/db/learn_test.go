package db

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

// TestLearnLifecycle runs a channel's learn switch, its hidden learn thread
// and proposals against a real SQLite, then deletes the channel and checks
// nothing learn-related is left behind.
func (s *IntegrationSuite) TestLearnLifecycle() {
	store, err := NewSQLiteStore(filepath.Join(s.T().TempDir(), "loop.db"))
	require.NoError(s.T(), err)
	defer store.Close()
	ctx := context.Background()
	chatID := seedChannel(s.T(), store, "c1")

	require.NoError(s.T(), store.UpdateChannelLearnOverride(ctx, "c1", LearnOn))
	ch, err := store.GetChannel(ctx, "c1")
	require.NoError(s.T(), err)
	require.Equal(s.T(), LearnOn, ch.LearnOverride)
	require.Empty(s.T(), ch.Kind)

	l, err := store.GetLearnChannel(ctx, "c1")
	require.NoError(s.T(), err)
	require.Nil(s.T(), l)
	require.NoError(s.T(), store.InsertLearnChannel(ctx, &Channel{ChannelID: "l1", Name: "learn", DirPath: "/tmp/c1", ParentID: "c1", Platform: "local", Kind: "ignored"}))
	l, err = store.GetLearnChannel(ctx, "c1")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "l1", l.ChannelID)
	require.Equal(s.T(), ChannelKindLearn, l.Kind)
	require.True(s.T(), l.Active)
	require.NoError(s.T(), store.InsertMessage(ctx, &Message{ChatID: chatID, ChannelID: "l1", MsgID: "m1", Content: "learn", Kind: MessageKindMessage, CreatedAt: time.Now()}))

	ps := []*LearnProposal{
		{ChannelID: "c1", LearnChannelID: "l1", Kind: LearnKindRename, Title: "rename", Payload: `{"name":"x"}`},
		{ChannelID: "c1", LearnChannelID: "l1", Kind: LearnKindMount, Title: "mount", Rationale: "why"},
	}
	require.NoError(s.T(), store.InsertLearnProposals(ctx, ps))
	require.NotZero(s.T(), ps[0].ID)
	require.Equal(s.T(), LearnPending, ps[1].Status)

	list, err := store.ListLearnProposals(ctx, "c1")
	require.NoError(s.T(), err)
	require.Len(s.T(), list, 2)
	require.Equal(s.T(), ps[1].ID, list[0].ID, "newest first")

	claimed, err := store.ClaimLearnProposal(ctx, ps[0].ID)
	require.NoError(s.T(), err)
	require.True(s.T(), claimed)
	claimed, err = store.ClaimLearnProposal(ctx, ps[0].ID)
	require.NoError(s.T(), err)
	require.False(s.T(), claimed, "already applying")
	require.NoError(s.T(), store.SetLearnProposalStatus(ctx, ps[0].ID, LearnFailed, "boom"))
	got, err := store.GetLearnProposal(ctx, ps[0].ID)
	require.NoError(s.T(), err)
	require.Equal(s.T(), LearnFailed, got.Status)
	require.Equal(s.T(), "boom", got.Error)
	claimed, err = store.ClaimLearnProposal(ctx, ps[0].ID)
	require.NoError(s.T(), err)
	require.True(s.T(), claimed, "a failed one can be retried")
	got, err = store.GetLearnProposal(ctx, ps[0].ID)
	require.NoError(s.T(), err)
	require.Empty(s.T(), got.Error, "the retry clears the error")

	missing, err := store.GetLearnProposal(ctx, 999)
	require.NoError(s.T(), err)
	require.Nil(s.T(), missing)

	require.NoError(s.T(), store.DeleteChannel(ctx, "c1"))
	l, err = store.GetChannel(ctx, "l1")
	require.NoError(s.T(), err)
	require.Nil(s.T(), l)
	list, err = store.ListLearnProposals(ctx, "c1")
	require.NoError(s.T(), err)
	require.Empty(s.T(), list)
	msgs, err := store.GetRecentMessages(ctx, "l1", 10)
	require.NoError(s.T(), err)
	require.Empty(s.T(), msgs)
}

// TestLearnThreadsOfChildrenDeleted checks that deleting a channel's threads
// also deletes the learn threads under them.
func (s *IntegrationSuite) TestLearnThreadsOfChildrenDeleted() {
	store, err := NewSQLiteStore(filepath.Join(s.T().TempDir(), "loop.db"))
	require.NoError(s.T(), err)
	defer store.Close()
	ctx := context.Background()
	seedChannel(s.T(), store, "c1")
	require.NoError(s.T(), store.UpsertChannel(ctx, &Channel{ChannelID: "t1", Name: "t1", ParentID: "c1"}))
	require.NoError(s.T(), store.InsertLearnChannel(ctx, &Channel{ChannelID: "l1", ParentID: "t1"}))
	require.NoError(s.T(), store.InsertLearnProposals(ctx, []*LearnProposal{{ChannelID: "t1", LearnChannelID: "l1", Kind: LearnKindDescription}}))

	require.NoError(s.T(), store.DeleteChannelsByParentID(ctx, "c1"))
	for _, id := range []string{"t1", "l1"} {
		ch, err := store.GetChannel(ctx, id)
		require.NoError(s.T(), err)
		require.Nil(s.T(), ch, id)
	}
	list, err := store.ListLearnProposals(ctx, "t1")
	require.NoError(s.T(), err)
	require.Empty(s.T(), list)
}

var learnProposalCols = []string{"id", "channel_id", "learn_channel_id", "kind", "title", "rationale", "payload", "status", "error", "created_at", "updated_at"}

func (s *StoreSuite) TestLearnStoreErrors() {
	ctx := context.Background()
	boom := errors.New("boom")

	s.Run("get learn channel", func() {
		s.mock.ExpectQuery(`FROM channels WHERE parent_id = \? AND kind = \?`).WithArgs("c1", ChannelKindLearn).WillReturnError(boom)
		_, err := s.store.GetLearnChannel(ctx, "c1")
		require.ErrorIs(s.T(), err, boom)
	})
	s.Run("insert proposal", func() {
		s.mock.ExpectBegin()
		s.mock.ExpectExec(`INSERT INTO learn_proposals`).WillReturnError(boom)
		s.mock.ExpectRollback()
		require.ErrorIs(s.T(), s.store.InsertLearnProposals(ctx, []*LearnProposal{{}}), boom)
	})
	s.Run("proposal id", func() {
		s.mock.ExpectBegin()
		s.mock.ExpectExec(`INSERT INTO learn_proposals`).WillReturnResult(sqlmock.NewErrorResult(boom))
		s.mock.ExpectRollback()
		require.ErrorIs(s.T(), s.store.InsertLearnProposals(ctx, []*LearnProposal{{}}), boom)
	})
	s.Run("list query", func() {
		s.mock.ExpectQuery(`FROM learn_proposals WHERE channel_id = \?`).WillReturnError(boom)
		_, err := s.store.ListLearnProposals(ctx, "c1")
		require.ErrorIs(s.T(), err, boom)
	})
	s.Run("list scan", func() {
		s.mock.ExpectQuery(`FROM learn_proposals WHERE channel_id = \?`).
			WillReturnRows(sqlmock.NewRows(learnProposalCols).AddRow("x", "", "", "", "", "", "", "", "", time.Now(), time.Now()))
		_, err := s.store.ListLearnProposals(ctx, "c1")
		require.Error(s.T(), err)
	})
	s.Run("get proposal", func() {
		s.mock.ExpectQuery(`FROM learn_proposals WHERE id = \?`).WillReturnError(boom)
		_, err := s.store.GetLearnProposal(ctx, 1)
		require.ErrorIs(s.T(), err, boom)
	})
	s.Run("claim", func() {
		s.mock.ExpectExec(`UPDATE learn_proposals SET status = \?, error = ''`).WillReturnError(boom)
		_, err := s.store.ClaimLearnProposal(ctx, 1)
		require.ErrorIs(s.T(), err, boom)
	})
	s.Run("claim rows affected", func() {
		s.mock.ExpectExec(`UPDATE learn_proposals SET status = \?, error = ''`).WillReturnResult(sqlmock.NewErrorResult(sql.ErrConnDone))
		_, err := s.store.ClaimLearnProposal(ctx, 1)
		require.ErrorIs(s.T(), err, sql.ErrConnDone)
	})
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}
