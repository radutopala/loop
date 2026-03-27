package api

import (
	"context"
	"net/http"

	"github.com/radutopala/loop/internal/container"
)

// ImageManager defines the interface for image lifecycle operations.
type ImageManager interface {
	Status() container.ImageBuildStatus
	Versions() container.ImageVersions
	RemoveImage(ctx context.Context) error
	Rebuild(ctx context.Context) error
}

type imageStatusResponse struct {
	Status   container.ImageBuildStatus `json:"status"`
	Versions container.ImageVersions    `json:"versions"`
}

func (s *Server) handleImageStatus(w http.ResponseWriter, _ *http.Request) {
	if !requireConfigured(w, s.imageManager, "image management not configured") {
		return
	}

	resp := imageStatusResponse{
		Status:   s.imageManager.Status(),
		Versions: s.imageManager.Versions(),
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
