package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type uiRunInput struct {
	Steps    []uiRunStep `json:"steps" jsonschema:"The steps, run in order until one fails"`
	ClientID string      `json:"client_id,omitempty" jsonschema:"The window (from ui_state); empty for the one the user last focused"`
	Timeout  string      `json:"timeout,omitempty" jsonschema:"How long the steps may take, as a Go duration (default 1m, at most 10m)"`
}

// paneRef is a $n pane reference: the pane step n of the call made.
var paneRef = regexp.MustCompile(`^\$(\d+)$`)

// shiftPaneRefs returns steps with their $n pane references (pane, next_to)
// renumbered for a step put before them.
func shiftPaneRefs(steps []uiRunStep) []uiRunStep {
	shift := func(ref string) string {
		m := paneRef.FindStringSubmatch(ref)
		if m == nil {
			return ref
		}
		n, err := strconv.Atoi(m[1])
		if err != nil {
			return ref
		}
		return "$" + strconv.Itoa(n+1)
	}
	out := make([]uiRunStep, len(steps))
	for i, step := range steps {
		step.Pane, step.NextTo = shift(step.Pane), shift(step.NextTo)
		out[i] = step
	}
	return out
}

// uiRunStep is a step of ui_run, as the app's window runs it. Pointers keep
// a false or 0 the agent gives.
type uiRunStep struct {
	Op        string `json:"op" jsonschema:"select_channel {channel_id}, set_tab {tab}, create_tab {tab?, canvas?}, rename_tab {tab, name}, remove_tab {tab}, replace_pane {pane, panel, open_mode?, item?, scope?}, add_pane {panel, next_to?, direction?, side?, open_mode?, item?, scope?}, remove_pane {pane}, maximize_pane {pane}, restore_pane, open_file {path, line?}, show_message {message_id}, send_input {pane, text, submit?}, read_output {pane, lines?}, wait_for {pane, match?, quiet_ms?, lines?}"`
	ChannelID string `json:"channel_id,omitempty" jsonschema:"select_channel: a channel, thread or worktree thread of this project"`
	Tab       string `json:"tab,omitempty" jsonschema:"set_tab, rename_tab, remove_tab: a layout tab (from ui_state); create_tab: the new tab's name (default the next Layout N)"`
	Name      string `json:"name,omitempty" jsonschema:"rename_tab: the tab's new name"`
	Canvas    *bool  `json:"canvas,omitempty" jsonschema:"create_tab: a canvas tab, whose panes are free-floating tiles (default false, a split tab)"`
	Pane      string `json:"pane,omitempty" jsonschema:"A pane id (from ui_state), a panel type for its first pane, or $n for the pane step n (from 1) of this call made (e.g. add_pane then send_input to $1); on a canvas tab the panes are its tiles"`
	Panel     string `json:"panel,omitempty" jsonschema:"replace_pane, add_pane: the new pane's panel"`
	OpenMode  string `json:"open_mode,omitempty" jsonschema:"replace_pane, add_pane of a docker-agent (Chat Terminal): resume the channel's session, fork it (the default) or start fresh; of a chat-side (Chat (Resume with fork) / Chat (Fresh session)): fork or fresh"`
	NextTo    string `json:"next_to,omitempty" jsonschema:"add_pane: the pane (id, panel type or $n) the new one goes beside; without it, the new pane spans the tab's edge (on a canvas, its tile goes beside all the tiles)"`
	Direction string `json:"direction,omitempty" jsonschema:"add_pane: horizontal makes a column, vertical a row (default horizontal); beside a pane in a row of columns (or a column of rows) it joins that row with an equal share"`
	Side      string `json:"side,omitempty" jsonschema:"add_pane: after puts the new pane right of or below next_to (or the tab), before left of or above it (default after)"`
	Item      string `json:"item,omitempty" jsonschema:"replace_pane, add_pane of a playground: the playground it shows; of a plugin: its panel, as plugin/panel, or the plugin's id when it has one panel"`
	Scope     string `json:"scope,omitempty" jsonschema:"With item: the playground's scope, when both have one by that name"`
	Path      string `json:"path,omitempty" jsonschema:"open_file: a path in one of the channel's roots"`
	Line      *int   `json:"line,omitempty" jsonschema:"open_file: the line to show, from 1"`
	MessageID *int64 `json:"message_id,omitempty" jsonschema:"show_message: a message of the open channel's chat (its #id), to scroll the chat to and highlight. A message link loop://channel/<channel-id>/<message-id> is select_channel {channel_id} then show_message {message_id}"`
	Text      string `json:"text,omitempty" jsonschema:"send_input: what to type, or, on a chat or chat-side pane, the message to queue"`
	Submit    *bool  `json:"submit,omitempty" jsonschema:"send_input: whether to press enter after the text (default true)"`
	Lines     *int   `json:"lines,omitempty" jsonschema:"read_output, wait_for: how many of the terminal's (or chat's messages') last lines (default 50, at most 2000)"`
	Match     string `json:"match,omitempty" jsonschema:"wait_for: a regular expression (multiline, RE2 syntax: no lookarounds or backreferences) the last lines must match"`
	QuietMS   *int   `json:"quiet_ms,omitempty" jsonschema:"wait_for without match: how long the terminal must be quiet, or a chat idle when no send_input queued a message in it, in ms (default 2000)"`
}

// uiOps are the ops of a ui_run step; uiPanels are the panels a pane step
// makes. Keep them in sync with the app's executor and PANEL_OPTIONS, plus
// "plugin" (app/src/uiBridge/schemaSync.test.ts checks).
var (
	uiOps = []any{
		"select_channel", "set_tab", "create_tab", "rename_tab", "remove_tab",
		"replace_pane", "add_pane", "remove_pane", "maximize_pane", "restore_pane",
		"open_file", "show_message", "send_input", "read_output", "wait_for",
	}
	uiPanels = []any{
		"chat", "chat-side", "editor", "file-tree", "memory", "git", "docker-agent", "docker-shell", "host-shell",
		"docker-browser", "host-browser", "sessions", "playground", "notes", "tasks", "kanban",
		"workflows", "audit", "quality", "review", "plugin",
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
	// first; without one, it's this channel, a step before the agent's, so
	// their $n pane references move up by one.
	steps := input.Steps
	if steps[0].Op != "select_channel" {
		steps = append([]uiRunStep{{Op: "select_channel", ChannelID: s.channelID}}, shiftPaneRefs(steps)...)
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
