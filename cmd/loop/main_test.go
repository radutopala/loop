package main

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"runtime/debug"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/api"
	"github.com/radutopala/loop/internal/bot"

	"github.com/radutopala/loop/internal/browser"
	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/container"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/local"
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

func (m *mockDockerClient) ListContainerInfos(ctx context.Context) ([]*container.ContainerInfo, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*container.ContainerInfo), args.Error(1)
}

func (m *mockDockerClient) CopyToContainer(ctx context.Context, containerID, dstPath string, content io.Reader) error {
	return m.Called(ctx, containerID, dstPath, content).Error(0)
}

func (m *mockDockerClient) NetworkEnsure(ctx context.Context, name string) error {
	return m.Called(ctx, name).Error(0)
}

func (m *mockDockerClient) RemoveImageAndContainers(ctx context.Context, imageName string) error {
	return m.Called(ctx, imageName).Error(0)
}

func (m *mockDockerClient) ImageInspectLabels(ctx context.Context, imageName string) (map[string]string, error) {
	args := m.Called(ctx, imageName)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(map[string]string), args.Error(1)
}

func (m *mockDockerClient) SetLoopVersion(v string) {}

func (m *mockDockerClient) LatestClaudeVersion() string {
	args := m.Called()
	return args.String(0)
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

func (m *mockBot) SendApproval(ctx context.Context, channelID string, prompt bot.ApprovalPrompt) (string, error) {
	args := m.Called(ctx, channelID, prompt)
	return args.String(0), args.Error(1)
}

func (m *mockBot) RemoveApproval(ctx context.Context, channelID, messageID string) error {
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
	m.store.On("WriterDB").Return((*sql.DB)(nil)).Maybe()
	m.store.On("ListWorkflowRunsByStatus", mock.Anything, mock.Anything).Return(nil, nil).Maybe()
	m.dockerClient.On("LatestClaudeVersion").Return("1.0.0").Maybe()
	m.dockerClient.On("ListContainerInfos", mock.Anything).Return([]*container.ContainerInfo{}, nil).Maybe()
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
	m.dockerClient.On("ContainerList", mock.Anything, "loop-instance", mock.Anything).Return([]string{}, nil).Maybe()
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

// --- newDockerproxyCmd ---

func (s *MainSuite) TestNewDockerproxyCmdRunEInvokesRunAndExits() {
	var gotCode int
	var ran bool
	s.app.osExit = func(code int) { gotCode = code }
	s.app.dockerproxyRun = func(_ io.Writer, _ io.Writer) int {
		ran = true
		return 7
	}

	cmd := s.app.newDockerproxyCmd()
	require.Equal(s.T(), "dockerproxy", cmd.Use)
	require.True(s.T(), cmd.Hidden)

	cmd.SetArgs([]string{})
	require.NoError(s.T(), cmd.Execute())
	require.True(s.T(), ran)
	require.Equal(s.T(), 7, gotCode)
}

// --- newSyscallwrapCmd ---

// TestRunSyscallwrapProdDelegatesToSyscallwrapRun covers the package-level
// runSyscallwrap helper (Linux build). No target args → parseArgs errors →
// runMain returns 1.
func (s *MainSuite) TestRunSyscallwrapProdDelegatesToSyscallwrapRun() {
	code := runSyscallwrap(nil, []string{"loop", "syscallwrap"})
	require.Equal(s.T(), 1, code)
}

func (s *MainSuite) TestNewSyscallwrapCmdRunEForwardsArgs() {
	var gotForward []string
	var gotCode int
	s.app.osExit = func(code int) { gotCode = code }
	s.app.syscallwrapRun = func(forward, _ []string) int {
		gotForward = forward
		return 3
	}

	cmd := s.app.newSyscallwrapCmd()
	require.Equal(s.T(), "syscallwrap [--] <cmd> [args...]", cmd.Use)
	require.True(s.T(), cmd.Hidden)
	require.True(s.T(), cmd.DisableFlagParsing)

	cmd.SetArgs([]string{"--", "claude", "-p"})
	require.NoError(s.T(), cmd.Execute())
	require.Equal(s.T(), []string{"--", "claude", "-p"}, gotForward)
	require.Equal(s.T(), 3, gotCode)
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
	m.dockerClient.On("ContainerList", mock.Anything, "loop-instance", mock.Anything).Return([]string{}, nil).Maybe()

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

func (s *MainSuite) TestServeWithDockerBrowserProvider() {
	m := s.setupServeMocks()
	m.setupHappyBot()
	m.cfg.Browser.Enabled = true

	s.app.newBrowserProvider = func(_ string, logger *slog.Logger) (api.BrowserProvider, error) {
		return browser.NewDockerProvider(nil, "loop-chrome:latest", "1920,1080", logger), nil
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

func (s *MainSuite) TestServeRegistryRestoreError() {
	m := s.setupServeMocks()
	m.setupHappyBot()

	// Override ListContainerInfos to return an error (covers the warn log path).
	m.dockerClient.ExpectedCalls = filterExpected(m.dockerClient.ExpectedCalls, "ListContainerInfos")
	m.dockerClient.On("ListContainerInfos", mock.Anything).Return(([]*container.ContainerInfo)(nil), errors.New("docker unavailable"))

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

func (s *MainSuite) TestServeRegistryRestoreWithData() {
	m := s.setupServeMocks()
	m.setupHappyBot()

	// Override ListContainerInfos to return existing containers (covers the Restore path
	// and ScheduleRemove for stopped containers).
	m.dockerClient.ExpectedCalls = filterExpected(m.dockerClient.ExpectedCalls, "ListContainerInfos")
	m.dockerClient.On("ListContainerInfos", mock.Anything).Return([]*container.ContainerInfo{
		{ContainerID: "existing-1", ChannelID: "ch-1", Type: container.ContainerTypeAgent},
		{ContainerID: "stopped-1", ChannelID: "ch-2", Type: container.ContainerTypeAgent, Status: container.ContainerStatusStopped},
	}, nil)
	// ScheduleRemove fires immediately (ContainerKeepAlive=0) and calls ContainerRemove
	// for the stopped container. Cleanup scoped by loop-instance label won't match
	// restored containers since they were created by a different daemon instance.
	m.dockerClient.On("ContainerRemove", mock.Anything, "stopped-1").Return(nil).Maybe()

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

func (s *MainSuite) TestServeWithWorkflowBashLocal() {
	m := s.setupServeMocks()
	m.setupHappyBot()
	m.cfg.WorkflowBashLocal = true

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

	orch := orchestrator.New(store, localBot, nil, sched, logger, config.Config{}, nil)
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
	orch := orchestrator.New(store, localBot, nil, sched, logger, config.Config{}, nil)
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
	orch := orchestrator.New(store, localBot, nil, sched, logger, config.Config{}, nil)
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
	orch := orchestrator.New(store, localBot, nil, sched, logger, config.Config{}, nil)
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

func (n *noopExecClient) ExecInspectPid(_ context.Context, _ string) (int, error) {
	return 0, nil
}

type noopBrowserProvider struct{}

func (n *noopBrowserProvider) EnsureBrowser(_ context.Context, _, _ string) error { return nil }
func (n *noopBrowserProvider) StopBrowser(_ context.Context, _ string) (string, error) {
	return "", nil
}
func (n *noopBrowserProvider) IsRunning(_ context.Context, _ string) bool { return false }
func (n *noopBrowserProvider) GetCDPEndpoint(_ string) string             { return "" }
func (n *noopBrowserProvider) GetContainerID(_ string) (string, bool)     { return "", false }
func (n *noopBrowserProvider) IsHostMode() bool                           { return false }

func (s *MainSuite) TestDumpPlaygroundExamplesSkipExisting() {
	dir := s.T().TempDir()
	s.app.sys = newPassthroughMock()

	// First run: populate all examples.
	err := s.app.dumpPlaygroundExamples(dir)
	require.NoError(s.T(), err)

	// Pick one of the example directories and write a sentinel file inside it.
	entries, err := os.ReadDir(dir)
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), entries)

	existingExample := entries[0].Name()
	sentinelPath := filepath.Join(dir, existingExample, "sentinel.txt")
	require.NoError(s.T(), os.WriteFile(sentinelPath, []byte("do-not-overwrite"), 0644))

	// Second run: should skip the existing example directory.
	err = s.app.dumpPlaygroundExamples(dir)
	require.NoError(s.T(), err)

	// Verify the sentinel file still exists (directory was not re-created).
	data, err := os.ReadFile(sentinelPath)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "do-not-overwrite", string(data))
}

// --- Fake FS types for playground error branch testing ---

// brokenPlaygroundReadDirFS fails on ReadDir("examples").
type brokenPlaygroundReadDirFS struct{}

func (brokenPlaygroundReadDirFS) Open(string) (fs.File, error) { return nil, errors.New("broken") }

// playgroundReadDirErrorFS succeeds on ReadDir("examples") returning one dir entry,
// but fails on ReadDir("examples/myexample") to cover the per-example ReadDir error.
type playgroundReadDirErrorFS struct{}

func (playgroundReadDirErrorFS) Open(name string) (fs.File, error) {
	if name == "examples" {
		return &fakeDirFile{entries: []fs.DirEntry{&fakeDirEntry{name: "myexample"}}}, nil
	}
	return nil, errors.New("broken readdir for example")
}

// playgroundReadFileErrorFS succeeds on ReadDir at both levels,
// but fails on ReadFile to cover the per-file read error.
type playgroundReadFileErrorFS struct{}

func (playgroundReadFileErrorFS) Open(name string) (fs.File, error) {
	if name == "examples" {
		return &fakeDirFile{entries: []fs.DirEntry{&fakeDirEntry{name: "myexample"}}}, nil
	}
	if name == "examples/myexample" {
		return &fakeDirFile{entries: []fs.DirEntry{&fakeEntry{name: "index.html"}}}, nil
	}
	return nil, errors.New("broken readfile")
}

func (playgroundReadFileErrorFS) ReadFile(string) ([]byte, error) {
	return nil, errors.New("broken readfile")
}

func (s *MainSuite) TestDumpPlaygroundExamplesReadDirError() {
	s.app.sys = newPassthroughMock()
	s.app.playgroundExamplesFS = brokenPlaygroundReadDirFS{}

	err := s.app.dumpPlaygroundExamples(s.T().TempDir())
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "reading embedded playground examples")
}

func (s *MainSuite) TestDumpPlaygroundExamplesExampleReadDirError() {
	s.app.sys = newPassthroughMock()
	s.app.playgroundExamplesFS = playgroundReadDirErrorFS{}

	err := s.app.dumpPlaygroundExamples(s.T().TempDir())
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "reading playground example myexample")
}

func (s *MainSuite) TestDumpPlaygroundExamplesReadFileError() {
	s.app.sys = newPassthroughMock()
	s.app.playgroundExamplesFS = playgroundReadFileErrorFS{}

	err := s.app.dumpPlaygroundExamples(s.T().TempDir())
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "reading playground file myexample/index.html")
}

// --- dumpShortcuts ---

type brokenShortcutsReadDirFS struct{}

func (brokenShortcutsReadDirFS) Open(string) (fs.File, error)    { return nil, errors.New("broken") }
func (brokenShortcutsReadDirFS) ReadFile(string) ([]byte, error) { return nil, errors.New("broken") }

type brokenShortcutsReadFileFS struct{ brokenShortcutsReadDirFS }

func (brokenShortcutsReadFileFS) Open(name string) (fs.File, error) {
	if name == "shortcuts" {
		return &fakeDirFile{entries: []fs.DirEntry{&fakeEntry{name: "test.md"}}}, nil
	}
	return nil, errors.New("broken")
}

type shortcutsDirEntryFS struct{}

func (shortcutsDirEntryFS) Open(name string) (fs.File, error) {
	if name == "shortcuts" {
		return &fakeDirFile{entries: []fs.DirEntry{&fakeDirEntry{name: "subdir"}}}, nil
	}
	return nil, errors.New("not found")
}

func (shortcutsDirEntryFS) ReadFile(string) ([]byte, error) {
	return nil, errors.New("should not be called")
}

func (s *MainSuite) TestDumpShortcutsReadDirError() {
	s.app.shortcutsFS = brokenShortcutsReadDirFS{}

	err := s.app.dumpShortcuts(s.T().TempDir())
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "reading embedded shortcuts")
}

func (s *MainSuite) TestDumpShortcutsReadFileError() {
	s.app.shortcutsFS = brokenShortcutsReadFileFS{}

	err := s.app.dumpShortcuts(s.T().TempDir())
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "reading embedded shortcut test.md")
}

func (s *MainSuite) TestDumpShortcutsSkipsDirectories() {
	s.app.shortcutsFS = &shortcutsDirEntryFS{}

	err := s.app.dumpShortcuts(s.T().TempDir())
	require.NoError(s.T(), err)
}

func (s *MainSuite) TestDumpShortcutsSkipIfExist() {
	tmpDir := s.T().TempDir()
	require.NoError(s.T(), os.WriteFile(filepath.Join(tmpDir, "review-code.md"), []byte("custom"), 0644))

	s.app.sys = newPassthroughMock()
	err := s.app.dumpShortcuts(tmpDir)
	require.NoError(s.T(), err)

	data, err := os.ReadFile(filepath.Join(tmpDir, "review-code.md"))
	require.NoError(s.T(), err)
	require.Equal(s.T(), "custom", string(data))
}

func (s *MainSuite) TestDumpShortcutsWriteError() {
	sys := newPassthroughMock()
	sys.Override("WriteFile", mock.Anything, mock.Anything, mock.Anything).Return(errors.New("disk full"))
	s.app.sys = sys

	err := s.app.dumpShortcuts(s.T().TempDir())
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "writing shortcut")
}

func (s *MainSuite) TestMainCallsOsExit() {
	var exitCode int
	origExit := osExit
	osExit = func(code int) { exitCode = code }
	defer func() { osExit = origExit }()

	origArgs := os.Args
	os.Args = []string{"loop", "--help"}
	defer func() { os.Args = origArgs }()

	main()
	require.Equal(s.T(), 0, exitCode)
}
