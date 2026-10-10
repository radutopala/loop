package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/radutopala/loop/internal/container"
	"github.com/radutopala/loop/internal/events"
)

// ImageManager defines the interface for image lifecycle operations.
type ImageManager interface {
	Status() container.ImageBuildStatus
	Versions() container.ImageVersions
	UpdateAvailable() *events.ImageUpdateAvailableData
	RemoveImage(ctx context.Context) error
	Rebuild(ctx context.Context) error
	Reclaimable(ctx context.Context, volumeSizes bool) (container.Reclaimable, error)
	ReclaimSpace(ctx context.Context, opts container.ReclaimOptions) (container.ReclaimResult, error)
}

type imageStatusResponse struct {
	Status          container.ImageBuildStatus       `json:"status"`
	Versions        container.ImageVersions          `json:"versions"`
	UpdateAvailable *events.ImageUpdateAvailableData `json:"update_available,omitempty"`
}

func (s *Server) handleImageStatus(w http.ResponseWriter, _ *http.Request) {
	if !requireConfigured(w, s.imageManager, "image management not configured") {
		return
	}

	resp := imageStatusResponse{
		Status:          s.imageManager.Status(),
		Versions:        s.imageManager.Versions(),
		UpdateAvailable: s.imageManager.UpdateAvailable(),
	}
	writeHTTPJSON(w, http.StatusOK, resp, s.logger)
}

func (s *Server) handleImageRebuild(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.imageManager, "image management not configured") {
		return
	}

	if err := s.imageManager.Rebuild(r.Context()); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}

	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) handleImageRemove(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.imageManager, "image management not configured") {
		return
	}

	if err := s.imageManager.RemoveImage(r.Context()); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleImageReclaimable estimates what reclaiming Docker space would free.
// Volumes are sized only with ?volume_sizes=true: that takes minutes on a
// large daemon.
func (s *Server) handleImageReclaimable(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.imageManager, "image management not configured") {
		return
	}

	estimate, err := s.imageManager.Reclaimable(r.Context(), r.URL.Query().Get("volume_sizes") == "true")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeHTTPJSON(w, http.StatusOK, estimate, s.logger)
}

// handleImageReclaim reclaims Docker space. The body, optional, picks the
// opt-in parts.
func (s *Server) handleImageReclaim(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.imageManager, "image management not configured") {
		return
	}

	var opts container.ReclaimOptions
	if err := json.NewDecoder(r.Body).Decode(&opts); err != nil && !errors.Is(err, io.EOF) {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	result, err := s.imageManager.ReclaimSpace(r.Context(), opts)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeHTTPJSON(w, http.StatusOK, result, s.logger)
}
