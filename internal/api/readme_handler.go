package api

import (
	"net/http"

	"github.com/radutopala/loop/internal/readme"
)

func (s *Server) handleGetReadme(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(readme.Content))
}
