package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/apiauth"
	"github.com/radutopala/loop/internal/bot"
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
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1"}, nil).Maybe()

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

func (s *ServerSuite) TestSendMessageOriginTags() {
	agent := &apiauth.Principal{Kind: apiauth.KindAgent, ChannelID: "agent-ch"}
	owner := &apiauth.Principal{Kind: apiauth.KindOwner}
	named := &db.Channel{ChannelID: "agent-ch", Name: "backend"}
	tests := []struct {
		name      string
		principal *apiauth.Principal
		body      string
		channel   *db.Channel
		lookupErr error
		promptID  int64
		promptErr error
		noLookups bool // sent from its own channel, or none: nothing to look up
		want      map[string]string
	}{
		{
			name:      "agent token: all tags",
			principal: agent,
			body:      `{"channel_id":"ch-1","content":"hi"}`,
			channel:   named,
			promptID:  42,
			want:      map[string]string{bot.TagOriginChannel: "agent-ch", bot.TagOriginChannelName: "backend", bot.TagOriginMessage: "42"},
		},
		{
			name:      "agent token can't claim another channel",
			principal: agent,
			body:      `{"channel_id":"ch-1","content":"hi","from_channel_id":"forged"}`,
			channel:   named,
			want:      map[string]string{bot.TagOriginChannel: "agent-ch", bot.TagOriginChannelName: "backend"},
		},
		{
			name:      "plugin token counts as its own channel too",
			principal: &apiauth.Principal{Kind: apiauth.KindPlugin, ChannelID: "agent-ch"},
			body:      `{"channel_id":"ch-1","content":"hi","from_channel_id":"forged"}`,
			channel:   named,
			want:      map[string]string{bot.TagOriginChannel: "agent-ch", bot.TagOriginChannelName: "backend"},
		},
		{
			name:      "owner token names its channel",
			principal: owner,
			body:      `{"channel_id":"ch-1","content":"hi","from_channel_id":"agent-ch"}`,
			channel:   named,
			want:      map[string]string{bot.TagOriginChannel: "agent-ch", bot.TagOriginChannelName: "backend"},
		},
		{
			name:      "lookup failures leave the channel id",
			principal: agent,
			body:      `{"channel_id":"ch-1","content":"hi"}`,
			lookupErr: errors.New("db down"),
			promptErr: errors.New("db down"),
			want:      map[string]string{bot.TagOriginChannel: "agent-ch"},
		},
		{
			name:      "unknown channel, no prompt yet",
			principal: agent,
			body:      `{"channel_id":"ch-1","content":"hi"}`,
			want:      map[string]string{bot.TagOriginChannel: "agent-ch"},
		},
		{name: "own channel", principal: agent, body: `{"channel_id":"agent-ch","content":"hi"}`, noLookups: true},
		{name: "owner without a channel", principal: owner, body: `{"channel_id":"ch-1","content":"hi"}`, noLookups: true},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			target := "ch-1"
			if tc.noLookups && tc.principal == agent {
				target = "agent-ch"
			}
			if target == "agent-ch" {
				s.store.On("GetChannel", mock.Anything, target).Return(&db.Channel{ChannelID: target}, nil).Once()
			}
			if !tc.noLookups {
				s.store.On("GetChannel", mock.Anything, "agent-ch").Return(tc.channel, tc.lookupErr).Once()
				s.store.On("LatestPromptRowID", mock.Anything, "agent-ch").Return(tc.promptID, tc.promptErr).Once()
			}
			handler := new(MockIncomingMessageHandler)
			s.srv.SetIncomingMessageHandler(handler)
			s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1"}, nil).Maybe()
			var got map[string]string
			handler.On("HandleIncomingMessage", mock.Anything, target, "", "hi", "").
				Run(func(args mock.Arguments) { got = bot.MessageTagsFrom(args.Get(0).(context.Context)) }).Return().Once()

			req := httptest.NewRequest("POST", "/api/messages", strings.NewReader(tc.body))
			req = req.WithContext(apiauth.WithPrincipal(req.Context(), *tc.principal))
			rec := httptest.NewRecorder()
			s.srv.handleSendMessage(rec, req)

			require.Equal(s.T(), http.StatusNoContent, rec.Code)
			require.Equal(s.T(), tc.want, got)
			handler.AssertExpectations(s.T())
			s.store.AssertExpectations(s.T())
		})
	}
}

// The orchestrator drops a message for a channel it can't find, so the sender
// gets the error instead, however the message was to be delivered.
func (s *ServerSuite) TestSendMessageTargetChannel() {
	tests := []struct {
		name       string
		extra      string
		channel    *db.Channel
		lookupErr  error
		wantStatus int
		wantBody   string
	}{
		{name: "missing", wantStatus: http.StatusNotFound, wantBody: "channel not found"},
		{name: "missing, added", extra: `,"delivery":"add"`, wantStatus: http.StatusNotFound, wantBody: "channel not found"},
		{name: "missing, steered", extra: `,"delivery":"steer"`, wantStatus: http.StatusNotFound, wantBody: "channel not found"},
		{name: "missing, delayed", extra: `,"delay_seconds":60`, wantStatus: http.StatusNotFound, wantBody: "channel not found"},
		{name: "lookup fails", lookupErr: errors.New("db down"), wantStatus: http.StatusInternalServerError, wantBody: "db down"},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.store.On("GetChannel", mock.Anything, "gone").Return(tc.channel, tc.lookupErr).Once()
			handler := new(MockIncomingMessageHandler)
			s.srv.SetIncomingMessageHandler(handler)

			rec := s.testRequest("POST", "/api/messages", fmt.Sprintf(`{"channel_id":"gone","content":"hi"%s}`, tc.extra))

			require.Equal(s.T(), tc.wantStatus, rec.Code)
			require.Contains(s.T(), rec.Body.String(), tc.wantBody)
			require.Empty(s.T(), handler.Calls)
			s.store.AssertExpectations(s.T())
		})
	}
}

// Delayed and added messages keep their tags, as queued ones do: each takes a
// different handler call.
func (s *ServerSuite) TestSendMessageOriginTagsOtherDeliveries() {
	body := `{"channel_id":"ch-1","content":"hi","from_channel_id":"from-ch"%s}`
	tests := []struct {
		name  string
		extra string
		call  string
		args  []any
	}{
		{name: "added", extra: `,"delivery":"add"`, call: "HandleIncomingMessageAdded", args: []any{mock.Anything, "ch-1", "", "hi", ""}},
		{name: "steered", extra: `,"delivery":"steer"`, call: "HandleIncomingMessageWithPriority", args: []any{mock.Anything, "ch-1", "", "hi", "", mock.Anything}},
		{name: "delayed", extra: `,"delay_seconds":60`, call: "HandleIncomingMessageDelayed", args: []any{mock.Anything, "ch-1", "", mock.Anything, "hi", "", mock.Anything}},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.store.On("GetChannel", mock.Anything, "from-ch").Return(nil, nil).Once()
			s.store.On("LatestPromptRowID", mock.Anything, "from-ch").Return(int64(0), nil).Once()
			s.store.On("MaxQueuedPriority", mock.Anything, "ch-1").Return(0, nil).Maybe()
			handler := new(MockIncomingMessageHandler)
			s.srv.SetIncomingMessageHandler(handler)
			s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1"}, nil).Maybe()
			canceller := new(MockRunCanceller)
			canceller.On("CancelActiveRun", "ch-1").Return(true).Maybe()
			s.srv.SetRunCanceller(canceller)
			var got map[string]string
			handler.On(tc.call, tc.args...).
				Run(func(args mock.Arguments) { got = bot.MessageTagsFrom(args.Get(0).(context.Context)) }).Return().Once()

			rec := s.testRequest("POST", "/api/messages", fmt.Sprintf(body, tc.extra))

			require.Less(s.T(), rec.Code, 300)
			require.Equal(s.T(), map[string]string{bot.TagOriginChannel: "from-ch"}, got)
		})
	}
}

// Without an orchestrator the row and its tags don't exist, so the label goes
// straight into the posted text.
func (s *ServerSuite) TestSendMessageOriginLabelWithoutHandler() {
	s.srv.store = nil
	s.srv.msgHandler = nil
	s.messages.On("PostMessage", mock.Anything, "ch-1", "[from channel_id: from-ch; if it needs an answer, send it with send_message to this channel_id] hi").Return(nil).Once()

	rec := s.testRequest("POST", "/api/messages", `{"channel_id":"ch-1","content":"hi","from_channel_id":"from-ch"}`)

	require.Equal(s.T(), http.StatusNoContent, rec.Code)
	s.messages.AssertExpectations(s.T())
}

func (s *ServerSuite) TestSendMessageViaHandlerPlanMode() {
	handler := new(MockIncomingMessageHandler)
	s.srv.SetIncomingMessageHandler(handler)
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1"}, nil).Maybe()

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
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1"}, nil).Maybe()
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
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1"}, nil).Maybe()
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
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1"}, nil).Maybe()
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
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1"}, nil).Maybe()
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

func (s *ServerSuite) TestSendMessageDelivery() {
	tests := []struct {
		name string
		body string
		on   func(h *MockIncomingMessageHandler, c *MockRunCanceller)
	}{
		{
			name: "add goes to the running turn",
			body: `{"channel_id":"ch-1","content":"also this","delivery":"add"}`,
			on: func(h *MockIncomingMessageHandler, _ *MockRunCanceller) {
				h.On("HandleIncomingMessageAdded", mock.Anything, "ch-1", "", "also this", "").Return().Once()
			},
		},
		{
			name: "queue waits behind the run",
			body: `{"channel_id":"ch-1","content":"also this","delivery":"queue"}`,
			on: func(h *MockIncomingMessageHandler, _ *MockRunCanceller) {
				h.On("HandleIncomingMessage", mock.Anything, "ch-1", "", "also this", "").Return().Once()
			},
		},
		{
			name: "steer cancels the run",
			body: `{"channel_id":"ch-1","content":"also this","delivery":"steer"}`,
			on: func(h *MockIncomingMessageHandler, c *MockRunCanceller) {
				s.store.On("MaxQueuedPriority", mock.Anything, "ch-1").Return(0, nil).Once()
				h.On("HandleIncomingMessageWithPriority", mock.Anything, "ch-1", "", "also this", "", 1).Return().Once()
				c.On("CancelActiveRun", "ch-1").Return(true).Once()
			},
		},
		{
			name: "steer agrees with interrupt",
			body: `{"channel_id":"ch-1","content":"also this","delivery":"steer","interrupt":true}`,
			on: func(h *MockIncomingMessageHandler, c *MockRunCanceller) {
				s.store.On("MaxQueuedPriority", mock.Anything, "ch-1").Return(0, nil).Once()
				h.On("HandleIncomingMessageWithPriority", mock.Anything, "ch-1", "", "also this", "", 1).Return().Once()
				c.On("CancelActiveRun", "ch-1").Return(true).Once()
			},
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			handler := new(MockIncomingMessageHandler)
			canceller := new(MockRunCanceller)
			s.srv.SetIncomingMessageHandler(handler)
			s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1"}, nil).Maybe()
			s.srv.SetRunCanceller(canceller)
			tc.on(handler, canceller)

			rec := s.testRequest("POST", "/api/messages", tc.body)

			require.Equal(s.T(), http.StatusNoContent, rec.Code)
			handler.AssertExpectations(s.T())
			canceller.AssertExpectations(s.T())
		})
	}
}

func (s *ServerSuite) TestSendMessageBadDelivery() {
	for _, body := range []string{
		`{"channel_id":"ch-1","content":"x","delivery":"later"}`,
		`{"channel_id":"ch-1","content":"x","delivery":"add","interrupt":true}`,
		`{"channel_id":"ch-1","content":"x","delivery":"queue","interrupt":true}`,
	} {
		handler := new(MockIncomingMessageHandler)
		s.srv.SetIncomingMessageHandler(handler)
		s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1"}, nil).Maybe()

		rec := s.testRequest("POST", "/api/messages", body)

		require.Equal(s.T(), http.StatusBadRequest, rec.Code, body)
		handler.AssertNotCalled(s.T(), "HandleIncomingMessage", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	}
}

func (s *ServerSuite) TestSendMessageDelayed() {
	handler := new(MockIncomingMessageHandler)
	canceller := new(MockRunCanceller)
	s.srv.SetIncomingMessageHandler(handler)
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1"}, nil).Maybe()
	s.srv.SetRunCanceller(canceller)

	called := make(chan struct{}, 1)
	before := time.Now().Add(30 * time.Second).Unix()
	var msgID string
	handler.On("HandleIncomingMessageDelayed", mock.Anything, "ch-1", "",
		mock.MatchedBy(func(id string) bool { return strings.HasPrefix(id, "ask-") }), "later", "",
		mock.MatchedBy(func(notBefore int64) bool {
			after := time.Now().Add(30 * time.Second).Unix()
			return notBefore >= before && notBefore <= after
		})).
		Run(func(args mock.Arguments) {
			msgID = args.String(3)
			called <- struct{}{}
		}).Return()

	// delay_seconds takes precedence over interrupt: the run is never cancelled.
	rec := s.testRequest("POST", "/api/messages", `{"channel_id":"ch-1","content":"later","delay_seconds":30,"interrupt":true}`)

	require.Equal(s.T(), http.StatusOK, rec.Code)
	// The id handed to the orchestrator is the one returned to the caller.
	require.JSONEq(s.T(), `{"msg_id":"`+msgID+`"}`, rec.Body.String())

	select {
	case <-called:
	case <-time.After(time.Second):
		s.T().Fatal("HandleIncomingMessageDelayed was not called within 1s")
	}

	handler.AssertExpectations(s.T())
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

func (s *ServerSuite) TestReorderQueuedMessagesSuccess() {
	s.store.On("ReorderQueuedMessages", mock.Anything, "ch-1", []string{"a", "b"}).Return(nil)

	rec := s.testRequest("POST", "/api/channels/ch-1/queued/reorder", `{"order":["a","b"]}`)
	require.Equal(s.T(), http.StatusNoContent, rec.Code)
	s.store.AssertExpectations(s.T())
}

func (s *ServerSuite) TestReorderQueuedMessagesBadBody() {
	rec := s.testRequest("POST", "/api/channels/ch-1/queued/reorder", `not json`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestReorderQueuedMessagesError() {
	s.store.On("ReorderQueuedMessages", mock.Anything, "ch-1", []string{"a"}).Return(errors.New("boom"))

	rec := s.testRequest("POST", "/api/channels/ch-1/queued/reorder", `{"order":["a"]}`)
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	s.store.AssertExpectations(s.T())
}

func (s *ServerSuite) TestReorderQueuedMessagesNotConfigured() {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := NewServer(nil, nil, nil, nil, nil, logger)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/channels/{id}/queued/reorder", srv.handleReorderQueuedMessages)
	req := httptest.NewRequest("POST", "/api/channels/ch-1/queued/reorder", strings.NewReader(`{"order":[]}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

func (s *ServerSuite) TestSteerQueuedMessageSuccess() {
	canceller := new(MockRunCanceller)
	s.srv.SetRunCanceller(canceller)

	// Same ordering rule as the interrupt send: the row is promoted before the
	// run is cancelled, so the dying run's drain cannot claim an older row
	// ahead of the steered one.
	var order []string
	s.store.On("SteerQueuedMessage", mock.Anything, "ch-1", "msg-7").
		Run(func(_ mock.Arguments) { order = append(order, "promote") }).Return(true, nil)
	canceller.On("CancelActiveRun", "ch-1").
		Run(func(_ mock.Arguments) { order = append(order, "cancel") }).Return(true)

	rec := s.testRequest("POST", "/api/channels/ch-1/queued/msg-7/steer", "")
	require.Equal(s.T(), http.StatusNoContent, rec.Code)
	require.Equal(s.T(), []string{"promote", "cancel"}, order)
	s.store.AssertExpectations(s.T())
	canceller.AssertExpectations(s.T())
}

func (s *ServerSuite) TestSteerQueuedMessageNoCanceller() {
	// runCanceller is nil — the promotion still stands and nothing panics.
	s.store.On("SteerQueuedMessage", mock.Anything, "ch-1", "msg-7").Return(true, nil)

	rec := s.testRequest("POST", "/api/channels/ch-1/queued/msg-7/steer", "")
	require.Equal(s.T(), http.StatusNoContent, rec.Code)
	s.store.AssertExpectations(s.T())
}

func (s *ServerSuite) TestSteerQueuedMessageNotFound() {
	canceller := new(MockRunCanceller)
	s.srv.SetRunCanceller(canceller)
	s.store.On("SteerQueuedMessage", mock.Anything, "ch-1", "gone").Return(false, nil)

	rec := s.testRequest("POST", "/api/channels/ch-1/queued/gone/steer", "")
	require.Equal(s.T(), http.StatusNotFound, rec.Code)
	// A row that was never promoted must not cost the user their running turn.
	canceller.AssertNotCalled(s.T(), "CancelActiveRun", "ch-1")
	s.store.AssertExpectations(s.T())
}

func (s *ServerSuite) TestSteerQueuedMessageError() {
	s.store.On("SteerQueuedMessage", mock.Anything, "ch-1", "msg-7").Return(false, errors.New("boom"))

	rec := s.testRequest("POST", "/api/channels/ch-1/queued/msg-7/steer", "")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
	s.store.AssertExpectations(s.T())
}

func (s *ServerSuite) TestSteerQueuedMessageNotConfigured() {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := NewServer(nil, nil, nil, nil, nil, logger)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/channels/{id}/queued/{msg_id}/steer", srv.handleSteerQueuedMessage)
	req := httptest.NewRequest("POST", "/api/channels/ch-1/queued/msg-7/steer", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

func (s *ServerSuite) TestSetIncomingMessageHandler() {
	require.Nil(s.T(), s.srv.msgHandler)

	handler := new(MockIncomingMessageHandler)
	s.srv.SetIncomingMessageHandler(handler)
	s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1"}, nil).Maybe()

	require.NotNil(s.T(), s.srv.msgHandler)
	require.Equal(s.T(), handler, s.srv.msgHandler)
}

// --- ListQueuedMessages tests ---

func (s *ServerSuite) TestListQueuedMessagesSuccess() {
	now := time.Now().UTC()
	msgs := []*db.Message{
		{ID: 12, ChannelID: "ch-1", MsgID: "m12", AuthorID: "u1", AuthorName: "alice", Content: "bumped", Priority: 1, IsRunning: true, CreatedAt: now},
		{ID: 10, ChannelID: "ch-1", MsgID: "m10", AuthorID: "u1", AuthorName: "alice", Content: "first", CreatedAt: now},
		{ID: 14, ChannelID: "ch-1", MsgID: "m14", AuthorID: "u1", AuthorName: "alice", Content: "also", AddUUID: "u-1", CreatedAt: now},
	}
	s.store.On("ListQueuedUserMessages", mock.Anything, "ch-1").Return(msgs, nil)

	rec := s.testRequest("GET", "/api/channels/ch-1/queued", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp queuedMessagesResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Len(s.T(), resp.Messages, 3)
	require.Equal(s.T(), "m12", resp.Messages[0].MsgID)
	require.Equal(s.T(), 1, resp.Messages[0].Priority)
	require.True(s.T(), resp.Messages[0].IsRunning)
	require.Equal(s.T(), "m10", resp.Messages[1].MsgID)
	require.False(s.T(), resp.Messages[1].IsRunning)
	require.False(s.T(), resp.Messages[1].Adding)
	require.True(s.T(), resp.Messages[2].Adding)
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

// --- SearchChannelMessages tests ---

func (s *ServerSuite) TestSearchChannelMessages() {
	tests := []struct {
		name  string
		url   string
		q     string
		limit int
		ids   []int64
		body  string
	}{
		{name: "matches", url: "/api/channels/ch-1/messages/search?q=hello", q: "hello", limit: 500, ids: []int64{12, 7}, body: `{"ids":[12,7]}`},
		{name: "no matches, query trimmed", url: "/api/channels/ch-1/messages/search?q=%20nope%20", q: "nope", limit: 500, ids: nil, body: `{"ids":[]}`},
		{name: "limit", url: "/api/channels/ch-1/messages/search?q=hello&limit=5", q: "hello", limit: 5, ids: []int64{12}, body: `{"ids":[12]}`},
		{name: "limit cap", url: "/api/channels/ch-1/messages/search?q=hello&limit=5000", q: "hello", limit: 1000, ids: []int64{}, body: `{"ids":[]}`},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.store.On("SearchChannelMessages", mock.Anything, "ch-1", tc.q, tc.limit).Return(tc.ids, nil)

			rec := s.testRequest("GET", tc.url, "")
			require.Equal(s.T(), http.StatusOK, rec.Code)
			require.JSONEq(s.T(), tc.body, rec.Body.String())
		})
	}
}

func (s *ServerSuite) TestSearchChannelMessagesBadRequest() {
	for _, url := range []string{
		"/api/channels/ch-1/messages/search",
		"/api/channels/ch-1/messages/search?q=%20",
		"/api/channels/ch-1/messages/search?q=x&limit=abc",
	} {
		rec := s.testRequest("GET", url, "")
		require.Equal(s.T(), http.StatusBadRequest, rec.Code, url)
	}
}

func (s *ServerSuite) TestSearchChannelMessagesError() {
	s.store.On("SearchChannelMessages", mock.Anything, "ch-1", "fail", 500).Return(nil, errors.New("db error"))

	rec := s.testRequest("GET", "/api/channels/ch-1/messages/search?q=fail", "")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
}

func (s *ServerSuite) TestSearchChannelMessagesNotConfigured() {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := NewServer(nil, nil, nil, nil, nil, logger)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/channels/{id}/messages/search", srv.handleSearchChannelMessages)
	req := httptest.NewRequest("GET", "/api/channels/ch-1/messages/search?q=hello", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

// --- ComposerHistory tests ---

func (s *ServerSuite) TestComposerHistorySuccess() {
	s.store.On("ListUserMessageContents", mock.Anything, "ch-1", 100).
		Return([]string{"first", "second"}, nil)

	rec := s.testRequest("GET", "/api/channels/ch-1/composer-history", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp composerHistoryResponse
	require.NoError(s.T(), json.NewDecoder(rec.Body).Decode(&resp))
	require.Equal(s.T(), []string{"first", "second"}, resp.Messages)
}

func (s *ServerSuite) TestComposerHistoryCustomLimitAndEmpty() {
	s.store.On("ListUserMessageContents", mock.Anything, "ch-1", 25).
		Return(nil, nil)

	rec := s.testRequest("GET", "/api/channels/ch-1/composer-history?limit=25", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)
	require.Contains(s.T(), rec.Body.String(), `"messages":[]`)
}

func (s *ServerSuite) TestComposerHistoryInvalidLimitFallsBack() {
	// Out-of-range / non-numeric limits fall back to the default 100.
	s.store.On("ListUserMessageContents", mock.Anything, "ch-1", 100).
		Return([]string{"x"}, nil).Times(3)
	for _, q := range []string{"?limit=0", "?limit=9999", "?limit=abc"} {
		rec := s.testRequest("GET", "/api/channels/ch-1/composer-history"+q, "")
		require.Equal(s.T(), http.StatusOK, rec.Code)
	}
}

func (s *ServerSuite) TestComposerHistoryStoreError() {
	s.store.On("ListUserMessageContents", mock.Anything, "ch-1", 100).
		Return(nil, errors.New("db down"))

	rec := s.testRequest("GET", "/api/channels/ch-1/composer-history", "")
	require.Equal(s.T(), http.StatusInternalServerError, rec.Code)
}

// TestComposerHistoryStoreNotConfigured pins the 501 guard when the server
// has no message store wired.
func (s *ServerSuite) TestComposerHistoryStoreNotConfigured() {
	srv := &Server{serverDeps: serverDeps{logger: s.srv.logger}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/channels/{id}/composer-history", srv.handleComposerHistory)
	req := httptest.NewRequest("GET", "/api/channels/ch-1/composer-history", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

// --- WakeChannel / PublishChannelEvent tests ---

func (s *ServerSuite) TestWakeChannel() {
	s.Run("no handler", func() {
		s.srv.WakeChannel(context.Background(), "ch-1", "done", false)
	})
	s.Run("queue", func() {
		handler := new(MockIncomingMessageHandler)
		s.srv.SetIncomingMessageHandler(handler)
		s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1"}, nil).Maybe()
		handler.On("HandleIncomingMessage", mock.Anything, "ch-1", "", "done", "").Return()

		s.srv.WakeChannel(context.Background(), "ch-1", "done", false)
		handler.AssertExpectations(s.T())
	})
	s.Run("interrupt", func() {
		handler := new(MockIncomingMessageHandler)
		canceller := new(MockRunCanceller)
		s.srv.SetIncomingMessageHandler(handler)
		s.store.On("GetChannel", mock.Anything, "ch-1").Return(&db.Channel{ChannelID: "ch-1"}, nil).Maybe()
		s.srv.SetRunCanceller(canceller)
		var order []string
		s.store.On("MaxQueuedPriority", mock.Anything, "ch-1").Return(4, nil)
		handler.On("HandleIncomingMessageWithPriority", mock.Anything, "ch-1", "", "done", "", 5).
			Run(func(_ mock.Arguments) { order = append(order, "insert") }).Return()
		canceller.On("CancelActiveRun", "ch-1").
			Run(func(_ mock.Arguments) { order = append(order, "cancel") }).Return(true)

		s.srv.WakeChannel(context.Background(), "ch-1", "done", true)
		require.Equal(s.T(), []string{"insert", "cancel"}, order)
	})
}

func (s *ServerSuite) TestPublishChannelEvent() {
	s.srv.PublishChannelEvent("ch-1", "monitor.started", nil)

	s.srv.SetEventsHub(NewEventsHub(testLogger()))
	var got []Event
	s.srv.EventsHub().Observe(func(e Event) { got = append(got, e) })
	s.srv.PublishChannelEvent("ch-1", "monitor.started", map[string]string{"id": "mon-a"})

	require.Len(s.T(), got, 1)
	require.Equal(s.T(), "monitor.started", got[0].Type)
	require.Equal(s.T(), "ch-1", got[0].ChannelID)
	require.Equal(s.T(), map[string]string{"id": "mon-a"}, got[0].Data)
}
