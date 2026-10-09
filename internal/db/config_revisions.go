// config_revisions.go holds SQLiteStore methods for the history of the
// config files Loop reads.
package db

import (
	"context"
	"database/sql"
)

const configRevisionColumns = `id, path, content, hash, source, created_at`

// InsertConfigRevision stores rev as its path's newest revision, unless the
// newest one already has rev's hash, then drops all but the path's newest
// keep revisions. An external revision of a path with no history is stored
// as initial: it's the content Loop first saw, not an edit. Reports whether
// rev was stored.
func (s *SQLiteStore) InsertConfigRevision(ctx context.Context, rev *ConfigRevision, keep int) (bool, error) {
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO config_revisions (path, content, hash, source, created_at)
		 SELECT ?, ?, ?,
		        CASE WHEN ? = ? AND NOT EXISTS (SELECT 1 FROM config_revisions WHERE path = ?) THEN ? ELSE ? END,
		        ?
		 WHERE COALESCE((SELECT hash FROM config_revisions WHERE path = ? ORDER BY id DESC LIMIT 1), '') != ?`,
		rev.Path, rev.Content, rev.Hash,
		rev.Source, ConfigSourceExternal, rev.Path, ConfigSourceInitial, rev.Source,
		s.nowFunc(),
		rev.Path, rev.Hash,
	)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if n == 0 {
		return false, nil
	}
	_, err = s.db.ExecContext(ctx,
		`DELETE FROM config_revisions WHERE path = ? AND id NOT IN
		 (SELECT id FROM config_revisions WHERE path = ? ORDER BY id DESC LIMIT ?)`,
		rev.Path, rev.Path, keep,
	)
	return true, err
}

// ListConfigRevisions returns path's revisions, newest first.
func (s *SQLiteStore) ListConfigRevisions(ctx context.Context, path string) ([]*ConfigRevision, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+configRevisionColumns+` FROM config_revisions WHERE path = ? ORDER BY id DESC`, path)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ConfigRevision
	for rows.Next() {
		rev, err := scanConfigRevision(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rev)
	}
	return out, rows.Err()
}

// GetConfigRevision returns the revision with the given id, or nil when
// there's none.
func (s *SQLiteStore) GetConfigRevision(ctx context.Context, id int64) (*ConfigRevision, error) {
	rev, err := scanConfigRevision(s.db.QueryRowContext(ctx,
		`SELECT `+configRevisionColumns+` FROM config_revisions WHERE id = ?`, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return rev, err
}

func scanConfigRevision(row rowScanner) (*ConfigRevision, error) {
	var rev ConfigRevision
	if err := row.Scan(&rev.ID, &rev.Path, &rev.Content, &rev.Hash, &rev.Source, &rev.CreatedAt); err != nil {
		return nil, err
	}
	return &rev, nil
}
