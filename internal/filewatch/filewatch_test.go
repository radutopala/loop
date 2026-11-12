package filewatch

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// fakeWatcher records Add/Remove and feeds the events a test sends.
type fakeWatcher struct {
	mu      sync.Mutex
	added   map[string]int
	removed []string
	failAdd map[string]bool
	events  chan fsnotify.Event
	errs    chan error
	closed  bool
}

func newFakeWatcher() *fakeWatcher {
	return &fakeWatcher{added: map[string]int{}, failAdd: map[string]bool{}, events: make(chan fsnotify.Event, 16), errs: make(chan error, 1)}
}

func (w *fakeWatcher) Add(name string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.failAdd[name] {
		return errors.New("no such dir")
	}
	w.added[name]++
	return nil
}

func (w *fakeWatcher) Remove(name string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.removed = append(w.removed, name)
	return nil
}

func (w *fakeWatcher) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
	return nil
}

func (w *fakeWatcher) Events() <-chan fsnotify.Event { return w.events }
func (w *fakeWatcher) Errors() <-chan error          { return w.errs }

type FilewatchSuite struct {
	suite.Suite
	w *fakeWatcher
	s *Session
}

func TestFilewatchSuite(t *testing.T) { suite.Run(t, new(FilewatchSuite)) }

func (s *FilewatchSuite) SetupTest() {
	s.w = newFakeWatcher()
	s.s = NewSession(s.w, time.Millisecond)
	s.s.cost = func(string) int { return 1 }
}

func (s *FilewatchSuite) TestSetWatchesDirsAndFileParents() {
	s.s.Set([]Target{{Key: "f1", Path: "/r/a/x.go"}, {Key: "f2", Path: "/r/a/y.go"}}, []Target{{Key: "d", Path: "/r"}})
	require.Equal(s.T(), map[string]int{"/r/a": 1, "/r": 1}, s.w.added)

	// The same set again adds nothing; dropping a file stops its dir.
	s.s.Set([]Target{{Key: "f1", Path: "/r/a/x.go"}, {Key: "f2", Path: "/r/a/y.go"}}, []Target{{Key: "d", Path: "/r"}})
	require.Equal(s.T(), map[string]int{"/r/a": 1, "/r": 1}, s.w.added)
	s.s.Set(nil, []Target{{Key: "d", Path: "/r"}})
	require.Equal(s.T(), []string{"/r/a"}, s.w.removed)
}

func (s *FilewatchSuite) TestSetSkipsUnwatchableAndCaps() {
	s.w.failAdd["/gone"] = true
	s.s.Set(nil, []Target{{Key: "g", Path: "/gone"}})
	require.Empty(s.T(), s.w.added)

	dirs := make([]Target, MaxDirs+10)
	for i := range dirs {
		dirs[i] = Target{Key: fmt.Sprint(i), Path: fmt.Sprintf("/d%d", i)}
	}
	s.s.Set(nil, dirs)
	require.Len(s.T(), s.w.added, MaxDirs)
}

func (s *FilewatchSuite) TestSetBudget() {
	costs := map[string]int{"/big": MaxCost, "/r/huge": MaxCost + 1, "/r": 10, "/r/a": 20}
	calls := map[string]int{}
	s.s.cost = func(dir string) int {
		calls[dir]++
		return costs[dir]
	}

	// Open files come first: x.go's dir fits, huge's files are watched one
	// by one, and /big no longer fits after them.
	files := []Target{{Key: "x", Path: "/r/a/x.go"}, {Key: "h1", Path: "/r/huge/1"}, {Key: "h2", Path: "/r/huge/2"}}
	s.s.Set(files, []Target{{Key: "big", Path: "/big"}, {Key: "r", Path: "/r"}})
	require.Equal(s.T(), map[string]int{"/r/a": 1, "/r/huge/1": 1, "/r/huge/2": 1, "/r": 1}, s.w.added)
	require.Equal(s.T(), map[string]int{"/r/a": 20, "/r/huge/1": 1, "/r/huge/2": 1, "/r": 10}, s.s.watched)

	// A dir is measured once per Set, and not again while it's watched.
	s.s.Set(files, []Target{{Key: "r", Path: "/r"}})
	require.Equal(s.T(), map[string]int{"/r/a": 1, "/r/huge": 2, "/big": 1, "/r": 1}, calls)

	// Closing the files frees room for /big.
	s.s.Set(nil, []Target{{Key: "big", Path: "/big"}})
	require.Equal(s.T(), map[string]int{"/big": MaxCost}, s.s.watched)
	require.ElementsMatch(s.T(), []string{"/r/a", "/r/huge/1", "/r/huge/2", "/r"}, s.w.removed)
}

func (s *FilewatchSuite) TestRewatch() {
	s.s.Set([]Target{{Key: "f", Path: "/r/x.go"}}, nil)
	tests := []struct {
		name    string
		op      fsnotify.Op
		gone    bool
		added   int
		watched bool
	}{
		{"a write leaves the watch alone", fsnotify.Write, false, 1, true},
		{"a replaced path is watched again", fsnotify.Rename, false, 2, true},
		{"a removed path is forgotten", fsnotify.Remove, true, 2, false},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.w.failAdd["/r"] = tt.gone
			s.s.rewatch(fsnotify.Event{Name: "/r", Op: tt.op})
			require.Equal(s.T(), tt.added, s.w.added["/r"])
			_, ok := s.s.watched["/r"]
			require.Equal(s.T(), tt.watched, ok)
		})
	}
	// An unwatched path is ignored.
	s.s.rewatch(fsnotify.Event{Name: "/elsewhere", Op: fsnotify.Remove})
	require.Zero(s.T(), s.w.added["/elsewhere"])
}

func (s *FilewatchSuite) TestCountEntries() {
	dir := s.T().TempDir()
	for i := range 5 {
		require.NoError(s.T(), os.WriteFile(filepath.Join(dir, fmt.Sprint(i)), nil, 0o644))
	}
	require.Equal(s.T(), 5, countEntries(dir, MaxCost))
	require.Equal(s.T(), 0, countEntries(filepath.Join(dir, "missing"), MaxCost))
	require.Positive(s.T(), dirCost(dir))
}

func (s *FilewatchSuite) TestChangesOf() {
	s.s.Set([]Target{{Key: "f", Path: "/r/a/x.go"}}, []Target{{Key: "d", Path: "/r/a"}})
	tests := []struct {
		name string
		ev   fsnotify.Event
		want []Change
	}{
		{"write to the file", fsnotify.Event{Name: "/r/a/x.go", Op: fsnotify.Write}, []Change{{KindFile, "f"}}},
		{"file replaced by rename", fsnotify.Event{Name: "/r/a/x.go", Op: fsnotify.Create}, []Change{{KindFile, "f"}, {KindDir, "d"}}},
		{"file removed", fsnotify.Event{Name: "/r/a/x.go", Op: fsnotify.Remove}, []Change{{KindFile, "f"}, {KindDir, "d"}}},
		{"chmod is ignored", fsnotify.Event{Name: "/r/a/x.go", Op: fsnotify.Chmod}, nil},
		{"new entry in the dir", fsnotify.Event{Name: "/r/a/new.txt", Op: fsnotify.Create}, []Change{{KindDir, "d"}}},
		{"write to another file", fsnotify.Event{Name: "/r/a/other.txt", Op: fsnotify.Write}, nil},
		{"dir's own write (kqueue)", fsnotify.Event{Name: "/r/a", Op: fsnotify.Write}, []Change{{KindDir, "d"}}},
		{"unwatched path", fsnotify.Event{Name: "/elsewhere/z", Op: fsnotify.Create}, nil},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			require.Equal(s.T(), tt.want, s.s.changesOf(tt.ev))
		})
	}
}

func (s *FilewatchSuite) TestRunCoalescesAndStops() {
	s.s.Set([]Target{{Key: "f", Path: "/r/x.go"}}, []Target{{Key: "d", Path: "/r"}})
	got := make(chan Change, 8)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.s.Run(ctx, func(c Change) { got <- c })
		close(done)
	}()

	s.w.errs <- errors.New("overflow")
	s.w.events <- fsnotify.Event{Name: "/r/x.go", Op: fsnotify.Write}
	s.w.events <- fsnotify.Event{Name: "/r/x.go", Op: fsnotify.Write}
	s.w.events <- fsnotify.Event{Name: "/r/new", Op: fsnotify.Create}
	s.w.events <- fsnotify.Event{Name: "/r/other", Op: fsnotify.Write}
	require.Equal(s.T(), Change{KindFile, "f"}, <-got)
	require.Equal(s.T(), Change{KindDir, "d"}, <-got)

	// A later batch reports again.
	s.w.events <- fsnotify.Event{Name: "/r/x.go", Op: fsnotify.Write}
	require.Equal(s.T(), Change{KindFile, "f"}, <-got)

	cancel()
	<-done
	require.True(s.T(), s.w.closed)
	require.Empty(s.T(), got)
}

func (s *FilewatchSuite) TestRunEndsWhenChannelsClose() {
	for _, closeEvents := range []bool{true, false} {
		w := newFakeWatcher()
		if closeEvents {
			close(w.events)
		} else {
			close(w.errs)
		}
		NewSession(w, time.Millisecond).Run(context.Background(), func(Change) {})
		require.True(s.T(), w.closed)
	}
}

func (s *FilewatchSuite) TestWrapError() {
	w, err := wrap(nil, errors.New("too many open files"))
	require.Nil(s.T(), w)
	require.EqualError(s.T(), err, "too many open files")
}

// TestRealWatcher runs a Session over fsnotify on a temp dir.
func (s *FilewatchSuite) TestRealWatcher() {
	dir := s.T().TempDir()
	file := filepath.Join(dir, "a.txt")
	require.NoError(s.T(), os.WriteFile(file, []byte("1"), 0o644))

	w, err := New()
	require.NoError(s.T(), err)
	sess := NewSession(w, 20*time.Millisecond)
	sess.Set([]Target{{Key: "a", Path: file}}, []Target{{Key: "root", Path: dir}})

	got := make(chan Change, 8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sess.Run(ctx, func(c Change) { got <- c })

	require.NoError(s.T(), os.WriteFile(file, []byte("2"), 0o644))
	s.waitFor(got, Change{KindFile, "a"})
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, "b.txt"), []byte("x"), 0o644))
	s.waitFor(got, Change{KindDir, "root"})
}

// TestRealWatcherFileFallback watches a file by itself, its dir over budget,
// through an atomic replace and the writes after it.
func (s *FilewatchSuite) TestRealWatcherFileFallback() {
	dir := s.T().TempDir()
	file := filepath.Join(dir, "a.txt")
	require.NoError(s.T(), os.WriteFile(file, []byte("1"), 0o644))

	w, err := New()
	require.NoError(s.T(), err)
	sess := NewSession(w, 20*time.Millisecond)
	sess.cost = func(string) int { return MaxCost + 1 }
	sess.Set([]Target{{Key: "a", Path: file}}, nil)
	require.Equal(s.T(), map[string]int{file: 1}, sess.watched)

	got := make(chan Change, 8)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sess.Run(ctx, func(c Change) { got <- c })

	tmp := filepath.Join(dir, "a.txt.tmp")
	require.NoError(s.T(), os.WriteFile(tmp, []byte("2"), 0o644))
	require.NoError(s.T(), os.Rename(tmp, file))
	s.waitFor(got, Change{KindFile, "a"})
	// The replace took a moment to rewatch; give it that before writing again.
	time.Sleep(100 * time.Millisecond)
	require.NoError(s.T(), os.WriteFile(file, []byte("3"), 0o644))
	s.waitFor(got, Change{KindFile, "a"})
}

func (s *FilewatchSuite) waitFor(got <-chan Change, want Change) {
	deadline := time.After(5 * time.Second)
	for {
		select {
		case c := <-got:
			if c == want {
				return
			}
		case <-deadline:
			s.FailNow("no change", "%v", want)
		}
	}
}
