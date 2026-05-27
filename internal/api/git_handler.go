package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/radutopala/loop/internal/osutil"
	"github.com/radutopala/loop/internal/randutil"
)

type branchListResponse struct {
	Branches  []string        `json:"branches"`
	Current   string          `json:"current"`
	Worktrees []worktreeEntry `json:"worktrees"`
}

type worktreeEntry struct {
	Path     string `json:"path"`
	Branch   string `json:"branch"`
	ThreadID string `json:"thread_id,omitempty"`
	Locked   bool   `json:"locked,omitempty"`
}

// validBranchName matches safe git branch names (alphanumeric, slashes, hyphens, dots, underscores).
var validBranchName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9/_.\-]*$`)

// sanitizeBranch validates a branch name and returns it unchanged if valid.
// Returns empty string and false if the name is invalid. This function acts as
// a sanitization barrier so static analysis tools can see the data is validated
// before being passed to exec.Command.
func sanitizeBranch(name string) (string, bool) {
	if !validBranchName.MatchString(name) {
		return "", false
	}
	return name, true
}

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

	branches := []string{}
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

	// Enrich worktrees with thread IDs (and DB-side locked state) for worktrees
	// already imported as threads. DB locked OR git locked → Locked=true so the
	// UI sees a single consolidated state.
	if allChannels, err := s.store.ListChannels(r.Context()); err == nil {
		type info struct {
			threadID string
			locked   bool
		}
		wtPathToInfo := make(map[string]info)
		for _, ch := range allChannels {
			if ch.ParentID == channelID && ch.Worktree && ch.DirPath != "" {
				wtPathToInfo[realPath(ch.DirPath)] = info{threadID: ch.ChannelID, locked: ch.Locked}
			}
		}
		for i := range worktrees {
			if v, ok := wtPathToInfo[realPath(worktrees[i].Path)]; ok {
				worktrees[i].ThreadID = v.threadID
				if v.locked {
					worktrees[i].Locked = true
				}
			}
		}
	}

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
// Paths are symlink-resolved so comparisons work on macOS where
// /tmp → /private/var/folders/… causes mismatches.
func parseWorktrees(output, mainDir string) []worktreeEntry {
	realMain, err := filepath.EvalSymlinks(mainDir)
	if err != nil {
		realMain = mainDir
	}
	worktrees := []worktreeEntry{}
	var current worktreeEntry
	for _, line := range strings.Split(output, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			if current.Path != "" && realPath(current.Path) != realMain {
				worktrees = append(worktrees, current)
			}
			current = worktreeEntry{Path: strings.TrimPrefix(line, "worktree ")}
		case strings.HasPrefix(line, "branch "):
			ref := strings.TrimPrefix(line, "branch ")
			// Convert refs/heads/foo to foo.
			current.Branch = strings.TrimPrefix(ref, "refs/heads/")
		case line == "locked" || strings.HasPrefix(line, "locked "):
			current.Locked = true
		}
	}
	// Flush last entry.
	if current.Path != "" && realPath(current.Path) != realMain {
		worktrees = append(worktrees, current)
	}
	return worktrees
}

// realPath resolves symlinks, falling back to the original path on error.
func realPath(p string) string {
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		return p
	}
	return r
}

// ── Commits ──

type commitEntry struct {
	Hash    string `json:"hash"`
	Short   string `json:"short"`
	Subject string `json:"subject"`
	Author  string `json:"author"`
	Date    string `json:"date"`
}

type commitsResponse struct {
	Commits []commitEntry `json:"commits"`
}

// handleListCommits returns the commit log for a given branch (or HEAD if omitted).
// GET /api/channels/{id}/commits?branch=...&limit=...&skip=...
func (s *Server) handleListCommits(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.store, "channel listing not configured") {
		return
	}

	channelID := r.PathValue("id")
	dirPath, err := s.resolveDirPath(r.Context(), "", channelID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// ?root=N selects an extra_dirs entry instead of the primary dir_path.
	// Mirrors handleGitDiff. root=0 (default) is the primary dir; root>0
	// indexes into extra_dirs in order.
	if rootStr := r.URL.Query().Get("root"); rootStr != "" {
		rootIdx, err := strconv.Atoi(rootStr)
		if err != nil || rootIdx < 0 {
			http.Error(w, "invalid root index", http.StatusBadRequest)
			return
		}
		if rootIdx > 0 {
			resolved, err := s.resolveRootDir(r.Context(), channelID, r)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			dirPath = resolved
		}
	}

	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, parseErr := strconv.Atoi(l); parseErr == nil && parsed >= 1 {
			limit = parsed
		}
		if limit > 200 {
			limit = 200
		}
	}

	skip := 0
	if s := r.URL.Query().Get("skip"); s != "" {
		if parsed, parseErr := strconv.Atoi(s); parseErr == nil && parsed >= 0 {
			skip = parsed
		}
	}

	args := []string{"log", fmt.Sprintf("--max-count=%d", limit), fmt.Sprintf("--skip=%d", skip), "--format=%H\x1e%h\x1e%s\x1e%an\x1e%ci"}
	if branch := r.URL.Query().Get("branch"); branch != "" {
		safe, ok := sanitizeBranch(branch)
		if !ok {
			http.Error(w, "invalid branch name", http.StatusBadRequest)
			return
		}
		args = append(args, safe)
	}

	cmd := exec.CommandContext(r.Context(), "git", args...)
	cmd.Dir = dirPath
	out, err := cmd.Output()
	if err != nil {
		// Empty repos (no commits yet) cause git log to fail — return empty list.
		writeHTTPJSON(w, http.StatusOK, commitsResponse{Commits: []commitEntry{}}, s.logger)
		return
	}

	writeHTTPJSON(w, http.StatusOK, commitsResponse{Commits: parseCommitLog(string(out))}, s.logger)
}

// parseCommitLog parses the output of git log --format=%H\x1e%h\x1e%s\x1e%an\x1e%ci
// into a slice of commitEntry. Malformed lines are skipped.
func parseCommitLog(output string) []commitEntry {
	var commits []commitEntry
	for _, line := range strings.Split(strings.TrimSpace(output), "\n") {
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\x1e", 5)
		if len(parts) < 5 {
			continue
		}
		commits = append(commits, commitEntry{
			Hash:    parts[0],
			Short:   parts[1],
			Subject: parts[2],
			Author:  parts[3],
			Date:    parts[4],
		})
	}
	if commits == nil {
		commits = []commitEntry{}
	}
	return commits
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

	branch, ok := sanitizeBranch(req.Branch)
	if !ok {
		http.Error(w, "invalid branch name", http.StatusBadRequest)
		return
	}

	channelID := r.PathValue("id")
	dirPath, err := s.resolveDirPath(r.Context(), "", channelID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	cmd := exec.CommandContext(r.Context(), "git", "checkout", branch)
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

	name, ok := sanitizeBranch(req.Name)
	if !ok {
		http.Error(w, "invalid branch name", http.StatusBadRequest)
		return
	}

	var from string
	if req.From != "" {
		from, ok = sanitizeBranch(req.From)
		if !ok {
			http.Error(w, "invalid base branch name", http.StatusBadRequest)
			return
		}
	}

	channelID := r.PathValue("id")
	dirPath, err := s.resolveDirPath(r.Context(), "", channelID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	args := []string{"checkout", "-b", name}
	if from != "" {
		args = append(args, from)
	}

	cmd := exec.CommandContext(r.Context(), "git", args...)
	cmd.Dir = dirPath
	if out, err := cmd.CombinedOutput(); err != nil {
		http.Error(w, "git checkout -b failed: "+strings.TrimSpace(string(out)), http.StatusInternalServerError)
		return
	}

	writeHTTPJSON(w, http.StatusOK, writeFileResponse{OK: true}, s.logger)
}

type deleteBranchRequest struct {
	Branch string `json:"branch"`
}

func (s *Server) handleDeleteBranch(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.store, "channel listing not configured") {
		return
	}

	var req deleteBranchRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if req.Branch == "" {
		http.Error(w, "branch is required", http.StatusBadRequest)
		return
	}

	branch, ok := sanitizeBranch(req.Branch)
	if !ok {
		http.Error(w, "invalid branch name", http.StatusBadRequest)
		return
	}

	channelID := r.PathValue("id")
	dirPath, err := s.resolveDirPath(r.Context(), "", channelID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Prevent deleting the current branch.
	current := gitBranch(r.Context(), dirPath)
	if branch == current {
		http.Error(w, "cannot delete the currently checked-out branch", http.StatusBadRequest)
		return
	}

	cmd := exec.CommandContext(r.Context(), "git", "branch", "-D", branch)
	cmd.Dir = dirPath
	if out, err := cmd.CombinedOutput(); err != nil {
		http.Error(w, "git branch -D failed: "+strings.TrimSpace(string(out)), http.StatusInternalServerError)
		return
	}

	writeHTTPJSON(w, http.StatusOK, writeFileResponse{OK: true}, s.logger)
}

// ── Worktrees ──

type createWorktreeRequest struct {
	ChannelID string `json:"channel_id"`
	Branch    string `json:"branch"`
	Name      string `json:"name,omitempty"`
	AuthorID  string `json:"author_id,omitempty"`
	Message   string `json:"message,omitempty"`
}

type createWorktreeResponse struct {
	ThreadID     string `json:"thread_id"`
	WorktreePath string `json:"worktree_path"`
}

func (s *Server) handleCreateWorktree(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.store, "channel listing not configured") {
		return
	}
	if !requireConfigured(w, s.threads, "thread creation not configured") {
		return
	}

	var req createWorktreeRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if req.ChannelID == "" {
		http.Error(w, "channel_id is required", http.StatusBadRequest)
		return
	}
	if req.Branch == "" {
		http.Error(w, "branch is required", http.StatusBadRequest)
		return
	}
	branch, ok := sanitizeBranch(req.Branch)
	if !ok {
		http.Error(w, "invalid branch name", http.StatusBadRequest)
		return
	}

	parent, err := s.store.GetChannel(r.Context(), req.ChannelID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if parent == nil || parent.DirPath == "" {
		http.Error(w, "channel not found or has no dir_path", http.StatusBadRequest)
		return
	}
	// If req.ChannelID is a thread, resolve to its parent project channel so
	// the worktree's seeded session matches what the new thread row will store
	// (thread_service.CreateThread also re-resolves to the grandparent).
	if parent.ParentID != "" {
		grandparent, err := s.store.GetChannel(r.Context(), parent.ParentID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if grandparent != nil && grandparent.DirPath != "" {
			parent = grandparent
		}
	}
	dirPath := parent.DirPath

	name := req.Name
	if name == "" {
		name = "wt-" + randutil.HexID(4)
	}

	result, err := s.worktreeCreator.Create(r.Context(), dirPath, branch, name, parent.SessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	worktreePath := result.WorktreePath

	threadName := fmt.Sprintf("%s (%s)", name, req.Branch)
	// When msgHandler is set, skip storing the message in CreateThread —
	// HandleThreadCreated will store it as a user message instead.
	msg := req.Message
	if s.msgHandler != nil {
		msg = ""
	}
	threadID, err := s.threads.CreateThread(r.Context(), req.ChannelID, threadName, req.AuthorID, msg)
	if err != nil {
		http.Error(w, fmt.Sprintf("creating thread: %s", err), http.StatusInternalServerError)
		return
	}

	ch, err := s.store.GetChannel(r.Context(), threadID)
	if err != nil || ch == nil {
		http.Error(w, "failed to get created thread", http.StatusInternalServerError)
		return
	}
	ch.DirPath = worktreePath
	ch.Worktree = true
	if err := s.store.UpsertChannel(r.Context(), ch); err != nil {
		http.Error(w, fmt.Sprintf("updating thread: %s", err), http.StatusInternalServerError)
		return
	}

	if s.eventsHub != nil {
		s.eventsHub.BroadcastChannelCreated(req.ChannelID, threadID)
	}

	if s.msgHandler != nil && req.Message != "" {
		go s.msgHandler.HandleThreadCreated(context.Background(), threadID, req.AuthorID, req.Message)
	}

	writeHTTPJSON(w, http.StatusCreated, createWorktreeResponse{
		ThreadID:     threadID,
		WorktreePath: worktreePath,
	}, s.logger)
}

// ── Import Worktree ──

type importWorktreeRequest struct {
	ChannelID    string `json:"channel_id"`
	WorktreePath string `json:"worktree_path"`
}

func (s *Server) handleImportWorktree(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.store, "channel listing not configured") {
		return
	}
	if !requireConfigured(w, s.threads, "thread creation not configured") {
		return
	}

	var req importWorktreeRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if req.ChannelID == "" {
		http.Error(w, "channel_id is required", http.StatusBadRequest)
		return
	}
	if req.WorktreePath == "" {
		http.Error(w, "worktree_path is required", http.StatusBadRequest)
		return
	}

	parent, err := s.store.GetChannel(r.Context(), req.ChannelID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if parent == nil || parent.DirPath == "" {
		http.Error(w, "channel not found or has no dir_path", http.StatusBadRequest)
		return
	}

	// Validate that the path is an actual git worktree by checking git worktree list.
	wtCmd := exec.CommandContext(r.Context(), "git", "worktree", "list", "--porcelain")
	wtCmd.Dir = parent.DirPath
	wtOut, err := wtCmd.Output()
	if err != nil {
		http.Error(w, "failed to list worktrees", http.StatusInternalServerError)
		return
	}

	worktrees := parseWorktrees(string(wtOut), parent.DirPath)
	var matched *worktreeEntry
	resolvedPath := realPath(req.WorktreePath)
	for i := range worktrees {
		if realPath(worktrees[i].Path) == resolvedPath {
			matched = &worktrees[i]
			break
		}
	}
	if matched == nil {
		http.Error(w, "path is not a known git worktree", http.StatusBadRequest)
		return
	}

	// Check if a thread already exists for this worktree path.
	if allChannels, err := s.store.ListChannels(r.Context()); err == nil {
		for _, ch := range allChannels {
			if ch.ParentID == req.ChannelID && ch.Worktree && realPath(ch.DirPath) == resolvedPath {
				writeHTTPJSON(w, http.StatusOK, createWorktreeResponse{
					ThreadID:     ch.ChannelID,
					WorktreePath: req.WorktreePath,
				}, s.logger)
				return
			}
		}
	}

	// Derive thread name from worktree directory name and branch.
	name := filepath.Base(resolvedPath)
	branchLabel := matched.Branch
	if branchLabel == "" {
		branchLabel = "detached"
	}
	threadName := fmt.Sprintf("%s (%s)", name, branchLabel)

	threadID, err := s.threads.CreateThread(r.Context(), req.ChannelID, threadName, "", "")
	if err != nil {
		http.Error(w, fmt.Sprintf("creating thread: %s", err), http.StatusInternalServerError)
		return
	}

	ch, err := s.store.GetChannel(r.Context(), threadID)
	if err != nil || ch == nil {
		http.Error(w, "failed to get created thread", http.StatusInternalServerError)
		return
	}
	ch.DirPath = req.WorktreePath
	ch.Worktree = true
	if err := s.store.UpsertChannel(r.Context(), ch); err != nil {
		http.Error(w, fmt.Sprintf("updating thread: %s", err), http.StatusInternalServerError)
		return
	}

	// Seed worktree config with extra_dirs pointing at the parent project.
	wtLoopDir := filepath.Join(req.WorktreePath, ".loop")
	if err := s.sys.MkdirAll(wtLoopDir, 0755); err != nil {
		s.logger.Warn("creating worktree .loop dir", "error", err)
	} else {
		cfgPath := filepath.Join(wtLoopDir, "config.json")
		// Only write if the config doesn't already exist (don't overwrite user edits).
		if _, err := s.sys.Stat(cfgPath); errors.Is(err, fs.ErrNotExist) {
			wtCfg := fmt.Sprintf("{\n  \"extra_dirs\": [\n    %q\n  ]\n}\n", parent.DirPath)
			if err := s.sys.WriteFile(cfgPath, []byte(wtCfg), 0644); err != nil {
				s.logger.Warn("writing worktree config", "error", err)
			}
		}
	}

	// Copy session file so --resume --fork-session works in the worktree dir.
	if parent.SessionID != "" {
		if err := s.copySessionFile(parent.DirPath, req.WorktreePath, parent.SessionID); err != nil {
			s.logger.Warn("copying session file for imported worktree", "error", err)
		}
	}

	if s.eventsHub != nil {
		s.eventsHub.BroadcastChannelCreated(req.ChannelID, threadID)
	}

	writeHTTPJSON(w, http.StatusCreated, createWorktreeResponse{
		ThreadID:     threadID,
		WorktreePath: req.WorktreePath,
	}, s.logger)
}

// ── Delete Worktree ──

// handleDeleteWorktree permanently removes a git worktree: deletes the Loop
// thread, runs `git worktree remove --force`, and prunes stale metadata.
// handleRemoveWorktree removes a git worktree from disk and optionally deletes
// the associated thread record. Works for both imported (has thread_id) and
// non-imported (disk-only) worktrees.
//
// Body: { channel_id, worktree_path, thread_id? }
func (s *Server) handleRemoveWorktree(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.store, "channel store not configured") {
		return
	}

	var body struct {
		ChannelID    string `json:"channel_id"`
		WorktreePath string `json:"worktree_path"`
		ThreadID     string `json:"thread_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ChannelID == "" || body.WorktreePath == "" {
		http.Error(w, "channel_id and worktree_path required", http.StatusBadRequest)
		return
	}

	ch, err := s.store.GetChannel(r.Context(), body.ChannelID)
	if err != nil || ch == nil || ch.DirPath == "" {
		http.Error(w, "channel not found or has no dir_path", http.StatusBadRequest)
		return
	}

	// Remove the git worktree from disk.
	if s.worktreeCreator != nil {
		if err := s.worktreeCreator.Remove(r.Context(), ch.DirPath, body.WorktreePath); err != nil {
			http.Error(w, fmt.Sprintf("failed to remove worktree: %v", err), http.StatusInternalServerError)
			return
		}
	}

	// If this worktree was imported as a thread, delete the thread record too.
	if body.ThreadID != "" && s.threads != nil {
		if err := s.threads.DeleteThread(r.Context(), body.ThreadID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if s.eventsHub != nil {
			s.eventsHub.BroadcastChannelDeleted(body.ThreadID)
		}
	}

	w.WriteHeader(http.StatusNoContent)
}

// ── Lock / Unlock Worktree (path-based, for non-imported worktrees) ──

// handleSetWorktreeLocked toggles `git worktree lock`/`unlock` for a worktree
// path that is NOT imported as a Loop thread. For imported worktrees the
// caller should PATCH /api/channels/{id}/lock instead, which updates DB +
// git in one go via handleSetChannelLocked.
//
// Body: { channel_id, worktree_path, locked }
func (s *Server) handleSetWorktreeLocked(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.store, "channel store not configured") {
		return
	}
	if s.worktreeCreator == nil {
		http.Error(w, "worktree operations not configured", http.StatusNotImplemented)
		return
	}

	var body struct {
		ChannelID    string `json:"channel_id"`
		WorktreePath string `json:"worktree_path"`
		Locked       bool   `json:"locked"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ChannelID == "" || body.WorktreePath == "" {
		http.Error(w, "channel_id and worktree_path required", http.StatusBadRequest)
		return
	}

	ch, err := s.store.GetChannel(r.Context(), body.ChannelID)
	if err != nil || ch == nil || ch.DirPath == "" {
		http.Error(w, "channel not found or has no dir_path", http.StatusBadRequest)
		return
	}

	if body.Locked {
		err = s.worktreeCreator.Lock(r.Context(), ch.DirPath, body.WorktreePath, "locked from Loop UI")
	} else {
		err = s.worktreeCreator.Unlock(r.Context(), ch.DirPath, body.WorktreePath)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ── Session Copy ──

// copySessionFile copies a Claude session file from the parent project dir
// to the worktree project dir so that --resume --fork-session can find it.
func (s *Server) copySessionFile(parentDirPath, worktreeDirPath, sessionID string) error {
	// Sanitise sessionID to prevent path traversal.
	sessionID = filepath.Base(sessionID)
	if sessionID == "." || sessionID == ".." || sessionID == "" {
		return fmt.Errorf("invalid session ID")
	}

	home, err := s.sys.UserHomeDir()
	if err != nil {
		return fmt.Errorf("getting home dir: %w", err)
	}
	srcDir := filepath.Join(home, ".claude", "projects", osutil.EncodeClaudeProjectPath(parentDirPath))
	src := filepath.Join(srcDir, sessionID+".jsonl")
	dstDir := filepath.Join(home, ".claude", "projects", osutil.EncodeClaudeProjectPath(worktreeDirPath))
	dst := filepath.Join(dstDir, sessionID+".jsonl")

	data, err := s.sys.ReadFile(src)
	if err != nil {
		return fmt.Errorf("reading session file: %w", err)
	}
	if err := s.sys.MkdirAll(dstDir, 0o755); err != nil {
		return fmt.Errorf("creating project dir: %w", err)
	}
	if err := s.sys.WriteFile(dst, data, 0o644); err != nil {
		return fmt.Errorf("writing session file: %w", err)
	}
	return nil
}
