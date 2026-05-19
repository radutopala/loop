package api

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/db"
)

// --- SendMessage tests ---

func (s *ServerSuite) TestSendMessageSuccess() {
	s.messages.On("PostMessage", mock.Anything, "ch-1", "hello world").Return(nil)

	rec := s.testRequest("POST", "/api/messages", `{"channel_id":"ch-1","content":"hello world"}`)

	require.Equal(s.T(), http.StatusNoContent, rec.Code)
	s.messages.AssertExpectations(s.T())
}

func (s *ServerSuite) TestSendMessageMissingFields() {
	tests := []struct {
		name string
		body string
	}{
		{"MissingChannelID", `{"content":"hello"}`},
		{"MissingContent", `{"channel_id":"ch-1"}`},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			rec := s.testRequest("POST", "/api/messages", tt.body)
			require.Equal(s.T(), http.StatusBadRequest, rec.Code)
		})
	}
}

func (s *ServerSuite) TestSendMessageError() {
	s.messages.On("PostMessage", mock.Anything, "ch-1", "hello").Return(errors.New("send failed"))

	rec := s.testRequest("POST", "/api/messages", `{"channel_id":"ch-1","content":"hello"}`)

	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	s.messages.AssertExpectations(s.T())
}

func (s *ServerSuite) TestSendMessageViaHandler() {
	handler := new(MockIncomingMessageHandler)
	s.srv.SetIncomingMessageHandler(handler)

	called := make(chan struct{}, 1)
	handler.On("HandleIncomingMessage", mock.Anything, "ch-1", "", "hello world", "").
		Run(func(_ mock.Arguments) { called <- struct{}{} }).Return()

	rec := s.testRequest("POST", "/api/messages", `{"channel_id":"ch-1","content":"hello world"}`)

	require.Equal(s.T(), http.StatusNoContent, rec.Code)

	// Wait for the goroutine to invoke the handler.
	select {
	case <-called:
	case <-time.After(time.Second):
		s.T().Fatal("HandleIncomingMessage was not called within 1s")
	}

	handler.AssertExpectations(s.T())
	// PostMessage must NOT be called when the handler is set.
	s.messages.AssertNotCalled(s.T(), "PostMessage")
}

func (s *ServerSuite) TestSendMessageViaHandlerPlanMode() {
	handler := new(MockIncomingMessageHandler)
	s.srv.SetIncomingMessageHandler(handler)

	called := make(chan struct{}, 1)
	handler.On("HandleIncomingMessage", mock.Anything, "ch-1", "", "plan this", "plan").
		Run(func(_ mock.Arguments) { called <- struct{}{} }).Return()

	rec := s.testRequest("POST", "/api/messages", `{"channel_id":"ch-1","content":"plan this","mode":"plan"}`)

	require.Equal(s.T(), http.StatusNoContent, rec.Code)

	select {
	case <-called:
	case <-time.After(time.Second):
		s.T().Fatal("HandleIncomingMessage was not called within 1s")
	}

	handler.AssertExpectations(s.T())
}

func (s *ServerSuite) TestSendMessageInterrupt() {
	handler := new(MockIncomingMessageHandler)
	canceller := new(MockRunCanceller)
	s.srv.SetIncomingMessageHandler(handler)
	s.srv.SetRunCanceller(canceller)

	// Record call order: the priority-bumped insert MUST land before
	// CancelActiveRun, otherwise the cancelled run's drain loop can re-claim
	// an older queued row in the window between cancel and insert.
	var order []string
	var orderMu sync.Mutex
	record := func(name string) {
		orderMu.Lock()
		defer orderMu.Unlock()
		order = append(order, name)
	}

	canceller.On("CancelActiveRun", "ch-1").Run(func(_ mock.Arguments) { record("cancel") }).Return(true)
	s.store.On("MaxQueuedPriority", mock.Anything, "ch-1").Return(2, nil)
	called := make(chan struct{}, 1)
	handler.On("HandleIncomingMessageWithPriority", mock.Anything, "ch-1", "", "stop and go", "", 3).
		Run(func(_ mock.Arguments) {
			record("insert")
			called <- struct{}{}
		}).Return()

	rec := s.testRequest("POST", "/api/messages", `{"channel_id":"ch-1","content":"stop and go","interrupt":true}`)

	require.Equal(s.T(), http.StatusNoContent, rec.Code)

	select {
	case <-called:
	case <-time.After(time.Second):
		s.T().Fatal("HandleIncomingMessageWithPriority was not called within 1s")
	}

	canceller.AssertExpectations(s.T())
	handler.AssertExpectations(s.T())

	orderMu.Lock()
	defer orderMu.Unlock()
	require.Equal(s.T(), []string{"insert", "cancel"}, order,
		"insert must happen before cancel to avoid the cancel-and-reclaim race")
}

func (s *ServerSuite) TestSendMessageInterruptMaxQueuedPriorityError() {
	handler := new(MockIncomingMessageHandler)
	canceller := new(MockRunCanceller)
	s.srv.SetIncomingMessageHandler(handler)
	s.srv.SetRunCanceller(canceller)

	canceller.On("CancelActiveRun", "ch-1").Return(true)
	s.store.On("MaxQueuedPriority", mock.Anything, "ch-1").Return(0, errors.New("db error"))
	called := make(chan struct{}, 1)
	// On error, priority falls back to 0.
	handler.On("HandleIncomingMessageWithPriority", mock.Anything, "ch-1", "", "go now", "", 0).
		Run(func(_ mock.Arguments) { called <- struct{}{} }).Return()

	rec := s.testRequest("POST", "/api/messages", `{"channel_id":"ch-1","content":"go now","interrupt":true}`)

	require.Equal(s.T(), http.StatusNoContent, rec.Code)

	select {
	case <-called:
	case <-time.After(time.Second):
		s.T().Fatal("HandleIncomingMessageWithPriority was not called within 1s")
	}

	canceller.AssertExpectations(s.T())
	handler.AssertExpectations(s.T())
}

func (s *ServerSuite) TestSendMessageInterruptNoCanceller() {
	handler := new(MockIncomingMessageHandler)
	s.srv.SetIncomingMessageHandler(handler)
	// runCanceller is nil — should not panic.

	called := make(chan struct{}, 1)
	handler.On("HandleIncomingMessage", mock.Anything, "ch-1", "", "hello", "").
		Run(func(_ mock.Arguments) { called <- struct{}{} }).Return()

	rec := s.testRequest("POST", "/api/messages", `{"channel_id":"ch-1","content":"hello","interrupt":true}`)

	require.Equal(s.T(), http.StatusNoContent, rec.Code)

	select {
	case <-called:
	case <-time.After(time.Second):
		s.T().Fatal("HandleIncomingMessage was not called within 1s")
	}

	handler.AssertExpectations(s.T())
}

func (s *ServerSuite) TestSendMessageNoInterrupt() {
	handler := new(MockIncomingMessageHandler)
	canceller := new(MockRunCanceller)
	s.srv.SetIncomingMessageHandler(handler)
	s.srv.SetRunCanceller(canceller)

	called := make(chan struct{}, 1)
	handler.On("HandleIncomingMessage", mock.Anything, "ch-1", "", "normal msg", "").
		Run(func(_ mock.Arguments) { called <- struct{}{} }).Return()

	rec := s.testRequest("POST", "/api/messages", `{"channel_id":"ch-1","content":"normal msg"}`)

	require.Equal(s.T(), http.StatusNoContent, rec.Code)

	select {
	case <-called:
	case <-time.After(time.Second):
		s.T().Fatal("HandleIncomingMessage was not called within 1s")
	}

	handler.AssertExpectations(s.T())
	// CancelActiveRun must NOT be called when interrupt is false/absent.
	canceller.AssertNotCalled(s.T(), "CancelActiveRun")
}

func (s *ServerSuite) TestDeleteQueuedMessageSuccess() {
	s.store.On("DeleteQueuedMessage", mock.Anything, "ch-1", "msg-queued").Return(true, nil)

	hub := NewEventsHub(slog.Default())
	s.srv.SetEventsHub(hub)
	defer func() { s.srv.eventsHub = nil }()

	rec := s.testRequest("DELETE", "/api/messages/msg-queued?channel_id=ch-1", "")

	require.Equal(s.T(), http.StatusNoContent, rec.Code)
	s.store.AssertExpectations(s.T())
}

func (s *ServerSuite) TestDeleteQueuedMessageMissingChannelID() {
	rec := s.testRequest("DELETE", "/api/messages/msg-queued", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestDeleteQueuedMessageNotFound() {
	s.store.On("DeleteQueuedMessage", mock.Anything, "ch-1", "missing").Return(false, nil)

	rec := s.testRequest("DELETE", "/api/messages/missing?channel_id=ch-1", "")
	require.Equal(s.T(), http.StatusNotFound, rec.Code)
	s.store.AssertExpectations(s.T())
}

func (s *ServerSuite) TestDeleteQueuedMessageError() {
	s.store.On("DeleteQueuedMessage", mock.Anything, "ch-1", "msg-1").Return(false, errors.New("boom"))

	rec := s.testRequest("DELETE", "/api/messages/msg-1?channel_id=ch-1", "")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	s.store.AssertExpectations(s.T())
}

func (s *ServerSuite) TestSetIncomingMessageHandler() {
	require.Nil(s.T(), s.srv.msgHandler)

	handler := new(MockIncomingMessageHandler)
	s.srv.SetIncomingMessageHandler(handler)

	require.NotNil(s.T(), s.srv.msgHandler)
	require.Equal(s.T(), handler, s.srv.msgHandler)
}

// --- ListQueuedMessages tests ---

func (s *ServerSuite) TestListQueuedMessagesSuccess() {
	now := time.Now().UTC()
	msgs := []*db.Message{
		{ID: 12, ChannelID: "ch-1", MsgID: "m12", AuthorID: "u1", AuthorName: "alice", Content: "bumped", Priority: 1, CreatedAt: now},
		{ID: 10, ChannelID: "ch-1", MsgID: "m10", AuthorID: "u1", AuthorName: "alice", Content: "first", CreatedAt: now},
	}
	s.store.On("ListQueuedUserMessages", mock.Anything, "ch-1").Return(msgs, nil)

	rec := s.testRequest("GET", "/api/channels/ch-1/queued", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp queuedMessagesResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(s.T(), resp.Messages, 2)
	require.Equal(s.T(), "m12", resp.Messages[0].MsgID)
	require.Equal(s.T(), 1, resp.Messages[0].Priority)
	require.Equal(s.T(), "m10", resp.Messages[1].MsgID)
}

func (s *ServerSuite) TestListQueuedMessagesEmpty() {
	s.store.On("ListQueuedUserMessages", mock.Anything, "ch-1").Return([]*db.Message{}, nil)

	rec := s.testRequest("GET", "/api/channels/ch-1/queued", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp queuedMessagesResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Empty(s.T(), resp.Messages)
}

func (s *ServerSuite) TestListQueuedMessagesError() {
	s.store.On("ListQueuedUserMessages", mock.Anything, "ch-1").Return(nil, errors.New("db error"))

	rec := s.testRequest("GET", "/api/channels/ch-1/queued", "")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
}

func (s *ServerSuite) TestListQueuedMessagesNotConfigured() {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := NewServer(nil, nil, nil, nil, nil, logger)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/channels/{id}/queued", srv.handleListQueuedMessages)
	req := httptest.NewRequest("GET", "/api/channels/ch-1/queued", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

// --- ListMessages tests ---

func (s *ServerSuite) TestListMessagesSuccess() {
	now := time.Now().UTC()
	msgs := []*db.Message{
		{ID: 10, ChannelID: "ch-1", MsgID: "m10", AuthorID: "u1", AuthorName: "alice", Content: "hello", IsBot: false, CreatedAt: now},
		{ID: 9, ChannelID: "ch-1", MsgID: "m9", AuthorID: "bot", AuthorName: "Bot", Content: "hi", IsBot: true, CreatedAt: now},
	}
	s.store.On("GetMessagesCursor", mock.Anything, "ch-1", int64(0), 51).Return(msgs, nil)

	rec := s.testRequest("GET", "/api/channels/ch-1/messages", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp messagesListResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(s.T(), resp.Messages, 2)
	require.Equal(s.T(), int64(10), resp.Messages[0].ID)
	require.Equal(s.T(), "hello", resp.Messages[0].Content)
	require.False(s.T(), resp.Messages[0].IsBot)
	require.True(s.T(), resp.Messages[1].IsBot)
	require.Nil(s.T(), resp.NextCursor)
}

func (s *ServerSuite) TestListMessagesWithCursor() {
	now := time.Now().UTC()
	msgs := []*db.Message{
		{ID: 5, ChannelID: "ch-1", MsgID: "m5", AuthorID: "u1", AuthorName: "alice", Content: "five", CreatedAt: now},
	}
	s.store.On("GetMessagesCursor", mock.Anything, "ch-1", int64(10), 51).Return(msgs, nil)

	rec := s.testRequest("GET", "/api/channels/ch-1/messages?cursor=10", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp messagesListResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(s.T(), resp.Messages, 1)
	require.Nil(s.T(), resp.NextCursor)
}

func (s *ServerSuite) TestListMessagesWithLimit() {
	now := time.Now().UTC()
	// Return limit+1 messages to trigger pagination
	msgs := []*db.Message{
		{ID: 3, ChannelID: "ch-1", MsgID: "m3", AuthorID: "u1", AuthorName: "alice", Content: "three", CreatedAt: now},
		{ID: 2, ChannelID: "ch-1", MsgID: "m2", AuthorID: "u1", AuthorName: "alice", Content: "two", CreatedAt: now},
		{ID: 1, ChannelID: "ch-1", MsgID: "m1", AuthorID: "u1", AuthorName: "alice", Content: "one", CreatedAt: now},
	}
	s.store.On("GetMessagesCursor", mock.Anything, "ch-1", int64(0), 3).Return(msgs, nil)

	rec := s.testRequest("GET", "/api/channels/ch-1/messages?limit=2", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp messagesListResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(s.T(), resp.Messages, 2)
	require.NotNil(s.T(), resp.NextCursor)
	require.Equal(s.T(), int64(2), *resp.NextCursor)
}

func (s *ServerSuite) TestListMessagesInvalidLimit() {
	rec := s.testRequest("GET", "/api/channels/ch-1/messages?limit=abc", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)

	rec = s.testRequest("GET", "/api/channels/ch-1/messages?limit=0", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestListMessagesInvalidCursor() {
	rec := s.testRequest("GET", "/api/channels/ch-1/messages?cursor=abc", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)

	rec = s.testRequest("GET", "/api/channels/ch-1/messages?cursor=0", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestListMessagesLimitCap() {
	s.store.On("GetMessagesCursor", mock.Anything, "ch-1", int64(0), 201).Return([]*db.Message{}, nil)

	rec := s.testRequest("GET", "/api/channels/ch-1/messages?limit=500", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
}

func (s *ServerSuite) TestListMessagesError() {
	s.store.On("GetMessagesCursor", mock.Anything, "ch-1", int64(0), 51).Return(nil, errors.New("db error"))

	rec := s.testRequest("GET", "/api/channels/ch-1/messages", "")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
}

func (s *ServerSuite) TestListMessagesNotConfigured() {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := NewServer(nil, nil, nil, nil, nil, logger)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/channels/{id}/messages", srv.handleListMessages)
	req := httptest.NewRequest("GET", "/api/channels/ch-1/messages", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

// --- ListMessages around tests ---

func (s *ServerSuite) TestListMessagesAroundSuccess() {
	now := time.Now().UTC()
	msgs := []*db.Message{
		{ID: 8, ChannelID: "ch-1", MsgID: "m8", AuthorID: "u1", AuthorName: "alice", Content: "before", CreatedAt: now},
		{ID: 10, ChannelID: "ch-1", MsgID: "m10", AuthorID: "u1", AuthorName: "alice", Content: "target", CreatedAt: now},
		{ID: 12, ChannelID: "ch-1", MsgID: "m12", AuthorID: "bot", AuthorName: "assistant", Content: "after", IsBot: true, CreatedAt: now},
	}
	s.store.On("GetMessagesAround", mock.Anything, "ch-1", int64(10), 50).Return(msgs, nil)

	rec := s.testRequest("GET", "/api/channels/ch-1/messages?around=10", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp messagesListResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(s.T(), resp.Messages, 3)
	require.Equal(s.T(), int64(8), resp.Messages[0].ID)
	require.Equal(s.T(), int64(10), resp.Messages[1].ID)
	require.Equal(s.T(), int64(12), resp.Messages[2].ID)
	require.Nil(s.T(), resp.NextCursor)
}

func (s *ServerSuite) TestListMessagesAroundInvalid() {
	rec := s.testRequest("GET", "/api/channels/ch-1/messages?around=abc", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)

	rec = s.testRequest("GET", "/api/channels/ch-1/messages?around=0", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestListMessagesAroundError() {
	s.store.On("GetMessagesAround", mock.Anything, "ch-1", int64(5), 50).Return(nil, errors.New("db error"))

	rec := s.testRequest("GET", "/api/channels/ch-1/messages?around=5", "")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
}

func (s *ServerSuite) TestListMessagesAroundWithLimit() {
	now := time.Now().UTC()
	msgs := []*db.Message{
		{ID: 10, ChannelID: "ch-1", MsgID: "m10", AuthorID: "u1", AuthorName: "alice", Content: "target", CreatedAt: now},
	}
	s.store.On("GetMessagesAround", mock.Anything, "ch-1", int64(10), 20).Return(msgs, nil)

	rec := s.testRequest("GET", "/api/channels/ch-1/messages?around=10&limit=20", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp messagesListResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(s.T(), resp.Messages, 1)
}

func (s *ServerSuite) TestListMessagesAroundInvalidLimit() {
	rec := s.testRequest("GET", "/api/channels/ch-1/messages?around=10&limit=abc", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)

	rec = s.testRequest("GET", "/api/channels/ch-1/messages?around=10&limit=0", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestListMessagesAroundLimitCapped() {
	now := time.Now().UTC()
	msgs := []*db.Message{
		{ID: 10, ChannelID: "ch-1", MsgID: "m10", AuthorID: "u1", AuthorName: "alice", Content: "target", CreatedAt: now},
	}
	// Limit > maxMessageLimit should be capped to maxMessageLimit (200)
	s.store.On("GetMessagesAround", mock.Anything, "ch-1", int64(10), 200).Return(msgs, nil)

	rec := s.testRequest("GET", "/api/channels/ch-1/messages?around=10&limit=999", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
}

// --- SearchMessages tests ---

func (s *ServerSuite) TestSearchMessagesSuccess() {
	now := time.Now().UTC()
	msgs := []*db.Message{
		{ID: 10, ChannelID: "ch-1", MsgID: "m10", AuthorID: "u1", AuthorName: "alice", Content: "hello world", IsBot: false, CreatedAt: now},
		{ID: 5, ChannelID: "ch-2", MsgID: "m5", AuthorID: "bot", AuthorName: "assistant", Content: "hello there", IsBot: true, CreatedAt: now},
	}
	s.store.On("SearchMessages", mock.Anything, "hello", 20).Return(msgs, nil)

	rec := s.testRequest("GET", "/api/messages/search?q=hello", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var results []searchMessageResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&results))
	require.Len(s.T(), results, 2)
	require.Equal(s.T(), "hello world", results[0].Content)
	require.Equal(s.T(), "ch-1", results[0].ChannelID)
	require.False(s.T(), results[0].IsBot)
	require.Equal(s.T(), "hello there", results[1].Content)
	require.True(s.T(), results[1].IsBot)
}

func (s *ServerSuite) TestSearchMessagesEmptyQuery() {
	rec := s.testRequest("GET", "/api/messages/search?q=", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)

	rec = s.testRequest("GET", "/api/messages/search", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestSearchMessagesWithLimit() {
	s.store.On("SearchMessages", mock.Anything, "test", 5).Return([]*db.Message{}, nil)

	rec := s.testRequest("GET", "/api/messages/search?q=test&limit=5", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
}

func (s *ServerSuite) TestSearchMessagesInvalidLimit() {
	rec := s.testRequest("GET", "/api/messages/search?q=test&limit=abc", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)

	rec = s.testRequest("GET", "/api/messages/search?q=test&limit=0", "")
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestSearchMessagesLimitCap() {
	s.store.On("SearchMessages", mock.Anything, "test", 50).Return([]*db.Message{}, nil)

	rec := s.testRequest("GET", "/api/messages/search?q=test&limit=100", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
}

func (s *ServerSuite) TestSearchMessagesError() {
	s.store.On("SearchMessages", mock.Anything, "fail", 20).Return(nil, errors.New("db error"))

	rec := s.testRequest("GET", "/api/messages/search?q=fail", "")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
}

func (s *ServerSuite) TestSearchMessagesNotConfigured() {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := NewServer(nil, nil, nil, nil, nil, logger)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/messages/search", srv.handleSearchMessages)
	req := httptest.NewRequest("GET", "/api/messages/search?q=hello", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}
