package main

import (
	"context"
	"errors"
	"os"
	"syscall"
	"time"

	containertypes "github.com/docker/docker/api/types/container"
	dockerclient "github.com/docker/docker/client"
	"github.com/stretchr/testify/require"
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
