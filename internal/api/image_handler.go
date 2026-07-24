package api

import (
	"context"
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
	ReclaimSpace(ctx context.Context) (container.ReclaimResult, error)
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

func (s *Server) handleImageReclaim(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.imageManager, "image management not configured") {
		return
	}

	result, err := s.imageManager.ReclaimSpace(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	writeHTTPJSON(w, http.StatusOK, result, s.logger)
}
