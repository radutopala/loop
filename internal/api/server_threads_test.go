package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
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

// --- CreateThread tests ---

func (s *ServerSuite) TestCreateThreadSuccess() {
	s.threads.On("CreateThread", mock.Anything, "ch-1", "my-thread", "", "").
		Return("thread-1", nil)

	rec := s.testRequest("POST", "/api/threads", `{"channel_id":"ch-1","name":"my-thread"}`)

	require.Equal(s.T(), http.StatusCreated, rec.Code)

	var resp createThreadResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(s.T(), "thread-1", resp.ThreadID)
	s.threads.AssertExpectations(s.T())
}

func (s *ServerSuite) TestCreateThreadSuccessWithAuthorID() {
	s.threads.On("CreateThread", mock.Anything, "ch-1", "my-thread", "user-42", "").
		Return("thread-1", nil)

	rec := s.testRequest("POST", "/api/threads", `{"channel_id":"ch-1","name":"my-thread","author_id":"user-42"}`)

	require.Equal(s.T(), http.StatusCreated, rec.Code)

	var resp createThreadResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(s.T(), "thread-1", resp.ThreadID)
	s.threads.AssertExpectations(s.T())
}

func (s *ServerSuite) TestCreateThreadSuccessWithMessage() {
	s.threads.On("CreateThread", mock.Anything, "ch-1", "my-thread", "", "Do the task").
		Return("thread-1", nil)

	rec := s.testRequest("POST", "/api/threads", `{"channel_id":"ch-1","name":"my-thread","message":"Do the task"}`)

	require.Equal(s.T(), http.StatusCreated, rec.Code)

	var resp createThreadResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(s.T(), "thread-1", resp.ThreadID)
	s.threads.AssertExpectations(s.T())
}

func (s *ServerSuite) TestCreateThreadLocalAutoTrigger() {
	// Message is NOT passed to CreateThread when msgHandler is set — it goes through HandleThreadCreated instead.
	s.threads.On("CreateThread", mock.Anything, "ch-1", "my-thread", "user-42", "").
		Return("thread-1", nil)

	handler := new(MockIncomingMessageHandler)
	s.srv.SetIncomingMessageHandler(handler)
	defer func() { s.srv.msgHandler = nil }()

	// Set eventsHub so BroadcastChannelCreated is exercised.
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	hub := NewEventsHub(logger)
	s.srv.SetEventsHub(hub)
	defer func() { s.srv.eventsHub = nil }()

	called := make(chan struct{}, 1)
	handler.On("HandleThreadCreated", mock.Anything, "thread-1", "user-42", "Do the task").
		Run(func(_ mock.Arguments) { called <- struct{}{} }).Return()

	rec := s.testRequest("POST", "/api/threads", `{"channel_id":"ch-1","name":"my-thread","author_id":"user-42","message":"Do the task"}`)

	require.Equal(s.T(), http.StatusCreated, rec.Code)

	var resp createThreadResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(s.T(), "thread-1", resp.ThreadID)

	select {
	case <-called:
	case <-time.After(time.Second):
		s.T().Fatal("HandleThreadCreated was not called within 1s")
	}

	handler.AssertExpectations(s.T())
	s.threads.AssertExpectations(s.T())
}

func (s *ServerSuite) TestCreateThreadLocalAutoTriggerDefaultAuthor() {
	s.threads.On("CreateThread", mock.Anything, "ch-1", "my-thread", "", "").
		Return("thread-1", nil)

	handler := new(MockIncomingMessageHandler)
	s.srv.SetIncomingMessageHandler(handler)
	defer func() { s.srv.msgHandler = nil }()

	called := make(chan struct{}, 1)
	handler.On("HandleThreadCreated", mock.Anything, "thread-1", "", "Do the task").
		Run(func(_ mock.Arguments) { called <- struct{}{} }).Return()

	rec := s.testRequest("POST", "/api/threads", `{"channel_id":"ch-1","name":"my-thread","message":"Do the task"}`)

	require.Equal(s.T(), http.StatusCreated, rec.Code)

	select {
	case <-called:
	case <-time.After(time.Second):
		s.T().Fatal("HandleThreadCreated was not called within 1s")
	}

	handler.AssertExpectations(s.T())
}

func (s *ServerSuite) TestCreateThreadLocalNoTriggerWithoutMessage() {
	s.threads.On("CreateThread", mock.Anything, "ch-1", "my-thread", "", "").
		Return("thread-1", nil)

	handler := new(MockIncomingMessageHandler)
	s.srv.SetIncomingMessageHandler(handler)
	defer func() { s.srv.msgHandler = nil }()

	rec := s.testRequest("POST", "/api/threads", `{"channel_id":"ch-1","name":"my-thread"}`)

	require.Equal(s.T(), http.StatusCreated, rec.Code)

	// No message means no auto-trigger.
	handler.AssertNotCalled(s.T(), "HandleThreadCreated", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func (s *ServerSuite) TestCreateThreadMissingFields() {
	tests := []struct {
		name string
		body string
	}{
		{"MissingChannelID", `{"name":"my-thread"}`},
		{"MissingName", `{"channel_id":"ch-1"}`},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			rec := s.testRequest("POST", "/api/threads", tt.body)
			require.Equal(s.T(), http.StatusBadRequest, rec.Code)
		})
	}
}

func (s *ServerSuite) TestCreateThreadError() {
	s.threads.On("CreateThread", mock.Anything, "ch-1", "my-thread", "", "").
		Return("", errors.New("create failed"))

	rec := s.testRequest("POST", "/api/threads", `{"channel_id":"ch-1","name":"my-thread"}`)

	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	s.threads.AssertExpectations(s.T())
}

func (s *ServerSuite) TestCreateThreadWithSessionID() {
	s.threads.On("CreateThread", mock.Anything, "ch-1", "my-thread", "", "").
		Return("thread-1", nil)
	s.store.On("UpdateSessionID", mock.Anything, "thread-1", "imported-session-42").Return(nil)
	// importSessionMessages looks up parent channel — return nil to skip import in this test.
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(nil, nil).Maybe()

	rec := s.testRequest("POST", "/api/threads", `{"channel_id":"ch-1","name":"my-thread","session_id":"imported-session-42"}`)

	require.Equal(s.T(), http.StatusCreated, rec.Code)

	var resp createThreadResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(s.T(), "thread-1", resp.ThreadID)
	s.threads.AssertExpectations(s.T())
	s.store.AssertCalled(s.T(), "UpdateSessionID", mock.Anything, "thread-1", "imported-session-42")
}

func (s *ServerSuite) TestCreateThreadWithSessionIDNoStore() {
	threads := new(MockThreadEnsurer)
	threads.On("CreateThread", mock.Anything, "ch-1", "my-thread", "", "").
		Return("thread-1", nil)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := NewServer(nil, nil, threads, nil, nil, logger)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/threads", srv.handleCreateThread)

	req := httptest.NewRequest("POST", "/api/threads", bytes.NewBufferString(`{"channel_id":"ch-1","name":"my-thread","session_id":"sess-1"}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	// session_id is silently ignored when store is nil.
	require.Equal(s.T(), http.StatusCreated, rec.Code)
	threads.AssertExpectations(s.T())
}

func (s *ServerSuite) TestCreateThreadWithEmptySessionID() {
	s.threads.On("CreateThread", mock.Anything, "ch-1", "my-thread", "", "").
		Return("thread-1", nil)

	rec := s.testRequest("POST", "/api/threads", `{"channel_id":"ch-1","name":"my-thread","session_id":""}`)

	require.Equal(s.T(), http.StatusCreated, rec.Code)

	var resp createThreadResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(s.T(), "thread-1", resp.ThreadID)
	s.threads.AssertExpectations(s.T())
	// UpdateSessionID should NOT be called when session_id is empty.
	s.store.AssertNotCalled(s.T(), "UpdateSessionID", mock.Anything, mock.Anything, mock.Anything)
}

func (s *ServerSuite) TestCreateThreadWithSessionIDUpdateError() {
	s.threads.On("CreateThread", mock.Anything, "ch-1", "test", "desktop", "").Return("thread-1", nil)
	s.store.On("UpdateSessionID", mock.Anything, "thread-1", "bad-session").Return(errors.New("db error"))

	body := `{"channel_id":"ch-1","name":"test","author_id":"desktop","session_id":"bad-session"}`
	req := httptest.NewRequest("POST", "/api/threads", strings.NewReader(body))
	w := httptest.NewRecorder()
	s.mux.ServeHTTP(w, req)

	require.Equal(s.T(), http.StatusInternalServerError, w.Code)
}

func (s *ServerSuite) TestCreateThreadWithTraversalSessionID() {
	s.threads.On("CreateThread", mock.Anything, "ch-1", "test", "", "").
		Return("thread-1", nil)
	// filepath.Base strips traversal: "../../etc/passwd" → "passwd".
	s.store.On("UpdateSessionID", mock.Anything, "thread-1", "passwd").Return(nil)
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(nil, nil).Maybe()

	body := `{"channel_id":"ch-1","name":"test","session_id":"../../etc/passwd"}`
	rec := s.testRequest("POST", "/api/threads", body)

	require.Equal(s.T(), http.StatusCreated, rec.Code)
	// Stored value must be the sanitised base name, not the original traversal path.
	s.store.AssertCalled(s.T(), "UpdateSessionID", mock.Anything, "thread-1", "passwd")
}

// --- importSessionMessages tests ---

func (s *ServerSuite) TestImportSessionMessagesTraversalSessionID() {
	store := new(MockChannelLister)
	// filepath.Base strips traversal — "../../etc/passwd" → "passwd".
	// GetChannel is called with the sanitised value; parent lookup returns nil to stop the flow.
	store.On("GetChannel", mock.Anything, "ch-parent").Return(nil, nil)

	srv := NewServer(nil, nil, nil, store, nil, testLogger())
	srv.sys = s.sys
	srv.importSessionMessages(context.Background(), "ch-parent", "thread-1", "../../etc/passwd")
	store.AssertExpectations(s.T())
}

func (s *ServerSuite) TestImportSessionMessagesDotDotSessionID() {
	store := new(MockChannelLister)
	// A bare ".." sessionID should be rejected entirely — no store calls.
	srv := NewServer(nil, nil, nil, store, nil, testLogger())
	srv.sys = s.sys
	srv.importSessionMessages(context.Background(), "ch-parent", "thread-1", "..")
	store.AssertNotCalled(s.T(), "GetChannel")
}

func (s *ServerSuite) TestImportSessionMessagesNilStoreOrSys() {
	// When store is nil, importSessionMessages returns early.
	srv := NewServer(nil, nil, nil, nil, nil, testLogger())
	srv.importSessionMessages(context.Background(), "ch-1", "thread-1", "sess-1")
	// No panic = success.

	// When sys is nil, importSessionMessages returns early.
	store := new(MockChannelLister)
	srv2 := NewServer(nil, nil, nil, store, nil, testLogger())
	srv2.sys = nil
	srv2.importSessionMessages(context.Background(), "ch-1", "thread-1", "sess-1")
	// store.GetChannel should not be called when sys is nil.
	store.AssertNotCalled(s.T(), "GetChannel")
}

func (s *ServerSuite) TestImportSessionMessagesParentChannelNotFound() {
	store := new(MockChannelLister)
	store.On("GetChannel", mock.Anything, "ch-parent").Return(nil, nil)

	srv := NewServer(nil, nil, nil, store, nil, testLogger())
	srv.sys = s.sys
	srv.importSessionMessages(context.Background(), "ch-parent", "thread-1", "sess-1")
	store.AssertExpectations(s.T())
}

func (s *ServerSuite) TestImportSessionMessagesParentEmptyDirPath() {
	store := new(MockChannelLister)
	store.On("GetChannel", mock.Anything, "ch-parent").Return(&db.Channel{ChannelID: "ch-parent", DirPath: ""}, nil)

	srv := NewServer(nil, nil, nil, store, nil, testLogger())
	srv.sys = s.sys
	srv.importSessionMessages(context.Background(), "ch-parent", "thread-1", "sess-1")
	store.AssertExpectations(s.T())
}

func (s *ServerSuite) TestImportSessionMessagesParentGetError() {
	store := new(MockChannelLister)
	store.On("GetChannel", mock.Anything, "ch-parent").Return(nil, errors.New("db err"))

	srv := NewServer(nil, nil, nil, store, nil, testLogger())
	srv.sys = s.sys
	srv.importSessionMessages(context.Background(), "ch-parent", "thread-1", "sess-1")
	store.AssertExpectations(s.T())
}

func (s *ServerSuite) TestImportSessionMessagesThreadNotFound() {
	store := new(MockChannelLister)
	store.On("GetChannel", mock.Anything, "ch-parent").Return(&db.Channel{ChannelID: "ch-parent", DirPath: "/Users/test/dev/proj"}, nil)
	store.On("GetChannel", mock.Anything, "thread-1").Return(nil, nil)

	srv := NewServer(nil, nil, nil, store, nil, testLogger())
	srv.sys = s.sys
	srv.importSessionMessages(context.Background(), "ch-parent", "thread-1", "sess-1")
	store.AssertExpectations(s.T())
}

func (s *ServerSuite) TestImportSessionMessagesThreadGetError() {
	store := new(MockChannelLister)
	store.On("GetChannel", mock.Anything, "ch-parent").Return(&db.Channel{ChannelID: "ch-parent", DirPath: "/Users/test/dev/proj"}, nil)
	store.On("GetChannel", mock.Anything, "thread-1").Return(nil, errors.New("db err"))

	srv := NewServer(nil, nil, nil, store, nil, testLogger())
	srv.sys = s.sys
	srv.importSessionMessages(context.Background(), "ch-parent", "thread-1", "sess-1")
	store.AssertExpectations(s.T())
}

func (s *ServerSuite) TestImportSessionMessagesUserHomeDirError() {
	store := new(MockChannelLister)
	store.On("GetChannel", mock.Anything, "ch-parent").Return(&db.Channel{ChannelID: "ch-parent", DirPath: "/Users/test/dev/proj"}, nil)
	store.On("GetChannel", mock.Anything, "thread-1").Return(&db.Channel{ChannelID: "thread-1", ID: 42}, nil)

	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("", errors.New("no home"))

	srv := NewServer(nil, nil, nil, store, nil, testLogger())
	srv.sys = sys
	srv.importSessionMessages(context.Background(), "ch-parent", "thread-1", "sess-1")
	store.AssertExpectations(s.T())
}

func (s *ServerSuite) TestImportSessionMessagesFileNotFound() {
	store := new(MockChannelLister)
	store.On("GetChannel", mock.Anything, "ch-parent").Return(&db.Channel{ChannelID: "ch-parent", DirPath: "/Users/test/dev/proj"}, nil)
	store.On("GetChannel", mock.Anything, "thread-1").Return(&db.Channel{ChannelID: "thread-1", ID: 42}, nil)

	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return("/home/testuser", nil)
	sys.On("Open", mock.Anything).Return(nil, os.ErrNotExist)

	srv := NewServer(nil, nil, nil, store, nil, testLogger())
	srv.sys = sys
	srv.importSessionMessages(context.Background(), "ch-parent", "thread-1", "sess-1")
	store.AssertExpectations(s.T())
}

func (s *ServerSuite) TestImportSessionMessagesSuccess() {
	tmpDir := s.T().TempDir()
	encodedPath := "-Users-test-dev-proj" // EncodeClaudeProjectPath for /Users/test/dev/proj
	projectDir := filepath.Join(tmpDir, ".claude", "projects", encodedPath)
	require.NoError(s.T(), os.MkdirAll(projectDir, 0755))

	// Build a JSONL file with various line types.
	lines := []string{
		`{"type":"system","subtype":"init"}`,
		`{"type":"user","message":{"role":"user","content":"Hello, world!"}}`,
		`{"type":"assistant","timestamp":"2025-06-15T10:30:00Z","message":{"content":[{"type":"text","text":"Hi there!"}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}}`,
		`not valid json`,
		`{"type":"result","subtype":"success"}`,
		`{"type":"user","message":{"role":"user","content":"Second question"}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":""}]}}`,
		``,
	}
	jsonlPath := filepath.Join(projectDir, "sess-import.jsonl")
	require.NoError(s.T(), os.WriteFile(jsonlPath, []byte(strings.Join(lines, "\n")), 0644))

	store := new(MockChannelLister)
	store.On("GetChannel", mock.Anything, "ch-parent").Return(&db.Channel{ChannelID: "ch-parent", DirPath: "/Users/test/dev/proj"}, nil)
	store.On("GetChannel", mock.Anything, "thread-1").Return(&db.Channel{ChannelID: "thread-1", ID: 42}, nil)
	store.On("InsertMessage", mock.Anything, mock.MatchedBy(func(msg *db.Message) bool {
		return msg.ChatID == 42 && msg.ChannelID == "thread-1"
	})).Return(nil)

	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return(tmpDir, nil)

	srv := NewServer(nil, nil, nil, store, nil, testLogger())
	srv.sys = &realOpenSys{sys}

	srv.importSessionMessages(context.Background(), "ch-parent", "thread-1", "sess-import")

	// Expected messages: "Hello, world!" (user), "Hi there!" (assistant), "Second question" (user).
	// tool_result user line, invalid json, result type, empty assistant text are all skipped.
	store.AssertNumberOfCalls(s.T(), "InsertMessage", 3)

	// Verify message details from the calls.
	calls := store.Calls
	var insertedMsgs []*db.Message
	for _, c := range calls {
		if c.Method == "InsertMessage" {
			insertedMsgs = append(insertedMsgs, c.Arguments.Get(1).(*db.Message))
		}
	}
	require.Len(s.T(), insertedMsgs, 3)

	// First message: user "Hello, world!"
	require.Equal(s.T(), "Hello, world!", insertedMsgs[0].Content)
	require.Equal(s.T(), "user", insertedMsgs[0].AuthorName)
	require.False(s.T(), insertedMsgs[0].IsBot)

	// Second message: assistant "Hi there!" with timestamp
	require.Equal(s.T(), "Hi there!", insertedMsgs[1].Content)
	require.Equal(s.T(), "agent", insertedMsgs[1].AuthorName)
	require.True(s.T(), insertedMsgs[1].IsBot)
	require.Equal(s.T(), time.Date(2025, 6, 15, 10, 30, 0, 0, time.UTC), insertedMsgs[1].CreatedAt)

	// Third message: user "Second question"
	require.Equal(s.T(), "Second question", insertedMsgs[2].Content)
	require.Equal(s.T(), "user", insertedMsgs[2].AuthorName)
	require.False(s.T(), insertedMsgs[2].IsBot)
}

func (s *ServerSuite) TestImportSessionMessagesReadAllError() {
	tmpDir := s.T().TempDir()
	encodedPath := "-Users-test-dev-proj"
	projectDir := filepath.Join(tmpDir, ".claude", "projects", encodedPath)
	require.NoError(s.T(), os.MkdirAll(projectDir, 0755))

	// Create a directory at the JSONL path so Open succeeds but ReadAll fails.
	jsonlDir := filepath.Join(projectDir, "sess-dir.jsonl")
	require.NoError(s.T(), os.MkdirAll(jsonlDir, 0755))

	store := new(MockChannelLister)
	store.On("GetChannel", mock.Anything, "ch-parent").Return(&db.Channel{ChannelID: "ch-parent", DirPath: "/Users/test/dev/proj"}, nil)
	store.On("GetChannel", mock.Anything, "thread-1").Return(&db.Channel{ChannelID: "thread-1", ID: 10}, nil)

	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return(tmpDir, nil)

	srv := NewServer(nil, nil, nil, store, nil, testLogger())
	srv.sys = &realOpenSys{sys}

	// Should return early because ReadAll fails on a directory fd.
	srv.importSessionMessages(context.Background(), "ch-parent", "thread-1", "sess-dir")
	store.AssertNotCalled(s.T(), "InsertMessage")
}

func (s *ServerSuite) TestImportSessionMessagesInsertError() {
	tmpDir := s.T().TempDir()
	encodedPath := "-Users-test-dev-proj"
	projectDir := filepath.Join(tmpDir, ".claude", "projects", encodedPath)
	require.NoError(s.T(), os.MkdirAll(projectDir, 0755))

	lines := []string{
		`{"type":"user","message":{"role":"user","content":"First"}}`,
		`{"type":"user","message":{"role":"user","content":"Second"}}`,
	}
	jsonlPath := filepath.Join(projectDir, "sess-fail.jsonl")
	require.NoError(s.T(), os.WriteFile(jsonlPath, []byte(strings.Join(lines, "\n")), 0644))

	store := new(MockChannelLister)
	store.On("GetChannel", mock.Anything, "ch-parent").Return(&db.Channel{ChannelID: "ch-parent", DirPath: "/Users/test/dev/proj"}, nil)
	store.On("GetChannel", mock.Anything, "thread-1").Return(&db.Channel{ChannelID: "thread-1", ID: 10}, nil)
	// First insert succeeds, second fails — should abort.
	store.On("InsertMessage", mock.Anything, mock.MatchedBy(func(msg *db.Message) bool {
		return msg.Content == "First"
	})).Return(nil).Once()
	store.On("InsertMessage", mock.Anything, mock.MatchedBy(func(msg *db.Message) bool {
		return msg.Content == "Second"
	})).Return(errors.New("insert failed")).Once()

	sys := new(testutil.MockSystem)
	sys.On("UserHomeDir").Return(tmpDir, nil)

	srv := NewServer(nil, nil, nil, store, nil, testLogger())
	srv.sys = &realOpenSys{sys}

	srv.importSessionMessages(context.Background(), "ch-parent", "thread-1", "sess-fail")
	// "Second" insert fails — only 2 InsertMessage calls total.
	store.AssertNumberOfCalls(s.T(), "InsertMessage", 2)
}
