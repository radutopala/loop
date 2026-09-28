package api

import (
	"path/filepath"
	"sync"
)

// configLocks serializes the read-modify-write edits of a config file, one
// mutex per path. Two edits of the same file at once would each read the
// old content, and the later write would drop the earlier one's change.
// The zero value is ready to use.
type configLocks struct {
	mu    sync.Mutex
	paths map[string]*sync.Mutex
}

// lock locks path's mutex and returns the func that unlocks it.
func (c *configLocks) lock(path string) func() {
	path = filepath.Clean(path)
	c.mu.Lock()
	if c.paths == nil {
		c.paths = map[string]*sync.Mutex{}
	}
	m := c.paths[path]
	if m == nil {
		m = &sync.Mutex{}
		c.paths[path] = m
	}
	c.mu.Unlock()
	m.Lock()
	return m.Unlock
}
