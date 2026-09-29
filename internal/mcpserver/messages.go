package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type sendMessageInput struct {
	ChannelID string `json:"channel_id,omitempty" jsonschema:"The channel or thread ID to send the message to. Optional — defaults to the current channel/thread this agent is running in when omitted."`
	Content   string `json:"content" jsonschema:"The message content to send"`
}

func (s *Server) handleSendMessage(_ context.Context, _ *mcp.CallToolRequest, input sendMessageInput) (*mcp.CallToolResult, any, error) {
	// channel_id is optional: an empty value targets the agent's own
	// channel/thread, matching every other channel-scoped tool in this
	// package (create_thread, tasks, quality_*, ...). This lets an agent
	// enqueue a follow-up into its own queue without first discovering its
	// channel id via search_channels.
	channelID := input.ChannelID
	if channelID == "" {
		channelID = s.channelID
	}

	s.logger.Info("mcp tool call", "tool", "send_message", "channel_id", channelID, "content", input.Content)

	if channelID == "" {
		return errorResult("channel_id is required"), nil, nil
	}
	if input.Content == "" {
		return errorResult("content is required"), nil, nil
	}

	data, _ := json.Marshal(map[string]string{
		"channel_id": channelID,
		"content":    input.Content,
	})

	if errResult, err := doAPICallNoBody(s, "POST", s.apiURL+"/api/messages", http.StatusNoContent, data); errResult != nil || err != nil {
		return errResult, nil, err
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: "Message sent successfully."},
		},
	}, nil, nil
}

type queueMessageInput struct {
	Content      string `json:"content" jsonschema:"The prompt to enqueue as a follow-up turn in the current channel/thread/worktree"`
	Interrupt    bool   `json:"interrupt,omitempty" jsonschema:"When true, jump the queue and cancel the active run so this prompt runs next. When false (default), the prompt waits behind any already-queued items. Ignored when delay_seconds is set."`
	DelaySeconds int    `json:"delay_seconds,omitempty" jsonschema:"Defer the prompt: wait this many seconds before it becomes eligible to run. 0 (default) enqueues it immediately. The chat UI shows a live countdown until it fires. Takes precedence over interrupt."`
}

// handleQueueMessage enqueues a follow-up prompt into the agent's OWN channel
// (s.channelID). It is a focused self-queue affordance: unlike send_message it
// never targets another channel, and it exposes the interrupt flag so an agent
// can either append a follow-up turn (default) or bump it to run next. The row
// lands in the same per-channel pending queue user messages flow through and
// shows up in the chat UI's queued-messages list.
func (s *Server) handleQueueMessage(_ context.Context, _ *mcp.CallToolRequest, input queueMessageInput) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "queue_message", "channel_id", s.channelID, "interrupt", input.Interrupt, "delay_seconds", input.DelaySeconds, "content", input.Content)

	if s.channelID == "" {
		return errorResult("queue_message is only available to channel-scoped agents"), nil, nil
	}
	if input.Content == "" {
		return errorResult("content is required"), nil, nil
	}
	if input.DelaySeconds < 0 {
		return errorResult("delay_seconds cannot be negative"), nil, nil
	}

	// A positive delay defers the prompt and overrides interrupt — a scheduled
	// follow-up can't also jump the active run.
	interrupt := input.Interrupt && input.DelaySeconds == 0
	data, _ := json.Marshal(map[string]any{
		"channel_id":    s.channelID,
		"content":       input.Content,
		"interrupt":     interrupt,
		"delay_seconds": input.DelaySeconds,
	})

	// A delayed prompt comes back with its id, so the agent can take it back
	// with delete_queued_message before it runs.
	if input.DelaySeconds > 0 {
		queued, errResult, err := doAPICall[queuedMessage](s, "POST", s.apiURL+"/api/messages", http.StatusOK, data)
		if errResult != nil || err != nil {
			return errResult, nil, err
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.TextContent{Text: fmt.Sprintf("Prompt queued with a %ds delay before it runs (msg_id: %s).", input.DelaySeconds, queued.MsgID)},
			},
		}, nil, nil
	}

	if errResult, err := doAPICallNoBody(s, "POST", s.apiURL+"/api/messages", http.StatusNoContent, data); errResult != nil || err != nil {
		return errResult, nil, err
	}

	msg := "Prompt queued in the current channel."
	if input.Interrupt {
		msg = "Prompt queued to run next (active run interrupted)."
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: msg},
		},
	}, nil, nil
}

// queuedMessage is a message waiting in a channel's queue, as the API lists it.
type queuedMessage struct {
	MsgID     string `json:"msg_id"`
	Content   string `json:"content"`
	IsRunning bool   `json:"is_running,omitempty"`
	NotBefore int64  `json:"not_before,omitempty"`
}

type queuedMessagesList struct {
	Messages []queuedMessage `json:"messages"`
}

// queuedSnippet is how many characters of a queued message list_queued_messages shows.
const queuedSnippet = 120

// listQueued returns the agent's own channel's queue, in run order.
func (s *Server) listQueued() ([]queuedMessage, *mcp.CallToolResult, error) {
	list, errResult, err := doAPICall[queuedMessagesList](s, "GET", s.apiURL+"/api/channels/"+url.PathEscape(s.channelID)+"/queued", http.StatusOK, nil)
	if errResult != nil || err != nil {
		return nil, errResult, err
	}
	return list.Messages, nil, nil
}

type listQueuedMessagesInput struct{}

// handleListQueuedMessages lists the messages waiting in the agent's own
// channel: its msg_id, whether it's running or when a delayed one runs, and
// the start of its text.
func (s *Server) handleListQueuedMessages(_ context.Context, _ *mcp.CallToolRequest, _ listQueuedMessagesInput) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "list_queued_messages", "channel_id", s.channelID)

	if s.channelID == "" {
		return errorResult("list_queued_messages is only available to channel-scoped agents"), nil, nil
	}
	msgs, errResult, err := s.listQueued()
	if errResult != nil || err != nil {
		return errResult, nil, err
	}
	if len(msgs) == 0 {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "No queued messages in the current channel."}}}, nil, nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%d queued message(s) in the current channel:\n", len(msgs))
	for _, m := range msgs {
		state := "queued"
		switch {
		case m.IsRunning:
			state = "running"
		case m.NotBefore > 0:
			state = "delayed until " + time.Unix(m.NotBefore, 0).UTC().Format(time.RFC3339)
		}
		text := strings.Join(strings.Fields(m.Content), " ")
		if r := []rune(text); len(r) > queuedSnippet {
			text = string(r[:queuedSnippet]) + "…"
		}
		fmt.Fprintf(&b, "- %s [%s] %s\n", m.MsgID, state, text)
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: b.String()}}}, nil, nil
}

type deleteQueuedMessageInput struct {
	MsgID string `json:"msg_id" jsonschema:"The msg_id of the queued message to remove, from queue_message's result or list_queued_messages"`
}

// handleDeleteQueuedMessage removes a message waiting in the agent's own
// channel, like the chat's Remove from queue. The running message, the one the
// agent is answering, can't be removed.
func (s *Server) handleDeleteQueuedMessage(_ context.Context, _ *mcp.CallToolRequest, input deleteQueuedMessageInput) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "delete_queued_message", "channel_id", s.channelID, "msg_id", input.MsgID)

	if s.channelID == "" {
		return errorResult("delete_queued_message is only available to channel-scoped agents"), nil, nil
	}
	if input.MsgID == "" {
		return errorResult("msg_id is required"), nil, nil
	}
	// The store's delete only checks the row isn't processed, which a running
	// one isn't yet: look it up first.
	msgs, errResult, err := s.listQueued()
	if errResult != nil || err != nil {
		return errResult, nil, err
	}
	i := slices.IndexFunc(msgs, func(m queuedMessage) bool { return m.MsgID == input.MsgID })
	if i < 0 {
		return errorResult(fmt.Sprintf("no queued message %s in the current channel (it may have run already)", input.MsgID)), nil, nil
	}
	if msgs[i].IsRunning {
		return errorResult(fmt.Sprintf("message %s is already running", input.MsgID)), nil, nil
	}

	u := s.apiURL + "/api/messages/" + url.PathEscape(input.MsgID) + "?channel_id=" + url.QueryEscape(s.channelID)
	if errResult, err := doAPICallNoBody(s, "DELETE", u, http.StatusNoContent, nil); errResult != nil || err != nil {
		return errResult, nil, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Removed queued message %s.", input.MsgID)}}}, nil, nil
}
