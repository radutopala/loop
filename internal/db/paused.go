// paused.go holds SQLiteStore methods for the paused_channels table — the
// persisted mirror of the orchestrator's parked ask/plan card state.
package db

import "context"

// UpsertPausedChannel records that a channel is parked on an ask/plan card.
// One row per (channel, kind); a re-park overwrites the previous payload.
func (s *SQLiteStore) UpsertPausedChannel(ctx context.Context, p *PausedChannel) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO paused_channels (channel_id, kind, mode, data, created_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(channel_id, kind) DO UPDATE SET
		   mode = excluded.mode,
		   data = excluded.data,
		   created_at = excluded.created_at`,
		p.ChannelID, p.Kind, p.Mode, p.Data, s.nowFunc(),
	)
	return err
}

// DeletePausedChannel removes a channel's park for the given kind. A no-op
// when the row doesn't exist.
func (s *SQLiteStore) DeletePausedChannel(ctx context.Context, channelID, kind string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM paused_channels WHERE channel_id = ? AND kind = ?`,
		channelID, kind,
	)
	return err
}

// ListPausedChannels returns every persisted park. Used at daemon startup to
// restore the orchestrator's in-memory parked state.
func (s *SQLiteStore) ListPausedChannels(ctx context.Context) ([]*PausedChannel, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT channel_id, kind, mode, data FROM paused_channels`,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []*PausedChannel
	for rows.Next() {
		p := &PausedChannel{}
		if err := rows.Scan(&p.ChannelID, &p.Kind, &p.Mode, &p.Data); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
