package api

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"path/filepath"
	"regexp"

	"github.com/radutopala/loop/internal/osutil"
	"github.com/radutopala/loop/internal/plugin"
)

// pluginMemoryMax caps a plugin's memory file.
const pluginMemoryMax = 256 << 10

// pluginMemoryName is the name a plugin gives one of its memory files.
var pluginMemoryName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

type pluginMemoryRequest struct {
	Content string `json:"content"`
}

// pluginMemoryPath returns where the calling plugin's memory file name
// lives: plugin-<plugin>-<name>.md in the memory dir of its channel's dir,
// which search_memory indexes before every search. Otherwise it answers
// the request and returns false.
func (s *Server) pluginMemoryPath(w http.ResponseWriter, r *http.Request) (string, bool) {
	p, ch, ok := s.pluginCaller(w, r, plugin.CapMemoryWrite)
	if !ok {
		return "", false
	}
	name := r.PathValue("name")
	if !pluginMemoryName.MatchString(name) {
		http.Error(w, "name must be lowercase letters, digits and dashes, at most 64", http.StatusBadRequest)
		return "", false
	}
	if ch.DirPath == "" {
		http.Error(w, "the channel has no dir", http.StatusConflict)
		return "", false
	}
	dir, err := claudeMemoryDir(s.sys, ch.DirPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return "", false
	}
	return filepath.Join(dir, "plugin-"+p.PluginID+"-"+name+".md"), true
}

// handlePutPluginMemory writes one of the calling plugin's memory files, so
// the channel's agents find it with search_memory.
func (s *Server) handlePutPluginMemory(w http.ResponseWriter, r *http.Request) {
	path, ok := s.pluginMemoryPath(w, r)
	if !ok {
		return
	}
	var req pluginMemoryRequest
	r.Body = http.MaxBytesReader(w, r.Body, pluginMemoryMax)
	if !decodeJSON(w, r, &req) {
		return
	}
	if err := s.sys.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		http.Error(w, "creating the memory dir failed", http.StatusInternalServerError)
		return
	}
	if err := s.sys.WriteFile(path, []byte(req.Content), 0o644); err != nil {
		http.Error(w, "writing the memory file failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleDeletePluginMemory removes one of the calling plugin's memory
// files; one that isn't there is already gone.
func (s *Server) handleDeletePluginMemory(w http.ResponseWriter, r *http.Request) {
	path, ok := s.pluginMemoryPath(w, r)
	if !ok {
		return
	}
	if err := s.sys.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		http.Error(w, "removing the memory file failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// claudeMemoryDir returns the memory dir Claude Code keeps for dirPath, the
// one Loop indexes for the channels of that dir.
func claudeMemoryDir(sys serverSystem, dirPath string) (string, error) {
	home, err := sys.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("getting home dir: %w", err)
	}
	return filepath.Join(home, ".claude", "projects", osutil.EncodeClaudeProjectPath(dirPath), "memory"), nil
}
