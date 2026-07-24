package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"database/sql"

	"github.com/spf13/cobra"
	"github.com/tailscale/hujson"

	"github.com/radutopala/loop/internal/agentgate"
	"github.com/radutopala/loop/internal/agentregistry"
	"github.com/radutopala/loop/internal/api"
	"github.com/radutopala/loop/internal/bot"
	"github.com/radutopala/loop/internal/browser"
	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/container"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/embeddings"
	"github.com/radutopala/loop/internal/events"
	"github.com/radutopala/loop/internal/fsmigrate"
	"github.com/radutopala/loop/internal/githubapi"
	"github.com/radutopala/loop/internal/local"
	"github.com/radutopala/loop/internal/logging"
	"github.com/radutopala/loop/internal/memory"
	"github.com/radutopala/loop/internal/orchestrator"
	"github.com/radutopala/loop/internal/osutil"
	"github.com/radutopala/loop/internal/quality/engine"
	"github.com/radutopala/loop/internal/quality/evolution"
	"github.com/radutopala/loop/internal/quality/graph"
	"github.com/radutopala/loop/internal/quality/metrics"
	"github.com/radutopala/loop/internal/quality/rules"
	"github.com/radutopala/loop/internal/quality/snapshot"
	"github.com/radutopala/loop/internal/review"
	"github.com/radutopala/loop/internal/scheduler"
	"github.com/radutopala/loop/internal/terminal"
	"github.com/radutopala/loop/internal/tunnel"
	"github.com/radutopala/loop/internal/types"
	"github.com/radutopala/loop/internal/workflow"
	"github.com/radutopala/loop/internal/worktree"
)

func (a *app) newServeCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "serve",
		Aliases: []string{"s"},
		Short:   "Start the bot",
		RunE: func(_ *cobra.Command, _ []string) error {
			return a.serve()
		},
	}
}

func (a *app) ensureImageAsync(ctx context.Context, client container.DockerClient, cfg *config.Config, hub *api.EventsHub, mgr *container.ImageLifecycleManager, logger *slog.Logger) {
	go a.ensureImageWithBroadcast(ctx, client, cfg, hub, mgr, logger)
}

func (a *app) ensureImageWithBroadcast(ctx context.Context, client container.DockerClient, cfg *config.Config, hub *api.EventsHub, mgr *container.ImageLifecycleManager, logger *slog.Logger) {
	mgr.SetStatus(container.ImageBuildStatus{State: "building", Phase: "checking"})
	hub.BroadcastImageBuildStatus(events.ImageBuildStatusData{State: "building", Phase: "checking"})
	logger.Info("ensuring agent image", "image", cfg.ContainerImage)
	if err := a.ensureImage(ctx, client, cfg); err != nil {
		logger.Error("ensuring agent image failed", "error", err)
		mgr.SetStatus(container.ImageBuildStatus{State: "failed", Error: err.Error()})
		hub.BroadcastImageBuildStatus(events.ImageBuildStatusData{State: "failed", Error: err.Error()})
	} else {
		mgr.SetStatus(container.ImageBuildStatus{State: "completed"})
		hub.BroadcastImageBuildStatus(events.ImageBuildStatusData{State: "completed"})
		mgr.RebuildChildren(ctx)
		logger.Info("agent image ready", "image", cfg.ContainerImage)
	}
	go mgr.RunUpdateChecker(ctx, 30*time.Minute)
}

func (a *app) defaultEnsureImage(ctx context.Context, client container.DockerClient, cfg *config.Config) error {
	// container/ files are populated by fsmigrate.Run earlier in serve(),
	// so we only need to manage the docker images here.
	containerDir := filepath.Join(cfg.LoopDir, "container")

	// Build agent image if missing or if Loop version changed.
	ids, err := client.ImageList(ctx, cfg.ContainerImage)
	if err != nil {
		return fmt.Errorf("listing images: %w", err)
	}
	needsBuild := len(ids) == 0
	if !needsBuild && a.version != "" && a.version != "dev" && !strings.Contains(a.version, "-g") && !strings.Contains(a.version, "-dirty") {
		if labels, err := client.ImageInspectLabels(ctx, cfg.ContainerImage); err == nil && labels != nil {
			if imgVersion := labels["loop.version"]; imgVersion != "" && imgVersion != a.version {
				needsBuild = true
			}
		}
	}
	built := false
	if needsBuild {
		if err := client.ImageBuild(ctx, containerDir, cfg.ContainerImage); err != nil {
			return err
		}
		built = true
	}

	// Build chrome image if missing
	chromeIDs, err := client.ImageList(ctx, cfg.Browser.ChromeImage)
	if err != nil {
		return fmt.Errorf("listing chrome images: %w", err)
	}
	if len(chromeIDs) == 0 {
		if err := client.ImageBuildFile(ctx, containerDir, "chrome.Dockerfile", cfg.Browser.ChromeImage); err != nil {
			return err
		}
		built = true
	}

	// Drop BuildKit cache entries not used in the last 30 days. Loop's
	// images get rebuilt on every `make restart` and on cold daemon
	// starts; without periodic eviction the build cache grew to 100GB+
	// in real-world installs. Logged-and-ignored on failure — a stale
	// cache is preferable to a failed startup.
	if built {
		if _, err := client.PruneBuildCache(ctx, 30*24*time.Hour); err != nil {
			slog.Warn("build cache prune failed", "error", err)
		}
	}

	return nil
}

func (a *app) defaultNewEmbedder(cfg *config.Config) (embeddings.Embedder, error) {
	switch cfg.Memory.Embeddings.Provider {
	case "ollama":
		opts := []embeddings.OllamaOption{
			embeddings.WithOllamaURL(cfg.Memory.Embeddings.OllamaURL),
			embeddings.WithOllamaLoopDir(cfg.LoopDir),
		}
		if cfg.Memory.Embeddings.Model != "" {
			opts = append(opts, embeddings.WithOllamaModel(cfg.Memory.Embeddings.Model))
		}
		return embeddings.NewOllamaEmbedder(opts...), nil
	default:
		return nil, fmt.Errorf("unsupported embeddings provider: %q", cfg.Memory.Embeddings.Provider)
	}
}

type memIndexer interface {
	Index(ctx context.Context, memoryPath, dirPath string, excludePaths []string) (int, error)
	Search(ctx context.Context, dirPath, query string, topK int) ([]memory.SearchResult, error)
}

type memoryPathEntry struct {
	path   string
	global bool
}

type multiDirIndexer struct {
	indexer           memIndexer
	logger            *slog.Logger
	globalMemoryPaths []string
	app               *app
}

func (m *multiDirIndexer) Search(ctx context.Context, dirPath, query string, topK int) ([]memory.SearchResult, error) {
	entries, excludePaths := m.resolveMemoryPaths(dirPath)
	for _, e := range entries {
		scope := dirPath
		if e.global {
			scope = ""
		}
		if _, err := m.indexer.Index(ctx, e.path, scope, excludePaths); err != nil {
			m.logger.Warn("memory index error", "path", e.path, "error", err)
		}
	}
	return m.indexer.Search(ctx, dirPath, query, topK)
}

func (m *multiDirIndexer) Index(ctx context.Context, dirPath string) (int, error) {
	entries, excludePaths := m.resolveMemoryPaths(dirPath)
	total := 0
	for _, e := range entries {
		scope := dirPath
		if e.global {
			scope = ""
		}
		n, err := m.indexer.Index(ctx, e.path, scope, excludePaths)
		if err != nil {
			m.logger.Warn("memory index error", "path", e.path, "error", err)
			continue
		}
		total += n
	}
	return total, nil
}

type channelLister interface {
	ListChannels(ctx context.Context) ([]*db.Channel, error)
}

const defaultReindexInterval = 5 * time.Minute

func (m *multiDirIndexer) reindexAll(ctx context.Context, store channelLister) {
	channels, err := store.ListChannels(ctx)
	if err != nil {
		m.logger.Warn("re-index: listing channels", "error", err)
		return
	}
	for _, ch := range channels {
		if ch.DirPath == "" {
			continue
		}
		if ctx.Err() != nil {
			return
		}
		n, _ := m.Index(ctx, ch.DirPath)
		if n > 0 {
			m.logger.Info("re-index", "channel", ch.ChannelID, "dir", ch.DirPath, "chunks", n)
		}
	}
}

func (m *multiDirIndexer) reindexLoop(ctx context.Context, store channelLister, intervalSec int) {
	m.reindexAll(ctx, store)
	m.logger.Info("startup re-index complete")

	interval := defaultReindexInterval
	if intervalSec > 0 {
		interval = time.Duration(intervalSec) * time.Second
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.reindexAll(ctx, store)
		}
	}
}

func (m *multiDirIndexer) resolveMemoryPaths(dirPath string) ([]memoryPathEntry, []string) {
	var entries []memoryPathEntry
	var excludePaths []string

	addPath := func(p string, global bool) {
		if strings.HasPrefix(p, "!") {
			resolved := resolveRelativePath(dirPath, p[1:])
			excludePaths = append(excludePaths, resolved)
			return
		}
		entries = append(entries, memoryPathEntry{
			path:   resolveRelativePath(dirPath, p),
			global: global,
		})
	}

	if memDir, err := m.app.memoryDir(dirPath); err == nil {
		entries = append(entries, memoryPathEntry{path: memDir, global: false})
	}

	// Index Claude Code CLAUDE.md files so agents can search project context.
	if home, err := m.app.sys.UserHomeDir(); err == nil {
		// Global user instructions.
		entries = append(entries, memoryPathEntry{
			path:   filepath.Join(home, ".claude", "CLAUDE.md"),
			global: true,
		})
	}
	// Project-level CLAUDE.md (root and .claude/).
	entries = append(entries,
		memoryPathEntry{path: filepath.Join(dirPath, "CLAUDE.md"), global: false},
		memoryPathEntry{path: filepath.Join(dirPath, ".claude", "CLAUDE.md"), global: false},
	)

	for _, p := range m.globalMemoryPaths {
		addPath(p, filepath.IsAbs(strings.TrimPrefix(p, "!")))
	}
	for _, p := range m.app.loadProjectMemoryPaths(dirPath) {
		addPath(p, filepath.IsAbs(strings.TrimPrefix(p, "!")))
	}

	seen := make(map[string]struct{}, len(entries))
	deduped := entries[:0]
	for _, e := range entries {
		if _, ok := seen[e.path]; !ok {
			seen[e.path] = struct{}{}
			deduped = append(deduped, e)
		}
	}
	return deduped, excludePaths
}

func resolveRelativePath(dirPath, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(dirPath, p)
}

func (a *app) defaultLoadProjectMemoryPaths(dirPath string) []string {
	data, err := a.sys.ReadFile(filepath.Join(dirPath, ".loop", "config.json"))
	if err != nil {
		return nil
	}
	standardJSON, err := hujson.Standardize(data)
	if err != nil {
		return nil
	}
	var pc struct {
		Memory *struct {
			Paths []string `json:"paths"`
		} `json:"memory"`
	}
	_ = json.Unmarshal(standardJSON, &pc)
	if pc.Memory != nil {
		return pc.Memory.Paths
	}
	return nil
}

func (a *app) memoryDir(dirPath string) (string, error) {
	home, err := a.sys.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("getting home directory: %w", err)
	}
	encoded := osutil.EncodeClaudeProjectPath(dirPath)
	return filepath.Join(home, ".claude", "projects", encoded, "memory"), nil
}

func (a *app) serve() error {
	cfg, err := a.configLoad()
	if err != nil {
		return err
	}

	logger := logging.NewLogger(cfg.LogLevel, cfg.LogFormat)
	logger.Info("starting loop", "db_path", cfg.DBPath)

	store, err := a.newSQLiteStore(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer store.Close()

	// Run filesystem migrations once the DB writer is available. State is
	// tracked in the fs_migrations table alongside schema_migrations.
	if w, ok := store.(interface{ WriterDB() *sql.DB }); ok {
		if writer := w.WriterDB(); writer != nil {
			if err := a.fsMigrateRun(context.Background(), writer, &fsmigrate.Ctx{
				Sys:     a.sys,
				LoopDir: cfg.LoopDir,
				Version: a.version,
			}); err != nil {
				return fmt.Errorf("running fs migrations: %w", err)
			}
		}
	}

	localBot := a.newLocalBot(store, logger)

	// Agentgate stage-2 resolver: a single multiplexer that routes approval
	// clicks from any platform (Discord / Slack / Local) to the per-container
	// Manager that created the request. Constructed once, shared by every
	// bot's SetApprovalResolver call below and by the HTTP /api/gate handler.
	var gateResolver *agentgate.MultiManagerResolver
	if cfg.Gates.Agentgate.Enabled || cfg.Gates.DockerProxy.Enabled {
		gateResolver = agentgate.NewMultiManagerResolver()
	}

	bots := make(map[types.Platform]orchestrator.Bot)
	for _, p := range cfg.Platforms {
		switch p {
		case types.PlatformLocal:
			bots[p] = localBot
		case types.PlatformSlack:
			slackBot, slackErr := a.newSlackBot(cfg.SlackBotToken, cfg.SlackAppToken, logger)
			if slackErr != nil {
				return fmt.Errorf("creating slack bot: %w", slackErr)
			}
			bots[p] = slackBot
		case types.PlatformDiscord:
			discordBot, discordErr := a.newDiscordBot(cfg.DiscordToken, cfg.DiscordAppID, cfg.DiscordGuildID, logger)
			if discordErr != nil {
				return fmt.Errorf("creating discord bot: %w", discordErr)
			}
			bots[p] = discordBot
		}
		if gateResolver != nil {
			if r, ok := bots[p].(interface{ SetApprovalResolver(bot.ApprovalResolver) }); ok {
				r.SetApprovalResolver(gateResolver)
			}
		}
	}

	chatBot := orchestrator.NewBotRouter(bots, store, logger)

	dockerClient, err := a.newDockerClient()
	if err != nil {
		return fmt.Errorf("creating docker client: %w", err)
	}
	dockerClient.SetLoopVersion(a.version)
	if closer, ok := dockerClient.(io.Closer); ok {
		defer closer.Close()
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	// One shared mtime-cached reloader backs every hot-reload consumer, so a
	// config edit is still picked up immediately but unchanged files are
	// parsed once instead of on every message/run/request.
	reloadConfig := config.NewCachedReloader().Reload
	runner := container.NewDockerRunner(dockerClient, cfg, reloadConfig)
	// Per-container policy files live under ~/.loop/run/<cid>/ (not
	// /run/loop/<cid>/) because macOS /run is on the read-only system
	// volume. Linux hosts could use /run/loop but we keep one path for both
	// OSes so the bind-mount code paths stay identical.
	policyDir := filepath.Join(cfg.LoopDir, "run")
	if cfg.Gates.Agentgate.Enabled || cfg.Gates.DockerProxy.Enabled {
		if err := os.MkdirAll(policyDir, 0o750); err != nil {
			return fmt.Errorf("creating policy dir: %w", err)
		}
	}
	if gateResolver != nil {
		runner.SetGateDeps(gateResolver, &orchestrator.GateBotRouter{Bot: chatBot}, cfg.Gates.RateLimits)
	}
	runner.SetDockerProxyDeps(policyDir, "")

	// Compile the shared seccomp policy once per daemon. The compiled form
	// stays on the host for sanity checking at startup; the runner writes
	// the JSON form into each container's policyDir and loop-syscallwrap
	// recompiles it inside the container. Compile here primarily to fail
	// fast on a broken user config before any container spawn.
	a.wireGatePolicy(cfg, runner, policyDir, logger)

	agentReg := agentregistry.New()

	executor := orchestrator.NewTaskExecutor(runner, chatBot, store, logger, cfg.ContainerTimeout, reloadConfig)
	executor.SetWorktreeCreator(&worktree.Creator{
		Sys: osutil.RealSystem{},
		Run: worktree.ExecCommandRunner,
	})
	sched := scheduler.NewTaskScheduler(store, executor, cfg.PollInterval, logger)

	channelCreators := make(map[types.Platform]api.ChannelCreator, len(bots))
	for p, b := range bots {
		if cc, ok := b.(api.ChannelCreator); ok {
			channelCreators[p] = cc
		}
	}
	channelSvc := api.NewChannelService(store, channelCreators, cfg.LoopDir)
	threadSvc := api.NewThreadService(store, chatBot, logger, cfg.KeepMCPConfigs)

	containerReg := container.NewRegistry(nil)
	containerReg.SetLogger(logger)
	// Remove via runner so the per-container docker proxy (stage 2 of
	// agentgate) is torn down on the same code path as docker removal.
	// Runner delegates to dockerClient for the actual container remove.
	containerReg.SetContainerRemover(runner)
	containerReg.SetShellCreator(runner)
	runner.SetContainerRegistry(containerReg)

	// Restore registry from running Docker containers (survives daemon restarts).
	// Skip running containers owned by a different daemon instance — they are
	// actively managed by another daemon sharing the same Docker socket.
	// Stopped containers from any instance are restored (orphaned after crash/restart).
	if infos, restoreErr := dockerClient.ListContainerInfos(ctx); restoreErr != nil {
		logger.Warn("failed to restore container registry", "error", restoreErr)
	} else if len(infos) > 0 {
		instanceID := runner.InstanceID()
		var ownInfos []*container.ContainerInfo
		for _, info := range infos {
			foreignRunning := info.InstanceID != "" && info.InstanceID != instanceID && info.Status == container.ContainerStatusRunning
			if !foreignRunning {
				ownInfos = append(ownInfos, info)
			}
		}
		containerReg.Restore(ownInfos)
		logger.Info("restored container registry", "count", len(ownInfos))
		// Schedule removal for containers that are already stopped.
		for _, info := range ownInfos {
			if info.Status == container.ContainerStatusStopped {
				containerReg.ScheduleRemove(info.ContainerID, cfg.ContainerKeepAlive)
			}
		}
	}

	// Stage-2 domain dependencies are constructed up front and injected at
	// server construction via options — none of them need the server; only
	// the engine's progress hook flows the other way and is wired after.
	ghClient := githubapi.NewClient()
	reviewPrompt, reviewErr := cfg.Review.ResolvePrompt(cfg.LoopDir, os.ReadFile)
	if reviewErr != nil {
		logger.Warn("review prompt resolve failed, using built-in default", "error", reviewErr)
		reviewPrompt = ""
	}
	serverOpts := []api.Option{
		api.WithGitHubLookup(ghClient),
		api.WithTunnel(tunnel.NewManager(filepath.Join(cfg.LoopDir, "bin"), logger)),
		api.WithReview(ghClient, review.NewStore(), &review.GitPR{Run: worktree.ExecCommandRunner}),
		api.WithReviewAgent(&review.Runner{Agent: runner}, "", reviewPrompt),
		// Ceiling for the daemon-side review goroutine. Picked below the
		// CLI's `loop review run --timeout` default (60m) so the daemon
		// flips the session to status=error first and the CLI surfaces the
		// daemon's "timed out" message instead of its own generic wrapper.
		api.WithReviewRunTimeout(50 * time.Minute),
	}

	// Quality engine: parser + graph cache + SQL-backed snapshot store. The
	// HTTP handlers stay 501 unless all deps are wired; CLI (`loop quality
	// scan`) uses its own ephemeral instances. Scans are manual or
	// agent-triggered (via the MCP `scan` tool); there is no live-rescan loop.
	var qEngine *engine.Engine
	if w, ok := store.(interface{ WriterDB() *sql.DB }); ok && w.WriterDB() != nil {
		qParser, qErr := a.newQualityParser()
		if qErr != nil {
			logger.Warn("quality engine disabled: parser init failed", "error", qErr)
		} else {
			qCache := graph.NewCache()
			qStore := snapshot.NewSQLStore(w.WriterDB())
			qEngine = engine.New(qParser, qStore, qCache, engine.OSFileSystem{}, engine.Config{
				MaxFiles:     cfg.Quality.MaxFiles,
				ExcludePaths: cfg.Quality.ExcludePaths,
				Metrics:      buildMetricsConfig(cfg.Quality),
			}, qualityConfigLoader(cfg, reloadConfig), nil)
			serverOpts = append(serverOpts, api.WithQuality(api.QualityDeps{
				Scanner:       qEngine,
				Graph:         qCache,
				Snapshots:     qStore,
				RulesLoader:   qualityRulesLoader(cfg, reloadConfig),
				MetricsLoader: qualityMetricsLoader(cfg, reloadConfig),
				History:       evolution.NewExecReader(),
			}))
		}
	} else {
		logger.Warn("quality engine disabled: store does not expose WriterDB")
	}

	if cfg.Browser.Enabled {
		dockerProvider, browserErr := a.newBrowserProvider(cfg.Browser.ChromeImage, logger)
		if browserErr != nil {
			logger.Warn("browser docker provider unavailable", "error", browserErr)
		} else if dp, ok := dockerProvider.(*browser.DockerProvider); ok {
			dp.SetContainerRegistry(containerReg)
		}

		// Always initialize host browser provider so the UI pill can switch to it.
		hostProvider := browser.NewHostProvider(cfg.Browser.HostCDPPort, logger)

		serverOpts = append(serverOpts,
			api.WithBrowserProviders(dockerProvider, hostProvider),
			api.WithBrowserKeepAlive(cfg.ContainerKeepAlive),
		)
	}

	screenshotDir := filepath.Join(cfg.LoopDir, "screenshots")
	_ = os.MkdirAll(screenshotDir, 0o755)
	serverOpts = append(serverOpts, api.WithScreenshotDir(screenshotDir))

	apiSrv := a.newAPIServer(sched, channelSvc, threadSvc, store, chatBot, logger, serverOpts...)
	apiSrv.SetLoopDir(cfg.LoopDir)
	if qEngine != nil {
		qEngine.SetProgress(apiSrv.EmitQualityProgress)
	}
	apiSrv.SetContainerRegistry(containerReg)
	apiSrv.SetAgentRegistry(agentReg)
	apiSrv.SetAuditDirResolver(runner)
	if gateResolver != nil {
		apiSrv.SetApprovalResolver(gateResolver)
		apiSrv.SetContainerApprovalRouter(containerApprovalAdapter{gateResolver})
		apiSrv.SetPendingApprovalLister(gateResolver)
	}

	execClient, err := a.newDockerExecClient()
	if err != nil {
		logger.Warn("terminal manager unavailable", "error", err)
	} else {
		termMgr := terminal.NewManager(execClient, logger)
		apiSrv.SetTerminalManager(terminal.NewManagerAdapter(termMgr))
		apiSrv.SetInteractiveCmdBuilder(container.NewClaudeCmdBuilder(cfg, reloadConfig))
	}

	hostExecClient := a.newHostExecClient()
	hostTermMgr := terminal.NewManager(hostExecClient, logger)
	apiSrv.SetHostTerminalManager(terminal.NewManagerAdapter(hostTermMgr))

	if cfg.Browser.Enabled {
		// Idle monitoring for browser sessions (CDPManagers + containers).
		go apiSrv.RunBrowserIdleMonitor(ctx, 5*time.Minute)
	}

	if cfg.Memory.Enabled {
		emb, embErr := a.newEmbedder(cfg)
		if embErr != nil {
			logger.Warn("skipping embeddings", "error", embErr)
		} else {
			indexer := memory.NewIndexer(emb, store, logger, cfg.Memory.MaxChunkChars)
			mdi := &multiDirIndexer{indexer: indexer, logger: logger, globalMemoryPaths: cfg.Memory.Paths, app: a}
			apiSrv.SetMemoryIndexer(mdi)
			if ollamaEmb, ok := emb.(*embeddings.OllamaEmbedder); ok {
				go ollamaEmb.RunIdleMonitor(ctx)
			}
			go mdi.reindexLoop(ctx, store, cfg.Memory.ReindexIntervalSec)
		}
	}

	eventsHub := api.NewEventsHub(logger)
	apiSrv.SetEventsHub(eventsHub)
	branchPoller := api.NewBranchPoller(store, eventsHub, cfg.LoopDir, 0, logger)
	apiSrv.SetBranchPoller(branchPoller)
	branchPoller.SetOnDirChange(apiSrv.InvalidatePRCacheForDir)
	go branchPoller.Run(ctx)
	containerReg.SetBroadcaster(eventsHub)
	if gateResolver != nil {
		if gb, ok := localBot.(interface {
			SetGateBroadcaster(local.GateBroadcaster)
		}); ok {
			gb.SetGateBroadcaster(eventsHub)
		}
	}

	if streamer, ok := dockerClient.(container.OOMEventStreamer); ok {
		watcher := container.NewOOMWatcher(streamer.OOMEvents, func(noticeCtx context.Context, channelID, content string) {
			orchestrator.StoreSystemNotice(noticeCtx, store, eventsHub, channelID, content)
		}, logger)
		go watcher.Run(ctx)
	}

	containerDir := filepath.Join(cfg.LoopDir, "container")
	lifecycleMgr := container.NewImageLifecycleManager(
		dockerClient, eventsHub, a.sys, logger,
		containerDir, cfg.ContainerImage, a.version,
		dockerClient.LatestClaudeVersion,
	)
	lifecycleMgr.SetContainerRegistry(containerReg)
	apiSrv.SetImageManager(lifecycleMgr)

	// Child-image cascade: projects overriding container_image with a
	// .loop/container/Dockerfile FROM the agent image get rebuilt whenever
	// the base image is (re)built, so they never linger on an old base.
	childImages := container.NewChildImageManager(dockerClient, cfg.ContainerImage,
		childProjectsLister(store, cfg, config.LoadProjectConfig), logger)
	lifecycleMgr.SetChildRebuilder(childImages.RebuildStale)

	// Ensure agent image asynchronously so the API is available during builds.
	a.ensureImageAsync(ctx, dockerClient, cfg, eventsHub, lifecycleMgr, logger)

	executor.SetEventBroadcaster(eventsHub)
	sched.SetEventBroadcaster(eventsHub)

	// Workflow engine: uses the same runner for prompt nodes; for bash nodes,
	// optionally fall back to local shell execution when Docker is unavailable.
	var bashRunner workflow.BashRunner = runner
	if cfg.WorkflowBashLocal {
		bashRunner = &workflow.LocalBashRunner{SafeDir: cfg.LoopDir, APIURL: localAPIURL(cfg.APIAddr)}
		logger.Info("workflow bash nodes will execute locally (workflow_bash_local=true)")
	}
	wfEngine := workflow.NewEngine(store, runner, bashRunner, eventsHub, workflowsFromConfig(cfg, reloadConfig), cfg.LoopDir, cfg.WorkflowConcurrency, logger)
	if err := wfEngine.RecoverRuns(ctx); err != nil {
		logger.Error("failed to recover workflow runs", "error", err)
	}
	apiSrv.SetWorkflowEngine(wfEngine)
	executor.SetWorkflowEngine(wfEngine)

	orch := orchestrator.New(store, chatBot, runner, sched, logger, *cfg, reloadConfig)
	orch.SetEventBroadcaster(eventsHub)
	orch.SetWorkflowEngine(wfEngine)
	executor.SetActiveRuns(orch.ActiveRunsMap())
	apiSrv.SetIncomingMessageHandler(chatBot)
	apiSrv.SetRunCanceller(orch)
	apiSrv.SetPlanResolver(orch)
	apiSrv.SetAskResolver(orch)
	if statsClient, ok := dockerClient.(api.ContainerStatsFetcher); ok {
		apiSrv.SetContainerStatsFetcher(statsClient)
	}
	apiSrv.SetPendingAsksLister(orch)
	apiSrv.SetPendingPlansLister(orch)
	apiSrv.SetInteractionHandler(orch)
	apiSrv.SetActiveChatLister(orch)

	// Restore persisted ask/plan card parks BEFORE the API server begins
	// serving, so GET /api/plans/pending and /api/asks/pending never hand a
	// freshly-(re)connected renderer an empty snapshot. Restarting the desktop
	// app also restarts the daemon, so the renderer's card rehydrate races
	// daemon startup: if the API answered before RestoreParkedChannels populated
	// the in-memory park maps, the parked channel showed no card (leaving it
	// unapprovable) until the next reconnect. Wiring every apiSrv handler above
	// before Start closes the same startup window for the other endpoints too.
	orch.RestoreParkedChannels(ctx)

	if err := apiSrv.Start(cfg.APIAddr); err != nil {
		return fmt.Errorf("starting api server: %w", err)
	}

	// Start the bot + scheduler only after the API is listening so any run they
	// trigger (a due scheduled task, an inbound message) can call back into the
	// loop API.
	if err := orch.Start(ctx); err != nil {
		_ = apiSrv.Stop(context.Background())
		return fmt.Errorf("starting orchestrator: %w", err)
	}

	// Resume DB-queued messages from the prior daemon run: clear stale
	// is_running rows (their agent runs cannot survive a restart), then
	// kick off a drain per channel that still has unprocessed triggered rows.
	if stale, err := store.ResetStaleRunningMessages(ctx); err != nil {
		logger.Error("resetting stale running messages", "error", err)
	} else if len(stale) > 0 {
		logger.Warn("reset stale running messages from prior daemon run", "count", len(stale))
		if eventsHub != nil {
			byChan := make(map[string][]string, len(stale))
			for _, rec := range stale {
				if rec.MsgID == "" {
					continue
				}
				byChan[rec.ChannelID] = append(byChan[rec.ChannelID], rec.MsgID)
			}
			for ch, ids := range byChan {
				eventsHub.BroadcastMessagesProcessed(ch, events.MessagesProcessedData{MsgIDs: ids})
			}
		}
	}
	if pending, err := store.ListPendingChannels(ctx); err != nil {
		logger.Error("listing pending channels", "error", err)
	} else {
		for _, ch := range pending {
			go orch.ResumeChannel(ctx, ch)
		}
	}

	// Periodic registry reconciliation: detect containers that disappeared
	// externally (OOM kill, docker rm, daemon restart) and remove stale entries.
	go containerReg.RunReconcileLoop(ctx, dockerClient, 30*time.Second, cfg.ContainerKeepAlive, logger)

	// Signal readiness: API server, orchestrator, and signal handler are wired.
	// Tests block on this channel before sending SIGINT to avoid timing flakes.
	close(a.serveReady)

	<-ctx.Done()
	logger.Info("shutting down")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := apiSrv.Stop(shutdownCtx); err != nil {
		slog.Error("api server stop error", "error", err)
	}

	// Clean up Chrome sidecar containers.
	apiSrv.CleanupBrowsers(shutdownCtx)

	if err := orch.Stop(); err != nil {
		slog.Error("shutdown error", "error", err)
	}

	return nil
}

// buildRulesConfig overlays project-config rule overrides on top of the
// rules-engine defaults. Returns nil when the user hasn't configured any
// rule overrides — the api server then falls through to rules.DefaultConfig
// at evaluation time, keeping behaviour identical to the unconfigured case.
func buildRulesConfig(overrides map[string]config.QualityRuleConfig) *rules.Config {
	if len(overrides) == 0 {
		return nil
	}
	cfg := rules.DefaultConfig()
	for name, override := range overrides {
		rc, ok := cfg.Rules[name]
		if !ok {
			continue
		}
		rc.Enabled = override.Enabled
		if override.Threshold > 0 {
			rc.Threshold = override.Threshold
		}
		cfg.Rules[name] = rc
	}
	return &cfg
}

// wireGatePolicy compiles the shared seccomp policy and hands it to the
// runner. Compilation happens once at startup so a broken user config fails
// fast; the runner serialises the policy to JSON per-container and
// loop-syscallwrap recompiles it inside the container before installing the
// filter. Compile failure logs a warning and disables the gate (runner leaves
// LOOP_GATE_ENABLED unset, entrypoint.sh skips loop-syscallwrap).
func (a *app) wireGatePolicy(
	cfg *config.Config,
	runner *container.DockerRunner,
	policyDir string,
	logger *slog.Logger,
) {
	if !cfg.Gates.Agentgate.Enabled {
		return
	}
	policy, err := agentgate.CompilePolicy(
		cfg.Gates.Agentgate.DefaultDecision,
		cfg.Gates.Agentgate.PathRules,
		cfg.Gates.Agentgate.CommandRules,
		cfg.Gates.Agentgate.FileRules,
	)
	if err != nil {
		logger.Warn("gate policy compile failed; gate disabled", "error", err)
		return
	}
	runner.SetGatePolicy(policy, policyDir)
}

// mergedProjectConfig resolves the effective config for a scan, layering
// global → [parent →] project. dirPath="" falls through to global;
// parentDirPath !="" triggers the three-layer worktree merge. Reload
// errors fall back silently to the initial cfg; project-load errors
// propagate to the caller.
func mergedProjectConfig(
	cfg *config.Config,
	reload func() (*config.Config, error),
	dirPath, parentDirPath string,
) (*config.Config, error) {
	base := cfg
	if fresh, err := reload(); err == nil {
		base = fresh
	}
	switch {
	case parentDirPath != "" && dirPath != "":
		return config.LoadWorktreeProjectConfig(dirPath, parentDirPath, base)
	case dirPath != "":
		return config.LoadProjectConfig(dirPath, base)
	default:
		return base, nil
	}
}

// qualityConfigLoader returns an engine.ConfigLoader that maps the
// merged project config (global → [parent →] project) into the
// engine-only Config the scanner consumes.
//
// Loader errors propagate to the engine, which falls back to its
// last-known-good Config rather than failing the scan — see
// engine.currentConfig.
func qualityConfigLoader(cfg *config.Config, reload func() (*config.Config, error)) engine.ConfigLoader {
	return func(dirPath, parentDirPath string) (engine.Config, error) {
		merged, err := mergedProjectConfig(cfg, reload, dirPath, parentDirPath)
		if err != nil {
			return engine.Config{}, err
		}
		return engine.Config{
			MaxFiles:     merged.Quality.MaxFiles,
			ExcludePaths: merged.Quality.ExcludePaths,
			Metrics:      buildMetricsConfig(merged.Quality),
		}, nil
	}
}

// buildMetricsConfig overlays project-config complexity / clones
// thresholds on top of metrics.DefaultConfig(). A zero on any field
// means "leave the default in place" — projects can tweak just one
// dimension without restating the rest.
func buildMetricsConfig(qc config.QualityConfig) metrics.Config {
	cfg := metrics.DefaultConfig()
	if qc.Complexity.CyclomaticT > 0 {
		cfg.Complexity.CyclomaticT = qc.Complexity.CyclomaticT
	}
	if qc.Complexity.CognitiveT > 0 {
		cfg.Complexity.CognitiveT = qc.Complexity.CognitiveT
	}
	if qc.Complexity.NestingT > 0 {
		cfg.Complexity.NestingT = qc.Complexity.NestingT
	}
	if qc.Complexity.ParamsT > 0 {
		cfg.Complexity.ParamsT = qc.Complexity.ParamsT
	}
	if qc.Complexity.LOCT > 0 {
		cfg.Complexity.LOCT = qc.Complexity.LOCT
	}
	if qc.Clones.MinLOC > 0 {
		cfg.Clones.MinLOC = qc.Clones.MinLOC
	}
	if qc.Clones.MaxDistance > 0 {
		cfg.Clones.MaxDistance = qc.Clones.MaxDistance
	}
	return cfg
}

// qualityRulesLoader returns a function that resolves the rules.Config
// for a scan, applying project-level overrides on top of the
// rules-engine defaults. Returns nil when neither global nor project
// config sets any rule overrides — callers (collectRules,
// handleQualityRules) treat nil as "use rules.DefaultConfig()".
//
// On project-config load error, falls back to the initial cfg's rule
// overrides rather than failing the scan.
func qualityRulesLoader(cfg *config.Config, reload func() (*config.Config, error)) func(dirPath, parentDirPath string) *rules.Config {
	return func(dirPath, parentDirPath string) *rules.Config {
		merged, err := mergedProjectConfig(cfg, reload, dirPath, parentDirPath)
		if err != nil {
			merged = cfg
		}
		return buildRulesConfig(merged.Quality.Rules)
	}
}

// qualityMetricsLoader returns a function that resolves the
// metrics.Config for the diagnostics handlers (rules, whatif) on the
// cached graph. Mirrors qualityRulesLoader so per-project Complexity /
// Clones threshold overrides reach those endpoints on every request
// without a daemon restart.
//
// On project-config load error, falls back to the initial cfg's
// thresholds rather than failing the request.
func qualityMetricsLoader(cfg *config.Config, reload func() (*config.Config, error)) func(dirPath, parentDirPath string) metrics.Config {
	return func(dirPath, parentDirPath string) metrics.Config {
		merged, err := mergedProjectConfig(cfg, reload, dirPath, parentDirPath)
		if err != nil {
			merged = cfg
		}
		return buildMetricsConfig(merged.Quality)
	}
}

// workflowsFromConfig returns a function that loads workflows from the
// latest config, merging project-level config when dirPath is provided.
// When parentDirPath is set (worktree channels), uses three-layer merging:
// global → parent project → worktree project.
// Falls back to the initial config on reload error.
func workflowsFromConfig(cfg *config.Config, reload func() (*config.Config, error)) func(dirPath, parentDirPath string) []config.WorkflowDef {
	return func(dirPath, parentDirPath string) []config.WorkflowDef {
		base := cfg
		if fresh, err := reload(); err == nil {
			base = fresh
		}
		if parentDirPath != "" && dirPath != "" {
			// Worktree channel: global → parent project → worktree project
			if merged, err := config.LoadWorktreeProjectConfig(dirPath, parentDirPath, base); err == nil {
				return merged.Workflows
			}
		} else if dirPath != "" {
			// Regular channel/thread: global → project
			if merged, err := config.LoadProjectConfig(dirPath, base); err == nil {
				return merged.Workflows
			}
		}
		return base.Workflows
	}
}

// containerApprovalAdapter bridges *agentgate.MultiManagerResolver to
// api.ContainerApprovalRouter. The resolver's ByToken returns *agentgate.Manager
// concretely; the api interface uses a narrower ContainerApprovalManager so test
// doubles can skip the full Manager shape.
type containerApprovalAdapter struct {
	r *agentgate.MultiManagerResolver
}

func (a containerApprovalAdapter) ByToken(token string) (string, api.ContainerApprovalManager, string, bool) {
	cid, mgr, channelID, ok := a.r.ByToken(token)
	if !ok {
		return "", nil, "", false
	}
	return cid, mgr, channelID, true
}

// childProjectsLister resolves the registered projects' container_image
// overrides for the child-image cascade: one entry per distinct non-worktree
// channel dir_path, with the project's merged image + autobuild flag.
// loadProject is injectable for tests (production: config.LoadProjectConfig).
func childProjectsLister(store channelLister, cfg *config.Config, loadProject func(string, *config.Config) (*config.Config, error)) func(ctx context.Context) ([]container.ChildProject, error) {
	return func(ctx context.Context) ([]container.ChildProject, error) {
		chs, err := store.ListChannels(ctx)
		if err != nil {
			return nil, err
		}
		seenDirs := map[string]bool{}
		var out []container.ChildProject
		for _, ch := range chs {
			if ch.DirPath == "" || ch.Worktree || seenDirs[ch.DirPath] {
				continue
			}
			seenDirs[ch.DirPath] = true
			pc, perr := loadProject(ch.DirPath, cfg)
			if perr != nil || pc == nil {
				continue
			}
			out = append(out, container.ChildProject{DirPath: ch.DirPath, Image: pc.ContainerImage, Autobuild: pc.ContainerImageAutobuild})
		}
		return out, nil
	}
}

// localAPIURL turns the daemon's listen address into a URL that a process on
// the same host can call: ":8222" and "0.0.0.0:8222" become
// "http://localhost:8222"; an address with an explicit host is used as-is.
func localAPIURL(apiAddr string) string {
	host, port, err := net.SplitHostPort(apiAddr)
	if err != nil || port == "" {
		return "http://localhost:8222"
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "localhost"
	}
	return "http://" + net.JoinHostPort(host, port)
}
