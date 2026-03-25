package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/api"
	"github.com/radutopala/loop/internal/bot"

	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/container"
	"github.com/radutopala/loop/internal/daemon"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/embeddings"
	"github.com/radutopala/loop/internal/local"
	"github.com/radutopala/loop/internal/mcpbrowser"
	"github.com/radutopala/loop/internal/mcpserver"
	"github.com/radutopala/loop/internal/memory"
	"github.com/radutopala/loop/internal/orchestrator"
	"github.com/radutopala/loop/internal/scheduler"
	"github.com/radutopala/loop/internal/terminal"
	"github.com/radutopala/loop/internal/testutil"
	"github.com/radutopala/loop/internal/types"
)

// --- Mock implementations ---

type mockDockerClient struct {
	mock.Mock
}

func (m *mockDockerClient) ContainerCreate(ctx context.Context, cfg *container.ContainerConfig, name string) (string, error) {
	args := m.Called(ctx, cfg, name)
	return args.String(0), args.Error(1)
}

func (m *mockDockerClient) ContainerLogs(ctx context.Context, containerID string) (io.Reader, error) {
	args := m.Called(ctx, containerID)
	var r io.Reader
	if v := args.Get(0); v != nil {
		r = v.(io.Reader)
	}
	return r, args.Error(1)
}

func (m *mockDockerClient) ContainerLogsFollow(ctx context.Context, containerID string) (io.ReadCloser, error) {
	args := m.Called(ctx, containerID)
	var r io.ReadCloser
	if v := args.Get(0); v != nil {
		r = v.(io.ReadCloser)
	}
	return r, args.Error(1)
}

func (m *mockDockerClient) ContainerStart(ctx context.Context, containerID string) error {
	return m.Called(ctx, containerID).Error(0)
}

func (m *mockDockerClient) ContainerWait(ctx context.Context, containerID string) (<-chan container.WaitResponse, <-chan error) {
	args := m.Called(ctx, containerID)
	return args.Get(0).(<-chan container.WaitResponse), args.Get(1).(<-chan error)
}

func (m *mockDockerClient) ContainerRemove(ctx context.Context, containerID string) error {
	return m.Called(ctx, containerID).Error(0)
}

func (m *mockDockerClient) ContainerStop(ctx context.Context, containerID string) error {
	return m.Called(ctx, containerID).Error(0)
}

func (m *mockDockerClient) ImageList(ctx context.Context, image string) ([]string, error) {
	args := m.Called(ctx, image)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]string), args.Error(1)
}

func (m *mockDockerClient) ImagePull(ctx context.Context, image string) error {
	return m.Called(ctx, image).Error(0)
}

func (m *mockDockerClient) ImageBuild(ctx context.Context, contextDir, tag string) error {
	return m.Called(ctx, contextDir, tag).Error(0)
}

func (m *mockDockerClient) ImageBuildFile(ctx context.Context, contextDir, dockerfile, tag string) error {
	return m.Called(ctx, contextDir, dockerfile, tag).Error(0)
}

func (m *mockDockerClient) ContainerList(ctx context.Context, labelKey, labelValue string) ([]string, error) {
	args := m.Called(ctx, labelKey, labelValue)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]string), args.Error(1)
}

func (m *mockDockerClient) RunningChannelIDs(ctx context.Context) (map[string]struct{}, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(map[string]struct{}), args.Error(1)
}

func (m *mockDockerClient) CopyToContainer(ctx context.Context, containerID, dstPath string, content io.Reader) error {
	return m.Called(ctx, containerID, dstPath, content).Error(0)
}

func (m *mockDockerClient) NetworkEnsure(ctx context.Context, name string) error {
	return m.Called(ctx, name).Error(0)
}

type mockBot struct {
	mock.Mock
}

func (m *mockBot) Start(ctx context.Context) error {
	return m.Called(ctx).Error(0)
}

func (m *mockBot) Stop() error {
	return m.Called().Error(0)
}

func (m *mockBot) SendMessage(ctx context.Context, msg *bot.OutgoingMessage) error {
	return m.Called(ctx, msg).Error(0)
}

func (m *mockBot) SendTyping(ctx context.Context, channelID string) error {
	return m.Called(ctx, channelID).Error(0)
}

func (m *mockBot) RegisterCommands(ctx context.Context) error {
	return m.Called(ctx).Error(0)
}

func (m *mockBot) RemoveCommands(ctx context.Context) error {
	return m.Called(ctx).Error(0)
}

func (m *mockBot) OnMessage(handler func(ctx context.Context, msg *bot.IncomingMessage)) {
	m.Called(handler)
}

func (m *mockBot) OnInteraction(handler func(ctx context.Context, i *bot.Interaction)) {
	m.Called(handler)
}

func (m *mockBot) OnChannelDelete(handler func(ctx context.Context, channelID string, isThread bool)) {
	m.Called(handler)
}

func (m *mockBot) OnChannelJoin(handler func(ctx context.Context, channelID string, platform types.Platform)) {
	m.Called(handler)
}

func (m *mockBot) BotUserID() string {
	return m.Called().String(0)
}

func (m *mockBot) IsBotUser(userID string) bool {
	args := m.Called(userID)
	return args.Bool(0)
}

func (m *mockBot) InviteUserToChannel(ctx context.Context, channelID, userID string) error {
	return m.Called(ctx, channelID, userID).Error(0)
}

func (m *mockBot) SetChannelTopic(ctx context.Context, channelID, topic string) error {
	return m.Called(ctx, channelID, topic).Error(0)
}

func (m *mockBot) CreateThread(ctx context.Context, channelID, name, mentionUserID, message string) (string, error) {
	args := m.Called(ctx, channelID, name, mentionUserID, message)
	return args.String(0), args.Error(1)
}

func (m *mockBot) CreateSimpleThread(ctx context.Context, channelID, name, initialMessage string) (string, error) {
	args := m.Called(ctx, channelID, name, initialMessage)
	return args.String(0), args.Error(1)
}

func (m *mockBot) HandleIncomingMessage(ctx context.Context, channelID, authorID, content, mode string) {
	m.Called(ctx, channelID, authorID, content, mode)
}

func (m *mockBot) HandleThreadCreated(ctx context.Context, threadID, authorID, message string) {
	m.Called(ctx, threadID, authorID, message)
}

func (m *mockBot) DeleteThread(ctx context.Context, threadID string) error {
	return m.Called(ctx, threadID).Error(0)
}

func (m *mockBot) RenameThread(ctx context.Context, threadID, name string) error {
	return m.Called(ctx, threadID, name).Error(0)
}

func (m *mockBot) PostMessage(ctx context.Context, channelID, content string) error {
	return m.Called(ctx, channelID, content).Error(0)
}

func (m *mockBot) GetChannelParentID(ctx context.Context, channelID string) (string, error) {
	args := m.Called(ctx, channelID)
	return args.String(0), args.Error(1)
}

func (m *mockBot) GetChannelName(ctx context.Context, channelID string) (string, error) {
	args := m.Called(ctx, channelID)
	return args.String(0), args.Error(1)
}

func (m *mockBot) SendStopButton(ctx context.Context, channelID, runID string) (string, error) {
	args := m.Called(ctx, channelID, runID)
	return args.String(0), args.Error(1)
}

func (m *mockBot) RemoveStopButton(ctx context.Context, channelID, messageID string) error {
	return m.Called(ctx, channelID, messageID).Error(0)
}

type closableDockerClient struct {
	*mockDockerClient
	closeFn func() error
}

func (c *closableDockerClient) Close() error {
	return c.closeFn()
}

// newPassthroughMock returns a *testutil.MockSystem where every method
// delegates to the real OS implementation by default.  Individual methods
// can then be overridden with sys.Override(...).Return(...).
func newPassthroughMock() *testutil.MockSystem {
	sys := new(testutil.MockSystem)

	// UserHomeDir
	userHomeDirCall := sys.On("UserHomeDir").Maybe().Return("", nil)
	userHomeDirCall.RunFn = func(_ mock.Arguments) {
		dir, err := os.UserHomeDir()
		userHomeDirCall.ReturnArguments = mock.Arguments{dir, err}
	}

	// Stat
	statCall := sys.On("Stat", mock.Anything).Maybe().Return(nil, nil)
	statCall.RunFn = func(args mock.Arguments) {
		info, err := os.Stat(args.String(0))
		statCall.ReturnArguments = mock.Arguments{info, err}
	}

	// MkdirAll
	mkdirAllCall := sys.On("MkdirAll", mock.Anything, mock.Anything).Maybe().Return(nil)
	mkdirAllCall.RunFn = func(args mock.Arguments) {
		err := os.MkdirAll(args.String(0), args.Get(1).(os.FileMode))
		mkdirAllCall.ReturnArguments = mock.Arguments{err}
	}

	// WriteFile
	writeFileCall := sys.On("WriteFile", mock.Anything, mock.Anything, mock.Anything).Maybe().Return(nil)
	writeFileCall.RunFn = func(args mock.Arguments) {
		err := os.WriteFile(args.String(0), args.Get(1).([]byte), args.Get(2).(os.FileMode))
		writeFileCall.ReturnArguments = mock.Arguments{err}
	}

	// ReadFile
	readFileCall := sys.On("ReadFile", mock.Anything).Maybe().Return(nil, nil)
	readFileCall.RunFn = func(args mock.Arguments) {
		data, err := os.ReadFile(args.String(0))
		readFileCall.ReturnArguments = mock.Arguments{data, err}
	}

	// Getwd
	getwdCall := sys.On("Getwd").Maybe().Return("", nil)
	getwdCall.RunFn = func(_ mock.Arguments) {
		dir, err := os.Getwd()
		getwdCall.ReturnArguments = mock.Arguments{dir, err}
	}

	// Remove
	removeCall := sys.On("Remove", mock.Anything).Maybe().Return(nil)
	removeCall.RunFn = func(args mock.Arguments) {
		err := os.Remove(args.String(0))
		removeCall.ReturnArguments = mock.Arguments{err}
	}

	// Executable
	executableCall := sys.On("Executable").Maybe().Return("", nil)
	executableCall.RunFn = func(_ mock.Arguments) {
		path, err := os.Executable()
		executableCall.ReturnArguments = mock.Arguments{path, err}
	}

	// EvalSymlinks
	evalSymlinksCall := sys.On("EvalSymlinks", mock.Anything).Maybe().Return("", nil)
	evalSymlinksCall.RunFn = func(args mock.Arguments) {
		resolved, err := filepath.EvalSymlinks(args.String(0))
		evalSymlinksCall.ReturnArguments = mock.Arguments{resolved, err}
	}

	// Chmod
	chmodCall := sys.On("Chmod", mock.Anything, mock.Anything).Maybe().Return(nil)
	chmodCall.RunFn = func(args mock.Arguments) {
		err := os.Chmod(args.String(0), args.Get(1).(os.FileMode))
		chmodCall.ReturnArguments = mock.Arguments{err}
	}

	// Rename
	renameCall := sys.On("Rename", mock.Anything, mock.Anything).Maybe().Return(nil)
	renameCall.RunFn = func(args mock.Arguments) {
		err := os.Rename(args.String(0), args.String(1))
		renameCall.ReturnArguments = mock.Arguments{err}
	}

	// CreateTemp
	createTempCall := sys.On("CreateTemp", mock.Anything, mock.Anything).Maybe().Return(nil, nil)
	createTempCall.RunFn = func(args mock.Arguments) {
		f, err := os.CreateTemp(args.String(0), args.String(1))
		createTempCall.ReturnArguments = mock.Arguments{f, err}
	}

	return sys
}

// --- Test Suite ---

type MainSuite struct {
	suite.Suite
	app *app
}

func TestMainSuite(t *testing.T) {
	suite.Run(t, new(MainSuite))
}

func (s *MainSuite) SetupTest() {
	s.app = newApp()
	s.app.loadProjectMemoryPaths = func(_ string) []string { return nil }
}

func testConfig() *config.Config {
	return &config.Config{
		Platforms:    []types.Platform{types.PlatformDiscord},
		DiscordToken: "test-token",
		DiscordAppID: "test-app",
		LogLevel:     "info",
		LogFormat:    "text",
		DBPath:       "test.db",
		PollInterval: time.Hour,
		APIAddr:      "127.0.0.1:0",
	}
}

func testSlackConfig() *config.Config {
	return &config.Config{
		Platforms:     []types.Platform{types.PlatformSlack},
		SlackBotToken: "xoxb-test-token",
		SlackAppToken: "xapp-test-token",
		LogLevel:      "info",
		LogFormat:     "text",
		DBPath:        "test.db",
		PollInterval:  time.Hour,
		APIAddr:       "127.0.0.1:0",
	}
}

// fakeAPIServer returns a newAPIServer func that creates a real api.Server
// but binds to a random port (127.0.0.1:0).
func fakeAPIServer() func(scheduler.Scheduler, api.ChannelEnsurer, api.ThreadEnsurer, api.ChannelLister, api.MessageSender, *slog.Logger) *api.Server {
	return api.NewServer
}

// serveSetupMocks creates and wires the standard mock objects for serve() tests.
// It returns the mocks so callers can add extra expectations or adjust config.
type serveMocks struct {
	store        *testutil.MockStore
	bot          *mockBot
	dockerClient *mockDockerClient
	cfg          *config.Config
}

func (s *MainSuite) setupServeMocks() *serveMocks {
	m := &serveMocks{
		store:        new(testutil.MockStore),
		bot:          new(mockBot),
		dockerClient: new(mockDockerClient),
		cfg:          testConfig(),
	}
	m.store.On("Close").Return(nil)
	s.app.configLoad = func() (*config.Config, error) { return m.cfg, nil }
	s.app.newSQLiteStore = func(_ string) (db.Store, error) { return m.store, nil }
	s.app.newDiscordBot = func(_, _, _ string, _ *slog.Logger) (orchestrator.Bot, error) { return m.bot, nil }
	s.app.newLocalBot = func(_ db.Store, _ *slog.Logger) orchestrator.Bot { return m.bot }
	s.app.newDockerClient = func() (container.DockerClient, error) { return m.dockerClient, nil }
	s.app.ensureImage = func(_ context.Context, _ container.DockerClient, _ *config.Config) error { return nil }
	s.app.newDockerExecClient = func() (terminal.ExecClient, error) { return nil, errors.New("no docker") }
	s.app.newHostExecClient = func() terminal.ExecClient { return &noopExecClient{} }
	s.app.newBrowserProvider = func(_ string, _ *slog.Logger) (api.BrowserProvider, error) { return nil, errors.New("no browser") }
	s.app.newAPIServer = fakeAPIServer()
	return m
}

func (m *serveMocks) setupHappyBot() {
	m.bot.On("OnMessage", mock.Anything).Return()
	m.bot.On("OnInteraction", mock.Anything).Return()
	m.bot.On("OnChannelDelete", mock.Anything).Return()
	m.bot.On("OnChannelJoin", mock.Anything).Return()
	m.bot.On("RegisterCommands", mock.Anything).Return(nil)
	m.bot.On("Start", mock.Anything).Return(nil)
	m.bot.On("Stop").Return(nil)
	m.dockerClient.On("ContainerList", mock.Anything, "app", "loop-agent").Return([]string{}, nil)
}

// filterExpected removes mock expectations for the given method name.
func filterExpected(calls []*mock.Call, method string) []*mock.Call {
	filtered := make([]*mock.Call, 0, len(calls))
	for _, c := range calls {
		if c.Method != method {
			filtered = append(filtered, c)
		}
	}
	return filtered
}

// --- newRootCmd ---

func (s *MainSuite) TestNewRootCmd() {
	cmd := s.app.newRootCmd()
	require.Equal(s.T(), "loop", cmd.Use)
	require.True(s.T(), cmd.HasSubCommands())

	want := map[string]bool{
		"serve":          false,
		"mcp":            false,
		"daemon:start":   false,
		"daemon:stop":    false,
		"daemon:restart": false,
		"daemon:status":  false,
		"onboard:global": false,
		"onboard:local":  false,
		"version":        false,
		"readme":         false,
		"update":         false,
		"mcp-browser":    false,
	}
	for _, sub := range cmd.Commands() {
		if _, ok := want[sub.Use]; ok {
			want[sub.Use] = true
		}
	}
	for name, found := range want {
		require.True(s.T(), found, "root command should have %s subcommand", name)
	}
}

// --- newServeCmd ---

func (s *MainSuite) TestNewServeCmd() {
	cmd := s.app.newServeCmd()
	require.Equal(s.T(), "serve", cmd.Use)
	require.Equal(s.T(), []string{"s"}, cmd.Aliases)
	require.NotNil(s.T(), cmd.RunE)

	// Exercise the RunE closure to cover it.
	s.app.configLoad = func() (*config.Config, error) {
		return nil, errors.New("test")
	}
	err := cmd.RunE(nil, nil)
	require.Error(s.T(), err)
}

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
	require.Len(s.T(), res.Tools, 12)
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

// --- memoryDir ---

func (s *MainSuite) TestMemoryDir() {
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return("/home/testuser", nil)
	dir, err := s.app.memoryDir("/Users/dev/loop")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "/home/testuser/.claude/projects/-Users-dev-loop/memory", dir)
}

func (s *MainSuite) TestMemoryDirDotPaths() {
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return("/home/testuser", nil)
	dir, err := s.app.memoryDir("/Users/me/.loop/work")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "/home/testuser/.claude/projects/-Users-me--loop-work/memory", dir)
}

func (s *MainSuite) TestMemoryDirHomeDirError() {
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return("", errors.New("no home"))
	_, err := s.app.memoryDir("/path")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "getting home directory")
}

// --- multiDirIndexer ---

type mockMemIndexer struct {
	mock.Mock
}

func (m *mockMemIndexer) Index(ctx context.Context, memoryPath, dirPath string, excludePaths []string) (int, error) {
	args := m.Called(ctx, memoryPath, dirPath, excludePaths)
	return args.Int(0), args.Error(1)
}

func (m *mockMemIndexer) Search(ctx context.Context, dirPath, query string, topK int) ([]memory.SearchResult, error) {
	args := m.Called(ctx, dirPath, query, topK)
	return args.Get(0).([]memory.SearchResult), args.Error(1)
}

type fakeEmbedder struct{}

func (f *fakeEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	result := make([][]float32, len(texts))
	for i := range texts {
		result[i] = []float32{0.1, 0.2, 0.3}
	}
	return result, nil
}

func (f *fakeEmbedder) Dimensions() int { return 3 }

func (s *MainSuite) TestMultiDirIndexerResolveMemoryPaths() {
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return("/home/test", nil)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := new(testutil.MockStore)
	indexer := memory.NewIndexer(&fakeEmbedder{}, store, logger, 0)
	mdi := &multiDirIndexer{indexer: indexer, logger: logger, globalMemoryPaths: []string{"./memory"}, app: s.app}

	entries, excludePaths := mdi.resolveMemoryPaths("/home/user/project")
	require.Len(s.T(), entries, 5)
	require.Empty(s.T(), excludePaths)
	require.Contains(s.T(), entries[0].path, ".claude/projects")
	require.False(s.T(), entries[0].global)
	// CLAUDE.md entries: global, project root, project .claude/
	require.Equal(s.T(), "/home/test/.claude/CLAUDE.md", entries[1].path)
	require.True(s.T(), entries[1].global)
	require.Equal(s.T(), "/home/user/project/CLAUDE.md", entries[2].path)
	require.False(s.T(), entries[2].global)
	require.Equal(s.T(), "/home/user/project/.claude/CLAUDE.md", entries[3].path)
	require.False(s.T(), entries[3].global)
	require.Equal(s.T(), "/home/user/project/memory", entries[4].path)
	require.False(s.T(), entries[4].global) // relative config path
}

func (s *MainSuite) TestMultiDirIndexerResolveMemoryPathsHomeDirError() {
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return("", errors.New("no home"))

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := new(testutil.MockStore)
	indexer := memory.NewIndexer(&fakeEmbedder{}, store, logger, 0)
	mdi := &multiDirIndexer{indexer: indexer, logger: logger, globalMemoryPaths: []string{"./memory"}, app: s.app}

	entries, excludePaths := mdi.resolveMemoryPaths("/path")
	require.Len(s.T(), entries, 3)
	require.Empty(s.T(), excludePaths)
	// No auto-memory or global CLAUDE.md when home dir fails.
	require.Equal(s.T(), "/path/CLAUDE.md", entries[0].path)
	require.False(s.T(), entries[0].global)
	require.Equal(s.T(), "/path/.claude/CLAUDE.md", entries[1].path)
	require.False(s.T(), entries[1].global)
	require.Equal(s.T(), "/path/memory", entries[2].path)
	require.False(s.T(), entries[2].global)
}

func (s *MainSuite) TestMultiDirIndexerResolveMemoryPathsWithGlobalAndProject() {
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return("/home/test", nil)
	s.app.loadProjectMemoryPaths = func(_ string) []string { return []string{"./docs/arch.md"} }

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := new(testutil.MockStore)
	indexer := memory.NewIndexer(&fakeEmbedder{}, store, logger, 0)
	mdi := &multiDirIndexer{
		indexer:           indexer,
		logger:            logger,
		globalMemoryPaths: []string{"/shared/knowledge"},
		app:               s.app,
	}

	entries, excludePaths := mdi.resolveMemoryPaths("/home/user/project")
	require.Len(s.T(), entries, 6)
	require.Empty(s.T(), excludePaths)
	require.Contains(s.T(), entries[0].path, ".claude/projects")
	require.False(s.T(), entries[0].global)
	// CLAUDE.md entries
	require.Equal(s.T(), "/home/test/.claude/CLAUDE.md", entries[1].path)
	require.True(s.T(), entries[1].global)
	require.Equal(s.T(), "/home/user/project/CLAUDE.md", entries[2].path)
	require.False(s.T(), entries[2].global)
	require.Equal(s.T(), "/home/user/project/.claude/CLAUDE.md", entries[3].path)
	require.False(s.T(), entries[3].global)
	require.Equal(s.T(), "/shared/knowledge", entries[4].path)
	require.True(s.T(), entries[4].global) // absolute config path
	require.Equal(s.T(), "/home/user/project/docs/arch.md", entries[5].path)
	require.False(s.T(), entries[5].global) // relative project path
}

func (s *MainSuite) TestMultiDirIndexerResolveMemoryPathsDedup() {
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return("/home/test", nil)
	// Project config returns paths that duplicate a global path.
	s.app.loadProjectMemoryPaths = func(_ string) []string {
		return []string{"./memory", "/shared/knowledge"}
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := new(testutil.MockStore)
	indexer := memory.NewIndexer(&fakeEmbedder{}, store, logger, 0)
	mdi := &multiDirIndexer{
		indexer:           indexer,
		logger:            logger,
		globalMemoryPaths: []string{"./memory", "/shared/knowledge"},
		app:               s.app,
	}

	entries, excludePaths := mdi.resolveMemoryPaths("/home/user/project")
	// Should be deduplicated: auto-memory, CLAUDE.md x3, project/memory, /shared/knowledge — no duplicates.
	require.Len(s.T(), entries, 6)
	require.Empty(s.T(), excludePaths)
	require.Contains(s.T(), entries[0].path, ".claude/projects")
	require.Equal(s.T(), "/home/test/.claude/CLAUDE.md", entries[1].path)
	require.Equal(s.T(), "/home/user/project/CLAUDE.md", entries[2].path)
	require.Equal(s.T(), "/home/user/project/.claude/CLAUDE.md", entries[3].path)
	require.Equal(s.T(), "/home/user/project/memory", entries[4].path)
	require.Equal(s.T(), "/shared/knowledge", entries[5].path)
	require.True(s.T(), entries[5].global)
}

func (s *MainSuite) TestResolveMemoryPathsWithExclusions() {
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return("/home/test", nil)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := new(testutil.MockStore)
	indexer := memory.NewIndexer(&fakeEmbedder{}, store, logger, 0)
	mdi := &multiDirIndexer{
		indexer:           indexer,
		logger:            logger,
		globalMemoryPaths: []string{"./memory", "!./memory/drafts"},
		app:               s.app,
	}

	entries, excludePaths := mdi.resolveMemoryPaths("/home/user/project")
	require.Len(s.T(), entries, 5) // auto-memory + CLAUDE.md x3 + ./memory
	require.Len(s.T(), excludePaths, 1)
	require.Equal(s.T(), "/home/user/project/memory/drafts", excludePaths[0])
}

func (s *MainSuite) TestResolveMemoryPathsAbsoluteExclusion() {
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return("/home/test", nil)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := new(testutil.MockStore)
	indexer := memory.NewIndexer(&fakeEmbedder{}, store, logger, 0)
	mdi := &multiDirIndexer{
		indexer:           indexer,
		logger:            logger,
		globalMemoryPaths: []string{"./memory", "!/shared/secret"},
		app:               s.app,
	}

	entries, excludePaths := mdi.resolveMemoryPaths("/home/user/project")
	require.Len(s.T(), entries, 5) // auto-memory + CLAUDE.md x3 + ./memory
	require.Len(s.T(), excludePaths, 1)
	require.Equal(s.T(), "/shared/secret", excludePaths[0])
}

func (s *MainSuite) TestResolveMemoryPathsProjectExclusion() {
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return("/home/test", nil)
	s.app.loadProjectMemoryPaths = func(_ string) []string {
		return []string{"./docs", "!./docs/wip"}
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := new(testutil.MockStore)
	indexer := memory.NewIndexer(&fakeEmbedder{}, store, logger, 0)
	mdi := &multiDirIndexer{indexer: indexer, logger: logger, app: s.app}

	entries, excludePaths := mdi.resolveMemoryPaths("/home/user/project")
	require.Len(s.T(), entries, 5) // auto-memory + CLAUDE.md x3 + ./docs
	require.Len(s.T(), excludePaths, 1)
	require.Equal(s.T(), "/home/user/project/docs/wip", excludePaths[0])
}

func (s *MainSuite) TestResolveRelativePath() {
	require.Equal(s.T(), "/project/memory", resolveRelativePath("/project", "./memory"))
	require.Equal(s.T(), "/project/docs/arch.md", resolveRelativePath("/project", "./docs/arch.md"))
	require.Equal(s.T(), "/project/notes.md", resolveRelativePath("/project", "notes.md"))
	require.Equal(s.T(), "/absolute/path", resolveRelativePath("/project", "/absolute/path"))
}

func (s *MainSuite) TestLoadProjectMemoryPathsDefault() {
	tmpDir := s.T().TempDir()
	loopDir := filepath.Join(tmpDir, ".loop")
	require.NoError(s.T(), os.MkdirAll(loopDir, 0755))
	require.NoError(s.T(), os.WriteFile(
		filepath.Join(loopDir, "config.json"),
		[]byte(`{"memory": {"paths": ["/extra/docs", "./notes.md"]}}`),
		0644,
	))

	paths := s.app.defaultLoadProjectMemoryPaths(tmpDir)
	require.Equal(s.T(), []string{"/extra/docs", "./notes.md"}, paths)
}

func (s *MainSuite) TestLoadProjectMemoryPathsHJSON() {
	tmpDir := s.T().TempDir()
	loopDir := filepath.Join(tmpDir, ".loop")
	require.NoError(s.T(), os.MkdirAll(loopDir, 0755))
	require.NoError(s.T(), os.WriteFile(
		filepath.Join(loopDir, "config.json"),
		[]byte(`{
			// A comment
			"memory": {"paths": ["/docs"]},
		}`),
		0644,
	))

	paths := s.app.defaultLoadProjectMemoryPaths(tmpDir)
	require.Equal(s.T(), []string{"/docs"}, paths)
}

func (s *MainSuite) TestLoadProjectMemoryPathsMissingFile() {
	paths := s.app.defaultLoadProjectMemoryPaths("/nonexistent")
	require.Nil(s.T(), paths)
}

func (s *MainSuite) TestLoadProjectMemoryPathsInvalidJSON() {
	tmpDir := s.T().TempDir()
	loopDir := filepath.Join(tmpDir, ".loop")
	require.NoError(s.T(), os.MkdirAll(loopDir, 0755))
	require.NoError(s.T(), os.WriteFile(
		filepath.Join(loopDir, "config.json"),
		[]byte(`{not valid`),
		0644,
	))

	paths := s.app.defaultLoadProjectMemoryPaths(tmpDir)
	require.Nil(s.T(), paths)
}

func (s *MainSuite) TestLoadProjectMemoryPathsNoMemoryPaths() {
	tmpDir := s.T().TempDir()
	loopDir := filepath.Join(tmpDir, ".loop")
	require.NoError(s.T(), os.MkdirAll(loopDir, 0755))
	require.NoError(s.T(), os.WriteFile(
		filepath.Join(loopDir, "config.json"),
		[]byte(`{"claude_model": "opus"}`),
		0644,
	))

	paths := s.app.defaultLoadProjectMemoryPaths(tmpDir)
	require.Nil(s.T(), paths)
}

func (s *MainSuite) TestMultiDirIndexerSearch() {
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return(s.T().TempDir(), nil)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := new(testutil.MockStore)
	store.On("GetMemoryFilesByDirPath", mock.Anything, mock.Anything).Return([]*db.MemoryFile{}, nil)
	indexer := memory.NewIndexer(&fakeEmbedder{}, store, logger, 0)
	mdi := &multiDirIndexer{indexer: indexer, logger: logger, app: s.app}

	ctx := context.Background()
	results, err := mdi.Search(ctx, "/nonexistent/project", "test", 5)
	require.NoError(s.T(), err)
	require.Empty(s.T(), results)
}

func (s *MainSuite) TestMultiDirIndexerSearchWithError() {
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return(s.T().TempDir(), nil)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := new(testutil.MockStore)
	// GetMemoryFilesByDirPath returning an error triggers the error path
	store.On("GetMemoryFilesByDirPath", mock.Anything, mock.Anything).Return(nil, errors.New("db error"))
	indexer := memory.NewIndexer(&fakeEmbedder{}, store, logger, 0)
	mdi := &multiDirIndexer{indexer: indexer, logger: logger, app: s.app}

	ctx := context.Background()
	results, err := mdi.Search(ctx, "/nonexistent/project", "test", 5)
	require.Error(s.T(), err)
	require.Nil(s.T(), results)
}

func (s *MainSuite) TestMultiDirIndexerIndex() {
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return(s.T().TempDir(), nil)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := new(testutil.MockStore)
	indexer := memory.NewIndexer(&fakeEmbedder{}, store, logger, 0)
	mdi := &multiDirIndexer{indexer: indexer, logger: logger, globalMemoryPaths: []string{"./memory"}, app: s.app}

	ctx := context.Background()
	count, err := mdi.Index(ctx, "/nonexistent/project")
	require.NoError(s.T(), err)
	require.Equal(s.T(), 0, count)
}

func (s *MainSuite) TestMultiDirIndexerIndexWithError() {
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return("/nonexistent-home", nil)

	mi := new(mockMemIndexer)
	mi.On("Index", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(0, errors.New("stat error"))

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mdi := &multiDirIndexer{indexer: mi, logger: logger, globalMemoryPaths: []string{"./memory"}, app: s.app}

	ctx := context.Background()
	count, err := mdi.Index(ctx, "/some/project")
	require.NoError(s.T(), err)
	require.Equal(s.T(), 0, count) // Error was logged, not returned
	mi.AssertExpectations(s.T())
}

func (s *MainSuite) TestMultiDirIndexerIndexWithCount() {
	tmpDir := s.T().TempDir()
	memDir := filepath.Join(tmpDir, "memory")
	require.NoError(s.T(), os.MkdirAll(memDir, 0755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(memDir, "notes.md"), []byte("## Topic\nSome content\n"), 0644))

	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return("/nonexistent-home", nil)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := new(testutil.MockStore)
	store.On("GetMemoryFileHash", mock.Anything, mock.Anything, mock.Anything).Return("", nil)
	store.On("UpsertMemoryFile", mock.Anything, mock.Anything).Return(nil)
	indexer := memory.NewIndexer(&fakeEmbedder{}, store, logger, 0)
	mdi := &multiDirIndexer{indexer: indexer, logger: logger, globalMemoryPaths: []string{"./memory"}, app: s.app}

	ctx := context.Background()
	count, err := mdi.Index(ctx, tmpDir)
	require.NoError(s.T(), err)
	require.Greater(s.T(), count, 0) // Should have indexed files
}

func (s *MainSuite) TestMultiDirIndexerSearchWithSortAndTopK() {
	tmpDir := s.T().TempDir()
	memDir := filepath.Join(tmpDir, "memory")
	require.NoError(s.T(), os.MkdirAll(memDir, 0755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(memDir, "a.md"), []byte("content a"), 0644))
	require.NoError(s.T(), os.WriteFile(filepath.Join(memDir, "b.md"), []byte("content b"), 0644))

	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return("/nonexistent-home", nil)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := new(testutil.MockStore)
	store.On("GetMemoryFileHash", mock.Anything, mock.Anything, mock.Anything).Return("", nil)
	store.On("UpsertMemoryFile", mock.Anything, mock.Anything).Return(nil)
	emb1 := embeddings.SerializeFloat32([]float32{0.1, 0.2, 0.3})
	emb2 := embeddings.SerializeFloat32([]float32{0.3, 0.2, 0.1})
	store.On("GetMemoryFilesByDirPath", mock.Anything, mock.Anything).Return([]*db.MemoryFile{
		{FilePath: "a.md", Content: "content a", Embedding: emb1, Dimensions: 3},
		{FilePath: "b.md", Content: "content b", Embedding: emb2, Dimensions: 3},
	}, nil)
	indexer := memory.NewIndexer(&fakeEmbedder{}, store, logger, 0)
	mdi := &multiDirIndexer{indexer: indexer, logger: logger, globalMemoryPaths: []string{"./memory"}, app: s.app}

	ctx := context.Background()
	results, err := mdi.Search(ctx, tmpDir, "test query", 1)
	require.NoError(s.T(), err)
	require.Len(s.T(), results, 1) // topK=1 truncates to 1 result
}

func (s *MainSuite) TestMultiDirIndexerSearchWithGlobalPath() {
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return("/nonexistent-home", nil)

	mi := new(mockMemIndexer)
	// Auto-memory + project CLAUDE.md paths (project-scoped)
	mi.On("Index", mock.Anything, mock.Anything, "/some/project", mock.Anything).Return(0, nil)
	// Global paths (global CLAUDE.md + absolute config path, scope = "")
	mi.On("Index", mock.Anything, mock.Anything, "", mock.Anything).Return(0, nil)
	mi.On("Search", mock.Anything, "/some/project", "test", 5).Return([]memory.SearchResult{}, nil)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mdi := &multiDirIndexer{indexer: mi, logger: logger, globalMemoryPaths: []string{"/shared/knowledge"}, app: s.app}

	ctx := context.Background()
	results, err := mdi.Search(ctx, "/some/project", "test", 5)
	require.NoError(s.T(), err)
	require.Empty(s.T(), results)
	mi.AssertExpectations(s.T())
}

func (s *MainSuite) TestMultiDirIndexerSearchWithIndexError() {
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return("/nonexistent-home", nil)

	mi := new(mockMemIndexer)
	// Auto-memory path fails
	mi.On("Index", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(0, errors.New("index error"))
	mi.On("Search", mock.Anything, "/some/project", "test", 5).Return([]memory.SearchResult{}, nil)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mdi := &multiDirIndexer{indexer: mi, logger: logger, app: s.app}

	ctx := context.Background()
	results, err := mdi.Search(ctx, "/some/project", "test", 5)
	require.NoError(s.T(), err) // Error was logged, not returned
	require.Empty(s.T(), results)
}

func (s *MainSuite) TestMultiDirIndexerIndexWithGlobalPath() {
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return("/nonexistent-home", nil)

	mi := new(mockMemIndexer)
	// Auto-memory + project CLAUDE.md paths (project-scoped)
	mi.On("Index", mock.Anything, mock.Anything, "/some/project", mock.Anything).Return(1, nil)
	// Global paths (global CLAUDE.md + absolute config path, scope = "")
	mi.On("Index", mock.Anything, mock.Anything, "", mock.Anything).Return(2, nil)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mdi := &multiDirIndexer{indexer: mi, logger: logger, globalMemoryPaths: []string{"/shared/knowledge"}, app: s.app}

	ctx := context.Background()
	count, err := mdi.Index(ctx, "/some/project")
	require.NoError(s.T(), err)
	require.Equal(s.T(), 7, count) // 3 project-scoped (1 each) + 2 global-scoped (2 each)
	mi.AssertExpectations(s.T())
}

// --- reindexAll ---

type mockChannelLister struct {
	mock.Mock
}

func (m *mockChannelLister) ListChannels(ctx context.Context) ([]*db.Channel, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*db.Channel), args.Error(1)
}

func (s *MainSuite) TestReindexAll() {
	tmpDir := s.T().TempDir()
	memDir := filepath.Join(tmpDir, "memory")
	require.NoError(s.T(), os.MkdirAll(memDir, 0755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(memDir, "notes.md"), []byte("## Topic\nSome content\n"), 0644))

	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return("/nonexistent-home", nil)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := new(testutil.MockStore)
	store.On("GetMemoryFileHash", mock.Anything, mock.Anything, mock.Anything).Return("", nil)
	store.On("UpsertMemoryFile", mock.Anything, mock.Anything).Return(nil)
	store.On("ListChannels", mock.Anything).Return([]*db.Channel{
		{ChannelID: "ch1", DirPath: tmpDir},
		{ChannelID: "ch2", DirPath: ""},             // empty dir_path — skipped
		{ChannelID: "ch3", DirPath: "/nonexistent"}, // no files — 0 indexed
	}, nil)
	indexer := memory.NewIndexer(&fakeEmbedder{}, store, logger, 0)
	mdi := &multiDirIndexer{indexer: indexer, logger: logger, globalMemoryPaths: []string{"./memory"}, app: s.app}

	mdi.reindexAll(context.Background(), store)
	store.AssertExpectations(s.T())
}

func (s *MainSuite) TestReindexAllListError() {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := new(testutil.MockStore)
	indexer := memory.NewIndexer(&fakeEmbedder{}, store, logger, 0)
	mdi := &multiDirIndexer{indexer: indexer, logger: logger, app: s.app}

	store.On("ListChannels", mock.Anything).Return(nil, errors.New("db error"))

	mdi.reindexAll(context.Background(), store)
	store.AssertExpectations(s.T())
}

func (s *MainSuite) TestReindexAllCancelledContext() {
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return("/nonexistent-home", nil)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := new(testutil.MockStore)
	indexer := memory.NewIndexer(&fakeEmbedder{}, store, logger, 0)
	mdi := &multiDirIndexer{indexer: indexer, logger: logger, app: s.app}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	cl := new(mockChannelLister)
	cl.On("ListChannels", mock.Anything).Return([]*db.Channel{
		{ChannelID: "ch1", DirPath: "/some/path"},
	}, nil)

	mdi.reindexAll(ctx, cl)
	cl.AssertExpectations(s.T())
	// Index should not be called because ctx is cancelled.
}

// --- reindexLoop ---

func (s *MainSuite) TestReindexLoop() {
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return("/nonexistent-home", nil)

	mi := new(mockMemIndexer)
	mi.On("Index", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(0, nil)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mdi := &multiDirIndexer{indexer: mi, logger: logger, app: s.app}

	var callCount atomic.Int32
	cl := new(mockChannelLister)
	cl.On("ListChannels", mock.Anything).Run(func(_ mock.Arguments) {
		callCount.Add(1)
	}).Return([]*db.Channel{
		{ChannelID: "ch1", DirPath: "/some/path"},
	}, nil)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		mdi.reindexLoop(ctx, cl, 1) // 1-second interval
		close(done)
	}()

	// Wait for at least 2 ListChannels calls (startup + one tick).
	require.Eventually(s.T(), func() bool {
		return callCount.Load() >= 2
	}, 5*time.Second, 100*time.Millisecond)

	cancel()
	<-done
}

func (s *MainSuite) TestReindexLoopDefaultInterval() {
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return("/nonexistent-home", nil)

	mi := new(mockMemIndexer)
	mi.On("Index", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(0, nil)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mdi := &multiDirIndexer{indexer: mi, logger: logger, app: s.app}

	var callCount atomic.Int32
	cl := new(mockChannelLister)
	cl.On("ListChannels", mock.Anything).Run(func(_ mock.Arguments) {
		callCount.Add(1)
	}).Return([]*db.Channel{}, nil)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		mdi.reindexLoop(ctx, cl, 0) // 0 = default interval
		close(done)
	}()

	// Wait for the startup reindexAll call.
	require.Eventually(s.T(), func() bool {
		return callCount.Load() >= 1
	}, 2*time.Second, 50*time.Millisecond)

	cancel()
	<-done
}

// --- newEmbedder ---

func (s *MainSuite) TestNewEmbedderOllama() {
	cfg := &config.Config{
		Memory: config.MemoryConfig{Enabled: true, Embeddings: config.EmbeddingsConfig{
			Provider:  "ollama",
			Model:     "nomic-embed-text",
			OllamaURL: "http://localhost:11434",
		}},
	}
	embedder, err := s.app.defaultNewEmbedder(cfg)
	require.NoError(s.T(), err)
	require.NotNil(s.T(), embedder)
}

func (s *MainSuite) TestNewEmbedderOllamaDefaultModel() {
	cfg := &config.Config{
		Memory: config.MemoryConfig{Enabled: true, Embeddings: config.EmbeddingsConfig{
			Provider:  "ollama",
			OllamaURL: "http://localhost:11434",
		}},
	}
	embedder, err := s.app.defaultNewEmbedder(cfg)
	require.NoError(s.T(), err)
	require.NotNil(s.T(), embedder)
}

func (s *MainSuite) TestNewEmbedderUnsupportedProvider() {
	cfg := &config.Config{
		Memory: config.MemoryConfig{Enabled: true, Embeddings: config.EmbeddingsConfig{
			Provider: "unknown",
		}},
	}
	_, err := s.app.defaultNewEmbedder(cfg)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "unsupported embeddings provider")
}

// --- runMCP with embeddings ---

func (s *MainSuite) TestRunMCPWithMemoryEnabled() {
	logPath := filepath.Join(s.T().TempDir(), "mcp.log")

	s.app.configLoad = func() (*config.Config, error) {
		return &config.Config{
			LogLevel:  "info",
			LogFormat: "text",
			Memory: config.MemoryConfig{
				Enabled: true,
				Embeddings: config.EmbeddingsConfig{
					Provider:  "ollama",
					OllamaURL: "http://localhost:11434",
				},
			},
		}, nil
	}

	s.app.ensureChannelFn = func(_, _, _ string) (string, error) {
		return "resolved-ch", nil
	}

	memoryOptReceived := false
	s.app.newMCPServer = func(channelID, apiURL, authorID string, httpClient mcpserver.HTTPClient, logger *slog.Logger, opts ...mcpserver.MemoryOption) *mcpserver.Server {
		require.Equal(s.T(), "resolved-ch", channelID)
		if len(opts) > 0 {
			memoryOptReceived = true
		}
		return mcpserver.New(channelID, apiURL, authorID, httpClient, logger, opts...)
	}

	_ = s.app.runMCP("", "http://localhost:8222", "/home/user/dev/loop", logPath, "", "local", "", false)
	require.True(s.T(), memoryOptReceived)
}

func (s *MainSuite) TestRunMCPWithMemoryEnabledChannelIDMode() {
	// When dirPath is empty (channel-id mode), memory should still be enabled via channel_id
	logPath := filepath.Join(s.T().TempDir(), "mcp.log")

	s.app.configLoad = func() (*config.Config, error) {
		return &config.Config{
			LogLevel:  "info",
			LogFormat: "text",
			Memory: config.MemoryConfig{
				Enabled: true,
				Embeddings: config.EmbeddingsConfig{
					Provider: "ollama",
				},
			},
		}, nil
	}

	memoryOptReceived := false
	s.app.newMCPServer = func(channelID, apiURL, authorID string, httpClient mcpserver.HTTPClient, logger *slog.Logger, opts ...mcpserver.MemoryOption) *mcpserver.Server {
		if len(opts) > 0 {
			memoryOptReceived = true
		}
		return mcpserver.New(channelID, apiURL, authorID, httpClient, logger, opts...)
	}

	_ = s.app.runMCP("ch1", "http://localhost:8222", "", logPath, "", "local", "", false)
	require.True(s.T(), memoryOptReceived)
}

func (s *MainSuite) TestRunMCPWithMemoryNotEnabled() {
	// When memory is not enabled, memory tools should NOT be wired
	logPath := filepath.Join(s.T().TempDir(), "mcp.log")

	s.app.configLoad = func() (*config.Config, error) {
		return &config.Config{
			LogLevel:  "info",
			LogFormat: "text",
		}, nil
	}

	memoryOptReceived := false
	s.app.newMCPServer = func(channelID, apiURL, authorID string, httpClient mcpserver.HTTPClient, logger *slog.Logger, opts ...mcpserver.MemoryOption) *mcpserver.Server {
		if len(opts) > 0 {
			memoryOptReceived = true
		}
		return mcpserver.New(channelID, apiURL, authorID, httpClient, logger)
	}

	s.app.ensureChannelFn = func(_, _, _ string) (string, error) {
		return "ch1", nil
	}

	_ = s.app.runMCP("", "http://localhost:8222", "/path", logPath, "", "local", "", false)
	require.False(s.T(), memoryOptReceived)
}

func (s *MainSuite) TestRunMCPWithMemoryFlag() {
	// When --memory flag is true, memory tools should be enabled regardless of config.
	logPath := filepath.Join(s.T().TempDir(), "mcp.log")

	s.app.configLoad = func() (*config.Config, error) {
		return &config.Config{
			LogLevel:  "info",
			LogFormat: "text",
		}, nil
	}

	memoryOptReceived := false
	s.app.newMCPServer = func(channelID, apiURL, authorID string, httpClient mcpserver.HTTPClient, logger *slog.Logger, opts ...mcpserver.MemoryOption) *mcpserver.Server {
		if len(opts) > 0 {
			memoryOptReceived = true
		}
		return mcpserver.New(channelID, apiURL, authorID, httpClient, logger, opts...)
	}

	_ = s.app.runMCP("ch1", "http://localhost:8222", "", logPath, "", "local", "", true)
	require.True(s.T(), memoryOptReceived)
}

// --- serve() error cases ---

func (s *MainSuite) TestServeEarlyErrors() {
	tests := []struct {
		name    string
		setup   func(store *testutil.MockStore)
		wantErr string
	}{
		{
			name: "config load error",
			setup: func(_ *testutil.MockStore) {
				s.app.configLoad = func() (*config.Config, error) {
					return nil, errors.New("config error")
				}
			},
			wantErr: "config error",
		},
		{
			name: "sqlite store error",
			setup: func(_ *testutil.MockStore) {
				s.app.configLoad = func() (*config.Config, error) { return testConfig(), nil }
				s.app.newSQLiteStore = func(_ string) (db.Store, error) {
					return nil, errors.New("db error")
				}
			},
			wantErr: "opening database",
		},
		{
			name: "discord bot error",
			setup: func(store *testutil.MockStore) {
				store.On("Close").Return(nil)
				s.app.configLoad = func() (*config.Config, error) { return testConfig(), nil }
				s.app.newSQLiteStore = func(_ string) (db.Store, error) { return store, nil }
				s.app.newDiscordBot = func(_, _, _ string, _ *slog.Logger) (orchestrator.Bot, error) {
					return nil, errors.New("discord error")
				}
			},
			wantErr: "creating discord bot",
		},
		{
			name: "slack bot error",
			setup: func(store *testutil.MockStore) {
				store.On("Close").Return(nil)
				s.app.configLoad = func() (*config.Config, error) { return testSlackConfig(), nil }
				s.app.newSQLiteStore = func(_ string) (db.Store, error) { return store, nil }
				s.app.newSlackBot = func(_, _ string, _ *slog.Logger) (orchestrator.Bot, error) {
					return nil, errors.New("slack error")
				}
			},
			wantErr: "creating slack bot",
		},
		{
			name: "docker client error",
			setup: func(store *testutil.MockStore) {
				store.On("Close").Return(nil)
				s.app.configLoad = func() (*config.Config, error) { return testConfig(), nil }
				s.app.newSQLiteStore = func(_ string) (db.Store, error) { return store, nil }
				s.app.newDiscordBot = func(_, _, _ string, _ *slog.Logger) (orchestrator.Bot, error) {
					return new(mockBot), nil
				}
				s.app.newDockerClient = func() (container.DockerClient, error) {
					return nil, errors.New("docker error")
				}
			},
			wantErr: "creating docker client",
		},
		{
			name: "ensure image error",
			setup: func(store *testutil.MockStore) {
				store.On("Close").Return(nil)
				s.app.configLoad = func() (*config.Config, error) { return testConfig(), nil }
				s.app.newSQLiteStore = func(_ string) (db.Store, error) { return store, nil }
				s.app.newDiscordBot = func(_, _, _ string, _ *slog.Logger) (orchestrator.Bot, error) {
					return new(mockBot), nil
				}
				s.app.newDockerClient = func() (container.DockerClient, error) {
					return new(mockDockerClient), nil
				}
				s.app.ensureImage = func(_ context.Context, _ container.DockerClient, _ *config.Config) error {
					return errors.New("image build failed")
				}
			},
			wantErr: "ensuring agent image",
		},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			store := new(testutil.MockStore)
			tt.setup(store)
			err := s.app.serve()
			require.Error(s.T(), err)
			require.Contains(s.T(), err.Error(), tt.wantErr)
			store.AssertExpectations(s.T())
		})
	}
}

func (s *MainSuite) TestServeSlackHappyPathShutdown() {
	m := s.setupServeMocks()
	m.cfg = testSlackConfig()
	s.app.configLoad = func() (*config.Config, error) { return m.cfg, nil }
	s.app.newSlackBot = func(_, _ string, _ *slog.Logger) (orchestrator.Bot, error) { return m.bot, nil }
	m.setupHappyBot()

	channelsCh := make(chan api.ChannelEnsurer, 1)
	threadsCh := make(chan api.ThreadEnsurer, 1)
	s.app.newAPIServer = func(sched scheduler.Scheduler, channels api.ChannelEnsurer, threads api.ThreadEnsurer, store api.ChannelLister, messages api.MessageSender, logger *slog.Logger) *api.Server {
		channelsCh <- channels
		threadsCh <- threads
		return api.NewServer(sched, channels, threads, store, messages, logger)
	}

	errCh := make(chan error, 1)
	go func() { errCh <- s.app.serve() }()

	gotChannels := <-channelsCh
	gotThreads := <-threadsCh
	require.NotNil(s.T(), gotChannels, "Slack should always create channel service")
	require.NotNil(s.T(), gotThreads, "Slack should always create thread service")

	time.Sleep(100 * time.Millisecond)
	p, err := os.FindProcess(os.Getpid())
	require.NoError(s.T(), err)
	require.NoError(s.T(), p.Signal(syscall.SIGINT))

	select {
	case err := <-errCh:
		require.NoError(s.T(), err)
	case <-time.After(5 * time.Second):
		s.T().Fatal("serve() did not return in time")
	}

	m.store.AssertExpectations(s.T())
	m.bot.AssertExpectations(s.T())
}

func (s *MainSuite) TestServeAPIServerStartError() {
	m := s.setupServeMocks()
	m.cfg.APIAddr = "invalid-addr-no-port"

	err := s.app.serve()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "starting api server")
	m.store.AssertExpectations(s.T())
}

func (s *MainSuite) TestServeOrchestratorStartError() {
	m := s.setupServeMocks()
	m.bot.On("OnMessage", mock.Anything).Return()
	m.bot.On("OnInteraction", mock.Anything).Return()
	m.bot.On("OnChannelDelete", mock.Anything).Return()
	m.bot.On("OnChannelJoin", mock.Anything).Return()
	m.bot.On("RegisterCommands", mock.Anything).Return(errors.New("register failed"))

	err := s.app.serve()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "starting orchestrator")
	m.store.AssertExpectations(s.T())
	m.bot.AssertExpectations(s.T())
}

func (s *MainSuite) TestServeHappyPathShutdown() {
	m := s.setupServeMocks()
	m.setupHappyBot()

	errCh := make(chan error, 1)
	go func() { errCh <- s.app.serve() }()

	time.Sleep(100 * time.Millisecond)
	p, err := os.FindProcess(os.Getpid())
	require.NoError(s.T(), err)
	require.NoError(s.T(), p.Signal(syscall.SIGINT))

	select {
	case err := <-errCh:
		require.NoError(s.T(), err)
	case <-time.After(5 * time.Second):
		s.T().Fatal("serve() did not return in time")
	}

	m.store.AssertExpectations(s.T())
	m.bot.AssertExpectations(s.T())
}

func (s *MainSuite) TestServeHappyPathWithChannelService() {
	m := s.setupServeMocks()
	m.setupHappyBot()

	channelsCh := make(chan api.ChannelEnsurer, 1)
	s.app.newAPIServer = func(sched scheduler.Scheduler, channels api.ChannelEnsurer, threads api.ThreadEnsurer, store api.ChannelLister, messages api.MessageSender, logger *slog.Logger) *api.Server {
		channelsCh <- channels
		return api.NewServer(sched, channels, threads, store, messages, logger)
	}

	errCh := make(chan error, 1)
	go func() { errCh <- s.app.serve() }()

	gotChannels := <-channelsCh
	require.NotNil(s.T(), gotChannels)

	time.Sleep(100 * time.Millisecond)
	p, err := os.FindProcess(os.Getpid())
	require.NoError(s.T(), err)
	require.NoError(s.T(), p.Signal(syscall.SIGINT))

	select {
	case err := <-errCh:
		require.NoError(s.T(), err)
	case <-time.After(5 * time.Second):
		s.T().Fatal("serve() did not return in time")
	}

	m.store.AssertExpectations(s.T())
	m.bot.AssertExpectations(s.T())
}

func (s *MainSuite) TestServeHappyPathShutdownWithStopError() {
	m := s.setupServeMocks()
	m.setupHappyBot()
	// Override Stop to return an error
	m.bot.ExpectedCalls = filterExpected(m.bot.ExpectedCalls, "Stop")
	m.bot.On("Stop").Return(errors.New("bot stop error"))

	errCh := make(chan error, 1)
	go func() { errCh <- s.app.serve() }()

	time.Sleep(100 * time.Millisecond)
	p, err := os.FindProcess(os.Getpid())
	require.NoError(s.T(), err)
	require.NoError(s.T(), p.Signal(syscall.SIGINT))

	select {
	case err := <-errCh:
		// serve() returns nil even when Stop() fails — it logs the error.
		require.NoError(s.T(), err)
	case <-time.After(5 * time.Second):
		s.T().Fatal("serve() did not return in time")
	}

	m.bot.AssertExpectations(s.T())
}

func (s *MainSuite) TestServeHappyPathShutdownWithAPIStopError() {
	// Verify serve() returns nil even when the API server's Stop() returns an
	// error. We inject a stop error via SetStopError.
	m := s.setupServeMocks()
	m.setupHappyBot()

	s.app.newAPIServer = func(sched scheduler.Scheduler, channels api.ChannelEnsurer, threads api.ThreadEnsurer, store api.ChannelLister, messages api.MessageSender, logger *slog.Logger) *api.Server {
		srv := api.NewServer(sched, channels, threads, store, messages, logger)
		srv.SetStopError(errors.New("injected stop error"))
		return srv
	}

	errCh := make(chan error, 1)
	go func() { errCh <- s.app.serve() }()

	time.Sleep(100 * time.Millisecond)
	p, err := os.FindProcess(os.Getpid())
	require.NoError(s.T(), err)
	require.NoError(s.T(), p.Signal(syscall.SIGINT))

	select {
	case err := <-errCh:
		// serve() returns nil — it logs the Stop() error but doesn't propagate it.
		require.NoError(s.T(), err)
	case <-time.After(5 * time.Second):
		s.T().Fatal("serve() did not return in time")
	}

	m.bot.AssertExpectations(s.T())
}

func (s *MainSuite) TestServeWithMemoryEnabled() {
	m := s.setupServeMocks()
	m.store.On("ListChannels", mock.Anything).Maybe().Return(nil, nil)
	m.bot.On("OnMessage", mock.Anything).Return()
	m.bot.On("OnInteraction", mock.Anything).Return()
	m.bot.On("OnChannelDelete", mock.Anything).Return()
	m.bot.On("OnChannelJoin", mock.Anything).Return()
	m.bot.On("RegisterCommands", mock.Anything).Return(errors.New("fail early"))

	m.cfg.Memory = config.MemoryConfig{
		Enabled: true,
		Embeddings: config.EmbeddingsConfig{
			Provider:  "ollama",
			OllamaURL: "http://localhost:11434",
		},
		Paths: []string{"./memory"},
	}
	m.cfg.LoopDir = s.T().TempDir()

	memoryIndexerSet := false
	defaultNewEmbedder := s.app.newEmbedder
	s.app.newEmbedder = func(cfg *config.Config) (embeddings.Embedder, error) {
		memoryIndexerSet = true
		return defaultNewEmbedder(cfg)
	}

	err := s.app.serve()
	require.Error(s.T(), err)
	require.True(s.T(), memoryIndexerSet, "embedder should be created when memory is enabled")
}

func (s *MainSuite) TestServeWithMemoryEmbedderError() {
	m := s.setupServeMocks()
	m.bot.On("OnMessage", mock.Anything).Return()
	m.bot.On("OnInteraction", mock.Anything).Return()
	m.bot.On("OnChannelDelete", mock.Anything).Return()
	m.bot.On("OnChannelJoin", mock.Anything).Return()
	m.bot.On("RegisterCommands", mock.Anything).Return(errors.New("fail early"))

	m.cfg.Memory = config.MemoryConfig{
		Enabled: true,
		Embeddings: config.EmbeddingsConfig{
			Provider: "unsupported-provider",
		},
	}

	// serve() continues even when embeddings fail (logs a warning)
	err := s.app.serve()
	require.Error(s.T(), err) // Fails at orchestrator, not at embeddings
	require.Contains(s.T(), err.Error(), "starting orchestrator")
}

func (s *MainSuite) TestServeDockerClientCloserCalled() {
	m := s.setupServeMocks()
	m.bot.On("OnMessage", mock.Anything).Return()
	m.bot.On("OnInteraction", mock.Anything).Return()
	m.bot.On("OnChannelDelete", mock.Anything).Return()
	m.bot.On("OnChannelJoin", mock.Anything).Return()
	m.bot.On("RegisterCommands", mock.Anything).Return(errors.New("fail"))

	closeCalled := false
	s.app.newDockerClient = func() (container.DockerClient, error) {
		return &closableDockerClient{
			mockDockerClient: new(mockDockerClient),
			closeFn:          func() error { closeCalled = true; return nil },
		}, nil
	}

	err := s.app.serve()
	require.Error(s.T(), err)
	require.True(s.T(), closeCalled, "docker client Close() should be called via io.Closer")
}

// --- main() ---

func (s *MainSuite) TestRunSuccess() {
	oldArgs := os.Args
	os.Args = []string{"loop", "version"}
	defer func() { os.Args = oldArgs }()

	code := s.app.run()
	require.Equal(s.T(), 0, code)
}

func (s *MainSuite) TestRunError() {
	s.app.configLoad = func() (*config.Config, error) {
		return nil, errors.New("fail")
	}

	// run() creates its own root cmd, so set os.Args to trigger the error path.
	oldArgs := os.Args
	os.Args = []string{"loop", "serve"}
	defer func() { os.Args = oldArgs }()

	code := s.app.run()
	require.Equal(s.T(), 1, code)
}

// --- Verify the default var functions have correct signatures ---

func (s *MainSuite) TestDefaultVarSignatures() {
	a := newApp()
	require.NotNil(s.T(), a.configLoad)
	require.NotNil(s.T(), a.newDiscordBot)
	require.NotNil(s.T(), a.newSlackBot)
	require.NotNil(s.T(), a.newDockerClient)
	require.NotNil(s.T(), a.newSQLiteStore)
	require.NotNil(s.T(), a.newAPIServer)
	require.NotNil(s.T(), a.newMCPServer)

	// Verify newAPIServer produces a non-nil *api.Server
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	apiSrv := a.newAPIServer(nil, nil, nil, nil, nil, logger)
	require.NotNil(s.T(), apiSrv)

	// Verify newMCPServer produces a non-nil server
	mcpSrv := a.newMCPServer("ch1", "http://localhost:8222", "", http.DefaultClient, nil)
	require.NotNil(s.T(), mcpSrv)
}

func (s *MainSuite) TestDefaultNewSQLiteStore() {
	// Exercise the default newSQLiteStore with a temp file.
	tmpDir := s.T().TempDir()
	store, err := newApp().newSQLiteStore(tmpDir + "/test.db")
	require.NoError(s.T(), err)
	require.NotNil(s.T(), store)
	require.NoError(s.T(), store.Close())
}

func (s *MainSuite) TestDefaultNewDiscordBot() {
	// Exercise the default newDiscordBot — discordgo.New succeeds without a server.
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bot, err := newApp().newDiscordBot("fake-token", "fake-app-id", "fake-guild-id", logger)
	require.NoError(s.T(), err)
	require.NotNil(s.T(), bot)
}

func (s *MainSuite) TestDefaultNewDiscordBotSessionError() {
	s.app.discordgoNew = func(string) (*discordgo.Session, error) {
		return nil, errors.New("session error")
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	_, err := s.app.newDiscordBot("fake-token", "fake-app-id", "fake-guild-id", logger)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "session error")
}

func (s *MainSuite) TestDefaultNewSlackBot() {
	// Exercise the default newSlackBot — creates a bot without needing a server.
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bot, err := newApp().newSlackBot("xoxb-fake", "xapp-fake", logger)
	require.NoError(s.T(), err)
	require.NotNil(s.T(), bot)
}

func (s *MainSuite) TestDefaultNewDockerClient() {
	// Exercise the default newDockerClient — Docker client creation succeeds without a running daemon.
	dc, err := newApp().newDockerClient()
	require.NoError(s.T(), err)
	require.NotNil(s.T(), dc)
	if closer, ok := dc.(io.Closer); ok {
		_ = closer.Close()
	}
}

func (s *MainSuite) TestDefaultNewDockerExecClient() {
	// Exercise the default newDockerExecClient to cover serve.go var body.
	_, _ = newApp().newDockerExecClient()
}

func (s *MainSuite) TestDefaultNewHostExecClient() {
	// Exercise the default newHostExecClient to cover serve.go var body.
	c := newApp().newHostExecClient()
	require.NotNil(s.T(), c)
}

func (s *MainSuite) TestDefaultNewBrowserProvider() {
	// Exercise the default newBrowserProvider to cover main.go factory body.
	_, _ = newApp().newBrowserProvider("loop-chrome:latest", slog.Default())
}

func (s *MainSuite) TestDefaultNewBrowserProviderDockerError() {
	// Force browser.NewDockerExecAPI() to fail by requesting TLS
	// verification with a non-existent cert path.
	s.T().Setenv("DOCKER_TLS_VERIFY", "1")
	s.T().Setenv("DOCKER_CERT_PATH", "/nonexistent/certs")
	_, err := newApp().newBrowserProvider("loop-chrome:latest", slog.Default())
	require.Error(s.T(), err)
}

func (s *MainSuite) TestDefaultNewLocalBot() {
	store := &testutil.MockStore{}
	b := newApp().newLocalBot(store, slog.Default())
	require.NotNil(s.T(), b)
}

func (s *MainSuite) TestDefaultGetLatestVersionFn() {
	// Exercise the default getLatestVersionFn to cover main.go factory body.
	// It will fail (no network) but that's fine — we just cover the code path.
	_, _ = newApp().getLatestVersionFn()
}

// --- daemon commands ---

func (s *MainSuite) TestNewDaemonStartCmd() {
	cmd := s.app.newDaemonStartCmd()
	require.Equal(s.T(), "daemon:start", cmd.Use)
	require.Equal(s.T(), []string{"d:start", "up"}, cmd.Aliases)
	require.NotNil(s.T(), cmd.RunE)
}

func (s *MainSuite) TestNewDaemonStopCmd() {
	cmd := s.app.newDaemonStopCmd()
	require.Equal(s.T(), "daemon:stop", cmd.Use)
	require.Equal(s.T(), []string{"d:stop", "down"}, cmd.Aliases)
	require.NotNil(s.T(), cmd.RunE)
}

func (s *MainSuite) TestNewDaemonStatusCmd() {
	cmd := s.app.newDaemonStatusCmd()
	require.Equal(s.T(), "daemon:status", cmd.Use)
	require.Equal(s.T(), []string{"d:status"}, cmd.Aliases)
	require.NotNil(s.T(), cmd.RunE)
}

func (s *MainSuite) TestDaemonStartSuccess() {
	s.app.configLoad = func() (*config.Config, error) { return testConfig(), nil }
	s.app.daemonStart = func(_ daemon.System, _ string) error { return nil }
	s.app.newSystem = func() daemon.System { return daemon.RealSystem{} }

	cmd := s.app.newDaemonStartCmd()
	err := cmd.Execute()
	require.NoError(s.T(), err)
}

func (s *MainSuite) TestDaemonStartError() {
	s.app.configLoad = func() (*config.Config, error) { return testConfig(), nil }
	s.app.daemonStart = func(_ daemon.System, _ string) error { return errors.New("start fail") }
	s.app.newSystem = func() daemon.System { return daemon.RealSystem{} }

	cmd := s.app.newDaemonStartCmd()
	err := cmd.Execute()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "start fail")
}

func (s *MainSuite) TestDaemonStartConfigError() {
	s.app.configLoad = func() (*config.Config, error) { return nil, errors.New("config fail") }

	cmd := s.app.newDaemonStartCmd()
	err := cmd.Execute()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "config fail")
}

func (s *MainSuite) TestDaemonStopSuccess() {
	s.app.daemonStop = func(_ daemon.System) error { return nil }
	s.app.newSystem = func() daemon.System { return daemon.RealSystem{} }

	cmd := s.app.newDaemonStopCmd()
	err := cmd.Execute()
	require.NoError(s.T(), err)
}

func (s *MainSuite) TestDaemonStopError() {
	s.app.daemonStop = func(_ daemon.System) error { return errors.New("stop fail") }
	s.app.newSystem = func() daemon.System { return daemon.RealSystem{} }

	cmd := s.app.newDaemonStopCmd()
	err := cmd.Execute()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "stop fail")
}

func (s *MainSuite) TestNewDaemonRestartCmd() {
	cmd := s.app.newDaemonRestartCmd()
	require.Equal(s.T(), "daemon:restart", cmd.Use)
	require.Equal(s.T(), []string{"d:restart", "restart"}, cmd.Aliases)
	require.NotNil(s.T(), cmd.RunE)
}

func (s *MainSuite) TestDaemonRestartSuccess() {
	s.app.configLoad = func() (*config.Config, error) { return testConfig(), nil }
	s.app.daemonStop = func(_ daemon.System) error { return nil }
	s.app.daemonStart = func(_ daemon.System, _ string) error { return nil }
	s.app.newSystem = func() daemon.System { return daemon.RealSystem{} }

	cmd := s.app.newDaemonRestartCmd()
	err := cmd.Execute()
	require.NoError(s.T(), err)
}

func (s *MainSuite) TestDaemonRestartSuccessWhenNotRunning() {
	s.app.configLoad = func() (*config.Config, error) { return testConfig(), nil }
	s.app.daemonStop = func(_ daemon.System) error { return errors.New("not running") }
	s.app.daemonStart = func(_ daemon.System, _ string) error { return nil }
	s.app.newSystem = func() daemon.System { return daemon.RealSystem{} }

	cmd := s.app.newDaemonRestartCmd()
	err := cmd.Execute()
	require.NoError(s.T(), err)
}

func (s *MainSuite) TestDaemonRestartStartError() {
	s.app.configLoad = func() (*config.Config, error) { return testConfig(), nil }
	s.app.daemonStop = func(_ daemon.System) error { return nil }
	s.app.daemonStart = func(_ daemon.System, _ string) error { return errors.New("start fail") }
	s.app.newSystem = func() daemon.System { return daemon.RealSystem{} }

	cmd := s.app.newDaemonRestartCmd()
	err := cmd.Execute()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "start fail")
}

func (s *MainSuite) TestDaemonRestartConfigError() {
	s.app.configLoad = func() (*config.Config, error) { return nil, errors.New("config fail") }

	cmd := s.app.newDaemonRestartCmd()
	err := cmd.Execute()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "config fail")
}

func (s *MainSuite) TestDaemonStatusSuccess() {
	s.app.daemonStatus = func(_ daemon.System) (string, error) { return "running", nil }
	s.app.newSystem = func() daemon.System { return daemon.RealSystem{} }

	cmd := s.app.newDaemonStatusCmd()
	err := cmd.Execute()
	require.NoError(s.T(), err)
}

func (s *MainSuite) TestDaemonStatusError() {
	s.app.daemonStatus = func(_ daemon.System) (string, error) { return "", errors.New("status fail") }
	s.app.newSystem = func() daemon.System { return daemon.RealSystem{} }

	cmd := s.app.newDaemonStatusCmd()
	err := cmd.Execute()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "status fail")
}

func (s *MainSuite) TestDefaultDaemonVars() {
	a := newApp()
	require.NotNil(s.T(), a.daemonStart)
	require.NotNil(s.T(), a.daemonStop)
	require.NotNil(s.T(), a.daemonStatus)
	require.NotNil(s.T(), a.newSystem)

	sys := a.newSystem()
	require.IsType(s.T(), daemon.RealSystem{}, sys)
}

// --- onboard:global ---

func (s *MainSuite) TestNewOnboardGlobalCmd() {
	cmd := s.app.newOnboardGlobalCmd()
	require.Equal(s.T(), "onboard:global", cmd.Use)
	require.Equal(s.T(), []string{"o:global", "setup"}, cmd.Aliases)
	require.NotNil(s.T(), cmd.RunE)
	require.NotNil(s.T(), cmd.Flags().Lookup("force"))
	f := cmd.Flags().Lookup("owner-id")
	require.NotNil(s.T(), f)
	require.Equal(s.T(), "", f.DefValue)
}

func (s *MainSuite) TestNewOnboardLocalCmd() {
	cmd := s.app.newOnboardLocalCmd()
	require.Equal(s.T(), "onboard:local", cmd.Use)
	require.Equal(s.T(), []string{"o:local", "init"}, cmd.Aliases)
	require.NotNil(s.T(), cmd.RunE)
	f := cmd.Flags().Lookup("api-url")
	require.NotNil(s.T(), f)
	require.Equal(s.T(), "http://localhost:8222", f.DefValue)
	ownerF := cmd.Flags().Lookup("owner-id")
	require.NotNil(s.T(), ownerF)
	require.Equal(s.T(), "", ownerF.DefValue)
}

func (s *MainSuite) TestOnboardGlobalSuccess() {
	tmpDir := s.T().TempDir()
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return(tmpDir, nil)

	err := s.app.onboardGlobal(false, "")
	require.NoError(s.T(), err)

	configPath := filepath.Join(tmpDir, ".loop", "config.json")
	data, err := os.ReadFile(configPath)
	require.NoError(s.T(), err)
	require.Contains(s.T(), string(data), "discord_token")
	require.Contains(s.T(), string(data), "task_templates")

	// Verify container files were written
	dockerfilePath := filepath.Join(tmpDir, ".loop", "container", "Dockerfile")
	dockerfileData, err := os.ReadFile(dockerfilePath)
	require.NoError(s.T(), err)
	require.Contains(s.T(), string(dockerfileData), "FROM golang:")

	entrypointPath := filepath.Join(tmpDir, ".loop", "container", "entrypoint.sh")
	entrypointData, err := os.ReadFile(entrypointPath)
	require.NoError(s.T(), err)
	require.Contains(s.T(), string(entrypointData), `su-exec "$AGENT_USER" "$@"`)

	setupPath := filepath.Join(tmpDir, ".loop", "container", "setup.sh")
	setupData, err := os.ReadFile(setupPath)
	require.NoError(s.T(), err)
	require.Contains(s.T(), string(setupData), "#!/bin/sh")

	bashrcPath := filepath.Join(tmpDir, ".loop", ".bashrc")
	bashrcData, err := os.ReadFile(bashrcPath)
	require.NoError(s.T(), err)
	require.Contains(s.T(), string(bashrcData), "Shell aliases")

	// Verify Slack manifest was written
	manifestPath := filepath.Join(tmpDir, ".loop", "slack-manifest.json")
	manifestData, err := os.ReadFile(manifestPath)
	require.NoError(s.T(), err)
	require.Contains(s.T(), string(manifestData), "LoopBot")
	require.Contains(s.T(), string(manifestData), "socket_mode_enabled")

	// Verify templates directory was created
	templatesDir := filepath.Join(tmpDir, ".loop", "templates")
	info, err := os.Stat(templatesDir)
	require.NoError(s.T(), err)
	require.True(s.T(), info.IsDir())

	// Verify embedded templates were written
	heartbeatPath := filepath.Join(templatesDir, "heartbeat.md")
	heartbeatData, err := os.ReadFile(heartbeatPath)
	require.NoError(s.T(), err)
	require.Contains(s.T(), string(heartbeatData), "heartbeat check")

	tkAutoWorkerPath := filepath.Join(templatesDir, "tk-auto-worker.md")
	tkAutoWorkerData, err := os.ReadFile(tkAutoWorkerPath)
	require.NoError(s.T(), err)
	require.Contains(s.T(), string(tkAutoWorkerData), "ticket dispatcher")
}

func (s *MainSuite) TestOnboardGlobalConfigAlreadyExists() {
	tmpDir := s.T().TempDir()
	loopDir := filepath.Join(tmpDir, ".loop")
	configPath := filepath.Join(loopDir, "config.json")

	require.NoError(s.T(), os.MkdirAll(loopDir, 0755))
	require.NoError(s.T(), os.WriteFile(configPath, []byte("existing"), 0600))

	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return(tmpDir, nil)

	err := s.app.onboardGlobal(false, "")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "config already exists")
	require.Contains(s.T(), err.Error(), "--force")

	// Verify original content is unchanged
	data, err := os.ReadFile(configPath)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "existing", string(data))
}

func (s *MainSuite) TestOnboardGlobalForceOverwrite() {
	tmpDir := s.T().TempDir()
	loopDir := filepath.Join(tmpDir, ".loop")
	configPath := filepath.Join(loopDir, "config.json")

	require.NoError(s.T(), os.MkdirAll(loopDir, 0755))
	require.NoError(s.T(), os.WriteFile(configPath, []byte("old config"), 0600))

	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return(tmpDir, nil)

	err := s.app.onboardGlobal(true, "")
	require.NoError(s.T(), err)

	// Verify config was overwritten
	data, err := os.ReadFile(configPath)
	require.NoError(s.T(), err)
	require.Contains(s.T(), string(data), "discord_token")
	require.Contains(s.T(), string(data), "task_templates")
	require.NotContains(s.T(), string(data), "old config")
}

func (s *MainSuite) TestOnboardGlobalHomeDirError() {
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return("", errors.New("home dir error"))

	err := s.app.onboardGlobal(false, "")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "getting home directory")
}

func (s *MainSuite) TestOnboardGlobalMkdirErrors() {
	tests := []struct {
		name      string
		failCallN int
		wantErr   string
	}{
		{"loop directory", 1, "creating loop directory"},
		{"container directory", 2, "creating container directory"},
		{"templates directory", 3, "creating templates directory"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			tmpDir := s.T().TempDir()
			sys := newPassthroughMock()
			s.app.sys = sys
			sys.Override("UserHomeDir").Return(tmpDir, nil)
			mkdirCall := sys.Override("MkdirAll", mock.Anything, mock.Anything).Maybe().Return(nil)
			calls := 0
			mkdirCall.RunFn = func(args mock.Arguments) {
				calls++
				if calls == tt.failCallN {
					mkdirCall.ReturnArguments = mock.Arguments{errors.New("mkdir error")}
					return
				}
				mkdirCall.ReturnArguments = mock.Arguments{os.MkdirAll(args.String(0), args.Get(1).(os.FileMode))}
			}

			err := s.app.onboardGlobal(false, "")
			require.Error(s.T(), err)
			require.Contains(s.T(), err.Error(), tt.wantErr)
		})
	}
}

func (s *MainSuite) TestOnboardGlobalCmdWithForceFlag() {
	tmpDir := s.T().TempDir()
	loopDir := filepath.Join(tmpDir, ".loop")
	configPath := filepath.Join(loopDir, "config.json")

	require.NoError(s.T(), os.MkdirAll(loopDir, 0755))
	require.NoError(s.T(), os.WriteFile(configPath, []byte("old"), 0600))

	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return(tmpDir, nil)

	cmd := s.app.newOnboardGlobalCmd()
	cmd.SetArgs([]string{"--force"})
	err := cmd.Execute()
	require.NoError(s.T(), err)

	data, err := os.ReadFile(configPath)
	require.NoError(s.T(), err)
	require.Contains(s.T(), string(data), "discord_token")
}

func (s *MainSuite) TestOnboardGlobalBashrcSkipsIfExists() {
	tmpDir := s.T().TempDir()
	loopDir := filepath.Join(tmpDir, ".loop")
	require.NoError(s.T(), os.MkdirAll(loopDir, 0755))
	bashrcPath := filepath.Join(loopDir, ".bashrc")
	require.NoError(s.T(), os.WriteFile(bashrcPath, []byte("existing aliases"), 0644))

	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return(tmpDir, nil)

	err := s.app.onboardGlobal(true, "") // force overwrites config but not .bashrc
	require.NoError(s.T(), err)

	data, err := os.ReadFile(bashrcPath)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "existing aliases", string(data))
}

func (s *MainSuite) TestOnboardGlobalSetupSkipsIfExists() {
	tmpDir := s.T().TempDir()
	containerDir := filepath.Join(tmpDir, ".loop", "container")
	require.NoError(s.T(), os.MkdirAll(containerDir, 0755))
	setupPath := filepath.Join(containerDir, "setup.sh")
	require.NoError(s.T(), os.WriteFile(setupPath, []byte("existing setup"), 0644))

	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return(tmpDir, nil)

	err := s.app.onboardGlobal(true, "") // force overwrites config but not setup.sh
	require.NoError(s.T(), err)

	data, err := os.ReadFile(setupPath)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "existing setup", string(data))
}

func (s *MainSuite) TestOnboardGlobalWriteErrors() {
	tests := []struct {
		name      string
		failCallN int
		wantErr   string
	}{
		{"config file", 1, "writing config file"},
		{".bashrc", 2, "writing .bashrc"},
		{"Dockerfile", 3, "writing container Dockerfile"},
		{"chrome Dockerfile", 4, "writing chrome Dockerfile"},
		{"chrome entrypoint", 5, "writing chrome entrypoint"},
		{"entrypoint", 6, "writing container entrypoint"},
		{"setup script", 7, "writing container setup script"},
		{"Slack manifest", 8, "writing Slack manifest"},
		{"template", 9, "writing template"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			tmpDir := s.T().TempDir()
			sys := newPassthroughMock()
			s.app.sys = sys
			sys.Override("UserHomeDir").Return(tmpDir, nil)
			writeCall := sys.Override("WriteFile", mock.Anything, mock.Anything, mock.Anything).Maybe().Return(nil)
			calls := 0
			writeCall.RunFn = func(args mock.Arguments) {
				calls++
				if calls == tt.failCallN {
					writeCall.ReturnArguments = mock.Arguments{errors.New("write error")}
					return
				}
				writeCall.ReturnArguments = mock.Arguments{os.WriteFile(args.String(0), args.Get(1).([]byte), args.Get(2).(os.FileMode))}
			}

			err := s.app.onboardGlobal(false, "")
			require.Error(s.T(), err)
			require.Contains(s.T(), err.Error(), tt.wantErr)
		})
	}
}

func (s *MainSuite) TestOnboardGlobalTemplatesSkipIfExist() {
	tmpDir := s.T().TempDir()
	loopDir := filepath.Join(tmpDir, ".loop")
	templatesDir := filepath.Join(loopDir, "templates")

	require.NoError(s.T(), os.MkdirAll(templatesDir, 0755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(templatesDir, "heartbeat.md"), []byte("custom heartbeat"), 0644))
	require.NoError(s.T(), os.WriteFile(filepath.Join(templatesDir, "tk-auto-worker.md"), []byte("custom worker"), 0644))

	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return(tmpDir, nil)

	err := s.app.onboardGlobal(true, "") // force overwrites config but not templates
	require.NoError(s.T(), err)

	data, err := os.ReadFile(filepath.Join(templatesDir, "heartbeat.md"))
	require.NoError(s.T(), err)
	require.Equal(s.T(), "custom heartbeat", string(data))

	data, err = os.ReadFile(filepath.Join(templatesDir, "tk-auto-worker.md"))
	require.NoError(s.T(), err)
	require.Equal(s.T(), "custom worker", string(data))
}

// brokenReadDirFS implements fs.ReadFileFS but fails on ReadDir.
type brokenReadDirFS struct{}

func (brokenReadDirFS) Open(string) (fs.File, error)    { return nil, errors.New("broken") }
func (brokenReadDirFS) ReadFile(string) ([]byte, error) { return nil, errors.New("broken") }

// brokenReadFileFS succeeds on ReadDir (returns one fake entry) but fails on ReadFile.
type brokenReadFileFS struct{ brokenReadDirFS }

func (brokenReadFileFS) Open(name string) (fs.File, error) {
	// fs.ReadDir calls Open; return a dir with one fake file entry.
	if name == "templates" {
		return &fakeDirFile{entries: []fs.DirEntry{&fakeEntry{name: "test.md"}}}, nil
	}
	return nil, errors.New("broken")
}

type fakeDirFile struct {
	entries []fs.DirEntry
	read    bool
}

func (f *fakeDirFile) Stat() (fs.FileInfo, error) { return nil, nil }
func (f *fakeDirFile) Read([]byte) (int, error)   { return 0, io.EOF }
func (f *fakeDirFile) Close() error               { return nil }
func (f *fakeDirFile) ReadDir(int) ([]fs.DirEntry, error) {
	if f.read {
		return nil, io.EOF
	}
	f.read = true
	return f.entries, nil
}

type fakeEntry struct{ name string }

func (e *fakeEntry) Name() string               { return e.name }
func (e *fakeEntry) IsDir() bool                { return false }
func (e *fakeEntry) Type() fs.FileMode          { return 0 }
func (e *fakeEntry) Info() (fs.FileInfo, error) { return nil, nil }

func (s *MainSuite) TestDumpTemplatesReadDirError() {
	s.app.templatesFS = brokenReadDirFS{}

	err := s.app.dumpTemplates(s.T().TempDir())
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "reading embedded templates")
}

func (s *MainSuite) TestDumpTemplatesReadFileError() {
	s.app.templatesFS = brokenReadFileFS{}

	err := s.app.dumpTemplates(s.T().TempDir())
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "reading embedded template test.md")
}

func (s *MainSuite) TestOnboardGlobalWithOwnerID() {
	tmpDir := s.T().TempDir()
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return(tmpDir, nil)

	err := s.app.onboardGlobal(false, "U99887766")
	require.NoError(s.T(), err)

	configPath := filepath.Join(tmpDir, ".loop", "config.json")
	data, err := os.ReadFile(configPath)
	require.NoError(s.T(), err)

	content := string(data)
	// Verify the permissions block is uncommented with the real owner ID
	require.Contains(s.T(), content, `"permissions": {`)
	require.Contains(s.T(), content, `"U99887766"`)
	require.NotContains(s.T(), content, `//  "owners"`)
	require.NotContains(s.T(), content, `U12345678`)
}

func (s *MainSuite) TestOnboardGlobalCmdWithOwnerIDFlag() {
	tmpDir := s.T().TempDir()
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("UserHomeDir").Return(tmpDir, nil)

	cmd := s.app.newOnboardGlobalCmd()
	cmd.SetArgs([]string{"--owner-id", "UTEST12345"})
	err := cmd.Execute()
	require.NoError(s.T(), err)

	configPath := filepath.Join(tmpDir, ".loop", "config.json")
	data, err := os.ReadFile(configPath)
	require.NoError(s.T(), err)

	content := string(data)
	require.Contains(s.T(), content, `"UTEST12345"`)
	require.Contains(s.T(), content, `"permissions": {`)
}

// --- onboard:local ---

func (s *MainSuite) TestOnboardLocalSuccess() {
	tmpDir := s.T().TempDir()
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("Getwd").Return(tmpDir, nil)
	s.app.ensureAllChannelsFn = func(_, _ string) ([]ensureResult, error) {
		return []ensureResult{{Platform: "local", ChannelID: "ch-test", Created: true}}, nil
	}

	err := s.app.onboardLocal("http://localhost:8222", "", "")
	require.NoError(s.T(), err)

	mcpPath := filepath.Join(tmpDir, ".mcp.json")
	data, err := os.ReadFile(mcpPath)
	require.NoError(s.T(), err)

	var result map[string]any
	require.NoError(s.T(), json.Unmarshal(data, &result))

	servers := result["mcpServers"].(map[string]any)
	loop := servers["loop"].(map[string]any)
	require.Equal(s.T(), "loop", loop["command"])

	args := loop["args"].([]any)
	require.Equal(s.T(), "mcp", args[0])
	require.Equal(s.T(), "--dir", args[1])
	require.Equal(s.T(), tmpDir, args[2])
	require.Equal(s.T(), "--api-url", args[3])
	require.Equal(s.T(), "http://localhost:8222", args[4])
	require.Equal(s.T(), "--platform", args[5])
	require.Equal(s.T(), "local", args[6])
	require.Equal(s.T(), "--log", args[7])
	require.Equal(s.T(), filepath.Join(tmpDir, ".loop", "mcp.log"), args[8])
}

func (s *MainSuite) TestOnboardLocalWithMemoryEnabled() {
	tmpDir := s.T().TempDir()
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("Getwd").Return(tmpDir, nil)
	s.app.ensureAllChannelsFn = func(_, _ string) ([]ensureResult, error) {
		return []ensureResult{{Platform: "local", ChannelID: "ch-test", Created: true}}, nil
	}
	s.app.configLoad = func() (*config.Config, error) {
		return &config.Config{Memory: config.MemoryConfig{Enabled: true}}, nil
	}

	err := s.app.onboardLocal("http://localhost:8222", "", "")
	require.NoError(s.T(), err)

	data, err := os.ReadFile(filepath.Join(tmpDir, ".mcp.json"))
	require.NoError(s.T(), err)

	var result map[string]any
	require.NoError(s.T(), json.Unmarshal(data, &result))

	servers := result["mcpServers"].(map[string]any)
	loop := servers["loop"].(map[string]any)
	args := loop["args"].([]any)
	require.Equal(s.T(), "--memory", args[len(args)-1])
}

func (s *MainSuite) TestOnboardLocalMergesExisting() {
	tmpDir := s.T().TempDir()
	existing := `{"mcpServers":{"other":{"command":"other-cmd"}}}`
	require.NoError(s.T(), os.WriteFile(filepath.Join(tmpDir, ".mcp.json"), []byte(existing), 0644))

	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("Getwd").Return(tmpDir, nil)
	s.app.ensureAllChannelsFn = func(_, _ string) ([]ensureResult, error) {
		return []ensureResult{{Platform: "local", ChannelID: "ch-test", Created: true}}, nil
	}

	err := s.app.onboardLocal("http://localhost:8222", "", "")
	require.NoError(s.T(), err)

	data, err := os.ReadFile(filepath.Join(tmpDir, ".mcp.json"))
	require.NoError(s.T(), err)

	var result map[string]any
	require.NoError(s.T(), json.Unmarshal(data, &result))

	servers := result["mcpServers"].(map[string]any)
	require.Contains(s.T(), servers, "other", "existing server should be preserved")
	require.Contains(s.T(), servers, "loop", "loop server should be added")
}

func (s *MainSuite) TestOnboardLocalAlreadyRegisteredUpdatesArgs() {
	tmpDir := s.T().TempDir()
	existing := `{"mcpServers":{"loop":{"command":"loop","args":["mcp"]}}}`
	require.NoError(s.T(), os.WriteFile(filepath.Join(tmpDir, ".mcp.json"), []byte(existing), 0644))

	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("Getwd").Return(tmpDir, nil)
	s.app.ensureAllChannelsFn = func(_, _ string) ([]ensureResult, error) {
		return []ensureResult{{Platform: "local", ChannelID: "ch-test", Created: true}}, nil
	}

	err := s.app.onboardLocal("http://localhost:8222", "", "")
	require.NoError(s.T(), err)

	// Verify file was updated with rebuilt args
	data, err := os.ReadFile(filepath.Join(tmpDir, ".mcp.json"))
	require.NoError(s.T(), err)
	var result map[string]any
	require.NoError(s.T(), json.Unmarshal(data, &result))
	servers := result["mcpServers"].(map[string]any)
	loop := servers["loop"].(map[string]any)
	args := loop["args"].([]any)
	require.Equal(s.T(), "mcp", args[0])
	require.Equal(s.T(), "--dir", args[1])
}

func (s *MainSuite) TestOnboardLocalInvalidExistingJSON() {
	tmpDir := s.T().TempDir()
	require.NoError(s.T(), os.WriteFile(filepath.Join(tmpDir, ".mcp.json"), []byte("not json"), 0644))

	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("Getwd").Return(tmpDir, nil)

	err := s.app.onboardLocal("http://localhost:8222", "", "")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "parsing existing .mcp.json")
}

func (s *MainSuite) TestOnboardLocalGetwdError() {
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("Getwd").Return("", errors.New("getwd error"))

	err := s.app.onboardLocal("http://localhost:8222", "", "")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "getting working directory")
}

func (s *MainSuite) TestOnboardLocalWriteError() {
	tmpDir := s.T().TempDir()
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("Getwd").Return(tmpDir, nil)
	sys.Override("WriteFile", mock.Anything, mock.Anything, mock.Anything).Return(errors.New("write error"))

	err := s.app.onboardLocal("http://localhost:8222", "", "")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "writing .mcp.json")
}

func (s *MainSuite) TestOnboardLocalCmdRunE() {
	tmpDir := s.T().TempDir()
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("Getwd").Return(tmpDir, nil)
	s.app.ensureAllChannelsFn = func(_, _ string) ([]ensureResult, error) {
		return []ensureResult{{Platform: "local", ChannelID: "ch-test", Created: true}}, nil
	}

	cmd := s.app.newOnboardLocalCmd()
	cmd.SetArgs([]string{"--api-url", "http://custom:9999"})
	err := cmd.Execute()
	require.NoError(s.T(), err)

	data, err := os.ReadFile(filepath.Join(tmpDir, ".mcp.json"))
	require.NoError(s.T(), err)

	var result map[string]any
	require.NoError(s.T(), json.Unmarshal(data, &result))

	servers := result["mcpServers"].(map[string]any)
	loop := servers["loop"].(map[string]any)
	args := loop["args"].([]any)
	require.Equal(s.T(), "http://custom:9999", args[4])
	require.Equal(s.T(), "--platform", args[5])
	require.Equal(s.T(), "local", args[6])
	require.Equal(s.T(), "--log", args[7])
	require.Equal(s.T(), filepath.Join(tmpDir, ".loop", "mcp.log"), args[8])
}

func (s *MainSuite) TestOnboardLocalEnsuresChannels() {
	tmpDir := s.T().TempDir()
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("Getwd").Return(tmpDir, nil)

	var calledAPIURL, calledDir string
	s.app.ensureAllChannelsFn = func(apiURL, dir string) ([]ensureResult, error) {
		calledAPIURL = apiURL
		calledDir = dir
		return []ensureResult{{Platform: "local", ChannelID: "ch-123", Created: true}}, nil
	}

	err := s.app.onboardLocal("http://localhost:8222", "", "")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "http://localhost:8222", calledAPIURL)
	require.Equal(s.T(), tmpDir, calledDir)
}

func (s *MainSuite) TestOnboardLocalEnsureChannelsFailsGracefully() {
	tmpDir := s.T().TempDir()
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("Getwd").Return(tmpDir, nil)
	s.app.ensureAllChannelsFn = func(_, _ string) ([]ensureResult, error) {
		return nil, errors.New("server not running")
	}

	err := s.app.onboardLocal("http://localhost:8222", "", "")
	require.NoError(s.T(), err, "onboardLocal should succeed even when ensureAllChannels fails")
}

func (s *MainSuite) TestOnboardLocalWithPlatformFlag() {
	tmpDir := s.T().TempDir()
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("Getwd").Return(tmpDir, nil)

	var calledAPIURL, calledDir, calledPlatform string
	s.app.ensureChannelFn = func(apiURL, dir, platform string) (string, error) {
		calledAPIURL = apiURL
		calledDir = dir
		calledPlatform = platform
		return "ch-local-123", nil
	}
	ensureAllCalled := false
	s.app.ensureAllChannelsFn = func(_, _ string) ([]ensureResult, error) {
		ensureAllCalled = true
		return nil, nil
	}

	err := s.app.onboardLocal("http://localhost:8222", "", "local")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "http://localhost:8222", calledAPIURL)
	require.Equal(s.T(), tmpDir, calledDir)
	require.Equal(s.T(), "local", calledPlatform)
	require.False(s.T(), ensureAllCalled, "ensureAllChannelsFunc should NOT be called when --platform is set")
}

func (s *MainSuite) TestOnboardLocalWithPlatformFlagEnsureError() {
	tmpDir := s.T().TempDir()
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("Getwd").Return(tmpDir, nil)

	s.app.ensureChannelFn = func(_, _, _ string) (string, error) {
		return "", errors.New("server not running")
	}

	err := s.app.onboardLocal("http://localhost:8222", "", "local")
	require.NoError(s.T(), err, "onboardLocal should succeed even when ensureChannel fails")
}

func (s *MainSuite) TestOnboardLocalAlreadyRegisteredStillEnsuresChannels() {
	tmpDir := s.T().TempDir()
	existing := `{"mcpServers":{"loop":{"command":"loop","args":["mcp","--dir","` + tmpDir + `","--api-url","http://localhost:8222"]}}}`
	require.NoError(s.T(), os.WriteFile(filepath.Join(tmpDir, ".mcp.json"), []byte(existing), 0644))

	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("Getwd").Return(tmpDir, nil)

	called := false
	s.app.ensureAllChannelsFn = func(_, _ string) ([]ensureResult, error) {
		called = true
		return []ensureResult{{Platform: "local", ChannelID: "ch-456", Created: false}}, nil
	}

	err := s.app.onboardLocal("http://localhost:8222", "", "")
	require.NoError(s.T(), err)
	require.True(s.T(), called, "ensureAllChannelsFunc should be called even when loop is already registered")
}

func (s *MainSuite) TestOnboardLocalProjectConfigWritten() {
	tmpDir := s.T().TempDir()
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("Getwd").Return(tmpDir, nil)
	s.app.ensureAllChannelsFn = func(_, _ string) ([]ensureResult, error) {
		return []ensureResult{{Platform: "local", ChannelID: "ch-test", Created: true}}, nil
	}

	err := s.app.onboardLocal("http://localhost:8222", "", "")
	require.NoError(s.T(), err)

	projectConfigPath := filepath.Join(tmpDir, ".loop", "config.json")
	data, err := os.ReadFile(projectConfigPath)
	require.NoError(s.T(), err)
	require.Equal(s.T(), string(config.ProjectExampleConfig), string(data))
}

func (s *MainSuite) TestOnboardLocalProjectConfigAlreadyExists() {
	tmpDir := s.T().TempDir()
	loopDir := filepath.Join(tmpDir, ".loop")
	require.NoError(s.T(), os.MkdirAll(loopDir, 0755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(loopDir, "config.json"), []byte(`{"claude_model":"custom"}`), 0644))

	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("Getwd").Return(tmpDir, nil)
	s.app.ensureAllChannelsFn = func(_, _ string) ([]ensureResult, error) {
		return []ensureResult{{Platform: "local", ChannelID: "ch-test", Created: true}}, nil
	}

	err := s.app.onboardLocal("http://localhost:8222", "", "")
	require.NoError(s.T(), err)

	// Verify existing config was NOT overwritten
	data, err := os.ReadFile(filepath.Join(loopDir, "config.json"))
	require.NoError(s.T(), err)
	require.Equal(s.T(), `{"claude_model":"custom"}`, string(data))
}

func (s *MainSuite) TestOnboardLocalProjectConfigMkdirError() {
	tmpDir := s.T().TempDir()
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("Getwd").Return(tmpDir, nil)
	sys.Override("MkdirAll", mock.Anything, mock.Anything).Return(errors.New("mkdir error"))
	s.app.ensureAllChannelsFn = func(_, _ string) ([]ensureResult, error) {
		return []ensureResult{{Platform: "local", ChannelID: "ch-test", Created: true}}, nil
	}

	err := s.app.onboardLocal("http://localhost:8222", "", "")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "creating .loop directory")
}

func (s *MainSuite) TestOnboardLocalProjectConfigWriteError() {
	tmpDir := s.T().TempDir()
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("Getwd").Return(tmpDir, nil)
	writeCall := sys.Override("WriteFile", mock.Anything, mock.Anything, mock.Anything).Maybe().Return(nil)
	writeCount := 0
	writeCall.RunFn = func(args mock.Arguments) {
		writeCount++
		if writeCount == 2 {
			writeCall.ReturnArguments = mock.Arguments{errors.New("write config error")}
			return
		}
		writeCall.ReturnArguments = mock.Arguments{os.WriteFile(args.String(0), args.Get(1).([]byte), args.Get(2).(os.FileMode))}
	}
	s.app.ensureAllChannelsFn = func(_, _ string) ([]ensureResult, error) {
		return []ensureResult{{Platform: "local", ChannelID: "ch-test", Created: true}}, nil
	}

	err := s.app.onboardLocal("http://localhost:8222", "", "")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "writing project config")
}

func (s *MainSuite) TestOnboardLocalTemplatesDirError() {
	tmpDir := s.T().TempDir()
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("Getwd").Return(tmpDir, nil)
	mkdirCall := sys.Override("MkdirAll", mock.Anything, mock.Anything).Maybe().Return(nil)
	mkdirCalls := 0
	mkdirCall.RunFn = func(args mock.Arguments) {
		mkdirCalls++
		if mkdirCalls == 2 { // Second mkdir is templates dir (after .loop dir)
			mkdirCall.ReturnArguments = mock.Arguments{errors.New("templates mkdir error")}
			return
		}
		mkdirCall.ReturnArguments = mock.Arguments{os.MkdirAll(args.String(0), args.Get(1).(os.FileMode))}
	}
	s.app.ensureAllChannelsFn = func(_, _ string) ([]ensureResult, error) {
		return []ensureResult{{Platform: "local", ChannelID: "ch-test", Created: true}}, nil
	}

	err := s.app.onboardLocal("http://localhost:8222", "", "")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "creating templates directory")
}

func (s *MainSuite) TestOnboardLocalTemplatesDirCreated() {
	tmpDir := s.T().TempDir()
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("Getwd").Return(tmpDir, nil)
	s.app.ensureAllChannelsFn = func(_, _ string) ([]ensureResult, error) {
		return []ensureResult{{Platform: "local", ChannelID: "ch-test", Created: true}}, nil
	}

	err := s.app.onboardLocal("http://localhost:8222", "", "")
	require.NoError(s.T(), err)

	// Verify templates directory was created
	templatesDir := filepath.Join(tmpDir, ".loop", "templates")
	info, err := os.Stat(templatesDir)
	require.NoError(s.T(), err)
	require.True(s.T(), info.IsDir())
}

func (s *MainSuite) TestOnboardLocalWithOwnerID() {
	tmpDir := s.T().TempDir()
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("Getwd").Return(tmpDir, nil)
	s.app.ensureAllChannelsFn = func(_, _ string) ([]ensureResult, error) {
		return []ensureResult{{Platform: "local", ChannelID: "ch-test", Created: true}}, nil
	}

	err := s.app.onboardLocal("http://localhost:8222", "U99887766", "")
	require.NoError(s.T(), err)

	projectConfigPath := filepath.Join(tmpDir, ".loop", "config.json")
	data, err := os.ReadFile(projectConfigPath)
	require.NoError(s.T(), err)

	content := string(data)
	require.Contains(s.T(), content, `"permissions": {`)
	require.Contains(s.T(), content, `"U99887766"`)
	require.NotContains(s.T(), content, `//  "owners"`)
}

func (s *MainSuite) TestOnboardLocalCmdWithOwnerIDFlag() {
	tmpDir := s.T().TempDir()
	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("Getwd").Return(tmpDir, nil)
	s.app.ensureAllChannelsFn = func(_, _ string) ([]ensureResult, error) {
		return []ensureResult{{Platform: "local", ChannelID: "ch-test", Created: true}}, nil
	}

	cmd := s.app.newOnboardLocalCmd()
	cmd.SetArgs([]string{"--owner-id", "ULOCAL123"})
	err := cmd.Execute()
	require.NoError(s.T(), err)

	projectConfigPath := filepath.Join(tmpDir, ".loop", "config.json")
	data, err := os.ReadFile(projectConfigPath)
	require.NoError(s.T(), err)

	content := string(data)
	require.Contains(s.T(), content, `"ULOCAL123"`)
	require.Contains(s.T(), content, `"permissions": {`)
}

// --- ensureImage tests ---

func (s *MainSuite) TestEnsureImageSkipsWhenExists() {
	dockerClient := new(mockDockerClient)
	dockerClient.On("ImageList", mock.Anything, "loop-agent:latest").Return([]string{"sha256:abc"}, nil)
	dockerClient.On("ImageList", mock.Anything, "loop-chrome:latest").Return([]string{"sha256:def"}, nil)

	cfg := &config.Config{
		LoopDir:        s.T().TempDir(),
		ContainerImage: "loop-agent:latest",
		Browser:        config.BrowserConfig{ChromeImage: "loop-chrome:latest"},
	}
	// Create container dir with Dockerfile so it doesn't try to write
	containerDir := filepath.Join(cfg.LoopDir, "container")
	require.NoError(s.T(), os.MkdirAll(containerDir, 0755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(containerDir, "Dockerfile"), []byte("FROM alpine"), 0644))
	require.NoError(s.T(), os.WriteFile(filepath.Join(containerDir, "chrome.Dockerfile"), []byte("FROM alpine"), 0644))

	s.app.sys = newPassthroughMock()
	err := s.app.defaultEnsureImage(context.Background(), dockerClient, cfg)
	require.NoError(s.T(), err)
	dockerClient.AssertExpectations(s.T())
}

func (s *MainSuite) TestEnsureImageBuildsWhenMissing() {
	dockerClient := new(mockDockerClient)
	dockerClient.On("ImageList", mock.Anything, "loop-agent:latest").Return([]string{}, nil)
	dockerClient.On("ImageBuild", mock.Anything, mock.Anything, "loop-agent:latest").Return(nil)
	dockerClient.On("ImageList", mock.Anything, "loop-chrome:latest").Return([]string{}, nil)
	dockerClient.On("ImageBuildFile", mock.Anything, mock.Anything, "chrome.Dockerfile", "loop-chrome:latest").Return(nil)

	cfg := &config.Config{
		LoopDir:        s.T().TempDir(),
		ContainerImage: "loop-agent:latest",
		Browser:        config.BrowserConfig{ChromeImage: "loop-chrome:latest"},
	}
	// Create container dir with Dockerfile
	containerDir := filepath.Join(cfg.LoopDir, "container")
	require.NoError(s.T(), os.MkdirAll(containerDir, 0755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(containerDir, "Dockerfile"), []byte("FROM alpine"), 0644))
	require.NoError(s.T(), os.WriteFile(filepath.Join(containerDir, "chrome.Dockerfile"), []byte("FROM alpine"), 0644))

	s.app.sys = newPassthroughMock()
	err := s.app.defaultEnsureImage(context.Background(), dockerClient, cfg)
	require.NoError(s.T(), err)
	dockerClient.AssertExpectations(s.T())
}

func (s *MainSuite) TestEnsureImageWritesEmbeddedFiles() {
	dockerClient := new(mockDockerClient)
	dockerClient.On("ImageList", mock.Anything, "loop-agent:latest").Return([]string{"sha256:abc"}, nil)
	dockerClient.On("ImageList", mock.Anything, "loop-chrome:latest").Return([]string{"sha256:def"}, nil)

	cfg := &config.Config{
		LoopDir:        s.T().TempDir(),
		ContainerImage: "loop-agent:latest",
		Browser:        config.BrowserConfig{ChromeImage: "loop-chrome:latest"},
	}

	s.app.sys = newPassthroughMock()

	err := s.app.defaultEnsureImage(context.Background(), dockerClient, cfg)
	require.NoError(s.T(), err)

	// Verify embedded files were written
	containerDir := filepath.Join(cfg.LoopDir, "container")
	dockerfileData, err := os.ReadFile(filepath.Join(containerDir, "Dockerfile"))
	require.NoError(s.T(), err)
	require.Contains(s.T(), string(dockerfileData), "FROM golang:")

	chromeData, err := os.ReadFile(filepath.Join(containerDir, "chrome.Dockerfile"))
	require.NoError(s.T(), err)
	require.Contains(s.T(), string(chromeData), "chromium")

	entrypointData, err := os.ReadFile(filepath.Join(containerDir, "entrypoint.sh"))
	require.NoError(s.T(), err)
	require.Contains(s.T(), string(entrypointData), `su-exec "$AGENT_USER" "$@"`)

	setupData, err := os.ReadFile(filepath.Join(containerDir, "setup.sh"))
	require.NoError(s.T(), err)
	require.Contains(s.T(), string(setupData), "#!/bin/sh")

	dockerClient.AssertExpectations(s.T())
}

func (s *MainSuite) TestEnsureImageListError() {
	dockerClient := new(mockDockerClient)
	dockerClient.On("ImageList", mock.Anything, "loop-agent:latest").Return(nil, errors.New("list error"))

	cfg := &config.Config{
		LoopDir:        s.T().TempDir(),
		ContainerImage: "loop-agent:latest",
		Browser:        config.BrowserConfig{ChromeImage: "loop-chrome:latest"},
	}
	containerDir := filepath.Join(cfg.LoopDir, "container")
	require.NoError(s.T(), os.MkdirAll(containerDir, 0755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(containerDir, "Dockerfile"), []byte("FROM alpine"), 0644))
	require.NoError(s.T(), os.WriteFile(filepath.Join(containerDir, "chrome.Dockerfile"), []byte("FROM alpine"), 0644))

	s.app.sys = newPassthroughMock()
	err := s.app.defaultEnsureImage(context.Background(), dockerClient, cfg)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "listing images")
	dockerClient.AssertExpectations(s.T())
}

func (s *MainSuite) TestEnsureImageMkdirError() {
	dockerClient := new(mockDockerClient)

	cfg := &config.Config{
		LoopDir:        s.T().TempDir(),
		ContainerImage: "loop-agent:latest",
		Browser:        config.BrowserConfig{ChromeImage: "loop-chrome:latest"},
	}

	sys := newPassthroughMock()
	s.app.sys = sys
	sys.Override("MkdirAll", mock.Anything, mock.Anything).Return(errors.New("mkdir error"))

	err := s.app.defaultEnsureImage(context.Background(), dockerClient, cfg)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "creating container directory")
}

func (s *MainSuite) TestEnsureImageWriteErrors() {
	tests := []struct {
		name      string
		failCallN int
		wantErr   string
	}{
		{"Dockerfile", 1, "writing Dockerfile"},
		{"entrypoint", 2, "writing entrypoint"},
		{"setup script", 3, "writing setup script"},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			dockerClient := new(mockDockerClient)
			cfg := &config.Config{
				LoopDir:        s.T().TempDir(),
				ContainerImage: "loop-agent:latest",
				Browser:        config.BrowserConfig{ChromeImage: "loop-chrome:latest"},
			}
			sys := newPassthroughMock()
			s.app.sys = sys
			writeCall := sys.Override("WriteFile", mock.Anything, mock.Anything, mock.Anything).Maybe().Return(nil)
			calls := 0
			writeCall.RunFn = func(args mock.Arguments) {
				calls++
				if calls == tt.failCallN {
					writeCall.ReturnArguments = mock.Arguments{errors.New("write error")}
					return
				}
				writeCall.ReturnArguments = mock.Arguments{os.WriteFile(args.String(0), args.Get(1).([]byte), args.Get(2).(os.FileMode))}
			}

			err := s.app.defaultEnsureImage(context.Background(), dockerClient, cfg)
			require.Error(s.T(), err)
			require.Contains(s.T(), err.Error(), tt.wantErr)
		})
	}
}

func (s *MainSuite) TestEnsureImageAgentBuildError() {
	dockerClient := new(mockDockerClient)
	dockerClient.On("ImageList", mock.Anything, "loop-agent:latest").Return([]string{}, nil)
	dockerClient.On("ImageBuild", mock.Anything, mock.Anything, "loop-agent:latest").Return(errors.New("agent build failed"))

	cfg := &config.Config{
		LoopDir:        s.T().TempDir(),
		ContainerImage: "loop-agent:latest",
		Browser:        config.BrowserConfig{ChromeImage: "loop-chrome:latest"},
	}
	containerDir := filepath.Join(cfg.LoopDir, "container")
	require.NoError(s.T(), os.MkdirAll(containerDir, 0755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(containerDir, "Dockerfile"), []byte("FROM alpine"), 0644))
	require.NoError(s.T(), os.WriteFile(filepath.Join(containerDir, "chrome.Dockerfile"), []byte("FROM alpine"), 0644))

	s.app.sys = newPassthroughMock()
	err := s.app.defaultEnsureImage(context.Background(), dockerClient, cfg)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "agent build failed")
}

func (s *MainSuite) TestEnsureImageChromeListError() {
	dockerClient := new(mockDockerClient)
	dockerClient.On("ImageList", mock.Anything, "loop-agent:latest").Return([]string{"sha256:abc"}, nil)
	dockerClient.On("ImageList", mock.Anything, "loop-chrome:latest").Return(nil, errors.New("chrome list error"))

	cfg := &config.Config{
		LoopDir:        s.T().TempDir(),
		ContainerImage: "loop-agent:latest",
		Browser:        config.BrowserConfig{ChromeImage: "loop-chrome:latest"},
	}
	containerDir := filepath.Join(cfg.LoopDir, "container")
	require.NoError(s.T(), os.MkdirAll(containerDir, 0755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(containerDir, "Dockerfile"), []byte("FROM alpine"), 0644))
	require.NoError(s.T(), os.WriteFile(filepath.Join(containerDir, "chrome.Dockerfile"), []byte("FROM alpine"), 0644))

	s.app.sys = newPassthroughMock()
	err := s.app.defaultEnsureImage(context.Background(), dockerClient, cfg)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "listing chrome images")
}

func (s *MainSuite) TestEnsureImageChromeBuildError() {
	dockerClient := new(mockDockerClient)
	dockerClient.On("ImageList", mock.Anything, "loop-agent:latest").Return([]string{"sha256:abc"}, nil)
	dockerClient.On("ImageList", mock.Anything, "loop-chrome:latest").Return([]string{}, nil)
	dockerClient.On("ImageBuildFile", mock.Anything, mock.Anything, "chrome.Dockerfile", "loop-chrome:latest").Return(errors.New("chrome build failed"))

	cfg := &config.Config{
		LoopDir:        s.T().TempDir(),
		ContainerImage: "loop-agent:latest",
		Browser:        config.BrowserConfig{ChromeImage: "loop-chrome:latest"},
	}
	containerDir := filepath.Join(cfg.LoopDir, "container")
	require.NoError(s.T(), os.MkdirAll(containerDir, 0755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(containerDir, "Dockerfile"), []byte("FROM alpine"), 0644))
	require.NoError(s.T(), os.WriteFile(filepath.Join(containerDir, "chrome.Dockerfile"), []byte("FROM alpine"), 0644))

	s.app.sys = newPassthroughMock()
	err := s.app.defaultEnsureImage(context.Background(), dockerClient, cfg)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "chrome build failed")
}

func (s *MainSuite) TestEnsureImageChromeDockerfileWriteError() {
	dockerClient := new(mockDockerClient)

	cfg := &config.Config{
		LoopDir:        s.T().TempDir(),
		ContainerImage: "loop-agent:latest",
		Browser:        config.BrowserConfig{ChromeImage: "loop-chrome:latest"},
	}
	// Create container dir with Dockerfile only (no chrome.Dockerfile triggers write)
	containerDir := filepath.Join(cfg.LoopDir, "container")
	require.NoError(s.T(), os.MkdirAll(containerDir, 0755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(containerDir, "Dockerfile"), []byte("FROM alpine"), 0644))

	sys := newPassthroughMock()
	s.app.sys = sys
	// First WriteFile call (chrome.Dockerfile) should fail
	writeCall := sys.Override("WriteFile", mock.Anything, mock.Anything, mock.Anything).Maybe().Return(nil)
	writeCall.RunFn = func(args mock.Arguments) {
		path := args.String(0)
		if strings.Contains(path, "chrome.Dockerfile") {
			writeCall.ReturnArguments = mock.Arguments{errors.New("write error")}
			return
		}
		writeCall.ReturnArguments = mock.Arguments{os.WriteFile(path, args.Get(1).([]byte), args.Get(2).(os.FileMode))}
	}

	err := s.app.defaultEnsureImage(context.Background(), dockerClient, cfg)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "writing chrome Dockerfile")
}

func (s *MainSuite) TestEnsureImageChromeEntrypointWriteError() {
	dockerClient := new(mockDockerClient)

	cfg := &config.Config{
		LoopDir:        s.T().TempDir(),
		ContainerImage: "loop-agent:latest",
		Browser:        config.BrowserConfig{ChromeImage: "loop-chrome:latest"},
	}
	containerDir := filepath.Join(cfg.LoopDir, "container")
	require.NoError(s.T(), os.MkdirAll(containerDir, 0755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(containerDir, "Dockerfile"), []byte("FROM alpine"), 0644))

	sys := newPassthroughMock()
	s.app.sys = sys
	writeCall := sys.Override("WriteFile", mock.Anything, mock.Anything, mock.Anything).Maybe().Return(nil)
	writeCall.RunFn = func(args mock.Arguments) {
		path := args.String(0)
		if strings.Contains(path, "chrome-entrypoint") {
			writeCall.ReturnArguments = mock.Arguments{errors.New("write error")}
			return
		}
		writeCall.ReturnArguments = mock.Arguments{os.WriteFile(path, args.Get(1).([]byte), args.Get(2).(os.FileMode))}
	}

	err := s.app.defaultEnsureImage(context.Background(), dockerClient, cfg)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "writing chrome entrypoint")
}

func (s *MainSuite) TestEnsureImageChromeDockerfileWrite() {
	dockerClient := new(mockDockerClient)
	dockerClient.On("ImageList", mock.Anything, "loop-agent:latest").Return([]string{"sha256:abc"}, nil)
	dockerClient.On("ImageList", mock.Anything, "loop-chrome:latest").Return([]string{"sha256:def"}, nil)

	cfg := &config.Config{
		LoopDir:        s.T().TempDir(),
		ContainerImage: "loop-agent:latest",
		Browser:        config.BrowserConfig{ChromeImage: "loop-chrome:latest"},
	}
	// Create container dir with Dockerfile but NOT chrome.Dockerfile — triggers write
	containerDir := filepath.Join(cfg.LoopDir, "container")
	require.NoError(s.T(), os.MkdirAll(containerDir, 0755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(containerDir, "Dockerfile"), []byte("FROM alpine"), 0644))

	s.app.sys = newPassthroughMock()
	err := s.app.defaultEnsureImage(context.Background(), dockerClient, cfg)
	require.NoError(s.T(), err)

	// Verify chrome.Dockerfile was written
	data, err := os.ReadFile(filepath.Join(containerDir, "chrome.Dockerfile"))
	require.NoError(s.T(), err)
	require.Contains(s.T(), string(data), "chromium")
}

// --- resolveVersion ---

func (s *MainSuite) TestResolveVersionKeepsNonDev() {
	require.Equal(s.T(), "1.2.3", resolveVersion("1.2.3"))
}

func (s *MainSuite) TestResolveVersionDevFallback() {
	// Default resolveVersion with "dev" — ReadBuildInfo returns "(devel)" in tests
	require.Equal(s.T(), "dev", resolveVersion("dev"))
}

func (s *MainSuite) TestDoResolveVersionFromBuildInfo() {
	got := doResolveVersion("dev", func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{Main: debug.Module{Version: "v1.5.0"}}, true
	})
	require.Equal(s.T(), "v1.5.0", got)
}

func (s *MainSuite) TestDoResolveVersionEmpty() {
	got := doResolveVersion("dev", func() (*debug.BuildInfo, bool) {
		return &debug.BuildInfo{Main: debug.Module{Version: ""}}, true
	})
	require.Equal(s.T(), "dev", got)
}

func (s *MainSuite) TestDoResolveVersionNotOK() {
	got := doResolveVersion("dev", func() (*debug.BuildInfo, bool) {
		return nil, false
	})
	require.Equal(s.T(), "dev", got)
}

// --- dumpTemplates ---

func (s *MainSuite) TestDumpTemplatesSkipsDirectories() {
	s.app.templatesFS = &dirEntryFS{}

	err := s.app.dumpTemplates(s.T().TempDir())
	require.NoError(s.T(), err)
}

// dirEntryFS returns a directory entry that dumpTemplates should skip.
type dirEntryFS struct{}

func (dirEntryFS) Open(name string) (fs.File, error) {
	if name == "templates" {
		return &fakeDirFile{entries: []fs.DirEntry{&fakeDirEntry{name: "subdir"}}}, nil
	}
	return nil, errors.New("not found")
}

func (dirEntryFS) ReadFile(string) ([]byte, error) { return nil, errors.New("should not be called") }

type fakeDirEntry struct{ name string }

func (e *fakeDirEntry) Name() string               { return e.name }
func (e *fakeDirEntry) IsDir() bool                { return true }
func (e *fakeDirEntry) Type() fs.FileMode          { return fs.ModeDir }
func (e *fakeDirEntry) Info() (fs.FileInfo, error) { return nil, nil }

// --- version ---

func (s *MainSuite) TestNewVersionCmd() {
	cmd := s.app.newVersionCmd()
	require.Equal(s.T(), "version", cmd.Use)
	require.Equal(s.T(), []string{"v"}, cmd.Aliases)
	require.NotNil(s.T(), cmd.Run)
}

func (s *MainSuite) TestVersionOutput() {
	s.app.version = "1.2.3"
	s.app.commit = "abc1234"
	s.app.date = "2026-01-01T00:00:00Z"

	cmd := s.app.newVersionCmd()
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	require.NoError(s.T(), err)
}

func (s *MainSuite) TestVersionOutputDefaults() {
	s.app.version = "dev"
	s.app.commit = "none"
	s.app.date = "unknown"

	cmd := s.app.newVersionCmd()
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	require.NoError(s.T(), err)
}

// --- newReadmeCmd ---

func (s *MainSuite) TestNewReadmeCmd() {
	cmd := s.app.newReadmeCmd()
	require.Equal(s.T(), "readme", cmd.Use)
	require.Equal(s.T(), []string{"r"}, cmd.Aliases)
	require.NotNil(s.T(), cmd.Run)
}

func (s *MainSuite) TestReadmeOutput() {
	cmd := s.app.newReadmeCmd()
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	require.NoError(s.T(), err)
}

func (s *MainSuite) TestServeLocalPlatformHappyPath() {
	m := s.setupServeMocks()
	m.cfg.Platforms = []types.Platform{types.PlatformLocal}
	// No bot tokens needed for local platform.
	m.cfg.DiscordToken = ""
	m.cfg.DiscordAppID = ""

	// Local platform creates a LocalBot (not the mock), so no mock bot expectations needed.
	s.app.newLocalBot = func(store db.Store, logger *slog.Logger) orchestrator.Bot {
		return local.NewBot(store, logger)
	}
	m.dockerClient.On("ContainerList", mock.Anything, "app", "loop-agent").Return([]string{}, nil)

	errCh := make(chan error, 1)
	go func() { errCh <- s.app.serve() }()

	time.Sleep(100 * time.Millisecond)
	p, err := os.FindProcess(os.Getpid())
	require.NoError(s.T(), err)
	require.NoError(s.T(), p.Signal(syscall.SIGINT))

	select {
	case err := <-errCh:
		require.NoError(s.T(), err)
	case <-time.After(5 * time.Second):
		s.T().Fatal("serve() did not return in time")
	}

	m.store.AssertExpectations(s.T())
}

func (s *MainSuite) TestServeWithTerminalManager() {
	m := s.setupServeMocks()
	m.setupHappyBot()

	// Provide a successful exec client to cover the terminal manager wiring path.
	s.app.newDockerExecClient = func() (terminal.ExecClient, error) {
		return &noopExecClient{}, nil
	}

	errCh := make(chan error, 1)
	go func() { errCh <- s.app.serve() }()

	time.Sleep(100 * time.Millisecond)
	p, err := os.FindProcess(os.Getpid())
	require.NoError(s.T(), err)
	require.NoError(s.T(), p.Signal(syscall.SIGINT))

	select {
	case err := <-errCh:
		require.NoError(s.T(), err)
	case <-time.After(5 * time.Second):
		s.T().Fatal("serve() did not return in time")
	}
}

func (s *MainSuite) TestServeWithBrowserProvider() {
	m := s.setupServeMocks()
	m.setupHappyBot()
	m.cfg.Browser.Enabled = true

	s.app.newBrowserProvider = func(_ string, _ *slog.Logger) (api.BrowserProvider, error) {
		return &noopBrowserProvider{}, nil
	}

	errCh := make(chan error, 1)
	go func() { errCh <- s.app.serve() }()

	time.Sleep(100 * time.Millisecond)
	p, err := os.FindProcess(os.Getpid())
	require.NoError(s.T(), err)
	require.NoError(s.T(), p.Signal(syscall.SIGINT))

	select {
	case err := <-errCh:
		require.NoError(s.T(), err)
	case <-time.After(5 * time.Second):
		s.T().Fatal("serve() did not return in time")
	}
}

func (s *MainSuite) TestServeWithBrowserProviderError() {
	m := s.setupServeMocks()
	m.setupHappyBot()
	m.cfg.Browser.Enabled = true

	s.app.newBrowserProvider = func(_ string, _ *slog.Logger) (api.BrowserProvider, error) {
		return nil, errors.New("no docker")
	}

	errCh := make(chan error, 1)
	go func() { errCh <- s.app.serve() }()

	time.Sleep(100 * time.Millisecond)
	p, err := os.FindProcess(os.Getpid())
	require.NoError(s.T(), err)
	require.NoError(s.T(), p.Signal(syscall.SIGINT))

	select {
	case err := <-errCh:
		require.NoError(s.T(), err)
	case <-time.After(5 * time.Second):
		s.T().Fatal("serve() did not return in time")
	}
}

func (s *MainSuite) TestLocalBotMessageHandler() {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := &testutil.MockStore{}
	localBot := local.NewBot(store, logger)
	sched := scheduler.NewTaskScheduler(store, nil, 0, logger)

	orch := orchestrator.New(store, localBot, nil, sched, logger, config.Config{})
	// Manually wire the OnMessage handler (in production, orch.Start does this via BotRouter).
	localBot.OnMessage(func(ctx context.Context, msg *bot.IncomingMessage) {
		orch.HandleMessage(ctx, msg)
	})

	// IsChannelActive returns an error so HandleMessage exits early.
	store.On("IsChannelActive", mock.Anything, "ch-1").Return(false, errors.New("db error"))

	localBot.HandleIncomingMessage(context.Background(), "ch-1", "user-1", "hello", "")

	store.AssertExpectations(s.T())
}

func (s *MainSuite) TestLocalBotMentionParsing() {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := &testutil.MockStore{}
	localBot := local.NewBot(store, logger)
	sched := scheduler.NewTaskScheduler(store, nil, 0, logger)
	orch := orchestrator.New(store, localBot, nil, sched, logger, config.Config{})
	localBot.OnMessage(func(ctx context.Context, msg *bot.IncomingMessage) {
		orch.HandleMessage(ctx, msg)
	})

	ch := &db.Channel{ID: 1, ChannelID: "ch-1"}
	store.On("IsChannelActive", mock.Anything, "ch-1").Return(true, nil)
	store.On("GetChannel", mock.Anything, "ch-1").Return(ch, nil)
	store.On("InsertMessage", mock.Anything, mock.MatchedBy(func(m *db.Message) bool {
		// Content should have @LoopBot stripped.
		return m.Content == "do this" && m.ChannelID == "ch-1"
	})).Return(nil)
	// Triggered (mention detected) — GetRecentMessages is called next but
	// returns an error so processing stops early.
	store.On("GetRecentMessages", mock.Anything, "ch-1", mock.Anything).
		Return(nil, errors.New("stop early"))

	localBot.HandleIncomingMessage(context.Background(), "ch-1", "user-1", "@LoopBot do this", "")

	store.AssertExpectations(s.T())
}

func (s *MainSuite) TestLocalBotPrefixParsing() {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := &testutil.MockStore{}
	localBot := local.NewBot(store, logger)
	sched := scheduler.NewTaskScheduler(store, nil, 0, logger)
	orch := orchestrator.New(store, localBot, nil, sched, logger, config.Config{})
	localBot.OnMessage(func(ctx context.Context, msg *bot.IncomingMessage) {
		orch.HandleMessage(ctx, msg)
	})

	ch := &db.Channel{ID: 1, ChannelID: "ch-1"}
	store.On("IsChannelActive", mock.Anything, "ch-1").Return(true, nil)
	store.On("GetChannel", mock.Anything, "ch-1").Return(ch, nil)
	store.On("InsertMessage", mock.Anything, mock.MatchedBy(func(m *db.Message) bool {
		// Content should have !loop prefix stripped.
		return m.Content == "check status" && m.ChannelID == "ch-1"
	})).Return(nil)
	store.On("GetRecentMessages", mock.Anything, "ch-1", mock.Anything).
		Return(nil, errors.New("stop early"))

	localBot.HandleIncomingMessage(context.Background(), "ch-1", "user-1", "!loop check status", "")

	store.AssertExpectations(s.T())
}

func (s *MainSuite) TestLocalBotPlainMessageTriggers() {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := &testutil.MockStore{}
	localBot := local.NewBot(store, logger)
	sched := scheduler.NewTaskScheduler(store, nil, 0, logger)
	orch := orchestrator.New(store, localBot, nil, sched, logger, config.Config{})
	localBot.OnMessage(func(ctx context.Context, msg *bot.IncomingMessage) {
		orch.HandleMessage(ctx, msg)
	})

	// Plain messages (no @LoopBot, no !loop) still trigger on local platform
	// because IsDM is always true.
	ch := &db.Channel{ID: 1, ChannelID: "ch-1"}
	store.On("IsChannelActive", mock.Anything, "ch-1").Return(true, nil)
	store.On("GetChannel", mock.Anything, "ch-1").Return(ch, nil)
	store.On("InsertMessage", mock.Anything, mock.MatchedBy(func(m *db.Message) bool {
		return m.Content == "just a note" && m.ChannelID == "ch-1"
	})).Return(nil)
	store.On("GetRecentMessages", mock.Anything, "ch-1", mock.Anything).
		Return(nil, errors.New("stop early"))

	localBot.HandleIncomingMessage(context.Background(), "ch-1", "user-1", "just a note", "")

	store.AssertExpectations(s.T())
}

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

// noopExecClient satisfies terminal.ExecClient for testing.
type noopExecClient struct{}

func (n *noopExecClient) ExecCreate(_ context.Context, _ string, _ []string, _ bool) (string, error) {
	return "", nil
}

func (n *noopExecClient) ExecAttach(_ context.Context, _ string) (io.ReadWriteCloser, error) {
	return nil, nil
}

func (n *noopExecClient) ExecResize(_ context.Context, _ string, _, _ uint) error {
	return nil
}

type noopBrowserProvider struct{}

func (n *noopBrowserProvider) EnsureBrowser(_ context.Context, _, _ string) error { return nil }
func (n *noopBrowserProvider) StopBrowser(_ context.Context, _ string) error      { return nil }
func (n *noopBrowserProvider) IsRunning(_ context.Context, _ string) bool         { return false }
func (n *noopBrowserProvider) GetCDPEndpoint(_ string) string                     { return "" }
func (n *noopBrowserProvider) GetContainerID(_ string) (string, bool)             { return "", false }
func (n *noopBrowserProvider) IsHostMode() bool                                   { return false }
