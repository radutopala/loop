package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/events"
)

// EventNotificationsChanged tells the app the notifications changed: one
// was added, read or removed.
const EventNotificationsChanged = "notifications.changed"

// notificationsShown is how many of a channel's notifications the drawer
// lists.
const notificationsShown = 200

// NotificationStore keeps what Loop and the plugins tell the owner.
type NotificationStore interface {
	InsertNotification(ctx context.Context, n *db.Notification) error
	ListNotifications(ctx context.Context, channelID string, limit int) ([]*db.Notification, error)
	UnreadNotificationCounts(ctx context.Context) (map[string]int, error)
	MarkNotificationsRead(ctx context.Context, channelID string, upTo int64) error
	DeleteNotification(ctx context.Context, id int64) (bool, error)
	DeleteChannelNotifications(ctx context.Context, channelID string) (bool, error)
}

// SetNotifications keeps notifications in store and serves the
// notification routes.
func (s *Server) SetNotifications(store NotificationStore) {
	s.notifications = store
}

// notification is a notification as the API shows it.
type notification struct {
	ID        int64           `json:"id"`
	PluginID  string          `json:"plugin_id,omitempty"`
	Kind      string          `json:"kind"`
	ChannelID string          `json:"channel_id,omitempty"`
	Title     string          `json:"title,omitempty"`
	Body      string          `json:"body,omitempty"`
	Card      string          `json:"card,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
	Read      bool            `json:"read"`
	CreatedAt time.Time       `json:"created_at"`
}

type notificationsResponse struct {
	Notifications []notification `json:"notifications"`
	Unread        int            `json:"unread"`
}

// handleListNotifications returns a channel's newest notifications and how
// many of them are unread.
func (s *Server) handleListNotifications(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.notifications, "notifications not configured") {
		return
	}
	channelID, ok := notificationsChannel(w, r.URL.Query().Get("channel_id"))
	if !ok {
		return
	}
	list, err := s.notifications.ListNotifications(r.Context(), channelID, notificationsShown)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := notificationsResponse{Notifications: make([]notification, 0, len(list))}
	for _, n := range list {
		if !n.Read {
			out.Unread++
		}
		var data json.RawMessage
		if n.Kind == db.NotificationCard {
			data = json.RawMessage(n.Data)
		}
		out.Notifications = append(out.Notifications, notification{
			ID: n.ID, PluginID: n.PluginID, Kind: n.Kind, ChannelID: n.ChannelID, Title: n.Title, Body: n.Body,
			Card: n.Card, Data: data, Read: n.Read, CreatedAt: n.CreatedAt,
		})
	}
	writeHTTPJSON(w, http.StatusOK, out, s.logger)
}

type unreadNotificationsResponse struct {
	// Unread is how many unread notifications each channel has; the
	// channels with none are left out.
	Unread map[string]int `json:"unread"`
}

// handleUnreadNotifications returns how many unread notifications each
// channel has.
func (s *Server) handleUnreadNotifications(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.notifications, "notifications not configured") {
		return
	}
	counts, err := s.notifications.UnreadNotificationCounts(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeHTTPJSON(w, http.StatusOK, unreadNotificationsResponse{Unread: counts}, s.logger)
}

type notificationsReadRequest struct {
	ChannelID string `json:"channel_id"`
	UpTo      int64  `json:"up_to"`
}

// handleReadNotifications marks a channel's notifications up to an id read.
func (s *Server) handleReadNotifications(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.notifications, "notifications not configured") {
		return
	}
	var req notificationsReadRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if _, ok := notificationsChannel(w, req.ChannelID); !ok {
		return
	}
	if err := s.notifications.MarkNotificationsRead(r.Context(), req.ChannelID, req.UpTo); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.notificationsChanged()
	w.WriteHeader(http.StatusNoContent)
}

// handleDeleteNotification dismisses a notification.
func (s *Server) handleDeleteNotification(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.notifications, "notifications not configured") {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "invalid notification id", http.StatusBadRequest)
		return
	}
	found, err := s.notifications.DeleteNotification(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(w, "notification not found", http.StatusNotFound)
		return
	}
	s.notificationsChanged()
	w.WriteHeader(http.StatusNoContent)
}

// handleClearNotifications dismisses a channel's notifications.
func (s *Server) handleClearNotifications(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.notifications, "notifications not configured") {
		return
	}
	channelID, ok := notificationsChannel(w, r.URL.Query().Get("channel_id"))
	if !ok {
		return
	}
	if _, err := s.notifications.DeleteChannelNotifications(r.Context(), channelID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.notificationsChanged()
	w.WriteHeader(http.StatusNoContent)
}

// notificationsChannel is the channel a notification route acts on, which
// it requires.
func notificationsChannel(w http.ResponseWriter, channelID string) (string, bool) {
	if channelID == "" {
		http.Error(w, "channel_id is required", http.StatusBadRequest)
		return "", false
	}
	return channelID, true
}

// notificationsChanged tells every window the notifications changed.
func (s *Server) notificationsChanged() {
	if s.eventsHub != nil {
		s.eventsHub.Broadcast(Event{Type: EventNotificationsChanged, Global: true})
	}
}

// notify stores n and tells the windows.
func (s *Server) notify(ctx context.Context, n *db.Notification) error {
	if err := s.notifications.InsertNotification(ctx, n); err != nil {
		return err
	}
	s.notificationsChanged()
	return nil
}

// dropChannelNotifications removes a deleted channel's notifications. A
// failure only leaves them listed, so it's logged.
func (s *Server) dropChannelNotifications(ctx context.Context, channelID string) {
	if s.notifications == nil {
		return
	}
	found, err := s.notifications.DeleteChannelNotifications(ctx, channelID)
	if err != nil {
		s.logger.Warn("removing a deleted channel's notifications", "channel_id", channelID, "error", err)
		return
	}
	if found {
		s.notificationsChanged()
	}
}

// RecordNotifications stores, in the background, the notification an event
// of Loop's makes, if any: a run the owner started that failed, or a
// scheduled plugin action that failed. Loop's own scheduled tasks and
// workflows make none; their threads and run history tell how they went.
func (s *Server) RecordNotifications(evt Event) {
	if s.notifications == nil {
		return
	}
	n := loopNotification(evt)
	if n == nil {
		return
	}
	s.deliverAsync(func() {
		if err := s.notify(context.Background(), n); err != nil {
			s.logger.Warn("recording a notification", "kind", n.Kind, "error", err)
		}
	})
}

// loopNotification is the notification evt makes, or nil.
func loopNotification(evt Event) *db.Notification {
	switch d := evt.Data.(type) {
	case events.AgentStatusEventData:
		return runNotification(evt.ChannelID, d)
	case events.TaskRunEventData:
		return taskRunNotification(d)
	case events.PREventData:
		return prNotification(d)
	}
	return nil
}

// runNotification is the notification of an agent run that failed in
// channelID, except one the user stopped, a scheduled one and the hidden
// ones, a plugin's agent's, learn's and explain's. A thread's run is told to its parent too, with the
// thread's id; that copy makes none.
func runNotification(channelID string, d events.AgentStatusEventData) *db.Notification {
	if d.ThreadID != "" && d.ThreadID != channelID {
		return nil
	}
	switch d.Trigger {
	case "scheduled", "learn", "learn-reply", "explain", "plugin":
		return nil
	}
	if d.Status != "error" || d.Stopped {
		return nil
	}
	return &db.Notification{Kind: db.NotificationRun, ChannelID: channelID, Title: "Run failed", Body: d.Error}
}

// prNotification is the notification of a PR/MR CI failure that needs
// the owner.
func prNotification(d events.PREventData) *db.Notification {
	if d.Alert == "" {
		return nil
	}
	return &db.Notification{Kind: db.NotificationRun, ChannelID: d.ChannelID, Title: d.Alert}
}

// taskRunNotification is the notification of a scheduled plugin action that
// failed, in the task's thread if it has one.
func taskRunNotification(d events.TaskRunEventData) *db.Notification {
	if d.Status != string(db.RunStatusFailed) || d.Kind != events.TaskRunPlugin {
		return nil
	}
	channelID := d.ChannelID
	if d.ThreadID != "" {
		channelID = d.ThreadID
	}
	return &db.Notification{Kind: db.NotificationRun, ChannelID: channelID, Title: taskName(d.TaskID) + " failed", Body: d.Error}
}

// taskName names a scheduled task in a notification: "Task #12", or
// "Scheduled task" without its id.
func taskName(id int64) string {
	if id == 0 {
		return "Scheduled task"
	}
	return fmt.Sprintf("Task #%d", id)
}
