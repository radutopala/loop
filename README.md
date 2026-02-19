# Loop

[![Go](https://img.shields.io/badge/Go-1.26-blue)](https://go.dev/) [![CI](https://github.com/radutopala/loop/actions/workflows/ci.yml/badge.svg)](https://github.com/radutopala/loop/actions/workflows/ci.yml) [![release](https://img.shields.io/github/v/release/radutopala/loop)](https://github.com/radutopala/loop/releases/latest) [![license](https://img.shields.io/github/license/radutopala/loop)](LICENSE)

A Slack/Discord bot powered by Claude that runs AI agents in Docker containers.

## Architecture

```
              Slack / Discord
                     │
              @mention / reply / !loop / DM
                     ▼
                    Bot
                     │
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
          │   MCP: loop
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
```

- **Orchestrator** coordinates message handling, channel registration, session management, and scheduled tasks
- **DockerRunner** mounts the channel's `dir_path` (falling back to `~/.loop/<channelID>/work`) at its original path inside the container, then runs `claude --print`
- **Scheduler** polls for due tasks (cron, interval, once) and executes them via DockerRunner
- **MCP Server** (inside the container) gives Claude tools to schedule/manage tasks — calls loop back through the API server
- **API Server** exposes REST endpoints for task and channel management
- **SQLite** stores channels, messages, scheduled tasks, run logs, and memory file embeddings

## Prerequisites

- macOS (recommended) or Linux
- [Docker Desktop](https://docs.docker.com/desktop/) (macOS) or [Docker Engine](https://docs.docker.com/engine/install/) (Linux)
- A **Slack** bot token and app token, or a **Discord** bot token and application ID
- An Anthropic API key or Claude Code OAuth token (for agent containers)

> **Note:** `loop daemon:start/stop/status` use launchd (macOS-only). On Linux, use `loop serve` directly.

## Getting Started

### Step 1: Install

```sh
# Homebrew
brew install radutopala/tap/loop

# Or from source
go install github.com/radutopala/loop/cmd/loop@latest
```

### Step 2: Create a bot

Choose **Slack** or **Discord**:

<details>
<summary><strong>Slack</strong></summary>

1. Go to https://api.slack.com/apps → **Create New App** → **From a manifest**
2. Select your workspace, choose **JSON**, and paste the contents of `~/.loop/slack-manifest.json` (created by `loop onboard:global`)
3. Click **Create**
4. Go to **Socket Mode** → generate an app-level token with `connections:write` scope → copy the token (starts with `xapp-`)
5. Go to **Install App** → install to workspace → copy the **Bot User OAuth Token** (starts with `xoxb-`)

</details>

<details>
<summary><strong>Discord</strong></summary>

1. Go to https://discord.com/developers/applications and create a new application
2. Under **Bot**, copy the **Bot Token**
3. Under **Bot** → **Privileged Gateway Intents**, enable **Message Content Intent**
4. Copy the **Application ID** from the General Information page
5. Invite the bot to your server (replace `YOUR_APP_ID`):

   ```
   https://discord.com/oauth2/authorize?client_id=YOUR_APP_ID&scope=bot%20applications.commands&permissions=395137059856
   ```

   This grants: View Channels, Send Messages, Read Message History, Manage Channels, Manage Threads, Send Messages in Threads, Create Public Threads, Create Private Threads.

</details>

### Step 3: Initialize global config

```sh
loop onboard:global
```

This creates:
- `~/.loop/config.json` — main configuration file
- `~/.loop/slack-manifest.json` — Slack app manifest (for creating a Slack app)
- `~/.loop/.bashrc` — shell aliases sourced inside containers
- `~/.loop/templates/` — directory for prompt template files (used by `prompt_path`)
- `~/.loop/container/Dockerfile` — agent container image definition
- `~/.loop/container/entrypoint.sh` — container entrypoint script
- `~/.loop/container/setup.sh` — custom build-time setup script (runs once during `docker build`)

### Step 4: Add your credentials

Edit `~/.loop/config.json` and fill in the required fields for your platform:

```jsonc
// Slack:
{
  "platform": "slack",
  "slack_bot_token": "xoxb-your-bot-token",
  "slack_app_token": "xapp-your-app-token"
}

// Discord:
{
  "platform": "discord",
  "discord_token": "your-bot-token-from-step-2",
  "discord_app_id": "your-app-id-from-step-2",
  "discord_guild_id": "your-discord-guild-id" // optional, enables auto-channel creation
}
```

The config file uses HJSON (comments and trailing commas are allowed). See `config.global.example.json` for all available options and their defaults.

### Step 5: Start Loop

```sh
# Run directly (auto-builds the agent Docker image on first run)
loop serve

# Or run as a background daemon (macOS only)
loop daemon:start
loop daemon:status   # check status
loop daemon:stop     # stop
```

### Step 6: Set up a project (optional)

To use Loop with a specific project directory:

```sh
cd /path/to/your/project
loop onboard:local
# optionally: loop onboard:local --api-url http://custom:9999
```

This does four things:
1. Writes `.mcp.json` — registers the Loop MCP server so Claude Code can schedule tasks from your IDE
2. Creates `.loop/config.json` — project-specific overrides (mounts, MCP servers, model, task templates)
3. Creates `.loop/templates/` — directory for project-specific prompt template files
4. Registers a channel for this directory (requires `loop serve` to be running)

## Configuration Reference

### Global Config (`~/.loop/config.json`)

| Field | Default | Description |
|---|---|---|
| `platform` | **(required)** | Chat platform: `"slack"` or `"discord"` |
| `slack_bot_token` | | Slack bot token (required for Slack) |
| `slack_app_token` | | Slack app-level token (required for Slack) |
| `discord_token` | | Discord bot token (required for Discord) |
| `discord_app_id` | | Discord application ID (required for Discord) |
| `discord_guild_id` | `""` | Guild ID for auto-creating Discord channels |
| `log_file` | `"~/.loop/loop.log"` | Daemon log file path |
| `log_level` | `"info"` | Log level (`debug`, `info`, `warn`, `error`) |
| `log_format` | `"text"` | Log format (`text`, `json`) |
| `container_image` | `"loop-agent:latest"` | Docker image for agent containers |
| `container_timeout_sec` | `3600` | Max seconds per agent run |
| `container_memory_mb` | `512` | Memory limit per container (MB) |
| `container_cpus` | `1.0` | CPU limit per container |
| `container_keep_alive_sec` | `300` | Keep-alive duration for idle containers |
| `poll_interval_sec` | `30` | Task scheduler poll interval |
| `claude_model` | `""` | Override Claude model (e.g. `"claude-sonnet-4-5-20250929"`) |
| `claude_bin_path` | `"claude"` | Path to Claude Code binary |
| `mounts` | `[]` | Host directories to mount into containers |
| `mcp` | `{}` | MCP server configurations |
| `task_templates` | `[]` | Reusable task templates |
| `memory` | `{}` | Semantic memory search configuration (see below) |

### Memory

The `memory` block enables semantic search over `.md` files. The daemon indexes files, generates embeddings (via Ollama), and serves search results to MCP processes via its API.

```jsonc
// Global config (~/.loop/config.json)
"memory": {
  "enabled": true,                 // Must be explicitly true
  "paths": [                       // Directories or .md files to index (resolved per project work dir)
    "./memory",
    "!./memory/plans"              // Exclude with ! prefix (gitignore-style)
  ],
  //"max_chunk_chars": 5000,       // Max chars per embedding chunk (increase for models with larger context)
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

When using Ollama, the daemon automatically manages a `loop-ollama` Docker container — starting it on demand and stopping it after 5 minutes of inactivity.

### Container Mounts

The `mounts` array mounts host directories into all agent containers. Format: `"host_path:container_path[:ro]"`

```jsonc
"mounts": [
  "~/.claude:~/.claude",                      // Claude sessions (writable)
  "~/.claude.json:~/.claude.json",            // Claude config (writable)
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

### Per-Project Config (`{project}/.loop/config.json`)

Project config overrides specific global settings. Only these fields are allowed:

| Field | Merge behavior |
|---|---|
| `mounts` | **Replaces** global mounts entirely |
| `mcp` | **Merged** with global; project servers take precedence |
| `task_templates` | **Merged** with global; project overrides by name |
| `claude_model` | **Overrides** global model |
| `claude_bin_path` | **Overrides** global binary path |
| `container_image` | **Overrides** global image |
| `container_memory_mb` | **Overrides** global memory limit |
| `container_cpus` | **Overrides** global CPU limit |
| `memory` | **Merged** — paths appended, embeddings override |

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

To customize: edit `~/.loop/container/Dockerfile`, then `docker rmi loop-agent:latest` and restart.

For development: `make docker-build` builds from `container/Dockerfile` in the repo.

## CLI Commands

| Command | Aliases | Description |
|---|---|---|
| `loop serve` | `s` | Start the bot (Slack or Discord) |
| `loop mcp` | `m` | Run as an MCP server over stdio |
| `loop onboard:global` | `o:global`, `setup` | Initialize global Loop configuration (~/.loop/config.json) |
| `loop onboard:local` | `o:local`, `init` | Register Loop MCP server in current project (.mcp.json) |
| `loop daemon:start` | `d:start`, `up` | Install and start the daemon |
| `loop daemon:stop` | `d:stop`, `down` | Stop and uninstall the daemon |
| `loop daemon:status` | `d:status` | Show daemon status |

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
| `/loop status` | Show bot status |
| `/loop template add <name>` | Load a task template into the current channel |
| `/loop template list` | List available task templates from config |

The bot responds to `@mentions`, replies to its own messages, DMs, and messages prefixed with `!loop`. It auto-joins threads in active channels — tagging the bot in a thread inherits the parent channel's project directory and forks its session so each thread gets independent context.

Agents can trigger work in other channels using the `send_message` MCP tool. The bot can self-reference itself — a message it sends with its own `@mention` will trigger a runner in the target channel. Text mentions like `@LoopBot` are automatically converted to proper platform mentions (Discord `<@ID>`, Slack `<@ID>`). For example, an agent in channel A can ask:

> Send a message to the backend channel asking @LoopBot to check the last commit

The agent will use `search_channels` to find the backend channel, then `send_message` with a bot mention, which triggers a new runner in that channel.

### Task Templates

The config.json file can include a `task_templates` array with reusable task patterns. Use `/loop template add <name>` in Discord to load a template as a scheduled task in the current channel. Templates are idempotent — adding the same template twice to a channel is a no-op.

Each template requires exactly one of:
- `prompt` — inline prompt text
- `prompt_path` — path to a prompt file relative to the `templates/` directory (`~/.loop/templates/` for global, `.loop/templates/` for project)

Example templates in `~/.loop/config.json`:

```jsonc
{
  "task_templates": [
    {
      "name": "tk-auto-worker",
      "description": "Automatically work on ready tickets from tk queue",
      "schedule": "*/5 * * * *",
      "type": "cron",
      "prompt": "Check the tk ticket queue with 'tk ready'. Find any tickets that are marked as ready to start. If you find a ready ticket, use 'tk start <id>' to begin working on it, implement the solution following the ticket's requirements, and when complete use 'tk close <id>' to mark it as done. If no tickets are ready, report the current queue status."
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

## REST API

| Method | Endpoint | Description |
|---|---|---|
| `POST` | `/api/tasks` | Create a scheduled task |
| `GET` | `/api/tasks?channel_id=<id>` | List tasks for a channel |
| `PATCH` | `/api/tasks/{id}` | Update a task (enabled, schedule, type, prompt) |
| `DELETE` | `/api/tasks/{id}` | Delete a task |
| `GET` | `/api/channels?query=<term>` | Search channels and threads (optional query filter) |
| `POST` | `/api/channels` | Ensure/create a channel for a directory |
| `POST` | `/api/channels/create` | Create a channel by name |
| `POST` | `/api/messages` | Send a message to a channel or thread |
| `POST` | `/api/threads` | Create a thread in an existing channel |
| `DELETE` | `/api/threads/{id}` | Delete a thread |
| `POST` | `/api/memory/search` | Semantic search across memory files |
| `POST` | `/api/memory/index` | Re-index memory files |

## MCP Tools

| Tool | Description |
|---|---|
| `schedule_task` | Create a scheduled task (cron/interval/once) |
| `list_tasks` | List all scheduled tasks for this channel |
| `cancel_task` | Cancel a scheduled task by ID |
| `toggle_task` | Enable or disable a scheduled task by ID |
| `edit_task` | Edit a task's schedule, type, and/or prompt |
| `create_channel` | Create a new channel by name |
| `create_thread` | Create a new thread; optional `message` triggers a runner immediately |
| `delete_thread` | Delete a thread by ID |
| `search_channels` | Search for channels and threads by name |
| `send_message` | Send a message to a channel or thread |
| `search_memory` | Semantic search across memory files (ranked by similarity) |
| `index_memory` | Force re-index all memory files |

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

## License

This project is licensed under the [GNU General Public License v3.0](LICENSE).
