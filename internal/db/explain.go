// explain.go holds SQLiteStore methods for explanations: the per-channel
// switch, the turns they explain and the write-ups themselves.
package db

import (
	"context"
	"database/sql"
	"time"
)

// UpdateChannelExplainOverride sets a channel's explain switch: LearnOn
// ("on"), LearnOff ("off") or empty to inherit the config.
func (s *SQLiteStore) UpdateChannelExplainOverride(ctx context.Context, channelID, value string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE channels SET explain_override = ?, updated_at = ? WHERE channel_id = ?`,
		value, s.nowFunc(), channelID,
	)
	return err
}

// GetChatMessage returns the chat message msgID in a channel, bot or user,
// or nil when there's none.
func (s *SQLiteStore) GetChatMessage(ctx context.Context, channelID, msgID string) (*Message, error) {
	return s.queryMessage(ctx,
		`SELECT `+messageColumns+` FROM messages
		 WHERE channel_id = ? AND msg_id = ? AND kind = 'message' ORDER BY id DESC LIMIT 1`,
		channelID, msgID,
	)
}

// LastBotMessage returns the last bot message of the turn triggerMsgID
// started in a channel, or nil when it has none.
func (s *SQLiteStore) LastBotMessage(ctx context.Context, channelID, triggerMsgID string) (*Message, error) {
	return s.queryMessage(ctx,
		`SELECT `+messageColumns+` FROM messages
		 WHERE channel_id = ? AND trigger_msg_id = ? AND is_bot = 1 AND kind = 'message' ORDER BY id DESC LIMIT 1`,
		channelID, triggerMsgID,
	)
}

// queryMessage returns the one message query selects, or nil when there's
// none.
func (s *SQLiteStore) queryMessage(ctx context.Context, query string, args ...any) (*Message, error) {
	m, err := scanMessageRow(s.db.QueryRowContext(ctx, query, args...))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return m, err
}

// explainStale is how long an explanation may sit queued or running before
// it can be queued again. One that old lost its run (its trigger was
// dropped) and would otherwise never be explained again.
const explainStale = time.Hour

// QueueExplanation queues an explanation of e's turn, run by the message
// e.TriggerMsgID in explain thread e.ExplainChannelID: it creates it, or
// resets a done, failed or stale one to queued with its content cleared.
// It returns the explanation as stored and whether it was queued; an
// explanation already queued or running is left alone.
func (s *SQLiteStore) QueueExplanation(ctx context.Context, e *Explanation) (*Explanation, bool, error) {
	now := s.nowFunc()
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO explanations (channel_id, message_id, explain_channel_id, trigger_msg_id, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(channel_id, message_id) DO UPDATE SET
		   explain_channel_id = excluded.explain_channel_id, trigger_msg_id = excluded.trigger_msg_id,
		   status = excluded.status, content = '', error = '', updated_at = excluded.updated_at
		 WHERE explanations.status NOT IN (?, ?) OR explanations.updated_at < ?`,
		e.ChannelID, e.MessageID, e.ExplainChannelID, e.TriggerMsgID, ExplainQueued, now, now,
		ExplainQueued, ExplainRunning, now.Add(-explainStale),
	)
	if err != nil {
		return nil, false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, false, err
	}
	got, err := s.GetExplanation(ctx, e.ChannelID, e.MessageID)
	if err != nil {
		return nil, false, err
	}
	return got, n == 1, nil
}

const explanationColumns = `id, channel_id, message_id, explain_channel_id, trigger_msg_id, status, content, error, created_at, updated_at`

// GetExplanation returns the explanation of messageID's turn in a channel,
// or nil when it has none.
func (s *SQLiteStore) GetExplanation(ctx context.Context, channelID, messageID string) (*Explanation, error) {
	return s.queryExplanation(ctx,
		`SELECT `+explanationColumns+` FROM explanations WHERE channel_id = ? AND message_id = ?`,
		channelID, messageID,
	)
}

// GetExplanationByTrigger returns the explanation whose run the message
// triggerMsgID started in explain thread explainChannelID, or nil when
// there's none.
func (s *SQLiteStore) GetExplanationByTrigger(ctx context.Context, explainChannelID, triggerMsgID string) (*Explanation, error) {
	return s.queryExplanation(ctx,
		`SELECT `+explanationColumns+` FROM explanations WHERE explain_channel_id = ? AND trigger_msg_id = ?`,
		explainChannelID, triggerMsgID,
	)
}

func (s *SQLiteStore) queryExplanation(ctx context.Context, query string, args ...any) (*Explanation, error) {
	e := &Explanation{}
	err := s.db.QueryRowContext(ctx, query, args...).Scan(&e.ID, &e.ChannelID, &e.MessageID, &e.ExplainChannelID,
		&e.TriggerMsgID, &e.Status, &e.Content, &e.Error, &e.CreatedAt, &e.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return e, nil
}

// UpdateExplanation sets an explanation's status, content and error text.
func (s *SQLiteStore) UpdateExplanation(ctx context.Context, id int64, status, content, errText string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE explanations SET status = ?, content = ?, error = ?, updated_at = ? WHERE id = ?`,
		status, content, errText, s.nowFunc(), id,
	)
	return err
}

// explainSnippet is how many characters of the explained turn's prompt and
// reply ListExplanations returns.
const explainSnippet = 300

// ExplainSnippet cuts text to the start ListExplanations returns of an
// explained turn's prompt or reply.
func ExplainSnippet(text string) string {
	r := []rune(text)
	if len(r) <= explainSnippet {
		return text
	}
	return string(r[:explainSnippet])
}

// ListExplanations returns a channel's explanations, newest first, each
// with the explained turn's bot message row id and the start of its prompt
// and reply.
func (s *SQLiteStore) ListExplanations(ctx context.Context, channelID string) ([]*Explanation, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT e.id, e.channel_id, e.message_id, e.explain_channel_id, e.trigger_msg_id, e.status, e.content, e.error, e.created_at, e.updated_at,
		        COALESCE(b.id, 0), COALESCE(substr(p.content, 1, ?), ''), COALESCE(substr(b.content, 1, ?), '')
		 FROM explanations e
		 LEFT JOIN messages b ON b.id = (SELECT id FROM messages WHERE channel_id = e.channel_id AND msg_id = e.message_id AND kind = 'message' ORDER BY id DESC LIMIT 1)
		 LEFT JOIN messages p ON p.id = (SELECT id FROM messages WHERE channel_id = e.channel_id AND msg_id = b.trigger_msg_id AND b.trigger_msg_id != '' AND kind = 'message' ORDER BY id LIMIT 1)
		 WHERE e.channel_id = ?
		 ORDER BY e.created_at DESC, e.id DESC`,
		explainSnippet, explainSnippet, channelID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Explanation
	for rows.Next() {
		e := &Explanation{}
		if err := rows.Scan(&e.ID, &e.ChannelID, &e.MessageID, &e.ExplainChannelID, &e.TriggerMsgID, &e.Status,
			&e.Content, &e.Error, &e.CreatedAt, &e.UpdatedAt, &e.MessageRowID, &e.Prompt, &e.Reply); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// FailInterruptedExplanations fails the queued and running explanations
// whose trigger no longer waits to run: Loop stopped mid-run, or the
// trigger was dropped. Called at startup after ResetStaleRunningMessages,
// so explanations still queued there resume with their thread. It returns
// how many it failed.
func (s *SQLiteStore) FailInterruptedExplanations(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE explanations SET status = ?, error = ?, updated_at = ?
		 WHERE status IN (?, ?) AND NOT EXISTS (
		   SELECT 1 FROM messages m WHERE m.channel_id = explanations.explain_channel_id
		     AND m.msg_id = explanations.trigger_msg_id AND m.kind = 'message' AND m.is_processed = 0)`,
		ExplainFailed, "Loop stopped before the explanation finished", s.nowFunc(), ExplainQueued, ExplainRunning,
	)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
