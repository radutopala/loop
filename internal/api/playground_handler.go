package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/adrg/frontmatter"
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

// validatePlaygroundDir validates the playground name (path containment + regex)
// and returns a safe directory path under the playground base directory.
func (s *Server) validatePlaygroundDir(name string) (string, error) {
	baseDir := filepath.Join(s.loopDir, "playground")
	return validatePlaygroundDirIn(baseDir, name)
}

// validatePlaygroundDirIn validates a playground name under an arbitrary base directory.
func validatePlaygroundDirIn(baseDir, name string) (string, error) {
	pgDir := filepath.Join(baseDir, filepath.Clean(name))
	if !strings.HasPrefix(pgDir, baseDir+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid or missing playground name")
	}
	if !validPlaygroundName.MatchString(name) {
		return "", fmt.Errorf("invalid or missing playground name")
	}
	return pgDir, nil
}

// resolvePlaygroundDir resolves the playground directory based on scope.
// scope "project" requires a channel_id to resolve the project dir.
func (s *Server) resolvePlaygroundDir(r *http.Request, name string) (string, error) {
	scope := r.URL.Query().Get("scope")
	if scope == "project" {
		channelID := r.URL.Query().Get("channel_id")
		if channelID == "" {
			return "", fmt.Errorf("channel_id is required for project-scoped playgrounds")
		}
		dirPath, err := s.resolveDirPath(r.Context(), "", channelID)
		if err != nil {
			return "", err
		}
		baseDir := filepath.Join(dirPath, ".loop", "playground")
		return validatePlaygroundDirIn(baseDir, name)
	}
	return s.validatePlaygroundDir(name)
}

func playgroundScopeFromRequest(r *http.Request) (scope, channelID string) {
	if r.URL.Query().Get("scope") == "project" {
		return "project", r.URL.Query().Get("channel_id")
	}
	return "global", ""
}

// validatePlaygroundPath validates a relative file path within a playground directory,
// preventing path traversal. Unlike validateFilePath, it does not require the target
// directory to exist (playground dirs are created on demand by the server).
func validatePlaygroundPath(rootDir, relativePath string) (string, error) {
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
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path traversal not allowed")
	}
	fullPath := filepath.Join(rootDir, cleaned)
	if !strings.HasPrefix(fullPath, rootDir+string(filepath.Separator)) {
		return "", fmt.Errorf("path traversal not allowed")
	}
	return fullPath, nil
}

// handlePlaygroundUpdate handles PUT /api/playground?name=...&scope=...&channel_id=... — stores code and pushes event.
func (s *Server) handlePlaygroundUpdate(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	pgDir, err := s.resolvePlaygroundDir(r, name)
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
	if err := os.MkdirAll(pgDir, 0o755); err != nil {
		http.Error(w, "creating playground dir: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if content.HTML != "" {
		if err := os.WriteFile(filepath.Join(pgDir, "index.html"), []byte(content.HTML), 0o644); err != nil {
			http.Error(w, "writing index.html: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if readme := buildReadme(content.Title, content.Description); readme != "" {
		if err := os.WriteFile(filepath.Join(pgDir, "README.md"), []byte(readme), 0o644); err != nil {
			http.Error(w, "writing README.md: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}

	// Broadcast event to all connected clients (global — no channel scoping).
	if s.eventsHub != nil {
		s.eventsHub.Broadcast(Event{
			Type:   EventPlaygroundUpdate,
			Global: true,
			Data:   content,
		})
	}

	w.WriteHeader(http.StatusOK)
}

// handlePlaygroundGet handles GET /api/playground?name=...&scope=...&channel_id=... — retrieves playground content.
func (s *Server) handlePlaygroundGet(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	pgDir, err := s.resolvePlaygroundDir(r, name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	html, _ := os.ReadFile(filepath.Join(pgDir, "index.html"))
	readme, _ := os.ReadFile(filepath.Join(pgDir, "README.md"))

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

// listPlaygroundsIn scans a base directory for valid playground subdirectories.
func listPlaygroundsIn(baseDir, scope string) []playgroundItem {
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return nil
	}
	var items []playgroundItem
	for _, e := range entries {
		if e.IsDir() && validPlaygroundName.MatchString(e.Name()) {
			item := playgroundItem{Name: e.Name(), Scope: scope}
			if readme, readErr := os.ReadFile(filepath.Join(baseDir, e.Name(), "README.md")); readErr == nil {
				item.Title, item.Description = parseReadme(readme)
			}
			items = append(items, item)
		}
	}
	return items
}

// handlePlaygroundList handles GET /api/playground/items?channel_id=... — lists all playground names.
// Returns items from both global (~/.loop/playground/) and project ({dir}/.loop/playground/) scopes.
func (s *Server) handlePlaygroundList(w http.ResponseWriter, r *http.Request) {
	globalDir := filepath.Join(s.loopDir, "playground")
	items := listPlaygroundsIn(globalDir, "global")

	// If channel_id is provided, also list project-scoped playgrounds.
	if channelID := r.URL.Query().Get("channel_id"); channelID != "" {
		if dirPath, err := s.resolveDirPath(r.Context(), "", channelID); err == nil {
			projectDir := filepath.Clean(filepath.Join(dirPath, ".loop", "playground"))
			if !strings.Contains(projectDir, "..") {
				items = append(items, listPlaygroundsIn(projectDir, "project")...)
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
// console messages from the iframe to the parent via postMessage.
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
})();
</script>`

// renderPlaygroundIndex composes and writes the full HTML document for a
// playground given its resolved absolute directory and the <base href> the
// relative assets (style.css, script.js, ES module imports) should resolve
// against. Shared by the local serve routes and the public /p/{token} route so
// the served output is byte-identical regardless of entry point.
func renderPlaygroundIndex(w http.ResponseWriter, pgDir, baseHref string) {
	rawHTML, _ := os.ReadFile(filepath.Join(pgDir, "index.html"))
	importMap, _ := os.ReadFile(filepath.Join(pgDir, "importmap.json"))

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

// servePlaygroundFile serves a single file from a playground directory,
// guarding against path traversal and setting the content type by extension.
// Shared by the local serve-file routes and the public /p/{token}/{path...}
// route.
func servePlaygroundFile(w http.ResponseWriter, pgDir, relPath string) {
	fullPath, err := validatePlaygroundPath(pgDir, relPath)
	if err != nil {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}
	data, err := os.ReadFile(fullPath)
	if err != nil {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}

	ext := filepath.Ext(fullPath)
	ct := mime.TypeByExtension(ext)
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Write(data) //nolint:errcheck
}

// handlePlaygroundServe serves the composed HTML page for a playground.
// GET /api/playground/serve/{name}?scope=...&channel_id=...
func (s *Server) handlePlaygroundServe(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	pgDir, err := s.resolvePlaygroundDir(r, name)
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
	renderPlaygroundIndex(w, pgDir, baseURL)
}

// handlePlaygroundServeFile serves individual files from a playground directory.
// GET /api/playground/serve/{name}/{path...}?scope=...&channel_id=...
func (s *Server) handlePlaygroundServeFile(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	pgDir, err := s.resolvePlaygroundDir(r, name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	servePlaygroundFile(w, pgDir, r.PathValue("path"))
}

// resolveProjectPlaygroundDir resolves a playground dir from channel_id and name path values.
func (s *Server) resolveProjectPlaygroundDir(r *http.Request) (string, error) {
	channelID := r.PathValue("channel_id")
	name := r.PathValue("name")
	if channelID == "" || name == "" {
		return "", fmt.Errorf("channel_id and name are required")
	}
	dirPath, err := s.resolveDirPath(r.Context(), "", channelID)
	if err != nil {
		return "", err
	}
	baseDir := filepath.Join(dirPath, ".loop", "playground")
	return validatePlaygroundDirIn(baseDir, name)
}

// handlePlaygroundServeProject serves the composed HTML page for a project-scoped playground.
// GET /api/playground/serve-project/{channel_id}/{name}
func (s *Server) handlePlaygroundServeProject(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	pgDir, err := s.resolveProjectPlaygroundDir(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	channelID := r.PathValue("channel_id")
	baseURL := fmt.Sprintf("/api/playground/serve-project/%s/%s/", channelID, name)
	renderPlaygroundIndex(w, pgDir, baseURL)
}

// handlePlaygroundServeProjectFile serves files from a project-scoped playground.
// GET /api/playground/serve-project/{channel_id}/{name}/{path...}
func (s *Server) handlePlaygroundServeProjectFile(w http.ResponseWriter, r *http.Request) {
	pgDir, err := s.resolveProjectPlaygroundDir(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	servePlaygroundFile(w, pgDir, r.PathValue("path"))
}

// handlePlaygroundDelete handles DELETE /api/playground?name=...&scope=...&channel_id=... — removes an entire playground.
func (s *Server) handlePlaygroundDelete(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	pgDir, err := s.resolvePlaygroundDir(r, name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if _, err := s.sys.Stat(pgDir); os.IsNotExist(err) {
		http.Error(w, "playground not found", http.StatusNotFound)
		return
	}
	if err := s.sys.RemoveAll(pgDir); err != nil {
		http.Error(w, "deleting playground: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if s.eventsHub != nil {
		scope, channelID := playgroundScopeFromRequest(r)
		s.eventsHub.Broadcast(Event{
			Type:   EventPlaygroundUpdate,
			Global: true,
			Data:   map[string]string{"name": name, "deleted": "true", "scope": scope, "channel_id": channelID},
		})
	}

	w.WriteHeader(http.StatusNoContent)
}

// handlePlaygroundFileWrite handles PUT /api/playground/file?name=...&path=...&scope=...&channel_id=... — creates or updates a file.
func (s *Server) handlePlaygroundFileWrite(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	pgDir, err := s.resolvePlaygroundDir(r, name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	fullPath, err := validatePlaygroundPath(pgDir, r.URL.Query().Get("path"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	content, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "reading body: "+err.Error(), http.StatusBadRequest)
		return
	}

	if dir := filepath.Dir(fullPath); dir != pgDir {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			http.Error(w, "creating directory: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if err := os.WriteFile(fullPath, content, 0o644); err != nil {
		http.Error(w, "writing file: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if s.eventsHub != nil {
		scope, channelID := playgroundScopeFromRequest(r)
		s.eventsHub.Broadcast(Event{
			Type:   EventPlaygroundUpdate,
			Global: true,
			Data:   map[string]string{"name": name, "scope": scope, "channel_id": channelID},
		})
	}

	w.WriteHeader(http.StatusOK)
}

// handlePlaygroundFileRead handles GET /api/playground/file?name=...&path=...&scope=...&channel_id=... — reads a file.
func (s *Server) handlePlaygroundFileRead(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	pgDir, err := s.resolvePlaygroundDir(r, name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	fullPath, err := validatePlaygroundPath(pgDir, r.URL.Query().Get("path"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	data, err := os.ReadFile(fullPath)
	if err != nil {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write(data) //nolint:errcheck
}

// handlePlaygroundFileDelete handles DELETE /api/playground/file?name=...&path=...&scope=...&channel_id=... — removes a file.
func (s *Server) handlePlaygroundFileDelete(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	pgDir, err := s.resolvePlaygroundDir(r, name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	fullPath, err := validatePlaygroundPath(pgDir, r.URL.Query().Get("path"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := os.Remove(fullPath); err != nil {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}

	if s.eventsHub != nil {
		scope, channelID := playgroundScopeFromRequest(r)
		s.eventsHub.Broadcast(Event{
			Type:   EventPlaygroundUpdate,
			Global: true,
			Data:   map[string]string{"name": name, "scope": scope, "channel_id": channelID},
		})
	}

	w.WriteHeader(http.StatusNoContent)
}

// handlePlaygroundFileList handles GET /api/playground/files?name=...&scope=...&channel_id=... — lists all files.
func (s *Server) handlePlaygroundFileList(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	pgDir, err := s.resolvePlaygroundDir(r, name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var files []string
	filepath.WalkDir(pgDir, func(path string, d os.DirEntry, _ error) error { //nolint:errcheck
		if d != nil && !d.IsDir() {
			if rel, relErr := filepath.Rel(pgDir, path); relErr == nil {
				files = append(files, rel)
			}
		}
		return nil
	})
	if files == nil {
		files = []string{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string][]string{"files": files}) //nolint:errcheck
}
