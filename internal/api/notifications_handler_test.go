package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/events"
	"github.com/radutopala/loop/internal/plugin"
)

// notificationRoutes registers the notification routes over a real SQLite
// and returns it with the events they broadcast.
func (s *ServerSuite) notificationRoutes() (*db.SQLiteStore, *[]Event) {
	s.mux.HandleFunc("GET /api/notifications", s.srv.handleListNotifications)
	s.mux.HandleFunc("GET /api/notifications/unread", s.srv.handleUnreadNotifications)
	s.mux.HandleFunc("POST /api/notifications/read", s.srv.handleReadNotifications)
	s.mux.HandleFunc("DELETE /api/notifications/{id}", s.srv.handleDeleteNotification)
	s.mux.HandleFunc("DELETE /api/notifications", s.srv.handleClearNotifications)
	return s.notificationStore()
}

func (s *ServerSuite) listNotifications(channelID string) notificationsResponse {
	rec := s.testRequest("GET", "/api/notifications?channel_id="+channelID, "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	var resp notificationsResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	return resp
}

func (s *ServerSuite) unreadNotifications() map[string]int {
	rec := s.testRequest("GET", "/api/notifications/unread", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	var resp unreadNotificationsResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	return resp.Unread
}

func (s *ServerSuite) TestNotifications() {
	store, sent := s.notificationRoutes()
	ctx := context.Background()
	require.Equal(s.T(), notificationsResponse{Notifications: []notification{}}, s.listNotifications("ch-1"))
	require.Empty(s.T(), s.unreadNotifications())

	run := &db.Notification{Kind: db.NotificationRun, ChannelID: "ch-1", Title: "Run failed", Body: "boom"}
	require.NoError(s.T(), store.InsertNotification(ctx, run))
	card := &db.Notification{PluginID: "echo", Kind: db.NotificationCard, ChannelID: "ch-1", Card: "score", Data: `{"score":3}`}
	require.NoError(s.T(), store.InsertNotification(ctx, card))
	other := &db.Notification{Kind: db.NotificationRun, ChannelID: "ch-2", Title: "Run failed"}
	require.NoError(s.T(), store.InsertNotification(ctx, other))

	got := s.listNotifications("ch-1")
	require.Equal(s.T(), 2, got.Unread)
	require.Equal(s.T(), []notification{
		{ID: card.ID, PluginID: "echo", Kind: db.NotificationCard, ChannelID: "ch-1", Card: "score", Data: json.RawMessage(`{"score":3}`), CreatedAt: card.CreatedAt},
		{ID: run.ID, Kind: db.NotificationRun, ChannelID: "ch-1", Title: "Run failed", Body: "boom", CreatedAt: run.CreatedAt},
	}, got.Notifications)
	require.Equal(s.T(), map[string]int{"ch-1": 2, "ch-2": 1}, s.unreadNotifications())

	require.Equal(s.T(), http.StatusNoContent, s.testRequest("POST", "/api/notifications/read", `{"channel_id":"ch-1","up_to":`+strconv.FormatInt(other.ID, 10)+`}`).Code)
	got = s.listNotifications("ch-1")
	require.Zero(s.T(), got.Unread)
	require.Equal(s.T(), map[string]int{"ch-2": 1}, s.unreadNotifications(), "another channel's stay unread")

	require.Equal(s.T(), http.StatusNoContent, s.testRequest("DELETE", "/api/notifications/"+strconv.FormatInt(run.ID, 10), "").Code)
	require.Equal(s.T(), http.StatusNotFound, s.testRequest("DELETE", "/api/notifications/"+strconv.FormatInt(run.ID, 10), "").Code)
	require.Equal(s.T(), http.StatusBadRequest, s.testRequest("DELETE", "/api/notifications/x", "").Code)
	require.Len(s.T(), s.listNotifications("ch-1").Notifications, 1)

	require.Equal(s.T(), http.StatusNoContent, s.testRequest("DELETE", "/api/notifications?channel_id=ch-1", "").Code)
	require.Empty(s.T(), s.listNotifications("ch-1").Notifications)
	require.Len(s.T(), s.listNotifications("ch-2").Notifications, 1, "another channel's are kept")

	for _, tc := range []struct{ method, path, body string }{
		{"POST", "/api/notifications/read", `{`},
		{"POST", "/api/notifications/read", `{"up_to":1}`},
		{"GET", "/api/notifications", ""},
		{"DELETE", "/api/notifications", ""},
	} {
		require.Equal(s.T(), http.StatusBadRequest, s.testRequest(tc.method, tc.path, tc.body).Code, tc.method+" "+tc.path+" "+tc.body)
	}

	require.Len(s.T(), *sent, 3, "read, removed, cleared")
	for _, e := range *sent {
		require.Equal(s.T(), EventNotificationsChanged, e.Type)
		require.True(s.T(), e.Global)
	}
}

func (s *ServerSuite) TestNotificationsStoreFails() {
	store, sent := s.notificationRoutes()
	require.NoError(s.T(), store.Close())
	for _, tc := range []struct{ method, path, body string }{
		{"GET", "/api/notifications?channel_id=ch-1", ""},
		{"GET", "/api/notifications/unread", ""},
		{"POST", "/api/notifications/read", `{"channel_id":"ch-1","up_to":1}`},
		{"DELETE", "/api/notifications/1", ""},
		{"DELETE", "/api/notifications?channel_id=ch-1", ""},
	} {
		rec := s.testRequest(tc.method, tc.path, tc.body)
		require.Equal(s.T(), http.StatusInternalServerError, rec.Code, tc.method+" "+tc.path)
	}
	require.Empty(s.T(), *sent)
}

func (s *ServerSuite) TestNotificationsNotConfigured() {
	s.mux.HandleFunc("GET /api/notifications", s.srv.handleListNotifications)
	s.mux.HandleFunc("GET /api/notifications/unread", s.srv.handleUnreadNotifications)
	s.mux.HandleFunc("POST /api/notifications/read", s.srv.handleReadNotifications)
	s.mux.HandleFunc("DELETE /api/notifications/{id}", s.srv.handleDeleteNotification)
	s.mux.HandleFunc("DELETE /api/notifications", s.srv.handleClearNotifications)
	for _, tc := range []struct{ method, path string }{
		{"GET", "/api/notifications"},
		{"GET", "/api/notifications/unread"},
		{"POST", "/api/notifications/read"},
		{"DELETE", "/api/notifications/1"},
		{"DELETE", "/api/notifications"},
	} {
		require.Equal(s.T(), http.StatusNotImplemented, s.testRequest(tc.method, tc.path, `{}`).Code, tc.method+" "+tc.path)
	}
	// Without a store, recording and a channel's cleanup do nothing.
	s.srv.RecordNotifications(Event{Type: EventAgentStatus, ChannelID: "ch-1", Data: events.AgentStatusEventData{Status: "error"}})
	s.srv.dropChannelNotifications(context.Background(), "ch-1")
}

func (s *ServerSuite) TestDropChannelNotifications() {
	store, sent := s.notificationStore()
	ctx := context.Background()
	require.NoError(s.T(), store.InsertNotification(ctx, &db.Notification{Kind: db.NotificationRun, ChannelID: "ch-1"}))
	require.NoError(s.T(), store.InsertNotification(ctx, &db.Notification{Kind: db.NotificationRun, ChannelID: "ch-2"}))

	s.srv.dropChannelNotifications(ctx, "ch-1")
	list, err := store.ListNotifications(ctx, "ch-1", 10)
	require.NoError(s.T(), err)
	require.Empty(s.T(), list)
	list, err = store.ListNotifications(ctx, "ch-2", 10)
	require.NoError(s.T(), err)
	require.Len(s.T(), list, 1)
	require.Len(s.T(), *sent, 1)

	s.srv.dropChannelNotifications(ctx, "ch-1")
	require.Len(s.T(), *sent, 1, "nothing removed, nothing told")

	require.NoError(s.T(), store.Close())
	s.srv.dropChannelNotifications(ctx, "ch-2")
	require.Len(s.T(), *sent, 1, "a failure is only logged")
}

func (s *ServerSuite) TestRecordNotifications() {
	store, sent := s.notificationStore()
	ctx := context.Background()
	s.srv.deliverAsync = func(f func()) { f() }

	s.srv.RecordNotifications(Event{Type: EventAgentStatus, ChannelID: "ch-1", Data: events.AgentStatusEventData{Status: "running"}})
	s.srv.RecordNotifications(Event{Type: EventAgentStatus, ChannelID: "ch-1", Data: events.AgentStatusEventData{Status: "error", Error: "boom"}})
	list, err := store.ListNotifications(ctx, "ch-1", 10)
	require.NoError(s.T(), err)
	require.Len(s.T(), list, 1)
	require.Equal(s.T(), "Run failed", list[0].Title)
	require.Equal(s.T(), []string{EventNotificationsChanged}, eventTypes(*sent))

	require.NoError(s.T(), store.Close())
	s.srv.RecordNotifications(Event{Type: EventAgentStatus, ChannelID: "ch-1", Data: events.AgentStatusEventData{Status: "error"}})
	require.Len(s.T(), *sent, 1, "a failure is only logged")
}

func (s *ServerSuite) TestLoopNotification() {
	wf := events.WorkflowRunEventData{WorkflowName: "nightly", ChannelID: "ch-1", Status: "failed", Error: "node x failed"}
	tests := []struct {
		name string
		evt  Event
		want *db.Notification
	}{
		{name: "scheduled run finished",
			evt:  Event{ChannelID: "ch-1", Data: events.AgentStatusEventData{Status: "completed", Trigger: "scheduled", TaskID: 12, DurationMs: 136400, NumTurns: 5}},
			want: &db.Notification{Kind: db.NotificationRun, ChannelID: "ch-1", Title: "Task #12 finished", Body: "Took 2m16s · 5 turns"}},
		{name: "scheduled run of one turn",
			evt:  Event{ChannelID: "ch-1", Data: events.AgentStatusEventData{Status: "completed", Trigger: "scheduled", TaskID: 12, DurationMs: 2600, NumTurns: 1}},
			want: &db.Notification{Kind: db.NotificationRun, ChannelID: "ch-1", Title: "Task #12 finished", Body: "Took 3s · 1 turn"}},
		{name: "scheduled run in its thread, without a task id",
			evt:  Event{ChannelID: "th-1", Data: events.AgentStatusEventData{Status: "completed", Trigger: "scheduled", ThreadID: "th-1"}},
			want: &db.Notification{Kind: db.NotificationRun, ChannelID: "th-1", Title: "Scheduled task finished", Body: "Took 0s"}},
		{name: "scheduled run waiting on an answer",
			evt:  Event{ChannelID: "ch-1", Data: events.AgentStatusEventData{Status: "completed", Trigger: "scheduled", TaskID: 12, Parked: true}},
			want: &db.Notification{Kind: db.NotificationRun, ChannelID: "ch-1", Title: "Task #12 is waiting for your answer"}},
		{name: "the parent's copy",
			evt: Event{ChannelID: "ch-1", Data: events.AgentStatusEventData{Status: "completed", Trigger: "scheduled", ThreadID: "th-1"}}},
		{name: "scheduled run failed",
			evt:  Event{ChannelID: "ch-1", Data: events.AgentStatusEventData{Status: "error", Trigger: "scheduled", TaskID: 12, Error: "boom"}},
			want: &db.Notification{Kind: db.NotificationRun, ChannelID: "ch-1", Title: "Task #12 failed", Body: "boom"}},
		{name: "run failed",
			evt:  Event{ChannelID: "ch-1", Data: events.AgentStatusEventData{Status: "error", Error: "boom"}},
			want: &db.Notification{Kind: db.NotificationRun, ChannelID: "ch-1", Title: "Run failed", Body: "boom"}},
		{name: "the owner's run finished",
			evt: Event{ChannelID: "ch-1", Data: events.AgentStatusEventData{Status: "completed"}}},
		{name: "stopped",
			evt: Event{ChannelID: "ch-1", Data: events.AgentStatusEventData{Status: "stopped", Trigger: "scheduled"}}},
		{name: "hidden run failed",
			evt: Event{ChannelID: "ch-1", Data: events.AgentStatusEventData{Status: "error", Trigger: "learn"}}},
		{name: "a plugin's agent failed",
			evt: Event{ChannelID: "ch-1", Data: events.AgentStatusEventData{Status: "error", Trigger: "plugin"}}},
		{name: "workflow ended",
			evt:  Event{Type: EventWorkflowRunCompleted, Data: wf},
			want: &db.Notification{Kind: db.NotificationWorkflow, ChannelID: "ch-1", Title: "Workflow nightly failed", Body: "node x failed"}},
		{name: "workflow paused",
			evt:  Event{Type: EventWorkflowRunPaused, Data: events.WorkflowRunEventData{WorkflowName: "nightly", ChannelID: "ch-1"}},
			want: &db.Notification{Kind: db.NotificationWorkflow, ChannelID: "ch-1", Title: "Workflow nightly is waiting for input"}},
		{name: "workflow completed",
			evt:  Event{Type: EventWorkflowRunCompleted, Data: events.WorkflowRunEventData{WorkflowName: "nightly", ChannelID: "ch-1", Status: "completed"}},
			want: &db.Notification{Kind: db.NotificationWorkflow, ChannelID: "ch-1", Title: "Workflow nightly completed"}},
		{name: "workflow cancelled",
			evt: Event{Type: EventWorkflowRunCompleted, Data: events.WorkflowRunEventData{WorkflowName: "nightly", Status: "cancelled"}}},
		{name: "workflow deleted",
			evt: Event{Type: EventWorkflowRunCompleted, Data: events.WorkflowRunEventData{WorkflowName: "nightly", Status: "deleted"}}},
		{name: "workflow in no channel",
			evt: Event{Type: EventWorkflowRunCompleted, Data: events.WorkflowRunEventData{WorkflowName: "nightly", Status: "failed"}}},
		{name: "workflow started",
			evt: Event{Type: EventWorkflowRunStarted, Data: wf}},
		{name: "anything else",
			evt: Event{Type: EventMessageCreated, Data: events.MessageEventData{}}},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			require.Equal(s.T(), tt.want, loopNotification(tt.evt))
		})
	}
}

func (s *ServerSuite) TestPluginNotifyRecorded() {
	s.pluginAPIRoutes(plugin.CapUINotify, "/src/app")
	store, sent := s.notificationStore()
	ctx := context.Background()

	rec := s.requestAs(echoPlugin, "POST", "/api/plugin/v1/notify", `{"title":"Quiz due","body":"3 cards"}`)
	require.Equal(s.T(), http.StatusNoContent, rec.Code)
	list, err := store.ListNotifications(ctx, "ch-1", 10)
	require.NoError(s.T(), err)
	require.Len(s.T(), list, 1)
	require.Equal(s.T(), db.Notification{ID: list[0].ID, PluginID: "echo", Kind: db.NotificationNotify, ChannelID: "ch-1", Title: "Quiz due", Body: "3 cards", Data: "null", CreatedAt: list[0].CreatedAt}, *list[0])
	require.Equal(s.T(), []string{EventNotificationsChanged}, eventTypes(*sent))

	// A failed recording is only logged.
	require.NoError(s.T(), store.Close())
	rec = s.requestAs(echoPlugin, "POST", "/api/plugin/v1/notify", `{"title":"again"}`)
	require.Equal(s.T(), http.StatusNoContent, rec.Code)
	require.Equal(s.T(), []string{EventNotificationsChanged}, eventTypes(*sent))
}

func (s *ServerSuite) TestPluginPageNotify() {
	tests := []struct {
		name     string
		body     string
		channel  *db.Channel
		chErr    error
		shows    bool
		showsErr error
		wantCode int
		wantBody string
	}{
		{name: "bad body", body: `{`, wantCode: http.StatusBadRequest},
		{name: "no channel", body: `{"title":"t"}`, wantCode: http.StatusBadRequest, wantBody: "channel_id is required"},
		{name: "channel lookup fails", body: `{"channel_id":"ch-1","title":"t"}`, chErr: errors.New("boom"), wantCode: http.StatusInternalServerError},
		{name: "no such channel", body: `{"channel_id":"ch-1","title":"t"}`, wantCode: http.StatusNotFound},
		{name: "shows fails", body: `{"channel_id":"ch-1","title":"t"}`, channel: &db.Channel{ChannelID: "ch-1"}, showsErr: errors.New("boom"), wantCode: http.StatusInternalServerError},
		{name: "not shown", body: `{"channel_id":"ch-1","title":"t"}`, channel: &db.Channel{ChannelID: "ch-1"}, wantCode: http.StatusForbidden, wantBody: "the plugin can't notify in this channel"},
		{name: "no title", body: `{"channel_id":"ch-1"}`, channel: &db.Channel{ChannelID: "ch-1"}, shows: true, wantCode: http.StatusBadRequest, wantBody: "title is required"},
		{name: "kept", body: `{"channel_id":"ch-1","title":"Quiz due","body":"3 cards"}`, channel: &db.Channel{ChannelID: "ch-1"}, shows: true, wantCode: http.StatusNoContent},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			pm := s.pluginRoutes()
			s.mux.HandleFunc("POST /api/plugins/{plugin}/notifications", s.srv.handlePluginPageNotify)
			store, sent := s.notificationStore()
			if tt.channel != nil || tt.chErr != nil || tt.wantCode == http.StatusNotFound {
				s.store.On("GetChannel", mock.Anything, "ch-1").Return(tt.channel, tt.chErr).Once()
			}
			if tt.channel != nil {
				pm.On("Shows", mock.Anything, "echo", plugin.CapUINotify, pluginRuntimeChannel(tt.channel)).Return(tt.shows, tt.showsErr).Once()
			}
			rec := s.testRequest("POST", "/api/plugins/echo/notifications", tt.body)
			require.Equal(s.T(), tt.wantCode, rec.Code)
			if tt.wantBody != "" {
				require.Equal(s.T(), tt.wantBody, strings.TrimSpace(rec.Body.String()))
			}
			list, err := store.ListNotifications(context.Background(), "ch-1", 10)
			require.NoError(s.T(), err)
			if tt.wantCode != http.StatusNoContent {
				require.Empty(s.T(), list)
				require.Empty(s.T(), *sent)
				return
			}
			require.Len(s.T(), list, 1)
			require.Equal(s.T(), db.Notification{ID: list[0].ID, PluginID: "echo", Kind: db.NotificationNotify, ChannelID: "ch-1", Title: "Quiz due", Body: "3 cards", Data: "null", CreatedAt: list[0].CreatedAt}, *list[0])
			require.Equal(s.T(), []string{EventNotificationsChanged}, eventTypes(*sent))
		})
	}
}

func (s *ServerSuite) TestPluginPageNotifyDisabled() {
	s.mux.HandleFunc("POST /api/plugins/{plugin}/notifications", s.srv.handlePluginPageNotify)
	rec := s.testRequest("POST", "/api/plugins/echo/notifications", `{"channel_id":"ch-1","title":"t"}`)
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

func eventTypes(evts []Event) []string {
	out := make([]string, 0, len(evts))
	for _, e := range evts {
		out = append(out, e.Type)
	}
	return out
}
