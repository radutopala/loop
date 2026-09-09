//go:build integration

package browser

import (
	"net"
	"net/url"
	"os"
	"testing"
)

// allowDirectCDP excludes a CDP endpoint's host from the proxy for the duration
// of the test.
//
// The CDP handshake is a plain HTTP GET to /json/version, so Go's
// http.ProxyFromEnvironment applies to it. In an environment with a corporate
// HTTP_PROXY the request is handed to the proxy, which has no route to a Docker
// bridge address, and the connection times out rather than being refused —
// making it look like Chrome never came up. The daemon does not hit this
// because it normally runs on the Docker host and dials 127.0.0.1, which Go
// never proxies.
func allowDirectCDP(t *testing.T, endpoint string) {
	t.Helper()

	host := endpoint
	if u, err := url.Parse(endpoint); err == nil && u.Host != "" {
		host = u.Host
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if host == "" {
		return
	}
	for _, key := range []string{"NO_PROXY", "no_proxy"} {
		if v := os.Getenv(key); v != "" {
			t.Setenv(key, v+","+host)
			continue
		}
		t.Setenv(key, host)
	}
}
