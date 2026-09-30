package api

import (
	"errors"
	"net/http"
	"os"

	"github.com/radutopala/loop/internal/config"
)

// ProjectTrust records which project configs the owner trusts: the fields
// of a project's .loop/config.json that reach past the container take
// effect only once the owner approved them. Satisfied by
// *config.TrustStore.
type ProjectTrust interface {
	Status(dir string) (config.TrustStatus, error)
	Trust(dir, hash string) error
	Keep(dir string, before, after []byte) error
}

// WithProjectTrust makes owner writes to project configs keep them trusted
// and serves the trust status and approval routes.
func WithProjectTrust(t ProjectTrust) Option {
	return func(s *Server) { s.projectTrust = t }
}

type projectTrustRequest struct {
	// Hash is the Hash of the status the owner reviewed.
	Hash string `json:"hash"`
}

// handleGetProjectTrust reports whether the channel's project config is
// trusted, and what changed since the owner last trusted it.
func (s *Server) handleGetProjectTrust(w http.ResponseWriter, r *http.Request) {
	if s.projectTrust == nil {
		http.Error(w, "project trust not configured", http.StatusNotImplemented)
		return
	}
	dir, err := s.resolveProjectConfigDirPath(r.Context(), r.URL.Query().Get("channel_id"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	st, err := s.projectTrust.Status(dir)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeHTTPJSON(w, http.StatusOK, st, s.logger)
}

// handleTrustProjectConfig trusts the channel's project config as the owner
// reviewed it. A config that changed since answers 409.
func (s *Server) handleTrustProjectConfig(w http.ResponseWriter, r *http.Request) {
	if s.projectTrust == nil {
		http.Error(w, "project trust not configured", http.StatusNotImplemented)
		return
	}
	dir, err := s.resolveProjectConfigDirPath(r.Context(), r.URL.Query().Get("channel_id"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var req projectTrustRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if req.Hash == "" {
		http.Error(w, "hash is required", http.StatusBadRequest)
		return
	}
	if err := s.projectTrust.Trust(dir, req.Hash); err != nil {
		if errors.Is(err, config.ErrTrustChanged) {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// projectTrustPending reports whether the project config in dir has fields
// waiting for the owner's trust. A config the store can't read reports
// nothing: Settings → Project shows the error.
func (s *Server) projectTrustPending(dir string) bool {
	if s.projectTrust == nil {
		return false
	}
	st, err := s.projectTrust.Status(dir)
	if err != nil {
		s.logger.Debug("checking project config trust", "dir", dir, "error", err)
		return false
	}
	return !st.Trusted
}

// readProjectConfigBefore reads the project config at path ahead of an
// owner's write to it, for keepProjectTrust. ok is false when there's no
// trust store, or the file can't be read: then the write can't show what
// it started from, and keeps nothing trusted. A missing file is nil.
func (s *Server) readProjectConfigBefore(path string) (before []byte, ok bool) {
	if s.projectTrust == nil {
		return nil, false
	}
	data, err := s.sys.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, true
	}
	if err != nil {
		return nil, false
	}
	return data, true
}

// keepProjectTrust keeps the project config in dir trusted across an
// owner's write from before to after, when it was trusted before. The write
// already landed, so a failure only leaves the project waiting for review.
func (s *Server) keepProjectTrust(dir string, before, after []byte) {
	if err := s.projectTrust.Keep(dir, before, after); err != nil {
		s.logger.Warn("keeping the project config trusted", "dir", dir, "error", err)
	}
}
