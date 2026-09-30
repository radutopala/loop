package apiauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync"

	"github.com/radutopala/loop/internal/db"
)

// Kind is who a caller is.
type Kind string

const (
	// KindOwner is the user's own desktop app, CLI or host tool.
	KindOwner Kind = "owner"
	// KindAgent is an agent container.
	KindAgent Kind = "agent"
)

// Principal is an authenticated caller. The container, channel and dir are
// set for agents only: the ones its token was issued for.
type Principal struct {
	Kind        Kind
	ContainerID string
	ChannelID   string
	DirPath     string
}

// Store persists issued agent tokens, by hash.
type Store interface {
	InsertAPIToken(ctx context.Context, t *db.APIToken) error
	DeleteAPITokens(ctx context.Context, containerID string) error
	ListAPITokens(ctx context.Context) ([]*db.APIToken, error)
}

// Registry issues agent tokens and looks them up. Tokens are kept by hash,
// in memory and, when there's a store, in the database, so containers keep
// working across a daemon restart.
type Registry struct {
	mu       sync.RWMutex
	byHash   map[string]Principal
	store    Store
	readRand func([]byte) (int, error)
}

// NewRegistry returns a Registry backed by store; nil keeps tokens in
// memory only.
func NewRegistry(store Store) *Registry {
	return &Registry{byHash: map[string]Principal{}, store: store, readRand: rand.Read}
}

// Load reads the stored tokens, dropping those whose container is gone.
func (r *Registry) Load(ctx context.Context, alive func(containerID string) bool) error {
	if r.store == nil {
		return nil
	}
	tokens, err := r.store.ListAPITokens(ctx)
	if err != nil {
		return fmt.Errorf("listing api tokens: %w", err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	gone := map[string]bool{}
	for _, t := range tokens {
		if !alive(t.ContainerID) {
			gone[t.ContainerID] = true
			continue
		}
		r.byHash[t.Hash] = Principal{Kind: KindAgent, ContainerID: t.ContainerID, ChannelID: t.ChannelID, DirPath: t.DirPath}
	}
	for id := range gone {
		if err := r.store.DeleteAPITokens(ctx, id); err != nil {
			return fmt.Errorf("deleting api tokens of %s: %w", id, err)
		}
	}
	return nil
}

// Issue returns a new token for an agent container.
func (r *Registry) Issue(containerID, channelID, dirPath string) (string, error) {
	tok, err := newToken(r.readRand)
	if err != nil {
		return "", err
	}
	h := hashToken(tok)
	if r.store != nil {
		if err := r.store.InsertAPIToken(context.Background(), &db.APIToken{Hash: h, ContainerID: containerID, ChannelID: channelID, DirPath: dirPath}); err != nil {
			return "", fmt.Errorf("storing api token: %w", err)
		}
	}
	r.mu.Lock()
	r.byHash[h] = Principal{Kind: KindAgent, ContainerID: containerID, ChannelID: channelID, DirPath: dirPath}
	r.mu.Unlock()
	return tok, nil
}

// Revoke drops the tokens issued to a container.
func (r *Registry) Revoke(containerID string) error {
	r.mu.Lock()
	for h, p := range r.byHash {
		if p.ContainerID == containerID {
			delete(r.byHash, h)
		}
	}
	r.mu.Unlock()
	if r.store == nil {
		return nil
	}
	return r.store.DeleteAPITokens(context.Background(), containerID)
}

// Lookup returns the agent a token was issued to.
func (r *Registry) Lookup(token string) (Principal, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.byHash[hashToken(token)]
	return p, ok
}

// hashToken is how a token is keyed: looking up its hash rather than the
// token itself keeps the lookup's timing independent of the token.
func hashToken(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}
