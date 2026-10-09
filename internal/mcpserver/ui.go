package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type uiRunInput struct {
	Steps    []map[string]any `json:"steps" jsonschema:"required,The steps, run in order until one fails. Each has an op: select_channel {channel_id}, set_tab {tab}, replace_pane {pane, panel, open_mode?, item?, scope?}, add_pane {panel, next_to?, direction?, open_mode?, item?, scope?}, remove_pane {pane}, maximize_pane {pane}, restore_pane, open_file {path, line?}, send_input {pane, text, submit?}, read_output {pane, lines?}, wait_for {pane, match?, quiet_ms?, lines?}. A pane is a pane id or a panel type (its first pane)."`
	ClientID string           `json:"client_id,omitempty" jsonschema:"The window (from ui_state); empty for the one the user last focused"`
	Timeout  string           `json:"timeout,omitempty" jsonschema:"How long the steps may take, as a Go duration (default 1m, at most 10m)"`
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
	if len(input.Steps) == 0 {
		return errorResult("steps is required"), nil, nil
	}
	// The daemon has an agent's command open one of its project's channels
	// first; without one, it's this channel.
	steps := input.Steps
	if steps[0]["op"] != "select_channel" {
		steps = append([]map[string]any{{"op": "select_channel", "channel_id": s.channelID}}, steps...)
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
