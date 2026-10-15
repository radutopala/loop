// side_chats.go holds SQLiteStore methods for side chats (see
// ChannelKindSide).
package db

import (
	"context"
)

// InsertSideChat creates side chat ch under ch.ParentID, with ch's session
// id and fork_pending flag: a fork of the parent's session borrows its id
// until its first run. The insert and the check that the parent still
// exists are one statement; a parent deleted meanwhile is ErrParentGone.
func (s *SQLiteStore) InsertSideChat(ctx context.Context, ch *Channel) error {
	now := s.nowFunc()
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO channels (channel_id, guild_id, name, dir_path, parent_id, platform, active, session_id, fork_pending, kind, created_at, updated_at)
		 SELECT ?, ?, ?, ?, ?, ?, 1, ?, ?, ?, ?, ?
		 WHERE EXISTS (SELECT 1 FROM channels WHERE channel_id = ?)`,
		ch.ChannelID, ch.GuildID, ch.Name, ch.DirPath, ch.ParentID, ch.Platform, ch.SessionID, ch.ForkPending, ChannelKindSide, now, now, ch.ParentID,
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

// ListSideChats returns the side chats under parentID, oldest first.
func (s *SQLiteStore) ListSideChats(ctx context.Context, parentID string) ([]*Channel, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+channelColumns+`
		 FROM channels WHERE parent_id = ? AND kind = ? ORDER BY id`,
		parentID, ChannelKindSide,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanChannels(rows)
}
