package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"reflect"
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
	Op        uiOp        `json:"op" jsonschema:"select_channel {channel_id}, set_tab {tab}, create_tab {tab?, canvas?}, rename_tab {tab, name}, remove_tab {tab}, replace_pane {pane, panel, open_mode?, item?, scope?}, add_pane {panel, next_to?, direction?, side?, open_mode?, item?, scope?}, remove_pane {pane}, maximize_pane {pane}, restore_pane, open_file {path, line?}, show_message {message_id}, open_side {view}, close_side, send_input {pane, text, submit?}, read_output {pane, lines?}, wait_for {pane, match?, quiet_ms?, lines?}, open_walkthrough {walkthrough_id, walkthrough_step?, autoplay_sec?}, close_walkthrough"`
	ChannelID string      `json:"channel_id,omitempty" jsonschema:"select_channel: a channel, thread or worktree thread of this project"`
	Tab       string      `json:"tab,omitempty" jsonschema:"set_tab, rename_tab, remove_tab: a layout tab (from ui_state); create_tab: the new tab's name (default the next Layout N)"`
	Name      string      `json:"name,omitempty" jsonschema:"rename_tab: the tab's new name"`
	Canvas    *bool       `json:"canvas,omitempty" jsonschema:"create_tab: a canvas tab, whose panes are free-floating tiles (default false, a split tab)"`
	Pane      string      `json:"pane,omitempty" jsonschema:"A pane id (from ui_state), a panel type for its first pane, or $n for the pane step n (from 1) of this call made (e.g. add_pane then send_input to $1); on a canvas tab the panes are its tiles. side is the side view open beside the chat (ui_state's side): read_output reads the Learn view's proposals and learn thread, the Explain view's write-ups or the Shell view's shell; send_input types into the Shell view's shell or asks the learn thread"`
	Panel     uiPanel     `json:"panel,omitempty" jsonschema:"replace_pane, add_pane: the new pane's panel"`
	OpenMode  uiOpenMode  `json:"open_mode,omitempty" jsonschema:"replace_pane, add_pane of a docker-agent (Chat Terminal): resume the channel's session, fork it (the default) or start fresh; of a chat-side (Chat (Resume with fork) / Chat (Fresh session)): fork or fresh; a chat with fork or fresh opens a chat-side"`
	NextTo    string      `json:"next_to,omitempty" jsonschema:"add_pane: the pane (id, panel type or $n) the new one goes beside; without it, the new pane spans the tab's edge (on a canvas, its tile goes beside all the tiles)"`
	Direction uiDirection `json:"direction,omitempty" jsonschema:"add_pane: horizontal makes a column, vertical a row (default horizontal); beside a pane in a row of columns (or a column of rows) it joins that row with an equal share"`
	Side      uiSide      `json:"side,omitempty" jsonschema:"add_pane: after puts the new pane right of or below next_to (or the tab), before left of or above it (default after)"`
	Item      string      `json:"item,omitempty" jsonschema:"replace_pane, add_pane of a playground: the playground it shows; of a plugin: its panel, as plugin/panel, or the plugin's id when it has one panel"`
	Scope     uiScope     `json:"scope,omitempty" jsonschema:"With item: the playground's scope, when both have one by that name"`
	Path      string      `json:"path,omitempty" jsonschema:"open_file: a path in one of the channel's roots"`
	Line      *int        `json:"line,omitempty" jsonschema:"open_file: the line to show, from 1"`
	MessageID *int64      `json:"message_id,omitempty" jsonschema:"show_message: a message of the open channel's chat (its #id), to scroll the chat to and highlight. A message link loop://channel/<channel-id>/<message-id> is select_channel {channel_id} then show_message {message_id}"`
	View      uiSideView  `json:"view,omitempty" jsonschema:"open_side: the side view to open beside the chat, or swap the open one for: learn (the learn pass's proposals and thread), explain (the turns' write-ups) or shell (a shell in the chat's own container, while it runs)"`
	Text      string      `json:"text,omitempty" jsonschema:"send_input: what to type, or, on a chat or chat-side pane, the message to queue"`
	Submit    *bool       `json:"submit,omitempty" jsonschema:"send_input: whether to press enter after the text (default true)"`
	Lines     *int        `json:"lines,omitempty" jsonschema:"read_output, wait_for: how many of the terminal's (or chat's messages') last lines (default 50, at most 2000)"`
	Match     string      `json:"match,omitempty" jsonschema:"wait_for: a regular expression (multiline, RE2 syntax: no lookarounds or backreferences) the last lines must match"`
	QuietMS   *int        `json:"quiet_ms,omitempty" jsonschema:"wait_for without match: how long the terminal must be quiet, or a chat idle when no send_input queued a message in it, in ms (default 2000)"`

	WalkthroughID   *int64 `json:"walkthrough_id,omitempty" jsonschema:"open_walkthrough: a walkthrough of the open channel (from ui_walkthrough), to show in the editor again"`
	WalkthroughStep *int   `json:"walkthrough_step,omitempty" jsonschema:"open_walkthrough: the step to show, from 1 (default 1)"`
	AutoplaySec     *int   `json:"autoplay_sec,omitempty" jsonschema:"open_walkthrough: play it, moving to the next step every this many seconds until the last, or until the user pauses or steps through it themselves (default: the user steps through)"`
}

// The step fields that take one of a few values have types of their own,
// each listed in uiEnums for the schema.
type (
	uiOp        string
	uiPanel     string
	uiOpenMode  string
	uiDirection string
	uiSide      string
	uiScope     string
	uiSideView  string
)

// uiOps are the ops of a ui_run step; uiPanels are the panels a pane step
// makes. Keep them in sync with the app's executor and PANEL_OPTIONS, plus
// "plugin" (app/src/uiBridge/schemaSync.test.ts checks).
var (
	uiOps = []any{
		"select_channel", "set_tab", "create_tab", "rename_tab", "remove_tab",
		"replace_pane", "add_pane", "remove_pane", "maximize_pane", "restore_pane",
		"open_file", "show_message", "open_side", "close_side", "send_input", "read_output", "wait_for",
		"open_walkthrough", "close_walkthrough",
	}
	uiPanels = []any{
		"chat", "chat-side", "editor", "file-tree", "memory", "git", "docker-agent", "docker-shell", "host-shell",
		"docker-browser", "host-browser", "sessions", "playground", "notes", "tasks", "kanban",
		"workflows", "audit", "quality", "review", "plugin",
	}
)

// uiEnums are the values of each of a step's enum types.
var uiEnums = map[reflect.Type][]any{
	reflect.TypeFor[uiOp]():        uiOps,
	reflect.TypeFor[uiPanel]():     uiPanels,
	reflect.TypeFor[uiOpenMode]():  {"resume", "fork", "fresh"},
	reflect.TypeFor[uiDirection](): {"horizontal", "vertical"},
	reflect.TypeFor[uiSide]():      {"before", "after"},
	reflect.TypeFor[uiScope]():     {"global", "project"},
	reflect.TypeFor[uiSideView]():  {"learn", "explain", "shell"},
}

// uiRunSchema is ui_run's input schema: the one inferred from uiRunInput,
// its enum types as their values, and the window's bounds on its numbers.
func uiRunSchema() *jsonschema.Schema {
	types := make(map[reflect.Type]*jsonschema.Schema, len(uiEnums))
	for t, values := range uiEnums {
		types[t] = &jsonschema.Schema{Type: "string", Enum: values}
	}
	schema, _ := jsonschema.For[uiRunInput](&jsonschema.ForOptions{TypeSchemas: types}) // a struct of plain fields infers
	schema.Properties["steps"].MinItems = jsonschema.Ptr(1)
	step := schema.Properties["steps"].Items.Properties
	step["lines"].Minimum, step["lines"].Maximum = jsonschema.Ptr(1.0), jsonschema.Ptr(2000.0)
	step["line"].Minimum = jsonschema.Ptr(1.0)
	step["message_id"].Minimum = jsonschema.Ptr(1.0)
	step["quiet_ms"].Minimum = jsonschema.Ptr(0.0)
	step["walkthrough_id"].Minimum = jsonschema.Ptr(1.0)
	step["walkthrough_step"].Minimum = jsonschema.Ptr(1.0)
	step["autoplay_sec"].Minimum, step["autoplay_sec"].Maximum = jsonschema.Ptr(1.0), jsonschema.Ptr(float64(maxAutoplaySec))
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
	failed, err := uiRunFailed(respBody)
	if err != nil {
		return errorResult(fmt.Sprintf("decoding response: %v", err)), nil, nil
	}
	return &mcp.CallToolResult{IsError: failed, Content: []mcp.Content{&mcp.TextContent{Text: string(respBody)}}}, nil, nil
}

// uiRunFailed reports whether a step of the window's reply to a command
// failed, which makes the call an error, with the results as they are.
func uiRunFailed(respBody []byte) (bool, error) {
	var reply struct {
		Results []struct {
			OK bool `json:"ok"`
		} `json:"results"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(respBody, &reply); err != nil {
		return false, err
	}
	failed := reply.Error != ""
	for _, r := range reply.Results {
		failed = failed || !r.OK
	}
	return failed, nil
}

type uiWalkthroughInput struct {
	Title       string              `json:"title" jsonschema:"What the walkthrough explains, shown on its card and in the editor's walkthroughs menu"`
	Steps       []uiWalkthroughStep `json:"steps" jsonschema:"The steps, shown one at a time in order (at most 50)"`
	AutoplaySec *int                `json:"autoplay_sec,omitempty" jsonschema:"Play it, moving to the next step every this many seconds until the last, or until the user pauses or steps through it themselves (default: the user steps through)"`
	ClientID    string              `json:"client_id,omitempty" jsonschema:"The window (from ui_state); empty for the one the user last focused"`
}

// maxAutoplaySec is the longest a played walkthrough shows a step.
const maxAutoplaySec = 600

type uiWalkthroughStep struct {
	Path      string `json:"path" jsonschema:"A text file in one of the channel's roots, relative to the channel's dir or absolute"`
	StartLine int    `json:"start_line" jsonschema:"The first line the step highlights, from 1"`
	EndLine   int    `json:"end_line,omitempty" jsonschema:"The last line it highlights (default start_line)"`
	Title     string `json:"title,omitempty" jsonschema:"A short heading for the step"`
	Body      string `json:"body" jsonschema:"The explanation shown over the lines, in Markdown (at most 8000 bytes)"`
}

// uiWalkthroughSchema is ui_walkthrough's input schema, with the API's
// bounds on its steps.
func uiWalkthroughSchema() *jsonschema.Schema {
	schema, _ := jsonschema.For[uiWalkthroughInput](nil) // a struct of plain fields infers
	schema.Properties["steps"].MinItems = jsonschema.Ptr(1)
	schema.Properties["steps"].MaxItems = jsonschema.Ptr(50)
	step := schema.Properties["steps"].Items.Properties
	step["start_line"].Minimum = jsonschema.Ptr(1.0)
	step["end_line"].Minimum = jsonschema.Ptr(1.0)
	schema.Properties["autoplay_sec"].Minimum, schema.Properties["autoplay_sec"].Maximum = jsonschema.Ptr(1.0), jsonschema.Ptr(float64(maxAutoplaySec))
	return schema
}

// handleUIWalkthrough saves a walkthrough in this channel and opens it in
// the app's editor. It's kept until the channel is deleted, so it can be
// opened again when no window is open now.
func (s *Server) handleUIWalkthrough(_ context.Context, _ *mcp.CallToolRequest, input uiWalkthroughInput) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "ui_walkthrough", "steps", len(input.Steps))
	body, _ := json.Marshal(map[string]any{"title": input.Title, "steps": input.Steps})
	respBody, status, err := s.doRequest("POST", s.apiURL+"/api/channels/"+url.PathEscape(s.channelID)+"/walkthroughs", body)
	if err != nil {
		return errorResult(fmt.Sprintf("calling API: %v", err)), nil, nil
	}
	if status != http.StatusCreated {
		return errorResult(fmt.Sprintf("API error (status %d): %s", status, string(respBody))), nil, nil
	}
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(respBody, &created); err != nil {
		return errorResult(fmt.Sprintf("decoding response: %v", err)), nil, nil
	}
	saved := fmt.Sprintf("Saved as walkthrough %d", created.ID)

	steps := []uiRunStep{
		{Op: "select_channel", ChannelID: s.channelID},
		{Op: "open_walkthrough", WalkthroughID: &created.ID, AutoplaySec: input.AutoplaySec},
	}
	body, _ = json.Marshal(map[string]any{"steps": steps, "client_id": input.ClientID})
	respBody, status, err = s.doRequest("POST", s.apiURL+"/api/ui/commands", body)
	switch {
	case err != nil:
		return errorResult(fmt.Sprintf("%s, but opening it failed: calling API: %v", saved, err)), nil, nil
	case status == http.StatusNotFound:
		return textResult(saved + "; no app window is open to show it. Open it later with ui_run open_walkthrough, or the user can from the editor's walkthroughs menu."), nil, nil
	case status != http.StatusOK:
		return errorResult(fmt.Sprintf("%s, but opening it failed: API error (status %d): %s", saved, status, string(respBody))), nil, nil
	}
	if failed, err := uiRunFailed(respBody); err != nil || failed {
		return errorResult(fmt.Sprintf("%s, but opening it failed: %s", saved, string(respBody))), nil, nil
	}
	return textResult(saved + ", open in the app's editor."), nil, nil
}
