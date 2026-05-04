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
// file-count cap. Exclusion layers, in order: built-in defaults,
// rootDir/.gitignore (if present), opts.ExtraExcludePatterns. Scan order
// is deterministic (filepath.WalkDir).
func Enumerate(rootDir string, opts EnumerateOptions) ([]string, error) {
	return enumerate(filepath.WalkDir, os.ReadFile, rootDir, opts)
}

func enumerate(walk walkFunc, readFile readFileFunc, rootDir string, opts EnumerateOptions) ([]string, error) {
	limit := opts.MaxFiles
	if limit == 0 {
		limit = DefaultMaxFiles
	}

	gitignorePatterns, err := loadGitignorePatterns(readFile, filepath.Join(rootDir, ".gitignore"))
	if err != nil {
		return nil, err
	}

	patterns := make([]string, 0, len(DefaultExcludePatterns)+len(gitignorePatterns)+len(opts.ExtraExcludePatterns))
	patterns = append(patterns, DefaultExcludePatterns...)
	patterns = append(patterns, gitignorePatterns...)
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
		match, dirSkip := matchExcluded(rel, d.IsDir(), patterns)
		if match {
			if dirSkip {
				return fs.SkipDir
			}
			return nil
		}
		if !d.IsDir() {
			files = append(files, rel)
		}
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

func matchExcluded(rel string, isDir bool, patterns []string) (matched, dirSkip bool) {
	base := filepath.Base(rel)
	for _, pat := range patterns {
		if strings.HasSuffix(pat, "/") {
			if !isDir {
				continue
			}
			name := strings.TrimSuffix(pat, "/")
			if base == name {
				return true, true
			}
			if ok, _ := doublestar.Match(name, rel); ok {
				return true, true
			}
			continue
		}
		if isDir {
			continue
		}
		if ok, _ := doublestar.Match(pat, rel); ok {
			return true, false
		}
		if ok, _ := doublestar.Match(pat, base); ok {
			return true, false
		}
	}
	return false, false
}
