package graph

import (
	"errors"
	"io/fs"
	"strings"
)

// readFileFunc is the file-read abstraction the gitignore layer depends on.
// Production uses os.ReadFile; tests inject fakes to provoke read errors
// without touching the filesystem.
type readFileFunc func(name string) ([]byte, error)

// loadGitignorePatterns reads rootDir/.gitignore and returns its patterns
// translated into the same shape matchExcluded consumes (trailing "/" =
// directory pattern, otherwise file glob). Missing file is not an error
// — most projects have one, some don't, both are fine. Negation lines
// ("!pattern") are dropped; v0 doesn't model exclusion overrides.
func loadGitignorePatterns(readFile readFileFunc, gitignorePath string) ([]string, error) {
	data, err := readFile(gitignorePath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return parseGitignore(string(data)), nil
}

// parseGitignore extracts the directory and file patterns from a
// .gitignore body. Comment lines, blank lines, and negation lines are
// skipped. Leading "/" anchoring is stripped (the engine matches by
// relative path, so "/build" and "build" behave the same for our use).
func parseGitignore(content string) []string {
	var patterns []string
	for raw := range strings.SplitSeq(content, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}
		line = strings.TrimPrefix(line, "/")
		if line == "" {
			continue
		}
		patterns = append(patterns, line)
	}
	return patterns
}
