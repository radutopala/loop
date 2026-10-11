package pluginsdk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type ServerSuite struct {
	suite.Suite
	srv  *Server
	h    http.Handler
	logs *bytes.Buffer

	mu      sync.Mutex
	actions []ActionRequest
}

func TestServerSuite(t *testing.T) {
	suite.Run(t, new(ServerSuite))
}

func (s *ServerSuite) SetupTest() {
	s.logs = &bytes.Buffer{}
	s.actions = nil
	s.srv = &Server{
		Secret:  "sekret",
		Version: "1.2.3",
		Logger:  slog.New(slog.NewTextHandler(s.logs, nil)),
		Actions: map[string]ActionFunc{
			"daily": func(_ context.Context, req ActionRequest) error {
				s.mu.Lock()
				defer s.mu.Unlock()
				s.actions = append(s.actions, req)
				return nil
			},
			"broken": func(context.Context, ActionRequest) error { return errors.New("no quiz today") },
		},
		Tools: map[string]ToolFunc{
			"echo": func(_ context.Context, req ToolRequest) (ToolResult, error) {
				return ToolResult{Content: req.RunID + ":" + string(req.Input)}, nil
			},
			"broken": func(context.Context, ToolRequest) (ToolResult, error) { return ToolResult{}, errors.New("store down") },
		},
		RunDone: func(_ context.Context, runID string, req RunDoneRequest) error {
			if runID == "bad" {
				return errors.New("unknown run")
			}
			return nil
		},
		X: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b, err := io.ReadAll(r.Body)
			if err != nil {
				WriteError(w, http.StatusRequestEntityTooLarge, err.Error())
				return
			}
			WriteJSON(w, http.StatusOK, map[string]string{"path": r.URL.Path, "method": r.Method, "body": string(b)})
		}),
	}
	s.h = s.srv.Handler()
	s.T().Cleanup(s.srv.Close)
}

func (s *ServerSuite) call(method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer sekret")
	w := httptest.NewRecorder()
	s.h.ServeHTTP(w, r)
	return w
}

func (s *ServerSuite) TestSecret() {
	for _, auth := range []string{"", "Bearer nope", "sekret"} {
		r := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		r.Header.Set("Authorization", auth)
		w := httptest.NewRecorder()
		s.h.ServeHTTP(w, r)
		require.Equal(s.T(), http.StatusUnauthorized, w.Code, auth)
	}
	s.srv = &Server{}
	s.h = s.srv.Handler()
	r := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	r.Header.Set("Authorization", "Bearer ")
	w := httptest.NewRecorder()
	s.h.ServeHTTP(w, r)
	require.Equal(s.T(), http.StatusUnauthorized, w.Code, "a server without a secret refuses everything")
}

func (s *ServerSuite) TestHealthz() {
	w := s.call(http.MethodGet, "/healthz", "")
	require.Equal(s.T(), http.StatusOK, w.Code)
	require.Equal(s.T(), "application/json", w.Header().Get("Content-Type"))
	require.JSONEq(s.T(), `{"api":"1","version":"1.2.3"}`, w.Body.String())
}

func (s *ServerSuite) TestActions() {
	require.Equal(s.T(), http.StatusAccepted, s.call(http.MethodPost, "/actions/daily", "").Code)
	require.Equal(s.T(), http.StatusAccepted, s.call(http.MethodPost, "/actions/daily", `{"trigger":"schedule","task_id":7}`).Code)
	require.Equal(s.T(), http.StatusAccepted, s.call(http.MethodPost, "/actions/broken", "{").Code)
	w := s.call(http.MethodPost, "/actions/nope", "")
	require.Equal(s.T(), http.StatusNotFound, w.Code)
	require.JSONEq(s.T(), `{"error":"unknown action"}`, w.Body.String())

	s.srv.Wait()
	require.ElementsMatch(s.T(), []ActionRequest{{Trigger: TriggerManual}, {Trigger: TriggerSchedule, TaskID: 7}}, s.actions)
	require.Contains(s.T(), s.logs.String(), `msg="action broken" err="no quiz today"`)
}

func (s *ServerSuite) TestTools() {
	tests := []struct {
		name, path, body string
		status           int
		want             string
	}{
		{"answer", "/tools/echo", `{"run_id":"r1","input":{"a":1}}`, http.StatusOK, `{"content":"r1:{\"a\":1}","is_error":false}`},
		{"error", "/tools/broken", `{}`, http.StatusOK, `{"content":"store down","is_error":true}`},
		{"unknown", "/tools/nope", `{}`, http.StatusNotFound, `{"content":"unknown tool","is_error":true}`},
		{"bad body", "/tools/echo", `{`, http.StatusBadRequest, `{"error":"invalid JSON body: unexpected EOF"}`},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			w := s.call(http.MethodPost, tt.path, tt.body)
			require.Equal(s.T(), tt.status, w.Code)
			require.JSONEq(s.T(), tt.want, w.Body.String())
		})
	}
	require.Contains(s.T(), s.logs.String(), `msg="tool broken" err="store down"`)
}

func (s *ServerSuite) TestRunDone() {
	require.Equal(s.T(), http.StatusNoContent, s.call(http.MethodPost, "/runs/r1/done", `{"status":"done"}`).Code)
	w := s.call(http.MethodPost, "/runs/bad/done", `{"status":"failed","error":"x"}`)
	require.Equal(s.T(), http.StatusInternalServerError, w.Code)
	require.JSONEq(s.T(), `{"error":"unknown run"}`, w.Body.String())
	require.Equal(s.T(), http.StatusBadRequest, s.call(http.MethodPost, "/runs/r1/done", `nope`).Code)

	s.srv.RunDone = nil
	require.Equal(s.T(), http.StatusNoContent, s.call(http.MethodPost, "/runs/bad/done", `{"status":"done"}`).Code, "without RunDone the end is ignored")
}

func (s *ServerSuite) TestX() {
	w := s.call(http.MethodPut, "/x/quizzes/3?q=1", `{"a":1}`)
	require.Equal(s.T(), http.StatusOK, w.Code)
	require.JSONEq(s.T(), `{"path":"/quizzes/3","method":"PUT","body":"{\"a\":1}"}`, w.Body.String())

	w = s.call(http.MethodPost, "/x/big", strings.Repeat("a", MaxXBody+1))
	require.Equal(s.T(), http.StatusRequestEntityTooLarge, w.Code)

	s.srv = &Server{Secret: "sekret"}
	s.h = s.srv.Handler()
	require.Equal(s.T(), http.StatusNotFound, s.call(http.MethodGet, "/x/quizzes", "").Code, "a plugin without X has no /x")
}

func (s *ServerSuite) TestGo() {
	s.srv.WorkTimeout = time.Millisecond
	s.srv.Go("slow", func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})
	s.srv.Wait()
	require.Contains(s.T(), s.logs.String(), `msg=slow err="context deadline exceeded"`)

	s.srv.WorkTimeout = 0
	started := make(chan struct{})
	s.srv.Go("waits", func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		return nil
	})
	<-started
	s.srv.Close()
}

func (s *ServerSuite) TestDecodeJSON() {
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"a":1}`))
	w := httptest.NewRecorder()
	var v map[string]int
	require.True(s.T(), DecodeJSON(w, r, &v))
	require.Equal(s.T(), map[string]int{"a": 1}, v)

	var out Health
	require.NoError(s.T(), json.Unmarshal(s.call(http.MethodGet, "/healthz", "").Body.Bytes(), &out))
	require.Equal(s.T(), Health{API: APIVersion, Version: "1.2.3"}, out)
}

func (s *ServerSuite) TestCloseWithoutHandler() {
	srv := &Server{}
	srv.Close()
}
