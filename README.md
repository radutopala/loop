# Loop

A Discord bot powered by Claude that runs AI agents in Docker containers.

## Architecture

```
Discord ─▶ Bot ─▶ Orchestrator ─▶ DockerRunner ─▶ Container (claude --print)
                       │
                   Scheduler ◀─▶ SQLite
```

- **Orchestrator** coordinates message handling, channel registration, session management, and scheduled tasks
- **DockerRunner** executes `claude --print` directly inside Docker containers — plain text in, plain text out
- **Scheduler** runs cron, interval, and one-shot tasks
- **SQLite** stores channels, messages, scheduled tasks, and run logs

## Setup

### Prerequisites

- Go 1.25+
- Docker
- A Discord bot token and application ID
- Claude Code CLI (`@anthropic-ai/claude-code`)

### Discord Bot Setup

1. Create an application at https://discord.com/developers/applications
2. Copy the **Application ID** and **Bot Token** into your `.env` file
3. Invite the bot to your server using this OAuth2 URL (replace `YOUR_APP_ID`):

   ```
   https://discord.com/oauth2/authorize?client_id=YOUR_APP_ID&scope=bot%20applications.commands&permissions=68608
   ```

4. After the bot is running, use `/loop register` in each channel where you want it to respond

### Configuration

Create `~/.loop/.env`:

```env
# Required
DISCORD_TOKEN=your-discord-bot-token
DISCORD_APP_ID=your-discord-app-id

# Optional (defaults shown)
CLAUDE_BIN_PATH=claude
DB_PATH=loop.db
LOG_LEVEL=info
LOG_FORMAT=text
CONTAINER_IMAGE=loop-agent:latest
CONTAINER_TIMEOUT_SEC=300
CONTAINER_MEMORY_MB=512
CONTAINER_CPUS=1.0
POLL_INTERVAL_SEC=30
MOUNT_ALLOWLIST=/path/one,/path/two
```

Environment variables override values from the `.env` file.

### Build

```sh
# Build the loop binary
make build

# Build the Docker agent image
make docker-build
```

### Run

```sh
loop serve
```

## Discord Commands

| Command | Description |
|---|---|
| `/loop ask <prompt>` | Ask the AI a question |
| `/loop register` | Register the current channel |
| `/loop unregister` | Unregister the current channel |
| `/loop schedule <schedule> <prompt> <type>` | Schedule a task (cron/interval/once) |
| `/loop tasks` | List scheduled tasks |
| `/loop cancel <task_id>` | Cancel a scheduled task |
| `/loop status` | Show bot status |

The bot also responds to `@mentions`, replies to its own messages, and messages prefixed with `!loop`.

## Development

```sh
# Run tests
make test

# Run linter
make lint

# Check 100% test coverage
make coverage-check

# Generate HTML coverage report
make coverage
```
