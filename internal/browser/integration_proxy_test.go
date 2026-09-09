//go:build integration

package browser

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
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

// startPageServer serves the suite's test pages and returns the base URL Chrome
// should use to reach them.
//
// Chrome runs in its own container, so 127.0.0.1 is not an option. On a
// developer machine the server listens on the host's loopback and Chrome
// reaches it through host.docker.internal, which Docker maps to the host
// gateway. When the tests themselves run inside a container that mapping leads
// to the wrong machine — the server is here, not on the host — so the listener
// is opened on every interface and Chrome is pointed at this container's own
// bridge address instead.
func startPageServer(t *testing.T, h http.Handler) (*httptest.Server, string) {
	t.Helper()

	srv := httptest.NewUnstartedServer(h)
	host := "host.docker.internal"
	if inDockerContainer() {
		ln, err := net.Listen("tcp", ":0")
		require.NoError(t, err, "listen on all interfaces")
		require.NoError(t, srv.Listener.Close())
		srv.Listener = ln
		host = localBridgeIP(t)
	}
	srv.Start()

	_, port, err := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	require.NoError(t, err, "parse test server URL %q", srv.URL)
	return srv, "http://" + net.JoinHostPort(host, port)
}

// localBridgeIP returns this container's address on the Docker bridge, which is
// how a sidecar on the same network reaches it.
func localBridgeIP(t *testing.T) string {
	t.Helper()

	addrs, err := net.InterfaceAddrs()
	require.NoError(t, err)
	for _, a := range addrs {
		ipNet, ok := a.(*net.IPNet)
		if !ok || ipNet.IP.IsLoopback() {
			continue
		}
		if ip4 := ipNet.IP.To4(); ip4 != nil {
			return ip4.String()
		}
	}
	require.Fail(t, "no non-loopback IPv4 address for this container")
	return ""
}

// dialCDP connects to a sidecar's CDP endpoint, retrying while Chrome finishes
// coming up. Starting the container is not the same as Chrome listening: socat
// accepts the connection first and the handshake comes back as an EOF for the
// first few seconds.
func dialCDP(t *testing.T, endpoint string) *CDPClient {
	t.Helper()

	allowDirectCDP(t, endpoint)

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	var err error
	for range 20 {
		var client *CDPClient
		client, err = NewCDPClient(context.Background(), endpoint, logger)
		if err == nil {
			return client
		}
		time.Sleep(500 * time.Millisecond)
	}
	require.NoError(t, err, "chrome not ready after 10s")
	return nil
}
