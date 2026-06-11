package config

import "sync"

// SchemaProperty describes a single configuration field for UI rendering.
type SchemaProperty struct {
	Type                 string                     `json:"type"`
	Title                string                     `json:"title,omitempty"`
	Description          string                     `json:"description,omitempty"`
	Enum                 []any                      `json:"enum,omitempty"`
	Default              any                        `json:"default,omitempty"`
	Items                *SchemaProperty            `json:"items,omitempty"`
	Properties           map[string]*SchemaProperty `json:"properties,omitempty"`
	AdditionalProperties *SchemaProperty            `json:"additionalProperties,omitempty"`
	XSection             string                     `json:"x-section,omitempty"`
	XGlobalOnly          bool                       `json:"x-global-only,omitempty"`
	XSecret              bool                       `json:"x-secret,omitempty"`
	XOrder               int                        `json:"x-order,omitempty"`
	XStep                float64                    `json:"x-step,omitempty"`
	XPlaceholder         string                     `json:"x-placeholder,omitempty"`
	XWidget              string                     `json:"x-widget,omitempty"`
	XAutoSave            bool                       `json:"x-auto-save,omitempty"`
}

// ConfigSchema is the top-level JSON schema for the config file.
type ConfigSchema struct {
	Type       string                     `json:"type"`
	Properties map[string]*SchemaProperty `json:"properties"`
}

var (
	globalSchema     *ConfigSchema
	globalSchemaOnce sync.Once
)

// GlobalConfigSchema returns the singleton config schema describing all
// configuration fields with their types and UI metadata.
func GlobalConfigSchema() *ConfigSchema {
	globalSchemaOnce.Do(func() {
		globalSchema = buildSchema()
	})
	return globalSchema
}

func buildSchema() *ConfigSchema {
	return &ConfigSchema{
		Type: "object",
		Properties: map[string]*SchemaProperty{
			// ── Claude section ──
			"claude_model": {
				Type:        "string",
				Title:       "Model",
				Description: "Claude model override",
				Enum:        []any{"", "claude-fable-5", "claude-opus-4-8", "claude-opus-4-7", "claude-opus-4-6[1m]", "claude-opus-4-6", "claude-sonnet-4-6"},
				Default:     "claude-sonnet-4-6",
				XSection:    "Claude",
				XOrder:      1,
			},
			"claude_bin_path": {
				Type:         "string",
				Title:        "Binary Path",
				XSection:     "Claude",
				XOrder:       2,
				XPlaceholder: "claude",
			},
			"claude_dangerously_load_development_channels": {
				Type:        "boolean",
				Title:       "MCP Channels (dangerous)",
				Description: "Pass --dangerously-load-development-channels to the Claude CLI so the agent can receive push notifications from other agents. Off by default — Anthropic ships the flag as development-only.",
				Default:     false,
				XSection:    "Claude",
				XOrder:      4,
			},

			// ── Authentication section ──
			"claude_code_oauth_token": {
				Type:     "string",
				Title:    "OAuth Token",
				XSecret:  true,
				XSection: "Authentication",
				XOrder:   1,
			},
			"anthropic_api_key": {
				Type:        "string",
				Title:       "API Key",
				Description: "Used if OAuth not set",
				XSecret:     true,
				XSection:    "Authentication",
				XOrder:      2,
			},

			// ── Container section ──
			"container_image": {
				Type:         "string",
				Title:        "Image",
				XSection:     "Container",
				XOrder:       1,
				XPlaceholder: "loop-agent:latest",
			},
			"container_memory_mb": {
				Type:         "integer",
				Title:        "Memory (MB)",
				Default:      1024,
				XSection:     "Container",
				XOrder:       2,
				XPlaceholder: "1024",
			},
			"container_cpus": {
				Type:         "number",
				Title:        "CPUs",
				Default:      1.0,
				XStep:        0.5,
				XSection:     "Container",
				XOrder:       3,
				XPlaceholder: "1.0",
			},
			"container_timeout_sec": {
				Type:         "integer",
				Title:        "Timeout (sec)",
				XSection:     "Container",
				XOrder:       4,
				XGlobalOnly:  true,
				XPlaceholder: "21600",
			},
			"container_keep_alive_sec": {
				Type:         "integer",
				Title:        "Keep Alive (sec)",
				Description:  "Seconds to keep container alive after run",
				XSection:     "Container",
				XOrder:       5,
				XGlobalOnly:  true,
				XPlaceholder: "300",
			},
			"keep_mcp_configs": {
				Type:        "boolean",
				Title:       "Keep MCP Configs",
				Description: "Preserve MCP config files after runs",
				XSection:    "Container",
				XOrder:      6,
			},

			// ── Browser section (nested object) ──
			"browser": {
				Type:     "object",
				XSection: "Browser",
				Properties: map[string]*SchemaProperty{
					"enabled": {
						Type:    "boolean",
						Title:   "Enabled",
						Default: true,
					},
					"chrome_image": {
						Type:         "string",
						Title:        "Chrome Image",
						XPlaceholder: "loop-chrome:latest",
					},
					"mode": {
						Type:    "string",
						Title:   "Mode",
						Enum:    []any{"docker", "host"},
						Default: "docker",
					},
					"host_cdp_port": {
						Type:         "integer",
						Title:        "Host CDP Port",
						Description:  "Chrome DevTools port (when mode is host)",
						XPlaceholder: "9222",
					},
				},
			},

			// ── Quality section (nested object) ──
			"quality": {
				Type:     "object",
				XSection: "Quality",
				Properties: map[string]*SchemaProperty{
					"max_files": {
						Type:         "integer",
						Title:        "Max Files",
						Description:  "Refuse to scan when scannable file count exceeds this (0 = engine default of 25000)",
						XPlaceholder: "25000",
					},
					"exclude_paths": {
						Type:        "array",
						Title:       "Exclude Paths",
						Description: "Additional path patterns to skip during scan (applied after built-in defaults and .gitignore)",
						Items:       &SchemaProperty{Type: "string"},
					},
					"rules": {
						Type:        "object",
						Title:       "Rules",
						Description: "Per-rule overrides (built-in: no_import_cycles, signal_floor, parse_fail)",
						AdditionalProperties: &SchemaProperty{
							Type: "object",
							Properties: map[string]*SchemaProperty{
								"enabled": {
									Type:  "boolean",
									Title: "Enabled",
								},
								"threshold": {
									Type:        "number",
									Title:       "Threshold",
									Description: "Numeric threshold (e.g. signal_floor=5000, parse_fail=0.01)",
								},
							},
						},
					},
				},
			},

			// ── Memory section (nested object) ──
			"memory": {
				Type:     "object",
				XSection: "Memory",
				Properties: map[string]*SchemaProperty{
					"enabled": {
						Type:  "boolean",
						Title: "Enabled",
					},
					"paths": {
						Type:        "array",
						Title:       "Paths",
						Description: "Memory file paths (prefix with ! to exclude)",
						Items:       &SchemaProperty{Type: "string"},
					},
					"max_chunk_chars": {
						Type:         "integer",
						Title:        "Max Chunk Chars",
						Description:  "Characters per embedding chunk",
						XPlaceholder: "5000",
					},
					"reindex_interval_sec": {
						Type:         "integer",
						Title:        "Reindex Interval (sec)",
						XPlaceholder: "300",
					},
					"embeddings": {
						Type: "object",
						Properties: map[string]*SchemaProperty{
							"provider": {
								Type:  "string",
								Title: "Provider",
								Enum:  []any{"ollama"},
							},
							"model": {
								Type:         "string",
								Title:        "Model",
								XPlaceholder: "nomic-embed-text",
							},
							"ollama_url": {
								Type:         "string",
								Title:        "Ollama URL",
								XPlaceholder: "http://localhost:11434",
							},
						},
					},
				},
			},

			// ── Workspace section ──
			"extra_dirs": {
				Type:        "array",
				Title:       "Extra Directories",
				Description: "Additional workspace directories",
				Items:       &SchemaProperty{Type: "string"},
				XSection:    "Workspace",
				XOrder:      1,
			},
			"mounts": {
				Type:        "array",
				Title:       "Mounts",
				Description: "Container bind mounts (host:container[:ro])",
				Items:       &SchemaProperty{Type: "string"},
				XSection:    "Workspace",
				XOrder:      2,
			},
			"copy_files": {
				Type:        "array",
				Title:       "Copy Files",
				Description: "Files copied into containers",
				Items:       &SchemaProperty{Type: "string"},
				XSection:    "Workspace",
				XOrder:      3,
			},

			// ── Platforms section ──
			"platforms": {
				Type:        "array",
				Title:       "Platforms",
				Description: "Enabled platforms",
				Items:       &SchemaProperty{Type: "string", Enum: []any{"local", "discord", "slack"}},
				XSection:    "Platforms",
				XGlobalOnly: true,
			},

			// ── Discord section ──
			"discord_token": {
				Type:        "string",
				Title:       "Token",
				XSecret:     true,
				XSection:    "Discord",
				XGlobalOnly: true,
			},
			"discord_app_id": {
				Type:        "string",
				Title:       "App ID",
				XSection:    "Discord",
				XGlobalOnly: true,
			},
			"discord_guild_id": {
				Type:        "string",
				Title:       "Guild ID",
				XSection:    "Discord",
				XGlobalOnly: true,
			},

			// ── Slack section ──
			"slack_bot_token": {
				Type:        "string",
				Title:       "Bot Token",
				XSecret:     true,
				XSection:    "Slack",
				XGlobalOnly: true,
			},
			"slack_app_token": {
				Type:        "string",
				Title:       "App Token",
				XSecret:     true,
				XSection:    "Slack",
				XGlobalOnly: true,
			},

			// ── Environment section ──
			"envs": {
				Type:                 "object",
				Title:                "Environment Variables",
				AdditionalProperties: &SchemaProperty{Type: "string"},
				XSection:             "Environment",
			},

			// ── Logging section ──
			"log_level": {
				Type:        "string",
				Title:       "Level",
				Enum:        []any{"info", "debug", "warn", "error"},
				XSection:    "Logging",
				XGlobalOnly: true,
			},
			"log_format": {
				Type:        "string",
				Title:       "Format",
				Enum:        []any{"text", "json"},
				XSection:    "Logging",
				XGlobalOnly: true,
			},
			"log_file": {
				Type:         "string",
				Title:        "File",
				XSection:     "Logging",
				XGlobalOnly:  true,
				XPlaceholder: "~/.loop/loop.log",
			},

			// ── API section ──
			"api_addr": {
				Type:         "string",
				Title:        "Listen Address",
				XSection:     "API",
				XGlobalOnly:  true,
				XPlaceholder: ":8222",
			},
			"db_path": {
				Type:         "string",
				Title:        "Database Path",
				XSection:     "API",
				XGlobalOnly:  true,
				XPlaceholder: "~/.loop/loop.db",
			},
			"poll_interval_sec": {
				Type:         "integer",
				Title:        "Poll Interval (sec)",
				XSection:     "API",
				XGlobalOnly:  true,
				XPlaceholder: "30",
			},

			// ── MCP section (nested object) ──
			"mcp": {
				Type:     "object",
				XSection: "MCP Servers",
				Properties: map[string]*SchemaProperty{
					"servers": {
						Type:  "object",
						Title: "Servers",
						AdditionalProperties: &SchemaProperty{
							Type: "object",
							Properties: map[string]*SchemaProperty{
								"command": {Type: "string", Title: "Command"},
								"args":    {Type: "array", Title: "Args", Items: &SchemaProperty{Type: "string"}},
								"env":     {Type: "object", Title: "Env", AdditionalProperties: &SchemaProperty{Type: "string"}},
							},
						},
					},
				},
			},

			// ── Task Templates section ──
			"task_templates": {
				Type:     "array",
				Title:    "Task Templates",
				XSection: "Task Templates",
				Items: &SchemaProperty{
					Type: "object",
					Properties: map[string]*SchemaProperty{
						"name":            {Type: "string", Title: "Name"},
						"description":     {Type: "string", Title: "Description"},
						"schedule":        {Type: "string", Title: "Schedule", Description: "Cron expression, Go duration, or RFC3339 timestamp"},
						"type":            {Type: "string", Title: "Type", Enum: []any{"cron", "interval", "once", "manual"}},
						"prompt":          {Type: "string", Title: "Prompt"},
						"prompt_path":     {Type: "string", Title: "Prompt Path", Description: "Relative to ~/.loop/templates/"},
						"auto_delete_sec": {Type: "integer", Title: "Auto Delete (sec)"},
					},
				},
			},

			// ── Workflows section ──
			"workflows": {
				Type:     "array",
				Title:    "Workflows",
				XSection: "Workflows",
				Items: &SchemaProperty{
					Type: "object",
					Properties: map[string]*SchemaProperty{
						"name":        {Type: "string", Title: "Name"},
						"description": {Type: "string", Title: "Description"},
						"timeout":     {Type: "string", Title: "Timeout"},
						"inputs": {
							Type:  "object",
							Title: "Inputs",
							AdditionalProperties: &SchemaProperty{
								Type: "object",
								Properties: map[string]*SchemaProperty{
									"description": {Type: "string", Title: "Description"},
									"required":    {Type: "boolean", Title: "Required"},
									"default":     {Type: "string", Title: "Default"},
								},
							},
						},
						"nodes": {
							Type:  "array",
							Title: "Nodes",
							Items: &SchemaProperty{
								Type: "object",
								Properties: map[string]*SchemaProperty{
									"id":             {Type: "string", Title: "ID"},
									"type":           {Type: "string", Title: "Type", Enum: []any{"prompt", "bash", "loop", "approval"}},
									"depends_on":     {Type: "array", Title: "Depends On", Items: &SchemaProperty{Type: "string"}},
									"when":           {Type: "string", Title: "When Condition"},
									"trigger_rule":   {Type: "string", Title: "Trigger Rule", Enum: []any{"all_success", "all_done", "one_success"}},
									"prompt":         {Type: "string", Title: "Prompt", XWidget: "textarea"},
									"prompt_path":    {Type: "string", Title: "Prompt Path", Description: "Relative to ~/.loop/workflows/"},
									"system_prompt":  {Type: "string", Title: "System Prompt", XWidget: "textarea"},
									"model":          {Type: "string", Title: "Model"},
									"script":         {Type: "string", Title: "Script", XWidget: "textarea"},
									"max_iterations": {Type: "integer", Title: "Max Iterations"},
									"condition":      {Type: "string", Title: "Loop Condition"},
									"message":        {Type: "string", Title: "Approval Message"},
									"timeout":        {Type: "string", Title: "Timeout"},
									"retry": {
										Type:  "object",
										Title: "Retry",
										Properties: map[string]*SchemaProperty{
											"max_retries":  {Type: "integer", Title: "Max Retries"},
											"backoff_base": {Type: "string", Title: "Backoff Base"},
											"backoff_max":  {Type: "string", Title: "Backoff Max"},
										},
									},
								},
							},
						},
					},
				},
			},

			"workflow_bash_local": {
				Type:        "boolean",
				Title:       "Workflow Local Bash",
				Description: "Execute workflow bash nodes locally instead of in Docker containers",
				XSection:    "Workflows",
				XGlobalOnly: true,
			},

			// ── Workflow Concurrency section ──
			"workflow_concurrency": {
				Type:     "object",
				Title:    "Workflow Concurrency",
				XSection: "Workflows",
				Properties: map[string]*SchemaProperty{
					"max_concurrent_runs":  {Type: "integer", Title: "Max Concurrent Runs", Description: "Maximum number of workflow runs executing in parallel (0 = unlimited)"},
					"max_concurrent_nodes": {Type: "integer", Title: "Max Concurrent Nodes", Description: "Maximum number of workflow nodes executing in parallel across all runs (0 = unlimited)"},
				},
			},

			// ── Prompt Shortcuts section ──
			"prompt_shortcuts": {
				Type:     "array",
				Title:    "Prompt Shortcuts",
				XSection: "Prompt Shortcuts",
				Items: &SchemaProperty{
					Type: "object",
					Properties: map[string]*SchemaProperty{
						"name":        {Type: "string", Title: "Name"},
						"description": {Type: "string", Title: "Description"},
						"prompt":      {Type: "string", Title: "Prompt", XWidget: "textarea"},
						"prompt_path": {Type: "string", Title: "Prompt Path", Description: "Relative to ~/.loop/shortcuts/"},
					},
				},
			},

			// ── Bash Shortcuts section ──
			"bash_shortcuts": {
				Type:     "array",
				Title:    "Bash Shortcuts",
				XSection: "Bash Shortcuts",
				Items: &SchemaProperty{
					Type: "object",
					Properties: map[string]*SchemaProperty{
						"name":         {Type: "string", Title: "Name"},
						"description":  {Type: "string", Title: "Description"},
						"command":      {Type: "string", Title: "Command", XWidget: "textarea"},
						"command_path": {Type: "string", Title: "Command Path", Description: "Relative to ~/.loop/bash-shortcuts/"},
					},
				},
			},

			// ── Desktop section (Electron app preferences) ──
			"desktop": {
				Type:        "object",
				XSection:    "Desktop",
				XGlobalOnly: true,
				XAutoSave:   true,
				XOrder:      -100,
				Properties: map[string]*SchemaProperty{
					"stop_daemon_on_quit": {
						Type:        "boolean",
						Title:       "Stop Daemon on Quit",
						Description: "Uninstalls the daemon service on quit. It will be re-installed on next app launch.",
					},
					"auto_save_on_blur": {
						Type:        "boolean",
						Title:       "Auto-save on Blur",
						Description: "Save open editor tabs when the window loses focus.",
					},
					"preview_tabs": {
						Type:        "boolean",
						Title:       "Preview Tabs",
						Description: "Single-click opens files in a transient preview tab. Double-click promotes to permanent.",
						Default:     true,
					},
					"islands": {
						Type:        "boolean",
						Title:       "Islands Layout",
						Description: "Panels float as rounded cards over a deep canvas background with gaps between them.",
						Default:     true,
					},
					"theme": {
						Type:    "string",
						Title:   "Theme",
						Enum:    []any{"dark", "light", "claude"},
						XWidget: "theme-picker",
						XOrder:  1,
					},
					"font_sizes": {
						Type:   "object",
						Title:  "Font Sizes",
						XOrder: 2,
						Properties: map[string]*SchemaProperty{
							"sidebar":  {Type: "integer", Title: "Sidebar", Default: 12, XWidget: "stepper", XOrder: 1},
							"chat":     {Type: "integer", Title: "Chat", Default: 13, XWidget: "stepper", XOrder: 2},
							"terminal": {Type: "integer", Title: "Terminal", Default: 13, XWidget: "stepper", XOrder: 3},
							"editor":   {Type: "integer", Title: "Editor", Default: 13, XWidget: "stepper", XOrder: 4},
							"panels":   {Type: "integer", Title: "Panels", Default: 12, XWidget: "stepper", XOrder: 5},
						},
					},
				},
			},

			// ── GitHub section ──
			"github": {
				Type:     "object",
				XSection: "GitHub",
				Properties: map[string]*SchemaProperty{
					"gh_user": {
						Type:        "string",
						Title:       "gh CLI User",
						Description: "Named gh CLI account to use for PR lookups (see `gh auth status`). Per-project override goes in .loop/config.json. Empty uses gh's currently-active account.",
					},
				},
			},

			// ── Permissions section (nested object) ──
			"permissions": {
				Type:     "object",
				XSection: "Permissions",
				Properties: map[string]*SchemaProperty{
					"owners": {
						Type:  "object",
						Title: "Owners",
						Properties: map[string]*SchemaProperty{
							"users": {Type: "array", Title: "Users", Items: &SchemaProperty{Type: "string"}},
							"roles": {Type: "array", Title: "Roles", Items: &SchemaProperty{Type: "string"}},
						},
					},
					"members": {
						Type:  "object",
						Title: "Members",
						Properties: map[string]*SchemaProperty{
							"users": {Type: "array", Title: "Users", Items: &SchemaProperty{Type: "string"}},
							"roles": {Type: "array", Title: "Roles", Items: &SchemaProperty{Type: "string"}},
						},
					},
				},
			},

			// ── Gates section (nested object) ──
			// Two enforcement layers — the seccomp-based agentgate and the
			// Docker HTTP proxy — share rate limits, audit config, and a
			// per-container approval Manager. Decision values across all
			// rules: "allow" (silent), "deny" (silent block), "approve"
			// (prompt user).
			"gates": {
				Type:     "object",
				XSection: "Gates",
				Properties: map[string]*SchemaProperty{
					"rate_limits": {
						Type:  "object",
						Title: "Rate Limits",
						Properties: map[string]*SchemaProperty{
							"pending":    {Type: "integer", Title: "Pending", Description: "Maximum approvals queued at once per container"},
							"per_minute": {Type: "integer", Title: "Per Minute", Description: "Approvals allowed per minute per container"},
							"total":      {Type: "integer", Title: "Total", Description: "Lifetime approval cap per container"},
						},
					},
					"audit": {
						Type:  "object",
						Title: "Audit",
						Properties: map[string]*SchemaProperty{
							"retention_days": {Type: "integer", Title: "Retention Days", Description: "Days to keep gate decision logs"},
							"verbose":        {Type: "boolean", Title: "Verbose", Description: "Log every decision (including silent allows and cache hits)"},
						},
					},
					"agentgate": {
						Type:  "object",
						Title: "Agentgate (seccomp)",
						Properties: map[string]*SchemaProperty{
							"enabled":          {Type: "boolean", Title: "Enabled"},
							"default_decision": {Type: "string", Title: "Default Decision", Enum: []any{"allow", "deny", "approve"}},
							"path_rules": {
								Type:        "array",
								Title:       "Path Rules",
								Description: "Match unix-socket connect targets by absolute path",
								Items: &SchemaProperty{
									Type: "object",
									Properties: map[string]*SchemaProperty{
										"pattern":  {Type: "string", Title: "Pattern"},
										"decision": {Type: "string", Title: "Decision", Enum: []any{"allow", "deny", "approve"}},
										"message":  {Type: "string", Title: "Message"},
									},
								},
							},
							"command_rules": {
								Type:        "array",
								Title:       "Command Rules",
								Description: "Match exec calls by basename and argv pattern",
								Items: &SchemaProperty{
									Type: "object",
									Properties: map[string]*SchemaProperty{
										"commands":      {Type: "array", Title: "Commands", Items: &SchemaProperty{Type: "string"}},
										"args_patterns": {Type: "array", Title: "Args Patterns", Items: &SchemaProperty{Type: "string"}},
										"decision":      {Type: "string", Title: "Decision", Enum: []any{"allow", "deny", "approve"}},
										"message":       {Type: "string", Title: "Message"},
									},
								},
							},
							"file_rules": {
								Type:        "array",
								Title:       "File Rules",
								Description: "Match openat/renameat/unlinkat by resolved path and operation",
								Items: &SchemaProperty{
									Type: "object",
									Properties: map[string]*SchemaProperty{
										"paths":      {Type: "array", Title: "Paths", Items: &SchemaProperty{Type: "string"}},
										"operations": {Type: "array", Title: "Operations", Items: &SchemaProperty{Type: "string"}},
										"decision":   {Type: "string", Title: "Decision", Enum: []any{"allow", "deny", "approve"}},
										"message":    {Type: "string", Title: "Message"},
									},
								},
							},
						},
					},
					"docker_proxy": {
						Type:  "object",
						Title: "Docker Proxy",
						Properties: map[string]*SchemaProperty{
							"enabled":          {Type: "boolean", Title: "Enabled"},
							"default_decision": {Type: "string", Title: "Default Decision", Enum: []any{"allow", "deny", "approve"}},
							"http_rules": {
								Type:        "array",
								Title:       "HTTP Rules",
								Description: "Match Docker HTTP requests by method and path regex",
								Items: &SchemaProperty{
									Type: "object",
									Properties: map[string]*SchemaProperty{
										"methods":  {Type: "array", Title: "Methods", Items: &SchemaProperty{Type: "string"}},
										"paths":    {Type: "array", Title: "Paths", Items: &SchemaProperty{Type: "string"}},
										"decision": {Type: "string", Title: "Decision", Enum: []any{"allow", "deny", "approve"}},
										"message":  {Type: "string", Title: "Message"},
									},
								},
							},
							"body_rules": {
								Type:        "array",
								Title:       "Body Rules",
								Description: "Inspect JSON bodies of Docker requests for container-escape shapes",
								Items: &SchemaProperty{
									Type: "object",
									Properties: map[string]*SchemaProperty{
										"applies_to":     {Type: "string", Title: "Applies To", Description: "Method + path regex (e.g. \"POST ^/containers/create$\")"},
										"content_types":  {Type: "array", Title: "Content Types", Items: &SchemaProperty{Type: "string"}},
										"max_body_bytes": {Type: "integer", Title: "Max Body Bytes"},
										"json_checks": {
											Type:  "array",
											Title: "JSON Checks",
											Items: &SchemaProperty{
												Type: "object",
												Properties: map[string]*SchemaProperty{
													"path":   {Type: "string", Title: "Path", Description: "JSON path (e.g. \"HostConfig.Binds[*]\")"},
													"op":     {Type: "string", Title: "Op", Enum: []any{"source_path_in", "equals", "contains_any", "starts_with_any", "present", "empty_array"}},
													"values": {Type: "array", Title: "Values", Items: &SchemaProperty{Type: "string"}},
												},
											},
										},
										"decision": {Type: "string", Title: "Decision", Enum: []any{"allow", "deny", "approve"}},
										"message":  {Type: "string", Title: "Message"},
									},
								},
							},
						},
					},
				},
			},
		},
	}
}
