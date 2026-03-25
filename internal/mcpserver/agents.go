package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// WithAgentTools enables inter-agent communication tools and channel push notifications.
func WithAgentTools(agentID string) MemoryOption {
	return func(s *Server) {
		s.agentID = agentID
		s.channelTransport = newChannelTransport()
		// Tools are registered after mcpServer is created (in New).
	}
}

type listAgentsInput struct{}

type sendAgentMessageInput struct {
	ToAgentID string `json:"to_agent_id" jsonschema:"The agent ID to send the message to"`
	Content   string `json:"content" jsonschema:"The message content"`
}

type updateAgentStatusInput struct {
	Name        string `json:"name,omitempty" jsonschema:"New display name for this agent"`
	WorkSummary string `json:"work_summary,omitempty" jsonschema:"Brief description of current work"`
}

func (s *Server) registerAgentTools() {
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "list_agents",
		Description: "List all active agents in the current channel with their status and work summaries.",
	}, s.handleListAgents)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "send_agent_message",
		Description: "Send a message to another agent in this channel. The message will be delivered via push notification.",
	}, s.handleSendAgentMessage)

	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "update_agent_status",
		Description: "Update this agent's name and work summary. Other agents can see this via list_agents.",
	}, s.handleUpdateAgentStatus)
}

type agentListItem struct {
	AgentID     string `json:"agent_id"`
	Name        string `json:"name"`
	Status      string `json:"status"`
	WorkSummary string `json:"work_summary"`
}

func (s *Server) handleListAgents(_ context.Context, _ *mcp.CallToolRequest, _ listAgentsInput) (*mcp.CallToolResult, any, error) {
	agents, errResult, err := doAPICall[[]agentListItem](s, "GET", s.apiURL+"/api/agents?channel_id="+s.channelID, http.StatusOK, nil)
	if errResult != nil || err != nil {
		return errResult, nil, err
	}

	if len(*agents) == 0 {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "No agents active in this channel."}}}, nil, nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "%d agent(s):\n", len(*agents))
	for _, a := range *agents {
		marker := " "
		if a.AgentID == s.agentID {
			marker = "*"
		}
		fmt.Fprintf(&sb, "%s [%s] %s — %s", marker, a.AgentID, a.Name, a.Status)
		if a.WorkSummary != "" {
			fmt.Fprintf(&sb, " (%s)", a.WorkSummary)
		}
		sb.WriteString("\n")
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: sb.String()}}}, nil, nil
}

func (s *Server) handleSendAgentMessage(_ context.Context, _ *mcp.CallToolRequest, input sendAgentMessageInput) (*mcp.CallToolResult, any, error) {
	if input.ToAgentID == "" || input.Content == "" {
		return errorResult("to_agent_id and content are required"), nil, nil
	}

	reqBody, _ := json.Marshal(map[string]string{
		"channel_id":    s.channelID,
		"from_agent_id": s.agentID,
		"content":       input.Content,
	})

	_, status, err := s.doRequest("POST", s.apiURL+"/api/agents/"+input.ToAgentID+"/message", reqBody)
	if err != nil {
		return errorResult(fmt.Sprintf("failed to send message: %v", err)), nil, nil
	}
	if status >= 400 {
		return errorResult(fmt.Sprintf("send message failed: HTTP %d", status)), nil, nil
	}

	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Message sent to %s.", input.ToAgentID)}}}, nil, nil
}

func (s *Server) handleUpdateAgentStatus(_ context.Context, _ *mcp.CallToolRequest, input updateAgentStatusInput) (*mcp.CallToolResult, any, error) {
	if input.Name == "" && input.WorkSummary == "" {
		return errorResult("at least one of name or work_summary is required"), nil, nil
	}

	reqBody, _ := json.Marshal(map[string]string{
		"channel_id":   s.channelID,
		"name":         input.Name,
		"work_summary": input.WorkSummary,
	})

	_, status, err := s.doRequest("PATCH", s.apiURL+"/api/agents/"+s.agentID, reqBody)
	if err != nil {
		return errorResult(fmt.Sprintf("failed to update status: %v", err)), nil, nil
	}
	if status >= 400 {
		return errorResult(fmt.Sprintf("update failed: HTTP %d", status)), nil, nil
	}

	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "Agent status updated."}}}, nil, nil
}
