package api

import (
	"net/http"
	"os/exec"
	"regexp"
	"strings"
)

type branchListResponse struct {
	Branches  []string        `json:"branches"`
	Current   string          `json:"current"`
	Worktrees []worktreeEntry `json:"worktrees"`
}

type worktreeEntry struct {
	Path   string `json:"path"`
	Branch string `json:"branch"`
}

// validBranchName matches safe git branch names (alphanumeric, slashes, hyphens, dots, underscores).
var validBranchName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9/_.\-]*$`)

func (s *Server) handleListBranches(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.store, "channel listing not configured") {
		return
	}

	channelID := r.PathValue("id")
	dirPath, err := s.resolveDirPath(r.Context(), "", channelID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// List local branches.
	branchCmd := exec.CommandContext(r.Context(), "git", "branch", "--format=%(refname:short)")
	branchCmd.Dir = dirPath
	branchOut, err := branchCmd.Output()
	if err != nil {
		http.Error(w, "failed to list branches", http.StatusInternalServerError)
		return
	}

	var branches []string
	for _, line := range strings.Split(strings.TrimSpace(string(branchOut)), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			branches = append(branches, line)
		}
	}

	// Current branch.
	current := gitBranch(r.Context(), dirPath)

	// List worktrees.
	wtCmd := exec.CommandContext(r.Context(), "git", "worktree", "list", "--porcelain")
	wtCmd.Dir = dirPath
	wtOut, _ := wtCmd.Output() // ignore error — worktrees may not exist

	worktrees := parseWorktrees(string(wtOut), dirPath)

	// Exclude branches checked out in other worktrees — git checkout
	// in the main working directory would fail for these.
	wtBranches := make(map[string]struct{}, len(worktrees))
	for _, wt := range worktrees {
		wtBranches[wt.Branch] = struct{}{}
	}
	filtered := branches[:0]
	for _, b := range branches {
		if _, locked := wtBranches[b]; !locked {
			filtered = append(filtered, b)
		}
	}

	writeHTTPJSON(w, http.StatusOK, branchListResponse{
		Branches:  filtered,
		Current:   current,
		Worktrees: worktrees,
	}, s.logger)
}

// parseWorktrees parses `git worktree list --porcelain` output.
// Skips the main worktree (whose path matches dirPath).
func parseWorktrees(output, mainDir string) []worktreeEntry {
	var worktrees []worktreeEntry
	var current worktreeEntry
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "worktree ") {
			if current.Path != "" && current.Path != mainDir {
				worktrees = append(worktrees, current)
			}
			current = worktreeEntry{Path: strings.TrimPrefix(line, "worktree ")}
		} else if strings.HasPrefix(line, "branch ") {
			ref := strings.TrimPrefix(line, "branch ")
			// Convert refs/heads/foo to foo.
			current.Branch = strings.TrimPrefix(ref, "refs/heads/")
		}
	}
	// Flush last entry.
	if current.Path != "" && current.Path != mainDir {
		worktrees = append(worktrees, current)
	}
	return worktrees
}

type switchBranchRequest struct {
	Branch string `json:"branch"`
}

func (s *Server) handleSwitchBranch(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.store, "channel listing not configured") {
		return
	}

	var req switchBranchRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if req.Branch == "" {
		http.Error(w, "branch is required", http.StatusBadRequest)
		return
	}

	if !validBranchName.MatchString(req.Branch) {
		http.Error(w, "invalid branch name", http.StatusBadRequest)
		return
	}

	channelID := r.PathValue("id")
	dirPath, err := s.resolveDirPath(r.Context(), "", channelID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	cmd := exec.CommandContext(r.Context(), "git", "checkout", req.Branch)
	cmd.Dir = dirPath
	if out, err := cmd.CombinedOutput(); err != nil {
		http.Error(w, "git checkout failed: "+strings.TrimSpace(string(out)), http.StatusInternalServerError)
		return
	}

	writeHTTPJSON(w, http.StatusOK, writeFileResponse{OK: true}, s.logger)
}

type createBranchRequest struct {
	Name string `json:"name"`
	From string `json:"from,omitempty"`
}

func (s *Server) handleCreateBranch(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.store, "channel listing not configured") {
		return
	}

	var req createBranchRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if req.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}

	if !validBranchName.MatchString(req.Name) {
		http.Error(w, "invalid branch name", http.StatusBadRequest)
		return
	}

	if req.From != "" && !validBranchName.MatchString(req.From) {
		http.Error(w, "invalid base branch name", http.StatusBadRequest)
		return
	}

	channelID := r.PathValue("id")
	dirPath, err := s.resolveDirPath(r.Context(), "", channelID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Names are validated above by validBranchName regex (alphanumeric + /_.-).
	args := []string{"checkout", "-b", req.Name} // #nosec G204 -- validated by validBranchName
	if req.From != "" {
		args = append(args, req.From)
	}

	cmd := exec.CommandContext(r.Context(), "git", args...) //nolint:gosec // branch names validated above
	cmd.Dir = dirPath
	if out, err := cmd.CombinedOutput(); err != nil {
		http.Error(w, "git checkout -b failed: "+strings.TrimSpace(string(out)), http.StatusInternalServerError)
		return
	}

	writeHTTPJSON(w, http.StatusOK, writeFileResponse{OK: true}, s.logger)
}
