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

type sessionsList struct {
	CurrentSessionID string `json:"current_session_id"`
	Sessions         []struct {
		SessionID    string    `json:"session_id"`
		LastModified time.Time `json:"last_modified"`
		LastMessage  string    `json:"last_message"`
	} `json:"sessions"`
	ImportedSessionIDs []string `json:"imported_session_ids"`
}

// sessionSnippet is how many characters of a session's last message
// list_sessions shows.
const sessionSnippet = 120

type listSessionsInput struct {
	ChannelID string `json:"channel_id,omitempty" jsonschema:"The channel or thread whose sessions to list. Optional — defaults to the current channel/thread this agent is running in."`
}

// handleListSessions lists the Claude sessions in a channel's project dir,
// like the Sessions panel: newest first, the channel's own marked.
func (s *Server) handleListSessions(_ context.Context, _ *mcp.CallToolRequest, input listSessionsInput) (*mcp.CallToolResult, any, error) {
	channelID := input.ChannelID
	if channelID == "" {
		channelID = s.channelID
	}
	s.logger.Info("mcp tool call", "tool", "list_sessions", "channel_id", channelID)

	if channelID == "" {
		return errorResult("channel_id is required"), nil, nil
	}
	list, errResult, err := doAPICall[sessionsList](s, "GET", s.apiURL+"/api/channels/"+url.PathEscape(channelID)+"/sessions", http.StatusOK, nil)
	if errResult != nil || err != nil {
		return errResult, nil, err
	}
	if len(list.Sessions) == 0 {
		return textResult("No sessions in this channel's project."), nil, nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%d session(s), newest first; * is the channel's current session, [thread] one a channel or thread already holds:\n", len(list.Sessions))
	for _, e := range list.Sessions {
		mark := ""
		switch {
		case e.SessionID == list.CurrentSessionID:
			mark = " *"
		case slices.Contains(list.ImportedSessionIDs, e.SessionID):
			mark = " [thread]"
		}
		text := strings.Join(strings.Fields(e.LastMessage), " ")
		if r := []rune(text); len(r) > sessionSnippet {
			text = string(r[:sessionSnippet]) + "…"
		}
		fmt.Fprintf(&b, "- %s%s %s %s\n", e.SessionID, mark, e.LastModified.UTC().Format(time.RFC3339), text)
	}
	return textResult(b.String()), nil, nil
}

type resumeSessionInput struct {
	SessionID string `json:"session_id" jsonschema:"The session to resume, from list_sessions"`
	ChannelID string `json:"channel_id,omitempty" jsonschema:"The channel or thread to switch. Optional — defaults to the current channel/thread this agent is running in."`
}

// handleResumeSession switches a channel to another of its project's
// sessions, like the Sessions panel's resume here.
func (s *Server) handleResumeSession(_ context.Context, _ *mcp.CallToolRequest, input resumeSessionInput) (*mcp.CallToolResult, any, error) {
	channelID := input.ChannelID
	if channelID == "" {
		channelID = s.channelID
	}
	s.logger.Info("mcp tool call", "tool", "resume_session", "channel_id", channelID, "session_id", input.SessionID)

	if channelID == "" {
		return errorResult("channel_id is required"), nil, nil
	}
	if input.SessionID == "" {
		return errorResult("session_id is required"), nil, nil
	}
	body, _ := json.Marshal(map[string]string{"session_id": input.SessionID})
	resp, errResult, err := doAPICall[struct {
		Deferred bool `json:"deferred"`
	}](s, "PUT", s.apiURL+"/api/channels/"+url.PathEscape(channelID)+"/session", http.StatusOK, body)
	if errResult != nil || err != nil {
		return errResult, nil, err
	}
	if resp.Deferred {
		return textResult(fmt.Sprintf("Session %s will become the channel's once its current run ends; the next message after that resumes it.", input.SessionID)), nil, nil
	}
	return textResult(fmt.Sprintf("Switched to session %s; the channel's next message resumes it.", input.SessionID)), nil, nil
}
