// cached.go provides a mtime-gated wrapper around Reload so the hot-reload
// path — called by the runner, orchestrator, command builder, workflow engine
// and API handlers on every message/run/request — re-parses
// ~/.loop/config.json only when the file actually changes.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// CachedReloader memoizes Reload keyed on the config file's mtime+size.
// Behavior is otherwise identical to Reload: hot-reload semantics are
// preserved (an edited file is picked up on the next call), errors are
// returned uncached, and every caller receives its own shallow copy so
// top-level field mutations can't leak across consumers.
type CachedReloader struct {
	mu     sync.Mutex
	loader *Loader
	stat   func(string) (os.FileInfo, error)

	path   string // resolved on first use (home dir lookup can fail transiently)
	cached *Config
	mtime  time.Time
	size   int64
}

// NewCachedReloader returns a reloader backed by the real filesystem.
// Pass its Reload method wherever a `func() (*Config, error)` hot-reload
// loader is expected.
func NewCachedReloader() *CachedReloader {
	return &CachedReloader{loader: newLoader(), stat: os.Stat}
}

// Reload returns the parsed config, re-reading the file only when its
// mtime or size changed since the last successful parse.
func (c *CachedReloader) Reload() (*Config, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.path == "" {
		home, err := c.loader.userHomeDir()
		if err != nil {
			return nil, fmt.Errorf("getting home directory: %w", err)
		}
		c.path = filepath.Join(home, ".loop", "config.json")
	}

	fi, err := c.stat(c.path)
	if err != nil {
		// Can't stat (missing file, transient FS error): fall through to a
		// plain reload so the error surface matches the uncached path.
		return c.loader.reload()
	}
	if c.cached != nil && fi.ModTime().Equal(c.mtime) && fi.Size() == c.size {
		cp := *c.cached
		return &cp, nil
	}

	cfg, err := c.loader.reload()
	if err != nil {
		return nil, err
	}
	c.cached, c.mtime, c.size = cfg, fi.ModTime(), fi.Size()
	cp := *cfg
	return &cp, nil
}
