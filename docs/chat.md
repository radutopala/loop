# Chat View & Messaging

The chat view is the primary interface for interacting with the Loop agent. It displays a conversation timeline with message bubbles, real-time streaming, agent activity indicators, and a multi-mode input area.

Related docs: [Layouts](layouts.md) | [Sidebar](sidebar.md) | [Settings](settings.md)

---

## Architecture

The `ChatView` component (`src/components/chat/ChatView.tsx`) receives a `ChatState` object as props. The state is managed by the `useChatState` hook, which is hoisted in `WorkspaceLayout` so the WebSocket connection and messages persist across layout tab switches. See [Layouts - Chat State Hoisting](layouts.md#chat-state-hoisting).

### ChatState Interface

```typescript
interface ChatState {
  messages: Message[];
  loading: boolean;
  loadMore: () => void;
  hasMore: boolean;
  addMessage: (msg: Message) => void;
  streamingContent: string | null;
  isRunning: boolean;
  toolActivity: { tool_name: string; input: string } | null;
  agentActivity: AgentActivityData | null;
  askUserQuestions: AskUserQuestionData | null;
  exitPlanRequest: ExitPlanModeData | null;
  todos: TodoWriteData | null;
  clearAskUser: () => void;
  clearExitPlan: () => void;
  mode: "agent" | "plan";
  setMode: (mode: "agent" | "plan") => void;
  completionInfo: { duration_ms?: number; num_turns?: number; stop_reason?: string; model?: string } | null;
  triggerContent: string | null;
}
```

The `isRunning` flag is scoped per agent run via a `run_id` tracked internally by the state hooks. When a scheduled task and a chat agent run concurrently on the same channel, each has a unique `run_id` — the task completing only clears its own running state, not the chat agent's.

---

## Message Rendering

### Bot Messages (Left-Aligned)

- Aligned to the left (`alignItems: "flex-start"`)
- No background color (transparent)
- Robot icon SVG in the header (antenna, eyes, mouth)
- Timestamp displayed next to the robot icon
- Rounded corners: `18px 18px 18px 4px` (bottom-left is sharp)

### User Messages (Right-Aligned)

- Aligned to the right (`alignItems: "flex-end"`)
- Dark background (`#2f2f2f`)
- No icon in header; timestamp shown below the message
- Rounded corners: `18px 18px 4px 18px` (bottom-right is sharp)
- When the agent is processing, the last user message shows an eyes indicator (processing indicator)
- Max width: 85% of the container

### Highlighted Messages

When jumping to a message from search, the target message receives:
- Background: `rgba(99, 102, 241, 0.15)` (indigo flash)
- Extra padding: `4px 8px`
- CSS transition: `background-color 0.5s ease`
- The highlight fades after 2 seconds via `setTimeout`

Each message has a `data-msg-id` attribute for scroll targeting.

---

## Markdown Support

Messages are rendered through the `MarkdownContent` component, which uses a custom line-by-line parser.

### Block-Level Elements

| Element | Syntax | Rendering |
|---------|--------|-----------|
| Fenced code block | ` ``` ` ... ` ``` ` | `<pre>` with `colors.surface` background, 8px border-radius, 13px monospace font |
| Language label | ` ```go ` | Shown above the code block in `colors.textDim`, 11px font |
| Paragraph | Any non-empty line | `<p>` with 2px vertical margin |
| Empty line | Blank line | `<br>` |

### Inline Elements

| Element | Syntax | Rendering |
|---------|--------|-----------|
| Inline code | `` `code` `` | `<code>` with `colors.surface` background, 3px border-radius, 13px monospace font |
| Bold | `**text**` | `<strong>` |
| Italic | `*text*` | `<em>` |

Inline elements are parsed via regex: `` /(`[^`]+`|\*\*[^*]+\*\*|\*[^*]+\*)/ ``

---

## Streaming

When the agent is generating a response, the chat view shows a streaming bubble:

1. A `message.streaming` event arrives via the WebSocket event stream with partial content.
2. The `StreamingBubble` component renders with:
   - Robot icon (same as bot messages)
   - Italic "streaming..." label next to the timestamp
   - Incrementally rendered markdown content
3. When the full message arrives (`message.created` with `is_bot: true`), the streaming content is cleared and the final message is added to the messages list.

---

## Agent Activity Indicators

While the agent is running, activity events are shown between the last message and the streaming bubble.

### Activity Types

| Activity | Icon | Display |
|----------|------|---------|
| `model` | Robot emoji | Model name (e.g., "claude-sonnet-4-20250514") |
| `subagent_started` | Link emoji | "Agent: " + description |
| `subagent_progress` | Magnifying glass emoji | Description text |

Activity text is truncated to **100 characters** maximum with "..." appended.

### Tool Use Indicator

When the agent invokes a tool (`tool.use` event), a separate indicator shows:
- Gear icon
- Tool name in bold
- Input summary (truncated to 80 characters)

The tool indicator is only shown when there is no streaming content and the agent is running.

---

## Completion Summary

After the agent finishes a run, a completion summary is displayed:

- Timer icon
- Parts joined with " . " separator:
  - Model name (if available)
  - Duration in seconds (e.g., "12.3s")
  - Turn count (e.g., "5 turns")
  - Stop reason (e.g., "end_turn")

The summary is only shown when the agent is not running and completion info is available.

---

## Chat Input

The input area (`ChatInput` component) sits at the bottom of the chat view.

### Layout

- Container: `max-width: 768px`, rounded (16px border-radius), `colors.surface` background
- Textarea: 3 rows, transparent background, 14px sans-serif font, no resize
- Mode toggle pill (left of send button)
- Send/Stop button (right)

### Mode Toggle

A pill-shaped segmented control with two options:

| Mode | Behavior |
|------|----------|
| **Agent** | Default mode. Message is sent as a regular prompt. |
| **Plan** | Message is sent with `mode: "plan"`. The agent plans but does not execute. |

The active segment has white background with black text; inactive has transparent background with dimmed text.

### Send Actions

| Action | Trigger |
|--------|---------|
| Send message | Press `Enter` (without Shift) |
| New line | Press `Shift+Enter` |
| Stop running agent | Click the Stop button (square icon with `colors.textDim` border) |

The send button:
- **Not running:** White circle with up-arrow icon. Disabled (40% opacity) when textarea is empty.
- **Running:** Transparent with dimmed border, contains a filled square (stop) icon.

After sending, the textarea is cleared and re-focused via `requestAnimationFrame`.

---

## Command Autocomplete

Typing `/` or `/loop` triggers command autocomplete.

### Trigger Logic

1. If the text starts with `/` and is a partial match for `/loop`, show all commands.
2. If the text starts with `/loop `, filter commands by partial subcommand name.
3. If the text already has a subcommand + arguments (space after subcommand), hide the picker.

### Available Commands

| Command | Description |
|---------|-------------|
| `/loop tasks` | List scheduled tasks |
| `/loop task <id>` | Show task details |
| `/loop schedule` | Schedule a new task |
| `/loop cancel <id>` | Cancel a task |
| `/loop toggle <id>` | Enable/disable a task |
| `/loop edit <id>` | Edit a task |
| `/loop status` | Check bot status |
| `/loop stop` | Stop active run |
| `/loop readme` | Show README |
| `/loop template-add <name>` | Add a template |
| `/loop template-list` | List templates |
| `/loop allow_user <id>` | Grant user access |
| `/loop deny_user <id>` | Revoke user access |
| `/loop iamtheowner` | Claim channel ownership |

### Dropdown UI

- Appears above the input (positioned with `bottom: 100%`)
- Dark sidebar background with border, 8px border-radius, shadow
- Max height: 280px with scrollable area
- Each item shows: command name (bold, 13px mono), description (12px sans), usage (11px mono, dimmed)
- Selected item: `colors.selectedBg` background

### Navigation

| Key | Action |
|-----|--------|
| `ArrowDown` | Move selection down |
| `ArrowUp` | Move selection up |
| `Tab` / `Enter` | Accept selected command |
| `Escape` | Close dropdown |

Accepting a command fills the textarea with `/loop <command> ` (with trailing space).

---

## @Mention Autocomplete

Typing `@` followed by a partial match for "LoopBot" triggers mention autocomplete.

### Trigger Conditions

- `@` must be at the start of text or preceded by a space/newline
- The partial text after `@` must be a case-insensitive prefix of "LoopBot"
- No spaces in the partial text

### Acceptance

Pressing `Tab` or `Enter` replaces the `@partial` text with `@LoopBot ` (with trailing space). The cursor is positioned after the space.

### UI

- Small dropdown positioned above the input
- Single item: `@LoopBot` with bold text
- Pre-selected with `colors.selectedBg` background

---

## Auto-Scroll

The chat view tracks whether the user is scrolled to the bottom.

### Behavior

- **Auto-scroll active:** New messages, streaming updates, tool activity, and agent activity automatically scroll the view to the bottom (smooth scroll).
- **Auto-scroll disabled:** If the user scrolls up (more than 40px from the bottom), auto-scroll is paused.
- **Re-enabling:** Scrolling back to the bottom re-enables auto-scroll. Sending a message calls `scrollToBottom()` which re-enables auto-scroll.

### Scroll Detection

```typescript
const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
autoScrollRef.current = atBottom;
```

---

## Load More

When the user scrolls to the top of the message list and more messages are available (`hasMore`), `loadMore()` is called automatically.

A "Load older messages" button is also shown above the message list as a fallback. While loading, the button text changes to "Loading...".

The `loadMore` function uses cursor-based pagination via the `useMessages` hook.

---

## Message Search Integration

The chat view accepts a `scrollToMessageId` prop from the [Command Palette](settings.md#command-palette).

### Flow

1. User searches in the Command Palette and clicks a message result.
2. `App` sets `scrollToMessageId` and selects the appropriate channel.
3. `ChatView` finds the element with `data-msg-id={scrollToMessageId}`.
4. The element is scrolled into view with `behavior: "smooth", block: "center"`.
5. Auto-scroll is disabled to prevent the view from jumping back.
6. The message is highlighted with an indigo background (fades after 2 seconds).
7. `onScrollComplete()` is called to clear the scroll target.

---

## Empty State

When there are no messages and loading is complete, the chat view shows:
- Centered Loop logo
- Full-width input area at the bottom
- Docker isolation label: "Running non-interactively in an isolated Docker container"

---

## Copy-on-Select

Selecting text anywhere in the workspace automatically copies it to the clipboard. A single top-level `useCopyOnSelect` hook (mounted in `WorkspaceLayout`) listens for `mouseup` events on the document and copies any non-empty selection to the clipboard. The hook:

- **Skips editable elements**: Selections inside `<textarea>` or `<input>` are not auto-copied (avoids interfering with chat input, form fields, etc.).
- **Supports xterm.js terminals**: For canvas-rendered terminal panels (Agent, Shell), the hook reads the selection via a `_xtermGetSelection` property set on the `.xterm` DOM element by `useXTerminal`.
- **Works on non-secure HTTP contexts**: Falls back to `document.execCommand("copy")` with a hidden textarea and `clipboardData.setData` when `navigator.clipboard` is unavailable (e.g., `host.docker.internal`).

---

## Chat Drafts

Unsent text in the chat input is automatically persisted to `localStorage` under the key `loop-chat-drafts`, keyed by channel ID. Drafts survive channel switches and app restarts.

| Event | Action |
|-------|--------|
| Typing | `draftText.set(channelId, text)` |
| Clearing input | `draftText.delete(channelId)` |
| Sending message | `draftText.delete(channelId)` |
| Mounting ChatInput | Restore draft via `draftText.get(channelId)`, cursor moved to end |
| Accepting command/mention | Draft updated to reflect accepted text |

---

## Message History (ArrowUp / ArrowDown)

The chat input supports shell-style message history navigation. Pressing ArrowUp cycles through previously sent messages; ArrowDown returns toward the current draft.

### Behavior

| Key | Condition | Action |
|-----|-----------|--------|
| `ArrowUp` | Cursor at position 0 | Save current text as draft, load previous sent message |
| `ArrowDown` | Cursor at end of text, in history mode | Load next sent message, or restore draft at the end |

### Seeding

On first ArrowUp press, if the local history buffer is empty, it is seeded from backend messages (`is_bot === false`) for the current channel. Subsequent presses use the local buffer.

### Implementation

History is stored in `useRef` arrays (`historyRef`, `historyIdxRef`, `draftRef`) to avoid unnecessary re-renders. Each sent message is appended to the history buffer before clearing the input. The index `-1` means "not in history mode."

---

## Message Queue Indicators

When multiple messages are sent while the agent is running, unprocessed messages are tracked and annotated with status labels.

### Processing State

Each user message has an `is_processed` flag. The first unprocessed user message is considered "currently being processed"; subsequent unprocessed messages are "queued".

| State | Label | Style |
|-------|-------|-------|
| Processing | `processing` | 10px dimmed text, pill badge below the message |
| Queued | `queued` | 10px dimmed text, pill badge below the message |

### Trigger Quote

When the agent is processing a message and there are queued messages (or there were queued messages in this batch), a `TriggerQuote` component is shown between the messages list and the agent activity indicators. It displays:

- A reply-arrow icon (SVG)
- The triggering message content (truncated to 120 characters)
- A timestamp (HH:MM format)

The trigger quote persists across channel switches via the chat state store and remains visible until all messages in the batch are processed.

---

## Docker Isolation Label

Below the input area, a small label reads: "Running non-interactively in an isolated Docker container" with a monitor icon. The icon stroke color changes to green (`#48bb78`) when the agent is running.
