# Loop

A Discord bot powered by Claude that runs AI agents in Docker containers.

## Architecture

```
Discord ─▶ Bot ─▶ Orchestrator ─▶ DockerRunner ─▶ Container (claude --print)
                       │
                   Scheduler ◀─▶ SQLite
                       │
                   API Server ◀─▶ MCP Server
```

- **Orchestrator** coordinates message handling, channel registration, session management, and scheduled tasks
- **DockerRunner** executes `claude --print` directly inside Docker containers — plain text in, plain text out
- **Scheduler** runs cron, interval, and one-shot tasks
- **API Server** exposes REST endpoints for task and channel management
- **MCP Server** provides tools for Claude agents to manage scheduled tasks
- **SQLite** stores channels, messages, scheduled tasks, and run logs

## Setup

### Prerequisites

- Go 1.25+
- Docker
- A Discord bot token and application ID
- Claude Code CLI (`@anthropic-ai/claude-code`)

### Discord Bot Setup

1. Create an application at https://discord.com/developers/applications
2. Copy the **Application ID** and **Bot Token** into your `config.json`
3. Invite the bot to your server using this OAuth2 URL (replace `YOUR_APP_ID`):

   ```
   https://discord.com/oauth2/authorize?client_id=YOUR_APP_ID&scope=bot%20applications.commands&permissions=68624
   ```

4. After the bot is running, use `/loop register` in each channel where you want it to respond

### Configuration

Create `~/.loop/config.json` (HJSON — comments and trailing commas are allowed):

```jsonc
{
  // Required
  "discord_token": "your-discord-bot-token",
  "discord_app_id": "your-discord-app-id",

  // Optional — enables auto-creation of Discord channels via `loop mcp --dir`
  "discord_guild_id": "your-discord-guild-id",

  // Optional (defaults shown)
  "log_level": "info",
  "log_format": "text",
  "container_image": "loop-agent:latest",
  "container_timeout_sec": 300,
  "container_memory_mb": 512,
  "container_cpus": 1.0,
  "poll_interval_sec": 30,
  "mount_allowlist": ["/path/one", "/path/two"],
}
```

### Build

```sh
# Build the loop binary
make build

# Install to GOPATH/bin
make install

# Build the Docker agent image
make docker-build
```

### Run

```sh
# Run directly
loop serve

# Or run as a background daemon
loop daemon start
loop daemon status
loop daemon stop

# Shortcut: reinstall + restart daemon
make restart
```

## CLI Commands

| Command | Alias | Description |
|---|---|---|
| `loop serve` | `s` | Start the Discord bot |
| `loop mcp` | `m` | Run as an MCP server over stdio |
| `loop daemon start` | `d up` | Install and start the daemon |
| `loop daemon stop` | `d down` | Stop and uninstall the daemon |
| `loop daemon status` | `d st` | Show daemon status |

### MCP Server Options

```sh
loop mcp --channel-id <id> --api-url <url>   # Attach to existing channel
loop mcp --dir <path> --api-url <url>         # Auto-create channel for directory
```

## Discord Commands

| Command | Description |
|---|---|
| `/loop ask <prompt>` | Ask the AI a question |
| `/loop register` | Register the current channel |
| `/loop unregister` | Unregister the current channel |
| `/loop schedule <schedule> <prompt> <type>` | Schedule a task (cron/interval/once) |
| `/loop tasks` | List scheduled tasks with status |
| `/loop cancel <task_id>` | Cancel a scheduled task |
| `/loop toggle <task_id>` | Toggle a scheduled task on or off |
| `/loop edit <task_id> [schedule] [type] [prompt]` | Edit a scheduled task |
| `/loop status` | Show bot status |

The bot also responds to `@mentions`, replies to its own messages, and messages prefixed with `!loop`.

## REST API

| Method | Endpoint | Description |
|---|---|---|
| `POST` | `/api/tasks` | Create a scheduled task |
| `GET` | `/api/tasks?channel_id=<id>` | List tasks for a channel |
| `PATCH` | `/api/tasks/{id}` | Update a task (enabled, schedule, type, prompt) |
| `DELETE` | `/api/tasks/{id}` | Delete a task |
| `POST` | `/api/channels` | Ensure/create a Discord channel for a directory |

## MCP Tools

| Tool | Description |
|---|---|
| `schedule_task` | Create a scheduled task (cron/interval/once) |
| `list_tasks` | List all scheduled tasks for this channel |
| `cancel_task` | Cancel a scheduled task by ID |
| `toggle_task` | Enable or disable a scheduled task by ID |
| `edit_task` | Edit a task's schedule, type, and/or prompt |

## Development

```sh
make test             # Run tests
make lint             # Run linter
make coverage-check   # Enforce 100% test coverage
make coverage         # Generate HTML coverage report
make clean            # Remove build artifacts
```
