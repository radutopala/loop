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
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
