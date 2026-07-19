package api

import (
	"net"
	"net/http"
	"sync"
)

// playgroundService owns the playground and public-share domain: playground
// CRUD/serving, and the public-share infra (opaque-token share store,
// ephemeral listener, cloudflared tunnel). It was extracted from Server so
// playground/share state is reachable only through this struct; shared
// daemon deps are accessed via srv.
type playgroundService struct {
	srv *Server // shared deps: store, logger, loopDir, eventsHub, sys, loadConfig

	// Playground public-share state. shares maps opaque tokens to
	// playgrounds; pgShareServer is an ephemeral listener that serves ONLY
	// /p/{token} (never the main API), which the tunnel exposes publicly.
	// tunnel owns the cloudflared subprocess. All are lazily started on the
	// first share and torn down when the last share is removed.
	shares          *shareStore
	pgShareServer   *http.Server
	pgShareListener net.Listener
	tunnel          TunnelManager
	shareMu         sync.Mutex
	listenTCP       func(addr string) (net.Listener, error) // injectable for tests; nil → net.Listen
}
