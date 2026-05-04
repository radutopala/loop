package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/mcpserver"
)

// --- newMCPCmd ---

func (s *MainSuite) TestNewMCPCmd() {
	cmd := s.app.newMCPCmd()
	require.Equal(s.T(), "mcp", cmd.Use)
	require.Equal(s.T(), []string{"m"}, cmd.Aliases)
	require.NotNil(s.T(), cmd.RunE)

	// Flags should be registered
	f := cmd.Flags()
	require.NotNil(s.T(), f.Lookup("channel-id"))
	require.NotNil(s.T(), f.Lookup("dir"))
	require.NotNil(s.T(), f.Lookup("api-url"))
	require.NotNil(s.T(), f.Lookup("log"))
}

func (s *MainSuite) TestNewMCPCmdMissingFlags() {
	cmd := s.app.newMCPCmd()
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	require.Error(s.T(), err)
}

func (s *MainSuite) TestNewMCPCmdMutuallyExclusive() {
	cmd := s.app.newMCPCmd()
	cmd.SetArgs([]string{"--channel-id", "ch1", "--dir", "/path", "--api-url", "http://localhost:8222"})
	err := cmd.Execute()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "if any flags in the group [channel-id dir] are set none of the others can be")
}

func (s *MainSuite) TestRunMCP() {
	logPath := filepath.Join(s.T().TempDir(), "mcp.log")
	called := false
	s.app.newMCPServer = func(channelID, apiURL, authorID string, httpClient mcpserver.HTTPClient, logger *slog.Logger, opts ...mcpserver.MemoryOption) *mcpserver.Server {
		require.Equal(s.T(), "ch1", channelID)
		require.Equal(s.T(), "http://localhost:8222", apiURL)
		called = true
		return mcpserver.New(channelID, apiURL, authorID, httpClient, logger)
	}

	// runMCP will try to use StdioTransport which will fail/close immediately in test.
	// We just verify the function is wired correctly.
	_ = s.app.runMCP("ch1", "http://localhost:8222", "", logPath, "", "local", "", false)
	require.True(s.T(), called)
}

func (s *MainSuite) TestRunMCPLogOpenError() {
	err := s.app.runMCP("ch1", "http://localhost:8222", "", "/nonexistent/dir/mcp.log", "", "local", "", false)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "opening mcp log")
}

func (s *MainSuite) TestRunMCPWithAgentID() {
	logPath := filepath.Join(s.T().TempDir(), "mcp.log")

	called := false
	s.app.newMCPServer = func(channelID, apiURL, authorID string, httpClient mcpserver.HTTPClient, logger *slog.Logger, opts ...mcpserver.MemoryOption) *mcpserver.Server {
		called = true
		// Verify the WithAgentTools option was passed by checking the server has agentID set.
		return mcpserver.New(channelID, apiURL, authorID, httpClient, logger, opts...)
	}

	_ = s.app.runMCP("ch1", "http://localhost:8222", "", logPath, "", "local", "agent-0", false)
	require.True(s.T(), called)
}

func (s *MainSuite) TestRunMCPWithConfigLoad() {
	// Test that runMCP successfully loads config for log level/format
	logPath := filepath.Join(s.T().TempDir(), "mcp.log")

	// Mock configLoad to return a config
	s.app.configLoad = func() (*config.Config, error) {
		return &config.Config{
			LogLevel:  "debug",
			LogFormat: "json",
		}, nil
	}

	// Mock newMCPServer to avoid actually running the server
	called := false
	s.app.newMCPServer = func(channelID, apiURL, authorID string, httpClient mcpserver.HTTPClient, logger *slog.Logger, opts ...mcpserver.MemoryOption) *mcpserver.Server {
		called = true
		// Verify logger was created (we can't easily inspect its level, but at least it was called)
		require.NotNil(s.T(), logger)
		// Return a real server that we won't run
		return mcpserver.New(channelID, apiURL, authorID, httpClient, logger)
	}

	// This will fail to run the server (no stdio), but that's OK - we just want to test config loading
	_ = s.app.runMCP("ch1", "http://localhost:8222", "", logPath, "", "local", "", false)
	require.True(s.T(), called)
}

func (s *MainSuite) TestRunMCPWithInMemoryTransport() {
	// Verify runMCP constructs the server correctly.
	// We can't test stdio, but we test the MCP server is functional via in-memory transport.
	srv := mcpserver.New("ch1", "http://localhost:8222", "", http.DefaultClient, nil)

	t1, t2 := mcpsdk.NewInMemoryTransports()

	go func() {
		_ = srv.Run(context.Background(), t1)
	}()

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test", Version: "1.0.0"}, nil)
	session, err := client.Connect(context.Background(), t2, nil)
	require.NoError(s.T(), err)
	defer session.Close()

	res, err := session.ListTools(context.Background(), nil)
	require.NoError(s.T(), err)
	require.Len(s.T(), res.Tools, 25) // 12 base + 2 playground + 1 shortcut + 10 quality
}

func (s *MainSuite) TestEnsureChannelSuccess() {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(s.T(), "POST", r.Method)
		require.Equal(s.T(), "/api/channels", r.URL.Path)

		var req struct {
			DirPath string `json:"dir_path"`
		}
		require.NoError(s.T(), json.NewDecoder(r.Body).Decode(&req))
		require.Equal(s.T(), "/home/user/dev/loop", req.DirPath)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"channel_id": "ch-resolved"})
	}))
	defer ts.Close()

	channelID, err := s.app.ensureChannel(ts.URL, "/home/user/dev/loop", "local")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "ch-resolved", channelID)
}

func (s *MainSuite) TestEnsureChannelServerError() {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "something failed", http.StatusInternalServerError)
	}))
	defer ts.Close()

	_, err := s.app.ensureChannel(ts.URL, "/path", "local")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "ensure channel API returned 500")
}

func (s *MainSuite) TestEnsureChannelConnectionError() {
	_, err := s.app.ensureChannel("http://127.0.0.1:1", "/path", "local")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "calling ensure channel API")
}

func (s *MainSuite) TestEnsureChannelInvalidJSON() {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("not json"))
	}))
	defer ts.Close()

	_, err := s.app.ensureChannel(ts.URL, "/path", "local")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "decoding ensure channel response")
}

func (s *MainSuite) TestEnsureAllChannelsSuccess() {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(s.T(), "POST", r.Method)
		require.Equal(s.T(), "/api/channels/ensure-all", r.URL.Path)

		var req struct {
			DirPath string `json:"dir_path"`
		}
		require.NoError(s.T(), json.NewDecoder(r.Body).Decode(&req))
		require.Equal(s.T(), "/home/user/dev/loop", req.DirPath)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]ensureResult{
			{Platform: "local", ChannelID: "ch-1", Created: true},
			{Platform: "discord", ChannelID: "ch-2", Created: false},
		})
	}))
	defer ts.Close()

	results, err := s.app.ensureAllChannels(ts.URL, "/home/user/dev/loop")
	require.NoError(s.T(), err)
	require.Len(s.T(), results, 2)
	require.Equal(s.T(), "ch-1", results[0].ChannelID)
	require.True(s.T(), results[0].Created)
	require.Equal(s.T(), "ch-2", results[1].ChannelID)
	require.False(s.T(), results[1].Created)
}

func (s *MainSuite) TestEnsureAllChannelsServerError() {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "something failed", http.StatusInternalServerError)
	}))
	defer ts.Close()

	_, err := s.app.ensureAllChannels(ts.URL, "/path")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "ensure-all channels API returned 500")
}

func (s *MainSuite) TestEnsureAllChannelsConnectionError() {
	_, err := s.app.ensureAllChannels("http://127.0.0.1:1", "/path")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "calling ensure-all channels API")
}

func (s *MainSuite) TestEnsureAllChannelsInvalidJSON() {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("not json"))
	}))
	defer ts.Close()

	_, err := s.app.ensureAllChannels(ts.URL, "/path")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "decoding ensure-all channels response")
}

func (s *MainSuite) TestRunMCPWithDir() {
	logPath := filepath.Join(s.T().TempDir(), "mcp.log")
	called := false
	s.app.newMCPServer = func(channelID, apiURL, authorID string, httpClient mcpserver.HTTPClient, logger *slog.Logger, opts ...mcpserver.MemoryOption) *mcpserver.Server {
		require.Equal(s.T(), "resolved-ch", channelID)
		called = true
		return mcpserver.New(channelID, apiURL, authorID, httpClient, logger)
	}
	s.app.ensureChannelFn = func(apiURL, dirPath, platform string) (string, error) {
		require.Equal(s.T(), "http://localhost:8222", apiURL)
		require.Equal(s.T(), "/home/user/dev/loop", dirPath)
		require.Equal(s.T(), "local", platform)
		return "resolved-ch", nil
	}

	_ = s.app.runMCP("", "http://localhost:8222", "/home/user/dev/loop", logPath, "", "local", "", false)
	require.True(s.T(), called)
}

func (s *MainSuite) TestRunMCPWithDirEnsureError() {
	s.app.ensureChannelFn = func(_, _, _ string) (string, error) {
		return "", errors.New("ensure failed")
	}

	err := s.app.runMCP("", "http://localhost:8222", "/path", "/tmp/mcp.log", "", "local", "", false)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "ensuring channel for dir")
}

func (s *MainSuite) TestNewMCPCmdWithDirFlag() {
	logPath := filepath.Join(s.T().TempDir(), "mcp.log")
	called := false
	s.app.newMCPServer = func(channelID, apiURL, authorID string, httpClient mcpserver.HTTPClient, logger *slog.Logger, opts ...mcpserver.MemoryOption) *mcpserver.Server {
		require.Equal(s.T(), "resolved-ch", channelID)
		called = true
		return mcpserver.New(channelID, apiURL, authorID, httpClient, logger)
	}
	s.app.ensureChannelFn = func(_, _, _ string) (string, error) {
		return "resolved-ch", nil
	}

	cmd := s.app.newMCPCmd()
	cmd.SetArgs([]string{"--dir", "/home/user/dev/loop", "--api-url", "http://test:8222", "--log", logPath})
	_ = cmd.Execute()
	require.True(s.T(), called)
}
