package main

import (
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/mcpbrowser"
)

// --- newMCPBrowserCmd ---

func (s *MainSuite) TestNewMCPBrowserCmd() {
	cmd := s.app.newMCPBrowserCmd()
	require.Equal(s.T(), "mcp-browser", cmd.Use)
	require.NotNil(s.T(), cmd.RunE)

	f := cmd.Flags()
	require.NotNil(s.T(), f.Lookup("log"))
	require.NotNil(s.T(), f.Lookup("api-url"))
	require.NotNil(s.T(), f.Lookup("channel-id"))
}

func (s *MainSuite) TestRunMCPBrowserLogOpenError() {
	err := s.app.runMCPBrowser("", "", "/nonexistent/dir/mcp-browser.log", mcpbrowser.New)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "opening mcp-browser log")
}

func (s *MainSuite) TestRunMCPBrowserSuccess() {
	logPath := filepath.Join(s.T().TempDir(), "mcp-browser.log")

	called := false
	newServer := func(apiURL, channelID string, logger *slog.Logger) *mcpbrowser.Server {
		require.Equal(s.T(), "http://host:8222", apiURL)
		require.Equal(s.T(), "ch-1", channelID)
		require.NotNil(s.T(), logger)
		called = true
		return mcpbrowser.New(apiURL, channelID, logger)
	}

	// runMCPBrowser will try to use StdioTransport which will fail/close immediately in test.
	_ = s.app.runMCPBrowser("http://host:8222", "ch-1", logPath, newServer)
	require.True(s.T(), called)
}

func (s *MainSuite) TestRunMCPBrowserWithAPICallback() {
	logPath := filepath.Join(s.T().TempDir(), "mcp-browser.log")
	_ = s.app.runMCPBrowser("http://host.docker.internal:8222", "ch-1", logPath, mcpbrowser.New)
}

func (s *MainSuite) TestRunMCPBrowserWithConfig() {
	logPath := filepath.Join(s.T().TempDir(), "mcp-browser.log")

	s.app.configLoad = func() (*config.Config, error) {
		return &config.Config{
			LogLevel:  "debug",
			LogFormat: "json",
		}, nil
	}

	called := false
	newServer := func(apiURL, channelID string, logger *slog.Logger) *mcpbrowser.Server {
		require.NotNil(s.T(), logger)
		called = true
		return mcpbrowser.New(apiURL, channelID, logger)
	}

	_ = s.app.runMCPBrowser("", "", logPath, newServer)
	require.True(s.T(), called)
}

func (s *MainSuite) TestNewMCPBrowserCmdRunE() {
	logPath := filepath.Join(s.T().TempDir(), "mcp-browser.log")
	cmd := s.app.newMCPBrowserCmd()
	require.NoError(s.T(), cmd.Flags().Set("log", logPath))

	// RunE wraps runMCPBrowser — the stdio transport will close immediately.
	err := cmd.RunE(cmd, nil)
	_ = err
}

// --- newMCPHostBrowserCmd ---

func (s *MainSuite) TestNewMCPHostBrowserCmd() {
	cmd := s.app.newMCPHostBrowserCmd()
	require.Equal(s.T(), "mcp-host-browser", cmd.Use)
	require.NotNil(s.T(), cmd.RunE)

	f := cmd.Flags()
	require.Nil(s.T(), f.Lookup("host")) // removed — DevToolsActivePort discovery replaces it
	require.Nil(s.T(), f.Lookup("port")) // removed — no fallback needed
	require.NotNil(s.T(), f.Lookup("log"))
}

func (s *MainSuite) TestRunMCPHostBrowserLogOpenError() {
	err := s.app.runMCPHostBrowser("/nonexistent/dir/mcp-host-browser.log", mcpbrowser.NewDirect)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "opening mcp-host-browser log")
}

func (s *MainSuite) TestRunMCPHostBrowserSuccess() {
	logPath := filepath.Join(s.T().TempDir(), "mcp-host-browser.log")
	s.app.discoverWSEndpoint = func() (string, error) {
		return "ws://127.0.0.1:9333/devtools/browser/fake-guid", nil
	}

	called := false
	newServer := func(cdpEndpoint string, logger *slog.Logger) *mcpbrowser.Server {
		require.Equal(s.T(), "ws://127.0.0.1:9333/devtools/browser/fake-guid", cdpEndpoint)
		require.NotNil(s.T(), logger)
		called = true
		return mcpbrowser.NewDirect(cdpEndpoint, logger)
	}

	_ = s.app.runMCPHostBrowser(logPath, newServer)
	require.True(s.T(), called)
}

func (s *MainSuite) TestRunMCPHostBrowserDiscoveryError() {
	logPath := filepath.Join(s.T().TempDir(), "mcp-host-browser.log")
	s.app.discoverWSEndpoint = func() (string, error) {
		return "", fmt.Errorf("no DevToolsActivePort")
	}

	err := s.app.runMCPHostBrowser(logPath, mcpbrowser.NewDirect)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "discovering Chrome CDP endpoint")
}

func (s *MainSuite) TestRunMCPHostBrowserWithConfig() {
	logPath := filepath.Join(s.T().TempDir(), "mcp-host-browser.log")
	s.app.configLoad = func() (*config.Config, error) {
		return &config.Config{
			LogLevel:  "debug",
			LogFormat: "json",
		}, nil
	}
	s.app.discoverWSEndpoint = func() (string, error) {
		return "ws://127.0.0.1:9222/devtools/browser/fake-guid", nil
	}

	called := false
	newServer := func(cdpEndpoint string, logger *slog.Logger) *mcpbrowser.Server {
		require.NotNil(s.T(), logger)
		called = true
		return mcpbrowser.NewDirect(cdpEndpoint, logger)
	}

	_ = s.app.runMCPHostBrowser(logPath, newServer)
	require.True(s.T(), called)
}

func (s *MainSuite) TestNewMCPHostBrowserCmdRunE() {
	logPath := filepath.Join(s.T().TempDir(), "mcp-host-browser.log")
	s.app.discoverWSEndpoint = func() (string, error) {
		return "ws://127.0.0.1:9222/devtools/browser/fake-guid", nil
	}
	cmd := s.app.newMCPHostBrowserCmd()
	require.NoError(s.T(), cmd.Flags().Set("log", logPath))
	// RunE wraps runMCPHostBrowser — the stdio transport will close immediately.
	_ = cmd.RunE(cmd, nil)
}
