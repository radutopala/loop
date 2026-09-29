// learn_passes.go holds SQLiteStore methods for learn passes: which turn
// each one reviews and how far it got.
package db

import (
	"context"
	"database/sql"
)

// InsertLearnPass stores p as a queued pass and returns it as stored, with
// its id, status and times filled in.
func (s *SQLiteStore) InsertLearnPass(ctx context.Context, p *LearnPass) (*LearnPass, error) {
	now := s.nowFunc()
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO learn_passes (channel_id, message_id, learn_channel_id, trigger_msg_id, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		p.ChannelID, p.MessageID, p.LearnChannelID, p.TriggerMsgID, LearnPassQueued, now, now,
	)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	got := *p
	got.ID, got.Status, got.Error, got.CreatedAt, got.UpdatedAt = id, LearnPassQueued, "", now, now
	return &got, nil
}

const learnPassColumns = `id, channel_id, message_id, learn_channel_id, trigger_msg_id, status, error, created_at, updated_at`

// GetLearnPassByTrigger returns the pass whose run the message triggerMsgID
// started in learn thread learnChannelID, or nil when there's none.
func (s *SQLiteStore) GetLearnPassByTrigger(ctx context.Context, learnChannelID, triggerMsgID string) (*LearnPass, error) {
	return s.queryLearnPass(ctx,
		`SELECT `+learnPassColumns+` FROM learn_passes WHERE learn_channel_id = ? AND trigger_msg_id = ? ORDER BY id DESC LIMIT 1`,
		learnChannelID, triggerMsgID,
	)
}

// LatestLearnPass returns the pass proposals filed in learn thread
// learnChannelID belong to: the running one, else the newest done one, or
// nil when there's neither. Proposals are filed while a pass runs, or later
// by a user's reply in the learn thread.
func (s *SQLiteStore) LatestLearnPass(ctx context.Context, learnChannelID string) (*LearnPass, error) {
	return s.queryLearnPass(ctx,
		`SELECT `+learnPassColumns+` FROM learn_passes WHERE learn_channel_id = ? AND status IN (?, ?)
		 ORDER BY status = ? DESC, id DESC LIMIT 1`,
		learnChannelID, LearnPassRunning, LearnPassDone, LearnPassRunning,
	)
}

// ActiveLearnPass returns the newest queued or running pass over the turn
// that ended with bot message messageID in channel channelID, or nil when
// there's none.
func (s *SQLiteStore) ActiveLearnPass(ctx context.Context, channelID, messageID string) (*LearnPass, error) {
	return s.queryLearnPass(ctx,
		`SELECT `+learnPassColumns+` FROM learn_passes WHERE channel_id = ? AND message_id = ? AND status IN (?, ?)
		 ORDER BY id DESC LIMIT 1`,
		channelID, messageID, LearnPassQueued, LearnPassRunning,
	)
}

// LearnPassRunning reports whether a pass is running in learn thread
// learnChannelID. A user's reply running there isn't a pass, nor is a pass
// still queued.
func (s *SQLiteStore) LearnPassRunning(ctx context.Context, learnChannelID string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM learn_passes WHERE learn_channel_id = ? AND status = ?`,
		learnChannelID, LearnPassRunning,
	).Scan(&n)
	return n > 0, err
}

func (s *SQLiteStore) queryLearnPass(ctx context.Context, query string, args ...any) (*LearnPass, error) {
	p := &LearnPass{}
	err := s.db.QueryRowContext(ctx, query, args...).Scan(&p.ID, &p.ChannelID, &p.MessageID, &p.LearnChannelID,
		&p.TriggerMsgID, &p.Status, &p.Error, &p.CreatedAt, &p.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return p, nil
}

// UpdateLearnPass sets a pass's status and error text.
func (s *SQLiteStore) UpdateLearnPass(ctx context.Context, id int64, status, errText string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE learn_passes SET status = ?, error = ?, updated_at = ? WHERE id = ?`,
		status, errText, s.nowFunc(), id,
	)
	return err
}

// ListLearnPasses returns a channel's learn passes, newest first, each with
// the reviewed turn's bot message row id.
func (s *SQLiteStore) ListLearnPasses(ctx context.Context, channelID string) ([]*LearnPass, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT p.id, p.channel_id, p.message_id, p.learn_channel_id, p.trigger_msg_id, p.status, p.error, p.created_at, p.updated_at,
		        COALESCE((SELECT id FROM messages WHERE channel_id = p.channel_id AND msg_id = p.message_id AND kind = 'message' ORDER BY id DESC LIMIT 1), 0)
		 FROM learn_passes p
		 WHERE p.channel_id = ?
		 ORDER BY p.id DESC`,
		channelID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*LearnPass
	for rows.Next() {
		p := &LearnPass{}
		if err := rows.Scan(&p.ID, &p.ChannelID, &p.MessageID, &p.LearnChannelID, &p.TriggerMsgID, &p.Status,
			&p.Error, &p.CreatedAt, &p.UpdatedAt, &p.MessageRowID); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// FailInterruptedLearnPasses fails the queued and running passes whose
// trigger no longer waits to run: Loop stopped mid-run, or the trigger was
// dropped. Called at startup after ResetStaleRunningMessages, so passes
// still queued there resume with their thread. It returns how many it
// failed.
func (s *SQLiteStore) FailInterruptedLearnPasses(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`UPDATE learn_passes SET status = ?, error = ?, updated_at = ?
		 WHERE status IN (?, ?) AND NOT EXISTS (
		   SELECT 1 FROM messages m WHERE m.channel_id = learn_passes.learn_channel_id
		     AND m.msg_id = learn_passes.trigger_msg_id AND m.kind = 'message' AND m.is_processed = 0)`,
		LearnPassFailed, "Loop stopped before the learn pass finished", s.nowFunc(), LearnPassQueued, LearnPassRunning,
	)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
