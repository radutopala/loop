# Configuration Reference

Loop is configured via HJSON files (JSON with comments and trailing commas). This document covers all configuration fields, their defaults, and the merge rules for project-level overrides.

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
| `claude_model` | `string` | `""` | Claude model to use. Defaults to Sonnet. Options: `"claude-opus-4-6"`, `"claude-sonnet-4-6"`. |
| `streaming_enabled` | `bool` | `true` | Stream intermediate Claude turns to chat as they happen. |

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
| `copy_files` | `string[]` | `["~/.claude.json"]` | Files copied into each container (not mounted). Each container gets its own independent copy. |

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
| `auto_delete_sec` | `int` | Seconds after execution to auto-delete the task's thread. `0` disables. |

See [Task Scheduling](scheduling.md) for full details.

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
| `mcp.servers` | **Merged** with global servers. Project servers override global servers with the same name. |
| `envs` | **Merged** with global envs. Project values override global values with the same key. |
| `claude_model` | **Overrides** global value when set. |
| `claude_bin_path` | **Overrides** global value when set. |
| `claude_code_oauth_token` | **Overrides** global auth entirely. Clears `anthropic_api_key`. |
| `anthropic_api_key` | **Overrides** global auth entirely. Clears `claude_code_oauth_token`. |
| `container_image` | **Overrides** global value when set. |
| `container_memory_mb` | **Overrides** global value when set. |
| `container_cpus` | **Overrides** global value when set. |
| `memory.paths` | **Appended** to global memory paths. |
| `memory.max_chunk_chars` | **Overrides** global value when set (> 0). |
| `memory.embeddings` | **Overrides** global embeddings config entirely when set. |
| `permissions` | **Replaces** global permissions entirely when set. |
| `task_templates` | **Merged** by name. Project templates override global templates with the same name; new names are appended. |
| `browser.enabled` | **Overrides** global value when set. |
| `browser.chrome_image` | **Overrides** global value when set. |
| `browser.host_cdp_port` | **Overrides** global value when set. |

### Project Config Merge Rules

The merge follows these principles:

- **Replace**: The project value completely replaces the global value (mounts, copy_files, permissions).
- **Merge**: Both global and project values are combined, with project taking precedence on conflicts (MCP servers, envs, task templates).
- **Append**: Project values are added to the global list (memory paths).
- **Override**: A single scalar value replaces the global one (claude_model, container_image, etc.).
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
  //"streaming_enabled": true,

  // Browser automation
  //"browser": {
  //  "enabled": true,
  //  "chrome_image": "loop-chrome:latest",
  //  "host_cdp_port": 9222
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
  ]
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

  // Browser automation override
  //"browser": {
  //  "enabled": false,
  //  "chrome_image": "loop-chrome:latest"
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
  //]
}
```
