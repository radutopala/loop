package db

import (
	"context"
	"time"
)

// PluginSwitch is the state of a switch a plugin puts in a chat's composer:
// whether it's on, what the plugin says of it (Status, "" or one of busy, ok
// and error) and its tooltip.
type PluginSwitch struct {
	PluginID  string
	ChannelID string
	SwitchID  string
	On        bool
	Status    string
	Title     string
	UpdatedAt time.Time
}

// SetPluginSwitch stores sw, replacing that switch's state in its channel.
func (s *SQLiteStore) SetPluginSwitch(ctx context.Context, sw *PluginSwitch) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO plugin_composer_switches (plugin_id, channel_id, switch_id, is_on, status, title, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(plugin_id, channel_id, switch_id) DO UPDATE SET
		   is_on = excluded.is_on, status = excluded.status, title = excluded.title, updated_at = excluded.updated_at`,
		sw.PluginID, sw.ChannelID, sw.SwitchID, sw.On, sw.Status, sw.Title, s.nowFunc(),
	)
	return err
}

// DeletePluginSwitch forgets a switch's state in a channel.
func (s *SQLiteStore) DeletePluginSwitch(ctx context.Context, pluginID, channelID, switchID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM plugin_composer_switches WHERE plugin_id = ? AND channel_id = ? AND switch_id = ?`, pluginID, channelID, switchID)
	return err
}

// ListPluginSwitches returns every switch's state, by channel, plugin then
// switch.
func (s *SQLiteStore) ListPluginSwitches(ctx context.Context) ([]*PluginSwitch, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT plugin_id, channel_id, switch_id, is_on, status, title, updated_at FROM plugin_composer_switches ORDER BY channel_id, plugin_id, switch_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*PluginSwitch
	for rows.Next() {
		var sw PluginSwitch
		if err := rows.Scan(&sw.PluginID, &sw.ChannelID, &sw.SwitchID, &sw.On, &sw.Status, &sw.Title, &sw.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, &sw)
	}
	return out, rows.Err()
}
