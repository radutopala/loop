// walkthroughs.go holds SQLiteStore methods for walkthroughs, the guided
// tours over files an agent shows in the app's editor.
package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// CreateWalkthrough stores w and returns it with its id and creation time.
func (s *SQLiteStore) CreateWalkthrough(ctx context.Context, w *Walkthrough) (*Walkthrough, error) {
	steps, _ := json.Marshal(w.Steps) // plain strings and ints: can't fail
	now := s.nowFunc()
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO walkthroughs (channel_id, title, steps, created_at) VALUES (?, ?, ?, ?)`,
		w.ChannelID, w.Title, string(steps), now,
	)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	out := *w
	out.ID, out.StepCount, out.CreatedAt = id, len(w.Steps), now
	return &out, nil
}

// GetWalkthrough returns the walkthrough id with its steps, or nil when
// there's none.
func (s *SQLiteStore) GetWalkthrough(ctx context.Context, id int64) (*Walkthrough, error) {
	w := &Walkthrough{}
	var steps string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, channel_id, title, steps, created_at FROM walkthroughs WHERE id = ?`, id,
	).Scan(&w.ID, &w.ChannelID, &w.Title, &steps, &w.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(steps), &w.Steps); err != nil {
		return nil, err
	}
	w.StepCount = len(w.Steps)
	return w, nil
}

// ListWalkthroughs returns a channel's walkthroughs, newest first, without
// their steps.
func (s *SQLiteStore) ListWalkthroughs(ctx context.Context, channelID string) ([]*Walkthrough, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, channel_id, title, json_array_length(steps), created_at FROM walkthroughs
		 WHERE channel_id = ? ORDER BY id DESC`, channelID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Walkthrough
	for rows.Next() {
		w := &Walkthrough{}
		if err := rows.Scan(&w.ID, &w.ChannelID, &w.Title, &w.StepCount, &w.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// DeleteWalkthrough deletes the walkthrough id of a channel, reporting
// whether there was one.
func (s *SQLiteStore) DeleteWalkthrough(ctx context.Context, channelID string, id int64) (bool, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM walkthroughs WHERE id = ? AND channel_id = ?`, id, channelID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}
