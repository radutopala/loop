// plugins.go holds SQLiteStore methods for installed plugins and their
// per-channel containers.
package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

// Plugin statuses.
const (
	PluginStatusInstalled    = "installed"
	PluginStatusDisabled     = "disabled"
	PluginStatusIncompatible = "incompatible"
)

// Plugin container statuses.
const (
	PluginContainerRunning = "running"
	PluginContainerStopped = "stopped"
	PluginContainerFailed  = "failed"
)

// PluginState is an installed plugin: the image it came from, the digest it
// is pinned to (empty for a dev install, which follows its local build), its
// manifest's version and API, and the capabilities the owner approved.
// VerifiedKeyID names the key its signature verified with, if any.
type PluginState struct {
	PluginID      string
	Image         string
	Digest        string
	Version       string
	API           string
	ApprovedCaps  []string
	Dev           bool
	Status        string
	VerifiedKeyID string
	InstalledAt   time.Time
	UpdatedAt     time.Time
}

// PluginContainer is a plugin's container in one channel and how it last
// went: HostPort is where the daemon reaches it, LastError why it failed.
type PluginContainer struct {
	PluginID    string
	ChannelID   string
	ContainerID string
	HostPort    int
	Status      string
	LastError   string
	UpdatedAt   time.Time
}

const pluginStateColumns = `plugin_id, image, digest, version, api, approved_caps, dev, status, verified_key_id, installed_at, updated_at`

// UpsertPluginState stores st, replacing the plugin's row but keeping the
// time it was first installed.
func (s *SQLiteStore) UpsertPluginState(ctx context.Context, st *PluginState) error {
	caps, _ := json.Marshal(st.ApprovedCaps) // a string slice always marshals
	now := s.nowFunc()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO plugin_state (`+pluginStateColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(plugin_id) DO UPDATE SET
		   image = excluded.image, digest = excluded.digest, version = excluded.version, api = excluded.api,
		   approved_caps = excluded.approved_caps, dev = excluded.dev, status = excluded.status,
		   verified_key_id = excluded.verified_key_id, updated_at = excluded.updated_at`,
		st.PluginID, st.Image, st.Digest, st.Version, st.API, string(caps), st.Dev, st.Status, st.VerifiedKeyID, now, now,
	)
	return err
}

// GetPluginState returns a plugin's state, or nil when it isn't installed.
func (s *SQLiteStore) GetPluginState(ctx context.Context, pluginID string) (*PluginState, error) {
	st, err := scanPluginState(s.db.QueryRowContext(ctx,
		`SELECT `+pluginStateColumns+` FROM plugin_state WHERE plugin_id = ?`, pluginID))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return st, err
}

// ListPluginStates returns every installed plugin, by id.
func (s *SQLiteStore) ListPluginStates(ctx context.Context) ([]*PluginState, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+pluginStateColumns+` FROM plugin_state ORDER BY plugin_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*PluginState
	for rows.Next() {
		st, err := scanPluginState(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// DeletePluginState removes a plugin's state, its containers' rows, its
// badges, its notifications and its scheduled tasks.
func (s *SQLiteStore) DeletePluginState(ctx context.Context, pluginID string) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		for _, q := range []string{
			`DELETE FROM task_run_logs WHERE task_id IN (SELECT id FROM scheduled_tasks WHERE plugin_id = ?)`,
			`DELETE FROM scheduled_tasks WHERE plugin_id = ?`,
			`DELETE FROM plugin_schedules WHERE plugin_id = ?`,
			`DELETE FROM plugin_containers WHERE plugin_id = ?`,
			`DELETE FROM plugin_badges WHERE plugin_id = ?`,
			`DELETE FROM plugin_composer_switches WHERE plugin_id = ?`,
			`DELETE FROM notifications WHERE plugin_id = ?`,
			`DELETE FROM plugin_state WHERE plugin_id = ?`,
		} {
			if _, err := tx.ExecContext(ctx, q, pluginID); err != nil {
				return err
			}
		}
		return nil
	})
}

// PluginSchedule is the scheduled task Loop keeps of a plugin action in a
// channel.
type PluginSchedule struct {
	// Applied is the schedule a project config last asked of it.
	Applied string
	// TaskID is the task's id, 0 once the owner deleted it.
	TaskID int64
}

// GetPluginSchedule returns the scheduled task Loop keeps of the plugin's
// action in channelID, nil when it never made one.
func (s *SQLiteStore) GetPluginSchedule(ctx context.Context, pluginID, channelID, action string) (*PluginSchedule, error) {
	var ps PluginSchedule
	err := s.db.QueryRowContext(ctx,
		`SELECT p.applied, COALESCE((SELECT MIN(t.id) FROM scheduled_tasks t WHERE t.plugin_id = p.plugin_id AND t.channel_id = p.channel_id AND t.plugin_action = p.action), 0)
		 FROM plugin_schedules p WHERE p.plugin_id = ? AND p.channel_id = ? AND p.action = ?`,
		pluginID, channelID, action).Scan(&ps.Applied, &ps.TaskID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &ps, nil
}

// ApplyPluginSchedule makes the scheduled task of task's plugin action in
// its channel what task says, updating the one Loop made or creating it,
// and records applied as the schedule the config asked for.
func (s *SQLiteStore) ApplyPluginSchedule(ctx context.Context, task *ScheduledTask, applied string) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO plugin_schedules (plugin_id, channel_id, action, applied) VALUES (?, ?, ?, ?)
			 ON CONFLICT (plugin_id, channel_id, action) DO UPDATE SET applied = excluded.applied`,
			task.PluginID, task.ChannelID, task.PluginAction, applied); err != nil {
			return err
		}
		now := s.nowFunc()
		res, err := tx.ExecContext(ctx,
			`UPDATE scheduled_tasks SET schedule = ?, type = ?, enabled = ?, next_run_at = ?, plugin_paused = 0, updated_at = ?
			 WHERE plugin_id = ? AND channel_id = ? AND plugin_action = ?`,
			task.Schedule, string(task.Type), boolToInt(task.Enabled), task.NextRunAt, now, task.PluginID, task.ChannelID, task.PluginAction)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil || n > 0 {
			return err
		}
		_, err = insertScheduledTask(ctx, tx, now, task)
		return err
	})
}

// DeletePluginSchedule deletes the scheduled task of the plugin's action in
// channelID, its run logs, and Loop's record of it.
func (s *SQLiteStore) DeletePluginSchedule(ctx context.Context, pluginID, channelID, action string) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		for _, q := range []string{
			`DELETE FROM task_run_logs WHERE task_id IN (SELECT id FROM scheduled_tasks WHERE plugin_id = ? AND channel_id = ? AND plugin_action = ?)`,
			`DELETE FROM scheduled_tasks WHERE plugin_id = ? AND channel_id = ? AND plugin_action = ?`,
			`DELETE FROM plugin_schedules WHERE plugin_id = ? AND channel_id = ? AND action = ?`,
		} {
			if _, err := tx.ExecContext(ctx, q, pluginID, channelID, action); err != nil {
				return err
			}
		}
		return nil
	})
}

// SetPluginTasksPaused turns off the plugin's enabled scheduled tasks in
// channelID, marking them paused, or turns the paused ones back on.
func (s *SQLiteStore) SetPluginTasksPaused(ctx context.Context, pluginID, channelID string, paused bool) error {
	q := `UPDATE scheduled_tasks SET enabled = 1, plugin_paused = 0, updated_at = ? WHERE plugin_id = ? AND channel_id = ? AND plugin_paused = 1`
	if paused {
		q = `UPDATE scheduled_tasks SET enabled = 0, plugin_paused = 1, updated_at = ? WHERE plugin_id = ? AND channel_id = ? AND enabled = 1`
	}
	_, err := s.db.ExecContext(ctx, q, s.nowFunc(), pluginID, channelID)
	return err
}

func scanPluginState(row rowScanner) (*PluginState, error) {
	var st PluginState
	var caps string
	if err := row.Scan(&st.PluginID, &st.Image, &st.Digest, &st.Version, &st.API, &caps, &st.Dev, &st.Status, &st.VerifiedKeyID, &st.InstalledAt, &st.UpdatedAt); err != nil {
		return nil, err
	}
	// Only UpsertPluginState writes the column, always as a JSON array.
	_ = json.Unmarshal([]byte(caps), &st.ApprovedCaps)
	return &st, nil
}

const pluginContainerColumns = `plugin_id, channel_id, container_id, host_port, status, last_error, updated_at`

// UpsertPluginContainer stores c, replacing the row of its plugin and
// channel.
func (s *SQLiteStore) UpsertPluginContainer(ctx context.Context, c *PluginContainer) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO plugin_containers (`+pluginContainerColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(plugin_id, channel_id) DO UPDATE SET
		   container_id = excluded.container_id, host_port = excluded.host_port, status = excluded.status,
		   last_error = excluded.last_error, updated_at = excluded.updated_at`,
		c.PluginID, c.ChannelID, c.ContainerID, c.HostPort, c.Status, c.LastError, s.nowFunc(),
	)
	return err
}

// ListPluginContainers returns a plugin's containers, by channel; an empty
// pluginID returns every plugin's.
func (s *SQLiteStore) ListPluginContainers(ctx context.Context, pluginID string) ([]*PluginContainer, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+pluginContainerColumns+` FROM plugin_containers WHERE ? = '' OR plugin_id = ? ORDER BY plugin_id, channel_id`,
		pluginID, pluginID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*PluginContainer
	for rows.Next() {
		var c PluginContainer
		if err := rows.Scan(&c.PluginID, &c.ChannelID, &c.ContainerID, &c.HostPort, &c.Status, &c.LastError, &c.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, &c)
	}
	return out, rows.Err()
}

// DeletePluginContainer removes a plugin's container row in a channel.
func (s *SQLiteStore) DeletePluginContainer(ctx context.Context, pluginID, channelID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM plugin_containers WHERE plugin_id = ? AND channel_id = ?`, pluginID, channelID)
	return err
}
