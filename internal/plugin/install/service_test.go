package install

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/plugin"
	"github.com/radutopala/loop/internal/plugin/proxy"
	"github.com/radutopala/loop/internal/plugin/runtime"
	"github.com/radutopala/loop/internal/types"
)

type mockRuntime struct {
	mock.Mock
}

func (m *mockRuntime) StopPlugin(ctx context.Context, pluginID string) {
	m.Called(ctx, pluginID)
}

func (m *mockRuntime) Stop(ctx context.Context, pluginID, channelID string) {
	m.Called(ctx, pluginID, channelID)
}

func (m *mockRuntime) Ensure(ctx context.Context, p runtime.Plugin, ch runtime.Channel) (runtime.Endpoint, error) {
	args := m.Called(ctx, p, ch)
	return args.Get(0).(runtime.Endpoint), args.Error(1)
}

type mockCaller struct {
	mock.Mock
}

func (m *mockCaller) Action(ctx context.Context, ep runtime.Endpoint, action string, req proxy.ActionRequest) error {
	return m.Called(ctx, ep, action, req).Error(0)
}

func (m *mockCaller) Tool(ctx context.Context, ep runtime.Endpoint, tool string, req proxy.ToolRequest) (*proxy.ToolResult, error) {
	args := m.Called(ctx, ep, tool, req)
	res, _ := args.Get(0).(*proxy.ToolResult)
	return res, args.Error(1)
}

func (m *mockCaller) RunDone(ctx context.Context, ep runtime.Endpoint, runID string, req proxy.RunDoneRequest) error {
	return m.Called(ctx, ep, runID, req).Error(0)
}

func (m *mockCaller) X(ctx context.Context, ep runtime.Endpoint, req proxy.XRequest) (*proxy.XResponse, error) {
	args := m.Called(ctx, ep, req)
	res, _ := args.Get(0).(*proxy.XResponse)
	return res, args.Error(1)
}

func (m *mockRuntime) StopIdle(ctx context.Context) bool {
	return m.Called(ctx).Bool(0)
}

func (m *mockRuntime) Running() []runtime.Running {
	return m.Called().Get(0).([]runtime.Running)
}

func (m *mockRuntime) Remove(ctx context.Context, pluginID, channelID string, purge bool) error {
	return m.Called(ctx, pluginID, channelID, purge).Error(0)
}

type ServiceSuite struct {
	base
	runtime *mockRuntime
	caller  *mockCaller
	svc     *Service
	// enabled and enabledErr are what the Service's Enabled answers.
	enabled    bool
	enabledErr error
}

func TestServiceSuite(t *testing.T) {
	suite.Run(t, new(ServiceSuite))
}

func (s *ServiceSuite) SetupTest() {
	s.base.SetupTest()
	s.runtime = new(mockRuntime)
	s.caller = new(mockCaller)
	s.enabled, s.enabledErr = true, nil
	s.svc = NewService(s.in, s.runtime, s.store, s.caller, func(_ context.Context, pluginID string, ch runtime.Channel) (bool, error) {
		s.Require().Equal("echo", pluginID)
		return s.enabled, s.enabledErr
	})
}

func (s *ServiceSuite) TearDownTest() {
	s.base.TearDownTest()
	s.runtime.AssertExpectations(s.T())
	s.caller.AssertExpectations(s.T())
}

// expectInspect expects a full, successful inspection of ref, finding the
// plugin installed as prev.
func (s *ServiceSuite) expectInspect(prev *db.PluginState) {
	s.expectPull()
	s.docker.On("ImageInspect", s.ctx, ref).Return(s.info(), nil).Once()
	s.expectCreated()
	s.expectData()
	s.docker.On("CopyFromContainer", s.ctx, "x1", "/loop-plugin").Return(pluginTar(manifest), nil).Once()
	s.store.On("GetPluginState", s.ctx, "echo").Return(prev, nil).Once()
}

func (s *ServiceSuite) extracted() []os.DirEntry {
	entries, _ := os.ReadDir(filepath.Join(s.root, "echo", "images"))
	return entries
}

func (s *ServiceSuite) TestList() {
	states := []*db.PluginState{{PluginID: "echo"}}
	s.store.On("ListPluginStates", s.ctx).Return(states, nil).Once()
	got, err := s.svc.List(s.ctx)
	require.NoError(s.T(), err)
	require.Equal(s.T(), states, got)
}

func (s *ServiceSuite) TestStopDisabled() {
	s.runtime.On("Running").Return([]runtime.Running{
		{PluginID: "echo", Channel: runtime.Channel{ID: "on"}},
		{PluginID: "echo", Channel: runtime.Channel{ID: "off"}},
		{PluginID: "echo", Channel: runtime.Channel{ID: "broken"}},
	}).Once()
	s.svc.enabled = func(_ context.Context, pluginID string, ch runtime.Channel) (bool, error) {
		s.Require().Equal("echo", pluginID)
		if ch.ID == "broken" {
			return false, errors.New("bad config")
		}
		return ch.ID == "on", nil
	}
	s.runtime.On("Stop", s.ctx, "echo", "off").Once()
	require.True(s.T(), s.svc.StopDisabled(s.ctx))

	s.runtime.On("Running").Return([]runtime.Running{{PluginID: "echo", Channel: runtime.Channel{ID: "on"}}}).Once()
	require.False(s.T(), s.svc.StopDisabled(s.ctx))
}

func (s *ServiceSuite) TestRun() {
	tests := []struct {
		name    string
		idle    bool
		running []runtime.Running
		changed bool
	}{
		{name: "nothing stopped", running: []runtime.Running{}},
		{name: "idle stopped", idle: true, running: []runtime.Running{}, changed: true},
		{name: "disabled stopped", running: []runtime.Running{{PluginID: "echo", Channel: runtime.Channel{ID: "ch1"}}}, changed: true},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			defer s.TearDownTest()
			s.enabled = false
			ctx, cancel := context.WithCancel(s.ctx)
			defer cancel()
			// A tick may land before the cancel does: expect each call again.
			s.runtime.On("StopIdle", ctx).Return(tt.idle)
			s.runtime.On("Running").Return(tt.running).Run(func(mock.Arguments) {
				if !tt.changed {
					cancel()
				}
			})
			if len(tt.running) > 0 {
				s.runtime.On("Stop", ctx, "echo", "ch1")
			}
			// Run and each tick sync the schedules too; a failed sync is logged.
			s.store.On("ListPluginStates", ctx).Return(nil, errors.New("boom"))
			changed := false
			s.svc.Run(ctx, time.Millisecond, func() {
				changed = true
				cancel()
			}, slog.New(slog.DiscardHandler))
			require.Equal(s.T(), tt.changed, changed)
		})
	}
}

func (s *ServiceSuite) TestContainersAndStop() {
	cs := []*db.PluginContainer{{PluginID: "echo", ChannelID: "ch1", Status: db.PluginContainerRunning}}
	s.store.On("ListPluginContainers", s.ctx, "echo").Return(cs, nil).Once()
	got, err := s.svc.Containers(s.ctx, "echo")
	require.NoError(s.T(), err)
	require.Equal(s.T(), cs, got)

	s.runtime.On("Stop", s.ctx, "echo", "ch1").Once()
	s.svc.Stop(s.ctx, "echo", "ch1")
}

func (s *ServiceSuite) TestInspect() {
	s.expectInspect(nil)
	c, err := s.svc.Inspect(s.ctx, ref, false)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "echo", c.Manifest.ID)
	require.Empty(s.T(), s.extracted(), "inspecting keeps nothing")

	s.expectPull()
	s.docker.On("ImageInspect", s.ctx, ref).Return(s.info(), errors.New("boom")).Once()
	_, err = s.svc.Inspect(s.ctx, ref, false)
	require.ErrorContains(s.T(), err, "boom")
}

func (s *ServiceSuite) TestInstall() {
	s.expectInspect(nil)
	s.store.On("UpsertPluginState", s.ctx, mock.Anything).Return(nil).Once()
	c, err := s.svc.Install(s.ctx, ref, false, digest, plugin.Capabilities)
	require.NoError(s.T(), err)
	require.Len(s.T(), s.extracted(), 1)
	require.DirExists(s.T(), c.Dir)
}

func (s *ServiceSuite) TestUpgradeStopsContainers() {
	s.expectInspect(&db.PluginState{PluginID: "echo", Image: "ghcr.io/acme/echo@sha256:old"})
	s.store.On("UpsertPluginState", s.ctx, mock.Anything).Return(nil).Once()
	s.runtime.On("StopPlugin", s.ctx, "echo").Once()
	_, err := s.svc.Install(s.ctx, ref, false, "", plugin.Capabilities)
	require.NoError(s.T(), err)
}

func (s *ServiceSuite) TestInstallFailures() {
	s.expectPull()
	s.docker.On("ImageInspect", s.ctx, ref).Return(s.info(), errors.New("boom")).Once()
	_, err := s.svc.Install(s.ctx, ref, false, "", plugin.Capabilities)
	require.ErrorContains(s.T(), err, "boom")

	s.expectInspect(nil)
	_, err = s.svc.Install(s.ctx, ref, false, "sha256:other", plugin.Capabilities)
	require.ErrorIs(s.T(), err, ErrDigestChanged)
	require.Empty(s.T(), s.extracted())

	s.expectInspect(nil)
	_, err = s.svc.Install(s.ctx, ref, false, digest, nil)
	require.ErrorIs(s.T(), err, ErrNotApproved)
	require.Empty(s.T(), s.extracted())
}

func (s *ServiceSuite) TestRemove() {
	s.store.On("GetPluginState", s.ctx, "echo").Return(&db.PluginState{PluginID: "echo"}, nil).Once()
	s.store.On("ListPluginContainers", s.ctx, "echo").Return([]*db.PluginContainer{{ChannelID: "ch1"}, {ChannelID: "ch2"}}, nil).Once()
	s.runtime.On("Remove", s.ctx, "echo", "ch1", true).Return(nil).Once()
	s.runtime.On("Remove", s.ctx, "echo", "ch2", true).Return(errors.New("busy")).Once()
	s.store.On("DeletePluginState", s.ctx, "echo").Return(nil).Once()
	require.EqualError(s.T(), s.svc.Remove(s.ctx, "echo", true), "busy")
}

func (s *ServiceSuite) TestRemoveFailures() {
	boom := errors.New("boom")
	s.store.On("GetPluginState", s.ctx, "echo").Return(nil, boom).Once()
	require.ErrorIs(s.T(), s.svc.Remove(s.ctx, "echo", false), boom)

	s.store.On("GetPluginState", s.ctx, "echo").Return(nil, nil).Once()
	require.ErrorIs(s.T(), s.svc.Remove(s.ctx, "echo", false), ErrNotInstalled)

	s.store.On("GetPluginState", s.ctx, "echo").Return(&db.PluginState{}, nil).Once()
	s.store.On("ListPluginContainers", s.ctx, "echo").Return(nil, boom).Once()
	require.EqualError(s.T(), s.svc.Remove(s.ctx, "echo", false), "listing the plugin's containers: boom")
}

// installed extracts the plugin with actionManifest as the image it runs
// and returns its state.
func (s *ServiceSuite) installed(status string) *db.PluginState {
	c := s.candidate(strings.TrimPrefix(digest, "sha256:"))
	require.NoError(s.T(), os.WriteFile(filepath.Join(c.Dir, "plugin.json"), []byte(actionManifest), 0o644))
	return &db.PluginState{PluginID: "echo", Image: c.Image, Status: status}
}

const actionManifest = `{
	"id": "echo", "name": "Echo", "version": "1.0.0", "api": "1",
	"capabilities": ["agent.write", "schedule"],
	"actions": [{"id": "ping"}, {"id": "daily", "default_schedule": {"type": "cron", "schedule": "0 9 * * *", "enabled": true}}],
	"tools": [
		{"name": "echo", "description": "Echoes text", "input_schema": {"type": "object", "properties": {"text": {"type": "string"}}, "required": ["text"]}},
		{"name": "edit", "description": "Edits", "input_schema": {"type": "object"}},
	],
	"agents": [{"id": "say", "read_only": true, "tools": ["echo"]}, {"id": "writer", "tools": ["edit"]}],
}`

func (s *ServiceSuite) TestSyncSchedules() {
	now := time.Date(2026, 10, 11, 8, 0, 0, 0, time.UTC)
	local := types.PlatformLocal
	channels := []*db.Channel{
		{ChannelID: "on", GuildID: "g", DirPath: "/on", Platform: local},
		{ChannelID: "off", Platform: local},
		{ChannelID: "broken", Platform: local},
		{ChannelID: "thread", ParentID: "on", Platform: local},
		{ChannelID: "C0SLACK", Platform: types.PlatformSlack},
	}
	// setup enables echo in "on", fails to read the config of "broken" and
	// disables it elsewhere.
	setup := func() {
		s.SetupTest()
		s.svc.now = func() time.Time { return now }
		s.svc.enabled = func(_ context.Context, pluginID string, ch runtime.Channel) (bool, error) {
			s.Require().Equal("echo", pluginID)
			if ch.ID == "broken" {
				return false, errors.New("bad config")
			}
			return ch.ID == "on", nil
		}
	}
	daily := &db.ScheduledTask{
		ChannelID: "on", GuildID: "g", Schedule: "0 9 * * *", Type: db.TaskTypeCron,
		Prompt: "Runs the echo plugin's daily action.", Enabled: true,
		NextRunAt: time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC), PluginID: "echo", PluginAction: "daily",
	}
	approved := func(status string) *db.PluginState {
		st := s.installed(status)
		st.ApprovedCaps = []string{"agent.write", "schedule"}
		return st
	}
	boom := errors.New("boom")

	s.Run("seeds where enabled and pauses elsewhere", func() {
		setup()
		s.store.On("ListPluginStates", s.ctx).Return([]*db.PluginState{
			{PluginID: "unscheduled", ApprovedCaps: []string{"ui.panel"}},
			approved(db.PluginStatusInstalled),
		}, nil).Once()
		s.store.On("ListChannels", s.ctx).Return(channels, nil).Once()
		s.store.On("SeedPluginTask", s.ctx, daily).Return(true, nil).Once()
		s.store.On("SetPluginTasksPaused", s.ctx, "echo", "on", false).Return(nil).Once()
		s.store.On("SetPluginTasksPaused", s.ctx, "echo", "off", true).Return(nil).Once()
		require.NoError(s.T(), s.svc.SyncSchedules(s.ctx))
		s.store.AssertExpectations(s.T())
	})
	s.Run("a disabled plugin's tasks pause everywhere", func() {
		setup()
		s.store.On("ListPluginStates", s.ctx).Return([]*db.PluginState{approved(db.PluginStatusDisabled)}, nil).Once()
		s.store.On("ListChannels", s.ctx).Return(channels, nil).Once()
		for _, id := range []string{"on", "off", "broken"} {
			s.store.On("SetPluginTasksPaused", s.ctx, "echo", id, true).Return(nil).Once()
		}
		require.NoError(s.T(), s.svc.SyncSchedules(s.ctx))
		s.store.AssertExpectations(s.T())
	})

	tests := []struct {
		name    string
		setup   func()
		wantErr string
	}{
		{name: "plugins", setup: func() {
			s.store.On("ListPluginStates", s.ctx).Return(nil, boom).Once()
		}, wantErr: "listing the plugins: boom"},
		{name: "manifest and channels", setup: func() {
			s.store.On("ListPluginStates", s.ctx).Return([]*db.PluginState{
				{PluginID: "echo", Image: "sha256:00", ApprovedCaps: []string{"schedule"}},
				approved(db.PluginStatusInstalled),
			}, nil).Once()
			s.store.On("ListChannels", s.ctx).Return(nil, boom).Once()
		}, wantErr: "echo: reading the plugin's manifest"},
		{name: "channels", setup: func() {
			s.store.On("ListPluginStates", s.ctx).Return([]*db.PluginState{approved(db.PluginStatusInstalled)}, nil).Once()
			s.store.On("ListChannels", s.ctx).Return(nil, boom).Once()
		}, wantErr: "listing the channels: boom"},
		{name: "seed", setup: func() {
			s.store.On("ListPluginStates", s.ctx).Return([]*db.PluginState{approved(db.PluginStatusInstalled)}, nil).Once()
			s.store.On("ListChannels", s.ctx).Return(channels[:1], nil).Once()
			s.store.On("SeedPluginTask", s.ctx, daily).Return(false, boom).Once()
		}, wantErr: "creating echo's daily task in on: boom"},
		{name: "pause", setup: func() {
			s.store.On("ListPluginStates", s.ctx).Return([]*db.PluginState{approved(db.PluginStatusInstalled)}, nil).Once()
			s.store.On("ListChannels", s.ctx).Return(channels[1:2], nil).Once()
			s.store.On("SetPluginTasksPaused", s.ctx, "echo", "off", true).Return(boom).Once()
		}, wantErr: "boom"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			setup()
			tt.setup()
			err := s.svc.SyncSchedules(s.ctx)
			require.ErrorContains(s.T(), err, tt.wantErr)
			s.store.AssertExpectations(s.T())
		})
	}
}

func (s *ServiceSuite) TestRunAction() {
	st := s.installed(db.PluginStatusInstalled)
	ch := runtime.Channel{ID: "ch1", DirPath: "/src"}
	ep := runtime.Endpoint{URL: "http://127.0.0.1:1234", Secret: "s"}
	req := proxy.ActionRequest{Trigger: proxy.TriggerManual}
	s.store.On("GetPluginState", s.ctx, "echo").Return(st, nil).Once()
	s.caller.On("Action", s.ctx, ep, "ping", req).Return(nil).Once()

	s.runtime.On("Ensure", s.ctx, mock.MatchedBy(func(p runtime.Plugin) bool {
		return p.Manifest.ID == "echo" && p.Image == st.Image
	}), ch).Return(ep, nil).Once()
	require.NoError(s.T(), s.svc.RunAction(s.ctx, "echo", "ping", ch, req))
}

func (s *ServiceSuite) TestRunActionFailures() {
	ch := runtime.Channel{ID: "ch1"}
	req := proxy.ActionRequest{Trigger: proxy.TriggerManual}
	tests := []struct {
		name    string
		setup   func()
		action  string
		wantIs  error
		wantErr string
	}{
		{name: "store", setup: func() {
			s.store.On("GetPluginState", s.ctx, "echo").Return(nil, errors.New("boom")).Once()
		}, wantErr: "reading the plugin: boom"},
		{name: "not installed", setup: func() {
			s.store.On("GetPluginState", s.ctx, "echo").Return(nil, nil).Once()
		}, wantIs: ErrNotInstalled},
		{name: "disabled", setup: func() {
			s.store.On("GetPluginState", s.ctx, "echo").Return(s.installed(db.PluginStatusDisabled), nil).Once()
		}, wantErr: "the plugin is not runnable: it is disabled"},
		{name: "no manifest", setup: func() {
			s.store.On("GetPluginState", s.ctx, "echo").Return(&db.PluginState{PluginID: "echo", Image: "sha256:00", Status: db.PluginStatusInstalled}, nil).Once()
		}, wantErr: "reading the plugin's manifest"},
		{name: "unknown action", action: "nope", setup: func() {
			s.store.On("GetPluginState", s.ctx, "echo").Return(s.installed(db.PluginStatusInstalled), nil).Once()
		}, wantErr: "the plugin has no such action: nope"},
		{name: "not enabled", setup: func() {
			s.store.On("GetPluginState", s.ctx, "echo").Return(s.installed(db.PluginStatusInstalled), nil).Once()
			s.enabled = false
		}, wantIs: ErrNotEnabled},
		{name: "config", setup: func() {
			s.store.On("GetPluginState", s.ctx, "echo").Return(s.installed(db.PluginStatusInstalled), nil).Once()
			s.enabledErr = errors.New("bad hjson")
		}, wantErr: "reading the plugin's config: bad hjson"},
		{name: "start", setup: func() {
			s.store.On("GetPluginState", s.ctx, "echo").Return(s.installed(db.PluginStatusInstalled), nil).Once()
			s.runtime.On("Ensure", s.ctx, mock.Anything, ch).Return(runtime.Endpoint{}, errors.New("unhealthy")).Once()
		}, wantErr: "starting the plugin: unhealthy"},
		{name: "call", setup: func() {
			s.store.On("GetPluginState", s.ctx, "echo").Return(s.installed(db.PluginStatusInstalled), nil).Once()
			s.runtime.On("Ensure", s.ctx, mock.Anything, ch).Return(runtime.Endpoint{}, nil).Once()
			s.caller.On("Action", s.ctx, runtime.Endpoint{}, "ping", req).Return(errors.New("the plugin answered 500")).Once()
		}, wantErr: "the plugin answered 500"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			defer s.TearDownTest()
			tt.setup()
			action := tt.action
			if action == "" {
				action = "ping"
			}
			err := s.svc.RunAction(s.ctx, "echo", action, ch, req)
			if tt.wantIs != nil {
				require.ErrorIs(s.T(), err, tt.wantIs)
				return
			}
			require.ErrorContains(s.T(), err, tt.wantErr)
		})
	}
}

func (s *ServiceSuite) TestAgentRun() {
	tests := []struct {
		name      string
		agent     string
		caps      []string
		wantTools []string
		wantErr   string
	}{
		{name: "read-only with agent.readonly", agent: "say", caps: []string{"agent.readonly"}, wantTools: []string{"echo"}},
		{name: "read-only with agent.write", agent: "say", caps: []string{"agent.write"}, wantTools: []string{"echo"}},
		{name: "writer with agent.write", agent: "writer", caps: []string{"agent.write"}, wantTools: []string{"edit"}},
		{name: "writer with agent.readonly", agent: "writer", caps: []string{"agent.readonly"}, wantErr: "capability not approved: the agent writer needs agent.write"},
		{name: "nothing approved", agent: "say", wantErr: "capability not approved: the agent say needs agent.readonly"},
		{name: "unknown agent", agent: "nope", caps: []string{"agent.write"}, wantErr: "the plugin has no such agent: nope"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			defer s.TearDownTest()
			st := s.installed(db.PluginStatusInstalled)
			st.ApprovedCaps = tt.caps
			s.store.On("GetPluginState", s.ctx, "echo").Return(st, nil).Once()
			run, err := s.svc.AgentRun(s.ctx, "echo", tt.agent)
			if tt.wantErr != "" {
				require.EqualError(s.T(), err, tt.wantErr)
				return
			}
			require.NoError(s.T(), err)
			require.Equal(s.T(), tt.agent, run.Agent.ID)
			var names []string
			for _, t := range run.Tools {
				names = append(names, t.Name)
			}
			require.Equal(s.T(), tt.wantTools, names)
		})
	}

	s.Run("not installed", func() {
		s.SetupTest()
		defer s.TearDownTest()
		s.store.On("GetPluginState", s.ctx, "echo").Return(nil, nil).Once()
		_, err := s.svc.AgentRun(s.ctx, "echo", "say")
		require.ErrorIs(s.T(), err, ErrNotInstalled)
	})
}

func (s *ServiceSuite) TestCallTool() {
	ch := runtime.Channel{ID: "ch1", DirPath: "/src"}
	ep := runtime.Endpoint{URL: "http://127.0.0.1:1234", Secret: "s"}
	call := func(agent, input string) proxy.ToolRequest {
		return proxy.ToolRequest{RunID: "r1", Agent: agent, ThreadID: "plugin:echo-1", Input: json.RawMessage(input)}
	}
	tests := []struct {
		name    string
		tool    string
		req     proxy.ToolRequest
		setup   func(req proxy.ToolRequest)
		want    *proxy.ToolResult
		wantIs  error
		wantErr string
	}{
		{name: "forwarded", tool: "echo", req: call("say", `{"text":"hi"}`), setup: func(req proxy.ToolRequest) {
			s.runtime.On("Ensure", s.ctx, mock.Anything, ch).Return(ep, nil).Once()
			s.caller.On("Tool", s.ctx, ep, "echo", req).Return(&proxy.ToolResult{Content: "hi"}, nil).Once()
		}, want: &proxy.ToolResult{Content: "hi"}},
		{name: "unknown agent", tool: "echo", req: call("nope", `{}`), wantIs: ErrUnknownAgent},
		{name: "tool the agent wasn't given", tool: "edit", req: call("say", `{}`), wantIs: ErrUnknownTool},
		{name: "undeclared tool", tool: "nope", req: call("say", `{}`), wantIs: ErrUnknownTool},
		{name: "invalid input", tool: "echo", req: call("say", `{"text":1}`), wantIs: ErrInvalidInput},
		{name: "start", tool: "echo", req: call("say", `{"text":"hi"}`), setup: func(proxy.ToolRequest) {
			s.runtime.On("Ensure", s.ctx, mock.Anything, ch).Return(runtime.Endpoint{}, errors.New("unhealthy")).Once()
		}, wantErr: "starting the plugin: unhealthy"},
		{name: "call", tool: "echo", req: call("say", `{"text":"hi"}`), setup: func(req proxy.ToolRequest) {
			s.runtime.On("Ensure", s.ctx, mock.Anything, ch).Return(ep, nil).Once()
			s.caller.On("Tool", s.ctx, ep, "echo", req).Return(nil, errors.New("calling the plugin: refused")).Once()
		}, wantErr: "calling the plugin: refused"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			defer s.TearDownTest()
			s.store.On("GetPluginState", s.ctx, "echo").Return(s.installed(db.PluginStatusInstalled), nil).Once()
			if tt.setup != nil {
				tt.setup(tt.req)
			}
			res, err := s.svc.CallTool(s.ctx, "echo", tt.tool, ch, tt.req)
			switch {
			case tt.wantIs != nil:
				require.ErrorIs(s.T(), err, tt.wantIs)
			case tt.wantErr != "":
				require.EqualError(s.T(), err, tt.wantErr)
			default:
				require.NoError(s.T(), err)
				require.Equal(s.T(), tt.want, res)
			}
		})
	}

	s.Run("not installed", func() {
		s.SetupTest()
		defer s.TearDownTest()
		s.store.On("GetPluginState", s.ctx, "echo").Return(nil, nil).Once()
		_, err := s.svc.CallTool(s.ctx, "echo", "echo", ch, call("say", `{}`))
		require.ErrorIs(s.T(), err, ErrNotInstalled)
	})
}

func (s *ServiceSuite) TestAllows() {
	tests := []struct {
		name    string
		caps    []string
		status  string
		c       plugin.Capability
		wantIs  error
		wantErr string
	}{
		{name: "approved", caps: []string{"files.read"}, c: plugin.CapFilesRead},
		{name: "may run", c: ""},
		{name: "not approved", caps: []string{"git.read"}, c: plugin.CapFilesRead, wantErr: "capability not approved: files.read"},
		{name: "disabled", status: db.PluginStatusDisabled, wantIs: ErrNotRunnable},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			defer s.TearDownTest()
			status := tt.status
			if status == "" {
				status = db.PluginStatusInstalled
			}
			st := s.installed(status)
			st.ApprovedCaps = tt.caps
			s.store.On("GetPluginState", s.ctx, "echo").Return(st, nil).Once()
			err := s.svc.Allows(s.ctx, "echo", tt.c)
			switch {
			case tt.wantIs != nil:
				require.ErrorIs(s.T(), err, tt.wantIs)
			case tt.wantErr != "":
				require.EqualError(s.T(), err, tt.wantErr)
			default:
				require.NoError(s.T(), err)
			}
		})
	}
}

func (s *ServiceSuite) TestRunDoneAndX() {
	ch := runtime.Channel{ID: "ch1", DirPath: "/src"}
	ep := runtime.Endpoint{URL: "http://127.0.0.1:1234", Secret: "s"}
	done := proxy.RunDoneRequest{Status: proxy.RunStatusDone}
	xreq := proxy.XRequest{Method: "GET", Path: "/quiz"}
	xres := &proxy.XResponse{Status: 200, Body: []byte("{}")}

	s.Run("forwarded", func() {
		s.SetupTest()
		defer s.TearDownTest()
		s.store.On("GetPluginState", s.ctx, "echo").Return(s.installed(db.PluginStatusInstalled), nil).Twice()
		s.runtime.On("Ensure", s.ctx, mock.Anything, ch).Return(ep, nil).Twice()
		s.caller.On("RunDone", s.ctx, ep, "r1", done).Return(nil).Once()
		s.caller.On("X", s.ctx, ep, xreq).Return(xres, nil).Once()
		require.NoError(s.T(), s.svc.RunDone(s.ctx, "echo", ch, "r1", done))
		got, err := s.svc.X(s.ctx, "echo", ch, xreq)
		require.NoError(s.T(), err)
		require.Equal(s.T(), xres, got)
	})

	s.Run("not installed", func() {
		s.SetupTest()
		defer s.TearDownTest()
		s.store.On("GetPluginState", s.ctx, "echo").Return(nil, nil).Twice()
		require.ErrorIs(s.T(), s.svc.RunDone(s.ctx, "echo", ch, "r1", done), ErrNotInstalled)
		_, err := s.svc.X(s.ctx, "echo", ch, xreq)
		require.ErrorIs(s.T(), err, ErrNotInstalled)
	})

	s.Run("start", func() {
		s.SetupTest()
		defer s.TearDownTest()
		s.store.On("GetPluginState", s.ctx, "echo").Return(s.installed(db.PluginStatusInstalled), nil).Twice()
		s.runtime.On("Ensure", s.ctx, mock.Anything, ch).Return(runtime.Endpoint{}, errors.New("unhealthy")).Twice()
		require.EqualError(s.T(), s.svc.RunDone(s.ctx, "echo", ch, "r1", done), "starting the plugin: unhealthy")
		_, err := s.svc.X(s.ctx, "echo", ch, xreq)
		require.EqualError(s.T(), err, "starting the plugin: unhealthy")
	})
}

func (s *ServiceSuite) TestUI() {
	st := s.installed(db.PluginStatusInstalled)
	s.store.On("GetPluginState", s.ctx, "echo").Return(st, nil).Once()
	dir, m, err := s.svc.UI(s.ctx, "echo")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "echo", m.ID)
	require.FileExists(s.T(), filepath.Join(dir, "plugin.json"))

	s.store.On("GetPluginState", s.ctx, "echo").Return(nil, nil).Once()
	_, _, err = s.svc.UI(s.ctx, "echo")
	require.ErrorIs(s.T(), err, ErrNotInstalled)
}
