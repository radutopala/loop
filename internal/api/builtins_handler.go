package api

import (
	"net/http"

	"github.com/radutopala/loop/internal/fsmigrate"
)

// builtinRestoreRequest is the request body for POST /api/builtins/restore.
// Kind selects which family of built-ins to re-seed.
type builtinRestoreRequest struct {
	Kind string `json:"kind"` // "workflows" | "shortcuts"
}

// builtinRestoreResponse reports which built-in names were added (i.e. were
// missing and have now been written back) and which were skipped (already
// present, possibly user-modified). An empty Added list with a non-empty
// Skipped list means "nothing to do — everything was already there."
type builtinRestoreResponse struct {
	Kind    string   `json:"kind"`
	Added   []string `json:"added"`
	Skipped []string `json:"skipped"`
}

// canonicalBuiltins lists the canonical names of all built-ins per kind. The
// handler computes Skipped = canonicalBuiltins[kind] − Added so the FE can
// show "X already present, Y restored."
var canonicalBuiltins = map[string][]string{
	"shortcuts": {"builtin code review"},
	"workflows": {"review-loop", "review-fix-loop"},
}

// handleRestoreBuiltins re-seeds any missing built-in workflows or prompt
// shortcuts into the user's ~/.loop/config.json. Idempotent: items the user
// kept (even if modified) are left untouched. Returns the names added vs
// skipped so the FE can show a meaningful toast.
func (s *Server) handleRestoreBuiltins(w http.ResponseWriter, r *http.Request) {
	var req builtinRestoreRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	canonical, ok := canonicalBuiltins[req.Kind]
	if !ok {
		http.Error(w, "kind must be 'workflows' or 'shortcuts'", http.StatusBadRequest)
		return
	}
	if s.loopDir == "" {
		http.Error(w, "loop directory not configured", http.StatusInternalServerError)
		return
	}

	ctx := &fsmigrate.Ctx{Sys: s.sys, LoopDir: s.loopDir}
	var (
		added []string
		err   error
	)
	switch req.Kind {
	case "workflows":
		added, err = fsmigrate.RestoreBuiltinWorkflows(r.Context(), ctx)
	case "shortcuts":
		added, err = fsmigrate.RestoreBuiltinShortcuts(r.Context(), ctx)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	addedSet := make(map[string]struct{}, len(added))
	for _, n := range added {
		addedSet[n] = struct{}{}
	}
	skipped := []string{}
	for _, n := range canonical {
		if _, ok := addedSet[n]; !ok {
			skipped = append(skipped, n)
		}
	}
	if added == nil {
		added = []string{}
	}
	writeHTTPJSON(w, http.StatusOK, builtinRestoreResponse{
		Kind:    req.Kind,
		Added:   added,
		Skipped: skipped,
	}, s.logger)
}
