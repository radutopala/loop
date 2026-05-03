package graph

import "sync"

// Cache is the per-channel Graph store the engine reads from on snapshot
// requests.
//
// The cache is keyed by channelID; each channel owns one current Graph. A
// nil Graph slot signals "needs rebuild" — the next Snapshot call will
// trigger a rescan instead of returning stale state. Invalidate marks a
// slot dirty; full replace happens through Set after a scan completes.
//
// Concurrency: a single sync.RWMutex guards the map. Reads are common
// (every snapshot fetch); writes are rare (one per scan + one per
// invalidate burst, debounced upstream). RWMutex is the simplest
// shape that satisfies both without lock contention.
type Cache struct {
	mu     sync.RWMutex
	graphs map[string]*Graph
	dirty  map[string]struct{}
}

// NewCache returns an empty Cache ready for Set/Get/Invalidate.
func NewCache() *Cache {
	return &Cache{
		graphs: make(map[string]*Graph),
		dirty:  make(map[string]struct{}),
	}
}

// Set replaces the Graph for channelID and clears the dirty flag. Called
// by the engine after a scan completes (or after a forced rebuild). A nil
// graph is rejected — use Invalidate for "needs rebuild" semantics.
func (c *Cache) Set(channelID string, g *Graph) {
	if g == nil {
		return
	}
	c.mu.Lock()
	c.graphs[channelID] = g
	delete(c.dirty, channelID)
	c.mu.Unlock()
}

// Get returns the cached Graph for channelID and whether the slot is
// considered dirty. The dirty flag flips on Invalidate and resets on Set;
// the engine uses it to decide whether to return the cached snapshot or
// trigger a rescan.
func (c *Cache) Get(channelID string) (g *Graph, dirty bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	g = c.graphs[channelID]
	_, dirty = c.dirty[channelID]
	return g, dirty
}

// Invalidate marks the channel's slot dirty. The cached Graph is kept
// (so the panel can keep rendering the previous snapshot at reduced
// opacity while the rescan is in flight) — only the dirty flag flips.
// Calling Invalidate on an unknown channelID is a no-op: writes to a
// channel before it has ever been scanned simply get coalesced into the
// first scan.
func (c *Cache) Invalidate(channelID string) {
	c.mu.Lock()
	c.dirty[channelID] = struct{}{}
	c.mu.Unlock()
}

// Drop removes a channel's slot entirely. Called when a channel row is
// deleted (the snapshot foreign key cascades; the cache must not retain
// state for a vanished channel).
func (c *Cache) Drop(channelID string) {
	c.mu.Lock()
	delete(c.graphs, channelID)
	delete(c.dirty, channelID)
	c.mu.Unlock()
}
