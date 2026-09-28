package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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

	l, err := store.GetHiddenThread(ctx, "c1", ChannelKindLearn)
	require.NoError(s.T(), err)
	require.Nil(s.T(), l)
	require.ErrorContains(s.T(), store.InsertHiddenThread(ctx, &Channel{ChannelID: "l0", ParentID: "c1", Kind: "chat"}), "not a hidden thread kind")
	require.NoError(s.T(), store.InsertHiddenThread(ctx, &Channel{ChannelID: "l1", Name: "learn", DirPath: "/tmp/c1", ParentID: "c1", Platform: "local", Kind: ChannelKindLearn}))
	require.NoError(s.T(), store.InsertHiddenThread(ctx, &Channel{ChannelID: "e1", Name: "explain", DirPath: "/tmp/c1", ParentID: "c1", Platform: "local", Kind: ChannelKindExplain}))
	hidden, err := store.ListHiddenThreads(ctx, "c1")
	require.NoError(s.T(), err)
	require.Len(s.T(), hidden, 2)
	require.Equal(s.T(), ChannelKindExplain, hidden[1].Kind)
	l, err = store.GetHiddenThread(ctx, "c1", ChannelKindLearn)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "l1", l.ChannelID)
	require.Equal(s.T(), ChannelKindLearn, l.Kind)
	require.True(s.T(), l.Active)
	_, err = store.db.ExecContext(ctx, `INSERT INTO quality_snapshots (channel_id, signal_value, geo_mean) VALUES ('l1', 1, 1)`)
	require.NoError(s.T(), err)
	require.NoError(s.T(), store.InsertMessage(ctx, &Message{ChatID: chatID, ChannelID: "l1", MsgID: "m1", Content: "learn", Kind: MessageKindMessage, CreatedAt: time.Now()}))
	require.NoError(s.T(), store.InsertMessage(ctx, &Message{ChatID: chatID, ChannelID: "e1", MsgID: "m2", Content: "learn", Kind: MessageKindMessage, CreatedAt: time.Now()}))
	require.NoError(s.T(), store.InsertMessage(ctx, &Message{ChatID: chatID, ChannelID: "c1", MsgID: "m0", Content: "learn in chat", Kind: MessageKindMessage, CreatedAt: time.Now()}))
	found, err := store.SearchMessages(ctx, "learn", 10)
	require.NoError(s.T(), err)
	require.Len(s.T(), found, 1, "learn and explain threads are left out of search")
	require.Equal(s.T(), "c1", found[0].ChannelID)

	ps := []*LearnProposal{
		{ChannelID: "c1", LearnChannelID: "l1", Kind: LearnKindRename, Title: "rename", Payload: `{"name":"x"}`},
		{ChannelID: "c1", LearnChannelID: "l1", Kind: LearnKindMount, Title: "mount", Rationale: "why"},
	}
	withdrawn, err := store.FileLearnProposals(ctx, "c1", ps, nil)
	require.NoError(s.T(), err)
	require.Empty(s.T(), withdrawn)
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
	store.nowFunc = func() time.Time { return got.UpdatedAt.Add(learnApplyStale + time.Second) }
	claimed, err = store.ClaimLearnProposal(ctx, ps[0].ID)
	require.NoError(s.T(), err)
	require.True(s.T(), claimed, "one stuck in applying can be claimed again")

	missing, err := store.GetLearnProposal(ctx, 999)
	require.NoError(s.T(), err)
	require.Nil(s.T(), missing)

	require.NoError(s.T(), store.DeleteChannel(ctx, "c1"))
	for _, id := range []string{"l1", "e1"} {
		l, err = store.GetChannel(ctx, id)
		require.NoError(s.T(), err)
		require.Nil(s.T(), l, id)
	}
	list, err = store.ListLearnProposals(ctx, "c1")
	require.NoError(s.T(), err)
	require.Empty(s.T(), list)
	for _, id := range []string{"l1", "e1"} {
		msgs, err := store.GetRecentMessages(ctx, id, 10)
		require.NoError(s.T(), err)
		require.Empty(s.T(), msgs, id)
	}
	var snapshots int
	require.NoError(s.T(), store.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM quality_snapshots WHERE channel_id = 'l1'`).Scan(&snapshots))
	require.Zero(s.T(), snapshots)
}

// TestLearnWithdraw withdraws proposals against a real SQLite: only a
// pending or failed one of the channel, and nothing changes when one of a
// call's withdrawals is refused.
func (s *IntegrationSuite) TestLearnWithdraw() {
	store, err := NewSQLiteStore(filepath.Join(s.T().TempDir(), "loop.db"))
	require.NoError(s.T(), err)
	defer store.Close()
	ctx := context.Background()
	seedChannel(s.T(), store, "c1")
	seedChannel(s.T(), store, "c2")
	file := func(channelID string, n int) []*LearnProposal {
		var ps []*LearnProposal
		for range n {
			ps = append(ps, &LearnProposal{ChannelID: channelID, LearnChannelID: "l-" + channelID, Kind: LearnKindRename, Title: "rename", Payload: `{"name":"x"}`})
		}
		_, err := store.FileLearnProposals(ctx, channelID, ps, nil)
		require.NoError(s.T(), err)
		return ps
	}
	ps := file("c1", 5)
	pending, failed, applying, applied, dismissed := ps[0].ID, ps[1].ID, ps[2].ID, ps[3].ID, ps[4].ID
	require.NoError(s.T(), store.SetLearnProposalStatus(ctx, failed, LearnFailed, "boom"))
	claimed, err := store.ClaimLearnProposal(ctx, applying)
	require.NoError(s.T(), err)
	require.True(s.T(), claimed)
	require.NoError(s.T(), store.SetLearnProposalStatus(ctx, applied, LearnApplied, ""))
	require.NoError(s.T(), store.SetLearnProposalStatus(ctx, dismissed, LearnDismissed, ""))
	other := file("c2", 1)[0].ID

	refused := []struct {
		name string
		id   int64
		want string
	}{
		{name: "applying", id: applying, want: "is applying; only pending or failed proposals can be withdrawn"},
		{name: "applied", id: applied, want: "is applied; only pending or failed proposals can be withdrawn"},
		{name: "dismissed", id: dismissed, want: "is dismissed; only pending or failed proposals can be withdrawn"},
		{name: "another channel's", id: other, want: "not found in this channel"},
		{name: "missing", id: 999, want: "not found in this channel"},
	}
	for _, tc := range refused {
		s.Run(tc.name, func() {
			// The pending one comes first, so the refusal rolls it back too.
			newer := &LearnProposal{ChannelID: "c1", LearnChannelID: "l-c1", Kind: LearnKindRename}
			_, err := store.FileLearnProposals(ctx, "c1", []*LearnProposal{newer}, []LearnWithdrawal{{ID: pending, Reason: "stale"}, {ID: tc.id, Reason: "stale"}})
			var werr *LearnWithdrawError
			require.ErrorAs(s.T(), err, &werr)
			require.Equal(s.T(), tc.id, werr.ID)
			require.EqualError(s.T(), err, fmt.Sprintf("proposal %d %s", tc.id, tc.want))
			got, err := store.GetLearnProposal(ctx, pending)
			require.NoError(s.T(), err)
			require.Equal(s.T(), LearnPending, got.Status)
			list, err := store.ListLearnProposals(ctx, "c1")
			require.NoError(s.T(), err)
			require.Len(s.T(), list, 5, "the new proposal was rolled back")
		})
	}

	newer := &LearnProposal{ChannelID: "c1", LearnChannelID: "l-c1", Kind: LearnKindRename, Title: "better"}
	withdrawn, err := store.FileLearnProposals(ctx, "c1", []*LearnProposal{newer}, []LearnWithdrawal{{ID: pending, Reason: "stale"}, {ID: failed, Reason: "replaced"}})
	require.NoError(s.T(), err)
	require.NotZero(s.T(), newer.ID)
	require.Len(s.T(), withdrawn, 2)
	require.Equal(s.T(), pending, withdrawn[0].ID)
	require.Equal(s.T(), LearnWithdrawn, withdrawn[0].Status)
	require.Equal(s.T(), "stale", withdrawn[0].WithdrawnReason)
	require.Equal(s.T(), LearnWithdrawn, withdrawn[1].Status)
	require.Equal(s.T(), "replaced", withdrawn[1].WithdrawnReason)
	require.Empty(s.T(), withdrawn[1].Error, "withdrawing clears a failed apply's error")

	// A withdrawn proposal can't be claimed, nor withdrawn again.
	claimed, err = store.ClaimLearnProposal(ctx, pending)
	require.NoError(s.T(), err)
	require.False(s.T(), claimed)
	_, err = store.FileLearnProposals(ctx, "c1", nil, []LearnWithdrawal{{ID: pending, Reason: "again"}})
	require.EqualError(s.T(), err, fmt.Sprintf("proposal %d is withdrawn; only pending or failed proposals can be withdrawn", pending))
}

// TestLearnThreadsOfChildrenDeleted checks that deleting a channel's threads
// also deletes the learn and explain threads under them.
func (s *IntegrationSuite) TestLearnThreadsOfChildrenDeleted() {
	store, err := NewSQLiteStore(filepath.Join(s.T().TempDir(), "loop.db"))
	require.NoError(s.T(), err)
	defer store.Close()
	ctx := context.Background()
	seedChannel(s.T(), store, "c1")
	require.NoError(s.T(), store.UpsertChannel(ctx, &Channel{ChannelID: "t1", Name: "t1", ParentID: "c1"}))
	require.NoError(s.T(), store.InsertHiddenThread(ctx, &Channel{ChannelID: "l1", ParentID: "t1", Kind: ChannelKindLearn}))
	require.NoError(s.T(), store.InsertHiddenThread(ctx, &Channel{ChannelID: "e1", ParentID: "t1", Kind: ChannelKindExplain}))
	_, _, err = store.QueueExplanation(ctx, &Explanation{ChannelID: "t1", MessageID: "b1", ExplainChannelID: "e1", TriggerMsgID: "x1"})
	require.NoError(s.T(), err)
	_, err = store.FileLearnProposals(ctx, "t1", []*LearnProposal{{ChannelID: "t1", LearnChannelID: "l1", Kind: LearnKindDescription}}, nil)
	require.NoError(s.T(), err)

	require.NoError(s.T(), store.DeleteChannelsByParentID(ctx, "c1"))
	require.ErrorIs(s.T(), store.InsertHiddenThread(ctx, &Channel{ChannelID: "l2", ParentID: "t1", Kind: ChannelKindLearn}), ErrParentGone, "no learn thread under a deleted parent")
	for _, id := range []string{"t1", "l1", "l2", "e1"} {
		ch, err := store.GetChannel(ctx, id)
		require.NoError(s.T(), err)
		require.Nil(s.T(), ch, id)
	}
	list, err := store.ListLearnProposals(ctx, "t1")
	require.NoError(s.T(), err)
	require.Empty(s.T(), list)
	explanations, err := store.ListExplanations(ctx, "t1")
	require.NoError(s.T(), err)
	require.Empty(s.T(), explanations)
}

var learnProposalCols = []string{"id", "channel_id", "learn_channel_id", "kind", "title", "rationale", "payload", "status", "error", "withdrawn_reason", "created_at", "updated_at"}

func (s *StoreSuite) TestLearnStoreErrors() {
	ctx := context.Background()
	boom := errors.New("boom")

	s.Run("get hidden thread", func() {
		s.mock.ExpectQuery(`FROM channels WHERE parent_id = \? AND kind = \?`).WithArgs("c1", ChannelKindLearn).WillReturnError(boom)
		_, err := s.store.GetHiddenThread(ctx, "c1", ChannelKindLearn)
		require.ErrorIs(s.T(), err, boom)
	})
	s.Run("list hidden threads", func() {
		s.mock.ExpectQuery(`FROM channels WHERE parent_id = \? AND kind IN`).WithArgs("c1").WillReturnError(boom)
		_, err := s.store.ListHiddenThreads(ctx, "c1")
		require.ErrorIs(s.T(), err, boom)
	})
	s.Run("insert hidden thread", func() {
		s.mock.ExpectExec(`INSERT INTO channels`).WillReturnError(boom)
		require.ErrorIs(s.T(), s.store.InsertHiddenThread(ctx, &Channel{Kind: ChannelKindLearn}), boom)
	})
	s.Run("insert hidden thread rows affected", func() {
		s.mock.ExpectExec(`INSERT INTO channels`).WillReturnResult(sqlmock.NewErrorResult(boom))
		require.ErrorIs(s.T(), s.store.InsertHiddenThread(ctx, &Channel{Kind: ChannelKindExplain}), boom)
	})
	s.Run("insert proposal", func() {
		s.mock.ExpectBegin()
		s.mock.ExpectExec(`INSERT INTO learn_proposals`).WillReturnError(boom)
		s.mock.ExpectRollback()
		_, err := s.store.FileLearnProposals(ctx, "c1", []*LearnProposal{{}}, nil)
		require.ErrorIs(s.T(), err, boom)
	})
	s.Run("proposal id", func() {
		s.mock.ExpectBegin()
		s.mock.ExpectExec(`INSERT INTO learn_proposals`).WillReturnResult(sqlmock.NewErrorResult(boom))
		s.mock.ExpectRollback()
		_, err := s.store.FileLearnProposals(ctx, "c1", []*LearnProposal{{}}, nil)
		require.ErrorIs(s.T(), err, boom)
	})
	withdraw := []LearnWithdrawal{{ID: 1, Reason: "stale"}}
	s.Run("withdraw", func() {
		s.mock.ExpectBegin()
		s.mock.ExpectExec(`UPDATE learn_proposals SET status = \?, withdrawn_reason = \?`).WillReturnError(boom)
		s.mock.ExpectRollback()
		_, err := s.store.FileLearnProposals(ctx, "c1", nil, withdraw)
		require.ErrorIs(s.T(), err, boom)
	})
	s.Run("withdraw rows affected", func() {
		s.mock.ExpectBegin()
		s.mock.ExpectExec(`UPDATE learn_proposals SET status = \?, withdrawn_reason = \?`).WillReturnResult(sqlmock.NewErrorResult(boom))
		s.mock.ExpectRollback()
		_, err := s.store.FileLearnProposals(ctx, "c1", nil, withdraw)
		require.ErrorIs(s.T(), err, boom)
	})
	s.Run("withdraw refused status", func() {
		s.mock.ExpectBegin()
		s.mock.ExpectExec(`UPDATE learn_proposals SET status = \?, withdrawn_reason = \?`).WillReturnResult(sqlmock.NewResult(0, 0))
		s.mock.ExpectQuery(`SELECT status FROM learn_proposals WHERE id = \? AND channel_id = \?`).WithArgs(int64(1), "c1").WillReturnError(boom)
		s.mock.ExpectRollback()
		_, err := s.store.FileLearnProposals(ctx, "c1", nil, withdraw)
		require.ErrorIs(s.T(), err, boom)
	})
	s.Run("withdrawn row", func() {
		s.mock.ExpectBegin()
		s.mock.ExpectExec(`UPDATE learn_proposals SET status = \?, withdrawn_reason = \?`).WillReturnResult(sqlmock.NewResult(0, 1))
		s.mock.ExpectQuery(`FROM learn_proposals WHERE id = \?`).WillReturnError(boom)
		s.mock.ExpectRollback()
		_, err := s.store.FileLearnProposals(ctx, "c1", nil, withdraw)
		require.ErrorIs(s.T(), err, boom)
	})
	s.Run("list query", func() {
		s.mock.ExpectQuery(`FROM learn_proposals WHERE channel_id = \?`).WillReturnError(boom)
		_, err := s.store.ListLearnProposals(ctx, "c1")
		require.ErrorIs(s.T(), err, boom)
	})
	s.Run("list scan", func() {
		s.mock.ExpectQuery(`FROM learn_proposals WHERE channel_id = \?`).
			WillReturnRows(sqlmock.NewRows(learnProposalCols).AddRow("x", "", "", "", "", "", "", "", "", "", time.Now(), time.Now()))
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
