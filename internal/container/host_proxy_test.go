package container

import (
	"errors"
	"net/http"

	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/config"
)

func (s *RunnerSuite) TestHostProxyFunc() {
	tests := []struct {
		name      string
		proxies   config.ProxiesConfig
		reloadErr error
		env       map[string]string
		target    string
		want      string
	}{
		{
			name:   "no proxy anywhere",
			target: "https://discord.com/api",
		},
		{
			name:    "config proxy, host address kept as configured",
			proxies: config.ProxiesConfig{HTTPProxy: "http://127.0.0.1:3128", HTTPSProxy: "http://127.0.0.1:3129"},
			target:  "https://discord.com/api",
			want:    "http://127.0.0.1:3129",
		},
		{
			name:    "config wins over the environment",
			proxies: config.ProxiesConfig{HTTPProxy: "http://cfg:1"},
			env:     map[string]string{"HTTP_PROXY": "http://env:1"},
			target:  "http://example.com",
			want:    "http://cfg:1",
		},
		{
			name:   "environment fills an unset variable, either spelling",
			env:    map[string]string{"https_proxy": "http://env:2"},
			target: "https://slack.com",
			want:   "http://env:2",
		},
		{
			name:      "config that cannot be read falls back to the environment",
			proxies:   config.ProxiesConfig{HTTPSProxy: "http://cfg:2"},
			reloadErr: errors.New("bad config"),
			env:       map[string]string{"HTTPS_PROXY": "http://env:2"},
			target:    "https://slack.com",
			want:      "http://env:2",
		},
		{
			name:    "localhost is never proxied",
			proxies: config.ProxiesConfig{HTTPProxy: "http://cfg:1"},
			target:  "http://localhost:11434",
		},
		{
			name:    "docker bridge addresses are never proxied",
			proxies: config.ProxiesConfig{HTTPProxy: "http://cfg:1"},
			target:  "http://172.17.0.5:11434",
		},
		{
			name:    "config no_proxy adds to the environment's",
			proxies: config.ProxiesConfig{HTTPSProxy: "http://cfg:2", NoProxy: []string{"internal.example"}},
			env:     map[string]string{"no_proxy": "corp.example"},
			target:  "https://internal.example",
		},
		{
			name:    "environment no_proxy still applies alongside config",
			proxies: config.ProxiesConfig{HTTPSProxy: "http://cfg:2", NoProxy: []string{"internal.example"}},
			env:     map[string]string{"NO_PROXY": "corp.example"},
			target:  "https://corp.example",
		},
		{
			name:    "config no_proxy without an environment one",
			proxies: config.ProxiesConfig{HTTPSProxy: "http://cfg:2", NoProxy: []string{"internal.example"}},
			target:  "https://internal.example",
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			reload := func() (*config.Config, error) {
				if tt.reloadErr != nil {
					return nil, tt.reloadErr
				}
				return &config.Config{Proxies: tt.proxies}, nil
			}
			proxy := HostProxyFunc(reload, func(key string) string { return tt.env[key] })

			req, err := http.NewRequest(http.MethodGet, tt.target, nil)
			require.NoError(s.T(), err)
			got, err := proxy(req)
			require.NoError(s.T(), err)
			if tt.want == "" {
				require.Nil(s.T(), got)
				return
			}
			require.NotNil(s.T(), got)
			require.Equal(s.T(), tt.want, got.String())
		})
	}
}

func (s *RunnerSuite) TestHostProxyFuncRereadsConfig() {
	proxies := config.ProxiesConfig{HTTPSProxy: "http://cfg:2"}
	reload := func() (*config.Config, error) { return &config.Config{Proxies: proxies}, nil }
	proxy := HostProxyFunc(reload, func(string) string { return "" })
	req, err := http.NewRequest(http.MethodGet, "https://discord.com", nil)
	require.NoError(s.T(), err)

	got, err := proxy(req)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "http://cfg:2", got.String())

	// Switching the proxy off in config applies to the next connection.
	proxies = config.ProxiesConfig{}
	got, err = proxy(req)
	require.NoError(s.T(), err)
	require.Nil(s.T(), got)
}

func (s *RunnerSuite) TestHostProxyEnv() {
	const noProxy = "localhost,127.0.0.1,::1,172.16.0.0/12"
	tests := []struct {
		name      string
		proxies   config.ProxiesConfig
		reloadErr error
		env       map[string]string
		want      []string
	}{
		{
			name: "no proxy anywhere sets nothing",
		},
		{
			name:    "config proxies in both letter cases, localhost and bridge bypassed",
			proxies: config.ProxiesConfig{HTTPProxy: "http://127.0.0.1:3128", HTTPSProxy: "http://127.0.0.1:3129"},
			want: []string{
				"HTTP_PROXY=http://127.0.0.1:3128", "http_proxy=http://127.0.0.1:3128",
				"HTTPS_PROXY=http://127.0.0.1:3129", "https_proxy=http://127.0.0.1:3129",
				"NO_PROXY=" + noProxy, "no_proxy=" + noProxy,
			},
		},
		{
			name:    "environment fills an unset variable and no_proxy adds up",
			proxies: config.ProxiesConfig{HTTPSProxy: "http://cfg:2", NoProxy: []string{"internal.example"}},
			env:     map[string]string{"http_proxy": "http://env:1", "NO_PROXY": "corp.example"},
			want: []string{
				"HTTP_PROXY=http://env:1", "http_proxy=http://env:1",
				"HTTPS_PROXY=http://cfg:2", "https_proxy=http://cfg:2",
				"NO_PROXY=" + noProxy + ",corp.example,internal.example",
				"no_proxy=" + noProxy + ",corp.example,internal.example",
			},
		},
		{
			name:      "config that cannot be read falls back to the environment",
			proxies:   config.ProxiesConfig{HTTPSProxy: "http://cfg:2"},
			reloadErr: errors.New("bad config"),
			env:       map[string]string{"HTTPS_PROXY": "http://env:2"},
			want: []string{
				"HTTPS_PROXY=http://env:2", "https_proxy=http://env:2",
				"NO_PROXY=" + noProxy, "no_proxy=" + noProxy,
			},
		},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			reload := func() (*config.Config, error) {
				if tt.reloadErr != nil {
					return nil, tt.reloadErr
				}
				return &config.Config{Proxies: tt.proxies}, nil
			}
			got := HostProxyEnv(reload, func(key string) string { return tt.env[key] })()
			require.Equal(s.T(), tt.want, got)
		})
	}
}

func (s *RunnerSuite) TestHostProxyEnvRereadsConfig() {
	proxies := config.ProxiesConfig{HTTPSProxy: "http://cfg:2"}
	reload := func() (*config.Config, error) { return &config.Config{Proxies: proxies}, nil }
	proxyEnv := HostProxyEnv(reload, func(string) string { return "" })
	require.Contains(s.T(), proxyEnv(), "HTTPS_PROXY=http://cfg:2")

	// Switching the proxy off in config applies to the next subprocess.
	proxies = config.ProxiesConfig{}
	require.Nil(s.T(), proxyEnv())
}
