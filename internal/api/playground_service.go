package api

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"sync"

	"github.com/radutopala/loop/internal/db"
)

// playgroundService owns the playground and public-share domain: playground
// CRUD/serving, and the public-share infra (opaque-token share store,
// ephemeral listener, cloudflared tunnel). It was extracted from Server so
// playground/share state is reachable only through this struct; shared
// daemon deps are accessed via srv.
type playgroundService struct {
	deps *serverDeps // shared infrastructure; see serverDeps

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

// newPlaygroundService creates the playground domain with an empty share
// store. The tunnel manager is injected at construction via WithTunnel.
func newPlaygroundService(deps *serverDeps) *playgroundService {
	return &playgroundService{deps: deps, shares: newShareStore()}
}

// TunnelManager is the subset of tunnel.Manager the server needs, injectable
// so tests don't spawn cloudflared.
type TunnelManager interface {
	Start(ctx context.Context, localPort int) (string, error)
	Stop()
	PublicURL() string
	Running() bool
}

// validatePlaygroundDir validates the playground name (path containment + regex)
// and returns a safe directory path under the playground base directory.
func (s *playgroundService) validatePlaygroundDir(name string) (string, error) {
	baseDir := filepath.Join(s.deps.loopDir, "playground")
	return validatePlaygroundDirIn(baseDir, name)
}

// resolvePlaygroundDir resolves the playground directory based on scope.
// scope "project" requires a channel_id to resolve the project dir.
func (s *playgroundService) resolvePlaygroundDir(r *http.Request, name string) (string, error) {
	scope := r.URL.Query().Get("scope")
	if scope == "project" {
		channelID := r.URL.Query().Get("channel_id")
		if channelID == "" {
			return "", fmt.Errorf("channel_id is required for project-scoped playgrounds")
		}
		dirPath, err := s.projectPlaygroundDir(r.Context(), channelID)
		if err != nil {
			return "", err
		}
		baseDir := filepath.Join(dirPath, ".loop", "playground")
		return validatePlaygroundDirIn(baseDir, name)
	}
	return s.validatePlaygroundDir(name)
}

// projectPlaygroundDir resolves the directory that holds a channel's
// project-scoped playgrounds. For a worktree channel — or a thread under a
// worktree chain — it returns the root project checkout, so every thread and
// worktree of a project sees and shares the same project playgrounds (matching
// how worktrees inherit the root's .loop/config.json). Non-worktree channels
// resolve to their own dir. The channel is fetched once; the worktree walk
// only makes extra lookups when the channel is (or is under) a worktree.
func (s *playgroundService) projectPlaygroundDir(ctx context.Context, channelID string) (string, error) {
	if channelID == "" {
		return "", fmt.Errorf("channel_id is required")
	}
	if s.deps.store == nil {
		return "", fmt.Errorf("channel lookup not configured")
	}
	ch, err := s.deps.store.GetChannel(ctx, channelID)
	if err != nil {
		return "", fmt.Errorf("looking up channel: %w", err)
	}
	if ch == nil {
		return "", fmt.Errorf("channel %s not found", channelID)
	}
	if root := s.worktreeRootDir(ctx, ch); root != "" {
		return root, nil
	}
	if ch.DirPath == "" {
		if s.deps.loopDir != "" {
			return filepath.Join(s.deps.loopDir, channelID, "work"), nil
		}
		return "", fmt.Errorf("channel %s has no dir_path", channelID)
	}
	return ch.DirPath, nil
}

// worktreeRootDir returns the DirPath of the nearest non-worktree ancestor for
// an already-fetched channel that is (or lives under) a worktree chain, or ""
// when it isn't part of one. Handles worktree channels, threads that share a
// worktree's dir without the worktree flag, and nested worktrees. The walk is
// bounded to guard against parent-id cycles. Mirrors the orchestrator's
// worktreeRootFor.
func (s *playgroundService) worktreeRootDir(ctx context.Context, ch *db.Channel) string {
	cur := ch
	if !cur.Worktree {
		// A thread row under a worktree channel: hop to the worktree itself.
		if cur.ParentID == "" {
			return ""
		}
		p, err := s.deps.store.GetChannel(ctx, cur.ParentID)
		if err != nil || p == nil || !p.Worktree {
			return ""
		}
		cur = p
	}
	for range 8 {
		if cur.ParentID == "" {
			return ""
		}
		p, err := s.deps.store.GetChannel(ctx, cur.ParentID)
		if err != nil || p == nil {
			return ""
		}
		if !p.Worktree {
			return p.DirPath
		}
		cur = p
	}
	return ""
}

// resolveProjectPlaygroundDir resolves a playground dir from channel_id and name path values.
func (s *playgroundService) resolveProjectPlaygroundDir(r *http.Request) (string, error) {
	channelID := r.PathValue("channel_id")
	name := r.PathValue("name")
	if channelID == "" || name == "" {
		return "", fmt.Errorf("channel_id and name are required")
	}
	dirPath, err := s.projectPlaygroundDir(r.Context(), channelID)
	if err != nil {
		return "", err
	}
	baseDir := filepath.Join(dirPath, ".loop", "playground")
	return validatePlaygroundDirIn(baseDir, name)
}

// playgroundShareEnabled reports whether the public-share feature is turned on
// in config (default off).
func (s *playgroundService) playgroundShareEnabled() bool {
	cfg := s.deps.configs.merged("", "")
	if cfg == nil {
		return false
	}
	return cfg.PlaygroundShare.Enabled
}

// shareURL builds the public URL for a token, or "" if the tunnel isn't up.
func (s *playgroundService) shareURL(token string) string {
	if s.tunnel == nil {
		return ""
	}
	base := s.tunnel.PublicURL()
	if base == "" {
		return ""
	}
	return base + "/p/" + token
}

// broadcastShareUpdate notifies the panel that a playground's share state
// changed (url empty means unshared).
func (s *playgroundService) broadcastShareUpdate(name, scope, channelID, url string) {
	if s.deps.eventsHub == nil {
		return
	}
	s.deps.eventsHub.Broadcast(Event{
		Type:   EventPlaygroundUpdate,
		Global: true,
		Data: map[string]string{
			"kind":       "share",
			"name":       name,
			"scope":      scope,
			"channel_id": channelID,
			"url":        url,
		},
	})
}

// ensureShareInfra lazily starts the ephemeral playground listener and the
// cloudflared tunnel, returning the public tunnel URL. Idempotent.
func (s *playgroundService) ensureShareInfra(ctx context.Context) (string, error) {
	s.shareMu.Lock()
	defer s.shareMu.Unlock()

	if s.pgShareListener == nil {
		listen := s.listenTCP
		if listen == nil {
			listen = func(addr string) (net.Listener, error) { return net.Listen("tcp", addr) }
		}
		ln, err := listen("127.0.0.1:0")
		if err != nil {
			return "", fmt.Errorf("starting playground listener: %w", err)
		}
		s.pgShareListener = ln
		s.pgShareServer = &http.Server{Handler: s.buildShareMux()}
		go s.pgShareServer.Serve(ln) //nolint:errcheck
	}
	if s.tunnel == nil {
		return "", fmt.Errorf("tunnel manager not configured")
	}
	port := s.pgShareListener.Addr().(*net.TCPAddr).Port
	url, err := s.tunnel.Start(ctx, port)
	if err != nil {
		return "", err
	}
	return url, nil
}

// maybeStopShareInfra tears down the tunnel and ephemeral listener once no
// shares remain.
func (s *playgroundService) maybeStopShareInfra() {
	if s.shares.count() > 0 {
		return
	}
	s.shareMu.Lock()
	defer s.shareMu.Unlock()
	if s.tunnel != nil {
		s.tunnel.Stop()
	}
	if s.pgShareServer != nil {
		_ = s.pgShareServer.Close()
		s.pgShareServer = nil
		s.pgShareListener = nil
	}
}

// stopShareInfra unconditionally tears down the tunnel and ephemeral listener,
// used on daemon shutdown regardless of remaining share count.
func (s *playgroundService) stopShareInfra() {
	s.shareMu.Lock()
	defer s.shareMu.Unlock()
	if s.tunnel != nil {
		s.tunnel.Stop()
	}
	if s.pgShareServer != nil {
		_ = s.pgShareServer.Close()
		s.pgShareServer = nil
		s.pgShareListener = nil
	}
}

// buildShareMux builds the ephemeral listener's handler. It registers ONLY the
// public /p/{token} routes — no other endpoint of the main API is reachable
// through the tunnel.
func (s *playgroundService) buildShareMux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /p/{token}", s.handleSharedPlaygroundServe)
	mux.HandleFunc("GET /p/{token}/{path...}", s.handleSharedPlaygroundServeFile)
	return noStore(mux)
}

// setTunnel wires the cloudflared tunnel manager used by the public
// playground-share feature. Nil leaves sharing unavailable.
func (s *playgroundService) setTunnel(tm TunnelManager) {
	s.tunnel = tm
}

// WithTunnel configures the playground-share tunnel manager at construction.
func WithTunnel(tm TunnelManager) Option {
	return func(s *Server) { s.playground.setTunnel(tm) }
}
