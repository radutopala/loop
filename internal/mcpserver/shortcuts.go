package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// shortcutKind describes one of the two parallel shortcut APIs (prompt or bash).
type shortcutKind struct {
	display     string // "prompt shortcut" / "bash shortcut" — used in user-facing strings
	apiPath     string // "/api/shortcuts" / "/api/bash-shortcuts"
	inlineField string // "prompt" / "command" — payload key for inline body
	pathField   string // "prompt_path" / "command_path" — payload key for file-backed body
}

var (
	promptShortcutKind = shortcutKind{
		display:     "prompt shortcut",
		apiPath:     "/api/shortcuts",
		inlineField: "prompt",
		pathField:   "prompt_path",
	}
	bashShortcutKind = shortcutKind{
		display:     "bash shortcut",
		apiPath:     "/api/bash-shortcuts",
		inlineField: "command",
		pathField:   "command_path",
	}
)

// shortcutOp is the field-agnostic action payload shared by both tools.
type shortcutOp struct {
	Action      string
	Name        string
	Description string
	Inline      string
	Path        string
	Scope       string
}

type shortcutInput struct {
	Action      string `json:"action" jsonschema:"required,Action: add (create new shortcut), update (modify existing), delete (remove by name), list (show all resolved shortcuts)"`
	Name        string `json:"name,omitempty" jsonschema:"Shortcut name (required for add/update/delete)"`
	Description string `json:"description,omitempty" jsonschema:"Human-readable description shown in the # picker"`
	Prompt      string `json:"prompt,omitempty" jsonschema:"Inline prompt text (mutually exclusive with prompt_path)"`
	PromptPath  string `json:"prompt_path,omitempty" jsonschema:"Path to prompt file relative to shortcuts/ dir (mutually exclusive with prompt)"`
	Scope       string `json:"scope,omitempty" jsonschema:"Storage scope: 'global' (default, ~/.loop/config.json) or 'project' (project .loop/config.json). Requires channel context for project scope."`
}

type bashShortcutInput struct {
	Action      string `json:"action" jsonschema:"required,Action: add (create new shortcut), update (modify existing), delete (remove by name), list (show all resolved shortcuts)"`
	Name        string `json:"name,omitempty" jsonschema:"Shortcut name (required for add/update/delete)"`
	Description string `json:"description,omitempty" jsonschema:"Human-readable description shown in the $ picker"`
	Command     string `json:"command,omitempty" jsonschema:"Inline bash command text (mutually exclusive with command_path)"`
	CommandPath string `json:"command_path,omitempty" jsonschema:"Path to command file relative to bash-shortcuts/ dir (mutually exclusive with command)"`
	Scope       string `json:"scope,omitempty" jsonschema:"Storage scope: 'global' (default, ~/.loop/config.json) or 'project' (project .loop/config.json). Requires channel context for project scope."`
}

func (s *Server) handlePromptShortcut(_ context.Context, _ *mcp.CallToolRequest, input shortcutInput) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "prompt_shortcut", "action", input.Action, "name", input.Name, "scope", input.Scope)
	return s.dispatchShortcut(promptShortcutKind, shortcutOp{
		Action:      input.Action,
		Name:        input.Name,
		Description: input.Description,
		Inline:      input.Prompt,
		Path:        input.PromptPath,
		Scope:       input.Scope,
	})
}

func (s *Server) handleBashShortcut(_ context.Context, _ *mcp.CallToolRequest, input bashShortcutInput) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "bash_shortcut", "action", input.Action, "name", input.Name, "scope", input.Scope)
	return s.dispatchShortcut(bashShortcutKind, shortcutOp{
		Action:      input.Action,
		Name:        input.Name,
		Description: input.Description,
		Inline:      input.Command,
		Path:        input.CommandPath,
		Scope:       input.Scope,
	})
}

func (s *Server) dispatchShortcut(k shortcutKind, op shortcutOp) (*mcp.CallToolResult, any, error) {
	switch op.Action {
	case "list":
		return s.listShortcutsByKind(k)
	case "add", "update", "delete":
		return s.modifyShortcutByKind(k, op)
	default:
		return errorResult("action must be one of: list, add, update, delete"), nil, nil
	}
}

func (s *Server) listShortcutsByKind(k shortcutKind) (*mcp.CallToolResult, any, error) {
	params := ""
	if s.channelID != "" {
		params = "?channel_id=" + url.QueryEscape(s.channelID)
	}
	apiURL := fmt.Sprintf("%s%s%s", s.apiURL, k.apiPath, params)

	respBody, status, err := s.doRequest("GET", apiURL, nil)
	if err != nil {
		return errorResult(fmt.Sprintf("calling API: %v", err)), nil, nil
	}
	if status != http.StatusOK {
		return errorResult(fmt.Sprintf("API error (status %d): %s", status, string(respBody))), nil, nil
	}
	var items []map[string]any
	if err := json.Unmarshal(respBody, &items); err != nil {
		return errorResult(fmt.Sprintf("decoding response: %v", err)), nil, nil
	}
	if len(items) == 0 {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("No %ss configured.", k.display)}},
		}, nil, nil
	}
	out, _ := json.MarshalIndent(items, "", "  ")
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(out)}},
	}, nil, nil
}

func (s *Server) modifyShortcutByKind(k shortcutKind, op shortcutOp) (*mcp.CallToolResult, any, error) {
	if op.Name == "" {
		return errorResult("name is required for " + op.Action), nil, nil
	}

	body := map[string]string{
		"action": op.Action,
		"name":   op.Name,
	}
	if op.Scope == "project" {
		body["scope"] = "project"
		if s.channelID != "" {
			body["channel_id"] = s.channelID
		}
	} else {
		body["scope"] = "global"
	}
	if op.Description != "" {
		body["description"] = op.Description
	}
	if op.Inline != "" {
		body[k.inlineField] = op.Inline
	}
	if op.Path != "" {
		body[k.pathField] = op.Path
	}

	data, _ := json.Marshal(body)
	apiURL := fmt.Sprintf("%s%s", s.apiURL, k.apiPath)

	if errResult, err := doAPICallNoBody(s, "POST", apiURL, http.StatusNoContent, data); errResult != nil || err != nil {
		return errResult, nil, err
	}

	scope := body["scope"]
	switch op.Action {
	case "add":
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Added %s %q (%s scope).", k.display, op.Name, scope)}},
		}, nil, nil
	case "update":
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Updated %s %q (%s scope).", k.display, op.Name, scope)}},
		}, nil, nil
	default: // delete
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Deleted %s %q (%s scope).", k.display, op.Name, scope)}},
		}, nil, nil
	}
}
