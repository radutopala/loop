package api

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/apiauth"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/testutil"
)

// agentLookup is a fixed token → principal table.
type agentLookup map[string]apiauth.Principal

func (a agentLookup) Lookup(tok string) (apiauth.Principal, bool) {
	p, ok := a[tok]
	return p, ok
}

type AgentScopeSuite struct {
	suite.Suite
	srv       *Server
	store     *MockChannelLister
	scheduler *testutil.MockScheduler
	sys       *testutil.MockSystem
	workflows *MockWorkflowEngine
	handler   http.Handler
	seenBody  string
}

func TestAgentScopeSuite(t *testing.T) {
	suite.Run(t, new(AgentScopeSuite))
}

func (s *AgentScopeSuite) SetupTest() {
	s.store = new(MockChannelLister)
	s.scheduler = new(testutil.MockScheduler)
	s.sys = new(testutil.MockSystem)
	s.workflows = new(MockWorkflowEngine)

	agents := agentLookup{
		"agent":  {Kind: apiauth.KindAgent, ContainerID: "c1", ChannelID: "ch1", DirPath: "/wt"},
		"orphan": {Kind: apiauth.KindAgent, ContainerID: "c2", ChannelID: "gone"},
	}
	s.srv = NewServer(s.scheduler, nil, nil, s.store, nil, slog.New(slog.NewTextHandler(io.Discard, nil)),
		WithAuth(AuthDeps{OwnerToken: "owner", Agents: agents}))
	s.srv.sys = s.sys
	s.srv.SetWorkflowEngine(s.workflows)

	for id, dir := range map[string]string{"ch1": "/proj", "ch2": "/proj", "ch3": "/other"} {
		s.store.On("GetChannel", mock.Anything, id).Return(&db.Channel{ChannelID: id, DirPath: dir}, nil).Maybe()
	}
	s.store.On("GetChannel", mock.Anything, "ghost").Return(nil, nil).Maybe()
	s.store.On("GetChannel", mock.Anything, "gone").Return(nil, errors.New("db down")).Maybe()

	s.scheduler.On("GetTask", mock.Anything, int64(5)).Return(&db.ScheduledTask{ChannelID: "ch1"}, nil).Maybe()
	s.scheduler.On("GetTask", mock.Anything, int64(6)).Return(&db.ScheduledTask{ChannelID: "ch3"}, nil).Maybe()
	s.scheduler.On("GetTask", mock.Anything, int64(7)).Return(nil, errors.New("no task")).Maybe()

	s.workflows.On("GetRun", mock.Anything, "r1").Return(&db.WorkflowRun{ChannelID: "ch2"}, nil, nil).Maybe()
	s.workflows.On("GetRun", mock.Anything, "r3").Return(&db.WorkflowRun{ChannelID: "ch3"}, nil, nil).Maybe()
	s.workflows.On("GetRun", mock.Anything, mock.Anything).Return(nil, nil, errors.New("no run")).Maybe()

	s.sys.On("EvalSymlinks", "/proj/link").Return("/etc", nil).Maybe()
	s.sys.On("EvalSymlinks", "/proj").Return("/proj", nil).Maybe()
	s.sys.On("EvalSymlinks", mock.Anything).Return("", errors.New("not found")).Maybe()

	s.seenBody = ""
	s.handler = s.srv.auth.Wrap(s.srv.agentGuard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		s.seenBody = string(b)
		w.WriteHeader(http.StatusOK)
	})))
}

func (s *AgentScopeSuite) do(tok, method, target string, body io.Reader) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, body)
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	s.handler.ServeHTTP(rec, req)
	return rec
}

func (s *AgentScopeSuite) TestGuard() {
	tests := []struct {
		name    string
		tok     string
		method  string
		target  string
		body    string
		want    int
		wantMsg string
	}{
		{name: "owner passes through", tok: "owner", method: "GET", target: "/api/channels/ch3/queued", want: 200},
		{name: "owner-only route", tok: "agent", method: "PUT", target: "/api/config", want: 403},
		{name: "rotate the owner token", tok: "agent", method: "POST", target: "/api/auth/rotate", want: 403},
		{name: "trust a project config", tok: "agent", method: "POST", target: "/api/config/project/trust?channel_id=ch1", want: 403},
		{name: "preview a learn proposal", tok: "agent", method: "GET", target: "/api/learn/proposals/1/preview", want: 403},

		{name: "own channel", tok: "agent", method: "GET", target: "/api/channels/ch1/queued", want: 200},
		{name: "same project channel", tok: "agent", method: "GET", target: "/api/channels/ch2/queued", want: 200},
		{name: "other project channel", tok: "agent", method: "GET", target: "/api/channels/ch3/queued", want: 403, wantMsg: "channel ch3 is outside"},
		{name: "unknown channel", tok: "agent", method: "GET", target: "/api/channels/ghost/queued", want: 403},
		{name: "thread path", tok: "agent", method: "DELETE", target: "/api/threads/ch3", want: 403},
		{name: "own channel lookup fails", tok: "orphan", method: "GET", target: "/api/channels/ch1/queued", want: 403},
		{name: "id that names no channel", tok: "agent", method: "PATCH", target: "/api/agents/a1", body: "{}", want: 200},

		{name: "own task", tok: "agent", method: "GET", target: "/api/tasks/5", want: 200},
		{name: "other project task", tok: "agent", method: "DELETE", target: "/api/tasks/6", want: 403},
		{name: "task lookup fails", tok: "agent", method: "GET", target: "/api/tasks/7", want: 403, wantMsg: "unknown task"},
		{name: "task id not a number", tok: "agent", method: "GET", target: "/api/tasks/abc", want: 403, wantMsg: "unknown task"},

		{name: "own run", tok: "agent", method: "GET", target: "/api/workflows/runs/r1", want: 200},
		{name: "other project run", tok: "agent", method: "POST", target: "/api/workflows/runs/r3/cancel", want: 403},
		{name: "unknown run", tok: "agent", method: "GET", target: "/api/workflows/runs/nope", want: 403, wantMsg: "unknown workflow run"},

		{name: "query channel", tok: "agent", method: "GET", target: "/api/workflows/runs?channel_id=ch3", want: 403},
		{name: "query dir in project", tok: "agent", method: "GET", target: "/api/channels?dir_path=/proj/sub", want: 200},
		{name: "query dir in agent dir", tok: "agent", method: "GET", target: "/api/channels?dir_path=/wt/a", want: 200},
		{name: "query dir elsewhere", tok: "agent", method: "GET", target: "/api/channels?dir_path=/home/u", want: 403, wantMsg: "dir /home/u is outside"},
		{name: "query dir sibling prefix", tok: "agent", method: "GET", target: "/api/channels?dir_path=/project", want: 403},
		{name: "query dir relative", tok: "agent", method: "GET", target: "/api/channels?dir_path=proj", want: 403},
		{name: "query dir symlink out", tok: "agent", method: "GET", target: "/api/channels?dir_path=/proj/link", want: 403},
		{name: "dir with no agent dir or project", tok: "orphan", method: "GET", target: "/api/channels?dir_path=/proj", want: 403},

		{name: "body own channel", tok: "agent", method: "POST", target: "/api/messages", body: `{"channel_id":"ch1","text":"hi"}`, want: 200},
		{name: "post to another project's channel", tok: "agent", method: "POST", target: "/api/messages", body: `{"channel_id":"ch3"}`, want: 200},
		{name: "body other channel elsewhere", tok: "agent", method: "POST", target: "/api/threads", body: `{"channel_id":"ch3"}`, want: 403},
		{name: "body thread", tok: "agent", method: "POST", target: "/api/threads", body: `{"channel_id":"ch1","thread_id":"ch3"}`, want: 403},
		{name: "body learn channel", tok: "agent", method: "POST", target: "/api/channels/ch1/learn/proposals", body: `{"learn_channel_id":"ch3"}`, want: 403},
		{name: "body dir", tok: "agent", method: "POST", target: "/api/memory/search", body: `{"dir_path":"/etc"}`, want: 403},
		{name: "body partly mistyped", tok: "agent", method: "POST", target: "/api/worktrees", body: `{"channel_id":5,"dir_path":"/etc"}`, want: 403},
		{name: "body not json", tok: "agent", method: "POST", target: "/api/messages", body: `nope`, want: 200},
		{name: "playground file body is content", tok: "agent", method: "PUT", target: "/api/playground/file?channel_id=ch1", body: `{"channel_id":"ch3"}`, want: 200},

		{name: "project bash shortcut", tok: "agent", method: "POST", target: "/api/bash-shortcuts", body: `{"scope":"project","channel_id":"ch1"}`, want: 200},
		{name: "global bash shortcut", tok: "agent", method: "POST", target: "/api/bash-shortcuts", body: `{"scope":"global"}`, want: 403, wantMsg: "project bash shortcuts"},
		{name: "workflow run", tok: "agent", method: "POST", target: "/api/workflows/runs", body: `{"channel_id":"ch1"}`, want: 200},

		// Changing review comments, on the PR too, is held to the agent's own
		// channel: a channel in the same project isn't enough.
		{name: "delete own review comment", tok: "agent", method: "DELETE", target: "/api/channels/ch1/review/comments/c1", want: 200},
		{name: "edit own review comment", tok: "agent", method: "PATCH", target: "/api/channels/ch1/review/comments/c1", body: `{"body":"x"}`, want: 200},
		{name: "push own review comment", tok: "agent", method: "POST", target: "/api/channels/ch1/review/comments/c1/push", want: 200},
		{name: "push all own review comments", tok: "agent", method: "POST", target: "/api/channels/ch1/review/push-all", want: 200},
		{name: "delete review comment in project channel", tok: "agent", method: "DELETE", target: "/api/channels/ch2/review/comments/c1", want: 403, wantMsg: "own channel"},
		{name: "edit review comment in project channel", tok: "agent", method: "PATCH", target: "/api/channels/ch2/review/comments/c1", body: `{"body":"x"}`, want: 403, wantMsg: "own channel"},
		{name: "push review comment in project channel", tok: "agent", method: "POST", target: "/api/channels/ch2/review/comments/c1/push", want: 403, wantMsg: "own channel"},
		{name: "push all in project channel", tok: "agent", method: "POST", target: "/api/channels/ch2/review/push-all", want: 403, wantMsg: "own channel"},
		{name: "push all in other project channel", tok: "agent", method: "POST", target: "/api/channels/ch3/review/push-all", want: 403, wantMsg: "own channel"},
		{name: "owner pushes any channel's comments", tok: "owner", method: "POST", target: "/api/channels/ch3/review/push-all", want: 200},
		// Reading and deduping keep the project-wide scope.
		{name: "read review in project channel", tok: "agent", method: "GET", target: "/api/channels/ch2/review", want: 200},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			rec := s.do(tc.tok, tc.method, tc.target, strings.NewReader(tc.body))
			require.Equal(s.T(), tc.want, rec.Code, rec.Body.String())
			require.Contains(s.T(), rec.Body.String(), tc.wantMsg)
			if tc.want == http.StatusOK {
				require.Equal(s.T(), tc.body, s.seenBody, "the handler reads the whole body")
			}
		})
	}
}

func (s *AgentScopeSuite) TestBody() {
	s.Run("too large", func() {
		rec := s.do("agent", "POST", "/api/messages", strings.NewReader(strings.Repeat(" ", maxGuardedBody+1)))
		require.Equal(s.T(), http.StatusForbidden, rec.Code)
		require.Contains(s.T(), rec.Body.String(), "request body too large")
	})
	s.Run("read error", func() {
		rec := s.do("agent", "POST", "/api/messages", &errReader{})
		require.Equal(s.T(), http.StatusForbidden, rec.Code)
	})
}

func (s *AgentScopeSuite) TestWorkflowBashLocal() {
	WithWorkflowBashLocal(true)(s.srv)
	tests := []struct {
		name   string
		method string
		target string
		body   string
		want   int
	}{
		{"start run", "POST", "/api/workflows/runs", `{"channel_id":"ch1"}`, 403},
		{"retry run", "POST", "/api/workflows/runs/r1/retry", `{}`, 403},
		{"resume run", "POST", "/api/workflows/runs/r1/resume", `{}`, 403},
		{"workflow task", "POST", "/api/tasks", `{"channel_id":"ch1","workflow_name":"w"}`, 403},
		{"task switched to a workflow", "PATCH", "/api/tasks/5", `{"workflow_name":"w"}`, 403},
		{"prompt task", "POST", "/api/tasks", `{"channel_id":"ch1","prompt":"p"}`, 200},
		{"cancel run", "POST", "/api/workflows/runs/r1/cancel", `{}`, 200},
		{"list workflows", "GET", "/api/workflows", "", 200},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			rec := s.do("agent", tc.method, tc.target, strings.NewReader(tc.body))
			require.Equal(s.T(), tc.want, rec.Code, rec.Body.String())
		})
	}
}

func (s *AgentScopeSuite) TestLookupsNotConfigured() {
	s.srv.scheduler = nil
	s.srv.workflowEngine = nil
	require.Equal(s.T(), http.StatusForbidden, s.do("agent", "GET", "/api/tasks/5", nil).Code)
	require.Equal(s.T(), http.StatusForbidden, s.do("agent", "GET", "/api/workflows/runs/r1", nil).Code)
}
