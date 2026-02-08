package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/bwmarrin/discordgo"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/radutopala/loop/internal/api"
	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/container"
	"github.com/radutopala/loop/internal/daemon"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/discord"
	"github.com/radutopala/loop/internal/logging"
	"github.com/radutopala/loop/internal/mcpserver"
	"github.com/radutopala/loop/internal/orchestrator"
	"github.com/radutopala/loop/internal/scheduler"
	"github.com/spf13/cobra"
)

func init() {
	cobra.EnablePrefixMatching = true
}

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
	root.AddCommand(newDaemonCmd())
	root.AddCommand(newOnboardCmd())
	return root
}

func newServeCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "serve",
		Aliases: []string{"s"},
		Short:   "Start the Discord bot",
		RunE: func(_ *cobra.Command, _ []string) error {
			return serve()
		},
	}
}

func newMCPCmd() *cobra.Command {
	var channelID, apiURL, logPath, dirPath string

	cmd := &cobra.Command{
		Use:     "mcp",
		Aliases: []string{"m"},
		Short:   "Run as an MCP server over stdio",
		RunE: func(_ *cobra.Command, _ []string) error {
			return runMCP(channelID, apiURL, dirPath, logPath)
		},
	}

	cmd.Flags().StringVar(&channelID, "channel-id", "", "Discord channel ID")
	cmd.Flags().StringVar(&dirPath, "dir", "", "Project directory path (auto-creates Discord channel)")
	cmd.Flags().StringVar(&apiURL, "api-url", "", "Loop API base URL")
	cmd.Flags().StringVar(&logPath, "log", "/mcp/mcp.log", "Path to MCP log file")
	cmd.MarkFlagsOneRequired("channel-id", "dir")
	cmd.MarkFlagsMutuallyExclusive("channel-id", "dir")
	_ = cmd.MarkFlagRequired("api-url")

	return cmd
}

var newMCPServer = mcpserver.New

var (
	daemonStart  = daemon.Start
	daemonStop   = daemon.Stop
	daemonStatus = daemon.Status
	newSystem    = func() daemon.System { return daemon.RealSystem{} }
)

func newDaemonCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "daemon",
		Aliases: []string{"d"},
		Short:   "Manage the Loop background service",
	}

	cmd.AddCommand(&cobra.Command{
		Use:     "start",
		Aliases: []string{"up"},
		Short:   "Install and start the daemon",
		RunE: func(_ *cobra.Command, _ []string) error {
			if err := daemonStart(newSystem()); err != nil {
				return err
			}
			fmt.Println("Daemon started.")
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:     "stop",
		Aliases: []string{"down"},
		Short:   "Stop and uninstall the daemon",
		RunE: func(_ *cobra.Command, _ []string) error {
			if err := daemonStop(newSystem()); err != nil {
				return err
			}
			fmt.Println("Daemon stopped.")
			return nil
		},
	})

	cmd.AddCommand(&cobra.Command{
		Use:     "status",
		Aliases: []string{"st"},
		Short:   "Show daemon status",
		RunE: func(_ *cobra.Command, _ []string) error {
			status, err := daemonStatus(newSystem())
			if err != nil {
				return err
			}
			fmt.Println(status)
			return nil
		},
	})

	return cmd
}

func newOnboardCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "onboard",
		Short: "Initialize Loop configuration",
		Long:  "Copies config.example.json to ~/.loop/config.json for first-time setup",
		RunE: func(cmd *cobra.Command, _ []string) error {
			force, _ := cmd.Flags().GetBool("force")
			return onboard(force)
		},
	}

	cmd.Flags().Bool("force", false, "Overwrite existing config")

	return cmd
}

var (
	userHomeDir = os.UserHomeDir
	osStat      = os.Stat
	osMkdirAll  = os.MkdirAll
	osWriteFile = os.WriteFile
)

func onboard(force bool) error {
	home, err := userHomeDir()
	if err != nil {
		return fmt.Errorf("getting home directory: %w", err)
	}

	loopDir := filepath.Join(home, ".loop")
	configPath := filepath.Join(loopDir, "config.json")

	// Check if config already exists
	if _, err := osStat(configPath); err == nil {
		if !force {
			return fmt.Errorf("config already exists at %s (use --force to overwrite)", configPath)
		}
	}

	// Create ~/.loop directory if it doesn't exist
	if err := osMkdirAll(loopDir, 0755); err != nil {
		return fmt.Errorf("creating loop directory: %w", err)
	}

	// Write embedded example config
	if err := osWriteFile(configPath, config.ExampleConfig, 0600); err != nil {
		return fmt.Errorf("writing config file: %w", err)
	}

	fmt.Printf("✓ Created config at %s\n", configPath)
	fmt.Println("\nNext steps:")
	fmt.Println("1. Edit config.json and add your discord_token and discord_app_id")
	fmt.Println("2. Run 'loop serve' to start the bot")
	fmt.Println("3. Use '/loop template add <name>' in Discord to load task templates")

	return nil
}

var ensureChannelFunc = ensureChannel

func runMCP(channelID, apiURL, dirPath, logPath string) error {
	if dirPath != "" {
		resolved, err := ensureChannelFunc(apiURL, dirPath)
		if err != nil {
			return fmt.Errorf("ensuring channel for dir %s: %w", dirPath, err)
		}
		channelID = resolved
	}

	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("opening mcp log: %w", err)
	}
	defer f.Close()

	logger := slog.New(slog.NewTextHandler(f, nil))
	srv := newMCPServer(channelID, apiURL, http.DefaultClient, logger)
	return srv.Run(context.Background(), &mcp.StdioTransport{})
}

func ensureChannel(apiURL, dirPath string) (string, error) {
	body := fmt.Sprintf(`{"dir_path":%q}`, dirPath)
	resp, err := http.Post(apiURL+"/api/channels", "application/json", strings.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("calling ensure channel API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("ensure channel API returned %d: %s", resp.StatusCode, string(respBody))
	}

	var result struct {
		ChannelID string `json:"channel_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decoding ensure channel response: %w", err)
	}
	return result.ChannelID, nil
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
	newAPIServer = func(sched scheduler.Scheduler, channels api.ChannelEnsurer, logger *slog.Logger) apiServer {
		return api.NewServer(sched, channels, logger)
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

	var channelSvc api.ChannelEnsurer
	if cfg.DiscordGuildID != "" {
		channelSvc = api.NewChannelService(store, bot, cfg.DiscordGuildID)
	}

	apiSrv := newAPIServer(sched, channelSvc, logger)
	if err := apiSrv.Start(cfg.APIAddr); err != nil {
		return fmt.Errorf("starting api server: %w", err)
	}

	orch := orchestrator.New(store, bot, runner, sched, logger, cfg.TaskTemplates)

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
