package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"net/http"

	"github.com/bwmarrin/discordgo"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/radutopala/loop/internal/api"
	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/container"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/discord"
	"github.com/radutopala/loop/internal/logging"
	"github.com/radutopala/loop/internal/mcpserver"
	"github.com/radutopala/loop/internal/orchestrator"
	"github.com/radutopala/loop/internal/scheduler"
	"github.com/spf13/cobra"
)

var osExit = os.Exit

func main() {
	if err := newRootCmd().Execute(); err != nil {
		osExit(1)
	}
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "loop",
		Short: "Loop Discord bot powered by Claude",
	}
	root.AddCommand(newServeCmd())
	root.AddCommand(newMCPCmd())
	return root
}

func newServeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Start the Discord bot",
		RunE: func(_ *cobra.Command, _ []string) error {
			return serve()
		},
	}
}

func newMCPCmd() *cobra.Command {
	var channelID, apiURL string

	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "Run as an MCP server over stdio",
		RunE: func(_ *cobra.Command, _ []string) error {
			return runMCP(channelID, apiURL)
		},
	}

	cmd.Flags().StringVar(&channelID, "channel-id", "", "Discord channel ID")
	cmd.Flags().StringVar(&apiURL, "api-url", "", "Loop API base URL")
	_ = cmd.MarkFlagRequired("channel-id")
	_ = cmd.MarkFlagRequired("api-url")

	return cmd
}

var newMCPServer = mcpserver.New

func runMCP(channelID, apiURL string) error {
	srv := newMCPServer(channelID, apiURL, http.DefaultClient)
	return srv.Run(context.Background(), &mcp.StdioTransport{})
}

// apiServer is the interface used by serve() to decouple from api.Server for testing.
type apiServer interface {
	Start(addr string) error
	Stop(ctx context.Context) error
}

var (
	configLoad     = config.Load
	newSQLiteStore = func(path string) (db.Store, error) {
		return db.NewSQLiteStore(path)
	}
	newDiscordBot = func(token, appID string, logger *slog.Logger) (orchestrator.Bot, error) {
		session, err := discordgo.New("Bot " + token)
		if err != nil {
			return nil, err
		}
		session.Identify.Intents |= discordgo.IntentMessageContent
		return discord.NewBot(session, appID, logger), nil
	}
	newDockerClient = func() (container.DockerClient, error) {
		return container.NewClient()
	}
	newAPIServer = func(sched scheduler.Scheduler, logger *slog.Logger) apiServer {
		return api.NewServer(sched, logger)
	}
)

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

	bot, err := newDiscordBot(cfg.DiscordToken, cfg.DiscordAppID, logger)
	if err != nil {
		return fmt.Errorf("creating discord bot: %w", err)
	}

	dockerClient, err := newDockerClient()
	if err != nil {
		return fmt.Errorf("creating docker client: %w", err)
	}
	if closer, ok := dockerClient.(io.Closer); ok {
		defer closer.Close()
	}
	runner := container.NewDockerRunner(dockerClient, cfg)

	executor := orchestrator.NewTaskExecutor(runner, bot, store, logger)
	sched := scheduler.NewTaskScheduler(store, executor, cfg.PollInterval, logger)

	apiSrv := newAPIServer(sched, logger)
	if err := apiSrv.Start(cfg.APIAddr); err != nil {
		return fmt.Errorf("starting api server: %w", err)
	}

	orch := orchestrator.New(store, bot, runner, sched, logger)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

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
