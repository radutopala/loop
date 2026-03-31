package api

import "net/http"

// handleListContainers returns all tracked containers.
func (s *Server) handleListContainers(w http.ResponseWriter, r *http.Request) {
	if s.containerRegistry == nil {
		http.Error(w, "container registry not configured", http.StatusServiceUnavailable)
		return
	}
	containers := s.containerRegistry.List()
	writeHTTPJSON(w, http.StatusOK, containers, s.logger)
}
