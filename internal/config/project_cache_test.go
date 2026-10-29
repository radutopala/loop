package config

import (
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/osutil"
)

type ProjectCacheSuite struct {
	suite.Suite
	dir, path string
	cache     *projectCache
}

func TestProjectCacheSuite(t *testing.T) {
	suite.Run(t, new(ProjectCacheSuite))
}

func (s *ProjectCacheSuite) SetupTest() {
	s.dir = s.T().TempDir()
	s.path = filepath.Join(s.dir, ".loop", "config.json")
	require.NoError(s.T(), os.MkdirAll(filepath.Dir(s.path), 0o755))
	s.cache = newProjectCache()
}

func (s *ProjectCacheSuite) write(content string, mtime time.Time) {
	require.NoError(s.T(), os.WriteFile(s.path, []byte(content), 0o644))
	require.NoError(s.T(), os.Chtimes(s.path, mtime, mtime))
}

// TestRead: an unchanged file is neither read nor decoded again, the same
// content under a new stat isn't decoded again, and changed content is.
// Every caller gets its own copy.
func (s *ProjectCacheSuite) TestRead() {
	_, err := s.cache.read(s.dir, s.path)
	require.ErrorIs(s.T(), err, fs.ErrNotExist)

	at := time.Unix(1000, 0)
	s.write(`{"extra_dirs": ["/a"], "envs": {"K": "v"}}`, at)
	first, err := s.cache.read(s.dir, s.path)
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{"/a"}, first.ExtraDirs)
	first.ExtraDirs[0] = "/changed"
	first.Envs["K"] = "changed"

	// Planted in the cache: an unchanged stat returns it unread.
	cached := s.cache.files[s.path].pc
	cached.ClaudeModel = "planted"
	again, err := s.cache.read(s.dir, s.path)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "planted", again.ClaudeModel)
	require.Equal(s.T(), []string{"/a"}, again.ExtraDirs, "a caller's change stays its own")
	require.Equal(s.T(), "v", again.Envs["K"])

	// Same content, new stat: read, not decoded.
	s.write(`{"extra_dirs": ["/a"], "envs": {"K": "v"}}`, at.Add(time.Second))
	again, err = s.cache.read(s.dir, s.path)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "planted", again.ClaudeModel)
	require.Same(s.T(), cached, s.cache.files[s.path].pc)

	s.write(`{"extra_dirs": ["/b"]}`, at.Add(2*time.Second))
	changed, err := s.cache.read(s.dir, s.path)
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{"/b"}, changed.ExtraDirs)
	require.Empty(s.T(), changed.ClaudeModel)

	s.write(`{not hjson`, at.Add(3*time.Second))
	_, err = s.cache.read(s.dir, s.path)
	require.ErrorContains(s.T(), err, "parsing project config file")
	require.Equal(s.T(), []string{"/b"}, s.cache.files[s.path].pc.ExtraDirs, "a file that doesn't decode isn't kept")
}

// TestReadErrors: failures to open, stat or read the file are read errors.
func (s *ProjectCacheSuite) TestReadErrors() {
	closed, err := os.Open(s.dir)
	require.NoError(s.T(), err)
	require.NoError(s.T(), closed.Close())
	tests := []struct {
		name string
		open func(root, path string) (*os.File, error)
	}{
		{"open", func(string, string) (*os.File, error) { return nil, os.ErrPermission }},
		{"stat", func(string, string) (*os.File, error) { return closed, nil }},
		{"read", func(root, _ string) (*os.File, error) { return osutil.OpenFileIn(root, filepath.Dir(s.path)) }},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.cache.open = tc.open
			_, err := s.cache.read(s.dir, s.path)
			require.ErrorContains(s.T(), err, "reading project config file")
		})
	}
}

// TestNewProjectLoaderReadsThroughCache: a loader from NewProjectLoader
// reads project and worktree configs through its cache.
func (s *ProjectCacheSuite) TestNewProjectLoaderReadsThroughCache() {
	s.write(`{"extra_dirs": ["/x"]}`, time.Unix(1000, 0))
	l := NewProjectLoader(nil)
	merged, err := l.LoadProject(s.dir, &Config{})
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{"/x"}, merged.ExtraDirs)
	require.Contains(s.T(), l.projects.files, s.path)

	wt := filepath.Join(s.dir, ".worktrees", "wt")
	require.NoError(s.T(), os.MkdirAll(filepath.Join(wt, ".loop"), 0o755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(wt, ".loop", "config.json"), []byte(`{"extra_dirs": ["`+s.dir+`"]}`), 0o644))
	merged, err = l.LoadWorktreeProject(wt, s.dir, &Config{})
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{"/x", s.dir}, merged.ExtraDirs)
}

type copyInner struct{ N int }

type copyAll struct {
	P      *copyInner
	NilP   *copyInner
	S      []copyInner
	NilS   []string
	M      map[string]*copyInner
	NilM   map[string]int
	I      any
	NilI   any
	A      [2]*copyInner
	Str    string
	hidden int
}

// TestDeepCopy: the copy equals the original and shares nothing with it.
func (s *ProjectCacheSuite) TestDeepCopy() {
	src := copyAll{
		P:   &copyInner{1},
		S:   []copyInner{{2}},
		M:   map[string]*copyInner{"k": {3}},
		I:   map[string]any{"x": []any{"y"}},
		A:   [2]*copyInner{{4}, nil},
		Str: "s", hidden: 5,
	}
	var dst copyAll
	deepCopy(reflect.ValueOf(&dst).Elem(), reflect.ValueOf(src))
	require.Equal(s.T(), src, dst)
	require.NotSame(s.T(), src.P, dst.P)
	require.NotSame(s.T(), &src.S[0], &dst.S[0])
	require.NotSame(s.T(), src.M["k"], dst.M["k"])
	require.NotSame(s.T(), src.A[0], dst.A[0])
	dst.I.(map[string]any)["x"].([]any)[0] = "z"
	require.Equal(s.T(), "y", src.I.(map[string]any)["x"].([]any)[0])
	require.Nil(s.T(), dst.NilS)
	require.Nil(s.T(), dst.NilM)
}

// TestProjectConfigHasNoHiddenReferences: deepCopy copies unexported
// fields as they are, so none in projectConfig may refer to anything.
func (s *ProjectCacheSuite) TestProjectConfigHasNoHiddenReferences() {
	seen := map[reflect.Type]bool{}
	var walk func(t reflect.Type, exported bool)
	walk = func(t reflect.Type, exported bool) {
		switch t.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Map, reflect.Interface, reflect.Chan, reflect.Func, reflect.UnsafePointer:
			require.True(s.T(), exported, "unexported reference field of type %s", t)
		}
		if seen[t] {
			return
		}
		seen[t] = true
		switch t.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Array:
			walk(t.Elem(), exported)
		case reflect.Map:
			walk(t.Key(), exported)
			walk(t.Elem(), exported)
		case reflect.Struct:
			for i := range t.NumField() {
				walk(t.Field(i).Type, exported && t.Field(i).IsExported())
			}
		}
	}
	walk(reflect.TypeFor[projectConfig](), true)
}
