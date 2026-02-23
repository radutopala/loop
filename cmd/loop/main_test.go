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
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/api"
	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/container"
	"github.com/radutopala/loop/internal/daemon"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/embeddings"
	"github.com/radutopala/loop/internal/mcpserver"
	"github.com/radutopala/loop/internal/memory"
	"github.com/radutopala/loop/internal/orchestrator"
	"github.com/radutopala/loop/internal/scheduler"
	"github.com/radutopala/loop/internal/types"
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

func (m *mockStore) GetChannelByDirPath(ctx context.Context, dirPath string, platform types.Platform) (*db.Channel, error) {
	args := m.Called(ctx, dirPath, platform)
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

func (m *mockStore) DeleteChannel(ctx context.Context, channelID string) error {
	return m.Called(ctx, channelID).Error(0)
}

func (m *mockStore) DeleteChannelsByParentID(ctx context.Context, parentID string) error {
	return m.Called(ctx, parentID).Error(0)
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

func (m *mockStore) ListChannels(ctx context.Context) ([]*db.Channel, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*db.Channel), args.Error(1)
}

func (m *mockStore) UpsertMemoryFile(ctx context.Context, file *db.MemoryFile) error {
	return m.Called(ctx, file).Error(0)
}

func (m *mockStore) GetMemoryFilesByDirPath(ctx context.Context, dirPath string) ([]*db.MemoryFile, error) {
	args := m.Called(ctx, dirPath)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*db.MemoryFile), args.Error(1)
}

func (m *mockStore) GetMemoryFileHash(ctx context.Context, filePath, dirPath string) (string, error) {
	args := m.Called(ctx, filePath, dirPath)
	return args.String(0), args.Error(1)
}

func (m *mockStore) DeleteMemoryFile(ctx context.Context, filePath, dirPath string) error {
	return m.Called(ctx, filePath, dirPath).Error(0)
}

func (m *mockStore) UpdateChannelPermissions(ctx context.Context, channelID string, perms db.ChannelPermissions) error {
	return m.Called(ctx, channelID, perms).Error(0)
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

func (m *mockBot) OnChannelDelete(handler func(ctx context.Context, channelID string, isThread bool)) {
	m.Called(handler)
}

func (m *mockBot) OnChannelJoin(handler func(ctx context.Context, channelID string)) {
	m.Called(handler)
}

func (m *mockBot) BotUserID() string {
	return m.Called().String(0)
}

func (m *mockBot) CreateChannel(ctx context.Context, guildID, name string) (string, error) {
	args := m.Called(ctx, guildID, name)
	return args.String(0), args.Error(1)
}

func (m *mockBot) InviteUserToChannel(ctx context.Context, channelID, userID string) error {
	return m.Called(ctx, channelID, userID).Error(0)
}

func (m *mockBot) GetOwnerUserID(ctx context.Context) (string, error) {
	args := m.Called(ctx)
	return args.String(0), args.Error(1)
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

func (m *mockBot) DeleteThread(ctx context.Context, threadID string) error {
	return m.Called(ctx, threadID).Error(0)
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

func (m *mockBot) GetMemberRoles(ctx context.Context, guildID, userID string) ([]string, error) {
	args := m.Called(ctx, guildID, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]string), args.Error(1)
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

type mockAPIServer struct {
	mock.Mock
}

func (m *mockAPIServer) Start(addr string) error {
	return m.Called(addr).Error(0)
}

func (m *mockAPIServer) Stop(ctx context.Context) error {
	return m.Called(ctx).Error(0)
}

func (m *mockAPIServer) SetMemoryIndexer(idx api.MemoryIndexer) {
	m.Called(idx)
}

func (m *mockAPIServer) SetLoopDir(dir string) {
	m.Called(dir)
}

// --- Test Suite ---

type MainSuite struct {
	suite.Suite
	origConfigLoad             func() (*config.Config, error)
	origNewDiscordBot          func(string, string, *slog.Logger) (orchestrator.Bot, error)
	origNewSlackBot            func(string, string, *slog.Logger) (orchestrator.Bot, error)
	origNewDockerClient        func() (container.DockerClient, error)
	origNewSQLiteStore         func(string) (db.Store, error)
	origOsExit                 func(int)
	origNewAPIServer           func(scheduler.Scheduler, api.ChannelEnsurer, api.ThreadEnsurer, api.ChannelLister, api.MessageSender, *slog.Logger) apiServer
	origNewMCPServer           func(string, string, string, mcpserver.HTTPClient, *slog.Logger, ...mcpserver.MemoryOption) *mcpserver.Server
	origDaemonStart            func(daemon.System, string) error
	origDaemonStop             func(daemon.System) error
	origDaemonStatus           func(daemon.System) (string, error)
	origNewSystem              func() daemon.System
	origEnsureChannelFunc      func(string, string) (string, error)
	origEnsureImage            func(context.Context, container.DockerClient, *config.Config) error
	origUserHomeDir            func() (string, error)
	origOsStat                 func(string) (os.FileInfo, error)
	origOsMkdirAll             func(string, os.FileMode) error
	origOsWriteFile            func(string, []byte, os.FileMode) error
	origOsGetwd                func() (string, error)
	origOsReadFile             func(string) ([]byte, error)
	origNewEmbedder            func(*config.Config) (embeddings.Embedder, error)
	origLoadProjectMemoryPaths func(string) []string
}

func TestMainSuite(t *testing.T) {
	suite.Run(t, new(MainSuite))
}

func (s *MainSuite) SetupTest() {
	s.origConfigLoad = configLoad
	s.origNewDiscordBot = newDiscordBot
	s.origNewSlackBot = newSlackBot
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
	s.origNewEmbedder = newEmbedder
	s.origLoadProjectMemoryPaths = loadProjectMemoryPaths
	loadProjectMemoryPaths = func(_ string) []string { return nil }
}

func (s *MainSuite) TearDownTest() {
	configLoad = s.origConfigLoad
	newDiscordBot = s.origNewDiscordBot
	newSlackBot = s.origNewSlackBot
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
	newEmbedder = s.origNewEmbedder
	loadProjectMemoryPaths = s.origLoadProjectMemoryPaths
}

func testConfig() *config.Config {
	return &config.Config{
		PlatformType: types.PlatformDiscord,
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
		PlatformType:  types.PlatformSlack,
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
func fakeAPIServer() func(scheduler.Scheduler, api.ChannelEnsurer, api.ThreadEnsurer, api.ChannelLister, api.MessageSender, *slog.Logger) apiServer {
	return func(sched scheduler.Scheduler, channels api.ChannelEnsurer, threads api.ThreadEnsurer, store api.ChannelLister, messages api.MessageSender, logger *slog.Logger) apiServer {
		return api.NewServer(sched, channels, threads, store, messages, logger)
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
		"readme":         false,
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
	newMCPServer = func(channelID, apiURL, authorID string, httpClient mcpserver.HTTPClient, logger *slog.Logger, opts ...mcpserver.MemoryOption) *mcpserver.Server {
		require.Equal(s.T(), "ch1", channelID)
		require.Equal(s.T(), "http://localhost:8222", apiURL)
		called = true
		return mcpserver.New(channelID, apiURL, authorID, httpClient, logger)
	}

	// runMCP will try to use StdioTransport which will fail/close immediately in test.
	// We just verify the function is wired correctly.
	_ = runMCP("ch1", "http://localhost:8222", "", logPath, "", false)
	require.True(s.T(), called)
}

func (s *MainSuite) TestRunMCPLogOpenError() {
	err := runMCP("ch1", "http://localhost:8222", "", "/nonexistent/dir/mcp.log", "", false)
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
	newMCPServer = func(channelID, apiURL, authorID string, httpClient mcpserver.HTTPClient, logger *slog.Logger, opts ...mcpserver.MemoryOption) *mcpserver.Server {
		called = true
		// Verify logger was created (we can't easily inspect its level, but at least it was called)
		require.NotNil(s.T(), logger)
		// Return a real server that we won't run
		return mcpserver.New(channelID, apiURL, authorID, httpClient, logger)
	}

	// This will fail to run the server (no stdio), but that's OK - we just want to test config loading
	_ = runMCP("ch1", "http://localhost:8222", "", logPath, "", false)
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
	require.Len(s.T(), res.Tools, 11)
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
	newMCPServer = func(channelID, apiURL, authorID string, httpClient mcpserver.HTTPClient, logger *slog.Logger, opts ...mcpserver.MemoryOption) *mcpserver.Server {
		require.Equal(s.T(), "resolved-ch", channelID)
		called = true
		return mcpserver.New(channelID, apiURL, authorID, httpClient, logger)
	}
	ensureChannelFunc = func(apiURL, dirPath string) (string, error) {
		require.Equal(s.T(), "http://localhost:8222", apiURL)
		require.Equal(s.T(), "/home/user/dev/loop", dirPath)
		return "resolved-ch", nil
	}

	_ = runMCP("", "http://localhost:8222", "/home/user/dev/loop", logPath, "", false)
	require.True(s.T(), called)
}

func (s *MainSuite) TestRunMCPWithDirEnsureError() {
	ensureChannelFunc = func(_, _ string) (string, error) {
		return "", errors.New("ensure failed")
	}

	err := runMCP("", "http://localhost:8222", "/path", "/tmp/mcp.log", "", false)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "ensuring channel for dir")
}

func (s *MainSuite) TestNewMCPCmdWithDirFlag() {
	logPath := filepath.Join(s.T().TempDir(), "mcp.log")
	called := false
	newMCPServer = func(channelID, apiURL, authorID string, httpClient mcpserver.HTTPClient, logger *slog.Logger, opts ...mcpserver.MemoryOption) *mcpserver.Server {
		require.Equal(s.T(), "resolved-ch", channelID)
		called = true
		return mcpserver.New(channelID, apiURL, authorID, httpClient, logger)
	}
	ensureChannelFunc = func(_, _ string) (string, error) {
		return "resolved-ch", nil
	}

	cmd := newMCPCmd()
	cmd.SetArgs([]string{"--dir", "/home/user/dev/loop", "--api-url", "http://test:8222", "--log", logPath})
	_ = cmd.Execute()
	require.True(s.T(), called)
}

// --- memoryDir ---

func (s *MainSuite) TestMemoryDir() {
	userHomeDir = func() (string, error) {
		return "/home/testuser", nil
	}
	dir, err := memoryDir("/Users/dev/loop")
	require.NoError(s.T(), err)
	require.Equal(s.T(), "/home/testuser/.claude/projects/-Users-dev-loop/memory", dir)
}

func (s *MainSuite) TestMemoryDirHomeDirError() {
	userHomeDir = func() (string, error) {
		return "", errors.New("no home")
	}
	_, err := memoryDir("/path")
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
	userHomeDir = func() (string, error) {
		return "/home/test", nil
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := new(mockStore)
	indexer := memory.NewIndexer(&fakeEmbedder{}, store, logger, 0)
	mdi := &multiDirIndexer{indexer: indexer, logger: logger, globalMemoryPaths: []string{"./memory"}}

	entries, excludePaths := mdi.resolveMemoryPaths("/home/user/project")
	require.Len(s.T(), entries, 2)
	require.Empty(s.T(), excludePaths)
	require.Contains(s.T(), entries[0].path, ".claude/projects")
	require.False(s.T(), entries[0].global)
	require.Equal(s.T(), "/home/user/project/memory", entries[1].path)
	require.False(s.T(), entries[1].global) // relative config path
}

func (s *MainSuite) TestMultiDirIndexerResolveMemoryPathsHomeDirError() {
	userHomeDir = func() (string, error) {
		return "", errors.New("no home")
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := new(mockStore)
	indexer := memory.NewIndexer(&fakeEmbedder{}, store, logger, 0)
	mdi := &multiDirIndexer{indexer: indexer, logger: logger, globalMemoryPaths: []string{"./memory"}}

	entries, excludePaths := mdi.resolveMemoryPaths("/path")
	require.Len(s.T(), entries, 1)
	require.Empty(s.T(), excludePaths)
	require.Equal(s.T(), "/path/memory", entries[0].path)
	require.False(s.T(), entries[0].global)
}

func (s *MainSuite) TestMultiDirIndexerResolveMemoryPathsWithGlobalAndProject() {
	userHomeDir = func() (string, error) {
		return "/home/test", nil
	}
	loadProjectMemoryPaths = func(_ string) []string { return []string{"./docs/arch.md"} }

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := new(mockStore)
	indexer := memory.NewIndexer(&fakeEmbedder{}, store, logger, 0)
	mdi := &multiDirIndexer{
		indexer:           indexer,
		logger:            logger,
		globalMemoryPaths: []string{"/shared/knowledge"},
	}

	entries, excludePaths := mdi.resolveMemoryPaths("/home/user/project")
	require.Len(s.T(), entries, 3)
	require.Empty(s.T(), excludePaths)
	require.Contains(s.T(), entries[0].path, ".claude/projects")
	require.False(s.T(), entries[0].global)
	require.Equal(s.T(), "/shared/knowledge", entries[1].path)
	require.True(s.T(), entries[1].global) // absolute config path
	require.Equal(s.T(), "/home/user/project/docs/arch.md", entries[2].path)
	require.False(s.T(), entries[2].global) // relative project path
}

func (s *MainSuite) TestMultiDirIndexerResolveMemoryPathsDedup() {
	userHomeDir = func() (string, error) {
		return "/home/test", nil
	}
	// Project config returns paths that duplicate a global path.
	loadProjectMemoryPaths = func(_ string) []string {
		return []string{"./memory", "/shared/knowledge"}
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := new(mockStore)
	indexer := memory.NewIndexer(&fakeEmbedder{}, store, logger, 0)
	mdi := &multiDirIndexer{
		indexer:           indexer,
		logger:            logger,
		globalMemoryPaths: []string{"./memory", "/shared/knowledge"},
	}

	entries, excludePaths := mdi.resolveMemoryPaths("/home/user/project")
	// Should be deduplicated: auto-memory, project/memory, /shared/knowledge — no duplicates.
	require.Len(s.T(), entries, 3)
	require.Empty(s.T(), excludePaths)
	require.Contains(s.T(), entries[0].path, ".claude/projects")
	require.Equal(s.T(), "/home/user/project/memory", entries[1].path)
	require.Equal(s.T(), "/shared/knowledge", entries[2].path)
	require.True(s.T(), entries[2].global)
}

func (s *MainSuite) TestResolveMemoryPathsWithExclusions() {
	userHomeDir = func() (string, error) {
		return "/home/test", nil
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := new(mockStore)
	indexer := memory.NewIndexer(&fakeEmbedder{}, store, logger, 0)
	mdi := &multiDirIndexer{
		indexer:           indexer,
		logger:            logger,
		globalMemoryPaths: []string{"./memory", "!./memory/drafts"},
	}

	entries, excludePaths := mdi.resolveMemoryPaths("/home/user/project")
	require.Len(s.T(), entries, 2) // auto-memory + ./memory
	require.Len(s.T(), excludePaths, 1)
	require.Equal(s.T(), "/home/user/project/memory/drafts", excludePaths[0])
}

func (s *MainSuite) TestResolveMemoryPathsAbsoluteExclusion() {
	userHomeDir = func() (string, error) {
		return "/home/test", nil
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := new(mockStore)
	indexer := memory.NewIndexer(&fakeEmbedder{}, store, logger, 0)
	mdi := &multiDirIndexer{
		indexer:           indexer,
		logger:            logger,
		globalMemoryPaths: []string{"./memory", "!/shared/secret"},
	}

	entries, excludePaths := mdi.resolveMemoryPaths("/home/user/project")
	require.Len(s.T(), entries, 2) // auto-memory + ./memory
	require.Len(s.T(), excludePaths, 1)
	require.Equal(s.T(), "/shared/secret", excludePaths[0])
}

func (s *MainSuite) TestResolveMemoryPathsProjectExclusion() {
	userHomeDir = func() (string, error) {
		return "/home/test", nil
	}
	loadProjectMemoryPaths = func(_ string) []string {
		return []string{"./docs", "!./docs/wip"}
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := new(mockStore)
	indexer := memory.NewIndexer(&fakeEmbedder{}, store, logger, 0)
	mdi := &multiDirIndexer{indexer: indexer, logger: logger}

	entries, excludePaths := mdi.resolveMemoryPaths("/home/user/project")
	require.Len(s.T(), entries, 2) // auto-memory + ./docs
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

	paths := defaultLoadProjectMemoryPaths(tmpDir)
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

	paths := defaultLoadProjectMemoryPaths(tmpDir)
	require.Equal(s.T(), []string{"/docs"}, paths)
}

func (s *MainSuite) TestLoadProjectMemoryPathsMissingFile() {
	paths := defaultLoadProjectMemoryPaths("/nonexistent")
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

	paths := defaultLoadProjectMemoryPaths(tmpDir)
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

	paths := defaultLoadProjectMemoryPaths(tmpDir)
	require.Nil(s.T(), paths)
}

func (s *MainSuite) TestMultiDirIndexerSearch() {
	userHomeDir = func() (string, error) {
		return s.T().TempDir(), nil
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := new(mockStore)
	store.On("GetMemoryFilesByDirPath", mock.Anything, mock.Anything).Return([]*db.MemoryFile{}, nil)
	indexer := memory.NewIndexer(&fakeEmbedder{}, store, logger, 0)
	mdi := &multiDirIndexer{indexer: indexer, logger: logger}

	ctx := context.Background()
	results, err := mdi.Search(ctx, "/nonexistent/project", "test", 5)
	require.NoError(s.T(), err)
	require.Empty(s.T(), results)
}

func (s *MainSuite) TestMultiDirIndexerSearchWithError() {
	userHomeDir = func() (string, error) {
		return s.T().TempDir(), nil
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := new(mockStore)
	// GetMemoryFilesByDirPath returning an error triggers the error path
	store.On("GetMemoryFilesByDirPath", mock.Anything, mock.Anything).Return(nil, errors.New("db error"))
	indexer := memory.NewIndexer(&fakeEmbedder{}, store, logger, 0)
	mdi := &multiDirIndexer{indexer: indexer, logger: logger}

	ctx := context.Background()
	results, err := mdi.Search(ctx, "/nonexistent/project", "test", 5)
	require.Error(s.T(), err)
	require.Nil(s.T(), results)
}

func (s *MainSuite) TestMultiDirIndexerIndex() {
	userHomeDir = func() (string, error) {
		return s.T().TempDir(), nil
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := new(mockStore)
	indexer := memory.NewIndexer(&fakeEmbedder{}, store, logger, 0)
	mdi := &multiDirIndexer{indexer: indexer, logger: logger, globalMemoryPaths: []string{"./memory"}}

	ctx := context.Background()
	count, err := mdi.Index(ctx, "/nonexistent/project")
	require.NoError(s.T(), err)
	require.Equal(s.T(), 0, count)
}

func (s *MainSuite) TestMultiDirIndexerIndexWithError() {
	userHomeDir = func() (string, error) {
		return "/nonexistent-home", nil
	}

	mi := new(mockMemIndexer)
	mi.On("Index", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(0, errors.New("stat error"))

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mdi := &multiDirIndexer{indexer: mi, logger: logger, globalMemoryPaths: []string{"./memory"}}

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

	userHomeDir = func() (string, error) {
		return "/nonexistent-home", nil
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := new(mockStore)
	store.On("GetMemoryFileHash", mock.Anything, mock.Anything, mock.Anything).Return("", nil)
	store.On("UpsertMemoryFile", mock.Anything, mock.Anything).Return(nil)
	indexer := memory.NewIndexer(&fakeEmbedder{}, store, logger, 0)
	mdi := &multiDirIndexer{indexer: indexer, logger: logger, globalMemoryPaths: []string{"./memory"}}

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

	userHomeDir = func() (string, error) {
		return "/nonexistent-home", nil
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := new(mockStore)
	store.On("GetMemoryFileHash", mock.Anything, mock.Anything, mock.Anything).Return("", nil)
	store.On("UpsertMemoryFile", mock.Anything, mock.Anything).Return(nil)
	emb1 := embeddings.SerializeFloat32([]float32{0.1, 0.2, 0.3})
	emb2 := embeddings.SerializeFloat32([]float32{0.3, 0.2, 0.1})
	store.On("GetMemoryFilesByDirPath", mock.Anything, mock.Anything).Return([]*db.MemoryFile{
		{FilePath: "a.md", Content: "content a", Embedding: emb1, Dimensions: 3},
		{FilePath: "b.md", Content: "content b", Embedding: emb2, Dimensions: 3},
	}, nil)
	indexer := memory.NewIndexer(&fakeEmbedder{}, store, logger, 0)
	mdi := &multiDirIndexer{indexer: indexer, logger: logger, globalMemoryPaths: []string{"./memory"}}

	ctx := context.Background()
	results, err := mdi.Search(ctx, tmpDir, "test query", 1)
	require.NoError(s.T(), err)
	require.Len(s.T(), results, 1) // topK=1 truncates to 1 result
}

func (s *MainSuite) TestMultiDirIndexerSearchWithGlobalPath() {
	userHomeDir = func() (string, error) {
		return "/nonexistent-home", nil
	}

	mi := new(mockMemIndexer)
	// Auto-memory path (project-scoped)
	mi.On("Index", mock.Anything, mock.Anything, "/some/project", mock.Anything).Return(0, nil)
	// Global path (absolute config path, scope = "")
	mi.On("Index", mock.Anything, "/shared/knowledge", "", mock.Anything).Return(0, nil)
	mi.On("Search", mock.Anything, "/some/project", "test", 5).Return([]memory.SearchResult{}, nil)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mdi := &multiDirIndexer{indexer: mi, logger: logger, globalMemoryPaths: []string{"/shared/knowledge"}}

	ctx := context.Background()
	results, err := mdi.Search(ctx, "/some/project", "test", 5)
	require.NoError(s.T(), err)
	require.Empty(s.T(), results)
	mi.AssertExpectations(s.T())
}

func (s *MainSuite) TestMultiDirIndexerSearchWithIndexError() {
	userHomeDir = func() (string, error) {
		return "/nonexistent-home", nil
	}

	mi := new(mockMemIndexer)
	// Auto-memory path fails
	mi.On("Index", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(0, errors.New("index error"))
	mi.On("Search", mock.Anything, "/some/project", "test", 5).Return([]memory.SearchResult{}, nil)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mdi := &multiDirIndexer{indexer: mi, logger: logger}

	ctx := context.Background()
	results, err := mdi.Search(ctx, "/some/project", "test", 5)
	require.NoError(s.T(), err) // Error was logged, not returned
	require.Empty(s.T(), results)
}

func (s *MainSuite) TestMultiDirIndexerIndexWithGlobalPath() {
	userHomeDir = func() (string, error) {
		return "/nonexistent-home", nil
	}

	mi := new(mockMemIndexer)
	// Auto-memory path (project-scoped)
	mi.On("Index", mock.Anything, mock.Anything, "/some/project", mock.Anything).Return(1, nil)
	// Global path (absolute config path, scope = "")
	mi.On("Index", mock.Anything, "/shared/knowledge", "", mock.Anything).Return(2, nil)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mdi := &multiDirIndexer{indexer: mi, logger: logger, globalMemoryPaths: []string{"/shared/knowledge"}}

	ctx := context.Background()
	count, err := mdi.Index(ctx, "/some/project")
	require.NoError(s.T(), err)
	require.Equal(s.T(), 3, count) // 1 + 2
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

	userHomeDir = func() (string, error) {
		return "/nonexistent-home", nil
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := new(mockStore)
	store.On("GetMemoryFileHash", mock.Anything, mock.Anything, mock.Anything).Return("", nil)
	store.On("UpsertMemoryFile", mock.Anything, mock.Anything).Return(nil)
	indexer := memory.NewIndexer(&fakeEmbedder{}, store, logger, 0)
	mdi := &multiDirIndexer{indexer: indexer, logger: logger, globalMemoryPaths: []string{"./memory"}}

	cl := new(mockChannelLister)
	cl.On("ListChannels", mock.Anything).Return([]*db.Channel{
		{ChannelID: "ch1", DirPath: tmpDir},
		{ChannelID: "ch2", DirPath: ""},             // empty dir_path — skipped
		{ChannelID: "ch3", DirPath: "/nonexistent"}, // no files — 0 indexed
	}, nil)

	mdi.reindexAll(context.Background(), cl)
	cl.AssertExpectations(s.T())
	store.AssertCalled(s.T(), "UpsertMemoryFile", mock.Anything, mock.Anything)
}

func (s *MainSuite) TestReindexAllListError() {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := new(mockStore)
	indexer := memory.NewIndexer(&fakeEmbedder{}, store, logger, 0)
	mdi := &multiDirIndexer{indexer: indexer, logger: logger}

	cl := new(mockChannelLister)
	cl.On("ListChannels", mock.Anything).Return(nil, errors.New("db error"))

	mdi.reindexAll(context.Background(), cl)
	cl.AssertExpectations(s.T())
}

func (s *MainSuite) TestReindexAllCancelledContext() {
	userHomeDir = func() (string, error) {
		return "/nonexistent-home", nil
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := new(mockStore)
	indexer := memory.NewIndexer(&fakeEmbedder{}, store, logger, 0)
	mdi := &multiDirIndexer{indexer: indexer, logger: logger}

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
	userHomeDir = func() (string, error) {
		return "/nonexistent-home", nil
	}

	mi := new(mockMemIndexer)
	mi.On("Index", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(0, nil)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mdi := &multiDirIndexer{indexer: mi, logger: logger}

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
	userHomeDir = func() (string, error) {
		return "/nonexistent-home", nil
	}

	mi := new(mockMemIndexer)
	mi.On("Index", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(0, nil)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mdi := &multiDirIndexer{indexer: mi, logger: logger}

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
	embedder, err := newEmbedder(cfg)
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
	embedder, err := newEmbedder(cfg)
	require.NoError(s.T(), err)
	require.NotNil(s.T(), embedder)
}

func (s *MainSuite) TestNewEmbedderUnsupportedProvider() {
	cfg := &config.Config{
		Memory: config.MemoryConfig{Enabled: true, Embeddings: config.EmbeddingsConfig{
			Provider: "unknown",
		}},
	}
	_, err := newEmbedder(cfg)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "unsupported embeddings provider")
}

// --- runMCP with embeddings ---

func (s *MainSuite) TestRunMCPWithMemoryEnabled() {
	logPath := filepath.Join(s.T().TempDir(), "mcp.log")

	configLoad = func() (*config.Config, error) {
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

	ensureChannelFunc = func(_, _ string) (string, error) {
		return "resolved-ch", nil
	}

	memoryOptReceived := false
	newMCPServer = func(channelID, apiURL, authorID string, httpClient mcpserver.HTTPClient, logger *slog.Logger, opts ...mcpserver.MemoryOption) *mcpserver.Server {
		require.Equal(s.T(), "resolved-ch", channelID)
		if len(opts) > 0 {
			memoryOptReceived = true
		}
		return mcpserver.New(channelID, apiURL, authorID, httpClient, logger, opts...)
	}

	_ = runMCP("", "http://localhost:8222", "/home/user/dev/loop", logPath, "", false)
	require.True(s.T(), memoryOptReceived)
}

func (s *MainSuite) TestRunMCPWithMemoryEnabledChannelIDMode() {
	// When dirPath is empty (channel-id mode), memory should still be enabled via channel_id
	logPath := filepath.Join(s.T().TempDir(), "mcp.log")

	configLoad = func() (*config.Config, error) {
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
	newMCPServer = func(channelID, apiURL, authorID string, httpClient mcpserver.HTTPClient, logger *slog.Logger, opts ...mcpserver.MemoryOption) *mcpserver.Server {
		if len(opts) > 0 {
			memoryOptReceived = true
		}
		return mcpserver.New(channelID, apiURL, authorID, httpClient, logger, opts...)
	}

	_ = runMCP("ch1", "http://localhost:8222", "", logPath, "", false)
	require.True(s.T(), memoryOptReceived)
}

func (s *MainSuite) TestRunMCPWithMemoryNotEnabled() {
	// When memory is not enabled, memory tools should NOT be wired
	logPath := filepath.Join(s.T().TempDir(), "mcp.log")

	configLoad = func() (*config.Config, error) {
		return &config.Config{
			LogLevel:  "info",
			LogFormat: "text",
		}, nil
	}

	memoryOptReceived := false
	newMCPServer = func(channelID, apiURL, authorID string, httpClient mcpserver.HTTPClient, logger *slog.Logger, opts ...mcpserver.MemoryOption) *mcpserver.Server {
		if len(opts) > 0 {
			memoryOptReceived = true
		}
		return mcpserver.New(channelID, apiURL, authorID, httpClient, logger)
	}

	ensureChannelFunc = func(_, _ string) (string, error) {
		return "ch1", nil
	}

	_ = runMCP("", "http://localhost:8222", "/path", logPath, "", false)
	require.False(s.T(), memoryOptReceived)
}

func (s *MainSuite) TestRunMCPWithMemoryFlag() {
	// When --memory flag is true, memory tools should be enabled regardless of config.
	logPath := filepath.Join(s.T().TempDir(), "mcp.log")

	configLoad = func() (*config.Config, error) {
		return &config.Config{
			LogLevel:  "info",
			LogFormat: "text",
		}, nil
	}

	memoryOptReceived := false
	newMCPServer = func(channelID, apiURL, authorID string, httpClient mcpserver.HTTPClient, logger *slog.Logger, opts ...mcpserver.MemoryOption) *mcpserver.Server {
		if len(opts) > 0 {
			memoryOptReceived = true
		}
		return mcpserver.New(channelID, apiURL, authorID, httpClient, logger, opts...)
	}

	_ = runMCP("ch1", "http://localhost:8222", "", logPath, "", true)
	require.True(s.T(), memoryOptReceived)
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

func (s *MainSuite) TestServeSlackBotError() {
	store := new(mockStore)
	store.On("Close").Return(nil)

	configLoad = func() (*config.Config, error) {
		return testSlackConfig(), nil
	}
	newSQLiteStore = func(_ string) (db.Store, error) {
		return store, nil
	}
	newSlackBot = func(_, _ string, _ *slog.Logger) (orchestrator.Bot, error) {
		return nil, errors.New("slack error")
	}

	err := serve()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "creating slack bot")
	store.AssertExpectations(s.T())
}

func (s *MainSuite) TestServeSlackHappyPathShutdown() {
	store := new(mockStore)
	store.On("Close").Return(nil)

	bot := new(mockBot)
	bot.On("OnMessage", mock.Anything).Return()
	bot.On("OnInteraction", mock.Anything).Return()
	bot.On("OnChannelDelete", mock.Anything).Return()
	bot.On("OnChannelJoin", mock.Anything).Return()
	bot.On("RegisterCommands", mock.Anything).Return(nil)
	bot.On("Start", mock.Anything).Return(nil)
	bot.On("Stop").Return(nil)

	dockerClient := new(mockDockerClient)
	dockerClient.On("ContainerList", mock.Anything, "app", "loop-agent").Return([]string{}, nil)

	configLoad = func() (*config.Config, error) {
		return testSlackConfig(), nil
	}
	newSQLiteStore = func(_ string) (db.Store, error) {
		return store, nil
	}
	newSlackBot = func(_, _ string, _ *slog.Logger) (orchestrator.Bot, error) {
		return bot, nil
	}
	newDockerClient = func() (container.DockerClient, error) {
		return dockerClient, nil
	}
	ensureImage = func(_ context.Context, _ container.DockerClient, _ *config.Config) error {
		return nil
	}

	// Verify that channel/thread services are created for Slack (no guild ID needed).
	channelsCh := make(chan api.ChannelEnsurer, 1)
	threadsCh := make(chan api.ThreadEnsurer, 1)
	newAPIServer = func(sched scheduler.Scheduler, channels api.ChannelEnsurer, threads api.ThreadEnsurer, store api.ChannelLister, messages api.MessageSender, logger *slog.Logger) apiServer {
		channelsCh <- channels
		threadsCh <- threads
		return api.NewServer(sched, channels, threads, store, messages, logger)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- serve()
	}()

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

	store.AssertExpectations(s.T())
	bot.AssertExpectations(s.T())
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
	bot.On("OnChannelDelete", mock.Anything).Return()
	bot.On("OnChannelJoin", mock.Anything).Return()
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
	bot.On("OnChannelDelete", mock.Anything).Return()
	bot.On("OnChannelJoin", mock.Anything).Return()
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
	bot.On("OnChannelDelete", mock.Anything).Return()
	bot.On("OnChannelJoin", mock.Anything).Return()
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
	newAPIServer = func(sched scheduler.Scheduler, channels api.ChannelEnsurer, threads api.ThreadEnsurer, store api.ChannelLister, messages api.MessageSender, logger *slog.Logger) apiServer {
		channelsCh <- channels
		return api.NewServer(sched, channels, threads, store, messages, logger)
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
	bot.On("OnChannelDelete", mock.Anything).Return()
	bot.On("OnChannelJoin", mock.Anything).Return()
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
	bot.On("OnChannelDelete", mock.Anything).Return()
	bot.On("OnChannelJoin", mock.Anything).Return()
	bot.On("RegisterCommands", mock.Anything).Return(nil)
	bot.On("Start", mock.Anything).Return(nil)
	bot.On("Stop").Return(nil)

	dockerClient := new(mockDockerClient)
	dockerClient.On("ContainerList", mock.Anything, "app", "loop-agent").Return([]string{}, nil)

	mockAPI := new(mockAPIServer)
	mockAPI.On("SetLoopDir", mock.Anything).Return()
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
	newAPIServer = func(_ scheduler.Scheduler, _ api.ChannelEnsurer, _ api.ThreadEnsurer, _ api.ChannelLister, _ api.MessageSender, _ *slog.Logger) apiServer {
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

func (s *MainSuite) TestServeWithMemoryEnabled() {
	store := new(mockStore)
	store.On("Close").Return(nil)
	// reindexAll goroutine may call ListChannels before the test exits.
	store.On("ListChannels", mock.Anything).Maybe().Return([]*db.Channel{}, nil)

	bot := new(mockBot)
	bot.On("OnMessage", mock.Anything).Return()
	bot.On("OnInteraction", mock.Anything).Return()
	bot.On("OnChannelDelete", mock.Anything).Return()
	bot.On("OnChannelJoin", mock.Anything).Return()
	bot.On("RegisterCommands", mock.Anything).Return(errors.New("fail early"))

	dockerClient := new(mockDockerClient)

	cfg := testConfig()
	cfg.Memory = config.MemoryConfig{
		Enabled: true,
		Embeddings: config.EmbeddingsConfig{
			Provider:  "ollama",
			OllamaURL: "http://localhost:11434",
		},
		Paths: []string{"./memory"},
	}
	cfg.LoopDir = s.T().TempDir()

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

	memoryIndexerSet := false
	newAPIServer = func(sched scheduler.Scheduler, channels api.ChannelEnsurer, threads api.ThreadEnsurer, store api.ChannelLister, messages api.MessageSender, logger *slog.Logger) apiServer {
		srv := api.NewServer(sched, channels, threads, store, messages, logger)
		return &memoryIndexableAPIServer{Server: srv, onSetMemoryIndexer: func(idx api.MemoryIndexer) {
			memoryIndexerSet = true
			srv.SetMemoryIndexer(idx)
		}}
	}

	// serve() will fail at orchestrator start (RegisterCommands error)
	err := serve()
	require.Error(s.T(), err)
	require.True(s.T(), memoryIndexerSet, "memory indexer should be set on API server")
}

func (s *MainSuite) TestServeWithMemoryEmbedderError() {
	store := new(mockStore)
	store.On("Close").Return(nil)

	bot := new(mockBot)
	bot.On("OnMessage", mock.Anything).Return()
	bot.On("OnInteraction", mock.Anything).Return()
	bot.On("OnChannelDelete", mock.Anything).Return()
	bot.On("OnChannelJoin", mock.Anything).Return()
	bot.On("RegisterCommands", mock.Anything).Return(errors.New("fail early"))

	dockerClient := new(mockDockerClient)

	cfg := testConfig()
	cfg.Memory = config.MemoryConfig{
		Enabled: true,
		Embeddings: config.EmbeddingsConfig{
			Provider: "unsupported-provider",
		},
	}

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
	newAPIServer = fakeAPIServer()

	// serve() continues even when embeddings fail (logs a warning)
	err := serve()
	require.Error(s.T(), err) // Fails at orchestrator, not at embeddings
	require.Contains(s.T(), err.Error(), "starting orchestrator")
}

// memoryIndexableAPIServer wraps api.Server to detect SetMemoryIndexer calls.
type memoryIndexableAPIServer struct {
	*api.Server
	onSetMemoryIndexer func(api.MemoryIndexer)
}

func (s *memoryIndexableAPIServer) SetMemoryIndexer(idx api.MemoryIndexer) {
	s.onSetMemoryIndexer(idx)
}

func (s *MainSuite) TestServeDockerClientCloserCalled() {
	store := new(mockStore)
	store.On("Close").Return(nil)

	bot := new(mockBot)
	bot.On("OnMessage", mock.Anything).Return()
	bot.On("OnInteraction", mock.Anything).Return()
	bot.On("OnChannelDelete", mock.Anything).Return()
	bot.On("OnChannelJoin", mock.Anything).Return()
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
	require.NotNil(s.T(), newSlackBot)
	require.NotNil(s.T(), newDockerClient)
	require.NotNil(s.T(), newSQLiteStore)
	require.NotNil(s.T(), newAPIServer)
	require.NotNil(s.T(), newMCPServer)

	// Verify newAPIServer produces a non-nil apiServer
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	apiSrv := newAPIServer(nil, nil, nil, nil, nil, logger)
	require.NotNil(s.T(), apiSrv)

	// Verify newMCPServer produces a non-nil server
	mcpSrv := newMCPServer("ch1", "http://localhost:8222", "", http.DefaultClient, nil)
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

func (s *MainSuite) TestDefaultNewSlackBot() {
	// Exercise the default newSlackBot — creates a bot without needing a server.
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	bot, err := s.origNewSlackBot("xoxb-fake", "xapp-fake", logger)
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
	f := cmd.Flags().Lookup("owner-id")
	require.NotNil(s.T(), f)
	require.Equal(s.T(), "", f.DefValue)
}

func (s *MainSuite) TestNewOnboardLocalCmd() {
	cmd := newOnboardLocalCmd()
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
	userHomeDir = func() (string, error) {
		return tmpDir, nil
	}
	osStat = os.Stat
	osMkdirAll = os.MkdirAll
	osWriteFile = os.WriteFile

	err := onboardGlobal(false, "")
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

	// Verify heartbeat template was written
	heartbeatPath := filepath.Join(templatesDir, "heartbeat.md")
	heartbeatData, err := os.ReadFile(heartbeatPath)
	require.NoError(s.T(), err)
	require.Contains(s.T(), string(heartbeatData), "heartbeat check")
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

	err := onboardGlobal(false, "")
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

	err := onboardGlobal(true, "")
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

	err := onboardGlobal(false, "")
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

	err := onboardGlobal(false, "")
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

	err := onboardGlobal(false, "")
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

	err := onboardGlobal(false, "")
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

	err := onboardGlobal(true, "") // force overwrites config but not .bashrc
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

	err := onboardGlobal(false, "")
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

	err := onboardGlobal(false, "")
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

	err := onboardGlobal(false, "")
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

	err := onboardGlobal(false, "")
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

	err := onboardGlobal(true, "") // force overwrites config but not setup.sh
	require.NoError(s.T(), err)

	data, err := os.ReadFile(setupPath)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "existing setup", string(data))
}

func (s *MainSuite) TestOnboardGlobalSlackManifestWriteError() {
	tmpDir := s.T().TempDir()
	userHomeDir = func() (string, error) {
		return tmpDir, nil
	}
	osStat = os.Stat
	osMkdirAll = os.MkdirAll
	calls := 0
	osWriteFile = func(path string, data []byte, perm os.FileMode) error {
		calls++
		if calls == 6 { // Sixth write is slack-manifest.json
			return errors.New("manifest write error")
		}
		return os.WriteFile(path, data, perm)
	}

	err := onboardGlobal(false, "")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "writing Slack manifest")
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

	err := onboardGlobal(false, "")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "creating templates directory")
}

func (s *MainSuite) TestOnboardGlobalHeartbeatWriteError() {
	tmpDir := s.T().TempDir()
	userHomeDir = func() (string, error) {
		return tmpDir, nil
	}
	osStat = os.Stat
	osMkdirAll = os.MkdirAll
	calls := 0
	osWriteFile = func(path string, data []byte, perm os.FileMode) error {
		calls++
		if calls == 7 { // Seventh write is heartbeat template
			return errors.New("heartbeat write error")
		}
		return os.WriteFile(path, data, perm)
	}

	err := onboardGlobal(false, "")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "writing heartbeat template")
}

func (s *MainSuite) TestOnboardGlobalHeartbeatSkipsIfExists() {
	tmpDir := s.T().TempDir()
	loopDir := filepath.Join(tmpDir, ".loop")
	templatesDir := filepath.Join(loopDir, "templates")
	heartbeatPath := filepath.Join(templatesDir, "heartbeat.md")

	require.NoError(s.T(), os.MkdirAll(templatesDir, 0755))
	require.NoError(s.T(), os.WriteFile(heartbeatPath, []byte("custom heartbeat"), 0644))

	userHomeDir = func() (string, error) {
		return tmpDir, nil
	}
	osStat = os.Stat
	osMkdirAll = os.MkdirAll
	osWriteFile = os.WriteFile

	err := onboardGlobal(true, "") // force overwrites config but not heartbeat template
	require.NoError(s.T(), err)

	data, err := os.ReadFile(heartbeatPath)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "custom heartbeat", string(data))
}

func (s *MainSuite) TestOnboardGlobalWithOwnerID() {
	tmpDir := s.T().TempDir()
	userHomeDir = func() (string, error) {
		return tmpDir, nil
	}
	osStat = os.Stat
	osMkdirAll = os.MkdirAll
	osWriteFile = os.WriteFile

	err := onboardGlobal(false, "U99887766")
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
	userHomeDir = func() (string, error) {
		return tmpDir, nil
	}
	osStat = os.Stat
	osMkdirAll = os.MkdirAll
	osWriteFile = os.WriteFile

	cmd := newOnboardGlobalCmd()
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
	osGetwd = func() (string, error) { return tmpDir, nil }
	osReadFile = os.ReadFile
	osWriteFile = os.WriteFile
	osStat = os.Stat
	osMkdirAll = os.MkdirAll
	ensureChannelFunc = func(_, _ string) (string, error) { return "ch-test", nil }

	err := onboardLocal("http://localhost:8222", "")
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

func (s *MainSuite) TestOnboardLocalWithMemoryEnabled() {
	tmpDir := s.T().TempDir()
	osGetwd = func() (string, error) { return tmpDir, nil }
	osReadFile = os.ReadFile
	osWriteFile = os.WriteFile
	osStat = os.Stat
	osMkdirAll = os.MkdirAll
	ensureChannelFunc = func(_, _ string) (string, error) { return "ch-test", nil }
	configLoad = func() (*config.Config, error) {
		return &config.Config{Memory: config.MemoryConfig{Enabled: true}}, nil
	}
	defer func() { configLoad = config.Load }()

	err := onboardLocal("http://localhost:8222", "")
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

	osGetwd = func() (string, error) { return tmpDir, nil }
	osReadFile = os.ReadFile
	osWriteFile = os.WriteFile
	ensureChannelFunc = func(_, _ string) (string, error) { return "ch-test", nil }

	err := onboardLocal("http://localhost:8222", "")
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

	osGetwd = func() (string, error) { return tmpDir, nil }
	osReadFile = os.ReadFile
	osWriteFile = os.WriteFile
	osStat = os.Stat
	osMkdirAll = os.MkdirAll
	ensureChannelFunc = func(_, _ string) (string, error) { return "ch-test", nil }

	err := onboardLocal("http://localhost:8222", "")
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

	osGetwd = func() (string, error) { return tmpDir, nil }
	osReadFile = os.ReadFile

	err := onboardLocal("http://localhost:8222", "")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "parsing existing .mcp.json")
}

func (s *MainSuite) TestOnboardLocalGetwdError() {
	osGetwd = func() (string, error) { return "", errors.New("getwd error") }

	err := onboardLocal("http://localhost:8222", "")
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

	err := onboardLocal("http://localhost:8222", "")
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

	err := onboardLocal("http://localhost:8222", "")
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

	err := onboardLocal("http://localhost:8222", "")
	require.NoError(s.T(), err, "onboardLocal should succeed even when ensureChannel fails")
}

func (s *MainSuite) TestOnboardLocalAlreadyRegisteredStillEnsuresChannel() {
	tmpDir := s.T().TempDir()
	existing := `{"mcpServers":{"loop":{"command":"loop","args":["mcp","--dir","` + tmpDir + `","--api-url","http://localhost:8222"]}}}`
	require.NoError(s.T(), os.WriteFile(filepath.Join(tmpDir, ".mcp.json"), []byte(existing), 0644))

	osGetwd = func() (string, error) { return tmpDir, nil }
	osReadFile = os.ReadFile
	osWriteFile = os.WriteFile
	osStat = os.Stat
	osMkdirAll = os.MkdirAll

	called := false
	ensureChannelFunc = func(_, _ string) (string, error) {
		called = true
		return "ch-456", nil
	}

	err := onboardLocal("http://localhost:8222", "")
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

	err := onboardLocal("http://localhost:8222", "")
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

	err := onboardLocal("http://localhost:8222", "")
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

	err := onboardLocal("http://localhost:8222", "")
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

	err := onboardLocal("http://localhost:8222", "")
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

	err := onboardLocal("http://localhost:8222", "")
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

	err := onboardLocal("http://localhost:8222", "")
	require.NoError(s.T(), err)

	// Verify templates directory was created
	templatesDir := filepath.Join(tmpDir, ".loop", "templates")
	info, err := os.Stat(templatesDir)
	require.NoError(s.T(), err)
	require.True(s.T(), info.IsDir())
}

func (s *MainSuite) TestOnboardLocalWithOwnerID() {
	tmpDir := s.T().TempDir()
	osGetwd = func() (string, error) { return tmpDir, nil }
	osReadFile = os.ReadFile
	osWriteFile = os.WriteFile
	osStat = os.Stat
	osMkdirAll = os.MkdirAll
	ensureChannelFunc = func(_, _ string) (string, error) { return "ch-test", nil }

	err := onboardLocal("http://localhost:8222", "U99887766")
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
	osGetwd = func() (string, error) { return tmpDir, nil }
	osReadFile = os.ReadFile
	osWriteFile = os.WriteFile
	osStat = os.Stat
	osMkdirAll = os.MkdirAll
	ensureChannelFunc = func(_, _ string) (string, error) { return "ch-test", nil }

	cmd := newOnboardLocalCmd()
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

// --- newReadmeCmd ---

func (s *MainSuite) TestNewReadmeCmd() {
	cmd := newReadmeCmd()
	require.Equal(s.T(), "readme", cmd.Use)
	require.Equal(s.T(), []string{"r"}, cmd.Aliases)
	require.NotNil(s.T(), cmd.Run)
}

func (s *MainSuite) TestReadmeOutput() {
	cmd := newReadmeCmd()
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	require.NoError(s.T(), err)
}
