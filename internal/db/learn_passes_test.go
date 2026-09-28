package db

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

// TestLearnPassLifecycle runs learn passes from queued to done against a
// real SQLite: the lookups by trigger, the pass proposals attach to, the
// list with the reviewed turn's row id, and the proposals' turn.
func (s *IntegrationSuite) TestLearnPassLifecycle() {
	store, err := NewSQLiteStore(filepath.Join(s.T().TempDir(), "loop.db"))
	require.NoError(s.T(), err)
	defer store.Close()
	ctx := context.Background()
	chatID := seedChannel(s.T(), store, "c1")
	bot := &Message{ChatID: chatID, ChannelID: "c1", MsgID: "b1", IsBot: true, TriggerMsgID: "u1", Kind: MessageKindMessage, CreatedAt: time.Now()}
	require.NoError(s.T(), store.InsertMessage(ctx, bot))

	none, err := store.LatestLearnPass(ctx, "l1")
	require.NoError(s.T(), err)
	require.Nil(s.T(), none)

	insert := func(messageID, trigger string) *LearnPass {
		p, err := store.InsertLearnPass(ctx, &LearnPass{ChannelID: "c1", MessageID: messageID, LearnChannelID: "l1", TriggerMsgID: trigger})
		require.NoError(s.T(), err)
		require.NotZero(s.T(), p.ID)
		require.Equal(s.T(), LearnPassQueued, p.Status)
		return p
	}
	first := insert("b1", "x1")
	second := insert("gone", "x2")

	got, err := store.GetLearnPassByTrigger(ctx, "l1", "x2")
	require.NoError(s.T(), err)
	require.Equal(s.T(), second.ID, got.ID)
	require.Equal(s.T(), "x2", got.TriggerMsgID)
	missing, err := store.GetLearnPassByTrigger(ctx, "l1", "x3")
	require.NoError(s.T(), err)
	require.Nil(s.T(), missing)

	latest, err := store.LatestLearnPass(ctx, "l1")
	require.NoError(s.T(), err)
	require.Nil(s.T(), latest, "queued passes file nothing")

	active, err := store.ActiveLearnPass(ctx, "c1", "b1")
	require.NoError(s.T(), err)
	require.Equal(s.T(), first.ID, active.ID, "a queued pass is active")
	none, err = store.ActiveLearnPass(ctx, "c1", "b2")
	require.NoError(s.T(), err)
	require.Nil(s.T(), none, "no pass over the turn")

	require.NoError(s.T(), store.UpdateLearnPass(ctx, first.ID, LearnPassRunning, ""))
	require.NoError(s.T(), store.UpdateLearnPass(ctx, second.ID, LearnPassDone, ""))
	latest, err = store.LatestLearnPass(ctx, "l1")
	require.NoError(s.T(), err)
	require.Equal(s.T(), first.ID, latest.ID, "the running pass wins over a newer done one")
	active, err = store.ActiveLearnPass(ctx, "c1", "b1")
	require.NoError(s.T(), err)
	require.Equal(s.T(), first.ID, active.ID, "a running pass is active")
	active, err = store.ActiveLearnPass(ctx, "c1", "gone")
	require.NoError(s.T(), err)
	require.Nil(s.T(), active, "a done pass isn't")
	require.NoError(s.T(), store.UpdateLearnPass(ctx, first.ID, LearnPassFailed, "boom"))
	latest, err = store.LatestLearnPass(ctx, "l1")
	require.NoError(s.T(), err)
	require.Equal(s.T(), second.ID, latest.ID, "else the newest done one")
	active, err = store.ActiveLearnPass(ctx, "c1", "b1")
	require.NoError(s.T(), err)
	require.Nil(s.T(), active, "nor a failed one")

	list, err := store.ListLearnPasses(ctx, "c1")
	require.NoError(s.T(), err)
	require.Len(s.T(), list, 2)
	require.Equal(s.T(), second.ID, list[0].ID, "newest first")
	require.Zero(s.T(), list[0].MessageRowID, "the turn's message is gone")
	require.Equal(s.T(), bot.ID, list[1].MessageRowID)
	require.Equal(s.T(), LearnPassFailed, list[1].Status)
	require.Equal(s.T(), "boom", list[1].Error)

	ps := []*LearnProposal{{ChannelID: "c1", LearnChannelID: "l1", MessageID: "b1", Kind: LearnKindRename}}
	_, err = store.FileLearnProposals(ctx, "c1", ps, nil)
	require.NoError(s.T(), err)
	stored, err := store.GetLearnProposal(ctx, ps[0].ID)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "b1", stored.MessageID)

	require.NoError(s.T(), store.DeleteChannel(ctx, "c1"))
	list, err = store.ListLearnPasses(ctx, "c1")
	require.NoError(s.T(), err)
	require.Empty(s.T(), list)
}

// TestFailInterruptedLearnPasses checks that only passes whose trigger no
// longer waits to run are failed at startup.
func (s *IntegrationSuite) TestFailInterruptedLearnPasses() {
	store, err := NewSQLiteStore(filepath.Join(s.T().TempDir(), "loop.db"))
	require.NoError(s.T(), err)
	defer store.Close()
	ctx := context.Background()
	chatID := seedChannel(s.T(), store, "c1")
	require.NoError(s.T(), store.InsertHiddenThread(ctx, &Channel{ChannelID: "l1", ParentID: "c1", Kind: ChannelKindLearn}))
	require.NoError(s.T(), store.InsertMessage(ctx, &Message{ChatID: chatID, ChannelID: "l1", MsgID: "pending", IsTriggered: true, Kind: MessageKindMessage, CreatedAt: time.Now()}))
	require.NoError(s.T(), store.InsertMessage(ctx, &Message{ChatID: chatID, ChannelID: "l1", MsgID: "ran", IsTriggered: true, IsProcessed: true, Kind: MessageKindMessage, CreatedAt: time.Now()}))

	insert := func(trigger, status string) *LearnPass {
		p, err := store.InsertLearnPass(ctx, &LearnPass{ChannelID: "c1", MessageID: "b-" + trigger, LearnChannelID: "l1", TriggerMsgID: trigger})
		require.NoError(s.T(), err)
		require.NoError(s.T(), store.UpdateLearnPass(ctx, p.ID, status, ""))
		return p
	}
	tests := []struct {
		pass *LearnPass
		want string
	}{
		{insert("pending", LearnPassQueued), LearnPassQueued},
		{insert("ran", LearnPassRunning), LearnPassFailed},
		{insert("never-stored", LearnPassQueued), LearnPassFailed},
		{insert("ran-too", LearnPassDone), LearnPassDone},
		{insert("replaced", LearnPassSuperseded), LearnPassSuperseded},
	}

	n, err := store.FailInterruptedLearnPasses(ctx)
	require.NoError(s.T(), err)
	require.EqualValues(s.T(), 2, n)
	for _, tc := range tests {
		got, err := store.GetLearnPassByTrigger(ctx, "l1", tc.pass.TriggerMsgID)
		require.NoError(s.T(), err)
		require.Equal(s.T(), tc.want, got.Status, tc.pass.TriggerMsgID)
		if tc.want == LearnPassFailed {
			require.Equal(s.T(), "Loop stopped before the learn pass finished", got.Error)
		}
	}
}

var learnPassCols = []string{"id", "channel_id", "message_id", "learn_channel_id", "trigger_msg_id", "status", "error", "created_at", "updated_at"}

func (s *StoreSuite) TestLearnPassStoreErrors() {
	ctx := context.Background()
	boom := errors.New("boom")

	s.Run("insert", func() {
		s.mock.ExpectExec(`INSERT INTO learn_passes`).WillReturnError(boom)
		_, err := s.store.InsertLearnPass(ctx, &LearnPass{})
		require.ErrorIs(s.T(), err, boom)
	})
	s.Run("insert id", func() {
		s.mock.ExpectExec(`INSERT INTO learn_passes`).WillReturnResult(sqlmock.NewErrorResult(boom))
		_, err := s.store.InsertLearnPass(ctx, &LearnPass{})
		require.ErrorIs(s.T(), err, boom)
	})
	s.Run("get by trigger", func() {
		s.mock.ExpectQuery(`FROM learn_passes WHERE learn_channel_id = \? AND trigger_msg_id = \?`).WillReturnError(boom)
		_, err := s.store.GetLearnPassByTrigger(ctx, "l1", "x1")
		require.ErrorIs(s.T(), err, boom)
	})
	s.Run("list query", func() {
		s.mock.ExpectQuery(`FROM learn_passes p`).WillReturnError(boom)
		_, err := s.store.ListLearnPasses(ctx, "c1")
		require.ErrorIs(s.T(), err, boom)
	})
	s.Run("list scan", func() {
		s.mock.ExpectQuery(`FROM learn_passes p`).
			WillReturnRows(sqlmock.NewRows(append(learnPassCols, "row_id")).
				AddRow("x", "", "", "", "", "", "", time.Now(), time.Now(), 0))
		_, err := s.store.ListLearnPasses(ctx, "c1")
		require.Error(s.T(), err)
	})
	s.Run("fail interrupted", func() {
		s.mock.ExpectExec(`UPDATE learn_passes SET status = \?, error = \?`).WillReturnError(boom)
		_, err := s.store.FailInterruptedLearnPasses(ctx)
		require.ErrorIs(s.T(), err, boom)
	})
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}
