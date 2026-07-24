// messages.go holds SQLiteStore methods for the messages table.
package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

func (s *SQLiteStore) InsertMessage(ctx context.Context, msg *Message) error {
	// chain_position is assigned atomically as MAX+1 over the channel so the
	// row sorts after every prior chat-or-event row. Single-writer SQLite
	// serialises Exec calls, so this subselect can't race itself.
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO messages (chat_id, channel_id, msg_id, author_id, author_name, content, is_bot, is_processed, is_triggered, priority, mode, trigger_msg_id, not_before, created_at, chain_position)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, COALESCE((SELECT MAX(chain_position) FROM messages WHERE channel_id = ?), 0) + 1)`,
		msg.ChatID, msg.ChannelID, msg.MsgID, msg.AuthorID, msg.AuthorName, msg.Content,
		boolToInt(msg.IsBot), boolToInt(msg.IsProcessed), boolToInt(msg.IsTriggered),
		msg.Priority, msg.Mode, msg.TriggerMsgID, msg.NotBefore, msg.CreatedAt, msg.ChannelID,
	)
	if err != nil {
		return err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return err
	}
	msg.ID = id
	if err := s.db.QueryRowContext(ctx, `SELECT chain_position FROM messages WHERE id = ?`, id).Scan(&msg.ChainPosition); err != nil {
		return err
	}
	return nil
}

func (s *SQLiteStore) MarkMessagesProcessed(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE messages SET is_processed = 1 WHERE id IN (`+strings.Join(placeholders, ",")+`)`,
		args...,
	)
	return err
}

// ClaimNextPending atomically picks the highest-priority pending row for a channel
// and marks it is_running=1 in a single transaction. Eligibility: is_processed=0,
// is_triggered=1, is_running=0, kind='message', and not delayed into the future
// (not_before = 0 or already reached). Order: priority DESC, id ASC.
// Returns nil with no error when the channel has nothing to process.
func (s *SQLiteStore) ClaimNextPending(ctx context.Context, channelID string) (*Message, error) {
	var msg *Message
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		row := tx.QueryRowContext(ctx,
			`SELECT `+messageColumns+` FROM messages
			 WHERE channel_id = ? AND is_processed = 0 AND is_triggered = 1
			   AND is_running = 0 AND kind = 'message'
			   AND (not_before = 0 OR not_before <= strftime('%s','now'))
			 ORDER BY priority DESC, id ASC LIMIT 1`,
			channelID,
		)
		m, err := scanMessageRow(row)
		if err == sql.ErrNoRows {
			return nil
		}
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE messages SET is_running = 1 WHERE id = ?`, m.ID); err != nil {
			return err
		}
		m.IsRunning = true
		msg = m
		return nil
	})
	return msg, err
}

// ChannelsWithDueDelayedMessages returns the distinct channel ids that have at
// least one delayed message (not_before > 0) whose delay has now elapsed and is
// still eligible to run (pending, triggered, not already running). The drain is
// event-driven, so nothing re-triggers a delayed row on its own once the delay
// passes — the orchestrator's delay poller calls this to find channels that
// need a fresh drain. Rows that never carried a delay (not_before = 0) are
// excluded so the poller only ever wakes channels that actually deferred work.
func (s *SQLiteStore) ChannelsWithDueDelayedMessages(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT channel_id FROM messages
		 WHERE not_before > 0 AND not_before <= strftime('%s','now')
		   AND is_processed = 0 AND is_triggered = 1 AND is_running = 0
		   AND kind = 'message'`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// ReleaseRunningMessage clears the is_running flag on a row. When processed=true
// also marks the row is_processed=1 — the normal completion path. processed=false
// leaves the row eligible for re-claim (used when the agent cannot be invoked,
// e.g. row picked up before channel is registered).
func (s *SQLiteStore) ReleaseRunningMessage(ctx context.Context, id int64, processed bool) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE messages SET is_running = 0, is_processed = CASE WHEN ? = 1 THEN 1 ELSE is_processed END WHERE id = ?`,
		boolToInt(processed), id,
	)
	return err
}

// ResetStaleRunningMessages clears is_running=1 left over from a previous daemon
// process (the agent run cannot survive a restart) and marks those rows
// is_processed=1 so chat history doesn't keep showing them as "processing".
// Returns (channel_id, msg_id) pairs for cleared rows so the caller can
// broadcast per-channel messages.processed events. Safe to call at daemon startup.
func (s *SQLiteStore) ResetStaleRunningMessages(ctx context.Context) ([]StaleRunningMessage, error) {
	var records []StaleRunningMessage
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx,
			`SELECT channel_id, msg_id FROM messages WHERE is_running = 1`,
		)
		if err != nil {
			return err
		}
		for rows.Next() {
			var rec StaleRunningMessage
			if err := rows.Scan(&rec.ChannelID, &rec.MsgID); err != nil {
				rows.Close()
				return err
			}
			records = append(records, rec)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
		if _, err := tx.ExecContext(ctx,
			`UPDATE messages SET is_running = 0, is_processed = 1 WHERE is_running = 1`,
		); err != nil {
			return err
		}
		return nil
	})
	return records, err
}

// MaxQueuedPriority returns the highest priority among eligible-but-not-running
// rows for a channel. Used by the interrupt branch to insert a higher-priority
// row that will be claimed ahead of everything else queued.
func (s *SQLiteStore) MaxQueuedPriority(ctx context.Context, channelID string) (int, error) {
	var prio sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		`SELECT MAX(priority) FROM messages
		 WHERE channel_id = ? AND is_processed = 0 AND is_triggered = 1 AND kind = 'message'`,
		channelID,
	).Scan(&prio)
	if err != nil {
		return 0, err
	}
	if !prio.Valid {
		return 0, nil
	}
	return int(prio.Int64), nil
}

// ListPendingChannels returns the set of channel_ids that have at least one
// eligible (is_triggered=1, is_processed=0, is_running=0) pending message row.
// Used at daemon startup to wake processors for channels with queued work.
func (s *SQLiteStore) ListPendingChannels(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT DISTINCT channel_id FROM messages
		 WHERE is_processed = 0 AND is_triggered = 1 AND is_running = 0 AND kind = 'message'`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// DeleteQueuedMessage removes a waiting (not-yet-processed, non-bot) user message
// from the queue. Returns true when a row was deleted, false when no matching row
// exists (already processed, wrong channel, bot message, or never existed).
func (s *SQLiteStore) DeleteQueuedMessage(ctx context.Context, channelID, msgID string) (bool, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM messages WHERE channel_id = ? AND msg_id = ? AND is_bot = 0 AND is_processed = 0 AND kind = 'message'`,
		channelID, msgID,
	)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// ListUserMessageContents returns the contents of the channel's most recent
// user-sent chat messages in chronological order, capped at limit. It backs
// the composer's ArrowUp history, so it deliberately excludes bot rows, agent
// event rows, and system-injected user rows (ask/plan continuations carry an
// empty author_id) — only text the user actually typed comes back.
func (s *SQLiteStore) ListUserMessageContents(ctx context.Context, channelID string, limit int) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT content FROM messages
		 WHERE channel_id = ? AND is_bot = 0 AND kind = 'message' AND author_id != '' AND content != ''
		 ORDER BY id DESC LIMIT ?`,
		channelID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Reverse DESC → chronological so the composer walks backwards naturally.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

func (s *SQLiteStore) GetRecentMessages(ctx context.Context, channelID string, limit int) ([]*Message, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+messageColumns+` FROM messages WHERE channel_id = ? AND kind = 'message' ORDER BY created_at DESC LIMIT ?`,
		channelID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMessages(rows)
}

// ListQueuedUserMessages returns every user message on the channel that is
// still unprocessed, ordered by the same (priority DESC, id ASC) rule the
// processor uses to pick the next row. This is the canonical queue: the FE
// should render it directly rather than filtering its paginated subset, which
// can include stale unprocessed orphans from crashes that the in-memory
// processor will never run.
func (s *SQLiteStore) ListQueuedUserMessages(ctx context.Context, channelID string) ([]*Message, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+messageColumns+` FROM messages WHERE channel_id = ? AND kind = 'message' AND is_bot = 0 AND is_processed = 0 ORDER BY priority DESC, id ASC`,
		channelID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMessages(rows)
}

// ReorderQueuedMessages rewrites priorities so the channel's queued user
// messages sort in the given msg_id order under the (priority DESC, id ASC)
// rule — the first id gets the highest priority. Ids that aren't currently
// queued user messages are simply not matched. Runs in one write transaction.
func (s *SQLiteStore) ReorderQueuedMessages(ctx context.Context, channelID string, orderedMsgIDs []string) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		n := len(orderedMsgIDs)
		for i, msgID := range orderedMsgIDs {
			if _, err := tx.ExecContext(ctx,
				`UPDATE messages SET priority = ? WHERE channel_id = ? AND msg_id = ? AND kind = 'message' AND is_bot = 0 AND is_processed = 0`,
				n-i, channelID, msgID,
			); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *SQLiteStore) GetMessagesCursor(ctx context.Context, channelID string, cursor int64, limit int) ([]*Message, error) {
	var rows *sql.Rows
	var err error
	if cursor > 0 {
		rows, err = s.db.QueryContext(ctx,
			`SELECT `+messageColumns+` FROM messages WHERE channel_id = ? AND kind = 'message' AND id < ? ORDER BY id DESC LIMIT ?`,
			channelID, cursor, limit,
		)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT `+messageColumns+` FROM messages WHERE channel_id = ? AND kind = 'message' ORDER BY id DESC LIMIT ?`,
			channelID, limit,
		)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMessages(rows)
}

func (s *SQLiteStore) SearchMessages(ctx context.Context, query string, limit int) ([]*Message, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+messageColumns+` FROM messages WHERE kind = 'message' AND content LIKE ? ORDER BY created_at DESC LIMIT ?`,
		"%"+query+"%", limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMessages(rows)
}

func (s *SQLiteStore) GetMessagesAround(ctx context.Context, channelID string, messageID int64, limit int) ([]*Message, error) {
	half := limit / 2
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+messageColumns+` FROM (
		   SELECT `+messageColumns+` FROM messages WHERE channel_id = ? AND kind = 'message' AND id < ? ORDER BY id DESC LIMIT ?
		 ) UNION ALL
		 SELECT `+messageColumns+` FROM (
		   SELECT `+messageColumns+` FROM messages WHERE channel_id = ? AND kind = 'message' AND id >= ? ORDER BY id ASC LIMIT ?
		 ) ORDER BY id ASC`,
		channelID, messageID, half,
		channelID, messageID, limit-half,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMessages(rows)
}

// InsertAgentEvent inserts a new agent-event row (kind != "message") into the
// messages table. Caller must populate Kind plus the kind-specific payload
// fields (Content for thinking/tool_result, ToolName/Content for tool_use,
// IsError for tool_result, etc.). chain_position is assigned atomically as
// MAX+1 over the channel so the row sorts after every prior chat-or-event row
// in the same channel. MsgID defaults to a synthetic id; AuthorName defaults
// to "agent". The single-writer SQLite connection serialises Exec calls so
// the MAX+1 subselect cannot race itself.
func (s *SQLiteStore) InsertAgentEvent(ctx context.Context, evt *Message) error {
	if evt.MsgID == "" {
		evt.MsgID = fmt.Sprintf("evt-%d-%s", s.nowFunc().UnixNano(), evt.ToolUseID)
	}
	if evt.AuthorName == "" {
		evt.AuthorName = "agent"
	}
	evt.IsBot = true
	evt.IsProcessed = true
	result, err := s.db.ExecContext(ctx,
		`INSERT INTO messages (chat_id, channel_id, msg_id, author_id, author_name, content, is_bot, is_processed, trigger_msg_id, created_at,
		                       kind, chain_position, tool_use_id, tool_name, is_error)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
		         COALESCE((SELECT MAX(chain_position) FROM messages WHERE channel_id = ?), 0) + 1,
		         ?, ?, ?)`,
		evt.ChatID, evt.ChannelID, evt.MsgID, evt.AuthorID, evt.AuthorName, evt.Content,
		boolToInt(evt.IsBot), boolToInt(evt.IsProcessed), evt.TriggerMsgID, evt.CreatedAt,
		string(evt.Kind),
		evt.ChannelID,
		evt.ToolUseID, evt.ToolName, boolToInt(evt.IsError),
	)
	if err != nil {
		return err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return err
	}
	evt.ID = id
	if err := s.db.QueryRowContext(ctx, `SELECT chain_position FROM messages WHERE id = ?`, id).Scan(&evt.ChainPosition); err != nil {
		return err
	}
	return nil
}

// GetTimeline returns a page of timeline rows for a channel — both real messages
// and agent events — ordered by (chain_position DESC, id DESC). Legacy rows
// (chain_position=0) sort by id, matching today's chat-list behaviour.
//
// Cursor semantics: pass cursorPosition=0 + cursorID=0 for the first page; for
// subsequent pages, pass the (chain_position, id) of the last item from the
// previous page so the next page picks up strictly older rows.
func (s *SQLiteStore) GetTimeline(ctx context.Context, channelID string, cursorPosition, cursorID int64, limit int) ([]*Message, error) {
	var rows *sql.Rows
	var err error
	if cursorPosition > 0 || cursorID > 0 {
		rows, err = s.db.QueryContext(ctx,
			`SELECT `+messageColumns+` FROM messages
			 WHERE channel_id = ?
			   AND (chain_position < ? OR (chain_position = ? AND id < ?))
			 ORDER BY chain_position DESC, id DESC LIMIT ?`,
			channelID, cursorPosition, cursorPosition, cursorID, limit,
		)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT `+messageColumns+` FROM messages
			 WHERE channel_id = ?
			 ORDER BY chain_position DESC, id DESC LIMIT ?`,
			channelID, limit,
		)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMessages(rows)
}
