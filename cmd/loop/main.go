package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"runtime/debug"
	"strings"

	"github.com/bwmarrin/discordgo"
	dockerclient "github.com/docker/docker/client"
	goslack "github.com/slack-go/slack"
	"github.com/slack-go/slack/socketmode"
	"github.com/spf13/cobra"

	"github.com/radutopala/loop/internal/api"
	"github.com/radutopala/loop/internal/browser"
	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/container"
	"github.com/radutopala/loop/internal/daemon"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/discord"
	"github.com/radutopala/loop/internal/dockerproxy"
	"github.com/radutopala/loop/internal/embeddings"
	"github.com/radutopala/loop/internal/fsmigrate"
	"github.com/radutopala/loop/internal/local"
	"github.com/radutopala/loop/internal/mcpserver"
	"github.com/radutopala/loop/internal/orchestrator"
	"github.com/radutopala/loop/internal/osutil"
	"github.com/radutopala/loop/internal/playground"
	_ "github.com/radutopala/loop/internal/quality"
	"github.com/radutopala/loop/internal/quality/evolution"
	"github.com/radutopala/loop/internal/quality/parser"
	"github.com/radutopala/loop/internal/readme"
	"github.com/radutopala/loop/internal/scheduler"
	slackbot "github.com/radutopala/loop/internal/slack"
	"github.com/radutopala/loop/internal/terminal"
)

func init() {
	cobra.EnablePrefixMatching = true
	version = resolveVersion(version)
}

// resolveVersion uses debug.ReadBuildInfo to replace "dev" with the actual
// module version when installed via `go install`.
func resolveVersion(v string) string {
	return doResolveVersion(v, debug.ReadBuildInfo)
}

func doResolveVersion(v string, readBuildInfo func() (*debug.BuildInfo, bool)) string {
	if v != "dev" {
		return v
	}
	if info, ok := readBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return v
}

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// appSystem abstracts OS operations needed by the CLI app.
type appSystem interface {
	UserHomeDir() (string, error)
	Stat(name string) (os.FileInfo, error)
	MkdirAll(path string, perm os.FileMode) error
	WriteFile(name string, data []byte, perm os.FileMode) error
	Getwd() (string, error)
	ReadFile(name string) ([]byte, error)
	Remove(name string) error
	Executable() (string, error)
	EvalSymlinks(path string) (string, error)
	Chmod(name string, mode os.FileMode) error
	Rename(oldpath, newpath string) error
	CreateTemp(dir, pattern string) (*os.File, error)
}

// app holds all injectable dependencies for the CLI, replacing package-level
// var mocks with struct-field injection.
type app struct {
	sys         appSystem
	templatesFS fs.ReadFileFS
	shortcutsFS fs.ReadFileFS

	// Build info (set from package-level ldflags vars)
	version string
	commit  string
	date    string

	// Config & DB
	configLoad     func() (*config.Config, error)
	newSQLiteStore func(string) (db.Store, error)
	fsMigrateRun   func(ctx context.Context, sqlDB *sql.DB, c *fsmigrate.Ctx) error

	// Bots
	discordgoNew  func(string) (*discordgo.Session, error)
	newDiscordBot func(string, string, string, *slog.Logger) (orchestrator.Bot, error)
	newSlackBot   func(string, string, *slog.Logger) (orchestrator.Bot, error)
	newLocalBot   func(db.Store, *slog.Logger) orchestrator.Bot

	// Daemon
	daemonStart  func(daemon.System, string) error
	daemonStop   func(daemon.System) error
	daemonStatus func(daemon.System) (string, error)
	newSystem    func() daemon.System

	// Channel helpers
	ensureChannelFn     func(string, string, string) (string, error)
	ensureAllChannelsFn func(string, string) ([]ensureResult, error)

	// Serve dependencies
	newAPIServer           func(scheduler.Scheduler, api.ChannelEnsurer, api.ThreadEnsurer, api.ChannelLister, api.MessageSender, *slog.Logger) *api.Server
	newMCPServer           func(string, string, string, mcpserver.HTTPClient, *slog.Logger, ...mcpserver.MemoryOption) *mcpserver.Server
	newDockerClient        func() (container.DockerClient, error)
	ensureImage            func(context.Context, container.DockerClient, *config.Config) error
	newEmbedder            func(*config.Config) (embeddings.Embedder, error)
	loadProjectMemoryPaths func(string) []string
	newDockerExecClient    func() (terminal.ExecClient, error)
	newHostExecClient      func() terminal.ExecClient
	newBrowserProvider     func(string, *slog.Logger) (api.BrowserProvider, error)
	discoverWSEndpoint     func() (string, error)
	openLogFile            func(string) (*os.File, error)

	// Update dependencies
	httpGet            func(string) (*http.Response, error)
	getLatestVersionFn func() (string, error)

	// Embedded FS for playground examples (overridable for testing)
	playgroundExamplesFS fs.FS

	// In-container subcommand wrappers. Injected so tests can observe the
	// exit code without actually exiting the test process.
	osExit         func(int)
	dockerproxyRun func(stdout, stderr io.Writer) int
	syscallwrapRun func(forwardArgs, selfArgv []string) int

	// Quality-scan parser factory. Held as an injectable field so tests
	// can exercise both the success and init-failure paths without
	// invoking gotreesitter directly.
	newQualityParser func() (parser.Parser, error)

	// Evolution history-reader factory. Held as an injectable field so
	// tests can substitute a fake reader without provisioning a real
	// git repo.
	newEvolutionReader func() evolution.HistoryReader
}

func newApp() *app {
	a := &app{
		sys:                  osutil.RealSystem{},
		templatesFS:          config.Templates,
		shortcutsFS:          config.Shortcuts,
		playgroundExamplesFS: playground.Examples,
		version:              version,
		commit:               commit,
		date:                 date,

		// Config & DB
		configLoad: config.Load,
		newSQLiteStore: func(path string) (db.Store, error) {
			return db.NewSQLiteStore(path)
		},
		fsMigrateRun: fsmigrate.Run,

		// Bots
		discordgoNew: discordgo.New,
		newSlackBot: func(botToken, appToken string, logger *slog.Logger) (orchestrator.Bot, error) {
			sapi := goslack.New(botToken, goslack.OptionAppLevelToken(appToken))
			smClient := socketmode.New(sapi)
			return slackbot.NewBot(sapi, slackbot.NewSocketModeAdapter(smClient), logger), nil
		},
		newLocalBot: func(store db.Store, logger *slog.Logger) orchestrator.Bot {
			return local.NewBot(store, logger)
		},

		// Daemon
		daemonStart:  daemon.Start,
		daemonStop:   daemon.Stop,
		daemonStatus: daemon.Status,
		newSystem:    func() daemon.System { return daemon.RealSystem{} },

		// Serve dependencies
		newAPIServer: api.NewServer,
		newMCPServer: mcpserver.New,
		newDockerClient: func() (container.DockerClient, error) {
			return container.NewClient()
		},
		newDockerExecClient: func() (terminal.ExecClient, error) {
			return terminal.NewDockerExecClient()
		},
		newHostExecClient: func() terminal.ExecClient {
			return terminal.NewHostExecClient()
		},
		newBrowserProvider: func(chromeImage string, logger *slog.Logger) (api.BrowserProvider, error) {
			dockerClient, err := dockerclient.NewClientWithOpts(dockerclient.FromEnv, dockerclient.WithAPIVersionNegotiation())
			if err != nil {
				return nil, err
			}
			return browser.NewDockerProvider(dockerClient, chromeImage, "1920,1080", logger), nil
		},

		// MCP host browser
		discoverWSEndpoint: browser.DiscoverWSEndpoint,
		openLogFile: func(path string) (*os.File, error) {
			return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		},

		// Update dependencies
		httpGet: http.Get,

		// In-container subcommand wrappers
		osExit:         os.Exit,
		dockerproxyRun: dockerproxy.Run,
		syscallwrapRun: runSyscallwrap,

		// Quality-scan parser factory
		newQualityParser: defaultNewQualityParser,

		// Evolution history-reader factory
		newEvolutionReader: func() evolution.HistoryReader { return evolution.NewExecReader() },
	}
	// Wire up functions that reference methods on a.
	a.newDiscordBot = func(token, appID, guildID string, logger *slog.Logger) (orchestrator.Bot, error) {
		session, err := a.discordgoNew("Bot " + token)
		if err != nil {
			return nil, err
		}
		session.Identify.Intents |= discordgo.IntentMessageContent
		return discord.NewBot(session, appID, guildID, logger), nil
	}
	a.ensureChannelFn = a.ensureChannel
	a.ensureAllChannelsFn = a.ensureAllChannels
	a.ensureImage = a.defaultEnsureImage
	a.newEmbedder = a.defaultNewEmbedder
	a.loadProjectMemoryPaths = a.defaultLoadProjectMemoryPaths
	a.getLatestVersionFn = func() (string, error) {
		return getLatestVersion(fmt.Sprintf("https://github.com/%s/%s/releases/latest", repoOwner, repoName))
	}
	return a
}

var osExit = os.Exit

func main() {
	osExit(newApp().run())
}

func (a *app) run() int {
	if err := a.newRootCmd().Execute(); err != nil {
		return 1
	}
	return 0
}

func (a *app) newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "loop",
		Short: "Loop bot powered by Claude",
	}
	root.AddCommand(a.newServeCmd())
	root.AddCommand(a.newMCPCmd())
	root.AddCommand(a.newDaemonStartCmd())
	root.AddCommand(a.newDaemonStopCmd())
	root.AddCommand(a.newDaemonRestartCmd())
	root.AddCommand(a.newDaemonStatusCmd())
	root.AddCommand(a.newOnboardGlobalCmd())
	root.AddCommand(a.newOnboardLocalCmd())
	root.AddCommand(a.newVersionCmd())
	root.AddCommand(a.newReadmeCmd())
	root.AddCommand(a.newUpdateCmd())
	root.AddCommand(a.newImageRebuildCmd())
	root.AddCommand(a.newImageStatusCmd())
	root.AddCommand(a.newMCPBrowserCmd())
	root.AddCommand(a.newMCPHostBrowserCmd())
	root.AddCommand(a.newSyscallwrapCmd())
	root.AddCommand(a.newDockerproxyCmd())
	root.AddCommand(a.newQualityCmd())
	root.SetHelpTemplate(helpTemplate)
	return root
}

const helpTemplate = `loop - AI-powered development platform with Claude agents, browser automation, and team collaboration

Usage:
  loop [command]

Available Commands:
  serve                    Start the bot (alias: s)
  mcp                      Run as an MCP server over stdio (alias: m)
    --channel-id           Channel ID
    --dir                  Project directory path (auto-creates channel)
    --api-url              Loop API base URL (required)
    --log                  Path to MCP log file [default: .loop/mcp.log]
    --author-id            User ID of the message author
    --platform             Platform for channel creation [default: local]
    --memory               Enable memory search/index tools
    --agent-id             Agent ID for inter-agent tools and MCP Channels
  onboard:global           Initialize global config at ~/.loop/ (aliases: o:global, setup)
    --force                Overwrite existing config
    --owner-id             Set RBAC owner user ID (exits bootstrap mode)
  onboard:local            Register Loop MCP server in current project (aliases: o:local, init)
    --api-url              Loop API base URL [default: http://localhost:8222]
    --owner-id             Set RBAC owner user ID in project config
    --platform             Only register channel for this platform (e.g. local)
  daemon:start             Install and start the daemon — launchd on macOS, Windows services on Windows, systemd on Linux (aliases: d:start, up)
  daemon:stop              Stop and uninstall the daemon (aliases: d:stop, down)
  daemon:restart           Restart the daemon (aliases: d:restart, restart)
  daemon:status            Show daemon status (alias: d:status)
  mcp-host-browser         Standalone MCP server for host Chrome automation (auto-discovers via DevToolsActivePort)
    --log                  Path to MCP log file [default: .loop/mcp-host-browser.log]
  image:rebuild            Rebuild the Docker agent image (aliases: i:rebuild, i:r)
  image:status             Show Docker agent image status and versions (aliases: i:status, i:s)
  version                  Print version information (alias: v)
  readme                   Print the README documentation (alias: r)
  update                   Update loop to the latest version (alias: u)

Use "loop [command] --help" for more information about a command.
`

func (a *app) newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "version",
		Aliases: []string{"v"},
		Short:   "Print version information",
		Run: func(_ *cobra.Command, _ []string) {
			fmt.Printf("loop %s\n", a.version)
			if a.commit != "none" {
				fmt.Printf("  commit: %s\n", a.commit)
			}
			if a.date != "unknown" {
				fmt.Printf("  built:  %s\n", a.date)
			}
		},
	}
}

func (a *app) newReadmeCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "readme",
		Aliases: []string{"r"},
		Short:   "Print the README documentation",
		Run: func(_ *cobra.Command, _ []string) {
			fmt.Print(readme.Content)
		},
	}
}

type ensureResult struct {
	Platform  string `json:"platform"`
	ChannelID string `json:"channel_id"`
	Created   bool   `json:"created"`
}

func (a *app) ensureChannel(apiURL, dirPath, platform string) (string, error) {
	body := fmt.Sprintf(`{"dir_path":%q,"platform":%q}`, dirPath, platform)
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

func (a *app) ensureAllChannels(apiURL, dirPath string) ([]ensureResult, error) {
	body := fmt.Sprintf(`{"dir_path":%q}`, dirPath)
	resp, err := http.Post(apiURL+"/api/channels/ensure-all", "application/json", strings.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("calling ensure-all channels API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ensure-all channels API returned %d: %s", resp.StatusCode, string(respBody))
	}

	var results []ensureResult
	if err := json.NewDecoder(resp.Body).Decode(&results); err != nil {
		return nil, fmt.Errorf("decoding ensure-all channels response: %w", err)
	}
	return results, nil
}

// --- Daemon commands ---

func (a *app) newDaemonStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "daemon:start",
		Aliases: []string{"d:start", "up"},
		Short:   "Install and start the daemon",
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, err := a.configLoad()
			if err != nil {
				return err
			}
			if err := a.daemonStart(a.newSystem(), cfg.LogFile); err != nil {
				return err
			}
			fmt.Println("Daemon started.")
			return nil
		},
	}
}

func (a *app) newDaemonStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "daemon:stop",
		Aliases: []string{"d:stop", "down"},
		Short:   "Stop and uninstall the daemon",
		RunE: func(_ *cobra.Command, _ []string) error {
			if err := a.daemonStop(a.newSystem()); err != nil {
				return err
			}
			fmt.Println("Daemon stopped.")
			return nil
		},
	}
}

func (a *app) newDaemonRestartCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "daemon:restart",
		Aliases: []string{"d:restart", "restart"},
		Short:   "Restart the daemon",
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, err := a.configLoad()
			if err != nil {
				return err
			}
			_ = a.daemonStop(a.newSystem()) // ignore error — may not be running
			if err := a.daemonStart(a.newSystem(), cfg.LogFile); err != nil {
				return err
			}
			fmt.Println("Daemon restarted.")
			return nil
		},
	}
}

func (a *app) newDaemonStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "daemon:status",
		Aliases: []string{"d:status"},
		Short:   "Show daemon status",
		RunE: func(_ *cobra.Command, _ []string) error {
			status, err := a.daemonStatus(a.newSystem())
			if err != nil {
				return err
			}
			fmt.Println(status)
			return nil
		},
	}
}
