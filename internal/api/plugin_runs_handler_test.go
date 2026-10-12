package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/apiauth"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/orchestrator"
	"github.com/radutopala/loop/internal/plugin"
	"github.com/radutopala/loop/internal/plugin/install"
	"github.com/radutopala/loop/internal/plugin/proxy"
	"github.com/radutopala/loop/internal/plugin/runtime"
)

type MockPluginRunner struct {
	mock.Mock
}

func (m *MockPluginRunner) StartPluginRun(ctx context.Context, parent *db.Channel, run orchestrator.PluginRun) (string, string, error) {
	args := m.Called(ctx, parent, run)
	return args.String(0), args.String(1), args.Error(2)
}

func (m *MockPluginRunner) ActivePluginRun(threadID string) (orchestrator.ActivePluginRun, bool) {
	args := m.Called(threadID)
	return args.Get(0).(orchestrator.ActivePluginRun), args.Bool(1)
}

func (m *MockPluginRunner) PluginRunState(pluginID, channelID, runID string) (orchestrator.PluginRunState, bool) {
	args := m.Called(pluginID, channelID, runID)
	return args.Get(0).(orchestrator.PluginRunState), args.Bool(1)
}

func (m *MockPluginRunner) CancelPluginRun(ctx context.Context, pluginID, channelID, runID string) (bool, error) {
	args := m.Called(ctx, pluginID, channelID, runID)
	return args.Bool(0), args.Error(1)
}

// pluginRunRoutes registers the plugin agent run routes with a plugin
// manager and runner.
func (s *ServerSuite) pluginRunRoutes() (*MockPluginManager, *MockPluginRunner) {
	pm := s.pluginRoutes()
	pr := new(MockPluginRunner)
	s.srv.SetPluginRunner(pr)
	s.mux.HandleFunc("POST /api/plugin/v1/runs", s.srv.handleStartPluginRun)
	s.mux.HandleFunc("GET /api/plugin/v1/runs/{run_id}", s.srv.handleGetPluginRun)
	s.mux.HandleFunc("DELETE /api/plugin/v1/runs/{run_id}", s.srv.handleCancelPluginRun)
	s.mux.HandleFunc("POST /api/plugins/{plugin}/tools/{tool}", s.srv.handleCallPluginTool)
	s.mux.HandleFunc("GET /api/plugins/{plugin}/agent", s.srv.handleGetPluginAgent)
	s.T().Cleanup(func() { pr.AssertExpectations(s.T()) })
	return pm, pr
}

// requestAs makes a request as principal p.
func (s *ServerSuite) requestAs(p apiauth.Principal, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	req = req.WithContext(apiauth.WithPrincipal(req.Context(), p))
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	return rec
}

func (s *ServerSuite) TestPluginRunsDisabled() {
	s.mux.HandleFunc("POST /api/plugin/v1/runs", s.srv.handleStartPluginRun)
	s.mux.HandleFunc("POST /api/plugins/{plugin}/tools/{tool}", s.srv.handleCallPluginTool)
	for _, path := range []string{"/api/plugin/v1/runs", "/api/plugins/echo/tools/echo"} {
		rec := s.testRequest("POST", path, "{}")
		require.Equal(s.T(), http.StatusNotImplemented, rec.Code, path)
	}
	s.srv.SetPluginManager(new(MockPluginManager))
	for _, path := range []string{"/api/plugin/v1/runs", "/api/plugins/echo/tools/echo"} {
		rec := s.testRequest("POST", path, "{}")
		require.Equal(s.T(), http.StatusNotImplemented, rec.Code, path)
	}
}

func (s *ServerSuite) TestStartPluginRun() {
	echo := apiauth.Principal{Kind: apiauth.KindPlugin, PluginID: "echo", ChannelID: "ch-1"}
	ch := &db.Channel{ChannelID: "ch-1", DirPath: "/src/app"}
	tools := []plugin.Tool{{Name: "echo", InputSchema: json.RawMessage(`{"type":"object"}`)}}
	sayRun := &install.AgentRun{Agent: plugin.Agent{ID: "say", ReadOnly: true, Tools: []string{"echo"}}, Tools: tools}
	body := `{"agent":"say","prompt":"say hi","model":"sonnet","effort":"low"}`
	tests := []struct {
		name     string
		as       apiauth.Principal
		body     string
		setup    func(pm *MockPluginManager, pr *MockPluginRunner)
		wantCode int
		wantBody string
	}{
		{name: "queued", as: echo, body: body, setup: func(pm *MockPluginManager, pr *MockPluginRunner) {
			pm.On("AgentRun", mock.Anything, "echo", "say").Return(sayRun, nil).Once()
			s.store.On("GetChannel", mock.Anything, "ch-1").Return(ch, nil).Once()
			pr.On("StartPluginRun", mock.Anything, ch, orchestrator.PluginRun{
				PluginID: "echo", Agent: "say", ReadOnly: true, Prompt: "say hi", Model: "sonnet", Effort: "low", Tools: tools,
			}).Return("ask-1", "th-1", nil).Once()
		}, wantCode: http.StatusAccepted, wantBody: `{"run_id":"ask-1","thread_id":"th-1"}`},
		{name: "not a plugin", as: apiauth.Principal{Kind: apiauth.KindOwner}, body: body, wantCode: http.StatusForbidden, wantBody: "only a plugin"},
		{name: "bad body", as: echo, body: `{`, wantCode: http.StatusBadRequest},
		{name: "no prompt", as: echo, body: `{"agent":"say"}`, wantCode: http.StatusBadRequest, wantBody: "agent and prompt are required"},
		{name: "unknown agent", as: echo, body: body, setup: func(pm *MockPluginManager, _ *MockPluginRunner) {
			pm.On("AgentRun", mock.Anything, "echo", "say").Return(nil, install.ErrUnknownAgent).Once()
		}, wantCode: http.StatusNotFound},
		{name: "not approved", as: echo, body: body, setup: func(pm *MockPluginManager, _ *MockPluginRunner) {
			pm.On("AgentRun", mock.Anything, "echo", "say").Return(nil, install.ErrNotApproved).Once()
		}, wantCode: http.StatusForbidden},
		{name: "disabled", as: echo, body: body, setup: func(pm *MockPluginManager, _ *MockPluginRunner) {
			pm.On("AgentRun", mock.Anything, "echo", "say").Return(nil, install.ErrNotRunnable).Once()
		}, wantCode: http.StatusConflict},
		{name: "lookup fails", as: echo, body: body, setup: func(pm *MockPluginManager, _ *MockPluginRunner) {
			pm.On("AgentRun", mock.Anything, "echo", "say").Return(nil, errors.New("boom")).Once()
		}, wantCode: http.StatusInternalServerError, wantBody: "boom"},
		{name: "channel fails", as: echo, body: body, setup: func(pm *MockPluginManager, _ *MockPluginRunner) {
			pm.On("AgentRun", mock.Anything, "echo", "say").Return(sayRun, nil).Once()
			s.store.On("GetChannel", mock.Anything, "ch-1").Return(nil, errors.New("db down")).Once()
		}, wantCode: http.StatusInternalServerError, wantBody: "db down"},
		{name: "channel gone", as: echo, body: body, setup: func(pm *MockPluginManager, _ *MockPluginRunner) {
			pm.On("AgentRun", mock.Anything, "echo", "say").Return(sayRun, nil).Once()
			s.store.On("GetChannel", mock.Anything, "ch-1").Return(nil, nil).Once()
		}, wantCode: http.StatusNotFound, wantBody: "channel not found"},
		{name: "channel deleted meanwhile", as: echo, body: body, setup: func(pm *MockPluginManager, pr *MockPluginRunner) {
			pm.On("AgentRun", mock.Anything, "echo", "say").Return(sayRun, nil).Once()
			s.store.On("GetChannel", mock.Anything, "ch-1").Return(ch, nil).Once()
			pr.On("StartPluginRun", mock.Anything, ch, mock.Anything).Return("", "", db.ErrParentGone).Once()
		}, wantCode: http.StatusNotFound, wantBody: "channel not found"},
		{name: "daily runs started", as: echo, body: body, setup: func(pm *MockPluginManager, pr *MockPluginRunner) {
			pm.On("AgentRun", mock.Anything, "echo", "say").Return(sayRun, nil).Once()
			s.store.On("GetChannel", mock.Anything, "ch-1").Return(ch, nil).Once()
			pr.On("StartPluginRun", mock.Anything, ch, mock.Anything).Return("", "", orchestrator.ErrPluginRunBudget).Once()
		}, wantCode: http.StatusTooManyRequests, wantBody: "plugins.daily_runs"},
		{name: "queueing fails", as: echo, body: body, setup: func(pm *MockPluginManager, pr *MockPluginRunner) {
			pm.On("AgentRun", mock.Anything, "echo", "say").Return(sayRun, nil).Once()
			s.store.On("GetChannel", mock.Anything, "ch-1").Return(ch, nil).Once()
			pr.On("StartPluginRun", mock.Anything, ch, mock.Anything).Return("", "", errors.New("db down")).Once()
		}, wantCode: http.StatusInternalServerError, wantBody: "db down"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			pm, pr := s.pluginRunRoutes()
			if tt.setup != nil {
				tt.setup(pm, pr)
			}
			rec := s.requestAs(tt.as, "POST", "/api/plugin/v1/runs", tt.body)
			require.Equal(s.T(), tt.wantCode, rec.Code, rec.Body.String())
			require.Contains(s.T(), rec.Body.String(), tt.wantBody)
		})
	}
}

func (s *ServerSuite) TestCallPluginTool() {
	agentInThread := apiauth.Principal{Kind: apiauth.KindAgent, ChannelID: "th-1"}
	active := orchestrator.ActivePluginRun{ID: "ask-1", Run: orchestrator.PluginRun{PluginID: "echo", Agent: "say"}}
	thread := &db.Channel{ChannelID: "th-1", ParentID: "ch-1", Kind: db.PluginKind("echo")}
	parent := &db.Channel{ChannelID: "ch-1", DirPath: "/src/app"}
	rch := runtime.Channel{ID: "ch-1", DirPath: "/src/app"}
	call := proxy.ToolRequest{RunID: "ask-1", Agent: "say", ThreadID: "th-1", Input: json.RawMessage(`{"text":"hi"}`)}
	body := `{"input":{"text":"hi"}}`
	channels := func() {
		s.store.On("GetChannel", mock.Anything, "th-1").Return(thread, nil).Once()
		s.store.On("GetChannel", mock.Anything, "ch-1").Return(parent, nil).Once()
	}
	tests := []struct {
		name     string
		as       apiauth.Principal
		path     string
		body     string
		active   *orchestrator.ActivePluginRun
		setup    func(pm *MockPluginManager)
		wantCode int
		wantBody string
	}{
		{name: "answered", as: agentInThread, active: &active, body: body, setup: func(pm *MockPluginManager) {
			channels()
			pm.On("CallTool", mock.Anything, "echo", "echo", rch, call).Return(&proxy.ToolResult{Content: "hi"}, nil).Once()
		}, wantCode: http.StatusOK, wantBody: `{"content":"hi","is_error":false}`},
		{name: "no run going on", as: agentInThread, body: body, wantCode: http.StatusForbidden, wantBody: "only the plugin's own agent runs"},
		{name: "another plugin's run", as: agentInThread, path: "/api/plugins/other/tools/echo", active: &active, body: body, wantCode: http.StatusForbidden},
		{name: "not an agent", as: apiauth.Principal{Kind: apiauth.KindPlugin, PluginID: "echo", ChannelID: "th-1"}, active: &active, body: body, wantCode: http.StatusForbidden},
		{name: "bad body", as: agentInThread, active: &active, body: `{`, wantCode: http.StatusBadRequest},
		{name: "thread fails", as: agentInThread, active: &active, body: body, setup: func(*MockPluginManager) {
			s.store.On("GetChannel", mock.Anything, "th-1").Return(nil, errors.New("db down")).Once()
		}, wantCode: http.StatusInternalServerError, wantBody: "db down"},
		{name: "thread gone", as: agentInThread, active: &active, body: body, setup: func(*MockPluginManager) {
			s.store.On("GetChannel", mock.Anything, "th-1").Return(nil, nil).Once()
		}, wantCode: http.StatusNotFound, wantBody: "channel not found"},
		{name: "bad input", as: agentInThread, active: &active, body: body, setup: func(pm *MockPluginManager) {
			channels()
			pm.On("CallTool", mock.Anything, "echo", "echo", rch, call).Return(nil, install.ErrInvalidInput).Once()
		}, wantCode: http.StatusOK, wantBody: `{"content":"invalid tool input","is_error":true}`},
		{name: "bad result", as: agentInThread, active: &active, body: body, setup: func(pm *MockPluginManager) {
			channels()
			pm.On("CallTool", mock.Anything, "echo", "echo", rch, call).Return(nil, install.ErrInvalidOutput).Once()
		}, wantCode: http.StatusOK, wantBody: `{"content":"invalid tool result","is_error":true}`},
		{name: "unknown tool", as: agentInThread, active: &active, body: body, setup: func(pm *MockPluginManager) {
			channels()
			pm.On("CallTool", mock.Anything, "echo", "echo", rch, call).Return(nil, install.ErrUnknownTool).Once()
		}, wantCode: http.StatusOK, wantBody: `"is_error":true`},
		{name: "plugin not answering", as: agentInThread, active: &active, body: body, setup: func(pm *MockPluginManager) {
			channels()
			pm.On("CallTool", mock.Anything, "echo", "echo", rch, call).Return(nil, proxy.ErrUnavailable).Once()
		}, wantCode: http.StatusOK, wantBody: `{"content":"the plugin isn't answering","is_error":true}`},
		{name: "not enabled", as: agentInThread, active: &active, body: body, setup: func(pm *MockPluginManager) {
			channels()
			pm.On("CallTool", mock.Anything, "echo", "echo", rch, call).Return(nil, install.ErrNotEnabled).Once()
		}, wantCode: http.StatusConflict, wantBody: "not enabled"},
		{name: "plugin fails", as: agentInThread, active: &active, body: body, setup: func(pm *MockPluginManager) {
			channels()
			pm.On("CallTool", mock.Anything, "echo", "echo", rch, call).Return(nil, errors.New("starting the plugin: unhealthy")).Once()
		}, wantCode: http.StatusBadGateway, wantBody: "unhealthy"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			pm, pr := s.pluginRunRoutes()
			if tt.active != nil {
				pr.On("ActivePluginRun", tt.as.ChannelID).Return(*tt.active, true).Once()
			} else {
				pr.On("ActivePluginRun", tt.as.ChannelID).Return(orchestrator.ActivePluginRun{}, false).Once()
			}
			if tt.setup != nil {
				tt.setup(pm)
			}
			path := tt.path
			if path == "" {
				path = "/api/plugins/echo/tools/echo"
			}
			rec := s.requestAs(tt.as, "POST", path, tt.body)
			require.Equal(s.T(), tt.wantCode, rec.Code, rec.Body.String())
			require.Contains(s.T(), rec.Body.String(), tt.wantBody)
		})
	}
}

func (s *ServerSuite) TestStartPluginRunBroadcasts() {
	pm, pr := s.pluginRunRoutes()
	hub := NewEventsHub(testLogger())
	var sent []Event
	hub.captureHook = func(e Event) {
		e.Timestamp = 0
		sent = append(sent, e)
	}
	s.srv.SetEventsHub(hub)
	ch := &db.Channel{ChannelID: "ch-1"}
	pm.On("AgentRun", mock.Anything, "echo", "say").Return(&install.AgentRun{Agent: plugin.Agent{ID: "say"}}, nil).Once()
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(ch, nil).Once()
	pr.On("StartPluginRun", mock.Anything, ch, mock.Anything).Return("ask-1", "th-1", nil).Once()

	rec := s.requestAs(apiauth.Principal{Kind: apiauth.KindPlugin, PluginID: "echo", ChannelID: "ch-1"}, "POST", "/api/plugin/v1/runs", `{"agent":"say","prompt":"hi"}`)
	require.Equal(s.T(), http.StatusAccepted, rec.Code, rec.Body.String())
	require.Equal(s.T(), []Event{{Type: EventPluginRunStarted, ChannelID: "ch-1", Data: map[string]string{
		"plugin_id": "echo", "run_id": "ask-1", "thread_id": "th-1",
	}}}, sent)
}

func (s *ServerSuite) TestGetPluginAgent() {
	thread := &db.Channel{ChannelID: "plugin:echo-abc123", ParentID: "ch-1", Kind: db.PluginKind("echo")}
	tests := []struct {
		name     string
		query    string
		noRunner bool
		setup    func(pr *MockPluginRunner)
		wantCode int
		wantBody string
	}{
		{name: "running", query: "?channel_id=th-1", setup: func(pr *MockPluginRunner) {
			s.store.On("GetChannel", mock.Anything, "th-1").Return(&db.Channel{ChannelID: "th-1", ParentID: "ch-1"}, nil).Once()
			s.store.On("GetHiddenThread", mock.Anything, "th-1", db.PluginKind("echo")).Return(thread, nil).Once()
			pr.On("ActivePluginRun", "plugin:echo-abc123").Return(orchestrator.ActivePluginRun{ID: "ask-1"}, true).Once()
		}, wantCode: http.StatusOK, wantBody: `{"thread_id":"plugin:echo-abc123","running":true}`},
		{name: "idle", query: "?channel_id=ch-1", setup: func(pr *MockPluginRunner) {
			s.store.On("GetHiddenThread", mock.Anything, "ch-1", db.PluginKind("echo")).Return(thread, nil).Once()
			pr.On("ActivePluginRun", "plugin:echo-abc123").Return(orchestrator.ActivePluginRun{}, false).Once()
		}, wantCode: http.StatusOK, wantBody: `{"thread_id":"plugin:echo-abc123","running":false}`},
		{name: "no runner", query: "?channel_id=ch-1", noRunner: true, setup: func(*MockPluginRunner) {
			s.store.On("GetHiddenThread", mock.Anything, "ch-1", db.PluginKind("echo")).Return(thread, nil).Once()
		}, wantCode: http.StatusOK, wantBody: `{"thread_id":"plugin:echo-abc123","running":false}`},
		{name: "no runs yet", query: "?channel_id=ch-1", setup: func(*MockPluginRunner) {
			s.store.On("GetHiddenThread", mock.Anything, "ch-1", db.PluginKind("echo")).Return(nil, nil).Once()
		}, wantCode: http.StatusOK, wantBody: `{"running":false}`},
		{name: "no channel id", wantCode: http.StatusBadRequest, wantBody: "channel_id is required"},
		{name: "unknown channel", query: "?channel_id=ghost", setup: func(*MockPluginRunner) {
			s.store.On("GetChannel", mock.Anything, "ghost").Return(nil, nil).Once()
		}, wantCode: http.StatusNotFound, wantBody: "channel not found"},
		{name: "lookup fails", query: "?channel_id=ch-1", setup: func(*MockPluginRunner) {
			s.store.On("GetHiddenThread", mock.Anything, "ch-1", db.PluginKind("echo")).Return(nil, errors.New("db down")).Once()
		}, wantCode: http.StatusInternalServerError, wantBody: "db down"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			_, pr := s.pluginRunRoutes()
			if tt.noRunner {
				s.srv.SetPluginRunner(nil)
			}
			s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1"}, nil).Maybe()
			if tt.setup != nil {
				tt.setup(pr)
			}
			rec := s.testRequest("GET", "/api/plugins/echo/agent"+tt.query, "")
			require.Equal(s.T(), tt.wantCode, rec.Code, rec.Body.String())
			require.Contains(s.T(), rec.Body.String(), tt.wantBody)
		})
	}
}

// TestPluginRunStateAndCancel covers a plugin reading where its agent runs
// stand and cancelling them.
func (s *ServerSuite) TestPluginRunStateAndCancel() {
	echo := apiauth.Principal{Kind: apiauth.KindPlugin, PluginID: "echo", ChannelID: "ch-1"}
	allow := func(readOnly, write error) func(pm *MockPluginManager) {
		return func(pm *MockPluginManager) {
			pm.On("Allows", mock.Anything, "echo", plugin.CapAgentReadOnly).Return(readOnly).Once()
			if errors.Is(readOnly, install.ErrNotApproved) {
				pm.On("Allows", mock.Anything, "echo", plugin.CapAgentWrite).Return(write).Once()
			}
		}
	}
	tests := []struct {
		name     string
		method   string
		as       apiauth.Principal
		allow    func(pm *MockPluginManager)
		setup    func(pr *MockPluginRunner)
		wantCode int
		wantBody string
	}{
		{name: "not a plugin", method: "GET", as: apiauth.Principal{Kind: apiauth.KindOwner}, wantCode: http.StatusForbidden, wantBody: "only a plugin"},
		{name: "no agent capability", method: "GET", as: echo, allow: allow(install.ErrNotApproved, install.ErrNotApproved), wantCode: http.StatusForbidden},
		{name: "disabled", method: "DELETE", as: echo, allow: allow(install.ErrNotRunnable, nil), wantCode: http.StatusConflict},
		{name: "queued", method: "GET", as: echo, allow: allow(nil, nil), setup: func(pr *MockPluginRunner) {
			pr.On("PluginRunState", "echo", "ch-1", "ask-1").Return(orchestrator.PluginRunState{ThreadID: "th-1", Status: orchestrator.PluginRunQueued}, true).Once()
		}, wantCode: http.StatusOK, wantBody: `{"run_id":"ask-1","thread_id":"th-1","status":"queued"}`},
		{name: "running, with agent.write", method: "GET", as: echo, allow: allow(install.ErrNotApproved, nil), setup: func(pr *MockPluginRunner) {
			pr.On("PluginRunState", "echo", "ch-1", "ask-1").Return(orchestrator.PluginRunState{ThreadID: "th-1", Status: orchestrator.PluginRunRunning}, true).Once()
		}, wantCode: http.StatusOK, wantBody: `"status":"running"`},
		{name: "ended", method: "GET", as: echo, allow: allow(nil, nil), setup: func(pr *MockPluginRunner) {
			pr.On("PluginRunState", "echo", "ch-1", "ask-1").Return(orchestrator.PluginRunState{}, false).Once()
		}, wantCode: http.StatusNotFound, wantBody: "no queued or running run"},
		{name: "cancelled", method: "DELETE", as: echo, allow: allow(nil, nil), setup: func(pr *MockPluginRunner) {
			pr.On("CancelPluginRun", mock.Anything, "echo", "ch-1", "ask-1").Return(true, nil).Once()
		}, wantCode: http.StatusNoContent},
		{name: "cancel an ended run", method: "DELETE", as: echo, allow: allow(nil, nil), setup: func(pr *MockPluginRunner) {
			pr.On("CancelPluginRun", mock.Anything, "echo", "ch-1", "ask-1").Return(false, nil).Once()
		}, wantCode: http.StatusNotFound, wantBody: "no queued or running run"},
		{name: "cancel a starting run", method: "DELETE", as: echo, allow: allow(nil, nil), setup: func(pr *MockPluginRunner) {
			pr.On("CancelPluginRun", mock.Anything, "echo", "ch-1", "ask-1").Return(false, orchestrator.ErrPluginRunStarting).Once()
		}, wantCode: http.StatusConflict, wantBody: "try again"},
		{name: "cancel fails", method: "DELETE", as: echo, allow: allow(nil, nil), setup: func(pr *MockPluginRunner) {
			pr.On("CancelPluginRun", mock.Anything, "echo", "ch-1", "ask-1").Return(false, errors.New("db down")).Once()
		}, wantCode: http.StatusInternalServerError, wantBody: "db down"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			pm, pr := s.pluginRunRoutes()
			if tt.allow != nil {
				tt.allow(pm)
			}
			if tt.setup != nil {
				tt.setup(pr)
			}
			rec := s.requestAs(tt.as, tt.method, "/api/plugin/v1/runs/ask-1", "")
			require.Equal(s.T(), tt.wantCode, rec.Code, rec.Body.String())
			require.Contains(s.T(), rec.Body.String(), tt.wantBody)
			pm.AssertExpectations(s.T())
		})
	}

	s.Run("plugins off", func() {
		s.SetupTest()
		s.mux.HandleFunc("GET /api/plugin/v1/runs/{run_id}", s.srv.handleGetPluginRun)
		rec := s.requestAs(echo, "GET", "/api/plugin/v1/runs/ask-1", "")
		require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
	})
}
