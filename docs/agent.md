# Agent Types

Core request/response types and message structures for agent execution. Serves as the data model layer between the orchestrator and the container runner.

**Package:** `internal/agent`

## Types

### AgentRequest

Input sent to the agent runner.

| Field | Type | Description |
|-------|------|-------------|
| `SessionID` | `string` | Persistent session ID for resuming context |
| `ForkSession` | `bool` | Fork session on first thread message (thread isolation) |
| `Messages` | `[]AgentMessage` | Conversation history (oldest-first) |
| `SystemPrompt` | `string` | Custom system prompt override |
| `ChannelID` | `string` | Chat channel or thread ID |
| `AuthorID` | `string` | ID of user who triggered the request |
| `DirPath` | `string` | Working directory for the container |
| `ParentDirPath` | `string` | Parent working directory (for worktree containers) |
| `PlanMode` | `bool` | Enable plan mode |
| `Prompt` | `string` | Current user prompt |
| `OnTurn` | `func(string)` | Callback for each assistant text turn during streaming |
| `OnToolUse` | `func(name, input string)` | Callback for each tool invocation |
| `OnActivity` | `func(activity, detail string)` | Callback for model detection and system events |

### AgentMessage

Single message in conversation history.

| Field | Type | Description |
|-------|------|-------------|
| `Role` | `string` | `"user"` or `"assistant"` |
| `Content` | `string` | Message text |

### AgentResponse

Output returned by the agent runner.

| Field | Type | Description |
|-------|------|-------------|
| `Response` | `string` | Final text response |
| `SessionID` | `string` | Updated session ID for resuming next turn |
| `Error` | `string` | Error message if run failed |
| `DurationMs` | `int` | Total execution time |
| `NumTurns` | `int` | Number of assistant turns |
| `StopReason` | `string` | Why the agent stopped (e.g. `"end_turn"`) |
| `Model` | `string` | Model used (e.g. `"claude-opus-4-6"`) |

## BuildPrompt

`AgentRequest.BuildPrompt()` returns the effective prompt for the request:

- **Session resume with prompt** — returns only the new `Prompt` (avoids redundancy)
- **Session resume with messages** — returns only the last message's content
- **No session** — returns all messages formatted as `"role: content\n"`

## Callback-Driven Streaming

All callbacks are optional (nil-safe):

- `OnTurn` — fired on each assistant text turn (used for streaming to chat)
- `OnToolUse` — fired on each tool invocation (e.g. Bash, Read, Edit)
- `OnActivity` — fired for model detection and subagent progress

## Usage

| Consumer | Purpose |
|----------|---------|
| `internal/container/runner.go` | Executes agent in Docker via `Runner.Run(*AgentRequest) → *AgentResponse` |
| `internal/orchestrator/orchestrator.go` | Builds requests from message history, executes with streaming |
| `internal/orchestrator/executor.go` | Executes scheduled tasks, creates threads on first turn |

## Related docs

- [Containers](containers.md) — Docker container lifecycle
- [Orchestrator](orchestrator.md) — Message processing pipeline
- [Scheduling](scheduling.md) — Task execution
