package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/stretchr/testify/require"
)

// playgroundStateDir makes the global playground my-app and returns its dir.
func (s *ServerSuite) playgroundStateDir() string {
	pgDir := filepath.Join(s.setPlaygroundDir(), "playground", "my-app")
	require.NoError(s.T(), os.MkdirAll(pgDir, 0o755))
	return pgDir
}

func (s *ServerSuite) TestPlaygroundStateRoundTrip() {
	pgDir := s.playgroundStateDir()
	hub := NewEventsHub(testLogger())
	var events []Event
	hub.captureHook = func(e Event) { events = append(events, e) }
	s.srv.SetEventsHub(hub)

	rec := s.testRequest("GET", "/api/playground/state?name=my-app", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	require.JSONEq(s.T(), `{}`, rec.Body.String())

	rec = s.testRequest("PATCH", "/api/playground/state?name=my-app", `{"frame":3,"color":"red","gone":1}`)
	require.Equal(s.T(), http.StatusOK, rec.Code)
	require.JSONEq(s.T(), `{"frame":3,"color":"red","gone":1}`, rec.Body.String())

	// A patch merges; null removes a key.
	rec = s.testRequest("PATCH", "/api/playground/state?name=my-app", `{"frame":4,"gone":null}`)
	require.Equal(s.T(), http.StatusOK, rec.Code)
	require.JSONEq(s.T(), `{"frame":4,"color":"red"}`, rec.Body.String())

	data, err := os.ReadFile(filepath.Join(pgDir, "state.json"))
	require.NoError(s.T(), err)
	require.JSONEq(s.T(), `{"frame":4,"color":"red"}`, string(data))

	rec = s.testRequest("GET", "/api/playground/state?name=my-app", "")
	require.JSONEq(s.T(), `{"frame":4,"color":"red"}`, rec.Body.String())

	// Each write tells the panels, as a state change, not a code change.
	require.Len(s.T(), events, 2)
	require.Equal(s.T(), EventPlaygroundUpdate, events[1].Type)
	require.Equal(s.T(), map[string]string{"name": "my-app", "scope": "global", "channel_id": "", "kind": "state"}, events[1].Data)
}

func (s *ServerSuite) TestPlaygroundStateNotAnObject() {
	pgDir := s.playgroundStateDir()
	for _, content := range []string{`[1,2]`, `null`, `{broken`} {
		s.Run(content, func() {
			require.NoError(s.T(), os.WriteFile(filepath.Join(pgDir, "state.json"), []byte(content), 0o644))
			rec := s.testRequest("GET", "/api/playground/state?name=my-app", "")
			require.Equal(s.T(), http.StatusOK, rec.Code)
			require.JSONEq(s.T(), `{}`, rec.Body.String())
		})
	}
}

func (s *ServerSuite) TestPlaygroundStateBadRequests() {
	s.playgroundStateDir()
	tests := []struct {
		name   string
		method string
		url    string
		body   string
		status int
	}{
		{name: "get an unknown playground", method: "GET", url: "/api/playground/state?name=missing", status: http.StatusNotFound},
		{name: "patch an unknown playground", method: "PATCH", url: "/api/playground/state?name=missing", body: `{}`, status: http.StatusNotFound},
		{name: "a name out of the playgrounds", method: "GET", url: "/api/playground/state?name=..", status: http.StatusBadRequest},
		{name: "project scope without a channel", method: "GET", url: "/api/playground/state?name=my-app&scope=project", status: http.StatusBadRequest},
		{name: "an array", method: "PATCH", url: "/api/playground/state?name=my-app", body: `[1]`, status: http.StatusBadRequest},
		{name: "null", method: "PATCH", url: "/api/playground/state?name=my-app", body: `null`, status: http.StatusBadRequest},
		{name: "a body over 1 MiB", method: "PATCH", url: "/api/playground/state?name=my-app", body: `{"a":"` + strings.Repeat("x", maxPlaygroundStateBytes) + `"}`, status: http.StatusBadRequest},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			rec := s.testRequest(tc.method, tc.url, tc.body)
			require.Equal(s.T(), tc.status, rec.Code)
		})
	}
}

func (s *ServerSuite) TestPlaygroundStateTooLarge() {
	pgDir := s.playgroundStateDir()
	half, _ := json.Marshal(map[string]string{"a": strings.Repeat("x", maxPlaygroundStateBytes/2)})
	rec := s.testRequest("PATCH", "/api/playground/state?name=my-app", string(half))
	require.Equal(s.T(), http.StatusOK, rec.Code)

	other, _ := json.Marshal(map[string]string{"b": strings.Repeat("y", maxPlaygroundStateBytes/2)})
	rec = s.testRequest("PATCH", "/api/playground/state?name=my-app", string(other))
	require.Equal(s.T(), http.StatusRequestEntityTooLarge, rec.Code)

	// The state is as it was.
	data, err := os.ReadFile(filepath.Join(pgDir, "state.json"))
	require.NoError(s.T(), err)
	require.Equal(s.T(), string(half), strings.Join(strings.Fields(string(data)), ""))
}

func (s *ServerSuite) TestPlaygroundStateReadError() {
	pgDir := s.playgroundStateDir()
	// A directory where the file goes can't be read.
	require.NoError(s.T(), os.Mkdir(filepath.Join(pgDir, "state.json"), 0o755))

	rec := s.testRequest("GET", "/api/playground/state?name=my-app", "")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	rec = s.testRequest("PATCH", "/api/playground/state?name=my-app", `{"a":1}`)
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
}

func (s *ServerSuite) TestPlaygroundStateWriteError() {
	s.playgroundStateDir()
	s.srv.playground.writeStateFile = func(string, []byte, os.FileMode) error { return errors.New("disk full") }

	rec := s.testRequest("PATCH", "/api/playground/state?name=my-app", `{"a":1}`)
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
}

func (s *ServerSuite) TestPlaygroundStateRefusesALinkOut() {
	pgDir := s.playgroundStateDir()
	outside := filepath.Join(s.T().TempDir(), "secret.json")
	require.NoError(s.T(), os.WriteFile(outside, []byte(`{"token":"x"}`), 0o644))
	require.NoError(s.T(), os.Symlink(outside, filepath.Join(pgDir, "state.json")))

	rec := s.testRequest("GET", "/api/playground/state?name=my-app", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
	rec = s.testRequest("PATCH", "/api/playground/state?name=my-app", `{"a":1}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
	data, err := os.ReadFile(outside)
	require.NoError(s.T(), err)
	require.Equal(s.T(), `{"token":"x"}`, string(data))
}
