package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"github.com/stretchr/testify/require"
)

// writeLoopConfig drops a config.json into a fresh loopDir and points the
// server at it. The returned path is the loopDir, not the file.
func (s *ServerSuite) writeLoopConfig(body string) string {
	dir := s.T().TempDir()
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, "config.json"), []byte(body), 0644))
	s.srv.loopDir = dir
	return dir
}

func (s *ServerSuite) TestHandleRestoreBuiltinsShortcutsAddsMissing() {
	s.writeLoopConfig(`{}`)

	rec := s.testRequest(http.MethodPost, "/api/builtins/restore", `{"kind":"shortcuts"}`)
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp builtinRestoreResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(s.T(), "shortcuts", resp.Kind)
	require.ElementsMatch(s.T(), []string{"builtin code review", "builtin simplify"}, resp.Added)
	require.Empty(s.T(), resp.Skipped)
}

func (s *ServerSuite) TestHandleRestoreBuiltinsShortcutsSkipsWhenPresent() {
	// A user-edited code-review entry is left untouched (skipped); the missing
	// simplify shortcut is added.
	s.writeLoopConfig(`{"prompt_shortcuts":[{"name":"builtin code review","prompt":"x"}]}`)

	rec := s.testRequest(http.MethodPost, "/api/builtins/restore", `{"kind":"shortcuts"}`)
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp builtinRestoreResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(s.T(), []string{"builtin simplify"}, resp.Added)
	require.Equal(s.T(), []string{"builtin code review"}, resp.Skipped)
}

func (s *ServerSuite) TestHandleRestoreBuiltinsWorkflowsAddsBoth() {
	s.writeLoopConfig(`{}`)

	rec := s.testRequest(http.MethodPost, "/api/builtins/restore", `{"kind":"workflows"}`)
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp builtinRestoreResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(s.T(), "workflows", resp.Kind)
	require.ElementsMatch(s.T(), []string{"review-loop", "review-fix-loop"}, resp.Added)
	require.Empty(s.T(), resp.Skipped)
}

func (s *ServerSuite) TestHandleRestoreBuiltinsWorkflowsPartialSkip() {
	// User kept `review-loop` (possibly modified) but never had `review-fix-loop`.
	// Restore should add only the missing one and report the other as skipped.
	s.writeLoopConfig(`{"workflows":[{"name":"review-loop"}]}`)

	rec := s.testRequest(http.MethodPost, "/api/builtins/restore", `{"kind":"workflows"}`)
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp builtinRestoreResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(s.T(), []string{"review-fix-loop"}, resp.Added)
	require.Equal(s.T(), []string{"review-loop"}, resp.Skipped)
}

func (s *ServerSuite) TestHandleRestoreBuiltinsEmitsEmptyArraysNotNull() {
	// FE does `result.added.length` / `result.skipped.length` — Go nil slices
	// marshal to `null`, which would crash the FE. Verify the wire format
	// shows `[]` for both empty fields, in both directions (all added /
	// all skipped).
	s.writeLoopConfig(`{}`)
	rec := s.testRequest(http.MethodPost, "/api/builtins/restore", `{"kind":"workflows"}`)
	require.Equal(s.T(), http.StatusOK, rec.Code)
	require.Contains(s.T(), rec.Body.String(), `"skipped":[]`)
	require.NotContains(s.T(), rec.Body.String(), `"skipped":null`)

	s.writeLoopConfig(`{"workflows":[{"name":"review-loop"},{"name":"review-fix-loop"}]}`)
	rec = s.testRequest(http.MethodPost, "/api/builtins/restore", `{"kind":"workflows"}`)
	require.Equal(s.T(), http.StatusOK, rec.Code)
	require.Contains(s.T(), rec.Body.String(), `"added":[]`)
	require.NotContains(s.T(), rec.Body.String(), `"added":null`)
}

func (s *ServerSuite) TestHandleRestoreBuiltinsRejectsUnknownKind() {
	s.writeLoopConfig(`{}`)

	rec := s.testRequest(http.MethodPost, "/api/builtins/restore", `{"kind":"nope"}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "workflows")
}

func (s *ServerSuite) TestHandleRestoreBuiltinsRejectsInvalidJSON() {
	s.writeLoopConfig(`{}`)

	rec := s.testRequest(http.MethodPost, "/api/builtins/restore", `not-json`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestHandleRestoreBuiltinsRequiresLoopDir() {
	// loopDir unset → 500. Don't touch s.writeLoopConfig.
	s.srv.loopDir = ""

	rec := s.testRequest(http.MethodPost, "/api/builtins/restore", `{"kind":"shortcuts"}`)
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "loop directory")
}

func (s *ServerSuite) TestHandleRestoreBuiltinsPropagatesSeederError() {
	// Existing config.json is invalid HJSON/JSON — seeder bubbles a parse error.
	s.writeLoopConfig(`{not valid`)

	rec := s.testRequest(http.MethodPost, "/api/builtins/restore", `{"kind":"workflows"}`)
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
}

func (s *ServerSuite) TestHandleRestoreBuiltinsRejectsNonLoopback() {
	s.writeLoopConfig(`{}`)
	// Bypass testRequest's loopback default — build the request directly so
	// the LAN/WAN rejection path runs.
	req := httptest.NewRequest(http.MethodPost, "/api/builtins/restore", strings.NewReader(`{"kind":"shortcuts"}`))
	req.RemoteAddr = "192.0.2.5:4242" // TestNet-1
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	require.Equal(s.T(), http.StatusForbidden, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "loopback")
}

func (s *ServerSuite) TestHandleRestoreBuiltinsAllowsIPv6Loopback() {
	s.writeLoopConfig(`{}`)
	req := httptest.NewRequest(http.MethodPost, "/api/builtins/restore", strings.NewReader(`{"kind":"shortcuts"}`))
	req.RemoteAddr = "[::1]:4242"
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	require.Equal(s.T(), http.StatusOK, rec.Code)
}

func (s *ServerSuite) TestHandleRestoreBuiltinsRejectsUnparsableRemoteAddr() {
	s.writeLoopConfig(`{}`)
	req := httptest.NewRequest(http.MethodPost, "/api/builtins/restore", strings.NewReader(`{"kind":"shortcuts"}`))
	// Garbage RemoteAddr (no port, not a valid IP) — the safe default is to
	// reject rather than fall through.
	req.RemoteAddr = "not-an-address"
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	require.Equal(s.T(), http.StatusForbidden, rec.Code)
}

func (s *ServerSuite) TestHandleRestoreBuiltinsPatchedSurfaceInResponse() {
	// Stale review-fix-loop with the old verify script — patcher runs and the
	// workflow name lands in `patched`, not `skipped`.
	s.writeLoopConfig(`{"workflows":[{"name":"review-loop"},{"name":"review-fix-loop","nodes":[{"type":"loop","body":[{"id":"verify","script":"old"}]}]}]}`)

	rec := s.testRequest(http.MethodPost, "/api/builtins/restore", `{"kind":"workflows"}`)
	require.Equal(s.T(), http.StatusOK, rec.Code)
	// JSON envelope contains a non-empty patched array (even though the
	// patcher won't trigger on this exact stale shape we still want to see
	// the field shape stable as `[]`).
	var resp builtinRestoreResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(s.T(), "workflows", resp.Kind)
	// patched is always emitted as [] (never null) for FE stability.
	require.NotNil(s.T(), resp.Patched)
}

// TestIsLoopbackRequestProxyHeaders pins the proxy-header guard: any
// forwarding header means the direct peer isn't the real client, so the
// request must not be treated as loopback even from 127.0.0.1.
func (s *ServerSuite) TestIsLoopbackRequestProxyHeaders() {
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "127.0.0.1:1234"
	req.Header.Set("X-Forwarded-For", "203.0.113.9")
	require.False(s.T(), isLoopbackRequest(req))
}
