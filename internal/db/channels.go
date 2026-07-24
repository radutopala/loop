// channels.go holds SQLiteStore methods for the channels table.
package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/radutopala/loop/internal/types"
)

func (s *SQLiteStore) UpsertChannel(ctx context.Context, ch *Channel) error {
	var permStr string
	if !ch.Permissions.IsEmpty() {
		data, _ := json.Marshal(ch.Permissions) // Permissions is always serializable
		permStr = string(data)
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO channels (channel_id, guild_id, name, dir_path, parent_id, platform, session_id, permissions, active, worktree, base_branch, locked, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(channel_id) DO UPDATE SET
		   guild_id = excluded.guild_id,
		   name = excluded.name,
		   dir_path = CASE WHEN excluded.dir_path != '' THEN excluded.dir_path ELSE channels.dir_path END,
		   parent_id = excluded.parent_id,
		   platform = CASE WHEN excluded.platform != '' THEN excluded.platform ELSE channels.platform END,
		   session_id = CASE WHEN excluded.session_id != '' THEN excluded.session_id ELSE channels.session_id END,
		   permissions = CASE WHEN excluded.permissions != '' THEN excluded.permissions ELSE channels.permissions END,
		   active = excluded.active,
		   worktree = excluded.worktree,
		   base_branch = CASE WHEN excluded.base_branch != '' THEN excluded.base_branch ELSE channels.base_branch END,
		   updated_at = excluded.updated_at`,
		ch.ChannelID, ch.GuildID, ch.Name, ch.DirPath, ch.ParentID, ch.Platform, ch.SessionID, permStr, boolToInt(ch.Active), boolToInt(ch.Worktree), ch.BaseBranch, boolToInt(ch.Locked), s.nowFunc(),
	)
	return err
}

func (s *SQLiteStore) GetChannel(ctx context.Context, channelID string) (*Channel, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, channel_id, guild_id, name, dir_path, parent_id, platform, active, session_id, permissions, worktree, base_branch, locked, model_override, effort_override, fork_pending, created_at, updated_at FROM channels WHERE channel_id = ?`,
		channelID,
	)
	ch, err := scanChannel(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return ch, err
}

func (s *SQLiteStore) GetChannelByDirPath(ctx context.Context, dirPath string, platform types.Platform) (*Channel, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT id, channel_id, guild_id, name, dir_path, parent_id, platform, active, session_id, permissions, worktree, base_branch, locked, model_override, effort_override, fork_pending, created_at, updated_at
		 FROM channels WHERE dir_path = ? AND platform = ? AND parent_id = ''`,
		dirPath, platform,
	)
	ch, err := scanChannel(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return ch, err
}

func (s *SQLiteStore) GetChannelsByDirPath(ctx context.Context, dirPath string) ([]*Channel, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, channel_id, guild_id, name, dir_path, parent_id, platform, active, session_id, permissions, worktree, base_branch, locked, model_override, effort_override, fork_pending, created_at, updated_at
		 FROM channels WHERE dir_path = ? AND parent_id = ''`,
		dirPath,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanChannels(rows)
}

func (s *SQLiteStore) IsChannelActive(ctx context.Context, channelID string) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM channels WHERE channel_id = ? AND active = 1`,
		channelID,
	).Scan(&count)
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

// MarkSessionForkPending sets a fork-created thread's session id together
// with the fork_pending flag: the id is borrowed from the SOURCE thread, so
// the first message must run with --fork-session or it would write into the
// source's conversation. The flag clears on the next UpdateSessionID (every
// run updates the session id, and by then the fork has happened).
func (s *SQLiteStore) MarkSessionForkPending(ctx context.Context, channelID string, sessionID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE channels SET session_id = ?, fork_pending = 1, updated_at = ? WHERE channel_id = ?`,
		sessionID, s.nowFunc(), channelID,
	)
	return err
}

func (s *SQLiteStore) UpdateSessionID(ctx context.Context, channelID string, sessionID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE channels SET session_id = ?, fork_pending = 0, updated_at = ? WHERE channel_id = ?`,
		sessionID, s.nowFunc(), channelID,
	)
	return err
}

// UpdateChannelAgentOverrides sets the per-channel model/effort overrides.
// Empty strings clear the override (inherit from config). Applies to a single
// channel/thread row — threads do not inherit a parent's override.
func (s *SQLiteStore) UpdateChannelAgentOverrides(ctx context.Context, channelID, model, effort string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE channels SET model_override = ?, effort_override = ?, updated_at = ? WHERE channel_id = ?`,
		model, effort, s.nowFunc(), channelID,
	)
	return err
}

func (s *SQLiteStore) UpdateChannelPermissions(ctx context.Context, channelID string, perms types.Permissions) error {
	data, _ := json.Marshal(perms) // Permissions is always serializable
	permStr := string(data)
	now := s.nowFunc()
	// Update the channel and propagate to all child threads
	_, err := s.db.ExecContext(ctx,
		`UPDATE channels SET permissions = ?, updated_at = ? WHERE channel_id = ? OR parent_id = ?`,
		permStr, now, channelID, channelID,
	)
	return err
}

// UpdateChannelLocked flips the locked flag on a single channel/thread row.
// The flag is intentionally not propagated to children: a parent channel's
// lock state is independent from its threads'.
func (s *SQLiteStore) UpdateChannelLocked(ctx context.Context, channelID string, locked bool) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE channels SET locked = ?, updated_at = ? WHERE channel_id = ?`,
		boolToInt(locked), s.nowFunc(), channelID,
	)
	return err
}

// UpdateChannelName sets the name of a channel.
func (s *SQLiteStore) UpdateChannelName(ctx context.Context, channelID, name string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE channels SET name = ?, updated_at = ? WHERE channel_id = ?`,
		name, s.nowFunc(), channelID,
	)
	return err
}

// UpdateChannelDirPath sets the dir_path of a channel.
func (s *SQLiteStore) UpdateChannelDirPath(ctx context.Context, channelID, dirPath string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE channels SET dir_path = ?, updated_at = ? WHERE channel_id = ?`,
		dirPath, s.nowFunc(), channelID,
	)
	return err
}

func (s *SQLiteStore) DeleteChannel(ctx context.Context, channelID string) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM messages WHERE channel_id = ?`, channelID); err != nil {
			return fmt.Errorf("deleting messages for channel: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM quality_snapshots WHERE channel_id = ?`, channelID,
		); err != nil {
			return fmt.Errorf("deleting quality snapshots for channel: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM channels WHERE channel_id = ?`, channelID); err != nil {
			return err
		}
		return nil
	})
}

func (s *SQLiteStore) DeleteChannelsByParentID(ctx context.Context, parentID string) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM messages WHERE channel_id IN (SELECT channel_id FROM channels WHERE parent_id = ?)`, parentID); err != nil {
			return fmt.Errorf("deleting messages for child channels: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM quality_snapshots WHERE channel_id IN (SELECT channel_id FROM channels WHERE parent_id = ?)`, parentID,
		); err != nil {
			return fmt.Errorf("deleting quality snapshots for child channels: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM channels WHERE parent_id = ?`, parentID); err != nil {
			return err
		}
		return nil
	})
}

func (s *SQLiteStore) ListChannelIDsByParentID(ctx context.Context, parentID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT channel_id FROM channels WHERE parent_id = ?`, parentID)
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

func (s *SQLiteStore) ListChannels(ctx context.Context) ([]*Channel, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, channel_id, guild_id, name, dir_path, parent_id, platform, active, session_id, permissions, worktree, base_branch, locked, model_override, effort_override, fork_pending, created_at, updated_at
		 FROM channels ORDER BY name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanChannels(rows)
}
