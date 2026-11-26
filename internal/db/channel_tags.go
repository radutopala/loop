// channel_tags.go holds SQLiteStore methods for channel and thread tags.
package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/radutopala/loop/internal/types"
)

// UpdateChannelTags adds and removes tags on a channel or thread, which must
// already be normalized, and returns its tags after, sorted. A tag in both is
// removed. A tag no channel carries any more is dropped. It returns
// sql.ErrNoRows when there's no such channel, and types.ErrTooManyTags when
// the channel would carry more than types.MaxTags.
func (s *SQLiteStore) UpdateChannelTags(ctx context.Context, channelID string, add, remove []string) ([]string, error) {
	var out []string
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		var id int64
		if err := tx.QueryRowContext(ctx, `SELECT id FROM channels WHERE channel_id = ?`, channelID).Scan(&id); err != nil {
			return err
		}
		for _, tag := range add {
			if slices.Contains(remove, tag) {
				continue
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO tags (name) VALUES (?) ON CONFLICT(name) DO NOTHING`, tag); err != nil {
				return fmt.Errorf("adding tag %s: %w", tag, err)
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT OR IGNORE INTO channel_tags (channel_id, tag_id) SELECT ?, id FROM tags WHERE name = ?`,
				id, tag); err != nil {
				return fmt.Errorf("tagging %s: %w", tag, err)
			}
		}
		for _, tag := range remove {
			if _, err := tx.ExecContext(ctx,
				`DELETE FROM channel_tags WHERE channel_id = ? AND tag_id IN (SELECT id FROM tags WHERE name = ?)`,
				id, tag); err != nil {
				return fmt.Errorf("untagging %s: %w", tag, err)
			}
		}
		var raw string
		if err := tx.QueryRowContext(ctx,
			`SELECT json_group_array(t.name) FROM channel_tags ct JOIN tags t ON t.id = ct.tag_id WHERE ct.channel_id = ?`,
			id).Scan(&raw); err != nil {
			return fmt.Errorf("reading tags: %w", err)
		}
		if out = parseTags(raw); len(out) > types.MaxTags {
			return types.ErrTooManyTags
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM tags WHERE id NOT IN (SELECT tag_id FROM channel_tags)`); err != nil {
			return fmt.Errorf("pruning tags: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE channels SET updated_at = ? WHERE id = ?`, s.nowFunc(), id); err != nil {
			return fmt.Errorf("touching channel: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ListChannelTags returns every tag in use, sorted, with how many channels
// and threads carry it.
func (s *SQLiteStore) ListChannelTags(ctx context.Context) ([]TagCount, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT t.name, COUNT(*) FROM tags t JOIN channel_tags ct ON ct.tag_id = t.id
		 GROUP BY t.id ORDER BY t.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TagCount{}
	for rows.Next() {
		var tc TagCount
		if err := rows.Scan(&tc.Tag, &tc.Count); err != nil {
			return nil, err
		}
		out = append(out, tc)
	}
	return out, rows.Err()
}

// parseTags reads a JSON array of tag names, sorted; nil when it's empty.
func parseTags(raw string) []string {
	var tags []string
	_ = json.Unmarshal([]byte(raw), &tags) // SQLite's json_group_array always yields an array
	if len(tags) == 0 {
		return nil
	}
	slices.Sort(tags)
	return tags
}
