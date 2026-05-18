package api

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/db"
)

// pasteBody returns a JSON request body for the paste-image endpoint with the
// given image bytes (base64-encoded) and media type.
func pasteBody(raw []byte, mediaType string) string {
	body, _ := json.Marshal(pasteImageRequest{
		Data:      base64.StdEncoding.EncodeToString(raw),
		MediaType: mediaType,
	})
	return string(body)
}

func (s *ServerSuite) TestPasteImage_SuccessPNG() {
	tmpDir := s.T().TempDir()

	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	rec := s.testRequest("POST", "/api/channels/ch-1/paste-image", pasteBody([]byte("PNGDATA"), "image/png"))
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp pasteImageResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.True(s.T(), filepath.IsAbs(resp.Path), "expected absolute path, got %s", resp.Path)
	wantPrefix := filepath.Join(tmpDir, ".loop", "pastes", "paste-")
	require.True(s.T(), strings.HasPrefix(resp.Path, wantPrefix), "path %s missing prefix %s", resp.Path, wantPrefix)
	require.True(s.T(), strings.HasSuffix(resp.Path, ".png"))

	pattern := regexp.QuoteMeta(filepath.Join(tmpDir, ".loop", "pastes")+string(filepath.Separator)) + `paste-\d{8}-\d{6}-[0-9a-f]{8}\.png$`
	matched, err := regexp.MatchString(pattern, resp.Path)
	require.NoError(s.T(), err)
	require.True(s.T(), matched, "filename mismatch: %s", resp.Path)

	// File actually written at the returned absolute path.
	data, err := os.ReadFile(resp.Path)
	require.NoError(s.T(), err)
	require.Equal(s.T(), []byte("PNGDATA"), data)
}

func (s *ServerSuite) TestPasteImage_SuccessAllMediaTypes() {
	cases := []struct {
		media string
		ext   string
	}{
		{"image/png", ".png"},
		{"image/jpeg", ".jpg"},
		{"image/gif", ".gif"},
		{"image/webp", ".webp"},
	}

	for _, tc := range cases {
		s.Run(tc.media, func() {
			tmpDir := s.T().TempDir()
			// Use a unique channel ID per sub-test so mock expectations don't collide.
			chID := "ch-" + strings.TrimPrefix(tc.media, "image/")
			s.store.On("GetChannel", mock.Anything, chID).
				Return(&db.Channel{ChannelID: chID, DirPath: tmpDir}, nil)

			rec := s.testRequest("POST", "/api/channels/"+chID+"/paste-image", pasteBody([]byte("X"), tc.media))
			require.Equal(s.T(), http.StatusOK, rec.Code)

			var resp pasteImageResponse
			require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
			require.True(s.T(), strings.HasSuffix(resp.Path, tc.ext), "wanted ext %s, got %s", tc.ext, resp.Path)
		})
	}
}

func (s *ServerSuite) TestPasteImage_InvalidJSON() {
	tmpDir := s.T().TempDir()
	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	rec := s.testRequest("POST", "/api/channels/ch-1/paste-image", "{not json")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "invalid json body")
}

func (s *ServerSuite) TestPasteImage_UnsupportedMediaType() {
	tmpDir := s.T().TempDir()
	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	rec := s.testRequest("POST", "/api/channels/ch-1/paste-image", pasteBody([]byte("x"), "image/bmp"))
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "unsupported media_type")
}

func (s *ServerSuite) TestPasteImage_EmptyData() {
	tmpDir := s.T().TempDir()
	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	body, _ := json.Marshal(pasteImageRequest{Data: "", MediaType: "image/png"})
	rec := s.testRequest("POST", "/api/channels/ch-1/paste-image", string(body))
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "data required")
}

func (s *ServerSuite) TestPasteImage_InvalidBase64() {
	tmpDir := s.T().TempDir()
	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	body, _ := json.Marshal(pasteImageRequest{Data: "not!!base64!!", MediaType: "image/png"})
	rec := s.testRequest("POST", "/api/channels/ch-1/paste-image", string(body))
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "invalid base64 data")
}

func (s *ServerSuite) TestPasteImage_TooLarge() {
	tmpDir := s.T().TempDir()
	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	big := make([]byte, maxPasteImageBytes+1)
	rec := s.testRequest("POST", "/api/channels/ch-1/paste-image", pasteBody(big, "image/png"))
	require.Equal(s.T(), http.StatusRequestEntityTooLarge, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "image too large")
}

func (s *ServerSuite) TestPasteImage_NotConfigured() {
	srv := nilServer()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/channels/{id}/paste-image", srv.handlePasteImage)

	req, _ := http.NewRequest("POST", "/api/channels/ch-1/paste-image", strings.NewReader(pasteBody([]byte("x"), "image/png")))
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusNotImplemented, w.Code)
}

func (s *ServerSuite) TestPasteImage_ChannelNotFound() {
	s.store.On("GetChannel", mock.Anything, "missing").
		Return((*db.Channel)(nil), nil)

	rec := s.testRequest("POST", "/api/channels/missing/paste-image", pasteBody([]byte("x"), "image/png"))
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestPasteImage_MkdirAllError() {
	tmpDir := s.T().TempDir()
	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	s.sys.Override("MkdirAll", mock.Anything, mock.Anything).Return(fmt.Errorf("injected mkdir error"))
	s.srv.sys = &realOpenSys{s.sys}

	rec := s.testRequest("POST", "/api/channels/ch-1/paste-image", pasteBody([]byte("x"), "image/png"))
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "failed to create pastes directory")
}

func (s *ServerSuite) TestPasteImage_WriteFileError() {
	tmpDir := s.T().TempDir()
	s.store.On("GetChannel", mock.Anything, "ch-1").
		Return(&db.Channel{ChannelID: "ch-1", DirPath: tmpDir}, nil)

	s.sys.Override("WriteFile", mock.Anything, mock.Anything, mock.Anything).Return(fmt.Errorf("injected write error"))
	s.srv.sys = &realOpenSys{s.sys}

	rec := s.testRequest("POST", "/api/channels/ch-1/paste-image", pasteBody([]byte("x"), "image/png"))
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "failed to write image")
}

func (s *ServerSuite) TestPasteFilename_Format() {
	name := pasteFilename(".png")
	matched, err := regexp.MatchString(`^paste-\d{8}-\d{6}-[0-9a-f]{8}\.png$`, name)
	require.NoError(s.T(), err)
	require.True(s.T(), matched, "got %s", name)
}
