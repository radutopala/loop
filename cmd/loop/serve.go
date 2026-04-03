package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/tailscale/hujson"

	"github.com/radutopala/loop/internal/agentregistry"
	"github.com/radutopala/loop/internal/api"
	"github.com/radutopala/loop/internal/browser"
	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/container"
	containerimage "github.com/radutopala/loop/internal/container/image"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/embeddings"
	"github.com/radutopala/loop/internal/events"
	"github.com/radutopala/loop/internal/logging"
	"github.com/radutopala/loop/internal/memory"
	"github.com/radutopala/loop/internal/orchestrator"
	"github.com/radutopala/loop/internal/osutil"
	"github.com/radutopala/loop/internal/scheduler"
	"github.com/radutopala/loop/internal/terminal"
	"github.com/radutopala/loop/internal/types"
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
		logger.Info("agent image ready", "image", cfg.ContainerImage)
	}
	go mgr.RunUpdateChecker(ctx, 30*time.Minute)
}

func (a *app) defaultEnsureImage(ctx context.Context, client container.DockerClient, cfg *config.Config) error {
	containerDir := filepath.Join(cfg.LoopDir, "container")

	dockerfilePath := filepath.Join(containerDir, "Dockerfile")
	if _, err := a.sys.Stat(dockerfilePath); os.IsNotExist(err) {
		if err := a.sys.MkdirAll(containerDir, 0755); err != nil {
			return fmt.Errorf("creating container directory: %w", err)
		}
		if err := a.sys.WriteFile(dockerfilePath, containerimage.Dockerfile, 0644); err != nil {
			return fmt.Errorf("writing Dockerfile: %w", err)
		}
		if err := a.sys.WriteFile(filepath.Join(containerDir, "entrypoint.sh"), containerimage.Entrypoint, 0644); err != nil {
			return fmt.Errorf("writing entrypoint: %w", err)
		}
		if err := a.sys.WriteFile(filepath.Join(containerDir, "setup.sh"), containerimage.Setup, 0644); err != nil {
			return fmt.Errorf("writing setup script: %w", err)
		}
	}

	// Ensure chrome.Dockerfile and chrome-entrypoint.sh exist
	chromeDockerfilePath := filepath.Join(containerDir, "chrome.Dockerfile")
	if _, err := a.sys.Stat(chromeDockerfilePath); os.IsNotExist(err) {
		if err := a.sys.WriteFile(chromeDockerfilePath, containerimage.ChromeDockerfile, 0644); err != nil {
			return fmt.Errorf("writing chrome Dockerfile: %w", err)
		}
		if err := a.sys.WriteFile(filepath.Join(containerDir, "chrome-entrypoint.sh"), containerimage.ChromeEntrypoint, 0644); err != nil {
			return fmt.Errorf("writing chrome entrypoint: %w", err)
		}
	}

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
	if needsBuild {
		if err := client.ImageBuild(ctx, containerDir, cfg.ContainerImage); err != nil {
			return err
		}
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

	localBot := a.newLocalBot(store, logger)

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

	runner := container.NewDockerRunner(dockerClient, cfg, config.Reload)

	agentReg := agentregistry.New()

	executor := orchestrator.NewTaskExecutor(runner, chatBot, store, logger, cfg.ContainerTimeout, cfg.StreamingEnabled, config.Reload)
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
	containerReg.SetContainerRemover(dockerClient)
	containerReg.SetShellCreator(runner)
	runner.SetContainerRegistry(containerReg)

	// Restore registry from running Docker containers (survives daemon restarts).
	if infos, restoreErr := dockerClient.ListContainerInfos(ctx); restoreErr != nil {
		logger.Warn("failed to restore container registry", "error", restoreErr)
	} else if len(infos) > 0 {
		containerReg.Restore(infos)
		logger.Info("restored container registry", "count", len(infos))
		// Schedule removal for containers that are already stopped.
		for _, info := range infos {
			if info.Status == container.ContainerStatusStopped {
				containerReg.ScheduleRemove(info.ContainerID, cfg.ContainerKeepAlive)
			}
		}
	}

	apiSrv := a.newAPIServer(sched, channelSvc, threadSvc, store, chatBot, logger)
	apiSrv.SetLoopDir(cfg.LoopDir)
	apiSrv.SetContainerRegistry(containerReg)
	apiSrv.SetAgentRegistry(agentReg)

	execClient, err := a.newDockerExecClient()
	if err != nil {
		logger.Warn("terminal manager unavailable", "error", err)
	} else {
		termMgr := terminal.NewManager(execClient, logger)
		apiSrv.SetTerminalManager(terminal.NewManagerAdapter(termMgr))
		apiSrv.SetInteractiveCmdBuilder(container.NewClaudeCmdBuilder(cfg, config.Reload))
	}

	hostExecClient := a.newHostExecClient()
	hostTermMgr := terminal.NewManager(hostExecClient, logger)
	apiSrv.SetHostTerminalManager(terminal.NewManagerAdapter(hostTermMgr))

	if cfg.Browser.Enabled {
		dockerProvider, browserErr := a.newBrowserProvider(cfg.Browser.ChromeImage, logger)
		if browserErr != nil {
			logger.Warn("browser docker provider unavailable", "error", browserErr)
		} else {
			if dp, ok := dockerProvider.(*browser.DockerProvider); ok {
				dp.SetContainerRegistry(containerReg)
			}
			apiSrv.SetBrowserProvider(dockerProvider)
		}

		// Always initialize host browser provider so the UI pill can switch to it.
		hostProvider := browser.NewHostProvider(cfg.Browser.HostCDPPort, logger)
		apiSrv.SetHostBrowserProvider(hostProvider)

		// Idle monitoring for browser sessions (CDPManagers + containers).
		apiSrv.SetBrowserKeepAlive(cfg.ContainerKeepAlive)
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
	containerReg.SetBroadcaster(eventsHub)

	containerDir := filepath.Join(cfg.LoopDir, "container")
	lifecycleMgr := container.NewImageLifecycleManager(
		dockerClient, eventsHub, a.sys, logger,
		containerDir, cfg.ContainerImage, a.version,
		dockerClient.LatestClaudeVersion,
	)
	lifecycleMgr.SetContainerRegistry(containerReg)
	apiSrv.SetImageManager(lifecycleMgr)

	// Ensure agent image asynchronously so the API is available during builds.
	a.ensureImageAsync(ctx, dockerClient, cfg, eventsHub, lifecycleMgr, logger)

	executor.SetEventBroadcaster(eventsHub)
	sched.SetEventBroadcaster(eventsHub)

	screenshotDir := filepath.Join(cfg.LoopDir, "screenshots")
	_ = os.MkdirAll(screenshotDir, 0o755)
	apiSrv.SetScreenshotDir(screenshotDir)

	if err := apiSrv.Start(cfg.APIAddr); err != nil {
		return fmt.Errorf("starting api server: %w", err)
	}

	orch := orchestrator.New(store, chatBot, runner, sched, logger, *cfg, config.Reload)
	orch.SetEventBroadcaster(eventsHub)
	apiSrv.SetIncomingMessageHandler(chatBot)
	apiSrv.SetInteractionHandler(orch)
	apiSrv.SetActiveChatLister(orch)

	if err := orch.Start(ctx); err != nil {
		_ = apiSrv.Stop(context.Background())
		return fmt.Errorf("starting orchestrator: %w", err)
	}

	// Periodic registry reconciliation: detect containers that disappeared
	// externally (OOM kill, docker rm, daemon restart) and remove stale entries.
	go containerReg.RunReconcileLoop(ctx, dockerClient, 30*time.Second, cfg.ContainerKeepAlive, logger)

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
