package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
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
	return s.badgeStore()
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
			store := s.sqliteStore()
			ctx := context.Background()
			require.NoError(s.T(), store.SetPluginBadge(ctx, &db.PluginBadge{PluginID: "echo", ChannelID: "ch-1", Text: "3", Title: "3 due"}))
			require.NoError(s.T(), store.SetPluginBadge(ctx, &db.PluginBadge{PluginID: "quiz", ChannelID: "ch-1", Text: "ok"}))
			require.NoError(s.T(), store.SetPluginBadge(ctx, &db.PluginBadge{PluginID: "echo", ChannelID: "ch-2", Text: "1"}))
			if tt.closed {
				require.NoError(s.T(), store.Close())
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

// badgeDeleteFails is a badge store whose deletes fail.
type badgeDeleteFails struct{ *db.SQLiteStore }

func (badgeDeleteFails) DeletePluginBadge(context.Context, string, string) error {
	return errors.New("boom")
}

// TestPrunePluginBadges clears the badges of a deleted channel and of a
// plugin that may no longer show one; a badge whose state can't be read is
// kept.
func (s *ServerSuite) TestPrunePluginBadges() {
	pm := s.pluginRoutes()
	store, sent := s.badgeStore()
	ctx := context.Background()
	for _, b := range []*db.PluginBadge{
		{PluginID: "echo", ChannelID: "ch-ok", Text: "1"},
		{PluginID: "echo", ChannelID: "ch-err", Text: "2"},
		{PluginID: "echo", ChannelID: "ch-gone", Text: "3"},
		{PluginID: "off", ChannelID: "ch-ok", Text: "4"},
		{PluginID: "unsure", ChannelID: "ch-ok", Text: "5"},
	} {
		require.NoError(s.T(), store.SetPluginBadge(ctx, b))
	}
	ok := &db.Channel{ChannelID: "ch-ok", DirPath: "/src/app"}
	s.store.On("GetChannel", mock.Anything, "ch-ok").Return(ok, nil)
	s.store.On("GetChannel", mock.Anything, "ch-err").Return(nil, errors.New("boom"))
	s.store.On("GetChannel", mock.Anything, "ch-gone").Return(nil, nil)
	rch := pluginRuntimeChannel(ok)
	pm.On("Shows", mock.Anything, "echo", plugin.CapUIBadge, rch).Return(true, nil)
	pm.On("Shows", mock.Anything, "off", plugin.CapUIBadge, rch).Return(false, nil)
	pm.On("Shows", mock.Anything, "unsure", plugin.CapUIBadge, rch).Return(false, errors.New("boom"))

	s.srv.prunePluginBadges(ctx)

	badges, err := store.ListPluginBadges(ctx)
	require.NoError(s.T(), err)
	var left []string
	for _, b := range badges {
		left = append(left, b.PluginID+"@"+b.ChannelID)
	}
	require.ElementsMatch(s.T(), []string{"echo@ch-ok", "echo@ch-err", "unsure@ch-ok"}, left)
	var cleared []string
	for _, e := range *sent {
		cleared = append(cleared, e.Data.(pluginBadge).PluginID+"@"+e.ChannelID)
	}
	require.ElementsMatch(s.T(), []string{"echo@ch-gone", "off@ch-ok"}, cleared)

	// A failed clear is only logged and told to no one.
	*sent = nil
	require.NoError(s.T(), store.SetPluginBadge(ctx, &db.PluginBadge{PluginID: "echo", ChannelID: "ch-gone", Text: "3"}))
	s.srv.SetPluginBadges(badgeDeleteFails{store})
	s.srv.prunePluginBadges(ctx)
	require.Empty(s.T(), *sent)

	// Nor can a failed lookup clear anything.
	require.NoError(s.T(), store.Close())
	s.srv.prunePluginBadges(ctx)
	require.Empty(s.T(), *sent)
	require.Empty(s.T(), s.srv.pluginBadgesOf(ctx, "echo"))
}

func (s *ServerSuite) TestPrunePluginBadgesOff() {
	s.srv.prunePluginBadges(context.Background())
	require.Empty(s.T(), s.srv.pluginBadgesOf(context.Background(), "echo"))
}

// badgeStore keeps badges in a real SQLite and returns it with the events
// the server broadcasts.
func (s *ServerSuite) badgeStore() (*db.SQLiteStore, *[]Event) {
	store := s.sqliteStore()
	s.srv.SetPluginBadges(store)
	hub := NewEventsHub(testLogger())
	sent := &[]Event{}
	hub.captureHook = func(e Event) { *sent = append(*sent, e) }
	s.srv.SetEventsHub(hub)
	return store, sent
}

// TestRemovePluginClearsItsBadges: removing a plugin tells every window its
// badges and notifications are gone.
func (s *ServerSuite) TestRemovePluginClearsItsBadges() {
	pm := s.pluginRoutes()
	store, sent := s.badgeStore()
	s.srv.SetNotifications(store)
	ctx := context.Background()
	require.NoError(s.T(), store.SetPluginBadge(ctx, &db.PluginBadge{PluginID: "echo", ChannelID: "ch-1", Text: "1"}))
	require.NoError(s.T(), store.SetPluginBadge(ctx, &db.PluginBadge{PluginID: "other", ChannelID: "ch-1", Text: "2"}))
	pm.On("Remove", mock.Anything, "echo", false).Return(nil).Once()

	require.Equal(s.T(), http.StatusNoContent, s.testRequest("DELETE", "/api/plugins/echo", "").Code)

	require.Len(s.T(), *sent, 3)
	require.Equal(s.T(), "plugin.badge", (*sent)[0].Type)
	require.Equal(s.T(), pluginBadge{PluginID: "echo"}, (*sent)[0].Data)
	require.Equal(s.T(), EventNotificationsChanged, (*sent)[1].Type)
	require.Equal(s.T(), EventPluginsChanged, (*sent)[2].Type)
}

// TestPluginsStopped prunes the badges no longer shown and tells the app.
func (s *ServerSuite) TestPluginsStopped() {
	pm := s.pluginRoutes()
	store, sent := s.badgeStore()
	ctx := context.Background()
	require.NoError(s.T(), store.SetPluginBadge(ctx, &db.PluginBadge{PluginID: "echo", ChannelID: "ch-1", Text: "1"}))
	ch := &db.Channel{ChannelID: "ch-1"}
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(ch, nil).Once()
	pm.On("Shows", mock.Anything, "echo", plugin.CapUIBadge, pluginRuntimeChannel(ch)).Return(false, nil).Once()

	s.srv.PluginsStopped()

	require.Len(s.T(), *sent, 2)
	require.Equal(s.T(), "plugin.badge", (*sent)[0].Type)
	require.Equal(s.T(), EventPluginsChanged, (*sent)[1].Type)
}
