package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/types"
)

func (s *ServerSuite) learnRequest(method, channelID, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/api/channels/"+channelID+"/learn", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	return w
}

func (s *ServerSuite) TestLearnGet() {
	tests := []struct {
		name     string
		override string
		platform types.Platform
		def      bool
		learnCh  *db.Channel
		running  bool
		want     learnStateResponse
	}{
		{name: "inherits off", want: learnStateResponse{Available: true}},
		{name: "inherits on", def: true, want: learnStateResponse{Available: true, DefaultLearn: true, Enabled: true}},
		{name: "override on", override: db.LearnOn, want: learnStateResponse{Available: true, Learn: "on", Enabled: true}},
		{name: "override off", override: db.LearnOff, def: true, want: learnStateResponse{Available: true, Learn: "off", DefaultLearn: true}},
		{
			name: "slack channel never learns", override: db.LearnOn, platform: types.PlatformSlack,
			want: learnStateResponse{Learn: "on"},
		},
		{
			name: "learn thread idle", override: db.LearnOn, learnCh: &db.Channel{ChannelID: "l-1"},
			want: learnStateResponse{Available: true, Learn: "on", Enabled: true, LearnChannelID: "l-1"},
		},
		{
			name: "learn thread running", override: db.LearnOn, learnCh: &db.Channel{ChannelID: "l-1"}, running: true,
			want: learnStateResponse{Available: true, Learn: "on", Enabled: true, LearnChannelID: "l-1", Running: true},
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			platform := tc.platform
			if platform == "" {
				platform = types.PlatformLocal
			}
			s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1", DirPath: "/p", LearnOverride: tc.override, Platform: platform}, nil)
			s.store.On("GetLearnChannel", mock.Anything, "ch-1").Return(tc.learnCh, nil)
			s.srv.configs.load = func() (*config.Config, error) { return &config.Config{}, nil }
			s.srv.configs.loadProject = func(_ string, base *config.Config) (*config.Config, error) {
				merged := *base
				merged.Learn.Enabled = tc.def
				return &merged, nil
			}
			if tc.learnCh != nil {
				tracker := new(MockLearnPassTracker)
				tracker.On("IsLearnPassRunning", "l-1").Return(tc.running)
				s.srv.SetLearnPassTracker(tracker)
			}

			w := s.learnRequest("GET", "ch-1", "")
			require.Equal(s.T(), http.StatusOK, w.Code)
			var resp learnStateResponse
			require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
			require.Equal(s.T(), tc.want, resp)
		})
	}
}

func (s *ServerSuite) TestLearnGetConfigLoadError() {
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1"}, nil)
	s.store.On("GetLearnChannel", mock.Anything, "ch-1").Return(nil, nil)
	s.srv.configs.load = func() (*config.Config, error) { return nil, os.ErrNotExist }

	w := s.learnRequest("GET", "ch-1", "")
	require.Equal(s.T(), http.StatusOK, w.Code)
	var resp learnStateResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.False(s.T(), resp.DefaultLearn)
}

func (s *ServerSuite) TestLearnGetErrors() {
	s.store.On("GetChannel", mock.Anything, "err").Return(nil, os.ErrPermission)
	s.store.On("GetChannel", mock.Anything, "gone").Return(nil, nil)
	s.store.On("GetChannel", mock.Anything, "l-1").Return(&db.Channel{ChannelID: "l-1", Kind: db.ChannelKindLearn}, nil)
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1"}, nil)
	s.store.On("GetLearnChannel", mock.Anything, "ch-1").Return(nil, os.ErrPermission)

	require.Equal(s.T(), http.StatusInternalServerError, s.learnRequest("GET", "err", "").Code)
	require.Equal(s.T(), http.StatusNotFound, s.learnRequest("GET", "gone", "").Code)
	require.Equal(s.T(), http.StatusNotFound, s.learnRequest("GET", "l-1", "").Code, "a learn thread has no learn switch")
	require.Equal(s.T(), http.StatusInternalServerError, s.learnRequest("GET", "ch-1", "").Code)

	s.srv.store = nil
	require.Equal(s.T(), http.StatusNotImplemented, s.learnRequest("GET", "ch-1", "").Code)
}

func (s *ServerSuite) TestLearnSet() {
	for _, v := range []string{db.LearnOn, db.LearnOff, ""} {
		s.Run(v, func() {
			s.SetupTest()
			s.srv.eventsHub = NewEventsHub(testLogger())
			s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1", Platform: types.PlatformLocal}, nil)
			s.store.On("UpdateChannelLearnOverride", mock.Anything, "ch-1", v).Return(nil)

			w := s.learnRequest("PUT", "ch-1", `{"learn":"`+v+`"}`)
			require.Equal(s.T(), http.StatusNoContent, w.Code)
			s.store.AssertExpectations(s.T())
		})
	}
}

func (s *ServerSuite) TestLearnSetErrors() {
	s.store.On("GetChannel", mock.Anything, "gone").Return(nil, nil)
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1", Platform: types.PlatformLocal}, nil)
	s.store.On("GetChannel", mock.Anything, "slack-1").Return(&db.Channel{ChannelID: "slack-1", Platform: types.PlatformSlack}, nil)
	s.store.On("GetChannel", mock.Anything, "task-1").Return(&db.Channel{ChannelID: "task-1", Platform: types.PlatformLocal, TaskID: 7}, nil)
	s.store.On("UpdateChannelLearnOverride", mock.Anything, "ch-1", "on").Return(os.ErrPermission)

	w := s.learnRequest("PUT", "ch-1", `{"learn":"maybe"}`)
	require.Equal(s.T(), http.StatusBadRequest, w.Code)
	require.Contains(s.T(), w.Body.String(), "invalid learn")
	require.Equal(s.T(), http.StatusBadRequest, s.learnRequest("PUT", "ch-1", `{`).Code)
	require.Equal(s.T(), http.StatusNotFound, s.learnRequest("PUT", "gone", `{"learn":"on"}`).Code)
	w = s.learnRequest("PUT", "slack-1", `{"learn":"on"}`)
	require.Equal(s.T(), http.StatusBadRequest, w.Code)
	require.Contains(s.T(), w.Body.String(), "desktop app channels")
	w = s.learnRequest("PUT", "task-1", `{"learn":"on"}`)
	require.Equal(s.T(), http.StatusBadRequest, w.Code)
	require.Contains(s.T(), w.Body.String(), "task threads")
	require.Equal(s.T(), http.StatusInternalServerError, s.learnRequest("PUT", "ch-1", `{"learn":"on"}`).Code)
}
