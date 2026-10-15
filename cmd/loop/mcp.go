package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/explain"
	"github.com/radutopala/loop/internal/learn"
	"github.com/radutopala/loop/internal/logging"
	"github.com/radutopala/loop/internal/mcpserver"
	"github.com/radutopala/loop/internal/plugin"
)

// closeLogFile closes f and merges any close error into existingErr. If
// existingErr is non-nil, it wins; otherwise a close failure is wrapped with
// component for context. This is the single point where the deferred close of
// a writable MCP log file is handled.
func closeLogFile(f *os.File, component string, existingErr error) error {
	cerr := f.Close()
	if cerr != nil && existingErr == nil {
		return fmt.Errorf("closing %s log: %w", component, cerr)
	}
	return existingErr
}

func (a *app) newMCPCmd() *cobra.Command {
	var channelID, apiURL, logPath, dirPath, authorID, platform, agentID, pluginID, pluginTools string
	var memoryEnabled bool

	cmd := &cobra.Command{
		Use:     "mcp",
		Aliases: []string{"m"},
		Short:   "Run as an MCP server over stdio",
		RunE: func(_ *cobra.Command, _ []string) error {
			var extra []mcpserver.MemoryOption
			if pluginID != "" {
				var tools []plugin.Tool
				if err := json.Unmarshal([]byte(pluginTools), &tools); err != nil {
					return fmt.Errorf("parsing --plugin-tools: %w", err)
				}
				extra = append(extra, mcpserver.WithPluginTools(pluginID, tools))
			}
			return a.runMCP(channelID, apiURL, dirPath, logPath, authorID, platform, agentID, memoryEnabled, extra...)
		},
	}

	cmd.Flags().StringVar(&channelID, "channel-id", "", "Channel ID")
	cmd.Flags().StringVar(&dirPath, "dir", "", "Project directory path (auto-creates channel)")
	cmd.Flags().StringVar(&apiURL, "api-url", "", "Loop API base URL")
	cmd.Flags().StringVar(&logPath, "log", ".loop/mcp.log", "Path to MCP log file")
	cmd.Flags().StringVar(&authorID, "author-id", "", "User ID of the message author")
	cmd.Flags().StringVar(&platform, "platform", "local", "Platform for channel creation (used with --dir)")
	cmd.Flags().BoolVar(&memoryEnabled, "memory", false, "Enable memory search/index tools")
	cmd.Flags().StringVar(&agentID, "agent-id", "", "Agent ID for inter-agent communication tools")
	cmd.Flags().StringVar(&pluginID, "plugin-id", "", "Plugin whose tools a plugin agent run gets")
	cmd.Flags().StringVar(&pluginTools, "plugin-tools", "[]", "The plugin tools, as a JSON array")
	cmd.MarkFlagsRequiredTogether("plugin-id", "plugin-tools")
	cmd.MarkFlagsOneRequired("channel-id", "dir")
	cmd.MarkFlagsMutuallyExclusive("channel-id", "dir")
	_ = cmd.MarkFlagRequired("api-url")

	return cmd
}

func (a *app) runMCP(channelID, apiURL, dirPath, logPath, authorID, platform, agentID string, memoryEnabled bool, extra ...mcpserver.MemoryOption) (err error) {
	if dirPath != "" {
		resolved, rerr := a.ensureChannelFn(apiURL, dirPath, platform)
		if rerr != nil {
			return fmt.Errorf("ensuring channel for dir %s: %w", dirPath, rerr)
		}
		channelID = resolved
	}

	f, err := a.openLogFile(logPath)
	if err != nil {
		return fmt.Errorf("opening mcp log: %w", err)
	}
	defer func() { err = closeLogFile(f, "mcp", err) }()

	logLevel, logFormat := "info", "text"
	cfg, cfgErr := a.configLoad()
	if cfgErr == nil {
		logLevel = cfg.LogLevel
		logFormat = cfg.LogFormat
	}

	logger := logging.NewLoggerWithWriter(logLevel, logFormat, f)

	var memOpts []mcpserver.MemoryOption
	if memoryEnabled || (cfgErr == nil && cfg.Memory.Enabled) {
		memOpts = append(memOpts, mcpserver.WithMemoryAPI(dirPath))
	}
	switch {
	case agentID == "":
	case strings.HasPrefix(agentID, agent.PluginAgentPrefix):
		// A plugin's agent gets the plugin's tools (see extra) and works
		// alone, like a learn pass.
	case agentID == learn.AgentID:
		// A learn pass files proposals, and gets none of the inter-agent
		// tools: it works alone, and other agents shouldn't see or message
		// it.
		memOpts = append(memOpts, mcpserver.WithLearnTools())
	case agentID == agent.ChatAgentID:
		// Chat has no terminal: it isn't registered, and the daemon queues
		// the messages sent to it.
		memOpts = append(memOpts, mcpserver.WithAgentSender(agentID))
	case agentID == explain.AgentID:
		// An explanation only reads: like a learn pass it works alone, and
		// it has no tools of its own either.
	default:
		memOpts = append(memOpts, mcpserver.WithAgentTools(agentID))
	}
	memOpts = append(memOpts, mcpserver.WithWorkflowAPI())
	memOpts = append(memOpts, extra...)

	srv := a.newMCPServer(channelID, apiURL, authorID, a.apiClient, logger, memOpts...)
	srv.RegisterAgent()
	runErr := srv.Run(context.Background(), &mcp.StdioTransport{})
	srv.UnregisterAgent()
	return runErr
}
