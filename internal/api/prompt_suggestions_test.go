package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/events"
)

type PromptSuggestionsSuite struct {
	suite.Suite
	srv *Server
	hub *EventsHub
	mux *http.ServeMux
}

func TestPromptSuggestionsSuite(t *testing.T) {
	suite.Run(t, new(PromptSuggestionsSuite))
}

func (s *PromptSuggestionsSuite) SetupTest() {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s.srv = NewServer(nil, nil, nil, nil, nil, logger)
	s.hub = NewEventsHub(logger)
	s.srv.SetEventsHub(s.hub)
	s.mux = http.NewServeMux()
	s.mux.HandleFunc("GET /api/channels/{id}/prompt-suggestion", s.srv.handlePromptSuggestion)
}

// suggestion fetches channelID's suggestion through the endpoint.
func (s *PromptSuggestionsSuite) suggestion(channelID string) string {
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/channels/"+channelID+"/prompt-suggestion", nil))
	require.Equal(s.T(), http.StatusOK, rec.Code)
	var resp promptSuggestionResponse
	require.NoError(s.T(), json.Unmarshal(rec.Body.Bytes(), &resp))
	return resp.Suggestion
}

func (s *PromptSuggestionsSuite) TestKeepsLatestUntilRunStarts() {
	tests := []struct {
		name  string
		after func(h *EventsHub)
		want  string
	}{
		{name: "kept", after: func(*EventsHub) {}, want: "run the tests"},
		{name: "a newer one replaces it", after: func(h *EventsHub) {
			h.BroadcastPromptSuggestion("ch1", events.PromptSuggestionEventData{Suggestion: " commit it "})
		}, want: "commit it"},
		{name: "a blank one keeps it", after: func(h *EventsHub) {
			h.BroadcastPromptSuggestion("ch1", events.PromptSuggestionEventData{Suggestion: "  "})
		}, want: "run the tests"},
		{name: "its run starting drops it", after: func(h *EventsHub) {
			h.BroadcastAgentStatus("ch1", events.AgentStatusEventData{Status: "running"})
		}, want: ""},
		{name: "its own thread's run starting drops it", after: func(h *EventsHub) {
			h.BroadcastAgentStatus("ch1", events.AgentStatusEventData{Status: "running", ThreadID: "ch1"})
		}, want: ""},
		{name: "a thread's run told to the parent keeps it", after: func(h *EventsHub) {
			h.BroadcastAgentStatus("ch1", events.AgentStatusEventData{Status: "running", ThreadID: "th1"})
		}, want: "run the tests"},
		{name: "a run ending keeps it", after: func(h *EventsHub) {
			h.BroadcastAgentStatus("ch1", events.AgentStatusEventData{Status: "completed"})
		}, want: "run the tests"},
		{name: "another channel's run keeps it", after: func(h *EventsHub) {
			h.BroadcastAgentStatus("ch2", events.AgentStatusEventData{Status: "running"})
		}, want: "run the tests"},
		{name: "deleting the channel drops it", after: func(h *EventsHub) {
			h.BroadcastChannelDeleted("ch1")
		}, want: ""},
		{name: "other events keep it", after: func(h *EventsHub) {
			h.BroadcastToolUse("ch1", events.ToolUseEventData{ToolName: "Bash"})
		}, want: "run the tests"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			s.SetupTest()
			s.hub.BroadcastPromptSuggestion("ch1", events.PromptSuggestionEventData{Suggestion: "run the tests"})
			tt.after(s.hub)
			require.Equal(s.T(), tt.want, s.suggestion("ch1"))
		})
	}
}

func (s *PromptSuggestionsSuite) TestNoneForChannelWithout() {
	s.hub.BroadcastPromptSuggestion("ch1", events.PromptSuggestionEventData{Suggestion: "run the tests"})
	require.Empty(s.T(), s.suggestion("ch2"))
}
