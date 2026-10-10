// api_tokens.go holds SQLiteStore methods for agent and plugin containers'
// API credentials. Only a token's hash is stored, so the table never holds a
// usable credential.
package db

import (
	"context"
	"time"
)

// APIToken is a container's API credential: the hash of its token and the
// container and channel it was issued to. Kind is who holds it, "agent" or
// "plugin"; PluginID names a plugin token's plugin.
type APIToken struct {
	Hash        string
	Kind        string
	PluginID    string
	ContainerID string
	ChannelID   string
	DirPath     string
	CreatedAt   time.Time
}

// InsertAPIToken stores an issued token.
func (s *SQLiteStore) InsertAPIToken(ctx context.Context, t *APIToken) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO api_tokens (token_hash, kind, plugin_id, container_id, channel_id, dir_path, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		t.Hash, t.Kind, t.PluginID, t.ContainerID, t.ChannelID, t.DirPath, s.nowFunc(),
	)
	return err
}

// DeleteAPITokens removes the tokens issued to a container.
func (s *SQLiteStore) DeleteAPITokens(ctx context.Context, containerID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM api_tokens WHERE container_id = ?`, containerID)
	return err
}

// ListAPITokens returns every stored token.
func (s *SQLiteStore) ListAPITokens(ctx context.Context) ([]*APIToken, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT token_hash, kind, plugin_id, container_id, channel_id, dir_path, created_at FROM api_tokens ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*APIToken
	for rows.Next() {
		t := &APIToken{}
		if err := rows.Scan(&t.Hash, &t.Kind, &t.PluginID, &t.ContainerID, &t.ChannelID, &t.DirPath, &t.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
