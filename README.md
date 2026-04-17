# Loop

[![Go](https://img.shields.io/badge/Go-1.26-blue)](https://go.dev/) [![CI](https://github.com/radutopala/loop/actions/workflows/ci.yaml/badge.svg)](https://github.com/radutopala/loop/actions/workflows/ci.yaml) [![coverage](https://img.shields.io/badge/coverage-100%25-brightgreen)](https://github.com/radutopala/loop/actions/workflows/ci.yaml) [![release](https://img.shields.io/github/v/release/radutopala/loop)](https://github.com/radutopala/loop/releases/latest) [![license](https://img.shields.io/badge/license-Apache--2.0-blue)](LICENSE)

AI agents powered by Claude, running in Docker containers. Use the **desktop app** for a local-first experience, or connect to **Slack** / **Discord** for team collaboration — or run all three at once.

## Architecture

```
     Desktop App          Slack / Discord
     (Electron)                 │
          │             @mention / reply / !loop / DM
          │                     │
          ▼                     ▼
     Local Bot             Slack/Discord Bot
          │                     │
          └──────────┬──────────┘
                     ▼
               Orchestrator ◀──────────────── Scheduler (poll loop)
                     │                              │
            build AgentRequest              due task? execute it
            (messages + session +           (cron / interval / once)
             channel dir_path)                      │
                     │                              │
                     ▼                              ▼
               DockerRunner ◄───────────────────────┘
                     │
          ┌──────────┴──────────┐
          │  create container   │
          │  mount dir_path or  │
          │  ~/.loop/<ch>/work  │
          │  (path-preserving)  │
          └──────────┬──────────┘
                     ▼
              Container (Docker)
          ┌─────────────────────┐
          │ claude --print      │
          │   workDir (project) │
          │   mcpDir  (logs)    │
          │   MCP: loop         │
          │   MCP: loop-browser │──▶ Host API ──▶ Chrome (Docker or Host)
          └─────────┬───────────┘
                    │
         MCP tool calls (schedule, list, cancel…)
                    ▼
              API Server ◀──▶ SQLite
                    │
             /api/memory/search
                    ▼
           Memory Indexer + Embedder
           (Ollama)

  Standalone (no Docker):
     Claude Code ──▶ loop mcp-host-browser ──▶ Host Chrome (CDP)
```

- **Orchestrator** coordinates message handling, channel registration, session management, and scheduled tasks
- **DockerRunner** mounts the channel's `dir_path` (falling back to `~/.loop/<channelID>/work`) at its original path inside the container, then runs `claude --print`
- **Scheduler** polls for due tasks (cron, interval, once) and executes them via DockerRunner
- **Workflow Engine** runs declarative DAG pipelines — parallel fan-out of prompt and bash nodes with dependency tracking, trigger rules, and real-time status events
- **MCP Server** (inside the container) gives Claude tools to schedule/manage tasks and run workflows — calls loop back through the API server
- **Browser** supports Docker mode (headless Chrome container per channel) and Host mode (user's local Chrome via CDP). The desktop app toggles modes per channel; `loop mcp-host-browser` runs standalone without Docker
- **API Server** exposes REST endpoints for task and channel management
- **SQLite** stores channels, messages, scheduled tasks, run logs, and memory file embeddings

## Prerequisites

- macOS, Windows, or Linux
- [Docker Desktop](https://docs.docker.com/desktop/) (macOS / Windows) or [Docker Engine](https://docs.docker.com/engine/install/) (Linux)
- An [Anthropic API key](https://console.anthropic.com/) (recommended) or Claude Code OAuth token

> **Note:** `loop daemon:start/stop/status` use launchd on macOS, Windows services on Windows, and systemd user services on Linux (`~/.config/systemd/user/loop.service`).

## Getting Started

Loop supports three platforms that can run independently or simultaneously:

| Platform | Best for | Requires |
|---|---|---|
| **Desktop App** | Solo development, local-first workflow | Just the app — no bot setup needed |
| **Discord** | Team collaboration with channels and threads | Discord bot token |
| **Slack** | Team collaboration in your existing workspace | Slack bot + app tokens |

Set `"platforms"` in your config to enable one or more: `["local"]`, `["discord"]`, `["local", "discord"]`, etc.

---

### Path A: Desktop App (quickest)

The desktop app gives you a full IDE-like experience — chat, terminal, file editor, diff viewer, session browser — without needing Slack or Discord.

**1. Download and install**

Grab the latest `.dmg` (macOS), `.exe` installer (Windows), or `.AppImage` / `.deb` (Linux) from [Releases](https://github.com/radutopala/loop/releases/latest). The app auto-updates when new versions are available.

Or build from source:
```sh
# Homebrew (installs the CLI; app is a separate download)
brew install radutopala/tap/loop

# From source
go install github.com/radutopala/loop/cmd/loop@main
cd app && npm install && npm run dev   # run the app in dev mode
```

**2. Initialize config**

```sh
loop onboard:global
```

This creates `~/.loop/config.json` and supporting files. Set the platform to local:

```jsonc
{
  "platforms": ["local"]
}
```

**3. Add Claude Code credentials**

Add one of these to `~/.loop/config.json`:

```jsonc
// Option A: API key (recommended — pay-per-token, fully compliant)
{ "anthropic_api_key": "sk-ant-..." }

// Option B: OAuth token (uses your Pro/Max subscription)
// Generate with: claude setup-token
{ "claude_code_oauth_token": "sk-ant-..." }
```

See [Authenticating Claude Code](#authenticating-claude-code) below for details on each option.

**4. Start the daemon**

```sh
loop daemon:start
```

**5. Add a project**

Open the app and click **"+ new"** → **"Open directory..."** to add a project folder. You can add additional directories to a workspace via the **Settings** panel (gear icon on a channel) under the **Directories** section — useful for multi-root projects. Or from the CLI:

```sh
cd /path/to/your/project
loop onboard:local --platform local
```

That's it — start chatting with the agent in the app.

---

### Path B: Slack

<details>
<summary><strong>Setup instructions</strong></summary>

**1. Create a Slack app**

1. Go to https://api.slack.com/apps → **Create New App** → **From a manifest**
2. Select your workspace, choose **JSON**, and paste the contents of [`slack.manifest.json`](https://github.com/radutopala/loop/blob/main/internal/config/slack.manifest.json)
3. Click **Create**
4. Go to **Socket Mode** → generate an app-level token with `connections:write` scope → copy the token (starts with `xapp-`)
5. Go to **Install App** → install to workspace → copy the **Bot User OAuth Token** (starts with `xoxb-`)

**2. Initialize and configure**

```sh
loop onboard:global
# optionally: loop onboard:global --owner-id U12345678
```

Edit `~/.loop/config.json`:

```jsonc
{
  "platforms": ["slack"],
  "slack_bot_token": "xoxb-your-bot-token",
  "slack_app_token": "xapp-your-app-token"
}
```

Add your [Claude Code credentials](#authenticating-claude-code), then:

```sh
loop daemon:start
```

</details>

### Path C: Discord

<details>
<summary><strong>Setup instructions</strong></summary>

**1. Create a Discord bot**

1. Go to https://discord.com/developers/applications and create a new application
2. Under **Bot**, copy the **Bot Token**
3. Under **Bot** → **Privileged Gateway Intents**, enable **Message Content Intent**
4. Copy the **Application ID** from the General Information page
5. Invite the bot to your server (replace `YOUR_APP_ID`):

   ```
   https://discord.com/oauth2/authorize?client_id=YOUR_APP_ID&scope=bot%20applications.commands&permissions=395137059856
   ```

   This grants: View Channels, Send Messages, Read Message History, Manage Channels, Manage Threads, Send Messages in Threads, Create Public Threads, Create Private Threads.

**2. Initialize and configure**

```sh
loop onboard:global
# optionally: loop onboard:global --owner-id U12345678
```

Edit `~/.loop/config.json`:

```jsonc
{
  "platforms": ["discord"],
  "discord_token": "your-bot-token",
  "discord_app_id": "your-app-id",
  "discord_guild_id": "your-guild-id"   // optional, enables auto-channel creation
}
```

Add your [Claude Code credentials](#authenticating-claude-code), then:

```sh
loop daemon:start
```

</details>

### Running multiple platforms

You can run the desktop app alongside Slack or Discord — all platforms share the same daemon, database, and project directories:

```jsonc
{
  "platforms": ["local", "discord"]
}
```

---

### Authenticating Claude Code

Agents inside containers need Claude Code credentials. Loop supports two methods:

#### Option A: Anthropic API key (recommended)

Uses the Anthropic API with pay-per-token pricing. Routes through the [Commercial Terms of Service](https://www.anthropic.com/legal/commercial-terms) — fully compliant with Anthropic's terms for automated usage.

Get an API key from [console.anthropic.com](https://console.anthropic.com/):

```jsonc
{
  "anthropic_api_key": "sk-ant-..."
}
```

#### Option B: OAuth token (subscription)

Uses your Claude Pro/Max subscription. Generate a long-lived token:

```sh
claude setup-token
```

```jsonc
{
  "claude_code_oauth_token": "sk-ant-..."
}
```

> **Note:** `claude login` stores credentials in the macOS keychain, which containers cannot access. Use `claude setup-token` instead.

> **Terms of Service:** Anthropic's [Consumer Terms](https://www.anthropic.com/legal/consumer-terms) (Section 3.7) restrict accessing the Services "through automated or non-human means, whether through a bot, script, or otherwise" unless using an Anthropic API Key or where otherwise explicitly permitted. Loop runs the real Claude Code binary but invokes it programmatically, which may fall under this restriction when using a subscription OAuth token.
>
> **If compliance matters to you, use an API key (Option A).** It routes through the [Commercial Terms](https://www.anthropic.com/legal/commercial-terms), which explicitly permit programmatic access.

> If both are set, `claude_code_oauth_token` takes precedence.

### Setting up a project

To register a project directory with Loop:

```sh
cd /path/to/your/project
loop onboard:local
# optionally: loop onboard:local --platform local   # local-only channel
# optionally: loop onboard:local --api-url http://custom:9999
# optionally: loop onboard:local --owner-id U12345678
```

The `--owner-id` flag sets your user ID as an RBAC owner in the project config. See [Finding your user ID](#finding-your-user-id) below.

This does four things:
1. Writes `.mcp.json` — registers the Loop MCP server so Claude Code can schedule tasks from your IDE
2. Creates `.loop/config.json` — project-specific overrides (mounts, MCP servers, model, task templates)
3. Creates `.loop/templates/` — directory for project-specific prompt template files
4. Registers a channel for this directory (requires the daemon to be running)

### Global onboard details

`loop onboard:global` creates:
- `~/.loop/config.json` — main configuration file
- `~/.loop/slack-manifest.json` — Slack app manifest
- `~/.loop/.bashrc` — shell aliases sourced inside containers
- `~/.loop/templates/` — directory for prompt template files
- `~/.loop/container/Dockerfile` — agent container image definition
- `~/.loop/container/entrypoint.sh` — container entrypoint script
- `~/.loop/container/setup.sh` — custom build-time setup script

### Finding your user ID

**Slack:** Click your profile picture → **Profile** → click the **⋯** menu → **Copy member ID** (looks like `U01ABCDEF`).

**Discord:** Click your profile picture → click the **⋯** menu → **Copy User ID** (looks like `123456789012345678`).

## Configuration Reference

### Global Config (`~/.loop/config.json`)

| Field | Default | Description |
|---|---|---|
| `platforms` | **(required)** | Platforms to enable: `["local"]`, `["discord"]`, `["slack"]`, or any combination |
| `slack_bot_token` | | Slack bot token (required for Slack) |
| `slack_app_token` | | Slack app-level token (required for Slack) |
| `discord_token` | | Discord bot token (required for Discord) |
| `discord_app_id` | | Discord application ID (required for Discord) |
| `discord_guild_id` | `""` | Guild ID for auto-creating Discord channels |
| `claude_code_oauth_token` | `""` | OAuth token passed as `CLAUDE_CODE_OAUTH_TOKEN` env var to containers |
| `anthropic_api_key` | `""` | API key passed as `ANTHROPIC_API_KEY` env var to containers (used when OAuth token is not set) |
| `db_path` | `"~/.loop/loop.db"` | SQLite database file path |
| `log_file` | `"~/.loop/loop.log"` | Daemon log file path |
| `log_level` | `"info"` | Log level (`debug`, `info`, `warn`, `error`) |
| `log_format` | `"text"` | Log format (`text`, `json`) |
| `container_image` | `"loop-agent:latest"` | Docker image for agent containers |
| `container_timeout_sec` | `3600` | Max seconds per agent run |
| `container_memory_mb` | `512` | Memory limit per container (MB) |
| `container_cpus` | `1.0` | CPU limit per container |
| `container_keep_alive_sec` | `300` | Keep-alive duration for idle containers |
| `browser.enabled` | `true` | Enable Chrome browser automation |
| `browser.chrome_image` | `"loop-chrome:latest"` | Docker image for Chrome sidecar containers |
| `browser.host_cdp_port` | `9222` | CDP port for Host mode (requires `chrome://inspect/#remote-debugging` in Chrome) |
| `poll_interval_sec` | `30` | Task scheduler poll interval |
| `claude_model` | `""` | Override Claude model (e.g. `"claude-sonnet-4-6"`) |
| `claude_bin_path` | `"claude"` | Path to Claude Code binary |
| `mounts` | `[]` | Host directories to mount into containers |
| `copy_files` | `["~/.claude.json"]` | Files copied (not mounted) into each container |
| `mcp` | `{}` | MCP server configurations |
| `task_templates` | `[]` | Reusable task templates |
| `prompt_shortcuts` | `[]` | Quick-access prompt shortcuts (triggered via `#` in chat) |
| `workflows` | `[]` | Declarative DAG-based workflow definitions (see [Workflows](#workflows)) |
| `workflow_concurrency` | `{}` | Max concurrent runs and nodes (`max_concurrent_runs`, `max_concurrent_nodes`; 0 = unlimited) |
| `memory` | `{}` | Semantic memory search configuration (see below) |
| `permissions` | `{}` | RBAC permissions: owners and members (see below) |

### Permissions

Loop supports per-channel RBAC with two roles: **owner** and **member**.

- **Owners** can manage scheduled tasks, trigger the bot, and grant/revoke permissions via `/loop allow_user`, `/loop allow_role`, `/loop deny_user`, `/loop deny_role`.
- **Members** can trigger the bot and manage scheduled tasks, but cannot manage permissions.
- Users without any role are denied access.

**Bootstrap mode:** If both config and DB permissions are empty (no grants configured anywhere), everyone is treated as owner. This lets you start using Loop immediately — configure permissions only when you're ready to restrict access.

Permissions can be set in two ways, and the more privileged role wins:

1. **Config file** (`~/.loop/config.json` or `.loop/config.json` per project):

```jsonc
"permissions": {
  "owners":  { "users": ["U12345678"], "roles": ["1234567890123456789"] },
  "members": { "users": [], "roles": [] }
}
```

2. **Slash commands** (stored in the DB per channel):

| Command | Description |
|---|---|
| `/loop allow_user @user [owner\|member]` | Grant a user a role (default: member) |
| `/loop allow_role @role [owner\|member]` | Grant a Discord role a role (Discord only) |
| `/loop deny_user @user` | Remove a user's DB-granted role |
| `/loop deny_role @role` | Remove a Discord role's DB-granted access |
| `/loop iamtheowner` | Self-onboard as channel owner (bootstrap mode only) |

Project config permissions override global config. DB permissions are per-channel and managed via slash commands.

**Thread inheritance:** Threads automatically inherit their parent channel's DB permissions when created or auto-resolved. This means you only need to configure permissions on the parent channel — all threads will share the same access rules.

### Memory

The `memory` block enables semantic search over `.md` files. The daemon indexes files, generates embeddings (via Ollama), and serves search results to MCP processes via its API. The daemon periodically re-indexes memory files to pick up changes (default: every 5 minutes, configurable via `reindex_interval_sec`).

**Why semantic search?** Claude Code's own [auto-memory](https://docs.anthropic.com/en/docs/claude-code/memory) is designed to be concise and loaded directly into the system prompt — no search needed. That works well for a single user on a single project. Loop serves a different use case: agents running across **many projects** with **larger, less curated** content pools (architecture docs, knowledge bases, accumulated notes). Semantic search lets agents find relevant information from content that wouldn't all fit in a single prompt, using conceptual matching rather than exact keywords.

Loop automatically indexes Claude Code's auto-memory directory (`~/.claude/projects/<encoded-path>/memory/`) for each project, plus any additional paths you configure. This means insights Claude saves across sessions are searchable by the bot's agents via the `search_memory` MCP tool — no extra configuration needed.

```jsonc
// Global config (~/.loop/config.json)
"memory": {
  "enabled": true,                 // Must be explicitly true
  "paths": [                       // Directories or .md files to index (resolved per project work dir)
    "./memory",
    "!./memory/plans"              // Exclude with ! prefix (gitignore-style)
  ],
  //"max_chunk_chars": 5000,       // Max chars per embedding chunk (increase for models with larger context)
  //"reindex_interval_sec": 300,   // Periodic re-index interval in seconds (default: 300 = 5 min)
  "embeddings": {
    "provider": "ollama",
    "model": "nomic-embed-text"
    //"ollama_url": "http://localhost:11434"
  }
}
```

Paths prefixed with `!` are exclusions — any file or directory matching the resolved path is skipped during indexing. Uses separator-safe prefix matching (e.g., `!./memory/drafts` won't exclude `./memory/drafts-v2`).

Project config memory settings are **merged** with global — project paths are appended, project embeddings override:

```jsonc
// Project config ({project}/.loop/config.json)
"memory": {
  "paths": [
    "./docs/architecture.md",      // Appended to global paths
    "!./docs/wip"                  // Exclude project-specific paths
  ]
}
```

When using Ollama, the daemon automatically manages a `loop-ollama` Docker container — starting it lazily on the first embedding request and stopping it after 5 minutes of inactivity.

### Container Mounts

The `mounts` array mounts host directories into all agent containers. Format: `"host_path:container_path[:ro]"`

```jsonc
"mounts": [
  "~/.claude:~/.claude",                      // Claude sessions (writable)
  "~/.gitconfig:~/.gitconfig:ro",             // Git identity (read-only)
  "~/.ssh:~/.ssh:ro",                         // SSH keys (read-only)
  "~/.aws:~/.aws",                            // AWS credentials (writable)
  "/var/run/docker.sock:/var/run/docker.sock"  // Docker access
]
```

- Paths starting with `~/` are expanded to the user's home directory
- Non-existent paths are silently skipped
- Docker named volumes are supported (e.g. `"loop-cache:~/.cache"`) — Docker manages them automatically
- The Docker socket's GID is auto-detected and added to the container process
- Project directories (`workDir`) and MCP logs (`mcpDir`) are always mounted automatically at their actual paths

### Copied Files

The `copy_files` array lists host files that are **copied** (not mounted) into each container. This avoids corruption when concurrent containers write to the same file. Default: `["~/.claude.json"]`.

```jsonc
"copy_files": [
  "~/.claude.json"
]
```

- Paths starting with `~/` are expanded to the user's home directory
- Non-existent files are silently skipped

### Per-Project Config (`{project}/.loop/config.json`)

Project config overrides specific global settings. Only these fields are allowed:

| Field | Merge behavior |
|---|---|
| `mounts` | **Replaces** global mounts entirely |
| `copy_files` | **Replaces** global copy_files entirely |
| `mcp` | **Merged** with global; project servers take precedence |
| `task_templates` | **Merged** with global; project overrides by name |
| `prompt_shortcuts` | **Merged** with global; project overrides by name |
| `workflows` | **Merged** with global; project overrides by name |
| `workflow_concurrency` | **Overrides** global values when > 0 |
| `claude_model` | **Overrides** global model |
| `claude_bin_path` | **Overrides** global binary path |
| `claude_code_oauth_token` | **Overrides** global auth (clears API key) |
| `anthropic_api_key` | **Overrides** global auth (clears OAuth token) |
| `container_image` | **Overrides** global image |
| `container_memory_mb` | **Overrides** global memory limit |
| `container_cpus` | **Overrides** global CPU limit |
| `memory` | **Merged** — paths appended, embeddings override |
| `browser.enabled` | **Overrides** global value when set |
| `browser.chrome_image` | **Overrides** global value when set |
| `browser.host_cdp_port` | **Overrides** global value when set |

**Worktree threads** inherit their parent project's config unless the worktree directory has its own `.loop/config.json`. This means you only need to configure mounts, MCP servers, and model once in the parent project — all worktree threads will use the same settings automatically.

Relative paths in project mounts (e.g., `./data`) are resolved relative to the project directory.

```jsonc
{
  "mounts": [
    "./data:/app/data",              // Relative to project dir
    "~/.claude:~/.claude",           // Home expansion works
    "/absolute/path:/app/external"   // Absolute paths too
  ],
  "mcp": {
    "servers": {
      "project-db": {
        "command": "npx",
        "args": ["-y", "@modelcontextprotocol/server-postgres"],
        "env": {"DATABASE_URL": "postgresql://localhost/projectdb"}
      }
    }
  }
}
```

### Container Image

The agent Docker image is auto-built on first `loop serve` / `loop daemon:start` if it doesn't exist. The Dockerfile and entrypoint are embedded in the binary and written to `~/.loop/container/` during `loop onboard:global`.

The default image ships with Go 1.26, Node.js, and common development tools. You can build any custom Dockerfile to suit your stack — edit `~/.loop/container/Dockerfile`, then `docker rmi loop-agent:latest` and restart.

For development: `make docker-build` builds from `container/Dockerfile` in the repo.

## CLI Commands

| Command | Aliases | Description |
|---|---|---|
| `loop serve` | `s` | Start the bot (Slack or Discord) |
| `loop mcp` | `m` | Run as an MCP server over stdio |
| `loop onboard:global` | `o:global`, `setup` | Initialize global Loop configuration (`--owner-id` to set RBAC owner) |
| `loop onboard:local` | `o:local`, `init` | Register Loop MCP server in current project (`--owner-id` to set RBAC owner) |
| `loop daemon:start` | `d:start`, `up` | Install and start the daemon |
| `loop daemon:stop` | `d:stop`, `down` | Stop and uninstall the daemon |
| `loop daemon:restart` | `d:restart`, `restart` | Restart the daemon |
| `loop daemon:status` | `d:status` | Show daemon status |
| `loop mcp-host-browser` | | Standalone MCP server for host Chrome browser automation |
| `loop readme` | `r` | Print the README documentation |

### MCP Host Browser (standalone)

`loop mcp-host-browser` runs as a standalone MCP server that connects directly to your local Chrome via CDP — no Docker, no daemon, no agent container required. It auto-discovers Chrome's DevTools endpoint via the `DevToolsActivePort` file.

**Prerequisites:** enable remote debugging in Chrome at `chrome://inspect/#remote-debugging`.

Add it to your Claude Code MCP config (`.mcp.json` or `settings.json`):

```json
{
  "mcpServers": {
    "browser": {
      "command": "loop",
      "args": ["mcp-host-browser"]
    }
  }
}
```

This gives Claude Code full browser automation tools (navigate, screenshot, click, type, evaluate JS, tab management, console/network capture) on your host Chrome.

### MCP Server Options

```sh
loop mcp --channel-id <id> --api-url <url>   # Attach to existing channel
loop mcp --dir <path> --api-url <url>        # Auto-create channel for directory
```

### Using with Claude Code

`loop mcp` is the same MCP server used in both contexts:

- **On the host** — registered in your local Claude Code so you can schedule tasks from your IDE
- **Inside containers** — automatically injected into every agent container so scheduled tasks can themselves schedule follow-up tasks

When using `--dir`, Loop automatically registers a channel (and creates a channel in the configured guild/workspace) for that directory. The project directory is then mounted at its original path inside agent containers.

To register it in your local Claude Code, run `loop onboard:local` in your project directory. This writes a `.mcp.json` file that Claude Code auto-discovers:

```sh
cd /path/to/your/project
loop onboard:local
# optionally: loop onboard:local --api-url http://custom:9999
# optionally: loop onboard:local --owner-id U12345678
```

## Bot Commands

Both Discord slash commands and Slack `/loop` subcommands use the same syntax:

| Command | Description |
|---|---|
| `/loop schedule <schedule> <type> <prompt>` | Schedule a task (cron/interval/once) |
| `/loop tasks` | List scheduled tasks with status |
| `/loop cancel <task_id>` | Cancel a scheduled task |
| `/loop toggle <task_id>` | Toggle a scheduled task on or off |
| `/loop edit <task_id> [--schedule] [--type] [--prompt]` | Edit a scheduled task |
| `/loop stop` | Stop the currently running agent |
| `/loop status` | Show bot status |
| `/loop readme` | Show the README documentation |
| `/loop template add <name>` | Load a task template into the current channel |
| `/loop template list` | List available task templates from config |
| `/loop iamtheowner` | Self-onboard as channel owner (only when no permissions are configured) |

The bot responds to `@mentions`, replies to its own messages, DMs, and messages prefixed with `!loop`. While processing, a **Stop** button appears that cancels the running agent when clicked. It auto-joins threads in active channels — tagging the bot in a thread inherits the parent channel's project directory and forks its session so each thread gets independent context.

Agents can trigger work in other channels using the `send_message` MCP tool. The bot can self-reference itself — a message it sends with its own `@mention` will trigger a runner in the target channel. Text mentions like `@LoopBot` are automatically converted to proper platform mentions (Discord `<@ID>`, Slack `<@ID>`). For example, an agent in channel A can ask:

> Send a message to the backend channel asking @LoopBot to check the last commit

The agent will use `search_channels` to find the backend channel, then `send_message` with a bot mention, which triggers a new runner in that channel.

### Examples

**Triggering the bot**

```
@LoopBot what's the status of the payments service?    # @mention in a channel
!loop summarize today's changes                        # prefix trigger
# Reply to any bot message to continue the conversation
# DM the bot directly for private interactions
```

**Working with threads**

```
# Tag the bot in an existing thread — it inherits the parent channel's
# project directory and gets its own independent session context
@LoopBot can you review the diff in this thread?

# Ask the bot to create a thread for longer work
@LoopBot investigate the failing CI pipeline and work in a thread
```

**Scheduling tasks**

```
/loop schedule "0 9 * * 1-5" cron Review open PRs and post a summary
/loop schedule "2026-03-01T14:00:00Z" once Run the quarterly DB migration
/loop schedule "30m" interval Check API health and alert on errors
/loop tasks                          # list all scheduled tasks
/loop cancel 3                       # cancel task #3
/loop toggle 5                       # enable/disable task #5
```

**Cross-channel work**

```
# In any channel, ask the agent to reach out to another channel:
@LoopBot send a message to the #backend channel asking to check the last deploy

# The agent uses search_channels + send_message MCP tools under the hood
```

**Reminders**

```
@LoopBot remind me in 30 minutes to check the deployment
@LoopBot remind me in 2 hours to review the PR feedback
@LoopBot remind me tomorrow morning to update the changelog
@LoopBot remind me on Friday at 3pm to send the weekly report
```

**Agent-driven workflows**

```
# Ask the agent to create a thread and do autonomous work
@LoopBot create a new thread and investigate the codebase for possible refactoring, then make a plan
@LoopBot spin up a thread and review all open TODOs in the codebase
@LoopBot start a thread to analyze test coverage gaps and suggest improvements

# Ask the agent to coordinate across channels
@LoopBot check the #backend channel for recent errors and summarize them here
@LoopBot create a thread in #devops asking to rotate the API keys
```

**Stopping a run**

```
# Click the Stop button that appears while the agent is running
# Or use the slash command:
/loop stop
```

### Parallel Work with `tk` Tickets

Loop ships with a ticket-driven workflow that lets you split work across multiple parallel threads, each working in its own git worktree. A dispatcher automatically creates worker threads and chains merge tickets so branches are merged back into main in order.

The **Kanban panel** in the desktop app provides a visual interface for the same ticket system — see all tickets by status, create/edit/delete, and assign worktrees with one click. The CLI (`tk`) and Kanban UI share the same `.tickets/` directory, so changes from either side are reflected everywhere. See [Kanban docs](docs/kanban.md) for the full UI reference.

#### How it works

1. **You ask the bot** to break a task into work tickets:
   ```
   @LoopBot analyze the test files and create tk work tickets to reduce verbosity
     in each test file. Tag them with "work". Don't start working on them yet.
   ```
   The agent creates tickets like:
   ```
   tk create "Reduce db_test.go verbosity" -d "Extract helpers, table-drive tests..." --tags work
   tk create "Reduce api_test.go verbosity" -d "Consolidate error scenarios..." --tags work
   ```

2. **The heartbeat** (`tk-heartbeat` template) polls every 5 minutes. When it detects ready work tickets, it enables the dispatcher.

3. **The dispatcher** (`tk-auto-worker` template) runs every minute when enabled:
   - For each ready **work ticket**: creates a thread with a worker agent that checks out a git worktree (`tk-<id>` branch), implements the solution, commits, and closes the ticket — without merging into main.
   - For each work ticket, also creates a **merge ticket** (tagged `merge`) chained in dependency order so merges happen sequentially.
   - For each ready **merge ticket**: creates a thread with a worker that rebases the branch onto main and fast-forward merges it (`git rebase main && git merge --ff-only`), then cleans up the worktree and branch.

4. **Work happens in parallel** — multiple worker threads run simultaneously in isolated worktrees. Merges happen one at a time in the correct order via the dependency chain.

5. **When no tickets remain**, the heartbeat disables the dispatcher to save resources.

#### Setup

Add both templates to your `~/.loop/config.json`:

```jsonc
{
  "task_templates": [
    {
      "name": "tk-auto-worker",
      "description": "Dispatch ready tickets to worker threads",
      "schedule": "* * * * *",
      "type": "cron",
      "prompt_path": "tk-auto-worker.md"
    },
    {
      "name": "tk-heartbeat",
      "description": "Enable/disable dispatcher based on ready tickets",
      "schedule": "5m",
      "type": "interval",
      "prompt_path": "tk-heartbeat.md",
      "auto_delete_sec": 60
    }
  ]
}
```

The prompt files (`tk-auto-worker.md`, `tk-heartbeat.md`) are installed to `~/.loop/templates/` during `loop onboard:global`.

Then add both templates to your channel:
```
/loop template add tk-heartbeat
/loop template add tk-auto-worker
```

The heartbeat starts polling immediately. The dispatcher stays disabled until work tickets appear.

#### Example workflow

```
# 1. Ask the bot to plan and create work tickets
@LoopBot look at all test files over 1000 lines, create a tk work ticket for
  each one to reduce verbosity with table-driven tests. Tag them with "work".

# 2. The bot creates tickets:
#    tic-a1b2 [work] Reduce db_test.go verbosity
#    tic-c3d4 [work] Reduce api_test.go verbosity
#    tic-e5f6 [work] Reduce bot_test.go verbosity

# 3. Heartbeat detects ready tickets → enables dispatcher
# 4. Dispatcher creates worker threads + merge tickets:
#    Thread "tic-a1b2" → worker creates worktree, implements, commits, closes
#    Thread "tic-c3d4" → worker creates worktree, implements, commits, closes
#    Thread "tic-e5f6" → worker creates worktree, implements, commits, closes
#    (all three run in parallel)

# 5. As work tickets close, merge tickets become ready (in order):
#    Thread "tic-m001" → rebase tk-a1b2, merge --ff-only into main
#    Thread "tic-m002" → rebase tk-c3d4, merge --ff-only into main (after m001)
#    Thread "tic-m003" → rebase tk-e5f6, merge --ff-only into main (after m002)

# 6. All done — heartbeat disables dispatcher
```

### Task Templates

The config.json file can include a `task_templates` array with reusable task patterns. Use `/loop template add <name>` in Discord to load a template as a scheduled task in the current channel. Templates are idempotent — adding the same template twice to a channel is a no-op.

Each template requires exactly one of:
- `prompt` — inline prompt text
- `prompt_path` — path to a prompt file relative to the `templates/` directory (`~/.loop/templates/` for global, `.loop/templates/` for project)

Optional: `auto_delete_sec` — when set (> 0), the agent is instructed to prefix its response with `[EPHEMERAL]` if it has nothing meaningful to report. If the prefix is detected, the thread is renamed (💨 on Discord/Slack, `[ephemeral]` on local) and auto-deleted after the specified number of seconds. Non-ephemeral responses keep the thread permanently (0 = disabled, default). On local platform, recurring tasks reuse the same thread across executions.

Example templates in `~/.loop/config.json`:

```jsonc
{
  "task_templates": [
    {
      "name": "tk-auto-worker",
      "description": "Dispatch ready tickets to worker threads",
      "schedule": "* * * * *",
      "type": "cron",
      "prompt_path": "tk-auto-worker.md"  // loaded from ~/.loop/templates/tk-auto-worker.md
    },
    {
      "name": "tk-heartbeat",
      "description": "Check for ready work tickets; enable/disable tk-auto-worker accordingly",
      "schedule": "5m",
      "type": "interval",
      "prompt_path": "tk-heartbeat.md",  // loaded from ~/.loop/templates/tk-heartbeat.md
      "auto_delete_sec": 60  // auto-delete thread 1 min after execution (0 = disabled)
    },
    {
      "name": "daily-summary",
      "description": "Generate a daily summary of completed tickets",
      "schedule": "0 17 * * *",
      "type": "cron",
      "prompt": "Generate a summary of all tickets closed today using 'tk list --status=closed'. Include ticket IDs, titles, and brief descriptions of what was accomplished."
    },
    {
      "name": "dependency-audit",
      "description": "Check for outdated or vulnerable dependencies",
      "schedule": "0 8 * * 1",
      "type": "cron",
      "prompt_path": "dependency-audit.md"  // loaded from ~/.loop/templates/dependency-audit.md
    }
  ]
}
```

Example `~/.loop/templates/tk-auto-worker.md`:

```markdown
You are a ticket dispatcher. Process ready tickets in two categories:

## A) Work Tickets

Run `tk ready -T work`. Find the merge chain tail via `tk list --status=open -T merge` — the last
ticket is the tail. For each ready work ticket (skip if already in `tk list --status=in_progress`):

1. Create a merge ticket tagged `merge`:
   `tk create "merge-<id>: merge branch tk-<id> into main" -d "Merge worktree branch tk-<id> into main. Run: git checkout tk-<id> && git rebase main, resolve any conflicts, then git checkout main && git merge --ff-only tk-<id>, delete the worktree and branch, then tk close this ticket." --tags merge`
2. Chain with `tk dep add <merge-id> <work-id>` and if a tail exists `tk dep add <merge-id> <tail-id>`.
   Update tail to this merge ticket.
3. Create a thread via `create_thread` MCP tool with the work ticket ID as name and a message telling
   the worker to:
   a) `tk start <id>`
   b) Create a git worktree on branch `tk-<id>`
   c) Implement the solution in the worktree — do NOT create new work tickets (`tk create --tags work`)
   d) Commit and `tk close <id>` — do NOT merge into main

## B) Merge Tickets

Run `tk ready -T merge`. For each ready merge ticket (skip if already in progress), create a thread
via `create_thread` MCP tool with the merge ticket ID as name and a message telling the worker to
follow the ticket description (`tk show <id>`) to merge the branch into main — do NOT create new work tickets (`tk create --tags work`).

---

If no ready tickets exist in either category, do nothing — do NOT send any messages to this channel.
```

#### Project-Level Templates

Project configs (`.loop/config.json`) can define their own `task_templates` that merge with global templates. Project templates override global ones by name, and new templates are appended.

```jsonc
// .loop/config.json
{
  "task_templates": [
    {
      "name": "test-suite",
      "description": "Run full test suite and report failures",
      "schedule": "0 6 * * *",
      "type": "cron",
      "prompt_path": "test-suite.md"  // loaded from .loop/templates/test-suite.md
    }
  ]
}
```

### Prompt Shortcuts

The `prompt_shortcuts` array defines quick-access prompts that users can trigger by typing `#` in the chat input. Each shortcut has a name, optional description, and either an inline prompt or a path to a prompt file.

```jsonc
{
  "prompt_shortcuts": [
    {
      "name": "coverage",
      "description": "Run coverage check",
      "prompt": "Run make coverage-check and report results"
    },
    {
      "name": "review",
      "description": "Review uncommitted and branch changes",
      "prompt_path": "review-code.md"  // loaded from ~/.loop/shortcuts/review-code.md
    }
  ]
}
```

| Field | Type | Description |
|---|---|---|
| `name` | `string` | Unique shortcut identifier. Shown in the `#` picker. |
| `description` | `string` | Human-readable description shown below the name. |
| `prompt` | `string` | Inline prompt text. Mutually exclusive with `prompt_path`. |
| `prompt_path` | `string` | Path to a prompt file, resolved as `~/.loop/shortcuts/{prompt_path}` (global) or `.loop/shortcuts/{prompt_path}` (project). Mutually exclusive with `prompt`. |

Project configs (`.loop/config.json`) can define their own `prompt_shortcuts` that merge with global shortcuts. Project shortcuts override global ones by name, and new shortcuts are appended.

Agents can manage shortcuts via the `prompt_shortcut` MCP tool — list, add, update, or delete shortcuts in either global or project scope.

### Workflows

Workflows are declarative DAG-based pipelines of prompt and bash nodes. They provide repeatable, structured execution with parallel fan-out, dependency tracking, and real-time status events. Defined in the `workflows` array in config, using the same merge-by-name system as `task_templates`.

```jsonc
{
  "workflows": [
    {
      "name": "fix-issue",
      "description": "Analyze a GitHub issue, implement, test, and create a PR",
      "inputs": {
        "issue_url": { "description": "GitHub issue URL", "required": true }
      },
      "nodes": [
        { "id": "analyze", "type": "bash", "script": "gh issue view {{.Inputs.issue_url}} --json title,body,labels" },
        { "id": "plan", "type": "prompt", "depends_on": ["analyze"], "prompt": "Create a plan:\n\n{{.NodeOutputs.analyze}}" },
        { "id": "implement", "type": "prompt", "depends_on": ["plan"], "prompt": "Implement:\n\n{{.NodeOutputs.plan}}" },
        { "id": "test", "type": "bash", "depends_on": ["implement"], "script": "make test 2>&1" },
        { "id": "pr", "type": "prompt", "depends_on": ["test"], "prompt": "Create a PR. Tests:\n{{.NodeOutputs.test}}" }
      ]
    }
  ]
}
```

Nodes support four types: `prompt` (AI agent), `bash` (shell script), `loop` (iterative prompt), and `approval` (human gate). Independent nodes execute in parallel. Templates use Go `text/template` syntax with access to `{{.Inputs.name}}` and `{{.NodeOutputs.node_id}}`. Use `workflow_concurrency` to limit parallel runs and nodes. Both workflows and individual nodes support enforced `timeout` deadlines (Go duration strings like `"30m"` or `"5m"`).

Agents can start workflows via the `run_workflow` MCP tool, and the Workflows panel in the desktop app provides an interactive DAG graph visualization for monitoring runs with per-node status, output, and real-time updates. Available as both a global overlay and an embedded per-channel split panel. See [Workflows docs](docs/workflows.md) for the full reference.

## Desktop App

Loop includes a cross-platform desktop app for macOS, Windows, and Linux, built with Electron + React. Download from [Releases](https://github.com/radutopala/loop/releases/latest) or build from source.

### Features

- **Chat** — send messages, stream agent responses in real-time, search messages (Cmd+K), copy-on-select, persistent drafts across channel switches, message history navigation (ArrowUp/ArrowDown), prompt shortcuts (`#` picker)
- **Terminal** — interactive xterm.js terminals for agent containers and host shells, with horizontal/vertical splits
- **File editor** — CodeMirror-powered editor with syntax highlighting, markdown preview, in-file search, context menus, auto-save, and directory creation/deletion
- **Git panel** — git changes with per-file addition/deletion stats, maximizable to full width, expandable context rows between hunks (GitLab-style "load more"), branch-to-branch diff mode for comparing any two branches, renamed file support with `{old => new}` notation, commit history view with branch selector and lazy pagination, worktrees tab for managing git worktrees (import, navigate, delete)
- **Kanban panel** — visual ticket board with Open / In Progress / Closed columns backed by filesystem-based `tk` tickets (`.tickets/` directory). Create, edit, delete tickets with full metadata (priority, type, assignee, tags, dependencies, design notes, acceptance criteria). One-click "Assign Worktree" atomically claims a ticket, creates a git worktree, spawns a thread, and auto-starts an agent. Live updates via WebSocket. See [Kanban](docs/kanban.md)
- **Workflows panel** — start, monitor, and manage DAG-based workflow runs. Available as both a global overlay panel and an embedded split panel per channel. Two-pane layout with run list and interactive DAG graph visualization — nodes are rendered on an SVG canvas with a dot grid background, pan/zoom (scroll + Ctrl+Scroll), cursor-anchored zoom, minimap with draggable viewport, and zoom controls. Nodes show status (pending, running, success, failed, skipped, paused), type badges, retry counts, and elapsed time with connected dependency edges. Click a node to expand its output in a 50/50 split below the graph. Approval widget for paused runs. Real-time updates via WebSocket events. See [Workflows](docs/workflows.md)
- **Containers panel** — global view of all Docker containers (agent, shell, chrome) with real-time status lifecycle (running → stopped → pending-removal), type labels, scheduled removal countdown, and live updates via WebSocket events
- **Memory panel** — browse and search semantic memory files
- **Custom layouts** — named split-pane workspaces with drag-to-resize, saved per channel. Create, rename, delete, and restore default layouts from the tab bar
- **Islands layout** — panels float as rounded cards over a deep canvas background with gaps between them. Enable via `"islands": true` in the `desktop` config section (on by default)
- **Multi-window** — open multiple windows (Cmd+N), each navigating independently
- **Sidebar** — browse channels and threads, create new ones, batch-delete, see running status (green dot), and open directories directly from the sidebar
- **Auto-update** — checks for new releases every 30 minutes, download and install with one click
- **Deep links** — `loop://channel/<id>` opens the app directly to a channel
- **Branch picker** — switch branches from the header bar, create worktree threads, import existing worktrees. Threads show branches only; parent channels show branches + worktrees in a 50/50 split. Double-click a branch name to copy it
- **Browser** — live Chrome screencast via WebSocket, click/type/navigate directly in the browser pane. Two panel types: Docker Browser (headless container) and Host Browser (local Chrome via CDP), mutually exclusive per layout
- **Playground** — live interactive sandbox where agents generate HTML/CSS/JS and it renders in a sandboxed iframe. Multiple named playgrounds with two scopes: global (`~/.loop/playground/`, shared across channels) and project (`.loop/playground/` in the channel's working directory). Multi-instance panels, hot-reloads on updates, console capture, import maps, multi-file support with relative imports. Agents use `playground` + `playground_file` MCP tools
- **Settings** — schema-driven config form with typed controls (toggles, dropdowns, number inputs, password fields, arrays, key-value editors) plus a raw JSON editor, with Form/JSON toggle and unsaved changes confirmation
- **Plan mode** — run agents in read-only preview mode via Claude Code's `EnterPlanMode` tool
- **Agent activity** — see model info, tool use, and completion summaries in the chat view
- **Message queue** — processing indicators and trigger quote showing which message is being handled, with timestamp. A collapsible popup above the input surfaces waiting messages and lets you remove them from the queue before the agent picks them up

### Platforms

| Platform | Format | Auto-update |
|---|---|---|
| macOS (Apple Silicon) | `.dmg` (arm64) | Yes |
| macOS (Intel) | `.dmg` (x64) | Yes |
| Windows (x64) | `.exe` (NSIS installer) | Yes |
| Windows (ARM64) | `.exe` (NSIS installer) | Yes |
| Linux (x64) | `.AppImage`, `.deb` | AppImage only |
| Linux (ARM64) | `.AppImage`, `.deb` | AppImage only |

Release builds for macOS are signed with a Developer ID Application certificate and notarized by Apple.

### Build from source

Requires [Node.js 22+](https://nodejs.org/).

```sh
# Development
cd app && npm install && npm run dev

# Build and install to /Applications (macOS)
make app-install
```

## REST API

| Method | Endpoint | Description |
|---|---|---|
| `POST` | `/api/tasks` | Create a scheduled task |
| `GET` | `/api/tasks?channel_id=<id>` | List tasks for a channel |
| `GET` | `/api/tasks/{id}` | Get a single task by ID |
| `PATCH` | `/api/tasks/{id}` | Update a task (enabled, schedule, type, prompt) |
| `DELETE` | `/api/tasks/{id}` | Delete a task |
| `POST` | `/api/tasks/{id}/run` | Run a task immediately (409 if already running) |
| `GET` | `/api/tasks/{id}/runs` | List recent run logs for a task |
| `GET` | `/api/channels?query=<term>` | Search channels and threads (optional query filter) |
| `POST` | `/api/channels` | Ensure/create a channel for a directory |
| `POST` | `/api/channels/create` | Create a channel by name |
| `POST` | `/api/channels/ensure-all` | Ensure channels exist for all configured directories |
| `DELETE` | `/api/channels/{id}` | Delete a channel and its child threads |
| `POST` | `/api/messages` | Send a message to a channel or thread |
| `POST` | `/api/threads` | Create a thread in an existing channel |
| `DELETE` | `/api/threads/{id}` | Delete a thread (cleans up worktree and branch if applicable) |
| `GET` | `/api/channels/{id}/branches` | List branches and worktrees for a channel |
| `POST` | `/api/channels/{id}/branches/switch` | Switch git branch |
| `POST` | `/api/channels/{id}/branches/create` | Create and checkout a new branch |
| `POST` | `/api/worktrees` | Create a git worktree as a new thread |
| `POST` | `/api/worktrees/import` | Import an existing worktree as a thread |
| `DELETE` | `/api/worktrees` | Remove a git worktree from disk and optionally delete its thread |
| `GET` | `/api/channels/{id}/roots` | List all root directories (primary + extra from project config) |
| `GET` | `/api/channels/{id}/diff` | Get git diff (working changes, or `?source=X&target=Y` for branch diff) |
| `GET` | `/api/channels/{id}/messages` | List messages with cursor-based pagination |
| `POST` | `/api/commands` | Send a slash command to a channel |
| `POST` | `/api/memory/search` | Semantic search across memory files |
| `POST` | `/api/memory/index` | Re-index memory files |
| `GET` | `/api/readme` | Get the Loop README documentation |
| `PUT` | `/api/playground?name=...` | Create/update a playground (html, title, description) |
| `GET` | `/api/playground?name=...` | Get playground metadata |
| `DELETE` | `/api/playground?name=...` | Delete entire playground |
| `GET` | `/api/playground/items` | List all playgrounds |
| `PUT` | `/api/playground/file?name=...&path=...` | Write a file |
| `GET` | `/api/playground/file?name=...&path=...` | Read a file |
| `DELETE` | `/api/playground/file?name=...&path=...` | Delete a file |
| `GET` | `/api/playground/files?name=...` | List files in a playground |
| `POST` | `/api/browser/action` | Browser automation (navigate, tabs, screenshot, input, etc.) |
| `POST` | `/api/browser/mode` | Switch browser mode (docker/host) |
| `GET` | `/api/tickets` | List tickets for a project directory (filter by status, tag, assignee, type) |
| `POST` | `/api/tickets` | Create a ticket |
| `GET` | `/api/tickets/{id}` | Get a single ticket by ID |
| `PATCH` | `/api/tickets/{id}` | Update ticket fields (status, title, description, deps, etc.) |
| `DELETE` | `/api/tickets/{id}` | Delete a ticket |
| `POST` | `/api/tickets/{id}/assign` | Assign a worktree to a ticket (claim, create worktree, start agent) |
| `GET` | `/api/workflows` | List workflow definitions from merged config |
| `POST` | `/api/workflows/runs` | Start a new workflow run |
| `GET` | `/api/workflows/runs` | List workflow runs (optional `channel_id`, `limit`) |
| `GET` | `/api/workflows/runs/{id}` | Get run detail with node statuses |
| `POST` | `/api/workflows/runs/{id}/resume` | Resume a paused workflow (body: `{"response": "..."}`) |
| `POST` | `/api/workflows/runs/{id}/cancel` | Cancel a running workflow |
| `DELETE` | `/api/workflows/runs/{id}` | Permanently delete a workflow run from the database |
| `POST` | `/api/workflows/runs/{id}/retry` | Retry a completed/failed workflow run (creates a new run) |
| `GET` | `/api/shortcuts` | List resolved prompt shortcuts (optional `channel_id` for project merge) |
| `POST` | `/api/shortcuts` | Add, update, or delete a prompt shortcut (global or project scope) |
| `GET` | `/api/config/schema` | JSON Schema for all config fields |
| `GET` | `/api/config` | Get global config (parsed + raw HJSON) |
| `PUT` | `/api/config` | Save global config |
| `GET` | `/api/config/project?channel_id=<id>` | Get project config for a channel |
| `PUT` | `/api/config/project?channel_id=<id>` | Save project config for a channel |
| `GET` | `/api/ws` | WebSocket for real-time event streaming |
| `GET` | `/api/ws/terminal` | WebSocket for interactive terminal sessions |
| `GET` | `/api/ws/browser` | WebSocket for browser screencast frames and input |

## MCP Tools

| Tool | Description |
|---|---|
| `schedule_task` | Create a scheduled task (cron/interval/once) |
| `list_tasks` | List all scheduled tasks for this channel |
| `show_task` | Show details of a scheduled task by ID |
| `cancel_task` | Cancel a scheduled task by ID |
| `toggle_task` | Enable or disable a scheduled task by ID |
| `edit_task` | Edit a task's schedule, type, and/or prompt |
| `create_channel` | Create a new channel by name |
| `create_thread` | Create a new thread; optional `message` triggers a runner immediately |
| `delete_thread` | Delete a thread by ID (cleans up worktree and branch if applicable) |
| `search_channels` | Search for channels and threads by name |
| `send_message` | Send a message to a channel or thread |
| `search_memory` | Semantic search across memory files (ranked by similarity) |
| `index_memory` | Force re-index all memory files |
| `get_readme` | Get the full Loop README documentation |
| `playground` | Manage playgrounds (create/update/delete) |
| `playground_file` | Manage files within a playground (create/update/read/delete/list) |
| `prompt_shortcut` | Manage prompt shortcuts (list, add, update, delete) in global or project scope |
| | **Workflows** |
| `run_workflow` | Start a workflow run by name with optional inputs |
| `get_workflow_run` | Get run status and node outputs |
| `list_workflows` | List available workflow definitions |
| `list_workflow_runs` | List recent workflow runs |
| `cancel_workflow_run` | Cancel a running workflow |
| `resume_workflow_run` | Resume a paused workflow with an optional response |
| | **Browser Automation** |
| `navigate` | Navigate the browser to a URL |
| `read_page` | Get the accessibility tree of interactive elements |
| `computer` | Perform click, type, key, scroll, move, screenshot, drag actions |
| `screenshot` | Take a screenshot of the current page |
| `find` | Find interactive elements by natural language query |
| `form_input` | Fill in a form field (click, clear, type) |
| `evaluate` | Evaluate JavaScript in the page context |
| `get_page_text` | Get all text content from the page |
| `read_console_messages` | Read captured browser console messages |
| `read_network_requests` | Read captured network requests |
| `list_tabs` | List all open browser tabs |
| `new_tab` | Open a new browser tab |
| `switch_tab` | Switch to a browser tab by target ID |
| `close_tab` | Close a browser tab |
| `resize_window` | Resize the browser viewport |

## Development

Requires [Go 1.26+](https://go.dev/dl/).

```sh
make build            # Build the loop binary
make install          # Install to $GOPATH/bin
make docker-build     # Build the Docker agent image (from local source)
make restart          # Reinstall + restart daemon
make test             # Run tests
make lint             # Run linter
make coverage-check   # Enforce 100% test coverage
make coverage         # Generate HTML coverage report
make app-install      # Build Electron app and copy to /Applications
make app-dev-docker   # Run Vite dev server in Docker (browser-only, no Electron)
make clean            # Remove build artifacts
```

### Integration Tests

Integration tests run against the real platform APIs to verify bot behavior end-to-end. Both Discord and Slack suites are available — each creates temporary channels, runs all tests, and cleans up on teardown.

#### Slack

The Slack integration tests run against the real Slack API using Socket Mode. They require a dedicated Slack app with bot, app-level, and user tokens.

**Setup:**

1. Create a Slack app (or reuse the one from your main config) with these additional **User Token Scopes**: `channels:write`, `channels:read`, `chat:write`, `reactions:read`, `im:write`
2. Add the following to `~/.loop/config.integration.json`:

```json
{
  "slack_bot_token": "xoxb-...",
  "slack_app_token": "xapp-...",
  "slack_user_token": "xoxp-..."
}
```

Alternatively, set environment variables: `SLACK_BOT_TOKEN`, `SLACK_APP_TOKEN`, `SLACK_USER_TOKEN`.

The user token is optional — tests requiring it (e.g. DM events) will be skipped if not provided.

#### Discord

The Discord integration tests run against the real Discord API using a bot token. They require a Discord bot with appropriate permissions in a test guild (server).

**Setup:**

1. Use an existing Discord bot or create one with the required permissions (View Channels, Send Messages, Manage Channels, Manage Threads, Read Message History, Send/Create Threads)
2. Add the following to `~/.loop/config.integration.json`:

```json
{
  "discord_token": "MTA...",
  "discord_app_id": "...",
  "discord_guild_id": "..."
}
```

Alternatively, set environment variables: `DISCORD_BOT_TOKEN`, `DISCORD_APP_ID`, `DISCORD_GUILD_ID`.

#### Running

```sh
make test-integration
```

Both suites create temporary channels, run all tests, and clean up on teardown. Tests are skipped automatically when the required credentials are not configured.

## Documentation

Full documentation is available in the [docs/](docs/README.md) directory. For common issues such as LaunchAgents permissions or corporate proxy TLS errors during Docker builds, see the [Troubleshooting Guide](docs/troubleshooting.md).

## License

This project is licensed under the [Apache License 2.0](LICENSE).
