package api

import (
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/radutopala/loop/internal/container"
	"github.com/radutopala/loop/internal/events"
)

// Event type constants.
const (
	EventMessageCreated            = "message.created"
	EventMessageStreaming          = "message.streaming"
	EventMessagesProcessed         = "messages.processed"
	EventMessageDeleted            = "message.deleted"
	EventAgentStatus               = "agent.status"
	EventToolUse                   = "tool.use"
	EventToolResult                = "tool.result"
	EventAgentThinking             = "agent.thinking"
	EventAgentActivity             = "agent.activity"
	EventAskUser                   = "agent.ask_user"
	EventExitPlan                  = "agent.exit_plan"
	EventTodoWrite                 = "agent.todos"
	EventChannelCreated            = "channel.created"
	EventChannelDeleted            = "channel.deleted"
	EventAgentInstanceRegistered   = "agent_instance.registered"
	EventAgentInstanceUnregistered = "agent_instance.unregistered"
	EventAgentInstanceMetadata     = "agent_instance.metadata"
	EventImageBuildStatus          = "image.build_status"
	EventImageUpdateAvailable      = "image.update_available"
	EventPlaygroundUpdate          = "playground.update"
	EventContainerRegistered       = "container.registered"
	EventContainerRemoved          = "container.removed"
	EventContainerStatusChanged    = "container.status_changed"
	EventTaskCreated               = "task.created"
	EventTaskUpdated               = "task.updated"
	EventTaskDeleted               = "task.deleted"
	EventTaskRunCompleted          = "task.run_completed"
	EventTicketCreated             = "ticket.created"
	EventTicketUpdated             = "ticket.updated"
	EventTicketDeleted             = "ticket.deleted"
	EventWorkflowRunStarted        = "workflow.run_started"
	EventWorkflowRunCompleted      = "workflow.run_completed"
	EventWorkflowRunPaused         = "workflow.run_paused"
	EventWorkflowNodeStarted       = "workflow.node_started"
	EventWorkflowNodeCompleted     = "workflow.node_completed"
	EventGateApprovalRequested     = "gate.approval_requested"
	EventGateApprovalResolved      = "gate.approval_resolved"
	EventQualitySessionStarted     = "quality.session_started"
	EventQualitySessionEnded       = "quality.session_ended"
	EventQualityScanned            = "quality.scanned"
	EventQualityScanProgress       = "quality.scan_progress"
	EventQualityScanCancelled      = "quality.scan_cancelled"
	EventQualityRulesViolated      = "quality.rules_violated"
)

// Event represents a server-sent event to WebSocket clients.
type Event struct {
	Type      string `json:"type"`
	ChannelID string `json:"channel_id"`
	Data      any    `json:"data"`
	Timestamp int64  `json:"timestamp"`
	Global    bool   `json:"-"` // when true, bypass channel filtering
}

// EventsHub manages WebSocket event subscribers and broadcasts events.
//
// Thread-safety: The hub's subscriber set is protected by mu (RWMutex).
// Broadcast takes a snapshot of subscribers under RLock, then releases the lock
// before writing to each connection. Each connection has its own writeMu to
// serialize writes. This means a concurrent Unregister may remove a subscriber
// that Broadcast is about to write to — this is safe because writeMu still
// guards the connection, and a failed write triggers Unregister (idempotent
// delete from the map).
type EventsHub struct {
	mu          sync.RWMutex
	subscribers map[*eventConn]struct{}
	logger      *slog.Logger
	// captureHook is a test-only seam: when non-nil, every Broadcast invokes
	// it after marshalling so tests can observe events without registering
	// a real websocket. Production code never sets it.
	captureHook func(Event)
}

type eventConn struct {
	conn     *websocket.Conn
	channels map[string]struct{} // subscribed channel IDs; empty = all
	writeMu  sync.Mutex          // serializes writes to conn; held during Broadcast per-connection send
}

// NewEventsHub creates a new EventsHub.
func NewEventsHub(logger *slog.Logger) *EventsHub {
	return &EventsHub{
		subscribers: make(map[*eventConn]struct{}),
		logger:      logger,
	}
}

// Register adds a WebSocket connection as a subscriber.
func (h *EventsHub) Register(conn *websocket.Conn, channels []string) *eventConn {
	ec := &eventConn{
		conn:     conn,
		channels: make(map[string]struct{}, len(channels)),
	}
	for _, ch := range channels {
		ec.channels[ch] = struct{}{}
	}

	h.mu.Lock()
	h.subscribers[ec] = struct{}{}
	h.mu.Unlock()
	return ec
}

// Unregister removes a subscriber.
func (h *EventsHub) Unregister(ec *eventConn) {
	h.mu.Lock()
	delete(h.subscribers, ec)
	h.mu.Unlock()
}

// Broadcast sends an event to all subscribers whose channel filter matches.
func (h *EventsHub) Broadcast(evt Event) {
	evt.Timestamp = time.Now().UnixMilli()
	data, err := json.Marshal(evt)
	if err != nil {
		h.logger.Error("events hub: marshal failed", "error", err, "type", evt.Type)
		return
	}
	if h.captureHook != nil {
		h.captureHook(evt)
	}

	h.mu.RLock()
	subs := make([]*eventConn, 0, len(h.subscribers))
	for ec := range h.subscribers {
		subs = append(subs, ec)
	}
	h.mu.RUnlock()

	for _, ec := range subs {
		ec.writeMu.Lock()
		if len(ec.channels) > 0 && !evt.Global {
			if _, ok := ec.channels[evt.ChannelID]; !ok {
				ec.writeMu.Unlock()
				continue
			}
		}
		err := ec.conn.WriteMessage(websocket.TextMessage, data)
		ec.writeMu.Unlock()
		if err != nil {
			h.logger.Error("events hub: write failed, unregistering client", "error", err)
			h.Unregister(ec)
		}
	}
}

// BroadcastMessageCreated sends a message.created event.
func (h *EventsHub) BroadcastMessageCreated(channelID string, data events.MessageEventData) {
	h.Broadcast(Event{
		Type:      EventMessageCreated,
		ChannelID: channelID,
		Data:      data,
	})
}

// BroadcastMessagesProcessed sends a messages.processed event with the processed message IDs.
func (h *EventsHub) BroadcastMessagesProcessed(channelID string, data events.MessagesProcessedData) {
	h.Broadcast(Event{
		Type:      EventMessagesProcessed,
		ChannelID: channelID,
		Data:      data,
	})
}

// BroadcastMessageDeleted sends a message.deleted event for a queued message that was removed.
func (h *EventsHub) BroadcastMessageDeleted(channelID string, data events.MessageDeletedData) {
	h.Broadcast(Event{
		Type:      EventMessageDeleted,
		ChannelID: channelID,
		Data:      data,
	})
}

// BroadcastMessageStreaming sends a message.streaming event with partial bot response.
func (h *EventsHub) BroadcastMessageStreaming(channelID string, data events.MessageStreamingData) {
	h.Broadcast(Event{
		Type:      EventMessageStreaming,
		ChannelID: channelID,
		Data:      data,
	})
}

// BroadcastChannelCreated sends a channel.created event to all subscribers.
func (h *EventsHub) BroadcastChannelCreated(parentChannelID, channelID string) {
	h.Broadcast(Event{
		Type:      EventChannelCreated,
		ChannelID: parentChannelID,
		Data:      map[string]string{"channel_id": channelID},
		Global:    true,
	})
}

// BroadcastChannelDeleted sends a channel.deleted event to all subscribers.
func (h *EventsHub) BroadcastChannelDeleted(channelID string) {
	h.Broadcast(Event{
		Type:      EventChannelDeleted,
		ChannelID: channelID,
		Global:    true,
	})
}

// BroadcastAgentInstanceRegistered sends an agent_instance.registered event.
func (h *EventsHub) BroadcastAgentInstanceRegistered(channelID string, data events.AgentInstanceEventData) {
	h.Broadcast(Event{
		Type:      EventAgentInstanceRegistered,
		ChannelID: channelID,
		Data:      data,
	})
}

// BroadcastAgentInstanceUnregistered sends an agent_instance.unregistered event.
func (h *EventsHub) BroadcastAgentInstanceUnregistered(channelID string, data events.AgentInstanceEventData) {
	h.Broadcast(Event{
		Type:      EventAgentInstanceUnregistered,
		ChannelID: channelID,
		Data:      data,
	})
}

// BroadcastAgentInstanceMetadata sends an agent_instance.metadata event.
func (h *EventsHub) BroadcastAgentInstanceMetadata(channelID string, data events.AgentInstanceEventData) {
	h.Broadcast(Event{
		Type:      EventAgentInstanceMetadata,
		ChannelID: channelID,
		Data:      data,
	})
}

// BroadcastAgentStatus sends an agent.status event.
// When the event carries a ThreadID (scheduled task), it broadcasts to all
// subscribers so the frontend receives it even if not subscribed to the channel.
func (h *EventsHub) BroadcastAgentStatus(channelID string, data events.AgentStatusEventData) {
	h.Broadcast(Event{
		Type:      EventAgentStatus,
		ChannelID: channelID,
		Data:      data,
		Global:    data.ThreadID != "",
	})
}

// BroadcastToolUse sends a tool.use event.
func (h *EventsHub) BroadcastToolUse(channelID string, data events.ToolUseEventData) {
	h.Broadcast(Event{
		Type:      EventToolUse,
		ChannelID: channelID,
		Data:      data,
	})
}

// BroadcastAgentActivity sends an agent.activity event (model, subagent progress, etc.).
func (h *EventsHub) BroadcastAgentActivity(channelID string, data events.AgentActivityEventData) {
	h.Broadcast(Event{
		Type:      EventAgentActivity,
		ChannelID: channelID,
		Data:      data,
	})
}

// BroadcastAgentThinking sends an agent.thinking event with the assistant's
// extended-thinking content for the live UI.
func (h *EventsHub) BroadcastAgentThinking(channelID string, data events.AgentThinkingEventData) {
	h.Broadcast(Event{
		Type:      EventAgentThinking,
		ChannelID: channelID,
		Data:      data,
	})
}

// BroadcastToolResult sends a tool.result event with the (already-truncated)
// output of a single tool_result block.
func (h *EventsHub) BroadcastToolResult(channelID string, data events.ToolResultEventData) {
	h.Broadcast(Event{
		Type:      EventToolResult,
		ChannelID: channelID,
		Data:      data,
	})
}

// BroadcastAskUser sends an agent.ask_user event with structured questions.
func (h *EventsHub) BroadcastAskUser(channelID string, data events.AskUserQuestionEventData) {
	h.Broadcast(Event{
		Type:      EventAskUser,
		ChannelID: channelID,
		Data:      data,
	})
}

// BroadcastExitPlan sends an agent.exit_plan event when Claude wants to exit plan mode.
func (h *EventsHub) BroadcastExitPlan(channelID string, data events.ExitPlanModeEventData) {
	h.Broadcast(Event{
		Type:      EventExitPlan,
		ChannelID: channelID,
		Data:      data,
	})
}

// BroadcastTodoWrite sends an agent.todos event with the current todo list.
func (h *EventsHub) BroadcastTodoWrite(channelID string, data events.TodoWriteEventData) {
	h.Broadcast(Event{
		Type:      EventTodoWrite,
		ChannelID: channelID,
		Data:      data,
	})
}

// BroadcastImageBuildStatus sends a global image.build_status event.
func (h *EventsHub) BroadcastImageBuildStatus(data events.ImageBuildStatusData) {
	h.Broadcast(Event{
		Type:   EventImageBuildStatus,
		Data:   data,
		Global: true,
	})
}

// BroadcastImageUpdateAvailable sends a global image.update_available event.
func (h *EventsHub) BroadcastImageUpdateAvailable(data events.ImageUpdateAvailableData) {
	h.Broadcast(Event{
		Type:   EventImageUpdateAvailable,
		Data:   data,
		Global: true,
	})
}

// BroadcastContainerRegistered sends a global container.registered event.
func (h *EventsHub) BroadcastContainerRegistered(data container.ContainerEventData) {
	h.Broadcast(Event{
		Type:   EventContainerRegistered,
		Data:   data,
		Global: true,
	})
}

// BroadcastContainerRemoved sends a global container.removed event.
func (h *EventsHub) BroadcastContainerRemoved(data container.ContainerEventData) {
	h.Broadcast(Event{
		Type:   EventContainerRemoved,
		Data:   data,
		Global: true,
	})
}

// BroadcastContainerStatusChanged sends a global container.status_changed event.
func (h *EventsHub) BroadcastContainerStatusChanged(data container.ContainerEventData) {
	h.Broadcast(Event{
		Type:   EventContainerStatusChanged,
		Data:   data,
		Global: true,
	})
}

// BroadcastTaskCreated sends a global task.created event.
func (h *EventsHub) BroadcastTaskCreated(data events.TaskEventData) {
	h.Broadcast(Event{
		Type:   EventTaskCreated,
		Data:   data,
		Global: true,
	})
}

// BroadcastTaskUpdated sends a global task.updated event.
func (h *EventsHub) BroadcastTaskUpdated(data events.TaskEventData) {
	h.Broadcast(Event{
		Type:   EventTaskUpdated,
		Data:   data,
		Global: true,
	})
}

// BroadcastTaskDeleted sends a global task.deleted event.
func (h *EventsHub) BroadcastTaskDeleted(data events.TaskEventData) {
	h.Broadcast(Event{
		Type:   EventTaskDeleted,
		Data:   data,
		Global: true,
	})
}

// BroadcastTaskRunCompleted sends a global task.run_completed event.
func (h *EventsHub) BroadcastTaskRunCompleted(data events.TaskRunEventData) {
	h.Broadcast(Event{
		Type:   EventTaskRunCompleted,
		Data:   data,
		Global: true,
	})
}

// BroadcastTicketEvent sends a global ticket event (created or updated).
func (h *EventsHub) BroadcastTicketEvent(eventType, ticketID string) {
	h.Broadcast(Event{
		Type:   eventType,
		Data:   map[string]string{"ticket_id": ticketID},
		Global: true,
	})
}

// BroadcastWorkflowRunStarted sends a global workflow.run_started event.
func (h *EventsHub) BroadcastWorkflowRunStarted(data events.WorkflowRunEventData) {
	h.Broadcast(Event{
		Type:   EventWorkflowRunStarted,
		Data:   data,
		Global: true,
	})
}

// BroadcastWorkflowRunPaused sends a global workflow.run_paused event.
func (h *EventsHub) BroadcastWorkflowRunPaused(data events.WorkflowRunEventData) {
	h.Broadcast(Event{
		Type:   EventWorkflowRunPaused,
		Data:   data,
		Global: true,
	})
}

// BroadcastWorkflowRunCompleted sends a global workflow.run_completed event.
func (h *EventsHub) BroadcastWorkflowRunCompleted(data events.WorkflowRunEventData) {
	h.Broadcast(Event{
		Type:   EventWorkflowRunCompleted,
		Data:   data,
		Global: true,
	})
}

// BroadcastWorkflowNodeStarted sends a global workflow.node_started event.
func (h *EventsHub) BroadcastWorkflowNodeStarted(data events.WorkflowNodeEventData) {
	h.Broadcast(Event{
		Type:   EventWorkflowNodeStarted,
		Data:   data,
		Global: true,
	})
}

// BroadcastWorkflowNodeCompleted sends a global workflow.node_completed event.
func (h *EventsHub) BroadcastWorkflowNodeCompleted(data events.WorkflowNodeEventData) {
	h.Broadcast(Event{
		Type:   EventWorkflowNodeCompleted,
		Data:   data,
		Global: true,
	})
}

// BroadcastGateApprovalRequested sends a gate.approval_requested event to
// subscribers of the approving channel; the React ApprovalCard listens on
// this and renders the three gate decision buttons.
func (h *EventsHub) BroadcastGateApprovalRequested(channelID string, data events.GateApprovalEventData) {
	h.Broadcast(Event{
		Type:      EventGateApprovalRequested,
		ChannelID: channelID,
		Data:      data,
	})
}

// BroadcastGateApprovalResolved sends a gate.approval_resolved event after a
// decision is recorded, so any pending ApprovalCard can dismiss itself.
func (h *EventsHub) BroadcastGateApprovalResolved(channelID string, data events.GateApprovalResolvedData) {
	h.Broadcast(Event{
		Type:      EventGateApprovalResolved,
		ChannelID: channelID,
		Data:      data,
	})
}

// BroadcastQualityEvent sends a generic quality.* event scoped to channelID.
// One broadcaster covers all six quality event types — payload shape varies
// per type and is the handler's responsibility, so a single entry point
// keeps the hub surface small.
func (h *EventsHub) BroadcastQualityEvent(eventType, channelID string, data any) {
	h.Broadcast(Event{
		Type:      eventType,
		ChannelID: channelID,
		Data:      data,
	})
}
