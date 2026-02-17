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
	containerimage "github.com/radutopala/loop/internal/container/image"
	"github.com/radutopala/loop/internal/daemon"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/discord"
	"github.com/radutopala/loop/internal/logging"
	"github.com/radutopala/loop/internal/mcpserver"
	"github.com/radutopala/loop/internal/orchestrator"
	"github.com/radutopala/loop/internal/scheduler"
	slackbot "github.com/radutopala/loop/internal/slack"
	"github.com/radutopala/loop/internal/types"
	goslack "github.com/slack-go/slack"
	"github.com/slack-go/slack/socketmode"
	"github.com/spf13/cobra"
)

func init() {
	cobra.EnablePrefixMatching = true
}

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
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
		Short: "Loop bot powered by Claude",
	}
	root.AddCommand(newServeCmd())
	root.AddCommand(newMCPCmd())
	root.AddCommand(newDaemonStartCmd())
	root.AddCommand(newDaemonStopCmd())
	root.AddCommand(newDaemonStatusCmd())
	root.AddCommand(newOnboardGlobalCmd())
	root.AddCommand(newOnboardLocalCmd())
	root.AddCommand(newVersionCmd())
	root.AddCommand(newUpdateCmd())
	root.SetHelpTemplate(helpTemplate)
	return root
}

const helpTemplate = `loop - Chat bot powered by Claude that runs AI agents in Docker containers

Usage:
  loop [command]

Available Commands:
  serve                    Start the bot (alias: s)
  mcp                      Run as an MCP server over stdio (alias: m)
    --channel-id           Channel ID
    --dir                  Project directory path (auto-creates channel)
    --api-url              Loop API base URL (required)
    --log                  Path to MCP log file [default: .loop/mcp.log]
  onboard:global           Initialize global config at ~/.loop/ (aliases: o:global, setup)
    --force                Overwrite existing config
  onboard:local            Register Loop MCP server in current project (aliases: o:local, init)
    --api-url              Loop API base URL [default: http://localhost:8222]
  daemon:start             Install and start the daemon (aliases: d:start, up)
  daemon:stop              Stop and uninstall the daemon (aliases: d:stop, down)
  daemon:status            Show daemon status (alias: d:status)
  version                  Print version information (alias: v)
  update                   Update loop to the latest version (alias: u)

Use "loop [command] --help" for more information about a command.
`

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "version",
		Aliases: []string{"v"},
		Short:   "Print version information",
		Run: func(_ *cobra.Command, _ []string) {
			fmt.Printf("loop %s\n", version)
			if commit != "none" {
				fmt.Printf("  commit: %s\n", commit)
			}
			if date != "unknown" {
				fmt.Printf("  built:  %s\n", date)
			}
		},
	}
}

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

func newMCPCmd() *cobra.Command {
	var channelID, apiURL, logPath, dirPath, authorID string

	cmd := &cobra.Command{
		Use:     "mcp",
		Aliases: []string{"m"},
		Short:   "Run as an MCP server over stdio",
		RunE: func(_ *cobra.Command, _ []string) error {
			return runMCP(channelID, apiURL, dirPath, logPath, authorID)
		},
	}

	cmd.Flags().StringVar(&channelID, "channel-id", "", "Channel ID")
	cmd.Flags().StringVar(&dirPath, "dir", "", "Project directory path (auto-creates channel)")
	cmd.Flags().StringVar(&apiURL, "api-url", "", "Loop API base URL")
	cmd.Flags().StringVar(&logPath, "log", ".loop/mcp.log", "Path to MCP log file")
	cmd.Flags().StringVar(&authorID, "author-id", "", "User ID of the message author")
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

func newDaemonStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "daemon:start",
		Aliases: []string{"d:start", "up"},
		Short:   "Install and start the daemon",
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, err := configLoad()
			if err != nil {
				return err
			}
			if err := daemonStart(newSystem(), cfg.LogFile); err != nil {
				return err
			}
			fmt.Println("Daemon started.")
			return nil
		},
	}
}

func newDaemonStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "daemon:stop",
		Aliases: []string{"d:stop", "down"},
		Short:   "Stop and uninstall the daemon",
		RunE: func(_ *cobra.Command, _ []string) error {
			if err := daemonStop(newSystem()); err != nil {
				return err
			}
			fmt.Println("Daemon stopped.")
			return nil
		},
	}
}

func newDaemonStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "daemon:status",
		Aliases: []string{"d:status"},
		Short:   "Show daemon status",
		RunE: func(_ *cobra.Command, _ []string) error {
			status, err := daemonStatus(newSystem())
			if err != nil {
				return err
			}
			fmt.Println(status)
			return nil
		},
	}
}

func newOnboardGlobalCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "onboard:global",
		Aliases: []string{"o:global", "setup"},
		Short:   "Initialize global Loop configuration at ~/.loop/",
		Long:    "Copies config.example.json to ~/.loop/config.json for first-time setup",
		RunE: func(cmd *cobra.Command, _ []string) error {
			force, _ := cmd.Flags().GetBool("force")
			return onboardGlobal(force)
		},
	}
	cmd.Flags().Bool("force", false, "Overwrite existing config")
	return cmd
}

func newOnboardLocalCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "onboard:local",
		Aliases: []string{"o:local", "init"},
		Short:   "Register Loop MCP server in the current project",
		Long:    "Writes .mcp.json with the loop MCP server for Claude Code integration",
		RunE: func(cmd *cobra.Command, _ []string) error {
			apiURL, _ := cmd.Flags().GetString("api-url")
			return onboardLocal(apiURL)
		},
	}
	cmd.Flags().String("api-url", "http://localhost:8222", "Loop API base URL")
	return cmd
}

var (
	userHomeDir = os.UserHomeDir
	osStat      = os.Stat
	osMkdirAll  = os.MkdirAll
	osWriteFile = os.WriteFile
	osGetwd     = os.Getwd
	osReadFile  = os.ReadFile
)

func onboardGlobal(force bool) error {
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

	// Create default .bashrc for container shell aliases
	bashrcPath := filepath.Join(loopDir, ".bashrc")
	if _, err := osStat(bashrcPath); err != nil {
		bashrcContent := []byte("# Shell aliases and config sourced inside Loop containers.\n# Add your aliases here — this file is bind-mounted as ~/.bashrc.\n")
		if err := osWriteFile(bashrcPath, bashrcContent, 0644); err != nil {
			return fmt.Errorf("writing .bashrc: %w", err)
		}
	}

	// Flush embedded container files
	containerDir := filepath.Join(loopDir, "container")
	if err := osMkdirAll(containerDir, 0755); err != nil {
		return fmt.Errorf("creating container directory: %w", err)
	}
	if err := osWriteFile(filepath.Join(containerDir, "Dockerfile"), containerimage.Dockerfile, 0644); err != nil {
		return fmt.Errorf("writing container Dockerfile: %w", err)
	}
	if err := osWriteFile(filepath.Join(containerDir, "entrypoint.sh"), containerimage.Entrypoint, 0644); err != nil {
		return fmt.Errorf("writing container entrypoint: %w", err)
	}
	setupPath := filepath.Join(containerDir, "setup.sh")
	if _, err := osStat(setupPath); err != nil {
		if err := osWriteFile(setupPath, containerimage.Setup, 0644); err != nil {
			return fmt.Errorf("writing container setup script: %w", err)
		}
	}

	// Write Slack app manifest
	if err := osWriteFile(filepath.Join(loopDir, "slack-manifest.json"), config.SlackManifest, 0644); err != nil {
		return fmt.Errorf("writing Slack manifest: %w", err)
	}

	// Create templates directory for prompt_path templates
	templatesDir := filepath.Join(loopDir, "templates")
	if err := osMkdirAll(templatesDir, 0755); err != nil {
		return fmt.Errorf("creating templates directory: %w", err)
	}

	fmt.Printf("✓ Created config at %s\n", configPath)
	fmt.Println("\nNext steps:")
	fmt.Println("1. Edit config.json and add your platform credentials (Discord or Slack)")
	fmt.Println("   For Slack: create an app from ~/.loop/slack-manifest.json (see README)")
	fmt.Println("2. Run 'loop serve' to start the bot")
	fmt.Println("3. Customize the Dockerfile at ~/.loop/container/ if needed")

	return nil
}

func onboardLocal(apiURL string) error {
	dir, err := osGetwd()
	if err != nil {
		return fmt.Errorf("getting working directory: %w", err)
	}

	mcpPath := filepath.Join(dir, ".mcp.json")

	// Read existing .mcp.json if it exists, merge into it
	existing := make(map[string]any)
	if data, err := osReadFile(mcpPath); err == nil {
		if err := json.Unmarshal(data, &existing); err != nil {
			return fmt.Errorf("parsing existing .mcp.json: %w", err)
		}
	}

	// Ensure mcpServers key exists
	servers, _ := existing["mcpServers"].(map[string]any)
	if servers == nil {
		servers = make(map[string]any)
	}

	// Check if loop is already registered
	if _, exists := servers["loop"]; exists {
		fmt.Println("loop MCP server is already registered in .mcp.json")
	} else {
		// Add loop server
		servers["loop"] = map[string]any{
			"command": "loop",
			"args":    []string{"mcp", "--dir", dir, "--api-url", apiURL, "--log", filepath.Join(dir, ".loop", "mcp.log")},
		}
		existing["mcpServers"] = servers

		mcpJSON, _ := json.MarshalIndent(existing, "", "  ")
		if err := osWriteFile(mcpPath, append(mcpJSON, '\n'), 0644); err != nil {
			return fmt.Errorf("writing .mcp.json: %w", err)
		}

		fmt.Printf("Added loop MCP server to %s\n", mcpPath)
		fmt.Println("\nMake sure 'loop serve' or 'loop daemon:start' is running.")
	}

	// Write project config example if .loop/config.json doesn't exist
	loopDir := filepath.Join(dir, ".loop")
	projectConfigPath := filepath.Join(loopDir, "config.json")
	if _, err := osStat(projectConfigPath); os.IsNotExist(err) {
		if err := osMkdirAll(loopDir, 0755); err != nil {
			return fmt.Errorf("creating .loop directory: %w", err)
		}
		if err := osWriteFile(projectConfigPath, config.ProjectExampleConfig, 0644); err != nil {
			return fmt.Errorf("writing project config: %w", err)
		}
		fmt.Printf("Created project config at %s\n", projectConfigPath)
	}

	// Create templates directory for prompt_path templates
	templatesDir := filepath.Join(loopDir, "templates")
	if err := osMkdirAll(templatesDir, 0755); err != nil {
		return fmt.Errorf("creating templates directory: %w", err)
	}

	// Eagerly create the channel so it's ready immediately
	channelID, err := ensureChannelFunc(apiURL, dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not register channel (is 'loop serve' running?): %v\n", err)
	} else {
		fmt.Printf("Channel ready: %s\n", channelID)
	}

	return nil
}

var ensureChannelFunc = ensureChannel

func runMCP(channelID, apiURL, dirPath, logPath, authorID string) error {
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

	logLevel, logFormat := "info", "text"
	if cfg, err := configLoad(); err == nil {
		logLevel = cfg.LogLevel
		logFormat = cfg.LogFormat
	}

	logger := logging.NewLoggerWithWriter(logLevel, logFormat, f)
	srv := newMCPServer(channelID, apiURL, authorID, http.DefaultClient, logger)
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
	newSlackBot = func(botToken, appToken string, logger *slog.Logger) (orchestrator.Bot, error) {
		api := goslack.New(botToken, goslack.OptionAppLevelToken(appToken))
		smClient := socketmode.New(api)
		return slackbot.NewBot(api, slackbot.NewSocketModeAdapter(smClient), logger), nil
	}
	newDockerClient = func() (container.DockerClient, error) {
		return container.NewClient()
	}
	newAPIServer = func(sched scheduler.Scheduler, channels api.ChannelEnsurer, threads api.ThreadEnsurer, store api.ChannelLister, messages api.MessageSender, logger *slog.Logger) apiServer {
		return api.NewServer(sched, channels, threads, store, messages, logger)
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

	platform := cfg.Platform()

	var bot orchestrator.Bot
	switch platform {
	case types.PlatformSlack:
		bot, err = newSlackBot(cfg.SlackBotToken, cfg.SlackAppToken, logger)
		if err != nil {
			return fmt.Errorf("creating slack bot: %w", err)
		}
	default:
		bot, err = newDiscordBot(cfg.DiscordToken, cfg.DiscordAppID, logger)
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

	executor := orchestrator.NewTaskExecutor(runner, bot, store, logger, cfg.ContainerTimeout, cfg.StreamingEnabled)
	sched := scheduler.NewTaskScheduler(store, executor, cfg.PollInterval, logger)

	var channelSvc api.ChannelEnsurer
	var threadSvc api.ThreadEnsurer
	switch platform {
	case types.PlatformSlack:
		// Slack doesn't use guild IDs — channel/thread services are always available.
		channelSvc = api.NewChannelService(store, bot, "", platform)
		threadSvc = api.NewThreadService(store, bot, platform)
	default:
		if cfg.DiscordGuildID != "" {
			channelSvc = api.NewChannelService(store, bot, cfg.DiscordGuildID, platform)
			threadSvc = api.NewThreadService(store, bot, platform)
		}
	}

	apiSrv := newAPIServer(sched, channelSvc, threadSvc, store, bot, logger)
	if err := apiSrv.Start(cfg.APIAddr); err != nil {
		return fmt.Errorf("starting api server: %w", err)
	}

	orch := orchestrator.New(store, bot, runner, sched, logger, cfg.TaskTemplates, cfg.ContainerTimeout, cfg.LoopDir, platform, cfg.StreamingEnabled)

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
