package api

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type diffFileEntry struct {
	Path      string `json:"path"`
	OldPath   string `json:"old_path,omitempty"` // set when file was renamed
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Binary    bool   `json:"binary"`
	// Status is "staged" (in the index), "unstaged" (modified worktree),
	// or "untracked" (new file). Empty for branch-to-branch diff entries
	// where the staged/unstaged distinction does not apply.
	Status string `json:"status,omitempty"`
}

const (
	statusStaged    = "staged"
	statusUnstaged  = "unstaged"
	statusUntracked = "untracked"
)

// statusPriority orders entries within a single path so that, when a file is
// partially staged, the staged row precedes the unstaged row in the output.
func statusPriority(status string) int {
	switch status {
	case statusStaged:
		return 0
	case statusUnstaged:
		return 1
	case statusUntracked:
		return 2
	default:
		return 3
	}
}

// stampStatus sets Status on every entry in place, preserving zero values
// elsewhere on the struct.
func stampStatus(entries []diffFileEntry, status string) {
	for i := range entries {
		entries[i].Status = status
	}
}

type diffResponse struct {
	Files          []diffFileEntry `json:"files"`
	Diff           string          `json:"diff"`
	TotalAdditions int             `json:"total_additions"`
	TotalDeletions int             `json:"total_deletions"`
}

func (s *Server) handleGitDiff(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.store, "channel listing not configured") {
		return
	}

	channelID := r.PathValue("id")
	ch, err := s.store.GetChannel(r.Context(), channelID)
	if err != nil {
		http.Error(w, "failed to look up channel", http.StatusInternalServerError)
		return
	}
	if ch == nil {
		http.Error(w, "channel not found", http.StatusNotFound)
		return
	}

	dirPath := ch.DirPath
	if dirPath == "" && s.loopDir != "" {
		dirPath = filepath.Join(s.loopDir, ch.ChannelID, "work")
	}
	if dirPath == "" {
		writeHTTPJSON(w, http.StatusOK, diffResponse{Files: []diffFileEntry{}}, s.logger)
		return
	}

	// Branch-to-branch diff mode: ?source=branchA&target=branchB
	source := r.URL.Query().Get("source")
	target := r.URL.Query().Get("target")
	if source != "" && target != "" {
		s.handleBranchDiff(w, r, dirPath, source, target)
		return
	}

	// Staged changes: index vs HEAD. May fail in a brand-new repo with no
	// commits — treat that as "no staged entries" rather than erroring.
	stagedCmd := exec.CommandContext(r.Context(), "git", "diff", "--cached", "--numstat", "-z")
	stagedCmd.Dir = dirPath
	stagedNumstatOut, stagedErr := stagedCmd.Output()
	var stagedFiles []diffFileEntry
	var stagedDiffText string
	if stagedErr == nil {
		stagedFiles = parseNumstat(string(stagedNumstatOut))
		stampStatus(stagedFiles, statusStaged)

		stagedDiffCmd := exec.CommandContext(r.Context(), "git", "diff", "--cached")
		stagedDiffCmd.Dir = dirPath
		stagedDiffOut, _ := stagedDiffCmd.Output()
		stagedDiffText = string(stagedDiffOut)
	}

	// Unstaged changes: worktree vs index.
	numstatCmd := exec.CommandContext(r.Context(), "git", "diff", "--numstat", "-z")
	numstatCmd.Dir = dirPath
	numstatOut, err := numstatCmd.Output()
	if err != nil {
		// Not a git repo or git not available — return empty diff.
		writeHTTPJSON(w, http.StatusOK, diffResponse{Files: []diffFileEntry{}}, s.logger)
		return
	}

	unstagedFiles := parseNumstat(string(numstatOut))
	stampStatus(unstagedFiles, statusUnstaged)

	diffCmd := exec.CommandContext(r.Context(), "git", "diff")
	diffCmd.Dir = dirPath
	diffOut, _ := diffCmd.Output()
	unstagedDiffText := string(diffOut)

	files := make([]diffFileEntry, 0, len(stagedFiles)+len(unstagedFiles))
	files = append(files, stagedFiles...)
	files = append(files, unstagedFiles...)
	diffText := stagedDiffText + unstagedDiffText

	// Include untracked files.
	untrackedCmd := exec.CommandContext(r.Context(), "git", "ls-files", "--others", "--exclude-standard")
	untrackedCmd.Dir = dirPath
	if untrackedOut, err := untrackedCmd.Output(); err == nil {
		for _, uf := range splitLines(string(untrackedOut)) {
			entry, patch := buildUntrackedEntry(dirPath, uf)
			if entry != nil {
				entry.Status = statusUntracked
				files = append(files, *entry)
				diffText += patch
			}
		}
	}

	sort.SliceStable(files, func(i, j int) bool {
		if files[i].Path != files[j].Path {
			return files[i].Path < files[j].Path
		}
		return statusPriority(files[i].Status) < statusPriority(files[j].Status)
	})

	var totalAdd, totalDel int
	for _, f := range files {
		totalAdd += f.Additions
		totalDel += f.Deletions
	}

	resp := diffResponse{
		Files:          files,
		Diff:           diffText,
		TotalAdditions: totalAdd,
		TotalDeletions: totalDel,
	}

	writeHTTPJSON(w, http.StatusOK, resp, s.logger)
}

// splitLines splits output into non-empty trimmed lines.
func splitLines(output string) []string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			result = append(result, line)
		}
	}
	return result
}

// buildUntrackedEntry reads an untracked file and returns a diff file entry
// plus a unified diff patch string. Returns nil entry if the file cannot be read.
func buildUntrackedEntry(dirPath, relPath string) (*diffFileEntry, string) {
	absPath := filepath.Join(dirPath, relPath)
	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil, ""
	}

	entry := diffFileEntry{Path: relPath}

	// Binary detection: check first 512 bytes for null bytes.
	checkLen := len(data)
	if checkLen > 512 {
		checkLen = 512
	}
	for i := 0; i < checkLen; i++ {
		if data[i] == 0 {
			entry.Binary = true
			patch := fmt.Sprintf("diff --git a/%s b/%s\nnew file mode 100644\nBinary files /dev/null and b/%s differ\n", relPath, relPath, relPath)
			return &entry, patch
		}
	}

	lines := strings.Split(string(data), "\n")
	// Remove trailing empty element from final newline.
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	entry.Additions = len(lines)

	var b strings.Builder
	fmt.Fprintf(&b, "diff --git a/%s b/%s\nnew file mode 100644\n--- /dev/null\n+++ b/%s\n@@ -0,0 +1,%d @@\n", relPath, relPath, relPath, len(lines))
	for _, line := range lines {
		fmt.Fprintf(&b, "+%s\n", line)
	}
	return &entry, b.String()
}

// handleBranchDiff computes a diff between two branch refs using the three-dot
// merge-base syntax (source...target), showing what target has that source doesn't.
func (s *Server) handleBranchDiff(w http.ResponseWriter, r *http.Request, dirPath, source, target string) {
	source, ok := sanitizeBranch(source)
	if !ok {
		http.Error(w, "invalid source branch name", http.StatusBadRequest)
		return
	}
	target, ok = sanitizeBranch(target)
	if !ok {
		http.Error(w, "invalid target branch name", http.StatusBadRequest)
		return
	}

	rangeSpec := source + "..." + target

	numstatCmd := exec.CommandContext(r.Context(), "git", "diff", "--numstat", "-z", rangeSpec)
	numstatCmd.Dir = dirPath
	numstatOut, err := numstatCmd.Output()
	if err != nil {
		msg := "git diff failed"
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			msg += ": " + strings.TrimSpace(string(exitErr.Stderr))
		}
		http.Error(w, msg, http.StatusInternalServerError)
		return
	}

	files := parseNumstat(string(numstatOut))

	diffCmd := exec.CommandContext(r.Context(), "git", "diff", rangeSpec)
	diffCmd.Dir = dirPath
	diffOut, _ := diffCmd.Output()
	diffText := string(diffOut)

	sort.Slice(files, func(i, j int) bool {
		return files[i].Path < files[j].Path
	})

	var totalAdd, totalDel int
	for _, f := range files {
		totalAdd += f.Additions
		totalDel += f.Deletions
	}

	writeHTTPJSON(w, http.StatusOK, diffResponse{
		Files:          files,
		Diff:           diffText,
		TotalAdditions: totalAdd,
		TotalDeletions: totalDel,
	}, s.logger)
}

// parseNumstat parses `git diff --numstat -z` output into file entries.
// With -z, fields are NUL-separated. Renames have an empty path field
// followed by two NUL-separated paths: "adds\tdels\t\0old\0new\0".
// Normal files: "adds\tdels\tpath\0".
func parseNumstat(output string) []diffFileEntry {
	// Split on NUL to get tokens. The output is a sequence of records
	// where each record starts with "adds\tdels\tpath" (normal) or
	// "adds\tdels\t" followed by old_path and new_path (rename).
	records := strings.Split(output, "\x00")
	files := make([]diffFileEntry, 0)

	for i := 0; i < len(records); i++ {
		record := records[i]
		if record == "" || record == "\n" {
			continue
		}
		// Each record is "adds\tdels\tpath" — split on tabs.
		parts := strings.SplitN(strings.TrimLeft(record, "\n"), "\t", 3)
		if len(parts) < 3 {
			continue
		}
		entry := diffFileEntry{}
		if parts[0] == "-" && parts[1] == "-" {
			entry.Binary = true
		} else {
			entry.Additions, _ = strconv.Atoi(parts[0])
			entry.Deletions, _ = strconv.Atoi(parts[1])
		}
		if parts[2] == "" {
			// Rename: next two NUL-separated tokens are old_path and new_path.
			if i+2 < len(records) {
				entry.OldPath = records[i+1]
				entry.Path = records[i+2]
				i += 2
			}
		} else {
			entry.Path = parts[2]
		}
		if entry.Path != "" {
			files = append(files, entry)
		}
	}
	return files
}
