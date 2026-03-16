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
	var host string
	var port int
	var logPath string
	var targetID string
	var apiURL string
	var channelID string

	cmd := &cobra.Command{
		Use:   "mcp-browser",
		Short: "Run as an MCP server for browser automation via CDP",
		RunE: func(_ *cobra.Command, _ []string) error {
			return a.runMCPBrowser(host, port, targetID, apiURL, channelID, logPath, mcpbrowser.New)
		},
	}

	cmd.Flags().StringVar(&host, "host", "127.0.0.1", "Chrome host for CDP connection")
	cmd.Flags().IntVar(&port, "port", 9222, "Chrome DevTools Protocol port")
	cmd.Flags().StringVar(&targetID, "target", "", "Chrome page target ID to attach to (shares tab with browser pane)")
	cmd.Flags().StringVar(&apiURL, "api-url", "", "Host API URL for lazy Chrome start callback")
	cmd.Flags().StringVar(&channelID, "channel-id", "", "Channel ID for lazy Chrome start callback")
	cmd.Flags().StringVar(&logPath, "log", ".loop/mcp-browser.log", "Path to MCP log file")

	return cmd
}

func (a *app) runMCPBrowser(host string, port int, targetID, apiURL, channelID, logPath string, newServer func(string, *slog.Logger) *mcpbrowser.Server) error {
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

	cdpEndpoint := fmt.Sprintf("ws://%s:%d", host, port)
	srv := newServer(cdpEndpoint, logger)
	if targetID != "" {
		srv.SetTargetID(targetID)
	}
	if apiURL != "" && channelID != "" {
		srv.SetAPICallback(apiURL, channelID)
	}
	return srv.Run(context.Background(), &mcp.StdioTransport{})
}
