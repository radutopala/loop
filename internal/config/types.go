// types.go holds the public Config struct and all supporting type declarations
// for the config package, including their tightly-bound marshal/resolve helpers.
package config

import (
	"fmt"
	"path/filepath"
	"slices"
	"time"

	"github.com/radutopala/loop/internal/types"
)

// MCPServerConfig represents a single MCP server entry in the config.
type MCPServerConfig struct {
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

// TaskTemplate represents a reusable task template with schedule and prompt.
type TaskTemplate struct {
	Name            string `json:"name"`
	Description     string `json:"description"`
	Schedule        string `json:"schedule"`
	Type            string `json:"type"`
	Prompt          string `json:"prompt"`
	PromptPath      string `json:"prompt_path"`
	AutoDeleteSec   int    `json:"auto_delete_sec"`
	Worktree        bool   `json:"worktree,omitempty"`
	OriginBranch    string `json:"origin_branch,omitempty"`
	UpdateBeforeRun bool   `json:"update_before_run,omitempty"`
}

// ResolvePrompt returns the prompt text for the template.
// If Prompt is set, it is returned directly.
// If PromptPath is set, the file is read from {loopDir}/templates/{prompt_path}.
// Exactly one of Prompt or PromptPath must be set.
func (t *TaskTemplate) ResolvePrompt(loopDir string, readFile func(string) ([]byte, error)) (string, error) {
	return resolvePromptField(t.Name, t.Prompt, t.PromptPath, filepath.Join(loopDir, "templates"), readFile)
}

// NodeType represents the type of workflow node.
type NodeType string

const (
	NodeTypePrompt   NodeType = "prompt"
	NodeTypeBash     NodeType = "bash"
	NodeTypeLoop     NodeType = "loop"
	NodeTypeApproval NodeType = "approval"
)

// WorkflowInput defines a workflow input parameter.
type WorkflowInput struct {
	Description string `json:"description" jsonschema:"Human-readable description of the input parameter"`
	Required    bool   `json:"required,omitempty" jsonschema:"If true, the workflow cannot start without this input"`
	Default     string `json:"default,omitempty" jsonschema:"Default value when the input is not provided"`
}

// RetryConfig controls per-node retry behavior.
type RetryConfig struct {
	MaxRetries  int    `json:"max_retries" jsonschema:"Maximum number of retry attempts after the first failure"`
	BackoffBase string `json:"backoff_base,omitempty" jsonschema:"Base backoff duration (Go time.Duration e.g. '5s'); retries double from this up to backoff_max"`
	BackoffMax  string `json:"backoff_max,omitempty" jsonschema:"Maximum backoff duration between retries (Go time.Duration e.g. '5m')"`
}

// NodeDef defines a single node in a workflow DAG.
type NodeDef struct {
	ID            string       `json:"id" jsonschema:"required,Unique node identifier within the workflow"`
	Type          NodeType     `json:"type" jsonschema:"required,Node type: 'prompt' (AI agent), 'bash' (shell script), 'loop' (prompt repeated until condition), or 'approval' (human decision)"`
	DependsOn     []string     `json:"depends_on,omitempty" jsonschema:"IDs of nodes that must complete before this one starts"`
	When          string       `json:"when,omitempty" jsonschema:"Go template expression; node is skipped when it renders 'false'"`
	TriggerRule   string       `json:"trigger_rule,omitempty" jsonschema:"How dependencies gate this node: 'all_success' (default), 'all_done', or 'one_success'"`
	Prompt        string       `json:"prompt,omitempty" jsonschema:"Inline prompt text for 'prompt'/'loop' nodes. Supports Go text/template. Mutually exclusive with prompt_path."`
	PromptPath    string       `json:"prompt_path,omitempty" jsonschema:"Path to a prompt file, resolved as {loopDir}/workflows/{prompt_path}. Mutually exclusive with prompt."`
	SystemPrompt  string       `json:"system_prompt,omitempty" jsonschema:"Optional system prompt for 'prompt' nodes; supports templates"`
	Model         string       `json:"model,omitempty" jsonschema:"Optional Claude model override (e.g. 'claude-sonnet-4-6')"`
	Script        string       `json:"script,omitempty" jsonschema:"Shell command(s) for 'bash' nodes, passed to /bin/sh -c. Any sh-compatible content works: a one-liner, a multi-line script, pipelines, heredocs. To execute a script file on disk, just invoke it (e.g. 'bash workflows/build.sh') — the bash container shares the same mounts as agent containers. Supports Go text/template rendering against workflow inputs and upstream node outputs."`
	MaxIterations int          `json:"max_iterations,omitempty" jsonschema:"Maximum iterations for 'loop' nodes (default 10)"`
	Condition     string       `json:"condition,omitempty" jsonschema:"Go template evaluated after each 'loop' iteration; stops when it renders 'true'"`
	Body          []*NodeDef   `json:"body,omitempty" jsonschema:"Child nodes executed in order per iteration. For 'loop' nodes only. Empty body keeps the legacy self-prompt behavior."`
	Message       string       `json:"message,omitempty" jsonschema:"Approval message shown to the human for 'approval' nodes; supports templates"`
	Timeout       string       `json:"timeout,omitempty" jsonschema:"Per-node timeout as a Go time.Duration (e.g. '5m'). For 'approval' nodes: deadline for human response."`
	Retry         *RetryConfig `json:"retry,omitempty" jsonschema:"Optional retry policy for transient failures"`
}

// ResolvePrompt returns the prompt text for the node.
// If Prompt is set, it is returned directly.
// If PromptPath is set, the file is read from {loopDir}/workflows/{prompt_path}.
// For prompt/loop nodes, exactly one of Prompt or PromptPath must be set.
func (n *NodeDef) ResolvePrompt(loopDir string, readFile func(string) ([]byte, error)) (string, error) {
	return resolvePromptField(n.ID, n.Prompt, n.PromptPath, filepath.Join(loopDir, "workflows"), readFile)
}

// WorkflowConcurrency controls how many workflows and nodes run in parallel.
type WorkflowConcurrency struct {
	MaxConcurrentRuns  int `json:"max_concurrent_runs"`
	MaxConcurrentNodes int `json:"max_concurrent_nodes"`
}

// WorkflowDef defines a declarative workflow as a DAG of nodes.
type WorkflowDef struct {
	Name        string                   `json:"name" jsonschema:"required,Workflow name (unique within its scope)"`
	Description string                   `json:"description,omitempty" jsonschema:"Human-readable description of what the workflow does"`
	Timeout     string                   `json:"timeout,omitempty" jsonschema:"Optional whole-DAG timeout as a Go time.Duration (e.g. '30m')"`
	Inputs      map[string]WorkflowInput `json:"inputs,omitempty" jsonschema:"Named input parameters the workflow expects at run time"`
	Nodes       []NodeDef                `json:"nodes" jsonschema:"required,Ordered list of DAG nodes; execution order is derived from depends_on"`
}

// PromptShortcut defines a reusable prompt that auto-sends in the chat UI.
type PromptShortcut struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Prompt      string `json:"prompt"`
	PromptPath  string `json:"prompt_path"`
}

// ResolvePrompt returns the prompt text for the shortcut.
// If Prompt is set, it is returned directly.
// If PromptPath is set, the file is read from {loopDir}/shortcuts/{prompt_path}.
func (s *PromptShortcut) ResolvePrompt(loopDir string, readFile func(string) ([]byte, error)) (string, error) {
	return resolvePromptField(s.Name, s.Prompt, s.PromptPath, filepath.Join(loopDir, "shortcuts"), readFile)
}

// BashShortcut defines a reusable bash command that auto-runs in a terminal
// (host or agent) when picked from the footer "$" menu.
type BashShortcut struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Command     string `json:"command"`
	CommandPath string `json:"command_path"`
}

// ResolveCommand returns the command text for the shortcut.
// If Command is set, it is returned directly.
// If CommandPath is set, the file is read from {loopDir}/bash-shortcuts/{command_path}.
func (s *BashShortcut) ResolveCommand(loopDir string, readFile func(string) ([]byte, error)) (string, error) {
	return resolvePromptField(s.Name, s.Command, s.CommandPath, filepath.Join(loopDir, "bash-shortcuts"), readFile)
}

// resolvePromptField resolves a prompt from either an inline value or a file path.
// Exactly one of prompt or promptPath must be set.
func resolvePromptField(name, prompt, promptPath, baseDir string, readFile func(string) ([]byte, error)) (string, error) {
	if prompt != "" && promptPath != "" {
		return "", fmt.Errorf("%q: prompt and prompt_path are mutually exclusive", name)
	}
	if prompt == "" && promptPath == "" {
		return "", fmt.Errorf("%q: one of prompt or prompt_path is required", name)
	}
	if prompt != "" {
		return prompt, nil
	}
	path := filepath.Join(baseDir, promptPath)
	data, err := readFile(path)
	if err != nil {
		return "", fmt.Errorf("reading prompt file for %q: %w", name, err)
	}
	return string(data), nil
}

// EmbeddingsConfig configures the embedding provider for semantic memory search.
type EmbeddingsConfig struct {
	Provider  string `json:"provider"`   // "ollama"
	Model     string `json:"model"`      // e.g. "nomic-embed-text"
	OllamaURL string `json:"ollama_url"` // default "http://localhost:11434"
}

// ReviewConfig configures the review-panel agent prompt. Either Prompt
// (inline) or PromptPath ({loopDir}/review/{prompt_path}) may be set;
// both empty means the daemon uses its built-in default prompt.
// Enabled gates the review panel feature: false (the default) makes the
// FE hide the panel from the picker and the backend reject /review/*
// requests with 403. The flag is per global/project/worktree, layered
// the same way as github.gh_user.
type ReviewConfig struct {
	Enabled    bool   `json:"enabled"`
	Prompt     string `json:"prompt"`
	PromptPath string `json:"prompt_path"`
}

// jsonReviewConfig is the JSON representation of ReviewConfig with an
// optional Enabled pointer so we can distinguish "unset" (inherit parent
// layer) from "explicitly false" (force-disable at this layer).
type jsonReviewConfig struct {
	Enabled    *bool  `json:"enabled"`
	Prompt     string `json:"prompt"`
	PromptPath string `json:"prompt_path"`
}

// ResolvePrompt returns the configured prompt text. When neither field
// is set returns ("", nil) so the caller can fall back to a built-in
// default. When both are set returns an error (same exclusivity as
// PromptShortcut).
func (r *ReviewConfig) ResolvePrompt(loopDir string, readFile func(string) ([]byte, error)) (string, error) {
	if r.Prompt == "" && r.PromptPath == "" {
		return "", nil
	}
	return resolvePromptField("review", r.Prompt, r.PromptPath, filepath.Join(loopDir, "review"), readFile)
}

// BrowserConfig groups all browser-related settings.
type BrowserConfig struct {
	Enabled     bool
	ChromeImage string
	Mode        string // "docker" (default) or "host"
	HostCDPPort int    // default 9222
}

// MemoryConfig groups all memory-related settings: enable flag, paths, and embeddings.
type MemoryConfig struct {
	Enabled            bool
	Paths              []string
	MaxChunkChars      int
	ReindexIntervalSec int
	Embeddings         EmbeddingsConfig
}

// QualityConfig groups all architectural-quality settings. Scans are
// triggered manually (via the panel's "Scan now" button or `loop quality
// scan`) or by the agent (via the `quality_scan` MCP tool); there is no
// live-rescan loop.
//
// MaxFiles, ExcludePaths, Rules, Complexity, and Clones are all
// hot-reloaded on every Scan: the engine pulls them via config.Reload,
// and the API server pulls Rules via apiSrv.SetQualityRulesLoader.
// Project-level `.loop/config.json` overrides (including the worktree →
// parent → global layering) are picked up the same way. No daemon
// restart needed.
type QualityConfig struct {
	MaxFiles     int
	ExcludePaths []string
	Rules        map[string]QualityRuleConfig

	// Complexity carries the soft-threshold knobs the per-function
	// complexity score uses. Zero values fall back to
	// metrics.DefaultComplexityConfig() at scan time.
	Complexity QualityComplexityConfig

	// Clones carries the clone-detector knobs (minimum function LOC,
	// SimHash hamming-distance ceiling). Zero values fall back to
	// metrics.DefaultClonesConfig() at scan time.
	Clones QualityClonesConfig
}

// QualityComplexityConfig mirrors metrics.ComplexityConfig in the
// project-config layer. Per-dimension threshold T means "score 1.0 at
// or below T, decay linearly to 0 at 2·T". Zero on any field reverts
// the dimension to its default.
type QualityComplexityConfig struct {
	CyclomaticT int
	CognitiveT  int
	NestingT    int
	ParamsT     int
	LOCT        int
}

// QualityClonesConfig mirrors metrics.ClonesConfig in the project-config
// layer. MinLOC zero falls back to default; MaxDistance zero is treated
// as "exact match only" (legitimate config), so a separate explicit-set
// signal is unnecessary — the json layer uses pointers to disambiguate
// "absent" from "0".
type QualityClonesConfig struct {
	MinLOC      int
	MaxDistance int
}

// QualityRuleConfig is the project-config override for one built-in rule.
// Threshold zero means "use the rule's default"; the rules engine treats
// it as unset (see rules.ruleThreshold).
type QualityRuleConfig struct {
	Enabled   bool
	Threshold float64
}

// GatesConfig groups all approval-based enforcement layers: the kernel
// syscall gate ("agentgate") and the Docker HTTP proxy gate. They share a
// single per-container Manager, bearer token, and approval endpoint — so
// settings that belong to both (RateLimits, Audit) live at the umbrella
// level rather than being duplicated per layer.
type GatesConfig struct {
	RateLimits  types.RateLimits
	Audit       types.AuditConfig
	Agentgate   AgentgateConfig
	DockerProxy DockerProxyConfig
}

// AgentgateConfig groups the kernel seccomp-gate layer settings.
type AgentgateConfig struct {
	Enabled         bool                `json:"enabled"`
	DefaultDecision types.Decision      `json:"default_decision"`
	PathRules       []types.PathRule    `json:"path_rules"`
	CommandRules    []types.CommandRule `json:"command_rules"`
	FileRules       []types.FileRule    `json:"file_rules"`
}

// DockerProxyConfig groups the Docker HTTP proxy gate layer settings.
type DockerProxyConfig struct {
	Enabled         bool                    `json:"enabled"`
	DefaultDecision types.Decision          `json:"default_decision"`
	HTTPRules       []types.HTTPServiceRule `json:"http_rules"`
	BodyRules       []types.BodyRule        `json:"body_rules"`
}

// Config holds all application configuration loaded from config.json.
type Config struct {
	Platforms            []types.Platform
	DiscordToken         string
	DiscordAppID         string
	SlackBotToken        string
	SlackAppToken        string
	ClaudeBinPath        string
	DBPath               string
	LogFile              string
	LogLevel             string
	LogFormat            string
	ContainerImage       string
	ContainerTimeout     time.Duration
	ContainerMemoryMB    int64
	ContainerCPUs        float64
	ContainerKeepAlive   time.Duration
	PollInterval         time.Duration
	APIAddr              string
	ClaudeCodeOAuthToken string
	AnthropicAPIKey      string
	DiscordGuildID       string
	LoopDir              string
	MCPServers           map[string]MCPServerConfig
	TaskTemplates        []TaskTemplate
	Workflows            []WorkflowDef
	WorkflowConcurrency  WorkflowConcurrency
	PromptShortcuts      []PromptShortcut
	BashShortcuts        []BashShortcut
	Mounts               []string
	CopyFiles            []string
	Envs                 map[string]string
	ClaudeModel          string
	// ClaudeDangerouslyLoadDevelopmentChannels gates the
	// `--dangerously-load-development-channels server:loop` CLI flag added to
	// the agent's `claude` invocation. Loop's MCP Channels surface depends on
	// it, but Anthropic ships the flag as development-only; default to off so
	// users opt in deliberately. Hierarchy: global → project → worktree.
	ClaudeDangerouslyLoadDevelopmentChannels bool
	KeepMCPConfigs                           bool
	WorkflowBashLocal                        bool
	Browser                                  BrowserConfig
	Memory                                   MemoryConfig
	Quality                                  QualityConfig
	Permissions                              types.Permissions
	ExtraDirs                                []string
	Desktop                                  DesktopConfig
	Gates                                    GatesConfig
	GitHub                                   GitHubConfig
	Review                                   ReviewConfig
}

// GitHubConfig holds GitHub integration settings. GHUser names a `gh` CLI
// account (per `gh auth status`); when set, PR lookups read its token via
// `gh auth token --user <name>` and invoke `gh` with GH_TOKEN env override —
// avoiding the racy mutation of global gh state via `gh auth switch`.
type GitHubConfig struct {
	GHUser string `json:"gh_user"`
}

// DesktopConfig holds Electron desktop app preferences.
type DesktopConfig struct {
	StopDaemonOnQuit bool              `json:"stop_daemon_on_quit"`
	AutoSaveOnBlur   bool              `json:"auto_save_on_blur"`
	PreviewTabs      bool              `json:"preview_tabs"`
	Islands          bool              `json:"islands"`
	Theme            string            `json:"theme,omitempty"`
	FontSizes        *DesktopFontSizes `json:"font_sizes,omitempty"`
}

// DesktopFontSizes holds per-area font size preferences.
type DesktopFontSizes struct {
	Sidebar  *int `json:"sidebar,omitempty"`
	Chat     *int `json:"chat,omitempty"`
	Terminal *int `json:"terminal,omitempty"`
	Editor   *int `json:"editor,omitempty"`
	Panels   *int `json:"panels,omitempty"`
}

// HasPlatform returns true if the given platform is enabled.
func (c *Config) HasPlatform(p types.Platform) bool {
	return slices.Contains(c.Platforms, p)
}
