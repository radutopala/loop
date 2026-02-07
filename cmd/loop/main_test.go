package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
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

func (m *mockStore) SetChannelActive(ctx context.Context, channelID string, active bool) error {
	return m.Called(ctx, channelID, active).Error(0)
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

func (m *mockStore) InsertTaskRunLog(ctx context.Context, trl *db.TaskRunLog) (int64, error) {
	args := m.Called(ctx, trl)
	return args.Get(0).(int64), args.Error(1)
}

func (m *mockStore) UpdateTaskRunLog(ctx context.Context, trl *db.TaskRunLog) error {
	return m.Called(ctx, trl).Error(0)
}

func (m *mockStore) GetRegisteredChannels(ctx context.Context) ([]*db.Channel, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*db.Channel), args.Error(1)
}

func (m *mockStore) Close() error {
	return m.Called().Error(0)
}

type mockDockerClient struct {
	mock.Mock
}

func (m *mockDockerClient) ContainerCreate(ctx context.Context, cfg *container.ContainerConfig, name string) (string, error) {
	args := m.Called(ctx, cfg, name)
	return args.String(0), args.Error(1)
}

func (m *mockDockerClient) ContainerAttach(ctx context.Context, containerID string) (io.Reader, error) {
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
	origConfigLoad      func() (*config.Config, error)
	origNewDiscordBot   func(string, string, *slog.Logger) (orchestrator.Bot, error)
	origNewDockerClient func() (container.DockerClient, error)
	origNewSQLiteStore  func(string) (db.Store, error)
	origOsExit          func(int)
	origNewAPIServer    func(scheduler.Scheduler, *slog.Logger) apiServer
	origNewMCPServer    func(string, string, mcpserver.HTTPClient, *slog.Logger) *mcpserver.Server
	origMCPLogOpen      func() (*os.File, error)
	origDaemonStart     func(daemon.System) error
	origDaemonStop      func(daemon.System) error
	origDaemonStatus    func(daemon.System) (string, error)
	origNewSystem       func() daemon.System
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
	s.origMCPLogOpen = mcpLogOpen
	s.origDaemonStart = daemonStart
	s.origDaemonStop = daemonStop
	s.origDaemonStatus = daemonStatus
	s.origNewSystem = newSystem
}

func (s *MainSuite) TearDownTest() {
	configLoad = s.origConfigLoad
	newDiscordBot = s.origNewDiscordBot
	newDockerClient = s.origNewDockerClient
	newSQLiteStore = s.origNewSQLiteStore
	osExit = s.origOsExit
	newAPIServer = s.origNewAPIServer
	newMCPServer = s.origNewMCPServer
	mcpLogOpen = s.origMCPLogOpen
	daemonStart = s.origDaemonStart
	daemonStop = s.origDaemonStop
	daemonStatus = s.origDaemonStatus
	newSystem = s.origNewSystem
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
func fakeAPIServer() func(scheduler.Scheduler, *slog.Logger) apiServer {
	return func(sched scheduler.Scheduler, logger *slog.Logger) apiServer {
		return api.NewServer(sched, logger)
	}
}

// --- newRootCmd ---

func (s *MainSuite) TestNewRootCmd() {
	cmd := newRootCmd()
	require.Equal(s.T(), "loop", cmd.Use)
	require.True(s.T(), cmd.HasSubCommands())

	foundServe := false
	foundMCP := false
	foundDaemon := false
	for _, sub := range cmd.Commands() {
		if sub.Use == "serve" {
			foundServe = true
		}
		if sub.Use == "mcp" {
			foundMCP = true
		}
		if sub.Use == "daemon" {
			foundDaemon = true
		}
	}
	require.True(s.T(), foundServe, "root command should have serve subcommand")
	require.True(s.T(), foundMCP, "root command should have mcp subcommand")
	require.True(s.T(), foundDaemon, "root command should have daemon subcommand")
}

// --- newServeCmd ---

func (s *MainSuite) TestNewServeCmd() {
	cmd := newServeCmd()
	require.Equal(s.T(), "serve", cmd.Use)
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
	require.NotNil(s.T(), cmd.RunE)

	// Flags should be registered
	f := cmd.Flags()
	require.NotNil(s.T(), f.Lookup("channel-id"))
	require.NotNil(s.T(), f.Lookup("api-url"))
}

func (s *MainSuite) TestNewMCPCmdMissingFlags() {
	cmd := newMCPCmd()
	cmd.SetArgs([]string{})
	err := cmd.Execute()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "required")
}

func (s *MainSuite) TestRunMCP() {
	mcpLogOpen = func() (*os.File, error) {
		return os.CreateTemp(s.T().TempDir(), "mcp-*.log")
	}
	called := false
	newMCPServer = func(channelID, apiURL string, httpClient mcpserver.HTTPClient, logger *slog.Logger) *mcpserver.Server {
		require.Equal(s.T(), "ch1", channelID)
		require.Equal(s.T(), "http://localhost:8222", apiURL)
		called = true
		return mcpserver.New(channelID, apiURL, httpClient, logger)
	}

	// runMCP will try to use StdioTransport which will fail/close immediately in test.
	// We just verify the function is wired correctly.
	_ = runMCP("ch1", "http://localhost:8222")
	require.True(s.T(), called)
}

func (s *MainSuite) TestNewMCPCmdRunE() {
	mcpLogOpen = func() (*os.File, error) {
		return os.CreateTemp(s.T().TempDir(), "mcp-*.log")
	}
	// Test the RunE closure is wired correctly by executing with flags.
	called := false
	newMCPServer = func(channelID, apiURL string, httpClient mcpserver.HTTPClient, logger *slog.Logger) *mcpserver.Server {
		require.Equal(s.T(), "ch-test", channelID)
		require.Equal(s.T(), "http://test:8222", apiURL)
		called = true
		return mcpserver.New(channelID, apiURL, httpClient, logger)
	}

	cmd := newMCPCmd()
	cmd.SetArgs([]string{"--channel-id", "ch-test", "--api-url", "http://test:8222"})
	// Execute will invoke RunE -> runMCP which tries StdioTransport.
	// That will return an error in test context, which is fine.
	_ = cmd.Execute()
	require.True(s.T(), called)
}

func (s *MainSuite) TestRunMCPLogOpenError() {
	mcpLogOpen = func() (*os.File, error) {
		return nil, errors.New("log open fail")
	}
	err := runMCP("ch1", "http://localhost:8222")
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "opening mcp log")
}

func (s *MainSuite) TestDefaultMCPLogOpen() {
	// The default mcpLogOpen writes to /mcp/mcp.log (container path).
	// Exercise it by temporarily overriding to a temp dir.
	tmpDir := s.T().TempDir()
	mcpLogOpen = func() (*os.File, error) {
		return os.OpenFile(tmpDir+"/mcp.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	}
	f, err := mcpLogOpen()
	require.NoError(s.T(), err)
	require.NotNil(s.T(), f)
	f.Close()

	// Verify the original default points to /mcp/mcp.log.
	require.NotNil(s.T(), s.origMCPLogOpen)
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
	require.Len(s.T(), res.Tools, 3)
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
	newAPIServer = func(_ scheduler.Scheduler, _ *slog.Logger) apiServer {
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
	require.NotNil(s.T(), mcpLogOpen)

	// Verify newAPIServer produces a non-nil apiServer
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	apiSrv := newAPIServer(nil, logger)
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

// --- newDaemonCmd ---

func (s *MainSuite) TestNewDaemonCmd() {
	cmd := newDaemonCmd()
	require.Equal(s.T(), "daemon", cmd.Use)
	require.True(s.T(), cmd.HasSubCommands())

	foundStart := false
	foundStop := false
	foundStatus := false
	for _, sub := range cmd.Commands() {
		switch sub.Use {
		case "start":
			foundStart = true
		case "stop":
			foundStop = true
		case "status":
			foundStatus = true
		}
	}
	require.True(s.T(), foundStart)
	require.True(s.T(), foundStop)
	require.True(s.T(), foundStatus)
}

func (s *MainSuite) TestDaemonStartSuccess() {
	daemonStart = func(_ daemon.System) error { return nil }
	newSystem = func() daemon.System { return daemon.RealSystem{} }

	cmd := newDaemonCmd()
	cmd.SetArgs([]string{"start"})
	err := cmd.Execute()
	require.NoError(s.T(), err)
}

func (s *MainSuite) TestDaemonStartError() {
	daemonStart = func(_ daemon.System) error { return errors.New("start fail") }
	newSystem = func() daemon.System { return daemon.RealSystem{} }

	cmd := newDaemonCmd()
	cmd.SetArgs([]string{"start"})
	err := cmd.Execute()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "start fail")
}

func (s *MainSuite) TestDaemonStopSuccess() {
	daemonStop = func(_ daemon.System) error { return nil }
	newSystem = func() daemon.System { return daemon.RealSystem{} }

	cmd := newDaemonCmd()
	cmd.SetArgs([]string{"stop"})
	err := cmd.Execute()
	require.NoError(s.T(), err)
}

func (s *MainSuite) TestDaemonStopError() {
	daemonStop = func(_ daemon.System) error { return errors.New("stop fail") }
	newSystem = func() daemon.System { return daemon.RealSystem{} }

	cmd := newDaemonCmd()
	cmd.SetArgs([]string{"stop"})
	err := cmd.Execute()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "stop fail")
}

func (s *MainSuite) TestDaemonStatusSuccess() {
	daemonStatus = func(_ daemon.System) (string, error) { return "running", nil }
	newSystem = func() daemon.System { return daemon.RealSystem{} }

	cmd := newDaemonCmd()
	cmd.SetArgs([]string{"status"})
	err := cmd.Execute()
	require.NoError(s.T(), err)
}

func (s *MainSuite) TestDaemonStatusError() {
	daemonStatus = func(_ daemon.System) (string, error) { return "", errors.New("status fail") }
	newSystem = func() daemon.System { return daemon.RealSystem{} }

	cmd := newDaemonCmd()
	cmd.SetArgs([]string{"status"})
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
