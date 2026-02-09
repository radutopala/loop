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
```

- **Orchestrator** coordinates message handling, channel registration, session management, and scheduled tasks
- **DockerRunner** mounts the channel's `dir_path` (falling back to `~/.loop/<channelID>/work`) at its original path inside the container, then runs `claude --print`
- **Scheduler** polls for due tasks (cron, interval, once) and executes them via DockerRunner
- **MCP Server** (inside the container) gives Claude tools to schedule/manage tasks — calls loop back through the API server
- **API Server** exposes REST endpoints for task and channel management
- **SQLite** stores channels, messages, scheduled tasks, and run logs

## Setup

### Prerequisites

**macOS is recommended.** The `loop daemon:start/stop/status` commands use launchd (macOS-only) to manage the background service. `loop serve` works on any platform but requires manual process management on Linux.

**Host (build & run):**

- macOS (recommended) or Linux
- [Go 1.25+](https://go.dev/dl/)
- [Docker Desktop](https://docs.docker.com/desktop/) (macOS) or [Docker Engine](https://docs.docker.com/engine/install/) (Linux)
- A Discord bot token and application ID
- An Anthropic API key or Claude Code OAuth token (for agent containers)

**Container image (bundled automatically via `make docker-build`):**

- [Claude Code CLI](https://www.npmjs.com/package/@anthropic-ai/claude-code) (`@anthropic-ai/claude-code`)
- Go 1.25+ toolchain (for agent code execution)
- Node.js & npm (Claude Code runtime)
- Docker CLI (for Docker-in-Docker via socket mount)
- git, bash, curl

### Discord Bot Setup

1. Create an application at https://discord.com/developers/applications
2. Copy the **Application ID** and **Bot Token** into your `config.json`
3. Invite the bot to your server using this OAuth2 URL (replace `YOUR_APP_ID`):

   ```
   https://discord.com/oauth2/authorize?client_id=YOUR_APP_ID&scope=bot%20applications.commands&permissions=68624
   ```

### Configuration

Initialize your configuration with the onboard commands:

```sh
loop onboard:global          # Initialize ~/.loop/config.json
loop onboard:local           # Register Loop MCP server in current project's .mcp.json
```

`onboard:global` creates `~/.loop/config.json` from the embedded example config. Edit it to add your Discord credentials.

Alternatively, manually create `~/.loop/config.json` (HJSON — comments and trailing commas are allowed):

```jsonc
{
  // Required
  "discord_token": "your-discord-bot-token",
  "discord_app_id": "your-discord-app-id",

  // Optional — enables auto-creation of Discord channels via `loop mcp --dir`
  "discord_guild_id": "your-discord-guild-id",

  // Optional (defaults shown)
  "log_level": "debug",
  "log_format": "text",
  "container_image": "loop-agent:latest",
  "container_timeout_sec": 300,
  "container_memory_mb": 512,
  "container_cpus": 1.0,
  "poll_interval_sec": 30,
  // "claude_model": "claude-sonnet-4-5-20250929",  // Override Claude model (overridable in project config)

  // Mounts for all containers (optional)
  // Format: "host_path:container_path" or "host_path:container_path:ro"
  "mounts": [
    "~/.claude:~/.claude",                     // Writable - for Claude sessions
    "~/.gitconfig:~/.gitconfig:ro",            // Read-only - for git identity
    "~/.ssh:~/.ssh:ro",                        // Read-only - for SSH keys
    "~/.aws:~/.aws",                           // Writable - for AWS credentials and cache
    "/var/run/docker.sock:/var/run/docker.sock" // Docker access
  ]
}
```

### Container Mounts

The `mounts` configuration allows you to mount host directories into all agent containers. This enables:

- **Session Portability**: Share Claude sessions between host and containers by mounting `~/.claude`
- **Git Identity**: Inherit git config (user.name, user.email) from the host by mounting `~/.gitconfig`
- **SSH Access**: Use host SSH keys for git operations by mounting `~/.ssh`
- **Docker Access**: Run Docker commands inside containers by mounting `/var/run/docker.sock` (the host socket's GID is auto-detected and added to the container process)

Mount format: `"host_path:container_path[:mode]"` where mode is `ro` for read-only (optional).

Paths starting with `~/` are automatically expanded to the user's home directory. Non-existent paths are silently skipped.

**Important**: Project directories (`workDir`) and MCP logs (`mcpDir`) are automatically mounted at their actual paths, ensuring Claude sessions reference correct absolute paths.

### Per-Project Configuration

You can override mounts and MCP servers on a per-project basis by creating a `.loop/config.json` file in your project directory. This allows each project to have its own custom tooling, data mounts, and MCP servers.

**Project Config** (`{project}/.loop/config.json`):

```jsonc
{
  // Project-specific mounts (appended to main config mounts)
  "mounts": [
    "./data:/app/data",              // Relative paths resolved to project dir
    "./logs:/app/logs:ro",           // Read-only mode supported
    "/absolute/path:/app/external"   // Absolute paths work too
  ],

  // Project-specific MCP servers (merged with main config)
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

**Merge Behavior:**

- **Mounts**: Project mounts are **appended** to main config mounts. All mounts from both configs are applied to the container.
- **MCP Servers**: Project MCP servers are **merged** with main config servers. If a project defines an MCP server with the same name as the main config, the **project version takes precedence**.
- **Path Resolution**: Relative paths in project mounts (e.g., `./data`) are automatically resolved relative to the project directory.
- **Claude Model**: Project config can override `claude_model` to use a different model per project.
- **Security**: Only `mounts`, `mcp`, and `claude_model` can be set in project config. Critical settings (Discord token, API settings, etc.) cannot be overridden.

**Use Cases:**

1. **Project-specific databases**: Each project can have its own database MCP server configuration
2. **Project data mounts**: ML projects mount models/datasets, web projects mount public assets
3. **Development tools**: Mount project-specific tools, configs, or cache directories
4. **Monorepo support**: Each service subdirectory can have its own tools and mounts

**Example Scenarios:**

```jsonc
// Machine Learning Project
{
  "mounts": [
    "./models:/app/models:ro",     // Pre-trained models
    "./datasets:/app/data:ro",     // Training data
    "./outputs:/app/outputs"       // Model outputs
  ]
}

// Web Development Project
{
  "mounts": [
    "./public:/app/public:ro",     // Static assets
    "./.env:/app/.env:ro"          // Environment variables
  ],
  "mcp": {
    "servers": {
      "postgres": {
        "command": "npx",
        "args": ["-y", "@modelcontextprotocol/server-postgres"],
        "env": {"DATABASE_URL": "postgresql://localhost/webapp"}
      }
    }
  }
}
```

### Container Image

The agent Docker image is auto-built on first `loop serve` / `loop daemon:start` if it doesn't exist. The Dockerfile and entrypoint are embedded in the binary and flushed to `~/.loop/container/` during `loop onboard:global`.

You can customize the container by editing `~/.loop/container/Dockerfile` and `~/.loop/container/entrypoint.sh`. To trigger a rebuild, remove the existing image (`docker rmi loop-agent:latest`) and restart the daemon.

For **development** (building from local source), use `make docker-build` which uses `container/Dockerfile`.

### Build

```sh
# Build the loop binary
make build

# Install to GOPATH/bin
make install

# Build the Docker agent image (dev, from local source)
make docker-build
```

### Run

```sh
# Run directly (auto-builds image if missing)
loop serve

# Or run as a background daemon
loop daemon:start
loop daemon:status
loop daemon:stop

# Shortcut: reinstall + restart daemon
make restart
```

## CLI Commands

| Command | Aliases | Description |
|---|---|---|
| `loop serve` | `s` | Start the Discord bot |
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

When using `--dir`, Loop automatically registers a channel (and creates a Discord channel in the configured guild) for that directory. The project directory is then mounted at its original path inside agent containers.

To register it in your local Claude Code, run `loop onboard:local` in your project directory. This writes a `.mcp.json` file that Claude Code auto-discovers:

```sh
cd /path/to/your/project
loop onboard:local
# optionally: loop onboard:local --api-url http://custom:9999
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
| `/loop template add <name>` | Load a task template into the current channel |
| `/loop template list` | List available task templates from config |

The bot also responds to `@mentions`, replies to its own messages, and messages prefixed with `!loop`.

### Task Templates

The config.json file can include a `task_templates` array with reusable task patterns. Use `/loop template add <name>` in Discord to load a template as a scheduled task in the current channel. Templates are idempotent — adding the same template twice to a channel is a no-op.

Example templates in config.json:

```jsonc
{
  // ... other config ...

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
    }
  ]
}
```

To use a template, copy its schedule, type, and prompt values into the `/loop schedule` command in Discord.

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

## License

This project is licensed under the [GNU General Public License v3.0](LICENSE).
