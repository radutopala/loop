// hidden_threads.go holds SQLiteStore methods for the hidden threads a
// channel's learn passes and explanations run in.
package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// GetHiddenThread returns the hidden thread of the given kind under
// parentID, or nil when it has none yet.
func (s *SQLiteStore) GetHiddenThread(ctx context.Context, parentID, kind string) (*Channel, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT `+channelColumns+`
		 FROM channels WHERE parent_id = ? AND kind = ? ORDER BY id LIMIT 1`,
		parentID, kind,
	)
	ch, err := scanChannel(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return ch, err
}

// ErrParentGone is returned by InsertHiddenThread when the hidden thread's
// parent no longer exists.
var ErrParentGone = errors.New("hidden thread parent is gone")

// InsertHiddenThread creates a hidden thread of ch.Kind, which must be a
// hidden kind (see IsHiddenKind). The insert and the check that its parent
// still exists are one statement, so a parent deleted meanwhile can't be
// left with an orphan hidden thread; that's ErrParentGone.
func (s *SQLiteStore) InsertHiddenThread(ctx context.Context, ch *Channel) error {
	if !IsHiddenKind(ch.Kind) {
		return fmt.Errorf("%q is not a hidden thread kind", ch.Kind)
	}
	now := s.nowFunc()
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO channels (channel_id, guild_id, name, dir_path, parent_id, platform, active, kind, created_at, updated_at)
		 SELECT ?, ?, ?, ?, ?, ?, 1, ?, ?, ?
		 WHERE EXISTS (SELECT 1 FROM channels WHERE channel_id = ?)`,
		ch.ChannelID, ch.GuildID, ch.Name, ch.DirPath, ch.ParentID, ch.Platform, ch.Kind, now, now, ch.ParentID,
	)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrParentGone
	}
	return nil
}

// ListHiddenThreads returns the hidden threads under parentID, of every
// kind.
func (s *SQLiteStore) ListHiddenThreads(ctx context.Context, parentID string) ([]*Channel, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+channelColumns+`
		 FROM channels WHERE parent_id = ? AND kind IN (`+hiddenKinds+`) ORDER BY id`,
		parentID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanChannels(rows)
}
