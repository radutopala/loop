package db

import (
	"context"
	"time"
)

// PluginBadge is the short label a plugin shows on a channel's sidebar row;
// Title is its tooltip.
type PluginBadge struct {
	PluginID  string
	ChannelID string
	Text      string
	Title     string
	UpdatedAt time.Time
}

// SetPluginBadge stores b, replacing its plugin's badge in its channel.
func (s *SQLiteStore) SetPluginBadge(ctx context.Context, b *PluginBadge) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO plugin_badges (plugin_id, channel_id, text, title, updated_at) VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(plugin_id, channel_id) DO UPDATE SET
		   text = excluded.text, title = excluded.title, updated_at = excluded.updated_at`,
		b.PluginID, b.ChannelID, b.Text, b.Title, s.nowFunc(),
	)
	return err
}

// DeletePluginBadge removes a plugin's badge in a channel.
func (s *SQLiteStore) DeletePluginBadge(ctx context.Context, pluginID, channelID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM plugin_badges WHERE plugin_id = ? AND channel_id = ?`, pluginID, channelID)
	return err
}

// ListPluginBadges returns every badge, by channel then plugin.
func (s *SQLiteStore) ListPluginBadges(ctx context.Context) ([]*PluginBadge, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT plugin_id, channel_id, text, title, updated_at FROM plugin_badges ORDER BY channel_id, plugin_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*PluginBadge
	for rows.Next() {
		var b PluginBadge
		if err := rows.Scan(&b.PluginID, &b.ChannelID, &b.Text, &b.Title, &b.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, &b)
	}
	return out, rows.Err()
}
