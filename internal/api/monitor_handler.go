// monitor_handler.go starts, lists and cancels a channel's monitors: watch
// scripts that run in the background and wake the channel's agent when they
// exit.
package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/radutopala/loop/internal/monitor"
)

// MonitorManager runs channels' watch scripts. monitor.Manager implements it.
type MonitorManager interface {
	Start(channelID, dirPath, parentDirPath, command, description string, timeout time.Duration, interrupt bool) (*monitor.Monitor, error)
	List(channelID string) []monitor.Monitor
	Cancel(channelID, id string) bool
}

// SetMonitors configures the monitors behind /api/channels/{id}/monitors.
func (s *Server) SetMonitors(m MonitorManager) {
	s.monitors = m
}

// registerMonitorRoutes registers the channel monitor routes.
func (s *Server) registerMonitorRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/channels/{id}/monitors", s.handleStartMonitor)
	mux.HandleFunc("GET /api/channels/{id}/monitors", s.handleListMonitors)
	mux.HandleFunc("DELETE /api/channels/{id}/monitors/{mid}", s.handleCancelMonitor)
}

// startMonitorRequest is the body of POST /api/channels/{id}/monitors. A zero
// TimeoutSec means monitor.DefaultTimeout. Interrupt makes the wake-up
// cancel the agent's active run instead of queueing behind it.
type startMonitorRequest struct {
	Command     string `json:"command"`
	Description string `json:"description"`
	TimeoutSec  int    `json:"timeout_sec"`
	Interrupt   bool   `json:"interrupt"`
}

// handleStartMonitor runs the script in the channel's agent container in the
// background and returns the monitor at once.
func (s *Server) handleStartMonitor(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.monitors, "monitors not configured") {
		return
	}
	var req startMonitorRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	req.Command = strings.TrimSpace(req.Command)
	req.Description = strings.TrimSpace(req.Description)
	if req.Command == "" || req.Description == "" {
		http.Error(w, "command and description are required", http.StatusBadRequest)
		return
	}
	if req.TimeoutSec < 0 {
		http.Error(w, "timeout_sec must not be negative", http.StatusBadRequest)
		return
	}
	channelID := r.PathValue("id")
	dirPath, parentDirPath, err := s.resolveWorkflowConfigPaths(r.Context(), channelID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	mon, err := s.monitors.Start(channelID, dirPath, parentDirPath, req.Command, req.Description, time.Duration(req.TimeoutSec)*time.Second, req.Interrupt)
	switch {
	case errors.Is(err, monitor.ErrLimit):
		http.Error(w, err.Error(), http.StatusConflict)
	case err != nil:
		http.Error(w, err.Error(), http.StatusServiceUnavailable)
	default:
		writeHTTPJSON(w, http.StatusCreated, mon, s.logger)
	}
}

// listMonitorsResponse is the body of GET /api/channels/{id}/monitors.
type listMonitorsResponse struct {
	Monitors []monitor.Monitor `json:"monitors"`
}

// handleListMonitors returns the channel's running monitors, oldest first.
func (s *Server) handleListMonitors(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.monitors, "monitors not configured") {
		return
	}
	writeHTTPJSON(w, http.StatusOK, listMonitorsResponse{Monitors: s.monitors.List(r.PathValue("id"))}, s.logger)
}

// handleCancelMonitor stops a monitor without waking the agent.
func (s *Server) handleCancelMonitor(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.monitors, "monitors not configured") {
		return
	}
	if !s.monitors.Cancel(r.PathValue("id"), r.PathValue("mid")) {
		http.Error(w, "monitor not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
