// Package pluginsdktest tests Loop plugins: FakeLoop serves the Plugin API v1
// from memory, Plugin calls a plugin the way Loop does, and Check drives a
// plugin through the Loop → plugin contract.
package pluginsdktest

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/pluginsdk"
)

// Config is what a FakeLoop serves.
type Config struct {
	// Manifest, when set, limits the plugin to its capabilities and agents,
	// as installing it would. Without one every call is allowed.
	Manifest *pluginsdk.Manifest
	// Channel is GET /channel's answer; its ID defaults to "ch1".
	Channel pluginsdk.Channel
	// Files are the channel's files by slash path.
	Files map[string]string
	// Git is GET /channel/git/recent's answer; nil answers 409, as for a
	// dir that isn't a git repository.
	Git *pluginsdk.GitRecent
	// Hotspots is GET /channel/quality/hotspots's answer; nil answers 503,
	// as before the first quality scan.
	Hotspots []pluginsdk.Hotspot
}

// Event is an event the plugin sent to its panels.
type Event struct {
	Type string
	Data json.RawMessage
}

// Notification is a desktop notification the plugin asked for.
type Notification struct {
	Title string
	Body  string
}

// StartedRun is an agent run the plugin started.
type StartedRun struct {
	pluginsdk.RunRequest
	pluginsdk.Run
}

// FakeLoop serves the Plugin API v1 from memory and records what the plugin
// sends it.
type FakeLoop struct {
	// URL is the fake's LOOP_API_URL.
	URL string
	// TokenFile is the fake's LOOP_PLUGIN_TOKEN_FILE.
	TokenFile string

	cfg   Config
	token string

	mu            sync.Mutex
	events        []Event
	notifications []Notification
	runs          []StartedRun
}

// NewFakeLoop starts a FakeLoop serving cfg, stopped when t ends.
func NewFakeLoop(t testing.TB, cfg Config) *FakeLoop {
	t.Helper()
	if cfg.Channel.ID == "" {
		cfg.Channel.ID = "ch1"
	}
	f := &FakeLoop{cfg: cfg, token: "plugin-token", TokenFile: filepath.Join(t.TempDir(), "plugin-token")}
	require.NoError(t, os.WriteFile(f.TokenFile, []byte(f.token+"\n"), 0o600), "writing the plugin token")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/plugin/v1/channel", f.channel)
	mux.HandleFunc("GET /api/plugin/v1/channel/files", f.allow(pluginsdk.CapFilesRead, f.file))
	mux.HandleFunc("GET /api/plugin/v1/channel/tree", f.allow(pluginsdk.CapFilesRead, f.tree))
	mux.HandleFunc("GET /api/plugin/v1/channel/git/recent", f.allow(pluginsdk.CapGitRead, f.git))
	mux.HandleFunc("GET /api/plugin/v1/channel/quality/hotspots", f.allow(pluginsdk.CapQualityRead, f.hotspots))
	mux.HandleFunc("POST /api/plugin/v1/runs", f.startRun)
	mux.HandleFunc("POST /api/plugin/v1/events", f.event)
	mux.HandleFunc("POST /api/plugin/v1/notify", f.allow(pluginsdk.CapUINotify, f.notify))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+f.token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	f.URL = srv.URL
	return f
}

// Client returns a Plugin API client for the fake.
func (f *FakeLoop) Client() *pluginsdk.Client {
	return pluginsdk.NewClient(f.URL, f.TokenFile)
}

// Events returns the events the plugin sent, oldest first.
func (f *FakeLoop) Events() []Event {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.events)
}

// Notifications returns the notifications the plugin asked for, oldest
// first.
func (f *FakeLoop) Notifications() []Notification {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.notifications)
}

// Runs returns the agent runs the plugin started, oldest first.
func (f *FakeLoop) Runs() []StartedRun {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.runs)
}

// approved reports whether the plugin may use capability.
func (f *FakeLoop) approved(capability string) bool {
	return f.cfg.Manifest == nil || slices.Contains(f.cfg.Manifest.Capabilities, capability)
}

func (f *FakeLoop) allow(capability string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !f.approved(capability) {
			http.Error(w, "the plugin may not "+capability, http.StatusForbidden)
			return
		}
		h(w, r)
	}
}

func (f *FakeLoop) channel(w http.ResponseWriter, _ *http.Request) {
	pluginsdk.WriteJSON(w, http.StatusOK, f.cfg.Channel)
}

// query reads the non-negative integer key of r's query, zero when absent.
func query(r *http.Request, key string) (int, error) {
	v := r.URL.Query().Get(key)
	if v == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%s must be a non-negative integer", key)
	}
	return n, nil
}

func (f *FakeLoop) file(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	start, err := query(r, "start")
	if err == nil {
		var end int
		end, err = query(r, "end")
		if err == nil {
			f.serveFile(w, p, start, end)
			return
		}
	}
	http.Error(w, err.Error(), http.StatusBadRequest)
}

func (f *FakeLoop) serveFile(w http.ResponseWriter, p string, start, end int) {
	content, ok := f.cfg.Files[p]
	if !ok {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}
	var lines []string
	if content != "" {
		lines = strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	}
	total := len(lines)
	if start == 0 && end == 0 {
		pluginsdk.WriteJSON(w, http.StatusOK, pluginsdk.File{Path: p, Content: content, Start: min(1, total), End: total, TotalLines: total})
		return
	}
	start = max(start, 1)
	if end == 0 || end > total {
		end = total
	}
	if start > total || end < start {
		http.Error(w, fmt.Sprintf("the file has %d lines", total), http.StatusBadRequest)
		return
	}
	pluginsdk.WriteJSON(w, http.StatusOK, pluginsdk.File{Path: p, Content: strings.Join(lines[start-1:end], "\n"), Start: start, End: end, TotalLines: total})
}

func (f *FakeLoop) tree(w http.ResponseWriter, r *http.Request) {
	glob := r.URL.Query().Get("glob")
	if _, err := path.Match(glob, ""); err != nil {
		http.Error(w, "invalid glob", http.StatusBadRequest)
		return
	}
	files := []string{}
	for _, p := range slices.Sorted(maps.Keys(f.cfg.Files)) {
		full, _ := path.Match(glob, p)
		base, _ := path.Match(glob, path.Base(p))
		if glob == "" || full || base {
			files = append(files, p)
		}
	}
	pluginsdk.WriteJSON(w, http.StatusOK, pluginsdk.Tree{Files: files})
}

func (f *FakeLoop) git(w http.ResponseWriter, _ *http.Request) {
	if f.cfg.Git == nil {
		http.Error(w, "not a git repository", http.StatusConflict)
		return
	}
	pluginsdk.WriteJSON(w, http.StatusOK, f.cfg.Git)
}

func (f *FakeLoop) hotspots(w http.ResponseWriter, r *http.Request) {
	if f.cfg.Hotspots == nil {
		http.Error(w, "no quality scan yet", http.StatusServiceUnavailable)
		return
	}
	limit, err := query(r, "limit")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	fns := f.cfg.Hotspots
	if limit > 0 && limit < len(fns) {
		fns = fns[:limit]
	}
	pluginsdk.WriteJSON(w, http.StatusOK, map[string]any{"functions": fns})
}

// agent returns the manifest's agent id, or nil when the fake has no
// manifest and allows any.
func (f *FakeLoop) agent(id string) (*pluginsdk.Agent, bool) {
	if f.cfg.Manifest == nil {
		return nil, true
	}
	i := slices.IndexFunc(f.cfg.Manifest.Agents, func(a pluginsdk.Agent) bool { return a.ID == id })
	if i < 0 {
		return nil, false
	}
	return &f.cfg.Manifest.Agents[i], true
}

func (f *FakeLoop) startRun(w http.ResponseWriter, r *http.Request) {
	var req pluginsdk.RunRequest
	if !pluginsdk.DecodeJSON(w, r, &req) {
		return
	}
	if req.Agent == "" || req.Prompt == "" {
		http.Error(w, "agent and prompt are required", http.StatusBadRequest)
		return
	}
	a, ok := f.agent(req.Agent)
	if !ok {
		http.Error(w, "the plugin has no agent "+req.Agent, http.StatusNotFound)
		return
	}
	if a != nil && !f.approved(pluginsdk.CapAgentWrite) && (!a.ReadOnly || !f.approved(pluginsdk.CapAgentReadOnly)) {
		http.Error(w, "the plugin may not run agent "+req.Agent, http.StatusForbidden)
		return
	}
	f.mu.Lock()
	run := pluginsdk.Run{RunID: fmt.Sprintf("run-%d", len(f.runs)+1), ThreadID: "plugin-thread"}
	f.runs = append(f.runs, StartedRun{RunRequest: req, Run: run})
	f.mu.Unlock()
	pluginsdk.WriteJSON(w, http.StatusAccepted, run)
}

func (f *FakeLoop) event(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Type string          `json:"type"`
		Data json.RawMessage `json:"data"`
	}
	if !pluginsdk.DecodeJSON(w, r, &req) {
		return
	}
	if req.Type == "" {
		http.Error(w, "type is required", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	f.events = append(f.events, Event(req))
	f.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (f *FakeLoop) notify(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	if !pluginsdk.DecodeJSON(w, r, &req) {
		return
	}
	f.mu.Lock()
	f.notifications = append(f.notifications, Notification(req))
	f.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}
