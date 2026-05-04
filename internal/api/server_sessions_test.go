package api

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/testutil"
)

// --- ListSessions tests ---

// mockDirEntry implements fs.DirEntry for testing.
type mockDirEntry struct {
	name    string
	isDir   bool
	modTime time.Time
	infoErr error
}

func (m *mockDirEntry) Name() string      { return m.name }
func (m *mockDirEntry) IsDir() bool       { return m.isDir }
func (m *mockDirEntry) Type() fs.FileMode { return 0 }
func (m *mockDirEntry) Info() (fs.FileInfo, error) {
	if m.infoErr != nil {
		return nil, m.infoErr
	}
	return &mockFileInfo{name: m.name, modTime: m.modTime}, nil
}

type mockFileInfo struct {
	name    string
	size    int64
	modTime time.Time
}

func (m *mockFileInfo) Name() string       { return m.name }
func (m *mockFileInfo) Size() int64        { return m.size }
func (m *mockFileInfo) Mode() fs.FileMode  { return 0 }
func (m *mockFileInfo) ModTime() time.Time { return m.modTime }
func (m *mockFileInfo) IsDir() bool        { return false }
func (m *mockFileInfo) Sys() any           { return nil }

// realOpenSys wraps MockSystem but delegates Open and EvalSymlinks to the real
// OS implementations (for tests that use real temp directories).
type realOpenSys struct{ *testutil.MockSystem }

func (r *realOpenSys) Open(name string) (*os.File, error)       { return os.Open(name) }
func (r *realOpenSys) EvalSymlinks(path string) (string, error) { return filepath.EvalSymlinks(path) }

func (s *ServerSuite) TestSessionListSuccess() {
	// Create a temp dir to simulate the Claude projects directory.
	tmpDir := s.T().TempDir()
	projectDir := filepath.Join(tmpDir, ".claude", "projects", "-Users-test-dev-myproject")
	require.NoError(s.T(), os.MkdirAll(projectDir, 0755))

	// Create .jsonl files with different mod times.
	t1 := time.Now().Add(-2 * time.Hour)
	t2 := time.Now().Add(-1 * time.Hour)
	t3 := time.Now()

	f1 := filepath.Join(projectDir, "session-aaa.jsonl")
	f2 := filepath.Join(projectDir, "session-bbb.jsonl")
	f3 := filepath.Join(projectDir, "session-ccc.jsonl")

	userLine := `{"type":"user","message":{"role":"user","content":"What is Go?"}}`
	require.NoError(s.T(), os.WriteFile(f1, []byte(userLine+"\n"), 0644))
	assistantLine := `{"type":"assistant","message":{"content":[{"type":"text","text":"Hello from Claude!"}]}}`
	require.NoError(s.T(), os.WriteFile(f2, []byte(assistantLine+"\n"), 0644))
	require.NoError(s.T(), os.WriteFile(f3, []byte("{}"), 0644))

	require.NoError(s.T(), os.Chtimes(f1, t1, t1))
	require.NoError(s.T(), os.Chtimes(f2, t2, t2))
	require.NoError(s.T(), os.Chtimes(f3, t3, t3))

	// Also create a non-.jsonl file and a directory that should be skipped.
	require.NoError(s.T(), os.WriteFile(filepath.Join(projectDir, "notes.txt"), []byte("hi"), 0644))
	require.NoError(s.T(), os.MkdirAll(filepath.Join(projectDir, "subdir"), 0755))

	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{
		ChannelID: "ch-1",
		DirPath:   "/Users/test/dev/myproject",
		SessionID: "session-bbb",
	}, nil)

	// Override sys mocks for this test.
	sys := new(testutil.MockSystem)
	s.srv.sys = sys
	sys.On("UserHomeDir").Return(tmpDir, nil)

	// Use real ReadDir and Open since we have real temp files.
	realEntries, err := os.ReadDir(projectDir)
	require.NoError(s.T(), err)
	sys.On("ReadDir", projectDir).Return(realEntries, nil)
	sys.On("Open", mock.Anything).Return(nil, os.ErrNotExist).Maybe()

	// Wrap the mock so Open delegates to os.Open (for real temp files).
	s.srv.sys = &realOpenSys{sys}

	// Mock ListChannels for imported_session_ids — include a thread with a session.
	s.store.On("ListChannels", mock.Anything).Return([]*db.Channel{
		{ChannelID: "thread-1", ParentID: "ch-1", SessionID: "session-bbb"},
	}, nil).Maybe()

	req := httptest.NewRequest("GET", "/api/channels/ch-1/sessions", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusOK, w.Code)

	var resp listSessionsResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(s.T(), "session-bbb", resp.CurrentSessionID)
	require.Len(s.T(), resp.Sessions, 3)

	// Newest first.
	require.Equal(s.T(), "session-ccc", resp.Sessions[0].SessionID)
	require.Equal(s.T(), "session-bbb", resp.Sessions[1].SessionID)
	require.Equal(s.T(), "session-aaa", resp.Sessions[2].SessionID)

	// Verify last_message extraction.
	require.Equal(s.T(), "Hello from Claude!", resp.Sessions[1].LastMessage) // session-bbb (assistant)
	require.Equal(s.T(), "What is Go?", resp.Sessions[2].LastMessage)        // session-aaa (user prompt)
	require.Empty(s.T(), resp.Sessions[0].LastMessage)                       // session-ccc (empty)

	// Verify imported_session_ids — session-bbb is already a thread.
	require.Equal(s.T(), []string{"session-bbb"}, resp.ImportedSessionIDs)
}

func (s *ServerSuite) TestFindLastMessage() {
	// Assistant text block is last.
	data := `{"type":"system","subtype":"init"}` + "\n" +
		`{"type":"user","message":{"role":"user","content":"Hello"}}` + "\n" +
		`{"type":"assistant","message":{"content":[{"type":"text","text":"Hi there!"}]}}` + "\n"
	require.Equal(s.T(), "Hi there!", findLastMessage([]byte(data)))

	// User prompt is last.
	data = `{"type":"assistant","message":{"content":[{"type":"text","text":"earlier"}]}}` + "\n" +
		`{"type":"user","message":{"role":"user","content":"What next?"}}` + "\n"
	require.Equal(s.T(), "What next?", findLastMessage([]byte(data)))

	// Tool result user line is skipped — falls through to assistant.
	data = `{"type":"assistant","message":{"content":[{"type":"text","text":"check this"}]}}` + "\n" +
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}}` + "\n"
	require.Equal(s.T(), "check this", findLastMessage([]byte(data)))

	// Long text is truncated.
	longText := strings.Repeat("x", 300)
	data = `{"type":"assistant","message":{"content":[{"type":"text","text":"` + longText + `"}]}}` + "\n"
	result := findLastMessage([]byte(data))
	require.Equal(s.T(), maxLastMessageLen+3, len(result))
	require.True(s.T(), strings.HasSuffix(result, "..."))

	// Empty input.
	require.Empty(s.T(), findLastMessage([]byte("")))

	// Only system events.
	require.Empty(s.T(), findLastMessage([]byte(`{"type":"system","subtype":"init"}`+"\n")))

	// Malformed JSON is skipped.
	data = `{"type":"assistant","message":{"content":[{"type":"text","text":"good"}]}}` + "\n" + "not json\n"
	require.Equal(s.T(), "good", findLastMessage([]byte(data)))

	// Invalid content array falls through.
	data = `{"type":"user","message":{"role":"user","content":"fallback"}}` + "\n" +
		`{"type":"assistant","message":{"content":"not an array"}}` + "\n"
	require.Equal(s.T(), "fallback", findLastMessage([]byte(data)))
}

func (s *ServerSuite) TestReadLastMessageTextFileNotFound() {
	require.Empty(s.T(), readLastMessageText(realSys{}, "/nonexistent/path.jsonl"))
}

func (s *ServerSuite) TestFindLastMessageFromReaderStatError() {
	require.Empty(s.T(), findLastMessageFromReader(&failStatReader{}, tailReadSize))
}

func (s *ServerSuite) TestFindLastMessageFromReaderSeekError() {
	// File "larger" than maxBytes triggers Seek.
	require.Empty(s.T(), findLastMessageFromReader(&failSeekReader{size: tailReadSize + 100}, tailReadSize))
}

func (s *ServerSuite) TestFindLastMessageFromReaderReadError() {
	require.Empty(s.T(), findLastMessageFromReader(&failReadReader{size: 10}, tailReadSize))
}

type failStatReader struct{}

func (f *failStatReader) Stat() (os.FileInfo, error)     { return nil, errors.New("stat err") }
func (f *failStatReader) Read([]byte) (int, error)       { return 0, nil }
func (f *failStatReader) Seek(int64, int) (int64, error) { return 0, nil }

type failSeekReader struct{ size int64 }

func (f *failSeekReader) Stat() (os.FileInfo, error)     { return &mockFileInfo{size: f.size}, nil }
func (f *failSeekReader) Read([]byte) (int, error)       { return 0, nil }
func (f *failSeekReader) Seek(int64, int) (int64, error) { return 0, errors.New("seek err") }

type failReadReader struct{ size int64 }

func (f *failReadReader) Stat() (os.FileInfo, error)     { return &mockFileInfo{size: f.size}, nil }
func (f *failReadReader) Read([]byte) (int, error)       { return 0, errors.New("read err") }
func (f *failReadReader) Seek(int64, int) (int64, error) { return 0, nil }

// realSys delegates Open to os.Open for testing readLastMessageText.
type realSys struct{}

func (realSys) Open(name string) (*os.File, error) { return os.Open(name) }

func (s *ServerSuite) TestSessionListEmptyDirPath() {
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{
		ChannelID: "ch-1",
		DirPath:   "",
		SessionID: "sess-1",
	}, nil)

	req := httptest.NewRequest("GET", "/api/channels/ch-1/sessions", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusOK, w.Code)

	var resp listSessionsResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(s.T(), "sess-1", resp.CurrentSessionID)
	require.Empty(s.T(), resp.Sessions)
}

func (s *ServerSuite) TestSessionListChannelNotFound() {
	s.store.On("GetChannel", mock.Anything, "unknown-ch").Return(nil, nil)

	req := httptest.NewRequest("GET", "/api/channels/unknown-ch/sessions", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusNotFound, w.Code)
}

func (s *ServerSuite) TestSessionListGetChannelError() {
	s.store.On("GetChannel", mock.Anything, "err-ch").Return(nil, errors.New("db error"))

	req := httptest.NewRequest("GET", "/api/channels/err-ch/sessions", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusInternalServerError, w.Code)
}

func (s *ServerSuite) TestSessionListNoProjectDir() {
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{
		ChannelID: "ch-1",
		DirPath:   "/Users/test/dev/myproject",
		SessionID: "sess-1",
	}, nil)

	tmpDir := s.T().TempDir()

	// Override sys mocks for this test.
	sys := new(testutil.MockSystem)
	s.srv.sys = sys
	sys.On("UserHomeDir").Return(tmpDir, nil)

	// ReadDir will fail because the directory doesn't exist.
	projectDir := filepath.Join(tmpDir, ".claude", "projects", "-Users-test-dev-myproject")
	sys.On("ReadDir", projectDir).Return(nil, os.ErrNotExist)

	req := httptest.NewRequest("GET", "/api/channels/ch-1/sessions", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusOK, w.Code)

	var resp listSessionsResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(s.T(), "sess-1", resp.CurrentSessionID)
	require.Empty(s.T(), resp.Sessions)
}

func (s *ServerSuite) TestSessionListStoreNotConfigured() {
	oldStore := s.srv.store
	defer func() { s.srv.store = oldStore }()
	s.srv.store = nil

	req := httptest.NewRequest("GET", "/api/channels/ch-1/sessions", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusNotImplemented, w.Code)
}

func (s *ServerSuite) TestSessionListHomeDirError() {
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{
		ChannelID: "ch-1",
		DirPath:   "/Users/test/dev/myproject",
		SessionID: "sess-1",
	}, nil)

	// Override sys mocks for this test.
	sys := new(testutil.MockSystem)
	s.srv.sys = sys
	sys.On("UserHomeDir").Return("", os.ErrNotExist)

	req := httptest.NewRequest("GET", "/api/channels/ch-1/sessions", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusInternalServerError, w.Code)
}

func (s *ServerSuite) TestSessionListEntryInfoError() {
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{
		ChannelID: "ch-1",
		DirPath:   "/Users/test/dev/myproject",
		SessionID: "sess-1",
	}, nil)

	tmpDir := s.T().TempDir()

	// Override sys mocks for this test.
	sys := new(testutil.MockSystem)
	s.srv.sys = sys
	sys.On("UserHomeDir").Return(tmpDir, nil)

	projectDir := filepath.Join(tmpDir, ".claude", "projects", "-Users-test-dev-myproject")
	// Return a mock DirEntry whose Info() returns an error.
	badEntry := &mockDirEntry{name: "bad.jsonl", isDir: false, infoErr: errors.New("stat failed")}
	goodEntry := &mockDirEntry{name: "good.jsonl", isDir: false, modTime: time.Now()}
	sys.On("ReadDir", projectDir).Return([]fs.DirEntry{badEntry, goodEntry}, nil)
	sys.On("Open", mock.Anything).Return(nil, os.ErrNotExist).Maybe()
	s.store.On("ListChannels", mock.Anything).Return([]*db.Channel{}, nil).Maybe()

	req := httptest.NewRequest("GET", "/api/channels/ch-1/sessions", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusOK, w.Code)

	var resp listSessionsResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	// Bad entry skipped, good entry included.
	require.Len(s.T(), resp.Sessions, 1)
	require.Equal(s.T(), "good", resp.Sessions[0].SessionID)
}

func (s *ServerSuite) TestSessionListWithRealJSONLFile() {
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{
		ChannelID: "ch-1",
		DirPath:   "/Users/test/dev/myproject",
		SessionID: "sess-1",
	}, nil)

	tmpDir := s.T().TempDir()
	projectDir := filepath.Join(tmpDir, ".claude", "projects", "-Users-test-dev-myproject")
	require.NoError(s.T(), os.MkdirAll(projectDir, 0o755))

	// Write a real JSONL file so Open succeeds and readLastMessageText can parse it.
	jsonl := `{"type":"assistant","message":{"content":[{"type":"text","text":"Hello world"}]}}` + "\n"
	require.NoError(s.T(), os.WriteFile(filepath.Join(projectDir, "sess-abc.jsonl"), []byte(jsonl), 0o644))

	sys := new(testutil.MockSystem)
	s.srv.sys = sys
	sys.On("UserHomeDir").Return(tmpDir, nil)

	realEntries, _ := os.ReadDir(projectDir)
	sys.On("ReadDir", projectDir).Return(realEntries, nil)
	// Open the file now so the mock returns a real *os.File (exercises MockSystem.Open success path line 58).
	jsonlPath := filepath.Join(projectDir, "sess-abc.jsonl")
	realFile, err := os.Open(jsonlPath)
	require.NoError(s.T(), err)
	s.T().Cleanup(func() { realFile.Close() })
	sys.On("Open", jsonlPath).Return(realFile, nil)
	sys.On("Open", mock.Anything).Return(nil, os.ErrNotExist).Maybe()
	s.store.On("ListChannels", mock.Anything).Return([]*db.Channel{}, nil).Maybe()

	req := httptest.NewRequest("GET", "/api/channels/ch-1/sessions", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusOK, w.Code)

	var resp listSessionsResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Len(s.T(), resp.Sessions, 1)
	require.Equal(s.T(), "sess-abc", resp.Sessions[0].SessionID)
	require.Equal(s.T(), "Hello world", resp.Sessions[0].LastMessage)
}

func (s *ServerSuite) TestSessionListEmptyDir() {
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{
		ChannelID: "ch-1",
		DirPath:   "/Users/test/dev/myproject",
		SessionID: "",
	}, nil)

	tmpDir := s.T().TempDir()

	// Override sys mocks for this test.
	sys := new(testutil.MockSystem)
	s.srv.sys = sys
	sys.On("UserHomeDir").Return(tmpDir, nil)

	projectDir := filepath.Join(tmpDir, ".claude", "projects", "-Users-test-dev-myproject")
	sys.On("ReadDir", projectDir).Return([]fs.DirEntry{}, nil)
	s.store.On("ListChannels", mock.Anything).Return([]*db.Channel{}, nil).Maybe()

	req := httptest.NewRequest("GET", "/api/channels/ch-1/sessions", nil)
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusOK, w.Code)

	var resp listSessionsResponse
	require.NoError(s.T(), json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(s.T(), "", resp.CurrentSessionID)
	require.Empty(s.T(), resp.Sessions)
}
