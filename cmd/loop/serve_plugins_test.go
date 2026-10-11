package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/docker/docker/api/types"
	containertypes "github.com/docker/docker/api/types/container"
	dockerclient "github.com/docker/docker/client"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/plugin/install"
	"github.com/radutopala/loop/internal/plugin/proxy"
	"github.com/radutopala/loop/internal/plugin/runtime"
	"github.com/radutopala/loop/internal/testutil"
)

// listingDocker is a Docker client whose container list succeeds, with no
// plugin containers; every other call goes to a daemon that isn't there.
type listingDocker struct {
	*dockerclient.Client
}

func (listingDocker) ContainerList(context.Context, containertypes.ListOptions) ([]containertypes.Summary, error) {
	return nil, nil
}

func noDocker() (*dockerclient.Client, error) {
	return dockerclient.NewClientWithOpts(dockerclient.WithHost("unix:///nonexistent/docker.sock"))
}

func (s *MainSuite) TestServeWithPlugins() {
	tests := []struct {
		name   string
		docker func() (pluginDockerClient, error)
	}{
		{"no leftovers", func() (pluginDockerClient, error) {
			c, err := noDocker()
			return listingDocker{c}, err
		}},
		{"docker unreachable", func() (pluginDockerClient, error) { return noDocker() }},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			m := s.setupServeMocks()
			m.setupHappyBot()
			store := s.useRealStore(m)
			defer store.Close()
			m.cfg.Plugins.Enabled = true
			m.cfg.LoopDir = s.T().TempDir()
			s.app.newPluginDocker = tc.docker

			errCh := make(chan error, 1)
			go func() { errCh <- s.app.serve() }()

			s.waitForServeReady(errCh)
			p, err := os.FindProcess(os.Getpid())
			require.NoError(s.T(), err)
			require.NoError(s.T(), p.Signal(syscall.SIGINT))

			select {
			case err := <-errCh:
				require.NoError(s.T(), err)
			case <-time.After(10 * time.Second):
				s.T().Fatal("serve() did not return in time")
			}
		})
	}
}

func (s *MainSuite) TestServePluginsErrors() {
	s.Run("mock store", func() {
		s.SetupTest()
		m := s.setupServeMocks()
		m.setupHappyBot()
		m.cfg.Plugins.Enabled = true
		require.EqualError(s.T(), s.app.serve(), "plugins need the SQLite store")
	})
	s.Run("docker client", func() {
		s.SetupTest()
		m := s.setupServeMocks()
		m.setupHappyBot()
		store := s.useRealStore(m)
		defer store.Close()
		m.cfg.Plugins.Enabled = true
		s.app.newPluginDocker = func() (pluginDockerClient, error) { return nil, errors.New("no docker") }
		require.EqualError(s.T(), s.app.serve(), "creating the plugin docker client: no docker")
	})
}

func (s *MainSuite) TestDefaultNewPluginDocker() {
	c, err := newApp().newPluginDocker()
	require.NoError(s.T(), err)
	require.NoError(s.T(), c.Close())
}

// versionlessDocker is a Docker client whose server version can't be read.
type versionlessDocker struct {
	*dockerclient.Client
}

func (versionlessDocker) ServerVersion(context.Context) (types.Version, error) {
	return types.Version{}, errors.New("no daemon")
}

func (s *MainSuite) TestPluginGate() {
	cfg := &config.Config{LoopDir: s.T().TempDir()}
	c, err := noDocker()
	require.NoError(s.T(), err)
	cli := versionlessDocker{c}
	require.Nil(s.T(), s.app.pluginGate(cfg, cli), "no gate while agentgate is off")

	cfg.Gates.Agentgate.Enabled = true
	cfg.Gates.Audit.RetentionDays = 7
	cfg.Gates.Audit.Verbose = true
	bin := filepath.Join(cfg.LoopDir, "loop-linux")
	require.NoError(s.T(), os.WriteFile(bin, []byte("ELF"), 0o755))
	cfg.Plugins.GateBinary = bin
	gate := s.app.pluginGate(cfg, cli)
	require.Equal(s.T(), 7, gate.AuditRetentionDays)
	require.True(s.T(), gate.AuditVerbose)
	path, err := gate.Binary(context.Background())
	require.NoError(s.T(), err)
	require.Equal(s.T(), bin, path)

	// A release build downloads its release for the Docker daemon's arch.
	cfg.Plugins.GateBinary = ""
	s.app.version = "2026.10.3"
	_, err = s.app.pluginGate(cfg, cli).Binary(context.Background())
	require.ErrorContains(s.T(), err, "architecture: no daemon")
}

func (s *MainSuite) TestPluginEnabled() {
	on, off := true, false
	cfg := &config.Config{LoopDir: s.T().TempDir()}
	global := &config.Config{Plugins: config.PluginsConfig{ByID: map[string]config.PluginConfig{"echo": {Enabled: &on}, "quiz": {Enabled: &off}}}}
	reload := func() (*config.Config, error) { return global, nil }
	store := new(testutil.MockStore)
	store.On("GetChannel", mock.Anything, mock.Anything).Return(nil, nil)
	enabled := pluginEnabled(cfg, reload, store)
	ctx := context.Background()

	// A channel without a dir reads the project config the API keeps for it.
	got, err := enabled(ctx, "echo", runtime.Channel{ID: "ch1"})
	require.NoError(s.T(), err)
	require.True(s.T(), got)
	got, err = enabled(ctx, "quiz", runtime.Channel{ID: "ch1"})
	require.NoError(s.T(), err)
	require.False(s.T(), got)

	// A broken project config is an error.
	dir := filepath.Join(cfg.LoopDir, "ch1", "work", ".loop")
	require.NoError(s.T(), os.MkdirAll(dir, 0o755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, "config.json"), []byte("{"), 0o644))
	_, err = enabled(ctx, "echo", runtime.Channel{ID: "ch1"})
	require.ErrorContains(s.T(), err, "parsing project config file")

	// A project with a config that doesn't touch plugins keeps the global value.
	proj := s.T().TempDir()
	require.NoError(s.T(), os.MkdirAll(filepath.Join(proj, ".loop"), 0o755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(proj, ".loop", "config.json"), []byte(`{"claude_model": "opus"}`), 0o644))
	got, err = enabled(ctx, "echo", runtime.Channel{ID: "ch2", DirPath: proj})
	require.NoError(s.T(), err)
	require.True(s.T(), got)
}

func (s *MainSuite) TestPluginEnabledWorktree() {
	on := true
	cfg := &config.Config{LoopDir: s.T().TempDir()}
	global := &config.Config{Plugins: config.PluginsConfig{ByID: map[string]config.PluginConfig{"echo": {Enabled: &on}}}}
	reload := func() (*config.Config, error) { return global, nil }
	proj, wt := s.T().TempDir(), s.T().TempDir()
	require.NoError(s.T(), os.MkdirAll(filepath.Join(proj, ".loop"), 0o755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(proj, ".loop", "config.json"), []byte(`{"claude_model": "opus"}`), 0o644))
	// The worktree's own config, which a read would fail on.
	require.NoError(s.T(), os.MkdirAll(filepath.Join(wt, ".loop"), 0o755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(wt, ".loop", "config.json"), []byte("{"), 0o644))
	store := new(testutil.MockStore)
	store.On("GetChannel", mock.Anything, "wt").Return(&db.Channel{ChannelID: "wt", DirPath: wt, ParentID: "proj", Worktree: true}, nil)
	store.On("GetChannel", mock.Anything, "proj").Return(&db.Channel{ChannelID: "proj", DirPath: proj}, nil)
	store.On("GetChannel", mock.Anything, "broken").Return(nil, errors.New("db down"))
	enabled := pluginEnabled(cfg, reload, store)
	ctx := context.Background()

	// A worktree thread reads its checkout's project config, not its own.
	got, err := enabled(ctx, "echo", runtime.Channel{ID: "wt", DirPath: wt})
	require.NoError(s.T(), err)
	require.True(s.T(), got)

	_, err = enabled(ctx, "echo", runtime.Channel{ID: "broken", DirPath: wt})
	require.ErrorContains(s.T(), err, "db down")
}

func (s *MainSuite) TestPluginActionRunner() {
	store, err := db.NewSQLiteStore(filepath.Join(s.T().TempDir(), "loop.db"))
	require.NoError(s.T(), err)
	defer store.Close()
	run := pluginActionRunner(install.NewService(nil, nil, store, nil, nil))
	err = run(context.Background(), "quiz", "daily", "ch1", "/src", 7)
	require.ErrorIs(s.T(), err, install.ErrNotInstalled)
}

type mockWorkflowPluginService struct {
	mock.Mock
}

func (m *mockWorkflowPluginService) RunAction(ctx context.Context, pluginID, action string, ch runtime.Channel, req proxy.ActionRequest) error {
	return m.Called(ctx, pluginID, action, ch, req).Error(0)
}

func (m *mockWorkflowPluginService) WorkflowTool(ctx context.Context, pluginID, tool string, ch runtime.Channel, req proxy.ToolRequest) (*proxy.ToolResult, error) {
	args := m.Called(ctx, pluginID, tool, ch, req)
	res, _ := args.Get(0).(*proxy.ToolResult)
	return res, args.Error(1)
}

func (s *MainSuite) TestWorkflowPlugins() {
	ctx := context.Background()
	ch := runtime.Channel{ID: "ch1", DirPath: "/src"}
	req := proxy.ToolRequest{RunID: "workflow:r1:n1", Input: json.RawMessage(`{"a":1}`)}

	s.Run("action", func() {
		svc := new(mockWorkflowPluginService)
		svc.On("RunAction", ctx, "quiz", "daily", ch, proxy.ActionRequest{Trigger: proxy.TriggerWorkflow}).Return(errors.New("boom")).Once()
		err := workflowPlugins{svc: svc}.RunPluginAction(ctx, "quiz", "daily", "ch1", "/src")
		require.EqualError(s.T(), err, "boom")
		svc.AssertExpectations(s.T())
	})

	tests := []struct {
		name    string
		res     *proxy.ToolResult
		err     error
		content string
		isError bool
	}{
		{name: "result", res: &proxy.ToolResult{Content: "hi"}, content: "hi"},
		{name: "error result", res: &proxy.ToolResult{Content: "bad", IsError: true}, content: "bad", isError: true},
		{name: "call fails", err: errors.New("down")},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			svc := new(mockWorkflowPluginService)
			svc.On("WorkflowTool", ctx, "quiz", "ask", ch, req).Return(tt.res, tt.err).Once()
			content, isError, err := workflowPlugins{svc: svc}.CallPluginTool(ctx, "quiz", "ask", "ch1", "/src", req.RunID, req.Input)
			if tt.err != nil {
				require.ErrorIs(s.T(), err, tt.err)
			} else {
				require.NoError(s.T(), err)
			}
			require.Equal(s.T(), tt.content, content)
			require.Equal(s.T(), tt.isError, isError)
			svc.AssertExpectations(s.T())
		})
	}
}
