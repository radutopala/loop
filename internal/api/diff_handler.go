package api

import (
	"net/http"
	"os/exec"
	"path/filepath"
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

	var totalAdd, totalDel int
	for _, f := range files {
		totalAdd += f.Additions
		totalDel += f.Deletions
	}

	resp := diffResponse{
		Files:          files,
		Diff:           string(diffOut),
		TotalAdditions: totalAdd,
		TotalDeletions: totalDel,
	}

	writeHTTPJSON(w, http.StatusOK, resp, s.logger)
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
