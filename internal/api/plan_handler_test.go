package api

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// MockPlanResolver mocks api.PlanResolver. Local to the test package so
// production code stays free of test-only types.
type MockPlanResolver struct {
	mock.Mock
}

func (m *MockPlanResolver) ClearPlannedChannel(channelID string) {
	m.Called(channelID)
}

func (m *MockPlanResolver) ResumeChannel(ctx context.Context, channelID string) {
	m.Called(ctx, channelID)
}

// awaitCall blocks up to 1s for a buffered signal, failing the test if the
// expected mock interaction never arrives.
func (s *ServerSuite) awaitPlanCall(name string, ch <-chan struct{}) {
	select {
	case <-ch:
	case <-time.After(time.Second):
		s.T().Fatalf("%s was not called within 1s", name)
	}
}

func (s *ServerSuite) TestPlanResolveApprove() {
	handler := new(MockIncomingMessageHandler)
	resolver := new(MockPlanResolver)
	s.srv.SetIncomingMessageHandler(handler)
	s.srv.SetPlanResolver(resolver)

	// Order matters: insert (priority-bumped) must precede ClearPlannedChannel
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
	handler.On("HandleIncomingMessageWithPriority", mock.Anything, "ch-1", "", planApprovePrompt, "", 3).
		Run(func(_ mock.Arguments) {
			record("insert")
			called <- struct{}{}
		}).Return()
	resolver.On("ClearPlannedChannel", "ch-1").
		Run(func(_ mock.Arguments) { record("clear") }).Return()
	// ResumeChannel kicks a fresh drain so the just-inserted row gets
	// claimed even if the drain spawned by HandleIncomingMessageWithPriority
	// bailed early while the pause flag was still set.
	resumed := make(chan struct{}, 1)
	resolver.On("ResumeChannel", mock.Anything, "ch-1").
		Run(func(_ mock.Arguments) {
			record("resume")
			resumed <- struct{}{}
		}).Return()

	rec := s.testRequest("POST", "/api/channels/ch-1/plan/resolve", `{"action":"approve"}`)

	require.Equal(s.T(), http.StatusNoContent, rec.Code)

	s.awaitPlanCall("HandleIncomingMessageWithPriority", called)
	s.awaitPlanCall("ResumeChannel", resumed)

	handler.AssertExpectations(s.T())
	resolver.AssertExpectations(s.T())

	orderMu.Lock()
	defer orderMu.Unlock()
	require.Equal(s.T(), []string{"insert", "clear", "resume"}, order,
		"insert → clear → resume; clear before resume avoids the bail-early race")
}

func (s *ServerSuite) TestPlanResolveApproveMaxPriorityErrorFallsBackToZero() {
	handler := new(MockIncomingMessageHandler)
	resolver := new(MockPlanResolver)
	s.srv.SetIncomingMessageHandler(handler)
	s.srv.SetPlanResolver(resolver)

	s.store.On("MaxQueuedPriority", mock.Anything, "ch-1").Return(0, errors.New("db error"))
	called := make(chan struct{}, 1)
	handler.On("HandleIncomingMessageWithPriority", mock.Anything, "ch-1", "", planApprovePrompt, "", 0).
		Run(func(_ mock.Arguments) { called <- struct{}{} }).Return()
	resolver.On("ClearPlannedChannel", "ch-1").Return()
	resumed := make(chan struct{}, 1)
	resolver.On("ResumeChannel", mock.Anything, "ch-1").
		Run(func(_ mock.Arguments) { resumed <- struct{}{} }).Return()

	rec := s.testRequest("POST", "/api/channels/ch-1/plan/resolve", `{"action":"approve"}`)
	require.Equal(s.T(), http.StatusNoContent, rec.Code)

	s.awaitPlanCall("HandleIncomingMessageWithPriority", called)
	s.awaitPlanCall("ResumeChannel", resumed)
	handler.AssertExpectations(s.T())
	resolver.AssertExpectations(s.T())
}

func (s *ServerSuite) TestPlanResolveDeny() {
	handler := new(MockIncomingMessageHandler)
	resolver := new(MockPlanResolver)
	s.srv.SetIncomingMessageHandler(handler)
	s.srv.SetPlanResolver(resolver)

	s.store.On("MaxQueuedPriority", mock.Anything, "ch-1").Return(0, nil)
	called := make(chan struct{}, 1)
	handler.On("HandleIncomingMessageWithPriority", mock.Anything, "ch-1", "", "switch to redis", "plan", 1).
		Run(func(_ mock.Arguments) { called <- struct{}{} }).Return()
	resolver.On("ClearPlannedChannel", "ch-1").Return()
	resumed := make(chan struct{}, 1)
	resolver.On("ResumeChannel", mock.Anything, "ch-1").
		Run(func(_ mock.Arguments) { resumed <- struct{}{} }).Return()

	rec := s.testRequest("POST", "/api/channels/ch-1/plan/resolve",
		`{"action":"deny","prompt":"switch to redis","mode":"plan"}`)
	require.Equal(s.T(), http.StatusNoContent, rec.Code)

	s.awaitPlanCall("HandleIncomingMessageWithPriority", called)
	s.awaitPlanCall("ResumeChannel", resumed)
	handler.AssertExpectations(s.T())
	resolver.AssertExpectations(s.T())
}

func (s *ServerSuite) TestPlanResolveDenyMissingPrompt() {
	handler := new(MockIncomingMessageHandler)
	resolver := new(MockPlanResolver)
	s.srv.SetIncomingMessageHandler(handler)
	s.srv.SetPlanResolver(resolver)

	rec := s.testRequest("POST", "/api/channels/ch-1/plan/resolve", `{"action":"deny"}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)

	handler.AssertNotCalled(s.T(), "HandleIncomingMessageWithPriority")
	resolver.AssertNotCalled(s.T(), "ClearPlannedChannel")
}

func (s *ServerSuite) TestPlanResolveReject() {
	handler := new(MockIncomingMessageHandler)
	resolver := new(MockPlanResolver)
	s.srv.SetIncomingMessageHandler(handler)
	s.srv.SetPlanResolver(resolver)

	cleared := make(chan struct{}, 1)
	resumed := make(chan struct{}, 1)
	resolver.On("ClearPlannedChannel", "ch-1").
		Run(func(_ mock.Arguments) { cleared <- struct{}{} }).Return()
	resolver.On("ResumeChannel", mock.Anything, "ch-1").
		Run(func(_ mock.Arguments) { resumed <- struct{}{} }).Return()

	rec := s.testRequest("POST", "/api/channels/ch-1/plan/resolve", `{"action":"reject"}`)
	require.Equal(s.T(), http.StatusNoContent, rec.Code)

	s.awaitPlanCall("ClearPlannedChannel", cleared)
	s.awaitPlanCall("ResumeChannel", resumed)

	handler.AssertNotCalled(s.T(), "HandleIncomingMessageWithPriority")
	resolver.AssertExpectations(s.T())
}

func (s *ServerSuite) TestPlanResolveInvalidAction() {
	resolver := new(MockPlanResolver)
	s.srv.SetIncomingMessageHandler(new(MockIncomingMessageHandler))
	s.srv.SetPlanResolver(resolver)

	rec := s.testRequest("POST", "/api/channels/ch-1/plan/resolve", `{"action":"nope"}`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)

	resolver.AssertNotCalled(s.T(), "ClearPlannedChannel")
}

func (s *ServerSuite) TestPlanResolveBadJSON() {
	s.srv.SetIncomingMessageHandler(new(MockIncomingMessageHandler))
	s.srv.SetPlanResolver(new(MockPlanResolver))

	rec := s.testRequest("POST", "/api/channels/ch-1/plan/resolve", `not json`)
	require.Equal(s.T(), http.StatusBadRequest, rec.Code)
}

func (s *ServerSuite) TestPlanResolveNoResolverConfigured() {
	rec := s.testRequest("POST", "/api/channels/ch-1/plan/resolve", `{"action":"approve"}`)
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

func (s *ServerSuite) TestPlanResolveNoHandlerConfigured() {
	s.srv.SetPlanResolver(new(MockPlanResolver))
	// msgHandler not set
	rec := s.testRequest("POST", "/api/channels/ch-1/plan/resolve", `{"action":"approve"}`)
	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}
