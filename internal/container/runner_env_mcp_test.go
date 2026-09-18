package container

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/config"
)

func (s *RunnerSuite) TestAddAuthEnv() {
	tests := []struct {
		name       string
		oauthToken string
		apiKey     string
		want       []string
	}{
		{
			name:       "OAuth token set",
			oauthToken: "oauth-tok",
			want:       []string{"BASE=1", "CLAUDE_CODE_OAUTH_TOKEN=oauth-tok"},
		},
		{
			name:   "API key set",
			apiKey: "api-key",
			want:   []string{"BASE=1", "ANTHROPIC_API_KEY=api-key"},
		},
		{
			name:       "OAuth takes precedence",
			oauthToken: "oauth-tok",
			apiKey:     "api-key",
			want:       []string{"BASE=1", "CLAUDE_CODE_OAUTH_TOKEN=oauth-tok"},
		},
		{
			name: "neither set",
			want: []string{"BASE=1"},
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			cfg := &config.Config{
				ClaudeCodeOAuthToken: tc.oauthToken,
				AnthropicAPIKey:      tc.apiKey,
			}
			result := addAuthEnv([]string{"BASE=1"}, cfg)
			require.Equal(s.T(), tc.want, result)
		})
	}
}

func (s *RunnerSuite) TestProxyEnv() {
	const noProxy = "host.docker.internal,localhost,127.0.0.1,::1,172.16.0.0/12"
	tests := []struct {
		name       string
		proxy      ProxySettings
		envs       map[string]string
		extraHosts []string
		want       []string
	}{
		{
			name: "no proxy vars",
			envs: map[string]string{},
			want: nil,
		},
		{
			name: "HTTP_PROXY forwarded in both spellings with NO_PROXY added",
			envs: map[string]string{"HTTP_PROXY": "http://proxy:8080"},
			want: []string{
				"HTTP_PROXY=http://proxy:8080", "http_proxy=http://proxy:8080",
				"NO_PROXY=" + noProxy, "no_proxy=" + noProxy,
			},
		},
		{
			name: "lower-case daemon spelling answers for both",
			envs: map[string]string{"http_proxy": "http://proxy:8080"},
			want: []string{
				"HTTP_PROXY=http://proxy:8080", "http_proxy=http://proxy:8080",
				"NO_PROXY=" + noProxy, "no_proxy=" + noProxy,
			},
		},
		{
			name: "localhost rewritten to docker host",
			envs: map[string]string{"HTTP_PROXY": "http://localhost:3128"},
			want: []string{
				"HTTP_PROXY=http://host.docker.internal:3128", "http_proxy=http://host.docker.internal:3128",
				"NO_PROXY=" + noProxy, "no_proxy=" + noProxy,
			},
		},
		{
			// The whole point of the config layer: a daemon launched before the
			// proxy was exported still creates containers that can reach it.
			name:  "config proxy used when the daemon has none",
			proxy: ProxySettings{HTTPProxy: "http://127.0.0.1:3128"},
			envs:  map[string]string{},
			want: []string{
				"HTTP_PROXY=http://host.docker.internal:3128", "http_proxy=http://host.docker.internal:3128",
				"NO_PROXY=" + noProxy, "no_proxy=" + noProxy,
			},
		},
		{
			name:  "config wins over the daemon environment",
			proxy: ProxySettings{HTTPProxy: "http://cfg:3128", HTTPSProxy: "http://cfg:3128"},
			envs:  map[string]string{"HTTP_PROXY": "http://env:8080", "HTTPS_PROXY": "http://env:8080"},
			want: []string{
				"HTTP_PROXY=http://cfg:3128", "http_proxy=http://cfg:3128",
				"HTTPS_PROXY=http://cfg:3128", "https_proxy=http://cfg:3128",
				"NO_PROXY=" + noProxy, "no_proxy=" + noProxy,
			},
		},
		{
			// Per variable, not all-or-nothing: config naming only the HTTPS
			// proxy leaves the daemon's HTTP one in place.
			name:  "config and environment merge per variable",
			proxy: ProxySettings{HTTPSProxy: "http://cfg:3128"},
			envs:  map[string]string{"HTTP_PROXY": "http://env:8080"},
			want: []string{
				"HTTP_PROXY=http://env:8080", "http_proxy=http://env:8080",
				"HTTPS_PROXY=http://cfg:3128", "https_proxy=http://cfg:3128",
				"NO_PROXY=" + noProxy, "no_proxy=" + noProxy,
			},
		},
		{
			// Additive, not a replacement: configured entries land after
			// loop's own bypasses and after whatever the daemon carried.
			name:  "configured no_proxy adds to loop's own bypasses",
			proxy: ProxySettings{HTTPProxy: "http://cfg:3128", NoProxy: []string{"*.internal"}},
			envs:  map[string]string{},
			want: []string{
				"HTTP_PROXY=http://cfg:3128", "http_proxy=http://cfg:3128",
				"NO_PROXY=" + noProxy + ",*.internal", "no_proxy=" + noProxy + ",*.internal",
			},
		},
		{
			name:  "configured no_proxy keeps the daemon's own list",
			proxy: ProxySettings{NoProxy: []string{"my-service"}},
			envs:  map[string]string{"HTTP_PROXY": "http://env:8080", "NO_PROXY": "*.corp"},
			want: []string{
				"HTTP_PROXY=http://env:8080", "http_proxy=http://env:8080",
				"NO_PROXY=*.corp," + noProxy + ",my-service", "no_proxy=*.corp," + noProxy + ",my-service",
			},
		},
		{
			// The sidecar hostname is passed by the caller and the project's
			// own entries ride in the settings; both end up in the list.
			name:       "extra hosts and configured entries combine",
			proxy:      ProxySettings{HTTPProxy: "http://cfg:3128", NoProxy: []string{"my-service"}},
			extraHosts: []string{"loop-chrome-ch1"},
			want: []string{
				"HTTP_PROXY=http://cfg:3128", "http_proxy=http://cfg:3128",
				"NO_PROXY=" + noProxy + ",loop-chrome-ch1,my-service",
				"no_proxy=" + noProxy + ",loop-chrome-ch1,my-service",
			},
		},
		{
			name:       "extra hosts bypass the proxy too",
			envs:       map[string]string{"HTTP_PROXY": "http://proxy:8080"},
			extraHosts: []string{"loop-chrome-ch1"},
			want: []string{
				"HTTP_PROXY=http://proxy:8080", "http_proxy=http://proxy:8080",
				"NO_PROXY=" + noProxy + ",loop-chrome-ch1", "no_proxy=" + noProxy + ",loop-chrome-ch1",
			},
		},
		{
			name:       "extra hosts ignored without a proxy",
			envs:       map[string]string{"NO_PROXY": "localhost"},
			extraHosts: []string{"loop-chrome-ch1"},
			want:       []string{"NO_PROXY=localhost", "no_proxy=localhost"},
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			getenv := func(key string) string { return tc.envs[key] }
			require.Equal(s.T(), tc.want, ProxyEnv(tc.proxy, getenv, tc.extraHosts...))
		})
	}
}

func (s *RunnerSuite) TestChromeHostname() {
	require.Equal(s.T(), "loop-chrome-ch-1", ChromeHostname("ch-1"))
}

func (s *RunnerSuite) TestLocalhostToDockerHost() {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"bare port", ":3128", "http://host.docker.internal:3128"},
		{"localhost with port", "http://localhost:3128", "http://host.docker.internal:3128"},
		{"127.0.0.1 with port", "http://127.0.0.1:3128", "http://host.docker.internal:3128"},
		{"https localhost", "https://localhost:3128", "https://host.docker.internal:3128"},
		{"localhost no port", "http://localhost", "http://host.docker.internal"},
		{"127.0.0.1 no port", "http://127.0.0.1", "http://host.docker.internal"},
		{"localhost with path", "http://localhost/proxy", "http://host.docker.internal/proxy"},
		{"remote proxy unchanged", "http://proxy.corp:8080", "http://proxy.corp:8080"},
		{"empty string", "", ""},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			require.Equal(s.T(), tc.want, localhostToDockerHost(tc.input))
		})
	}
}

func (s *RunnerSuite) TestAgentAPIBase() {
	// Default: host.docker.internal + APIAddr (daemon on the Docker host).
	require.Equal(s.T(), "http://host.docker.internal:8222",
		agentAPIBase(&config.Config{APIAddr: ":8222"}))
	// Override: used when the daemon itself runs in a container.
	require.Equal(s.T(), "http://172.17.0.2:8222",
		agentAPIBase(&config.Config{APIAddr: ":8222", APIAdvertiseURL: "http://172.17.0.2:8222"}))
}

func (s *RunnerSuite) TestEnsureNoProxy() {
	tests := []struct {
		name       string
		env        []string
		extraHosts []string
		want       []string
	}{
		{
			"appends to existing NO_PROXY",
			[]string{"HTTP_PROXY=http://proxy:8080", "NO_PROXY=localhost,127.0.0.1"},
			nil,
			[]string{"HTTP_PROXY=http://proxy:8080", "NO_PROXY=localhost,127.0.0.1,host.docker.internal,::1,172.16.0.0/12"},
		},
		{
			"appends to existing no_proxy",
			[]string{"http_proxy=http://proxy:8080", "no_proxy=localhost"},
			nil,
			[]string{"http_proxy=http://proxy:8080", "no_proxy=localhost,host.docker.internal,127.0.0.1,::1,172.16.0.0/12"},
		},
		{
			"adds both NO_PROXY and no_proxy when missing",
			[]string{"HTTP_PROXY=http://proxy:8080"},
			nil,
			[]string{"HTTP_PROXY=http://proxy:8080", "NO_PROXY=host.docker.internal,localhost,127.0.0.1,::1,172.16.0.0/12", "no_proxy=host.docker.internal,localhost,127.0.0.1,::1,172.16.0.0/12"},
		},
		{
			"no-op when already present",
			[]string{"NO_PROXY=host.docker.internal,localhost,127.0.0.1,::1,172.16.0.0/12,other"},
			nil,
			[]string{"NO_PROXY=host.docker.internal,localhost,127.0.0.1,::1,172.16.0.0/12,other"},
		},
		{
			"empty NO_PROXY value",
			[]string{"NO_PROXY="},
			nil,
			[]string{"NO_PROXY=host.docker.internal,localhost,127.0.0.1,::1,172.16.0.0/12"},
		},
		{
			"extra hosts added to NO_PROXY",
			[]string{"HTTP_PROXY=http://proxy:8080", "NO_PROXY=localhost"},
			[]string{"loop-chrome-ch1"},
			[]string{"HTTP_PROXY=http://proxy:8080", "NO_PROXY=localhost,host.docker.internal,127.0.0.1,::1,172.16.0.0/12,loop-chrome-ch1"},
		},
		{
			"extra hosts added when NO_PROXY missing",
			[]string{"HTTP_PROXY=http://proxy:8080"},
			[]string{"loop-chrome-ch1"},
			[]string{"HTTP_PROXY=http://proxy:8080", "NO_PROXY=host.docker.internal,localhost,127.0.0.1,::1,172.16.0.0/12,loop-chrome-ch1", "no_proxy=host.docker.internal,localhost,127.0.0.1,::1,172.16.0.0/12,loop-chrome-ch1"},
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			result := ensureNoProxy(tc.env, tc.extraHosts...)
			require.Equal(s.T(), tc.want, result)
		})
	}
}

func (s *RunnerSuite) TestBuildMCPConfig() {
	cfg := buildMCPConfig("ch-1", "http://host.docker.internal:8222", "/home/user/project", "", "", false, true, nil)
	require.Len(s.T(), cfg.MCPServers, 2)
	ls := cfg.MCPServers["loop"]
	require.Equal(s.T(), "/usr/local/bin/loop", ls.Command)
	require.Equal(s.T(), []string{"mcp", "--channel-id", "ch-1", "--api-url", "http://host.docker.internal:8222", "--log", "/home/user/project/.loop/mcp.log"}, ls.Args)
	require.Nil(s.T(), ls.Env)

	bs := cfg.MCPServers["loop-browser"]
	require.Equal(s.T(), "/usr/local/bin/loop", bs.Command)
	require.Equal(s.T(), []string{"mcp-browser", "--log", "/home/user/project/.loop/mcp-browser.log", "--api-url", "http://host.docker.internal:8222", "--channel-id", "ch-1"}, bs.Args)
}

func (s *RunnerSuite) TestBuildMCPConfigBrowserDisabled() {
	cfg := buildMCPConfig("ch-1", "http://host.docker.internal:8222", "/home/user/project", "", "", false, false, nil)
	require.Len(s.T(), cfg.MCPServers, 1)
	_, hasBrowser := cfg.MCPServers["loop-browser"]
	require.False(s.T(), hasBrowser)
	_, hasLoop := cfg.MCPServers["loop"]
	require.True(s.T(), hasLoop)
}

func (s *RunnerSuite) TestBuildMCPConfigWithAuthorID() {
	cfg := buildMCPConfig("ch-1", "http://host.docker.internal:8222", "/home/user/project", "user-42", "", false, true, nil)
	require.Len(s.T(), cfg.MCPServers, 2)
	ls := cfg.MCPServers["loop"]
	require.Equal(s.T(), "/usr/local/bin/loop", ls.Command)
	require.Equal(s.T(), []string{"mcp", "--channel-id", "ch-1", "--api-url", "http://host.docker.internal:8222", "--log", "/home/user/project/.loop/mcp.log", "--author-id", "user-42"}, ls.Args)
}

func (s *RunnerSuite) TestBuildMCPConfigWithUserServers() {
	userServers := map[string]config.MCPServerConfig{
		"custom-tool": {
			Command: "/path/to/binary",
			Args:    []string{"--flag"},
			Env:     map[string]string{"API_KEY": "secret"},
		},
	}
	cfg := buildMCPConfig("ch-1", "http://host.docker.internal:8222", "/home/user/project", "", "", false, true, userServers)
	require.Len(s.T(), cfg.MCPServers, 3)

	custom := cfg.MCPServers["custom-tool"]
	require.Equal(s.T(), "/path/to/binary", custom.Command)
	require.Equal(s.T(), []string{"--flag"}, custom.Args)
	require.Equal(s.T(), map[string]string{"API_KEY": "secret"}, custom.Env)

	ls := cfg.MCPServers["loop"]
	require.Equal(s.T(), "/usr/local/bin/loop", ls.Command)
}

func (s *RunnerSuite) TestBuildMCPConfigUserLoopPreserved() {
	userServers := map[string]config.MCPServerConfig{
		"loop": {
			Command: "/user/custom/loop",
			Args:    []string{"--custom-flag"},
		},
	}
	cfg := buildMCPConfig("ch-1", "http://host.docker.internal:8222", "/home/user/project", "", "", false, true, userServers)
	require.Len(s.T(), cfg.MCPServers, 2)
	ls := cfg.MCPServers["loop"]
	require.Equal(s.T(), "/user/custom/loop", ls.Command)
	require.Equal(s.T(), []string{"--custom-flag"}, ls.Args)
}

func (s *RunnerSuite) TestBuildMCPConfigUserBrowserPreserved() {
	userServers := map[string]config.MCPServerConfig{
		"loop-browser": {
			Command: "/user/custom/browser",
			Args:    []string{"--port", "9999"},
		},
	}
	cfg := buildMCPConfig("ch-1", "http://host.docker.internal:8222", "/home/user/project", "", "", false, true, userServers)
	require.Len(s.T(), cfg.MCPServers, 2)
	bs := cfg.MCPServers["loop-browser"]
	require.Equal(s.T(), "/user/custom/browser", bs.Command)
	require.Equal(s.T(), []string{"--port", "9999"}, bs.Args)
}

func (s *RunnerSuite) TestBuildMCPConfigWithMemory() {
	cfg := buildMCPConfig("ch-1", "http://host.docker.internal:8222", "/home/user/project", "", "", true, true, nil)
	require.Len(s.T(), cfg.MCPServers, 2)
	ls := cfg.MCPServers["loop"]
	require.Contains(s.T(), ls.Args, "--memory")
}

func (s *RunnerSuite) TestBuildMCPConfigWithAgentID() {
	cfg := buildMCPConfig("ch-1", "http://host.docker.internal:8222", "/home/user/project", "", "agent-0", false, true, nil)
	require.Len(s.T(), cfg.MCPServers, 2)
	ls := cfg.MCPServers["loop"]
	require.Contains(s.T(), ls.Args, "--agent-id")
	require.Contains(s.T(), ls.Args, "agent-0")
}

func (s *RunnerSuite) TestRunBrowserDisabledNoNetwork() {
	s.cfg.Browser.Enabled = false
	s.runner = NewDockerRunner(s.client, s.cfg, nil)
	s.applyMockDefaults()
	ctx := context.Background()

	s.setupMockRun(ctx, mock.MatchedBy(func(cfg *ContainerConfig) bool {
		return cfg.NetworkName == "" && cfg.Hostname == ""
	}), testContainerName, testJSONOK)

	resp, err := s.runner.Run(ctx, &agent.AgentRequest{
		ChannelID: "ch-1",
		Messages:  []agent.AgentMessage{{Role: "user", Content: "hi"}},
	})
	require.NoError(s.T(), err)
	require.Equal(s.T(), "ok", resp.Response)
}

func (s *RunnerSuite) TestRunBrowserEnabledNoNetwork() {
	// Even with browser enabled, the agent container no longer joins a Docker
	// network — the mcp-browser server proxies actions through the host API instead.
	s.cfg.Browser.Enabled = true
	s.runner = NewDockerRunner(s.client, s.cfg, nil)
	s.applyMockDefaults()
	ctx := context.Background()

	s.setupMockRun(ctx, mock.MatchedBy(func(cfg *ContainerConfig) bool {
		return cfg.NetworkName == "" && cfg.Hostname == ""
	}), testContainerName, testJSONOK)

	resp, err := s.runner.Run(ctx, &agent.AgentRequest{
		ChannelID: "ch-1",
		Messages:  []agent.AgentMessage{{Role: "user", Content: "hi"}},
	})
	require.NoError(s.T(), err)
	require.Equal(s.T(), "ok", resp.Response)
}

func (s *RunnerSuite) TestRunWithScreenshotDirBind() {
	// Screenshot directory is always bind-mounted read-only.
	ctx := context.Background()

	screenshotDir := filepath.Join(s.cfg.LoopDir, "screenshots")

	s.setupMockRun(ctx, mock.MatchedBy(func(cfg *ContainerConfig) bool {
		hasScreenshotBind := slices.Contains(cfg.Binds, screenshotDir+":"+screenshotDir+":ro")
		return hasScreenshotBind
	}), testContainerName, testJSONOK)

	resp, err := s.runner.Run(ctx, &agent.AgentRequest{
		ChannelID: "ch-1",
		Messages:  []agent.AgentMessage{{Role: "user", Content: "hi"}},
	})
	require.NoError(s.T(), err)
	require.Equal(s.T(), "ok", resp.Response)
}

func (s *RunnerSuite) TestRunWithDirPath() {
	ctx := context.Background()
	req := &agent.AgentRequest{
		Messages:  []agent.AgentMessage{{Role: "user", Content: "hello"}},
		ChannelID: "ch-1",
		DirPath:   "/home/user/project",
	}

	s.setupMockRun(ctx, mock.MatchedBy(func(cfg *ContainerConfig) bool {
		return slices.Contains(cfg.Binds, "/home/user/project:/home/user/project")
	}), "loop-project-aabbcc", testJSONOK)

	resp, err := s.runner.Run(ctx, req)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "ok", resp.Response)

	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunMCPConfigWriteError() {
	s.sys.Override("WriteFile", mock.Anything, mock.Anything, mock.Anything).Return(errors.New("write failed"))

	ctx := context.Background()
	req := &agent.AgentRequest{ChannelID: "ch-1"}

	resp, err := s.runner.Run(ctx, req)
	require.Nil(s.T(), resp)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "writing mcp config")
}

func (s *RunnerSuite) TestRunMCPConfigWritten() {
	var writtenPath string
	var writtenData []byte
	s.sys.Override("WriteFile", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			writtenPath = args.String(0)
			writtenData = args.Get(1).([]byte)
		}).Return(nil)

	ctx := context.Background()
	req := &agent.AgentRequest{
		Messages:  []agent.AgentMessage{{Role: "user", Content: "hello"}},
		ChannelID: "ch-1",
	}

	s.setupMockRun(ctx, mock.AnythingOfType("*container.ContainerConfig"), testContainerName, testJSONOK)

	_, err := s.runner.Run(ctx, req)
	require.NoError(s.T(), err)

	require.Equal(s.T(), "/home/testuser/.loop/ch-1/work/.loop/mcp-ch-1.json", writtenPath)

	var cfg mcpConfig
	require.NoError(s.T(), json.Unmarshal(writtenData, &cfg))
	require.Contains(s.T(), cfg.MCPServers, "loop")
	ls := cfg.MCPServers["loop"]
	require.Equal(s.T(), "/usr/local/bin/loop", ls.Command)
	require.Equal(s.T(), []string{"mcp", "--channel-id", "ch-1", "--api-url", "http://host.docker.internal:8222", "--log", "/home/testuser/.loop/ch-1/work/.loop/mcp.log"}, ls.Args)

	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunMCPConfigRemovedAfterRun() {
	var removedPath string
	s.sys.Override("Remove", mock.Anything).
		Run(func(args mock.Arguments) {
			removedPath = args.String(0)
		}).Return(nil)

	ctx := context.Background()
	req := &agent.AgentRequest{
		Messages:  []agent.AgentMessage{{Role: "user", Content: "hello"}},
		ChannelID: "ch-1",
	}

	s.setupMockRun(ctx, mock.AnythingOfType("*container.ContainerConfig"), testContainerName, testJSONOK)

	_, err := s.runner.Run(ctx, req)
	require.NoError(s.T(), err)

	require.Equal(s.T(), "/home/testuser/.loop/ch-1/work/.loop/mcp-ch-1.json", removedPath)
	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunKeepMCPConfigsSkipsRemoval() {
	s.cfg.KeepMCPConfigs = true

	ctx := context.Background()
	req := &agent.AgentRequest{
		Messages:  []agent.AgentMessage{{Role: "user", Content: "hello"}},
		ChannelID: "ch-1",
	}

	s.setupMockRun(ctx, mock.AnythingOfType("*container.ContainerConfig"), testContainerName, testJSONOK)

	_, err := s.runner.Run(ctx, req)
	require.NoError(s.T(), err)

	// Remove should NOT have been called — KeepMCPConfigs is true.
	s.sys.AssertNotCalled(s.T(), "Remove", mock.Anything)
	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunMCPConfigIncludesAgentID() {
	var writtenPath string
	var writtenData []byte
	s.sys.Override("WriteFile", mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			writtenPath = args.String(0)
			writtenData = args.Get(1).([]byte)
		}).Return(nil)

	ctx := context.Background()
	req := &agent.AgentRequest{
		Messages:  []agent.AgentMessage{{Role: "user", Content: "hello"}},
		ChannelID: "ch-1",
		AgentID:   "chat",
	}

	s.setupMockRun(ctx, mock.AnythingOfType("*container.ContainerConfig"), testContainerName, testJSONOK)

	_, err := s.runner.Run(ctx, req)
	require.NoError(s.T(), err)

	// Per-agent MCP config path includes the agent ID.
	require.Equal(s.T(), "/home/testuser/.loop/ch-1/work/.loop/mcp-ch-1-chat.json", writtenPath)

	var cfg mcpConfig
	require.NoError(s.T(), json.Unmarshal(writtenData, &cfg))
	require.Contains(s.T(), cfg.MCPServers, "loop")
	ls := cfg.MCPServers["loop"]
	require.Contains(s.T(), ls.Args, "--agent-id")
	require.Contains(s.T(), ls.Args, "chat")

	s.client.AssertExpectations(s.T())
}

func (s *RunnerSuite) TestRunAgentIDAddsChannelFlag() {
	ctx := context.Background()
	s.cfg.ClaudeDangerouslyLoadDevelopmentChannels = true
	req := &agent.AgentRequest{
		Messages:  []agent.AgentMessage{{Role: "user", Content: "hello"}},
		ChannelID: "ch-1",
		AgentID:   "chat",
	}

	s.setupMockRun(ctx, mock.MatchedBy(func(cfg *ContainerConfig) bool {
		return slices.Contains(cfg.Cmd, "--dangerously-load-development-channels")
	}), testContainerName, testJSONOK)

	_, err := s.runner.Run(ctx, req)
	require.NoError(s.T(), err)
	s.client.AssertExpectations(s.T())
}

// TestRunAgentIDOmitsChannelFlagByDefault confirms that the development-channels
// flag is gated behind the config opt-in even when an agent ID is set.
func (s *RunnerSuite) TestRunAgentIDOmitsChannelFlagByDefault() {
	ctx := context.Background()
	req := &agent.AgentRequest{
		Messages:  []agent.AgentMessage{{Role: "user", Content: "hello"}},
		ChannelID: "ch-1",
		AgentID:   "chat",
	}

	s.setupMockRun(ctx, mock.MatchedBy(func(cfg *ContainerConfig) bool {
		return !slices.Contains(cfg.Cmd, "--dangerously-load-development-channels")
	}), testContainerName, testJSONOK)

	_, err := s.runner.Run(ctx, req)
	require.NoError(s.T(), err)
	s.client.AssertExpectations(s.T())
}
