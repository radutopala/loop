package graph

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type EnumerateSuite struct {
	suite.Suite
}

func TestEnumerateSuite(t *testing.T) {
	suite.Run(t, new(EnumerateSuite))
}

// writeTree materialises a map of relative-path → contents under root.
func (s *EnumerateSuite) writeTree(root string, files map[string]string) {
	for rel, contents := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		require.NoError(s.T(), os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(s.T(), os.WriteFile(full, []byte(contents), 0o644))
	}
}

// --- Happy path + default exclusions ---

func (s *EnumerateSuite) TestEnumerateHappyPathWithDefaults() {
	root := s.T().TempDir()
	s.writeTree(root, map[string]string{
		"src/foo.go":                    "package foo",
		"src/bar.ts":                    "export {};",
		"README.md":                     "readme",
		"package.json":                  "{}",
		"node_modules/x.js":             "skip me",
		"node_modules/sub/y":            "skip me too",
		"dist/bundle.min.js":            "skip me",
		"build/out.bin":                 "skip",
		"target/cache":                  "skip",
		"vendor/dep/dep.go":             "skip",
		"pkg/__pycache__/m.cpython.pyc": "skip",
		".venv/lib/site.py":             "skip",
		"venv/lib/site.py":              "skip",
		".tox/py312/x.py":               "skip",
		".mypy_cache/3.12/m.json":       "skip",
		".pytest_cache/v/cache":         "skip",
		".git/HEAD":                     "ref: refs/heads/main",
		".worktrees/wt-a/internal/x.go": "skip",
		"foo.generated.go":              "// generated",
		"docs/page.md":                  "doc",
	})

	files, err := Enumerate(root, EnumerateOptions{})
	require.NoError(s.T(), err)
	sort.Strings(files)
	require.Equal(s.T(), []string{
		"README.md",
		"docs/page.md",
		"package.json",
		"src/bar.ts",
		"src/foo.go",
	}, files)
}

func (s *EnumerateSuite) TestEnumerateMinifiedFileGlobAtAnyDepth() {
	root := s.T().TempDir()
	s.writeTree(root, map[string]string{
		"web/static/app.min.js": "skip",
		"web/static/app.js":     "keep",
	})

	files, err := Enumerate(root, EnumerateOptions{})
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{"web/static/app.js"}, files)
}

// --- Extra user exclusions ---

func (s *EnumerateSuite) TestEnumerateExtraDirExclusion() {
	root := s.T().TempDir()
	s.writeTree(root, map[string]string{
		"src/foo.go":             "package foo",
		"internal/legacy/old.go": "package legacy",
	})

	files, err := Enumerate(root, EnumerateOptions{
		ExtraExcludePatterns: []string{"legacy/"},
	})
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{"src/foo.go"}, files)
}

func (s *EnumerateSuite) TestEnumerateExtraFileGlobExclusion() {
	root := s.T().TempDir()
	s.writeTree(root, map[string]string{
		"src/foo.go":      "package foo",
		"src/foo_test.go": "package foo",
	})

	files, err := Enumerate(root, EnumerateOptions{
		ExtraExcludePatterns: []string{"*_test.go"},
	})
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{"src/foo.go"}, files)
}

func (s *EnumerateSuite) TestEnumerateDirPatternMatchesPath() {
	root := s.T().TempDir()
	s.writeTree(root, map[string]string{
		"a/b/c/x.go": "skip",
		"a/b/d/y.go": "keep",
	})

	files, err := Enumerate(root, EnumerateOptions{
		ExtraExcludePatterns: []string{"a/b/c/"},
	})
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{"a/b/d/y.go"}, files)
}

// --- File-count cap ---

func (s *EnumerateSuite) TestEnumerateRepoTooLarge() {
	root := s.T().TempDir()
	s.writeTree(root, map[string]string{
		"a.go": "", "b.go": "", "c.go": "",
	})

	_, err := Enumerate(root, EnumerateOptions{MaxFiles: 2})
	var tooLarge *RepoTooLargeError
	require.ErrorAs(s.T(), err, &tooLarge)
	require.Equal(s.T(), 3, tooLarge.FileCount)
	require.Equal(s.T(), 2, tooLarge.Limit)
	require.Equal(s.T(), "repo too large to scan: 3 files (cap 2)", tooLarge.Error())
}

func (s *EnumerateSuite) TestEnumerateMaxFilesNegativeDisablesCap() {
	root := s.T().TempDir()
	s.writeTree(root, map[string]string{"a.go": "", "b.go": ""})

	files, err := Enumerate(root, EnumerateOptions{MaxFiles: -1})
	require.NoError(s.T(), err)
	require.Len(s.T(), files, 2)
}

// --- .gitignore layer ---

func (s *EnumerateSuite) TestEnumerateAppliesGitignorePatterns() {
	root := s.T().TempDir()
	s.writeTree(root, map[string]string{
		"src/foo.go":      "package foo",
		"secrets/key.pem": "secret",
		"out/binary":      "binary",
		".gitignore":      "secrets/\nout/\n",
	})

	files, err := Enumerate(root, EnumerateOptions{})
	require.NoError(s.T(), err)
	require.Contains(s.T(), files, "src/foo.go")
	require.NotContains(s.T(), files, "secrets/key.pem")
	require.NotContains(s.T(), files, "out/binary")
}

func (s *EnumerateSuite) TestEnumerateGitignoreSkipsCommentsAndBlanks() {
	root := s.T().TempDir()
	s.writeTree(root, map[string]string{
		"src/foo.go":      "package foo",
		"secrets/key.pem": "secret",
		".gitignore":      "# top-level comment\n\n   \nsecrets/\n",
	})

	files, err := Enumerate(root, EnumerateOptions{})
	require.NoError(s.T(), err)
	require.Contains(s.T(), files, "src/foo.go")
	require.NotContains(s.T(), files, "secrets/key.pem")
}

func (s *EnumerateSuite) TestEnumerateGitignoreStripsLeadingSlash() {
	root := s.T().TempDir()
	s.writeTree(root, map[string]string{
		"build/bin":  "out",
		"src/foo.go": "package foo",
		".gitignore": "/build/\n",
	})

	files, err := Enumerate(root, EnumerateOptions{})
	require.NoError(s.T(), err)
	require.Contains(s.T(), files, "src/foo.go")
	require.NotContains(s.T(), files, "build/bin")
}

func (s *EnumerateSuite) TestEnumerateNestedGitignoreScopedToSubtree() {
	root := s.T().TempDir()
	s.writeTree(root, map[string]string{
		"app/.gitignore":        ".vite/\n*.log\n",
		"app/.vite/deps/x.js":   "skip",
		"app/debug.log":         "skip",
		"app/src/main.ts":       "keep",
		"other/.vite/keep.js":   "keep",
		"other/keep.log":        "keep",
		"docs/.gitignore":       "public/\n",
		"docs/public/index.htm": "skip",
		"docs/guide.md":         "keep",
	})

	files, err := Enumerate(root, EnumerateOptions{})
	require.NoError(s.T(), err)
	sort.Strings(files)
	require.Equal(s.T(), []string{
		"app/.gitignore",
		"app/src/main.ts",
		"docs/.gitignore",
		"docs/guide.md",
		"other/.vite/keep.js",
		"other/keep.log",
	}, files)
}

// TestEnumerateGitignoreSemantics checks git's matching rules across root
// and nested .gitignore files.
func (s *EnumerateSuite) TestEnumerateGitignoreSemantics() {
	tests := []struct {
		name  string
		files map[string]string
		want  []string
	}{
		{
			name: "root anchored pattern matches only at the root",
			files: map[string]string{
				".gitignore":   "/gen/\n",
				"gen/a.go":     "",
				"pkg/gen/b.go": "",
				"pkg/keep.go":  "",
			},
			want: []string{".gitignore", "pkg/gen/b.go", "pkg/keep.go"},
		},
		{
			name: "nested anchored pattern matches only in its dir",
			files: map[string]string{
				"app/.gitignore":      "/gen/\n",
				"app/gen/a.go":        "",
				"app/src/gen/real.go": "",
			},
			want: []string{"app/.gitignore", "app/src/gen/real.go"},
		},
		{
			name: "pattern with a middle slash is anchored",
			files: map[string]string{
				".gitignore":     "web/out\n",
				"web/out/a.js":   "",
				"x/web/out/b.js": "",
			},
			want: []string{".gitignore", "x/web/out/b.js"},
		},
		{
			name: "unanchored pattern matches at any depth",
			files: map[string]string{
				"pkg/.gitignore": "*.log\n",
				"pkg/a.log":      "",
				"pkg/x/y/b.log":  "",
				"pkg/c.go":       "",
				"top.log":        "",
			},
			want: []string{"pkg/.gitignore", "pkg/c.go", "top.log"},
		},
		{
			name: "bare name prunes a directory",
			files: map[string]string{
				"svc/.gitignore":     "generated\n",
				"svc/generated/x.go": "",
				"svc/main.go":        "",
			},
			want: []string{"svc/.gitignore", "svc/main.go"},
		},
		{
			name: "negation re-includes, last match wins",
			files: map[string]string{
				"lib/.gitignore": "*.txt\n!keep.txt\n",
				"lib/drop.txt":   "",
				"lib/keep.txt":   "",
			},
			want: []string{"lib/.gitignore", "lib/keep.txt"},
		},
		{
			name: "deeper file overrides the root",
			files: map[string]string{
				".gitignore":   "*.gen.go\n",
				"a/.gitignore": "!*.gen.go\n",
				"a/x.gen.go":   "",
				"b/y.gen.go":   "",
			},
			want: []string{".gitignore", "a/.gitignore", "a/x.gen.go"},
		},
		{
			name: "dir-only pattern leaves files of that name",
			files: map[string]string{
				".gitignore": "cache/\n",
				"cache/a.go": "",
				"x/cache":    "",
			},
			want: []string{".gitignore", "x/cache"},
		},
		{
			name: "backslash escapes a leading hash or bang",
			files: map[string]string{
				".gitignore": "\\#notes\n\\!bang\n",
				"#notes":     "",
				"!bang":      "",
				"keep":       "",
			},
			want: []string{".gitignore", "keep"},
		},
		{
			name: "other backslash escapes stay literal",
			files: map[string]string{
				".gitignore": "\\*.tmp\n",
				"*.tmp":      "",
				"a.tmp":      "",
			},
			want: []string{".gitignore", "a.tmp"},
		},
		{
			name: "dir/** ignores the contents, so a negation re-includes",
			files: map[string]string{
				".gitignore":         "generated/**\n!generated/keep.go\n",
				"generated/drop.go":  "",
				"generated/keep.go":  "",
				"generated/x/y.go":   "",
				"other/generated.go": "",
			},
			want: []string{".gitignore", "generated/keep.go", "other/generated.go"},
		},
		{
			name: "leading spaces are part of the pattern",
			files: map[string]string{
				".gitignore": "  foo.go\n",
				"foo.go":     "",
				"  foo.go":   "",
			},
			want: []string{".gitignore", "foo.go"},
		},
		{
			name: "an escaped trailing space is kept",
			files: map[string]string{
				".gitignore": "bar\\ \nbaz   \n",
				"bar" + " ":  "", // the file name ends in a space
				"bar":        "",
				"baz":        "",
			},
			want: []string{".gitignore", "bar"},
		},
		{
			name: "braces are literal",
			files: map[string]string{
				".gitignore": "*.{js,ts}\n",
				"a.js":       "",
				"b.ts":       "",
				"x.{js,ts}":  "",
			},
			want: []string{".gitignore", "a.js", "b.ts"},
		},
		{
			name: "CRLF line endings",
			files: map[string]string{
				".gitignore": "*.log\r\n",
				"a.log":      "",
				"a.go":       "",
			},
			want: []string{".gitignore", "a.go"},
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			root := s.T().TempDir()
			s.writeTree(root, tc.files)
			files, err := Enumerate(root, EnumerateOptions{})
			require.NoError(s.T(), err)
			sort.Strings(files)
			require.Equal(s.T(), tc.want, files)
		})
	}
}

func (s *EnumerateSuite) TestEnumerateSkipsUnreadableNestedGitignore() {
	root := s.T().TempDir()
	s.writeTree(root, map[string]string{
		"sub/file.go": "package sub",
	})
	fsys := osEnumFS()
	fsys.readFile = func(name string) ([]byte, error) {
		if filepath.Base(filepath.Dir(name)) == "sub" {
			return nil, errors.New("permission denied")
		}
		return nil, fs.ErrNotExist
	}
	files, err := enumerate(fsys, root, EnumerateOptions{})
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{"sub/file.go"}, files)
}

// TestEnumerateSkipsUnreadableRootGitignore checks that the root
// .gitignore is skipped like a nested one when it can't be read.
func (s *EnumerateSuite) TestEnumerateSkipsUnreadableRootGitignore() {
	root := s.T().TempDir()
	s.writeTree(root, map[string]string{"a.go": ""})
	fsys := osEnumFS()
	fsys.readFile = func(string) ([]byte, error) { return nil, errors.New("read boom") }
	files, err := enumerate(fsys, root, EnumerateOptions{})
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{"a.go"}, files)
}

// TestEnumerateAppliesRepoIgnoresAboveRoot scans a subdirectory of a repo
// and checks that info/exclude and the .gitignore files above it apply.
func (s *EnumerateSuite) TestEnumerateAppliesRepoIgnoresAboveRoot() {
	repo := s.T().TempDir()
	s.writeTree(repo, map[string]string{
		".git/info/exclude":           "*.local\n",
		".gitignore":                  "*.pb.go\n/internal/api/gen/\n!keep.local\n",
		"internal/.gitignore":         "*.tmp\n",
		"internal/api/a.go":           "",
		"internal/api/a.pb.go":        "",
		"internal/api/b.tmp":          "",
		"internal/api/c.local":        "",
		"internal/api/keep.local":     "",
		"internal/api/gen/x.go":       "",
		"internal/api/sub/gen/y.go":   "",
		"internal/api/sub/.gitignore": "/z.go\n",
		"internal/api/sub/z.go":       "",
	})
	files, err := Enumerate(filepath.Join(repo, "internal", "api"), EnumerateOptions{})
	require.NoError(s.T(), err)
	sort.Strings(files)
	require.Equal(s.T(), []string{"a.go", "keep.local", "sub/.gitignore", "sub/gen/y.go"}, files)
}

// TestEnumerateReadsWorktreeCommonExclude scans a linked worktree, whose
// info/exclude lives in the main checkout's .git dir.
func (s *EnumerateSuite) TestEnumerateReadsWorktreeCommonExclude() {
	top := s.T().TempDir()
	s.writeTree(top, map[string]string{
		"main/.git/info/exclude":           "*.local\n",
		"main/.git/worktrees/wt/commondir": "../..\n",
		"wt/.git":                          "gitdir: ../main/.git/worktrees/wt\n",
		"wt/a.go":                          "",
		"wt/b.local":                       "",
	})
	files, err := Enumerate(filepath.Join(top, "wt"), EnumerateOptions{})
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{"a.go"}, files)
}

// TestEnumerateFallsBackWhenAbsFails checks that an unresolvable root is
// scanned as given, with only its own .gitignore.
func (s *EnumerateSuite) TestEnumerateFallsBackWhenAbsFails() {
	root := s.T().TempDir()
	s.writeTree(root, map[string]string{".gitignore": "*.log\n", "a.go": "", "b.log": ""})
	fsys := osEnumFS()
	fsys.abs = func(string) (string, error) { return "", errors.New("no cwd") }
	fsys.stat = func(string) (fs.FileInfo, error) { return nil, fs.ErrNotExist }
	files, err := enumerate(fsys, root, EnumerateOptions{})
	require.NoError(s.T(), err)
	sort.Strings(files)
	require.Equal(s.T(), []string{".gitignore", "a.go"}, files)
}

// --- Walk error paths (injected) ---

func (s *EnumerateSuite) TestEnumerateNonexistentRoot() {
	_, err := Enumerate(filepath.Join(s.T().TempDir(), "does-not-exist"), EnumerateOptions{})
	require.Error(s.T(), err)
}

func (s *EnumerateSuite) TestEnumeratePropagatesWalkError() {
	bang := errors.New("walk boom")
	fsys := osEnumFS()
	fsys.walk = func(_ string, fn fs.WalkDirFunc) error {
		return fn("/root", nil, bang)
	}
	fsys.readFile = missingFileReader
	_, err := enumerate(fsys, "/root", EnumerateOptions{})
	require.ErrorIs(s.T(), err, bang)
}

func (s *EnumerateSuite) TestEnumeratePropagatesRelError() {
	// filepath.Rel errors when basepath is absolute and path is relative
	// (or vice-versa). Inject a walk that yields such a pair.
	fsys := osEnumFS()
	fsys.walk = func(_ string, fn fs.WalkDirFunc) error {
		return fn("relative-path", fakeDir{name: "relative-path"}, nil)
	}
	fsys.readFile = missingFileReader
	_, err := enumerate(fsys, "/abs/root", EnumerateOptions{})
	require.Error(s.T(), err)
}

// --- parser and matcher direct cases ---

func (s *EnumerateSuite) TestParseGitignore() {
	require.Equal(s.T(), []gitignoreRule{
		{pattern: "build", dirOnly: true, anchored: true},
		{pattern: "*.log"},
		{pattern: "keep.log", negate: true},
		{pattern: "a/b", anchored: true},
	}, parseGitignore("/build/\n*.log\n!keep.log\na/b\n/\n!\n#c\n   \n"))
}

func (s *EnumerateSuite) TestTrimTrailingSpaces() {
	tests := []struct{ in, want string }{
		{"foo", "foo"},
		{"foo   ", "foo"},
		{`foo\ `, `foo\ `},
		{`foo\  `, `foo\ `},
		{`foo\\ `, `foo\\`},
		{"  foo", "  foo"},
	}
	for _, tc := range tests {
		s.Run(tc.in, func() {
			require.Equal(s.T(), tc.want, trimTrailingSpaces(tc.in))
		})
	}
}

func (s *EnumerateSuite) TestEscapeBraces() {
	tests := []struct{ in, want string }{
		{"*.go", "*.go"},
		{"*.{js,ts}", `*.\{js,ts\}`},
		{`a\{b}`, `a\{b\}`},
		{`x{\`, `x\{\`},
	}
	for _, tc := range tests {
		s.Run(tc.in, func() {
			require.Equal(s.T(), tc.want, escapeBraces(tc.in))
		})
	}
}

func (s *EnumerateSuite) TestMatchExcluded() {
	tests := []struct {
		name    string
		rel     string
		isDir   bool
		pattern string
		want    bool
	}{
		{"no patterns", "foo.go", false, "", false},
		{"dir pattern skips files", "node_modules", false, "node_modules/", false},
		{"file pattern skips dirs", "foo.min.js", true, "*.min.js", false},
		{"dir pattern by path", "a/b/c", true, "a/b/c/", true},
		{"file glob by basename", "x/y.min.js", false, "*.min.js", true},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			var pats []string
			if tc.pattern != "" {
				pats = []string{tc.pattern}
			}
			require.Equal(s.T(), tc.want, matchExcluded(tc.rel, tc.isDir, pats))
		})
	}
}

// missingFileReader stands in for os.ReadFile when the test wants the
// gitignore layer to behave as if no file is present.
func missingFileReader(_ string) ([]byte, error) {
	return nil, fs.ErrNotExist
}

// fakeDir is a minimal fs.DirEntry that lets injected walks satisfy the
// visitor signature without touching the filesystem.
type fakeDir struct {
	name  string
	isDir bool
}

func (f fakeDir) Name() string               { return f.name }
func (f fakeDir) IsDir() bool                { return f.isDir }
func (f fakeDir) Type() fs.FileMode          { return 0 }
func (f fakeDir) Info() (fs.FileInfo, error) { return nil, nil }
