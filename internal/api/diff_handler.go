package api

import (
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
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
	Binary    bool   `json:"binary"`
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

	// Run git diff --numstat for file-level stats.
	numstatCmd := exec.CommandContext(r.Context(), "git", "diff", "--numstat")
	numstatCmd.Dir = dirPath
	numstatOut, err := numstatCmd.Output()
	if err != nil {
		// Not a git repo or git not available — return empty diff.
		writeHTTPJSON(w, http.StatusOK, diffResponse{Files: []diffFileEntry{}}, s.logger)
		return
	}

	files := parseNumstat(string(numstatOut))

	// Run git diff for the unified diff text.
	diffCmd := exec.CommandContext(r.Context(), "git", "diff")
	diffCmd.Dir = dirPath
	diffOut, _ := diffCmd.Output()
	diffText := string(diffOut)

	// Include untracked files.
	untrackedCmd := exec.CommandContext(r.Context(), "git", "ls-files", "--others", "--exclude-standard")
	untrackedCmd.Dir = dirPath
	if untrackedOut, err := untrackedCmd.Output(); err == nil {
		for _, uf := range splitLines(string(untrackedOut)) {
			entry, patch := buildUntrackedEntry(dirPath, uf)
			if entry != nil {
				files = append(files, *entry)
				diffText += patch
			}
		}
	}

	sort.Slice(files, func(i, j int) bool {
		return files[i].Path < files[j].Path
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

// parseNumstat parses `git diff --numstat` output into file entries.
// Format: "additions\tdeletions\tpath" per line. Binary files show "-\t-\tpath".
func parseNumstat(output string) []diffFileEntry {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	files := make([]diffFileEntry, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) < 3 {
			continue
		}
		entry := diffFileEntry{Path: parts[2]}
		if parts[0] == "-" && parts[1] == "-" {
			entry.Binary = true
		} else {
			entry.Additions, _ = strconv.Atoi(parts[0])
			entry.Deletions, _ = strconv.Atoi(parts[1])
		}
		files = append(files, entry)
	}
	return files
}
