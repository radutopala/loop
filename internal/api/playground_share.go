// playground_share.go implements the public playground-share feature: a
// playground can be exposed over the internet through a cloudflared quick
// tunnel that points at a dedicated, playground-only HTTP listener. The main
// API listener (:8222) is never tunneled — only the /p/{token} routes on the
// ephemeral listener are reachable publicly, so no other endpoint is exposed.
//
// Lifecycle is reference-counted: the first active share lazily starts the
// ephemeral listener + tunnel; the last removal tears both down.
package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"
)

// shareEntry describes one publicly-shared playground.
type shareEntry struct {
	Token     string `json:"token"`
	Name      string `json:"name"`
	Scope     string `json:"scope"`
	ChannelID string `json:"channel_id"`
	AbsDir    string `json:"-"` // resolved absolute dir; never serialized to clients
}

// shareStore holds the active shares, keyed by opaque token. Safe for
// concurrent use.
type shareStore struct {
	mu     sync.Mutex
	shares map[string]shareEntry
}

func newShareStore() *shareStore {
	return &shareStore{shares: map[string]shareEntry{}}
}

// add registers (or returns the existing) share for a playground and returns
// its token. Idempotent per resolved absolute dir (absDir): the same physical
// playground shared from different channels/threads/panels — including a
// project playground shared from multiple threads of the same project, which
// all resolve to the same dir — returns the same token rather than stacking
// duplicate tunnels. (Global playgrounds always resolve to one dir; worktree
// threads resolve to distinct dirs, so those remain separate shares.)
func (st *shareStore) add(name, scope, channelID, absDir string) string {
	st.mu.Lock()
	defer st.mu.Unlock()
	for tok, e := range st.shares {
		if e.AbsDir == absDir {
			return tok
		}
	}
	tok := newShareToken(name, scope)
	st.shares[tok] = shareEntry{Token: tok, Name: name, Scope: scope, ChannelID: channelID, AbsDir: absDir}
	return tok
}

// removeByDir drops the share for the given resolved absolute dir, if any, and
// returns whether one was removed.
func (st *shareStore) removeByDir(absDir string) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	for tok, e := range st.shares {
		if e.AbsDir == absDir {
			delete(st.shares, tok)
			return true
		}
	}
	return false
}

// lookupByDir returns the share for a resolved absolute dir, if any.
func (st *shareStore) lookupByDir(absDir string) (shareEntry, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	for _, e := range st.shares {
		if e.AbsDir == absDir {
			return e, true
		}
	}
	return shareEntry{}, false
}

// lookup returns the share entry for a token.
func (st *shareStore) lookup(token string) (shareEntry, bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	e, ok := st.shares[token]
	return e, ok
}

// count returns the number of active shares.
func (st *shareStore) count() int {
	st.mu.Lock()
	defer st.mu.Unlock()
	return len(st.shares)
}

// list returns a snapshot of the active shares.
func (st *shareStore) list() []shareEntry {
	st.mu.Lock()
	defer st.mu.Unlock()
	out := make([]shareEntry, 0, len(st.shares))
	for _, e := range st.shares {
		out = append(out, e)
	}
	return out
}

// newShareToken derives a 32-hex opaque, unguessable token from the
// playground identity plus a random nonce.
func newShareToken(name, scope string) string {
	nonce := make([]byte, 16)
	_, _ = rand.Read(nonce)
	sum := sha256.Sum256(append([]byte(name+"\x00"+scope+"\x00"), nonce...))
	return hex.EncodeToString(sum[:])[:32]
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

// handlePlaygroundShare handles PUT /api/playground/share — shares a playground
// publicly and returns its tunnel URL. Body/query: name, scope, channel_id.
func (s *playgroundService) handlePlaygroundShare(w http.ResponseWriter, r *http.Request) {
	if !s.playgroundShareEnabled() {
		http.Error(w, "playground share is disabled", http.StatusForbidden)
		return
	}
	name := r.URL.Query().Get("name")
	if name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	pgDir, err := s.resolvePlaygroundDir(r, name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	scope, channelID := playgroundScopeFromRequest(r)

	token := s.shares.add(name, scope, channelID, pgDir)

	publicURL, err := s.ensureShareInfra(r.Context())
	if err != nil {
		// Roll back the share so a failed tunnel start doesn't leave a
		// dangling entry that blocks teardown.
		s.shares.removeByDir(pgDir)
		http.Error(w, "starting tunnel: "+err.Error(), http.StatusInternalServerError)
		return
	}

	url := publicURL + "/p/" + token
	s.broadcastShareUpdate(name, scope, channelID, url)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"url": url, "token": token}) //nolint:errcheck
}

// handlePlaygroundUnshare handles DELETE /api/playground/share — stops sharing
// a playground and tears down the tunnel when it was the last share. The dir is
// resolved so any channel/thread that maps to the same playground can unshare
// it (identity is the dir, not the requesting channel).
func (s *playgroundService) handlePlaygroundUnshare(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	pgDir, err := s.resolvePlaygroundDir(r, name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	scope, channelID := playgroundScopeFromRequest(r)
	s.shares.removeByDir(pgDir)
	s.maybeStopShareInfra()
	s.broadcastShareUpdate(name, scope, channelID, "")
	w.WriteHeader(http.StatusNoContent)
}

// handlePlaygroundShareList handles GET /api/playground/share. With a `name`
// query param it returns the share status for that one playground (resolving
// its dir, so any channel mapping to the same dir sees the same answer); with
// no name it lists every active share for the global panel.
func (s *playgroundService) handlePlaygroundShareList(w http.ResponseWriter, r *http.Request) {
	if name := r.URL.Query().Get("name"); name != "" {
		s.handlePlaygroundShareStatus(w, r, name)
		return
	}
	type row struct {
		Name      string `json:"name"`
		Scope     string `json:"scope"`
		ChannelID string `json:"channel_id"`
		URL       string `json:"url"`
	}
	rows := []row{}
	for _, e := range s.shares.list() {
		rows = append(rows, row{Name: e.Name, Scope: e.Scope, ChannelID: e.ChannelID, URL: s.shareURL(e.Token)})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string][]row{"shares": rows}) //nolint:errcheck
}

// handlePlaygroundShareStatus returns whether a specific playground is shared
// and its public URL, resolving the dir so the answer is identical for every
// channel/thread that maps to the same playground.
func (s *playgroundService) handlePlaygroundShareStatus(w http.ResponseWriter, r *http.Request, name string) {
	pgDir, err := s.resolvePlaygroundDir(r, name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	url := ""
	if e, ok := s.shares.lookupByDir(pgDir); ok {
		url = s.shareURL(e.Token)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"shared": url != "", "url": url}) //nolint:errcheck
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

// noStore wraps a handler to set Cache-Control: no-store, so revoked shares
// aren't served from a cache after toggle-off.
func noStore(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		h.ServeHTTP(w, r)
	})
}

// handleSharedPlaygroundServe serves a shared playground's index by token.
func (s *playgroundService) handleSharedPlaygroundServe(w http.ResponseWriter, r *http.Request) {
	e, ok := s.shares.lookup(r.PathValue("token"))
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	renderPlaygroundIndex(w, e.AbsDir, "/p/"+e.Token+"/")
}

// handleSharedPlaygroundServeFile serves a shared playground's asset by token.
func (s *playgroundService) handleSharedPlaygroundServeFile(w http.ResponseWriter, r *http.Request) {
	e, ok := s.shares.lookup(r.PathValue("token"))
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	servePlaygroundFile(w, e.AbsDir, r.PathValue("path"))
}
