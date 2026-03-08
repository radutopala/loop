package api

import (
	"encoding/json"
	"log/slog"
	"net/http"

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

// writeJSON encodes data as JSON and writes it to w with the given status code.
// It sets the Content-Type header and logs any encoding errors.
func writeJSON(w http.ResponseWriter, status int, data any, logger *slog.Logger) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		logger.Error("json encode failed", "error", err)
	}
}
