package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type chatComponentInput struct {
	Action   string `json:"action" jsonschema:"required,Action: templates (list the templates and how to fill them — call it first), show (render a component in the chat)"`
	Template string `json:"template,omitempty" jsonschema:"Template name for show, e.g. math or canvas"`
	Title    string `json:"title,omitempty" jsonschema:"Short plain-text title shown above the component; write < > & as they are, not as HTML entities"`
	HTML     string `json:"html,omitempty" jsonschema:"HTML fragment placed in the template (not a full page)"`
	CSS      string `json:"css,omitempty" jsonschema:"Optional CSS for the component"`
	JS       string `json:"js,omitempty" jsonschema:"Optional JS, run as an ES module after the html; can import from a CDN such as https://esm.sh"`
}

func (s *Server) handleChatComponent(_ context.Context, _ *mcp.CallToolRequest, input chatComponentInput) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "chat_component", "action", input.Action, "template", input.Template, "title", input.Title)
	query := "?channel_id=" + url.QueryEscape(s.channelID)
	switch input.Action {
	case "templates":
		respBody, status, err := s.doRequest("GET", s.apiURL+"/api/components"+query+"&format=guide", nil)
		if err != nil {
			return errorResult(fmt.Sprintf("calling API: %v", err)), nil, nil
		}
		if status != http.StatusOK {
			return errorResult(fmt.Sprintf("API error (status %d): %s", status, string(respBody))), nil, nil
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(respBody)}}}, nil, nil
	case "show":
		if input.Template == "" {
			return errorResult("template is required for show; call the templates action to see them"), nil, nil
		}
		body, _ := json.Marshal(map[string]string{
			"template": input.Template,
			"title":    input.Title,
			"html":     input.HTML,
			"css":      input.CSS,
			"js":       input.JS,
		})
		respBody, status, err := s.doRequest("POST", s.apiURL+"/api/components"+query, body)
		if err != nil {
			return errorResult(fmt.Sprintf("calling API: %v", err)), nil, nil
		}
		if status != http.StatusCreated {
			return errorResult(fmt.Sprintf("API error (status %d): %s", status, string(respBody))), nil, nil
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Component %q is shown in the chat. Don't repeat its content in your reply.", input.Template)}},
		}, nil, nil
	default:
		return errorResult("action must be one of: templates, show"), nil, nil
	}
}
