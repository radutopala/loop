package api

import (
	"fmt"
	"net/http"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/radutopala/loop/internal/randutil"
)

type createWorktreeRequest struct {
	ChannelID string `json:"channel_id"`
	Branch    string `json:"branch"`
	Name      string `json:"name,omitempty"`
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
	if !validBranchName.MatchString(req.Branch) {
		http.Error(w, "invalid branch name", http.StatusBadRequest)
		return
	}

	// Resolve parent channel and get DirPath + SessionID.
	parent, err := s.store.GetChannel(r.Context(), req.ChannelID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if parent == nil || parent.DirPath == "" {
		http.Error(w, "channel not found or has no dir_path", http.StatusBadRequest)
		return
	}
	dirPath := parent.DirPath

	// Generate worktree name.
	name := req.Name
	if name == "" {
		name = "wt-" + randutil.HexID(4)
	}

	worktreePath := filepath.Join(dirPath, ".worktrees", name)

	// Create git worktree with a new branch based on the selected one.
	// Using -b creates a dedicated branch for the worktree, which avoids
	// "already checked out" errors when the base branch is in use.
	wtBranch := "worktree/" + name
	cmd := exec.CommandContext(r.Context(), "git", "worktree", "add", "-b", wtBranch, worktreePath, req.Branch)
	cmd.Dir = dirPath
	if out, err := cmd.CombinedOutput(); err != nil {
		http.Error(w, fmt.Sprintf("git worktree add failed: %s", strings.TrimSpace(string(out))), http.StatusInternalServerError)
		return
	}

	// Copy session file so --resume --fork-session works in the worktree dir.
	if parent.SessionID != "" {
		if err := s.copySessionFile(dirPath, worktreePath, parent.SessionID); err != nil {
			s.logger.Warn("copying session file for worktree", "error", err)
		}
	}

	// Create thread with worktree flag.
	threadName := fmt.Sprintf("\U0001F500 %s (%s)", name, req.Branch)
	threadID, err := s.threads.CreateThread(r.Context(), req.ChannelID, threadName, "", "")
	if err != nil {
		http.Error(w, fmt.Sprintf("creating thread: %s", err), http.StatusInternalServerError)
		return
	}

	// Update the thread's DirPath to point to the worktree and set Worktree flag.
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

	writeHTTPJSON(w, http.StatusCreated, createWorktreeResponse{
		ThreadID:     threadID,
		WorktreePath: worktreePath,
	}, s.logger)
}

// encodeClaudeProjectPath encodes a directory path the same way Claude Code does:
// replace "/" and "." with "-".
// E.g. "/Users/me/.worktrees/wt" → "-Users-me--worktrees-wt".
func encodeClaudeProjectPath(dirPath string) string {
	r := strings.NewReplacer("/", "-", ".", "-")
	return r.Replace(dirPath)
}

// copySessionFile copies a Claude session file from the parent project dir
// to the worktree project dir so that --resume --fork-session can find it.
func (s *Server) copySessionFile(parentDirPath, worktreeDirPath, sessionID string) error {
	home, err := s.sys.UserHomeDir()
	if err != nil {
		return fmt.Errorf("getting home dir: %w", err)
	}
	srcDir := filepath.Join(home, ".claude", "projects", encodeClaudeProjectPath(parentDirPath))
	src := filepath.Join(srcDir, sessionID+".jsonl")
	dstDir := filepath.Join(home, ".claude", "projects", encodeClaudeProjectPath(worktreeDirPath))
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
