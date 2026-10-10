package api

import (
	"context"
	"errors"
	"net/http"
	"time"

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

func (s *Server) handleListPlugins(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.plugins, pluginsDisabled) {
		return
	}
	states, err := s.plugins.List(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := make([]pluginInfo, 0, len(states))
	for _, st := range states {
		out = append(out, pluginInfo{
			ID: st.PluginID, Image: st.Image, Digest: st.Digest, Version: st.Version, API: st.API,
			Capabilities: st.ApprovedCaps, Dev: st.Dev, Status: st.Status,
			InstalledAt: st.InstalledAt, UpdatedAt: st.UpdatedAt,
		})
	}
	writeHTTPJSON(w, http.StatusOK, map[string]any{"plugins": out}, s.logger)
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
	writeHTTPJSON(w, http.StatusOK, newPluginCandidate(c), s.logger)
}

func (s *Server) handleRemovePlugin(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.plugins, pluginsDisabled) {
		return
	}
	err := s.plugins.Remove(r.Context(), r.PathValue("id"), r.URL.Query().Get("purge") == "true")
	switch {
	case errors.Is(err, install.ErrNotInstalled):
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	case err != nil:
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type pluginActionRequest struct {
	ChannelID string `json:"channel_id"`
}

// handleRunPluginAction runs a plugin action by hand in a channel, starting
// the plugin's container there first if it isn't running. A thread runs it
// in its parent channel, since a plugin runs per channel.
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
	ch, err := s.store.GetChannel(r.Context(), req.ChannelID)
	if err == nil && ch != nil && ch.ParentID != "" {
		ch, err = s.store.GetChannel(r.Context(), ch.ParentID)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if ch == nil {
		http.Error(w, "channel not found", http.StatusNotFound)
		return
	}
	// Starting the container outlives a client that hangs up, so a slow
	// start isn't counted as a failed one.
	ctx := context.WithoutCancel(r.Context())
	err = s.plugins.RunAction(ctx, r.PathValue("plugin"), r.PathValue("action"),
		runtime.Channel{ID: ch.ChannelID, DirPath: ch.DirPath}, proxy.ActionRequest{Trigger: proxy.TriggerManual})
	switch {
	case errors.Is(err, install.ErrNotInstalled), errors.Is(err, install.ErrUnknownAction):
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	case errors.Is(err, install.ErrNotRunnable):
		http.Error(w, err.Error(), http.StatusConflict)
		return
	case err != nil:
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeHTTPJSON(w, http.StatusAccepted, map[string]string{"channel_id": ch.ChannelID}, s.logger)
}

func requireImage(w http.ResponseWriter, image string) bool {
	if image == "" {
		http.Error(w, "image is required", http.StatusBadRequest)
		return false
	}
	return true
}
