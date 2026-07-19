package api

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/radutopala/loop/internal/githubapi"
)

// GitHubLookup is the subset of githubapi.Client the PR endpoint needs.
// It is an interface so tests can stub `gh` invocations without a real
// binary on PATH.
type GitHubLookup interface {
	LookupPR(ctx context.Context, workdir, ghUser, branch string) (*githubapi.PRInfo, error)
}

// prCacheTTL bounds how long a PR lookup (hit or miss) is served from cache.
// Every lookup is a `gh` subprocess doing a network round-trip (1-3s through
// a proxy), and the Git panel fires one per channel select — mostly for
// branches with no PR at all. The cache is also invalidated eagerly when the
// branch poller sees the dir's branch/commit change, and the frontend can
// bypass it with ?fresh=1 (used right after an agent run completes, when a
// new PR is most likely).
const prCacheTTL = time.Minute

type prCacheEntry struct {
	resp prResponse
	at   time.Time
}

func prCacheKey(dir, branch string) string { return dir + "\x00" + branch }

// prLookup owns the PR-lookup slice of the pr domain: the gh client and the
// per-(dir, branch) response cache with TTL and eager per-dir invalidation.
// A value field on Server; the zero value works (nil client → endpoint
// returns empty, nil cache lazily created on first put).
type prLookup struct {
	client GitHubLookup
	mu     sync.Mutex
	cache  map[string]prCacheEntry
	clock  func() time.Time // injectable cache clock for tests; nil → time.Now
}

func (p *prLookup) get(dir, branch string) (prResponse, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.cache[prCacheKey(dir, branch)]
	if !ok || p.now().Sub(e.at) > prCacheTTL {
		return prResponse{}, false
	}
	return e.resp, true
}

func (p *prLookup) put(dir, branch string, resp prResponse) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cache == nil {
		p.cache = make(map[string]prCacheEntry)
	}
	p.cache[prCacheKey(dir, branch)] = prCacheEntry{resp: resp, at: p.now()}
}

// invalidateDir drops every cached PR lookup for a directory.
func (p *prLookup) invalidateDir(dir string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	prefix := dir + "\x00"
	for k := range p.cache {
		if strings.HasPrefix(k, prefix) {
			delete(p.cache, k)
		}
	}
}

// now returns the cache clock (injectable for tests).
func (p *prLookup) now() time.Time {
	if p.clock != nil {
		return p.clock()
	}
	return time.Now()
}

// WithGitHubLookup configures the GitHub PR lookup client used by
// GET /api/channels/{id}/pr. Without it the endpoint returns an empty
// response — useful in tests and headless environments.
func WithGitHubLookup(g GitHubLookup) Option {
	return func(s *Server) { s.prLookup.client = g }
}
