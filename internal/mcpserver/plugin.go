package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/radutopala/loop/internal/plugin"
)

// PluginToolPrefix starts the MCP name of every plugin tool, so a plugin's
// tool can't shadow one of Loop's own.
const PluginToolPrefix = "plugin_"

// WithPluginTools makes this a plugin agent run's MCP server: it serves
// tools, which the plugin pluginID answers through Loop.
func WithPluginTools(pluginID string, tools []plugin.Tool) MemoryOption {
	return func(s *Server) {
		s.pluginID = pluginID
		s.pluginTools = tools
	}
}

// registerPluginTools adds the plugin's tools. Loop checks each call's
// input against the tool's schema before the plugin sees it.
func (s *Server) registerPluginTools() {
	for _, t := range s.pluginTools {
		s.mcpServer.AddTool(&mcp.Tool{
			Name:        PluginToolPrefix + t.Name,
			Description: t.Description,
			InputSchema: t.InputSchema,
		}, s.pluginToolHandler(t.Name))
	}
}

// pluginToolResult is the plugin's answer to a tool call.
type pluginToolResult struct {
	Content string `json:"content"`
	IsError bool   `json:"is_error"`
}

func (s *Server) pluginToolHandler(tool string) mcp.ToolHandler {
	return func(_ context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		s.logger.Info("mcp tool call", "tool", PluginToolPrefix+tool, "plugin", s.pluginID)
		input := req.Params.Arguments
		if len(input) == 0 || string(input) == "null" {
			input = json.RawMessage("{}")
		}
		data, _ := json.Marshal(map[string]json.RawMessage{"input": input})
		apiURL := fmt.Sprintf("%s/api/plugins/%s/tools/%s", s.apiURL, url.PathEscape(s.pluginID), url.PathEscape(tool))
		result, errResult, err := doAPICall[pluginToolResult](s, "POST", apiURL, 200, data)
		if errResult != nil || err != nil {
			return errResult, err
		}
		return &mcp.CallToolResult{
			IsError: result.IsError,
			Content: []mcp.Content{&mcp.TextContent{Text: result.Content}},
		}, nil
	}
}
