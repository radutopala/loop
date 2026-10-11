package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/plugin"
)

// pluginProject is what a channel's project config says about plugins.
type pluginProject struct {
	// merged is the config the channel runs with.
	merged *config.Config
	// global is the global config's plugins settings.
	global config.PluginsConfig
	// own is the project config file's plugins settings as written, which
	// apply only once the owner trusts them.
	own config.PluginsConfig
	// trusted is whether the project config applies as written.
	trusted bool
}

// pluginProject reads the project config of ch, a channel (not a thread).
func (s *Server) pluginProject(ctx context.Context, ch *db.Channel) (pluginProject, error) {
	dir, err := s.resolveProjectConfigDirPath(ctx, ch.ChannelID)
	if err != nil {
		return pluginProject{}, err
	}
	global, merged := s.configs.layered(dir, "")
	if merged == nil {
		return pluginProject{}, errors.New("loading config failed")
	}
	p := pluginProject{merged: merged, global: global.Plugins, trusted: true}
	data, err := s.sys.ReadFile(filepath.Join(dir, ".loop", "config.json"))
	switch {
	case os.IsNotExist(err):
	case err != nil:
		return pluginProject{}, fmt.Errorf("reading the project config: %w", err)
	default:
		if p.own, err = config.ProjectPlugins(data); err != nil {
			return pluginProject{}, err
		}
	}
	if s.projectTrust != nil {
		st, err := s.projectTrust.Status(dir)
		if err != nil {
			return pluginProject{}, err
		}
		p.trusted = st.Trusted
	}
	return p, nil
}

// settings resolves the settings of the plugin id with manifest m: the
// project's values over the global ones, each refused value left out so
// the one below it applies.
func (p pluginProject) settings(m *plugin.Manifest, id string) (map[string]any, []string) {
	return m.ResolveSettings(p.global.ByID[id].Settings, p.own.ByID[id].Settings)
}

// PluginsChanged tells the app the plugins, their enablement or their
// containers changed.
func (s *Server) PluginsChanged() {
	if s.eventsHub != nil {
		s.eventsHub.Broadcast(Event{Type: EventPluginsChanged, Global: true})
	}
}

// pluginConfigChanged follows a config write: the panes learn of the
// plugins' enablement anew, the containers of the projects that stopped
// enabling their plugin are stopped, and the plugins' scheduled tasks
// follow.
func (s *Server) pluginConfigChanged(ctx context.Context) {
	if s.plugins == nil {
		return
	}
	s.plugins.StopDisabled(ctx)
	s.syncPluginSchedules(ctx)
	s.PluginsChanged()
}

// syncPluginSchedules creates, pauses and resumes the plugins' scheduled
// tasks as the projects enabling them say.
func (s *Server) syncPluginSchedules(ctx context.Context) {
	if err := s.plugins.SyncSchedules(ctx); err != nil {
		s.logger.Warn("syncing plugin schedules", "error", err)
	}
}

// pluginInstalled reports whether plugin id is installed; otherwise it
// answers the request and returns false.
func (s *Server) pluginInstalled(w http.ResponseWriter, r *http.Request, id string) bool {
	states, err := s.plugins.List(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return false
	}
	for _, st := range states {
		if st.PluginID == id {
			return true
		}
	}
	http.Error(w, fmt.Sprintf("plugin %s is not installed", id), http.StatusNotFound)
	return false
}

// handlePluginAudit returns the gate's records for a plugin's container in
// a channel, newest first: up to ?limit= of them, 200 by default.
func (s *Server) handlePluginAudit(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.plugins, pluginsDisabled) {
		return
	}
	id := r.PathValue("plugin")
	if !s.pluginInstalled(w, r, id) {
		return
	}
	_, limit := parsePaging(r, 0, 200)
	entries, err := s.plugins.Audit(id, r.PathValue("channel"), limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeHTTPJSON(w, http.StatusOK, map[string]any{"entries": entries}, s.logger)
}

// handleStopPluginContainer stops a plugin's container in a channel,
// keeping its data; the next call starts it again.
func (s *Server) handleStopPluginContainer(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.plugins, pluginsDisabled) {
		return
	}
	id := r.PathValue("plugin")
	if !s.pluginInstalled(w, r, id) {
		return
	}
	s.plugins.Stop(r.Context(), id, r.PathValue("channel"))
	s.PluginsChanged()
	w.WriteHeader(http.StatusNoContent)
}
