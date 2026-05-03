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
		"src/foo.go":         "package foo",
		"src/bar.ts":         "export {};",
		"README.md":          "readme",
		"package.json":       "{}",
		"node_modules/x.js":  "skip me",
		"node_modules/sub/y": "skip me too",
		"dist/bundle.min.js": "skip me",
		"build/out.bin":      "skip",
		"target/cache":       "skip",
		"vendor/dep/dep.go":  "skip",
		".git/HEAD":          "ref: refs/heads/main",
		"foo.generated.go":   "// generated",
		"docs/page.md":       "doc",
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

func (s *EnumerateSuite) TestEnumerateGitignoreSkipsCommentsBlanksAndNegation() {
	root := s.T().TempDir()
	s.writeTree(root, map[string]string{
		"src/foo.go":      "package foo",
		"secrets/key.pem": "secret",
		// "!secrets/key.pem" would re-include in real git semantics, but v0
		// drops negation lines on purpose; the file stays excluded.
		".gitignore": "# top-level comment\n\n   \nsecrets/\n!secrets/key.pem\n",
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

// --- Walk error paths (injected) ---

func (s *EnumerateSuite) TestEnumerateNonexistentRoot() {
	_, err := Enumerate(filepath.Join(s.T().TempDir(), "does-not-exist"), EnumerateOptions{})
	require.Error(s.T(), err)
}

func (s *EnumerateSuite) TestEnumeratePropagatesWalkError() {
	bang := errors.New("walk boom")
	_, err := enumerate(func(_ string, fn fs.WalkDirFunc) error {
		return fn("/root", nil, bang)
	}, missingFileReader, "/root", EnumerateOptions{})
	require.ErrorIs(s.T(), err, bang)
}

func (s *EnumerateSuite) TestEnumeratePropagatesRelError() {
	// filepath.Rel errors when basepath is absolute and path is relative
	// (or vice-versa). Inject a walk that yields such a pair.
	_, err := enumerate(func(_ string, fn fs.WalkDirFunc) error {
		return fn("relative-path", fakeDir{name: "relative-path"}, nil)
	}, missingFileReader, "/abs/root", EnumerateOptions{})
	require.Error(s.T(), err)
}

func (s *EnumerateSuite) TestEnumeratePropagatesGitignoreReadError() {
	bang := errors.New("read boom")
	_, err := enumerate(filepath.WalkDir, func(_ string) ([]byte, error) {
		return nil, bang
	}, s.T().TempDir(), EnumerateOptions{})
	require.ErrorIs(s.T(), err, bang)
}

// --- parseGitignore direct cases for branch coverage ---

func (s *EnumerateSuite) TestParseGitignoreSlashOnlyLineDropped() {
	// "/" alone strips to "" and must be dropped to avoid an empty pattern
	// that would match everything in matchExcluded.
	patterns := parseGitignore("/\n")
	require.Empty(s.T(), patterns)
}

// --- matchExcluded direct cases for branch coverage ---

func (s *EnumerateSuite) TestMatchExcludedNoPatternsReturnsFalse() {
	matched, dirSkip := matchExcluded("foo.go", false, nil)
	require.False(s.T(), matched)
	require.False(s.T(), dirSkip)
}

func (s *EnumerateSuite) TestMatchExcludedDirPatternSkipsFiles() {
	// Dir pattern + non-dir entry → should not match.
	matched, _ := matchExcluded("node_modules", false, []string{"node_modules/"})
	require.False(s.T(), matched)
}

func (s *EnumerateSuite) TestMatchExcludedFilePatternSkipsDirs() {
	// File pattern + dir entry → should not match.
	matched, _ := matchExcluded("foo.min.js", true, []string{"*.min.js"})
	require.False(s.T(), matched)
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
