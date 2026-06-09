package api

import (
	"net"
	"net/http"

	"github.com/radutopala/loop/internal/fsmigrate"
)

// isLoopbackRequest returns true when the request's peer is on the loopback
// interface (127.0.0.0/8, ::1) AND there is no evidence the request was
// forwarded by a reverse proxy. The daemon defaults to binding 127.0.0.1
// only, but a user who explicitly binds to 0.0.0.0 or a LAN IP and puts a
// same-host reverse proxy (nginx, Caddy, Traefik) in front would otherwise
// see every request appear to originate from 127.0.0.1 — opening write
// endpoints to arbitrary network peers. The proxy-header sniff is best
// effort but is the floor: if any of the well-known forwarding headers are
// set, the original peer is not the daemon's direct caller and we cannot
// trust r.RemoteAddr.
func isLoopbackRequest(r *http.Request) bool {
	if r.Header.Get("X-Forwarded-For") != "" ||
		r.Header.Get("X-Real-IP") != "" ||
		r.Header.Get("Forwarded") != "" {
		return false
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		// Some test setups (httptest.NewServer with a Unix-socket-like
		// listener) leave RemoteAddr in non-host:port shapes. Fall back to
		// the raw value.
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}

// builtinRestoreRequest is the request body for POST /api/builtins/restore.
// Kind selects which family of built-ins to re-seed.
type builtinRestoreRequest struct {
	Kind string `json:"kind"` // "workflows" | "shortcuts"
}

// builtinRestoreResponse reports which built-in names were added (newly
// seeded), patched in place (kept by name but had an internal shape mutated
// to track the current canonical definition), and skipped (already present
// AND already matched the canonical shape). Empty Added + empty Patched with
// a non-empty Skipped means "nothing to do — everything was already there."
type builtinRestoreResponse struct {
	Kind    string   `json:"kind"`
	Added   []string `json:"added"`
	Patched []string `json:"patched"`
	Skipped []string `json:"skipped"`
}

// canonicalBuiltins lists the canonical names of all built-ins per kind. The
// handler computes Skipped = canonicalBuiltins[kind] − Added so the FE can
// show "X already present, Y restored."
var canonicalBuiltins = map[string][]string{
	"shortcuts": {"builtin code review", "builtin simplify"},
	"workflows": {"review-loop", "review-fix-loop"},
}

// handleRestoreBuiltins re-seeds any missing built-in workflows or prompt
// shortcuts into the user's ~/.loop/config.json. Idempotent: items the user
// kept (even if modified) are left untouched. Returns the names added vs
// patched vs skipped so the FE can show a meaningful toast.
//
// Loopback-only: this is the first endpoint that writes to ~/.loop/config.json
// from the HTTP surface (other config writes are gated through the FE which
// only loads in the Electron desktop app's bundled WebContents). The daemon
// defaults to binding 127.0.0.1, but explicit binds to 0.0.0.0 or a LAN IP
// would otherwise let a network peer mutate the user's config. The check is
// a floor — we have no per-user auth scheme yet — but it stops the worst of
// the cross-origin exposure without a config plumb-through.
func (s *Server) handleRestoreBuiltins(w http.ResponseWriter, r *http.Request) {
	if !isLoopbackRequest(r) {
		http.Error(w, "restore is restricted to loopback callers", http.StatusForbidden)
		return
	}
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
		added   []string
		patched []string
		err     error
	)
	switch req.Kind {
	case "workflows":
		added, patched, err = fsmigrate.RestoreBuiltinWorkflows(r.Context(), ctx)
	case "shortcuts":
		added, patched, err = fsmigrate.RestoreBuiltinShortcuts(r.Context(), ctx)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Skipped = canonical − (added ∪ patched). Without subtracting patched,
	// a workflow that the patcher rewrote (e.g. stale verify script swapped
	// to the current canonical version) would be reported as "already
	// present" — misleading the FE toast about whether the user's config
	// changed on disk.
	touched := make(map[string]struct{}, len(added)+len(patched))
	for _, n := range added {
		touched[n] = struct{}{}
	}
	for _, n := range patched {
		touched[n] = struct{}{}
	}
	skipped := []string{}
	for _, n := range canonical {
		if _, ok := touched[n]; !ok {
			skipped = append(skipped, n)
		}
	}
	if added == nil {
		added = []string{}
	}
	if patched == nil {
		patched = []string{}
	}
	writeHTTPJSON(w, http.StatusOK, builtinRestoreResponse{
		Kind:    req.Kind,
		Added:   added,
		Patched: patched,
		Skipped: skipped,
	}, s.logger)
}
