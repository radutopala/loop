package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/radutopala/loop/internal/filewatch"
)

const fileWatchMsgWatch = "watch"

// fileWatchRef names a file or directory in one of a channel's workspace
// roots, as the editor knows it.
type fileWatchRef struct {
	Root int    `json:"root"`
	Path string `json:"path"`
}

// fileWatchRequest replaces what the connection watches: the files open in
// the editor and the directories expanded in its tree.
type fileWatchRequest struct {
	Type  string         `json:"type"`
	Files []fileWatchRef `json:"files,omitempty"`
	Dirs  []fileWatchRef `json:"dirs,omitempty"`
}

// fileWatchChange tells the client a watched file changed on disk, or a
// watched directory's entries did.
type fileWatchChange struct {
	Type    filewatch.Kind `json:"type"`
	Root    int            `json:"root"`
	Path    string         `json:"path,omitempty"`
	Message string         `json:"message,omitempty"`
}

func fileWatchKey(ref fileWatchRef) string { return strconv.Itoa(ref.Root) + "\x00" + ref.Path }

func parseFileWatchKey(key string) fileWatchRef {
	root, path, _ := strings.Cut(key, "\x00")
	n, _ := strconv.Atoi(root)
	return fileWatchRef{Root: n, Path: path}
}

// handleFileWatch serves GET /api/channels/{id}/files/watch, a WebSocket
// that reports changes to the files and directories the client asks for,
// whoever makes them. Each "watch" message replaces the set; paths that
// don't resolve inside the channel's roots are left out.
func (s *Server) handleFileWatch(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.store, "channel listing not configured") {
		return
	}
	channelID := r.PathValue("id")
	conn, err := wsUpgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	var mu sync.Mutex
	send := func(c fileWatchChange) {
		mu.Lock()
		defer mu.Unlock()
		_ = conn.WriteJSON(c)
	}
	watcher, err := s.newFileWatcher()
	if err != nil {
		s.logger.Warn("file watch: starting watcher", "channel_id", channelID, "error", err)
		send(fileWatchChange{Type: "error", Message: err.Error()})
		return
	}
	sess := filewatch.NewSession(watcher, s.fileWatchDebounce)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go func() {
		defer cancel()
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var req fileWatchRequest
			if json.Unmarshal(data, &req) != nil || req.Type != fileWatchMsgWatch {
				continue
			}
			files, dirs := s.fileWatchTargets(ctx, channelID, req)
			sess.Set(files, dirs)
		}
	}()
	sess.Run(ctx, func(c filewatch.Change) {
		ref := parseFileWatchKey(c.Key)
		send(fileWatchChange{Type: c.Kind, Root: ref.Root, Path: ref.Path})
	})
}

// fileWatchTargets resolves a request's paths against the channel's roots,
// with the same checks as reading them.
func (s *Server) fileWatchTargets(ctx context.Context, channelID string, req fileWatchRequest) (files, dirs []filewatch.Target) {
	roots := map[int]string{}
	resolve := func(ref fileWatchRef) (filewatch.Target, bool) {
		root, ok := roots[ref.Root]
		if !ok {
			dir, err := s.rootDirByIndex(ctx, channelID, ref.Root)
			if err != nil {
				dir = ""
			}
			roots[ref.Root], root = dir, dir
		}
		if root == "" {
			return filewatch.Target{}, false
		}
		rel := ref.Path
		if rel == "" {
			rel = "."
		}
		abs, err := s.validateFilePath(root, rel)
		if err != nil {
			return filewatch.Target{}, false
		}
		return filewatch.Target{Key: fileWatchKey(ref), Path: abs}, true
	}
	for _, ref := range req.Files {
		if t, ok := resolve(ref); ok {
			files = append(files, t)
		}
	}
	for _, ref := range req.Dirs {
		if t, ok := resolve(ref); ok {
			dirs = append(dirs, t)
		}
	}
	return files, dirs
}
