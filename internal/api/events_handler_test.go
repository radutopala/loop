package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type EventsHandlerSuite struct {
	suite.Suite
}

func TestEventsHandlerSuite(t *testing.T) {
	suite.Run(t, new(EventsHandlerSuite))
}

func (s *EventsHandlerSuite) newServer() *Server {
	srv := nilServer()
	hub := NewEventsHub(testLogger())
	srv.SetEventsHub(hub)
	return srv
}

func (s *EventsHandlerSuite) TestEventsWSNotConfigured() {
	srv := nilServer()
	// No events hub set

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/ws", srv.handleEventsWS)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/ws", nil)
	mux.ServeHTTP(rec, req)

	require.Equal(s.T(), http.StatusNotImplemented, rec.Code)
}

func (s *EventsHandlerSuite) TestEventsWSReceivesEvents() {
	srv := s.newServer()

	ts := httptest.NewServer(http.HandlerFunc(srv.handleEventsWS))
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(s.T(), err)
	defer conn.Close()

	time.Sleep(50 * time.Millisecond)

	srv.eventsHub.BroadcastMessageCreated("ch-1", MessageData{
		MsgID:      "msg-1",
		AuthorName: "alice",
		Content:    "hello",
	})

	_, msg, err := conn.ReadMessage()
	require.NoError(s.T(), err)

	var evt Event
	require.NoError(s.T(), json.Unmarshal(msg, &evt))
	require.Equal(s.T(), "message.created", evt.Type)
	require.Equal(s.T(), "ch-1", evt.ChannelID)
}

func (s *EventsHandlerSuite) TestEventsWSChannelQueryParam() {
	srv := s.newServer()

	ts := httptest.NewServer(http.HandlerFunc(srv.handleEventsWS))
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "?channels=ch-2"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(s.T(), err)
	defer conn.Close()

	time.Sleep(50 * time.Millisecond)

	// Should not receive ch-1 events
	srv.eventsHub.BroadcastMessageCreated("ch-1", MessageData{Content: "skip"})

	// Should receive ch-2 events
	srv.eventsHub.BroadcastAgentStatus("ch-2", AgentStatusData{Status: "running"})

	_, msg, err := conn.ReadMessage()
	require.NoError(s.T(), err)

	var evt Event
	require.NoError(s.T(), json.Unmarshal(msg, &evt))
	require.Equal(s.T(), "agent.status", evt.Type)
	require.Equal(s.T(), "ch-2", evt.ChannelID)
}

func (s *EventsHandlerSuite) TestEventsWSSubscribeMessage() {
	srv := s.newServer()

	ts := httptest.NewServer(http.HandlerFunc(srv.handleEventsWS))
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(s.T(), err)
	defer conn.Close()

	time.Sleep(50 * time.Millisecond)

	// Subscribe to ch-3 only
	sub := eventSubscribeMessage{Type: "subscribe", Channels: []string{"ch-3"}}
	require.NoError(s.T(), conn.WriteJSON(sub))

	time.Sleep(50 * time.Millisecond)

	// ch-1 event should be filtered
	srv.eventsHub.BroadcastMessageCreated("ch-1", MessageData{Content: "skip"})

	// ch-3 event should arrive
	srv.eventsHub.BroadcastMessageCreated("ch-3", MessageData{Content: "pass"})

	_, msg, err := conn.ReadMessage()
	require.NoError(s.T(), err)

	var evt Event
	require.NoError(s.T(), json.Unmarshal(msg, &evt))
	require.Equal(s.T(), "ch-3", evt.ChannelID)
}

func (s *EventsHandlerSuite) TestEventsWSInvalidJSON() {
	srv := s.newServer()

	ts := httptest.NewServer(http.HandlerFunc(srv.handleEventsWS))
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(s.T(), err)
	defer conn.Close()

	// Send invalid JSON — should be silently ignored
	require.NoError(s.T(), conn.WriteMessage(websocket.TextMessage, []byte("not json")))

	time.Sleep(50 * time.Millisecond)

	// Connection should still work
	srv.eventsHub.BroadcastMessageCreated("ch-1", MessageData{Content: "still works"})

	_, msg, err := conn.ReadMessage()
	require.NoError(s.T(), err)

	var evt Event
	require.NoError(s.T(), json.Unmarshal(msg, &evt))
	require.Equal(s.T(), "ch-1", evt.ChannelID)
}

func (s *EventsHandlerSuite) TestEventsWSDisconnect() {
	srv := s.newServer()

	ts := httptest.NewServer(http.HandlerFunc(srv.handleEventsWS))
	defer ts.Close()

	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(s.T(), err)

	time.Sleep(50 * time.Millisecond)

	srv.eventsHub.mu.RLock()
	require.Len(s.T(), srv.eventsHub.subscribers, 1)
	srv.eventsHub.mu.RUnlock()

	conn.Close()
	time.Sleep(100 * time.Millisecond)

	// Trigger broadcast to clean up
	srv.eventsHub.BroadcastMessageCreated("ch-1", MessageData{Content: "cleanup"})
	time.Sleep(50 * time.Millisecond)

	srv.eventsHub.mu.RLock()
	require.Empty(s.T(), srv.eventsHub.subscribers)
	srv.eventsHub.mu.RUnlock()
}

func (s *EventsHandlerSuite) TestEventsWSUpgradeError() {
	srv := s.newServer()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/ws", srv.handleEventsWS)

	// Send a non-WebSocket request — upgrade will fail
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/ws", nil)
	mux.ServeHTTP(rec, req)

	// The handler returns without writing an HTTP error (upgrade handles it)
	require.NotEqual(s.T(), http.StatusSwitchingProtocols, rec.Code)
}

func (s *EventsHandlerSuite) TestSetEventsHub() {
	srv := nilServer()

	require.Nil(s.T(), srv.EventsHub())

	hub := NewEventsHub(testLogger())
	srv.SetEventsHub(hub)

	require.Same(s.T(), hub, srv.EventsHub())
}
