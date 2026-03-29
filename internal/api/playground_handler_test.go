package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"

	"github.com/stretchr/testify/require"
)

func (s *ServerSuite) setPlaygroundDir() string {
	dir := s.T().TempDir()
	s.srv.loopDir = dir
	return dir
}

// --- handlePlaygroundUpdate ---

func (s *ServerSuite) TestPlaygroundUpdateSuccess() {
	dir := s.setPlaygroundDir()

	hub := NewEventsHub(testLogger())
	s.srv.SetEventsHub(hub)

	body := `{"html":"<h1>Hello</h1>","title":"My App","description":"A cool app"}`
	rec := s.testRequest("PUT", "/api/playground?name=my-app", body)
	require.Equal(s.T(), http.StatusOK, rec.Code)

	pgDir := filepath.Join(dir, "playground", "my-app")
	html, err := os.ReadFile(filepath.Join(pgDir, "index.html"))
	require.NoError(s.T(), err)
	require.Equal(s.T(), "<h1>Hello</h1>", string(html))

	readme, err := os.ReadFile(filepath.Join(pgDir, "README.md"))
	require.NoError(s.T(), err)
	require.Contains(s.T(), string(readme), "title: My App")
	require.Contains(s.T(), string(readme), "A cool app")
}

func (s *ServerSuite) TestPlaygroundUpdateNoDescription() {
	s.setPlaygroundDir()

	hub := NewEventsHub(testLogger())
	s.srv.SetEventsHub(hub)

	body := `{"html":"<h1>No desc</h1>"}`
	rec := s.testRequest("PUT", "/api/playground?name=nodesc", body)
	require.Equal(s.T(), http.StatusOK, rec.Code)
}

func (s *ServerSuite) TestPlaygroundUpdateInvalidJSON() {
	s.setPlaygroundDir()
	rec := s.testRequest("PUT", "/api/playground?name=test", "not json")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestPlaygroundUpdateMissingName() {
	rec := s.testRequest("PUT", "/api/playground", `{"html":"<p>test</p>"}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "invalid or missing playground name")
}

func (s *ServerSuite) TestPlaygroundUpdateInvalidName() {
	rec := s.testRequest("PUT", "/api/playground?name=../evil", `{"html":"<p>test</p>"}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestPlaygroundUpdateMkdirError() {
	dir := s.setPlaygroundDir()
	// Block dir creation by placing a file where the directory should be.
	require.NoError(s.T(), os.MkdirAll(filepath.Join(dir, "playground"), 0o755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(dir, "playground", "blocked"), []byte("x"), 0o644))

	// Name "blocked" collides with the file.
	s.srv.loopDir = dir
	// Create a file at the exact path where MkdirAll needs a directory.
	blockPath := filepath.Join(dir, "playground", "fail")
	require.NoError(s.T(), os.WriteFile(blockPath, []byte("x"), 0o644))

	body := `{"html":"<p>test</p>"}`
	rec := s.testRequest("PUT", "/api/playground?name=fail", body)
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "creating playground dir")
}

func (s *ServerSuite) TestPlaygroundUpdateFileWriteError() {
	dir := s.setPlaygroundDir()
	pgDir := filepath.Join(dir, "playground", "broken")
	require.NoError(s.T(), os.MkdirAll(pgDir, 0o755))
	// Block index.html write by making it a directory.
	require.NoError(s.T(), os.MkdirAll(filepath.Join(pgDir, "index.html"), 0o755))

	body := `{"html":"<p>fail</p>"}`
	rec := s.testRequest("PUT", "/api/playground?name=broken", body)
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "writing")
}

func (s *ServerSuite) TestPlaygroundUpdateNoEventsHub() {
	s.setPlaygroundDir()
	s.srv.eventsHub = nil

	body := `{"html":"<p>no hub</p>"}`
	rec := s.testRequest("PUT", "/api/playground?name=nohub", body)
	require.Equal(s.T(), http.StatusOK, rec.Code)
}

// --- handlePlaygroundGet ---

func (s *ServerSuite) TestPlaygroundGetSuccess() {
	dir := s.setPlaygroundDir()
	pgDir := filepath.Join(dir, "playground", "my-app")
	require.NoError(s.T(), os.MkdirAll(pgDir, 0o755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(pgDir, "index.html"), []byte("<p>hi</p>"), 0o644))
	require.NoError(s.T(), os.WriteFile(filepath.Join(pgDir, "README.md"), []byte("---\ntitle: My App\n---\n\nA cool app\n"), 0o644))

	rec := s.testRequest("GET", "/api/playground?name=my-app", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var content playgroundContent
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &content))
	require.Equal(s.T(), "my-app", content.Name)
	require.Equal(s.T(), "My App", content.Title)
	require.Equal(s.T(), "<p>hi</p>", content.HTML)
	require.Equal(s.T(), "A cool app", content.Description)
}

func (s *ServerSuite) TestPlaygroundGetEmptyReadme() {
	dir := s.setPlaygroundDir()
	pgDir := filepath.Join(dir, "playground", "empty-readme")
	require.NoError(s.T(), os.MkdirAll(pgDir, 0o755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(pgDir, "index.html"), []byte("<p>hi</p>"), 0o644))
	require.NoError(s.T(), os.WriteFile(filepath.Join(pgDir, "style.css"), []byte(""), 0o644))
	require.NoError(s.T(), os.WriteFile(filepath.Join(pgDir, "script.js"), []byte(""), 0o644))
	// Empty README — no frontmatter, no body.

	rec := s.testRequest("GET", "/api/playground?name=empty-readme", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var content playgroundContent
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &content))
	require.Equal(s.T(), "", content.Title)
	require.Equal(s.T(), "", content.Description)
}

func (s *ServerSuite) TestPlaygroundGetNoFrontmatter() {
	dir := s.setPlaygroundDir()
	pgDir := filepath.Join(dir, "playground", "plain")
	require.NoError(s.T(), os.MkdirAll(pgDir, 0o755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(pgDir, "index.html"), []byte("<p>hi</p>"), 0o644))
	require.NoError(s.T(), os.WriteFile(filepath.Join(pgDir, "style.css"), []byte(""), 0o644))
	require.NoError(s.T(), os.WriteFile(filepath.Join(pgDir, "script.js"), []byte(""), 0o644))
	require.NoError(s.T(), os.WriteFile(filepath.Join(pgDir, "README.md"), []byte("Just plain text, no frontmatter"), 0o644))

	rec := s.testRequest("GET", "/api/playground?name=plain", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var content playgroundContent
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &content))
	require.Equal(s.T(), "", content.Title)
	require.Equal(s.T(), "Just plain text, no frontmatter", content.Description)
}

func (s *ServerSuite) TestPlaygroundGetNotFound() {
	s.setPlaygroundDir()
	rec := s.testRequest("GET", "/api/playground?name=nonexistent", "")
	require.Equal(s.T(), http.StatusNotFound, rec.Code)
}

func (s *ServerSuite) TestPlaygroundGetMissingName() {
	rec := s.testRequest("GET", "/api/playground", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

// --- handlePlaygroundList ---

func (s *ServerSuite) TestPlaygroundListSuccess() {
	dir := s.setPlaygroundDir()
	baseDir := filepath.Join(dir, "playground")
	require.NoError(s.T(), os.MkdirAll(filepath.Join(baseDir, "app1"), 0o755))
	require.NoError(s.T(), os.MkdirAll(filepath.Join(baseDir, "app2"), 0o755))
	// Add a README with frontmatter to app1.
	require.NoError(s.T(), os.WriteFile(filepath.Join(baseDir, "app1", "README.md"), []byte("---\ntitle: My App\n---\nSome description"), 0o644))
	// Create a file (not dir) — should be excluded.
	require.NoError(s.T(), os.WriteFile(filepath.Join(baseDir, "stray-file"), []byte("x"), 0o644))

	rec := s.testRequest("GET", "/api/playground/items", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var result struct{ Items []playgroundItem }
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &result))
	require.Len(s.T(), result.Items, 2)

	names := make(map[string]string)
	for _, item := range result.Items {
		names[item.Name] = item.Title
	}
	require.Equal(s.T(), "My App", names["app1"])
	require.Equal(s.T(), "", names["app2"])
}

func (s *ServerSuite) TestPlaygroundListEmpty() {
	s.setPlaygroundDir()
	rec := s.testRequest("GET", "/api/playground/items", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var result struct{ Items []playgroundItem }
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &result))
	require.Empty(s.T(), result.Items)
}

func (s *ServerSuite) TestPlaygroundListOnlyInvalidNames() {
	dir := s.setPlaygroundDir()
	baseDir := filepath.Join(dir, "playground")
	require.NoError(s.T(), os.MkdirAll(filepath.Join(baseDir, ".hidden"), 0o755))
	require.NoError(s.T(), os.MkdirAll(filepath.Join(baseDir, "-starts-dash"), 0o755))

	rec := s.testRequest("GET", "/api/playground/items", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var result struct{ Items []playgroundItem }
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &result))
	require.Empty(s.T(), result.Items)
	require.NotNil(s.T(), result.Items)
}

// --- handlePlaygroundServe ---

func (s *ServerSuite) TestPlaygroundServeSuccess() {
	dir := s.setPlaygroundDir()
	pgDir := filepath.Join(dir, "playground", "my-app")
	require.NoError(s.T(), os.MkdirAll(pgDir, 0o755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(pgDir, "index.html"), []byte("<div>hello</div>"), 0o644))
	require.NoError(s.T(), os.WriteFile(filepath.Join(pgDir, "importmap.json"), []byte(`{"imports":{"react":"https://esm.sh/react"}}`), 0o644))

	rec := s.testRequest("GET", "/api/playground/serve/my-app", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	require.Contains(s.T(), rec.Header().Get("Content-Type"), "text/html")
	require.Contains(s.T(), rec.Body.String(), "<div>hello</div>")
	require.Contains(s.T(), rec.Body.String(), `<script type="importmap">`)
	require.Contains(s.T(), rec.Body.String(), `<script type="module" src="script.js">`)
	require.Contains(s.T(), rec.Body.String(), "playground-console")
}

func (s *ServerSuite) TestPlaygroundServeEmpty() {
	dir := s.setPlaygroundDir()
	pgDir := filepath.Join(dir, "playground", "empty")
	require.NoError(s.T(), os.MkdirAll(pgDir, 0o755))

	rec := s.testRequest("GET", "/api/playground/serve/empty", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "waiting for code from agent")
}

func (s *ServerSuite) TestPlaygroundServeInvalidName() {
	rec := s.testRequest("GET", "/api/playground/serve/..%2Fevil", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

// --- handlePlaygroundServeFile ---

func (s *ServerSuite) TestPlaygroundServeFileSuccess() {
	dir := s.setPlaygroundDir()
	pgDir := filepath.Join(dir, "playground", "my-app")
	require.NoError(s.T(), os.MkdirAll(pgDir, 0o755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(pgDir, "script.js"), []byte("console.log('hi')"), 0o644))
	require.NoError(s.T(), os.WriteFile(filepath.Join(pgDir, "style.css"), []byte("body{}"), 0o644))

	rec := s.testRequest("GET", "/api/playground/serve/my-app/script.js", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	require.Contains(s.T(), rec.Header().Get("Content-Type"), "javascript")
	require.Equal(s.T(), "console.log('hi')", rec.Body.String())

	rec = s.testRequest("GET", "/api/playground/serve/my-app/style.css", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	require.Contains(s.T(), rec.Header().Get("Content-Type"), "css")
}

func (s *ServerSuite) TestPlaygroundServeFileSubdir() {
	dir := s.setPlaygroundDir()
	pgDir := filepath.Join(dir, "playground", "my-app", "lib")
	require.NoError(s.T(), os.MkdirAll(pgDir, 0o755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(pgDir, "utils.js"), []byte("export default 42"), 0o644))

	rec := s.testRequest("GET", "/api/playground/serve/my-app/lib/utils.js", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	require.Equal(s.T(), "export default 42", rec.Body.String())
}

func (s *ServerSuite) TestPlaygroundServeFileNotFound() {
	s.setPlaygroundDir()
	rec := s.testRequest("GET", "/api/playground/serve/my-app/nonexistent.js", "")
	require.Equal(s.T(), http.StatusNotFound, rec.Code)
}

func (s *ServerSuite) TestPlaygroundServeFileUnknownExtension() {
	dir := s.setPlaygroundDir()
	pgDir := filepath.Join(dir, "playground", "my-app")
	require.NoError(s.T(), os.MkdirAll(pgDir, 0o755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(pgDir, "data"), []byte("binary data"), 0o644))

	rec := s.testRequest("GET", "/api/playground/serve/my-app/data", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	require.Equal(s.T(), "application/octet-stream", rec.Header().Get("Content-Type"))
}

func (s *ServerSuite) TestPlaygroundServeFileInvalidName() {
	rec := s.testRequest("GET", "/api/playground/serve/..evil/file.js", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

// --- handlePlaygroundDelete ---

func (s *ServerSuite) TestPlaygroundDeleteSuccess() {
	dir := s.setPlaygroundDir()
	pgDir := filepath.Join(dir, "playground", "doomed")
	require.NoError(s.T(), os.MkdirAll(pgDir, 0o755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(pgDir, "index.html"), []byte("<p>bye</p>"), 0o644))

	hub := NewEventsHub(testLogger())
	s.srv.SetEventsHub(hub)

	rec := s.testRequest("DELETE", "/api/playground?name=doomed", "")
	require.Equal(s.T(), http.StatusNoContent, rec.Code)

	_, err := os.Stat(pgDir)
	require.True(s.T(), os.IsNotExist(err))
}

func (s *ServerSuite) TestPlaygroundDeleteNotFound() {
	s.setPlaygroundDir()
	rec := s.testRequest("DELETE", "/api/playground?name=nonexistent", "")
	require.Equal(s.T(), http.StatusNotFound, rec.Code)
}

func (s *ServerSuite) TestPlaygroundDeleteNoEventsHub() {
	dir := s.setPlaygroundDir()
	pgDir := filepath.Join(dir, "playground", "doomed2")
	require.NoError(s.T(), os.MkdirAll(pgDir, 0o755))
	s.srv.eventsHub = nil

	rec := s.testRequest("DELETE", "/api/playground?name=doomed2", "")
	require.Equal(s.T(), http.StatusNoContent, rec.Code)
}

func (s *ServerSuite) TestPlaygroundDeleteMissingName() {
	rec := s.testRequest("DELETE", "/api/playground", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

// --- handlePlaygroundFileWrite ---

func (s *ServerSuite) TestPlaygroundFileWriteSuccess() {
	dir := s.setPlaygroundDir()
	pgDir := filepath.Join(dir, "playground", "my-app")
	require.NoError(s.T(), os.MkdirAll(pgDir, 0o755))

	hub := NewEventsHub(testLogger())
	s.srv.SetEventsHub(hub)

	rec := s.testRequest("PUT", "/api/playground/file?name=my-app&path=script.js", "console.log('hi')")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	data, err := os.ReadFile(filepath.Join(pgDir, "script.js"))
	require.NoError(s.T(), err)
	require.Equal(s.T(), "console.log('hi')", string(data))
}

func (s *ServerSuite) TestPlaygroundFileWriteSubdir() {
	dir := s.setPlaygroundDir()
	pgDir := filepath.Join(dir, "playground", "my-app")
	require.NoError(s.T(), os.MkdirAll(pgDir, 0o755))

	rec := s.testRequest("PUT", "/api/playground/file?name=my-app&path=lib/utils.js", "export default 42")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	data, err := os.ReadFile(filepath.Join(pgDir, "lib", "utils.js"))
	require.NoError(s.T(), err)
	require.Equal(s.T(), "export default 42", string(data))
}

func (s *ServerSuite) TestPlaygroundFileWriteSubdirError() {
	dir := s.setPlaygroundDir()
	pgDir := filepath.Join(dir, "playground", "my-app")
	require.NoError(s.T(), os.MkdirAll(pgDir, 0o755))
	// Block subdirectory creation.
	require.NoError(s.T(), os.WriteFile(filepath.Join(pgDir, "lib"), []byte("x"), 0o644))

	rec := s.testRequest("PUT", "/api/playground/file?name=my-app&path=lib/utils.js", "data")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "creating directory")
}

func (s *ServerSuite) TestPlaygroundFileWriteBodyReadError() {
	s.setPlaygroundDir()
	req := httptest.NewRequest("PUT", "/api/playground/file?name=my-app&path=script.js", &errReader{})
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "reading body")
}

func (s *ServerSuite) TestPlaygroundFileWriteError() {
	dir := s.setPlaygroundDir()
	pgDir := filepath.Join(dir, "playground", "my-app")
	require.NoError(s.T(), os.MkdirAll(pgDir, 0o755))
	require.NoError(s.T(), os.MkdirAll(filepath.Join(pgDir, "script.js"), 0o755))

	rec := s.testRequest("PUT", "/api/playground/file?name=my-app&path=script.js", "data")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "writing file")
}

func (s *ServerSuite) TestPlaygroundFileWriteMissingPath() {
	s.setPlaygroundDir()
	rec := s.testRequest("PUT", "/api/playground/file?name=my-app", "data")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestPlaygroundFileWriteNoEventsHub() {
	dir := s.setPlaygroundDir()
	pgDir := filepath.Join(dir, "playground", "my-app")
	require.NoError(s.T(), os.MkdirAll(pgDir, 0o755))
	s.srv.eventsHub = nil

	rec := s.testRequest("PUT", "/api/playground/file?name=my-app&path=script.js", "data")
	require.Equal(s.T(), http.StatusOK, rec.Code)
}

func (s *ServerSuite) TestPlaygroundFileDeleteNoEventsHub() {
	dir := s.setPlaygroundDir()
	pgDir := filepath.Join(dir, "playground", "my-app")
	require.NoError(s.T(), os.MkdirAll(pgDir, 0o755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(pgDir, "old.js"), []byte("x"), 0o644))
	s.srv.eventsHub = nil

	rec := s.testRequest("DELETE", "/api/playground/file?name=my-app&path=old.js", "")
	require.Equal(s.T(), http.StatusNoContent, rec.Code)
}

func (s *ServerSuite) TestPlaygroundUpdateReadmeWriteError() {
	dir := s.setPlaygroundDir()
	pgDir := filepath.Join(dir, "playground", "broken")
	require.NoError(s.T(), os.MkdirAll(pgDir, 0o755))
	require.NoError(s.T(), os.MkdirAll(filepath.Join(pgDir, "README.md"), 0o755))

	body := `{"html":"ok","title":"Broken","description":"fail"}`
	rec := s.testRequest("PUT", "/api/playground?name=broken", body)
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "writing README.md")
}

func (s *ServerSuite) TestPlaygroundFileWriteMissingName() {
	rec := s.testRequest("PUT", "/api/playground/file?path=script.js", "data")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

// --- handlePlaygroundFileRead ---

func (s *ServerSuite) TestPlaygroundFileReadSuccess() {
	dir := s.setPlaygroundDir()
	pgDir := filepath.Join(dir, "playground", "my-app")
	require.NoError(s.T(), os.MkdirAll(pgDir, 0o755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(pgDir, "script.js"), []byte("console.log('hi')"), 0o644))

	rec := s.testRequest("GET", "/api/playground/file?name=my-app&path=script.js", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	require.Equal(s.T(), "console.log('hi')", rec.Body.String())
}

func (s *ServerSuite) TestPlaygroundFileReadNotFound() {
	s.setPlaygroundDir()
	rec := s.testRequest("GET", "/api/playground/file?name=my-app&path=nope.js", "")
	require.Equal(s.T(), http.StatusNotFound, rec.Code)
}

func (s *ServerSuite) TestPlaygroundFileReadMissingPath() {
	s.setPlaygroundDir()
	rec := s.testRequest("GET", "/api/playground/file?name=my-app", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

// --- handlePlaygroundFileDelete ---

func (s *ServerSuite) TestPlaygroundFileDeleteSuccess() {
	dir := s.setPlaygroundDir()
	pgDir := filepath.Join(dir, "playground", "my-app")
	require.NoError(s.T(), os.MkdirAll(pgDir, 0o755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(pgDir, "old.js"), []byte("x"), 0o644))

	hub := NewEventsHub(testLogger())
	s.srv.SetEventsHub(hub)

	rec := s.testRequest("DELETE", "/api/playground/file?name=my-app&path=old.js", "")
	require.Equal(s.T(), http.StatusNoContent, rec.Code)

	_, err := os.Stat(filepath.Join(pgDir, "old.js"))
	require.True(s.T(), os.IsNotExist(err))
}

func (s *ServerSuite) TestPlaygroundFileDeleteNotFound() {
	s.setPlaygroundDir()
	rec := s.testRequest("DELETE", "/api/playground/file?name=my-app&path=nope.js", "")
	require.Equal(s.T(), http.StatusNotFound, rec.Code)
}

func (s *ServerSuite) TestPlaygroundFileDeleteMissingPath() {
	s.setPlaygroundDir()
	rec := s.testRequest("DELETE", "/api/playground/file?name=my-app", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

// --- handlePlaygroundFileList ---

func (s *ServerSuite) TestPlaygroundFileListSuccess() {
	dir := s.setPlaygroundDir()
	pgDir := filepath.Join(dir, "playground", "my-app")
	require.NoError(s.T(), os.MkdirAll(filepath.Join(pgDir, "lib"), 0o755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(pgDir, "index.html"), []byte("<p>hi</p>"), 0o644))
	require.NoError(s.T(), os.WriteFile(filepath.Join(pgDir, "script.js"), []byte("x"), 0o644))
	require.NoError(s.T(), os.WriteFile(filepath.Join(pgDir, "lib", "utils.js"), []byte("x"), 0o644))

	rec := s.testRequest("GET", "/api/playground/files?name=my-app", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var result struct{ Files []string }
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &result))
	require.Contains(s.T(), result.Files, "index.html")
	require.Contains(s.T(), result.Files, "script.js")
	require.Contains(s.T(), result.Files, filepath.Join("lib", "utils.js"))
}

func (s *ServerSuite) TestPlaygroundFileListEmpty() {
	s.setPlaygroundDir()
	rec := s.testRequest("GET", "/api/playground/files?name=nonexistent", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var result struct{ Files []string }
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &result))
	require.Empty(s.T(), result.Files)
}

func (s *ServerSuite) TestPlaygroundDeleteRemoveError() {
	dir := s.setPlaygroundDir()
	pgDir := filepath.Join(dir, "playground", "locked")
	require.NoError(s.T(), os.MkdirAll(pgDir, 0o755))
	// Make parent read-only so RemoveAll fails.
	require.NoError(s.T(), os.Chmod(filepath.Join(dir, "playground"), 0o555))
	defer os.Chmod(filepath.Join(dir, "playground"), 0o755) //nolint:errcheck

	rec := s.testRequest("DELETE", "/api/playground?name=locked", "")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	require.Contains(s.T(), rec.Body.String(), "deleting playground")
}

func (s *ServerSuite) TestPlaygroundFileReadMissingName() {
	rec := s.testRequest("GET", "/api/playground/file", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestPlaygroundFileDeleteMissingName() {
	rec := s.testRequest("DELETE", "/api/playground/file", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestPlaygroundFileListWalkError() {
	dir := s.setPlaygroundDir()
	pgDir := filepath.Join(dir, "playground", "broken-walk")
	require.NoError(s.T(), os.MkdirAll(pgDir, 0o755))
	// Create a broken symlink — WalkDir's callback receives an error for it.
	require.NoError(s.T(), os.Symlink("/nonexistent-target", filepath.Join(pgDir, "broken-link")))

	rec := s.testRequest("GET", "/api/playground/files?name=broken-walk", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
}

func (s *ServerSuite) TestPlaygroundFileListMissingName() {
	rec := s.testRequest("GET", "/api/playground/files", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}
