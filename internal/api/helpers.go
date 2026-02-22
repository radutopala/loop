package api

import (
	"encoding/json"
	"net/http"
)

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
