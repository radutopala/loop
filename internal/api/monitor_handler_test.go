package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/monitor"
)

type mockMonitors struct{ mock.Mock }

func (m *mockMonitors) Start(channelID, dirPath, parentDirPath, command, description string, timeout time.Duration, interrupt bool) (*monitor.Monitor, error) {
	args := m.Called(channelID, dirPath, parentDirPath, command, description, timeout, interrupt)
	mon, _ := args.Get(0).(*monitor.Monitor)
	return mon, args.Error(1)
}

func (m *mockMonitors) List(channelID string) []monitor.Monitor {
	return m.Called(channelID).Get(0).([]monitor.Monitor)
}

func (m *mockMonitors) Cancel(channelID, id string) bool {
	return m.Called(channelID, id).Bool(0)
}

type MonitorHandlerSuite struct {
	suite.Suite
	srv      *Server
	store    *MockChannelLister
	monitors *mockMonitors
	mux      *http.ServeMux
}

func TestMonitorHandlerSuite(t *testing.T) {
	suite.Run(t, new(MonitorHandlerSuite))
}

func (s *MonitorHandlerSuite) SetupTest() {
	s.srv = newServerForReviewTests(s.T())
	s.store = s.srv.store.(*MockChannelLister)
	s.monitors = new(mockMonitors)
	s.srv.SetMonitors(s.monitors)
	s.mux = s.srv.buildMux()
}

func (s *MonitorHandlerSuite) do(method, path string, body any) *httptest.ResponseRecorder {
	var buf []byte
	if body != nil {
		var err error
		buf, err = json.Marshal(body)
		require.NoError(s.T(), err)
	}
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, httptest.NewRequest(method, path, bytes.NewReader(buf)))
	return w
}

func (s *MonitorHandlerSuite) TestNotConfigured() {
	s.srv.monitors = nil
	for _, req := range []struct{ method, path string }{
		{http.MethodPost, "/api/channels/ch1/monitors"},
		{http.MethodGet, "/api/channels/ch1/monitors"},
		{http.MethodDelete, "/api/channels/ch1/monitors/mon-a"},
	} {
		require.Equal(s.T(), http.StatusNotImplemented, s.do(req.method, req.path, nil).Code, req.method)
	}
}

func (s *MonitorHandlerSuite) TestStartBadRequest() {
	tests := []struct {
		name string
		body any
	}{
		{"not an object", "nope"},
		{"no command", map[string]any{"description": "wait"}},
		{"no description", map[string]any{"command": "sleep 1", "description": "  "}},
		{"negative timeout", map[string]any{"command": "sleep 1", "description": "wait", "timeout_sec": -1}},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			require.Equal(s.T(), http.StatusBadRequest, s.do(http.MethodPost, "/api/channels/ch1/monitors", tt.body).Code)
		})
	}
}

func (s *MonitorHandlerSuite) TestStartUnknownChannel() {
	s.store.On("GetChannel", mock.Anything, "ch1").Return(nil, nil)
	w := s.do(http.MethodPost, "/api/channels/ch1/monitors", map[string]any{"command": "sleep 1", "description": "wait"})
	require.Equal(s.T(), http.StatusBadRequest, w.Code)
	require.Contains(s.T(), w.Body.String(), "channel ch1 not found")
}

func (s *MonitorHandlerSuite) TestStart() {
	mon := &monitor.Monitor{ID: "mon-a", ChannelID: "ch1", Description: "wait", Command: "sleep 1", TimeoutSec: 90, Interrupt: true}
	tests := []struct {
		name string
		err  error
		code int
	}{
		{"started", nil, http.StatusCreated},
		{"limit", monitor.ErrLimit, http.StatusConflict},
		{"stopped", monitor.ErrStopped, http.StatusServiceUnavailable},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			s.store.On("GetChannel", mock.Anything, "ch1").Return(&db.Channel{ChannelID: "ch1", DirPath: "/repo"}, nil)
			var ret *monitor.Monitor
			if tt.err == nil {
				ret = mon
			}
			s.monitors.On("Start", "ch1", "/repo", "", "sleep 1", "wait", 90*time.Second, true).Return(ret, tt.err)

			w := s.do(http.MethodPost, "/api/channels/ch1/monitors", map[string]any{"command": " sleep 1 ", "description": "wait", "timeout_sec": 90, "interrupt": true})
			require.Equal(s.T(), tt.code, w.Code, w.Body.String())
			if tt.err != nil {
				require.Contains(s.T(), w.Body.String(), tt.err.Error())
				return
			}
			var got monitor.Monitor
			require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &got))
			require.Equal(s.T(), *mon, got)
		})
	}
}

func (s *MonitorHandlerSuite) TestList() {
	s.monitors.On("List", "ch1").Return([]monitor.Monitor{{ID: "mon-a", ChannelID: "ch1"}})
	w := s.do(http.MethodGet, "/api/channels/ch1/monitors", nil)
	require.Equal(s.T(), http.StatusOK, w.Code)
	require.JSONEq(s.T(), `{"monitors":[{"id":"mon-a","channel_id":"ch1","description":"","command":"","started_at":"0001-01-01T00:00:00Z","timeout_sec":0,"interrupt":false}]}`, w.Body.String())
}

func (s *MonitorHandlerSuite) TestCancel() {
	s.monitors.On("Cancel", "ch1", "mon-a").Return(true)
	s.monitors.On("Cancel", "ch1", "mon-b").Return(false)
	require.Equal(s.T(), http.StatusNoContent, s.do(http.MethodDelete, "/api/channels/ch1/monitors/mon-a", nil).Code)
	require.Equal(s.T(), http.StatusNotFound, s.do(http.MethodDelete, "/api/channels/ch1/monitors/mon-b", nil).Code)
}
