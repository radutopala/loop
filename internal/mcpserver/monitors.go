package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// monitorInfo is a running monitor, as the API returns it.
type monitorInfo struct {
	ID          string    `json:"id"`
	Description string    `json:"description"`
	Command     string    `json:"command"`
	StartedAt   time.Time `json:"started_at"`
	TimeoutSec  int       `json:"timeout_sec"`
}

type monitorsList struct {
	Monitors []monitorInfo `json:"monitors"`
}

// monitorsURL is the agent's own channel's monitors route.
func (s *Server) monitorsURL() string {
	return s.apiURL + "/api/channels/" + url.PathEscape(s.channelID) + "/monitors"
}

type monitorInput struct {
	Command     string `json:"command" jsonschema:"The shell script to run (/bin/sh -c) until the condition holds, e.g. an until loop that polls and sleeps. Its exit status and the tail of its output go into the message that wakes you"`
	Description string `json:"description" jsonschema:"What the monitor waits for, in a few words; shown in the chat and in the wake-up message"`
	TimeoutSec  int    `json:"timeout_sec,omitempty" jsonschema:"Stop the script after this many seconds and wake you with a timeout. Optional — defaults to 3600, at most 86400"`
	Interrupt   bool   `json:"interrupt,omitempty" jsonschema:"Wake you even if you are busy, by cancelling your current run. Optional — by default the wake-up message queues behind your current run"`
}

// handleMonitor starts a watch script for the agent's own channel and
// returns at once; the daemon wakes the channel when the script ends.
func (s *Server) handleMonitor(_ context.Context, _ *mcp.CallToolRequest, input monitorInput) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "monitor", "channel_id", s.channelID, "description", input.Description)

	if s.channelID == "" {
		return errorResult("monitor is only available to channel-scoped agents"), nil, nil
	}
	if strings.TrimSpace(input.Command) == "" || strings.TrimSpace(input.Description) == "" {
		return errorResult("command and description are required"), nil, nil
	}
	body, _ := json.Marshal(input)
	mon, errResult, err := doAPICall[monitorInfo](s, "POST", s.monitorsURL(), http.StatusCreated, body)
	if errResult != nil || err != nil {
		return errResult, nil, err
	}
	text := fmt.Sprintf("Monitor %s started (times out after %ds). End your turn now: a message starting with [monitor %s] will wake you when the script exits.", mon.ID, mon.TimeoutSec, mon.ID)
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}, nil, nil
}

type listMonitorsInput struct{}

// handleListMonitors lists the agent's own channel's running monitors.
func (s *Server) handleListMonitors(_ context.Context, _ *mcp.CallToolRequest, _ listMonitorsInput) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "list_monitors", "channel_id", s.channelID)

	if s.channelID == "" {
		return errorResult("list_monitors is only available to channel-scoped agents"), nil, nil
	}
	list, errResult, err := doAPICall[monitorsList](s, "GET", s.monitorsURL(), http.StatusOK, nil)
	if errResult != nil || err != nil {
		return errResult, nil, err
	}
	if len(list.Monitors) == 0 {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "No monitors running in the current channel."}}}, nil, nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d monitor(s) running in the current channel:\n", len(list.Monitors))
	for _, m := range list.Monitors {
		fmt.Fprintf(&b, "- %s %q since %s (timeout %ds): %s\n", m.ID, m.Description, m.StartedAt.UTC().Format(time.RFC3339), m.TimeoutSec, m.Command)
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: b.String()}}}, nil, nil
}

type cancelMonitorInput struct {
	MonitorID string `json:"monitor_id" jsonschema:"The monitor's ID, from monitor's result or list_monitors"`
}

// handleCancelMonitor stops one of the agent's own channel's monitors
// without waking it.
func (s *Server) handleCancelMonitor(_ context.Context, _ *mcp.CallToolRequest, input cancelMonitorInput) (*mcp.CallToolResult, any, error) {
	s.logger.Info("mcp tool call", "tool", "cancel_monitor", "channel_id", s.channelID, "monitor_id", input.MonitorID)

	if s.channelID == "" {
		return errorResult("cancel_monitor is only available to channel-scoped agents"), nil, nil
	}
	if input.MonitorID == "" {
		return errorResult("monitor_id is required"), nil, nil
	}
	if errResult, err := doAPICallNoBody(s, "DELETE", s.monitorsURL()+"/"+url.PathEscape(input.MonitorID), http.StatusNoContent, nil); errResult != nil || err != nil {
		return errResult, nil, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("Cancelled monitor %s.", input.MonitorID)}}}, nil, nil
}
