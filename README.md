# Loop

A Discord bot powered by Claude that runs AI agents in Docker containers.

## Architecture

```
                   Discord
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
          │  → /work            │
          └──────────┬──────────┘
                     ▼
              Container (Docker)
          ┌─────────────────────┐
          │ claude --print      │
          │   /work (project)   │
          │   /mcp  (logs)      │
          │   MCP: loop
          └─────────┬───────────┘
                    │
         MCP tool calls (schedule, list, cancel…)
                    ▼
              API Server ◀──▶ SQLite
```

- **Orchestrator** coordinates message handling, channel registration, session management, and scheduled tasks
- **DockerRunner** mounts the channel's `dir_path` to `/work` (falling back to `~/.loop/<channelID>/work`), then runs `claude --print` inside a Docker container
- **Scheduler** polls for due tasks (cron, interval, once) and executes them via DockerRunner
- **MCP Server** (inside the container) gives Claude tools to schedule/manage tasks — calls loop back through the API server
- **API Server** exposes REST endpoints for task and channel management
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

### Using with Claude Code

`loop mcp` is the same MCP server used in both contexts:

- **On the host** — registered in your local Claude Code so you can schedule tasks from your IDE
- **Inside containers** — automatically injected into every agent container so scheduled tasks can themselves schedule follow-up tasks

When using `--dir`, Loop automatically registers a channel (and creates a Discord channel in the configured guild) for that directory. The project directory is then mounted to `/work` inside agent containers.

To register it in your local Claude Code, add to `.mcp.json` (project-level or `~/.claude/.mcp.json`):

```json
{
  "mcpServers": {
    "loop": {
      "command": "loop",
      "args": ["mcp", "--dir", "/path/to/your/project", "--api-url", "http://localhost:8222"]
    }
  }
}
```

## Discord Commands

| Command | Description |
|---|---|
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
