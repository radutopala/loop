package db

import (
	"context"
	"database/sql"
	"time"
)

// NotificationsKept is how many notifications Loop, and each plugin, keeps;
// a newer one drops the oldest of its source.
const NotificationsKept = 200

// Notification kinds.
const (
	// NotificationRun is an agent run that ended unattended or failed.
	NotificationRun = "run"
	// NotificationWorkflow is a workflow run that ended or paused.
	NotificationWorkflow = "workflow"
	// NotificationNotify is a plugin's notification.
	NotificationNotify = "notify"
	// NotificationCard is a plugin's card: a page of its bundle drawn with
	// the data it was posted with.
	NotificationCard = "card"
)

// Notification is something Loop or a plugin tells the owner, shown in the
// app's notification drawer. PluginID is empty for Loop's own; ChannelID,
// when set, is the channel or thread it's about. Card and Data are a
// card's id and its JSON data.
type Notification struct {
	ID        int64
	PluginID  string
	Kind      string
	ChannelID string
	Title     string
	Body      string
	Card      string
	Data      string
	Read      bool
	CreatedAt time.Time
}

// InsertNotification stores n, setting its ID and time, and drops its
// source's notifications past the newest NotificationsKept.
func (s *SQLiteStore) InsertNotification(ctx context.Context, n *Notification) error {
	if n.Data == "" {
		n.Data = "null"
	}
	return s.withTx(ctx, func(tx *sql.Tx) error {
		n.CreatedAt = s.nowFunc()
		res, err := tx.ExecContext(ctx,
			`INSERT INTO notifications (plugin_id, kind, channel_id, title, body, card, data, read, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, 0, ?)`,
			n.PluginID, n.Kind, n.ChannelID, n.Title, n.Body, n.Card, n.Data, n.CreatedAt,
		)
		if err != nil {
			return err
		}
		if n.ID, err = res.LastInsertId(); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx,
			`DELETE FROM notifications WHERE plugin_id = ? AND id NOT IN
			   (SELECT id FROM notifications WHERE plugin_id = ? ORDER BY id DESC LIMIT ?)`,
			n.PluginID, n.PluginID, NotificationsKept,
		)
		return err
	})
}

// ListNotifications returns the newest notifications, up to limit.
func (s *SQLiteStore) ListNotifications(ctx context.Context, limit int) ([]*Notification, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, plugin_id, kind, channel_id, title, body, card, data, read, created_at FROM notifications ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Notification
	for rows.Next() {
		var n Notification
		if err := rows.Scan(&n.ID, &n.PluginID, &n.Kind, &n.ChannelID, &n.Title, &n.Body, &n.Card, &n.Data, &n.Read, &n.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &n)
	}
	return out, rows.Err()
}

// MarkNotificationsRead marks the notifications up to id read.
func (s *SQLiteStore) MarkNotificationsRead(ctx context.Context, upTo int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE notifications SET read = 1 WHERE id <= ? AND read = 0`, upTo)
	return err
}

// DeleteNotification removes a notification, reporting whether it was
// there.
func (s *SQLiteStore) DeleteNotification(ctx context.Context, id int64) (bool, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM notifications WHERE id = ?`, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// DeleteNotifications removes every notification.
func (s *SQLiteStore) DeleteNotifications(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM notifications`)
	return err
}

// DeleteChannelNotifications removes a channel's notifications, reporting
// whether it had any.
func (s *SQLiteStore) DeleteChannelNotifications(ctx context.Context, channelID string) (bool, error) {
	res, err := s.db.ExecContext(ctx, `DELETE FROM notifications WHERE channel_id = ?`, channelID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}
