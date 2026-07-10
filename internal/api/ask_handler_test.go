package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/events"
)

// MockPendingAsksLister mocks api.PendingAsksLister. Local to the test
// package so production code stays free of test-only types.
type MockPendingAsksLister struct {
	mock.Mock
}

func (m *MockPendingAsksLister) ListAskedChannels() []events.AskedChannelEntry {
	args := m.Called()
	if v := args.Get(0); v != nil {
		return v.([]events.AskedChannelEntry)
	}
	return nil
}

// MockAskResolver mocks api.AskResolver. Local to the test package so
// production code stays free of test-only types.
type MockAskResolver struct {
	mock.Mock
}

func (m *MockAskResolver) ClearAskedChannel(channelID string) {
	m.Called(channelID)
}

func (m *MockAskResolver) ResumeChannel(ctx context.Context, channelID string) {
	m.Called(ctx, channelID)
}

func (m *MockAskResolver) AskedChannelMode(channelID string) string {
	return m.Called(channelID).String(0)
}

func (s *ServerSuite) awaitAskCall(name string, ch <-chan struct{}) {
	select {
	case <-ch:
	case <-time.After(time.Second):
		s.T().Fatalf("%s was not called within 1s", name)
	}
}

func (s *ServerSuite) TestAskResolveAnswer() {
	handler := new(MockIncomingMessageHandler)
	resolver := new(MockAskResolver)
	s.srv.SetIncomingMessageHandler(handler)
	s.srv.SetAskResolver(resolver)

	// Order matters: insert (priority-bumped) must precede ClearAskedChannel
	// so the drain cannot claim before the new row lands. Mirrors the
	// interrupt path in handleSendMessage.
	var order []string
	var orderMu sync.Mutex
	record := func(name string) {
		orderMu.Lock()
		defer orderMu.Unlock()
		order = append(order, name)
	}

	s.store.On("MaxQueuedPriority", mock.Anything, "ch-1").Return(2, nil)
	called := make(chan struct{}, 1)
	handler.On("HandleIncomingMessageWithPriority", mock.Anything, "ch-1", "", "use redis", "agent", 3).
		Run(func(_ mock.Arguments) {
			record("insert")
			called <- struct{}{}
		}).Return()
	resolver.On("ClearAskedChannel", "ch-1").
		Run(func(_ mock.Arguments) { record("clear") }).Return()
	resumed := make(chan struct{}, 1)
	resolver.On("ResumeChannel", mock.Anything, "ch-1").
		Run(func(_ mock.Arguments) {
			record("resume")
			resumed <- struct{}{}
		}).Return()

	rec := s.testRequest("POST", "/api/channels/ch-1/ask/resolve",
		`{"action":"answer","answer":"use redis","mode":"agent"}`)
	require.Equal(s.T(), http.StatusNoContent, rec.Code)

	s.awaitAskCall("HandleIncomingMessageWithPriority", called)
	s.awaitAskCall("ResumeChannel", resumed)

	handler.AssertExpectations(s.T())
	resolver.AssertExpectations(s.T())

	orderMu.Lock()
	defer orderMu.Unlock()
	require.Equal(s.T(), []string{"insert", "clear", "resume"}, order,
		"insert → clear → resume; clear before resume avoids the bail-early race")
}

func (s *ServerSuite) TestAskResolveAnswerMaxPriorityErrorFallsBackToZero() {
	handler := new(MockIncomingMessageHandler)
	resolver := new(MockAskResolver)
	s.srv.SetIncomingMessageHandler(handler)
	s.srv.SetAskResolver(resolver)

	resolver.On("AskedChannelMode", "ch-1").Return("")
	s.store.On("MaxQueuedPriority", mock.Anything, "ch-1").Return(0, errors.New("db error"))
	called := make(chan struct{}, 1)
	handler.On("HandleIncomingMessageWithPriority", mock.Anything, "ch-1", "", "x", "", 0).
		Run(func(_ mock.Arguments) { called <- struct{}{} }).Return()
	resolver.On("ClearAskedChannel", "ch-1").Return()
	resumed := make(chan struct{}, 1)
	resolver.On("ResumeChannel", mock.Anything, "ch-1").
		Run(func(_ mock.Arguments) { resumed <- struct{}{} }).Return()

	rec := s.testRequest("POST", "/api/channels/ch-1/ask/resolve", `{"action":"answer","answer":"x"}`)
	require.Equal(s.T(), http.StatusNoContent, rec.Code)

	s.awaitAskCall("HandleIncomingMessageWithPriority", called)
	s.awaitAskCall("ResumeChannel", resumed)
	handler.AssertExpectations(s.T())
	resolver.AssertExpectations(s.T())
}

// TestAskResolveInheritsParkedMode verifies that when the resolve request
// carries no mode, the continuation inherits the asking run's composer mode —
// an ask raised mid-plan must resume in plan mode, or the agent implements
// without plan approval.
func (s *ServerSuite) TestAskResolveInheritsParkedMode() {
	handler := new(MockIncomingMessageHandler)
	resolver := new(MockAskResolver)
	s.srv.SetIncomingMessageHandler(handler)
	s.srv.SetAskResolver(resolver)

	resolver.On("AskedChannelMode", "ch-1").Return("plan")
	s.store.On("MaxQueuedPriority", mock.Anything, "ch-1").Return(0, nil)
	called := make(chan struct{}, 1)
	handler.On("HandleIncomingMessageWithPriority", mock.Anything, "ch-1", "", "use redis", "plan", 1).
		Run(func(_ mock.Arguments) { called <- struct{}{} }).Return()
	resolver.On("ClearAskedChannel", "ch-1").Return()
	resumed := make(chan struct{}, 1)
	resolver.On("ResumeChannel", mock.Anything, "ch-1").
		Run(func(_ mock.Arguments) { resumed <- struct{}{} }).Return()

	rec := s.testRequest("POST", "/api/channels/ch-1/ask/resolve", `{"action":"answer","answer":"use redis"}`)
	require.Equal(s.T(), http.StatusNoContent, rec.Code)

	s.awaitAskCall("HandleIncomingMessageWithPriority", called)
	s.awaitAskCall("ResumeChannel", resumed)
	handler.AssertExpectations(s.T())
	resolver.AssertExpectations(s.T())
}

func (s *ServerSuite) TestAskResolveAnswerMissingAnswer() {
	handler := new(MockIncomingMessageHandler)
	resolver := new(MockAskResolver)
	s.srv.SetIncomingMessageHandler(handler)
	s.srv.SetAskResolver(resolver)

	resolver.On("AskedChannelMode", "ch-1").Return("")

	rec := s.testRequest("POST", "/api/channels/ch-1/ask/resolve", `{"action":"answer"}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)

	handler.AssertNotCalled(s.T(), "HandleIncomingMessageWithPriority")
	resolver.AssertNotCalled(s.T(), "ClearAskedChannel")
}

func (s *ServerSuite) TestAskResolveCancel() {
	handler := new(MockIncomingMessageHandler)
	resolver := new(MockAskResolver)
	s.srv.SetIncomingMessageHandler(handler)
	s.srv.SetAskResolver(resolver)

	resolver.On("AskedChannelMode", "ch-1").Return("")
	s.store.On("MaxQueuedPriority", mock.Anything, "ch-1").Return(0, nil)
	called := make(chan struct{}, 1)
	handler.On("HandleIncomingMessageWithPriority", mock.Anything, "ch-1", "", askCancelPrompt, "", 1).
		Run(func(_ mock.Arguments) { called <- struct{}{} }).Return()
	resolver.On("ClearAskedChannel", "ch-1").Return()
	resumed := make(chan struct{}, 1)
	resolver.On("ResumeChannel", mock.Anything, "ch-1").
		Run(func(_ mock.Arguments) { resumed <- struct{}{} }).Return()

	rec := s.testRequest("POST", "/api/channels/ch-1/ask/resolve", `{"action":"cancel"}`)
	require.Equal(s.T(), http.StatusNoContent, rec.Code)

	s.awaitAskCall("HandleIncomingMessageWithPriority", called)
	s.awaitAskCall("ResumeChannel", resumed)
	handler.AssertExpectations(s.T())
	resolver.AssertExpectations(s.T())
}

func (s *ServerSuite) TestAskResolveInvalidAction() {
	resolver := new(MockAskResolver)
	s.srv.SetIncomingMessageHandler(new(MockIncomingMessageHandler))
	s.srv.SetAskResolver(resolver)
	resolver.On("AskedChannelMode", "ch-1").Return("")

	rec := s.testRequest("POST", "/api/channels/ch-1/ask/resolve", `{"action":"nope"}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)

	resolver.AssertNotCalled(s.T(), "ClearAskedChannel")
}

func (s *ServerSuite) TestAskResolveBadJSON() {
	s.srv.SetIncomingMessageHandler(new(MockIncomingMessageHandler))
	s.srv.SetAskResolver(new(MockAskResolver))

	rec := s.testRequest("POST", "/api/channels/ch-1/ask/resolve", `not json`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestAskResolveNoResolverConfigured() {
	rec := s.testRequest("POST", "/api/channels/ch-1/ask/resolve", `{"action":"answer","answer":"x"}`)
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

func (s *ServerSuite) TestAskResolveNoHandlerConfigured() {
	s.srv.SetAskResolver(new(MockAskResolver))
	rec := s.testRequest("POST", "/api/channels/ch-1/ask/resolve", `{"action":"answer","answer":"x"}`)
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

func (s *ServerSuite) TestListPendingAsksNotConfigured() {
	rec := s.testRequest("GET", "/api/asks/pending", "")
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

func (s *ServerSuite) TestListPendingAsksEmpty() {
	lister := new(MockPendingAsksLister)
	lister.On("ListAskedChannels").Return(nil)
	s.srv.SetPendingAsksLister(lister)

	rec := s.testRequest("GET", "/api/asks/pending", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp pendingAsksResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	require.NotNil(s.T(), resp.Asks)
	require.Empty(s.T(), resp.Asks)
}

func (s *ServerSuite) TestListPendingAsksWithEntries() {
	entries := []events.AskedChannelEntry{
		{ChannelID: "ch-1", Data: events.AskUserQuestionEventData{Questions: []events.AskUserQuestion{{Question: "yes/no?"}}}},
	}
	lister := new(MockPendingAsksLister)
	lister.On("ListAskedChannels").Return(entries)
	s.srv.SetPendingAsksLister(lister)

	rec := s.testRequest("GET", "/api/asks/pending", "")
	require.Equal(s.T(), http.StatusOK, rec.Code)

	var resp pendingAsksResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(s.T(), entries, resp.Asks)
}
