package container

import (
	"bytes"
	"context"
	"log/slog"
	"slices"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/config"
)

func (s *RunnerSuite) TestProxySettingsFromConfig() {
	tests := []struct {
		name string
		cfg  *config.Config
		want ProxySettings
	}{
		{
			// The browser package resolves settings for channels that may have
			// no config to resolve against; a nil there must not panic.
			name: "nil config",
			cfg:  nil,
			want: ProxySettings{},
		},
		{
			name: "values carried across",
			cfg: &config.Config{
				HTTPProxy:  "http://cfg:3128",
				HTTPSProxy: "http://cfg:3129",
				NoProxy:    []string{"*.internal"},
			},
			want: ProxySettings{
				HTTPProxy:  "http://cfg:3128",
				HTTPSProxy: "http://cfg:3129",
				NoProxy:    []string{"*.internal"},
			},
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			require.Equal(s.T(), tc.want, ProxySettingsFromConfig(tc.cfg))
		})
	}
}

func (s *RunnerSuite) TestProxySummary() {
	tests := []struct {
		name       string
		proxy      ProxySettings
		envs       map[string]string
		wantValue  string
		wantSource string
	}{
		{
			name:       "nothing configured anywhere",
			wantValue:  "",
			wantSource: "none",
		},
		{
			// A NO_PROXY on its own is not a proxy: containers still get none.
			name:       "no_proxy alone is not a proxy",
			envs:       map[string]string{"NO_PROXY": "localhost"},
			wantValue:  "",
			wantSource: "none",
		},
		{
			name:       "from the daemon environment",
			envs:       map[string]string{"HTTP_PROXY": "http://env:8080"},
			wantValue:  "http://env:8080",
			wantSource: "daemon environment",
		},
		{
			name:       "from config",
			proxy:      ProxySettings{HTTPProxy: "http://cfg:3128"},
			envs:       map[string]string{"HTTP_PROXY": "http://env:8080"},
			wantValue:  "http://cfg:3128",
			wantSource: "config",
		},
		{
			name:       "https-only config still reports a proxy",
			proxy:      ProxySettings{HTTPSProxy: "http://cfg:3128"},
			wantValue:  "http://cfg:3128",
			wantSource: "config",
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			getenv := func(key string) string { return tc.envs[key] }
			value, source := ProxySummary(tc.proxy, getenv)
			require.Equal(s.T(), tc.wantValue, value)
			require.Equal(s.T(), tc.wantSource, source)
		})
	}
}

// The regression this whole key exists for: the daemon's own environment is
// fixed at launch, so a daemon started before the proxy was exported used to
// create proxy-less containers until someone restarted it. Config is re-read
// per run, so it reaches the container regardless.
func (s *RunnerSuite) TestRunProxyFromConfigWhenDaemonHasNone() {
	s.cfg.HTTPProxy = "http://127.0.0.1:3128"
	s.cfg.HTTPSProxy = "http://127.0.0.1:3128"
	s.applyMockDefaults()

	ctx := context.Background()
	const want = "http://host.docker.internal:3128"
	s.setupMockRun(ctx, mock.MatchedBy(func(cfg *ContainerConfig) bool {
		return slices.Contains(cfg.Env, "HTTP_PROXY="+want) &&
			slices.Contains(cfg.Env, "http_proxy="+want) &&
			slices.Contains(cfg.Env, "HTTPS_PROXY="+want) &&
			slices.Contains(cfg.Env, "https_proxy="+want)
	}), testContainerName, testJSONOK)
	s.client.On("CopyToContainer", ctx, testContainerID, "/", mock.Anything).Return(nil)

	resp, err := s.runner.Run(ctx, &agent.AgentRequest{
		Messages:  []agent.AgentMessage{{Role: "user", Content: "hello"}},
		ChannelID: "ch-1",
	})
	require.NoError(s.T(), err)
	require.Equal(s.T(), "ok", resp.Response)
	s.client.AssertExpectations(s.T())
}

// A container created without a proxy cannot be repaired afterwards — its env
// is fixed at create. Saying so in the log is the difference between a
// five-minute fix and a failing request an hour later.
func (s *RunnerSuite) TestWarnProxyMissing() {
	tests := []struct {
		name     string
		cfg      *config.Config
		proxyEnv []string
		logger   bool
		wantWarn bool
	}{
		{
			name:     "no proxy but no_proxy set",
			cfg:      &config.Config{NoProxy: []string{"my-service"}},
			logger:   true,
			wantWarn: true,
		},
		{
			name:     "proxy present",
			cfg:      &config.Config{NoProxy: []string{"my-service"}},
			proxyEnv: []string{"HTTP_PROXY=http://proxy:8080"},
			logger:   true,
		},
		{
			// Nothing proxy-shaped in the config: this machine is simply not
			// behind a proxy, and a warning would be noise on every run.
			name:   "no proxy and no no_proxy entries",
			cfg:    &config.Config{},
			logger: true,
		},
		{
			name: "nil config",
			cfg:  nil, logger: true,
		},
		{
			name: "no logger",
			cfg:  &config.Config{NoProxy: []string{"my-service"}},
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			var buf bytes.Buffer
			s.runner.logger = nil
			if tc.logger {
				s.runner.SetLogger(slog.New(slog.NewTextHandler(&buf, nil)))
			}
			s.runner.warnProxyMissing(tc.cfg, tc.proxyEnv)
			if tc.wantWarn {
				require.Contains(s.T(), buf.String(), "creating container with no proxy")
				return
			}
			require.Empty(s.T(), buf.String())
		})
	}
}
