package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/docker/docker/api/types"
	containertypes "github.com/docker/docker/api/types/container"
	dockerclient "github.com/docker/docker/client"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/plugin/runtime"
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
	enabled := pluginEnabled(cfg, reload)
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
