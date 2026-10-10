package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type uiRunInput struct {
	Steps    []uiRunStep `json:"steps" jsonschema:"The steps, run in order until one fails"`
	ClientID string      `json:"client_id,omitempty" jsonschema:"The window (from ui_state); empty for the one the user last focused"`
	Timeout  string      `json:"timeout,omitempty" jsonschema:"How long the steps may take, as a Go duration (default 1m, at most 10m)"`
}

// uiRunStep is a step of ui_run, as the app's window runs it. Pointers keep
// a false or 0 the agent gives.
type uiRunStep struct {
	Op        string `json:"op" jsonschema:"select_channel {channel_id}, set_tab {tab}, create_tab {tab?}, rename_tab {tab, name}, remove_tab {tab}, replace_pane {pane, panel, open_mode?, item?, scope?}, add_pane {panel, next_to?, direction?, side?, open_mode?, item?, scope?}, remove_pane {pane}, maximize_pane {pane}, restore_pane, open_file {path, line?}, send_input {pane, text, submit?}, read_output {pane, lines?}, wait_for {pane, match?, quiet_ms?, lines?}"`
	ChannelID string `json:"channel_id,omitempty" jsonschema:"select_channel: a channel, thread or worktree thread of this project"`
	Tab       string `json:"tab,omitempty" jsonschema:"set_tab, rename_tab, remove_tab: a layout tab (from ui_state); create_tab: the new tab's name (default the next Layout N)"`
	Name      string `json:"name,omitempty" jsonschema:"rename_tab: the tab's new name"`
	Pane      string `json:"pane,omitempty" jsonschema:"A pane id (from ui_state), or a panel type for its first pane"`
	Panel     string `json:"panel,omitempty" jsonschema:"replace_pane, add_pane: the new pane's panel"`
	OpenMode  string `json:"open_mode,omitempty" jsonschema:"replace_pane, add_pane of a docker-agent: resume the channel's session, fork it (the default) or start fresh"`
	NextTo    string `json:"next_to,omitempty" jsonschema:"add_pane: the pane (id or panel type) the new one goes beside; without it, the new pane spans the tab's edge"`
	Direction string `json:"direction,omitempty" jsonschema:"add_pane: horizontal makes a column, vertical a row (default horizontal); beside a pane in a row of columns (or a column of rows) it joins that row with an equal share"`
	Side      string `json:"side,omitempty" jsonschema:"add_pane: after puts the new pane right of or below next_to (or the tab), before left of or above it (default after)"`
	Item      string `json:"item,omitempty" jsonschema:"replace_pane, add_pane of a playground: the playground it shows"`
	Scope     string `json:"scope,omitempty" jsonschema:"With item: the playground's scope, when both have one by that name"`
	Path      string `json:"path,omitempty" jsonschema:"open_file: a path in one of the channel's roots"`
	Line      *int   `json:"line,omitempty" jsonschema:"open_file: the line to show, from 1"`
	Text      string `json:"text,omitempty" jsonschema:"send_input: what to type"`
	Submit    *bool  `json:"submit,omitempty" jsonschema:"send_input: whether to press enter after the text (default true)"`
	Lines     *int   `json:"lines,omitempty" jsonschema:"read_output, wait_for: how many of the terminal's last lines (default 50, at most 2000)"`
	Match     string `json:"match,omitempty" jsonschema:"wait_for: a regular expression (multiline, RE2 syntax: no lookarounds or backreferences) the last lines must match"`
	QuietMS   *int   `json:"quiet_ms,omitempty" jsonschema:"wait_for without match: how long the terminal must be quiet, in ms (default 2000)"`
}

// uiOps are the ops of a ui_run step; uiPanels are the panels a pane step
// makes. Keep them in sync with the app's executor and PANEL_OPTIONS
// (app/src/uiBridge/schemaSync.test.ts checks).
var (
	uiOps = []any{
		"select_channel", "set_tab", "create_tab", "rename_tab", "remove_tab",
		"replace_pane", "add_pane", "remove_pane", "maximize_pane", "restore_pane",
		"open_file", "send_input", "read_output", "wait_for",
	}
	uiPanels = []any{
		"chat", "editor", "file-tree", "memory", "git", "docker-agent", "docker-shell", "host-shell",
		"docker-browser", "host-browser", "sessions", "playground", "notes", "tasks", "kanban",
		"workflows", "audit", "quality", "review",
	}
)

// uiRunSchema is ui_run's input schema: the one inferred from uiRunInput,
// with the fields that take one of a few values listed.
func uiRunSchema() *jsonschema.Schema {
	schema, _ := jsonschema.For[uiRunInput](nil) // a struct of plain fields infers
	schema.Properties["steps"].MinItems = jsonschema.Ptr(1)
	step := schema.Properties["steps"].Items.Properties
	step["op"].Enum = uiOps
	step["panel"].Enum = uiPanels
	step["open_mode"].Enum = []any{"resume", "fork", "fresh"}
	step["direction"].Enum = []any{"horizontal", "vertical"}
	step["side"].Enum = []any{"before", "after"}
	step["scope"].Enum = []any{"global", "project"}
	return schema
}

func (s *Server) handleUIState(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "ui_state")
	respBody, status, err := s.doRequest("GET", s.apiURL+"/api/ui/state", nil)
	if err != nil {
		return errorResult(fmt.Sprintf("calling API: %v", err)), nil, nil
	}
	if status != http.StatusOK {
		return errorResult(fmt.Sprintf("API error (status %d): %s", status, string(respBody))), nil, nil
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(respBody)}}}, nil, nil
}

func (s *Server) handleUIRun(_ context.Context, _ *mcp.CallToolRequest, input uiRunInput) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "ui_run", "steps", len(input.Steps))
	// The daemon has an agent's command open one of its project's channels
	// first; without one, it's this channel.
	steps := input.Steps
	if steps[0].Op != "select_channel" {
		steps = append([]uiRunStep{{Op: "select_channel", ChannelID: s.channelID}}, steps...)
	}
	body, _ := json.Marshal(map[string]any{"steps": steps, "client_id": input.ClientID, "timeout": input.Timeout})
	respBody, status, err := s.doRequest("POST", s.apiURL+"/api/ui/commands", body)
	if err != nil {
		return errorResult(fmt.Sprintf("calling API: %v", err)), nil, nil
	}
	if status != http.StatusOK {
		return errorResult(fmt.Sprintf("API error (status %d): %s", status, string(respBody))), nil, nil
	}

	// A failed step makes the call an error, with the results as they are.
	var reply struct {
		Results []struct {
			OK bool `json:"ok"`
		} `json:"results"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(respBody, &reply); err != nil {
		return errorResult(fmt.Sprintf("decoding response: %v", err)), nil, nil
	}
	failed := reply.Error != ""
	for _, r := range reply.Results {
		failed = failed || !r.OK
	}
	return &mcp.CallToolResult{IsError: failed, Content: []mcp.Content{&mcp.TextContent{Text: string(respBody)}}}, nil, nil
}
