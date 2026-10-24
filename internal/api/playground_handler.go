package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/adrg/frontmatter"

	"github.com/radutopala/loop/internal/osutil"
)

var validPlaygroundName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

// playgroundContent represents a named playground item.
type playgroundContent struct {
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	HTML        string `json:"html,omitempty"`
	Description string `json:"description,omitempty"`
	Scope       string `json:"scope,omitempty"`
	ChannelID   string `json:"channel_id,omitempty"`
}

// readmeFrontmatter holds parsed YAML frontmatter fields from README.md.
type readmeFrontmatter struct {
	Title string `yaml:"title"`
}

// parseReadme extracts title (from frontmatter) and body (after frontmatter) from README.md.
func parseReadme(data []byte) (title, body string) {
	var fm readmeFrontmatter
	rest, _ := frontmatter.Parse(bytes.NewReader(data), &fm)
	return fm.Title, string(bytes.TrimSpace(rest))
}

// buildReadme composes a README.md from title and body.
func buildReadme(title, body string) string {
	if title == "" && body == "" {
		return ""
	}
	var buf bytes.Buffer
	if title != "" {
		fmt.Fprintf(&buf, "---\ntitle: %s\n---\n\n", title)
	}
	buf.WriteString(body)
	buf.WriteByte('\n')
	return buf.String()
}

// validatePlaygroundDirIn validates a playground name and locates it in
// baseDir, a playground base under project ("" for the global one).
func validatePlaygroundDirIn(project, baseDir, name string) (playgroundLoc, error) {
	pgDir := filepath.Join(baseDir, filepath.Clean(name))
	if !strings.HasPrefix(pgDir, baseDir+string(filepath.Separator)) || !validPlaygroundName.MatchString(name) {
		return playgroundLoc{}, fmt.Errorf("invalid or missing playground name")
	}
	return playgroundLoc{project: project, base: baseDir, name: name}, nil
}

func playgroundScopeFromRequest(r *http.Request) (scope, channelID string) {
	if r.URL.Query().Get("scope") == "project" {
		return "project", r.URL.Query().Get("channel_id")
	}
	return "global", ""
}

// validatePlaygroundPath validates a file path relative to a playground dir
// and returns it cleaned. It refuses one that names the dir itself or climbs
// out of it by name; where its symlinks lead is left to the playground's
// os.Root.
func validatePlaygroundPath(relativePath string) (string, error) {
	if relativePath == "" {
		return "", fmt.Errorf("path is required")
	}
	if filepath.IsAbs(relativePath) {
		return "", fmt.Errorf("absolute paths are not allowed")
	}
	if strings.ContainsRune(relativePath, 0) {
		return "", fmt.Errorf("path contains invalid characters")
	}
	cleaned := filepath.Clean(relativePath)
	if cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", errors.New(errPlaygroundTraversal)
	}
	return cleaned, nil
}

// handlePlaygroundUpdate handles PUT /api/playground?name=...&scope=...&channel_id=... — stores code and pushes event.
func (s *playgroundService) handlePlaygroundUpdate(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	loc, err := s.resolvePlaygroundDir(r, name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var content playgroundContent
	if err := json.NewDecoder(r.Body).Decode(&content); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	content.Name = name
	content.Scope, content.ChannelID = playgroundScopeFromRequest(r)
	pg, err := loc.open(true)
	if err != nil {
		playgroundFileError(w, err, "creating playground dir: ")
		return
	}
	defer pg.Close()

	if content.HTML != "" {
		if err := pg.WriteFile("index.html", []byte(content.HTML), 0o644); err != nil {
			playgroundFileError(w, err, "writing index.html: ")
			return
		}
	}
	if readme := buildReadme(content.Title, content.Description); readme != "" {
		if err := pg.WriteFile("README.md", []byte(readme), 0o644); err != nil {
			playgroundFileError(w, err, "writing README.md: ")
			return
		}
	}

	// Broadcast event to all connected clients (global — no channel scoping).
	if s.deps.eventsHub != nil {
		s.deps.eventsHub.Broadcast(Event{
			Type:   EventPlaygroundUpdate,
			Global: true,
			Data:   content,
		})
	}

	w.WriteHeader(http.StatusOK)
}

// handlePlaygroundGet handles GET /api/playground?name=...&scope=...&channel_id=... — retrieves playground content.
func (s *playgroundService) handlePlaygroundGet(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	loc, err := s.resolvePlaygroundDir(r, name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	pg, ok := openPlayground(w, loc, http.StatusBadRequest)
	if !ok {
		return
	}
	if pg != nil {
		defer pg.Close()
	}
	html, _ := readPlaygroundFile(pg, "index.html")
	readme, _ := readPlaygroundFile(pg, "README.md")

	if len(html) == 0 {
		http.Error(w, "no playground content", http.StatusNotFound)
		return
	}

	title, body := parseReadme(readme)
	scope, channelID := playgroundScopeFromRequest(r)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(playgroundContent{ //nolint:errcheck
		Name:        name,
		Title:       title,
		HTML:        string(html),
		Description: body,
		Scope:       scope,
		ChannelID:   channelID,
	})
}

type playgroundItem struct {
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	Scope       string `json:"scope"` // "global" or "project"
}

// listPlaygroundsIn scans the base dir of l (whose name is unused) for
// valid playground subdirectories, reading each README.md through the base
// as a root.
func listPlaygroundsIn(l playgroundLoc, scope string) []playgroundItem {
	base, err := l.openBase(false)
	if err != nil {
		return nil
	}
	defer base.Close()
	entries, _ := fs.ReadDir(base.FS(), ".")
	var items []playgroundItem
	for _, e := range entries {
		if e.IsDir() && validPlaygroundName.MatchString(e.Name()) {
			item := playgroundItem{Name: e.Name(), Scope: scope}
			if readme, readErr := base.ReadFile(path.Join(e.Name(), "README.md")); readErr == nil {
				item.Title, item.Description = parseReadme(readme)
			}
			items = append(items, item)
		}
	}
	return items
}

// handlePlaygroundList handles GET /api/playground/items?channel_id=... — lists all playground names.
// Returns items from both global (~/.loop/playground/) and project ({dir}/.loop/playground/) scopes.
func (s *playgroundService) handlePlaygroundList(w http.ResponseWriter, r *http.Request) {
	globalDir := filepath.Join(s.deps.loopDir, "playground")
	items := listPlaygroundsIn(playgroundLoc{base: globalDir}, "global")

	// If channel_id is provided, also list project-scoped playgrounds.
	if channelID := r.URL.Query().Get("channel_id"); channelID != "" {
		if dirPath, err := s.projectPlaygroundDir(r.Context(), channelID); err == nil {
			projectDir := filepath.Clean(filepath.Join(dirPath, ".loop", "playground"))
			if !strings.Contains(projectDir, "..") {
				items = append(items, listPlaygroundsIn(playgroundLoc{project: dirPath, base: projectDir}, "project")...)
			}
		}
	}

	if items == nil {
		items = []playgroundItem{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string][]playgroundItem{"items": items}) //nolint:errcheck
}

// consoleBridgeScript is injected into the served playground HTML to forward
// console messages from the iframe to the parent via postMessage, and to give
// the page window.loop.state, which the parent panel answers.
const consoleBridgeScript = `<script>
(function() {
  var orig = { log: console.log, warn: console.warn, error: console.error, info: console.info, debug: console.debug };
  function send(level, args) {
    try { parent.postMessage({ type: "playground-console", level: level, message: Array.prototype.map.call(args, String).join(" ") }, "*"); } catch(e) {}
  }
  console.log = function() { send(1, arguments); orig.log.apply(console, arguments); };
  console.warn = function() { send(2, arguments); orig.warn.apply(console, arguments); };
  console.error = function() { send(3, arguments); orig.error.apply(console, arguments); };
  console.info = function() { send(1, arguments); orig.info.apply(console, arguments); };
  console.debug = function() { send(0, arguments); orig.debug.apply(console, arguments); };
  window.onerror = function(msg) { send(3, ["Error: " + msg]); };

  // window.loop.state keeps the playground's state in the daemon, through
  // the panel: get() and set(patch) resolve with the whole state (set merges
  // patch into it, a null value removing its key), and subscribe(fn) calls
  // fn with it whenever it changes, from here or from the API.
  var pending = {}, nextId = 0, listeners = [];
  function request(op, patch) {
    return new Promise(function(resolve, reject) {
      var id = ++nextId;
      var timer = setTimeout(function() { delete pending[id]; reject(new Error("loop.state: no answer from the panel")); }, 10000);
      pending[id] = function(msg) { clearTimeout(timer); if (msg.error) reject(new Error(msg.error)); else resolve(msg.state); };
      try { parent.postMessage({ type: "playground-state", id: id, op: op, patch: patch }, "*"); } catch(e) { clearTimeout(timer); delete pending[id]; reject(e); }
    });
  }
  window.addEventListener("message", function(e) {
    if (e.source !== parent || !e.data) return;
    if (e.data.type === "playground-state-reply" && pending[e.data.id]) {
      var done = pending[e.data.id]; delete pending[e.data.id]; done(e.data);
    } else if (e.data.type === "playground-state-changed") {
      listeners.slice().forEach(function(fn) { try { fn(e.data.state); } catch(err) { console.error(err); } });
    }
  });
  window.loop = { state: {
    get: function() { return request("get"); },
    set: function(patch) { return request("set", patch); },
    subscribe: function(fn) { listeners.push(fn); return function() { listeners = listeners.filter(function(l) { return l !== fn; }); }; }
  } };
})();
</script>`

// servePlayground serves loc's page when rel is empty, else its file rel,
// answering with refuseStatus when loc leads outside its base. Shared by the
// local serve routes, the capability route and the public /p/{token} routes.
func (s *playgroundService) servePlayground(w http.ResponseWriter, loc playgroundLoc, rel, baseHref string, refuseStatus int) {
	pg, ok := openPlayground(w, loc, refuseStatus)
	if !ok {
		return
	}
	if pg != nil {
		defer pg.Close()
	}
	if rel == "" {
		s.renderPlaygroundIndex(w, pg, baseHref)
		return
	}
	s.servePlaygroundFile(w, pg, rel)
}

// renderPlaygroundIndex composes and writes the full HTML document for a
// playground given its opened dir (nil when missing) and the <base href> the
// relative assets (style.css, script.js, ES module imports) should resolve
// against. Shared by the local serve routes and the public /p/{token} route so
// the served output is byte-identical regardless of entry point.
func (s *playgroundService) renderPlaygroundIndex(w http.ResponseWriter, pg *os.Root, baseHref string) {
	rawHTML, _ := readPlaygroundFile(pg, "index.html")
	importMap, _ := readPlaygroundFile(pg, "importmap.json")

	var importMapBlock string
	if len(importMap) > 0 {
		importMapBlock = fmt.Sprintf("<script type=\"importmap\">%s</script>\n", string(importMap))
	}

	body := string(rawHTML)
	if body == "" {
		body = `<div style="display:flex;align-items:center;justify-content:center;height:100vh;margin:0;color:#555;font-family:system-ui,sans-serif;font-size:13px">Playground — waiting for code from agent</div>`
	}

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "<!DOCTYPE html>\n<html>\n<head>\n<meta charset=\"utf-8\">\n")
	fmt.Fprintf(&buf, "<base href=\"%s\">\n", html.EscapeString(baseHref))
	buf.WriteString("<link rel=\"stylesheet\" href=\"style.css\">\n</head>\n<body style=\"margin:0;background:#000\">\n")
	buf.WriteString(consoleBridgeScript)
	buf.WriteByte('\n')
	buf.WriteString(body)
	buf.WriteByte('\n')
	if importMapBlock != "" {
		buf.WriteString(importMapBlock)
	}
	buf.WriteString("<script type=\"module\" src=\"script.js\"></script>\n")
	buf.WriteString("</body>\n</html>")

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(buf.Bytes()) //nolint:errcheck
}

// servePlaygroundFile serves a single file from an opened playground dir
// (nil when missing), guarding against path traversal and setting the
// content type by extension.
func (s *playgroundService) servePlaygroundFile(w http.ResponseWriter, pg *os.Root, relPath string) {
	rel, err := validatePlaygroundPath(relPath)
	if err != nil {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}
	data, err := readPlaygroundFile(pg, rel)
	if err != nil {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}

	ext := filepath.Ext(rel)
	ct := mime.TypeByExtension(ext)
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Write(data) //nolint:errcheck
}

// handlePlaygroundServe serves the composed HTML page for a playground.
// GET /api/playground/serve/{name}?scope=...&channel_id=...
func (s *playgroundService) handlePlaygroundServe(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	loc, err := s.resolvePlaygroundDir(r, name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// <base> ensures relative URLs (style.css, script.js, import './utils.js')
	// resolve against the playground's serve path, not the document URL.
	baseURL := fmt.Sprintf("/api/playground/serve/%s/", name)
	if scope := r.URL.Query().Get("scope"); scope == "project" {
		channelID := r.URL.Query().Get("channel_id")
		baseURL = fmt.Sprintf("/api/playground/serve-project/%s/%s/", channelID, name)
	}
	s.servePlayground(w, loc, "", baseURL, http.StatusBadRequest)
}

// handlePlaygroundServeFile serves individual files from a playground directory.
// GET /api/playground/serve/{name}/{path...}?scope=...&channel_id=...
func (s *playgroundService) handlePlaygroundServeFile(w http.ResponseWriter, r *http.Request) {
	loc, err := s.resolvePlaygroundDir(r, r.PathValue("name"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.servePlayground(w, loc, r.PathValue("path"), "", http.StatusBadRequest)
}

// handlePlaygroundServeProject serves the composed HTML page for a project-scoped playground.
// GET /api/playground/serve-project/{channel_id}/{name}
func (s *playgroundService) handlePlaygroundServeProject(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	loc, err := s.resolveProjectPlaygroundDir(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	channelID := r.PathValue("channel_id")
	baseURL := fmt.Sprintf("/api/playground/serve-project/%s/%s/", channelID, name)
	s.servePlayground(w, loc, "", baseURL, http.StatusBadRequest)
}

// handlePlaygroundServeProjectFile serves files from a project-scoped playground.
// GET /api/playground/serve-project/{channel_id}/{name}/{path...}
func (s *playgroundService) handlePlaygroundServeProjectFile(w http.ResponseWriter, r *http.Request) {
	loc, err := s.resolveProjectPlaygroundDir(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.servePlayground(w, loc, r.PathValue("path"), "", http.StatusBadRequest)
}

// handlePlaygroundDelete handles DELETE /api/playground?name=...&scope=...&channel_id=... — removes an entire playground.
func (s *playgroundService) handlePlaygroundDelete(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	loc, err := s.resolvePlaygroundDir(r, name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// The playground is removed through its base as a root: a playground
	// dir that is a symlink loses the link, never what it points to.
	base, err := loc.openBase(false)
	if err == nil {
		defer base.Close()
		_, err = base.Stat(loc.name)
	}
	if osutil.IsEscape(err) {
		http.Error(w, errPlaygroundTraversal, http.StatusBadRequest)
		return
	}
	if err != nil {
		http.Error(w, "playground not found", http.StatusNotFound)
		return
	}
	removeAll := s.removeAll
	if removeAll == nil {
		removeAll = (*os.Root).RemoveAll
	}
	if err := removeAll(base, loc.name); err != nil {
		http.Error(w, "deleting playground: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if s.deps.eventsHub != nil {
		scope, channelID := playgroundScopeFromRequest(r)
		s.deps.eventsHub.Broadcast(Event{
			Type:   EventPlaygroundUpdate,
			Global: true,
			Data:   map[string]string{"name": name, "deleted": "true", "scope": scope, "channel_id": channelID},
		})
	}

	w.WriteHeader(http.StatusNoContent)
}

// handlePlaygroundFileWrite handles PUT /api/playground/file?name=...&path=...&scope=...&channel_id=... — creates or updates a file.
func (s *playgroundService) handlePlaygroundFileWrite(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	loc, err := s.resolvePlaygroundDir(r, name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	rel, err := validatePlaygroundPath(r.URL.Query().Get("path"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	content, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "reading body: "+err.Error(), http.StatusBadRequest)
		return
	}

	pg, err := loc.open(true)
	if err != nil {
		playgroundFileError(w, err, "creating directory: ")
		return
	}
	defer pg.Close()
	if dir := filepath.Dir(rel); dir != "." {
		if err := pg.MkdirAll(dir, 0o755); err != nil {
			playgroundFileError(w, err, "creating directory: ")
			return
		}
	}
	if err := pg.WriteFile(rel, content, 0o644); err != nil {
		playgroundFileError(w, err, "writing file: ")
		return
	}

	if s.deps.eventsHub != nil {
		scope, channelID := playgroundScopeFromRequest(r)
		s.deps.eventsHub.Broadcast(Event{
			Type:   EventPlaygroundUpdate,
			Global: true,
			Data:   map[string]string{"name": name, "scope": scope, "channel_id": channelID},
		})
	}

	w.WriteHeader(http.StatusOK)
}

// handlePlaygroundFileRead handles GET /api/playground/file?name=...&path=...&scope=...&channel_id=... — reads a file.
func (s *playgroundService) handlePlaygroundFileRead(w http.ResponseWriter, r *http.Request) {
	pg, rel, ok := s.playgroundFileRequest(w, r)
	if !ok {
		return
	}
	if pg != nil {
		defer pg.Close()
	}
	data, err := readPlaygroundFile(pg, rel)
	if osutil.IsEscape(err) {
		http.Error(w, errPlaygroundTraversal, http.StatusBadRequest)
		return
	}
	if err != nil {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write(data) //nolint:errcheck
}

// handlePlaygroundFileDelete handles DELETE /api/playground/file?name=...&path=...&scope=...&channel_id=... — removes a file.
func (s *playgroundService) handlePlaygroundFileDelete(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	pg, rel, ok := s.playgroundFileRequest(w, r)
	if !ok {
		return
	}
	if pg == nil {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}
	defer pg.Close()
	// Remove takes a symlink itself away, never what it points to.
	if err := pg.Remove(rel); osutil.IsEscape(err) {
		http.Error(w, errPlaygroundTraversal, http.StatusBadRequest)
		return
	} else if err != nil {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}

	if s.deps.eventsHub != nil {
		scope, channelID := playgroundScopeFromRequest(r)
		s.deps.eventsHub.Broadcast(Event{
			Type:   EventPlaygroundUpdate,
			Global: true,
			Data:   map[string]string{"name": name, "scope": scope, "channel_id": channelID},
		})
	}

	w.WriteHeader(http.StatusNoContent)
}

// handlePlaygroundFileList handles GET /api/playground/files?name=...&scope=...&channel_id=... — lists all files.
func (s *playgroundService) handlePlaygroundFileList(w http.ResponseWriter, r *http.Request) {
	loc, err := s.resolvePlaygroundDir(r, r.URL.Query().Get("name"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	pg, ok := openPlayground(w, loc, http.StatusBadRequest)
	if !ok {
		return
	}
	var files []string
	if pg != nil {
		defer pg.Close()
		// WalkDir lists symlinks as entries and doesn't follow them.
		fs.WalkDir(pg.FS(), ".", func(p string, d fs.DirEntry, _ error) error { //nolint:errcheck
			if d != nil && !d.IsDir() {
				files = append(files, filepath.FromSlash(p))
			}
			return nil
		})
	}
	if files == nil {
		files = []string{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string][]string{"files": files}) //nolint:errcheck
}

// playgroundFileRequest resolves the playground and file path of a
// /api/playground/file request (?name=&path=&scope=&channel_id=) and opens
// the playground (nil when missing), answering the request itself when it
// can't.
func (s *playgroundService) playgroundFileRequest(w http.ResponseWriter, r *http.Request) (*os.Root, string, bool) {
	loc, err := s.resolvePlaygroundDir(r, r.URL.Query().Get("name"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return nil, "", false
	}
	rel, err := validatePlaygroundPath(r.URL.Query().Get("path"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return nil, "", false
	}
	pg, ok := openPlayground(w, loc, http.StatusBadRequest)
	return pg, rel, ok
}
