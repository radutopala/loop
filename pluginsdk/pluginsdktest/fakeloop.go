// Package pluginsdktest tests Loop plugins: FakeLoop serves the Plugin API v1
// from memory, Plugin calls a plugin the way Loop does, and Check drives a
// plugin through the Loop → plugin contract.
package pluginsdktest

import (
	"cmp"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"

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
	// GitHead is GET /channel/git/head's answer; nil answers 409, as for a
	// dir that isn't a git repository.
	GitHead *pluginsdk.GitHead
	// Hotspots are the latest quality scan's functions, worst first, which
	// GET /channel/quality/hotspots pages through; nil answers 503, as
	// before the first quality scan.
	Hotspots []pluginsdk.Hotspot
	// Quality is the scan's score, raw value, over-threshold count and
	// histogram, which the hotspots answer carries; the fake fills in the
	// rest.
	Quality pluginsdk.Hotspots
	// Threads are the channel's listed threads: those a UI command may
	// open, and whose chats a switch or live call may pick.
	Threads []string
	// NotLive makes the chats not run live: holding a session and sending
	// it control requests answer 409.
	NotLive bool
	// LiveResponses are the live sessions' responses to control requests,
	// by subtype; a subtype not there answers {}.
	LiveResponses map[string]map[string]any
}

// Event is an event the plugin sent to its panels.
type Event struct {
	Type string
	Data json.RawMessage
}

// Notification is a notification the plugin asked to keep in the drawer.
type Notification struct {
	Title string
	Body  string
}

// Badge is the badge the plugin shows on its channel's sidebar row.
type Badge struct {
	Text  string
	Title string
}

// Card is a card the plugin posted to the channel's chat.
type Card struct {
	ID    int64
	Card  string
	Title string
	Data  json.RawMessage
}

// LiveControl is a control request the plugin sent to a chat's live
// session.
type LiveControl struct {
	// ThreadID is the thread whose chat it was for; "" for the channel's.
	ThreadID string
	Request  map[string]any
}

// StartedRun is an agent run the plugin started. The fake runs no agent:
// a run stays queued until the plugin cancels it.
type StartedRun struct {
	pluginsdk.RunRequest
	pluginsdk.Run
	// Cancelled is set once the plugin cancelled the run.
	Cancelled bool
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
	memory        map[string]string
	uiCommands    []pluginsdk.UICommand
	badge         *Badge
	cards         []Card
	switches      map[switchKey]pluginsdk.SwitchState
	liveControls  []LiveControl
	holds         map[string]bool
}

// switchKey is a composer switch in a chat.
type switchKey struct{ id, threadID string }

// NewFakeLoop starts a FakeLoop serving cfg, stopped when t ends.
func NewFakeLoop(t testing.TB, cfg Config) *FakeLoop {
	t.Helper()
	if cfg.Channel.ID == "" {
		cfg.Channel.ID = "ch1"
	}
	f := &FakeLoop{cfg: cfg, memory: map[string]string{}, switches: map[switchKey]pluginsdk.SwitchState{}, holds: map[string]bool{}, token: "plugin-token", TokenFile: filepath.Join(t.TempDir(), "plugin-token")}
	require.NoError(t, os.WriteFile(f.TokenFile, []byte(f.token+"\n"), 0o600), "writing the plugin token")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/plugin/v1/channel", f.channel)
	mux.HandleFunc("GET /api/plugin/v1/channel/files", f.allow(pluginsdk.CapFilesRead, f.file))
	mux.HandleFunc("GET /api/plugin/v1/channel/tree", f.allow(pluginsdk.CapFilesRead, f.tree))
	mux.HandleFunc("GET /api/plugin/v1/channel/git/recent", f.allow(pluginsdk.CapGitRead, f.git))
	mux.HandleFunc("GET /api/plugin/v1/channel/git/head", f.allow(pluginsdk.CapGitRead, f.gitHead))
	mux.HandleFunc("GET /api/plugin/v1/channel/quality/hotspots", f.allow(pluginsdk.CapQualityRead, f.hotspots))
	mux.HandleFunc("POST /api/plugin/v1/runs", f.startRun)
	mux.HandleFunc("GET /api/plugin/v1/runs/{run_id}", f.allowRuns(f.getRun))
	mux.HandleFunc("DELETE /api/plugin/v1/runs/{run_id}", f.allowRuns(f.cancelRun))
	mux.HandleFunc("POST /api/plugin/v1/events", f.event)
	mux.HandleFunc("POST /api/plugin/v1/notify", f.allow(pluginsdk.CapUINotify, f.notify))
	mux.HandleFunc("PUT /api/plugin/v1/memory/{name}", f.allow(pluginsdk.CapMemoryWrite, f.putMemory))
	mux.HandleFunc("DELETE /api/plugin/v1/memory/{name}", f.allow(pluginsdk.CapMemoryWrite, f.deleteMemory))
	mux.HandleFunc("POST /api/plugin/v1/ui/commands", f.allow(pluginsdk.CapUIRun, f.runUI))
	mux.HandleFunc("POST /api/plugin/v1/ui/open_file", f.allow(pluginsdk.CapUIOpenFile, f.openFile))
	mux.HandleFunc("PUT /api/plugin/v1/badge", f.allow(pluginsdk.CapUIBadge, f.setBadge))
	mux.HandleFunc("DELETE /api/plugin/v1/badge", f.allow(pluginsdk.CapUIBadge, f.clearBadge))
	mux.HandleFunc("POST /api/plugin/v1/cards", f.allow(pluginsdk.CapUICard, f.postCard))
	mux.HandleFunc("PUT /api/plugin/v1/composer/switches/{id}", f.allow(pluginsdk.CapUISwitch, f.setSwitch))
	mux.HandleFunc("DELETE /api/plugin/v1/composer/switches/{id}", f.allow(pluginsdk.CapUISwitch, f.resetSwitch))
	mux.HandleFunc("POST /api/plugin/v1/channel/live/control", f.allow(pluginsdk.CapLiveControl, f.liveControl))
	mux.HandleFunc("PUT /api/plugin/v1/channel/live/hold", f.allow(pluginsdk.CapLiveControl, f.holdLive))
	mux.HandleFunc("DELETE /api/plugin/v1/channel/live/hold", f.allow(pluginsdk.CapLiveControl, f.releaseLive))
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

// UICommands returns the UI commands the plugin ran, oldest first.
func (f *FakeLoop) UICommands() []pluginsdk.UICommand {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.uiCommands)
}

// Badge returns the plugin's badge, or nil when it shows none.
func (f *FakeLoop) Badge() *Badge {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.badge == nil {
		return nil
	}
	b := *f.badge
	return &b
}

// Cards returns the cards the plugin posted, oldest first.
func (f *FakeLoop) Cards() []Card {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.cards)
}

// Switch returns what the composer switch id shows in the chat of
// threadID, "" for the channel's; ok is false while the plugin hasn't set
// it.
func (f *FakeLoop) Switch(id, threadID string) (state pluginsdk.SwitchState, ok bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	state, ok = f.switches[switchKey{id, threadID}]
	return state, ok
}

// LiveControls returns the control requests the plugin sent, oldest first.
func (f *FakeLoop) LiveControls() []LiveControl {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.liveControls)
}

// Holds returns the threads whose live sessions the plugin holds, "" for
// the channel's, sorted.
func (f *FakeLoop) Holds() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Sorted(maps.Keys(f.holds))
}

// Memory returns the plugin's memory files by name.
func (f *FakeLoop) Memory() map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return maps.Clone(f.memory)
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

// allowRuns lets h answer when the plugin may run agents: either agent
// capability lets it read and cancel its runs.
func (f *FakeLoop) allowRuns(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !f.approved(pluginsdk.CapAgentReadOnly) && !f.approved(pluginsdk.CapAgentWrite) {
			http.Error(w, "the plugin may not run agents", http.StatusForbidden)
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

func (f *FakeLoop) gitHead(w http.ResponseWriter, _ *http.Request) {
	if f.cfg.GitHead == nil {
		http.Error(w, "not a git repository", http.StatusConflict)
		return
	}
	pluginsdk.WriteJSON(w, http.StatusOK, f.cfg.GitHead)
}

// Loop's default and largest page of hotspots.
const (
	hotspotsLimit    = 50
	hotspotsMaxLimit = 100
)

// hotspots pages through the scan's functions as Loop does.
func (f *FakeLoop) hotspots(w http.ResponseWriter, r *http.Request) {
	if f.cfg.Hotspots == nil {
		http.Error(w, "no quality scan yet", http.StatusServiceUnavailable)
		return
	}
	limit, err := query(r, "limit")
	if err != nil || limit == 0 && r.URL.Query().Has("limit") {
		http.Error(w, "limit must be a positive integer", http.StatusBadRequest)
		return
	}
	offset, err := query(r, "offset")
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if limit == 0 {
		limit = hotspotsLimit
	}
	limit = min(limit, hotspotsMaxLimit)
	all := f.cfg.Hotspots
	start := min(offset, len(all))
	page := all[start:min(start+limit, len(all))]
	h := f.cfg.Quality
	h.TotalFunctions, h.Functions, h.Offset, h.Limit, h.Returned = len(all), page, offset, limit, len(page)
	if h.Histogram == nil {
		h.Histogram = map[string]map[string]int{}
	}
	pluginsdk.WriteJSON(w, http.StatusOK, h)
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

// errRunGone is Loop's answer for a run that ended or isn't the plugin's.
const errRunGone = "no queued or running run of the plugin with that id"

// queuedRun returns the index of the plugin's run id while it's queued, or
// -1. Call it with f.mu held.
func (f *FakeLoop) queuedRun(id string) int {
	return slices.IndexFunc(f.runs, func(r StartedRun) bool { return r.RunID == id && !r.Cancelled })
}

func (f *FakeLoop) getRun(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	i := f.queuedRun(r.PathValue("run_id"))
	var run StartedRun
	if i >= 0 {
		run = f.runs[i]
	}
	f.mu.Unlock()
	if i < 0 {
		http.Error(w, errRunGone, http.StatusNotFound)
		return
	}
	pluginsdk.WriteJSON(w, http.StatusOK, pluginsdk.RunState{RunID: run.RunID, ThreadID: run.ThreadID, Status: pluginsdk.RunQueued})
}

func (f *FakeLoop) cancelRun(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	i := f.queuedRun(r.PathValue("run_id"))
	if i >= 0 {
		f.runs[i].Cancelled = true
	}
	f.mu.Unlock()
	if i < 0 {
		http.Error(w, errRunGone, http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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

// memoryName is the name a plugin gives one of its memory files.
var memoryName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

func (f *FakeLoop) putMemory(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !memoryName.MatchString(name) {
		http.Error(w, "name must be lowercase letters, digits and dashes, at most 64", http.StatusBadRequest)
		return
	}
	var req struct {
		Content string `json:"content"`
	}
	if !pluginsdk.DecodeJSON(w, r, &req) {
		return
	}
	f.mu.Lock()
	f.memory[name] = req.Content
	f.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (f *FakeLoop) deleteMemory(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	delete(f.memory, r.PathValue("name"))
	f.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

// setBadge checks a badge as Loop does and keeps it.
func (f *FakeLoop) setBadge(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Text  string `json:"text"`
		Title string `json:"title"`
	}
	if !pluginsdk.DecodeJSON(w, r, &req) {
		return
	}
	req.Text = strings.TrimSpace(req.Text)
	if req.Text == "" || utf8.RuneCountInString(req.Text) > 16 || strings.ContainsFunc(req.Text, unicode.IsControl) ||
		utf8.RuneCountInString(req.Title) > 200 {
		http.Error(w, "text must be one line of 1 to 16 characters, and title at most 200", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	f.badge = &Badge{Text: req.Text, Title: req.Title}
	f.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (f *FakeLoop) clearBadge(w http.ResponseWriter, _ *http.Request) {
	f.mu.Lock()
	f.badge = nil
	f.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

// postCard checks a card is the manifest's, when there is one, and keeps
// it.
func (f *FakeLoop) postCard(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Card  string          `json:"card"`
		Title string          `json:"title"`
		Data  json.RawMessage `json:"data"`
	}
	if !pluginsdk.DecodeJSON(w, r, &req) {
		return
	}
	if !f.declaresCard(req.Card) {
		http.Error(w, fmt.Sprintf("the plugin declares no card %q", req.Card), http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	c := Card{ID: int64(len(f.cards) + 1), Card: req.Card, Title: req.Title, Data: req.Data}
	f.cards = append(f.cards, c)
	f.mu.Unlock()
	pluginsdk.WriteJSON(w, http.StatusCreated, map[string]int64{"id": c.ID})
}

// declaresCard reports whether the manifest declares card; without a
// manifest, any named card.
func (f *FakeLoop) declaresCard(card string) bool {
	m := f.cfg.Manifest
	if m == nil {
		return card != ""
	}
	return m.UI != nil && slices.ContainsFunc(m.UI.Cards, func(c pluginsdk.Card) bool { return c.ID == card })
}

// runUI checks a UI command as Loop does and answers that the window ran
// every step.
func (f *FakeLoop) runUI(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ClientID string             `json:"client_id"`
		Steps    []pluginsdk.UIStep `json:"steps"`
		Timeout  string             `json:"timeout"`
	}
	if !pluginsdk.DecodeJSON(w, r, &req) {
		return
	}
	if len(req.Steps) == 0 || slices.ContainsFunc(req.Steps, func(step pluginsdk.UIStep) bool { return step["op"] == "" || step["op"] == nil }) {
		http.Error(w, "steps is required, and every step needs an op", http.StatusBadRequest)
		return
	}
	cmd := pluginsdk.UICommand{ClientID: req.ClientID, Steps: req.Steps}
	if req.Timeout != "" {
		d, err := time.ParseDuration(req.Timeout)
		if err != nil || d <= 0 || d > 10*time.Minute {
			http.Error(w, "timeout must be a Go duration up to 10m", http.StatusBadRequest)
			return
		}
		cmd.Timeout = d
	}
	if msg := f.uiRefusal(req.Steps); msg != "" {
		http.Error(w, msg, http.StatusForbidden)
		return
	}
	results := make([]map[string]any, len(req.Steps))
	for i, step := range req.Steps {
		results[i] = map[string]any{"op": step["op"], "ok": true}
	}
	f.mu.Lock()
	f.uiCommands = append(f.uiCommands, cmd)
	f.mu.Unlock()
	clientID := cmp.Or(req.ClientID, "window")
	pluginsdk.WriteJSON(w, http.StatusOK, pluginsdk.UIReply{ClientID: clientID, Results: results})
}

// openFile checks the path as Loop does, records the two steps Loop sends
// the window, and answers that the window ran them.
func (f *FakeLoop) openFile(w http.ResponseWriter, r *http.Request) {
	var req pluginsdk.OpenFile
	if !pluginsdk.DecodeJSON(w, r, &req) {
		return
	}
	rel := path.Clean(req.Path)
	if req.Path == "" || path.IsAbs(rel) || rel == "." || rel == ".." || strings.HasPrefix(rel, "../") {
		http.Error(w, "path must name a file inside the channel's dir, relative to it", http.StatusBadRequest)
		return
	}
	if req.Line < 0 {
		http.Error(w, "line must be a whole number from 1", http.StatusBadRequest)
		return
	}
	open := pluginsdk.UIStep{"op": "open_file", "path": rel}
	if req.Line > 0 {
		open["line"] = req.Line
	}
	cmd := pluginsdk.UICommand{Steps: []pluginsdk.UIStep{{"op": "select_channel", "channel_id": f.cfg.Channel.ID}, open}}
	f.mu.Lock()
	f.uiCommands = append(f.uiCommands, cmd)
	f.mu.Unlock()
	pluginsdk.WriteJSON(w, http.StatusOK, pluginsdk.UIReply{ClientID: "window", Results: []map[string]any{
		{"op": "select_channel", "ok": true}, {"op": "open_file", "ok": true},
	}})
}

// uiRefusal returns why Loop would refuse steps, or "" when it runs them.
func (f *FakeLoop) uiRefusal(steps []pluginsdk.UIStep) string {
	if steps[0]["op"] != "select_channel" {
		return "a plugin's command starts with select_channel, to its channel or one of its threads"
	}
	for _, step := range steps {
		id, _ := step["channel_id"].(string)
		if step["op"] == "select_channel" && id != f.cfg.Channel.ID && !slices.Contains(f.cfg.Threads, id) {
			return "channel " + id + " isn't the plugin's channel or one of its threads"
		}
	}
	return ""
}

// chat checks threadID picks the channel's chat or one of its listed
// threads', as Loop does, and returns it, "" for the channel's.
func (f *FakeLoop) chat(w http.ResponseWriter, threadID string) (string, bool) {
	if threadID == f.cfg.Channel.ID {
		threadID = ""
	}
	if threadID != "" && !slices.Contains(f.cfg.Threads, threadID) {
		http.Error(w, "thread not found in the plugin's channel", http.StatusNotFound)
		return "", false
	}
	return threadID, true
}

// switchStatuses are the statuses a composer switch may show.
var switchStatuses = []pluginsdk.SwitchStatus{pluginsdk.SwitchPlain, pluginsdk.SwitchBusy, pluginsdk.SwitchOK, pluginsdk.SwitchError}

// setSwitch checks a switch's state as Loop does and keeps it.
func (f *FakeLoop) setSwitch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		pluginsdk.SwitchState
		ThreadID string `json:"thread_id"`
	}
	if !pluginsdk.DecodeJSON(w, r, &req) {
		return
	}
	if !slices.Contains(switchStatuses, req.Status) || utf8.RuneCountInString(req.Title) > 200 {
		http.Error(w, "status must be busy, ok or error, and title at most 200 characters", http.StatusBadRequest)
		return
	}
	id := r.PathValue("id")
	threadID, ok := f.switchChat(w, id, req.ThreadID)
	if !ok {
		return
	}
	f.mu.Lock()
	f.switches[switchKey{id, threadID}] = req.SwitchState
	f.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (f *FakeLoop) resetSwitch(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	threadID, ok := f.switchChat(w, id, r.URL.Query().Get("thread_id"))
	if !ok {
		return
	}
	f.mu.Lock()
	f.switches[switchKey{id, threadID}] = pluginsdk.SwitchState{}
	f.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

// switchChat checks the manifest declares the switch id, when there is
// one, and returns the chat threadID picks.
func (f *FakeLoop) switchChat(w http.ResponseWriter, id, threadID string) (string, bool) {
	if m := f.cfg.Manifest; m != nil &&
		(m.UI == nil || !slices.ContainsFunc(m.UI.ComposerSwitches, func(c pluginsdk.ComposerSwitch) bool { return c.ID == id })) {
		http.Error(w, fmt.Sprintf("the plugin declares no composer switch %q", id), http.StatusNotFound)
		return "", false
	}
	return f.chat(w, threadID)
}

// liveControl checks the manifest declares the request's subtype, when
// there is one, keeps the request and answers the configured response.
func (f *FakeLoop) liveControl(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Request  map[string]any `json:"request"`
		ThreadID string         `json:"thread_id"`
	}
	if !pluginsdk.DecodeJSON(w, r, &req) {
		return
	}
	subtype, _ := req.Request["subtype"].(string)
	if subtype == "" {
		http.Error(w, "request is an object with a subtype", http.StatusBadRequest)
		return
	}
	if m := f.cfg.Manifest; m != nil && (m.Live == nil || !slices.Contains(m.Live.Control, subtype)) {
		http.Error(w, "the plugin's manifest doesn't declare the control request "+subtype, http.StatusBadRequest)
		return
	}
	threadID, ok := f.chat(w, req.ThreadID)
	if !ok {
		return
	}
	if f.cfg.NotLive {
		http.Error(w, "the chat has no live session", http.StatusConflict)
		return
	}
	f.mu.Lock()
	f.liveControls = append(f.liveControls, LiveControl{ThreadID: threadID, Request: req.Request})
	f.mu.Unlock()
	res := f.cfg.LiveResponses[subtype]
	if res == nil {
		res = map[string]any{}
	}
	pluginsdk.WriteJSON(w, http.StatusOK, map[string]any{"response": res})
}

func (f *FakeLoop) holdLive(w http.ResponseWriter, r *http.Request) {
	threadID, ok := f.chat(w, r.URL.Query().Get("thread_id"))
	if !ok {
		return
	}
	if f.cfg.NotLive {
		http.Error(w, "the chat doesn't run live", http.StatusConflict)
		return
	}
	f.mu.Lock()
	f.holds[threadID] = true
	f.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (f *FakeLoop) releaseLive(w http.ResponseWriter, r *http.Request) {
	threadID, ok := f.chat(w, r.URL.Query().Get("thread_id"))
	if !ok {
		return
	}
	f.mu.Lock()
	delete(f.holds, threadID)
	f.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}
