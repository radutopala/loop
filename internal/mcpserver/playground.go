package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type playgroundInput struct {
	Action      string `json:"action" jsonschema:"required,Action: create (new playground), update (modify existing), delete (remove entirely)"`
	Name        string `json:"name" jsonschema:"required,Playground name (e.g. 'snake-game'). Alphanumeric with hyphens and underscores."`
	Title       string `json:"title,omitempty" jsonschema:"Display title (required for create, optional for update)"`
	Description string `json:"description,omitempty" jsonschema:"What it does, controls, usage (required for create, optional for update). Saved as README.md body, supports markdown."`
	HTML        string `json:"html,omitempty" jsonschema:"HTML body content — the entry point (required for create, optional for update). No html/head/body tags."`
	Scope       string `json:"scope,omitempty" jsonschema:"Storage scope: 'global' (default, ~/.loop/playground/) or 'project' (project .loop/playground/). Requires channel_id for project scope."`
}

// playgroundScopeParams returns URL query params for scope routing.
func (s *Server) playgroundScopeParams(scope string) string {
	if scope == "project" && s.channelID != "" {
		return "&scope=project&channel_id=" + url.QueryEscape(s.channelID)
	}
	return ""
}

func (s *Server) handlePlayground(_ context.Context, _ *mcp.CallToolRequest, input playgroundInput) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "playground", "action", input.Action, "name", input.Name, "scope", input.Scope)

	apiURL := fmt.Sprintf("%s/api/playground?name=%s%s", s.apiURL, url.QueryEscape(input.Name), s.playgroundScopeParams(input.Scope))

	switch input.Action {
	case "create":
		if input.HTML == "" {
			return errorResult("html is required for create"), nil, nil
		}
		if input.Title == "" || input.Description == "" {
			return errorResult("title and description are required for create"), nil, nil
		}
		data, _ := json.Marshal(map[string]string{
			"html":        input.HTML,
			"title":       input.Title,
			"description": input.Description,
		})
		if errResult, err := doAPICallNoBody(s, "PUT", apiURL, http.StatusOK, data); errResult != nil || err != nil {
			return errResult, nil, err
		}
		return textResult(fmt.Sprintf("Playground '%s' created. Use playground_file to add script.js, style.css, and other files.", input.Name)), nil, nil

	case "update":
		payload := map[string]string{}
		if input.HTML != "" {
			payload["html"] = input.HTML
		}
		if input.Title != "" {
			payload["title"] = input.Title
		}
		if input.Description != "" {
			payload["description"] = input.Description
		}
		if len(payload) == 0 {
			return errorResult("at least one of html, title, or description is required for update"), nil, nil
		}
		data, _ := json.Marshal(payload)
		if errResult, err := doAPICallNoBody(s, "PUT", apiURL, http.StatusOK, data); errResult != nil || err != nil {
			return errResult, nil, err
		}
		return textResult(fmt.Sprintf("Playground '%s' updated.", input.Name)), nil, nil

	case "delete":
		if errResult, err := doAPICallNoBody(s, "DELETE", apiURL, http.StatusNoContent, nil); errResult != nil || err != nil {
			return errResult, nil, err
		}
		return textResult(fmt.Sprintf("Playground '%s' deleted.", input.Name)), nil, nil

	default:
		return errorResult("invalid action: " + input.Action), nil, nil
	}
}

type playgroundFileInput struct {
	Action  string `json:"action" jsonschema:"required,Action: create/update (write file), read (get content), delete (remove file), list (all files)"`
	Name    string `json:"name" jsonschema:"required,Playground name"`
	Path    string `json:"path,omitempty" jsonschema:"File path relative to playground root (e.g. 'script.js', 'lib/utils.js'). Required for create/update/read/delete."`
	Content string `json:"content,omitempty" jsonschema:"File content. Required for create/update."`
	Scope   string `json:"scope,omitempty" jsonschema:"Storage scope: 'global' (default) or 'project'. Must match the playground's scope."`
}

func (s *Server) handlePlaygroundFile(_ context.Context, _ *mcp.CallToolRequest, input playgroundFileInput) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "playground_file", "action", input.Action, "name", input.Name, "path", input.Path)

	baseURL := fmt.Sprintf("%s/api/playground", s.apiURL)
	nameParam := url.QueryEscape(input.Name)
	pathParam := url.QueryEscape(input.Path)
	scopeParams := s.playgroundScopeParams(input.Scope)

	switch input.Action {
	case "create", "update":
		if input.Path == "" {
			return errorResult("path is required"), nil, nil
		}
		if input.Content == "" {
			return errorResult("content is required"), nil, nil
		}
		apiURL := fmt.Sprintf("%s/file?name=%s&path=%s%s", baseURL, nameParam, pathParam, scopeParams)
		if errResult, err := doAPICallNoBody(s, "PUT", apiURL, http.StatusOK, []byte(input.Content)); errResult != nil || err != nil {
			return errResult, nil, err
		}
		return textResult(fmt.Sprintf("File '%s' written to playground '%s'.", input.Path, input.Name)), nil, nil

	case "read":
		if input.Path == "" {
			return errorResult("path is required"), nil, nil
		}
		apiURL := fmt.Sprintf("%s/file?name=%s&path=%s%s", baseURL, nameParam, pathParam, scopeParams)
		body, status, err := s.doRequest("GET", apiURL, nil)
		if err != nil {
			return errorResult(fmt.Sprintf("reading file: %v", err)), nil, nil
		}
		if status != http.StatusOK {
			return errorResult(fmt.Sprintf("read failed (%d): %s", status, strings.TrimSpace(string(body)))), nil, nil
		}
		return textResult(string(body)), nil, nil

	case "delete":
		if input.Path == "" {
			return errorResult("path is required"), nil, nil
		}
		apiURL := fmt.Sprintf("%s/file?name=%s&path=%s%s", baseURL, nameParam, pathParam, scopeParams)
		if errResult, err := doAPICallNoBody(s, "DELETE", apiURL, http.StatusNoContent, nil); errResult != nil || err != nil {
			return errResult, nil, err
		}
		return textResult(fmt.Sprintf("File '%s' deleted from playground '%s'.", input.Path, input.Name)), nil, nil

	case "list":
		apiURL := fmt.Sprintf("%s/files?name=%s%s", baseURL, nameParam, scopeParams)
		body, status, err := s.doRequest("GET", apiURL, nil)
		if err != nil {
			return errorResult(fmt.Sprintf("listing files: %v", err)), nil, nil
		}
		if status != http.StatusOK {
			return errorResult(fmt.Sprintf("list failed (%d): %s", status, strings.TrimSpace(string(body)))), nil, nil
		}
		var result struct {
			Files []string `json:"files"`
		}
		json.Unmarshal(body, &result) //nolint:errcheck
		if len(result.Files) == 0 {
			return textResult("No files in playground '" + input.Name + "'."), nil, nil
		}
		return textResult("Files in '" + input.Name + "':\n" + strings.Join(result.Files, "\n")), nil, nil

	default:
		return errorResult("invalid action: " + input.Action), nil, nil
	}
}

func textResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
	}
}
