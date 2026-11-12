// Package filewatch reports changes to a set of files and directories a
// client has on screen: a file is changed when it's written, replaced or
// removed, a directory when an entry in it appears, goes or is renamed.
// Changes are coalesced, so a burst of writes reads as one.
package filewatch

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Watcher is the part of fsnotify.Watcher a Session uses.
type Watcher interface {
	Add(name string) error
	Remove(name string) error
	Close() error
	Events() <-chan fsnotify.Event
	Errors() <-chan error
}

type fsWatcher struct{ *fsnotify.Watcher }

func (w fsWatcher) Events() <-chan fsnotify.Event { return w.Watcher.Events }
func (w fsWatcher) Errors() <-chan error          { return w.Watcher.Errors }

// New returns a Watcher backed by fsnotify.
func New() (Watcher, error) {
	return wrap(fsnotify.NewWatcher())
}

func wrap(w *fsnotify.Watcher, err error) (Watcher, error) {
	if err != nil {
		return nil, err
	}
	return fsWatcher{w}, nil
}

// Kind tells what changed.
type Kind string

const (
	KindFile Kind = "file"
	KindDir  Kind = "dir"
)

// Target is one file or directory to watch: Path is absolute, Key is what
// its changes are reported under.
type Target struct {
	Key  string
	Path string
}

// Change is one coalesced change.
type Change struct {
	Kind Kind
	Key  string
}

// MaxDirs caps the paths one Session watches.
const MaxDirs = 256

// MaxCost caps what one Session's watches cost, in the units its cost
// function reports. On kqueue (macOS, BSD) a watched directory holds one
// file descriptor for itself and one for each entry in it, so an expanded
// directory with tens of thousands of files would hold as many; the budget
// stops that. inotify (Linux) holds one watch per directory whatever its size.
const MaxCost = 4096

// DefaultDebounce is how long a Session gathers changes before reporting.
const DefaultDebounce = 150 * time.Millisecond

// Session watches one client's targets.
type Session struct {
	w        Watcher
	debounce time.Duration
	// cost is what watching a directory costs against MaxCost.
	cost func(dir string) int

	mu      sync.Mutex
	files   map[string]string // abs path → key
	dirs    map[string]string // abs path → key
	watched map[string]int    // abs path → cost
}

// NewSession returns a Session over w that reports changes a debounce
// after the first one, coalesced. It owns w and closes it when Run returns.
func NewSession(w Watcher, debounce time.Duration) *Session {
	return &Session{w: w, debounce: debounce, cost: dirCost, files: map[string]string{}, dirs: map[string]string{}, watched: map[string]int{}}
}

// Set replaces the watched targets. Files are watched through their
// directories, open files first, then directories in the order given, while
// MaxDirs and MaxCost allow. A file whose directory doesn't fit is watched
// by itself, for one descriptor. Paths that can't be watched (gone,
// unreadable, past the caps) are skipped: their targets just don't report.
func (s *Session) Set(files, dirs []Target) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.files = make(map[string]string, len(files))
	s.dirs = make(map[string]string, len(dirs))
	want := map[string]int{}
	measured := map[string]int{}
	used := 0
	add := func(p string, cost func() int) bool {
		if _, ok := want[p]; ok {
			return true
		}
		if len(want) >= MaxDirs {
			return false
		}
		c, ok := s.watched[p]
		if !ok {
			if c, ok = measured[p]; !ok {
				c = cost()
				measured[p] = c
			}
		}
		if used+c > MaxCost {
			return false
		}
		want[p] = c
		used += c
		return true
	}
	for _, f := range files {
		s.files[f.Path] = f.Key
		dir := filepath.Dir(f.Path)
		if !add(dir, func() int { return s.cost(dir) }) {
			add(f.Path, func() int { return 1 })
		}
	}
	for _, d := range dirs {
		s.dirs[d.Path] = d.Key
		add(d.Path, func() int { return s.cost(d.Path) })
	}
	for p := range s.watched {
		if _, ok := want[p]; !ok {
			_ = s.w.Remove(p)
			delete(s.watched, p)
		}
	}
	for p, c := range want {
		if _, ok := s.watched[p]; ok {
			continue
		}
		if s.w.Add(p) == nil {
			s.watched[p] = c
		}
	}
}

// rewatch watches a watched path again after it was removed or renamed:
// the watcher drops a watch then, and an atomic save leaves a new file
// under the same name (inotify doesn't follow it; kqueue does, and adding
// it again there is a no-op). A path that's gone is forgotten, so a later
// Set tries it again.
func (s *Session) rewatch(ev fsnotify.Event) {
	if !ev.Has(fsnotify.Remove) && !ev.Has(fsnotify.Rename) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.watched[ev.Name]; ok && s.w.Add(ev.Name) != nil {
		delete(s.watched, ev.Name)
	}
}

// countEntries counts dir's entries, stopping past limit.
func countEntries(dir string, limit int) int {
	f, err := os.Open(dir)
	if err != nil {
		return 0
	}
	defer f.Close()
	n := 0
	for n <= limit {
		names, err := f.Readdirnames(1024)
		n += len(names)
		if err != nil {
			break
		}
	}
	return n
}

// changesOf maps an event to the changes it makes to the targets.
func (s *Session) changesOf(ev fsnotify.Event) []Change {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Change
	if key, ok := s.files[ev.Name]; ok && ev.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove|fsnotify.Rename) != 0 {
		out = append(out, Change{Kind: KindFile, Key: key})
	}
	if key, ok := s.dirs[filepath.Dir(ev.Name)]; ok && ev.Op&(fsnotify.Create|fsnotify.Remove|fsnotify.Rename) != 0 {
		out = append(out, Change{Kind: KindDir, Key: key})
	}
	// kqueue also reports a directory's own Write when its entries change.
	if key, ok := s.dirs[ev.Name]; ok && ev.Op.Has(fsnotify.Write) {
		out = append(out, Change{Kind: KindDir, Key: key})
	}
	return out
}

// Run reports changes through emit, in batches a debounce after each
// batch's first change, until ctx ends or the watcher closes. It closes
// the watcher on return.
func (s *Session) Run(ctx context.Context, emit func(Change)) {
	defer s.w.Close()
	pending := map[Change]struct{}{}
	var order []Change
	var timer <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-s.w.Events():
			if !ok {
				return
			}
			s.rewatch(ev)
			for _, c := range s.changesOf(ev) {
				if _, dup := pending[c]; !dup {
					pending[c] = struct{}{}
					order = append(order, c)
				}
			}
			// The first change starts the wait; later ones ride along, so
			// a steady stream still reports every debounce.
			if len(pending) > 0 && timer == nil {
				timer = time.After(s.debounce)
			}
		case _, ok := <-s.w.Errors():
			if !ok {
				return
			}
		case <-timer:
			for _, c := range order {
				emit(c)
			}
			pending = map[Change]struct{}{}
			order = nil
			timer = nil
		}
	}
}
