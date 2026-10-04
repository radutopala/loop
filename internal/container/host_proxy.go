package container

import (
	"net/http"
	"net/url"
	"strings"

	"golang.org/x/net/http/httpproxy"

	"github.com/radutopala/loop/internal/config"
)

// HostProxyFunc returns the proxy function for the daemon's own outbound
// connections (chat platforms, version checks), for use as an
// http.Transport's or a websocket.Dialer's Proxy.
//
// Containers get their proxy from config falling back to the daemon's
// environment, and so does the daemon: the config's proxies block wins per
// variable, getenv fills the rest, and proxies.no_proxy adds to the
// environment's NO_PROXY. The config is re-read through reload on every
// connection, so a proxy switched on or off in config applies to the next
// connection without a restart. Unlike ProxyEnv, localhost is not rewritten:
// the daemon runs on the host, where the proxy is reached as configured.
// Requests to localhost itself are never proxied, nor are those to the Docker
// bridge range: a daemon running in a container reaches its sidecars by
// bridge address, which a proxy on the Docker host has no route back into.
func HostProxyFunc(reload func() (*config.Config, error), getenv func(string) string) func(*http.Request) (*url.URL, error) {
	return func(req *http.Request) (*url.URL, error) {
		var proxies config.ProxiesConfig
		if cfg, err := reload(); err == nil {
			proxies = ProxySettingsFromConfig(cfg)
		}
		return hostProxyConfig(proxies, getenv).ProxyFunc()(req.URL)
	}
}

// hostProxyConfig resolves proxies against getenv into the matcher config.
func hostProxyConfig(proxies config.ProxiesConfig, getenv func(string) string) *httpproxy.Config {
	resolve := func(configured, upper, lower string) string {
		if configured != "" {
			return configured
		}
		if v := getenv(upper); v != "" {
			return v
		}
		return getenv(lower)
	}
	noProxy := []string{dockerBridgeCIDR}
	if v := resolve("", "NO_PROXY", "no_proxy"); v != "" {
		noProxy = append(noProxy, v)
	}
	return &httpproxy.Config{
		HTTPProxy:  resolve(proxies.HTTPProxy, "HTTP_PROXY", "http_proxy"),
		HTTPSProxy: resolve(proxies.HTTPSProxy, "HTTPS_PROXY", "https_proxy"),
		NoProxy:    strings.Join(append(noProxy, proxies.NoProxy...), ","),
	}
}
