package main

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"github.com/radutopala/loop/internal/logging"
	"github.com/radutopala/loop/internal/mcpserver"
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
	var channelID, apiURL, logPath, dirPath, authorID, platform, agentID string
	var memoryEnabled bool

	cmd := &cobra.Command{
		Use:     "mcp",
		Aliases: []string{"m"},
		Short:   "Run as an MCP server over stdio",
		RunE: func(_ *cobra.Command, _ []string) error {
			return a.runMCP(channelID, apiURL, dirPath, logPath, authorID, platform, agentID, memoryEnabled)
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
	cmd.MarkFlagsOneRequired("channel-id", "dir")
	cmd.MarkFlagsMutuallyExclusive("channel-id", "dir")
	_ = cmd.MarkFlagRequired("api-url")

	return cmd
}

func (a *app) runMCP(channelID, apiURL, dirPath, logPath, authorID, platform, agentID string, memoryEnabled bool) (err error) {
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
	if agentID != "" {
		memOpts = append(memOpts, mcpserver.WithAgentTools(agentID))
	}
	memOpts = append(memOpts, mcpserver.WithWorkflowAPI())

	srv := a.newMCPServer(channelID, apiURL, authorID, http.DefaultClient, logger, memOpts...)
	srv.RegisterAgent()
	runErr := srv.Run(context.Background(), &mcp.StdioTransport{})
	srv.UnregisterAgent()
	return runErr
}
