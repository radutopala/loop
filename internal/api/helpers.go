package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gorilla/websocket"
)

// wsUpgrader is the shared WebSocket upgrader for all WS endpoints.
var wsUpgrader = websocket.Upgrader{
	CheckOrigin: func(_ *http.Request) bool { return true },
}

// decodeJSON reads the request body into dst. On failure it writes a 400 response
// and returns false so the caller can return early.
func decodeJSON[T any](w http.ResponseWriter, r *http.Request, dst *T) bool {
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return false
	}
	return true
}

// requireConfigured checks that service is non-nil. If nil it writes a 501
// response with msg and returns false.
func requireConfigured(w http.ResponseWriter, service any, msg string) bool {
	if service == nil {
		http.Error(w, msg, http.StatusNotImplemented)
		return false
	}
	return true
}

// parsePathInt64 parses a path value as int64. On failure it writes a 400 response
// and returns 0, false.
func parsePathInt64(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	v, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil {
		http.Error(w, fmt.Sprintf("invalid %s", name), http.StatusBadRequest)
		return 0, false
	}
	return v, true
}

// parseQueryInt parses an optional integer query parameter with min/max clamping.
// If the parameter is absent, defaultVal is returned. On parse error it writes a 400
// response and returns 0, false.
func parseQueryInt(w http.ResponseWriter, r *http.Request, name string, defaultVal, maxVal int) (int, bool) {
	s := r.URL.Query().Get(name)
	if s == "" {
		return defaultVal, true
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < 1 {
		http.Error(w, fmt.Sprintf("invalid %s", name), http.StatusBadRequest)
		return 0, false
	}
	if v > maxVal {
		v = maxVal
	}
	return v, true
}

// parseQueryInt64 parses an optional int64 query parameter. Returns 0 if absent.
// On parse error it writes a 400 response and returns 0, false.
func parseQueryInt64(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	s := r.URL.Query().Get(name)
	if s == "" {
		return 0, true
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil || v < 1 {
		http.Error(w, fmt.Sprintf("invalid %s", name), http.StatusBadRequest)
		return 0, false
	}
	return v, true
}

// parseQueryInt64NonNeg parses an optional non-negative int64 query parameter.
// Unlike parseQueryInt64 it accepts 0 as a meaningful value (used by the
// timeline cursor where chain_position=0 represents legacy rows). Returns 0
// if absent. On parse error or negative value it writes a 400 response.
func parseQueryInt64NonNeg(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	s := r.URL.Query().Get(name)
	if s == "" {
		return 0, true
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil || v < 0 {
		http.Error(w, fmt.Sprintf("invalid %s", name), http.StatusBadRequest)
		return 0, false
	}
	return v, true
}

// writeHTTPJSON encodes data as JSON and writes it to w with the given status code.
// It sets the Content-Type header and logs any encoding errors.
func writeHTTPJSON(w http.ResponseWriter, status int, data any, logger *slog.Logger) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		logger.Error("json encode failed", "error", err)
	}
}
