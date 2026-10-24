package api

import (
	"context"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"github.com/radutopala/loop/internal/osutil"
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

	// stateMu serializes reads and writes of playground state files.
	stateMu        sync.Mutex
	writeStateFile func(pg *os.Root, name string, data []byte, perm os.FileMode) error // injectable for tests; nil → pg.WriteFile

	removeAll func(base *os.Root, name string) error // injectable for tests; nil → base.RemoveAll
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

// playgroundLoc locates playground name in the playground base dir base.
// A project playground's base, {project}/.loop/playground, lies in a dir
// the agent can write, so project names the dir it's reached through; a
// global playground's base is ~/.loop/playground and project is "".
//
// Nothing under base is opened by path: open and openBase return os.Roots,
// and every read, write and delete goes through one, so a symlink the agent
// plants can't lead the daemon to a host file, not even one swapped in after
// a check.
type playgroundLoc struct {
	project string
	base    string
	name    string
}

// dir is the playground's path, which identifies it for sharing.
func (l playgroundLoc) dir() string { return filepath.Join(l.base, l.name) }

// openBase opens l's playground base dir as an os.Root, creating it first
// when create is set. A project's base must be a real dir in the project
// (see osutil.OpenRootIn): linked to the repo's .git, say, it would have
// the daemon write hooks the agent can't. The global base is the mount
// point the agent reaches it through, which it can fill but not replace,
// so a link the user made there is followed.
func (l playgroundLoc) openBase(create bool) (*os.Root, error) {
	if l.project == "" {
		if create {
			if err := os.MkdirAll(l.base, 0o755); err != nil {
				return nil, err
			}
		}
		return os.OpenRoot(l.base)
	}
	if create {
		if err := os.MkdirAll(l.project, 0o755); err != nil {
			return nil, err
		}
		if err := osutil.MkdirAllIn(l.project, l.base, 0o755); err != nil {
			return nil, err
		}
	}
	return osutil.OpenRootIn(l.project, l.base)
}

// open opens the playground's own dir as an os.Root, through its base,
// creating it first when create is set. The dir may be a link to another
// playground in the base; the files are confined to the dir it opens, so a
// playground's links can't reach the host, nor a sibling playground (which
// would go public with it when it's shared).
func (l playgroundLoc) open(create bool) (*os.Root, error) {
	base, err := l.openBase(create)
	if err != nil {
		return nil, err
	}
	defer base.Close()
	if create {
		if err := base.MkdirAll(l.name, 0o755); err != nil {
			return nil, err
		}
	}
	return base.OpenRoot(l.name)
}

// errPlaygroundTraversal answers a path that leads outside its playground.
const errPlaygroundTraversal = "path traversal not allowed"

// openPlayground opens loc's playground for a handler that doesn't create
// it, answering the request itself with status when loc leads outside its
// base. A playground that can't be opened otherwise, most likely missing, is
// a nil root, which readPlaygroundFile finds nothing in.
func openPlayground(w http.ResponseWriter, loc playgroundLoc, status int) (*os.Root, bool) {
	pg, err := loc.open(false)
	if osutil.IsEscape(err) {
		http.Error(w, errPlaygroundTraversal, status)
		return nil, false
	}
	return pg, true
}

// readPlaygroundFile reads rel from pg, a playground opened by
// openPlayground.
func readPlaygroundFile(pg *os.Root, rel string) ([]byte, error) {
	if pg == nil {
		return nil, fs.ErrNotExist
	}
	return pg.ReadFile(rel)
}

// playgroundFileError answers a failed write: a path that leads outside the
// playground is a 400, anything else a 500 prefixed with what failed.
func playgroundFileError(w http.ResponseWriter, err error, what string) {
	if osutil.IsEscape(err) {
		http.Error(w, errPlaygroundTraversal, http.StatusBadRequest)
		return
	}
	http.Error(w, what+err.Error(), http.StatusInternalServerError)
}

// validatePlaygroundDir validates the playground name and locates it under
// the global playground base dir.
func (s *playgroundService) validatePlaygroundDir(name string) (playgroundLoc, error) {
	return validatePlaygroundDirIn("", filepath.Join(s.deps.loopDir, "playground"), name)
}

// projectPlayground locates playground name of channelID's project.
func (s *playgroundService) projectPlayground(ctx context.Context, channelID, name string) (playgroundLoc, error) {
	dirPath, err := s.projectPlaygroundDir(ctx, channelID)
	if err != nil {
		return playgroundLoc{}, err
	}
	return validatePlaygroundDirIn(dirPath, filepath.Join(dirPath, ".loop", "playground"), name)
}

// resolvePlaygroundDir resolves the playground directory based on scope.
// scope "project" requires a channel_id to resolve the project dir.
func (s *playgroundService) resolvePlaygroundDir(r *http.Request, name string) (playgroundLoc, error) {
	scope := r.URL.Query().Get("scope")
	if scope == "project" {
		channelID := r.URL.Query().Get("channel_id")
		if channelID == "" {
			return playgroundLoc{}, fmt.Errorf("channel_id is required for project-scoped playgrounds")
		}
		return s.projectPlayground(r.Context(), channelID, name)
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
	if root := worktreeRootDirPath(ctx, s.deps.store, ch); root != "" {
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

// resolveProjectPlaygroundDir resolves a playground dir from channel_id and name path values.
func (s *playgroundService) resolveProjectPlaygroundDir(r *http.Request) (playgroundLoc, error) {
	channelID := r.PathValue("channel_id")
	name := r.PathValue("name")
	if channelID == "" || name == "" {
		return playgroundLoc{}, fmt.Errorf("channel_id and name are required")
	}
	return s.projectPlayground(r.Context(), channelID, name)
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
