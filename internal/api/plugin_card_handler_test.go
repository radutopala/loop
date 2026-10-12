package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/plugin"
	"github.com/radutopala/loop/internal/plugin/install"
	"github.com/radutopala/loop/internal/types"
)

var cardManifest = &plugin.Manifest{UI: &plugin.UI{Cards: []plugin.Card{{ID: "score", Entry: "ui/score.html"}}}}

// notificationStore keeps notifications in a real SQLite for the server
// and returns it with the events the server broadcasts.
func (s *ServerSuite) notificationStore() (*db.SQLiteStore, *[]Event) {
	store := s.sqliteStore()
	s.srv.SetNotifications(store)
	hub := NewEventsHub(testLogger())
	sent := &[]Event{}
	hub.captureHook = func(e Event) { *sent = append(*sent, e) }
	s.srv.SetEventsHub(hub)
	return store, sent
}

func (s *ServerSuite) TestPostPluginCard() {
	tests := []struct {
		name      string
		body      string
		wantTitle string
		wantData  string
	}{
		{name: "data and title", body: `{"card":"score","title":"  Quiz\n results ","data":{ "score": 3,  "of": [1, 2] }}`,
			wantTitle: "Quiz results", wantData: `{"score":3,"of":[1,2]}`},
		{name: "no data", body: `{"card":"score"}`, wantData: "null"},
		{name: "a string", body: `{"card":"score","data":"line\none"}`, wantData: `"line\none"`},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			pm := s.pluginAPIRoutes(plugin.CapUICard, "/src/app")
			pm.On("UI", mock.Anything, "echo").Return("/d", cardManifest, nil).Once()
			store, sent := s.notificationStore()

			rec := s.requestAs(echoPlugin, "POST", "/api/plugin/v1/cards", tt.body)

			require.Equal(s.T(), http.StatusCreated, rec.Code, rec.Body.String())
			list, err := store.ListNotifications(context.Background(), "ch-1", 10)
			require.NoError(s.T(), err)
			require.Len(s.T(), list, 1)
			n := list[0]
			require.JSONEq(s.T(), `{"id":`+strconv.FormatInt(n.ID, 10)+`}`, rec.Body.String())
			require.Equal(s.T(), "echo", n.PluginID)
			require.Equal(s.T(), db.NotificationCard, n.Kind)
			require.Equal(s.T(), "ch-1", n.ChannelID)
			require.Equal(s.T(), "score", n.Card)
			require.Equal(s.T(), tt.wantTitle, n.Title)
			require.Equal(s.T(), tt.wantData, n.Data)
			require.Equal(s.T(), []Event{{Type: EventNotificationsChanged, Global: true, Timestamp: (*sent)[0].Timestamp}}, *sent)
		})
	}
}

func (s *ServerSuite) TestPostPluginCardRefused() {
	tests := []struct {
		name     string
		body     string
		platform types.Platform
		ui       error
		closed   bool
		limited  bool
		wantCode int
		wantBody string
	}{
		{name: "bad body", body: `{`, wantCode: http.StatusBadRequest, wantBody: "invalid request body"},
		{name: "too big", body: `{"card":"score","data":"` + strings.Repeat("a", pluginEventMax) + `"}`, wantCode: http.StatusBadRequest},
		{name: "not the desktop app", body: `{"card":"score"}`, platform: types.PlatformSlack, wantCode: http.StatusBadRequest, wantBody: "desktop app only"},
		{name: "manifest unreadable", body: `{"card":"score"}`, ui: errors.New("boom"), wantCode: http.StatusInternalServerError, wantBody: "boom"},
		{name: "unknown card", body: `{"card":"other"}`, wantCode: http.StatusBadRequest, wantBody: `the plugin declares no card "other"`},
		{name: "no card", body: `{}`, wantCode: http.StatusBadRequest, wantBody: `no card ""`},
		{name: "too many", body: `{"card":"score"}`, limited: true, wantCode: http.StatusTooManyRequests, wantBody: "too many notifications"},
		{name: "store fails", body: `{"card":"score"}`, closed: true, wantCode: http.StatusInternalServerError},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			pm := s.pluginAPIRoutes(plugin.CapUICard, "/src/app")
			if tt.platform != "" {
				s.store.On("GetChannel", mock.Anything, "ch-1").Unset()
				s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1", Platform: tt.platform}, nil).Once()
			}
			pm.On("UI", mock.Anything, "echo").Return("/d", cardManifest, tt.ui).Maybe()
			store, sent := s.notificationStore()
			if tt.closed {
				require.NoError(s.T(), store.Close())
			}
			if tt.limited {
				now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
				s.srv.notifyLimit.now = func() time.Time { return now }
				for range pluginNotifyBurst {
					s.srv.notifyLimit.allow("echo")
				}
			}

			rec := s.requestAs(echoPlugin, "POST", "/api/plugin/v1/cards", tt.body)

			require.Equal(s.T(), tt.wantCode, rec.Code)
			require.Contains(s.T(), rec.Body.String(), tt.wantBody)
			require.Empty(s.T(), *sent)
		})
	}
}

func (s *ServerSuite) TestPostPluginCardNotConfigured() {
	s.pluginAPIRoutes(plugin.CapUICard, "/src/app")
	rec := s.requestAs(echoPlugin, "POST", "/api/plugin/v1/cards", `{"card":"score"}`)
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

func (s *ServerSuite) TestPostPluginCardNeedsTheCapability() {
	pm := s.pluginRoutes()
	s.mux.HandleFunc("POST /api/plugin/v1/cards", s.srv.handlePostPluginCard)
	pm.On("Allows", mock.Anything, "echo", plugin.CapUICard).Return(install.ErrNotApproved).Once()
	rec := s.requestAs(echoPlugin, "POST", "/api/plugin/v1/cards", `{"card":"score"}`)
	require.Equal(s.T(), http.StatusForbidden, rec.Code)
}
