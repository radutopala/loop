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

	"github.com/radutopala/loop/internal/api"
	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/container"
	containerimage "github.com/radutopala/loop/internal/container/image"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/embeddings"
	"github.com/radutopala/loop/internal/logging"
	"github.com/radutopala/loop/internal/memory"
	"github.com/radutopala/loop/internal/orchestrator"
	"github.com/radutopala/loop/internal/scheduler"
	"github.com/radutopala/loop/internal/types"
)

func newServeCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "serve",
		Aliases: []string{"s"},
		Short:   "Start the bot",
		RunE: func(_ *cobra.Command, _ []string) error {
			return serve()
		},
	}
}

// apiServer is the interface used by serve() to decouple from api.Server for testing.
type apiServer interface {
	Start(addr string) error
	Stop(ctx context.Context) error
	SetMemoryIndexer(idx api.MemoryIndexer)
	SetLoopDir(dir string)
}

var ensureImage = func(ctx context.Context, client container.DockerClient, cfg *config.Config) error {
	containerDir := filepath.Join(cfg.LoopDir, "container")

	// Write embedded files if they don't exist (first-run fallback)
	dockerfilePath := filepath.Join(containerDir, "Dockerfile")
	if _, err := osStat(dockerfilePath); os.IsNotExist(err) {
		if err := osMkdirAll(containerDir, 0755); err != nil {
			return fmt.Errorf("creating container directory: %w", err)
		}
		if err := osWriteFile(dockerfilePath, containerimage.Dockerfile, 0644); err != nil {
			return fmt.Errorf("writing Dockerfile: %w", err)
		}
		if err := osWriteFile(filepath.Join(containerDir, "entrypoint.sh"), containerimage.Entrypoint, 0644); err != nil {
			return fmt.Errorf("writing entrypoint: %w", err)
		}
		if err := osWriteFile(filepath.Join(containerDir, "setup.sh"), containerimage.Setup, 0644); err != nil {
			return fmt.Errorf("writing setup script: %w", err)
		}
	}

	// Skip build if image already exists
	ids, err := client.ImageList(ctx, cfg.ContainerImage)
	if err != nil {
		return fmt.Errorf("listing images: %w", err)
	}
	if len(ids) > 0 {
		return nil
	}

	return client.ImageBuild(ctx, containerDir, cfg.ContainerImage)
}

var newEmbedder = func(cfg *config.Config) (embeddings.Embedder, error) {
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

var newDockerClient = func() (container.DockerClient, error) {
	return container.NewClient()
}

var newAPIServer = func(sched scheduler.Scheduler, channels api.ChannelEnsurer, threads api.ThreadEnsurer, store api.ChannelLister, messages api.MessageSender, logger *slog.Logger) apiServer {
	return api.NewServer(sched, channels, threads, store, messages, logger)
}

// memIndexer is the subset of *memory.Indexer used by multiDirIndexer.
type memIndexer interface {
	Index(ctx context.Context, memoryPath, dirPath string, excludePaths []string) (int, error)
	Search(ctx context.Context, dirPath, query string, topK int) ([]memory.SearchResult, error)
}

// memoryPathEntry holds a resolved memory path with its scope.
// global == true means the config path was absolute (dir_path = "").
type memoryPathEntry struct {
	path   string
	global bool
}

// multiDirIndexer wraps memory.Indexer to search across multiple memory paths
// resolved from a project dir_path: Claude auto-memory dir, project-level memory/ dir,
// global memory_paths from config, and project-level memory_paths.
type multiDirIndexer struct {
	indexer           memIndexer
	logger            *slog.Logger
	globalMemoryPaths []string
}

func (m *multiDirIndexer) Search(ctx context.Context, dirPath, query string, topK int) ([]memory.SearchResult, error) {
	// Index all paths for freshness first.
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

// channelLister is the subset of db.Store needed for startup re-indexing.
type channelLister interface {
	ListChannels(ctx context.Context) ([]*db.Channel, error)
}

// defaultReindexInterval is the default periodic re-index interval (5 minutes).
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

// reindexLoop runs reindexAll at startup then periodically at the configured interval.
// intervalSec <= 0 uses the default (5 minutes). Blocks until ctx is cancelled.
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

	// Helper: classify a path as inclusion or exclusion.
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

	// 1. Claude auto-memory dir (~/.claude/projects/-encoded-path/memory/).
	if memDir, err := memoryDir(dirPath); err == nil {
		entries = append(entries, memoryPathEntry{path: memDir, global: false})
	}
	// 2. Global memory paths (from config).
	for _, p := range m.globalMemoryPaths {
		addPath(p, filepath.IsAbs(strings.TrimPrefix(p, "!")))
	}
	// 3. Project memory paths (from project config).
	for _, p := range loadProjectMemoryPaths(dirPath) {
		addPath(p, filepath.IsAbs(strings.TrimPrefix(p, "!")))
	}

	// Deduplicate by path.
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

// resolveRelativePath resolves a path relative to dirPath if it's not absolute.
func resolveRelativePath(dirPath, p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(dirPath, p)
}

var loadProjectMemoryPaths = defaultLoadProjectMemoryPaths

func defaultLoadProjectMemoryPaths(dirPath string) []string {
	data, err := osReadFile(filepath.Join(dirPath, ".loop", "config.json"))
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

// memoryDir returns the Claude Code auto memory directory for a project path.
// Claude encodes project paths by replacing "/" with "-".
func memoryDir(dirPath string) (string, error) {
	home, err := userHomeDir()
	if err != nil {
		return "", fmt.Errorf("getting home directory: %w", err)
	}
	encoded := strings.ReplaceAll(dirPath, "/", "-")
	return filepath.Join(home, ".claude", "projects", encoded, "memory"), nil
}

func serve() error {
	cfg, err := configLoad()
	if err != nil {
		return err
	}

	logger := logging.NewLogger(cfg.LogLevel, cfg.LogFormat)
	logger.Info("starting loop", "db_path", cfg.DBPath)

	store, err := newSQLiteStore(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer store.Close()

	platform := cfg.Platform()

	var chatBot orchestrator.Bot
	switch platform {
	case types.PlatformSlack:
		chatBot, err = newSlackBot(cfg.SlackBotToken, cfg.SlackAppToken, logger)
		if err != nil {
			return fmt.Errorf("creating slack bot: %w", err)
		}
	default:
		chatBot, err = newDiscordBot(cfg.DiscordToken, cfg.DiscordAppID, logger)
		if err != nil {
			return fmt.Errorf("creating discord bot: %w", err)
		}
	}

	dockerClient, err := newDockerClient()
	if err != nil {
		return fmt.Errorf("creating docker client: %w", err)
	}
	if closer, ok := dockerClient.(io.Closer); ok {
		defer closer.Close()
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	logger.Info("ensuring agent image", "image", cfg.ContainerImage)
	if err := ensureImage(ctx, dockerClient, cfg); err != nil {
		return fmt.Errorf("ensuring agent image: %w", err)
	}

	runner := container.NewDockerRunner(dockerClient, cfg)

	executor := orchestrator.NewTaskExecutor(runner, chatBot, store, logger, cfg.ContainerTimeout, cfg.StreamingEnabled)
	sched := scheduler.NewTaskScheduler(store, executor, cfg.PollInterval, logger)

	var channelSvc api.ChannelEnsurer
	var threadSvc api.ThreadEnsurer
	switch platform {
	case types.PlatformSlack:
		// Slack doesn't use guild IDs — channel/thread services are always available.
		channelSvc = api.NewChannelService(store, chatBot, "", platform)
		threadSvc = api.NewThreadService(store, chatBot, platform)
	default:
		if cfg.DiscordGuildID != "" {
			channelSvc = api.NewChannelService(store, chatBot, cfg.DiscordGuildID, platform)
			threadSvc = api.NewThreadService(store, chatBot, platform)
		}
	}

	apiSrv := newAPIServer(sched, channelSvc, threadSvc, store, chatBot, logger)
	apiSrv.SetLoopDir(cfg.LoopDir)

	// Configure embeddings and memory indexer at the daemon level.
	if cfg.Memory.Enabled {
		emb, embErr := newEmbedder(cfg)
		if embErr != nil {
			logger.Warn("skipping embeddings", "error", embErr)
		} else {
			indexer := memory.NewIndexer(emb, store, logger, cfg.Memory.MaxChunkChars)
			mdi := &multiDirIndexer{indexer: indexer, logger: logger, globalMemoryPaths: cfg.Memory.Paths}
			apiSrv.SetMemoryIndexer(mdi)
			// Start idle monitor for Ollama container.
			if ollamaEmb, ok := emb.(*embeddings.OllamaEmbedder); ok {
				go ollamaEmb.RunIdleMonitor(ctx)
			}
			// Re-index all channels at startup, then periodically.
			go mdi.reindexLoop(ctx, store, cfg.Memory.ReindexIntervalSec)
		}
	}

	if err := apiSrv.Start(cfg.APIAddr); err != nil {
		return fmt.Errorf("starting api server: %w", err)
	}

	orch := orchestrator.New(store, chatBot, runner, sched, logger, platform, *cfg)

	if err := orch.Start(ctx); err != nil {
		_ = apiSrv.Stop(context.Background())
		return fmt.Errorf("starting orchestrator: %w", err)
	}

	<-ctx.Done()
	logger.Info("shutting down")

	if err := apiSrv.Stop(context.Background()); err != nil {
		slog.Error("api server stop error", "error", err)
	}

	if err := orch.Stop(); err != nil {
		slog.Error("shutdown error", "error", err)
	}

	return nil
}
