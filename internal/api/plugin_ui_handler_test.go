package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/plugin"
	"github.com/radutopala/loop/internal/plugin/install"
	"github.com/radutopala/loop/internal/uibridge"
)

func (s *ServerSuite) TestPluginUICommand() {
	tests := []struct {
		name     string
		body     string
		setup    func()
		wantCode int
		wantBody string
	}{
		{name: "not opening a channel first", body: `{"steps":[{"op":"set_tab","tab":"Git"}]}`,
			wantCode: http.StatusForbidden, wantBody: "starts with select_channel"},
		{name: "select_channel without a channel", body: `{"steps":[{"op":"select_channel","channel_id":"ch-1"},{"op":"select_channel"}]}`,
			wantCode: http.StatusForbidden, wantBody: "select_channel needs a channel_id"},
		{name: "another channel", body: `{"steps":[{"op":"select_channel","channel_id":"other"}]}`, setup: func() {
			s.store.On("GetChannel", mock.Anything, "other").Return(&db.Channel{ChannelID: "other"}, nil).Once()
		}, wantCode: http.StatusForbidden, wantBody: "channel other isn't the plugin's channel or one of its threads"},
		{name: "another channel's thread", body: `{"steps":[{"op":"select_channel","channel_id":"th-9"}]}`, setup: func() {
			s.store.On("GetChannel", mock.Anything, "th-9").Return(&db.Channel{ChannelID: "th-9", ParentID: "ch-9"}, nil).Once()
		}, wantCode: http.StatusForbidden, wantBody: "isn't the plugin's channel"},
		{name: "hidden thread", body: `{"steps":[{"op":"select_channel","channel_id":"th-1"}]}`, setup: func() {
			s.store.On("GetChannel", mock.Anything, "th-1").Return(&db.Channel{ChannelID: "th-1", ParentID: "ch-1", Kind: db.PluginKind("echo")}, nil).Once()
		}, wantCode: http.StatusForbidden, wantBody: "isn't the plugin's channel"},
		{name: "unknown channel", body: `{"steps":[{"op":"select_channel","channel_id":"gone"}]}`, setup: func() {
			s.store.On("GetChannel", mock.Anything, "gone").Return(nil, nil).Once()
		}, wantCode: http.StatusForbidden, wantBody: "isn't the plugin's channel"},
		{name: "channel unreadable", body: `{"steps":[{"op":"select_channel","channel_id":"th-1"}]}`, setup: func() {
			s.store.On("GetChannel", mock.Anything, "th-1").Return(nil, errors.New("db down")).Once()
		}, wantCode: http.StatusInternalServerError, wantBody: "db down"},
		{name: "no steps", body: `{"steps":[]}`, wantCode: http.StatusBadRequest},
		// Past the checks, to no window.
		{name: "its channel", body: `{"steps":[{"op":"select_channel","channel_id":"ch-1"},{"op":"set_tab","tab":"Git"}]}`,
			wantCode: http.StatusNotFound, wantBody: "no app window"},
		{name: "one of its threads", body: `{"steps":[{"op":"select_channel","channel_id":"th-1"}]}`, setup: func() {
			s.store.On("GetChannel", mock.Anything, "th-1").Return(&db.Channel{ChannelID: "th-1", ParentID: "ch-1"}, nil).Once()
		}, wantCode: http.StatusNotFound, wantBody: "no app window"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			s.pluginAPIRoutes(plugin.CapUIRun, "/src/app")
			if tt.setup != nil {
				tt.setup()
			}
			rec := s.requestAs(echoPlugin, http.MethodPost, "/api/plugin/v1/ui/commands", tt.body)
			require.Equal(s.T(), tt.wantCode, rec.Code, rec.Body.String())
			require.Contains(s.T(), rec.Body.String(), tt.wantBody)
			s.store.AssertExpectations(s.T())
		})
	}
}

// TestPluginUICommandRuns sends a plugin's command to a window and answers
// with the window's results.
func (s *ServerSuite) TestPluginUICommandRuns() {
	s.pluginAPIRoutes(plugin.CapUIRun, "/src/app")
	detach := s.srv.ui.Attach("w1", func(data []byte) error {
		var cmd uibridge.Message
		require.NoError(s.T(), json.Unmarshal(data, &cmd))
		go s.srv.ui.Handle("w1", uibridge.Message{Type: uibridge.MsgResult, ID: cmd.ID, Results: json.RawMessage(`[{"op":"select_channel","ok":true}]`)})
		return nil
	})
	defer detach()
	rec := s.requestAs(echoPlugin, http.MethodPost, "/api/plugin/v1/ui/commands", `{"steps":[{"op":"select_channel","channel_id":"ch-1"}]}`)
	require.Equal(s.T(), http.StatusOK, rec.Code, rec.Body.String())
	require.JSONEq(s.T(), `{"client_id":"w1","results":[{"op":"select_channel","ok":true}]}`, rec.Body.String())
}

func (s *ServerSuite) TestPluginUICommandNeedsTheCapability() {
	pm := s.pluginRoutes()
	s.mux.HandleFunc("POST /api/plugin/v1/ui/commands", s.srv.handlePluginUICommand)
	pm.On("Allows", mock.Anything, "echo", plugin.CapUIRun).Return(install.ErrNotApproved).Once()
	rec := s.requestAs(echoPlugin, http.MethodPost, "/api/plugin/v1/ui/commands", `{"steps":[{"op":"select_channel","channel_id":"ch-1"}]}`)
	require.Equal(s.T(), http.StatusForbidden, rec.Code)
}

// TestPluginOpenFile covers a plugin opening a file of its channel: the
// path stays inside the channel's dir, and the window gets one open_file
// step in the channel.
func (s *ServerSuite) TestPluginOpenFile() {
	refused := []struct {
		name     string
		body     string
		wantBody string
	}{
		{name: "bad body", body: `{`},
		{name: "no path", body: `{}`, wantBody: "path must name a file"},
		{name: "absolute", body: `{"path":"/etc/passwd"}`, wantBody: "path must name a file"},
		{name: "outside the dir", body: `{"path":"src/../../secret"}`, wantBody: "path must name a file"},
		{name: "the parent dir", body: `{"path":".."}`, wantBody: "path must name a file"},
		{name: "the dir itself", body: `{"path":"./"}`, wantBody: "path must name a file"},
		{name: "a bad line", body: `{"path":"a.go","line":-1}`, wantBody: "line must be"},
	}
	for _, tt := range refused {
		s.Run(tt.name, func() {
			s.SetupTest()
			s.pluginAPIRoutes(plugin.CapUIOpenFile, "/src/app")
			rec := s.requestAs(echoPlugin, http.MethodPost, "/api/plugin/v1/ui/open_file", tt.body)
			require.Equal(s.T(), http.StatusBadRequest, rec.Code, rec.Body.String())
			require.Contains(s.T(), rec.Body.String(), tt.wantBody)
		})
	}

	s.Run("not approved", func() {
		s.SetupTest()
		pm := s.pluginAPIRoutes(plugin.CapUIRun, "/src/app")
		pm.On("Allows", mock.Anything, "echo", plugin.CapUIOpenFile).Return(install.ErrNotApproved).Once()
		rec := s.requestAs(echoPlugin, http.MethodPost, "/api/plugin/v1/ui/open_file", `{"path":"a.go"}`)
		require.Equal(s.T(), http.StatusForbidden, rec.Code, rec.Body.String())
	})

	opened := []struct {
		name      string
		body      string
		wantSteps string
	}{
		{name: "a file", body: `{"path":"./src//a.go"}`,
			wantSteps: `[{"op":"select_channel","channel_id":"ch-1"},{"op":"open_file","path":"src/a.go"}]`},
		{name: "a file at a line", body: `{"path":"a.go","line":12}`,
			wantSteps: `[{"op":"select_channel","channel_id":"ch-1"},{"op":"open_file","path":"a.go","line":12}]`},
	}
	for _, tt := range opened {
		s.Run(tt.name, func() {
			s.SetupTest()
			s.pluginAPIRoutes(plugin.CapUIOpenFile, "/src/app")
			var steps json.RawMessage
			detach := s.srv.ui.Attach("w1", func(data []byte) error {
				var cmd uibridge.Message
				require.NoError(s.T(), json.Unmarshal(data, &cmd))
				steps = cmd.Steps
				go s.srv.ui.Handle("w1", uibridge.Message{Type: uibridge.MsgResult, ID: cmd.ID, Results: json.RawMessage(`[{"op":"select_channel","ok":true},{"op":"open_file","ok":true}]`)})
				return nil
			})
			defer detach()
			rec := s.requestAs(echoPlugin, http.MethodPost, "/api/plugin/v1/ui/open_file", tt.body)
			require.Equal(s.T(), http.StatusOK, rec.Code, rec.Body.String())
			require.JSONEq(s.T(), tt.wantSteps, string(steps))
			require.Contains(s.T(), rec.Body.String(), `"op":"open_file","ok":true`)
		})
	}
}
