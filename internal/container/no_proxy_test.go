package container

import (
	"context"
	"encoding/json"
	"net/url"
	"slices"
	"strings"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/http/httpproxy"

	"github.com/radutopala/loop/internal/agent"
)

// noProxyFor reports the proxy Go would pick for rawURL under the NO_PROXY the
// container env carries. It uses the same matcher net/http does, so this pins
// real behaviour rather than our reading of it.
func noProxyFor(env []string, rawURL string) string {
	cfg := &httpproxy.Config{}
	for _, e := range env {
		k, v, ok := strings.Cut(e, "=")
		if !ok {
			continue
		}
		switch strings.ToUpper(k) {
		case "HTTP_PROXY":
			cfg.HTTPProxy = v
		case "HTTPS_PROXY":
			cfg.HTTPSProxy = v
		case "NO_PROXY":
			cfg.NoProxy = v
		}
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return err.Error()
	}
	proxyURL, err := cfg.ProxyFunc()(u)
	if err != nil {
		return err.Error()
	}
	if proxyURL == nil {
		return ""
	}
	return proxyURL.String()
}

// A sibling container is dialled by bare name, and dockerBridgeCIDR does not
// cover that: the matcher compares the URL host before DNS resolves it, so a
// compose service name is never tested against an IP range. The CIDR entry
// makes it look handled when it is not — hence no_proxy.
func (s *RunnerSuite) TestNoProxyMatchesBareHostnamesNotJustCIDR() {
	const proxy = "http://host.docker.internal:3128"
	getenv := func(k string) string {
		if k == "HTTP_PROXY" || k == "HTTPS_PROXY" {
			return proxy
		}
		return ""
	}

	tests := []struct {
		name      string
		extra     []string
		url       string
		wantProxy string
	}{
		{
			name:      "bare sibling name is proxied without an entry for it",
			url:       "http://my-service:4566/",
			wantProxy: proxy,
		},
		{
			name:      "the bridge CIDR only helps when a bare IP is dialled",
			url:       "http://172.17.0.4:4566/",
			wantProxy: "",
		},
		{
			name:      "naming the sibling bypasses the proxy",
			extra:     []string{"my-service"},
			url:       "http://my-service:4566/",
			wantProxy: "",
		},
		{
			name:      "the entry needs no port to match one",
			extra:     []string{"my-service"},
			url:       "http://my-service:9999/",
			wantProxy: "",
		},
		{
			name:      "an unrelated sibling is still proxied",
			extra:     []string{"my-service"},
			url:       "http://my-cache:6379/",
			wantProxy: proxy,
		},
		{
			name:      "external hosts keep going through the proxy",
			extra:     []string{"my-service", "my-cache"},
			url:       "https://example.com/simple/",
			wantProxy: proxy,
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			env := ProxyEnv(ProxySettings{}, getenv, tc.extra...)
			require.Equal(s.T(), tc.wantProxy, noProxyFor(env, tc.url))
		})
	}
}

// The same list has to reach the Docker CLI config, or the sibling names bypass
// the proxy for the agent but not for the containers it creates.
func (s *RunnerSuite) TestDockerCLIProxyConfigCarriesNoProxyEntries() {
	const proxy = "http://host.docker.internal:3128"
	getenv := func(k string) string {
		if k == "HTTP_PROXY" || k == "HTTPS_PROXY" {
			return proxy
		}
		return ""
	}
	env := ProxyEnv(ProxySettings{}, getenv, "loop-chrome-abc", "my-service", "my-cache")

	data := dockerCLIProxyConfig(env)
	require.Contains(s.T(), string(data), "my-service")
	require.Contains(s.T(), string(data), "my-cache")

	// And the rendered value behaves the same way the container's own env does.
	var decoded struct {
		Proxies struct {
			Default struct {
				NoProxy string `json:"noProxy"`
			} `json:"default"`
		} `json:"proxies"`
	}
	require.NoError(s.T(), json.Unmarshal(data, &decoded))
	rendered := []string{
		"HTTP_PROXY=" + proxy,
		"HTTPS_PROXY=" + proxy,
		"NO_PROXY=" + decoded.Proxies.Default.NoProxy,
	}
	require.Equal(s.T(), "", noProxyFor(rendered, "http://my-service:4566/"))
	require.Equal(s.T(), proxy, noProxyFor(rendered, "https://example.com/"))
}

// End to end: a project naming its compose services in config has them in the
// container's own NO_PROXY, in both letter cases, alongside the Chrome sidecar.
func (s *RunnerSuite) TestRunNoProxyFromConfig() {
	s.cfg.NoProxy = []string{"my-service", "my-cache"}
	s.applyMockDefaults()
	s.sys.Override("Getenv", "USER").Return("testuser")
	s.sys.On("Getenv", "HTTP_PROXY").Return("http://proxy:8080")
	s.sys.On("Getenv", mock.Anything).Return("")

	ctx := context.Background()
	const want = "host.docker.internal,localhost,127.0.0.1,::1,172.16.0.0/12,loop-chrome-ch-1,my-service,my-cache"
	s.setupMockRun(ctx, mock.MatchedBy(func(cfg *ContainerConfig) bool {
		return slices.Contains(cfg.Env, "NO_PROXY="+want) && slices.Contains(cfg.Env, "no_proxy="+want)
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
