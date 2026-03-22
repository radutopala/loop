# Task Scheduling

Loop includes a built-in task scheduler that executes agent prompts on a recurring or one-time basis. Tasks are stored in the database, polled on a timer, and executed inside Docker containers.

See also: [Configuration Reference](configuration.md) for `poll_interval_sec` and `task_templates` config fields. [Docker Container Lifecycle](containers.md) for how task containers are created.

---

## Task Types

Each task has a `type` that determines how its `schedule` field is interpreted.

### Cron (`"cron"`)

Standard 5-field cron expressions, parsed by [robfig/cron](https://github.com/robfig/cron).

| Field | Values |
|---|---|
| Minute | `0-59` |
| Hour | `0-23` |
| Day of month | `1-31` |
| Month | `1-12` or `JAN-DEC` |
| Day of week | `0-6` or `SUN-SAT` |

Examples:
```
* * * * *        # every minute
0 17 * * *       # daily at 5:00 PM
0 9 * * MON-FRI  # weekdays at 9:00 AM
*/15 * * * *     # every 15 minutes
0 0 1 * *        # first day of each month at midnight
```

After each execution, the next run time is computed using `sched.Next(now)`.

### Interval (`"interval"`)

Go duration strings. The next run is calculated as `now + duration` after each execution.

Examples:
```
5m       # every 5 minutes
30m      # every 30 minutes
1h       # every hour
2h30m    # every 2 hours 30 minutes
24h      # every 24 hours
```

### Once (`"once"`)

RFC3339 timestamps for single-execution tasks. The task is automatically disabled (`enabled = false`) after execution.

Examples:
```
2026-03-15T14:30:00Z          # UTC
2026-03-15T10:30:00-04:00     # with timezone offset
```

---

## Task Lifecycle

### 1. Create

Tasks are created via:
- **Slash commands**: `/loop schedule` with `type`, `schedule`, and `prompt` options.
- **Templates**: `/loop template-add` loads a pre-defined template from config.
- **MCP tools**: `schedule_task` tool available to agents.

On creation:
- `calculateNextRun` computes the first `next_run_at` based on the task type and schedule.
- The task is stored in the database with `enabled = true`.

### 2. Poll

The scheduler runs a polling loop at a configurable interval (default 30 seconds, set via `poll_interval_sec`).

Each tick:
1. Queries the database for tasks where `next_run_at <= now AND enabled = true`.
2. Executes each due task sequentially.

### 3. Execute

For each due task:
1. A `TaskRunLog` record is inserted with status `"running"`.
2. The task executor retrieves the channel from the database to get the session ID and work directory.
3. An `AgentRequest` is built with:
   - The task's prompt as a user message.
   - A system prompt instructing the agent NOT to use `send_message`, `create_thread`, or `create_channel` MCP tools (responses are delivered automatically).
   - If `auto_delete_sec > 0`, the system prompt also instructs the agent to prefix "nothing to report" responses with `[EPHEMERAL]`.
4. The agent runs inside a Docker container (see [Containers](containers.md)).
5. The run log is updated to `"success"` or `"failed"` with the response or error text.

### 4. Update Next Run

After execution, the next run is determined by task type:

| Type | Behavior |
|---|---|
| `cron` | `next_run_at` set to the next matching time via the cron parser. |
| `interval` | `next_run_at` set to `now + duration`. |
| `once` | Task is disabled (`enabled = false`). No further runs. |

---

## Thread Creation for Scheduled Tasks

When streaming is enabled (default), task execution creates a thread for its output.

### Thread Reuse (Local Platform)

On the local platform (Electron app), recurring tasks (`cron`/`interval`) reuse the same thread across executions:

1. **First execution**: A new thread is created and its ID is stored in the task's `thread_id` column.
2. **Subsequent executions**: Messages are posted to the existing thread instead of creating a new one.
3. **Once tasks**: Always create a fresh thread (no reuse).

On Discord/Slack, a new thread is created for each execution (platform threads are ephemeral notification threads).

### Thread Naming

Thread names differ by platform:

| Platform | Format | Example |
|---|---|---|
| **Discord/Slack** | `⏱ task #<ID> (<schedule>) <prompt>` | `⏱ task #42 (*/5 * * * *) Check for new deployments...` |
| **Local** | `task #<ID> (<schedule>) <prompt>` | `task #42 (5m) Check for new deployments...` |

- The schedule is wrapped in backticks to prevent Slack/Discord markdown from mangling cron asterisks.
- The full string is truncated to 100 characters.
- The Electron sidebar shows a clock SVG icon for task threads (instead of the emoji).

### Thread Lifecycle

1. **First streaming turn**: A thread is created via `CreateSimpleThread` (no bot @mention to avoid re-triggering the agent).
2. **Subsequent turns**: Messages are sent to the thread.
3. **Final response**: Sent to the thread (or channel if thread creation failed). Duplicate detection prevents re-sending the last streamed turn.
4. **Thread channel record**: A DB channel record is upserted for the thread, inheriting the parent channel's guild, directory, platform, session, and permissions.
5. **Permission users invited**: All RBAC owner and member users are invited to the thread.
6. **UI notification**: A `channel_created` event is broadcast so the Electron app sidebar refreshes.

If thread creation fails, the executor falls back to sending messages directly to the parent channel.

### Sub-Thread Resolution

When an agent running inside a task thread (sub-thread) schedules or lists tasks, the API automatically resolves the channel up to the parent thread. This ensures tasks are always associated with the correct parent rather than being nested deeper.

---

## Auto-Deletion

Tasks with `auto_delete_sec > 0` support ephemeral execution:

### Ephemeral Marker

The agent is instructed to prefix responses with `[EPHEMERAL]` when there is nothing meaningful to report. The marker is:
- Stripped from the response text before delivery.
- Stripped from streaming turns before the stream tracker records them.

### Deletion Flow

When `auto_delete_sec > 0` and a thread was created:

1. If the response contains `[EPHEMERAL]`:
   - **Discord/Slack**: The thread is renamed, replacing `⏱` with `💨` (puff emoji) to visually indicate ephemeral status.
   - **Local**: The thread name is prefixed with `[ephemeral]`. The Electron sidebar shows an undo-arrow SVG icon.
2. A `time.AfterFunc` is scheduled with the configured delay.
3. After the delay, the thread is deleted via `bot.DeleteThread` and a `channel_deleted` event is broadcast to the UI.

This allows heartbeat-style tasks to create threads that auto-clean when there is nothing to report.

---

## Task Templates

Templates are pre-defined task configurations in the global config. They allow quick deployment of common tasks without typing full schedules and prompts.

### Template Fields

| Field | Description |
|---|---|
| `name` | Unique identifier. Used by `/loop template-add` and for deduplication. |
| `description` | Shown in `/loop template-list` output. |
| `schedule` | Cron expression, Go duration, or RFC3339 timestamp. |
| `type` | `"cron"`, `"interval"`, or `"once"`. |
| `prompt` | Inline prompt text. |
| `prompt_path` | File path resolved as `~/.loop/templates/{prompt_path}`. |
| `auto_delete_sec` | Auto-delete delay for the task's thread. |

### Prompt Resolution

Exactly one of `prompt` or `prompt_path` must be set:

- **`prompt`**: The text is used directly as the task prompt.
- **`prompt_path`**: The file at `~/.loop/templates/{prompt_path}` is read and its contents used as the prompt. This allows long, detailed prompts to be maintained as separate files.

Setting both or neither is an error.

### Template Loading

When `/loop template-add` is invoked:
1. The template is looked up by name in the config's `task_templates` array.
2. A check ensures no task with the same `template_name` already exists in the channel (prevents duplicates).
3. The prompt is resolved via `ResolvePrompt`.
4. A new `ScheduledTask` is created with the template's settings and `template_name` set for tracking.

### Template Merging (Project Config)

Project configs can define their own `task_templates`. Templates are merged by name:
- If a project template has the same `name` as a global template, the project version replaces it.
- New template names are appended to the list.

---

## Task Management Commands

All commands are available as slash commands (`/loop <command>`) and MCP tools.

### Create

```
/loop schedule type:<cron|interval|once> schedule:<expression> prompt:<text>
```

Creates a new task. The scheduler calculates `next_run_at` and enables the task immediately.

### List

```
/loop tasks
```

Lists all tasks for the current channel with their ID, type, status (enabled/disabled), schedule, prompt (truncated to 80 chars), next run time (as relative duration), and auto-delete setting if configured.

### Show

```
/loop task task_id:<id>
```

Shows full details of a single task: type, schedule, status, next run, template name (if from a template), auto-delete setting, and the complete prompt text.

### Cancel

```
/loop cancel task_id:<id>
```

Permanently deletes a task from the database.

### Toggle

```
/loop toggle task_id:<id>
```

Flips a task's enabled state. Disabled tasks are skipped during poll cycles but remain in the database.

### Edit

```
/loop edit task_id:<id> [schedule:<expr>] [type:<type>] [prompt:<text>]
```

Updates one or more fields of an existing task. If `schedule` or `type` changes, `next_run_at` is recalculated. At least one field must be provided.

### Load Template

```
/loop template-add name:<template_name>
```

Creates a task from a configured template. Prevents duplicate loading of the same template in a channel.

### List Templates

```
/loop template-list
```

Shows all configured templates with their name, type, schedule, and description.

---

## Poll Interval

The scheduler polls the database on a fixed interval, configured via `poll_interval_sec` (default: 30 seconds). This means:

- Tasks may execute up to `poll_interval_sec` seconds after their scheduled time.
- Very short intervals (e.g. `5s`) increase database load.
- The poll loop uses `time.NewTicker` and stops cleanly on context cancellation.

---

## Run Logging

Every task execution is recorded in a `TaskRunLog`:

| Field | Description |
|---|---|
| `task_id` | The task that was executed. |
| `status` | `"running"`, `"success"`, or `"failed"`. |
| `response_text` | Agent response on success. |
| `error_text` | Error message on failure. |
| `started_at` | When execution began. |
| `finished_at` | When execution completed. |

The log is created with `"running"` status before execution starts and updated to the final status after the agent returns.
