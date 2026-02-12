package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/radutopala/loop/internal/api"
	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/container"
	"github.com/radutopala/loop/internal/daemon"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/mcpserver"
	"github.com/radutopala/loop/internal/orchestrator"
	"github.com/radutopala/loop/internal/scheduler"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// --- Mock implementations ---

type mockStore struct {
	mock.Mock
}

func (m *mockStore) UpsertChannel(ctx context.Context, ch *db.Channel) error {
	return m.Called(ctx, ch).Error(0)
}

func (m *mockStore) GetChannel(ctx context.Context, channelID string) (*db.Channel, error) {
	args := m.Called(ctx, channelID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*db.Channel), args.Error(1)
}

func (m *mockStore) GetChannelByDirPath(ctx context.Context, dirPath string) (*db.Channel, error) {
	args := m.Called(ctx, dirPath)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*db.Channel), args.Error(1)
}

func (m *mockStore) IsChannelActive(ctx context.Context, channelID string) (bool, error) {
	args := m.Called(ctx, channelID)
	return args.Bool(0), args.Error(1)
}

func (m *mockStore) UpdateSessionID(ctx context.Context, channelID string, sessionID string) error {
	return m.Called(ctx, channelID, sessionID).Error(0)
}

func (m *mockStore) InsertMessage(ctx context.Context, msg *db.Message) error {
	return m.Called(ctx, msg).Error(0)
}

func (m *mockStore) GetUnprocessedMessages(ctx context.Context, channelID string) ([]*db.Message, error) {
	args := m.Called(ctx, channelID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*db.Message), args.Error(1)
}

func (m *mockStore) MarkMessagesProcessed(ctx context.Context, ids []int64) error {
	return m.Called(ctx, ids).Error(0)
}

func (m *mockStore) GetRecentMessages(ctx context.Context, channelID string, limit int) ([]*db.Message, error) {
	args := m.Called(ctx, channelID, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*db.Message), args.Error(1)
}

func (m *mockStore) CreateScheduledTask(ctx context.Context, task *db.ScheduledTask) (int64, error) {
	args := m.Called(ctx, task)
	return args.Get(0).(int64), args.Error(1)
}

func (m *mockStore) GetDueTasks(ctx context.Context, now time.Time) ([]*db.ScheduledTask, error) {
	args := m.Called(ctx, now)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*db.ScheduledTask), args.Error(1)
}

func (m *mockStore) UpdateScheduledTask(ctx context.Context, task *db.ScheduledTask) error {
	return m.Called(ctx, task).Error(0)
}

func (m *mockStore) DeleteScheduledTask(ctx context.Context, id int64) error {
	return m.Called(ctx, id).Error(0)
}

func (m *mockStore) ListScheduledTasks(ctx context.Context, channelID string) ([]*db.ScheduledTask, error) {
	args := m.Called(ctx, channelID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*db.ScheduledTask), args.Error(1)
}

func (m *mockStore) UpdateScheduledTaskEnabled(ctx context.Context, id int64, enabled bool) error {
	return m.Called(ctx, id, enabled).Error(0)
}

func (m *mockStore) GetScheduledTask(ctx context.Context, id int64) (*db.ScheduledTask, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*db.ScheduledTask), args.Error(1)
}

func (m *mockStore) InsertTaskRunLog(ctx context.Context, trl *db.TaskRunLog) (int64, error) {
	args := m.Called(ctx, trl)
	return args.Get(0).(int64), args.Error(1)
}

func (m *mockStore) UpdateTaskRunLog(ctx context.Context, trl *db.TaskRunLog) error {
	return m.Called(ctx, trl).Error(0)
}

func (m *mockStore) Close() error {
	return m.Called().Error(0)
}

func (m *mockStore) GetScheduledTaskByTemplateName(ctx context.Context, channelID, templateName string) (*db.ScheduledTask, error) {
	args := m.Called(ctx, channelID, templateName)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*db.ScheduledTask), args.Error(1)
}

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

func (m *mockDockerClient) ContainerList(ctx context.Context, labelKey, labelValue string) ([]string, error) {
	args := m.Called(ctx, labelKey, labelValue)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]string), args.Error(1)
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

func (m *mockBot) SendMessage(ctx context.Context, msg *orchestrator.OutgoingMessage) error {
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

func (m *mockBot) OnMessage(handler func(ctx context.Context, msg *orchestrator.IncomingMessage)) {
	m.Called(handler)
}

func (m *mockBot) OnInteraction(handler func(ctx context.Context, i any)) {
	m.Called(handler)
}

func (m *mockBot) BotUserID() string {
	return m.Called().String(0)
}

func (m *mockBot) CreateChannel(ctx context.Context, guildID, name string) (string, error) {
	args := m.Called(ctx, guildID, name)
	return args.String(0), args.Error(1)
}

func (m *mockBot) GetChannelParentID(ctx context.Context, channelID string) (string, error) {
	args := m.Called(ctx, channelID)
	return args.String(0), args.Error(1)
}

type closableDockerClient struct {
	*mockDockerClient
	closeFn func() error
}

func (c *closableDockerClient) Close() error {
	return c.closeFn()
}

type mockAPIServer struct {
	mock.Mock
}

func (m *mockAPIServer) Start(addr string) error {
	return m.Called(addr).Error(0)
}

func (m *mockAPIServer) Stop(ctx context.Context) error {
	return m.Called(ctx).Error(0)
}

// --- Test Suite ---

type MainSuite struct {
	suite.Suite
	origConfigLoad        func() (*config.Config, error)
	origNewDiscordBot     func(string, string, *slog.Logger) (orchestrator.Bot, error)
	origNewDockerClient   func() (container.DockerClient, error)
	origNewSQLiteStore    func(string) (db.Store, error)
	origOsExit            func(int)
	origNewAPIServer      func(scheduler.Scheduler, api.ChannelEnsurer, *slog.Logger) apiServer
	origNewMCPServer      func(string, string, mcpserver.HTTPClient, *slog.Logger) *mcpserver.Server
	origDaemonStart       func(daemon.System, string) error
	origDaemonStop        func(daemon.System) error
	origDaemonStatus      func(daemon.System) (string, error)
	origNewSystem         func() daemon.System
	origEnsureChannelFunc func(string, string) (string, error)
	origEnsureImage       func(context.Context, container.DockerClient, *config.Config) error
	origUserHomeDir       func() (string, error)
	origOsStat            func(string) (os.FileInfo, error)
	origOsMkdirAll        func(string, os.FileMode) error
	origOsWriteFile       func(string, []byte, os.FileMode) error
	origOsGetwd           func() (string, error)
	origOsReadFile        func(string) ([]byte, error)
}

func TestMainSuite(t *testing.T) {
	suite.Run(t, new(MainSuite))
}

func (s *MainSuite) SetupTest() {
	s.origConfigLoad = configLoad
	s.origNewDiscordBot = newDiscordBot
	s.origNewDockerClient = newDockerClient
	s.origNewSQLiteStore = newSQLiteStore
	s.origOsExit = osExit
	s.origNewAPIServer = newAPIServer
	s.origNewMCPServer = newMCPServer
	s.origDaemonStart = daemonStart
	s.origDaemonStop = daemonStop
	s.origDaemonStatus = daemonStatus
	s.origNewSystem = newSystem
	s.origEnsureChannelFunc = ensureChannelFunc
	s.origEnsureImage = ensureImage
	s.origUserHomeDir = userHomeDir
	s.origOsStat = osStat
	s.origOsMkdirAll = osMkdirAll
	s.origOsWriteFile = osWriteFile
	s.origOsGetwd = osGetwd
	s.origOsReadFile = osReadFile
}

func (s *MainSuite) TearDownTest() {
	configLoad = s.origConfigLoad
	newDiscordBot = s.origNewDiscordBot
	newDockerClient = s.origNewDockerClient
	newSQLiteStore = s.origNewSQLiteStore
	osExit = s.origOsExit
	newAPIServer = s.origNewAPIServer
	newMCPServer = s.origNewMCPServer
	daemonStart = s.origDaemonStart
	daemonStop = s.origDaemonStop
	daemonStatus = s.origDaemonStatus
	newSystem = s.origNewSystem
	ensureChannelFunc = s.origEnsureChannelFunc
	ensureImage = s.origEnsureImage
	userHomeDir = s.origUserHomeDir
	osStat = s.origOsStat
	osMkdirAll = s.origOsMkdirAll
	osWriteFile = s.origOsWriteFile
	osGetwd = s.origOsGetwd
	osReadFile = s.origOsReadFile
}

func testConfig() *config.Config {
	return &config.Config{
		DiscordToken: "test-token",
		DiscordAppID: "test-app",
		LogLevel:     "info",
		LogFormat:    "text",
		DBPath:       "test.db",
		PollInterval: time.Hour,
		APIAddr:      "127.0.0.1:0",
	}
}

// fakeAPIServer returns a newAPIServer func that creates a real api.Server
// but binds to a random port (127.0.0.1:0).
func fakeAPIServer() func(scheduler.Scheduler, api.ChannelEnsurer, *slog.Logger) apiServer {
	return func(sched scheduler.Scheduler, channels api.ChannelEnsurer, logger *slog.Logger) apiServer {
		return api.NewServer(sched, channels, logger)
	}
}

// --- newRootCmd ---

func (s *MainSuite) TestNewRootCmd() {
	cmd := newRootCmd()
	require.Equal(s.T(), "loop", cmd.Use)
	require.True(s.T(), cmd.HasSubCommands())

	want := map[string]bool{
		"serve":          false,
		"mcp":            false,
		"daemon:start":   false,
		"daemon:stop":    false,
		"daemon:status":  false,
		"onboard:global": false,
		"onboard:local":  false,
		"version":        false,
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
	cmd := newServeCmd()
	require.Equal(s.T(), "serve", cmd.Use)
	require.Equal(s.T(), []string{"s"}, cmd.Aliases)
	require.NotNil(s.T(), cmd.RunE)

	// Exercise the RunE closure to cover it.
	configLoad = func() (*config.Config, error) {
		return nil, errors.New("test")
	}
	err := cmd.RunE(nil, nil)
	require.Error(s.T(), err)
}

// --- newMCPCmd ---

func (s *MainSuite) TestNewMCPCmd() {
	cmd := newMCPCmd()
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
	cmd := newMCPCmd()
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	require.Error(s.T(), err)
}

func (s *MainSuite) TestNewMCPCmdMutuallyExclusive() {
	cmd := newMCPCmd()
	cmd.SetArgs([]string{"--channel-id", "ch1", "--dir", "/path", "--api-url", "http://localhost:8222"})
	err := cmd.Execute()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "if any flags in the group [channel-id dir] are set none of the others can be")
}

func (s *MainSuite) TestRunMCP() {
	logPath := filepath.Join(s.T().TempDir(), "mcp.log")
	called := false
	newMCPServer = func(channelID, apiURL string, httpClient mcpserver.HTTPClient, logger *slog.Logger) *mcpserver.Server {
		require.Equal(s.T(), "ch1", channelID)
		require.Equal(s.T(), "http://localhost:8222", apiURL)
		called = true
		return mcpserver.New(channelID, apiURL, httpClient, logger)
	}

	// runMCP will try to use StdioTransport which will fail/close immediately in test.
	// We just verify the function is wired correctly.
	_ = runMCP("ch1", "http://localhost:8222", "", logPath)
	require.True(s.T(), called)
}

func (s *MainSuite) TestRunMCPLogOpenError() {
	err := runMCP("ch1", "http://localhost:8222", "", "/nonexistent/dir/mcp.log")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "opening mcp log")
}

func (s *MainSuite) TestRunMCPWithConfigLoad() {
	// Test that runMCP successfully loads config for log level/format
	logPath := filepath.Join(s.T().TempDir(), "mcp.log")

	// Mock configLoad to return a config
	origConfigLoad := configLoad
	configLoad = func() (*config.Config, error) {
		return &config.Config{
			LogLevel:  "debug",
			LogFormat: "json",
		}, nil
	}
	defer func() { configLoad = origConfigLoad }()

	// Mock newMCPServer to avoid actually running the server
	called := false
	newMCPServer = func(channelID, apiURL string, httpClient mcpserver.HTTPClient, logger *slog.Logger) *mcpserver.Server {
		called = true
		// Verify logger was created (we can't easily inspect its level, but at least it was called)
		require.NotNil(s.T(), logger)
		// Return a real server that we won't run
		return mcpserver.New(channelID, apiURL, httpClient, logger)
	}

	// This will fail to run the server (no stdio), but that's OK - we just want to test config loading
	_ = runMCP("ch1", "http://localhost:8222", "", logPath)
	require.True(s.T(), called)
}

func (s *MainSuite) TestRunMCPWithInMemoryTransport() {
	// Verify runMCP constructs the server correctly.
	// We can't test stdio, but we test the MCP server is functional via in-memory transport.
	srv := mcpserver.New("ch1", "http://localhost:8222", http.DefaultClient, nil)

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
	require.Len(s.T(), res.Tools, 5)
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

	channelID, err := ensureChannel(ts.URL, "/home/user/dev/loop")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "ch-resolved", channelID)
}

func (s *MainSuite) TestEnsureChannelServerError() {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "something failed", http.StatusInternalServerError)
	}))
	defer ts.Close()

	_, err := ensureChannel(ts.URL, "/path")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "ensure channel API returned 500")
}

func (s *MainSuite) TestEnsureChannelConnectionError() {
	_, err := ensureChannel("http://127.0.0.1:1", "/path")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "calling ensure channel API")
}

func (s *MainSuite) TestEnsureChannelInvalidJSON() {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("not json"))
	}))
	defer ts.Close()

	_, err := ensureChannel(ts.URL, "/path")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "decoding ensure channel response")
}

func (s *MainSuite) TestRunMCPWithDir() {
	logPath := filepath.Join(s.T().TempDir(), "mcp.log")
	called := false
	newMCPServer = func(channelID, apiURL string, httpClient mcpserver.HTTPClient, logger *slog.Logger) *mcpserver.Server {
		require.Equal(s.T(), "resolved-ch", channelID)
		called = true
		return mcpserver.New(channelID, apiURL, httpClient, logger)
	}
	ensureChannelFunc = func(apiURL, dirPath string) (string, error) {
		require.Equal(s.T(), "http://localhost:8222", apiURL)
		require.Equal(s.T(), "/home/user/dev/loop", dirPath)
		return "resolved-ch", nil
	}

	_ = runMCP("", "http://localhost:8222", "/home/user/dev/loop", logPath)
	require.True(s.T(), called)
}

func (s *MainSuite) TestRunMCPWithDirEnsureError() {
	ensureChannelFunc = func(_, _ string) (string, error) {
		return "", errors.New("ensure failed")
	}

	err := runMCP("", "http://localhost:8222", "/path", "/tmp/mcp.log")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "ensuring channel for dir")
}

func (s *MainSuite) TestNewMCPCmdWithDirFlag() {
	logPath := filepath.Join(s.T().TempDir(), "mcp.log")
	called := false
	newMCPServer = func(channelID, apiURL string, httpClient mcpserver.HTTPClient, logger *slog.Logger) *mcpserver.Server {
		require.Equal(s.T(), "resolved-ch", channelID)
		called = true
		return mcpserver.New(channelID, apiURL, httpClient, logger)
	}
	ensureChannelFunc = func(_, _ string) (string, error) {
		return "resolved-ch", nil
	}

	cmd := newMCPCmd()
	cmd.SetArgs([]string{"--dir", "/home/user/dev/loop", "--api-url", "http://test:8222", "--log", logPath})
	_ = cmd.Execute()
	require.True(s.T(), called)
}

// --- serve() error cases ---

func (s *MainSuite) TestServeConfigLoadError() {
	configLoad = func() (*config.Config, error) {
		return nil, errors.New("config error")
	}

	err := serve()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "config error")
}

func (s *MainSuite) TestServeSQLiteStoreError() {
	configLoad = func() (*config.Config, error) {
		return testConfig(), nil
	}
	newSQLiteStore = func(_ string) (db.Store, error) {
		return nil, errors.New("db error")
	}

	err := serve()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "opening database")
}

func (s *MainSuite) TestServeDiscordBotError() {
	store := new(mockStore)
	store.On("Close").Return(nil)

	configLoad = func() (*config.Config, error) {
		return testConfig(), nil
	}
	newSQLiteStore = func(_ string) (db.Store, error) {
		return store, nil
	}
	newDiscordBot = func(_, _ string, _ *slog.Logger) (orchestrator.Bot, error) {
		return nil, errors.New("discord error")
	}

	err := serve()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "creating discord bot")
	store.AssertExpectations(s.T())
}

func (s *MainSuite) TestServeDockerClientError() {
	store := new(mockStore)
	store.On("Close").Return(nil)
	bot := new(mockBot)

	configLoad = func() (*config.Config, error) {
		return testConfig(), nil
	}
	newSQLiteStore = func(_ string) (db.Store, error) {
		return store, nil
	}
	newDiscordBot = func(_, _ string, _ *slog.Logger) (orchestrator.Bot, error) {
		return bot, nil
	}
	newDockerClient = func() (container.DockerClient, error) {
		return nil, errors.New("docker error")
	}

	err := serve()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "creating docker client")
	store.AssertExpectations(s.T())
}

func (s *MainSuite) TestServeEnsureImageError() {
	store := new(mockStore)
	store.On("Close").Return(nil)
	bot := new(mockBot)
	dockerClient := new(mockDockerClient)

	configLoad = func() (*config.Config, error) {
		return testConfig(), nil
	}
	newSQLiteStore = func(_ string) (db.Store, error) {
		return store, nil
	}
	newDiscordBot = func(_, _ string, _ *slog.Logger) (orchestrator.Bot, error) {
		return bot, nil
	}
	newDockerClient = func() (container.DockerClient, error) {
		return dockerClient, nil
	}
	ensureImage = func(_ context.Context, _ container.DockerClient, _ *config.Config) error {
		return errors.New("image build failed")
	}

	err := serve()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "ensuring agent image")
	store.AssertExpectations(s.T())
}

func (s *MainSuite) TestServeAPIServerStartError() {
	store := new(mockStore)
	store.On("Close").Return(nil)
	bot := new(mockBot)
	dockerClient := new(mockDockerClient)

	configLoad = func() (*config.Config, error) {
		cfg := testConfig()
		cfg.APIAddr = "invalid-addr-no-port"
		return cfg, nil
	}
	newSQLiteStore = func(_ string) (db.Store, error) {
		return store, nil
	}
	newDiscordBot = func(_, _ string, _ *slog.Logger) (orchestrator.Bot, error) {
		return bot, nil
	}
	newDockerClient = func() (container.DockerClient, error) {
		return dockerClient, nil
	}
	ensureImage = func(_ context.Context, _ container.DockerClient, _ *config.Config) error {
		return nil
	}
	newAPIServer = fakeAPIServer()

	err := serve()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "starting api server")
	store.AssertExpectations(s.T())
}

func (s *MainSuite) TestServeOrchestratorStartError() {
	store := new(mockStore)
	store.On("Close").Return(nil)

	bot := new(mockBot)
	bot.On("OnMessage", mock.Anything).Return()
	bot.On("OnInteraction", mock.Anything).Return()
	bot.On("RegisterCommands", mock.Anything).Return(errors.New("register failed"))

	dockerClient := new(mockDockerClient)

	configLoad = func() (*config.Config, error) {
		return testConfig(), nil
	}
	newSQLiteStore = func(_ string) (db.Store, error) {
		return store, nil
	}
	newDiscordBot = func(_, _ string, _ *slog.Logger) (orchestrator.Bot, error) {
		return bot, nil
	}
	newDockerClient = func() (container.DockerClient, error) {
		return dockerClient, nil
	}
	ensureImage = func(_ context.Context, _ container.DockerClient, _ *config.Config) error {
		return nil
	}
	newAPIServer = fakeAPIServer()

	err := serve()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "starting orchestrator")
	store.AssertExpectations(s.T())
	bot.AssertExpectations(s.T())
}

func (s *MainSuite) TestServeHappyPathShutdown() {
	store := new(mockStore)
	store.On("Close").Return(nil)

	bot := new(mockBot)
	bot.On("OnMessage", mock.Anything).Return()
	bot.On("OnInteraction", mock.Anything).Return()
	bot.On("RegisterCommands", mock.Anything).Return(nil)
	bot.On("Start", mock.Anything).Return(nil)
	bot.On("Stop").Return(nil)

	dockerClient := new(mockDockerClient)
	dockerClient.On("ContainerList", mock.Anything, "app", "loop-agent").Return([]string{}, nil)

	configLoad = func() (*config.Config, error) {
		return testConfig(), nil
	}
	newSQLiteStore = func(_ string) (db.Store, error) {
		return store, nil
	}
	newDiscordBot = func(_, _ string, _ *slog.Logger) (orchestrator.Bot, error) {
		return bot, nil
	}
	newDockerClient = func() (container.DockerClient, error) {
		return dockerClient, nil
	}
	ensureImage = func(_ context.Context, _ container.DockerClient, _ *config.Config) error {
		return nil
	}
	newAPIServer = fakeAPIServer()

	errCh := make(chan error, 1)
	go func() {
		errCh <- serve()
	}()

	// Give serve() time to start, then send SIGINT.
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

	store.AssertExpectations(s.T())
	bot.AssertExpectations(s.T())
}

func (s *MainSuite) TestServeHappyPathWithGuildID() {
	store := new(mockStore)
	store.On("Close").Return(nil)

	bot := new(mockBot)
	bot.On("OnMessage", mock.Anything).Return()
	bot.On("OnInteraction", mock.Anything).Return()
	bot.On("RegisterCommands", mock.Anything).Return(nil)
	bot.On("Start", mock.Anything).Return(nil)
	bot.On("Stop").Return(nil)

	dockerClient := new(mockDockerClient)
	dockerClient.On("ContainerList", mock.Anything, "app", "loop-agent").Return([]string{}, nil)

	cfg := testConfig()
	cfg.DiscordGuildID = "guild-123"
	configLoad = func() (*config.Config, error) {
		return cfg, nil
	}
	newSQLiteStore = func(_ string) (db.Store, error) {
		return store, nil
	}
	newDiscordBot = func(_, _ string, _ *slog.Logger) (orchestrator.Bot, error) {
		return bot, nil
	}
	newDockerClient = func() (container.DockerClient, error) {
		return dockerClient, nil
	}
	ensureImage = func(_ context.Context, _ container.DockerClient, _ *config.Config) error {
		return nil
	}
	channelsCh := make(chan api.ChannelEnsurer, 1)
	newAPIServer = func(sched scheduler.Scheduler, channels api.ChannelEnsurer, logger *slog.Logger) apiServer {
		channelsCh <- channels
		return api.NewServer(sched, channels, logger)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- serve()
	}()

	gotChannels := <-channelsCh
	require.NotNil(s.T(), gotChannels)

	// Wait for serve() to set up signal handler via signal.NotifyContext.
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

	store.AssertExpectations(s.T())
	bot.AssertExpectations(s.T())
}

func (s *MainSuite) TestServeHappyPathShutdownWithStopError() {
	store := new(mockStore)
	store.On("Close").Return(nil)

	bot := new(mockBot)
	bot.On("OnMessage", mock.Anything).Return()
	bot.On("OnInteraction", mock.Anything).Return()
	bot.On("RegisterCommands", mock.Anything).Return(nil)
	bot.On("Start", mock.Anything).Return(nil)
	bot.On("Stop").Return(errors.New("bot stop error"))

	dockerClient := new(mockDockerClient)
	dockerClient.On("ContainerList", mock.Anything, "app", "loop-agent").Return([]string{}, nil)

	configLoad = func() (*config.Config, error) {
		return testConfig(), nil
	}
	newSQLiteStore = func(_ string) (db.Store, error) {
		return store, nil
	}
	newDiscordBot = func(_, _ string, _ *slog.Logger) (orchestrator.Bot, error) {
		return bot, nil
	}
	newDockerClient = func() (container.DockerClient, error) {
		return dockerClient, nil
	}
	ensureImage = func(_ context.Context, _ container.DockerClient, _ *config.Config) error {
		return nil
	}
	newAPIServer = fakeAPIServer()

	errCh := make(chan error, 1)
	go func() {
		errCh <- serve()
	}()

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

	bot.AssertExpectations(s.T())
}

func (s *MainSuite) TestServeHappyPathShutdownWithAPIStopError() {
	store := new(mockStore)
	store.On("Close").Return(nil)

	bot := new(mockBot)
	bot.On("OnMessage", mock.Anything).Return()
	bot.On("OnInteraction", mock.Anything).Return()
	bot.On("RegisterCommands", mock.Anything).Return(nil)
	bot.On("Start", mock.Anything).Return(nil)
	bot.On("Stop").Return(nil)

	dockerClient := new(mockDockerClient)
	dockerClient.On("ContainerList", mock.Anything, "app", "loop-agent").Return([]string{}, nil)

	mockAPI := new(mockAPIServer)
	mockAPI.On("Start", mock.Anything).Return(nil)
	mockAPI.On("Stop", mock.Anything).Return(errors.New("api stop error"))

	configLoad = func() (*config.Config, error) {
		return testConfig(), nil
	}
	newSQLiteStore = func(_ string) (db.Store, error) {
		return store, nil
	}
	newDiscordBot = func(_, _ string, _ *slog.Logger) (orchestrator.Bot, error) {
		return bot, nil
	}
	newDockerClient = func() (container.DockerClient, error) {
		return dockerClient, nil
	}
	ensureImage = func(_ context.Context, _ container.DockerClient, _ *config.Config) error {
		return nil
	}
	newAPIServer = func(_ scheduler.Scheduler, _ api.ChannelEnsurer, _ *slog.Logger) apiServer {
		return mockAPI
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- serve()
	}()

	time.Sleep(100 * time.Millisecond)
	p, err := os.FindProcess(os.Getpid())
	require.NoError(s.T(), err)
	require.NoError(s.T(), p.Signal(syscall.SIGINT))

	select {
	case err := <-errCh:
		// serve() returns nil even when apiSrv.Stop() fails — it logs the error.
		require.NoError(s.T(), err)
	case <-time.After(5 * time.Second):
		s.T().Fatal("serve() did not return in time")
	}

	mockAPI.AssertExpectations(s.T())
	bot.AssertExpectations(s.T())
}

func (s *MainSuite) TestServeDockerClientCloserCalled() {
	store := new(mockStore)
	store.On("Close").Return(nil)

	bot := new(mockBot)
	bot.On("OnMessage", mock.Anything).Return()
	bot.On("OnInteraction", mock.Anything).Return()
	bot.On("RegisterCommands", mock.Anything).Return(errors.New("fail"))

	closeCalled := false
	dockerClient := &closableDockerClient{
		mockDockerClient: new(mockDockerClient),
		closeFn:          func() error { closeCalled = true; return nil },
	}

	configLoad = func() (*config.Config, error) {
		return testConfig(), nil
	}
	newSQLiteStore = func(_ string) (db.Store, error) {
		return store, nil
	}
	newDiscordBot = func(_, _ string, _ *slog.Logger) (orchestrator.Bot, error) {
		return bot, nil
	}
	newDockerClient = func() (container.DockerClient, error) {
		return dockerClient, nil
	}
	ensureImage = func(_ context.Context, _ container.DockerClient, _ *config.Config) error {
		return nil
	}
	newAPIServer = fakeAPIServer()

	err := serve()
	require.Error(s.T(), err)
	require.True(s.T(), closeCalled, "docker client Close() should be called via io.Closer")
}

// --- main() ---

func (s *MainSuite) TestMainSuccess() {
	origArgs := os.Args
	defer func() { os.Args = origArgs }()
	os.Args = []string{"loop", "--help"}

	exitCalled := false
	osExit = func(code int) {
		exitCalled = true
	}

	main()
	require.False(s.T(), exitCalled, "os.Exit should not be called on success")
}

func (s *MainSuite) TestMainError() {
	origArgs := os.Args
	defer func() { os.Args = origArgs }()
	os.Args = []string{"loop", "serve"}

	configLoad = func() (*config.Config, error) {
		return nil, errors.New("fail")
	}

	var exitCode int
	osExit = func(code int) {
		exitCode = code
	}

	main()
	require.Equal(s.T(), 1, exitCode)
}

// --- Verify the default var functions have correct signatures ---

func (s *MainSuite) TestDefaultVarSignatures() {
	require.NotNil(s.T(), configLoad)
	require.NotNil(s.T(), newDiscordBot)
	require.NotNil(s.T(), newDockerClient)
	require.NotNil(s.T(), newSQLiteStore)
	require.NotNil(s.T(), newAPIServer)
	require.NotNil(s.T(), newMCPServer)

	// Verify newAPIServer produces a non-nil apiServer
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	apiSrv := newAPIServer(nil, nil, logger)
	require.NotNil(s.T(), apiSrv)

	// Verify newMCPServer produces a non-nil server
	mcpSrv := newMCPServer("ch1", "http://localhost:8222", http.DefaultClient, nil)
	require.NotNil(s.T(), mcpSrv)
}

func (s *MainSuite) TestDefaultNewSQLiteStore() {
	// Exercise the default newSQLiteStore with a temp file.
	tmpDir := s.T().TempDir()
	store, err := s.origNewSQLiteStore(tmpDir + "/test.db")
	require.NoError(s.T(), err)
	require.NotNil(s.T(), store)
	require.NoError(s.T(), store.Close())
}

func (s *MainSuite) TestDefaultNewDiscordBot() {
	// Exercise the default newDiscordBot — discordgo.New succeeds without a server.
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bot, err := s.origNewDiscordBot("fake-token", "fake-app-id", logger)
	require.NoError(s.T(), err)
	require.NotNil(s.T(), bot)
}

func (s *MainSuite) TestDefaultNewDockerClient() {
	// Exercise the default newDockerClient — Docker client creation succeeds without a running daemon.
	dc, err := s.origNewDockerClient()
	require.NoError(s.T(), err)
	require.NotNil(s.T(), dc)
	if closer, ok := dc.(io.Closer); ok {
		_ = closer.Close()
	}
}

// --- daemon commands ---

func (s *MainSuite) TestNewDaemonStartCmd() {
	cmd := newDaemonStartCmd()
	require.Equal(s.T(), "daemon:start", cmd.Use)
	require.Equal(s.T(), []string{"d:start", "up"}, cmd.Aliases)
	require.NotNil(s.T(), cmd.RunE)
}

func (s *MainSuite) TestNewDaemonStopCmd() {
	cmd := newDaemonStopCmd()
	require.Equal(s.T(), "daemon:stop", cmd.Use)
	require.Equal(s.T(), []string{"d:stop", "down"}, cmd.Aliases)
	require.NotNil(s.T(), cmd.RunE)
}

func (s *MainSuite) TestNewDaemonStatusCmd() {
	cmd := newDaemonStatusCmd()
	require.Equal(s.T(), "daemon:status", cmd.Use)
	require.Equal(s.T(), []string{"d:status"}, cmd.Aliases)
	require.NotNil(s.T(), cmd.RunE)
}

func (s *MainSuite) TestDaemonStartSuccess() {
	configLoad = func() (*config.Config, error) { return testConfig(), nil }
	daemonStart = func(_ daemon.System, _ string) error { return nil }
	newSystem = func() daemon.System { return daemon.RealSystem{} }

	cmd := newDaemonStartCmd()
	err := cmd.Execute()
	require.NoError(s.T(), err)
}

func (s *MainSuite) TestDaemonStartError() {
	configLoad = func() (*config.Config, error) { return testConfig(), nil }
	daemonStart = func(_ daemon.System, _ string) error { return errors.New("start fail") }
	newSystem = func() daemon.System { return daemon.RealSystem{} }

	cmd := newDaemonStartCmd()
	err := cmd.Execute()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "start fail")
}

func (s *MainSuite) TestDaemonStartConfigError() {
	configLoad = func() (*config.Config, error) { return nil, errors.New("config fail") }

	cmd := newDaemonStartCmd()
	err := cmd.Execute()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "config fail")
}

func (s *MainSuite) TestDaemonStopSuccess() {
	daemonStop = func(_ daemon.System) error { return nil }
	newSystem = func() daemon.System { return daemon.RealSystem{} }

	cmd := newDaemonStopCmd()
	err := cmd.Execute()
	require.NoError(s.T(), err)
}

func (s *MainSuite) TestDaemonStopError() {
	daemonStop = func(_ daemon.System) error { return errors.New("stop fail") }
	newSystem = func() daemon.System { return daemon.RealSystem{} }

	cmd := newDaemonStopCmd()
	err := cmd.Execute()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "stop fail")
}

func (s *MainSuite) TestDaemonStatusSuccess() {
	daemonStatus = func(_ daemon.System) (string, error) { return "running", nil }
	newSystem = func() daemon.System { return daemon.RealSystem{} }

	cmd := newDaemonStatusCmd()
	err := cmd.Execute()
	require.NoError(s.T(), err)
}

func (s *MainSuite) TestDaemonStatusError() {
	daemonStatus = func(_ daemon.System) (string, error) { return "", errors.New("status fail") }
	newSystem = func() daemon.System { return daemon.RealSystem{} }

	cmd := newDaemonStatusCmd()
	err := cmd.Execute()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "status fail")
}

func (s *MainSuite) TestDefaultDaemonVars() {
	require.NotNil(s.T(), s.origDaemonStart)
	require.NotNil(s.T(), s.origDaemonStop)
	require.NotNil(s.T(), s.origDaemonStatus)
	require.NotNil(s.T(), s.origNewSystem)

	sys := s.origNewSystem()
	require.IsType(s.T(), daemon.RealSystem{}, sys)
}

// --- onboard:global ---

func (s *MainSuite) TestNewOnboardGlobalCmd() {
	cmd := newOnboardGlobalCmd()
	require.Equal(s.T(), "onboard:global", cmd.Use)
	require.Equal(s.T(), []string{"o:global", "setup"}, cmd.Aliases)
	require.NotNil(s.T(), cmd.RunE)
	require.NotNil(s.T(), cmd.Flags().Lookup("force"))
}

func (s *MainSuite) TestNewOnboardLocalCmd() {
	cmd := newOnboardLocalCmd()
	require.Equal(s.T(), "onboard:local", cmd.Use)
	require.Equal(s.T(), []string{"o:local", "init"}, cmd.Aliases)
	require.NotNil(s.T(), cmd.RunE)
	f := cmd.Flags().Lookup("api-url")
	require.NotNil(s.T(), f)
	require.Equal(s.T(), "http://localhost:8222", f.DefValue)
}

func (s *MainSuite) TestOnboardGlobalSuccess() {
	tmpDir := s.T().TempDir()
	userHomeDir = func() (string, error) {
		return tmpDir, nil
	}
	osStat = os.Stat
	osMkdirAll = os.MkdirAll
	osWriteFile = os.WriteFile

	err := onboardGlobal(false)
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

	// Verify templates directory was created
	templatesDir := filepath.Join(tmpDir, ".loop", "templates")
	info, err := os.Stat(templatesDir)
	require.NoError(s.T(), err)
	require.True(s.T(), info.IsDir())
}

func (s *MainSuite) TestOnboardGlobalConfigAlreadyExists() {
	tmpDir := s.T().TempDir()
	loopDir := filepath.Join(tmpDir, ".loop")
	configPath := filepath.Join(loopDir, "config.json")

	require.NoError(s.T(), os.MkdirAll(loopDir, 0755))
	require.NoError(s.T(), os.WriteFile(configPath, []byte("existing"), 0600))

	userHomeDir = func() (string, error) {
		return tmpDir, nil
	}
	osStat = os.Stat
	osMkdirAll = os.MkdirAll
	osWriteFile = os.WriteFile

	err := onboardGlobal(false)
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

	userHomeDir = func() (string, error) {
		return tmpDir, nil
	}
	osStat = os.Stat
	osMkdirAll = os.MkdirAll
	osWriteFile = os.WriteFile

	err := onboardGlobal(true)
	require.NoError(s.T(), err)

	// Verify config was overwritten
	data, err := os.ReadFile(configPath)
	require.NoError(s.T(), err)
	require.Contains(s.T(), string(data), "discord_token")
	require.Contains(s.T(), string(data), "task_templates")
	require.NotContains(s.T(), string(data), "old config")
}

func (s *MainSuite) TestOnboardGlobalHomeDirError() {
	userHomeDir = func() (string, error) {
		return "", errors.New("home dir error")
	}

	err := onboardGlobal(false)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "getting home directory")
}

func (s *MainSuite) TestOnboardGlobalMkdirAllError() {
	tmpDir := s.T().TempDir()
	userHomeDir = func() (string, error) {
		return tmpDir, nil
	}
	osStat = os.Stat
	osMkdirAll = func(_ string, _ os.FileMode) error {
		return errors.New("mkdir error")
	}

	err := onboardGlobal(false)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "creating loop directory")
}

func (s *MainSuite) TestOnboardGlobalWriteFileError() {
	tmpDir := s.T().TempDir()
	userHomeDir = func() (string, error) {
		return tmpDir, nil
	}
	osStat = os.Stat
	osMkdirAll = os.MkdirAll
	osWriteFile = func(_ string, _ []byte, _ os.FileMode) error {
		return errors.New("write error")
	}

	err := onboardGlobal(false)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "writing config file")
}

func (s *MainSuite) TestOnboardGlobalCmdWithForceFlag() {
	tmpDir := s.T().TempDir()
	loopDir := filepath.Join(tmpDir, ".loop")
	configPath := filepath.Join(loopDir, "config.json")

	require.NoError(s.T(), os.MkdirAll(loopDir, 0755))
	require.NoError(s.T(), os.WriteFile(configPath, []byte("old"), 0600))

	userHomeDir = func() (string, error) {
		return tmpDir, nil
	}
	osStat = os.Stat
	osMkdirAll = os.MkdirAll
	osWriteFile = os.WriteFile

	cmd := newOnboardGlobalCmd()
	cmd.SetArgs([]string{"--force"})
	err := cmd.Execute()
	require.NoError(s.T(), err)

	data, err := os.ReadFile(configPath)
	require.NoError(s.T(), err)
	require.Contains(s.T(), string(data), "discord_token")
}

func (s *MainSuite) TestOnboardGlobalBashrcWriteError() {
	tmpDir := s.T().TempDir()
	userHomeDir = func() (string, error) {
		return tmpDir, nil
	}
	osStat = os.Stat
	osMkdirAll = os.MkdirAll
	calls := 0
	osWriteFile = func(path string, data []byte, perm os.FileMode) error {
		calls++
		if calls == 2 { // Second write is .bashrc (after config)
			return errors.New("bashrc write error")
		}
		return os.WriteFile(path, data, perm)
	}

	err := onboardGlobal(false)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "writing .bashrc")
}

func (s *MainSuite) TestOnboardGlobalBashrcSkipsIfExists() {
	tmpDir := s.T().TempDir()
	loopDir := filepath.Join(tmpDir, ".loop")
	require.NoError(s.T(), os.MkdirAll(loopDir, 0755))
	bashrcPath := filepath.Join(loopDir, ".bashrc")
	require.NoError(s.T(), os.WriteFile(bashrcPath, []byte("existing aliases"), 0644))

	userHomeDir = func() (string, error) {
		return tmpDir, nil
	}
	osStat = os.Stat
	osMkdirAll = os.MkdirAll
	osWriteFile = os.WriteFile

	err := onboardGlobal(true) // force overwrites config but not .bashrc
	require.NoError(s.T(), err)

	data, err := os.ReadFile(bashrcPath)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "existing aliases", string(data))
}

func (s *MainSuite) TestOnboardGlobalContainerDirError() {
	tmpDir := s.T().TempDir()
	userHomeDir = func() (string, error) {
		return tmpDir, nil
	}
	osStat = os.Stat
	calls := 0
	osMkdirAll = func(path string, perm os.FileMode) error {
		calls++
		if calls == 2 {
			return errors.New("container mkdir error")
		}
		return os.MkdirAll(path, perm)
	}
	osWriteFile = os.WriteFile

	err := onboardGlobal(false)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "creating container directory")
}

func (s *MainSuite) TestOnboardGlobalContainerDockerfileWriteError() {
	tmpDir := s.T().TempDir()
	userHomeDir = func() (string, error) {
		return tmpDir, nil
	}
	osStat = os.Stat
	osMkdirAll = os.MkdirAll
	calls := 0
	osWriteFile = func(path string, data []byte, perm os.FileMode) error {
		calls++
		if calls == 3 { // Third write is Dockerfile (after config and .bashrc)
			return errors.New("dockerfile write error")
		}
		return os.WriteFile(path, data, perm)
	}

	err := onboardGlobal(false)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "writing container Dockerfile")
}

func (s *MainSuite) TestOnboardGlobalContainerEntrypointWriteError() {
	tmpDir := s.T().TempDir()
	userHomeDir = func() (string, error) {
		return tmpDir, nil
	}
	osStat = os.Stat
	osMkdirAll = os.MkdirAll
	calls := 0
	osWriteFile = func(path string, data []byte, perm os.FileMode) error {
		calls++
		if calls == 4 { // Fourth write is entrypoint.sh (after config, .bashrc, Dockerfile)
			return errors.New("entrypoint write error")
		}
		return os.WriteFile(path, data, perm)
	}

	err := onboardGlobal(false)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "writing container entrypoint")
}

func (s *MainSuite) TestOnboardGlobalContainerSetupWriteError() {
	tmpDir := s.T().TempDir()
	userHomeDir = func() (string, error) {
		return tmpDir, nil
	}
	osStat = os.Stat
	osMkdirAll = os.MkdirAll
	calls := 0
	osWriteFile = func(path string, data []byte, perm os.FileMode) error {
		calls++
		if calls == 5 { // Fifth write is setup.sh (after config, .bashrc, Dockerfile, entrypoint)
			return errors.New("setup write error")
		}
		return os.WriteFile(path, data, perm)
	}

	err := onboardGlobal(false)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "writing container setup script")
}

func (s *MainSuite) TestOnboardGlobalSetupSkipsIfExists() {
	tmpDir := s.T().TempDir()
	containerDir := filepath.Join(tmpDir, ".loop", "container")
	require.NoError(s.T(), os.MkdirAll(containerDir, 0755))
	setupPath := filepath.Join(containerDir, "setup.sh")
	require.NoError(s.T(), os.WriteFile(setupPath, []byte("existing setup"), 0644))

	userHomeDir = func() (string, error) {
		return tmpDir, nil
	}
	osStat = os.Stat
	osMkdirAll = os.MkdirAll
	osWriteFile = os.WriteFile

	err := onboardGlobal(true) // force overwrites config but not setup.sh
	require.NoError(s.T(), err)

	data, err := os.ReadFile(setupPath)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "existing setup", string(data))
}

func (s *MainSuite) TestOnboardGlobalTemplatesDirError() {
	tmpDir := s.T().TempDir()
	userHomeDir = func() (string, error) {
		return tmpDir, nil
	}
	osStat = os.Stat
	calls := 0
	osMkdirAll = func(path string, perm os.FileMode) error {
		calls++
		if calls == 3 { // Third mkdir is templates dir (after loop dir and container dir)
			return errors.New("templates mkdir error")
		}
		return os.MkdirAll(path, perm)
	}
	osWriteFile = os.WriteFile

	err := onboardGlobal(false)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "creating templates directory")
}

// --- onboard:local ---

func (s *MainSuite) TestOnboardLocalSuccess() {
	tmpDir := s.T().TempDir()
	osGetwd = func() (string, error) { return tmpDir, nil }
	osReadFile = os.ReadFile
	osWriteFile = os.WriteFile
	osStat = os.Stat
	osMkdirAll = os.MkdirAll
	ensureChannelFunc = func(_, _ string) (string, error) { return "ch-test", nil }

	err := onboardLocal("http://localhost:8222")
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
	require.Equal(s.T(), "--log", args[5])
	require.Equal(s.T(), filepath.Join(tmpDir, ".loop", "mcp.log"), args[6])
}

func (s *MainSuite) TestOnboardLocalMergesExisting() {
	tmpDir := s.T().TempDir()
	existing := `{"mcpServers":{"other":{"command":"other-cmd"}}}`
	require.NoError(s.T(), os.WriteFile(filepath.Join(tmpDir, ".mcp.json"), []byte(existing), 0644))

	osGetwd = func() (string, error) { return tmpDir, nil }
	osReadFile = os.ReadFile
	osWriteFile = os.WriteFile
	ensureChannelFunc = func(_, _ string) (string, error) { return "ch-test", nil }

	err := onboardLocal("http://localhost:8222")
	require.NoError(s.T(), err)

	data, err := os.ReadFile(filepath.Join(tmpDir, ".mcp.json"))
	require.NoError(s.T(), err)

	var result map[string]any
	require.NoError(s.T(), json.Unmarshal(data, &result))

	servers := result["mcpServers"].(map[string]any)
	require.Contains(s.T(), servers, "other", "existing server should be preserved")
	require.Contains(s.T(), servers, "loop", "loop server should be added")
}

func (s *MainSuite) TestOnboardLocalAlreadyRegistered() {
	tmpDir := s.T().TempDir()
	existing := `{"mcpServers":{"loop":{"command":"loop","args":["mcp"]}}}`
	require.NoError(s.T(), os.WriteFile(filepath.Join(tmpDir, ".mcp.json"), []byte(existing), 0644))

	osGetwd = func() (string, error) { return tmpDir, nil }
	osReadFile = os.ReadFile
	osWriteFile = os.WriteFile
	ensureChannelFunc = func(_, _ string) (string, error) { return "ch-test", nil }

	err := onboardLocal("http://localhost:8222")
	require.NoError(s.T(), err)

	// Verify file was NOT modified (still has original content)
	data, err := os.ReadFile(filepath.Join(tmpDir, ".mcp.json"))
	require.NoError(s.T(), err)
	require.JSONEq(s.T(), existing, string(data))
}

func (s *MainSuite) TestOnboardLocalInvalidExistingJSON() {
	tmpDir := s.T().TempDir()
	require.NoError(s.T(), os.WriteFile(filepath.Join(tmpDir, ".mcp.json"), []byte("not json"), 0644))

	osGetwd = func() (string, error) { return tmpDir, nil }
	osReadFile = os.ReadFile

	err := onboardLocal("http://localhost:8222")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "parsing existing .mcp.json")
}

func (s *MainSuite) TestOnboardLocalGetwdError() {
	osGetwd = func() (string, error) { return "", errors.New("getwd error") }

	err := onboardLocal("http://localhost:8222")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "getting working directory")
}

func (s *MainSuite) TestOnboardLocalWriteError() {
	tmpDir := s.T().TempDir()
	osGetwd = func() (string, error) { return tmpDir, nil }
	osReadFile = os.ReadFile
	osWriteFile = func(_ string, _ []byte, _ os.FileMode) error {
		return errors.New("write error")
	}

	err := onboardLocal("http://localhost:8222")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "writing .mcp.json")
}

func (s *MainSuite) TestOnboardLocalCmdRunE() {
	tmpDir := s.T().TempDir()
	osGetwd = func() (string, error) { return tmpDir, nil }
	osReadFile = os.ReadFile
	osWriteFile = os.WriteFile
	ensureChannelFunc = func(_, _ string) (string, error) { return "ch-test", nil }

	cmd := newOnboardLocalCmd()
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
	require.Equal(s.T(), "--log", args[5])
	require.Equal(s.T(), filepath.Join(tmpDir, ".loop", "mcp.log"), args[6])
}

func (s *MainSuite) TestOnboardLocalEnsuresChannel() {
	tmpDir := s.T().TempDir()
	osGetwd = func() (string, error) { return tmpDir, nil }
	osReadFile = os.ReadFile
	osWriteFile = os.WriteFile

	var calledAPIURL, calledDir string
	ensureChannelFunc = func(apiURL, dir string) (string, error) {
		calledAPIURL = apiURL
		calledDir = dir
		return "ch-123", nil
	}

	err := onboardLocal("http://localhost:8222")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "http://localhost:8222", calledAPIURL)
	require.Equal(s.T(), tmpDir, calledDir)
}

func (s *MainSuite) TestOnboardLocalEnsureChannelFailsGracefully() {
	tmpDir := s.T().TempDir()
	osGetwd = func() (string, error) { return tmpDir, nil }
	osReadFile = os.ReadFile
	osWriteFile = os.WriteFile
	ensureChannelFunc = func(_, _ string) (string, error) {
		return "", errors.New("server not running")
	}

	err := onboardLocal("http://localhost:8222")
	require.NoError(s.T(), err, "onboardLocal should succeed even when ensureChannel fails")
}

func (s *MainSuite) TestOnboardLocalAlreadyRegisteredStillEnsuresChannel() {
	tmpDir := s.T().TempDir()
	existing := `{"mcpServers":{"loop":{"command":"loop","args":["mcp","--dir","` + tmpDir + `","--api-url","http://localhost:8222"]}}}`
	require.NoError(s.T(), os.WriteFile(filepath.Join(tmpDir, ".mcp.json"), []byte(existing), 0644))

	osGetwd = func() (string, error) { return tmpDir, nil }
	osReadFile = os.ReadFile
	osWriteFile = os.WriteFile

	called := false
	ensureChannelFunc = func(_, _ string) (string, error) {
		called = true
		return "ch-456", nil
	}

	err := onboardLocal("http://localhost:8222")
	require.NoError(s.T(), err)
	require.True(s.T(), called, "ensureChannelFunc should be called even when loop is already registered")
}

func (s *MainSuite) TestOnboardLocalProjectConfigWritten() {
	tmpDir := s.T().TempDir()
	osGetwd = func() (string, error) { return tmpDir, nil }
	osReadFile = os.ReadFile
	osWriteFile = os.WriteFile
	osStat = os.Stat
	osMkdirAll = os.MkdirAll
	ensureChannelFunc = func(_, _ string) (string, error) { return "ch-test", nil }

	err := onboardLocal("http://localhost:8222")
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

	osGetwd = func() (string, error) { return tmpDir, nil }
	osReadFile = os.ReadFile
	osWriteFile = os.WriteFile
	osStat = os.Stat
	osMkdirAll = os.MkdirAll
	ensureChannelFunc = func(_, _ string) (string, error) { return "ch-test", nil }

	err := onboardLocal("http://localhost:8222")
	require.NoError(s.T(), err)

	// Verify existing config was NOT overwritten
	data, err := os.ReadFile(filepath.Join(loopDir, "config.json"))
	require.NoError(s.T(), err)
	require.Equal(s.T(), `{"claude_model":"custom"}`, string(data))
}

func (s *MainSuite) TestOnboardLocalProjectConfigMkdirError() {
	tmpDir := s.T().TempDir()
	osGetwd = func() (string, error) { return tmpDir, nil }
	osReadFile = os.ReadFile
	osStat = os.Stat

	writeCount := 0
	osWriteFile = func(path string, data []byte, perm os.FileMode) error {
		writeCount++
		return os.WriteFile(path, data, perm)
	}
	osMkdirAll = func(_ string, _ os.FileMode) error { return errors.New("mkdir error") }
	ensureChannelFunc = func(_, _ string) (string, error) { return "ch-test", nil }

	err := onboardLocal("http://localhost:8222")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "creating .loop directory")
}

func (s *MainSuite) TestOnboardLocalProjectConfigWriteError() {
	tmpDir := s.T().TempDir()
	osGetwd = func() (string, error) { return tmpDir, nil }
	osReadFile = os.ReadFile
	osStat = os.Stat
	osMkdirAll = os.MkdirAll

	writeCount := 0
	osWriteFile = func(path string, data []byte, perm os.FileMode) error {
		writeCount++
		if writeCount == 2 {
			return errors.New("write config error")
		}
		return os.WriteFile(path, data, perm)
	}
	ensureChannelFunc = func(_, _ string) (string, error) { return "ch-test", nil }

	err := onboardLocal("http://localhost:8222")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "writing project config")
}

func (s *MainSuite) TestOnboardLocalTemplatesDirError() {
	tmpDir := s.T().TempDir()
	osGetwd = func() (string, error) { return tmpDir, nil }
	osReadFile = os.ReadFile
	osWriteFile = os.WriteFile
	osStat = os.Stat
	mkdirCalls := 0
	osMkdirAll = func(path string, perm os.FileMode) error {
		mkdirCalls++
		if mkdirCalls == 2 { // Second mkdir is templates dir (after .loop dir)
			return errors.New("templates mkdir error")
		}
		return os.MkdirAll(path, perm)
	}
	ensureChannelFunc = func(_, _ string) (string, error) { return "ch-test", nil }

	err := onboardLocal("http://localhost:8222")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "creating templates directory")
}

func (s *MainSuite) TestOnboardLocalTemplatesDirCreated() {
	tmpDir := s.T().TempDir()
	osGetwd = func() (string, error) { return tmpDir, nil }
	osReadFile = os.ReadFile
	osWriteFile = os.WriteFile
	osStat = os.Stat
	osMkdirAll = os.MkdirAll
	ensureChannelFunc = func(_, _ string) (string, error) { return "ch-test", nil }

	err := onboardLocal("http://localhost:8222")
	require.NoError(s.T(), err)

	// Verify templates directory was created
	templatesDir := filepath.Join(tmpDir, ".loop", "templates")
	info, err := os.Stat(templatesDir)
	require.NoError(s.T(), err)
	require.True(s.T(), info.IsDir())
}

// --- ensureImage tests ---

func (s *MainSuite) TestEnsureImageSkipsWhenExists() {
	dockerClient := new(mockDockerClient)
	dockerClient.On("ImageList", mock.Anything, "loop-agent:latest").Return([]string{"sha256:abc"}, nil)

	cfg := &config.Config{
		LoopDir:        s.T().TempDir(),
		ContainerImage: "loop-agent:latest",
	}
	// Create container dir with Dockerfile so it doesn't try to write
	containerDir := filepath.Join(cfg.LoopDir, "container")
	require.NoError(s.T(), os.MkdirAll(containerDir, 0755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(containerDir, "Dockerfile"), []byte("FROM alpine"), 0644))

	osStat = os.Stat
	err := s.origEnsureImage(context.Background(), dockerClient, cfg)
	require.NoError(s.T(), err)
	dockerClient.AssertExpectations(s.T())
}

func (s *MainSuite) TestEnsureImageBuildsWhenMissing() {
	dockerClient := new(mockDockerClient)
	dockerClient.On("ImageList", mock.Anything, "loop-agent:latest").Return([]string{}, nil)
	dockerClient.On("ImageBuild", mock.Anything, mock.Anything, "loop-agent:latest").Return(nil)

	cfg := &config.Config{
		LoopDir:        s.T().TempDir(),
		ContainerImage: "loop-agent:latest",
	}
	// Create container dir with Dockerfile
	containerDir := filepath.Join(cfg.LoopDir, "container")
	require.NoError(s.T(), os.MkdirAll(containerDir, 0755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(containerDir, "Dockerfile"), []byte("FROM alpine"), 0644))

	osStat = os.Stat
	err := s.origEnsureImage(context.Background(), dockerClient, cfg)
	require.NoError(s.T(), err)
	dockerClient.AssertExpectations(s.T())
}

func (s *MainSuite) TestEnsureImageWritesEmbeddedFiles() {
	dockerClient := new(mockDockerClient)
	dockerClient.On("ImageList", mock.Anything, "loop-agent:latest").Return([]string{"sha256:abc"}, nil)

	cfg := &config.Config{
		LoopDir:        s.T().TempDir(),
		ContainerImage: "loop-agent:latest",
	}

	osStat = os.Stat
	osMkdirAll = os.MkdirAll
	osWriteFile = os.WriteFile

	err := s.origEnsureImage(context.Background(), dockerClient, cfg)
	require.NoError(s.T(), err)

	// Verify embedded files were written
	containerDir := filepath.Join(cfg.LoopDir, "container")
	dockerfileData, err := os.ReadFile(filepath.Join(containerDir, "Dockerfile"))
	require.NoError(s.T(), err)
	require.Contains(s.T(), string(dockerfileData), "FROM golang:")

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
	}
	containerDir := filepath.Join(cfg.LoopDir, "container")
	require.NoError(s.T(), os.MkdirAll(containerDir, 0755))
	require.NoError(s.T(), os.WriteFile(filepath.Join(containerDir, "Dockerfile"), []byte("FROM alpine"), 0644))

	osStat = os.Stat
	err := s.origEnsureImage(context.Background(), dockerClient, cfg)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "listing images")
	dockerClient.AssertExpectations(s.T())
}

func (s *MainSuite) TestEnsureImageMkdirError() {
	dockerClient := new(mockDockerClient)

	cfg := &config.Config{
		LoopDir:        s.T().TempDir(),
		ContainerImage: "loop-agent:latest",
	}

	osStat = os.Stat
	osMkdirAll = func(_ string, _ os.FileMode) error {
		return errors.New("mkdir error")
	}

	err := s.origEnsureImage(context.Background(), dockerClient, cfg)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "creating container directory")
}

func (s *MainSuite) TestEnsureImageDockerfileWriteError() {
	dockerClient := new(mockDockerClient)

	cfg := &config.Config{
		LoopDir:        s.T().TempDir(),
		ContainerImage: "loop-agent:latest",
	}

	osStat = os.Stat
	osMkdirAll = os.MkdirAll
	osWriteFile = func(_ string, _ []byte, _ os.FileMode) error {
		return errors.New("write error")
	}

	err := s.origEnsureImage(context.Background(), dockerClient, cfg)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "writing Dockerfile")
}

func (s *MainSuite) TestEnsureImageEntrypointWriteError() {
	dockerClient := new(mockDockerClient)

	cfg := &config.Config{
		LoopDir:        s.T().TempDir(),
		ContainerImage: "loop-agent:latest",
	}

	osStat = os.Stat
	osMkdirAll = os.MkdirAll
	calls := 0
	osWriteFile = func(path string, data []byte, perm os.FileMode) error {
		calls++
		if calls == 2 { // Second write is entrypoint.sh
			return errors.New("entrypoint write error")
		}
		return os.WriteFile(path, data, perm)
	}

	err := s.origEnsureImage(context.Background(), dockerClient, cfg)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "writing entrypoint")
}

func (s *MainSuite) TestEnsureImageSetupWriteError() {
	dockerClient := new(mockDockerClient)

	cfg := &config.Config{
		LoopDir:        s.T().TempDir(),
		ContainerImage: "loop-agent:latest",
	}

	osStat = os.Stat
	osMkdirAll = os.MkdirAll
	calls := 0
	osWriteFile = func(path string, data []byte, perm os.FileMode) error {
		calls++
		if calls == 3 { // Third write is setup.sh
			return errors.New("setup write error")
		}
		return os.WriteFile(path, data, perm)
	}

	err := s.origEnsureImage(context.Background(), dockerClient, cfg)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "writing setup script")
}

// --- version ---

func (s *MainSuite) TestNewVersionCmd() {
	cmd := newVersionCmd()
	require.Equal(s.T(), "version", cmd.Use)
	require.Equal(s.T(), []string{"v"}, cmd.Aliases)
	require.NotNil(s.T(), cmd.Run)
}

func (s *MainSuite) TestVersionOutput() {
	origVersion, origCommit, origDate := version, commit, date
	defer func() { version, commit, date = origVersion, origCommit, origDate }()

	version = "1.2.3"
	commit = "abc1234"
	date = "2026-01-01T00:00:00Z"

	cmd := newVersionCmd()
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	require.NoError(s.T(), err)
}

func (s *MainSuite) TestVersionOutputDefaults() {
	origVersion, origCommit, origDate := version, commit, date
	defer func() { version, commit, date = origVersion, origCommit, origDate }()

	version = "dev"
	commit = "none"
	date = "unknown"

	cmd := newVersionCmd()
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	require.NoError(s.T(), err)
}
