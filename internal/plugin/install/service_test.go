package install

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/agentgate"
	"github.com/radutopala/loop/internal/config"
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

func (m *mockCaller) Event(ctx context.Context, ep runtime.Endpoint, evt proxy.Event) error {
	return m.Called(ctx, ep, evt).Error(0)
}

func (m *mockCaller) Switch(ctx context.Context, ep runtime.Endpoint, id string, req proxy.SwitchRequest) (*proxy.SwitchState, error) {
	args := m.Called(ctx, ep, id, req)
	res, _ := args.Get(0).(*proxy.SwitchState)
	return res, args.Error(1)
}

func (m *mockRuntime) StopIdle(ctx context.Context) bool {
	return m.Called(ctx).Bool(0)
}

func (m *mockRuntime) Running() []runtime.Running {
	return m.Called().Get(0).([]runtime.Running)
}

func (m *mockRuntime) Audit(pluginID, channelID string, limit int) ([]agentgate.AuditEntry, error) {
	args := m.Called(pluginID, channelID, limit)
	v, _ := args.Get(0).([]agentgate.AuditEntry)
	return v, args.Error(1)
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
	s.svc = NewService(s.in, s.runtime, s.store, s.caller, func(_ context.Context, pluginID string, ch runtime.Channel) (config.PluginConfig, error) {
		s.Require().Equal("echo", pluginID)
		return config.PluginConfig{Enabled: &s.enabled}, s.enabledErr
	})
}

// onlyIn is a Project that enables echo in the channel "on", asking it for
// schedules, fails to read the config of "broken" and disables it
// elsewhere.
func (s *ServiceSuite) onlyIn(schedules map[string]config.PluginSchedule) Project {
	return func(_ context.Context, pluginID string, ch runtime.Channel) (config.PluginConfig, error) {
		s.Require().Equal("echo", pluginID)
		if ch.ID == "broken" {
			return config.PluginConfig{}, errors.New("bad config")
		}
		on := ch.ID == "on"
		return config.PluginConfig{Enabled: &on, Schedules: schedules}, nil
	}
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

func (s *ServiceSuite) TestAudit() {
	want := []agentgate.AuditEntry{{Kind: "exec", Target: "/bin/sh", Decision: "deny"}}
	s.runtime.On("Audit", "echo", "ch1", 50).Return(want, nil).Once()
	got, err := s.svc.Audit("echo", "ch1", 50)
	require.NoError(s.T(), err)
	require.Equal(s.T(), want, got)
}

func (s *ServiceSuite) TestRemoveChannel() {
	all := []*db.PluginContainer{
		{PluginID: "echo", ChannelID: "ch1"},
		{PluginID: "quiz", ChannelID: "ch1"},
		{PluginID: "echo", ChannelID: "ch2"},
	}
	s.store.On("ListPluginContainers", s.ctx, "").Return(all, nil).Once()
	s.runtime.On("Remove", s.ctx, "echo", "ch1", false).Return(nil).Once()
	s.runtime.On("Remove", s.ctx, "quiz", "ch1", false).Return(errors.New("busy")).Once()
	removed, err := s.svc.RemoveChannel(s.ctx, "ch1")
	require.True(s.T(), removed)
	require.EqualError(s.T(), err, "busy")

	s.store.On("ListPluginContainers", s.ctx, "").Return(all[2:], nil).Once()
	removed, err = s.svc.RemoveChannel(s.ctx, "ch1")
	require.False(s.T(), removed)
	require.NoError(s.T(), err)

	s.store.On("ListPluginContainers", s.ctx, "").Return(nil, errors.New("boom")).Once()
	removed, err = s.svc.RemoveChannel(s.ctx, "ch1")
	require.False(s.T(), removed)
	require.EqualError(s.T(), err, "listing plugin containers: boom")
}

func (s *ServiceSuite) TestPruneChannels() {
	s.store.On("ListChannels", s.ctx).Return([]*db.Channel{{ChannelID: "ch1"}, {ChannelID: "t1"}}, nil).Once()
	s.store.On("ListPluginContainers", s.ctx, "").Return([]*db.PluginContainer{
		{PluginID: "echo", ChannelID: "ch1"},
		{PluginID: "echo", ChannelID: "gone"},
		{PluginID: "quiz", ChannelID: "t1"},
		{PluginID: "quiz", ChannelID: "gone"},
	}, nil).Once()
	s.runtime.On("Remove", s.ctx, "echo", "gone", false).Return(nil).Once()
	s.runtime.On("Remove", s.ctx, "quiz", "gone", false).Return(nil).Once()
	require.NoError(s.T(), s.svc.PruneChannels(s.ctx))

	s.store.On("ListChannels", s.ctx).Return(nil, errors.New("boom")).Once()
	require.EqualError(s.T(), s.svc.PruneChannels(s.ctx), "listing channels: boom")
}

func (s *ServiceSuite) TestStopDisabled() {
	s.runtime.On("Running").Return([]runtime.Running{
		{PluginID: "echo", Channel: runtime.Channel{ID: "on"}},
		{PluginID: "echo", Channel: runtime.Channel{ID: "off"}},
		{PluginID: "echo", Channel: runtime.Channel{ID: "broken"}},
	}).Once()
	s.svc.project = s.onlyIn(nil)
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
		{"name": "score", "description": "Scores", "input_schema": {"type": "object"}, "output_schema": {"type": "object", "properties": {"score": {"type": "integer"}}, "required": ["score"]}},
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
	off := false
	setup := func(schedules map[string]config.PluginSchedule) {
		s.SetupTest()
		s.svc.now = func() time.Time { return now }
		s.svc.project = s.onlyIn(schedules)
	}
	task := func(action, typ, schedule string, enabled bool, next time.Time) *db.ScheduledTask {
		return &db.ScheduledTask{
			ChannelID: "on", GuildID: "g", Schedule: schedule, Type: db.TaskType(typ),
			Prompt: "Runs the echo plugin's " + action + " action.", Enabled: enabled,
			NextRunAt: next, PluginID: "echo", PluginAction: action,
		}
	}
	nine := time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)
	dailyApplied := `{"type":"cron","schedule":"0 9 * * *","enabled":true}`
	approved := func(status string) *db.PluginState {
		st := s.installed(status)
		st.ApprovedCaps = []string{"agent.write", "schedule"}
		return st
	}
	// onlyOn lists echo, installed, and the channel "on".
	onlyOn := func() {
		s.store.On("ListPluginStates", s.ctx).Return([]*db.PluginState{approved(db.PluginStatusInstalled)}, nil).Once()
		s.store.On("ListChannels", s.ctx).Return(channels[:1], nil).Once()
	}
	boom := errors.New("boom")

	s.Run("makes what the config asks where enabled and pauses elsewhere", func() {
		setup(map[string]config.PluginSchedule{"daily": {}})
		s.store.On("ListPluginStates", s.ctx).Return([]*db.PluginState{
			{PluginID: "unscheduled", ApprovedCaps: []string{"ui.panel"}},
			approved(db.PluginStatusInstalled),
		}, nil).Once()
		s.store.On("ListChannels", s.ctx).Return(channels, nil).Once()
		s.store.On("GetPluginSchedule", s.ctx, "echo", "on", "ping").Return(nil, nil).Once()
		s.store.On("GetPluginSchedule", s.ctx, "echo", "on", "daily").Return(nil, nil).Once()
		s.store.On("ApplyPluginSchedule", s.ctx, task("daily", "cron", "0 9 * * *", true, nine), dailyApplied).Return(nil).Once()
		s.store.On("SetPluginTasksPaused", s.ctx, "echo", "on", false).Return(nil).Once()
		s.store.On("SetPluginTasksPaused", s.ctx, "echo", "off", true).Return(nil).Once()
		require.NoError(s.T(), s.svc.SyncSchedules(s.ctx))
		s.store.AssertExpectations(s.T())
	})
	s.Run("a disabled plugin's tasks pause everywhere", func() {
		setup(nil)
		s.store.On("ListPluginStates", s.ctx).Return([]*db.PluginState{approved(db.PluginStatusDisabled)}, nil).Once()
		s.store.On("ListChannels", s.ctx).Return(channels, nil).Once()
		for _, id := range []string{"on", "off", "broken"} {
			s.store.On("SetPluginTasksPaused", s.ctx, "echo", id, true).Return(nil).Once()
		}
		require.NoError(s.T(), s.svc.SyncSchedules(s.ctx))
		s.store.AssertExpectations(s.T())
	})

	tests := []struct {
		name      string
		schedules map[string]config.PluginSchedule
		setup     func()
		wantErr   string
	}{
		{name: "an unchanged ask leaves the owner's edits", schedules: map[string]config.PluginSchedule{"daily": {}}, setup: func() {
			onlyOn()
			s.store.On("GetPluginSchedule", s.ctx, "echo", "on", "ping").Return(nil, nil).Once()
			s.store.On("GetPluginSchedule", s.ctx, "echo", "on", "daily").Return(&db.PluginSchedule{Applied: dailyApplied}, nil).Once()
			s.store.On("SetPluginTasksPaused", s.ctx, "echo", "on", false).Return(nil).Once()
		}},
		{name: "an ask after a delete makes the task again", schedules: map[string]config.PluginSchedule{"daily": {}}, setup: func() {
			onlyOn()
			s.store.On("GetPluginSchedule", s.ctx, "echo", "on", "ping").Return(nil, nil).Once()
			s.store.On("GetPluginSchedule", s.ctx, "echo", "on", "daily").Return(&db.PluginSchedule{}, nil).Once()
			s.store.On("ApplyPluginSchedule", s.ctx, task("daily", "cron", "0 9 * * *", true, nine), dailyApplied).Return(nil).Once()
			s.store.On("SetPluginTasksPaused", s.ctx, "echo", "on", false).Return(nil).Once()
		}},
		{name: "the config's own schedule, turned off", schedules: map[string]config.PluginSchedule{
			"ping": {Type: "interval", Schedule: "2h", Enabled: &off},
			"gone": {},
		}, setup: func() {
			onlyOn()
			s.store.On("GetPluginSchedule", s.ctx, "echo", "on", "ping").Return(nil, nil).Once()
			s.store.On("GetPluginSchedule", s.ctx, "echo", "on", "daily").Return(nil, nil).Once()
			s.store.On("ApplyPluginSchedule", s.ctx, task("ping", "interval", "2h", false, now.Add(2*time.Hour)), `{"type":"interval","schedule":"2h","enabled":false}`).Return(nil).Once()
			s.store.On("SetPluginTasksPaused", s.ctx, "echo", "on", false).Return(nil).Once()
		}},
		{name: "a task the config stopped asking for is deleted", setup: func() {
			onlyOn()
			s.store.On("GetPluginSchedule", s.ctx, "echo", "on", "ping").Return(&db.PluginSchedule{Applied: "{}"}, nil).Once()
			s.store.On("GetPluginSchedule", s.ctx, "echo", "on", "daily").Return(&db.PluginSchedule{TaskID: 7}, nil).Once()
			s.store.On("DeletePluginSchedule", s.ctx, "echo", "on", "ping").Return(nil).Once()
			s.store.On("SetPluginTasksPaused", s.ctx, "echo", "on", false).Return(nil).Once()
		}},
		{name: "delete", setup: func() {
			onlyOn()
			s.store.On("GetPluginSchedule", s.ctx, "echo", "on", "ping").Return(&db.PluginSchedule{Applied: "{}"}, nil).Once()
			s.store.On("GetPluginSchedule", s.ctx, "echo", "on", "daily").Return(nil, nil).Once()
			s.store.On("DeletePluginSchedule", s.ctx, "echo", "on", "ping").Return(boom).Once()
			s.store.On("SetPluginTasksPaused", s.ctx, "echo", "on", false).Return(nil).Once()
		}, wantErr: "deleting echo's ping task in on: boom"},
		{name: "read", setup: func() {
			onlyOn()
			s.store.On("GetPluginSchedule", s.ctx, "echo", "on", "ping").Return(nil, boom).Once()
			s.store.On("GetPluginSchedule", s.ctx, "echo", "on", "daily").Return(nil, nil).Once()
			s.store.On("SetPluginTasksPaused", s.ctx, "echo", "on", false).Return(nil).Once()
		}, wantErr: "reading echo's ping task in on: boom"},
		{name: "apply", schedules: map[string]config.PluginSchedule{"daily": {}}, setup: func() {
			onlyOn()
			s.store.On("GetPluginSchedule", s.ctx, "echo", "on", "ping").Return(nil, nil).Once()
			s.store.On("GetPluginSchedule", s.ctx, "echo", "on", "daily").Return(nil, nil).Once()
			s.store.On("ApplyPluginSchedule", s.ctx, mock.Anything, dailyApplied).Return(boom).Once()
			s.store.On("SetPluginTasksPaused", s.ctx, "echo", "on", false).Return(nil).Once()
		}, wantErr: "making echo's daily task in on: boom"},
		{name: "no schedule", schedules: map[string]config.PluginSchedule{"ping": {}}, setup: func() {
			onlyOn()
			s.store.On("GetPluginSchedule", s.ctx, "echo", "on", "ping").Return(nil, nil).Once()
			s.store.On("GetPluginSchedule", s.ctx, "echo", "on", "daily").Return(nil, nil).Once()
			s.store.On("SetPluginTasksPaused", s.ctx, "echo", "on", false).Return(nil).Once()
		}, wantErr: "plugins.echo.schedules.ping: invalid task schedule"},
		{name: "type", schedules: map[string]config.PluginSchedule{"daily": {Type: "once", Schedule: "2026-10-12T09:00:00Z"}}, setup: func() {
			onlyOn()
			s.store.On("GetPluginSchedule", s.ctx, "echo", "on", "ping").Return(nil, nil).Once()
			s.store.On("GetPluginSchedule", s.ctx, "echo", "on", "daily").Return(nil, nil).Once()
			s.store.On("SetPluginTasksPaused", s.ctx, "echo", "on", false).Return(nil).Once()
		}, wantErr: `plugins.echo.schedules.daily: type must be cron or interval, not "once"`},
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
		{name: "pause", setup: func() {
			s.store.On("ListPluginStates", s.ctx).Return([]*db.PluginState{approved(db.PluginStatusInstalled)}, nil).Once()
			s.store.On("ListChannels", s.ctx).Return(channels[1:2], nil).Once()
			s.store.On("SetPluginTasksPaused", s.ctx, "echo", "off", true).Return(boom).Once()
		}, wantErr: "boom"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			setup(tt.schedules)
			tt.setup()
			err := s.svc.SyncSchedules(s.ctx)
			if tt.wantErr == "" {
				require.NoError(s.T(), err)
			} else {
				require.ErrorContains(s.T(), err, tt.wantErr)
			}
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

func (s *ServiceSuite) TestWorkflowTool() {
	ch := runtime.Channel{ID: "ch1", DirPath: "/src"}
	ep := runtime.Endpoint{URL: "http://127.0.0.1:1234", Secret: "s"}
	call := func(input string) proxy.ToolRequest {
		return proxy.ToolRequest{RunID: "workflow:r1:n1", Input: json.RawMessage(input)}
	}
	answers := func(tool string, res *proxy.ToolResult) func(proxy.ToolRequest) {
		return func(req proxy.ToolRequest) {
			s.runtime.On("Ensure", s.ctx, mock.Anything, ch).Return(ep, nil).Once()
			s.caller.On("Tool", s.ctx, ep, tool, req).Return(res, nil).Once()
		}
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
		{name: "any declared tool", tool: "edit", req: call(`{}`), setup: func(req proxy.ToolRequest) {
			s.runtime.On("Ensure", s.ctx, mock.Anything, ch).Return(ep, nil).Once()
			s.caller.On("Tool", s.ctx, ep, "edit", req).Return(&proxy.ToolResult{Content: "done"}, nil).Once()
		}, want: &proxy.ToolResult{Content: "done"}},
		{name: "undeclared tool", tool: "nope", req: call(`{}`), wantIs: ErrUnknownTool},
		{name: "invalid input", tool: "echo", req: call(`{"text":1}`), wantIs: ErrInvalidInput},
		{name: "structured", tool: "score", req: call(`{}`), setup: answers("score", &proxy.ToolResult{Content: "7", StructuredContent: json.RawMessage(`{"score":7}`)}),
			want: &proxy.ToolResult{Content: "7", StructuredContent: json.RawMessage(`{"score":7}`)}},
		{name: "structured without a schema", tool: "edit", req: call(`{}`), setup: answers("edit", &proxy.ToolResult{Content: "ok", StructuredContent: json.RawMessage(`{"a":[1]}`)}),
			want: &proxy.ToolResult{Content: "ok", StructuredContent: json.RawMessage(`{"a":[1]}`)}},
		{name: "an error's structured content is dropped", tool: "score", req: call(`{}`), setup: answers("score", &proxy.ToolResult{Content: "no", IsError: true, StructuredContent: json.RawMessage(`[]`)}),
			want: &proxy.ToolResult{Content: "no", IsError: true}},
		{name: "missing structured content", tool: "score", req: call(`{}`), setup: answers("score", &proxy.ToolResult{Content: "7"}),
			wantErr: "invalid tool result: the result has no structured_content, which the tool's output_schema requires"},
		{name: "structured content off schema", tool: "score", req: call(`{}`), setup: answers("score", &proxy.ToolResult{StructuredContent: json.RawMessage(`{"score":"high"}`)}), wantIs: ErrInvalidOutput},
		{name: "structured content not an object", tool: "edit", req: call(`{}`), setup: answers("edit", &proxy.ToolResult{StructuredContent: json.RawMessage(`[1]`)}),
			wantErr: "invalid tool result: structured_content must be a JSON object"},
		{name: "structured content not JSON", tool: "edit", req: call(`{}`), setup: answers("edit", &proxy.ToolResult{StructuredContent: json.RawMessage(`{`)}),
			wantErr: "invalid tool result: structured_content is not JSON: unexpected end of JSON input"},
		{name: "call", tool: "edit", req: call(`{}`), setup: func(req proxy.ToolRequest) {
			s.runtime.On("Ensure", s.ctx, mock.Anything, ch).Return(ep, nil).Once()
			s.caller.On("Tool", s.ctx, ep, "edit", req).Return(nil, errors.New("refused")).Once()
		}, wantErr: "refused"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			defer s.TearDownTest()
			s.store.On("GetPluginState", s.ctx, "echo").Return(s.installed(db.PluginStatusInstalled), nil).Once()
			if tt.setup != nil {
				tt.setup(tt.req)
			}
			res, err := s.svc.WorkflowTool(s.ctx, "echo", tt.tool, ch, tt.req)
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
		_, err := s.svc.WorkflowTool(s.ctx, "echo", "echo", ch, call(`{}`))
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

func (s *ServiceSuite) TestShows() {
	ch := runtime.Channel{ID: "ch1", DirPath: "/src"}
	tests := []struct {
		name       string
		state      func() *db.PluginState
		readErr    error
		enabled    bool
		enabledErr error
		want       bool
		wantErr    string
	}{
		{name: "shown", state: func() *db.PluginState { return s.installedWith("ui.badge") }, enabled: true, want: true},
		{name: "not enabled", state: func() *db.PluginState { return s.installedWith("ui.badge") }},
		{name: "config unreadable", state: func() *db.PluginState { return s.installedWith("ui.badge") }, enabledErr: errors.New("bad config"), wantErr: "bad config"},
		{name: "not approved", state: func() *db.PluginState { return s.installedWith("ui.notification.card") }, enabled: true},
		{name: "not installed", state: func() *db.PluginState { return nil }, enabled: true},
		{name: "disabled", state: func() *db.PluginState { return s.installed(db.PluginStatusDisabled) }, enabled: true},
		{name: "store fails", state: func() *db.PluginState { return nil }, readErr: errors.New("db down"), wantErr: "reading the plugin: db down"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			defer s.TearDownTest()
			s.enabled, s.enabledErr = tt.enabled, tt.enabledErr
			s.store.On("GetPluginState", s.ctx, "echo").Return(tt.state(), tt.readErr).Once()
			got, err := s.svc.Shows(s.ctx, "echo", plugin.CapUIBadge, ch)
			if tt.wantErr != "" {
				require.EqualError(s.T(), err, tt.wantErr)
				return
			}
			require.NoError(s.T(), err)
			require.Equal(s.T(), tt.want, got)
		})
	}
}

// installedWith is an installed plugin with caps approved.
func (s *ServiceSuite) installedWith(caps ...string) *db.PluginState {
	st := s.installed(db.PluginStatusInstalled)
	st.ApprovedCaps = caps
	return st
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

	s.Run("woken", func() {
		s.SetupTest()
		defer s.TearDownTest()
		s.store.On("GetPluginState", s.ctx, "echo").Return(s.installed(db.PluginStatusInstalled), nil).Once()
		s.runtime.On("Ensure", s.ctx, mock.Anything, ch).Return(ep, nil).Once()
		require.NoError(s.T(), s.svc.Wake(s.ctx, "echo", ch))
		s.store.On("GetPluginState", s.ctx, "gone").Return(nil, nil).Once()
		require.ErrorIs(s.T(), s.svc.Wake(s.ctx, "gone", ch), ErrNotInstalled)
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

const eventsManifest = `{
	"id": "echo", "name": "Echo", "version": "1.0.0", "api": "1",
	"capabilities": ["events.subscribe"],
	"events": ["message.created"],
}`

// subscriber returns echo installed with eventsManifest and caps approved.
func (s *ServiceSuite) subscriber(caps ...string) *db.PluginState {
	st := s.installed(db.PluginStatusInstalled)
	require.NoError(s.T(), os.WriteFile(filepath.Join(s.in.Dir(st), "plugin.json"), []byte(eventsManifest), 0o644))
	st.ApprovedCaps = caps
	return st
}

func (s *ServiceSuite) TestDeliver() {
	ch := runtime.Channel{ID: "ch1", DirPath: "/src"}
	ep := runtime.Endpoint{URL: "http://127.0.0.1:1234", Secret: "s"}
	evt := proxy.Event{Type: "message.created", ChannelID: "ch1", Data: map[string]string{"content": "hi"}}
	sub := string(plugin.CapEventsSub)
	tests := []struct {
		name    string
		evt     proxy.Event
		states  func() []*db.PluginState
		setup   func()
		wantErr string
	}{
		{name: "delivered", states: func() []*db.PluginState { return []*db.PluginState{s.subscriber(sub)} }, setup: func() {
			s.runtime.On("Ensure", s.ctx, mock.Anything, ch).Return(ep, nil).Once()
			s.caller.On("Event", s.ctx, ep, evt).Return(nil).Once()
		}},
		{name: "not approved", states: func() []*db.PluginState { return []*db.PluginState{s.subscriber()} }},
		{name: "not installed", states: func() []*db.PluginState {
			st := s.subscriber(sub)
			st.Status = db.PluginStatusDisabled
			return []*db.PluginState{st}
		}},
		{name: "another type", evt: proxy.Event{Type: "agent.status"}, states: func() []*db.PluginState { return []*db.PluginState{s.subscriber(sub)} }},
		{name: "not enabled in the channel", states: func() []*db.PluginState { return []*db.PluginState{s.subscriber(sub)} }, setup: func() {
			s.enabled = false
		}},
		{name: "no manifest", states: func() []*db.PluginState {
			return []*db.PluginState{{PluginID: "echo", Image: "sha256:00", Status: db.PluginStatusInstalled, ApprovedCaps: []string{sub}}}
		}, wantErr: "echo: reading the plugin's manifest"},
		{name: "start fails", states: func() []*db.PluginState { return []*db.PluginState{s.subscriber(sub)} }, setup: func() {
			s.runtime.On("Ensure", s.ctx, mock.Anything, ch).Return(runtime.Endpoint{}, errors.New("unhealthy")).Once()
		}, wantErr: "echo: starting the plugin: unhealthy"},
		{name: "call fails", states: func() []*db.PluginState { return []*db.PluginState{s.subscriber(sub)} }, setup: func() {
			s.runtime.On("Ensure", s.ctx, mock.Anything, ch).Return(ep, nil).Once()
			s.caller.On("Event", s.ctx, ep, evt).Return(errors.New("the plugin answered 500")).Once()
		}, wantErr: "echo: the plugin answered 500"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			defer s.TearDownTest()
			s.store.On("ListPluginStates", s.ctx).Return(tt.states(), nil).Once()
			if tt.setup != nil {
				tt.setup()
			}
			e := tt.evt
			if e.Type == "" {
				e = evt
			}
			err := s.svc.Deliver(s.ctx, ch, nil, e)
			if tt.wantErr == "" {
				require.NoError(s.T(), err)
				return
			}
			require.ErrorContains(s.T(), err, tt.wantErr)
		})
	}

	s.Run("store", func() {
		s.SetupTest()
		defer s.TearDownTest()
		s.store.On("ListPluginStates", s.ctx).Return(nil, errors.New("boom")).Once()
		require.EqualError(s.T(), s.svc.Deliver(s.ctx, ch, nil, evt), "listing plugins: boom")
	})
}

func (s *ServiceSuite) TestNotifySettings() {
	ch := runtime.Channel{ID: "ch1", DirPath: "/src"}
	ep := runtime.Endpoint{URL: "http://127.0.0.1:1234", Secret: "s"}
	at := time.UnixMilli(7)
	values := map[string]any{"on": true}
	evt := proxy.Event{Type: plugin.EventSettingsChanged, ChannelID: "ch1", Data: plugin.SettingsChanged{Settings: values}, Timestamp: 7}
	sub := string(plugin.CapEventsSub)
	// listener returns echo subscribed to settings.changed, with caps
	// approved.
	listener := func(caps ...string) *db.PluginState {
		st := s.subscriber(caps...)
		manifest := strings.Replace(eventsManifest, `"message.created"`, `"settings.changed"`, 1)
		require.NoError(s.T(), os.WriteFile(filepath.Join(s.in.Dir(st), "plugin.json"), []byte(manifest), 0o644))
		return st
	}
	tests := []struct {
		name        string
		state       func() *db.PluginState
		settingsErr error
		setup       func()
		wantErr     string
	}{
		{name: "notified", state: func() *db.PluginState { return listener(sub) }, setup: func() {
			s.runtime.On("Ensure", s.ctx, mock.Anything, ch).Return(ep, nil).Once()
			s.caller.On("Event", s.ctx, ep, evt).Return(nil).Once()
		}},
		{name: "not subscribed", state: func() *db.PluginState { return s.subscriber(sub) }},
		{name: "not approved", state: func() *db.PluginState { return listener() }},
		{name: "not installed", state: func() *db.PluginState { return nil }, wantErr: "echo: " + ErrNotInstalled.Error()},
		{name: "settings fail", state: func() *db.PluginState { return listener(sub) }, settingsErr: errors.New("bad config"), wantErr: "echo: bad config"},
		{name: "call fails", state: func() *db.PluginState { return listener(sub) }, setup: func() {
			s.runtime.On("Ensure", s.ctx, mock.Anything, ch).Return(ep, nil).Once()
			s.caller.On("Event", s.ctx, ep, evt).Return(errors.New("the plugin answered 500")).Once()
		}, wantErr: "echo: the plugin answered 500"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			defer s.TearDownTest()
			s.svc.now = func() time.Time { return at }
			s.runtime.On("Running").Return([]runtime.Running{{PluginID: "echo", Channel: ch}}).Once()
			s.store.On("GetPluginState", s.ctx, "echo").Return(tt.state(), nil).Once()
			if tt.setup != nil {
				tt.setup()
			}
			err := s.svc.NotifySettings(s.ctx, func(_ context.Context, pluginID string, m *plugin.Manifest, got runtime.Channel) (map[string]any, error) {
				require.Equal(s.T(), "echo", pluginID)
				require.Equal(s.T(), "echo", m.ID)
				require.Equal(s.T(), ch, got)
				return values, tt.settingsErr
			})
			if tt.wantErr == "" {
				require.NoError(s.T(), err)
				return
			}
			require.EqualError(s.T(), err, tt.wantErr)
		})
	}
}

func (s *ServiceSuite) TestDeliverThreadEvent() {
	ch := runtime.Channel{ID: "ch1", DirPath: "/src"}
	thread := runtime.Channel{ID: "th1", DirPath: "/src/.worktrees/th1"}
	threadEP := runtime.Endpoint{URL: "http://127.0.0.1:1234", Secret: "t"}
	channelEP := runtime.Endpoint{URL: "http://127.0.0.1:1235", Secret: "c"}
	evt := proxy.Event{Type: "message.created", ChannelID: "ch1", ThreadID: "th1"}
	tests := []struct {
		name         string
		threadEvents string
		toThread     bool
		toChannel    bool
	}{
		{name: "default", toThread: true},
		{name: "thread", threadEvents: plugin.ThreadEventsThread, toThread: true},
		{name: "channel", threadEvents: plugin.ThreadEventsChannel, toChannel: true},
		{name: "both", threadEvents: plugin.ThreadEventsBoth, toThread: true, toChannel: true},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			defer s.TearDownTest()
			st := s.installed(db.PluginStatusInstalled)
			manifest := strings.Replace(eventsManifest, `"events"`, fmt.Sprintf(`"thread_events": %q, "events"`, tt.threadEvents), 1)
			require.NoError(s.T(), os.WriteFile(filepath.Join(s.in.Dir(st), "plugin.json"), []byte(manifest), 0o644))
			st.ApprovedCaps = []string{string(plugin.CapEventsSub)}
			s.store.On("ListPluginStates", s.ctx).Return([]*db.PluginState{st}, nil).Once()
			if tt.toThread {
				s.runtime.On("Ensure", s.ctx, mock.Anything, thread).Return(threadEP, nil).Once()
				s.caller.On("Event", s.ctx, threadEP, evt).Return(nil).Once()
			}
			if tt.toChannel {
				s.runtime.On("Ensure", s.ctx, mock.Anything, ch).Return(channelEP, errors.New("unhealthy")).Once()
			}
			err := s.svc.Deliver(s.ctx, ch, &thread, evt)
			if tt.toChannel {
				require.EqualError(s.T(), err, "echo: starting the plugin: unhealthy")
				return
			}
			require.NoError(s.T(), err)
		})
	}
}

// startManifest is echo subscribed to message.created in its threads'
// containers and the channel's, which an event starts only as eventsStart
// says.
const startManifest = `{
	"id": "echo", "name": "Echo", "version": "1.0.0", "api": "1",
	"capabilities": ["events.subscribe", "ui.composer_switch"],
	"events": ["message.created"],
	"thread_events": "both",
	"events_start": %s,
	"settings": {"on": {"type": "boolean", "default": false}},
	"ui": {"composer_switches": [{"id": "mode", "label": "Mode", "icon": "ui/mode.svg"}]},
}`

func (s *ServiceSuite) TestDeliverEventsStart() {
	ch := runtime.Channel{ID: "ch1", DirPath: "/src"}
	thread := runtime.Channel{ID: "th1", DirPath: "/src/.worktrees/th1"}
	ep := runtime.Endpoint{URL: "http://127.0.0.1:1234", Secret: "s"}
	evt := proxy.Event{Type: "message.created", ChannelID: "ch1", ThreadID: "th1"}
	both := `{"settings": ["on"], "switches": ["mode"]}`
	on := func(pluginID, channelID, switchID string) *db.PluginSwitch {
		return &db.PluginSwitch{PluginID: pluginID, ChannelID: channelID, SwitchID: switchID, On: true}
	}
	tests := []struct {
		name        string
		eventsStart string
		running     []string
		settings    map[string]any
		projectErr  error
		switches    []*db.PluginSwitch
		switchesErr error
		// lists is whether the switches are looked up.
		lists   bool
		want    []runtime.Channel
		wantErr string
	}{
		{name: "running", eventsStart: both, running: []string{"th1", "ch1"}, want: []runtime.Channel{thread, ch}},
		{name: "setting on", eventsStart: both, settings: map[string]any{"on": true}, want: []runtime.Channel{thread, ch}},
		{name: "switch on in the chat", eventsStart: both, lists: true, switches: []*db.PluginSwitch{on("echo", "th1", "mode")}, want: []runtime.Channel{thread, ch}},
		{name: "nothing on", eventsStart: both, lists: true, switches: []*db.PluginSwitch{
			on("echo", "ch1", "mode"), on("other", "th1", "mode"), on("echo", "th1", "other"), {PluginID: "echo", ChannelID: "th1", SwitchID: "mode"},
		}},
		{name: "only the running one", eventsStart: both, running: []string{"ch1", "elsewhere"}, lists: true, want: []runtime.Channel{ch}},
		{name: "another plugin's container", eventsStart: `{"switches": ["mode"]}`, lists: true},
		{name: "settings only", eventsStart: `{"settings": ["on"]}`},
		{name: "never", eventsStart: `{}`},
		{name: "project fails", eventsStart: both, projectErr: errors.New("bad config"), wantErr: "echo: bad config"},
		{name: "switches fail", eventsStart: both, lists: true, switchesErr: errors.New("boom"), wantErr: "echo: boom"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			defer s.TearDownTest()
			st := s.installed(db.PluginStatusInstalled)
			require.NoError(s.T(), os.WriteFile(filepath.Join(s.in.Dir(st), "plugin.json"), []byte(fmt.Sprintf(startManifest, tt.eventsStart)), 0o644))
			st.ApprovedCaps = []string{string(plugin.CapEventsSub)}
			s.svc.project = func(_ context.Context, pluginID string, _ runtime.Channel) (config.PluginConfig, error) {
				s.Require().Equal("echo", pluginID)
				return config.PluginConfig{Enabled: &s.enabled, Settings: tt.settings}, tt.projectErr
			}
			s.store.On("ListPluginStates", s.ctx).Return([]*db.PluginState{st}, nil).Once()
			running := []runtime.Running{{PluginID: "other", Channel: thread}}
			for _, id := range tt.running {
				running = append(running, runtime.Running{PluginID: "echo", Channel: runtime.Channel{ID: id}})
			}
			s.runtime.On("Running").Return(running).Once()
			if tt.lists {
				s.store.On("ListPluginSwitches", s.ctx).Return(tt.switches, tt.switchesErr).Once()
			}
			for _, target := range tt.want {
				s.runtime.On("Ensure", s.ctx, mock.Anything, target).Return(ep, nil).Once()
			}
			if len(tt.want) > 0 {
				s.caller.On("Event", s.ctx, ep, evt).Return(nil).Times(len(tt.want))
			}
			err := s.svc.Deliver(s.ctx, ch, &thread, evt)
			if tt.wantErr != "" {
				require.EqualError(s.T(), err, tt.wantErr)
				return
			}
			require.NoError(s.T(), err)
		})
	}
}

const switchManifest = `{
	"id": "echo", "name": "Echo", "version": "1.0.0", "api": "1",
	"capabilities": ["ui.composer_switch", "live.control", "events.subscribe"],
	"events": ["live.system"],
	"live": {"control": ["set_mode"], "messages": ["mode_state"]},
	"ui": {"composer_switches": [{"id": "mode", "label": "Mode", "icon": "ui/mode.svg"}]},
}`

// switcher returns echo installed with switchManifest and caps approved.
func (s *ServiceSuite) switcher(caps ...string) *db.PluginState {
	st := s.installed(db.PluginStatusInstalled)
	require.NoError(s.T(), os.WriteFile(filepath.Join(s.in.Dir(st), "plugin.json"), []byte(switchManifest), 0o644))
	st.ApprovedCaps = caps
	return st
}

func (s *ServiceSuite) TestSwitch() {
	ch := runtime.Channel{ID: "ch1", DirPath: "/src"}
	ep := runtime.Endpoint{URL: "http://127.0.0.1:1234", Secret: "s"}
	req := proxy.SwitchRequest{On: true, ThreadID: "th1"}
	sw := string(plugin.CapUISwitch)
	tests := []struct {
		name    string
		id      string
		state   func() *db.PluginState
		setup   func()
		want    *proxy.SwitchState
		wantIs  error
		wantErr string
	}{
		{name: "forwarded", id: "mode", state: func() *db.PluginState { return s.switcher(sw) }, setup: func() {
			s.runtime.On("Ensure", s.ctx, mock.Anything, ch).Return(ep, nil).Once()
			s.caller.On("Switch", s.ctx, ep, "mode", req).Return(&proxy.SwitchState{On: true}, nil).Once()
		}, want: &proxy.SwitchState{On: true}},
		{name: "not installed", id: "mode", state: func() *db.PluginState { return nil }, wantIs: ErrNotInstalled},
		{name: "not approved", id: "mode", state: func() *db.PluginState { return s.switcher() }, wantIs: ErrNotApproved},
		{name: "unknown switch", id: "nope", state: func() *db.PluginState { return s.switcher(sw) }, wantIs: ErrUnknownSwitch},
		{name: "start", id: "mode", state: func() *db.PluginState { return s.switcher(sw) }, setup: func() {
			s.runtime.On("Ensure", s.ctx, mock.Anything, ch).Return(runtime.Endpoint{}, errors.New("unhealthy")).Once()
		}, wantErr: "starting the plugin: unhealthy"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			defer s.TearDownTest()
			s.store.On("GetPluginState", s.ctx, "echo").Return(tt.state(), nil).Once()
			if tt.setup != nil {
				tt.setup()
			}
			res, err := s.svc.Switch(s.ctx, "echo", tt.id, ch, req)
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
}

func (s *ServiceSuite) TestDeliverLiveSystem() {
	ch := runtime.Channel{ID: "ch1", DirPath: "/src"}
	ep := runtime.Endpoint{URL: "http://127.0.0.1:1234", Secret: "s"}
	sub, live := string(plugin.CapEventsSub), string(plugin.CapLiveControl)
	event := func(data any) proxy.Event {
		return proxy.Event{Type: plugin.EventLiveSystem, ChannelID: "ch1", Data: data}
	}
	tests := []struct {
		name      string
		caps      []string
		evt       proxy.Event
		delivered bool
	}{
		{name: "asked for", caps: []string{sub, live}, evt: event(plugin.LiveSystem{Subtype: "mode_state"}), delivered: true},
		{name: "another subtype", caps: []string{sub, live}, evt: event(plugin.LiveSystem{Subtype: "status"})},
		{name: "live.control not approved", caps: []string{sub}, evt: event(plugin.LiveSystem{Subtype: "mode_state"})},
		{name: "not a live system message", caps: []string{sub, live}, evt: event("x")},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			defer s.TearDownTest()
			s.store.On("ListPluginStates", s.ctx).Return([]*db.PluginState{s.switcher(tt.caps...)}, nil).Once()
			if tt.delivered {
				s.runtime.On("Ensure", s.ctx, mock.Anything, ch).Return(ep, nil).Once()
				s.caller.On("Event", s.ctx, ep, tt.evt).Return(nil).Once()
			}
			require.NoError(s.T(), s.svc.Deliver(s.ctx, ch, nil, tt.evt))
		})
	}
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
