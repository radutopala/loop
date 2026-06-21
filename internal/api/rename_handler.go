package api

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/radutopala/loop/internal/events"
	"github.com/radutopala/loop/internal/osutil"
)

// ── Rename Channel ──

type renameChannelRequest struct {
	Name string `json:"name"`
}

type renameChannelResponse struct {
	ChannelID string `json:"channel_id"`
	Name      string `json:"name"`
}

func (s *Server) handleRenameChannel(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.store, "channel store not configured") {
		return
	}

	channelID := r.PathValue("id")

	var req renameChannelRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if req.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}

	ch, err := s.store.GetChannel(r.Context(), channelID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if ch == nil {
		http.Error(w, "channel not found", http.StatusNotFound)
		return
	}

	if err := s.store.UpdateChannelName(r.Context(), channelID, req.Name); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if s.eventsHub != nil {
		s.eventsHub.BroadcastChannelUpdated(events.ChannelUpdatedData{
			ChannelID: channelID,
			Name:      req.Name,
		})
	}

	writeHTTPJSON(w, http.StatusOK, renameChannelResponse{
		ChannelID: channelID,
		Name:      req.Name,
	}, s.logger)
}

// ── Move Worktree ──

type moveWorktreeRequest struct {
	ChannelID string `json:"channel_id"`
	NewName   string `json:"new_name"`
}

type moveWorktreeResponse struct {
	ChannelID string `json:"channel_id"`
	DirPath   string `json:"dir_path"`
	Name      string `json:"name"`
}

// relocateSessionDir renames the Claude session store directory from the
// encoded path for oldDir to the encoded path for newDir.
// If the source does not exist, it is a no-op.
// If the destination already exists, an error is returned.
func relocateSessionDir(sys serverSystem, oldDir, newDir string) error {
	home, err := sys.UserHomeDir()
	if err != nil {
		return fmt.Errorf("getting home dir: %w", err)
	}
	src := filepath.Join(home, ".claude", "projects", osutil.EncodeClaudeProjectPath(oldDir))
	dst := filepath.Join(home, ".claude", "projects", osutil.EncodeClaudeProjectPath(newDir))

	// Check source exists.
	if _, err := sys.Stat(src); err != nil {
		if os.IsNotExist(err) {
			return nil // no-op
		}
		return fmt.Errorf("stat session dir: %w", err)
	}

	// Check destination does not already exist.
	if _, err := sys.Stat(dst); err == nil {
		return fmt.Errorf("destination session dir already exists")
	}

	return sys.Rename(src, dst)
}

func (s *Server) handleMoveWorktree(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.store, "channel store not configured") {
		return
	}

	var req moveWorktreeRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if req.ChannelID == "" {
		http.Error(w, "channel_id is required", http.StatusBadRequest)
		return
	}
	if req.NewName == "" {
		http.Error(w, "new_name is required", http.StatusBadRequest)
		return
	}

	newName, ok := sanitizeBranch(req.NewName)
	if !ok {
		http.Error(w, "invalid new_name", http.StatusBadRequest)
		return
	}

	ch, err := s.store.GetChannel(r.Context(), req.ChannelID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if ch == nil {
		http.Error(w, "channel not found", http.StatusNotFound)
		return
	}
	if !ch.Worktree {
		http.Error(w, "channel is not a worktree thread", http.StatusBadRequest)
		return
	}
	if ch.DirPath == "" {
		http.Error(w, "channel has no dir_path", http.StatusBadRequest)
		return
	}

	// Guard: reject if an active run is in progress.
	if s.activeChatLister != nil {
		if _, active := s.activeChatLister.ActiveChatChannelIDs()[req.ChannelID]; active {
			http.Error(w, "channel has an active run", http.StatusConflict)
			return
		}
	}
	if s.containerRegistry != nil {
		if _, running := s.containerRegistry.RunningChannelIDs(r.Context())[req.ChannelID]; running {
			http.Error(w, "channel has an active run", http.StatusConflict)
			return
		}
	}

	oldDir := ch.DirPath
	newDir := filepath.Join(filepath.Dir(oldDir), newName)
	oldBranch := "worktree/" + filepath.Base(oldDir)
	newBranch := "worktree/" + newName

	parentDir := s.resolveWorktreeParentDir(r.Context(), ch)
	if parentDir == "" {
		http.Error(w, "cannot resolve parent directory", http.StatusBadRequest)
		return
	}

	if err := s.worktreeCreator.Move(r.Context(), parentDir, oldDir, newDir, oldBranch, newBranch); err != nil {
		http.Error(w, fmt.Sprintf("failed to move worktree: %v", err), http.StatusInternalServerError)
		return
	}

	if err := relocateSessionDir(s.sys, oldDir, newDir); err != nil {
		if err.Error() == "destination session dir already exists" {
			// Session dir already in final place — don't rollback, just continue.
			http.Error(w, fmt.Sprintf("failed to relocate session dir: %v", err), http.StatusInternalServerError)
			return
		}
		// Unexpected error: try to rollback the Move.
		_ = s.worktreeCreator.Move(r.Context(), parentDir, newDir, oldDir, newBranch, oldBranch)
		http.Error(w, fmt.Sprintf("failed to relocate session dir: %v", err), http.StatusInternalServerError)
		return
	}

	if err := s.store.UpdateChannelDirPath(r.Context(), req.ChannelID, newDir); err != nil {
		http.Error(w, fmt.Sprintf("failed to update dir_path: %v", err), http.StatusInternalServerError)
		return
	}

	if err := s.store.UpdateChannelName(r.Context(), req.ChannelID, newName); err != nil {
		http.Error(w, fmt.Sprintf("failed to update name: %v", err), http.StatusInternalServerError)
		return
	}

	if s.eventsHub != nil {
		s.eventsHub.BroadcastChannelUpdated(events.ChannelUpdatedData{
			ChannelID: req.ChannelID,
			Name:      newName,
			DirPath:   newDir,
		})
	}

	writeHTTPJSON(w, http.StatusOK, moveWorktreeResponse{
		ChannelID: req.ChannelID,
		DirPath:   newDir,
		Name:      newName,
	}, s.logger)
}
