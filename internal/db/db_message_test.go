package db

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

// --- Message tests ---

func (s *StoreSuite) TestInsertMessage() {
	msg := &Message{
		ChatID: 1, ChannelID: "ch1", MsgID: "msg1",
		AuthorID: "u1", AuthorName: "user1", Content: "hello",
		IsBot: false, IsProcessed: false, IsTriggered: true, Priority: 7, Mode: "plan",
		TriggerMsgID: "trig-msg",
		CreatedAt:    time.Now().UTC(),
	}
	s.mock.ExpectExec(`INSERT INTO messages`).
		WithArgs(msg.ChatID, msg.ChannelID, msg.MsgID, msg.AuthorID, msg.AuthorName, msg.Content, 0, 0, 1, 7, "plan", "trig-msg", sqlmock.AnyArg(), msg.ChannelID).
		WillReturnResult(sqlmock.NewResult(42, 1))
	s.mock.ExpectQuery(`SELECT chain_position FROM messages WHERE id`).
		WithArgs(int64(42)).
		WillReturnRows(sqlmock.NewRows([]string{"chain_position"}).AddRow(int64(1)))

	err := s.store.InsertMessage(context.Background(), msg)
	require.NoError(s.T(), err)
	require.Equal(s.T(), int64(42), msg.ID)
	require.Equal(s.T(), int64(1), msg.ChainPosition)
}

func (s *StoreSuite) TestInsertMessageErrors() {
	anyArgs := []driver.Value{
		sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
		sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
		sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
	}
	s.mock.ExpectExec(`INSERT INTO messages`).WithArgs(anyArgs...).WillReturnError(sql.ErrConnDone)
	err := s.store.InsertMessage(context.Background(), &Message{ChatID: 1, ChannelID: "ch1", MsgID: "msg1", AuthorID: "u1", CreatedAt: time.Now().UTC()})
	require.Error(s.T(), err)

	s.mock.ExpectExec(`INSERT INTO messages`).WithArgs(anyArgs...).WillReturnResult(sqlmock.NewErrorResult(sql.ErrConnDone))
	err = s.store.InsertMessage(context.Background(), &Message{ChatID: 1, ChannelID: "ch1", MsgID: "msg1", AuthorID: "u1", CreatedAt: time.Now().UTC()})
	require.Error(s.T(), err)

	// chain_position read-back failure path.
	s.mock.ExpectExec(`INSERT INTO messages`).WithArgs(anyArgs...).WillReturnResult(sqlmock.NewResult(99, 1))
	s.mock.ExpectQuery(`SELECT chain_position FROM messages WHERE id`).WithArgs(int64(99)).WillReturnError(sql.ErrConnDone)
	err = s.store.InsertMessage(context.Background(), &Message{ChatID: 1, ChannelID: "ch1", MsgID: "msg1", AuthorID: "u1", CreatedAt: time.Now().UTC()})
	require.Error(s.T(), err)
}

func (s *StoreSuite) TestMarkMessagesProcessed() {
	s.mock.ExpectExec(`UPDATE messages SET is_processed = 1 WHERE id IN \(\?,\?\)`).
		WithArgs(int64(1), int64(2)).
		WillReturnResult(sqlmock.NewResult(0, 2))

	err := s.store.MarkMessagesProcessed(context.Background(), []int64{1, 2})
	require.NoError(s.T(), err)
}

func (s *StoreSuite) TestMarkMessagesProcessedError() {
	s.mock.ExpectExec(`UPDATE messages SET is_processed = 1 WHERE id IN`).
		WithArgs(int64(1), int64(2)).
		WillReturnError(sql.ErrConnDone)
	require.Error(s.T(), s.store.MarkMessagesProcessed(context.Background(), []int64{1, 2}))
}

func (s *StoreSuite) TestMarkMessagesProcessedEmpty() {
	err := s.store.MarkMessagesProcessed(context.Background(), []int64{})
	require.NoError(s.T(), err)
}

func (s *StoreSuite) TestDeleteQueuedMessage() {
	s.mock.ExpectExec(`DELETE FROM messages WHERE channel_id = \? AND msg_id = \? AND is_bot = 0 AND is_processed = 0`).
		WithArgs("ch1", "msg-queued").
		WillReturnResult(sqlmock.NewResult(0, 1))

	ok, err := s.store.DeleteQueuedMessage(context.Background(), "ch1", "msg-queued")
	require.NoError(s.T(), err)
	require.True(s.T(), ok)
}

func (s *StoreSuite) TestDeleteQueuedMessageNotFound() {
	s.mock.ExpectExec(`DELETE FROM messages`).
		WithArgs("ch1", "missing").
		WillReturnResult(sqlmock.NewResult(0, 0))

	ok, err := s.store.DeleteQueuedMessage(context.Background(), "ch1", "missing")
	require.NoError(s.T(), err)
	require.False(s.T(), ok)
}

func (s *StoreSuite) TestDeleteQueuedMessageExecError() {
	s.mock.ExpectExec(`DELETE FROM messages`).
		WithArgs("ch1", "msg1").
		WillReturnError(sql.ErrConnDone)

	ok, err := s.store.DeleteQueuedMessage(context.Background(), "ch1", "msg1")
	require.Error(s.T(), err)
	require.False(s.T(), ok)
}

func (s *StoreSuite) TestDeleteQueuedMessageRowsAffectedError() {
	s.mock.ExpectExec(`DELETE FROM messages`).
		WithArgs("ch1", "msg1").
		WillReturnResult(sqlmock.NewErrorResult(sql.ErrConnDone))

	ok, err := s.store.DeleteQueuedMessage(context.Background(), "ch1", "msg1")
	require.Error(s.T(), err)
	require.False(s.T(), ok)
}

func (s *StoreSuite) TestGetRecentMessages() {
	now := time.Now().UTC()
	rows := addMessageRow(newMockMessageRows(), 1, 1, "ch1", "msg1", "u1", "user1", "hello", 0, 1, now)
	s.mock.ExpectQuery(`SELECT .+ FROM messages WHERE channel_id .+ ORDER BY created_at DESC LIMIT`).
		WithArgs("ch1", 10).
		WillReturnRows(rows)

	msgs, err := s.store.GetRecentMessages(context.Background(), "ch1", 10)
	require.NoError(s.T(), err)
	require.Len(s.T(), msgs, 1)
	require.True(s.T(), msgs[0].IsProcessed)
}

func (s *StoreSuite) TestGetRecentMessagesError() {
	s.mock.ExpectQuery(`SELECT .+ FROM messages WHERE channel_id`).WithArgs("ch1", 10).WillReturnError(sql.ErrConnDone)
	msgs, err := s.store.GetRecentMessages(context.Background(), "ch1", 10)
	require.Error(s.T(), err)
	require.Nil(s.T(), msgs)

	// Scan error inside scanMessages.
	s.mock.ExpectQuery(`SELECT .+ FROM messages WHERE channel_id`).WithArgs("ch1", 10).WillReturnRows(
		newMockMessageRows().AddRow("not-an-int", 1, "ch1", "msg1", "u1", "user1", "hello", 0, 0, 0, 0, 0, "", time.Now().UTC(), "message", int64(0), "", "", 0, ""))
	msgs, err = s.store.GetRecentMessages(context.Background(), "ch1", 10)
	require.Error(s.T(), err)
	require.Nil(s.T(), msgs)
}

func (s *StoreSuite) TestListQueuedUserMessages() {
	now := time.Now().UTC()
	rows := newMockMessageRows()
	// id=2 with priority=1 should sort before id=1 (priority DESC). The SQL
	// itself enforces this — the mock just returns rows in order.
	rows = rows.AddRow(2, 1, "ch1", "msg-bump", "u1", "user1", "do this first", 0, 0, 1, 0, 1, "", now, "message", int64(0), "", "", 0, "")
	rows = addMessageRow(rows, 1, 1, "ch1", "msg-first", "u1", "user1", "do this second", 0, 0, now)
	s.mock.ExpectQuery(`SELECT .+ FROM messages WHERE channel_id = \? AND kind = 'message' AND is_bot = 0 AND is_processed = 0 ORDER BY priority DESC, id ASC`).
		WithArgs("ch1").
		WillReturnRows(rows)

	msgs, err := s.store.ListQueuedUserMessages(context.Background(), "ch1")
	require.NoError(s.T(), err)
	require.Len(s.T(), msgs, 2)
	require.Equal(s.T(), "msg-bump", msgs[0].MsgID)
	require.Equal(s.T(), 1, msgs[0].Priority)
	require.Equal(s.T(), "msg-first", msgs[1].MsgID)
}

func (s *StoreSuite) TestListQueuedUserMessagesError() {
	s.mock.ExpectQuery(`SELECT .+ FROM messages WHERE channel_id = \? AND kind = 'message' AND is_bot = 0 AND is_processed = 0`).
		WithArgs("ch1").
		WillReturnError(sql.ErrConnDone)
	msgs, err := s.store.ListQueuedUserMessages(context.Background(), "ch1")
	require.Error(s.T(), err)
	require.Nil(s.T(), msgs)
}

func (s *StoreSuite) TestGetMessagesCursor() {
	now := time.Now().UTC()
	rows := addMessageRow(newMockMessageRows(), 5, 1, "ch1", "msg5", "u1", "user1", "five", 0, 0, now)
	s.mock.ExpectQuery(`SELECT .+ FROM messages WHERE channel_id .+ ORDER BY id DESC LIMIT`).
		WithArgs("ch1", 10).
		WillReturnRows(rows)

	msgs, err := s.store.GetMessagesCursor(context.Background(), "ch1", 0, 10)
	require.NoError(s.T(), err)
	require.Len(s.T(), msgs, 1)
	require.Equal(s.T(), int64(5), msgs[0].ID)
}

func (s *StoreSuite) TestGetMessagesCursorWithCursor() {
	now := time.Now().UTC()
	rows := addMessageRow(newMockMessageRows(), 3, 1, "ch1", "msg3", "u1", "user1", "three", 0, 0, now)
	s.mock.ExpectQuery(`SELECT .+ FROM messages WHERE channel_id .+ AND id < .+ ORDER BY id DESC LIMIT`).
		WithArgs("ch1", int64(5), 10).
		WillReturnRows(rows)

	msgs, err := s.store.GetMessagesCursor(context.Background(), "ch1", 5, 10)
	require.NoError(s.T(), err)
	require.Len(s.T(), msgs, 1)
	require.Equal(s.T(), int64(3), msgs[0].ID)
}

func (s *StoreSuite) TestGetMessagesCursorError() {
	s.mock.ExpectQuery(`SELECT .+ FROM messages WHERE channel_id`).WithArgs("ch1", 10).WillReturnError(sql.ErrConnDone)
	msgs, err := s.store.GetMessagesCursor(context.Background(), "ch1", 0, 10)
	require.Error(s.T(), err)
	require.Nil(s.T(), msgs)
}

func (s *StoreSuite) TestGetMessagesCursorWithCursorError() {
	s.mock.ExpectQuery(`SELECT .+ FROM messages WHERE channel_id`).WithArgs("ch1", int64(5), 10).WillReturnError(sql.ErrConnDone)
	msgs, err := s.store.GetMessagesCursor(context.Background(), "ch1", 5, 10)
	require.Error(s.T(), err)
	require.Nil(s.T(), msgs)
}

// --- SearchMessages tests ---

func (s *StoreSuite) TestSearchMessages() {
	now := time.Now().UTC()
	rows := addMessageRow(addMessageRow(newMockMessageRows(),
		10, 1, "ch1", "msg10", "u1", "alice", "hello world", 0, 0, now),
		5, 2, "ch2", "msg5", "bot", "assistant", "hello there", 1, 1, now)
	s.mock.ExpectQuery(`SELECT .+ FROM messages WHERE .+ content LIKE .+ ORDER BY created_at DESC LIMIT`).
		WithArgs("%hello%", 20).
		WillReturnRows(rows)

	msgs, err := s.store.SearchMessages(context.Background(), "hello", 20)
	require.NoError(s.T(), err)
	require.Len(s.T(), msgs, 2)
	require.Equal(s.T(), "hello world", msgs[0].Content)
	require.Equal(s.T(), "ch1", msgs[0].ChannelID)
	require.False(s.T(), msgs[0].IsBot)
	require.Equal(s.T(), "hello there", msgs[1].Content)
	require.True(s.T(), msgs[1].IsBot)
}

func (s *StoreSuite) TestSearchMessagesError() {
	s.mock.ExpectQuery(`SELECT .+ FROM messages WHERE content LIKE`).
		WithArgs("%fail%", 10).
		WillReturnError(sql.ErrConnDone)

	msgs, err := s.store.SearchMessages(context.Background(), "fail", 10)
	require.Error(s.T(), err)
	require.Nil(s.T(), msgs)
}

// --- GetMessagesAround tests ---

func (s *StoreSuite) TestGetMessagesAround() {
	now := time.Now().UTC()
	rows := addMessageRow(addMessageRow(addMessageRow(newMockMessageRows(),
		8, 1, "ch1", "msg8", "u1", "alice", "before", 0, 0, now),
		10, 1, "ch1", "msg10", "u1", "alice", "target", 0, 0, now),
		11, 1, "ch1", "msg11", "bot", "assistant", "after", 1, 1, now)
	s.mock.ExpectQuery(`SELECT .+ FROM .+ UNION ALL .+ ORDER BY id ASC`).
		WithArgs("ch1", int64(10), 25, "ch1", int64(10), 25).
		WillReturnRows(rows)

	msgs, err := s.store.GetMessagesAround(context.Background(), "ch1", 10, 50)
	require.NoError(s.T(), err)
	require.Len(s.T(), msgs, 3)
	require.Equal(s.T(), int64(8), msgs[0].ID)
	require.Equal(s.T(), int64(10), msgs[1].ID)
	require.Equal(s.T(), int64(11), msgs[2].ID)
}

func (s *StoreSuite) TestGetMessagesAroundError() {
	s.mock.ExpectQuery(`SELECT .+ FROM .+ UNION ALL`).
		WithArgs("ch1", int64(5), 25, "ch1", int64(5), 25).
		WillReturnError(sql.ErrConnDone)

	msgs, err := s.store.GetMessagesAround(context.Background(), "ch1", 5, 50)
	require.Error(s.T(), err)
	require.Nil(s.T(), msgs)
}

// --- Timeline pointer tests (peppy-mapping-pudding) ---

func (s *StoreSuite) TestInsertAgentEvent() {
	now := time.Now().UTC()
	evt := &Message{
		ChatID:        1,
		ChannelID:     "ch1",
		MsgID:         "uuid-tu",
		Kind:          MessageKindToolUse,
		ChainPosition: 5,
		ToolUseID:     "toolu_42",
		TriggerMsgID:  "trig-1",
		CreatedAt:     now,
	}
	s.mock.ExpectExec(`INSERT INTO messages`).
		WithArgs(int64(1), "ch1", "uuid-tu", "", "agent", "", 1, 1, "trig-1", now,
			"tool_use", "ch1", "toolu_42", "", 0).
		WillReturnResult(sqlmock.NewResult(123, 1))
	s.mock.ExpectQuery(`SELECT chain_position FROM messages WHERE id = \?`).
		WithArgs(int64(123)).
		WillReturnRows(sqlmock.NewRows([]string{"chain_position"}).AddRow(int64(7)))

	require.NoError(s.T(), s.store.InsertAgentEvent(context.Background(), evt))
	require.Equal(s.T(), int64(123), evt.ID)
	require.Equal(s.T(), int64(7), evt.ChainPosition)
	// Defaults applied for empty fields.
	require.Equal(s.T(), "uuid-tu", evt.MsgID)
	require.Equal(s.T(), "agent", evt.AuthorName)
	require.True(s.T(), evt.IsBot)
	require.True(s.T(), evt.IsProcessed)
}

func (s *StoreSuite) TestInsertAgentEventExecError() {
	anyArgs := []driver.Value{
		sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
		sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
		sqlmock.AnyArg(),
		sqlmock.AnyArg(),
		sqlmock.AnyArg(),
		sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
	}
	s.mock.ExpectExec(`INSERT INTO messages`).WithArgs(anyArgs...).WillReturnError(sql.ErrConnDone)

	err := s.store.InsertAgentEvent(context.Background(), &Message{
		ChannelID: "ch1", MsgID: "uuid-x", Kind: MessageKindThinking, CreatedAt: time.Now().UTC(),
	})
	require.Error(s.T(), err)
}

func (s *StoreSuite) TestInsertAgentEventLastInsertIDError() {
	anyArgs := []driver.Value{
		sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
		sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
		sqlmock.AnyArg(),
		sqlmock.AnyArg(),
		sqlmock.AnyArg(),
		sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
	}
	s.mock.ExpectExec(`INSERT INTO messages`).WithArgs(anyArgs...).WillReturnResult(sqlmock.NewErrorResult(sql.ErrConnDone))

	err := s.store.InsertAgentEvent(context.Background(), &Message{
		ChannelID: "ch1", MsgID: "uuid-x", Kind: MessageKindThinking, CreatedAt: time.Now().UTC(),
	})
	require.Error(s.T(), err)
}

func (s *StoreSuite) TestInsertAgentEventChainPositionReadbackError() {
	anyArgs := []driver.Value{
		sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
		sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
		sqlmock.AnyArg(),
		sqlmock.AnyArg(),
		sqlmock.AnyArg(),
		sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
	}
	s.mock.ExpectExec(`INSERT INTO messages`).WithArgs(anyArgs...).WillReturnResult(sqlmock.NewResult(99, 1))
	s.mock.ExpectQuery(`SELECT chain_position FROM messages WHERE id = \?`).
		WithArgs(int64(99)).
		WillReturnError(sql.ErrConnDone)

	err := s.store.InsertAgentEvent(context.Background(), &Message{
		ChannelID: "ch1", MsgID: "uuid-x", Kind: MessageKindThinking, CreatedAt: time.Now().UTC(),
	})
	require.Error(s.T(), err)
}

func (s *StoreSuite) TestInsertAgentEventGeneratesSyntheticMsgID() {
	fixedTime := time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
	s.store.nowFunc = func() time.Time { return fixedTime }
	expectedMsgID := fmt.Sprintf("evt-%d-%s", fixedTime.UnixNano(), "toolu_synth")

	evt := &Message{
		ChannelID: "ch1",
		Kind:      MessageKindToolResult,
		ToolUseID: "toolu_synth",
		CreatedAt: fixedTime,
	}
	s.mock.ExpectExec(`INSERT INTO messages`).
		WithArgs(int64(0), "ch1", expectedMsgID, "", "agent", "", 1, 1, "", fixedTime,
			"tool_result", "ch1", "toolu_synth", "", 0).
		WillReturnResult(sqlmock.NewResult(55, 1))
	s.mock.ExpectQuery(`SELECT chain_position FROM messages WHERE id = \?`).
		WithArgs(int64(55)).
		WillReturnRows(sqlmock.NewRows([]string{"chain_position"}).AddRow(int64(3)))

	require.NoError(s.T(), s.store.InsertAgentEvent(context.Background(), evt))
	require.Equal(s.T(), expectedMsgID, evt.MsgID)
	require.NoError(s.T(), s.mock.ExpectationsWereMet())
}

func (s *StoreSuite) TestGetTimelineFirstPage() {
	now := time.Now().UTC()
	rows := addMessageRow(newMockMessageRows(), 1, 1, "ch1", "msg1", "u1", "user1", "hello", 0, 0, now)
	rows.AddRow(int64(2), int64(1), "ch1", "uuid-think", "", "agent", "", 1, 1, 0, 0, 0, "", now,
		"thinking", int64(7), "", "", 0, "")

	s.mock.ExpectQuery(`SELECT .+ FROM messages\s+WHERE channel_id = \?\s+ORDER BY chain_position DESC, id DESC LIMIT`).
		WithArgs("ch1", 50).
		WillReturnRows(rows)

	msgs, err := s.store.GetTimeline(context.Background(), "ch1", 0, 0, 50)
	require.NoError(s.T(), err)
	require.Len(s.T(), msgs, 2)
	require.Equal(s.T(), MessageKindMessage, msgs[0].Kind)
	require.Equal(s.T(), MessageKindThinking, msgs[1].Kind)
	require.Equal(s.T(), int64(7), msgs[1].ChainPosition)
	require.Equal(s.T(), "uuid-think", msgs[1].MsgID)
}

func (s *StoreSuite) TestGetTimelineWithCursor() {
	now := time.Now().UTC()
	rows := addMessageRow(newMockMessageRows(), 1, 1, "ch1", "msg1", "u1", "user1", "older", 0, 0, now)
	s.mock.ExpectQuery(`SELECT .+ FROM messages\s+WHERE channel_id = \?\s+AND \(chain_position < \? OR \(chain_position = \? AND id < \?\)\)\s+ORDER BY chain_position DESC, id DESC LIMIT`).
		WithArgs("ch1", int64(5), int64(5), int64(10), 50).
		WillReturnRows(rows)

	msgs, err := s.store.GetTimeline(context.Background(), "ch1", 5, 10, 50)
	require.NoError(s.T(), err)
	require.Len(s.T(), msgs, 1)
}

func (s *StoreSuite) TestGetTimelineError() {
	s.mock.ExpectQuery(`SELECT .+ FROM messages`).
		WithArgs("ch1", 50).
		WillReturnError(sql.ErrConnDone)

	_, err := s.store.GetTimeline(context.Background(), "ch1", 0, 0, 50)
	require.Error(s.T(), err)
}

func (s *StoreSuite) TestGetTimelineWithCursorError() {
	s.mock.ExpectQuery(`SELECT .+ FROM messages`).
		WithArgs("ch1", int64(5), int64(5), int64(10), 50).
		WillReturnError(sql.ErrConnDone)

	_, err := s.store.GetTimeline(context.Background(), "ch1", 5, 10, 50)
	require.Error(s.T(), err)
}

// --- ClaimNextPending tests ---

func (s *StoreSuite) TestClaimNextPending() {
	now := time.Now().UTC()
	rows := addMessageRow(newMockMessageRows(), 42, 1, "ch1", "msg-42", "u1", "user1", "hello", 0, 0, now)
	s.mock.ExpectBegin()
	s.mock.ExpectQuery(`SELECT .+ FROM messages\s+WHERE channel_id = \? AND is_processed = 0 AND is_triggered = 1\s+AND is_running = 0 AND kind = 'message'\s+ORDER BY priority DESC, id ASC LIMIT 1`).
		WithArgs("ch1").
		WillReturnRows(rows)
	s.mock.ExpectExec(`UPDATE messages SET is_running = 1 WHERE id = \?`).
		WithArgs(int64(42)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	s.mock.ExpectCommit()

	msg, err := s.store.ClaimNextPending(context.Background(), "ch1")
	require.NoError(s.T(), err)
	require.NotNil(s.T(), msg)
	require.Equal(s.T(), int64(42), msg.ID)
	require.True(s.T(), msg.IsRunning)
}

func (s *StoreSuite) TestClaimNextPendingNoRows() {
	s.mock.ExpectBegin()
	s.mock.ExpectQuery(`SELECT .+ FROM messages`).
		WithArgs("ch1").
		WillReturnRows(newMockMessageRows())
	s.mock.ExpectCommit()

	msg, err := s.store.ClaimNextPending(context.Background(), "ch1")
	require.NoError(s.T(), err)
	require.Nil(s.T(), msg)
}

func (s *StoreSuite) TestClaimNextPendingScanError() {
	s.mock.ExpectBegin()
	// Wrong-shape row → scan error.
	s.mock.ExpectQuery(`SELECT .+ FROM messages`).
		WithArgs("ch1").
		WillReturnRows(newMockMessageRows().AddRow("not-an-int", 1, "ch1", "m", "", "", "", 0, 0, 0, 0, 0, "", time.Now().UTC(), "message", int64(0), "", "", 0, ""))
	s.mock.ExpectRollback()

	_, err := s.store.ClaimNextPending(context.Background(), "ch1")
	require.Error(s.T(), err)
}

func (s *StoreSuite) TestClaimNextPendingUpdateError() {
	now := time.Now().UTC()
	rows := addMessageRow(newMockMessageRows(), 42, 1, "ch1", "msg-42", "u1", "user1", "hello", 0, 0, now)
	s.mock.ExpectBegin()
	s.mock.ExpectQuery(`SELECT .+ FROM messages`).WithArgs("ch1").WillReturnRows(rows)
	s.mock.ExpectExec(`UPDATE messages SET is_running = 1`).
		WithArgs(int64(42)).
		WillReturnError(sql.ErrConnDone)
	s.mock.ExpectRollback()

	_, err := s.store.ClaimNextPending(context.Background(), "ch1")
	require.Error(s.T(), err)
}

func (s *StoreSuite) TestClaimNextPendingBeginError() {
	s.mock.ExpectBegin().WillReturnError(sql.ErrConnDone)
	_, err := s.store.ClaimNextPending(context.Background(), "ch1")
	require.Error(s.T(), err)
}

// --- ReleaseRunningMessage tests ---

func (s *StoreSuite) TestReleaseRunningMessageProcessed() {
	s.mock.ExpectExec(`UPDATE messages SET is_running = 0, is_processed = CASE WHEN \? = 1 THEN 1 ELSE is_processed END WHERE id = \?`).
		WithArgs(1, int64(42)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(s.T(), s.store.ReleaseRunningMessage(context.Background(), 42, true))
}

func (s *StoreSuite) TestReleaseRunningMessageNotProcessed() {
	s.mock.ExpectExec(`UPDATE messages SET is_running = 0`).
		WithArgs(0, int64(42)).
		WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(s.T(), s.store.ReleaseRunningMessage(context.Background(), 42, false))
}

func (s *StoreSuite) TestReleaseRunningMessageError() {
	s.mock.ExpectExec(`UPDATE messages SET is_running = 0`).
		WithArgs(1, int64(42)).
		WillReturnError(sql.ErrConnDone)

	require.Error(s.T(), s.store.ReleaseRunningMessage(context.Background(), 42, true))
}

// --- ResetStaleRunningMessages tests ---

func (s *StoreSuite) TestResetStaleRunningMessages() {
	s.mock.ExpectBegin()
	s.mock.ExpectQuery(`SELECT channel_id, msg_id FROM messages WHERE is_running = 1`).
		WillReturnRows(sqlmock.NewRows([]string{"channel_id", "msg_id"}).AddRow("ch-1", "msg-a").AddRow("ch-2", "msg-b"))
	s.mock.ExpectExec(`UPDATE messages SET is_running = 0, is_processed = 1 WHERE is_running = 1`).
		WillReturnResult(sqlmock.NewResult(0, 2))
	s.mock.ExpectCommit()

	records, err := s.store.ResetStaleRunningMessages(context.Background())
	require.NoError(s.T(), err)
	require.Equal(s.T(), []StaleRunningMessage{
		{ChannelID: "ch-1", MsgID: "msg-a"},
		{ChannelID: "ch-2", MsgID: "msg-b"},
	}, records)
}

func (s *StoreSuite) TestResetStaleRunningMessagesQueryError() {
	s.mock.ExpectBegin()
	s.mock.ExpectQuery(`SELECT channel_id, msg_id FROM messages WHERE is_running = 1`).
		WillReturnError(sql.ErrConnDone)
	s.mock.ExpectRollback()

	_, err := s.store.ResetStaleRunningMessages(context.Background())
	require.Error(s.T(), err)
}

func (s *StoreSuite) TestResetStaleRunningMessagesScanError() {
	s.mock.ExpectBegin()
	s.mock.ExpectQuery(`SELECT channel_id, msg_id FROM messages WHERE is_running = 1`).
		WillReturnRows(sqlmock.NewRows([]string{"channel_id", "msg_id"}).AddRow(nil, nil))
	s.mock.ExpectRollback()

	_, err := s.store.ResetStaleRunningMessages(context.Background())
	require.Error(s.T(), err)
}

func (s *StoreSuite) TestResetStaleRunningMessagesRowsError() {
	s.mock.ExpectBegin()
	s.mock.ExpectQuery(`SELECT channel_id, msg_id FROM messages WHERE is_running = 1`).
		WillReturnRows(sqlmock.NewRows([]string{"channel_id", "msg_id"}).AddRow("ch-1", "ok").RowError(0, sql.ErrConnDone))
	s.mock.ExpectRollback()

	_, err := s.store.ResetStaleRunningMessages(context.Background())
	require.Error(s.T(), err)
}

func (s *StoreSuite) TestResetStaleRunningMessagesUpdateError() {
	s.mock.ExpectBegin()
	s.mock.ExpectQuery(`SELECT channel_id, msg_id FROM messages WHERE is_running = 1`).
		WillReturnRows(sqlmock.NewRows([]string{"channel_id", "msg_id"}))
	s.mock.ExpectExec(`UPDATE messages SET is_running = 0`).
		WillReturnError(sql.ErrConnDone)
	s.mock.ExpectRollback()

	_, err := s.store.ResetStaleRunningMessages(context.Background())
	require.Error(s.T(), err)
}

// --- MaxQueuedPriority tests ---

func (s *StoreSuite) TestMaxQueuedPriority() {
	s.mock.ExpectQuery(`SELECT MAX\(priority\) FROM messages`).
		WithArgs("ch1").
		WillReturnRows(sqlmock.NewRows([]string{"max"}).AddRow(int64(5)))

	prio, err := s.store.MaxQueuedPriority(context.Background(), "ch1")
	require.NoError(s.T(), err)
	require.Equal(s.T(), 5, prio)
}

func (s *StoreSuite) TestMaxQueuedPriorityEmpty() {
	s.mock.ExpectQuery(`SELECT MAX\(priority\) FROM messages`).
		WithArgs("ch1").
		WillReturnRows(sqlmock.NewRows([]string{"max"}).AddRow(nil))

	prio, err := s.store.MaxQueuedPriority(context.Background(), "ch1")
	require.NoError(s.T(), err)
	require.Equal(s.T(), 0, prio)
}

func (s *StoreSuite) TestMaxQueuedPriorityError() {
	s.mock.ExpectQuery(`SELECT MAX\(priority\) FROM messages`).
		WithArgs("ch1").
		WillReturnError(sql.ErrConnDone)

	_, err := s.store.MaxQueuedPriority(context.Background(), "ch1")
	require.Error(s.T(), err)
}

// --- ListPendingChannels tests ---

func (s *StoreSuite) TestListPendingChannels() {
	s.mock.ExpectQuery(`SELECT DISTINCT channel_id FROM messages`).
		WillReturnRows(sqlmock.NewRows([]string{"channel_id"}).AddRow("ch1").AddRow("ch2"))

	ids, err := s.store.ListPendingChannels(context.Background())
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{"ch1", "ch2"}, ids)
}

func (s *StoreSuite) TestListPendingChannelsQueryError() {
	s.mock.ExpectQuery(`SELECT DISTINCT channel_id FROM messages`).
		WillReturnError(sql.ErrConnDone)

	_, err := s.store.ListPendingChannels(context.Background())
	require.Error(s.T(), err)
}

func (s *StoreSuite) TestListPendingChannelsScanError() {
	s.mock.ExpectQuery(`SELECT DISTINCT channel_id FROM messages`).
		WillReturnRows(sqlmock.NewRows([]string{"channel_id"}).AddRow(nil))

	_, err := s.store.ListPendingChannels(context.Background())
	require.Error(s.T(), err)
}
