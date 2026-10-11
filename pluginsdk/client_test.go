package pluginsdk

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type ClientSuite struct {
	suite.Suite
	srv    *httptest.Server
	client *Client
	// handle answers the request the test makes.
	handle func(w http.ResponseWriter, r *http.Request)
	got    *http.Request
	body   string
}

func TestClientSuite(t *testing.T) {
	suite.Run(t, new(ClientSuite))
}

func (s *ClientSuite) SetupTest() {
	s.handle = nil
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		s.got, s.body = r, string(b)
		s.handle(w, r)
	}))
	s.T().Cleanup(s.srv.Close)
	token := filepath.Join(s.T().TempDir(), "token")
	require.NoError(s.T(), os.WriteFile(token, []byte("tok\n"), 0o600))
	s.client = NewClient(s.srv.URL+"/", token)
}

func (s *ClientSuite) answer(status int, body string) {
	s.handle = func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}
}

func (s *ClientSuite) requireCall(method, path, query string) {
	require.Equal(s.T(), method, s.got.Method)
	require.Equal(s.T(), "/api/plugin/v1"+path, s.got.URL.Path)
	require.Equal(s.T(), query, s.got.URL.RawQuery)
	require.Equal(s.T(), "Bearer tok", s.got.Header.Get("Authorization"))
}

func (s *ClientSuite) TestReads() {
	ctx := context.Background()
	tests := []struct {
		name   string
		answer string
		call   func() (any, error)
		method string
		path   string
		query  string
		want   any
	}{
		{
			name:   "channel",
			answer: `{"id":"ch1","name":"loop","dir_path":"/src","platform":"local","model":"opus","effort":"high","settings":{"n":3}}`,
			call:   func() (any, error) { return s.client.Channel(ctx) },
			method: http.MethodGet, path: "/channel",
			want: &Channel{ID: "ch1", Name: "loop", DirPath: "/src", Platform: "local", Model: "opus", Effort: "high", Settings: map[string]any{"n": float64(3)}},
		},
		{
			name:   "file range",
			answer: `{"path":"a.go","content":"x","start":2,"end":3,"total_lines":9}`,
			call:   func() (any, error) { return s.client.File(ctx, "a.go", 2, 3) },
			method: http.MethodGet, path: "/channel/files", query: "end=3&path=a.go&start=2",
			want: &File{Path: "a.go", Content: "x", Start: 2, End: 3, TotalLines: 9},
		},
		{
			name:   "whole file",
			answer: `{"path":"a.go"}`,
			call:   func() (any, error) { return s.client.File(ctx, "a.go", 0, 0) },
			method: http.MethodGet, path: "/channel/files", query: "path=a.go",
			want: &File{Path: "a.go"},
		},
		{
			name:   "tree",
			answer: `{"files":["a.go"],"truncated":true}`,
			call:   func() (any, error) { return s.client.Tree(ctx, "*.go") },
			method: http.MethodGet, path: "/channel/tree", query: "glob=%2A.go",
			want: &Tree{Files: []string{"a.go"}, Truncated: true},
		},
		{
			name:   "whole tree",
			answer: `{"files":[]}`,
			call:   func() (any, error) { return s.client.Tree(ctx, "") },
			method: http.MethodGet, path: "/channel/tree",
			want: &Tree{Files: []string{}},
		},
		{
			name:   "git recent",
			answer: `{"commits":[{"sha":"abc","subject":"s","author":"a","time":"t"}],"files":[{"path":"a.go","commits":2}]}`,
			call:   func() (any, error) { return s.client.GitRecent(ctx, 7) },
			method: http.MethodGet, path: "/channel/git/recent", query: "days=7",
			want: &GitRecent{Commits: []Commit{{SHA: "abc", Subject: "s", Author: "a", Time: "t"}}, Files: []FileChurn{{Path: "a.go", Commits: 2}}},
		},
		{
			name:   "hotspots",
			answer: `{"functions":[{"path":"a.go","name":"f","start_line":3,"cyclomatic":4,"cognitive":5,"max_nesting":2,"param_count":1,"loc":20,"score":0.5}]}`,
			call:   func() (any, error) { return s.client.Hotspots(ctx, 0) },
			method: http.MethodGet, path: "/channel/quality/hotspots",
			want: []Hotspot{{Path: "a.go", Name: "f", StartLine: 3, Cyclomatic: 4, Cognitive: 5, MaxNesting: 2, ParamCount: 1, LOC: 20, Score: 0.5}},
		},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.answer(http.StatusOK, tt.answer)
			got, err := tt.call()
			require.NoError(s.T(), err)
			require.Equal(s.T(), tt.want, got)
			s.requireCall(tt.method, tt.path, tt.query)
		})
	}
}

func (s *ClientSuite) TestWrites() {
	ctx := context.Background()

	s.answer(http.StatusAccepted, `{"run_id":"r1","thread_id":"t1"}`)
	run, err := s.client.StartRun(ctx, RunRequest{Agent: "gen", Prompt: "go", Model: "opus"})
	require.NoError(s.T(), err)
	require.Equal(s.T(), &Run{RunID: "r1", ThreadID: "t1"}, run)
	s.requireCall(http.MethodPost, "/runs", "")
	require.Equal(s.T(), "application/json", s.got.Header.Get("Content-Type"))
	require.JSONEq(s.T(), `{"agent":"gen","prompt":"go","model":"opus"}`, s.body)

	s.answer(http.StatusNoContent, "")
	require.NoError(s.T(), s.client.Event(ctx, "quiz.ready", map[string]int{"id": 1}))
	s.requireCall(http.MethodPost, "/events", "")
	require.JSONEq(s.T(), `{"type":"quiz.ready","data":{"id":1}}`, s.body)

	require.NoError(s.T(), s.client.Notify(ctx, "Quiz", "ready"))
	s.requireCall(http.MethodPost, "/notify", "")
	require.JSONEq(s.T(), `{"title":"Quiz","body":"ready"}`, s.body)

	require.NoError(s.T(), s.client.WriteMemory(ctx, "quiz-notes", "# Notes"))
	s.requireCall(http.MethodPut, "/memory/quiz-notes", "")
	require.JSONEq(s.T(), `{"content":"# Notes"}`, s.body)

	require.NoError(s.T(), s.client.DeleteMemory(ctx, "quiz-notes"))
	s.requireCall(http.MethodDelete, "/memory/quiz-notes", "")

	require.NoError(s.T(), s.client.SetBadge(ctx, "3 due", "3 quizzes due"))
	s.requireCall(http.MethodPut, "/badge", "")
	require.JSONEq(s.T(), `{"text":"3 due","title":"3 quizzes due"}`, s.body)

	require.NoError(s.T(), s.client.ClearBadge(ctx))
	s.requireCall(http.MethodDelete, "/badge", "")

	s.answer(http.StatusCreated, `{"msg_id":"card-1"}`)
	msgID, err := s.client.PostCard(ctx, "score", "Quiz", map[string]int{"score": 3})
	require.NoError(s.T(), err)
	require.Equal(s.T(), "card-1", msgID)
	s.requireCall(http.MethodPost, "/cards", "")
	require.JSONEq(s.T(), `{"card":"score","title":"Quiz","data":{"score":3}}`, s.body)
}

// deadlineDoer records how long the request had to run.
type deadlineDoer struct {
	next Doer
	left time.Duration
	set  bool
}

func (d *deadlineDoer) Do(r *http.Request) (*http.Response, error) {
	var at time.Time
	at, d.set = r.Context().Deadline()
	d.left = time.Until(at)
	return d.next.Do(r)
}

func (s *ClientSuite) TestRunUI() {
	tests := []struct {
		name     string
		timeout  time.Duration
		cmd      UICommand
		wantBody string
		wantWait time.Duration
	}{
		{
			name:     "the default timeout",
			timeout:  20 * time.Second,
			cmd:      UICommand{Steps: []UIStep{{"op": "select_channel", "channel_id": "ch1"}}},
			wantBody: `{"steps":[{"op":"select_channel","channel_id":"ch1"}]}`,
			wantWait: 70 * time.Second,
		},
		{
			name:     "a window and a timeout",
			timeout:  20 * time.Second,
			cmd:      UICommand{ClientID: "w1", Steps: []UIStep{{"op": "wait", "ms": 10}}, Timeout: 5 * time.Minute},
			wantBody: `{"client_id":"w1","steps":[{"op":"wait","ms":10}],"timeout":"5m0s"}`,
			wantWait: 5*time.Minute + 10*time.Second,
		},
		{
			name:     "a longer client timeout",
			timeout:  time.Hour,
			cmd:      UICommand{Steps: []UIStep{{"op": "wait"}}, Timeout: time.Second},
			wantBody: `{"steps":[{"op":"wait"}],"timeout":"1s"}`,
			wantWait: time.Hour,
		},
		{
			name:     "no client timeout",
			cmd:      UICommand{Steps: []UIStep{{"op": "wait"}}},
			wantBody: `{"steps":[{"op":"wait"}]}`,
		},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			d := &deadlineDoer{next: s.srv.Client()}
			s.client.HTTP, s.client.Timeout = d, tt.timeout
			s.answer(http.StatusOK, `{"client_id":"w1","results":[{"op":"wait","ok":true}]}`)

			reply, err := s.client.RunUI(context.Background(), tt.cmd)
			require.NoError(s.T(), err)
			require.Equal(s.T(), &UIReply{ClientID: "w1", Results: []map[string]any{{"op": "wait", "ok": true}}}, reply)
			s.requireCall(http.MethodPost, "/ui/commands", "")
			require.JSONEq(s.T(), tt.wantBody, s.body)
			require.Equal(s.T(), tt.wantWait > 0, d.set)
			if d.set {
				require.InDelta(s.T(), tt.wantWait.Seconds(), d.left.Seconds(), 5)
			}
		})
	}
}

func (s *ClientSuite) TestErrors() {
	ctx := context.Background()
	tests := []struct {
		name    string
		setup   func()
		call    func() error
		wantErr string
		status  int
	}{
		{
			name:    "status with a body",
			setup:   func() { s.answer(http.StatusForbidden, "no files.read\n") },
			call:    func() error { _, err := s.client.File(ctx, "a.go", 0, 0); return err },
			wantErr: "GET /channel/files: Loop answered 403: no files.read",
			status:  http.StatusForbidden,
		},
		{
			name:    "status without a body",
			setup:   func() { s.answer(http.StatusServiceUnavailable, "") },
			call:    func() error { _, err := s.client.Hotspots(ctx, 5); return err },
			wantErr: "GET /channel/quality/hotspots: Loop answered 503",
			status:  http.StatusServiceUnavailable,
		},
		{
			name:    "bad answer",
			setup:   func() { s.answer(http.StatusOK, "{") },
			call:    func() error { _, err := s.client.Channel(ctx); return err },
			wantErr: "decoding GET /channel",
		},
		{
			name:    "run without an id",
			setup:   func() { s.answer(http.StatusAccepted, `{}`) },
			call:    func() error { _, err := s.client.StartRun(ctx, RunRequest{}); return err },
			wantErr: "Loop answered without a run_id",
		},
		{
			name:    "ui refused",
			setup:   func() { s.answer(http.StatusForbidden, "no ui.run") },
			call:    func() error { _, err := s.client.RunUI(ctx, UICommand{}); return err },
			wantErr: "POST /ui/commands: Loop answered 403: no ui.run",
			status:  http.StatusForbidden,
		},
		{
			name:    "badge refused",
			setup:   func() { s.answer(http.StatusBadRequest, "text is required") },
			call:    func() error { return s.client.SetBadge(ctx, "", "") },
			wantErr: "PUT /badge: Loop answered 400: text is required",
			status:  http.StatusBadRequest,
		},
		{
			name:    "card refused",
			setup:   func() { s.answer(http.StatusBadRequest, "no card") },
			call:    func() error { _, err := s.client.PostCard(ctx, "x", "", nil); return err },
			wantErr: "POST /cards: Loop answered 400: no card",
			status:  http.StatusBadRequest,
		},
		{
			name:    "run refused",
			setup:   func() { s.answer(http.StatusNotFound, "") },
			call:    func() error { _, err := s.client.StartRun(ctx, RunRequest{}); return err },
			wantErr: "POST /runs: Loop answered 404",
			status:  http.StatusNotFound,
		},
		{
			name:    "tree refused",
			setup:   func() { s.answer(http.StatusBadRequest, "") },
			call:    func() error { _, err := s.client.Tree(ctx, "["); return err },
			wantErr: "Loop answered 400",
			status:  http.StatusBadRequest,
		},
		{
			name:    "git refused",
			setup:   func() { s.answer(http.StatusConflict, "") },
			call:    func() error { _, err := s.client.GitRecent(ctx, 0); return err },
			wantErr: "Loop answered 409",
			status:  http.StatusConflict,
		},
		{
			name:    "data that isn't JSON",
			setup:   func() {},
			call:    func() error { return s.client.Event(ctx, "x", func() {}) },
			wantErr: "encoding POST /events",
		},
		{
			name:    "missing token",
			setup:   func() { s.client.TokenFile = filepath.Join(s.T().TempDir(), "none") },
			call:    func() error { return s.client.Notify(ctx, "t", "b") },
			wantErr: "reading the plugin token",
		},
		{
			name:    "bad base URL",
			setup:   func() { s.client.BaseURL = "http://\x7f" },
			call:    func() error { return s.client.Notify(ctx, "t", "b") },
			wantErr: "building POST /notify",
		},
		{
			name:    "Loop is down",
			setup:   func() { s.srv.Close() },
			call:    func() error { return s.client.Notify(ctx, "t", "b") },
			wantErr: "POST /notify: ",
		},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			tt.setup()
			err := tt.call()
			require.ErrorContains(s.T(), err, tt.wantErr)
			if tt.status != 0 {
				require.True(s.T(), HasStatus(err, http.StatusTeapot, tt.status))
			} else {
				require.False(s.T(), HasStatus(err, http.StatusOK, http.StatusBadRequest))
			}
		})
	}
}

func (s *ClientSuite) TestNoTimeout() {
	s.client.Timeout = 0
	s.answer(http.StatusNoContent, "")
	require.NoError(s.T(), s.client.Notify(context.Background(), "t", "b"))
}

func (s *ClientSuite) TestHasStatus() {
	require.False(s.T(), HasStatus(errors.New("x"), http.StatusNotFound))
	require.True(s.T(), HasStatus(&StatusError{Status: http.StatusNotFound}, http.StatusNotFound))
}
