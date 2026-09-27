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
		def      bool
		learnCh  *db.Channel
		running  bool
		want     learnStateResponse
	}{
		{name: "inherits off", want: learnStateResponse{}},
		{name: "inherits on", def: true, want: learnStateResponse{DefaultLearn: true, Enabled: true}},
		{name: "override on", override: db.LearnOn, want: learnStateResponse{Learn: "on", Enabled: true}},
		{name: "override off", override: db.LearnOff, def: true, want: learnStateResponse{Learn: "off", DefaultLearn: true}},
		{
			name: "learn thread idle", override: db.LearnOn, learnCh: &db.Channel{ChannelID: "l-1"},
			want: learnStateResponse{Learn: "on", Enabled: true, LearnChannelID: "l-1"},
		},
		{
			name: "learn thread running", override: db.LearnOn, learnCh: &db.Channel{ChannelID: "l-1"}, running: true,
			want: learnStateResponse{Learn: "on", Enabled: true, LearnChannelID: "l-1", Running: true},
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1", DirPath: "/p", LearnOverride: tc.override}, nil)
			s.store.On("GetLearnChannel", mock.Anything, "ch-1").Return(tc.learnCh, nil)
			s.srv.configs.load = func() (*config.Config, error) { return &config.Config{}, nil }
			s.srv.configs.loadProject = func(_ string, base *config.Config) (*config.Config, error) {
				merged := *base
				merged.Learn.Enabled = tc.def
				return &merged, nil
			}
			if tc.running {
				chatLister := new(MockActiveChatLister)
				chatLister.On("ActiveChatChannelIDs").Return(map[string]struct{}{"l-1": {}})
				s.srv.SetActiveChatLister(chatLister)
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
			s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1"}, nil)
			s.store.On("UpdateChannelLearnOverride", mock.Anything, "ch-1", v).Return(nil)

			w := s.learnRequest("PUT", "ch-1", `{"learn":"`+v+`"}`)
			require.Equal(s.T(), http.StatusNoContent, w.Code)
			s.store.AssertExpectations(s.T())
		})
	}
}

func (s *ServerSuite) TestLearnSetErrors() {
	s.store.On("GetChannel", mock.Anything, "gone").Return(nil, nil)
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1"}, nil)
	s.store.On("UpdateChannelLearnOverride", mock.Anything, "ch-1", "on").Return(os.ErrPermission)

	w := s.learnRequest("PUT", "ch-1", `{"learn":"maybe"}`)
	require.Equal(s.T(), http.StatusBadRequest, w.Code)
	require.Contains(s.T(), w.Body.String(), "invalid learn")
	require.Equal(s.T(), http.StatusBadRequest, s.learnRequest("PUT", "ch-1", `{`).Code)
	require.Equal(s.T(), http.StatusNotFound, s.learnRequest("PUT", "gone", `{"learn":"on"}`).Code)
	require.Equal(s.T(), http.StatusInternalServerError, s.learnRequest("PUT", "ch-1", `{"learn":"on"}`).Code)
}
