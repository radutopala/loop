package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"github.com/radutopala/loop/internal/logging"
	"github.com/radutopala/loop/internal/mcpbrowser"
)

func (a *app) newMCPBrowserCmd() *cobra.Command {
	var logPath string
	var apiURL string
	var channelID string

	cmd := &cobra.Command{
		Use:   "mcp-browser",
		Short: "Run as an MCP server for browser automation via the host API",
		RunE: func(_ *cobra.Command, _ []string) error {
			return a.runMCPBrowser(apiURL, channelID, logPath, mcpbrowser.New)
		},
	}

	cmd.Flags().StringVar(&apiURL, "api-url", "", "Host API URL for browser actions")
	cmd.Flags().StringVar(&channelID, "channel-id", "", "Channel ID for browser actions")
	cmd.Flags().StringVar(&logPath, "log", ".loop/mcp-browser.log", "Path to MCP log file")

	return cmd
}

func (a *app) runMCPBrowser(apiURL, channelID, logPath string, newServer func(string, string, *slog.Logger) *mcpbrowser.Server) error {
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("opening mcp-browser log: %w", err)
	}
	defer f.Close()

	logLevel, logFormat := "info", "text"
	cfg, cfgErr := a.configLoad()
	if cfgErr == nil {
		logLevel = cfg.LogLevel
		logFormat = cfg.LogFormat
	}

	logger := logging.NewLoggerWithWriter(logLevel, logFormat, f)
	srv := newServer(apiURL, channelID, logger)
	return srv.Run(context.Background(), &mcp.StdioTransport{})
}
