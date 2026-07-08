package main

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/bwmarrin/discordgo"
	"github.com/docker/docker/api/types/events"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/api"
	"github.com/radutopala/loop/internal/bot"
	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/container"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/embeddings"
	"github.com/radutopala/loop/internal/fsmigrate"
	"github.com/radutopala/loop/internal/local"
	"github.com/radutopala/loop/internal/orchestrator"
	"github.com/radutopala/loop/internal/quality/parser"
	"github.com/radutopala/loop/internal/scheduler"
	"github.com/radutopala/loop/internal/testutil"
)

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
		// Note: ensureImage errors are now logged (not returned) since
		// image build runs async after the API server starts.
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			store := new(testutil.MockStore)
			store.On("WriterDB").Return((*sql.DB)(nil)).Maybe()
			tt.setup(store)
			err := s.app.serve()
			require.Error(s.T(), err)
			require.Contains(s.T(), err.Error(), tt.wantErr)
			store.AssertExpectations(s.T())
		})
	}
}

// writerDBStore wraps a *testutil.MockStore and exposes a WriterDB() method so
// the optional fs-migration interface assertion in serve() succeeds.
type writerDBStore struct {
	*testutil.MockStore
	writer *sql.DB
}

func (w *writerDBStore) WriterDB() *sql.DB { return w.writer }

func (s *MainSuite) TestServeFSMigrationError() {
	store := new(testutil.MockStore)
	store.On("Close").Return(nil)
	sqlMock, _, err := sqlmock.New()
	require.NoError(s.T(), err)
	s.T().Cleanup(func() { _ = sqlMock.Close() })
	wrapped := &writerDBStore{MockStore: store, writer: sqlMock}

	s.app.configLoad = func() (*config.Config, error) { return testConfig(), nil }
	s.app.newSQLiteStore = func(_ string) (db.Store, error) { return wrapped, nil }
	s.app.fsMigrateRun = func(_ context.Context, _ *sql.DB, _ *fsmigrate.Ctx) error {
		return errors.New("migration boom")
	}

	err = s.app.serve()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "running fs migrations")
	require.Contains(s.T(), err.Error(), "migration boom")
	store.AssertExpectations(s.T())
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

	s.waitForServeReady(errCh)
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

func (s *MainSuite) TestServeRecoverWorkflowRunsError() {
	m := s.setupServeMocks()
	m.bot.On("OnMessage", mock.Anything).Return()
	m.bot.On("OnInteraction", mock.Anything).Return()
	m.bot.On("OnChannelDelete", mock.Anything).Return()
	m.bot.On("OnChannelJoin", mock.Anything).Return()
	m.bot.On("RegisterCommands", mock.Anything).Return(errors.New("fail early"))

	// Override the default Maybe() mock to return an error.
	m.store.ExpectedCalls = filterExpected(m.store.ExpectedCalls, "ListWorkflowRunsByStatus")
	m.store.On("ListWorkflowRunsByStatus", mock.Anything, mock.Anything).Return(nil, errors.New("db unavailable"))

	// serve() continues past recovery error (logs it) but fails at orchestrator.
	err := s.app.serve()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "starting orchestrator")
}

func (s *MainSuite) TestServeHappyPathShutdown() {
	m := s.setupServeMocks()
	m.setupHappyBot()

	errCh := make(chan error, 1)
	go func() { errCh <- s.app.serve() }()

	s.waitForServeReady(errCh)
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

// TestServeWiresOOMWatcherNotice exercises the OOM-watcher wiring in serve():
// when the docker client implements container.OOMEventStreamer and emits an
// OOM event for a labeled container, serve() should post a channel notice via
// orchestrator.StoreSystemNotice (store insert + broadcast).
func (s *MainSuite) TestServeWiresOOMWatcherNotice() {
	m := s.setupServeMocks()
	m.setupHappyBot()

	msgCh := make(chan events.Message, 1)
	var errChTyped <-chan error
	// Replace the default setupServeMocks OOMEvents(nil,nil) expectation with
	// one that returns a live message channel this test can push into —
	// testify matches same-specificity expectations in registration order, so
	// the default must be removed rather than shadowed.
	filtered := m.dockerClient.ExpectedCalls[:0]
	for _, call := range m.dockerClient.ExpectedCalls {
		if call.Method != "OOMEvents" {
			filtered = append(filtered, call)
		}
	}
	m.dockerClient.ExpectedCalls = filtered
	m.dockerClient.On("OOMEvents", mock.Anything).Return((<-chan events.Message)(msgCh), errChTyped).Maybe()

	notified := make(chan struct{}, 1)
	m.store.On("GetChannel", mock.Anything, "ch-oom").Return(nil, nil).Run(func(_ mock.Arguments) {
		notified <- struct{}{}
	})

	errCh := make(chan error, 1)
	go func() { errCh <- s.app.serve() }()

	s.waitForServeReady(errCh)

	msgCh <- events.Message{Actor: events.Actor{
		ID:         "container-oom-1",
		Attributes: map[string]string{container.ChannelLabelKey: "ch-oom", "name": "loop-agent-ch-oom"},
	}}

	select {
	case <-notified:
	case <-time.After(5 * time.Second):
		s.T().Fatal("expected OOM notice to reach store.GetChannel")
	}

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

// TestServeResumesPendingMessages exercises the startup-recovery block that
// broadcasts messages.processed for stale running rows and spawns ResumeChannel
// goroutines for channels with still-pending triggered rows. We swap the
// default ResetStaleRunningMessages / ListPendingChannels mocks (set up as
// .Maybe() with nil returns in setupServeMocks) for explicit ones that return
// representative data.
func (s *MainSuite) TestServeResumesPendingMessages() {
	m := s.setupServeMocks()
	m.setupHappyBot()
	// Replace the default .Maybe nil mocks with explicit ones returning data.
	// Testify matches in registration order, so prepending via ExpectedCalls
	// would be needed if we wanted to override. Simpler path: register new
	// expectations that match BEFORE the SetupTest .Maybe (which won't run
	// because the new ones consume the call). We tag .Once() to make this
	// crystal clear in the suite output.
	m.store.ExpectedCalls = filterMockCalls(m.store.ExpectedCalls, "ResetStaleRunningMessages", "ListPendingChannels")
	m.store.On("ResetStaleRunningMessages", mock.Anything).Return([]db.StaleRunningMessage{
		{ChannelID: "ch-1", MsgID: "msg-a"},
		{ChannelID: "ch-1", MsgID: "msg-b"},
		{ChannelID: "ch-2", MsgID: "msg-c"},
		{ChannelID: "ch-skip", MsgID: ""}, // empty msg_id is filtered out
	}, nil).Once()
	// Returning a pending channel kicks off orch.ResumeChannel — that goroutine
	// calls ClaimNextPending; allow it to no-op cleanly with Maybe.
	m.store.On("ListPendingChannels", mock.Anything).Return([]string{"ch-pending"}, nil).Once()
	m.store.On("ClaimNextPending", mock.Anything, "ch-pending").Return(nil, nil).Maybe()

	errCh := make(chan error, 1)
	go func() { errCh <- s.app.serve() }()
	s.waitForServeReady(errCh)
	p, err := os.FindProcess(os.Getpid())
	require.NoError(s.T(), err)
	require.NoError(s.T(), p.Signal(syscall.SIGINT))

	select {
	case err := <-errCh:
		require.NoError(s.T(), err)
	case <-time.After(5 * time.Second):
		s.T().Fatal("serve() did not return in time")
	}
	m.store.AssertCalled(s.T(), "ResetStaleRunningMessages", mock.Anything)
	m.store.AssertCalled(s.T(), "ListPendingChannels", mock.Anything)
}

// TestServeStartupRecoveryErrors covers the error branches: both
// ResetStaleRunningMessages and ListPendingChannels return errors which serve()
// logs and continues past.
func (s *MainSuite) TestServeStartupRecoveryErrors() {
	m := s.setupServeMocks()
	m.setupHappyBot()
	m.store.ExpectedCalls = filterMockCalls(m.store.ExpectedCalls, "ResetStaleRunningMessages", "ListPendingChannels")
	m.store.On("ResetStaleRunningMessages", mock.Anything).Return(nil, errors.New("db gone")).Once()
	m.store.On("ListPendingChannels", mock.Anything).Return(nil, errors.New("read failed")).Once()

	errCh := make(chan error, 1)
	go func() { errCh <- s.app.serve() }()
	s.waitForServeReady(errCh)
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

// filterMockCalls returns a copy of calls without entries whose Method is in
// drop. Used to swap out .Maybe() expectations set by setupServeMocks with
// per-test expectations.
func filterMockCalls(calls []*mock.Call, drop ...string) []*mock.Call {
	dropSet := make(map[string]struct{}, len(drop))
	for _, d := range drop {
		dropSet[d] = struct{}{}
	}
	out := calls[:0:len(calls)]
	for _, c := range calls {
		if _, skip := dropSet[c.Method]; skip {
			continue
		}
		out = append(out, c)
	}
	return out
}

// TestServeReviewPromptResolveError covers the warn-and-fallback branch when
// ResolvePrompt returns an error (both inline + path set is mutually exclusive).
// serve() should log a warning and continue with an empty prompt rather than
// fail.
func (s *MainSuite) TestServeReviewPromptResolveError() {
	m := s.setupServeMocks()
	m.cfg.Review = config.ReviewConfig{Prompt: "inline", PromptPath: "file.txt"}
	m.setupHappyBot()

	errCh := make(chan error, 1)
	go func() { errCh <- s.app.serve() }()
	s.waitForServeReady(errCh)
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

	s.waitForServeReady(errCh)
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

	s.waitForServeReady(errCh)
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

	s.waitForServeReady(errCh)
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
	innerClient := new(mockDockerClient)
	innerClient.On("LatestClaudeVersion").Return("1.0.0").Maybe()
	innerClient.On("ListContainerInfos", mock.Anything).Return([]*container.ContainerInfo{}, nil).Maybe()
	innerClient.On("OOMEvents", mock.Anything).
		Return((<-chan events.Message)(nil), (<-chan error)(nil)).Maybe()
	s.app.newDockerClient = func() (container.DockerClient, error) {
		return &closableDockerClient{
			mockDockerClient: innerClient,
			closeFn:          func() error { closeCalled = true; return nil },
		}, nil
	}

	err := s.app.serve()
	require.Error(s.T(), err)
	require.True(s.T(), closeCalled, "docker client Close() should be called via io.Closer")
}

// approverMockBot wraps mockBot and advertises the SetApprovalResolver and
// SetGateBroadcaster shapes that serve() type-asserts when cfg.Gates.Agentgate.Enabled
// is true. Counts non-nil calls so tests can assert wiring happened.
type approverMockBot struct {
	*mockBot
	approvalResolverSet atomic.Int32
	gateBroadcasterSet  atomic.Int32
}

func (a *approverMockBot) SetApprovalResolver(r bot.ApprovalResolver) {
	if r != nil {
		a.approvalResolverSet.Add(1)
	}
}

func (a *approverMockBot) SetGateBroadcaster(g local.GateBroadcaster) {
	if g != nil {
		a.gateBroadcasterSet.Add(1)
	}
}

func (s *MainSuite) TestServeGateEnabledWiresApprovalResolverAndBroadcaster() {
	m := s.setupServeMocks()
	m.cfg.Gates.Agentgate.Enabled = true
	m.setupHappyBot()

	approver := &approverMockBot{mockBot: m.bot}
	s.app.newDiscordBot = func(_, _, _ string, _ *slog.Logger) (orchestrator.Bot, error) { return approver, nil }
	s.app.newLocalBot = func(_ db.Store, _ *slog.Logger) orchestrator.Bot { return approver }

	errCh := make(chan error, 1)
	go func() { errCh <- s.app.serve() }()

	s.waitForServeReady(errCh)
	p, err := os.FindProcess(os.Getpid())
	require.NoError(s.T(), err)
	require.NoError(s.T(), p.Signal(syscall.SIGINT))

	select {
	case err := <-errCh:
		require.NoError(s.T(), err)
	case <-time.After(5 * time.Second):
		s.T().Fatal("serve() did not return in time")
	}

	require.GreaterOrEqual(s.T(), int(approver.approvalResolverSet.Load()), 1,
		"SetApprovalResolver should be called on bots implementing it when gate is enabled")
	require.GreaterOrEqual(s.T(), int(approver.gateBroadcasterSet.Load()), 1,
		"SetGateBroadcaster should be called on localBot when gate is enabled")
}

// TestServePolicyDirMkdirError covers the failure branch when serve() cannot
// create the per-container policy directory under cfg.LoopDir. Putting LoopDir
// under a regular file makes os.MkdirAll fail with ENOTDIR.
func (s *MainSuite) TestServePolicyDirMkdirError() {
	m := s.setupServeMocks()
	m.cfg.Gates.Agentgate.Enabled = true
	m.setupHappyBot()

	// A regular file cannot contain subdirectories; LoopDir/run MkdirAll must fail.
	tmp := s.T().TempDir()
	blocker := filepath.Join(tmp, "blocker")
	require.NoError(s.T(), os.WriteFile(blocker, []byte("x"), 0o600))
	m.cfg.LoopDir = blocker

	err := s.app.serve()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "creating policy dir")
}

func (s *MainSuite) TestServeGateEnabledBotWithoutSettersIsIgnored() {
	// Bots that don't implement SetApprovalResolver / SetGateBroadcaster
	// (plain mockBot) must be skipped without panic when gate is enabled.
	m := s.setupServeMocks()
	m.cfg.Gates.Agentgate.Enabled = true
	m.setupHappyBot()

	errCh := make(chan error, 1)
	go func() { errCh <- s.app.serve() }()

	s.waitForServeReady(errCh)
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

// useRealStore swaps newSQLiteStore for a real in-memory db.SQLiteStore so
// the WriterDB() type-assertion returns a non-nil *sql.DB. This is needed
// to cover serve()'s fs-migration block and the quality engine wiring,
// both of which are gated on a real writer DB.
//
// Sets cfg.LoopDir to a per-test temp dir if the caller hasn't already —
// fsmigrate writes container/ files under LoopDir, and an empty LoopDir
// would land them in the current working directory.
func (s *MainSuite) useRealStore(m *serveMocks) *db.SQLiteStore {
	store, err := db.NewSQLiteStore(":memory:")
	require.NoError(s.T(), err)
	s.app.newSQLiteStore = func(_ string) (db.Store, error) { return store, nil }
	if m.cfg.LoopDir == "" {
		m.cfg.LoopDir = s.T().TempDir()
	}
	// Replace the MockStore in m so AssertExpectations doesn't trigger.
	m.store = nil
	return store
}

// TestServeWithRealStoreCoversFsMigrateAndQualityWiring exercises the two
// blocks gated on `WriterDB() != nil`: fs migration and the quality
// engine assembly. A real in-memory SQLiteStore satisfies both branches.
func (s *MainSuite) TestServeWithRealStoreCoversFsMigrateAndQualityWiring() {
	m := s.setupServeMocks()
	m.setupHappyBot()
	store := s.useRealStore(m)
	defer store.Close()

	errCh := make(chan error, 1)
	go func() { errCh <- s.app.serve() }()

	s.waitForServeReady(errCh)
	p, err := os.FindProcess(os.Getpid())
	require.NoError(s.T(), err)
	require.NoError(s.T(), p.Signal(syscall.SIGINT))

	select {
	case err := <-errCh:
		require.NoError(s.T(), err)
	case <-time.After(10 * time.Second):
		s.T().Fatal("serve() did not return in time")
	}
}

// TestServeWithQualityRulesOverrideCoversSetRulesConfig exercises the
// `if rcfg := buildRulesConfig(...); rcfg != nil` branch in the quality
// wiring — only fires when the project supplied at least one rule override.
func (s *MainSuite) TestServeWithQualityRulesOverrideCoversSetRulesConfig() {
	m := s.setupServeMocks()
	m.setupHappyBot()
	m.cfg.Quality.Rules = map[string]config.QualityRuleConfig{
		"signal_floor": {Enabled: true, Threshold: 6500},
	}
	store := s.useRealStore(m)
	defer store.Close()

	errCh := make(chan error, 1)
	go func() { errCh <- s.app.serve() }()

	s.waitForServeReady(errCh)
	p, err := os.FindProcess(os.Getpid())
	require.NoError(s.T(), err)
	require.NoError(s.T(), p.Signal(syscall.SIGINT))

	select {
	case err := <-errCh:
		require.NoError(s.T(), err)
	case <-time.After(10 * time.Second):
		s.T().Fatal("serve() did not return in time")
	}
}

// TestServeFsMigrateError covers the early-return when fs migrations fail.
// A real store makes WriterDB() non-nil so the block is entered; the
// injected fsMigrateRun error then trips the wrapped-error return.
func (s *MainSuite) TestServeFsMigrateError() {
	m := s.setupServeMocks()
	store := s.useRealStore(m)
	defer store.Close()
	s.app.fsMigrateRun = func(_ context.Context, _ *sql.DB, _ *fsmigrate.Ctx) error {
		return errors.New("fs migrate boom")
	}

	err := s.app.serve()
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "running fs migrations")
	require.Contains(s.T(), err.Error(), "fs migrate boom")
}

// TestServeQualityParserInitErrorIsLogged covers the "parser init failed"
// warning branch in the quality wiring. serve() must continue past the
// error (panel just stays empty) so we still need a happy-bot setup +
// SIGINT shutdown to round-trip.
func (s *MainSuite) TestServeQualityParserInitErrorIsLogged() {
	m := s.setupServeMocks()
	m.setupHappyBot()
	store := s.useRealStore(m)
	defer store.Close()
	s.app.newQualityParser = func() (parser.Parser, error) {
		return nil, errors.New("grammar load failed")
	}

	errCh := make(chan error, 1)
	go func() { errCh <- s.app.serve() }()

	s.waitForServeReady(errCh)
	p, err := os.FindProcess(os.Getpid())
	require.NoError(s.T(), err)
	require.NoError(s.T(), p.Signal(syscall.SIGINT))

	select {
	case err := <-errCh:
		require.NoError(s.T(), err)
	case <-time.After(10 * time.Second):
		s.T().Fatal("serve() did not return in time")
	}
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
