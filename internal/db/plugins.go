// plugins.go holds SQLiteStore methods for installed plugins and their
// per-channel containers.
package db

import (
	"context"
	"database/sql"
	"encoding/json"
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

// DeletePluginState removes a plugin's state and its containers' rows.
func (s *SQLiteStore) DeletePluginState(ctx context.Context, pluginID string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM plugin_containers WHERE plugin_id = ?`, pluginID); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM plugin_state WHERE plugin_id = ?`, pluginID)
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
