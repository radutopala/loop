package api

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/plugin"
	"github.com/radutopala/loop/internal/plugin/install"
	"github.com/radutopala/loop/internal/types"
)

// pluginBadgeRoutes registers the badge routes for echo, allowed ui.badge
// in ch-1, keeping badges in a real SQLite, and returns the events they
// broadcast.
func (s *ServerSuite) pluginBadgeRoutes() (*db.SQLiteStore, *[]Event) {
	s.pluginAPIRoutes(plugin.CapUIBadge, "/src/app")
	store, err := db.NewSQLiteStore(filepath.Join(s.T().TempDir(), "loop.db"))
	require.NoError(s.T(), err)
	s.T().Cleanup(func() { _ = store.Close() })
	s.srv.SetPluginBadges(store)
	hub := NewEventsHub(testLogger())
	sent := &[]Event{}
	hub.captureHook = func(e Event) { *sent = append(*sent, e) }
	s.srv.SetEventsHub(hub)
	return store, sent
}

func (s *ServerSuite) TestPutPluginBadge() {
	tests := []struct {
		name     string
		body     string
		wantCode int
		wantBody string
		want     *pluginBadge
	}{
		{name: "sets", body: `{"text":" 3 due ","title":"3 quizzes due"}`, wantCode: http.StatusNoContent,
			want: &pluginBadge{PluginID: "echo", Text: "3 due", Title: "3 quizzes due"}},
		{name: "without a title", body: `{"text":"ok"}`, wantCode: http.StatusNoContent,
			want: &pluginBadge{PluginID: "echo", Text: "ok"}},
		{name: "16 characters", body: `{"text":"` + strings.Repeat("é", 16) + `"}`, wantCode: http.StatusNoContent,
			want: &pluginBadge{PluginID: "echo", Text: strings.Repeat("é", 16)}},
		{name: "no text", body: `{"text":"  "}`, wantCode: http.StatusBadRequest, wantBody: "text is required"},
		{name: "text too long", body: `{"text":"` + strings.Repeat("a", 17) + `"}`, wantCode: http.StatusBadRequest, wantBody: "at most 16"},
		{name: "control character", body: `{"text":"a\tb"}`, wantCode: http.StatusBadRequest, wantBody: "one line"},
		{name: "title too long", body: `{"text":"a","title":"` + strings.Repeat("a", 201) + `"}`, wantCode: http.StatusBadRequest, wantBody: "at most 200"},
		{name: "bad body", body: `{`, wantCode: http.StatusBadRequest},
		{name: "too big", body: `{"text":"a","title":"` + strings.Repeat("a", pluginEventMax) + `"}`, wantCode: http.StatusBadRequest},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			store, sent := s.pluginBadgeRoutes()
			rec := s.requestAs(echoPlugin, "PUT", "/api/plugin/v1/badge", tt.body)
			require.Equal(s.T(), tt.wantCode, rec.Code)
			require.Contains(s.T(), rec.Body.String(), tt.wantBody)
			badges, err := store.ListPluginBadges(context.Background())
			require.NoError(s.T(), err)
			if tt.want == nil {
				require.Empty(s.T(), badges)
				require.Empty(s.T(), *sent)
				return
			}
			require.Len(s.T(), badges, 1)
			require.Equal(s.T(), "ch-1", badges[0].ChannelID)
			require.Equal(s.T(), *tt.want, pluginBadge{PluginID: badges[0].PluginID, Text: badges[0].Text, Title: badges[0].Title})
			require.Len(s.T(), *sent, 1)
			e := (*sent)[0]
			require.Equal(s.T(), "plugin.badge", e.Type)
			require.Equal(s.T(), "ch-1", e.ChannelID)
			require.True(s.T(), e.Global, "the sidebar shows every channel's badges")
			require.Equal(s.T(), *tt.want, e.Data)
		})
	}
}

func (s *ServerSuite) TestDeletePluginBadge() {
	store, sent := s.pluginBadgeRoutes()
	ctx := context.Background()
	require.NoError(s.T(), store.SetPluginBadge(ctx, &db.PluginBadge{PluginID: "echo", ChannelID: "ch-1", Text: "3"}))
	require.NoError(s.T(), store.SetPluginBadge(ctx, &db.PluginBadge{PluginID: "other", ChannelID: "ch-1", Text: "4"}))

	rec := s.requestAs(echoPlugin, "DELETE", "/api/plugin/v1/badge", "")

	require.Equal(s.T(), http.StatusNoContent, rec.Code)
	badges, err := store.ListPluginBadges(ctx)
	require.NoError(s.T(), err)
	require.Len(s.T(), badges, 1)
	require.Equal(s.T(), "other", badges[0].PluginID)
	require.Len(s.T(), *sent, 1)
	require.Equal(s.T(), pluginBadge{PluginID: "echo"}, (*sent)[0].Data)
	require.True(s.T(), (*sent)[0].Global)
}

func (s *ServerSuite) TestPluginBadgeStoreFails() {
	for _, method := range []string{"PUT", "DELETE"} {
		s.Run(method, func() {
			s.SetupTest()
			store, sent := s.pluginBadgeRoutes()
			require.NoError(s.T(), store.Close())
			rec := s.requestAs(echoPlugin, method, "/api/plugin/v1/badge", `{"text":"a"}`)
			require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
			require.Empty(s.T(), *sent)
		})
	}
}

func (s *ServerSuite) TestPluginBadgeNotConfigured() {
	s.pluginAPIRoutes(plugin.CapUIBadge, "/src/app")
	for _, method := range []string{"PUT", "DELETE"} {
		rec := s.requestAs(echoPlugin, method, "/api/plugin/v1/badge", `{"text":"a"}`)
		require.Equal(s.T(), http.StatusNotImplemented, rec.Code, method)
	}
}

func (s *ServerSuite) TestPluginBadgeNeedsTheCapability() {
	pm := s.pluginRoutes()
	s.mux.HandleFunc("PUT /api/plugin/v1/badge", s.srv.handlePutPluginBadge)
	s.mux.HandleFunc("DELETE /api/plugin/v1/badge", s.srv.handleDeletePluginBadge)
	pm.On("Allows", mock.Anything, "echo", plugin.CapUIBadge).Return(install.ErrNotApproved).Twice()
	for _, method := range []string{"PUT", "DELETE"} {
		rec := s.requestAs(echoPlugin, method, "/api/plugin/v1/badge", `{"text":"a"}`)
		require.Equal(s.T(), http.StatusForbidden, rec.Code, method)
	}
}

// TestSearchChannelsPluginBadges: each row carries the badges plugins set
// on it, threads too; without plugins, or when the lookup fails, none.
func (s *ServerSuite) TestSearchChannelsPluginBadges() {
	tests := []struct {
		name    string
		plugins bool
		closed  bool
		want    map[string][]pluginBadge
	}{
		{name: "badges by channel", plugins: true, want: map[string][]pluginBadge{
			"ch-1": {{PluginID: "echo", Text: "3", Title: "3 due"}, {PluginID: "quiz", Text: "ok"}},
			"ch-2": {{PluginID: "echo", Text: "1"}},
		}},
		{name: "plugins off", want: map[string][]pluginBadge{}},
		{name: "lookup fails", plugins: true, closed: true, want: map[string][]pluginBadge{}},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			s.store.On("ListChannels", mock.Anything).Return([]*db.Channel{
				{ChannelID: "ch-1", Name: "app", DirPath: "/a", Platform: types.PlatformLocal},
				{ChannelID: "ch-2", Name: "thread", DirPath: "/a", ParentID: "ch-1", Platform: types.PlatformLocal},
				{ChannelID: "ch-3", Name: "other", DirPath: "/c", Platform: types.PlatformLocal},
			}, nil)
			s.store.On("ChannelActivity", mock.Anything).Return(map[string]time.Time{}, nil)
			if tt.plugins {
				s.pluginRoutes()
			}
			store, err := db.NewSQLiteStore(filepath.Join(s.T().TempDir(), "loop.db"))
			require.NoError(s.T(), err)
			ctx := context.Background()
			require.NoError(s.T(), store.SetPluginBadge(ctx, &db.PluginBadge{PluginID: "echo", ChannelID: "ch-1", Text: "3", Title: "3 due"}))
			require.NoError(s.T(), store.SetPluginBadge(ctx, &db.PluginBadge{PluginID: "quiz", ChannelID: "ch-1", Text: "ok"}))
			require.NoError(s.T(), store.SetPluginBadge(ctx, &db.PluginBadge{PluginID: "echo", ChannelID: "ch-2", Text: "1"}))
			if tt.closed {
				require.NoError(s.T(), store.Close())
			} else {
				s.T().Cleanup(func() { _ = store.Close() })
			}
			s.srv.SetPluginBadges(store)

			rec := s.testRequest("GET", "/api/channels", "")

			require.Equal(s.T(), http.StatusOK, rec.Code)
			var resp []channelResponse
			require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
			got := map[string][]pluginBadge{}
			for _, ch := range resp {
				if ch.PluginBadges != nil {
					got[ch.ChannelID] = ch.PluginBadges
				}
			}
			require.Equal(s.T(), tt.want, got)
		})
	}
}
