// Package api — paste_image_handler.go implements POST /api/channels/{id}/paste-image.
// The renderer posts a base64-encoded image from the chat input's onPaste; we
// write it under <workspace>/.loop/pastes/ and return the absolute path so the
// user can reference it in their message and the agent can read it via its
// built-in Read tool (which requires absolute paths).
package api

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/radutopala/loop/internal/randutil"
)

// maxPasteImageBytes caps the decoded image size. base64 inflates by ~33%, so
// the HTTP body limit below is sized to fit a maxPasteImageBytes-decoded
// payload plus the small JSON envelope.
const maxPasteImageBytes = 10 * 1024 * 1024 // 10 MiB

// maxPasteImageRequestBytes caps the raw JSON body. ceil(10 MiB * 4/3) plus
// slack for the surrounding JSON.
const maxPasteImageRequestBytes = 14 * 1024 * 1024

// pasteImageRequest is the JSON body posted by the renderer.
type pasteImageRequest struct {
	Data      string `json:"data"`       // base64-encoded image bytes (no data: prefix)
	MediaType string `json:"media_type"` // e.g. image/png
}

// pasteImageResponse echoes the absolute path the image was written to.
type pasteImageResponse struct {
	Path string `json:"path"`
}

// extByMediaType maps the JSON media_type to a file extension. Unlisted
// types are rejected so the saved file's extension always matches what we
// actually wrote.
var extByMediaType = map[string]string{
	"image/png":  ".png",
	"image/jpeg": ".jpg",
	"image/gif":  ".gif",
	"image/webp": ".webp",
}

func (s *Server) handlePasteImage(w http.ResponseWriter, r *http.Request) {
	if !requireConfigured(w, s.store, "channel listing not configured") {
		return
	}

	channelID := r.PathValue("id")
	dirPath, err := s.resolveRootDir(r.Context(), channelID, r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	var req pasteImageRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxPasteImageRequestBytes)).Decode(&req); err != nil {
		http.Error(w, "invalid json body", http.StatusBadRequest)
		return
	}

	ext, ok := extByMediaType[req.MediaType]
	if !ok {
		http.Error(w, "unsupported media_type", http.StatusBadRequest)
		return
	}

	if req.Data == "" {
		http.Error(w, "data required", http.StatusBadRequest)
		return
	}

	data, err := base64.StdEncoding.DecodeString(req.Data)
	if err != nil {
		http.Error(w, "invalid base64 data", http.StatusBadRequest)
		return
	}

	if len(data) > maxPasteImageBytes {
		http.Error(w, "image too large", http.StatusRequestEntityTooLarge)
		return
	}

	pastesDir := filepath.Join(dirPath, ".loop", "pastes")
	if err := s.sys.MkdirAll(pastesDir, 0o755); err != nil {
		http.Error(w, "failed to create pastes directory", http.StatusInternalServerError)
		return
	}

	name := pasteFilename(ext)
	absPath := filepath.Join(pastesDir, name)

	if err := s.sys.WriteFile(absPath, data, 0o644); err != nil {
		http.Error(w, "failed to write image", http.StatusInternalServerError)
		return
	}

	writeHTTPJSON(w, http.StatusOK, pasteImageResponse{Path: absPath}, s.logger)
}

// pasteFilename returns a collision-resistant filename for a pasted image.
// Format: paste-YYYYMMDD-HHMMSS-<8hexrand><ext>.
func pasteFilename(ext string) string {
	ts := time.Now().UTC().Format("20060102-150405")
	return strings.Join([]string{"paste-", ts, "-", randutil.HexID(4), ext}, "")
}
