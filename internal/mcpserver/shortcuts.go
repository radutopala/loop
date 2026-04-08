package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type shortcutInput struct {
	Action      string `json:"action" jsonschema:"required,Action: add (create new shortcut), update (modify existing), delete (remove by name), list (show all resolved shortcuts)"`
	Name        string `json:"name,omitempty" jsonschema:"Shortcut name (required for add/update/delete)"`
	Description string `json:"description,omitempty" jsonschema:"Human-readable description shown in the # picker"`
	Prompt      string `json:"prompt,omitempty" jsonschema:"Inline prompt text (mutually exclusive with prompt_path)"`
	PromptPath  string `json:"prompt_path,omitempty" jsonschema:"Path to prompt file relative to shortcuts/ dir (mutually exclusive with prompt)"`
	Scope       string `json:"scope,omitempty" jsonschema:"Storage scope: 'global' (default, ~/.loop/config.json) or 'project' (project .loop/config.json). Requires channel context for project scope."`
}

func (s *Server) handlePromptShortcut(_ context.Context, _ *mcp.CallToolRequest, input shortcutInput) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "prompt_shortcut", "action", input.Action, "name", input.Name, "scope", input.Scope)

	switch input.Action {
	case "list":
		return s.listShortcuts()
	case "add", "update", "delete":
		return s.modifyShortcut(input)
	default:
		return errorResult("action must be one of: list, add, update, delete"), nil, nil
	}
}

func (s *Server) listShortcuts() (*mcp.CallToolResult, any, error) {
	params := ""
	if s.channelID != "" {
		params = "?channel_id=" + url.QueryEscape(s.channelID)
	}
	apiURL := fmt.Sprintf("%s/api/shortcuts%s", s.apiURL, params)

	type shortcutItem struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Prompt      string `json:"prompt"`
	}

	respBody, status, err := s.doRequest("GET", apiURL, nil)
	if err != nil {
		return errorResult(fmt.Sprintf("calling API: %v", err)), nil, nil
	}
	if status != http.StatusOK {
		return errorResult(fmt.Sprintf("API error (status %d): %s", status, string(respBody))), nil, nil
	}
	var items []shortcutItem
	if err := json.Unmarshal(respBody, &items); err != nil {
		return errorResult(fmt.Sprintf("decoding response: %v", err)), nil, nil
	}
	if len(items) == 0 {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "No prompt shortcuts configured."}},
		}, nil, nil
	}
	out, _ := json.MarshalIndent(items, "", "  ")
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(out)}},
	}, nil, nil
}

func (s *Server) modifyShortcut(input shortcutInput) (*mcp.CallToolResult, any, error) {
	if input.Name == "" {
		return errorResult("name is required for " + input.Action), nil, nil
	}

	body := map[string]string{
		"action": input.Action,
		"name":   input.Name,
	}
	if input.Scope == "project" {
		body["scope"] = "project"
		if s.channelID != "" {
			body["channel_id"] = s.channelID
		}
	} else {
		body["scope"] = "global"
	}
	if input.Description != "" {
		body["description"] = input.Description
	}
	if input.Prompt != "" {
		body["prompt"] = input.Prompt
	}
	if input.PromptPath != "" {
		body["prompt_path"] = input.PromptPath
	}

	data, _ := json.Marshal(body)
	apiURL := fmt.Sprintf("%s/api/shortcuts", s.apiURL)

	if errResult, err := doAPICallNoBody(s, "POST", apiURL, http.StatusNoContent, data); errResult != nil || err != nil {
		return errResult, nil, err
	}

	scope := body["scope"]
	switch input.Action {
	case "add":
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Added shortcut %q (%s scope).", input.Name, scope)}},
		}, nil, nil
	case "update":
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Updated shortcut %q (%s scope).", input.Name, scope)}},
		}, nil, nil
	default: // delete
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Deleted shortcut %q (%s scope).", input.Name, scope)}},
		}, nil, nil
	}
}
