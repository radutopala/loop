package db

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

// TestExplainLifecycle runs a channel's explain switch, the turn lookups and
// an explanation from queued to done and back against a real SQLite.
func (s *IntegrationSuite) TestExplainLifecycle() {
	store, err := NewSQLiteStore(filepath.Join(s.T().TempDir(), "loop.db"))
	require.NoError(s.T(), err)
	defer store.Close()
	ctx := context.Background()
	chatID := seedChannel(s.T(), store, "c1")

	require.NoError(s.T(), store.UpdateChannelExplainOverride(ctx, "c1", LearnOn))
	ch, err := store.GetChannel(ctx, "c1")
	require.NoError(s.T(), err)
	require.Equal(s.T(), LearnOn, ch.ExplainOverride)

	base := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	insert := func(m *Message) {
		m.ChatID, m.ChannelID, m.Kind = chatID, "c1", MessageKindMessage
		require.NoError(s.T(), store.InsertMessage(ctx, m))
	}
	insert(&Message{MsgID: "u1", AuthorID: "user", Content: "fix the tests", IsTriggered: true, CreatedAt: base})
	insert(&Message{MsgID: "b1", IsBot: true, Content: "looking", TriggerMsgID: "u1", CreatedAt: base.Add(time.Second)})
	insert(&Message{MsgID: "b2", IsBot: true, Content: "fixed them", TriggerMsgID: "u1", CreatedAt: base.Add(2 * time.Second)})

	last, err := store.LastBotMessage(ctx, "c1", "u1")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "b2", last.MsgID)
	none, err := store.LastBotMessage(ctx, "c1", "u2")
	require.NoError(s.T(), err)
	require.Nil(s.T(), none)
	prompt, err := store.GetChatMessage(ctx, "c1", "u1")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "fix the tests", prompt.Content)
	missing, err := store.GetChatMessage(ctx, "c1", "nope")
	require.NoError(s.T(), err)
	require.Nil(s.T(), missing)

	e, queued, err := store.QueueExplanation(ctx, &Explanation{ChannelID: "c1", MessageID: "b2", ExplainChannelID: "e1", TriggerMsgID: "x1"})
	require.NoError(s.T(), err)
	require.True(s.T(), queued)
	require.Equal(s.T(), ExplainQueued, e.Status)
	require.Equal(s.T(), "x1", e.TriggerMsgID)

	_, queued, err = store.QueueExplanation(ctx, &Explanation{ChannelID: "c1", MessageID: "b2", ExplainChannelID: "e1", TriggerMsgID: "x2"})
	require.NoError(s.T(), err)
	require.False(s.T(), queued, "already queued")

	byTrigger, err := store.GetExplanationByTrigger(ctx, "e1", "x1")
	require.NoError(s.T(), err)
	require.Equal(s.T(), e.ID, byTrigger.ID)
	noTrigger, err := store.GetExplanationByTrigger(ctx, "e1", "x2")
	require.NoError(s.T(), err)
	require.Nil(s.T(), noTrigger)

	require.NoError(s.T(), store.UpdateExplanation(ctx, e.ID, ExplainDone, "## Summary", ""))
	list, err := store.ListExplanations(ctx, "c1")
	require.NoError(s.T(), err)
	require.Len(s.T(), list, 1)
	require.Equal(s.T(), ExplainDone, list[0].Status)
	require.Equal(s.T(), "## Summary", list[0].Content)
	require.Equal(s.T(), last.ID, list[0].MessageRowID)
	require.Equal(s.T(), "fix the tests", list[0].Prompt)
	require.Equal(s.T(), "fixed them", list[0].Reply)

	again, queued, err := store.QueueExplanation(ctx, &Explanation{ChannelID: "c1", MessageID: "b2", ExplainChannelID: "e1", TriggerMsgID: "x3"})
	require.NoError(s.T(), err)
	require.True(s.T(), queued, "a done one can be explained again")
	require.Equal(s.T(), e.ID, again.ID)
	require.Empty(s.T(), again.Content, "re-explaining clears the old write-up")
	require.Equal(s.T(), "x3", again.TriggerMsgID)

	store.nowFunc = func() time.Time { return again.UpdatedAt.Add(explainStale + time.Second) }
	_, queued, err = store.QueueExplanation(ctx, &Explanation{ChannelID: "c1", MessageID: "b2", ExplainChannelID: "e1", TriggerMsgID: "x4"})
	require.NoError(s.T(), err)
	require.True(s.T(), queued, "one stuck in queued can be queued again")

	// An explanation of a deleted message keeps its content but loses the
	// turn's details.
	_, _, err = store.QueueExplanation(ctx, &Explanation{ChannelID: "c1", MessageID: "gone", ExplainChannelID: "e1", TriggerMsgID: "x5"})
	require.NoError(s.T(), err)
	list, err = store.ListExplanations(ctx, "c1")
	require.NoError(s.T(), err)
	require.Len(s.T(), list, 2)
	require.Equal(s.T(), "gone", list[0].MessageID, "newest first")
	require.Zero(s.T(), list[0].MessageRowID)
	require.Empty(s.T(), list[0].Prompt)
}

// TestFailInterruptedExplanations checks that only explanations whose
// trigger no longer waits to run are failed at startup.
func (s *IntegrationSuite) TestFailInterruptedExplanations() {
	store, err := NewSQLiteStore(filepath.Join(s.T().TempDir(), "loop.db"))
	require.NoError(s.T(), err)
	defer store.Close()
	ctx := context.Background()
	chatID := seedChannel(s.T(), store, "c1")
	require.NoError(s.T(), store.InsertHiddenThread(ctx, &Channel{ChannelID: "e1", ParentID: "c1", Kind: ChannelKindExplain}))
	require.NoError(s.T(), store.InsertMessage(ctx, &Message{ChatID: chatID, ChannelID: "e1", MsgID: "pending", IsTriggered: true, Kind: MessageKindMessage, CreatedAt: time.Now()}))
	require.NoError(s.T(), store.InsertMessage(ctx, &Message{ChatID: chatID, ChannelID: "e1", MsgID: "ran", IsTriggered: true, IsProcessed: true, Kind: MessageKindMessage, CreatedAt: time.Now()}))

	queue := func(messageID, trigger string) *Explanation {
		e, _, err := store.QueueExplanation(ctx, &Explanation{ChannelID: "c1", MessageID: messageID, ExplainChannelID: "e1", TriggerMsgID: trigger})
		require.NoError(s.T(), err)
		return e
	}
	waiting := queue("b1", "pending")
	interrupted := queue("b2", "ran")
	lost := queue("b3", "never-stored")
	done := queue("b4", "ran-too")
	require.NoError(s.T(), store.UpdateExplanation(ctx, interrupted.ID, ExplainRunning, "", ""))
	require.NoError(s.T(), store.UpdateExplanation(ctx, done.ID, ExplainDone, "ok", ""))

	n, err := store.FailInterruptedExplanations(ctx)
	require.NoError(s.T(), err)
	require.EqualValues(s.T(), 2, n)
	for _, tc := range []struct {
		e    *Explanation
		want string
	}{
		{waiting, ExplainQueued},
		{interrupted, ExplainFailed},
		{lost, ExplainFailed},
		{done, ExplainDone},
	} {
		got, err := store.GetExplanation(ctx, "c1", tc.e.MessageID)
		require.NoError(s.T(), err)
		require.Equal(s.T(), tc.want, got.Status, tc.e.MessageID)
	}
	got, err := store.GetExplanation(ctx, "c1", "b2")
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), got.Error)
}

var explanationCols = []string{"id", "channel_id", "message_id", "explain_channel_id", "trigger_msg_id", "status", "content", "error", "created_at", "updated_at"}

func (s *StoreSuite) TestExplainStoreErrors() {
	ctx := context.Background()
	boom := errors.New("boom")
	e := &Explanation{ChannelID: "c1", MessageID: "b1"}

	s.Run("chat message", func() {
		s.mock.ExpectQuery(`FROM messages\s+WHERE channel_id = \? AND msg_id = \?`).WillReturnError(boom)
		_, err := s.store.GetChatMessage(ctx, "c1", "b1")
		require.ErrorIs(s.T(), err, boom)
	})
	s.Run("queue", func() {
		s.mock.ExpectExec(`INSERT INTO explanations`).WillReturnError(boom)
		_, _, err := s.store.QueueExplanation(ctx, e)
		require.ErrorIs(s.T(), err, boom)
	})
	s.Run("queue rows affected", func() {
		s.mock.ExpectExec(`INSERT INTO explanations`).WillReturnResult(sqlmock.NewErrorResult(boom))
		_, _, err := s.store.QueueExplanation(ctx, e)
		require.ErrorIs(s.T(), err, boom)
	})
	s.Run("queue reload", func() {
		s.mock.ExpectExec(`INSERT INTO explanations`).WillReturnResult(sqlmock.NewResult(1, 1))
		s.mock.ExpectQuery(`FROM explanations WHERE channel_id = \? AND message_id = \?`).WillReturnError(boom)
		_, _, err := s.store.QueueExplanation(ctx, e)
		require.ErrorIs(s.T(), err, boom)
	})
	s.Run("list query", func() {
		s.mock.ExpectQuery(`FROM explanations e`).WillReturnError(boom)
		_, err := s.store.ListExplanations(ctx, "c1")
		require.ErrorIs(s.T(), err, boom)
	})
	s.Run("list scan", func() {
		s.mock.ExpectQuery(`FROM explanations e`).
			WillReturnRows(sqlmock.NewRows(append(explanationCols, "row_id", "prompt", "reply")).
				AddRow("x", "", "", "", "", "", "", "", time.Now(), time.Now(), 0, "", ""))
		_, err := s.store.ListExplanations(ctx, "c1")
		require.Error(s.T(), err)
	})
	s.Run("fail interrupted", func() {
		s.mock.ExpectExec(`UPDATE explanations SET status = \?, error = \?`).WillReturnError(boom)
		_, err := s.store.FailInterruptedExplanations(ctx)
		require.ErrorIs(s.T(), err, boom)
	})
	s.Run("get by trigger", func() {
		s.mock.ExpectQuery(`FROM explanations WHERE explain_channel_id = \?`).WillReturnError(sql.ErrConnDone)
		_, err := s.store.GetExplanationByTrigger(ctx, "e1", "x1")
		require.ErrorIs(s.T(), err, sql.ErrConnDone)
	})
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestExplainSnippet() {
	long := strings.Repeat("é", explainSnippet+5)
	require.Equal(s.T(), "short", ExplainSnippet("short"))
	require.Equal(s.T(), strings.Repeat("é", explainSnippet), ExplainSnippet(long))
}
