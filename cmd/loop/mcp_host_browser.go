package main

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"github.com/radutopala/loop/internal/logging"
	"github.com/radutopala/loop/internal/mcpbrowser"
)

func (a *app) newMCPHostBrowserCmd() *cobra.Command {
	var logPath string

	cmd := &cobra.Command{
		Use:   "mcp-host-browser",
		Short: "Run as an MCP server for host Chrome browser automation via CDP",
		RunE: func(_ *cobra.Command, _ []string) error {
			return a.runMCPHostBrowser(logPath, mcpbrowser.NewDirect)
		},
	}

	cmd.Flags().StringVar(&logPath, "log", ".loop/mcp-host-browser.log", "Path to MCP log file")

	return cmd
}

func (a *app) runMCPHostBrowser(logPath string, newServer func(string, *slog.Logger) *mcpbrowser.Server) (err error) {
	f, err := a.openLogFile(logPath)
	if err != nil {
		return fmt.Errorf("opening mcp-host-browser log: %w", err)
	}
	defer func() { err = closeLogFile(f, "mcp-host-browser", err) }()

	logLevel, logFormat := "info", "text"
	cfg, cfgErr := a.configLoad()
	if cfgErr == nil {
		logLevel = cfg.LogLevel
		logFormat = cfg.LogFormat
	}

	logger := logging.NewLoggerWithWriter(logLevel, logFormat, f)
	cdpEndpoint, err := a.discoverWSEndpoint()
	if err != nil {
		return fmt.Errorf("discovering Chrome CDP endpoint: %w", err)
	}
	logger.Info("mcp-host-browser: using CDP endpoint", "endpoint", cdpEndpoint)
	srv := newServer(cdpEndpoint, logger)
	return srv.Run(context.Background(), &mcp.StdioTransport{})
}
