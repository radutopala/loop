package api

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/explain"
	"github.com/radutopala/loop/internal/types"
)

type MockExplainer struct {
	mock.Mock
}

func (m *MockExplainer) Explain(ctx context.Context, ch *db.Channel, messageID string, force bool) (*db.Explanation, error) {
	args := m.Called(ctx, ch, messageID, force)
	e, _ := args.Get(0).(*db.Explanation)
	return e, args.Error(1)
}

func (s *ServerSuite) explainRequest(method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/api/channels/"+path, strings.NewReader(body))
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)
	return w
}

func (s *ServerSuite) TestExplainGet() {
	tests := []struct {
		name     string
		override string
		platform types.Platform
		def      bool
		loadErr  error
		want     explainStateResponse
	}{
		{name: "inherits off", want: explainStateResponse{Available: true}},
		{name: "config load error falls back to off", loadErr: os.ErrNotExist, want: explainStateResponse{Available: true}},
		{name: "inherits on", def: true, want: explainStateResponse{Available: true, DefaultExplain: true, Enabled: true}},
		{name: "override on", override: db.LearnOn, want: explainStateResponse{Available: true, Explain: "on", Enabled: true}},
		{name: "override off", override: db.LearnOff, def: true, want: explainStateResponse{Available: true, Explain: "off", DefaultExplain: true}},
		{name: "slack channel never explains", override: db.LearnOn, platform: types.PlatformSlack, want: explainStateResponse{Explain: "on"}},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			platform := tc.platform
			if platform == "" {
				platform = types.PlatformLocal
			}
			s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1", DirPath: "/p", ExplainOverride: tc.override, Platform: platform}, nil)
			s.srv.configs.load = func() (*config.Config, error) {
				if tc.loadErr != nil {
					return nil, tc.loadErr
				}
				return &config.Config{}, nil
			}
			s.srv.configs.loadProject = func(_ string, base *config.Config) (*config.Config, error) {
				merged := *base
				merged.Explain.Enabled = tc.def
				return &merged, nil
			}

			w := s.explainRequest("GET", "ch-1/explain", "")
			require.Equal(s.T(), http.StatusOK, w.Code)
			var resp explainStateResponse
			require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
			require.Equal(s.T(), tc.want, resp)
		})
	}
}

func (s *ServerSuite) TestExplainGetHiddenThread() {
	s.store.On("GetChannel", mock.Anything, "x-1").Return(&db.Channel{ChannelID: "x-1", Kind: db.ChannelKindExplain}, nil)
	require.Equal(s.T(), http.StatusNotFound, s.explainRequest("GET", "x-1/explain", "").Code, "an explain thread has no explain switch")
}

func (s *ServerSuite) TestExplainSet() {
	for _, v := range []string{db.LearnOn, db.LearnOff, ""} {
		s.Run(v, func() {
			s.SetupTest()
			s.srv.eventsHub = NewEventsHub(testLogger())
			s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1", Platform: types.PlatformLocal}, nil)
			s.store.On("UpdateChannelExplainOverride", mock.Anything, "ch-1", v).Return(nil)

			w := s.explainRequest("PUT", "ch-1/explain", `{"explain":"`+v+`"}`)
			require.Equal(s.T(), http.StatusNoContent, w.Code)
			s.store.AssertExpectations(s.T())
		})
	}
}

func (s *ServerSuite) TestExplainSetErrors() {
	s.store.On("GetChannel", mock.Anything, "gone").Return(nil, nil)
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1", Platform: types.PlatformLocal}, nil)
	s.store.On("GetChannel", mock.Anything, "slack-1").Return(&db.Channel{ChannelID: "slack-1", Platform: types.PlatformSlack}, nil)
	s.store.On("UpdateChannelExplainOverride", mock.Anything, "ch-1", "on").Return(os.ErrPermission)

	w := s.explainRequest("PUT", "ch-1/explain", `{"explain":"maybe"}`)
	require.Equal(s.T(), http.StatusBadRequest, w.Code)
	require.Contains(s.T(), w.Body.String(), "invalid explain")
	require.Equal(s.T(), http.StatusBadRequest, s.explainRequest("PUT", "ch-1/explain", `{`).Code)
	require.Equal(s.T(), http.StatusNotFound, s.explainRequest("PUT", "gone/explain", `{"explain":"on"}`).Code)
	w = s.explainRequest("PUT", "slack-1/explain", `{"explain":"on"}`)
	require.Equal(s.T(), http.StatusBadRequest, w.Code)
	require.Contains(s.T(), w.Body.String(), "desktop app channels")
	require.Equal(s.T(), http.StatusInternalServerError, s.explainRequest("PUT", "ch-1/explain", `{"explain":"on"}`).Code)
}

func (s *ServerSuite) TestListExplanations() {
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1"}, nil)
	s.store.On("GetChannel", mock.Anything, "ch-2").Return(&db.Channel{ChannelID: "ch-2"}, nil)
	s.store.On("GetChannel", mock.Anything, "ch-3").Return(&db.Channel{ChannelID: "ch-3"}, nil)
	s.store.On("GetChannel", mock.Anything, "gone").Return(nil, nil)
	s.store.On("ListExplanations", mock.Anything, "ch-1").Return([]*db.Explanation{{ID: 1, ChannelID: "ch-1", MessageID: "ask-1", Status: db.ExplainDone, Content: "## Summary"}}, nil)
	s.store.On("ListExplanations", mock.Anything, "ch-2").Return([]*db.Explanation(nil), nil)
	s.store.On("ListExplanations", mock.Anything, "ch-3").Return([]*db.Explanation(nil), os.ErrPermission)

	w := s.explainRequest("GET", "ch-1/explanations", "")
	require.Equal(s.T(), http.StatusOK, w.Code)
	var list []*db.Explanation
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &list))
	require.Len(s.T(), list, 1)
	require.Equal(s.T(), "ask-1", list[0].MessageID)

	w = s.explainRequest("GET", "ch-2/explanations", "")
	require.Equal(s.T(), http.StatusOK, w.Code)
	require.JSONEq(s.T(), `[]`, w.Body.String(), "no explanations lists as an empty array, not null")

	require.Equal(s.T(), http.StatusInternalServerError, s.explainRequest("GET", "ch-3/explanations", "").Code)
	require.Equal(s.T(), http.StatusNotFound, s.explainRequest("GET", "gone/explanations", "").Code)
}

func (s *ServerSuite) TestExplainNotConfigured() {
	require.Equal(s.T(), http.StatusNotImplemented, s.explainRequest("POST", "ch-1/explanations", `{"message_id":"ask-1"}`).Code)
}

func (s *ServerSuite) TestExplainPost() {
	ch := &db.Channel{ChannelID: "ch-1", Platform: types.PlatformLocal}
	tests := []struct {
		name     string
		body     string
		channel  string
		force    bool
		result   *db.Explanation
		err      error
		wantCode int
		wantBody string
	}{
		{name: "bad body", body: `{`, wantCode: http.StatusBadRequest},
		{name: "missing message id", body: `{}`, wantCode: http.StatusBadRequest, wantBody: "message_id is required"},
		{name: "channel gone", body: `{"message_id":"ask-1"}`, channel: "gone", wantCode: http.StatusNotFound},
		{
			name: "queues", body: `{"message_id":"ask-1"}`,
			result:   &db.Explanation{ID: 3, ChannelID: "ch-1", MessageID: "ask-1", Status: db.ExplainQueued},
			wantCode: http.StatusOK, wantBody: `"status":"queued"`,
		},
		{
			name: "re-explains", body: `{"message_id":"ask-1","force":true}`, force: true,
			result:   &db.Explanation{ID: 4, ChannelID: "ch-1", MessageID: "ask-1", Status: db.ExplainQueued},
			wantCode: http.StatusOK, wantBody: `"id":4`,
		},
		{name: "unavailable", body: `{"message_id":"ask-1"}`, err: explain.ErrUnavailable, wantCode: http.StatusBadRequest},
		{name: "not a turn", body: `{"message_id":"ask-1"}`, err: explain.ErrNotATurn, wantCode: http.StatusBadRequest},
		{name: "no session", body: `{"message_id":"ask-1"}`, err: explain.ErrNoSession, wantCode: http.StatusConflict},
		{name: "parent gone", body: `{"message_id":"ask-1"}`, err: fmt.Errorf("thread: %w", db.ErrParentGone), wantCode: http.StatusNotFound},
		{name: "store error", body: `{"message_id":"ask-1"}`, err: errors.New("disk full"), wantCode: http.StatusInternalServerError, wantBody: "disk full"},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			explainer := new(MockExplainer)
			s.srv.SetExplainer(explainer)
			s.store.On("GetChannel", mock.Anything, "ch-1").Return(ch, nil).Maybe()
			s.store.On("GetChannel", mock.Anything, "gone").Return(nil, nil).Maybe()
			explainer.On("Explain", mock.Anything, ch, "ask-1", tc.force).Return(tc.result, tc.err).Maybe()

			channel := cmp.Or(tc.channel, "ch-1")
			w := s.explainRequest("POST", channel+"/explanations", tc.body)
			require.Equal(s.T(), tc.wantCode, w.Code, w.Body.String())
			if tc.wantBody != "" {
				require.Contains(s.T(), w.Body.String(), tc.wantBody)
			}
			if tc.result != nil || tc.err != nil {
				explainer.AssertExpectations(s.T())
			}
		})
	}
}
