package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"regexp"

	"github.com/adrg/frontmatter"
)

var validPlaygroundName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

// playgroundContent represents a named playground item.
type playgroundContent struct {
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	HTML        string `json:"html,omitempty"`
	Description string `json:"description,omitempty"`
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

func (s *Server) playgroundDir(name string) string {
	return filepath.Join(s.loopDir, "playground", name)
}

func parsePlaygroundName(r *http.Request) (string, bool) {
	name := r.URL.Query().Get("name")
	if name == "" {
		return "", false
	}
	return name, validPlaygroundName.MatchString(name)
}

// handlePlaygroundUpdate handles PUT /api/playground?name=... — stores code and pushes event.
func (s *Server) handlePlaygroundUpdate(w http.ResponseWriter, r *http.Request) {
	name, valid := parsePlaygroundName(r)
	if !valid {
		http.Error(w, "invalid or missing playground name", http.StatusBadRequest)
		return
	}

	var content playgroundContent
	if err := json.NewDecoder(r.Body).Decode(&content); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	content.Name = name

	pgDir := s.playgroundDir(name)
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

// handlePlaygroundGet handles GET /api/playground?name=... — retrieves playground content.
func (s *Server) handlePlaygroundGet(w http.ResponseWriter, r *http.Request) {
	name, valid := parsePlaygroundName(r)
	if !valid {
		http.Error(w, "invalid or missing playground name", http.StatusBadRequest)
		return
	}

	pgDir := s.playgroundDir(name)
	html, _ := os.ReadFile(filepath.Join(pgDir, "index.html"))
	readme, _ := os.ReadFile(filepath.Join(pgDir, "README.md"))

	if len(html) == 0 {
		http.Error(w, "no playground content", http.StatusNotFound)
		return
	}

	title, body := parseReadme(readme)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(playgroundContent{ //nolint:errcheck
		Name:        name,
		Title:       title,
		HTML:        string(html),
		Description: body,
	})
}

type playgroundItem struct {
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
}

// handlePlaygroundList handles GET /api/playground/items — lists all playground names.
func (s *Server) handlePlaygroundList(w http.ResponseWriter, _ *http.Request) {
	baseDir := filepath.Join(s.loopDir, "playground")
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string][]playgroundItem{"items": {}}) //nolint:errcheck
		return
	}

	var items []playgroundItem
	for _, e := range entries {
		if e.IsDir() && validPlaygroundName.MatchString(e.Name()) {
			item := playgroundItem{Name: e.Name()}
			if readme, readErr := os.ReadFile(filepath.Join(baseDir, e.Name(), "README.md")); readErr == nil {
				item.Title, item.Description = parseReadme(readme)
			}
			items = append(items, item)
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

// handlePlaygroundServe serves the composed HTML page for a playground.
// GET /api/playground/serve/{name}
func (s *Server) handlePlaygroundServe(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !validPlaygroundName.MatchString(name) {
		http.Error(w, "invalid playground name", http.StatusBadRequest)
		return
	}

	pgDir := s.playgroundDir(name)
	html, _ := os.ReadFile(filepath.Join(pgDir, "index.html"))
	importMap, _ := os.ReadFile(filepath.Join(pgDir, "importmap.json"))

	var importMapBlock string
	if len(importMap) > 0 {
		importMapBlock = fmt.Sprintf("<script type=\"importmap\">%s</script>\n", string(importMap))
	}

	body := string(html)
	if body == "" {
		body = `<div style="padding:20px;color:#888;text-align:center">Playground — waiting for code from agent</div>`
	}

	// <base> ensures relative URLs (style.css, script.js, import './utils.js')
	// resolve against the playground's serve path, not the document URL.
	baseURL := fmt.Sprintf("/api/playground/serve/%s/", name)

	var buf bytes.Buffer
	fmt.Fprintf(&buf, "<!DOCTYPE html>\n<html>\n<head>\n<meta charset=\"utf-8\">\n")
	fmt.Fprintf(&buf, "<base href=\"%s\">\n", baseURL)
	buf.WriteString("<link rel=\"stylesheet\" href=\"style.css\">\n</head>\n<body>\n")
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

// handlePlaygroundServeFile serves individual files from a playground directory.
// GET /api/playground/serve/{name}/{path...}
func (s *Server) handlePlaygroundServeFile(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !validPlaygroundName.MatchString(name) {
		http.Error(w, "invalid playground name", http.StatusBadRequest)
		return
	}

	// Go's mux normalizes ".." segments before they reach the handler.
	filePath := filepath.Clean(r.PathValue("path"))
	fullPath := filepath.Join(s.playgroundDir(name), filePath)
	data, err := os.ReadFile(fullPath)
	if err != nil {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}

	// Set content type based on extension.
	ext := filepath.Ext(filePath)
	ct := mime.TypeByExtension(ext)
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Write(data) //nolint:errcheck
}

// handlePlaygroundDelete handles DELETE /api/playground?name=... — removes an entire playground.
func (s *Server) handlePlaygroundDelete(w http.ResponseWriter, r *http.Request) {
	name, valid := parsePlaygroundName(r)
	if !valid {
		http.Error(w, "invalid or missing playground name", http.StatusBadRequest)
		return
	}

	pgDir := s.playgroundDir(name)
	if _, err := os.Stat(pgDir); os.IsNotExist(err) {
		http.Error(w, "playground not found", http.StatusNotFound)
		return
	}
	if err := os.RemoveAll(pgDir); err != nil {
		http.Error(w, "deleting playground: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if s.eventsHub != nil {
		s.eventsHub.Broadcast(Event{
			Type:   EventPlaygroundUpdate,
			Global: true,
			Data:   map[string]string{"name": name, "deleted": "true"},
		})
	}

	w.WriteHeader(http.StatusNoContent)
}

// handlePlaygroundFileWrite handles PUT /api/playground/file?name=...&path=... — creates or updates a file.
func (s *Server) handlePlaygroundFileWrite(w http.ResponseWriter, r *http.Request) {
	name, valid := parsePlaygroundName(r)
	if !valid {
		http.Error(w, "invalid or missing playground name", http.StatusBadRequest)
		return
	}
	filePath := r.URL.Query().Get("path")
	if filePath == "" {
		http.Error(w, "path is required", http.StatusBadRequest)
		return
	}

	content, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "reading body: "+err.Error(), http.StatusBadRequest)
		return
	}

	cleaned := filepath.Clean(filePath)
	fullPath := filepath.Join(s.playgroundDir(name), cleaned)

	if dir := filepath.Dir(fullPath); dir != s.playgroundDir(name) {
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
		s.eventsHub.Broadcast(Event{
			Type:   EventPlaygroundUpdate,
			Global: true,
			Data:   map[string]string{"name": name},
		})
	}

	w.WriteHeader(http.StatusOK)
}

// handlePlaygroundFileRead handles GET /api/playground/file?name=...&path=... — reads a file.
func (s *Server) handlePlaygroundFileRead(w http.ResponseWriter, r *http.Request) {
	name, valid := parsePlaygroundName(r)
	if !valid {
		http.Error(w, "invalid or missing playground name", http.StatusBadRequest)
		return
	}
	filePath := r.URL.Query().Get("path")
	if filePath == "" {
		http.Error(w, "path is required", http.StatusBadRequest)
		return
	}

	cleaned := filepath.Clean(filePath)
	fullPath := filepath.Join(s.playgroundDir(name), cleaned)
	data, err := os.ReadFile(fullPath)
	if err != nil {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write(data) //nolint:errcheck
}

// handlePlaygroundFileDelete handles DELETE /api/playground/file?name=...&path=... — removes a file.
func (s *Server) handlePlaygroundFileDelete(w http.ResponseWriter, r *http.Request) {
	name, valid := parsePlaygroundName(r)
	if !valid {
		http.Error(w, "invalid or missing playground name", http.StatusBadRequest)
		return
	}
	filePath := r.URL.Query().Get("path")
	if filePath == "" {
		http.Error(w, "path is required", http.StatusBadRequest)
		return
	}

	cleaned := filepath.Clean(filePath)
	fullPath := filepath.Join(s.playgroundDir(name), cleaned)
	if err := os.Remove(fullPath); err != nil {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}

	if s.eventsHub != nil {
		s.eventsHub.Broadcast(Event{
			Type:   EventPlaygroundUpdate,
			Global: true,
			Data:   map[string]string{"name": name},
		})
	}

	w.WriteHeader(http.StatusNoContent)
}

// handlePlaygroundFileList handles GET /api/playground/files?name=... — lists all files.
func (s *Server) handlePlaygroundFileList(w http.ResponseWriter, r *http.Request) {
	name, valid := parsePlaygroundName(r)
	if !valid {
		http.Error(w, "invalid or missing playground name", http.StatusBadRequest)
		return
	}

	pgDir := s.playgroundDir(name)
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
