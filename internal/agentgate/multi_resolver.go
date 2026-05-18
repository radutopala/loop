package agentgate

import (
	"crypto/subtle"
	"errors"
	"sync"
)

// MultiManagerResolver dispatches approval clicks to the Manager that holds
// the pending request. A single bot (Discord/Slack/Local) holds one of these
// and each per-container Manager is added on container spawn and removed on
// container exit.
//
// Resolve consults each registered Manager in turn: the first one whose
// Resolve does not return ErrNoSuchRequest wins. If no Manager recognises the
// reqID, ErrNoSuchRequest is returned to the caller.
//
// In addition to click routing, the resolver holds a per-container bearer
// token + channelID so the in-container docker proxy and seccomp-gate parent
// can call back via HTTP. ByToken authenticates and returns the matching
// container's Manager + channelID.
//
// Satisfies bot.ApprovalResolver.
type MultiManagerResolver struct {
	mu       sync.RWMutex
	managers map[string]*Manager // keyed by containerID (opaque to the resolver)
	tokens   map[string]string   // token → containerID
	channels map[string]string   // containerID → channelID
}

// NewMultiManagerResolver constructs an empty resolver.
func NewMultiManagerResolver() *MultiManagerResolver {
	return &MultiManagerResolver{
		managers: map[string]*Manager{},
		tokens:   map[string]string{},
		channels: map[string]string{},
	}
}

// Add registers a Manager under containerID without an HTTP route. Calling
// Add twice with the same containerID replaces the prior Manager — caller is
// responsible for ensuring the previous container is gone.
func (r *MultiManagerResolver) Add(containerID string, m *Manager) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.managers[containerID] = m
}

// AddWithToken registers a Manager under containerID and associates a shared
// bearer token + channelID for HTTP approval routing. The token authenticates
// inbound requests from the in-container docker proxy and seccomp-gate
// parent process. Passing an empty token registers a Manager with no HTTP
// route (equivalent to Add).
func (r *MultiManagerResolver) AddWithToken(containerID, token string, m *Manager, channelID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.managers[containerID] = m
	if token != "" {
		r.tokens[token] = containerID
	}
	r.channels[containerID] = channelID
}

// ByToken returns (containerID, Manager, channelID) whose token matches.
// Comparison is performed in constant time via crypto/subtle to avoid
// timing-based token guessing. Returns (_, nil, _, false) on miss.
func (r *MultiManagerResolver) ByToken(token string) (string, *Manager, string, bool) {
	if token == "" {
		return "", nil, "", false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	// Constant-time scan: compare against every known token so lookup time
	// is independent of which token (if any) matches.
	var matchedCID string
	tokenBytes := []byte(token)
	for knownToken, cid := range r.tokens {
		if subtle.ConstantTimeCompare([]byte(knownToken), tokenBytes) == 1 {
			matchedCID = cid
		}
	}
	if matchedCID == "" {
		return "", nil, "", false
	}
	mgr, ok := r.managers[matchedCID]
	if !ok {
		return "", nil, "", false
	}
	return matchedCID, mgr, r.channels[matchedCID], true
}

// Remove drops the Manager for containerID, along with any associated token
// and channelID. Safe to call for an unknown ID. The Manager's Shutdown is
// invoked first so any pending approvals get a deny resolution + a
// gate.approval_resolved broadcast — without this, the in-container HTTP
// caller is already gone (its socket is dead) but the FE/electron bouncer
// has no way to learn the request was abandoned.
func (r *MultiManagerResolver) Remove(containerID string) {
	r.mu.Lock()
	mgr := r.managers[containerID]
	delete(r.managers, containerID)
	delete(r.channels, containerID)
	for tok, cid := range r.tokens {
		if cid == containerID {
			delete(r.tokens, tok)
		}
	}
	r.mu.Unlock()
	if mgr != nil {
		mgr.Shutdown()
	}
}

// Resolve routes the click to whichever Manager holds the pending request.
// Returns ErrNoSuchRequest when no Manager owns the reqID.
func (r *MultiManagerResolver) Resolve(reqID, decision, actorID string) error {
	r.mu.RLock()
	mgrs := make([]*Manager, 0, len(r.managers))
	for _, m := range r.managers {
		mgrs = append(mgrs, m)
	}
	r.mu.RUnlock()

	for _, m := range mgrs {
		err := m.Resolve(reqID, decision, actorID)
		if err == nil {
			return nil
		}
		if !errors.Is(err, ErrNoSuchRequest) {
			return err
		}
	}
	return ErrNoSuchRequest
}
