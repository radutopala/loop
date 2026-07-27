// claude_cmd.go holds ClaudeCmdBuilder and the helpers that assemble the Claude
// CLI command for both batch (stream-json) and interactive terminal sessions.
package container

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/config"
)

// buildBaseClaudeCmd returns the common Claude CLI flags shared by both
// batch and interactive modes. When continueSession is true, sessionID is
// ignored and `--continue` is emitted instead — used to relaunch a terminal
// pane after its Claude process died without knowing which (possibly forked)
// session id it was running.
func buildBaseClaudeCmd(cfg *config.Config, mcpConfigPath, sessionID, agentID string, forkSession, continueSession bool, extraDirs []string) []string {
	cmd := []string{cfg.ClaudeBinPath, "--mcp-config", mcpConfigPath}
	if cfg.ClaudeModel != "" {
		cmd = append(cmd, "--model", cfg.ClaudeModel)
	}
	if cfg.ClaudeEffort != "" {
		cmd = append(cmd, "--effort", cfg.ClaudeEffort)
	}
	cmd = append(cmd, "--dangerously-skip-permissions")
	switch {
	case continueSession:
		cmd = append(cmd, "--continue")
	case sessionID != "":
		cmd = append(cmd, "--resume", sessionID)
		if forkSession {
			cmd = append(cmd, "--fork-session")
		}
	}
	// Enable MCP Channels when agent tools are configured, so the agent
	// can receive push notifications from other agents. Anthropic ships
	// `--dangerously-load-development-channels` as a development-only flag,
	// so it's opt-in via config (global → project → worktree).
	if agentID != "" && cfg.ClaudeDangerouslyLoadDevelopmentChannels {
		cmd = append(cmd, "--dangerously-load-development-channels", "server:loop")
	}
	for _, dir := range extraDirs {
		cmd = append(cmd, "--add-dir", dir)
	}
	return cmd
}

// planModePromptPrefix is prepended to the user's prompt when req.PlanMode is
// true. Calling EnterPlanMode flips the session's permission context to
// "plan", which causes Claude Code's per-turn attachment loop to inject the
// full pair-planning prompt (with a computed planFilePath and read-only
// restrictions) from the next turn onward.
const planModePromptPrefix = "Call the EnterPlanMode tool before doing anything else, then follow the plan-mode instructions that follow.\n\n"

// reportFindingsTool is Claude Code's built-in code-review reporting tool.
// Denied by default in batch runs (see config.DefaultBatchDisallowedTools),
// re-enabled for review runs.
const reportFindingsTool = "ReportFindings"

// withoutTool returns tools with name removed, preserving order. The input
// slice is never mutated — it belongs to the cached config.
func withoutTool(tools []string, name string) []string {
	out := make([]string, 0, len(tools))
	for _, t := range tools {
		if t != name {
			out = append(out, t)
		}
	}
	return out
}

// reviewModeSettings is the --settings payload for a review run. It carries
// env, not container env vars, because Claude Code applies each settings
// scope over process.env with a plain assign, in the fixed order
// userSettings → flagSettings → policySettings. A container-level env var is
// therefore *lower* precedence than ~/.claude/settings.json, which Loop
// bind-mounts from the host into every agent container: a host setting of
// e.g. CLAUDE_CODE_SUBAGENT_MODEL=sonnet silently overwrote it. --settings is
// the flagSettings scope, so it lands after the user's file and wins.
//
// Both keys are load-bearing; see agent.AgentRequest.ReviewMode.
const reviewModeSettings = `{"env":{"CLAUDE_CODE_REPORT_FINDINGS":"1","CLAUDE_CODE_SUBAGENT_MODEL":"inherit"}}`

// buildClaudeCmd assembles the Claude CLI command with all flags for batch mode.
func buildClaudeCmd(cfg *config.Config, mcpConfigPath string, req *agent.AgentRequest) []string {
	// Per-channel on-demand overrides beat the merged config's model/effort.
	// Shallow-copy so the cached config is never mutated.
	if req.Model != "" || req.Effort != "" || req.ReviewMode {
		override := *cfg
		if req.Model != "" {
			override.ClaudeModel = req.Model
		}
		if req.Effort != "" {
			override.ClaudeEffort = req.Effort
		}
		// A review run is the one batch case that wants ReportFindings: it's
		// how the built-in code-review command hands its findings back, and
		// keeping it available is half of what makes that command run inline
		// rather than fork. See agent.AgentRequest.ReviewMode.
		if req.ReviewMode {
			override.ClaudeBatchDisallowedTools = withoutTool(cfg.ClaudeBatchDisallowedTools, reportFindingsTool)
		}
		cfg = &override
	}
	cmd := buildBaseClaudeCmd(cfg, mcpConfigPath, req.SessionID, req.AgentID, req.ForkSession, false, cfg.ExtraDirs)
	if req.ReviewMode {
		cmd = append(cmd, "--settings", reviewModeSettings)
	}
	// Deny tools that only make sense in a persistent interactive harness.
	// In one-shot `--print` mode the container exits at end of turn, so tools
	// like ScheduleWakeup / Cron* schedule re-invocations that never fire —
	// the agent silently "parks" work that never resumes. Batch-only: the
	// interactive terminal path (BuildInteractiveClaudeCmd) keeps them.
	//
	// `--disallowedTools` is variadic (`<tools...>`): it must be emitted BEFORE
	// another flag (here `--print`) so the parser stops consuming at that flag.
	// Emitted last (adjacent to the trailing positional prompt) it would swallow
	// the prompt and split it into bogus tool names, leaving an empty prompt.
	if len(cfg.ClaudeBatchDisallowedTools) > 0 {
		cmd = append(cmd, "--disallowedTools", strings.Join(cfg.ClaudeBatchDisallowedTools, ","))
	}
	// Claude Code gates the interactive tools (AskUserQuestion, EnterPlanMode,
	// ExitPlanMode) behind a configured --permission-prompt-tool in headless
	// --print mode. It must name a *registered* MCP tool — an empty value leaves
	// the tools gated. Claude invokes it with a {tool_name, input, tool_use_id}
	// payload when one of those tools fires, so the tool's schema must accept
	// that payload and return an allow decision — a tool with a stricter schema
	// (e.g. get_readme) rejects the payload and the interactive tool errors.
	// mcp__loop__permission_prompt accepts any object and always allows; Loop
	// runs under --dangerously-skip-permissions, so ordinary tools never reach
	// this gate, and Loop surfaces the interactive tools via stream interception.
	// Batch-only: the interactive terminal already exposes these.
	cmd = append(cmd, "--permission-prompt-tool", "mcp__loop__permission_prompt")
	cmd = append(cmd, "--print", "--verbose", "--output-format", "stream-json")
	if req.SystemPrompt != "" {
		cmd = append(cmd, "--append-system-prompt", req.SystemPrompt)
	}
	prompt := req.BuildPrompt()
	if req.PlanMode {
		prompt = planModePromptPrefix + prompt
	}
	return append(cmd, prompt)
}

// mcpConfigPathForAgent returns the MCP config file path. When agentID is set,
// a per-agent config is used so each agent gets its own --agent-id flag.
func mcpConfigPathForAgent(workDir, channelID, agentID string) string {
	if agentID != "" {
		return filepath.Join(workDir, ".loop", "mcp-"+channelID+"-"+agentID+".json")
	}
	return filepath.Join(workDir, ".loop", "mcp-"+channelID+".json")
}

// ClaudeExitMarkerPrefix prefixes the exit-code line an interactive Claude
// terminal command emits to its own pty when the Claude process exits. The
// terminal handler scans live output for this marker to detect a dead Claude
// process (e.g. OOM-killed) and relaunch it. See claudeExitTrailer.
const ClaudeExitMarkerPrefix = "__LOOP_CLAUDE_EXIT:"

// claudeExitTrailer is appended to an interactive Claude command so that,
// once Claude exits for any reason, the shell resets any mouse-tracking
// modes Claude may have left enabled (a dead TUI can leave xterm mouse
// reporting on, flooding the shell with unusable input) and reports Claude's
// exit code via ClaudeExitMarkerPrefix.
const claudeExitTrailer = `; __lec=$?; printf '\033[?1000l\033[?1002l\033[?1003l\033[?1006l\n` + ClaudeExitMarkerPrefix + `%d\n' "$__lec"`

// buildInteractiveClaudeCmd assembles the Claude CLI shell command for interactive
// terminal sessions (no --print, --verbose, --output-format flags).
//
// When the seccomp gate is enabled, the command is wrapped in
// `loop syscallwrap --` so the interactive claude runs under the same filter
// the agent-mode (stream) path gets via entrypoint.sh. docker-exec'ing into the
// running shell container does NOT inherit the shell's seccomp state (setns(2)
// is per-namespace, but seccomp is per-process), so without this wrapper a
// user typing `claude` at the terminal would bypass the gate entirely.
func buildInteractiveClaudeCmd(cfg *config.Config, channelID, workDir, sessionID, agentID string, forkSession, continueSession bool) string {
	mcpConfigPath := mcpConfigPathForAgent(workDir, channelID, agentID)
	cmd := buildBaseClaudeCmd(cfg, mcpConfigPath, sessionID, agentID, forkSession, continueSession, cfg.ExtraDirs)
	if cfg.Gates.Agentgate.Enabled {
		cmd = append([]string{"loop", "syscallwrap", "--"}, cmd...)
	}
	return "CLAUDE_CODE_NO_FLICKER=1 " + strings.Join(cmd, " ") + claudeExitTrailer
}

// BuildInteractiveClaudeCmd assembles the Claude CLI shell command for interactive
// terminal sessions. See buildInteractiveClaudeCmd for details.
func BuildInteractiveClaudeCmd(cfg *config.Config, channelID, workDir, sessionID, agentID string, forkSession bool) string {
	return buildInteractiveClaudeCmd(cfg, channelID, workDir, sessionID, agentID, forkSession, false)
}

// ClaudeCmdBuilder builds the interactive Claude command for terminal sessions.
// It implements api.InteractiveCmdBuilder.
type ClaudeCmdBuilder struct {
	cfg                       atomic.Pointer[config.Config]
	configLoad                func() (*config.Config, error)
	loadProjectConfig         func(string, *config.Config) (*config.Config, error)
	loadWorktreeProjectConfig func(string, string, *config.Config) (*config.Config, error)
	writeFile                 func(string, []byte, os.FileMode) error
	mkdirAll                  func(string, os.FileMode) error
}

// NewClaudeCmdBuilder creates a builder that uses the given config.
func NewClaudeCmdBuilder(cfg *config.Config, configLoad func() (*config.Config, error)) *ClaudeCmdBuilder {
	b := &ClaudeCmdBuilder{
		configLoad:                configLoad,
		loadProjectConfig:         config.LoadProjectConfig,
		loadWorktreeProjectConfig: config.LoadWorktreeProjectConfig,
		writeFile:                 os.WriteFile,
		mkdirAll:                  os.MkdirAll,
	}
	b.cfg.Store(cfg)
	return b
}

// currentConfig returns a fresh config by calling configLoad, falling back
// to the last-known-good config on error or when configLoad is nil.
func (b *ClaudeCmdBuilder) currentConfig() *config.Config {
	if b.configLoad == nil {
		return b.cfg.Load()
	}
	fresh, err := b.configLoad()
	if err != nil {
		return b.cfg.Load()
	}
	b.cfg.Store(fresh)
	return fresh
}

// BuildInteractiveCmd returns the interactive Claude shell command for the given channel.
// It loads the project-specific config (if any) to apply per-project overrides
// such as claude_model before building the command.
// When agentID is set, a per-agent MCP config is written with --agent-id so
// the agent can identify itself via the MCP tools.
func (b *ClaudeCmdBuilder) BuildInteractiveCmd(channelID, dirPath, parentDirPath, sessionID, agentID string, forkSession bool) string {
	cfg, workDir := b.resolveCmdConfig(channelID, dirPath, parentDirPath, agentID)
	return buildInteractiveClaudeCmd(cfg, channelID, workDir, sessionID, agentID, forkSession, false)
}

// BuildContinueCmd returns the interactive Claude shell command that resumes
// the most recently modified session for the channel's working directory via
// `claude --continue`, without needing to know its (possibly forked) session
// id. Used to relaunch a terminal pane's Claude process after it exits
// unexpectedly (e.g. OOM-killed).
func (b *ClaudeCmdBuilder) BuildContinueCmd(channelID, dirPath, parentDirPath, agentID string) string {
	cfg, workDir := b.resolveCmdConfig(channelID, dirPath, parentDirPath, agentID)
	return buildInteractiveClaudeCmd(cfg, channelID, workDir, "", agentID, false, true)
}

// resolveCmdConfig loads the effective project config (applying worktree/project
// overrides) and resolves the working directory for a channel's interactive
// Claude command, writing the per-agent MCP config file as a side effect when
// agentID is set.
func (b *ClaudeCmdBuilder) resolveCmdConfig(channelID, dirPath, parentDirPath, agentID string) (*config.Config, string) {
	baseCfg := b.currentConfig()
	workDir := dirPath
	if workDir == "" {
		workDir = filepath.Join(baseCfg.LoopDir, channelID, "work")
	}
	cfg := baseCfg
	if parentDirPath != "" {
		if merged, err := b.loadWorktreeProjectConfig(workDir, parentDirPath, baseCfg); err == nil {
			cfg = merged
		}
	} else if merged, err := b.loadProjectConfig(workDir, baseCfg); err == nil {
		cfg = merged
	}
	if agentID != "" {
		b.writeAgentMCPConfig(cfg, workDir, channelID, agentID)
	}
	return cfg, workDir
}

// writeAgentMCPConfig writes a per-agent MCP config file with --agent-id.
func (b *ClaudeCmdBuilder) writeAgentMCPConfig(cfg *config.Config, workDir, channelID, agentID string) {
	apiURL := agentAPIBase(cfg)
	loopDir := filepath.Join(workDir, ".loop")
	_ = b.mkdirAll(loopDir, 0o755)
	mcpCfg := buildMCPConfig(channelID, apiURL, workDir, "", agentID, cfg.Memory.Enabled, cfg.Browser.Enabled, cfg.MCPServers)
	data, _ := json.MarshalIndent(mcpCfg, "", "  ")
	configPath := mcpConfigPathForAgent(workDir, channelID, agentID)
	_ = b.writeFile(configPath, data, 0o644)
}
