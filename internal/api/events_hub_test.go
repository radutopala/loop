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

	"github.com/radutopala/loop/internal/events"
)

type EventsHubSuite struct {
	suite.Suite
}

func TestEventsHubSuite(t *testing.T) {
	suite.Run(t, new(EventsHubSuite))
}

func (s *EventsHubSuite) TestNewEventsHub() {
	hub := NewEventsHub(testLogger())
	require.NotNil(s.T(), hub)
	require.NotNil(s.T(), hub.subscribers)
}

func (s *EventsHubSuite) TestRegisterUnregister() {
	hub := NewEventsHub(testLogger())

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		ec := hub.Register(conn, []string{"ch-1"})
		defer hub.Unregister(ec)

		hub.mu.RLock()
		require.Len(s.T(), hub.subscribers, 1)
		hub.mu.RUnlock()
		// Keep connection open briefly
		time.Sleep(100 * time.Millisecond)
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(s.T(), err)
	defer conn.Close()

	time.Sleep(200 * time.Millisecond)

	hub.mu.RLock()
	require.Empty(s.T(), hub.subscribers)
	hub.mu.RUnlock()
}

func (s *EventsHubSuite) TestBroadcastToAll() {
	hub := NewEventsHub(testLogger())

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		// Subscribe to all channels (empty filter)
		hub.Register(conn, nil)
		select {}
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(s.T(), err)
	defer conn.Close()

	time.Sleep(50 * time.Millisecond)

	hub.BroadcastMessageCreated("ch-1", events.MessageEventData{
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
	require.Greater(s.T(), evt.Timestamp, int64(0))
}

func (s *EventsHubSuite) TestBroadcastFilteredByChannel() {
	hub := NewEventsHub(testLogger())

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		hub.Register(conn, []string{"ch-2"})
		select {}
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(s.T(), err)
	defer conn.Close()

	time.Sleep(50 * time.Millisecond)

	// Send to ch-1 — subscriber is on ch-2, should not receive
	hub.BroadcastMessageCreated("ch-1", events.MessageEventData{Content: "nope"})

	// Send to ch-2 — subscriber should receive
	hub.BroadcastAgentStatus("ch-2", events.AgentStatusEventData{Status: "running"})

	_, msg, err := conn.ReadMessage()
	require.NoError(s.T(), err)

	var evt Event
	require.NoError(s.T(), json.Unmarshal(msg, &evt))
	require.Equal(s.T(), "agent.status", evt.Type)
	require.Equal(s.T(), "ch-2", evt.ChannelID)
}

func (s *EventsHubSuite) TestBroadcastAgentStatus() {
	hub := NewEventsHub(testLogger())

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		hub.Register(conn, nil)
		select {}
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(s.T(), err)
	defer conn.Close()

	time.Sleep(50 * time.Millisecond)

	hub.BroadcastAgentStatus("ch-1", events.AgentStatusEventData{Status: "error", Error: "boom"})

	_, msg, err := conn.ReadMessage()
	require.NoError(s.T(), err)

	var evt Event
	require.NoError(s.T(), json.Unmarshal(msg, &evt))
	require.Equal(s.T(), "agent.status", evt.Type)
	require.Equal(s.T(), "ch-1", evt.ChannelID)
}

func (s *EventsHubSuite) TestBroadcastAgentStatusWithThreadIDBypassesFilter() {
	hub := NewEventsHub(testLogger())

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		// Subscribe to ch-other only — NOT the task's channel
		hub.Register(conn, []string{"ch-other"})
		select {}
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(s.T(), err)
	defer conn.Close()

	time.Sleep(50 * time.Millisecond)

	// ThreadID set — should bypass channel filter and reach subscriber
	hub.BroadcastAgentStatus("ch-task-parent", events.AgentStatusEventData{
		Status:   "completed",
		ThreadID: "task-thread-1",
	})

	_, msg, err := conn.ReadMessage()
	require.NoError(s.T(), err)

	var evt Event
	require.NoError(s.T(), json.Unmarshal(msg, &evt))
	require.Equal(s.T(), "agent.status", evt.Type)
	require.Equal(s.T(), "ch-task-parent", evt.ChannelID)
}

func (s *EventsHubSuite) TestBroadcastToolUse() {
	hub := NewEventsHub(testLogger())

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		hub.Register(conn, nil)
		select {}
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(s.T(), err)
	defer conn.Close()

	time.Sleep(50 * time.Millisecond)

	hub.BroadcastToolUse("ch-1", events.ToolUseEventData{ToolName: "Bash", Input: "go test"})

	_, msg, err := conn.ReadMessage()
	require.NoError(s.T(), err)

	var evt Event
	require.NoError(s.T(), json.Unmarshal(msg, &evt))
	require.Equal(s.T(), "tool.use", evt.Type)
	require.Equal(s.T(), "ch-1", evt.ChannelID)
}

func (s *EventsHubSuite) TestBroadcastAgentActivity() {
	hub := NewEventsHub(testLogger())

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		hub.Register(conn, nil)
		select {}
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(s.T(), err)
	defer conn.Close()

	time.Sleep(50 * time.Millisecond)

	hub.BroadcastAgentActivity("ch-1", events.AgentActivityEventData{Activity: "model", Model: "claude-opus-4-6"})

	_, msg, err := conn.ReadMessage()
	require.NoError(s.T(), err)

	var evt Event
	require.NoError(s.T(), json.Unmarshal(msg, &evt))
	require.Equal(s.T(), "agent.activity", evt.Type)
	require.Equal(s.T(), "ch-1", evt.ChannelID)
}

func (s *EventsHubSuite) TestBroadcastMessageStreaming() {
	hub := NewEventsHub(testLogger())

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		hub.Register(conn, nil)
		select {}
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(s.T(), err)
	defer conn.Close()

	time.Sleep(50 * time.Millisecond)

	hub.BroadcastMessageStreaming("ch-1", events.MessageStreamingData{Content: "partial..."})

	_, msg, err := conn.ReadMessage()
	require.NoError(s.T(), err)

	var evt Event
	require.NoError(s.T(), json.Unmarshal(msg, &evt))
	require.Equal(s.T(), "message.streaming", evt.Type)
	require.Equal(s.T(), "ch-1", evt.ChannelID)
}

func (s *EventsHubSuite) TestBroadcastChannelCreated() {
	hub := NewEventsHub(testLogger())

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		hub.Register(conn, nil)
		select {}
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(s.T(), err)
	defer conn.Close()

	time.Sleep(50 * time.Millisecond)

	hub.BroadcastChannelCreated("parent-1", "thread-1")

	_, msg, err := conn.ReadMessage()
	require.NoError(s.T(), err)

	var evt Event
	require.NoError(s.T(), json.Unmarshal(msg, &evt))
	require.Equal(s.T(), "channel.created", evt.Type)
	require.Equal(s.T(), "parent-1", evt.ChannelID)
}

func (s *EventsHubSuite) TestBroadcastChannelDeleted() {
	hub := NewEventsHub(testLogger())

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		hub.Register(conn, nil)
		select {}
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(s.T(), err)
	defer conn.Close()

	time.Sleep(50 * time.Millisecond)

	hub.BroadcastChannelDeleted("ch-1")

	_, msg, err := conn.ReadMessage()
	require.NoError(s.T(), err)

	var evt Event
	require.NoError(s.T(), json.Unmarshal(msg, &evt))
	require.Equal(s.T(), "channel.deleted", evt.Type)
	require.Equal(s.T(), "ch-1", evt.ChannelID)
}

func (s *EventsHubSuite) TestBroadcastRemovesClosedConnections() {
	hub := NewEventsHub(testLogger())

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		hub.Register(conn, nil)
		select {}
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(s.T(), err)

	time.Sleep(50 * time.Millisecond)

	hub.mu.RLock()
	require.Len(s.T(), hub.subscribers, 1)
	hub.mu.RUnlock()

	// Close the client side — broadcast attempts should eventually detect the write error.
	conn.Close()
	time.Sleep(50 * time.Millisecond)

	// May take more than one broadcast for the write error to surface.
	for range 5 {
		hub.BroadcastMessageCreated("ch-1", events.MessageEventData{Content: "test"})
		time.Sleep(50 * time.Millisecond)
	}

	hub.mu.RLock()
	require.Empty(s.T(), hub.subscribers)
	hub.mu.RUnlock()
}

func (s *EventsHubSuite) TestBroadcastNoSubscribers() {
	hub := NewEventsHub(testLogger())

	// Broadcast with zero subscribers — must not panic or leak.
	hub.BroadcastMessageCreated("ch-1", events.MessageEventData{Content: "hello"})
	hub.BroadcastAgentStatus("ch-1", events.AgentStatusEventData{Status: "running"})

	hub.mu.RLock()
	require.Empty(s.T(), hub.subscribers)
	hub.mu.RUnlock()
}

func (s *EventsHubSuite) TestBroadcastAskUser() {
	hub := NewEventsHub(testLogger())

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		hub.Register(conn, nil)
		select {}
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(s.T(), err)
	defer conn.Close()

	time.Sleep(50 * time.Millisecond)

	hub.BroadcastAskUser("ch-1", events.AskUserQuestionEventData{
		Questions: []events.AskUserQuestion{
			{Question: "What next?", Header: "Task", Options: []events.AskUserOption{{Label: "A"}, {Label: "B"}}},
		},
	})

	_, msg, err := conn.ReadMessage()
	require.NoError(s.T(), err)

	var evt Event
	require.NoError(s.T(), json.Unmarshal(msg, &evt))
	require.Equal(s.T(), "agent.ask_user", evt.Type)
	require.Equal(s.T(), "ch-1", evt.ChannelID)
}

func (s *EventsHubSuite) TestBroadcastExitPlan() {
	hub := NewEventsHub(testLogger())

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		hub.Register(conn, nil)
		select {}
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(s.T(), err)
	defer conn.Close()

	time.Sleep(50 * time.Millisecond)

	hub.BroadcastExitPlan("ch-1", events.ExitPlanModeEventData{Plan: "# My Plan", PlanFilePath: "/tmp/plan.md"})

	_, msg, err := conn.ReadMessage()
	require.NoError(s.T(), err)

	var evt Event
	require.NoError(s.T(), json.Unmarshal(msg, &evt))
	require.Equal(s.T(), "agent.exit_plan", evt.Type)
	require.Equal(s.T(), "ch-1", evt.ChannelID)
}

func (s *EventsHubSuite) TestBroadcastMessagesProcessed() {
	hub := NewEventsHub(testLogger())

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		hub.Register(conn, nil)
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(s.T(), err)
	defer conn.Close()

	time.Sleep(50 * time.Millisecond)

	hub.BroadcastMessagesProcessed("ch-1", events.MessagesProcessedData{
		MsgIDs: []string{"msg-1", "msg-2"},
	})

	_, msg, err := conn.ReadMessage()
	require.NoError(s.T(), err)

	var evt Event
	require.NoError(s.T(), json.Unmarshal(msg, &evt))
	require.Equal(s.T(), "messages.processed", evt.Type)
	require.Equal(s.T(), "ch-1", evt.ChannelID)
}

func (s *EventsHubSuite) TestBroadcastMarshalError() {
	hub := NewEventsHub(testLogger())

	// Use a Data value that cannot be marshaled to trigger the marshal error path.
	hub.Broadcast(Event{
		Type:      "test",
		ChannelID: "ch-1",
		Data:      func() {}, // functions cannot be marshaled to JSON
	})
}
