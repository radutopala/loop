---
title: Slash Commands & Interactions
---

# Slash Commands & Interactions

Loop provides slash commands for task management, bot control, and permission management. Commands are available on all platforms but are invoked differently depending on the platform.

## Command Invocation by Platform

| Platform | Invocation Method | Example |
|---|---|---|
| Discord | Native slash commands (`/loop <subcommand>`) | `/loop schedule` opens Discord's autocomplete UI |
| Slack | Slash command text parsing (`/loop <text>`) | `/loop schedule "0 9 * * *" cron Check updates` |
| Local (API) | HTTP POST to `/api/commands` | `{"channel_id": "abc", "command": "tasks"}` |

### Discord

Discord commands are registered as a single top-level `/loop` application command with subcommands and subcommand groups. The `Commands()` function in `internal/discord/commands.go` defines the full command tree.

Discord slash commands must be acknowledged within 3 seconds. The bot immediately responds with `DeferredChannelMessageWithSource`, stores the interaction object in `pendingInteractions[channelID]`, then dispatches the parsed interaction to the orchestrator. When the orchestrator replies via `SendMessage`, the pending interaction is resolved with `InteractionResponseEdit` instead of sending a regular message.

Subcommand groups (like `template`) produce hyphenated command names internally: `template add` becomes `template-add`, `template list` becomes `template-list`.

### Slack

Slack uses Socket Mode to receive `/loop` slash commands. The raw text after `/loop` is parsed by `parseSlashCommand` into an `Interaction` struct. If parsing fails, help text or an error message is posted back to the channel.

The Slack parser handles platform-specific differences:

- Role-based permission commands (`allow role`, `deny role`) are explicitly unsupported on Slack with a descriptive error message.
- User IDs must be in Slack mention format: `<@U123456>` or `<@U123456|username>`. The `extractUserID` function strips the angle brackets and optional display name.
- The `schedule` command uses the type keyword (`cron`, `interval`, `once`) as a positional delimiter, since cron expressions contain spaces.
- The `edit` command uses `--flag value` syntax for optional parameters, with `--prompt` consuming all remaining text.

### Local (API)

The Electron app sends commands via `POST /api/commands` with a JSON body:

```json
{
  "channel_id": "abc123",
  "author_id": "local-user",
  "command": "schedule type=cron schedule=\"0 9 * * *\" prompt=\"Check updates\""
}
```

The `parseCommand` function tokenizes the command string (respecting quoted strings), then maps the subcommand name to its argument format:

- **No-arg commands** -- `tasks`, `status`, `readme`, `template-list`, `iamtheowner`
- **Single positional** -- `task <task_id>`, `cancel <task_id>`, `toggle <task_id>`, `template-add <name>`
- **Optional positional** -- `stop [channel_id]`
- **Key=value pairs** -- `schedule type=cron schedule="0 9 * * *" prompt="Do something"`
- **Positional + key=value** -- `edit <task_id> schedule="new" prompt="updated"`
- **Multiple positionals** -- `allow_user <target_id> [role]`, `deny_user <target_id>`

The `author_id` defaults to `"local-user"` if not provided.

## Command Reference

### schedule

Create a scheduled task that runs an agent at specified intervals.

**Permission:** Member or Owner

**Parameters:**

| Parameter | Required | Description |
|---|---|---|
| `schedule` | Yes | Schedule expression: cron (`0 9 * * *`), duration (`1h`, `30m`), or ISO 8601 timestamp |
| `type` | Yes | One of: `cron`, `interval`, `once` |
| `prompt` | Yes | The prompt to execute when the task fires |

**Examples:**

```
Discord: /loop schedule schedule:0 9 * * * type:cron prompt:Check for updates
Slack:   /loop schedule "0 9 * * *" cron Check for updates
API:     schedule type=cron schedule="0 9 * * *" prompt="Check for updates"
```

```
Slack:   /loop schedule 1h interval Run health check
Slack:   /loop schedule 2026-02-15T10:00:00Z once Deploy release
```

**Response:** `Task scheduled (ID: <N>).`

### tasks

List all scheduled tasks for the current channel.

**Permission:** Member or Owner

**Parameters:** None

**Example:**

```
Discord: /loop tasks
Slack:   /loop tasks
API:     tasks
```

**Response:** A formatted list showing each task's ID, type, status (enabled/disabled), schedule expression, truncated prompt (80 chars), next run time, and auto-delete setting if configured.

### task

Show full details of a specific scheduled task.

**Permission:** Member or Owner

**Parameters:**

| Parameter | Required | Description |
|---|---|---|
| `task_id` | Yes | Integer ID of the task |

**Example:**

```
Discord: /loop task task_id:42
Slack:   /loop task 42
API:     task 42
```

**Response:** Detailed task information including type, schedule, status, next run, template name (if applicable), auto-delete setting, and the full prompt text.

### cancel

Delete a scheduled task permanently.

**Permission:** Member or Owner

**Parameters:**

| Parameter | Required | Description |
|---|---|---|
| `task_id` | Yes | Integer ID of the task to cancel |

**Example:**

```
Discord: /loop cancel task_id:42
Slack:   /loop cancel 42
API:     cancel 42
```

**Response:** `Task <N> cancelled.`

### toggle

Enable or disable a scheduled task without deleting it.

**Permission:** Member or Owner

**Parameters:**

| Parameter | Required | Description |
|---|---|---|
| `task_id` | Yes | Integer ID of the task to toggle |

**Example:**

```
Discord: /loop toggle task_id:42
Slack:   /loop toggle 42
API:     toggle 42
```

**Response:** `Task <N> enabled.` or `Task <N> disabled.`

### edit

Modify one or more properties of an existing scheduled task.

**Permission:** Member or Owner

**Parameters:**

| Parameter | Required | Description |
|---|---|---|
| `task_id` | Yes | Integer ID of the task to edit |
| `schedule` | No | New schedule expression |
| `type` | No | New type: `cron`, `interval`, or `once` |
| `prompt` | No | New prompt text |

At least one of `schedule`, `type`, or `prompt` must be provided.

**Examples:**

```
Discord: /loop edit task_id:42 schedule:0 */2 * * * prompt:Updated check
Slack:   /loop edit 42 --schedule "0 */2 * * *" --prompt Updated check
API:     edit 42 schedule="0 */2 * * *" prompt="Updated check"
```

On Slack, `--prompt` consumes all remaining text after it. On the API, the value is a single quoted token.

**Response:** `Task <N> updated.`

### status

Check if the bot is running.

**Permission:** Member or Owner

**Parameters:** None

**Example:**

```
Discord: /loop status
Slack:   /loop status
API:     status
```

**Response:** `Loop bot is running.`

### stop

Cancel the currently running agent in the channel. The stop button in the chat UI also triggers this command.

**Permission:** Member or Owner

**Parameters:**

| Parameter | Required | Description |
|---|---|---|
| `channel_id` | No | Target channel (defaults to the current channel; used by stop button with run-specific channel IDs) |

**Example:**

```
Discord: /loop stop
Slack:   /loop stop
API:     stop
```

**Response:** No explicit reply -- the active run is cancelled and sends "Run stopped." itself. If no run is active: `No active run to stop.`

### readme

Display the embedded README documentation.

**Permission:** Member or Owner

**Parameters:** None

**Example:**

```
Discord: /loop readme
Slack:   /loop readme
API:     readme
```

**Response:** The full README content from `internal/readme/embed.go`.

### template-add

Load a preconfigured task template into the current channel. Templates are defined in the config file under `task_templates`.

**Permission:** Member or Owner

**Parameters:**

| Parameter | Required | Description |
|---|---|---|
| `name` | Yes | Template name as defined in config |

**Example:**

```
Discord: /loop template add name:daily-standup
Slack:   /loop template add daily-standup
API:     template-add daily-standup
```

**Behavior:**

1. Look up the template by name in `cfg.TaskTemplates`.
2. Check if a task with this template name already exists in the channel (prevents duplicates).
3. Resolve the prompt text -- either from `prompt` field directly or by reading `{loopDir}/templates/{prompt_path}`.
4. Create a scheduled task with the template's schedule, type, prompt, and auto-delete settings.

**Response:** `Template '<name>' loaded (task ID: <N>).` or error if unknown, already loaded, or prompt resolution fails.

### template-list

List all available task templates from the config.

**Permission:** Member or Owner

**Parameters:** None

**Example:**

```
Discord: /loop template list
Slack:   /loop template list
API:     template-list
```

**Response:** A formatted list showing each template's name, type, schedule, and description.

### allow_user

Grant a user a role (owner or member) in the current channel's database permissions.

**Permission:** Owner only

**Parameters:**

| Parameter | Required | Description |
|---|---|---|
| `target_id` | Yes | User ID to grant access (Discord user or Slack `<@U...>`) |
| `role` | No | Role to grant: `owner` or `member` (default: `member`) |

**Example:**

```
Discord: /loop allow_user target_id:@SomeUser role:owner
Slack:   /loop allow user <@U123456> owner
API:     allow_user U123456 owner
```

**Behavior:** The target is first removed from all permission lists (owners and members), then added to the specified role's list. This ensures a user has exactly one role.

**Response:** `<mention> granted <role> role.`

### allow_role

Grant a Discord role access to the current channel. Discord-only.

**Permission:** Owner only

**Parameters:**

| Parameter | Required | Description |
|---|---|---|
| `target_id` | Yes | Discord role ID |
| `role` | No | Role to grant: `owner` or `member` (default: `member`) |

**Example:**

```
Discord: /loop allow_role target_id:@Developers role:member
```

**Response:** `Role <@&ROLE_ID> granted <role> role.`

Slack returns: `allow role is Discord-only (role-based permissions are not supported on Slack)`

### deny_user

Remove a user's database-granted role from the current channel.

**Permission:** Owner only

**Parameters:**

| Parameter | Required | Description |
|---|---|---|
| `target_id` | Yes | User ID to remove |

**Example:**

```
Discord: /loop deny_user target_id:@SomeUser
Slack:   /loop deny user <@U123456>
API:     deny_user U123456
```

**Behavior:** The target is removed from both the owners and members lists in the database permissions. Config-file permissions are not affected.

**Response:** `<mention> removed from channel permissions.`

### deny_role

Remove a Discord role's database-granted access from the current channel. Discord-only.

**Permission:** Owner only

**Parameters:**

| Parameter | Required | Description |
|---|---|---|
| `target_id` | Yes | Discord role ID to remove |

**Example:**

```
Discord: /loop deny_role target_id:@Developers
```

**Response:** `Role <@&ROLE_ID> removed from channel permissions.`

### iamtheowner

Claim ownership of the current channel. This command only works during initial setup when no permissions are configured anywhere (config file or database).

**Permission:** Bypasses normal permission checks; validates bootstrap mode internally.

**Parameters:** None

**Example:**

```
Discord: /loop iamtheowner
Slack:   /loop iamtheowner
API:     iamtheowner
```

**Behavior:**

1. Check that both config permissions and database permissions are empty (bootstrap mode).
2. If an owner is already configured, reject with: `An owner is already configured. Use /loop allow_user to manage permissions.`
3. If the channel is not registered, reject with: `Channel not registered.`
4. Add the invoking user to the channel's database permissions as an owner.

**Response:** `<@AUTHOR_ID> is now the owner of this channel.`

## Permission Requirements Summary

| Command | Required Role |
|---|---|
| `schedule` | Member or Owner |
| `tasks` | Member or Owner |
| `task` | Member or Owner |
| `cancel` | Member or Owner |
| `toggle` | Member or Owner |
| `edit` | Member or Owner |
| `status` | Member or Owner |
| `stop` | Member or Owner |
| `readme` | Member or Owner |
| `template-add` | Member or Owner |
| `template-list` | Member or Owner |
| `allow_user` | Owner only |
| `allow_role` | Owner only |
| `deny_user` | Owner only |
| `deny_role` | Owner only |
| `iamtheowner` | Special (bootstrap bypass) |

Users with no role assigned are rejected with: `You don't have permission to use this command.`

## Interaction Flow

The unified interaction handling flow in the orchestrator:

1. **Load channel** -- Get the channel from the database (may be nil for unregistered channels).
2. **Resolve permissions** -- Load config permissions for the channel's `DirPath`, get DB permissions from the channel record.
3. **Resolve role** -- Merge config and DB permissions via `resolveRole`. If the platform is Local and the role is empty, auto-grant Owner.
4. **Permission gate** -- Permission commands (`allow_user`, `allow_role`, `deny_user`, `deny_role`) require Owner. `iamtheowner` bypasses checks (validated internally). All other commands require any role (Member or Owner). No role = rejected.
5. **Dispatch** -- Route to the command-specific handler based on `inter.CommandName`.
6. **Reply** -- The handler sends a response via `sendReply`, which calls `bot.SendMessage` and stores the message in the database + broadcasts via EventsHub.

## Related Documentation

- [Platform Support](platforms.md) -- How each platform invokes and acks commands
- [Orchestrator & Message Processing](orchestrator.md) -- How `HandleInteraction` fits in the overall flow
- [Permission & RBAC System](permissions.md) -- Full role resolution logic
