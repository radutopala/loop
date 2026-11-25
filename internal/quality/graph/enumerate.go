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
// marks a pattern as directory-only and prunes the subtree.
var DefaultExcludePatterns = []string{
	".git/",
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

// Enumerate walks rootDir and returns slash-separated relative paths the
// engine should hand to the parser, applying the exclusion layers and the
// file-count cap. Exclusion layers: built-in defaults and
// opts.ExtraExcludePatterns, then the repo's .gitignore files (the root
// one and nested ones, with git's anchoring, negation and precedence).
// Scan order is deterministic (filepath.WalkDir).
func Enumerate(rootDir string, opts EnumerateOptions) ([]string, error) {
	return enumerate(filepath.WalkDir, os.ReadFile, rootDir, opts)
}

func enumerate(walk walkFunc, readFile readFileFunc, rootDir string, opts EnumerateOptions) ([]string, error) {
	limit := opts.MaxFiles
	if limit == 0 {
		limit = DefaultMaxFiles
	}

	rootRules, err := loadGitignore(readFile, filepath.Join(rootDir, ".gitignore"))
	if err != nil {
		return nil, err
	}
	ignores := gitignoreSet{"": rootRules}
	patterns := make([]string, 0, len(DefaultExcludePatterns)+len(opts.ExtraExcludePatterns))
	patterns = append(patterns, DefaultExcludePatterns...)
	patterns = append(patterns, opts.ExtraExcludePatterns...)

	var files []string
	err = walk(rootDir, func(path string, d fs.DirEntry, walkErr error) error {
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
		if matchExcluded(rel, isDir, patterns) || ignores.ignored(rel, isDir) {
			if isDir {
				return fs.SkipDir
			}
			return nil
		}
		if isDir {
			// An unreadable nested .gitignore is skipped, as git does,
			// rather than failing the whole scan.
			if nested, err := loadGitignore(readFile, filepath.Join(path, ".gitignore")); err == nil && len(nested) > 0 {
				ignores[rel+"/"] = nested
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
