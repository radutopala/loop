package graph

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// DefaultMaxFiles caps the number of scannable files the engine will accept
// after exclusions. Repos over this size return RepoTooLargeError so the
// user owns the trade-off explicitly (raise the cap or add exclusions).
const DefaultMaxFiles = 25_000

// DefaultExcludePatterns drop the directories and file shapes that never
// belong in a structural metric: VCS metadata, dependency caches, build
// outputs, vendored deps, minified bundles, generated code. Trailing "/"
// marks a pattern as directory-only and prunes the subtree; the plain
// ".git" is a linked worktree's or submodule's .git file.
var DefaultExcludePatterns = []string{
	".git/",
	".git",
	".worktrees/",
	"node_modules/",
	"dist/",
	"build/",
	"target/",
	"vendor/",
	"*.min.js",
	"*.generated.go",
}

// EnumerateOptions tunes a single enumeration sweep.
type EnumerateOptions struct {
	// ExtraExcludePatterns appends to DefaultExcludePatterns. Same syntax:
	// trailing "/" = directory pattern (subtree pruned); else file glob
	// matched against the relative path or basename via doublestar.
	ExtraExcludePatterns []string

	// MaxFiles overrides DefaultMaxFiles. Zero uses the default; negative
	// disables the cap (intended for tests, never production).
	MaxFiles int
}

// RepoTooLargeError is returned by Enumerate when the unfiltered file
// count after exclusions exceeds the cap. The engine surfaces this as a
// structured response — the panel renders an empty state with a banner
// instructing the user to raise quality.max_files or extend exclusions.
type RepoTooLargeError struct {
	FileCount int
	Limit     int
}

func (e *RepoTooLargeError) Error() string {
	return fmt.Sprintf("repo too large to scan: %d files (cap %d)", e.FileCount, e.Limit)
}

// walkFunc is the directory walker abstraction the package depends on.
// Production uses filepath.WalkDir; tests inject fakes to provoke I/O
// error paths deterministically.
type walkFunc func(root string, fn fs.WalkDirFunc) error

// enumFS is the filesystem Enumerate reads. Production uses the os
// package; tests swap single fields.
type enumFS struct {
	walk     walkFunc
	readFile readFileFunc
	stat     statFunc
	abs      func(path string) (string, error)
}

func osEnumFS() enumFS {
	return enumFS{walk: filepath.WalkDir, readFile: os.ReadFile, stat: os.Stat, abs: filepath.Abs}
}

// Enumerate walks rootDir and returns slash-separated relative paths the
// engine should hand to the parser, applying the exclusion layers and the
// file-count cap. Exclusion layers: built-in defaults and
// opts.ExtraExcludePatterns, then git's: the repo's info/exclude and
// every .gitignore from the repo root down (including those above rootDir
// when it is a subdirectory), with git's anchoring, negation and
// precedence. The user's global excludes file (core.excludesFile) is not
// read. Scan order is deterministic (filepath.WalkDir).
func Enumerate(rootDir string, opts EnumerateOptions) ([]string, error) {
	return enumerate(osEnumFS(), rootDir, opts)
}

func enumerate(fsys enumFS, rootDir string, opts EnumerateOptions) ([]string, error) {
	limit := opts.MaxFiles
	if limit == 0 {
		limit = DefaultMaxFiles
	}

	// Ignore rules are keyed by their dir relative to the repo root, and
	// base is rootDir's place under it, so a scan of a subdirectory still
	// gets the .gitignore files above it.
	ignores, base := seedGitignores(fsys, rootDir)
	patterns := make([]string, 0, len(DefaultExcludePatterns)+len(opts.ExtraExcludePatterns))
	patterns = append(patterns, DefaultExcludePatterns...)
	patterns = append(patterns, opts.ExtraExcludePatterns...)

	var files []string
	err := fsys.walk(rootDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == rootDir {
			return nil
		}
		rel, err := filepath.Rel(rootDir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		isDir := d.IsDir()
		if matchExcluded(rel, isDir, patterns) || ignores.ignored(base+rel, isDir) {
			if isDir {
				return fs.SkipDir
			}
			return nil
		}
		if isDir {
			if nested := loadGitignore(fsys.readFile, filepath.Join(path, ".gitignore")); len(nested) > 0 {
				ignores[base+rel+"/"] = nested
			}
			return nil
		}
		files = append(files, rel)
		return nil
	})
	if err != nil {
		return nil, err
	}

	if limit >= 0 && len(files) > limit {
		return nil, &RepoTooLargeError{FileCount: len(files), Limit: limit}
	}
	return files, nil
}

// seedGitignores loads the rules that apply before the walk starts: the
// repo's info/exclude (lowest precedence) and the .gitignore of each dir
// from the repo root down to rootDir. Outside a repo, only rootDir's own
// .gitignore applies. base is rootDir relative to the repo root, slash
// terminated, or "" when rootDir is the root.
func seedGitignores(fsys enumFS, rootDir string) (gitignoreSet, string) {
	ignores := gitignoreSet{}
	abs, err := fsys.abs(rootDir)
	if err != nil {
		abs = rootDir
	}
	repo, ok := findRepo(fsys.stat, fsys.readFile, abs)
	if !ok {
		ignores[""] = loadGitignore(fsys.readFile, filepath.Join(rootDir, ".gitignore"))
		return ignores, ""
	}
	var rootRules []gitignoreRule
	if repo.CommonDir != "" {
		rootRules = loadGitignore(fsys.readFile, filepath.Join(repo.CommonDir, "info", "exclude"))
	}
	ignores[""] = append(rootRules, loadGitignore(fsys.readFile, filepath.Join(repo.Root, ".gitignore"))...)

	rel := strings.TrimLeft(filepath.ToSlash(strings.TrimPrefix(abs, repo.Root)), "/")
	if rel == "" {
		return ignores, ""
	}
	prefix := ""
	for seg := range strings.SplitSeq(rel, "/") {
		prefix += seg + "/"
		if rules := loadGitignore(fsys.readFile, filepath.Join(repo.Root, filepath.FromSlash(prefix), ".gitignore")); len(rules) > 0 {
			ignores[prefix] = rules
		}
	}
	return ignores, prefix
}

// matchExcluded reports whether rel matches one of the engine's own
// exclusion patterns (DefaultExcludePatterns, quality.exclude_paths).
func matchExcluded(rel string, isDir bool, patterns []string) bool {
	base := filepath.Base(rel)
	for _, p := range patterns {
		if matchPattern(rel, base, isDir, p) {
			return true
		}
	}
	return false
}

func matchPattern(rel, base string, isDir bool, pat string) bool {
	if name, ok := strings.CutSuffix(pat, "/"); ok {
		if !isDir {
			return false
		}
		if base == name {
			return true
		}
		ok, _ := doublestar.Match(name, rel)
		return ok
	}
	if isDir {
		return false
	}
	if ok, _ := doublestar.Match(pat, rel); ok {
		return true
	}
	ok, _ := doublestar.Match(pat, base)
	return ok
}
