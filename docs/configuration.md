---
title: Configuration Reference
---
Loop is configured via HJSON files (JSON with comments and trailing commas). This document covers all configuration fields, their defaults, and the merge rules for project-level overrides.

Config files can be edited directly on disk, or through the HTTP API (`GET/PUT /api/config` for global, `GET/PUT /api/config/project` for per-project). The desktop app Settings panel uses the API to provide a schema-driven form editor and a raw JSON editor. See [API Reference: Configuration](api.md#configuration) for endpoint details.

See also: [Docker Container Lifecycle](containers.md), [Task Scheduling](scheduling.md).

---

## Global Config

**Location:** `~/.loop/config.json`

This is the primary configuration file, loaded at startup. All paths below are relative to the `~/.loop/` directory (referred to as `loopDir`).

### All Fields

#### Platforms & Credentials

| Field | Type | Default | Description |
|---|---|---|---|
| `platforms` | `string[]` | **(required)** | One or more of `"discord"`, `"slack"`, `"local"`. Multiple platforms can run simultaneously. |
| `discord_token` | `string` | `""` | Discord bot token. Required when `"discord"` is in `platforms`. |
| `discord_app_id` | `string` | `""` | Discord application ID. Required when `"discord"` is in `platforms`. |
| `discord_guild_id` | `string` | `""` | Discord guild (server) ID. Enables auto-creation of Discord channels via `loop mcp --dir`. |
| `slack_bot_token` | `string` | `""` | Slack bot token (`xoxb-...`). Required when `"slack"` is in `platforms`. |
| `slack_app_token` | `string` | `""` | Slack app-level token (`xapp-...`). Required when `"slack"` is in `platforms`. |

The `"local"` platform requires no external credentials -- it runs with the Electron desktop app as the UI.

#### Authentication

Exactly one of these should be set. OAuth takes precedence if both are provided.

| Field | Type | Default | Description |
|---|---|---|---|
| `claude_code_oauth_token` | `string` | `""` | OAuth token from `claude setup-token`. Uses your Claude subscription. |
| `anthropic_api_key` | `string` | `""` | Anthropic API key. Uses pay-per-token API pricing. |

#### Claude & Agent

| Field | Type | Default | Description |
|---|---|---|---|
| `claude_bin_path` | `string` | `"claude"` | Path to the Claude CLI binary inside containers. |
| `claude_model` | `string` | `"claude-sonnet-4-6"` | Claude model to use. Options: `"claude-opus-4-7"`, `"claude-opus-4-6[1m]"`, `"claude-opus-4-6"`, `"claude-sonnet-4-6"`. |
| `keep_mcp_configs` | `bool` | `false` | When true, preserves MCP config JSON files after container runs. Useful for debugging MCP server configuration. |

#### Storage & Logging

| Field | Type | Default | Description |
|---|---|---|---|
| `db_path` | `string` | `"~/.loop/loop.db"` | SQLite database path. |
| `log_file` | `string` | `"~/.loop/loop.log"` | Log file path. |
| `log_level` | `string` | `"info"` | Log level (`debug`, `info`, `warn`, `error`). |
| `log_format` | `string` | `"text"` | Log format (`text` or `json`). |

#### Container Settings

| Field | Type | Default | Description |
|---|---|---|---|
| `container_image` | `string` | `"loop-agent:latest"` | Docker image for agent containers. |
| `container_timeout_sec` | `int` | `3600` | Maximum execution time per container run (seconds). |
| `container_memory_mb` | `int` | `1024` | Memory limit per container (MB). |
| `container_cpus` | `float` | `1.0` | CPU limit per container (fractional cores). |
| `container_keep_alive_sec` | `int` | `300` | Seconds to keep a finished container before removal (for `docker logs` debugging). |

#### Browser Automation

| Field | Type | Default | Description |
|---|---|---|---|
Browser settings are grouped under `"browser"`:

| Field | Type | Default | Description |
|---|---|---|---|
| `browser.enabled` | `bool` | `true` | Enable Chrome browser automation. When disabled, no Chrome container is started and the `loop-browser` MCP server is not registered. |
| `browser.chrome_image` | `string` | `"loop-chrome:latest"` | Docker image for Chrome sidecar containers. |
| `browser.host_cdp_port` | `int` | `9222` | CDP port for Host mode. Requires `chrome://inspect/#remote-debugging` enabled in Chrome. |

#### Networking & Scheduling

| Field | Type | Default | Description |
|---|---|---|---|
| `api_addr` | `string` | `":8222"` | HTTP API listen address. |
| `poll_interval_sec` | `int` | `30` | How often the scheduler checks for due tasks (seconds). |

#### GitHub

| Field | Type | Default | Description |
|---|---|---|---|
| `github.gh_user` | `string` | `""` | Named `gh` CLI account to use for PR lookups (see `gh auth status`). Empty falls back to whichever account `gh` currently has active. Used by `GET /api/channels/{id}/pr` and the Git panel's PR chip. |

#### Mounts

```jsonc
"mounts": [
  "~/.claude:~/.claude",
  "~/.gitconfig:~/.gitconfig:ro",
  "~/.ssh:~/.ssh:ro",
  "/var/run/docker.sock:/var/run/docker.sock",
  "loop-npmcache:~/.npm",  // Docker named volume
  "loop-gocache:/go"        // Docker named volume
]
```

Format: `host_path:container_path[:mode]`

- `~` is expanded to the host user's home directory on both sides.
- Named volumes (no `/`, `~`, or `.` prefix) are passed to Docker directly.
- Non-existent host paths are silently skipped.
- See [Containers: Mount Processing](containers.md#mount-processing) for full details.

#### Copy Files

```jsonc
"copy_files": ["~/.claude.json"]
```

| Field | Type | Default | Description |
|---|---|---|---|
| `copy_files` | `string[]` | `[]` | Extra files copied into each container (not mounted); each gets its own copy. `~/.claude.json` is always copied automatically (flag-merged) regardless of this list — see [Containers — File Copying](containers.md#file-copying). |

See [Containers: File Copying](containers.md#file-copying).

#### Custom Environment Variables

```jsonc
"envs": {
  "BASH_ENV": "~/.bashrc",
  "ENABLE_LSP_TOOL": false
}
```

| Field | Type | Default | Description |
|---|---|---|---|
| `envs` | `map[string]any` | `null` | Extra environment variables passed to all containers. Values can be any JSON type; non-strings are stringified. `~` in values is expanded. |

#### MCP Servers

```jsonc
"mcp": {
  "servers": {
    "github": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-github"],
      "env": {
        "GITHUB_PERSONAL_ACCESS_TOKEN": "ghp_your_token"
      }
    }
  }
}
```

Each server entry has:

| Field | Type | Description |
|---|---|---|
| `command` | `string` | Executable to run the MCP server. |
| `args` | `string[]` | Command-line arguments. |
| `env` | `map[string]string` | Environment variables for the server process. |

A built-in `"loop"` server is always added unless the user defines one with the same name. See [Containers: MCP Config Generation](containers.md#mcp-config-generation).

#### Task Templates

```jsonc
"task_templates": [
  {
    "name": "daily-summary",
    "description": "Generate a daily summary",
    "schedule": "0 17 * * *",
    "type": "cron",
    "prompt": "Summarize today's activity",
    "auto_delete_sec": 60
  },
  {
    "name": "heartbeat",
    "description": "Periodic health check",
    "schedule": "30m",
    "type": "interval",
    "prompt_path": "heartbeat.md"
  }
]
```

| Field | Type | Description |
|---|---|---|
| `name` | `string` | Unique template identifier. |
| `description` | `string` | Human-readable description shown in template listings. |
| `schedule` | `string` | Cron expression, Go duration, or RFC3339 timestamp (depends on `type`). |
| `type` | `string` | One of `"cron"`, `"interval"`, `"once"`. |
| `prompt` | `string` | Inline prompt text. Mutually exclusive with `prompt_path`. |
| `prompt_path` | `string` | Path to a prompt file, resolved as `~/.loop/templates/{prompt_path}`. Mutually exclusive with `prompt`. |
| `origin_branch` | `string` | Base branch for worktree tasks. If omitted, auto-detected from the parent repo on first run. |
| `update_before_run` | `bool` | When `true`, prepends git fetch/rebase instructions to the prompt before each run. Default: `false`. |
| `auto_delete_sec` | `int` | Seconds after execution to auto-delete the task's thread. `0` disables. |

See [Task Scheduling](scheduling.md) for full details.

#### Prompt Shortcuts

```jsonc
"prompt_shortcuts": [
  {
    "name": "coverage",
    "description": "Run coverage check",
    "prompt": "Run make coverage-check and report results"
  },
  {
    "name": "review",
    "description": "Review uncommitted and branch changes",
    "prompt_path": "review-code.md"
  }
]
```

| Field | Type | Description |
|---|---|---|
| `name` | `string` | Unique shortcut identifier. Shown in the `#` picker in chat. |
| `description` | `string` | Human-readable description shown below the name. |
| `prompt` | `string` | Inline prompt text. Mutually exclusive with `prompt_path`. |
| `prompt_path` | `string` | Path to a prompt file, resolved as `~/.loop/shortcuts/{prompt_path}` (global) or `.loop/shortcuts/{prompt_path}` (project). Mutually exclusive with `prompt`. |

Shortcuts appear in the chat input when the user types `#`. Selecting a shortcut sends its resolved prompt as a message. The API endpoint `GET /api/shortcuts` returns all shortcuts with resolved prompts; pass `?channel_id=<id>` to merge project-level shortcuts. Agents can manage shortcuts via the `prompt_shortcut` MCP tool or the `POST /api/shortcuts` endpoint — add, update, or delete shortcuts in either global or project scope.

#### Bash Shortcuts

```jsonc
"bash_shortcuts": [
  {
    "name": "make lint",
    "description": "Run the linter",
    "command": "make lint"
  },
  {
    "name": "tests",
    "description": "Run unit tests",
    "command_path": "run-tests.sh"
  }
]
```

| Field | Type | Description |
|---|---|---|
| `name` | `string` | Unique shortcut identifier. Shown in the `$` picker on terminal panes. |
| `description` | `string` | Human-readable description shown below the name. |
| `command` | `string` | Inline shell command. Mutually exclusive with `command_path`. |
| `command_path` | `string` | Path to a script file, resolved as `~/.loop/bash-shortcuts/{command_path}` (global) or `.loop/bash-shortcuts/{command_path}` (project). The file contents become the command body. Mutually exclusive with `command`. |

Shortcuts appear in the terminal footer `$` picker. They mount on **Docker Shell** and **Host Shell** panes (raw bash, sent with a trailing newline) and on **Docker Agent** panes (Claude TUI, sent as a bracketed paste + `\r` so multi-line scripts arrive as a single paste buffer). The `#` prompt picker and the `$` bash picker are mutually exclusive on a given pane — Docker Agent panes show `#`, raw-shell panes show `$`. The API endpoint `GET /api/bash-shortcuts` returns shortcuts with resolved command text; pass `?channel_id=<id>` to merge project-level shortcuts. Agents can manage them via the `bash_shortcut` MCP tool or the `POST /api/bash-shortcuts` endpoint.

#### Review

Enables and configures the Review panel (see [review.md](review.md)).

```jsonc
"review": {
  "enabled": true,
  "prompt_path": "review.md"
}
```

| Field | Type | Description |
|---|---|---|
| `enabled` | `bool` | Gates the Review panel. `false` (the default) hides the panel in the FE picker and makes the backend reject `/review/*` requests with `403`. Layered per-global/per-project/per-worktree like `github.gh_user`. |
| `prompt` | `string` | Inline prompt text. Mutually exclusive with `prompt_path`. |
| `prompt_path` | `string` | Path to a prompt file, resolved as `~/.loop/review/{prompt_path}`. Mutually exclusive with `prompt`. |

Both prompt fields empty (the default) uses the daemon's built-in default prompt, which instructs the agent to emit `<review-comment>` blocks for actionable issues only.

#### Workflows

```jsonc
"workflows": [
  {
    "name": "fix-issue",
    "description": "Analyze an issue, plan, implement, and create a PR",
    "inputs": {
      "issue_url": { "description": "Issue URL", "required": true }
    },
    "nodes": [
      { "id": "analyze", "type": "bash", "script": "gh issue view {{.Inputs.issue_url}} --json title,body,labels" },
      { "id": "plan", "type": "prompt", "depends_on": ["analyze"], "prompt": "Create a plan:\n\n{{.NodeOutputs.analyze}}" },
      { "id": "implement", "type": "prompt", "depends_on": ["plan"], "prompt": "Implement:\n\n{{.NodeOutputs.plan}}" },
      { "id": "pr", "type": "prompt", "depends_on": ["implement"], "prompt": "Commit and create a PR" }
    ]
  }
]
```

| Field | Type | Description |
|---|---|---|
| `name` | `string` | Unique workflow identifier. |
| `description` | `string` | Human-readable description. |
| `timeout` | `string` | Go duration (e.g. `"30m"`) that caps total DAG execution time. Run fails with `"workflow timeout exceeded"` on expiry. |
| `inputs` | `map[string]WorkflowInput` | Named inputs with `description`, `required`, and `default` fields. |
| `nodes` | `NodeDef[]` | Ordered list of DAG nodes. |

**Node fields:**

| Field | Type | Description |
|---|---|---|
| `id` | `string` | Unique node identifier within the workflow. |
| `type` | `string` | `"prompt"`, `"bash"`, `"loop"`, or `"approval"`. |
| `depends_on` | `string[]` | IDs of nodes that must complete before this one starts. |
| `prompt` | `string` | Prompt text for `prompt`/`loop` nodes. Supports Go `text/template`. Mutually exclusive with `prompt_path`. |
| `prompt_path` | `string` | Path to a prompt file, resolved as `{loopDir}/workflows/{prompt_path}`. Mutually exclusive with `prompt`. |
| `system_prompt` | `string` | Optional system prompt for `prompt` nodes. Supports templates. |
| `script` | `string` | Shell command(s) for `bash` nodes, passed to `/bin/sh -c`. Any sh-compatible content — one-liners, multi-line scripts, pipelines, heredocs. To run a script file on disk, just invoke it (e.g. `bash workflows/build.sh`); the bash container shares the same mounts as agent containers. Supports templates. |
| `max_iterations` | `int` | Max iterations for `loop` nodes (default: 10). |
| `condition` | `string` | Template for `loop` nodes; stops when it renders `"true"`. |
| `body` | `NodeDef[]` | Optional child nodes for `loop` nodes. When set, each iteration runs the body sequentially instead of re-running the loop node's own prompt. Children may be `prompt` or `bash` only — nested `loop` and `approval` are rejected by the validator. Mutually exclusive with `prompt`/`prompt_path` on the loop node itself. |
| `message` | `string` | Approval message for `approval` nodes. Supports templates. |
| `timeout` | `string` | Go duration (e.g. `"5m"`, `"1h"`). For `approval` nodes: deadline for human response. For `prompt`/`bash`/`loop` nodes: enforced execution deadline via context cancellation. |
| `retry` | `RetryConfig` | Optional retry with `max_retries`, `backoff_base`, `backoff_max`. |
| `when` | `string` | Template that must evaluate to `"true"` for the node to run. Skipped otherwise. |
| `trigger_rule` | `string` | `"all_success"` (default), `"all_done"`, or `"one_success"`. Controls how dependency failures affect this node. |

See [Workflows](workflows.md) for architecture details and the DAG execution model.

#### Workflow Concurrency

```jsonc
"workflow_concurrency": {
  "max_concurrent_runs": 5,
  "max_concurrent_nodes": 10
}
```

| Field | Type | Description |
|---|---|---|
| `max_concurrent_runs` | `int` | Maximum workflow runs executing in parallel. `0` = unlimited. Default: `0`. |
| `max_concurrent_nodes` | `int` | Maximum node goroutines across all active runs. `0` = unlimited. Default: `0`. |

Available at both global and project level. Project values override global values when > 0.

#### Memory

```jsonc
"memory": {
  "enabled": true,
  "paths": ["./memory", "!./memory/plans"],
  "max_chunk_chars": 5000,
  "reindex_interval_sec": 300,
  "embeddings": {
    "provider": "ollama",
    "model": "nomic-embed-text",
    "ollama_url": "http://localhost:11434"
  }
}
```

| Field | Type | Default | Description |
|---|---|---|---|
| `memory.enabled` | `bool` | `false` | Must be explicitly `true` to enable semantic memory search. |
| `memory.paths` | `string[]` | `["./memory"]` | Directories to index. Paths can be absolute or relative to the project work dir. Prefix with `!` to exclude. |
| `memory.max_chunk_chars` | `int` | `5000` | Maximum characters per embedding chunk. |
| `memory.reindex_interval_sec` | `int` | `300` | Periodic re-index interval in seconds (5 minutes default). |
| `memory.embeddings.provider` | `string` | `""` | Embedding provider. Currently only `"ollama"` is supported. |
| `memory.embeddings.model` | `string` | `""` | Embedding model name (e.g. `"nomic-embed-text"`). |
| `memory.embeddings.ollama_url` | `string` | `"http://localhost:11434"` | Ollama API endpoint. |

#### Quality

```jsonc
"quality": {
  "max_files": 25000,
  "exclude_paths": ["docs/**"],
  "complexity": {
    "cyclomatic_t": 10,
    "cognitive_t":  15,
    "nesting_t":    4,
    "params_t":     5,
    "loc_t":        60
  },
  "clones": {
    "min_loc":      5,
    "max_distance": 3
  },
  "rules": {
    "signal_floor":            { "enabled": true, "threshold": 5000 },
    "parse_fail":              { "enabled": true, "threshold": 0.01 },
    "no_import_cycles":        { "enabled": true },
    "complexity_ceiling":      { "enabled": true, "threshold": 10 },
    "complexity_score_floor":  { "enabled": true, "threshold": 0.5 },
    "duplication_ceiling":     { "enabled": true, "threshold": 0.10 }
  }
}
```

Scans are manual: panel "Scan now" button, `loop quality scan`, or the `quality_scan` MCP tool.

| Field | Type | Default | Description |
|---|---|---|---|
| `quality.max_files` | `int` | `25000` | Hard cap on scannable files after exclusions. Over the cap returns a structured `RepoTooLarge` error; no partial scan is produced. |
| `quality.exclude_paths` | `string[]` | `[]` | Doublestar globs appended to the built-in defaults (`.git/`, `node_modules/`, `dist/`, `build/`, `target/`, `vendor/`, `*.min.js`, `*.generated.go`) and the repo's `.gitignore`. |
| `quality.complexity.cyclomatic_t` | `int` | `10` | Soft threshold for McCabe cyclomatic complexity. Above T the dimension scores `T/raw` (0.50 at 2T, 0.10 at 10T, asymptotic to 0). `0` disables the dimension. |
| `quality.complexity.cognitive_t` | `int` | `15` | Soft threshold for Sonar cognitive load. Same `T/raw` curve. |
| `quality.complexity.nesting_t` | `int` | `4` | Soft threshold for max nesting depth. |
| `quality.complexity.params_t` | `int` | `5` | Soft threshold for parameter count. |
| `quality.complexity.loc_t` | `int` | `60` | Soft threshold for function LOC. |
| `quality.clones.min_loc` | `int` | `5` | Minimum function LOC to be considered for clone clustering. Below this, functions are skipped to avoid trivial getter/setter clusters. |
| `quality.clones.max_distance` | `int` | `3` | Max SimHash Hamming distance between two functions in the same cluster. `0` requires exact-shape duplicates; `64` clusters anything. |
| `quality.rules.<name>.enabled` | `bool` | `true` | Per-rule on/off. Built-in rules: `no_import_cycles`, `signal_floor`, `parse_fail`, `complexity_ceiling`, `complexity_score_floor`, `duplication_ceiling`. |
| `quality.rules.signal_floor.threshold` | `float` | `5000` | Lower bound on `quality_signal` (0–10000). Below this, the rule fails. |
| `quality.rules.parse_fail.threshold` | `float` | `0.01` | Maximum fraction of files allowed to fail to parse (0.01 = 1%). |
| `quality.rules.complexity_ceiling.threshold` | `int` | `10` | Maximum number of functions allowed to breach any complexity soft threshold before this rule fails. |
| `quality.rules.complexity_score_floor.threshold` | `float` | `0.5` | Lower bound on the complexity metric's headline score (0–1). |
| `quality.rules.duplication_ceiling.threshold` | `float` | `0.10` | Maximum tolerated `duplicated_loc / total_loc` ratio across clone clusters. |

Config changes drop the engine's parser/graph cache for the affected channel but **do not auto-trigger a rescan** — the panel keeps rendering the previous snapshot until the next manual or live scan rebuilds with the new config. See [Quality](quality.md) for the full surface.

#### Permissions (RBAC)

```jsonc
"permissions": {
  "owners": { "users": ["U12345678"], "roles": ["1234567890123456789"] },
  "members": { "users": [], "roles": [] }
}
```

| Field | Type | Description |
|---|---|---|
| `permissions.owners.users` | `string[]` | User IDs with owner access (full control including permission management). |
| `permissions.owners.roles` | `string[]` | Role IDs with owner access. |
| `permissions.members.users` | `string[]` | User IDs with member access (can trigger bot and manage tasks). |
| `permissions.members.roles` | `string[]` | Role IDs with member access. |

If all config and DB permissions are empty, everyone is treated as an owner (bootstrap mode).

#### Security Gate

The gate runs a seccomp `RET_USER_NOTIF` filter inside each agent container and forwards approve-rule traps to Discord / Slack / the desktop. See [HTTP API: Gate Approvals](api.md#gate-approvals) for the resolve endpoint and [Events: gate.approval_requested](events.md#gateapproval_requested) for the WebSocket payload.

```jsonc
"gates": {
  "rate_limits": { "pending": 30, "per_minute": 60, "total": 500 },
  "audit":       { "retention_days": 30, "verbose": false },

  "agentgate": {
    "enabled": true,
    "default_decision": "allow",
    "path_rules":    [ /* unix-socket connect rules */ ],
    "command_rules": [ /* execve/execveat rules */ ],
    "file_rules":    [ /* openat/renameat2/unlinkat rules */ ]
  },

  "docker_proxy": {
    "enabled": true,
    "default_decision": "approve",
    "http_rules": [ /* per-method/path rules */ ],
    "body_rules": [ /* JSON-body inspection for container-create etc. */ ]
  }
}
```

`gates` is the single umbrella for both enforcement layers — the kernel-level `agentgate` (seccomp) and the application-level `docker_proxy` (Docker HTTP). Shared resources (`rate_limits`, `audit`) live at the umbrella level because both layers feed into the same `agentgate.Manager` for approvals.

| Field | Type | Default | Description |
|---|---|---|---|
| `gates.rate_limits.pending` | `int` | `30` | Max simultaneously-pending prompts per container; new requests over this auto-deny. Shared across both gate layers. |
| `gates.rate_limits.per_minute` | `int` | `60` | Max distinct-target prompts per minute per container. |
| `gates.rate_limits.total` | `int` | `500` | Per-container lifetime cap; exceeded = deny-by-default for the rest of the run. |
| `gates.audit.retention_days` | `int` | `30` | Days of gate decision logs to retain on disk. `FileAuditor` prunes older `agentgate-YYYY-MM-DD.jsonl` files on each rotation; `0` or negative disables pruning. Surfaced into the container as `LOOP_GATE_AUDIT_RETENTION_DAYS`. |
| `gates.audit.verbose` | `bool` | `false` | When `false` (default), `FileAuditor` drops silent allows (policy said allow, nobody was prompted) and cache-hit allows — only denies (silent or prompted) and user-clicked decisions are logged, keeping the trail focused on events operators actually want to review. When `true`, every decision is logged, including silent allows and cache hits — use for debugging rule authoring or exporting a full trace to a SIEM. Surfaced into the container as `LOOP_GATE_AUDIT_VERBOSE=1`. |
| `gates.agentgate.enabled` | `bool` | `true` | Master switch for the seccomp gate. When false, no filter is installed and no gate policy file is bind-mounted. Transitively disables `gates.docker_proxy` unless explicitly overridden. |
| `gates.agentgate.default_decision` | `string` | `"allow"` | `"allow"` or `"approve"`. Fallback when no rule matches. |
| `gates.agentgate.path_rules` | `[]PathRule` | 1 rule (docker.sock → approve) | Rules for unix-socket connects, matched by absolute socket path. User rules are appended to the shipped baseline. |
| `gates.agentgate.command_rules` | `[]CommandRule` | 2 rules (rm -rf / deny, git push etc. approve) | Rules for `execve`/`execveat`, matched by basename glob and argv regex. User rules are appended to the baseline. |
| `gates.agentgate.file_rules` | `[]FileRule` | 8 rules | Rules for file ops (`openat`, `renameat2`, `unlinkat`, `linkat`, `symlinkat`, `fchmodat`, `fchownat`, `mkdirat`), matched by doublestar path glob × operation set. User rules are appended. |

**Rule field shapes:**

```jsonc
// PathRule
{ "pattern": "/var/run/docker.sock", "decision": "approve", "message": "agent wants to use docker" }

// CommandRule — empty commands or args_patterns means "match any"
{ "commands": ["git"], "args_patterns": ["^(push|remote add|reset --hard) .*"], "decision": "approve", "message": "git write op" }

// FileRule — empty paths or operations means "match any"
{ "paths": ["**/.ssh/**"], "operations": ["read","write","create","delete","chmod"], "decision": "deny", "message": "credentials blocked" }
```

`decision` is one of `"allow"`, `"deny"`, or `"approve"`. `approve` blocks the syscall and broadcasts a `gate.approval_requested` event; the UI's three-button card resolves it via [`POST /api/gate/approvals/{id}`](api.md#post-apigateapprovalsid).

| Field | Type | Default | Description |
|---|---|---|---|
| `gates.docker_proxy.enabled` | `bool` | mirrors `gates.agentgate.enabled` | Replaces the container's direct `/var/run/docker.sock` bind with an in-container HTTP proxy (`loop dockerproxy`) that listens on `/var/run/docker.sock` (tmpfs) and reverse-proxies to the real daemon socket at `/var/run/docker.sock.host` while enforcing `http_rules` + `body_rules`. |
| `gates.docker_proxy.default_decision` | `string` | `"approve"` | Fallback when no `http_rules` entry matches. `"allow"`, `"approve"`, or `"deny"`. |
| `gates.docker_proxy.http_rules` | `[]HTTPServiceRule` | 15 rules | Method × path-regex rules. User rules are appended to the baseline (GET reads allowed; POST create/start/exec/update approved; swarm/secrets/plugins denied). |
| `gates.docker_proxy.body_rules` | `[]BodyRule` | 2 rules | JSON-body inspection rules on `POST /containers/create` and `/update`. Baseline denies root-level bind-mounts (`/`, `/etc`, `/root`, `/proc`, `/sys`, `/dev`, `/var/run/docker.sock`), `Privileged`, host-namespace flags, dangerous `CapAdd`, `--security-opt=*unconfined`, `Devices`/`VolumesFrom`, and `MaskedPaths: []`. Body-rule decisions follow the same semantics as `http_rules`: `deny` returns 403 with no prompt, `approve` blocks and prompts the user, `allow` passes through silently. |

#### Desktop (Electron App)

```jsonc
"desktop": {
  "theme": "dark",
  "islands": true,
  "preview_tabs": true,
  "auto_save_on_blur": false,
  "stop_daemon_on_quit": false,
  "font_sizes": {
    "sidebar": 12,
    "chat": 13,
    "terminal": 13,
    "editor": 13,
    "panels": 12
  }
}
```

| Field | Type | Default | Description |
|---|---|---|---|
| `desktop.theme` | `string` | `"dark"` | Color theme. One of `"dark"`, `"light"`, `"claude"`. |
| `desktop.islands` | `bool` | `true` | Islands layout — panels float as rounded cards over a deep canvas with gaps between them. |
| `desktop.preview_tabs` | `bool` | `true` | Single-click opens files in a transient preview tab. Double-click promotes to permanent. |
| `desktop.auto_save_on_blur` | `bool` | `false` | Save open editor tabs when the window loses focus. |
| `desktop.stop_daemon_on_quit` | `bool` | `false` | Uninstalls the daemon service on quit. Re-installed on next app launch. |
| `desktop.font_sizes` | `object` | See above | Per-area font size overrides (in px). Keys: `sidebar`, `chat`, `terminal`, `editor`, `panels`. |

These settings are global-only (not available in project configs). Changes are applied live when saving the config — no restart required.

---

## Project Config

**Location:** `{workDir}/.loop/config.json`

Project-level configs allow per-project overrides. They are loaded and merged with the global config whenever a container is created for a channel that has a custom work directory.

### Available Fields

Not all global fields are available in project configs. The following fields can be set:

| Field | Merge Behavior |
|---|---|
| `mounts` | **Replaces** global mounts entirely. Relative host paths are resolved relative to `workDir`. |
| `copy_files` | **Replaces** global `copy_files` entirely when set. |
| `extra_dirs` | **Replaces** global value when set. In **worktree** configs the parent project's `extra_dirs` are **unioned** with the worktree's (deduped, parent first), so a worktree inherits the same extra roots as its parent channel. |
| `mcp.servers` | **Merged** with global servers. Project servers override global servers with the same name. |
| `envs` | **Merged** with global envs. Project values override global values with the same key. |
| `claude_model` | **Overrides** global value when set. |
| `claude_bin_path` | **Overrides** global value when set. |
| `claude_code_oauth_token` | **Overrides** global auth entirely. Clears `anthropic_api_key`. |
| `anthropic_api_key` | **Overrides** global auth entirely. Clears `claude_code_oauth_token`. |
| `container_image` | **Overrides** global value when set. |
| `container_memory_mb` | **Overrides** global value when set. |
| `container_cpus` | **Overrides** global value when set. |
| `keep_mcp_configs` | **Overrides** global value when set. |
| `memory.paths` | **Appended** to global memory paths. |
| `memory.max_chunk_chars` | **Overrides** global value when set (> 0). |
| `memory.embeddings` | **Overrides** global embeddings config entirely when set. |
| `permissions` | **Replaces** global permissions entirely when set. |
| `task_templates` | **Merged** by name. Project templates override global templates with the same name; new names are appended. |
| `prompt_shortcuts` | **Merged** by name. Project shortcuts override global shortcuts with the same name; new names are appended. |
| `workflows` | **Merged** by name. Project workflows override global workflows with the same name; new names are appended. |
| `workflow_concurrency.max_concurrent_runs` | **Overrides** global value when > 0. |
| `workflow_concurrency.max_concurrent_nodes` | **Overrides** global value when > 0. |
| `browser.enabled` | **Overrides** global value when set. |
| `browser.chrome_image` | **Overrides** global value when set. |
| `browser.host_cdp_port` | **Overrides** global value when set. |
| `github.gh_user` | **Overrides** global value when set. |
| `gates.agentgate.enabled` | **Narrows only**: project may set `false` to disable the gate for this project; it **cannot** re-enable the gate when global `gates.agentgate.enabled` is `false`. Transitively disables `gates.docker_proxy.enabled` when the project turns the gate off. |
| `gates.agentgate.path_rules` / `command_rules` / `file_rules` | **Prepended** to the merged global rules (first-match-wins applies project rules first). **Rules with `decision: "allow"` are rejected at load time** — project configs may only tighten the policy, not loosen it. |
| `gates.agentgate.default_decision` | **Ignored** — global wins unconditionally. |
| `gates.docker_proxy.enabled` | Same narrow rule as `gates.agentgate.enabled`: project can disable (not re-enable). |
| `gates.docker_proxy.http_rules` / `body_rules` | **Prepended** to the merged global rules. Rules with `decision: "allow"` are rejected at load time. |
| `gates.docker_proxy.default_decision` | **Ignored** — global wins. |
| `gates.rate_limits` / `gates.audit` | **Ignored** — global wins unconditionally. |

### Project Config Merge Rules

The merge follows these principles:

- **Replace**: The project value completely replaces the global value (mounts, copy_files, permissions).
- **Merge**: Both global and project values are combined, with project taking precedence on conflicts (MCP servers, envs, task templates, workflows).
- **Append**: Project values are added to the global list (memory paths).
- **Override**: A single scalar value replaces the global one (claude_model, container_image, etc.).
- **Narrow merge**: Security-sensitive fields under `gates` (`agentgate`, `docker_proxy`) have a locked-down merge: project rules prepend, `allow` rules are rejected at load, and `default_decision` / `rate_limits` / `audit` are ignored so a compromised project file cannot loosen global policy.
- **Absent = inherit**: If a field is not set in the project config, the global value is used unchanged.

---

## Complete Example Config

```jsonc
{
  // Required: platform(s) to run
  "platforms": ["local"],

  // Discord credentials (required when platform is "discord")
  "discord_token": "your-discord-bot-token-here",
  "discord_app_id": "your-discord-app-id-here",
  "discord_guild_id": "your-discord-guild-id-here",

  // Slack credentials (required when platform is "slack")
  //"slack_bot_token": "xoxb-your-slack-bot-token",
  //"slack_app_token": "xapp-your-slack-app-token",

  // Authentication (use one; OAuth takes precedence)
  //"claude_code_oauth_token": "sk-ant-your-oauth-token-here",
  //"anthropic_api_key": "sk-ant-your-api-key-here",

  // Storage & logging
  //"db_path": "~/.loop/loop.db",
  //"log_file": "~/.loop/loop.log",
  //"log_level": "info",
  //"log_format": "text",

  // Container settings
  //"container_image": "loop-agent:latest",
  //"container_timeout_sec": 3600,
  //"container_memory_mb": 1024,
  //"container_cpus": 1.0,
  //"container_keep_alive_sec": 300,

  // Scheduling & networking
  //"poll_interval_sec": 30,
  //"api_addr": ":8222",

  // Agent settings
  //"claude_model": "",
  //"claude_bin_path": "claude",
  //"keep_mcp_configs": false, // preserve MCP config files after container runs for debugging

  // Browser automation
  //"browser": {
  //  "enabled": true,
  //  "chrome_image": "loop-chrome:latest",
  //  "host_cdp_port": 9222
  //},

  // GitHub integration (used by the Git panel's PR chip)
  //"github": {
  //  "gh_user": "" // named gh CLI account; empty = gh's currently-active account
  //},

  // RBAC permissions
  //"permissions": {
  //  "owners":  { "users": ["U12345678"], "roles": ["1234567890123456789"] },
  //  "members": { "users": [], "roles": [] }
  //},

  // Semantic memory search
  "memory": {
    "enabled": true,
    "paths": ["./memory", "!./memory/plans"],
    //"max_chunk_chars": 5000,
    //"reindex_interval_sec": 300,
    "embeddings": {
      "provider": "ollama",
      "model": "nomic-embed-text"
      //"ollama_url": "http://localhost:11434"
    }
  },

  // MCP servers
  "mcp": {
    //"servers": {
    //  "github": {
    //    "command": "npx",
    //    "args": ["-y", "@modelcontextprotocol/server-github"],
    //    "env": { "GITHUB_PERSONAL_ACCESS_TOKEN": "ghp_your_token" }
    //  }
    //}
  },

  // Custom environment variables for containers
  "envs": {
    "BASH_ENV": "~/.bashrc",
    "ENABLE_LSP_TOOL": false
  },

  // Files copied into containers (not mounted)
  "copy_files": ["~/.claude.json"],

  // Container mounts
  "mounts": [
    "~/.claude:~/.claude",
    "~/.gitconfig:~/.gitconfig:ro",
    "~/.ssh:~/.ssh:ro",
    "~/.aws:~/.aws",
    "/var/run/docker.sock:/var/run/docker.sock",
    "~/.loop/.bashrc:~/.bashrc:ro",
    "loop-npmcache:~/.npm",
    "loop-uvcache:~/.local/share/uv",
    "loop-cache:~/.cache",
    "loop-gocache:/go",
    "loop-ollama:~/.ollama"
  ],

  // Task templates
  "task_templates": [
    {
      "name": "daily-summary",
      "description": "Generate a daily summary of completed tickets",
      "schedule": "0 17 * * *",
      "type": "cron",
      "prompt": "Generate a summary of all tickets closed today."
    },
    {
      "name": "heartbeat",
      "description": "Periodic health check",
      "schedule": "30m",
      "type": "interval",
      "prompt_path": "heartbeat.md",
      "auto_delete_sec": 60
    }
  ],

  // Prompt shortcuts (triggered via # in chat)
  "prompt_shortcuts": [
    {
      "name": "coverage",
      "description": "Run coverage check",
      "prompt": "Run make coverage-check and report the results"
    },
    {
      "name": "review",
      "description": "Review uncommitted and branch changes",
      "prompt_path": "review-code.md"
    }
  ],

  // Workflows — declarative DAG pipelines
  "workflows": [],

  // Workflow concurrency limits (0 = unlimited)
  "workflow_concurrency": {
    "max_concurrent_runs": 0,
    "max_concurrent_nodes": 0
  },

  // Security gates umbrella. Both layers enabled by default with a baseline
  // deny-list; uncomment to extend. User rules append to the shipped baseline.
  //"gates": {
  //  //"rate_limits": { "pending": 30, "per_minute": 60, "total": 500 },
  //  //"audit":       { "retention_days": 30, "verbose": false },
  //
  //  // Kernel-level seccomp gate.
  //  //"agentgate": {
  //  //  "enabled": true,
  //  //  "default_decision": "allow",
  //  //  //"path_rules":    [ { "pattern": "/custom/socket", "decision": "deny" } ],
  //  //  //"command_rules": [ { "commands": ["curl"], "args_patterns": ["evil\\.com"], "decision": "deny" } ],
  //  //  //"file_rules":    [ { "paths": ["/project-secret/**"], "operations": ["read","write"], "decision": "deny" } ]
  //  //},
  //
  //  // Docker HTTP proxy (enabled automatically when agentgate.enabled). Replaces the
  //  // direct docker.sock bind with an in-container proxy that enforces per-method
  //  // and body-level rules.
  //  //"docker_proxy": {
  //  //  "enabled": true,
  //  //  "default_decision": "approve"
  //  //  //"http_rules": [ { "methods": ["POST"], "paths": ["^/my/route$"], "decision": "deny" } ]
  //  //}
  //}
}
```

---

## Project Example Config

```jsonc
{
  // Claude model override for this project
  //"claude_model": "claude-opus-4-6",

  // Claude binary path override
  //"claude_bin_path": "/usr/local/bin/claude",

  // Auth override (overrides global auth entirely)
  //"claude_code_oauth_token": "sk-ant-project-oauth-token",
  //"anthropic_api_key": "sk-ant-project-api-key",

  // Container overrides
  //"container_image": "loop-agent:latest",
  //"container_memory_mb": 2048,
  //"container_cpus": 2.0,

  // Preserve MCP config files after container runs for debugging
  //"keep_mcp_configs": false,

  // Browser automation override
  //"browser": {
  //  "enabled": false,
  //  "chrome_image": "loop-chrome:latest"
  //},

  // GitHub override (per-project gh CLI account)
  //"github": {
  //  "gh_user": ""
  //},

  // Memory config (paths appended to global; embeddings override global)
  //"memory": {
  //  "paths": ["./docs/architecture.md", "!./docs/wip"],
  //  "max_chunk_chars": 5000,
  //  "embeddings": { "provider": "ollama", "model": "nomic-embed-text" }
  //},

  // Files copied into containers (replaces global when set)
  //"copy_files": ["~/.claude.json"],

  // Project-specific MCP servers (merged with global; project overrides by name)
  //"mcp": {
  //  "servers": {
  //    "my-tool": {
  //      "command": "/path/to/binary",
  //      "args": ["--flag"],
  //      "env": { "API_KEY": "secret" }
  //    }
  //  }
  //},

  // Extra env vars (merged with global; project overrides by key)
  //"envs": {},

  // Project mounts (replaces global mounts; relative paths resolved to project dir)
  //"mounts": [
  //  "~/.claude:~/.claude",
  //  "~/.gitconfig:~/.gitconfig:ro",
  //  "~/.ssh:~/.ssh:ro"
  //],

  // Permissions override (replaces global permissions when set)
  //"permissions": {
  //  "owners":  { "users": [], "roles": [] },
  //  "members": { "users": [], "roles": [] }
  //},

  // Task templates (merged by name; project overrides global templates with same name)
  //"task_templates": [
  //  {
  //    "name": "daily-summary",
  //    "description": "Project-specific daily summary",
  //    "schedule": "0 18 * * *",
  //    "type": "cron",
  //    "prompt": "Summarize today's project activity"
  //  }
  //],

  // Prompt shortcuts (merged by name; project overrides global shortcuts with same name)
  //"prompt_shortcuts": [
  //  {
  //    "name": "lint",
  //    "description": "Run linter",
  //    "prompt": "Run make lint and fix any issues"
  //  }
  //],

  // Workflows (merged by name; project overrides global workflows with same name)
  //"workflows": [
  //  {
  //    "name": "code-review",
  //    "description": "Review branch changes",
  //    "nodes": [
  //      { "id": "diff", "type": "bash", "script": "git diff main...HEAD" },
  //      { "id": "review", "type": "prompt", "depends_on": ["diff"], "prompt": "Review:\n\n{{.NodeOutputs.diff}}" }
  //    ]
  //  }
  //],

  // Workflow concurrency limits (overrides global values)
  //"workflow_concurrency": {
  //  "max_concurrent_runs": 5,
  //  "max_concurrent_nodes": 10
  //},

  // Security gates override for this project only. Can disable (not re-enable);
  // rules prepend to global and must use decision: "deny" or "approve"
  // ("allow" is rejected at load time).
  //"gates": {
  //  //"agentgate": {
  //  //  "enabled": false,
  //  //  //"command_rules": [ { "commands": ["npm"], "args_patterns": ["^publish"], "decision": "deny" } ],
  //  //  //"file_rules":    [ { "paths": ["./secret-vault/**"], "operations": ["read"], "decision": "deny" } ]
  //  //},
  //
  //  //"docker_proxy": {
  //  //  //"http_rules": [ /* project-specific Docker API restrictions */ ]
  //  //}
  //}
}
```
