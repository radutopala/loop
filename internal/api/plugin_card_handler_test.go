package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/events"
	"github.com/radutopala/loop/internal/plugin"
	"github.com/radutopala/loop/internal/plugin/install"
	"github.com/radutopala/loop/internal/types"
)

var cardManifest = &plugin.Manifest{UI: &plugin.UI{Cards: []plugin.Card{{ID: "score", Entry: "ui/score.html"}}}}

func (s *ServerSuite) TestPostPluginCard() {
	tests := []struct {
		name        string
		body        string
		wantContent string
	}{
		{name: "data and title", body: `{"card":"score","title":"  Quiz\n results ","data":{ "score": 3,  "of": [1, 2] }}`,
			wantContent: "```loop-plugin-card echo score Quiz results\n{\"score\":3,\"of\":[1,2]}\n```"},
		{name: "no data", body: `{"card":"score"}`, wantContent: "```loop-plugin-card echo score\nnull\n```"},
		{name: "a string", body: `{"card":"score","data":"line\none"}`, wantContent: "```loop-plugin-card echo score\n\"line\\none\"\n```"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			pm := s.pluginAPIRoutes(plugin.CapUICard, "/src/app")
			pm.On("UI", mock.Anything, "echo").Return("/d", cardManifest, nil).Once()
			s.store.On("GetChannel", mock.Anything, "ch-1").Unset()
			s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ID: 7, ChannelID: "ch-1", Platform: types.PlatformLocal}, nil).Once()
			var stored *db.Message
			s.store.On("InsertMessage", mock.Anything, mock.AnythingOfType("*db.Message")).Run(func(args mock.Arguments) {
				stored = args.Get(1).(*db.Message)
				stored.ID = 42
			}).Return(nil).Once()
			hub := NewEventsHub(testLogger())
			var sent []Event
			hub.captureHook = func(e Event) { sent = append(sent, e) }
			s.srv.SetEventsHub(hub)

			rec := s.requestAs(echoPlugin, "POST", "/api/plugin/v1/cards", tt.body)

			require.Equal(s.T(), http.StatusCreated, rec.Code, rec.Body.String())
			require.NotNil(s.T(), stored)
			require.True(s.T(), strings.HasPrefix(stored.MsgID, "card-"))
			require.JSONEq(s.T(), `{"msg_id":"`+stored.MsgID+`"}`, rec.Body.String())
			require.Equal(s.T(), int64(7), stored.ChatID)
			require.Equal(s.T(), "ch-1", stored.ChannelID)
			require.Equal(s.T(), "echo", stored.AuthorName)
			require.True(s.T(), stored.IsBot)
			require.True(s.T(), stored.IsProcessed)
			require.Equal(s.T(), tt.wantContent, stored.Content)
			require.Len(s.T(), sent, 1)
			require.Equal(s.T(), EventMessageCreated, sent[0].Type)
			require.Equal(s.T(), "ch-1", sent[0].ChannelID)
			require.Equal(s.T(), events.MessageEventData{ID: 42, MsgID: stored.MsgID, AuthorName: "echo", Content: tt.wantContent, IsBot: true, IsProcessed: true}, sent[0].Data)
		})
	}
}

func (s *ServerSuite) TestPostPluginCardNoEventsHub() {
	pm := s.pluginAPIRoutes(plugin.CapUICard, "/src/app")
	pm.On("UI", mock.Anything, "echo").Return("/d", cardManifest, nil).Once()
	s.store.On("InsertMessage", mock.Anything, mock.AnythingOfType("*db.Message")).Return(nil).Once()
	rec := s.requestAs(echoPlugin, "POST", "/api/plugin/v1/cards", `{"card":"score"}`)
	require.Equal(s.T(), http.StatusCreated, rec.Code)
}

func (s *ServerSuite) TestPostPluginCardRefused() {
	tests := []struct {
		name     string
		body     string
		platform types.Platform
		ui       error
		insert   error
		wantCode int
		wantBody string
	}{
		{name: "bad body", body: `{`, wantCode: http.StatusBadRequest, wantBody: "invalid request body"},
		{name: "too big", body: `{"card":"score","data":"` + strings.Repeat("a", pluginEventMax) + `"}`, wantCode: http.StatusBadRequest},
		{name: "not the desktop app", body: `{"card":"score"}`, platform: types.PlatformSlack, wantCode: http.StatusBadRequest, wantBody: "desktop app only"},
		{name: "manifest unreadable", body: `{"card":"score"}`, ui: errors.New("boom"), wantCode: http.StatusInternalServerError, wantBody: "boom"},
		{name: "unknown card", body: `{"card":"other"}`, wantCode: http.StatusBadRequest, wantBody: `the plugin declares no card "other"`},
		{name: "no card", body: `{}`, wantCode: http.StatusBadRequest, wantBody: `no card ""`},
		{name: "insert fails", body: `{"card":"score"}`, insert: errors.New("db down"), wantCode: http.StatusInternalServerError, wantBody: "db down"},
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
			s.store.On("InsertMessage", mock.Anything, mock.Anything).Return(tt.insert).Maybe()

			rec := s.requestAs(echoPlugin, "POST", "/api/plugin/v1/cards", tt.body)

			require.Equal(s.T(), tt.wantCode, rec.Code)
			require.Contains(s.T(), rec.Body.String(), tt.wantBody)
			if tt.insert == nil {
				s.store.AssertNotCalled(s.T(), "InsertMessage", mock.Anything, mock.Anything)
			}
		})
	}
}

func (s *ServerSuite) TestPostPluginCardNeedsTheCapability() {
	pm := s.pluginRoutes()
	s.mux.HandleFunc("POST /api/plugin/v1/cards", s.srv.handlePostPluginCard)
	pm.On("Allows", mock.Anything, "echo", plugin.CapUICard).Return(install.ErrNotApproved).Once()
	rec := s.requestAs(echoPlugin, "POST", "/api/plugin/v1/cards", `{"card":"score"}`)
	require.Equal(s.T(), http.StatusForbidden, rec.Code)
}
