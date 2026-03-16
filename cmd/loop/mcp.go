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

func (a *app) newMCPCmd() *cobra.Command {
	var channelID, apiURL, logPath, dirPath, authorID, platform string
	var memoryEnabled bool

	cmd := &cobra.Command{
		Use:     "mcp",
		Aliases: []string{"m"},
		Short:   "Run as an MCP server over stdio",
		RunE: func(_ *cobra.Command, _ []string) error {
			return a.runMCP(channelID, apiURL, dirPath, logPath, authorID, platform, memoryEnabled)
		},
	}

	cmd.Flags().StringVar(&channelID, "channel-id", "", "Channel ID")
	cmd.Flags().StringVar(&dirPath, "dir", "", "Project directory path (auto-creates channel)")
	cmd.Flags().StringVar(&apiURL, "api-url", "", "Loop API base URL")
	cmd.Flags().StringVar(&logPath, "log", ".loop/mcp.log", "Path to MCP log file")
	cmd.Flags().StringVar(&authorID, "author-id", "", "User ID of the message author")
	cmd.Flags().StringVar(&platform, "platform", "local", "Platform for channel creation (used with --dir)")
	cmd.Flags().BoolVar(&memoryEnabled, "memory", false, "Enable memory search/index tools")
	cmd.MarkFlagsOneRequired("channel-id", "dir")
	cmd.MarkFlagsMutuallyExclusive("channel-id", "dir")
	_ = cmd.MarkFlagRequired("api-url")

	return cmd
}

func (a *app) runMCP(channelID, apiURL, dirPath, logPath, authorID, platform string, memoryEnabled bool) error {
	if dirPath != "" {
		resolved, err := a.ensureChannelFn(apiURL, dirPath, platform)
		if err != nil {
			return fmt.Errorf("ensuring channel for dir %s: %w", dirPath, err)
		}
		channelID = resolved
	}

	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("opening mcp log: %w", err)
	}
	defer f.Close()

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

	srv := a.newMCPServer(channelID, apiURL, authorID, http.DefaultClient, logger, memOpts...)
	return srv.Run(context.Background(), &mcp.StdioTransport{})
}
