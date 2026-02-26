package main

import (
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
	goslack "github.com/slack-go/slack"
	"github.com/slack-go/slack/socketmode"
	"github.com/spf13/cobra"

	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/daemon"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/discord"
	"github.com/radutopala/loop/internal/orchestrator"
	"github.com/radutopala/loop/internal/readme"
	slackbot "github.com/radutopala/loop/internal/slack"
)

func init() {
	cobra.EnablePrefixMatching = true
	version = resolveVersion(version)
}

// resolveVersion uses debug.ReadBuildInfo to replace "dev" with the actual
// module version when installed via `go install`.
var resolveVersion = func(v string) string {
	if v != "dev" {
		return v
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return v
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
	root.AddCommand(newDaemonRestartCmd())
	root.AddCommand(newDaemonStatusCmd())
	root.AddCommand(newOnboardGlobalCmd())
	root.AddCommand(newOnboardLocalCmd())
	root.AddCommand(newVersionCmd())
	root.AddCommand(newReadmeCmd())
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
    --owner-id             Set RBAC owner user ID (exits bootstrap mode)
  onboard:local            Register Loop MCP server in current project (aliases: o:local, init)
    --api-url              Loop API base URL [default: http://localhost:8222]
    --owner-id             Set RBAC owner user ID in project config
  daemon:start             Install and start the daemon — launchd on macOS, systemd on Linux (aliases: d:start, up)
  daemon:stop              Stop and uninstall the daemon (aliases: d:stop, down)
  daemon:restart           Restart the daemon (aliases: d:restart, restart)
  daemon:status            Show daemon status (alias: d:status)
  version                  Print version information (alias: v)
  readme                   Print the README documentation (alias: r)
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

func newReadmeCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "readme",
		Aliases: []string{"r"},
		Short:   "Print the README documentation",
		Run: func(_ *cobra.Command, _ []string) {
			fmt.Print(readme.Content)
		},
	}
}

// --- Shared testable vars ---

var (
	userHomeDir               = os.UserHomeDir
	osStat                    = os.Stat
	osMkdirAll                = os.MkdirAll
	osWriteFile               = os.WriteFile
	osGetwd                   = os.Getwd
	osReadFile                = os.ReadFile
	templatesFS fs.ReadFileFS = config.Templates
)

var (
	configLoad     = config.Load
	newSQLiteStore = func(path string) (db.Store, error) {
		return db.NewSQLiteStore(path)
	}
)

var ensureChannelFunc = ensureChannel

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

// --- Daemon commands ---

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

func newDaemonRestartCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "daemon:restart",
		Aliases: []string{"d:restart", "restart"},
		Short:   "Restart the daemon",
		RunE: func(_ *cobra.Command, _ []string) error {
			cfg, err := configLoad()
			if err != nil {
				return err
			}
			_ = daemonStop(newSystem()) // ignore error — may not be running
			if err := daemonStart(newSystem(), cfg.LogFile); err != nil {
				return err
			}
			fmt.Println("Daemon restarted.")
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

// --- Bot constructors (kept in main.go to isolate discordgo/slack-go imports) ---

var (
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
)
