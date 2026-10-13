package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/radutopala/loop/internal/agentgate"
	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/plugin"
	"github.com/radutopala/loop/internal/plugin/install"
	"github.com/radutopala/loop/internal/plugin/proxy"
	"github.com/radutopala/loop/internal/plugin/runtime"
)

// PluginManager installs, upgrades and removes plugins.
type PluginManager interface {
	List(ctx context.Context) ([]*db.PluginState, error)
	Inspect(ctx context.Context, ref string, dev bool) (*install.Candidate, error)
	Install(ctx context.Context, ref string, dev bool, digest string, approved []plugin.Capability) (*install.Candidate, error)
	Remove(ctx context.Context, pluginID string, purge bool) error
	RunAction(ctx context.Context, pluginID, action string, ch runtime.Channel, req proxy.ActionRequest) error
	AgentRun(ctx context.Context, pluginID, agentID string) (*install.AgentRun, error)
	CallTool(ctx context.Context, pluginID, tool string, ch runtime.Channel, req proxy.ToolRequest) (*proxy.ToolResult, error)
	Allows(ctx context.Context, pluginID string, c plugin.Capability) error
	RunDone(ctx context.Context, pluginID string, ch runtime.Channel, runID string, req proxy.RunDoneRequest) error
	X(ctx context.Context, pluginID string, ch runtime.Channel, req proxy.XRequest) (*proxy.XResponse, error)
	Deliver(ctx context.Context, ch runtime.Channel, thread *runtime.Channel, evt proxy.Event) error
	UI(ctx context.Context, pluginID string) (string, *plugin.Manifest, error)
	Containers(ctx context.Context, pluginID string) ([]*db.PluginContainer, error)
	Stop(ctx context.Context, pluginID, channelID string)
	RemoveChannel(ctx context.Context, channelID string) (bool, error)
	Audit(pluginID, channelID string, limit int) ([]agentgate.AuditEntry, error)
	StopDisabled(ctx context.Context) bool
	SyncSchedules(ctx context.Context) error
	Shows(ctx context.Context, pluginID string, c plugin.Capability, ch runtime.Channel) (bool, error)
}

const pluginsDisabled = "plugins are disabled; set plugins.enabled in the global config"

// pluginInfo is an installed plugin as the API shows it.
type pluginInfo struct {
	ID           string    `json:"id"`
	Image        string    `json:"image"`
	Digest       string    `json:"digest,omitempty"`
	Version      string    `json:"version"`
	API          string    `json:"api"`
	Capabilities []string  `json:"capabilities"`
	Dev          bool      `json:"dev"`
	Status       string    `json:"status"`
	InstalledAt  time.Time `json:"installed_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	// Name, Panels, Cards and Actions come from the manifest of a plugin
	// that may run; Panels only when the owner approved ui.panel, Cards
	// ui.notification.card.
	Name    string         `json:"name,omitempty"`
	Panels  []plugin.Panel `json:"panels,omitempty"`
	Cards   []plugin.Card  `json:"cards,omitempty"`
	Actions []string       `json:"actions,omitempty"`
	// EnabledByDefault is the global plugins.<id>.enabled, which every
	// project without its own setting follows.
	EnabledByDefault bool `json:"enabled_by_default"`
	// Enabled is whether the plugin runs in the channel_id's project, and
	// ProjectEnabled the project config's own setting, unset when it has
	// none. They differ while the owner hasn't trusted the project config.
	Enabled        *bool `json:"enabled,omitempty"`
	ProjectEnabled *bool `json:"project_enabled,omitempty"`
	// SettingsSchema is the settings the manifest declares, and
	// GlobalSettings the global config's values for them.
	SettingsSchema map[string]*config.SchemaProperty `json:"settings_schema,omitempty"`
	GlobalSettings map[string]any                    `json:"global_settings,omitempty"`
	// ProjectSettings are the channel_id's project config's own values, and
	// Settings what the plugin runs with there: each default, overridden by
	// the global then the project values. SettingsProblems are the values
	// left out, for no such setting or a value its schema refuses.
	ProjectSettings  map[string]any `json:"project_settings,omitempty"`
	Settings         map[string]any `json:"settings,omitempty"`
	SettingsProblems []string       `json:"settings_problems,omitempty"`
	// DefaultSchedules are the default_schedule of each action that has
	// one, when the owner approved schedule. GlobalSchedules are the
	// global config's plugins.<id>.schedules, ProjectSchedules the
	// channel_id's project config's own, and Schedules the ones that apply
	// there: the global ones, each replaced by the trusted project one.
	DefaultSchedules map[string]plugin.Schedule       `json:"default_schedules,omitempty"`
	GlobalSchedules  map[string]config.PluginSchedule `json:"global_schedules,omitempty"`
	ProjectSchedules map[string]config.PluginSchedule `json:"project_schedules,omitempty"`
	Schedules        map[string]config.PluginSchedule `json:"schedules,omitempty"`
	// Containers are the plugin's containers, one per channel it ran in.
	Containers []pluginContainerInfo `json:"containers"`
}

// pluginContainerInfo is a plugin's container in a channel.
type pluginContainerInfo struct {
	ChannelID   string    `json:"channel_id"`
	ChannelName string    `json:"channel_name,omitempty"`
	Status      string    `json:"status"`
	LastError   string    `json:"last_error,omitempty"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// pluginCandidate is a pulled plugin image for the owner to review: what it
// asks for, and which of that is new since the version installed.
type pluginCandidate struct {
	ID              string              `json:"id"`
	Name            string              `json:"name"`
	Version         string              `json:"version"`
	API             string              `json:"api"`
	Image           string              `json:"image"`
	Digest          string              `json:"digest,omitempty"`
	Dev             bool                `json:"dev"`
	Capabilities    []plugin.Capability `json:"capabilities"`
	NewCapabilities []plugin.Capability `json:"new_capabilities"`
	PreviousVersion string              `json:"previous_version,omitempty"`
}

func newPluginCandidate(c *install.Candidate) pluginCandidate {
	out := pluginCandidate{
		ID: c.Manifest.ID, Name: c.Manifest.Name, Version: c.Manifest.Version, API: c.Manifest.API,
		Image: c.Image, Digest: c.Digest, Dev: c.Dev,
		Capabilities: c.Manifest.Capabilities, NewCapabilities: c.NewCapabilities(),
	}
	if c.Previous != nil {
		out.PreviousVersion = c.Previous.Version
	}
	return out
}

type pluginInspectRequest struct {
	Image string `json:"image"`
	Dev   bool   `json:"dev"`
}

type pluginInstallRequest struct {
	Image string `json:"image"`
	Dev   bool   `json:"dev"`
	// Digest, from the inspect the owner reviewed, refuses the install if the
	// image changed since.
	Digest  string              `json:"digest"`
	Approve []plugin.Capability `json:"approve"`
}

// handleListPlugins lists the installed plugins. With channel_id, each
// says whether it's enabled in that channel's project.
func (s *Server) handleListPlugins(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.plugins, pluginsDisabled) {
		return
	}
	ctx := r.Context()
	var proj *pluginProject
	if id := r.URL.Query().Get("channel_id"); id != "" {
		ch, ok := s.pluginChannel(w, r, id)
		if !ok {
			return
		}
		p, err := s.pluginProject(ctx, ch)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		proj = &p
	}
	states, err := s.plugins.List(ctx)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	global := s.configs.merged("", "")
	out := make([]pluginInfo, 0, len(states))
	for _, st := range states {
		info := pluginInfo{
			ID: st.PluginID, Image: st.Image, Digest: st.Digest, Version: st.Version, API: st.API,
			Capabilities: st.ApprovedCaps, Dev: st.Dev, Status: st.Status,
			InstalledAt: st.InstalledAt, UpdatedAt: st.UpdatedAt,
			Containers: []pluginContainerInfo{},
		}
		_, m, err := s.plugins.UI(ctx, st.PluginID)
		if err == nil {
			info.Name = m.Name
			info.SettingsSchema = m.Settings
			if m.UI != nil && slices.Contains(st.ApprovedCaps, string(plugin.CapUIPanel)) {
				info.Panels = m.UI.Panels
			}
			if m.UI != nil && slices.Contains(st.ApprovedCaps, string(plugin.CapUICard)) {
				info.Cards = m.UI.Cards
			}
			for _, a := range m.Actions {
				info.Actions = append(info.Actions, a.ID)
				if a.DefaultSchedule != nil && slices.Contains(st.ApprovedCaps, string(plugin.CapSchedule)) {
					if info.DefaultSchedules == nil {
						info.DefaultSchedules = map[string]plugin.Schedule{}
					}
					info.DefaultSchedules[a.ID] = *a.DefaultSchedule
				}
			}
		}
		if global != nil {
			info.EnabledByDefault = global.Plugins.EnabledFor(st.PluginID)
			info.GlobalSettings = global.Plugins.ByID[st.PluginID].Settings
			info.GlobalSchedules = global.Plugins.ByID[st.PluginID].Schedules
		}
		if proj != nil {
			on := proj.merged.Plugins.EnabledFor(st.PluginID)
			info.Enabled, info.ProjectEnabled = &on, proj.own.ByID[st.PluginID].Enabled
			info.ProjectSettings = proj.own.ByID[st.PluginID].Settings
			info.ProjectSchedules = proj.own.ByID[st.PluginID].Schedules
			info.Schedules = proj.merged.Plugins.ByID[st.PluginID].Schedules
			if m != nil {
				info.Settings, info.SettingsProblems = proj.settings(m, st.PluginID)
			}
		}
		containers, err := s.plugins.Containers(ctx, st.PluginID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		for _, c := range containers {
			ci := pluginContainerInfo{ChannelID: c.ChannelID, Status: c.Status, LastError: c.LastError, UpdatedAt: c.UpdatedAt}
			if ch, err := s.store.GetChannel(ctx, c.ChannelID); err == nil && ch != nil {
				ci.ChannelName = ch.Name
			}
			info.Containers = append(info.Containers, ci)
		}
		out = append(out, info)
	}
	resp := map[string]any{"plugins": out}
	if proj != nil {
		resp["project_trusted"] = proj.trusted
	}
	writeHTTPJSON(w, http.StatusOK, resp, s.logger)
}

func (s *Server) handleInspectPlugin(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.plugins, pluginsDisabled) {
		return
	}
	var req pluginInspectRequest
	if !decodeJSON(w, r, &req) || !requireImage(w, req.Image) {
		return
	}
	c, err := s.plugins.Inspect(r.Context(), req.Image, req.Dev)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	writeHTTPJSON(w, http.StatusOK, newPluginCandidate(c), s.logger)
}

func (s *Server) handleInstallPlugin(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.plugins, pluginsDisabled) {
		return
	}
	var req pluginInstallRequest
	if !decodeJSON(w, r, &req) || !requireImage(w, req.Image) {
		return
	}
	c, err := s.plugins.Install(r.Context(), req.Image, req.Dev, req.Digest, req.Approve)
	switch {
	case errors.Is(err, install.ErrDigestChanged):
		http.Error(w, err.Error(), http.StatusConflict)
		return
	case errors.Is(err, install.ErrNotApproved):
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	case err != nil:
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	s.syncPluginSchedules(r.Context())
	s.prunePluginBadges(r.Context())
	s.PluginsChanged()
	writeHTTPJSON(w, http.StatusOK, newPluginCandidate(c), s.logger)
}

func (s *Server) handleRemovePlugin(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.plugins, pluginsDisabled) {
		return
	}
	id := r.PathValue("id")
	purge := r.URL.Query().Get("purge") == "true"
	badges := s.pluginBadgesOf(r.Context(), id)
	var threads []*db.Channel
	if purge {
		threads = s.pluginThreads(r.Context(), id)
	}
	err := s.plugins.Remove(r.Context(), id, purge)
	switch {
	case errors.Is(err, install.ErrNotInstalled):
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	case err != nil:
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	s.deletePluginThreads(r.Context(), threads)
	// Removing the plugin dropped its badges and its notifications.
	for _, b := range badges {
		s.broadcastPluginBadge(b.ChannelID, pluginBadge{PluginID: id})
	}
	if s.notifications != nil {
		s.notificationsChanged()
	}
	s.PluginsChanged()
	w.WriteHeader(http.StatusNoContent)
}

// pluginThreads returns the hidden threads the plugin's agent runs went
// in, which a purge deletes. A lookup error only leaves them behind, so it's
// logged.
func (s *Server) pluginThreads(ctx context.Context, pluginID string) []*db.Channel {
	threads, err := s.store.ListPluginThreads(ctx, pluginID)
	if err != nil {
		s.logger.Warn("plugin cleanup: listing the plugin's threads", "plugin", pluginID, "error", err)
	}
	return threads
}

// deletePluginThreads deletes a purged plugin's hidden threads, with their
// messages, sessions, containers and MCP configs, as deleting their channel
// would. The plugin is gone either way, so a failure is logged.
func (s *Server) deletePluginThreads(ctx context.Context, threads []*db.Channel) {
	for _, t := range threads {
		if err := s.store.DeleteChannel(ctx, t.ChannelID); err != nil {
			s.logger.Warn("plugin cleanup: deleting the plugin's thread", "thread_id", t.ChannelID, "error", err)
			continue
		}
		s.stopHiddenThreads(ctx, []*db.Channel{t})
		s.removeMCPConfigs(t, []*db.Channel{t})
	}
}

type pluginActionRequest struct {
	ChannelID string `json:"channel_id"`
}

// handleRunPluginAction runs a plugin action by hand in a channel, thread
// or worktree thread, starting the plugin's container there first if it
// isn't running.
func (s *Server) handleRunPluginAction(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.plugins, pluginsDisabled) {
		return
	}
	var req pluginActionRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.ChannelID == "" {
		http.Error(w, "channel_id is required", http.StatusBadRequest)
		return
	}
	ch, ok := s.pluginChannel(w, r, req.ChannelID)
	if !ok {
		return
	}
	// Starting the container outlives a client that hangs up, so a slow
	// start isn't counted as a failed one.
	ctx := context.WithoutCancel(r.Context())
	err := s.plugins.RunAction(ctx, r.PathValue("plugin"), r.PathValue("action"),
		pluginRuntimeChannel(ch), proxy.ActionRequest{Trigger: proxy.TriggerManual})
	switch {
	case errors.Is(err, install.ErrNotInstalled), errors.Is(err, install.ErrUnknownAction):
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	case errors.Is(err, install.ErrNotRunnable), errors.Is(err, install.ErrNotEnabled):
		http.Error(w, err.Error(), http.StatusConflict)
		return
	case errors.Is(err, proxy.ErrUnavailable):
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	case err != nil:
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeHTTPJSON(w, http.StatusAccepted, map[string]string{"channel_id": ch.ChannelID}, s.logger)
}

// pluginChannel returns channelID's channel, which a plugin serves with a
// container of its own, whether it's a channel, a thread or a worktree
// thread. Otherwise it answers the request and returns false.
func (s *Server) pluginChannel(w http.ResponseWriter, r *http.Request, channelID string) (*db.Channel, bool) {
	ch, err := s.store.GetChannel(r.Context(), channelID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return nil, false
	}
	if ch == nil {
		http.Error(w, "channel not found", http.StatusNotFound)
		return nil, false
	}
	return ch, true
}

// pluginRuntimeChannel is ch as the plugin runtime takes it.
func pluginRuntimeChannel(ch *db.Channel) runtime.Channel {
	return runtime.Channel{ID: ch.ChannelID, DirPath: ch.DirPath}
}

// handlePluginX forwards a call of a plugin's panel to the plugin's own
// API, /x/{path}, on its container in the channel_id query parameter's
// channel. The rest of the query, the body and its type go along.
func (s *Server) handlePluginX(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.plugins, pluginsDisabled) {
		return
	}
	q := r.URL.Query()
	channelID := q.Get("channel_id")
	if channelID == "" {
		http.Error(w, "channel_id is required", http.StatusBadRequest)
		return
	}
	q.Del("channel_id")
	ch, ok := s.pluginChannel(w, r, channelID)
	if !ok {
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, proxy.MaxXBody))
	if err != nil {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
	}
	// Starting the container outlives a client that hangs up, so a slow
	// start isn't counted as a failed one.
	res, err := s.plugins.X(context.WithoutCancel(r.Context()), r.PathValue("plugin"),
		pluginRuntimeChannel(ch), proxy.XRequest{
			Method: r.Method, Path: r.PathValue("path"), RawQuery: q.Encode(),
			ContentType: r.Header.Get("Content-Type"), Body: body,
		})
	switch {
	case errors.Is(err, install.ErrNotInstalled):
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	case errors.Is(err, install.ErrNotRunnable), errors.Is(err, install.ErrNotEnabled):
		http.Error(w, err.Error(), http.StatusConflict)
		return
	case errors.Is(err, proxy.ErrUnavailable):
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
		return
	case err != nil:
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	ctype := res.ContentType
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(res.Status)
	_, _ = w.Write(res.Body)
}

// pluginPanelCSP is the policy of a plugin's panel pages, served under
// prefix: an opaque origin, nothing loaded from outside the plugin's
// bundle, no inline scripts, frames, form posts or navigation.
func pluginPanelCSP(prefix string) string {
	return "sandbox allow-scripts allow-forms; default-src 'none'; " +
		"script-src " + prefix + " 'wasm-unsafe-eval'; style-src " + prefix + " 'unsafe-inline'; " +
		"img-src " + prefix + " data: blob:; font-src " + prefix + " data:; media-src " + prefix + " blob:; " +
		"worker-src " + prefix + " blob:; connect-src " + prefix + "; " +
		"form-action 'none'; frame-src 'none'; base-uri 'none'"
}

// servePluginUI serves a file of the UI bundle of plugin pluginID, rel
// under its /loop-plugin dir, while the plugin may run and draw panels or
// cards.
func (s *Server) servePluginUI(w http.ResponseWriter, r *http.Request, tok, pluginID, rel string) {
	if !requireConfigured(w, s.plugins, pluginsDisabled) {
		return
	}
	err := s.plugins.Allows(r.Context(), pluginID, plugin.CapUIPanel)
	if errors.Is(err, install.ErrNotApproved) {
		err = s.plugins.Allows(r.Context(), pluginID, plugin.CapUICard)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	dir, _, err := s.plugins.UI(r.Context(), pluginID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	ui, ok := strings.CutPrefix(rel, "ui/")
	if !ok {
		http.NotFound(w, r)
		return
	}
	abs, err := s.validateFilePath(filepath.Join(dir, "ui"), ui)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	f, err := s.sys.Open(abs)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	w.Header().Set("Content-Security-Policy", pluginPanelCSP(scheme+"://"+r.Host+"/c/"+tok+"/"))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, info.Name(), info.ModTime(), f)
}

func requireImage(w http.ResponseWriter, image string) bool {
	if image == "" {
		http.Error(w, "image is required", http.StatusBadRequest)
		return false
	}
	return true
}
