package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"time"

	"github.com/stretchr/testify/require"
)

// TestForkAtMessage covers what a fork at a message reads and writes: which
// rows can be forked at, the first such reply to a prompt, and the resume
// point a fork-pending thread keeps until its first run stores a session.
func (s *IntegrationSuite) TestForkAtMessage() {
	store, err := NewSQLiteStore(filepath.Join(s.T().TempDir(), "loop.db"))
	require.NoError(s.T(), err)
	defer store.Close()
	ctx := context.Background()
	chatID := seedChannel(s.T(), store, "c1")

	base := time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)
	insert := func(m *Message) {
		m.ChatID, m.ChannelID, m.Kind = chatID, "c1", MessageKindMessage
		require.NoError(s.T(), store.InsertMessage(ctx, m))
	}
	insert(&Message{MsgID: "u1", Content: "fix the tests", CreatedAt: base})
	insert(&Message{MsgID: "b0", IsBot: true, Content: "subagent", TriggerMsgID: "u1", CreatedAt: base.Add(time.Second)})
	insert(&Message{MsgID: "b1", IsBot: true, Content: "looking", TriggerMsgID: "u1", SessionID: "sess-1", TranscriptUUID: "uuid-1", CreatedAt: base.Add(2 * time.Second)})
	insert(&Message{MsgID: "b2", IsBot: true, Content: "fixed", TriggerMsgID: "u1", SessionID: "sess-1", TranscriptUUID: "uuid-2", CreatedAt: base.Add(3 * time.Second)})

	forkable := func(want map[string]bool) {
		for msgID, want := range want {
			m, err := store.GetChatMessage(ctx, "c1", msgID)
			require.NoError(s.T(), err)
			require.Equal(s.T(), want, m.Forkable, msgID)
		}
	}
	forkable(map[string]bool{"u1": false, "b0": false, "b1": true, "b2": true})

	// A prompt's entry is recorded on its own row only.
	require.NoError(s.T(), store.SetPromptTranscriptRef(ctx, "c1", "u1", "sess-1", "uuid-0"))
	require.NoError(s.T(), store.SetPromptTranscriptRef(ctx, "c1", "b0", "sess-1", "uuid-x"))
	forkable(map[string]bool{"u1": true, "b0": false})
	u1, err := store.GetChatMessage(ctx, "c1", "u1")
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{"sess-1", "uuid-0"}, []string{u1.SessionID, u1.TranscriptUUID})

	first, err := store.FirstForkableReply(ctx, "c1", "u1")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "b1", first.MsgID)
	none, err := store.FirstForkableReply(ctx, "c1", "u2")
	require.NoError(s.T(), err)
	require.Nil(s.T(), none)

	ok, err := store.MarkSessionForkPendingAt(ctx, "c1", "sess-1", "uuid-1")
	require.NoError(s.T(), err)
	require.True(s.T(), ok)
	at, err := store.ForkResumeAt(ctx, "c1")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "uuid-1", at)

	// Forking a whole session, or the first run storing its own, clears it.
	_, err = store.MarkSessionForkPending(ctx, "c1", "sess-1")
	require.NoError(s.T(), err)
	at, err = store.ForkResumeAt(ctx, "c1")
	require.NoError(s.T(), err)
	require.Empty(s.T(), at)
	_, err = store.MarkSessionForkPendingAt(ctx, "c1", "sess-1", "uuid-2")
	require.NoError(s.T(), err)
	require.NoError(s.T(), store.UpdateSessionID(ctx, "c1", "sess-2"))
	ch, err := store.GetChannel(ctx, "c1")
	require.NoError(s.T(), err)
	require.False(s.T(), ch.ForkPending)
	at, err = store.ForkResumeAt(ctx, "c1")
	require.NoError(s.T(), err)
	require.Empty(s.T(), at)

	gone, err := store.ForkResumeAt(ctx, "nope")
	require.NoError(s.T(), err)
	require.Empty(s.T(), gone)
}

func (s *StoreSuite) TestForkResumeAtError() {
	s.mock.ExpectQuery(`SELECT fork_resume_at FROM channels`).WithArgs("c1").WillReturnError(sql.ErrConnDone)
	_, err := s.store.ForkResumeAt(context.Background(), "c1")
	require.ErrorIs(s.T(), err, sql.ErrConnDone)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}
